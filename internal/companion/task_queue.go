// 本文件实现每伙伴任务 FIFO 与任务状态机。全部方法为纯值域操作：没有锁、
// goroutine 或 I/O；调用方（server 侧 Companion Manager）保证权威 tick 边界
// 的单写者串行化。非法迁移一律 no-op 并返回空事件——状态机的合法路径由本
// 文件一处定义，调用方的防御性错误不会破坏队列内容。
package companion

// TaskQueue 是一个伙伴的待执行 FIFO 与当前任务槽。
//
// 不变量：
//   - pending 至多 MaxTaskQueueDepth 条，严格按接收顺序排列；
//   - 同一时刻至多一个非终态任务（current 槽）；
//   - current 进入终态即清空槽位，下一次 BeginHead 立即开始原队首；
//   - generation 随每次队首提升单调递增，用于丢弃过时 worker 结果。
type TaskQueue struct {
	pending    []Task
	current    Task
	hasCurrent bool
	generation uint64
}

// Len 返回 FIFO 中的待执行指令数（不含当前任务）。
func (q *TaskQueue) Len() int { return len(q.pending) }

// Generation 返回当前世代计数。worker 结果携带的世代与它不符即为过时。
func (q *TaskQueue) Generation() uint64 { return q.generation }

// Enqueue 把一条指令按接收顺序追加到 FIFO 尾部。文本非法或 FIFO 已有
// MaxTaskQueueDepth 条待执行指令时同步拒绝并返回 false——拒绝绝不影响既有
// 队列内容，也不会触碰任何模型请求。
func (q *TaskQueue) Enqueue(command TaskCommand) bool {
	if command.Validate() != nil || len(q.pending) >= MaxTaskQueueDepth {
		return false
	}
	q.pending = append(q.pending, Task{Command: command, State: TaskQueued})
	return true
}

// Current 返回当前任务的值拷贝。返回 ok=false 表示没有非终态任务。
func (q *TaskQueue) Current() (Task, bool) {
	return q.current, q.hasCurrent
}

// BeginHead 把队首指令提升为当前任务：世代递增并盖戳到任务上。已有当前任务
// 或 FIFO 为空时返回 false。前一个任务进入终态的同一 tick 即可调用本方法，
// 满足“终态后立即开始原队首”的规格约束。
func (q *TaskQueue) BeginHead() bool {
	if q.hasCurrent || len(q.pending) == 0 {
		return false
	}
	q.generation++
	head := q.pending[0]
	q.pending = q.pending[1:]
	head.Generation = q.generation
	head.State = TaskQueued
	q.current = head
	q.hasCurrent = true
	return true
}

// BeginPlanning 把当前任务从 Queued 迁移到 Planning。规划请求的发起由编排层
// 在迁移成功后进行；本方法只推进状态，不产生公开事件（Planning 是内部阶段）。
func (q *TaskQueue) BeginPlanning() bool {
	if !q.hasCurrent || q.current.State != TaskQueued {
		return false
	}
	q.current.State = TaskPlanning
	return true
}

// AcceptPlan 把模型返回的计划挂到当前任务上并迁移到 Validating。计划的结构
// 校验推迟到 FinishValidation——解码层已保证的约束在这里再验一次，防止未来的
// 恢复路径（任务 7）把未解码的持久化数据直接送进执行。
func (q *TaskQueue) AcceptPlan(plan Plan) []TaskEvent {
	if !q.hasCurrent || q.current.State != TaskPlanning {
		return nil
	}
	q.current.Plan = plan
	q.current.State = TaskValidating
	return nil
}

// FailPlanning 令 Planning 阶段的任务以指定原因失败（PlannerUnavailable 或
// InvalidPlan）。产生 TaskFailed 事件事实并清空当前槽位。
func (q *TaskQueue) FailPlanning(reason TaskFailReason) []TaskEvent {
	if !q.hasCurrent || q.current.State != TaskPlanning || reason == TaskFailNone {
		return nil
	}
	return q.finishFailure(reason)
}

