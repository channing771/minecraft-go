# authoritative-companion-entities Specification

## REMOVED Requirements

### Requirement: 伙伴是独立的服务端权威静态实体

被移动 actor 要求取代：M5B 起伙伴可经权威任务移动，"静态"约束不再成立；独立性、权威性与玩家语义隔离保持不变。

## ADDED Requirements

### Requirement: 伙伴是独立的服务端权威移动 actor

服务端 SHALL 为每个 active 定义持有一个独立伙伴实体。伙伴 MUST 保持独立于玩家会话语义：MUST NOT 进入玩家登录、心跳、生命、伤害、死亡、掉落或重生语义。伙伴身体状态 MUST 只在权威 tick 边界变化，并 MUST 按 `CompanionID` 字节序发布确定性状态。M5B 伙伴 MAY 经权威任务移动；移动 MUST 只由 Task Runner 提交的 `CompanionAction` 驱动，MUST NOT 由玩家输入、客户端指令或模型输出直接触发；聊天寻址本身 MUST NOT 改变身体。

#### Scenario: 乱序配置产生稳定状态

- **GIVEN** 服务端以非 ID 顺序配置多个伙伴并推进多个 tick
- **WHEN** 每个 tick 取得伙伴状态
- **THEN** 状态 MUST 按 `CompanionID` 严格升序，无任务伙伴的位置、朝向和背包 MUST 保持不变

#### Scenario: 寻址入队不移动身体

- **GIVEN** 一个已激活伙伴与一条成功寻址并创建任务的聊天指令
- **WHEN** 任务尚未进入 Running
- **THEN** 伙伴位置、朝向、背包与世界方块 MUST 不变，首个移动 MUST 只发生在任务进入 Running 之后

### Requirement: CompanionAction 按固定顺序进入权威 tick

`sim` SHALL 提供按 `CompanionID` 寻址的有界 `CompanionAction` inbox；action MUST NOT 携带 `SessionID` 或任何玩家会话身份。`Engine.Step` MUST 先按既有规则处理玩家命令，再按 `CompanionID` 字节序处理本 tick 的伙伴 action，最后统一推进所有 actor 的物理与世界变更，为同一 tick 建立固定顺序。伙伴物理积分 MUST 复用既有 Rust engine 物理出口，MUST NOT 新写 Go 生产积分实现；`go_to` 与每 tick 移动 MUST 只通过规范移动输入提交，实际位移由权威物理决定。

#### Scenario: 同 tick 多伙伴顺序固定

- **GIVEN** 四个伙伴在同一 tick 各有一个移动 action
- **WHEN** `Engine.Step` 推进该 tick
- **THEN** 伙伴 action MUST 全部按 `CompanionID` 字节序在玩家命令之后、统一物理积分之前处理，两个相同输入的连续 tick MUST 产生相同的可观察结果

#### Scenario: 伙伴与玩家共用同一物理出口

- **GIVEN** 一名玩家与一个伙伴以相同初始身体状态和相同移动输入推进
- **WHEN** 权威模拟执行一个 tick
- **THEN** 两者的位移与碰撞结果 MUST 与既有 Rust engine 物理出口一致，MUST NOT 出现第二套积分实现的行为差异

### Requirement: actorState 只容纳两类 actor 共有状态

`sim` SHALL 从 `playerState` 提取不导出的 `actorState`，M5B 只容纳运动、朝向与背包的共有状态；`playerState` MUST 保留生命、重生与玩家输入序号，`companionState` MUST 保留稳定 `CompanionID`。提取后既有玩家全部可观察行为 MUST 逐 tick 保持不变。

#### Scenario: 提取后玩家行为逐 tick 不变

- **GIVEN** 提取 `actorState` 前后各一次完整玩家移动、采掘、放置与背包操作序列
- **WHEN** 在相同世界与输入下重放
- **THEN** 两次序列的每个 tick 快照 MUST 完全一致

## MODIFIED Requirements

### Requirement: 每个伙伴只保持固定三乘三区块兴趣

每个 active 伙伴 SHALL 只保持脚下区块及其水平相邻区块，即固定 `3×3` 兴趣；伙伴移动跨入新区块时兴趣 MUST 随脚下区块滑动，新增区块 MUST 复用既有 acquire/generate/persistence 流程，离开的区块 MUST 按既有规则释放。待恢复或待出生伙伴的候选工作也 MUST 保持在同一有界范围内，并在所需碰撞区块就绪后才恢复或选择出生位置。

#### Scenario: 四个伙伴的兴趣保持有界

- **GIVEN** 四个伙伴分别位于互不重叠的脚下区块
- **WHEN** 服务端汇总伙伴区块兴趣
- **THEN** 每个伙伴 MUST 最多贡献九个区块，四个伙伴合计 MUST 最多贡献 36 个区块

#### Scenario: 跨区块移动时兴趣滑动

- **GIVEN** 一个伙伴正沿 `go_to` 路径跨入相邻区块
- **WHEN** 权威 tick 把伙伴身体推进到新区块
- **THEN** 其兴趣 MUST 滑动到以新脚下区块为中心的 `3×3`，新进入区块 MUST 经既有流程就绪，任一时刻该伙伴兴趣区块数 MUST 不超过九个

#### Scenario: 未就绪恢复不会猜测位置

- **GIVEN** 一个持久化身体的碰撞判定所需区块尚未全部就绪
- **WHEN** 服务端推进伙伴恢复
- **THEN** 伙伴 MUST 在客户端保持不可见且不得占用未验证位置，直到所需区块全部就绪后才恢复原位置或执行有界出生回退
