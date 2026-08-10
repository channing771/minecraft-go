## Why

当前世界仅生成石头、泥土和草，已注册的沙子、砾石、黏土与雪块无法通过自然地形获得；旧世界也没有安全、可恢复的更新路径。现在以独立 change 同时补齐确定性自然材料生成和显式离线迁移，使新旧世界均能获得一致地形。

## What Changes

- 新增四种自然材料的确定性生成：沙子、砾石、黏土和雪块；单点查询与整区块生成一致，矿石仍只替换最终石头。
- 为 `mcgod` 新增必须带完整备份的离线迁移模式；该模式与服务器启动互斥、可失败续跑、完成后幂等，并只重算七种自然材料值。
- 保持非自然方块和所有非方块负载，明确强制迁移会重算符合条件的玩家建造自然材料。

## Capabilities

### New Capabilities

- `natural-material-generation`: 确定性自然材料地形生成与旧世界可恢复离线迁移。

### Modified Capabilities

无。

## Impact

- 受影响代码：`internal/worldgen`、`internal/storage` 与 `cmd/mcgod`。
- 所有权：`internal/storage` 仅为具体 `DiskStore` 增加 `ChunkKeys` 与 `Backup`；七种自然值纯迁移、runner、进度文件和故障注入位于 `cmd/mcgod`，由该命令同时依赖 `storage` 与 `worldgen`，不扩大 `storage.Store`。
- 存档与协议：继续使用区块 schema v8、协议 v15 和世界 metadata v2，不改变字节布局或语义版本；迁移状态单独保存。
- 并发与性能：生成保持无状态纯判断；迁移只在离线模式执行，获得既有世界锁且不监听 TCP，不进入权威 tick 或渲染热路径。
- 回退：正常关服后以迁移前的完整备份整体替换源世界；不提供逐块反向迁移。
