//go:build cgo && (darwin || linux)

package nativeabi

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"unsafe"
)

const (
	nativeScratchWords = (48 * 48 * 48 * 5) / 8
	nativeOutputWords  = 6 * 4096
	nativeBufferCanary = uint64(0xd15e_a5ed_f00d_cafe)
)

func TestABIValuesMatchEngineContract(t *testing.T) {
	if got := EngineABIVersion(); got != ABIVersion {
		t.Fatalf("engine ABI version=%d，想要 %d", got, ABIVersion)
	}
	if got := []Status{StatusOK, StatusABIVersion, StatusInvalidArgument, StatusInput, StatusScratch, StatusRegistry, StatusEmission, StatusOutputOverflow, StatusQueueOverflow, StatusPanic}; !slices.Equal(got, []Status{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}) {
		t.Fatalf("engine status=%v，想要 0..9", got)
	}
}

func TestMeshSectionRejectsInvalidBuffersAtomically(t *testing.T) {
	scratch := make([]uint64, nativeScratchWords)
	outputArena := make([]uint64, nativeOutputWords+2)
	for i := range outputArena {
		outputArena[i] = nativeBufferCanary
	}
	output := outputArena[1 : 1+nativeOutputWords]
	before := slices.Clone(output)

	for _, tt := range []struct {
		name    string
		input   []byte
		scratch []uint64
		output  []uint64
		want    Status
	}{
		{"nil", nil, scratch, output, StatusInvalidArgument},
		{"undersized scratch", []byte{0}, scratch[:1], output, StatusScratch},
		{"undersized output", []byte{0}, scratch, output[:nativeOutputWords-1], StatusOutputOverflow},
		{"malformed input", []byte{0}, scratch, output, StatusInput},
		{"input output overlap", unsafe.Slice((*byte)(unsafe.Pointer(unsafe.SliceData(output))), 1), scratch, output, StatusInvalidArgument},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for i := range output {
				output[i] = nativeBufferCanary
			}
			status, count := MeshSection(ABIVersion, tt.input, tt.scratch, tt.output)
			if status != tt.want || count != 0 {
				t.Fatalf("status/count=%d/%d，想要 %d/0", status, count, tt.want)
			}
			if outputArena[0] != nativeBufferCanary || outputArena[len(outputArena)-1] != nativeBufferCanary {
				t.Fatal("native 写出调用方 output 边界")
			}
			if !slices.Equal(output, before) {
				t.Fatal("失败调用修改了 caller-owned output")
			}
		})
	}
}

func TestEngineCgoDirectivesArePresent(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("无法定位 nativeabi 测试文件")
	}
	contents, err := os.ReadFile(filepath.Join(filepath.Dir(file), "native.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, directive := range []string{
		"#cgo noescape mornlea_engine_abi_version",
		"#cgo nocallback mornlea_engine_abi_version",
		"#cgo noescape mornlea_mesh_section",
		"#cgo nocallback mornlea_mesh_section",
		"#cgo noescape mornlea_collision_resolve",
		"#cgo nocallback mornlea_collision_resolve",
		"#cgo noescape mornlea_raycast_batch",
		"#cgo nocallback mornlea_raycast_batch",
		"#cgo noescape mornlea_physics_step",
		"#cgo nocallback mornlea_physics_step",
		"#cgo noescape mornlea_worldgen_chunk",
		"#cgo nocallback mornlea_worldgen_chunk",
		"#cgo noescape mornlea_worldgen_probe",
		"#cgo nocallback mornlea_worldgen_probe",
	} {
		if !strings.Contains(string(contents), directive) {
			t.Errorf("缺少 %s", directive)
		}
	}
}

