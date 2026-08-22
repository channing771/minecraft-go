package sim

import (
	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/world"
)

// companionPlaceIntent 是 Place action 在伙伴 action 阶段记录、同一权威 tick
// 稍后结算的单次放置意图。放置没有进度语义（区别于采掘的按住意图），因此意图
// 不落入 companionState 持久字段，只随 tick 存活：action 阶段收集、写入区结算，
// 结算完即随切片丢弃，绝不跨 tick 滞留。
type companionPlaceIntent struct {
	id     companion.ID
	target core.BlockPos
	block  core.BlockID
}

// consumeFirstInventoryItem 在 36 格完整背包的副本上按统一索引（快捷栏 0..8
// 在前、背包 9..35 在后）找到首个对应物品堆并扣除一件，数量归零时清空栏位——
// 扣一件与清空语义对齐既有 core.Hotbar.Consume；core.Inventory 尚无等价方法，
// 这里的单一 helper 是它的 36 格推广。找不到对应物品时返回原值和 false，调用方
// 据此以 action 语义拒绝放置，背包零副作用。
func consumeFirstInventoryItem(
	inventory core.Inventory,
	item core.ItemID,
) (core.Inventory, bool) {
	for slot := uint8(0); slot < core.InventorySlots; slot++ {
		stack, _ := inventory.Slot(slot)
		if stack.Item != item || stack.Count == 0 {
			continue
		}
		stack.Count--
		if stack.Count == 0 {
			stack = core.ItemStack{}
		}
		next, ok := inventory.SetSlot(slot, stack)
		if !ok {
			// 理论不可达：合法堆扣一件仍合法、清空恒合法；防御返回原值。
			return inventory, false
		}
		return next, true
	}
	return inventory, false
}

// companionPlaceableBlock 是伙伴放置目标的防御清单：返回放置该方块所需消耗的
// 物品。可放置注册表是物品→方块方向（core.ItemPlacement）的像；action 携带的
// 已是方块形态，经 core.BlockDrop 反查物品后必须能由 ItemPlacement 还原成同一
// 方块。矿石等已注册但没有物品能放置成的方块在这里被拒绝。
//
// 农业方块必须在往返校验之外**显式**拒绝（Ruling 8）：BlockDrop(WheatStage0ID)
// = 种子、ItemPlacement(种子) = WheatStage0ID，往返对种子是成立的，二重校验
// 本身挡不住伙伴种地。伙伴的农业语义尚未裁决（design.md 遗留 11），与采掘侧的
// companionMineableBlock 取同一立场：十个农业编号一律拒绝。
func companionPlaceableBlock(blockID core.BlockID) (core.ItemID, bool) {
	if core.IsCrop(blockID) || core.IsFarmland(blockID) {
		return core.ItemNone, false
	}
	item, ok := core.BlockDrop(blockID)
	if !ok {
		return core.ItemNone, false
	}
	if placement, ok := core.ItemPlacement(item); !ok || placement != blockID {
		return core.ItemNone, false
	}
	return item, true
}

// settleCompanionPlacements 结算本 tick 收集的放置意图。它必须位于 Step 的
// reconcileSubscriptions 之后（阶段顺序契约：所有区块写者都在订阅收敛之后——
// 订阅收缩会立即删除干净区块，先写方块会让 finishChanges 取到 nil record 而
// 崩溃；玩家放置路径的 interactions 循环同样在收敛之后结算，与此对齐）。与
// executePlacement 的玩家语义一致，碰撞判定使用物理阶段之后的身体位置。
//
// 意图在 action 阶段已按 CompanionID 字节序收集，这里按同一顺序结算：多个
// 伙伴竞争同一空气格时先结算者成交，后来者观察到非空气目标被零副作用拒绝，
// 写序完全确定。任一意图失败都不影响其他意图，也不产生 Rejection——伙伴
// action 的拒绝不进入 result.Rejected，"任务失败"判定属于 Manager（Task 7）。
func (engine *Engine) settleCompanionPlacements(
	intents []companionPlaceIntent,
	pending map[core.ChunkKey]*pendingChunkChanges,
) {
	for index := range intents {
		intent := intents[index]
		entry := engine.companions[intent.id]
		// 意图来自本 tick action 阶段的 active 快照，正常路径必然命中；
		// 防御性跳过以防未来引入同 tick 失活路径。
		if entry == nil || !entry.active {
			continue
		}
		engine.completeCompanionPlacement(entry, intent.target, intent.block, pending)
	}
}

