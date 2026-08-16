// 任务 FIFO 与任务状态机的纯值域测试：容量、接收顺序、溢出拒绝、七态全路径、
// 停止迁移的三条件矩阵、世代丢弃锚点与 deadline 的世界时间语义。全部用例不
// 涉及 goroutine 或 I/O——并发与编排归 server 侧 Companion Manager。
package companion

import (
	"slices"
	"testing"

	"github.com/channing771/mornlea/internal/core"
)

// drivePlanningToRunning 把当前任务从 Queued 一路推进到 Running，是全路径表驱动
// 测试的公共前缀。plan 的步骤数由调用方控制以覆盖 Progress/Completed 分支。
func drivePlanningToRunning(t *testing.T, queue *TaskQueue, steps []PlanStep) []TaskEvent {
	t.Helper()
	if !queue.BeginPlanning() {
		t.Fatal("BeginPlanning 失败")
	}
	if events := queue.AcceptPlan(Plan{Summary: "测试计划", Steps: steps}); len(events) != 0 {
		t.Fatalf("AcceptPlan 产出事件=%v，想要无事件", events)
	}
	events := queue.FinishValidation(1000, 10)
	if len(events) != 1 || events[0].Kind != TaskEventStarted {
		t.Fatalf("FinishValidation 事件=%v，想要唯一 TaskStarted", events)
	}
	return events
}

func TestTaskQueueCapacityOrderAndOverflow(t *testing.T) {
	var queue TaskQueue
	for index := range MaxTaskQueueDepth {
		if !queue.Enqueue(TaskCommand(commandText(index))) {
			t.Fatalf("第 %d 条入队被拒，容量内必须全部成功", index+1)
		}
	}
	if queue.Enqueue(TaskCommand("第十七条")) {
		t.Fatal("第 17 条入队成功，想要 FIFO 满同步拒绝")
	}
	if got := queue.Len(); got != MaxTaskQueueDepth {
		t.Fatalf("FIFO 深度=%d，想要 %d", got, MaxTaskQueueDepth)
	}

	// 队首提升为当前任务后，空出的槽位允许再次填满；当前任务是唯一非终态。
	if !queue.BeginHead() {
		t.Fatal("BeginHead 失败")
	}
	current, ok := queue.Current()
	if !ok || current.Command != TaskCommand(commandText(0)) || current.State != TaskQueued {
		t.Fatalf("队首任务=%+v ok=%v，想要首条指令且 Queued", current, ok)
	}
	if !queue.Enqueue(TaskCommand("补充指令")) {
		t.Fatal("取出队首后补充入队被拒")
	}
	if queue.Enqueue(TaskCommand("再次溢出")) {
		t.Fatal("重新填满后入队成功，想要拒绝")
	}

	// 当前任务进入终态后必须立即允许开始原队首，且严格保持接收顺序。
	if !queue.BeginPlanning() {
		t.Fatal("BeginPlanning 失败")
	}
	if events := queue.FailPlanning(TaskFailPlannerUnavailable); len(events) != 1 ||
		events[0].Kind != TaskEventFailed {
		t.Fatalf("终态事件=%v，想要 TaskFailed", events)
	}
	if _, still := queue.Current(); still {
		t.Fatal("终态后当前任务未清空")
	}
	if !queue.BeginHead() {
		t.Fatal("终态后 BeginHead 失败")
	}
	current, _ = queue.Current()
	if current.Command != TaskCommand(commandText(1)) {
		t.Fatalf("第二条指令=%q，想要接收顺序保持", current.Command)
	}

	// 非法指令文本不入队也不占容量。
	var empty TaskQueue
	if empty.Enqueue(TaskCommand("")) || empty.Enqueue(TaskCommand(" \t ")) ||
		empty.Enqueue(TaskCommand(string([]byte{0xff, 0xfe}))) {
		t.Fatal("非法指令文本入队成功")
	}
	if empty.Len() != 0 || empty.Enqueue(TaskCommand("合法")) != true {
		t.Fatalf("非法文本污染容量：len=%d", empty.Len())
	}
}

