package client

import (
	"errors"
	"math"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/physics"
)

const (
	predictionHistoryCapacity = 256
	maxPredictionSteps        = 5
)

// Control 是渲染帧提供给固定步预测器的当前控制意图。
type Control struct {
	MoveX int8
	MoveZ int8
	Jump  bool
	Yaw   float32
	Pitch float32
}

// ReconcileResult 描述权威状态和解对视角的影响。
type ReconcileResult struct {
	ResetView bool
	Yaw       float32
	Pitch     float32
}

type predictedInput struct {
	sequence uint64
	input    physics.Input
}

// Predictor 使用与服务端共享的固定步物理维护本地玩家预测状态。
type Predictor struct {
	ready               bool
	dimension           core.DimensionID
	current, previous   physics.State
	accumulator         time.Duration
	history             []predictedInput
	lastServerTick      uint64
	maxSentInput        uint64
	suspended           bool
	suspendSequence     uint64
	displayOffset       mgl32.Vec3
	correctionRemaining time.Duration
}

// NewPredictor 创建具有固定历史容量的未就绪预测器。
func NewPredictor() *Predictor {
	return &Predictor{
		history: make([]predictedInput, 0, predictionHistoryCapacity),
	}
}

// Begin 从第一条有限且 Ready 的权威状态开始预测。
func (p *Predictor) Begin(message network.PlayerState) error {
	state := physics.State{
		Position: message.Position,
		Velocity: message.Velocity,
		OnGround: message.OnGround,
	}
	if !message.Ready {
		return errors.New("client: cannot begin prediction before player is ready")
	}
	if !physics.ValidState(state) || !finiteFloat32(message.Yaw) ||
		!finiteFloat32(message.Pitch) {
		return errors.New("client: cannot begin prediction from non-finite state")
	}

	p.ready = true
	p.dimension = message.Dimension
	p.current = state
	p.previous = state
	p.accumulator = 0
	p.history = p.history[:0]
	p.lastServerTick = message.ServerTick
	p.maxSentInput = message.LastInputSequence
	p.suspended = false
	p.suspendSequence = 0
	p.displayOffset = mgl32.Vec3{}
	p.correctionRemaining = 0
	return nil
}

// Advance 累积渲染帧时间，并执行有上限的固定物理步。
func (p *Predictor) Advance(
	elapsed time.Duration,
	control Control,
	source physics.CollisionSource,
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
		if p.suspendSequence != 0 {
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
			p.suspended = true
			p.accumulator = 0
			return p.sendNeutral(control, nextSequence, send)
		}

		message := network.PlayerInput{
			Sequence: nextSequence(),
			MoveX:    control.MoveX,
			MoveZ:    control.MoveZ,
			Jump:     control.Jump,
			Yaw:      control.Yaw,
			Pitch:    control.Pitch,
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
		p.current = physics.Step(
			p.current,
			p.history[len(p.history)-1].input,
			source,
		).State
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
	p.maxSentInput = message.Sequence
	return nil
}

// State 返回当前预测物理状态以及预测器是否已就绪。
func (p *Predictor) State() (physics.State, bool) {
	return p.current, p.ready
}

// HistoryLen 返回尚未被权威状态确认的输入数量。
func (p *Predictor) HistoryLen() int {
	return len(p.history)
}

// Suspended 报告预测是否因历史达到容量而暂停。
func (p *Predictor) Suspended() bool {
	return p.suspended
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
