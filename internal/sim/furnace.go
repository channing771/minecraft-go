package sim

import (
	"errors"
	"slices"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/physics"
	"minecraft-go/internal/world"
)

// advanceFurnaces 在单写者 tick 中推进活动范围内的熔炉。
// 它复用与掉落物相同的区块兴趣集合，按稳定的区块与槽位顺序处理，
// 因此多名玩家的重叠观察在同一 tick 内只推进一次。
func (engine *Engine) advanceFurnaces(pending map[core.ChunkKey]*pendingChunkChanges) {
	keys := engine.activeInterestKeys()
	for _, key := range keys {
		dimension := engine.dimensions[key.Dimension]
		if dimension == nil {
			continue
		}
		record, ok := dimension.records[key.Pos]
		if !ok || record.State != ChunkReady || record.Chunk == nil {
			continue
		}
		if advanceChunkFurnaces(record.Chunk) {
			engine.touchChunk(key, pending)
		}
	}
}

// advanceChunkFurnaces 推进一个区块的全部熔炉槽，返回该区块是否发生变化。
func advanceChunkFurnaces(chunk *world.Chunk) bool {
	changed := false
	for slot := range core.FurnacesPerChunk {
		furnace := chunk.Furnace(slot)
		if !furnace.Active {
			continue
		}
		next, updated := advanceFurnace(furnace)
		if !updated {
			continue
		}
		chunk.SetFurnace(slot, next)
		changed = true
	}
	return changed
}

// advanceFurnace 推进一个熔炉一个 tick，返回新值与是否发生变化。
// 输入无效或输出无容量时状态完全暂停：进度与剩余燃烧 tick 都不减少，
// 因此燃料不会在空转中静默损失。
func advanceFurnace(furnace world.FurnaceSlot) (world.FurnaceSlot, bool) {
	if !canSmelt(furnace) {
		return furnace, false
	}
	if furnace.BurnTicks == 0 {
		if furnace.Fuel.Item != core.ItemCoal || furnace.Fuel.Count == 0 {
			return furnace, false
		}
		furnace.Fuel.Count--
		if furnace.Fuel.Count == 0 {
			furnace.Fuel = core.ItemStack{}
		}
		furnace.BurnTicks = core.FurnaceBurnTicks
	}
	furnace.BurnTicks--
	furnace.ProgressTicks++
	if furnace.ProgressTicks < core.FurnaceSmeltTicks {
		return furnace, true
	}
	furnace.ProgressTicks = 0
	furnace.Input.Count--
	if furnace.Input.Count == 0 {
		furnace.Input = core.ItemStack{}
	}
	if furnace.Output.Item == core.ItemNone {
		furnace.Output = core.ItemStack{Item: core.ItemIronIngot, Count: 1}
	} else {
		furnace.Output.Count++
	}
	return furnace, true
}

// canSmelt 报告熔炉当前是否具备继续熔炼的输入与输出条件。
func canSmelt(furnace world.FurnaceSlot) bool {
	if furnace.Input.Item != core.ItemRawIron || furnace.Input.Count == 0 {
		return false
	}
	return furnace.Output.Item == core.ItemNone ||
		(furnace.Output.Item == core.ItemIronIngot && furnace.Output.Count < core.MaxStackCount)
}

// SetChunkFurnaceForTest 直接写入一个已 Ready 区块的熔炉槽，仅供测试构造固定场景。
func (engine *Engine) SetChunkFurnaceForTest(
	key core.ChunkKey,
	slot int,
	value world.FurnaceSlot,
) {
	dimension := engine.dimensions[key.Dimension]
	if dimension == nil {
		return
	}
	if record, ok := dimension.records[key.Pos]; ok && record.Chunk != nil {
		record.Chunk.SetFurnace(slot, value)
	}
}

