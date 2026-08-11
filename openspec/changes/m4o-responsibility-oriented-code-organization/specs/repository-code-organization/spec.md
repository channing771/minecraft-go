## Purpose

为全仓 Go 文件提供可判定的职责审计与纯结构重组契约，确保文件或包边界变化不会改变任何既有外部行为、固定 artifact 或架构守卫语义。

## ADDED Requirements

### Requirement: 代码组织重构保持外部行为
系统 MUST 确保职责化文件和包迁移不改变任何既有外部行为或固定 artifact。

#### Scenario: 重组后既有契约不漂移
- **GIVEN** 当前协议、存档、视觉与性能 fixture
- **WHEN** 完成职责化文件和包迁移
- **THEN** 除 Task 2 明确批准新增 `TestProductionGoSourceScansSplitFiles`、`TestTopLevelDeclarationNamesInScansSplitFiles`，并将 `TestSessionLifecycleResponsibilitiesLiveInSessionFile` 重命名为 `TestSessionLifecycleResponsibilitiesStayInSessionFiles` 外，其余既有 Test、Benchmark、Fuzz 入口与固定 artifact MUST 保持不变
- **AND** benchmark 与 `perfcheck` 的性能数值及既有阈值 MUST 只保存记录且不得改变退出状态
- **AND** 只有报告结构、身份/provenance、真实 overflow、数据丢失、I/O 错误和非数值命令失败 MUST 阻断

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
