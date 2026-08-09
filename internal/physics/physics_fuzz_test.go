package physics_test

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/physics"
)

func TestValidStateRejectsNonFiniteComponents(t *testing.T) {
	finiteState := physics.State{
		Position: mgl32.Vec3{0.5, 1, -0.5},
		Velocity: mgl32.Vec3{1, -2, 3},
		OnGround: true,
	}
	tests := []struct {
		name  string
		state physics.State
		want  bool
	}{
		{name: "finite", state: finiteState, want: true},
		{name: "position x nan", state: physics.State{Position: mgl32.Vec3{float32(math.NaN()), 1, 1}}, want: false},
		{name: "position y positive infinity", state: physics.State{Position: mgl32.Vec3{1, float32(math.Inf(1)), 1}}, want: false},
		{name: "position z negative infinity", state: physics.State{Position: mgl32.Vec3{1, 1, float32(math.Inf(-1))}}, want: false},
		{name: "velocity x nan", state: physics.State{Velocity: mgl32.Vec3{float32(math.NaN()), 1, 1}}, want: false},
		{name: "velocity y positive infinity", state: physics.State{Velocity: mgl32.Vec3{1, float32(math.Inf(1)), 1}}, want: false},
		{name: "velocity z negative infinity", state: physics.State{Velocity: mgl32.Vec3{1, 1, float32(math.Inf(-1))}}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := physics.ValidState(test.state); got != test.want {
				t.Fatalf("ValidState(%+v) = %t, want %t", test.state, got, test.want)
			}
		})
	}
}

func TestStepDeterministic(t *testing.T) {
	world := invariantWorld{}
	tests := []struct {
		name  string
		state physics.State
		input physics.Input
	}{
		{
			name:  "flat ground",
			state: physics.State{Position: mgl32.Vec3{0.5, 1, 0.5}, OnGround: true},
			input: physics.Input{MoveZ: 1},
		},
		{
			name:  "wall collision",
			state: physics.State{Position: mgl32.Vec3{0.5, 1, 0.5}, Velocity: mgl32.Vec3{physics.DefaultTunables().WalkSpeed, 0, 0}, OnGround: true},
			input: physics.Input{MoveX: 1},
		},
		{
			name:  "half block step",
			state: physics.State{Position: mgl32.Vec3{-2.5, 1, 0.5}, Velocity: mgl32.Vec3{physics.DefaultTunables().WalkSpeed, 0, 0}, OnGround: true},
			input: physics.Input{MoveX: 1},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			first := physics.Step(test.state, test.input, world)
			second := physics.Step(test.state, test.input, world)
			if first != second {
				t.Fatalf("same step produced different results: first=%+v second=%+v", first, second)
			}
			assertStepInvariants(t, first.State, world)
		})
	}
}

func TestStepKeepsClearAtHalfBlockBoundary(t *testing.T) {
	world := invariantWorld{}
	state := physics.State{
		Position: mgl32.Vec3{-1.1, 1, 1.4},
		OnGround: true,
	}
	got := physics.Step(state, physics.Input{MoveZ: 1}, world).State
	if overlapsLoadedCollider(got.Position, world) {
		bounds := physics.PlayerBounds(got.Position)
		t.Fatalf("final bounds overlap a loaded collider: state=%+v min z=%.9g", got, bounds.Min.Z())
	}
}

func TestStepKeepsClearAtFullBlockBoundary(t *testing.T) {
	world := invariantWorld{}
	state := physics.State{
		Position: mgl32.Vec3{2, 1, 1.5},
		Velocity: mgl32.Vec3{0, 0, -3.3},
		OnGround: true,
	}
	got := physics.Step(state, physics.Input{MoveZ: 1}, world).State
	if overlapsLoadedCollider(got.Position, world) {
		bounds := physics.PlayerBounds(got.Position)
		t.Fatalf("final bounds overlap a loaded collider: state=%+v min z=%.9g", got, bounds.Min.Z())
	}
}

func TestStepKeepsClearAtExactBoundaryDistance(t *testing.T) {
	world := invariantWorld{}
	state := physics.State{
		Position: mgl32.Vec3{1.8, 1, 1.5},
		Velocity: mgl32.Vec3{0, 0, -2},
		OnGround: true,
	}
	got := physics.Step(state, physics.Input{MoveZ: 1}, world).State
	if overlapsLoadedCollider(got.Position, world) {
		t.Fatalf("final bounds overlap a loaded collider: %+v", got)
	}
}

