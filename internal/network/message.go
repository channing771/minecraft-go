// Package network 定义端无关消息协议与传输接口。
package network

import (
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

type SetViewCenter struct {
	Sequence  uint64
	Dimension core.DimensionID
	Center    core.ChunkPos
}

func (SetViewCenter) clientMessage() {}

type BreakRay struct {
	Sequence  uint64
	Dimension core.DimensionID
	Origin    mgl32.Vec3
	Direction mgl32.Vec3
}

func (BreakRay) clientMessage() {}

type PlaceRay struct {
	Sequence  uint64
	Dimension core.DimensionID
	Origin    mgl32.Vec3
	Direction mgl32.Vec3
	Block     core.BlockID
}

func (PlaceRay) clientMessage() {}

type RequestChunkResync struct {
	Sequence     uint64
	Dimension    core.DimensionID
	Chunk        core.ChunkPos
	HaveRevision uint64
}

func (RequestChunkResync) clientMessage() {}

type ForgetChunks struct {
	Dimension core.DimensionID
	Chunks    []core.ChunkPos
}

func (ForgetChunks) serverMessage() {}

type RejectReason string

const (
	RejectInvalidRay     RejectReason = "invalid_ray"
	RejectNoTarget       RejectReason = "no_target"
	RejectChunkNotReady  RejectReason = "chunk_not_ready"
	RejectProtectedBlock RejectReason = "protected_block"
	RejectInvalidBlock   RejectReason = "invalid_block"
	RejectOccupied       RejectReason = "occupied"
)

type CommandRejected struct {
	Sequence uint64
	Reason   RejectReason
}

func (CommandRejected) serverMessage() {}
