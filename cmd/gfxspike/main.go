//go:build darwin

// Command gfxspike 是 M0 技术验证程序。
// 它验证完整的 compute → indirect draw 链路：compute shader 从 128 个候选中
// 筛出 64 个偶数编号的实例，并把实例数直接写进 indirect 参数缓冲；CPU 不读取
// 实例数，render pass 直接按 GPU 生成的参数绘制。
//
// 注意本文件（以及将来的 internal/render）都不 import WebGPU 绑定——
// 所有 GPU 调用都经过 internal/gfx 的接口。
//
// M0 只验证 macOS/Metal 这一条最不确定的路径，所以本文件带 darwin 构建约束；
// 跨平台窗口句柄分支留到需要时再加。
package main

import (
	"encoding/binary"
	"log"
	"math"
	"runtime"
	"unsafe"

	"github.com/go-gl/glfw/v3.3/glfw"

	"minecraft-go/internal/gfx"
)

const candidates = 128

func init() {
	// GLFW 与图形 API 都要求所有调用发生在同一个 OS 线程上。
	runtime.LockOSThread()
}

func main() {
	if err := glfw.Init(); err != nil {
		log.Fatalf("glfw 初始化失败: %v", err)
	}
	defer glfw.Terminate()

	// 不创建任何 OpenGL 上下文——渲染交给 WebGPU。
	glfw.WindowHint(glfw.ClientAPI, glfw.NoAPI)
	win, err := glfw.CreateWindow(1280, 720, "gfxspike — GPU-driven indirect draw", nil, nil)
	if err != nil {
		log.Fatalf("创建窗口失败: %v", err)
	}
	defer win.Destroy()

	// surface 要按帧缓冲（像素）尺寸配置，Retina 屏上它是窗口逻辑尺寸的两倍。
	fbWidth, fbHeight := win.GetFramebufferSize()

	device, surface, err := gfx.NewDevice(cocoaHandle(win), uint32(fbWidth), uint32(fbHeight))
	if err != nil {
		log.Fatalf("创建 GPU 设备失败: %v", err)
	}
	defer device.Release()
	defer surface.Release()

	renderer := newSpikeRenderer(device, surface.Format(), uint32(fbWidth), uint32(fbHeight))
	defer renderer.Release()

	for !win.ShouldClose() {
		glfw.PollEvents()

		// 窗口尺寸可能被拖动改过；Resize 内部会跳过没有变化的调用。
		w, h := win.GetFramebufferSize()
		surface.Resize(uint32(w), uint32(h))

		renderer.Frame(device, surface)
	}
}

// spikeRenderer 持有 M0 验证链路所需的全部 GPU 资源。
type spikeRenderer struct {
	cullPipeline gfx.ComputePipeline
	drawPipeline gfx.RenderPipeline
	cullGroup    gfx.BindGroup
	drawGroup    gfx.BindGroup
	args         gfx.Buffer
	visible      gfx.Buffer
	index        gfx.Buffer
	camera       gfx.Buffer
}

func newSpikeRenderer(device gfx.Device, colorFormat gfx.TextureFormat, width, height uint32) *spikeRenderer {
	r := &spikeRenderer{}

	// DrawIndexedIndirect 参数：
	// indexCount, instanceCount, firstIndex, baseVertex, firstInstance。
	// instanceCount 每帧先清零，再完全由 compute shader 填写。
	r.args = device.CreateBuffer(gfx.BufferDesc{
		Label: "spike indirect args",
		Size:  5 * 4,
		Usage: gfx.BufferUsageIndirect | gfx.BufferUsageStorage |
			gfx.BufferUsageCopyDst | gfx.BufferUsageCopySrc,
	})
	r.args.Write(0, uint32Bytes(6, 0, 0, 0, 0))

	r.visible = device.CreateBuffer(gfx.BufferDesc{
		Label: "spike visible instances",
		Size:  candidates * 4,
		Usage: gfx.BufferUsageStorage,
	})

	r.index = device.CreateBuffer(gfx.BufferDesc{
		Label: "spike quad indices",
		Size:  6 * 4,
		Usage: gfx.BufferUsageIndex | gfx.BufferUsageCopyDst,
	})
	r.index.Write(0, uint32Bytes(0, 1, 2, 0, 2, 3))

	r.camera = device.CreateBuffer(gfx.BufferDesc{
		Label: "spike camera",
		Size:  16 * 4,
		Usage: gfx.BufferUsageUniform | gfx.BufferUsageCopyDst,
	})
	// 正交矩阵把 X=0..192 映射到屏幕宽度。Y 缩放按初始宽高比校正，
	// 让方块在默认 1280×720 窗口中保持接近正方形。
	xScale := float32(2.0 / 192.0)
	yScale := xScale
	if height != 0 {
		yScale *= float32(width) / float32(height)
	}
	r.camera.Write(0, float32Bytes(
		xScale, 0, 0, 0,
		0, yScale, 0, 0,
		0, 0, 1, 0,
		-0.99, -yScale/2, 0, 1,
	))

	cullModule := device.CreateShaderModule(gfx.ShaderSpikeCull)
	defer cullModule.Release()
	cullLayout := gfx.BindGroupLayout{
		Label: "spike cull layout",
		Entries: []gfx.BindGroupLayoutEntry{
			{Binding: 0, Type: gfx.BindingStorageBufferRW, VisibleIn: gfx.StageCompute},
			{Binding: 1, Type: gfx.BindingStorageBufferRW, VisibleIn: gfx.StageCompute},
		},
	}
	r.cullPipeline = device.CreateComputePipeline(gfx.ComputePipelineDesc{
		Label:      "spike cull",
		Shader:     cullModule,
		Entry:      "cs_main",
		BindGroups: []gfx.BindGroupLayout{cullLayout},
	})
	r.cullGroup = device.CreateBindGroup(gfx.BindGroupDesc{
		Label:  "spike cull group",
		Layout: cullLayout,
		Entries: []gfx.BindGroupEntry{
			{Binding: 0, Buffer: r.args},
			{Binding: 1, Buffer: r.visible},
		},
	})

	drawModule := device.CreateShaderModule(gfx.ShaderSpikeDraw)
	defer drawModule.Release()
	drawLayout := gfx.BindGroupLayout{
		Label: "spike draw layout",
		Entries: []gfx.BindGroupLayoutEntry{
			{Binding: 0, Type: gfx.BindingUniformBuffer, VisibleIn: gfx.StageVertex},
			{Binding: 1, Type: gfx.BindingStorageBufferRO, VisibleIn: gfx.StageVertex},
		},
	}
	r.drawPipeline = device.CreateRenderPipeline(gfx.RenderPipelineDesc{
		Label:         "spike draw",
		Shader:        drawModule,
		VertexEntry:   "vs_main",
		FragmentEntry: "fs_main",
		BindGroups:    []gfx.BindGroupLayout{drawLayout},
		ColorFormat:   colorFormat,
	})
	r.drawGroup = device.CreateBindGroup(gfx.BindGroupDesc{
		Label:  "spike draw group",
		Layout: drawLayout,
		Entries: []gfx.BindGroupEntry{
			{Binding: 0, Buffer: r.camera},
			{Binding: 1, Buffer: r.visible},
		},
	})

	return r
}