func TestStepDoesNotStopBeforePositiveWallGap(t *testing.T) {
	world := boxes(
		block(0, 0, 0, fullCube),
		block(1, 1, 0, fullCube),
	)
	got := physics.Step(physics.State{
		Position: mgl32.Vec3{0.5, 1, 0.5},
		Velocity: mgl32.Vec3{1.9998, 0, 0},
		OnGround: true,
	}, physics.Input{MoveX: 1}, world).State
	if got.Position.X() < 0.69998 || got.Position.X() > 0.70000 {
		t.Fatalf("positive gap movement stopped: position=%v", got.Position)
	}
	if got.Velocity.X() < 3.9997 || got.Velocity.X() > 3.9999 {
		t.Fatalf("positive gap movement lost velocity: velocity=%v", got.Velocity)
	}
	if physics.PlayerBounds(got.Position).Max.X() >= 1 {
		t.Fatalf("positive gap reached wall: bounds=%+v", physics.PlayerBounds(got.Position))
	}
}

func TestStepDoesNotStopBeforeNegativeWallGap(t *testing.T) {
	world := boxes(
		block(1, 0, 0, fullCube),
		block(0, 1, 0, fullCube),
	)
	got := physics.Step(physics.State{
		Position: mgl32.Vec3{1.5, 1, 0.5},
		Velocity: mgl32.Vec3{-1.9999, 0, 0},
		OnGround: true,
	}, physics.Input{MoveX: -1}, world).State
	if got.Position.X() < 1.30000 || got.Position.X() > 1.30001 {
		t.Fatalf("negative gap movement stopped: position=%v", got.Position)
	}
	if got.Velocity.X() < -4.0000 || got.Velocity.X() > -3.9998 {
		t.Fatalf("negative gap movement lost velocity: velocity=%v", got.Velocity)
	}
	if physics.PlayerBounds(got.Position).Min.X() <= 1 {
		t.Fatalf("negative gap reached wall: bounds=%+v", physics.PlayerBounds(got.Position))
	}
}

func TestStepIgnoresUnknownCellTouchingParallelFace(t *testing.T) {
	world := unknownAt(core.BlockPos{X: -1, Y: 1, Z: 0})
	got := physics.Step(physics.State{
		Position: mgl32.Vec3{0.3, 1, 0.5},
		OnGround: true,
	}, physics.Input{MoveZ: 1}, world)
	if got.HitUnknown {
		t.Fatalf("face-touching unknown cell reported as hit: %+v", got)
	}
	if got.State.Position.Z() >= 0.5 {
		t.Fatalf("parallel movement did not progress: %+v", got)
	}
}

func FuzzStepKeepsFiniteNonOverlappingState(f *testing.F) {
	f.Add(int16(5), int16(10), int16(5), int16(0), int16(0), int16(0), int8(0), int8(1), false, int16(0))
	f.Add(int16(-25), int16(10), int16(5), int16(43), int16(0), int16(0), int8(1), int8(0), false, int16(0))
	f.Add(int16(15), int16(25), int16(-15), int16(-20), int16(-100), int16(20), int8(-1), int8(1), true, int16(314))

	world := invariantWorld{}
	f.Fuzz(func(t *testing.T, positionX, positionY, positionZ, velocityX, velocityY, velocityZ int16, moveX, moveZ int8, jump bool, yawHundredths int16) {
		if positionX < -30 || positionX > 30 || positionY < 10 || positionY > 30 || positionZ < -30 || positionZ > 30 ||
			velocityX < -43 || velocityX > 43 || velocityY < -784 || velocityY > 200 || velocityZ < -43 || velocityZ > 43 ||
			moveX < -1 || moveX > 1 || moveZ < -1 || moveZ > 1 || yawHundredths < -628 || yawHundredths > 628 {
			t.Skip()
		}

		state := physics.State{
			Position: mgl32.Vec3{float32(positionX) / 10, float32(positionY) / 10, float32(positionZ) / 10},
			Velocity: mgl32.Vec3{float32(velocityX) / 10, float32(velocityY) / 10, float32(velocityZ) / 10},
			OnGround: positionY == 10,
		}
		if horizontalSpeed(state.Velocity) > float64(physics.DefaultTunables().WalkSpeed) || overlapsLoadedCollider(state.Position, world) {
			t.Skip()
		}
		input := physics.Input{MoveX: moveX, MoveZ: moveZ, Jump: jump, Yaw: float32(yawHundredths) / 100}

		first := physics.Step(state, input, world)
		second := physics.Step(state, input, world)
		if first != second {
			t.Fatalf("same step produced different results: first=%+v second=%+v", first, second)
		}
		assertStepInvariants(t, first.State, world)
	})
}

