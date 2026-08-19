package fluid

import (
	"sort"

	"github.com/channing771/mornlea/internal/core"
)

// item 是待更新队列的一条记录：位置与到期 tick。
type item struct {
	pos     core.BlockPos
	dueTick uint64
}

// lessItem 实现 design.md D3 的全序：(dueTick, ChunkKey, y, z, x)。
//
// core.BlockPos 不携带维度，FluidWorld 同理只按世界坐标寻址；调用方（sim）
// 按维度各自持有独立的 Queue 实例，因此同一个 Queue 内的坐标天然属于同一
// 维度，这里用区块坐标 (X, Z) 近似排序键里的 ChunkKey，不需要再比较维度。
//
// 这条全序只依赖 dueTick 与位置本身，与元素被 Enqueue 的先后次序无关——这是
// spec.md「入队顺序无关」得以成立的基础：不管调用方以什么顺序调用 Enqueue，
// 只要最终集合相同，排序结果就相同。
func lessItem(a, b item) bool {
	if a.dueTick != b.dueTick {
		return a.dueTick < b.dueTick
	}
	return lessPos(a.pos, b.pos)
}

// lessPos 实现全序里去掉 dueTick 之后剩下的 (ChunkKey, y, z, x) 部分，
// 单独抽出来是因为 Advance 在合并变更集合时也需要一个与 dueTick 无关、
// 纯粹按位置排序的全序（见 Advance 内的说明）。
func lessPos(a, b core.BlockPos) bool {
	ca, cb := a.Chunk(), b.Chunk()
	if ca.X != cb.X {
		return ca.X < cb.X
	}
	if ca.Z != cb.Z {
		return ca.Z < cb.Z
	}
	if a.Y != b.Y {
		return a.Y < b.Y
	}
	if a.Z != b.Z {
		return a.Z < b.Z
	}
	return a.X < b.X
}

// sortItems 就地按全序排序 items。
func sortItems(items []item) {
	sort.Slice(items, func(i, j int) bool { return lessItem(items[i], items[j]) })
}

// Queue 是流体的待更新队列：一组 (位置, 到期 tick) 记录，按位置去重。
//
// Queue 不是并发安全的：调用方（权威 tick）必须保证任意时刻只有一个
// goroutine 访问同一个 Queue 实例。
//
// Queue 不持久化（design.md D5）：它只是通往平衡态的中间态，重启后由调用方
// 对已加载区块执行边界重扫（对每个流体格及其空气邻居调用 Enqueue）来恢复。
type Queue struct {
	// pending 以位置去重。Go 的 map 遍历顺序是随机的，Advance 处理前必须把
	// 到期项落到 sortItems 定义的全序，绝不能直接遍历本 map。
	pending map[core.BlockPos]uint64
}

// NewQueue 构造一个空的待更新队列。
func NewQueue() *Queue {
	return &Queue{pending: make(map[core.BlockPos]uint64)}
}

// Enqueue 把 pos 加入待更新队列，到期 tick 为 now+delay。
//
// delay（流动延迟）由调用方传入，本包不定义任何隐藏默认值——它归 sim 的
// tunable 所有（design.md D2 的依赖方向约束）。
//
// 若 pos 已在队列中，保留两次入队里更早的 dueTick：重复的入队请求（比如
// 流动传播同时把同一格标记为「自身变化」与「某邻居变化的邻居」）不应该把
// 已排定的更新往后推迟。
func (q *Queue) Enqueue(pos core.BlockPos, now, delay uint64) {
	due := now + delay
	if existing, ok := q.pending[pos]; ok && existing <= due {
		return
	}
	q.pending[pos] = due
}

// Clear 清空队列中的全部待更新项。
//
// 提供给调用方在重启/区块重新进入活动兴趣范围时，先清空再执行边界重扫——
// 队列不持久化，重扫是唯一的恢复路径（design.md D5）。
func (q *Queue) Clear() {
	for k := range q.pending {
		delete(q.pending, k)
	}
}

// Len 返回当前排队的待更新项数。主要供测试与可观测性使用。
func (q *Queue) Len() int {
	return len(q.pending)
}

// Advance 推进一个 tick 的流体，返回本 tick 实际发生变化的格（按处理次序）。
//
// 语义：
//  1. 从队列里挑出 dueTick<=now 的项，按 sortItems 的全序排序。
//  2. 最多处理 budget 个；超出的项保持在队列里、dueTick 不变，按原全序
//     顺延到后续 Advance 调用——不会被丢弃（spec.md「预算不改变平衡态」）。
//  3. 存活/替换判定只读取 w 在本次 Advance 调用开始时的状态：evalCell 只
//     读不写，本函数在整个处理循环期间不调用 w.SetBlock，全部候选写入先
//     收集到 pendingWrites，循环结束后才一次性提交。这避免了同一 tick 内
//     一次写入被后续求值读到，从而让处理次序影响结果（design.md 提到的
//     振荡风险）。
//  4. 若同一 tick 内多个来源（不同待更新格的传播）都想写同一目标格，取
//     处理次序中较晚的一次生效。处理次序本身只由 sortItems 的全序决定、
//     与入队顺序无关，因此这条合并规则不会破坏「入队顺序无关」。
//  5. 因本 tick 变化（包括消失为空气）的格，其自身与六个面邻格以
//     dueTick=now+delay 重新入队，供后续 tick 继续推进。
//
// delay（流动延迟）与 budget（每 tick 预算）都是调用参数，本包不读取任何
// 包内 tunable——这两个值归 sim 所有（design.md D2）。
func (q *Queue) Advance(now uint64, w FluidWorld, budget int, delay uint64) []core.BlockPos {
	due := make([]item, 0)
	for pos, dueTick := range q.pending {
		if dueTick <= now {
			due = append(due, item{pos: pos, dueTick: dueTick})
		}
	}
	sortItems(due)

	if budget < 0 {
		// 负数预算没有物理意义，按 0 处理（本 tick 不处理任何项），
		// 避免下面的切片操作因负索引 panic。
		budget = 0
	}
	process := due
	if len(process) > budget {
		process = due[:budget]
		// 超出预算的项原样留在 q.pending 里（dueTick 不变），不从队列删除。
	}

	// 阶段一：只读求值，把全部候选写入合并进 pendingWrites。
	pendingWrites := make(map[core.BlockPos]core.BlockID)
	for _, it := range process {
		delete(q.pending, it.pos) // 该项本 tick 已被取出处理，从队列移除。
		for pos, id := range evalCell(it.pos, w) {
			pendingWrites[pos] = id // 全序中较晚处理的项覆盖较早的，语义见函数注释。
		}
	}

	// 阶段二：一次性提交，并只把「值真的变了」的格计入本 tick 的变化集合。
	// pendingWrites 是 map，遍历顺序随机；先按 lessPos 排序目标格再遍历，
	// 使返回的变化集合与提交顺序都不依赖 map 的随机遍历顺序。
	targets := make([]core.BlockPos, 0, len(pendingWrites))
	for pos := range pendingWrites {
		targets = append(targets, pos)
	}
	sort.Slice(targets, func(i, j int) bool { return lessPos(targets[i], targets[j]) })

	changed := make([]core.BlockPos, 0, len(targets))
	for _, pos := range targets {
		id := pendingWrites[pos]
		if w.BlockAt(pos) != id {
			changed = append(changed, pos)
		}
		w.SetBlock(pos, id)
	}

	for _, pos := range changed {
		q.Enqueue(pos, now, delay)
		for _, n := range sixNeighbors(pos) {
			q.Enqueue(n, now, delay)
		}
	}

	return changed
}
