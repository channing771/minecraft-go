package core

// MaxHunger 是玩家饥饿值的权威上限；合法区间是 0..MaxHunger，吃饱为 20。
//
// 与 MaxHealth 取同一个量级不是巧合：饥饿条与生命条在 HUD 上并排显示，同为
// 20 才能共用同一套「10 个图标、每个图标半格」的呈现刻度。
const MaxHunger uint8 = 20

// SaturationMilliPerPoint 是「一点饱和度」对应的千分位整数。
//
// 饱和度在权威侧全程以千分位整数存储（uint16，上界 MaxHunger×本常量 = 20000），
// 因为疲劳、饱和与食物恢复值在参考实现里都是小数，而本项目的权威推进**不用
// 浮点**：浮点在 Memory/TCP 两传输的重放一致与跨平台逐位相同这两条契约下不可
// 证。千分位足以无损表达全部数值（0.05、0.005、6.0），且 uint16 的动态范围
// 仍有三倍余量。
const SaturationMilliPerPoint uint16 = 1000

// InitialSaturationMilli 是新玩家、重生玩家与旧版玩家存档迁移共用的固定饱和度
// 初值（千分位）。
//
// 它放在 core 而不是 sim，是因为它同时是**存档契约**的一部分：不含饥饿字段的
// 旧版玩家存档要按它迁移，而 internal/storage 不依赖 internal/sim。两处各写一份
// 字面量会让同一份存档在"迁移路径"与"新玩家路径"上得到不同的饱和度。
//
// 它也不是 tunable：初值随配置漂移会让同一份旧存档在不同机器上迁出不同的状态。
const InitialSaturationMilli uint16 = 5 * SaturationMilliPerPoint

// ValidHunger 判断饥饿值是否落在 0..MaxHunger 的合法区间内。
func ValidHunger(hunger uint8) bool {
	return hunger <= MaxHunger
}

// FoodValue 返回可食物品吃下后恢复的饥饿点数与饱和度（千分位）；非食物返回
// (0, 0, false)。
//
// 它是食物的**唯一固定表**：进食状态机的准入判定（手持物是不是食物）、结算时
// 的恢复量、以及「非食物不可进食」的穷举守护都查这一处，不存在第二份数值。
//
// 目前只有面包一种食物：一种已经足以验证三层状态与进食状态机的全部路径，肉类
// 需要生物、熟食需要熔炉食谱，两者都不在当前范围内。新增食物是给本表加一行。
func FoodValue(item ItemID) (uint8, uint16, bool) {
	switch item {
	// 面包 (5, 6.0)：与参考实现同值。饱和度大于饥饿点数是刻意的——刚吃饱的
	// 玩家先烧饱和度、饥饿条才开始下降，这正是「吃饱之后有一段无损耗窗口」
	// 的来源。
	case ItemBread:
		return 5, 6 * SaturationMilliPerPoint, true
	default:
		return 0, 0, false
	}
}
