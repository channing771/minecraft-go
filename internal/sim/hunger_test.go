package sim

import (
	"testing"

	"github.com/channing771/mornlea/internal/core"
)

// TestApplyExhaustionExhaustive 是疲劳结算的穷举表：三层状态的每种起始组合
// 乘上四种累加量，逐条断言**精确**的三个字段末值。
//
// 为什么必须逐条写死期望值而不是用一个「期望函数」算出来：期望函数只会是被测
// 实现的第二份拷贝，两份一起错时测试照样全绿。
//
// 四种累加量刻意跨越阈值的三个区间：0 与 3999 不跨（读数必须原样累加）、
// 4000 恰好跨一次、8000 跨两次。**只测不跨阈值的量是本变更最大的假绿点**：
// 疲劳不跨阈值时，「扣饱和」与「不扣饱和」两种实现的读数完全相同，差值恒等。
//
// 饥饿 0 配饱和 1000 是刻意的越界探针：它本身违反「饱和度 ≤ 饥饿值×1000」这条
// 不变量，放进表里是为了钉住 applyExhaustion 的分支次序——先看饱和度、饱和度
// 归零后才动饥饿值——与饥饿值当前取值无关。
func TestApplyExhaustionExhaustive(t *testing.T) {
	const threshold = defaultExhaustionThresholdMilli
	for _, tc := range []struct {
		name                           string
		hunger                         uint8
		saturation, exhaustion         uint16
		add                            uint16
		wantHunger                     uint8
		wantSaturation, wantExhaustion uint16
	}{
		// 饥饿 0 / 饱和 0：没有任何可消耗的资源，跨阈值只把疲劳清零。
		{"饥饿0饱和0加0", 0, 0, 0, 0, 0, 0, 0},
		{"饥饿0饱和0加3999", 0, 0, 0, 3999, 0, 0, 3999},
		{"饥饿0饱和0加4000", 0, 0, 0, 4000, 0, 0, 0},
		{"饥饿0饱和0加8000", 0, 0, 0, 8000, 0, 0, 0},

		// 饥饿 0 / 饱和 1000（越界探针）：饱和度仍然先被消耗。
		{"饥饿0饱和1000加0", 0, 1000, 0, 0, 0, 1000, 0},
		{"饥饿0饱和1000加3999", 0, 1000, 0, 3999, 0, 1000, 3999},
		{"饥饿0饱和1000加4000", 0, 1000, 0, 4000, 0, 0, 0},
		{"饥饿0饱和1000加8000", 0, 1000, 0, 8000, 0, 0, 0},

		// 饥饿 1 / 饱和 0：第一次跨阈值把饥饿值扣到 0，之后不再下降。
		{"饥饿1饱和0加0", 1, 0, 0, 0, 1, 0, 0},
		{"饥饿1饱和0加3999", 1, 0, 0, 3999, 1, 0, 3999},
		{"饥饿1饱和0加4000", 1, 0, 0, 4000, 0, 0, 0},
		{"饥饿1饱和0加8000", 1, 0, 0, 8000, 0, 0, 0},

		// 饥饿 1 / 饱和 1000：一次跨阈值只烧饱和度，两次才动饥饿值。
		{"饥饿1饱和1000加0", 1, 1000, 0, 0, 1, 1000, 0},
		{"饥饿1饱和1000加3999", 1, 1000, 0, 3999, 1, 1000, 3999},
		{"饥饿1饱和1000加4000", 1, 1000, 0, 4000, 1, 0, 0},
		{"饥饿1饱和1000加8000", 1, 1000, 0, 8000, 0, 0, 0},

		// 饥饿满 / 饱和 0：每次跨阈值恰好扣 1 点饥饿。
		{"饥饿20饱和0加0", 20, 0, 0, 0, 20, 0, 0},
		{"饥饿20饱和0加3999", 20, 0, 0, 3999, 20, 0, 3999},
		{"饥饿20饱和0加4000", 20, 0, 0, 4000, 19, 0, 0},
		{"饥饿20饱和0加8000", 20, 0, 0, 8000, 18, 0, 0},

		// 饥饿满 / 饱和 1 点：spec Scenario「疲劳先消耗饱和度」的直接编码。
		{"饥饿20饱和1000加0", 20, 1000, 0, 0, 20, 1000, 0},
		{"饥饿20饱和1000加3999", 20, 1000, 0, 3999, 20, 1000, 3999},
		{"饥饿20饱和1000加4000", 20, 1000, 0, 4000, 20, 0, 0},
		{"饥饿20饱和1000加8000", 20, 1000, 0, 8000, 19, 0, 0},

		// 饥饿满 / 饱和满：只烧饱和度，饥饿值纹丝不动。
		{"饥饿20饱和满加0", 20, 20000, 0, 0, 20, 20000, 0},
		{"饥饿20饱和满加3999", 20, 20000, 0, 3999, 20, 20000, 3999},
		{"饥饿20饱和满加4000", 20, 20000, 0, 4000, 20, 19000, 0},
		{"饥饿20饱和满加8000", 20, 20000, 0, 8000, 20, 18000, 0},

		// 不足一点的残余饱和度：整点扣减把它清零，且**不**顺手扣饥饿值
		// （一次跨阈值最多消耗一种资源）。第二次跨阈值才轮到饥饿值。
		{"残余饱和500加4000", 20, 500, 0, 4000, 20, 0, 0},
		{"残余饱和500加8000", 20, 500, 0, 8000, 19, 0, 0},

		// 起始疲劳非零：累加必须叠在既有读数之上，不是覆盖。
		{"起始3999加1恰好跨", 20, 1000, 3999, 1, 20, 0, 0},
		{"起始3999加0不跨", 20, 1000, 3999, 0, 20, 1000, 3999},
		{"起始2000加2001跨一次", 20, 1000, 2000, 2001, 20, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			player := &playerState{
				hunger:          tc.hunger,
				saturationMilli: tc.saturation,
				exhaustionMilli: tc.exhaustion,
			}
			player.applyExhaustion(tc.add, threshold)
			if player.hunger != tc.wantHunger ||
				player.saturationMilli != tc.wantSaturation ||
				player.exhaustionMilli != tc.wantExhaustion {
				t.Fatalf("applyExhaustion(%d) 后 (饥饿,饱和,疲劳)=(%d,%d,%d)，想要 (%d,%d,%d)",
					tc.add, player.hunger, player.saturationMilli, player.exhaustionMilli,
					tc.wantHunger, tc.wantSaturation, tc.wantExhaustion)
			}
		})
	}
}

