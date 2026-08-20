package physics

import (
	"encoding/binary"
	"math"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/nativeabi"
)

const (
	// stepHeaderBytes 是 StepInput header v2 的长度：v1 的 128 字节已被排满，
	// 浸没标志与四个水中 tunable 需要新的字节，故扩到 160（仍是 32 的整数倍）。
	// 这是同一个 engine ABI v5 内的 header 扩展，不再升 ABI 版本。
	stepHeaderBytes  = 160
	stepOutputBytes  = 32
	stepRegularCells = 135
	stepRegularBytes = stepHeaderBytes + stepRegularCells*collisionCellBytes

	// stepLayoutVersion 是 StepInput header 的布局版本。v1 → v2 的唯一差异是
	// 追加浸没标志与四个水中 tunable；Rust 侧只接受当前版本，混装立即报错。
	stepLayoutVersion = 2
)

// Step 推进一个固定步：校验与编码在 Go，积分 + 碰撞解析 + 速度裁剪在 Rust engine。
//
// 参数在函数入口取一次快照，全程使用该快照，因此单次固定步内参数不会中途变化。
func Step(state State, input Input, source CollisionSource) StepResult {
	tunables := ActiveTunables()
	validate(state, input)
	yawSin := float32(math.Sin(float64(input.Yaw)))
	yawCos := float32(math.Cos(float64(input.Yaw)))
	sweepMin, sweepMax := stepSweepBounds(state, input, tunables, yawSin, yawCos)
	prism := stepPrismFor(state.Position, sweepMin, sweepMax, tunables.StepHeight)
	var regular [stepRegularBytes]byte
	var bytes []byte
	if prism.bytes <= stepRegularBytes {
		bytes = regular[:prism.bytes]
	} else {
		bytes = make([]byte, prism.bytes)
	}
	encodeStepInput(bytes, prism, state, input, tunables, yawSin, yawCos, sweepMin, sweepMax, source)
	var output [stepOutputBytes]byte
	nativeabi.PhysicsStep(bytes, output[:])
	return decodeStepOutput(output[:])
}

// movementTargetFromYaw 与 Rust 的 movement_target 逐位一致（三角已由调用方算好）。
func movementTargetFromYaw(moveX, moveZ int8, walkSpeed, yawSin, yawCos float32) mgl32.Vec3 {
	forward := mgl32.Vec3{-yawSin, 0, -yawCos}
	right := mgl32.Vec3{yawCos, 0, -yawSin}
	intent := right.Mul(float32(moveX)).Add(forward.Mul(float32(moveZ)))
	if intent.Len() == 0 {
		return mgl32.Vec3{}
	}
	return intent.Normalize().Mul(walkSpeed)
}

// stepSweepBounds 计算积分位移的凸包界。Rust 积分后自检位移落在界内。
//
// 水平轴取 [min(0,v,t)·dt, max(0,v,t)·dt]（t 为 moveToward 后的目标速度分量）；
// 垂直轴按 jump/fallen/−terminal 分支取凸包。界必须与 Rust 积分逐位一致，
// 由 step 级差分测试锁定。
func stepSweepBounds(state State, input Input, tunables Tunables, yawSin, yawCos float32) (mgl32.Vec3, mgl32.Vec3) {
	target := movementTargetFromYaw(input.MoveX, input.MoveZ, tunables.WalkSpeed, yawSin, yawCos)
	horizontal := mgl32.Vec3{state.Velocity.X(), 0, state.Velocity.Z()}
	if state.OnGround {
		if target.Len() == 0 {
			horizontal = moveToward(horizontal, mgl32.Vec3{}, tunables.GroundDeceleration*FixedDeltaSeconds)
		} else {
			horizontal = moveToward(horizontal, target, tunables.GroundAcceleration*FixedDeltaSeconds)
		}
	} else {
		horizontal = moveToward(horizontal, target, tunables.AirAcceleration*FixedDeltaSeconds)
		if horizontal.Len() > tunables.WalkSpeed {
			horizontal = horizontal.Normalize().Mul(tunables.WalkSpeed)
		}
	}
	if input.BodyInFluid {
		horizontal = horizontal.Mul(tunables.FluidHorizontalDrag)
	}
	vx, vz := state.Velocity.X(), state.Velocity.Z()
	tx, tz := horizontal.X(), horizontal.Z()
	dt := FixedDeltaSeconds
	var minimum, maximum mgl32.Vec3
	minimum[0] = min3(0, vx, tx) * dt
	maximum[0] = max3(0, vx, tx) * dt
	minimum[2] = min3(0, vz, tz) * dt
	maximum[2] = max3(0, vz, tz) * dt
	vy := state.Velocity.Y()
	gravity, terminal := tunables.Gravity, tunables.TerminalFallSpeed
	if input.BodyInFluid {
		gravity, terminal = tunables.FluidGravity, tunables.FluidSinkSpeed
	}
	switch {
	case input.BodyInFluid && input.Jump:
		// 水中上升是「每 tick 直接赋值」的持续上浮，不看 OnGround，也不是一次
		// 性冲量，因此位移恒为 FluidAscendSpeed*dt。
		minimum[1] = min(0, tunables.FluidAscendSpeed*dt)
		maximum[1] = max(0, tunables.FluidAscendSpeed*dt)
	case state.OnGround && input.Jump:
		maximum[1] = tunables.JumpSpeed * dt
	default:
		fallen := vy - gravity*dt
		if fallen >= -terminal {
			minimum[1] = min3(0, vy, fallen) * dt
			maximum[1] = max3(0, vy, fallen) * dt
		} else {
			minimum[1] = min3(0, vy, -terminal) * dt
			maximum[1] = max3(0, vy, -terminal) * dt
		}
	}
	return minimum, maximum
}

