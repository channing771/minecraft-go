// D5 Dialogue worker 与共享模型槽的测试：全服四槽被 Planner 占满时 Dialogue
// 节点跳过且不排队不补发、每伙伴单在途（新节点跳过且不取消在途）、过时结果
// 丢弃（世代变化后无副作用）、模型持续失败只跳过台词不改任务事实、四伙伴
// 在途挂起模型不阻塞权威 tick、关服取消在途请求。全部使用 httptest 假模型。
package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/network"
)

// dialogueRequestRecord 是假台词模型观察到的一次请求输入事实：节点类别的
// 稳定 wire 文本（start/progress/first_arrival/terminal）、进展节点完成的
// 步骤类别（step_kind，D8 阶段验收断言「进展节点与计划步骤一一对应」用）、
// 人设与最近对话摘要（D6 接线断言用：触发节点序列、persona 透传与摘要
// 生命周期）。
type dialogueRequestRecord struct {
	NodeKind string
	StepKind string
	Persona  string
	Summary  string
}

// fakeDialogueModel 是 httptest 假台词模型：默认返回固定合法台词 JSON，可整
// 体阻塞全部在途请求（关服取消观察）、按请求持续失败，并统计请求数、在途数
// 与 context 取消数。终态请求的判定依据是用户消息里的固定节点类别文本
// （companion 包的稳定 wire 枚举 "terminal"）；每次请求的输入事实按到达顺序
// 记录进 records（snapshotDialogueRequests 读取）。
type fakeDialogueModel struct {
	mu       sync.Mutex
	requests int
	inFlight int
	cancels  int
	block    chan struct{}
	status   int
	records  []dialogueRequestRecord
	server   *httptest.Server
}

func newFakeDialogueModel(t *testing.T) *fakeDialogueModel {
	t.Helper()
	model := &fakeDialogueModel{}
	model.server = httptest.NewServer(http.HandlerFunc(model.handle))
	t.Cleanup(model.server.Close)
	return model
}

func (model *fakeDialogueModel) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	// 用户消息是嵌套在外层请求 JSON 里的字符串（内层引号被转义），先解出
	// content 再判断节点类别；判定依据是 companion 包的稳定 wire 枚举文本。
	userContent := ""
	var outer struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &outer); err == nil && len(outer.Messages) == 2 {
		userContent = outer.Messages[1].Content
	}
	// 输入事实按 dialogueUserPayload 的固定形态解出：节点 kind 是稳定英文
	// wire 文本，persona 与摘要是请求输入的两类有界数据。
	record := dialogueRequestRecord{}
	if userContent != "" {
		var payload struct {
			Persona string `json:"persona"`
			Summary string `json:"summary"`
			Node    struct {
				Kind     string `json:"kind"`
				StepKind string `json:"step_kind"`
			} `json:"node"`
		}
		if err := json.Unmarshal([]byte(userContent), &payload); err == nil {
			record = dialogueRequestRecord{
				NodeKind: payload.Node.Kind,
				StepKind: payload.Node.StepKind,
				Persona:  payload.Persona,
				Summary:  payload.Summary,
			}
		}
	}
	model.mu.Lock()
	model.requests++
	model.inFlight++
	model.records = append(model.records, record)
	block := model.block
	status := model.status
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
			model.mu.Unlock()
			return
		}
	}
	if status != 0 {
		w.WriteHeader(status)
		return
	}
	content := `{"line":"我出发了"}`
	if strings.Contains(userContent, `"kind":"terminal"`) {
		content = `{"line":"完成了","summary":"最近完成了任务"}`
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"choices": []map[string]any{
			{"message": map[string]string{"role": "assistant", "content": content}},
		},
	})
}

// holdRequests 阻塞后续全部请求直至 releaseRequests。
func (model *fakeDialogueModel) holdRequests() {
	model.mu.Lock()
	model.block = make(chan struct{})
	model.mu.Unlock()
}

func (model *fakeDialogueModel) releaseRequests() {
	model.mu.Lock()
	block := model.block
	model.block = nil
	model.mu.Unlock()
	if block != nil {
		close(block)
	}
}

// setStatus 配置非 2xx 响应（持续失败）。
func (model *fakeDialogueModel) setStatus(status int) {
	model.mu.Lock()
	model.status = status
	model.mu.Unlock()
}

