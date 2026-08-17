# companion-persistence Specification

## Purpose

以单一有界、可校验且原子替换的世界文件保存伙伴身体，同时保留暂时移出配置的记录，避免配置失误、存档损坏或运行期 I/O 失败造成静默数据丢失。
## Requirements
### Requirement: 存档格式有界且损坏不可静默覆盖

`companions.ai` MUST 使用 magic `MCAI`、envelope v1、schema v3、聚合 revision、记录数、payload 长度与覆盖 schema header 和 payload 的 CRC32C。schema v2 与 v1 文件 MUST 按只读迁移加载，未来版本 MUST 被拒绝。记录 MUST 按 `CompanionID` 严格升序且不重复，active+inactive 总数 MUST 不超过 64。单条记录任务字段 MUST 有界：原始指令与 FIFO 每条指令不超过 1,024 bytes，计划步骤数不超过 5,000，FIFO 不超过 16 条。物理文件最大长度 MUST 为 430,080 bytes，解码 MUST 在任何解析与分配之前拒绝超长。未来版本、CRC 错误、截断、超长、非法数值、非法任务状态、变长步骤错位（非法 kind）或非法背包 MUST 被拒绝；保存 MUST NOT 覆盖已损坏或未来版本的正式文件。

#### Scenario: 第六十五个身份被拒绝且旧记录保留

- **GIVEN** 存档已经包含 64 个不同的 active+inactive ID
- **WHEN** 配置尝试引入第六十五个新 ID
- **THEN** 启动 MUST 失败，正式文件和全部旧记录 MUST 保持不变

#### Scenario: 损坏文件不被新保存掩盖

- **GIVEN** AI 已启用且正式 `companions.ai` 的 CRC、长度或 schema 无效
- **WHEN** 服务端加载或尝试保存伙伴状态
- **THEN** 操作 MUST 返回可识别的损坏或未来版本错误，并 MUST NOT 用新文件覆盖正式文件

### Requirement: 配置合并保留 inactive 记录

AI 启用时，服务端 SHALL 先验证配置再加载存档：已存且仍配置的 ID MUST 恢复身体与其任务/FIFO 状态；新配置 ID MUST 从世界出生点创建空背包、无任务身体；已存但不再配置的 ID MUST 保留为 inactive（仅身体字段）且不得注册到模拟。配置为空时 AI MUST 关闭，服务端 MUST NOT 加载、保存或改写已有 `companions.ai`。

#### Scenario: 暂时移除配置不会删除身体

- **GIVEN** 存档含一个带任务的伙伴记录，但当前非空配置没有该 ID
- **WHEN** 服务端启动、运行并保存其他 active 伙伴
- **THEN** 该记录 MUST 作为 inactive 保留在聚合文件中，且不得出现在权威模拟或客户端

#### Scenario: 清空配置保持文件原样

- **GIVEN** 世界目录已有 `companions.ai` 且当前 `ai.companions` 为空
- **WHEN** 服务端启动并安全关闭
- **THEN** 服务端 MUST 保持 AI 关闭，不读取、不保存也不改写该文件

### Requirement: 运行期保存异步、可重试且关服可靠

伙伴身体与任务/FIFO 状态变化 SHALL 在权威 tick 边界进入待保存状态，磁盘 I/O MUST NOT 阻塞权威 tick。任一时刻 MUST 最多执行一次聚合保存；运行期失败 MUST 保留旧正式文件与最新未保存状态，并按既有 tick 调度重试。安全关服 MUST 在世界存储完成持久同步与关闭前保存最后一次权威 step 后的最新状态；失败 MUST 返回错误并允许再次关服重试。

#### Scenario: 保存失败保留未保存状态后重试

- **GIVEN** 一次聚合保存因 I/O 错误失败且内存中有更新版本
- **WHEN** 后续 retry tick 到达
- **THEN** 系统 MUST 重试最新未保存状态，旧正式文件 MUST 在成功原子替换前保持可读，较早保存的失败或完成 MUST NOT 丢弃其后发生的更新

#### Scenario: 关服顺序防止最后状态丢失

- **GIVEN** 关服 drain 的最后一次权威 step 更新了伙伴身体或任务状态
- **WHEN** 服务端执行安全关服
- **THEN** 可观察持久化顺序 MUST 是伙伴保存、世界存储持久同步、世界存储关闭；伙伴保存失败时 MUST 保持可重试状态且不得继续关闭世界存储

### Requirement: 恢复任务在下一动作前重验

