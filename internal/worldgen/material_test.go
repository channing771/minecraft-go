package worldgen_test

import (
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/worldgen"
)

func TestNaturalMaterialsAppearInContinuousAreas(t *testing.T) {
	generator := worldgen.New(42)
	seen := make(map[core.BlockID]int)
	seenNegative := make(map[core.BlockID]bool)
	adjacent := make(map[core.BlockID]bool)
	for x := int32(-1024); x <= 1024; x += 4 {
		for z := int32(-1024); z <= 1024; z += 4 {
			height := generator.HeightAt(x, z)
			for _, y := range []int32{height, height - 1, height - 2, height - 4, height - 10} {
				block := generator.BaseBlockAt(core.BlockPos{X: x, Y: y, Z: z})
				seen[block]++
				if x < 0 || z < 0 {
					seenNegative[block] = true
				}
				if generator.BaseBlockAt(core.BlockPos{X: x + 1, Y: y, Z: z}) == block {
					adjacent[block] = true
				}
			}
		}
	}
	for _, block := range []core.BlockID{core.SandID, core.GravelID, core.ClayID, core.SnowBlockID} {
		if seen[block] == 0 || !adjacent[block] || !seenNegative[block] {
			t.Fatalf("材料 %d seen=%d adjacent=%v negative=%v", block, seen[block], adjacent[block], seenNegative[block])
		}
	}
}
