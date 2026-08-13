## MODIFIED Requirements

### Requirement: 代码组织重构保持外部行为
系统 MUST 确保 Mornlea 身份切换不改变任何未获批准的外部行为、测试入口或固定 artifact。测试入口基线 MUST 是 Task 7 初始 HEAD 持久化的清单，该 HEAD 已包含 Tasks 4–6 新增的数据迁移和命令路由测试。

#### Scenario: 身份切换只改变获批测试入口
- **GIVEN** Task 7 初始 HEAD 已持久化全部 Test、Benchmark 与 Fuzz 入口
- **WHEN** 完成原子身份切换
- **THEN** MUST 仅有以下 6 项重命名：`TestMCGodHasNoGraphicsDependencies` → `TestMornleaServerHasNoGraphicsDependencies`、`TestMcgoUsesLoginStreamsInsteadOfAttachedServerEndpoints` → `TestMornleaUsesLoginStreamsInsteadOfAttachedServerEndpoints`、`TestMcgoBenchmarkTCPPathUsesTheSharedLoginStateMachine` → `TestMornleaBenchmarkTCPPathUsesTheSharedLoginStateMachine`、`TestMCGodProcess` → `TestMornleaServerProcess`、`TestMCGodProcessReleasesWorldLockAfterSIGTERM` → `TestMornleaServerProcessReleasesWorldLockAfterSIGTERM`、`TestMCGodProcessSaveFailureExitsNonzero` → `TestMornleaServerProcessSaveFailureExitsNonzero`
- **AND** MUST 仅新增 `TestMornleaCurrentIdentity`
- **AND** 其余 Test、Benchmark、Fuzz 入口与 Task 1 后冻结的 fixture、golden 和性能 baseline MUST 保持不变
- **AND** benchmark 与 `perfcheck` 的性能数值及既有阈值 MUST 只保存记录且不得改变退出状态
- **AND** 只有报告结构、身份/provenance、真实 overflow、数据丢失、I/O 错误和非数值命令失败 MUST 阻断

#### Scenario: Apple M2 固定主线的同环境视觉失败不掩盖身份漂移
- **GIVEN** `system_profiler SPHardwareDataType` 的精确 Chip 为 `Apple M2`，且原始 Task 1 `origin/main` 仅有 `materials-showcase` 最大通道差 1、26 个差异像素（0.0113%）与 `oak-grove` 最大通道差 47、10 个差异像素（0.0043%）两个精确已知失败
- **WHEN** 原始主线与 Mornlea 分支在各自隔离 HOME 下运行同一非更新 capture
- **THEN** 两边 10 个场景 PNG 与上述两个失败的 actual/diff MUST 逐字节一致
- **AND** 两边 MUST 仅有上述两个失败且摘要完全一致，其余 8 个场景 MUST 通过 tracked golden
- **AND** 此裁决 MUST NOT 修改或放宽 golden、阈值、capture 代码或其他视觉失败

#### Scenario: 非 Apple M2 固定主线同环境视觉结果不漂移
- **GIVEN** `system_profiler SPHardwareDataType` 的精确 Chip 不是 `Apple M2`
- **WHEN** 原始 Task 1 `origin/main` 与 Mornlea 分支在各自隔离 HOME 下运行同一非更新 capture
- **THEN** 两边 10 个场景 PNG MUST 逐字节一致，且两次 `visual-check` MUST 退出 0
- **AND** 两边都 MUST 不产生 `*-actual.png` 或 `*-diff.png`
- **AND** 此裁决 MUST NOT 修改或放宽 golden、阈值或 capture 代码
