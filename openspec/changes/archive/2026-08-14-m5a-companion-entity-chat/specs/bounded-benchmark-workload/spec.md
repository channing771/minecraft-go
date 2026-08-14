## MODIFIED Requirements

### Requirement: 工作负载变化使用新场景版本

Avatar、NameTag 与 Hotbar HUD 为容纳最多七名远端玩家、四个伙伴、一个目标标签和固定聊天 overlay 而改变固定 GPU 上传布局、offset 与每帧写入字节数后，benchmark 报告 MUST 标记为 scenario v16。固定 benchmark 输入 MUST 继续是七名远端玩家、零伙伴且不注入聊天；v16 MUST 保持 `2560×1440` 离屏目标、阶段时长、运动、样本、指标、绝对阈值、`20%` 相对回归阈值以及 v15 的天空光/方块光工作不变。p99、FPS、RSS、GPU、tick、队列高水位、绝对阈值和相对回归结果 MUST 只记录和报告，不得导致 producer、比较器或 CI 失败；只要报告结构、字段和样本完整且数据有效，producer MUST 写出 JSON。既有 scenario v6 至 v15 报告与基线 MUST 保持可读取，比较器不得把不同 scenario 静默作相对比较。当前不同场景之间 MUST 只接受唯一显式 `15:16` 迁移；该迁移 MUST 校验报告完整性与硬件身份并跳过跨 workload 的相对回归判定。历史 `14:15` 与更早迁移只作为既有报告和归档证据，不再是当前可授权迁移。任何其他迁移参数、损坏或缺字段报告、样本不完整、硬件身份不兼容、transport 或 commit 身份不一致、真实 overflow 或数据丢失以及 I/O 错误 MUST 继续失败；队列高水位数值本身 MUST 只记录。

#### Scenario: v16 固定上传布局完整

- **GIVEN** scenario v16 使用固定七名远端玩家、零伙伴和空聊天输入
- **WHEN** producer 准备 Avatar、NameTag 与 Hotbar HUD 固定上传
- **THEN** Avatar MUST 容纳 66 个 body parts、instance 区 5280 bytes、indirect offset 5536、总上传 5556 bytes
- **AND** NameTag MUST 容纳 12 个标签、background 区 768 bytes、glyph offset 1024、glyph 区 24576 bytes、总上传 25600 bytes
- **AND** Hotbar HUD MUST 容纳 236 个 quad 与 700 个 glyph、glyph offset 11776、总容量 45376 bytes，空聊天帧实际写入 MUST 为 11776 bytes

#### Scenario: v16 同场景比较只记录性能

- **GIVEN** baseline 与 current 都是完整有效且身份兼容的 scenario v16 报告
- **WHEN** 比较器执行同场景比较
- **THEN** 比较器 MUST 输出既有绝对指标和相对回归记录，且任何性能数值变差都 MUST 返回成功

#### Scenario: v15 同场景比较只记录性能

- **GIVEN** baseline 与 current 都是完整有效且身份兼容的 scenario v15 报告
- **WHEN** 比较器执行同场景比较
- **THEN** 比较器 MUST 输出该场景既有绝对指标和相对回归记录，且任何性能数值变差都 MUST 返回成功

#### Scenario: v14 同场景比较只记录性能

- **GIVEN** baseline 与 current 都是完整有效且身份兼容的 scenario v14 报告
- **WHEN** 比较器执行同场景比较
- **THEN** 比较器 MUST 输出该场景既有绝对指标和相对回归记录，且任何性能数值变差都 MUST 返回成功

#### Scenario: v13 同场景比较只记录性能

- **GIVEN** baseline 与 current 都是完整有效且身份兼容的 scenario v13 报告
- **WHEN** 比较器执行同场景比较
- **THEN** 比较器 MUST 输出该场景既有绝对指标和相对回归记录，且任何性能数值变差都 MUST 返回成功

#### Scenario: v12 同场景比较只记录性能

- **GIVEN** baseline 与 current 都是完整有效且身份兼容的 scenario v12 报告
- **WHEN** 比较器执行同场景比较
- **THEN** 比较器 MUST 输出该场景既有性能比较记录，且任何性能数值变差都 MUST 返回成功

#### Scenario: v11 同场景比较只记录性能

- **GIVEN** baseline 与 current 都是完整有效且身份兼容的 scenario v11 报告
- **WHEN** 比较器执行同场景比较
- **THEN** 比较器 MUST 输出该场景既有性能比较记录，且任何性能数值变差都 MUST 返回成功

#### Scenario: v10 同场景比较只记录性能

- **GIVEN** baseline 与 current 都是完整有效且身份兼容的 scenario v10 报告
- **WHEN** 比较器执行同场景比较
- **THEN** 比较器 MUST 输出该场景既有性能比较记录，且任何性能数值变差都 MUST 返回成功

#### Scenario: v9 同场景比较只记录性能

- **GIVEN** baseline 与 current 都是完整有效且身份兼容的 scenario v9 报告
- **WHEN** 比较器执行同场景比较
- **THEN** 比较器 MUST 输出该场景既有性能比较记录，且任何性能数值变差都 MUST 返回成功

#### Scenario: v8 同场景比较只记录性能

