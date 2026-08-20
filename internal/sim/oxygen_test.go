package sim

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/physics"
)

// drownColumn 是溺水用例统一使用的玩家所在列（readyRegenPlayer 把玩家放在
// {2.5, 1, 0.5}）。水填在 y=1..2 两格：默认眼高 1.62 使眼睛落在 y=2 格，
// 身体 AABB 覆盖 y=1..2，因此身体与眼睛同时浸没。
var drownColumn = mgl32.Vec3{2.5, 1, 0.5}

// submergePlayerColumn 把玩家所在列的 y=1..2 改成水源。
//
// 它刻意在玩家已经 Ready 之后调用：出生/恢复校验只认碰撞几何，而流体没有碰撞盒，
// 提前放水不会改变校验结果，但放在之后能让用例与出生路径完全解耦。
func submergePlayerColumn(t *testing.T, engine *Engine, position mgl32.Vec3) {
	t.Helper()
	setColumn(t, engine, position, core.WaterSourceID)
}

// drainPlayerColumn 把同一列恢复成空气，用来观察「眼睛离开流体」这一边沿。
func drainPlayerColumn(t *testing.T, engine *Engine, position mgl32.Vec3) {
	t.Helper()
	setColumn(t, engine, position, core.AirID)
}

func setColumn(t *testing.T, engine *Engine, position mgl32.Vec3, block core.BlockID) {
	t.Helper()
	base := core.BlockPos{
		X: int32(math.Floor(float64(position.X()))),
		Y: 1,
		Z: int32(math.Floor(float64(position.Z()))),
	}
	record := engine.dimensions[core.Overworld].records[base.Chunk()]
	if record == nil || record.Chunk == nil {
		t.Fatalf("列 %+v 所在区块未就绪", base)
	}
	x, _, z := base.Local()
	for y := int32(1); y <= 2; y++ {
		record.Chunk.SetBlock(x, y, z, block)
	}
}

// TestOxygenDrainsWhileEyeSubmergedWithoutDamage 覆盖 spec Scenario「水下氧气递减」：
// 眼睛持续浸没时氧气逐 tick 递减，且在归零之前不得受到任何溺水伤害。
func TestOxygenDrainsWhileEyeSubmergedWithoutDamage(t *testing.T) {
	const id = SessionID(20)
	engine := readyRegenPlayer(t, id, 10)
	submergePlayerColumn(t, engine, drownColumn)
	player := engine.sessions[id].player
	if player.oxygen != core.MaxOxygenTicks {
		t.Fatalf("入水前 oxygen=%d，想要 %d", player.oxygen, core.MaxOxygenTicks)
	}

	// 逐 tick 观察：氧气必须每 tick 恰好 −1，而不是「若干 tick 后总量对得上」。
	lowest := uint8(10)
	for tick := 1; tick <= int(core.MaxOxygenTicks); tick++ {
		stepRegen(t, engine, id, 1)
		if want := core.MaxOxygenTicks - uint16(tick); player.oxygen != want {
			t.Fatalf("第 %d tick oxygen=%d，想要 %d", tick, player.oxygen, want)
		}
		lowest = min(lowest, player.health)
	}
	// 归零之前一次都不能掉血。自动回复会让生命值上升，因此这里比的是历史最低值。
	if lowest != 10 {
		t.Fatalf("氧气归零前生命值曾跌到 %d，想要保持 10（不得有溺水伤害）", lowest)
	}
	if player.oxygen != 0 {
		t.Fatalf("第 %d tick oxygen=%d，想要 0", core.MaxOxygenTicks, player.oxygen)
	}

	// 夹具承重守卫排在真实断言之后：这一列必须真的浸没了眼睛，否则上面整段
	// 只是在验证「不在水里的玩家氧气满」，把 advanceOxygen 删掉也照样绿。
	if _, eyeInFluid := playerSubmersion(engine, id); !eyeInFluid {
		t.Fatal("夹具无效：玩家眼睛并未浸没，本用例观察不到任何氧气消耗")
	}
}

