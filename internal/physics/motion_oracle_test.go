package physics_test

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/physics"
)

// oracleStep 是旧 Go 积分实现的逐字副本（生产曾位于 motion.go），
// （三处 sanctioned 差异：tunables 作参数、省去 validate、collision 走 oracleResolveCollision）
// 只用于与 native 生产路径做逐位奇偶断言。不得在生产代码引用。
func oracleStep(
	state physics.State,
	input physics.Input,
	source physics.CollisionSource,
	tunables physics.Tunables,
) physics.StepResult {
	beganGrounded := state.OnGround

	target := oracleMovementTarget(input, tunables.WalkSpeed)
	horizontal := mgl32.Vec3{state.Velocity.X(), 0, state.Velocity.Z()}
	if state.OnGround {
		if target.Len() == 0 {
			horizontal = oracleMoveToward(horizontal, mgl32.Vec3{}, tunables.GroundDeceleration*physics.FixedDeltaSeconds)
		} else {
			horizontal = oracleMoveToward(horizontal, target, tunables.GroundAcceleration*physics.FixedDeltaSeconds)
		}
	} else {
		horizontal = oracleMoveToward(horizontal, target, tunables.AirAcceleration*physics.FixedDeltaSeconds)
		if horizontal.Len() > tunables.WalkSpeed {
			horizontal = horizontal.Normalize().Mul(tunables.WalkSpeed)
		}
	}
	// 水中分支是 Rust integrate 的逐字镜像（本变更任务 5.3 同批加入 oracle）。
	if input.BodyInFluid {
		horizontal = horizontal.Mul(tunables.FluidHorizontalDrag)
	}
	state.Velocity[0], state.Velocity[2] = horizontal.X(), horizontal.Z()

	switch {
	case input.BodyInFluid && input.Jump:
		state.Velocity[1] = tunables.FluidAscendSpeed
		state.OnGround = false
	case state.OnGround && input.Jump:
		state.Velocity[1] = tunables.JumpSpeed
		state.OnGround = false
	default:
		gravity, terminal := tunables.Gravity, tunables.TerminalFallSpeed
		if input.BodyInFluid {
			gravity, terminal = tunables.FluidGravity, tunables.FluidSinkSpeed
		}
		state.Velocity[1] = max(
			state.Velocity.Y()-gravity*physics.FixedDeltaSeconds,
			-terminal,
		)
	}
	displacement := state.Velocity.Mul(physics.FixedDeltaSeconds)
	move, usedStep := oracleResolveCollision(
		state,
		displacement,
		source,
		beganGrounded,
		tunables.StepHeight,
	)
	state.Position = move.position
	state.OnGround = move.onGround
	for axis, clipped := range move.clipped {
		if clipped {
			state.Velocity[axis] = 0
		}
	}

	return physics.StepResult{
		State:      state,
		UsedStep:   usedStep,
		HitUnknown: move.hitUnknown,
	}
}

func oracleMovementTarget(input physics.Input, walkSpeed float32) mgl32.Vec3 {
	yawSin := float32(math.Sin(float64(input.Yaw)))
	yawCos := float32(math.Cos(float64(input.Yaw)))
	forward := mgl32.Vec3{-yawSin, 0, -yawCos}
	right := mgl32.Vec3{yawCos, 0, -yawSin}
	intent := right.Mul(float32(input.MoveX)).Add(forward.Mul(float32(input.MoveZ)))
	if intent.Len() == 0 {
		return mgl32.Vec3{}
	}
	return intent.Normalize().Mul(walkSpeed)
}

func oracleMoveToward(current, target mgl32.Vec3, maximumDelta float32) mgl32.Vec3 {
	delta := target.Sub(current)
	if length := delta.Len(); length <= maximumDelta {
		return target
	}
	return current.Add(delta.Mul(maximumDelta / delta.Len()))
}
