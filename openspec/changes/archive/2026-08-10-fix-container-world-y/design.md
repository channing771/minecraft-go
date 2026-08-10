## Context

见 `proposal.md`。chunk codec 已通过 `world.BlockPosFromChunkIndex` 得到完整世界坐标；但验证 Furnace 与 Chest 时把该坐标拆出的 Y 作为 section-local 值传入只接受世界 Y 的 `(*world.Chunk).BlockAt`，使非零 section 的合法容器被拒绝。

数据所有权仍属于区块，`storage` 只依赖 `world` 与 `core`，不新增依赖方向；codec 在调用方的持久化路径中串行执行，不改变跨 goroutine 的所有权或不可变边界。

## Goals / Non-Goals

**Goals:**

- 令 Furnace 与 Chest 的容器索引验证使用由索引还原出的完整世界 Y。
- 保持越界、重复或方块类型不匹配的记录拒绝为 `ErrCorrupt`。

**Non-Goals:**

- 不修改 schema v8、protocol v15、字节布局、容器数量或内容结构。
- 不扫描、修复或重写离线存档，也不改动权威模拟与网络路径。

## Decisions

### 复用索引还原的世界 Y

对两个已有容器校验点，继续复用 `world.BlockPosFromChunkIndex` 的结果，并将其完整世界 Y 直接传给 `Chunk.BlockAt`。该函数的既有契约正是世界 Y，改动限于两处错误的坐标传递。

被否决的替代方案：将世界 Y 再转换为 section-local Y，或在 codec 中新增坐标转换辅助函数。前者会重复当前错误；后者没有新的行为或复用价值。

### 保持失败语义和格式

沿用现有索引边界、唯一性及方块类型校验，所有失败仍由 codec 归一为 `ErrCorrupt`。编码字段和解码顺序不变，因此 schema v8 与 protocol v15 无需迁移。

被否决的替代方案：提升 schema 或增加兼容分支。该问题只发生在读取后校验，字节表示本身正确，版本变更会无故扩大兼容面。

## Risks / Trade-offs

- [遗漏其中一种容器的同类校验] → Furnace 与 Chest 各有非零 section 往返回归，以及索引 mutation 覆盖。
- [放宽损坏存档门禁] → 保留越界、重复和错误方块索引返回 `ErrCorrupt` 的测试。
- [兼容性回归] → 验证 schema v8 字节布局不变，并运行 storage 与全量门禁。

## Migration Plan

发布后直接读取既有 schema v8 存档：此前被误拒绝的合法非零 section 容器将正常读取；无需迁移或离线扫描。若出现回归，回退这两处校验的提交即可，已写入的存档不受格式影响。

验证顺序为 storage 回归和 mutation、`go test ./... -race`、`go vet ./...`、`gofmt -l .` 及 OpenSpec 严格校验。
