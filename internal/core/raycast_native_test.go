package core

import (
	"encoding/binary"
	"errors"
	"math"
	"math/rand"
	"strconv"
	"sync"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/nativeabi"
)

func TestRaycastInputCursorAndRecordLayoutV1(t *testing.T) {
	var input [40]byte
	encodeRaycastInput(
		input[:],
		mgl32.Vec3{1.25, -2.5, 3.75},
		mgl32.Vec3{-0.25, 0.5, -0.75},
		6.5,
	)
	if string(input[0:4]) != "MGR1" || binary.LittleEndian.Uint32(input[4:8]) != 1 {
		t.Fatalf("input identity=%q/%d", input[0:4], binary.LittleEndian.Uint32(input[4:8]))
	}
	for index, want := range [...]float32{1.25, -2.5, 3.75, -0.25, 0.5, -0.75, 6.5} {
		offset := 8 + index*4
		if got := binary.LittleEndian.Uint32(input[offset : offset+4]); got != math.Float32bits(want) {
			t.Fatalf("input float[%d] bits=%08x，想要 %08x", index, got, math.Float32bits(want))
		}
	}
	if input[36] != 0 || input[37] != 0 || input[38] != 0 || input[39] != 0 {
		t.Fatalf("input reserved=%v，想要全零", input[36:40])
	}

	var cursor [64]byte
	initializeRaycastCursor(cursor[:])
	if string(cursor[0:4]) != "MRC1" || binary.LittleEndian.Uint32(cursor[4:8]) != 1 {
		t.Fatalf("cursor identity=%q/%d", cursor[0:4], binary.LittleEndian.Uint32(cursor[4:8]))
	}
	for offset, value := range cursor[8:] {
		if value != 0 {
			t.Fatalf("fresh cursor[%d]=%d，想要 0", offset+8, value)
		}
	}

	recordBytes := [20]byte{
		0xf9, 0xff, 0xff, 0xff,
		0x08, 0x00, 0x00, 0x00,
		0xf7, 0xff, 0xff, 0xff,
		byte(BlockFacePosY), 0, 0, 0,
		0x00, 0x00, 0xa0, 0x3f,
	}
	record := decodeRaycastRecord(recordBytes[:], input[:])
	if record.block != (BlockPos{X: -7, Y: 8, Z: -9}) ||
		record.face != BlockFacePosY || math.Float32bits(record.distance) != math.Float32bits(1.25) {
		t.Fatalf("record=%+v", record)
	}
}

func TestNativeRaycastMatchesGoOracle(t *testing.T) {
	origin := mgl32.Vec3{0.5, 5.5, 2.5}
	direction := mgl32.Vec3{-1, 0, 0}
	assertNativeRaycastMatchesOracle(t, origin, direction, 6)
}

func TestNativeRaycastMatchesGoOracleDeterministicCorpus(t *testing.T) {
	negativeZero := math.Float32frombits(0x8000_0000)
	for _, test := range []struct {
		name              string
		origin, direction mgl32.Vec3
		maximum           float32
	}{
		{name: "origin signed zero", origin: mgl32.Vec3{negativeZero, 0.5, negativeZero}, direction: mgl32.Vec3{1, 0, 0}, maximum: 2},
		{name: "boundary zero-distance face", origin: mgl32.Vec3{1, negativeZero, 0.5}, direction: mgl32.Vec3{-1, negativeZero, 0}, maximum: 2},
		{name: "positive X", origin: mgl32.Vec3{0.5, 0.5, 0.5}, direction: mgl32.Vec3{1, 0, 0}, maximum: 2},
		{name: "negative X", origin: mgl32.Vec3{0.5, 0.5, 0.5}, direction: mgl32.Vec3{-1, 0, 0}, maximum: 2},
		{name: "positive Y", origin: mgl32.Vec3{0.5, 0.5, 0.5}, direction: mgl32.Vec3{0, 1, 0}, maximum: 2},
		{name: "negative Y", origin: mgl32.Vec3{0.5, 0.5, 0.5}, direction: mgl32.Vec3{0, -1, 0}, maximum: 2},
		{name: "positive Z", origin: mgl32.Vec3{0.5, 0.5, 0.5}, direction: mgl32.Vec3{0, 0, 1}, maximum: 2},
		{name: "negative Z", origin: mgl32.Vec3{0.5, 0.5, 0.5}, direction: mgl32.Vec3{0, 0, -1}, maximum: 2},
		{name: "negative floor", origin: mgl32.Vec3{-0.25, -1.75, -2.5}, direction: mgl32.Vec3{-1, 0.5, -0.25}, maximum: 8},
		{name: "XYZ tie", origin: mgl32.Vec3{0.5, 0.5, 0.5}, direction: mgl32.Vec3{1, 1, 1}, maximum: 4},
		{name: "exact endpoint", origin: mgl32.Vec3{0, 0.5, 0.5}, direction: mgl32.Vec3{1, 0, 0}, maximum: 6},
		{name: "next-float endpoint", origin: mgl32.Vec3{6 - math.Nextafter32(6, float32(math.Inf(1))), 0.5, 0.5}, direction: mgl32.Vec3{1, 0, 0}, maximum: 6},
		{name: "multiple batches", origin: mgl32.Vec3{0.5, 0.5, 0.5}, direction: mgl32.Vec3{1, 0, 0}, maximum: 130},
		{name: "int32 wrapping", origin: mgl32.Vec3{float32(math.MinInt32), 0.5, 0.5}, direction: mgl32.Vec3{-1, 0, 0}, maximum: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertNativeRaycastMatchesOracle(t, test.origin, test.direction, test.maximum)
		})
	}
}

