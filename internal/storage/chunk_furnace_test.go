package storage

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

// furnaceFixtureChunk 返回一个同时含方块、掉落物与熔炉状态的固定区块。
func furnaceFixtureChunk(t *testing.T, pos core.ChunkPos) *world.Chunk {
	t.Helper()
	chunk := dropFixtureChunk(t, pos)
	first := furnaceBlockIndex(t, pos, 1, 2, 3)
	second := furnaceBlockIndex(t, pos, 4, 5, 6)
	chunk.SetBlock(1, 2, 3, core.FurnaceID)
	chunk.SetBlock(4, 5, 6, core.FurnaceID)
	chunk.SetFurnace(0, world.FurnaceSlot{
		Generation: 3, Active: true, BlockIndex: first,
		Input:         core.ItemStack{Item: core.ItemRawIron, Count: 7},
		Fuel:          core.ItemStack{Item: core.ItemCoal, Count: 2},
		Output:        core.ItemStack{Item: core.ItemIronIngot, Count: 5},
		ProgressTicks: 137, BurnTicks: 1463,
	})
	chunk.SetFurnace(31, world.FurnaceSlot{
		Generation: 1, Active: true, BlockIndex: second,
		ProgressTicks: 0, BurnTicks: 0,
	})
	// 非活动槽仍保留 generation，避免复用时重复分配旧引用。
	chunk.SetFurnace(9, world.FurnaceSlot{Generation: 4})
	return chunk
}

func furnaceBlockIndex(t *testing.T, pos core.ChunkPos, lx, y, lz int32) uint32 {
	t.Helper()
	index, ok := world.ChunkBlockIndex(core.BlockPos{
		X: pos.X<<core.SectionShift + lx, Y: y, Z: pos.Z<<core.SectionShift + lz,
	})
	if !ok {
		t.Fatalf("局部坐标 (%d,%d,%d) 没有区块索引", lx, y, lz)
	}
	return index
}

