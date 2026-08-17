// 本文件定义 Dialogue 的触发节点类型与节点选择的确定性纯函数。节点选择只
// 依赖计划长度与任务事实（spec：companion-dialogue「触发节点确定且每任务
// 八次预算」），绝不依赖模型输出或任何非确定状态，因此同一任务在任何服务端
// 上导出完全相同的节点集合。全部为纯类型与纯函数，无 I/O、无 goroutine；
// follow 任务的 first_arrival 语义由 manager 侧接线（持续跟随没有计划步骤
// 完成事实，本文件只服务普通任务的进展节点选择）。
package companion

import "fmt"

// 台词触发节点的预算常量（spec 冻结值）。
const (
	// MaxDialogueRequestsPerTask 是同一任务全生命周期的台词请求预算上限：
	// 1（开始）+ MaxDialogueProgressNodes（进展）+ 1（终态）= 8。
	MaxDialogueRequestsPerTask = 8
	// MaxDialogueProgressNodes 是普通任务按计划长度确定性均匀选择的进展节点
	// 上限，同时是 SelectProgressSteps 在长计划上选取的节点个数。
	MaxDialogueProgressNodes = 6
	// FollowDialogueNodeCount 是持续跟随任务的节点总数：开始、首次到达跟随
	// 距离与终止恰好三个，期间步骤进度不产生台词请求（follow 是无终点的
	// 持续步骤，不存在「步骤完成」事实）。
	FollowDialogueNodeCount = 3
)

// 关于预算不持久化的裁决（design.md「触发节点确定性算法」）：预算计数的目
// 的是限制模型调用量这个运行期资源，不是需要跨重启保持的事实。摘要才是持久
// 记忆；节点集合在计划校验成功后按需重导出，重启后已发起计数从零开始，任务
// 最多多花 ≤8 次请求，属可接受的模型调用量上界。把限流事实混入任务持久状态
// 会增加 schema 字段并让「台词尽力而为」的表达平面污染事实平面，已被否决。

// DialogueNodeKind 是台词触发节点的类别。开始节点 = 任务进入 Running；进展
// 节点 = 一个被选中的计划步骤完成；终止节点 = 任务进入四种终态之一
// （Completed/Failed/TimedOut/Stopped 全部视为终止节点——主动停止是玩家的
// 成功意图，与失败终态一样值得一句收尾台词）。
type DialogueNodeKind uint8

const (
	// DialogueNodeStart 是任务进入 Running 时的一次性节点。
	DialogueNodeStart DialogueNodeKind = iota + 1
	// DialogueNodeProgress 是一个被 SelectProgressSteps 选中的计划步骤
	// 完成时的节点，携带该步骤的 kind。
	DialogueNodeProgress
	// DialogueNodeTerminal 是任务进入终态时的一次性节点，携带具体终态与
	// 稳定原因。
	DialogueNodeTerminal
)

// DialogueNode 是一次台词请求携带的当前事实节点：类别 + 类别专属载荷。
// 载荷复用既有语义域枚举——StepKind 是 PlanStepKind（进展节点携带完成的
// 步骤类型）、State 是 TaskState（终止节点携带四种终态之一）、Reason 是
// TaskFailReason（Failed 终态携带服务器侧稳定失败原因）。三类枚举都是服务
// 器产生的稳定事实，节点文本进入 Dialogue 提示时全部是数据而不是指令。
type DialogueNode struct {
	// Kind 是节点类别；专属载荷按下表使用，其余字段必须保持零值。
	Kind DialogueNodeKind
	// StepKind 仅 Progress 节点有效：完成的步骤类型，只允许 go_to/mine/
	// place——follow 是无终点的持续步骤，没有「步骤完成」事实，出现在进展
	// 节点即调用方缺陷。
	StepKind PlanStepKind
	// State 仅 Terminal 节点有效：四种终态之一（TaskState 的零值 0 不是
	// 合法状态）。
	State TaskState
	// Reason 仅 Terminal 且 State 为 TaskFailed 时有效：稳定失败原因；其余
	// 终态（Completed/TimedOut/Stopped）的 reason 恒为 None，与任务事件
	// 侧的稳定事实组合一致。
	Reason TaskFailReason
}

