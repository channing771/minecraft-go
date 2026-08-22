## MODIFIED Requirements

### Requirement: 未受伤时生命值自动回复
系统 SHALL 在最后一次受伤后连续 100 tick 未再受伤、**且玩家饥饿值不低于 18** 时开始回复生命值，每 40 tick 回复 1 点，直到满值;每回复 1 点 MUST 累积固定的回血疲劳量(见 `authoritative-hunger`)。饥饿值低于 18 时 MUST NOT 回复。任何伤害 MUST 把计时清零并中断回复。满血时 MUST NOT 计时或回复。

#### Scenario: 延迟期内不回复
- **GIVEN** 某玩家刚受到伤害且生命值低于满值
- **WHEN** 系统推进 99 tick 且玩家未再受伤
- **THEN** 生命值 MUST 保持不变

#### Scenario: 延迟满足后按固定速率回复
- **GIVEN** 某玩家生命值为 10 且已连续 100 tick 未受伤
- **WHEN** 系统再推进 40 tick
- **THEN** 生命值 MUST 变为 11

#### Scenario: 受伤打断回复
- **GIVEN** 某玩家已连续 100 tick 未受伤并正在回复
- **WHEN** 玩家再次受到伤害
- **THEN** 回复 MUST 立即停止，且必须重新连续 100 tick 未受伤才能再次开始

#### Scenario: 满血不回复
- **GIVEN** 某玩家生命值为 20
- **WHEN** 系统推进任意 tick
- **THEN** 生命值 MUST 保持 20 且不产生额外状态发布

#### Scenario: 饥饿值不足时不回复
- **GIVEN** 某玩家生命值为 10、饥饿值为 17 且已连续 100 tick 未受伤
- **WHEN** 系统再推进 40 tick
- **THEN** 生命值 MUST 保持 10

#### Scenario: 饥饿伤害经同一入口且止于 1
- **GIVEN** 某玩家饥饿值为 0、生命值为 2
- **WHEN** 系统推进两个饥饿伤害间隔
- **THEN** 生命值 MUST 为 1 且 MUST NOT 继续下降,每次扣血 MUST 重置回血计时
