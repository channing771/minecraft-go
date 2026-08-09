## MODIFIED Requirements

### Requirement: 工作负载变化使用新场景版本
静态方块光增加固定 `48³` 光源扫描和存在光源时的有界传播，改变固定 benchmark 的网格 CPU/GPU workload 后，benchmark 报告 MUST 标记为 scenario v15。v15 MUST 保持 `2560×1440` 离屏目标、阶段时长、运动、样本、指标、绝对阈值和 `20%` 相对回归阈值不变；交互客户端和无窗口 benchmark 的 still/flying 帧 MUST 执行相同的天空光与方块光网格工作。p99、FPS、RSS、GPU、tick、队列高水位、绝对阈值和相对回归结果 MUST 只记录和报告，不得导致 producer、比较器或 CI 失败；只要报告结构、字段和样本完整且数据有效，producer MUST 写出 JSON。既有 scenario v6 至 v14 报告与基线 MUST 保持可读取，比较器不得把不同 scenario 静默作相对比较。scenario v14 与 v15 之间 MUST 只通过唯一显式 `14:15` 迁移；该迁移 MUST 校验报告完整性和硬件身份，并跳过跨 workload 的相对回归判定。历史 `13:14` MUST 只用于分别读取和同版本比较既有 v13、v14 报告，不再是可授权迁移。任何其他迁移参数、损坏或缺字段报告、样本不完整、硬件身份不兼容、transport 或 commit 身份不一致、真实 overflow 或数据丢失以及 I/O 错误 MUST 继续失败；队列高水位数值本身 MUST 只记录。

#### Scenario: v15 同场景比较只记录性能
- **WHEN** baseline 与 current 都是完整有效、身份兼容的 scenario v15 报告
- **THEN** 比较器 MUST 输出既有绝对指标和相对回归记录，且任何性能数值变差都 MUST 返回成功

#### Scenario: v14 同场景比较只记录性能
- **WHEN** baseline 与 current 都是完整有效、身份兼容的 scenario v14 报告
- **THEN** 比较器 MUST 输出该场景既有绝对指标和相对回归记录，且任何性能数值变差都 MUST 返回成功

#### Scenario: v13 同场景比较只记录性能
- **WHEN** baseline 与 current 都是完整有效、身份兼容的 scenario v13 报告
- **THEN** 比较器 MUST 输出该场景既有绝对指标和相对回归记录，且任何性能数值变差都 MUST 返回成功

#### Scenario: v12 同场景比较只记录性能
- **WHEN** baseline 与 current 都是完整有效、身份兼容的 scenario v12 报告
- **THEN** 比较器 MUST 输出该场景既有性能比较记录，且任何性能数值变差都 MUST 返回成功

#### Scenario: v11 同场景比较只记录性能
- **WHEN** baseline 与 current 都是完整有效、身份兼容的 scenario v11 报告
- **THEN** 比较器 MUST 输出该场景既有性能比较记录，且任何性能数值变差都 MUST 返回成功

#### Scenario: v10 同场景比较只记录性能
- **WHEN** baseline 与 current 都是完整有效、身份兼容的 scenario v10 报告
- **THEN** 比较器 MUST 输出该场景既有性能比较记录，且任何性能数值变差都 MUST 返回成功

#### Scenario: v9 同场景比较只记录性能
- **WHEN** baseline 与 current 都是完整有效、身份兼容的 scenario v9 报告
- **THEN** 比较器 MUST 输出该场景既有性能比较记录，且任何性能数值变差都 MUST 返回成功

#### Scenario: v8 同场景比较只记录性能
- **WHEN** baseline 与 current 都是完整有效、身份兼容的 scenario v8 报告
- **THEN** 比较器 MUST 输出该场景既有性能比较记录，且任何性能数值变差都 MUST 返回成功

#### Scenario: v7 同场景比较只记录性能
- **WHEN** baseline 与 current 都是完整有效、身份兼容的 scenario v7 报告
- **THEN** 比较器 MUST 输出该场景既有性能比较记录，且任何性能数值变差都 MUST 返回成功

