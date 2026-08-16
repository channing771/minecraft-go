# rust-client-render-terrain Delta Spec

## Purpose

在 `mornlea_client` 内以纯离屏 wgpu 渲染器平行实现世界地形呈现(terrain/sky/culling/HiZ),用双后端图像对照锁定与 Go 渲染器的一致性,为后续切换建立可验证基线;生产渲染路径本期保持不变。

## ADDED Requirements

### Requirement: Rust 离屏渲染器与 Go 渲染器图像一致

对同一组 section quads、相机、日照时间与分辨率,Rust 离屏渲染器输出的世界
地形图像(terrain/sky/云,含 GPU culling 与 HiZ 遮挡)MUST 与 Go 渲染器
输出在既有 `diffThreshold` 双阈值内一致;对照 MUST 覆盖既有无窗口 capture
场景,阈值 MUST NOT 因本变更放宽。

#### Scenario: capture 场景双后端对照通过

- GIVEN 既有无窗口 capture 场景之一(如 materials-showcase)
- WHEN 同帧数据分别驱动 Go Renderer 与 Rust 渲染器并回读 640×360 图像
- THEN 两图差异落在既有 diffThreshold 双阈值内

#### Scenario: 遮挡剔除不产生可见差异

- GIVEN 含大量互相遮挡 section 的场景
- WHEN 双后端分别渲染
- THEN Rust 侧 culling/HiZ 的剔除结果不引入超阈值的图像差异

### Requirement: mesh 数据单次过境与每帧单次渲染调用

section quad 数据 MUST 只在 section 变脏时经一次 upload 调用过境;每帧的
渲染 MUST 是一次 `render_frame` 调用(携带相机、日照与可见 section 列表),
帧内 MUST NOT 产生逐 pass 或逐资源的额外渲染 FFI 调用。

#### Scenario: 帧循环调用计数

- GIVEN 已上传若干 section 的 Rust 渲染器
- WHEN 连续渲染多帧且无 section 变化
- THEN 每帧恰好一次渲染 FFI 调用,无 upload 调用

### Requirement: 生产渲染路径保持不变

本变更交付后,客户端与无窗口 capture 的生产渲染 MUST 仍由 Go 渲染器执行;
golden 基线字节 MUST 不变,`internal/gfx` 与 Go WebGPU 绑定 MUST 保持在位。

#### Scenario: 既有视觉门禁零改动通过

- GIVEN 既有 capture golden 测试与视觉比对门禁
- WHEN 在本变更合并后的主线运行
- THEN 全部断言与 golden 文件不修改而通过

### Requirement: render ABI 输入校验拒绝

render 入口收到非法输入(ABI 版本不匹配、空指针、缓冲长度不符、无效
渲染器句柄、quad 字节数非 8 的倍数、可见列表越界)时 MUST 返回错误状态且
MUST 不修改调用方缓冲;Go 侧 MUST 转换为稳定中文错误报告。

#### Scenario: 非法 render 调用被拒绝且缓冲不变

- GIVEN 构造的非法 render 请求(如回读缓冲长度不符或已销毁句柄)
- WHEN 调用 render 入口
- THEN 返回错误状态,调用方缓冲保持调用前内容
