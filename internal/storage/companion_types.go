package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
)

// ErrCompanionsNotFound 表示世界尚无伙伴聚合存档。
var ErrCompanionsNotFound = errors.New("storage: companions not found")

// 任务区与 FIFO 的持久化上界。全部在编码/解码边界强制：磁盘文件与内存
// 占用都不随世界规模无界增长（推导见 codec 的 maxCompanionFileLength）。
const (
	// MaxCompanionTaskCommandBytes 是任务区与 FIFO 每条指令的持久化字节
	// 上界，与网络聊天指令及 TaskCommand 的上界一致。
	MaxCompanionTaskCommandBytes = companion.MaxPlanCommandBytes
	// MaxCompanionPlanSteps 是单条任务持久化计划步骤数的防御性二进制
	// 上界：设计上不设步骤数上限而以 64 KiB 模型响应为天然界限（最密
	// go_to JSON step ≥30 bytes，实际 ≤ ~2,200 步），这里固定 5,000 以
	// 封顶单记录磁盘占用。
	MaxCompanionPlanSteps = 5000
	// MaxCompanionFIFOEntries 是单伙伴 FIFO 的持久化条数上界，与运行期
	// TaskQueue 的容量一致。
	MaxCompanionFIFOEntries = companion.MaxTaskQueueDepth
)

// StoredCompanions 是从聚合存档恢复的伙伴身体快照与任务域载荷。Queues
// 只包含有任务事实的记录（v1 文件迁移后恒为 nil）；每条载荷与记录经 ID
// 关联，记录本体按 ID 严格升序排列。
type StoredCompanions struct {
	Revision uint64
	Records  []companion.Body
	Queues   []StoredCompanionQueue
}

// CompanionSave 是一次伙伴身体与任务域聚合保存请求。Queues 的每条载荷
// 必须关联一条 Records 中的记录；编码只读取载荷，绝不修改调用方切片。
type CompanionSave struct {
	Revision uint64
	Records  []companion.Body
	Queues   []StoredCompanionQueue
}

// StoredCompanionTask 是 v2 存档中一条当前任务的持久化载荷。Summary 与
// Generation 刻意不落盘：模型自由文本不属于任务事实（M5D 前不上屏），
// 世代只用于丢弃过时 worker 结果，重启后没有在途请求可丢弃。
type StoredCompanionTask struct {
	// Command 是玩家原始指令（不含 @伙伴名 前缀），≤MaxCompanionTaskCommandBytes。
	Command string
	// PlanSteps 是 go_to 计划步骤；只有 Running 任务携带（模型计划只在
	// Validating 成功后落盘），≤MaxCompanionPlanSteps。
	PlanSteps []companion.PlanStep
	// StepIndex 是下一个待执行步骤的索引；仅 Running 任务可非零。
	StepIndex int
	// State 是保存时刻的六态任务状态。
	State companion.TaskState
	// StartTick 与 DeadlineTicks 使用持久化 WorldTimeTicks：关服期间世界
	// 时间不推进，恢复后不消耗执行时长；仅 Running 任务可非零。
	StartTick     uint64
	DeadlineTicks uint64
	// FailReason 仅与 TaskFailed 状态成对出现，其余状态必须为 TaskFailNone。
	FailReason companion.TaskFailReason
}

// StoredCompanionQueue 是一个伙伴任务域的持久化载荷：当前任务（若有）与
// 按接收顺序排列的 FIFO 指令。空载荷（无当前任务且 FIFO 为空）不可保存。
type StoredCompanionQueue struct {
	ID         companion.ID
	HasCurrent bool
	Current    StoredCompanionTask
	Pending    []string
}

// CompanionStore 定义伙伴聚合存档的加载与保存边界。
type CompanionStore interface {
	LoadCompanions(context.Context) (StoredCompanions, error)
	SaveCompanions(context.Context, CompanionSave) error
}

