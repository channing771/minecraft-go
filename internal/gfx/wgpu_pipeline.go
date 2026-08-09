//go:build darwin

package gfx

import (
	"fmt"

	"github.com/oliverbestmann/webgpu/wgpu"
)

// createBindGroupLayouts 按描述实例化底层布局对象，并返回统一的释放函数。
//
// gfx 把 BindGroupLayout 设计成纯描述，所以建管线和建 bind group 时各自实例化
// 一次。WebGPU 的布局兼容性按结构判定，wgpu-core 也会对相同描述符做去重，
// 因此两边分别创建不会破坏兼容性。管线/bind group 内部持有引用计数，
// 所以创建完立即释放这些临时句柄是安全的。
func (d *wgpuDevice) createBindGroupLayouts(groups []BindGroupLayout) ([]*wgpu.BindGroupLayout, func()) {
	out := make([]*wgpu.BindGroupLayout, 0, len(groups))
	release := func() {
		for _, l := range out {
			l.Release()
		}
	}
	for _, g := range groups {
		entries := make([]wgpu.BindGroupLayoutEntry, len(g.Entries))
		for i, e := range g.Entries {
			entries[i] = toBindGroupLayoutEntry(e)
		}
		layout, err := d.device.TryCreateBindGroupLayout(&wgpu.BindGroupLayoutDescriptor{
			Label:   g.Label,
			Entries: entries,
		})
		if err != nil {
			release()
			panic(fmt.Errorf("gfx: 创建 bind group layout %q 失败: %w", g.Label, err))
		}
		out = append(out, layout)
	}
	return out, release
}

// toBindGroupLayoutEntry 把 gfx 的单一 BindingType 展开成绑定里那四个并列的
// 子结构。留空的子结构其 Type 字段零值即 "BindingNotUsed"，正是我们要的。
func toBindGroupLayoutEntry(e BindGroupLayoutEntry) wgpu.BindGroupLayoutEntry {
	out := wgpu.BindGroupLayoutEntry{
		Binding:    e.Binding,
		Visibility: toShaderStage(e.VisibleIn),
	}
	switch e.Type {
	case BindingUniformBuffer:
		out.Buffer.Type = wgpu.BufferBindingTypeUniform
	case BindingStorageBufferRO:
		out.Buffer.Type = wgpu.BufferBindingTypeReadOnlyStorage
	case BindingStorageBufferRW:
		out.Buffer.Type = wgpu.BufferBindingTypeStorage
	case BindingSampledTextureFloat:
		out.Texture = wgpu.TextureBindingLayout{
			SampleType:    wgpu.TextureSampleTypeFloat,
			ViewDimension: toViewDimension(e.ViewDimension),
		}
	case BindingSampledTextureUnfilterableFloat:
		out.Texture = wgpu.TextureBindingLayout{
			SampleType:    wgpu.TextureSampleTypeUnfilterableFloat,
			ViewDimension: toViewDimension(e.ViewDimension),
		}
	case BindingDepthTexture:
		out.Texture = wgpu.TextureBindingLayout{
			SampleType:    wgpu.TextureSampleTypeDepth,
			ViewDimension: toViewDimension(e.ViewDimension),
		}
	case BindingStorageTextureWrite:
		out.StorageTexture = wgpu.StorageTextureBindingLayout{
			Access:        wgpu.StorageTextureAccessWriteOnly,
			Format:        toFormat(e.StorageFormat),
			ViewDimension: toViewDimension(e.ViewDimension),
		}
	case BindingSampler:
		out.Sampler.Type = wgpu.SamplerBindingTypeFiltering
	default:
		panic(fmt.Errorf("gfx: 未知的绑定类型 %d", e.Type))
	}
	return out
}

// createPipelineLayout 建出显式的管线布局。即使一个 bind group 都没有也建空布局，
// 而不是传 nil 走 auto layout——auto layout 推导出的布局跟我们自己声明的对不上。
func (d *wgpuDevice) createPipelineLayout(label string, groups []BindGroupLayout) (*wgpu.PipelineLayout, func()) {
	layouts, releaseLayouts := d.createBindGroupLayouts(groups)
	pl, err := d.device.TryCreatePipelineLayout(&wgpu.PipelineLayoutDescriptor{
		Label:            label + " layout",
		BindGroupLayouts: layouts,
	})
	if err != nil {
		releaseLayouts()
		panic(fmt.Errorf("gfx: 创建管线布局 %q 失败: %w", label, err))
	}
	return pl, func() {
		pl.Release()
		releaseLayouts()
	}
}