func (model *fakeDialogueModel) snapshotCounts() (requests, inFlight, cancels int) {
	model.mu.Lock()
	defer model.mu.Unlock()
	return model.requests, model.inFlight, model.cancels
}

// snapshotDialogueRequests 返回按到达顺序记录的请求输入事实快照。
func (model *fakeDialogueModel) snapshotDialogueRequests() []dialogueRequestRecord {
	model.mu.Lock()
	defer model.mu.Unlock()
	return append([]dialogueRequestRecord(nil), model.records...)
}

// dialogueRequestKinds 把请求记录投影为节点类别序列（失败信息用）。
func dialogueRequestKinds(records []dialogueRequestRecord) []string {
	kinds := make([]string, len(records))
	for index := range records {
		kinds[index] = records[index].NodeKind
	}
	return kinds
}

func waitForDialogueRequests(t *testing.T, model *fakeDialogueModel, want int) {
	t.Helper()
	waitIntegrationCondition(t, "假台词模型请求数", func() bool {
		requests, _, _ := model.snapshotCounts()
		return requests >= want
	})
}

// replaceDialogueForTest 把 manager 的台词客户端换成指向假台词模型的真
// DialogueClient（对齐 replacePlannerForTest 的测试模式）。
func (m *companionManager) replaceDialogueForTest(t *testing.T, model *fakeDialogueModel) {
	t.Helper()
	client, err := companion.NewDialogueClient(companion.ModelSettings{
		Endpoint: model.server.URL + "/v1",
		Model:    "test-model",
	}, "", nil)
	if err != nil {
		t.Fatalf("构造测试 dialogue 客户端: %v", err)
	}
	m.dialogue = client
}

// dialogueEffectCount 在 stepMu 下读取有效台词结果进入 applyDialogueEffect 的
// 次数（D5 的可观察哨兵）。
func dialogueEffectCount(t *testing.T, host *Host, id companion.ID) (effects int, inFlight bool) {
	t.Helper()
	host.world.stepMu.Lock()
	defer host.world.stepMu.Unlock()
	slot := host.world.companionManager.slots[id]
	if slot == nil {
		t.Fatalf("伙伴 %s 没有任务槽位", id)
	}
	return host.world.companionManager.dialogueEffects, slot.dialogueInFlight
}

