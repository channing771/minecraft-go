// 持续跟随（follow 步骤）的编排测试：距离边界内停止移动、目标远离后恢复
// 跟随、目标离线以 TaskFailWorldChanged 失败并推进 FIFO、deadline 豁免
// （运行超过 timeout 不转 TimedOut）、恢复的 Running follow 先验目标在线性，
// 以及规划快照 OnlinePlayers 的填充。全部使用 httptest 假模型，绝不访问
// 真实模型服务；玩家位置经 SetPlayerPositionForTest 构造确定性几何。
package server

import (
	"bytes"
	"encoding/json"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/sim"
)

// followPlanContent 构造单一 follow 步骤的受限计划 JSON 文本：目标玩家以
// canonical UUIDv4 文本表达（与生产 planner 契约一致）。脚本条目是计划
// 正文本身，chat envelope 由假模型 handler 统一包装（与 planContentJSON
// 的既有约定一致）。
func followPlanContent(playerID core.PlayerID) string {
	encoded, _ := json.Marshal(map[string]any{
		"summary": "持续跟随",
		"steps":   []map[string]any{{"kind": "follow", "player_id": playerID.String()}},
	})
	return string(encoded)
}

// setPlayerPosition 把一名在线玩家放到指定权威位置（测试构造确定性几何）。
func setPlayerPosition(t *testing.T, host *Host, session sim.SessionID, position [3]float32) {
	t.Helper()
	host.world.SetPlayerPositionForTest(session, mgl32.Vec3(position))
}

// followHorizontalDistance 返回两个位置的水平（XZ）距离。
func followHorizontalDistance(a, b [3]float32) float32 {
	dx, dz := a[0]-b[0], a[2]-b[2]
	return float32(math.Sqrt(float64(dx*dx + dz*dz)))
}

// stepFollowTick 推进一个权威 tick 并排空全部客户端消息，保持流同步。
func stepFollowTick(t *testing.T, host *Host, clients []network.ClientEndpoint) {
	t.Helper()
	result := host.world.StepForTest()
	for _, endpoint := range clients {
		receiveCompanionChatTick(t, endpoint, result.Tick)
	}
}

// stopFollowTask 用停止旁路结束持续跟随，让队列在测试收尾（关服落盘）前
// 回到空态：既有 schema v2 尚不能编码 follow 步骤，M5C 任务 8 才交付 v3
// 变长编码；不断开会令 Flush 以编码失败告终。断言 TaskStopped 确认旁路
// 真正生效，而不只是发送了指令。
func stopFollowTask(t *testing.T, host *Host, clients []network.ClientEndpoint) {
	t.Helper()
	sendIntegration(t, clients[0], network.ChatCommand{Text: "@阿木 停止"})
	waitForIncomingChatDepth(t, host.world, 1)
	result := host.world.StepForTest()
	stopped := false
	for _, endpoint := range clients {
		for _, event := range companionChatEvents(receiveCompanionChatTick(t, endpoint, result.Tick)) {
			if event.Kind == network.ChatEventTaskStopped {
				stopped = true
			}
		}
	}
	if !stopped {
		t.Fatal("停止旁路未终止持续跟随（关服前的 v2 落盘会因此失败）")
	}
}

// waitFollowSettle 等待伙伴进入目标附近的跟随距离边界内并静止：先推进至
// 水平距离进入边界附近（允许输入切断后的惯性滑行），再观察一段静置窗口，
// 静置后位置必须稳定且与目标保持可感知的距离（既不贴脸也不停在边界外）。
func waitFollowSettle(
	t *testing.T,
	host *Host,
	clients []network.ClientEndpoint,
	id companion.ID,
	target [3]float32,
	maxTicks int,
) companion.Body {
	t.Helper()
	for range maxTicks {
		stepFollowTick(t, host, clients)
		if body := currentCompanionBody(t, host, id); followHorizontalDistance(body.Position, target) <= 4.8 {
			break
		}
	}
	// 惯性滑行窗口：输入切断后若干 tick 内位移归零。
	for range 30 {
		stepFollowTick(t, host, clients)
	}
	before := currentCompanionBody(t, host, id).Position
	for range 20 {
		stepFollowTick(t, host, clients)
	}
	after := currentCompanionBody(t, host, id)
	dx, dz := after.Position[0]-before[0], after.Position[2]-before[2]
	if dx*dx+dz*dz > 0.01 {
		t.Fatalf("跟随距离内仍在移动：%v -> %v", before, after.Position)
	}
	distance := followHorizontalDistance(after.Position, target)
	if distance < 1 || distance > 5.5 {
		t.Fatalf("静止位置与目标距离=%f（目标=%v 伙伴=%v），想要 (1, 5.5)："+
			"既不得贴到目标身上，也不得停在跟随边界外", distance, target, after.Position)
	}
	return after
}

