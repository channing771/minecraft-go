//go:build darwin

package gfx

import "github.com/oliverbestmann/webgpu/wgpu"

// ---------------------------------------------------------------------------
// 命令编码
// ---------------------------------------------------------------------------

type wgpuEncoder struct{ encoder *wgpu.CommandEncoder }

func (e *wgpuEncoder) BeginRenderPass(desc RenderPassDesc) RenderPass {
	colorView, ok := desc.ColorView.(*wgpuTextureView)
	if !ok {
		panic("gfx: RenderPassDesc.ColorView 不是本后端创建的纹理视图")
	}

	var depth *wgpu.RenderPassDepthStencilAttachment
	if desc.DepthView != nil {
		dv, ok := desc.DepthView.(*wgpuTextureView)
		if !ok {
			panic("gfx: RenderPassDesc.DepthView 不是本后端创建的纹理视图")
		}
		depth = &wgpu.RenderPassDepthStencilAttachment{
			View:            dv.view,
			DepthLoadOp:     toLoadOp(desc.LoadClear),
			DepthStoreOp:    wgpu.StoreOpStore,
			DepthClearValue: 1.0,
		}
	}

	pass := must(e.encoder.TryBeginRenderPass(&wgpu.RenderPassDescriptor{
		Label: desc.Label,
		ColorAttachments: []wgpu.RenderPassColorAttachment{{
			View:    colorView.view,
			LoadOp:  toLoadOp(desc.LoadClear),
			StoreOp: wgpu.StoreOpStore,
			ClearValue: wgpu.Color{
				R: float64(desc.ClearColor[0]),
				G: float64(desc.ClearColor[1]),
				B: float64(desc.ClearColor[2]),
				A: float64(desc.ClearColor[3]),
			},
		}},
		DepthStencilAttachment: depth,
	}))
	return &wgpuRenderPass{pass: pass}
}

func (e *wgpuEncoder) BeginComputePass(label string) ComputePass {
	pass := e.encoder.BeginComputePass(&wgpu.ComputePassDescriptor{Label: label})
	return &wgpuComputePass{pass: pass}
}

func (e *wgpuEncoder) CopyBufferToBuffer(src Buffer, srcOffset uint64, dst Buffer, dstOffset, size uint64) {
	s, ok := src.(*wgpuBuffer)
	if !ok {
		panic("gfx: CopyBufferToBuffer 的源不是本后端创建的缓冲")
	}
	d, ok := dst.(*wgpuBuffer)
	if !ok {
		panic("gfx: CopyBufferToBuffer 的目标不是本后端创建的缓冲")
	}
	check(e.encoder.TryCopyBufferToBuffer(s.buf, srcOffset, d.buf, dstOffset, size))
}

// Finish 结束录制。encoder 本身在此一并释放——命令已经转移到 CommandBuffer 里，
// 之后再碰这个 encoder 都是非法操作，没有留着它的理由。
func (e *wgpuEncoder) Finish() CommandBuffer {
	cmd := must(e.encoder.TryFinish(nil))
	e.encoder.Release()
	e.encoder = nil
	return &wgpuCommandBuffer{buffer: cmd}
}

type wgpuCommandBuffer struct{ buffer *wgpu.CommandBuffer }

func (c *wgpuCommandBuffer) Release() {
	if c.buffer != nil {
		c.buffer.Release()
		c.buffer = nil
	}
}

// ---------------------------------------------------------------------------
// RenderPass / ComputePass
// ---------------------------------------------------------------------------

type wgpuRenderPass struct{ pass *wgpu.RenderPassEncoder }

func (p *wgpuRenderPass) SetPipeline(pipeline RenderPipeline) {
	rp, ok := pipeline.(*wgpuRenderPipeline)
	if !ok {
		panic("gfx: SetPipeline 收到的不是本后端创建的渲染管线")
	}
	p.pass.SetPipeline(rp.pipeline)
}

func (p *wgpuRenderPass) SetBindGroup(index uint32, g BindGroup) {
	bg, ok := g.(*wgpuBindGroup)
	if !ok {
		panic("gfx: SetBindGroup 收到的不是本后端创建的 bind group")
	}
	p.pass.SetBindGroup(index, bg.group, nil)
}

func (p *wgpuRenderPass) SetVertexBuffer(slot uint32, b Buffer, offset uint64) {
	buf, ok := b.(*wgpuBuffer)
	if !ok {
		panic("gfx: SetVertexBuffer 收到的不是本后端创建的缓冲")
	}
	p.pass.SetVertexBuffer(slot, buf.buf, offset, buf.buf.GetSize()-offset)
}

func (p *wgpuRenderPass) SetIndexBuffer(b Buffer, offset uint64) {
	buf, ok := b.(*wgpuBuffer)
	if !ok {
		panic("gfx: SetIndexBuffer 收到的不是本后端创建的缓冲")
	}
	p.pass.SetIndexBuffer(buf.buf, wgpu.IndexFormatUint32, offset, buf.buf.GetSize()-offset)
}

func (p *wgpuRenderPass) DrawIndexedIndirect(indirect Buffer, offset uint64) {
	buf, ok := indirect.(*wgpuBuffer)
	if !ok {
		panic("gfx: DrawIndexedIndirect 收到的不是本后端创建的缓冲")
	}
	p.pass.DrawIndexedIndirect(buf.buf, offset)
}

func (p *wgpuRenderPass) Draw(vertexCount, instanceCount uint32) {
	p.pass.Draw(vertexCount, instanceCount, 0, 0)
}

// End 结束并释放 pass。TryEnd 之后 pass 的命令已并入父 encoder 的命令流，
// 句柄只剩引用计数壳，此时释放是安全的。
func (p *wgpuRenderPass) End() {
	err := p.pass.TryEnd()
	p.pass.Release()
	p.pass = nil
	check(err)
}

type wgpuComputePass struct{ pass *wgpu.ComputePassEncoder }

func (p *wgpuComputePass) SetPipeline(pipeline ComputePipeline) {
	cp, ok := pipeline.(*wgpuComputePipeline)
	if !ok {
		panic("gfx: SetPipeline 收到的不是本后端创建的计算管线")
	}
	p.pass.SetPipeline(cp.pipeline)
}

func (p *wgpuComputePass) SetBindGroup(index uint32, g BindGroup) {
	bg, ok := g.(*wgpuBindGroup)
	if !ok {
		panic("gfx: SetBindGroup 收到的不是本后端创建的 bind group")
	}
	p.pass.SetBindGroup(index, bg.group, nil)
}

func (p *wgpuComputePass) Dispatch(x, y, z uint32) {
	p.pass.DispatchWorkgroups(x, y, z)
}

func (p *wgpuComputePass) End() {
	err := p.pass.TryEnd()
	p.pass.Release()
	p.pass = nil
	check(err)
}
