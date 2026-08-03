package client_test

import (
	"testing"

	"minecraft-go/internal/client"
	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
)

func stockedMirrorHotbar() core.Hotbar {
	var hotbar core.Hotbar
	hotbar.Selected = 5
	hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 12}
	return hotbar
}

func TestHotbarMirrorStartsUnconfirmed(t *testing.T) {
	var mirror client.HotbarMirror
	if hotbar, ok := mirror.State(); ok || hotbar != (core.Hotbar{}) {
		t.Fatalf("初始镜像 = %+v, %v，想要空且未确认", hotbar, ok)
	}
}

func TestHotbarMirrorAppliesAuthoritativeState(t *testing.T) {
	var mirror client.HotbarMirror
	want := stockedMirrorHotbar()
	if err := mirror.Apply(network.HotbarState{Hotbar: want}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, ok := mirror.State()
	if !ok || got != want {
		t.Fatalf("镜像 = %+v, %v，想要 %+v, true", got, ok, want)
	}
}

func TestHotbarMirrorRejectsInvalidStateWithoutPartialApply(t *testing.T) {
	var mirror client.HotbarMirror
	confirmed := stockedMirrorHotbar()
	if err := mirror.Apply(network.HotbarState{Hotbar: confirmed}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	invalid := []core.Hotbar{
		{Selected: core.HotbarSlots},
		{Slots: [core.HotbarSlots]core.ItemStack{
			0: {Item: core.ItemID(4242), Count: 1},
		}},
		{Slots: [core.HotbarSlots]core.ItemStack{
			0: {Item: core.ItemDirt, Count: core.MaxStackCount + 1},
		}},
		{Slots: [core.HotbarSlots]core.ItemStack{0: {Item: core.ItemNone, Count: 2}}},
	}
	for _, hotbar := range invalid {
		if err := mirror.Apply(network.HotbarState{Hotbar: hotbar}); err == nil {
			t.Fatalf("非法状态 %+v 被接受", hotbar)
		}
		got, ok := mirror.State()
		if !ok || got != confirmed {
			t.Fatalf("非法状态改写了镜像: %+v, %v", got, ok)
		}
	}
}

func TestHotbarMirrorResetDropsPreviousSession(t *testing.T) {
	var mirror client.HotbarMirror
	if err := mirror.Apply(network.HotbarState{Hotbar: stockedMirrorHotbar()}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	mirror.Reset()
	if hotbar, ok := mirror.State(); ok || hotbar != (core.Hotbar{}) {
		t.Fatalf("reset 后镜像 = %+v, %v，想要空且未确认", hotbar, ok)
	}
}
