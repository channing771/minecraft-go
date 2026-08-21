package sim

import (
	"errors"
	"fmt"
	"math"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/physics"
	"github.com/channing771/mornlea/internal/world"
)

func (engine *Engine) executePlacement(
	command Command,
	pending map[core.ChunkKey]*pendingChunkChanges,
) (RejectReason, bool) {
	session := engine.sessions[command.Session]
	if command.Kind != CommandPlaceBlock {
		return RejectInvalidRay, true
	}
	if session == nil || session.player == nil ||
		session.player.lifecycle != PlayerActive {
		return RejectPlayerNotReady, true
	}
	dimensionID := session.dimension
	origin := session.player.state.Position.Add(mgl32.Vec3{0, engine.physicsTunables.EyeHeight, 0})
	direction := LookDirection(command.Yaw, command.Pitch)
	dimension := engine.dimensions[dimensionID]
	originBlock := core.BlockPos{
		X: int32(math.Floor(float64(origin.X()))),
		Y: int32(math.Floor(float64(origin.Y()))),
		Z: int32(math.Floor(float64(origin.Z()))),
	}
	if !session.hasView || dimension == nil {
		return RejectInvalidRay, true
	}
	originKey := core.ChunkKey{
		Dimension: dimensionID,
		Pos:       originBlock.Chunk(),
	}
	if _, subscribed := session.wanted[originKey]; !subscribed {
		return RejectInvalidRay, true
	}
	player := session.player
	// 放置先从请求栏位解析物品和方块，并在快捷栏副本上预演扣除。
	if command.Slot >= core.HotbarSlots {
		return RejectInvalidSlot, true
	}
	placement, ok := core.ItemPlacement(player.inventory.Hotbar.Slots[command.Slot].Item)
	if !ok {
		return RejectInvalidBlock, true
	}
	consumed, ok := player.inventory.Hotbar.Consume(command.Slot)
	if !ok {
		return RejectInvalidBlock, true
	}

	hit, ok, err := core.RaycastBlocks(
		origin,
		direction,
		engine.tunables.InteractionReach,
		blockRaycastSampler(dimension),
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

	if hit.Face == core.BlockFaceNone {
		return RejectOccupied, true
	}
	target := adjacentBlock(hit.Block, hit.Face)
	if target.Y < core.MinY || target.Y >= core.MaxY {
		return RejectChunkNotReady, true
	}
	block, ready := dimension.BlockAt(target)
	if !ready {
		return RejectChunkNotReady, true
	}
	// 流体格不算被占用：射线现在会穿过水命中水下的固体，落点因此常常是一格水，
	// 放置必须直接把它覆盖掉（与 Minecraft 系的「向水里放方块」同语义）。覆盖
	// 走的是下面同一条 SetBlock → recordChange 路径，因此该格及其六个面邻格会被
	// enqueueFluidUpdate 重新入队，被切断的下游流动水才会按规则变干，水面不会
	// 留下一个不会被填回的洞。
	occupied := (block != core.AirID && !core.IsFluid(block)) || placementOverlapsPlayer(
		placement,
		target,
		player.state.Position,
	)
	if occupied {
		return RejectOccupied, true
	}
	// 作物是唯一有「落脚方块」前置的放置物：种子只能种在耕地正上方。这条前置
	// **追加**在通用校验之后，因此非种子物品的放置行为一字不变。
	//
	// 判据读的是**目标格正下方的方块编号**，不是命中面：玩家通常俯视耕地顶面
	// （目标格就在耕地之上），但也可以瞄准旁边方块的侧面把目标格落到耕地上方，
	// 两种瞄法都应放行。要求"命中面必须是耕地"会把后一种合法瞄法误拒。
	//
	// 「手持物不适用于当前目标」与「选中物品不产生可放置方块」是同一类事实，
	// 复用放置路径与翻地已在用的 RejectInvalidBlock，不新增 wire 值。
	if core.IsCrop(placement) {
		below := target
		below.Y--
		belowBlock, belowReady := dimension.BlockAt(below)
		if !belowReady {
			return RejectChunkNotReady, true
		}
		if !core.IsFarmland(belowBlock) {
			return RejectInvalidBlock, true
		}
	}
	// 放置熔炉或箱子必须先预留槽位；槽位耗尽时不改方块也不扣物品。
	targetRecord, targetOK := dimension.records[target.Chunk()]
	targetIndex, targetIndexed := world.ChunkBlockIndex(target)
	furnaceSlot, reserveFurnace := -1, false
	chestSlot, reserveChest := -1, false
	if placement == core.FurnaceID {
		if !targetOK || targetRecord.Chunk == nil || !targetIndexed {
			return RejectChunkNotReady, true
		}
		slot, ok := targetRecord.Chunk.PrepareFurnace(targetIndex)
		if !ok {
			return RejectContainerCapacity, true
		}
		furnaceSlot, reserveFurnace = slot, true
	}
	if placement == core.ChestID {
		if !targetOK || targetRecord.Chunk == nil || !targetIndexed {
			return RejectChunkNotReady, true
		}
		slot, ok := targetRecord.Chunk.PrepareChest(targetIndex)
		if !ok {
			return RejectContainerCapacity, true
		}
		chestSlot, reserveChest = slot, true
	}
	_, changed, setErr := dimension.SetBlock(target, placement)
	if setErr != nil {
		return mapSetBlockError(setErr), true
	}
	if changed {
		engine.recordChange(
			dimensionID,
			target,
			placement,
			pending,
		)
		if reserveFurnace {
			targetRecord.Chunk.CommitFurnace(furnaceSlot, targetIndex)
		}
		if reserveChest {
			targetRecord.Chunk.CommitChest(chestSlot, targetIndex)
		}
		player.inventory.Hotbar = consumed
		player.inventoryDirty = true
	}
	return 0, false
}

func validPlayerInput(command Command) bool {
	return command.MoveX >= -1 && command.MoveX <= 1 &&
		command.MoveZ >= -1 && command.MoveZ <= 1 &&
		validPlayerLook(command.Yaw, command.Pitch)
}

func validPlayerLook(yaw, pitch float32) bool {
	const maxPitch = float32(math.Pi/2 - 0.01)
	return finiteInputComponent(yaw) && finiteInputComponent(pitch) &&
		pitch >= -maxPitch && pitch <= maxPitch
}

func finiteInputComponent(value float32) bool {
	return !math.IsNaN(float64(value)) && !math.IsInf(float64(value), 0)
}

func normalizeYaw(yaw float32) float32 {
	normalized := math.Mod(float64(yaw)+math.Pi, 2*math.Pi)
	if normalized < 0 {
		normalized += 2 * math.Pi
	}
	return float32(normalized - math.Pi)
}

func adjacentBlock(block core.BlockPos, face core.BlockFace) core.BlockPos {
	switch face {
	case core.BlockFaceNegX:
		block.X--
	case core.BlockFacePosX:
		block.X++
	case core.BlockFaceNegY:
		block.Y--
	case core.BlockFacePosY:
		block.Y++
	case core.BlockFaceNegZ:
		block.Z--
	case core.BlockFacePosZ:
		block.Z++
	default:
		panic(fmt.Sprintf("sim: invalid hit face %d", face))
	}
	return block
}

func placementOverlapsPlayer(
	block core.BlockID,
	position core.BlockPos,
	playerPosition mgl32.Vec3,
) bool {
	playerBounds := physics.PlayerBounds(playerPosition)
	boxes := physics.BlockCollisionBoxes(block, true)
	offset := mgl32.Vec3{float32(position.X), float32(position.Y), float32(position.Z)}
	for index := 0; index < min(int(boxes.Count), len(boxes.Boxes)); index++ {
		box := core.AABB{
			Min: boxes.Boxes[index].Min.Add(offset),
			Max: boxes.Boxes[index].Max.Add(offset),
		}
		if playerBounds.Overlaps(box) {
			return true
		}
	}
	return false
}

func mapSetBlockError(err error) RejectReason {
	if errors.Is(err, ErrChunkNotReady) || errors.Is(err, ErrBlockOutOfWorld) {
		return RejectChunkNotReady
	}
	return RejectInvalidRay
}
