package sim

import (
	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/world"
)

// MiningUpdate 是一名玩家本 tick 发布的规范权威采掘进度。M5C 起同一结构也
// 承载伙伴的采掘进度发布（CompanionUpdate.Mining），两类 actor 的进度语义
// 完全一致，共用这一个载体。
type MiningUpdate struct {
	Active        bool
	Target        core.BlockPos
	ProgressTicks uint16
	RequiredTicks uint16
	Harvestable   bool
}

// miningState 是玩家与伙伴两类 actor 共有的权威采掘进度状态机（原
// playerMiningState 整体上移 actorState）：记录目标、命中时的方块、持握工具
// 与进度计数。同一 target/block/held 连续命中时进度递增，任一变化即从 1
// 重新开始——这条"目标替换失效"语义由两类 actor 共用。
type miningState struct {
	target        core.BlockPos
	block         core.BlockID
	held          core.ItemID
	progressTicks uint16
	requiredTicks uint16
	harvestable   bool
}

func (state miningState) update() MiningUpdate {
	if state.requiredTicks == 0 {
		return MiningUpdate{}
	}
	return MiningUpdate{
		Active:        true,
		Target:        state.target,
		ProgressTicks: state.progressTicks,
		RequiredTicks: state.requiredTicks,
		Harvestable:   state.harvestable,
	}
}

// wheatSeedDropCount 是收获一株成熟小麦额外产出的种子数（design.md D9：掉落
// 数量固定，不随机）。固定值同时保证「误挖不亏种子」——耕种循环不会死。
const wheatSeedDropCount = uint8(2)

func miningRule(block core.BlockID, held core.ItemID) (uint16, bool) {
	// 农业方块与手持无关（锄头不是采掘工具，作物与耕地徒手即可收）：作物 1
	// tick、耕地 5 tick（与泥土同价，翻地这一步撤销得和挖泥土一样费力）。
	//
	// 作物是 1 tick 而不是 0：`0` 是本函数「不可采掘」的哨兵（基岩语义），
	// stepMiningProgress 与两条完成判定都以 requiredTicks == 0 直接跳过，用 0
	// 会让作物永远挖不动。1 是最小权威量子，玩家感知上仍是"一碰就掉"。
	//
	// 两条判据写成 core.IsCrop / core.IsFarmland 而不是穷举十个编号：阶段编号
	// 连续且只会整体追加，穷举漏一个阶段就是一格永远挖不动的作物。
	if core.IsCrop(block) {
		return 1, true
	}
	if core.IsFarmland(block) {
		return 5, true
	}
	switch block {
	case core.DirtID, core.GrassID, core.SandID, core.GravelID, core.LeavesID,
		core.GlassID, core.WhiteWoolID, core.ClayID, core.SnowBlockID:
		return 5, true
	case core.OakLogID, core.OakPlanksID:
		return 15, true
	case core.StoneID, core.CobblestoneID, core.SmoothStoneID, core.BrickID,
		core.RoofTileID, core.MossyCobblestoneID:
		switch held {
		case core.ItemNone, core.ItemBrokenStonePickaxe, core.ItemBrokenIronPickaxe:
			return 30, true
		case core.ItemStonePickaxe:
			return 15, true
		case core.ItemIronPickaxe:
			return 8, true
		default:
			return 30, false
		}
	case core.StoneBrickID, core.FurnaceID, core.ChestID, core.LightBlockID,
		core.CoalOreID, core.IronOreID:
		switch held {
		case core.ItemStonePickaxe:
			return 15, true
		case core.ItemIronPickaxe:
			return 8, true
		default:
			return 30, false
		}
	case core.IronBlockID:
		switch held {
		case core.ItemStonePickaxe:
			return 20, false
		case core.ItemIronPickaxe:
			return 10, true
		default:
			return 40, false
		}
	default:
		return 0, false
	}
}

