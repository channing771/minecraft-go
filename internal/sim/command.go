package sim

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

type SessionID uint64

type CommandKind uint8

const (
	CommandSetViewCenter CommandKind = iota
	CommandBreakRay
	CommandPlaceRay
	CommandResync
	CommandPlayerInput CommandKind = 4
	CommandBreakBlock  CommandKind = 5
	CommandPlaceBlock  CommandKind = 6
)

// LookDirection 把玩家 look 角转换为单位方向；yaw=0、pitch=0 朝向 -Z。
func LookDirection(yaw, pitch float32) mgl32.Vec3 {
	cosPitch := float32(math.Cos(float64(pitch)))
	return mgl32.Vec3{
		-float32(math.Sin(float64(yaw))) * cosPitch,
		float32(math.Sin(float64(pitch))),
		-float32(math.Cos(float64(yaw))) * cosPitch,
	}
}

type RejectReason uint8

const (
	RejectInvalidRay RejectReason = iota
	RejectNoTarget
	RejectChunkNotReady
	RejectProtectedBlock
	RejectInvalidBlock
	RejectOccupied
	RejectInvalidInput   RejectReason = 6
	RejectPlayerNotReady RejectReason = 7
)

type Command struct {
	Session      SessionID
	Sequence     uint64
	Kind         CommandKind
	Dimension    core.DimensionID
	Center       core.ChunkPos
	Chunk        core.ChunkPos
	HaveRevision uint64
	Origin       mgl32.Vec3
	Direction    mgl32.Vec3
	Block        core.BlockID
	MoveX        int8
	MoveZ        int8
	Jump         bool
	Yaw          float32
	Pitch        float32
}

type GeneratedChunk struct {
	Dimension core.DimensionID
	Pos       core.ChunkPos
	Chunk     *world.Chunk
	Err       error
}

type BlockChange struct {
	Position core.BlockPos
	Block    core.BlockID
}

type ChunkChangeBatch struct {
	Dimension    core.DimensionID
	Chunk        core.ChunkPos
	BaseRevision uint64
	NewRevision  uint64
	Changes      []BlockChange
}

type Rejection struct {
	Session  SessionID
	Sequence uint64
	Reason   RejectReason
}

type ResyncRequest struct {
	Session      SessionID
	Sequence     uint64
	Dimension    core.DimensionID
	Chunk        core.ChunkPos
	HaveRevision uint64
}

type TickResult struct {
	Generate []core.ChunkKey
	Forget   map[SessionID][]core.ChunkKey
	Ready    []core.ChunkKey
	Changes  []ChunkChangeBatch
	Rejected []Rejection
	Resync   []ResyncRequest
	Players  []PlayerUpdate
	Tick     uint64
}
