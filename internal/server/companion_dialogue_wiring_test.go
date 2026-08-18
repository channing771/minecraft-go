// D6 触发节点接线、CompanionSpeech 广播与摘要生命周期的测试：进入 Running、
// 选中步骤完成与四种终态的节点评估、每任务预算、follow 恰好三节点（开始/
// 首次到达/终止，长跟随不产生 progress）、台词广播给全部在线玩家且 EventID
// 严格递增、终态后到达的过时结果不广播、终态摘要「写入→dirty→落盘→重启
// 恢复→进入下一次 Dialogue 请求输入」、summary-only 队列不被保存侧判 clean，
// 以及 Memory/TCP 同序同种类事件（含 Speech）。全部使用 httptest 假模型，
// 绝不打开前台窗口或访问真实模型服务。
package server

import (
	"context"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/storage"
)

// waitDialogueRequests 等待假台词模型收到的请求达到 want（毫秒级轮询，
// 容纳 worker goroutine 的调度延迟）。
func waitDialogueRequests(t *testing.T, model *fakeDialogueModel, want int) {
	t.Helper()
	waitIntegrationCondition(t, "假台词模型请求数", func() bool {
		requests, _, _ := model.snapshotCounts()
		return requests >= want
	})
}

// stepDialogueTick 推进一个权威 tick、排空全部客户端消息并留 2ms 墙钟。
// 本地 httptest 的台词往返在 race 检测下与快速任务的步骤间隙处于同一量级，
// 不预留往返窗口会让「相邻节点被每伙伴单在途挤掉」变成测试噪声——该挤掉
// 是规格允许的尽力而为语义，但节点全集断言需要确定性的应用时机。返回本
// tick 全部客户端收到的 ChatEvent。
func stepDialogueTick(t *testing.T, host *Host, clients []network.ClientEndpoint) []network.ChatEvent {
	t.Helper()
	result := host.world.StepForTest()
	var events []network.ChatEvent
	for _, endpoint := range clients {
		events = append(events, companionChatEvents(receiveCompanionChatTick(t, endpoint, result.Tick))...)
	}
	time.Sleep(2 * time.Millisecond)
	return events
}

// collectDialogueEvents 逐 tick 推进（带台词往返窗口）并收集指定客户端的
// ChatEvent，直到 stop 返回 true 或达到 maxTicks。
func collectDialogueEvents(
	t *testing.T,
	host *Host,
	client network.ClientEndpoint,
	maxTicks int,
	stop func(events []network.ChatEvent) bool,
) []network.ChatEvent {
	t.Helper()
	var collected []network.ChatEvent
	for range maxTicks {
		collected = append(collected, stepDialogueTick(t, host, []network.ClientEndpoint{client})...)
		if stop != nil && stop(collected) {
			return collected
		}
	}
	return collected
}

// companionDialogueSlotSummary 在 stepMu 下读取槽位持有的最近对话摘要。
func companionDialogueSlotSummary(t *testing.T, host *Host, id companion.ID) string {
	t.Helper()
	host.world.stepMu.Lock()
	defer host.world.stepMu.Unlock()
	slot := host.world.companionManager.slots[id]
	if slot == nil {
		t.Fatalf("伙伴 %s 没有任务槽位", id)
	}
	return slot.summary
}

// countDialogueTerminalRequests 统计请求记录中终止节点的个数。
func countDialogueTerminalRequests(records []dialogueRequestRecord) int {
	count := 0
	for _, record := range records {
		if record.NodeKind == "terminal" {
			count++
		}
	}
	return count
}

