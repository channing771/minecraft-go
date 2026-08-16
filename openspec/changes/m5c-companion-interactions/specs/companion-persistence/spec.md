# companion-persistence Specification

## MODIFIED Requirements

### Requirement: companions.ai schema v2 保存身体与任务状态

世界目录 SHALL 继续使用单一 `companions.ai` 文件，companion schema 保持 v2。v2 每条记录 MUST 保存 v1 的全部身体字段（`CompanionID`、维度、位置、yaw、pitch、36 格背包及 selected slot），active 记录 MUST 额外保存当前任务（原始指令、计划步骤、步骤索引、状态、开始 tick 与 deadline）与最多 16 条 FIFO 指令；计划步骤的 kind 在 M5C 起 MUST 支持交付全集 `go_to`/`follow`/`mine`/`place`（schema 结构不变，仅扩展合法枚举值）。inactive 记录 MUST 只保存身体字段。名称与 persona MUST NOT 写入文件，名称 MUST 继续取自当前配置。模型计划 MUST 只在 `Validating` 成功后落盘；关服时仍处 `Planning` 或 `Validating` 的任务 MUST 以 `Queued` 状态与原始指令一并保存。持续跟随任务 MUST 持久化为步骤 `follow` 与目标玩家 ID，且 MUST NOT 保存 deadline。玩家 schema v6、区块 schema v8 与世界 metadata v2 MUST 保持不变。

#### Scenario: 任务与 FIFO 跨重启精确恢复

- **GIVEN** 一个伙伴有一个 Running 的多步任务与两条待执行 FIFO 指令
- **WHEN** schema v2 保存后重启恢复
- **THEN** 当前任务的指令、计划、步骤索引、状态、开始 tick 与 deadline 以及 FIFO 顺序 MUST 精确恢复，身体与背包 MUST 与 v1 语义一致

#### Scenario: 持续跟随跨重启恢复且不保存 deadline

- **GIVEN** 一个伙伴有一个 Running 的持续跟随任务
- **WHEN** schema v2 保存后重启恢复
- **THEN** 任务 MUST 以 `follow` 步骤与目标玩家 ID 精确恢复，deadline 字段 MUST 为未设置，恢复后按在线性重验继续或失败

#### Scenario: 规划中任务按 Queued 恢复

- **GIVEN** 服务端在任务 Planning 阶段安全关服
- **WHEN** 存档保存并随后恢复
- **THEN** 该任务 MUST 以 `Queued` 状态恢复并保留原始指令，重启后 MUST 重新发起规划

#### Scenario: v1 文件只读迁移

- **GIVEN** 一个仅包含身体记录的 schema v1 `companions.ai`
- **WHEN** 服务端启动加载
- **THEN** 全部身体记录 MUST 按既有规则恢复，所有任务与 FIFO MUST 为空，首次保存 MUST 写出 schema v2

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
