// 本文件实现 server 侧 Companion Manager：任务 FIFO 与状态机的 tick 边界
// 编排。
//
// 并发模型（权威 tick 是唯一写者）：
//   - slots（队列/世代/路径/在途标记）只在持有 stepMu 的 tick 路径被读写：
//     step() 的 advanceCompanionTasks、drainIncomingChats 的入队、Shutdown
//     的冻结段；
//   - worker goroutine 只持有不可变值（PlanSnapshot、PathGrid），经有界
//     channel 回送结果；channel 与 semaphore 是它们触碰的全部共享状态；
//   - 结果只在 tick 边界非阻塞接收，世代或状态不符即丢弃。
//
// channel 容量论证：plannerResults 与 pathResults 容量 4——在途上限由
// “每伙伴 ≤1 规划 + 每伙伴 ≤1 寻路、伙伴数 ≤ companion.MaxActive=4”封顶，
// 满容量时 worker 经 ctx.Done 退出，绝不阻塞；结果每 tick 全量排空，容量
// 恰好覆盖峰值。
package server

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"math"
	"slices"
	"sync"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/physics"
	"github.com/channing771/mornlea/internal/sim"
	"github.com/channing771/mornlea/internal/storage"
)

// companionPlanner 是规划器依赖面：生产实现是 companion.PlannerClient，
// 测试可注入假模型端点构造的真客户端。
type companionPlanner interface {
	Plan(ctx context.Context, snapshot companion.PlanSnapshot) (companion.Plan, error)
}

// companionTaskIssuer 是入队时刻冻结的发令者事实。指令的规划输入不随发令者
// 后续移动漂移；身份字段供任务事件回溯“谁下了这条指令”。
type companionTaskIssuer struct {
	playerID   core.PlayerID
	name       string
	position   [3]float32
	yaw        float32
	pitch      float32
	lookHit    core.BlockPos
	hasLookHit bool
}

// companionTaskSlot 是一个伙伴的全部任务编排状态。只有权威 tick 写。
type companionTaskSlot struct {
	definition companion.Definition
	queue      companion.TaskQueue
	// issuers 与 queue.pending 一一配对：入队时追加，BeginHead 时消费，
	// 使事件能回溯每条指令的发令者。
	issuers        []companionTaskIssuer
	currentIssuer  companionTaskIssuer
	currentCommand companion.TaskCommand

	// planningInFlight 表示该伙伴有一个规划请求在途；在途期间绝不发起第二个。
	planningInFlight bool

	// 路径执行状态（仅 Running 有效）。三连失败预算属于单个任务而非槽位：
	// dispatchPlanning 消费新队首与 restoreQueue 成功恢复时都会把 policy
	// 归零，前一个任务的失败计数绝不削减下一个任务的预算。
	policy       companion.PathPolicy
	path         *companion.PathResult
	waypoint     int
	pathInFlight bool
	replanAtTick uint64
	hasReplanAt  bool

	// followGoal 是当前 follow 路径（或在途寻路请求）计算时的目标站立格。
	// 它只在「当前步骤是 follow 且 slot.path 非空」时被漂移判定消费；路径
	// 在别处被清空（终态/超时/停止）后残留的旧值不参与任何判定，下一次
	// follow 寻路派发会整体覆写。目标玩家持续移动时 advanceRunners 以它
	// 判定「终点漂移超出重算阈值」并丢弃既有路径，令 follow 复用 go_to 的
	// 寻路/冷却/三连失败语义而不必每 tick 重算。
	followGoal    companion.PathCell
	hasFollowGoal bool

	// 交互执行状态（仅 mine/place 步骤的 Running 任务有效）。interactStepIndex
	// 记录交互所属的步骤索引，步骤推进或任务更替后与 queue.Current 的
	// StepIndex 不再相等，交互记忆随之作废——跨步骤/跨任务边界因此自愈，
	// 终态路径无需逐一清理；任务边界（BeginHead/restore）仍经 resetInteraction
	// 显式归零，避免「新任务恰好也从 StepIndex 0 开始」时误继承就绪标记。
	interactStepIndex int
	// interactionReady 表示走近段已结束（路径走尽），进入按住采掘/提交放置
	// 的交互段；dispatchPathRequests 以它停止交互期间的寻路派发。
	interactionReady bool
	// miningHeld 表示 sim 侧采掘意图正在按住（已提交 MineHold 且未 Release）。
	// sim 的按住语义跨 tick 保持，步骤离开采掘时必须显式释放（见
	// releaseFinishedMining）。
	miningHeld bool
	// mineProgress/mineRequired 是最近一次活跃观察到的 sim 采掘进度与计时
	// 规则：同一目标/方块/工具的进度单调递增，回退或规则变化是 sim「目标
	// 替换失效」语义的唯一可观察征兆。
	mineProgress uint16
	mineRequired uint16
}

// plannerOutcome 是一次规划请求的结果，携带任务身份供过期判定。
type plannerOutcome struct {
	id         companion.ID
	generation uint64
	plan       companion.Plan
	err        error
}

// pathOutcome 是一次寻路的结果，同样携带任务身份。
type pathOutcome struct {
	id         companion.ID
	generation uint64
	result     companion.PathResult
	err        error
}

// taskEventFact 是一次状态机迁移产出的待发布事件事实：编排层补齐身份后
// 由 Server 转成 ChatEvent 广播。
type taskEventFact struct {
	issuer     companionTaskIssuer
	definition companion.Definition
	command    companion.TaskCommand
	event      companion.TaskEvent
}

