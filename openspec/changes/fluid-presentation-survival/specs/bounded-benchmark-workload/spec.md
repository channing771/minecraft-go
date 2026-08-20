## MODIFIED Requirements

### Requirement: 工作负载变化使用新场景版本

Avatar、NameTag 与 Hotbar HUD 为容纳最多七名远端玩家、四个伙伴、一个目标标签和固定聊天 overlay 而改变固定 GPU 上传布局、offset 与每帧写入字节数后，benchmark 报告 MUST 标记为 scenario v16。默认世界内容因注水默认开启而改变、并新增水面绘制阶段与水下天空光衰减后，benchmark 报告 MUST 标记为 scenario v17。固定 benchmark 输入 MUST 继续是七名远端玩家、零伙伴且不注入聊天；v17 MUST 保持 `2560×1440` 离屏目标、阶段时长、运动、样本、指标、绝对阈值与 `20%` 相对回归阈值不变，其与 v16 的唯一差异 MUST 是注水默认开启带来的世界内容、水面绘制与天空光衰减。p99、FPS、RSS、GPU、tick、队列高水位、绝对阈值和相对回归结果 MUST 只记录和报告，不得导致 producer、比较器或 CI 失败；只要报告结构、字段和样本完整且数据有效，producer MUST 写出 JSON。既有 scenario v6 至 v16 报告与基线 MUST 保持可读取，比较器不得把不同 scenario 静默作相对比较。当前不同场景之间 MUST 只接受唯一显式 `16:17` 迁移；该迁移 MUST 校验报告完整性与硬件身份并跳过跨 workload 的相对回归判定。历史 `15:16`、`14:15` 与更早迁移只作为既有报告和归档证据，不再是当前可授权迁移。任何其他迁移参数、损坏或缺字段报告、样本不完整、硬件身份不兼容、transport 或 commit 身份不一致、真实 overflow 或数据丢失以及 I/O 错误 MUST 继续失败；队列高水位数值本身 MUST 只记录。

#### Scenario: v16 固定上传布局完整

- **GIVEN** scenario v16 使用固定七名远端玩家、零伙伴和空聊天输入
- **WHEN** producer 准备 Avatar、NameTag 与 Hotbar HUD 固定上传
- **THEN** Avatar MUST 容纳 66 个 body parts、instance 区 5280 bytes、indirect offset 5536、总上传 5556 bytes
- **AND** NameTag MUST 容纳 12 个标签、background 区 768 bytes、glyph offset 1024、glyph 区 24576 bytes、总上传 25600 bytes
- **AND** Hotbar HUD MUST 容纳 236 个 quad 与 700 个 glyph、glyph offset 11776、总容量 45376 bytes，空聊天帧实际写入 MUST 为 11776 bytes

#### Scenario: v17 只因注水与水面呈现区别于 v16

- **GIVEN** scenario v17 使用与 v16 相同的七名远端玩家、零伙伴和空聊天输入
- **WHEN** producer 准备该场景
- **THEN** 离屏目标、阶段时长、运动、样本与指标 MUST 与 v16 相同
- **AND** 二者的唯一差异 MUST 是注水默认开启带来的世界内容、水面绘制阶段与水下天空光衰减

#### Scenario: v17 同场景比较只记录性能

- **GIVEN** baseline 与 current 都是完整有效且身份兼容的 scenario v17 报告
- **WHEN** 比较器执行同场景比较
- **THEN** 比较器 MUST 输出既有绝对指标和相对回归记录，且任何性能数值变差都 MUST 返回成功

#### Scenario: 只接受 16:17 跨场景迁移

- **GIVEN** 一份 scenario v16 基线与一份 scenario v17 当前报告
- **WHEN** 比较器以显式 `16:17` 迁移参数运行
- **THEN** 比较器 MUST 校验报告完整性与硬件身份并跳过跨 workload 的相对回归判定
- **AND** 任何其他迁移参数（含历史的 `15:16`）MUST 失败

#### Scenario: v16 同场景比较只记录性能

- **GIVEN** baseline 与 current 都是完整有效且身份兼容的 scenario v16 报告
- **WHEN** 比较器执行同场景比较
- **THEN** 比较器 MUST 输出既有绝对指标和相对回归记录，且任何性能数值变差都 MUST 返回成功

#### Scenario: v15 同场景比较只记录性能

- **GIVEN** baseline 与 current 都是完整有效且身份兼容的 scenario v15 报告
- **WHEN** 比较器执行同场景比较
- **THEN** 比较器 MUST 输出该场景既有绝对指标和相对回归记录，且任何性能数值变差都 MUST 返回成功
