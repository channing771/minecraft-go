## Purpose

在不改变 Go 的世界、玩家状态和公开 API 所有权前提下，为 Rust collision 与 raycast 建立可验证、可回退且跨平台一致的唯一生产 kernel 契约。

## ADDED Requirements

### Requirement: Rust collision 保持共享物理结果
系统 MUST 让 Rust 唯一生产 kernel 对同一 state、displacement 与 collision snapshot 产生与冻结 Go oracle 逐位一致的位置、clipped mask、OnGround、UsedStep 与 HitUnknown；解析顺序 MUST 为 Y/X/Z，unknown MUST 作为闭合边界，step 只在水平进度严格更大时选中。

#### Scenario: 被拒绝的 step 不污染最终 unknown
- **GIVEN** ordinary path 全部已知且备选 step path 遇到 unknown 后被拒绝
- **WHEN** 解析同一 movement
- **THEN** MUST 返回 ordinary path
- **AND** 最终 HitUnknown MUST 为 false

#### Scenario: 原始 box count 保持 clamp 语义
- **GIVEN** CollisionBoxSet.Count 为 8、9 或 255
- **WHEN** Go 编码 snapshot
- **THEN** MUST 只编码前八个 AABB
- **AND** 9 或 255 MUST NOT 因 count 本身被拒绝

### Requirement: collision snapshot 具有硬资源上限
系统 MUST 在 source 查询、分配、native 调用和状态发布前以 checked arithmetic 计算完整 prism，并 MUST 原子拒绝超过 4096 cell、整数溢出或不可表示的输入。

#### Scenario: 上限内完整编码
- **WHEN** prism 恰含 4096 cell
- **THEN** 系统 MUST 完整编码且不得截断

#### Scenario: 超过上限在查询前 panic
- **WHEN** prism 需要 4097 cell
- **THEN** 系统 MUST 在零次 CollisionSource 查询后稳定 panic

### Requirement: Rust raycast 保持惰性 callback 契约
系统 MUST 让 Rust 以最多 64 record 的 caller-owned cursor batch 执行 DDA，同时由 Go 按现有顺序调用 callback、传播第一个 error 并计算最终 Point。

#### Scenario: batch 预取不改变惰性
- **GIVEN** Rust 已生成包含多个候选格的一个 batch
- **WHEN** 第一条 callback 返回 sentinel error 或首个 solid
- **THEN** Go MUST 立即返回且 MUST NOT 调用后续 record
- **AND** error identity MUST 原样保留

#### Scenario: 多 batch 保持遍历语义
- **GIVEN** 合法射线跨越超过 64 个候选格
- **WHEN** 使用 caller-owned cursor 继续下一批
- **THEN** origin cell、负坐标 floor、XYZ tie、精确 endpoint 与 int32 wrapping MUST 与冻结 Go oracle 一致

### Requirement: additive native ABI 原子且无跨调用所有权
系统 MUST 保持 ABI version 1、旧 mesh symbol/layout/status 不变，并由 Go 独占所有 input、scratch、cursor 与 output；Rust MUST 不保存地址、不回调 Go、不启动后台线程，且任一非法输入或 panic MUST 不发布部分 collision/raycast 结果。

#### Scenario: 两个平台逐位一致
- **WHEN** macOS arm64 与 Linux amd64 对固定、随机和 fuzz corpus 调用 collision/raycast
- **THEN** 两个平台 MUST 与 test-only Go oracle 逐位一致
- **AND** 常规 collision/raycast bridge MUST 保持零 Go heap allocation

#### Scenario: 非法 ABI 输入不发布部分结果
- **GIVEN** collision/raycast 调用的 version、指针范围、长度、overlap、magic、layout、reserved byte、cursor state 或内容非法
- **WHEN** native bridge 拒绝该调用
- **THEN** 在两个 result metadata 指针本身有效时，raycast 的 `output_count`/`done` MUST 清零
- **AND** collision output 与 raycast output/cursor MUST 保持调用前字节
- **AND** Go State MUST 不发布部分结果

#### Scenario: Rust panic 不跨越 C ABI
- **GIVEN** test-only fault injection 使 collision/raycast kernel panic
- **WHEN** 调用对应 C ABI
- **THEN** unwind MUST 不跨越 C ABI
- **AND** 在两个 result metadata 指针本身有效时，raycast 的 `output_count`/`done` MUST 清零
- **AND** collision output 与 raycast output/cursor、Go State MUST 保持未发布
