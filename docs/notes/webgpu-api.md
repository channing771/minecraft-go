# WebGPU 绑定 API 速查

- 绑定：`github.com/oliverbestmann/webgpu` **v1.34.2**（import 路径是子包 `github.com/oliverbestmann/webgpu/wgpu`）
- wgpu-native：v29.0.0.0（见绑定 README「Current upstream version」）
- 验证日期：2026-07-27
- 验证平台：darwin/arm64，Metal 后端，Apple M5（integrated-gpu）

> 本文所有签名都是从模块缓存里的源码实地抄回并用 `go doc` 复核的，
> 缓存路径：`$(go env GOMODCACHE)/github.com/oliverbestmann/webgpu@v1.34.2`。
> 未做替换：备选绑定 `github.com/go-webgpu/webgpu` 没有用上，本绑定拉取和运行都正常。

## 全局约定：panic 版 vs Try 版

绑定对大多数方法提供两个版本：

- `X(...)` —— 出错直接 panic（绑定作者认为验证错误属于「程序员错误」）；
- `TryX(...) (..., error)` —— 返回 error，不 panic。

panic 版集中生成在 `wgpu/gen_wrappers.go`，是对 `TryX` 的薄封装。
**本工程一律使用 `TryX` 版本**，让错误沿调用栈显式传播。

例外：少数方法只有一个版本，没有 Try 变体，出错就是 panic 或返回零值：
`CreateInstance`、`Instance.CreateSurface`、`Instance.RequestAdapter`（本来就返回 error）、
`Adapter.RequestDevice`（本来就返回 error）、`Surface.Configure`、`Surface.Present`、
`Surface.GetCapabilities`、`Queue.Submit`、以及 RenderPass/ComputePass 上所有的
`SetPipeline` / `SetBindGroup` / `Draw*` / `Dispatch*`。

打开日志（验证层告警会打到 stderr，M0 的验收要看这个）：

```go
func SetLogLevel(level LogLevel)   // LogLevelOff/Error/Warn/Info/Debug/Trace
```

## 实例与设备

创建流程是**全同步**的：`RequestAdapter` / `RequestDevice` 内部虽然走 wgpu-native 的
回调，但回调在调用内即时触发，函数直接返回结果，**不需要 Future/轮询**。

```go
func CreateInstance(descriptor *InstanceDescriptor) *Instance   // 传 nil 即用默认值，无 error

type InstanceDescriptor struct {
	Backends           InstanceBackend
	Dx12ShaderCompiler Dx12Compiler
	DxcPath            string
}

func (g *Instance) RequestAdapter(options *RequestAdapterOptions) (*Adapter, error)

type RequestAdapterOptions struct {
	CompatibleSurface    *Surface   // 必须先建 surface 再请求 adapter
	PowerPreference      PowerPreference
	ForceFallbackAdapter bool
	BackendType          BackendType
}

func (g *Adapter) RequestDevice(descriptor *DeviceDescriptor) (*Device, error)  // 传 nil 也可以

type DeviceDescriptor struct {
	Label              string
	RequiredFeatures   []FeatureName
	RequiredLimits     *Limits
	DeviceLostCallback DeviceLostCallback
	TracePath          string
}

func (g *Device) GetQueue() *Queue
func (g *Adapter) GetInfo() AdapterInfo   // Vendor/Architecture/Device/Description/AdapterType/BackendType/...
func (g *Adapter) GetLimits() Limits
func (g *Device) GetLimits() Limits
func (g *Device) HasFeature(feature FeatureName) bool
func (g *Device) Poll(wait bool, submissionIndex *uint64) (queueEmpty bool)
```

**顺序**：instance → surface → adapter(CompatibleSurface=surface) → device → queue。

## Surface（macOS/Metal）

绑定用一个「联合体式」的描述符表达平台差异：填哪个字段就用哪个平台的路径。

```go
type SurfaceDescriptor struct {
	Label string

	WindowsHWND         *SurfaceSourceWindowsHWND
	XcbWindow           *SurfaceSourceXcbWindow
	XlibWindow          *SurfaceSourceXlibWindow
	MetalLayer          *SurfaceSourceMetalLayer   // ← macOS 走这个
	WaylandSurface      *SurfaceSourceWaylandSurface
	AndroidNativeWindow *SurfaceSourceAndroidNativeWindow
}

type SurfaceSourceMetalLayer struct {
	Layer unsafe.Pointer   // CAMetalLayer 指针，不是 NSWindow！
}

func (g *Instance) CreateSurface(descriptor *SurfaceDescriptor) *Surface   // 无 error，失败即 panic
```

