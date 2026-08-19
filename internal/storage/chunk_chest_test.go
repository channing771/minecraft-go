package storage

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/world"
)

// chestFixtureChunk 返回一个同时含方块、掉落物、熔炉与箱子状态的固定区块。
func chestFixtureChunk(t *testing.T, pos core.ChunkPos) *world.Chunk {
	t.Helper()
	chunk := furnaceFixtureChunk(t, pos)
	first := furnaceBlockIndex(t, pos, 7, 8, 9)
	second := furnaceBlockIndex(t, pos, 10, 11, 12)
	chunk.SetBlock(7, 8, 9, core.ChestID)
	chunk.SetBlock(10, 11, 12, core.ChestID)

	full, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	var items [core.ChestSlots]core.ItemStack
	items[0] = core.ItemStack{Item: core.ItemStone, Count: 42}
	items[1] = core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: full / 2}
	items[26] = core.ItemStack{Item: core.ItemIronIngot, Count: core.MaxStackCount}

	chunk.SetChest(0, world.ChestSlot{
		Generation: 5, Active: true, BlockIndex: first, Items: items,
	})
	chunk.SetChest(15, world.ChestSlot{
		Generation: 2, Active: true, BlockIndex: second,
	})
	// 非活动槽仍保留 generation，避免复用时重复分配旧引用。
	chunk.SetChest(9, world.ChestSlot{Generation: 3})
	return chunk
}

