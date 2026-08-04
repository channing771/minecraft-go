package client_test

import (
	"testing"

	"minecraft-go/internal/client"
	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
)

func testFurnaceRef(generation uint32) core.FurnaceRef {
	return core.FurnaceRef{
		Dimension:  core.Overworld,
		Chunk:      core.ChunkPos{X: -3, Z: 7},
		Slot:       5,
		Generation: generation,
	}
}

func testFurnaceState(generation uint32) network.FurnaceState {
	return network.FurnaceState{
		Furnace:       testFurnaceRef(generation),
		Input:         core.ItemStack{Item: core.ItemRawIron, Count: 7},
		Fuel:          core.ItemStack{Item: core.ItemCoal, Count: 2},
		Output:        core.ItemStack{Item: core.ItemIronIngot, Count: 5},
		ProgressTicks: 137,
		BurnTicks:     1463,
	}
}

func TestFurnaceMirrorAppliesAuthoritativeState(t *testing.T) {
	var mirror client.FurnaceMirror
	if _, ok := mirror.State(); ok {
		t.Fatal("初始镜像报告已打开")
	}
	want := testFurnaceState(9)
	if err := mirror.Apply(want); err != nil {
		t.Fatal(err)
	}
	got, ok := mirror.State()
	if !ok || got != want {
		t.Fatalf("镜像状态 = %+v, %v", got, ok)
	}
	// State 返回值副本，改动不得回写镜像。
	got.ProgressTicks = 0
	if again, _ := mirror.State(); again.ProgressTicks != 137 {
		t.Fatal("State 返回了可写引用")
	}
	if ref, ok := mirror.Ref(); !ok || ref != want.Furnace {
		t.Fatalf("镜像引用 = %+v, %v", ref, ok)
	}
}

func TestFurnaceMirrorReplacesOnNewGeneration(t *testing.T) {
	var mirror client.FurnaceMirror
	if err := mirror.Apply(testFurnaceState(9)); err != nil {
		t.Fatal(err)
	}
	next := testFurnaceState(10)
	next.ProgressTicks = 1
	if err := mirror.Apply(next); err != nil {
		t.Fatal(err)
	}
	got, _ := mirror.State()
	if got != next {
		t.Fatalf("新 generation 未替换旧界面: %+v", got)
	}
}

func TestFurnaceMirrorIgnoresStaleClose(t *testing.T) {
	var mirror client.FurnaceMirror
	current := testFurnaceState(10)
	if err := mirror.Apply(current); err != nil {
		t.Fatal(err)
	}
	if err := mirror.Close(network.FurnaceClosed{Furnace: testFurnaceRef(9)}); err != nil {
		t.Fatal(err)
	}
	if got, ok := mirror.State(); !ok || got != current {
		t.Fatalf("过期关闭通知影响了当前界面: %+v, %v", got, ok)
	}
	if err := mirror.Close(network.FurnaceClosed{Furnace: current.Furnace}); err != nil {
		t.Fatal(err)
	}
	if _, ok := mirror.State(); ok {
		t.Fatal("匹配的关闭通知未清空镜像")
	}
}

func TestFurnaceMirrorRejectsInvalidState(t *testing.T) {
	var mirror client.FurnaceMirror
	valid := testFurnaceState(9)
	if err := mirror.Apply(valid); err != nil {
		t.Fatal(err)
	}
	invalid := valid
	invalid.Input = core.ItemStack{Item: core.ItemCoal, Count: 1}
	if err := mirror.Apply(invalid); err == nil {
		t.Fatal("非法状态被接受")
	}
	if got, _ := mirror.State(); got != valid {
		t.Fatalf("非法状态部分应用: %+v", got)
	}
	if err := mirror.Close(network.FurnaceClosed{}); err == nil {
		t.Fatal("非法关闭通知被接受")
	}
}

func TestFurnaceMirrorResetDropsSession(t *testing.T) {
	var mirror client.FurnaceMirror
	if err := mirror.Apply(testFurnaceState(9)); err != nil {
		t.Fatal(err)
	}
	mirror.Reset()
	if _, ok := mirror.State(); ok {
		t.Fatal("Reset 后仍报告已打开")
	}
}
