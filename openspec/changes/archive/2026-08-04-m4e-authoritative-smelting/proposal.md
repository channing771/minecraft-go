## Why

M4D 已经提供服务端权威的 36 格物品状态、固定石砖配方、协议 v6 和区块 schema v3，但世界里没有任何需要时间和燃料的资源转换：物品合法性仍与“能否放置”绑定，区块只保存方块和固定掉落物槽，没有燃料、矿石或方块实体。M4E 用一条最小但真实的资源链补上权威熔炼，并为后续方块实体确立“世界拥有共享状态、客户端只发意图”的边界。

## What Changes

- 新增稳定的煤矿、铁矿、熔炉、铁块方块 ID 与煤炭、粗铁、铁锭、熔炉、铁块物品 ID；物品合法性从 `ItemPlacement` 拆出独立的“已注册物品”判断，煤炭、粗铁、铁锭合法但不可放置。
- 世界生成按世界种子与三维坐标的稳定哈希只替换石头：煤矿仅 `Y < 96` 约 `1/2048`，铁矿仅 `Y < 48` 约 `1/4096`，铁矿优先；单点查询与整区块生成共用同一纯判断，已保存区块不批量改写。
- 固定合成表追加两条单输入单输出配方：8 石头合成 1 熔炉，9 铁锭合成 1 铁块；4 石头合成 4 石砖保持不变。
- `world.Chunk` 新增固定 32 个熔炉槽（generation、活动标志、方块索引、输入/燃料/输出三格、`ProgressTicks 0..199`、`BurnTicks 0..1600`），熔炉是区块拥有的共享状态，不属于任何玩家。
- 熔炼只在至少一名 Ready 玩家同维度、区块半径 2 内推进：燃料不足时消耗 1 煤设 1600，进度满 200 产出 1 铁锭；输入无效或输出无容量时进度与燃烧 tick 都暂停，因此一个煤恰好支持 8 个铁锭。
- 熔炉界面使用统一 `0..38` 栏位（`0..35` 玩家物品、36 输入、37 燃料、38 输出）；跨容器整堆移动在值副本上计算，只有两侧最终槽位都合法才同时提交，输出格只能作为来源。
- 放置熔炉与挖掘熔炉都是原子操作：槽位耗尽时拒绝第 33 个熔炉且不扣物品；破坏前在掉落物副本上预演本体与三格全部物品，容量不足时方块、熔炉与掉落物均不变。
- **BREAKING**：线上协议从 v6 升为 v7，新增固定长度 `OpenFurnace`、`MoveFurnaceStack`、`CloseFurnace`、`FurnaceState`、`FurnaceClosed`；v6 及更早版本在进入 Play 前稳定拒绝，不提供协商或降级解码。
- **BREAKING**：区块存档从 schema v3 升为 v4，在方块与掉落物之后追加固定 32 个熔炉槽；v3 无损迁移为空熔炉集合，v1、v2 沿既有链升级。玩家 schema 保持 v3。
- 性能报告场景从 v8 升为 v9，因为矿石改变了固定种子 benchmark 世界的材料分布；阈值、指标与 M2 基线保持不变。
- 本批不实现矿脉、工具等级、耐久、多燃料、多配方、经验、自动化、拖拽拆分堆或离线进度补算。

## Capabilities

### New Capabilities

- `deterministic-ore-generation`: 定义煤矿与铁矿的确定性分布、Y 上限、只替换石头以及单点与整区块生成的一致性。
- `authoritative-furnaces`: 定义区块拥有的固定熔炉槽、熔炼状态机、查看生命周期、跨容器原子移动与原子破坏。

### Modified Capabilities

- `authoritative-inventory`: 玩家物品状态可容纳新增的不可放置物品，界面扩展到统一 `0..38` 熔炉视图，并把唯一支持的线上协议升级为 v7。
- `bounded-benchmark-workload`: 矿石改变固定 benchmark 世界的材料分布，报告场景显式升级为 v9。

## Impact

- 受影响包：`internal/core`、`internal/worldgen`、`internal/assets`、`internal/world`、`internal/storage`、`internal/sim`、`internal/network`、`internal/server`、`internal/client`、`internal/render`、`cmd/mcgo`、`cmd/perfcheck` 与 `internal/archcheck`；`cmd/mcgod` 只复用共享服务端能力。
- 数据所有权：熔炉状态由 `world.Chunk` 拥有并随区块原子持久化；`sim` 单写者是唯一推进者；客户端只发送意图并显示权威确认，不预测物品、燃料或进度。
- 兼容性：协议 v7 拒绝 v6；区块 v4 读取 v1-v3 后无损迁移，只支持 v3 的旧程序把 v4 envelope 当作未来版本拒绝；玩家 schema v3 布局不变但旧程序遇到未知物品必须拒绝且不得覆盖。回退必须停止服务并恢复首次写入 v4 前的完整世界备份。
- 并发与性能：最坏活动检查量为 8 玩家 × 25 区块 × 32 槽 = 6400 槽/tick，使用固定数组与复用 scratch，不在熔炉热路径分配；同一 tick 内同一区块的多个熔炉变化只提升一次 revision。
- 依赖与资源：复用现有 `Inventory`、掉落物槽、命令序列、私有发布、背包 renderer 与程序化材质；不新增第三方依赖、配方 DSL、方块实体 goroutine、通用容器接口或二进制美术资源。
- 验证：补充确定性生成、熔炉不变式、跨容器原子性、schema v4 迁移/golden/fuzz、协议 v7 固定长度、Memory/TCP 多人等价、重启恢复、UI 命中与固定分配测试；全部自动验证保持 headless，不启动或聚焦游戏窗口。
