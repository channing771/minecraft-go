package fluid

import (
	"math"
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
// # 结构：索引最小堆
//
// 队列由两份互为镜像的数据组成：
//
//   - order 是按 lessItem 组织的二叉最小堆，**它本身就是队列内容**，每个排队
//     位置在里面恰好占一条记录；
//   - index 是 order 的完全反查表，把位置映射到它当前所在的下标。
//
// 两条不变量（任何改动都必须同时保住）：
//
//  1. **双射**：len(index) == len(order)，且对每个下标 i 都有
//     index[order[i].pos] == i。等价地说：一个位置在队列中当且仅当它是 index
//     的键，而它的 dueTick 只有 order[index[pos]] 这一个存放处。
//  2. **堆序**：order 满足最小堆性质，堆顶恒为 lessItem 全序下的最小条目。
//
// 维护方式：所有元素移动都经 swapOrder，它在交换两条记录的同时更新两者的
// index，因此不变量 1 在 siftUp/siftDown 的每一步中间态都成立；pushOrder 与
// popOrder 各自在末尾维护自己那一条 index 的增删。
//
// # 为什么索引堆使「过时条目」不可能存在
//
// 上一版用的是惰性删除堆：没有 index，Enqueue 把一个已在队的位置改到更早的
// dueTick 时只能**再压一条**，旧那条就成了「过时条目」，只能等它浮到堆顶再
// 弹出丢弃。那带来两个问题，都已在实测中兑现：
//
//   - 过时条目不消耗预算，单个 Advance 会一口气弹掉几万条（delay 从 100 下调
//     到 0 的实测：5 万条）；
//   - 更严重的是，**过时条目的条数依赖 Enqueue 的调用次序**（同一位置先以
//     delay=5 再以 delay=0 入队会留下一条过时条目，反过来则一条都不留，而两者
//     得到的队列内容完全相同）。一旦有任何按「本 tick 处理了多少条」分叉的
//     逻辑，推进结果就依赖了入队次序——这直接违反 spec.md 那条无条件 MUST
//     「推进顺序 MUST NOT 依赖待更新项的入队顺序或任何哈希遍历顺序」。
//
// 索引堆从根上消掉这个类：有了 index，Enqueue 命中已在队的位置时**就地改写**
// 那唯一一条记录的 dueTick 再上浮，绝不新增第二条。由不变量 1，任一位置在
// order 中至多一条记录，因此「与 index 不符的残留条目」在结构上无法产生——
// 不是「实际不会发生」，是「表示里没有地方放它」。
//
// 由此，Advance 每 tick 弹出的条目全部是真实待更新项，探视数恒为
// budget + 至多 1 次未到期堆顶探视，与 len(order) 无关，也与任何 tunable
// 怎么调无关。
//
// # 单 tick 成本正比于什么
//
// 单 tick 触及的条目数 ≤ budget + 1，每次取出付 O(log len(order))；提交阶段
// 排序的目标格数 ≤ 4*budget。没有任何一项正比于队列规模。budget 的上界由调用
// 方的 FluidUpdatesPerTick 保证。
type Queue struct {
	// order 见类型注释：按 lessItem 组织的最小堆，每个排队位置恰好一条记录。
	order []item
	// index 是 order 的反查表（pos -> order 下标），与 order 严格双射。
	index map[core.BlockPos]int
	// lastAdvanceExamined 记录最近一次 Advance 从 order 里取出/探视的条目数。
	// 它是「单 tick 成本与队列规模解耦」这条结构性属性的可观测量，供
	// queue_bounded_test.go 直接断言；生产路径不读它。
	lastAdvanceExamined int
	// advanceExamineLimitHits 累计 advanceExamineLimit 这条守卫触发的次数，
	// 是它唯一的对外信号。索引堆下它**应当恒为 0**；大场景测试直接断言这一点，
	// 于是「守卫真的触发了」会表现成 CI 红灯，而不是生产里一声不响的吞吐损失。
	// 见 advanceExamineLimit 的注释。
	advanceExamineLimitHits int
}

// NewQueue 构造一个空的待更新队列。
func NewQueue() *Queue {
	return &Queue{index: make(map[core.BlockPos]int)}
}

// swapOrder 交换堆里两条记录，并同步两者的 index。
//
// 堆的所有元素移动都必须走这里：index 与 order 的双射不变量正是靠「移动即同步」
// 在每一步中间态保持成立的，任何直接对 q.order 做赋值的捷径都会把它悄悄破坏，
// 而破坏之后队列仍然「看起来能用」——只是某些位置永远取不出来。
func (q *Queue) swapOrder(i, j int) {
	q.order[i], q.order[j] = q.order[j], q.order[i]
	q.index[q.order[i].pos] = i
	q.index[q.order[j].pos] = j
}

// siftUp 把下标 child 处的记录上浮到满足堆序的位置。
func (q *Queue) siftUp(child int) {
	for child > 0 {
		parent := (child - 1) / 2
		if !lessItem(q.order[child], q.order[parent]) {
			return
		}
		q.swapOrder(child, parent)
		child = parent
	}
}

// siftDown 把下标 parent 处的记录下沉到满足堆序的位置。
func (q *Queue) siftDown(parent int) {
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
			return
		}
		q.swapOrder(parent, smallest)
		parent = smallest
	}
}

// pushOrder 把一条**新位置**的记录压入堆，代价 O(log len(order))。
// 调用方必须先确认 it.pos 不在 index 里，否则会破坏双射不变量。
func (q *Queue) pushOrder(it item) {
	q.order = append(q.order, it)
	q.index[it.pos] = len(q.order) - 1
	q.siftUp(len(q.order) - 1)
}