**macOS 的关键一步**：`Layer` 要的是 `CAMetalLayer`，而 GLFW 只给得出 `NSWindow`。
必须自己写一小段 Objective-C 把 layer 挂到 `contentView` 上：

```objc
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa -framework QuartzCore
#import <Cocoa/Cocoa.h>
#import <QuartzCore/CAMetalLayer.h>

static void *metalLayerFromNSWindow(uintptr_t nsWindowRef) {
	NSWindow *nsWindow = (__bridge NSWindow *)(void *)nsWindowRef;
	id metalLayer = NULL;
	[nsWindow.contentView setWantsLayer:YES];
	metalLayer = [CAMetalLayer layer];
	[nsWindow.contentView setLayer:metalLayer];
	return metalLayer;
}
```

绑定自带 `wgpuglfw.GetSurfaceDescriptor(*glfw.Window)` 做同样的事，
**但它 import 了 `go-gl/glfw/v3.4`**，与本工程「gfx 不依赖窗口库」的约束冲突，
且本工程用的是 glfw v3.3，所以我们在 `internal/gfx/probe.go` 里自己重写了这段。

### 配置与取帧

```go
func (g *Surface) GetCapabilities(adapter *Adapter) (ret SurfaceCapabilities)

type SurfaceCapabilities struct {
	Formats      []TextureFormat
	PresentModes []PresentMode
	AlphaModes   []CompositeAlphaMode
}

type SurfaceConfiguration struct {
	Usage                      TextureUsage        // 必填：TextureUsageRenderAttachment
	Format                     TextureFormat       // 必填：一般取 caps.Formats[0]
	Width                      uint32              // 必填：帧缓冲像素宽（Retina 是逻辑宽的 2 倍）
	Height                     uint32              // 必填
	PresentMode                PresentMode         // 必填
	AlphaMode                  CompositeAlphaMode  // 必填：一般取 caps.AlphaModes[0]
	ViewFormats                []TextureFormat     // 可选
	DesiredMaximumFrameLatency uint32              // 可选
}

func (g *Surface) Configure(device *Device, config *SurfaceConfiguration)   // 无返回值
func (g *Surface) TryGetCurrentTexture() (SurfaceTexture, error)
func (g *Surface) GetCurrentTexture() SurfaceTexture                        // panic 版
func (g *Surface) Present()

type SurfaceTexture struct {
	Texture *Texture
	Status  SurfaceGetCurrentTextureStatus
}
func (s *SurfaceTexture) Get() (nextTexture *Texture, success bool)   // 拿不到（遮挡/超时/过期）时 success=false
func (s *SurfaceTexture) IsStatusSuccess() bool
```

**窗口尺寸变化**：改 `config.Width/Height` 后重新 `Configure` 一次即可，
不需要重建 surface。

### present mode（VSync 开关）—— Task 17 依赖

绑定**没有**「自动 VSync / 自动非 VSync」的封装，必须自己从 caps 里挑：

```go
const PresentModeUndefined   PresentMode = 0x00000000
const PresentModeFifo        PresentMode = 0x00000001  // VSync（WebGPU 规范保证一定可用）
const PresentModeFifoRelaxed PresentMode = 0x00000002
const PresentModeImmediate   PresentMode = 0x00000003  // 非 VSync
const PresentModeMailbox     PresentMode = 0x00000004
```

**macOS/Metal 实测上报**：`[fifo immediate]` —— 没有 mailbox。
所以 Task 17 关 VSync 就用 `PresentModeImmediate`，
但仍应先检查 `caps.PresentModes` 里有没有它，没有就退回 `PresentModeFifo`。

**macOS/Metal 实测格式**：`[bgra8unorm-srgb bgra8unorm rgba16float rgb10a2unorm]`。
`caps.Formats[0]` 是 **sRGB** 格式，这意味着 shader 写出的（和 ClearValue 里的）
是**线性**值，硬件会自动做 sRGB 编码。所以清屏色 `(0.1, 0.2, 0.3)` 在屏幕上
量出来是 `#607B93` 这样偏亮的蓝灰，而不是 `#1A334D`。**这是正确行为，不是 bug。**
后续做光照时按线性空间算颜色即可，不要再手工做 gamma 校正。