// companionManager 编排全部伙伴的任务执行。零值不可用，经 newCompanionManager
// 构造；关闭顺序见 beginShutdown/close。
type companionManager struct {
	engine         *sim.Engine
	planner        companionPlanner
	timeoutMinutes int
	table          companion.PathBlockTable

	// onlinePlayers 返回 tick 边界的在线玩家事实（稳定 ID + 权威位置，已按
	// ID 升序去重归一），由 Server 在构造后注入——会话注册表归 Server 所有，
	// manager 只消费这一权威源。规划快照的 OnlinePlayers 填充与 follow 目标
	// 的在线性/位置解析共用它；nil 是防御缺省（视同无人在线）。调用方必须
	// 持有 stepMu（与 manager 其余状态同一单写者边界）。
	onlinePlayers func() []companion.PlanPlayer

	slots      map[companion.ID]*companionTaskSlot
	orderedIDs []companion.ID
	bodies     map[companion.ID]companion.Body
	// mining 缓存每个已激活伙伴在上一次权威 TickResult 中的采掘进度
	//（observeTickResult 回填），与 bodies 同属「上一 tick 末」的一致观察
	// 截面，mine 步骤执行器的完成与失败判定只读这一缓存。
	mining map[companion.ID]sim.MiningUpdate

	semaphore      chan struct{}
	plannerResults chan plannerOutcome
	pathResults    chan pathOutcome

	ctx       context.Context
	cancel    context.CancelFunc
	waitGroup sync.WaitGroup

	// events 是本 tick 累积的事件事实，takeEventFacts 排空后归 Server 发布。
	events []taskEventFact
}

// newCompanionManager 构造 Companion Manager。config 必须已含校验过的
// AIModel 与伙伴定义（NewHost 的第二道边界保证）。
func newCompanionManager(
	engine *sim.Engine,
	config Config,
	planner companionPlanner,
) *companionManager {
	ctx, cancel := context.WithCancel(context.Background())
	manager := &companionManager{
		engine:         engine,
		planner:        planner,
		timeoutMinutes: config.AIModel.TaskTimeout(),
		table:          companion.NewPathBlockTable(productionCompanionPassableBlocks()),
		slots:          make(map[companion.ID]*companionTaskSlot, len(config.Companions)),
		orderedIDs:     make([]companion.ID, 0, len(config.Companions)),
		bodies:         make(map[companion.ID]companion.Body, companion.MaxActive),
		mining:         make(map[companion.ID]sim.MiningUpdate, companion.MaxActive),
		semaphore:      make(chan struct{}, companion.MaxActive),
		plannerResults: make(chan plannerOutcome, companion.MaxActive),
		pathResults:    make(chan pathOutcome, companion.MaxActive),
		ctx:            ctx,
		cancel:         cancel,
	}
	for _, definition := range config.Companions {
		manager.slots[definition.ID] = &companionTaskSlot{definition: definition}
		manager.orderedIDs = append(manager.orderedIDs, definition.ID)
	}
	// orderedIDs 按字节序排序：每 tick 的事件产生顺序因此确定，EventID 分配
	// 在同一世界状态下可重放。
	slices.SortFunc(manager.orderedIDs, func(a, b companion.ID) int {
		return bytes.Compare(a[:], b[:])
	})
	return manager
}

// enqueueCommand 在 Accepted 分支把指令入队。返回 false 表示 FIFO 已满
// （QueueFull 同步拒绝）。issuer 由调用方在入队 tick 冻结。
func (m *companionManager) enqueueCommand(
	definition companion.Definition,
	command companion.TaskCommand,
	issuer companionTaskIssuer,
) bool {
	slot := m.slots[definition.ID]
	if slot == nil {
		// companionsByName 与 slots 同源构造，这里只是防御：未知槽位按接受
		// 处理但不入队，避免把配置缺陷伪装成队列满。
		slog.Error("任务入队找不到伙伴槽位", "companion", definition.ID)
		return true
	}
	if !slot.queue.Enqueue(command) {
		return false
	}
	slot.issuers = append(slot.issuers, issuer)
	return true
}

// stopCompanion 是停止旁路在 tick 边界的唯一入口（drainIncomingChats 调用，
// 持有 stepMu）。当前任务可停（Running 且计划最后一步是 follow）时转入
// Stopped 终态并累积 TaskStopped 广播事实（携带被停任务的原始指令与发令者
// 身份，复用 applyQueueEvents 的既有组装）；返回 false 表示不可停（非跟随
// 或空闲），由聊天层以 NotFollowing 只回发令者。移动清理沿用既有终态语义：
// 丢弃在途路径与重算计划，runner 不再为终态任务提交任何移动输入，伙伴在
// 权威物理作用下自然停下；在途寻路结果由既有世代/状态双重判定拦截。原队首
// 的推进不在这里特判——终态清槽后本 tick 的 dispatchPlanning 按 FIFO 语义
// 立即开始原队首，pending 不清空也不重排。
func (m *companionManager) stopCompanion(definition companion.Definition) bool {
	slot := m.slots[definition.ID]
	if slot == nil {
		// 与 enqueueCommand 的防御一致：配置缺陷按不可停处理并保留可诊断
		// 日志，绝不伪装成停止成功。
		slog.Error("停止指令找不到伙伴槽位", "companion", definition.ID)
		return false
	}
	events := slot.queue.Stop()
	if len(events) == 0 {
		return false
	}
	slot.path = nil
	slot.hasReplanAt = false
	m.applyQueueEvents(slot, events)
	return true
}

// captureIssuer 在入队 tick 冻结发令者事实：位置朝向来自权威玩家状态，
// 视线命中方块用与交互一致的确定性射线（≤交互距离）。玩家尚未出生时保留
// 有界缺省（有限坐标），指令本身仍然合法。
func (m *companionManager) captureIssuer(
	playerID core.PlayerID,
	name string,
	session sim.SessionID,
) companionTaskIssuer {
	issuer := companionTaskIssuer{
		playerID: playerID,
		name:     name,
		position: [3]float32{0, 1, 0},
	}
	player, ok := m.engine.Player(session)
	if !ok {
		return issuer
	}
	issuer.position = [3]float32(player.State.Position)
	issuer.yaw = player.Yaw
	issuer.pitch = player.Pitch
	issuer.lookHit, issuer.hasLookHit = m.issuerLookHit(player)
	return issuer
}

