//go:build darwin

package gfx_test

import (
	"encoding/binary"
	"testing"

	"minecraft-go/internal/gfx"
)

// TestComputeDoublesInput 验证 compute shader 能读入缓冲、计算、写出缓冲，
// 且结果可以被 CPU 读回断言。这是所有 GPU 剔除逻辑可测性的基础。
func TestComputeDoublesInput(t *testing.T) {
	dev, err := gfx.NewHeadlessDevice()
	if err != nil {
		t.Skipf("本机无可用 GPU 适配器: %v", err)
	}
	defer dev.Release()

	const n = 256
	input := make([]byte, n*4)
	for i := 0; i < n; i++ {
		binary.LittleEndian.PutUint32(input[i*4:], uint32(i))
	}

	inBuf := dev.CreateBuffer(gfx.BufferDesc{
		Label: "test-in",
		Size:  uint64(len(input)),
		Usage: gfx.BufferUsageStorage | gfx.BufferUsageCopyDst,
	})
	defer inBuf.Release()
	inBuf.Write(0, input)

	outBuf := dev.CreateBuffer(gfx.BufferDesc{
		Label: "test-out",
		Size:  uint64(len(input)),
		Usage: gfx.BufferUsageStorage | gfx.BufferUsageCopySrc,
	})
	defer outBuf.Release()

	shader := dev.CreateShaderModule(gfx.ShaderTestDouble)
	defer shader.Release()

	layout := gfx.BindGroupLayout{
		Label: "test-layout",
		Entries: []gfx.BindGroupLayoutEntry{
			{Binding: 0, Type: gfx.BindingStorageBufferRO, VisibleIn: gfx.StageCompute},
			{Binding: 1, Type: gfx.BindingStorageBufferRW, VisibleIn: gfx.StageCompute},
		},
	}
	pipe := dev.CreateComputePipeline(gfx.ComputePipelineDesc{
		Label:      "test-double",
		Shader:     shader,
		Entry:      "cs_main",
		BindGroups: []gfx.BindGroupLayout{layout},
	})
	defer pipe.Release()

	bg := dev.CreateBindGroup(gfx.BindGroupDesc{
		Label:  "test-bg",
		Layout: layout,
		Entries: []gfx.BindGroupEntry{
			{Binding: 0, Buffer: inBuf},
			{Binding: 1, Buffer: outBuf},
		},
	})
	defer bg.Release()

	enc := dev.CreateCommandEncoder()
	pass := enc.BeginComputePass("double")
	pass.SetPipeline(pipe)
	pass.SetBindGroup(0, bg)
	pass.Dispatch(n/64, 1, 1)
	pass.End()
	cmd := enc.Finish()
	dev.Submit(cmd)
	cmd.Release()
	dev.Poll(true)

	got := outBuf.ReadBack()
	if len(got) != len(input) {
		t.Fatalf("读回长度 = %d，想要 %d", len(got), len(input))
	}
	for i := 0; i < n; i++ {
		want := uint32(i) * 2
		if v := binary.LittleEndian.Uint32(got[i*4:]); v != want {
			t.Fatalf("第 %d 个元素 = %d，想要 %d", i, v, want)
		}
	}
}
