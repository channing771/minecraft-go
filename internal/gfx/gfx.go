//go:build darwin

// Package gfx 把 GPU 后端封装在一组最小接口之后。
//
// 这是整个仓库中唯一允许 import WebGPU 绑定的包。上层（internal/render）
// 只依赖本包的接口，因此更换底层绑定时 render/ 无需改动任何一行。
//
// 范围：只封装本工程实际用到的 WebGPU 子集，不做通用渲染硬件接口。
//
// 错误处理约定：除 NewDevice 与 Surface.SetPresentMode 外，本包所有方法都不返回
// error。资源创建失败（管线编译不过、描述符不合法）在本工程视为程序员错误，
// 直接 panic —— 这与底层绑定的取向一致，也避免让每个调用点都背上无从恢复的
// error 分支。运行期的瞬时失败（取不到 surface 纹理）走"返回 nil、跳过本帧"。
//
// 平台：M0 只验证 macOS/Metal 这一条最不确定的路径，因此本包整体带 darwin 构建
// 约束。跨平台分支（Windows/HWND、Linux/X11-Wayland）留到真正需要时再加。
package gfx

// BufferUsage 是缓冲用途位掩码。
type BufferUsage uint32

const (
	BufferUsageVertex BufferUsage = 1 << iota
	BufferUsageIndex
	BufferUsageUniform
	BufferUsageStorage
	BufferUsageIndirect
	BufferUsageCopySrc
	BufferUsageCopyDst
	BufferUsageMapRead
)

// BufferDesc 描述一个待创建的缓冲。
type BufferDesc struct {
	Label string
	Size  uint64
	Usage BufferUsage
	// MappedAtCreation 为 true 时，创建后立即可写，用于一次性上传初始数据。
	MappedAtCreation bool
}

// Buffer 是一块 GPU 内存。
type Buffer interface {
	Size() uint64
	// Write 把数据写入缓冲的 offset 处。要求 Usage 含 BufferUsageCopyDst。
	Write(offset uint64, data []byte)
	// ReadBack 同步读回缓冲内容，仅用于测试。要求 Usage 含 BufferUsageCopySrc。
	//
	// 实现必须在内部创建 Usage=MapRead|CopyDst 的 staging buffer，
	// 先 CopyBufferToBuffer，再映射 staging。WebGPU 规定 MapRead 只能与
	// CopyDst 组合，不能把 Storage/Indirect 等工作缓冲直接映射。
	ReadBack() []byte
	Release()
}

// ShaderModule 是一段已编译的 WGSL。
type ShaderModule interface{ Release() }

// VertexFormat 描述顶点属性的数据类型。
type VertexFormat uint8

const (
	VertexFormatUint32x2 VertexFormat = iota
	VertexFormatFloat32x3
)

// VertexAttribute 是一条顶点属性。
type VertexAttribute struct {
	ShaderLocation uint32
	Offset         uint64
	Format         VertexFormat
}

// VertexBufferLayout 描述一个顶点缓冲的布局。
type VertexBufferLayout struct {
	ArrayStride uint64
	// StepModeInstance 为 true 表示每实例前进一次，而非每顶点。
	StepModeInstance bool
	Attributes       []VertexAttribute
}

// RenderPipelineDesc 描述一条渲染管线。
//
// 图元状态目前固定为 triangle-list / 不剔除背面：本阶段还没有需要剔除的用例，
// 等真正需要时再加字段。深度测试固定为 less。
type RenderPipelineDesc struct {
	Label         string
	Shader        ShaderModule
	VertexEntry   string
	FragmentEntry string
	Buffers       []VertexBufferLayout
	BindGroups    []BindGroupLayout
	// DepthWrite 为 false 时只做深度测试不写深度（半透明 pass 用）。
	DepthWrite bool
	// ColorFormat 必须与 surface 配置一致。
	ColorFormat TextureFormat
	// DepthFormat 为 FormatUndefined（零值）时不挂深度附件，此时 DepthWrite 无意义。
	DepthFormat TextureFormat
	// Blend 指定颜色附件的混合方式。零值 BlendReplace 保持现有行为。
	Blend BlendMode
}

