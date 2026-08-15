package core

import (
	"errors"
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// 以下 DDA 是 Task 8 切换前生产 raycast.go 的机械副本，只作独立测试 oracle。
func oracleRaycastBlocks(
	origin, direction mgl32.Vec3,
	maxDistance float32,
	solid func(BlockPos) (bool, error),
) (RayHit, bool, error) {
	if !oracleFiniteVec3(origin) || !oracleFiniteVec3(direction) ||
		!oracleFiniteFloat32(maxDistance) || maxDistance <= 0 {
		return RayHit{}, false, errors.New("core: invalid ray")
	}
	if solid == nil {
		return RayHit{}, false, errors.New("core: nil ray lookup")
	}

	length := math.Hypot(
		math.Hypot(float64(direction[0]), float64(direction[1])),
		float64(direction[2]),
	)
	if length < 1e-6 {
		return RayHit{}, false, errors.New("core: invalid ray direction")
	}
	direction = direction.Mul(float32(1 / length))

	cell := [3]int32{
		oracleFloorToI32(origin[0]),
		oracleFloorToI32(origin[1]),
		oracleFloorToI32(origin[2]),
	}
	start := BlockPos{X: cell[0], Y: cell[1], Z: cell[2]}
	occupied, err := solid(start)
	if err != nil {
		return RayHit{}, false, err
	}
	if occupied {
		return RayHit{
			Block:    start,
			Face:     BlockFaceNone,
			Distance: 0,
			Point:    origin,
		}, true, nil
	}

	var step [3]int32
	var tDelta, tMax [3]float32
	for axis := range 3 {
		component := direction[axis]
		switch {
		case component > 0:
			step[axis] = 1
			tDelta[axis] = 1 / component
			boundary := float32(cell[axis] + 1)
			tMax[axis] = (boundary - origin[axis]) / component
		case component < 0:
			step[axis] = -1
			tDelta[axis] = -1 / component
			boundary := float32(cell[axis])
			tMax[axis] = (boundary - origin[axis]) / component
		default:
			tDelta[axis] = float32(math.Inf(1))
			tMax[axis] = float32(math.Inf(1))
		}
	}

	for {
		axis := 0
		if tMax[1] < tMax[axis] {
			axis = 1
		}
		if tMax[2] < tMax[axis] {
			axis = 2
		}
		distance := tMax[axis]
		if distance > maxDistance {
			return RayHit{}, false, nil
		}
		cell[axis] += step[axis]
		tMax[axis] += tDelta[axis]
		face := oracleEntryFace(axis, step[axis])
		pos := BlockPos{X: cell[0], Y: cell[1], Z: cell[2]}
		occupied, err := solid(pos)
		if err != nil {
			return RayHit{}, false, err
		}
		if occupied {
			return RayHit{
				Block:    pos,
				Face:     face,
				Distance: distance,
				Point:    origin.Add(direction.Mul(distance)),
			}, true, nil
		}
	}
}

func oracleFloorToI32(value float32) int32 {
	floored := math.Floor(float64(value))
	if floored < float64(math.MinInt32) || floored >= float64(math.MaxInt32)+1 {
		return math.MinInt32
	}
	return int32(floored)
}

func oracleFiniteVec3(v mgl32.Vec3) bool {
	return oracleFiniteFloat32(v[0]) && oracleFiniteFloat32(v[1]) && oracleFiniteFloat32(v[2])
}

func oracleFiniteFloat32(v float32) bool {
	return !math.IsNaN(float64(v)) && !math.IsInf(float64(v), 0)
}

func oracleEntryFace(axis int, step int32) BlockFace {
	if step > 0 {
		return [...]BlockFace{BlockFaceNegX, BlockFaceNegY, BlockFaceNegZ}[axis]
	}
	return [...]BlockFace{BlockFacePosX, BlockFacePosY, BlockFacePosZ}[axis]
}