## 缓冲与着色器

```go
type BufferDescriptor struct {
	Label            string
	Usage            BufferUsage
	Size             uint64
	MappedAtCreation bool
}
func (g *Device) TryCreateBuffer(descriptor *BufferDescriptor) (*Buffer, error)

// 便利函数：建 buffer 的同时写入初值
type BufferInitDescriptor struct {
	Label    string
	Contents []byte
	Usage    BufferUsage
}
func (g *Device) CreateBufferInit(descriptor *BufferInitDescriptor) *Buffer   // 只有 panic 版

func (p *Queue) TryWriteBuffer(buffer *Buffer, bufferOffset uint64, data []byte) error
func (p *Buffer) TryMapAsync(mode MapMode, offset, size uint64, callback BufferMapCallback) error
func (p *Buffer) GetMappedRange(offset, size uint) []byte
func (p *Buffer) TryUnmap() error
func (p *Buffer) Destroy()

// []T <-> []byte 的零拷贝转换，写 GPU 结构体时很有用
func ToBytes[E any](src []E) []byte
func FromBytes[E any](src []byte) []E
```

BufferUsage 位掩码（`BufferUsage` 底层是 uint64，用 `|` 组合）：
`BufferUsageMapRead(0x1)` `MapWrite(0x2)` `CopySrc(0x4)` `CopyDst(0x8)` `Index(0x10)`
`Vertex(0x20)` `Uniform(0x40)` `Storage(0x80)` **`Indirect(0x100)`** `QueryResolve(0x200)`

```go
type ShaderModuleDescriptor struct {
	Label       string
	SPIRVSource *ShaderSourceSPIRV
	WGSLSource  *ShaderSourceWGSL    // ← 我们用这个
	GLSLSource  *ShaderSourceGLSL
}
type ShaderSourceWGSL struct { Code string }
func (g *Device) TryCreateShaderModule(descriptor *ShaderModuleDescriptor) (*ShaderModule, error)
```

## 管线与 bind group

```go
type RenderPipelineDescriptor struct {
	Label        string
	Layout       *PipelineLayout    // nil = auto layout
	Vertex       VertexState
	Primitive    PrimitiveState
	DepthStencil *DepthStencilState
	Multisample  MultisampleState
	Fragment     *FragmentState
}
type VertexState struct {
	Module     *ShaderModule
	EntryPoint string
	Buffers    []VertexBufferLayout
	// （另有 Constants 等，见 device.go:448）
}
type FragmentState struct {
	Module     *ShaderModule
	EntryPoint string
	Targets    []ColorTargetState
}
type ColorTargetState struct {
	Format    TextureFormat
	Blend     *BlendState     // 可用现成的 &wgpu.BlendStateReplace
	WriteMask ColorWriteMask  // wgpu.ColorWriteMaskAll
}
type PrimitiveState struct {
	Topology         PrimitiveTopology
	StripIndexFormat IndexFormat
	FrontFace        FrontFace
	CullMode         CullMode
}
type MultisampleState struct {
	Count                  uint32   // 不开 MSAA 时必须填 1，填 0 会挂
	Mask                   uint32   // 不开 MSAA 时填 0xFFFFFFFF
	AlphaToCoverageEnabled bool
}
func (g *Device) TryCreateRenderPipeline(descriptor *RenderPipelineDescriptor) (*RenderPipeline, error)

type ComputePipelineDescriptor struct {
	Label   string
	Layout  *PipelineLayout
	Compute ProgrammableStageDescriptor
}
type ProgrammableStageDescriptor struct {
	Module     *ShaderModule
	EntryPoint string
}
func (g *Device) TryCreateComputePipeline(descriptor *ComputePipelineDescriptor) (*ComputePipeline, error)

type BindGroupLayoutDescriptor struct {
	Label   string
	Entries []BindGroupLayoutEntry
}
type BindGroupLayoutEntry struct {
	Binding        uint32
	Visibility     ShaderStage
	Buffer         BufferBindingLayout
	Sampler        SamplerBindingLayout
	Texture        TextureBindingLayout
	StorageTexture StorageTextureBindingLayout
}
type BufferBindingLayout struct {
	Type             BufferBindingType   // Uniform / Storage / ReadOnlyStorage
	HasDynamicOffset bool
	MinBindingSize   uint64
}
func (g *Device) TryCreateBindGroupLayout(descriptor *BindGroupLayoutDescriptor) (*BindGroupLayout, error)

type BindGroupDescriptor struct {
	Label   string
	Layout  *BindGroupLayout
	Entries []BindGroupEntry
}
type BindGroupEntry struct {
	Binding     uint32
	Buffer      *Buffer
	Offset      uint64
	Size        uint64
	Sampler     *Sampler
	TextureView *TextureView
}
func (g *Device) TryCreateBindGroup(descriptor *BindGroupDescriptor) (*BindGroup, error)

type PipelineLayoutDescriptor struct {
	Label            string
	BindGroupLayouts []*BindGroupLayout
}
func (g *Device) TryCreatePipelineLayout(descriptor *PipelineLayoutDescriptor) (*PipelineLayout, error)
```