func TestCollisionRawFailureAtomicity(t *testing.T) {
	validInput := testValidCollisionInput()

	malformed := slices.Clone(validInput)
	malformed[33] = 1
	for _, test := range []struct {
		name    string
		version uint32
		input   []byte
		output  []byte
		want    Status
	}{
		{name: "ABI version", version: ABIVersion + 1, input: validInput, output: make([]byte, 16), want: StatusABIVersion},
		{name: "nil input", version: ABIVersion, output: make([]byte, 16), want: StatusInvalidArgument},
		{name: "short input", version: ABIVersion, input: validInput[:63], output: make([]byte, 16), want: StatusInput},
		{name: "long input", version: ABIVersion, input: append(slices.Clone(validInput), 0), output: make([]byte, 16), want: StatusInput},
		{name: "reserved", version: ABIVersion, input: malformed, output: make([]byte, 16), want: StatusInput},
		{name: "short output", version: ABIVersion, input: validInput, output: make([]byte, 15), want: StatusOutputOverflow},
		{name: "long output", version: ABIVersion, input: validInput, output: make([]byte, 17), want: StatusInvalidArgument},
	} {
		t.Run(test.name, func(t *testing.T) {
			outputArena := [19]byte{}
			for index := range outputArena {
				outputArena[index] = 0xa5
			}
			output := outputArena[1 : 1+len(test.output)]
			before := outputArena
			if status := collisionResolveVersion(test.version, test.input, output); status != test.want {
				t.Fatalf("collision status=%d，想要 %d", status, test.want)
			}
			if outputArena != before {
				t.Fatal("失败的 collision 调用修改了 caller-owned output")
			}
		})
	}

	shared := make([]byte, len(validInput))
	copy(shared, validInput)
	if status := collisionResolveVersion(ABIVersion, shared, shared[:16]); status != StatusInvalidArgument {
		t.Fatalf("overlap status=%d，想要 %d", status, StatusInvalidArgument)
	}
	if !slices.Equal(shared, validInput) {
		t.Fatal("overlap failure 修改了共享 buffer")
	}

	for _, test := range []struct {
		status Status
		want   string
	}{
		{StatusABIVersion, "nativeabi: collision ABI 版本不匹配"},
		{StatusInvalidArgument, "nativeabi: collision 参数非法"},
		{StatusInput, "nativeabi: collision 输入非法"},
		{StatusOutputOverflow, "nativeabi: collision output 过短"},
		{StatusPanic, "nativeabi: collision Rust panic"},
		{StatusScratch, "nativeabi: collision 未知状态"},
	} {
		if got := collisionStatusPanicText(test.status); got != test.want {
			t.Fatalf("status %d panic=%q，想要 %q", test.status, got, test.want)
		}
	}
}

func testValidPhysicsStepInput() []byte {
	input := make([]byte, 128+196)
	copy(input[:4], "MGP1")
	binary.LittleEndian.PutUint32(input[4:8], 1)
	for _, offset := range []int{8, 12, 16, 20, 24, 28, 36, 40, 44} {
		binary.LittleEndian.PutUint32(input[offset:offset+4], math.Float32bits(0))
	}
	for index, value := range [...]float32{0.6, 4.3, 40, 50, 8, 8.4, 32, 78.4} {
		binary.LittleEndian.PutUint32(input[48+index*4:52+index*4], math.Float32bits(value))
	}
	for _, offset := range []int{80, 84, 88, 92, 96, 100} {
		binary.LittleEndian.PutUint32(input[offset:offset+4], math.Float32bits(0))
	}
	for index := range 3 {
		binary.LittleEndian.PutUint32(input[116+index*4:120+index*4], 1)
	}
	input[128] = 1 // cell loaded
	return input
}