func TestChunkCodecRoundTripsChests(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	want := chestFixtureChunk(t, key.Pos)
	encoded, err := encodeChunkPayload(ChunkSave{Key: key, Revision: 19, Chunk: want})
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeChunkPayload(key, 19, encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got.Schema != currentChunkSchema || currentChunkSchema != 9 {
		t.Fatalf("schema = %d，想要 9", got.Schema)
	}
	for slot := range core.ChestsPerChunk {
		if got.Chunk.Chest(slot) != want.Chest(slot) {
			t.Fatalf("箱子槽 %d = %+v，想要 %+v", slot, got.Chunk.Chest(slot), want.Chest(slot))
		}
	}
	if got.Chunk.Hash() != want.Hash() || got.Chunk.DropsHash() != want.DropsHash() {
		t.Fatal("方块或掉落物在往返后改变")
	}
	for slot := range core.FurnacesPerChunk {
		if got.Chunk.Furnace(slot) != want.Furnace(slot) {
			t.Fatalf("熔炉槽 %d = %+v，想要 %+v", slot, got.Chunk.Furnace(slot), want.Furnace(slot))
		}
	}
	if got.Migrated {
		t.Fatal("当前 schema 不应标记为已迁移")
	}
}

func TestChunkV5FixtureMigratesToEmptyChests(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	encoded, err := os.ReadFile(filepath.Join("testdata", "chunk-v5.bin"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeChunkPayload(key, 19, encoded)
	if err != nil {
		t.Fatal(err)
	}
	want := furnaceFixtureChunk(t, key.Pos)
	if got.Chunk.Hash() != want.Hash() || got.Chunk.DropsHash() != want.DropsHash() {
		t.Fatal("v5 迁移改变了方块或掉落物状态")
	}
	for slot := range core.FurnacesPerChunk {
		if got.Chunk.Furnace(slot) != want.Furnace(slot) {
			t.Fatalf("v5 迁移改变了熔炉槽 %d", slot)
		}
	}
	empty := world.NewChunk(key.Pos)
	for slot := range core.ChestsPerChunk {
		if got.Chunk.Chest(slot) != empty.Chest(slot) {
			t.Fatalf("v5 迁移必须得到空箱子集合: 槽 %d = %+v", slot, got.Chunk.Chest(slot))
		}
	}
	if got.Schema != currentChunkSchema {
		t.Fatalf("迁移后 schema = %d，想要 %d", got.Schema, currentChunkSchema)
	}
	if !got.Migrated {
		t.Fatal("v5 区块必须标记为已迁移，才能在下次保存时改写为当前 schema")
	}
}

func TestChunkV1ThroughV4FixturesChainMigrateToEmptyChests(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	empty := world.NewChunk(key.Pos)
	for _, name := range []string{"chunk-v1.bin", "chunk-v2.bin", "chunk-v3.bin", "chunk-v4.bin"} {
		encoded, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		got, err := decodeChunkPayload(key, 19, encoded)
		if err != nil {
			t.Fatalf("%s 迁移失败: %v", name, err)
		}
		if got.Schema != currentChunkSchema || !got.Migrated {
			t.Fatalf("%s 迁移结果 schema=%d migrated=%v", name, got.Schema, got.Migrated)
		}
		for slot := range core.ChestsPerChunk {
			if got.Chunk.Chest(slot) != empty.Chest(slot) {
				t.Fatalf("%s 迁移未初始化空箱子: 槽 %d", name, slot)
			}
		}
	}
}

func TestChunkV6FixtureMigratesLosslessly(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	want := chestFixtureChunk(t, key.Pos)
	path := filepath.Join("testdata", "chunk-v6.bin")
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeChunkPayload(key, 19, encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !decoded.Migrated || decoded.Schema != currentChunkSchema || decoded.Revision != 19 {
		t.Fatalf("v6 fixture schema=%d revision=%d migrated=%v", decoded.Schema, decoded.Revision, decoded.Migrated)
	}
	if decoded.Chunk.Hash() != want.Hash() || decoded.Chunk.DropsHash() != want.DropsHash() {
		t.Fatal("v6 identity migration 改变了 palette 或掉落物")
	}
	for slot := range core.FurnacesPerChunk {
		if decoded.Chunk.Furnace(slot) != want.Furnace(slot) {
			t.Fatalf("v6 fixture 熔炉槽 %d = %+v，想要 %+v", slot, decoded.Chunk.Furnace(slot), want.Furnace(slot))
		}
	}
	for slot := range core.ChestsPerChunk {
		if decoded.Chunk.Chest(slot) != want.Chest(slot) {
			t.Fatalf("v6 fixture 箱子槽 %d = %+v，想要 %+v", slot, decoded.Chunk.Chest(slot), want.Chest(slot))
		}
	}
	if decoded.Chunk.Hash() != want.Hash() || decoded.Chunk.DropsHash() != want.DropsHash() {
		t.Fatal("v6 fixture 迁移改变了方块或掉落物状态")
	}
}

// TestChunkV9Fixture 冻结当前 schema 的编码结果，防止字节布局无声漂移。
// 夹具含全部 8 个流体编号，因此 golden 同时覆盖含流体的区块。
func TestChunkV9Fixture(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	chunk := fluidFixtureChunk(t, key.Pos)
	encoded, err := encodeChunkPayload(ChunkSave{Key: key, Revision: 19, Chunk: chunk})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "chunk-v9.bin")
	if *updateStorageFixtures {
		if err := os.WriteFile(path, encoded, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, encoded) {
		t.Fatal("v9 fixture drift; change schema version")
	}
	// 夹具前提守卫排在真实断言之后：golden 必须真的含流体，否则它不覆盖 v9 的新语义。
	if fluids := countFluidCells(chunk); fluids != len(fluidBlockIDs) {
		t.Fatalf("v9 golden 流体格数 = %d，想要 %d（夹具失效）", fluids, len(fluidBlockIDs))
	}
}

func TestChunkCodecRejectsInvalidChestSlots(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	index := furnaceBlockIndex(t, key.Pos, 1, 2, 3)
	other := furnaceBlockIndex(t, key.Pos, 4, 5, 6)

	cases := []struct {
		name string
		slot world.ChestSlot
	}{
		{"活动槽零 generation", world.ChestSlot{Active: true, BlockIndex: index}},
		{"越界方块索引", world.ChestSlot{
			Generation: 1, Active: true,
			BlockIndex: core.SectionsPerChunk * core.BlocksPerSection,
		}},
		{"未知物品", world.ChestSlot{
			Generation: 1, Active: true, BlockIndex: index,
			Items: chestItems(0, core.ItemStack{Item: core.ItemID(4242), Count: 1}),
		}},
		{"数量超限", world.ChestSlot{
			Generation: 1, Active: true, BlockIndex: index,
			Items: chestItems(0, core.ItemStack{Item: core.ItemStone, Count: core.MaxStackCount + 1}),
		}},
		{"停用槽残留字段", world.ChestSlot{
			Generation: 1, BlockIndex: index,
		}},
		{"对应方块不是箱子", world.ChestSlot{
			Generation: 1, Active: true, BlockIndex: other,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chunk := codecFixtureChunk(key.Pos)
			chunk.SetBlock(1, 2, 3, core.ChestID)
			chunk.SetChest(1, tc.slot)
			if _, err := encodeChunkPayload(ChunkSave{
				Key: key, Revision: 19, Chunk: chunk,
			}); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("编码非法箱子槽 error = %v，想要 ErrCorrupt", err)
			}
		})
	}
}

func chestItems(slot int, stack core.ItemStack) [core.ChestSlots]core.ItemStack {
	var items [core.ChestSlots]core.ItemStack
	items[slot] = stack
	return items
}

func TestChunkCodecRejectsDuplicateChestBlockIndex(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	index := furnaceBlockIndex(t, key.Pos, 1, 2, 3)
	chunk := codecFixtureChunk(key.Pos)
	chunk.SetBlock(1, 2, 3, core.ChestID)
	chunk.SetChest(0, world.ChestSlot{Generation: 1, Active: true, BlockIndex: index})
	chunk.SetChest(4, world.ChestSlot{Generation: 1, Active: true, BlockIndex: index})

	if _, err := encodeChunkPayload(ChunkSave{
		Key: key, Revision: 19, Chunk: chunk,
	}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("重复方块索引 error = %v，想要 ErrCorrupt", err)
	}
}

func TestChunkCodecRejectsChestActiveFlagOutOfRange(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	logical, err := encodeLogicalChunk(ChunkSave{
		Key: key, Revision: 19, Chunk: chestFixtureChunk(t, key.Pos),
	})
	if err != nil {
		t.Fatal(err)
	}
	// 第一个箱子槽的 active 字节位于箱子负载起始处偏移 4（generation 之后）。
	offset := len(logical) - core.ChestsPerChunk*world.ChestSlotBytes + 4
	corrupted := bytes.Clone(logical)
	corrupted[offset] = 2
	if _, err := decodeLogicalChunk(key, 19, currentChunkSchema, corrupted); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("非法箱子 active 标志 error = %v，想要 ErrCorrupt", err)
	}
}

func TestChunkCodecRejectsWrongChestSlotCount(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	logical, err := encodeLogicalChunk(ChunkSave{
		Key: key, Revision: 19, Chunk: chestFixtureChunk(t, key.Pos),
	})
	if err != nil {
		t.Fatal(err)
	}
	truncated := logical[:len(logical)-world.ChestSlotBytes]
	if _, err := decodeLogicalChunk(key, 19, currentChunkSchema, truncated); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("截断箱子负载 error = %v，想要 ErrCorrupt", err)
	}
	trailing := append(bytes.Clone(logical), 0)
	if _, err := decodeLogicalChunk(key, 19, currentChunkSchema, trailing); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("尾随字节 error = %v，想要 ErrCorrupt", err)
	}
}
