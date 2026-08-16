# companion-pathfinding Specification

## Purpose

为 `go_to` 提供确定性、有界且不挖改世界的网格寻路，使伙伴移动可重放、可预算且在路径失效时可控地失败。

## ADDED Requirements

### Requirement: 寻路确定性且有界

寻路 SHALL 在 worker goroutine 上对不可变区块快照执行整数代价搜索：窗口为伙伴附近水平 16 格、垂直 4 格；单次搜索 MUST 最多考察 4096 个节点；邻居展开顺序 MUST 固定；相同快照、相同起点与目标 MUST 产生相同路径。搜索 MAY 在有支撑的站立格之间移动、跨越一格水平间隙并跳上一格；MUST NOT 游泳、攀爬、搭桥，MUST NOT 为开辟通路而修改任何方块。窗口内不存在可行路径或节点预算耗尽时，本次搜索 MUST 返回失败。

#### Scenario: 相同输入产生可重放路径

- **GIVEN** 同一份不可变快照、相同的伙伴位置与目标
- **WHEN** 寻路执行两次
- **THEN** 两次 MUST 返回完全相同的路径点序列或相同的失败结果

#### Scenario: 节点预算耗尽返回失败

- **GIVEN** 一个窗口内可达路径需要考察超过 4096 个节点
- **WHEN** 寻路执行
- **THEN** 搜索 MUST 在 4096 节点处停止并返回失败，MUST NOT 无界消耗内存或时间

#### Scenario: 不为通路修改方块

- **GIVEN** 目标被单格方块完全阻挡且没有绕行路径
- **WHEN** 寻路执行
- **THEN** 搜索 MUST 返回失败，且权威世界 MUST NOT 发生任何方块变化

### Requirement: 路径使用前重验且三次失败终止

每次寻路结果 MUST 携带相关区块 revision。Task Runner 在提交每个路径点的移动前 MUST 按当前权威状态重新验证该路径点仍然合法；验证失败时 MUST 按固定冷却重新规划。同一任务内连续三次无法得到可用路径 MUST 令当前任务以路径不可达失败，MUST NOT 无限重算。

#### Scenario: revision 失效触发重算

- **GIVEN** 一条已产出的路径及其 revision，且玩家随后修改了路径经过的区块
- **WHEN** Task Runner 准备提交下一个路径点
- **THEN** 该路径点 MUST 被拒绝使用，服务端 MUST 在固定冷却后基于新快照重新规划

#### Scenario: 连续三次失败公开不可达

- **GIVEN** 目标周围被持续变化的世界状态阻断且三次重规划都失败
- **WHEN** 第三次重算失败
- **THEN** 当前任务 MUST 以路径不可达原因失败并广播事件，MUST NOT 发起第四次重算
