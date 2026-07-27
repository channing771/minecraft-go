# M0 技术验证 + M1 可飞行的世界 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 交付一个能在 32 区块视距下自由飞行的体素世界，渲染走 GPU-driven 单次 indirect draw 管线。

**Architecture:** 先用一周的 spike（M0）在 macOS/Metal 上验证 WebGPU 绑定能跑通 compute → indirect draw 链路，并把 GPU 层封进自研 `gfx` 抽象包；然后（M1）自底向上构建纯 Go 的世界数据层（调色板容器 → 噪声地形 → 贪心网格化 → 可见性图），最后接上渲染器与相机。纯 Go 部分全部可脱离 GPU 测试。

**Tech Stack:** Go 1.26 · WebGPU（`github.com/oliverbestmann/webgpu`，wgpu-native v29）· GLFW（`github.com/go-gl/glfw/v3.3/glfw`）· mgl32（`github.com/go-gl/mathgl`）· WGSL 着色器

**依据 spec:** `docs/superpowers/specs/2026-07-26-minecraft-go-design.md`

---

## Global Constraints

以下约束适用于**每一个**任务，不再逐任务重复：

- **Go 版本下限 go 1.26**；module path 为 `minecraft-go`（未发布模块，无需域名前缀）。
- **所有包置于 `internal/`**。禁止创建 `pkg/` 目录。
- **依赖方向单向，违反即为缺陷**（spec §3.1）：
  - `core` 不 import 任何内部包；`gfx` 不 import 任何游戏概念（不得出现 `BlockID`、`ChunkPos` 等类型）
  - `sim` 不得 import `render`；`world` 不得 import `net`
  - `render` 只能通过 `gfx` 接触 GPU——**`internal/render/` 下任何文件都不得 import WebGPU 绑定**
- **`internal/gfx` 是唯一允许 import `github.com/oliverbestmann/webgpu` 的包。**
- **世界高度 Y ∈ [-64, 320)**，每区块 24 个区段，区段边长 16。
- **仓库内不得提交任何二进制美术资源**（材质由代码生成）。原版 Minecraft 材质是 Mojang 版权资产。
- **确定性要求**：`worldgen` 与 `sim` 对同一种子 + 同一输入序列必须产出完全相同的结果。禁止在这两个包内使用 `map` 遍历顺序、`time`、未播种的随机源。
- **单元/集成测试命令统一为** `go test ./...`；纯 Go 微基准为 `go test -bench=. -benchmem ./...`。M1 的真实 1440p 帧时间与 RSS 门禁另用 Task 17 的 `cmd/mcgo --benchmark`。
- 提交信息用中文，遵循 `type: 摘要` 格式（`feat:` / `test:` / `fix:` / `docs:` / `chore:` / `perf:`）。

### 关于 GPU 相关任务的一个前提

Task 1 之前，**Go 的 WebGPU 绑定 API 签名未经实地验证**。Task 1 的核心产出之一就是把真实签名固化到 `docs/notes/webgpu-api.md`。Task 2 及之后的 GPU 代码以该文档为准——若与本计划中的示例代码有出入，**以绑定的真实 API 为准，并同步修正后续任务**。

若 Task 1–4（M0）证明该绑定不可用，**停止执行本计划**，回到 spec §2.2 重新选型；Task 12–15 的细节届时需重写。

---

# M0：技术验证 spike

出口条件：屏幕上出现一批方块，且**实例数由 GPU 的 compute shader 决定**，CPU 不知道要画多少个。

---

### Task 1: 项目骨架、绑定验证与空窗口

**Files:**
- Create: `go.mod`
- Create: `.gitignore`
- Create: `docs/notes/webgpu-api.md`
- Create: `cmd/gfxspike/main.go`
- Create: `internal/gfx/doc.go`
- Create: `internal/gfx/probe.go`（本任务全部的绑定调用都在这里）

**Interfaces:**
- Consumes: 无（首个任务）
- Produces: 可运行的 `cmd/gfxspike` 二进制；`docs/notes/webgpu-api.md` 中记录的真实绑定签名，后续所有 GPU 任务依赖它

- [ ] **Step 1: 初始化 module 与 .gitignore**

```bash
cd /Users/chen/chenwork/minecraft-go
go mod init minecraft-go
```

`.gitignore`：

```gitignore
# 编译产物
/bin/
*.exe
*.test
*.out

# 依赖缓存
/vendor/

# 编辑器
.DS_Store
.idea/
.vscode/

# 运行时产生的世界存档
/saves/

# 性能剖析产物
*.pprof
cpu.prof
mem.prof
```

- [ ] **Step 2: 拉取依赖并确认能编译**

```bash
go get github.com/oliverbestmann/webgpu@latest
go get github.com/go-gl/glfw/v3.3/glfw@latest
go get github.com/go-gl/mathgl/mgl32@latest
go mod tidy
```

预期：三个模块进入 `go.mod`。若 `oliverbestmann/webgpu` 拉取失败或无可用版本，**立即改用 `github.com/go-webgpu/webgpu`**（spec §2.2 的备选），并在 `docs/notes/webgpu-api.md` 首行记录这次替换及原因。

CGO 前提：macOS 需已安装 Xcode Command Line Tools（`xcode-select -p` 应返回路径）。

- [ ] **Step 3: 跑通绑定自带的 example，抄回真实 API 签名**

这一步是 M0 的核心，不可跳过。

```bash
# 把绑定源码拉到本地可读位置
go mod download github.com/oliverbestmann/webgpu
go list -m -f '{{.Dir}}' github.com/oliverbestmann/webgpu
# 进入上一条命令输出的目录，阅读其 examples/ 与顶层 API
```

阅读重点，逐条抄进 `docs/notes/webgpu-api.md`：

1. 如何创建 Instance（函数名、参数、是否返回 error）
2. 如何请求 Adapter 与 Device（同步还是回调/Future 风格）
3. 如何从 GLFW 窗口句柄创建 Surface（macOS 上需要 `CAMetalLayer`，绑定通常提供 `SurfaceDescriptorFromMetalLayer` 一类的结构；记录确切名称）
4. `SurfaceConfiguration` 的字段名与必填项；如何选择自动 VSync / 自动非 VSync present mode（Task 17 的 ≥100 fps 基准必须关闭 VSync）
5. `CreateBuffer` / `CreateShaderModule` / `CreateRenderPipeline` / `CreateComputePipeline` / `CreateBindGroup` 的描述符结构体字段名
6. `CommandEncoder` → `RenderPassEncoder` / `ComputePassEncoder` 的方法名
7. `DrawIndexedIndirect` 的确切方法名与参数（buffer + offset）
8. 资源释放语义：该 fork 声称加了 GC 自动释放，确认 `Release()` 是否仍需手动调用

`docs/notes/webgpu-api.md` 模板：

```markdown
# WebGPU 绑定 API 速查

- 绑定：github.com/oliverbestmann/webgpu@<版本>
- wgpu-native：v29.0.0.0
- 验证日期：2026-07-26
- 验证平台：darwin/arm64，Metal 后端

## 实例与设备

<抄写真实签名>

## Surface（macOS/Metal）

<抄写真实签名>

## 缓冲与着色器

<抄写真实签名>

## 间接绘制

<抄写真实签名>

## 资源释放语义

<记录结论>
```

- [ ] **Step 4: 写空窗口 + clear color**

**全部 WebGPU 绑定调用必须写在 `internal/gfx/probe.go` 里，`cmd/gfxspike/main.go` 不得 import 绑定。** 这条从第一行代码起就生效（见 Global Constraints），不是等 Task 2 再补救。Task 2 会把 `probe.go` 演化成完整的 `wgpu.go`，本任务先把边界立住。

`gfx` 同样不 import GLFW——它只接收一个平台相关的窗口句柄，与窗口库解耦。

**以 Step 3 抄回的真实签名为准编写。**

`internal/gfx/probe.go`：

```go
package gfx

// Probe 是 M0 的最小验证入口：用给定的原生窗口句柄建立设备与 surface，
// 每次调用 Frame 清一次屏。Task 2 会把它演化成完整的设备抽象。
type Probe struct {
	// 字段按 Step 3 抄回的绑定类型填写：
	// instance、adapter、device、queue、surface、surfaceFormat。
}

// NewProbe 依次创建 instance → surface(handle) → adapter → device
// → 配置 surface，全部按 docs/notes/webgpu-api.md 记录的真实签名调用。
//
// handle 是平台相关的窗口句柄：macOS 上是 NSWindow 指针。
func NewProbe(handle uintptr, width, height uint32) (*Probe, error)

// Frame 取 surface 当前纹理，开一个 render pass，
// LoadOp=Clear、ClearValue={0.1,0.2,0.3,1.0}、StoreOp=Store，
// 不画任何东西直接结束 pass，然后提交并 Present。
func (p *Probe) Frame() error

// Close 释放 surface 与设备。
func (p *Probe) Close()
```

`cmd/gfxspike/main.go`：

```go
// Command gfxspike 是 M0 技术验证程序。
// 它的唯一职责是证明 compute shader 能决定 indirect draw 的实例数。
package main

import (
	"log"
	"runtime"

	"github.com/go-gl/glfw/v3.3/glfw"
	"minecraft-go/internal/gfx"
)

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
	win, err := glfw.CreateWindow(1280, 720, "gfxspike", nil, nil)
	if err != nil {
		log.Fatalf("创建窗口失败: %v", err)
	}
	defer win.Destroy()

	// GetCocoaWindow 返回 NSWindow 指针。跨平台分支留到需要时再加——
	// M0 只验证 macOS/Metal 这一条最不确定的路径。
	probe, err := gfx.NewProbe(cocoaHandle(win), 1280, 720)
	if err != nil {
		log.Fatalf("创建 WebGPU 设备失败: %v", err)
	}
	defer probe.Close()

	for !win.ShouldClose() {
		glfw.PollEvents()
		if err := probe.Frame(); err != nil {
			log.Fatalf("渲染帧失败: %v", err)
		}
	}
}
```

`cocoaHandle` 是一个把 `win.GetCocoaWindow()` 转成 `uintptr` 的小助手——go-gl/glfw 不同版本的返回类型可能是 `unsafe.Pointer` 或 `uintptr`，以实际签名为准。

本步骤结束时，`cmd/gfxspike/main.go` 里不得出现任何 WebGPU 绑定的类型或函数。

- [ ] **Step 5: 运行验证**

```bash
go run ./cmd/gfxspike
```

预期：出现一个 1280×720 的窗口，整屏为深蓝色（0.1, 0.2, 0.3），关闭窗口进程正常退出。终端无 panic、无 wgpu 验证层报错。

若此步失败，说明绑定在 macOS/Metal 上的 surface 创建路径有问题——这正是 M0 要暴露的风险。记录失败现象到 `docs/notes/webgpu-api.md`，尝试备选绑定 `go-webgpu/webgpu`，两个都失败则停止执行本计划并上报。

- [ ] **Step 6: 提交**

```bash
git add go.mod go.sum .gitignore cmd/gfxspike/main.go internal/gfx docs/notes/webgpu-api.md
git commit -m "feat: 项目骨架与 WebGPU 绑定验证，跑通空窗口"
```

---

### Task 2: gfx 抽象层与三角形

**Files:**
- Create: `internal/gfx/gfx.go`（接口定义）
- Create: `internal/gfx/wgpu.go`（唯一 import WebGPU 绑定的文件）
- Create: `internal/gfx/shader/triangle.wgsl`
- Modify: `cmd/gfxspike/main.go`

**Interfaces:**
- Consumes: Task 1 的 `docs/notes/webgpu-api.md`
- Produces: `gfx.Device` 及其方法，后续所有渲染任务只通过这套接口接触 GPU

```go
// 本任务确立的对外契约（Task 3、4、12-15 依赖这些名字）
type Device interface {
    CreateBuffer(BufferDesc) Buffer
    CreateShaderModule(wgsl string) ShaderModule
    CreateRenderPipeline(RenderPipelineDesc) RenderPipeline
    CreateComputePipeline(ComputePipelineDesc) ComputePipeline
    CreateBindGroup(BindGroupDesc) BindGroup
    CreateCommandEncoder() CommandEncoder
    Submit(...CommandBuffer)
    Poll(wait bool)
}
```

- [ ] **Step 1: 定义 gfx 接口**

`internal/gfx/gfx.go`。**只定义我们实际用到的 WebGPU 子集**——不做通用渲染硬件接口（spec §5.6）。

```go
// Package gfx 把 GPU 后端封装在一组最小接口之后。
//
// 这是整个仓库中唯一允许 import WebGPU 绑定的包。上层（internal/render）
// 只依赖本包的接口，因此更换底层绑定时 render/ 无需改动任何一行。
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
	DepthFormat TextureFormat
}

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
	Binding       uint32
	Type          BindingType
	VisibleIn     ShaderStage
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
type BindGroupLayout struct {
	Label   string
	Entries []BindGroupLayoutEntry
}

// BindGroupEntry 把一个具体资源绑到槽位上。
type BindGroupEntry struct {
	Binding uint32
	Buffer  Buffer
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
	Label      string
	ColorView  TextureView
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
```

纹理相关类型（`TextureFormat`、`TextureDesc`、`Texture`、`TextureView`、`SamplerDesc`、`Sampler`）在同文件定义。Task 11 会用到 `TextureDimension2DArray`：

```go
// TextureFormat 是纹理像素格式。
type TextureFormat uint8

const (
	FormatBGRA8Unorm TextureFormat = iota
	FormatRGBA8Unorm
	FormatDepth32Float
	FormatR32Float
	FormatR32Uint
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
type TextureDesc struct {
	Label       string
	Width       uint32
	Height      uint32
	Layers      uint32
	MipLevels   uint32
	Format      TextureFormat
	Dimension   TextureDimension
	Usage       TextureUsage
}

// Texture 是一张 GPU 纹理。
type Texture interface {
	// View 创建指定 mip / array layer 范围的视图。
	// 零值描述符表示覆盖全部 mip 与全部层。
	View(TextureViewDesc) TextureView
	// WriteLayer 把一层的像素数据写入指定 mip 级别。
	WriteLayer(layer, mip uint32, rgba []byte)
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
	Aspect           TextureAspect
	Dimension        TextureViewDimension
}

// TextureView 是纹理的一个视图。
type TextureView interface{ Release() }

// FilterMode 是采样过滤方式。
type FilterMode uint8

const (
	FilterNearest FilterMode = iota
	FilterLinear
)

// SamplerDesc 描述一个采样器。
type SamplerDesc struct {
	Label     string
	MagFilter FilterMode
	MinFilter FilterMode
	MipFilter FilterMode
}

// Sampler 是一个已创建的采样器。
type Sampler interface{ Release() }
```

- [ ] **Step 2: 实现 wgpu 后端**

`internal/gfx/wgpu.go`：把 Task 1 中直接写在 `main.go` 里的绑定调用搬进来，实现上述接口。同时提供构造入口：

```go
// NewDevice 用给定的原生窗口句柄创建设备与 surface。
// handle 由 internal/client 从 GLFW 取得，本包不 import GLFW——
// 保持 gfx 与窗口库解耦。
func NewDevice(handle NativeWindowHandle, width, height uint32) (Device, Surface, error)

// NativeWindowHandle 是平台相关的窗口句柄。
// macOS 上是 NSWindow 指针，Windows 上是 HWND，Linux 上是 (Display*, Window)。
type NativeWindowHandle struct {
	Kind    HandleKind
	Pointer uintptr
	Extra   uintptr
}

// Surface 是可呈现的目标表面。
type Surface interface {
	// Acquire 取得当前帧的颜色附件视图。
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

type PresentMode uint8

const (
	PresentModeAutoVSync PresentMode = iota
	PresentModeAutoNoVSync
)
```

后端实现还必须满足两条 WebGPU 验证规则：

1. `Buffer.ReadBack` 不直接映射工作缓冲，而是创建 `MapRead|CopyDst` staging buffer，从要求带 `CopySrc` 的工作缓冲拷贝后再映射。
2. `Texture.View(TextureViewDesc)` 必须能创建单 mip 视图；纹理布局必须区分 filterable float、unfilterable float、depth 与 storage，并显式携带 2D/2D-array 维度。`BindingStorageTextureWrite` 映射到带明确 `StorageFormat` 的 write-only storage texture layout。Task 11 的 texture array 与 Task 15 的 R32Float Hi-Z 都依赖这些能力，Task 2 的三角形虽暂时用不到也必须在此固化接口。

- [ ] **Step 3: 写三角形着色器**

`internal/gfx/shader/triangle.wgsl`：

```wgsl
// 顶点由 vertex_index 程序化生成，不需要顶点缓冲。
@vertex
fn vs_main(@builtin(vertex_index) i: u32) -> @builtin(position) vec4f {
    var pos = array<vec2f, 3>(
        vec2f( 0.0,  0.5),
        vec2f(-0.5, -0.5),
        vec2f( 0.5, -0.5),
    );
    return vec4f(pos[i], 0.0, 1.0);
}

@fragment
fn fs_main() -> @location(0) vec4f {
    return vec4f(1.0, 0.6, 0.1, 1.0);
}
```

- [ ] **Step 4: 改写 spike 走 gfx 接口**

`cmd/gfxspike/main.go` 改成：GLFW 建窗 → 取原生句柄 → `gfx.NewDevice` → 创建管线 → 每帧 `BeginRenderPass` + `SetPipeline` + `Draw(3, 1)` + `End` + `Submit` + `Present`。

**验收要点：`main.go` 与未来的 `internal/render/` 都不再 import WebGPU 绑定。**

- [ ] **Step 5: 运行验证**

```bash
go run ./cmd/gfxspike
```

预期：深蓝背景上出现一个橙色三角形。

- [ ] **Step 6: 加一条编译期约束检查**

`internal/gfx/gfx.go` 末尾：

```go
// 编译期断言：wgpu 后端确实实现了全部接口。
var (
	_ Device         = (*wgpuDevice)(nil)
	_ Buffer         = (*wgpuBuffer)(nil)
	_ CommandEncoder = (*wgpuEncoder)(nil)
	_ RenderPass     = (*wgpuRenderPass)(nil)
	_ ComputePass    = (*wgpuComputePass)(nil)
	_ Texture        = (*wgpuTexture)(nil)
	_ Surface        = (*wgpuSurface)(nil)
)
```

- [ ] **Step 7: 提交**

```bash
git add internal/gfx cmd/gfxspike/main.go
git commit -m "feat: gfx 抽象层与三角形，GPU 后端收敛到单一包"
```

---

### Task 3: compute shader 的 headless 可断言测试

**Files:**
- Create: `internal/gfx/compute_test.go`
- Create: `internal/gfx/shader/testdouble.wgsl`
- Create: `internal/gfx/headless.go`

**Interfaces:**
- Consumes: Task 2 的 `gfx.Device`、`gfx.Buffer.ReadBack`
- Produces: `gfx.NewHeadlessDevice() (Device, error)`——Task 14、15 的剔除逻辑测试依赖它

这个任务的价值：**让 GPU 计算变成可以写 `go test` 断言的东西**。没有它，后面 compute 剔除的正确性只能靠肉眼看画面，这在剔除出 bug 时（画面少了几个区块）几乎不可调。

- [ ] **Step 1: 写失败的测试**

`internal/gfx/compute_test.go`：

```go
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
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
go test ./internal/gfx/ -run TestComputeDoublesInput -v
```

预期：编译失败，`undefined: gfx.NewHeadlessDevice` 与 `undefined: gfx.ShaderTestDouble`。

- [ ] **Step 3: 写着色器**

`internal/gfx/shader/testdouble.wgsl`：

```wgsl
@group(0) @binding(0) var<storage, read>       src: array<u32>;
@group(0) @binding(1) var<storage, read_write> dst: array<u32>;

@compute @workgroup_size(64)
fn cs_main(@builtin(global_invocation_id) gid: vec3u) {
    let i = gid.x;
    if (i >= arrayLength(&src)) {
        return;
    }
    dst[i] = src[i] * 2u;
}
```

- [ ] **Step 4: 实现 headless 设备与着色器嵌入**

`internal/gfx/headless.go`：

```go
package gfx

import _ "embed"

//go:embed shader/testdouble.wgsl
var ShaderTestDouble string

// NewHeadlessDevice 创建一个不带 surface 的设备，用于测试与离线计算。
// 本机无可用适配器时返回 error，调用方（测试）应据此 skip 而非 fail——
// CI 容器里常常没有 GPU。
func NewHeadlessDevice() (Device, error) {
	return newDevice(NativeWindowHandle{Kind: HandleNone}, 0, 0)
}
```

`newDevice` 是 Task 2 中 `NewDevice` 的内部实现，`HandleNone` 时跳过 surface 创建、返回 nil surface。

同时在 `internal/gfx/gfx.go` 中补上 Task 2 遗漏的句柄类型枚举：

```go
// HandleKind 区分平台相关的窗口句柄类型。
type HandleKind uint8

const (
	// HandleNone 表示无窗口，用于 headless 设备。
	HandleNone HandleKind = iota
	HandleCocoa   // macOS：Pointer 为 NSWindow*
	HandleWin32   // Windows：Pointer 为 HWND
	HandleX11     // Linux/X11：Pointer 为 Display*，Extra 为 Window
	HandleWayland // Linux/Wayland：Pointer 为 wl_display*，Extra 为 wl_surface*
)
```

- [ ] **Step 5: 运行测试，确认通过**

```bash
go test ./internal/gfx/ -run TestComputeDoublesInput -v
```

预期：PASS。若本机确实无适配器则为 SKIP——但在你的 Apple Silicon 上必须是 PASS，SKIP 说明设备创建有问题，要查。

- [ ] **Step 6: 提交**

```bash
git add internal/gfx
git commit -m "test: compute shader 的 headless 可断言测试"
```

---

### Task 4: compute → indirect draw（M0 出口）

**Files:**
- Create: `internal/gfx/shader/spike_cull.wgsl`
- Create: `internal/gfx/shader/spike_draw.wgsl`
- Modify: `cmd/gfxspike/main.go`
- Create: `internal/gfx/indirect_test.go`

**Interfaces:**
- Consumes: Task 2 的 `RenderPass.DrawIndexedIndirect`、Task 3 的 `NewHeadlessDevice`
- Produces: 验证结论——本绑定在 Metal 上支持 GPU 决定实例数的间接绘制

- [ ] **Step 1: 写失败的测试——compute 能否正确填写 indirect 参数**

`internal/gfx/indirect_test.go`：

```go
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
	// instanceCount 由 compute 累加，初始为 0

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
	dev.Submit(enc.Finish())
	dev.Poll(true)

	got := argsBuf.ReadBack()
	if n := binary.LittleEndian.Uint32(got[4:]); n != wantInstances {
		t.Fatalf("instanceCount = %d，想要 %d", n, wantInstances)
	}
	if n := binary.LittleEndian.Uint32(got[0:]); n != 6 {
		t.Fatalf("indexCount 被意外改写 = %d，想要 6", n)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
go test ./internal/gfx/ -run TestComputeFillsIndirectArgs -v
```

预期：编译失败，`undefined: gfx.ShaderSpikeCull`。

- [ ] **Step 3: 写剔除着色器**

`internal/gfx/shader/spike_cull.wgsl`：

```wgsl
// DrawIndexedIndirect 的参数布局，字段顺序由 WebGPU 规范固定。
struct DrawIndexedIndirect {
    index_count:    u32,
    instance_count: atomic<u32>,
    first_index:    u32,
    base_vertex:    u32,
    first_instance: u32,
};

@group(0) @binding(0) var<storage, read_write> args: DrawIndexedIndirect;
@group(0) @binding(1) var<storage, read_write> visible: array<u32>;

// 筛选规则：偶数号候选通过。真实管线里这里换成视锥/遮挡/背面判定。
@compute @workgroup_size(64)
fn cs_main(@builtin(global_invocation_id) gid: vec3u) {
    let i = gid.x;
    if (i >= arrayLength(&visible)) {
        return;
    }
    if ((i & 1u) != 0u) {
        return;
    }
    // 原子累加得到本实例在紧凑输出中的槽位，同时累出总实例数。
    let slot = atomicAdd(&args.instance_count, 1u);
    visible[slot] = i;
}
```

- [ ] **Step 4: 嵌入着色器**

`internal/gfx/headless.go` 追加：

```go
//go:embed shader/spike_cull.wgsl
var ShaderSpikeCull string

//go:embed shader/spike_draw.wgsl
var ShaderSpikeDraw string
```

- [ ] **Step 5: 运行测试，确认通过**

```bash
go test ./internal/gfx/ -run TestComputeFillsIndirectArgs -v
```

预期：PASS，`instanceCount == 64`。

**这一步是 M0 最关键的信号。** 若 `atomicAdd` 在 Metal 后端上不工作，或 indirect 缓冲无法同时作为 storage 绑定，GPU-driven 方案不成立——记录到 `docs/notes/webgpu-api.md` 并上报。

- [ ] **Step 6: 写绘制着色器**

`internal/gfx/shader/spike_draw.wgsl`——把通过筛选的实例画成一排方块：

```wgsl
struct Camera {
    view_proj: mat4x4f,
};

@group(0) @binding(0) var<uniform> camera: Camera;
@group(0) @binding(1) var<storage, read> visible: array<u32>;

struct VsOut {
    @builtin(position) pos:   vec4f,
    @location(0)       color: vec3f,
};

@vertex
fn vs_main(
    @builtin(vertex_index)   vi: u32,
    @builtin(instance_index) ii: u32,
) -> VsOut {
    // 一个正方形的 4 个角，配合 6 个索引组成两个三角形。
    var corner = array<vec2f, 4>(
        vec2f(0.0, 0.0), vec2f(1.0, 0.0),
        vec2f(1.0, 1.0), vec2f(0.0, 1.0),
    );

    let id = visible[ii];
    // 沿 X 轴排开，方块索引直接当世界坐标。
    let origin = vec3f(f32(id) * 1.5, 0.0, 0.0);
    let local  = vec3f(corner[vi].x, corner[vi].y, 0.0);

    var out: VsOut;
    out.pos   = camera.view_proj * vec4f(origin + local, 1.0);
    out.color = vec3f(f32(id) / 128.0, 0.5, 1.0 - f32(id) / 128.0);
    return out;
}

@fragment
fn fs_main(in: VsOut) -> @location(0) vec4f {
    return vec4f(in.color, 1.0);
}
```

