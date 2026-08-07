//go:build darwin

// 本文件是 gfx 接口的 WebGPU 后端实现，也是整个仓库中唯一 import
// github.com/oliverbestmann/webgpu/... 的文件。换绑定只需重写这一个文件。

package gfx

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa -framework QuartzCore

#import <Cocoa/Cocoa.h>
#import <QuartzCore/CAMetalLayer.h>
#include <stdint.h>

// wgpu 的 Metal surface 要的是一个 CAMetalLayer，而不是 NSWindow 本身。
// 这里给 NSWindow 的 contentView 挂上一个新建的 CAMetalLayer 并把它返回。
// 实现照抄绑定自带的 wgpuglfw/surface_darwin.go——那份代码依赖 *glfw.Window，
// 而本包不允许 import GLFW，所以只能在这里按原样重写一遍。
//
// 入参故意用 uintptr_t 而不是指针：这样 Go 侧就不必做 uintptr → unsafe.Pointer
// 的转换，go vet 的 unsafeptr 检查也就无话可说。
static void *metalLayerFromNSWindow(uintptr_t nsWindowRef) {
	NSWindow *nsWindow = (__bridge NSWindow *)(void *)nsWindowRef;
	id metalLayer = NULL;
	[nsWindow.contentView setWantsLayer:YES];
	metalLayer = [CAMetalLayer layer];
	[nsWindow.contentView setLayer:metalLayer];
	return metalLayer;
}
*/
import "C"

import (
	"fmt"
	"log"

	"github.com/oliverbestmann/webgpu/wgpu"
)

// must 把「(值, error)」收敛成「值」，出错即 panic。
// gfx 的接口刻意不返回 error（见 gfx.go 顶部的错误处理约定），
// 资源创建失败在本工程视为程序员错误。
func must[T any](v T, err error) T {
	if err != nil {
		panic(fmt.Errorf("gfx: %w", err))
	}
	return v
}

// check 是 must 的无返回值版本。
func check(err error) {
	if err != nil {
		panic(fmt.Errorf("gfx: %w", err))
	}
}

// ---------------------------------------------------------------------------
// 枚举映射：gfx 的中立枚举 → 绑定的枚举
// ---------------------------------------------------------------------------

func toBufferUsage(u BufferUsage) wgpu.BufferUsage {
	var out wgpu.BufferUsage
	for gfxBit, wgpuBit := range map[BufferUsage]wgpu.BufferUsage{
		BufferUsageVertex:   wgpu.BufferUsageVertex,
		BufferUsageIndex:    wgpu.BufferUsageIndex,
		BufferUsageUniform:  wgpu.BufferUsageUniform,
		BufferUsageStorage:  wgpu.BufferUsageStorage,
		BufferUsageIndirect: wgpu.BufferUsageIndirect,
		BufferUsageCopySrc:  wgpu.BufferUsageCopySrc,
		BufferUsageCopyDst:  wgpu.BufferUsageCopyDst,
		BufferUsageMapRead:  wgpu.BufferUsageMapRead,
	} {
		if u&gfxBit != 0 {
			out |= wgpuBit
		}
	}
	return out
}

func toTextureUsage(u TextureUsage) wgpu.TextureUsage {
	var out wgpu.TextureUsage
	for gfxBit, wgpuBit := range map[TextureUsage]wgpu.TextureUsage{
		TextureUsageBinding:      wgpu.TextureUsageTextureBinding,
		TextureUsageRenderTarget: wgpu.TextureUsageRenderAttachment,
		TextureUsageCopyDst:      wgpu.TextureUsageCopyDst,
		TextureUsageStorage:      wgpu.TextureUsageStorageBinding,
		TextureUsageCopySrc:      wgpu.TextureUsageCopySrc,
	} {
		if u&gfxBit != 0 {
			out |= wgpuBit
		}
	}
	return out
}

func toShaderStage(s ShaderStage) wgpu.ShaderStage {
	var out wgpu.ShaderStage
	if s&StageVertex != 0 {
		out |= wgpu.ShaderStageVertex
	}
	if s&StageFragment != 0 {
		out |= wgpu.ShaderStageFragment
	}
	if s&StageCompute != 0 {
		out |= wgpu.ShaderStageCompute
	}
	return out
}