## 命令编码：CommandEncoder → RenderPass / ComputePass

```go
type CommandEncoderDescriptor struct { Label string }
func (g *Device) TryCreateCommandEncoder(descriptor *CommandEncoderDescriptor) (*CommandEncoder, error)

type RenderPassDescriptor struct {
	Label                  string
	ColorAttachments       []RenderPassColorAttachment
	DepthStencilAttachment *RenderPassDepthStencilAttachment
}
type RenderPassColorAttachment struct {
	View          *TextureView
	ResolveTarget *TextureView
	LoadOp        LoadOp     // LoadOpClear / LoadOpLoad
	StoreOp       StoreOp    // StoreOpStore / StoreOpDiscard
	ClearValue    Color      // Color{R,G,B,A float64}
}
type RenderPassDepthStencilAttachment struct {
	View              *TextureView
	DepthLoadOp       LoadOp
	DepthStoreOp      StoreOp
	DepthClearValue   float32
	DepthReadOnly     bool
	StencilLoadOp     LoadOp
	StencilStoreOp    StoreOp
	StencilClearValue uint32
	StencilReadOnly   bool
}
func (p *CommandEncoder) TryBeginRenderPass(descriptor *RenderPassDescriptor) (*RenderPassEncoder, error)

type ComputePassDescriptor struct { Label string }
func (p *CommandEncoder) BeginComputePass(descriptor *ComputePassDescriptor) *ComputePassEncoder  // 只有 panic 版

func (p *CommandEncoder) TryFinish(descriptor *CommandBufferDescriptor) (*CommandBuffer, error)   // 传 nil 可以
func (p *CommandEncoder) TryCopyBufferToBuffer(src *Buffer, srcOffset uint64, dst *Buffer, dstOffset uint64, size uint64) error
func (p *CommandEncoder) TryClearBuffer(buffer *Buffer, offset uint64, size uint64) error

func (p *Queue) Submit(commands ...*CommandBuffer) (submissionIndex SubmissionIndex)
```

RenderPassEncoder 常用方法：

```go
func (p *RenderPassEncoder) SetPipeline(pipeline *RenderPipeline)
func (p *RenderPassEncoder) SetBindGroup(groupIndex uint32, group *BindGroup, dynamicOffsets []uint32)
func (p *RenderPassEncoder) SetVertexBuffer(slot uint32, buffer *Buffer, offset uint64, size uint64)
func (p *RenderPassEncoder) SetIndexBuffer(buffer *Buffer, format IndexFormat, offset uint64, size uint64)
func (p *RenderPassEncoder) SetViewport(x, y, width, height, minDepth, maxDepth float32)
func (p *RenderPassEncoder) Draw(vertexCount, instanceCount, firstVertex, firstInstance uint32)
func (p *RenderPassEncoder) DrawIndexed(indexCount, instanceCount, firstIndex uint32, baseVertex int32, firstInstance uint32)
func (p *RenderPassEncoder) TryEnd() error      // panic 版是 End()
```

ComputePassEncoder 常用方法：

```go
func (p *ComputePassEncoder) SetPipeline(pipeline *ComputePipeline)
func (p *ComputePassEncoder) SetBindGroup(groupIndex uint32, group *BindGroup, dynamicOffsets []uint32)
func (p *ComputePassEncoder) DispatchWorkgroups(x, y, z uint32)
func (p *ComputePassEncoder) DispatchWorkgroupsIndirect(indirectBuffer *Buffer, indirectOffset uint64)
func (p *ComputePassEncoder) TryEnd() error     // panic 版是 End()
```