// TestCompanionManagerFollowDistanceBoundaryAndResume 验证持续跟随的距离
// 边界双向语义：目标在跟随距离（CompanionFollowDistanceBlocks，水平）外时
// 伙伴复用既有寻路向目标移动；进入边界内后停止提交移动输入并保持原地；
// 目标再次远离后恢复跟随（向新位置移动并再次停在边界内）。
func TestCompanionManagerFollowDistanceBoundaryAndResume(t *testing.T) {
	definitions := []companion.Definition{{ID: chatTestCompanionID(1), Name: "阿木"}}
	model := newFakeCompanionModel(t)
	host := newCompanionManagerHost(t, definitions, model, nil)
	issuer := openCompanionChatClient(t, host, "memory", integrationIdentity(0x91, "发令者"))
	targetIdentity := integrationIdentity(0x92, "跟随目标")
	targetClient := openCompanionChatClient(t, host, "memory", targetIdentity)
	clients := []network.ClientEndpoint{issuer, targetClient}
	body := stepUntilCompanionManagerReady(t, host, clients, definitions[0].ID)
	if want := companion.CompanionFollowDistanceBlocks; want != 4 {
		t.Fatalf("跟随距离常量=%d，想要 4", want)
	}

	// 阶段一：目标在 +X 方向 10 格（跟随距离外），伙伴必须接近并停在边界内。
	targetLogin := activeLoginForPlayer(t, host, targetIdentity.PlayerID)
	firstTarget := [3]float32{body.Position[0] + 10, 1, body.Position[2]}
	setPlayerPosition(t, host, targetLogin.Session, firstTarget)
	model.setPlanScript(followPlanContent(targetIdentity.PlayerID))
	sendIntegration(t, issuer, network.ChatCommand{Text: "@阿木 跟着她"})

	settled := waitFollowSettle(t, host, clients, definitions[0].ID, firstTarget, 800)
	if settled.Position[0] <= body.Position[0]+2 {
		t.Fatalf("跟随距离外未向目标移动：%v -> %v", body.Position, settled.Position)
	}

	// 阶段二：目标远离（反方向 8 格），伙伴必须恢复跟随并再次停在边界内。
	secondTarget := [3]float32{settled.Position[0] - 8, 1, settled.Position[2]}
	setPlayerPosition(t, host, targetLogin.Session, secondTarget)
	resumed := waitFollowSettle(t, host, clients, definitions[0].ID, secondTarget, 800)
	if resumed.Position[0] >= settled.Position[0]-2 {
		t.Fatalf("目标远离后未恢复跟随：%v -> %v（新目标 %v）",
			settled.Position, resumed.Position, secondTarget)
	}
	stopFollowTask(t, host, clients)
}

