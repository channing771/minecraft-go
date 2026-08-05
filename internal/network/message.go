// Package network 定义端无关消息协议与传输接口。
package network

import (
	"bytes"
	"errors"
	"fmt"
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
	Mining   bool
}

func (PlayerInput) clientMessage() {}
func (PlayerInput) clientPacket()  {}

func (input PlayerInput) Validate() error {
	if !finite32(input.Yaw) || !finite32(input.Pitch) {
		return errors.New("network: player input has non-finite rotation")
	}
	return nil
}

type PlaceBlock struct {
	Sequence   uint64
	Yaw, Pitch float32
	Slot       uint8
}

func (PlaceBlock) clientMessage() {}
func (PlaceBlock) clientPacket()  {}

func (command PlaceBlock) Validate() error {
	if !finite32(command.Yaw) || !finite32(command.Pitch) {
		return errors.New("network: place block has non-finite rotation")
	}
	if command.Slot >= core.HotbarSlots {
		return errors.New("network: place block hotbar slot is outside 0..8")
	}
	return nil
}

// SelectHotbar 请求把权威选中栏位切换到 Slot。
type SelectHotbar struct {
	Sequence uint64
	Slot     uint8
}

func (SelectHotbar) clientMessage() {}
func (SelectHotbar) clientPacket()  {}

func (command SelectHotbar) Validate() error {
	if command.Slot >= core.HotbarSlots {
		return errors.New("network: hotbar selection slot is outside 0..8")
	}
	return nil
}

// CraftRecipe 按稳定配方 ID 请求一次合成；原料与产物完全由服务端决定。
type CraftRecipe struct {
	Sequence uint64
	Recipe   core.RecipeID
}

func (CraftRecipe) clientMessage() {}
func (CraftRecipe) clientPacket()  {}

func (command CraftRecipe) Validate() error {
	if _, ok := core.Recipe(command.Recipe); !ok {
		return errors.New("network: unknown crafting recipe")
	}
	return nil
}

// OpenFurnace 请求打开视线内的熔炉；服务端用权威射线验证距离与目标方块。
type OpenFurnace struct {
	Sequence   uint64
	Yaw, Pitch float32
}

func (OpenFurnace) clientMessage() {}
func (OpenFurnace) clientPacket()  {}

func (command OpenFurnace) Validate() error {
	if !finite32(command.Yaw) || !finite32(command.Pitch) {
		return errors.New("network: open furnace has non-finite rotation")
	}
	return nil
}

// MoveFurnaceStack 请求在统一栏位 0..38 之间整堆移动；输出格只能作为来源。
type MoveFurnaceStack struct {
	Sequence uint64
	Furnace  core.FurnaceRef
	From, To uint8
}

func (MoveFurnaceStack) clientMessage() {}
func (MoveFurnaceStack) clientPacket()  {}

func (command MoveFurnaceStack) Validate() error {
	if err := validFurnaceRef(command.Furnace); err != nil {
		return err
	}
	if command.From >= core.FurnaceViewSlots || command.To >= core.FurnaceViewSlots {
		return errors.New("network: furnace move slot is outside 0..38")
	}
	if command.From == command.To {
		return errors.New("network: furnace move source equals target")
	}
	if command.To == core.FurnaceOutputSlot {
		return errors.New("network: furnace output slot cannot be a move target")
	}
	return nil
}

// CloseFurnace 结束当前会话的查看关系。
type CloseFurnace struct {
	Sequence uint64
}

func (CloseFurnace) clientMessage() {}
func (CloseFurnace) clientPacket()  {}

func (CloseFurnace) Validate() error { return nil }

// FurnaceState 是服务端发给当前查看者的完整熔炉状态。
type FurnaceState struct {
	Furnace       core.FurnaceRef
	Input         core.ItemStack
	Fuel          core.ItemStack
	Output        core.ItemStack
	ProgressTicks uint8
	BurnTicks     uint16
}

func (FurnaceState) serverMessage() {}
func (FurnaceState) serverPacket()  {}

func (state FurnaceState) Validate() error {
	if err := validFurnaceRef(state.Furnace); err != nil {
		return err
	}
	if state.ProgressTicks >= core.FurnaceSmeltTicks || state.BurnTicks > core.FurnaceBurnTicks {
		return errors.New("network: furnace timers are outside their fixed ranges")
	}
	if !validFurnaceStack(state.Input, core.ItemRawIron) ||
		!validFurnaceStack(state.Fuel, core.ItemCoal) ||
		!validFurnaceStack(state.Output, core.ItemIronIngot) {
		return errors.New("network: furnace slot holds an item it cannot contain")
	}
	return nil
}

