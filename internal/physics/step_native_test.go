package physics_test

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/physics"
)

const (
	testStepHeaderBytes  = 128
	testStepCellBytes    = 196
	testStepOutputBytes  = 32
	testStepMaxCells     = 4096
	testStepMaxBytes     = testStepHeaderBytes + testStepMaxCells*testStepCellBytes
	testStepRegularCells = 135
	testStepRegularBytes = testStepHeaderBytes + testStepRegularCells*testStepCellBytes
)

func putTestStepVec3(output []byte, value mgl32.Vec3) {
	for index := range 3 {
		binary.LittleEndian.PutUint32(output[index*4:index*4+4], math.Float32bits(value[index]))
	}
}

func putTestStepFloat(output []byte, value float32) {
	binary.LittleEndian.PutUint32(output, math.Float32bits(value))
}

// testEncodeStepInputInto 是生产 encodeStepInput 的镜像（测试自持副本，不依赖生产实现细节之外的 ABI）。
func testEncodeStepInputInto(
	input []byte,
	prism testStepPrism,
	state physics.State,
	moveX, moveZ int8,
	jump bool,
	yawSin, yawCos float32,
	tunables physics.Tunables,
	sweepMin, sweepMax mgl32.Vec3,
	source physics.CollisionSource,
) {
	clear(input)
	copy(input[:4], "MGP1")
	binary.LittleEndian.PutUint32(input[4:8], 1)
	putTestStepVec3(input[8:20], state.Position)
	putTestStepVec3(input[20:32], state.Velocity)
	if state.OnGround {
		input[32] = 1
	}
	if jump {
		input[33] = 1
	}
	input[34] = byte(moveX)
	input[35] = byte(moveZ)
	putTestStepFloat(input[36:40], yawSin)
	putTestStepFloat(input[40:44], yawCos)
	putTestStepFloat(input[44:48], physics.FixedDeltaSeconds)
	for index, value := range [...]float32{
		tunables.StepHeight, tunables.WalkSpeed, tunables.GroundAcceleration,
		tunables.GroundDeceleration, tunables.AirAcceleration, tunables.JumpSpeed,
		tunables.Gravity, tunables.TerminalFallSpeed,
	} {
		putTestStepFloat(input[48+index*4:52+index*4], value)
	}
	putTestStepFloat(input[80:84], sweepMin.X())
	putTestStepFloat(input[84:88], sweepMax.X())
	putTestStepFloat(input[88:92], sweepMin.Y())
	putTestStepFloat(input[92:96], sweepMax.Y())
	putTestStepFloat(input[96:100], sweepMin.Z())
	putTestStepFloat(input[100:104], sweepMax.Z())
	for index, value := range [...]int32{prism.origin.X, prism.origin.Y, prism.origin.Z} {
		binary.LittleEndian.PutUint32(input[104+index*4:108+index*4], uint32(value))
	}
	for index, value := range prism.dimensions {
		binary.LittleEndian.PutUint32(input[116+index*4:120+index*4], value)
	}
	offset := testStepHeaderBytes
	for y := uint32(0); y < prism.dimensions[1]; y++ {
		for x := uint32(0); x < prism.dimensions[0]; x++ {
			for z := uint32(0); z < prism.dimensions[2]; z++ {
				position := core.BlockPos{
					X: prism.origin.X + int32(x),
					Y: prism.origin.Y + int32(y),
					Z: prism.origin.Z + int32(z),
				}
				set := source.CollisionBoxes(position)
				if set.Loaded {
					input[offset] = 1
				}
				count := min(int(set.Count), len(set.Boxes))
				input[offset+1] = byte(count)
				for boxIndex := range count {
					box := set.Boxes[boxIndex]
					components := [...]float32{
						box.Min.X(), box.Min.Y(), box.Min.Z(),
						box.Max.X(), box.Max.Y(), box.Max.Z(),
					}
					for componentIndex, value := range components {
						putTestStepFloat(input[offset+4+boxIndex*24+componentIndex*4:], value)
					}
				}
				offset += testStepCellBytes
			}
		}
	}
	if offset != len(input) {
		panic("test step input 编码不完整")
	}
}

type testStepPrism struct {
	origin     core.BlockPos
	dimensions [3]uint32
	cells      int
	bytes      int
}