// stepMiningProgress 是两类 actor 共用的采掘进度状态机推进器：按 miningRule
// 重新判定计时与可收获性，同一目标/方块/工具连续命中时递增进度，任一变化从
// 1 重新开始（目标替换失效语义的唯一实现点）。调用方负责先完成交互距离、
// Ready 区块与（伙伴侧的）容器防御校验。没有采掘规则的方块（如基岩）会把
// 状态清零，调用方必须以 requiredTicks == 0 为准直接跳过完成判定——与既有
// 玩家路径在规则为零时的提前 continue 语义逐字对齐。
//
// 递增在进度满格时饱和：唯一能让进度停在满格的是伙伴完成 tick 因背包无容量
// 不结算——此时进度必须保持满格作为"就绪但无容量"的稳定可观察状态；玩家路径
// 在完成 tick 总会清零状态，永远观察不到这一钳制，玩家行为逐 tick 不变。
func stepMiningProgress(actor *actorState, target core.BlockPos, block core.BlockID) {
	held := actor.inventory.Hotbar.Slots[actor.inventory.Hotbar.Selected].Item
	required, harvestable := miningRule(block, held)
	if required == 0 {
		actor.mining = miningState{}
		return
	}
	if actor.mining.target == target && actor.mining.block == block &&
		actor.mining.held == held && actor.mining.requiredTicks != 0 {
		if actor.mining.progressTicks < actor.mining.requiredTicks {
			actor.mining.progressTicks++
		}
		return
	}
	actor.mining = miningState{
		target:        target,
		block:         block,
		held:          held,
		progressTicks: 1,
		requiredTicks: required,
		harvestable:   harvestable,
	}
}

// blockRaycastSampler 返回权威交互射线共用的采样回调：区块未就绪返回
// ErrChunkNotReady，命中判定一律走 core.InteractionTarget（空气与流体都不是
// 目标）。玩家采掘、玩家放置、伙伴采掘的视线遮挡与开启容器四条路径共用它，
// 同一份交互距离（InteractionReach）加同一份 solid 谓词，保证没有第二套规则
// 实现——流体豁免只在这一处写，任何调用点都不可能漏掉。
func blockRaycastSampler(dimension *Dimension) func(core.BlockPos) (bool, error) {
	return func(position core.BlockPos) (bool, error) {
		block, ready := dimension.BlockAt(position)
		if !ready {
			return false, ErrChunkNotReady
		}
		return core.InteractionTarget(block), nil
	}
}

// companionMineableBlock 是伙伴采掘目标的防御清单：必须具有单一 BlockDrop 且
// 不是箱子/熔炉——容器破坏会掉落本体加全部内容物的多份产物，超出"单一产物
// 直入背包"的结算形状。Planner 契约之外，权威模拟在这里完成第二重拒绝。
func companionMineableBlock(block core.BlockID) bool {
	if block == core.ChestID || block == core.FurnaceID {
		return false
	}
	_, ok := core.BlockDrop(block)
	return ok
}

// blockCenterVec3 返回方块几何中心，用作伙伴采掘射线的方向锚点。
func blockCenterVec3(target core.BlockPos) mgl32.Vec3 {
	return mgl32.Vec3{
		float32(target.X) + 0.5,
		float32(target.Y) + 0.5,
		float32(target.Z) + 0.5,
	}
}