- [ ] **Step 7: spike 主程序串起完整链路**

`cmd/gfxspike/main.go` 每帧：

1. 把 indirect 缓冲的 `instance_count` 清零（`CopyBufferToBuffer` 从一个全零的小缓冲拷 4 字节，或每帧重写 args）
2. compute pass：跑 `spike_cull`，填出 `instance_count` 与 `visible[]`
3. render pass：`SetPipeline(spikeDraw)`、`SetIndexBuffer`（6 个索引 `[0,1,2, 0,2,3]`）、`DrawIndexedIndirect(argsBuf, 0)`
4. Present

相机用一个固定的正交或透视矩阵，能把 X 轴上 0..192 范围看全即可（`mgl32.LookAtV` + `mgl32.Perspective`）。

**CPU 侧全程不知道要画多少个实例**——这就是 M0 的出口条件。

- [ ] **Step 8: 运行验证**

```bash
go run ./cmd/gfxspike
```

预期：屏幕上出现 64 个方块（128 个候选中的偶数号），颜色沿 X 轴渐变。

- [ ] **Step 9: 记录 M0 结论**

在 `docs/notes/webgpu-api.md` 末尾追加一节：

```markdown
## M0 结论（2026-07-26）

- [x] macOS/Metal 上 surface 创建可用
- [x] compute shader 可读写 storage buffer 并被 CPU 断言
- [x] compute 内 atomicAdd 到 indirect 参数缓冲可用
- [x] DrawIndexedIndirect 实例数由 GPU 决定

结论：GPU-driven 管线成立，M1 可以按 spec §5 推进。
遗留问题：<记录任何绕过去的坑，如资源释放时机、验证层警告>
```

若任何一条为 `[ ]`，写清现象，停止执行本计划，回到 spec §2.2。

- [ ] **Step 10: 提交**

```bash
git add internal/gfx cmd/gfxspike docs/notes/webgpu-api.md
git commit -m "feat: compute 决定实例数的 indirect draw 打通，M0 出口达成"
```

---

# M1：可飞行的世界

出口条件：32 区块视距下自由飞行，帧时间达 spec §1.2 指标。

Task 5–10 全部是纯 Go，**不碰 GPU、不需要窗口**，可以完全用 `go test` 覆盖。这是刻意的：M1 的大部分复杂度在数据层，把它们隔离出来测，比在渲染出画面后靠肉眼调试便宜得多。

---

### Task 5: core 坐标类型与数学

**Files:**
- Create: `internal/core/pos.go`
- Create: `internal/core/pos_test.go`
- Create: `internal/core/geom.go`
- Create: `internal/core/geom_test.go`

**Interfaces:**
- Consumes: 无（`core` 不依赖任何内部包）
- Produces:

```go
type BlockPos struct{ X, Y, Z int32 }
type ChunkPos struct{ X, Z int32 }
type SectionPos struct{ X, Y, Z int32 }   // Y 是区段索引，取值 0..23

func (b BlockPos) Chunk() ChunkPos
func (b BlockPos) SectionIndex() int      // 0..23
func (b BlockPos) Local() (x, y, z int)   // 各 0..15

type AABB struct{ Min, Max mgl32.Vec3 }
type Ray  struct{ Origin, Dir mgl32.Vec3 }
type Frustum [6]mgl32.Vec4                // 6 个平面，xyz 为法线、w 为距离

func FrustumFrom(viewProj mgl32.Mat4) Frustum
func (f Frustum) IntersectsAABB(b AABB) bool
```

- [ ] **Step 1: 写失败的测试——坐标换算在负坐标下的行为**

负坐标的向下取整是这类项目的经典 bug 源：`-1 / 16 == 0` 但正确答案是 `-1`。用位移而非除法，并用测试钉死。

`internal/core/pos_test.go`：

```go
package core_test

import (
	"testing"

	"minecraft-go/internal/core"
)

func TestBlockPosChunkHandlesNegatives(t *testing.T) {
	cases := []struct {
		name string
		in   core.BlockPos
		want core.ChunkPos
	}{
		{"原点", core.BlockPos{X: 0, Y: 0, Z: 0}, core.ChunkPos{X: 0, Z: 0}},
		{"区块内最大", core.BlockPos{X: 15, Y: 0, Z: 15}, core.ChunkPos{X: 0, Z: 0}},
		{"跨到下一区块", core.BlockPos{X: 16, Y: 0, Z: 16}, core.ChunkPos{X: 1, Z: 1}},
		{"负一属于 -1 号区块", core.BlockPos{X: -1, Y: 0, Z: -1}, core.ChunkPos{X: -1, Z: -1}},
		{"负十六属于 -1 号区块", core.BlockPos{X: -16, Y: 0, Z: -16}, core.ChunkPos{X: -1, Z: -1}},
		{"负十七属于 -2 号区块", core.BlockPos{X: -17, Y: 0, Z: -17}, core.ChunkPos{X: -2, Z: -2}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.in.Chunk(); got != c.want {
				t.Fatalf("Chunk() = %+v，想要 %+v", got, c.want)
			}
		})
	}
}

func TestBlockPosLocalAlwaysInRange(t *testing.T) {
	for _, y := range []int32{core.MinY, -1, 0, 1, core.MaxY - 1} {
		for _, x := range []int32{-33, -17, -16, -1, 0, 15, 16, 31} {
			p := core.BlockPos{X: x, Y: y, Z: x}
			lx, ly, lz := p.Local()
			if lx < 0 || lx > 15 || ly < 0 || ly > 15 || lz < 0 || lz > 15 {
				t.Fatalf("Local() 越界: pos=%+v -> (%d,%d,%d)", p, lx, ly, lz)
			}
		}
	}
}

func TestBlockPosSectionIndexCoversWorldHeight(t *testing.T) {
	if got := (core.BlockPos{Y: core.MinY}).SectionIndex(); got != 0 {
		t.Fatalf("世界底部区段索引 = %d，想要 0", got)
	}
	if got := (core.BlockPos{Y: core.MaxY - 1}).SectionIndex(); got != core.SectionsPerChunk-1 {
		t.Fatalf("世界顶部区段索引 = %d，想要 %d", got, core.SectionsPerChunk-1)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
go test ./internal/core/ -v
```

预期：编译失败，`undefined: core.BlockPos` 等。

- [ ] **Step 3: 实现坐标类型**

`internal/core/pos.go`：

```go
// Package core 提供与客户端/服务端无关的公共域类型。
//
// 本包不 import 任何其他内部包（见 spec §3.1）。
package core

// 世界几何常量（spec §4.1）。
const (
	// SectionSize 是区段边长，区段含 16³ = 4096 个方块。
	SectionSize = 16
	// SectionShift 是 SectionSize 的以 2 为底的对数，用于位移代替除法。
	SectionShift = 4
	// SectionMask 用于取区段内局部坐标。
	SectionMask = SectionSize - 1

	// MinY 是世界最低方块的 Y 坐标（含）。
	MinY = -64
	// MaxY 是世界最高方块 Y 坐标的上界（不含）。
	MaxY = 320
	// SectionsPerChunk 是每个区块的区段数。
	SectionsPerChunk = (MaxY - MinY) / SectionSize // 24
	// BlocksPerSection 是每个区段的方块数。
	BlocksPerSection = SectionSize * SectionSize * SectionSize // 4096
)

// BlockPos 是方块的世界坐标。
type BlockPos struct{ X, Y, Z int32 }

// ChunkPos 是区块的世界坐标（16×16 的水平柱）。
type ChunkPos struct{ X, Z int32 }

// SectionPos 定位一个区段。Y 是区段索引（0..SectionsPerChunk-1），不是方块 Y。
type SectionPos struct{ X, Y, Z int32 }

// Chunk 返回该方块所在的区块坐标。
//
// 用算术右移而非除法：Go 的整数除法向零取整，-1/16 得 0，
// 而正确答案是 -1。算术右移天然向负无穷取整。
func (b BlockPos) Chunk() ChunkPos {
	return ChunkPos{X: b.X >> SectionShift, Z: b.Z >> SectionShift}
}

// SectionIndex 返回该方块在其区块内的区段索引，取值 0..SectionsPerChunk-1。
// 调用方需保证 Y 在 [MinY, MaxY) 内。
func (b BlockPos) SectionIndex() int {
	return int((b.Y - MinY) >> SectionShift)
}

// Local 返回该方块在其区段内的局部坐标，三个分量均在 0..15。
func (b BlockPos) Local() (x, y, z int) {
	return int(b.X & SectionMask),
		int((b.Y - MinY) & SectionMask),
		int(b.Z & SectionMask)
}

// Section 返回该方块所在的区段坐标。
func (b BlockPos) Section() SectionPos {
	return SectionPos{
		X: b.X >> SectionShift,
		Y: int32(b.SectionIndex()),
		Z: b.Z >> SectionShift,
	}
}

// MinCorner 返回该区段最小角的方块世界坐标。
func (s SectionPos) MinCorner() BlockPos {
	return BlockPos{
		X: s.X << SectionShift,
		Y: s.Y<<SectionShift + MinY,
		Z: s.Z << SectionShift,
	}
}
```

- [ ] **Step 4: 运行测试，确认通过**

```bash
go test ./internal/core/ -run 'TestBlockPos' -v
```

预期：全部 PASS。

- [ ] **Step 5: 写视锥测试**

`internal/core/geom_test.go`：

```go
package core_test

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"minecraft-go/internal/core"
)

func TestFrustumCullsBehindCamera(t *testing.T) {
	// 相机在原点看向 -Z，这是 OpenGL/WebGPU 的惯例朝向。
	view := mgl32.LookAtV(
		mgl32.Vec3{0, 0, 0},
		mgl32.Vec3{0, 0, -1},
		mgl32.Vec3{0, 1, 0},
	)
	proj := mgl32.Perspective(mgl32.DegToRad(70), 16.0/9.0, 0.1, 1000)
	f := core.FrustumFrom(proj.Mul4(view))

	inFront := core.AABB{
		Min: mgl32.Vec3{-1, -1, -20},
		Max: mgl32.Vec3{1, 1, -18},
	}
	if !f.IntersectsAABB(inFront) {
		t.Fatal("正前方 20 米的盒子被错误剔除")
	}

	behind := core.AABB{
		Min: mgl32.Vec3{-1, -1, 18},
		Max: mgl32.Vec3{1, 1, 20},
	}
	if f.IntersectsAABB(behind) {
		t.Fatal("相机背后的盒子没有被剔除")
	}

	farAway := core.AABB{
		Min: mgl32.Vec3{-1, -1, -2000},
		Max: mgl32.Vec3{1, 1, -1900},
	}
	if f.IntersectsAABB(farAway) {
		t.Fatal("远平面外的盒子没有被剔除")
	}

	wayLeft := core.AABB{
		Min: mgl32.Vec3{500, -1, -20},
		Max: mgl32.Vec3{502, 1, -18},
	}
	if f.IntersectsAABB(wayLeft) {
		t.Fatal("视锥左右范围外的盒子没有被剔除")
	}
}
```

- [ ] **Step 6: 运行测试，确认失败**

```bash
go test ./internal/core/ -run TestFrustum -v
```

预期：`undefined: core.FrustumFrom`。

- [ ] **Step 7: 实现几何类型**

`internal/core/geom.go`：

```go
package core

import "github.com/go-gl/mathgl/mgl32"

// AABB 是轴对齐包围盒。
type AABB struct{ Min, Max mgl32.Vec3 }

// Ray 是一条射线，Dir 应为单位向量。
type Ray struct{ Origin, Dir mgl32.Vec3 }

// Frustum 是 6 个平面：左、右、下、上、近、远。
// 每个平面存为 vec4，xyz 是指向视锥内侧的法线，w 是平面到原点的有符号距离。
type Frustum [6]mgl32.Vec4

// FrustumFrom 用 Gribb-Hartmann 方法从 view-projection 矩阵提取 6 个平面。
//
// mgl32.Mat4 是列主序，索引 m[col*4+row]。
func FrustumFrom(m mgl32.Mat4) Frustum {
	row := func(i int) mgl32.Vec4 {
		return mgl32.Vec4{m[0*4+i], m[1*4+i], m[2*4+i], m[3*4+i]}
	}
	r0, r1, r2, r3 := row(0), row(1), row(2), row(3)

	var f Frustum
	f[0] = r3.Add(r0) // 左
	f[1] = r3.Sub(r0) // 右
	f[2] = r3.Add(r1) // 下
	f[3] = r3.Sub(r1) // 上
	f[4] = r2         // 近（WebGPU 深度范围是 [0,1]，近平面直接取第 2 行）
	f[5] = r3.Sub(r2) // 远

	// 归一化，使 w 成为真实距离——否则不同平面的尺度不一致。
	for i := range f {
		n := mgl32.Vec3{f[i][0], f[i][1], f[i][2]}
		if l := n.Len(); l > 0 {
			f[i] = f[i].Mul(1 / l)
		}
	}
	return f
}

// IntersectsAABB 判断包围盒是否与视锥相交。
//
// 用「正顶点」测试：对每个平面，取盒子在该平面法线方向上最远的那个角，
// 若它都在平面外侧，则整个盒子都在外侧。这会保留少量假阳性
// （盒子在视锥角落外但被判为相交），对剔除而言是安全的方向。
func (f Frustum) IntersectsAABB(b AABB) bool {
	for _, p := range f {
		positive := mgl32.Vec3{b.Min[0], b.Min[1], b.Min[2]}
		if p[0] >= 0 {
			positive[0] = b.Max[0]
		}
		if p[1] >= 0 {
			positive[1] = b.Max[1]
		}
		if p[2] >= 0 {
			positive[2] = b.Max[2]
		}
		if p[0]*positive[0]+p[1]*positive[1]+p[2]*positive[2]+p[3] < 0 {
			return false
		}
	}
	return true
}
```

- [ ] **Step 8: 运行全部 core 测试**

```bash
go test ./internal/core/ -v
```

预期：全部 PASS。

- [ ] **Step 9: 提交**

```bash
git add internal/core
git commit -m "feat: core 坐标类型与视锥数学"
```

---

### Task 6: 调色板容器

**Files:**
- Create: `internal/world/palette.go`
- Create: `internal/world/palette_test.go`

**Interfaces:**
- Consumes: `core.BlocksPerSection`
- Produces:

```go
type BlockID uint16

const AirID BlockID = 0

type PalettedContainer struct{ /* 非导出字段 */ }

func NewPalettedContainer(fill BlockID) *PalettedContainer
func (c *PalettedContainer) Get(x, y, z int) BlockID
func (c *PalettedContainer) Set(x, y, z int, id BlockID)
func (c *PalettedContainer) Compact()          // 惰性降级，tick 末调用
func (c *PalettedContainer) IsUniform() (BlockID, bool)
func (c *PalettedContainer) PayloadBytes() int // 位数据、调色板与 lookup 条目的逻辑大小
func (c *PalettedContainer) Clone() *PalettedContainer
```

这是内存预算的关键（spec §4.1：朴素方案 820 MB，不可行）。

- [ ] **Step 1: 写失败的属性测试**

对拍一个朴素的 `[4096]BlockID` 参考实现。任何形态升降级的 bug 都会被随机操作序列抓出来。

`internal/world/palette_test.go`：

```go
package world_test

import (
	"math/rand"
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

// naiveSection 是对拍用的参考实现：最笨但显然正确。
type naiveSection [core.BlocksPerSection]world.BlockID

func idx(x, y, z int) int { return (y << 8) | (z << 4) | x }

// TestPalettedContainerMatchesNaive 用随机操作序列对拍参考实现。
// 覆盖三态之间的全部升降级路径。
func TestPalettedContainerMatchesNaive(t *testing.T) {
	// 三档不同的方块种类数，分别把容器逼进单值态、索引态、直接态。
	for _, variety := range []int{1, 12, 200, 5000} {
		variety := variety
		t.Run("", func(t *testing.T) {
			rng := rand.New(rand.NewSource(int64(variety)))
			c := world.NewPalettedContainer(world.AirID)
			var want naiveSection

			for op := 0; op < 20000; op++ {
				x, y, z := rng.Intn(16), rng.Intn(16), rng.Intn(16)
				id := world.BlockID(rng.Intn(variety) + 1)
				c.Set(x, y, z, id)
				want[idx(x, y, z)] = id

				// 每隔一段触发一次降级，确保 Compact 不破坏内容。
				if op%1000 == 999 {
					c.Compact()
				}
			}

			for y := 0; y < 16; y++ {
				for z := 0; z < 16; z++ {
					for x := 0; x < 16; x++ {
						if got := c.Get(x, y, z); got != want[idx(x, y, z)] {
							t.Fatalf("variety=%d (%d,%d,%d): Get = %d，想要 %d",
								variety, x, y, z, got, want[idx(x, y, z)])
						}
					}
				}
			}
		})
	}
}

// TestPalettedContainerUniformCostsNothing 验证单值态不分配位数据。
// 这是 100 MB 内存预算成立的前提（spec §4.1）。
func TestPalettedContainerUniformCostsNothing(t *testing.T) {
	c := world.NewPalettedContainer(world.AirID)
	if _, ok := c.IsUniform(); !ok {
		t.Fatal("新建容器应为单值态")
	}
	if n := c.PayloadBytes(); n > 64 {
		t.Fatalf("单值态占用 %d 字节，应接近 0", n)
	}

	// 写入同一个值不应触发升级。
	c.Set(3, 4, 5, world.AirID)
	if _, ok := c.IsUniform(); !ok {
		t.Fatal("写入相同值后仍应为单值态")
	}

	// 写入不同值触发升级，全部改回后 Compact 应降级回单值态。
	c.Set(3, 4, 5, world.BlockID(7))
	if _, ok := c.IsUniform(); ok {
		t.Fatal("写入不同值后不应还是单值态")
	}
	c.Set(3, 4, 5, world.AirID)
	c.Compact()
	if _, ok := c.IsUniform(); !ok {
		t.Fatal("内容重新统一后 Compact 应降级回单值态")
	}
}

// TestPalettedContainerCloneIsDeep 验证 COW 依赖的深拷贝语义（spec §4.3）。
func TestPalettedContainerCloneIsDeep(t *testing.T) {
	c := world.NewPalettedContainer(world.AirID)
	c.Set(1, 2, 3, world.BlockID(42))

	cp := c.Clone()
	cp.Set(1, 2, 3, world.BlockID(99))

	if got := c.Get(1, 2, 3); got != 42 {
		t.Fatalf("修改副本影响了原件: 原件 Get = %d，想要 42", got)
	}
	if got := cp.Get(1, 2, 3); got != 99 {
		t.Fatalf("副本 Get = %d，想要 99", got)
	}
}

func BenchmarkPalettedContainerGet(b *testing.B) {
	rng := rand.New(rand.NewSource(1))
	c := world.NewPalettedContainer(world.AirID)
	for i := 0; i < 4096; i++ {
		c.Set(rng.Intn(16), rng.Intn(16), rng.Intn(16), world.BlockID(rng.Intn(64)+1))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = c.Get(i&15, (i>>4)&15, (i>>8)&15)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
go test ./internal/world/ -v
```

预期：`undefined: world.NewPalettedContainer`。

- [ ] **Step 3: 实现调色板容器**

`internal/world/palette.go`：

```go
// Package world 提供世界数据模型：区块、区段、调色板存储与光照。
package world

import "minecraft-go/internal/core"

// BlockID 是全局方块 ID。0 恒为空气。
type BlockID uint16

// AirID 是空气的方块 ID。
const AirID BlockID = 0

// storageKind 是调色板容器的三种形态（spec §4.1）。
type storageKind uint8

const (
	// kindSingle：整段同一种方块，不分配位数据。
	kindSingle storageKind = iota
	// kindIndexed：用调色板索引，每方块 4 或 8 位。
	kindIndexed
	// kindDirect：直接存全局 ID，每方块 15 位。
	kindDirect
)

// directBits 是直接态每方块的位数。15 位可表示 32768 种方块。
const directBits = 15

// PalettedContainer 存储一个 16³ 区段的方块。
//
// 三态自动升级，降级由 Compact 惰性触发——避免在方块反复变更时抖动。
// 本类型不是并发安全的；并发访问由 COW + 原子指针交换保证（spec §4.3）。
type PalettedContainer struct {
	kind   storageKind
	single BlockID          // kindSingle 时有效
	palette []BlockID       // kindIndexed 时有效，索引 -> BlockID
	lookup  map[BlockID]uint32 // kindIndexed 时有效，BlockID -> 索引
	bits    uint8           // 每方块位数：kindSingle 为 0
	data    []uint64        // 位打包数据
}

// NewPalettedContainer 创建一个全部填充 fill 的容器，处于单值态。
func NewPalettedContainer(fill BlockID) *PalettedContainer {
	return &PalettedContainer{kind: kindSingle, single: fill}
}

// blockIndex 把局部坐标映射为 0..4095 的线性索引。
//
// 用 YZX 顺序：同一 Y 平面上的方块在内存中连续，
// 贪心网格化按平面切片扫描（Task 9），这个顺序对它的缓存局部性最好。
func blockIndex(x, y, z int) int { return (y << 8) | (z << 4) | x }

// Get 返回局部坐标处的方块。坐标须在 0..15。
func (c *PalettedContainer) Get(x, y, z int) BlockID {
	if c.kind == kindSingle {
		return c.single
	}
	v := c.readRaw(blockIndex(x, y, z))
	if c.kind == kindDirect {
		return BlockID(v)
	}
	return c.palette[v]
}

// readRaw 从位打包数据中读出第 i 个槽的原始值。
//
// 不跨 uint64 边界打包：每个 uint64 装 64/bits 个槽，余下的位空着。
// 直接态每字 4 个槽（60 位用、4 位废），换来的是无分支的读写路径。
func (c *PalettedContainer) readRaw(i int) uint32 {
	perWord := 64 / int(c.bits)
	word := c.data[i/perWord]
	shift := uint((i % perWord) * int(c.bits))
	mask := uint64(1)<<c.bits - 1
	return uint32((word >> shift) & mask)
}

// writeRaw 把原始值写入第 i 个槽。
func (c *PalettedContainer) writeRaw(i int, v uint32) {
	perWord := 64 / int(c.bits)
	shift := uint((i % perWord) * int(c.bits))
	mask := uint64(1)<<c.bits - 1
	w := &c.data[i/perWord]
	*w = (*w &^ (mask << shift)) | (uint64(v)&mask)<<shift
}

// wordsFor 返回容纳 4096 个 bits 位槽所需的 uint64 数量。
func wordsFor(bits uint8) int {
	perWord := 64 / int(bits)
	return (core.BlocksPerSection + perWord - 1) / perWord
}

// Set 写入局部坐标处的方块，必要时升级存储形态。
func (c *PalettedContainer) Set(x, y, z int, id BlockID) {
	if c.kind == kindSingle {
		if id == c.single {
			return // 值没变，保持单值态
		}
		c.upgradeToIndexed()
	}

	if c.kind == kindIndexed {
		slot, ok := c.lookup[id]
		if !ok {
			if len(c.palette) >= 1<<c.bits {
				if c.bits >= 8 {
					c.upgradeToDirect()
					c.writeRaw(blockIndex(x, y, z), uint32(id))
					return
				}
				c.growIndexed(8)
			}
			slot = uint32(len(c.palette))
			c.palette = append(c.palette, id)
			c.lookup[id] = slot
		}
		c.writeRaw(blockIndex(x, y, z), slot)
		return
	}

	c.writeRaw(blockIndex(x, y, z), uint32(id))
}

// upgradeToIndexed 把单值态升级为 4 位索引态。
func (c *PalettedContainer) upgradeToIndexed() {
	c.kind = kindIndexed
	c.bits = 4
	c.palette = []BlockID{c.single}
	c.lookup = map[BlockID]uint32{c.single: 0}
	c.data = make([]uint64, wordsFor(4))
	// 全 0 即全部指向调色板槽 0，也就是原来的 single，无需再填。
}

// growIndexed 把索引态的位宽扩大到 newBits，重新打包已有数据。
func (c *PalettedContainer) growIndexed(newBits uint8) {
	old := c.data
	oldBits := c.bits
	c.bits = newBits
	c.data = make([]uint64, wordsFor(newBits))

	oldPerWord := 64 / int(oldBits)
	mask := uint64(1)<<oldBits - 1
	for i := 0; i < core.BlocksPerSection; i++ {
		shift := uint((i % oldPerWord) * int(oldBits))
		c.writeRaw(i, uint32((old[i/oldPerWord]>>shift)&mask))
	}
}

// upgradeToDirect 把索引态升级为直接态，槽内改存全局 ID。
func (c *PalettedContainer) upgradeToDirect() {
	pal := c.palette
	oldBits := c.bits
	old := c.data

	c.kind = kindDirect
	c.bits = directBits
	c.data = make([]uint64, wordsFor(directBits))
	c.palette = nil
	c.lookup = nil

	oldPerWord := 64 / int(oldBits)
	mask := uint64(1)<<oldBits - 1
	for i := 0; i < core.BlocksPerSection; i++ {
		shift := uint((i % oldPerWord) * int(oldBits))
		slot := (old[i/oldPerWord] >> shift) & mask
		c.writeRaw(i, uint32(pal[slot]))
	}
}

// Compact 惰性降级：内容重新统一时退回单值态，
// 调色板实际用量缩小时退回更窄的位宽。
//
// 应在 tick 末对本 tick 被修改过的区段调用，而非每次 Set 都调用——
// 否则方块反复变更会在形态之间抖动。
func (c *PalettedContainer) Compact() {
	if c.kind == kindSingle {
		return
	}

	first := c.Get(0, 0, 0)
	uniform := true
	used := make(map[BlockID]struct{}, 16)
	for i := 0; i < core.BlocksPerSection; i++ {
		var id BlockID
		if c.kind == kindDirect {
			id = BlockID(c.readRaw(i))
		} else {
			id = c.palette[c.readRaw(i)]
		}
		used[id] = struct{}{}
		if id != first {
			uniform = false
		}
	}

	if uniform {
		*c = PalettedContainer{kind: kindSingle, single: first}
		return
	}
	if len(used) > 256 {
		return // 仍需直接态
	}

	// 重建为最紧凑的索引态。遍历顺序取自 data 而非 map，保证确定性。
	rebuilt := &PalettedContainer{kind: kindIndexed, lookup: map[BlockID]uint32{}}
	if len(used) <= 16 {
		rebuilt.bits = 4
	} else {
		rebuilt.bits = 8
	}
	rebuilt.data = make([]uint64, wordsFor(rebuilt.bits))
	for i := 0; i < core.BlocksPerSection; i++ {
		var id BlockID
		if c.kind == kindDirect {
			id = BlockID(c.readRaw(i))
		} else {
			id = c.palette[c.readRaw(i)]
		}
		slot, ok := rebuilt.lookup[id]
		if !ok {
			slot = uint32(len(rebuilt.palette))
			rebuilt.palette = append(rebuilt.palette, id)
			rebuilt.lookup[id] = slot
		}
		rebuilt.writeRaw(i, slot)
	}
	*c = *rebuilt
}

// IsUniform 在容器处于单值态时返回该值与 true。
func (c *PalettedContainer) IsUniform() (BlockID, bool) {
	if c.kind == kindSingle {
		return c.single, true
	}
	return 0, false
}

// PayloadBytes 返回压缩 payload 的逻辑大小。
//
// 它不包含 Go 对象头、map bucket、allocator size class 等运行时开销，
// 只能比较压缩率，不能当作进程驻留内存。真实内存由 Task 17 采样 RSS。
func (c *PalettedContainer) PayloadBytes() int {
	n := len(c.data) * 8
	n += len(c.palette) * 2
	n += len(c.lookup) * 8 // map 开销的粗略估计
	return n
}

// Clone 返回一份深拷贝，供 COW 使用（spec §4.3）。
func (c *PalettedContainer) Clone() *PalettedContainer {
	cp := &PalettedContainer{
		kind:   c.kind,
		single: c.single,
		bits:   c.bits,
	}
	if c.data != nil {
		cp.data = make([]uint64, len(c.data))
		copy(cp.data, c.data)
	}
	if c.palette != nil {
		cp.palette = make([]BlockID, len(c.palette))
		copy(cp.palette, c.palette)
		cp.lookup = make(map[BlockID]uint32, len(c.lookup))
		for k, v := range c.lookup {
			cp.lookup[k] = v
		}
	}
	return cp
}
```