// testStepPrismFor 复刻生产 prism 构建（bounds 版本）。
func testStepPrismFor(position, sweepMin, sweepMax mgl32.Vec3, stepHeight float32) testStepPrism {
	halfWidth := physics.PlayerWidth / 2
	minimum := mgl32.Vec3{
		position.X() + sweepMin.X() - halfWidth - physics.CollisionEpsilon,
		position.Y() + min(float32(0), sweepMin.Y(), stepHeight) - physics.GroundProbe - physics.CollisionEpsilon,
		position.Z() + sweepMin.Z() - halfWidth - physics.CollisionEpsilon,
	}
	maximum := mgl32.Vec3{
		position.X() + sweepMax.X() + halfWidth + physics.CollisionEpsilon,
		position.Y() + max(float32(0), sweepMax.Y(), stepHeight) + physics.PlayerHeight + physics.CollisionEpsilon,
		position.Z() + sweepMax.Z() + halfWidth + physics.CollisionEpsilon,
	}
	origin := core.BlockPos{
		X: int32(math.Floor(float64(minimum.X()))),
		Y: int32(math.Floor(float64(minimum.Y()))),
		Z: int32(math.Floor(float64(minimum.Z()))),
	}
	end := core.BlockPos{
		X: int32(math.Floor(float64(maximum.X()))),
		Y: int32(math.Floor(float64(maximum.Y()))),
		Z: int32(math.Floor(float64(maximum.Z()))),
	}
	dimensions := [3]uint32{
		uint32(end.X - origin.X + 1),
		uint32(end.Y - origin.Y + 1),
		uint32(end.Z - origin.Z + 1),
	}
	cells := int(dimensions[0]) * int(dimensions[1]) * int(dimensions[2])
	return testStepPrism{
		origin:     origin,
		dimensions: dimensions,
		cells:      cells,
		bytes:      testStepHeaderBytes + cells*testStepCellBytes,
	}
}

func TestStepInputLayoutV1(t *testing.T) {
	state := physics.State{
		Position: mgl32.Vec3{0.5, 1, 0.5},
		Velocity: mgl32.Vec3{4.3, -1.6, 0},
		OnGround: true,
	}
	source := testCollisionWorld{}
	prism := testStepPrismFor(state.Position, mgl32.Vec3{}, mgl32.Vec3{0.2, 0.1, 0}, physics.DefaultTunables().StepHeight)
	input := make([]byte, prism.bytes)
	testEncodeStepInputInto(
		input, prism, state, 1, -1, true,
		0.25, 0.9682458,
		physics.DefaultTunables(),
		mgl32.Vec3{}, mgl32.Vec3{0.2, 0.1, 0},
		source,
	)
	if len(input) != prism.bytes || prism.bytes != testStepHeaderBytes+prism.cells*testStepCellBytes {
		t.Fatalf("step input 长度=%d，want %d", len(input), prism.bytes)
	}
	if got := string(input[:4]); got != "MGP1" {
		t.Fatalf("magic=%q，want MGP1", got)
	}
	if got := binary.LittleEndian.Uint32(input[4:8]); got != 1 {
		t.Fatalf("layout version=%d，want 1", got)
	}
	if input[32] != 1 || input[33] != 1 {
		t.Fatalf("on_ground/jump=%v，want 1,1", input[32:34])
	}
	if input[34] != 1 || input[35] != 255 {
		t.Fatalf("move_x/move_z=%d/%d，want 1/-1", input[34], int8(input[35]))
	}
	if got := math.Float32frombits(binary.LittleEndian.Uint32(input[36:40])); math.Float32bits(got) != math.Float32bits(0.25) {
		t.Fatalf("yaw_sin bits=%08x，want %08x", math.Float32bits(got), math.Float32bits(float32(0.25)))
	}
	if got := binary.LittleEndian.Uint32(input[44:48]); math.Float32bits(math.Float32frombits(got)) != math.Float32bits(physics.FixedDeltaSeconds) {
		t.Fatalf("dt bits=%08x，want %08x", got, math.Float32bits(physics.FixedDeltaSeconds))
	}
	for index, value := range [...]float32{
		physics.DefaultTunables().StepHeight, physics.DefaultTunables().WalkSpeed,
		physics.DefaultTunables().GroundAcceleration, physics.DefaultTunables().GroundDeceleration,
		physics.DefaultTunables().AirAcceleration, physics.DefaultTunables().JumpSpeed,
		physics.DefaultTunables().Gravity, physics.DefaultTunables().TerminalFallSpeed,
	} {
		if got := math.Float32frombits(binary.LittleEndian.Uint32(input[48+index*4 : 52+index*4])); math.Float32bits(got) != math.Float32bits(value) {
			t.Fatalf("tunable[%d] bits=%08x，want %08x", index, math.Float32bits(got), math.Float32bits(value))
		}
	}
	if got := math.Float32frombits(binary.LittleEndian.Uint32(input[28:32])); math.Float32bits(got) != math.Float32bits(0) {
		t.Fatalf("velocity z bits=%08x，want +0", math.Float32bits(got))
	}
}

