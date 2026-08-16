// Companion Manager 的 tick 边界编排测试：端到端任务事件序列、FIFO 顺序、
// QueueFull 同步拒绝不调模型、慢 HTTP 不阻塞权威 tick、每伙伴单在途与全服
// 四并发、过时结果丢弃、路径不可达与世界时间超时、关服顺序与 Memory/TCP
// parity。全部使用 httptest 假模型，绝不访问真实模型服务。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/storage"
)

// fakeCompanionModel 是 httptest 假模型：按配置返回固定 go_to 计划，可整体
// 阻塞全部在途请求，并统计请求数、峰值并发与 context 取消次数。
type fakeCompanionModel struct {
	mu          sync.Mutex
	requests    int
	inFlight    int
	peak        int
	cancels     int
	block       chan struct{}
	steps       [][3]int32
	status      int
	server      *httptest.Server
	cancelOrder *shutdownOrderLog
}

func newFakeCompanionModel(t *testing.T, steps ...[3]int32) *fakeCompanionModel {
	t.Helper()
	model := &fakeCompanionModel{steps: steps}
	model.server = httptest.NewServer(http.HandlerFunc(model.handle))
	t.Cleanup(model.server.Close)
	return model
}

func (model *fakeCompanionModel) handle(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, 1<<20))
	model.mu.Lock()
	model.requests++
	model.inFlight++
	if model.inFlight > model.peak {
		model.peak = model.inFlight
	}
	block := model.block
	status := model.status
	steps := model.steps
	model.mu.Unlock()
	defer func() {
		model.mu.Lock()
		model.inFlight--
		model.mu.Unlock()
	}()
	if block != nil {
		select {
		case <-block:
		case <-r.Context().Done():
			model.mu.Lock()
			model.cancels++
			cancels := model.cancels
			model.mu.Unlock()
			if model.cancelOrder != nil && cancels == 1 {
				model.cancelOrder.record("model-cancel")
			}
			return
		}
	}
	if status != 0 {
		w.WriteHeader(status)
		return
	}
	content := planContentJSON(steps)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"choices": []map[string]any{
			{"message": map[string]string{"role": "assistant", "content": content}},
		},
	})
}

// planContentJSON 构造嵌套在 chat envelope 里的受限计划 JSON 文本。
func planContentJSON(steps [][3]int32) string {
	type wireStep struct {
		Kind string `json:"kind"`
		X    int32  `json:"x"`
		Y    int32  `json:"y"`
		Z    int32  `json:"z"`
	}
	type wirePlan struct {
		Summary string     `json:"summary"`
		Steps   []wireStep `json:"steps"`
	}
	plan := wirePlan{Summary: "按指令移动", Steps: make([]wireStep, 0, len(steps))}
	for _, step := range steps {
		plan.Steps = append(plan.Steps, wireStep{Kind: "go_to", X: step[0], Y: step[1], Z: step[2]})
	}
	encoded, _ := json.Marshal(plan)
	return string(encoded)
}

func (model *fakeCompanionModel) holdRequests() {
	model.mu.Lock()
	model.block = make(chan struct{})
	model.mu.Unlock()
}

func (model *fakeCompanionModel) releaseRequests() {
	model.mu.Lock()
	block := model.block
	model.block = nil
	model.mu.Unlock()
	if block != nil {
		close(block)
	}
}

func (model *fakeCompanionModel) snapshotCounts() (requests, peak, inFlight, cancels int) {
	model.mu.Lock()
	defer model.mu.Unlock()
	return model.requests, model.peak, model.inFlight, model.cancels
}

// shutdownOrderLog 记录关服期间跨组件事件的相对顺序。
type shutdownOrderLog struct {
	mu     sync.Mutex
	events []string
}

func (log *shutdownOrderLog) record(event string) {
	log.mu.Lock()
	log.events = append(log.events, event)
	log.mu.Unlock()
}

func (log *shutdownOrderLog) snapshot() []string {
	log.mu.Lock()
	defer log.mu.Unlock()
	return slices.Clone(log.events)
}

// newCompanionManagerHost 构造启用了任务编排的 Host；model 非 nil 时把模型
// endpoint 指向假模型，否则保持 hostTestConfig 的 loopback 缺省。
func newCompanionManagerHost(
	t *testing.T,
	definitions []companion.Definition,
	model *fakeCompanionModel,
	modify func(*Config),
) *Host {
	t.Helper()
	config := hostTestConfig()
	config.Companions = append([]companion.Definition(nil), definitions...)
	config.MaxPlayers = 2
	config.OutboxCapacity = 4096
	config.HeartbeatInterval = time.Hour
	config.HeartbeatTimeout = time.Hour
	if model != nil {
		config.AIModel.Endpoint = model.server.URL + "/v1"
	}
	if modify != nil {
		modify(&config)
	}
	host := mustNewHost(t, config, flatTestGenerator{}, newHostTestStore())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), longWaitDeadline)
		defer cancel()
		if err := host.Shutdown(ctx); err != nil {
			t.Errorf("Host.Shutdown: %v", err)
		}
	})
	return host
}

