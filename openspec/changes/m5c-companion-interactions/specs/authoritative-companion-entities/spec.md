# authoritative-companion-entities Specification

## MODIFIED Requirements

### Requirement: CompanionAction 按固定顺序进入权威 tick

`sim` SHALL 提供按 `CompanionID` 寻址的有界 `CompanionAction` inbox；action MUST NOT 携带 `SessionID` 或任何玩家会话身份。M5C 起 action MUST 是移动、采掘按住、采掘释放或放置四种载荷之一的判别值：移动载荷复用既有规范移动输入；采掘载荷携带目标 `BlockPos`（按住语义与玩家采掘一致）；放置载荷携带目标 `BlockPos` 与方块（经既有玩家放置规则校验后写入）。`Engine.Step` MUST 先按既有规则处理玩家命令，再按 `CompanionID` 字节序处理本 tick 的伙伴 action，最后统一推进所有 actor 的物理、采掘与世界变更，为同一 tick 建立固定顺序。伙伴物理积分 MUST 复用既有 Rust engine 物理出口，MUST NOT 新写 Go 生产积分实现；伙伴采掘 MUST 复用既有 `miningRule` 计时与工具判定，放置 MUST 复用既有玩家放置校验路径，MUST NOT 出现第二套规则实现。

#### Scenario: 同 tick 多伙伴顺序固定

- **GIVEN** 四个伙伴在同一 tick 各有一个 action（任意载荷）
- **WHEN** `Engine.Step` 推进该 tick
- **THEN** 伙伴 action MUST 全部按 `CompanionID` 字节序在玩家命令之后、统一物理积分之前处理，两个相同输入的连续 tick MUST 产生相同的可观察结果

#### Scenario: 伙伴与玩家共用同一物理出口

- **GIVEN** 一名玩家与一个伙伴以相同初始身体状态和相同移动输入推进
- **WHEN** 权威模拟执行一个 tick
- **THEN** 两者的位移与碰撞结果 MUST 与既有 Rust engine 物理出口一致，MUST NOT 出现第二套积分实现的行为差异

#### Scenario: 伙伴采掘与玩家同一计时规则

- **GIVEN** 一名玩家与一个伙伴以相同工具对相同方块持续采掘
- **WHEN** 权威模拟推进相同 tick 数
- **THEN** 两者的完成时机、耐久扣减与产物判定 MUST 完全一致，差别仅在产物去向（玩家为掉落物、伙伴直入背包）

### Requirement: actorState 只容纳两类 actor 共有状态

`sim` SHALL 维护不导出的 `actorState` 共有状态；M5B 容纳运动、朝向与背包，M5C 起 MUST 追加上采掘共有状态（目标、方块、持握工具、进度、可收获标志）与交互距离/Ready 区块校验的共享使用；`playerState` MUST 保留生命、重生与玩家输入序号，`companionState` MUST 保留稳定 `CompanionID`。每次扩展后既有玩家全部可观察行为 MUST 逐 tick 保持不变。

#### Scenario: 提取后玩家行为逐 tick 不变

- **GIVEN** 采掘状态共享扩展前后各一次完整玩家移动、采掘、放置与背包操作序列
- **WHEN** 在相同世界与输入下重放
- **THEN** 两次序列的每个 tick 快照 MUST 完全一致

#### Scenario: 采掘状态为两类 actor 同构字段

- **GIVEN** 一名玩家与一个伙伴各自采掘不同目标
- **WHEN** 读取两者的采掘进度状态
- **THEN** 二者 MUST 使用同一结构体类型与同一进度语义，MUST NOT 存在两套进度定义
