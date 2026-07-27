package sim

import (
	"fmt"
	"sort"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

type ChunkOverlay map[uint32]core.BlockID

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

	index, ok := world.ChunkBlockIndex(position)
	if !ok {
		panic("sim: in-range block has no chunk index")
	}
	chunkPos := position.Chunk()
	if block == dimension.base(position) {
		if overlay := dimension.overlays[chunkPos]; overlay != nil {
			delete(overlay, index)
			if len(overlay) == 0 {
				delete(dimension.overlays, chunkPos)
			}
		}
		return old, true, nil
	}
	overlay := dimension.overlays[chunkPos]
	if overlay == nil {
		overlay = make(ChunkOverlay)
		dimension.overlays[chunkPos] = overlay
	}
	overlay[index] = block
	return old, true, nil
}

func (dimension *Dimension) OverlayEntries(pos core.ChunkPos) int {
	return len(dimension.overlays[pos])
}

func (dimension *Dimension) applyOverlay(
	pos core.ChunkPos,
	chunk *world.Chunk,
) error {
	overlay := dimension.overlays[pos]
	if len(overlay) == 0 {
		return nil
	}
	indices := make([]uint32, 0, len(overlay))
	for index := range overlay {
		indices = append(indices, index)
	}
	sort.Slice(indices, func(i, j int) bool {
		return indices[i] < indices[j]
	})

	dirtySections := make(map[int]struct{})
	for _, index := range indices {
		position, ok := world.BlockPosFromChunkIndex(pos, index)
		if !ok {
			return fmt.Errorf("sim: overlay index %d is outside chunk", index)
		}
		x, _, z := position.Local()
		chunk.SetBlock(x, position.Y, z, overlay[index])
		dirtySections[position.SectionIndex()] = struct{}{}
	}
	for sectionIndex := range dirtySections {
		chunk.Section(sectionIndex).Blocks.Compact()
	}
	return nil
}
