package world

import (
	"crypto/sha256"
	"encoding/binary"

	"minecraft-go/internal/core"
)

// Chunk 是一根 16×16 的世界柱，含 core.SectionsPerChunk 个区段。
type Chunk struct {
	Pos      core.ChunkPos
	sections [core.SectionsPerChunk]*Section
}

// NewChunk 创建一个全空气的区块。
func NewChunk(pos core.ChunkPos) *Chunk {
	c := &Chunk{Pos: pos}
	for i := range c.sections {
		c.sections[i] = NewSection()
	}
	return c
}

// Section 返回第 i 个区段，i 取值 0..core.SectionsPerChunk-1。
func (c *Chunk) Section(i int) *Section { return c.sections[i] }

// Clone 返回区块及其全部区段的深拷贝。
func (c *Chunk) Clone() *Chunk {
	clone := &Chunk{Pos: c.Pos}
	for index, section := range c.sections {
		clone.sections[index] = section.Clone()
	}
	return clone
}

// Hash 返回只由逻辑方块值决定的稳定 SHA-256。
func (c *Chunk) Hash() [sha256.Size]byte {
	hash := sha256.New()
	var encoded [2]byte
	for sectionIndex := 0; sectionIndex < core.SectionsPerChunk; sectionIndex++ {
		section := c.sections[sectionIndex]
		for y := 0; y < core.SectionSize; y++ {
			for z := 0; z < core.SectionSize; z++ {
				for x := 0; x < core.SectionSize; x++ {
					binary.LittleEndian.PutUint16(
						encoded[:],
						uint16(section.Blocks.Get(x, y, z)),
					)
					_, _ = hash.Write(encoded[:])
				}
			}
		}
	}
	var sum [sha256.Size]byte
	hash.Sum(sum[:0])
	return sum
}

// ChunkBlockIndex 把世界方块位置映射为所属区块内的紧凑索引。
func ChunkBlockIndex(position core.BlockPos) (uint32, bool) {
	if position.Y < core.MinY || position.Y >= core.MaxY {
		return 0, false
	}
	x, y, z := position.Local()
	index := position.SectionIndex()*core.BlocksPerSection +
		y*core.SectionSize*core.SectionSize +
		z*core.SectionSize +
		x
	return uint32(index), true
}

// BlockPosFromChunkIndex 把紧凑索引还原为指定区块中的世界方块位置。
func BlockPosFromChunkIndex(
	chunk core.ChunkPos,
	index uint32,
) (core.BlockPos, bool) {
	if index >= uint32(core.SectionsPerChunk*core.BlocksPerSection) {
		return core.BlockPos{}, false
	}
	sectionIndex := int(index) / core.BlocksPerSection
	localIndex := int(index) % core.BlocksPerSection
	y := localIndex / (core.SectionSize * core.SectionSize)
	z := localIndex / core.SectionSize & core.SectionMask
	x := localIndex & core.SectionMask
	return core.BlockPos{
		X: chunk.X<<core.SectionShift + int32(x),
		Y: int32(core.MinY + sectionIndex*core.SectionSize + y),
		Z: chunk.Z<<core.SectionShift + int32(z),
	}, true
}

// sectionIndexOf 把世界 Y 映射为区段索引与区段内局部 Y。
func sectionIndexOf(wy int32) (si, ly int) {
	d := wy - core.MinY
	return int(d >> core.SectionShift), int(d & core.SectionMask)
}

// BlockAt 读取方块。lx/lz 是区块内局部坐标 0..15，wy 是世界 Y。
// wy 超出 [core.MinY, core.MaxY) 时返回空气。
func (c *Chunk) BlockAt(lx int, wy int32, lz int) BlockID {
	if wy < core.MinY || wy >= core.MaxY {
		return AirID
	}
	si, ly := sectionIndexOf(wy)
	return c.sections[si].Blocks.Get(lx, ly, lz)
}

// SetBlock 写入方块。wy 超出世界高度范围时静默忽略。
func (c *Chunk) SetBlock(lx int, wy int32, lz int, id BlockID) {
	if wy < core.MinY || wy >= core.MaxY {
		return
	}
	si, ly := sectionIndexOf(wy)
	c.sections[si].Blocks.Set(lx, ly, lz, id)
}

// Compact 对所有区段做惰性降级，应在一批批量写入之后调用。
func (c *Chunk) Compact() {
	for _, s := range c.sections {
		s.Blocks.Compact()
	}
}
