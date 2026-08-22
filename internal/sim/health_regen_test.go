package sim

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
)

// readyRegenPlayer 构造一个已激活、生命值为 health 的玩家，站在安全的平坦地面上，
// 用于精确钉住自动回复的 tick 边界。
func readyRegenPlayer(t *testing.T, id SessionID, health uint8) *Engine {
	t.Helper()
	engine := NewEngine(0, 0, 0)
	current := PlayerLocation{
		Dimension: core.Overworld,
		Position:  mgl32.Vec3{2.5, 1, 0.5},
	}
	safe := current
	engine.RegisterPlayer(id, PlayerRestore{
		Current:        &current,
		Safe:           &safe,
		Health:         health,
		SpawnDimension: core.Overworld,
	})
	makeRestoreWorldReady(t, engine, current, safe)
	update := onlyPlayerUpdate(t, engine.Step(), id)
	if !update.Ready {
		t.Fatalf("玩家未激活: %+v", update)
	}
	player := engine.sessions[id].player
	if player.health != health {
		t.Fatalf("激活后 health=%d，想要 %d", player.health, health)
	}
	// 激活当 tick 也会推进一次回复计时；把它当作"最后一次受伤"的基线归零。
	player.resetRegenTimer()
	return engine
}

// stepRegen 推进 engine 若干 tick，只关心 result.Players 恰好一个的稳定场景。
func stepRegen(t *testing.T, engine *Engine, id SessionID, ticks int) {
	t.Helper()
	for range ticks {
		update := onlyPlayerUpdate(t, engine.Step(), id)
		if !update.Ready {
			t.Fatalf("玩家在推进过程中失去 Ready: %+v", update)
		}
	}
}

// TestHealthRegenDelayNinetyNineTicksNoHeal 覆盖"延迟期内不回复"场景：
// 受伤（此处以非满血基线代替）后第 99 tick 生命值必须保持不变。
func TestHealthRegenDelayNinetyNineTicksNoHeal(t *testing.T) {
	const id = SessionID(1)
	engine := readyRegenPlayer(t, id, 10)
	stepRegen(t, engine, id, 99)
	player := engine.sessions[id].player
	if player.health != 10 {
		t.Fatalf("99 tick 后 health=%d，想要保持 10", player.health)
	}
	if player.ticksSinceDamage != 99 {
		t.Fatalf("ticksSinceDamage=%d，想要 99", player.ticksSinceDamage)
	}
}

// TestHealthRegenTickOneHundredStillNoHeal 覆盖"第 100 tick 起进入回复阶段，
// 但尚未回复"场景：延迟满足的那一刻本身不产生回复。
func TestHealthRegenTickOneHundredStillNoHeal(t *testing.T) {
	const id = SessionID(2)
	engine := readyRegenPlayer(t, id, 10)
	stepRegen(t, engine, id, 100)
	player := engine.sessions[id].player
	if player.health != 10 {
		t.Fatalf("第 100 tick health=%d，想要保持 10", player.health)
	}
}

// TestHealthRegenDelaySatisfiedHealsAfterInterval 覆盖"延迟满足后按固定速率回复"
// 场景：已连续 100 tick 未受伤后再推进 40 tick，生命值必须恰好 +1。
// 边界严格钉在字面 139/140 tick 上（而不是只在 100+40 的和上检查）：
// 如果 RegenDelayTicks 或 RegenIntervalTicks 被改小 1，回复会提前发生在第 139
// tick，被下面第一个断言当场抓住；只在第 140 tick 检查末值会让这类变异悄悄漏网。
func TestHealthRegenDelaySatisfiedHealsAfterInterval(t *testing.T) {
	const id = SessionID(3)
	engine := readyRegenPlayer(t, id, 10)
	stepRegen(t, engine, id, 139)
	player := engine.sessions[id].player
	if player.health != 10 {
		t.Fatalf("第 139 tick health=%d，想要保持 10（回复不应提前发生）", player.health)
	}
	stepRegen(t, engine, id, 1)
	if player.health != 11 {
		t.Fatalf("第 140 tick health=%d，想要 11", player.health)
	}
}

// TestHealthRegenContinuesEveryIntervalUntilFull 覆盖持续回复直到满值：
// 每隔 RegenIntervalTicks 恰好回复 1 点，满值后即便继续推进也不再变化。
func TestHealthRegenContinuesEveryIntervalUntilFull(t *testing.T) {
	const id = SessionID(4)
	engine := readyRegenPlayer(t, id, core.MaxHealth-2)
	stepRegen(t, engine, id, 100)
	stepRegen(t, engine, id, 40)
	player := engine.sessions[id].player
	if player.health != core.MaxHealth-1 {
		t.Fatalf("第一次回复后 health=%d，想要 %d", player.health, core.MaxHealth-1)
	}
	stepRegen(t, engine, id, 40)
	if player.health != core.MaxHealth {
		t.Fatalf("第二次回复后 health=%d，想要 %d", player.health, core.MaxHealth)
	}
	stepRegen(t, engine, id, 120)
	if player.health != core.MaxHealth {
		t.Fatalf("满血后继续推进 health=%d，想要保持 %d", player.health, core.MaxHealth)
	}
}