// advanceMining 是物理阶段之后的统一采掘推进：先按会话 ID 序处理玩家，再按
// CompanionID 字节序处理 active 伙伴。两类 actor 共用 stepMiningProgress 的
// 累积语义与完成判定，玩家与伙伴的差别只在完成 tick 的产物去向（玩家掉落物、
// 伙伴直入背包）与进度发布载体（MiningUpdate/CompanionUpdate.Mining）。
func (engine *Engine) advanceMining(
	pending map[core.ChunkKey]*pendingChunkChanges,
	result *TickResult,
) {
	var sessions [8]SessionID
	count := 0
	for id, session := range engine.sessions {
		if session.player == nil || session.player.lifecycle != PlayerActive {
			continue
		}
		if count == len(sessions) {
			panic("sim: more than eight active player sessions")
		}
		index := count
		for index > 0 && sessions[index-1] > id {
			sessions[index] = sessions[index-1]
			index--
		}
		sessions[index] = id
		count++
	}

	for _, id := range sessions[:count] {
		session := engine.sessions[id]
		player := session.player
		if !player.miningHeld || player.reset || !session.hasView || session.viewContainer {
			player.mining = miningState{}
			continue
		}
		dimension := engine.dimensions[session.dimension]
		if dimension == nil {
			player.mining = miningState{}
			continue
		}
		origin := player.state.Position.Add(mgl32.Vec3{0, engine.physicsTunables.EyeHeight, 0})
		hit, ok, err := core.RaycastBlocks(
			origin,
			LookDirection(player.yaw, player.pitch),
			engine.tunables.InteractionReach,
			blockRaycastSampler(dimension),
		)
		if err != nil || !ok {
			player.mining = miningState{}
			continue
		}
		block, ready := dimension.BlockAt(hit.Block)
		if !ready {
			player.mining = miningState{}
			continue
		}
		stepMiningProgress(&player.actorState, hit.Block, block)
		if player.mining.requiredTicks == 0 ||
			player.mining.progressTicks < player.mining.requiredTicks {
			continue
		}
		// 完成分叉（玩家侧）：产物成为世界掉落物，语义与 M5C 之前逐字相同。
		reason, rejected := engine.completeMining(
			session.dimension,
			player.mining.target,
			player.mining.block,
			player.mining.harvestable,
			pending,
		)
		player.mining = miningState{}
		if rejected {
			result.Rejected = append(result.Rejected, Rejection{
				Session: id, Sequence: player.lastInputSequence, Reason: reason,
			})
			continue
		}
		if consumeToolDurability(&player.actorState) {
			player.inventoryDirty = true
		}
	}

	// 无伙伴注册时保持既有玩家路径的零分配轮廓，不进入伙伴循环。
	if len(engine.companions) == 0 {
		return
	}
	for _, id := range engine.activeCompanionIDs() {
		engine.advanceCompanionMining(engine.companions[id], pending)
	}
}

// advanceCompanionMining 推进一个 active 伙伴的采掘。交互距离、Ready 区块与
// 视线遮挡复用玩家的 core.RaycastBlocks + InteractionReach + blockRaycastSampler
// 实现：射线从伙伴眼睛指向目标方块中心，命中必须恰好是目标本身（被遮挡、超距
// 或区块未就绪都会清空进度，与玩家的无效目标语义一致）。容器与多掉落方块在
// 进度累积之前就被防御拒绝。完成 tick 经 completeCompanionMining 分叉结算。
func (engine *Engine) advanceCompanionMining(
	entry *companionState,
	pending map[core.ChunkKey]*pendingChunkChanges,
) {
	if !entry.miningHeld {
		entry.mining = miningState{}
		return
	}
	dimension := engine.dimensions[entry.dimension]
	if dimension == nil {
		entry.mining = miningState{}
		return
	}
	target := entry.miningTarget
	eye := entry.state.Position.Add(mgl32.Vec3{0, engine.physicsTunables.EyeHeight, 0})
	hit, ok, err := core.RaycastBlocks(
		eye,
		blockCenterVec3(target).Sub(eye),
		engine.tunables.InteractionReach,
		blockRaycastSampler(dimension),
	)
	if err != nil || !ok || hit.Block != target {
		entry.mining = miningState{}
		return
	}
	block, ready := dimension.BlockAt(target)
	if !ready {
		entry.mining = miningState{}
		return
	}
	if !companionMineableBlock(block) {
		entry.mining = miningState{}
		return
	}
	stepMiningProgress(&entry.actorState, target, block)
	if entry.mining.requiredTicks == 0 ||
		entry.mining.progressTicks < entry.mining.requiredTicks {
		return
	}
	// 完成分叉（伙伴侧）：产物直入背包，三方原子。
	engine.completeCompanionMining(entry, pending)
}