func TestStepProductionMatchesGoIntegrationOracle(t *testing.T) {
	previousTunables := physics.ActiveTunables()
	t.Cleanup(func() { physics.SetTunables(previousTunables) })
	physics.SetTunables(physics.DefaultTunables())

	floor := func() testCollisionWorld {
		world := testCollisionWorld{}
		for x := int32(-3); x <= 3; x++ {
			for z := int32(-3); z <= 3; z++ {
				world[core.BlockPos{X: x, Y: 0, Z: z}] = physics.CollisionBoxSet{Loaded: true, Count: 1, Boxes: [8]core.AABB{fullCube}}
			}
		}
		return world
	}
	negativeZeroZ := math.Float32frombits(1 << 31)

	tests := []struct {
		name  string
		state physics.State
		input physics.Input
		world testCollisionWorld
	}{
		{name: "grounded diagonal walk", state: physics.State{Position: mgl32.Vec3{0.5, 1, 0.5}, OnGround: true}, input: physics.Input{MoveX: 1, MoveZ: 1}, world: floor()},
		{name: "grounded decel to stop", state: physics.State{Position: mgl32.Vec3{0.5, 1, 0.5}, Velocity: mgl32.Vec3{10, 0, -3}, OnGround: true}, input: physics.Input{}, world: floor()},
		{name: "jump from ground", state: physics.State{Position: mgl32.Vec3{0.5, 1, 0.5}, OnGround: true}, input: physics.Input{Jump: true, MoveX: 1}, world: floor()},
		{name: "airborne gravity", state: physics.State{Position: mgl32.Vec3{0.5, 3.2, 0.5}, Velocity: mgl32.Vec3{4, 8.4, 0}}, input: physics.Input{MoveX: -1, Yaw: 1.25}, world: floor()},
		{name: "terminal fall clamp", state: physics.State{Position: mgl32.Vec3{0.5, 40, 0.5}, Velocity: mgl32.Vec3{0, -78, 0}}, input: physics.Input{}, world: floor()},
		{name: "negative zero z velocity", state: physics.State{Position: mgl32.Vec3{0.5, 1, 0.5}, Velocity: mgl32.Vec3{0, 0, negativeZeroZ}, OnGround: true}, input: physics.Input{MoveX: 1}, world: floor()},
		{name: "unknown cell blocks path", state: physics.State{Position: mgl32.Vec3{0.5, 1, 0.5}, Velocity: mgl32.Vec3{4.3, 0, 0}, OnGround: true}, input: physics.Input{MoveX: 1}, world: testCollisionWorld{{X: 1, Y: 1, Z: 0}: {}}},
		{name: "half block step", state: groundedTowardObstacle(), input: physics.Input{MoveX: 1}, world: testCollisionWorld{
			{X: 0, Y: 0, Z: 0}: {Loaded: true, Count: 1, Boxes: [8]core.AABB{fullCube}},
			{X: 1, Y: 0, Z: 0}: {Loaded: true, Count: 1, Boxes: [8]core.AABB{fullCube}},
			{X: 1, Y: 1, Z: 0}: {Loaded: true, Count: 1, Boxes: [8]core.AABB{{Max: mgl32.Vec3{1, 0.5, 1}}}},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			testAssertProductionStepMatchesOracle(t, test.state, test.input, test.world)
		})
	}
}
