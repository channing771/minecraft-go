package server

import (
	"bytes"
	"context"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/sim"
)

func TestChatCommandAddressesExactConfiguredCompanionAtTickBoundary(t *testing.T) {
	definitions := chatTestDefinitions()
	host := newCompanionChatHost(t, definitions, 1)
	client := openCompanionChatClient(t, host, "memory", integrationIdentity(0x41, "发送者"))
	waitForCompanionChatWorld(t, host, []network.ClientEndpoint{client}, len(definitions))

	sendIntegration(t, client, network.ChatCommand{Text: "@阿木 挖石头"})
	waitForIncomingChatDepth(t, host.world, 1)
	host.world.stepMu.Lock()
	if got := host.world.nextChatEventID; got != 0 {
		host.world.stepMu.Unlock()
		t.Fatalf("tick 前 nextChatEventID=%d，想要 0", got)
	}
	host.world.stepMu.Unlock()

	result := host.world.StepForTest()
	messages := receiveCompanionChatTick(t, client, result.Tick)
	events := companionChatEvents(messages)
	if len(events) != 1 {
		t.Fatalf("ChatEvent=%v，想要 1 条", events)
	}
	want := network.ChatEvent{
		EventID:       1,
		PlayerID:      integrationIdentity(0x41, "发送者").PlayerID,
		PlayerName:    "发送者",
		CompanionID:   definitions[0].ID,
		CompanionName: "阿木",
		Kind:          network.ChatEventAccepted,
		RejectReason:  network.ChatRejectNone,
		Command:       "挖石头",
	}
	if !reflect.DeepEqual(events[0], want) {
		t.Fatalf("ChatEvent=%+v，想要 %+v", events[0], want)
	}
	if err := events[0].Validate(); err != nil {
		t.Fatalf("ChatEvent.Validate: %v", err)
	}
	if _, ok := messages[len(messages)-1].(network.PlayerState); !ok {
		t.Fatalf("tick 尾消息=%T，想要 PlayerState", messages[len(messages)-1])
	}
	if indexOfCompanionChatEvent(messages, events[0].EventID) >= len(messages)-1 {
		t.Fatal("ChatEvent 没有排在 tick 尾 PlayerState 之前")
	}

	// 相邻配置不能把合法前缀误当成伙伴名称。
	sendIntegration(t, client, network.ChatCommand{Text: "@阿 挖石头"})
	waitForIncomingChatDepth(t, host.world, 1)
	result = host.world.StepForTest()
	events = companionChatEvents(receiveCompanionChatTick(t, client, result.Tick))
	if len(events) != 1 || events[0].RejectReason != network.ChatRejectUnknownCompanion ||
		events[0].CompanionName != "阿" {
		t.Fatalf("前缀寻址事件=%+v，想要 UnknownCompanion(阿)", events)
	}
}

func TestMalformedOrUnknownCompanionChatRejectsOnlySender(t *testing.T) {
	server := newCompanionChatRoutingServer(t, chatTestDefinitions(), map[sim.SessionID]int{
		1: 16,
		2: 16,
	})
	cases := []struct {
		text      string
		reason    network.ChatRejectReason
		companion string
	}{
		{text: "阿木 挖石头", reason: network.ChatRejectInvalidFormat},
		{text: "@阿木", reason: network.ChatRejectInvalidFormat},
		{text: "@ 挖石头", reason: network.ChatRejectInvalidFormat},
		{text: "@不存在 看看", reason: network.ChatRejectUnknownCompanion, companion: "不存在"},
	}
	for index, test := range cases {
		server.incomingChats <- incomingChat{
			sessionID: 1, generation: 1, command: network.ChatCommand{Text: test.text},
		}
		deliveries := server.drainIncomingChats()
		if len(deliveries) != 1 {
			t.Fatalf("case %d delivery=%d，想要 1", index, len(deliveries))
		}
		delivery := deliveries[0]
		wantID := uint64(index + 1)
		if delivery.recipient != 1 || delivery.event.EventID != wantID ||
			delivery.event.PlayerID != publicationPlayerID(1) ||
			delivery.event.PlayerName != "Player-1" ||
			delivery.event.Kind != network.ChatEventRejected ||
			delivery.event.RejectReason != test.reason ||
			delivery.event.CompanionID != (companion.ID{}) ||
			delivery.event.CompanionName != test.companion ||
			delivery.event.Command != "" {
			t.Fatalf("case %d delivery=%+v", index, delivery)
		}
		if err := delivery.event.Validate(); err != nil {
			t.Fatalf("case %d ChatEvent.Validate: %v", index, err)
		}
		server.publishWithChats(sim.TickResult{}, deliveries)
		if got := companionChatEvents(drainCompanionChatOutbox(server.sessions[1])); len(got) != 1 || got[0].EventID != wantID {
			t.Fatalf("case %d sender events=%v", index, got)
		}
		if got := companionChatEvents(drainCompanionChatOutbox(server.sessions[2])); len(got) != 0 {
			t.Fatalf("case %d observer 收到拒绝事件=%v", index, got)
		}
	}
}

