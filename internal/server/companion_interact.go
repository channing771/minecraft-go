// 本文件实现 mine/place 步骤的执行编排：走近（复用 go_to 的移动语义）→
// action 提交 → 完成观察 → 事件与失败映射。全部逻辑运行在权威 tick 边界
// （持有 stepMu），观察输入只有三类：本 tick 缓存的伙伴身体（refreshBodies，
// 含 36 格背包）、上一次权威 TickResult 的采掘进度（observeTickResult）与
// 目标方块的当前权威值（CloneReadyChunk 只读拷贝）。对 sim 只提交
// CompanionAction——MineHold/MineRelease/Place 载荷，绝不旁路权威模拟，也
// 不在 Runner 侧复刻任何 sim 已有的判定规则。
package server

import (
	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/physics"
	"github.com/channing771/mornlea/internal/sim"
)

// interactionStepOf 返回任务当前步骤的 mine/place 形态；其他 kind（go_to、
// follow 或步骤索引越界的防御情形）返回 nil。
func interactionStepOf(task companion.Task) *companion.PlanStep {
	if task.StepIndex < 0 || task.StepIndex >= len(task.Plan.Steps) {
		return nil
	}
	step := &task.Plan.Steps[task.StepIndex]
	if step.Kind != companion.PlanStepMine && step.Kind != companion.PlanStepPlace {
		return nil
	}
	return step
}

// resetInteraction 归零 mine/place 的交互执行状态。调用点是任务边界的唯一
// 必经处（dispatchPlanning 的 BeginHead 分支与 restoreQueue）：三连失败等
// 终态路径没有逐一清理的义务——interactStepIndex 与新任务的 StepIndex 不
// 相等时执行器自会重新初始化，miningHeld 则由 releaseFinishedMining 统一
// 兜底释放。-1 哨兵保证「新任务恰好也从 StepIndex 0 开始」时不会误继承
// 上一个任务的就绪标记。
func (slot *companionTaskSlot) resetInteraction() {
	slot.interactStepIndex = -1
	slot.interactionReady = false
	slot.mineProgress = 0
	slot.mineRequired = 0
}

// interactionPhaseActive 报告当前任务是否正处于 mine/place 的交互阶段（走
// 近已结束）。dispatchPathRequests 以它停止交互期间的寻路派发：交互位置已
// 经确定，重算只会把伙伴拉离交互距离。
func (slot *companionTaskSlot) interactionPhaseActive(current companion.Task) bool {
	return slot.interactionReady &&
		slot.interactStepIndex == current.StepIndex &&
		interactionStepOf(current) != nil
}

// advanceInteractionRunner 推进一个 mine/place 步骤的一 tick 编排，分两段：
//
//   - 走近段：与 go_to 完全相同的移动语义（寻路、revision 重验、路径点
//     消费、每 tick 至多一个移动输入），终点是目标方块的相邻站立格（见
//     interactionGoal）；路径走尽即转入交互段。
//   - 交互段：mine 持续提交 MineHold 并观察 sim 的采掘进度；place 先验
//     交互距离后提交 Place 并观察目标方块的成交结果。
//
// 两个阶段每 tick 至多提交一个 action（sim 的每伙伴单 action 约束），
// 阶段切换发生在路径走尽的 tick，不产生额外动作。
func (m *companionManager) advanceInteractionRunner(
	slot *companionTaskSlot,
	id companion.ID,
	current companion.Task,
	step companion.PlanStep,
) {
	body, active := m.body(id)
	if !active {
		// 身体尚未激活（出生扫描在途）：既不移动也不交互，等下一 tick。
		return
	}
	if slot.interactStepIndex != current.StepIndex {
		// 步骤索引变化（同任务推进到下一个交互步骤，或任务更替后残留）：
		// 交互记忆整体作废，从走近段重新开始。
		slot.interactStepIndex = current.StepIndex
		slot.interactionReady = false
		slot.mineProgress = 0
		slot.mineRequired = 0
	}
	if !slot.interactionReady {
		if slot.path == nil {
			// 走近段缺少路径：等待 dispatchPathRequests 派发（含失败冷却），
			// 与 go_to 的等待语义一致。
			return
		}
		if m.advancePathMovement(slot, id, body) {
			// 路径走尽：抵达目标邻近站立格，转入交互段。本 tick 不再提交
			// 动作——移动输入撤销后伙伴自然减速，交互从下一 tick 开始，
			// 观察面（TickResult 与世界方块）保持一格一致的滞后。
			slot.interactionReady = true
			slot.hasReplanAt = false
		}
		return
	}
	if step.Kind == companion.PlanStepMine {
		m.holdCompanionMining(slot, id, body, step)
		return
	}
	m.submitCompanionPlacement(slot, id, body, step)
}

