package fluid

import "github.com/channing771/mornlea/internal/core"

// Replaceable 报告：若把流体等级为 newLevel 的流动水（newLevel 取 1..7）写入
// target 所在的格，target 现有的内容是否允许被改写。
//
// 判定表（对应 task-2-brief.md 2.1 与 spec.md「流动规则」的多条 Scenario）：
//
//	目标格                        可替换？
//	空气                          是
//	流体等级更大（更弱）的流动水    是
//	源方块                        否——「源不可被流动方块替换」
//	流体等级更小或相等的流动水      否——防止弱水倒灌强水，也防止无意义的同值改写
//	任意其他非空气方块（实心）      否——「实心方块不可替换」
//
// newLevel 由调用方按当前正在做的传播（垂直恒为 1，水平为 N+1）算出，本函数
// 不关心它是如何得来的，只做纯粹的比较。
func Replaceable(target core.BlockID, newLevel uint8) bool {
	if target == core.AirID {
		return true
	}
	if !core.IsFluid(target) {
		// 非空气、非流体：实心方块，一律不可替换。
		return false
	}
	if target == core.WaterSourceID {
		// 源方块的流体等级读作 0，若不特判会被 0 > newLevel 误判为不可替换——
		// 语义上确实不可替换，但要靠这条显式分支表达“源永不可替换”这条独立
		// 规则，而不是恰好靠等级比较凑对。
		return false
	}
	return core.FluidLevel(target) > newLevel
}

// horizontalNeighbors 返回 pos 的四个水平相邻格（不含上下）。
func horizontalNeighbors(pos core.BlockPos) [4]core.BlockPos {
	return [4]core.BlockPos{
		{X: pos.X + 1, Y: pos.Y, Z: pos.Z},
		{X: pos.X - 1, Y: pos.Y, Z: pos.Z},
		{X: pos.X, Y: pos.Y, Z: pos.Z + 1},
		{X: pos.X, Y: pos.Y, Z: pos.Z - 1},
	}
}

// sixNeighbors 返回 pos 的六个面邻格（上下 + 四个水平方向）。
func sixNeighbors(pos core.BlockPos) [6]core.BlockPos {
	h := horizontalNeighbors(pos)
	return [6]core.BlockPos{
		{X: pos.X, Y: pos.Y + 1, Z: pos.Z},
		{X: pos.X, Y: pos.Y - 1, Z: pos.Z},
		h[0], h[1], h[2], h[3],
	}
}

// flowingSurvives 判定非源流动格 pos（当前方块 self，流体等级为
// core.FluidLevel(self)）在本 tick 是否继续存活：上方是任意流体，或水平邻居
// 中存在流体等级严格小于自身的流体（源的等级读作 0，天然满足“更小”）。
//
// 只调用一次 w.BlockAt 读每个邻居，且只在 Advance 提交写入之前被调用，因此
// 读到的都是本 tick 起始时的世界状态——这是避免同 tick 内读写交错导致振荡的
// 关键（design.md 的 Risk「流动规则的存活判定产生振荡」）。
func flowingSurvives(pos core.BlockPos, self core.BlockID, w FluidWorld) bool {
	level := core.FluidLevel(self)
	above := core.BlockPos{X: pos.X, Y: pos.Y + 1, Z: pos.Z}
	if core.IsFluid(w.BlockAt(above)) {
		return true
	}
	for _, n := range horizontalNeighbors(pos) {
		nb := w.BlockAt(n)
		if core.IsFluid(nb) && core.FluidLevel(nb) < level {
			return true
		}
	}
	return false
}

// evalCell 对 pos 求值一次完整的单格流动规则（spec.md「流动规则」全部
// Scenario），返回本次求值想要做出的写入集合：key 是目标格，value 是新
// 方块编号。返回空 map 表示本次求值不产生任何变化。
//
// evalCell 本身不写入 w，只读取——调用方（Queue.Advance）负责把多次 evalCell
// 的结果合并后一次性提交，从而保证本 tick 内的存活/替换判定全部只看 tick
// 起始时的状态。
func evalCell(pos core.BlockPos, w FluidWorld) map[core.BlockPos]core.BlockID {
	writes := make(map[core.BlockPos]core.BlockID)

	self := w.BlockAt(pos)
	if !core.IsFluid(self) {
		// 队列里的格在真正被处理前可能已经因为别的原因变成非流体（比如被
		// 玩家挖掉后又放了实心方块）；这种陈旧待更新项直接跳过，不产生变化。
		return writes
	}

	if self != core.WaterSourceID {
		// 规则「源方块永不自然消失」+「流动方块失去支撑后消失」：只有非源
		// 流动格才需要做存活判定。
		if !flowingSurvives(pos, self, w) {
			writes[pos] = core.AirID
			return writes // 本格本 tick 消失，不再谈传播。
		}
	}

	// 规则「垂直优先」：下方可替换时只向下写最强流动水（等级 1），本次
	// MUST NOT 再向任何水平方向传播。
	below := core.BlockPos{X: pos.X, Y: pos.Y - 1, Z: pos.Z}
	if Replaceable(w.BlockAt(below), 1) {
		writes[below] = core.WaterLevel1ID
		return writes
	}

	// 规则「水平传播递减」+「水平传播上界」：下方不可替换时才水平扩散，
	// 等级从当前格的等级 +1；源的等级读作 0，其水平邻居因此得到等级 1。
	nextLevel := core.FluidLevel(self) + 1
	if nextLevel > 7 {
		// 等级 7 已是传播下界，世界中不得出现等级 > 7 的流体方块。
		return writes
	}
	nextID := core.WaterSourceID + core.BlockID(nextLevel)
	for _, n := range horizontalNeighbors(pos) {
		if Replaceable(w.BlockAt(n), nextLevel) {
			writes[n] = nextID
		}
	}
	return writes
}
