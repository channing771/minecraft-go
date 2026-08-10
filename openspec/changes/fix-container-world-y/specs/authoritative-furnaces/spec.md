## ADDED Requirements

### Requirement: 容器索引按世界高度校验
系统 SHALL 把持久化 Furnace 的紧凑方块索引还原为完整世界坐标，并以该世界 Y 验证对应 Furnace 方块。

#### Scenario: 非零垂直 section 的合法 Furnace 可往返
- **GIVEN** 一个活动 Furnace 位于 `core.MinY` 与 `core.MaxY` 之间的任意合法垂直 section
- **WHEN** 当前 chunk schema 编码并解码该区块
- **THEN** Furnace、方块索引和内容 MUST 无损往返，且 MUST NOT 因 section-local Y 被误判损坏

#### Scenario: 索引仍必须指向正确方块
- **WHEN** 活动 Furnace 索引越界、重复或没有指向对应 Furnace 方块
- **THEN** codec MUST 返回 `ErrCorrupt`，且 MUST NOT 接受或改写该记录