func TestNativeRaycastMatchesGoOracleFixedRandomCorpus(t *testing.T) {
	random := rand.New(rand.NewSource(0x5241_5943_4153_5431))
	for index := range 64 {
		origin := mgl32.Vec3{
			random.Float32()*2048 - 1024,
			random.Float32()*2048 - 1024,
			random.Float32()*2048 - 1024,
		}
		direction := mgl32.Vec3{
			random.Float32()*2 - 1,
			random.Float32()*2 - 1,
			random.Float32()*2 - 1,
		}
		if direction.Len() < 1e-3 {
			direction[0] = 1
		}
		maximum := 0.01 + random.Float32()*127.99
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			assertNativeRaycastMatchesOracle(t, origin, direction, maximum)
		})
	}
}

func TestNativeRaycastConcurrentCalls(t *testing.T) {
	const workers = 32
	type testCase struct {
		origin, direction mgl32.Vec3
		want              []raycastRecord
	}
	corpus := make([]testCase, workers)
	for worker := range workers {
		origin := mgl32.Vec3{float32(worker%7) - 3.25, float32(worker%5) + 0.5, -2.75}
		direction := mgl32.Vec3{1, float32(worker%3) - 1, -0.25}
		corpus[worker] = testCase{
			origin:    origin,
			direction: direction,
			want:      oracleRaycastRecords(t, origin, direction, 96),
		}
	}
	start := make(chan struct{})
	var group sync.WaitGroup
	group.Add(workers)
	errors := make(chan string, workers)
	for worker := range workers {
		go func() {
			defer group.Done()
			<-start
			test := corpus[worker]
			actual := nativeRaycastRecords(test.origin, test.direction, 96)
			if mismatch := raycastRecordMismatch(actual, test.want); mismatch != "" {
				errors <- mismatch
			}
		}()
	}
	close(start)
	group.Wait()
	close(errors)
	for mismatch := range errors {
		t.Error(mismatch)
	}
}

func TestRaycastBlocksDoesNotAllocate(t *testing.T) {
	solid := func(BlockPos) (bool, error) { return false, nil }
	origin := mgl32.Vec3{0.5, 0.5, 0.5}
	direction := mgl32.Vec3{1, 0.73, 0.41}
	allocations := testing.AllocsPerRun(1000, func() {
		if _, _, err := RaycastBlocks(origin, direction, 32, solid); err != nil {
			panic(err)
		}
	})
	if allocations != 0 {
		t.Fatalf("RaycastBlocks allocations=%v，想要 0", allocations)
	}
}

