package storage

import (
	"context"

	"minecraft-go/internal/core"
)

type PlayerLocation struct {
	Dimension core.DimensionID
	Position  [3]float32
}

type StoredPlayer struct {
	PlayerID     core.PlayerID
	Revision     uint64
	DisplayName  string
	Current      PlayerLocation
	Yaw, Pitch   float32
	Safe         *PlayerLocation
	Hotbar       core.Hotbar
	NeedsRewrite bool
}

type PlayerSave struct {
	PlayerID    core.PlayerID
	Revision    uint64
	DisplayName string
	Current     PlayerLocation
	Yaw, Pitch  float32
	Safe        *PlayerLocation
	Hotbar      core.Hotbar
}

type PlayerStore interface {
	LoadPlayer(context.Context, core.PlayerID) (StoredPlayer, error)
	SavePlayer(context.Context, PlayerSave) (uint64, error)
}