func TestPhysicsStepRawFailureAtomicity(t *testing.T) {
	validInput := testValidPhysicsStepInput()
	malformed := slices.Clone(validInput)
	malformed[33] = 2 // jump 非法
	for _, test := range []struct {
		name    string
		version uint32
		input   []byte
		output  []byte
		want    Status
	}{
		{name: "ABI version", version: ABIVersion + 1, input: validInput, output: make([]byte, 32), want: StatusABIVersion},
		{name: "nil input", version: ABIVersion, output: make([]byte, 32), want: StatusInvalidArgument},
		{name: "short input", version: ABIVersion, input: validInput[:127], output: make([]byte, 32), want: StatusInput},
		{name: "long input", version: ABIVersion, input: append(slices.Clone(validInput), 0), output: make([]byte, 32), want: StatusInput},
		{name: "jump flag", version: ABIVersion, input: malformed, output: make([]byte, 32), want: StatusInput},
		{name: "short output", version: ABIVersion, input: validInput, output: make([]byte, 31), want: StatusOutputOverflow},
		{name: "long output", version: ABIVersion, input: validInput, output: make([]byte, 33), want: StatusInvalidArgument},
	} {
		t.Run(test.name, func(t *testing.T) {
			output := slices.Clone(test.output)
			status := physicsStepVersion(test.version, test.input, output)
			if status != test.want {
				t.Fatalf("status=%d，想要 %d", status, test.want)
			}
			if !slices.Equal(output, test.output) {
				t.Fatal("失败调用修改了 caller-owned output")
			}
		})
	}
}

func TestCollisionRawValidatesOnlyUsedBoxComponents(t *testing.T) {
	unusedNonfinite := testValidCollisionInput()
	binary.LittleEndian.PutUint32(unusedNonfinite[68:72], math.Float32bits(float32(math.NaN())))
	if status := collisionResolveVersion(ABIVersion, unusedNonfinite, make([]byte, 16)); status != StatusOK {
		t.Fatalf("unused non-finite box status=%d，想要 %d", status, StatusOK)
	}

	usedNonfinite := slices.Clone(unusedNonfinite)
	usedNonfinite[65] = 1
	output := [16]byte{}
	for index := range output {
		output[index] = 0xa5
	}
	before := output
	if status := collisionResolveVersion(ABIVersion, usedNonfinite, output[:]); status != StatusInput {
		t.Fatalf("used non-finite box status=%d，想要 %d", status, StatusInput)
	}
	if output != before {
		t.Fatal("used non-finite box failure 修改了 output")
	}

	unconstrainedBounds := testValidCollisionInput()
	unconstrainedBounds[65] = 1
	for index, value := range [...]float32{2, 2, 2, -1, -1, -1} {
		binary.LittleEndian.PutUint32(unconstrainedBounds[68+index*4:72+index*4], math.Float32bits(value))
	}
	if status := collisionResolveVersion(ABIVersion, unconstrainedBounds, make([]byte, 16)); status != StatusOK {
		t.Fatalf("finite inverted bounds status=%d，想要 %d", status, StatusOK)
	}

	tooManyBoxes := testValidCollisionInput()
	tooManyBoxes[65] = 9
	if status := collisionResolveVersion(ABIVersion, tooManyBoxes, make([]byte, 16)); status != StatusInput {
		t.Fatalf("raw box_count=9 status=%d，想要 %d", status, StatusInput)
	}
}

func TestCollisionRawAcceptsCoveringSupersetPrism(t *testing.T) {
	input := make([]byte, 64+8*196)
	copy(input[:64], testValidCollisionInput()[:64])
	originX := int32(-1)
	binary.LittleEndian.PutUint32(input[40:44], uint32(originX))
	binary.LittleEndian.PutUint32(input[52:56], 2)
	for offset := 64; offset < len(input); offset += 196 {
		input[offset] = 1
	}
	if status := collisionResolveVersion(ABIVersion, input, make([]byte, 16)); status != StatusOK {
		t.Fatalf("covering superset prism status=%d，想要 %d", status, StatusOK)
	}
}

