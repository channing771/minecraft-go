package mesh_test

import (
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/mesh"
	"minecraft-go/internal/world"
)

// skyWorld 造出 3×3 个区块，并在 y=64 铺满一层实心地面。
// roofX 中的局部 x 列会在 y=80 额外加一块屋顶。
func skyWorld(t *testing.T, roofX map[int]bool) (*world.Neighborhood, int) {
	t.Helper()
	chunks := make(map[core.ChunkPos]*world.Chunk, 9)
	for dx := int32(-1); dx <= 1; dx++ {
		for dz := int32(-1); dz <= 1; dz++ {
			pos := core.ChunkPos{X: dx, Z: dz}
			c := world.NewChunk(pos)
			for lz := 0; lz < core.SectionSize; lz++ {
				for lx := 0; lx < core.SectionSize; lx++ {
					c.SetBlock(lx, 64, lz, world.BlockID(2))
					if pos == (core.ChunkPos{}) && roofX[lx] {
						c.SetBlock(lx, 80, lz, world.BlockID(2))
					}
				}
			}
			chunks[pos] = c
		}
	}
	get := func(pos core.ChunkPos) *world.Chunk { return chunks[pos] }
	si := int(64-core.MinY) >> core.SectionShift
	n := world.NeighborhoodAt(get, core.ChunkPos{}, si)
	if n == nil {
		t.Fatal("NeighborhoodAt 返回 nil")
	}
	return n, int(64-core.MinY) & core.SectionMask
}

func TestMeshSectionOpenSkyTopFaceIsFullyLit(t *testing.T) {
	n, localY := skyWorld(t, nil)
	quads := mesh.MeshSection(n, testRegistry{})

	found := false
	for _, q := range quads {
		if q.Face != mesh.FacePosY || int(q.Y) != localY {
			continue
		}
		found = true
		if q.Light != 0xF0 {
			t.Fatalf("露天顶面 Light = %#x，想要 0xF0", q.Light)
		}
	}
	if !found {
		t.Fatal("没有生成任何露天顶面")
	}
}

func TestMeshSectionRoofedTopFaceHasNoSkyLight(t *testing.T) {
	roof := map[int]bool{}
	for lx := 0; lx < core.SectionSize; lx++ {
		roof[lx] = true
	}
	n, localY := skyWorld(t, roof)
	quads := mesh.MeshSection(n, testRegistry{})

	found := false
	for _, q := range quads {
		if q.Face != mesh.FacePosY || int(q.Y) != localY {
			continue
		}
		found = true
		if q.Light != 0x00 {
			t.Fatalf("屋顶下顶面 Light = %#x，想要 0x00", q.Light)
		}
	}
	if !found {
		t.Fatal("没有生成任何屋顶下顶面")
	}
}

func TestMeshSectionDoesNotMergeAcrossSkyLightBoundary(t *testing.T) {
	roof := map[int]bool{}
	for lx := 0; lx < 8; lx++ {
		roof[lx] = true
	}
	n, localY := skyWorld(t, roof)
	quads := mesh.MeshSection(n, testRegistry{})

	// FacePosY 的 quad 沿 z 展开 W、沿 x 展开 H，屋顶边界在 x=8。
	var lit, dark int
	for _, q := range quads {
		if q.Face != mesh.FacePosY || int(q.Y) != localY {
			continue
		}
		switch q.Light {
		case 0xF0:
			lit += int(q.W) * int(q.H)
			if int(q.X) < 8 {
				t.Fatalf("露天 quad 越过屋顶边界：X=%d H=%d", q.X, q.H)
			}
		case 0x00:
			dark += int(q.W) * int(q.H)
			if int(q.X)+int(q.H) > 8 {
				t.Fatalf("屋顶下 quad 越过边界：X=%d H=%d", q.X, q.H)
			}
		default:
			t.Fatalf("意外的顶面 Light = %#x", q.Light)
		}
	}
	if lit != 8*core.SectionSize || dark != 8*core.SectionSize {
		t.Fatalf("露天/遮蔽面积 = %d/%d，各想要 %d", lit, dark, 8*core.SectionSize)
	}
}

func TestMeshSectionMissingNeighborHasNoSkyLight(t *testing.T) {
	// 只加载中心区块，四周邻区缺失时必须按遮挡处理。
	center := world.NewChunk(core.ChunkPos{})
	for lz := 0; lz < core.SectionSize; lz++ {
		for lx := 0; lx < core.SectionSize; lx++ {
			center.SetBlock(lx, 64, lz, world.BlockID(2))
		}
	}
	get := func(pos core.ChunkPos) *world.Chunk {
		if pos == (core.ChunkPos{}) {
			return center
		}
		return nil
	}
	si := int(64-core.MinY) >> core.SectionShift
	n := world.NeighborhoodAt(get, core.ChunkPos{}, si)
	localY := int(64-core.MinY) & core.SectionMask

	if got := n.SkyLight(-1, localY+1, 0); got != 0 {
		t.Fatalf("缺失邻区天空光 = %d，想要 0", got)
	}
	quads := mesh.MeshSection(n, testRegistry{})
	for _, q := range quads {
		if q.Face == mesh.FacePosY && int(q.Y) == localY && q.Light != 0xF0 {
			t.Fatalf("中心区块顶面 Light = %#x，想要 0xF0", q.Light)
		}
	}
}
