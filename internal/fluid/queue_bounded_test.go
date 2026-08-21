package fluid

import (
	"runtime"
	"sort"
	"testing"

	"github.com/channing771/mornlea/internal/core"
)

// sortItems 就地按全序排序 items。
//
// 它曾经是 Queue.Advance 的生产实现（每 tick 遍历整张队列内容、收集全部到期项
// 再整体排序），任务组 10b 把取批换成最小堆之后，生产路径不再需要它。这里刻意
// 把它保留在**测试侧**：它是 lessItem 全序的一份与最小堆完全独立的第二实现，
// queue_test.go 用它检查 lessItem 本身，本文件用它当「Advance 到底该取哪
// budget 项」的 oracle——两条独立实现互相对照，比让 Advance 自己跟自己对照
// 有意义得多。
func sortItems(items []item) {
	sort.Slice(items, func(i, j int) bool { return lessItem(items[i], items[j]) })
}

// queuedDueTick 读出 pos 当前排定的 dueTick，是**表示层**的测试助手。
//
// 任务组 10b 修复轮 2 之前，队列内容存放在 Queue.pending（map[BlockPos]uint64），
// 测试直接写 q.pending[pos] 就能拿到 dueTick。改成索引最小堆之后，dueTick 的唯一
// 存放处变成 q.order[q.index[pos]].dueTick——所有既有白盒断言想问的还是同一个问题
// 「这个位置排定在哪个 tick」，只是问法要跟着表示走，故统一收敛到这个助手。
func queuedDueTick(q *Queue, pos core.BlockPos) (uint64, bool) {
	i, ok := q.index[pos]
	if !ok {
		return 0, false
	}
	return q.order[i].dueTick, true
}

// boundedPos 把下标 i 映射到互不相同、跨多个区块分布的方块坐标。
// (x, y, z) 三个分量分别取 i 的不同位段，因此该映射是单射：不同的 i 一定得到
// 不同的坐标，队列规模才真的等于入队次数。
func boundedPos(i int) core.BlockPos {
	return core.BlockPos{
		X: int32(i%64) - 32,
		Y: int32((i / 64) % 64),
		Z: int32(i/4096) - 32,
	}
}