// TestApplyExhaustionReadsThresholdFromParameter 钉住「阈值来自本 tick 的
// tunable 快照，不是写死的编译期常量」：同一个累加量在两个不同阈值下必须给出
// 不同的结果。若实现把 4000 写死，把阈值调到 8000 的那一半会当场变红。
func TestApplyExhaustionReadsThresholdFromParameter(t *testing.T) {
	small := &playerState{hunger: 20, saturationMilli: 5000}
	small.applyExhaustion(4000, 4000)
	if small.saturationMilli != 4000 || small.exhaustionMilli != 0 {
		t.Fatalf("阈值 4000 下 (饱和,疲劳)=(%d,%d)，想要 (4000,0)",
			small.saturationMilli, small.exhaustionMilli)
	}
	large := &playerState{hunger: 20, saturationMilli: 5000}
	large.applyExhaustion(4000, 8000)
	if large.saturationMilli != 5000 || large.exhaustionMilli != 4000 {
		t.Fatalf("阈值 8000 下 (饱和,疲劳)=(%d,%d)，想要 (5000,4000)：4000 不应跨过 8000 的阈值",
			large.saturationMilli, large.exhaustionMilli)
	}
}

// TestApplyExhaustionZeroThresholdDoesNotHang 是权威 tick 内的死循环兜底：
// 阈值 0 会让「while 疲劳 >= 阈值」永不退出。配置层已把下限钳到 1000，但 sim
// 按架构约束不得导入 config，那道钳制隔着一个包，本包必须自己兜底。
//
// 与 advanceOxygen 的 max(…, 1) 同形。
func TestApplyExhaustionZeroThresholdDoesNotHang(t *testing.T) {
	player := &playerState{hunger: 20, saturationMilli: 20000}
	player.applyExhaustion(3, 0)
	if player.exhaustionMilli != 0 {
		t.Fatalf("阈值 0 时疲劳=%d，想要被兜底阈值消化为 0", player.exhaustionMilli)
	}
	if player.saturationMilli != 17000 {
		t.Fatalf("阈值 0 兜底为 1 时饱和=%d，想要 17000（3 次跨阈值）", player.saturationMilli)
	}
}

// TestNewPlayerStartsFedAndUnexhausted 覆盖新玩家/重生的固定初值。
func TestNewPlayerStartsFedAndUnexhausted(t *testing.T) {
	const id = SessionID(41)
	engine := readyRegenPlayer(t, id, core.MaxHealth)
	player := engine.sessions[id].player
	if player.hunger != core.MaxHunger {
		t.Fatalf("新玩家饥饿=%d，想要 %d", player.hunger, core.MaxHunger)
	}
	if player.saturationMilli != core.InitialSaturationMilli {
		t.Fatalf("新玩家饱和=%d，想要 %d", player.saturationMilli, core.InitialSaturationMilli)
	}
	if player.exhaustionMilli != 0 || player.starvationTicks != 0 {
		t.Fatalf("新玩家 (疲劳,饥饿计时)=(%d,%d)，想要 (0,0)",
			player.exhaustionMilli, player.starvationTicks)
	}
}