// AdvanceFurnacesForBenchmark 只在活动区块上推进熔炉本身，
// 不做 revision 记账，供固定工作量基准与热路径分配门禁使用。
func (engine *Engine) AdvanceFurnacesForBenchmark() {
	for _, key := range engine.activeInterestKeys() {
		dimension := engine.dimensions[key.Dimension]
		if dimension == nil {
			continue
		}
		record, ok := dimension.records[key.Pos]
		if !ok || record.State != ChunkReady || record.Chunk == nil {
			continue
		}
		advanceChunkFurnaces(record.Chunk)
	}
}

// ActiveInterestKeysForTest 暴露本 tick 的活动区块集合，仅供测试断言使用。
func (engine *Engine) ActiveInterestKeysForTest() []core.ChunkKey {
	return engine.activeInterestKeys()
}

// furnaceView 定位一个熔炉引用当前指向的槽；引用失效时返回 false。
// 区块未加载、槽位停用或 generation 不匹配都视为失效。
func (engine *Engine) furnaceView(ref core.FurnaceRef) (*world.Chunk, world.FurnaceSlot, bool) {
	dimension := engine.dimensions[ref.Dimension]
	if dimension == nil {
		return nil, world.FurnaceSlot{}, false
	}
	record, ok := dimension.records[ref.Chunk]
	if !ok || record.State != ChunkReady || record.Chunk == nil {
		return nil, world.FurnaceSlot{}, false
	}
	furnace := record.Chunk.Furnace(int(ref.Slot))
	if !furnace.Active || furnace.Generation != ref.Generation {
		return nil, world.FurnaceSlot{}, false
	}
	return record.Chunk, furnace, true
}

// openFurnace 处理打开请求：用权威射线在六格内命中活动熔炉才建立查看关系。
func (engine *Engine) openFurnace(id SessionID, command Command) (RejectReason, bool) {
	session := engine.sessions[id]
	if session == nil || session.player == nil || session.player.lifecycle != PlayerActive {
		return RejectPlayerNotReady, true
	}
	dimension := engine.dimensions[session.dimension]
	if dimension == nil {
		return RejectChunkNotReady, true
	}
	origin := session.player.state.Position.Add(mgl32.Vec3{0, physics.EyeHeight, 0})
	direction := LookDirection(command.Yaw, command.Pitch)
	hit, ok, err := core.RaycastBlocks(
		origin,
		direction,
		interactionReach,
		func(position core.BlockPos) (bool, error) {
			block, ready := dimension.BlockAt(position)
			if !ready {
				return false, ErrChunkNotReady
			}
			return block != core.AirID, nil
		},
	)
	if err != nil {
		if errors.Is(err, ErrChunkNotReady) {
			return RejectChunkNotReady, true
		}
		return RejectInvalidRay, true
	}
	if !ok {
		return RejectNoTarget, true
	}
	block, ready := dimension.BlockAt(hit.Block)
	if !ready {
		return RejectChunkNotReady, true
	}
	if block != core.FurnaceID {
		return RejectNoTarget, true
	}
	key := core.ChunkKey{Dimension: session.dimension, Pos: hit.Block.Chunk()}
	record, exists := dimension.records[key.Pos]
	if !exists || record.State != ChunkReady || record.Chunk == nil {
		return RejectChunkNotReady, true
	}
	index, indexed := world.ChunkBlockIndex(hit.Block)
	if !indexed {
		return RejectNoTarget, true
	}
	slot, found := record.Chunk.FurnaceAt(index)
	if !found {
		return RejectNoTarget, true
	}
	session.furnace = core.FurnaceRef{
		Dimension:  session.dimension,
		Chunk:      key.Pos,
		Slot:       uint8(slot),
		Generation: record.Chunk.Furnace(slot).Generation,
	}
	session.viewFurnace = true
	return 0, false
}

