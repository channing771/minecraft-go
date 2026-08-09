//go:build darwin

// 本文件族是 gfx 的 WebGPU 后端。

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
	"log/slog"

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
	slog.Info("gfx 设备就绪",
		"backend", info.BackendType, "adapter", info.Device, "type", info.AdapterType)

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
	slog.Info("gfx surface 能力", "formats", caps.Formats, "presentModes", caps.PresentModes)

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