// TestCompanionDialogueWiringExactBudgetAndSelection 验证普通任务的触发节点
// 接线与预算：十二步任务的全部成功路径上，开始与终态各恰好一次、进展节点
// 只在 SelectProgressSteps 选中的步骤完成（TaskProgress 广播位置）发起。
//
// 末步裁决（brief 内两处数字的调和）：SelectProgressSteps 的最后一个选中
// 索引恒为最后一步，而最后一步的完成迁移产出 TaskCompleted（非 TaskProgress），
// 其「完成表达」由终态台词承载（dialogue_nodes.go「末段永远被覆盖」的本意）；
// 每伙伴单在途约束下同一迁移点也只能保住一次请求，终态台词是 spec 锁定的
// 「恰好一次」，故优先。因此十二步任务实际请求数 = 1 + 5 + 1 = 7，仍严格
// 不超过八次预算。
func TestCompanionDialogueWiringExactBudgetAndSelection(t *testing.T) {
	definitions := []companion.Definition{{ID: chatTestCompanionID(1), Name: "阿木"}}
	host, client, body := companionManagerHostReady(t, definitions, nil)
	baseX, baseZ := int32(body.Position[0]), int32(body.Position[2])
	baseY := int32(body.Position[1])
	steps := make([][3]int32, 0, 12)
	for index := 1; index <= 12; index++ {
		steps = append(steps, [3]int32{baseX + int32(index)*2, baseY, baseZ})
	}
	planner := newFakeCompanionModel(t, steps...)
	host.world.companionManager.replacePlannerForTest(t, planner)
	dialogue := newFakeDialogueModel(t)
	host.world.companionManager.replaceDialogueForTest(t, dialogue)

	sendIntegration(t, client, network.ChatCommand{Text: "@阿木 走遍十二个点"})
	waitForIncomingChatDepth(t, host.world, 1)
	events := collectDialogueEvents(t, host, client, 4000, func(events []network.ChatEvent) bool {
		return countKind(events, network.ChatEventTaskCompleted) == 1
	})
	if countKind(events, network.ChatEventTaskCompleted) != 1 {
		t.Fatalf("十二步任务未完成：事件=%v", chatEventKinds(events))
	}
	if got := countKind(events, network.ChatEventTaskProgress); got != 11 {
		t.Fatalf("TaskProgress=%d，想要 11（中间步骤各一次）", got)
	}
	// 终止请求在 Completed 的迁移点异步发起，先等它抵达假模型再取快照。
	waitDialogueRequests(t, dialogue, 7)
	records := dialogue.snapshotDialogueRequests()
	// 无 persona 伙伴照常有台词：人设透传为空串。
	for _, record := range records {
		if record.Persona != "" {
			t.Fatalf("未配置 persona 的伙伴请求携带了人设 %q", record.Persona)
		}
	}
	// 选中集合中可触发 TaskProgress 的子集：去掉恒为最后一步的末元素。
	selected := companion.SelectProgressSteps(12)
	wantProgress := selected[:len(selected)-1]
	wantKinds := []string{"start"}
	for range wantProgress {
		wantKinds = append(wantKinds, "progress")
	}
	wantKinds = append(wantKinds, "terminal")
	gotKinds := dialogueRequestKinds(records)
	if len(gotKinds) != len(wantKinds) {
		t.Fatalf("十二步任务台词请求数=%d（%v），想要 %d（%v）",
			len(gotKinds), gotKinds, len(wantKinds), wantKinds)
	}
	for index, kind := range gotKinds {
		if kind != wantKinds[index] {
			t.Fatalf("请求 %d 节点类别=%q，想要 %q（序列=%v）",
				index, kind, wantKinds[index], gotKinds)
		}
	}
	if total := len(records); total > companion.MaxDialogueRequestsPerTask {
		t.Fatalf("总请求数=%d 超过预算 %d", total, companion.MaxDialogueRequestsPerTask)
	}
}