恢复的 `Running` 任务在提交下一个动作前 MUST 按当前权威状态重新校验目标与路径点合法性；校验失败 MUST 令任务以既有失败语义终止，MUST NOT 从旧路径点继续盲走。恢复的持续跟随任务 MUST 在下一动作前重新校验目标玩家在线性：目标已离线则任务 MUST 以 `TaskFailWorldChanged` 失败；目标在线则继续跟随。恢复的 FIFO MUST 保持顺序并从前一个任务之后继续执行。

#### Scenario: 世界已变化的恢复任务失败而不盲走

- **GIVEN** 关服前一条已验证路径，重启后目标站立点已被方块占据
- **WHEN** 恢复的任务尝试继续执行
- **THEN** 任务 MUST 先重验并按路径不可达或重算语义处理，MUST NOT 沿旧路径点产生任何移动

#### Scenario: 恢复的跟随任务先验在线性

- **GIVEN** 关服前一个持续跟随任务，重启后目标玩家不在线
- **WHEN** 恢复的任务推进第一个 tick
- **THEN** 任务 MUST 以 `TaskFailWorldChanged` 失败并广播事件，MUST NOT 产生任何移动

### Requirement: companions.ai schema v3 保存身体与任务状态

世界目录 SHALL 继续使用单一 `companions.ai` 文件，companion schema 升级到 v3。v3 每条记录 MUST 保存 v2 的全部字段（`CompanionID`、维度、位置、yaw、pitch、36 格背包及 selected slot；active 记录的任务区与最多 16 条 FIFO 指令），计划步骤 MUST 按 kind 变长编码：`go_to`/`mine` 13 bytes（kind + 3×int32 坐标）、`place` 15 bytes（追加 block uint16）、`follow` 17 bytes（kind + 16-byte 目标玩家 ID，坐标不落盘）。计划步骤的 kind MUST 属于交付全集 `go_to`/`follow`/`mine`/`place`，且各 kind 未使用字段 MUST 为零。inactive 记录 MUST 只保存身体字段。名称与 persona MUST NOT 写入文件，名称 MUST 继续取自当前配置。模型计划 MUST 只在 `Validating` 成功后落盘；关服时仍处 `Planning` 或 `Validating` 的任务 MUST 以 `Queued` 状态与原始指令一并保存。持续跟随任务 MUST 持久化为步骤 `follow` 与目标玩家 ID，且 MUST NOT 保存 deadline（deadline 字段零值）。玩家 schema v6、区块 schema v8 与世界 metadata v2 MUST 保持不变。

#### Scenario: 四 kind 任务与 FIFO 跨重启精确恢复

- **GIVEN** 一个伙伴有一个 Running 的多步任务（`go_to`/`mine`/`place`/`follow` 各至少一步）与两条待执行 FIFO 指令
- **WHEN** schema v3 保存后重启恢复
- **THEN** 当前任务的指令、计划（含 place 方块与 follow 目标玩家 ID）、步骤索引、状态与开始 tick 以及 FIFO 顺序 MUST 精确恢复，身体与背包 MUST 与 v2 语义一致
- **AND** deadline 除持续跟随（零值）外 MUST 精确恢复

#### Scenario: 持续跟随跨重启恢复且不保存 deadline

- **GIVEN** 一个伙伴有一个 Running 的持续跟随任务
- **WHEN** schema v3 保存后重启恢复
- **THEN** 任务 MUST 以 `follow` 步骤与目标玩家 ID 精确恢复，deadline 字段 MUST 为未设置，恢复后按在线性重验继续或失败

#### Scenario: 规划中任务按 Queued 恢复

- **GIVEN** 服务端在任务 Planning 阶段安全关服
- **WHEN** 存档保存并随后恢复
- **THEN** 该任务 MUST 以 `Queued` 状态恢复并保留原始指令，重启后 MUST 重新发起规划

#### Scenario: v2 文件只读迁移

- **GIVEN** 一个 schema v2 `companions.ai`（任务步骤全部为既有 `go_to` 载荷）
- **WHEN** 服务端启动加载
- **THEN** 全部身体、任务与 FIFO 状态 MUST 无损恢复，首次保存 MUST 写出 schema v3（`go_to` 步骤按相同 13-byte 布局到达）

#### Scenario: v1 文件只读迁移

- **GIVEN** 一个仅包含身体记录的 schema v1 `companions.ai`
- **WHEN** 服务端启动加载
- **THEN** 全部身体记录 MUST 按既有规则恢复，所有任务与 FIFO MUST 为空，首次保存 MUST 写出 schema v3

