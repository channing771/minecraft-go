package world_test

import (
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

func TestChunkRoundTripAcrossFullHeight(t *testing.T) {
	c := world.NewChunk(core.ChunkPos{X: 3, Z: -7})

	// 世界的最低、最高与中间三层都要能正确读写。
	for _, y := range []int32{core.MinY, core.MinY + 1, 0, 63, core.MaxY - 1} {
		c.SetBlock(5, y, 11, world.BlockID(y-core.MinY+1))
	}
	for _, y := range []int32{core.MinY, core.MinY + 1, 0, 63, core.MaxY - 1} {
		want := world.BlockID(y - core.MinY + 1)
		if got := c.BlockAt(5, y, 11); got != want {
			t.Fatalf("y=%d: BlockAt = %d，想要 %d", y, got, want)
		}
	}
}

func TestChunkStartsAllAir(t *testing.T) {
	c := world.NewChunk(core.ChunkPos{})
	for i := 0; i < core.SectionsPerChunk; i++ {
		s := c.Section(i)
		if id, ok := s.Blocks.IsUniform(); !ok || id != world.AirID {
			t.Fatalf("第 %d 个区段不是全空气的单值态", i)
		}
	}
}

func TestChunkIgnoresOutOfWorldWrites(t *testing.T) {
	c := world.NewChunk(core.ChunkPos{})
	c.SetBlock(1, core.MinY-1, 2, world.BlockID(7))
	c.SetBlock(1, core.MaxY, 2, world.BlockID(8))
	if got := c.BlockAt(1, core.MinY-1, 2); got != world.AirID {
		t.Fatalf("世界下界外 BlockAt = %d，想要空气", got)
	}
	if got := c.BlockAt(1, core.MaxY, 2); got != world.AirID {
		t.Fatalf("世界上界外 BlockAt = %d，想要空气", got)
	}
}

func TestChunkCloneIsDeep(t *testing.T) {
	source := world.NewChunk(core.ChunkPos{X: -2, Z: 1})
	source.SetBlock(15, core.MinY, 15, core.StoneID)

	clone := source.Clone()
	clone.SetBlock(15, core.MinY, 15, core.DirtID)

	if got := source.BlockAt(15, core.MinY, 15); got != core.StoneID {
		t.Fatalf("修改 clone 影响 source: got %d", got)
	}
	if got := clone.BlockAt(15, core.MinY, 15); got != core.DirtID {
		t.Fatalf("clone block = %d，想要 dirt", got)
	}
}

func TestChunkHashUsesLogicalBlocksNotPaletteLayout(t *testing.T) {
	a := world.NewChunk(core.ChunkPos{})
	b := world.NewChunk(core.ChunkPos{})

	a.SetBlock(0, 0, 0, core.StoneID)
	a.SetBlock(1, 0, 0, core.DirtID)
	b.SetBlock(1, 0, 0, core.DirtID)
	b.SetBlock(0, 0, 0, core.StoneID)

	if a.Hash() != b.Hash() {
		t.Fatal("相同逻辑方块、不同调色板顺序产生了不同哈希")
	}
	clone := a.Clone()
	if clone.Hash() != a.Hash() {
		t.Fatal("深克隆改变了逻辑哈希")
	}
	clone.SetBlock(0, 0, 0, core.GrassID)
	if clone.Hash() == a.Hash() {
		t.Fatal("方块修改后哈希没有改变")
	}
}

func TestChunkPayloadBytesIsDeterministicAndAllocationFree(t *testing.T) {
	chunk := world.NewChunk(core.ChunkPos{})
	if got := chunk.PayloadBytes(); got != 512 {
		t.Fatalf("all-air payload bytes=%d, want 512", got)
	}
	chunk.SetBlock(0, 0, 0, core.StoneID)
	if got := chunk.PayloadBytes(); got != 2580 {
		t.Fatalf("indexed payload bytes=%d, want 2580", got)
	}

	var got int
	if allocations := testing.AllocsPerRun(100, func() {
		got = chunk.PayloadBytes()
	}); allocations != 0 {
		t.Fatalf("PayloadBytes allocations=%v, want 0", allocations)
	}
	if got != 2580 {
		t.Fatalf("repeated payload bytes=%d, want 2580", got)
	}
}

func TestChunkBlockIndexRoundTripsNegativeChunksAndHeightBounds(t *testing.T) {
	positions := []core.BlockPos{
		{X: -17, Y: core.MinY, Z: 31},
		{X: -32, Y: 0, Z: 16},
		{X: -18, Y: core.MaxY - 1, Z: 17},
	}
	for _, position := range positions {
		index, ok := world.ChunkBlockIndex(position)
		if !ok {
			t.Fatalf("ChunkBlockIndex(%+v) 被拒绝", position)
		}
		got, ok := world.BlockPosFromChunkIndex(position.Chunk(), index)
		if !ok || got != position {
			t.Fatalf("round trip %+v => %d => (%+v,%v)", position, index, got, ok)
		}
	}

	for _, position := range []core.BlockPos{
		{Y: core.MinY - 1},
		{Y: core.MaxY},
	} {
		if _, ok := world.ChunkBlockIndex(position); ok {
			t.Fatalf("越界位置 %+v 被接受", position)
		}
	}
	if _, ok := world.BlockPosFromChunkIndex(
		core.ChunkPos{},
		uint32(core.SectionsPerChunk*core.BlocksPerSection),
	); ok {
		t.Fatal("越界 block index 被接受")
	}
}

// TestNeighborhoodCrossesSectionBoundary 验证网格化邻域能读到
// -1 与 16 这两个越界坐标，这是面剔除正确性的前提。
func TestNeighborhoodCrossesSectionBoundary(t *testing.T) {
	center := world.NewSection()
	below := world.NewSection()
	below.Blocks.Set(7, 15, 7, world.BlockID(42))

	above := world.NewSection()
	above.Blocks.Set(7, 0, 7, world.BlockID(99))

	n := &world.Neighborhood{Center: center}
	n.Around[1][0][1] = below // -Y
	n.Around[1][2][1] = above // +Y

	if got := n.At(7, -1, 7); got != 42 {
		t.Fatalf("At(7,-1,7) = %d，想要 42（应读到 -Y 邻居的顶层）", got)
	}
	if got := n.At(7, 16, 7); got != 99 {
		t.Fatalf("At(7,16,7) = %d，想要 99（应读到 +Y 邻居的底层）", got)
	}
	if got := n.At(7, 8, 7); got != world.AirID {
		t.Fatalf("At(7,8,7) = %d，想要空气", got)
	}
}

// TestNeighborhoodMissingNeighborIsSolid 验证未加载的邻居按实心处理。
func TestNeighborhoodMissingNeighborIsSolid(t *testing.T) {
	n := &world.Neighborhood{Center: world.NewSection()}
	if got := n.At(-1, 5, 5); got != world.BarrierID {
		t.Fatalf("缺失邻居处 At = %d，想要 BarrierID", got)
	}
}

// TestNeighborhoodAtWiresHorizontalNeighbors 验证水平邻居接对了。
func TestNeighborhoodAtWiresHorizontalNeighbors(t *testing.T) {
	chunks := map[core.ChunkPos]*world.Chunk{}
	for dx := int32(-1); dx <= 1; dx++ {
		for dz := int32(-1); dz <= 1; dz++ {
			pos := core.ChunkPos{X: dx, Z: dz}
			chunks[pos] = world.NewChunk(pos)
		}
	}
	// 在 -X 邻居的东边界、+X 邻居的西边界各放一个可区分的方块。
	chunks[core.ChunkPos{X: -1, Z: 0}].SetBlock(15, 0, 8, world.BlockID(70))
	chunks[core.ChunkPos{X: 1, Z: 0}].SetBlock(0, 0, 8, world.BlockID(71))

	get := func(p core.ChunkPos) *world.Chunk { return chunks[p] }
	n := world.NeighborhoodAt(get, core.ChunkPos{X: 0, Z: 0}, 4) // y=0 落在第 4 个区段

	if got := n.At(-1, 0, 8); got != 70 {
		t.Fatalf("At(-1,0,8) = %d，想要 70（-X 邻居）", got)
	}
	if got := n.At(16, 0, 8); got != 71 {
		t.Fatalf("At(16,0,8) = %d，想要 71（+X 邻居）", got)
	}
}

func TestNeighborhoodReadsDiagonalForAO(t *testing.T) {
	chunks := map[core.ChunkPos]*world.Chunk{}
	for dx := int32(-1); dx <= 1; dx++ {
		for dz := int32(-1); dz <= 1; dz++ {
			pos := core.ChunkPos{X: dx, Z: dz}
			chunks[pos] = world.NewChunk(pos)
		}
	}
	// 中心 si=4 覆盖 y=0..15；(+Y) 邻居的底层是世界 y=16。
	chunks[core.ChunkPos{X: -1, Z: 1}].SetBlock(15, 16, 0, world.BlockID(123))

	get := func(p core.ChunkPos) *world.Chunk { return chunks[p] }
	n := world.NeighborhoodAt(get, core.ChunkPos{}, 4)
	if got := n.At(-1, 16, 16); got != 123 {
		t.Fatalf("At(-1,16,16) = %d，想要 123（-X,+Y,+Z 对角邻居）", got)
	}
}