func TestRaycastBlocksExtremeFiniteInputPreservesSecondCallbackError(t *testing.T) {
	origin := mgl32.Vec3{float32(math.MaxInt32), 0.5, 0.5}
	direction := mgl32.Vec3{1e-30, 1, 0}
	want := [...]BlockPos{{X: math.MinInt32}, {X: math.MinInt32 + 1}}
	sentinel := errors.New("extreme ray sentinel")
	for _, test := range []struct {
		name    string
		raycast func(mgl32.Vec3, mgl32.Vec3, float32, func(BlockPos) (bool, error)) (RayHit, bool, error)
	}{
		{name: "oracle", raycast: oracleRaycastBlocks},
		{name: "native", raycast: RaycastBlocks},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			var visited [2]BlockPos
			_, found, err := test.raycast(origin, direction, 1, func(position BlockPos) (bool, error) {
				if calls < len(visited) {
					visited[calls] = position
				}
				calls++
				if calls == len(want) {
					return false, sentinel
				}
				return false, nil
			})
			if err != sentinel || found || calls != len(want) {
				t.Fatalf("found/err/calls=%v/%v/%d，想要 false/sentinel/%d", found, err, calls, len(want))
			}
			if visited != want {
				t.Fatalf("callback 顺序=%+v，想要 %+v", visited, want)
			}
		})
	}
}

func TestRaycastBlocksExtremeFiniteInputPreservesSecondCellHit(t *testing.T) {
	origin := mgl32.Vec3{float32(math.MaxInt32), 0.5, 0.5}
	direction := mgl32.Vec3{1e-30, 1, 0}
	target := BlockPos{X: math.MinInt32 + 1}
	var oracleHit RayHit
	for _, test := range []struct {
		name    string
		raycast func(mgl32.Vec3, mgl32.Vec3, float32, func(BlockPos) (bool, error)) (RayHit, bool, error)
	}{
		{name: "oracle", raycast: oracleRaycastBlocks},
		{name: "native", raycast: RaycastBlocks},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			hit, found, err := test.raycast(origin, direction, 1, func(position BlockPos) (bool, error) {
				calls++
				return calls == 2, nil
			})
			if err != nil || !found || calls != 2 || hit.Block != target || hit.Face != BlockFaceNegX ||
				!math.IsInf(float64(hit.Distance), -1) {
				t.Fatalf("hit/found/err/calls=%+v/%v/%v/%d", hit, found, err, calls)
			}
			if test.name == "oracle" {
				oracleHit = hit
				return
			}
			for axis := range 3 {
				if math.Float32bits(hit.Point[axis]) != math.Float32bits(oracleHit.Point[axis]) {
					t.Fatalf("Point[%d] bits=%08x，oracle=%08x", axis, math.Float32bits(hit.Point[axis]), math.Float32bits(oracleHit.Point[axis]))
				}
			}
		})
	}
}

func TestRaycastBlocksExtremeFiniteInputContinuesAcrossBatch(t *testing.T) {
	origin := mgl32.Vec3{float32(math.MaxInt32), 0.5, 0.5}
	target := BlockPos{X: math.MinInt32 + 64}
	for _, directionX := range []float32{1e-30, 1e-40} {
		direction := mgl32.Vec3{directionX, 1, 0}
		for _, test := range []struct {
			name    string
			raycast func(mgl32.Vec3, mgl32.Vec3, float32, func(BlockPos) (bool, error)) (RayHit, bool, error)
		}{
			{name: "oracle", raycast: oracleRaycastBlocks},
			{name: "native", raycast: RaycastBlocks},
		} {
			t.Run(test.name+"/"+strconv.FormatUint(uint64(math.Float32bits(directionX)), 16), func(t *testing.T) {
				sentinel := errors.New("cross-batch sentinel")
				calls := 0
				var last BlockPos
				_, found, err := test.raycast(origin, direction, 1, func(position BlockPos) (bool, error) {
					calls++
					last = position
					if calls == 65 {
						return false, sentinel
					}
					return false, nil
				})
				if err != sentinel || found || calls != 65 {
					t.Fatalf("found/err/calls=%v/%v/%d，想要 false/sentinel/65", found, err, calls)
				}
				if last != target {
					t.Fatalf("callback[64]=%+v，想要 %+v", last, target)
				}
			})
		}
	}
}