func TestRaycastBatchRawFailureAtomicity(t *testing.T) {
	validInput := testValidRaycastInput()
	validCursor := testFreshRaycastCursor()

	malformedInput := slices.Clone(validInput)
	malformedInput[36] = 1
	malformedCursor := slices.Clone(validCursor)
	malformedCursor[9] = 1
	for _, test := range []struct {
		name    string
		version uint32
		input   []byte
		cursor  []byte
		output  []byte
		want    Status
	}{
		{name: "ABI version", version: ABIVersion + 1, input: validInput, cursor: validCursor, output: make([]byte, 1280), want: StatusABIVersion},
		{name: "nil input", version: ABIVersion, cursor: validCursor, output: make([]byte, 1280), want: StatusInvalidArgument},
		{name: "short input", version: ABIVersion, input: validInput[:39], cursor: validCursor, output: make([]byte, 1280), want: StatusInput},
		{name: "long input", version: ABIVersion, input: append(slices.Clone(validInput), 0), cursor: validCursor, output: make([]byte, 1280), want: StatusInput},
		{name: "input reserved", version: ABIVersion, input: malformedInput, cursor: validCursor, output: make([]byte, 1280), want: StatusInput},
		{name: "nil cursor", version: ABIVersion, input: validInput, output: make([]byte, 1280), want: StatusInvalidArgument},
		{name: "short cursor", version: ABIVersion, input: validInput, cursor: validCursor[:63], output: make([]byte, 1280), want: StatusInput},
		{name: "long cursor", version: ABIVersion, input: validInput, cursor: append(slices.Clone(validCursor), 0), output: make([]byte, 1280), want: StatusInput},
		{name: "cursor reserved", version: ABIVersion, input: validInput, cursor: malformedCursor, output: make([]byte, 1280), want: StatusInput},
		{name: "nil output", version: ABIVersion, input: validInput, cursor: validCursor, want: StatusInvalidArgument},
		{name: "short output", version: ABIVersion, input: validInput, cursor: validCursor, output: make([]byte, 1279), want: StatusOutputOverflow},
		{name: "long output", version: ABIVersion, input: validInput, cursor: validCursor, output: make([]byte, 1281), want: StatusInvalidArgument},
	} {
		t.Run(test.name, func(t *testing.T) {
			cursorBefore := slices.Clone(test.cursor)
			outputBefore := slices.Clone(test.output)
			status, count, done := raycastBatchVersion(
				test.version, test.input, test.cursor, test.output,
			)
			if status != test.want || count != 0 || done != 0 {
				t.Fatalf("raycast status/count/done=%d/%d/%d，想要 %d/0/0", status, count, done, test.want)
			}
			if !slices.Equal(test.cursor, cursorBefore) {
				t.Fatal("失败的 raycast 调用修改了 caller-owned cursor")
			}
			if !slices.Equal(test.output, outputBefore) {
				t.Fatal("失败的 raycast 调用修改了 caller-owned output")
			}
		})
	}

	t.Run("pairwise overlap", func(t *testing.T) {
		for _, test := range []struct {
			name           string
			input, cursor  []byte
			output         []byte
			shared, before []byte
		}{
			func() struct {
				name           string
				input, cursor  []byte
				output         []byte
				shared, before []byte
			} {
				shared := make([]byte, 1280)
				copy(shared, validCursor)
				return struct {
					name           string
					input, cursor  []byte
					output         []byte
					shared, before []byte
				}{"input cursor", shared[:40], shared[:64], make([]byte, 1280), shared, slices.Clone(shared)}
			}(),
			func() struct {
				name           string
				input, cursor  []byte
				output         []byte
				shared, before []byte
			} {
				shared := make([]byte, 1280)
				copy(shared, validInput)
				return struct {
					name           string
					input, cursor  []byte
					output         []byte
					shared, before []byte
				}{"input output", shared[:40], validCursor, shared, shared, slices.Clone(shared)}
			}(),
			func() struct {
				name           string
				input, cursor  []byte
				output         []byte
				shared, before []byte
			} {
				shared := make([]byte, 1280)
				copy(shared, validCursor)
				return struct {
					name           string
					input, cursor  []byte
					output         []byte
					shared, before []byte
				}{"cursor output", validInput, shared[:64], shared, shared, slices.Clone(shared)}
			}(),
		} {
			t.Run(test.name, func(t *testing.T) {
				status, count, done := raycastBatchVersion(ABIVersion, test.input, test.cursor, test.output)
				if status != StatusInvalidArgument || count != 0 || done != 0 {
					t.Fatalf("overlap status/count/done=%d/%d/%d，想要 %d/0/0", status, count, done, StatusInvalidArgument)
				}
				if !slices.Equal(test.shared, test.before) {
					t.Fatal("overlap failure 修改了共享 buffer")
				}
			})
		}
	})
}

