package physics

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// Step 推进一个固定步。碰撞解析将在后续阶段接入；本阶段按自由空间积分。
func Step(state State, input Input, _ CollisionSource) StepResult {
	validate(state, input)

	target := movementTarget(input)
	horizontal := mgl32.Vec3{state.Velocity.X(), 0, state.Velocity.Z()}
	if state.OnGround {
		if target.Len() == 0 {
			horizontal = moveToward(horizontal, mgl32.Vec3{}, GroundDeceleration*FixedDeltaSeconds)
		} else {
			horizontal = moveToward(horizontal, target, GroundAcceleration*FixedDeltaSeconds)
		}
	} else {
		horizontal = moveToward(horizontal, target, AirAcceleration*FixedDeltaSeconds)
		if horizontal.Len() > WalkSpeed {
			horizontal = horizontal.Normalize().Mul(WalkSpeed)
		}
	}
	state.Velocity[0], state.Velocity[2] = horizontal.X(), horizontal.Z()

	if state.OnGround && input.Jump {
		state.Velocity[1] = JumpSpeed
		state.OnGround = false
	} else {
		state.Velocity[1] = max(state.Velocity.Y()-Gravity*FixedDeltaSeconds, -TerminalFallSpeed)
	}
	state.Position = state.Position.Add(state.Velocity.Mul(FixedDeltaSeconds))

	return StepResult{State: state}
}

func movementTarget(input Input) mgl32.Vec3 {
	yawSin := float32(math.Sin(float64(input.Yaw)))
	yawCos := float32(math.Cos(float64(input.Yaw)))
	forward := mgl32.Vec3{-yawSin, 0, -yawCos}
	right := mgl32.Vec3{yawCos, 0, -yawSin}
	intent := right.Mul(float32(input.MoveX)).Add(forward.Mul(float32(input.MoveZ)))
	if intent.Len() == 0 {
		return mgl32.Vec3{}
	}
	return intent.Normalize().Mul(WalkSpeed)
}

func moveToward(current, target mgl32.Vec3, maximumDelta float32) mgl32.Vec3 {
	delta := target.Sub(current)
	if length := delta.Len(); length <= maximumDelta {
		return target
	}
	return current.Add(delta.Mul(maximumDelta / delta.Len()))
}

func validate(state State, input Input) {
	if input.MoveX < -1 || input.MoveX > 1 || input.MoveZ < -1 || input.MoveZ > 1 {
		panic("physics: invalid movement input")
	}
	if !finiteVector(state.Position) || !finiteVector(state.Velocity) || !finite(input.Yaw) {
		panic("physics: non-finite state or input")
	}
}

func finiteVector(v mgl32.Vec3) bool {
	return finite(v.X()) && finite(v.Y()) && finite(v.Z())
}

func finite(v float32) bool {
	return !math.IsNaN(float64(v)) && !math.IsInf(float64(v), 0)
}