// TestHealthRegenInterruptedByDamageResetsTimer 覆盖"受伤打断回复"场景：
// 回复进行中再次受伤，回复必须立即停止，且要重新连续 100 tick 才能再次开始。
func TestHealthRegenInterruptedByDamageResetsTimer(t *testing.T) {
	const id = SessionID(5)
	engine := readyRegenPlayer(t, id, 10)
	stepRegen(t, engine, id, 100)
	stepRegen(t, engine, id, 40)
	player := engine.sessions[id].player
	if player.health != 11 {
		t.Fatalf("回复中 health=%d，想要 11", player.health)
	}

	// 模拟一次受伤：直接调用伤害入口共用的计时重置。
	player.resetRegenTimer()

	stepRegen(t, engine, id, 99)
	if player.health != 11 {
		t.Fatalf("受伤后第 99 tick health=%d，想要保持 11", player.health)
	}
	stepRegen(t, engine, id, 1)
	if player.health != 11 {
		t.Fatalf("受伤后第 100 tick health=%d，想要保持 11", player.health)
	}
	// 同样把恢复后的第二次回复钉在字面 139/140 tick 边界上，而不是只在
	// 100+40 的和上检查，防止常量变小 1 时提前回复被漏判。
	stepRegen(t, engine, id, 39)
	if player.health != 11 {
		t.Fatalf("受伤后第 139 tick health=%d，想要保持 11（回复不应提前发生）", player.health)
	}
	stepRegen(t, engine, id, 1)
	if player.health != 12 {
		t.Fatalf("受伤后第 140 tick health=%d，想要 12", player.health)
	}
}

// TestHealthRegenFullHealthIsNoOp 覆盖"满血不回复"场景：满血玩家推进任意 tick，
// 生命值必须保持满值，且回复计时字段本身也不应该被推进（彻底 no-op，
// 不产生本可以避免的额外发布）。
func TestHealthRegenFullHealthIsNoOp(t *testing.T) {
	const id = SessionID(6)
	engine := readyRegenPlayer(t, id, core.MaxHealth)
	stepRegen(t, engine, id, 220)
	player := engine.sessions[id].player
	if player.health != core.MaxHealth {
		t.Fatalf("满血推进后 health=%d，想要 %d", player.health, core.MaxHealth)
	}
	if player.ticksSinceDamage != 0 {
		t.Fatalf("满血玩家的 ticksSinceDamage=%d，想要保持 0（未计时）", player.ticksSinceDamage)
	}
}

// TestApplyFallDamageResetsRegenTimer 覆盖摔落扣血这一既有伤害入口必须清零回复计时。
func TestApplyFallDamageResetsRegenTimer(t *testing.T) {
	const id = SessionID(7)
	engine := readyRegenPlayer(t, id, core.MaxHealth)
	stepRegen(t, engine, id, 50)
	player := engine.sessions[id].player
	if player.ticksSinceDamage != 0 {
		t.Fatalf("满血推进后 ticksSinceDamage=%d，想要保持 0", player.ticksSinceDamage)
	}

	player.health = 10
	player.ticksSinceDamage = 42
	player.peakY = player.state.Position.Y() + 10
	player.applyFallDamage()
	if player.health >= 10 {
		t.Fatalf("applyFallDamage 未扣血: health=%d", player.health)
	}
	if player.ticksSinceDamage != 0 {
		t.Fatalf("applyFallDamage 未清零回复计时: ticksSinceDamage=%d", player.ticksSinceDamage)
	}
}

// hungryRegenPlayer 在 readyRegenPlayer 的基础上把饥饿值改成 hunger，其余一切
// 保持逐字相同。饥饿 17 与 18 两条用例**共用这一个夹具、只差一个入参**：
// 门控用例最常见的假绿是两条用例各自搭一套夹具，然后差异其实来自别处。
func hungryRegenPlayer(t *testing.T, id SessionID, health, hunger uint8) *Engine {
	t.Helper()
	engine := readyRegenPlayer(t, id, health)
	engine.sessions[id].player.hunger = hunger
	return engine
}

