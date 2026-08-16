// 本文件定义任务域的纯值类型：有界指令文本、六态任务状态、稳定失败原因、
// 事件事实与任务值。全部类型不含锁、goroutine 或 I/O——权威 tick 是唯一写者，
// 并发串行化由 server 侧 Companion Manager 负责（见变更 design.md 的归属裁决）。
package companion

// MaxTaskQueueDepth 是每伙伴 FIFO 的待执行指令容量（spec：16 条）。
// 容量只约束 pending 深度；当前执行中的任务独占一个槽位，不计入本上限。
const MaxTaskQueueDepth = 16

// TicksPerMinute 是任务超时分钟数换算为世界时间 tick 的系数：权威 tick 固定
// 50ms（20 tps），一分钟恰好 1200 tick。deadline 全程使用 WorldTimeTicks，
// 关服停摆期间世界时间不推进，因此不会消耗执行时长。
const TicksPerMinute = 1200

// TaskCommand 是一条已通过聊天寻址校验的玩家原始指令文本（不含 @伙伴名 前缀）。
// 与网络聊天指令共用 1,024 字节上限：drain 边界已经过 network 的校验，这里的
// 重复校验是防御性的——直接构造 TaskQueue 的调用方（测试、未来的恢复路径）
// 也被同一上界约束。
type TaskCommand string

// Validate 校验指令文本：合法 UTF-8、无控制字符、非空白且不超过
// MaxPlanCommandBytes 字节。约束与快照指令字段完全一致，保证入队指令总能
// 进入 PlanSnapshot.Command。
func (c TaskCommand) Validate() error {
	return validatePlanText("任务指令", string(c), MaxPlanCommandBytes, true)
}

// TaskState 是任务生命周期的六态枚举，推进方向固定为
// Queued → Planning → Validating → Running → Completed/Failed/TimedOut。
// Failed 可从 Planning/Validating/Running 进入（模型失败、非法计划与执行失败
// 各自发生在不同阶段）；Completed 与 TimedOut 只能从 Running 进入。
type TaskState uint8

const (
	// TaskQueued 表示指令在 FIFO 中等待成为当前任务。
	TaskQueued TaskState = iota + 1
	// TaskPlanning 表示规划请求在途，等待模型结果。
	TaskPlanning
	// TaskValidating 表示计划已到达，正在做结构校验与快照约束校验。
	TaskValidating
	// TaskRunning 表示计划已接受，Runner 正在按路径点提交移动输入。
	TaskRunning
	// TaskCompleted 是终态：全部计划步骤完成。
	TaskCompleted
	// TaskFailed 是终态：携带 TaskFailReason 的稳定失败原因。
	TaskFailed
	// TaskTimedOut 是终态：世界时间越过 deadline。
	TaskTimedOut
)

// Terminal 报告状态是否为终态。终态任务永远离开 FIFO 的当前槽位。
func (s TaskState) Terminal() bool {
	return s == TaskCompleted || s == TaskFailed || s == TaskTimedOut
}

// String 返回状态的中文短名，供日志与快照任务摘要使用。
func (s TaskState) String() string {
	switch s {
	case TaskQueued:
		return "待执行"
	case TaskPlanning:
		return "规划中"
	case TaskValidating:
		return "校验中"
	case TaskRunning:
		return "执行中"
	case TaskCompleted:
		return "已完成"
	case TaskFailed:
		return "失败"
	case TaskTimedOut:
		return "已超时"
	default:
		return "未知状态"
	}
}

// TaskFailReason 是任务失败的稳定服务端原因枚举（语义域）。server 侧把它映射
// 为 network.TaskFailReason 的 wire 枚举（16..19）；两个枚举刻意分离——本包
// 不依赖 network，wire 编号属于协议层。
type TaskFailReason uint8

