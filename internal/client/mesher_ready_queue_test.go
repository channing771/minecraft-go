package client

import (
	"reflect"
	"testing"

	"github.com/channing771/mornlea/internal/core"
)

func TestReadySectionHeapOrdersAndDeduplicates(t *testing.T) {
	ready := newReadySectionHeap()
	keys := []core.SectionKey{
		{Dimension: 1, Pos: core.SectionPos{X: 0, Y: 0, Z: 0}},
		{Dimension: core.Overworld, Pos: core.SectionPos{X: 1, Y: 0, Z: 0}},
		{Dimension: core.Overworld, Pos: core.SectionPos{X: 0, Y: 1, Z: 0}},
		{Dimension: core.Overworld, Pos: core.SectionPos{X: 0, Y: 0, Z: 1}},
		{Dimension: core.Overworld, Pos: core.SectionPos{X: 0, Y: 0, Z: 0}},
	}
	for _, key := range keys {
		if !ready.Add(key) {
			t.Fatalf("首次 Add(%+v) = false", key)
		}
	}
	if ready.Add(keys[2]) {
		t.Fatal("重复 Add 返回 true")
	}
	want := []core.SectionKey{keys[4], keys[2], keys[3], keys[1], keys[0]}
	got := make([]core.SectionKey, 0, len(want))
	for {
		key, ok := ready.Take()
		if !ok {
			break
		}
		got = append(got, key)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Take 顺序 = %+v，想要 %+v", got, want)
	}
}

func TestReadySectionHeapRemoveMaintainsIndexes(t *testing.T) {
	ready := newReadySectionHeap()
	left := core.SectionKey{Dimension: core.Overworld, Pos: core.SectionPos{X: -1}}
	middle := core.SectionKey{Dimension: core.Overworld, Pos: core.SectionPos{}}
	right := core.SectionKey{Dimension: core.Overworld, Pos: core.SectionPos{X: 1}}
	for _, key := range []core.SectionKey{middle, right, left} {
		ready.Add(key)
	}
	if !ready.Remove(middle) || ready.Remove(middle) {
		t.Fatal("Remove 未精确报告存在性")
	}
	if !ready.Add(middle) || !ready.Remove(left) {
		t.Fatal("删除后重新添加或交换索引失败")
	}
	first, ok := ready.Take()
	if !ok || first != middle {
		t.Fatalf("首项 = %+v,%v，想要 middle", first, ok)
	}
	second, ok := ready.Take()
	if !ok || second != right {
		t.Fatalf("次项 = %+v,%v，想要 right", second, ok)
	}
}
