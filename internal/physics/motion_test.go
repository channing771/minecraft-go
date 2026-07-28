package physics_test

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"minecraft-go/internal/core"
	"minecraft-go/internal/physics"
)

type emptySource struct{}

func (emptySource) CollisionBoxes(core.BlockPos) physics.CollisionBoxSet {
	return physics.CollisionBoxSet{Loaded: true}
}

func TestPlayerBoundsUseFeetCenter(t *testing.T) {
	bounds := physics.PlayerBounds(mgl32.Vec3{10, 20, -4})
	wantMin := mgl32.Vec3{9.7, 20, -4.3}
	wantMax := mgl32.Vec3{10.3, 21.8, -3.7}
	if !bounds.Min.ApproxEqualThreshold(wantMin, 1e-6) ||
		!bounds.Max.ApproxEqualThreshold(wantMax, 1e-6) {
		t.Fatalf("bounds=%+v，想要 min=%v max=%v", bounds, wantMin, wantMax)
	}
}

func TestDiagonalInputAcceleratesWithoutDiagonalBoost(t *testing.T) {
	got := physics.Step(physics.State{OnGround: true}, physics.Input{
		MoveX: 1, MoveZ: 1,
	}, emptySource{}).State
	horizontal := float32(math.Hypot(float64(got.Velocity.X()), float64(got.Velocity.Z())))
	if math.Abs(float64(horizontal-2.0)) > 1e-5 {
		t.Fatalf("首步水平速度=%f，想要 acceleration*dt=2", horizontal)
	}
}

func TestJumpAndGravityUseFixedConstants(t *testing.T) {
	jump := physics.Step(physics.State{OnGround: true}, physics.Input{Jump: true}, emptySource{}).State
	if jump.Velocity.Y() != physics.JumpSpeed || jump.OnGround {
		t.Fatalf("jump=%+v", jump)
	}
	fall := physics.Step(physics.State{Velocity: mgl32.Vec3{0, -78, 0}}, physics.Input{}, emptySource{}).State
	if fall.Velocity.Y() != -physics.TerminalFallSpeed {
		t.Fatalf("terminal velocity=%f", fall.Velocity.Y())
	}
}
