# companion-task-queue Specification

## MODIFIED Requirements

### Requirement: 任务状态机与公开事件

任务 SHALL 按 `Queued → Planning → Validating → Running → Completed/Failed/TimedOut/Stopped` 推进，`Completed` 与 `TimedOut` MUST 只能从 `Running` 进入；`Failed` MAY 从 `Planning`、`Validating` 或 `Running` 进入——模型与校验失败必须能够终结尚未运行的任务；`Stopped` MUST 只能从 `Running` 的持续跟随任务经停止指令进入。任务进入 Running MUST 广播 `TaskStarted`，一个计划步骤完成 MUST 广播 `TaskProgress`，终态 MUST 分别广播 `TaskCompleted`、`TaskFailed`、`TaskTimedOut` 或 `TaskStopped`，且每次状态迁移 MUST 标记 AI 存档 dirty。`TaskFailed` MUST 携带稳定的服务器侧失败原因枚举（M5C 起 `TaskFailWorldChanged` 由跟随目标离线产生）。Task Runner MUST NOT 改写计划、插入计划外行为或为失败任务自动降级；任一终态 MUST 产生事件并推进 FIFO 下一项。

#### Scenario: 完整生命周期事件序列

- **GIVEN** 一条可用假模型成功规划的 `go_to` 指令且路径可达
- **WHEN** 任务从入队推进到完成
- **THEN** 玩家 MUST 依次观察到 Accepted、`TaskStarted` 与 `TaskCompleted`（或逐步骤 `TaskProgress`）事件，EventID 严格递增

#### Scenario: 失败任务推进 FIFO 下一项

- **GIVEN** 一个伙伴 FIFO 中第一条任务因模型不可用失败且队列还有下一条
- **WHEN** 失败终态事件产生
- **THEN** 服务端 MUST 公开 `TaskFailed` 及其原因，并 MUST 立即开始处理下一条指令

#### Scenario: 停止终态推进 FIFO 下一项

- **GIVEN** 一个伙伴当前持续跟随任务被停止且队列还有下一条
- **WHEN** `TaskStopped` 事件产生
- **THEN** 服务端 MUST 立即开始处理下一条指令，队列 MUST 不变

#### Scenario: Stopped 只能来自持续跟随

- **GIVEN** 一个伙伴当前任务是一次普通 `go_to`
- **WHEN** 停止指令到达
- **THEN** 任务 MUST NOT 转入 `Stopped`，停止 MUST 被同步拒绝

### Requirement: 普通任务超时使用持久化世界时间

每个普通任务 SHALL 在进入 Running 时记录 deadline 为当时的 `WorldTimeTicks` 加配置的执行分钟数；`taskTimeoutMinutes` MUST 在 `1..60` 范围内，缺省为 10。任务到达 deadline 时 MUST 转入 `TimedOut` 终态并广播事件。deadline MUST 随任务持久化；服务端停止运行期间世界时间不推进，安全关服期间 MUST NOT 消耗任务执行时长。持续跟随任务 MUST NOT 记录 deadline，也 MUST NOT 因执行时长转入 `TimedOut`——它只能由停止指令或目标离线终结。

#### Scenario: 到达 deadline 转入超时终态

- **GIVEN** 一个 Running 普通任务的世界时间 deadline 即将到达
- **WHEN** 权威 tick 推进过该 deadline
- **THEN** 任务 MUST 转入 `TimedOut`，广播对应事件，且移动 MUST 停止在当前位置

#### Scenario: 关服重启不消耗执行时长

- **GIVEN** 一个 Running 任务剩余一半执行时长时服务端安全关服
- **WHEN** 服务端重启并恢复该任务
- **THEN** 恢复后的 deadline 与关服前一致，任务 MUST 从已完成的步骤之后继续执行

#### Scenario: 持续跟随不设 deadline

- **GIVEN** 一个持续跟随任务
- **WHEN** 其运行时长超过任意配置的 `taskTimeoutMinutes`
- **THEN** 任务 MUST NOT 转入 `TimedOut`，持久化记录 MUST NOT 为其保存 deadline