func TestTaskStateMachineFullPaths(t *testing.T) {
	oneStep := []PlanStep{{Kind: PlanStepGoTo, X: 1, Y: 1, Z: 1}}
	twoSteps := []PlanStep{
		{Kind: PlanStepGoTo, X: 1, Y: 1, Z: 1},
		{Kind: PlanStepGoTo, X: 2, Y: 1, Z: 2},
	}

	t.Run("Completed", func(t *testing.T) {
		var queue TaskQueue
		queue.Enqueue(TaskCommand("走一步"))
		queue.BeginHead()
		started := drivePlanningToRunning(t, &queue, oneStep)
		current, _ := queue.Current()
		if current.State != TaskRunning || current.StartTick != 1000 ||
			current.DeadlineTicks != TaskDeadlineTicks(1000, 10) {
			t.Fatalf("Running 任务=%+v started=%v", current, started)
		}
		events := queue.CompleteStep()
		if len(events) != 1 || events[0].Kind != TaskEventCompleted {
			t.Fatalf("终态事件=%v，想要 TaskCompleted", events)
		}
		if _, ok := queue.Current(); ok {
			t.Fatal("Completed 后当前任务未清空")
		}
	})

	t.Run("ProgressThenCompleted", func(t *testing.T) {
		var queue TaskQueue
		queue.Enqueue(TaskCommand("走两步"))
		queue.BeginHead()
		drivePlanningToRunning(t, &queue, twoSteps)
		events := queue.CompleteStep()
		if len(events) != 1 || events[0].Kind != TaskEventProgress {
			t.Fatalf("首步事件=%v，想要 TaskProgress", events)
		}
		current, _ := queue.Current()
		if current.StepIndex != 1 || current.State != TaskRunning {
			t.Fatalf("首步后任务=%+v，想要 StepIndex=1 且保持 Running", current)
		}
		events = queue.CompleteStep()
		if len(events) != 1 || events[0].Kind != TaskEventCompleted {
			t.Fatalf("末步事件=%v，想要 TaskCompleted", events)
		}
	})

	t.Run("FailedFromPlanning", func(t *testing.T) {
		for _, reason := range []TaskFailReason{TaskFailPlannerUnavailable, TaskFailInvalidPlan} {
			var queue TaskQueue
			queue.Enqueue(TaskCommand("规划即败"))
			queue.BeginHead()
			queue.BeginPlanning()
			events := queue.FailPlanning(reason)
			if len(events) != 1 || events[0].Kind != TaskEventFailed || events[0].Reason != reason {
				t.Fatalf("reason=%d 事件=%v", reason, events)
			}
			if _, ok := queue.Current(); ok {
				t.Fatal("Failed 后当前任务未清空")
			}
		}
	})

	t.Run("FailedFromValidatingInvalidPlan", func(t *testing.T) {
		var queue TaskQueue
		queue.Enqueue(TaskCommand("非法计划"))
		queue.BeginHead()
		queue.BeginPlanning()
		queue.AcceptPlan(Plan{Summary: "", Steps: oneStep}) // summary 非法
		events := queue.FinishValidation(1000, 10)
		if len(events) != 1 || events[0].Kind != TaskEventFailed ||
			events[0].Reason != TaskFailInvalidPlan {
			t.Fatalf("校验失败事件=%v，想要 TaskFailed(InvalidPlan)", events)
		}
		if _, ok := queue.Current(); ok {
			t.Fatal("校验失败后当前任务未清空")
		}
	})

	t.Run("FailedFromRunning", func(t *testing.T) {
		for _, reason := range []TaskFailReason{TaskFailPathUnreachable, TaskFailWorldChanged} {
			var queue TaskQueue
			queue.Enqueue(TaskCommand("执行失败"))
			queue.BeginHead()
			drivePlanningToRunning(t, &queue, oneStep)
			events := queue.FailRun(reason)
			if len(events) != 1 || events[0].Kind != TaskEventFailed || events[0].Reason != reason {
				t.Fatalf("reason=%d 事件=%v", reason, events)
			}
		}
	})

	t.Run("TimedOut", func(t *testing.T) {
		var queue TaskQueue
		queue.Enqueue(TaskCommand("超时任务"))
		queue.BeginHead()
		drivePlanningToRunning(t, &queue, oneStep)
		current, _ := queue.Current()
		if queue.Expire(current.DeadlineTicks-1) != nil {
			t.Fatal("deadline 前到期")
		}
		events := queue.Expire(current.DeadlineTicks)
		if len(events) != 1 || events[0].Kind != TaskEventTimedOut {
			t.Fatalf("超时事件=%v，想要 TaskTimedOut", events)
		}
		if _, ok := queue.Current(); ok {
			t.Fatal("TimedOut 后当前任务未清空")
		}
	})

	t.Run("StoppedFromRunningFollow", func(t *testing.T) {
		var queue TaskQueue
		queue.Enqueue(TaskCommand("跟随"))
		queue.BeginHead()
		drivePlanningToRunning(t, &queue, stopFollowSteps())
		events := queue.Stop()
		if len(events) != 1 || events[0].Kind != TaskEventStopped ||
			events[0].Reason != TaskFailNone {
			t.Fatalf("停止事件=%v，想要唯一 TaskStopped(reason None)", events)
		}
		if _, ok := queue.Current(); ok {
			t.Fatal("Stopped 后当前任务未清空")
		}
		// 终态清槽后重复停止是 no-op：第二次停止面对的是空槽。
		if events := queue.Stop(); len(events) != 0 {
			t.Fatalf("重复停止产出事件=%v，想要 no-op", events)
		}
	})

	t.Run("IllegalTransitionsAreNoOps", func(t *testing.T) {
		var queue TaskQueue
		queue.Enqueue(TaskCommand("防御性"))
		if !queue.BeginHead() {
			t.Fatal("BeginHead 失败")
		}
		// Queued 状态下只允许 BeginPlanning。
		if queue.BeginPlanning() && queue.BeginPlanning() {
			t.Fatal("重复 BeginPlanning 成功")
		}
		if events := queue.FinishValidation(1, 10); len(events) != 0 {
			t.Fatalf("Planning 前校验产出事件=%v", events)
		}
		if events := queue.CompleteStep(); len(events) != 0 {
			t.Fatalf("非 Running 完成步骤产出事件=%v", events)
		}
		if events := queue.Expire(^uint64(0)); len(events) != 0 {
			t.Fatalf("非 Running 超时产出事件=%v", events)
		}
		if events := queue.FailRun(TaskFailPathUnreachable); len(events) != 0 {
			t.Fatalf("非 Running 失败产出事件=%v", events)
		}
		if events := queue.Stop(); len(events) != 0 {
			t.Fatalf("Queued 态停止产出事件=%v", events)
		}
		if _, ok := queue.Current(); !ok {
			t.Fatal("非法迁移清掉了当前任务")
		}
		// 无当前任务时全部迁移都是 no-op。
		var idle TaskQueue
		if idle.BeginPlanning() || idle.CompleteStep() != nil || idle.Expire(1) != nil ||
			idle.FailRun(TaskFailPathUnreachable) != nil || idle.FailPlanning(TaskFailInvalidPlan) != nil ||
			idle.Stop() != nil {
			t.Fatal("无当前任务的迁移不是 no-op")
		}
	})
}

