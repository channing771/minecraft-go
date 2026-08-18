# companion-task-queue Specification

## MODIFIED Requirements

### Requirement: 编排只在 tick 边界且不阻塞

Companion Manager SHALL 由服务端在权威 tick 边界驱动：接收聊天、入队或同步拒绝、构造快照、应用 Planner 与寻路 worker 结果、推进 Task Runner 并发布事件；M5D 起还 SHALL 评估台词触发节点、应用 Dialogue worker 结果（台词文本与新摘要）。Planner、Dialogue、寻路与存档 I/O MUST 在 worker goroutine 上处理不可变值，结果经有界 channel 回到 tick 边界应用，MUST NOT 阻塞权威 tick 或渲染热路径；Dialogue 请求的发起、跳过与结果应用 MUST NOT 改变任务状态机、FIFO 或任何世界事实。安全关服 SHALL 先停止接受新 `ChatCommand`、取消在途模型请求（含 Planner 与 Dialogue）、冻结队列与 actor 状态，完成最终 AI 保存，再关闭世界存储。

#### Scenario: 挂起的模型请求不阻塞权威模拟

- **GIVEN** 四个伙伴各有任务处于 Planning 且模型服务全部挂起
- **WHEN** 权威模拟连续推进多个 tick
- **THEN** 每个 tick MUST 按既有节拍完成，玩家命令处理与世界模拟 MUST 不因模型请求延迟而变化

#### Scenario: 台词结果应用不触碰任务事实

- **GIVEN** 一个任务的步骤完成台词结果到达 tick 边界
- **WHEN** Companion Manager 应用该结果
- **THEN** 系统 MUST 只广播 `CompanionSpeech` 与（终态时）更新摘要并标记 dirty，任务状态、步骤索引与 FIFO MUST 不因台词结果而变化

#### Scenario: 关服顺序保证状态一致

- **GIVEN** 一个任务正在 Planning、一条台词请求在途，且 FIFO 中还有两条待执行指令
- **WHEN** 服务端执行安全关服
- **THEN** 可观察顺序 MUST 是停止接受聊天、取消全部模型请求、冻结队列与 actor、最终 AI 保存、世界存储同步与关闭；保存失败 MUST 保持可重试且不得继续关闭世界存储