## 间接绘制

这是 M0 要验证的核心链路。**确切签名**（`wgpu/render_pass_encoder.go:40,44`）：

```go
func (p *RenderPassEncoder) DrawIndirect(indirectBuffer *Buffer, indirectOffset uint64)
func (p *RenderPassEncoder) DrawIndexedIndirect(indirectBuffer *Buffer, indirectOffset uint64)
```

- 只有 buffer + offset 两个参数，**没有 error 返回，也没有 Try 变体**。
- indirect buffer 必须带 `BufferUsageIndirect`；要让 compute shader 写它，
  还得同时带 `BufferUsageStorage`。
- `DrawIndexedIndirect` 读的结构是 5 个 u32（WebGPU 规范固定布局）：
  `indexCount, instanceCount, firstIndex, baseVertex(i32), firstInstance`
  —— 也就是说 **compute shader 只要往 offset+4 处写 instanceCount 就能决定实例数**，
  这正是本项目要验证的那条链路。
- `DrawIndirect` 读的是 4 个 u32：`vertexCount, instanceCount, firstVertex, firstInstance`。
- `firstInstance != 0` 需要 `FeatureNameIndirectFirstInstance`（0x0000000A）；用 0 就不需要。

**⚠️ MultiDraw 系列签名可疑，暂勿使用**：

```go
func (p *RenderPassEncoder) MultiDrawIndirect(encoder *RenderPassEncoder, buffer Buffer, offset uint64, count uint32)
func (p *RenderPassEncoder) MultiDrawIndexedIndirect(encoder *RenderPassEncoder, buffer Buffer, offset uint64, count uint32)
func (p *RenderPassEncoder) MultiDrawIndirectCount(...)
func (p *RenderPassEncoder) MultiDrawIndexedIndirectCount(...)
```

它们既有接收者 `p` 又多带一个 `encoder *RenderPassEncoder` 参数，而且 buffer 是**按值**
传的（`buffer Buffer` 而非 `*Buffer`）——看起来是绑定的代码生成缺陷。
如果后续真要用 multi-draw（`NativeFeatureMultiDrawIndirect` / `NativeFeatureMultiDrawIndirectCount`），
必须先单独验证这几个函数。M0 的方案只用单次 `DrawIndexedIndirect`，不受影响。

## 资源释放语义

绑定 README 与 `wgpu/doc.go` 的说法，加上实地确认：

- 每个 wgpu 对象都有 `Release()`；**GC 会兜底**——对象被回收时自动释放，
  所以漏掉 `Release()` 不会立刻泄漏，但 Go GC 看不见 GPU 侧的真实占用
  （它只看到 Go 端那几十字节的壳），大纹理/大 buffer 堆积仍会撑爆显存。
- **结论：本工程仍然手动 `Release()`**，GC 只当保险。
- `Release()` **可以重复调用**，第二次是 no-op（doc.go 明确写了）。
  所以 `Close()` 里可以无脑释放，不必先判断是否已释放。
- `wgpu.Share(obj)` 可以把对象标记为「共享」：此后 `Release()` 变成 no-op，
  只由 GC 回收。用于缓存/对外分发对象的场景。
- **每帧的 surface 纹理**：正确顺序是 `Submit → Present → texture.Release()`。
  在 `Present()` 之前释放会触发验证层报错。绑定自带的 triangle example 干脆不释放、
  全交给 GC，我们选择显式释放，实测无告警。
- `Buffer.Destroy()` / `Texture.Destroy()` 是另一回事（立即释放 GPU 内存），
  与 `Release()`（释放句柄引用）语义不同。

## 已实测确认（M0 / Task 1）

在 `cmd/gfxspike` 里跑通：CreateInstance → CAMetalLayer surface → RequestAdapter →
RequestDevice → Configure → 每帧 `TryGetCurrentTexture` → clear render pass →
`Submit` → `Present`。

- 后端：`metal`，适配器 `"Apple M5"`（integrated-gpu）
- surface 格式：`[bgra8unorm-srgb bgra8unorm rgba16float rgb10a2unorm]`
- present 模式：`[fifo immediate]`
- 全程无 wgpu 验证层告警（已 `SetLogLevel(LogLevelWarn)`），关窗退出码 0