func toFormat(f TextureFormat) wgpu.TextureFormat {
	switch f {
	case FormatUndefined:
		return wgpu.TextureFormatUndefined
	case FormatBGRA8Unorm:
		return wgpu.TextureFormatBGRA8Unorm
	case FormatBGRA8UnormSrgb:
		return wgpu.TextureFormatBGRA8UnormSrgb
	case FormatRGBA8Unorm:
		return wgpu.TextureFormatRGBA8Unorm
	case FormatDepth32Float:
		return wgpu.TextureFormatDepth32Float
	case FormatR32Float:
		return wgpu.TextureFormatR32Float
	case FormatR32Uint:
		return wgpu.TextureFormatR32Uint
	case FormatR8Unorm:
		return wgpu.TextureFormatR8Unorm
	}
	panic(fmt.Errorf("gfx: 未知的纹理格式 %d", f))
}

// fromFormat 是 toFormat 的反向映射，只覆盖 gfx 认识的格式。
// 用于把 surface 报回来的格式翻译成中立枚举。
func fromFormat(f wgpu.TextureFormat) (TextureFormat, bool) {
	switch f {
	case wgpu.TextureFormatBGRA8Unorm:
		return FormatBGRA8Unorm, true
	case wgpu.TextureFormatBGRA8UnormSrgb:
		return FormatBGRA8UnormSrgb, true
	case wgpu.TextureFormatRGBA8Unorm:
		return FormatRGBA8Unorm, true
	case wgpu.TextureFormatDepth32Float:
		return FormatDepth32Float, true
	case wgpu.TextureFormatR32Float:
		return FormatR32Float, true
	case wgpu.TextureFormatR32Uint:
		return FormatR32Uint, true
	case wgpu.TextureFormatR8Unorm:
		return FormatR8Unorm, true
	}
	return FormatUndefined, false
}

// toBlendState 把 gfx 混合模式映射成 WebGPU 的颜色附件状态。
func toBlendState(mode BlendMode) wgpu.BlendState {
	switch mode {
	case BlendReplace:
		return wgpu.BlendStateReplace
	case BlendAlpha:
		return wgpu.BlendState{
			Color: wgpu.BlendComponent{
				Operation: wgpu.BlendOperationAdd,
				SrcFactor: wgpu.BlendFactorSrcAlpha,
				DstFactor: wgpu.BlendFactorOneMinusSrcAlpha,
			},
			Alpha: wgpu.BlendComponent{
				Operation: wgpu.BlendOperationAdd,
				SrcFactor: wgpu.BlendFactorOne,
				DstFactor: wgpu.BlendFactorOneMinusSrcAlpha,
			},
		}
	}
	panic(fmt.Errorf("gfx: 未知的混合模式 %d", mode))
}

func toViewDimension(d TextureViewDimension) wgpu.TextureViewDimension {
	switch d {
	case TextureViewDimensionAuto:
		return wgpu.TextureViewDimensionUndefined
	case TextureViewDimension2D:
		return wgpu.TextureViewDimension2D
	case TextureViewDimension2DArray:
		return wgpu.TextureViewDimension2DArray
	}
	panic(fmt.Errorf("gfx: 未知的视图维度 %d", d))
}

func toAspect(a TextureAspect) wgpu.TextureAspect {
	if a == AspectDepthOnly {
		return wgpu.TextureAspectDepthOnly
	}
	return wgpu.TextureAspectAll
}

func toFilter(f FilterMode) wgpu.FilterMode {
	if f == FilterLinear {
		return wgpu.FilterModeLinear
	}
	return wgpu.FilterModeNearest
}

func toMipFilter(f FilterMode) wgpu.MipmapFilterMode {
	if f == FilterLinear {
		return wgpu.MipmapFilterModeLinear
	}
	return wgpu.MipmapFilterModeNearest
}

func toAddressMode(m AddressMode) wgpu.AddressMode {
	if m == AddressRepeat {
		return wgpu.AddressModeRepeat
	}
	return wgpu.AddressModeClampToEdge
}

