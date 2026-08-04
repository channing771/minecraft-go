## Purpose

保证性能报告记录的是有界且接近交互客户端的逐帧工作负载，并在工作负载语义变化时通过场景版本阻止静默混比。

## ADDED Requirements

### Requirement: 计时帧分别限制消息与网格工作
性能 benchmark 的预热和正式计时帧 MUST 分别应用消息排空上限与网格工作上限；网格工作上限 MUST 与交互客户端一致，消息积压不得隐式扩大单帧网格调度或回收量。

#### Scenario: 消息排空上限大于网格上限
- **WHEN** benchmark 的一帧允许排空至多 `4096` 条服务端消息
- **THEN** 该帧的网格调度和完成结果回收仍分别不得超过 `64`

#### Scenario: 载入阶段快速收敛
- **WHEN** benchmark 尚未进入预热或正式计时阶段
- **THEN** 系统 MAY 使用更高的网格工作上限完成初始载入，且这些帧不得进入延迟样本

### Requirement: 工作负载变化使用新场景版本
采用有界计时帧的报告 MUST 标记为 scenario v7；既有 scenario v6 报告与基线 MUST 保持原样，比较器不得把 v6 与 v7 当作同一工作负载静默比较。

#### Scenario: v7 同场景比较
- **WHEN** baseline 与 current 都是完整的 scenario v7 报告
- **THEN** 比较器 MUST 使用既有绝对门禁和回归门禁完成比较

#### Scenario: 未授权的 v6 与 v7 比较
- **WHEN** baseline 为 scenario v6、current 为 scenario v7 且没有显式迁移授权
- **THEN** 比较器 MUST 拒绝比较并说明场景版本不一致

#### Scenario: 显式授权同硬件迁移
- **WHEN** 调用方显式授权 `6:7` 迁移且两份报告的硬件身份一致
- **THEN** 比较器 MUST 允许继续执行既有完整性、绝对门禁和回归门禁

#### Scenario: 跨硬件迁移
- **WHEN** v6 与 v7 报告的硬件身份不同
- **THEN** 比较器 MUST 拒绝比较，即使调用方显式授权 `6:7` 迁移

### Requirement: 性能阈值保持不变
scenario v7 MUST 继续使用现有 still、flying、RSS 和 Memory/TCP 比较阈值，不得以场景升级为由放宽门禁。

#### Scenario: 飞行尾延迟超限
- **WHEN** scenario v7 的 flying p99 大于或等于 `12ms`
- **THEN** 性能门禁 MUST 失败