// completeCompanionPlacement 在单一权威 tick 内完成一个放置意图的原子结算：
// 先走与 executePlacement 相同的玩家校验链（可放置注册表、目标为空气、与放置者
// 自身的碰撞判定、Ready 区块、容器槽位预留），全部通过后在背包副本上预演扣一件
// （首个对应物品堆），再写方块并在成功时一次性提交背包。任一步失败都零副作用：
// 校验与预演不触碰世界与背包，SetBlock 成功后没有再可失败的路径，扣料与写方块
// 因此总是同 tick 同时成立或同时不发生，世界变更汇入 pendingChunkChanges 原子
// 发布。物品不足由 action 语义拒绝（本函数返回 false，无任何可观察副作用）。
func (engine *Engine) completeCompanionPlacement(
	entry *companionState,
	target core.BlockPos,
	blockID core.BlockID,
	pending map[core.ChunkKey]*pendingChunkChanges,
) bool {
	item, ok := companionPlaceableBlock(blockID)
	if !ok {
		return false
	}
	// 往返校验通过意味着 ItemPlacement(item) 恰好还原成 blockID，因此下面沿用
	// 的 placement 与 blockID 是同一个值，只是保留放置方向的命名。
	placement := blockID
	dimension := engine.dimensions[entry.dimension]
	if dimension == nil {
		return false
	}
	if target.Y < core.MinY || target.Y >= core.MaxY {
		return false
	}
	block, ready := dimension.BlockAt(target)
	if !ready {
		return false
	}
	// 空气目标与放置者碰撞共用 executePlacement 的同一判定；伙伴与玩家同体
	// （同一 physics 出口），placementOverlapsPlayer 直接适用。
	if block != core.AirID || placementOverlapsPlayer(placement, target, entry.state.Position) {
		return false
	}
	// 放置熔炉或箱子必须先预留槽位；Prepare* 是纯预检不改区块，失败路径零副作用。
	// 槽位耗尽与玩家路径同样整体拒绝，不改方块也不扣物品。
	targetRecord, targetOK := dimension.records[target.Chunk()]
	targetIndex, targetIndexed := world.ChunkBlockIndex(target)
	furnaceSlot, reserveFurnace := -1, false
	chestSlot, reserveChest := -1, false
	if placement == core.FurnaceID {
		if !targetOK || targetRecord.Chunk == nil || !targetIndexed {
			return false
		}
		slot, ok := targetRecord.Chunk.PrepareFurnace(targetIndex)
		if !ok {
			return false
		}
		furnaceSlot, reserveFurnace = slot, true
	}
	if placement == core.ChestID {
		if !targetOK || targetRecord.Chunk == nil || !targetIndexed {
			return false
		}
		slot, ok := targetRecord.Chunk.PrepareChest(targetIndex)
		if !ok {
			return false
		}
		chestSlot, reserveChest = slot, true
	}
	// 物品选择没有快捷栏语义：按统一索引扣首个对应物品堆，在副本上预演；
	// 背包没有对应物品时由 action 语义拒绝，此时连预演结果都不存在。
	staged, ok := consumeFirstInventoryItem(entry.inventory, item)
	if !ok {
		return false
	}
	_, changed, err := dimension.SetBlock(target, placement)
	if err != nil || !changed {
		// 目标已在前置校验确认是空气；changed=false 只能来自同 tick 更早结算的
		// 意图（多伙伴竞争），对齐玩家的 RejectOccupied 语义整体拒绝不扣料。
		return false
	}
	engine.recordChange(entry.dimension, target, placement, pending)
	if reserveFurnace {
		targetRecord.Chunk.CommitFurnace(furnaceSlot, targetIndex)
	}
	if reserveChest {
		targetRecord.Chunk.CommitChest(chestSlot, targetIndex)
	}
	entry.inventory = staged
	entry.inventoryDirty = true
	return true
}
