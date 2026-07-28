package physics_test

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/physics"
)

// These tests would fail if the alternate path were chosen without first
// requiring a strictly greater horizontal distance than ordinary movement.
func TestStepClimbsHalfBlock(t *testing.T) {
	world := floorWithObstacle(0.5, true)
	got := physics.Step(physics.State{
		Position: mgl32.Vec3{0.5, 1, 0.5},
		Velocity: mgl32.Vec3{4.3, 0, 0},
		OnGround: true,
	}, physics.Input{MoveX: 1}, world)
	if !got.UsedStep || got.State.Position.Y() < 1.49 {
		t.Fatalf("未跨上半格: %+v", got)
	}
}

func TestStepDoesNotClimbFullBlock(t *testing.T) {
	got := physics.Step(groundedTowardObstacle(), physics.Input{MoveX: 1}, floorWithObstacle(1, true))
	if got.UsedStep || got.State.Position.X() > 0.7+1e-5 {
		t.Fatalf("越过整格障碍: %+v", got)
	}
}

func TestStepRejectsInsufficientHeadroom(t *testing.T) {
	world := boxes(
		block(0, 0, 0, fullCube),
		block(1, 0, 0, fullCube),
		block(1, 1, 0, core.AABB{Max: mgl32.Vec3{1, 0.5, 1}}),
		block(0, 3, 0, fullCube),
	)
	got := physics.Step(groundedTowardObstacle(), physics.Input{MoveX: 1}, world)
	if got.UsedStep || got.State.Position.X() > 0.7+1e-5 {
		t.Fatalf("头顶空间不足仍跨步: %+v", got)
	}
}

func TestStepRejectsAirbornePlayer(t *testing.T) {
	got := physics.Step(physics.State{
		Position: mgl32.Vec3{0.5, 1.5, 0.5},
		Velocity: mgl32.Vec3{4.3, 0, 0},
	}, physics.Input{MoveX: 1}, floorWithObstacle(0.5, true))
	if got.UsedStep || got.State.Position.X() > 0.7+1e-5 {
		t.Fatalf("空中玩家仍跨步: %+v", got)
	}
}

func TestStepRejectsUnknownObstacleCells(t *testing.T) {
	world := floorWithObstacle(0.5, false)
	got := physics.Step(groundedTowardObstacle(), physics.Input{MoveX: 1}, world)
	if got.UsedStep || !got.HitUnknown || got.State.Position.X() > 0.7+1e-5 {
		t.Fatalf("未知方块仍跨步: %+v", got)
	}
}

func TestStepKeepsOrdinaryPathOnEqualHorizontalDistance(t *testing.T) {
	got := physics.Step(groundedTowardObstacle(), physics.Input{MoveX: 1}, floorWithObstacle(1, true))
	if got.UsedStep || got.State.Position.X() < 0.7-1e-5 || got.State.Position.X() > 0.7+1e-5 {
		t.Fatalf("水平距离相同却未保留普通路径: %+v", got)
	}
}

func TestStepDoesNotHopAcrossGroundLevelCollision(t *testing.T) {
	got := physics.Step(physics.State{
		Position: mgl32.Vec3{0.5, 1, 0.5},
		Velocity: mgl32.Vec3{-30, 0, 0},
		OnGround: true,
	}, physics.Input{MoveX: 1}, boxes(block(-1, 0, 0, fullCube)))
	if got.UsedStep || got.State.Position.X() < 0.3-1e-5 || got.State.Position.X() > 0.3+1e-5 {
		t.Fatalf("地面高度碰撞被错误当作跨步: %+v", got)
	}
}

func groundedTowardObstacle() physics.State {
	return physics.State{
		Position: mgl32.Vec3{0.5, 1, 0.5},
		Velocity: mgl32.Vec3{4.3, 0, 0},
		OnGround: true,
	}
}

func floorWithObstacle(height float32, loaded bool) physics.CollisionSource {
	return stepWorld{height: height, obstacleLoaded: loaded}
}

type stepWorld struct {
	height         float32
	obstacleLoaded bool
}

func (w stepWorld) CollisionBoxes(position core.BlockPos) physics.CollisionBoxSet {
	if position == (core.BlockPos{X: 1, Y: 1, Z: 0}) {
		if !w.obstacleLoaded {
			return physics.CollisionBoxSet{}
		}
		return physics.CollisionBoxSet{
			Loaded: true,
			Count:  1,
			Boxes:  [8]core.AABB{{Max: mgl32.Vec3{1, w.height, 1}}},
		}
	}
	if position.Y == 0 && (position.X == 0 || position.X == 1) && position.Z == 0 {
		return physics.CollisionBoxSet{Loaded: true, Count: 1, Boxes: [8]core.AABB{fullCube}}
	}
	return physics.CollisionBoxSet{Loaded: true}
}
