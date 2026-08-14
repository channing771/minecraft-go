## Purpose

以单一有界、可校验且原子替换的世界文件保存伙伴身体，同时保留暂时移出配置的记录，避免配置失误、存档损坏或运行期 I/O 失败造成静默数据丢失。

## ADDED Requirements

### Requirement: companions.ai schema v1 只保存伙伴身体

世界目录 SHALL 使用单一 `companions.ai` 文件和 companion schema v1。文件 MUST 保存聚合 revision，以及每条记录的 `CompanionID`、维度、位置、yaw、pitch、36 格背包及 selected slot；名称、persona、原始指令、任务、FIFO、计划和摘要 MUST NOT 写入 v1。玩家 schema v6、区块 schema v8 与世界 metadata v2 MUST 保持不变。

#### Scenario: 身体状态往返且名称来自配置

- **GIVEN** 两个伙伴具有不同身体、工具耐久与背包内容，配置同时提供名称
- **WHEN** schema v1 保存后重启恢复
- **THEN** 身体和背包 MUST 精确恢复，名称 MUST 重新取自当前配置，文件 MUST 不含名称或任务字段

#### Scenario: 后续任务状态不能写入 v1

- **GIVEN** M5A 运行期间成功寻址了一条聊天指令
- **WHEN** 服务端保存 `companions.ai`
- **THEN** schema MUST 仍为 v1，文件 MUST 只包含身体记录，聊天文本、任务和 FIFO MUST 不存在

### Requirement: 存档格式有界且损坏不可静默覆盖

`companions.ai` MUST 使用 magic `MCAI`、envelope v1、schema v1、聚合 revision、记录数、payload 长度与覆盖 schema header 和 payload 的 CRC32C。记录 MUST 按 `CompanionID` 严格升序且不重复，active+inactive 总数 MUST 不超过 64，物理文件最大长度 MUST 为 14,176 bytes。未来版本、CRC 错误、截断、超长、非法数值或非法背包 MUST 被拒绝；保存 MUST NOT 覆盖已损坏或未来版本的正式文件。

#### Scenario: 第六十五个身份被拒绝且旧记录保留

- **GIVEN** 存档已经包含 64 个不同的 active+inactive ID
- **WHEN** 配置尝试引入第六十五个新 ID
- **THEN** 启动 MUST 失败，正式文件和全部旧记录 MUST 保持不变

#### Scenario: 损坏文件不被新保存掩盖

- **GIVEN** AI 已启用且正式 `companions.ai` 的 CRC、长度或 schema 无效
- **WHEN** 服务端加载或尝试保存伙伴状态
- **THEN** 操作 MUST 返回可识别的损坏或未来版本错误，并 MUST NOT 用新文件覆盖正式文件

### Requirement: 配置合并保留 inactive 记录

AI 启用时，服务端 SHALL 先验证配置再加载存档：已存且仍配置的 ID MUST 恢复身体；新配置 ID MUST 从世界出生点创建空背包身体；已存但不再配置的 ID MUST 保留为 inactive 且不得注册到模拟。配置为空时 AI MUST 关闭，服务端 MUST NOT 加载、保存或改写已有 `companions.ai`。

#### Scenario: 暂时移除配置不会删除身体

- **GIVEN** 存档含一个伙伴身体，但当前非空配置没有该 ID
- **WHEN** 服务端启动、运行并保存其他 active 伙伴
- **THEN** 该记录 MUST 作为 inactive 保留在聚合文件中，且不得出现在权威模拟或客户端

#### Scenario: 清空配置保持文件原样

- **GIVEN** 世界目录已有 `companions.ai` 且当前 `ai.companions` 为空
- **WHEN** 服务端启动并安全关闭
- **THEN** 服务端 MUST 保持 AI 关闭，不读取、不保存也不改写该文件

### Requirement: 运行期保存异步、可重试且关服可靠

伙伴身体变化 SHALL 在权威 tick 边界进入待保存状态，磁盘 I/O MUST NOT 阻塞权威 tick。任一时刻 MUST 最多执行一次聚合保存；运行期失败 MUST 保留旧正式文件与最新未保存状态，并按既有 tick 调度重试。安全关服 MUST 在世界存储完成持久同步与关闭前保存最后一次权威 step 后的最新身体；失败 MUST 返回错误并允许再次关服重试。

#### Scenario: 保存失败保留未保存状态后重试

- **GIVEN** 一次聚合保存因 I/O 错误失败且内存中有更新版本
- **WHEN** 后续 retry tick 到达
- **THEN** 系统 MUST 重试最新未保存状态，旧正式文件 MUST 在成功原子替换前保持可读，较早保存的失败或完成 MUST NOT 丢弃其后发生的更新

#### Scenario: 关服顺序防止最后状态丢失

- **GIVEN** 关服 drain 的最后一次权威 step 创建或更新了伙伴身体
- **WHEN** 服务端执行安全关服
- **THEN** 可观察持久化顺序 MUST 是伙伴保存、世界存储持久同步、世界存储关闭；伙伴保存失败时 MUST 保持可重试状态且不得继续关闭世界存储