// holdCompanionMining 是 mine 步骤交互段的一 tick 编排：持续提交 MineHold
// 并以上一次 TickResult 的 CompanionUpdate.Mining 作为完成与失败判定的唯一
// 权威信号。
//
// 以 sim 权威信号代替 Runner 侧重复距离判定的权衡：sim 的采掘推进自带
// raycast 交互距离校验（超距/被遮挡/区块未 ready 的 MineHold 零进度清零），
// Runner 若再算一遍距离就存在第二套规则实现，两套判定迟早漂移。因此本
// 执行器只观察「进度是否开始累积」：未累积即保持按住（走近段已把伙伴带
// 到目标邻近站立格，正常几何下必然在交互距离内；个别边缘站位不达 raycast
// 可达性时按住无害——零进度不改变世界，deadline 兜底），累积即确知已达
// 标。距离语义因此只有 sim 一处实现。
//
// 失败映射（均经 FailRun 终局，绝不重试新目标）：
//   - 目标替换：sim 的目标替换失效语义把进度重置为 1（方块/工具变化时计时
//     规则也变化），Runner 观察到「进度回退或 RequiredTicks 变化」即以
//     TaskFailWorldChanged 失败，新方块 MUST NOT 被破坏；
//   - 背包无容量：sim 的容量前验拒绝结算时进度保持满格（稳定可观察状态），
//     Runner 观察到满格饱和且 tick 边界背包无法容纳产物（与 sim 同一
//     AddStack 判定）即以 TaskFailInventoryFull 失败，方块不变；
//   - 完成：sim 在结算 tick 清零采掘状态，Runner 观察到「未激活 + 方块已
//     空 + 进度证据恰好差一格达标」判定为本方完成（完成 tick 三方原子由
//     sim 保证，这里只观察）；方块已空而进度证据不足则是目标被其他 actor
//     移除，按目标变化语义失败。
func (m *companionManager) holdCompanionMining(
	slot *companionTaskSlot,
	id companion.ID,
	body companion.Body,
	step companion.PlanStep,
) {
	target := core.BlockPos{X: step.X, Y: step.Y, Z: step.Z}
	observed := m.mining[id]
	block, ready := m.blockAt(body.Dimension, target)
	if !ready {
		// 目标区块未 ready：不裁决也不改写按住状态，deadline 兜底。绝不
		// 基于半空世界观察失败任务。
		return
	}
	if !observed.Active || observed.Target != target {
		if block == core.AirID {
			// 完成与目标消失共用「进度未激活 + 方块已空」的观察面，用进度
			// 证据区分：结算发生在进度从 Required-1 递增到 Required 的那一
			// tick，此前最近一次活跃观察必然是 Required-1（无容量的饱和
			// 情形已在下方满格分支终局，到不了这里）。进度证据不足而方块
			// 已空只能是被其他 actor 移除——按目标变化语义失败。
			if slot.mineRequired != 0 && slot.mineProgress+1 >= slot.mineRequired {
				m.applyQueueEvents(slot, slot.queue.CompleteStep())
			} else {
				m.applyQueueEvents(slot, slot.queue.FailRun(companion.TaskFailWorldChanged))
			}
			return
		}
		// 方块仍在而进度尚未累积：保持按住，以 sim 的零进度作为「尚未进入
		// 交互距离」的权威信号（见函数头注释的距离判定权衡）。
		m.holdMining(slot, id, target)
		return
	}
	if slot.mineRequired != 0 &&
		(observed.ProgressTicks < slot.mineProgress || observed.RequiredTicks != slot.mineRequired) {
		// 同一目标/方块/工具的进度单调递增（满格即饱和不再增长），回退或
		// 计时规则变化只能是 sim 的目标替换失效语义——目标被其他 actor
		// 替换，既有进度作废。
		m.applyQueueEvents(slot, slot.queue.FailRun(companion.TaskFailWorldChanged))
		return
	}
	if observed.Harvestable && observed.ProgressTicks == observed.RequiredTicks {
		// 进度满格饱和且方块仍在：sim 的容量前验拒绝了结算（这是饱和唯一
		// 的稳定成因——有容量时满格 tick 必然结算并清零进度）。用与 sim
		// 完全相同的 AddStack 预演判定 tick 边界背包能否容纳产物，不能即
		// 以稳定原因失败；方块、耐久、背包在 sim 侧本就未变更。
		if item, hasDrop := core.BlockDrop(block); hasDrop {
			if _, leftover := body.Inventory.AddStack(core.ItemStack{Item: item, Count: 1}); leftover.Count != 0 {
				m.applyQueueEvents(slot, slot.queue.FailRun(companion.TaskFailInventoryFull))
				return
			}
		}
	}
	slot.mineProgress = observed.ProgressTicks
	slot.mineRequired = observed.RequiredTicks
	m.holdMining(slot, id, target)
}

