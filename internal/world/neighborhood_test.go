package world_test

import (
	"testing"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/world"
)

// chunkGetter 返回一个只认识给定区块的 get 函数。
func chunkGetter(chunks ...*world.Chunk) func(core.ChunkPos) *world.Chunk {
	index := make(map[core.ChunkPos]*world.Chunk, len(chunks))
	for _, c := range chunks {
		index[c.Pos] = c
	}
	return func(pos core.ChunkPos) *world.Chunk { return index[pos] }
}

// sectionIndexFor 返回世界 Y 所属的区段索引。
func sectionIndexFor(wy int32) int { return int(wy-core.MinY) >> core.SectionShift }

func TestNeighborhoodSkyLightAboveAndBelowColumnTop(t *testing.T) {
	center := world.NewChunk(core.ChunkPos{})
	center.SetBlock(4, 64, 6, world.BlockID(3))

	si := sectionIndexFor(64)
	n := world.NeighborhoodAt(chunkGetter(center), center.Pos, si)
	if n == nil {
		t.Fatal("NeighborhoodAt 返回 nil")
	}

	localY := int(64-core.MinY) & core.SectionMask
	if got := n.SkyLight(4, localY+1, 6); got != 15 {
		t.Fatalf("列顶正上方天空光 = %d，想要 15", got)
	}
	if got := n.SkyLight(4, localY, 6); got != 0 {
		t.Fatalf("列顶自身天空光 = %d，想要 0", got)
	}
	if got := n.SkyLight(4, localY-1, 6); got != 0 {
		t.Fatalf("列顶下方天空光 = %d，想要 0", got)
	}
	// 同区段内的其他列仍然是空列，全部露天。
	if got := n.SkyLight(5, localY, 6); got != 15 {
		t.Fatalf("空列天空光 = %d，想要 15", got)
	}
}

func TestNeighborhoodSamplesWholeThreeByThreeByThreeHalo(t *testing.T) {
	center := world.NewChunk(core.ChunkPos{})
	west := world.NewChunk(core.ChunkPos{X: -1})
	east := world.NewChunk(core.ChunkPos{X: 1})
	west.SetBlock(0, 64, 3, core.StoneID)
	east.SetBlock(15, 64, 4, core.DirtID)
	n := world.NeighborhoodAt(chunkGetter(center, west, east), center.Pos, sectionIndexFor(64))
	localY := int(64-core.MinY) & core.SectionMask
	if got := n.At(-16, localY, 3); got != core.StoneID {
		t.Fatalf("西侧 halo 方块=%d，想要 stone", got)
	}
	if got := n.At(31, localY, 4); got != core.DirtID {
		t.Fatalf("东侧 halo 方块=%d，想要 dirt", got)
	}
	if got := n.At(-17, localY, 3); got != world.BarrierID {
		t.Fatalf("halo 外方块=%d，想要 barrier", got)
	}
	if got := n.SkyLight(-16, localY, 4); got != 15 {
		t.Fatalf("西侧 halo 天空光=%d，想要 15", got)
	}
	if got := n.SkyLight(31, localY, 3); got != 15 {
		t.Fatalf("东侧 halo 天空光=%d，想要 15", got)
	}
	if got := n.SkyLight(-16, localY, 16); got != 0 {
		t.Fatalf("缺失邻区天空光=%d，想要 0", got)
	}
	if got := n.SkyLight(-16, -17, 4); got != 0 {
		t.Fatalf("halo 外 y=-17 天空光=%d，想要 0", got)
	}
	if got := n.SkyLight(31, 32, 3); got != 0 {
		t.Fatalf("halo 外 y=32 天空光=%d，想要 0", got)
	}
}

func TestNeighborhoodSkyLightCrossesChunkBoundary(t *testing.T) {
	center := world.NewChunk(core.ChunkPos{X: 0, Z: 0})
	east := world.NewChunk(core.ChunkPos{X: 1, Z: 0})
	east.SetBlock(0, 100, 3, world.BlockID(3))

	si := sectionIndexFor(64)
	n := world.NeighborhoodAt(chunkGetter(center, east), center.Pos, si)
	localY := int(64-core.MinY) & core.SectionMask

	// 局部 x=16 落在东侧邻区的 x=0 列，该列上方 100 处有遮挡。
	if got := n.SkyLight(16, localY, 3); got != 0 {
		t.Fatalf("跨区块被遮挡列天空光 = %d，想要 0", got)
	}
	if got := n.SkyLight(16, localY, 4); got != 15 {
		t.Fatalf("跨区块空列天空光 = %d，想要 15", got)
	}
}

func TestNeighborhoodSkyLightTreatsMissingChunkAsDark(t *testing.T) {
	center := world.NewChunk(core.ChunkPos{})
	si := sectionIndexFor(64)
	n := world.NeighborhoodAt(chunkGetter(center), center.Pos, si)
	localY := int(64-core.MinY) & core.SectionMask

	// 邻区未加载时必须按遮挡处理，避免边界亮缝。
	if got := n.SkyLight(-1, localY, 3); got != 0 {
		t.Fatalf("缺失西侧邻区天空光 = %d，想要 0", got)
	}
	if got := n.SkyLight(3, localY, 16); got != 0 {
		t.Fatalf("缺失南侧邻区天空光 = %d，想要 0", got)
	}
}

func TestNeighborhoodSkyLightAboveWorldTop(t *testing.T) {
	center := world.NewChunk(core.ChunkPos{})
	center.SetBlock(2, core.MaxY-1, 2, world.BlockID(3))

	si := sectionIndexFor(core.MaxY - 1)
	n := world.NeighborhoodAt(chunkGetter(center), center.Pos, si)

	// 局部 y=16 已经高于世界顶端方块，仍是露天。
	if got := n.SkyLight(2, core.SectionSize, 2); got != 15 {
		t.Fatalf("世界顶端上方天空光 = %d，想要 15", got)
	}
	if got := n.SkyLight(2, core.SectionSize-1, 2); got != 0 {
		t.Fatalf("世界顶端方块处天空光 = %d，想要 0", got)
	}
}
