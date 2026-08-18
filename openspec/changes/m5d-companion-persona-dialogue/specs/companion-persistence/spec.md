# companion-persistence Specification

## MODIFIED Requirements

### Requirement: 存档格式有界且损坏不可静默覆盖

`companions.ai` MUST 使用 magic `MCAI`、envelope v1、schema v4、聚合 revision、记录数、payload 长度与覆盖 schema header 和 payload 的 CRC32C。schema v3、v2 与 v1 文件 MUST 按只读迁移加载，未来版本 MUST 被拒绝。记录 MUST 按 `CompanionID` 严格升序且不重复，active+inactive 总数 MUST 不超过 64。单条记录任务字段 MUST 有界：原始指令与 FIFO 每条指令不超过 1,024 bytes，计划步骤数不超过 5,000，FIFO 不超过 16 条，最近对话摘要不超过 2,048 bytes。物理文件最大长度 MUST 为 438,280 bytes（= 430,080 + 4 条 active 记录各 2,050 bytes 可选摘要区），解码 MUST 在任何解析与分配之前拒绝超长。未来版本、CRC 错误、截断、超长、非法数值、非法任务状态、变长步骤错位（非法 kind）、非法摘要长度或非法背包 MUST 被拒绝；保存 MUST NOT 覆盖已损坏或未来版本的正式文件。

#### Scenario: 第六十五个身份被拒绝且旧记录保留

- **GIVEN** 存档已经包含 64 个不同的 active+inactive ID
- **WHEN** 配置尝试引入第六十五个新 ID
- **THEN** 启动 MUST 失败，正式文件和全部旧记录 MUST 保持不变

#### Scenario: 损坏文件不被新保存掩盖

- **GIVEN** AI 已启用且正式 `companions.ai` 的 CRC、长度或 schema 无效
- **WHEN** 服务端加载或尝试保存伙伴状态
- **THEN** 操作 MUST 返回可识别的损坏或未来版本错误，并 MUST NOT 用新文件覆盖正式文件

## ADDED Requirements

### Requirement: schema v4 增加最近对话摘要

companion schema SHALL 升级到 v4。v4 每条记录 MUST 保存 v3 的全部字段，并 MAY 追加可选的最近对话摘要区：2-byte 长度前缀加不超过 2,048 bytes 的有效 UTF-8 摘要文本。摘要 MUST 只属于有过对话历史的记录；inactive 记录 MUST NOT 保存摘要（迁移或去激活时丢弃既有摘要）。摘要文本 MUST NOT 含 NUL；名称与 persona MUST 继续不写入文件。v3、v2 与 v1 文件 MUST 按只读迁移加载（迁移后摘要为空），首次保存 MUST 写出 schema v4，未来版本 MUST 被拒绝。摘要更新 MUST 标记 AI 存档 dirty 并按既有异步保存纪律落盘。玩家 schema v6、区块 schema v8 与世界 metadata v2 MUST 保持不变。

#### Scenario: 摘要跨重启精确恢复

- **GIVEN** 一个 active 伙伴的终态台词写入了 1 KiB 摘要并完成保存
- **WHEN** 服务端重启并加载该存档
- **THEN** 该伙伴的摘要 MUST 逐字节恢复，其余记录的摘要 MUST 为空，文件 MUST 为 schema v4

#### Scenario: v3 文件只读迁移到 v4

- **GIVEN** 一个 schema v3 `companions.ai`（无摘要区）
- **WHEN** 服务端启动加载
- **THEN** 全部身体、任务与 FIFO 状态 MUST 无损恢复，所有摘要 MUST 为空，首次保存 MUST 写出 schema v4

#### Scenario: 去激活丢弃摘要

- **GIVEN** 存档中一个带摘要的伙伴被从当前配置移除
- **WHEN** 服务端启动并保存
- **THEN** 该记录 MUST 作为 inactive 保留身体字段且不带摘要区，其余 active 记录的摘要 MUST 不受影响
