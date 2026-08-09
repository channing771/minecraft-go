package sim

import (
	"slices"
	"sort"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

func (engine *Engine) recordChange(
	dimensionID core.DimensionID,
	position core.BlockPos,
	block core.BlockID,
	pending map[core.ChunkKey]*pendingChunkChanges,
) {
	key := core.ChunkKey{
		Dimension: dimensionID,
		Pos:       position.Chunk(),
	}
	changeSet := pending[key]
	if changeSet == nil {
		record := engine.dimensions[dimensionID].records[key.Pos]
		changeSet = &pendingChunkChanges{
			baseRevision: record.Revision,
			changes:      make(map[uint32]BlockChange),
			dirty:        make(map[int]struct{}),
		}
		pending[key] = changeSet
	}
	index, ok := world.ChunkBlockIndex(position)
	if !ok {
		panic("sim: changed block has no chunk index")
	}
	changeSet.changes[index] = BlockChange{
		Position: position,
		Block:    block,
	}
	changeSet.dirty[position.SectionIndex()] = struct{}{}
}

func (engine *Engine) finishChanges(
	pending map[core.ChunkKey]*pendingChunkChanges,
	result *TickResult,
) {
	keys := make([]core.ChunkKey, 0, len(pending))
	for key := range pending {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return chunkKeyLess(keys[i], keys[j])
	})
	for _, key := range keys {
		changeSet := pending[key]
		record := engine.dimensions[key.Dimension].records[key.Pos]
		for sectionIndex := range changeSet.dirty {
			record.Chunk.Section(sectionIndex).Blocks.Compact()
		}
		record.Revision++

		indices := make([]uint32, 0, len(changeSet.changes))
		for index := range changeSet.changes {
			indices = append(indices, index)
		}
		sort.Slice(indices, func(i, j int) bool {
			return indices[i] < indices[j]
		})
		changes := make([]BlockChange, 0, len(indices))
		for _, index := range indices {
			changes = append(changes, changeSet.changes[index])
		}
		result.Changes = append(result.Changes, ChunkChangeBatch{
			Dimension:    key.Dimension,
			Chunk:        key.Pos,
			BaseRevision: changeSet.baseRevision,
			NewRevision:  record.Revision,
			Changes:      changes,
		})
	}
}

// sortChunkKeys 用泛型排序避免 sort.Slice 的反射 swapper 分配，
// 使权威 tick 的热路径保持零分配。
func sortChunkKeys(keys []core.ChunkKey) {
	slices.SortFunc(keys, func(left, right core.ChunkKey) int {
		switch {
		case chunkKeyLess(left, right):
			return -1
		case chunkKeyLess(right, left):
			return 1
		default:
			return 0
		}
	})
}

func chunkKeyLess(left, right core.ChunkKey) bool {
	if left.Dimension != right.Dimension {
		return left.Dimension < right.Dimension
	}
	if left.Pos.X != right.Pos.X {
		return left.Pos.X < right.Pos.X
	}
	return left.Pos.Z < right.Pos.Z
}
