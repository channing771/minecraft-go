package mesh_test

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"github.com/channing771/mornlea/internal/assets"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/mesh"
	"github.com/channing771/mornlea/internal/world"
)

func assertNativeOracleParity(t *testing.T, n *world.Neighborhood, reg mesh.Registry) []mesh.Quad {
	t.Helper()
	want := meshSectionGoOracle(n, reg, newGoLightScratch())
	got := mesh.MeshSection(n, reg, mesh.NewLightScratch())
	if len(got) != len(want) {
		t.Fatalf("native=%d，oracle=%d", len(got), len(want))
	}
	for i := range want {
		if got[i].Pack() != want[i].Pack() {
			t.Fatalf("quad[%d]=%#016x，oracle=%#016x\ngot=%+v\nwant=%+v",
				i, got[i].Pack(), want[i].Pack(), got[i], want[i])
		}
	}
	return got
}

func TestNativeOracleParityBenchmarkTerrain(t *testing.T) {
	quads := assertNativeOracleParity(t, benchmarkTerrainNeighborhood(), testRegistry{})
	if len(quads) != 2016 {
		t.Fatalf("terrain benchmark quads=%d，想要 2016", len(quads))
	}
}

func TestNativeOracleParityFixedCorpus(t *testing.T) {
	assetRegistry := assets.NewRegistry()
	cases := []struct {
		name  string
		build func(*testing.T) (*world.Neighborhood, mesh.Registry)
	}{
		{"empty", func(*testing.T) (*world.Neighborhood, mesh.Registry) {
			return solidNeighbors(world.NewSection()), nil
		}},
		{"isolated", func(*testing.T) (*world.Neighborhood, mesh.Registry) {
			center := world.NewSection()
			center.Blocks.Set(8, 8, 8, core.StoneID)
			return solidNeighbors(center), testRegistry{}
		}},
		{"full", func(*testing.T) (*world.Neighborhood, mesh.Registry) {
			center := world.NewSection()
			fillOracleSection(center, core.StoneID)
			return solidNeighbors(center), testRegistry{}
		}},
		{"flat-slab", func(*testing.T) (*world.Neighborhood, mesh.Registry) {
			center := world.NewSection()
			for y := range 8 {
				for z := range core.SectionSize {
					for x := range core.SectionSize {
						center.Blocks.Set(x, y, z, core.StoneID)
					}
				}
			}
			return slabNeighbors(center, 7), testRegistry{}
		}},
		{"split-material", func(*testing.T) (*world.Neighborhood, mesh.Registry) {
			center := world.NewSection()
			for z := range core.SectionSize {
				for x := range core.SectionSize {
					id := world.BlockID(core.StoneID)
					if x >= core.SectionSize/2 {
						id = core.DirtID
					}
					center.Blocks.Set(x, 0, z, id)
				}
			}
			return slabNeighbors(center, 0), testRegistry{}
		}},
		{"glass-cutout", func(*testing.T) (*world.Neighborhood, mesh.Registry) {
			center := world.NewSection()
			center.Blocks.Set(7, 8, 8, core.GlassID)
			center.Blocks.Set(8, 8, 8, core.LeavesID)
			return solidNeighbors(center), assetRegistry
		}},
		{"unknown-id", func(*testing.T) (*world.Neighborhood, mesh.Registry) {
			center := world.NewSection()
			// 未注册编号一律用独占哨兵 core.BlockIDMax 表达：写死具体编号
			// （历史上写过 MossyCobblestoneID+1、WaterLevel7ID+1）会在追加
			// 新方块时静默变成已注册，本用例就不再覆盖未知方块那条路径。
			center.Blocks.Set(8, 8, 8, core.BlockIDMax)
			return solidNeighbors(center), assetRegistry
		}},
		{"sky-edge", func(t *testing.T) (*world.Neighborhood, mesh.Registry) {
			n, _ := propagatedSkyWorld(t, -1, nil)
			return n, testRegistry{}
		}},
		{"block-light-corridor", func(t *testing.T) (*world.Neighborhood, mesh.Registry) {
			const floorY int32 = 64
			n, _ := blockLightCorridor(t, floorY, map[core.BlockPos]world.BlockID{
				{X: 0, Y: floorY + 1, Z: 8}: core.LightBlockID,
			})
			return n, testRegistry{}
		}},
		{"missing-neighbor", func(*testing.T) (*world.Neighborhood, mesh.Registry) {
			chunk := world.NewChunk(core.ChunkPos{})
			chunk.SetBlock(0, 64, 8, core.StoneID)
			return world.NeighborhoodAt(func(pos core.ChunkPos) *world.Chunk {
				if pos == (core.ChunkPos{}) {
					return chunk
				}
				return nil
			}, core.ChunkPos{}, core.BlockPos{Y: 64}.SectionIndex()), testRegistry{}
		}},
		{"world-height-boundary", func(*testing.T) (*world.Neighborhood, mesh.Registry) {
			chunks := make(map[core.ChunkPos]*world.Chunk, 9)
			for x := int32(-1); x <= 1; x++ {
				for z := int32(-1); z <= 1; z++ {
					pos := core.ChunkPos{X: x, Z: z}
					chunks[pos] = world.NewChunk(pos)
				}
			}
			chunks[core.ChunkPos{}].SetBlock(8, core.MinY, 8, core.StoneID)
			return world.NeighborhoodAt(func(pos core.ChunkPos) *world.Chunk { return chunks[pos] }, core.ChunkPos{}, 0), testRegistry{}
		}},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			n, reg := tt.build(t)
			assertNativeOracleParity(t, n, reg)
		})
	}
}