- [ ] **Step 4: 运行测试，确认通过**

```bash
go test ./internal/world/ -v
go test ./internal/world/ -bench=. -benchmem
```

预期：三个测试全部 PASS。`BenchmarkPalettedContainerGet` 应在个位数 ns/op 量级、0 allocs/op。

- [ ] **Step 5: 提交**

```bash
git add internal/world/palette.go internal/world/palette_test.go
git commit -m "feat: 三态调色板容器，压缩区段方块存储"
```

---

### Task 7: 区段、区块与网格化邻域

**Files:**
- Create: `internal/world/section.go`
- Create: `internal/world/chunk.go`
- Create: `internal/world/neighborhood.go`
- Create: `internal/world/chunk_test.go`

**Interfaces:**
- Consumes: Task 6 的 `PalettedContainer`、Task 5 的 `core` 坐标
- Produces:

```go
type Section struct{ Blocks *PalettedContainer }
type Chunk   struct{ Pos core.ChunkPos; /* 非导出 */ }

func NewChunk(pos core.ChunkPos) *Chunk
func (c *Chunk) BlockAt(lx int, wy int32, lz int) BlockID   // lx/lz 为区块内 0..15，wy 为世界 Y
func (c *Chunk) SetBlock(lx int, wy int32, lz int, id BlockID)
func (c *Chunk) Section(i int) *Section                      // i 为 0..23
func (c *Chunk) Compact()

type Neighborhood struct {
    Center *Section
    Around [3][3][3]*Section // [dx+1][dy+1][dz+1]，含棱角邻居
}
func (n *Neighborhood) At(x, y, z int) BlockID               // 接受 -1..16
func NeighborhoodAt(get func(core.ChunkPos) *Chunk, pos core.ChunkPos, si int) *Neighborhood
```

- [ ] **Step 1: 写失败的测试**

`internal/world/chunk_test.go`：

```go
package world_test

import (
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

func TestChunkRoundTripAcrossFullHeight(t *testing.T) {
	c := world.NewChunk(core.ChunkPos{X: 3, Z: -7})

	// 世界的最低、最高与中间三层都要能正确读写。
	for _, y := range []int32{core.MinY, core.MinY + 1, 0, 63, core.MaxY - 1} {
		c.SetBlock(5, y, 11, world.BlockID(y-core.MinY+1))
	}
	for _, y := range []int32{core.MinY, core.MinY + 1, 0, 63, core.MaxY - 1} {
		want := world.BlockID(y - core.MinY + 1)
		if got := c.BlockAt(5, y, 11); got != want {
			t.Fatalf("y=%d: BlockAt = %d，想要 %d", y, got, want)
		}
	}
}

func TestChunkStartsAllAir(t *testing.T) {
	c := world.NewChunk(core.ChunkPos{})
	for i := 0; i < core.SectionsPerChunk; i++ {
		s := c.Section(i)
		if id, ok := s.Blocks.IsUniform(); !ok || id != world.AirID {
			t.Fatalf("第 %d 个区段不是全空气的单值态", i)
		}
	}
}

// TestNeighborhoodCrossesSectionBoundary 验证网格化邻域能读到
// -1 与 16 这两个越界坐标，这是面剔除正确性的前提。
func TestNeighborhoodCrossesSectionBoundary(t *testing.T) {
	center := world.NewSection()
	below := world.NewSection()
	below.Blocks.Set(7, 15, 7, world.BlockID(42))

	above := world.NewSection()
	above.Blocks.Set(7, 0, 7, world.BlockID(99))

	n := &world.Neighborhood{Center: center}
	n.Around[1][0][1] = below // -Y
	n.Around[1][2][1] = above // +Y

	if got := n.At(7, -1, 7); got != 42 {
		t.Fatalf("At(7,-1,7) = %d，想要 42（应读到 -Y 邻居的顶层）", got)
	}
	if got := n.At(7, 16, 7); got != 99 {
		t.Fatalf("At(7,16,7) = %d，想要 99（应读到 +Y 邻居的底层）", got)
	}
	if got := n.At(7, 8, 7); got != world.AirID {
		t.Fatalf("At(7,8,7) = %d，想要空气", got)
	}
}

// TestNeighborhoodMissingNeighborIsSolid 验证未加载的邻居按实心处理。
//
// 若按空气处理，未加载边界上会生成一整面永远会被遮住的四边形，
// 等邻居加载后又要全部重做——纯粹的浪费。
func TestNeighborhoodMissingNeighborIsSolid(t *testing.T) {
	n := &world.Neighborhood{Center: world.NewSection()}
	if got := n.At(-1, 5, 5); got != world.BarrierID {
		t.Fatalf("缺失邻居处 At = %d，想要 BarrierID", got)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
go test ./internal/world/ -run 'TestChunk|TestNeighborhood' -v
```

预期：`undefined: world.NewChunk` 等。

- [ ] **Step 3: 实现区段与区块**

`internal/world/section.go`：

```go
package world

// BarrierID 是一个内部专用的实心方块 ID，玩家永远看不到它。
//
// 它用来表示"这里的数据还没加载，但请当作实心"——见 Neighborhood.At。
const BarrierID BlockID = 1

// Section 是一个 16³ 的方块区段。
type Section struct {
	Blocks *PalettedContainer
}

// NewSection 创建一个全空气的区段。
func NewSection() *Section {
	return &Section{Blocks: NewPalettedContainer(AirID)}
}

// Clone 返回深拷贝，供 COW 使用（spec §4.3）。
func (s *Section) Clone() *Section {
	return &Section{Blocks: s.Blocks.Clone()}
}
```

`internal/world/chunk.go`：

```go
package world

import "minecraft-go/internal/core"

// Chunk 是一根 16×16 的世界柱，含 core.SectionsPerChunk 个区段。
type Chunk struct {
	Pos      core.ChunkPos
	sections [core.SectionsPerChunk]*Section
}

// NewChunk 创建一个全空气的区块。
func NewChunk(pos core.ChunkPos) *Chunk {
	c := &Chunk{Pos: pos}
	for i := range c.sections {
		c.sections[i] = NewSection()
	}
	return c
}

// Section 返回第 i 个区段，i 取值 0..core.SectionsPerChunk-1。
func (c *Chunk) Section(i int) *Section { return c.sections[i] }

// sectionIndexOf 把世界 Y 映射为区段索引与区段内局部 Y。
func sectionIndexOf(wy int32) (si, ly int) {
	d := wy - core.MinY
	return int(d >> core.SectionShift), int(d & core.SectionMask)
}

// BlockAt 读取方块。lx/lz 是区块内局部坐标 0..15，wy 是世界 Y。
// wy 超出 [core.MinY, core.MaxY) 时返回空气。
func (c *Chunk) BlockAt(lx int, wy int32, lz int) BlockID {
	if wy < core.MinY || wy >= core.MaxY {
		return AirID
	}
	si, ly := sectionIndexOf(wy)
	return c.sections[si].Blocks.Get(lx, ly, lz)
}

// SetBlock 写入方块。wy 超出世界高度范围时静默忽略。
func (c *Chunk) SetBlock(lx int, wy int32, lz int, id BlockID) {
	if wy < core.MinY || wy >= core.MaxY {
		return
	}
	si, ly := sectionIndexOf(wy)
	c.sections[si].Blocks.Set(lx, ly, lz, id)
}

// Compact 对所有区段做惰性降级，应在一批批量写入之后调用。
func (c *Chunk) Compact() {
	for _, s := range c.sections {
		s.Blocks.Compact()
	}
}
```

- [ ] **Step 4: 实现网格化邻域**

`internal/world/neighborhood.go`：

```go
package world

// Neighborhood 是网格化的输入：一个中心区段加周围 3×3×3 邻域。
//
// Around 的下标是 [dx+1][dy+1][dz+1]。棱角邻居不仅用于面剔除，
// 也用于 AO 在区段边缘的三格采样；缺失会形成永久暗缝。
// 邻居为 nil 表示尚未加载，At 会返回 BarrierID（按实心处理）——
// 这样不会在未加载边界上生成一批注定被遮住、且邻居到位后必须重做的面。
type Neighborhood struct {
	Center *Section
	Around [3][3][3]*Section
}

// At 读取局部坐标处的方块，三个分量各自允许 -1..16。
// 越界分量会映射到 Around 中对应的面、棱或角邻居。
func (n *Neighborhood) At(x, y, z int) BlockID {
	c := [3]int{x, y, z}
	cell := [3]int{1, 1, 1}
	outside := false
	for i, v := range c {
		if v < -1 || v > 16 {
			return BarrierID
		}
		switch v {
		case -1:
			cell[i], c[i], outside = 0, 15, true
		case 16:
			cell[i], c[i], outside = 2, 0, true
		}
	}

	if !outside {
		return n.Center.Blocks.Get(x, y, z)
	}

	nb := n.Around[cell[0]][cell[1]][cell[2]]
	if nb == nil {
		return BarrierID
	}
	return nb.Blocks.Get(c[0], c[1], c[2])
}

// NeighborhoodAt 组装一个区段的网格化邻域。
//
// get 返回给定区块，不存在时返回 nil。Around 会填满所有已加载的
// 面、棱、角邻居；越出世界高度或邻居未加载时留 nil，
// At 会按 BarrierID（实心）处理。
func NeighborhoodAt(get func(core.ChunkPos) *Chunk, pos core.ChunkPos, si int) *Neighborhood {
	self := get(pos)
	if self == nil {
		return nil
	}
	n := &Neighborhood{Center: self.Section(si)}
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			ch := get(core.ChunkPos{X: pos.X + int32(dx), Z: pos.Z + int32(dz)})
			if ch == nil {
				continue
			}
			for dy := -1; dy <= 1; dy++ {
				nsi := si + dy
				if nsi < 0 || nsi >= core.SectionsPerChunk {
					continue
				}
				n.Around[dx+1][dy+1][dz+1] = ch.Section(nsi)
			}
		}
	}
	return n
}
```

- [ ] **Step 5: 补一个邻域组装测试**

`internal/world/chunk_test.go` 追加：

```go
// TestNeighborhoodAtWiresHorizontalNeighbors 验证水平邻居接对了。
//
// 接错方向的表现是区块接缝处多出或缺少一整面，
// 而且只在特定朝向上出现——从画面上极难定位，必须在这里测掉。
func TestNeighborhoodAtWiresHorizontalNeighbors(t *testing.T) {
	chunks := map[core.ChunkPos]*world.Chunk{}
	for dx := int32(-1); dx <= 1; dx++ {
		for dz := int32(-1); dz <= 1; dz++ {
			chunks[core.ChunkPos{X: dx, Z: dz}] = world.NewChunk(core.ChunkPos{X: dx, Z: dz})
		}
	}
	// 在 -X 邻居的东边界、+X 邻居的西边界各放一个可区分的方块。
	chunks[core.ChunkPos{X: -1, Z: 0}].SetBlock(15, 0, 8, world.BlockID(70))
	chunks[core.ChunkPos{X: 1, Z: 0}].SetBlock(0, 0, 8, world.BlockID(71))

	get := func(p core.ChunkPos) *world.Chunk { return chunks[p] }
	n := world.NeighborhoodAt(get, core.ChunkPos{X: 0, Z: 0}, 4) // y=0 落在第 4 个区段

	if got := n.At(-1, 0, 8); got != 70 {
		t.Fatalf("At(-1,0,8) = %d，想要 70（-X 邻居）", got)
	}
	if got := n.At(16, 0, 8); got != 71 {
		t.Fatalf("At(16,0,8) = %d，想要 71（+X 邻居）", got)
	}
}
```

`y=0` 对应的区段索引是 `(0 - (-64)) / 16 = 4`，与测试中的 `si = 4` 一致。

再补 `TestNeighborhoodReadsDiagonalForAO`：在 `(-X,+Y,+Z)` 邻居的对应角放置方块，断言 `n.At(-1,16,16)` 能读到该方块而不是 `BarrierID`。这条测试守住区段边缘 AO 不出现暗缝。

- [ ] **Step 6: 运行测试，确认通过**

```bash
go test ./internal/world/ -v
```

预期：全部 PASS。

- [ ] **Step 7: 提交**

```bash
git add internal/world
git commit -m "feat: 区段、区块与网格化邻域"
```

---

### Task 8: 确定性噪声与高度图地形

**Files:**
- Create: `internal/worldgen/noise.go`
- Create: `internal/worldgen/noise_test.go`
- Create: `internal/worldgen/generator.go`
- Create: `internal/worldgen/generator_test.go`
- Create: `internal/worldgen/testdata/golden_seed42.txt`

**Interfaces:**
- Consumes: Task 7 的 `world.Chunk`、Task 5 的 `core.ChunkPos`
- Produces:

```go
type Generator struct{ /* 非导出 */ }
func New(seed int64) *Generator
func (g *Generator) GenerateChunk(pos core.ChunkPos) *world.Chunk
func (g *Generator) HeightAt(wx, wz int32) int32
```

**噪声自己实现，不用第三方库。** 理由：黄金文件测试要求地形逐字节稳定，而第三方噪声库升级一次就会让所有黄金文件失效、且无法分辨是库变了还是我们的代码坏了。Perlin 噪声是完全确定的经典算法，约 80 行。

- [ ] **Step 1: 写失败的噪声测试**

`internal/worldgen/noise_test.go`：

```go
package worldgen

import (
	"math"
	"testing"
)

// TestPerlinIsDeterministic 同种子必须给出完全相同的结果。
// 这是黄金文件测试与 spec §4.3 确定性要求的基础。
func TestPerlinIsDeterministic(t *testing.T) {
	a := newPerlin(42)
	b := newPerlin(42)
	for i := 0; i < 1000; i++ {
		x := float64(i) * 0.137
		z := float64(i) * 0.911
		if a.at(x, z) != b.at(x, z) {
			t.Fatalf("同种子在 (%f,%f) 处结果不同", x, z)
		}
	}
}

// TestPerlinDiffersBySeed 不同种子必须给出不同地形。
func TestPerlinDiffersBySeed(t *testing.T) {
	a, b := newPerlin(1), newPerlin(2)
	same := 0
	for i := 0; i < 1000; i++ {
		x := float64(i) * 0.137
		if a.at(x, 0.5) == b.at(x, 0.5) {
			same++
		}
	}
	if same > 50 {
		t.Fatalf("两个种子有 %d/1000 个采样点相同，种子未生效", same)
	}
}

// TestPerlinRangeAndZeroAtLattice 检查取值范围与格点性质。
func TestPerlinRangeAndZeroAtLattice(t *testing.T) {
	p := newPerlin(7)
	for i := 0; i < 10000; i++ {
		x := float64(i)*0.0173 - 80
		z := float64(i)*0.0291 - 40
		v := p.at(x, z)
		if v < -1.5 || v > 1.5 {
			t.Fatalf("噪声在 (%f,%f) 处越界: %f", x, z, v)
		}
	}
	// Perlin 噪声在整数格点上恒为 0，这是算法的定义性质。
	for x := -5; x <= 5; x++ {
		for z := -5; z <= 5; z++ {
			if v := p.at(float64(x), float64(z)); math.Abs(v) > 1e-9 {
				t.Fatalf("格点 (%d,%d) 处噪声 = %f，应为 0", x, z, v)
			}
		}
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
go test ./internal/worldgen/ -v
```

预期：`undefined: newPerlin`。

- [ ] **Step 3: 实现 Perlin 噪声**

`internal/worldgen/noise.go`：

```go
// Package worldgen 生成地形。
//
// 本包必须是确定性的：同种子 + 同区块坐标 = 完全相同的输出（spec §4.3）。
// 因此包内禁止使用 map 遍历顺序、time、以及未播种的随机源。
package worldgen

import (
	"math"
	"math/rand"
)

// perlin 是经典 2D Perlin 噪声。
//
// 自己实现而非用第三方库：黄金文件测试要求地形逐字节稳定，
// 而库升级会让所有黄金文件失效且无法区分是库变了还是我们坏了。
type perlin struct {
	// perm 是 0..255 的一个置换重复两遍，共 512 项。
	// 重复是为了让 perm[a+b] 在 a,b 各自 ≤255 时无需取模。
	perm [512]int
}

// newPerlin 用给定种子构造噪声。
func newPerlin(seed int64) *perlin {
	var p perlin
	base := make([]int, 256)
	for i := range base {
		base[i] = i
	}
	rng := rand.New(rand.NewSource(seed))
	rng.Shuffle(256, func(i, j int) { base[i], base[j] = base[j], base[i] })
	for i := 0; i < 512; i++ {
		p.perm[i] = base[i&255]
	}
	return &p
}

// fade 是 Perlin 的六次插值曲线 6t⁵-15t⁴+10t³，
// 保证一阶与二阶导数在格点处连续，避免可见的方格接缝。
func fade(t float64) float64 { return t * t * t * (t*(t*6-15) + 10) }

func lerp(a, b, t float64) float64 { return a + t*(b-a) }

// grad2 从哈希值取一个 2D 梯度方向并与偏移做点积。
func grad2(h int, x, y float64) float64 {
	switch h & 3 {
	case 0:
		return x + y
	case 1:
		return -x + y
	case 2:
		return x - y
	default:
		return -x - y
	}
}

// at 返回 (x,z) 处的噪声值，大致落在 [-1, 1]。
func (p *perlin) at(x, z float64) float64 {
	fx, fz := math.Floor(x), math.Floor(z)
	xi, zi := int(fx)&255, int(fz)&255
	xf, zf := x-fx, z-fz
	u, v := fade(xf), fade(zf)

	aa := p.perm[p.perm[xi]+zi]
	ab := p.perm[p.perm[xi]+zi+1]
	ba := p.perm[p.perm[xi+1]+zi]
	bb := p.perm[p.perm[xi+1]+zi+1]

	x1 := lerp(grad2(aa, xf, zf), grad2(ba, xf-1, zf), u)
	x2 := lerp(grad2(ab, xf, zf-1), grad2(bb, xf-1, zf-1), u)
	return lerp(x1, x2, v)
}

// fbm 是分形布朗运动：叠加多个倍频的噪声，得到有大尺度起伏
// 也有小尺度细节的地形。返回值归一化到大致 [-1, 1]。
func (p *perlin) fbm(x, z float64, octaves int, lacunarity, gain float64) float64 {
	var sum, amp, norm float64 = 0, 1, 0
	freq := 1.0
	for i := 0; i < octaves; i++ {
		sum += p.at(x*freq, z*freq) * amp
		norm += amp
		freq *= lacunarity
		amp *= gain
	}
	return sum / norm
}
```

- [ ] **Step 4: 运行噪声测试，确认通过**

```bash
go test ./internal/worldgen/ -run TestPerlin -v
```

预期：三个测试全部 PASS。

- [ ] **Step 5: 写地形生成的黄金测试**

`internal/worldgen/generator_test.go`：

```go
package worldgen_test

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
	"minecraft-go/internal/worldgen"
)

var update = flag.Bool("update", false, "重写黄金文件")

// TestGenerateChunkGolden 用哈希钉死地形输出。
//
// 地形算法的任何改动都会让这个测试变红——这是刻意的。
// 确认改动是有意的之后，用 go test ./internal/worldgen/ -update 重写黄金文件。
func TestGenerateChunkGolden(t *testing.T) {
	g := worldgen.New(42)

	var b strings.Builder
	for _, pos := range []core.ChunkPos{
		{X: 0, Z: 0}, {X: 1, Z: 0}, {X: -1, Z: -1}, {X: 37, Z: -104},
	} {
		c := g.GenerateChunk(pos)
		h := sha256.New()
		for y := core.MinY; y < core.MaxY; y++ {
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					id := c.BlockAt(x, y, z)
					h.Write([]byte{byte(id), byte(id >> 8)})
				}
			}
		}
		fmt.Fprintf(&b, "chunk(%d,%d) %s\n", pos.X, pos.Z, hex.EncodeToString(h.Sum(nil)))
	}
	got := b.String()

	golden := filepath.Join("testdata", "golden_seed42.txt")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Log("黄金文件已重写")
		return
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("读黄金文件失败（首次运行请加 -update）: %v", err)
	}
	if got != string(want) {
		t.Fatalf("地形输出已改变\n实际:\n%s\n期望:\n%s", got, want)
	}
}

// TestGenerateChunkIsDeterministic 同种子同坐标必须完全一致。
func TestGenerateChunkIsDeterministic(t *testing.T) {
	pos := core.ChunkPos{X: 5, Z: -3}
	a := worldgen.New(1234).GenerateChunk(pos)
	b := worldgen.New(1234).GenerateChunk(pos)
	for y := core.MinY; y < core.MaxY; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				if a.BlockAt(x, y, z) != b.BlockAt(x, y, z) {
					t.Fatalf("(%d,%d,%d) 处两次生成结果不同", x, y, z)
				}
			}
		}
	}
}

// TestGenerateChunkIsSeamlessAcrossBorders 验证相邻区块边界处高度连续。
//
// 地形生成若按区块局部坐标而非世界坐标采样噪声，就会在每个区块边界
// 出现明显的断崖。这个测试专门抓这个 bug。
func TestGenerateChunkIsSeamlessAcrossBorders(t *testing.T) {
	g := worldgen.New(99)
	for wz := int32(-40); wz < 40; wz++ {
		h0 := g.HeightAt(15, wz)
		h1 := g.HeightAt(16, wz)
		if d := h0 - h1; d > 4 || d < -4 {
			t.Fatalf("区块边界 x=15/16, z=%d 处高度突变 %d", wz, d)
		}
	}
}

// TestGeneratedChunkCompresses 验证生成的区块确实受益于调色板压缩。
// 这是 spec §4.1 内存预算成立的实证。
func TestGeneratedChunkCompresses(t *testing.T) {
	c := worldgen.New(7).GenerateChunk(core.ChunkPos{X: 0, Z: 0})
	c.Compact()

	total := 0
	uniform := 0
	for i := 0; i < core.SectionsPerChunk; i++ {
		s := c.Section(i)
		total += s.Blocks.PayloadBytes()
		if _, ok := s.Blocks.IsUniform(); ok {
			uniform++
		}
	}
	// 24 个区段里，地表只占几个，其余应全是空气或全是石头。
	if uniform < 15 {
		t.Fatalf("只有 %d/24 个区段是单值态，压缩效果不及预期", uniform)
	}
	// 朴素存储是 24*4096*2 = 196608 字节。
	if total > 40000 {
		t.Fatalf("单区块 payload 估算 %d 字节，朴素 payload 为 196608，压缩比不达标", total)
	}
}
```

- [ ] **Step 6: 运行测试，确认失败**

```bash
go test ./internal/worldgen/ -run TestGenerate -v
```

预期：`undefined: worldgen.New`。

