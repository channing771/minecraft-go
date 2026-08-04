package worldgen

import "minecraft-go/internal/core"

const (
	// 煤矿只出现在 Y<96 且约 1/2048 命中；铁矿只出现在 Y<48 且约 1/4096 命中。
	coalMaxY int32 = 96
	ironMaxY int32 = 48
	coalOdds       = 2048
	ironOdds       = 4096
	// salt 让同一坐标的两种矿石使用彼此独立的哈希序列。
	coalSalt uint64 = 0x9E3779B97F4A7C15
	ironSalt uint64 = 0xC2B2AE3D27D4EB4F
)

// oreHash 用世界种子、三维坐标和矿石 salt 生成稳定的 64 位混合值。
// 结果只依赖输入，与生成顺序、区块加载顺序和时间无关。
func oreHash(seed int64, pos core.BlockPos, salt uint64) uint64 {
	hash := uint64(seed) ^ salt
	for _, value := range [3]int64{int64(pos.X), int64(pos.Y), int64(pos.Z)} {
		hash ^= uint64(value) + 0x9E3779B97F4A7C15 + hash<<6 + hash>>2
		hash *= 0xFF51AFD7ED558CCD
		hash ^= hash >> 33
	}
	hash *= 0xC4CEB9FE1A85EC53
	hash ^= hash >> 33
	return hash
}