// FurnaceClosed 通知客户端当前查看的熔炉已经失效。
type FurnaceClosed struct {
	Furnace core.FurnaceRef
}

func (FurnaceClosed) serverMessage() {}
func (FurnaceClosed) serverPacket()  {}

func (closed FurnaceClosed) Validate() error {
	return validFurnaceRef(closed.Furnace)
}

// validFurnaceRef 检查熔炉引用的固定字段范围。
func validFurnaceRef(ref core.FurnaceRef) error {
	if ref.Dimension != core.Overworld {
		return errors.New("network: furnace dimension is not overworld")
	}
	if ref.Slot >= core.FurnacesPerChunk {
		return errors.New("network: furnace slot is outside 0..31")
	}
	if ref.Generation == 0 {
		return errors.New("network: furnace generation is zero")
	}
	return nil
}

// validFurnaceStack 报告某个熔炉格是否为空或恰好装着允许的物品。
func validFurnaceStack(stack core.ItemStack, allowed core.ItemID) bool {
	if stack.Item == core.ItemNone {
		return stack.Count == 0
	}
	return stack.Item == allowed && stack.Count >= 1 && stack.Count <= core.MaxStackCount
}

// InventoryState 是服务端发给所属玩家的完整权威物品状态。
type InventoryState struct {
	Inventory core.Inventory
}

func (InventoryState) serverMessage() {}
func (InventoryState) serverPacket()  {}

func (state InventoryState) Validate() error {
	if !state.Inventory.Valid() {
		return errors.New("network: inventory state is not a valid fixed inventory")
	}
	return nil
}

// MoveInventoryStack 请求在 36 格之间整堆移动。
type MoveInventoryStack struct {
	Sequence uint64
	From, To uint8
}

func (MoveInventoryStack) clientMessage() {}
func (MoveInventoryStack) clientPacket()  {}

