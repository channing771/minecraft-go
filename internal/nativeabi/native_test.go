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
				if recover() == nil {
					t.Fatal("非法 raycast success metadata 未 panic")
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
