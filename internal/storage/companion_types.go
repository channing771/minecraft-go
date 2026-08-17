package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

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
	// MaxCompanionSummaryBytes 是单条记录最近对话摘要的持久化字节上界，
	// 与 Dialogue 请求输入/终态响应的摘要上界同源（companion.
	// MaxDialogueSummaryBytes）：同一常量保证「能被写进请求的摘要」与
	// 「能被存档保留的摘要」两条边界不可能漂移。
	MaxCompanionSummaryBytes = companion.MaxDialogueSummaryBytes
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

// StoredCompanionTask 是存档中一条当前任务的持久化载荷。任务计划自带的
// 模型 summary 与 Generation 刻意不落盘：计划摘要是模型对计划的自由文本
// 描述，不属于任务事实（M5C/M5D 的任务事件不携带模型自由文本），世代只
// 用于丢弃过时 worker 结果，重启后没有在途请求可丢弃。注意与记录级的最
// 近对话摘要（StoredCompanionQueue.Summary，v4 起持久化）区分：后者是
// Dialogue 表达平面的近期记忆，不是任务事实。
type StoredCompanionTask struct {
	// Command 是玩家原始指令（不含 @伙伴名 前缀），≤MaxCompanionTaskCommandBytes。
	Command string
	// PlanSteps 是计划步骤（交付全集四 kind，编码按 kind 变长）；只有
	// Running 任务携带（模型计划只在 Validating 成功后落盘），
	// ≤MaxCompanionPlanSteps。
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

// StoredCompanionQueue 是一个伙伴任务域的持久化载荷：当前任务（若有）、
// 按接收顺序排列的 FIFO 指令与最近对话摘要（v4 起）。空载荷（无当前任务、
// FIFO 为空且摘要为空）不可保存。队列载荷同时是记录的 active 信号：调用方
// 只为当前配置的 active 伙伴提供队列——inactive 记录不提供队列（含摘要），
// 编码即无摘要区，去激活由此天然丢弃摘要（spec：inactive 记录 MUST NOT
// 保存摘要）。
type StoredCompanionQueue struct {
	ID         companion.ID
	HasCurrent bool
	Current    StoredCompanionTask
	Pending    []string
	// Summary 是该伙伴的最近对话摘要（Dialogue 终态响应捎带写入）：
	// ≤MaxCompanionSummaryBytes 字节、有效 UTF-8、不含 NUL；空串等价于
	// 「无摘要」——编码不写摘要区，解码读到的 v3/v2/v1 迁移记录恒为空。
	// 摘要只喂后续 Dialogue 请求，绝不进入 Planner 输入。
	Summary string
}

// CompanionStore 定义伙伴聚合存档的加载与保存边界。
type CompanionStore interface {
	LoadCompanions(context.Context) (StoredCompanions, error)
	SaveCompanions(context.Context, CompanionSave) error
}

// validateStoredCompanionTask 校验单条任务载荷的全部不变量：状态与失败
// 原因的枚举与配对、指令文本边界、计划步骤按 schema 的结构合法性，以及
// "计划只在 Running 落盘"的字段耦合（非 Running 必须无步骤、无进度、
// 无计时）。编码与解码共用本函数，保证双向边界一致；schema 只影响步骤
// 集合（v2 只认 go_to、v3 认交付全集四 kind），其余不变量跨 schema 相同。
func validateStoredCompanionTask(task StoredCompanionTask, schema uint32) error {
	if task.State < companion.TaskQueued || task.State > companion.TaskStopped {
		return fmt.Errorf("%w: companion task state %d outside enum", ErrCorrupt, task.State)
	}
	if task.State == companion.TaskFailed {
		if task.FailReason <= companion.TaskFailNone ||
			task.FailReason > companion.TaskFailInventoryFull {
			return fmt.Errorf("%w: companion task fail reason %d invalid", ErrCorrupt, task.FailReason)
		}
	} else if task.FailReason != companion.TaskFailNone {
		return fmt.Errorf("%w: companion task fail reason %d without failed state", ErrCorrupt, task.FailReason)
	}
	if err := companion.TaskCommand(task.Command).Validate(); err != nil {
		return fmt.Errorf("%w: companion task command: %v", ErrCorrupt, err)
	}
	if task.State == companion.TaskRunning {
		// 步骤约束与 companion 侧 validPlanSteps 的结构校验保持一致
		//（summary 不落盘，故不能复用整份计划校验；place 方块的注册表
		// 值域校验依赖 companion 的私有注册表，由恢复路径 RestoreCurrent
		// → validPlanSteps 兜底，存档边界把 Block 当作有界不透明载荷）。
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
	hasFollow := false
	for index, step := range task.PlanSteps {
		if err := validateStoredPlanStep(step, index, len(task.PlanSteps), schema); err != nil {
			return err
		}
		if step.Kind == companion.PlanStepFollow {
			hasFollow = true
		}
	}
	// 持续跟随不保存 deadline：DeadlineTicks 零值即运行期超时豁免（Task.
	// Expired 跳过零值），非零 deadline 的 follow 任务若被放行，恢复后将
	// 错误地重新挂上超时。v2 载荷不含 follow 步骤，本校验天然不影响 v2
	// 迁移；编码与解码共用同一道门。
	if hasFollow && task.DeadlineTicks != 0 {
		return fmt.Errorf(
			"%w: companion follow task keeps deadline %d", ErrCorrupt, task.DeadlineTicks,
		)
	}
	return nil
}

// validateStoredPlanStep 校验单个计划步骤的结构约束。v2 只写过 go_to：
// 任何其他 kind 都是 v2 时代不可能出现的字节，按损坏拒绝（迁移读入后按
// v3 重写）。v3 按交付全集四 kind 校验：坐标步骤的 Y 必须在世界竖直边界
// 内、follow 的目标必须是有效 UUIDv4 且只能居末（follow 没有自然终点，
// 排在其后的步骤无从执行——与 companion.validPlanSteps 的结构约束一致，
// 存档边界提前拒绝，恢复路径无需再丢弃）。各 kind 未使用字段必须为零：
// 变长编码只写 kind 专属字段，非零的未用字段会在编码时静默丢失，零值
// 约束保证 round-trip 精确无损。
func validateStoredPlanStep(step companion.PlanStep, index, total int, schema uint32) error {
	if schema == companionSchemaV2 {
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
		return nil
	}
	switch step.Kind {
	case companion.PlanStepGoTo, companion.PlanStepMine:
		if step.Block != 0 || step.PlayerID != (core.PlayerID{}) {
			return fmt.Errorf(
				"%w: companion task plan step %d keeps unused payload", ErrCorrupt, index,
			)
		}
	case companion.PlanStepPlace:
		if step.PlayerID != (core.PlayerID{}) {
			return fmt.Errorf(
				"%w: companion task plan step %d keeps unused player payload", ErrCorrupt, index,
			)
		}
	case companion.PlanStepFollow:
		if step.X != 0 || step.Y != 0 || step.Z != 0 || step.Block != 0 {
			return fmt.Errorf(
				"%w: companion task plan step %d keeps unused coordinate payload", ErrCorrupt, index,
			)
		}
		if !step.PlayerID.Valid() {
			return fmt.Errorf(
				"%w: companion task plan step %d follow target invalid", ErrCorrupt, index,
			)
		}
		if index != total-1 {
			return fmt.Errorf(
				"%w: companion task plan step %d follow is not last", ErrCorrupt, index,
			)
		}
	default:
		return fmt.Errorf(
			"%w: companion task plan step %d kind %d is not delivered", ErrCorrupt, index, step.Kind,
		)
	}
	if step.Kind != companion.PlanStepFollow && (step.Y < core.MinY || step.Y >= core.MaxY) {
		return fmt.Errorf(
			"%w: companion task plan step %d Y=%d outside world", ErrCorrupt, index, step.Y,
		)
	}
	return nil
}

// validateStoredCompanionQueues 校验一组任务载荷的结构不变量：非空、ID
// 唯一、每条关联一条既有记录、当前任务与 FIFO 全部有界。records 是已按
// ID 升序排好的保存记录；本函数只读，不修改任何输入切片。schema 决定
// 步骤集合的校验口径——编码端恒为当前 schema（v4）。
func validateStoredCompanionQueues(
	queues []StoredCompanionQueue,
	records []companion.Body,
	schema uint32,
) error {
	known := make(map[companion.ID]struct{}, len(records))
	for _, body := range records {
		known[body.ID] = struct{}{}
	}
	seen := make(map[companion.ID]struct{}, len(queues))
	for index, queue := range queues {
		if err := validateStoredCompanionQueue(queue, schema); err != nil {
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
// 与 FIFO 每条指令的字节上界、最近对话摘要的文本边界。HasCurrent 为假时
// Current 不参与编码（任务区只随 flags bit0 落盘），因此必须整体为零值——
// 非零的 Current 无法在磁盘上表达，放行它会静默丢数据，一律拒绝。摘要按
// v4 语义校验（v3/v2/v1 迁移读入的载荷摘要恒为空，天然通过）。
func validateStoredCompanionQueue(queue StoredCompanionQueue, schema uint32) error {
	if !queue.HasCurrent && len(queue.Pending) == 0 && queue.Summary == "" {
		return fmt.Errorf("%w: empty companion queue", ErrCorrupt)
	}
	if !queue.ID.Valid() {
		return fmt.Errorf("%w: invalid companion queue ID", ErrCorrupt)
	}
	if queue.HasCurrent {
		if err := validateStoredCompanionTask(queue.Current, schema); err != nil {
			return err
		}
	} else if !storedCompanionTaskIsZero(queue.Current) {
		return fmt.Errorf("%w: companion queue keeps current task without HasCurrent", ErrCorrupt)
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
	if err := validateStoredCompanionSummary(queue.Summary); err != nil {
		return err
	}
	return nil
}

// validateStoredCompanionSummary 校验最近对话摘要的持久化边界：不超过
// MaxCompanionSummaryBytes 字节、有效 UTF-8、不含 NUL。与 companion 侧
// validateDialogueSummary 同一规则（字节上界、编码与无 NUL），但存档边界
// 不设非空约束——空串等价于「无摘要」，是摘要被清空后的合法持久状态。
// 编码与解码共用本函数，保证双向边界一致。
func validateStoredCompanionSummary(summary string) error {
	if len(summary) > MaxCompanionSummaryBytes {
		return fmt.Errorf(
			"%w: companion summary %d bytes exceeds limit", ErrCorrupt, len(summary),
		)
	}
	if !utf8.ValidString(summary) {
		return fmt.Errorf("%w: companion summary is not valid UTF-8", ErrCorrupt)
	}
	if strings.ContainsRune(summary, 0) {
		return fmt.Errorf("%w: companion summary contains NUL", ErrCorrupt)
	}
	return nil
}

// storedCompanionTaskIsZero 报告任务载荷是否为整体零值。载荷含切片字段
// 不可用 == 比较，这里逐字段判断；HasCurrent 为假的队列要求 Current 为
// 零值（磁盘形态无法表达它，非零即调用方缺陷）。
func storedCompanionTaskIsZero(task StoredCompanionTask) bool {
	return task.Command == "" &&
		task.PlanSteps == nil &&
		task.StepIndex == 0 &&
		task.State == 0 &&
		task.StartTick == 0 &&
		task.DeadlineTicks == 0 &&
		task.FailReason == companion.TaskFailNone
}