// TestCompanionManagerFollowTargetOfflineFailsWorldChanged 验证持续跟随的
// 目标玩家断开后，任务在 tick 边界以 TaskFailWorldChanged 失败并广播，
// FIFO 推进原队首指令（下一任务获得 TaskStarted）。
func TestCompanionManagerFollowTargetOfflineFailsWorldChanged(t *testing.T) {
	definitions := []companion.Definition{{ID: chatTestCompanionID(1), Name: "阿木"}}
	model := newFakeCompanionModel(t)
	host := newCompanionManagerHost(t, definitions, model, nil)
	issuer := openCompanionChatClient(t, host, "memory", integrationIdentity(0x93, "发令者"))
	targetIdentity := integrationIdentity(0x94, "跟随目标")
	targetClient := openCompanionChatClient(t, host, "memory", targetIdentity)
	clients := []network.ClientEndpoint{issuer, targetClient}
	body := stepUntilCompanionManagerReady(t, host, clients, definitions[0].ID)

	// 目标放在跟随距离内：跟随保持原地，失败判定不受移动噪声干扰。
	targetLogin := activeLoginForPlayer(t, host, targetIdentity.PlayerID)
	setPlayerPosition(t, host, targetLogin.Session,
		[3]float32{body.Position[0] + 2, 1, body.Position[2]})
	// 脚本：首次规划返回 follow 计划，后续（FIFO 推进后的原队首）返回 go_to。
	goal := [3]int32{int32(body.Position[0]) + 2, 1, int32(body.Position[2])}
	model.setPlanScript(
		followPlanContent(targetIdentity.PlayerID),
		planContentJSON([][3]int32{goal}),
	)
	sendIntegration(t, issuer, network.ChatCommand{Text: "@阿木 跟着她"})
	sendIntegration(t, issuer, network.ChatCommand{Text: "@阿木 再走一步"})
	waitForIncomingChatDepth(t, host.world, 2)

	// 等待跟随任务真正进入 Running 后再断开目标。
	started := false
	for range 200 {
		result := host.world.StepForTest()
		for _, endpoint := range clients {
			messages := receiveCompanionChatTick(t, endpoint, result.Tick)
			for _, event := range companionChatEvents(messages) {
				if event.Kind == network.ChatEventTaskStarted && event.Command == "跟着她" {
					started = true
				}
			}
		}
		if started {
			break
		}
	}
	if !started {
		t.Fatal("跟随任务始终未进入 Running")
	}

	if err := targetClient.Close(); err != nil {
		t.Fatalf("关闭目标连接: %v", err)
	}
	waitForPlayerReleased(t, host, targetIdentity.PlayerID)

	// 目标离线后的推进：跟随任务失败(WorldChanged) 且 FIFO 推进原队首。
	var collected []network.ChatEvent
	failed, nextStarted := false, false
	for range 200 {
		result := host.world.StepForTest()
		events := companionChatEvents(receiveCompanionChatTick(t, issuer, result.Tick))
		collected = append(collected, events...)
		for _, event := range events {
			if event.Kind == network.ChatEventTaskFailed && event.Command == "跟着她" {
				failed = true
			}
			if event.Kind == network.ChatEventTaskStarted && event.Command == "再走一步" {
				nextStarted = true
			}
		}
		if failed && nextStarted {
			break
		}
	}
	failedEvents := make([]network.ChatEvent, 0, 1)
	for _, event := range collected {
		if event.Kind == network.ChatEventTaskFailed && event.Command == "跟着她" {
			failedEvents = append(failedEvents, event)
		}
	}
	if len(failedEvents) != 1 {
		t.Fatalf("跟随任务 TaskFailed=%d（事件=%v），想要恰好 1 次",
			len(failedEvents), chatEventKinds(collected))
	}
	if network.TaskFailReason(failedEvents[0].RejectReason) != network.TaskFailWorldChanged {
		t.Fatalf("失败原因=%d，想要 WorldChanged", failedEvents[0].RejectReason)
	}
	if err := failedEvents[0].Validate(); err != nil {
		t.Fatalf("TaskFailed Validate: %v", err)
	}
	if !nextStarted {
		t.Fatalf("目标离线失败后 FIFO 未推进原队首（事件=%v）", chatEventKinds(collected))
	}
}

// TestCompanionManagerFollowExemptFromDeadline 验证持续跟随不受执行时长
// 限制：跟随任务运行超过 taskTimeoutMinutes 配置的世界时间后仍保持
// Running，绝不转入 TimedOut（deadline 零值豁免），也没有任何失败事件。
func TestCompanionManagerFollowExemptFromDeadline(t *testing.T) {
	definitions := []companion.Definition{{ID: chatTestCompanionID(1), Name: "阿木"}}
	model := newFakeCompanionModel(t)
	host := newCompanionManagerHost(t, definitions, model, func(config *Config) {
		config.AIModel.TaskTimeoutMinutes = 1
	})
	identity := integrationIdentity(0x95, "发令者")
	client := openCompanionChatClient(t, host, "memory", identity)
	clients := []network.ClientEndpoint{client}
	body := stepUntilCompanionManagerReady(t, host, clients, definitions[0].ID)

	// 目标即发令者本人，且就在跟随距离内：任务保持原地，纯计时观察。
	login := activeLoginForPlayer(t, host, identity.PlayerID)
	setPlayerPosition(t, host, login.Session,
		[3]float32{body.Position[0] + 2, 1, body.Position[2]})
	model.setPlanScript(followPlanContent(identity.PlayerID))
	sendIntegration(t, client, network.ChatCommand{Text: "@阿木 跟着我"})
	waitForIncomingChatDepth(t, host.world, 1)

	// 1 分钟 = 1200 tick；多推进一段确保越过假想的 deadline。
	var events []network.ChatEvent
	for range companion.TicksPerMinute + 300 {
		result := host.world.StepForTest()
		events = append(events, companionChatEvents(receiveCompanionChatTick(t, client, result.Tick))...)
	}
	if len(eventsWithKind(events, network.ChatEventTaskStarted)) == 0 {
		t.Fatalf("缺少 TaskStarted（事件=%v）", chatEventKinds(events))
	}
	if len(eventsWithKind(events, network.ChatEventTaskTimedOut)) != 0 {
		t.Fatalf("持续跟随转入 TimedOut：%v", chatEventKinds(events))
	}
	if len(eventsWithKind(events, network.ChatEventTaskFailed)) != 0 {
		t.Fatalf("持续跟随出现失败事件：%v", chatEventKinds(events))
	}

	// 任务仍在 Running 且 deadline 保持零值（豁免的直接状态事实）。
	host.world.stepMu.Lock()
	current, hasCurrent := host.world.companionManager.slots[definitions[0].ID].queue.Current()
	host.world.stepMu.Unlock()
	if !hasCurrent || current.State != companion.TaskRunning {
		t.Fatalf("跟随任务状态=%v has=%v，想要仍在 Running", current.State, hasCurrent)
	}
	if current.DeadlineTicks != 0 {
		t.Fatalf("跟随任务记录了 deadline=%d，想要零值豁免", current.DeadlineTicks)
	}
	stopFollowTask(t, host, clients)
}