// TestCompanionDialogueTerminalCoversFourTerminalStates 验证四种终态
// （Completed/Failed/TimedOut/Stopped）都恰好发起一次终止节点台词请求。
func TestCompanionDialogueTerminalCoversFourTerminalStates(t *testing.T) {
	definitions := []companion.Definition{{ID: chatTestCompanionID(1), Name: "阿木"}}

	t.Run("Completed", func(t *testing.T) {
		host, client, body := companionManagerHostReady(t, definitions, nil)
		baseX, baseZ := int32(body.Position[0]), int32(body.Position[2])
		planner := newFakeCompanionModel(t,
			[3]int32{baseX + 2, int32(body.Position[1]), baseZ},
			[3]int32{baseX + 4, int32(body.Position[1]), baseZ})
		host.world.companionManager.replacePlannerForTest(t, planner)
		dialogue := newFakeDialogueModel(t)
		host.world.companionManager.replaceDialogueForTest(t, dialogue)
		sendIntegration(t, client, network.ChatCommand{Text: "@阿木 走两步"})
		waitForIncomingChatDepth(t, host.world, 1)
		collectDialogueEvents(t, host, client, 600, func(events []network.ChatEvent) bool {
			return countKind(events, network.ChatEventTaskCompleted) == 1
		})
		waitDialogueRequests(t, dialogue, 3)
		if count := countDialogueTerminalRequests(dialogue.snapshotDialogueRequests()); count != 1 {
			t.Fatalf("Completed 终止请求数=%d，想要 1", count)
		}
	})

	t.Run("Failed", func(t *testing.T) {
		// 不可达目标：三连寻路失败以 PathUnreachable 终结（失败发生在任务
		// Running 一段时间后，开始台词早已应用，终止节点不会被单在途挤掉）。
		host, client, body := companionManagerHostReady(t, definitions, nil)
		baseX, baseZ := int32(body.Position[0]), int32(body.Position[2])
		planner := newFakeCompanionModel(t, [3]int32{baseX + 1000, int32(body.Position[1]), baseZ})
		host.world.companionManager.replacePlannerForTest(t, planner)
		dialogue := newFakeDialogueModel(t)
		host.world.companionManager.replaceDialogueForTest(t, dialogue)
		sendIntegration(t, client, network.ChatCommand{Text: "@阿木 去远方"})
		waitForIncomingChatDepth(t, host.world, 1)
		collectDialogueEvents(t, host, client, 600, func(events []network.ChatEvent) bool {
			return countKind(events, network.ChatEventTaskFailed) == 1
		})
		waitDialogueRequests(t, dialogue, 2)
		if count := countDialogueTerminalRequests(dialogue.snapshotDialogueRequests()); count != 1 {
			t.Fatalf("Failed 终止请求数=%d，想要 1", count)
		}
	})

	t.Run("TimedOut", func(t *testing.T) {
		// 恢复一个 deadline 已迫近的 Running go_to 任务：冷启动的出生扫描
		//（异步区块生成 + 扫描）远慢于几个权威 tick，任务在世界时间到达
		// deadline 时过期——expireTasks 不依赖伙伴身体，TimedOut 先于任何
		// 执行动作发生，终止节点是该任务的第一个也是唯一一个台词请求。
		//
		// 但 terminal 派发本身依赖激活：requestDialogue 对未激活伙伴（出生
		// 扫描在途）按守卫跳过该节点、等下一个触发节点，而终态没有下一个
		// 触发节点——派发会被永久跳过（生产守卫是 M5D 裁决的正确语义，
		// 只能由测试侧保证派发时机）。deadline 是绝对世界 tick（=5），冷启动
		// WorldTime=0，进入时 WorldTime>=5 的那一步（第 6 次 step）才触发
		// 过期迁移，因此激活必须发生在 step 1..5 内；原泵每 tick 只留 2ms
		// 墙钟（合计约 10ms），race + 全仓并行下冷启动区块生成极易赌输，
		// TaskTimedOut 事件已发布而台词请求恒 0，waitDialogueRequests 等满
		// 60s 必假超时。这里在过期 tick 前的 step 预算内每步先给足区块生成
		// 墙钟并确认激活，使 terminal 派发必然发生在已激活状态。
		id := definitions[0].ID
		queue := storage.StoredCompanionQueue{
			ID:         id,
			HasCurrent: true,
			Current: storage.StoredCompanionTask{
				Command: "来不及执行的任务",
				PlanSteps: []companion.PlanStep{
					{Kind: companion.PlanStepGoTo, X: 4, Y: 1, Z: 2},
				},
				State:         companion.TaskRunning,
				DeadlineTicks: 5,
			},
		}
		host := restoredCompanionHost(t, definitions, nil, restoredCompanionSeed(
			t, id, [3]float32{0.5, 1, 0.5}, queue))
		dialogue := newFakeDialogueModel(t)
		host.world.companionManager.replaceDialogueForTest(t, dialogue)
		client := openCompanionChatClient(t, host, "memory", integrationIdentity(0x71, "发令者"))
		companionActivated := func() bool {
			for _, body := range host.world.engine.CompanionBodies() {
				if body.ID == id {
					return true
				}
			}
			return false
		}
		// 激活只发生在 engine step 内的 advancePendingCompanion，且需 restore
		// 点周围区块先 ready（异步、吃墙钟）；每步尝试前给 25ms（5 次机会
		// 合计 125ms，远大于原先 10ms 的赌注），激活后立即退出预泵。
		for host.world.engine.WorldTime() < queue.Current.DeadlineTicks && !companionActivated() {
			time.Sleep(25 * time.Millisecond)
			stepDialogueTick(t, host, []network.ClientEndpoint{client})
		}
		if !companionActivated() {
			t.Fatalf("过期 tick 前伙伴 %s 未激活，terminal 派发会被未激活守卫永久跳过", id)
		}
		events := collectDialogueEvents(t, host, client, 2000, func(events []network.ChatEvent) bool {
			return countKind(events, network.ChatEventTaskTimedOut) == 1
		})
		if countKind(events, network.ChatEventTaskTimedOut) != 1 {
			t.Fatalf("恢复的迫近 deadline 任务未超时：事件=%v", chatEventKinds(events))
		}
		waitDialogueRequests(t, dialogue, 1)
		records := dialogue.snapshotDialogueRequests()
		if len(records) != 1 || records[0].NodeKind != "terminal" {
			t.Fatalf("TimedOut 台词请求=%v，想要恰好一次 terminal", dialogueRequestKinds(records))
		}
	})

	t.Run("Stopped", func(t *testing.T) {
		// 持续跟随 Running 后由「停止」旁路终结：Stopped 终止不得被排除。
		// 注入路径不产生 Started 事件，终止节点是该任务唯一的台词请求。
		model := newFakeCompanionModel(t)
		host := newCompanionManagerHost(t, definitions, model, nil)
		dialogue := newFakeDialogueModel(t)
		host.world.companionManager.replaceDialogueForTest(t, dialogue)
		issuerIdentity := integrationIdentity(0x85, "原发令者")
		// follow 目标玩家必须在线：目标离线会让持续跟随在推进窗口内以
		// WorldChanged 失败，停止旁路就只剩 NotFollowing 同步拒绝。
		follower := openCompanionChatClient(t, host, "memory", issuerIdentity)
		stopper := openCompanionChatClient(t, host, "memory", integrationIdentity(0x83, "停止者"))
		clients := []network.ClientEndpoint{stopper, follower}
		waitForCompanionChatWorld(t, host, clients, 1)
		injectRunningCompanionTask(t, host, definitions[0].ID, stopTestIssuer(issuerIdentity),
			"跟着我", []companion.PlanStep{{Kind: companion.PlanStepFollow, PlayerID: issuerIdentity.PlayerID}})
		// 推进数个 tick 让身体缓存与队列状态稳定，再触发停止旁路。
		for range 5 {
			stepDialogueTick(t, host, clients)
		}
		sendIntegration(t, stopper, network.ChatCommand{Text: "@阿木 停止"})
		waitForIncomingChatDepth(t, host.world, 1)
		events := collectDialogueEvents(t, host, stopper, 100, func(events []network.ChatEvent) bool {
			return countKind(events, network.ChatEventTaskStopped) == 1
		})
		if countKind(events, network.ChatEventTaskStopped) != 1 {
			t.Fatalf("停止旁路未终结任务：事件=%v", chatEventKinds(events))
		}
		waitDialogueRequests(t, dialogue, 2)
		records := dialogue.snapshotDialogueRequests()
		if len(records) != 2 || records[0].NodeKind != "first_arrival" ||
			records[1].NodeKind != "terminal" {
			t.Fatalf("Stopped 台词请求=%v，想要 first_arrival + terminal", dialogueRequestKinds(records))
		}
	})
}

