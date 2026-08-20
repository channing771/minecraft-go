package client

import (
	"errors"
	"math"
	"time"

	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/physics"
)

// Advance 累积渲染帧时间，并执行有上限的固定物理步。
func (p *Predictor) Advance(
	elapsed time.Duration,
	control Control,
	source physics.WorldSource,
	nextSequence func() uint64,
	send func(network.PlayerInput) error,
) error {
	if err := validateControl(control); err != nil {
		return err
	}
	if !p.ready {
		return errors.New("client: predictor is not ready")
	}

	if p.suspended {
		if p.suspendInputSent {
			return nil
		}
		p.accumulator += elapsed
		if p.accumulator < physics.FixedDelta {
			return nil
		}
		p.accumulator = 0
		return p.sendNeutral(control, nextSequence, send)
	}

	p.accumulator += elapsed
	steps := int(p.accumulator / physics.FixedDelta)
	if steps == 0 {
		return nil
	}
	dropRemainder := steps > maxPredictionSteps
	if dropRemainder {
		steps = maxPredictionSteps
		p.accumulator = time.Duration(steps) * physics.FixedDelta
	}

	for range steps {
		if len(p.history) == predictionHistoryCapacity {
			frozenPosition := p.presentationPositionNoAdvance()
			p.suspended = true
			p.previous = p.current
			p.accumulator = 0
			p.displayOffset = frozenPosition.Sub(p.current.Position)
			p.correctionRemaining = 0
			return p.sendNeutral(control, nextSequence, send)
		}

		message := network.PlayerInput{
			Sequence: nextSequence(),
			MoveX:    control.MoveX,
			MoveZ:    control.MoveZ,
			Jump:     control.Jump,
			Yaw:      control.Yaw,
			Pitch:    control.Pitch,
			Mining:   control.Mining,
		}
		if err := send(message); err != nil {
			return err
		}
		p.maxSentInput = message.Sequence
		p.history = append(p.history, predictedInput{
			sequence: message.Sequence,
			input: physics.Input{
				MoveX: message.MoveX,
				MoveZ: message.MoveZ,
				Jump:  message.Jump,
				Yaw:   message.Yaw,
			},
		})
		p.previous = p.current
		p.current = stepWithSubmersion(p.current, p.history[len(p.history)-1].input, source)
		p.accumulator -= physics.FixedDelta
	}
	if dropRemainder {
		p.accumulator = 0
	}
	return nil
}

func (p *Predictor) sendNeutral(
	control Control,
	nextSequence func() uint64,
	send func(network.PlayerInput) error,
) error {
	message := network.PlayerInput{
		Sequence: nextSequence(),
		Yaw:      control.Yaw,
		Pitch:    control.Pitch,
	}
	if err := send(message); err != nil {
		return err
	}
	p.suspendSequence = message.Sequence
	p.suspendInputSent = true
	p.maxSentInput = message.Sequence
	return nil
}

func (p *Predictor) dropAcknowledged(sequence uint64) {
	firstUnacknowledged := 0
	for firstUnacknowledged < len(p.history) &&
		p.history[firstUnacknowledged].sequence <= sequence {
		firstUnacknowledged++
	}
	copy(p.history, p.history[firstUnacknowledged:])
	p.history = p.history[:len(p.history)-firstUnacknowledged]
}

func validateControl(control Control) error {
	if control.MoveX < -1 || control.MoveX > 1 ||
		control.MoveZ < -1 || control.MoveZ > 1 {
		return errors.New("client: invalid movement control")
	}
	const maxPitch = float32(math.Pi/2 - 0.01)
	if !finiteFloat32(control.Yaw) || !finiteFloat32(control.Pitch) ||
		control.Pitch < -maxPitch || control.Pitch > maxPitch {
		return errors.New("client: invalid look control")
	}
	return nil
}

func finiteFloat32(value float32) bool {
	return !math.IsNaN(float64(value)) && !math.IsInf(float64(value), 0)
}

// stepWithSubmersion 先按当前位置算好两个浸没标志，再推进一个固定步。
//
// 标志刻意不写回 predictedInput：权威侧每个 tick 都按「本 tick 开始时的位置」
// 重算，重放时若沿用记录当时的旧值，重放起点已经换成权威位置、标志却还是旧的，
// 两侧就会在水面附近分叉。重算的输入是位置与方块镜像，与权威侧同源。
func stepWithSubmersion(
	state physics.State,
	input physics.Input,
	source physics.WorldSource,
) physics.State {
	input.BodyInFluid, input.EyeInFluid = physics.SubmersionFlags(state.Position, source)
	return physics.Step(state, input, source).State
}