// completeCompanionMining 在进度满格的 tick 结算伙伴采掘，三方必须原子成立：
// 目标方块改为空气、按既有规则扣除工具耐久（含损坏形态）、可收获产物直入
// 伙伴背包。容量前验先行——AddStack 在背包副本上预演，余量非空则该 tick 整体
// 不结算：方块不变、耐久不变、背包不变、进度保持满格，Manager 由此观察到
// "就绪但无容量"的稳定状态（"任务失败"的判定属于 Manager，不在这里）。
// 预演通过后 SetBlock 与内存提交在单写者 tick 内不再有失败路径，三方在同一
// tick 内同时成立。
func (engine *Engine) completeCompanionMining(
	entry *companionState,
	pending map[core.ChunkKey]*pendingChunkChanges,
) {
	if !companionMineableBlock(entry.mining.block) {
		entry.mining = miningState{}
		return
	}
	item, _ := core.BlockDrop(entry.mining.block)
	var staged core.Inventory
	if entry.mining.harvestable {
		var leftover core.ItemStack
		staged, leftover = entry.inventory.AddStack(core.ItemStack{Item: item, Count: 1})
		if leftover.Count != 0 {
			return
		}
	}
	_, changed, err := engine.dimensions[entry.dimension].SetBlock(entry.mining.target, core.AirID)
	if err != nil || !changed {
		// 区块失效或方块已被同 tick 更早的 actor 移除：对齐玩家 RejectNoTarget
		// 语义，清零进度且不结算。
		entry.mining = miningState{}
		return
	}
	engine.recordChange(entry.dimension, entry.mining.target, core.AirID, pending)
	if entry.mining.harvestable {
		entry.inventory = staged
		entry.inventoryDirty = true
	}
	if consumeToolDurability(&entry.actorState) {
		entry.inventoryDirty = true
	}
	entry.mining = miningState{}
}

// consumeToolDurability 在成功破坏方块后扣减选中工具的耐久。
// 耐久归零时把栏位整体替换为损坏形态。返回背包是否发生变化。
func consumeToolDurability(actor *actorState) bool {
	selected := actor.inventory.Hotbar.Selected
	stack := actor.inventory.Hotbar.Slots[selected]
	if _, ok := core.ItemMaxDurability(stack.Item); !ok {
		return false
	}
	if stack.Durability > 1 {
		stack.Durability--
		actor.inventory.Hotbar.Slots[selected] = stack
		return true
	}
	broken, ok := core.ItemBrokenForm(stack.Item)
	if !ok {
		return false
	}
	actor.inventory.Hotbar.Slots[selected] = core.ItemStack{Item: broken, Count: 1}
	return true
}

