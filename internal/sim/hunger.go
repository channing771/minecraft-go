package sim

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
)

// defaultStarvationDamageIntervalTicks 是饥饿值归零后两次饥饿伤害之间的 tick 数。
// 唯一读取入口是 Tunables 快照，不得再以导出常量暴露——见 internal/archcheck
// 的 TestTunableConstantsAreNotExported。
//
// 取 80 的理由：权威 tick 是 20 TPS，80 tick 是 4 秒。饥饿伤害止于 1 点生命，
// 所以它不是死亡倒计时而是「一直饿着就别想打架」的压力源；比溺水（每秒 1 点）
// 慢四倍，因为断粮是可以边走边解决的问题，溺水不是。
const defaultStarvationDamageIntervalTicks = 80

// defaultExhaustionThresholdMilli 是疲劳值累积到多少（千分位）就结算一次消耗。
// 唯一读取入口同上。
//
// 取 4000（即 4.0）与参考实现同值：它决定了「跳 80 次」或「挖 800 个方块」
// 换掉一点饱和度，是整条饥饿曲线的时间刻度。
const defaultExhaustionThresholdMilli = 4000

// defaultRegenHungerThreshold 是允许自然回血的最低饥饿值。唯一读取入口同上。
//
// 取 18 与参考实现同值：它把「回满 20 点血」的代价定成 20×6000 = 120000 千分位
// 疲劳，也就是 30 次阈值结算——玩家必须至少吃回 18 才可能回血，而回血本身又会
// 把饥饿值推回 18 以下。这个反馈环就是本变更要交付的「回血有代价」。
const defaultRegenHungerThreshold = 18

// 疲劳来源固定表（单位：千分位）。
//
// 这五个数值**刻意不做 tunable**：它们之间的比例关系就是玩法本身（跳一次抵
// 十次挖掘、回一点血抵 120 次挖掘），逐个可调只会制造互相矛盾的配置组合，
// 而任何一种组合都没有对应的验收标准。新增疲劳来源是给这张表加一行，
// 并在对应的**成功路径**上调用 applyExhaustion。
//
// 全部判定点都只在玩家路径上：伙伴没有饥饿，也没有任何疲劳来源。
const (
	// exhaustionJumpMilli 是一次起跳的疲劳。判定点：advanceActivePlayers 的
	// 物理步边沿（见那里的起跳判据说明）。
	exhaustionJumpMilli uint16 = 50
	// exhaustionSwimMilliPerBlock 是身体浸没时每移动一格水平距离的疲劳。
	// 判定点同上，按步前步后的水平位移换算。
	exhaustionSwimMilliPerBlock = 10
	// exhaustionMiningMilli 是一次采掘完成的疲劳。判定点：advanceMining 的
	// 玩家完成分叉（被拒绝或中断的采掘不累积）。
	exhaustionMiningMilli uint16 = 5
	// exhaustionTillMilli 是一次翻地完成的疲劳。判定点：executeTillSoil 的
	// 唯一写入区（六道校验全过之后）。
	exhaustionTillMilli uint16 = 5
	// exhaustionRegenPerHealthMilli 是自然回复 1 点生命值的疲劳。判定点：
	// advanceHealthRegen 报告本 tick 回复了 1 点之后。
	//
	// 它比阈值（4000）大，因此**一次调用会跨过多个阈值**——applyExhaustion
	// 必须循环处理，只减一次阈值会让回血的代价凭空少掉三分之一。
	exhaustionRegenPerHealthMilli uint16 = 6000
)

// swimExhaustionMilli 把一次物理步的水平位移换算成游泳疲劳（千分位）。
//
// 这里是本变更**唯一**出现浮点的地方：物理体的位置本来就是 float32，而三层
// 饥饿状态全是整数，两者之间必须有一次取整。取整只做这一次，结果立刻回到整数
// 域，浮点绝不进入状态本身。
//
// 取整规则是「先放大到千分位再整除，余数丢弃」：
//
//	milli = floor(距离 × 1000 × exhaustionSwimMilliPerBlock) / 1000
//
// 也就是向下取整到 1 千分位疲劳。**绝不四舍五入**：物理步每 tick 都会有极小的
// 位置抖动，四舍五入会让原地泡在水里的玩家凭空积累疲劳。
//
// 只算水平分量：垂直方向的沉浮不是「游」，否则站在水里被水流托着上下也要付
// 代价。极端位移（传送、异常状态）被钳到 uint16 上界而不是回绕。
func swimExhaustionMilli(before, after mgl32.Vec3) uint16 {
	dx := float64(after.X()) - float64(before.X())
	dz := float64(after.Z()) - float64(before.Z())
	distance := math.Sqrt(dx*dx + dz*dz)
	// 非正与 NaN 一并挡在这里：NaN 的任何比较都为假，因此 !(distance > 0) 才是
	// 正确的写法，写成 distance <= 0 会让 NaN 漏进下面的换算。
	if !(distance > 0) {
		return 0
	}
	scaled := distance * 1000 * exhaustionSwimMilliPerBlock
	if scaled >= float64(math.MaxUint16)*1000 {
		return math.MaxUint16
	}
	return uint16(int64(scaled) / 1000)
}

