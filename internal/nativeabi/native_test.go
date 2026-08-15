//go:build cgo && (darwin || linux)

package nativeabi

import (
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
	} {
		if !strings.Contains(string(contents), directive) {
			t.Errorf("缺少 %s", directive)
		}
	}
}
