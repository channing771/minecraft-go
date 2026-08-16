# rust-client-render-cutover Delta Spec

## Purpose

把客户端生产渲染切换到 Rust 渲染器(窗口 surface 与无头离屏两条路径)并删除 Go 渲染栈的 GPU 半部,以 golden 字节不变锁定切换的行为零变化;Rust 成为呈现的唯一生产实现。

## ADDED Requirements

### Requirement: 生产渲染由 Rust 渲染器独占

darwin 客户端的窗口呈现与无头 capture MUST 由 `mornlea_client` 渲染器独占
生产;Go 生产路径 MUST 不包含 GPU pipeline/pass 实现与 WebGPU 绑定依赖,
`go.mod` MUST 不含 `oliverbestmann/webgpu`。CPU 准备(mesh 调度、可见性、
字形光栅化、布局、实体编码)保留在 Go。

#### Scenario: 客户端一帧经单次渲染 FFI 呈现

- GIVEN 运行中的 darwin 客户端
- WHEN 主循环渲染一帧
- THEN 全部 GPU pass 由一次 render FFI 调用执行并呈现到窗口 surface,
  帧内无逐 pass 或逐资源渲染调用

#### Scenario: 生产代码不再引用 WebGPU 绑定

- GIVEN 切换完成后的仓库
- WHEN 检查 Go module 依赖与生产源码
- THEN 不存在 `internal/gfx` 包与对 `oliverbestmann/webgpu` 的引用

### Requirement: golden 与既有视觉行为字节不变

切换后的无头 capture 输出 MUST 原样通过既有 golden 比对与
`diffThreshold` 门禁;golden 基线文件、阈值与场景 MUST NOT 修改。

#### Scenario: 既有 golden 零改动通过

- GIVEN 既有全部 capture 场景与 golden 基线
- WHEN 以 Rust 渲染器执行无头抓帧并比对
- THEN 全部场景在既有阈值内通过,golden 文件未修改

### Requirement: 窗口 surface 呈现语义

窗口模式渲染 MUST 在帧内获取 surface 纹理并于提交后呈现;窗口被遮挡、
最小化或 surface 过期时 MUST 跳过该帧且不报错;窗口尺寸变化后 MUST 重建
渲染目标与 HiZ 并以新尺寸继续渲染。

#### Scenario: 遮挡帧跳过不中断

- GIVEN 窗口模式渲染器
- WHEN surface 纹理获取失败(遮挡/过期)
- THEN 该帧被跳过,后续帧恢复正常呈现,进程不退出

#### Scenario: resize 后继续正确渲染

- GIVEN 窗口模式渲染器
- WHEN 调用 resize 并渲染后续帧
- THEN 渲染目标与 HiZ 以新尺寸重建,呈现无花屏或尺寸错位

### Requirement: 字形与 HUD 图集经 sink 独占供给

Go 字形光栅化与 HUD 图集构建 MUST 只经 client 渲染器上传入口供给 GPU;
迁移后 MUST 不存在第二份图集纹理路径。字形队列、tofu 兜底与上限语义
MUST 与迁移前一致。

#### Scenario: 字形上传语义保持

- GIVEN 聊天与名牌产生新字形请求
- WHEN 光栅化 worker 完成并按帧预算冲刷
- THEN 字形经 client 上传入口进入唯一图集,文本呈现与迁移前一致
