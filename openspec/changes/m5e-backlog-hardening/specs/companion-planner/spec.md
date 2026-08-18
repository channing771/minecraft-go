# companion-planner delta

## MODIFIED Requirements

### Requirement: 计划是严格 JSON 且步骤限定交付全集

Planner 响应正文 MUST 用 `json.Decoder` 严格解码为单一 JSON object：MUST 拒绝未知字段、拒绝尾随数据，并在分配前检查 64 KiB 上限。计划 MUST 包含非空有界 `summary` 与非空 `steps` 数组；M5C 的 step kind MUST 限定为交付全集 `go_to`/`follow`/`mine`/`place`。每种 step kind 的字段排他 MUST 严格成立：专属外字段无论携带显式 JSON null 还是非法值，MUST 一律令当前任务以非法计划失败——显式 null 与字段缺席 MUST NOT 被折叠为同一语义。`go_to(x,y,z)` 坐标 MUST 是有限整数值且在世界边界内。`follow(player_id)` 的目标 MUST 来自当前快照的在线玩家集合，且 `follow` MUST 是计划的最后一步。`mine(x,y,z)` 目标 MUST 在快照观察范围内、为具有单一 `BlockDrop` 且不是箱子或熔炉的普通方块。`place(x,y,z,block)` 的 block 名 MUST 来自固定注册表，且快照背包显示伙伴持有对应物品。空计划、未知 step kind、非法数值、不规范文本、`follow` 非最后一步、目标或物品不满足上述约束 MUST 令当前任务以非法计划失败；服务端 MUST NOT 重试、降级猜测或改写计划。玩家指令文本、世界方块名与模型输出 MUST 全部视为不可信数据；服务端 MUST NOT 执行模型返回的代码、URL、工具名或任意函数调用。

#### Scenario: 严格 JSON 拒绝未知字段与尾随数据

- **GIVEN** 三份响应正文分别包含未知顶层字段、JSON object 结束后的尾随数据与超过 64 KiB 的正文
- **WHEN** Planner 解码响应
- **THEN** 三者 MUST 全部令当前任务以非法计划失败，且不产生任何部分应用的计划状态

#### Scenario: 显式 null 视为字段出现

- **GIVEN** 模型返回的步骤在专属外字段携带显式 JSON null（如 `follow` 携带 `"x":null`、`go_to` 携带 `"block":null` 或 `"player_id":null`）
- **WHEN** Planner 解码并验证计划
- **THEN** 当前任务 MUST 以非法计划失败，拒绝语义与该字段携带非 null 非法值时完全一致，MUST NOT 进入寻路或任何模拟动作

#### Scenario: 未交付步骤类型令任务失败

- **GIVEN** 模型返回的计划 steps 中包含 `swim`、`attack` 等交付全集之外的 kind
- **WHEN** Planner 解码并验证计划
- **THEN** 当前任务 MUST 以非法计划失败，服务端 MUST NOT 把该步骤翻译成任何模拟动作

#### Scenario: go_to 坐标必须在世界边界内

- **GIVEN** 模型返回的 `go_to` 坐标之一超出世界边界或不是有限整数
- **WHEN** Planner 验证计划
- **THEN** 当前任务 MUST 以非法计划失败，且 MUST NOT 触发寻路或移动

#### Scenario: follow 只能作为最后一步

- **GIVEN** 模型返回的计划在 `follow` 之后还有任何步骤
- **WHEN** Planner 验证计划
- **THEN** 当前任务 MUST 以非法计划失败，MUST NOT 开始执行任何步骤

#### Scenario: follow 目标必须来自快照在线玩家

- **GIVEN** 模型返回的 `follow` 目标 `player_id` 不在当前快照的在线玩家集合中
- **WHEN** Planner 验证计划
- **THEN** 当前任务 MUST 以非法计划失败

#### Scenario: mine 目标须为快照内可采掘普通方块

- **GIVEN** 模型返回的 `mine` 目标不在快照观察范围内，或目标是箱子、熔炉或多掉落方块
- **WHEN** Planner 验证计划
- **THEN** 当前任务 MUST 以非法计划失败，MUST NOT 破坏任何方块

#### Scenario: place 方块须来自注册表且伙伴持有

- **GIVEN** 模型返回的 `place` block 名不在固定注册表中，或快照背包显示伙伴未持有对应物品
- **WHEN** Planner 验证计划
- **THEN** 当前任务 MUST 以非法计划失败，MUST NOT 扣除任何物品
