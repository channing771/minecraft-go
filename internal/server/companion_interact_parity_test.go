// M5C 多人一致性 parity 测试（任务组 9 的 9.1）：两名在线玩家共同指挥同一
// 伙伴——玩家甲发起持续跟随（假模型返回 follow 计划），玩家乙先后发出
// 「停止」旁路（先在队首提升前早到、以 NotFollowing 同步拒绝，再于跟随
// Running 期触发 TaskStopped）与后续普通指令（两步 go_to、命中非法计划的
// 指令、mine 采掘）。Memory 与 TCP 两种传输必须产出完全一致的 ChatEvent
// transcript（EventID、发令者身份、拒绝/失败原因全同）、同一组任务状态
// 快照与可比的世界结果（采掘方块清空、伙伴背包逐字段一致、容差内的伙伴
// 位置）。全部使用 httptest 假模型，绝不打开前台窗口或访问真实模型服务。
package server

import (
	"context"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/sim"
	"github.com/channing771/mornlea/internal/storage"
)

// interactionFollowMovementTicks 是 TaskStarted(跟着我) 观测点到「停止」指令
// 之间的固定推进 tick 数：给持续跟随留出真实的位移窗口（世界结果可比），
// 同时把两传输的位移 tick 数钉在同一事件锚点上。
const interactionFollowMovementTicks = 40

// interactionParityPositionTolerance 是两传输伙伴位置比较的欧氏距离容差：
// 事件与状态序列逐字节可比，位置只做容差比较——模型/寻路 worker 的
// goroutine 调度可让结果晚一两个权威 tick 落地，位移因此带有约一个移动
// tick 的抖动。这是传输层 parity 之外的调度噪声，用亚格级容差吸收；容差
// 远小于跟随/走步/采掘各阶段的净位移，不会掩盖真实分歧。
const interactionParityPositionTolerance = float32(0.75)

// interactionParityState 是一个权威任务状态快照的可比投影，只保留跨传输
// 确定的字段。StartTick/Generation/DeadlineTicks 的绝对值随事件落地 tick
// 漂移，因此 deadline 只记录零值事实（follow 的豁免形态、从未 Running 的
// 任务自然为零）。终态清槽后 Snapshot 仍保留最后一个任务值（持久化观察
// 语义），因此 HasCurrent=false 的快照携带的是刚终结任务的终态。
type interactionParityState struct {
	HasCurrent   bool
	State        companion.TaskState
	Command      companion.TaskCommand
	StepIndex    int
	DeadlineZero bool
	Pending      int
}

// interactionParityResult 是一次传输运行收集的全部可比事实：双客户端事件
// transcript、各阶段任务状态快照、伙伴位置/背包与采掘方块的世界结果，以及
// 假模型的请求计数（指令→规划请求的一对一事实）。
type interactionParityResult struct {
	Transcript        []companionChatTranscriptEvent
	States            []interactionParityState
	SpawnPosition     [3]float32
	AfterStopPosition [3]float32
	AfterGoToPosition [3]float32
	FinalPosition     [3]float32
	FinalInventory    core.Inventory
	MinedBlock        core.BlockID
	MineTargetCenter  [3]float32
	ModelRequests     int
}

// interactionExpectedEvent 是期望 transcript 条目的紧凑描述：接收者、全局
// EventID、事件类别、原始指令、发令者显示名与（拒绝/失败时的）原因枚举。
type interactionExpectedEvent struct {
	recipient  int
	eventID    uint64
	kind       network.ChatEventKind
	command    string
	issuerName string
	reason     network.ChatRejectReason
}

