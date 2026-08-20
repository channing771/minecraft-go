package fluid

import (
	"sort"
	"testing"

	"github.com/channing771/mornlea/internal/core"
)

// TestAdvance_OnlyDueItemsProcessed 覆盖 spec Scenario「到期前不处理」：
// 第 T tick 入队、延迟为 D 的项，在 T+D 之前不被处理。
func TestAdvance_OnlyDueItemsProcessed(t *testing.T) {
	w := newMemWorld()
	pos := core.BlockPos{X: 0, Y: 10, Z: 0}
	w.SetBlock(pos, core.WaterLevel3ID)
	// 无支撑，一旦被处理就会消失为空气。

	q := NewQueue()
	q.Enqueue(pos, 0, 5) // T=0, delay=5 => dueTick=5

	for now := uint64(0); now < 5; now++ {
		changed := q.Advance(now, w, 100, 5)
		if len(changed) != 0 {
			t.Fatalf("now=%d: 未到期项不应被处理，got changed=%v", now, changed)
		}
		if w.BlockAt(pos) != core.WaterLevel3ID {
			t.Fatalf("now=%d: 未到期项不应改写世界", now)
		}
	}

	changed := q.Advance(5, w, 100, 5)
	if len(changed) != 1 || changed[0] != pos {
		t.Fatalf("now=5: 到期项应被处理，got changed=%v", changed)
	}
	if w.BlockAt(pos) != core.AirID {
		t.Fatalf("到期后应变为空气，got %v", w.BlockAt(pos))
	}
}

// TestAdvance_BudgetLimitsPerTickAndPreservesOrder 覆盖 spec Scenario
// 「预算限制单 tick 更新数」与「待更新项不因预算丢失」：
// 到期项数超过 budget 时，本 tick 只处理 budget 个，其余按原全序顺延，
// 不丢失。
func TestAdvance_BudgetLimitsPerTickAndPreservesOrder(t *testing.T) {
	w := newMemWorld()
	var positions []core.BlockPos
	for i := int32(0); i < 10; i++ {
		pos := core.BlockPos{X: i, Y: 10, Z: 0}
		w.SetBlock(pos, core.WaterLevel3ID) // 均无支撑，一处理就消失
		positions = append(positions, pos)
	}
	sort.Slice(positions, func(i, j int) bool { return lessPos(positions[i], positions[j]) })

	q := NewQueue()
	for _, pos := range positions {
		q.Enqueue(pos, 0, 0) // 全部立即到期
	}
	if got := q.Len(); got != 10 {
		t.Fatalf("入队后 Len()=%d，want 10", got)
	}

	first := q.Advance(0, w, 3, 5)
	if len(first) != 3 {
		t.Fatalf("budget=3 时本 tick 应只处理 3 个，got %d: %v", len(first), first)
	}
	for i, pos := range first {
		if pos != positions[i] {
			t.Fatalf("超预算时应按原全序处理前 budget 个，index %d: got %v want %v", i, pos, positions[i])
		}
	}
	// 未处理的 7 个仍在队列里（dueTick 不变，仍然到期）。
	remaining := 10 - len(first)
	if got := q.Len(); got < remaining {
		t.Fatalf("剩余未处理项不应从队列丢失，got Len()=%d, want >= %d", got, remaining)
	}
	for _, pos := range positions[3:] {
		if w.BlockAt(pos) != core.WaterLevel3ID {
			t.Fatalf("超预算未处理的格本 tick 不应被改写: %v", pos)
		}
	}

	// 继续推进直至队列耗尽，断言全部 10 个格最终都被处理为空气，不丢失。
	// now 必须随 tick 前进：Advance 内部把变化格以 dueTick=now+delay 重新
	// 入队，若 now 固定不变，超过 delay 的新入队项会永远到不了期，队列永远
	// 非空——这不是被测代码的 bug，是调用方（这里是测试自己）必须像真实
	// 权威 tick 一样递增 now。
	total := len(first)
	now := uint64(1)
	for q.Len() > 0 && total < 100 && now < 1000 {
		batch := q.Advance(now, w, 3, 5)
		total += len(batch)
		now++
	}
	if q.Len() != 0 {
		t.Fatalf("推进应最终耗尽队列，got Len()=%d after now=%d", q.Len(), now)
	}
	for _, pos := range positions {
		if w.BlockAt(pos) != core.AirID {
			t.Fatalf("推进耗尽队列后所有格都应变为空气，%v 仍为 %v", pos, w.BlockAt(pos))
		}
	}
}

