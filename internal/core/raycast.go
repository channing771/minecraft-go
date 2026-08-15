package core

import (
	"encoding/binary"
	"errors"
	"math"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/nativeabi"
)

const (
	raycastInputBytes  = 40
	raycastCursorBytes = 64
	raycastRecordBytes = 20
	raycastOutputBytes = 64 * raycastRecordBytes
)

type raycastRecord struct {
	block    BlockPos
	face     BlockFace
	distance float32
}

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

	var input [raycastInputBytes]byte
	var cursor [raycastCursorBytes]byte
	var output [raycastOutputBytes]byte
	encodeRaycastInput(input[:], origin, direction, maxDistance)
	initializeRaycastCursor(cursor[:])
	firstRecord := true
	for {
		count, done := nativeabi.RaycastBatch(input[:], cursor[:], output[:])
		for index := range count {
			record := decodeRaycastRecord(output[index*raycastRecordBytes : (index+1)*raycastRecordBytes])
			if firstRecord && (record.face != BlockFaceNone || record.distance != 0) ||
				!firstRecord && record.face == BlockFaceNone {
				panic("core: native raycast origin record 非法")
			}
			occupied, err := solid(record.block)
			if err != nil {
				return RayHit{}, false, err
			}
			if occupied {
				point := origin.Add(direction.Mul(record.distance))
				if record.face == BlockFaceNone {
					point = origin
				}
				return RayHit{
					Block:    record.block,
					Face:     record.face,
					Distance: record.distance,
					Point:    point,
				}, true, nil
			}
			firstRecord = false
		}
		if done {
			return RayHit{}, false, nil
		}
	}
}

func finiteVec3(v mgl32.Vec3) bool {
	return finiteFloat32(v[0]) && finiteFloat32(v[1]) && finiteFloat32(v[2])
}

func finiteFloat32(v float32) bool {
	return !math.IsNaN(float64(v)) && !math.IsInf(float64(v), 0)
}

func encodeRaycastInput(output []byte, origin, direction mgl32.Vec3, maxDistance float32) {
	if len(output) != raycastInputBytes {
		panic("core: native raycast input 长度非法")
	}
	copy(output[0:4], "MGR1")
	binary.LittleEndian.PutUint32(output[4:8], 1)
	for index, value := range origin {
		binary.LittleEndian.PutUint32(output[8+index*4:12+index*4], math.Float32bits(value))
	}
	for index, value := range direction {
		binary.LittleEndian.PutUint32(output[20+index*4:24+index*4], math.Float32bits(value))
	}
	binary.LittleEndian.PutUint32(output[32:36], math.Float32bits(maxDistance))
	clear(output[36:40])
}

func initializeRaycastCursor(cursor []byte) {
	if len(cursor) != raycastCursorBytes {
		panic("core: native raycast cursor 长度非法")
	}
	clear(cursor)
	copy(cursor[0:4], "MRC1")
	binary.LittleEndian.PutUint32(cursor[4:8], 1)
}

func decodeRaycastRecord(input []byte) raycastRecord {
	if len(input) != raycastRecordBytes || input[13] != 0 || input[14] != 0 || input[15] != 0 {
		panic("core: native raycast record 非法")
	}
	face := BlockFace(input[12])
	if face > BlockFacePosZ && face != BlockFaceNone {
		panic("core: native raycast record 非法")
	}
	distance := math.Float32frombits(binary.LittleEndian.Uint32(input[16:20]))
	if !finiteFloat32(distance) {
		panic("core: native raycast record 非法")
	}
	return raycastRecord{
		block: BlockPos{
			X: int32(binary.LittleEndian.Uint32(input[0:4])),
			Y: int32(binary.LittleEndian.Uint32(input[4:8])),
			Z: int32(binary.LittleEndian.Uint32(input[8:12])),
		},
		face:     face,
		distance: distance,
	}
}
