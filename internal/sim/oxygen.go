package sim

import "github.com/channing771/mornlea/internal/core"

// defaultDrownDamageIntervalTicks 是氧气归零后两次溺水伤害之间的 tick 数。
// 唯一读取入口是 Tunables 快照，不得再以导出常量暴露——见 internal/archcheck
// 的 TestTunableConstantsAreNotExported。
//
// 取 20 的理由：权威 tick 是 20 TPS，20 tick 恰好一秒，也就是「氧气见底后每秒
// 掉一点血」。满血 core.MaxHealth（20）因此还有 20 秒可以自救，比
// core.MaxOxygenTicks 本身的 15 秒更长——憋气见底不等于必死，玩家仍有充裕时间
// 浮出水面，与摔落伤害那种一次结清的曲线刻意不同。
const defaultDrownDamageIntervalTicks = 20

// advanceOxygen 推进一名玩家一个 tick 的氧气与溺水结算，与 advanceHealthRegen
// 同形：固定整数运算、不分配、由调用方传入本 tick 的 tunable 快照值。
//
// 规则（spec fluid-survival「眼睛浸没消耗氧气，耗尽后按固定间隔造成伤害」）：
//   - 眼睛不在流体里：氧气立即回满、溺水计时清零。这条是**无条件**的，
//     不是「每 tick 回复一点」——出水那一 tick 就恢复满值。
//   - 眼睛在流体里且氧气尚有余量：每 tick 扣 1。
//   - 氧气已归零：每 drownDamageIntervalTicks 个 tick 走一次 applyDamage(1)。
//
// 溺水伤害必须经 applyDamage 这个既有伤害入口，不能就地扣 health：只有走那里
// 才会重置自动回复计时（与摔落伤害等其他来源一致），生命值归零后也才由本 tick
// 稍后的 settleDeaths 统一做死亡与重生结算。
//
// oxygen 是纯瞬态权威字段：不持久化、不进入快照/哈希，重启后由 RegisterPlayer
// 直接初始化为满值。
func (player *playerState) advanceOxygen(eyeInFluid bool, drownDamageIntervalTicks uint32) {
	if !eyeInFluid {
		player.oxygen = core.MaxOxygenTicks
		player.drownTicks = 0
		return
	}
	if player.oxygen > 0 {
		player.oxygen--
		return
	}
	player.drownTicks++
	// 间隔取 max(…, 1)：配置层（internal/config 的 Fields）已把下限钳到 1，
	// 但 sim 按架构约束不得导入 config，那道钳制隔着一个包；这里兜底避免
	// 间隔为 0 时退化成「每 tick 扣血」。
	if player.drownTicks >= max(drownDamageIntervalTicks, 1) {
		player.drownTicks = 0
		player.applyDamage(1)
	}
}