// TestAdvanceExaminedItemsIndependentOfQueueSize 是任务组 10b 的核心断言：
// **单 tick 触及的项数不随队列规模增长**。
//
// 这是一条结构性属性，不是性能数值——它不问「比原来快多少」，只问「成本正比于
// 什么」。旧实现每 tick 无条件遍历整张队列内容并排序全部到期项，触及项数恒等于
// 队列项数；换成最小堆后只从堆顶取至多 budget 项，触及项数恒等于 budget。
//
// 用两条互相独立的证据同时钉住，避免「自己写的计数器自己恒真」：
//
//  1. Queue.lastAdvanceExamined——直接、精确，但它是本次改动自带的可观测量；
//  2. 单次 Advance 分配的字节数——与实现无关的外部信号。旧实现要把全部到期项
//     收集进一个切片，队列 25 万项时那是数 MB 级的分配；堆实现只分配与 budget
//     同阶的几个小对象。任何「退回全量遍历并收集」的实现都会让这一条爆掉，哪怕
//     它把计数器写成常数。
//
// 夹具全是空气格：evalCell 对非流体格恒产出空写入，处理阶段的成本因此在两种
// 规模下完全相同，观测到的差异只可能来自「怎么取出下一批」这一步。
func TestAdvanceExaminedItemsIndependentOfQueueSize(t *testing.T) {
	const budget = 8
	const smallQueue = 1_000
	const largeQueue = 250_000

	measure := func(n int) (examined int, allocated uint64, queued int) {
		w := newMemWorld()
		q := NewQueue()
		for i := range n {
			q.Enqueue(boundedPos(i), 0, 1)
		}
		queued = q.Len()

		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		changed := q.Advance(1, w, budget, 1)
		runtime.ReadMemStats(&after)
		if len(changed) != 0 {
			t.Fatalf("全空气夹具不应产生任何变更，got %v", changed)
		}
		// TotalAlloc 是自进程启动以来的累计分配量，GC 不会把它减回去，
		// 因此这个差值就是本次 Advance 自己分配的字节数。
		return q.lastAdvanceExamined, after.TotalAlloc - before.TotalAlloc, queued
	}

	smallExamined, smallAlloc, smallQueued := measure(smallQueue)
	largeExamined, largeAlloc, largeQueued := measure(largeQueue)

	if smallExamined != budget || largeExamined != budget {
		t.Fatalf("单 tick 触及项数应恒为预算 %d，实测 %d 项队列=%d、%d 项队列=%d",
			budget, smallQueue, smallExamined, largeQueue, largeExamined)
	}

	// 1 MiB 的松弛量远大于堆实现的实际分配（几 KiB），又远小于全量收集实现在
	// 25 万项上要付的数 MB，因此这条阈值既不会因噪声抖动，也拦得住回归。
	const allocSlack = 1 << 20
	if largeAlloc > smallAlloc+allocSlack {
		t.Fatalf("单 tick 分配量随队列规模增长：%d 项队列分配 %d B，%d 项队列分配 %d B（松弛 %d B）",
			smallQueue, smallAlloc, largeQueue, largeAlloc, allocSlack)
	}

	// 夹具前提守卫排在真实断言之后：先让真故障有机会以自己的信息失败，
	// 再检查夹具本身是否真的把两种规模拉开了。
	if smallQueued != smallQueue || largeQueued != largeQueue {
		t.Fatalf("夹具入队数与预期不符：small=%d/%d large=%d/%d（坐标映射可能不是单射）",
			smallQueued, smallQueue, largeQueued, largeQueue)
	}
	if largeQueued < 200_000 || largeQueued < 100*smallQueued {
		t.Fatalf("大队列夹具没把规模真正拉开：small=%d large=%d，两种实现的复杂度行为区分不出来",
			smallQueued, largeQueued)
	}
	t.Logf("触及项数 %d（预算 %d）；分配 small=%d B large=%d B；队列 %d / %d 项",
		largeExamined, budget, smallAlloc, largeAlloc, smallQueued, largeQueued)
}

// TestAdvanceTakesGloballySmallestDueItemsAtScale 断言「换了取批结构之后，取出
// 的仍然是全序下最小的那 budget 个到期项」——用 sortItems 这份独立实现当 oracle。
//
// 夹具刻意做成**大队列 + 混合 dueTick**：
//
//   - 大队列（4 万项）保证小队列下两种实现偶然一致这件事不能蒙混过关；
//   - 一部分项 dueTick > now，它们哪怕位置排在最前也绝不能被取走，从而把
//     「先按 dueTick 过滤，再按位置排序」和「只按位置排序」区分开。
//
// 断言的是**位置性**（取走的恰好是 oracle 的前 budget 个）而不是存在性
// （取走了 budget 个）：后者在任何「取够数就行」的错误实现下同样成立。
func TestAdvanceTakesGloballySmallestDueItemsAtScale(t *testing.T) {
	const queueSize = 40_000
	const budget = 137
	const now = uint64(10)

	w := newMemWorld()
	q := NewQueue()
	all := make([]item, 0, queueSize)
	notDue := 0
	for i := range queueSize {
		pos := boundedPos(i)
		// 用下标的位混出一个与坐标全序**不相关**的 dueTick 分布：
		// 若 dueTick 与位置同序，「只按位置排」的错误实现会碰巧通过。
		due := uint64((i*2654435761)>>13) % 16
		if due > now {
			notDue++
		}
		q.Enqueue(pos, 0, due)
		all = append(all, item{pos: pos, dueTick: due})
	}

	// oracle：先滤到期项，再用独立实现的全序排序，取前 budget 个。
	due := make([]item, 0, len(all))
	for _, it := range all {
		if it.dueTick <= now {
			due = append(due, it)
		}
	}
	sortItems(due)
	want := due[:budget]

	before := q.Len()
	changed := q.Advance(now, w, budget, 1)
	if len(changed) != 0 {
		t.Fatalf("全空气夹具不应产生任何变更，got %v", changed)
	}
	if got := before - q.Len(); got != budget {
		t.Fatalf("本 tick 应恰好取走 %d 项，实际队列 %d→%d", budget, before, q.Len())
	}
	for i, it := range want {
		if _, still := queuedDueTick(q, it.pos); still {
			t.Fatalf("全序第 %d 小的到期项 %+v(due=%d) 没有被取走", i, it.pos, it.dueTick)
		}
	}
	// 反向：预算之外的到期项必须原样留在队列里、dueTick 不变。
	for _, it := range due[budget:] {
		got, still := queuedDueTick(q, it.pos)
		if !still {
			t.Fatalf("超出预算的到期项 %+v 被丢弃了", it.pos)
		}
		if got != it.dueTick {
			t.Fatalf("超出预算的到期项 %+v 的 dueTick 被改动：%d，want %d", it.pos, got, it.dueTick)
		}
	}

	// 夹具前提守卫排在真实断言之后。
	if notDue == 0 {
		t.Fatalf("夹具里没有任何未到期项，「按 dueTick 过滤」这一半没被覆盖")
	}
	if len(due) <= budget {
		t.Fatalf("到期项只有 %d 个，不超过预算 %d，预算截断没被覆盖", len(due), budget)
	}
	if queueSize < 10_000 {
		t.Fatalf("夹具队列只有 %d 项，太小，区分不出复杂度行为", queueSize)
	}
}

