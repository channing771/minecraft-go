# Change: rust-client-render-terrain

## Why

"GPU 与窗口迁 Rust"路线图的 R2a:渲染每帧经 `internal/gfx` 产生大量碎 FFI
调用(render 包 637 处 gfx 引用),是热路径迁移的最大收益点,也是全 Rust
客户端的必经之路。R2 整体过大,按已批准的三期分解,本期在 `mornlea_client`
内建**纯离屏**的 wgpu 世界地形渲染器并以双后端 golden 对照验证;生产客户端
继续使用 Go 渲染器,窗口 surface、实体/文本/HUD、入口切换与删除留给
R2b/R2c。

## What Changes

- Rust:`mornlea_client` 新增 render 模块——wgpu device/queue、离屏
  color+depth target、terrain pass(实例化紧凑 quad)、sky/云 pass、GPU
  culling(cull.wgsl)与 HiZ(hiz_build/hiz_copy);WGSL 以 `include_str!`
  相对路径直引 `internal/render/shader/*.wgsl` 保持单源。client ABI 1→2,
  新增 `render_create/destroy`、`render_upload_section`、
  `render_drop_section`、`render_frame`(相机+日照+可见 section 列表)、
  `render_readback`。
- Go:`internal/client` 新增 render 绑定(仅测试与后续期使用);双后端
  对照测试在既有 capture 场景上同帧驱动 Go Renderer 与 Rust 渲染器,
  以现有 `diffThreshold` 双阈值比较回读图像。
- 生产行为零变化:客户端渲染入口、golden 基线、协议、存档全部不动。

## Capabilities

### New Capabilities

- `rust-client-render-terrain`: Rust 离屏世界地形渲染器的行为契约——
  与 Go 渲染器的双后端图像一致性、mesh 数据单次过境、每帧单次渲染 FFI、
  ABI 输入校验拒绝与生产路径不变。

### Modified Capabilities

无。`voxel-visual-presentation` 等既有视觉规格描述的可观察行为不变;
本期只新增平行实现与对照门禁。

## Impact

- 受影响包:`engine/crates/mornlea_client`、`internal/client`、
  `cmd/mornlea`(仅新增对照测试)、`engine/include/mornlea_client.h`。
- 兼容性:client ABI 1→2(窗口入口不变,新增 render 入口族);协议 v16、
  存档 schema、benchmark scenario v16、golden 基线字节均不变;Linux 专服
  仍不链接 client 库。
- 依赖:`mornlea_client` 新增 wgpu crate(darwin target);Cargo.lock 锁定。
- 性能:平行实现不接生产,数值只记录;双后端对照测试限 darwin 运行。
- 并发:渲染器句柄与窗口同用 thread-local 表,全部操作限创建线程。

## 非目标

不接窗口 surface;不迁实体/文本/HUD/debug 面板;不切换生产渲染入口;
不删除任何 Go 渲染代码;不迁移 BFS 连通性可见性计算(纯 CPU 遍历,留 Go);
不修改 golden 基线与 `diffThreshold` 阈值。
