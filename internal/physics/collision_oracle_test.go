package physics_test

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/physics"
)

var oracleAxisOrder = [...]int{1, 0, 2} // Y, X, Z

type oracleMoveResult struct {
	position   mgl32.Vec3
	clipped    [3]bool
	onGround   bool
	hitUnknown bool
}

func oracleResolveCollision(
	state physics.State,
	displacement mgl32.Vec3,
	source physics.CollisionSource,
	beganGrounded bool,
	stepHeight float32,
) (oracleMoveResult, bool) {
	move := oracleResolveMove(state, displacement, source)
	if (move.clipped[0] || move.clipped[2]) &&
		(beganGrounded || move.onGround) &&
		(displacement.X() != 0 || displacement.Z() != 0) {
		if stepped, ok := oracleResolveStepMove(state, displacement, source, stepHeight); ok &&
			oracleHorizontalDistanceSquared(state.Position, stepped.position) >
				oracleHorizontalDistanceSquared(state.Position, move.position) {
			return stepped, true
		}
	}
	return move, false
}

func oracleResolveMove(state physics.State, displacement mgl32.Vec3, source physics.CollisionSource) oracleMoveResult {
	result := oracleMoveResult{position: state.Position}
	for _, axis := range oracleAxisOrder {
		moved, clipped, hitUnknown := oracleClipAxis(result.position, axis, displacement[axis], source)
		result.position[axis] += moved
		result.clipped[axis] = clipped
		result.hitUnknown = result.hitUnknown || hitUnknown
		if axis == 1 && clipped && displacement[axis] < 0 {
			result.onGround = true
		}
	}

	_, supported, hitUnknown := oracleClipAxis(result.position, 1, -physics.GroundProbe, source)
	result.onGround = supported
	result.hitUnknown = result.hitUnknown || hitUnknown
	return result
}

func oracleClipAxis(feetPosition mgl32.Vec3, axis int, requested float32, source physics.CollisionSource) (float32, bool, bool) {
	if requested == 0 {
		return 0, false, false
	}

	player := physics.PlayerBounds(feetPosition)
	min, max := player.Min, player.Max
	if requested < 0 {
		min[axis] += requested
	} else {
		max[axis] += requested
	}

	minX, maxX := oracleBlockRange(min.X()-physics.CollisionEpsilon, max.X()+physics.CollisionEpsilon)
	minY, maxY := oracleBlockRange(min.Y()-physics.CollisionEpsilon, max.Y()+physics.CollisionEpsilon)
	minZ, maxZ := oracleBlockRange(min.Z()-physics.CollisionEpsilon, max.Z()+physics.CollisionEpsilon)
	clipped := requested
	wasClipped := false
	hitUnknown := false
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			for z := minZ; z <= maxZ; z++ {
				blockPosition := core.BlockPos{X: x, Y: y, Z: z}
				set := source.CollisionBoxes(blockPosition)
				if !set.Loaded {
					candidate, blocks := oracleClipAgainst(feetPosition, player, axis, clipped, oracleBlockBounds(blockPosition, oracleFullCubeBounds))
					if blocks {
						hitUnknown = true
						clipped = candidate
						wasClipped = true
					}
					continue
				}
				count := int(set.Count)
				if count > len(set.Boxes) {
					count = len(set.Boxes)
				}
				for i := 0; i < count; i++ {
					candidate, blocks := oracleClipAgainst(feetPosition, player, axis, clipped, oracleBlockBounds(blockPosition, set.Boxes[i]))
					if blocks {
						clipped = candidate
						wasClipped = true
					}
				}
			}
		}
	}
	return clipped, wasClipped, hitUnknown
}

var oracleFullCubeBounds = core.AABB{Max: mgl32.Vec3{1, 1, 1}}

func oracleBlockBounds(position core.BlockPos, local core.AABB) core.AABB {
	offset := mgl32.Vec3{float32(position.X), float32(position.Y), float32(position.Z)}
	return core.AABB{Min: local.Min.Add(offset), Max: local.Max.Add(offset)}
}

func oracleClipAgainst(position mgl32.Vec3, player core.AABB, axis int, requested float32, collider core.AABB) (float32, bool) {
	if !oracleOverlapsOtherAxes(player, collider, axis) {
		return requested, false
	}
	if !oracleEndpointTouchesCollider(position, collider, axis, requested) {
		return requested, false
	}

	if requested > 0 {
		distance := collider.Min[axis] - player.Max[axis]
		if distance >= -physics.CollisionEpsilon && distance <= requested+physics.CollisionEpsilon {
			candidate := min(distance, requested)
			return oracleSafeCollisionDistance(position, collider, axis, requested, candidate), true
		}
		return requested, false
	}

	distance := collider.Max[axis] - player.Min[axis]
	if distance <= physics.CollisionEpsilon && distance >= requested-physics.CollisionEpsilon {
		candidate := max(distance, requested)
		return oracleSafeCollisionDistance(position, collider, axis, requested, candidate), true
	}
	return requested, false
}

