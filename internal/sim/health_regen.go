package sim

import "minecraft-go/internal/core"

// RegenDelayTicks 是最后一次受伤后必须连续经过的 tick 数才进入回复阶段。
const RegenDelayTicks = 100

// RegenIntervalTicks 是进入回复阶段后，每回复 1 点生命值需要经过的 tick 数。
const RegenIntervalTicks = 40

// advanceHealthRegen 推进玩家一个 tick 的自动回复计时，与熔炉推进（advanceFurnace）
// 同形：固定整数运算、不分配、返回是否发生了可观察变化（本 tick 是否回复了 1 点）。
// 满血玩家直接短路，不计时也不回复，确保满血 tick 是彻底的 no-op。
func (player *playerState) advanceHealthRegen() bool {
	if player.health >= core.MaxHealth {
		return false
	}
	player.ticksSinceDamage++
	if player.ticksSinceDamage <= RegenDelayTicks {
		return false
	}
	if (player.ticksSinceDamage-RegenDelayTicks)%RegenIntervalTicks != 0 {
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
