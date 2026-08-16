# Change: rust-client-window

## Why

客户端窗口与事件循环仍依赖第三方 Go GLFW 绑定(`go-gl/glfw`),且每帧对窗口做
约 30 次逐键轮询调用。作为"GPU 与窗口迁 Rust"路线图(R1→R3)的第一步,本变更
把窗口与事件循环迁入新的 Rust `mornlea_client` cdylib(winit):为后续 R2
(渲染核心迁 Rust)取得事件循环所有权,去除一个第三方依赖,并把每帧输入采集
收敛为单次 FFI 快照。

## What Changes

- Rust:workspace 新增 `engine/crates/mornlea_client` cdylib(client ABI v1,
  darwin-only 生产目标),基于 winit 提供窗口创建/销毁、每帧 `pump_app_events`
  驱动、固定布局输入快照(键位 bitmask、鼠标、光标、尺寸、close/overflow 标志
  + 有界 UTF-32 文本队列)、光标捕获、尺寸/浮动/焦点等低频操作与 NSWindow
  native handle 导出。
- Go:`internal/client/window.go` 底座从 GLFW 换为 client C ABI;`Window` 公共
  API 形状不变,`Poll()` 改为单次 FFI 快照 + 帧内缓存读取;`cmd/gfxspike` 同步
  切换;`go.mod` 删除 `go-gl/glfw`。
- 构建:`make rust` 同时构建 `libmornlea_engine` 与 `libmornlea_client`;
  Linux 专服不链接 client 库,release unit 约定不变。

## Capabilities

### New Capabilities

- `rust-client-window`: Rust winit 窗口独占客户端窗口与输入采集的行为契约——
  单次 FFI 输入快照、有界文本队列语义保持、光标捕获连续视角、ABI 输入校验
  拒绝与无 GLFW 生产依赖。

### Modified Capabilities

无。既有渲染、协议、存档与聊天输入的可观察行为规格不变;本变更只替换窗口
与输入采集的实现载体。

## Impact

- 受影响包:`internal/client`(window.go 重写)、`cmd/gfxspike`、
  `engine/`(新 crate 与 workspace)、`go.mod`/`go.sum`(删 GLFW)、Makefile。
- 调用方零改动:`cmd/mornlea` 依赖的 `Window` 接口方法与语义不变;
  `internal/gfx` 不动(仍从 NSWindow handle 创建 surface)。
- 兼容性:协议 v16、存档 schema、benchmark scenario v16 均不变;新增
  `mornlea_client` ABI v1,与 engine ABI(v3)互不依赖、各自演进;darwin
  客户端 binary 与 `libmornlea_client.dylib` 是同版本 release unit。
- 依赖:删除 `go-gl/glfw`;新增 crates.io 依赖(winit 系)并锁定 Cargo.lock。
- 并发:窗口全部操作仍限定 Go 主线程(`LockOSThread` 既有约定),Rust 侧
  不自建线程;快照缓存使同帧内输入读取自洽。
- 性能:每帧窗口 FFI 从 ~30 次逐键查询降为 1 次快照;数值只记录。

## 非目标

不迁移 wgpu/渲染(R2);不支持 Linux/Windows 窗口;不改变聊天输入的有界
Unicode 语义;不引入生产 GLFW fallback。