// applyExhaustion 累积疲劳并按固定规则结算跨阈值的消耗。
//
// 规则（spec authoritative-hunger「饥饿由三层权威状态表示且全为整数」）：
// 疲劳每累积满一个阈值，就减去一个阈值并消耗一点饱和度；饱和度已经为零时改为
// 消耗一点饥饿值；饥饿值也为零时什么都不消耗（饥饿值不低于零）。
//
// **一次调用可能跨过多个阈值**（回血一次就加 6000，而阈值只有 4000），因此这里
// 是循环而不是单次判断。累加在 uint32 上做：调用方给出的量在极端配置下可能让
// uint16 中间值溢出，而溢出会静默变成一个小得多的疲劳量。
//
// 一次跨阈值最多消耗**一种**资源：残余不足一点的饱和度被清零后，本次结算就结束，
// 不会顺手再扣一点饥饿值——与参考实现一致，也让「饱和度耗尽」成为一个可观察的
// 分界点而不是瞬间穿过的中间态。
//
// thresholdMilli 由调用方传入本 tick 的 tunable 快照值；playerState 没有引擎
// 引用，本方法绝不读取 ActiveTunables。阈值取 max(…, 1)：配置层已把下限钳到
// 1000，但 sim 按架构约束不得导入 config，阈值为 0 会让下面的循环在权威 tick
// 内永不退出——那比 panic 更难诊断。
func (player *playerState) applyExhaustion(milli, thresholdMilli uint16) {
	threshold := uint32(max(thresholdMilli, 1))
	total := uint32(player.exhaustionMilli) + uint32(milli)
	for total >= threshold {
		total -= threshold
		switch {
		case player.saturationMilli >= core.SaturationMilliPerPoint:
			player.saturationMilli -= core.SaturationMilliPerPoint
		case player.saturationMilli > 0:
			player.saturationMilli = 0
		case player.hunger > 0:
			player.hunger--
		}
	}
	player.exhaustionMilli = uint16(total)
}

// advanceStarvation 推进一名玩家一个 tick 的饥饿伤害结算，与 advanceOxygen
// 同形：固定整数运算、不分配、由调用方传入本 tick 的 tunable 快照值。
//
// 规则（spec authoritative-hunger「饥饿归零按固定间隔扣血但不致死」）：
//   - 饥饿值大于零：计时清零，什么都不发生。
//   - 饥饿值为零且生命值大于 1：每 intervalTicks 个 tick 走一次 applyDamage(1)。
//   - 饥饿值为零但生命值不高于 1：**计时也不推进**。计时冻结而不是照推是刻意的：
//     否则玩家在 1 点血上饿着熬过若干间隔后一吃饱、一挨打回到 2 点血，积攒的
//     计时会立刻结算掉一次伤害，读起来像是"隔着时间打了一拳"。
//
// 饥饿伤害必须经 applyDamage 这个既有伤害入口，不能就地扣 health：只有走那里
// 才会重置自动回复计时（否则玩家一边饿一边回血），也才会触发客户端的确认伤害
// 反馈。饥饿伤害**不致死**——生命值 1 点是硬地板，因此它永远不会把玩家送进
// settleDeaths。
//
// 与氧气同理，饥饿伤害计时是纯瞬态字段，不持久化、不进入快照/哈希。
func (player *playerState) advanceStarvation(intervalTicks uint32) {
	if player.hunger > 0 {
		player.starvationTicks = 0
		return
	}
	if player.health <= 1 {
		return
	}
	player.starvationTicks++
	// 间隔取 max(…, 1)：理由同 advanceOxygen——配置层的下限钳制隔着一个包，
	// 间隔为 0 会退化成「每 tick 扣血」。
	if player.starvationTicks >= max(intervalTicks, 1) {
		player.starvationTicks = 0
		player.applyDamage(1)
	}
}

// resetHunger 把三层饥饿状态与饥饿伤害计时置回固定初值。
//
// 它是初值的**唯一**来源：注册新玩家与死亡结算各调用一次，两处因此不可能给出
// 不同的初值。位置跳变（掉出世界、维度 reset）刻意**不**调用它——那条路径复用
// 的是 beginReset，把它也当成"重生"会让"跳进虚空"变成一次免费的饱餐。
func (player *playerState) resetHunger() {
	player.hunger = core.MaxHunger
	player.saturationMilli = core.InitialSaturationMilli
	player.exhaustionMilli = 0
	player.starvationTicks = 0
}