// replacePlannerForTest 把 manager 的 planner 换成指向假模型的真 PlannerClient。
// 测试需要在世界就绪后按伙伴出生位置构造计划目标，因此模型服务晚于 Host 创建。
func (m *companionManager) replacePlannerForTest(t *testing.T, model *fakeCompanionModel) {
	t.Helper()
	client, err := companion.NewPlannerClient(companion.ModelSettings{
		Endpoint: model.server.URL + "/v1",
		Model:    "test-model",
	}, "", nil)
	if err != nil {
		t.Fatalf("构造测试 planner: %v", err)
	}
	m.planner = client
}

// stepCollectingChatEvents 逐 tick 推进并收集客户端收到的 ChatEvent，直到
// stop 返回 true 或达到 maxTicks。
func stepCollectingChatEvents(
	t *testing.T,
	host *Host,
	client network.ClientEndpoint,
	maxTicks int,
	stop func(events []network.ChatEvent) bool,
) []network.ChatEvent {
	t.Helper()
	var collected []network.ChatEvent
	for range maxTicks {
		result := host.world.StepForTest()
		collected = append(collected,
			companionChatEvents(receiveCompanionChatTick(t, client, result.Tick))...)
		if stop != nil && stop(collected) {
			return collected
		}
	}
	return collected
}

func chatEventKinds(events []network.ChatEvent) []network.ChatEventKind {
	kinds := make([]network.ChatEventKind, len(events))
	for index, event := range events {
		kinds[index] = event.Kind
	}
	return kinds
}

func assertStrictlyIncreasingEventIDs(t *testing.T, events []network.ChatEvent) {
	t.Helper()
	for index := 1; index < len(events); index++ {
		if events[index].EventID <= events[index-1].EventID {
			t.Fatalf("EventID 非严格递增：%+v", chatEventKinds(events))
		}
	}
}

func waitForModelRequests(t *testing.T, model *fakeCompanionModel, want int) {
	t.Helper()
	waitIntegrationCondition(t, "假模型请求数", func() bool {
		requests, _, _, _ := model.snapshotCounts()
		return requests >= want
	})
}

// companionManagerHostReady 建好世界并登录发令者，返回 host、客户端与首个
// 伙伴的出生身体事实（供测试按位置构造计划目标）。每个 tick 都同步消费
// 客户端消息，保证返回后的下一步接收不会命中滞留的旧 tick。
func companionManagerHostReady(
	t *testing.T,
	definitions []companion.Definition,
	model *fakeCompanionModel,
) (*Host, network.ClientEndpoint, companion.Body) {
	t.Helper()
	host := newCompanionManagerHost(t, definitions, model, nil)
	client := openCompanionChatClient(t, host, "memory", integrationIdentity(0x71, "发令者"))
	body := stepUntilCompanionManagerReady(
		t, host, []network.ClientEndpoint{client}, definitions[0].ID,
	)
	return host, client, body
}

