package mesh_test

import (
	"testing"

	"minecraft-go/internal/assets"
	"minecraft-go/internal/core"
	"minecraft-go/internal/mesh"
	"minecraft-go/internal/world"
)

type testRegistry struct{}

func (testRegistry) Opaque(id world.BlockID) bool { return id != world.AirID }
func (testRegistry) FaceVisible(id, adjacent world.BlockID) bool {
	return id != world.AirID && adjacent == world.AirID
}
func (testRegistry) Material(id world.BlockID, _ mesh.Face) uint16 {
	return uint16(id)
}
func (testRegistry) Emission(id world.BlockID) uint8 {
	if id == core.LightBlockID {
		return 15
	}
	return 0
}

func (r testRegistry) MeshSnapshot() mesh.RegistrySnapshot {
	snapshot, err := mesh.BuildRegistrySnapshot([]world.BlockID{
		core.AirID,
		core.BarrierID,
		core.StoneID,
		core.DirtID,
		core.GrassID,
		core.StoneBrickID,
		core.LightBlockID,
	}, r)
	if err != nil {
		panic(err)
	}
	return snapshot
}

type materialCallRegistry struct {
	*assets.Registry
	calls int
}

func (r *materialCallRegistry) Material(id world.BlockID, face mesh.Face) uint16 {
	r.calls++
	return r.Registry.Material(id, face)
}

func solidNeighbors(center *world.Section) *world.Neighborhood {
	solid := world.NewSection()
	for y := 0; y < 16; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				solid.Blocks.Set(x, y, z, world.BlockID(2))
			}
		}
	}
	n := &world.Neighborhood{Center: center}
	for dx := 0; dx < 3; dx++ {
		for dy := 0; dy < 3; dy++ {
			for dz := 0; dz < 3; dz++ {
				if dx == 1 && dy == 1 && dz == 1 {
					continue
				}
				n.Around[dx][dy][dz] = solid
			}
		}
	}
	return n
}

// slabNeighbors 在 topY 及以下填实水平邻居，并把更高处保持为空气。
// 这样能封住平板的侧面/底面，同时不会用一圈高墙污染顶面边缘的 AO。
func slabNeighbors(center *world.Section, topY int) *world.Neighborhood {
	n := &world.Neighborhood{Center: center}
	for dx := 0; dx < 3; dx++ {
		for dy := 0; dy < 3; dy++ {
			for dz := 0; dz < 3; dz++ {
				if dx == 1 && dy == 1 && dz == 1 {
					continue
				}
				s := world.NewSection()
				switch dy {
				case 0: // 下方区段全实心，封住底面。
					for y := 0; y < 16; y++ {
						for z := 0; z < 16; z++ {
							for x := 0; x < 16; x++ {
								s.Blocks.Set(x, y, z, world.BlockID(2))
							}
						}
					}
				case 1: // 水平邻居只填到平板高度。
					for y := 0; y <= topY; y++ {
						for z := 0; z < 16; z++ {
							for x := 0; x < 16; x++ {
								s.Blocks.Set(x, y, z, world.BlockID(2))
							}
						}
					}
				}
				n.Around[dx][dy][dz] = s
			}
		}
	}
	return n
}

func TestMeshEmptySectionProducesNothing(t *testing.T) {
	n := solidNeighbors(world.NewSection())
	if q := mesh.MeshSection(n, testRegistry{}, mesh.NewLightScratch()); len(q) != 0 {
		t.Fatalf("全空气区段产生了 %d 个面，应为 0", len(q))
	}
}

func TestMeshUnknownBlockDoesNotSelectMaterial(t *testing.T) {
	center := world.NewSection()
	center.Blocks.Set(8, 8, 8, core.MossyCobblestoneID+1)
	registry := &materialCallRegistry{Registry: assets.NewRegistry()}
	if quads := mesh.MeshSection(solidNeighbors(center), registry, mesh.NewLightScratch()); len(quads) != 0 {
		t.Fatalf("未知方块产生了 %d 个面，想要 0", len(quads))
	}
	if registry.calls != 0 {
		t.Fatalf("未知方块调用了 Material %d 次，想要 0", registry.calls)
	}
}

func TestMeshFullSectionProducesNothing(t *testing.T) {
	center := world.NewSection()
	for y := 0; y < 16; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				center.Blocks.Set(x, y, z, world.BlockID(2))
			}
		}
	}
	n := solidNeighbors(center)
	if q := mesh.MeshSection(n, testRegistry{}, mesh.NewLightScratch()); len(q) != 0 {
		t.Fatalf("被实心邻居包围的实心区段产生了 %d 个面，应为 0", len(q))
	}
}

func TestMeshSingleBlockProducesSixUnitQuads(t *testing.T) {
	center := world.NewSection()
	center.Blocks.Set(8, 8, 8, world.BlockID(2))
	quads := mesh.MeshSection(solidNeighbors(center), testRegistry{}, mesh.NewLightScratch())
	if len(quads) != 6 {
		t.Fatalf("孤立方块产生了 %d 个面，应为 6", len(quads))
	}
	seen := map[mesh.Face]bool{}
	for _, q := range quads {
		if q.W != 1 || q.H != 1 {
			t.Fatalf("孤立方块的面尺寸 = %dx%d，应为 1x1", q.W, q.H)
		}
		if q.AO != 0xFF {
			t.Fatalf("孤立方块的面 AO = %#02x，应为四角全亮 0xff", q.AO)
		}
		if seen[q.Face] {
			t.Fatalf("面 %d 重复出现", q.Face)
		}
		seen[q.Face] = true
	}
}

