## Why

M4E 已经证明"区块拥有共享状态、服务端权威推进、客户端只发意图"的容器模型可以在最多八名玩家下稳定工作，但熔炉的三个格子只服务熔炼，玩家采到的资源除了 36 格背包之外没有任何存放处。背包一满就只能丢弃，采集与合成的产出无法在世界里积累，存储链路是当前唯一缺失的一环。

M4K 用固定容量箱子补上这一环。箱子没有计时、没有配方、没有燃料，是最纯粹的共享容器，因此它同时是把熔炉验证过的容器机制收敛为一套共用实现的正确时机：第二个容器出现之前建立抽象是投机，出现之后继续复制原子性逻辑则是把最容易藏 bug 的代码抄了两遍。

## What Changes

- 新增稳定的箱子方块与物品 ID；箱子由 8 石头合成，可放置、可挖掘，使用程序化材质。
- `world.Chunk` 新增固定 16 个箱子槽，每个箱子持有 27 个物品格；箱子状态由所属区块拥有并随区块原子持久化，不属于任何玩家。
- 把熔炉已验证的查看生命周期收敛为一套共用实现：`core.ContainerRef` 增加 `Kind` 字段同时标识熔炉与箱子，`FurnaceRef` 成为其类型别名以避免调用点改动；每名玩家在任一时刻最多查看一个容器，打开新容器自动结束上一个查看关系。
- 箱子界面使用统一栏位 `0..62`（`0..35` 玩家物品、`36..62` 箱子 27 格）；跨容器整堆移动继续在两侧值副本上计算完整结果，只有全部最终槽位合法才同时提交。箱子格接受任何已注册物品，因此没有熔炉那样的物品类型约束。
- 放置箱子时预留最低索引可复用槽，槽位耗尽时原子拒绝第 17 个箱子且不消耗玩家物品；挖掘箱子前在掉落物副本上预演箱子本体与全部非空格，只有全部能完整放入时才清除方块、停用槽并提交掉落物。
- **BREAKING**：线上协议从 v11 升为 v12。`OpenFurnace`/`CloseFurnace`/`MoveFurnaceStack`/`FurnaceClosed` 更名为容器中性的 `OpenContainer`/`CloseContainer`/`MoveContainerStack`/`ContainerClosed` 并保持原有 packet ID，新增 `ChestState`；v11 及更早版本在进入 Play 前稳定拒绝，不提供协商或降级解码。
- **BREAKING**：区块存档从 schema v5 升为 v6，在熔炉之后追加固定 16 个箱子槽；v5 无损迁移为空箱子集合，v1–v4 沿既有链升级。玩家 schema 保持 v4。
- benchmark scenario 保持 v12：箱子由玩家放置，固定种子的 benchmark 世界中不存在箱子，地形与运动完全不变。但每个区块记录固定增加 2304 字节，因此本批必须在实现早期实测 RSS 并确认既有绝对门禁未被突破。
- 本批不实现大箱子合并、箱子命名、排序、快捷搬运、拆分堆、漏斗、比较器、潜行放置或任何自动化。

## Capabilities

### New Capabilities

- `authoritative-chests`: 定义区块拥有的固定箱子槽、27 格容量、查看生命周期、跨容器原子移动与原子破坏。

### Modified Capabilities

- `authoritative-furnaces`: 熔炉复用共用的容器引用与查看生命周期，打开箱子会结束熔炉查看关系。
- `authoritative-inventory`: 界面扩展到统一栏位 `0..62`，并把唯一支持的线上协议升级为 v12。

## Impact

- 受影响包：`internal/core`、`internal/world`、`internal/storage`、`internal/sim`、`internal/network`、`internal/server`、`internal/client`、`internal/render`、`internal/assets`、`cmd/mcgo` 与 `internal/archcheck`；`cmd/mcgod` 只复用共享服务端能力。
- 数据所有权：箱子状态由 `world.Chunk` 拥有并随区块原子持久化；`sim` 单写者是唯一修改者；客户端只发送意图并显示权威确认，不预测任何格子内容。
- 兼容性：协议 v12 拒绝 v11；区块 v6 读取 v1–v5 后无损迁移，只支持 v5 的旧程序把 v6 envelope 当作未来版本拒绝；玩家 schema v4 不变。回退必须停止服务并恢复首次写入 v6 前的完整世界备份。
- 并发与性能：每次跨容器移动最多检查 63 个栏位，箱子不参与每 tick 推进，因此不增加权威 tick 的循环工作量。区块固定负载增加 16 × 144 = 2304 字节，必须实测确认不突破既有 RSS 绝对门禁。
- 依赖与资源：复用现有 `Inventory`、掉落物批量提交、命令序列、私有发布、背包 renderer 与程序化材质；不新增第三方依赖、通用容器接口、方块实体 goroutine 或二进制美术资源。
- 验证：补充箱子槽不变式、跨容器原子性、schema v6 迁移/golden/fuzz、协议 v12 固定长度、Memory/TCP 多人等价、重启恢复、UI 命中与固定容量测试；全部自动验证保持 headless，不启动或聚焦游戏窗口。