func TestRaycastBatchRejectsInvalidSuccessMetadata(t *testing.T) {
	for _, test := range []struct {
		name  string
		count uintptr
		done  uint8
	}{
		{name: "count", count: 65, done: 1},
		{name: "done", count: 1, done: 2},
		{name: "no progress", count: 0, done: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			defer func() {
				if got := recover(); got != "nativeabi: raycast success metadata 非法" {
					t.Fatalf("panic=%v，想要 stable metadata 文本", got)
				}
			}()
			raycastBatchResult(StatusOK, test.count, test.done)
		})
	}

	if count, done := raycastBatchResult(StatusOK, 64, 0); count != 64 || done {
		t.Fatalf("valid metadata=%d/%v，想要 64/false", count, done)
	}
	if count, done := raycastBatchResult(StatusOK, 0, 1); count != 0 || !done {
		t.Fatalf("done metadata=%d/%v，想要 0/true", count, done)
	}
}

func TestRaycastStatusPanicTextIsStable(t *testing.T) {
	for _, test := range []struct {
		status Status
		want   string
	}{
		{StatusABIVersion, "nativeabi: raycast ABI 版本不匹配"},
		{StatusInvalidArgument, "nativeabi: raycast 参数非法"},
		{StatusInput, "nativeabi: raycast 输入非法"},
		{StatusOutputOverflow, "nativeabi: raycast output 过短"},
		{StatusPanic, "nativeabi: raycast Rust panic"},
		{StatusScratch, "nativeabi: raycast 未知状态"},
	} {
		if got := raycastStatusPanicText(test.status); got != test.want {
			t.Fatalf("status %d panic=%q，想要 %q", test.status, got, test.want)
		}
	}
}

func testValidRaycastInput() []byte {
	input := make([]byte, 40)
	copy(input[:4], "MGR1")
	binary.LittleEndian.PutUint32(input[4:8], 1)
	for offset, value := range map[int]float32{
		8: 0.5, 12: -1.25, 16: 2.75, 20: 1, 32: 6,
	} {
		binary.LittleEndian.PutUint32(input[offset:offset+4], math.Float32bits(value))
	}
	return input
}

func testFreshRaycastCursor() []byte {
	cursor := make([]byte, 64)
	copy(cursor[:4], "MRC1")
	binary.LittleEndian.PutUint32(cursor[4:8], 1)
	return cursor
}

func testValidCollisionInput() []byte {
	input := make([]byte, 64+4*196)
	copy(input[:4], "MGC1")
	binary.LittleEndian.PutUint32(input[4:8], 1)
	for offset, value := range map[int]float32{
		8: 0.5, 12: 1, 16: 0.5, 36: 0.6,
	} {
		binary.LittleEndian.PutUint32(input[offset:offset+4], math.Float32bits(value))
	}
	input[32] = 1
	binary.LittleEndian.PutUint32(input[52:56], 1)
	binary.LittleEndian.PutUint32(input[56:60], 4)
	binary.LittleEndian.PutUint32(input[60:64], 1)
	for offset := 64; offset < len(input); offset += 196 {
		input[offset] = 1
	}
	return input
}

// testValidWorldgenHeader 构造合法 `MGW1` header:seed 42、互异材料表 1..=13、恒等 perm。
func testValidWorldgenHeader() []byte {
	header := make([]byte, 564)
	copy(header[:4], "MGW1")
	binary.LittleEndian.PutUint32(header[4:8], 1)
	binary.LittleEndian.PutUint64(header[8:16], 42)
	minY := int32(-64)
	binary.LittleEndian.PutUint32(header[16:20], uint32(minY))
	binary.LittleEndian.PutUint32(header[20:24], 320)
	for index := 0; index < 13; index++ {
		binary.LittleEndian.PutUint16(header[24+index*2:26+index*2], uint16(index+1))
	}
	for index := 0; index < 512; index++ {
		header[52+index] = byte(index & 255)
	}
	return header
}