// stopFollowSteps 构造「持续跟随任务」的计划步骤：go_to 前缀 + follow 尾步。
// 可停性判定基准是计划的最后一步为 follow（执行器由后续任务交付），前缀
// go_to 用于证明判定只看尾步而不是「计划里出现 follow」。
func stopFollowSteps() []PlanStep {
	target, err := core.ParsePlayerID(testPlayerUUID)
	if err != nil {
		panic("companion: 测试 follow 目标 ID 非法: " + err.Error())
	}
	return []PlanStep{
		{Kind: PlanStepGoTo, X: 1, Y: 1, Z: 1},
		{Kind: PlanStepFollow, PlayerID: target},
	}
}

// TestTaskQueueStopGuardMatrix 锁定停止迁移的可停性三条件矩阵与终态事实：
// 存在当前任务、状态为 Running、计划最后一步是 follow。任一条件不满足都
// 必须是 no-op 并返回空事件——普通 go_to 或空闲伙伴的停止由编排层以
// NotFollowing 同步拒绝，状态机绝不静默改写队列内容或任务状态。
func TestTaskQueueStopGuardMatrix(t *testing.T) {
	goToSteps := []PlanStep{{Kind: PlanStepGoTo, X: 1, Y: 1, Z: 1}}

	t.Run("FollowTailSucceedsAndKeepsFIFO", func(t *testing.T) {
		var queue TaskQueue
		queue.Enqueue(TaskCommand("跟着我"))
		queue.Enqueue(TaskCommand("下一条"))
		queue.BeginHead()
		drivePlanningToRunning(t, &queue, stopFollowSteps())
		events := queue.Stop()
		if len(events) != 1 || events[0].Kind != TaskEventStopped ||
			events[0].Reason != TaskFailNone {
			t.Fatalf("停止事件=%v，想要唯一 TaskStopped(reason None)", events)
		}
		if _, ok := queue.Current(); ok {
			t.Fatal("Stopped 后当前任务未清空")
		}
		// 停止只清当前槽：pending 既不清空也不重排，原队首立即可被提升，
		// 推进语义与既有终态完全一致。
		if got := queue.Len(); got != 1 {
			t.Fatalf("停止后 pending=%d，想要 1（队列不变）", got)
		}
		if !queue.BeginHead() {
			t.Fatal("停止后 BeginHead 失败，原队首必须立即可开始")
		}
		current, _ := queue.Current()
		if current.Command != TaskCommand("下一条") {
			t.Fatalf("原队首=%q，想要 下一条", current.Command)
		}
	})

	t.Run("NotRunningIsNoOp", func(t *testing.T) {
		states := []struct {
			name    string
			prepare func(*TaskQueue)
		}{
			{"Queued", func(*TaskQueue) {}},
			{"Planning", func(q *TaskQueue) { q.BeginPlanning() }},
			{"Validating", func(q *TaskQueue) {
				q.BeginPlanning()
				q.AcceptPlan(Plan{Summary: "跟随计划", Steps: stopFollowSteps()})
			}},
		}
		for _, state := range states {
			var queue TaskQueue
			queue.Enqueue(TaskCommand("未运行"))
			queue.BeginHead()
			state.prepare(&queue)
			if events := queue.Stop(); len(events) != 0 {
				t.Fatalf("%s 态停止产出事件=%v，想要 no-op", state.name, events)
			}
			current, ok := queue.Current()
			if !ok || current.State == TaskStopped {
				t.Fatalf("%s 态被停止改写：current=%+v ok=%v", state.name, current, ok)
			}
		}
	})

	t.Run("NonFollowTailIsNoOp", func(t *testing.T) {
		var queue TaskQueue
		queue.Enqueue(TaskCommand("普通移动"))
		queue.BeginHead()
		drivePlanningToRunning(t, &queue, goToSteps)
		if events := queue.Stop(); len(events) != 0 {
			t.Fatalf("普通 go_to 停止产出事件=%v，想要 no-op", events)
		}
		current, ok := queue.Current()
		if !ok || current.State != TaskRunning {
			t.Fatalf("普通任务被停止改写：current=%+v ok=%v", current, ok)
		}
	})

	t.Run("NoCurrentIsNoOp", func(t *testing.T) {
		var queue TaskQueue
		if events := queue.Stop(); len(events) != 0 {
			t.Fatalf("空闲队列停止产出事件=%v", events)
		}
	})

	t.Run("StoppedIsTerminal", func(t *testing.T) {
		if !TaskStopped.Terminal() {
			t.Fatal("TaskStopped 必须是终态")
		}
		if got := TaskStopped.String(); got != "已停止" {
			t.Fatalf("TaskStopped 中文短名=%q，想要 已停止", got)
		}
	})
}