// TestStarvationDamagesOncePerInterval 覆盖 Scenario「饥饿归零周期扣血」：
// 饥饿为 0 时每 StarvationDamageIntervalTicks 扣 1 点，且这一扣必须经既有伤害
// 入口——回血计时被清零就是"走了同一个入口"的可观察证据。
//
// 第 79 tick 的断言不可省：只在第 80 tick 检查末值，间隔被改小 1 的变异会
// 悄悄漏网。
func TestStarvationDamagesOncePerInterval(t *testing.T) {
	const id = SessionID(46)
	engine := readyRegenPlayer(t, id, 10)
	player := engine.sessions[id].player
	player.hunger = 0
	player.saturationMilli = 0

	stepRegen(t, engine, id, 79)
	if player.health != 10 {
		t.Fatalf("第 79 tick health=%d，想要保持 10（扣血不应提前）", player.health)
	}
	if player.starvationTicks != 79 {
		t.Fatalf("第 79 tick starvationTicks=%d，想要 79", player.starvationTicks)
	}
	stepRegen(t, engine, id, 1)
	if player.health != 9 {
		t.Fatalf("第 80 tick health=%d，想要 9", player.health)
	}
	if player.starvationTicks != 0 {
		t.Fatalf("扣血后 starvationTicks=%d，想要 0", player.starvationTicks)
	}
	// 走 applyDamage 的直接后果：回血计时被清零。
	if player.ticksSinceDamage != 0 {
		t.Fatalf("饥饿伤害未重置回血计时: ticksSinceDamage=%d", player.ticksSinceDamage)
	}
	// 第二个间隔照常扣。
	stepRegen(t, engine, id, 80)
	if player.health != 8 {
		t.Fatalf("第二个间隔后 health=%d，想要 8", player.health)
	}
}

// TestStarvationStopsAtOneHealth 覆盖 Scenario「饥饿伤害止于一点生命」与
// authoritative-health 的 MODIFIED Scenario「饥饿伤害经同一入口且止于 1」。
//
// 夹具从 health=2 起推进**两个**间隔：第一个把生命打到 1，第二个必须完全不动。
// 只推进一个间隔的用例测不出"止于"——那种夹具在"没有地板"的实现下读数相同。
func TestStarvationStopsAtOneHealth(t *testing.T) {
	const id = SessionID(47)
	engine := readyRegenPlayer(t, id, 2)
	player := engine.sessions[id].player
	player.hunger = 0
	player.saturationMilli = 0

	stepRegen(t, engine, id, 80)
	if player.health != 1 {
		t.Fatalf("第一个间隔后 health=%d，想要 1", player.health)
	}
	// 第二个间隔刻意**不**走 stepRegen：那个 helper 在玩家失去 Ready 时自己就
	// t.Fatalf 了，「饿死」这个违规会红在 helper 里，诊断信息指向「失去 Ready」
	// 而不是「止于 1」。这里直接推进，把红点留给下面两条显式断言。
	for range 80 {
		engine.Step()
	}
	if player.health != 1 || player.lifecycle != PlayerActive {
		t.Fatalf("第二个间隔后 (health,lifecycle)=(%d,%d)，想要 (1,Active)："+
			"饥饿伤害必须止于 1 点生命且不致死", player.health, player.lifecycle)
	}
	// 生命值触底后计时冻结，不是照推：否则一吃饱回血就会立刻结算一次积压伤害。
	if player.starvationTicks != 0 {
		t.Fatalf("触底后 starvationTicks=%d，想要冻结在 0", player.starvationTicks)
	}
	stepRegen(t, engine, id, 240)
	if player.health != 1 || player.lifecycle != PlayerActive {
		t.Fatalf("长时间饥饿后 (health,lifecycle)=(%d,%d)，想要 (1,Active)",
			player.health, player.lifecycle)
	}
}

// TestStarvationTimerResetsWhenFed 覆盖「饥饿值大于零时计时清零」：熬到间隔
// 前一刻吃回一点，已经积累的计时必须作废，不能在下一次饿到 0 时立刻结算。
func TestStarvationTimerResetsWhenFed(t *testing.T) {
	const id = SessionID(48)
	engine := readyRegenPlayer(t, id, 10)
	player := engine.sessions[id].player
	player.hunger = 0
	player.saturationMilli = 0

	stepRegen(t, engine, id, 79)
	if player.starvationTicks != 79 {
		t.Fatalf("starvationTicks=%d，想要 79", player.starvationTicks)
	}
	player.hunger = 1
	stepRegen(t, engine, id, 1)
	if player.starvationTicks != 0 || player.health != 10 {
		t.Fatalf("吃回一点后 (starvationTicks,health)=(%d,%d)，想要 (0,10)",
			player.starvationTicks, player.health)
	}
	player.hunger = 0
	stepRegen(t, engine, id, 79)
	if player.health != 10 {
		t.Fatalf("重新计时的第 79 tick health=%d，想要保持 10", player.health)
	}
	stepRegen(t, engine, id, 1)
	if player.health != 9 {
		t.Fatalf("重新计时的第 80 tick health=%d，想要 9", player.health)
	}
}

