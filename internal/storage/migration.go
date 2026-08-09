package storage

import (
	"fmt"
	"math"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

const oldestChunkSchema uint32 = 1

type chunkDTO struct {
	Key      core.ChunkKey
	Revision uint64
	Sections [core.SectionsPerChunk]world.ContainerSnapshot
	Drops    [core.DropsPerChunk]world.DropSlot
	Furnaces [core.FurnacesPerChunk]world.FurnaceSlot
	Chests   [core.ChestsPerChunk]world.ChestSlot
}

type chunkMigration func(chunkDTO) (chunkDTO, error)

var chunkMigrations = map[uint32]chunkMigration{
	// v1 没有掉落物负载，确定性迁移为全部空槽。
	1: func(dto chunkDTO) (chunkDTO, error) {
		dto.Drops = [core.DropsPerChunk]world.DropSlot{}
		return dto, nil
	},
	// v3 与 v2 的 payload 布局相同，只是让旧程序拒绝含新方块的记录。
	2: func(dto chunkDTO) (chunkDTO, error) { return dto, nil },
	// v3 没有熔炉负载，确定性迁移为全部空槽。
	3: func(dto chunkDTO) (chunkDTO, error) {
		dto.Furnaces = [core.FurnacesPerChunk]world.FurnaceSlot{}
		return dto, nil
	},
	// v4 的掉落物没有耐久字段，并可能含遗留的多件工具堆。
	4: func(dto chunkDTO) (chunkDTO, error) {
		for slot := range dto.Drops {
			dto.Drops[slot].Stack = fillFullDurability(dto.Drops[slot].Stack)
		}
		return normalizeV4LegacyToolDropStacks(dto)
	},
	// v5 没有箱子负载，确定性迁移为全部空槽。
	5: func(dto chunkDTO) (chunkDTO, error) {
		dto.Chests = [core.ChestsPerChunk]world.ChestSlot{}
		return dto, nil
	},
	// v7 与 v6 的 payload 布局相同，只扩展合法方块注册表。
	6: func(dto chunkDTO) (chunkDTO, error) { return dto, nil },
}

// fillFullDurability 把没有耐久的旧工具补为满耐久，非工具保持零值。
func fillFullDurability(stack core.ItemStack) core.ItemStack {
	full, ok := core.ItemMaxDurability(stack.Item)
	if !ok || stack.Durability != 0 {
		return stack
	}
	stack.Durability = full
	return stack
}

func normalizeV4LegacyToolDropStacks(dto chunkDTO) (chunkDTO, error) {
	sources := dto.Drops
	for sourceSlot, source := range sources {
		if !source.Active || source.Stack.Count <= 1 {
			continue
		}
		if _, ok := core.ItemMaxDurability(source.Stack.Item); !ok {
			continue
		}

		dto.Drops[sourceSlot].Stack.Count = 1
		for extra := uint8(1); extra < source.Stack.Count; extra++ {
			targetSlot := -1
			for slot, candidate := range dto.Drops {
				if !candidate.Active && candidate.Generation != math.MaxUint32 {
					targetSlot = slot
					break
				}
			}
			if targetSlot < 0 {
				return chunkDTO{}, fmt.Errorf(
					"%w: legacy tool drop slot %d has insufficient reusable drop slots",
					ErrCorrupt, sourceSlot,
				)
			}
			target := source
			target.Generation = dto.Drops[targetSlot].Generation + 1
			target.Stack.Count = 1
			dto.Drops[targetSlot] = target
		}
	}
	return dto, nil
}

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
