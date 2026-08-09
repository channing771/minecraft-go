package mesh_test

import (
	"reflect"
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/mesh"
	"minecraft-go/internal/world"
)

// propagatedSkyWorld 造出一格高、一格宽的封闭通道；apertureX 是唯一露天天窗，
// blockedX 非 nil 时在通道中放置一格石头。
func propagatedSkyWorld(t *testing.T, apertureX int, blockedX *int) (*world.Neighborhood, int) {
	t.Helper()
	chunks := make(map[core.ChunkPos]*world.Chunk, 9)
	for cx := int32(-1); cx <= 1; cx++ {
		for cz := int32(-1); cz <= 1; cz++ {
			pos := core.ChunkPos{X: cx, Z: cz}
			chunk := world.NewChunk(pos)
			for lz := 0; lz < core.SectionSize; lz++ {
				worldZ := int(cz)*core.SectionSize + lz
				for lx := 0; lx < core.SectionSize; lx++ {
					worldX := int(cx)*core.SectionSize + lx
					chunk.SetBlock(lx, 64, lz, core.StoneID)
					if worldZ != 8 || blockedX != nil && worldX == *blockedX {
						chunk.SetBlock(lx, 65, lz, core.StoneID)
					}
					if worldX != apertureX || worldZ != 8 {
						chunk.SetBlock(lx, 66, lz, core.StoneID)
					}
				}
			}
			chunks[pos] = chunk
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

func topFaceLightAt(t *testing.T, quads []mesh.Quad, x, y, z int) uint8 {
	t.Helper()
	for _, quad := range quads {
		if quad.Face == mesh.FacePosY && int(quad.Y) == y &&
			int(quad.X) <= x && x < int(quad.X)+int(quad.H) &&
			int(quad.Z) <= z && z < int(quad.Z)+int(quad.W) {
			return quad.Light
		}
	}
	t.Fatalf("没有找到覆盖 (%d,%d,%d) 的顶面", x, y, z)
	return 0
}

func skyLight(light uint8) uint8   { return light >> 4 }
func blockLight(light uint8) uint8 { return light & 0x0f }

func TestMeshSectionPropagatedSkyLightLevels(t *testing.T) {
	n, localY := propagatedSkyWorld(t, 0, nil)
	quads := mesh.MeshSection(n, testRegistry{}, mesh.NewLightScratch())
	tests := []struct {
		name string
		x    int
		want uint8
	}{
		{"直射", 0, 15},
		{"一步", 1, 14},
		{"从起点计第十五格", 14, 1},
		{"下一格归零", 15, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := skyLight(topFaceLightAt(t, quads, tt.x, localY, 8)); got != tt.want {
				t.Fatalf("x=%d 天空光 = %d，想要 %d", tt.x, got, tt.want)
			}
		})
	}
}

func TestMeshSectionPropagatedSkyLightStopsAtOpaqueBlock(t *testing.T) {
	blockedX := 7
	n, localY := propagatedSkyWorld(t, 0, &blockedX)
	quads := mesh.MeshSection(n, testRegistry{}, mesh.NewLightScratch())
	if got := skyLight(topFaceLightAt(t, quads, blockedX+1, localY, 8)); got != 0 {
		t.Fatalf("遮挡后天空光 = %d，想要 0", got)
	}
}

func TestMeshSectionPropagatedSkyLightCrossesChunkBoundary(t *testing.T) {
	n, localY := propagatedSkyWorld(t, -1, nil)
	quads := mesh.MeshSection(n, testRegistry{}, mesh.NewLightScratch())
	if got := skyLight(topFaceLightAt(t, quads, 0, localY, 8)); got != 14 {
		t.Fatalf("跨区块一步天空光 = %d，想要 14", got)
	}
}

type nonOpaqueBlockRegistry struct{ testRegistry }

func (nonOpaqueBlockRegistry) Opaque(id world.BlockID) bool {
	return id != world.AirID && id != core.StoneBrickID
}

func blockLightCorridor(
	t *testing.T,
	floorY int32,
	blocks map[core.BlockPos]world.BlockID,
) (*world.Neighborhood, int) {
	t.Helper()
	chunks := make(map[core.ChunkPos]*world.Chunk, 9)
	for cx := int32(-1); cx <= 1; cx++ {
		for cz := int32(-1); cz <= 1; cz++ {
			pos := core.ChunkPos{X: cx, Z: cz}
			chunk := world.NewChunk(pos)
			for lz := 0; lz < core.SectionSize; lz++ {
				worldZ := int(cz)*core.SectionSize + lz
				for lx := 0; lx < core.SectionSize; lx++ {
					chunk.SetBlock(lx, floorY, lz, core.StoneID)
					chunk.SetBlock(lx, floorY+2, lz, core.StoneID)
					if worldZ != 8 {
						chunk.SetBlock(lx, floorY+1, lz, core.StoneID)
					}
				}
			}
			chunks[pos] = chunk
		}
	}
	for position, id := range blocks {
		chunk := chunks[position.Chunk()]
		if chunk == nil {
			t.Fatalf("方块位置 %+v 超出测试邻域", position)
		}
		x, _, z := position.Local()
		chunk.SetBlock(x, position.Y, z, id)
	}
	get := func(pos core.ChunkPos) *world.Chunk { return chunks[pos] }
	n := world.NeighborhoodAt(get, core.ChunkPos{}, core.BlockPos{Y: floorY}.SectionIndex())
	if n == nil {
		t.Fatal("NeighborhoodAt 返回 nil")
	}
	return n, int(floorY-core.MinY) & core.SectionMask
}

func TestBlockLightSourceFaceSamplesAdjacentFourteen(t *testing.T) {
	const floorY int32 = 64
	n, localY := blockLightCorridor(t, floorY, map[core.BlockPos]world.BlockID{
		{X: 0, Y: floorY + 1, Z: 8}: core.LightBlockID,
	})
	quads := mesh.MeshSection(n, testRegistry{}, mesh.NewLightScratch())
	for _, quad := range quads {
		if quad.Face == mesh.FacePosX && quad.X == 0 && int(quad.Y) == localY+1 && quad.Z == 8 {
			if got := blockLight(quad.Light); got != 14 {
				t.Fatalf("光源可见面相邻方块光=%d，想要 14", got)
			}
			return
		}
	}
	t.Fatal("没有找到发光块的 +X 可见面")
}

func TestBlockLightFallsToOneAtDistanceFourteenAndZeroAtFifteen(t *testing.T) {
	const floorY int32 = 64
	n, localY := blockLightCorridor(t, floorY, map[core.BlockPos]world.BlockID{
		{X: 0, Y: floorY + 1, Z: 8}: core.LightBlockID,
	})
	quads := mesh.MeshSection(n, testRegistry{}, mesh.NewLightScratch())
	if got := blockLight(topFaceLightAt(t, quads, 14, localY, 8)); got != 1 {
		t.Fatalf("距离 14 的方块光=%d，想要 1", got)
	}
	if got := blockLight(topFaceLightAt(t, quads, 15, localY, 8)); got != 0 {
		t.Fatalf("距离 15 的方块光=%d，想要 0", got)
	}
}

func TestBlockLightStopsAtNonAirBlockEvenWhenMarkedNonOpaque(t *testing.T) {
	const floorY int32 = 64
	n, localY := blockLightCorridor(t, floorY, map[core.BlockPos]world.BlockID{
		{X: 0, Y: floorY + 1, Z: 8}: core.LightBlockID,
		{X: 7, Y: floorY + 1, Z: 8}: core.StoneBrickID,
	})
	quads := mesh.MeshSection(n, nonOpaqueBlockRegistry{}, mesh.NewLightScratch())
	if got := blockLight(topFaceLightAt(t, quads, 6, localY, 8)); got != 9 {
		t.Fatalf("阻断块前方方块光=%d，想要 9", got)
	}
	if got := blockLight(topFaceLightAt(t, quads, 8, localY, 8)); got != 0 {
		t.Fatalf("透明标记的非空气方块后方光照=%d，想要 0", got)
	}
}

func TestBlockLightMultipleSourcesTakeMaximum(t *testing.T) {
	const floorY int32 = 64
	n, localY := blockLightCorridor(t, floorY, map[core.BlockPos]world.BlockID{
		{X: 0, Y: floorY + 1, Z: 8}:  core.LightBlockID,
		{X: 10, Y: floorY + 1, Z: 8}: core.LightBlockID,
	})
	quads := mesh.MeshSection(n, testRegistry{}, mesh.NewLightScratch())
	if got := blockLight(topFaceLightAt(t, quads, 8, localY, 8)); got != 13 {
		t.Fatalf("多光源交汇方块光=%d，想要较近光源的 13", got)
	}
}

func TestBlockLightCrossesSectionBoundary(t *testing.T) {
	const floorY int32 = 79
	n, localY := blockLightCorridor(t, floorY, map[core.BlockPos]world.BlockID{
		{X: 0, Y: floorY + 1, Z: 8}: core.LightBlockID,
	})
	quads := mesh.MeshSection(n, testRegistry{}, mesh.NewLightScratch())
	if got := blockLight(topFaceLightAt(t, quads, 1, localY, 8)); got != 14 {
		t.Fatalf("跨区段一步方块光=%d，想要 14", got)
	}
}

func TestBlockLightCrossesChunkBoundary(t *testing.T) {
	const floorY int32 = 64
	n, localY := blockLightCorridor(t, floorY, map[core.BlockPos]world.BlockID{
		{X: -1, Y: floorY + 1, Z: 8}: core.LightBlockID,
	})
	quads := mesh.MeshSection(n, testRegistry{}, mesh.NewLightScratch())
	if got := blockLight(topFaceLightAt(t, quads, 0, localY, 8)); got != 14 {
		t.Fatalf("跨区块一步方块光=%d，想要 14", got)
	}
}

func TestBlockLightMissingNeighborStaysDark(t *testing.T) {
	const floorY int32 = 64
	loaded, loadedLocalY := blockLightCorridor(t, floorY, map[core.BlockPos]world.BlockID{
		{X: -1, Y: floorY + 1, Z: 8}: core.LightBlockID,
	})
	loadedQuads := mesh.MeshSection(loaded, testRegistry{}, mesh.NewLightScratch())
	if got := blockLight(topFaceLightAt(t, loadedQuads, 0, loadedLocalY, 8)); got != 14 {
		t.Fatalf("邻区存在时边界方块光=%d，想要 14", got)
	}

	center := world.NewChunk(core.ChunkPos{})
	for z := 0; z < core.SectionSize; z++ {
		for x := 0; x < core.SectionSize; x++ {
			center.SetBlock(x, floorY, z, core.StoneID)
			center.SetBlock(x, floorY+2, z, core.StoneID)
		}
	}
	get := func(pos core.ChunkPos) *world.Chunk {
		if pos == (core.ChunkPos{}) {
			return center
		}
		return nil
	}
	n := world.NeighborhoodAt(get, core.ChunkPos{}, core.BlockPos{Y: floorY}.SectionIndex())
	localY := int(floorY-core.MinY) & core.SectionMask
	quads := mesh.MeshSection(n, testRegistry{}, mesh.NewLightScratch())
	if got := blockLight(topFaceLightAt(t, quads, 0, localY, 8)); got != 0 {
		t.Fatalf("缺失邻区边界方块光=%d，想要 0", got)
	}
}

func TestPackedSkyAndBlockLightBuildIsDeterministic(t *testing.T) {
	n, localY := propagatedSkyWorld(t, 0, nil)
	n.Center.Blocks.Set(4, localY+1, 8, core.LightBlockID)
	scratch := mesh.NewLightScratch()
	quadsA := mesh.MeshSection(n, testRegistry{}, scratch)
	quadsB := mesh.MeshSection(n, testRegistry{}, scratch)
	if !reflect.DeepEqual(quadsA, quadsB) {
		t.Fatal("相同输入连续两次生成了不同 packed 光照网格")
	}
	light := topFaceLightAt(t, quadsA, 1, localY, 8)
	if sky, block := skyLight(light), blockLight(light); sky != 14 || block != 12 {
		t.Fatalf("packed 光照=%#x，想要 sky=14 block=12", light)
	}
}

func TestMeshSectionPropagatedSkyLightIsDeterministic(t *testing.T) {
	n, _ := propagatedSkyWorld(t, 0, nil)
	scratch := mesh.NewLightScratch()
	quadsA := mesh.MeshSection(n, testRegistry{}, scratch)
	quadsB := mesh.MeshSection(n, testRegistry{}, scratch)
	if !reflect.DeepEqual(quadsA, quadsB) {
		t.Fatal("相同输入连续两次生成了不同网格")
	}
}

// skyWorld 造出 3×3 个区块，并在 y=64 铺满一层实心地面。
// roofX 中的局部 x 列会在全部水平邻区的 y=80 额外加一块屋顶。
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
					if roofX[lx] {
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
	quads := mesh.MeshSection(n, testRegistry{}, mesh.NewLightScratch())

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
	quads := mesh.MeshSection(n, testRegistry{}, mesh.NewLightScratch())

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
	quads := mesh.MeshSection(n, testRegistry{}, mesh.NewLightScratch())
	for _, q := range quads {
		if q.Face == mesh.FacePosY && int(q.Y) == localY && q.Light != 0xF0 {
			t.Fatalf("中心区块顶面 Light = %#x，想要 0xF0", q.Light)
		}
	}
}
