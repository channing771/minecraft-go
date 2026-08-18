# companion-planner Specification

## Purpose
定义服务端如何以有界、隔离且不阻塞权威 tick 的方式把玩家指令转换为受限 JSON `go_to` 计划，并规定模型调用与解码的全部失败语义。
## Requirements
### Requirement: Planner 输入是有界不可变快照

服务端 SHALL 只在权威 tick 边界为一次规划构造不可变观察快照，Planner worker MUST 只读取该副本。快照 MUST 包含：发令玩家的稳定 ID、位置、朝向与视线命中方块；伙伴 ID、位置、朝向、36 格背包与当前任务状态；伙伴周围水平 16 格、垂直 8 格范围的确定性环境摘要（高度信息与最多 256 个按坐标排序的暴露/特殊方块）；相关区块 revision；当前世界时间。M5C 起快照 MUST 额外包含有界的在线玩家集合（每名玩家的稳定 ID 与位置，至多八名），供 `follow` 目标校验。快照 MUST NOT 包含 API key、其他玩家聊天或世界存档路径；M5D 起存在 persona 与最近对话摘要，二者都 MUST NOT 进入规划输入。

#### Scenario: 快照字段有界且按坐标排序

- **GIVEN** 伙伴周围水平 16 格、垂直 8 格范围内存在超过 256 个暴露或特殊方块
- **WHEN** 服务端在 tick 边界构造观察快照
- **THEN** 快照 MUST 只保留 256 个按坐标确定性排序的方块条目，且构造 MUST 在不随范围方块总数无界增长的工作内完成

#### Scenario: 在线玩家集合有界且随快照一致

- **GIVEN** 八名在线玩家与一次进入 Planning 的任务
- **WHEN** 服务端构造观察快照
- **THEN** 快照 MUST 包含全部八名玩家的稳定 ID 与位置，玩家数 MUST NOT 超过八，且 worker 读取期间该集合 MUST NOT 变化

#### Scenario: 规划输入不泄漏密钥与无关内容

- **GIVEN** 一个配置了 API key 的服务端与一次进入 Planning 的任务
- **WHEN** worker 取得快照并发起规划请求
- **THEN** 请求正文 MUST 只包含快照数据与固定系统提示，MUST NOT 包含 key、其他玩家聊天文本或存档路径

#### Scenario: 人设与摘要绝不进入规划输入

- **GIVEN** 一个带非空 persona 与非空最近摘要的伙伴任务进入 Planning
- **WHEN** Planner worker 构造快照与请求
- **THEN** 快照与请求正文 MUST 都不包含 persona 或摘要文本，人设 MUST NOT 通过提示注入影响动作规划

### Requirement: Planner 调用有界且失败不重试

Planner SHALL 使用配置的 OpenAI-compatible endpoint 调用 `/chat/completions`，不引入第三方 SDK。单次请求 MUST 使用 30 秒超时与 context 取消，MUST NOT 自动重试；响应头与正文 MUST 有固定上限，正文 MUST 在分配前限制为 64 KiB。全服务端 MUST 最多四个模型请求并发，每个伙伴 MUST 最多一个在途 Planner 请求。结果 MUST 经有界 channel 只在权威 tick 边界应用并携带任务身份；任务已进入终态或已被替换时，结果 MUST 被丢弃。HTTP 失败、非 2xx 状态码、超时、超限或解码错误 MUST 令当前任务失败并向玩家公开规范失败原因；错误上下文 MUST NOT 包含 key 或响应正文原文。

#### Scenario: 超时失败且不重试

- **GIVEN** 一个假模型服务在 31 秒内不返回响应
- **WHEN** Planner 发起请求
- **THEN** 请求 MUST 在 30 秒超时后令当前任务失败，且服务端 MUST NOT 再次发起同一任务的规划请求

#### Scenario: 超大响应被拒绝且不泄漏正文

- **GIVEN** 一个假模型服务返回超过 64 KiB 的响应正文
- **WHEN** Planner 接收响应
- **THEN** 当前任务 MUST 失败，公开的失败原因 MUST 是稳定的服务器侧枚举，日志与事件 MUST NOT 包含响应正文原文

#### Scenario: 过时任务结果被丢弃

- **GIVEN** 一个任务的规划请求在途期间该任务已因超时进入终态
- **WHEN** worker 结果到达 tick 边界
- **THEN** 该结果 MUST 被丢弃，MUST NOT 产生任务状态变化或世界动作

#### Scenario: 慢模型不阻塞权威 tick

- **GIVEN** 一个挂起的规划请求与一个持续推进的权威模拟
- **WHEN** 模型服务持续不响应多个 tick
- **THEN** 权威 tick MUST 继续按既有节拍推进，玩家命令与世界模拟 MUST 不受影响

### Requirement: 计划是严格 JSON 且步骤限定交付全集

Planner 响应正文 MUST 用 `json.Decoder` 严格解码为单一 JSON object：MUST 拒绝未知字段、拒绝尾随数据，并在分配前检查 64 KiB 上限。计划 MUST 包含非空有界 `summary` 与非空 `steps` 数组；M5C 的 step kind MUST 限定为交付全集 `go_to`/`follow`/`mine`/`place`。`go_to(x,y,z)` 坐标 MUST 是有限整数值且在世界边界内。`follow(player_id)` 的目标 MUST 来自当前快照的在线玩家集合，且 `follow` MUST 是计划的最后一步。`mine(x,y,z)` 目标 MUST 在快照观察范围内、为具有单一 `BlockDrop` 且不是箱子或熔炉的普通方块。`place(x,y,z,block)` 的 block 名 MUST 来自固定注册表，且快照背包显示伙伴持有对应物品。空计划、未知 step kind、非法数值、不规范文本、`follow` 非最后一步、目标或物品不满足上述约束 MUST 令当前任务以非法计划失败；服务端 MUST NOT 重试、降级猜测或改写计划。玩家指令文本、世界方块名与模型输出 MUST 全部视为不可信数据；服务端 MUST NOT 执行模型返回的代码、URL、工具名或任意函数调用。

#### Scenario: 严格 JSON 拒绝未知字段与尾随数据

- **GIVEN** 三份响应正文分别包含未知顶层字段、JSON object 结束后的尾随数据与超过 64 KiB 的正文
- **WHEN** Planner 解码响应
- **THEN** 三者 MUST 全部令当前任务以非法计划失败，且不产生任何部分应用的计划状态

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