// TestCompanionDialogueSkippedWhenModelSlotsFull 验证全服四个共享模型槽全部被
// Planner 请求占满时：Dialogue 节点被跳过（不排队）、槽位释放后也不补发、
// 对应任务照常推进。
func TestCompanionDialogueSkippedWhenModelSlotsFull(t *testing.T) {
	definitions := []companion.Definition{
		{ID: chatTestCompanionID(1), Name: "阿木"},
		{ID: chatTestCompanionID(2), Name: "阿木甲"},
		{ID: chatTestCompanionID(3), Name: "小石"},
		{ID: chatTestCompanionID(4), Name: "松果"},
	}
	planner := newFakeCompanionModel(t, [3]int32{0, 1, 0})
	planner.holdRequests()
	host := newCompanionManagerHost(t, definitions, planner, nil)
	dialogue := newFakeDialogueModel(t)
	host.world.companionManager.replaceDialogueForTest(t, dialogue)
	sender := openCompanionChatClient(t, host, "memory", integrationIdentity(0x90, "发令者"))
	waitForCompanionChatWorld(t, host, []network.ClientEndpoint{sender}, len(definitions))

	for _, definition := range definitions {
		sendIntegration(t, sender, network.ChatCommand{Text: "@" + definition.Name + " 出发"})
	}
	waitForIncomingChatDepth(t, host.world, len(definitions))
	result := host.world.StepForTest()
	receiveCompanionChatTick(t, sender, result.Tick)
	waitForModelRequests(t, planner, len(definitions))

	// 四个共享槽全部被 Planner 占据：首个到达节点台词被跳过，不排队、不置
	// 在途。选用 FirstArrival 节点做哨兵：这些伙伴没有 follow 任务，自动
	// 接线永远不会派发该类别——释放后若它再次出现即证明「跳过被补发」。
	host.world.stepMu.Lock()
	host.world.companionManager.requestDialogue(
		definitions[0].ID, companion.DialogueNode{Kind: companion.DialogueNodeFirstArrival})
	host.world.stepMu.Unlock()
	if requests, _, _ := dialogue.snapshotCounts(); requests != 0 {
		t.Fatalf("槽满时台词请求数=%d，想要 0（不得排队等槽）", requests)
	}
	effects, inFlight := dialogueEffectCount(t, host, definitions[0].ID)
	if effects != 0 || inFlight {
		t.Fatalf("槽满跳过后 effects=%d dialogueInFlight=%v，想要 0/false", effects, inFlight)
	}

	// 释放 Planner 槽位：任务照常推进（全部 TaskStarted），被跳过的节点不补发。
	planner.releaseRequests()
	events := stepCollectingChatEvents(t, host, sender, 600, func(events []network.ChatEvent) bool {
		return countKind(events, network.ChatEventTaskStarted) == len(definitions)
	})
	if got := countKind(events, network.ChatEventTaskStarted); got != len(definitions) {
		t.Fatalf("槽满期间任务推进受阻：TaskStarted=%d，想要 %d（事件=%v）",
			got, len(definitions), chatEventKinds(events))
	}
	// 槽位释放后观察一段：被跳过的台词节点绝不补发。D6 接线后四个任务各自
	// 进入 Running 并发起自动 start 节点（单步计划目标极近，终止节点可能被
	// 开始台词在途挤掉——「跳过即放弃」的规格语义，终止节点的正常路径由
	// TerminalCoversFourTerminalStates 锁定）；手动哨兵（FirstArrival）没有
	// 任何自动接线来源，释放后再次出现即证明跳过被补发。
	stepCollectingChatEvents(t, host, sender, 40, nil)
	if requests, _, _ := dialogue.snapshotCounts(); requests < len(definitions) {
		t.Fatalf("槽位释放后台词请求数=%d，想要至少 %d（自动 start 节点照常发起）",
			requests, len(definitions))
	}
	for _, record := range dialogue.snapshotDialogueRequests() {
		if record.NodeKind == "first_arrival" {
			t.Fatalf("槽满期间跳过的节点被补发：%+v", record)
		}
	}
}

// TestCompanionDialogueOneInFlightPerCompanion 验证每伙伴最多一个在途台词
// 请求：在途期间第二个节点被跳过且在途请求不被取消；结果落地后新节点可以
// 再次发起。
func TestCompanionDialogueOneInFlightPerCompanion(t *testing.T) {
	definitions := []companion.Definition{{ID: chatTestCompanionID(1), Name: "阿木"}}
	host, client, _ := companionManagerHostReady(t, definitions, nil)
	dialogue := newFakeDialogueModel(t)
	dialogue.holdRequests()
	host.world.companionManager.replaceDialogueForTest(t, dialogue)
	// follow 目标必须是已登录玩家：companionManagerHostReady 用 0x71 身份登录，
	// 离线目标会让任务在首个 advanceRunners 即以 WorldChanged 失败。
	issuerIdentity := integrationIdentity(0x71, "发令者")
	injectRunningCompanionTask(t, host, definitions[0].ID, stopTestIssuer(issuerIdentity),
		"跟着我", []companion.PlanStep{{Kind: companion.PlanStepFollow, PlayerID: issuerIdentity.PlayerID}})
	// 再推进一个 tick：伙伴身体在激活 tick 之后的下一次 refreshBodies 才进入
	// manager 的身体缓存（tick 边界的一致性语义，与 dispatchPlanning 相同）。
	warmup := host.world.StepForTest()
	receiveCompanionChatTick(t, client, warmup.Tick)

	host.world.stepMu.Lock()
	host.world.companionManager.requestDialogue(
		definitions[0].ID, companion.DialogueNode{Kind: companion.DialogueNodeStart})
	host.world.stepMu.Unlock()
	waitForDialogueRequests(t, dialogue, 1)

	// 在途期间第二个节点（进展）到来：直接跳过，不取消在途请求。
	host.world.stepMu.Lock()
	host.world.companionManager.requestDialogue(definitions[0].ID, companion.DialogueNode{
		Kind: companion.DialogueNodeProgress, StepKind: companion.PlanStepGoTo})
	host.world.stepMu.Unlock()
	result := host.world.StepForTest()
	receiveCompanionChatTick(t, client, result.Tick)
	requests, inFlightModel, cancels := dialogue.snapshotCounts()
	if requests != 1 {
		t.Fatalf("在途期间台词请求数=%d，想要 1（同一伙伴必须最多一个在途）", requests)
	}
	if inFlightModel != 1 || cancels != 0 {
		t.Fatalf("在途模型状态 inFlight=%d cancels=%d，想要 1/0（新节点不得取消在途）",
			inFlightModel, cancels)
	}
	if _, inFlightSlot := dialogueEffectCount(t, host, definitions[0].ID); !inFlightSlot {
		t.Fatal("在途标记丢失：slot.dialogueInFlight 应保持 true")
	}

	// 释放后结果在 tick 边界应用（D5 哨兵计数 +1），在途标记清除。
	dialogue.releaseRequests()
	applied := false
	for range 50 {
		stepResult := host.world.StepForTest()
		receiveCompanionChatTick(t, client, stepResult.Tick)
		if effects, inFlight := dialogueEffectCount(t, host, definitions[0].ID); effects == 1 && !inFlight {
			applied = true
			break
		}
	}
	if !applied {
		effects, inFlight := dialogueEffectCount(t, host, definitions[0].ID)
		t.Fatalf("台词结果未在 tick 边界应用：effects=%d inFlight=%v", effects, inFlight)
	}

	// 在途清除后新节点可以再次发起。
	host.world.stepMu.Lock()
	host.world.companionManager.requestDialogue(definitions[0].ID, companion.DialogueNode{
		Kind: companion.DialogueNodeProgress, StepKind: companion.PlanStepGoTo})
	host.world.stepMu.Unlock()
	waitForDialogueRequests(t, dialogue, 2)
	dialogue.releaseRequests()
}