func TestDecodeRaycastRecordRejectsInvalidConsumedRecord(t *testing.T) {
	var rayInput [raycastInputBytes]byte
	encodeRaycastInput(rayInput[:], mgl32.Vec3{0.5, 0.5, 0.5}, mgl32.Vec3{1, 0, 0}, 6)
	valid := make([]byte, raycastRecordBytes)
	valid[12] = byte(BlockFaceNegX)
	for _, mutate := range []func([]byte){
		func(record []byte) { record[12] = 6 },
		func(record []byte) { record[13] = 1 },
		func(record []byte) {
			binary.LittleEndian.PutUint32(record[16:20], math.Float32bits(float32(math.NaN())))
		},
	} {
		record := append([]byte(nil), valid...)
		mutate(record)
		func() {
			defer func() {
				if recover() == nil {
					t.Error("非法 consumed record 未 panic")
				}
			}()
			decodeRaycastRecord(record, rayInput[:])
		}()
	}
}

func TestDecodeRaycastRecordAllowsDerivedOverflowAfterCellWrap(t *testing.T) {
	for _, test := range []struct {
		directionX, distance float32
	}{
		{directionX: 1e-30, distance: float32(math.Inf(-1))},
		{directionX: 1e-40, distance: float32(math.NaN())},
	} {
		var rayInput [raycastInputBytes]byte
		encodeRaycastInput(
			rayInput[:],
			mgl32.Vec3{float32(math.MaxInt32), 0.5, 0.5},
			mgl32.Vec3{test.directionX, 1, 0},
			1,
		)
		var record [raycastRecordBytes]byte
		binary.LittleEndian.PutUint32(record[0:4], uint32(math.MaxInt32))
		record[12] = byte(BlockFaceNegX)
		binary.LittleEndian.PutUint32(record[16:20], math.Float32bits(test.distance))
		decodeRaycastRecord(record[:], rayInput[:])
	}
}

func assertNativeRaycastMatchesOracle(
	t *testing.T,
	origin, direction mgl32.Vec3,
	maximum float32,
) {
	t.Helper()
	actual := nativeRaycastRecords(origin, direction, maximum)
	want := oracleRaycastRecords(t, origin, direction, maximum)
	if mismatch := raycastRecordMismatch(actual, want); mismatch != "" {
		t.Fatal(mismatch)
	}
}

func nativeRaycastRecords(origin, direction mgl32.Vec3, maximum float32) []raycastRecord {
	length := math.Hypot(math.Hypot(float64(direction[0]), float64(direction[1])), float64(direction[2]))
	direction = direction.Mul(float32(1 / length))
	var input [raycastInputBytes]byte
	var cursor [raycastCursorBytes]byte
	var output [raycastOutputBytes]byte
	encodeRaycastInput(input[:], origin, direction, maximum)
	initializeRaycastCursor(cursor[:])
	records := make([]raycastRecord, 0, 64)
	for {
		count, done := nativeabi.RaycastBatch(input[:], cursor[:], output[:])
		for index := range count {
			records = append(records, decodeRaycastRecord(output[index*raycastRecordBytes:(index+1)*raycastRecordBytes], input[:]))
		}
		if done {
			return records
		}
	}
}

func oracleRaycastRecords(
	t *testing.T,
	origin, direction mgl32.Vec3,
	maximum float32,
) []raycastRecord {
	t.Helper()
	var visited []BlockPos
	if _, _, err := oracleRaycastBlocks(origin, direction, maximum, func(position BlockPos) (bool, error) {
		visited = append(visited, position)
		return false, nil
	}); err != nil {
		t.Fatal(err)
	}
	records := make([]raycastRecord, 0, len(visited))
	for _, target := range visited {
		hit, found, err := oracleRaycastBlocks(origin, direction, maximum, func(position BlockPos) (bool, error) {
			return position == target, nil
		})
		if err != nil || !found {
			t.Fatalf("oracle target=%+v found=%v err=%v", target, found, err)
		}
		records = append(records, raycastRecord{block: hit.Block, face: hit.Face, distance: hit.Distance})
	}
	return records
}

func raycastRecordMismatch(actual, want []raycastRecord) string {
	if len(actual) != len(want) {
		return "native raycast record count=" + strconv.Itoa(len(actual)) + "，想要 " + strconv.Itoa(len(want))
	}
	for index := range want {
		if actual[index].block != want[index].block || actual[index].face != want[index].face ||
			math.Float32bits(actual[index].distance) != math.Float32bits(want[index].distance) {
			return "native raycast record mismatch at " + strconv.Itoa(index)
		}
	}
	return ""
}