// Frame 先由 compute shader 筛选实例并填 indirect 参数，再直接间接绘制。
func (r *spikeRenderer) Frame(device gfx.Device, surface gfx.Surface) {
	view := surface.Acquire()
	if view == nil {
		// 这一帧取不到 surface 纹理（窗口被遮挡/最小化），跳过。
		return
	}

	// CPU 只负责把计数器清零，不读取也不知道本帧最终有多少个实例。
	r.args.Write(4, uint32Bytes(0))

	encoder := device.CreateCommandEncoder()
	compute := encoder.BeginComputePass("spike cull pass")
	compute.SetPipeline(r.cullPipeline)
	compute.SetBindGroup(0, r.cullGroup)
	compute.Dispatch(candidates/64, 1, 1)
	compute.End()

	render := encoder.BeginRenderPass(gfx.RenderPassDesc{
		Label:      "spike draw pass",
		ColorView:  view,
		ClearColor: [4]float32{0.1, 0.2, 0.3, 1.0},
		LoadClear:  true,
	})
	render.SetPipeline(r.drawPipeline)
	render.SetBindGroup(0, r.drawGroup)
	render.SetIndexBuffer(r.index, 0)
	render.DrawIndexedIndirect(r.args, 0)
	render.End()

	cmd := encoder.Finish()
	device.Submit(cmd)
	cmd.Release()

	surface.Present()
}

func (r *spikeRenderer) Release() {
	// 先释放引用资源的 bind group / pipeline，再释放底层缓冲。
	if r.drawGroup != nil {
		r.drawGroup.Release()
	}
	if r.cullGroup != nil {
		r.cullGroup.Release()
	}
	if r.drawPipeline != nil {
		r.drawPipeline.Release()
	}
	if r.cullPipeline != nil {
		r.cullPipeline.Release()
	}
	if r.camera != nil {
		r.camera.Release()
	}
	if r.index != nil {
		r.index.Release()
	}
	if r.visible != nil {
		r.visible.Release()
	}
	if r.args != nil {
		r.args.Release()
	}
}

func uint32Bytes(values ...uint32) []byte {
	out := make([]byte, len(values)*4)
	for i, value := range values {
		binary.LittleEndian.PutUint32(out[i*4:], value)
	}
	return out
}

func float32Bytes(values ...float32) []byte {
	out := make([]byte, len(values)*4)
	for i, value := range values {
		binary.LittleEndian.PutUint32(out[i*4:], math.Float32bits(value))
	}
	return out
}

// cocoaHandle 把 GLFW 窗口的 NSWindow 指针转成与窗口库无关的句柄，
// 好让 internal/gfx 不必知道 GLFW 的存在。
// go-gl/glfw v3.3 的 GetCocoaWindow 返回 unsafe.Pointer。
func cocoaHandle(win *glfw.Window) gfx.NativeWindowHandle {
	return gfx.NativeWindowHandle{
		Kind:    gfx.HandleKindNSWindow,
		Pointer: uintptr(unsafe.Pointer(win.GetCocoaWindow())),
	}
}