func TestMeshGreedyMergesFlatSurface(t *testing.T) {
	center := world.NewSection()
	for y := 0; y < 8; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				center.Blocks.Set(x, y, z, world.BlockID(2))
			}
		}
	}
	quads := mesh.MeshSection(slabNeighbors(center, 7), testRegistry{}, mesh.NewLightScratch())
	if len(quads) != 1 {
		t.Fatalf("平坦顶面产生了 %d 个面，贪心合并后应为 1", len(quads))
	}
	q := quads[0]
	if q.Face != mesh.FacePosY || q.W != 16 || q.H != 16 || q.Y != 7 {
		t.Fatalf("平坦顶面结果错误: %+v", q)
	}
}

func TestMeshDoesNotMergeAcrossMaterials(t *testing.T) {
	center := world.NewSection()
	for z := 0; z < 16; z++ {
		for x := 0; x < 16; x++ {
			id := world.BlockID(2)
			if x >= 8 {
				id = world.BlockID(3)
			}
			center.Blocks.Set(x, 0, z, id)
		}
	}
	quads := mesh.MeshSection(slabNeighbors(center, 0), testRegistry{}, mesh.NewLightScratch())
	if len(quads) != 2 {
		t.Fatalf("两种材质的平面产生了 %d 个面，应为 2", len(quads))
	}
	for _, q := range quads {
		// Y 面的面内轴按契约是 u=Z、v=X，所以 X 方向的一半编码在 H。
		if q.W != 16 || q.H != 8 {
			t.Fatalf("面尺寸 = %dx%d，应为 16x8", q.W, q.H)
		}
	}
}

func TestMeshCutoutFaces(t *testing.T) {
	reg := assets.NewRegistry()

	t.Run("孤立玻璃产生六个面", func(t *testing.T) {
		center := world.NewSection()
		center.Blocks.Set(8, 8, 8, core.GlassID)
		if got := len(mesh.MeshSection(solidNeighbors(center), reg, mesh.NewLightScratch())); got != 6 {
			t.Fatalf("孤立玻璃产生了 %d 个面，想要 6", got)
		}
	})

	for _, tt := range []struct {
		name        string
		left, right core.BlockID
	}{
		{"相邻玻璃", core.GlassID, core.GlassID},
		{"相邻树叶", core.LeavesID, core.LeavesID},
		{"不同 cutout", core.GlassID, core.LeavesID},
	} {
		t.Run(tt.name, func(t *testing.T) {
			center := world.NewSection()
			center.Blocks.Set(7, 8, 8, tt.left)
			center.Blocks.Set(8, 8, 8, tt.right)
			quads := mesh.MeshSection(solidNeighbors(center), reg, mesh.NewLightScratch())
			for _, q := range quads {
				if q.Face == mesh.FacePosX && q.X == 7 || q.Face == mesh.FaceNegX && q.X == 8 {
					t.Fatalf("cutout 边界产生了重叠内部面: %+v", q)
				}
			}
		})
	}

	t.Run("石头与玻璃只保留石头面", func(t *testing.T) {
		center := world.NewSection()
		center.Blocks.Set(7, 8, 8, core.StoneID)
		center.Blocks.Set(8, 8, 8, core.GlassID)
		quads := mesh.MeshSection(solidNeighbors(center), reg, mesh.NewLightScratch())
		stoneFace, glassFace := false, false
		for _, q := range quads {
			stoneFace = stoneFace || q.Face == mesh.FacePosX && q.X == 7
			glassFace = glassFace || q.Face == mesh.FaceNegX && q.X == 8
		}
		if !stoneFace || glassFace {
			t.Fatalf("stone/glass 边界 stone=%v glass=%v，想要 true/false", stoneFace, glassFace)
		}
	})
}

func TestMeshCutoutDoesNotOccludeAOOrSkyLight(t *testing.T) {
	reg := assets.NewRegistry()
	center := world.NewSection()
	center.Blocks.Set(8, 8, 8, core.StoneID)
	center.Blocks.Set(7, 9, 8, core.GlassID)
	center.Blocks.Set(8, 9, 7, core.LeavesID)
	center.Blocks.Set(7, 9, 7, core.GlassID)
	quads := mesh.MeshSection(solidNeighbors(center), reg, mesh.NewLightScratch())
	found := false
	for _, q := range quads {
		if q.Face == mesh.FacePosY && q.X == 8 && q.Y == 8 && q.Z == 8 {
			found = true
			if q.AO != 0xff {
				t.Fatalf("cutout 邻居下石头顶面 AO = %#02x，想要 0xff", q.AO)
			}
		}
	}
	if !found {
		t.Fatal("没有找到石头顶面")
	}

	n, localY := propagatedSkyWorld(t, 0, nil)
	n.Center.Blocks.Set(7, localY+1, 8, core.GlassID)
	quads = mesh.MeshSection(n, reg, mesh.NewLightScratch())
	if got := topFaceLightAt(t, quads, 8, localY, 8) >> 4; got != 7 {
		t.Fatalf("穿过玻璃后的天空光 = %d，想要 7", got)
	}
}

func benchmarkTerrainNeighborhood() *world.Neighborhood {
	center := world.NewSection()
	for z := 0; z < 16; z++ {
		for x := 0; x < 16; x++ {
			h := 4 + (x*3+z*5)%8
			for y := 0; y <= h; y++ {
				center.Blocks.Set(x, y, z, world.BlockID(2+(x+z)%3))
			}
		}
	}
	return solidNeighbors(center)
}

func BenchmarkMeshTerrainSection(b *testing.B) {
	n := benchmarkTerrainNeighborhood()
	reg := testRegistry{}
	light := mesh.NewLightScratch()
	var quads []mesh.Quad
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		quads = mesh.MeshSection(n, reg, light)
	}
	b.ReportMetric(float64(len(quads)), "quads/op")
	b.ReportMetric(float64(len(quads)*8), "upload_bytes/op")
}