func (command MoveInventoryStack) Validate() error {
	if command.From >= core.InventorySlots || command.To >= core.InventorySlots {
		return errors.New("network: inventory move slot is outside 0..35")
	}
	if command.From == command.To {
		return errors.New("network: inventory move source equals target")
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
	ServerTick          uint64
	LastInputSequence   uint64
	Dimension           core.DimensionID
	Position            mgl32.Vec3
	Velocity            mgl32.Vec3
	Yaw, Pitch          float32
	OnGround            bool
	Ready               bool
	Reset               bool
	MiningActive        bool
	MiningTarget        core.BlockPos
	MiningProgressTicks uint16
	MiningRequiredTicks uint16
	MiningHarvestable   bool
}

type RemotePlayerSpawn struct {
	PlayerID    core.PlayerID
	DisplayName string
	ServerTick  uint64
	Dimension   core.DimensionID
	Position    mgl32.Vec3
	Yaw, Pitch  float32
}

func (RemotePlayerSpawn) serverMessage() {}
func (RemotePlayerSpawn) serverPacket()  {}

func (spawn RemotePlayerSpawn) Validate() error {
	name, err := core.NormalizeDisplayName(spawn.DisplayName)
	if err != nil || name != spawn.DisplayName || !spawn.PlayerID.Valid() ||
		spawn.Dimension != core.Overworld || !finiteVec3(spawn.Position) ||
		!finite32(spawn.Yaw) || !finite32(spawn.Pitch) {
		return errors.New("network: invalid remote player spawn")
	}
	return nil
}

type RemotePlayerDespawn struct{ PlayerID core.PlayerID }

func (RemotePlayerDespawn) serverMessage() {}
func (RemotePlayerDespawn) serverPacket()  {}

func (despawn RemotePlayerDespawn) Validate() error {
	if !despawn.PlayerID.Valid() {
		return errors.New("network: invalid remote player despawn")
	}
	return nil
}

type RemotePlayerStates struct {
	ServerTick uint64
	Players    []RemotePlayerState
}

type RemotePlayerState struct {
	PlayerID   core.PlayerID
	Dimension  core.DimensionID
	Position   mgl32.Vec3
	Yaw, Pitch float32
	Reset      bool
}

func (RemotePlayerStates) serverMessage() {}
func (RemotePlayerStates) serverPacket()  {}

func (states RemotePlayerStates) Validate() error {
	if len(states.Players) < 1 || len(states.Players) > 7 {
		return errors.New("network: remote player state count is outside 1..7")
	}
	for index, state := range states.Players {
		if err := state.validate(); err != nil {
			return fmt.Errorf("network: remote player state %d: %w", index, err)
		}
		if index > 0 && bytes.Compare(states.Players[index-1].PlayerID[:], state.PlayerID[:]) >= 0 {
			return errors.New("network: remote player states are not strictly sorted")
		}
	}
	return nil
}

func (state RemotePlayerState) validate() error {
	if !state.PlayerID.Valid() || state.Dimension != core.Overworld || !finiteVec3(state.Position) ||
		!finite32(state.Yaw) || !finite32(state.Pitch) {
		return errors.New("invalid remote player state")
	}
	return nil
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
	if !state.MiningActive {
		if state.MiningTarget != (core.BlockPos{}) || state.MiningProgressTicks != 0 ||
			state.MiningRequiredTicks != 0 || state.MiningHarvestable {
			return errors.New("network: inactive player state has mining fields")
		}
		return nil
	}
	if state.MiningProgressTicks == 0 || state.MiningProgressTicks >= state.MiningRequiredTicks {
		return errors.New("network: active player state has invalid mining progress")
	}
	return nil
}

type RejectReason string

const (
	RejectInvalidRay      RejectReason = "invalid_ray"
	RejectNoTarget        RejectReason = "no_target"
	RejectChunkNotReady   RejectReason = "chunk_not_ready"
	RejectProtectedBlock  RejectReason = "protected_block"
	RejectInvalidBlock    RejectReason = "invalid_block"
	RejectOccupied        RejectReason = "occupied"
	RejectInvalidInput    RejectReason = "invalid_input"
	RejectPlayerNotReady  RejectReason = "player_not_ready"
	RejectInvalidSlot     RejectReason = "invalid_slot"
	RejectHotbarFull      RejectReason = "hotbar_full"
	RejectDropCapacity    RejectReason = "drop_capacity"
	RejectFurnaceCapacity RejectReason = "furnace_capacity"
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

// MaxItemDropBatch 是单条掉落物消息可携带的固定上限。
const MaxItemDropBatch = 32

// maxChunkBlockIndex 是区块内方块索引的排他上限。
const maxChunkBlockIndex = core.SectionsPerChunk * core.BlocksPerSection

// ItemDrop 是一个权威掉落物堆的完整线上值。
type ItemDrop struct {
	ID         core.DropID
	BlockIndex uint32
	Item       core.ItemID
	Count      uint8
}

func (drop ItemDrop) validate() error {
	if !drop.ID.Valid() {
		return errors.New("network: invalid item drop ID")
	}
	if drop.BlockIndex >= maxChunkBlockIndex {
		return errors.New("network: item drop block index is outside the chunk")
	}
	if _, ok := core.ItemPlacement(drop.Item); !ok {
		return errors.New("network: unknown item drop item")
	}
	if drop.Count < 1 || drop.Count > core.MaxStackCount {
		return errors.New("network: item drop count is outside 1..64")
	}
	return nil
}

// ItemDropUpserts 按稳定 ID 顺序新增或整体替换掉落物。
type ItemDropUpserts struct {
	ServerTick uint64
	Drops      []ItemDrop
}

func (ItemDropUpserts) serverMessage() {}
func (ItemDropUpserts) serverPacket()  {}

func (upserts ItemDropUpserts) Validate() error {
	if err := validItemDropBatchLength(len(upserts.Drops)); err != nil {
		return err
	}
	for index, drop := range upserts.Drops {
		if err := drop.validate(); err != nil {
			return fmt.Errorf("network: item drop %d: %w", index, err)
		}
		if index > 0 && upserts.Drops[index-1].ID.Compare(drop.ID) >= 0 {
			return errors.New("network: item drop upserts are not strictly sorted")
		}
	}
	return nil
}

// ItemDropRemoves 按稳定 ID 顺序移除掉落物。
type ItemDropRemoves struct {
	ServerTick uint64
	IDs        []core.DropID
}

func (ItemDropRemoves) serverMessage() {}
func (ItemDropRemoves) serverPacket()  {}

func (removes ItemDropRemoves) Validate() error {
	if err := validItemDropBatchLength(len(removes.IDs)); err != nil {
		return err
	}
	for index, id := range removes.IDs {
		if !id.Valid() {
			return fmt.Errorf("network: item drop remove %d: invalid ID", index)
		}
		if index > 0 && removes.IDs[index-1].Compare(id) >= 0 {
			return errors.New("network: item drop removes are not strictly sorted")
		}
	}
	return nil
}

func validItemDropBatchLength(count int) error {
	if count < 1 || count > MaxItemDropBatch {
		return errors.New("network: item drop batch count is outside 1..32")
	}
	return nil
}

func finite32(value float32) bool {
	return !math.IsNaN(float64(value)) && !math.IsInf(float64(value), 0)
}

func finiteVec3(value mgl32.Vec3) bool {
	for _, component := range value {
		if !finite32(component) {
			return false
		}
	}
	return true
}