func TestCompanionAddressNameBoundaryIsThirtyTwoRunesAnd128Bytes(t *testing.T) {
	server := newCompanionChatRoutingServer(t, nil, map[sim.SessionID]int{1: 16})
	cases := []struct {
		name   string
		reason network.ChatRejectReason
	}{
		{name: strings.Repeat("A", 32), reason: network.ChatRejectUnknownCompanion},
		{name: strings.Repeat("A", 33), reason: network.ChatRejectInvalidFormat},
		{name: strings.Repeat("😀", 32), reason: network.ChatRejectUnknownCompanion},
		{name: strings.Repeat("😀", 32) + "A", reason: network.ChatRejectInvalidFormat},
	}
	for index, test := range cases {
		text := "@" + test.name + " 指令"
		name, command, parseReason := parseCompanionAddress(text)
		if test.reason == network.ChatRejectUnknownCompanion {
			if parseReason != network.ChatRejectNone || name != test.name || command != "指令" {
				t.Fatalf("case %d parse=(%q,%q,%d)", index, name, command, parseReason)
			}
		} else if parseReason != network.ChatRejectInvalidFormat || name != "" || command != "" {
			t.Fatalf("case %d invalid parse=(%q,%q,%d)", index, name, command, parseReason)
		}

		server.incomingChats <- incomingChat{
			sessionID: 1, generation: 1, command: network.ChatCommand{Text: text},
		}
		deliveries := server.drainIncomingChats()
		if len(deliveries) != 1 || deliveries[0].event.RejectReason != test.reason {
			t.Fatalf("case %d delivery=%+v，想要 reason=%d", index, deliveries, test.reason)
		}
		wantName := ""
		if test.reason == network.ChatRejectUnknownCompanion {
			wantName = test.name
		}
		if deliveries[0].event.CompanionName != wantName ||
			deliveries[0].event.CompanionID != (companion.ID{}) ||
			deliveries[0].event.Command != "" {
			t.Fatalf("case %d 拒绝字段泄漏：%+v", index, deliveries[0].event)
		}
	}
}

func TestCompanionAddressRejectsControlAndUsesUnicodeSpaceDelimiter(t *testing.T) {
	invalid := []string{
		"@阿木\x00挖石头",
		"@阿木\n挖石头",
		"@阿木\t挖石头",
		" @阿木 挖石头",
		"@阿木 挖石头 ",
		string([]byte{'@', 0xff, ' ', 'x'}),
	}
	for _, text := range invalid {
		name, command, reason := parseCompanionAddress(text)
		if name != "" || command != "" || reason != network.ChatRejectInvalidFormat {
			t.Fatalf("parseCompanionAddress(%q)=(%q,%q,%d)", text, name, command, reason)
		}
	}

	cases := []struct {
		text    string
		name    string
		command string
	}{
		{text: "@阿木  挖  石头", name: "阿木", command: "挖  石头"},
		{text: "@阿木\u3000\u3000挖石头", name: "阿木", command: "挖石头"},
		{text: "@阿 木 指令", name: "阿", command: "木 指令"},
	}
	for _, test := range cases {
		name, command, reason := parseCompanionAddress(test.text)
		if name != test.name || command != test.command || reason != network.ChatRejectNone {
			t.Fatalf("parseCompanionAddress(%q)=(%q,%q,%d)", test.text, name, command, reason)
		}
	}
}

func TestAcceptedCompanionChatBroadcastsInChannelOrder(t *testing.T) {
	server := newCompanionChatRoutingServer(t, chatTestDefinitions(), map[sim.SessionID]int{
		1: 16,
		2: 16,
		3: 16,
	})
	for _, text := range []string{
		"不是寻址",
		"@阿木 挖石头",
		"@不存在 看看",
		"@阿木甲 等待",
	} {
		server.incomingChats <- incomingChat{
			sessionID: 1, generation: 1, command: network.ChatCommand{Text: text},
		}
	}
	deliveries := server.drainIncomingChats()
	if len(deliveries) != 4 {
		t.Fatalf("deliveries=%d，想要 4", len(deliveries))
	}
	for index, delivery := range deliveries {
		if delivery.event.EventID != uint64(index+1) {
			t.Fatalf("delivery[%d].EventID=%d", index, delivery.event.EventID)
		}
		if err := delivery.event.Validate(); err != nil {
			t.Fatalf("delivery[%d].Validate: %v", index, err)
		}
	}
	server.publishWithChats(sim.TickResult{}, deliveries)

	first := companionChatEvents(drainCompanionChatOutbox(server.sessions[1]))
	second := companionChatEvents(drainCompanionChatOutbox(server.sessions[2]))
	third := companionChatEvents(drainCompanionChatOutbox(server.sessions[3]))
	assertCompanionChatEventIDs(t, first, []uint64{1, 2, 3, 4})
	assertCompanionChatEventIDs(t, second, []uint64{2, 4})
	assertCompanionChatEventIDs(t, third, []uint64{2, 4})
	if !reflect.DeepEqual(second, third) || !reflect.DeepEqual(second, []network.ChatEvent{first[1], first[3]}) {
		t.Fatalf("广播事件不一致\nsender=%+v\nsecond=%+v\nthird=%+v", first, second, third)
	}
}

func TestAcceptedCompanionChatSlowRecipientDoesNotBlockHealthyBroadcast(t *testing.T) {
	server := newCompanionChatRoutingServer(t, chatTestDefinitions(), map[sim.SessionID]int{
		1: 4,
		2: 1,
		3: 4,
	})
	server.sessions[2].outbox <- network.KeepAlive{Token: 99}
	server.incomingChats <- incomingChat{
		sessionID: 1, generation: 1,
		command: network.ChatCommand{Text: "@阿木 挖石头"},
	}
	server.publishWithChats(sim.TickResult{}, server.drainIncomingChats())

	first := companionChatEvents(drainCompanionChatOutbox(server.sessions[1]))
	third := companionChatEvents(drainCompanionChatOutbox(server.sessions[3]))
	if len(first) != 1 || len(third) != 1 || !reflect.DeepEqual(first[0], third[0]) {
		t.Fatalf("慢接收者隔离后健康广播不一致：first=%+v third=%+v", first, third)
	}
	if server.sessions[2] != nil {
		t.Fatal("outbox 满的 session 未按终态语义移除")
	}

	server.incomingChats <- incomingChat{
		sessionID: 1, generation: 1,
		command: network.ChatCommand{Text: "@阿木甲 等待"},
	}
	server.publishWithChats(sim.TickResult{}, server.drainIncomingChats())
	first = companionChatEvents(drainCompanionChatOutbox(server.sessions[1]))
	third = companionChatEvents(drainCompanionChatOutbox(server.sessions[3]))
	if len(first) != 1 || len(third) != 1 || first[0].EventID != 2 ||
		!reflect.DeepEqual(first[0], third[0]) {
		t.Fatalf("后续健康广播：first=%+v third=%+v", first, third)
	}
}

