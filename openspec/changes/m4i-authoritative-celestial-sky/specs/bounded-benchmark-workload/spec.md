## MODIFIED Requirements

### Requirement: 工作负载变化使用新场景版本
在 M4J scenario v12 工作负载上加入每帧程序化天空 fullscreen draw 后，benchmark 报告 MUST 标记为 scenario v13；交互客户端与无窗口 benchmark 的 still/flying 帧 MUST 执行同一天空绘制。既有 scenario v6/v7/v8/v9/v10/v11/v12 报告与基线 MUST 保持可读取，比较器不得把不同 scenario 当作同一工作负载静默相对比较。scenario v12 与 v13 之间 MUST 只通过唯一一条显式 `12:13` 授权迁移，该迁移只执行完整性与绝对门禁；所有更早的迁移参数 MUST 已退役并被拒绝。

#### Scenario: v13 同场景比较
- **WHEN** baseline 与 current 都是完整的 scenario v13 报告
- **THEN** 比较器 MUST 使用既有绝对门禁和回归门禁完成比较

#### Scenario: v12 同场景比较
- **WHEN** baseline 与 current 都是完整的 scenario v12 报告
- **THEN** 比较器 MUST 使用既有绝对门禁和回归门禁完成比较

#### Scenario: v11 同场景比较
- **WHEN** baseline 与 current 都是完整的 scenario v11 报告
- **THEN** 比较器 MUST 使用既有绝对门禁和回归门禁完成比较

#### Scenario: v10 同场景比较
- **WHEN** baseline 与 current 都是完整的 scenario v10 报告
- **THEN** 比较器 MUST 使用既有绝对门禁和回归门禁完成比较

#### Scenario: v9 同场景比较
- **WHEN** baseline 与 current 都是完整的 scenario v9 报告
- **THEN** 比较器 MUST 使用既有绝对门禁和回归门禁完成比较

#### Scenario: v8 同场景比较
- **WHEN** baseline 与 current 都是完整的 scenario v8 报告
- **THEN** 比较器 MUST 使用既有绝对门禁和回归门禁完成比较

#### Scenario: v7 同场景比较
- **WHEN** baseline 与 current 都是完整的 scenario v7 报告
- **THEN** 比较器 MUST 使用既有绝对门禁和回归门禁完成比较

#### Scenario: v12 与 v13 不静默混比
- **WHEN** baseline 为 scenario v12、current 为 scenario v13 且没有显式迁移授权
- **THEN** 比较器 MUST 拒绝相对比较并说明场景版本不一致

#### Scenario: 显式授权 v12 到 v13 的迁移
- **WHEN** 调用方显式授权 `12:13` 迁移且两份报告的硬件身份一致
- **THEN** 比较器 MUST 执行既有完整性与绝对门禁，并跳过不同 workload 之间无意义的相对回归判定

#### Scenario: 跳过 v12 的迁移被拒绝
- **WHEN** 调用方使用 `11:13`、`10:13` 或其他非 `12:13` 参数
- **THEN** 比较器 MUST 拒绝比较并说明场景版本不一致

#### Scenario: 旧迁移参数退役
- **WHEN** 调用方使用 `10:12`、`11:12`、`10:11`、`9:10`、`8:9` 或 `6:7` 参数
- **THEN** 比较器 MUST 拒绝比较并说明场景版本不一致

#### Scenario: 历史报告保持可校验
- **WHEN** 调用方单独读取一份完整 scenario v6、v7、v8、v9、v10、v11 或 v12 报告
- **THEN** 比较器 MUST 按该历史场景原有的完整性规则校验，不得要求历史报告满足 v13 的场景版本

#### Scenario: 跨硬件迁移
- **WHEN** 两份 scenario 不同且硬件身份不同的报告请求迁移比较
- **THEN** 比较器 MUST 拒绝比较，且不得以场景升级为由执行跨硬件归一化

#### Scenario: 无窗口 workload 包含天空绘制
- **WHEN** benchmark 执行 scenario v13 的 still 或 flying 帧
- **THEN** 系统 MUST 在固定 2560x1440 离屏目标绘制与交互客户端相同的程序化天空，且不得创建、启动或聚焦游戏窗口

#### Scenario: 实现优化保持 scenario v13
- **GIVEN** 优化前后的天空视觉、每帧 fullscreen draw 数量、2560x1440 离屏目标、阶段时长、样本数和统计口径完全相同
- **WHEN** 项目只减少 shader 工作或修复运行时资源滞留
- **THEN** producer MUST 继续标记 scenario v13，且比较器 MUST 继续执行既有 v13 完整性和绝对门禁

#### Scenario: workload 或测量口径变化不能藏在 v13
- **WHEN** 性能修复改变天空 draw 数量、固定分辨率、阶段时长、样本数、场景运动、指标定义或其他 benchmark workload
- **THEN** 项目 MUST 在再次生成正式报告前升级场景版本并修订迁移规则，不得把变化后的报告标记为 scenario v13
