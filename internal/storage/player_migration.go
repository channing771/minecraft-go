package storage

import (
	"fmt"

	"minecraft-go/internal/core"
)

const oldestPlayerSchema uint32 = 1

type playerDTO struct {
	PlayerID    core.PlayerID
	Revision    uint64
	DisplayName string
	Current     PlayerLocation
	Yaw, Pitch  float32
	Safe        *PlayerLocation
	Inventory   core.Inventory
}

type playerMigration func(playerDTO) (playerDTO, error)

var playerMigrations = map[uint32]playerMigration{
	// v1 没有物品负载，确定性迁移为空快捷栏且选中栏位 0。
	1: func(dto playerDTO) (playerDTO, error) {
		dto.Inventory.Hotbar = core.Hotbar{}
		return dto, nil
	},
	// v2 没有背包负载，确定性迁移为空背包并保留既有快捷栏。
	2: func(dto playerDTO) (playerDTO, error) {
		dto.Inventory.Backpack = [core.BackpackSlots]core.ItemStack{}
		return dto, nil
	},
}

func migratePlayer(from uint32, dto playerDTO) (playerDTO, bool, error) {
	if from > currentPlayerSchema {
		return playerDTO{}, false, fmt.Errorf("%w: player schema %d", ErrFutureVersion, from)
	}
	migrated := false
	for version := from; version < currentPlayerSchema; version++ {
		migration, ok := playerMigrations[version]
		if !ok {
			return playerDTO{}, false, fmt.Errorf("storage: missing player migration %d", version)
		}
		var err error
		dto, err = migration(clonePlayerDTO(dto))
		if err != nil {
			return playerDTO{}, false, fmt.Errorf("migrate player %d: %w", version, err)
		}
		migrated = true
	}
	return dto, migrated, nil
}

func clonePlayerDTO(dto playerDTO) playerDTO {
	clone := dto
	if dto.Safe != nil {
		safe := *dto.Safe
		clone.Safe = &safe
	}
	return clone
}
