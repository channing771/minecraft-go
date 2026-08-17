# companion-chat-protocol Specification

## Purpose

定义协议 v16 中伙伴实体同步与 `@名称 指令` 的有界线格式，使 Memory/TCP 客户端都只观察服务端确定的寻址事实而不触发尚未交付的 AI 行为。
## Requirements
### Requirement: 协议 v16 追加固定伙伴与聊天消息

线上协议 SHALL 升级到 v18，且 MUST 保留既有全部 message ID 不变。Client `ChatCommand` MUST 使用 ID 12；Server `ChatEvent`、`CompanionSpawn`、`CompanionStates`、`CompanionDespawn` MUST 分别使用 ID 16、17、18、19。v17 与 v18 MUST NOT 跨版本互通，Memory 与 TCP MUST 产生相同 wire 内容、验证结果与可观察事件。

#### Scenario: v16 登录成功而 v15 被拒绝

- **GIVEN** 一个运行当前协议版本（v18）的服务端
- **WHEN** v18 客户端、v17 客户端、v16 客户端和 v15 客户端分别尝试登录
- **THEN** v18 登录 MUST 按既有规则继续，v17、v16 与 v15 登录 MUST 以协议版本不匹配被明确拒绝

#### Scenario: v17 登录成功而 v16 被拒绝

- **GIVEN** 一个运行协议 v18 的服务端
- **WHEN** v18 客户端和 v17 客户端分别尝试登录
- **THEN** v18 登录 MUST 按既有规则继续，v17 客户端 MUST 以协议版本不匹配被明确拒绝

#### Scenario: 旧 ID 保持追加兼容

- **GIVEN** v17 的全部 client/server message ID 清单
- **WHEN** 升级到 v18
- **THEN** 所有既有 ID MUST 保持原消息类型与 wire 语义，v18 MUST NOT 引入新的 message ID

### Requirement: ChatCommand 和 ChatEvent 保持有界事实语义

`ChatCommand` 文本 MUST 是 `1..1024` UTF-8 bytes、有效 UTF-8、无 NUL/Unicode control 且无首尾空白。`ChatEvent` SHALL 携带严格递增的进程内 event ID 和完整发令玩家身份；Accepted 事件 MUST 携带伙伴身份与 trim 后指令，InvalidFormat MUST 清空伙伴和指令字段，UnknownCompanion MUST 只保留合法目标名称。M5B 起 `ChatEventKind` SHALL 包含 `TaskStarted`、`TaskProgress`、`TaskCompleted`、`TaskFailed` 与 `TaskTimedOut`，`ChatRejectReason` SHALL 包含 `QueueFull`；M5C 起 `ChatEventKind` SHALL 追加 `TaskStopped`（携带伙伴身份与原始指令，reason 为 None），`ChatRejectReason` SHALL 追加用于停止旁路同步拒绝的 `NotFollowing`，`TaskFailReason` SHALL 追加 `TaskFailInventoryFull`（采掘产物或放置物品在伙伴背包无容量/已耗尽）。任务事件 MUST 携带伙伴身份与对应的原始指令，`TaskFailed` MUST 携带稳定失败原因枚举，任务事件 MUST NOT 携带模型生成的自由文本。固定最大 wire 长度 SHALL 保持 ChatCommand 1026 bytes、ChatEvent 1328 bytes 不变。

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

#### Scenario: QueueFull 只回复发令者

- **GIVEN** 一个伙伴 FIFO 已满
- **WHEN** 玩家发送寻址该伙伴的合法指令
- **THEN** `QueueFull` 拒绝事件 MUST 只发送给发令者，其余在线玩家 MUST NOT 收到该事件

### Requirement: 伙伴同步消息有界且按 ID 排序