func min3(a, b, c float32) float32 { return min(a, min(b, c)) }
func max3(a, b, c float32) float32 { return max(a, max(b, c)) }

func encodeStepInput(
	bytes []byte,
	prism collisionPrism,
	state State,
	input Input,
	tunables Tunables,
	yawSin, yawCos float32,
	sweepMin, sweepMax mgl32.Vec3,
	source CollisionSource,
) {
	if len(bytes) != prism.bytes {
		panic("physics: step input 缓冲区长度非法")
	}
	clear(bytes)
	copy(bytes[:4], "MGP1")
	binary.LittleEndian.PutUint32(bytes[4:8], stepLayoutVersion)
	putCollisionVec3(bytes[8:20], state.Position)
	putCollisionVec3(bytes[20:32], state.Velocity)
	if state.OnGround {
		bytes[32] = 1
	}
	if input.Jump {
		bytes[33] = 1
	}
	bytes[34] = byte(input.MoveX)
	bytes[35] = byte(input.MoveZ)
	putCollisionFloat(bytes[36:40], yawSin)
	putCollisionFloat(bytes[40:44], yawCos)
	putCollisionFloat(bytes[44:48], FixedDeltaSeconds)
	for index, value := range [...]float32{
		tunables.StepHeight, tunables.WalkSpeed, tunables.GroundAcceleration,
		tunables.GroundDeceleration, tunables.AirAcceleration, tunables.JumpSpeed,
		tunables.Gravity, tunables.TerminalFallSpeed,
	} {
		putCollisionFloat(bytes[48+index*4:52+index*4], value)
	}
	putCollisionFloat(bytes[80:84], sweepMin.X())
	putCollisionFloat(bytes[84:88], sweepMax.X())
	putCollisionFloat(bytes[88:92], sweepMin.Y())
	putCollisionFloat(bytes[92:96], sweepMax.Y())
	putCollisionFloat(bytes[96:100], sweepMin.Z())
	putCollisionFloat(bytes[100:104], sweepMax.Z())
	for index, value := range [...]int32{prism.origin.X, prism.origin.Y, prism.origin.Z} {
		binary.LittleEndian.PutUint32(bytes[104+index*4:108+index*4], uint32(value))
	}
	for index, value := range prism.dimensions {
		binary.LittleEndian.PutUint32(bytes[116+index*4:120+index*4], value)
	}
	// v2 新增区：128 是身体浸没标志，129..132 保留为 0；132..148 是四个水中
	// tunable；148..160 同样保留为 0。保留字节由上面的 clear 置零，Rust 侧
	// 逐字节要求为 0，未来扩字段时会立刻暴露版本不匹配。
	if input.BodyInFluid {
		bytes[128] = 1
	}
	for index, value := range [...]float32{
		tunables.FluidGravity, tunables.FluidSinkSpeed,
		tunables.FluidAscendSpeed, tunables.FluidHorizontalDrag,
	} {
		putCollisionFloat(bytes[132+index*4:136+index*4], value)
	}

	offset := stepHeaderBytes
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
					bytes[offset] = 1
				}
				count := min(int(set.Count), len(set.Boxes))
				bytes[offset+1] = byte(count)
				for boxIndex := range count {
					box := set.Boxes[boxIndex]
					components := [...]float32{
						box.Min.X(), box.Min.Y(), box.Min.Z(),
						box.Max.X(), box.Max.Y(), box.Max.Z(),
					}
					for componentIndex, value := range components {
						putCollisionFloat(bytes[offset+4+boxIndex*24+componentIndex*4:], value)
					}
				}
				offset += collisionCellBytes
			}
		}
	}
	if offset != len(bytes) {
		panic("physics: step input 编码不完整")
	}
}

func decodeStepOutput(output []byte) StepResult {
	if len(output) != stepOutputBytes || output[24]&^byte(7) != 0 ||
		output[25] > 1 || output[26] > 1 || output[27] > 1 ||
		output[28] != 0 || output[29] != 0 || output[30] != 0 || output[31] != 0 {
		panic("physics: native step output 非法")
	}
	return StepResult{
		State: State{
			Position: mgl32.Vec3{
				math.Float32frombits(binary.LittleEndian.Uint32(output[0:4])),
				math.Float32frombits(binary.LittleEndian.Uint32(output[4:8])),
				math.Float32frombits(binary.LittleEndian.Uint32(output[8:12])),
			},
			Velocity: mgl32.Vec3{
				math.Float32frombits(binary.LittleEndian.Uint32(output[12:16])),
				math.Float32frombits(binary.LittleEndian.Uint32(output[16:20])),
				math.Float32frombits(binary.LittleEndian.Uint32(output[20:24])),
			},
			OnGround: output[25] == 1,
		},
		UsedStep:   output[26] == 1,
		HitUnknown: output[27] == 1,
	}
}
