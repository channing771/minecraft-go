package physics

import (
	"encoding/binary"
	"math"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/nativeabi"
)

const (
	collisionHeaderBytes  = 64
	collisionCellBytes    = 196
	collisionOutputBytes  = 16
	collisionMaxCells     = 4096
	collisionMaxBytes     = collisionHeaderBytes + collisionMaxCells*collisionCellBytes
	collisionRegularCells = 135
	collisionRegularBytes = collisionHeaderBytes + collisionRegularCells*collisionCellBytes
)

type collisionPrism struct {
	origin     core.BlockPos
	dimensions [3]uint32
	cells      int
	bytes      int
}

type moveResult struct {
	position   mgl32.Vec3
	clipped    [3]bool
	onGround   bool
	usedStep   bool
	hitUnknown bool
}

func resolveCollision(
	state State,
	displacement mgl32.Vec3,
	source CollisionSource,
	beganGrounded bool,
	stepHeight float32,
) moveResult {
	prism := collisionPrismFor(state.Position, displacement, stepHeight)
	var regular [collisionRegularBytes]byte
	var input []byte
	if prism.cells <= collisionRegularCells {
		input = regular[:prism.bytes]
	} else {
		input = make([]byte, prism.bytes)
	}
	encodeCollisionInput(input, prism, state, displacement, source, beganGrounded, stepHeight)
	var output [collisionOutputBytes]byte
	nativeabi.CollisionResolve(input, output[:])
	return decodeCollisionOutput(output[:])
}

func collisionPrismFor(position, displacement mgl32.Vec3, stepHeight float32) collisionPrism {
	halfWidth := PlayerWidth / 2
	minimum := mgl32.Vec3{
		min(position.X(), position.X()+displacement.X()) - halfWidth - CollisionEpsilon,
		position.Y() + min(float32(0), displacement.Y(), stepHeight) - GroundProbe - CollisionEpsilon,
		min(position.Z(), position.Z()+displacement.Z()) - halfWidth - CollisionEpsilon,
	}
	maximum := mgl32.Vec3{
		max(position.X(), position.X()+displacement.X()) + halfWidth + CollisionEpsilon,
		position.Y() + max(float32(0), displacement.Y(), stepHeight) + PlayerHeight + CollisionEpsilon,
		max(position.Z(), position.Z()+displacement.Z()) + halfWidth + CollisionEpsilon,
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
	encodedBytes := uint64(collisionHeaderBytes) + cells*collisionCellBytes
	if encodedBytes > collisionMaxBytes {
		panic("physics: collision prism 编码长度溢出")
	}
	return collisionPrism{origin: origin, dimensions: dimensions, cells: int(cells), bytes: int(encodedBytes)}
}

func encodeCollisionInput(
	input []byte,
	prism collisionPrism,
	state State,
	displacement mgl32.Vec3,
	source CollisionSource,
	beganGrounded bool,
	stepHeight float32,
) {
	if len(input) != prism.bytes {
		panic("physics: collision input 缓冲区长度非法")
	}
	clear(input)
	copy(input[:4], "MGC1")
	binary.LittleEndian.PutUint32(input[4:8], 1)
	putCollisionVec3(input[8:20], state.Position)
	putCollisionVec3(input[20:32], displacement)
	if beganGrounded {
		input[32] = 1
	}
	putCollisionFloat(input[36:40], stepHeight)
	for index, value := range [...]int32{prism.origin.X, prism.origin.Y, prism.origin.Z} {
		binary.LittleEndian.PutUint32(input[40+index*4:44+index*4], uint32(value))
	}
	for index, value := range prism.dimensions {
		binary.LittleEndian.PutUint32(input[52+index*4:56+index*4], value)
	}

	offset := collisionHeaderBytes
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
					input[offset] = 1
				}
				count := min(int(set.Count), len(set.Boxes))
				input[offset+1] = byte(count)
				for boxIndex := range count {
					box := set.Boxes[boxIndex]
					components := [...]float32{
						box.Min.X(), box.Min.Y(), box.Min.Z(),
						box.Max.X(), box.Max.Y(), box.Max.Z(),
					}
					for componentIndex, value := range components {
						putCollisionFloat(input[offset+4+boxIndex*24+componentIndex*4:], value)
					}
				}
				offset += collisionCellBytes
			}
		}
	}
	if offset != len(input) {
		panic("physics: collision prism 编码不完整")
	}
}

func putCollisionVec3(output []byte, value mgl32.Vec3) {
	for index := range 3 {
		putCollisionFloat(output[index*4:index*4+4], value[index])
	}
}

func putCollisionFloat(output []byte, value float32) {
	binary.LittleEndian.PutUint32(output, math.Float32bits(value))
}

func decodeCollisionOutput(output []byte) moveResult {
	if len(output) != collisionOutputBytes || output[12]&^byte(7) != 0 ||
		output[13] > 1 || output[14] > 1 || output[15] > 1 {
		panic("physics: native collision output 非法")
	}
	result := moveResult{
		position: mgl32.Vec3{
			math.Float32frombits(binary.LittleEndian.Uint32(output[0:4])),
			math.Float32frombits(binary.LittleEndian.Uint32(output[4:8])),
			math.Float32frombits(binary.LittleEndian.Uint32(output[8:12])),
		},
		onGround:   output[13] == 1,
		usedStep:   output[14] == 1,
		hitUnknown: output[15] == 1,
	}
	for axis, mask := range [...]byte{1, 2, 4} {
		result.clipped[axis] = output[12]&mask != 0
	}
	return result
}
