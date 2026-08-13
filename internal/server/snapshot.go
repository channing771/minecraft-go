package server

import (
	"errors"
	"fmt"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/world"
)

func buildChunkSnapshot(
	dimension core.DimensionID,
	chunk *world.Chunk,
	revision uint64,
) (network.ChunkSnapshot, error) {
	if chunk == nil {
		return network.ChunkSnapshot{}, errors.New("server: nil chunk snapshot source")
	}
	message := network.ChunkSnapshot{
		Dimension: dimension,
		Chunk:     chunk.Pos,
		Revision:  revision,
		Sections:  make([]network.SectionData, core.SectionsPerChunk),
	}
	for index := 0; index < core.SectionsPerChunk; index++ {
		snapshot := chunk.Section(index).Blocks.Snapshot()
		storage, err := networkStorage(snapshot.Kind)
		if err != nil {
			return network.ChunkSnapshot{}, err
		}
		message.Sections[index] = network.SectionData{
			Y:       int32(index),
			Storage: storage,
			Single:  snapshot.Single,
			Bits:    snapshot.Bits,
			Palette: append([]core.BlockID(nil), snapshot.Palette...),
			Packed:  append([]uint64(nil), snapshot.Packed...),
		}
	}
	if err := message.Validate(); err != nil {
		return network.ChunkSnapshot{}, fmt.Errorf(
			"server: built invalid chunk snapshot: %w",
			err,
		)
	}
	return message, nil
}

func networkStorage(kind world.StorageKind) (network.SectionStorage, error) {
	switch kind {
	case world.StorageSingle:
		return network.SectionSingle, nil
	case world.StorageIndexed:
		return network.SectionIndexed, nil
	case world.StorageDirect:
		return network.SectionDirect, nil
	default:
		return 0, fmt.Errorf("server: unknown world storage %d", kind)
	}
}