// TestHealthRegenAtHungerThresholdHealsAndCostsExhaustion 覆盖 Scenario
// 「饥饿值 18 以上可回血」：饥饿恰好等于阈值时回血照常发生，并且**精确**累积
// 一次回血疲劳。
//
// 疲劳读数写死为跨过一次阈值之后的末值（6000 加进来、扣掉 4000 换 1 点饱和度、
// 余 2000），而不是「疲劳 > 0」：后者在「加了 6000 却一次阈值都没结算」的错误
// 实现下同样成立。
func TestHealthRegenAtHungerThresholdHealsAndCostsExhaustion(t *testing.T) {
	const id = SessionID(42)
	engine := hungryRegenPlayer(t, id, 10, 18)
	stepRegen(t, engine, id, 140)
	player := engine.sessions[id].player
	if player.health != 11 {
		t.Fatalf("饥饿 18 时第 140 tick health=%d，想要 11", player.health)
	}
	if player.exhaustionMilli != 2000 {
		t.Fatalf("回血后疲劳=%d，想要 2000（6000 减去一次 4000 阈值）", player.exhaustionMilli)
	}
	if player.saturationMilli != initialSaturationMilli-1000 {
		t.Fatalf("回血后饱和=%d，想要 %d（跨阈值扣 1 点）",
			player.saturationMilli, initialSaturationMilli-1000)
	}
	if player.hunger != 18 {
		t.Fatalf("回血后饥饿=%d，想要保持 18（饱和度尚未耗尽）", player.hunger)
	}
}

// TestHealthRegenBelowHungerThresholdDoesNotHeal 覆盖 Scenario「饥饿值 17
// 不回血」与 authoritative-health 的 MODIFIED Scenario「饥饿值不足时不回复」：
// 与上一条同夹具、只把饥饿从 18 改成 17。
//
// 夹具生命值刻意取 10 而不是满值：满血玩家门控开不开都不回血，那样的用例
// 在两种实现下读数相同，测不出门控。
func TestHealthRegenBelowHungerThresholdDoesNotHeal(t *testing.T) {
	const id = SessionID(43)
	engine := hungryRegenPlayer(t, id, 10, 17)
	stepRegen(t, engine, id, 140)
	player := engine.sessions[id].player
	if player.health != 10 {
		t.Fatalf("饥饿 17 时第 140 tick health=%d，想要保持 10", player.health)
	}
	if player.exhaustionMilli != 0 || player.saturationMilli != initialSaturationMilli {
		t.Fatalf("未回血却累积了疲劳: (疲劳,饱和)=(%d,%d)",
			player.exhaustionMilli, player.saturationMilli)
	}
	// 计时照常累积（design.md D3）：饥饿一旦回到阈值，已满的计时立即生效。
	if player.ticksSinceDamage != 140 {
		t.Fatalf("门控期间 ticksSinceDamage=%d，想要 140（计时不应被门控冻结）",
			player.ticksSinceDamage)
	}
}

// TestHealthRegenTimerSurvivesGateAndHealsOnceFed 钉住 D3 的另一半：门控只挡
// 「是否回复」，不挡计时。玩家在饥饿不足的状态下熬掉 139 tick，第 140 tick 前
// 一刻吃回阈值——回血必须**就在第 140 tick 发生**，而不是从头再等 100+40。
//
// 这条断言正是「计时被冻结」这个变异的杀手：计时若在门控期间停摆，第 140 tick
// 的计时读数只会是 1，离回复边界还差得远。
func TestHealthRegenTimerSurvivesGateAndHealsOnceFed(t *testing.T) {
	const id = SessionID(44)
	engine := hungryRegenPlayer(t, id, 10, 17)
	stepRegen(t, engine, id, 139)
	player := engine.sessions[id].player
	if player.health != 10 {
		t.Fatalf("门控期间 health=%d，想要保持 10", player.health)
	}
	if player.ticksSinceDamage != 139 {
		t.Fatalf("门控期间 ticksSinceDamage=%d，想要 139（计时不应被冻结）",
			player.ticksSinceDamage)
	}
	player.hunger = 18
	stepRegen(t, engine, id, 1)
	if player.health != 11 {
		t.Fatalf("饥饿回到阈值后第 140 tick health=%d，想要 11（已满的计时立即生效）",
			player.health)
	}
}

// TestHealthRegenExhaustionEventuallyDropsHunger 覆盖 Scenario「回血消耗最终
// 体现为饥饿下降」：饱和度为 0 时，第一次回血的疲劳直接扣在饥饿值上。
func TestHealthRegenExhaustionEventuallyDropsHunger(t *testing.T) {
	const id = SessionID(45)
	engine := hungryRegenPlayer(t, id, 10, core.MaxHunger)
	player := engine.sessions[id].player
	player.saturationMilli = 0
	stepRegen(t, engine, id, 140)
	if player.health != 11 {
		t.Fatalf("health=%d，想要 11", player.health)
	}
	if player.hunger != core.MaxHunger-1 {
		t.Fatalf("回血后饥饿=%d，想要 %d（饱和度为 0，疲劳直接扣饥饿）",
			player.hunger, core.MaxHunger-1)
	}
	if player.exhaustionMilli != 2000 {
		t.Fatalf("回血后疲劳=%d，想要 2000", player.exhaustionMilli)
	}
}
