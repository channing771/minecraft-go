# rust-client-window Delta Spec

## Purpose

把客户端窗口与输入采集交由 Rust winit(`mornlea_client` cdylib)唯一生产实现,Go 保留 `Window` 领域 API 与帧内快照缓存,去除 GLFW 依赖并把每帧输入采集收敛为单次 FFI。

## ADDED Requirements

### Requirement: 窗口与输入采集由 Rust client 库独占生产

darwin 客户端的窗口创建/销毁、事件循环驱动、键盘/鼠标/光标/尺寸/关闭状态采集
与文本输入队列 MUST 由 `mornlea_client` 库独占生产;Go 生产路径 MUST 不依赖
GLFW 或其他窗口库,`go.mod` MUST 不含 `go-gl/glfw`。

#### Scenario: 客户端经 Rust 窗口完成输入驱动

- GIVEN darwin 客户端以 Rust client 库创建的窗口运行
- WHEN 主循环每帧调用 Poll
- THEN 移动/快捷栏/调试面板按键、鼠标按键、光标位置、framebuffer 与 content
  尺寸、关闭请求全部来自该次快照,行为与迁移前 GLFW 实现等价

#### Scenario: 生产代码不再引用 GLFW

- GIVEN 迁移完成后的仓库
- WHEN 检查 Go module 依赖与生产源码
- THEN 不存在对 `go-gl/glfw` 的 import 或 module 依赖

### Requirement: 每帧输入采集为单次 FFI 快照

`Window.Poll` MUST 恰好触发一次 client 库 FFI 调用,取回固定布局的输入快照;
同一帧内的 KeyDown/PrimaryButtonDown/SecondaryButtonDown/CursorPos/
FramebufferSize/ContentSize/ShouldClose 读取 MUST 来自该快照缓存,MUST NOT
产生额外窗口 FFI 调用。

#### Scenario: 同帧输入读取自洽且零额外调用

- GIVEN 一次 Poll 之后、下一次 Poll 之前
- WHEN 多次读取任意组合的按键/鼠标/光标/尺寸状态
- THEN 所有读取返回同一快照的值,窗口 FFI 调用计数保持为该帧 1 次

### Requirement: 有界文本输入语义保持

文本输入 MUST 保持既有有界语义:每帧随快照排空自上次 Poll 以来的 Unicode
字符,队列上限 1024 字符,溢出时 MUST 置 overflow 标志且丢弃超出部分;
DrainTextInput 的追加返回与清空语义 MUST 与迁移前一致。IME 组合提交的文本
MUST 进入同一队列。

#### Scenario: 队列溢出置标志且不越界

- GIVEN 两次 Poll 之间产生超过 1024 个字符输入
- WHEN 调用 DrainTextInput
- THEN 返回前 1024 个字符与 overflow=true,后续帧队列从空开始

#### Scenario: IME 提交文本进入聊天队列

- GIVEN 用户通过输入法提交一段 Unicode 文本
- WHEN 下一次 Poll 后调用 DrainTextInput
- THEN 提交的字符按顺序出现在返回中

### Requirement: 光标捕获保持连续视角语义

SetCursorCaptured(true) 之后 MUST 隐藏并锁定光标,视角输入 MUST 使用连续的
相对位移(不受屏幕边界钳制);SetCursorCaptured(false) 之后 MUST 恢复可见
光标与绝对位置读取。捕获状态切换 MUST 幂等。

#### Scenario: 捕获期间视角连续

- GIVEN 光标已捕获
- WHEN 鼠标持续向同一方向移动越过屏幕边界距离
- THEN CursorPos 报告的位置持续单调变化,不发生跳变或钳制

### Requirement: client ABI 输入校验拒绝

`mornlea_client` 入口收到非法输入(ABI 版本不匹配、空指针、缓冲长度不符、
无效窗口句柄)时 MUST 返回错误状态且 MUST 不修改调用方缓冲;Go 侧 MUST 把
错误状态转换为带稳定中文文案的报告,MUST NOT 以部分快照继续运行。

#### Scenario: 非法调用被拒绝且缓冲不变

- GIVEN 构造的非法 client 请求(如快照缓冲长度不符或已销毁的窗口句柄)
- WHEN 调用 client 库入口
- THEN 返回错误状态,调用方缓冲保持调用前内容,Go 报告稳定中文错误
