// Package core 提供与客户端/服务端无关的公共域类型。
//
// 本包不 import 任何其他内部包（见 spec §3.1）。
package core

// 世界几何常量（spec §4.1）。
const (
	// SectionSize 是区段边长，区段含 16³ = 4096 个方块。
	SectionSize = 16
	// SectionShift 是 SectionSize 的以 2 为底的对数，用于位移代替除法。
	SectionShift = 4
	// SectionMask 用于取区段内局部坐标。
	SectionMask = SectionSize - 1

	// MinY 是世界最低方块的 Y 坐标（含）。
	MinY = -64
	// MaxY 是世界最高方块 Y 坐标的上界（不含）。
	MaxY = 320
	// SectionsPerChunk 是每个区块的区段数。
	SectionsPerChunk = (MaxY - MinY) / SectionSize // 24
	// BlocksPerSection 是每个区段的方块数。
	BlocksPerSection = SectionSize * SectionSize * SectionSize // 4096
)

// BlockPos 是方块的世界坐标。
type BlockPos struct{ X, Y, Z int32 }

// ChunkPos 是区块的世界坐标（16×16 的水平柱）。
type ChunkPos struct{ X, Z int32 }

// SectionPos 定位一个区段。Y 是区段索引（0..SectionsPerChunk-1），不是方块 Y。
type SectionPos struct{ X, Y, Z int32 }

// Chunk 返回该方块所在的区块坐标。
//
// 用算术右移而非除法：Go 的整数除法向零取整，-1/16 得 0，
// 而正确答案是 -1。算术右移天然向负无穷取整。
func (b BlockPos) Chunk() ChunkPos {
	return ChunkPos{X: b.X >> SectionShift, Z: b.Z >> SectionShift}
}

// SectionIndex 返回该方块在其区块内的区段索引，取值 0..SectionsPerChunk-1。
// 调用方需保证 Y 在 [MinY, MaxY) 内。
func (b BlockPos) SectionIndex() int {
	return int((b.Y - MinY) >> SectionShift)
}

// Local 返回该方块在其区段内的局部坐标，三个分量均在 0..15。
func (b BlockPos) Local() (x, y, z int) {
	return int(b.X & SectionMask),
		int((b.Y - MinY) & SectionMask),
		int(b.Z & SectionMask)
}

// Section 返回该方块所在的区段坐标。
func (b BlockPos) Section() SectionPos {
	return SectionPos{
		X: b.X >> SectionShift,
		Y: int32(b.SectionIndex()),
		Z: b.Z >> SectionShift,
	}
}

// MinCorner 返回该区段最小角的方块世界坐标。
func (s SectionPos) MinCorner() BlockPos {
	return BlockPos{
		X: s.X << SectionShift,
		Y: s.Y<<SectionShift + MinY,
		Z: s.Z << SectionShift,
	}
}