const (
	// TaskFailNone 表示任务未失败（零值）。
	TaskFailNone TaskFailReason = iota
	// TaskFailPlannerUnavailable 表示传输层失败：HTTP 错误、非 2xx、超时、
	// 取消或响应超限。
	TaskFailPlannerUnavailable
	// TaskFailInvalidPlan 表示模型输出不符合受限计划 schema（含非 go_to
	// 步骤、非法坐标或不规范文本）。
	TaskFailInvalidPlan
	// TaskFailPathUnreachable 表示路径三连失败或目标不可达。
	TaskFailPathUnreachable
	// TaskFailWorldChanged 是预留失败原因：M5B 全仓没有任何代码路径产生它。
	// 恢复任务的路径重验天然走重算与三连失败语义（终局为
	// TaskFailPathUnreachable），不存在单独的「恢复后重验失败」判定。保留
	// 枚举是为了失败原因分类学的稳定与 network wire 枚举（16..19）的一一
	// 对齐；若 M5C 引入更细粒度的恢复重验语义（例如与计划落盘时的方块
	// 快照比对），可在此落地而不破坏协议编号。
	TaskFailWorldChanged
)

// String 返回失败原因的中文短名。
func (r TaskFailReason) String() string {
	switch r {
	case TaskFailNone:
		return "未失败"
	case TaskFailPlannerUnavailable:
		return "模型不可用"
	case TaskFailInvalidPlan:
		return "非法计划"
	case TaskFailPathUnreachable:
		return "路径不可达"
	case TaskFailWorldChanged:
		return "世界已变化"
	default:
		return "未知原因"
	}
}

// TaskEventKind 是状态机迁移产出的待发布事件事实类别。Planning/Validating
// 迁移不产生公开事件（它们是服务端内部阶段）；进入 Running、步骤完成与终态
// 各产生一条广播事件，与 network.ChatEventKind 的任务枚举一一对应。
type TaskEventKind uint8

const (
	// TaskEventNone 表示无事件（零值）。
	TaskEventNone TaskEventKind = iota
	// TaskEventStarted 对应进入 Running 的 TaskStarted 广播。
	TaskEventStarted
	// TaskEventProgress 对应一个计划步骤完成的 TaskProgress 广播。
	TaskEventProgress
	// TaskEventCompleted 对应全部步骤完成的 TaskCompleted 广播。
	TaskEventCompleted
	// TaskEventFailed 对应任务失败的 TaskFailed 广播（携带原因）。
	TaskEventFailed
	// TaskEventTimedOut 对应世界时间超时的 TaskTimedOut 广播。
	TaskEventTimedOut
)

// TaskEvent 是一次迁移产出的待发布事件事实。事实只描述类别与原因；身份
// （伙伴、发令者、原始指令）由持有队列的编排层补充成完整 ChatEvent。
type TaskEvent struct {
	Kind   TaskEventKind
	Reason TaskFailReason
}

// Task 是一个任务的完整值状态。Generation 在任务成为当前任务时由队列盖戳，
// 用于丢弃过时的 worker 结果；Plan 在 Validating 之后有效；DeadlineTicks
// 只在 Running 及之后有效（进入 Running 时 = WorldTimeTicks + 超时分钟数）。
type Task struct {
	Generation    uint64
	Command       TaskCommand
	Plan          Plan
	StepIndex     int
	State         TaskState
	StartTick     uint64
	DeadlineTicks uint64
	FailReason    TaskFailReason
}

// Expired 报告世界时间是否已到达或越过本任务的 deadline。比较只依赖传入的
// WorldTimeTicks——权威世界时间在服务端停止运行期间不推进，因此持久化与关服
// 停摆天然不消耗执行时长（任务 7 依赖这一语义恢复任务）。
func (t Task) Expired(worldTimeTicks uint64) bool {
	return worldTimeTicks >= t.DeadlineTicks
}

// TaskDeadlineTicks 把进入 Running 时刻的世界时间与超时分钟数换算为 deadline。
// 分钟数必须已经过 ModelSettings 的 1..60 校验（或使用 TaskTimeout 缺省）。
func TaskDeadlineTicks(worldTimeTicks uint64, timeoutMinutes int) uint64 {
	if timeoutMinutes < 1 {
		timeoutMinutes = TaskTimeoutDefaultMinutes
	}
	return worldTimeTicks + uint64(timeoutMinutes)*TicksPerMinute
}