- [ ] **Step 7: 实现地形生成**

`internal/worldgen/generator.go`：

```go
package worldgen

import (
	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

// M1 用到的方块 ID。完整的方块注册表在 M4 建立（spec §6.3）。
const (
	IDStone   world.BlockID = 2
	IDDirt    world.BlockID = 3
	IDGrass   world.BlockID = 4
	IDBedrock world.BlockID = 5
)

// 地形参数。M1 只做高度图地形——洞穴、矿脉、生物群系属于 M4。
const (
	seaLevel      = 64
	terrainAmp    = 48.0   // 起伏幅度（方块）
	terrainScale  = 1.0 / 256.0 // 噪声采样频率：值越小地形越平缓
	octaves       = 5
	lacunarity    = 2.0
	gain          = 0.5
	soilDepth     = 4 // 草皮下的泥土层厚度
)

// Generator 按种子生成地形。
//
// 无内部可变状态，可并发调用（worker pool 会这么用，见 Task 17）。
type Generator struct {
	noise *perlin
}

// New 创建一个地形生成器。
func New(seed int64) *Generator {
	return &Generator{noise: newPerlin(seed)}
}

// HeightAt 返回世界坐标 (wx,wz) 处最高实心方块的 Y。
//
// 噪声按世界坐标采样，而非区块局部坐标——否则每个区块边界都会断崖。
func (g *Generator) HeightAt(wx, wz int32) int32 {
	n := g.noise.fbm(float64(wx)*terrainScale, float64(wz)*terrainScale,
		octaves, lacunarity, gain)
	return int32(seaLevel + n*terrainAmp)
}

// GenerateChunk 生成一个完整区块。
func (g *Generator) GenerateChunk(pos core.ChunkPos) *world.Chunk {
	c := world.NewChunk(pos)
	baseX := pos.X << core.SectionShift
	baseZ := pos.Z << core.SectionShift

	for lz := 0; lz < 16; lz++ {
		for lx := 0; lx < 16; lx++ {
			h := g.HeightAt(baseX+int32(lx), baseZ+int32(lz))
			if h >= core.MaxY {
				h = core.MaxY - 1
			}
			for y := int32(core.MinY); y <= h; y++ {
				var id world.BlockID
				switch {
				case y == core.MinY:
					id = IDBedrock
				case y == h:
					id = IDGrass
				case y > h-soilDepth:
					id = IDDirt
				default:
					id = IDStone
				}
				c.SetBlock(lx, y, lz, id)
			}
		}
	}
	c.Compact()
	return c
}
```

- [ ] **Step 8: 生成黄金文件并跑全部测试**

```bash
go test ./internal/worldgen/ -run TestGenerateChunkGolden -update
go test ./internal/worldgen/ -v
```

预期：`-update` 生成 `testdata/golden_seed42.txt`，随后全部测试 PASS。

若 `TestGeneratedChunkCompresses` 失败，说明 `Compact` 有问题或地形层次划分让区段过于杂乱——回到 Task 6 检查，不要靠放宽阈值蒙混过关。

- [ ] **Step 9: 提交**

```bash
git add internal/worldgen
git commit -m "feat: 确定性 Perlin 噪声与高度图地形生成"
```

---

### Task 9: 贪心网格化

**Files:**
- Create: `internal/mesh/quad.go`
- Create: `internal/mesh/quad_test.go`
- Create: `internal/mesh/greedy.go`
- Create: `internal/mesh/greedy_test.go`

**关于包位置的说明：** spec §3 的目录树把网格化归在 `render/` 下。这里把它单独拆成 `internal/mesh`，因为网格化是一个**对世界数据的纯函数**（区段快照进、四边形数组出），不碰 GPU。拆出来它就能脱离渲染独立测试与压测，而 `render/` 保持只做 GPU 编排。依赖方向 `mesh → world, core` 与 spec §3.1 一致。

**Interfaces:**
- Consumes: Task 7 的 `world.Neighborhood`
- Produces:

```go
type Face uint8
const (FaceNegX Face = iota; FacePosX; FaceNegY; FacePosY; FaceNegZ; FacePosZ)

type Quad struct {
    X, Y, Z uint8  // 区段内起点，0..15
    W, H    uint8  // 沿面内 u/v 轴的尺寸，1..16
    Face    Face
    Mat     uint16 // texture array 层号
    AO      uint8  // 4 个角各 2 位
    Light   uint8  // 高 4 位天空光，低 4 位方块光
}

func (q Quad) Pack() uint64
func UnpackQuad(v uint64) Quad

type Registry interface {
    Opaque(world.BlockID) bool
    Material(id world.BlockID, f Face) uint16
}

func MeshSection(n *world.Neighborhood, reg Registry) []Quad
```

- [ ] **Step 1: 写打包的往返测试**

`internal/mesh/quad_test.go`：

```go
package mesh_test

import (
	"math/rand"
	"testing"

	"minecraft-go/internal/mesh"
)

// TestQuadPackRoundTrip 验证 8 字节打包不丢信息。
//
// 打包布局是 GPU 实例数据的契约（spec §5.1），错一位就是满屏错乱，
// 且从画面上极难反推。用随机往返把它钉死。
func TestQuadPackRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 100000; i++ {
		want := mesh.Quad{
			X:     uint8(rng.Intn(16)),
			Y:     uint8(rng.Intn(16)),
			Z:     uint8(rng.Intn(16)),
			W:     uint8(rng.Intn(16) + 1),
			H:     uint8(rng.Intn(16) + 1),
			Face:  mesh.Face(rng.Intn(6)),
			Mat:   uint16(rng.Intn(65536)),
			AO:    uint8(rng.Intn(256)),
			Light: uint8(rng.Intn(256)),
		}
		if got := mesh.UnpackQuad(want.Pack()); got != want {
			t.Fatalf("往返不一致:\n实际 %+v\n期望 %+v", got, want)
		}
	}
}

// TestQuadPackFitsIn55Bits 验证打包留有余量，未来加字段不必改布局。
func TestQuadPackFitsIn55Bits(t *testing.T) {
	full := mesh.Quad{
		X: 15, Y: 15, Z: 15, W: 16, H: 16,
		Face: 5, Mat: 0xFFFF, AO: 0xFF, Light: 0xFF,
	}
	if v := full.Pack(); v>>55 != 0 {
		t.Fatalf("打包用满了高位: %#016x，第 55 位以上应为空", v)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
go test ./internal/mesh/ -v
```

预期：`undefined: mesh.Quad`。

- [ ] **Step 3: 实现 Quad 与打包**

`internal/mesh/quad.go`：

```go
// Package mesh 把区段方块数据转换成 GPU 实例数据。
//
// 本包是纯函数：区段快照进、四边形数组出，不碰 GPU，
// 因此可以完全用 go test 覆盖与压测。
package mesh

// Face 是方块的 6 个面之一。编号规则：axis = Face>>1（0=X,1=Y,2=Z），
// 正方向 = Face&1 == 1。着色器依赖这个编码，改动需同步 WGSL。
type Face uint8

const (
	FaceNegX Face = iota
	FacePosX
	FaceNegY
	FacePosY
	FaceNegZ
	FacePosZ
)

// Axis 返回该面的法线所在轴：0=X, 1=Y, 2=Z。
func (f Face) Axis() int { return int(f) >> 1 }

// Positive 返回该面法线是否指向轴的正方向。
func (f Face) Positive() bool { return f&1 == 1 }

// Quad 是一个贪心合并后的矩形面，也是 GPU 的一条实例数据（spec §5.1）。
type Quad struct {
	X, Y, Z uint8  // 区段内起点，各 0..15
	W, H    uint8  // 沿面内 u/v 轴的尺寸，各 1..16
	Face    Face
	Mat     uint16 // texture array 的层号
	AO      uint8  // 4 个角各 2 位，0 最暗、3 最亮
	Light   uint8  // 高 4 位天空光，低 4 位方块光
}

// 打包位布局。W/H 存 W-1、H-1，因为取值是 1..16 而只有 4 位。
const (
	shiftX     = 0
	shiftY     = 4
	shiftZ     = 8
	shiftW     = 12
	shiftH     = 16
	shiftFace  = 20
	shiftMat   = 23
	shiftAO    = 39
	shiftLight = 47
	// 最高占用到第 54 位，55 位及以上留给未来字段。
)

// Pack 把四边形压成 8 字节，供 GPU 实例缓冲直接使用。
func (q Quad) Pack() uint64 {
	return uint64(q.X)<<shiftX |
		uint64(q.Y)<<shiftY |
		uint64(q.Z)<<shiftZ |
		uint64(q.W-1)<<shiftW |
		uint64(q.H-1)<<shiftH |
		uint64(q.Face)<<shiftFace |
		uint64(q.Mat)<<shiftMat |
		uint64(q.AO)<<shiftAO |
		uint64(q.Light)<<shiftLight
}

// UnpackQuad 是 Pack 的逆运算，仅用于测试与调试。
func UnpackQuad(v uint64) Quad {
	return Quad{
		X:     uint8(v>>shiftX) & 0xF,
		Y:     uint8(v>>shiftY) & 0xF,
		Z:     uint8(v>>shiftZ) & 0xF,
		W:     uint8(v>>shiftW)&0xF + 1,
		H:     uint8(v>>shiftH)&0xF + 1,
		Face:  Face(uint8(v>>shiftFace) & 0x7),
		Mat:   uint16(v >> shiftMat),
		AO:    uint8(v >> shiftAO),
		Light: uint8(v >> shiftLight),
	}
}
```

- [ ] **Step 4: 运行打包测试，确认通过**

```bash
go test ./internal/mesh/ -run TestQuad -v
```

预期：两个测试 PASS。

- [ ] **Step 5: 写贪心网格化的测试**

`internal/mesh/greedy_test.go`：

```go
package mesh_test

import (
	"testing"

	"minecraft-go/internal/mesh"
	"minecraft-go/internal/world"
)

// testRegistry 是测试用的最小方块注册表：空气透明，其余不透明。
type testRegistry struct{}

func (testRegistry) Opaque(id world.BlockID) bool { return id != world.AirID }
func (testRegistry) Material(id world.BlockID, f mesh.Face) uint16 {
	return uint16(id)
}

// solidNeighbors 返回一个周围 26 个邻居都是实心的邻域，
// 这样中心区段与外界之间不会产生面，测试只观察内部结构。
func solidNeighbors(center *world.Section) *world.Neighborhood {
	solid := world.NewSection()
	for y := 0; y < 16; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				solid.Blocks.Set(x, y, z, world.BlockID(2))
			}
		}
	}
	n := &world.Neighborhood{Center: center}
	for dx := 0; dx < 3; dx++ {
		for dy := 0; dy < 3; dy++ {
			for dz := 0; dz < 3; dz++ {
				if dx == 1 && dy == 1 && dz == 1 {
					continue
				}
				n.Around[dx][dy][dz] = solid
			}
		}
	}
	return n
}

func TestMeshEmptySectionProducesNothing(t *testing.T) {
	n := solidNeighbors(world.NewSection())
	if q := mesh.MeshSection(n, testRegistry{}); len(q) != 0 {
		t.Fatalf("全空气区段产生了 %d 个面，应为 0", len(q))
	}
}

func TestMeshFullSectionProducesNothing(t *testing.T) {
	center := world.NewSection()
	for y := 0; y < 16; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				center.Blocks.Set(x, y, z, world.BlockID(2))
			}
		}
	}
	n := solidNeighbors(center)
	if q := mesh.MeshSection(n, testRegistry{}); len(q) != 0 {
		t.Fatalf("被实心邻居包围的实心区段产生了 %d 个面，应为 0", len(q))
	}
}

func TestMeshSingleBlockProducesSixUnitQuads(t *testing.T) {
	center := world.NewSection()
	center.Blocks.Set(8, 8, 8, world.BlockID(2))
	n := solidNeighbors(center)

	quads := mesh.MeshSection(n, testRegistry{})
	if len(quads) != 6 {
		t.Fatalf("孤立方块产生了 %d 个面，应为 6", len(quads))
	}
	seen := map[mesh.Face]bool{}
	for _, q := range quads {
		if q.W != 1 || q.H != 1 {
			t.Fatalf("孤立方块的面尺寸 = %dx%d，应为 1x1", q.W, q.H)
		}
		if seen[q.Face] {
			t.Fatalf("面 %d 重复出现", q.Face)
		}
		seen[q.Face] = true
	}
	if len(seen) != 6 {
		t.Fatalf("只覆盖了 %d 个朝向，应为 6", len(seen))
	}
}

// TestMeshGreedyMergesFlatSurface 是贪心合并的核心验证。
//
// 一整层 16×16 的石头、上方是空气，顶面必须合并成 1 个 16×16 的四边形，
// 而不是 256 个 1×1。若这个测试过不了，贪心逻辑就是坏的，
// 后面所有性能目标都无从谈起。
func TestMeshGreedyMergesFlatSurface(t *testing.T) {
	center := world.NewSection()
	for y := 0; y < 8; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				center.Blocks.Set(x, y, z, world.BlockID(2))
			}
		}
	}
	n := solidNeighbors(center)

	quads := mesh.MeshSection(n, testRegistry{})
	if len(quads) != 1 {
		t.Fatalf("平坦顶面产生了 %d 个面，贪心合并后应为 1", len(quads))
	}
	q := quads[0]
	if q.Face != mesh.FacePosY {
		t.Fatalf("面朝向 = %d，应为 FacePosY", q.Face)
	}
	if q.W != 16 || q.H != 16 {
		t.Fatalf("面尺寸 = %dx%d，应为 16x16", q.W, q.H)
	}
	if q.Y != 7 {
		t.Fatalf("面所在层 Y = %d，应为 7（最高一层石头）", q.Y)
	}
}

// TestMeshDoesNotMergeAcrossMaterials 验证不同材质不会被错误合并。
func TestMeshDoesNotMergeAcrossMaterials(t *testing.T) {
	center := world.NewSection()
	for z := 0; z < 16; z++ {
		for x := 0; x < 16; x++ {
			id := world.BlockID(2)
			if x >= 8 {
				id = world.BlockID(3)
			}
			center.Blocks.Set(x, 0, z, id)
		}
	}
	n := solidNeighbors(center)

	quads := mesh.MeshSection(n, testRegistry{})
	if len(quads) != 2 {
		t.Fatalf("两种材质的平面产生了 %d 个面，应为 2", len(quads))
	}
	for _, q := range quads {
		if q.W != 8 || q.H != 16 {
			t.Fatalf("面尺寸 = %dx%d，应为 8x16", q.W, q.H)
		}
	}
}

func BenchmarkMeshTerrainSection(b *testing.B) {
	// 造一个半填充的区段，接近真实地表的形态。
	center := world.NewSection()
	for z := 0; z < 16; z++ {
		for x := 0; x < 16; x++ {
			h := 4 + (x*3+z*5)%8
			for y := 0; y <= h; y++ {
				center.Blocks.Set(x, y, z, world.BlockID(2+(x+z)%3))
			}
		}
	}
	n := solidNeighbors(center)
	reg := testRegistry{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = mesh.MeshSection(n, reg)
	}
}
```

- [ ] **Step 6: 运行测试，确认失败**

```bash
go test ./internal/mesh/ -run TestMesh -v
```

预期：`undefined: mesh.MeshSection`。

- [ ] **Step 7: 实现贪心网格化**

`internal/mesh/greedy.go`：

```go
package mesh

import "minecraft-go/internal/world"

// Registry 提供网格化需要的方块属性。
// 由调用方实现，避免 mesh 包依赖完整的方块注册表。
type Registry interface {
	// Opaque 返回该方块是否完全不透明（会遮挡相邻面）。
	Opaque(world.BlockID) bool
	// Material 返回该方块某个面在 texture array 中的层号。
	Material(id world.BlockID, f Face) uint16
}

// maskCell 是切片掩膜中的一格。必须是可比较类型——
// 贪心合并靠 == 判断两格能否合并。
type maskCell struct {
	used  bool
	mat   uint16
	ao    uint8
	light uint8
}

// MeshSection 把一个区段转换成贪心合并后的四边形集合。
//
// 算法：对 6 个朝向各自处理；每个朝向沿法线轴切成 16 个平面，
// 每个平面建一张 16×16 的掩膜（记录哪里有可见面及其外观），
// 然后在掩膜上贪心地切出最大矩形。
//
// 外观完全相同的相邻格才会合并——材质、AO、光照任一不同都会分开，
// 这是必要的，否则会出现光照或 AO 被抹平的可见错误。
func MeshSection(n *world.Neighborhood, reg Registry) []Quad {
	// 预分配：真实地表区段通常产出几十到几百个面。
	out := make([]Quad, 0, 256)

	for face := Face(0); face < 6; face++ {
		axis := face.Axis()
		// 面内的两个轴。取 (axis+1)%3 与 (axis+2)%3，
		// 保证 (axis, u, v) 构成右手系，着色器按同样规则还原顶点。
		u := (axis + 1) % 3
		v := (axis + 2) % 3

		step := -1
		if face.Positive() {
			step = 1
		}

		for slice := 0; slice < 16; slice++ {
			var mask [16][16]maskCell
			any := false

			for vi := 0; vi < 16; vi++ {
				for ui := 0; ui < 16; ui++ {
					var p [3]int
					p[axis], p[u], p[v] = slice, ui, vi

					id := n.Center.Blocks.Get(p[0], p[1], p[2])
					if !reg.Opaque(id) {
						continue
					}
					// 法线方向上的邻居若也不透明，这个面看不见。
					q := p
					q[axis] += step
					if reg.Opaque(n.At(q[0], q[1], q[2])) {
						continue
					}
					mask[vi][ui] = maskCell{
						used:  true,
						mat:   reg.Material(id, face),
						ao:    computeAO(n, reg, p, axis, u, v, step),
						light: 0xF0, // M1 全亮；真实光照传播在 M4
					}
					any = true
				}
			}
			if !any {
				continue
			}

			// 贪心切矩形：先沿 u 扩到最宽，再沿 v 整行整行地扩高。
			for vi := 0; vi < 16; vi++ {
				for ui := 0; ui < 16; {
					c := mask[vi][ui]
					if !c.used {
						ui++
						continue
					}

					w := 1
					for ui+w < 16 && mask[vi][ui+w] == c {
						w++
					}

					h := 1
				grow:
					for vi+h < 16 {
						for k := 0; k < w; k++ {
							if mask[vi+h][ui+k] != c {
								break grow
							}
						}
						h++
					}

					for dv := 0; dv < h; dv++ {
						for du := 0; du < w; du++ {
							mask[vi+dv][ui+du] = maskCell{}
						}
					}

					var p [3]int
					p[axis], p[u], p[v] = slice, ui, vi
					out = append(out, Quad{
						X: uint8(p[0]), Y: uint8(p[1]), Z: uint8(p[2]),
						W: uint8(w), H: uint8(h),
						Face: face, Mat: c.mat, AO: c.ao, Light: c.light,
					})
					ui += w
				}
			}
		}
	}
	return out
}

// computeAO 计算一个面 4 个角的环境光遮蔽，每角 2 位，0 最暗、3 最亮。
//
// 角的遮蔽程度取决于面外那一层里，与该角相邻的 3 个方块
// （两条边 + 一个对角）。两条边都实心时该角完全被夹住，取 0。
func computeAO(n *world.Neighborhood, reg Registry, p [3]int, axis, u, v, step int) uint8 {
	base := p
	base[axis] += step

	solid := func(du, dv int) int {
		q := base
		q[u] += du
		q[v] += dv
		if reg.Opaque(n.At(q[0], q[1], q[2])) {
			return 1
		}
		return 0
	}

	// 4 个角在 (u,v) 平面上的方向，顺序与着色器还原顶点的顺序一致。
	corners := [4][2]int{{-1, -1}, {1, -1}, {1, 1}, {-1, 1}}
	var out uint8
	for i, c := range corners {
		s1 := solid(c[0], 0)
		s2 := solid(0, c[1])
		level := 0
		if s1 != 1 || s2 != 1 {
			level = 3 - (s1 + s2 + solid(c[0], c[1]))
		}
		out |= uint8(level) << (i * 2)
	}
	return out
}
```

- [ ] **Step 8: 运行全部 mesh 测试与基准**

```bash
go test ./internal/mesh/ -v
go test ./internal/mesh/ -bench=. -benchmem
```

预期：全部 PASS。`BenchmarkMeshTerrainSection` 记下基线数字——Task 17 的性能门禁会用到。

- [ ] **Step 9: 提交**

```bash
git add internal/mesh
git commit -m "feat: 贪心网格化，输出 8 字节定长实例"
```

---

### Task 10: 区块可见性图

**Files:**
- Create: `internal/mesh/visibility.go`
- Create: `internal/mesh/visibility_test.go`

**关于包位置：** 可见性图放在 `internal/mesh` 而非 `internal/world`，因为它需要 `Face` 与 `Registry`（都定义在 `mesh`），而 `world` 不能反向依赖 `mesh`。spec §5.2 也把它归在渲染剔除下——它是渲染侧的预计算加速结构，不是世界状态。

**Interfaces:**
- Consumes: Task 9 的 `Face`、`Registry`；Task 7 的 `world.Section`
- Produces:

```go
type Connectivity uint16
func ComputeConnectivity(s *world.Section, reg Registry) Connectivity
func (c Connectivity) Connected(a, b Face) bool
func VisibleSections(origin core.SectionPos, radius int, frustum core.Frustum,
    lookup func(core.SectionPos) (Connectivity, bool)) []core.SectionPos
```

这是三级剔除里收益最大的一级（spec §5.2 ①），且完全在 CPU 上、几乎免费。

- [ ] **Step 1: 写失败的测试**

`internal/mesh/visibility_test.go`：

```go
package mesh_test

import (
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/mesh"
	"minecraft-go/internal/world"
)

func allFacePairs(t *testing.T, c mesh.Connectivity, want bool) {
	t.Helper()
	for a := mesh.Face(0); a < 6; a++ {
		for b := a + 1; b < 6; b++ {
			if got := c.Connected(a, b); got != want {
				t.Fatalf("Connected(%d,%d) = %v，想要 %v", a, b, got, want)
			}
		}
	}
}

func TestConnectivityEmptySectionIsFullyConnected(t *testing.T) {
	allFacePairs(t, mesh.ComputeConnectivity(world.NewSection(), testRegistry{}), true)
}

func TestConnectivitySolidSectionIsFullyBlocked(t *testing.T) {
	s := world.NewSection()
	for y := 0; y < 16; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				s.Blocks.Set(x, y, z, world.BlockID(2))
			}
		}
	}
	allFacePairs(t, mesh.ComputeConnectivity(s, testRegistry{}), false)
}

// TestConnectivityTunnelConnectsOnlyItsAxis 是本任务的核心用例：
// 一条沿 X 轴的隧道只应让 -X 与 +X 连通，其余 14 对都不通。
func TestConnectivityTunnelConnectsOnlyItsAxis(t *testing.T) {
	s := world.NewSection()
	for y := 0; y < 16; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				s.Blocks.Set(x, y, z, world.BlockID(2))
			}
		}
	}
	// 在 y=8, z=8 挖穿一条隧道。
	for x := 0; x < 16; x++ {
		s.Blocks.Set(x, 8, 8, world.AirID)
	}

	c := mesh.ComputeConnectivity(s, testRegistry{})
	for a := mesh.Face(0); a < 6; a++ {
		for b := a + 1; b < 6; b++ {
			want := a == mesh.FaceNegX && b == mesh.FacePosX
			if got := c.Connected(a, b); got != want {
				t.Fatalf("Connected(%d,%d) = %v，想要 %v", a, b, got, want)
			}
		}
	}
}

// TestVisibleSectionsStopsAtSolidWall 验证 BFS 会被实心区段挡住。
func TestVisibleSectionsStopsAtSolidWall(t *testing.T) {
	open := mesh.ComputeConnectivity(world.NewSection(), testRegistry{})

	solidSec := world.NewSection()
	for y := 0; y < 16; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				solidSec.Blocks.Set(x, y, z, world.BlockID(2))
			}
		}
	}
	solid := mesh.ComputeConnectivity(solidSec, testRegistry{})

	origin := core.SectionPos{X: 0, Y: 4, Z: 0}
	// X=2 处立一堵不透明的墙。
	lookup := func(p core.SectionPos) (mesh.Connectivity, bool) {
		if p.X == 2 {
			return solid, true
		}
		return open, true
	}

	// 用一个能容纳全部候选的宽视锥，隔离出可见性图本身的效果。
	got := mesh.VisibleSections(origin, 5, mesh.EverythingVisible(), lookup)

	seen := map[core.SectionPos]bool{}
	for _, p := range got {
		seen[p] = true
	}
	if !seen[core.SectionPos{X: 2, Y: 4, Z: 0}] {
		t.Fatal("墙本身应当可见")
	}
	if seen[core.SectionPos{X: 3, Y: 4, Z: 0}] {
		t.Fatal("墙后的区段应被可见性图剔除")
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
go test ./internal/mesh/ -run 'TestConnectivity|TestVisible' -v
```

预期：`undefined: mesh.ComputeConnectivity`。

- [ ] **Step 3: 实现连通性计算**

`internal/mesh/visibility.go`：

