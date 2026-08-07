package core

// MaxHealth 是玩家生命值的权威上限；合法区间是 0..MaxHealth，满血为 20。
const MaxHealth uint8 = 20

// ValidHealth 判断生命值是否落在 0..MaxHealth 的合法区间内。
func ValidHealth(health uint8) bool {
	return health <= MaxHealth
}