// TestAdvanceExaminedBoundedWhenDelayLowered 钉住「下调 delay 不再产生过时条目」。
//
// 这条测试的历史：10b 初版用惰性删除堆，Enqueue 把已在队的位置改到更早的 dueTick
// 时只能再压一条，旧那条成为过时条目。评审建的反例是 sim.FluidFlowDelayTicks
// （internal/config 的实时可编辑项，Min 0 / Max 2000）被调小——delay=100 排入
// 5 万项后下调，堆里积下 5 万条过时条目，单个 Advance 会一口气把它们全弹掉。
// 修复轮 2 换成索引最小堆后，过时条目在**表示上就不存在**，本测试守这一点。
//
// 两条真实故障断言，都是**位置性**的：
//
//  1. 结构：下调 delay 之后，堆里的记录数必须恰好等于队列项数。惰性删除堆在这里
//     是 2 倍（10 万 vs 5 万）。
//  2. 行为：把队列一路推进到**堆**排空，每一个 tick 的探视数都不得超过
//     budget+1。惰性删除堆在旧 dueTick 到期后会连续多个 tick 只弹过时条目，
//     探视数顶到探视上界 2*budget。
//
// 循环条件刻意用 len(q.order)>0 而不是 q.Len()>0：前者才能把「队列已空但堆里还
// 压着一堆记录」这种状态暴露出来，后者会在那之前就退出，让断言空转。
func TestAdvanceExaminedBoundedWhenDelayLowered(t *testing.T) {
	const positions = 50_000
	const budget = 8

	w := newMemWorld()
	q := NewQueue()
	for i := range positions {
		q.Enqueue(boundedPos(i), 0, 100) // dueTick=100
	}
	for i := range positions {
		q.Enqueue(boundedPos(i), 0, 1) // delay 被调小的那一刻：dueTick 改到 1
	}

	if heapEntries, queued := len(q.order), q.Len(); heapEntries != queued {
		t.Fatalf("下调 delay 在堆里留下了多余记录：堆 %d 条、队列 %d 项——过时条目又回来了",
			heapEntries, queued)
	}

	worst, ticks := 0, 0
	for len(q.order) > 0 {
		q.Advance(uint64(1+ticks), w, budget, 1)
		if q.lastAdvanceExamined > worst {
			worst = q.lastAdvanceExamined
		}
		ticks++
		if ticks > 4*positions {
			t.Fatalf("队列在 %d 个 tick 内没有排空，堆里还剩 %d 条", ticks, len(q.order))
		}
	}
	if limit := budget + 1; worst > limit {
		t.Fatalf("排空过程中单 tick 最大探视 %d 项，超过 budget+1=%d", worst, limit)
	}
	if q.Len() != 0 {
		t.Fatalf("堆已空但 index 还剩 %d 项：双射不变量被破坏", q.Len())
	}

	// 夹具前提守卫排在真实断言之后。
	if positions <= budget+1 {
		t.Fatalf("夹具只有 %d 项，不超过 budget+1=%d，上界断言是空转", positions, budget+1)
	}
	if ticks < positions/budget {
		t.Fatalf("只用了 %d 个 tick 就排空 %d 项（预算 %d），夹具没真正走完预算受限的推进",
			ticks, positions, budget)
	}
	t.Logf("下调 delay 后堆 %d 条 == 队列 %d 项；%d 个 tick 排空，单 tick 最大探视 %d 项（上界 %d）",
		positions, positions, ticks, worst, budget+1)
}

