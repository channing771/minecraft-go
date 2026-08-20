package core

// IsFluid 报告 id 是否是流体方块（源方块或流动方块）。
// 流体方块只包含 WaterSourceID 与 WaterLevel1ID..WaterLevel7ID 这 8 个稳定
// 编号，其余任何方块（包括未注册编号）均返回 false。
func IsFluid(id BlockID) bool {
	return id >= WaterSourceID && id <= WaterLevel7ID
}

// FluidLevel 返回流体方块的水量等级：源方块为 0，流动方块为 1..7（数字越大
// 水量越弱）。非流体编号（含未注册编号）的行为未定义，本实现返回 0——调用方
// MUST 先用 IsFluid 判定，再据此解释 FluidLevel 的返回值；对非流体编号误用
// FluidLevel 不会 panic，但其返回值没有流体语义。
func FluidLevel(id BlockID) uint8 {
	if id == WaterSourceID || !IsFluid(id) {
		return 0
	}
	return uint8(id - WaterSourceID)
}