```go
package mesh

import "minecraft-go/internal/core"

// Connectivity 记录一个区段的 6 个面之间哪些两两可达，共 15 对，占 15 位。
//
// 用途：从相机出发做 BFS 时，只有"从进入面能走到出去面"的区段
// 才继续向外扩展。地下与被山体遮挡的区段因此根本不进入候选集
// （spec §5.2 ①）。
type Connectivity uint16

// pairBit 返回面对 (a,b) 在位掩码中的位号，要求 a < b。
// 6 个面共 15 对，按 (0,1),(0,2)...(0,5),(1,2)...(4,5) 顺序排列。
func pairBit(a, b Face) uint {
	// 前 a 行累计的对数：sum_{i<a}(5-i)
	return uint(a)*5 - uint(a)*(uint(a)-1)/2 + uint(b) - uint(a) - 1
}

// Connected 返回两个面之间是否可达。a == b 时返回 true。
func (c Connectivity) Connected(a, b Face) bool {
	if a == b {
		return true
	}
	if a > b {
		a, b = b, a
	}
	return c&(1<<pairBit(a, b)) != 0
}

// faceOf 返回局部坐标 (x,y,z) 贴在哪些面上（一个格可能同时贴多个面）。
func faceOf(x, y, z int, add func(Face)) {
	if x == 0 {
		add(FaceNegX)
	}
	if x == 15 {
		add(FacePosX)
	}
	if y == 0 {
		add(FaceNegY)
	}
	if y == 15 {
		add(FacePosY)
	}
	if z == 0 {
		add(FaceNegZ)
	}
	if z == 15 {
		add(FacePosZ)
	}
}

// ComputeConnectivity 用洪水填充算出区段的面连通性。
//
// 对每个尚未访问的透明格做一次 BFS，记录这个连通分量触及了哪些面；
// 该分量触及的任意两个面即互相可达。
func ComputeConnectivity(s *world.Section, reg Registry) Connectivity {
	var visited [core.BlocksPerSection]bool
	var out Connectivity

	// 复用同一个队列，避免每个分量都重新分配。
	queue := make([]int32, 0, core.BlocksPerSection)

	at := func(i int32) (x, y, z int) {
		return int(i & 15), int((i >> 8) & 15), int((i >> 4) & 15)
	}
	idxOf := func(x, y, z int) int32 { return int32(y<<8 | z<<4 | x) }

	for start := int32(0); start < core.BlocksPerSection; start++ {
		if visited[start] {
			continue
		}
		sx, sy, sz := at(start)
		if reg.Opaque(s.Blocks.Get(sx, sy, sz)) {
			visited[start] = true
			continue
		}

		var touched uint8
		queue = queue[:0]
		queue = append(queue, start)
		visited[start] = true

		for len(queue) > 0 {
			cur := queue[len(queue)-1]
			queue = queue[:len(queue)-1]
			x, y, z := at(cur)
			faceOf(x, y, z, func(f Face) { touched |= 1 << f })

			for _, d := range [6][3]int{
				{-1, 0, 0}, {1, 0, 0}, {0, -1, 0}, {0, 1, 0}, {0, 0, -1}, {0, 0, 1},
			} {
				nx, ny, nz := x+d[0], y+d[1], z+d[2]
				if nx < 0 || nx > 15 || ny < 0 || ny > 15 || nz < 0 || nz > 15 {
					continue
				}
				ni := idxOf(nx, ny, nz)
				if visited[ni] {
					continue
				}
				visited[ni] = true
				if reg.Opaque(s.Blocks.Get(nx, ny, nz)) {
					continue
				}
				queue = append(queue, ni)
			}
		}

		// 该分量触及的面两两互通。
		for a := Face(0); a < 6; a++ {
			if touched&(1<<a) == 0 {
				continue
			}
			for b := a + 1; b < 6; b++ {
				if touched&(1<<b) != 0 {
					out |= 1 << pairBit(a, b)
				}
			}
		}
	}
	return out
}
```

**注意**：`ComputeConnectivity` 需要 import `minecraft-go/internal/world`，与 `greedy.go` 相同。

- [ ] **Step 4: 实现 BFS 遍历**

同文件追加：

```go
// opposite 返回一个面的对面。
func opposite(f Face) Face { return f ^ 1 }

// stepOf 返回穿过某个面时的区段坐标偏移。
func stepOf(f Face) (dx, dy, dz int32) {
	d := int32(1)
	if !f.Positive() {
		d = -1
	}
	switch f.Axis() {
	case 0:
		return d, 0, 0
	case 1:
		return 0, d, 0
	default:
		return 0, 0, d
	}
}

// EverythingVisible 返回一个不剔除任何东西的视锥，供测试隔离可见性图的效果。
func EverythingVisible() core.Frustum {
	var f core.Frustum
	// 所有平面法线为零、距离为正 —— IntersectsAABB 的点积恒为正，永不剔除。
	for i := range f {
		f[i] = [4]float32{0, 0, 0, 1}
	}
	return f
}

// VisibleSections 从相机所在区段做广度优先遍历，返回可见候选区段。
//
// 每个区段带着"从哪个面进来的"这一信息入队；只有当该区段的连通性
// 允许从进入面走到某个出去面时，才继续从那个面向外扩展。
// 起点区段视为从所有面都能出去。
//
// lookup 返回 (连通性, 是否已加载)。未加载的区段不扩展但仍计入结果，
// 这样它一旦加载就能立刻显示。
func VisibleSections(
	origin core.SectionPos,
	radius int,
	frustum core.Frustum,
	lookup func(core.SectionPos) (Connectivity, bool),
) []core.SectionPos {
	type node struct {
		pos   core.SectionPos
		entry Face
		// isOrigin 为 true 时忽略 entry，可从任意面出去。
		isOrigin bool
	}

	out := make([]core.SectionPos, 0, 512)
	// emitted 保证每个区段在结果中只出现一次。
	emitted := make(map[core.SectionPos]bool, 1024)
	// enqueued 记录每个区段已经从哪些面进入过。
	// 同一区段从不同面进入会看到不同的可达出口，所以要按面去重，
	// 而不是按区段去重——按区段去重会漏掉本该可见的区域。
	enqueued := make(map[core.SectionPos]uint8, 1024)

	queue := []node{{pos: origin, isOrigin: true}}
	emitted[origin] = true
	out = append(out, origin)

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		conn, loaded := lookup(cur.pos)
		if !loaded {
			continue // 未加载的区段已计入结果，但无连通性信息可供扩展
		}

		for exit := Face(0); exit < 6; exit++ {
			if !cur.isOrigin && !conn.Connected(opposite(cur.entry), exit) {
				continue
			}
			dx, dy, dz := stepOf(exit)
			np := core.SectionPos{X: cur.pos.X + dx, Y: cur.pos.Y + dy, Z: cur.pos.Z + dz}

			if np.Y < 0 || np.Y >= core.SectionsPerChunk {
				continue
			}
			if abs32(np.X-origin.X) > int32(radius) || abs32(np.Z-origin.Z) > int32(radius) {
				continue
			}
			if !frustum.IntersectsAABB(sectionAABB(np)) {
				continue
			}

			if !emitted[np] {
				emitted[np] = true
				out = append(out, np)
			}

			bit := uint8(1) << exit
			if enqueued[np]&bit != 0 {
				continue
			}
			enqueued[np] |= bit
			queue = append(queue, node{pos: np, entry: exit})
		}
	}
	return out
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// sectionAABB 返回区段在世界空间的包围盒。
func sectionAABB(p core.SectionPos) core.AABB {
	c := p.MinCorner()
	return core.AABB{
		Min: [3]float32{float32(c.X), float32(c.Y), float32(c.Z)},
		Max: [3]float32{float32(c.X + 16), float32(c.Y + 16), float32(c.Z + 16)},
	}
}
```

- [ ] **Step 5: 补一个去重测试**

`internal/mesh/visibility_test.go` 追加：

```go
func TestVisibleSectionsHasNoDuplicates(t *testing.T) {
	open := mesh.ComputeConnectivity(world.NewSection(), testRegistry{})
	lookup := func(core.SectionPos) (mesh.Connectivity, bool) { return open, true }

	got := mesh.VisibleSections(core.SectionPos{X: 0, Y: 4, Z: 0}, 3,
		mesh.EverythingVisible(), lookup)

	seen := map[core.SectionPos]int{}
	for _, p := range got {
		seen[p]++
		if seen[p] > 1 {
			t.Fatalf("区段 %+v 在结果中出现 %d 次", p, seen[p])
		}
	}
}
```

- [ ] **Step 6: 运行测试，确认通过**

```bash
go test ./internal/mesh/ -v
```

预期：全部 PASS，包括去重测试。

- [ ] **Step 7: 提交**

```bash
git add internal/mesh/visibility.go internal/mesh/visibility_test.go
git commit -m "feat: 区段可见性图与 BFS 遍历，最廉价的一级剔除"
```

---

### Task 11: 程序化占位材质与 texture array

**Files:**
- Create: `internal/assets/blocks.go`
- Create: `internal/assets/procedural.go`
- Create: `internal/assets/procedural_test.go`
- Create: `internal/assets/atlas.go`
- Create: `internal/assets/atlas_test.go`

**Interfaces:**
- Consumes: Task 2 的 `gfx.Device`、`gfx.Texture`
- Produces:

```go
type Material uint16
type Registry struct{ /* 非导出 */ }

func NewRegistry() *Registry
func (r *Registry) Opaque(id world.BlockID) bool
func (r *Registry) Material(id world.BlockID, f mesh.Face) uint16
func (r *Registry) LayerCount() int
func (r *Registry) LayerRGBA(layer int) []byte   // 16×16×4 字节
func (r *Registry) UploadTo(dev gfx.Device) (gfx.Texture, gfx.Sampler)
```

**仓库内不得有任何二进制美术资源**（Global Constraints）。所有材质由代码生成。

- [ ] **Step 1: 写失败的测试**

`internal/assets/procedural_test.go`：

```go
package assets_test

import (
	"testing"

	"minecraft-go/internal/assets"
	"minecraft-go/internal/mesh"
	"minecraft-go/internal/world"
	"minecraft-go/internal/worldgen"
)

func TestRegistryAirIsTransparent(t *testing.T) {
	r := assets.NewRegistry()
	if r.Opaque(world.AirID) {
		t.Fatal("空气不应是不透明的")
	}
	if !r.Opaque(worldgen.IDStone) {
		t.Fatal("石头应是不透明的")
	}
}

// TestGrassHasDistinctTopAndSide 验证同一方块的不同面可以用不同材质，
// 这是草方块（顶绿、侧棕绿、底棕）的基本要求。
func TestGrassHasDistinctTopAndSide(t *testing.T) {
	r := assets.NewRegistry()
	top := r.Material(worldgen.IDGrass, mesh.FacePosY)
	side := r.Material(worldgen.IDGrass, mesh.FaceNegX)
	bottom := r.Material(worldgen.IDGrass, mesh.FaceNegY)

	if top == side {
		t.Fatal("草方块的顶面与侧面材质相同")
	}
	if bottom == top {
		t.Fatal("草方块的底面与顶面材质相同")
	}
	if bottom != r.Material(worldgen.IDDirt, mesh.FacePosY) {
		t.Fatal("草方块底面应复用泥土材质")
	}
}

// TestEveryLayerIsFullSize 验证每层材质都是完整的 16×16 RGBA。
// 尺寸不一致会让 texture array 上传直接失败（spec §5.4）。
func TestEveryLayerIsFullSize(t *testing.T) {
	r := assets.NewRegistry()
	if r.LayerCount() == 0 {
		t.Fatal("材质层数为 0")
	}
	for i := 0; i < r.LayerCount(); i++ {
		px := r.LayerRGBA(i)
		if len(px) != 16*16*4 {
			t.Fatalf("第 %d 层大小 = %d 字节，想要 %d", i, len(px), 16*16*4)
		}
	}
}

// TestProceduralTexturesAreDeterministic 同一层每次生成必须逐字节相同，
// 否则联机时不同客户端会看到不同的贴图噪点。
func TestProceduralTexturesAreDeterministic(t *testing.T) {
	a, b := assets.NewRegistry(), assets.NewRegistry()
	for i := 0; i < a.LayerCount(); i++ {
		pa, pb := a.LayerRGBA(i), b.LayerRGBA(i)
		for j := range pa {
			if pa[j] != pb[j] {
				t.Fatalf("第 %d 层第 %d 字节不一致: %d vs %d", i, j, pa[j], pb[j])
			}
		}
	}
}

// TestTexturesAreNotFlat 验证生成的材质有像素级变化，
// 否则场景会是一片纯色，看不出地形结构。
func TestTexturesAreNotFlat(t *testing.T) {
	r := assets.NewRegistry()
	for i := 0; i < r.LayerCount(); i++ {
		px := r.LayerRGBA(i)
		distinct := map[[3]byte]struct{}{}
		for j := 0; j < len(px); j += 4 {
			distinct[[3]byte{px[j], px[j+1], px[j+2]}] = struct{}{}
		}
		if len(distinct) < 4 {
			t.Fatalf("第 %d 层只有 %d 种颜色，材质太平", i, len(distinct))
		}
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
go test ./internal/assets/ -v
```

预期：`undefined: assets.NewRegistry`。

- [ ] **Step 3: 实现程序化材质生成**

`internal/assets/procedural.go`：

```go
// Package assets 提供方块注册表与材质。
//
// M1 只有代码生成的占位材质。标准资源包（.zip）加载器在 M4 加入，
// 届时用户材质优先于占位材质（spec §6.3）。
// 仓库内不得提交任何二进制美术资源。
package assets

const texSize = 16

// rgb 是一个不带 alpha 的颜色。
type rgb struct{ R, G, B uint8 }

// hash2 是一个确定性的整数哈希，用来给像素加噪点。
//
// 不用 math/rand：材质必须逐字节可复现，否则联机时各客户端贴图不一致。
func hash2(x, y, salt uint32) uint32 {
	h := x*374761393 + y*668265263 + salt*2246822519
	h = (h ^ (h >> 13)) * 1274126177
	return h ^ (h >> 16)
}

// noisyTexture 生成一张在基色附近抖动的 16×16 RGBA 材质。
// spread 控制噪点强度（0 为纯色）。
func noisyTexture(base rgb, spread int32, salt uint32) []byte {
	px := make([]byte, texSize*texSize*4)
	for y := 0; y < texSize; y++ {
		for x := 0; x < texSize; x++ {
			n := int32(hash2(uint32(x), uint32(y), salt)%uint32(2*spread+1)) - spread
			i := (y*texSize + x) * 4
			px[i+0] = clamp8(int32(base.R) + n)
			px[i+1] = clamp8(int32(base.G) + n)
			px[i+2] = clamp8(int32(base.B) + n)
			px[i+3] = 255
		}
	}
	return px
}

// grassTopTexture 在噪点基础上再撒一些更亮的草叶像素。
func grassTopTexture() []byte {
	px := noisyTexture(rgb{R: 88, G: 140, B: 60}, 14, 0x9E37)
	for y := 0; y < texSize; y++ {
		for x := 0; x < texSize; x++ {
			if hash2(uint32(x), uint32(y), 0x51ED)%5 != 0 {
				continue
			}
			i := (y*texSize + x) * 4
			px[i+1] = clamp8(int32(px[i+1]) + 30)
		}
	}
	return px
}

// grassSideTexture 是上部一条草、下部是泥土的侧面材质。
func grassSideTexture() []byte {
	px := noisyTexture(rgb{R: 134, G: 96, B: 67}, 12, 0x1B87)
	for y := 0; y < 4; y++ {
		for x := 0; x < texSize; x++ {
			n := int32(hash2(uint32(x), uint32(y), 0x77C1)%25) - 12
			i := (y*texSize + x) * 4
			px[i+0] = clamp8(88 + n)
			px[i+1] = clamp8(140 + n)
			px[i+2] = clamp8(60 + n)
		}
	}
	return px
}

func clamp8(v int32) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}
```

- [ ] **Step 4: 实现方块注册表**

`internal/assets/blocks.go`：

```go
package assets

import (
	"minecraft-go/internal/mesh"
	"minecraft-go/internal/world"
	"minecraft-go/internal/worldgen"
)

// 材质层号。这些常量同时是 texture array 的层索引。
const (
	LayerStone uint16 = iota
	LayerDirt
	LayerGrassTop
	LayerGrassSide
	LayerBedrock
	layerCount
)

// Registry 是方块属性与材质的注册表。
//
// 方块定义写在 Go 代码里而非数据文件（spec §6.3）：方块行为本就是代码，
// 拆成"数据 + 代码"只会制造两处都要改的同步负担。
type Registry struct {
	layers [layerCount][]byte
}

// NewRegistry 构造注册表并生成全部占位材质。
func NewRegistry() *Registry {
	r := &Registry{}
	r.layers[LayerStone] = noisyTexture(rgb{R: 128, G: 128, B: 128}, 18, 0x2545)
	r.layers[LayerDirt] = noisyTexture(rgb{R: 134, G: 96, B: 67}, 12, 0x1B87)
	r.layers[LayerGrassTop] = grassTopTexture()
	r.layers[LayerGrassSide] = grassSideTexture()
	r.layers[LayerBedrock] = noisyTexture(rgb{R: 60, G: 60, B: 64}, 28, 0x3F19)
	return r
}

// Opaque 返回方块是否完全不透明。实现 mesh.Registry。
func (r *Registry) Opaque(id world.BlockID) bool {
	return id != world.AirID
}

// Material 返回方块某个面的材质层号。实现 mesh.Registry。
func (r *Registry) Material(id world.BlockID, f mesh.Face) uint16 {
	switch id {
	case worldgen.IDStone:
		return LayerStone
	case worldgen.IDDirt:
		return LayerDirt
	case worldgen.IDBedrock:
		return LayerBedrock
	case worldgen.IDGrass:
		switch f {
		case mesh.FacePosY:
			return LayerGrassTop
		case mesh.FaceNegY:
			return LayerDirt
		default:
			return LayerGrassSide
		}
	default:
		// 未知方块用石头顶替，避免渲染出黑块而看不出问题在哪。
		return LayerStone
	}
}

// LayerCount 返回材质层数。
func (r *Registry) LayerCount() int { return int(layerCount) }

// LayerRGBA 返回第 layer 层的 16×16 RGBA 像素。
func (r *Registry) LayerRGBA(layer int) []byte { return r.layers[layer] }
```

编译期断言放在文件末尾：

```go
var _ mesh.Registry = (*Registry)(nil)
```

- [ ] **Step 5: 实现 texture array 上传**

`internal/assets/atlas.go`：

```go
package assets

import "minecraft-go/internal/gfx"

// UploadTo 把全部材质层建成一张 2D 数组纹理并生成 mipmap。
//
// 用 texture array 而非图集：图集方案的 mipmap 边缘渗色与
// 各向异性过滤问题在体素世界里特别刺眼（spec §5.4）。
// 代价是所有材质必须同分辨率——对 16×16 像素风格不是约束。
func (r *Registry) UploadTo(dev gfx.Device) (gfx.Texture, gfx.Sampler) {
	// 16×16 共有 5 级 mip：16,8,4,2,1
	const mips = 5

	tex := dev.CreateTexture(gfx.TextureDesc{
		Label:     "block-textures",
		Width:     texSize,
		Height:    texSize,
		Layers:    uint32(r.LayerCount()),
		MipLevels: mips,
		Format:    gfx.FormatRGBA8Unorm,
		Dimension: gfx.TextureDimension2DArray,
		Usage:     gfx.TextureUsageBinding | gfx.TextureUsageCopyDst,
	})

	for layer := 0; layer < r.LayerCount(); layer++ {
		px := r.LayerRGBA(layer)
		size := texSize
		tex.WriteLayer(uint32(layer), 0, px)
		// 在 CPU 上做 box filter 降采样。层数少、只在启动时做一次，
		// 没必要为此建一条 GPU mipmap 生成管线。
		for mip := 1; mip < mips; mip++ {
			px = downsample(px, size)
			size /= 2
			tex.WriteLayer(uint32(layer), uint32(mip), px)
		}
	}

	smp := dev.CreateSampler(gfx.SamplerDesc{
		Label: "block-sampler",
		// 放大用最近邻——像素风格必须保持硬边，线性会糊。
		MagFilter: gfx.FilterNearest,
		// 缩小与 mip 之间用线性，消除远处的摩尔纹。
		MinFilter: gfx.FilterLinear,
		MipFilter: gfx.FilterLinear,
	})
	return tex, smp
}

// downsample 把 size×size 的 RGBA 图做 2×2 平均，降为一半尺寸。
func downsample(src []byte, size int) []byte {
	half := size / 2
	dst := make([]byte, half*half*4)
	for y := 0; y < half; y++ {
		for x := 0; x < half; x++ {
			for c := 0; c < 4; c++ {
				sum := int(src[((y*2)*size+x*2)*4+c]) +
					int(src[((y*2)*size+x*2+1)*4+c]) +
					int(src[((y*2+1)*size+x*2)*4+c]) +
					int(src[((y*2+1)*size+x*2+1)*4+c])
				dst[(y*half+x)*4+c] = byte(sum / 4)
			}
		}
	}
	return dst
}
```

后续 terrain pipeline 的 atlas 布局必须写成 `Type: BindingSampledTextureFloat, ViewDimension: TextureViewDimension2DArray`；不能依赖后端从具体 TextureView 猜布局维度。

- [ ] **Step 6: 补 downsample 测试并跑全部测试**

`internal/assets/atlas_test.go` 使用同包测试，直接覆盖未导出的纯函数：

```go
package assets

import "testing"

func TestDownsampleHalvesSizeAndAveragesRGBA(t *testing.T) {
	// 2×2 RGBA，四个像素各通道平均应为 (40,50,60,70)。
	src := []byte{
		10, 20, 30, 40, 30, 40, 50, 60,
		50, 60, 70, 80, 70, 80, 90, 100,
	}
	got := downsample(src, 2)
	want := []byte{40, 50, 60, 70}
	if len(got) != len(want) {
		t.Fatalf("2×2 降采样长度 = %d，想要 %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("通道 %d = %d，想要 %d", i, got[i], want[i])
		}
	}
}

func TestDownsampleMipChainEndsAtOnePixel(t *testing.T) {
	px := make([]byte, 16*16*4)
	size := 16
	for size > 1 {
		px = downsample(px, size)
		size /= 2
		if len(px) != size*size*4 {
			t.Fatalf("mip %dx%d 长度 = %d", size, size, len(px))
		}
	}
}
```

```bash
go test ./internal/assets/ -v
```

预期：全部 PASS。

- [ ] **Step 7: 提交**

```bash
git add internal/assets
git commit -m "feat: 程序化占位材质与 texture array 上传"
```

---

### Task 12: 显存池分配器与上传限流

**Files:**
- Create: `internal/render/pool.go`
- Create: `internal/render/pool_test.go`

分配器本身是**纯 Go 数据结构**，不碰 GPU，可以完整用 `go test` 覆盖。碎片和分配 bug 在 GPU 上表现为随机的画面撕裂，几乎无法调试——所以先在 CPU 上把它测干净。

**Interfaces:**
- Consumes: 无（纯逻辑）
- Produces:

```go
type Alloc struct{ Offset, Size uint32 }   // 单位：面数，非字节
type Pool struct{ /* 非导出 */ }

func NewPool(capacity uint32) *Pool
func (p *Pool) Alloc(faces uint32) (Alloc, bool)
func (p *Pool) Free(a Alloc)
func (p *Pool) Used() uint32
func (p *Pool) Fragmentation() float32      // 空闲块数 / 总空闲量的粗略指标
func (p *Pool) LargestFree() uint32

type UploadBudget struct{ /* 非导出 */ }
func NewUploadBudget(bytesPerFrame uint32) *UploadBudget
func (b *UploadBudget) BeginFrame()
func (b *UploadBudget) TryConsume(bytes uint32) bool
```

- [ ] **Step 1: 写失败的测试**

`internal/render/pool_test.go`：

```go
package render_test

import (
	"math/rand"
	"testing"

	"minecraft-go/internal/render"
)

func TestPoolAllocAndFree(t *testing.T) {
	p := render.NewPool(1000)

	a, ok := p.Alloc(300)
	if !ok || a.Size != 300 {
		t.Fatalf("首次分配失败: %+v ok=%v", a, ok)
	}
	b, ok := p.Alloc(300)
	if !ok || b.Offset == a.Offset {
		t.Fatalf("第二次分配与第一次重叠: a=%+v b=%+v", a, b)
	}
	if p.Used() != 600 {
		t.Fatalf("Used = %d，想要 600", p.Used())
	}

	p.Free(a)
	if p.Used() != 300 {
		t.Fatalf("释放后 Used = %d，想要 300", p.Used())
	}
}

func TestPoolRejectsOversizedRequest(t *testing.T) {
	p := render.NewPool(100)
	if _, ok := p.Alloc(101); ok {
		t.Fatal("超出容量的请求应当失败")
	}
}

// TestPoolCoalescesAdjacentFreeBlocks 是分配器最关键的性质。
//
// 不合并相邻空闲块的分配器会在几分钟的飞行后把显存切成碎片，
// 表现为「明明还有一半空闲却分配不出一个中等区块」。
func TestPoolCoalescesAdjacentFreeBlocks(t *testing.T) {
	p := render.NewPool(300)
	a, _ := p.Alloc(100)
	b, _ := p.Alloc(100)
	c, _ := p.Alloc(100)

	p.Free(a)
	p.Free(c)
	p.Free(b) // 释放中间块后，三块应合并成一整块

	if got := p.LargestFree(); got != 300 {
		t.Fatalf("合并后最大空闲块 = %d，想要 300", got)
	}
	if _, ok := p.Alloc(300); !ok {
		t.Fatal("合并后应能一次分配出整个池")
	}
}

// TestPoolRandomChurnNeverOverlaps 用随机分配/释放序列
// 验证任何两个存活分配都不重叠。
func TestPoolRandomChurnNeverOverlaps(t *testing.T) {
	const capacity = 4096
	p := render.NewPool(capacity)
	rng := rand.New(rand.NewSource(7))
	live := map[uint32]render.Alloc{}

	for step := 0; step < 50000; step++ {
		if len(live) > 0 && rng.Intn(2) == 0 {
			for k, v := range live {
				p.Free(v)
				delete(live, k)
				break
			}
			continue
		}
		size := uint32(rng.Intn(64) + 1)
		a, ok := p.Alloc(size)
		if !ok {
			continue
		}
		if a.Offset+a.Size > capacity {
			t.Fatalf("分配越界: %+v，容量 %d", a, capacity)
		}
		for _, other := range live {
			if a.Offset < other.Offset+other.Size && other.Offset < a.Offset+a.Size {
				t.Fatalf("分配重叠: %+v 与 %+v", a, other)
			}
		}
		live[a.Offset] = a
	}
}

func TestUploadBudgetLimitsPerFrame(t *testing.T) {
	b := render.NewUploadBudget(1000)

	b.BeginFrame()
	if !b.TryConsume(600) {
		t.Fatal("600 应在 1000 的预算内")
	}
	if !b.TryConsume(400) {
		t.Fatal("累计 1000 应恰好在预算内")
	}
	if b.TryConsume(1) {
		t.Fatal("超出预算后应拒绝")
	}

	b.BeginFrame()
	if !b.TryConsume(1000) {
		t.Fatal("新一帧预算应重置")
	}
}

// TestUploadBudgetAllowsOversizedSingleItem 验证单个超预算的上传
// 不会被永久饿死——否则一个特别复杂的区段将永远无法上传。
func TestUploadBudgetAllowsOversizedSingleItem(t *testing.T) {
	b := render.NewUploadBudget(100)
	b.BeginFrame()
	if !b.TryConsume(5000) {
		t.Fatal("一帧内第一个请求即使超预算也应放行")
	}
	if b.TryConsume(1) {
		t.Fatal("放行超预算请求后，本帧不应再接受任何上传")
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
go test ./internal/render/ -v
```

