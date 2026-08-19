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
			// MossyCobblestoneID+1 现在是已注册的流体 WaterSourceID，不再是
			// 未知方块；真正越界、未注册的编号是 WaterLevel7ID+1。
			center.Blocks.Set(8, 8, 8, core.WaterLevel7ID+1)
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
		// WaterLevel7ID+1 是真正越界、未注册的编号：覆盖「registry 里完全不
		// 存在」这条路径。
		core.WaterLevel7ID + 1,
		// WaterSourceID 是已注册流体，但没有被纳入 assets.NewRegistry() 构建
		// mesh snapshot 时使用的 ids 范围（仍止于 MossyCobblestoneID），所以
		// 从 snapshot 的角度看同样是「缺条目」：覆盖「已注册但不在 snapshot
		// 里」这条独立路径，与上面的 WaterLevel7ID+1 分开断言。
		core.WaterSourceID,
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