// issuerLookHit 用确定性 DDA 求发令者视线命中的第一个实心方块。射线只穿
// 发令者 3×3 兴趣内的已 ready 区块；未加载方块按未命中处理（快照只描述
// 确凿看见的世界）。
func (m *companionManager) issuerLookHit(player sim.PlayerUpdate) (core.BlockPos, bool) {
	view := m.chunkViewAt(player.Dimension, [3]float32(player.State.Position))
	origin := player.State.Position.Add(
		mgl32.Vec3{0, physics.ActiveTunables().EyeHeight, 0},
	)
	direction := sim.LookDirection(player.Yaw, player.Pitch)
	hit, ok, err := core.RaycastBlocks(
		origin,
		direction,
		sim.ActiveTunables().InteractionReach,
		func(position core.BlockPos) (bool, error) {
			block, ready := view.blockAt(position.X, position.Y, position.Z)
			if !ready {
				return false, nil
			}
			return block != core.AirID, nil
		},
	)
	if err != nil || !ok {
		return core.BlockPos{}, false
	}
	return hit.Block, true
}

// advanceCompanionTasks 是 tick 边界的编排入口，在聊天 drain 之后、
// engine.Step 之前调用（伙伴 action 必须先入 inbox 才能被本 tick 消费）。
// 返回本 tick 产生的任务事件投递。
func (server *Server) advanceCompanionTasks() []chatDelivery {
	manager := server.companionManager
	if manager == nil {
		return nil
	}
	manager.refreshBodies()
	manager.applyPlannerOutcomes()
	manager.applyPathOutcomes()
	manager.expireTasks()
	manager.advanceRunners()
	manager.dispatchPlanning()
	manager.dispatchPathRequests()
	return server.taskEventDeliveries(manager.takeEventFacts())
}

// refreshBodies 缓存本 tick 的伙伴身体快照，编排各阶段共用，避免重复拷贝。
func (m *companionManager) refreshBodies() {
	clear(m.bodies)
	for _, body := range m.engine.CompanionBodies() {
		m.bodies[body.ID] = body
	}
}

func (m *companionManager) body(id companion.ID) (companion.Body, bool) {
	body, ok := m.bodies[id]
	return body, ok
}

// takeEventFacts 排空本 tick 累积的事件事实。
func (m *companionManager) takeEventFacts() []taskEventFact {
	facts := m.events
	m.events = nil
	return facts
}

// applyQueueEvents 把状态机迁移产出的事件事实补上任务身份后累积。
// currentCommand/currentIssuer 在任务占据当前槽位的全程有效，终态清槽后
// 不再产生事件。
func (m *companionManager) applyQueueEvents(slot *companionTaskSlot, events []companion.TaskEvent) {
	for _, event := range events {
		m.events = append(m.events, taskEventFact{
			issuer:     slot.currentIssuer,
			definition: slot.definition,
			command:    slot.currentCommand,
			event:      event,
		})
	}
}

// applyPlannerOutcomes 在 tick 边界非阻塞排空规划结果并应用：世代或状态
// 不符的结果直接丢弃（任务已终态或已被替换）。
func (m *companionManager) applyPlannerOutcomes() {
	for {
		select {
		case outcome := <-m.plannerResults:
			m.applyPlannerOutcome(outcome)
		default:
			return
		}
	}
}

func (m *companionManager) applyPlannerOutcome(outcome plannerOutcome) {
	slot := m.slots[outcome.id]
	if slot == nil || !slot.planningInFlight {
		return
	}
	slot.planningInFlight = false
	if slot.queue.Generation() != outcome.generation {
		return
	}
	current, ok := slot.queue.Current()
	if !ok || current.State != companion.TaskPlanning {
		return
	}
	switch {
	case outcome.err == nil:
		m.applyQueueEvents(slot, slot.queue.AcceptPlan(outcome.plan))
		// 结构校验是纯值操作，同一 tick 完成校验并进入 Running；失败即
		// 以 InvalidPlan 终止，绝不改写或降级模型计划。
		m.applyQueueEvents(slot, slot.queue.FinishValidation(
			m.engine.WorldTime(), m.timeoutMinutes,
		))
		current, ok = slot.queue.Current()
		if !ok || current.State != companion.TaskRunning {
			return
		}
		// 进入 Running 后立即请求第一步的路径（寻路 worker 异步执行）。
		if body, active := m.body(outcome.id); active {
			m.submitPathRequest(slot, outcome.id, body, current)
		}
	case errors.Is(outcome.err, companion.ErrPlannerInvalidPlan):
		m.applyQueueEvents(slot, slot.queue.FailPlanning(companion.TaskFailInvalidPlan))
	default:
		m.applyQueueEvents(slot, slot.queue.FailPlanning(companion.TaskFailPlannerUnavailable))
	}
}

// applyPathOutcomes 在 tick 边界非阻塞排空寻路结果并应用。
func (m *companionManager) applyPathOutcomes() {
	for {
		select {
		case outcome := <-m.pathResults:
			m.applyPathOutcome(outcome)
		default:
			return
		}
	}
}

