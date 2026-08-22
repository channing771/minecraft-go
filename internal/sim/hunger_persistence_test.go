package sim

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
)

// 恢复用的三层饥饿状态夹具：**三个字段全部取非初值**（初值是 20 /
// core.InitialSaturationMilli / 0）。任何一个字段在恢复链上被漏掉，读回来都会
// 落在初值上，与这里的取值不同，用例因此才承重；三个全取初值的夹具下，
// "恢复"与"不恢复"两种实现的读数完全相同。
//
// 饱和 2500 ≤ 12×1000，满足 storage 侧的 validatePlayerDTO 上界，这份取值因此
// 也能原样穿过存档编码。
const (
	restoredHunger          uint8  = 12
	restoredSaturationMilli uint16 = 2500
	restoredExhaustionMilli uint16 = 1750
)

// readyHungerPlayer 注册一名带指定 PlayerRestore 的玩家并推进到 Active。
// 与 readyRegenPlayer 同形，区别只在于恢复内容由调用方给出。
func readyHungerPlayer(t *testing.T, id SessionID, restore PlayerRestore) *Engine {
	t.Helper()
	engine := NewEngine(0, 0, 0)
	current := PlayerLocation{
		Dimension: core.Overworld,
		Position:  mgl32.Vec3{2.5, 1, 0.5},
	}
	safe := current
	restore.Current = &current
	restore.Safe = &safe
	restore.SpawnDimension = core.Overworld
	engine.RegisterPlayer(id, restore)
	makeRestoreWorldReady(t, engine, current, safe)
	if update := onlyPlayerUpdate(t, engine.Step(), id); !update.Ready {
		t.Fatalf("玩家未激活: %+v", update)
	}
	return engine
}

// assertSnapshotHunger 断言一份权威快照的三层饥饿状态。
func assertSnapshotHunger(
	t *testing.T,
	what string,
	snapshot PlayerSnapshot,
	hunger uint8,
	saturation, exhaustion uint16,
) {
	t.Helper()
	if snapshot.Hunger != hunger || snapshot.SaturationMilli != saturation ||
		snapshot.ExhaustionMilli != exhaustion {
		t.Fatalf("%s 快照三层饥饿状态 = (%d, %d, %d)，想要 (%d, %d, %d)",
			what, snapshot.Hunger, snapshot.SaturationMilli, snapshot.ExhaustionMilli,
			hunger, saturation, exhaustion)
	}
}

// TestRegisterPlayerRestoresHungerState 覆盖 Scenario「饥饿状态跨重启保留」的
// sim 一半：存档里的三层状态必须原样进入权威 playerState，并原样从快照读出。
//
// 快照那一半不是多余的：存档写盘读的是快照而不是 playerState，只断言
// playerState 会漏掉"恢复对了但快照没带"这种改法。
func TestRegisterPlayerRestoresHungerState(t *testing.T) {
	const id = SessionID(71)
	engine := readyHungerPlayer(t, id, PlayerRestore{
		Health:          core.MaxHealth,
		HasHunger:       true,
		Hunger:          restoredHunger,
		SaturationMilli: restoredSaturationMilli,
		ExhaustionMilli: restoredExhaustionMilli,
	})
	player := engine.sessions[id].player
	if player.hunger != restoredHunger ||
		player.saturationMilli != restoredSaturationMilli ||
		player.exhaustionMilli != restoredExhaustionMilli {
		t.Fatalf("恢复后权威三层状态 = (%d, %d, %d)，想要 (%d, %d, %d)",
			player.hunger, player.saturationMilli, player.exhaustionMilli,
			restoredHunger, restoredSaturationMilli, restoredExhaustionMilli)
	}
	snapshot, ok := engine.PlayerSnapshot(id)
	if !ok {
		t.Fatal("激活玩家取不到权威快照")
	}
	assertSnapshotHunger(t, "恢复后", snapshot,
		restoredHunger, restoredSaturationMilli, restoredExhaustionMilli)
}

// TestRegisterPlayerWithoutHungerUsesInitialValues 覆盖"没有饥饿存档的登录
// 一律回到固定初值"：新玩家、缺失玩家与只给维度锚点的 RegisterSession 都走
// 这条路。HasHunger 为假时，PlayerRestore 里的三个字段必须被完全忽略——
// 这里刻意填上非初值来钉住这一点，否则"零值代表缺失"那种写法也会通过。
func TestRegisterPlayerWithoutHungerUsesInitialValues(t *testing.T) {
	const id = SessionID(72)
	engine := readyHungerPlayer(t, id, PlayerRestore{
		Health:          core.MaxHealth,
		Hunger:          restoredHunger,
		SaturationMilli: restoredSaturationMilli,
		ExhaustionMilli: restoredExhaustionMilli,
	})
	snapshot, ok := engine.PlayerSnapshot(id)
	if !ok {
		t.Fatal("激活玩家取不到权威快照")
	}
	assertSnapshotHunger(t, "无饥饿存档登录", snapshot,
		core.MaxHunger, core.InitialSaturationMilli, 0)
}

// TestRespawnHungerResetReachesSnapshot 覆盖 Scenario「重生后饥饿回满」在
// 持久化链上的一环：死亡结算把三层状态置回初值之后，权威快照必须跟着回到初值。
//
// 死亡前刻意把三层状态推到非初值，否则"重生回满"与"什么都没做"读数相同。
func TestRespawnHungerResetReachesSnapshot(t *testing.T) {
	const id = SessionID(73)
	engine := readyRegenPlayer(t, id, 10)
	player := engine.sessions[id].player
	player.hunger = 3
	player.saturationMilli = 0
	player.exhaustionMilli = 3000
	before, ok := engine.PlayerSnapshot(id)
	if !ok {
		t.Fatal("死亡前取不到权威快照")
	}
	assertSnapshotHunger(t, "死亡前", before, 3, 0, 3000)

	player.applyDamage(int32(player.health))
	engine.Step()

	after, ok := engine.PlayerSnapshot(id)
	if !ok {
		t.Fatal("重生后取不到权威快照")
	}
	assertSnapshotHunger(t, "重生后", after, core.MaxHunger, core.InitialSaturationMilli, 0)
}
