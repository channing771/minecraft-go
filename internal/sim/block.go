package sim

import "minecraft-go/internal/core"

func (dimension *Dimension) SetBlock(
	position core.BlockPos,
	block core.BlockID,
) (old core.BlockID, changed bool, err error) {
	if position.Y < core.MinY || position.Y >= core.MaxY {
		return core.AirID, false, ErrBlockOutOfWorld
	}
	record, ok := dimension.records[position.Chunk()]
	if !ok || record.State != ChunkReady {
		return core.AirID, false, ErrChunkNotReady
	}
	x, _, z := position.Local()
	old = record.Chunk.BlockAt(x, position.Y, z)
	if old == block {
		return old, false, nil
	}
	record.Chunk.SetBlock(x, position.Y, z, block)
	return old, true, nil
}
