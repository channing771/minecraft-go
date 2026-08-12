## Why

区块 codec 当前会把由紧凑索引还原出的容器方块位置的世界 Y 再误当作 section-local Y 使用，因而会错误拒绝位于非零垂直 section 的合法 Furnace 与 Chest。需要仅修正 codec 的方块校验，使已有存档能正常读取和往返。

## What Changes

- 编解码容器记录时，以紧凑方块索引还原出的完整世界坐标验证 Furnace 与 Chest 所在方块。
- 保持越界、重复索引或索引未指向对应容器方块的记录为损坏并拒绝。
- 增加 Furnace 与 Chest 在任意合法非零垂直 section 中的存档往返及损坏输入回归覆盖。

### 非目标

- 不改变容器、区块或世界存档格式，不迁移既有存档。
- 不改变 schema v8、protocol v15 或任何字节布局。
- 不增加离线扫描、修复或重写存档的工作。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `authoritative-furnaces`: 熔炉持久化索引必须按完整世界高度校验。
- `authoritative-chests`: 箱子持久化索引必须按完整世界高度校验。

## Impact

- 受影响实现为 `internal/storage/chunk_codec.go` 及其回归与 mutation 测试；不新增包或依赖。
- 兼容性：chunk schema v8、protocol v15 与字节布局保持不变，已有合法存档无需迁移；既有损坏记录仍返回 `ErrCorrupt`。
- 并发与性能：只修改 codec 现有校验中的坐标传递，不改变数据所有权、并发边界或热路径；不增加离线扫描。
