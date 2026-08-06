package storage

import (
	"testing"

	"minecraft-go/internal/core"
)

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