func TestStaleSessionChatGenerationIsDroppedWithoutConsumingEventID(t *testing.T) {
	server := newCompanionChatRoutingServer(t, chatTestDefinitions(), map[sim.SessionID]int{1: 8})
	server.sessions[1].generation = 2
	server.incomingChats <- incomingChat{
		sessionID: 1, generation: 1,
		command: network.ChatCommand{Text: "即使解析也非法"},
	}
	if deliveries := server.drainIncomingChats(); len(deliveries) != 0 {
		t.Fatalf("stale delivery=%+v，想要丢弃", deliveries)
	}
	if server.nextChatEventID != 0 {
		t.Fatalf("stale 输入消耗 EventID：%d", server.nextChatEventID)
	}

	server.incomingChats <- incomingChat{
		sessionID: 1, generation: 2,
		command: network.ChatCommand{Text: "@阿木 挖石头"},
	}
	deliveries := server.drainIncomingChats()
	if len(deliveries) != 1 || deliveries[0].event.EventID != 1 ||
		deliveries[0].event.Kind != network.ChatEventAccepted {
		t.Fatalf("fresh delivery=%+v，想要首个 Accepted EventID=1", deliveries)
	}

	server.nextChatEventID = ^uint64(0) - 1
	server.incomingChats <- incomingChat{
		sessionID: 1, generation: 2,
		command: network.ChatCommand{Text: "@阿木 最后编号"},
	}
	server.incomingChats <- incomingChat{
		sessionID: 1, generation: 2,
		command: network.ChatCommand{Text: "@阿木 编号耗尽"},
	}
	deliveries = server.drainIncomingChats()
	if len(deliveries) != 1 || deliveries[0].event.EventID != ^uint64(0) {
		t.Fatalf("EventID 耗尽 delivery=%+v，想要唯一 MaxUint64 事件", deliveries)
	}
	if server.nextChatEventID != ^uint64(0) || server.sessions[1] != nil {
		t.Fatalf("EventID 耗尽未 fail-closed：next=%d session=%v",
			server.nextChatEventID, server.sessions[1] != nil)
	}
}

func TestChatCommandIngressIsBoundedAndCancellationWakesBlockedReader(t *testing.T) {
	t.Run("cancellation", func(t *testing.T) {
		server := newCompanionChatRoutingServer(t, chatTestDefinitions(), map[sim.SessionID]int{1: 8})
		if got := cap(server.incomingChats); got != inputCapacity || inputCapacity != 256 {
			t.Fatalf("incomingChats cap=%d，想要 %d", cap(server.incomingChats), inputCapacity)
		}
		for range inputCapacity {
			server.incomingChats <- incomingChat{sessionID: 1, generation: 1}
		}
		ctx, cancel := context.WithCancel(context.Background())
		started := make(chan struct{})
		done := make(chan struct{})
		go func() {
			close(started)
			server.enqueueIncomingChat(ctx, incomingChat{sessionID: 1, generation: 1})
			close(done)
		}()
		<-started
		select {
		case <-done:
			t.Fatal("满 ingress 错误丢弃了 command")
		case <-time.After(10 * time.Millisecond):
		}
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("session context 取消后 enqueueIncomingChat 未返回")
		}
		if len(server.incomingChats) != inputCapacity {
			t.Fatalf("取消后 ingress len=%d，想要 %d", len(server.incomingChats), inputCapacity)
		}
	})

	t.Run("tick-snapshot", func(t *testing.T) {
		oldProcs := runtime.GOMAXPROCS(1)
		defer runtime.GOMAXPROCS(oldProcs)
		server := newCompanionChatRoutingServer(t, chatTestDefinitions(), map[sim.SessionID]int{1: 8})
		for range inputCapacity {
			server.incomingChats <- incomingChat{
				sessionID: 1, generation: 1,
				command: network.ChatCommand{Text: "@阿木 指令"},
			}
		}
		started := make(chan struct{})
		done := make(chan struct{})
		go func() {
			close(started)
			server.enqueueIncomingChat(context.Background(), incomingChat{
				sessionID: 1, generation: 1,
				command: network.ChatCommand{Text: "@阿木 下一 tick"},
			})
			close(done)
		}()
		<-started
		runtime.Gosched()
		deliveries := server.drainIncomingChats()
		<-done
		if len(deliveries) != inputCapacity || len(server.incomingChats) != 1 {
			t.Fatalf("首 tick deliveries=%d remaining=%d", len(deliveries), len(server.incomingChats))
		}
		next := server.drainIncomingChats()
		if len(next) != 1 || next[0].event.EventID != inputCapacity+1 ||
			next[0].event.Command != "下一 tick" {
			t.Fatalf("下一 tick delivery=%+v", next)
		}
	})
}

