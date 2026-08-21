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

// IsFarmland 报告 id 是否是耕地方块（干耕地或湿耕地）。
//
// 与 IsCrop、IsFluid 同形的闭区间比较：两个耕地编号连续，且按方块演进纪律
// 只能整体追加，因此新增湿度态时只需推进上界。**耕地不是作物**——它是可站立
// 的实心方块（碰撞体只是顶面低 1/16），而作物零碰撞体，见 IsCrop 的说明。
func IsFarmland(id BlockID) bool {
	return id >= FarmlandDryID && id <= FarmlandWetID
}

// TillableBlock 报告方块能否被锄头翻成耕地：只有泥土与草。
//
// 耕地自身刻意不在此列——翻过的地不能再翻一次，否则同一格可以被反复翻地，
// 把锄头耐久无限消耗在一个不产生任何新状态的动作上。
//
// 权威模拟与客户端输入层共用这一份判定：客户端只用它决定「使用」键该发哪种
// 命令，真正的目标仍由服务端的权威射线重新判定，但两边的可翻集合必须是同一
// 个，否则客户端会对着一个服务端根本不接受的目标发翻地命令。
func TillableBlock(id BlockID) bool {
	return id == DirtID || id == GrassID
}

// TillingTool 报告物品是否是**还有耐久的**锄头，即能否用于翻地。
//
// 损坏形态（ItemBrokenStoneHoe / ItemBrokenIronHoe）刻意不在此列：它们是独立
// 物品编号，语义上等同空手——采掘规则对损坏的镐也是同一种对待。判定写成对两
// 个完好编号的显式枚举而不是「编号落在锄头区间内」，正是为了让损坏形态不会
// 因为编号相邻被顺带放行。
func TillingTool(item ItemID) bool {
	return item == ItemStoneHoe || item == ItemIronHoe
}
