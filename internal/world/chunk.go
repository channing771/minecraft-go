package world

import "minecraft-go/internal/core"

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
