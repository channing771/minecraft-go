package fluid

import (
	"math/rand"
	"testing"

	"github.com/channing771/mornlea/internal/core"
)

// buildItems 构造一组分散在不同区块、不同坐标、不同 dueTick 的待更新项，
// 用于全序与去重测试。
func buildItems() []item {
	return []item{
		{pos: core.BlockPos{X: 0, Y: 10, Z: 0}, dueTick: 5},
		{pos: core.BlockPos{X: 1, Y: 10, Z: 0}, dueTick: 5},
		{pos: core.BlockPos{X: 20, Y: 10, Z: 0}, dueTick: 5}, // 不同区块（X>>4 不同）
		{pos: core.BlockPos{X: 0, Y: 11, Z: 0}, dueTick: 5},
		{pos: core.BlockPos{X: 0, Y: 10, Z: 1}, dueTick: 3}, // 更早的 dueTick 排最前
		{pos: core.BlockPos{X: -5, Y: 10, Z: 0}, dueTick: 5},
		{pos: core.BlockPos{X: 0, Y: 10, Z: -20}, dueTick: 5},
	}
}

// TestItemOrderIsTotalAndStableUnderShuffle 断言 (dueTick, ChunkKey 近似,
// y, z, x) 全序与打乱输入顺序无关：任意打乱同一组待更新项后排序，结果都
// 是同一个序列。对应 task-2-brief.md 2.3「排序是全序」。
func TestItemOrderIsTotalAndStableUnderShuffle(t *testing.T) {
	base := buildItems()
	want := append([]item(nil), base...)
	sortItems(want)

	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 20; trial++ {
		shuffled := append([]item(nil), base...)
		rng.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		sortItems(shuffled)
		for i := range want {
			if shuffled[i] != want[i] {
				t.Fatalf("trial %d: 打乱后排序结果不一致，index %d: got %+v want %+v", trial, i, shuffled[i], want[i])
			}
		}
	}
}

// TestItemOrderIsIrreflexiveAndConsistent 断言 lessItem 是严格弱序：不存在
// a<b 且 b<a 同时成立，且对同一元素 a<a 恒为 false。
func TestItemOrderIsIrreflexiveAndConsistent(t *testing.T) {
	items := buildItems()
	for i := range items {
		if lessItem(items[i], items[i]) {
			t.Fatalf("lessItem 对自身必须返回 false: %+v", items[i])
		}
		for j := range items {
			if i == j {
				continue
			}
			if lessItem(items[i], items[j]) && lessItem(items[j], items[i]) {
				t.Fatalf("lessItem 不能同时判定 a<b 与 b<a: %+v, %+v", items[i], items[j])
			}
		}
	}
}

// TestEnqueueDedupKeepsEarliestDueTick 覆盖「去重可以用 map，但重复入队保留
// 更早的 dueTick」：同一格先后以不同 delay 入队两次，最终应保留更早到期
// 的那一次，且不重复计数。
func TestEnqueueDedupKeepsEarliestDueTick(t *testing.T) {
	q := NewQueue()
	pos := core.BlockPos{X: 0, Y: 10, Z: 0}
	q.Enqueue(pos, 0, 10) // due=10
	q.Enqueue(pos, 0, 3)  // due=3，更早
	if got := q.Len(); got != 1 {
		t.Fatalf("同一格重复入队应去重为 1 项，got %d", got)
	}
	if got, ok := queuedDueTick(q, pos); !ok || got != 3 {
		t.Fatalf("去重后应保留更早的 dueTick=3，got ok=%v got=%d", ok, got)
	}

	q.Enqueue(pos, 0, 100) // due=100，更晚，不应覆盖已保留的更早值
	if got, ok := queuedDueTick(q, pos); !ok || got != 3 {
		t.Fatalf("更晚的重复入队不应推迟已排定的 dueTick，got ok=%v got=%d", ok, got)
	}
}

// TestEnqueueDistinctPositionsNotLost 覆盖「去重后不丢项」：入队多个不同的
// 格，Len 应等于不同格的数量。
func TestEnqueueDistinctPositionsNotLost(t *testing.T) {
	q := NewQueue()
	items := buildItems()
	for _, it := range items {
		q.Enqueue(it.pos, 0, it.dueTick)
	}
	if got := q.Len(); got != len(items) {
		t.Fatalf("Len()=%d，want %d：入队项不应丢失", got, len(items))
	}
}

// TestClearEmptiesQueue 覆盖「从外部清空队列」这一 API 面（下个任务组的重扫
// 前置需要它）。
func TestClearEmptiesQueue(t *testing.T) {
	q := NewQueue()
	q.Enqueue(core.BlockPos{X: 0, Y: 10, Z: 0}, 0, 5)
	q.Enqueue(core.BlockPos{X: 1, Y: 10, Z: 0}, 0, 5)
	q.Clear()
	if got := q.Len(); got != 0 {
		t.Fatalf("Clear 后 Len() 应为 0，got %d", got)
	}
}
