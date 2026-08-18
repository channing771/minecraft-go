# companion-chat-protocol Specification

## MODIFIED Requirements

### Requirement: 协议 v16 追加固定伙伴与聊天消息

线上协议 SHALL 升级到 v19，且 MUST 保留既有全部 message ID 不变。Client `ChatCommand` MUST 使用 ID 12；Server `ChatEvent`、`CompanionSpawn`、`CompanionStates`、`CompanionDespawn` MUST 分别使用 ID 16、17、18、19。v18 与 v19 MUST NOT 跨版本互通，Memory 与 TCP MUST 产生相同 wire 内容、验证结果与可观察事件。

#### Scenario: v16 登录成功而 v15 被拒绝

- **GIVEN** 一个运行当前协议版本（v19）的服务端
- **WHEN** v19 客户端、v18 客户端、v17 客户端、v16 客户端和 v15 客户端分别尝试登录
- **THEN** v19 登录 MUST 按既有规则继续，v18、v17、v16 与 v15 登录 MUST 以协议版本不匹配被明确拒绝

#### Scenario: v17 登录成功而 v16 被拒绝

- **GIVEN** 一个运行协议 v19 的服务端
- **WHEN** v19 客户端和 v18 客户端分别尝试登录
- **THEN** v19 登录 MUST 按既有规则继续，v18 客户端 MUST 以协议版本不匹配被明确拒绝

#### Scenario: 旧 ID 保持追加兼容

- **GIVEN** v18 的全部 client/server message ID 清单
- **WHEN** 升级到 v19
- **THEN** 所有既有 ID MUST 保持原消息类型与 wire 语义，v19 MUST NOT 引入新的 message ID

### Requirement: ChatCommand 和 ChatEvent 保持有界事实语义

`ChatCommand` 文本 MUST 是 `1..1024` UTF-8 bytes、有效 UTF-8、无 NUL/Unicode control 且无首尾空白。`ChatEvent` SHALL 携带严格递增的进程内 event ID 和完整发令玩家身份；Accepted 事件 MUST 携带伙伴身份与 trim 后指令，InvalidFormat MUST 清空伙伴和指令字段，UnknownCompanion MUST 只保留合法目标名称。M5B 起 `ChatEventKind` SHALL 包含 `TaskStarted`、`TaskProgress`、`TaskCompleted`、`TaskFailed` 与 `TaskTimedOut`，`ChatRejectReason` SHALL 包含 `QueueFull`；M5C 起 `ChatEventKind` SHALL 追加 `TaskStopped`（携带伙伴身份与原始指令，reason 为 None），`ChatRejectReason` SHALL 追加用于停止旁路同步拒绝的 `NotFollowing`，`TaskFailReason` SHALL 追加 `TaskFailInventoryFull`（采掘产物或放置物品在伙伴背包无容量/已耗尽）。任务事件 MUST 携带伙伴身份与对应的原始指令，`TaskFailed` MUST 携带稳定失败原因枚举，任务事件 MUST NOT 携带模型生成的自由文本。M5D 起 `ChatEventKind` SHALL 追加 `CompanionSpeech`：MUST 携带伙伴身份与不超过 256 bytes 有效 UTF-8、无 NUL/Unicode control 的台词文本，reason MUST 为 None，且 MUST 广播给全部在线玩家；`CompanionSpeech` 是 ChatEvent 中唯一允许携带模型生成文本的 kind。固定最大 wire 长度 SHALL 保持 ChatCommand 1026 bytes、ChatEvent 1328 bytes 不变。

#### Scenario: 文本边界原子验证

- **GIVEN** 一条 1024-byte 的有效命令和一条 1025-byte 的命令
- **WHEN** 系统分别序列化或接收它们
- **THEN** 1024-byte 命令 MUST 被接受，1025-byte 命令 MUST 在应用任何字段前被拒绝

#### Scenario: 拒绝事件不泄漏无效字段

- **GIVEN** 一条格式非法的命令和一条目标名称合法但未配置的命令
- **WHEN** 服务端生成拒绝事件
- **THEN** InvalidFormat MUST 只含玩家身份与原因，UnknownCompanion MUST 额外只含合法目标名称，二者 MUST 只发送给发令者

#### Scenario: 任务事件字段组合严格

- **GIVEN** 一条 `TaskFailed` 事件与一条 `TaskStarted` 事件
- **WHEN** 系统编码并验证它们
- **THEN** 二者 MUST 携带合法事件 ID、玩家身份、伙伴身份与原始指令，`TaskFailed` 的原因 MUST 来自固定枚举，任何非法 kind 与原因组合 MUST 在应用字段前被拒绝

#### Scenario: TaskStopped 事件字段组合

- **GIVEN** 一条 `TaskStopped` 事件
- **WHEN** 系统编码并验证它
- **THEN** 它 MUST 携带合法事件 ID、发令玩家身份、伙伴身份与被停止任务的原始指令，reason MUST 为 None，且 MUST 广播给全部在线玩家

#### Scenario: CompanionSpeech 事件字段组合与广播

- **GIVEN** 一条 `CompanionSpeech` 事件（256-byte 合法台词）与一条 257-byte 台词的待编码事件
- **WHEN** 系统编码并分发
- **THEN** 合法事件 MUST 携带合法 event ID、伙伴身份与台词文本、reason 为 None 并广播给全部在线玩家；257-byte 台词 MUST 在编码前被拒绝且 MUST NOT 产生任何部分广播

#### Scenario: QueueFull 只回复发令者

- **GIVEN** 一个伙伴 FIFO 已满
- **WHEN** 玩家发送寻址该伙伴的合法指令
- **THEN** `QueueFull` 拒绝事件 MUST 只发送给发令者，其余在线玩家 MUST NOT 收到该事件