func TestChatCommandDoesNotMutateSimulationOrCreateCommand(t *testing.T) {
	definitions := chatTestDefinitions()[:1]
	host := newCompanionChatHost(t, definitions, 1)
	client := openCompanionChatClient(t, host, "memory", integrationIdentity(0x51, "观察员"))
	waitForCompanionChatWorld(t, host, []network.ClientEndpoint{client}, 1)
	// 再推进一个静止 tick，确保比较起点不含出生落地瞬态。
	result := host.world.StepForTest()
	receiveCompanionChatTick(t, client, result.Tick)

	active := activeLoginForPlayer(t, host, integrationIdentity(0x51, "观察员").PlayerID)
	beforeBodies := host.world.engine.CompanionBodies()
	beforePlayer, ok := host.world.PlayerSnapshotFor(active.Session)
	if !ok || len(beforeBodies) != 1 {
		t.Fatalf("比较前 bodies=%d player=%v", len(beforeBodies), ok)
	}
	foot := core.ChunkPos{
		X: int32(beforeBodies[0].Position[0]) >> 4,
		Z: int32(beforeBodies[0].Position[2]) >> 4,
	}
	beforeHash, beforeRevision, ok := host.world.ChunkHash(core.Overworld, foot)
	if !ok {
		t.Fatalf("伙伴脚下区块 %+v 未就绪", foot)
	}

	sendIntegration(t, client, network.ChatCommand{Text: "@阿木 挖石头"})
	waitForIncomingChatDepth(t, host.world, 1)
	result = host.world.StepForTest()
	events := companionChatEvents(receiveCompanionChatTick(t, client, result.Tick))
	if len(events) != 1 || events[0].Kind != network.ChatEventAccepted {
		t.Fatalf("事实事件=%+v", events)
	}
	if len(result.Rejected) != 0 {
		t.Fatalf("聊天伪装成 sim command rejection：%+v", result.Rejected)
	}
	afterBodies := host.world.engine.CompanionBodies()
	afterPlayer, ok := host.world.PlayerSnapshotFor(active.Session)
	if !ok || !reflect.DeepEqual(afterBodies, beforeBodies) || !reflect.DeepEqual(afterPlayer, beforePlayer) {
		t.Fatalf("聊天改变权威身体\nbeforeBodies=%+v\nafterBodies=%+v\nbeforePlayer=%+v\nafterPlayer=%+v",
			beforeBodies, afterBodies, beforePlayer, afterPlayer)
	}
	afterHash, afterRevision, ok := host.world.ChunkHash(core.Overworld, foot)
	if !ok || afterHash != beforeHash || afterRevision != beforeRevision {
		t.Fatalf("聊天改变区块：before=%x/%d after=%x/%d ok=%v",
			beforeHash, beforeRevision, afterHash, afterRevision, ok)
	}

	source, err := os.ReadFile("companion_chat.go")
	if err != nil {
		t.Fatalf("读取 companion_chat.go: %v", err)
	}
	if bytes.Contains(source, []byte("sim.Command")) || bytes.Contains(source, []byte("engine.Enqueue")) {
		t.Fatal("companion_chat.go 不得构造 sim.Command 或调用 engine.Enqueue")
	}
}

// injectRunningCompanionTask 在 stepMu 下把一条已进入 Running 的当前任务连同
// 后续 pending 指令直接注入伙伴槽位（等价于 enqueueCommand → BeginHead →
// BeginPlanning → AcceptPlan → FinishValidation 的手工前缀），供停止旁路测试
// 构造「当前持续跟随任务」现场。发令者事实与 issuers 配对同步补齐，保证停止
// 后 dispatchPlanning 提升原队首时能消费到配对条目；注入后不得再推进 tick，
// 停止指令必须在本 tick 的聊天 drain 中先于任务编排被处理。
func injectRunningCompanionTask(
	t *testing.T,
	host *Host,
	id companion.ID,
	issuer companionTaskIssuer,
	command string,
	steps []companion.PlanStep,
	pending ...string,
) {
	t.Helper()
	host.world.stepMu.Lock()
	defer host.world.stepMu.Unlock()
	slot := host.world.companionManager.slots[id]
	if slot == nil {
		t.Fatalf("伙伴 %s 没有任务槽位", id)
	}
	if !slot.queue.Enqueue(companion.TaskCommand(command)) || !slot.queue.BeginHead() ||
		!slot.queue.BeginPlanning() {
		t.Fatal("构造当前任务失败")
	}
	slot.queue.AcceptPlan(companion.Plan{Summary: "注入计划", Steps: steps})
	events := slot.queue.FinishValidation(host.world.engine.WorldTime(), 10)
	if len(events) != 1 || events[0].Kind != companion.TaskEventStarted {
		t.Fatalf("注入任务未进入 Running：%v", events)
	}
	slot.currentCommand = companion.TaskCommand(command)
	slot.currentIssuer = issuer
	for _, text := range pending {
		if !slot.queue.Enqueue(companion.TaskCommand(text)) {
			t.Fatalf("构造 pending 指令 %q 失败", text)
		}
		slot.issuers = append(slot.issuers, issuer)
	}
}

// stopTestIssuer 构造注入任务使用的有界发令者事实。
func stopTestIssuer(identity network.Identity) companionTaskIssuer {
	return companionTaskIssuer{
		playerID: identity.PlayerID,
		name:     identity.DisplayName,
		position: [3]float32{0, 1, 0},
	}
}