func assertStepInvariants(t *testing.T, state physics.State, world invariantWorld) {
	t.Helper()
	if !finiteState(state) {
		t.Fatalf("step returned non-finite state: %+v", state)
	}
	if speed := horizontalSpeed(state.Velocity); speed > float64(physics.DefaultTunables().WalkSpeed)+1e-5 {
		t.Fatalf("horizontal speed=%f exceeds walk speed", speed)
	}
	if state.Velocity.Y() < -physics.DefaultTunables().TerminalFallSpeed {
		t.Fatalf("vertical speed=%f is below terminal fall speed", state.Velocity.Y())
	}
	if overlapsLoadedCollider(state.Position, world) {
		t.Fatalf("final bounds overlap a loaded collider: %+v", state)
	}
}

func finiteState(state physics.State) bool {
	return finite(state.Position.X()) && finite(state.Position.Y()) && finite(state.Position.Z()) &&
		finite(state.Velocity.X()) && finite(state.Velocity.Y()) && finite(state.Velocity.Z())
}

func finite(value float32) bool {
	return !math.IsNaN(float64(value)) && !math.IsInf(float64(value), 0)
}

func horizontalSpeed(velocity mgl32.Vec3) float64 {
	return math.Hypot(float64(velocity.X()), float64(velocity.Z()))
}

type invariantWorld struct{}

func (invariantWorld) CollisionBoxes(position core.BlockPos) physics.CollisionBoxSet {
	if position.Y == 0 {
		return physics.CollisionBoxSet{Loaded: true, Count: 1, Boxes: [8]core.AABB{fullCube}}
	}
	if position == (core.BlockPos{X: 2, Y: 1, Z: 0}) {
		return physics.CollisionBoxSet{Loaded: true, Count: 1, Boxes: [8]core.AABB{fullCube}}
	}
	if position == (core.BlockPos{X: -2, Y: 1, Z: 0}) {
		return physics.CollisionBoxSet{Loaded: true, Count: 1, Boxes: [8]core.AABB{{Max: mgl32.Vec3{1, 0.5, 1}}}}
	}
	return physics.CollisionBoxSet{Loaded: true}
}

func overlapsLoadedCollider(position mgl32.Vec3, world invariantWorld) bool {
	player := physics.PlayerBounds(position)
	minX, maxX := blockRange(player.Min.X(), player.Max.X())
	minY, maxY := blockRange(player.Min.Y(), player.Max.Y())
	minZ, maxZ := blockRange(player.Min.Z(), player.Max.Z())
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			for z := minZ; z <= maxZ; z++ {
				position := core.BlockPos{X: x, Y: y, Z: z}
				set := world.CollisionBoxes(position)
				for i := 0; i < int(set.Count); i++ {
					if boundsOverlap(player, offsetBounds(position, set.Boxes[i])) {
						return true
					}
				}
			}
		}
	}
	return false
}

func blockRange(min, max float32) (int32, int32) {
	return int32(math.Floor(float64(min))), int32(math.Floor(float64(max)))
}

func offsetBounds(position core.BlockPos, bounds core.AABB) core.AABB {
	offset := mgl32.Vec3{float32(position.X), float32(position.Y), float32(position.Z)}
	return core.AABB{Min: bounds.Min.Add(offset), Max: bounds.Max.Add(offset)}
}

func boundsOverlap(a, b core.AABB) bool {
	return a.Min.X() < b.Max.X() && a.Max.X() > b.Min.X() &&
		a.Min.Y() < b.Max.Y() && a.Max.Y() > b.Min.Y() &&
		a.Min.Z() < b.Max.Z() && a.Max.Z() > b.Min.Z()
}
