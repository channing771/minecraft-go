package client

import (
	"fmt"

	"minecraft-go/internal/network"
	"minecraft-go/internal/world"
)

// chunkFromSnapshot 先完整验证和解码，再返回可整体替换进镜像的区块。
func chunkFromSnapshot(snapshot network.ChunkSnapshot) (*world.Chunk, error) {
	if err := snapshot.Validate(); err != nil {
		return nil, fmt.Errorf("client: invalid chunk snapshot: %w", err)
	}

	chunk := world.NewChunk(snapshot.Chunk)
	for index, section := range snapshot.Sections {
		kind, err := worldStorage(section.Storage)
		if err != nil {
			return nil, fmt.Errorf("client: section %d: %w", index, err)
		}
		container, err := world.NewPalettedContainerFromSnapshot(world.ContainerSnapshot{
			Kind:    kind,
			Single:  section.Single,
			Bits:    section.Bits,
			Palette: section.Palette,
			Packed:  section.Packed,
		})
		if err != nil {
			return nil, fmt.Errorf("client: import section %d: %w", index, err)
		}
		chunk.Section(index).Blocks = container
	}
	return chunk, nil
}

func worldStorage(storage network.SectionStorage) (world.StorageKind, error) {
	switch storage {
	case network.SectionSingle:
		return world.StorageSingle, nil
	case network.SectionIndexed:
		return world.StorageIndexed, nil
	case network.SectionDirect:
		return world.StorageDirect, nil
	default:
		return 0, fmt.Errorf("unknown network storage %d", storage)
	}
}
