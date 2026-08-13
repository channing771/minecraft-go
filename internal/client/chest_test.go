package client_test

import (
	"testing"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
)

func testChestRef(generation uint32) core.ContainerRef {
	return core.ContainerRef{
		Dimension:  core.Overworld,
		Chunk:      core.ChunkPos{X: -3, Z: 7},
		Kind:       core.ContainerKindChest,
		Slot:       5,
		Generation: generation,
	}
}

func testChestState(generation uint32) network.ChestState {
	var items [core.ChestSlots]core.ItemStack
	items[0] = core.ItemStack{Item: core.ItemStone, Count: 5}
	return network.ChestState{Chest: testChestRef(generation), Items: items}
}

func TestChestMirrorAppliesAuthoritativeState(t *testing.T) {
	var mirror client.ChestMirror
	if _, ok := mirror.State(); ok {
		t.Fatal("初始镜像报告已打开")
	}
	want := testChestState(9)
	if err := mirror.Apply(want); err != nil {
		t.Fatal(err)
	}
	got, ok := mirror.State()
	if !ok || got != want {
		t.Fatalf("镜像状态 = %+v, %v", got, ok)
	}
	// State 返回值副本，改动不得回写镜像。
	got.Items[0].Count = 1
	if again, _ := mirror.State(); again.Items[0].Count != 5 {
		t.Fatal("State 返回了可写引用")
	}
	if ref, ok := mirror.Ref(); !ok || ref != want.Chest {
		t.Fatalf("镜像引用 = %+v, %v", ref, ok)
	}
}

func TestChestMirrorReplacesOnNewGeneration(t *testing.T) {
	var mirror client.ChestMirror
	if err := mirror.Apply(testChestState(9)); err != nil {
		t.Fatal(err)
	}
	next := testChestState(10)
	next.Items[1] = core.ItemStack{Item: core.ItemCoal, Count: 2}
	if err := mirror.Apply(next); err != nil {
		t.Fatal(err)
	}
	got, _ := mirror.State()
	if got != next {
		t.Fatalf("新 generation 未替换旧界面: %+v", got)
	}
}

func TestChestMirrorIgnoresStaleClose(t *testing.T) {
	var mirror client.ChestMirror
	current := testChestState(10)
	if err := mirror.Apply(current); err != nil {
		t.Fatal(err)
	}
	if err := mirror.Close(network.ContainerClosed{Container: testChestRef(9)}); err != nil {
		t.Fatal(err)
	}
	if got, ok := mirror.State(); !ok || got != current {
		t.Fatalf("过期关闭通知影响了当前界面: %+v, %v", got, ok)
	}
	if err := mirror.Close(network.ContainerClosed{Container: current.Chest}); err != nil {
		t.Fatal(err)
	}
	if _, ok := mirror.State(); ok {
		t.Fatal("匹配的关闭通知未清空镜像")
	}
}

// TestChestMirrorIgnoresFurnaceClose 覆盖箱子与熔炉共用同一失效通知类型时，
// 熔炉引用的关闭通知不会影响正在查看的箱子界面。
func TestChestMirrorIgnoresFurnaceClose(t *testing.T) {
	var mirror client.ChestMirror
	current := testChestState(9)
	if err := mirror.Apply(current); err != nil {
		t.Fatal(err)
	}
	furnaceRef := core.FurnaceRef{Dimension: core.Overworld, Slot: 5, Generation: 9}
	if err := mirror.Close(network.ContainerClosed{Container: furnaceRef}); err != nil {
		t.Fatal(err)
	}
	if got, ok := mirror.State(); !ok || got != current {
		t.Fatalf("熔炉关闭通知影响了箱子界面: %+v, %v", got, ok)
	}
}

func TestChestMirrorRejectsInvalidState(t *testing.T) {
	var mirror client.ChestMirror
	valid := testChestState(9)
	if err := mirror.Apply(valid); err != nil {
		t.Fatal(err)
	}
	invalid := valid
	invalid.Chest.Kind = core.ContainerKindFurnace
	if err := mirror.Apply(invalid); err == nil {
		t.Fatal("非法状态被接受")
	}
	if got, _ := mirror.State(); got != valid {
		t.Fatalf("非法状态部分应用: %+v", got)
	}
	if err := mirror.Close(network.ContainerClosed{}); err == nil {
		t.Fatal("非法关闭通知被接受")
	}
}

func TestChestMirrorResetDropsSession(t *testing.T) {
	var mirror client.ChestMirror
	if err := mirror.Apply(testChestState(9)); err != nil {
		t.Fatal(err)
	}
	mirror.Reset()
	if _, ok := mirror.State(); ok {
		t.Fatal("Reset 后仍报告已打开")
	}
}
