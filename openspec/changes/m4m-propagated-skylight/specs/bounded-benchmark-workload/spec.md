## MODIFIED Requirements

### Requirement: 工作负载变化使用新场景版本

横向天空光传播改变固定 benchmark 的网格 CPU/GPU workload 后，benchmark 报告 MUST 标记为 scenario v14。v14 MUST 保持 `2560×1440` 离屏目标、阶段时长、运动、样本、指标、绝对门禁和 `20%` 相对回归阈值不变；交互客户端和无窗口 benchmark 的 still/flying 帧 MUST 执行相同传播后的网格工作。既有 scenario v6 至 v13 报告与基线 MUST 保持可读取，比较器不得把不同 scenario 静默作相对比较。scenario v13 与 v14 之间 MUST 只通过唯一显式 `13:14` 授权迁移；该迁移 MUST 执行完整性、硬件一致性和绝对门禁，并跳过跨 workload 的相对回归比较。任何其他迁移参数 MUST 被拒绝。

#### Scenario: v14 同场景比较
- **WHEN** baseline 与 current 都是完整的 scenario v14 报告
- **THEN** 比较器 MUST 使用既有绝对门禁和回归门禁完成比较

#### Scenario: v13 与 v14 不静默混比
- **WHEN** baseline 为 scenario v13、current 为 scenario v14 且没有显式迁移授权
- **THEN** 比较器 MUST 拒绝相对比较并说明场景版本不一致

#### Scenario: 显式授权 v13 到 v14 的迁移
- **WHEN** 调用方显式授权 `13:14` 迁移且两份报告的硬件身份一致
- **THEN** 比较器 MUST 执行完整性、硬件一致性和既有绝对门禁，并跳过不同 workload 间无意义的相对回归判定

#### Scenario: 唯一迁移之外的参数被拒绝
- **WHEN** 调用方使用 `12:13`、`11:13`、`12:14`、`14:13` 或其他非 `13:14` 参数
- **THEN** 比较器 MUST 拒绝比较并说明场景版本不一致

#### Scenario: 历史报告保持可读取
- **WHEN** 调用方单独读取一份完整 scenario v6、v7、v8、v9、v10、v11、v12 或 v13 报告
- **THEN** 比较器 MUST 按该历史场景原有完整性规则校验，不得要求其满足 v14 的场景版本

#### Scenario: 跨硬件迁移被拒绝
- **WHEN** 两份 scenario 不同且硬件身份不同的报告请求迁移比较
- **THEN** 比较器 MUST 拒绝比较，且不得以场景升级为由执行跨硬件归一化

#### Scenario: 实现优化保持 scenario v14
- **GIVEN** 优化前后的传播语义、固定分辨率、阶段时长、样本数和统计口径完全相同
- **WHEN** 项目只减少实现开销或修复资源滞留
- **THEN** producer MUST 继续标记 scenario v14，且比较器 MUST 继续执行既有 v14 完整性和绝对门禁

#### Scenario: workload 或测量口径变化不能藏在 v14
- **WHEN** 性能修复改变传播语义、固定分辨率、阶段时长、样本数、场景运动、指标定义或其他 benchmark workload
- **THEN** 项目 MUST 在再次生成正式报告前升级场景版本并修订迁移规则，不得把变化后的报告标记为 scenario v14