// holdMining 提交一个采掘按住载荷并记录 sim 侧意图状态。sim 的按住语义
// 与玩家一致跨 tick 保持，直到 MineRelease——因此 miningHeld 标记必须与
// sim 侧意图严格同步，离开采掘步骤时由 releaseFinishedMining 释放。
func (m *companionManager) holdMining(
	slot *companionTaskSlot,
	id companion.ID,
	target core.BlockPos,
) {
	slot.miningHeld = true
	m.engine.EnqueueCompanionAction(sim.CompanionAction{
		ID: id, Kind: sim.CompanionActionMineHold, Target: target,
	})
}

// submitCompanionPlacement 是 place 步骤交互段的一 tick 编排：先验交互距离
// （C3 Ruling：sim 放置无距离校验，交互距离的唯一关卡在 Runner 侧），再提
// 交 Place 载荷，下一 tick 观察成交。
//
// 观察选择（放置成交的权威路径）：放置结算写方块发生在权威 tick 的区块
// 写入区，Manager 在下一 tick 边界经 CloneReadyChunk 读到的目标方块就是
// 结算后的权威值—— TickResult 的区块变更批次面向客户端发布按会话裁剪，
// 不如直接读目标格来得精确；这是 tick 边界的只读拷贝，与权威世界的变化
// 隔离，读值恰为「上一 tick 全部写入完成」的一致截面。
//
// 失败映射：目标非空气且不等于计划方块 → TaskFailWorldChanged（目标被占，
// 绝不覆盖）；背包无对应物品 → TaskFailInventoryFull（已成交的先前步骤
// 变更自然保留——失败只清任务槽，绝不回滚世界）；目标仍是空气且物品仍在
// → 上一 tick 的 Place 未成交（inbox 满员丢弃或校验瞬时未过），重新提交，
// deadline 兜底重试次数。
func (m *companionManager) submitCompanionPlacement(
	slot *companionTaskSlot,
	id companion.ID,
	body companion.Body,
	step companion.PlanStep,
) {
	target := core.BlockPos{X: step.X, Y: step.Y, Z: step.Z}
	block, ready := m.blockAt(body.Dimension, target)
	if !ready {
		// 目标区块未 ready：等待，deadline 兜底。
		return
	}
	if block == step.Block {
		// 成交观察：目标位置已是计划方块。正常路径是本方上一 tick 的 Place
		// 结算；极罕见的世界重合（其他 actor 放了同种方块）下可观察结果与
		// 计划一致，步骤目标同样达成，不再区分归属。
		m.applyQueueEvents(slot, slot.queue.CompleteStep())
		return
	}
	if block != core.AirID {
		// 目标被其他 actor 占据：按目标变化语义失败，绝不覆盖。
		m.applyQueueEvents(slot, slot.queue.FailRun(companion.TaskFailWorldChanged))
		return
	}
	item, hasDrop := core.BlockDrop(step.Block)
	if !hasDrop || !inventoryHoldsItem(body.Inventory, item) {
		// 物品耗尽（首个 place 步骤扣掉最后一件后到达这里的典型路径）：
		// 以稳定原因失败，已成交变更保留。
		m.applyQueueEvents(slot, slot.queue.FailRun(companion.TaskFailInventoryFull))
		return
	}
	if !withinInteractionReach(body, target) {
		// 邻近站立格仍超出交互距离（罕见几何边缘）：不提交，等待物理微调，
		// deadline 兜底。sim 不校验放置距离，这道门是唯一关卡。
		return
	}
	m.engine.EnqueueCompanionAction(sim.CompanionAction{
		ID: id, Kind: sim.CompanionActionPlace, Target: target, Block: step.Block,
	})
}