// TestDrowningDamagesEveryIntervalAndResetsRegenTimer 覆盖 spec Scenario
// 「氧气耗尽后按间隔扣血」：按固定 tick 间隔扣 1 点，且每次扣血都重置自动回复计时。
//
// 计时断言是本组变异验证 1 的落点：溺水伤害一旦绕开 applyDamage 直接改 health，
// ticksSinceDamage 就不会归零，这里当场变红。
func TestDrowningDamagesEveryIntervalAndResetsRegenTimer(t *testing.T) {
	const id = SessionID(21)
	engine := readyRegenPlayer(t, id, 10)
	submergePlayerColumn(t, engine, drownColumn)
	player := engine.sessions[id].player
	interval := int(DefaultTunables().DrownDamageIntervalTicks)

	stepRegen(t, engine, id, int(core.MaxOxygenTicks))
	if player.oxygen != 0 {
		t.Fatalf("oxygen=%d，想要 0", player.oxygen)
	}
	// 生命值刻意不满：满血时 advanceHealthRegen 直接短路、ticksSinceDamage 恒为 0，
	// 「扣血后计时归零」这条断言会在两侧同时成立而恒真。这里先确认它确实在推进。
	beforeHealth := player.health
	if beforeHealth >= core.MaxHealth {
		t.Fatalf("氧气归零时 health=%d 已满，回血计时不会推进，本用例会退化成恒真", beforeHealth)
	}

	stepRegen(t, engine, id, interval-1)
	if player.health != beforeHealth {
		t.Fatalf("间隔未满就扣血：health=%d，想要 %d", player.health, beforeHealth)
	}
	if player.ticksSinceDamage == 0 {
		t.Fatal("扣血前 ticksSinceDamage 已经是 0，「扣血重置计时」这条断言无从区分")
	}

	stepRegen(t, engine, id, 1)
	if player.health != beforeHealth-1 {
		t.Fatalf("第 %d tick health=%d，想要 %d", interval, player.health, beforeHealth-1)
	}
	if player.ticksSinceDamage != 0 {
		t.Fatalf("溺水扣血后 ticksSinceDamage=%d，想要 0（必须走既有伤害入口重置回血计时）",
			player.ticksSinceDamage)
	}

	// 再走一个完整间隔：扣血必须继续按固定节奏发生，中途不得因回血抵消。
	stepRegen(t, engine, id, interval-1)
	if player.health != beforeHealth-1 {
		t.Fatalf("第二个间隔中途 health=%d，想要 %d", player.health, beforeHealth-1)
	}
	stepRegen(t, engine, id, 1)
	if player.health != beforeHealth-2 {
		t.Fatalf("第二个间隔满时 health=%d，想要 %d", player.health, beforeHealth-2)
	}
}

// TestDrowningKillsAndRunsExistingDeathSettlement 覆盖 spec Scenario
// 「溺水可致死并走既有死亡结算」：生命值归零后必须走与其他致死伤害相同的
// 死亡/重生路径（转入待重生、回满生命值、回到出生锚点列），而不是停在 0 血。
func TestDrowningKillsAndRunsExistingDeathSettlement(t *testing.T) {
	const id = SessionID(22)
	engine := readyRegenPlayer(t, id, 2)
	submergePlayerColumn(t, engine, drownColumn)
	player := engine.sessions[id].player
	interval := int(DefaultTunables().DrownDamageIntervalTicks)

	// 预算按最坏情况给足：氧气耗尽要 MaxOxygenTicks 个 tick，其后每 interval 个
	// tick 扣 1 点血，而自动回复在此期间永远追不上（间隔 20 远小于回复延迟 100），
	// 因此至多 MaxHealth 次扣血必然把血扣光。
	drownTicks := int(core.MaxOxygenTicks) + interval*(int(core.MaxHealth)+1)
	died := false
	for range drownTicks {
		result := engine.Step()
		// 死亡结算与扣血同在一个权威 tick 里跑完，因此发布出去的生命值绝不能是 0。
		for _, update := range result.Players {
			if update.Session == id && update.Health == 0 {
				t.Fatal("发布了 health == 0 的中间状态：死亡结算没有落在扣血的同一 tick 内")
			}
		}
		// 死后立刻会被重新出生，所以必须在推进过程中捕获这一刻，不能只看末态。
		if player.lifecycle == PlayerPendingSpawn {
			died = true
			break
		}
	}
	if !died {
		t.Fatalf("持续溺水 %d tick 仍未走到死亡结算", drownTicks)
	}
	if player.health != core.MaxHealth {
		t.Fatalf("溺死后 health=%d，想要 %d", player.health, core.MaxHealth)
	}
	if player.state.Position.Y() != core.MaxY+1 {
		t.Fatalf("溺死后位置 %v 不在出生锚点列顶（beginReset 未执行）", player.state.Position)
	}
	if player.oxygen != core.MaxOxygenTicks {
		t.Fatalf("重生后 oxygen=%d，想要 %d", player.oxygen, core.MaxOxygenTicks)
	}

	// 对照排在真实断言之后：同一场景在没有水的世界里必须活得好好的，否则上面
	// 那组断言可能只是在描述「任何玩家推进这么多 tick 都会死」。
	const dryID = SessionID(23)
	dryEngine := readyRegenPlayer(t, dryID, 2)
	for range drownTicks {
		dryEngine.Step()
	}
	dry := dryEngine.sessions[dryID].player
	if dry.lifecycle != PlayerActive {
		t.Fatalf("对照失效：无水世界的玩家也离开了 Active（lifecycle=%v）", dry.lifecycle)
	}
}