// TestCompanionDialogueStaleOutcomeDiscarded 验证任务终态后到达的开始节点
// 结果被第二级过时判定丢弃：不进入 applyDialogueEffect（哨兵计数为 0）、
// 在途标记照常清除、不触发新请求。D6 起构造方式改为「任务终态但不提升
// 队首」——不留 pending 指令，dispatchPlanning 无从开始新任务，世代保持
// 不变，从而把「开始/进展节点要求任务仍在 Running」的判定独立隔离出来。
func TestCompanionDialogueStaleOutcomeDiscarded(t *testing.T) {
	definitions := []companion.Definition{{ID: chatTestCompanionID(1), Name: "阿木"}}
	host, client, _ := companionManagerHostReady(t, definitions, nil)
	dialogue := newFakeDialogueModel(t)
	dialogue.holdRequests()
	host.world.companionManager.replaceDialogueForTest(t, dialogue)
	issuerIdentity := integrationIdentity(0x71, "发令者")
	injectRunningCompanionTask(t, host, definitions[0].ID, stopTestIssuer(issuerIdentity),
		"跟着我", []companion.PlanStep{{Kind: companion.PlanStepFollow, PlayerID: issuerIdentity.PlayerID}})
	// 同 T2：让身体缓存先于派发就绪。
	warmup := host.world.StepForTest()
	receiveCompanionChatTick(t, client, warmup.Tick)

	host.world.stepMu.Lock()
	host.world.companionManager.requestDialogue(
		definitions[0].ID, companion.DialogueNode{Kind: companion.DialogueNodeStart})
	host.world.stepMu.Unlock()
	waitForDialogueRequests(t, dialogue, 1)

	// 结果在途期间任务进入终态（清槽、世代不变且无队首可提升）。
	host.world.stepMu.Lock()
	slot := host.world.companionManager.slots[definitions[0].ID]
	if len(slot.queue.FailRun(companion.TaskFailPathUnreachable)) != 1 {
		host.world.stepMu.Unlock()
		t.Fatal("构造任务终态失败：FailRun 未产生事件")
	}
	host.world.stepMu.Unlock()

	dialogue.releaseRequests()
	for range 10 {
		result := host.world.StepForTest()
		receiveCompanionChatTick(t, client, result.Tick)
	}
	effects, inFlight := dialogueEffectCount(t, host, definitions[0].ID)
	if effects != 0 {
		t.Fatalf("过时结果产生了副作用：effects=%d，想要 0", effects)
	}
	if inFlight {
		t.Fatal("过时结果未清除在途标记")
	}
	if requests, _, _ := dialogue.snapshotCounts(); requests != 1 {
		t.Fatalf("过时结果触发了新请求=%d", requests)
	}
}

