package core

import (
	"errors"
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// RayHit 描述一条射线首次进入的实心方块。
type RayHit struct {
	Block    BlockPos
	Face     BlockFace
	Distance float32
	Point    mgl32.Vec3
}

// RaycastBlocks 用确定性的体素 DDA 返回射线命中的第一个实心方块。
func RaycastBlocks(
	origin, direction mgl32.Vec3,
	maxDistance float32,
	solid func(BlockPos) (bool, error),
) (RayHit, bool, error) {
	if !finiteVec3(origin) || !finiteVec3(direction) ||
		!finiteFloat32(maxDistance) || maxDistance <= 0 {
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
		int32(math.Floor(float64(origin[0]))),
		int32(math.Floor(float64(origin[1]))),
		int32(math.Floor(float64(origin[2]))),
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
		face := entryFace(axis, step[axis])
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

func finiteVec3(v mgl32.Vec3) bool {
	return finiteFloat32(v[0]) && finiteFloat32(v[1]) && finiteFloat32(v[2])
}

func finiteFloat32(v float32) bool {
	return !math.IsNaN(float64(v)) && !math.IsInf(float64(v), 0)
}

func entryFace(axis int, step int32) BlockFace {
	if step > 0 {
		return [...]BlockFace{BlockFaceNegX, BlockFaceNegY, BlockFaceNegZ}[axis]
	}
	return [...]BlockFace{BlockFacePosX, BlockFacePosY, BlockFacePosZ}[axis]
}
