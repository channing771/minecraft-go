package storage

import (
	"fmt"

	"github.com/channing771/mornlea/internal/core"
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
	// Health 是权威生命值，0..core.MaxHealth。
	Health uint8
	// Hunger 是权威饥饿值，0..core.MaxHunger。
	Hunger uint8
	// SaturationMilli 是权威饱和度（千分位），上界是 Hunger×core.SaturationMilliPerPoint。
	SaturationMilli uint16
	// ExhaustionMilli 是权威疲劳值（千分位）。
	ExhaustionMilli uint16
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
	// v3 没有耐久字段，旧工具一律迁移为满耐久。
	3: func(dto playerDTO) (playerDTO, error) {
		for slot, stack := range dto.Inventory.Hotbar.Slots {
			dto.Inventory.Hotbar.Slots[slot] = fillFullDurability(stack)
		}
		for slot, stack := range dto.Inventory.Backpack {
			dto.Inventory.Backpack[slot] = fillFullDurability(stack)
		}
		return dto, nil
	},
	// v4 没有生命值字段，历史存档一律迁移为满血。
	4: func(dto playerDTO) (playerDTO, error) {
		dto.Health = core.MaxHealth
		return dto, nil
	},
	// v6 与 v5 的 payload 布局相同，只扩展合法物品注册表。
	5: func(dto playerDTO) (playerDTO, error) { return dto, nil },
	// v6 没有三层饥饿状态，历史存档一律迁移为新玩家初值。初值与 sim 侧的
	// resetHunger 同源（core.MaxHunger / core.InitialSaturationMilli / 0），
	// 因此"旧存档登录"与"新玩家登录"得到的饥饿状态逐字段相同。
	6: func(dto playerDTO) (playerDTO, error) {
		dto.Hunger = core.MaxHunger
		dto.SaturationMilli = core.InitialSaturationMilli
		dto.ExhaustionMilli = 0
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