func (m *companionManager) applyPathOutcome(outcome pathOutcome) {
	slot := m.slots[outcome.id]
	if slot == nil || !slot.pathInFlight {
		return
	}
	slot.pathInFlight = false
	if slot.queue.Generation() != outcome.generation {
		return
	}
	current, ok := slot.queue.Current()
	if !ok || current.State != companion.TaskRunning {
		return
	}
	if outcome.err != nil {
		// 重算失败计入三连失败；未达上限按固定冷却重试，绝不无限重算。
		if slot.policy.RecordFailure() {
			slot.path = nil
			slot.hasReplanAt = false
			m.applyQueueEvents(slot, slot.queue.FailRun(companion.TaskFailPathUnreachable))
			return
		}
		slot.path = nil
		slot.replanAtTick = slot.policy.ReplanAfter(m.engine.TickCount())
		slot.hasReplanAt = true
		return
	}
	result := outcome.result
	slot.path = &result
	slot.waypoint = 0
	slot.hasReplanAt = false
}

// expireTasks 用权威世界时间检查 Running 任务的 deadline。到期转 TimedOut，
// 移动随当前任务一起停止（runner 不再为其提交任何输入）。
func (m *companionManager) expireTasks() {
	worldTime := m.engine.WorldTime()
	for _, id := range m.orderedIDs {
		slot := m.slots[id]
		current, ok := slot.queue.Current()
		if !ok || current.State != companion.TaskRunning {
			continue
		}
		events := slot.queue.Expire(worldTime)
		if len(events) == 0 {
			continue
		}
		slot.path = nil
		slot.hasReplanAt = false
		m.applyQueueEvents(slot, events)
	}
}

// advanceRunners 推进全部 Running 任务的执行：重验路径 revision、消费已到达
// 的路径点并提交至多一个移动输入。follow 步骤先经 advanceFollowRunner 的
// 在线性与距离裁决——持续跟随没有「步骤完成」语义，路径走尽或目标漂移只
// 触发按既有冷却的重算，绝不推进步骤索引；mine/place 步骤交给
// advanceInteractionRunner（走近复用同一移动语义，走尽后转入采掘按住/放置
// 提交）。全部执行器跑完后由 releaseFinishedMining 兜底释放已离开采掘
// 步骤但仍按住的采掘意图。
func (m *companionManager) advanceRunners() {
	for _, id := range m.orderedIDs {
		slot := m.slots[id]
		current, ok := slot.queue.Current()
		if !ok || current.State != companion.TaskRunning {
			continue
		}
		// follow 步骤每 tick 先做在线性与距离裁决；返回 false 表示任务已
		// 终态（目标离线），本 tick 不再有可执行内容。
		follow := followStepOf(current)
		if follow != nil && !m.advanceFollowRunner(slot, id, *follow) {
			continue
		}
		// mine/place 步骤：走近与交互的专用执行器，不复用下方 go_to 的
		// 路径走尽即完成语义。
		if step := interactionStepOf(current); step != nil {
			m.advanceInteractionRunner(slot, id, current, *step)
			continue
		}
		if slot.path == nil {
			// follow 距离内（或路径尚未就绪）：不提交任何移动输入，伙伴在
			// 权威物理作用下保持原地；普通任务缺少路径同样等待派发。
			continue
		}
		body, active := m.body(id)
		if !active {
			continue
		}
		if m.advancePathMovement(slot, id, body) && follow == nil {
			m.applyQueueEvents(slot, slot.queue.CompleteStep())
		}
		// follow：目标仍在距离外时路径才会走尽——清空路径等待
		// dispatchPathRequests 按当前目标重算；绝不 CompleteStep（持续
		// 语义，步骤索引不推进）。
	}
	m.releaseFinishedMining()
}

// advancePathMovement 是 go_to 与 mine/place 走近段共用的路径执行体：重验
// 路径 revision、消费已到达的路径点并提交至多一个移动输入。返回 true 表示
// 路径已走尽（调用方据此决定完成步骤或转入交互段）；路径失效时丢弃并按
// 固定冷却安排重算，返回 false。
func (m *companionManager) advancePathMovement(
	slot *companionTaskSlot,
	id companion.ID,
	body companion.Body,
) bool {
	// 路径点提交前重验：结果携带的每个区块 revision 都必须与当前权威
	// 状态一致，失效即丢弃路径并按固定冷却重算。
	if !slot.policy.ShouldUse(*slot.path, slot.waypoint, m.windowRevisions(body)) {
		slot.path = nil
		slot.replanAtTick = slot.policy.ReplanAfter(m.engine.TickCount())
		slot.hasReplanAt = true
		return false
	}
	// 到达检查先于提交输入：路径点 0（起点）在首个 tick 即被消费。
	for slot.waypoint < len(slot.path.Waypoints) {
		if !arrivedAtWaypoint(body.Position, slot.path.Waypoints[slot.waypoint]) {
			break
		}
		slot.waypoint++
		slot.policy.RecordSuccess()
	}
	if slot.waypoint >= len(slot.path.Waypoints) {
		slot.path = nil
		slot.hasReplanAt = false
		return true
	}
	m.engine.EnqueueCompanionAction(sim.CompanionAction{
		ID:   id,
		Kind: sim.CompanionActionMove,
		Input: movementInputToward(
			body.Position, slot.path.Waypoints[slot.waypoint]),
	})
	return false
}

// companionFollowReplanDriftBlocks 是 follow 动态终点的重算漂移阈值（水平
// 格距）：目标玩家的站立格相对上次寻路终点移动超过该距离才丢弃既有路径
// 重算。取 2（跟随距离的一半）的权衡：阈值 ≤1 时玩家每走一两格就触发重算，
// 寻路 worker 被位移噪声打满、既有冷却机制形同虚设；阈值 ≥ 跟随距离 4 时
// 旧路径的尾段已明显落后目标，伙伴要先走完过时路径才更新方向，跟随滞后
// 可感。2 格让重算频率由真实位移驱动（约每 2 格一次），旧路径仍保有朝向
// 目标的可用前缀；世界变化造成的路径失效继续由既有 revision 重验承担，
// 与漂移判定互不重叠。
const companionFollowReplanDriftBlocks = 2