#### Scenario: v6 同场景比较只记录性能
- **WHEN** baseline 与 current 都是完整有效、身份兼容的 scenario v6 报告
- **THEN** 比较器 MUST 输出该场景既有性能比较记录，且任何性能数值变差都 MUST 返回成功

#### Scenario: v14 与 v15 不静默混比
- **WHEN** baseline 为 scenario v14、current 为 scenario v15 且没有显式迁移授权
- **THEN** 比较器 MUST 拒绝相对比较并说明场景版本不一致

#### Scenario: 显式授权 v14 到 v15 的迁移
- **WHEN** 调用方显式授权 `14:15` 迁移且两份报告完整有效、硬件身份一致
- **THEN** 比较器 MUST 输出绝对性能记录、跳过不同 workload 间的相对回归判定，且性能数值不理想 MUST 返回成功

#### Scenario: 唯一迁移之外的参数被拒绝
- **WHEN** 调用方使用 `6:15`、`13:14`、`13:15`、`15:14`、`14:14`、`15:15` 或其他非 `14:15` 参数
- **THEN** 比较器 MUST 拒绝比较并说明迁移方向不兼容

#### Scenario: M2 v15 独立基线不借用跨场景迁移
- **GIVEN** 一份完整有效的 M2 scenario v15 Memory 报告
- **WHEN** 调用方把该报告同时作为 baseline 与 current 且不提供迁移授权
- **THEN** 比较器 MUST 按 v15 同场景规则验证完整性和硬件身份并返回成功，且 MUST NOT 要求或接受 `6:15`

#### Scenario: 历史报告保持可读取
- **WHEN** 调用方单独读取一份完整 scenario v6 至 v14 报告，或比较两份相同历史 scenario 的完整报告
- **THEN** 比较器 MUST 按该历史场景原有完整性规则校验，不得要求其满足 v15，也不得把 `13:14` 当作当前迁移授权

#### Scenario: 跨硬件迁移被拒绝
- **WHEN** 两份 scenario 不同且硬件身份不同的报告请求迁移比较
- **THEN** 比较器 MUST 拒绝比较，且不得以场景升级为由执行跨硬件归一化

#### Scenario: 跨 transport 不接受场景迁移
- **WHEN** 同一 commit 的 Memory scenario v14 与 TCP scenario v15 报告带 `14:15` 授权请求跨 transport 比较
- **THEN** 比较器 MUST 先拒绝 scenario 身份不一致；`14:15` 授权 MUST 只适用于同 transport 的 workload 迁移

#### Scenario: 完整报告不因性能数值中止写入
- **WHEN** producer 已取得完整有效的 v15 样本，但一个或多个性能指标超过绝对阈值或相对记录值
- **THEN** producer MUST 写出完整 JSON，且不得因这些性能数值返回失败

#### Scenario: 无效或不完整报告仍失败
- **WHEN** 报告损坏、缺少必需字段、样本不完整，或 transport 与 commit 身份和比较请求不一致
- **THEN** producer 或比较器 MUST 返回错误，且不得把该报告视为完整性能记录

#### Scenario: 队列高水位只记录而真实溢出失败
- **WHEN** 报告只显示队列高水位升高但没有 overflow 或数据丢失
- **THEN** 比较器 MUST 记录该数值并返回成功；若报告声明真实 overflow 或数据丢失，则 MUST 返回错误

#### Scenario: 报告写入错误仍失败
- **WHEN** producer 无法把完整报告写到请求的输出位置
- **THEN** producer MUST 返回包含 I/O 上下文的错误

#### Scenario: 实现优化保持 scenario v15
- **GIVEN** 优化前后的传播语义、固定分辨率、阶段时长、样本数和统计口径完全相同
- **WHEN** 项目只减少实现开销或修复资源滞留
- **THEN** producer MUST 继续标记 scenario v15，且比较器 MUST 继续输出既有 v15 性能记录

#### Scenario: workload 或测量口径变化不能藏在 v15
- **WHEN** 性能修复改变传播语义、固定分辨率、阶段时长、样本数、场景运动、指标定义或其他 benchmark workload
- **THEN** 项目 MUST 在再次生成报告前升级场景版本并修订迁移规则，不得把变化后的报告标记为 scenario v15
