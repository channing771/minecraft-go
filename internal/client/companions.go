package client

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
)

var ErrCompanionProtocol = errors.New("companion protocol error")

type CompanionPresentation struct {
	ID        companion.ID
	Name      string
	Dimension core.DimensionID
	Position  mgl32.Vec3
	Yaw       float32
	Pitch     float32
}

type companionPresentationState struct {
	name string
	remoteActor
}

type Companions struct {
	values map[companion.ID]*companionPresentationState
}

func (companions *Companions) ApplySpawn(spawn network.CompanionSpawn) error {
	if err := spawn.Validate(); err != nil {
		return companionProtocolError("CompanionSpawn: %v", err)
	}
	if _, exists := companions.values[spawn.ID]; exists {
		return companionProtocolError("CompanionSpawn: companion %s is already present", spawn.ID)
	}
	if len(companions.values) >= companion.MaxActive {
		return companionProtocolError("CompanionSpawn: companion count exceeds %d", companion.MaxActive)
	}
	if companions.values == nil {
		companions.values = make(map[companion.ID]*companionPresentationState, companion.MaxActive)
	}
	state := &companionPresentationState{name: spawn.Name}
	state.pushSnapshot(remoteSnapshot{
		tick: spawn.Tick, dimension: spawn.Dimension, position: spawn.Position,
		yaw: spawn.Yaw, pitch: spawn.Pitch,
	}, true)
	companions.values[spawn.ID] = state
	return nil
}

func (companions *Companions) ApplyStates(states network.CompanionStates) error {
	if err := states.Validate(); err != nil {
		return companionProtocolError("CompanionStates: %v", err)
	}
	for _, update := range states.States {
		state, exists := companions.values[update.ID]
		if !exists {
			return companionProtocolError(
				"CompanionStates: companion %s tick %d is unknown", update.ID, states.Tick,
			)
		}
		if states.Tick <= state.lastTick {
			return companionProtocolError(
				"CompanionStates: companion %s tick %d is not newer than %d",
				update.ID, states.Tick, state.lastTick,
			)
		}
	}
	for _, update := range states.States {
		companions.values[update.ID].pushSnapshot(remoteSnapshot{
			tick: states.Tick, dimension: update.Dimension, position: update.Position,
			yaw: update.Yaw, pitch: update.Pitch,
		}, update.Reset)
	}
	return nil
}

func (companions *Companions) ApplyDespawn(despawn network.CompanionDespawn) error {
	if err := despawn.Validate(); err != nil {
		return companionProtocolError("CompanionDespawn: %v", err)
	}
	if _, exists := companions.values[despawn.ID]; !exists {
		return companionProtocolError("CompanionDespawn: companion %s is unknown", despawn.ID)
	}
	delete(companions.values, despawn.ID)
	return nil
}

func (companions *Companions) Advance(elapsed time.Duration) {
	for _, state := range companions.values {
		state.advance(elapsed)
	}
}

func (companions *Companions) AppendPresentations(dst []CompanionPresentation) []CompanionPresentation {
	for id, state := range companions.values {
		dst = append(dst, CompanionPresentation{
			ID: id, Name: state.name, Dimension: state.dimension,
			Position: state.position, Yaw: state.yaw, Pitch: state.pitch,
		})
	}
	slices.SortFunc(dst, func(left, right CompanionPresentation) int {
		return bytes.Compare(left.ID[:], right.ID[:])
	})
	return dst
}

func (companions *Companions) Reset() {
	clear(companions.values)
}

func companionProtocolError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrCompanionProtocol, fmt.Sprintf(format, arguments...))
}
