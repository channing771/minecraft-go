package client

import (
	"errors"
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
	MoveX  int8
	MoveZ  int8
	Jump   bool
	Yaw    float32
	Pitch  float32
	Mining bool
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
	suspendInputSent    bool
	displayOffset       mgl32.Vec3
	correctionRemaining time.Duration
	health              uint8
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
	if !core.ValidHealth(message.Health) {
		return errors.New("client: cannot begin prediction from invalid health")
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
	p.suspendInputSent = false
	p.displayOffset = mgl32.Vec3{}
	p.correctionRemaining = 0
	p.health = message.Health
	return nil
}

// State 返回当前预测物理状态以及预测器是否已就绪。
func (p *Predictor) State() (physics.State, bool) {
	return p.current, p.ready
}

// Health 返回只读镜像持有的权威生命值以及预测器是否已就绪。
// 生命值只接受服务端确认值，客户端不对其做任何预测。
func (p *Predictor) Health() (uint8, bool) {
	return p.health, p.ready
}

// HistoryLen 返回尚未被权威状态确认的输入数量。
func (p *Predictor) HistoryLen() int {
	return len(p.history)
}

// Suspended 报告预测是否因历史达到容量而暂停。
func (p *Predictor) Suspended() bool {
	return p.suspended
}
