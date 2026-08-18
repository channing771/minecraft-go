# companion-planner Specification

## MODIFIED Requirements

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