// TestCompanionDialogueFollowExactlyThreeNodes 验证持续跟随任务的节点全集：
// 开始、首次到达跟随距离与终止恰好三个；目标反复进出跟随距离与长跟随
// 运行（deadline 豁免，无自然终点）都不产生新的台词请求。
func TestCompanionDialogueFollowExactlyThreeNodes(t *testing.T) {
	definitions := []companion.Definition{{ID: chatTestCompanionID(1), Name: "阿木"}}
	model := newFakeCompanionModel(t)
	targetIdentity := integrationIdentity(0x85, "跟随目标")
	model.setPlanScript(followPlanContent(targetIdentity.PlayerID))
	host := newCompanionManagerHost(t, definitions, model, nil)
	dialogue := newFakeDialogueModel(t)
	host.world.companionManager.replaceDialogueForTest(t, dialogue)
	follower := openCompanionChatClient(t, host, "memory", targetIdentity)
	clients := []network.ClientEndpoint{follower}
	body := stepUntilCompanionManagerReady(t, host, clients, definitions[0].ID)
	// 目标玩家先站到跟随距离外（+X 10 格），跟随开始后伙伴走进距离内触发
	// 首次到达。
	followerLogin := activeLoginForPlayer(t, host, targetIdentity.PlayerID)
	targetPosition := [3]float32{body.Position[0] + 10, body.Position[1], body.Position[2]}
	setPlayerPosition(t, host, followerLogin.Session, targetPosition)

	sendIntegration(t, follower, network.ChatCommand{Text: "@阿木 跟着我"})
	waitForIncomingChatDepth(t, host.world, 1)
	events := collectDialogueEvents(t, host, follower, 600, func(events []network.ChatEvent) bool {
		return countKind(events, network.ChatEventTaskStarted) == 1
	})
	if countKind(events, network.ChatEventTaskStarted) != 1 {
		t.Fatalf("跟随任务未开始：事件=%v", chatEventKinds(events))
	}
	// 等待伙伴进入跟随距离（首次到达节点在此窗口触发）并静置。
	waitArrival := func(maxTicks int) bool {
		for range maxTicks {
			stepDialogueTick(t, host, clients)
			if companionBody := currentCompanionBody(t, host, definitions[0].ID); followHorizontalDistance(
				companionBody.Position, targetPosition) <= 4.8 {
				return true
			}
		}
		return false
	}
	if !waitArrival(600) {
		t.Fatal("伙伴 600 tick 内未进入跟随距离")
	}
	for range 50 {
		stepDialogueTick(t, host, clients)
	}
	// 长跟随不产生 progress：原地持续跟随一段（deadline 豁免天然无超时）。
	for range 100 {
		stepDialogueTick(t, host, clients)
	}
	// 目标移出跟随距离再回来：首达只此一次，不重复。
	setPlayerPosition(t, host, followerLogin.Session,
		[3]float32{body.Position[0] + 20, body.Position[1], body.Position[2]})
	for range 200 {
		stepDialogueTick(t, host, clients)
	}
	setPlayerPosition(t, host, followerLogin.Session, targetPosition)
	if !waitArrival(600) {
		t.Fatal("目标返回后伙伴未重新进入跟随距离")
	}
	// 停止旁路终结：终止节点是第三个也是最后一个。
	sendIntegration(t, follower, network.ChatCommand{Text: "@阿木 停止"})
	waitForIncomingChatDepth(t, host.world, 1)
	collectDialogueEvents(t, host, follower, 100, func(events []network.ChatEvent) bool {
		return countKind(events, network.ChatEventTaskStopped) == 1
	})
	waitDialogueRequests(t, dialogue, 3)
	records := dialogue.snapshotDialogueRequests()
	want := []string{"start", "first_arrival", "terminal"}
	got := dialogueRequestKinds(records)
	if len(got) != len(want) {
		t.Fatalf("跟随任务台词请求=%v，想要恰好 %v（长跟随不得产生 progress）", got, want)
	}
	for index, kind := range got {
		if kind != want[index] {
			t.Fatalf("跟随节点 %d=%q，想要 %q（序列=%v）", index, kind, want[index], got)
		}
	}
}