// releaseFinishedMining 是采掘意图的统一释放旁路：每 tick 检查所有标记了
// miningHeld 的槽位，任务已离开「正在执行的采掘步骤」（步骤推进、终态——
// 完成/失败/超时均清空当前槽位）时提交一次 MineRelease。
//
// 为什么必须显式释放：sim 的按住意图跨 tick 保持直到 Release（与玩家松开
// 语义对齐），而 Manager 每个采掘 tick 都重新提交同一目标的 MineHold——
// 一旦步骤结束而意图残留，miningTarget 指向的旧位置若日后被重新放置方块
// 且恰好进入 raycast 可达范围，权威模拟会在无人指挥下恢复采掘。释放动作
// 恰好占据步骤迁移 tick 的那一个 action 额度（该 tick 执行器不再提交
// MineHold），不与任何移动输入竞争。
func (m *companionManager) releaseFinishedMining() {
	for _, id := range m.orderedIDs {
		slot := m.slots[id]
		if !slot.miningHeld {
			continue
		}
		current, hasCurrent := slot.queue.Current()
		holding := hasCurrent &&
			current.State == companion.TaskRunning &&
			slot.interactionReady &&
			slot.interactStepIndex == current.StepIndex &&
			current.StepIndex < len(current.Plan.Steps) &&
			current.Plan.Steps[current.StepIndex].Kind == companion.PlanStepMine
		if holding {
			continue
		}
		slot.miningHeld = false
		m.engine.EnqueueCompanionAction(sim.CompanionAction{
			ID: id, Kind: sim.CompanionActionMineRelease,
		})
	}
}

// observeTickResult 缓存权威 TickResult 中的伙伴采掘进度，供下一 tick 的
// advanceRunners 消费。MiningUpdate 只经 TickResult 发布（CompanionBodies
// 不含采掘域），这是 sim→Manager 的唯一采掘进度通道；调用点（server.step
// 在 engine.Step 之后）持有 stepMu，缓存与 bodies 同属「上一 tick 末」的
// 一致观察截面——两者的世界时间基准严格一致，进度判定不会跨截面错配。
//
// 生命周期刻意与 refreshBodies 的每 tick clear 重建不对称：这里只覆写、
// 不删除，条目可能比激活状态存活更久。刻意不清理的原因：sim 的
// publishCompanions 与 CompanionBodies 同源遍历激活集合，TickResult 对每个
// 已激活伙伴必然携带其最新采掘事实；唯一读点 holdCompanionMining 的调用链
// 先经 advanceInteractionRunner 的 m.body 激活 gate——gate 放行即意味着
// 条目来自与 body 同一 tick 末快照的覆写，陈旧条目只会在伙伴离开激活集合
// 后残留，而那正是 gate 拦截的情形（当前引擎的 activate 单向、没有去激活
// 路径，残留仅是面向未来语义的结构余量）；条目总数又受注册伙伴上限封顶，
// 逐 tick 清理换不来任何可观察正确性。
func (m *companionManager) observeTickResult(result sim.TickResult) {
	for _, update := range result.Companions {
		m.mining[update.ID] = update.Mining
	}
}

