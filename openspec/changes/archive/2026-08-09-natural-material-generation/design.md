## Context

本设计实现 proposal.md 与 `natural-material-generation` delta spec 的行为。当前 `worldgen.Generator` 已以世界种子和二维 Perlin 提供基础地形及矿石判断；`DiskStore` 持有磁盘世界、批量保存和世界锁；`mcgod` 是离线磁盘世界的唯一命令入口。

## Goals / Non-Goals

**Goals:**

- 用一套无状态纯判断同时驱动单点方块查询和整区块生成。
- 在不修改 schema、协议或 metadata 的前提下，为旧世界提供可恢复、可续跑的显式离线迁移。

**Non-Goals:**

- 不增加生物群系、海洋、树木、结构、运行时地形配置或第二个噪声对象。
- 不迁移苔藓圆石或全部十四种材料；不提供逐块反向迁移。
- 不扩大运行时 `storage.Store` 接口，不在 `mcgo` 或常驻服务器中暴露迁移入口。

## Decisions

### 共享自然材料判断

`worldgen.Generator` 以现有二维 Perlin 及固定坐标偏移作无状态判断；`BaseBlockAt` 与 `GenerateChunk` 共同调用它。基岩和空气优先返回；自然材料随后决定，矿石最后且仅在结果仍为石头时覆盖。

固定编译期常量如下，不增加配置：

```go
const (
	snowLine             int32 = 88
	sandLine             int32 = 62
	clayNoiseScale             = 1.0 / 96.0
	clayNoiseOffsetX     int32 = 417
	clayNoiseOffsetZ     int32 = -193
	clayNoiseThreshold         = 0.18
	gravelNoiseScale           = 1.0 / 72.0
	gravelNoiseOffsetX   int32 = -271
	gravelNoiseOffsetZ   int32 = 613
	gravelNoiseThreshold       = 0.22
	gravelMaxDepth       int32 = 10
)
```

高地表 `height >= 88` 为雪块；低地 `height <= 62` 的深度 `0..3` 为沙，深度 `2..3` 且黏土噪声超过阈值时为黏土；最终仍为石头且深度 `4..10`、砾石噪声超过阈值时为砾石。否决独立材料噪声器或运行时调参：现有 Perlin 加固定偏移已能保持可复现且不扩展配置面。

### 离线迁移的所有权和顺序

- `internal/worldgen` 只拥有纯自然材料计算，不读写存档或命令行。
- `internal/storage` 仅为具体 `DiskStore` 增加确定性只读 `ChunkKeys` 和完整 `Backup`；不依赖 `worldgen`，不拥有迁移或进度逻辑，也不扩张常驻服务器使用的 `storage.Store`。
- `cmd/mcgod/material_migration.go` 拥有七种自然值纯迁移、离线 runner 和 `material-migration-v1.json` 进度读写；由 `cmd/mcgod` 同时依赖 `storage` 与 `worldgen`，并通过具体 `DiskStore` 调用 `ChunkKeys`、`Backup`、既有世界锁、`SaveBatch` 与 `Sync`。对应故障注入位于 `cmd/mcgod/material_migration_test.go`。
- `cmd/mcgod` 的入口只解析互斥 flag 并调用 runner；迁移时不监听 TCP、不创建服务端。

迁移在获取世界锁后串行执行：首次写入前完成并同步外部完整备份，随后按维度、区块 X、区块 Z 稳定排序的键处理 Overworld。稳定排序后的扫描键每 `32` 个组成一个批次，最终不足 `32` 个键仍组成 partial batch；每批先对有变化的区块执行 `SaveBatch` 并 `Sync`，再以该批最后扫描键原子更新 `material-migration-v1.json` 的 `LastKey`。进度记录迁移版本、世界种子、备份路径和 `LastKey`；备份身份写入 `.mcgo-world-backup-v1.json`，绑定源世界绝对路径、种子和迁移版本。最终 partial batch 的进度成功落盘后才能原子标记完成。

区块迁移先克隆方块内容，只对石头、泥土、草、沙、砾石、黏土和雪块重算；非自然方块和所有非方块负载由迁移输入原样带回。仅有变化的区块以 `revision+1` 通过既有原子 `SaveBatch` 提交。

否决在线迁移：它会与权威 tick、客户端会话和持久化竞争。否决逐块反向记录：完整备份已提供更简单可靠的回退。

### 兼容、并发和验证

迁移状态是独立控制文件，世界数据继续使用 chunk schema v8、protocol v15 与 metadata v2；不引入版本升级或布局迁移。世界锁把命令与其他进程互斥；重 CPU、磁盘和同步 I/O 只发生在离线命令，不占用 tick 或渲染热路径。

测试冻结 seed `42` 的四种材料出现、相邻区域、负坐标和边界，逐格比较单点与整区块，并验证矿石不会覆盖最终自然材料。`internal/storage` 测试只覆盖 `DiskStore.ChunkKeys` 与 `DiskStore.Backup`；`cmd/mcgod` 测试覆盖锁冲突、七种值限定重算、负载保留、每 `32` 个扫描键成批、批量保存失败续跑、最终 partial batch 在进度或完成标记失败后重跑不重复 revision、完成幂等和 schema v8 往返。

## Risks / Trade-offs

- [符合七种值的玩家建筑会被强制重算] → 迁移必须显式调用、要求完整备份，并在文档与命令输出中提示；恢复以关服后整体还原备份完成。
- [备份、保存、进度或完成标记失败] → 首次写入前同步备份；每批按 `SaveBatch`、`Sync`、进度的顺序提交，纯迁移让进度落盘前失败的重跑不会再次增加已迁移区块 revision。
- [未来维度或未知数据] → 只处理 Overworld，其他维度和非方块负载保持原样；既有 future schema 拒绝语义不变。

## Migration Plan

1. 发布包含新生成规则和 `mcgod --migrate-materials --backup` 的版本；新区块立即采用新规则，已保存区块保持原内容直至显式迁移。
2. 操作者正常停止使用世界的进程后，提供外部备份路径执行迁移；命令持锁、验证/创建备份、按进度处理并在完成时落盘完成状态。
3. 若需回退，正常关服后用完整备份整体替换源世界；不改变 schema v8、protocol v15 或 metadata v2。
