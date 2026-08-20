package sim_test

import (
	"testing"

	"github.com/channing771/mornlea/internal/core"
)

// TestFallIntoFluidCancelsFallDamage 覆盖 spec Scenario「入水消除摔落伤害」：
// 从足以致伤的高度下落，但在触底前进入流体，不得结算摔落伤害。
func TestFallIntoFluidCancelsFallDamage(t *testing.T) {
	pool := map[core.BlockPos]core.BlockID{}
	for y := int32(1); y <= 4; y++ {
		pool[core.BlockPos{X: 0, Y: y, Z: 0}] = core.WaterSourceID
	}
	engine, session := readyFlatPlayerWithTarget(t, pool)
	dropPlayer(t, engine, session, 10)
	assertPlayerHealth(t, engine, session, core.MaxHealth)

	// 浅水：一格深的水池 + 足够快的下落，本步开始时玩家还在水面之上、结束时
	// 已经踩到水底，只判步首标志会漏掉整跤。逐高度扫一遍是因为「哪一 tick 跨过
	// 水面」取决于下落速度与 tick 对齐，单一高度很容易恰好落在步首就已入水的
	// 那一侧而漏测。
	shallow := map[core.BlockPos]core.BlockID{{X: 0, Y: 1, Z: 0}: core.WaterSourceID}
	for height := float32(5); height <= 12; height++ {
		shallowEngine, shallowSession := readyFlatPlayerWithTarget(t, shallow)
		dropPlayer(t, shallowEngine, shallowSession, height)
		if snapshot, ok := shallowEngine.PlayerSnapshot(shallowSession); !ok || snapshot.Health != core.MaxHealth {
			t.Fatalf("浅水池 height=%v：health=%d ok=%v，想要 %d", height, snapshot.Health, ok, core.MaxHealth)
		}
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
