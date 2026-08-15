package physics

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

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
