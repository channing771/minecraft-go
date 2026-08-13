package storage

import (
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/world"
)

func TestChunkV6MigrationPreservesStaticLightState(t *testing.T) {
	want := chunkDTO{
		Key:      core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: 2, Z: -3}},
		Revision: 17,
	}
	want.Sections[0] = world.ContainerSnapshot{
		Kind: world.StorageIndexed, Bits: 4,
		Palette: []core.BlockID{core.AirID, core.LightBlockID}, Packed: make([]uint64, 256),
	}
	want.Drops[0] = world.DropSlot{
		Generation: 1, Active: true,
		Stack: core.ItemStack{Item: core.ItemLightBlock, Count: 2}, BlockIndex: 4,
	}
	want.Furnaces[0] = world.FurnaceSlot{Generation: 2}
	want.Chests[0] = world.ChestSlot{Generation: 3}

	got, migrated, err := migrateChunk(6, want)
	if err != nil || !migrated {
		t.Fatalf("v6 identity migration migrated=%v err=%v", migrated, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("v6→v8 identity migration\n got=%+v\nwant=%+v", got, want)
	}
}

func TestChunkV7MigrationPreservesCommonMaterialState(t *testing.T) {
	var want chunkDTO
	want.Sections[0] = world.ContainerSnapshot{
		Kind: world.StorageIndexed, Bits: 4,
		Palette: []core.BlockID{core.AirID, core.MossyCobblestoneID}, Packed: []uint64{1},
	}
	want.Drops[0] = world.DropSlot{
		Generation: 1, Active: true,
		Stack: core.ItemStack{Item: core.ItemMossyCobblestone, Count: 1},
	}

	got, changed, err := migrateChunk(7, want)
	if err != nil || !changed {
		t.Fatalf("migrateChunk(7) changed=%v err=%v", changed, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("v7→v8 identity migration\n got=%+v\nwant=%+v", got, want)
	}
}

func TestChunkFutureSchemaIsRejected(t *testing.T) {
	if _, migrated, err := migrateChunk(currentChunkSchema+1, chunkDTO{}); !errors.Is(err, ErrFutureVersion) || migrated {
		t.Fatalf("未来区块 schema migrated=%v err=%v，想要 ErrFutureVersion", migrated, err)
	}
}

func TestMigrationRegistryIsContinuous(t *testing.T) {
	for schema := oldestChunkSchema; schema < currentChunkSchema; schema++ {
		if _, ok := chunkMigrations[schema]; !ok {
			t.Fatalf("missing migration from schema %d", schema)
		}
	}
	for schema := range chunkMigrations {
		if schema < oldestChunkSchema || schema >= currentChunkSchema {
			t.Fatalf("unexpected migration from schema %d", schema)
		}
	}
}

func TestChunkV4MigrationFillsFullToolDurability(t *testing.T) {
	var dto chunkDTO
	dto.Drops[0] = world.DropSlot{
		Generation: 3, Active: true,
		Stack: core.ItemStack{Item: core.ItemStonePickaxe, Count: 1},
	}
	dto.Drops[1] = world.DropSlot{
		Generation: 4, Active: true,
		Stack: core.ItemStack{Item: core.ItemCoal, Count: 9},
	}

	migrated, changed, err := migrateChunk(4, dto)
	if err != nil || !changed {
		t.Fatalf("migrateChunk(4) changed=%v err=%v", changed, err)
	}
	full, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	if got := migrated.Drops[0].Stack.Durability; got != full {
		t.Fatalf("掉落镐迁移后耐久 = %d，想要 %d", got, full)
	}
	if got := migrated.Drops[1].Stack.Durability; got != 0 {
		t.Fatalf("掉落煤炭迁移后耐久 = %d，想要 0", got)
	}
}

func TestChunkV4MigrationSplitsLegacyToolStacks(t *testing.T) {
	var dto chunkDTO
	dto.Drops[0] = world.DropSlot{Generation: 4}
	dto.Drops[1] = world.DropSlot{Generation: math.MaxUint32}
	dto.Drops[2] = world.DropSlot{Generation: 7}
	dto.Drops[5] = world.DropSlot{
		Generation: 11, Active: true,
		Stack:      core.ItemStack{Item: core.ItemStonePickaxe, Count: 2},
		BlockIndex: 42, AgeTicks: 101, PickupDelayTicks: 9,
	}

	migrated, changed, err := migrateChunk(4, dto)
	if err != nil || !changed {
		t.Fatalf("migrateChunk(4) changed=%v err=%v", changed, err)
	}
	full, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	want := core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: full}
	for _, slot := range []int{5, 0} {
		drop := migrated.Drops[slot]
		if drop.Stack != want || drop.BlockIndex != 42 || drop.AgeTicks != 101 ||
			drop.PickupDelayTicks != 9 {
			t.Fatalf("拆分槽 %d = %+v", slot, drop)
		}
	}
	if migrated.Drops[5].Generation != 11 || migrated.Drops[0].Generation != 5 {
		t.Fatalf("generation 原槽=%d 新槽=%d，想要 11/5",
			migrated.Drops[5].Generation, migrated.Drops[0].Generation)
	}
	if got := migrated.Drops[1]; got != (world.DropSlot{Generation: math.MaxUint32}) {
		t.Fatalf("耗尽槽被复用或修改: %+v", got)
	}
}

func TestChunkV4MigrationRejectsInsufficientCapacityAtomically(t *testing.T) {
	var dto chunkDTO
	dto.Drops[0] = world.DropSlot{
		Generation: 1, Active: true,
		Stack: core.ItemStack{Item: core.ItemStonePickaxe, Count: 2},
	}
	for slot := 1; slot < core.DropsPerChunk; slot++ {
		if slot%2 == 0 {
			dto.Drops[slot] = world.DropSlot{Generation: math.MaxUint32}
			continue
		}
		dto.Drops[slot] = world.DropSlot{
			Generation: uint32(slot), Active: true,
			Stack: core.ItemStack{Item: core.ItemStone, Count: 1},
		}
	}
	before := dto

	if _, changed, err := migrateChunk(4, dto); !errors.Is(err, ErrCorrupt) || changed {
		t.Fatalf("容量不足 changed=%v err=%v，想要 ErrCorrupt", changed, err)
	}
	if dto.Drops != before.Drops {
		t.Fatal("失败迁移修改了调用方 DTO")
	}
}