// TestChatCommandStopBypassOnRunningFollowTask 验证停止旁路的核心场景：当前
// 任务是 Running 的持续跟随（follow 尾步）且 FIFO 还有待执行指令时，
// `@伙伴名 停止` 不进入 FIFO、不产生 Accepted，而是广播唯一 TaskStopped
// （reason None、携带被停任务的原始指令与发令者身份），随后原队首在同 tick
// 立即开始、剩余队列保持不变。
func TestChatCommandStopBypassOnRunningFollowTask(t *testing.T) {
	definitions := []companion.Definition{{ID: chatTestCompanionID(1), Name: "阿木"}}
	// 假模型挂起全部请求：原队首被提升后停留在在途规划，避免模型噪声干扰
	// 停止事实的断言窗口。
	model := newFakeCompanionModel(t, [3]int32{0, 1, 0})
	model.holdRequests()
	host := newCompanionManagerHost(t, definitions, model, nil)
	stopper := openCompanionChatClient(t, host, "memory", integrationIdentity(0x83, "停止者"))
	observer := openCompanionChatClient(t, host, "memory", integrationIdentity(0x84, "旁观者"))
	clients := []network.ClientEndpoint{stopper, observer}
	waitForCompanionChatWorld(t, host, clients, 1)

	issuerIdentity := integrationIdentity(0x85, "原发令者")
	followTarget := issuerIdentity.PlayerID
	injectRunningCompanionTask(t, host, definitions[0].ID, stopTestIssuer(issuerIdentity),
		"跟着我", []companion.PlanStep{{Kind: companion.PlanStepFollow, PlayerID: followTarget}},
		"下一条", "第三条")

	sendIntegration(t, stopper, network.ChatCommand{Text: "@阿木 停止"})
	waitForIncomingChatDepth(t, host.world, 1)
	result := host.world.StepForTest()
	stopperEvents := companionChatEvents(receiveCompanionChatTick(t, stopper, result.Tick))
	observerEvents := companionChatEvents(receiveCompanionChatTick(t, observer, result.Tick))

	if len(stopperEvents) != 1 || len(observerEvents) != 1 {
		t.Fatalf("停止 tick 事件 sender=%v observer=%v，想要各 1 条 TaskStopped 广播",
			chatEventKinds(stopperEvents), chatEventKinds(observerEvents))
	}
	want := network.ChatEvent{
		EventID:       1,
		PlayerID:      issuerIdentity.PlayerID,
		PlayerName:    "原发令者",
		CompanionID:   definitions[0].ID,
		CompanionName: "阿木",
		Kind:          network.ChatEventTaskStopped,
		RejectReason:  network.ChatRejectNone,
		Command:       "跟着我",
	}
	if !reflect.DeepEqual(stopperEvents[0], want) {
		t.Fatalf("TaskStopped=%+v，想要 %+v", stopperEvents[0], want)
	}
	if !reflect.DeepEqual(observerEvents[0], want) {
		t.Fatalf("广播事件不一致：observer=%+v", observerEvents[0])
	}
	if err := stopperEvents[0].Validate(); err != nil {
		t.Fatalf("TaskStopped Validate: %v", err)
	}

	// 停止只终结当前任务：原队首在同 tick 的任务编排中被提升并派发规划
	// （挂起模型令其停留在 Planning），剩余 pending 顺序与成员保持不变。
	host.world.stepMu.Lock()
	slot := host.world.companionManager.slots[definitions[0].ID]
	snapshot := slot.queue.Snapshot()
	inFlight := slot.planningInFlight
	host.world.stepMu.Unlock()
	if !snapshot.HasCurrent || snapshot.Current.Command != companion.TaskCommand("下一条") ||
		snapshot.Current.State != companion.TaskPlanning {
		t.Fatalf("停止后原队首未立即开始：current=%+v has=%v",
			snapshot.Current, snapshot.HasCurrent)
	}
	if len(snapshot.Pending) != 1 || snapshot.Pending[0] != companion.TaskCommand("第三条") {
		t.Fatalf("停止后队列被改动：pending=%v，想要 [第三条]", snapshot.Pending)
	}
	if !inFlight {
		t.Fatal("原队首未被派发规划，未在同一 tick 开始处理")
	}
	// 停止本身绝不触碰模型：唯一请求属于「下一条」的规划（先等待它到达再核对
	// 总数，避免与异步 worker 竞争）。
	waitForModelRequests(t, model, 1)
	if requests, _, _, _ := model.snapshotCounts(); requests != 1 {
		t.Fatalf("模型请求数=%d，想要 1（停止旁路不得调用模型）", requests)
	}
}

