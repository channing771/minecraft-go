package storage

import (
	"fmt"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

const oldestChunkSchema uint32 = 1

type chunkDTO struct {
	Key      core.ChunkKey
	Revision uint64
	Sections [core.SectionsPerChunk]world.ContainerSnapshot
}

type chunkMigration func(chunkDTO) (chunkDTO, error)

var chunkMigrations = map[uint32]chunkMigration{}

func migrateChunk(from uint32, dto chunkDTO) (chunkDTO, bool, error) {
	if from > currentChunkSchema {
		return chunkDTO{}, false, fmt.Errorf("%w: chunk schema %d", ErrFutureVersion, from)
	}

	migrated := false
	for version := from; version < currentChunkSchema; version++ {
		migration, ok := chunkMigrations[version]
		if !ok {
			return chunkDTO{}, false, fmt.Errorf("storage: missing migration %d", version)
		}
		var err error
		dto, err = migration(cloneChunkDTO(dto))
		if err != nil {
			return chunkDTO{}, false, fmt.Errorf("migrate %d: %w", version, err)
		}
		migrated = true
	}
	return dto, migrated, nil
}

func cloneChunkDTO(dto chunkDTO) chunkDTO {
	clone := dto
	for index, section := range dto.Sections {
		clone.Sections[index] = world.ContainerSnapshot{
			Kind:    section.Kind,
			Single:  section.Single,
			Bits:    section.Bits,
			Palette: append([]core.BlockID(nil), section.Palette...),
			Packed:  append([]uint64(nil), section.Packed...),
		}
	}
	return clone
}