// TestCompanionManagerFollowRestoredTaskValidatesTargetOnline 验证恢复的
// Running follow 任务在下一动作前先验目标在线性：离线目标首 tick 即以
// WorldChanged 失败；在线目标则继续跟随（向目标移动）。
func TestCompanionManagerFollowRestoredTaskValidatesTargetOnline(t *testing.T) {
	t.Run("OfflineTargetFailsFirstTick", func(t *testing.T) {
		definitions := []companion.Definition{{ID: chatTestCompanionID(1), Name: "阿木"}}
		host := newCompanionManagerHost(t, definitions, nil, nil)
		client := openCompanionChatClient(t, host, "memory", integrationIdentity(0x96, "发令者"))
		clients := []network.ClientEndpoint{client}
		stepUntilCompanionManagerReady(t, host, clients, definitions[0].ID)

		// 合法 UUIDv4 但从未登录的目标：恢复现场等价于「目标已离线」。
		absent := integrationIdentity(0x97, "不在者")
		injectRunningCompanionTask(t, host, definitions[0].ID,
			stopTestIssuer(integrationIdentity(0x98, "原发令者")), "跟着我",
			[]companion.PlanStep{{Kind: companion.PlanStepFollow, PlayerID: absent.PlayerID}})

		result := host.world.StepForTest()
		events := companionChatEvents(receiveCompanionChatTick(t, client, result.Tick))
		if len(events) != 1 || events[0].Kind != network.ChatEventTaskFailed ||
			network.TaskFailReason(events[0].RejectReason) != network.TaskFailWorldChanged ||
			events[0].Command != "跟着我" {
			t.Fatalf("首 tick 事件=%+v，想要唯一 TaskFailed(WorldChanged)", events)
		}
		if err := events[0].Validate(); err != nil {
			t.Fatalf("TaskFailed Validate: %v", err)
		}
		host.world.stepMu.Lock()
		_, hasCurrent := host.world.companionManager.slots[definitions[0].ID].queue.Current()
		host.world.stepMu.Unlock()
		if hasCurrent {
			t.Fatal("离线失败后当前任务槽未清空")
		}
	})

	t.Run("OnlineTargetContinuesFollowing", func(t *testing.T) {
		definitions := []companion.Definition{{ID: chatTestCompanionID(1), Name: "阿木"}}
		host := newCompanionManagerHost(t, definitions, nil, nil)
		client := openCompanionChatClient(t, host, "memory", integrationIdentity(0x99, "发令者"))
		targetIdentity := integrationIdentity(0x9a, "跟随目标")
		targetClient := openCompanionChatClient(t, host, "memory", targetIdentity)
		clients := []network.ClientEndpoint{client, targetClient}
		body := stepUntilCompanionManagerReady(t, host, clients, definitions[0].ID)

		// 目标在跟随距离外 8 格：恢复的 Running follow 必须继续跟随。
		targetLogin := activeLoginForPlayer(t, host, targetIdentity.PlayerID)
		target := [3]float32{body.Position[0] + 8, 1, body.Position[2]}
		setPlayerPosition(t, host, targetLogin.Session, target)
		injectRunningCompanionTask(t, host, definitions[0].ID,
			stopTestIssuer(integrationIdentity(0x9b, "原发令者")), "跟着我",
			[]companion.PlanStep{{Kind: companion.PlanStepFollow, PlayerID: targetIdentity.PlayerID}})

		moved := float32(0)
		var events []network.ChatEvent
		for range 600 {
			result := host.world.StepForTest()
			for _, endpoint := range clients {
				events = append(events,
					companionChatEvents(receiveCompanionChatTick(t, endpoint, result.Tick))...)
			}
			current := currentCompanionBody(t, host, definitions[0].ID)
			moved = current.Position[0] - body.Position[0]
			if moved >= 3 {
				break
			}
		}
		if moved < 3 {
			t.Fatalf("恢复的 follow 未继续跟随：位移=%f（起点 %v 目标 %v）",
				moved, body.Position, target)
		}
		for _, event := range events {
			if event.Kind == network.ChatEventTaskFailed {
				t.Fatalf("在线目标的恢复 follow 出现失败事件：%+v", event)
			}
		}
		stopFollowTask(t, host, clients)
	})
}

