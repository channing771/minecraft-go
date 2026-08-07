package sim

import (
	"errors"
	"slices"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/physics"
	"minecraft-go/internal/world"
)

// containerChunk 解析一个容器引用所在的区块；区块未加载或未 Ready 都视为失效。
// 熔炉与箱子共用同一份区块解析，只有槽位本身的读取按 Kind 分叉。
func (engine *Engine) containerChunk(ref core.ContainerRef) (*world.Chunk, bool) {
	dimension := engine.dimensions[ref.Dimension]
	if dimension == nil {
		return nil, false
	}
	record, ok := dimension.records[ref.Chunk]
	if !ok || record.State != ChunkReady || record.Chunk == nil {
		return nil, false
	}
	return record.Chunk, true
}

// chestView 定位一个箱子引用当前指向的槽；引用失效时返回 false。
// 失效判定与 furnaceView 完全一致：区块未加载、槽位停用或 generation 不匹配。
func (engine *Engine) chestView(ref core.ContainerRef) (*world.Chunk, world.ChestSlot, bool) {
	chunk, ok := engine.containerChunk(ref)
	if !ok {
		return nil, world.ChestSlot{}, false
	}
	chest := chunk.Chest(int(ref.Slot))
	if !chest.Active || chest.Generation != ref.Generation {
		return nil, world.ChestSlot{}, false
	}
	return chunk, chest, true
}

