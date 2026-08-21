package core

// IsCrop 报告 id 是否是作物方块（小麦的八个生长阶段之一）。
//
// 与 IsFluid 同形：作物阶段是一段连续的稳定编号，因此判定就是一次闭区间比较。
// 其余任何方块（包括耕地与未注册编号）均返回 false——**耕地不是作物**：耕地在
// 视觉上是满立方体、仍然不透明，而作物是交叉斜面的 cutout 类，两者的渲染、
// 光照与碰撞规则完全不同，混在一个谓词里会让调用方悄悄拿错规则。
func IsCrop(id BlockID) bool {
	return id >= WheatStage0ID && id <= WheatStage7ID
}

// CropStage 返回作物方块的生长阶段 0..7。
//
// 非作物编号的行为未定义，本实现返回 0——调用方 MUST 先用 IsCrop 判定。
func CropStage(id BlockID) uint8 {
	if !IsCrop(id) {
		return 0
	}
	return uint8(id - WheatStage0ID)
}
