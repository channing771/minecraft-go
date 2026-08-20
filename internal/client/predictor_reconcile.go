package client

import (
	"errors"
	"math"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/physics"
)

// ApplyPlayerState 应用更新的权威玩家状态并重放尚未确认的输入。
func (p *Predictor) ApplyPlayerState(
	message network.PlayerState,
	source physics.WorldSource,
) (ReconcileResult, error) {
	if message.ServerTick <= p.lastServerTick {
		return ReconcileResult{}, nil
	}
	authority, err := validatePlayerState(message, p.maxSentInput)
	if err != nil {
		return ReconcileResult{}, err
	}

	if !message.Ready {
		p.clearForNotReady(message)
		return ReconcileResult{}, nil
	}
	if !p.ready || message.Reset || message.Dimension != p.dimension {
		if err := p.Begin(message); err != nil {
			return ReconcileResult{}, err
		}
		return ReconcileResult{
			ResetView: true,
			Yaw:       message.Yaw,
			Pitch:     message.Pitch,
		}, nil
	}
	if p.suspended && (!p.suspendInputSent || message.LastInputSequence < p.suspendSequence) {
		p.lastServerTick = message.ServerTick
		return ReconcileResult{}, nil
	}

	oldDisplayed := p.presentationPositionNoAdvance()
	oldPredicted := p.current.Position
	oldCorrectionRemaining := p.correctionRemaining
	p.current = authority
	p.previous = authority
	p.health = message.Health
	p.oxygen = message.Oxygen
	if p.suspended {
		p.history = p.history[:0]
		p.accumulator = 0
		p.suspended = false
		p.suspendSequence = 0
		p.suspendInputSent = false
	} else {
		p.dropAcknowledged(message.LastInputSequence)
		for _, entry := range p.history {
			p.previous = p.current
			p.current = stepWithSubmersion(p.current, entry.input, source)
		}
	}
	p.lastServerTick = message.ServerTick

	errorDistance := p.current.Position.Sub(oldPredicted).Len()
	switch {
	case errorDistance >= 0.5:
		p.displayOffset = mgl32.Vec3{}
		p.correctionRemaining = 0
	case errorDistance >= 1.0/128:
		p.displayOffset = oldDisplayed.Sub(p.interpolatedPosition())
		p.correctionRemaining = 100 * time.Millisecond
	default:
		p.displayOffset = oldDisplayed.Sub(p.interpolatedPosition())
		if p.displayOffset == (mgl32.Vec3{}) {
			p.correctionRemaining = 0
			break
		}
		remainingStep := max(time.Duration(0), physics.FixedDelta-p.accumulator)
		p.correctionRemaining = max(oldCorrectionRemaining, remainingStep)
		if p.correctionRemaining <= 0 {
			p.displayOffset = mgl32.Vec3{}
		}
	}
	return ReconcileResult{}, nil
}

func (p *Predictor) clearForNotReady(message network.PlayerState) {
	p.ready = false
	p.dimension = message.Dimension
	p.current = physics.State{}
	p.previous = physics.State{}
	p.accumulator = 0
	p.history = p.history[:0]
	p.lastServerTick = message.ServerTick
	p.maxSentInput = message.LastInputSequence
	p.suspended = false
	p.suspendSequence = 0
	p.suspendInputSent = false
	p.displayOffset = mgl32.Vec3{}
	p.correctionRemaining = 0
	p.health = 0
}

func validatePlayerState(message network.PlayerState, maxSentInput uint64) (physics.State, error) {
	state := physics.State{
		Position: message.Position,
		Velocity: message.Velocity,
		OnGround: message.OnGround,
	}
	if message.Dimension != core.Overworld {
		return physics.State{}, errors.New("client: player state has unknown dimension")
	}
	if !physics.ValidState(state) || !finiteFloat32(message.Yaw) ||
		!finiteFloat32(message.Pitch) {
		return physics.State{}, errors.New("client: player state contains non-finite value")
	}
	if !core.ValidHealth(message.Health) {
		return physics.State{}, errors.New("client: player state has out-of-range health")
	}
	if !core.ValidOxygen(message.Oxygen) {
		return physics.State{}, errors.New("client: player state has out-of-range oxygen")
	}
	const maxPitch = float32(math.Pi/2 - 0.01)
	if message.Pitch < -maxPitch || message.Pitch > maxPitch {
		return physics.State{}, errors.New("client: player state has invalid pitch")
	}
	if message.LastInputSequence > maxSentInput {
		return physics.State{}, errors.New("client: player state acknowledges unsent input")
	}
	if message.Reset && !message.Ready {
		return physics.State{}, errors.New("client: reset player state is not ready")
	}
	return state, nil
}
