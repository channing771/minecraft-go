package fluid

import (
	"runtime"
	"sort"
	"testing"

	"github.com/channing771/mornlea/internal/core"
)

// sortItems 就地按全序排序 items。
//
// 它曾经是 Queue.Advance 的生产实现（每 tick 遍历整张 pending、收集全部到期项
// 再整体排序），任务组 10b 把取批换成最小堆之后，生产路径不再需要它。这里刻意
// 把它保留在**测试侧**：它是 lessItem 全序的一份与最小堆完全独立的第二实现，
// queue_test.go 用它检查 lessItem 本身，本文件用它当「Advance 到底该取哪
// budget 项」的 oracle——两条独立实现互相对照，比让 Advance 自己跟自己对照
// 有意义得多。
func sortItems(items []item) {
	sort.Slice(items, func(i, j int) bool { return lessItem(items[i], items[j]) })
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
// **单 tick 触及的项数不随 len(pending) 增长**。
//
// 这是一条结构性属性，不是性能数值——它不问「比原来快多少」，只问「成本正比于
// 什么」。旧实现每 tick 无条件遍历整张 pending 并排序全部到期项，触及项数恒等于
// len(pending)；换成最小堆后只从堆顶取至多 budget 项，触及项数恒等于 budget。
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
		if _, still := q.pending[it.pos]; still {
			t.Fatalf("全序第 %d 小的到期项 %+v(due=%d) 没有被取走", i, it.pos, it.dueTick)
		}
	}
	// 反向：预算之外的到期项必须原样留在队列里、dueTick 不变。
	for _, it := range due[budget:] {
		got, still := q.pending[it.pos]
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

// TestAdvanceExaminedBoundedWhenDelayLowered 钉住 Advance 的**无条件探视上界**。
//
// 这条测试的存在理由是一条被证伪的前提：改动之初把有界性挂在「生产路径上不会出现
// 提前入队，故过时条目恒为 0」上，而 `sim.FluidFlowDelayTicks` 是 internal/config
// 里的实时可编辑项（Min 0 / Max 2000），运行中调小它就会让整张队列以更早的 dueTick
// 重新入队，把旧条目全部变成过时条目。过时条目在取批循环里走 continue、**不消耗
// 预算**，因此 processed<budget 这个条件拦不住它们。
//
// 夹具精确复现那个形态：
//
//  1. 以 delay=100 排入 5 万项（dueTick=100）；
//  2. 以 delay=1 对同样 5 万个位置重新入队（dueTick=1），旧的 5 万条 dueTick=100
//     条目全部变成过时条目，堆里此刻有 10 万条而队列只有 5 万项；
//  3. 用一个大预算把 5 万条真实项一次处理干净（全空气世界，无变更、无重新入队），
//     pending 清空，堆里只剩那 5 万条过时条目；
//  4. 推进到 now=100，此时那 5 万条过时条目**全部到期**，且没有任何真实项与它们
//     争预算——没有上界的话，单个 Advance 会一口气把它们全弹掉。
//
// 断言的是**位置性**：单 tick 的探视数落在 2*budget 这个常数内，而不是「有上界就行」。
// 同时断言过时条目会被**逐 tick 排空**（popOrder 是真删除，不会饿死），以及排空
// 过程中真实队列内容一项不少。
func TestAdvanceExaminedBoundedWhenDelayLowered(t *testing.T) {
	const stalePositions = 50_000
	const budget = 8

	w := newMemWorld()
	q := NewQueue()
	for i := range stalePositions {
		q.Enqueue(boundedPos(i), 0, 100) // dueTick=100，稍后被降级为过时条目
	}
	for i := range stalePositions {
		q.Enqueue(boundedPos(i), 0, 1) // dueTick=1，delay 被调小的那一刻
	}
	heapAfterLowering := len(q.order)

	// 把真实项全部处理掉，只留过时条目。
	q.Advance(1, w, stalePositions, 1)
	staleLeft := len(q.order)
	realLeft := q.Len()

	// 真实故障断言：过时条目全部到期的那个 tick，探视数必须落在常数上界内。
	q.Advance(100, w, budget, 1)
	if got, limit := q.lastAdvanceExamined, 2*budget; got > limit {
		t.Fatalf("过时条目堆积时单 tick 探视 %d 项，超过无条件上界 %d（堆里还有 %d 条）",
			got, limit, staleLeft)
	}

	// 过时条目必须被逐 tick 真删除、有限步排空，不会饿死后续真实项。
	ticks := 0
	for len(q.order) > 0 {
		q.Advance(100, w, budget, 1)
		ticks++
		if ticks > stalePositions {
			t.Fatalf("过时条目在 %d 个 tick 内没有排空，剩余 %d 条：popOrder 可能不是真删除",
				ticks, len(q.order))
		}
	}
	if q.Len() != 0 {
		t.Fatalf("排空过时条目的过程中队列内容被改动：Len=%d，want 0", q.Len())
	}

	// 夹具前提守卫排在真实断言之后。
	if heapAfterLowering != 2*stalePositions {
		t.Fatalf("下调 delay 没有产生预期的过时条目：堆 %d 条，want %d（Enqueue 的提前分支可能没走到）",
			heapAfterLowering, 2*stalePositions)
	}
	if realLeft != 0 || staleLeft < stalePositions {
		t.Fatalf("夹具没进入「只剩过时条目」的状态：真实项 %d（want 0）、堆 %d 条（want ≥%d）",
			realLeft, staleLeft, stalePositions)
	}
	if staleLeft <= 2*budget {
		t.Fatalf("过时条目只有 %d 条，不超过上界 %d，上界没被真正触发",
			staleLeft, 2*budget)
	}
	t.Logf("过时条目 %d 条，单 tick 探视 %d 项（上界 %d），%d 个 tick 排空",
		staleLeft, q.lastAdvanceExamined, 2*budget, ticks)
}