func TestChunkCodecRoundTripsFurnaces(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	want := furnaceFixtureChunk(t, key.Pos)
	encoded, err := encodeChunkPayload(ChunkSave{Key: key, Revision: 19, Chunk: want})
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeChunkPayload(key, 19, encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got.Schema != currentChunkSchema || currentChunkSchema != 5 {
		t.Fatalf("schema = %d，想要 5", got.Schema)
	}
	for slot := range core.FurnacesPerChunk {
		if got.Chunk.Furnace(slot) != want.Furnace(slot) {
			t.Fatalf("熔炉槽 %d = %+v，想要 %+v",
				slot, got.Chunk.Furnace(slot), want.Furnace(slot))
		}
	}
	if got.Chunk.Hash() != want.Hash() || got.Chunk.DropsHash() != want.DropsHash() {
		t.Fatal("方块或掉落物在往返后改变")
	}
	if got.Migrated {
		t.Fatal("当前 schema 不应标记为已迁移")
	}
}

func TestChunkV3FixtureMigratesToEmptyFurnaces(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	encoded, err := os.ReadFile(filepath.Join("testdata", "chunk-v3.bin"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeChunkPayload(key, 19, encoded)
	if err != nil {
		t.Fatal(err)
	}
	want := dropFixtureChunk(t, key.Pos)
	if got.Chunk.Hash() != want.Hash() {
		t.Fatal("v3 迁移改变了方块状态")
	}
	if got.Chunk.DropsHash() != want.DropsHash() {
		t.Fatal("v3 迁移改变了掉落物状态")
	}
	empty := world.NewChunk(key.Pos)
	for slot := range core.FurnacesPerChunk {
		if got.Chunk.Furnace(slot) != empty.Furnace(slot) {
			t.Fatalf("v3 迁移必须得到空熔炉集合: 槽 %d = %+v", slot, got.Chunk.Furnace(slot))
		}
	}
	if got.Schema != currentChunkSchema {
		t.Fatalf("迁移后 schema = %d，想要 %d", got.Schema, currentChunkSchema)
	}
	if !got.Migrated {
		t.Fatal("v3 区块必须标记为已迁移，才能在下次保存时改写为当前 schema")
	}
}

func TestChunkV1AndV2StillMigrateThroughChain(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	empty := world.NewChunk(key.Pos)
	for _, name := range []string{"chunk-v1.bin", "chunk-v2.bin"} {
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
		if got.Chunk.Hash() != codecFixtureChunk(key.Pos).Hash() {
			t.Fatalf("%s 迁移改变了方块状态", name)
		}
		for slot := range core.FurnacesPerChunk {
			if got.Chunk.Furnace(slot) != empty.Furnace(slot) {
				t.Fatalf("%s 迁移未初始化空熔炉: 槽 %d", name, slot)
			}
		}
	}
}

func TestChunkV5Fixture(t *testing.T) {
	want := furnaceFixtureChunk(t, core.ChunkPos{X: -3, Z: 7})
	encoded, err := encodeChunkPayload(ChunkSave{
		Key:      core.ChunkKey{Dimension: core.Overworld, Pos: want.Pos},
		Revision: 19,
		Chunk:    want,
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "chunk-v5.bin")
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
		t.Fatal("v5 fixture drift; change schema version")
	}
}

func TestChunkV4FixtureMigratesLosslessly(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	encoded, err := os.ReadFile(filepath.Join("testdata", "chunk-v4.bin"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeChunkPayload(key, 19, encoded)
	if err != nil {
		t.Fatal(err)
	}
	want := furnaceFixtureChunk(t, key.Pos)
	if got.Chunk.Hash() != want.Hash() || got.Chunk.DropsHash() != want.DropsHash() {
		t.Fatal("v4 迁移改变了方块或掉落物状态")
	}
	for slot := range core.FurnacesPerChunk {
		if got.Chunk.Furnace(slot) != want.Furnace(slot) {
			t.Fatalf("v4 迁移改变了熔炉槽 %d", slot)
		}
	}
	if !got.Migrated || got.Schema != currentChunkSchema {
		t.Fatalf("v4 迁移结果 schema=%d migrated=%v", got.Schema, got.Migrated)
	}
}

func TestChunkCodecRejectsInvalidFurnaceSlots(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	index := furnaceBlockIndex(t, key.Pos, 1, 2, 3)
	other := furnaceBlockIndex(t, key.Pos, 4, 5, 6)

	cases := []struct {
		name string
		slot world.FurnaceSlot
	}{
		{"活动槽零 generation", world.FurnaceSlot{Active: true, BlockIndex: index}},
		{"越界方块索引", world.FurnaceSlot{
			Generation: 1, Active: true,
			BlockIndex: core.SectionsPerChunk * core.BlocksPerSection,
		}},
		{"输入错误物品", world.FurnaceSlot{
			Generation: 1, Active: true, BlockIndex: index,
			Input: core.ItemStack{Item: core.ItemCoal, Count: 1},
		}},
		{"燃料错误物品", world.FurnaceSlot{
			Generation: 1, Active: true, BlockIndex: index,
			Fuel: core.ItemStack{Item: core.ItemRawIron, Count: 1},
		}},
		{"输出错误物品", world.FurnaceSlot{
			Generation: 1, Active: true, BlockIndex: index,
			Output: core.ItemStack{Item: core.ItemStone, Count: 1},
		}},
		{"数量超限", world.FurnaceSlot{
			Generation: 1, Active: true, BlockIndex: index,
			Input: core.ItemStack{Item: core.ItemRawIron, Count: core.MaxStackCount + 1},
		}},
		{"进度超限", world.FurnaceSlot{
			Generation: 1, Active: true, BlockIndex: index,
			ProgressTicks: core.FurnaceSmeltTicks,
		}},
		{"燃烧超限", world.FurnaceSlot{
			Generation: 1, Active: true, BlockIndex: index,
			BurnTicks: core.FurnaceBurnTicks + 1,
		}},
		{"停用槽残留字段", world.FurnaceSlot{
			Generation: 1, BlockIndex: index, ProgressTicks: 5,
		}},
		{"对应方块不是熔炉", world.FurnaceSlot{
			Generation: 1, Active: true, BlockIndex: other,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chunk := codecFixtureChunk(key.Pos)
			chunk.SetBlock(1, 2, 3, core.FurnaceID)
			chunk.SetFurnace(1, tc.slot)
			if _, err := encodeChunkPayload(ChunkSave{
				Key: key, Revision: 19, Chunk: chunk,
			}); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("编码非法熔炉槽 error = %v，想要 ErrCorrupt", err)
			}
		})
	}
}

func TestChunkCodecRejectsDuplicateFurnaceBlockIndex(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	index := furnaceBlockIndex(t, key.Pos, 1, 2, 3)
	chunk := codecFixtureChunk(key.Pos)
	chunk.SetBlock(1, 2, 3, core.FurnaceID)
	chunk.SetFurnace(0, world.FurnaceSlot{Generation: 1, Active: true, BlockIndex: index})
	chunk.SetFurnace(4, world.FurnaceSlot{Generation: 1, Active: true, BlockIndex: index})

	if _, err := encodeChunkPayload(ChunkSave{
		Key: key, Revision: 19, Chunk: chunk,
	}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("重复方块索引 error = %v，想要 ErrCorrupt", err)
	}
}

func TestChunkCodecRejectsWrongFurnaceSlotCount(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	logical, err := encodeLogicalChunk(ChunkSave{
		Key: key, Revision: 19, Chunk: furnaceFixtureChunk(t, key.Pos),
	})
	if err != nil {
		t.Fatal(err)
	}
	truncated := logical[:len(logical)-world.FurnaceSlotBytes]
	if _, err := decodeLogicalChunk(key, 19, currentChunkSchema, truncated); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("截断熔炉负载 error = %v，想要 ErrCorrupt", err)
	}
	trailing := append(bytes.Clone(logical), 0)
	if _, err := decodeLogicalChunk(key, 19, currentChunkSchema, trailing); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("尾随字节 error = %v，想要 ErrCorrupt", err)
	}
}

func TestChunkCodecRejectsFutureSchema(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	if _, _, err := migrateChunk(currentChunkSchema+1, chunkDTO{}); !errors.Is(err, ErrFutureVersion) {
		t.Fatalf("未来 schema error = %v，想要 ErrFutureVersion", err)
	}
	_ = key
}

func TestPlayerSchemaV4KeepsM4EItems(t *testing.T) {
	if currentPlayerSchema != 4 {
		t.Fatalf("玩家 schema = %d，想要 4", currentPlayerSchema)
	}
	var inventory core.Inventory
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemCoal, Count: 12}
	inventory.Hotbar.Slots[1] = core.ItemStack{Item: core.ItemRawIron, Count: 5}
	inventory.Backpack[0] = core.ItemStack{Item: core.ItemIronIngot, Count: 64}
	inventory.Backpack[1] = core.ItemStack{Item: core.ItemFurnace, Count: 1}
	inventory.Backpack[2] = core.ItemStack{Item: core.ItemIronBlock, Count: 3}

	save := fixturePlayerSave(fixturePlayerID(), 7)
	save.Inventory = inventory
	encoded, err := encodePlayer(save)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodePlayer(save.PlayerID, encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got.Inventory != inventory {
		t.Fatalf("M4E 物品未往返: %+v", got.Inventory)
	}
}
