package client_test

import (
	"testing"

	"minecraft-go/internal/client"
	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
)

func stockedMirrorInventory() core.Inventory {
	var inventory core.Inventory
	inventory.Hotbar.Selected = 5
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 12}
	inventory.Backpack[2] = core.ItemStack{Item: core.ItemDirt, Count: 30}
	return inventory
}

func TestInventoryMirrorStartsUnconfirmed(t *testing.T) {
	var mirror client.InventoryMirror
	if hotbar, ok := mirror.State(); ok || hotbar != (core.Inventory{}) {
		t.Fatalf("初始镜像 = %+v, %v，想要空且未确认", hotbar, ok)
	}
}

func TestInventoryMirrorAppliesAuthoritativeState(t *testing.T) {
	var mirror client.InventoryMirror
	want := stockedMirrorInventory()
	if err := mirror.Apply(network.InventoryState{Inventory: want}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, ok := mirror.State()
	if !ok || got != want {
		t.Fatalf("镜像 = %+v, %v，想要 %+v, true", got, ok, want)
	}
}

func TestInventoryMirrorRejectsInvalidStateWithoutPartialApply(t *testing.T) {
	var mirror client.InventoryMirror
	confirmed := stockedMirrorInventory()
	if err := mirror.Apply(network.InventoryState{Inventory: confirmed}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	invalid := []core.Inventory{
		{Hotbar: core.Hotbar{Selected: core.HotbarSlots}},
		{Hotbar: core.Hotbar{Slots: [core.HotbarSlots]core.ItemStack{
			0: {Item: core.ItemID(4242), Count: 1},
		}}},
		{Backpack: [core.BackpackSlots]core.ItemStack{
			0: {Item: core.ItemDirt, Count: core.MaxStackCount + 1},
		}},
		{Backpack: [core.BackpackSlots]core.ItemStack{
			core.BackpackSlots - 1: {Item: core.ItemNone, Count: 2},
		}},
	}
	for _, inventory := range invalid {
		if err := mirror.Apply(network.InventoryState{Inventory: inventory}); err == nil {
			t.Fatalf("非法状态 %+v 被接受", inventory)
		}
		got, ok := mirror.State()
		if !ok || got != confirmed {
			t.Fatalf("非法状态改写了镜像: %+v, %v", got, ok)
		}
	}
}

func TestInventoryMirrorResetDropsPreviousSession(t *testing.T) {
	var mirror client.InventoryMirror
	if err := mirror.Apply(network.InventoryState{Inventory: stockedMirrorInventory()}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	mirror.Reset()
	if hotbar, ok := mirror.State(); ok || hotbar != (core.Inventory{}) {
		t.Fatalf("reset 后镜像 = %+v, %v，想要空且未确认", hotbar, ok)
	}
}