// FinishValidation 结束 Validating：计划结构校验失败令任务以 InvalidPlan 失败；
// 校验通过则进入 Running——记录 StartTick 与 deadline（世界时间 + 超时分钟数）
// 并产出唯一的 TaskStarted 事件事实。
func (q *TaskQueue) FinishValidation(worldTimeTicks uint64, timeoutMinutes int) []TaskEvent {
	if !q.hasCurrent || q.current.State != TaskValidating {
		return nil
	}
	if err := q.current.Plan.Validate(); err != nil {
		return q.finishFailure(TaskFailInvalidPlan)
	}
	q.current.State = TaskRunning
	q.current.StartTick = worldTimeTicks
	q.current.DeadlineTicks = TaskDeadlineTicks(worldTimeTicks, timeoutMinutes)
	return []TaskEvent{{Kind: TaskEventStarted}}
}

// CompleteStep 标记当前计划步骤完成并推进 StepIndex。若还有后续步骤，保持
// Running 并产出 TaskProgress；最后一个步骤完成则产出 TaskCompleted 终态事件
// 并清空当前槽位。
func (q *TaskQueue) CompleteStep() []TaskEvent {
	if !q.hasCurrent || q.current.State != TaskRunning {
		return nil
	}
	if q.current.StepIndex+1 < len(q.current.Plan.Steps) {
		q.current.StepIndex++
		return []TaskEvent{{Kind: TaskEventProgress}}
	}
	return q.finishState(TaskCompleted, TaskFailNone)
}

// FailRun 令 Running 阶段的任务以指定原因失败（PathUnreachable 或
// WorldChanged），产出 TaskFailed 事件并清空当前槽位。Runner 从不重试、不
// 降级、不改写计划——失败原因一经判定即终局。
func (q *TaskQueue) FailRun(reason TaskFailReason) []TaskEvent {
	if !q.hasCurrent || q.current.State != TaskRunning || reason == TaskFailNone {
		return nil
	}
	return q.finishFailure(reason)
}

// Expire 在世界时间到达或越过 deadline 时把 Running 任务转入 TimedOut 终态。
// 未到期（或当前任务不在 Running）时是 no-op，返回空事件。
func (q *TaskQueue) Expire(worldTimeTicks uint64) []TaskEvent {
	if !q.hasCurrent || q.current.State != TaskRunning ||
		!q.current.Expired(worldTimeTicks) {
		return nil
	}
	return q.finishState(TaskTimedOut, TaskFailNone)
}

// finishState 把当前任务置为指定终态并返回对应事件事实。终态任务保留在
// 返回前的值里供 Snapshot 消费后即被清出槽位。
func (q *TaskQueue) finishState(state TaskState, reason TaskFailReason) []TaskEvent {
	q.current.State = state
	q.current.FailReason = reason
	q.hasCurrent = false
	return []TaskEvent{{Kind: terminalEventKind(state), Reason: reason}}
}

// finishFailure 是 finishState 的失败特化：终态固定为 TaskFailed。
func (q *TaskQueue) finishFailure(reason TaskFailReason) []TaskEvent {
	return q.finishState(TaskFailed, reason)
}

// terminalEventKind 把终态映射为事件类别；非终态是编程错误，返回 None 让
// 上层的事件组装显式暴露缺事件。
func terminalEventKind(state TaskState) TaskEventKind {
	switch state {
	case TaskCompleted:
		return TaskEventCompleted
	case TaskFailed:
		return TaskEventFailed
	case TaskTimedOut:
		return TaskEventTimedOut
	default:
		return TaskEventNone
	}
}

// TaskQueueState 是一个伙伴任务域的持久化观察输入：当前任务（若有）与剩余
// pending 指令的深拷贝。它经 companionPersistence.Observe 进入 dirty 判定；
// 载荷落盘由任务 7 扩展，本里程碑存储层可暂忽略其内容。
type TaskQueueState struct {
	ID         ID
	HasCurrent bool
	Current    Task
	Pending    []TaskCommand
}

// Snapshot 返回队列的可持久化深拷贝。返回值与队列此后的一切迁移互不影响。
func (q *TaskQueue) Snapshot() TaskQueueState {
	return TaskQueueState{
		HasCurrent: q.hasCurrent,
		Current:    q.current,
		Pending:    pendingCommands(q.pending),
	}
}

// pendingCommands 提取 pending 任务的指令文本列表。
func pendingCommands(pending []Task) []TaskCommand {
	commands := make([]TaskCommand, len(pending))
	for index := range pending {
		commands[index] = pending[index].Command
	}
	return commands
}