func (d *wgpuDevice) CreateRenderPipeline(desc RenderPipelineDesc) RenderPipeline {
	shader, ok := desc.Shader.(*wgpuShaderModule)
	if !ok {
		panic("gfx: RenderPipelineDesc.Shader 不是本后端创建的着色器模块")
	}

	layout, releaseLayout := d.createPipelineLayout(desc.Label, desc.BindGroups)
	defer releaseLayout()

	buffers := make([]wgpu.VertexBufferLayout, len(desc.Buffers))
	for i, b := range desc.Buffers {
		attrs := make([]wgpu.VertexAttribute, len(b.Attributes))
		for j, a := range b.Attributes {
			attrs[j] = wgpu.VertexAttribute{
				Format:         toVertexFormat(a.Format),
				Offset:         a.Offset,
				ShaderLocation: a.ShaderLocation,
			}
		}
		stepMode := wgpu.VertexStepModeVertex
		if b.StepModeInstance {
			stepMode = wgpu.VertexStepModeInstance
		}
		buffers[i] = wgpu.VertexBufferLayout{
			ArrayStride: b.ArrayStride,
			StepMode:    stepMode,
			Attributes:  attrs,
		}
	}

	var depthStencil *wgpu.DepthStencilState
	if desc.DepthFormat != FormatUndefined {
		depthWrite := wgpu.OptionalBoolFalse
		if desc.DepthWrite {
			depthWrite = wgpu.OptionalBoolTrue
		}
		// 不用模板缓冲，但 stencil 面状态仍须填成"永远通过 + 保持"，
		// 留零值会被解读成 Undefined 而触发验证层报错。
		keep := wgpu.StencilFaceState{
			Compare:     wgpu.CompareFunctionAlways,
			FailOp:      wgpu.StencilOperationKeep,
			DepthFailOp: wgpu.StencilOperationKeep,
			PassOp:      wgpu.StencilOperationKeep,
		}
		depthStencil = &wgpu.DepthStencilState{
			Format:            toFormat(desc.DepthFormat),
			DepthWriteEnabled: depthWrite,
			DepthCompare:      wgpu.CompareFunctionLess,
			StencilFront:      keep,
			StencilBack:       keep,
			StencilReadMask:   0xFFFFFFFF,
			StencilWriteMask:  0xFFFFFFFF,
		}
	}

	blend := toBlendState(desc.Blend)
	pipeline := must(d.device.TryCreateRenderPipeline(&wgpu.RenderPipelineDescriptor{
		Label:  desc.Label,
		Layout: layout,
		Vertex: wgpu.VertexState{
			Module:     shader.module,
			EntryPoint: desc.VertexEntry,
			Buffers:    buffers,
		},
		Primitive: wgpu.PrimitiveState{
			Topology:  wgpu.PrimitiveTopologyTriangleList,
			FrontFace: wgpu.FrontFaceCCW,
			CullMode:  wgpu.CullModeNone,
		},
		DepthStencil: depthStencil,
		Multisample: wgpu.MultisampleState{
			// 不开 MSAA：Count 必须是 1，Mask 必须是全 1。
			Count: 1,
			Mask:  0xFFFFFFFF,
		},
		Fragment: &wgpu.FragmentState{
			Module:     shader.module,
			EntryPoint: desc.FragmentEntry,
			Targets: []wgpu.ColorTargetState{{
				Format:    toFormat(desc.ColorFormat),
				Blend:     &blend,
				WriteMask: wgpu.ColorWriteMaskAll,
			}},
		},
	}))
	return &wgpuRenderPipeline{pipeline: pipeline}
}

func (d *wgpuDevice) CreateComputePipeline(desc ComputePipelineDesc) ComputePipeline {
	shader, ok := desc.Shader.(*wgpuShaderModule)
	if !ok {
		panic("gfx: ComputePipelineDesc.Shader 不是本后端创建的着色器模块")
	}

	layout, releaseLayout := d.createPipelineLayout(desc.Label, desc.BindGroups)
	defer releaseLayout()

	pipeline := must(d.device.TryCreateComputePipeline(&wgpu.ComputePipelineDescriptor{
		Label:  desc.Label,
		Layout: layout,
		Compute: wgpu.ProgrammableStageDescriptor{
			Module:     shader.module,
			EntryPoint: desc.Entry,
		},
	}))
	return &wgpuComputePipeline{pipeline: pipeline}
}

func (d *wgpuDevice) CreateBindGroup(desc BindGroupDesc) BindGroup {
	layouts, releaseLayouts := d.createBindGroupLayouts([]BindGroupLayout{desc.Layout})
	defer releaseLayouts()

	entries := make([]wgpu.BindGroupEntry, len(desc.Entries))
	for i, e := range desc.Entries {
		out := wgpu.BindGroupEntry{Binding: e.Binding}
		switch {
		case e.Buffer != nil:
			b, ok := e.Buffer.(*wgpuBuffer)
			if !ok {
				panic("gfx: BindGroupEntry.Buffer 不是本后端创建的缓冲")
			}
			out.Buffer = b.buf
			out.Offset, out.Size = resolveBindGroupBufferRange(desc.Label, e.Binding, e, b.buf.GetSize())
		case e.Texture != nil:
			v, ok := e.Texture.(*wgpuTextureView)
			if !ok {
				panic("gfx: BindGroupEntry.Texture 不是本后端创建的纹理视图")
			}
			out.TextureView = v.view
		case e.Sampler != nil:
			s, ok := e.Sampler.(*wgpuSampler)
			if !ok {
				panic("gfx: BindGroupEntry.Sampler 不是本后端创建的采样器")
			}
			out.Sampler = s.sampler
		default:
			panic(fmt.Errorf("gfx: bind group %q 的 binding %d 没有指定任何资源", desc.Label, e.Binding))
		}
		entries[i] = out
	}

	group := must(d.device.TryCreateBindGroup(&wgpu.BindGroupDescriptor{
		Label:   desc.Label,
		Layout:  layouts[0],
		Entries: entries,
	}))
	return &wgpuBindGroup{group: group}
}

func resolveBindGroupBufferRange(
	groupLabel string,
	binding uint32,
	entry BindGroupEntry,
	bufferSize uint64,
) (uint64, uint64) {
	if entry.Offset == 0 && entry.Size == 0 {
		return 0, bufferSize
	}
	if entry.Size == 0 {
		panic(fmt.Errorf(
			"gfx: bind group %q binding %d 的 buffer range offset=%d size=0 要求显式 size > 0",
			groupLabel, binding, entry.Offset,
		))
	}
	if entry.Offset > bufferSize || entry.Size > bufferSize-entry.Offset {
		panic(fmt.Errorf(
			"gfx: bind group %q binding %d 的 buffer range offset=%d size=%d 超出 buffer size=%d",
			groupLabel, binding, entry.Offset, entry.Size, bufferSize,
		))
	}
	return entry.Offset, entry.Size
}