func toVertexFormat(f VertexFormat) wgpu.VertexFormat {
	switch f {
	case VertexFormatUint32x2:
		return wgpu.VertexFormatUint32x2
	case VertexFormatFloat32x3:
		return wgpu.VertexFormatFloat32x3
	}
	panic(fmt.Errorf("gfx: 未知的顶点格式 %d", f))
}

func toLoadOp(clear bool) wgpu.LoadOp {
	if clear {
		return wgpu.LoadOpClear
	}
	return wgpu.LoadOpLoad
}

// ---------------------------------------------------------------------------
// Device
// ---------------------------------------------------------------------------

type wgpuDevice struct {
	instance *wgpu.Instance
	adapter  *wgpu.Adapter
	device   *wgpu.Device
	queue    *wgpu.Queue
}

// NewDevice 用给定的原生窗口句柄创建设备与 surface。
// handle 由 internal/client 从 GLFW 取得，本包不 import GLFW——
// 保持 gfx 与窗口库解耦。
//
// width/height 应当是帧缓冲（像素）尺寸，Retina 屏上它是窗口逻辑尺寸的两倍。
// 返回的 Device 与 Surface 都需要调用方显式 Release。
func NewDevice(handle NativeWindowHandle, width, height uint32) (_ Device, _ Surface, err error) {
	if handle.Kind == HandleNone {
		return nil, nil, fmt.Errorf("gfx: NewDevice 需要窗口句柄；headless 场景请调用 NewHeadlessDevice")
	}
	return newDevice(handle, width, height)
}

