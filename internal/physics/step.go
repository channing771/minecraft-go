package physics

import "github.com/go-gl/mathgl/mgl32"

func horizontalDistanceSquared(from, to mgl32.Vec3) float32 {
	dx, dz := to.X()-from.X(), to.Z()-from.Z()
	return dx*dx + dz*dz
}

func resolveStepMove(
	state State, displacement mgl32.Vec3, source CollisionSource, stepHeight float32,
) (moveResult, bool) {
	result := moveResult{position: state.Position}
	rise, riseClipped, riseUnknown := clipAxis(result.position, 1, stepHeight, source)
	result.position[1] += rise
	result.clipped[1] = riseClipped
	result.hitUnknown = riseUnknown
	if result.hitUnknown {
		return moveResult{}, false
	}

	for _, axis := range [...]int{0, 2} {
		moved, clipped, hitUnknown := clipAxis(result.position, axis, displacement[axis], source)
		result.position[axis] += moved
		result.clipped[axis] = clipped
		result.hitUnknown = result.hitUnknown || hitUnknown
	}
	if result.hitUnknown {
		return moveResult{}, false
	}

	down := -(rise + max(float32(0), -displacement.Y()))
	moved, clipped, hitUnknown := clipAxis(result.position, 1, down, source)
	result.position[1] += moved
	result.clipped[1] = result.clipped[1] || clipped
	result.hitUnknown = result.hitUnknown || hitUnknown
	if result.hitUnknown {
		return moveResult{}, false
	}

	_, result.onGround, hitUnknown = clipAxis(result.position, 1, -GroundProbe, source)
	result.hitUnknown = result.hitUnknown || hitUnknown
	if result.hitUnknown || !result.onGround {
		return moveResult{}, false
	}
	if clear, hitUnknown := boundsAreCollisionFree(result.position, source); !clear || hitUnknown {
		return moveResult{}, false
	}
	return result, true
}
