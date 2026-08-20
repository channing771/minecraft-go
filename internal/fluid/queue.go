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

// Queue 是流体的待更新队列：一组 (位置, 到期 tick) 记录，按位置去重。
//
// Queue 不是并发安全的：调用方（权威 tick）必须保证任意时刻只有一个
// goroutine 访问同一个 Queue 实例。
//
// Queue 不持久化（design.md D5）：它只是通往平衡态的中间态，重启后由调用方
// 对已加载区块执行边界重扫（对每个流体格及其空气邻居调用 Enqueue）来恢复。
//
// # 结构与不变量
//
// 队列由两部分组成，职责严格分开：
//
//   - pending 是队列内容的**唯一真相来源**：一个位置在队列中，当且仅当它是
//     pending 的键；它排定的到期 tick 就是对应的值。Len、去重、「取出即删除」
//     全部只看 pending。
//   - order 是按 lessItem 组织的二叉最小堆，只回答「下一批取谁」这一个问题。
//     它是**索引**，不是真相：里面允许存在过时条目。
//
// 三条不变量（任何改动都必须同时保住）：
//
//  1. 覆盖性：对 pending 里的每个 (pos, due)，order 中至少存在一条与之完全
//     相等的 item{pos, due}。Enqueue 是 pending 的唯一写入点，且每次真正写入
//     都紧跟一次 pushOrder，因此这条不变量成立。
//  2. 过时条目可丢弃：order 中若有 item 满足「pending 里没有该 pos」或
//     「pending[pos] != item.dueTick」，它一定是被更早的一次 Enqueue 覆盖过
//     或已被处理删除，直接丢弃不会丢任何真实待更新项——因为不变量 1 保证真实
//     项在 order 里另有一条精确匹配的条目。
//  3. 堆序：order 满足最小堆性质，堆顶恒为全序 lessItem 下的最小条目。
//
// # 为什么这个结构能让单 tick 成本与 len(pending) 解耦
//
// 旧实现每 tick 都要遍历整张 pending 并对全部到期项排序，成本 Θ(len(pending))
// 与 Θ(D log D)，与本 tick 实际能处理多少项完全无关——预算只截断处理循环，
// 截断不掉这两步。改成最小堆之后，Advance 只做「看堆顶、取堆顶」：
//
//   - 堆顶未到期就立刻停（O(1)），一项都不看；
//   - 到期就弹出，代价 O(log len(order))，取够 budget 项即停。
//
// 于是单 tick 触及的**项数**为 budget + 1（最后那一次未到期的堆顶探视）加上
// 本 tick 顺带丢弃的过时条目数，与 len(pending) 无关；单 tick 的**时间**正比于
// 这个项数乘以 log len(order)。
//
// 过时条目的上界：它只在 Enqueue 把一个**已在队列中**的位置的 dueTick **提前**
// 时产生（Enqueue 对不提前的重复入队直接早返回，不推堆）。生产路径上 now 单调
// 不减、delay 取自同一 tick 的 tunable 快照，因此已有条目的 due 恒不大于新算出
// 的 due，Enqueue 永远走早返回分支，过时条目数恒为 0。即便将来出现提前入队，
// 过时条目总数也被「提前入队次数」摊还压住：每一次提前入队至多制造一条过时
// 条目，每条至多被弹出丢弃一次。
type Queue struct {
	// pending 以位置去重，值是该位置当前排定的 dueTick。Go 的 map 遍历顺序
	// 是随机的，任何依赖处理次序的逻辑都绝不能直接遍历本 map——次序一律由
	// order 堆按 lessItem 给出。
	pending map[core.BlockPos]uint64
	// order 是 lessItem 全序下的二叉最小堆，见类型注释的三条不变量。
	order []item
	// lastAdvanceExamined 记录最近一次 Advance 从 order 里取出/探视的条目数。
	// 它是「单 tick 成本与 len(pending) 解耦」这条结构性属性的可观测量，供
	// queue_bounded_test.go 直接断言；生产路径不读它。
	lastAdvanceExamined int
}

// NewQueue 构造一个空的待更新队列。
func NewQueue() *Queue {
	return &Queue{pending: make(map[core.BlockPos]uint64)}
}

// pushOrder 把 it 压入最小堆并上浮到位，代价 O(log len(order))。
func (q *Queue) pushOrder(it item) {
	q.order = append(q.order, it)
	child := len(q.order) - 1
	for child > 0 {
		parent := (child - 1) / 2
		if !lessItem(q.order[child], q.order[parent]) {
			break
		}
		q.order[child], q.order[parent] = q.order[parent], q.order[child]
		child = parent
	}
}

