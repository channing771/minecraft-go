package sim

import (
	"testing"

	"minecraft-go/internal/core"
)

func TestDefaultTunablesMatchLegacyConstants(t *testing.T) {
	tunables := DefaultTunables()
	for _, check := range []struct {
		name      string
		got, want float64
	}{
		{"InteractionReach", float64(tunables.InteractionReach), 6},
		{"RegenDelayTicks", float64(tunables.RegenDelayTicks), 100},
		{"RegenIntervalTicks", float64(tunables.RegenIntervalTicks), 40},
		{"DropPickupDelayTicks", float64(tunables.DropPickupDelayTicks), 10},
		{"PlayerDropPickupDelayTicks", float64(tunables.PlayerDropPickupDelayTicks), 40},
		{"DropLifetimeTicks", float64(tunables.DropLifetimeTicks), 6000},
		{"DropPickupRange", float64(tunables.DropPickupRange), 1.25},
		{"SpawnRadius", float64(tunables.SpawnRadius), 16},
		{"FurnaceSmeltTicks", float64(tunables.FurnaceSmeltTicks), float64(core.FurnaceSmeltTicks)},
		{"FurnaceBurnTicks", float64(tunables.FurnaceBurnTicks), float64(core.FurnaceBurnTicks)},
	} {
		if check.got != check.want {
			t.Errorf("%s = %v，want %v", check.name, check.got, check.want)
		}
	}
}

func TestActiveTunablesDefaultsToDefaultTunables(t *testing.T) {
	if ActiveTunables() != DefaultTunables() {
		t.Fatal("未经设置时生效参数必须等于默认参数")
	}
}

// TestEngineRefreshesSnapshotAtTickStart 证明快照在 tick 入口刷新，
// 且同一 tick 内不再变化。
func TestEngineRefreshesSnapshotAtTickStart(t *testing.T) {
	t.Cleanup(func() { SetTunables(DefaultTunables()) })

	engine := NewEngine(0, 0)

	changed := DefaultTunables()
	changed.InteractionReach = 3
	SetTunables(changed)

	engine.Step()
	if engine.tunables.InteractionReach != 3 {
		t.Fatalf("tick 后引擎快照 InteractionReach = %v，want 3",
			engine.tunables.InteractionReach)
	}

	// tick 之间修改，在下一次 Step 之前引擎快照不应改变。
	again := DefaultTunables()
	again.InteractionReach = 5
	SetTunables(again)
	if engine.tunables.InteractionReach != 3 {
		t.Fatal("引擎快照必须只在 tick 入口刷新")
	}
	engine.Step()
	if engine.tunables.InteractionReach != 5 {
		t.Fatalf("下一次 tick 后应刷新为 5，实际 %v", engine.tunables.InteractionReach)
	}
}
