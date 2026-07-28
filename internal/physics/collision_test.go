package physics_test

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/physics"
)

var fullCube = core.AABB{Max: mgl32.Vec3{1, 1, 1}}

type boxSource map[core.BlockPos][]core.AABB

func boxes(entries ...boxEntry) boxSource {
	world := make(boxSource, len(entries))
	for _, entry := range entries {
		world[entry.position] = entry.boxes
	}
	return world
}

type boxEntry struct {
	position core.BlockPos
	boxes    []core.AABB
}

func block(x, y, z int32, collisionBoxes ...core.AABB) boxEntry {
	return boxEntry{position: core.BlockPos{X: x, Y: y, Z: z}, boxes: collisionBoxes}
}

func (s boxSource) CollisionBoxes(position core.BlockPos) physics.CollisionBoxSet {
	set := physics.CollisionBoxSet{Loaded: true}
	for i, box := range s[position] {
		set.Boxes[i] = box
		set.Count++
	}
	return set
}

type unknownSource struct{ position core.BlockPos }

func unknownAt(position core.BlockPos) unknownSource { return unknownSource{position: position} }

func (s unknownSource) CollisionBoxes(position core.BlockPos) physics.CollisionBoxSet {
	if position == s.position {
		return physics.CollisionBoxSet{}
	}
	return physics.CollisionBoxSet{Loaded: true}
}

func TestCollisionStopsOnFloorAndWall(t *testing.T) {
	world := boxes(
		block(0, 0, 0, fullCube),
		block(1, 1, 0, fullCube),
	)
	state := physics.State{
		Position: mgl32.Vec3{0.5, 1.2, 0.5},
		Velocity: mgl32.Vec3{10, -10, 0},
	}
	got := physics.Step(state, physics.Input{}, world).State
	if math.Abs(float64(got.Position.Y()-1)) > 1e-5 || !got.OnGround {
		t.Fatalf("未落在 y=1: %+v", got)
	}
	if got.Position.X() > 0.7+1e-5 || got.Velocity.X() != 0 {
		t.Fatalf("穿过 x=1 墙: %+v", got)
	}
}

func TestUnknownBlockIsClosedBoundary(t *testing.T) {
	world := unknownAt(core.BlockPos{X: 1, Y: 1, Z: 0})
	got := physics.Step(physics.State{
		Position: mgl32.Vec3{0.5, 1, 0.5},
		Velocity: mgl32.Vec3{10, 0, 0},
		OnGround: true,
	}, physics.Input{}, world)
	if !got.HitUnknown || got.State.Position.X() > 0.7+1e-5 {
		t.Fatalf("unknown 未阻挡: %+v", got)
	}
}

func TestWalkingOffLedgeClearsGroundInSameStep(t *testing.T) {
	world := boxes(block(0, 0, 0, fullCube))
	got := physics.Step(physics.State{
		Position: mgl32.Vec3{1.25, 1, 0.5},
		Velocity: mgl32.Vec3{4.3, 0, 0},
		OnGround: true,
	}, physics.Input{MoveX: 1}, world).State
	if got.OnGround {
		t.Fatalf("离开悬崖后仍 OnGround: %+v", got)
	}
}

func TestCollisionStopsAtCeilingAndClearsUpwardVelocity(t *testing.T) {
	world := boxes(block(0, 2, 0, fullCube))
	got := physics.Step(physics.State{
		Position: mgl32.Vec3{0.5, 0.1, 0.5},
		Velocity: mgl32.Vec3{0, 10, 0},
	}, physics.Input{}, world).State
	if math.Abs(float64(got.Position.Y()-0.2)) > 1e-5 || got.Velocity.Y() != 0 || got.OnGround {
		t.Fatalf("穿过天花板或未清除上升速度: %+v", got)
	}
}

func TestCollisionHandlesNegativeWorldCoordinates(t *testing.T) {
	world := boxes(block(-1, 0, 0, fullCube))
	got := physics.Step(physics.State{
		Position: mgl32.Vec3{0.5, 1, 0.5},
		Velocity: mgl32.Vec3{-30, 0, 0},
		OnGround: true,
	}, physics.Input{}, world).State
	if math.Abs(float64(got.Position.X()-0.3)) > 1e-5 || got.Velocity.X() != 0 {
		t.Fatalf("穿过负坐标方块: %+v", got)
	}
}

func TestCollisionResolvesCornerInYXZOrder(t *testing.T) {
	world := boxes(
		block(0, 0, 0, fullCube),
		block(1, 1, 0, fullCube),
		block(0, 1, 1, fullCube),
	)
	got := physics.Step(physics.State{
		Position: mgl32.Vec3{0.5, 1.2, 0.5},
		Velocity: mgl32.Vec3{10, -10, 10},
		OnGround: true,
	}, physics.Input{}, world).State
	want := mgl32.Vec3{0.7, 1, 0.7}
	if !got.Position.ApproxEqualThreshold(want, 1e-5) || got.Velocity != (mgl32.Vec3{}) || !got.OnGround {
		t.Fatalf("角落结果=%+v，想要位置=%v 且三个轴速度归零", got, want)
	}
}
