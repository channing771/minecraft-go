package physics_test

import (
	"sync"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/physics"
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

// TestStepHeightTunableGatesStepUp 证明 StepHeight 确实经快照送达跨步判定。
//
// StepHeight 经 Go collision snapshot header 传给 native resolver；若编码器把传入值
// 回退为 defaultStepHeight，本测试会变红。这里直接钉住可观察行为——
// StepHeight 归零后半格台阶必须跨不上去。
func TestStepHeightTunableGatesStepUp(t *testing.T) {
	t.Cleanup(func() { physics.SetTunables(physics.DefaultTunables()) })

	world := floorWithObstacle(0.5, true)
	physics.SetTunables(physics.DefaultTunables())
	if got := physics.Step(groundedTowardObstacle(), physics.Input{MoveX: 1}, world); !got.UsedStep {
		t.Fatalf("默认 StepHeight=0.6 时应能跨上半格: %+v", got)
	}

	flat := physics.DefaultTunables()
	flat.StepHeight = 0
	physics.SetTunables(flat)
	got := physics.Step(groundedTowardObstacle(), physics.Input{MoveX: 1}, world)
	if got.UsedStep || got.State.Position.X() > 0.7+1e-5 {
		t.Fatalf("StepHeight=0 时不得跨上半格，快照未送达跨步判定: %+v", got)
	}
}

// TestStepIsDeterministicForFixedTunables 证明参数固定时同样的输入逐位复现。
//
// 原名 TestStepUsesOneSnapshotPerCall 承诺的是"单次固定步内只取一次快照"，
// 但函数体全程没有并发写入，证不到原子性，只证得到确定性——按它实际验证的
// 性质改名。快照的并发安全由 TestConcurrentStepAndSetTunables 覆盖。
func TestStepIsDeterministicForFixedTunables(t *testing.T) {
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
