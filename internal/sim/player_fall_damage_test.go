package sim_test

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/sim"
)

// TestFallDamageThreeBlocksNoDamage 覆盖"三格及以内不扣血"场景。
func TestFallDamageThreeBlocksNoDamage(t *testing.T) {
	engine, session := readyFlatPlayer(t)
	dropPlayer(t, engine, session, 3)
	assertPlayerHealth(t, engine, session, core.MaxHealth)
}

// TestFallDamageFourBlocksDealsOne 覆盖"四格扣一点"场景。
func TestFallDamageFourBlocksDealsOne(t *testing.T) {
	engine, session := readyFlatPlayer(t)
	dropPlayer(t, engine, session, 4)
	assertPlayerHealth(t, engine, session, core.MaxHealth-1)
}

// TestFallDamageTwentyThreeBlocksFromFullHealthIsLethal 覆盖
// "二十三格从满血致死"场景：伤害恰为 20 使生命值归零，随即在同一 tick 内
// 由死亡结算送回出生锚点并回满，因此致死性只能从"玩家死过一次"观察到。
func TestFallDamageTwentyThreeBlocksFromFullHealthIsLethal(t *testing.T) {
	engine, session := readyFlatPlayer(t)
	killByFall(t, engine, session)
	respawnPlayer(t, engine, session)
	assertPlayerHealth(t, engine, session, core.MaxHealth)
}

// TestFallDamageNormalJumpNoDamage 覆盖"正常跳跃不扣血"场景：
// 原地起跳的峰值高度远低于安全高度，落地不应结算伤害。
func TestFallDamageNormalJumpNoDamage(t *testing.T) {
	engine, session := readyFlatPlayer(t)
	engine.Enqueue(sim.Command{
		Session: session, Sequence: 1, Kind: sim.CommandPlayerInput, Jump: true,
	})

	left := false
	landed := false
	for range 100 {
		result := engine.Step()
		update := onlyPlayer(t, result)
		if !update.State.OnGround {
			left = true
		}
		if left && update.State.OnGround {
			landed = true
			break
		}
	}
	if !left || !landed {
		t.Fatalf("跳跃未能观察到离地/落地边沿: left=%v landed=%v", left, landed)
	}
	assertPlayerHealth(t, engine, session, core.MaxHealth)
}

// TestFallDamageSettlesOnceAfterLanding 覆盖"落地只结算一次"场景：
// 落地扣血后继续停留、不再离地，生命值不应继续下降。
func TestFallDamageSettlesOnceAfterLanding(t *testing.T) {
	engine, session := readyFlatPlayer(t)
	dropPlayer(t, engine, session, 4)
	assertPlayerHealth(t, engine, session, core.MaxHealth-1)

	for range 20 {
		engine.Step()
	}
	assertPlayerHealth(t, engine, session, core.MaxHealth-1)
}

// dropPlayer 把玩家瞬移到地面之上 height 格处并推进 tick 直到落地，返回落地时的
// 权威玩家状态。
func dropPlayer(
	t *testing.T,
	engine *sim.Engine,
	session sim.SessionID,
	height float32,
) sim.PlayerUpdate {
	t.Helper()
	engine.SetPlayerPositionForTest(session, mgl32.Vec3{0.5, 1 + height, 0.5})
	for range 400 {
		result := engine.Step()
		update := onlyPlayer(t, result)
		if update.State.OnGround {
			return update
		}
	}
	t.Fatalf("玩家在 400 tick 内未落地: height=%v", height)
	return sim.PlayerUpdate{}
}

func assertPlayerHealth(
	t *testing.T,
	engine *sim.Engine,
	session sim.SessionID,
	want uint8,
) {
	t.Helper()
	snapshot, ok := engine.PlayerSnapshot(session)
	if !ok || snapshot.Health != want {
		t.Fatalf("health=%d ok=%v，想要 %d", snapshot.Health, ok, want)
	}
}
