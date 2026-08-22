package sim

import (
	"errors"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
)

// executeTillSoil 处理一条翻地命令：把视线内的泥土或草变成干耕地，并从权威
// 选中的锄头扣减恰好一点耐久。
//
// **取目标的路径与 openContainer 同形**：权威眼睛坐标作起点、LookDirection 解
// 命令朝向、同一份 blockRaycastSampler、同一个 engine.tunables.InteractionReach。
// 因此触及距离、流体豁免（射线穿过水）与"区块未就绪"三条语义没有第二套实现，
// 翻地不可能与开容器、采掘、放置在这些判定上产生分歧。
//
// **校验顺序是本函数的核心契约**，自上而下依次是：
//
//  1. 会话与玩家处于 Active；
//  2. 射线本身有效、且在触及距离内命中了目标；
//  3. 命中格所属区块已就绪；
//  4. 命中格是泥土或草；
//  5. 命中格正上方是空气；
//  6. 权威选中格握着一把还有耐久的锄头。
//
// 六道校验**全部通过之后**才执行最后一步：写方块 + 扣耐久。
//
// 为什么必须把扣耐久压到最后一步：耐久是玩家可见的不可逆资源，任何一条拒绝
// 路径提前扣掉它，都会让"被拒绝的命令不改变任何状态"这条不变量失效，而这种
// 失效在读数上极难发现（玩家只会觉得锄头"莫名其妙用得快"）。把唯一的写入点
// 压到全部校验之后，拒绝路径在**结构上**就不可能磨损工具——正确性不依赖每条
// return 语句前都记得别扣。同理，方块写入也只有这一处。
//
// 耐久归零（1 → 0）时本次翻地仍然生效，锄头同时转为损坏形态：这与采掘完成
// tick 的既有语义逐字一致，两者共用 consumeToolDurability 这一个实现。
func (engine *Engine) executeTillSoil(
	command Command,
	pending map[core.ChunkKey]*pendingChunkChanges,
) (RejectReason, bool) {
	session := engine.sessions[command.Session]
	if session == nil || session.player == nil ||
		session.player.lifecycle != PlayerActive {
		return RejectPlayerNotReady, true
	}
	dimension := engine.dimensions[session.dimension]
	if dimension == nil {
		return RejectChunkNotReady, true
	}
	player := session.player
	origin := player.state.Position.
		Add(mgl32.Vec3{0, engine.physicsTunables.EyeHeight, 0})
	hit, ok, err := core.RaycastBlocks(
		origin,
		LookDirection(command.Yaw, command.Pitch),
		engine.tunables.InteractionReach,
		blockRaycastSampler(dimension),
	)
	if err != nil {
		if errors.Is(err, ErrChunkNotReady) {
			return RejectChunkNotReady, true
		}
		return RejectInvalidRay, true
	}
	// "超出触及距离"与"视线内没有目标"在权威侧是同一个可观察事实：射线在
	// InteractionReach 之内没有命中任何交互目标。因此复用既有的 RejectNoTarget，
	// 不为翻地新增 wire 值——RejectReason 的编号已冻结，只有语义确实不同的
	// 原因才值得追加。
	if !ok {
		return RejectNoTarget, true
	}
	block, ready := dimension.BlockAt(hit.Block)
	if !ready {
		return RejectChunkNotReady, true
	}
	if !core.TillableBlock(block) {
		return RejectInvalidBlock, true
	}
	// 上方必须是**方块编号意义上的空气**，不是"没有碰撞体"：作物与流体都零
	// 碰撞体却都不是空气，用碰撞判定会把"水面下的泥土"和"作物脚下的泥土"
	// 误判成可以翻地。
	above := hit.Block
	above.Y++
	aboveBlock, ready := dimension.BlockAt(above)
	if !ready {
		return RejectChunkNotReady, true
	}
	if aboveBlock != core.AirID {
		return RejectOccupied, true
	}
	// 手持物按权威选中格解析，客户端不参与决定用哪一格。手持物不适用于本次
	// 动作与"选中物品不产生可放置方块"是同一类事实，复用放置路径已在用的
	// RejectInvalidBlock。
	if !core.TillingTool(player.inventory.Hotbar.Slots[player.inventory.Hotbar.Selected].Item) {
		return RejectInvalidBlock, true
	}

	// —— 以下是唯一的写入区：全部校验已过 ——
	_, changed, setErr := dimension.SetBlock(hit.Block, core.FarmlandDryID)
	if setErr != nil {
		return mapSetBlockError(setErr), true
	}
	if !changed {
		// 同 tick 内更早的写者已经改掉了这一格；对齐采掘的 RejectNoTarget 语义，
		// 不写变更也不扣耐久。
		return RejectNoTarget, true
	}
	engine.recordChange(session.dimension, hit.Block, core.FarmlandDryID, pending)
	if consumeToolDurability(&player.actorState) {
		player.inventoryDirty = true
	}
	return 0, false
}
