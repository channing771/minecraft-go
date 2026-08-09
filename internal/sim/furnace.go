package sim

import (
	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

// advanceFurnaces 在单写者 tick 中推进活动范围内的熔炉。
// 它复用与掉落物相同的区块兴趣集合，按稳定的区块与槽位顺序处理，
// 因此多名玩家的重叠观察在同一 tick 内只推进一次。
func (engine *Engine) advanceFurnaces(pending map[core.ChunkKey]*pendingChunkChanges) {
	keys := engine.activeInterestKeys()
	burnTicks := engine.tunables.FurnaceBurnTicks
	smeltTicks := engine.tunables.FurnaceSmeltTicks
	for _, key := range keys {
		dimension := engine.dimensions[key.Dimension]
		if dimension == nil {
			continue
		}
		record, ok := dimension.records[key.Pos]
		if !ok || record.State != ChunkReady || record.Chunk == nil {
			continue
		}
		if advanceChunkFurnaces(record.Chunk, burnTicks, smeltTicks) {
			engine.touchChunk(key, pending)
		}
	}
}

// advanceChunkFurnaces 推进一个区块的全部熔炉槽，返回该区块是否发生变化。
// burnTicks、smeltTicks 由调用方传入本 tick 的快照值，本函数本身绝不读取
// ActiveTunables。
func advanceChunkFurnaces(chunk *world.Chunk, burnTicks uint16, smeltTicks uint8) bool {
	changed := false
	for slot := range core.FurnacesPerChunk {
		furnace := chunk.Furnace(slot)
		if !furnace.Active {
			continue
		}
		next, updated := advanceFurnace(furnace, burnTicks, smeltTicks)
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
// 因此燃料不会在空转中静默损失。burnTicks、smeltTicks 由调用方传入本 tick 的
// 快照值，本函数本身绝不读取 ActiveTunables。
func advanceFurnace(furnace world.FurnaceSlot, burnTicks uint16, smeltTicks uint8) (world.FurnaceSlot, bool) {
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
		furnace.BurnTicks = burnTicks
	}
	furnace.BurnTicks--
	furnace.ProgressTicks++
	if furnace.ProgressTicks < smeltTicks {
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
	burnTicks := engine.tunables.FurnaceBurnTicks
	smeltTicks := engine.tunables.FurnaceSmeltTicks
	for _, key := range engine.activeInterestKeys() {
		dimension := engine.dimensions[key.Dimension]
		if dimension == nil {
			continue
		}
		record, ok := dimension.records[key.Pos]
		if !ok || record.State != ChunkReady || record.Chunk == nil {
			continue
		}
		advanceChunkFurnaces(record.Chunk, burnTicks, smeltTicks)
	}
}

// ActiveInterestKeysForTest 暴露本 tick 的活动区块集合，仅供测试断言使用。
func (engine *Engine) ActiveInterestKeysForTest() []core.ChunkKey {
	return engine.activeInterestKeys()
}

// furnaceView 定位一个熔炉引用当前指向的槽；引用失效时返回 false。
// 区块未加载、槽位停用或 generation 不匹配都视为失效。
func (engine *Engine) furnaceView(ref core.FurnaceRef) (*world.Chunk, world.FurnaceSlot, bool) {
	chunk, ok := engine.containerChunk(ref)
	if !ok {
		return nil, world.FurnaceSlot{}, false
	}
	furnace := chunk.Furnace(int(ref.Slot))
	if !furnace.Active || furnace.Generation != ref.Generation {
		return nil, world.FurnaceSlot{}, false
	}
	return chunk, furnace, true
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

	nextSource, nextTarget, ok := mergeStacks(source, target)
	if !ok {
		return inventory, furnace, false
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

// SetPlayerInventoryForTest 改写某个会话的权威物品状态，仅供纵向测试构造固定场景。
func (engine *Engine) SetPlayerInventoryForTest(
	id SessionID,
	mutate func(core.Inventory) core.Inventory,
) {
	session := engine.sessions[id]
	if session == nil || session.player == nil {
		return
	}
	session.player.inventory = mutate(session.player.inventory)
	session.player.inventoryDirty = true
}