// newInteractionParityHost 构造多人指挥 parity 用的 Host：存档预置一条与配置
// ID 匹配的伙伴身体记录（确定性出生位置与石镐背包——mine 阶段的工具事实），
// 心跳置为一小时以避免长推进窗口内的保活噪声。
func newInteractionParityHost(
	t *testing.T,
	id companion.ID,
	model *fakeCompanionModel,
) *Host {
	t.Helper()
	store := newHostTestStore()
	seed := companion.Body{
		ID:        id,
		Dimension: core.Overworld,
		Position:  interactionCompanionPosition,
		Inventory: pickaxeInventory(),
	}
	if err := store.MemoryStore.SaveCompanions(
		context.Background(), storage.CompanionSave{Revision: 1, Records: []companion.Body{seed}},
	); err != nil {
		t.Fatalf("种子伙伴身体: %v", err)
	}
	config := hostTestConfig()
	config.Companions = []companion.Definition{{ID: id, Name: "阿木"}}
	config.MaxPlayers = 2
	config.OutboxCapacity = 4096
	config.HeartbeatInterval = time.Hour
	config.HeartbeatTimeout = time.Hour
	config.AIModel.Endpoint = model.server.URL + "/v1"
	host := mustNewHost(t, config, flatTestGenerator{}, store)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), longWaitDeadline)
		defer cancel()
		if err := host.Shutdown(ctx); err != nil {
			t.Errorf("Host.Shutdown: %v", err)
		}
	})
	return host
}

// captureInteractionParityState 在 stepMu 下抓取伙伴任务域的可比状态投影。
func captureInteractionParityState(
	t *testing.T,
	host *Host,
	id companion.ID,
) interactionParityState {
	t.Helper()
	host.world.stepMu.Lock()
	snapshot := host.world.companionManager.slots[id].queue.Snapshot()
	host.world.stepMu.Unlock()
	return interactionParityState{
		HasCurrent:   snapshot.HasCurrent,
		State:        snapshot.Current.State,
		Command:      snapshot.Current.Command,
		StepIndex:    snapshot.Current.StepIndex,
		DeadlineZero: snapshot.Current.DeadlineTicks == 0,
		Pending:      len(snapshot.Pending),
	}
}

// interactionParityEventOf 返回匹配 (kind, command) 的事件判定闭包。
func interactionParityEventOf(
	kind network.ChatEventKind,
	command string,
) func(network.ChatEvent) bool {
	return func(event network.ChatEvent) bool {
		return event.Kind == kind && event.Command == command
	}
}