func TestTaskQueueGenerationDiscardsStaleResults(t *testing.T) {
	var queue TaskQueue
	if got := queue.Generation(); got != 0 {
		t.Fatalf("初始世代=%d，想要 0", got)
	}
	queue.Enqueue(TaskCommand("第一世代"))
	queue.BeginHead()
	first, _ := queue.Current()
	if first.Generation != 1 || queue.Generation() != 1 {
		t.Fatalf("首任务世代=%d queue=%d，想要 1", first.Generation, queue.Generation())
	}
	// 携带旧世代的 worker 结果必须能被世代比较识别为过时：世代随每次队首提升
	// 单调递增，绝不回退、绝不复用。
	if !queue.BeginPlanning() {
		t.Fatal("BeginPlanning 失败")
	}
	queue.Enqueue(TaskCommand("第二世代"))
	queue.FailPlanning(TaskFailPlannerUnavailable)
	queue.BeginHead()
	second, _ := queue.Current()
	if second.Generation != 2 || queue.Generation() != 2 {
		t.Fatalf("次任务世代=%d queue=%d，想要 2", second.Generation, queue.Generation())
	}
	stale := first.Generation != queue.Generation()
	if !stale {
		t.Fatal("旧世代结果无法与当前世代区分")
	}
	// 终态清空当前任务但保留世代计数。
	if !queue.BeginPlanning() {
		t.Fatal("第二任务 BeginPlanning 失败")
	}
	queue.FailPlanning(TaskFailPlannerUnavailable)
	if queue.Generation() != 2 {
		t.Fatalf("终态后世代=%d，想要保持 2", queue.Generation())
	}
}