预期：`undefined: render.NewPool`。

- [ ] **Step 3: 实现分配器**

`internal/render/pool.go`：

```go
// Package render 负责 GPU 渲染编排。
//
// 本包只通过 internal/gfx 接触 GPU，不得 import 任何 WebGPU 绑定
// （见 Global Constraints）。
package render

import "sort"

// Alloc 是显存池中的一段。单位是「面数」而非字节——
// 每个面固定 8 字节（spec §5.1），用面数计数可读性更好且不会算错。
type Alloc struct{ Offset, Size uint32 }

// freeBlock 是一段空闲区间。
type freeBlock struct{ offset, size uint32 }

// Pool 是持久映射显存缓冲的分配器（spec §5.3）。
//
// 用带合并的自由链表，采用 best-fit：选能装下请求的最小空闲块，
// 尽量把大块留给后面的大区段。first-fit 会更快，但在这种
// 「大量中等尺寸分配 + 长时间运行」的负载下碎片明显更严重。
//
// 非并发安全：只在渲染线程使用。
type Pool struct {
	capacity uint32
	free     []freeBlock // 按 offset 升序，保证相邻块可合并
	used     uint32
}

// NewPool 创建一个容量为 capacity 个面的池。
func NewPool(capacity uint32) *Pool {
	return &Pool{
		capacity: capacity,
		free:     []freeBlock{{offset: 0, size: capacity}},
	}
}

// Alloc 分配 faces 个面的空间。空间不足时返回 ok=false。
func (p *Pool) Alloc(faces uint32) (Alloc, bool) {
	if faces == 0 || faces > p.capacity {
		return Alloc{}, false
	}

	best := -1
	for i, b := range p.free {
		if b.size < faces {
			continue
		}
		if best < 0 || b.size < p.free[best].size {
			best = i
		}
	}
	if best < 0 {
		return Alloc{}, false
	}

	b := p.free[best]
	a := Alloc{Offset: b.offset, Size: faces}
	if b.size == faces {
		p.free = append(p.free[:best], p.free[best+1:]...)
	} else {
		p.free[best] = freeBlock{offset: b.offset + faces, size: b.size - faces}
	}
	p.used += faces
	return a, true
}

// Free 归还一段空间，并与相邻空闲块合并。
//
// 合并是必须的：不合并的话，长时间飞行后池会碎成大量小块，
// 出现「还有一半空闲却分配不出一个区段」的情况。
func (p *Pool) Free(a Alloc) {
	if a.Size == 0 {
		return
	}
	p.used -= a.Size

	i := sort.Search(len(p.free), func(i int) bool {
		return p.free[i].offset >= a.Offset
	})
	p.free = append(p.free, freeBlock{})
	copy(p.free[i+1:], p.free[i:])
	p.free[i] = freeBlock{offset: a.Offset, size: a.Size}

	// 先与后一块合并，再与前一块合并——顺序反过来会漏掉三块连续的情况。
	if i+1 < len(p.free) && p.free[i].offset+p.free[i].size == p.free[i+1].offset {
		p.free[i].size += p.free[i+1].size
		p.free = append(p.free[:i+1], p.free[i+2:]...)
	}
	if i > 0 && p.free[i-1].offset+p.free[i-1].size == p.free[i].offset {
		p.free[i-1].size += p.free[i].size
		p.free = append(p.free[:i], p.free[i+1:]...)
	}
}

// Used 返回已分配的面数。
func (p *Pool) Used() uint32 { return p.used }

// LargestFree 返回最大的连续空闲块大小。
func (p *Pool) LargestFree() uint32 {
	var max uint32
	for _, b := range p.free {
		if b.size > max {
			max = b.size
		}
	}
	return max
}

// Fragmentation 返回碎片化程度：1 - 最大空闲块 / 总空闲量。
// 0 表示所有空闲空间连成一片，接近 1 表示碎得很厉害。
// 超过阈值时应触发增量整理（M5 的优化专项）。
func (p *Pool) Fragmentation() float32 {
	total := p.capacity - p.used
	if total == 0 {
		return 0
	}
	return 1 - float32(p.LargestFree())/float32(total)
}
```

- [ ] **Step 4: 实现上传限流**

同包 `internal/render/pool.go` 追加：

```go
// UploadBudget 限制每帧写入 GPU 的字节数（spec §5.3）。
//
// 玩家高速移动时会有大批区块同时完成网格化，不限流的话
// 一帧内几十 MB 的上传会造成肉眼可见的卡顿。
// 宁可让远处网格晚几帧出现，也不掉帧。
type UploadBudget struct {
	perFrame  uint32
	spent     uint32
	exhausted bool
}

// NewUploadBudget 创建一个每帧 bytesPerFrame 字节的预算。
func NewUploadBudget(bytesPerFrame uint32) *UploadBudget {
	return &UploadBudget{perFrame: bytesPerFrame}
}

// BeginFrame 重置本帧预算。
func (b *UploadBudget) BeginFrame() {
	b.spent = 0
	b.exhausted = false
}

// TryConsume 申请上传 bytes 字节，超出本帧预算时返回 false。
//
// 单次请求即使超过整帧预算也会被放行一次，之后本帧不再接受任何上传——
// 否则一个特别复杂的区段（面数远超平均）将永远无法上传。
func (b *UploadBudget) TryConsume(bytes uint32) bool {
	if b.exhausted {
		return false
	}
	if b.spent+bytes > b.perFrame {
		if b.spent > 0 {
			return false
		}
		// 本帧还没上传过任何东西，放行这个大家伙。
		b.exhausted = true
		b.spent = bytes
		return true
	}
	b.spent += bytes
	return true
}
```

- [ ] **Step 5: 运行测试，确认通过**

```bash
go test ./internal/render/ -v
```

预期：全部 PASS，尤其是 `TestPoolRandomChurnNeverOverlaps` 与 `TestPoolCoalescesAdjacentFreeBlocks`。

- [ ] **Step 6: 提交**

```bash
git add internal/render/pool.go internal/render/pool_test.go
git commit -m "feat: 显存池分配器与每帧上传限流"
```

---

### Task 13: 第一帧画面——单次 indirect draw

**Files:**
- Create: `internal/render/renderer.go`
- Create: `internal/render/shader/terrain.wgsl`
- Modify: `internal/gfx/gfx.go`（给 `SamplerDesc` 加 `AddressMode`）
- Modify: `internal/assets/atlas.go`（采样器改为 Repeat）
- Modify: `cmd/gfxspike/main.go`（换成渲染真实地形）

**Interfaces:**
- Consumes: Task 12 的 `Pool`/`UploadBudget`、Task 11 的 `Registry`、Task 9 的 `Quad`、Task 2 的 `gfx`
- Produces:

```go
type Renderer struct{ /* 非导出 */ }
func New(dev gfx.Device, reg *assets.Registry, colorFmt gfx.TextureFormat) *Renderer
func (r *Renderer) BeginFrame()
func (r *Renderer) QueueSection(p core.SectionPos, quads []mesh.Quad) // 同位置新结果覆盖旧结果
func (r *Renderer) FlushUploads(center core.ChunkPos)                 // 按近到远消耗本帧预算
func (r *Renderer) PendingUploads() int                               // 测试与诊断
func (r *Renderer) DropSection(p core.SectionPos)
func (r *Renderer) DropOutside(center core.ChunkPos, radius int)
func (r *Renderer) Render(enc gfx.CommandEncoder, target, depth gfx.TextureView, cam Camera)

type Camera struct {
    ViewProj mgl32.Mat4
    Pos      mgl32.Vec3
}
```

本任务**先不做剔除**：CPU 把所有已上传区段的面写进紧凑实例缓冲，并直接填 `instanceCount`。这样先拿到画面，再在 Task 14 把这一步搬到 GPU 上。

- [ ] **Step 1: 给 SamplerDesc 加寻址模式**

贪心合并出的四边形要靠 UV 平铺来重复材质（一个 16×16 的面 UV 从 0 到 16），因此采样器必须是 Repeat。Task 2 的 `SamplerDesc` 漏了这个字段，在此补上。

`internal/gfx/gfx.go` 修改：

```go
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
```

`internal/assets/atlas.go` 中的 `CreateSampler` 调用加上 `Address: gfx.AddressRepeat`。

- [ ] **Step 2: 写地形着色器**

`internal/render/shader/terrain.wgsl`。**位布局必须与 `internal/mesh/quad.go` 的 `Pack` 完全一致**，改一处必须改另一处。

```wgsl
struct Camera {
    view_proj: mat4x4f,
    cam_pos:   vec4f,
};

// 紧凑实例：x=packed_lo, y=packed_hi, z=区段索引, w=保留。
// 源顶点池里每个面只占 8 字节；这里的 16 字节只用于「可见」的面，
// 数量小得多，多出来的 8 字节换来了区段索引，是划算的。
@group(0) @binding(0) var<uniform>              camera:    Camera;
@group(0) @binding(1) var<storage, read>        instances: array<vec4u>;
@group(0) @binding(2) var<storage, read>        origins:   array<vec4i>;
@group(0) @binding(3) var                       atlas:     texture_2d_array<f32>;
@group(0) @binding(4) var                       atlas_smp: sampler;

struct VsOut {
    @builtin(position) clip:  vec4f,
    @location(0)       uv:    vec2f,
    @location(1)       layer: f32,
    @location(2)       shade: f32,
};

// axis_vec 返回轴的单位向量。axis: 0=X, 1=Y, 2=Z。
fn axis_vec(axis: u32) -> vec3f {
    if (axis == 0u) { return vec3f(1.0, 0.0, 0.0); }
    if (axis == 1u) { return vec3f(0.0, 1.0, 0.0); }
    return vec3f(0.0, 0.0, 1.0);
}

// 各朝向的固定明暗，让没有真实光照时也能看出地形立体感。
// 顶面最亮、底面最暗，与太阳在上方的直觉一致。
fn face_shade(face: u32) -> f32 {
    switch face {
        case 3u: { return 1.00; }  // +Y 顶
        case 2u: { return 0.50; }  // -Y 底
        case 0u, 1u: { return 0.68; } // ±X
        default: { return 0.84; }  // ±Z
    }
}

@vertex
fn vs_main(
    @builtin(vertex_index)   vi: u32,
    @builtin(instance_index) ii: u32,
) -> VsOut {
    let inst = instances[ii];
    let lo = inst.x;
    let hi = inst.y;

    // 解包。位布局见 internal/mesh/quad.go 的 shift* 常量。
    let x     = f32( lo        & 0xFu);
    let y     = f32((lo >>  4u) & 0xFu);
    let z     = f32((lo >>  8u) & 0xFu);
    let w     = f32(((lo >> 12u) & 0xFu) + 1u);
    let h     = f32(((lo >> 16u) & 0xFu) + 1u);
    let face  =      (lo >> 20u) & 0x7u;
    // mat 跨 32 位边界：lo 的高 9 位 + hi 的低 7 位。
    let mat   = ((lo >> 23u) & 0x1FFu) | ((hi & 0x7Fu) << 9u);
    let ao    = (hi >>  7u) & 0xFFu;
    let light = (hi >> 15u) & 0xFFu;

    let axis = face >> 1u;
    let positive = f32(face & 1u);
    let ua = (axis + 1u) % 3u;
    let va = (axis + 2u) % 3u;

    // 四边形的 4 个角，顺序必须与 mesh.computeAO 的 corners 一致。
    var cu = array<f32, 4>(0.0, 1.0, 1.0, 0.0);
    var cv = array<f32, 4>(0.0, 0.0, 1.0, 1.0);

    let local = vec3f(x, y, z)
        + axis_vec(axis) * positive
        + axis_vec(ua) * (cu[vi] * w)
        + axis_vec(va) * (cv[vi] * h);

    let o = origins[inst.z];
    let world = vec3f(o.xyz) + local;

    // 每角的 AO 各 2 位，0 最暗、3 最亮。
    let ao_level = f32((ao >> (vi * 2u)) & 0x3u);
    let ao_factor = 0.55 + 0.45 * (ao_level / 3.0);
    // M1 只有天空光（高 4 位），方块光在 M4 接入。
    let sky = f32((light >> 4u) & 0xFu) / 15.0;

    var out: VsOut;
    out.clip  = camera.view_proj * vec4f(world, 1.0);
    // UV 直接用面内尺寸，配合 Repeat 采样器让材质按方块平铺。
    out.uv    = vec2f(cu[vi] * w, cv[vi] * h);
    out.layer = f32(mat);
    out.shade = face_shade(face) * ao_factor * sky;
    return out;
}

@fragment
fn fs_main(in: VsOut) -> @location(0) vec4f {
    let c = textureSample(atlas, atlas_smp, in.uv, i32(in.layer));
    return vec4f(c.rgb * in.shade, 1.0);
}
```

- [ ] **Step 3: 实现渲染器**

`internal/render/renderer.go` 的结构（关键点，不是全文）：

```go
package render

// 每个面在源池中占 8 字节，在紧凑实例缓冲中占 16 字节。
const (
	bytesPerPoolFace     = 8
	bytesPerVisibleFace  = 16
	// 面池容量：32 视距下实测峰值约 300 万个面，留 1.5 倍余量。
	defaultPoolFaces     = 4_500_000
	// 每帧上传上限 4 MB：约 50 万个面，足够跟上飞行时的区块加载，
	// 又不会在一帧里造成可见的卡顿。
	defaultUploadPerFrame = 4 << 20
)

// sectionSlot 记录一个已上传区段在池中的位置。
type sectionSlot struct {
	alloc   Alloc
	origin  [4]int32 // 世界坐标，第 4 个分量补齐 16 字节对齐
	originIdx uint32 // 在 origins 缓冲中的下标
}

type Renderer struct {
	dev    gfx.Device
	pool   *Pool
	budget *UploadBudget

	faces     gfx.Buffer // 源面池，array<u32>，每面 2 个 u32
	instances gfx.Buffer // 紧凑实例缓冲，array<vec4u>
	origins   gfx.Buffer // array<vec4i>，区段世界坐标
	camera    gfx.Buffer // uniform
	indirect  gfx.Buffer // DrawIndexedIndirect 参数
	index     gfx.Buffer // 固定 6 个索引：0,1,2, 0,2,3

	sections map[core.SectionPos]sectionSlot
	pipeline gfx.RenderPipeline
	bind     gfx.BindGroup
}
```

`QueueSection` 把结果放入 renderer 持有的 pending map，同一位置的新网格覆盖旧网格但不得静默丢失。`FlushUploads` 每帧按到相机中心的距离排序，反复调用内部 `uploadOne`：

1. `budget.TryConsume(len(quads) * bytesPerPoolFace)`，false 则保留在 pending 中，下一帧重试
2. 若该区段已有分配且容量不足，先 `pool.Free` 再重新 `Alloc`
3. 把 `quads` 逐个 `Pack()` 成 `[]uint64`，写入 `faces` 缓冲的对应偏移
4. 登记/更新 `origins` 中的区段世界坐标
5. 全部成功后才从 pending 删除；`DropSection` / `DropOutside` 必须同时删除 pending 与已上传分配

补 `TestPendingUploadsEventuallyDrain`：用每帧只能容纳一个普通区段的预算排队 10 个区段，连续 `BeginFrame+Flush` 后断言全部最终上传；其中一个单项大于整帧预算，验证它只独占一帧但不会饿死。

`Render` 的流程（本任务是 CPU 版，Task 14 换成 GPU）：

1. 遍历 `sections`，把每个面展开成 `vec4u{lo, hi, originIdx, 0}` 写入 `instances`
2. 写 `indirect` 参数：`indexCount=6, instanceCount=总面数, firstIndex=0, baseVertex=0, firstInstance=0`
3. `BeginRenderPass` → `SetPipeline` → `SetBindGroup` → `SetIndexBuffer` → `DrawIndexedIndirect(indirect, 0)` → `End`

- [ ] **Step 4: 改造 spike 渲染真实地形**

`cmd/gfxspike/main.go`：生成一个 8×8 区块的地形，全部网格化并上传，用一个固定的俯视相机渲染。

```go
gen := worldgen.New(42)
reg := assets.NewRegistry()
r := render.New(dev, reg, surface.Format())

for cx := int32(0); cx < 8; cx++ {
    for cz := int32(0); cz < 8; cz++ {
        ch := gen.GenerateChunk(core.ChunkPos{X: cx, Z: cz})
        chunks[core.ChunkPos{X: cx, Z: cz}] = ch
    }
}
// 网格化时需要邻居，所以先全部生成再统一网格化。
get := func(p core.ChunkPos) *world.Chunk { return chunks[p] }
for pos := range chunks {
    for si := 0; si < core.SectionsPerChunk; si++ {
        n := world.NeighborhoodAt(get, pos, si)
        quads := mesh.MeshSection(n, reg)
        if len(quads) == 0 {
            continue
        }
        r.QueueSection(core.SectionPos{X: pos.X, Y: int32(si), Z: pos.Z}, quads)
    }
}
```

上传限流会让首帧上传不完；结果留在 renderer 的 pending 队列中。主循环每帧先 `BeginFrame()`，再调用 `FlushUploads(core.ChunkPos{})`，直到 `PendingUploads()==0`。

- [ ] **Step 5: 运行验证**

```bash
go run ./cmd/gfxspike
```

预期：屏幕上出现一片有起伏的地形，草地是绿的、侧面是棕绿的、底下是石头，顶面比侧面亮、侧面比底面亮。

**常见故障与对应病因：**

| 现象 | 病因 |
|---|---|
| 全屏纯色 | 相机矩阵或 `instanceCount` 为 0 |
| 面的位置乱飞 | WGSL 解包位布局与 `mesh.Pack` 不一致 |
| 材质被拉伸而非平铺 | 采样器不是 `AddressRepeat`（Step 1） |
| 地形有洞、能看穿 | 面朝向或 `axis_vec(axis) * positive` 的偏移错了 |
| 相邻区块之间有缝或多余的面 | `Neighborhood` 的邻居没接上，退化成了 `BarrierID` |

- [ ] **Step 6: 提交**

```bash
git add internal/render internal/gfx/gfx.go internal/assets/atlas.go cmd/gfxspike
git commit -m "feat: 单次 indirect draw 渲染出第一帧真实地形"
```

---

### Task 14: compute 剔除——视锥、背面与压缩

**Files:**
- Create: `internal/render/shader/cull.wgsl`
- Create: `internal/render/cull.go`
- Create: `internal/render/cull_test.go`
- Modify: `internal/render/renderer.go`（`Render` 改走 GPU 剔除）

**Interfaces:**
- Consumes: Task 13 的 `Renderer`、Task 10 的 `VisibleSections`、Task 3 的 `NewHeadlessDevice`
- Produces: `Renderer.Render` 不再在 CPU 上展开实例；CPU 每帧只遍历并提交候选区段列表

**分工**：CPU 做可见性图 BFS + 区段级视锥；GPU 做面级背面剔除 + 压缩。CPU 成本与候选区段数相关、与总面数无关；候选数会随视距增长，不能写死为“几百”，必须由 Task 17 在固定 32 视距场景实测。

- [ ] **Step 1: 写失败的测试**

背面剔除的正确性用 headless compute 直接断言，不靠肉眼看画面。

`internal/render/cull_test.go`：

```go
package render_test

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"minecraft-go/internal/gfx"
	"minecraft-go/internal/mesh"
	"minecraft-go/internal/render"
)

// TestCullDropsBackFaces 验证 GPU 背面剔除：
// 相机在方块上方时，朝下的面必须被剔除、朝上的面必须保留。
//
// 体素世界里这一项能砍掉约一半的面，成本只是一次点积（spec §5.2 ②）。
func TestCullDropsBackFaces(t *testing.T) {
	dev, err := gfx.NewHeadlessDevice()
	if err != nil {
		t.Skipf("本机无可用 GPU 适配器: %v", err)
	}
	defer dev.Release()

	// 一个孤立方块的 6 个面，位于区段原点 (0,0,0) 的 (8,8,8) 处。
	quads := []mesh.Quad{
		{X: 8, Y: 8, Z: 8, W: 1, H: 1, Face: mesh.FaceNegX, Mat: 0, AO: 0xFF, Light: 0xF0},
		{X: 8, Y: 8, Z: 8, W: 1, H: 1, Face: mesh.FacePosX, Mat: 0, AO: 0xFF, Light: 0xF0},
		{X: 8, Y: 8, Z: 8, W: 1, H: 1, Face: mesh.FaceNegY, Mat: 0, AO: 0xFF, Light: 0xF0},
		{X: 8, Y: 8, Z: 8, W: 1, H: 1, Face: mesh.FacePosY, Mat: 0, AO: 0xFF, Light: 0xF0},
		{X: 8, Y: 8, Z: 8, W: 1, H: 1, Face: mesh.FaceNegZ, Mat: 0, AO: 0xFF, Light: 0xF0},
		{X: 8, Y: 8, Z: 8, W: 1, H: 1, Face: mesh.FacePosZ, Mat: 0, AO: 0xFF, Light: 0xF0},
	}

	// 相机在正上方远处向下看：只有 +Y 面朝向相机。
	camPos := mgl32.Vec3{8.5, 100, 8.5}
	got := render.RunCullForTest(dev, quads, camPos)

	if len(got) != 1 {
		t.Fatalf("剔除后剩 %d 个面，想要 1（只有 +Y 面朝向相机）", len(got))
	}
	if got[0].Face != mesh.FacePosY {
		t.Fatalf("保留的面朝向 = %d，想要 FacePosY", got[0].Face)
	}
}

// TestCullFromBelowKeepsBottomFace 从下方看时应只剩 -Y 面。
func TestCullFromBelowKeepsBottomFace(t *testing.T) {
	dev, err := gfx.NewHeadlessDevice()
	if err != nil {
		t.Skipf("本机无可用 GPU 适配器: %v", err)
	}
	defer dev.Release()

	quads := []mesh.Quad{
		{X: 8, Y: 8, Z: 8, W: 1, H: 1, Face: mesh.FaceNegY, Mat: 0, AO: 0xFF, Light: 0xF0},
		{X: 8, Y: 8, Z: 8, W: 1, H: 1, Face: mesh.FacePosY, Mat: 0, AO: 0xFF, Light: 0xF0},
	}
	got := render.RunCullForTest(dev, quads, mgl32.Vec3{8.5, -100, 8.5})

	if len(got) != 1 || got[0].Face != mesh.FaceNegY {
		t.Fatalf("从下方看应只剩 -Y 面，实际 %+v", got)
	}
}

// TestCullPreservesInstanceData 验证压缩过程没有破坏实例内容。
func TestCullPreservesInstanceData(t *testing.T) {
	dev, err := gfx.NewHeadlessDevice()
	if err != nil {
		t.Skipf("本机无可用 GPU 适配器: %v", err)
	}
	defer dev.Release()

	want := mesh.Quad{
		X: 3, Y: 11, Z: 7, W: 5, H: 9,
		Face: mesh.FacePosY, Mat: 1234, AO: 0xB4, Light: 0xF3,
	}
	got := render.RunCullForTest(dev, []mesh.Quad{want}, mgl32.Vec3{3, 200, 7})

	if len(got) != 1 {
		t.Fatalf("剔除后剩 %d 个面，想要 1", len(got))
	}
	if got[0] != want {
		t.Fatalf("实例数据被破坏:\n实际 %+v\n期望 %+v", got[0], want)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
go test ./internal/render/ -run TestCull -v
```

预期：`undefined: render.RunCullForTest`。

- [ ] **Step 3: 写剔除着色器**

`internal/render/shader/cull.wgsl`：

