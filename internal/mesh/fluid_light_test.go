package mesh_test

import (
	"testing"

	"github.com/channing771/mornlea/internal/assets"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/mesh"
	"github.com/channing771/mornlea/internal/world"
)

// localSectionY 把世界 Y 折算成区段内局部 Y，与 MeshSection 输出的 Quad.Y 同口径。
func localSectionY(worldY int32) int { return int(worldY-core.MinY) & core.SectionMask }

// openWaterWorld 造 3×3 个区块的**露天水体**：
//
//   - `y == floorY` 铺满石头湖底；
//   - `floorY < y <= surfaceY` 铺满水源方块；
//   - 水面之上全是空气，没有任何非流体遮挡。
//
// extra 在水体铺好之后覆盖写入，用来插入探针石块或水下气室。
// 三条边都铺满同一套方块是刻意的：单根水柱测不出竖直衰减——旁边的空气会以每格 1
// 的代价把光送到同样深度再横向灌进来，读数被空气路径而不是水路径决定。
func openWaterWorld(
	t *testing.T,
	floorY, surfaceY int32,
	extra map[core.BlockPos]world.BlockID,
) *world.Neighborhood {
	t.Helper()
	chunks := make(map[core.ChunkPos]*world.Chunk, 9)
	for cx := int32(-1); cx <= 1; cx++ {
		for cz := int32(-1); cz <= 1; cz++ {
			pos := core.ChunkPos{X: cx, Z: cz}
			chunk := world.NewChunk(pos)
			for lz := 0; lz < core.SectionSize; lz++ {
				for lx := 0; lx < core.SectionSize; lx++ {
					chunk.SetBlock(lx, floorY, lz, core.StoneID)
					for y := floorY + 1; y <= surfaceY; y++ {
						chunk.SetBlock(lx, y, lz, core.WaterSourceID)
					}
				}
			}
			chunks[pos] = chunk
		}
	}
	for position, id := range extra {
		chunk := chunks[position.Chunk()]
		if chunk == nil {
			t.Fatalf("方块位置 %+v 超出测试邻域", position)
		}
		lx, _, lz := position.Local()
		chunk.SetBlock(lx, position.Y, lz, id)
	}
	n := world.NeighborhoodAt(
		func(pos core.ChunkPos) *world.Chunk { return chunks[pos] },
		core.ChunkPos{},
		core.BlockPos{Y: floorY}.SectionIndex(),
	)
	if n == nil {
		t.Fatal("NeighborhoodAt 返回 nil")
	}
	return n
}

// TestSkyLightDimsWithDepthUnderOpenWater 覆盖三条光照 Scenario：
// authoritative-daylight 的「水面之下随深度变暗但不立刻归零」，以及 fluid-presentation
// 的「水下随深度变暗」与「浅水下方仍然可见」。
//
// 读数方式：在每个深度各插一块石头探针，读它顶面的天空光——顶面采样的正是它**上方
// 那一格水**。探针恒在被测水格之下，不会挡住这格水自己的进光路径。
//
// 期望值来自「每格扣减 = 固定的 1 + 水的 light_attenuation 1」：深度 d 处为 15-2d，
// 到 d=8 归零。水体做成 12 格深、探针铺到 d=8，是为了让夹具落在**「衰减 1」与
// 「衰减 0」必然分歧**的区间上：一格深的夹具两种规则会给出同一批读数。
func TestSkyLightDimsWithDepthUnderOpenWater(t *testing.T) {
	const (
		floorY   int32 = 64
		surfaceY int32 = 76
		deepest  int32 = 8
	)
	probes := make(map[core.BlockPos]world.BlockID, deepest)
	for depth := int32(1); depth <= deepest; depth++ {
		probes[core.BlockPos{X: depth, Y: surfaceY - depth, Z: 8}] = core.StoneID
	}
	n := openWaterWorld(t, floorY, surfaceY, probes)
	quads := mesh.MeshSection(n, assets.NewRegistry(), mesh.NewLightScratch())

	want := [deepest]uint8{13, 11, 9, 7, 5, 3, 1, 0}
	previous := uint8(15)
	for depth := int32(1); depth <= deepest; depth++ {
		got := skyLight(topFaceLightAt(t, quads, int(depth), localSectionY(surfaceY-depth), 8))
		if got != want[depth-1] {
			t.Fatalf("水下第 %d 格天空光=%d，想要 %d", depth, got, want[depth-1])
		}
		// 更深处 MUST NOT 高于更浅处。
		if got > previous {
			t.Fatalf("水下第 %d 格天空光=%d，高于更浅处的 %d", depth, got, previous)
		}
		previous = got
	}
	// 紧邻水面之下 MUST 大于 0（fluid-presentation：「浅水下方仍然可见」）。
	if want[0] == 0 {
		t.Fatal("期望表把紧邻水面之下写成了 0，与「不立刻归零」矛盾")
	}
	// 足够深处 MUST 到达 0。
	if want[deepest-1] != 0 {
		t.Fatalf("最深探针期望=%d，夹具没有深到足以归零", want[deepest-1])
	}
}

