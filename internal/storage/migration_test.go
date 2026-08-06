package storage

import (
	"errors"
	"math"
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

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