// runCompanionInteractionParity 在指定传输上执行完整的多人共同指挥脚本并
// 返回全部可比事实。指令发送严格串行：每条指令都先等到进入服务端 ingress
// 队列再发下一条——两个会话各有一条读循环，不串行化时两会话指令在
// incomingChats 里的先后次序存在竞争，EventID 分配顺序随之不可复现。
func runCompanionInteractionParity(t *testing.T, transport string) interactionParityResult {
	t.Helper()
	id := chatTestCompanionID(1)
	model := newFakeCompanionModel(t)
	host := newInteractionParityHost(t, id, model)
	firstIdentity := integrationIdentity(0xb1, "玩家甲")
	secondIdentity := integrationIdentity(0xb2, "玩家乙")
	first := openCompanionChatClient(t, host, transport, firstIdentity)
	second := openCompanionChatClient(t, host, transport, secondIdentity)
	clients := []network.ClientEndpoint{first, second}
	body := stepUntilCompanionManagerReady(t, host, clients, id)
	result := interactionParityResult{
		Transcript:    make([]companionChatTranscriptEvent, 0, 32),
		SpawnPosition: body.Position,
	}

	// 玩家甲放到 +X 方向 10 格（跟随距离外）：跟随阶段有真实位移可观察。
	firstLogin := activeLoginForPlayer(t, host, firstIdentity.PlayerID)
	setPlayerPosition(t, host, firstLogin.Session,
		[3]float32{body.Position[0] + 10, 1, body.Position[2]})
	// 采掘目标：出生点 -X 方向两格的煤炭矿（walk 阶段不经过、mine 阶段走近）。
	baseX, baseZ := int32(body.Position[0]), int32(body.Position[2])
	mineTarget := core.BlockPos{X: baseX - 2, Y: 1, Z: baseZ}
	setInteractionBlock(t, host, mineTarget, core.CoalOreID)

	// 假模型脚本按请求序：follow 计划（目标玩家甲）→ 两步 go_to → 非法
	// 计划正文 → mine 计划。请求与指令一一对应，无重试。
	model.setPlanScript(
		followPlanContent(firstIdentity.PlayerID),
		planContentJSON([][3]int32{{baseX + 6, 1, baseZ}, {baseX + 4, 1, baseZ}}),
		"这不是合法计划",
		minePlanJSON(mineTarget),
	)

	// stepParityTick 推进一个权威 tick 并按客户端顺序排空全部消息，把
	// ChatEvent 追加进 transcript（保持流同步：每 tick 每客户端必被排空）。
	stepParityTick := func() sim.TickResult {
		tickResult := host.world.StepForTest()
		for recipient, endpoint := range clients {
			for _, event := range companionChatEvents(receiveCompanionChatTick(t, endpoint, tickResult.Tick)) {
				result.Transcript = append(result.Transcript, companionChatTranscriptEvent{
					Recipient: recipient,
					Event:     event,
				})
			}
		}
		return tickResult
	}
	// stepUntilParity 推进至 match 命中（先排空本 tick 全部客户端再判定）或
	// 步数耗尽；match 为 nil 时只做定长推进。
	stepUntilParity := func(maxTicks int, match func(network.ChatEvent) bool) bool {
		for range maxTicks {
			if match == nil {
				stepParityTick()
				continue
			}
			hit := false
			tickResult := host.world.StepForTest()
			for recipient, endpoint := range clients {
				for _, event := range companionChatEvents(receiveCompanionChatTick(t, endpoint, tickResult.Tick)) {
					result.Transcript = append(result.Transcript, companionChatTranscriptEvent{
						Recipient: recipient,
						Event:     event,
					})
					if match(event) {
						hit = true
					}
				}
			}
			if hit {
				return true
			}
		}
		return false
	}

	// 预热：把「区块就绪」这一与传输无关的异步事实移出受测窗口（见
	// warmInteractionParityPathWindow 注释），两传输的位移才能从同一事件
	// 锚点起步。
	warmInteractionParityPathWindow(t, host, id, stepParityTick)

	// 阶段一：甲发跟随、乙的「停止」早到。同一 drain 内甲的指令只入 FIFO
	//（队首提升发生在其后的编排阶段），乙的「停止」无可停任务，必须同步
	// 拒绝为 NotFollowing 且只回乙。快照锁定 Planning 态。
	sendIntegration(t, first, network.ChatCommand{Text: "@阿木 跟着我"})
	waitForIncomingChatDepth(t, host.world, 1)
	sendIntegration(t, second, network.ChatCommand{Text: "@阿木 停止"})
	waitForIncomingChatDepth(t, host.world, 2)
	stepUntilParity(1, nil)
	result.States = append(result.States, captureInteractionParityState(t, host, id))

	// 阶段二：跟随进入 Running（TaskStarted 广播），固定推进若干 tick 让
	// 伙伴真实位移，随后乙的「停止」命中旁路：Running + follow 计划 →
	// TaskStopped，携带原发令者甲（而非停止者乙）的身份。
	if !stepUntilParity(300, interactionParityEventOf(network.ChatEventTaskStarted, "跟着我")) {
		t.Fatal("跟随任务始终未 TaskStarted")
	}
	result.States = append(result.States, captureInteractionParityState(t, host, id))
	for range interactionFollowMovementTicks {
		stepParityTick()
	}
	sendIntegration(t, second, network.ChatCommand{Text: "@阿木 停止"})
	waitForIncomingChatDepth(t, host.world, 1)
	if !stepUntilParity(3, interactionParityEventOf(network.ChatEventTaskStopped, "跟着我")) {
		t.Fatal("停止旁路未在跟随 Running 期终结任务")
	}
	result.States = append(result.States, captureInteractionParityState(t, host, id))
	result.AfterStopPosition = currentCompanionBody(t, host, id).Position

	// 阶段三：乙的普通指令（两步 go_to）：Accepted→Started→Progress→
	// Completed 全程广播；Progress 后的快照锁定 Running 的 StepIndex 与
	// 非豁免 deadline（普通任务有自然终点，不享 follow 的零值豁免）。
	sendIntegration(t, second, network.ChatCommand{Text: "@阿木 走两步"})
	waitForIncomingChatDepth(t, host.world, 1)
	if !stepUntilParity(600, interactionParityEventOf(network.ChatEventTaskProgress, "走两步")) {
		t.Fatal("go_to 任务未推进到 TaskProgress")
	}
	result.States = append(result.States, captureInteractionParityState(t, host, id))
	if !stepUntilParity(300, interactionParityEventOf(network.ChatEventTaskCompleted, "走两步")) {
		t.Fatal("go_to 任务未完成")
	}
	for range 3 {
		stepParityTick()
	}
	result.States = append(result.States, captureInteractionParityState(t, host, id))
	result.AfterGoToPosition = currentCompanionBody(t, host, id).Position

	// 阶段四：乙的指令命中非法计划：模型脚本第三条不是合法 JSON，任务以
	// TaskFailed(InvalidPlan) 终结并广播，FIFO 随即清空。
	sendIntegration(t, second, network.ChatCommand{Text: "@阿木 随便走走"})
	waitForIncomingChatDepth(t, host.world, 1)
	if !stepUntilParity(60, interactionParityEventOf(network.ChatEventTaskFailed, "随便走走")) {
		t.Fatal("非法计划未以 TaskFailed 终结")
	}
	result.States = append(result.States, captureInteractionParityState(t, host, id))

	// 阶段五：乙的 mine 指令：走近后按住采掘至完成——目标方块清空、煤炭
	// 入包、石镐耐久扣减，世界结果在同一权威 tick 原子成立。
	sendIntegration(t, second, network.ChatCommand{Text: "@阿木 挖那块矿"})
	waitForIncomingChatDepth(t, host.world, 1)
	if !stepUntilParity(900, interactionParityEventOf(network.ChatEventTaskCompleted, "挖那块矿")) {
		t.Fatal("mine 任务未完成")
	}
	for range 5 {
		stepParityTick()
	}
	result.States = append(result.States, captureInteractionParityState(t, host, id))
	final := currentCompanionBody(t, host, id)
	result.FinalPosition = final.Position
	result.FinalInventory = final.Inventory
	result.MinedBlock = interactionBlockAt(t, host, mineTarget)
	result.MineTargetCenter = [3]float32{
		float32(mineTarget.X) + 0.5, float32(mineTarget.Y) + 0.5, float32(mineTarget.Z) + 0.5,
	}
	result.ModelRequests, _, _, _ = model.snapshotCounts()
	return result
}

