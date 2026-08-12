## MODIFIED Requirements

### Requirement: 固定配方具有稳定语义
系统 SHALL 定义八条稳定固定配方：recipe ID `1` 每次消耗 4 个石头并产出 4 个石砖，ID `2` 消耗 8 个石头并产出 1 个熔炉，ID `3` 消耗 9 个铁锭并产出 1 个铁块，ID `4` 消耗 3 个石头并产出 1 个石镐，ID `5` 消耗 3 个铁锭并产出 1 个铁镐，ID `6` 消耗 8 个石头并产出 1 个箱子，ID `7` 消耗 1 个橡木原木并产出 4 个橡木木板，ID `8` 消耗 4 个玻璃并产出 4 个发光方块；recipe ID、原料、数量和产物 MUST 由服务端定义，客户端不得声明或覆盖这些值。新增配方 MUST 继续遵守既有完整物品状态的原子失败语义，且相同初始状态与命令序列经 Memory 和 TCP MUST 得到相同结果。

#### Scenario: 石砖配方可查询
- **WHEN** 系统读取 recipe ID `1`
- **THEN** 该配方稳定表示 4 个石头转换为 4 个石砖

#### Scenario: 熔炉配方可查询
- **WHEN** 系统读取 recipe ID `2`
- **THEN** 该配方稳定表示 8 个石头转换为 1 个熔炉

#### Scenario: 铁块配方可查询
- **WHEN** 系统读取 recipe ID `3`
- **THEN** 该配方稳定表示 9 个铁锭转换为 1 个铁块

#### Scenario: 石镐配方可查询
- **WHEN** 系统读取 recipe ID `4`
- **THEN** 该配方稳定表示 3 个石头转换为 1 个石镐

#### Scenario: 铁镐配方可查询
- **WHEN** 系统读取 recipe ID `5`
- **THEN** 该配方稳定表示 3 个铁锭转换为 1 个铁镐

#### Scenario: 箱子配方可查询
- **WHEN** 系统读取 recipe ID `6`
- **THEN** 该配方稳定表示 8 个石头转换为 1 个箱子

#### Scenario: 橡木木板配方可查询
- **WHEN** 系统读取 recipe ID `7`
- **THEN** 该配方稳定表示 1 个橡木原木转换为 4 个橡木木板

#### Scenario: 发光方块配方可查询
- **WHEN** 系统读取 recipe ID `8`
- **THEN** 该配方稳定表示 4 个玻璃转换为 4 个发光方块

#### Scenario: 未知配方被拒绝
- **GIVEN** 玩家具有任意有效完整物品状态
- **WHEN** 玩家请求 recipe ID `0` 或大于 `8` 的值
- **THEN** 系统 MUST 稳定拒绝且完整物品状态保持不变

#### Scenario: 发光方块配方失败保持原子
- **GIVEN** 玩家玻璃不足 4 个，或扣料后仍没有可接收全部 4 个发光方块的容量
- **WHEN** 玩家请求 recipe ID `8`
- **THEN** 服务端 MUST 拒绝请求，完整物品状态 MUST 保持不变且不得产生部分发光方块

#### Scenario: Memory 与 TCP 的发光方块合成一致
- **GIVEN** 相同初始完整物品状态和包含 recipe ID `8` 的相同合成命令序列
- **WHEN** 场景分别通过 Memory 与 TCP 执行
- **THEN** 最终完整物品状态与拒绝结果 MUST 一致
