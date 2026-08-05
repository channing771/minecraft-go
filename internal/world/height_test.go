package world_test

import (
	"testing"
	"unsafe"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

// emptyColumn 是空列的哨兵高度：没有任何非空气方块。
const emptyColumn = int32(core.MinY - 1)

func TestChunkHeightsStartEmpty(t *testing.T) {
	c := world.NewChunk(core.ChunkPos{X: 2, Z: -3})
	for lz := 0; lz < core.SectionSize; lz++ {
		for lx := 0; lx < core.SectionSize; lx++ {
			if got := c.HighestOpaque(lx, lz); got != emptyColumn {
				t.Fatalf("空列 (%d,%d) 高度 = %d，想要 %d", lx, lz, got, emptyColumn)
			}
		}
	}
}

func TestChunkHeightsRiseOnAscendingWrites(t *testing.T) {
	c := world.NewChunk(core.ChunkPos{})
	for _, y := range []int32{core.MinY, -1, 0, 63, 100} {
		c.SetBlock(4, y, 9, world.BlockID(3))
		if got := c.HighestOpaque(4, 9); got != y {
			t.Fatalf("写入 y=%d 后高度 = %d，想要 %d", y, got, y)
		}
	}
}

func TestChunkHeightsIgnoreNonTopWrites(t *testing.T) {
	c := world.NewChunk(core.ChunkPos{})
	c.SetBlock(1, 80, 2, world.BlockID(3))

	// 顶部之下写入实体方块或空气都不改变列顶。
	c.SetBlock(1, 40, 2, world.BlockID(5))
	c.SetBlock(1, 40, 2, world.AirID)
	if got := c.HighestOpaque(1, 2); got != 80 {
		t.Fatalf("非列顶修改后高度 = %d，想要 80", got)
	}

	// 越界写入被忽略，也不得改变列顶。
	c.SetBlock(1, core.MaxY, 2, world.BlockID(6))
	c.SetBlock(1, core.MinY-1, 2, world.BlockID(6))
	if got := c.HighestOpaque(1, 2); got != 80 {
		t.Fatalf("越界写入后高度 = %d，想要 80", got)
	}
}

func TestChunkHeightsDropWhenTopRemoved(t *testing.T) {
	c := world.NewChunk(core.ChunkPos{})
	c.SetBlock(7, 64, 7, world.BlockID(3))
	c.SetBlock(7, 80, 7, world.BlockID(3))

	c.SetBlock(7, 80, 7, world.AirID)
	if got := c.HighestOpaque(7, 7); got != 64 {
		t.Fatalf("移除列顶后高度 = %d，想要 64", got)
	}

	c.SetBlock(7, 64, 7, world.AirID)
	if got := c.HighestOpaque(7, 7); got != emptyColumn {
		t.Fatalf("清空列后高度 = %d，想要 %d", got, emptyColumn)
	}
}

func TestChunkHeightsWorstCaseScanStaysInWorld(t *testing.T) {
	c := world.NewChunk(core.ChunkPos{})
	// 世界顶端唯一一个方块被移除时，最坏需要向下扫描 384 格。
	c.SetBlock(0, core.MaxY-1, 0, world.BlockID(3))
	c.SetBlock(0, core.MaxY-1, 0, world.AirID)
	if got := c.HighestOpaque(0, 0); got != emptyColumn {
		t.Fatalf("最坏扫描后高度 = %d，想要 %d", got, emptyColumn)
	}
}

func TestChunkHeightsSurviveClone(t *testing.T) {
	c := world.NewChunk(core.ChunkPos{})
	c.SetBlock(3, 70, 5, world.BlockID(3))

	clone := c.Clone()
	if got := clone.HighestOpaque(3, 5); got != 70 {
		t.Fatalf("克隆后高度 = %d，想要 70", got)
	}

	// 克隆是深拷贝：修改任一侧都不得影响另一侧。
	clone.SetBlock(3, 90, 5, world.BlockID(3))
	if got := c.HighestOpaque(3, 5); got != 70 {
		t.Fatalf("修改克隆后原区块高度 = %d，想要 70", got)
	}
	if got := clone.HighestOpaque(3, 5); got != 90 {
		t.Fatalf("修改克隆后克隆高度 = %d，想要 90", got)
	}
}

func TestChunkRebuildHeightsMatchesSetBlock(t *testing.T) {
	incremental := world.NewChunk(core.ChunkPos{})
	direct := world.NewChunk(core.ChunkPos{})

	// direct 绕过 SetBlock 直接写 section，模拟 snapshot/存档装入路径。
	place := func(lx int, wy int32, lz int, id world.BlockID) {
		incremental.SetBlock(lx, wy, lz, id)
		si := int(wy-core.MinY) >> core.SectionShift
		ly := int(wy-core.MinY) & core.SectionMask
		direct.Section(si).Blocks.Set(lx, ly, lz, id)
	}
	place(0, core.MinY, 0, world.BlockID(3))
	place(1, 0, 1, world.BlockID(3))
	place(1, 200, 1, world.BlockID(3))
	place(15, core.MaxY-1, 15, world.BlockID(3))
	place(8, 100, 8, world.BlockID(3))
	place(8, 100, 8, world.AirID)

	direct.RebuildHeights()
	if got, want := direct.Heights(), incremental.Heights(); got != want {
		t.Fatalf("重建高度表与增量维护不一致")
	}
	if got := direct.HighestOpaque(1, 1); got != 200 {
		t.Fatalf("重建后 (1,1) 高度 = %d，想要 200", got)
	}
	if got := direct.HighestOpaque(8, 8); got != emptyColumn {
		t.Fatalf("重建后 (8,8) 高度 = %d，想要 %d", got, emptyColumn)
	}
}

func TestHeightMapUsesFixed512Bytes(t *testing.T) {
	var heights world.HeightMap
	if got := len(heights); got != core.SectionSize*core.SectionSize {
		t.Fatalf("高度表长度 = %d，想要 %d", got, core.SectionSize*core.SectionSize)
	}
	if got := int(unsafe.Sizeof(heights)); got != 512 {
		t.Fatalf("高度表字节数 = %d，想要 512", got)
	}
}