// publishFurnaces 在全部命令与推进之后校验查看关系，
// 只向仍然有效的查看者发送完整状态，并对失效引用发送精确一次关闭。
func (engine *Engine) publishFurnaces(result *TickResult) {
	sessions := engine.furnaceViewerScratch[:0]
	for id, session := range engine.sessions {
		if session.viewFurnace {
			sessions = append(sessions, id)
		}
	}
	slices.Sort(sessions)
	engine.furnaceViewerScratch = sessions

	for _, id := range sessions {
		session := engine.sessions[id]
		ref := session.furnace
		if session.player == nil || session.player.lifecycle != PlayerActive ||
			session.dimension != ref.Dimension {
			session.viewFurnace = false
			result.FurnaceEnds = append(result.FurnaceEnds, FurnaceEnd{Session: id, Furnace: ref})
			continue
		}
		chunk, furnace, ok := engine.furnaceView(ref)
		if !ok || !withinFurnaceReach(
			session.player.state.Position.Add(mgl32.Vec3{0, physics.EyeHeight, 0}),
			chunk.Pos, furnace.BlockIndex,
		) {
			session.viewFurnace = false
			result.FurnaceEnds = append(result.FurnaceEnds, FurnaceEnd{Session: id, Furnace: ref})
			continue
		}
		result.Furnaces = append(result.Furnaces, FurnaceUpdate{
			Session:       id,
			Furnace:       ref,
			Input:         furnace.Input,
			Fuel:          furnace.Fuel,
			Output:        furnace.Output,
			ProgressTicks: furnace.ProgressTicks,
			BurnTicks:     furnace.BurnTicks,
		})
	}
}

// withinFurnaceReach 报告玩家是否仍在熔炉的六格交互范围内。
func withinFurnaceReach(eye mgl32.Vec3, chunk core.ChunkPos, blockIndex uint32) bool {
	position, ok := world.BlockPosFromChunkIndex(chunk, blockIndex)
	if !ok {
		return false
	}
	center := mgl32.Vec3{
		float32(position.X) + 0.5,
		float32(position.Y) + 0.5,
		float32(position.Z) + 0.5,
	}
	return center.Sub(eye).Len() <= interactionReach
}

// moveFurnaceStack 在玩家物品与熔炉的值副本上计算一次整堆移动，
// 只有两侧最终槽位都满足约束时才返回新值；任何一步失败都返回原值和 false。
func moveFurnaceStack(
	inventory core.Inventory,
	furnace world.FurnaceSlot,
	from, to uint8,
) (core.Inventory, world.FurnaceSlot, bool) {
	if from >= core.FurnaceViewSlots || to >= core.FurnaceViewSlots || from == to {
		return inventory, furnace, false
	}
	if to == core.FurnaceOutputSlot {
		return inventory, furnace, false
	}
	if !inventory.Valid() || !furnace.Valid() || !furnace.Active {
		return inventory, furnace, false
	}
	// 两侧都在玩家物品栏内时复用既有整堆移动语义。
	if from < core.InventorySlots && to < core.InventorySlots {
		next, ok := inventory.MoveStack(from, to)
		return next, furnace, ok
	}

	source, ok := furnaceViewSlot(inventory, furnace, from)
	if !ok || source.Item == core.ItemNone {
		return inventory, furnace, false
	}
	target, ok := furnaceViewSlot(inventory, furnace, to)
	if !ok {
		return inventory, furnace, false
	}

	var nextSource, nextTarget core.ItemStack
	switch {
	case target.Item == core.ItemNone:
		nextTarget = source
	case target.Item == source.Item:
		space := core.MaxStackCount - target.Count
		if space == 0 {
			return inventory, furnace, false
		}
		moved := min(space, source.Count)
		nextTarget = core.ItemStack{Item: target.Item, Count: target.Count + moved}
		if source.Count > moved {
			nextSource = core.ItemStack{Item: source.Item, Count: source.Count - moved}
		}
	default:
		// 不同物品交换，交换后两侧约束都必须成立。
		nextTarget = source
		nextSource = target
	}

	nextInventory, nextFurnace := inventory, furnace
	if nextInventory, nextFurnace, ok = setFurnaceViewSlot(
		nextInventory, nextFurnace, from, nextSource,
	); !ok {
		return inventory, furnace, false
	}
	if nextInventory, nextFurnace, ok = setFurnaceViewSlot(
		nextInventory, nextFurnace, to, nextTarget,
	); !ok {
		return inventory, furnace, false
	}
	if !nextFurnace.Valid() || !nextInventory.Valid() {
		return inventory, furnace, false
	}
	return nextInventory, nextFurnace, true
}