```wgsl
// 一个候选区段。CPU 侧已用可见性图 BFS + 区段级视锥筛过一轮，
// 这里只剩几百个（spec §5.2 ①），GPU 负责面级的精细剔除与压缩。
struct SectionRec {
    origin:      vec4i,  // 区段最小角的世界坐标，w 补齐对齐
    face_offset: u32,    // 在源面池中的起始面号
    face_count:  u32,
    origin_idx:  u32,    // 在 origins 缓冲中的下标
    _pad:        u32,
};

struct CullUniforms {
    cam_pos: vec4f,
};

// DrawIndexedIndirect 的参数布局，字段顺序由 WebGPU 规范固定。
struct DrawArgs {
    index_count:    u32,
    instance_count: atomic<u32>,
    first_index:    u32,
    base_vertex:    u32,
    first_instance: u32,
};

@group(0) @binding(0) var<uniform>              u:          CullUniforms;
@group(0) @binding(1) var<storage, read>        sections:   array<SectionRec>;
@group(0) @binding(2) var<storage, read>        faces:      array<u32>;
@group(0) @binding(3) var<storage, read_write>  visible:    array<vec4u>;
@group(0) @binding(4) var<storage, read_write>  args:       DrawArgs;

fn axis_vec(axis: u32) -> vec3f {
    if (axis == 0u) { return vec3f(1.0, 0.0, 0.0); }
    if (axis == 1u) { return vec3f(0.0, 1.0, 0.0); }
    return vec3f(0.0, 0.0, 1.0);
}

// 一个工作组处理一个候选区段；组内 64 个线程跨步遍历该区段的面。
@compute @workgroup_size(64)
fn cs_main(
    @builtin(workgroup_id)         wg:  vec3u,
    @builtin(local_invocation_id)  lid: vec3u,
) {
    let sec = sections[wg.x];

    for (var i = lid.x; i < sec.face_count; i += 64u) {
        let base = (sec.face_offset + i) * 2u;
        let lo = faces[base];
        let hi = faces[base + 1u];

        let face = (lo >> 20u) & 0x7u;
        let axis = face >> 1u;

        // 法线：正方向面朝 +axis，负方向面朝 -axis。
        var normal = axis_vec(axis);
        if ((face & 1u) == 0u) {
            normal = -normal;
        }

        // 面的世界坐标（取起点即可，误差不影响背面判定）。
        let local = vec3f(
            f32( lo        & 0xFu),
            f32((lo >>  4u) & 0xFu),
            f32((lo >>  8u) & 0xFu),
        ) + normal * f32(face & 1u);
        let world = vec3f(sec.origin.xyz) + local;

        // 背面剔除：面法线背离相机则不可见。
        // 体素世界里这一项就能砍掉约一半的面，成本只是一次点积。
        if (dot(normal, world - u.cam_pos.xyz) >= 0.0) {
            continue;
        }

        // 原子累加拿到紧凑输出中的槽位，同时累出总实例数。
        let slot = atomicAdd(&args.instance_count, 1u);
        visible[slot] = vec4u(lo, hi, sec.origin_idx, 0u);
    }
}
```

- [ ] **Step 4: 实现剔除的 Go 侧编排与测试入口**

`internal/render/cull.go` 需要提供：

```go
// culler 持有剔除 pass 用到的 GPU 资源。
type culler struct {
	pipeline gfx.ComputePipeline
	uniforms gfx.Buffer // CullUniforms
	sections gfx.Buffer // array<SectionRec>
	bind     gfx.BindGroup
}

// dispatch 录制一次剔除。candidates 是 CPU 侧已筛过的候选区段。
// 调用方必须在此之前把 args.instance_count 清零。
func (c *culler) dispatch(enc gfx.CommandEncoder, candidates int, camPos mgl32.Vec3)

// RunCullForTest 是给测试用的入口：把 quads 当作单个区段送进剔除 pass，
// 返回存活下来的四边形。仅供 go test 使用，不在渲染路径上。
func RunCullForTest(dev gfx.Device, quads []mesh.Quad, camPos mgl32.Vec3) []mesh.Quad
```

`RunCullForTest` 的实现要点：把 `quads` 打包写入一个临时 faces 缓冲，造一条 `SectionRec{origin: {0,0,0,0}, face_offset: 0, face_count: len(quads), origin_idx: 0}`，跑一次 dispatch，然后 `ReadBack` 出 `visible` 与 `args`，按 `instance_count` 截断并 `mesh.UnpackQuad` 还原。

**注意**：`visible` 与 `args` 缓冲的 Usage 必须含 `gfx.BufferUsageCopySrc`。`ReadBack` 会在内部拷到 `MapRead|CopyDst` staging buffer；不得把 `MapRead` 与 `Storage` / `Indirect` 混在同一个缓冲上。

- [ ] **Step 5: 运行测试，确认通过**

```bash
go test ./internal/render/ -run TestCull -v
```

预期：三个测试全部 PASS。

若 `TestCullPreservesInstanceData` 失败而另两个通过，问题在压缩写入而非剔除判定——检查 `visible[slot]` 的写法与缓冲步长。

- [ ] **Step 6: 把 Render 切到 GPU 剔除**

`internal/render/renderer.go` 的 `Render` 改成：

1. CPU：`mesh.VisibleSections(camSection, viewDistance, frustum, lookup)` 拿候选区段
2. CPU：把候选区段的 `SectionRec` 写入 `culler.sections` 缓冲
3. GPU：`CopyBufferToBuffer` 把一个全零的 20 字节模板拷进 `indirect`，重置 `instance_count`（同时把 `index_count` 复位成 6）
4. GPU：compute pass 跑剔除
5. GPU：render pass 一条 `DrawIndexedIndirect`

**CPU 每帧的工作量此时只与候选区段数有关，与总面数（几百万）无关。** 这兑现的是“CPU 不逐面工作”，并不意味着与视距严格无关：BFS 与候选列表上传仍随候选区段数增长。Task 17 必须分别记录 16/24/32 视距下的候选数、BFS 时间和上传字节数；若 32 视距下 CPU 超预算，再把区段记录持久化到 GPU 并改为增量更新，而不是用不准确的口号宣告达标。

- [ ] **Step 7: 运行验证**

```bash
go run ./cmd/gfxspike
```

预期：画面与 Task 13 一致（背面本来就看不见），但如果加一行日志打印 `instance_count`，应能看到实例数比 Task 13 少约一半。

- [ ] **Step 8: 提交**

```bash
git add internal/render
git commit -m "feat: compute 剔除，CPU 每帧不再逐面展开"
```

---

### Task 15: Hi-Z 遮挡剔除

**Files:**
- Create: `internal/render/shader/hiz_copy.wgsl`
- Create: `internal/render/shader/hiz_build.wgsl`
- Create: `internal/render/hiz.go`
- Modify: `internal/render/shader/cull.wgsl`（加入遮挡测试）
- Modify: `internal/render/cull.go`

**Interfaces:**
- Consumes: Task 14 的 `culler`
- Produces: `hiZ` 类型，每帧从深度缓冲构建层次 Z 金字塔

**这一级的收益排在可见性图之后。** 可见性图已经砍掉了地下和被墙隔开的区块；Hi-Z 补的是「在同一片开阔空间里，被前方山体挡住的远处区段」——这部分可见性图看不出来。

**一个必须知道的取舍**：用**上一帧**的深度金字塔测**本帧**的可见性，代价是历史深度在相机运动后可能失效。为保证“绝不误剔除”，当相机平移超过 1 个方块、旋转超过 2°、投影矩阵变化或窗口 resize 时，本帧禁用 Hi-Z，只做可见性图与背面剔除，并用本帧深度重建下一帧金字塔。小幅运动继续复用上一帧；若实测收益不足，M5 再考虑深度预通道或重投影。

- [ ] **Step 1: 写深度金字塔构建着色器**

深度附件是 `Depth32Float`，Hi-Z 是可写的 `R32Float`，两者格式不同，不能用纹理拷贝直接复制。先用 `hiz_copy.wgsl` 把深度采样到 mip 0，同时把窗口外的 padding 写成最远深度 1：

```wgsl
// internal/render/shader/hiz_copy.wgsl
struct CopyUniforms {
    viewport: vec2u,
};

@group(0) @binding(0) var src: texture_depth_2d;
@group(0) @binding(1) var dst: texture_storage_2d<r32float, write>;
@group(0) @binding(2) var<uniform> u: CopyUniforms;

@compute @workgroup_size(8, 8)
fn cs_main(@builtin(global_invocation_id) gid: vec3u) {
    let size = textureDimensions(dst);
    if (gid.x >= size.x || gid.y >= size.y) {
        return;
    }
    var d = 1.0;
    if (gid.x < u.viewport.x && gid.y < u.viewport.y) {
        d = textureLoad(src, vec2i(gid.xy), 0);
    }
    textureStore(dst, vec2i(gid.xy), vec4f(d, 0.0, 0.0, 1.0));
}
```

该 pass 的布局分别使用 `BindGroupLayoutEntry{Type: BindingDepthTexture}` 与 `BindGroupLayoutEntry{Type: BindingStorageTextureWrite, StorageFormat: FormatR32Float}`。
主程序创建深度纹理时 Usage 必须为 `TextureUsageRenderTarget|TextureUsageBinding`，并用 `AspectDepthOnly` 视图供复制 pass 采样；只带 RenderTarget usage 的深度附件不能在 compute 中读取。

随后 `internal/render/shader/hiz_build.wgsl` 逐级下采样：

```wgsl
// 从上一级 mip 取 2×2，保留最远的深度（保守：宁可少剔除，不可多剔除）。
//
// 用 max 而非 min：WebGPU 深度范围是 [0,1]，0 近 1 远。
// 保留最远值意味着遮挡体被低估，只会漏剔除、不会错剔除。
// 反过来会把可见的东西剔掉，那是肉眼可见的破洞。
@group(0) @binding(0) var src: texture_2d<f32>;
@group(0) @binding(1) var dst: texture_storage_2d<r32float, write>;

@compute @workgroup_size(8, 8)
fn cs_main(@builtin(global_invocation_id) gid: vec3u) {
    let dst_size = textureDimensions(dst);
    if (gid.x >= dst_size.x || gid.y >= dst_size.y) {
        return;
    }
    let p = vec2i(gid.xy) * 2;
    let d0 = textureLoad(src, p + vec2i(0, 0), 0).r;
    let d1 = textureLoad(src, p + vec2i(1, 0), 0).r;
    let d2 = textureLoad(src, p + vec2i(0, 1), 0).r;
    let d3 = textureLoad(src, p + vec2i(1, 1), 0).r;
    textureStore(dst, vec2i(gid.xy), vec4f(max(max(d0, d1), max(d2, d3)), 0.0, 0.0, 1.0));
}
```

每一级都通过 `Texture.View(TextureViewDesc{BaseMipLevel: level, MipLevelCount: 1})` 单独绑定：R32Float 源是 `BindingSampledTextureUnfilterableFloat`，目标是 `BindingStorageTextureWrite`，两者 `ViewDimension` 都是 `TextureViewDimension2D`。不得把覆盖整条 mip 链的默认视图同时绑定为输入和输出，也不得为了 R32Float 请求非必要的 float32-filterable 特性。

- [ ] **Step 2: 在剔除着色器中加入遮挡测试**

`internal/render/shader/cull.wgsl` 修改：`SectionRec` 之外再传入 `view_proj` 与 Hi-Z 纹理，在**工作组的第一个线程**上做区段级遮挡测试，不通过则整个工作组直接返回。区段级足够——面级遮挡测试的收益远小于其成本。

```wgsl
// 追加到 CullUniforms
struct CullUniforms {
    cam_pos:   vec4f,
    view_proj: mat4x4f,
    // hiz_size.xy 是实际 viewport 像素尺寸，z 为最大 mip 级别。
    hiz_size:  vec4f,
    // viewport 在补齐到 2 次幂后的 Hi-Z mip 0 中所占的 UV 比例。
    hiz_uv_scale: vec4f,
};

@group(0) @binding(5) var hiz: texture_2d<f32>;

// section_occluded 把区段 AABB 投影到屏幕，取其屏幕包围盒对应的
// Hi-Z mip 级别采样，若 AABB 最近点仍比 Hi-Z 记录的深度更远，则被遮挡。
fn section_occluded(origin: vec3f) -> bool {
    var min_uv = vec2f( 1e30,  1e30);
    var max_uv = vec2f(-1e30, -1e30);
    var min_z  = 1e30;

    // AABB 的 8 个角。
    for (var i = 0u; i < 8u; i++) {
        let c = origin + vec3f(
            f32( i        & 1u) * 16.0,
            f32((i >> 1u) & 1u) * 16.0,
            f32((i >> 2u) & 1u) * 16.0,
        );
        let clip = u.view_proj * vec4f(c, 1.0);
        // 有角点在相机后方时放弃测试，保守地判为不遮挡。
        if (clip.w <= 0.0) {
            return false;
        }
        let ndc = clip.xyz / clip.w;
        let uv = ndc.xy * vec2f(0.5, -0.5) + vec2f(0.5, 0.5);
        min_uv = min(min_uv, uv);
        max_uv = max(max_uv, uv);
        min_z  = min(min_z, ndc.z);
    }

    // 完全在 viewport 外由前面的视锥剔除处理；这里仍 clamp，避免边界浮点误差。
    min_uv = clamp(min_uv, vec2f(0.0), vec2f(1.0));
    max_uv = clamp(max_uv, vec2f(0.0), vec2f(1.0));

    // 选到包围盒宽高不超过一个 mip 纹素的级别。即使宽高都不超过 1，
    // 包围盒也可能因未对齐跨越 2×2 个纹素，所以必须读四角，不能只采中心。
    let size_px = (max_uv - min_uv) * u.hiz_size.xy;
    let level = clamp(ceil(log2(max(max(size_px.x, size_px.y), 1.0))),
                      0.0, u.hiz_size.z);

    let dim = vec2i(textureDimensions(hiz, u32(level)));
    let padded_min = min_uv * u.hiz_uv_scale.xy;
    let padded_max = max_uv * u.hiz_uv_scale.xy;
    let p0 = clamp(vec2i(floor(padded_min * vec2f(dim))), vec2i(0), dim - 1);
    let p1 = clamp(vec2i(floor(padded_max * vec2f(dim))), vec2i(0), dim - 1);
    let d00 = textureLoad(hiz, vec2i(p0.x, p0.y), i32(level)).r;
    let d10 = textureLoad(hiz, vec2i(p1.x, p0.y), i32(level)).r;
    let d01 = textureLoad(hiz, vec2i(p0.x, p1.y), i32(level)).r;
    let d11 = textureLoad(hiz, vec2i(p1.x, p1.y), i32(level)).r;
    let d = max(max(d00, d10), max(d01, d11));
    // min_z 是区段最近点；它比已记录的最远深度还远，说明整体被挡住。
    return min_z > d;
}
```

在 `cs_main` 开头加：

```wgsl
    let sec = sections[wg.x];
    if (lid.x == 0u) {
        // 只让 0 号线程做测试，结果通过工作组共享变量广播。
        occluded = section_occluded(vec3f(sec.origin.xyz));
    }
    workgroupBarrier();
    if (occluded) {
        return;
    }
```

配套声明 `var<workgroup> occluded: bool;`。

- [ ] **Step 3: 实现 Hi-Z 金字塔管理**

`internal/render/hiz.go`：

```go
package render

// hiZ 管理深度金字塔：一张带完整 mip 链的 R32Float 纹理，
// 每帧从上一帧的深度缓冲逐级下采样构建。
//
// 用上一帧的深度测本帧的可见性（见 Task 15 说明），
// 换来的是省掉一整个深度预通道。
type hiZ struct {
	tex          gfx.Texture
	views        []gfx.TextureView // 每级一个单 mip 视图
	copyPipeline gfx.ComputePipeline
	buildPipeline gfx.ComputePipeline
	copyBind     gfx.BindGroup
	buildBinds   []gfx.BindGroup
	viewportW    uint32
	viewportH    uint32
	paddedW      uint32
	paddedH      uint32
	levels       uint32
}

// newHiZ 创建金字塔。尺寸分别向上补齐到不小于窗口尺寸的 2 次幂，
// 保证整个 viewport 都被覆盖；hiz_copy 把 padding 明确写成深度 1，
// 因而 padding 只会让剔除更保守，不会制造错误遮挡。
func newHiZ(dev gfx.Device, w, h uint32) *hiZ

// build 在本帧 terrain render pass 结束后调用，结果供下一帧剔除使用：
// 先以 compute 把 Depth32Float 转写进 R32Float mip 0 并填充 padding，
// 再通过单 mip view 逐级 dispatch 下采样。
func (z *hiZ) build(enc gfx.CommandEncoder, depth gfx.TextureView)

// resize 在窗口尺寸变化后重建纹理与视图。
func (z *hiZ) resize(dev gfx.Device, w, h uint32)
```

`newHiZ` 创建 `FormatR32Float`、`TextureUsageBinding|TextureUsageStorage` 的完整 mip 链。culler 使用覆盖整条 mip 链的 sampled view；构建 pass 使用 `views[level-1]` 与 `views[level]`。第一帧尚无历史深度时必须禁用遮挡剔除，不能依赖未初始化纹理内容。

- [ ] **Step 4: 写一个防回归的可见性测试**

遮挡剔除最危险的失败模式是**剔多了**——画面出现破洞，而这在静止截图上很容易被忽略。用一个 CPU 侧的等价实现来交叉验证保守性。

`internal/render/hiz_test.go`：

```go
package render_test

import (
	"testing"

	"minecraft-go/internal/render"
)

// TestHiZLevelSelectionIsConservative 验证 mip 级别选择公式
// 永远向上取整——选低了会采样不到整个包围盒，导致错误剔除。
func TestHiZLevelSelectionIsConservative(t *testing.T) {
	cases := []struct{ px, wantLevel float64 }{
		{1, 0}, {2, 1}, {3, 2}, {4, 2}, {5, 3}, {8, 3}, {9, 4},
	}
	for _, c := range cases {
		got := render.HiZLevelForTest(c.px)
		if got < c.wantLevel {
			t.Fatalf("包围盒 %g 像素选了 mip %g，至少需要 %g（选低了会错剔除）",
				c.px, got, c.wantLevel)
		}
	}
}

// TestNothingIsCulledWhenDepthIsFar 验证深度全为 1（什么都没画）时，
// 没有任何区段被遮挡剔除。这是「首帧不该是黑屏」的保证。
func TestNothingIsCulledWhenDepthIsFar(t *testing.T) {
	// 用 CPU 侧等价实现验证判定式：min_z > d 时才算被遮挡。
	// d = 1.0（最远）时，任何 min_z <= 1.0 都不应被剔除。
	for _, minZ := range []float32{0, 0.5, 0.999, 1.0} {
		if render.OccludedForTest(minZ, 1.0) {
			t.Fatalf("min_z=%g、深度=1.0 时不应判为被遮挡", minZ)
		}
	}
	if !render.OccludedForTest(0.9, 0.5) {
		t.Fatal("min_z=0.9 比记录深度 0.5 更远，应判为被遮挡")
	}
}

// TestHiZUsesAllCoveredTexels 防止实现退化为只采包围盒中心。
// 四个角只要有一个记录到更远深度，就必须取最大值并保守地保留区段。
func TestHiZUsesAllCoveredTexels(t *testing.T) {
	if got := render.Max4ForTest(0.2, 0.3, 1.0, 0.4); got != 1.0 {
		t.Fatalf("四纹素最大深度 = %g，想要 1.0", got)
	}
	if render.OccludedForTest(0.9, render.Max4ForTest(0.2, 0.3, 1.0, 0.4)) {
		t.Fatal("覆盖范围内存在深度 1.0 时不得错误剔除")
	}
}
```

对应导出测试专用函数（与 WGSL 中的公式保持一致，改一处必须改两处）：

```go
// HiZLevelForTest 与 cull.wgsl 中的 mip 级别选择公式等价，供测试验证。
func HiZLevelForTest(sizePx float64) float64 {
	return math.Ceil(math.Log2(math.Max(sizePx, 1)))
}

// OccludedForTest 与 cull.wgsl 中的遮挡判定等价，供测试验证。
func OccludedForTest(minZ, hizDepth float32) bool { return minZ > hizDepth }

func Max4ForTest(a, b, c, d float32) float32 {
	return max(max(a, b), max(c, d))
}
```

再用一个 headless GPU 测试构建 `13×7` viewport 的金字塔：断言实际纹理补齐到 `16×8`，padding 在所有 mip 中保持深度 1，并把随机生成的深度图与 CPU max-reduction 逐级对拍。这个用例同时覆盖非 2 次幂窗口与单 mip view 绑定。

- [ ] **Step 5: 运行测试与实机验证**

```bash
go test ./internal/render/ -v
go run ./cmd/gfxspike
```

预期：测试全部 PASS；画面与 Task 14 一致（**遮挡剔除正确时画面不应有任何变化**），但打印的 `instance_count` 在相机贴近山体时应明显下降。

**关键验收动作**：把相机贴到一座山前面，缓慢左右转动，观察山后的地形边缘有没有闪烁的破洞。有破洞说明剔多了，检查 `max` vs `min` 的方向与 mip 级别是否向上取整。

- [ ] **Step 6: 提交**

```bash
git add internal/render
git commit -m "feat: Hi-Z 遮挡剔除，补上可见性图看不到的远处遮挡"
```

---

### Task 16: 窗口、输入、自由飞行相机与区块流式加载

**Files:**
- Create: `internal/client/window.go`
- Create: `internal/client/camera.go`
- Create: `internal/client/camera_test.go`
- Create: `internal/client/streamer.go`
- Create: `internal/client/streamer_test.go`
- Create: `cmd/mcgo/main.go`

**Interfaces:**
- Consumes: Task 13-15 的 `Renderer`、Task 8 的 `Generator`、Task 9 的 `MeshSection`
- Produces:

```go
type Camera struct {
    Pos              mgl32.Vec3
    Yaw, Pitch       float32   // 弧度
    FovY, Aspect     float32
    Near, Far        float32
}
func (c *Camera) Forward() mgl32.Vec3
func (c *Camera) ViewProj() mgl32.Mat4
func (c *Camera) Rotate(dYaw, dPitch float32)
func (c *Camera) Move(fwd, right, up float32)

type Streamer struct{ /* 非导出 */ }
func NewStreamer(gen *worldgen.Generator, reg *assets.Registry, workers int) *Streamer
func (s *Streamer) SetCenter(p core.ChunkPos, radius int)
func (s *Streamer) Drain(max int) []MeshedSection
func (s *Streamer) Stats() StreamStats
func (s *Streamer) Close()

type StreamStats struct {
    CachedChunks, QueuedJobs, InFlightJobs int
    Generation uint64
}
```

- [ ] **Step 1: 写相机测试**

`internal/client/camera_test.go`：

```go
package client_test

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"minecraft-go/internal/client"
)

// TestCameraPitchIsClamped 俯仰角必须夹在 ±90° 内。
// 不夹住的话仰头越过天顶时视角会翻转，是最经典的手感 bug。
func TestCameraPitchIsClamped(t *testing.T) {
	c := client.Camera{FovY: 1.2, Aspect: 1.6, Near: 0.1, Far: 1000}
	for i := 0; i < 100; i++ {
		c.Rotate(0, 0.5)
	}
	if c.Pitch > math.Pi/2 {
		t.Fatalf("持续仰头后 Pitch = %f，应夹在 π/2 以内", c.Pitch)
	}
	for i := 0; i < 200; i++ {
		c.Rotate(0, -0.5)
	}
	if c.Pitch < -math.Pi/2 {
		t.Fatalf("持续低头后 Pitch = %f，应夹在 -π/2 以内", c.Pitch)
	}
}

func TestCameraForwardMatchesYaw(t *testing.T) {
	c := client.Camera{FovY: 1.2, Aspect: 1.6, Near: 0.1, Far: 1000}
	// yaw=0、pitch=0 时朝向 -Z，与 mgl32.LookAtV 的惯例一致。
	f := c.Forward()
	if math.Abs(float64(f.Z()+1)) > 1e-5 || math.Abs(float64(f.X())) > 1e-5 {
		t.Fatalf("初始朝向 = %v，想要 (0,0,-1)", f)
	}

	c.Yaw = math.Pi / 2
	f = c.Forward()
	if math.Abs(float64(f.X()+1)) > 1e-5 {
		t.Fatalf("yaw=90° 时朝向 = %v，X 分量应为 -1", f)
	}
}

// TestCameraMoveIsRelativeToYawOnly 验证水平移动不受俯仰角影响——
// 低头时按 W 应该平着往前走，而不是钻进地里。
func TestCameraMoveIsRelativeToYawOnly(t *testing.T) {
	c := client.Camera{FovY: 1.2, Aspect: 1.6, Near: 0.1, Far: 1000}
	c.Rotate(0, -1.0) // 低头
	before := c.Pos.Y()
	c.Move(1, 0, 0)
	if math.Abs(float64(c.Pos.Y()-before)) > 1e-5 {
		t.Fatalf("低头前进后 Y 变了 %f，水平移动不应受俯仰影响",
			c.Pos.Y()-before)
	}
}

func TestCameraViewProjIsFinite(t *testing.T) {
	c := client.Camera{
		Pos: mgl32.Vec3{100, 80, -300},
		Yaw: 1.1, Pitch: -0.4,
		FovY: 1.2, Aspect: 1.6, Near: 0.1, Far: 1000,
	}
	m := c.ViewProj()
	for i, v := range m {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			t.Fatalf("ViewProj 第 %d 项非有限值: %f", i, v)
		}
	}
}
```

- [ ] **Step 2: 运行测试，确认失败，然后实现相机**

```bash
go test ./internal/client/ -v
```

`internal/client/camera.go`：

