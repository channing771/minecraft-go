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
	CommandTrustedObserverCenter CommandKind = iota
	CommandPlayerInput
	CommandBreakBlock
	CommandPlaceBlock
	CommandResync
	CommandSelectHotbar
	CommandMoveInventoryStack
	CommandCraftRecipe
	CommandOpenFurnace
	CommandCloseFurnace
	CommandMoveFurnaceStack
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
	RejectInvalidSlot    RejectReason = 8
	RejectHotbarFull     RejectReason = 9
	RejectDropCapacity   RejectReason = 10
	// RejectFurnaceCapacity 表示区块的 32 个熔炉槽已经用尽。
	RejectFurnaceCapacity RejectReason = 11
)

type Command struct {
	Session      SessionID
	Sequence     uint64
	Kind         CommandKind
	Dimension    core.DimensionID
	Center       core.ChunkPos
	Chunk        core.ChunkPos
	HaveRevision uint64
	Slot         uint8
	ToSlot       uint8
	Recipe       core.RecipeID
	Furnace      core.FurnaceRef
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

type AcquiredChunk struct {
	Key               core.ChunkKey
	Chunk             *world.Chunk
	Revision          uint64
	PersistedRevision uint64
	NeedsRewrite      bool
	Recovered         bool
	Missing           bool
	Err               error
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
	Acquire     []core.ChunkKey
	Generate    []core.ChunkKey
	Forget      map[SessionID][]core.ChunkKey
	Ready       []core.ChunkKey
	Changes     []ChunkChangeBatch
	Rejected    []Rejection
	Resync      []ResyncRequest
	Players     []PlayerUpdate
	Inventories []InventoryUpdate
	Furnaces    []FurnaceUpdate
	FurnaceEnds []FurnaceEnd
	Tick        uint64
}

// FurnaceUpdate 是本 tick 发给某个查看者的完整权威熔炉状态。
type FurnaceUpdate struct {
	Session       SessionID
	Furnace       core.FurnaceRef
	Input         core.ItemStack
	Fuel          core.ItemStack
	Output        core.ItemStack
	ProgressTicks uint8
	BurnTicks     uint16
}

// FurnaceEnd 通知某个查看者其熔炉引用已经失效。
type FurnaceEnd struct {
	Session SessionID
	Furnace core.FurnaceRef
}
