## Purpose

为全仓 Go 文件提供可判定的职责审计与纯结构重组契约，确保文件或包边界变化不会改变任何既有外部行为、固定 artifact 或架构守卫语义。

## ADDED Requirements

### Requirement: 代码组织重构保持外部行为
系统 MUST 确保职责化文件和包迁移不改变任何既有外部行为或固定 artifact。

#### Scenario: 重组后既有契约不漂移
- **GIVEN** Task 20 固定主线基线的协议、存档、视觉与性能 fixture
- **WHEN** 完成职责化文件和包迁移
- **THEN** 除 Task 2 明确批准新增 `TestProductionGoSourceScansSplitFiles`、`TestTopLevelDeclarationNamesInScansSplitFiles`，并将 `TestSessionLifecycleResponsibilitiesLiveInSessionFile` 重命名为 `TestSessionLifecycleResponsibilitiesStayInSessionFiles` 外，其余相对 `37cdb3e` 的 Test、Benchmark、Fuzz 入口与固定 artifact MUST 保持不变
- **AND** benchmark 与 `perfcheck` 的性能数值及既有阈值 MUST 只保存记录且不得改变退出状态
- **AND** 只有报告结构、身份/provenance、真实 overflow、数据丢失、I/O 错误和非数值命令失败 MUST 阻断

#### Scenario: 同步主线后上游能力不回退
- **GIVEN** `origin/main` 的固定基线 `37cdb3e` 已提供协议 v15、区块 schema v8、玩家 schema v6、已归档 M4N 及其后续材料、伤害、目标反馈、加工、自然生成、配方和容器高度能力
- **WHEN** M4O 同步该基线并解决职责文件冲突
- **THEN** 这些上游能力及其可观察行为 MUST 全部保留且不得归因于 M4O
- **AND** 10 个视觉场景、storage/network fixture 与其他固定 artifact MUST 与 `37cdb3e` 字节一致
- **AND** 相对 `37cdb3e` 的 Test、Benchmark、Fuzz 入口除既有 Task 2 两项新增和一项重命名外 MUST 完全一致

#### Scenario: 固定主线的同环境视觉基线失败不掩盖迁移漂移
- **GIVEN** 同一 Apple M2/macOS 环境在原始 `37cdb3e` 连续复现 `materials-showcase` 的最大通道差 1、26 个差异像素（0.0113%），以及 `oak-grove` 的最大通道差 47、10 个差异像素（0.0043%），且其余 8 个场景各自通过 tracked golden
- **WHEN** Task 20 分支与 detached `37cdb3e` 在该同一环境以同一 `make visual-check` 命令重新 capture 全部 10 个场景
- **THEN** 两边 10 个场景输出 PNG MUST 逐字节一致
- **AND** 两边 MUST 仅有上述 2 个场景失败，且场景名、最大通道差、差异像素数与比例的失败摘要完全一致
- **AND** 其余 8 个场景 MUST 各自通过 tracked golden
- **AND** 此裁决 MUST NOT 泛化为跳过其他视觉失败，也 MUST NOT 修改 golden、阈值或 capture 代码

### Requirement: 架构守卫不依赖单一源文件位置
架构守卫 MUST 对完整职责文件集合执行原有检查，不得绑定单一固定源文件位置。

#### Scenario: 同一职责分布到多个文件
- **GIVEN** 一个包内职责被拆到多个生产 Go 文件
- **WHEN** 运行架构守卫
- **THEN** 守卫 MUST 扫描完整职责文件集合并继续拒绝旧路径

### Requirement: 全仓 Go 文件完成职责审计
基线中的全部生产和测试 Go 文件 MUST 获得且仅获得一种职责审计结论。

#### Scenario: 文件无需修改但职责单一
- **GIVEN** 基线中的任意生产或测试 Go 文件
- **WHEN** 完成其所属包的审计任务
- **THEN** 该文件 MUST 被判定为保留、同包拆分、提取新包或删除之一

#### Scenario: 主线同步后的完整审计
- **GIVEN** `37cdb3e` 中 `cmd/` 与 `internal/` 下的 412 个 Go 文件
- **WHEN** 完成 Task 20 的主线同步审计
- **THEN** 36 个同包拆分、2 个提取到唯一新包 `internal/render/hud`、0 个删除、374 个保留的结论 MUST 互斥且合计为 412
- **AND** 不得新增其他包、恢复拆分前旧大文件或以更新 golden/baseline 掩盖差异