func TestNativeOracleParityDeterministicRandomizedCorpus(t *testing.T) {
	rng := rand.New(rand.NewSource(0x4d3450))
	reg := assets.NewRegistry()
	ids := []world.BlockID{
		core.AirID,
		core.StoneID,
		core.DirtID,
		core.GlassID,
		core.LeavesID,
		core.LightBlockID,
		// core.BlockIDMax 是未注册编号的独占哨兵：覆盖「registry 里完全不
		// 存在」这条路径，且不会随新方块追加而失效。
		core.BlockIDMax,
		// 流体已纳入 assets.NewRegistry() 的 snapshot ids 范围，会真的产生
		// 几何。放两个不同等级，让「流体—流体」「流体—固体」「流体—空气」
		// 三类相邻都出现在随机语料里。
		core.WaterSourceID,
		core.WaterLevel5ID,
		// 农业方块同样已在 snapshot 范围内：耕地是普通不透明方块，作物走交叉斜面
		// 那条独立的出面路径。放进随机语料后，「作物—作物」「作物—固体」
		// 「作物—空气」「作物—流体」四类相邻都会出现。
		core.FarmlandDryID,
		core.WheatStage3ID,
	}

	for caseIndex := range 64 {
		n := &world.Neighborhood{Center: world.NewSection(), SectionY: rng.Intn(core.SectionsPerChunk)}
		for cx := range 3 {
			for cy := range 3 {
				for cz := range 3 {
					if cx == 1 && cy == 1 && cz == 1 {
						continue
					}
					if rng.Intn(8) == 0 {
						continue
					}
					n.Around[cx][cy][cz] = randomParitySection(rng, ids)
				}
			}
		}
		n.Center = randomParitySection(rng, ids)
		n.Center.Blocks.Set(caseIndex&15, caseIndex>>4, (caseIndex*7)&15, ids[1+caseIndex%(len(ids)-1)])
		for cx := range 3 {
			for cz := range 3 {
				n.HeightsPresent[cx][cz] = rng.Intn(2) == 0
				if !n.HeightsPresent[cx][cz] {
					continue
				}
				for i := range n.Heights[cx][cz] {
					n.Heights[cx][cz][i] = int16(core.MinY - 1 + rng.Intn(core.MaxY-core.MinY+1))
				}
			}
		}

		t.Run(fmt.Sprintf("case-%02d", caseIndex), func(t *testing.T) {
			assertNativeOracleParity(t, n, reg)
		})
	}
}

func randomParitySection(rng *rand.Rand, ids []world.BlockID) *world.Section {
	section := world.NewSection()
	for range 48 {
		section.Blocks.Set(rng.Intn(core.SectionSize), rng.Intn(core.SectionSize), rng.Intn(core.SectionSize), ids[rng.Intn(len(ids))])
	}
	return section
}