// blockAt 读取目标方块的当前权威值：目标所在区块的 tick 边界只读拷贝
// （CloneReadyChunk 深拷贝后与权威世界后续变化隔离）。区块未 ready 时返回
// false——交互观察绝不基于半空世界裁决。每次调用恰好拷贝一个区块，交互
// 期间每伙伴每 tick 一次，成本有界。
func (m *companionManager) blockAt(dimension core.DimensionID, position core.BlockPos) (core.BlockID, bool) {
	chunk, _, ready := m.engine.CloneReadyChunk(core.ChunkKey{
		Dimension: dimension,
		Pos:       position.Chunk(),
	})
	if !ready {
		return 0, false
	}
	return chunk.BlockAt(
		int(position.X&core.SectionMask), position.Y, int(position.Z&core.SectionMask)), true
}

// withinInteractionReach 报告伙伴眼位到目标方块中心的直线距离是否落在交互
// 距离内。几何对齐 sim 采掘射线的锚点（眼位 → 方块中心）与同一
// InteractionReach 常量；遮挡不在此判定——放置目标必然是空气格，无遮挡
// 可言，距离是唯一需要 Runner 把关的自由度（C3 Ruling）。
func withinInteractionReach(body companion.Body, target core.BlockPos) bool {
	eyeY := body.Position[1] + physics.ActiveTunables().EyeHeight
	dx := body.Position[0] - (float32(target.X) + 0.5)
	dy := eyeY - (float32(target.Y) + 0.5)
	dz := body.Position[2] - (float32(target.Z) + 0.5)
	reach := sim.ActiveTunables().InteractionReach
	return dx*dx+dy*dy+dz*dz <= reach*reach
}

// inventoryHoldsItem 报告 36 格背包（统一索引）是否持有至少一件指定物品，
// 与 sim 放置扣料（consumeFirstInventoryItem 的首个对应物品堆）的持有判定
// 使用同一背包事实。
func inventoryHoldsItem(inventory core.Inventory, item core.ItemID) bool {
	for slot := uint8(0); slot < core.InventorySlots; slot++ {
		stack, ok := inventory.Slot(slot)
		if ok && stack.Item == item && stack.Count > 0 {
			return true
		}
	}
	return false
}

// interactionGoal 为 mine/place 步骤选择走近的固定终点。mine 的目标是实心
// 方块、place 的目标虽是空气但站进去会触发放置的碰撞拒绝——两者都必须停
// 在目标的相邻站立格。候选按「同层 → 上一层 → 下一层」与 -X/+X/-Z/+Z 的
// 固定顺序展开，取第一个满足站立条件的候选；全不合格（目标被围死等罕见
// 几何）时退回 (+X, 同层) 哨兵候选，交由寻路的三连失败语义以
// PathUnreachable 终结——终点的启发式选择永远不是权威，可达性由 FindPath
// 裁决。
func (m *companionManager) interactionGoal(body companion.Body, step companion.PlanStep) companion.PathCell {
	view := m.chunkViewAt(body.Dimension, body.Position)
	for _, dy := range [3]int32{0, 1, -1} {
		for _, direction := range [4][2]int32{{-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
			cell := companion.PathCell{
				X: step.X + direction[0],
				Y: step.Y + dy,
				Z: step.Z + direction[1],
			}
			if standingCellInView(view, cell) {
				return cell
			}
		}
	}
	return companion.PathCell{X: step.X + 1, Y: step.Y, Z: step.Z}
}

// standingCellInView 按 PathGrid.standing 的语义判定候选站立格：feet 与 head
// 可通过、正下方支撑实心。生产阻挡表只有空气可通过（见
// productionCompanionPassableBlocks），因此「可通过」在这里等价于「是空气」
// ——与寻路侧判定同义而不重复实现表格。区块未 ready 的列判为不站立：宁可
// 顺延到寻路阶段的重试语义，也不基于未知地形选终点。
func standingCellInView(view companionChunkView, cell companion.PathCell) bool {
	feet, ok := view.blockAt(cell.X, cell.Y, cell.Z)
	if !ok || feet != core.AirID {
		return false
	}
	head, ok := view.blockAt(cell.X, cell.Y+1, cell.Z)
	if !ok || head != core.AirID {
		return false
	}
	support, ok := view.blockAt(cell.X, cell.Y-1, cell.Z)
	return ok && support != core.AirID
}
