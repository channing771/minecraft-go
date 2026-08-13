package sim

import "github.com/channing771/mornlea/internal/core"

// defaultRegenDelayTicks 是最后一次受伤后必须连续经过的 tick 数才进入回复阶段。
// 唯一读取入口是 Tunables 快照，不得再以导出常量暴露——见 internal/archcheck
// 的 TestTunableConstantsAreNotExported。
const defaultRegenDelayTicks = 100

// defaultRegenIntervalTicks 是进入回复阶段后，每回复 1 点生命值需要经过的 tick 数。
// 唯一读取入口同上。
const defaultRegenIntervalTicks = 40

// advanceHealthRegen 推进玩家一个 tick 的自动回复计时，与熔炉推进（advanceFurnace）
// 同形：固定整数运算、不分配、返回是否发生了可观察变化（本 tick 是否回复了 1 点）。
// 满血玩家直接短路，不计时也不回复，确保满血 tick 是彻底的 no-op。
//
// regenDelayTicks、regenIntervalTicks 由调用方传入本 tick 的快照值；playerState 没有
// 引擎引用，这个方法本身绝不读取 ActiveTunables。
//
// 除零安全依赖 regenIntervalTicks 已被钳制在 >= 1：下面的取模运算以它为除数，
// 未钳制的 0 会在此处触发权威 tick 内 panic。该钳制由本包的 SetTunables 兜底
// （internal/config 加载配置时也会钳一遍，但 sim 按架构约束不得导入 config，
// 不能把不变量托付给隔壁包）。
func (player *playerState) advanceHealthRegen(regenDelayTicks, regenIntervalTicks uint32) bool {
	if player.health >= core.MaxHealth {
		return false
	}
	player.ticksSinceDamage++
	if player.ticksSinceDamage <= regenDelayTicks {
		return false
	}
	if (player.ticksSinceDamage-regenDelayTicks)%regenIntervalTicks != 0 {
		return false
	}
	player.health++
	return true
}

// resetRegenTimer 把受伤计时清零并中断正在进行的回复。任何伤害结算都必须调用它；
// 第 5 组的死亡结算也会复用这个入口重置计时。
func (player *playerState) resetRegenTimer() {
	player.ticksSinceDamage = 0
}