// TestCompanionDialogueGenerationBumpDiscardsOutcome 验证第一级过时判定：
// 结果在途期间 FIFO 队首被提升（BeginHead 递增世代），迟到结果按世代不匹配
// 丢弃——不广播、不写摘要（对话效果哨兵为 0）、在途标记照常清除。与
// TestCompanionDialogueStaleOutcomeDiscarded（第二级「终态后丢弃」）互补，
// 共同覆盖两级过时判定。新任务挂在阻塞的假 planner 上（规划在途、不会
// 产生额外台词节点），隔离世代这一唯一变量。
func TestCompanionDialogueGenerationBumpDiscardsOutcome(t *testing.T) {
	definitions := []companion.Definition{{ID: chatTestCompanionID(1), Name: "阿木"}}
	host, client, _ := companionManagerHostReady(t, definitions, nil)
	planner := newFakeCompanionModel(t)
	planner.holdRequests()
	host.world.companionManager.replacePlannerForTest(t, planner)
	dialogue := newFakeDialogueModel(t)
	dialogue.holdRequests()
	host.world.companionManager.replaceDialogueForTest(t, dialogue)
	issuerIdentity := integrationIdentity(0x71, "发令者")
	issuer := stopTestIssuer(issuerIdentity)
	injectRunningCompanionTask(t, host, definitions[0].ID, issuer,
		"跟着我", []companion.PlanStep{{Kind: companion.PlanStepFollow, PlayerID: issuerIdentity.PlayerID}})
	// 与 StaleOutcomeDiscarded 相同：让身体缓存先于派发就绪。
	warmup := host.world.StepForTest()
	receiveCompanionChatTick(t, client, warmup.Tick)

	host.world.stepMu.Lock()
	host.world.companionManager.requestDialogue(
		definitions[0].ID, companion.DialogueNode{Kind: companion.DialogueNodeStart})
	host.world.stepMu.Unlock()
	waitForDialogueRequests(t, dialogue, 1)

	// 结果在途期间：任务终态清槽（终态节点因在途被跳过）、FIFO 入队下一条
	// 并提升队首——世代递增是本场景的唯一受控变量。
	host.world.stepMu.Lock()
	slot := host.world.companionManager.slots[definitions[0].ID]
	if len(slot.queue.FailRun(companion.TaskFailPathUnreachable)) != 1 {
		host.world.stepMu.Unlock()
		t.Fatal("构造任务终态失败：FailRun 未产生事件")
	}
	if !slot.queue.Enqueue(companion.TaskCommand("下一个任务")) {
		host.world.stepMu.Unlock()
		t.Fatal("构造待执行指令失败")
	}
	if !slot.queue.BeginHead() {
		host.world.stepMu.Unlock()
		t.Fatal("队首提升失败")
	}
	host.world.stepMu.Unlock()

	dialogue.releaseRequests()
	for range 10 {
		result := host.world.StepForTest()
		receiveCompanionChatTick(t, client, result.Tick)
	}
	effects, inFlight := dialogueEffectCount(t, host, definitions[0].ID)
	if effects != 0 {
		t.Fatalf("世代不匹配的结果产生了副作用：effects=%d，想要 0", effects)
	}
	if inFlight {
		t.Fatal("世代不匹配的结果未清除在途标记")
	}
	// 释放挂起的规划请求，让关服路径干净收敛。
	planner.releaseRequests()
}

