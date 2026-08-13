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
	Health       uint8
	NeedsRewrite bool
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
}

type PlayerStore interface {
	LoadPlayer(context.Context, core.PlayerID) (StoredPlayer, error)
	SavePlayer(context.Context, PlayerSave) (uint64, error)
}
