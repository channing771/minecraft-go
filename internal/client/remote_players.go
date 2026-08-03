package client

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
)

var ErrRemotePlayerProtocol = errors.New("remote player protocol error")

type RemotePresentation struct {
	PlayerID    core.PlayerID
	DisplayName string
	Dimension   core.DimensionID
	Position    mgl32.Vec3
	Yaw         float32
	Pitch       float32
}

type remotePlayer struct {
	displayName string
	dimension   core.DimensionID
	position    mgl32.Vec3
	yaw         float32
	pitch       float32
	lastTick    uint64
	snapshots   remoteSnapshotRing
	elapsed     time.Duration
}

type RemotePlayers struct {
	players map[core.PlayerID]*remotePlayer
}

func NewRemotePlayers() *RemotePlayers {
	return &RemotePlayers{players: make(map[core.PlayerID]*remotePlayer)}
}

func (players *RemotePlayers) Apply(message network.ServerMessage) error {
	switch message := message.(type) {
	case network.RemotePlayerSpawn:
		return players.applySpawn(message)
	case *network.RemotePlayerSpawn:
		if message == nil {
			return remotePlayerProtocolError("RemotePlayerSpawn: message is nil")
		}
		return players.applySpawn(*message)
	case network.RemotePlayerDespawn:
		return players.applyDespawn(message)
	case *network.RemotePlayerDespawn:
		if message == nil {
			return remotePlayerProtocolError("RemotePlayerDespawn: message is nil")
		}
		return players.applyDespawn(*message)
	case network.RemotePlayerStates:
		return players.applyStates(message)
	case *network.RemotePlayerStates:
		if message == nil {
			return remotePlayerProtocolError("RemotePlayerStates: message is nil")
		}
		return players.applyStates(*message)
	default:
		return remotePlayerProtocolError("%T: message is not remote-player data", message)
	}
}

func (players *RemotePlayers) Presentations() []RemotePresentation {
	presentations := make([]RemotePresentation, 0, len(players.players))
	for playerID, player := range players.players {
		presentations = append(presentations, RemotePresentation{
			PlayerID:    playerID,
			DisplayName: player.displayName,
			Dimension:   player.dimension,
			Position:    player.position,
			Yaw:         player.yaw,
			Pitch:       player.pitch,
		})
	}
	sort.Slice(presentations, func(i, j int) bool {
		return bytes.Compare(presentations[i].PlayerID[:], presentations[j].PlayerID[:]) < 0
	})
	return presentations
}

func (players *RemotePlayers) AppendPresentations(dst []RemotePresentation) []RemotePresentation {
	for playerID, player := range players.players {
		dst = append(dst, RemotePresentation{
			PlayerID:    playerID,
			DisplayName: player.displayName,
			Dimension:   player.dimension,
			Position:    player.position,
			Yaw:         player.yaw,
			Pitch:       player.pitch,
		})
	}
	slices.SortFunc(dst, func(left, right RemotePresentation) int {
		return bytes.Compare(left.PlayerID[:], right.PlayerID[:])
	})
	return dst
}

func (players *RemotePlayers) Reset() {
	clear(players.players)
}

func (players *RemotePlayers) applySpawn(spawn network.RemotePlayerSpawn) error {
	if err := validateRemotePlayerSpawn(spawn); err != nil {
		return err
	}
	if _, exists := players.players[spawn.PlayerID]; exists {
		return remotePlayerProtocolError(
			"RemotePlayerSpawn: player %s tick %d is already present",
			spawn.PlayerID, spawn.ServerTick,
		)
	}
	player := &remotePlayer{displayName: spawn.DisplayName}
	player.pushSnapshot(remoteSnapshot{
		tick:      spawn.ServerTick,
		dimension: spawn.Dimension,
		position:  spawn.Position,
		yaw:       spawn.Yaw,
		pitch:     spawn.Pitch,
	}, true)
	players.players[spawn.PlayerID] = player
	return nil
}

