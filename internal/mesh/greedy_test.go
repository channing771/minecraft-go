package mesh_test

import (
	"testing"

	"minecraft-go/internal/mesh"
	"minecraft-go/internal/world"
)

type testRegistry struct{}

func (testRegistry) Opaque(id world.BlockID) bool { return id != world.AirID }
func (testRegistry) Material(id world.BlockID, _ mesh.Face) uint16 {
	return uint16(id)
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
	if q := mesh.MeshSection(n, testRegistry{}); len(q) != 0 {
		t.Fatalf("全空气区段产生了 %d 个面，应为 0", len(q))
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
	if q := mesh.MeshSection(n, testRegistry{}); len(q) != 0 {
		t.Fatalf("被实心邻居包围的实心区段产生了 %d 个面，应为 0", len(q))
	}
}

func TestMeshSingleBlockProducesSixUnitQuads(t *testing.T) {
	center := world.NewSection()
	center.Blocks.Set(8, 8, 8, world.BlockID(2))
	quads := mesh.MeshSection(solidNeighbors(center), testRegistry{})
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
	quads := mesh.MeshSection(slabNeighbors(center, 7), testRegistry{})
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
	quads := mesh.MeshSection(slabNeighbors(center, 0), testRegistry{})
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

func BenchmarkMeshTerrainSection(b *testing.B) {
	center := world.NewSection()
	for z := 0; z < 16; z++ {
		for x := 0; x < 16; x++ {
			h := 4 + (x*3+z*5)%8
			for y := 0; y <= h; y++ {
				center.Blocks.Set(x, y, z, world.BlockID(2+(x+z)%3))
			}
		}
	}
	n := solidNeighbors(center)
	reg := testRegistry{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = mesh.MeshSection(n, reg)
	}
}