// TestRespawnRestoresHungerToInitialValues 覆盖 Scenario「重生后饥饿回满」。
// 死亡由既有伤害入口触发（不是直接写 health），因此这条同时证明死亡结算
// 复用了 resetHunger 这一个初值来源。
func TestRespawnRestoresHungerToInitialValues(t *testing.T) {
	const id = SessionID(49)
	engine := readyRegenPlayer(t, id, 10)
	player := engine.sessions[id].player
	player.hunger = 3
	player.saturationMilli = 0
	player.exhaustionMilli = 3000
	player.starvationTicks = 0
	player.applyDamage(int32(player.health))
	if player.health != 0 {
		t.Fatalf("致命伤后 health=%d，想要 0", player.health)
	}

	engine.Step()
	if player.hunger != core.MaxHunger {
		t.Fatalf("重生后饥饿=%d，想要 %d", player.hunger, core.MaxHunger)
	}
	if player.saturationMilli != core.InitialSaturationMilli {
		t.Fatalf("重生后饱和=%d，想要 %d", player.saturationMilli, core.InitialSaturationMilli)
	}
	if player.exhaustionMilli != 0 || player.starvationTicks != 0 {
		t.Fatalf("重生后 (疲劳,饥饿计时)=(%d,%d)，想要 (0,0)",
			player.exhaustionMilli, player.starvationTicks)
	}
}

// hungerReplayFingerprint 在一个全新引擎上跑完固定的输入脚本，返回三层饥饿
// 状态与生命值的指纹。脚本刻意把疲劳表的每一类来源都走一遍，其中游泳那一段
// 是唯一经过浮点换算的路径，也就是重放不一致最可能出现的地方。
func hungerReplayFingerprint(t *testing.T, id SessionID) [4]uint32 {
	t.Helper()
	engine := readyRegenPlayer(t, id, core.MaxHealth)
	player := engine.sessions[id].player

	// 第一段：平地起跳并落地。
	player.input.Jump = true
	engine.Step()
	player.input.Jump = false
	for range 20 {
		engine.Step()
	}

	// 第二段：泡进水里持续游动，走浮点位移换算。
	floodAroundPlayer(t, engine, player.state.Position)
	player.input.MoveX = 1
	player.input.MoveZ = -1
	for range 60 {
		engine.Step()
	}
	player.input.MoveX = 0
	player.input.MoveZ = 0

	// 第三段：饿到零并挨过若干饥饿伤害间隔（生命值先掉到中段，避免被回血门控
	// 与满血短路一起抹平差异）。
	player.hunger = 0
	player.saturationMilli = 0
	player.health = 10
	for range 200 {
		engine.Step()
	}

	return [4]uint32{
		uint32(player.hunger),
		uint32(player.saturationMilli),
		uint32(player.exhaustionMilli),
		uint32(player.health),
	}
}

// TestHungerReplayIsBitIdenticalAcrossEngines 覆盖 Scenario「相同输入重放逐位
// 一致」的 sim 层部分：两个**互相独立**的 Engine 吃同一串输入，三层饥饿状态与
// 生命值必须逐字段相同。两传输（Memory/TCP）的 parity 归后续任务组。
//
// 这条用例真正守的是「权威推进不用浮点」：状态本身全是整数，唯一的浮点是游泳
// 位移换算，且那一步立刻截断回整数。任何把浮点引进状态的改动都会让这里在某个
// 平台上开始抖动。
func TestHungerReplayIsBitIdenticalAcrossEngines(t *testing.T) {
	first := hungerReplayFingerprint(t, SessionID(61))
	second := hungerReplayFingerprint(t, SessionID(61))
	if first != second {
		t.Fatalf("两次重放的 (饥饿,饱和,疲劳,生命) 不一致: %v vs %v", first, second)
	}
	// 夹具自证：脚本必须真的把状态推离初值，否则「两次都等于初值」也会通过。
	initial := [4]uint32{
		uint32(core.MaxHunger), uint32(core.InitialSaturationMilli), 0, uint32(core.MaxHealth),
	}
	if first == initial {
		t.Fatalf("重放脚本没有改变任何状态: %v", first)
	}
}