// TestCompanionDialogueFailureSkipsOnlyLine 验证台词模型持续失败（5xx）只跳过
// 台词：同一任务场景在有/无台词两条运行下，任务事实 ChatEvent 序列完全一致。
// D6 起触发节点由 manager 自动接线（进入 Running、选中步骤完成、终态），
// 不再依赖测试手动派发。
func TestCompanionDialogueFailureSkipsOnlyLine(t *testing.T) {
	runScenario := func(withDialogue bool) []network.ChatEvent {
		definitions := []companion.Definition{{ID: chatTestCompanionID(1), Name: "阿木"}}
		host, client, body := companionManagerHostReady(t, definitions, nil)
		baseX, baseZ := int32(body.Position[0]), int32(body.Position[2])
		planner := newFakeCompanionModel(t,
			[3]int32{baseX + 2, 1, baseZ},
			[3]int32{baseX + 4, 1, baseZ},
		)
		host.world.companionManager.replacePlannerForTest(t, planner)
		var dialogue *fakeDialogueModel
		if withDialogue {
			dialogue = newFakeDialogueModel(t)
			dialogue.setStatus(http.StatusInternalServerError)
			host.world.companionManager.replaceDialogueForTest(t, dialogue)
		}

		sendIntegration(t, client, network.ChatCommand{Text: "@阿木 走两步"})
		waitForIncomingChatDepth(t, host.world, 1)
		var events []network.ChatEvent
		for range 400 {
			result := host.world.StepForTest()
			events = append(events,
				companionChatEvents(receiveCompanionChatTick(t, client, result.Tick))...)
			if countKind(events, network.ChatEventTaskCompleted) == 1 {
				break
			}
		}
		if withDialogue {
			if requests, _, _ := dialogue.snapshotCounts(); requests == 0 {
				t.Fatal("持续失败场景未发起任何台词请求")
			}
			if effects, _ := dialogueEffectCount(t, host, definitions[0].ID); effects != 0 {
				t.Fatalf("失败结果产生了副作用：effects=%d", effects)
			}
		}
		return events
	}

	baseline := runScenario(false)
	withDialogue := runScenario(true)
	if len(baseline) == 0 || countKind(baseline, network.ChatEventTaskCompleted) != 1 {
		t.Fatalf("基准场景未完成：事件=%v", chatEventKinds(baseline))
	}
	if len(baseline) != len(withDialogue) {
		t.Fatalf("事实事件序列长度不一致：无台词=%v 有台词=%v",
			chatEventKinds(baseline), chatEventKinds(withDialogue))
	}
	for index := range baseline {
		if baseline[index].Kind != withDialogue[index].Kind ||
			baseline[index].Command != withDialogue[index].Command ||
			baseline[index].RejectReason != withDialogue[index].RejectReason {
			t.Fatalf("事件 %d 不一致：无台词=%+v 有台词=%+v", index,
				baseline[index], withDialogue[index])
		}
	}
}

// TestCompanionDialogueSlowModelDoesNotBlockTicks 验证四个伙伴各有在途台词
// 请求且模型挂起时权威 tick 照常按节拍推进（对齐 Planner 的同款测试）。
func TestCompanionDialogueSlowModelDoesNotBlockTicks(t *testing.T) {
	definitions := []companion.Definition{
		{ID: chatTestCompanionID(1), Name: "阿木"},
		{ID: chatTestCompanionID(2), Name: "阿木甲"},
		{ID: chatTestCompanionID(3), Name: "小石"},
		{ID: chatTestCompanionID(4), Name: "松果"},
	}
	host := newCompanionManagerHost(t, definitions, nil, nil)
	dialogue := newFakeDialogueModel(t)
	dialogue.holdRequests()
	host.world.companionManager.replaceDialogueForTest(t, dialogue)
	sender := openCompanionChatClient(t, host, "memory", integrationIdentity(0x93, "发令者"))
	waitForCompanionChatWorld(t, host, []network.ClientEndpoint{sender}, len(definitions))
	// 再推进一个 tick 让身体缓存就绪（waitForCompanionChatWorld 可能在身体激活
	// 的同一 tick 返回，manager 缓存按 tick 边界滞后一拍）。
	warmup := host.world.StepForTest()
	receiveCompanionChatTick(t, sender, warmup.Tick)

	host.world.stepMu.Lock()
	for _, definition := range definitions {
		host.world.companionManager.requestDialogue(
			definition.ID, companion.DialogueNode{Kind: companion.DialogueNodeStart})
	}
	host.world.stepMu.Unlock()
	waitForDialogueRequests(t, dialogue, len(definitions))
	if _, inFlightModel, _ := dialogue.snapshotCounts(); inFlightModel != len(definitions) {
		t.Fatalf("台词在途=%d，想要 %d", inFlightModel, len(definitions))
	}

	before := host.world.TickCount()
	started := time.Now()
	const extraTicks = 20
	for range extraTicks {
		result := host.world.StepForTest()
		receiveCompanionChatTick(t, sender, result.Tick)
	}
	elapsed := time.Since(started)
	if after := host.world.TickCount(); after-before != extraTicks {
		t.Fatalf("tick 推进=%d，想要 %d", after-before, extraTicks)
	}
	// 挂起的台词请求期间，20 个 tick 必须远快于真实节拍 1 秒；阻塞边界放宽到
	// 2 秒以容纳 race 检测下的抖动。
	if elapsed > 2*time.Second {
		t.Fatalf("挂起台词模型期间 %d tick 耗时=%v，权威 tick 被阻塞", extraTicks, elapsed)
	}
	dialogue.releaseRequests()
}

