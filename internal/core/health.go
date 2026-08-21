package core

// MaxHealth 是玩家生命值的权威上限；合法区间是 0..MaxHealth，满血为 20。
const MaxHealth uint8 = 20

// ValidHealth 判断生命值是否落在 0..MaxHealth 的合法区间内。
func ValidHealth(health uint8) bool {
	return health <= MaxHealth
}

// MaxOxygenTicks 是玩家氧气的权威上限，合法区间是 0..MaxOxygenTicks。
//
// 取 300 的理由：氧气以「眼睛浸没的 tick 数」计量，权威 tick 是 20 TPS，
// 300 tick 恰好等于 15 秒——足够玩家潜下去看清水底再浮上来，又短到让「憋不住气」
// 成为真实压力。它同时被选为 uint16 能表达且远小于上限的整数，wire 上占 2 字节。
//
// 氧气是纯瞬态权威值：不入玩家存档（schema 保持 v6），不进 PlayerHash，
// 眼睛离开流体即回满。
const MaxOxygenTicks uint16 = 300

// ValidOxygen 判断氧气是否落在 0..MaxOxygenTicks 的合法区间内。
func ValidOxygen(oxygen uint16) bool {
	return oxygen <= MaxOxygenTicks
}