// TestOrderIndependenceSurvivesDelayLowering 把 spec.md
// 「推进顺序 MUST NOT 依赖待更新项的入队顺序」这条**无条件** MUST 钉在
// 「delay 跨 tick 被下调」这个角落里。
//
// 为什么需要单独一条：既有的 TestOrderIndependence_PerTickChangesMatch 全程用
// 同一个 delay，任何「同一位置先后以不同 delay 入队」的路径都不会被走到，因此
// 它对本角落什么也没说。而 sim.FluidFlowDelayTicks 是 internal/config 的实时可
// 编辑项（Min 0 / Max 2000），下调它就会让同一位置以更早的 dueTick 再次入队。
//
// 夹具：同一批位置各以 delay=5 与 delay=0 入队一次，两种调用次序**得到完全相同
// 的队列内容**（每个位置 dueTick 都是 0，因为 Enqueue 保留更早者），只有 Enqueue
// 的先后不同。随后以相同预算推进相同 tick 数，逐 tick 比较变更集合。
//
// 断言的是**位置性**：每一个 tick 的变更集合逐格一致，而不是「最终状态一致」——
// 后者在推进次序被打乱时仍可能靠收敛性蒙混过关。
func TestOrderIndependenceSurvivesDelayLowering(t *testing.T) {
	const budget = 64
	const ticks = 200

	run := func(lowerFirst bool) [][]core.BlockPos {
		w := newBasin(0, 0, 19, 19, 0, 16)
		fillBox(w, 0, 1, 0, 4, 12, 19, core.WaterSourceID)
		q := NewQueue()
		seeds := fluidPositions(w)
		enqueueAll := func(delay uint64) {
			for _, pos := range seeds {
				q.Enqueue(pos, 0, delay)
			}
		}
		if lowerFirst {
			enqueueAll(0)
			enqueueAll(5)
		} else {
			enqueueAll(5)
			enqueueAll(0)
		}
		out := make([][]core.BlockPos, 0, ticks)
		for now := uint64(0); now < ticks; now++ {
			changed := q.Advance(now, w, budget, 0)
			out = append(out, append([]core.BlockPos(nil), changed...))
		}
		return out
	}

	a, b := run(false), run(true)
	for tick := range a {
		if len(a[tick]) != len(b[tick]) {
			t.Fatalf("第 %d tick 变更数不一致：先 delay=5 得 %d 处，先 delay=0 得 %d 处——"+
				"两次运行的队列内容完全相同，推进顺序却依赖了 Enqueue 的调用次序",
				tick, len(a[tick]), len(b[tick]))
		}
		for i := range a[tick] {
			if a[tick][i] != b[tick][i] {
				t.Fatalf("第 %d tick 第 %d 项变更不一致：%+v vs %+v", tick, i, a[tick][i], b[tick][i])
			}
		}
	}

	// 夹具前提守卫排在真实断言之后。
	total := 0
	for _, changed := range a {
		total += len(changed)
	}
	if total == 0 {
		t.Fatalf("%d tick 内没有任何变更，逐 tick 断言是空转", ticks)
	}
	t.Logf("%d tick 共 %d 处变更，两种入队次序逐 tick 一致", ticks, total)
}