func TestTaskDeadlineUsesWorldTimeTicks(t *testing.T) {
	if got := TaskDeadlineTicks(24000, 1); got != 24000+TicksPerMinute {
		t.Fatalf("1 分钟 deadline=%d，想要 %d", got, 24000+TicksPerMinute)
	}
	if got := TaskDeadlineTicks(0, 10); got != 10*TicksPerMinute {
		t.Fatalf("10 分钟 deadline=%d，想要 %d", got, 10*TicksPerMinute)
	}
	// timeout 归一沿用 ModelSettings.TaskTimeout 的缺省约定：0 分钟不合法，
	// 由配置层守住；这里锁定 1..60 全区间都能换算。
	for minutes := 1; minutes <= 60; minutes++ {
		want := uint64(minutes) * TicksPerMinute
		if got := TaskDeadlineTicks(0, minutes); got != want {
			t.Fatalf("%d 分钟 deadline=%d，想要 %d", minutes, got, want)
		}
	}

	// deadline 只与传入的世界时间比较：关服停摆（世界时间不推进）期间不消耗。
	var queue TaskQueue
	queue.Enqueue(TaskCommand("世界时间"))
	queue.BeginHead()
	queue.BeginPlanning()
	queue.AcceptPlan(Plan{Summary: "计划", Steps: []PlanStep{{Kind: PlanStepGoTo, X: 0, Y: 1, Z: 0}}})
	queue.FinishValidation(500, 1)
	task, _ := queue.Current()
	if task.DeadlineTicks != 500+TicksPerMinute {
		t.Fatalf("deadline=%d，想要 %d", task.DeadlineTicks, 500+TicksPerMinute)
	}
	if task.Expired(task.DeadlineTicks - 1) {
		t.Fatal("deadline 前判定到期")
	}
	if !task.Expired(task.DeadlineTicks) || !task.Expired(task.DeadlineTicks+1) {
		t.Fatal("到达与越过 deadline 未判定到期")
	}
}

func TestTaskQueueSnapshotRecordsPendingCommands(t *testing.T) {
	var queue TaskQueue
	queue.Enqueue(TaskCommand("甲"))
	queue.Enqueue(TaskCommand("乙"))
	queue.BeginHead()
	queue.BeginPlanning()
	records := queue.Snapshot()
	if records.Pending == nil || len(records.Pending) != 1 || records.Pending[0] != TaskCommand("乙") {
		t.Fatalf("Pending=%v，想要只剩乙", records.Pending)
	}
	if !records.HasCurrent || records.Current.Command != TaskCommand("甲") ||
		records.Current.State != TaskPlanning || records.Current.Generation != 1 {
		t.Fatalf("Current=%+v has=%v", records.Current, records.HasCurrent)
	}
	// 快照必须深拷贝：后续迁移不改变已取出的记录。
	queue.FailPlanning(TaskFailPlannerUnavailable)
	if records.HasCurrent != true || records.Current.State != TaskPlanning {
		t.Fatalf("快照被后续迁移污染：%+v", records.Current)
	}
	if !slices.Equal(records.Pending, []TaskCommand{"乙"}) {
		t.Fatalf("Pending 被污染：%v", records.Pending)
	}
}

// commandText 生成确定性的合法指令文本。
func commandText(index int) string {
	return "指令-" + string(rune('A'+index))
}
