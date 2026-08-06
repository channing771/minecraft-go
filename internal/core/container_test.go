package core_test

import (
	"testing"

	"minecraft-go/internal/core"
)

// TestContainerKindZeroValueIsFurnace 锁定 ContainerKind 的零值必须表示熔炉，
// 否则大量既有 core.FurnaceRef{...} 字面量（不设置 Kind 字段）会被误判为箱子。
func TestContainerKindZeroValueIsFurnace(t *testing.T) {
	var zero core.ContainerKind
	if zero != core.ContainerKindFurnace {
		t.Fatalf("ContainerKind 零值 = %d，想要 ContainerKindFurnace(%d)", zero, core.ContainerKindFurnace)
	}
	if core.ContainerKindFurnace != 0 {
		t.Fatalf("ContainerKindFurnace = %d，必须为 0", core.ContainerKindFurnace)
	}
	if core.ContainerKindChest != core.ContainerKindFurnace+1 {
		t.Fatalf("ContainerKindChest = %d，必须紧随 ContainerKindFurnace 之后", core.ContainerKindChest)
	}
}

// TestFurnaceRefIsContainerRefAlias 锁定 FurnaceRef 是 ContainerRef 的类型别名而不是独立类型：
// 若不是别名，下面的直接赋值将无法通过编译。
func TestFurnaceRefIsContainerRefAlias(t *testing.T) {
	var ref core.FurnaceRef
	var same core.ContainerRef = ref
	var back core.FurnaceRef = same
	if back != ref {
		t.Fatalf("ContainerRef 与 FurnaceRef 互相赋值后不相等: %+v vs %+v", back, ref)
	}
}

// TestContainerRefFields 锁定 ContainerRef 的字段集合与既有熔炉引用字段完全一致，
// 只是新增了 Kind。字段名变化会破坏既有调用点的命名构造。
func TestContainerRefFields(t *testing.T) {
	ref := core.ContainerRef{
		Dimension:  core.Overworld,
		Chunk:      core.ChunkPos{X: -3, Z: 7},
		Kind:       core.ContainerKindChest,
		Slot:       5,
		Generation: 9,
	}
	if ref.Dimension != core.Overworld || ref.Chunk != (core.ChunkPos{X: -3, Z: 7}) ||
		ref.Kind != core.ContainerKindChest || ref.Slot != 5 || ref.Generation != 9 {
		t.Fatalf("字段未按名称写入: %+v", ref)
	}
}

// TestFurnaceRefLegacyConstructionDefaultsToFurnaceKind 覆盖 M4E/M4J 遗留的调用点：
// 它们只用 Dimension/Chunk/Slot/Generation 构造 core.FurnaceRef{...}，不知道 Kind 字段的存在，
// 这些构造出的引用必须继续被解释为熔炉，一行代码都不需要改。
func TestFurnaceRefLegacyConstructionDefaultsToFurnaceKind(t *testing.T) {
	legacy := core.FurnaceRef{
		Dimension:  core.Overworld,
		Chunk:      core.ChunkPos{X: 1, Z: 2},
		Slot:       3,
		Generation: 7,
	}
	if legacy.Kind != core.ContainerKindFurnace {
		t.Fatalf("未设置 Kind 的既有构造 = %+v，Kind 想要 ContainerKindFurnace", legacy)
	}
}