// warmInteractionParityPathWindow 推进世界直到伙伴寻路窗口的 3×3 区块全部
// ready。ViewRadius=0 的测试宿主只按伙伴兴趣集逐 tick 异步补块（Workers=1
// 时每次一块），冷启动的首次寻路要等窗口补齐——这是与传输无关的异步事实，
// 留在受测窗口内会让两传输的位移起点漂移。预热把它移出窗口；本脚本的全部
// 位移都留在伙伴出生的中心区块内（路径点 2..10 格均未跨区块），一次预热
// 覆盖全程。寻路窗口水平 ±16 格必然恰好覆盖中心区块 ±1 的 3×3，覆盖矩形
// 因此固定为 3×3。超时仍未 ready 属于环境异常，显式失败以区分于受测行为。
func warmInteractionParityPathWindow(
	t *testing.T,
	host *Host,
	id companion.ID,
	step func() sim.TickResult,
) {
	t.Helper()
	for range 600 {
		position := currentCompanionBody(t, host, id).Position
		center := (core.BlockPos{
			X: int32(math.Floor(float64(position[0]))),
			Z: int32(math.Floor(float64(position[2]))),
		}).Chunk()
		view := host.world.companionManager.chunkViewAt(core.Overworld, position)
		if view.allCoveredReady(core.ChunkPos{X: center.X - 1, Z: center.Z - 1}, 3, 3) {
			return
		}
		step()
	}
	t.Fatal("寻路窗口区块 600 tick 内未全部 ready，无法开始 parity 脚本")
}

