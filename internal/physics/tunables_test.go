package physics_test

import (
	"sync"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/physics"
)

// TestDefaultTunablesMatchLegacyConstants 是本次重构的行为不变式：
// 默认参数必须逐字段等于重构前的编译常量，否则手感会静默改变。
func TestDefaultTunablesMatchLegacyConstants(t *testing.T) {
	tunables := physics.DefaultTunables()
	for _, check := range []struct {
		name      string
		got, want float32
	}{
		{"EyeHeight", tunables.EyeHeight, 1.62},
		{"StepHeight", tunables.StepHeight, 0.6},
		{"WalkSpeed", tunables.WalkSpeed, 4.3},
		{"GroundAcceleration", tunables.GroundAcceleration, 40},
		{"GroundDeceleration", tunables.GroundDeceleration, 50},
		{"AirAcceleration", tunables.AirAcceleration, 8},
		{"JumpSpeed", tunables.JumpSpeed, 8.4},
		{"Gravity", tunables.Gravity, 32},
		{"TerminalFallSpeed", tunables.TerminalFallSpeed, 78.4},
	} {
		if check.got != check.want {
			t.Errorf("%s = %v，want %v", check.name, check.got, check.want)
		}
	}
}

func TestActiveTunablesDefaultsToDefaultTunables(t *testing.T) {
	if physics.ActiveTunables() != physics.DefaultTunables() {
		t.Fatal("未经设置时生效参数必须等于默认参数")
	}
}

// TestSetTunablesAffectsStep 证明快照确实被 Step 消费。
func TestSetTunablesAffectsStep(t *testing.T) {
	t.Cleanup(func() { physics.SetTunables(physics.DefaultTunables()) })

	source := emptySource{}
	state := physics.State{Position: mgl32.Vec3{0, 64, 0}}

	physics.SetTunables(physics.DefaultTunables())
	slow := physics.Step(state, physics.Input{}, source)

	heavy := physics.DefaultTunables()
	heavy.Gravity *= 2
	physics.SetTunables(heavy)
	fast := physics.Step(state, physics.Input{}, source)

	if !(fast.State.Velocity.Y() < slow.State.Velocity.Y()) {
		t.Fatalf("加倍重力后竖直速度应更负：fast=%v slow=%v",
			fast.State.Velocity.Y(), slow.State.Velocity.Y())
	}
}

// TestStepUsesOneSnapshotPerCall 证明单次固定步内参数不中途变化。
func TestStepUsesOneSnapshotPerCall(t *testing.T) {
	t.Cleanup(func() { physics.SetTunables(physics.DefaultTunables()) })

	source := emptySource{}
	state := physics.State{Position: mgl32.Vec3{0, 64, 0}}
	physics.SetTunables(physics.DefaultTunables())
	want := physics.Step(state, physics.Input{}, source)

	// 同样的输入重复推进，结果必须逐位一致。
	for i := 0; i < 16; i++ {
		if got := physics.Step(state, physics.Input{}, source); got != want {
			t.Fatalf("第 %d 次结果 %v != %v", i, got, want)
		}
	}
}

// TestConcurrentStepAndSetTunables 在 -race 下证明快照读写无竞争。
func TestConcurrentStepAndSetTunables(t *testing.T) {
	t.Cleanup(func() { physics.SetTunables(physics.DefaultTunables()) })

	source := emptySource{}
	state := physics.State{Position: mgl32.Vec3{0, 64, 0}}
	var group sync.WaitGroup
	group.Add(2)
	go func() {
		defer group.Done()
		for i := 0; i < 2000; i++ {
			physics.Step(state, physics.Input{}, source)
		}
	}()
	go func() {
		defer group.Done()
		for i := 0; i < 2000; i++ {
			tunables := physics.DefaultTunables()
			tunables.Gravity = float32(20 + i%20)
			physics.SetTunables(tunables)
		}
	}()
	group.Wait()
}
