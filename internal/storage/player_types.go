package storage

import (
	"context"

	"github.com/channing771/mornlea/internal/core"
)

type PlayerLocation struct {
	Dimension core.DimensionID
	Position  [3]float32
}

type StoredPlayer struct {
	PlayerID    core.PlayerID
	Revision    uint64
	DisplayName string
	Current     PlayerLocation
	Yaw, Pitch  float32
	Safe        *PlayerLocation
	Inventory   core.Inventory
	// Health 是权威生命值，0..core.MaxHealth。
	Health uint8
	// Hunger、SaturationMilli、ExhaustionMilli 是三层权威饥饿状态，schema v7 起
	// 落盘；更旧的存档读入时按 core 的固定初值迁移。
	Hunger          uint8
	SaturationMilli uint16
	ExhaustionMilli uint16
	NeedsRewrite    bool
}

type PlayerSave struct {
	PlayerID    core.PlayerID
	Revision    uint64
	DisplayName string
	Current     PlayerLocation
	Yaw, Pitch  float32
	Safe        *PlayerLocation
	Inventory   core.Inventory
	// Health 是权威生命值，0..core.MaxHealth。
	Health uint8
	// Hunger、SaturationMilli、ExhaustionMilli 是三层权威饥饿状态，schema v7 起
	// 落盘。三者都可能合法地为零（饿到零、无饱和、无疲劳），因此**没有**
	// "零值代表缺失"这种约定：缺失只可能来自更旧的 schema，由迁移链补初值。
	Hunger          uint8
	SaturationMilli uint16
	ExhaustionMilli uint16
}

type PlayerStore interface {
	LoadPlayer(context.Context, core.PlayerID) (StoredPlayer, error)
	SavePlayer(context.Context, PlayerSave) (uint64, error)
}
