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
	full, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
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
		{"工具数量大于一", world.DropSlot{
			Generation: 1, Active: true,
			Stack: core.ItemStack{
				Item: core.ItemStonePickaxe, Count: 2, Durability: full,
			},
			BlockIndex: index,
		}},
		{"工具零耐久", world.DropSlot{
			Generation: 1, Active: true,
			Stack:      core.ItemStack{Item: core.ItemStonePickaxe, Count: 1},
			BlockIndex: index,
		}},
		{"schema v4 磨损工具", world.DropSlot{
			Generation: 1, Active: true,
			Stack: core.ItemStack{
				Item: core.ItemStonePickaxe, Count: 1, Durability: full - 1,
			},
			BlockIndex: index,
		}},
		{"非工具携带耐久", world.DropSlot{
			Generation: 1, Active: true,
			Stack:      core.ItemStack{Item: core.ItemStone, Count: 1, Durability: 1},
			BlockIndex: index,
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

func TestChunkV4ToolDropRestoresFullDurabilityAndCanBePickedUp(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	chunk := codecFixtureChunk(key.Pos)
	index, ok := world.ChunkBlockIndex(core.BlockPos{
		X: key.Pos.X << core.SectionShift, Y: 5, Z: key.Pos.Z << core.SectionShift,
	})
	if !ok {
		t.Fatal("固定测试方块没有区块索引")
	}
	full, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	want := core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: full}
	chunk.SetDrop(0, world.DropSlot{
		Generation: 1, Active: true, Stack: want, BlockIndex: index,
	})

	encoded, err := encodeChunkPayload(ChunkSave{Key: key, Revision: 19, Chunk: chunk})
	if err != nil {
		t.Fatalf("编码 schema v4 满耐久工具: %v", err)
	}
	got, err := decodeChunkPayload(key, 19, encoded)
	if err != nil {
		t.Fatalf("解码 schema v4 满耐久工具: %v", err)
	}
	if got.Schema != 4 || got.Chunk.Drop(0).Stack != want {
		t.Fatalf("schema v4 工具掉落物 = %+v，想要 %+v", got.Chunk.Drop(0).Stack, want)
	}
	inventory, remainder := (core.Inventory{}).AddStack(got.Chunk.Drop(0).Stack)
	if remainder != (core.ItemStack{}) || inventory.Hotbar.Slots[0] != want {
		t.Fatalf("拾取 schema v4 工具后 inventory=%+v remainder=%+v", inventory, remainder)
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

func TestChunkV2FixtureMigratesLosslesslyAndNeedsRewrite(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	encoded, err := os.ReadFile(filepath.Join("testdata", "chunk-v2.bin"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeChunkPayload(key, 19, encoded)
	if err != nil {
		t.Fatal(err)
	}
	want := dropFixtureChunk(t, key.Pos)
	if got.Chunk.Hash() != want.Hash() {
		t.Fatal("v2 迁移改变了方块状态")
	}
	if got.Chunk.DropsHash() != want.DropsHash() {
		t.Fatal("v2 迁移改变了掉落物状态")
	}
	if got.Schema != currentChunkSchema {
		t.Fatalf("迁移后 schema = %d，想要 %d", got.Schema, currentChunkSchema)
	}
	if !got.Migrated {
		t.Fatal("v2 区块必须标记为已迁移，才能在下次保存时改写为 v3")
	}
}

func TestChunkV3FixtureDoesNotNeedRewrite(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	encoded, err := encodeChunkPayload(ChunkSave{
		Key: key, Revision: 19, Chunk: dropFixtureChunk(t, key.Pos),
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeChunkPayload(key, 19, encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got.Migrated {
		t.Fatal("当前 schema 不应标记为已迁移")
	}
}

func TestChunkCodecRoundTripsStoneBrick(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	chunk := codecFixtureChunk(key.Pos)
	chunk.SetBlock(1, 2, 3, core.StoneBrickID)
	index, ok := world.ChunkBlockIndex(core.BlockPos{
		X: key.Pos.X<<core.SectionShift + 1, Y: 2, Z: key.Pos.Z<<core.SectionShift + 3,
	})
	if !ok {
		t.Fatal("石砖方块没有区块索引")
	}
	chunk.SetDrop(0, world.DropSlot{
		Generation: 1, Active: true,
		Stack:      core.ItemStack{Item: core.ItemStoneBrick, Count: 4},
		BlockIndex: index,
	})

	encoded, err := encodeChunkPayload(ChunkSave{Key: key, Revision: 19, Chunk: chunk})
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeChunkPayload(key, 19, encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got.Chunk.BlockAt(1, 2, 3) != core.StoneBrickID {
		t.Fatalf("石砖方块未往返: %d", got.Chunk.BlockAt(1, 2, 3))
	}
	if got.Chunk.Drop(0).Stack.Item != core.ItemStoneBrick {
		t.Fatalf("石砖掉落物未往返: %+v", got.Chunk.Drop(0))
	}
}