// assertInteractionParityPosition 断言两传输的伙伴位置在容差内一致（容差
// 的理由见 interactionParityPositionTolerance 注释）。
func assertInteractionParityPosition(
	t *testing.T,
	label string,
	memory, tcpResult [3]float32,
) {
	t.Helper()
	dx, dy, dz := memory[0]-tcpResult[0], memory[1]-tcpResult[1], memory[2]-tcpResult[2]
	distance := float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))
	if distance > interactionParityPositionTolerance {
		t.Fatalf("%s 位置不一致：memory=%v tcp=%v（距离 %f 超过容差 %f）",
			label, memory, tcpResult, distance, interactionParityPositionTolerance)
	}
}

// TestCompanionInteractionMemoryTCPParity 验证 m5c-companion-interactions
// 任务 9.1 的多人一致性：两名玩家共同指挥同一伙伴时，Memory 与 TCP 传输的
// 任务状态序列、ChatEvent 事件序列与世界结果完全一致。事件按 (接收者，
// EventID，类别，指令，发令者，原因) 逐条锁定——包括乙的 NotFollowing 同步
// 拒绝（只回发令者）、甲发起任务的 TaskStopped 归属原发令者、非法计划的
// InvalidPlan 失败原因，以及 mine 完成后「方块清空 + 煤炭入包 + 耐久扣减」
// 的确定性世界结果。
func TestCompanionInteractionMemoryTCPParity(t *testing.T) {
	results := make(map[string]interactionParityResult, 2)
	for _, transport := range []string{"memory", "tcp"} {
		transport := transport
		t.Run(transport, func(t *testing.T) {
			results[transport] = runCompanionInteractionParity(t, transport)
		})
	}
	memory, tcpResult := results["memory"], results["tcp"]

	// 事件 transcript 与任务状态序列跨传输逐字节一致。
	if !reflect.DeepEqual(memory.Transcript, tcpResult.Transcript) {
		t.Fatalf("Memory/TCP 多人指挥 transcript 不一致\nMemory=%+v\nTCP=%+v",
			memory.Transcript, tcpResult.Transcript)
	}
	if !reflect.DeepEqual(memory.States, tcpResult.States) {
		t.Fatalf("Memory/TCP 任务状态序列不一致\nMemory=%+v\nTCP=%+v",
			memory.States, tcpResult.States)
	}
	// 世界结果：采掘方块、背包与出生位置逐字段一致；漂移位置按容差一致；
	// 指令与模型请求保持一对一。
	if memory.MinedBlock != tcpResult.MinedBlock {
		t.Fatalf("采掘方块 memory=%d tcp=%d", memory.MinedBlock, tcpResult.MinedBlock)
	}
	if !reflect.DeepEqual(memory.FinalInventory, tcpResult.FinalInventory) {
		t.Fatalf("伙伴背包不一致\nMemory=%+v\nTCP=%+v", memory.FinalInventory, tcpResult.FinalInventory)
	}
	if !reflect.DeepEqual(memory.SpawnPosition, tcpResult.SpawnPosition) {
		t.Fatalf("出生位置不一致 memory=%v tcp=%v", memory.SpawnPosition, tcpResult.SpawnPosition)
	}
	assertInteractionParityPosition(t, "停止后", memory.AfterStopPosition, tcpResult.AfterStopPosition)
	assertInteractionParityPosition(t, "go_to 后", memory.AfterGoToPosition, tcpResult.AfterGoToPosition)
	assertInteractionParityPosition(t, "最终", memory.FinalPosition, tcpResult.FinalPosition)
	if memory.ModelRequests != tcpResult.ModelRequests || memory.ModelRequests != 4 {
		t.Fatalf("模型请求 memory=%d tcp=%d，想要两传输各恰好 4 次",
			memory.ModelRequests, tcpResult.ModelRequests)
	}

	// 单传输锁定：任务状态快照序列（两传输已证一致，断言其一即覆盖两者）。
	// 依次为：跟随 Planning（deadline 尚零）→ 跟随 Running（deadline 零值
	// 豁免）→ 停止终态（槽位清空、终态记录保留）→ go_to Running 第二步
	//（deadline 已盖戳）→ go_to 完成终态 → 非法计划失败终态（从未 Running，
	// deadline 自然为零）→ mine 完成终态。
	wantStates := []interactionParityState{
		{HasCurrent: true, State: companion.TaskPlanning, Command: "跟着我", DeadlineZero: true},
		{HasCurrent: true, State: companion.TaskRunning, Command: "跟着我", DeadlineZero: true},
		{State: companion.TaskStopped, Command: "跟着我", DeadlineZero: true},
		{HasCurrent: true, State: companion.TaskRunning, Command: "走两步", StepIndex: 1},
		{State: companion.TaskCompleted, Command: "走两步", StepIndex: 1},
		{State: companion.TaskFailed, Command: "随便走走", DeadlineZero: true},
		{State: companion.TaskCompleted, Command: "挖那块矿"},
	}
	if !reflect.DeepEqual(memory.States, wantStates) {
		t.Fatalf("任务状态序列=%+v，想要 %+v", memory.States, wantStates)
	}

	// 单传输锁定：双接收者的完整事件身份表。广播事件两客户端各一条、
	// NotFollowing 只回乙；EventID 沿全服计数器严格递增（成功停止不占
	// drain 编号，TaskStopped 由编排阶段在同一计数器上取号）。
	firstIdentity := integrationIdentity(0xb1, "玩家甲")
	secondIdentity := integrationIdentity(0xb2, "玩家乙")
	want := []interactionExpectedEvent{
		{0, 1, network.ChatEventAccepted, "跟着我", "玩家甲", network.ChatRejectNone},
		{1, 1, network.ChatEventAccepted, "跟着我", "玩家甲", network.ChatRejectNone},
		{1, 2, network.ChatEventRejected, "停止", "玩家乙", network.ChatRejectNotFollowing},
		{0, 3, network.ChatEventTaskStarted, "跟着我", "玩家甲", network.ChatRejectNone},
		{1, 3, network.ChatEventTaskStarted, "跟着我", "玩家甲", network.ChatRejectNone},
		{0, 4, network.ChatEventTaskStopped, "跟着我", "玩家甲", network.ChatRejectNone},
		{1, 4, network.ChatEventTaskStopped, "跟着我", "玩家甲", network.ChatRejectNone},
		{0, 5, network.ChatEventAccepted, "走两步", "玩家乙", network.ChatRejectNone},
		{1, 5, network.ChatEventAccepted, "走两步", "玩家乙", network.ChatRejectNone},
		{0, 6, network.ChatEventTaskStarted, "走两步", "玩家乙", network.ChatRejectNone},
		{1, 6, network.ChatEventTaskStarted, "走两步", "玩家乙", network.ChatRejectNone},
		{0, 7, network.ChatEventTaskProgress, "走两步", "玩家乙", network.ChatRejectNone},
		{1, 7, network.ChatEventTaskProgress, "走两步", "玩家乙", network.ChatRejectNone},
		{0, 8, network.ChatEventTaskCompleted, "走两步", "玩家乙", network.ChatRejectNone},
		{1, 8, network.ChatEventTaskCompleted, "走两步", "玩家乙", network.ChatRejectNone},
		{0, 9, network.ChatEventAccepted, "随便走走", "玩家乙", network.ChatRejectNone},
		{1, 9, network.ChatEventAccepted, "随便走走", "玩家乙", network.ChatRejectNone},
		{0, 10, network.ChatEventTaskFailed, "随便走走", "玩家乙",
			network.ChatRejectReason(network.TaskFailInvalidPlan)},
		{1, 10, network.ChatEventTaskFailed, "随便走走", "玩家乙",
			network.ChatRejectReason(network.TaskFailInvalidPlan)},
		{0, 11, network.ChatEventAccepted, "挖那块矿", "玩家乙", network.ChatRejectNone},
		{1, 11, network.ChatEventAccepted, "挖那块矿", "玩家乙", network.ChatRejectNone},
		{0, 12, network.ChatEventTaskStarted, "挖那块矿", "玩家乙", network.ChatRejectNone},
		{1, 12, network.ChatEventTaskStarted, "挖那块矿", "玩家乙", network.ChatRejectNone},
		{0, 13, network.ChatEventTaskCompleted, "挖那块矿", "玩家乙", network.ChatRejectNone},
		{1, 13, network.ChatEventTaskCompleted, "挖那块矿", "玩家乙", network.ChatRejectNone},
	}
	if len(memory.Transcript) != len(want) {
		t.Fatalf("transcript 条数=%d，想要 %d（%v）",
			len(memory.Transcript), len(want), chatEventKinds(transcriptEventsOf(memory.Transcript)))
	}
	issuerIDs := map[string]core.PlayerID{
		"玩家甲": firstIdentity.PlayerID,
		"玩家乙": secondIdentity.PlayerID,
	}
	definitionID := chatTestCompanionID(1)
	for index, expected := range want {
		entry := memory.Transcript[index]
		event := entry.Event
		if entry.Recipient != expected.recipient ||
			event.EventID != expected.eventID ||
			event.Kind != expected.kind ||
			event.Command != expected.command ||
			event.PlayerName != expected.issuerName ||
			event.RejectReason != expected.reason ||
			event.PlayerID != issuerIDs[expected.issuerName] ||
			event.CompanionID != definitionID ||
			event.CompanionName != "阿木" {
			t.Fatalf("transcript[%d]=%+v（接收者 %d），想要 %+v",
				index, event, entry.Recipient, expected)
		}
		if err := event.Validate(); err != nil {
			t.Fatalf("transcript[%d] Validate: %v", index, err)
		}
	}
	for recipient := range 2 {
		stream := make([]network.ChatEvent, 0, 13)
		for _, entry := range memory.Transcript {
			if entry.Recipient == recipient {
				stream = append(stream, entry.Event)
			}
		}
		assertStrictlyIncreasingEventIDs(t, stream)
	}

	// 单传输锁定：世界结果的过程事实。跟随阶段必须朝玩家甲真实位移；mine
	// 完成后目标方块清空、煤炭恰好一件入包、石镐耐久 131→130，且最终位置
	// 位于采掘目标的交互可达范围内。
	if offset := memory.AfterStopPosition[0] - memory.SpawnPosition[0]; offset < 1.5 {
		t.Fatalf("跟随阶段位移=%f（%v→%v），想要朝玩家甲显著移动",
			offset, memory.SpawnPosition, memory.AfterStopPosition)
	}
	if memory.MinedBlock != core.AirID {
		t.Fatalf("采掘完成后目标方块=%d，想要空气", memory.MinedBlock)
	}
	if count := interactionInventoryCount(memory.FinalInventory, core.ItemCoal); count != 1 {
		t.Fatalf("产物煤炭数量=%d，想要 1（背包=%+v）", count, memory.FinalInventory)
	}
	pickaxe := memory.FinalInventory.Hotbar.Slots[0]
	if pickaxe.Item != core.ItemStonePickaxe || pickaxe.Durability != 130 {
		t.Fatalf("石镐=%+v，想要耐久 131→130", pickaxe)
	}
	if distance := interactionParityDistance(memory.FinalPosition, memory.MineTargetCenter); distance > 7 {
		t.Fatalf("最终位置 %v 距采掘目标 %v 为 %f，交互完成不可能超过交互距离",
			memory.FinalPosition, memory.MineTargetCenter, distance)
	}
}

// transcriptEventsOf 展开 transcript 的事件字段（失败信息用）。
func transcriptEventsOf(transcript []companionChatTranscriptEvent) []network.ChatEvent {
	events := make([]network.ChatEvent, len(transcript))
	for index := range transcript {
		events[index] = transcript[index].Event
	}
	return events
}

// interactionParityDistance 返回两位置的欧氏距离。
func interactionParityDistance(a, b [3]float32) float32 {
	dx, dy, dz := a[0]-b[0], a[1]-b[1], a[2]-b[2]
	return float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))
}