func oracleEndpointTouchesCollider(position mgl32.Vec3, collider core.AABB, axis int, requested float32) bool {
	position[axis] += requested
	player := physics.PlayerBounds(position)
	if requested > 0 {
		return player.Max[axis] >= collider.Min[axis]
	}
	return player.Min[axis] <= collider.Max[axis]
}

func oracleSafeCollisionDistance(position mgl32.Vec3, collider core.AABB, axis int, requested, distance float32) float32 {
	for {
		finalPosition := position
		finalPosition[axis] += distance
		finalBounds := physics.PlayerBounds(finalPosition)
		if requested > 0 {
			if finalBounds.Max[axis] <= collider.Min[axis] {
				return distance
			}
			distance = math.Nextafter32(distance, float32(math.Inf(-1)))
			continue
		}
		if finalBounds.Min[axis] >= collider.Max[axis] {
			return distance
		}
		distance = math.Nextafter32(distance, float32(math.Inf(1)))
	}
}

func oracleOverlapsOtherAxes(a, b core.AABB, axis int) bool {
	for other := 0; other < 3; other++ {
		if other == axis {
			continue
		}
		if a.Min[other] >= b.Max[other] || a.Max[other] <= b.Min[other] {
			return false
		}
	}
	return true
}

func oracleBoundsAreCollisionFree(position mgl32.Vec3, source physics.CollisionSource) (bool, bool) {
	player := physics.PlayerBounds(position)
	minX, maxX := oracleBlockRange(player.Min.X(), player.Max.X())
	minY, maxY := oracleBlockRange(player.Min.Y(), player.Max.Y())
	minZ, maxZ := oracleBlockRange(player.Min.Z(), player.Max.Z())
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			for z := minZ; z <= maxZ; z++ {
				set := source.CollisionBoxes(core.BlockPos{X: x, Y: y, Z: z})
				if !set.Loaded {
					return false, true
				}
				count := int(set.Count)
				if count > len(set.Boxes) {
					count = len(set.Boxes)
				}
				for i := 0; i < count; i++ {
					if oracleBoundsOverlap(player, oracleBlockBounds(core.BlockPos{X: x, Y: y, Z: z}, set.Boxes[i])) {
						return false, false
					}
				}
			}
		}
	}
	return true, false
}

func oracleBoundsOverlap(a, b core.AABB) bool {
	return a.Min.X() < b.Max.X() && a.Max.X() > b.Min.X() &&
		a.Min.Y() < b.Max.Y() && a.Max.Y() > b.Min.Y() &&
		a.Min.Z() < b.Max.Z() && a.Max.Z() > b.Min.Z()
}

func oracleBlockRange(min, max float32) (int32, int32) {
	return int32(math.Floor(float64(min))), int32(math.Floor(float64(max)))
}

func oracleHorizontalDistanceSquared(from, to mgl32.Vec3) float32 {
	dx, dz := to.X()-from.X(), to.Z()-from.Z()
	return dx*dx + dz*dz
}

func oracleResolveStepMove(
	state physics.State, displacement mgl32.Vec3, source physics.CollisionSource, stepHeight float32,
) (oracleMoveResult, bool) {
	result := oracleMoveResult{position: state.Position}
	rise, riseClipped, riseUnknown := oracleClipAxis(result.position, 1, stepHeight, source)
	result.position[1] += rise
	result.clipped[1] = riseClipped
	result.hitUnknown = riseUnknown
	if result.hitUnknown {
		return oracleMoveResult{}, false
	}

	for _, axis := range [...]int{0, 2} {
		moved, clipped, hitUnknown := oracleClipAxis(result.position, axis, displacement[axis], source)
		result.position[axis] += moved
		result.clipped[axis] = clipped
		result.hitUnknown = result.hitUnknown || hitUnknown
	}
	if result.hitUnknown {
		return oracleMoveResult{}, false
	}

	down := -(rise + max(float32(0), -displacement.Y()))
	moved, clipped, hitUnknown := oracleClipAxis(result.position, 1, down, source)
	result.position[1] += moved
	result.clipped[1] = result.clipped[1] || clipped
	result.hitUnknown = result.hitUnknown || hitUnknown
	if result.hitUnknown {
		return oracleMoveResult{}, false
	}

	_, result.onGround, hitUnknown = oracleClipAxis(result.position, 1, -physics.GroundProbe, source)
	result.hitUnknown = result.hitUnknown || hitUnknown
	if result.hitUnknown || !result.onGround {
		return oracleMoveResult{}, false
	}
	if clear, hitUnknown := oracleBoundsAreCollisionFree(result.position, source); !clear || hitUnknown {
		return oracleMoveResult{}, false
	}
	return result, true
}