`CompanionSpawn` SHALL 携带伙伴 ID、名称、tick、维度、位置与朝向；`CompanionStates` SHALL 携带一个 tick 内 `1..4` 个按 ID 严格升序且不重复的身体状态；`CompanionDespawn` SHALL 只按独立 `CompanionID` 寻址。移动中的伙伴位置变化 MUST 通过既有 `CompanionStates` 逐 tick 传达，MUST NOT 引入新的位置消息。固定最大 wire 长度 MUST 分别为 178、173 bytes；接收方 MUST 在接受消息前验证全部长度、计数、顺序和字段，非法计数不得触发与声明数量成比例的工作或内存增长。

#### Scenario: 五项或无序状态被原子拒绝

- **GIVEN** 一个包含五项、重复 ID 或非升序 ID 的 `CompanionStates`
- **WHEN** 客户端解码并应用该批次
- **THEN** 整批消息 MUST 被拒绝，现有伙伴镜像 MUST 不发生部分变化

#### Scenario: 同字节玩家与伙伴不会混淆

- **GIVEN** 一个 `PlayerID` 和一个 `CompanionID` 具有相同 16 bytes
- **WHEN** 客户端接收各自的 spawn/state 消息
- **THEN** 两个实体 MUST 保存在不同镜像并使用不同消息类型，伙伴 MUST NOT 被编码为远端玩家消息

### Requirement: 聊天只在 tick 边界执行精确寻址

服务端 SHALL 只接受 `@伙伴名 指令`，以固定容量接收请求，按接收顺序在 tick 边界处理，并按大小写精确匹配 active 名称。Accepted MUST 广播给全部在线玩家，且 Accepted 的指令 SHALL 进入该伙伴 FIFO 创建任务；InvalidFormat、UnknownCompanion 与 QueueFull MUST 只回复发令者，且 MUST NOT 创建任务或调用模型。M5C 起 `@伙伴名 停止`（精确文本、大小写敏感）是唯一绕过 FIFO 的控制指令：作用于持续跟随任务时按 `companion-follow` 规格执行，否则以 `NotFollowing` 只回复发令者。寻址本身 MUST NOT 改变伙伴身体或世界状态；移动与世界动作只能由进入 Running 的任务经权威模拟驱动。

#### Scenario: 精确名称产生接受事件

- **GIVEN** active 伙伴名称为 `阿木` 与 `阿木甲`
- **WHEN** 玩家发送 `@阿木 去 12 65 -4`
- **THEN** 服务端 MUST 精确寻址 `阿木`，广播一条 Accepted 事件，为其 FIFO 创建任务，并 MUST NOT 对 `阿木甲` 或世界状态产生作用

#### Scenario: 非法目标与未知目标可区分

- **GIVEN** 一条目标超过名称边界的命令和一条目标合法但未配置的命令
- **WHEN** 服务端在 tick 边界处理二者
- **THEN** 第一条 MUST 得到 InvalidFormat，第二条 MUST 得到 UnknownCompanion，且两条都 MUST NOT 执行指令文本

#### Scenario: 队列满同步拒绝且不调模型

- **GIVEN** 一个伙伴 FIFO 已有 16 条待执行指令
- **WHEN** 玩家发送寻址该伙伴的合法指令
- **THEN** 服务端 MUST 只向发令者回复 QueueFull，MUST NOT 创建任务或调用模型，既有 16 条 MUST 保持不变

#### Scenario: 停止指令绕过 FIFO

- **GIVEN** 一个伙伴正在持续跟随且 FIFO 有待执行指令
- **WHEN** 任意在线玩家发送 `@该伙伴 停止`
- **THEN** 服务端 MUST 广播 `TaskStopped`，MUST NOT 为停止创建 FIFO 任务，原队首任务 MUST 立即开始执行

#### Scenario: Memory 与 TCP 顺序一致

- **GIVEN** Memory 与 TCP 模式收到同样顺序的多条有效、无效与停止命令
- **WHEN** 两种模式推进相同 tick 并完成相同任务
- **THEN** 两者 MUST 产生相同顺序、种类、寻址身份、任务状态和接收者范围的 ChatEvent