// TestCompanionDialogueSpeechBroadcastToAllPlayers 验证 CompanionSpeech 广播
// 给全部在线玩家：两名玩家都收到台词事件（伙伴身份、reason None、不复述
// 指令）、EventID 沿全服计数器严格递增，且 persona 生效值透传进请求。
func TestCompanionDialogueSpeechBroadcastToAllPlayers(t *testing.T) {
	definitions := []companion.Definition{{
		ID:              chatTestCompanionID(1),
		Name:            "阿木",
		ResolvedPersona: "沉稳寡言的老向导。",
	}}
	host := newCompanionManagerHost(t, definitions, nil, nil)
	issuerIdentity := integrationIdentity(0x71, "发令者")
	issuer := openCompanionChatClient(t, host, "memory", issuerIdentity)
	observer := openCompanionChatClient(t, host, "memory", integrationIdentity(0x86, "旁观者"))
	clients := []network.ClientEndpoint{issuer, observer}
	body := stepUntilCompanionManagerReady(t, host, clients, definitions[0].ID)
	baseX, baseZ := int32(body.Position[0]), int32(body.Position[2])
	// 两步计划：开始、第一步进展与终态三个节点依次落地，相邻节点之间有
	// 真实的走步窗口。
	planner := newFakeCompanionModel(t,
		[3]int32{baseX + 2, int32(body.Position[1]), baseZ},
		[3]int32{baseX + 4, int32(body.Position[1]), baseZ})
	host.world.companionManager.replacePlannerForTest(t, planner)
	dialogue := newFakeDialogueModel(t)
	host.world.companionManager.replaceDialogueForTest(t, dialogue)

	sendIntegration(t, issuer, network.ChatCommand{Text: "@阿木 走两步"})
	waitForIncomingChatDepth(t, host.world, 1)
	var issuerEvents, observerEvents []network.ChatEvent
	for range 600 {
		result := host.world.StepForTest()
		issuerEvents = append(issuerEvents,
			companionChatEvents(receiveCompanionChatTick(t, issuer, result.Tick))...)
		observerEvents = append(observerEvents,
			companionChatEvents(receiveCompanionChatTick(t, observer, result.Tick))...)
		time.Sleep(2 * time.Millisecond)
		if countKind(issuerEvents, network.ChatEventTaskCompleted) == 1 &&
			countKind(issuerEvents, network.ChatEventCompanionSpeech) == 3 {
			break
		}
	}

	// 开始、进展与终态台词都已广播（两步计划的进展集合 SelectProgressSteps(2)
	// 去掉末步后恰为第一步）。
	speechOf := func(events []network.ChatEvent) []network.ChatEvent {
		return eventsWithKind(events, network.ChatEventCompanionSpeech)
	}
	issuerSpeech := speechOf(issuerEvents)
	observerSpeech := speechOf(observerEvents)
	if len(issuerSpeech) != 3 || len(observerSpeech) != 3 {
		t.Fatalf("Speech 广播数 issuer=%d observer=%d（issuer 事件=%v），想要各 3",
			len(issuerSpeech), len(observerSpeech), chatEventKinds(issuerEvents))
	}
	for index, event := range issuerSpeech {
		if event.CompanionID != definitions[0].ID || event.CompanionName != "阿木" ||
			event.RejectReason != network.ChatRejectNone || event.Command != "" ||
			event.PlayerID != issuerIdentity.PlayerID || event.PlayerName != "发令者" {
			t.Fatalf("Speech 事件字段=%+v，想要伙伴身份+发令者身份+reason None", event)
		}
		if err := event.Validate(); err != nil {
			t.Fatalf("Speech Validate: %v", err)
		}
		if event != observerSpeech[index] {
			t.Fatalf("两名玩家的 Speech 事件不一致：issuer=%+v observer=%+v",
				event, observerSpeech[index])
		}
	}
	if issuerSpeech[len(issuerSpeech)-1].Speech != "完成了" {
		t.Fatalf("终态 Speech 文本=%q，想要假模型的终态台词",
			issuerSpeech[len(issuerSpeech)-1].Speech)
	}
	assertStrictlyIncreasingEventIDs(t, issuerEvents)
	assertStrictlyIncreasingEventIDs(t, observerEvents)
	// persona 生效值进入请求输入。
	records := dialogue.snapshotDialogueRequests()
	if len(records) == 0 {
		t.Fatal("没有任何台词请求")
	}
	for _, record := range records {
		if record.Persona != "沉稳寡言的老向导。" {
			t.Fatalf("请求 persona=%q，想要生效人设透传", record.Persona)
		}
	}
}