func testValidWorldgenChunkInput() []byte {
	input := testValidWorldgenHeader()
	input = append(input, make([]byte, 8)...)
	return input
}

func testValidWorldgenProbeInput() []byte {
	input := testValidWorldgenHeader()
	input = binary.LittleEndian.AppendUint32(input, 1)
	input = binary.LittleEndian.AppendUint32(input, 2) // mode 2 = BaseBlockAt
	input = append(input, make([]byte, 12)...)
	return input
}

const worldgenChunkOutputBytes = 16 * 16 * 384 * 2

func TestWorldgenChunkRawFailureAtomicity(t *testing.T) {
	validInput := testValidWorldgenChunkInput()
	badMagic := slices.Clone(validInput)
	badMagic[0] = 'X'
	duplicateMaterial := slices.Clone(validInput)
	// dirt 改为与 stone 相同,触发材料表互异性校验。
	binary.LittleEndian.PutUint16(duplicateMaterial[26:28], 1)
	wrongMinY := slices.Clone(validInput)
	badMinY := int32(-32)
	binary.LittleEndian.PutUint32(wrongMinY[16:20], uint32(badMinY))
	for _, test := range []struct {
		name    string
		version uint32
		input   []byte
		output  []byte
		want    Status
	}{
		{name: "ABI version", version: ABIVersion + 1, input: validInput, output: make([]byte, worldgenChunkOutputBytes), want: StatusABIVersion},
		{name: "nil input", version: ABIVersion, output: make([]byte, worldgenChunkOutputBytes), want: StatusInvalidArgument},
		{name: "bad magic", version: ABIVersion, input: badMagic, output: make([]byte, worldgenChunkOutputBytes), want: StatusInput},
		{name: "duplicate material", version: ABIVersion, input: duplicateMaterial, output: make([]byte, worldgenChunkOutputBytes), want: StatusInput},
		{name: "wrong min y", version: ABIVersion, input: wrongMinY, output: make([]byte, worldgenChunkOutputBytes), want: StatusInput},
		{name: "short input", version: ABIVersion, input: validInput[:len(validInput)-1], output: make([]byte, worldgenChunkOutputBytes), want: StatusInput},
		{name: "short output", version: ABIVersion, input: validInput, output: make([]byte, worldgenChunkOutputBytes-1), want: StatusOutputOverflow},
		{name: "long output", version: ABIVersion, input: validInput, output: make([]byte, worldgenChunkOutputBytes+1), want: StatusInvalidArgument},
	} {
		t.Run(test.name, func(t *testing.T) {
			output := slices.Clone(test.output)
			status := worldgenChunkVersion(test.version, test.input, output)
			if status != test.want {
				t.Fatalf("status=%d，想要 %d", status, test.want)
			}
			if !slices.Equal(output, test.output) {
				t.Fatal("失败调用修改了 caller-owned output")
			}
		})
	}
}

func TestWorldgenChunkHappyPathIsDeterministic(t *testing.T) {
	input := testValidWorldgenChunkInput()
	first := make([]byte, worldgenChunkOutputBytes)
	second := make([]byte, worldgenChunkOutputBytes)
	WorldgenChunk(input, first)
	WorldgenChunk(input, second)
	if !slices.Equal(first, second) {
		t.Fatal("同输入两次生成结果不同")
	}
	// 最底层必须整层是 bedrock(材料表第 5 项 = 5)。
	for index := 0; index < 16*16; index++ {
		if got := binary.LittleEndian.Uint16(first[index*2 : index*2+2]); got != 5 {
			t.Fatalf("基岩层 index=%d 得到 %d", index, got)
		}
	}
}