// TestChatCommandStopRejectsNonFollowAndIdle 验证停止旁路的同步拒绝矩阵：
// 当前任务是普通 go_to（非持续跟随）或伙伴空闲时，`停止` 只向发令者单播
// NotFollowing（携带完整伙伴身份与指令），当前任务继续执行、队列不变，
// 绝不为停止创建 FIFO 任务。
func TestChatCommandStopRejectsNonFollowAndIdle(t *testing.T) {
	definitions := []companion.Definition{{ID: chatTestCompanionID(1), Name: "阿木"}}

	assertNotFollowing := func(t *testing.T, event network.ChatEvent, identity network.Identity) {
		t.Helper()
		want := network.ChatEvent{
			EventID:       event.EventID,
			PlayerID:      identity.PlayerID,
			PlayerName:    identity.DisplayName,
			CompanionID:   definitions[0].ID,
			CompanionName: "阿木",
			Kind:          network.ChatEventRejected,
			RejectReason:  network.ChatRejectNotFollowing,
			Command:       "停止",
		}
		if !reflect.DeepEqual(event, want) {
			t.Fatalf("NotFollowing 事件=%+v，想要 %+v", event, want)
		}
		if err := event.Validate(); err != nil {
			t.Fatalf("NotFollowing Validate: %v", err)
		}
	}

	t.Run("NonFollowRunningTask", func(t *testing.T) {
		model := newFakeCompanionModel(t, [3]int32{0, 1, 0})
		model.holdRequests()
		host := newCompanionManagerHost(t, definitions, model, nil)
		sender := openCompanionChatClient(t, host, "memory", integrationIdentity(0x86, "发令者"))
		observer := openCompanionChatClient(t, host, "memory", integrationIdentity(0x87, "旁观者"))
		waitForCompanionChatWorld(t, host, []network.ClientEndpoint{sender, observer}, 1)

		body := currentCompanionBody(t, host, definitions[0].ID)
		issuerIdentity := integrationIdentity(0x88, "原发令者")
		injectRunningCompanionTask(t, host, definitions[0].ID, stopTestIssuer(issuerIdentity),
			"去那边", []companion.PlanStep{{
				Kind: companion.PlanStepGoTo,
				X:    int32(body.Position[0]) + 2,
				Y:    1,
				Z:    int32(body.Position[2]),
			}})

		sendIntegration(t, sender, network.ChatCommand{Text: "@阿木 停止"})
		waitForIncomingChatDepth(t, host.world, 1)
		result := host.world.StepForTest()
		senderEvents := companionChatEvents(receiveCompanionChatTick(t, sender, result.Tick))
		observerEvents := companionChatEvents(receiveCompanionChatTick(t, observer, result.Tick))

		identity := integrationIdentity(0x86, "发令者")
		if len(senderEvents) != 1 || senderEvents[0].EventID != 1 {
			t.Fatalf("发令者事件=%+v，想要唯一 NotFollowing(EventID=1)", senderEvents)
		}
		assertNotFollowing(t, senderEvents[0], identity)
		if len(observerEvents) != 0 {
			t.Fatalf("NotFollowing 被广播：%v", chatEventKinds(observerEvents))
		}
		// 非跟随任务不受停止影响：保持 Running 继续执行，队列不变。
		host.world.stepMu.Lock()
		snapshot := host.world.companionManager.slots[definitions[0].ID].queue.Snapshot()
		host.world.stepMu.Unlock()
		if !snapshot.HasCurrent || snapshot.Current.State != companion.TaskRunning ||
			snapshot.Current.Command != companion.TaskCommand("去那边") {
			t.Fatalf("非跟随任务被停止改写：current=%+v has=%v",
				snapshot.Current, snapshot.HasCurrent)
		}
		if len(snapshot.Pending) != 0 {
			t.Fatalf("停止为非跟随任务改动队列：pending=%v", snapshot.Pending)
		}
	})

	t.Run("IdleCompanion", func(t *testing.T) {
		model := newFakeCompanionModel(t, [3]int32{0, 1, 0})
		model.holdRequests()
		host := newCompanionManagerHost(t, definitions, model, nil)
		sender := openCompanionChatClient(t, host, "memory", integrationIdentity(0x89, "发令者"))
		observer := openCompanionChatClient(t, host, "memory", integrationIdentity(0x8a, "旁观者"))
		waitForCompanionChatWorld(t, host, []network.ClientEndpoint{sender, observer}, 1)

		sendIntegration(t, sender, network.ChatCommand{Text: "@阿木 停止"})
		waitForIncomingChatDepth(t, host.world, 1)
		result := host.world.StepForTest()
		senderEvents := companionChatEvents(receiveCompanionChatTick(t, sender, result.Tick))
		observerEvents := companionChatEvents(receiveCompanionChatTick(t, observer, result.Tick))

		identity := integrationIdentity(0x89, "发令者")
		if len(senderEvents) != 1 || senderEvents[0].EventID != 1 {
			t.Fatalf("发令者事件=%+v，想要唯一 NotFollowing(EventID=1)", senderEvents)
		}
		assertNotFollowing(t, senderEvents[0], identity)
		if len(observerEvents) != 0 {
			t.Fatalf("NotFollowing 被广播：%v", chatEventKinds(observerEvents))
		}
		// 空闲伙伴的停止绝不创建任务或改动队列。
		host.world.stepMu.Lock()
		snapshot := host.world.companionManager.slots[definitions[0].ID].queue.Snapshot()
		host.world.stepMu.Unlock()
		if snapshot.HasCurrent || len(snapshot.Pending) != 0 {
			t.Fatalf("空闲伙伴被停止创建任务：has=%v pending=%v",
				snapshot.HasCurrent, snapshot.Pending)
		}
		if requests, _, _, _ := model.snapshotCounts(); requests != 0 {
			t.Fatalf("空闲停止触发模型请求=%d", requests)
		}
	})
}

// TestChatCommandStopNonExactTextEntersFIFO 验证非精确停止文本按普通指令进入
// FIFO（广播 Accepted、创建任务），绝不触发停止旁路；而 trim 后恰好等于
// 「停止」的寻址（多余分隔空白）仍走旁路——旁路判定基准与 Accepted 的指令
// 字段共用同一 trim 后文本。
func TestChatCommandStopNonExactTextEntersFIFO(t *testing.T) {
	definitions := []companion.Definition{{ID: chatTestCompanionID(1), Name: "阿木"}}
	model := newFakeCompanionModel(t, [3]int32{0, 1, 0})
	model.holdRequests()
	host := newCompanionManagerHost(t, definitions, model, nil)
	sender := openCompanionChatClient(t, host, "memory", integrationIdentity(0x8b, "发令者"))
	observer := openCompanionChatClient(t, host, "memory", integrationIdentity(0x8c, "旁观者"))
	waitForCompanionChatWorld(t, host, []network.ClientEndpoint{sender, observer}, 1)

	// 前两条是非精确文本（带参数/英文），第三条 trim 后精确等于「停止」。
	for _, text := range []string{"@阿木 停止移动", "@阿木 stop", "@阿木  停止"} {
		sendIntegration(t, sender, network.ChatCommand{Text: text})
	}
	waitForIncomingChatDepth(t, host.world, 3)
	result := host.world.StepForTest()
	senderEvents := companionChatEvents(receiveCompanionChatTick(t, sender, result.Tick))
	observerEvents := companionChatEvents(receiveCompanionChatTick(t, observer, result.Tick))

	identity := integrationIdentity(0x8b, "发令者")
	wantSender := []network.ChatEventKind{
		network.ChatEventAccepted,
		network.ChatEventAccepted,
		network.ChatEventRejected,
	}
	if !reflect.DeepEqual(chatEventKinds(senderEvents), wantSender) {
		t.Fatalf("发令者事件=%v，想要 %v", chatEventKinds(senderEvents), wantSender)
	}
	if !reflect.DeepEqual(chatEventKinds(observerEvents), wantSender[:2]) {
		t.Fatalf("旁观者事件=%v，想要两条 Accepted 广播", chatEventKinds(observerEvents))
	}
	for index, event := range senderEvents[:2] {
		if event.Kind != network.ChatEventAccepted || event.Command == "停止" {
			t.Fatalf("非精确文本事件[%d]=%+v，想要 Accepted 且携带原指令", index, event)
		}
		if err := event.Validate(); err != nil {
			t.Fatalf("Accepted[%d] Validate: %v", index, err)
		}
	}
	notFollowing := senderEvents[2]
	if notFollowing.RejectReason != network.ChatRejectNotFollowing ||
		notFollowing.Command != "停止" || notFollowing.PlayerID != identity.PlayerID {
		t.Fatalf("精确停止事件=%+v，想要 NotFollowing(停止)", notFollowing)
	}
	assertStrictlyIncreasingEventIDs(t, senderEvents)

	// 队列事实：非精确文本按接收顺序进入 FIFO；队首已被提升并在途规划
	//（挂起模型），「停止」本身不占任何槽位。
	host.world.stepMu.Lock()
	snapshot := host.world.companionManager.slots[definitions[0].ID].queue.Snapshot()
	host.world.stepMu.Unlock()
	if !snapshot.HasCurrent || snapshot.Current.Command != companion.TaskCommand("停止移动") ||
		len(snapshot.Pending) != 1 || snapshot.Pending[0] != companion.TaskCommand("stop") {
		t.Fatalf("FIFO 事实不符：current=%+v has=%v pending=%v",
			snapshot.Current, snapshot.HasCurrent, snapshot.Pending)
	}
}

