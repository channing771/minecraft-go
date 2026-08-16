# rust-client-render-entities Delta Spec

## Purpose

把客户端一帧的实体、文本与 HUD 呈现补齐到 Rust 离屏渲染器,以完整帧的双后端图像一致性锁定平行实现与 Go 渲染器的等价性;生产渲染路径本期保持不变。

## ADDED Requirements

### Requirement: 完整帧双后端图像一致

对同一份完整帧输入(地形 sections、avatar/掉落物 instance 流、目标方块
轮廓、名牌/HUD/调试面板顶点流、伤害红边强度、字形与 HUD 图集字节),Rust
离屏渲染器输出 MUST 与 Go 渲染器整帧输出在既有 `diffThreshold` 双阈值内
一致;对照 MUST 覆盖含实体与文本的既有 capture 场景,阈值 MUST NOT 放宽。

#### Scenario: 含实体与名牌的场景双后端对照通过

- GIVEN 含远端玩家/伙伴 avatar 与 Unicode 名牌的 capture 场景帧数据
- WHEN 同帧数据分别驱动 Go 渲染器与 Rust 渲染器并回读图像
- THEN 两图差异落在既有 diffThreshold 双阈值内

#### Scenario: 含 HUD 与调试面板的场景双后端对照通过

- GIVEN 含快捷栏 HUD 与调试面板文本的帧数据
- WHEN 双后端分别渲染整帧
- THEN 两图差异落在既有 diffThreshold 双阈值内

### Requirement: frame v2 单次 FFI 与 pass 段完整性

每帧的全部可变呈现数据(可见 sections、instance 流、顶点流、uniform 标量)
MUST 经一次 frame v2 调用过境;帧内 MUST NOT 产生逐 pass 渲染 FFI。pass 段
MUST 按 tag+length 编码,未知 tag 或长度越界 MUST 被拒绝且不产生部分渲染。

#### Scenario: 帧循环调用计数

- GIVEN 已装配完整场景的 Rust 渲染器
- WHEN 连续渲染多帧且资源无变化
- THEN 每帧恰好一次渲染 FFI,无图集或 section 上传调用

#### Scenario: 非法 pass 段被拒绝

- GIVEN 构造的含未知 tag 或长度越界 pass 段的 frame v2 输入
- WHEN 调用渲染入口
- THEN 返回输入错误状态,离屏 target 保持上一帧内容

### Requirement: 字形与 HUD 图集字节同源

Rust 侧字形图集内容 MUST 来自 Go 字形光栅化 worker 的增量矩形上传,与 Go
渲染器写入自身图集的字节一致;HUD 图集 MUST 来自 Go 构建的同一份像素。
Rust MUST NOT 内置字体光栅化或 HUD 贴图生成。

#### Scenario: 相同文本的名牌字形一致

- GIVEN 同一段 Unicode 名牌文本经 Go 光栅化并同步上传两个后端
- WHEN 双后端渲染名牌
- THEN 字形区域像素差异落在既有阈值内

### Requirement: 生产渲染路径保持不变

本变更交付后,客户端与无窗口 capture 的生产渲染 MUST 仍由 Go 渲染器执行;
golden 基线字节 MUST 不变,`internal/gfx` 与 Go WebGPU 绑定 MUST 保持在位。

#### Scenario: 既有视觉门禁零改动通过

- GIVEN 既有 capture golden 测试与视觉比对门禁
- WHEN 在本变更合并后的主线运行
- THEN 全部断言与 golden 文件不修改而通过