func (players *RemotePlayers) applyDespawn(despawn network.RemotePlayerDespawn) error {
	if !despawn.PlayerID.Valid() {
		return remotePlayerProtocolError(
			"RemotePlayerDespawn: player %s is not a UUIDv4", despawn.PlayerID,
		)
	}
	if _, exists := players.players[despawn.PlayerID]; !exists {
		return remotePlayerProtocolError(
			"RemotePlayerDespawn: player %s is unknown", despawn.PlayerID,
		)
	}
	delete(players.players, despawn.PlayerID)
	return nil
}

func (players *RemotePlayers) applyStates(states network.RemotePlayerStates) error {
	if err := validateRemotePlayerStates(states); err != nil {
		return err
	}
	for _, state := range states.Players {
		player, exists := players.players[state.PlayerID]
		if !exists {
			return remotePlayerProtocolError(
				"RemotePlayerStates: player %s tick %d is unknown",
				state.PlayerID, states.ServerTick,
			)
		}
		if states.ServerTick <= player.lastTick {
			return remotePlayerProtocolError(
				"RemotePlayerStates: player %s tick %d is not newer than %d",
				state.PlayerID, states.ServerTick, player.lastTick,
			)
		}
	}
	for _, state := range states.Players {
		player := players.players[state.PlayerID]
		player.pushSnapshot(remoteSnapshot{
			tick:      states.ServerTick,
			dimension: state.Dimension,
			position:  state.Position,
			yaw:       state.Yaw,
			pitch:     state.Pitch,
		}, state.Reset)
	}
	return nil
}

func validateRemotePlayerSpawn(spawn network.RemotePlayerSpawn) error {
	prefix := fmt.Sprintf("RemotePlayerSpawn: player %s tick %d", spawn.PlayerID, spawn.ServerTick)
	if !spawn.PlayerID.Valid() {
		return remotePlayerProtocolError("%s is not a UUIDv4", prefix)
	}
	canonicalName, err := core.NormalizeDisplayName(spawn.DisplayName)
	if err != nil || canonicalName != spawn.DisplayName {
		return remotePlayerProtocolError("%s has a noncanonical display name", prefix)
	}
	if spawn.Dimension != core.Overworld {
		return remotePlayerProtocolError("%s has invalid dimension %d", prefix, spawn.Dimension)
	}
	if !remoteVec3Finite(spawn.Position) || !remoteFloatFinite(spawn.Yaw) || !remoteFloatFinite(spawn.Pitch) {
		return remotePlayerProtocolError("%s has nonfinite motion", prefix)
	}
	return nil
}

func validateRemotePlayerStates(states network.RemotePlayerStates) error {
	if len(states.Players) < 1 || len(states.Players) > 7 {
		return remotePlayerProtocolError(
			"RemotePlayerStates: tick %d has player count %d outside 1..7",
			states.ServerTick, len(states.Players),
		)
	}
	for index, state := range states.Players {
		prefix := fmt.Sprintf("RemotePlayerStates: player %s tick %d", state.PlayerID, states.ServerTick)
		if !state.PlayerID.Valid() {
			return remotePlayerProtocolError("%s is not a UUIDv4", prefix)
		}
		if state.Dimension != core.Overworld {
			return remotePlayerProtocolError("%s has invalid dimension %d", prefix, state.Dimension)
		}
		if !remoteVec3Finite(state.Position) || !remoteFloatFinite(state.Yaw) || !remoteFloatFinite(state.Pitch) {
			return remotePlayerProtocolError("%s has nonfinite motion", prefix)
		}
		if index > 0 && bytes.Compare(states.Players[index-1].PlayerID[:], state.PlayerID[:]) >= 0 {
			return remotePlayerProtocolError("%s is not strictly sorted", prefix)
		}
	}
	return nil
}

func remoteFloatFinite(value float32) bool {
	return !math.IsNaN(float64(value)) && !math.IsInf(float64(value), 0)
}

func remoteVec3Finite(value mgl32.Vec3) bool {
	return remoteFloatFinite(value[0]) && remoteFloatFinite(value[1]) && remoteFloatFinite(value[2])
}

func remotePlayerProtocolError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrRemotePlayerProtocol, fmt.Sprintf(format, arguments...))
}