func TestCompanionChatMemoryTCPParity(t *testing.T) {
	results := make(map[string][]companionChatTranscriptEvent, 2)
	for _, transport := range []string{"memory", "tcp"} {
		transport := transport
		t.Run(transport, func(t *testing.T) {
			results[transport] = runCompanionChatParity(t, transport)
		})
	}
	if !reflect.DeepEqual(results["memory"], results["tcp"]) {
		t.Fatalf("Memory/TCP 聊天 transcript 不一致\nMemory=%+v\nTCP=%+v",
			results["memory"], results["tcp"])
	}
	got := results["memory"]
	if len(got) != 6 {
		t.Fatalf("transcript=%+v，想要 6 条接收记录", got)
	}
	wantRecipients := []int{0, 0, 0, 0, 1, 1}
	wantIDs := []uint64{1, 2, 3, 4, 1, 4}
	for index := range got {
		if got[index].Recipient != wantRecipients[index] || got[index].Event.EventID != wantIDs[index] {
			t.Fatalf("transcript[%d]=%+v", index, got[index])
		}
		if err := got[index].Event.Validate(); err != nil {
			t.Fatalf("transcript[%d].Validate: %v", index, err)
		}
	}
	if got[0].Event.Kind != network.ChatEventAccepted ||
		got[1].Event.RejectReason != network.ChatRejectInvalidFormat ||
		got[2].Event.RejectReason != network.ChatRejectUnknownCompanion ||
		got[3].Event.Kind != network.ChatEventAccepted {
		t.Fatalf("事件分类错误：%+v", got)
	}
}

func BenchmarkChatRoutingFourCompanions(b *testing.B) {
	definitions := []companion.Definition{
		{ID: chatTestCompanionID(1), Name: "阿木"},
		{ID: chatTestCompanionID(2), Name: "阿木甲"},
		{ID: chatTestCompanionID(3), Name: "小石"},
		{ID: chatTestCompanionID(4), Name: "松果"},
	}
	server := benchmarkCompanionChatServer(definitions)
	command := incomingChat{
		sessionID: 1, generation: 1,
		command: network.ChatCommand{Text: "@阿木 挖石头"},
	}
	server.incomingChats <- command
	if deliveries := server.drainIncomingChats(); len(deliveries) != 1 {
		b.Fatalf("预热 delivery=%d", len(deliveries))
	}
	server.nextChatEventID = 0
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		server.incomingChats <- command
		if deliveries := server.drainIncomingChats(); len(deliveries) != 1 {
			b.Fatalf("delivery=%d", len(deliveries))
		}
	}
}

type companionChatTranscriptEvent struct {
	Recipient int
	Event     network.ChatEvent
}

func runCompanionChatParity(t *testing.T, transport string) []companionChatTranscriptEvent {
	t.Helper()
	definitions := chatTestDefinitions()
	host := newCompanionChatHost(t, definitions, 2)
	identities := []network.Identity{
		integrationIdentity(0x61, "发送者"),
		integrationIdentity(0x62, "观察者"),
	}
	clients := []network.ClientEndpoint{
		openCompanionChatClient(t, host, transport, identities[0]),
		openCompanionChatClient(t, host, transport, identities[1]),
	}
	waitForCompanionChatWorld(t, host, clients, len(definitions))

	texts := []string{
		"@阿木 挖石头",
		"阿木 挖石头",
		"@不存在 看看",
		"@阿木甲 等待",
	}
	for index, text := range texts {
		sendIntegration(t, clients[0], network.ChatCommand{Text: text})
		waitForIncomingChatDepth(t, host.world, index+1)
	}
	result := host.world.StepForTest()
	transcript := make([]companionChatTranscriptEvent, 0, 6)
	for recipient, endpoint := range clients {
		messages := receiveCompanionChatTick(t, endpoint, result.Tick)
		for _, event := range companionChatEvents(messages) {
			transcript = append(transcript, companionChatTranscriptEvent{
				Recipient: recipient,
				Event:     event,
			})
		}
	}
	return transcript
}