// TestCompanionManagerFollowSnapshotOnlinePlayers 验证规划快照的在线玩家
// 集合从会话注册表正确填充：包含全部在线玩家的稳定 ID 与权威位置、按 ID
// 严格升序，且不超过 MaxPlanOnlinePlayers（快照 Validate 的既有门禁）。
func TestCompanionManagerFollowSnapshotOnlinePlayers(t *testing.T) {
	definitions := []companion.Definition{{ID: chatTestCompanionID(1), Name: "阿木"}}
	host := newCompanionManagerHost(t, definitions, nil, nil)
	firstIdentity := integrationIdentity(0x9c, "玩家甲")
	secondIdentity := integrationIdentity(0x9d, "玩家乙")
	first := openCompanionChatClient(t, host, "memory", firstIdentity)
	second := openCompanionChatClient(t, host, "memory", secondIdentity)
	clients := []network.ClientEndpoint{first, second}
	body := stepUntilCompanionManagerReady(t, host, clients, definitions[0].ID)

	firstLogin := activeLoginForPlayer(t, host, firstIdentity.PlayerID)
	secondLogin := activeLoginForPlayer(t, host, secondIdentity.PlayerID)
	host.world.stepMu.Lock()
	issuer := host.world.companionManager.captureIssuer(
		firstIdentity.PlayerID, "玩家甲", firstLogin.Session,
	)
	firstPlayer, firstOK := host.world.engine.Player(firstLogin.Session)
	secondPlayer, secondOK := host.world.engine.Player(secondLogin.Session)
	snapshot, err := host.world.companionManager.buildPlanSnapshot(
		definitions[0], companion.TaskCommand("跟着谁"), issuer, body,
	)
	host.world.stepMu.Unlock()
	if err != nil {
		t.Fatalf("构造快照: %v", err)
	}
	if !firstOK || !secondOK {
		t.Fatalf("权威玩家读取失败：first=%v second=%v", firstOK, secondOK)
	}

	online := snapshot.OnlinePlayers
	if len(online) != 2 {
		t.Fatalf("快照在线玩家=%d（%v），想要 2", len(online), online)
	}
	if len(online) > companion.MaxPlanOnlinePlayers {
		t.Fatalf("快照在线玩家 %d 超过上限 %d", len(online), companion.MaxPlanOnlinePlayers)
	}
	for index := 1; index < len(online); index++ {
		if bytes.Compare(online[index-1].ID[:], online[index].ID[:]) >= 0 {
			t.Fatalf("在线玩家未按 ID 严格升序：%s 后跟 %s",
				online[index-1].ID, online[index].ID)
		}
	}
	want := map[core.PlayerID][3]float32{
		firstIdentity.PlayerID:  [3]float32(firstPlayer.State.Position),
		secondIdentity.PlayerID: [3]float32(secondPlayer.State.Position),
	}
	for _, player := range online {
		wantPosition, known := want[player.ID]
		if !known {
			t.Fatalf("快照包含未登录玩家 %s", player.ID)
		}
		if player.Position != wantPosition {
			t.Fatalf("玩家 %s 快照位置=%v，想要权威位置 %v",
				player.ID, player.Position, wantPosition)
		}
	}
}
