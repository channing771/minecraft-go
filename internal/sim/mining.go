package sim

import (
	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/physics"
	"minecraft-go/internal/world"
)

// MiningUpdate 是一名玩家本 tick 发布的规范权威采掘进度。
type MiningUpdate struct {
	Active        bool
	Target        core.BlockPos
	ProgressTicks uint16
	RequiredTicks uint16
	Harvestable   bool
}

type playerMiningState struct {
	target        core.BlockPos
	block         core.BlockID
	held          core.ItemID
	progressTicks uint16
	requiredTicks uint16
	harvestable   bool
}

func (state playerMiningState) update() MiningUpdate {
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

func miningRule(block core.BlockID, held core.ItemID) (uint16, bool) {
	switch block {
	case core.DirtID, core.GrassID:
		return 5, true
	case core.StoneID:
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
	case core.StoneBrickID, core.FurnaceID, core.CoalOreID, core.IronOreID:
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
		if !player.miningHeld || player.reset || !session.hasView || session.viewFurnace {
			player.mining = playerMiningState{}
			continue
		}
		dimension := engine.dimensions[session.dimension]
		if dimension == nil {
			player.mining = playerMiningState{}
			continue
		}
		origin := player.state.Position.Add(mgl32.Vec3{0, physics.EyeHeight, 0})
		hit, ok, err := core.RaycastBlocks(
			origin,
			LookDirection(player.yaw, player.pitch),
			interactionReach,
			func(position core.BlockPos) (bool, error) {
				block, ready := dimension.BlockAt(position)
				if !ready {
					return false, ErrChunkNotReady
				}
				return block != core.AirID, nil
			},
		)
		if err != nil || !ok {
			player.mining = playerMiningState{}
			continue
		}
		block, ready := dimension.BlockAt(hit.Block)
		if !ready {
			player.mining = playerMiningState{}
			continue
		}
		held := player.inventory.Hotbar.Slots[player.inventory.Hotbar.Selected].Item
		required, harvestable := miningRule(block, held)
		if required == 0 {
			player.mining = playerMiningState{}
			continue
		}
		if player.mining.target == hit.Block && player.mining.block == block &&
			player.mining.held == held && player.mining.requiredTicks != 0 {
			player.mining.progressTicks++
		} else {
			player.mining = playerMiningState{
				target:        hit.Block,
				block:         block,
				held:          held,
				progressTicks: 1,
				requiredTicks: required,
				harvestable:   harvestable,
			}
		}
		if player.mining.progressTicks < player.mining.requiredTicks {
			continue
		}
		reason, rejected := engine.completeMining(
			session.dimension,
			player.mining.target,
			player.mining.block,
			player.mining.harvestable,
			pending,
		)
		player.mining = playerMiningState{}
		if rejected {
			result.Rejected = append(result.Rejected, Rejection{
				Session: id, Sequence: player.lastInputSequence, Reason: reason,
			})
			continue
		}
		if consumeToolDurability(player) {
			player.inventoryDirty = true
		}
	}
}

// consumeToolDurability 在成功破坏方块后扣减选中工具的耐久。
// 耐久归零时把栏位整体替换为损坏形态。返回背包是否发生变化。
func consumeToolDurability(player *playerState) bool {
	selected := player.inventory.Hotbar.Selected
	stack := player.inventory.Hotbar.Slots[selected]
	if _, ok := core.ItemMaxDurability(stack.Item); !ok {
		return false
	}
	if stack.Durability > 1 {
		stack.Durability--
		player.inventory.Hotbar.Slots[selected] = stack
		return true
	}
	broken, ok := core.ItemBrokenForm(stack.Item)
	if !ok {
		return false
	}
	player.inventory.Hotbar.Slots[selected] = core.ItemStack{Item: broken, Count: 1}
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
			stacks, blockIndex, DropPickupDelayTicks,
		)
		if !capacityOK {
			return RejectDropCapacity, true
		}
		_, changed, err := dimension.SetBlock(target, core.AirID)
		if err != nil {
			return mapSetBlockError(err), true
		}
		if changed {
			engine.recordChange(dimensionID, target, core.AirID, pending)
			record.Chunk.DeactivateFurnace(furnaceSlot)
			record.Chunk.CommitDropBatch(next)
		}
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
	if changed {
		engine.recordChange(dimensionID, target, core.AirID, pending)
		if harvestable {
			record.Chunk.CommitDrop(
				dropSlot,
				core.ItemStack{Item: item, Count: 1},
				blockIndex,
				DropPickupDelayTicks,
			)
		}
	}
	return 0, false
}