// stepUntilCompanionManagerReady 推进到玩家 Ready 且目标伙伴激活，逐 tick
// 消费全部客户端消息以保持流同步。
func stepUntilCompanionManagerReady(
	t *testing.T,
	host *Host,
	clients []network.ClientEndpoint,
	wantID companion.ID,
) companion.Body {
	t.Helper()
	deadline := time.Now().Add(longWaitDeadline)
	for time.Now().Before(deadline) {
		result := host.world.StepForTest()
		ready := true
		for _, endpoint := range clients {
			messages := receiveCompanionChatTick(t, endpoint, result.Tick)
			state, ok := messages[len(messages)-1].(network.PlayerState)
			ready = ready && ok && state.Ready
		}
		for _, body := range host.world.engine.CompanionBodies() {
			if body.ID == wantID && ready {
				return body
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("任务测试世界未就绪：companions=%d", len(host.world.engine.CompanionBodies()))
	return companion.Body{}
}

// currentCompanionBody 读取已激活伙伴的当前身体事实，不推进 tick（保持客户端
// 消息流与 tick 同步的测试约定）。
func currentCompanionBody(t *testing.T, host *Host, id companion.ID) companion.Body {
	t.Helper()
	deadline := time.Now().Add(shortWaitDeadline)
	for time.Now().Before(deadline) {
		for _, body := range host.world.engine.CompanionBodies() {
			if body.ID == id {
				return body
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("伙伴 %s 未激活", id)
	return companion.Body{}
}

func TestCompanionManagerTaskLifecycleEvents(t *testing.T) {
	definitions := []companion.Definition{{ID: chatTestCompanionID(1), Name: "阿木"}}
	host, client, body := companionManagerHostReady(t, definitions, nil)
	baseX, baseZ := int32(body.Position[0]), int32(body.Position[2])
	model := newFakeCompanionModel(t,
		[3]int32{baseX + 3, 1, baseZ},
		[3]int32{baseX + 6, 1, baseZ},
	)
	host.world.companionManager.replacePlannerForTest(t, model)

	sendIntegration(t, client, network.ChatCommand{Text: "@阿木 走到那边"})
	events := stepCollectingChatEvents(t, host, client, 400, func(events []network.ChatEvent) bool {
		return slices.Contains(chatEventKinds(events), network.ChatEventTaskCompleted)
	})

	wantKinds := []network.ChatEventKind{
		network.ChatEventAccepted,
		network.ChatEventTaskStarted,
		network.ChatEventTaskProgress,
		network.ChatEventTaskCompleted,
	}
	if !reflect.DeepEqual(chatEventKinds(events), wantKinds) {
		t.Fatalf("事件序列=%v，想要 %v", chatEventKinds(events), wantKinds)
	}
	assertStrictlyIncreasingEventIDs(t, events)
	issuer := integrationIdentity(0x71, "发令者")
	for _, event := range events {
		if err := event.Validate(); err != nil {
			t.Fatalf("事件 %d Validate: %v", event.EventID, err)
		}
		if event.PlayerID != issuer.PlayerID || event.PlayerName != "发令者" ||
			event.CompanionID != definitions[0].ID || event.CompanionName != "阿木" ||
			event.Command != "走到那边" {
			t.Fatalf("事件身份不完整：%+v", event)
		}
	}
	final := currentCompanionBody(t, host, definitions[0].ID)
	if offset := final.Position[0] - body.Position[0]; offset < 5 || offset > 7 {
		t.Fatalf("完成位置偏移=%f，想要约 6 格", offset)
	}
	// 终态后不再有任何任务事件。
	quiet := stepCollectingChatEvents(t, host, client, 3, nil)
	if len(quiet) != 0 {
		t.Fatalf("终态后事件=%v", chatEventKinds(quiet))
	}
}

func TestCompanionManagerFIFOExecutesCommandsInOrder(t *testing.T) {
	definitions := []companion.Definition{{ID: chatTestCompanionID(1), Name: "阿木"}}
	host, client, body := companionManagerHostReady(t, definitions, nil)
	baseX, baseZ := int32(body.Position[0]), int32(body.Position[2])
	model := newFakeCompanionModel(t,
		[3]int32{baseX + 2, 1, baseZ},
		[3]int32{baseX + 4, 1, baseZ},
		[3]int32{baseX + 6, 1, baseZ},
	)
	host.world.companionManager.replacePlannerForTest(t, model)

	for _, text := range []string{"@阿木 第一", "@阿木 第二", "@阿木 第三"} {
		sendIntegration(t, client, network.ChatCommand{Text: text})
	}
	waitForIncomingChatDepth(t, host.world, 3)
	events := stepCollectingChatEvents(t, host, client, 800, func(events []network.ChatEvent) bool {
		return countKind(events, network.ChatEventTaskCompleted) == 3
	})

	// 逐条指令的生命周期互不交叠：前一条终态之后下一条才 TaskStarted。
	type lifecycle struct {
		started  int
		finished int
	}
	byCommand := map[string]*lifecycle{}
	order := make([]string, 0, 3)
	for _, event := range events {
		switch event.Kind {
		case network.ChatEventTaskStarted, network.ChatEventTaskProgress,
			network.ChatEventTaskCompleted:
			entry, ok := byCommand[event.Command]
			if !ok {
				entry = &lifecycle{}
				byCommand[event.Command] = entry
				order = append(order, event.Command)
			}
			if event.Kind == network.ChatEventTaskStarted {
				entry.started++
			}
			if event.Kind == network.ChatEventTaskCompleted {
				entry.finished++
			}
		}
	}
	if !reflect.DeepEqual(order, []string{"第一", "第二", "第三"}) {
		t.Fatalf("执行顺序=%v，想要接收顺序 第一/第二/第三", order)
	}
	for _, command := range order {
		entry := byCommand[command]
		if entry.started != 1 || entry.finished != 1 {
			t.Fatalf("指令 %q started=%d finished=%d，想要各 1", command, entry.started, entry.finished)
		}
	}
	assertStrictlyIncreasingEventIDs(t, events)
}

func TestChatCommandQueueFullRejectsWithoutModelCall(t *testing.T) {
	definitions := []companion.Definition{{ID: chatTestCompanionID(1), Name: "阿木"}}
	model := newFakeCompanionModel(t, [3]int32{0, 1, 0})
	model.holdRequests()
	host := newCompanionManagerHost(t, definitions, model, nil)
	sender := openCompanionChatClient(t, host, "memory", integrationIdentity(0x72, "发令者"))
	observer := openCompanionChatClient(t, host, "memory", integrationIdentity(0x73, "旁观者"))
	waitForCompanionChatWorld(t, host, []network.ClientEndpoint{sender, observer}, 1)

	for index := range companion.MaxTaskQueueDepth + 1 {
		sendIntegration(t, sender, network.ChatCommand{Text: fmt.Sprintf("@阿木 指令%d", index)})
	}
	waitForIncomingChatDepth(t, host.world, companion.MaxTaskQueueDepth+1)
	result := host.world.StepForTest()
	senderEvents := companionChatEvents(receiveCompanionChatTick(t, sender, result.Tick))
	observerEvents := companionChatEvents(receiveCompanionChatTick(t, observer, result.Tick))

	accepted := countKind(senderEvents, network.ChatEventAccepted)
	rejected := eventsWithKind(senderEvents, network.ChatEventRejected)
	if accepted != companion.MaxTaskQueueDepth || len(rejected) != 1 {
		t.Fatalf("Accepted=%d QueueFull=%d，想要 %d/1",
			accepted, len(rejected), companion.MaxTaskQueueDepth)
	}
	full := rejected[0]
	if full.RejectReason != network.ChatRejectQueueFull || full.CompanionID != definitions[0].ID ||
		full.Command != fmt.Sprintf("指令%d", companion.MaxTaskQueueDepth) {
		t.Fatalf("QueueFull 事件=%+v", full)
	}
	if err := full.Validate(); err != nil {
		t.Fatalf("QueueFull Validate: %v", err)
	}
	if countKind(observerEvents, network.ChatEventAccepted) != companion.MaxTaskQueueDepth ||
		countKind(observerEvents, network.ChatEventRejected) != 0 {
		t.Fatalf("旁观者事件=%v，QueueFull 不得广播", chatEventKinds(observerEvents))
	}

	waitForModelRequests(t, model, 1)
	requests, _, inFlight, _ := model.snapshotCounts()
	if requests != 1 || inFlight != 1 {
		t.Fatalf("模型调用=%d 在途=%d，QueueFull 必须同步拒绝且只派发队首", requests, inFlight)
	}
	host.world.stepMu.Lock()
	slot := host.world.companionManager.slots[definitions[0].ID]
	depth := slot.queue.Len()
	current, hasCurrent := slot.queue.Current()
	inFlightFlag := slot.planningInFlight
	host.world.stepMu.Unlock()
	if depth != companion.MaxTaskQueueDepth-1 || !hasCurrent ||
		current.State != companion.TaskPlanning || !inFlightFlag {
		t.Fatalf("FIFO depth=%d current=%+v inFlight=%v，既有队列被破坏", depth, current, inFlightFlag)
	}
	if cap(host.world.companionManager.semaphore) != 4 {
		t.Fatalf("模型并发信号量容量=%d，想要 4", cap(host.world.companionManager.semaphore))
	}
}

func TestCompanionManagerSlowModelDoesNotBlockTicks(t *testing.T) {
	definitions := []companion.Definition{{ID: chatTestCompanionID(1), Name: "阿木"}}
	model := newFakeCompanionModel(t, [3]int32{0, 1, 0})
	model.holdRequests()
	host, client, _ := companionManagerHostReady(t, definitions, model)

	sendIntegration(t, client, network.ChatCommand{Text: "@阿木 慢慢想"})
	waitForIncomingChatDepth(t, host.world, 1)
	before := host.world.StepForTest()
	receiveCompanionChatTick(t, client, before.Tick)
	waitForModelRequests(t, model, 1)

	started := time.Now()
	const extraTicks = 20
	for range extraTicks {
		result := host.world.StepForTest()
		receiveCompanionChatTick(t, client, result.Tick)
	}
	elapsed := time.Since(started)
	if after := host.world.TickCount(); after-before.Tick != extraTicks {
		t.Fatalf("tick 推进=%d，想要 %d", after-before.Tick, extraTicks)
	}
	// 挂起的模型请求期间，20 个 tick 必须远快于真实节拍 1 秒；阻塞边界放宽到
	// 2 秒以容纳 race 检测下的抖动。
	if elapsed > 2*time.Second {
		t.Fatalf("挂起模型期间 %d tick 耗时=%v，权威 tick 被阻塞", extraTicks, elapsed)
	}
	if _, _, inFlight, _ := model.snapshotCounts(); inFlight != 1 {
		t.Fatalf("模型在途=%d，想要 1", inFlight)
	}
	model.releaseRequests()
}

func TestCompanionManagerOneInFlightRequestPerCompanion(t *testing.T) {
	definitions := []companion.Definition{{ID: chatTestCompanionID(1), Name: "阿木"}}
	model := newFakeCompanionModel(t,
		[3]int32{1, 1, 0},
		[3]int32{2, 1, 0},
	)
	model.holdRequests()
	host, client, _ := companionManagerHostReady(t, definitions, model)

	sendIntegration(t, client, network.ChatCommand{Text: "@阿木 第一条"})
	sendIntegration(t, client, network.ChatCommand{Text: "@阿木 第二条"})
	waitForIncomingChatDepth(t, host.world, 2)
	for range 8 {
		result := host.world.StepForTest()
		receiveCompanionChatTick(t, client, result.Tick)
	}
	waitForModelRequests(t, model, 1)
	if requests, _, _, _ := model.snapshotCounts(); requests != 1 {
		t.Fatalf("在途期间模型请求数=%d，同一伙伴必须最多一个在途规划请求", requests)
	}

	// 释放后第一条完成，第二条才发起自己的请求；两条转换都发生在后续
	// tick 边界，等待期间必须持续推进世界。
	model.releaseRequests()
	dispatchedSecond := false
	for range 400 {
		result := host.world.StepForTest()
		receiveCompanionChatTick(t, client, result.Tick)
		if requests, _, _, _ := model.snapshotCounts(); requests >= 2 {
			dispatchedSecond = true
			break
		}
	}
	if !dispatchedSecond {
		t.Fatal("释放后第二条指令始终未发起规划请求")
	}
	if requests, _, _, _ := model.snapshotCounts(); requests != 2 {
		t.Fatalf("释放后模型请求数=%d，想要 2", requests)
	}
}

func TestCompanionManagerFourConcurrentModelRequests(t *testing.T) {
	definitions := []companion.Definition{
		{ID: chatTestCompanionID(1), Name: "阿木"},
		{ID: chatTestCompanionID(2), Name: "阿木甲"},
		{ID: chatTestCompanionID(3), Name: "小石"},
		{ID: chatTestCompanionID(4), Name: "松果"},
	}
	model := newFakeCompanionModel(t, [3]int32{0, 1, 0})
	model.holdRequests()
	host := newCompanionManagerHost(t, definitions, model, nil)
	sender := openCompanionChatClient(t, host, "memory", integrationIdentity(0x74, "发令者"))
	waitForCompanionChatWorld(t, host, []network.ClientEndpoint{sender}, len(definitions))

	for _, definition := range definitions {
		sendIntegration(t, sender, network.ChatCommand{Text: "@" + definition.Name + " 出发"})
	}
	waitForIncomingChatDepth(t, host.world, len(definitions))
	result := host.world.StepForTest()
	receiveCompanionChatTick(t, sender, result.Tick)

	waitForModelRequests(t, model, len(definitions))
	if _, peak, _, _ := model.snapshotCounts(); peak != len(definitions) {
		t.Fatalf("峰值并发=%d，想要四个伙伴全部并发（上限 4）", peak)
	}
	model.releaseRequests()
}

func TestCompanionManagerStalePlannerResultDiscarded(t *testing.T) {
	definitions := []companion.Definition{{ID: chatTestCompanionID(1), Name: "阿木"}}
	model := newFakeCompanionModel(t, [3]int32{1, 1, 0})
	model.holdRequests()
	host, client, _ := companionManagerHostReady(t, definitions, model)

	sendIntegration(t, client, network.ChatCommand{Text: "@阿木 会过时"})
	waitForIncomingChatDepth(t, host.world, 1)
	result := host.world.StepForTest()
	receiveCompanionChatTick(t, client, result.Tick)
	waitForModelRequests(t, model, 1)

	// 模拟任务在结果在途期间进入终态（关服冻结后的丢弃路径）：直接把当前
	// 任务置为 Failed，晚到的成功计划绝不能让任务复活。
	host.world.stepMu.Lock()
	host.world.companionManager.slots[definitions[0].ID].queue.FailPlanning(
		companion.TaskFailPlannerUnavailable,
	)
	host.world.stepMu.Unlock()
	model.releaseRequests()

	events := stepCollectingChatEvents(t, host, client, 10, nil)
	if slices.Contains(chatEventKinds(events), network.ChatEventTaskStarted) {
		t.Fatalf("过时结果复活了任务：%v", chatEventKinds(events))
	}
	host.world.stepMu.Lock()
	_, hasCurrent := host.world.companionManager.slots[definitions[0].ID].queue.Current()
	host.world.stepMu.Unlock()
	if hasCurrent {
		t.Fatal("过时结果重新占据当前任务槽")
	}
	if requests, _, _, _ := model.snapshotCounts(); requests != 1 {
		t.Fatalf("过时结果触发了新请求=%d", requests)
	}
}

func TestCompanionManagerDistantGoalFailsPathUnreachable(t *testing.T) {
	definitions := []companion.Definition{{ID: chatTestCompanionID(1), Name: "阿木"}}
	host, client, body := companionManagerHostReady(t, definitions, nil)
	model := newFakeCompanionModel(t,
		[3]int32{int32(body.Position[0]) + 1000, 1, int32(body.Position[2])})
	host.world.companionManager.replacePlannerForTest(t, model)

	sendIntegration(t, client, network.ChatCommand{Text: "@阿木 去远方"})
	events := stepCollectingChatEvents(t, host, client, 400, func(events []network.ChatEvent) bool {
		return slices.Contains(chatEventKinds(events), network.ChatEventTaskFailed)
	})
	failed := eventsWithKind(events, network.ChatEventTaskFailed)
	if len(failed) != 1 {
		t.Fatalf("TaskFailed=%d，想要 1（事件=%v）", len(failed), chatEventKinds(events))
	}
	if network.TaskFailReason(failed[0].RejectReason) != network.TaskFailPathUnreachable {
		t.Fatalf("失败原因=%d，想要 PathUnreachable", failed[0].RejectReason)
	}
	// 目标在寻路窗口外，伙伴必须原地不动。
	final := currentCompanionBody(t, host, definitions[0].ID)
	if final.Position != body.Position {
		t.Fatalf("不可达任务产生了位移：%v -> %v", body.Position, final.Position)
	}
	// 终态后 FIFO 继续接受新指令。
	sendIntegration(t, client, network.ChatCommand{Text: "@阿木 再试"})
	waitForIncomingChatDepth(t, host.world, 1)
	result := host.world.StepForTest()
	events = companionChatEvents(receiveCompanionChatTick(t, client, result.Tick))
	if len(events) != 1 || events[0].Kind != network.ChatEventAccepted {
		t.Fatalf("终态后新指令事件=%v", chatEventKinds(events))
	}
}

func TestCompanionManagerTaskTimesOutAtWorldTimeDeadline(t *testing.T) {
	definitions := []companion.Definition{{ID: chatTestCompanionID(1), Name: "阿木"}}
	host := newCompanionManagerHost(t, definitions, nil, func(config *Config) {
		config.AIModel.TaskTimeoutMinutes = 1
	})
	client := openCompanionChatClient(t, host, "memory", integrationIdentity(0x75, "发令者"))
	body := stepUntilCompanionManagerReady(
		t, host, []network.ClientEndpoint{client}, definitions[0].ID,
	)
	baseX, baseZ := int32(body.Position[0]), int32(body.Position[2])
	// 20 步 × 15 格 ≈ 300 格 ≈ 1400+ tick 的行程；1 分钟 deadline（1200 tick）
	// 必然在途中命中，TimedOut 之后移动停在当前位置。
	steps := make([][3]int32, 0, 20)
	for index := 1; index <= 20; index++ {
		steps = append(steps, [3]int32{baseX + int32(index)*15, 1, baseZ})
	}
	model := newFakeCompanionModel(t, steps...)
	host.world.companionManager.replacePlannerForTest(t, model)

	sendIntegration(t, client, network.ChatCommand{Text: "@阿木 长途"})
	events := stepCollectingChatEvents(t, host, client, 4000, func(events []network.ChatEvent) bool {
		return slices.Contains(chatEventKinds(events), network.ChatEventTaskTimedOut)
	})
	timedOut := eventsWithKind(events, network.ChatEventTaskTimedOut)
	if len(timedOut) != 1 {
		t.Fatalf("TaskTimedOut=%d（事件=%v）", len(timedOut), chatEventKinds(events))
	}
	if timedOut[0].RejectReason != network.ChatRejectNone {
		t.Fatalf("TimedOut reason=%d，想要 None", timedOut[0].RejectReason)
	}
	if !slices.Contains(chatEventKinds(events), network.ChatEventTaskStarted) {
		t.Fatalf("缺少 TaskStarted：%v", chatEventKinds(events))
	}
	// 超时后移动必须停在当前位置。
	stop := currentCompanionBody(t, host, definitions[0].ID)
	for range 5 {
		host.world.StepForTest()
	}
	settled := currentCompanionBody(t, host, definitions[0].ID)
	dx := settled.Position[0] - stop.Position[0]
	dz := settled.Position[2] - stop.Position[2]
	if dx*dx+dz*dz > 0.01 {
		t.Fatalf("超时后仍在移动：%v -> %v", stop.Position, settled.Position)
	}
}

func TestCompanionShutdownCancelsPlannerBeforeFinalSaveAndStore(t *testing.T) {
	definitions := []companion.Definition{{ID: chatTestCompanionID(1), Name: "阿木"}}
	order := &shutdownOrderLog{}
	model := newFakeCompanionModel(t, [3]int32{0, 1, 0})
	model.holdRequests()
	model.cancelOrder = order

	config := hostTestConfig()
	config.Companions = definitions
	config.MaxPlayers = 1
	config.OutboxCapacity = 4096
	config.HeartbeatInterval = time.Hour
	config.HeartbeatTimeout = time.Hour
	config.AIModel.Endpoint = model.server.URL + "/v1"
	store := newCompanionManagerOrderStore(order)
	host, err := NewHost(context.Background(), config, flatTestGenerator{}, store)
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	client := openCompanionChatClient(t, host, "memory", integrationIdentity(0x76, "发令者"))
	waitForCompanionChatWorld(t, host, []network.ClientEndpoint{client}, 1)

	sendIntegration(t, client, network.ChatCommand{Text: "@阿木 关服前"})
	waitForIncomingChatDepth(t, host.world, 1)
	result := host.world.StepForTest()
	receiveCompanionChatTick(t, client, result.Tick)
	waitForModelRequests(t, model, 1)

	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
	defer cancel()
	if err := host.Shutdown(ctx); err != nil {
		t.Fatalf("Host.Shutdown: %v", err)
	}

	rank := map[string]int{}
	for index, event := range order.snapshot() {
		rank[event] = index
	}
	wantOrder := []string{"model-cancel", "companion-save", "sync", "close"}
	for index := 1; index < len(wantOrder); index++ {
		previous, okPrevious := rank[wantOrder[index-1]]
		current, okCurrent := rank[wantOrder[index]]
		if !okPrevious || !okCurrent || previous > current {
			t.Fatalf("关服顺序=%v，想要 %v 依序出现", order.snapshot(), wantOrder)
		}
	}
	// 冻结后 tick 不再推进，ChatCommand 不再被处理。
	if frozen := host.world.StepForTest(); frozen.Tick != 0 {
		t.Fatalf("冻结后 Step tick=%d，想要空结果", frozen.Tick)
	}
	if _, _, _, cancels := model.snapshotCounts(); cancels == 0 {
		t.Fatal("关服未取消在途模型请求")
	}
}

// companionManagerOrderStore 把伙伴保存与世界存储的关服动作记录进同一顺序
// 日志，供关服顺序断言。
type companionManagerOrderStore struct {
	*hostTestStore
	order *shutdownOrderLog
}

func newCompanionManagerOrderStore(order *shutdownOrderLog) *companionManagerOrderStore {
	return &companionManagerOrderStore{hostTestStore: newHostTestStore(), order: order}
}

func (store *companionManagerOrderStore) SaveCompanions(
	ctx context.Context,
	save storage.CompanionSave,
) error {
	store.order.record("companion-save")
	return store.hostTestStore.MemoryStore.SaveCompanions(ctx, save)
}

func (store *companionManagerOrderStore) Sync(ctx context.Context) error {
	store.order.record("sync")
	return store.hostTestStore.Sync(ctx)
}

func (store *companionManagerOrderStore) Close() error {
	store.order.record("close")
	return store.hostTestStore.Close()
}

func TestChatCommandTaskEventsMemoryTCPParity(t *testing.T) {
	results := make(map[string][]companionChatTranscriptEvent, 2)
	for _, transport := range []string{"memory", "tcp"} {
		transport := transport
		t.Run(transport, func(t *testing.T) {
			results[transport] = runCompanionManagerParity(t, transport)
		})
	}
	if !reflect.DeepEqual(results["memory"], results["tcp"]) {
		t.Fatalf("Memory/TCP 任务事件 transcript 不一致\nMemory=%+v\nTCP=%+v",
			results["memory"], results["tcp"])
	}
	got := results["memory"]
	senderEvents := make([]network.ChatEventKind, 0, 5)
	observerEvents := make([]network.ChatEventKind, 0, 4)
	for _, entry := range got {
		if entry.Recipient == 0 {
			senderEvents = append(senderEvents, entry.Event.Kind)
		} else {
			observerEvents = append(observerEvents, entry.Event.Kind)
		}
	}
	wantSender := []network.ChatEventKind{
		network.ChatEventAccepted,
		network.ChatEventRejected,
		network.ChatEventTaskStarted,
		network.ChatEventTaskProgress,
		network.ChatEventTaskCompleted,
	}
	wantObserver := []network.ChatEventKind{
		network.ChatEventAccepted,
		network.ChatEventTaskStarted,
		network.ChatEventTaskProgress,
		network.ChatEventTaskCompleted,
	}
	if !reflect.DeepEqual(senderEvents, wantSender) {
		t.Fatalf("发令者事件=%v，想要 %v", senderEvents, wantSender)
	}
	if !reflect.DeepEqual(observerEvents, wantObserver) {
		t.Fatalf("旁观者事件=%v，想要 %v", observerEvents, wantObserver)
	}
}

func runCompanionManagerParity(t *testing.T, transport string) []companionChatTranscriptEvent {
	t.Helper()
	definitions := chatTestDefinitions()[:1]
	host := newCompanionManagerHost(t, definitions, nil, nil)
	sender := openCompanionChatClient(t, host, transport, integrationIdentity(0x81, "发送者"))
	observer := openCompanionChatClient(t, host, transport, integrationIdentity(0x82, "观察者"))
	clients := []network.ClientEndpoint{sender, observer}
	body := stepUntilCompanionManagerReady(t, host, clients, definitions[0].ID)
	model := newFakeCompanionModel(t,
		[3]int32{int32(body.Position[0]) + 2, 1, int32(body.Position[2])},
		[3]int32{int32(body.Position[0]) + 4, 1, int32(body.Position[2])},
	)
	host.world.companionManager.replacePlannerForTest(t, model)

	sendIntegration(t, sender, network.ChatCommand{Text: "@阿木 走两步"})
	sendIntegration(t, sender, network.ChatCommand{Text: "@不存在 看看"})
	waitForIncomingChatDepth(t, host.world, 2)

	transcript := make([]companionChatTranscriptEvent, 0, 10)
	quiet := 0
	for range 600 {
		result := host.world.StepForTest()
		tickEvents := 0
		for recipient, endpoint := range clients {
			for _, event := range companionChatEvents(receiveCompanionChatTick(t, endpoint, result.Tick)) {
				transcript = append(transcript, companionChatTranscriptEvent{
					Recipient: recipient,
					Event:     event,
				})
				tickEvents++
			}
		}
		if tickEvents == 0 {
			quiet++
		} else {
			quiet = 0
		}
		completed := false
		for _, entry := range transcript {
			if entry.Event.Kind == network.ChatEventTaskCompleted {
				completed = true
			}
		}
		if completed && quiet >= 3 {
			break
		}
	}
	return transcript
}

func TestCompanionManagerPlanSnapshotBoundedAndOrdered(t *testing.T) {
	definitions := []companion.Definition{{ID: chatTestCompanionID(1), Name: "阿木"}}
	host, _, body := companionManagerHostReady(t, definitions, nil)
	active := activeLoginForPlayer(t, host, integrationIdentity(0x71, "发令者").PlayerID)

	host.world.stepMu.Lock()
	identity := integrationIdentity(0x71, "发令者")
	issuer := host.world.companionManager.captureIssuer(
		identity.PlayerID, "发令者", active.Session,
	)
	snapshot, err := host.world.companionManager.buildPlanSnapshot(
		definitions[0], companion.TaskCommand("环顾四周"), issuer, body,
	)
	worldTime := host.world.engine.WorldTime()
	host.world.stepMu.Unlock()
	if err != nil {
		t.Fatalf("构造快照: %v", err)
	}
	if err := snapshot.Validate(); err != nil {
		t.Fatalf("快照 Validate: %v", err)
	}
	if snapshot.Command != "环顾四周" || snapshot.Companion.ID != definitions[0].ID {
		t.Fatalf("快照身份=%+v", snapshot)
	}
	if snapshot.WorldTimeTicks != worldTime {
		t.Fatalf("快照世界时间=%d，想要 %d", snapshot.WorldTimeTicks, worldTime)
	}
	if snapshot.Issuer.ID != identity.PlayerID {
		t.Fatalf("发令者 ID=%s", snapshot.Issuer.ID)
	}
	if len(snapshot.ExposedBlocks) > companion.MaxPlanExposedBlocks {
		t.Fatalf("暴露方块=%d，超过上限", len(snapshot.ExposedBlocks))
	}
	for index := 1; index < len(snapshot.ExposedBlocks); index++ {
		previous := snapshot.ExposedBlocks[index-1].Pos
		current := snapshot.ExposedBlocks[index].Pos
		if !blockPosAfterForSort(current, previous) {
			t.Fatalf("暴露方块未按 (X,Y,Z) 严格升序：%v 后跟 %v", previous, current)
		}
	}
	if len(snapshot.Heights) > companion.MaxPlanHeightSamples {
		t.Fatalf("高度样本=%d，超过上限", len(snapshot.Heights))
	}
	for index := 1; index < len(snapshot.Heights); index++ {
		previous := snapshot.Heights[index-1]
		current := snapshot.Heights[index]
		if previous.X > current.X || (previous.X == current.X && previous.Z >= current.Z) {
			t.Fatalf("高度样本未按 (X,Z) 严格升序：%+v 后跟 %+v", previous, current)
		}
	}
	if len(snapshot.ChunkRevisions) == 0 || len(snapshot.ChunkRevisions) > companion.MaxPlanChunkRevisions {
		t.Fatalf("区块 revision 数=%d", len(snapshot.ChunkRevisions))
	}
}

func TestCompanionManagerPathBlockTableMatchesCollisionOracle(t *testing.T) {
	table := companion.NewPathBlockTable(productionCompanionPassableBlocks())
	if !table.PassableForTest(core.AirID) {
		t.Fatal("空气必须可通过")
	}
	for id := core.BlockID(1); id <= core.MossyCobblestoneID; id++ {
		if table.PassableForTest(id) {
			t.Fatalf("方块 %d 是碰撞实体（physics.BlockCollisionBoxes 全格阻挡），不得通过", id)
		}
	}
}

func countKind(events []network.ChatEvent, kind network.ChatEventKind) int {
	count := 0
	for _, event := range events {
		if event.Kind == kind {
			count++
		}
	}
	return count
}

func eventsWithKind(events []network.ChatEvent, kind network.ChatEventKind) []network.ChatEvent {
	matched := make([]network.ChatEvent, 0, len(events))
	for _, event := range events {
		if event.Kind == kind {
			matched = append(matched, event)
		}
	}
	return matched
}

func blockPosAfterForSort(pos, previous core.BlockPos) bool {
	if pos.X != previous.X {
		return pos.X > previous.X
	}
	if pos.Y != previous.Y {
		return pos.Y > previous.Y
	}
	return pos.Z > previous.Z
}
