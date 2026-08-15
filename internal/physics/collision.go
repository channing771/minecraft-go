package physics

import (
	"encoding/binary"
	"math"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
)

const (
	collisionCellBytes = 196
	collisionMaxCells  = 4096
)

type collisionPrism struct {
	origin     core.BlockPos
	dimensions [3]uint32
	cells      int
	bytes      int
}

// stepPrismFor 按位移凸包界构建 prism；sweepMin/sweepMax 来自 stepSweepBounds。
func stepPrismFor(position, sweepMin, sweepMax mgl32.Vec3, stepHeight float32) collisionPrism {
	halfWidth := PlayerWidth / 2
	minimum := mgl32.Vec3{
		position.X() + sweepMin.X() - halfWidth - CollisionEpsilon,
		position.Y() + min(float32(0), sweepMin.Y(), stepHeight) - GroundProbe - CollisionEpsilon,
		position.Z() + sweepMin.Z() - halfWidth - CollisionEpsilon,
	}
	maximum := mgl32.Vec3{
		position.X() + sweepMax.X() + halfWidth + CollisionEpsilon,
		position.Y() + max(float32(0), sweepMax.Y(), stepHeight) + PlayerHeight + CollisionEpsilon,
		position.Z() + sweepMax.Z() + halfWidth + CollisionEpsilon,
	}
	origin := core.BlockPos{
		X: collisionCheckedFloor(minimum.X()),
		Y: collisionCheckedFloor(minimum.Y()),
		Z: collisionCheckedFloor(minimum.Z()),
	}
	end := core.BlockPos{
		X: collisionCheckedFloor(maximum.X()),
		Y: collisionCheckedFloor(maximum.Y()),
		Z: collisionCheckedFloor(maximum.Z()),
	}
	return collisionCheckedPrism(origin, [3]uint32{
		collisionCheckedDimension(origin.X, end.X),
		collisionCheckedDimension(origin.Y, end.Y),
		collisionCheckedDimension(origin.Z, end.Z),
	})
}

func collisionCheckedFloor(value float32) int32 {
	floored := math.Floor(float64(value))
	if math.IsNaN(floored) || math.IsInf(floored, 0) || floored < -1<<31 || floored > 1<<31-1 {
		panic("physics: collision prism 坐标不可表示")
	}
	return int32(floored)
}

func collisionCheckedDimension(minimum, maximum int32) uint32 {
	dimension := int64(maximum) - int64(minimum) + 1
	if dimension <= 0 || dimension > 1<<32-1 {
		panic("physics: collision prism 尺寸不可表示")
	}
	return uint32(dimension)
}

func collisionCheckedPrism(origin core.BlockPos, dimensions [3]uint32) collisionPrism {
	coordinates := [...]int32{origin.X, origin.Y, origin.Z}
	cells := uint64(1)
	for axis, dimension := range dimensions {
		if dimension == 0 || int64(coordinates[axis])+int64(dimension)-1 > 1<<31-1 {
			panic("physics: collision prism 尺寸非法")
		}
		cells *= uint64(dimension)
		if cells > collisionMaxCells {
			panic("physics: collision prism 超过 4096 cells")
		}
	}
	encodedBytes := uint64(stepHeaderBytes) + cells*collisionCellBytes
	if encodedBytes > stepHeaderBytes+collisionMaxCells*collisionCellBytes {
		panic("physics: collision prism 编码长度溢出")
	}
	return collisionPrism{origin: origin, dimensions: dimensions, cells: int(cells), bytes: int(encodedBytes)}
}

func putCollisionVec3(output []byte, value mgl32.Vec3) {
	for index := range 3 {
		putCollisionFloat(output[index*4:index*4+4], value[index])
	}
}

func putCollisionFloat(output []byte, value float32) {
	binary.LittleEndian.PutUint32(output, math.Float32bits(value))
}
