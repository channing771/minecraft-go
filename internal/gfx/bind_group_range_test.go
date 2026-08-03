//go:build darwin

package gfx

import (
	"encoding/binary"
	"fmt"
	"math"
	"testing"
)

// Mutation killed: treating the zero-value entry as an empty binding, or
// ignoring an explicit range, changes these independently derived bounds.
func TestResolveBindGroupBufferRangeDefaultsAndTranslatesExplicitRange(t *testing.T) {
	tests := []struct {
		name       string
		entry      BindGroupEntry
		bufferSize uint64
		wantOffset uint64
		wantSize   uint64
	}{
		{name: "zero value means whole buffer", bufferSize: 512, wantSize: 512},
		{name: "explicit prefix", entry: BindGroupEntry{Size: 16}, bufferSize: 512, wantSize: 16},
		{name: "explicit aligned slice", entry: BindGroupEntry{Offset: 256, Size: 16}, bufferSize: 512, wantOffset: 256, wantSize: 16},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			offset, size := resolveBindGroupBufferRange("range-test", 3, test.entry, test.bufferSize)
			if offset != test.wantOffset || size != test.wantSize {
				t.Fatalf("range=(%d,%d) want=(%d,%d)", offset, size, test.wantOffset, test.wantSize)
			}
		})
	}
}

// Mutation killed: accepting offset-without-size, checking offset+size with a
// wrapping addition, or omitting the upper bound makes at least one case pass.
func TestResolveBindGroupBufferRangeRejectsInvalidExplicitRange(t *testing.T) {
	tests := []struct {
		name  string
		entry BindGroupEntry
		want  string
	}{
		{
			name:  "offset without size",
			entry: BindGroupEntry{Offset: 256},
			want:  `gfx: bind group "range-test" binding 3 的 buffer range offset=256 size=0 要求显式 size > 0`,
		},
		{
			name:  "past end",
			entry: BindGroupEntry{Offset: 511, Size: 2},
			want:  `gfx: bind group "range-test" binding 3 的 buffer range offset=511 size=2 超出 buffer size=512`,
		},
		{
			name:  "addition overflow",
			entry: BindGroupEntry{Offset: math.MaxUint64 - 3, Size: 8},
			want:  `gfx: bind group "range-test" binding 3 的 buffer range offset=18446744073709551612 size=8 超出 buffer size=512`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			defer func() {
				got := recover()
				if got == nil {
					t.Fatal("resolveBindGroupBufferRange did not panic")
				}
				if message := panicMessage(got); message != test.want {
					t.Fatalf("panic=%q want=%q", message, test.want)
				}
			}()
			resolveBindGroupBufferRange("range-test", 3, test.entry, 512)
		})
	}
}

// Mutation killed: CreateBindGroup ignoring Offset/Size makes both dispatches
// read 11 from offset zero instead of selecting the second aligned value 22.
func TestBindGroupExplicitBufferRangeSelectsBoundBytes(t *testing.T) {
	dev, err := NewHeadlessDevice()
	if err != nil {
		t.Skipf("本机无可用 GPU 适配器: %v", err)
	}
	defer dev.Release()

	inputBytes := make([]byte, 512)
	binary.LittleEndian.PutUint32(inputBytes[0:], 11)
	binary.LittleEndian.PutUint32(inputBytes[256:], 22)
	input := dev.CreateBuffer(BufferDesc{
		Label: "range input", Size: uint64(len(inputBytes)),
		Usage: BufferUsageUniform | BufferUsageCopyDst,
	})
	defer input.Release()
	input.Write(0, inputBytes)

	module := dev.CreateShaderModule(`
struct Input { value: vec4<u32> }
@group(0) @binding(0) var<uniform> input: Input;
@group(0) @binding(1) var<storage, read_write> output: array<u32>;
@compute @workgroup_size(1)
fn main() { output[0] = input.value.x; }
`)
	defer module.Release()
	layout := BindGroupLayout{
		Label: "range layout",
		Entries: []BindGroupLayoutEntry{
			{Binding: 0, Type: BindingUniformBuffer, VisibleIn: StageCompute},
			{Binding: 1, Type: BindingStorageBufferRW, VisibleIn: StageCompute},
		},
	}
	pipeline := dev.CreateComputePipeline(ComputePipelineDesc{
		Label: "range pipeline", Shader: module, Entry: "main", BindGroups: []BindGroupLayout{layout},
	})
	defer pipeline.Release()

	dispatch := func(t *testing.T, inputEntry BindGroupEntry) uint32 {
		t.Helper()
		output := dev.CreateBuffer(BufferDesc{
			Label: "range output", Size: 4,
			Usage: BufferUsageStorage | BufferUsageCopySrc,
		})
		defer output.Release()
		group := dev.CreateBindGroup(BindGroupDesc{
			Label: "range resources", Layout: layout,
			Entries: []BindGroupEntry{
				inputEntry,
				{Binding: 1, Buffer: output},
			},
		})
		defer group.Release()
		encoder := dev.CreateCommandEncoder()
		pass := encoder.BeginComputePass("range dispatch")
		pass.SetPipeline(pipeline)
		pass.SetBindGroup(0, group)
		pass.Dispatch(1, 1, 1)
		pass.End()
		command := encoder.Finish()
		dev.Submit(command)
		command.Release()
		dev.Poll(true)
		return binary.LittleEndian.Uint32(output.ReadBack())
	}

	if got := dispatch(t, BindGroupEntry{Binding: 0, Buffer: input}); got != 11 {
		t.Fatalf("default whole-buffer binding read=%d want=11", got)
	}
	if got := dispatch(t, BindGroupEntry{Binding: 0, Buffer: input, Offset: 256, Size: 16}); got != 22 {
		t.Fatalf("explicit ranged binding read=%d want=22", got)
	}
}

func panicMessage(value any) string {
	switch value := value.(type) {
	case error:
		return value.Error()
	case string:
		return value
	default:
		return fmt.Sprint(value)
	}
}