// BlendMode 指定颜色附件的混合方式。
type BlendMode uint8

const (
	BlendReplace BlendMode = iota
	BlendAlpha
)

// RenderPipeline 是一条已创建的渲染管线。
type RenderPipeline interface{ Release() }

// ComputePipelineDesc 描述一条计算管线。
type ComputePipelineDesc struct {
	Label      string
	Shader     ShaderModule
	Entry      string
	BindGroups []BindGroupLayout
}

// ComputePipeline 是一条已创建的计算管线。
type ComputePipeline interface{ Release() }

// BindingType 是绑定槽的资源类型。
type BindingType uint8

const (
	BindingUniformBuffer BindingType = iota
	BindingStorageBufferRO
	BindingStorageBufferRW
	BindingSampledTextureFloat
	BindingSampledTextureUnfilterableFloat
	BindingDepthTexture
	BindingStorageTextureWrite
	BindingSampler
)

// BindGroupLayoutEntry 是布局中的一个绑定槽。
type BindGroupLayoutEntry struct {
	Binding   uint32
	Type      BindingType
	VisibleIn ShaderStage
	// StorageFormat 仅 BindingStorageTextureWrite 使用。
	StorageFormat TextureFormat
	// ViewDimension 纹理绑定必须显式声明 2D 或 2D array，不得为 Auto。
	ViewDimension TextureViewDimension
}

// ShaderStage 是着色器阶段位掩码。
type ShaderStage uint32

const (
	StageVertex ShaderStage = 1 << iota
	StageFragment
	StageCompute
)

// BindGroupLayout 描述一组绑定槽。
//
// 它是一份描述而非句柄：后端在建管线 / 建 bind group 时按需实例化底层布局对象，
// 用完即释放。WebGPU 的布局兼容性是按结构判定的，因此两处用同样的描述创建出的
// 布局互相兼容。
type BindGroupLayout struct {
	Label   string
	Entries []BindGroupLayoutEntry
}

// BindGroupEntry 把一个具体资源绑到槽位上。
// 三个资源字段按 Layout 里对应槽位的 Type 取用其一，其余留 nil。
type BindGroupEntry struct {
	Binding uint32
	Buffer  Buffer
	// Offset/Size 仅用于 Buffer。两者均为零时绑定整个 buffer；否则 Size
	// 必须非零，且 [Offset, Offset+Size) 必须完全落在 buffer 内。
	Offset  uint64
	Size    uint64
	Texture TextureView
	Sampler Sampler
}

// BindGroupDesc 描述一组资源绑定。
type BindGroupDesc struct {
	Label   string
	Layout  BindGroupLayout
	Entries []BindGroupEntry
}

// BindGroup 是一组已绑定的资源。
type BindGroup interface{ Release() }

// RenderPassDesc 描述一个渲染 pass。
type RenderPassDesc struct {
	Label     string
	ColorView TextureView
	// DepthView 为 nil 时不挂深度附件。深度清除值固定为 1.0。
	DepthView  TextureView
	ClearColor [4]float32
	// LoadClear 为 true 表示 pass 开始时清屏，否则保留已有内容。
	LoadClear bool
}

// RenderPass 是一个进行中的渲染 pass。
type RenderPass interface {
	SetPipeline(RenderPipeline)
	SetBindGroup(index uint32, g BindGroup)
	SetVertexBuffer(slot uint32, b Buffer, offset uint64)
	// SetIndexBuffer 的索引格式固定为 uint32——本工程的网格索引一律用 u32。
	SetIndexBuffer(b Buffer, offset uint64)
	// DrawIndexedIndirect 从 indirect 缓冲的 offset 处读取
	// (indexCount, instanceCount, firstIndex, baseVertex, firstInstance)。
	DrawIndexedIndirect(indirect Buffer, offset uint64)
	// Draw 是不走 indirect 的直接绘制，仅用于 spike 与 UI。
	Draw(vertexCount, instanceCount uint32)
	End()
}