func TestNativeOracleParityConcurrentIndependentScratch(t *testing.T) {
	const (
		workers    = 8
		iterations = 100
		floorY     = int32(64)
	)
	n, _ := blockLightCorridor(t, floorY, map[core.BlockPos]world.BlockID{
		{X: 0, Y: floorY + 1, Z: 8}: core.LightBlockID,
		{X: 7, Y: floorY + 1, Z: 8}: core.GlassID,
	})
	reg := assets.NewRegistry()
	want := meshSectionGoOracle(n, reg, newGoLightScratch())
	failures := make(chan string, workers)
	var wait sync.WaitGroup
	wait.Add(workers)
	for worker := range workers {
		go func() {
			defer wait.Done()
			scratch := mesh.NewLightScratch()
			for iteration := range iterations {
				got := mesh.MeshSection(n, reg, scratch)
				if len(got) != len(want) {
					failures <- fmt.Sprintf("worker=%d iteration=%d quad count=%d，oracle=%d", worker, iteration, len(got), len(want))
					return
				}
				for i := range want {
					if got[i].Pack() != want[i].Pack() {
						failures <- fmt.Sprintf("worker=%d iteration=%d quad[%d]=%#016x，oracle=%#016x", worker, iteration, i, got[i].Pack(), want[i].Pack())
						return
					}
				}
			}
		}()
	}
	wait.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
}

// TestNativeOracleParityWaterSurface 覆盖流体纳入 mesh registry 快照之后的 Go/Rust 一致性。
//
// **assertNativeOracleParity 守的不是出面规则**。Go oracle 与 Rust 位图同源于
// assets.Registry.FaceVisible：oracle 每格现算，Rust 读的是 BuildRegistrySnapshot 把同一
// 函数烘焙进 Visibility 位图、再经 encodeNativeInput 送过去的那一份。规则本身被改坏时
// 两侧会**一起**改坏、差值恒等，两条 parity 断言照样全绿（任务组 1 评审实测：把已删除的
// `core.IsFluid` 补偿分支加回 FaceVisible 后 parity 全过，变红的是下面那条计数守卫）。
//
// parity 断言真正守的是端到端事实：整份 registry 快照（全部已注册方块）确实通过了
// Rust 的条目校验、
// 编码布局两侧一致、且贪心合并与位打包逐字节相同。**规则由末尾的计数守卫承重**——
// 若把它删掉，本用例对任何规则类变异都不再敏感。
//
// 夹具里水面之上单独放了一块石头，用来覆盖「流体紧邻不透明方块的面不可见」与其反方向
// 「不透明方块朝向流体的面可见」两条规则。
func TestNativeOracleParityWaterSurface(t *testing.T) {
	// build 造一个下半部为 fill、上方 (3,8,3) 放一块石头的中心区段，邻居全实心。
	// fill 传 AirID 时就是同一夹具的「无水对照组」。
	build := func(fill world.BlockID) *world.Neighborhood {
		center := world.NewSection()
		for y := range 8 {
			for z := range core.SectionSize {
				for x := range core.SectionSize {
					id := fill
					// 掺入不同等级的流动水，让「流体—流体」相邻也进入判定，
					// 而不是只覆盖单一编号。
					if core.IsFluid(fill) && x >= core.SectionSize/2 {
						id = core.WaterLevel3ID
					}
					center.Blocks.Set(x, y, z, id)
				}
			}
		}
		center.Blocks.Set(3, 8, 3, core.StoneID)
		return solidNeighbors(center)
	}

	registry := assets.NewRegistry()
	water := assertNativeOracleParity(t, build(core.WaterSourceID), registry)
	air := assertNativeOracleParity(t, build(core.AirID), registry)

	// 防空转守卫排在真实故障断言之后：一致性断言在「两侧都不给水出面」时同样成立，
	// 所以必须另外证明水**确实**贡献了几何。把水换成空气后 quad 只会更少（水面顶面
	// 全部消失，只剩那块孤立石头），若水版不严格多于空气版，说明水仍然画不出来。
	if len(water) <= len(air) {
		t.Fatalf("水版 quad=%d，空气对照组 quad=%d：水没有贡献任何几何，"+
			"上面的一致性断言已退化为「两侧都不出面」的恒真", len(water), len(air))
	}

	// 水面必须真的带上角高度且按 1×1 出面。角高度经 Rust 打包、Go 解包、
	// 上传前再打包，任一环节丢位都会让这里读到零角高度。
	tops := 0
	for _, quad := range water {
		if quad.Face != mesh.FacePosY || quad.Corners == ([4]uint8{}) {
			continue
		}
		tops++
		if quad.W != 1 || quad.H != 1 {
			t.Fatalf("水面顶面 %+v 被贪心合并成 %dx%d", quad, quad.W, quad.H)
		}
		for i, corner := range quad.Corners {
			if corner < 7 || corner > 15 {
				t.Fatalf("水面顶面 %+v 的角 %d 高度=%d，合法域是 7..15", quad, i, corner)
			}
		}
	}
	if tops == 0 {
		t.Fatal("没有任何带角高度的水面顶面：斜面几何整体缺失")
	}
}
