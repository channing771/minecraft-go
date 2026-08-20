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

	// 对照排在真实断言之后：同一高度在没有水的世界里必须真的致伤，否则上面
	// 那条断言只是在陈述「这个高度本来就不疼」，改坏实现也不会变红。
	dryEngine, drySession := readyFlatPlayer(t)
	dropPlayer(t, dryEngine, drySession, 10)
	snapshot, ok := dryEngine.PlayerSnapshot(drySession)
	if !ok || snapshot.Health >= core.MaxHealth {
		t.Fatalf("对照失效：无水世界同高度 health=%d ok=%v，想要严格小于 %d", snapshot.Health, ok, core.MaxHealth)
	}
}
