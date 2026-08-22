package core_test

import (
	"testing"

	"github.com/channing771/mornlea/internal/core"
)

// TestIsFluidAndFluidLevelExhaustive 对 BlockID 全域 0..BlockIDMax-1 穷举断言
// 分类正确：只有 WaterSourceID 与 WaterLevel1ID..WaterLevel7ID 这 8 个编号是
// 流体，源方块等级为 0，第 N 个流动方块等级为 N，其余编号一律不是流体。
func TestIsFluidAndFluidLevelExhaustive(t *testing.T) {
	for id := core.AirID; id < core.BlockIDMax; id++ {
		wantFluid := id >= core.WaterSourceID && id <= core.WaterLevel7ID
		if got := core.IsFluid(id); got != wantFluid {
			t.Fatalf("IsFluid(%d) = %v，想要 %v", id, got, wantFluid)
		}
		if !wantFluid {
			continue
		}
		wantLevel := uint8(id - core.WaterSourceID)
		if got := core.FluidLevel(id); got != wantLevel {
			t.Fatalf("FluidLevel(%d) = %d，想要 %d", id, got, wantLevel)
		}
	}
}

// TestFluidLevelRejectsNonFluidBlocks 锁定非流体编号（含未注册编号）下
// FluidLevel 的口径：不 panic，返回 0，且调用方 MUST 先用 IsFluid 判定。
func TestFluidLevelRejectsNonFluidBlocks(t *testing.T) {
	for _, id := range []core.BlockID{
		core.AirID, core.StoneID, core.MossyCobblestoneID,
		core.FarmlandDryID, core.WheatStage7ID, core.BlockIDMax, core.BlockID(65535),
	} {
		if core.IsFluid(id) {
			t.Fatalf("IsFluid(%d) 不应为 true", id)
		}
		if got := core.FluidLevel(id); got != 0 {
			t.Fatalf("FluidLevel(%d) = %d，想要 0", id, got)
		}
	}
}

// TestFluidBlockIDsAreRegisteredAndOrdered 锁定 8 个流体编号紧随
// MossyCobblestoneID 之后、严格递增追加，且全部已注册。
func TestFluidBlockIDsAreRegisteredAndOrdered(t *testing.T) {
	ids := []core.BlockID{
		core.WaterSourceID, core.WaterLevel1ID, core.WaterLevel2ID, core.WaterLevel3ID,
		core.WaterLevel4ID, core.WaterLevel5ID, core.WaterLevel6ID, core.WaterLevel7ID,
	}
	if ids[0] != core.MossyCobblestoneID+1 {
		t.Fatalf("WaterSourceID = %d，想要紧随 MossyCobblestoneID 之后 (%d)", ids[0], core.MossyCobblestoneID+1)
	}
	for i, id := range ids {
		if id != core.MossyCobblestoneID+1+core.BlockID(i) {
			t.Fatalf("流体编号[%d] = %d，与预期的连续追加顺序不符", i, id)
		}
		if !core.RegisteredBlock(id) {
			t.Fatalf("流体编号 %d 未注册", id)
		}
	}
	// 注意：WaterLevel7ID+1 现在是 FarmlandDryID（已注册农业方块），不再是
	// 未注册编号；未注册的独占上界只能用 BlockIDMax 表达。
	if core.RegisteredBlock(core.BlockIDMax) {
		t.Fatal("BlockIDMax 及其之后的编号不应被注册")
	}
}