// validateStoredCompanionTask 校验单条任务载荷的全部不变量：状态与失败
// 原因的枚举与配对、指令文本边界、计划步骤的 go_to 合法性，以及
// "计划只在 Running 落盘"的字段耦合（非 Running 必须无步骤、无进度、
// 无计时）。编码与解码共用本函数，保证双向边界一致。
func validateStoredCompanionTask(task StoredCompanionTask) error {
	if task.State < companion.TaskQueued || task.State > companion.TaskTimedOut {
		return fmt.Errorf("%w: companion task state %d outside enum", ErrCorrupt, task.State)
	}
	if task.State == companion.TaskFailed {
		if task.FailReason <= companion.TaskFailNone ||
			task.FailReason > companion.TaskFailWorldChanged {
			return fmt.Errorf("%w: companion task fail reason %d invalid", ErrCorrupt, task.FailReason)
		}
	} else if task.FailReason != companion.TaskFailNone {
		return fmt.Errorf("%w: companion task fail reason %d without failed state", ErrCorrupt, task.FailReason)
	}
	if err := companion.TaskCommand(task.Command).Validate(); err != nil {
		return fmt.Errorf("%w: companion task command: %v", ErrCorrupt, err)
	}
	if task.State == companion.TaskRunning {
		// 步骤约束与 companion.Plan.Validate 的步骤校验保持一致（summary
		// 不落盘，故不能复用整份计划校验）。
		if len(task.PlanSteps) == 0 {
			return fmt.Errorf("%w: running companion task has no plan steps", ErrCorrupt)
		}
		if task.StepIndex < 0 || task.StepIndex >= len(task.PlanSteps) {
			return fmt.Errorf(
				"%w: companion task step index %d outside plan", ErrCorrupt, task.StepIndex,
			)
		}
	} else if len(task.PlanSteps) != 0 || task.StepIndex != 0 ||
		task.StartTick != 0 || task.DeadlineTicks != 0 {
		return fmt.Errorf(
			"%w: companion task keeps plan progress outside running state", ErrCorrupt,
		)
	}
	if len(task.PlanSteps) > MaxCompanionPlanSteps {
		return fmt.Errorf(
			"%w: companion task plan steps %d exceeds limit", ErrCorrupt, len(task.PlanSteps),
		)
	}
	for index, step := range task.PlanSteps {
		if step.Kind != companion.PlanStepGoTo {
			return fmt.Errorf(
				"%w: companion task plan step %d kind %d is not go_to", ErrCorrupt, index, step.Kind,
			)
		}
		if step.Y < core.MinY || step.Y >= core.MaxY {
			return fmt.Errorf(
				"%w: companion task plan step %d Y=%d outside world", ErrCorrupt, index, step.Y,
			)
		}
	}
	return nil
}

// validateStoredCompanionQueues 校验一组任务载荷的结构不变量：非空、ID
// 唯一、每条关联一条既有记录、当前任务与 FIFO 全部有界。records 是已按
// ID 升序排好的保存记录；本函数只读，不修改任何输入切片。
func validateStoredCompanionQueues(queues []StoredCompanionQueue, records []companion.Body) error {
	known := make(map[companion.ID]struct{}, len(records))
	for _, body := range records {
		known[body.ID] = struct{}{}
	}
	seen := make(map[companion.ID]struct{}, len(queues))
	for index, queue := range queues {
		if err := validateStoredCompanionQueue(queue); err != nil {
			return fmt.Errorf("companion queue %d: %w", index, err)
		}
		if _, duplicate := seen[queue.ID]; duplicate {
			return fmt.Errorf("%w: duplicate companion queue ID", ErrCorrupt)
		}
		if _, exists := known[queue.ID]; !exists {
			return fmt.Errorf("%w: companion queue without body record", ErrCorrupt)
		}
		seen[queue.ID] = struct{}{}
	}
	return nil
}

// validateStoredCompanionQueue 校验单条队列载荷：非空、ID 有效、当前任务
// 与 FIFO 每条指令的字节上界。
func validateStoredCompanionQueue(queue StoredCompanionQueue) error {
	if !queue.HasCurrent && len(queue.Pending) == 0 {
		return fmt.Errorf("%w: empty companion queue", ErrCorrupt)
	}
	if !queue.ID.Valid() {
		return fmt.Errorf("%w: invalid companion queue ID", ErrCorrupt)
	}
	if err := validateStoredCompanionTask(queue.Current); err != nil {
		return err
	}
	if len(queue.Pending) > MaxCompanionFIFOEntries {
		return fmt.Errorf(
			"%w: companion FIFO depth %d exceeds limit", ErrCorrupt, len(queue.Pending),
		)
	}
	for index, command := range queue.Pending {
		if err := companion.TaskCommand(command).Validate(); err != nil {
			return fmt.Errorf("companion FIFO entry %d: %w: %v", index, ErrCorrupt, err)
		}
	}
	return nil
}
