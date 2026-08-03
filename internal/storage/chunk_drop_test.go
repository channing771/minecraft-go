package storage

import (
	"bytes"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

func dropFixtureChunk(t *testing.T, pos core.ChunkPos) *world.Chunk {
	t.Helper()
	chunk := codecFixtureChunk(pos)
	index, ok := world.ChunkBlockIndex(core.BlockPos{
		X: pos.X<<core.SectionShift + 3, Y: 5, Z: pos.Z<<core.SectionShift + 9,
	})
	if !ok {
		t.Fatal("固定测试方块没有区块索引")
	}
	chunk.SetDrop(0, world.DropSlot{
		Generation: 4, Active: true,
		Stack:      core.ItemStack{Item: core.ItemStone, Count: 17},
		BlockIndex: index, AgeTicks: 123, PickupDelayTicks: 7,
	})
	chunk.SetDrop(31, world.DropSlot{
		Generation: 9, Active: true,
		Stack:      core.ItemStack{Item: core.ItemGrass, Count: core.MaxStackCount},
		BlockIndex: index, AgeTicks: 5999, PickupDelayTicks: 0,
	})
	// 非活动槽仍保留 generation，避免复用时重复分配旧 ID。
	chunk.SetDrop(5, world.DropSlot{Generation: 2})
	return chunk
}

func TestChunkCodecRoundTripsDrops(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	want := dropFixtureChunk(t, key.Pos)
	encoded, err := encodeChunkPayload(ChunkSave{Key: key, Revision: 19, Chunk: want})
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeChunkPayload(key, 19, encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got.Chunk.Hash() != want.Hash() {
		t.Fatal("方块状态在往返后改变")
	}
	if got.Chunk.DropsHash() != want.DropsHash() {
		t.Fatal("掉落物状态在往返后改变")
	}
	for slot := range core.DropsPerChunk {
		if got.Chunk.Drop(slot) != want.Drop(slot) {
			t.Fatalf("槽 %d = %+v，想要 %+v", slot, got.Chunk.Drop(slot), want.Drop(slot))
		}
	}
	if got.Schema != currentChunkSchema {
		t.Fatalf("schema = %d，想要 %d", got.Schema, currentChunkSchema)
	}
}

func TestChunkV2Fixture(t *testing.T) {
	want := dropFixtureChunk(t, core.ChunkPos{X: -3, Z: 7})
	encoded, err := encodeChunkPayload(ChunkSave{
		Key:      core.ChunkKey{Dimension: core.Overworld, Pos: want.Pos},
		Revision: 19,
		Chunk:    want,
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "chunk-v2.bin")
	if *updateStorageFixtures {
		if err := os.WriteFile(path, encoded, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, encoded) {
		t.Fatal("v2 fixture drift; change schema version")
	}
}

func TestChunkV1FixtureMigratesToEmptyDrops(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	encoded, err := os.ReadFile(filepath.Join("testdata", "chunk-v1.bin"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeChunkPayload(key, 19, encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got.Chunk.Hash() != codecFixtureChunk(key.Pos).Hash() {
		t.Fatal("v1 迁移改变了方块状态")
	}
	empty := world.NewChunk(key.Pos)
	if got.Chunk.DropsHash() != empty.DropsHash() {
		t.Fatal("v1 迁移必须得到空掉落物集合")
	}
	if got.Schema != currentChunkSchema {
		t.Fatalf("迁移后 schema = %d，想要 %d", got.Schema, currentChunkSchema)
	}
}

func TestChunkCodecRejectsInvalidDropSlots(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	index, ok := world.ChunkBlockIndex(core.BlockPos{
		X: key.Pos.X << core.SectionShift, Y: 5, Z: key.Pos.Z << core.SectionShift,
	})
	if !ok {
		t.Fatal("固定测试方块没有区块索引")
	}
	cases := []struct {
		name string
		slot world.DropSlot
	}{
		{"活动槽零 generation", world.DropSlot{
			Active: true, Stack: core.ItemStack{Item: core.ItemStone, Count: 1}, BlockIndex: index,
		}},
		{"未知物品", world.DropSlot{
			Generation: 1, Active: true,
			Stack: core.ItemStack{Item: core.ItemID(4242), Count: 1}, BlockIndex: index,
		}},
		{"零数量", world.DropSlot{
			Generation: 1, Active: true,
			Stack: core.ItemStack{Item: core.ItemStone}, BlockIndex: index,
		}},
		{"数量超限", world.DropSlot{
			Generation: 1, Active: true,
			Stack:      core.ItemStack{Item: core.ItemStone, Count: core.MaxStackCount + 1},
			BlockIndex: index,
		}},
		{"越界方块位置", world.DropSlot{
			Generation: 1, Active: true,
			Stack:      core.ItemStack{Item: core.ItemStone, Count: 1},
			BlockIndex: core.SectionsPerChunk * core.BlocksPerSection,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chunk := codecFixtureChunk(key.Pos)
			chunk.SetDrop(1, tc.slot)
			if _, err := encodeChunkPayload(ChunkSave{
				Key: key, Revision: 19, Chunk: chunk,
			}); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("编码非法槽 error = %v，想要 ErrCorrupt", err)
			}
		})
	}
}

func TestChunkCodecRejectsWrongDropSlotCount(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	logical, err := encodeLogicalChunk(ChunkSave{
		Key: key, Revision: 19, Chunk: dropFixtureChunk(t, key.Pos),
	})
	if err != nil {
		t.Fatal(err)
	}
	truncated := logical[:len(logical)-world.DropSlotBytes]
	if _, err := decodeLogicalChunk(key, 19, currentChunkSchema, truncated); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("截断掉落物负载 error = %v，想要 ErrCorrupt", err)
	}
	trailing := append(bytes.Clone(logical), 0)
	if _, err := decodeLogicalChunk(key, 19, currentChunkSchema, trailing); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("尾随字节 error = %v，想要 ErrCorrupt", err)
	}
}

func TestChunkCodecKeepsExhaustedDropGeneration(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	chunk := codecFixtureChunk(key.Pos)
	chunk.SetDrop(2, world.DropSlot{Generation: math.MaxUint32})
	encoded, err := encodeChunkPayload(ChunkSave{Key: key, Revision: 19, Chunk: chunk})
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeChunkPayload(key, 19, encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got.Chunk.Drop(2).Generation != math.MaxUint32 {
		t.Fatalf("耗尽的 generation 未被保存: %+v", got.Chunk.Drop(2))
	}
}