// newDevice 是窗口设备与 headless 设备共用的创建路径。
// HandleNone 时不创建 surface，返回的 Surface 为 nil。
func newDevice(handle NativeWindowHandle, width, height uint32) (_ Device, _ Surface, err error) {
	if handle.Kind != HandleNone && handle.Kind != HandleKindNSWindow {
		return nil, nil, fmt.Errorf("gfx: 本平台只支持 NSWindow 句柄，收到 kind=%d", handle.Kind)
	}

	// 打开绑定的日志输出，这样 wgpu 的验证层告警会直接打到 stderr——
	// M0 的验收标准之一就是"终端无验证层报错"，没有这一行就无从观察。
	wgpu.SetLogLevel(wgpu.LogLevelWarn)

	d := &wgpuDevice{}
	var s *wgpuSurface
	defer func() {
		// 中途失败时把已经建出来的对象释放掉，避免半成品泄漏。
		// 闭包捕获的是局部变量而非返回值——出错路径上返回的是 nil。
		if err != nil {
			if s != nil {
				s.Release()
			}
			d.Release()
		}
	}()

	d.instance = wgpu.CreateInstance(nil)

	var compatibleSurface *wgpu.Surface
	if handle.Kind == HandleKindNSWindow {
		s = &wgpuSurface{}
		// handle.Pointer 是从窗口库拿到的 NSWindow 指针，它指向 Objective-C 运行时
		// 管理的对象，不受 Go GC 管辖，所以以整数形式跨过 cgo 边界是安全的。
		metalLayer := C.metalLayerFromNSWindow(C.uintptr_t(handle.Pointer))
		s.surface = d.instance.CreateSurface(&wgpu.SurfaceDescriptor{
			Label:      "gfx surface",
			MetalLayer: &wgpu.SurfaceSourceMetalLayer{Layer: metalLayer},
		})
		compatibleSurface = s.surface
	}

	d.adapter, err = d.instance.RequestAdapter(&wgpu.RequestAdapterOptions{
		CompatibleSurface: compatibleSurface,
		PowerPreference:   wgpu.PowerPreferenceHighPerformance,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("gfx: 请求 adapter 失败: %w", err)
	}

	d.device, err = d.adapter.RequestDevice(&wgpu.DeviceDescriptor{Label: "gfx device"})
	if err != nil {
		return nil, nil, fmt.Errorf("gfx: 请求 device 失败: %w", err)
	}
	d.queue = d.device.GetQueue()

	info := d.adapter.GetInfo()
	log.Printf("gfx: 后端=%v 适配器=%q 类型=%v", info.BackendType, info.Device, info.AdapterType)

	if s == nil {
		return d, nil, nil
	}

	caps := s.surface.GetCapabilities(d.adapter)
	if len(caps.Formats) == 0 || len(caps.AlphaModes) == 0 {
		return nil, nil, fmt.Errorf("gfx: surface 没有报告任何可用格式或 alpha 模式")
	}
	// caps.Formats[0] 是驱动的首选格式。它必须能翻译成 gfx 的中立枚举，
	// 否则上层没法用 Surface.Format() 的结果去建管线——这种情况下宁可当场报错，
	// 也好过让管线的颜色目标跟 surface 悄悄对不上。
	surfaceFormat, ok := fromFormat(caps.Formats[0])
	if !ok {
		return nil, nil, fmt.Errorf("gfx: surface 首选格式 %v 不在 gfx 支持的格式集合内", caps.Formats[0])
	}

	s.device = d
	s.presentModes = caps.PresentModes
	s.format = surfaceFormat
	s.config = &wgpu.SurfaceConfiguration{
		Usage:       wgpu.TextureUsageRenderAttachment,
		Format:      caps.Formats[0],
		Width:       width,
		Height:      height,
		PresentMode: wgpu.PresentModeFifo, // 默认 VSync；Task 17 的基准测试会切成 Immediate
		AlphaMode:   caps.AlphaModes[0],
	}
	s.surface.Configure(d.device, s.config)

	// 把实测到的 surface 能力打出来——这是 M0 的验证证据，也是选 present mode 的依据。
	log.Printf("gfx: surface 格式=%v present 模式=%v", caps.Formats, caps.PresentModes)

	return d, s, nil
}

func (d *wgpuDevice) CreateBuffer(desc BufferDesc) Buffer {
	buf := must(d.device.TryCreateBuffer(&wgpu.BufferDescriptor{
		Label:            desc.Label,
		Size:             desc.Size,
		Usage:            toBufferUsage(desc.Usage),
		MappedAtCreation: desc.MappedAtCreation,
	}))
	return &wgpuBuffer{device: d, buf: buf}
}

func (d *wgpuDevice) CreateShaderModule(wgsl string) ShaderModule {
	mod := must(d.device.TryCreateShaderModule(&wgpu.ShaderModuleDescriptor{
		WGSLSource: &wgpu.ShaderSourceWGSL{Code: wgsl},
	}))
	return &wgpuShaderModule{module: mod}
}

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

func (d *wgpuDevice) CreateTexture(desc TextureDesc) Texture {
	layers := max(desc.Layers, 1)
	mips := max(desc.MipLevels, 1)
	format := toFormat(desc.Format)

	// 2D 与 2D array 在 WebGPU 里同属 TextureDimension2D，区别只在层数；
	// 维度差异体现在创建视图时。
	tex := must(d.device.TryCreateTexture(&wgpu.TextureDescriptor{
		Label:     desc.Label,
		Usage:     toTextureUsage(desc.Usage),
		Dimension: wgpu.TextureDimension2D,
		Size: wgpu.Extent3D{
			Width:              desc.Width,
			Height:             desc.Height,
			DepthOrArrayLayers: layers,
		},
		Format:        format,
		MipLevelCount: mips,
		SampleCount:   1,
	}))
	return &wgpuTexture{
		device:       d,
		texture:      tex,
		width:        desc.Width,
		height:       desc.Height,
		layers:       layers,
		mipLevels:    mips,
		format:       format,
		writeTexture: d.queue.TryWriteTexture,
	}
}

func (d *wgpuDevice) CreateSampler(desc SamplerDesc) Sampler {
	sampler := must(d.device.TryCreateSampler(&wgpu.SamplerDescriptor{
		Label:        desc.Label,
		AddressModeU: toAddressMode(desc.Address),
		AddressModeV: toAddressMode(desc.Address),
		AddressModeW: toAddressMode(desc.Address),
		MagFilter:    toFilter(desc.MagFilter),
		MinFilter:    toFilter(desc.MinFilter),
		MipmapFilter: toMipFilter(desc.MipFilter),
		// LodMaxClamp 留 0 会把采样钳死在 mip 0，这里放开到全部级别。
		LodMinClamp:   0,
		LodMaxClamp:   32,
		MaxAnisotropy: 1,
	}))
	return &wgpuSampler{sampler: sampler}
}

func (d *wgpuDevice) CreateCommandEncoder() CommandEncoder {
	enc := must(d.device.TryCreateCommandEncoder(&wgpu.CommandEncoderDescriptor{}))
	return &wgpuEncoder{encoder: enc}
}

func (d *wgpuDevice) Submit(buffers ...CommandBuffer) {
	native := make([]*wgpu.CommandBuffer, len(buffers))
	for i, b := range buffers {
		cb, ok := b.(*wgpuCommandBuffer)
		if !ok {
			panic("gfx: Submit 收到的不是本后端创建的命令缓冲")
		}
		native[i] = cb.buffer
	}
	d.queue.Submit(native...)
}

func (d *wgpuDevice) Poll(wait bool) {
	d.device.Poll(wait, nil)
}

// Release 逆着创建顺序释放，可以在半初始化的设备上安全调用。
func (d *wgpuDevice) Release() {
	if d.queue != nil {
		d.queue.Release()
		d.queue = nil
	}
	if d.device != nil {
		d.device.Release()
		d.device = nil
	}
	if d.adapter != nil {
		d.adapter.Release()
		d.adapter = nil
	}
	if d.instance != nil {
		d.instance.Release()
		d.instance = nil
	}
}

// ---------------------------------------------------------------------------
// Surface
// ---------------------------------------------------------------------------

type wgpuSurface struct {
	device       *wgpuDevice
	surface      *wgpu.Surface
	config       *wgpu.SurfaceConfiguration
	presentModes []wgpu.PresentMode
	// format 是 config.Format 翻译成 gfx 中立枚举后的结果，NewDevice 里解析好。
	format TextureFormat

	// 当前帧已取得但尚未 Present 的纹理与视图，Present 时一并释放。
	frameTexture *wgpu.Texture
	frameView    *wgpu.TextureView
}

// Acquire 取当前帧的颜色附件视图。返回的视图归 Surface 所有，
// Present 时会连同底层 surface 纹理一起释放，调用方不必也不应释放它。
func (s *wgpuSurface) Acquire() TextureView {
	if s.frameTexture != nil {
		panic("gfx: 上一帧的 surface 纹理还没 Present，不能再次 Acquire")
	}

	st, err := s.surface.TryGetCurrentTexture()
	if err != nil {
		// 取纹理失败是瞬时状况（超时、surface 过期），跳过这一帧即可。
		log.Printf("gfx: 获取 surface 纹理失败，跳过本帧: %v", err)
		return nil
	}
	texture, ok := st.Get()
	if !ok {
		// 窗口被遮挡、最小化或 surface 已过期。
		return nil
	}

	view, err := texture.TryCreateView(nil)
	if err != nil {
		texture.Release()
		log.Printf("gfx: 创建 surface 纹理视图失败，跳过本帧: %v", err)
		return nil
	}

	s.frameTexture = texture
	s.frameView = view
	return &wgpuTextureView{view: view}
}

// Present 呈现当前帧。必须在 Submit 之后调用——wgpu-native 要求
// 先 Present 再释放 surface 纹理，提前释放会触发验证层报错。
func (s *wgpuSurface) Present() {
	if s.frameTexture == nil {
		return
	}
	s.surface.Present()
	s.frameView.Release()
	s.frameTexture.Release()
	s.frameView = nil
	s.frameTexture = nil
}

func (s *wgpuSurface) SetPresentMode(m PresentMode) error {
	// 绑定不提供"自动 VSync / 自动非 VSync"的封装，只能自己从 caps 里挑。
	// Metal 后端实测只上报 [fifo immediate]，没有 mailbox。
	var want wgpu.PresentMode
	switch m {
	case PresentModeAutoVSync:
		want = wgpu.PresentModeFifo
	case PresentModeAutoNoVSync:
		want = wgpu.PresentModeImmediate
	default:
		return fmt.Errorf("gfx: 未知的 present 模式 %d", m)
	}

	supported := false
	for _, pm := range s.presentModes {
		if pm == want {
			supported = true
			break
		}
	}
	if !supported {
		return fmt.Errorf("gfx: surface 不支持 present 模式 %v（可用：%v）", want, s.presentModes)
	}

	s.config.PresentMode = want
	s.surface.Configure(s.device.device, s.config)
	return nil
}

func (s *wgpuSurface) Resize(width, height uint32) {
	if width == 0 || height == 0 {
		// 窗口最小化时帧缓冲尺寸会变成 0，此时重新配置 surface 是非法的。
		return
	}
	if s.config.Width == width && s.config.Height == height {
		return
	}
	s.config.Width = width
	s.config.Height = height
	s.surface.Configure(s.device.device, s.config)
}

// Format 返回 surface 的颜色格式，用于建管线时填 ColorFormat。
func (s *wgpuSurface) Format() TextureFormat { return s.format }

func (s *wgpuSurface) Release() {
	if s.frameView != nil {
		s.frameView.Release()
		s.frameView = nil
	}
	if s.frameTexture != nil {
		s.frameTexture.Release()
		s.frameTexture = nil
	}
	if s.surface != nil {
		s.surface.Release()
		s.surface = nil
	}
	s.config = nil
}

// ---------------------------------------------------------------------------
// Buffer
// ---------------------------------------------------------------------------

type wgpuBuffer struct {
	device *wgpuDevice
	buf    *wgpu.Buffer
}

func (b *wgpuBuffer) Size() uint64 { return b.buf.GetSize() }

func (b *wgpuBuffer) Write(offset uint64, data []byte) {
	check(b.device.queue.TryWriteBuffer(b.buf, offset, data))
}

// ReadBack 按 WebGPU 的规矩绕一次 staging buffer：MapRead 只能与 CopyDst 组合，
// 带 Storage/Indirect 的工作缓冲不能直接映射。
func (b *wgpuBuffer) ReadBack() []byte {
	size := b.buf.GetSize()
	if size == 0 {
		return nil
	}
	dev := b.device

	staging := must(dev.device.TryCreateBuffer(&wgpu.BufferDescriptor{
		Label: "gfx readback staging",
		Size:  size,
		Usage: wgpu.BufferUsageMapRead | wgpu.BufferUsageCopyDst,
	}))
	defer staging.Release()

	encoder := must(dev.device.TryCreateCommandEncoder(&wgpu.CommandEncoderDescriptor{
		Label: "gfx readback encoder",
	}))
	defer encoder.Release()
	check(encoder.TryCopyBufferToBuffer(b.buf, 0, staging, 0, size))

	cmd := must(encoder.TryFinish(nil))
	defer cmd.Release()
	dev.queue.Submit(cmd)

	// MapAsync 的回调由 Poll 驱动；Poll(true) 会一直转到队列清空，
	// 返回时 status 一定已经被写过了。
	status := wgpu.MapAsyncStatusError
	check(staging.TryMapAsync(wgpu.MapModeRead, 0, size, func(s wgpu.MapAsyncStatus) {
		status = s
	}))
	dev.device.Poll(true, nil)
	if status != wgpu.MapAsyncStatusSuccess {
		panic(fmt.Errorf("gfx: 映射 staging buffer 失败: %v", status))
	}

	// GetMappedRange 返回的是映射内存上的视图，Unmap 之后就失效，必须拷出来。
	out := make([]byte, size)
	copy(out, staging.GetMappedRange(0, uint(size)))
	check(staging.TryUnmap())
	return out
}

func (b *wgpuBuffer) Release() {
	if b.buf != nil {
		b.buf.Release()
		b.buf = nil
	}
}

// ---------------------------------------------------------------------------
// 着色器、管线、bind group、采样器：清一色的薄壳
// ---------------------------------------------------------------------------

type wgpuShaderModule struct{ module *wgpu.ShaderModule }

func (m *wgpuShaderModule) Release() {
	if m.module != nil {
		m.module.Release()
		m.module = nil
	}
}

type wgpuRenderPipeline struct{ pipeline *wgpu.RenderPipeline }

func (p *wgpuRenderPipeline) Release() {
	if p.pipeline != nil {
		p.pipeline.Release()
		p.pipeline = nil
	}
}

type wgpuComputePipeline struct{ pipeline *wgpu.ComputePipeline }

func (p *wgpuComputePipeline) Release() {
	if p.pipeline != nil {
		p.pipeline.Release()
		p.pipeline = nil
	}
}

type wgpuBindGroup struct{ group *wgpu.BindGroup }

func (g *wgpuBindGroup) Release() {
	if g.group != nil {
		g.group.Release()
		g.group = nil
	}
}

type wgpuSampler struct{ sampler *wgpu.Sampler }

func (s *wgpuSampler) Release() {
	if s.sampler != nil {
		s.sampler.Release()
		s.sampler = nil
	}
}

type wgpuTextureView struct{ view *wgpu.TextureView }

func (v *wgpuTextureView) Release() {
	if v.view != nil {
		v.view.Release()
		v.view = nil
	}
}

// ---------------------------------------------------------------------------
// Texture
// ---------------------------------------------------------------------------

type wgpuTexture struct {
	device       *wgpuDevice
	texture      *wgpu.Texture
	writeTexture func(*wgpu.TexelCopyTextureInfo, []byte, *wgpu.TexelCopyBufferLayout, *wgpu.Extent3D) error
	width        uint32
	height       uint32
	layers       uint32
	mipLevels    uint32
	format       wgpu.TextureFormat
}

func (t *wgpuTexture) View(desc TextureViewDesc) TextureView {
	mipCount := uint32(wgpu.MipLevelCountUndefined)
	if desc.MipLevelCount != 0 {
		mipCount = desc.MipLevelCount
	}
	layerCount := uint32(wgpu.ArrayLayerCountUndefined)
	if desc.ArrayLayerCount != 0 {
		layerCount = desc.ArrayLayerCount
	}
	view := must(t.texture.TryCreateView(&wgpu.TextureViewDescriptor{
		Format:          t.format,
		Dimension:       toViewDimension(desc.Dimension),
		BaseMipLevel:    desc.BaseMipLevel,
		MipLevelCount:   mipCount,
		BaseArrayLayer:  desc.BaseArrayLayer,
		ArrayLayerCount: layerCount,
		Aspect:          toAspect(desc.Aspect),
	}))
	return &wgpuTextureView{view: view}
}

func (t *wgpuTexture) WriteLayer(layer, mip uint32, pixels []byte) {
	t.WriteRegion(layer, mip, 0, 0, mipSize(t.width, mip), mipSize(t.height, mip), pixels)
}

func (t *wgpuTexture) WriteRegion(layer, mip, x, y, width, height uint32, pixels []byte) {
	if layer >= t.layers {
		panic(fmt.Errorf("gfx: layer %d 超出纹理层数 %d", layer, t.layers))
	}
	if mip >= t.mipLevels {
		panic(fmt.Errorf("gfx: mip %d 超出纹理 mip 数 %d", mip, t.mipLevels))
	}
	if width == 0 || height == 0 {
		panic("gfx: 写入 region 的宽和高必须大于零")
	}
	mipWidth := mipSize(t.width, mip)
	mipHeight := mipSize(t.height, mip)
	if width > mipWidth || x > mipWidth-width || height > mipHeight || y > mipHeight-height {
		panic(fmt.Errorf("gfx: region (%d, %d, %d, %d) 超出 mip %d 尺寸 %dx%d", x, y, width, height, mip, mipWidth, mipHeight))
	}
	bytesPerPixel := t.format.ByteSize()
	if bytesPerPixel == 0 {
		panic(fmt.Errorf("gfx: 格式 %v 的每像素字节数未知，无法写入", t.format))
	}
	want := uint64(width) * uint64(height) * uint64(bytesPerPixel)
	if uint64(len(pixels)) != want {
		panic(fmt.Errorf("gfx: region payload 大小 = %d，想要 %d", len(pixels), want))
	}
	writeTexture := t.writeTexture
	if writeTexture == nil {
		writeTexture = t.device.queue.TryWriteTexture
	}
	check(writeTexture(
		&wgpu.TexelCopyTextureInfo{
			Texture:  t.texture,
			MipLevel: mip,
			Origin:   wgpu.Origin3D{X: x, Y: y, Z: layer},
			Aspect:   wgpu.TextureAspectAll,
		},
		pixels,
		&wgpu.TexelCopyBufferLayout{
			BytesPerRow:  width * bytesPerPixel,
			RowsPerImage: height,
		},
		&wgpu.Extent3D{Width: width, Height: height, DepthOrArrayLayers: 1},
	))
}

// copyBytesPerRowAlignment 是 WebGPU 对 CopyTextureToBuffer 行距的对齐要求。
const copyBytesPerRowAlignment = 256

// ReadLayer 按 WebGPU 的规矩绕一次 staging buffer，并把填充后的行距紧缩回
// 宽 × 每像素字节。CopyTextureToBuffer 要求 BytesPerRow 按 256 对齐，因此除非
// 行距恰好落在边界上，否则底层缓冲每行都带尾部填充，必须逐行拷出。
func (t *wgpuTexture) ReadLayer(layer, mip uint32) []byte {
	if layer >= t.layers {
		panic(fmt.Errorf("gfx: layer %d 超出纹理层数 %d", layer, t.layers))
	}
	if mip >= t.mipLevels {
		panic(fmt.Errorf("gfx: mip %d 超出纹理 mip 数 %d", mip, t.mipLevels))
	}
	bytesPerPixel := t.format.ByteSize()
	if bytesPerPixel == 0 {
		panic(fmt.Errorf("gfx: 格式 %v 的每像素字节数未知，无法回读", t.format))
	}
	width := mipSize(t.width, mip)
	height := mipSize(t.height, mip)
	tightRow := width * bytesPerPixel
	paddedRow := (tightRow + copyBytesPerRowAlignment - 1) /
		copyBytesPerRowAlignment * copyBytesPerRowAlignment
	size := uint64(paddedRow) * uint64(height)

	dev := t.device
	staging := must(dev.device.TryCreateBuffer(&wgpu.BufferDescriptor{
		Label: "gfx texture readback staging",
		Size:  size,
		Usage: wgpu.BufferUsageMapRead | wgpu.BufferUsageCopyDst,
	}))
	defer staging.Release()

	encoder := must(dev.device.TryCreateCommandEncoder(&wgpu.CommandEncoderDescriptor{
		Label: "gfx texture readback encoder",
	}))
	defer encoder.Release()
	check(encoder.TryCopyTextureToBuffer(
		&wgpu.TexelCopyTextureInfo{
			Texture:  t.texture,
			MipLevel: mip,
			Origin:   wgpu.Origin3D{Z: layer},
			Aspect:   wgpu.TextureAspectAll,
		},
		&wgpu.TexelCopyBufferInfo{
			Buffer: staging,
			Layout: wgpu.TexelCopyBufferLayout{
				BytesPerRow:  paddedRow,
				RowsPerImage: height,
			},
		},
		&wgpu.Extent3D{Width: width, Height: height, DepthOrArrayLayers: 1},
	))

	cmd := must(encoder.TryFinish(nil))
	defer cmd.Release()
	dev.queue.Submit(cmd)

	// MapAsync 的回调由 Poll 驱动；Poll(true) 会一直转到队列清空。
	status := wgpu.MapAsyncStatusError
	check(staging.TryMapAsync(wgpu.MapModeRead, 0, size, func(s wgpu.MapAsyncStatus) {
		status = s
	}))
	dev.device.Poll(true, nil)
	if status != wgpu.MapAsyncStatusSuccess {
		panic(fmt.Errorf("gfx: 映射纹理 staging buffer 失败: %v", status))
	}

	// GetMappedRange 返回的是映射内存上的视图，Unmap 之后就失效，必须拷出来。
	mapped := staging.GetMappedRange(0, uint(size))
	out := make([]byte, uint64(tightRow)*uint64(height))
	for row := uint32(0); row < height; row++ {
		src := uint64(row) * uint64(paddedRow)
		dst := uint64(row) * uint64(tightRow)
		copy(out[dst:dst+uint64(tightRow)], mapped[src:src+uint64(tightRow)])
	}
	check(staging.TryUnmap())
	return out
}

func mipSize(size, mip uint32) uint32 {
	if mip >= 32 {
		return 1
	}
	return max(size>>mip, 1)
}

func (t *wgpuTexture) Release() {
	if t.texture != nil {
		t.texture.Release()
		t.texture = nil
	}
}

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