func TestWorldgenProbeRawFailureAtomicity(t *testing.T) {
	validInput := testValidWorldgenProbeInput()
	badMode := slices.Clone(validInput)
	binary.LittleEndian.PutUint32(badMode[564+4:564+8], 3)
	zeroCount := slices.Clone(validInput[:564+4])
	binary.LittleEndian.PutUint32(zeroCount[564:568], 0)
	for _, test := range []struct {
		name    string
		version uint32
		input   []byte
		output  []byte
		want    Status
	}{
		{name: "ABI version", version: ABIVersion + 1, input: validInput, output: make([]byte, 8), want: StatusABIVersion},
		{name: "nil input", version: ABIVersion, output: make([]byte, 8), want: StatusInvalidArgument},
		{name: "bad mode", version: ABIVersion, input: badMode, output: make([]byte, 8), want: StatusInput},
		{name: "zero count", version: ABIVersion, input: zeroCount, output: make([]byte, 8), want: StatusInput},
		{name: "short input", version: ABIVersion, input: validInput[:len(validInput)-1], output: make([]byte, 8), want: StatusInput},
		{name: "short output", version: ABIVersion, input: validInput, output: make([]byte, 7), want: StatusOutputOverflow},
		{name: "long output", version: ABIVersion, input: validInput, output: make([]byte, 9), want: StatusInvalidArgument},
	} {
		t.Run(test.name, func(t *testing.T) {
			output := slices.Clone(test.output)
			status := worldgenProbeVersion(test.version, test.input, output)
			if status != test.want {
				t.Fatalf("status=%d，想要 %d", status, test.want)
			}
			if !slices.Equal(output, test.output) {
				t.Fatal("失败调用修改了 caller-owned output")
			}
		})
	}
}

func TestWorldgenProbeMatchesChunkColumn(t *testing.T) {
	chunkInput := testValidWorldgenChunkInput()
	dense := make([]byte, worldgenChunkOutputBytes)
	WorldgenChunk(chunkInput, dense)

	input := testValidWorldgenHeader()
	input = binary.LittleEndian.AppendUint32(input, 2)
	// mode 2 查询 (3, 0, 5),mode 0 查询同柱高度。
	input = binary.LittleEndian.AppendUint32(input, 2)
	input = binary.LittleEndian.AppendUint32(input, 3)
	input = binary.LittleEndian.AppendUint32(input, 0)
	input = binary.LittleEndian.AppendUint32(input, 5)
	input = binary.LittleEndian.AppendUint32(input, 0)
	input = binary.LittleEndian.AppendUint32(input, 3)
	input = binary.LittleEndian.AppendUint32(input, 0)
	input = binary.LittleEndian.AppendUint32(input, 5)
	output := make([]byte, 16)
	WorldgenProbe(input, output)

	denseOffset := ((0+64)*16*16 + 5*16 + 3) * 2
	want := binary.LittleEndian.Uint16(dense[denseOffset : denseOffset+2])
	if got := binary.LittleEndian.Uint16(output[4:6]); got != want {
		t.Fatalf("probe block=%d，chunk dense=%d", got, want)
	}
	height := int32(binary.LittleEndian.Uint32(output[8:12]))
	if height < 0 || height > 200 {
		t.Fatalf("height=%d 超出地形振幅范围", height)
	}
}

func TestWorldgenStatusPanicTextIsStable(t *testing.T) {
	for status, want := range map[Status]string{
		StatusABIVersion:      "nativeabi: worldgen chunk ABI 版本不匹配",
		StatusInvalidArgument: "nativeabi: worldgen chunk 参数非法",
		StatusInput:           "nativeabi: worldgen chunk 输入非法",
		StatusOutputOverflow:  "nativeabi: worldgen chunk output 过短",
		StatusPanic:           "nativeabi: worldgen chunk Rust panic",
		Status(200):           "nativeabi: worldgen chunk 未知状态",
	} {
		if got := worldgenStatusPanicText("chunk", status); got != want {
			t.Fatalf("status=%d 文案=%q，想要 %q", status, got, want)
		}
	}
	if got := worldgenStatusPanicText("probe", StatusInput); got != "nativeabi: worldgen probe 输入非法" {
		t.Fatalf("probe 文案=%q", got)
	}
}