```go
// Package client 负责窗口、输入、相机与区块流式加载。
package client

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// pitchLimit 略小于 90°，避免在正上/正下方时 up 向量与视线共线导致矩阵退化。
const pitchLimit = float32(math.Pi/2) - 0.01

// Camera 是自由飞行相机。yaw=0、pitch=0 时朝向 -Z。
type Camera struct {
	Pos        mgl32.Vec3
	Yaw, Pitch float32 // 弧度
	FovY       float32
	Aspect     float32
	Near, Far  float32
}

// Forward 返回视线方向的单位向量。
func (c *Camera) Forward() mgl32.Vec3 {
	cp := float32(math.Cos(float64(c.Pitch)))
	return mgl32.Vec3{
		-float32(math.Sin(float64(c.Yaw))) * cp,
		float32(math.Sin(float64(c.Pitch))),
		-float32(math.Cos(float64(c.Yaw))) * cp,
	}
}

// Rotate 转动视角，俯仰角夹在 ±pitchLimit 内。
func (c *Camera) Rotate(dYaw, dPitch float32) {
	c.Yaw += dYaw
	c.Pitch += dPitch
	if c.Pitch > pitchLimit {
		c.Pitch = pitchLimit
	}
	if c.Pitch < -pitchLimit {
		c.Pitch = -pitchLimit
	}
}

// Move 沿相机的水平朝向移动。
//
// 前后左右只用 yaw、不用 pitch：低头时按前进应该平着走，
// 而不是钻进地里。上下由 up 参数直接控制世界 Y 轴。
func (c *Camera) Move(fwd, right, up float32) {
	sy := float32(math.Sin(float64(c.Yaw)))
	cy := float32(math.Cos(float64(c.Yaw)))
	c.Pos = c.Pos.Add(mgl32.Vec3{
		-sy*fwd + cy*right,
		up,
		-cy*fwd - sy*right,
	})
}

// ViewProj 返回 view-projection 矩阵。
func (c *Camera) ViewProj() mgl32.Mat4 {
	view := mgl32.LookAtV(c.Pos, c.Pos.Add(c.Forward()), mgl32.Vec3{0, 1, 0})
	proj := mgl32.Perspective(c.FovY, c.Aspect, c.Near, c.Far)
	return proj.Mul4(view)
}
```

- [ ] **Step 3: 写流式加载测试**

`internal/client/streamer_test.go`：

```go
package client_test

import (
	"testing"
	"time"

	"minecraft-go/internal/assets"
	"minecraft-go/internal/client"
	"minecraft-go/internal/core"
	"minecraft-go/internal/worldgen"
)

// TestStreamerProducesSectionsAroundCenter 验证流式加载会把
// 中心附近的区段生成并网格化出来。
func TestStreamerProducesSectionsAroundCenter(t *testing.T) {
	s := client.NewStreamer(worldgen.New(42), assets.NewRegistry(), 4)
	defer s.Close()

	s.SetCenter(core.ChunkPos{X: 0, Z: 0}, 2)

	got := 0
	deadline := time.After(30 * time.Second)
	for got == 0 {
		select {
		case <-deadline:
			t.Fatal("30 秒内没有产出任何网格化区段")
		default:
		}
		got += len(s.Drain(64))
		time.Sleep(10 * time.Millisecond)
	}
}

// TestStreamerSurvivesPanickingWork 验证单个区块生成失败
// 不会拖垮整个流式加载器（spec §7.2 goroutine 崩溃隔离）。
func TestStreamerSurvivesPanickingWork(t *testing.T) {
	s := client.NewStreamer(worldgen.New(1), assets.NewRegistry(), 2)
	defer s.Close()

	s.InjectPanicForTest(core.ChunkPos{X: 1, Z: 1})
	s.SetCenter(core.ChunkPos{X: 0, Z: 0}, 2)

	got := 0
	deadline := time.After(30 * time.Second)
	for got < 5 {
		select {
		case <-deadline:
			t.Fatalf("一个区块 panic 后只产出了 %d 个区段，加载器被拖垮了", got)
		default:
		}
		got += len(s.Drain(64))
		time.Sleep(10 * time.Millisecond)
	}
}
```

- [ ] **Step 4: 实现流式加载**

`internal/client/streamer.go` 的要点：

```go
// MeshedSection 是一个已生成并网格化好的区段。
type MeshedSection struct {
	Pos        core.SectionPos
	Quads      []mesh.Quad
	Conn       mesh.Connectivity
	Generation uint64 // 任务创建时的 SetCenter 代次，供过期诊断
}

// Streamer 在 worker pool 上并行生成与网格化区块（spec §4.3）。
//
// 生成与网格化是纯函数（同种子同坐标必得同结果），因此可以随意并行，
// 不需要任何锁——这正是把它们设计成纯函数的回报。
type Streamer struct {
	gen     *worldgen.Generator
	reg     *assets.Registry
	jobs    chan core.ChunkPos
	results chan MeshedSection
	// chunks 只缓存当前 wanted 集合及一圈 halo；网格化 AO 需要 3×3 邻域。
	chunks  map[core.ChunkPos]*world.Chunk
	queued  map[core.ChunkPos]uint64
	inFlight map[core.ChunkPos]uint64
	center  core.ChunkPos
	radius  int
	generation uint64
	mu      sync.Mutex
	wg      sync.WaitGroup
	closed  chan struct{}
	panicAt map[core.ChunkPos]bool // 仅供测试注入
}
```

worker 主体必须包 `recover`：

```go
func (s *Streamer) work() {
	defer s.wg.Done()
	for pos := range s.jobs {
		s.handleOne(pos)
	}
}

// handleOne 处理一个区块。单个区块失败不得拖垮整个加载器——
// 记录错误、跳过该区块、继续（spec §7.2）。
func (s *Streamer) handleOne(pos core.ChunkPos) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("区块生成失败", "chunk", pos, "panic", r)
		}
	}()
		// 生成并缓存；检查本区块及周围 8 个区块，只有目标区块的
		// 3×3 水平邻域齐备时才网格化。结果携带任务代次送 results。
	}
```

`SetCenter` 必须完成以下状态转换，不能只向 channel 追加任务：

1. 代次加一，计算半径 `radius+1` 的 wanted chunk 集合；额外一圈是 AO/面邻域 halo，保证可见半径内区段都能完成网格化。
2. 对 wanted 集合按到中心的距离排序，只派发不在 `chunks/queued/inFlight` 中的区块。
3. 从 `chunks` 删除 wanted 之外的区块，同时清理对应 queued 标记；已在执行的旧任务不能强杀，完成时重新检查当前 wanted 集合：已离开 wanted 才丢弃，仍在 wanted 的旧代次结果仍然有效，不能仅因代次变化造成缺块。
4. 新区块到达后，重新检查它周围 3×3 中哪些中心区块刚刚凑齐邻域；每个 `(section, generation)` 最多产出一次。
5. `Drain` 按当前可见/wanted 集合过滤结果，而不是机械要求 generation 相等；`Close` 必须能在 jobs/results 背压下唤醒所有 worker，不能因发送结果而死锁。

`Stats` 仅用于测试与诊断。加入 `TestStreamerCacheRemainsBoundedWhileTravelling`：把中心沿 X 连续移动 100 次，每次等待新代次有结果，最终断言 `CachedChunks <= (2*(radius+1)+1)^2`，且旧中心的结果不会在新代次被 Drain 出来。

- [ ] **Step 5: 运行测试**

```bash
go test ./internal/client/ -v
```

预期：全部 PASS，包括 panic 隔离测试。

- [ ] **Step 6: 写主程序**

`cmd/mcgo/main.go`：GLFW 建窗（1440p）→ 创建 `gfx.Device` 与 `Renderer` → 起 `Streamer`（`runtime.NumCPU()-2` 个 worker，至少 1）→ 主循环：

1. 处理输入：WASD 水平移动、空格/Shift 升降、鼠标控制视角（捕获光标）、`Esc` 释放光标
2. 相机移动后若跨了区块，`streamer.SetCenter(newCenter, 32)`
3. `renderer.BeginFrame()` 重置上传预算
4. `streamer.Drain(64)` 取当前仍在可见集合内的结果并 `renderer.QueueSection`
5. `renderer.FlushUploads(newCenter)`；超预算结果保留到后续帧，不能丢弃
6. `renderer.DropOutside(newCenter, 32)` 卸载超出视距的 GPU 分配并清理 pending；Streamer 在 `SetCenter` 内淘汰半径外 CPU chunks
7. `renderer.Render(...)`
8. terrain render pass 结束后 `hiZ.build(...)`，供下一帧使用
9. Present

窗口尺寸变化时 `surface.Resize` + 重建深度缓冲 + `hiZ.resize` + 更新 `camera.Aspect`。

- [ ] **Step 7: 实机验证**

```bash
go run ./cmd/mcgo
```

**M1 出口条件检查清单：**

- [ ] 能用 WASD + 鼠标自由飞行，视角不翻转
- [ ] 视距 32 区块，远处地形随飞行连续加载，没有肉眼可见的卡顿
- [ ] 转身时没有破洞或闪烁
- [ ] 飞行 5 分钟后帧率不下降（显存池没有碎片化失控）
- [ ] 飞行 5 分钟后内存占用稳定（区段卸载正常，没有泄漏）

- [ ] **Step 8: 提交**

```bash
git add internal/client cmd/mcgo
git commit -m "feat: 自由飞行相机、输入与区块流式加载，M1 可飞行"
```

---

### Task 17: 性能基准与 CI 门禁

**Files:**
- Create: `internal/render/bench_test.go`
- Create: `internal/archcheck/deps_test.go`
- Create: `internal/client/perf.go`
- Create: `internal/client/perf_test.go`
- Create: `internal/client/rss_darwin.go`
- Create: `internal/client/rss_other.go`
- Create: `cmd/perfcheck/main.go`
- Modify: `cmd/mcgo/main.go`（增加固定场景 benchmark 模式）
- Create: `.github/workflows/ci.yml`
- Create: `docs/notes/perf-baseline.md`
- Create: `docs/notes/perf-baseline.json`

**Interfaces:**
- Consumes: 前面所有任务
- Produces: 架构 CI 门禁 + 可复现的本机性能门禁，防止约束被无声侵蚀

spec §7.1 明确要求：**性能是需求的一部分，所以要有性能门禁**。GitHub 托管 runner 的硬件不稳定，不能拿它比较绝对帧时间；因此 CI 负责架构、正确性与性能工具可运行，本计划另提供在基准开发机上执行的固定场景比较命令。两者不能混称为同一种门禁。

- [ ] **Step 1: 写依赖方向门禁**

这是 Global Constraints 中依赖约束的强制执行点。

`internal/archcheck/deps_test.go`：

```go
package archcheck_test

import (
	"os/exec"
	"strings"
	"testing"
)

// allowed 列出每个内部包允许直接依赖的内部包（spec §3.1）。
var allowed = map[string][]string{
	"internal/archcheck": {},
	"internal/core":     {},
	"internal/gfx":      {},
	"internal/world":    {"internal/core"},
	"internal/worldgen": {"internal/core", "internal/world"},
	"internal/mesh":     {"internal/core", "internal/world"},
	"internal/assets":   {"internal/core", "internal/world", "internal/mesh", "internal/worldgen", "internal/gfx"},
	"internal/render":   {"internal/core", "internal/world", "internal/mesh", "internal/assets", "internal/gfx"},
	"internal/client":   {"internal/core", "internal/world", "internal/mesh", "internal/assets", "internal/worldgen", "internal/render", "internal/gfx"},
}

// TestInternalDependenciesAreOneWay 强制单向依赖。
//
// 靠自觉守不住这条：某次「就先临时 import 一下」之后，
// 层次就永久地糊在一起了。
func TestInternalDependenciesAreOneWay(t *testing.T) {
	// 先枚举实际包，防止新增 internal 包却忘记登记而完全绕过门禁。
	pkgsOut, err := exec.Command("go", "list", "-f", "{{.ImportPath}}", "./internal/...").Output()
	if err != nil {
		t.Fatalf("枚举 internal 包失败: %v", err)
	}
	actual := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(pkgsOut)), "\n") {
		pkg := strings.TrimPrefix(strings.TrimSpace(line), "minecraft-go/")
		if pkg != "" {
			actual[pkg] = true
			if _, ok := allowed[pkg]; !ok {
				t.Errorf("新增内部包 %s 未登记依赖白名单", pkg)
			}
		}
	}
	for pkg := range allowed {
		if !actual[pkg] {
			t.Errorf("依赖白名单中的包 %s 不存在", pkg)
		}
	}

	for pkg, allow := range allowed {
		out, err := exec.Command("go", "list", "-deps", "./"+pkg).Output()
		if err != nil {
			t.Fatalf("go list -deps ./%s 失败: %v", pkg, err)
		}
		allowSet := map[string]bool{}
		for _, a := range allow {
			allowSet[a] = true
		}
		for _, line := range strings.Split(string(out), "\n") {
			dep := strings.TrimPrefix(strings.TrimSpace(line), "minecraft-go/")
			if dep == line || dep == pkg || dep == "" {
				continue // 不是本仓库的包，或是自己
			}
			if !allowSet[dep] {
				t.Errorf("%s 不允许依赖 %s", pkg, dep)
			}
		}
	}
}

// TestOnlyGfxImportsWebGPU 确保 GPU 绑定不泄漏到上层。
//
// 这条一旦破了，spec §5.6 的「换绑定时 render/ 一行不改」就不成立了，
// 而 WebGPU 绑定的不成熟正是本项目的头号风险（spec §9）。
func TestOnlyGfxImportsWebGPU(t *testing.T) {
	out, err := exec.Command("go", "list", "-f",
		"{{.ImportPath}} {{join .Imports \" \"}}", "./...").Output()
	if err != nil {
		t.Fatalf("go list 失败: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, "webgpu") {
			continue
		}
		pkg := strings.Fields(line)[0]
		if pkg != "minecraft-go/internal/gfx" {
			t.Errorf("%s 直接 import 了 WebGPU 绑定，只有 internal/gfx 可以", pkg)
		}
	}
}
```

- [ ] **Step 2: 运行门禁测试**

```bash
go test ./internal/archcheck/ -v
```

预期：PASS。若失败，**修代码而不是修白名单**——白名单是 spec §3.1 的机器可读版本，放宽它等于改架构。

- [ ] **Step 3: 写性能基准**

`internal/render/bench_test.go`：

```go
package render_test

import (
	"fmt"
	"testing"

	"minecraft-go/internal/assets"
	"minecraft-go/internal/core"
	"minecraft-go/internal/mesh"
	"minecraft-go/internal/world"
	"minecraft-go/internal/worldgen"
)

// benchWorld 用固定种子生成一片固定地形，保证基准可比。
// 换种子等于换测试对象，历史数字就作废了。
const benchSeed = 20260726

// BenchmarkGenerateChunk 地形生成吞吐。
func BenchmarkGenerateChunk(b *testing.B) {
	g := worldgen.New(benchSeed)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = g.GenerateChunk(core.ChunkPos{X: int32(i & 63), Z: int32((i >> 6) & 63)})
	}
}

// BenchmarkMeshChunk 一整根区块柱的网格化耗时。
//
// 这是流式加载能否跟上飞行速度的决定性指标：
// 32 视距下每秒可能要网格化上百个区段。
func BenchmarkMeshChunk(b *testing.B) {
	g := worldgen.New(benchSeed)
	reg := assets.NewRegistry()
	chunks := map[core.ChunkPos]*world.Chunk{}
	for cx := int32(-1); cx <= 1; cx++ {
		for cz := int32(-1); cz <= 1; cz++ {
			chunks[core.ChunkPos{X: cx, Z: cz}] = g.GenerateChunk(core.ChunkPos{X: cx, Z: cz})
		}
	}
	get := func(p core.ChunkPos) *world.Chunk { return chunks[p] }
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for si := 0; si < core.SectionsPerChunk; si++ {
			n := world.NeighborhoodAt(get, core.ChunkPos{}, si)
			_ = mesh.MeshSection(n, reg)
		}
	}
}

// BenchmarkVisibleSections 可见性图 BFS 的每帧开销。
// 它跑在渲染线程上，必须远小于一帧的预算。
func BenchmarkVisibleSections(b *testing.B) {
	g := worldgen.New(benchSeed)
	reg := assets.NewRegistry()
	conns := map[core.SectionPos]mesh.Connectivity{}
	for cx := int32(-32); cx <= 32; cx++ {
		for cz := int32(-32); cz <= 32; cz++ {
			ch := g.GenerateChunk(core.ChunkPos{X: cx, Z: cz})
			for si := 0; si < core.SectionsPerChunk; si++ {
				conns[core.SectionPos{X: cx, Y: int32(si), Z: cz}] =
					mesh.ComputeConnectivity(ch.Section(si), reg)
			}
		}
	}
	lookup := func(p core.SectionPos) (mesh.Connectivity, bool) {
		c, ok := conns[p]
		return c, ok
	}
	const sectionRecBytes = 32
	for _, radius := range []int{16, 24, 32} {
		b.Run(fmt.Sprintf("r%d", radius), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				got := mesh.VisibleSections(core.SectionPos{Y: 8}, radius,
					mesh.EverythingVisible(), lookup)
				b.ReportMetric(float64(len(got)), "candidate_sections")
				b.ReportMetric(float64(len(got)*sectionRecBytes), "candidate_bytes/frame")
			}
		})
	}
}

// BenchmarkPalettePayloadEstimate 只测调色板 payload 的逻辑大小。
// 它用于防止压缩率回退，不代表进程 RSS：Chunk/Section 对象、map bucket、
// 网格、队列、客户端状态与 GPU 资源均不在这里。<2 GB 的完成门禁由
// cmd/mcgo --benchmark 对真实进程峰值 RSS 采样。
func BenchmarkPalettePayloadEstimate(b *testing.B) {
	g := worldgen.New(benchSeed)
	var totalBytes int
	const radius = 32
	for cx := int32(-radius); cx <= radius; cx++ {
		for cz := int32(-radius); cz <= radius; cz++ {
			ch := g.GenerateChunk(core.ChunkPos{X: cx, Z: cz})
			for si := 0; si < core.SectionsPerChunk; si++ {
				totalBytes += ch.Section(si).Blocks.PayloadBytes()
			}
		}
	}
	b.ReportMetric(float64(totalBytes)/(1<<20), "MB")
	// 65×65 区块 × 24 区段的朴素方块 payload 是 820 MB（spec §4.1）。
	if mb := totalBytes / (1 << 20); mb > 300 {
		b.Fatalf("32 视距调色板 payload 估算 %d MB，超出预算", mb)
	}
}
```

- [ ] **Step 4: 记录基线数字**

先实现 `cmd/mcgo --benchmark`：

1. 强制窗口内容区为 2560×1440，`surface.SetPresentMode(PresentModeAutoNoVSync)`；若平台无法关闭 VSync，直接报错，不能生成无意义的基线。
2. 固定种子 `20260726`、视距 32。等 `Streamer.Stats()` 显示 queued/in-flight 均为 0、缓存达到 wanted 大小，且 renderer `PendingUploads()==0` 后再开始计时；设 5 分钟超时，加载未完成必须失败而不是永远等待。
3. 预热 10 秒；随后跑两个代码内固定的相机阶段：静止 60 秒、以恒速穿越地形并周期转向 120 秒。路径、速度和时长是基准契约，修改必须显式提升 `scenario_version`。
4. 每帧记录 wall-clock frame time、候选区段数、candidate buffer 上传字节、存活实例数和 pending uploads；每秒采样进程 RSS。首次加载时间另记，不混进稳态帧时间。
5. 输出 JSON，至少包含硬件、OS、Go 版本、git commit、场景版本，以及两个阶段各自的 fps、p50/p95/p99/max、峰值 RSS。任何阶段 p99 ≥12 ms、fps <100 或峰值 RSS ≥2 GiB 时命令退出非零。

`internal/client/perf.go` 负责无分配的环形采样与分位数汇总；`perf_test.go` 用已知序列验证 p50/p95/p99，避免排序下标写错。真实进程 RSS 使用带 build tag 的平台实现；darwin/arm64 是 M1 的基准平台，其他平台可以明确返回“不支持 RSS 门禁”，不得拿 `PalettedContainer.PayloadBytes` 冒充 RSS。

`cmd/perfcheck` 比较两个相同 `scenario_version`、相同硬件标识的 JSON：帧时间或峰值 RSS 退化超过 20% 时失败；硬件或场景不同则拒绝比较，而非给出误导结论。

```bash
# 纯 Go 微基准，保留人类可读记录
go test ./... -bench=. -benchmem -run='^$' | tee docs/notes/perf-baseline.md

# 开发机上的真实 1440p 固定场景；此命令本身执行绝对性能门禁
go run ./cmd/mcgo --benchmark --perf-output /tmp/mcgo-perf.json
cp /tmp/mcgo-perf.json docs/notes/perf-baseline.json

# 后续提交在同一开发机上比较回归
go run ./cmd/perfcheck \
  -baseline docs/notes/perf-baseline.json \
  -current /tmp/mcgo-perf.json \
  -max-regression 0.20
```

在 `docs/notes/perf-baseline.md` 顶部手写一段说明：

```markdown
# 性能基线

- 记录日期：<填写>
- 硬件：<填写具体型号，如 Apple M3 Pro / 18 GB>
- Go 版本：go1.26.0 darwin/arm64

下方是纯 Go 微基准的可读记录；真实 1440p 场景数据在 perf-baseline.json。
GitHub CI 不比较跨机器绝对值。性能回归由同一台基准开发机运行
cmd/mcgo --benchmark，再用 cmd/perfcheck 比较；退化超过 20% 判红。
换硬件或 scenario_version 后必须显式重建基线。
```

- [ ] **Step 5: 写 CI 配置**

`.github/workflows/ci.yml`：

```yaml
name: CI

on:
  push:
  pull_request:

jobs:
  test:
    runs-on: macos-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.26'

      - name: 依赖方向与绑定隔离门禁
        run: go test ./internal/archcheck/ -v

      - name: 单元测试
        # GPU 相关测试在无适配器的 runner 上会自行 skip，
        # 纯 Go 的逻辑测试（占绝大多数）照常执行。
        run: go test ./... -race

      - name: 静态检查
        run: |
          go vet ./...
          gofmt -l . | tee /tmp/fmt.txt
          test ! -s /tmp/fmt.txt

      - name: 微基准与 payload 阈值（不比较跨机器帧时间）
        run: go test ./... -bench=. -benchtime=1x -run='^$'
```

**说明**：跨机器的绝对帧时间没有可比性，所以托管 CI 只保证基准与性能工具能编译运行，并执行 `BenchmarkPalettePayloadEstimate` 的平台无关阈值。真实 fps、p99 与进程 RSS 的绝对门禁由同一台基准开发机执行 `cmd/mcgo --benchmark`；相对回归由 `cmd/perfcheck` 比较 JSON。文档与 CI 不再声称托管 runner 会做它实际没有做的 20% 比较。

- [ ] **Step 6: 跑完整验证**

```bash
go test ./... -race
go vet ./...
gofmt -l .
go test ./... -bench=. -benchtime=1x -run='^$'
go run ./cmd/mcgo --benchmark --perf-output /tmp/mcgo-perf.json
go run ./cmd/perfcheck -baseline docs/notes/perf-baseline.json \
  -current /tmp/mcgo-perf.json -max-regression 0.20
```

预期：全部通过，`gofmt -l` 无输出；固定场景的静止与飞行阶段均达到 ≥100 fps、p99 <12 ms，峰值 RSS <2 GiB，且相对基线没有超过 20% 的退化。

- [ ] **Step 7: 提交**

```bash
git add internal/archcheck internal/render/bench_test.go internal/client cmd/mcgo cmd/perfcheck .github docs/notes/perf-baseline.md docs/notes/perf-baseline.json
git commit -m "chore: 性能基准与 CI 门禁，锁住架构约束与性能目标"
```

---

## 完成标准

M0 与 M1 全部任务完成后，应满足：

| 检查项 | 验证方式 |
|---|---|
| M0：实例数由 GPU 决定 | `docs/notes/webgpu-api.md` 的 M0 结论四项全勾 |
| 32 视距自由飞行 | `go run ./cmd/mcgo`，对照 Task 16 Step 7 清单 |
| 帧时间达标 | 基准开发机运行 `cmd/mcgo --benchmark`：静止与飞行均 ≥100 fps、p99 <12 ms |
| 驻留内存 | 同一固定场景报告峰值进程 RSS <2 GiB；`BenchmarkPalettePayloadEstimate` 仅作压缩率辅助指标 |
| 性能未显著回退 | 同机运行 `cmd/perfcheck`，相对 `perf-baseline.json` 退化不超过 20% |
| 依赖方向未被破坏 | `go test ./internal/archcheck/` |
| GPU 绑定未泄漏到上层 | 同上，`TestOnlyGfxImportsWebGPU` |
| 全部测试通过 | `go test ./... -race` |

**M1 完成后要做的一件事**：拿实测数字回头修订 spec §1.2 的性能指标。那些数字是估的，spec §9 已经把「性能目标定得过于乐观」列为已知风险，M1 正是校准它的时机。

## 不在本计划内

M2–M5 各自单独出 spec 与计划（spec §8.1）。本计划**刻意不做**以下内容，遇到时不要顺手加：

- 方块的挖掘与放置、射线拾取（M2）
- 内置服务端与 tick 循环（M2）——spec §4.3 的单写者模型在 M2 落地；M1 只用到它的 COW 部分（`Clone()`），因为地形生成与网格化是纯函数，本来就不需要锁
- 完整的区块生命周期状态机（M2）——spec §4.2 的 `Empty→Generating→Populated→Lit→Ready` 七态；M1 的 `Streamer` 只走「生成 → 网格化」两步，没有 `Populated` 阶段（无地物装饰）也没有 `Lit` 阶段（无光照传播）
- 存档与联机（M3）
- 光照传播（M4）——M1 的 `Light` 字段固定为 `0xF0`（全天空光）
- 方块更新调度（M4）——spec §4.4 的邻居更新 / 计划刻 / 随机刻，M1 的世界是静态的
- 实体（M4）——spec §4.5，M1 没有任何实体，相机不是实体
- 生物群系、洞穴、地物装饰（M4）——M1 只有高度图地形
- 半透明 pass 与水（M4）
- 多维度（M4 之后）——spec §4.6 的 `Dimension` 顶层容器；M1 只有一个隐式主世界，`core.SectionPos` 尚未携带 dimension ID
- 显存碎片整理（M5）——M1 只提供 `Fragmentation()` 指标，不做整理
