package storage

import (
	"errors"
	"reflect"
	"testing"

	"github.com/channing771/mornlea/internal/core"
)

func TestPlayerV5MigrationPreservesState(t *testing.T) {
	safe := PlayerLocation{Dimension: core.Overworld, Position: [3]float32{7, 8, 9}}
	want := playerDTO{
		PlayerID: fixturePlayerID(), Revision: 12, DisplayName: "Chen",
		Current: PlayerLocation{Dimension: core.Overworld, Position: [3]float32{1, 2, 3}},
		Yaw:     4, Pitch: 0.5, Safe: &safe, Inventory: fixturePlayerInventory(), Health: 13,
	}
	got, migrated, err := migratePlayer(5, want)
	if err != nil || !migrated {
		t.Fatalf("v5 identity migration migrated=%v err=%v", migrated, err)
	}
	// v5→v6 是恒等迁移，v6→v7 只填三层饥饿初值：其余字段必须逐字节不变。
	want.Hunger = core.MaxHunger
	want.SaturationMilli = core.InitialSaturationMilli
	want.ExhaustionMilli = 0
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("v5 identity migration\n got=%+v\nwant=%+v", got, want)
	}
}

func TestPlayerFutureSchemaIsRejected(t *testing.T) {
	// 用 currentPlayerSchema+1 而不是字面量：升 schema 时这条断言必须自动跟随，
	// 否则它会退化成"迁移一个已经支持的版本"，永远绿。
	if _, migrated, err := migratePlayer(currentPlayerSchema+1, playerDTO{}); !errors.Is(err, ErrFutureVersion) || migrated {
		t.Fatalf("未来玩家 schema migrated=%v err=%v，想要 ErrFutureVersion", migrated, err)
	}
}

func TestPlayerMigrationRegistryIsContinuous(t *testing.T) {
	for schema := oldestPlayerSchema; schema < currentPlayerSchema; schema++ {
		if _, ok := playerMigrations[schema]; !ok {
			t.Fatalf("missing migration from schema %d", schema)
		}
	}
	for schema := range playerMigrations {
		if schema < oldestPlayerSchema || schema >= currentPlayerSchema {
			t.Fatalf("unexpected migration from schema %d", schema)
		}
	}
}

func TestPlayerV3MigrationFillsFullToolDurability(t *testing.T) {
	var dto playerDTO
	dto.Inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStonePickaxe, Count: 1}
	dto.Inventory.Backpack[0] = core.ItemStack{Item: core.ItemIronPickaxe, Count: 1}
	dto.Inventory.Hotbar.Slots[1] = core.ItemStack{Item: core.ItemStone, Count: 64}
	dto.Inventory.Hotbar.Slots[2] = core.ItemStack{
		Item: core.ItemStonePickaxe, Count: 1, Durability: 7,
	}

	migrated, changed, err := migratePlayer(3, dto)
	if err != nil || !changed {
		t.Fatalf("migratePlayer(3) changed=%v err=%v", changed, err)
	}
	stoneFull, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	ironFull, _ := core.ItemMaxDurability(core.ItemIronPickaxe)
	if got := migrated.Inventory.Hotbar.Slots[0].Durability; got != stoneFull {
		t.Fatalf("石镐迁移后耐久 = %d，想要 %d", got, stoneFull)
	}
	if got := migrated.Inventory.Backpack[0].Durability; got != ironFull {
		t.Fatalf("铁镐迁移后耐久 = %d，想要 %d", got, ironFull)
	}
	if got := migrated.Inventory.Hotbar.Slots[1].Durability; got != 0 {
		t.Fatalf("石头迁移后耐久 = %d，想要 0", got)
	}
	if got := migrated.Inventory.Hotbar.Slots[2].Durability; got != 7 {
		t.Fatalf("已有耐久迁移后 = %d，想要 7", got)
	}
}

func TestPlayerV6MigrationFillsInitialHunger(t *testing.T) {
	dto := playerDTO{Hunger: 0, SaturationMilli: 0, ExhaustionMilli: 7}
	migrated, changed, err := migratePlayer(6, dto)
	if err != nil || !changed {
		t.Fatalf("migratePlayer(6) changed=%v err=%v", changed, err)
	}
	if migrated.Hunger != core.MaxHunger ||
		migrated.SaturationMilli != core.InitialSaturationMilli ||
		migrated.ExhaustionMilli != 0 {
		t.Fatalf("v6 迁移三层饥饿状态 = (%d, %d, %d)，想要初值 (%d, %d, 0)",
			migrated.Hunger, migrated.SaturationMilli, migrated.ExhaustionMilli,
			core.MaxHunger, core.InitialSaturationMilli)
	}
}

func TestPlayerV4MigrationFillsFullHealth(t *testing.T) {
	dto := playerDTO{Health: 0}
	migrated, changed, err := migratePlayer(4, dto)
	if err != nil || !changed {
		t.Fatalf("migratePlayer(4) changed=%v err=%v", changed, err)
	}
	if migrated.Health != core.MaxHealth {
		t.Fatalf("v4 迁移生命值 = %d，想要满血 %d", migrated.Health, core.MaxHealth)
	}
}