// TestFluidDoesNotLowerDirectSkyStart 覆盖 authoritative-daylight 的「流体不作为直射
// 起点的遮挡」与 fluid-presentation 的「流体不再抬高直射起点」。
//
// 夹具是一座**水下石台，台面上一格是被水封住的空气**。该列最高的非空气方块是水面
// （surfaceY），但最高的非空气**且非流体**方块是石台（ledgeY）；按规格，严格高于石台
// 的那格空气就是亮度 15 的直射起点，于是石台顶面读到 15。
//
// 若列顶判定退回「任意非空气即列顶」，这格空气就落到水面之下、拿不到直射起点，只能
// 由水面之上的空气穿五格水送进来（15→13→11→9→7→5，再进空气 -1），读到 4。
func TestFluidDoesNotLowerDirectSkyStart(t *testing.T) {
	const (
		floorY   int32 = 64
		surfaceY int32 = 76
		ledgeY   int32 = 70
	)
	n := openWaterWorld(t, floorY, surfaceY, map[core.BlockPos]world.BlockID{
		{X: 8, Y: ledgeY, Z: 8}:     core.StoneID,
		{X: 8, Y: ledgeY + 1, Z: 8}: world.AirID,
	})
	quads := mesh.MeshSection(n, assets.NewRegistry(), mesh.NewLightScratch())

	if got := skyLight(topFaceLightAt(t, quads, 8, localSectionY(ledgeY), 8)); got != 15 {
		t.Fatalf("水下气室天空光=%d，想要直射起点的 15："+
			"直射起点必须由最高的非空气且非流体方块决定，不得因水面而下移", got)
	}

	// 防空转守卫排在真实故障断言之后：上面的 15 只有在「水确实压在气室之上」时才有
	// 意义。远处湖底顶面压着 12 格水，必须已经衰减到 0；若它也是 15，说明这份夹具
	// 根本没有水的衰减，上面那条断言退化成恒真。
	if got := skyLight(topFaceLightAt(t, quads, 0, localSectionY(floorY), 0)); got != 0 {
		t.Fatalf("12 格水之下的湖底顶面天空光=%d，想要 0：夹具里的水没有衰减", got)
	}
}

// TestBlockLightIsBlockedByWaterAndGlassAlike 是 static-block-light 的回归守卫。
//
// 主规格写死「光在六个轴向上仅向 AirID 相邻格传播并每格衰减 1，任何其他方块即使未来
// 被标记为透明也阻断方块光」。本变更只放宽天空光，**方块光模型不动**：水阻断方块光
// 与玻璃的既有行为同构，是已裁决的边界，不是缺陷。
//
// 这条守卫防的是「改天空光时顺手把方块光也放开」——放开之后既有的方块光用例大概率
// 仍然全绿，因为它们的夹具里既没有水也没有玻璃。两种阻断物跑同一套断言，正是为了
// 把「水和玻璃一视同仁」这件事本身钉住。
func TestBlockLightIsBlockedByWaterAndGlassAlike(t *testing.T) {
	const floorY int32 = 64
	registry := assets.NewRegistry()
	for _, tt := range []struct {
		name    string
		blocker world.BlockID
	}{
		{"水", core.WaterSourceID},
		{"玻璃", core.GlassID},
	} {
		t.Run(tt.name, func(t *testing.T) {
			n, localY := blockLightCorridor(t, floorY, map[core.BlockPos]world.BlockID{
				{X: 0, Y: floorY + 1, Z: 8}: core.LightBlockID,
				{X: 7, Y: floorY + 1, Z: 8}: tt.blocker,
			})
			quads := mesh.MeshSection(n, registry, mesh.NewLightScratch())

			if got := blockLight(topFaceLightAt(t, quads, 6, localY, 8)); got != 9 {
				t.Fatalf("阻断物前方方块光=%d，想要 9", got)
			}
			if got := blockLight(topFaceLightAt(t, quads, 8, localY, 8)); got != 0 {
				t.Fatalf("%s 后方方块光=%d，想要 0：方块光 MUST 仅经 AirID 传播", tt.name, got)
			}
		})
	}
}
