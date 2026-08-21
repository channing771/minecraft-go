package sim

import (
	"testing"

	"github.com/channing771/mornlea/internal/core"
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
		{"FluidFlowDelayTicks", float64(tunables.FluidFlowDelayTicks), 5},
		{"FluidUpdatesPerTick", float64(tunables.FluidUpdatesPerTick), 512},
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

// TestSetTunablesClampsAuthorityTickInvariants 证明 SetTunables 兜住了两条
// 直接决定权威 tick 安全的不变量：RegenIntervalTicks 是取模除数（0 会 panic），
// SpawnRadius 决定一次平方级分配（不钳制会触发巨额分配）。
//
// 这两条区间在 internal/config 里也有一份，但 archcheck 禁止 sim 导入 config，
// 靠约定隔着一个包维持不变量是不够的。
func TestSetTunablesClampsAuthorityTickInvariants(t *testing.T) {
	t.Cleanup(func() { SetTunables(DefaultTunables()) })

	unsafe := DefaultTunables()
	unsafe.RegenIntervalTicks = 0
	unsafe.SpawnRadius = 100000
	SetTunables(unsafe)
	if got := ActiveTunables().RegenIntervalTicks; got < 1 {
		t.Errorf("RegenIntervalTicks = %d，必须钳到 >= 1（否则取模除零 panic）", got)
	}
	if got := ActiveTunables().SpawnRadius; got != maxSpawnRadius {
		t.Errorf("SpawnRadius = %d，必须钳到上界 %d", got, maxSpawnRadius)
	}

	unsafe.SpawnRadius = -5
	SetTunables(unsafe)
	if got := ActiveTunables().SpawnRadius; got != minSpawnRadius {
		t.Errorf("SpawnRadius = %d，必须钳到下界 %d", got, minSpawnRadius)
	}
}

// TestSetTunablesRoundTripsFluidFields 证明 FluidFlowDelayTicks 与
// FluidUpdatesPerTick 已按既有 tunable 约定接入 SetTunables/ActiveTunables
// 快照机制——本组只定义这两个值，尚无消费方读取它们（见字段 GoDoc），但快照
// 写入与读出本身必须已经生效，供后续任务组（4.1）直接消费。
func TestSetTunablesRoundTripsFluidFields(t *testing.T) {
	t.Cleanup(func() { SetTunables(DefaultTunables()) })

	custom := DefaultTunables()
	custom.FluidFlowDelayTicks = 9
	custom.FluidUpdatesPerTick = 1024
	SetTunables(custom)

	if got := ActiveTunables().FluidFlowDelayTicks; got != 9 {
		t.Errorf("FluidFlowDelayTicks = %d，want 9", got)
	}
	if got := ActiveTunables().FluidUpdatesPerTick; got != 1024 {
		t.Errorf("FluidUpdatesPerTick = %d，want 1024", got)
	}
}

// TestEngineRefreshesSnapshotAtTickStart 证明快照在 tick 入口刷新，
// 且同一 tick 内不再变化。
func TestEngineRefreshesSnapshotAtTickStart(t *testing.T) {
	t.Cleanup(func() { SetTunables(DefaultTunables()) })

	engine := NewEngine(0, 0, 0)

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