// TestCompanionDialogueStaleAfterTerminalNotBroadcast 验证终态后到达的开始
// 节点结果被丢弃：不广播任何 CompanionSpeech，在途标记照常清除；在途期间
// 到来的终止节点按「跳过即放弃」处理，不补发。
func TestCompanionDialogueStaleAfterTerminalNotBroadcast(t *testing.T) {
	definitions := []companion.Definition{{ID: chatTestCompanionID(1), Name: "阿木"}}
	// 任务是一个不可达的 go_to（目标远超寻路窗口）：进入 Running（开始台词
	// 发起并挂起）后经三连寻路失败以 PathUnreachable 终结——失败迁移点的
	// 终止节点因开始请求在途被跳过，随后的开始结果对已终态任务过时。
	host, client, body := companionManagerHostReady(t, definitions, nil)
	baseX, baseZ := int32(body.Position[0]), int32(body.Position[2])
	planner := newFakeCompanionModel(t, [3]int32{baseX + 1000, int32(body.Position[1]), baseZ})
	host.world.companionManager.replacePlannerForTest(t, planner)
	dialogue := newFakeDialogueModel(t)
	dialogue.holdRequests()
	host.world.companionManager.replaceDialogueForTest(t, dialogue)
	clients := []network.ClientEndpoint{client}

	sendIntegration(t, client, network.ChatCommand{Text: "@阿木 去远方"})
	waitForIncomingChatDepth(t, host.world, 1)
	events := collectDialogueEvents(t, host, client, 300, func(events []network.ChatEvent) bool {
		return countKind(events, network.ChatEventTaskFailed) == 1
	})
	if countKind(events, network.ChatEventTaskFailed) != 1 {
		t.Fatalf("不可达任务未失败：事件=%v", chatEventKinds(events))
	}
	// 开始请求在任务进入 Running 时发起并挂起（hold 中）：HTTP 连接建立晚于
	// 事件广播属正常调度，先等它抵达假模型的 handler 再核对总数。
	waitDialogueRequests(t, dialogue, 1)
	if requests, _, _ := dialogue.snapshotCounts(); requests != 1 {
		t.Fatalf("挂起期间台词请求数=%d，想要恰好 1（开始节点）", requests)
	}

	dialogue.releaseRequests()
	for range 20 {
		for _, event := range stepDialogueTick(t, host, clients) {
			if event.Kind == network.ChatEventCompanionSpeech {
				t.Fatalf("过时的开始台词被广播：%+v", event)
			}
		}
	}
	if effects, _ := dialogueEffectCount(t, host, definitions[0].ID); effects != 0 {
		t.Fatalf("过时结果产生了副作用：effects=%d，想要 0", effects)
	}
	// 终止节点在开始请求在途期间被跳过：请求总数停在 1，绝不补发。
	if requests, _, _ := dialogue.snapshotCounts(); requests != 1 {
		t.Fatalf("台词请求数=%d，想要 1（在途跳过的终止节点不补发）", requests)
	}
}

// TestCompanionDialogueSummaryLifecycle 验证终态摘要的完整生命周期：terminal
// 响应写入 manager 摘要→标记存档 dirty→关服 Flush 落盘（summary-only 队列，
// 无任务无 FIFO）→重启恢复→进入下一次 Dialogue 请求输入（假模型断言请求体
// 含旧摘要）。
func TestCompanionDialogueSummaryLifecycle(t *testing.T) {
	id := chatTestCompanionID(1)
	definitions := []companion.Definition{{ID: id, Name: "阿木"}}
	store := newHostTestStore()

	// newHost 构造一段可独立关闭的宿主：close 幂等且经 t.Cleanup 兜底——
	// 第一段需要中途回收（后续从同一 store 读取落盘结果），第二段则完全
	// 依赖 cleanup；漏关任一段都会泄漏整组 world goroutine（persistence/
	// chunk/save worker），被后续测试的 goroutine 基线断言捕获。
	newHost := func() (*Host, *fakeDialogueModel, network.ClientEndpoint, func()) {
		config := hostTestConfig()
		config.Companions = append([]companion.Definition(nil), definitions...)
		config.MaxPlayers = 2
		config.OutboxCapacity = 4096
		config.HeartbeatInterval = time.Hour
		config.HeartbeatTimeout = time.Hour
		host, err := NewHost(context.Background(), config, flatTestGenerator{}, store)
		if err != nil {
			t.Fatalf("NewHost: %v", err)
		}
		client := openCompanionChatClient(t, host, "memory", integrationIdentity(0x71, "发令者"))
		body := stepUntilCompanionManagerReady(t, host, []network.ClientEndpoint{client}, id)
		planner := newFakeCompanionModel(t,
			[3]int32{int32(body.Position[0]) + 2, int32(body.Position[1]), int32(body.Position[2])})
		host.world.companionManager.replacePlannerForTest(t, planner)
		dialogue := newFakeDialogueModel(t)
		host.world.companionManager.replaceDialogueForTest(t, dialogue)
		closed := false
		closeHost := func() {
			if closed {
				return
			}
			closed = true
			ctx, cancel := context.WithTimeout(context.Background(), longWaitDeadline)
			defer cancel()
			if err := host.Shutdown(ctx); err != nil {
				t.Errorf("Host.Shutdown: %v", err)
			}
		}
		t.Cleanup(closeHost)
		return host, dialogue, client, closeHost
	}

	// 第一段：任务完成，terminal 响应捎带的摘要写入 manager 状态。目标在
	// 两格外——每 tick 2ms 的往返窗口保证终止节点先于开始台词应用后发起。
	first, _, firstClient, closeFirst := newHost()
	sendIntegration(t, firstClient, network.ChatCommand{Text: "@阿木 走一步"})
	waitForIncomingChatDepth(t, first.world, 1)
	events := collectDialogueEvents(t, first, firstClient, 600, func(events []network.ChatEvent) bool {
		return countKind(events, network.ChatEventTaskCompleted) == 1
	})
	if countKind(events, network.ChatEventTaskCompleted) != 1 {
		t.Fatalf("第一段任务未完成：事件=%v", chatEventKinds(events))
	}
	// 摘要应用在结果回到 tick 边界之后：推进至观察到 manager 摘要非空。
	deadline := time.Now().Add(waitDeadline)
	for time.Now().Before(deadline) &&
		companionDialogueSlotSummary(t, first, id) != "最近完成了任务" {
		stepDialogueTick(t, first, []network.ClientEndpoint{firstClient})
	}
	if summary := companionDialogueSlotSummary(t, first, id); summary != "最近完成了任务" {
		t.Fatalf("终态摘要未写入 manager：summary=%q", summary)
	}
	closeFirst()

	// 落盘检查：summary-only 队列（无任务无 FIFO）必须保留摘要。
	loaded, err := store.LoadCompanions(context.Background())
	if err != nil {
		t.Fatalf("LoadCompanions: %v", err)
	}
	if len(loaded.Queues) != 1 || loaded.Queues[0].ID != id ||
		loaded.Queues[0].Summary != "最近完成了任务" {
		t.Fatalf("落盘队列=%+v，想要 summary-only 载荷保留摘要", loaded.Queues)
	}

	// 第二段：重启恢复摘要，并进入下一次 Dialogue 请求输入。
	second, secondDialogue, secondClient, _ := newHost()
	if summary := companionDialogueSlotSummary(t, second, id); summary != "最近完成了任务" {
		t.Fatalf("重启后摘要未恢复：summary=%q", summary)
	}
	sendIntegration(t, secondClient, network.ChatCommand{Text: "@阿木 再走一步"})
	waitForIncomingChatDepth(t, second.world, 1)
	secondEvents := collectDialogueEvents(t, second, secondClient, 600, func(events []network.ChatEvent) bool {
		return countKind(events, network.ChatEventTaskStarted) == 1
	})
	if countKind(secondEvents, network.ChatEventTaskStarted) != 1 {
		t.Fatalf("第二段任务未开始：事件=%v", chatEventKinds(secondEvents))
	}
	waitDialogueRequests(t, secondDialogue, 1)
	records := secondDialogue.snapshotDialogueRequests()
	if len(records) == 0 || records[0].Summary != "最近完成了任务" {
		t.Fatalf("重启后的台词请求未携带恢复摘要：%+v", records)
	}
}