func (engine *Engine) completeMining(
	dimensionID core.DimensionID,
	target core.BlockPos,
	block core.BlockID,
	harvestable bool,
	pending map[core.ChunkKey]*pendingChunkChanges,
) (RejectReason, bool) {
	item, ok := core.BlockDrop(block)
	if !ok {
		return RejectProtectedBlock, true
	}
	dimension := engine.dimensions[dimensionID]
	if dimension == nil {
		return RejectChunkNotReady, true
	}
	record, recordOK := dimension.records[target.Chunk()]
	blockIndex, indexOK := world.ChunkBlockIndex(target)
	if !recordOK || record.State != ChunkReady || record.Chunk == nil || !indexOK {
		return RejectChunkNotReady, true
	}

	if block == core.FurnaceID {
		furnaceSlot, found := record.Chunk.FurnaceAt(blockIndex)
		if !found {
			return RejectChunkNotReady, true
		}
		furnace := record.Chunk.Furnace(furnaceSlot)
		stacks := [4]core.ItemStack{
			{},
			furnace.Input,
			furnace.Fuel,
			furnace.Output,
		}
		if harvestable {
			stacks[0] = core.ItemStack{Item: core.ItemFurnace, Count: 1}
		}
		next, capacityOK := record.Chunk.PrepareDropBatch(
			stacks[:], blockIndex, engine.tunables.DropPickupDelayTicks,
		)
		if !capacityOK {
			return RejectDropCapacity, true
		}
		_, changed, err := dimension.SetBlock(target, core.AirID)
		if err != nil {
			return mapSetBlockError(err), true
		}
		if !changed {
			return RejectNoTarget, true
		}
		engine.recordChange(dimensionID, target, core.AirID, pending)
		record.Chunk.DeactivateFurnace(furnaceSlot)
		record.Chunk.CommitDropBatch(next)
		return 0, false
	}

	if block == core.ChestID {
		chestSlot, found := record.Chunk.ChestAt(blockIndex)
		if !found {
			return RejectChunkNotReady, true
		}
		chest := record.Chunk.Chest(chestSlot)
		var stacks [1 + core.ChestSlots]core.ItemStack
		if harvestable {
			stacks[0] = core.ItemStack{Item: core.ItemChest, Count: 1}
		}
		copy(stacks[1:], chest.Items[:])
		next, capacityOK := record.Chunk.PrepareDropBatch(
			stacks[:], blockIndex, engine.tunables.DropPickupDelayTicks,
		)
		if !capacityOK {
			return RejectDropCapacity, true
		}
		_, changed, err := dimension.SetBlock(target, core.AirID)
		if err != nil {
			return mapSetBlockError(err), true
		}
		if !changed {
			return RejectNoTarget, true
		}
		engine.recordChange(dimensionID, target, core.AirID, pending)
		record.Chunk.DeactivateChest(chestSlot)
		record.Chunk.CommitDropBatch(next)
		return 0, false
	}

	// 成熟小麦是全仓唯一的多产物方块：1 小麦 + 2 种子。多产物**刻意不进
	// core.BlockDrop**——那张表的返回形状是单一产物，改成多产物会波及它的全部
	// 消费者（伙伴采掘与放置的防御清单、planner 的 place 注册表交叉校验、
	// 客户端镜像），而收益只是这一个方块（Ruling 5）。因此 core 只登记主产物
	// 小麦，额外的种子在这里按方块编号补发，多产物的知识只存在于权威结算路径。
	//
	// 批量预演复用破坏熔炉/箱子的 PrepareDropBatch：任一堆放不下就整体返回
	// false，方块与掉落槽逐字节不变，绝不出现"小麦掉了、种子没掉"的半掉落。
	if block == core.WheatStage7ID && harvestable {
		stacks := [2]core.ItemStack{
			{Item: item, Count: 1},
			{Item: core.ItemWheatSeeds, Count: wheatSeedDropCount},
		}
		next, capacityOK := record.Chunk.PrepareDropBatch(
			stacks[:], blockIndex, engine.tunables.DropPickupDelayTicks,
		)
		if !capacityOK {
			return RejectDropCapacity, true
		}
		_, changed, err := dimension.SetBlock(target, core.AirID)
		if err != nil {
			return mapSetBlockError(err), true
		}
		if !changed {
			return RejectNoTarget, true
		}
		engine.recordChange(dimensionID, target, core.AirID, pending)
		record.Chunk.CommitDropBatch(next)
		return 0, false
	}

	dropSlot := 0
	if harvestable {
		var capacityOK bool
		dropSlot, capacityOK = record.Chunk.PrepareDrop(item, blockIndex)
		if !capacityOK {
			return RejectDropCapacity, true
		}
	}
	_, changed, err := dimension.SetBlock(target, core.AirID)
	if err != nil {
		return mapSetBlockError(err), true
	}
	if !changed {
		return RejectNoTarget, true
	}
	engine.recordChange(dimensionID, target, core.AirID, pending)
	if harvestable {
		record.Chunk.CommitDrop(
			dropSlot,
			core.ItemStack{Item: item, Count: 1},
			blockIndex,
			engine.tunables.DropPickupDelayTicks,
		)
	}
	return 0, false
}
