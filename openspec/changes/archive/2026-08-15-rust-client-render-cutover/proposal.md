# Change: rust-client-render-cutover

## Why

R2a/R2b 已在 `mornlea_client` 内建成完整的平行 Rust 渲染器,并以完整帧零像素
差异通过双后端对照——切换的数学前提已成立。本期(R2c,路线图收官)把生产
渲染切换到 Rust 渲染器并删除 Go 渲染栈的 GPU 半部,兑现"渲染热路径零碎
FFI + 全 Rust 呈现"的目标;客户端每帧的 GPU 交互从数百次 gfx 调用收敛为
一次 frame FFI。

## What Changes

- Rust:client ABI 3→4——`render_create_windowed`(winit 窗口句柄 →
  wgpu surface,`render_frame` 内 acquire/present,遮挡/过期帧跳过语义
  镜像 Go `Surface.Acquire`)与 `render_resize`(重建 color/depth/HiZ 与
  surface 配置);离屏模式原样保留。
- Go 切换:`cmd/mornlea` 帧循环改为 RenderFrame 装配(相机 + 可见列表 +
  pass 段,全部编码函数已在);`GlyphAtlas` 纹理写点与 HUD 图集改经 sink
  接口重定向到 `client.Renderer`;各 pass renderer 构造不再收 `gfx.Device`;
  capture/materials-showcase/ai-companion/benchmark/gfxspike 切离屏
  `client.Renderer`。
- 删除:`internal/gfx` 全包、`oliverbestmann/webgpu` 依赖、render/hud 包的
  GPU 半部(pipeline/bind/pool/cull.go/hiz.go/renderer GPU 路径);CPU 半部
  (mesh 调度/budget、BFS 可见性、字形光栅化与布局、HUD/面板布局、实体
  编码)保留。双后端对照测试随 Go 渲染器退役,golden 接棒为长期视觉门禁。
- 文档与门禁:archcheck 白名单、"只有 internal/gfx 可导入 WebGPU"规则、
  CLAUDE.md/AGENTS.md 项目定位同步改写。

## Capabilities

### New Capabilities

- `rust-client-render-cutover`: 生产渲染由 Rust 渲染器独占的行为契约——
  golden 字节不变、窗口 surface 呈现语义、每帧单次渲染 FFI、无 Go 渲染
  fallback。

### Modified Capabilities

无新增行为变化;`visual-verification`、`voxel-visual-presentation` 等既有
视觉规格描述的可观察行为(golden、阈值、场景)MUST 原样成立,只更换实现
载体,不修改这些主规格。

## Impact

- 受影响包:`engine/crates/mornlea_client`、`internal/client`、
  `internal/render`(删 GPU 半部)、`internal/render/hud`、`internal/assets`
  (UploadTo 退役)、`internal/mesh`(仅调用方变化)、`cmd/mornlea`、
  `cmd/gfxspike`、`internal/archcheck`、`go.mod`(删 webgpu)。
- 兼容性:client ABI 3→4;协议 v16、存档 schema、benchmark scenario v16、
  golden 基线字节均不变;Linux 专服不受影响。
- 性能:每帧 GPU 交互从数百次 cgo gfx 调用收敛为 1 次 frame FFI + 变更时
  的资源上传;benchmark/perfcheck 数值只记录。
- 回退:切换与删除是先后两段提交;删除段之前的任意提交可单独 revert 回
  Go 渲染器。合并后回退整支 PR 即回到 R2b 基线。

## 非目标

不改变任何可观察渲染输出;不迁移 CPU 准备逻辑;不改 golden 与
`diffThreshold`;不做跨平台窗口/渲染;不删除 `internal/mesh` 与
`internal/physics` 等 engine 调用方。