// TestAdvance_SurvivalReadsTickStartSnapshot 覆盖 D4/design.md 提到的振荡
// 风险：同一 tick 内 A 消失、B 依赖 A 存活，B 的存活判定必须只看 tick 起始
// 状态（A 仍是流体），而不能看到 A 在本 tick 内被写成空气之后的状态。
//
// 场景：source -- level1(A, x=1) -- level2(B, x=2)。A 的水平邻居只有更弱的
// source(x=0，等级 0)与 B(等级 2)，上方为空；A 应当存活（等级0 < 1）。
// B 的水平邻居是 A(等级1)与空气；B 应当存活（等级1 < 2）。若判定意外用了
// tick 内已提交的写入，顺序不同会导致结果不同——这里断言两者本 tick 都不
// 应该消失。
func TestAdvance_SurvivalReadsTickStartSnapshot(t *testing.T) {
	w := newMemWorld()
	source := core.BlockPos{X: 0, Y: 10, Z: 0}
	a := core.BlockPos{X: 1, Y: 10, Z: 0}
	b := core.BlockPos{X: 2, Y: 10, Z: 0}
	w.SetBlock(source, core.WaterSourceID)
	w.SetBlock(a, core.WaterLevel1ID)
	w.SetBlock(b, core.WaterLevel2ID)
	// 全部下方实心，避免垂直传播干扰本测试要验证的存活判定。
	for _, p := range []core.BlockPos{source, a, b} {
		w.SetBlock(core.BlockPos{X: p.X, Y: p.Y - 1, Z: p.Z}, core.StoneID)
	}

	q := NewQueue()
	// 故意按「离源更远的先入队」的顺序入队，制造潜在的处理顺序敏感性。
	q.Enqueue(b, 0, 0)
	q.Enqueue(a, 0, 0)
	q.Enqueue(source, 0, 0)

	q.Advance(0, w, 100, 5)

	if w.BlockAt(a) != core.WaterLevel1ID {
		t.Fatalf("A 应因水平邻居 source(等级0) 存活，got %v", w.BlockAt(a))
	}
	if w.BlockAt(b) != core.WaterLevel2ID {
		t.Fatalf("B 应因水平邻居 A(等级1) 存活，got %v", w.BlockAt(b))
	}
}

// TestAdvance_ChangedCellsAndNeighborsRequeued 覆盖 spec 对「因流动产生变化
// 的格，其自身与六个邻居入队」的调度要求：一次垂直传播之后，写入的格与
// 其邻居都应重新出现在队列中，dueTick 反映了新的 delay。
func TestAdvance_ChangedCellsAndNeighborsRequeued(t *testing.T) {
	w := newMemWorld()
	pos := core.BlockPos{X: 0, Y: 10, Z: 0}
	w.SetBlock(pos, core.WaterSourceID)
	// 下方空气，触发垂直传播。

	q := NewQueue()
	q.Enqueue(pos, 0, 0)
	changed := q.Advance(0, w, 100, 5)

	below := core.BlockPos{X: 0, Y: 9, Z: 0}
	if len(changed) != 1 || changed[0] != below {
		t.Fatalf("本 tick 应只有 below 发生变化，got %v", changed)
	}
	wantDue := uint64(0 + 5)
	if got, ok := queuedDueTick(q, below); !ok || got != wantDue {
		t.Fatalf("变化格自身应重新入队，dueTick=%d，got ok=%v got=%d", wantDue, ok, got)
	}
	for _, n := range sixNeighbors(below) {
		if got, ok := queuedDueTick(q, n); !ok || got != wantDue {
			t.Fatalf("变化格的六邻应重新入队: %v, ok=%v got=%d", n, ok, got)
		}
	}
}

// TestAdvance_ConflictingWritesResolveToStrongest 覆盖 spec.md「同 tick
// 冲突写入取最强者」（评审 Ruling 7 补入）：mid 格同 tick 被两个不同强度的
// 传播源争抢——source(x=0) 传播出的等级 1，与 L2(x=2) 传播出的等级 3——必须
// 取更强（等级更小）的等级 1，而不是「处理次序中较晚者胜」。
//
// 地形（评审给出）：
//
//	source(x=0) | 空气(x=1,mid) | WaterLevel2(x=2) | WaterLevel1(x=3) | source(x=4)
//
// 全部下方实心，四个非空气格同 tick 到期。此前的版本（TestAdvance_
// ConflictingWritesResolveDeterministically）用两个产出同等级的源做冲突，
// 冲突双方值相同，合并策略无论「取最强」还是「较晚者胜」结果都一样，测不出
// C-1 那个 bug——这里换成等级不同的冲突，让两种策略产生可观测的差异。
func TestAdvance_ConflictingWritesResolveToStrongest(t *testing.T) {
	build := func() (w *memWorld, s0, mid, l2, l1, s4 core.BlockPos) {
		w = newMemWorld()
		s0 = core.BlockPos{X: 0, Y: 10, Z: 0}
		mid = core.BlockPos{X: 1, Y: 10, Z: 0}
		l2 = core.BlockPos{X: 2, Y: 10, Z: 0}
		l1 = core.BlockPos{X: 3, Y: 10, Z: 0}
		s4 = core.BlockPos{X: 4, Y: 10, Z: 0}
		w.SetBlock(s0, core.WaterSourceID)
		w.SetBlock(l2, core.WaterLevel2ID)
		w.SetBlock(l1, core.WaterLevel1ID)
		w.SetBlock(s4, core.WaterSourceID)
		for x := int32(0); x <= 4; x++ {
			w.SetBlock(core.BlockPos{X: x, Y: 9, Z: 0}, core.StoneID)
		}
		return
	}

	// run 用给定的入队顺序（对 [s0, l2, l1, s4] 的下标排列）跑一遍推进，
	// 返回 mid 的最终方块编号——用来验证结果与源格相对入队次序无关。
	run := func(order []int) core.BlockID {
		w, s0, mid, l2, l1, s4 := build()
		positions := []core.BlockPos{s0, l2, l1, s4}
		q := NewQueue()
		for _, i := range order {
			q.Enqueue(positions[i], 0, 0)
		}
		q.Advance(0, w, 100, 5)
		return w.BlockAt(mid)
	}

	forward := run([]int{0, 1, 2, 3})
	if forward != core.WaterLevel1ID {
		t.Fatalf("mid 应取更强的等级 1（来自 source），got %v", forward)
	}
	reversed := run([]int{3, 2, 1, 0})
	if reversed != forward {
		t.Fatalf("入队顺序不应影响结果：正序=%v 逆序=%v", forward, reversed)
	}
}
