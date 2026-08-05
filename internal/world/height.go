package world

import "minecraft-go/internal/core"

// EmptyColumnHeight 是空列的哨兵高度，表示该列没有任何非空气方块。
const EmptyColumnHeight = int32(core.MinY - 1)

// HeightMap 是一个区块 16×16 列的最高非空气方块 Y，恰好占用 512 字节。
// 它完全由权威方块派生，不进入区块存档、网络 payload 或区块 Hash。
type HeightMap [core.SectionSize * core.SectionSize]int16

// Highest 返回局部列 (lx, lz) 的最高非空气方块 Y，空列返回 EmptyColumnHeight。
func (h *HeightMap) Highest(lx, lz int) int32 {
	return int32(h[lz<<core.SectionShift|lx])
}

// Heights 返回高度表的值拷贝，供跨 goroutine 的不可变网格化输入使用。
func (c *Chunk) Heights() HeightMap { return c.heights }

// HighestOpaque 返回局部列 (lx, lz) 的最高非空气方块 Y，空列返回 EmptyColumnHeight。
func (c *Chunk) HighestOpaque(lx, lz int) int32 { return c.heights.Highest(lx, lz) }

// RebuildHeights 从当前 section 内容重算整张高度表。
// 只有绕过 SetBlock 直接装入 section 的路径（快照、存档）需要调用。
func (c *Chunk) RebuildHeights() {
	for lz := 0; lz < core.SectionSize; lz++ {
		for lx := 0; lx < core.SectionSize; lx++ {
			c.heights[lz<<core.SectionShift|lx] = int16(c.scanColumnTop(lx, core.MaxY-1, lz))
		}
	}
}

// updateHeight 在一次方块写入后维护列顶。
// 抬高是 O(1)；只有移除列顶才向下扫描，最坏为世界高度 384 格。
func (c *Chunk) updateHeight(lx int, wy int32, lz int, id BlockID) {
	index := lz<<core.SectionShift | lx
	current := int32(c.heights[index])
	switch {
	case id != AirID:
		if wy > current {
			c.heights[index] = int16(wy)
		}
	case wy == current:
		c.heights[index] = int16(c.scanColumnTop(lx, wy-1, lz))
	}
}

// scanColumnTop 从 from 向下找出该列第一个非空气方块的 Y，找不到时返回 EmptyColumnHeight。
func (c *Chunk) scanColumnTop(lx int, from int32, lz int) int32 {
	if from >= core.MaxY {
		from = core.MaxY - 1
	}
	for y := from; y >= core.MinY; y-- {
		if c.BlockAt(lx, y, lz) != AirID {
			return y
		}
	}
	return EmptyColumnHeight
}