// TestOxygenRefillsImmediatelyOnLeavingFluid 覆盖 spec Scenario「出水立即恢复满氧」：
// 眼睛离开流体的那一 tick 氧气就回到满值，不是逐 tick 缓慢回复。
func TestOxygenRefillsImmediatelyOnLeavingFluid(t *testing.T) {
	const id = SessionID(24)
	engine := readyRegenPlayer(t, id, 10)
	submergePlayerColumn(t, engine, drownColumn)
	player := engine.sessions[id].player

	stepRegen(t, engine, id, 100)
	if player.oxygen != core.MaxOxygenTicks-100 {
		t.Fatalf("潜水 100 tick 后 oxygen=%d，想要 %d", player.oxygen, core.MaxOxygenTicks-100)
	}

	drainPlayerColumn(t, engine, drownColumn)
	stepRegen(t, engine, id, 1)
	if player.oxygen != core.MaxOxygenTicks {
		t.Fatalf("出水一 tick 后 oxygen=%d，想要立即回到 %d", player.oxygen, core.MaxOxygenTicks)
	}
}

// TestOxygenIsFullOnRegistrationBeforeAnyTick 覆盖 spec Scenario
// 「氧气不跨重启保留」在 sim 一侧的那一半：氧气由 RegisterPlayer 直接初始化为满值。
//
// 断言刻意落在「第一个 tick 还没跑」的位置：如果只在重连若干 tick 之后检查，
// 满氧可能完全不是加载路径给的，而是「眼睛不在水里 → 立即回满」那条分支填的，
// 加载路径就算一个字都不初始化，用例照样绿。
func TestOxygenIsFullOnRegistrationBeforeAnyTick(t *testing.T) {
	const id = SessionID(25)
	engine := NewEngine(0, 0)
	engine.RegisterPlayer(id, PlayerRestore{SpawnDimension: core.Overworld})
	if oxygen := engine.sessions[id].player.oxygen; oxygen != core.MaxOxygenTicks {
		t.Fatalf("注册后（未跑任何 tick）oxygen=%d，想要 %d", oxygen, core.MaxOxygenTicks)
	}
}

// TestOxygenAfterReconnectUnderwaterCountsDownFromFull 是上一条的正面检验：
// 让「重连」进来的玩家一激活就浸没，于是「出水立即回满」那条分支根本不会执行，
// 氧气只能在加载路径给出的初始值上继续递减。加载路径若把氧气留成 0，
// 这里读到的就是 0 而不是从满值一路减下来的值。
func TestOxygenAfterReconnectUnderwaterCountsDownFromFull(t *testing.T) {
	const id = SessionID(26)
	engine := readyRegenPlayer(t, id, 10)
	submergePlayerColumn(t, engine, drownColumn)

	// 第二个会话模拟重连：PlayerRestore 里根本没有氧气字段可供恢复，
	// 它的位置正是那根已经灌满水的列。
	const reconnected = SessionID(27)
	current := PlayerLocation{Dimension: core.Overworld, Position: drownColumn}
	safe := current
	engine.RegisterPlayer(reconnected, PlayerRestore{
		Current:        &current,
		Safe:           &safe,
		Health:         10,
		SpawnDimension: core.Overworld,
	})
	player := engine.sessions[reconnected].player

	activated := false
	for range 16 {
		engine.Step()
		if player.lifecycle == PlayerActive {
			activated = true
			break
		}
	}
	if !activated {
		t.Fatal("重连玩家在 16 tick 内未激活")
	}
	if _, eyeInFluid := playerSubmersion(engine, reconnected); !eyeInFluid {
		t.Fatal("夹具无效：重连后的玩家不在水里，「出水立即回满」会替加载路径把氧气填满")
	}

	// 刚激活的这一刻氧气最多只可能被扣掉 1 个 tick——它只能来自加载路径给的满值。
	// 加载路径若把氧气留成 0（或任何低值），这条断言当场变红。
	start := player.oxygen
	if start < core.MaxOxygenTicks-1 {
		t.Fatalf("重连激活时 oxygen=%d，想要 %d 或 %d（必须从满值开始）",
			start, core.MaxOxygenTicks, core.MaxOxygenTicks-1)
	}
	// 再泡三个 tick：必须继续按每 tick −1 递减，而不是被「出水回满」反复填满。
	const extra = 3
	for range extra {
		engine.Step()
	}
	if want := start - extra; player.oxygen != want {
		t.Fatalf("再浸没 %d 个 tick 后 oxygen=%d，想要 %d", extra, player.oxygen, want)
	}
}

// playerSubmersion 用引擎自己的方块镜像与共享判定函数报告玩家的两个浸没标志，
// 供夹具承重守卫使用。
func playerSubmersion(engine *Engine, id SessionID) (bodyInFluid, eyeInFluid bool) {
	session := engine.sessions[id]
	source := dimensionCollisionSource{dimension: engine.dimensions[session.dimension]}
	return physics.SubmersionFlags(session.player.state.Position, source)
}