// TestCompanionDialogueSummaryOnlyQueuePersistence 验证 summary-only 队列的
// 保存侧接线：无任务无 FIFO 但有摘要的 active 伙伴不被守卫丢弃、
// equalStoredQueues 把摘要差异判为 dirty、未激活伙伴的摘要按 dropped 报告。
func TestCompanionDialogueSummaryOnlyQueuePersistence(t *testing.T) {
	body := companionBody(1, 10)
	summaries := []companionSummaryState{{ID: body.ID, Summary: "最近完成了任务"}}

	queues, dropped := companionQueuesForSave(nil, []companion.Body{body}, summaries)
	if dropped || len(queues) != 1 || queues[0].ID != body.ID ||
		queues[0].Summary != "最近完成了任务" || queues[0].HasCurrent || len(queues[0].Pending) != 0 {
		t.Fatalf("summary-only 载荷=%+v dropped=%v，想要仅含摘要的队列条目", queues, dropped)
	}

	// 摘要差异不得被判 clean：否则摘要变化后保存被跳过。
	if equalStoredQueues(
		[]storage.StoredCompanionQueue{{ID: body.ID, Summary: "旧摘要"}},
		[]storage.StoredCompanionQueue{{ID: body.ID, Summary: "新摘要"}},
	) {
		t.Fatal("equalStoredQueues 忽略了摘要差异，summary-only 变更会被判 clean")
	}

	// 身体尚未激活（records 缺席）的摘要按 dropped 报告，保持 dirty 等激活。
	if _, dropped := companionQueuesForSave(nil, nil, summaries); !dropped {
		t.Fatal("无记录伙伴的摘要未按 dropped 报告")
	}

	// 持久化观察：摘要变化触发保存，载荷携带 summary-only 条目。
	persistenceStore := newControllableCompanionStore()
	persistence := newCompanionPersistence(persistenceStore, storage.StoredCompanions{}, companionPersistenceTestConfig())
	t.Cleanup(persistence.Close)
	persistence.Observe([]companion.Body{body}, nil, summaries)
	if err := persistence.Poll(10); err != nil {
		t.Fatal(err)
	}
	save := receiveCompanionSave(t, persistenceStore)
	if len(save.Queues) != 1 || save.Queues[0].ID != body.ID ||
		save.Queues[0].Summary != "最近完成了任务" {
		t.Fatalf("保存载荷=%+v，想要 summary-only 队列", save.Queues)
	}
	persistenceStore.complete(nil)
}

