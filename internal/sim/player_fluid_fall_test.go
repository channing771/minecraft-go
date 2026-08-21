package sim_test

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/physics"
	"github.com/channing771/mornlea/internal/sim"
)

// testPoolFluid 把测试摆放的方块表当作 physics.FluidSource 用，让用例能自己
// 判断「某个位置是不是浸没」，而不必窥探 sim 的内部状态。
type testPoolFluid map[core.BlockPos]core.BlockID

func (pool testPoolFluid) IsFluidAt(position core.BlockPos) bool {
	return core.IsFluid(pool[position])
}

// TestFallIntoFluidCancelsFallDamage 覆盖 spec Scenario「入水消除摔落伤害」：
// 从足以致伤的高度下落，但在触底前进入流体，不得结算摔落伤害。
func TestFallIntoFluidCancelsFallDamage(t *testing.T) {
	pool := map[core.BlockPos]core.BlockID{}
	for y := int32(1); y <= 4; y++ {
		pool[core.BlockPos{X: 0, Y: y, Z: 0}] = core.WaterSourceID
	}
	engine, session := readyFlatPlayerPool(t, pool)
	dropPlayer(t, engine, session, 10)
	assertPlayerHealth(t, engine, session, core.MaxHealth)

	// 浅水：一格深的水池 + 足够快的下落，本步开始时玩家还在水面之上、结束时
	// 已经踩到水底，只判步首标志会漏掉整跤。逐高度扫一遍是因为「哪一 tick 跨过
	// 水面」取决于逐 tick 位移与 tick 对齐，绝大多数高度下落地前一步脚底已经
	// 在水面之下、步首重置就把活干完了，只有少数高度真正走到步末那条路径。
	shallow := map[core.BlockPos]core.BlockID{{X: 0, Y: 1, Z: 0}: core.WaterSourceID}
	crossings := 0
	for height := float32(5); height <= 12; height++ {
		shallowEngine, shallowSession := readyFlatPlayerPool(t, shallow)
		if dropCrossesSurfaceWithinOneStep(t, shallowEngine, shallowSession, height, testPoolFluid(shallow)) {
			crossings++
		}
		if snapshot, ok := shallowEngine.PlayerSnapshot(shallowSession); !ok || snapshot.Health != core.MaxHealth {
			t.Fatalf("浅水池 height=%v：health=%d ok=%v，想要 %d", height, snapshot.Health, ok, core.MaxHealth)
		}
	}

	// 夹具承重守卫排在真实断言之后：扫描区间里必须至少有一个高度真的出现
	// 「步首干、步末湿」，否则这一段只是在重复验证步首重置，步末那条路径零覆盖，
	// 而将来动重力/终端速度/tick 步长都会让它静默退化成全绿。
	if crossings == 0 {
		t.Fatal("夹具无效：扫描区间内没有任何高度走到「步首干、步末湿」，步末重置未被覆盖")
	}

	// 对照排在真实断言之后：同一高度在没有水的世界里必须真的致伤，否则上面
	// 那条断言只是在陈述「这个高度本来就不疼」，改坏实现也不会变红。
	for height := float32(5); height <= 12; height++ {
		dryEngine, drySession := readyFlatPlayer(t)
		dropPlayer(t, dryEngine, drySession, height)
		snapshot, ok := dryEngine.PlayerSnapshot(drySession)
		if !ok || snapshot.Health >= core.MaxHealth {
			t.Fatalf("对照失效：无水世界 height=%v health=%d ok=%v，想要严格小于 %d",
				height, snapshot.Health, ok, core.MaxHealth)
		}
	}
}

// dropCrossesSurfaceWithinOneStep 把玩家丢到地面之上 height 格处并推进到落地，
// 报告落地的那一 tick 是否恰好也是「开始时身体还没浸没、结束时已经浸没」的那一步
// ——只有这种对齐才会真正走到权威侧步末的那次摔落峰值重置。
//
// 判定用的是用例自己的方块表与共享的 physics.SubmersionFlags，观察量只有权威
// 广播出来的逐 tick 位置；「上一 tick 的步末位置」恒等于「这一 tick 的步首位置」，
// 因此这个计数与权威侧步首/步末两次判定看到的是同一组标志。
func dropCrossesSurfaceWithinOneStep(
	t *testing.T,
	engine *sim.Engine,
	session sim.SessionID,
	height float32,
	source physics.FluidSource,
) bool {
	t.Helper()
	previous := mgl32.Vec3{0.5, 1 + height, 0.5}
	engine.SetPlayerPositionForTest(session, previous)
	for range 400 {
		update := onlyPlayer(t, engine.Step())
		startWet, _ := physics.SubmersionFlags(previous, source)
		endWet, _ := physics.SubmersionFlags(update.State.Position, source)
		if update.State.OnGround {
			// 只有「跨过水面」与「落地」发生在同一 tick 时，步末那次重置才是
			// 唯一还能救下这一跤的东西：任何更早的入水都会被下一 tick 的步首
			// 重置接管。dry→wet 的转变每次下落都恰好发生一次，单看它会把每个
			// 高度都算进来，那样守卫恒真、等于没写。
			return !startWet && endWet
		}
		previous = update.State.Position
	}
	t.Fatalf("玩家在 400 tick 内未落地: height=%v", height)
	return false
}

// readyFlatPlayerPool 先在无水的平坦世界里让玩家出生，再把水池写进世界。
//
// 出生点选取会跳过身体浸没的候选列（任务 7.2「不把流体判为落脚点」），水池若
// 在区块生成时就压在出生列上，玩家根本不会在那里出生。本用例要的是「从高处
// 落进池子」，水池位置必须与下落列一致，因此水改在出生之后写入。
// SetBlockForTest 绕过 recordChange，池水不会被入队流动，与原先烘进区块的
// 静态池子逐格等价。
func readyFlatPlayerPool(
	t *testing.T,
	pool map[core.BlockPos]core.BlockID,
) (*sim.Engine, sim.SessionID) {
	t.Helper()
	engine, session := readyFlatPlayer(t)
	for position, block := range pool {
		engine.SetBlockForTest(position, block)
	}
	return engine, session
}
