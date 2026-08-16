# Design: rust-client-window

## Context

见 proposal.md「Why」。现状:`internal/client/window.go`(~200 行)封装 GLFW——
29 键轮询、鼠标键、光标捕获、1024 rune 有界文本队列(char 回调)、NSWindow
句柄、尺寸/浮动/焦点/关闭;`cmd/mornlea`/`cmd/gfxspike` 主 goroutine
`LockOSThread` 后每帧调 `Poll()`,app 依赖接口而非具体类型。`internal/gfx`
只消费 `NativeWindowHandle{NSWindow*}`,与窗口库无耦合。engine workspace 现有
单 crate `mornlea_engine`(ABI v3,纯确定性内核)。

## Goals / Non-Goals

- Goals:winit 窗口/事件循环独占生产;`Window` API 形状与语义不变;每帧
  1 次窗口 FFI;删除 `go-gl/glfw`;为 R2(渲染核心迁 Rust)取得事件循环
  所有权。
- Non-Goals:不迁 wgpu/渲染;不做跨平台窗口;不改 `internal/gfx`;
  不引入 Rust 自建线程。

## Decisions

### 新 crate `mornlea_client`,独立 cdylib 与 ABI

窗口是 OS 交互代码,不进 `mornlea_engine`(保持其纯确定性内核身份,也为 R2
渲染器预留同一归属地)。workspace 增加 `crates/mornlea_client`,产物
`libmornlea_client.dylib`,`mornlea_client_abi_version` 从 1 起与 engine ABI
互不耦合。生产依赖 winit + raw-window-handle(darwin target);Cargo.lock 锁定。
Linux 专服不链接该库,`libmornlea_engine` release unit 约定不动。
备选被否:并入 engine crate(target 门控)——单 dylib 少一个构建产物,但
winit/objc 依赖混进确定性内核 crate,架构叙事变浑,R2 会进一步恶化。

### 控制权留 Go:`pump_app_events` 泵模型

Go 主线程(已 `LockOSThread`)每帧调用 `mornlea_client_window_poll`,Rust 侧
以零超时 `pump_app_events` 处理完积压事件后立即返回。不反转控制权(winit
`run_app` 独占主循环是 R2 之后的事)。macOS 要求窗口/事件 API 在主线程:
Rust 侧记录创建线程并在所有入口校验,跨线程调用返回错误状态。

### 每帧单次 FFI:固定布局输入快照

`poll` 的输出是一个固定 O(1) 布局快照 + 变长文本段:

- 快照区(固定偏移,LE):layout version、键位 bitmask u64(29 键各占 1 位,
  位序即 Go `Key` 常量的 iota 值,新键只能追加高位)、鼠标键位 u32、光标
  x/y f64、framebuffer w/h u32、content w/h u32、should_close/text_overflow
  标志、文本字符数 u32。
- 文本段:紧随快照区的 UTF-32 code point 数组,上限 1024;溢出丢弃并置
  overflow 标志(语义与现 `enqueueTextInput` 一致)。

Go `Window.Poll()` 一次取回并缓存;`KeyDown` 等读取全部走缓存。低频操作
(create/destroy、SetCursorCaptured、SetContentSize、SetFloating、Focus、
CancelClose、native handle 查询)各自独立入口,错误以状态码返回,Go 转稳定
中文 panic 文案(与 nativeabi 模式一致)。

### 键位与文本语义映射

- 键位:winit `PhysicalKey::Code` → bitmask 位;映射表在 Rust,29 键与
  GLFW 语义一一对应(WASD、Space、LShift、LCtrl、Esc、1..9、E、Q、F3/F5/F6、
  方向键、Enter、LAlt、Backspace)。按下与 repeat 均视为 down(GLFW
  `Press|Repeat` 语义)。
- 文本:窗口启用 IME;`Ime::Commit` 的字符串与非 IME 路径 `KeyEvent.text`
  (仅 pressed,过滤控制字符,与 GLFW char 回调域一致)进入同一有界队列。
- 光标捕获:`set_cursor_visible(false)` + `set_cursor_grab(Locked)`(macOS
  支持;失败降级 `Confined` 并记录)。捕获期间光标位置由 `DeviceEvent::
  MouseMotion` delta 累计合成,保证越界连续(GLFW `CursorDisabled` 的等价
  语义);释放后恢复窗口绝对坐标。

### Go 侧结构

`internal/client/window.go` 重写:cgo 直接链接 `libmornlea_client`(client 库
的 C ABI 只被 `internal/client` 接触,与 `internal/nativeabi` 之于 engine 同
构;archcheck 通过既有依赖白名单约束)。`Window` 保持现有方法集;新增仅内部
快照缓存字段。`cmd/gfxspike` 改用 `client.NewWindow`,删除直接 GLFW 引用。
头文件放 `engine/include/mornlea_client.h`。

## 依赖与并发

- 依赖方向:`internal/client` → client C ABI;不新增 Go 包间依赖边
  (window.go 本就在 client 包)。`cmd/gfxspike` 已依赖 client。
- 线程:全部窗口 FFI 限定主线程;Rust 入口做线程校验,违约返回错误状态而非
  未定义行为。Rust 侧无自建线程、无全局可变状态跨线程共享。
- 生命周期:窗口句柄 destroy 后置无效;后续调用返回错误状态。

## 兼容与回退

- 协议/存档/benchmark 字节不变;`internal/gfx` 与渲染零改动。
- darwin 客户端 binary 与 `libmornlea_client.dylib` 同版本 release unit;
  Makefile `rust` target 同时构建两个 crate。
- 回退:单 PR revert 恢复 GLFW 实现(go.sum 历史保留)。

## 验证方法

- Rust 单测(无头):键位映射表、文本队列有界/溢出、快照编码布局、线程与
  句柄校验拒绝——不创建真实窗口。
- Go 单测:快照解码与帧内缓存语义(注入固定字节)、`DrainTextInput` 既有
  测试保持;自动测试不开真实窗口(仓库纪律)。
- 人工验收清单(一次):移动/跳跃/快捷栏、聊天中文 IME 输入、光标捕获视角
  连续、窗口关闭与 Esc、调试面板按键。
- 门禁:`make rust`、focused race、archcheck、`go test ./... -race`、
  gofmt/vet、openspec strict。

## 平台假设

窗口生产路径仅 darwin(仓库现状);winit 的 macOS 后端要求主线程,由 Go
`LockOSThread` + Rust 入口线程校验双重保证。IME 与光标捕获的行为差异属于
人工验收范围,自动测试只锁定队列与快照的可无头验证语义。