- **GIVEN** baseline 与 current 都是完整有效且身份兼容的 scenario v8 报告
- **WHEN** 比较器执行同场景比较
- **THEN** 比较器 MUST 输出该场景既有性能比较记录，且任何性能数值变差都 MUST 返回成功

#### Scenario: v7 同场景比较只记录性能

- **GIVEN** baseline 与 current 都是完整有效且身份兼容的 scenario v7 报告
- **WHEN** 比较器执行同场景比较
- **THEN** 比较器 MUST 输出该场景既有性能比较记录，且任何性能数值变差都 MUST 返回成功

#### Scenario: v6 同场景比较只记录性能

- **GIVEN** baseline 与 current 都是完整有效且身份兼容的 scenario v6 报告
- **WHEN** 比较器执行同场景比较
- **THEN** 比较器 MUST 输出该场景既有性能比较记录，且任何性能数值变差都 MUST 返回成功

#### Scenario: v15 与 v16 不静默混比

- **GIVEN** baseline 为 scenario v15 且 current 为 scenario v16
- **WHEN** 调用方未提供显式迁移授权
- **THEN** 比较器 MUST 拒绝相对比较并说明场景版本不一致

#### Scenario: 显式授权 v15 到 v16 的迁移

- **GIVEN** 两份报告完整有效、同硬件且使用同一 transport
- **WHEN** 调用方显式授权 `15:16` 迁移
- **THEN** 比较器 MUST 输出绝对性能记录、跳过不同 workload 间的相对回归判定，且性能数值不理想 MUST 返回成功

#### Scenario: 唯一迁移之外的参数被拒绝

- **GIVEN** baseline 与 current 的 scenario 不同
- **WHEN** 调用方使用 `6:16`、`14:15`、`14:16`、`16:15`、`15:15`、`16:16` 或其他非 `15:16` 参数
- **THEN** 比较器 MUST 拒绝比较并说明迁移方向不兼容

#### Scenario: M2 v15 基线不因 v16 记录改写

- **GIVEN** 当前 M2 基线是完整 scenario v15 Memory 报告
- **WHEN** 项目生成或比较 scenario v16 记录
- **THEN** M2 v15 基线字节与路径 MUST 保持不变，v16 报告 MUST NOT 被自动提升为该基线

#### Scenario: 历史报告保持可读取

- **GIVEN** 一份完整 scenario v6 至 v15 报告，或两份相同历史 scenario 的完整报告
- **WHEN** 调用方读取或比较该历史证据
- **THEN** 比较器 MUST 按该历史场景原有完整性规则校验，不得要求其满足 v16，也不得把历史迁移当作当前授权

#### Scenario: 跨硬件迁移被拒绝

- **GIVEN** 两份 scenario 不同且硬件身份不同的报告
- **WHEN** 调用方请求迁移比较
- **THEN** 比较器 MUST 拒绝比较，且不得以场景升级为由执行跨硬件归一化

#### Scenario: 跨 transport 不接受场景迁移

- **GIVEN** 同一 commit 的 Memory scenario v15 与 TCP scenario v16 报告
- **WHEN** 调用方带 `15:16` 授权请求跨 transport 比较
- **THEN** 比较器 MUST 先拒绝 scenario 身份不一致；`15:16` 授权 MUST 只适用于同 transport 的 workload 迁移

#### Scenario: 完整报告不因性能数值中止写入

- **GIVEN** producer 已取得完整有效的 v16 样本
- **WHEN** 一个或多个性能指标超过绝对阈值或相对记录值
- **THEN** producer MUST 写出完整 JSON，且不得因这些性能数值返回失败

#### Scenario: 无效或不完整报告仍失败

- **GIVEN** 报告损坏、缺少必需字段、样本不完整，或 transport 与 commit 身份和比较请求不一致
- **WHEN** producer 或比较器验证该报告
- **THEN** 操作 MUST 返回错误，且不得把该报告视为完整性能记录

#### Scenario: 队列高水位只记录而真实溢出失败

- **GIVEN** 报告包含队列高水位与 overflow/data-loss 状态
- **WHEN** 比较器验证报告
- **THEN** 只有高水位升高时 MUST 记录并返回成功；报告声明真实 overflow 或数据丢失时 MUST 返回错误

#### Scenario: 报告写入错误仍失败

- **GIVEN** producer 已生成完整报告内容
- **WHEN** 无法把报告写到请求的输出位置
- **THEN** producer MUST 返回包含 I/O 上下文的错误

#### Scenario: 实现优化保持 scenario v16

- **GIVEN** 优化前后的传播语义、固定上传布局、分辨率、阶段时长、样本数和统计口径完全相同
- **WHEN** 项目只减少实现开销或修复资源滞留
- **THEN** producer MUST 继续标记 scenario v16，且比较器 MUST 继续输出既有 v16 性能记录

#### Scenario: workload 或测量口径变化不能藏在 v16

- **GIVEN** 项目准备再次生成性能报告
- **WHEN** 修改改变固定上传布局、传播语义、分辨率、阶段时长、样本数、场景运动、指标定义或其他 benchmark workload
- **THEN** 项目 MUST 先升级场景版本并修订迁移规则，不得把变化后的报告标记为 scenario v16
