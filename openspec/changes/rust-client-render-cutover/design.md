# Design: rust-client-render-cutover

## Context

见 proposal.md「Why」。现状:R2b 后 Rust 渲染器覆盖全部 pass,完整帧双后端
零像素差异;生产仍走 Go `internal/gfx`(oliverbestmann/webgpu)。窗口与
渲染器同在 `mornlea_client` crate(R1 铺垫),surface 创建可全程留在 Rust。
Go 侧 GPU 耦合点:`GlyphAtlas`(tofu 写入 + FlushUploads 的 WriteRegion)、
HUD 图集(UploadTo/WriteLayer)、各 renderer 构造收 `gfx.Device`、app 帧
循环驱动 8 个 renderer、capture/benchmark 直读 gfx 纹理。

## Goals / Non-Goals

- Goals:生产切换(窗口 + 离屏)、Go GPU 半部删除、golden 字节不变、
  每帧单次渲染 FFI、可分段回退。
- Non-Goals:CPU 准备迁移;golden/阈值修改;跨平台;R3(HUD 布局迁移)。

## Decisions

### 窗口模式:renderer 关联 window 句柄(ABI 3→4)

`render_create_windowed(abi, window_handle, out_handle)`:从窗口表取
winit window,`instance.create_surface`,按窗口 framebuffer 尺寸配置
(Bgra8UnormSrgb、FIFO,镜像 Go surface 配置);渲染器仍持有离屏等价的
depth/HiZ。`render_frame` 在窗口模式下 acquire surface 纹理为 color
target,提交后 present;acquire 失败(遮挡/过期)返回专用 SKIPPED 状态,
调用方跳帧。`render_resize(abi, handle, w, h)` 重建 target/HiZ 并重配
surface。窗口模式渲染器与窗口同线程(thread-local 窗口表约束),因此
windowed 渲染器也放窗口线程表;离屏渲染器保持全局 Mutex 表。

### Go 帧装配替换 pass 驱动

`app_frame` 的渲染段重写:各 renderer 的 Prepare/编码保留(布局与字节
产物即 pass 段),Render 调用与 encoder/gfx 目标全部移除,代之以
`client.RenderFrame` 装配 + 一次 `Renderer.RenderFrame()`。可见列表由
Go 计算(`mesh.VisibleSectionsInto`,与对照测试同路径);FrameStats 类
统计以 Go 侧候选计数继续提供。

### GlyphAtlas 与 HUD 图集经 sink 解耦

`render` 包新增 `GlyphSink` 接口(`WriteGlyphRect(x, y, w, h, pixels)`),
`GlyphAtlas` 构造改收 sink,tofu 与 FlushUploads 的写入全部走 sink;
`Glyph`/`Kern`/布局 API 不变。生产 sink 由 cmd/mornlea 以
`client.Renderer.UploadGlyphRect` 适配;HUD 图集像素经
`Renderer.UploadHUDAtlas` 一次上传(`buildHotbarTextureAtlas` 保留)。
`gfx.Device`/`TextureView` 相关构造参数与字段随删除段移除。

### 删除清单与顺序(先切换后删除,可分段回退)

1. 切换段:窗口/离屏入口切到 client.Renderer,Go 渲染栈成为死代码但保留,
   golden 与全量门禁在此点必须全绿;
2. 删除段:`internal/gfx` 全包、webgpu 依赖(go.mod/go.sum)、render 包
   GPU 文件(renderer*.go 的 GPU 路径、cull.go、hiz.go、pool.go 的 GPU
   关联、avatar/drop/outline/name_tag/damage_overlay/debug_panel 的
   pipeline 半部)、hud renderer GPU 半部、assets.UploadTo、双后端对照
   测试与 render 包 GPU 单测;保留并收敛 CPU 半部文件。
3. 门禁段:archcheck 移除 gfx 白名单与 webgpu 禁令中的 gfx 例外(webgpu
   对全仓变为禁止)、依赖表更新;CLAUDE.md/AGENTS.md/progress 改写。

### 对照测试退役,golden 接棒

双后端对照测试的 oracle 使命随 Go 渲染器删除而完成;既有 capture golden
(字节不变)成为长期视觉门禁。crate 内的 GPU smoke 与解析矩阵测试全部
保留。

## 依赖与并发

- `internal/render`/`internal/render/hud` 不再依赖 gfx;archcheck 依赖表
  相应收缩;client ABI 仍只被 `internal/client` 接触。
- 窗口模式渲染器限主线程(与窗口同表);离屏渲染器跨线程(Mutex 表)。
- 字形 worker 并发模型不变(仅上传出口改 sink)。

## 兼容与回退

- 协议/存档/benchmark/golden 字节不变;client ABI 3→4(既有入口签名不变,
  新增 windowed/resize);Linux 专服零影响。
- 回退:切换段与删除段独立提交;删除前任意点 revert 即回 Go 渲染器;
  合并后 revert 整支 PR 回 R2b 基线。

## 验证方法

- Rust:windowed/resize 入口校验拒绝单测(无头);SKIPPED 语义单测
  (离屏路径模拟不可行处以窗口人工/注入验收覆盖)。
- Go:全部既有 capture golden 零改动通过(核心门禁);benchmark 冒烟;
  全量 race、archcheck、vet/gofmt、openspec strict。
- 真实窗口:CGEvent 注入自动化验收(复用 R1 工具):启动、截图比对场景
  内容、resize(SetContentSize)、关闭路径。

## 平台假设

同前:渲染仅 darwin;Linux CI 验证空库编译与专服。切换后性能数值
(benchmark/perfcheck)只记录;报告完整性与真实错误仍是门禁。
