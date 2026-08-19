package worldgen_test

import (
	"testing"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/worldgen"
)

// oreSamplePositions 是一批覆盖正负坐标与不同高度的固定采样点。
func oreSamplePositions() []core.BlockPos {
	positions := make([]core.BlockPos, 0, 4096)
	for x := int32(-32); x < 32; x++ {
		for z := int32(-32); z < 32; z++ {
			positions = append(positions, core.BlockPos{X: x, Y: -20, Z: z})
		}
	}
	return positions
}

func TestOreGenerationIsDeterministicForSameSeed(t *testing.T) {
	first := worldgen.New(42, false)
	second := worldgen.New(42, false)
	for _, pos := range oreSamplePositions() {
		if first.BaseBlockAt(pos) != second.BaseBlockAt(pos) {
			t.Fatalf("同种子在 %+v 结果不一致", pos)
		}
	}
}

func TestOreGenerationDiffersAcrossSeeds(t *testing.T) {
	first := worldgen.New(42, false)
	second := worldgen.New(43, false)
	for _, pos := range oreSamplePositions() {
		if first.BaseBlockAt(pos) != second.BaseBlockAt(pos) {
			return
		}
	}
	t.Fatal("不同种子在全部采样点结果相同")
}

func TestOreOnlyReplacesStoneWithinHeightLimits(t *testing.T) {
	generator := worldgen.New(42, false)
	coalSeen, ironSeen := false, false
	for x := int32(-64); x < 64; x++ {
		for z := int32(-64); z < 64; z++ {
			for y := int32(-64); y < 120; y += 7 {
				pos := core.BlockPos{X: x, Y: y, Z: z}
				block := generator.BaseBlockAt(pos)
				switch block {
				case core.CoalOreID:
					coalSeen = true
					if y >= 96 {
						t.Fatalf("煤矿出现在 Y=%d", y)
					}
				case core.IronOreID:
					ironSeen = true
					if y >= 48 {
						t.Fatalf("铁矿出现在 Y=%d", y)
					}
				}
			}
		}
	}
	if !coalSeen || !ironSeen {
		t.Fatalf("固定采样未命中矿石: coal=%v iron=%v", coalSeen, ironSeen)
	}
}

func TestOreNeverReplacesNonStone(t *testing.T) {
	// 同一坐标在没有矿石规则时的基础方块必须是石头，才允许出现矿石。
	generator := worldgen.New(7, false)
	for x := int32(-40); x < 40; x++ {
		for z := int32(-40); z < 40; z++ {
			height := generator.HeightAt(x, z)
			for _, y := range []int32{core.MinY, height, height - 1} {
				pos := core.BlockPos{X: x, Y: y, Z: z}
				block := generator.BaseBlockAt(pos)
				// 基岩层、地表草层与紧邻的泥土层永远不会是矿石。
				if block == core.CoalOreID || block == core.IronOreID {
					t.Fatalf("非石头位置 %+v 生成了矿石", pos)
				}
			}
		}
	}
}

func TestOreNeverReplacesNaturalGravel(t *testing.T) {
	pos := core.BlockPos{X: -256, Y: 54, Z: -200}
	if got := worldgen.New(42, false).BaseBlockAt(pos); got != core.GravelID {
		t.Fatalf("自然砾石 %+v 被矿石覆盖为 %d", pos, got)
	}
}

func TestBaseBlockAtMatchesGeneratedChunkWithOre(t *testing.T) {
	generator := worldgen.New(42, false)
	for _, chunkPos := range []core.ChunkPos{{}, {X: -3, Z: 7}, {X: 5, Z: -2}} {
		chunk := generator.GenerateChunk(chunkPos)
		baseX := chunkPos.X << core.SectionShift
		baseZ := chunkPos.Z << core.SectionShift
		for lx := range core.SectionSize {
			for lz := range core.SectionSize {
				for y := int32(core.MinY); y < core.MaxY; y++ {
					want := generator.BaseBlockAt(core.BlockPos{
						X: baseX + int32(lx), Y: y, Z: baseZ + int32(lz),
					})
					if got := chunk.BlockAt(lx, y, lz); got != want {
						t.Fatalf("区块 %+v 局部 (%d,%d,%d) = %d，单点查询 = %d",
							chunkPos, lx, y, lz, got, want)
					}
				}
			}
		}
	}
}