// popOrder 弹出并返回全序最小的条目，代价 O(log len(order))。
// 调用方必须先确认 len(q.order) > 0。
func (q *Queue) popOrder() item {
	top := q.order[0]
	last := len(q.order) - 1
	q.order[0] = q.order[last]
	// 清掉尾槽再截断：item 不含指针，这一步不为 GC，而是避免截断后的备用
	// 容量里残留一份看起来合法的旧条目，误导调试。
	q.order[last] = item{}
	q.order = q.order[:last]

	parent := 0
	for {
		left, right := 2*parent+1, 2*parent+2
		smallest := parent
		if left < len(q.order) && lessItem(q.order[left], q.order[smallest]) {
			smallest = left
		}
		if right < len(q.order) && lessItem(q.order[right], q.order[smallest]) {
			smallest = right
		}
		if smallest == parent {
			break
		}
		q.order[parent], q.order[smallest] = q.order[smallest], q.order[parent]
		parent = smallest
	}
	return top
}

// Enqueue 把 pos 加入待更新队列，到期 tick 为 now+delay。
//
// delay（流动延迟）由调用方传入，本包不定义任何隐藏默认值——它归 sim 的
// tunable 所有（design.md D2 的依赖方向约束）。
//
// 若 pos 已在队列中，保留两次入队里更早的 dueTick：重复的入队请求（比如
// 流动传播同时把同一格标记为「自身变化」与「某邻居变化的邻居」）不应该把
// 已排定的更新往后推迟。
//
// 早返回的那一支**不推堆**：这既是去重（同一位置在堆里不会因重复入队无限
// 膨胀），也是「生产路径上过时条目恒为 0」这条论证的落点，见 Queue 的类型
// 注释。
func (q *Queue) Enqueue(pos core.BlockPos, now, delay uint64) {
	due := now + delay
	if existing, ok := q.pending[pos]; ok && existing <= due {
		return
	}
	q.pending[pos] = due
	q.pushOrder(item{pos: pos, dueTick: due})
}

// Clear 清空队列中的全部待更新项。
//
// 提供给调用方在重启/区块重新进入活动兴趣范围时，先清空再执行边界重扫——
// 队列不持久化，重扫是唯一的恢复路径（design.md D5）。
//
// pending 与 order 必须一起清：只清其中一个会让堆里全是过时条目（或让
// pending 里的项在堆中无对应条目而永远取不出来），破坏覆盖性不变量。
func (q *Queue) Clear() {
	clear(q.pending)
	q.order = q.order[:0]
}

// Len 返回当前排队的待更新项数。主要供测试与可观测性使用。
//
// 以 pending 为准而不是 len(q.order)：order 允许含过时条目，只有 pending 才是
// 队列内容的真相来源。
func (q *Queue) Len() int {
	return len(q.pending)
}

