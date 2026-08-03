package core_test

import (
	"math"
	"testing"

	"minecraft-go/internal/core"
)

func TestDropsPerChunkIsFixed(t *testing.T) {
	if core.DropsPerChunk != 32 {
		t.Fatalf("DropsPerChunk = %d，契约要求 32", core.DropsPerChunk)
	}
}

func TestDropIDValid(t *testing.T) {
	valid := core.DropID{
		Dimension: core.Overworld, Chunk: core.ChunkPos{X: 1, Z: -2},
		Slot: core.DropsPerChunk - 1, Generation: 1,
	}
	if !valid.Valid() {
		t.Fatalf("合法 DropID 被拒绝: %+v", valid)
	}

	cases := []struct {
		name string
		id   core.DropID
	}{
		{"越界槽位", core.DropID{Slot: core.DropsPerChunk, Generation: 1}},
		{"零 generation", core.DropID{Slot: 0, Generation: 0}},
	}
	for _, tc := range cases {
		if tc.id.Valid() {
			t.Fatalf("%s：非法 DropID 被接受: %+v", tc.name, tc.id)
		}
	}
}

func TestDropIDCompareIsStable(t *testing.T) {
	base := core.DropID{
		Dimension: core.Overworld, Chunk: core.ChunkPos{X: 1, Z: 1},
		Slot: 2, Generation: 3,
	}
	if got := base.Compare(base); got != 0 {
		t.Fatalf("自比较 = %d，想要 0", got)
	}

	greater := []core.DropID{
		{Dimension: core.Overworld + 1, Chunk: core.ChunkPos{X: 1, Z: 1}, Slot: 2, Generation: 3},
		{Dimension: core.Overworld, Chunk: core.ChunkPos{X: 2, Z: 1}, Slot: 2, Generation: 3},
		{Dimension: core.Overworld, Chunk: core.ChunkPos{X: 1, Z: 2}, Slot: 2, Generation: 3},
		{Dimension: core.Overworld, Chunk: core.ChunkPos{X: 1, Z: 1}, Slot: 3, Generation: 3},
		{Dimension: core.Overworld, Chunk: core.ChunkPos{X: 1, Z: 1}, Slot: 2, Generation: 4},
	}
	for index, id := range greater {
		if base.Compare(id) >= 0 {
			t.Fatalf("字段 %d：base.Compare(%+v) = %d，想要负数", index, id, base.Compare(id))
		}
		if id.Compare(base) <= 0 {
			t.Fatalf("字段 %d：反向比较不对称", index)
		}
	}
}

func TestDropIDCompareOrdersNegativeChunks(t *testing.T) {
	left := core.DropID{Chunk: core.ChunkPos{X: -3, Z: 0}, Slot: 0, Generation: 1}
	right := core.DropID{Chunk: core.ChunkPos{X: 1, Z: 0}, Slot: 0, Generation: 1}
	if left.Compare(right) >= 0 {
		t.Fatalf("负数区块坐标排序错误: %d", left.Compare(right))
	}
}

func TestDropIDGenerationUpperBoundIsValid(t *testing.T) {
	id := core.DropID{Slot: 0, Generation: math.MaxUint32}
	if !id.Valid() {
		t.Fatal("generation 上限值应当仍是合法 ID")
	}
}