// withinContainerReach 报告玩家是否仍在某个容器的六格交互范围内；
// 熔炉与箱子共用同一份服务端权威距离判定。
func withinContainerReach(eye mgl32.Vec3, chunk core.ChunkPos, blockIndex uint32) bool {
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

// openContainer 处理打开请求：用权威射线在六格内命中活动熔炉或箱子才建立查看关系；
// 命中箱子会结束原有熔炉查看关系，反之亦然，因为每名玩家同时只能查看一个容器。
func (engine *Engine) openContainer(id SessionID, command Command) (RejectReason, bool) {
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
	var kind core.ContainerKind
	switch block {
	case core.FurnaceID:
		kind = core.ContainerKindFurnace
	case core.ChestID:
		kind = core.ContainerKindChest
	default:
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

	var ref core.ContainerRef
	switch kind {
	case core.ContainerKindFurnace:
		slot, found := record.Chunk.FurnaceAt(index)
		if !found {
			return RejectNoTarget, true
		}
		ref = core.ContainerRef{
			Dimension: session.dimension, Chunk: key.Pos, Kind: kind,
			Slot: uint8(slot), Generation: record.Chunk.Furnace(slot).Generation,
		}
	case core.ContainerKindChest:
		slot, found := record.Chunk.ChestAt(index)
		if !found {
			return RejectNoTarget, true
		}
		ref = core.ContainerRef{
			Dimension: session.dimension, Chunk: key.Pos, Kind: kind,
			Slot: uint8(slot), Generation: record.Chunk.Chest(slot).Generation,
		}
	}
	session.container = ref
	session.viewContainer = true
	return 0, false
}

// publishContainers 在全部命令与推进之后校验查看关系，
// 只向仍然有效的查看者发送完整状态，并对失效引用发送精确一次关闭。
// 熔炉与箱子共用同一份有效性判定，只有发布的状态形状按 Kind 分叉。
func (engine *Engine) publishContainers(result *TickResult) {
	sessions := engine.containerViewerScratch[:0]
	for id, session := range engine.sessions {
		if session.viewContainer {
			sessions = append(sessions, id)
		}
	}
	slices.Sort(sessions)
	engine.containerViewerScratch = sessions

	for _, id := range sessions {
		session := engine.sessions[id]
		ref := session.container
		if session.player == nil || session.player.lifecycle != PlayerActive ||
			session.dimension != ref.Dimension {
			session.viewContainer = false
			result.FurnaceEnds = append(result.FurnaceEnds, FurnaceEnd{Session: id, Furnace: ref})
			continue
		}
		eye := session.player.state.Position.Add(mgl32.Vec3{0, physics.EyeHeight, 0})
		switch ref.Kind {
		case core.ContainerKindChest:
			chunk, chest, ok := engine.chestView(ref)
			if !ok || !withinContainerReach(eye, chunk.Pos, chest.BlockIndex) {
				session.viewContainer = false
				result.FurnaceEnds = append(result.FurnaceEnds, FurnaceEnd{Session: id, Furnace: ref})
				continue
			}
			result.Chests = append(result.Chests, ChestUpdate{
				Session: id,
				Chest:   ref,
				Items:   chest.Items,
			})
		case core.ContainerKindFurnace:
			chunk, furnace, ok := engine.furnaceView(ref)
			if !ok || !withinContainerReach(eye, chunk.Pos, furnace.BlockIndex) {
				session.viewContainer = false
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
		default:
			// 未知容器类型：按失效处理，结束查看关系并发一次精确关闭。
			session.viewContainer = false
			result.FurnaceEnds = append(result.FurnaceEnds, FurnaceEnd{Session: id, Furnace: ref})
		}
	}
}

// mergeStacks 计算把 source 整堆或部分移入 target 后两侧的新值：目标为空时接收整堆，
// 同类目标按目标物品的堆叠上限合并并保留来源余量，异类目标交换；
// 与 core.Inventory.MoveStack 的语义保持一致，供熔炉与箱子的跨容器移动共用。
func mergeStacks(source, target core.ItemStack) (nextSource, nextTarget core.ItemStack, ok bool) {
	switch {
	case target.Item == core.ItemNone:
		return core.ItemStack{}, source, true
	case target.Item == source.Item:
		limit, hasLimit := core.ItemStackLimit(target.Item)
		if !hasLimit || target.Count >= limit {
			return core.ItemStack{}, core.ItemStack{}, false
		}
		moved := min(limit-target.Count, source.Count)
		nextTarget = target
		nextTarget.Count += moved
		if source.Count > moved {
			nextSource = core.ItemStack{Item: source.Item, Count: source.Count - moved}
		}
		return nextSource, nextTarget, true
	default:
		// 不同物品交换，交换后两侧约束都必须成立。
		return target, source, true
	}
}

// moveChestStack 在玩家物品与箱子的值副本上计算一次整堆移动，
// 只有两侧最终槽位都满足约束时才返回新值；任何一步失败都返回原值和 false。
// 箱子格接受任何已注册物品，因此这里只用 ItemStack.Valid 校验，不引入熔炉那样的物品类型约束。
func moveChestStack(
	inventory core.Inventory,
	chest world.ChestSlot,
	from, to uint8,
) (core.Inventory, world.ChestSlot, bool) {
	if from >= core.ChestViewSlots || to >= core.ChestViewSlots || from == to {
		return inventory, chest, false
	}
	if !inventory.Valid() || !chest.Valid() || !chest.Active {
		return inventory, chest, false
	}
	// 两侧都在玩家物品栏内时复用既有整堆移动语义。
	if from < core.InventorySlots && to < core.InventorySlots {
		next, ok := inventory.MoveStack(from, to)
		return next, chest, ok
	}

	source, ok := chestViewSlot(inventory, chest, from)
	if !ok || source.Item == core.ItemNone {
		return inventory, chest, false
	}
	target, ok := chestViewSlot(inventory, chest, to)
	if !ok {
		return inventory, chest, false
	}

	nextSource, nextTarget, ok := mergeStacks(source, target)
	if !ok {
		return inventory, chest, false
	}

	nextInventory, nextChest := inventory, chest
	if nextInventory, nextChest, ok = setChestViewSlot(
		nextInventory, nextChest, from, nextSource,
	); !ok {
		return inventory, chest, false
	}
	if nextInventory, nextChest, ok = setChestViewSlot(
		nextInventory, nextChest, to, nextTarget,
	); !ok {
		return inventory, chest, false
	}
	if !nextChest.Valid() || !nextInventory.Valid() {
		return inventory, chest, false
	}
	return nextInventory, nextChest, true
}

// chestViewSlot 读取统一栏位 0..62 中的一格：0..35 是物品栏，36..62 是箱子的 27 格。
func chestViewSlot(
	inventory core.Inventory,
	chest world.ChestSlot,
	slot uint8,
) (core.ItemStack, bool) {
	if slot >= core.ChestFirstSlot {
		return chest.Items[slot-core.ChestFirstSlot], true
	}
	return inventory.Slot(slot)
}

// setChestViewSlot 写入统一栏位 0..62 中的一格；箱子格没有物品类型约束，
// 只要求写入值本身是规范空值或已注册物品堆。
func setChestViewSlot(
	inventory core.Inventory,
	chest world.ChestSlot,
	slot uint8,
	stack core.ItemStack,
) (core.Inventory, world.ChestSlot, bool) {
	if slot >= core.ChestFirstSlot {
		if !stack.Valid() {
			return inventory, chest, false
		}
		chest.Items[slot-core.ChestFirstSlot] = stack
		return inventory, chest, true
	}
	next, ok := inventory.SetSlot(slot, stack)
	if !ok {
		return inventory, chest, false
	}
	return next, chest, true
}

// applyContainerMove 处理跨容器移动命令，成功时同时提交玩家物品与区块中的容器；
// 按引用的 Kind 分派到熔炉或箱子各自独立的边界与约束检查。
func (engine *Engine) applyContainerMove(
	id SessionID,
	command Command,
	pending map[core.ChunkKey]*pendingChunkChanges,
) (RejectReason, bool) {
	session := engine.sessions[id]
	if session == nil || session.player == nil || session.player.lifecycle != PlayerActive {
		return RejectPlayerNotReady, true
	}
	if !session.viewContainer || session.container != command.Furnace {
		return RejectInvalidInput, true
	}
	ref := command.Furnace
	key := core.ChunkKey{Dimension: ref.Dimension, Pos: ref.Chunk}

	switch ref.Kind {
	case core.ContainerKindChest:
		chunk, chest, ok := engine.chestView(ref)
		if !ok {
			return RejectInvalidInput, true
		}
		nextInventory, nextChest, ok := moveChestStack(
			session.player.inventory, chest, command.Slot, command.ToSlot,
		)
		if !ok {
			return RejectInvalidInput, true
		}
		chunk.SetChest(int(ref.Slot), nextChest)
		engine.touchChunk(key, pending)
		session.player.inventory = nextInventory
		session.player.inventoryDirty = true
		return 0, false
	case core.ContainerKindFurnace:
		chunk, furnace, ok := engine.furnaceView(ref)
		if !ok {
			return RejectInvalidInput, true
		}
		nextInventory, nextFurnace, ok := moveFurnaceStack(
			session.player.inventory, furnace, command.Slot, command.ToSlot,
		)
		if !ok {
			return RejectInvalidInput, true
		}
		chunk.SetFurnace(int(ref.Slot), nextFurnace)
		engine.touchChunk(key, pending)
		session.player.inventory = nextInventory
		session.player.inventoryDirty = true
		return 0, false
	default:
		return RejectInvalidInput, true
	}
}

// SetChunkChestForTest 直接写入一个已 Ready 区块的箱子槽，仅供测试构造固定场景。
func (engine *Engine) SetChunkChestForTest(
	key core.ChunkKey,
	slot int,
	value world.ChestSlot,
) {
	dimension := engine.dimensions[key.Dimension]
	if dimension == nil {
		return
	}
	if record, ok := dimension.records[key.Pos]; ok && record.Chunk != nil {
		record.Chunk.SetChest(slot, value)
	}
}
