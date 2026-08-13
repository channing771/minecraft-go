package physics

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
)

var axisOrder = [...]int{1, 0, 2} // Y, X, Z

type moveResult struct {
	position   mgl32.Vec3
	clipped    [3]bool
	onGround   bool
	hitUnknown bool
}

func resolveMove(state State, displacement mgl32.Vec3, source CollisionSource) moveResult {
	result := moveResult{position: state.Position}
	for _, axis := range axisOrder {
		moved, clipped, hitUnknown := clipAxis(result.position, axis, displacement[axis], source)
		result.position[axis] += moved
		result.clipped[axis] = clipped
		result.hitUnknown = result.hitUnknown || hitUnknown
		if axis == 1 && clipped && displacement[axis] < 0 {
			result.onGround = true
		}
	}

	_, supported, hitUnknown := clipAxis(result.position, 1, -GroundProbe, source)
	result.onGround = supported
	result.hitUnknown = result.hitUnknown || hitUnknown
	return result
}

func clipAxis(feetPosition mgl32.Vec3, axis int, requested float32, source CollisionSource) (float32, bool, bool) {
	if requested == 0 {
		return 0, false, false
	}

	player := PlayerBounds(feetPosition)
	min, max := player.Min, player.Max
	if requested < 0 {
		min[axis] += requested
	} else {
		max[axis] += requested
	}

	minX, maxX := blockRange(min.X()-CollisionEpsilon, max.X()+CollisionEpsilon)
	minY, maxY := blockRange(min.Y()-CollisionEpsilon, max.Y()+CollisionEpsilon)
	minZ, maxZ := blockRange(min.Z()-CollisionEpsilon, max.Z()+CollisionEpsilon)
	clipped := requested
	wasClipped := false
	hitUnknown := false
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			for z := minZ; z <= maxZ; z++ {
				blockPosition := core.BlockPos{X: x, Y: y, Z: z}
				set := source.CollisionBoxes(blockPosition)
				if !set.Loaded {
					candidate, blocks := clipAgainst(feetPosition, player, axis, clipped, blockBounds(blockPosition, fullCubeBounds))
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
					candidate, blocks := clipAgainst(feetPosition, player, axis, clipped, blockBounds(blockPosition, set.Boxes[i]))
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

var fullCubeBounds = core.AABB{Max: mgl32.Vec3{1, 1, 1}}

func blockBounds(position core.BlockPos, local core.AABB) core.AABB {
	offset := mgl32.Vec3{float32(position.X), float32(position.Y), float32(position.Z)}
	return core.AABB{Min: local.Min.Add(offset), Max: local.Max.Add(offset)}
}

func clipAgainst(position mgl32.Vec3, player core.AABB, axis int, requested float32, collider core.AABB) (float32, bool) {
	if !overlapsOtherAxes(player, collider, axis) {
		return requested, false
	}
	if !endpointTouchesCollider(position, collider, axis, requested) {
		return requested, false
	}

	if requested > 0 {
		distance := collider.Min[axis] - player.Max[axis]
		if distance >= -CollisionEpsilon && distance <= requested+CollisionEpsilon {
			candidate := min(distance, requested)
			return safeCollisionDistance(position, collider, axis, requested, candidate), true
		}
		return requested, false
	}

	distance := collider.Max[axis] - player.Min[axis]
	if distance <= CollisionEpsilon && distance >= requested-CollisionEpsilon {
		candidate := max(distance, requested)
		return safeCollisionDistance(position, collider, axis, requested, candidate), true
	}
	return requested, false
}

func endpointTouchesCollider(position mgl32.Vec3, collider core.AABB, axis int, requested float32) bool {
	position[axis] += requested
	player := PlayerBounds(position)
	if requested > 0 {
		return player.Max[axis] >= collider.Min[axis]
	}
	return player.Min[axis] <= collider.Max[axis]
}

func safeCollisionDistance(position mgl32.Vec3, collider core.AABB, axis int, requested, distance float32) float32 {
	for {
		finalPosition := position
		finalPosition[axis] += distance
		finalBounds := PlayerBounds(finalPosition)
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

func overlapsOtherAxes(a, b core.AABB, axis int) bool {
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

func boundsAreCollisionFree(position mgl32.Vec3, source CollisionSource) (bool, bool) {
	player := PlayerBounds(position)
	minX, maxX := blockRange(player.Min.X(), player.Max.X())
	minY, maxY := blockRange(player.Min.Y(), player.Max.Y())
	minZ, maxZ := blockRange(player.Min.Z(), player.Max.Z())
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
					if boundsOverlap(player, blockBounds(core.BlockPos{X: x, Y: y, Z: z}, set.Boxes[i])) {
						return false, false
					}
				}
			}
		}
	}
	return true, false
}

func boundsOverlap(a, b core.AABB) bool {
	return a.Min.X() < b.Max.X() && a.Max.X() > b.Min.X() &&
		a.Min.Y() < b.Max.Y() && a.Max.Y() > b.Min.Y() &&
		a.Min.Z() < b.Max.Z() && a.Max.Z() > b.Min.Z()
}

func blockRange(min, max float32) (int32, int32) {
	return int32(math.Floor(float64(min))), int32(math.Floor(float64(max)))
}
