package physics_test

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/physics"
)

var stepResultSink physics.StepResult

func TestStepPlayerDoesNotAllocate(t *testing.T) {
	state := physics.State{Position: mgl32.Vec3{0.5, 1, 0.5}, OnGround: true}
	input := physics.Input{MoveZ: 1}
	world := benchmarkWorld{}
	allocs := testing.AllocsPerRun(1000, func() {
		_ = physics.Step(state, input, world)
	})
	if allocs != 0 {
		t.Fatalf("physics.Step allocs/op=%f，想要 0", allocs)
	}
}

func TestStepPlayerCollidingDoesNotAllocate(t *testing.T) {
	state := physics.State{
		Position: mgl32.Vec3{0.5, 1, 0.5},
		Velocity: mgl32.Vec3{physics.WalkSpeed, 0, 0},
		OnGround: true,
	}
	input := physics.Input{MoveX: 1}
	world := collidingBenchmarkWorld{}
	allocs := testing.AllocsPerRun(1000, func() {
		_ = physics.Step(state, input, world)
	})
	if allocs != 0 {
		t.Fatalf("physics.Step allocs/op=%f，想要 0", allocs)
	}
}

func TestStepPlayerSteppingDoesNotAllocate(t *testing.T) {
	state := physics.State{
		Position: mgl32.Vec3{0.5, 1, 0.5},
		Velocity: mgl32.Vec3{physics.WalkSpeed, 0, 0},
		OnGround: true,
	}
	input := physics.Input{MoveX: 1}
	world := steppingBenchmarkWorld{}
	allocs := testing.AllocsPerRun(1000, func() {
		_ = physics.Step(state, input, world)
	})
	if allocs != 0 {
		t.Fatalf("physics.Step allocs/op=%f，想要 0", allocs)
	}
}

func BenchmarkStepPlayerFlat(b *testing.B) {
	state := physics.State{Position: mgl32.Vec3{0.5, 1, 0.5}, OnGround: true}
	input := physics.Input{MoveZ: 1}
	world := benchmarkWorld{}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		stepResultSink = physics.Step(state, input, world)
	}
}

func BenchmarkStepPlayerColliding(b *testing.B) {
	state := physics.State{
		Position: mgl32.Vec3{0.5, 1, 0.5},
		Velocity: mgl32.Vec3{physics.WalkSpeed, 0, 0},
		OnGround: true,
	}
	input := physics.Input{MoveX: 1}
	world := collidingBenchmarkWorld{}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		stepResultSink = physics.Step(state, input, world)
	}
}

func BenchmarkStepPlayerStepping(b *testing.B) {
	state := physics.State{
		Position: mgl32.Vec3{0.5, 1, 0.5},
		Velocity: mgl32.Vec3{physics.WalkSpeed, 0, 0},
		OnGround: true,
	}
	input := physics.Input{MoveX: 1}
	world := steppingBenchmarkWorld{}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		stepResultSink = physics.Step(state, input, world)
	}
}

type benchmarkWorld struct{}

func (benchmarkWorld) CollisionBoxes(position core.BlockPos) physics.CollisionBoxSet {
	return benchmarkCollisionBoxes(position, 0)
}

type collidingBenchmarkWorld struct{}

func (collidingBenchmarkWorld) CollisionBoxes(position core.BlockPos) physics.CollisionBoxSet {
	return benchmarkCollisionBoxes(position, 1)
}

type steppingBenchmarkWorld struct{}

func (steppingBenchmarkWorld) CollisionBoxes(position core.BlockPos) physics.CollisionBoxSet {
	return benchmarkCollisionBoxes(position, 0.5)
}

func benchmarkCollisionBoxes(position core.BlockPos, obstacleHeight float32) physics.CollisionBoxSet {
	if position.Y == 0 {
		return physics.CollisionBoxSet{Loaded: true, Count: 1, Boxes: [8]core.AABB{fullCube}}
	}
	if obstacleHeight != 0 && position == (core.BlockPos{X: 1, Y: 1, Z: 0}) {
		return physics.CollisionBoxSet{
			Loaded: true,
			Count:  1,
			Boxes:  [8]core.AABB{{Max: mgl32.Vec3{1, obstacleHeight, 1}}},
		}
	}
	return physics.CollisionBoxSet{Loaded: true}
}