// TestCompanionDialogueShutdownCancelsInFlight 验证关服取消在途台词请求：
// manager ctx 取消令 HTTP 调用返回、worker 释放槽位并随 waitGroup 收敛，无
// goroutine 泄漏（Shutdown 返回即 close 完成等待）。
func TestCompanionDialogueShutdownCancelsInFlight(t *testing.T) {
	definitions := []companion.Definition{{ID: chatTestCompanionID(1), Name: "阿木"}}
	dialogue := newFakeDialogueModel(t)
	dialogue.holdRequests()

	config := hostTestConfig()
	config.Companions = definitions
	config.MaxPlayers = 1
	config.OutboxCapacity = 4096
	config.HeartbeatInterval = time.Hour
	config.HeartbeatTimeout = time.Hour
	host, err := NewHost(context.Background(), config, flatTestGenerator{}, newHostTestStore())
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	client := openCompanionChatClient(t, host, "memory", integrationIdentity(0x94, "发令者"))
	waitForCompanionChatWorld(t, host, []network.ClientEndpoint{client}, 1)
	host.world.companionManager.replaceDialogueForTest(t, dialogue)
	// 再推进一个 tick 让身体缓存就绪（见 TestCompanionDialogueSlowModelDoesNotBlockTicks）。
	warmup := host.world.StepForTest()
	receiveCompanionChatTick(t, client, warmup.Tick)

	host.world.stepMu.Lock()
	host.world.companionManager.requestDialogue(
		definitions[0].ID, companion.DialogueNode{Kind: companion.DialogueNodeStart})
	host.world.stepMu.Unlock()
	waitForDialogueRequests(t, dialogue, 1)

	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
	defer cancel()
	if err := host.Shutdown(ctx); err != nil {
		t.Fatalf("Host.Shutdown: %v", err)
	}
	if _, _, cancels := dialogue.snapshotCounts(); cancels == 0 {
		t.Fatal("关服未取消在途台词请求")
	}
	// 取消后假模型的 handler 必须全部退出（在途归零），worker 与槽位随之收敛。
	waitIntegrationCondition(t, "台词模型在途归零", func() bool {
		_, inFlight, _ := dialogue.snapshotCounts()
		return inFlight == 0
	})
	if requests, _, _ := dialogue.snapshotCounts(); requests != 1 {
		t.Fatalf("关服期间台词请求数=%d，想要 1（取消不得重试）", requests)
	}
}

// TestCompanionDialogueDispatchRejectsUnknownSlot 覆盖派发入口的防御守卫：
// 未知槽位直接跳过，不崩溃、不占用共享槽、不产生任何模型请求。
func TestCompanionDialogueDispatchRejectsUnknownSlot(t *testing.T) {
	definitions := []companion.Definition{{ID: chatTestCompanionID(1), Name: "阿木"}}
	host, client, _ := companionManagerHostReady(t, definitions, nil)
	dialogue := newFakeDialogueModel(t)
	host.world.companionManager.replaceDialogueForTest(t, dialogue)

	host.world.stepMu.Lock()
	host.world.companionManager.requestDialogue(
		companion.ID{}, companion.DialogueNode{Kind: companion.DialogueNodeStart})
	host.world.stepMu.Unlock()
	result := host.world.StepForTest()
	receiveCompanionChatTick(t, client, result.Tick)
	if requests, _, _ := dialogue.snapshotCounts(); requests != 0 {
		t.Fatalf("未知槽位产生了台词请求=%d", requests)
	}
	// 共享信号量未被占用：后续任务规划照常获得槽位。
	host.world.stepMu.Lock()
	used := len(host.world.companionManager.semaphore)
	host.world.stepMu.Unlock()
	if used != 0 {
		t.Fatalf("未知槽位派发占用了共享模型槽：len=%d", used)
	}
}
