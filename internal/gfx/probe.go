//go:build darwin

// M0 只验证 macOS/Metal 这一条最不确定的路径，因此本文件带 darwin 构建约束。
// 跨平台分支（Windows/HWND、Linux/X11-Wayland）留到真正需要时再加。

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

// Probe 是 M0 的最小验证入口：用给定的原生窗口句柄建立设备与 surface，
// 每次调用 Frame 清一次屏。Task 2 会把它演化成完整的设备抽象。
type Probe struct {
	instance *wgpu.Instance
	adapter  *wgpu.Adapter
	device   *wgpu.Device
	queue    *wgpu.Queue
	surface  *wgpu.Surface

	// config 保存 surface 的当前配置，其中的 Format 就是后续建管线要用的
	// 颜色目标格式；窗口尺寸变化时改这里的 Width/Height 再 Configure 一次即可。
	config *wgpu.SurfaceConfiguration
}

// NewProbe 依次创建 instance → surface(handle) → adapter → device → 配置 surface。
//
// handle 是平台相关的窗口句柄：macOS 上是 NSWindow 指针。
// width/height 应当是帧缓冲（像素）尺寸，Retina 屏上它是窗口逻辑尺寸的两倍。
func NewProbe(handle uintptr, width, height uint32) (_ *Probe, err error) {
	// 打开绑定的日志输出，这样 wgpu 的验证层告警会直接打到 stderr——
	// M0 的验收标准之一就是"终端无验证层报错"，没有这一行就无从观察。
	wgpu.SetLogLevel(wgpu.LogLevelWarn)

	p := &Probe{}
	defer func() {
		// 中途失败时把已经建出来的对象释放掉，避免半成品泄漏。
		// 注意这里闭包捕获的是局部变量 p，而不是返回值——出错路径上返回的是 nil，
		// 靠返回值是清理不掉已创建对象的。
		if err != nil {
			p.Close()
		}
	}()

	p.instance = wgpu.CreateInstance(nil)

	// handle 是从窗口库拿到的 NSWindow 指针，它指向 Objective-C 运行时管理的对象，
	// 不受 Go GC 管辖，所以以整数形式跨过 cgo 边界是安全的。
	metalLayer := C.metalLayerFromNSWindow(C.uintptr_t(handle))
	p.surface = p.instance.CreateSurface(&wgpu.SurfaceDescriptor{
		Label:      "gfxspike surface",
		MetalLayer: &wgpu.SurfaceSourceMetalLayer{Layer: metalLayer},
	})

	p.adapter, err = p.instance.RequestAdapter(&wgpu.RequestAdapterOptions{
		CompatibleSurface: p.surface,
		PowerPreference:   wgpu.PowerPreferenceHighPerformance,
	})
	if err != nil {
		return nil, fmt.Errorf("请求 adapter 失败: %w", err)
	}

	p.device, err = p.adapter.RequestDevice(&wgpu.DeviceDescriptor{Label: "gfxspike device"})
	if err != nil {
		return nil, fmt.Errorf("请求 device 失败: %w", err)
	}
	p.queue = p.device.GetQueue()

	caps := p.surface.GetCapabilities(p.adapter)
	if len(caps.Formats) == 0 || len(caps.AlphaModes) == 0 {
		return nil, fmt.Errorf("surface 没有报告任何可用格式或 alpha 模式")
	}

	p.config = &wgpu.SurfaceConfiguration{
		Usage:       wgpu.TextureUsageRenderAttachment,
		Format:      caps.Formats[0],
		Width:       width,
		Height:      height,
		PresentMode: wgpu.PresentModeFifo, // M0 先用 VSync；Task 17 的基准测试要换成非 VSync
		AlphaMode:   caps.AlphaModes[0],
	}
	p.surface.Configure(p.device, p.config)

	// 把实测到的后端与能力打出来——这是 M0 的验证证据，也是 Task 17 选 present mode 的依据。
	info := p.adapter.GetInfo()
	log.Printf("gfx: 后端=%v 适配器=%q 类型=%v", info.BackendType, info.Device, info.AdapterType)
	log.Printf("gfx: surface 格式=%v present 模式=%v", caps.Formats, caps.PresentModes)

	return p, nil
}

// Frame 取 surface 当前纹理，开一个 render pass，
// LoadOp=Clear、ClearValue={0.1,0.2,0.3,1.0}、StoreOp=Store，
// 不画任何东西直接结束 pass，然后提交并 Present。
func (p *Probe) Frame() error {
	surfaceTexture, err := p.surface.TryGetCurrentTexture()
	if err != nil {
		return fmt.Errorf("获取 surface 纹理失败: %w", err)
	}

	texture, ok := surfaceTexture.Get()
	if !ok {
		// 窗口被遮挡、最小化或 surface 已过期，跳过这一帧即可。
		return nil
	}

	view, err := texture.TryCreateView(nil)
	if err != nil {
		texture.Release()
		return fmt.Errorf("创建纹理视图失败: %w", err)
	}
	defer view.Release()

	encoder, err := p.device.TryCreateCommandEncoder(&wgpu.CommandEncoderDescriptor{
		Label: "gfxspike encoder",
	})
	if err != nil {
		texture.Release()
		return fmt.Errorf("创建命令编码器失败: %w", err)
	}
	defer encoder.Release()

	pass, err := encoder.TryBeginRenderPass(&wgpu.RenderPassDescriptor{
		Label: "clear pass",
		ColorAttachments: []wgpu.RenderPassColorAttachment{{
			View:       view,
			LoadOp:     wgpu.LoadOpClear,
			StoreOp:    wgpu.StoreOpStore,
			ClearValue: wgpu.Color{R: 0.1, G: 0.2, B: 0.3, A: 1.0},
		}},
	})
	if err != nil {
		texture.Release()
		return fmt.Errorf("开启 render pass 失败: %w", err)
	}
	if err := pass.TryEnd(); err != nil {
		texture.Release()
		return fmt.Errorf("结束 render pass 失败: %w", err)
	}

	cmdBuffer, err := encoder.TryFinish(nil)
	if err != nil {
		texture.Release()
		return fmt.Errorf("生成命令缓冲失败: %w", err)
	}
	defer cmdBuffer.Release()

	p.queue.Submit(cmdBuffer)
	p.surface.Present()

	// wgpu-native 要求先 Present 再释放 surface 纹理；提前释放会触发验证层报错。
	texture.Release()

	return nil
}

// Close 释放 surface 与设备。可以在半初始化的 Probe 上安全调用。
func (p *Probe) Close() {
	if p == nil {
		return
	}
	// 释放顺序与创建顺序相反。绑定虽然带 GC 兜底，但 GPU 资源的实际占用
	// Go GC 是看不见的，显式释放仍然是本工程的规范做法。
	if p.queue != nil {
		p.queue.Release()
		p.queue = nil
	}
	if p.device != nil {
		p.device.Release()
		p.device = nil
	}
	if p.adapter != nil {
		p.adapter.Release()
		p.adapter = nil
	}
	if p.surface != nil {
		p.surface.Release()
		p.surface = nil
	}
	if p.instance != nil {
		p.instance.Release()
		p.instance = nil
	}
	p.config = nil
}