// popOrder 弹出并返回全序最小的记录，代价 O(log len(order))。
// 调用方必须先确认 len(q.order) > 0。
func (q *Queue) popOrder() item {
	last := len(q.order) - 1
	q.swapOrder(0, last)
	top := q.order[last]
	// 清掉尾槽再截断：item 不含指针，这一步不为 GC，而是避免截断后的备用
	// 容量里残留一份看起来合法的旧记录，误导调试。
	q.order[last] = item{}
	q.order = q.order[:last]
	delete(q.index, top.pos)
	if last > 0 {
		q.siftDown(0)
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
// 命中已在队的位置时是**就地改写那唯一一条记录**再上浮，绝不新增第二条——
// 这正是「过时条目在结构上不可能存在」的落点，见 Queue 的类型注释。
// dueTick 只可能变小，全序 lessItem 的首要键随之变小，因此只需上浮不需下沉。
func (q *Queue) Enqueue(pos core.BlockPos, now, delay uint64) {
	due := now + delay
	if i, ok := q.index[pos]; ok {
		if q.order[i].dueTick <= due {
			return
		}
		q.order[i].dueTick = due
		q.siftUp(i)
		return
	}
	q.pushOrder(item{pos: pos, dueTick: due})
}

// Clear 清空队列中的全部待更新项。
//
// 提供给调用方在重启/区块重新进入活动兴趣范围时，先清空再执行边界重扫——
// 队列不持久化，重扫是唯一的恢复路径（design.md D5）。
//
// order 与 index 必须一起清：只清其中一个会直接打破双射不变量。
func (q *Queue) Clear() {
	clear(q.index)
	q.order = q.order[:0]
}

// Len 返回当前排队的待更新项数。主要供测试与可观测性使用。
//
// 由双射不变量，len(index) 与 len(order) 恒等，取哪个都一样；这里取 index，
// 因为「一个位置在队列中当且仅当它是 index 的键」是这个类型对外的语义。
func (q *Queue) Len() int {
	return len(q.index)
}

// advanceExamineLimit 返回单次 Advance 允许探视的条目总数上界。
//
// 索引堆下这条上界**应当永不触发**（每次弹出都消耗一格预算，探视数自然封在
// budget+1 以内），它只是防止「有界性再次悄悄依赖某个前提」的廉价守卫。取
// 2*budget 而不是贴着 budget+1，是为了留出余量：贴太紧的话，任何将来正当的
// 微调都会让它变成一条静默截断处理量的暗雷，而不是守卫。它不是 tunable，
// 也不影响任何流体规则。
//
// **为什么保留一条永不触发的守卫**：本变更全程反复栽在同一个形态上——把某条
// 性质挂在一个「没有任何东西强制」的前提上，前提被别处的正当改动悄悄证伪，而
// 现场没有信号。这条守卫是那个形态的兜底：它不负责证明有界性（有界性由 Queue
// 的双射不变量给出），只负责在双射万一被破坏时不让有界性跟着一起无声地丢掉。
//
// **为什么它不 panic**：Advance 跑在权威 tick 上，服务端是世界与玩家状态的唯一
// 权威。在那里硬失败会让整局崩掉，比「本 tick 少处理几项」糟得多——后者只是短暂
// 的吞吐下降，流体规则、平衡态与存档都不受影响。所以生产路径只 break 并计数，
// 让 advanceExamineLimitHits 去承担报警：大场景测试断言它恒为 0，破坏会在 CI 上
// 变成红灯，而不是在玩家的服务器上变成一次静默降级。
//
// 溢出防御：budget 由调用方传入（测试里出现过 1<<24 这样的“不受限预算”），
// 2*budget 在极端取值下会翻负，翻负后 lastAdvanceExamined>=limit 会立刻为真、
// 一项都不处理——那是静默的功能失效而不是报错，所以这里显式饱和到 MaxInt。
func advanceExamineLimit(budget int) int {
	limit := 2 * budget
	if limit < budget {
		return math.MaxInt
	}
	return limit
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
//     （spec.md「预算不改变平衡态」）。除 budget 外还有一条无条件的探视上界
//     advanceExamineLimit，用来在过时条目堆积时封顶本 tick 的工作量；触发它的
//     效果与「本 tick 预算更小」完全一致，同样不丢项，见 Queue 的类型注释。
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
	examineLimit := advanceExamineLimit(budget)
	for processed := 0; processed < budget && len(q.order) > 0; {
		if q.lastAdvanceExamined >= examineLimit {
			// 改成索引堆之后这条**应当永不触发**：每次弹出都消耗一格预算，
			// processed<budget 自己就把探视数封在 budget+1 以内。保留它作为
			// 廉价的不变量守卫——万一将来有人重新引入「弹出后跳过、不消耗
			// 预算」的分支，有界性不至于跟着一起丢。
			// 它不再是有界性的依据，依据是双射不变量。
			//
			// 计数是这条守卫**唯一的对外信号**，必须留：一个永不触发、触发时
			// 又不留痕迹的守卫，在生产中与不存在等价，偏偏在最需要它说话的时候
			// 沉默——现场只会表现为轻微吞吐下降，没有任何线索。
			q.advanceExamineLimitHits++
			break
		}
		if q.order[0].dueTick > now {
			// 堆顶是全序最小项，它都没到期，后面的更不会到期：本 tick 到此
			// 为止。这一步是 O(1)，与队列里还压着多少项无关。
			q.lastAdvanceExamined++
			break
		}
		// popOrder 已经把该位置从 index 里删掉，即「取出即出队」。
		// 由双射不变量，弹出的每一条都是真实待更新项，不存在需要跳过的
		// 过时条目（见 Queue 的类型注释）。
		it := q.popOrder()
		q.lastAdvanceExamined++
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