func newCompanionChatHost(
	t *testing.T,
	definitions []companion.Definition,
	maxPlayers int,
) *Host {
	t.Helper()
	config := hostTestConfig()
	config.Companions = append([]companion.Definition(nil), definitions...)
	config.MaxPlayers = maxPlayers
	config.OutboxCapacity = 4096
	config.HeartbeatInterval = time.Hour
	config.HeartbeatTimeout = time.Hour
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

func openCompanionChatClient(
	t *testing.T,
	host *Host,
	transport string,
	identity network.Identity,
) network.ClientEndpoint {
	t.Helper()
	endpoint, acceptDone, closeTransport := openParityTransport(t, host, transport, identity)
	t.Cleanup(func() {
		_ = endpoint.Close()
		closeTransport()
		select {
		case <-acceptDone:
		case <-time.After(longWaitDeadline):
			t.Errorf("%s AcceptStream cleanup 超时", transport)
		}
	})
	return endpoint
}

func waitForCompanionChatWorld(
	t *testing.T,
	host *Host,
	clients []network.ClientEndpoint,
	wantCompanions int,
) {
	t.Helper()
	deadline := time.Now().Add(longWaitDeadline)
	for time.Now().Before(deadline) {
		result := host.world.StepForTest()
		allReady := true
		for _, endpoint := range clients {
			messages := receiveCompanionChatTick(t, endpoint, result.Tick)
			state, ok := messages[len(messages)-1].(network.PlayerState)
			allReady = allReady && ok && state.Ready
		}
		if allReady && len(host.world.engine.CompanionBodies()) == wantCompanions {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("聊天测试世界未就绪：companions=%d/%d", len(host.world.engine.CompanionBodies()), wantCompanions)
}

func receiveCompanionChatTick(
	t *testing.T,
	endpoint network.ClientEndpoint,
	tick uint64,
) []network.ServerMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), longWaitDeadline)
	defer cancel()
	messages := make([]network.ServerMessage, 0, 16)
	for {
		message, err := endpoint.Recv(ctx)
		if err != nil {
			t.Fatalf("tick %d Recv: %v", tick, err)
		}
		messages = append(messages, message)
		if state, ok := message.(network.PlayerState); ok {
			if state.ServerTick != tick {
				t.Fatalf("PlayerState tick=%d，想要 %d", state.ServerTick, tick)
			}
			return messages
		}
	}
}

func waitForIncomingChatDepth(t *testing.T, server *Server, want int) {
	t.Helper()
	waitIntegrationCondition(t, "chat ingress depth", func() bool {
		return len(server.incomingChats) == want
	})
}

func newCompanionChatRoutingServer(
	t *testing.T,
	definitions []companion.Definition,
	capacities map[sim.SessionID]int,
) *Server {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	config := DefaultConfig(1)
	config.ViewRadius = 0
	config.Companions = append([]companion.Definition(nil), definitions...)
	running := &Server{
		config:           config,
		engine:           sim.NewEngine(0, 0),
		sessions:         make(map[sim.SessionID]*session, len(capacities)),
		playerSessions:   make(map[core.PlayerID]sim.SessionID, len(capacities)),
		ctx:              ctx,
		cancel:           cancel,
		incomingChats:    make(chan incomingChat, inputCapacity),
		companionsByName: make(map[string]companion.Definition, len(definitions)),
		lifecycle:        serverRunning,
	}
	for _, definition := range definitions {
		running.companionsByName[definition.Name] = definition
	}
	for id, capacity := range capacities {
		playerID := publicationPlayerID(byte(id))
		sessionCtx, sessionCancel := context.WithCancel(ctx)
		current := &session{
			id:                id,
			generation:        1,
			playerID:          playerID,
			displayName:       "Player-" + string(rune('0'+id)),
			endpoint:          newBlockingServerEndpoint(),
			ctx:               sessionCtx,
			cancel:            sessionCancel,
			outbox:            make(chan network.ServerMessage, capacity),
			exit:              make(chan SessionExit, 1),
			publications:      make(map[core.ChunkKey]*publication),
			pendingSnapshots:  make(map[core.ChunkKey]snapshotRequest),
			visiblePlayers:    make(map[core.PlayerID]visiblePlayer),
			visibleCompanions: make(map[companion.ID]struct{}),
			publishedDrops:    make(map[core.DropID]sim.DropSnapshot),
		}
		running.engine.RegisterObserverSession(id)
		running.sessions[id] = current
		running.playerSessions[playerID] = id
	}
	t.Cleanup(func() {
		for _, current := range running.sessions {
			current.shutdown()
		}
		cancel()
	})
	return running
}

func benchmarkCompanionChatServer(definitions []companion.Definition) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	currentCtx, currentCancel := context.WithCancel(ctx)
	current := &session{
		id: 1, generation: 1, playerID: publicationPlayerID(1), displayName: "Player-1",
		ctx: currentCtx, cancel: currentCancel,
	}
	running := &Server{
		ctx:              ctx,
		cancel:           cancel,
		sessions:         map[sim.SessionID]*session{1: current},
		incomingChats:    make(chan incomingChat, inputCapacity),
		companionsByName: make(map[string]companion.Definition, len(definitions)),
	}
	for _, definition := range definitions {
		running.companionsByName[definition.Name] = definition
	}
	return running
}

func chatTestDefinitions() []companion.Definition {
	return []companion.Definition{
		{ID: chatTestCompanionID(1), Name: "阿木"},
		{ID: chatTestCompanionID(2), Name: "阿木甲"},
	}
}

func chatTestCompanionID(suffix byte) companion.ID {
	return companion.ID{0, 0, 0, 0, 0, 0, 0x40, 0, 0x80, 0, 0, 0, 0, 0, 0, suffix}
}

func companionChatEvents(messages []network.ServerMessage) []network.ChatEvent {
	events := make([]network.ChatEvent, 0, len(messages))
	for _, message := range messages {
		if event, ok := message.(network.ChatEvent); ok {
			events = append(events, event)
		}
	}
	return events
}

func drainCompanionChatOutbox(current *session) []network.ServerMessage {
	messages := make([]network.ServerMessage, 0, len(current.outbox))
	for len(current.outbox) != 0 {
		messages = append(messages, <-current.outbox)
	}
	return messages
}

func assertCompanionChatEventIDs(t *testing.T, events []network.ChatEvent, want []uint64) {
	t.Helper()
	got := make([]uint64, len(events))
	for index, event := range events {
		got[index] = event.EventID
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("EventIDs=%v，想要 %v", got, want)
	}
}

func indexOfCompanionChatEvent(messages []network.ServerMessage, id uint64) int {
	for index, message := range messages {
		if event, ok := message.(network.ChatEvent); ok && event.EventID == id {
			return index
		}
	}
	return -1
}