// ComputePass 是一个进行中的计算 pass。
type ComputePass interface {
	SetPipeline(ComputePipeline)
	SetBindGroup(index uint32, g BindGroup)
	Dispatch(x, y, z uint32)
	End()
}

// CommandEncoder 录制一批 GPU 命令。
type CommandEncoder interface {
	BeginRenderPass(RenderPassDesc) RenderPass
	BeginComputePass(label string) ComputePass
	CopyBufferToBuffer(src Buffer, srcOffset uint64, dst Buffer, dstOffset, size uint64)
	Finish() CommandBuffer
}

// CommandBuffer 是一批录好待提交的命令。
type CommandBuffer interface{ Release() }

// Device 是 GPU 的入口。
type Device interface {
	CreateBuffer(BufferDesc) Buffer
	CreateShaderModule(wgsl string) ShaderModule
	CreateRenderPipeline(RenderPipelineDesc) RenderPipeline
	CreateComputePipeline(ComputePipelineDesc) ComputePipeline
	CreateBindGroup(BindGroupDesc) BindGroup
	CreateTexture(TextureDesc) Texture
	CreateSampler(SamplerDesc) Sampler
	CreateCommandEncoder() CommandEncoder
	Submit(...CommandBuffer)
	// Poll 推进设备事件队列。wait 为 true 时阻塞到队列清空，测试中读回数据要用。
	Poll(wait bool)
	Release()
}

// TextureFormat 是纹理像素格式。
type TextureFormat uint8

const (
	// FormatUndefined 是零值，表示"没有格式"——用于表达"本管线不挂深度附件"
	// 这类可选场景。底层绑定同样把 0 定义为 undefined。
	FormatUndefined TextureFormat = iota
	FormatBGRA8Unorm
	// FormatBGRA8UnormSrgb 是 macOS/Metal 上 surface 的首选格式，
	// 管线的 ColorFormat 必须跟它一致，所以枚举里必须有它。
	// 写进这种格式的值是线性的，硬件负责 sRGB 编码——不要再手工做 gamma。
	FormatBGRA8UnormSrgb
	FormatRGBA8Unorm
	FormatDepth32Float
	FormatR32Float
	FormatR32Uint
	// FormatR8Unorm 是单通道归一化格式，供字形 atlas 使用。
	// 必须追加在已有枚举之后，以保持既有数值稳定。
	FormatR8Unorm TextureFormat = 7
)

// TextureDimension 区分普通 2D 纹理与 2D 数组纹理。
type TextureDimension uint8

const (
	TextureDimension2D TextureDimension = iota
	TextureDimension2DArray
)

// TextureViewDimension 是着色器看到的视图维度。
type TextureViewDimension uint8

const (
	TextureViewDimensionAuto TextureViewDimension = iota
	TextureViewDimension2D
	TextureViewDimension2DArray
)

// TextureUsage 是纹理用途位掩码。
type TextureUsage uint32

const (
	TextureUsageBinding TextureUsage = 1 << iota
	TextureUsageRenderTarget
	TextureUsageCopyDst
	TextureUsageStorage
)

// TextureDesc 描述一张待创建的纹理。
// Layers 与 MipLevels 为 0 时按 1 处理。
type TextureDesc struct {
	Label     string
	Width     uint32
	Height    uint32
	Layers    uint32
	MipLevels uint32
	Format    TextureFormat
	Dimension TextureDimension
	Usage     TextureUsage
}

// Texture 是一张 GPU 纹理。
type Texture interface {
	// View 创建指定 mip / array layer 范围的视图。
	// 零值描述符表示覆盖全部 mip 与全部层。
	View(TextureViewDesc) TextureView
	// WriteLayer 把一层的像素数据写入指定 mip 级别。
	WriteLayer(layer, mip uint32, pixels []byte)
	// WriteRegion 把像素数据写入指定 layer/mip 的矩形子区域。
	WriteRegion(layer, mip, x, y, width, height uint32, pixels []byte)
	Release()
}