// companionFollowDistanceSquared 是跟随距离边界的平方。常量的价值在命名与
// 语义单一来源（跟随距离平方只有一个定义点）；两次乘法编译期即可折叠，无
// 性能含义。
const companionFollowDistanceSquared = companion.CompanionFollowDistanceBlocks *
	companion.CompanionFollowDistanceBlocks

// followStepOf 返回任务当前执行步骤的 follow 形态；非 follow 步骤（或步骤
// 索引越界的防御情形）返回 nil。
func followStepOf(task companion.Task) *companion.PlanStep {
	if task.StepIndex < 0 || task.StepIndex >= len(task.Plan.Steps) {
		return nil
	}
	step := &task.Plan.Steps[task.StepIndex]
	if step.Kind != companion.PlanStepFollow {
		return nil
	}
	return step
}

// advanceFollowRunner 是持续跟随步骤的每 tick 裁决：先读目标在线性——离线
// 立即以 TaskFailWorldChanged 失败并广播（FIFO 随终态在下一 tick 推进），
// 新任务与恢复的 Running follow 共用这一先验，天然满足「恢复任务在下一
// 动作前先验在线性」；在线则按水平距离裁决：距离内清空路径与重算意图并
// 保持原地（不提交移动输入，物理照常），距离外以目标站立格为动态终点，
// 终点漂移超出阈值时丢弃既有路径交由 dispatchPathRequests 重算。返回
// false 表示任务已进入终态。
func (m *companionManager) advanceFollowRunner(
	slot *companionTaskSlot,
	id companion.ID,
	step companion.PlanStep,
) bool {
	target, online := m.followTarget(step.PlayerID)
	if !online {
		slot.path = nil
		slot.hasReplanAt = false
		slot.hasFollowGoal = false
		m.applyQueueEvents(slot, slot.queue.FailRun(companion.TaskFailWorldChanged))
		return false
	}
	body, active := m.body(id)
	if !active {
		// 身体尚未激活（出生扫描在途）：不裁决距离也不提交输入，等下一 tick。
		return true
	}
	if withinFollowDistance(body.Position, target.Position) {
		// 距离边界内：停止提交移动输入。清空既有路径与重算意图，防止
		// dispatchPathRequests 在距离内反复发起寻路；已派发的在途寻路结果
		// 落地后会被下一 tick 的本分支再次清空，最坏多一次寻路。
		slot.path = nil
		slot.hasReplanAt = false
		slot.hasFollowGoal = false
		return true
	}
	if slot.path != nil && slot.hasFollowGoal && followGoalDrifted(slot.followGoal, target.Position) {
		// 目标漂移超出重算阈值：旧路径指向过时终点。清空路径令
		// dispatchPathRequests 以当前目标重算；此刻必然没有失败冷却在身
		//（冷却仅在 path 为 nil 时存在），重算立即发起。
		slot.path = nil
		slot.hasReplanAt = false
		slot.hasFollowGoal = false
	}
	return true
}

// followTarget 从在线玩家集合解析 follow 目标的当前位置事实；找不到目标
// （从未登录或已断开）即离线。这是持续跟随在线性判定的唯一权威源。
func (m *companionManager) followTarget(playerID core.PlayerID) (companion.PlanPlayer, bool) {
	if m.onlinePlayers == nil {
		return companion.PlanPlayer{}, false
	}
	for _, player := range m.onlinePlayers() {
		if player.ID == playerID {
			return player, true
		}
	}
	return companion.PlanPlayer{}, false
}

// withinFollowDistance 报告伙伴与目标玩家的水平距离是否落在跟随距离内。
// 只用水平分量：垂直分离（跳跃/地形高差）由重力与寻路的 Y 语义处理，不
// 参与停止判定，避免伙伴位于玩家头顶/脚下时因垂直差被误判为距离外而抖动。
func withinFollowDistance(from, to [3]float32) bool {
	dx := from[0] - to[0]
	dz := from[2] - to[2]
	return dx*dx+dz*dz <= companionFollowDistanceSquared
}

// followGoalDrifted 报告目标当前位置相对上次寻路终点的水平漂移是否超出
// 重算阈值（平方比较避免开方）。基准取方块中心（+0.5）与站立格语义一致。
func followGoalDrifted(goal companion.PathCell, targetPos [3]float32) bool {
	dx := float32(goal.X) + 0.5 - targetPos[0]
	dz := float32(goal.Z) + 0.5 - targetPos[2]
	const limit = companionFollowReplanDriftBlocks * companionFollowReplanDriftBlocks
	return dx*dx+dz*dz > limit
}

// standingCellOf 把一个位置归一为它占用的站立格（feet 格）：X/Z 取 floor、
// Y 取 floor，与寻路网格的方块中心基准一致。玩家脚下格天然满足寻路端点的
// 站立约束（feet/head 可通过、正下方支撑）；个别悬空/嵌墙瞬间由寻路失败
// 与既有冷却重试语义兜底，不在此特判。
func standingCellOf(position [3]float32) companion.PathCell {
	return companion.PathCell{
		X: int32(math.Floor(float64(position[0]))),
		Y: int32(math.Floor(float64(position[1]))),
		Z: int32(math.Floor(float64(position[2]))),
	}
}