// Advance 推进一个 tick 的流体，返回本 tick 实际发生变化的格（按 lessPos
// 定义的位置全序，与处理次序无关，见下面第 4/5 点）。
//
// 语义：
//  1. 按 lessItem 全序，从队列里取出最小的、且 dueTick<=now 的项。取用最小堆
//     完成，**不遍历 pending、也不对整批到期项排序**：本 tick 触及的条目数由
//     budget 封顶，与 len(pending) 无关（论证见 Queue 的类型注释）。
//  2. 最多处理 budget 个；超出的项保持在队列里、dueTick 不变（既没从 pending
//     删除，也没从 order 弹出），按原全序顺延到后续 Advance 调用——不会被丢弃
//     （spec.md「预算不改变平衡态」）。
//  3. 存活/替换判定只读取 w 在本次 Advance 调用开始时的状态：evalCell 只
//     读不写，本函数在整个处理循环期间不调用 w.SetBlock，全部候选写入先
//     收集到 pendingWrites，循环结束后才一次性提交。这避免了同一 tick 内
//     一次写入被后续求值读到，从而让处理次序影响结果（design.md 提到的
//     振荡风险）。
//  4. 若同一 tick 内多个来源（不同待更新格的传播）都想写同一目标格，取
//     流体等级最小（最强）者生效（spec.md「同 tick 冲突写入取最强者」）；
//     合并用 strongerWrite 实现，是可交换、可结合的运算，结果只取决于
//     参与合并的候选值集合本身，与这些候选值被枚举/合并的次序无关——不管
//     process 的处理次序、不管 evalCell 内部 map 的遍历次序。
//  5. 因本 tick 变化（包括消失为空气）的格，其自身与六个面邻格以
//     dueTick=now+delay 重新入队，供后续 tick 继续推进。返回值按 lessPos
//     排序而非处理次序，与提交顺序、广播顺序保持同一套确定性排序口径。
//
// delay（流动延迟）与 budget（每 tick 预算）都是调用参数，本包不读取任何
// 包内 tunable——这两个值归 sim 所有（design.md D2）。
func (q *Queue) Advance(now uint64, w FluidWorld, budget int, delay uint64) []core.BlockPos {
	if budget < 0 {
		// 负数预算没有物理意义，按 0 处理（本 tick 不处理任何项）。
		budget = 0
	}
	q.lastAdvanceExamined = 0

	// 阶段一：按全序取出至多 budget 个到期项，就地只读求值，把全部候选写入
	// 合并进 pendingWrites。
	//
	// 同一目标格可能被多个不同的待更新格同 tick 写入（比如两股水从不同方向
	// 汇合到同一格）：spec.md「同 tick 冲突写入取最强者」要求取流体等级
	// 最小（最强）者，且结果不依赖参与合并的源格之间的遍历顺序——用
	// strongerWrite 合并，它可交换、可结合，天然满足这一点，不需要依赖
	// 取出次序已经按全序排好这件事。
	//
	// 「一格自身消亡写 Air」与「某邻居同 tick 向该格写水」这两类写入不会
	// 冲突：evalCell 的自我消亡分支只在 flowingSurvives 判否时触发，而
	// flowingSurvives 判否恰好意味着「上方不是流体」且「不存在等级更小的
	// 水平邻居」；反过来，任何能把水写进该格的邻居 B——不论是 B 在其正上方
	// 做垂直传播（此时 B 本身就是「上方是流体」的见证），还是 B 做水平传播
	// 且 nextLevel < 本格等级（此时 B 本身就是「等级更小的水平邻居」）——都
	// 恰好构成该格的存活支撑，使 flowingSurvives 判真、自我消亡分支根本不会
	// 触发。两者在当前规则集下不可达同 tick 冲突，strongerWrite 里让流体
	// 优先于空气纯粹是防御性兜底（万一将来规则变化打破这条论证），不是当前
	// 规则下真的会走到的分支。
	pendingWrites := make(map[core.BlockPos]core.BlockID)
	for processed := 0; processed < budget && len(q.order) > 0; {
		if q.order[0].dueTick > now {
			// 堆顶是全序最小项，它都没到期，后面的更不会到期：本 tick 到此
			// 为止。这一步是 O(1)，与队列里还压着多少项无关。
			q.lastAdvanceExamined++
			break
		}
		it := q.popOrder()
		q.lastAdvanceExamined++
		if due, ok := q.pending[it.pos]; !ok || due != it.dueTick {
			// 过时条目（见 Queue 类型注释的不变量 2）：真实待更新项在堆里
			// 另有一条精确匹配的条目，丢弃它不丢任何东西，也不消耗预算。
			continue
		}
		delete(q.pending, it.pos) // 该项本 tick 已被取出处理，从队列移除。
		processed++
		for pos, id := range evalCell(it.pos, w) {
			if existing, ok := pendingWrites[pos]; ok {
				pendingWrites[pos] = strongerWrite(existing, id)
			} else {
				pendingWrites[pos] = id
			}
		}
	}

	// 阶段二：一次性提交，并只把「值真的变了」的格计入本 tick 的变化集合。
	// pendingWrites 是 map，遍历顺序随机；先按 lessPos 排序目标格再遍历，
	// 使返回的变化集合与提交顺序都不依赖 map 的随机遍历顺序。只在值真的
	// 变化时才调用 w.SetBlock：调用方（未来的 sim 适配器）的 SetBlock 很可能
	// 附带 dirty 标记与区块变更广播，无变化的写入会产生纯噪声的存档改写与
	// 网络广播，因此不能无条件调用。
	//
	// 这里的排序规模由 budget 封顶（至多 budget 次 evalCell，每次至多 4 个
	// 目标格），同样与 len(pending) 无关。
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
			w.SetBlock(pos, id)
		}
	}

	for _, pos := range changed {
		q.Enqueue(pos, now, delay)
		for _, n := range sixNeighbors(pos) {
			q.Enqueue(n, now, delay)
		}
	}

	return changed
}