// TextureAspect 指定视图覆盖纹理的哪个 aspect。
type TextureAspect uint8

const (
	AspectAll TextureAspect = iota
	AspectDepthOnly
)

// TextureViewDesc 描述纹理视图。
// MipLevelCount/ArrayLayerCount 为 0 表示覆盖从 base 开始的全部剩余级别。
type TextureViewDesc struct {
	BaseMipLevel    uint32
	MipLevelCount   uint32
	BaseArrayLayer  uint32
	ArrayLayerCount uint32
	Aspect          TextureAspect
	Dimension       TextureViewDimension
}

// TextureView 是纹理的一个视图。
type TextureView interface{ Release() }

// FilterMode 是采样过滤方式。
type FilterMode uint8

const (
	FilterNearest FilterMode = iota
	FilterLinear
)

// AddressMode 是纹理坐标超出 [0,1] 时的处理方式。
type AddressMode uint8

const (
	// AddressRepeat 平铺重复。贪心合并后的四边形靠它重复材质。
	AddressRepeat AddressMode = iota
	AddressClampToEdge
)

// SamplerDesc 描述一个采样器。
type SamplerDesc struct {
	Label     string
	MagFilter FilterMode
	MinFilter FilterMode
	MipFilter FilterMode
	Address   AddressMode
}

// Sampler 是一个已创建的采样器。
type Sampler interface{ Release() }

// HandleKind 标识 NativeWindowHandle 里装的是哪一种平台句柄。
type HandleKind uint8

const (
	// HandleNone 表示不创建 surface，用于 headless 测试与离线计算。
	HandleNone HandleKind = iota
	// HandleKindNSWindow：Pointer 是 NSWindow*，Extra 未用。
	HandleKindNSWindow
)

// NativeWindowHandle 是平台相关的窗口句柄。
// macOS 上是 NSWindow 指针，Windows 上是 HWND，Linux 上是 (Display*, Window)。
type NativeWindowHandle struct {
	Kind    HandleKind
	Pointer uintptr
	Extra   uintptr
}

// PresentMode 是呈现模式。
type PresentMode uint8

const (
	PresentModeAutoVSync PresentMode = iota
	PresentModeAutoNoVSync
)

// Surface 是可呈现的目标表面。
type Surface interface {
	// Acquire 取得当前帧的颜色附件视图。
	// 返回 nil 表示这一帧取不到纹理（窗口被遮挡、最小化或 surface 过期），
	// 调用方应跳过本帧且不要调用 Present。
	Acquire() TextureView
	Present()
	// SetPresentMode 切换呈现模式；性能基准用 AutoNoVSync，
	// 避免显示器刷新率把 ≥100 fps 的目标钳住。
	SetPresentMode(PresentMode) error
	// Resize 在窗口尺寸变化后重新配置 surface。
	Resize(width, height uint32)
	Format() TextureFormat
	Release()
}

// 编译期断言：wgpu 后端确实实现了全部接口。
var (
	_ Device         = (*wgpuDevice)(nil)
	_ Buffer         = (*wgpuBuffer)(nil)
	_ CommandEncoder = (*wgpuEncoder)(nil)
	_ RenderPass     = (*wgpuRenderPass)(nil)
	_ ComputePass    = (*wgpuComputePass)(nil)
	_ Texture        = (*wgpuTexture)(nil)
	_ Surface        = (*wgpuSurface)(nil)

	// 其余小接口同样断言，免得后端改名后悄悄漏掉。
	_ ShaderModule    = (*wgpuShaderModule)(nil)
	_ RenderPipeline  = (*wgpuRenderPipeline)(nil)
	_ ComputePipeline = (*wgpuComputePipeline)(nil)
	_ BindGroup       = (*wgpuBindGroup)(nil)
	_ CommandBuffer   = (*wgpuCommandBuffer)(nil)
	_ TextureView     = (*wgpuTextureView)(nil)
	_ Sampler         = (*wgpuSampler)(nil)
)