// dispatchPlanning 为每个空闲槽位派发规划：取队首、获取并发名额、迁移
// Planning 后构造快照，再由 worker 发起模型请求。信号量满或伙伴未激活时
// 任务保持 Queued 顺延；快照构造失败时任务以 PlannerUnavailable 真实终
// 结（见函数内注释），FIFO 在下一 tick 推进。
func (m *companionManager) dispatchPlanning() {
	for _, id := range m.orderedIDs {
		slot := m.slots[id]
		if slot.planningInFlight {
			// 每伙伴最多一个在途规划请求：在途期间绝不发起第二个。
			continue
		}
		current, hasCurrent := slot.queue.Current()
		if hasCurrent && current.State != companion.TaskQueued {
			continue
		}
		if !hasCurrent {
			if !slot.queue.BeginHead() {
				continue
			}
			// 新任务从零开始计预算：三连失败上限约束「同一任务内」的连续
			// 重算失败（pathfinding spec），前一个任务遗留的计数（含已耗尽
			// 到 3 的终态计数）不得泄漏进新任务。交互状态同理归零——上一
			// 任务的就绪标记与采掘进度记忆不属于新任务。
			slot.policy = companion.PathPolicy{}
			slot.resetInteraction()
			if len(slot.issuers) == 0 {
				slog.Error("任务 FIFO 与发令者队列失配", "companion", id)
				continue
			}
			slot.currentIssuer, slot.issuers = slot.issuers[0], slot.issuers[1:]
			current, _ = slot.queue.Current()
		}
		slot.currentCommand = current.Command
		body, active := m.body(id)
		if !active {
			// 伙伴尚未激活（出生扫描在途）：任务保持 Queued，等下一 tick。
			continue
		}
		select {
		case m.semaphore <- struct{}{}:
		default:
			// 全服四个并发名额已满：任务保持 Queued，下一 tick 重试。
			continue
		}
		if !slot.queue.BeginPlanning() {
			<-m.semaphore
			continue
		}
		// 快照构造放在 BeginPlanning 成功之后：构造失败时任务已真实处于
		// Planning 态，FailPlanning 能令其进入终态并清出当前槽位（下一
		// tick 的 BeginHead 推进 FIFO），而不是在 Queued 态上被守卫拒绝、
		// 每 tick 原地重试。快照是纯值操作、不发起模型请求，失败路径只需
		// 归还刚占用的并发名额。
		snapshot, err := m.buildPlanSnapshot(slot.definition, current.Command, slot.currentIssuer, body)
		if err != nil {
			// 快照构造失败是服务端缺陷：令任务失败并保留可诊断日志，
			// 绝不让队列悬挂。
			slog.Error("构造规划快照失败", "companion", id, "error", err)
			m.applyQueueEvents(slot, slot.queue.FailPlanning(companion.TaskFailPlannerUnavailable))
			<-m.semaphore
			continue
		}
		slot.planningInFlight = true
		m.waitGroup.Add(1)
		go m.plannerWorker(id, slot.queue.Generation(), snapshot)
	}
}

// plannerWorker 在 worker goroutine 上调用模型：只读不可变快照，结果经有界
// channel 回 tick 边界；ctx 取消（关服）时放弃结果并释放并发名额。
func (m *companionManager) plannerWorker(
	id companion.ID,
	generation uint64,
	snapshot companion.PlanSnapshot,
) {
	defer m.waitGroup.Done()
	defer func() { <-m.semaphore }()
	plan, err := m.planner.Plan(m.ctx, snapshot)
	outcome := plannerOutcome{id: id, generation: generation, plan: plan, err: err}
	select {
	case m.plannerResults <- outcome:
	case <-m.ctx.Done():
	}
}

// dispatchPathRequests 为缺少路径的 Running 任务发起寻路请求：首次请求立即
// 发起；失效重算受固定冷却约束。
func (m *companionManager) dispatchPathRequests() {
	tick := m.engine.TickCount()
	for _, id := range m.orderedIDs {
		slot := m.slots[id]
		if slot.pathInFlight || slot.path != nil {
			continue
		}
		current, ok := slot.queue.Current()
		if !ok || current.State != companion.TaskRunning {
			continue
		}
		// mine/place 的交互阶段不再寻路：走近已结束，重算只会把伙伴拉离
		// 交互位置（采掘的零进度信号与放置的距离先验都以当前位置为基准）。
		if slot.interactionPhaseActive(current) {
			continue
		}
		if slot.hasReplanAt && tick < slot.replanAtTick {
			continue
		}
		body, active := m.body(id)
		if !active {
			continue
		}
		m.submitPathRequest(slot, id, body, current)
	}
}

// submitPathRequest 在 tick 边界构造不可变网格并交给 worker 执行整数 A*。
// go_to 步骤以步骤坐标为固定终点；mine/place 步骤的目标是交互对象本身
// （实心方块/待填充空气格），终点改取目标的相邻站立格（interactionGoal）；
// follow 步骤的终点是动态的——每次派发都以目标玩家的当前站立格为准（目标
// 离线或已在跟随距离内时不发起，交由 advanceFollowRunner 的每 tick 先验
// 裁决）。窗口区块未就绪时返回 false（下一 tick 重试，不计失败）。
func (m *companionManager) submitPathRequest(
	slot *companionTaskSlot,
	id companion.ID,
	body companion.Body,
	current companion.Task,
) {
	if slot.pathInFlight {
		return
	}
	center := companion.PathCell{
		X: int32(math.Floor(float64(body.Position[0]))),
		Y: int32(math.Floor(float64(body.Position[1]))),
		Z: int32(math.Floor(float64(body.Position[2]))),
	}
	if current.StepIndex >= len(current.Plan.Steps) {
		return
	}
	step := current.Plan.Steps[current.StepIndex]
	goal := companion.PathCell{X: step.X, Y: step.Y, Z: step.Z}
	if step.Kind == companion.PlanStepMine || step.Kind == companion.PlanStepPlace {
		goal = m.interactionGoal(body, step)
	}
	if step.Kind == companion.PlanStepFollow {
		// 先解析动态终点再构造网格：距离内/离线时避免一次无谓的区块深拷贝。
		target, online := m.followTarget(step.PlayerID)
		if !online {
			// 目标离线：寻路无从发起。失败裁决由 advanceRunners 的在线性
			// 先验统一产生（每 tick 必达），这里只静默跳过。
			return
		}
		if withinFollowDistance(body.Position, target.Position) {
			// 距离内无需路径：advanceFollowRunner 的距离分支先行裁决，
			// 这里只防御「本 tick 内目标恰好走进边界」的窗口。
			return
		}
		goal = standingCellOf(target.Position)
		slot.followGoal = goal
		slot.hasFollowGoal = true
	}
	grid, ok := m.buildPathGrid(body, companion.PathWindow{Center: center})
	if !ok {
		return
	}
	slot.pathInFlight = true
	m.waitGroup.Add(1)
	go m.pathWorker(id, slot.queue.Generation(), grid, center, goal)
}

