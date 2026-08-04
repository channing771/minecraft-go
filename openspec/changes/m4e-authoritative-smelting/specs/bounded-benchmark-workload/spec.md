## MODIFIED Requirements

### Requirement: 工作负载变化使用新场景版本
含确定性矿石分布的 benchmark 世界报告 MUST 标记为 scenario v9；既有 scenario v6/v7/v8 报告与基线 MUST 保持可读取，比较器不得把不同 scenario 当作同一工作负载静默相对比较。矿石改变了固定种子世界的材料分布，因此 v8 与 v9 之间 MUST 只通过显式授权迁移，且迁移只执行完整性与绝对门禁。

#### Scenario: v9 同场景比较
- **WHEN** baseline 与 current 都是完整的 scenario v9 报告
- **THEN** 比较器 MUST 使用既有绝对门禁和回归门禁完成比较

#### Scenario: v8 同场景比较
- **WHEN** baseline 与 current 都是完整的 scenario v8 报告
- **THEN** 比较器 MUST 使用既有绝对门禁和回归门禁完成比较

#### Scenario: v7 同场景比较
- **WHEN** baseline 与 current 都是完整的 scenario v7 报告
- **THEN** 比较器 MUST 使用既有绝对门禁和回归门禁完成比较

#### Scenario: v8 与 v9 不静默混比
- **WHEN** baseline 为 scenario v8、current 为 scenario v9 且没有显式迁移授权
- **THEN** 比较器 MUST 拒绝相对比较并说明场景版本不一致

#### Scenario: 显式授权 v8 到 v9 迁移
- **WHEN** 调用方显式授权 `8:9` 迁移且两份报告的硬件身份一致
- **THEN** 比较器 MUST 执行既有完整性与绝对门禁，并跳过不同 workload 之间无意义的相对回归判定

#### Scenario: v7 与 v8 不静默混比
- **WHEN** baseline 为 scenario v7、current 为 scenario v8
- **THEN** 比较器 MUST 拒绝相对比较并说明场景版本不一致

#### Scenario: 未授权的 v6 与 v7 比较
- **WHEN** baseline 为 scenario v6、current 为 scenario v7 且没有显式迁移授权
- **THEN** 比较器 MUST 拒绝比较并说明场景版本不一致

#### Scenario: 显式授权同硬件迁移
- **WHEN** 调用方显式授权 `6:7` 迁移且两份报告的硬件身份一致
- **THEN** 比较器 MUST 执行既有完整性与绝对门禁，并跳过不同 workload 之间无意义的相对回归判定

#### Scenario: 历史报告保持可校验
- **WHEN** 调用方单独读取一份完整 scenario v6 或 v7 报告
- **THEN** 比较器 MUST 按该历史场景原有的完整性规则校验，不得要求 2048 个 GPU 样本

#### Scenario: 跨硬件迁移
- **WHEN** 两份不同 scenario 报告的硬件身份不同
- **THEN** 比较器 MUST 拒绝比较，且不得以场景升级为由执行跨硬件归一化
