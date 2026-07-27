package sim

import (
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
)

type RejectReason uint8

const (
	RejectInvalidRay RejectReason = iota
	RejectNoTarget
	RejectChunkNotReady
	RejectProtectedBlock
	RejectInvalidBlock
	RejectOccupied
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
	Tick     uint64
}