// pathWorker 在 worker goroutine 上执行确定性寻路并把结果回送 tick 边界。
func (m *companionManager) pathWorker(
	id companion.ID,
	generation uint64,
	grid companion.PathGrid,
	start, goal companion.PathCell,
) {
	defer m.waitGroup.Done()
	result, err := companion.FindPath(grid, start, goal)
	select {
	case m.pathResults <- pathOutcome{id: id, generation: generation, result: result, err: err}:
	case <-m.ctx.Done():
	}
}

// taskStates 返回有任务内容的伙伴的任务域观察输入，经 companionPersistence.
// Observe 参与 dirty 判定并随保存载荷落盘。空闲队列（无当前任务且
// FIFO 为空）没有可持久化的任务事实，跳过它避免「首次观察到空队列」被误判
// 为任务状态变化而触发无意义的存档。
func (m *companionManager) taskStates() []companion.TaskQueueState {
	states := make([]companion.TaskQueueState, 0, len(m.orderedIDs))
	for _, id := range m.orderedIDs {
		slot := m.slots[id]
		if slot.queue.Len() == 0 {
			if _, hasCurrent := slot.queue.Current(); !hasCurrent {
				continue
			}
		}
		state := slot.queue.Snapshot()
		state.ID = id
		states = append(states, state)
	}
	return states
}

// restoredIssuerIdentity 是恢复任务的合成发令者事实：指令的真实发令者
// （玩家 ID/名称/位置）不落盘，重启后无法回溯；任务事件又必须携带合法
// 玩家身份才能通过 ChatEvent.Validate 发布，因此使用固定的「未知发令者」
// 身份。位置沿用 captureIssuer 的有界缺省。
var restoredIssuerIdentity = companionTaskIssuer{
	playerID: core.PlayerID{0, 0, 0, 0, 0, 0, 0x40, 0, 0x80, 0, 0, 0, 0, 0, 0, 0},
	name:     "未知发令者",
	position: [3]float32{0, 1, 0},
}

// restoreQueues 把启动加载的任务域载荷恢复进对应槽位（newWorld 在构造
// Manager 后调用一次）。未配置（inactive）的伙伴没有槽位，任务事实不
// 恢复——配置移除的伙伴不参与编排，存档中的 inactive 记录仍只保留身体。
func (m *companionManager) restoreQueues(queues []storage.StoredCompanionQueue) {
	for _, queue := range queues {
		slot := m.slots[queue.ID]
		if slot == nil {
			continue
		}
		m.restoreQueue(slot, queue)
	}
}

// restoreQueue 恢复单个槽位的任务域：当前任务与 FIFO 指令按存档顺序回填。
// 归一纪律（恢复侧）：Planning/Validating 按 Queued 恢复并保留原始指令，
// 重启后重新发起规划；Running 保留步骤索引与 deadline，但路径绝不落盘，
// 恢复后 slot.path 为 nil——首个动作前必须经 dispatchPathRequests 按当前
// 权威世界重算，天然满足「恢复任务在下一动作前重验」的规格约束。
func (m *companionManager) restoreQueue(slot *companionTaskSlot, queue storage.StoredCompanionQueue) {
	if queue.HasCurrent {
		task := companion.Task{
			Command:       companion.TaskCommand(queue.Current.Command),
			Plan:          companion.Plan{Steps: queue.Current.PlanSteps},
			StepIndex:     queue.Current.StepIndex,
			State:         queue.Current.State,
			StartTick:     queue.Current.StartTick,
			DeadlineTicks: queue.Current.DeadlineTicks,
		}
		if task.State == companion.TaskPlanning || task.State == companion.TaskValidating {
			task.State = companion.TaskQueued
			task.Plan = companion.Plan{}
			task.StepIndex = 0
			task.StartTick = 0
			task.DeadlineTicks = 0
		}
		if slot.queue.RestoreCurrent(task) {
			slot.currentIssuer = restoredIssuerIdentity
			slot.currentCommand = task.Command
			// 恢复的任务同样从零开始计预算：槽位此刻是新建零值，这里按
			// 「预算属于任务」的同一不变量补一次显式归零，防止未来的恢复
			// 时机变化把上一段运行期的计数带入恢复任务。交互状态同样按
			// 任务边界归零（恢复的 Running mine/place 任务从走近段重新开始）。
			slot.policy = companion.PathPolicy{}
			slot.resetInteraction()
		}
	}
	for _, command := range queue.Pending {
		if slot.queue.Enqueue(companion.TaskCommand(command)) {
			slot.issuers = append(slot.issuers, restoredIssuerIdentity)
		}
	}
}