// TestCompanionDialogueSuccessfulSpeechKeepsFactSequence 验证成功台词场景下
// 任务事实序列与无 Dialogue 完全一致（回归 D5 对照）：过滤 CompanionSpeech
// 后逐条相等，Speech 本身存在且属于表达平面。
func TestCompanionDialogueSuccessfulSpeechKeepsFactSequence(t *testing.T) {
	runScenario := func(withDialogue bool) []network.ChatEvent {
		definitions := []companion.Definition{{ID: chatTestCompanionID(1), Name: "阿木"}}
		host, client, body := companionManagerHostReady(t, definitions, nil)
		baseX, baseZ := int32(body.Position[0]), int32(body.Position[2])
		planner := newFakeCompanionModel(t,
			[3]int32{baseX + 2, int32(body.Position[1]), baseZ},
			[3]int32{baseX + 4, int32(body.Position[1]), baseZ},
		)
		host.world.companionManager.replacePlannerForTest(t, planner)
		if withDialogue {
			host.world.companionManager.replaceDialogueForTest(t, newFakeDialogueModel(t))
		}
		sendIntegration(t, client, network.ChatCommand{Text: "@阿木 走两步"})
		waitForIncomingChatDepth(t, host.world, 1)
		return collectDialogueEvents(t, host, client, 600, func(events []network.ChatEvent) bool {
			return countKind(events, network.ChatEventTaskCompleted) == 1
		})
	}

	baseline := runScenario(false)
	withDialogue := runScenario(true)
	if len(baseline) == 0 || countKind(baseline, network.ChatEventTaskCompleted) != 1 {
		t.Fatalf("基准场景未完成：事件=%v", chatEventKinds(baseline))
	}
	if got := countKind(withDialogue, network.ChatEventCompanionSpeech); got == 0 {
		t.Fatal("成功台词场景没有任何 CompanionSpeech 事件")
	}
	filtered := make([]network.ChatEvent, 0, len(withDialogue))
	for _, event := range withDialogue {
		if event.Kind != network.ChatEventCompanionSpeech {
			filtered = append(filtered, event)
		}
	}
	if len(baseline) != len(filtered) {
		t.Fatalf("事实事件序列长度不一致：无台词=%v 有台词=%v",
			chatEventKinds(baseline), chatEventKinds(filtered))
	}
	for index := range baseline {
		if baseline[index].Kind != filtered[index].Kind ||
			baseline[index].Command != filtered[index].Command ||
			baseline[index].RejectReason != filtered[index].RejectReason {
			t.Fatalf("事实事件 %d 不一致：无台词=%+v 有台词=%+v", index,
				baseline[index], filtered[index])
		}
	}
}

// dialogueParityProjection 是跨传输可比的事件投影：种类 + 文本槽（Speech 用
// 台词、其余用指令）+ 原因。台词落地 tick 受 worker 调度影响，EventID 的
// 绝对位置不跨传输断言（每传输内仍须严格递增，由调用方补充断言）。
type dialogueParityProjection struct {
	Kind   network.ChatEventKind
	Text   string
	Reason network.ChatRejectReason
}

func projectDialogueParityEvents(events []network.ChatEvent) []dialogueParityProjection {
	projection := make([]dialogueParityProjection, 0, len(events))
	for _, event := range events {
		text := event.Command
		if event.Kind == network.ChatEventCompanionSpeech {
			text = event.Speech
		}
		projection = append(projection, dialogueParityProjection{
			Kind: event.Kind, Text: text, Reason: event.RejectReason,
		})
	}
	return projection
}

// TestCompanionDialogueSpeechMemoryTCPParity 验证 Memory 与 TCP 两传输产出
// 同序同种类的 ChatEvent（含 CompanionSpeech）：单步任务全程的事件投影逐条
// 相等，Speech 文本来自同一假模型、两传输一致。
func TestCompanionDialogueSpeechMemoryTCPParity(t *testing.T) {
	run := func(transport string) []network.ChatEvent {
		definitions := []companion.Definition{{ID: chatTestCompanionID(1), Name: "阿木"}}
		model := newFakeCompanionModel(t)
		host := newCompanionManagerHost(t, definitions, model, nil)
		dialogue := newFakeDialogueModel(t)
		host.world.companionManager.replaceDialogueForTest(t, dialogue)
		client := openCompanionChatClient(t, host, transport, integrationIdentity(0x91, "发令者"))
		body := stepUntilCompanionManagerReady(t, host, []network.ClientEndpoint{client}, definitions[0].ID)
		planner := newFakeCompanionModel(t,
			[3]int32{int32(body.Position[0]) + 2, int32(body.Position[1]), int32(body.Position[2])})
		host.world.companionManager.replacePlannerForTest(t, planner)
		sendIntegration(t, client, network.ChatCommand{Text: "@阿木 走一步"})
		waitForIncomingChatDepth(t, host.world, 1)
		events := collectDialogueEvents(t, host, client, 600, func(events []network.ChatEvent) bool {
			return countKind(events, network.ChatEventTaskCompleted) == 1 &&
				countKind(events, network.ChatEventCompanionSpeech) >= 1
		})
		assertStrictlyIncreasingEventIDs(t, events)
		return events
	}
	memory := run("memory")
	tcp := run("tcp")
	memoryProjection := projectDialogueParityEvents(memory)
	tcpProjection := projectDialogueParityEvents(tcp)
	if len(memoryProjection) == 0 {
		t.Fatal("Memory 传输没有任何事件")
	}
	if len(memoryProjection) != len(tcpProjection) {
		t.Fatalf("事件数不一致 memory=%v tcp=%v", memoryProjection, tcpProjection)
	}
	for index := range memoryProjection {
		if memoryProjection[index] != tcpProjection[index] {
			t.Fatalf("事件 %d 不一致：memory=%+v tcp=%+v",
				index, memoryProjection[index], tcpProjection[index])
		}
	}
	speech := 0
	for _, projection := range memoryProjection {
		if projection.Kind == network.ChatEventCompanionSpeech {
			speech++
		}
	}
	if speech == 0 {
		t.Fatalf("没有任何 Speech 事件（投影=%v）", memoryProjection)
	}
}
