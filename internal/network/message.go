// Package network 定义端无关消息协议与传输接口。
package network

import (
	"errors"
	"math"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
)

// ClientMessage 是客户端可发送消息的封闭集合。
type ClientMessage interface {
	clientMessage()
}

// ServerMessage 是服务端可发送消息的封闭集合。
type ServerMessage interface {
	serverMessage()
}

// PlayerInput 仅承载玩家输入值；语义验证由 sim 和 client 完成。
type PlayerInput struct {
	Sequence uint64
	MoveX    int8
	MoveZ    int8
	Jump     bool
	Yaw      float32
	Pitch    float32
}

func (PlayerInput) clientMessage() {}
func (PlayerInput) clientPacket()  {}

func (input PlayerInput) Validate() error {
	if !finite32(input.Yaw) || !finite32(input.Pitch) {
		return errors.New("network: player input has non-finite rotation")
	}
	return nil
}

type BreakBlock struct {
	Sequence   uint64
	Yaw, Pitch float32
}

func (BreakBlock) clientMessage() {}
func (BreakBlock) clientPacket()  {}

func (command BreakBlock) Validate() error {
	if !finite32(command.Yaw) || !finite32(command.Pitch) {
		return errors.New("network: break block has non-finite rotation")
	}
	return nil
}

type PlaceBlock struct {
	Sequence   uint64
	Yaw, Pitch float32
	Block      core.BlockID
}

func (PlaceBlock) clientMessage() {}
func (PlaceBlock) clientPacket()  {}

func (command PlaceBlock) Validate() error {
	if !finite32(command.Yaw) || !finite32(command.Pitch) {
		return errors.New("network: place block has non-finite rotation")
	}
	if !validBlockID(command.Block) {
		return errors.New("network: place block ID exceeds 15 bits")
	}
	return nil
}

type RequestChunkResync struct {
	Sequence     uint64
	Dimension    core.DimensionID
	Chunk        core.ChunkPos
	HaveRevision uint64
}

func (RequestChunkResync) clientMessage() {}
func (RequestChunkResync) clientPacket()  {}

func (request RequestChunkResync) Validate() error {
	if request.Dimension != core.Overworld {
		return errors.New("network: chunk resync dimension is not overworld")
	}
	return nil
}

type ForgetChunks struct {
	Dimension core.DimensionID
	Chunks    []core.ChunkPos
}

func (ForgetChunks) serverMessage() {}
func (ForgetChunks) serverPacket()  {}

func (forget ForgetChunks) Validate() error {
	if len(forget.Chunks) < 1 || len(forget.Chunks) > 4096 {
		return errors.New("network: forget chunks count is outside 1..4096")
	}
	seen := make(map[core.ChunkPos]struct{}, len(forget.Chunks))
	for _, chunk := range forget.Chunks {
		if _, duplicate := seen[chunk]; duplicate {
			return errors.New("network: forget chunks contains a duplicate chunk")
		}
		seen[chunk] = struct{}{}
	}
	return nil
}

type PlayerState struct {
	ServerTick        uint64
	LastInputSequence uint64
	Dimension         core.DimensionID
	Position          mgl32.Vec3
	Velocity          mgl32.Vec3
	Yaw, Pitch        float32
	OnGround          bool
	Ready             bool
	Reset             bool
}

func (PlayerState) serverMessage() {}
func (PlayerState) serverPacket()  {}

func (state PlayerState) Validate() error {
	for _, value := range state.Position {
		if !finite32(value) {
			return errors.New("network: player state has non-finite position")
		}
	}
	for _, value := range state.Velocity {
		if !finite32(value) {
			return errors.New("network: player state has non-finite velocity")
		}
	}
	if !finite32(state.Yaw) || !finite32(state.Pitch) {
		return errors.New("network: player state has non-finite rotation")
	}
	return nil
}

type RejectReason string

const (
	RejectInvalidRay     RejectReason = "invalid_ray"
	RejectNoTarget       RejectReason = "no_target"
	RejectChunkNotReady  RejectReason = "chunk_not_ready"
	RejectProtectedBlock RejectReason = "protected_block"
	RejectInvalidBlock   RejectReason = "invalid_block"
	RejectOccupied       RejectReason = "occupied"
	RejectInvalidInput   RejectReason = "invalid_input"
	RejectPlayerNotReady RejectReason = "player_not_ready"
)

type CommandRejected struct {
	Sequence uint64
	Reason   RejectReason
}

func (CommandRejected) serverMessage() {}
func (CommandRejected) serverPacket()  {}

func (rejection CommandRejected) Validate() error {
	if _, ok := commandRejectReasonID(rejection.Reason); !ok {
		return errors.New("network: unknown command rejection reason")
	}
	return nil
}

func finite32(value float32) bool {
	return !math.IsNaN(float64(value)) && !math.IsInf(float64(value), 0)
}