// Validate 校验节点的类别与专属载荷组合矩阵：Start 不携带任何载荷；
// Progress 只携带 go_to/mine/place 步骤类型；Terminal 只携带四种终态之一，
// Failed 终态必须携带稳定原因，其余终态原因必须为 None。组合约束把「节点
// 身份」收敛为有限合法形态，manager 侧的结果过时判定（任务与节点身份比对）
// 依赖这一确定性。
func (n DialogueNode) Validate() error {
	switch n.Kind {
	case DialogueNodeStart:
		if n.StepKind != 0 || n.State != 0 || n.Reason != TaskFailNone {
			return fmt.Errorf("companion: 开始台词节点不得携带载荷")
		}
		return nil
	case DialogueNodeProgress:
		if n.State != 0 || n.Reason != TaskFailNone {
			return fmt.Errorf("companion: 进展台词节点不得携带终态或原因")
		}
		switch n.StepKind {
		case PlanStepGoTo, PlanStepMine, PlanStepPlace:
			return nil
		default:
			return fmt.Errorf("companion: 进展台词节点步骤类型 %d 非法（follow 无完成事实）", n.StepKind)
		}
	case DialogueNodeTerminal:
		if n.StepKind != 0 {
			return fmt.Errorf("companion: 终止台词节点不得携带步骤类型")
		}
		if !n.State.Terminal() {
			return fmt.Errorf("companion: 终止台词节点终态 %d 非法", n.State)
		}
		if n.State == TaskFailed {
			if n.Reason == TaskFailNone {
				return fmt.Errorf("companion: 失败终止台词节点缺少稳定原因")
			}
			return nil
		}
		if n.Reason != TaskFailNone {
			return fmt.Errorf("companion: 非失败终止台词节点不得携带原因")
		}
		return nil
	default:
		return fmt.Errorf("companion: 台词节点类别 %d 未交付", n.Kind)
	}
}

// SelectProgressSteps 按计划长度确定性均匀选择至多 MaxDialogueProgressNodes
// 个进展节点，返回严格升序去重的 0-based 步骤索引。
//
// 算法（design.md 公式）：stepCount ≤ 6 时全选；否则把整个计划六等分，取每
// 段末尾步骤的完成节点——1-based 步骤号 = floor(i*stepCount/6)（i=1..6），
// 换算 0-based 索引 = 步骤号-1。i=6 恰好落在最后一步（floor(n)=n → 索引
// n-1），末段永远被覆盖；每段长度 ≥ stepCount/6 ≥ 1，六个值天然严格递增，
// 去重循环只是防御性兜底。输出只依赖 stepCount，同一输入在任何时刻、任何
// 进程得到完全相同的结果，manager 侧可安全地在计划校验后一次性预计算并随
// 任务保存于内存。
//
// 负数与零返回空集（空计划本身不合法，这是对调用方缺陷的防御性返回）；
// stepCount ≤ 6 时返回 [0, stepCount)，与全选语义一致。
func SelectProgressSteps(stepCount int) []int {
	if stepCount <= 0 {
		return nil
	}
	if stepCount <= MaxDialogueProgressNodes {
		steps := make([]int, stepCount)
		for index := range steps {
			steps[index] = index
		}
		return steps
	}
	steps := make([]int, 0, MaxDialogueProgressNodes)
	for i := 1; i <= MaxDialogueProgressNodes; i++ {
		// floor(i*stepCount/6) 是 1-based 步骤号；整数除法天然向下取整。
		index := i*stepCount/MaxDialogueProgressNodes - 1
		if index >= stepCount {
			// 防御性夹取：公式推导保证 index ≤ stepCount-1，此分支不可达，
			// 仅防御未来公式改动引入越界。
			index = stepCount - 1
		}
		if len(steps) > 0 && index <= steps[len(steps)-1] {
			// 等距公式在 stepCount > 6 时天然严格递增，去重只是防御性兜底。
			continue
		}
		steps = append(steps, index)
	}
	return steps
}
