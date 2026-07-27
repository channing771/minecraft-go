//go:build darwin

package gfx_test

import (
	"encoding/binary"
	"testing"

	"minecraft-go/internal/gfx"
)

// TestComputeFillsIndirectArgs 验证 compute shader 能通过原子累加
// 决定 instanceCount 并写入 indirect 参数缓冲。
// 这是 GPU-driven 管线成立的前提（spec §2.3）。
func TestComputeFillsIndirectArgs(t *testing.T) {
	dev, err := gfx.NewHeadlessDevice()
	if err != nil {
		t.Skipf("本机无可用 GPU 适配器: %v", err)
	}
	defer dev.Release()

	// 128 个候选，其中偶数号通过筛选，期望 instanceCount == 64。
	const candidates = 128
	const wantInstances = candidates / 2

	// indirect 参数布局：indexCount, instanceCount, firstIndex, baseVertex, firstInstance
	args := make([]byte, 5*4)
	binary.LittleEndian.PutUint32(args[0:], 6) // indexCount：一个四边形 6 个索引
	// instanceCount 由 compute 累加，初始为 0。

	argsBuf := dev.CreateBuffer(gfx.BufferDesc{
		Label: "indirect-args",
		Size:  uint64(len(args)),
		Usage: gfx.BufferUsageIndirect | gfx.BufferUsageStorage |
			gfx.BufferUsageCopyDst | gfx.BufferUsageCopySrc,
	})
	defer argsBuf.Release()
	argsBuf.Write(0, args)

	visible := dev.CreateBuffer(gfx.BufferDesc{
		Label: "visible-out",
		Size:  candidates * 4,
		Usage: gfx.BufferUsageStorage,
	})
	defer visible.Release()

	shader := dev.CreateShaderModule(gfx.ShaderSpikeCull)
	defer shader.Release()

	layout := gfx.BindGroupLayout{
		Label: "cull-layout",
		Entries: []gfx.BindGroupLayoutEntry{
			{Binding: 0, Type: gfx.BindingStorageBufferRW, VisibleIn: gfx.StageCompute},
			{Binding: 1, Type: gfx.BindingStorageBufferRW, VisibleIn: gfx.StageCompute},
		},
	}
	pipe := dev.CreateComputePipeline(gfx.ComputePipelineDesc{
		Label:      "spike-cull",
		Shader:     shader,
		Entry:      "cs_main",
		BindGroups: []gfx.BindGroupLayout{layout},
	})
	defer pipe.Release()

	bg := dev.CreateBindGroup(gfx.BindGroupDesc{
		Label:  "cull-bg",
		Layout: layout,
		Entries: []gfx.BindGroupEntry{
			{Binding: 0, Buffer: argsBuf},
			{Binding: 1, Buffer: visible},
		},
	})
	defer bg.Release()

	enc := dev.CreateCommandEncoder()
	pass := enc.BeginComputePass("cull")
	pass.SetPipeline(pipe)
	pass.SetBindGroup(0, bg)
	pass.Dispatch(candidates/64, 1, 1)
	pass.End()
	cmd := enc.Finish()
	dev.Submit(cmd)
	cmd.Release()
	dev.Poll(true)

	got := argsBuf.ReadBack()
	if n := binary.LittleEndian.Uint32(got[4:]); n != wantInstances {
		t.Fatalf("instanceCount = %d，想要 %d", n, wantInstances)
	}
	if n := binary.LittleEndian.Uint32(got[0:]); n != 6 {
		t.Fatalf("indexCount 被意外改写 = %d，想要 6", n)
	}
}
