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
	engine := NewEngine(0, 0)
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