// beginShutdown 进入关服序列：取消在途模型请求。调用点（Server.Shutdown
// 冻结段）必须已经停止接受 ChatCommand 且先于最终 AI 保存；此后 tick 编排
// 不再运行，队列与 actor 状态随生命周期冻结保持一致。
func (m *companionManager) beginShutdown() {
	m.cancel()
}

// close 等待全部 worker 退出。结果 channel 中未被 drain 的结果直接放弃——
// 冻结后的任务状态已由 Observe 捕获，重启恢复语义由任务 7 落地。
func (m *companionManager) close() {
	m.cancel()
	m.waitGroup.Wait()
}

// companionManagerTaskStates 是 Observe 调用的空值安全包装。
func (server *Server) companionManagerTaskStates() []companion.TaskQueueState {
	if server.companionManager == nil {
		return nil
	}
	return server.companionManager.taskStates()
}

// taskEventDeliveries 把事件事实转成可发布的 ChatEvent 投递。任务事件全部
// 广播（recipient 0）；EventID 沿用聊天事件的同一权威计数器，保持全服严格
// 递增。构造出的非法事件（服务端缺陷）跳过并记录，绝不发布半成品。
func (server *Server) taskEventDeliveries(facts []taskEventFact) []chatDelivery {
	if len(facts) == 0 {
		return nil
	}
	deliveries := make([]chatDelivery, 0, len(facts))
	for _, fact := range facts {
		if server.nextChatEventID == ^uint64(0) {
			slog.Error("chat event ID 耗尽，丢弃任务事件",
				"companion", fact.definition.ID)
			continue
		}
		server.nextChatEventID++
		event := network.ChatEvent{
			EventID:       server.nextChatEventID,
			PlayerID:      fact.issuer.playerID,
			PlayerName:    fact.issuer.name,
			CompanionID:   fact.definition.ID,
			CompanionName: fact.definition.Name,
			Kind:          taskEventKind(fact.event.Kind),
			RejectReason:  taskEventRejectReason(fact.event),
			Command:       string(fact.command),
		}
		if err := event.Validate(); err != nil {
			slog.Error("任务事件非法", "companion", fact.definition.ID, "error", err)
			continue
		}
		deliveries = append(deliveries, chatDelivery{event: event})
	}
	return deliveries
}

// taskEventKind 把任务域事件类别映射为协议事件枚举。
func taskEventKind(kind companion.TaskEventKind) network.ChatEventKind {
	switch kind {
	case companion.TaskEventStarted:
		return network.ChatEventTaskStarted
	case companion.TaskEventProgress:
		return network.ChatEventTaskProgress
	case companion.TaskEventCompleted:
		return network.ChatEventTaskCompleted
	case companion.TaskEventFailed:
		return network.ChatEventTaskFailed
	case companion.TaskEventTimedOut:
		return network.ChatEventTaskTimedOut
	case companion.TaskEventStopped:
		return network.ChatEventTaskStopped
	default:
		return network.ChatEventKind(0)
	}
}

// taskEventRejectReason 把失败原因映射到 ChatEvent 的 reason 槽位（16..19 的
// wire 枚举）；非失败事件保持 None。
func taskEventRejectReason(event companion.TaskEvent) network.ChatRejectReason {
	if event.Kind != companion.TaskEventFailed {
		return network.ChatRejectNone
	}
	switch event.Reason {
	case companion.TaskFailPlannerUnavailable:
		return network.ChatRejectReason(network.TaskFailPlannerUnavailable)
	case companion.TaskFailInvalidPlan:
		return network.ChatRejectReason(network.TaskFailInvalidPlan)
	case companion.TaskFailPathUnreachable:
		return network.ChatRejectReason(network.TaskFailPathUnreachable)
	case companion.TaskFailWorldChanged:
		return network.ChatRejectReason(network.TaskFailWorldChanged)
	case companion.TaskFailInventoryFull:
		return network.ChatRejectReason(network.TaskFailInventoryFull)
	default:
		return network.ChatRejectNone
	}
}

// waypointArrivalRadiusSquared 是路径点到达阈值的平方：0.35 格半径。阈值
// 小于半格（0.5），保证伙伴不会在相邻路径点之间“抄近路”提前跳格；停止
// 语义由输入撤销后的地面减速保证，无需额外的输入死区。
const waypointArrivalRadiusSquared = float32(0.35 * 0.35)

// arrivedAtWaypoint 报告伙伴水平位置是否到达路径点（方块中心 ±0.35 格）。
// 只用水平距离：go_to 的垂直分量由跳跃/下落的物理语义保证。
func arrivedAtWaypoint(position [3]float32, cell companion.PathCell) bool {
	dx := position[0] - (float32(cell.X) + 0.5)
	dz := position[2] - (float32(cell.Z) + 0.5)
	return dx*dx+dz*dz <= waypointArrivalRadiusSquared
}

// movementInputToward 构造朝路径点移动的规范输入：yaw 朝向目标（yaw=0 面
// -Z，故 yaw=atan2(-dx,-dz)）、MoveZ=+1 沿视线前进；目标路径点高于当前脚
// 下一格及以上时按住 Jump——StepHeight（0.6）不足以登上整格台阶，跳上一格
// 与跨一格间隙都由权威物理裁决。每 tick 每伙伴最多提交这一个输入，实际
// 位移永远由权威物理决定；寻路结果与物理的分歧以物理为准（伙伴贴墙等待
// 重算或超时，绝不改写世界）。
func movementInputToward(position [3]float32, target companion.PathCell) physics.Input {
	dx := float32(target.X) + 0.5 - position[0]
	dz := float32(target.Z) + 0.5 - position[2]
	input := physics.Input{MoveZ: 1, Yaw: float32(math.Atan2(-float64(dx), -float64(dz)))}
	feetY := int32(math.Floor(float64(position[1])))
	if target.Y > feetY {
		input.Jump = true
	}
	return input
}
