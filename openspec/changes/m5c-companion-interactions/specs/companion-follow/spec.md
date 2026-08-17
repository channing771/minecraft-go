# companion-follow Specification

## Purpose

定义持续跟随与唯一停止旁路的行为契约：跟随是无限期任务，其开始、距离边界、目标离线失败与玩家主动停止的全部可观察语义在此裁决。

## ADDED Requirements

### Requirement: 持续跟随是最后一步的无限期任务

`follow` 步骤 SHALL 携带一个目标 `player_id`，且 MUST 是计划的最后一步。进入 Running 后任务进入持续跟随：目标玩家在跟随距离之外时，Task Runner MUST 复用 M5B 的寻路窗口、重算冷却与三连失败语义向目标移动；进入跟随距离内 MUST 停止提交移动输入并保持原地。持续跟随 MUST NOT 记录普通任务 deadline，也 MUST NOT 因执行时长转入 `TimedOut`。目标玩家离线时任务 MUST 以 `TaskFailWorldChanged` 失败并广播事件。

#### Scenario: 距离边界内停止移动

- **GIVEN** 一个持续跟随任务的目标玩家位于跟随距离之外
- **WHEN** 伙伴寻路接近至跟随距离内
- **THEN** Task Runner MUST 停止提交移动输入，伙伴 MUST 在权威物理（重力/碰撞）作用下保持原地，MUST NOT 继续逼近目标

#### Scenario: 目标远离后恢复跟随

- **GIVEN** 伙伴已停在跟随距离内且目标玩家开始远离
- **WHEN** 目标超出跟随距离
- **THEN** 任务 MUST 恢复向目标移动，寻路重算按既有冷却与三连失败语义执行

#### Scenario: 持续跟随不受执行时长限制

- **GIVEN** 一个持续跟随任务已运行超过 `taskTimeoutMinutes` 配置的时长
- **WHEN** 权威 tick 继续推进
- **THEN** 任务 MUST NOT 转入 `TimedOut`，跟随 MUST 继续直到停止指令或目标离线

#### Scenario: 目标离线以目标变化失败

- **GIVEN** 一个持续跟随任务的目标玩家断开连接
- **WHEN** 服务端在 tick 边界推进该任务
- **THEN** 任务 MUST 以 `TaskFailWorldChanged` 失败并广播 `TaskFailed` 事件，FIFO MUST 推进下一项

### Requirement: 停止是唯一绕过 FIFO 的控制指令

`@伙伴名 停止`（精确文本、大小写敏感、无多余参数）SHALL 是唯一不进入 FIFO 的控制指令：作用于该伙伴当前 Running 的持续跟随任务时，MUST 立即清空移动输入、把任务转入 `Stopped` 终态并广播 `TaskStopped`，随后 MUST 立即开始执行原队首任务且 MUST NOT 清空或重排队列。当前任务不是持续跟随（或没有 Running 任务）时，`停止` MUST 只向发令者同步拒绝且 MUST NOT 改变队列或任何任务状态。全部在线玩家 MAY 发送 `停止`；处理顺序遵循既有 tick 边界聊天接收顺序。

#### Scenario: 停止终止跟随并继续队首

- **GIVEN** 一个伙伴正在持续跟随且 FIFO 中还有两条待执行指令
- **WHEN** 任意在线玩家发送 `@该伙伴 停止`
- **THEN** 服务端 MUST 广播 `TaskStopped` 事件，伙伴 MUST 停止移动，原队首任务 MUST 立即开始执行，队列内容 MUST 保持不变

#### Scenario: 非跟随任务的停止被同步拒绝

- **GIVEN** 一个伙伴当前任务是一次普通的 `go_to`
- **WHEN** 玩家发送 `@该伙伴 停止`
- **THEN** 服务端 MUST 只向发令者回复拒绝事件，当前任务 MUST 继续执行，队列 MUST 不变

#### Scenario: 空闲伙伴的停止被同步拒绝

- **GIVEN** 一个伙伴没有 Running 任务
- **WHEN** 玩家发送 `@该伙伴 停止`
- **THEN** 服务端 MUST 只向发令者回复拒绝事件，MUST NOT 创建任务或改变队列

#### Scenario: 非精确停止文本进入 FIFO

- **GIVEN** 玩家发送 `@伙伴名 停止移动` 或 `@伙伴名 stop`
- **WHEN** 服务端在 tick 边界处理
- **THEN** 该指令 MUST 按普通指令进入 FIFO 创建任务，MUST NOT 触发停止旁路