// furnaceViewSlot 读取统一栏位 0..38 中的一格。
func furnaceViewSlot(
	inventory core.Inventory,
	furnace world.FurnaceSlot,
	slot uint8,
) (core.ItemStack, bool) {
	switch slot {
	case core.FurnaceInputSlot:
		return furnace.Input, true
	case core.FurnaceFuelSlot:
		return furnace.Fuel, true
	case core.FurnaceOutputSlot:
		return furnace.Output, true
	default:
		return inventory.Slot(slot)
	}
}

// setFurnaceViewSlot 写入统一栏位 0..38 中的一格，并保持熔炉格的物品约束。
func setFurnaceViewSlot(
	inventory core.Inventory,
	furnace world.FurnaceSlot,
	slot uint8,
	stack core.ItemStack,
) (core.Inventory, world.FurnaceSlot, bool) {
	switch slot {
	case core.FurnaceInputSlot:
		if !allowedFurnaceStack(stack, core.ItemRawIron) {
			return inventory, furnace, false
		}
		furnace.Input = stack
	case core.FurnaceFuelSlot:
		if !allowedFurnaceStack(stack, core.ItemCoal) {
			return inventory, furnace, false
		}
		furnace.Fuel = stack
	case core.FurnaceOutputSlot:
		if !allowedFurnaceStack(stack, core.ItemIronIngot) {
			return inventory, furnace, false
		}
		furnace.Output = stack
	default:
		next, ok := inventory.SetSlot(slot, stack)
		if !ok {
			return inventory, furnace, false
		}
		inventory = next
	}
	return inventory, furnace, true
}

// allowedFurnaceStack 报告某个堆是否可以放进只接受特定物品的熔炉格。
func allowedFurnaceStack(stack core.ItemStack, allowed core.ItemID) bool {
	if stack.Item == core.ItemNone {
		return stack.Count == 0
	}
	return stack.Item == allowed && stack.Count >= 1 && stack.Count <= core.MaxStackCount
}

// applyFurnaceMove 处理跨容器移动命令，成功时同时提交玩家物品与区块熔炉。
func (engine *Engine) applyFurnaceMove(
	id SessionID,
	command Command,
	pending map[core.ChunkKey]*pendingChunkChanges,
) (RejectReason, bool) {
	session := engine.sessions[id]
	if session == nil || session.player == nil || session.player.lifecycle != PlayerActive {
		return RejectPlayerNotReady, true
	}
	if !session.viewFurnace || session.furnace != command.Furnace {
		return RejectInvalidInput, true
	}
	chunk, furnace, ok := engine.furnaceView(command.Furnace)
	if !ok {
		return RejectInvalidInput, true
	}
	nextInventory, nextFurnace, ok := moveFurnaceStack(
		session.player.inventory, furnace, command.Slot, command.ToSlot,
	)
	if !ok {
		return RejectInvalidInput, true
	}
	chunk.SetFurnace(int(command.Furnace.Slot), nextFurnace)
	engine.touchChunk(core.ChunkKey{
		Dimension: command.Furnace.Dimension,
		Pos:       command.Furnace.Chunk,
	}, pending)
	session.player.inventory = nextInventory
	session.player.inventoryDirty = true
	return 0, false
}
