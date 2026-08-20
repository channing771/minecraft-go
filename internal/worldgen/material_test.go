package worldgen_test

import (
	"testing"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/worldgen"
)

func TestNaturalMaterialBoundaries(t *testing.T) {
	generator := worldgen.New(42, false)
	tests := []struct {
		name string
		pos  core.BlockPos
		want core.BlockID
	}{
		{name: "雪线下方地表", pos: core.BlockPos{X: -695, Y: 87, Z: 470}, want: core.GrassID},
		{name: "雪线地表", pos: core.BlockPos{X: -583, Y: 88, Z: 663}, want: core.SnowBlockID},
		{name: "雪线地表下一层", pos: core.BlockPos{X: -583, Y: 87, Z: 663}, want: core.DirtID},
		{name: "黏土阈值边界", pos: core.BlockPos{X: -1024, Y: 58, Z: -869}, want: core.ClayID},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := generator.BaseBlockAt(test.pos); got != test.want {
				t.Fatalf("BaseBlockAt(%+v) = %d，期望 %d", test.pos, got, test.want)
			}
		})
	}
}

func TestNaturalMaterialsAppearInContinuousAreas(t *testing.T) {
	generator := worldgen.New(42, false)
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
