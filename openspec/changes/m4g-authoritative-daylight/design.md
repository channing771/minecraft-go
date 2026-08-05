## Context

动机见 `proposal.md`。本 change 以已完成并归档的 M4F 为硬前置：协议 v8、metadata v1、玩家 schema v3、区块 schema v4、scenario v10 与对应 M5 基线必须先成为稳定事实。当前地形网格已经为每个 quad 保留 `Light` 八位，但 mesher 固定写入 `0xF0`，terrain shader 只读取高四位天空光；世界区块、网络快照和存档均没有光照负载。服务端 `Engine.Step` 是权威单写者，区块保存已有固定 worker、队列、退避和可重试关服屏障，客户端 mesher 已通过 3×3 chunk revision 印章隔离并发快照。

本 change 受三份 delta specs 约束。实现必须保持 Memory/TCP 同逻辑、客户端镜像只读、权威 tick 无 I/O、自动验证无窗口和既有性能门禁不放宽。

## Goals / Non-Goals

**Goals:**

- 用现有权威 tick、玩家状态和保存 worker 提供持久化、多人一致的世界时间。
- 用每列最高遮挡这一固定派生值替换写死天空光，使屋顶放置/移除能增量改变网格光照。
- 只更新固定大小 uniform 即可推进昼夜，不因时间流逝重新网格化。
- 保持玩家/区块 schema 与网络 chunk payload 不变，明确 metadata v2、协议 v9 和 scenario v11 的迁移边界。

**Non-Goals:**

- 不实现横向洪泛、衰减、方块光、光源方块或通用光照 worker。
- 不提前加入透明方块、天气、阴影、天体或基于光照的怪物规则。
- 不把高度表或天空光复制进协议、区块存档或独立客户端状态树。
- 不为世界时间增加独立消息、goroutine、队列或配置系统。

## Decisions

### 1. 绝对世界时间归 simulation owner

`sim.Engine` 增加一个 `uint64` 绝对世界时间，由构造参数从 metadata 初始化，并在每次 `Step` 完成时恰好加一。`TickResult`/`PlayerUpdate` 携带本 tick 的最终时间，`internal/server` 只映射到每名玩家已有的 `PlayerState`。客户端继续用 `ServerTick` 丢弃旧状态，并保存最后确认的绝对世界时间；显示时才对 `24000` 取模。

不使用墙钟：暂停、机器时区和调度抖动会让重放及多人分叉。不使用独立 `WorldTime` packet：所属玩家本来每 tick 都收到 `PlayerState`，新增消息只会增加 outbox 生命周期。客户端不做独立时钟推进；20 Hz 下一个完整昼夜仍有 24000 个状态点，不会形成可感知跳变。

### 2. metadata v2 追加绝对时间并复用保存 worker

`storage.Metadata` 追加 `WorldTimeTicks`，metadata v2 payload 在 v1 的种子、维度和出生锚点后追加一个 `uint64`。解码器同时接受 v1/v2：v1 读取后在内存中规范为 v2、时间为零；未来版本继续返回 `ErrFutureVersion`。`Store` 增加原子保存 metadata 的方法，Memory/Disk 共用相同值语义；Disk 复用现有临时文件、fsync、rename、目录 fsync 和 CRC 路径。

现有 `saveJob` 增加固定 kind，使相同 worker/channel 能处理 chunk batch 或一份 metadata 快照。Server 只维护“最新待保存时间、最多一个 in-flight、失败次数与下一重试 tick”，完成旧快照时若权威时间已前进则继续保持 dirty。自动保存边界投递一次最新值；队列满时保留 dirty 而不阻塞。失败使用现有 `RetryBaseTicks/RetryMaxTicks`，但不塞进按 region 分组的区块 retry map。`flushFrozen` 把 metadata 与区块、玩家保存一起纳入可重试关服屏障。

否决每 tick 写 metadata，因为 20 Hz 磁盘 I/O 没有必要且会破坏 tick；否决另建 `world-time` 文件，因为它制造第二份世界级提交点和额外恢复组合。

### 3. 区块只保存固定最高遮挡表

`world.Chunk` 增加 `[256]int16` 最高非空气 Y，空列为 `core.MinY-1`。`NewChunk` 初始化空值；按升序生成时 `SetBlock` 只做 O(1) 抬高；移除列顶时自上向下最多扫描 384 格；`Clone` 复制固定数组。由 snapshot/存档直接装入 section 后调用一次重建，避免把派生值写入任何格式。

当前所有非空气方块都不透明，所以判定直接使用 `BlockID != AirID`，不引入方块属性注册表。真正加入透明方块时再把遮光规则提升为共享 block property；现在提前做注册表只会扩大 M4G。

否决两个 4-bit 光照数组和洪泛传播：当前没有方块光源或透明方块，存储、跨区块收敛和迁移成本不能带来本批可见收益。

### 4. mesher 读取不可变高度快照

`world.Neighborhood` 在既有 3×3×3 section 引用之外，保存九个 chunk 的固定高度表副本与存在标记。`NeighborhoodAt` 和客户端 `cloneNeighborhood` 从同一 `Chunk` 查询构造；mesher worker 只读该副本，不回读可变 Mirror。九个既有 `ChunkStamp` 继续证明方块和高度输入的 revision，一方变化即丢弃旧结果。

`Neighborhood` 提供局部坐标 `-1..16` 的直射天空光查询。目标 chunk 缺失时返回零；存在时把局部坐标映射到对应列，并按采样 Y 是否严格高于最高遮挡返回 `15/0`。`MeshSection` 对可见面的相邻空气采样并写入 `Light` 高四位，低四位保持零；现有 `maskCell.light` 自动阻止不同光照 quad 被错误合并。

否决 worker 直接访问 Mirror：这会跨 goroutine 共享可变 chunk，并使 revision 印章失去意义。否决在每个面上向世界顶部扫描：最坏网格热路径会从 O(方块数) 变为 O(方块数×世界高度)。

### 5. 遮挡变化按高度跨度标脏

客户端应用每个权威 block change 前后读取该列最高遮挡。若未变化，只走现有方块/AO `±1` dirty；若变化，额外遍历新旧高度跨度覆盖的 section Y，并加入采样该列天空光所需的水平相邻 section。一个 block 列及其 `±1` 方块邻域最多跨四个 chunk，24 个 section 高度下最多产生 96 个唯一 key；现有 dirty map 会合并重复项，ready heap、job channel 和每帧调度/回收上限继续限流。

邻区 snapshot 到达或 forget 时沿用现有整 chunk 水平邻域标脏。世界时间变化不修改 chunk revision、不触发 mesher，也不重新上传 quad。

否决每次顶高变化重做整个 3×3×24 chunk 邻域或整个视距；精确垂直跨度已经覆盖全部天空光变化，额外工作只会放大快速建造时的积压。

### 6. 一个纯函数驱动全部世界空间明暗

`internal/render` 提供无状态昼夜计算：

```text
p = WorldTimeTicks mod 24000
sun = max(0, sin(2πp/24000))
daylight = 0.15 + 0.85*sun
terrain = 0.08 + (sky/15)*(daylight-0.08)
```

terrain camera uniform 在现有矩阵和位置后追加 `daylight`；shader 从 quad 读取 `sky` 并计算 `terrain`，再乘原有朝向与 AO。CPU 用 `sun` 在固定夜空色与既有日间 clear color 之间插值。Avatar 与 item-drop uniform 追加同一个 `daylight`；name-tag 与 hotbar pipeline 不改。`cmd/mcgo` 只把最后确认时间计算成同一份固定值传给三个世界空间 renderer。

不新增天空 pipeline、纹理或 mesh 更新。也不按实体位置采样直射天空光：本批只要求玩家和掉落物与全局昼夜一致，实体遮蔽留到存在通用光场时处理。

### 7. 协议 v9 只扩展既有玩家状态

`network.ProtocolVersion` 升为 9，在 `PlayerState` 固定 payload 末尾追加 `uint64 WorldTimeTicks`；packet ID、其他消息与 chunk snapshot 保持不变。codec golden、最大 payload、registry、fuzz、Memory/TCP 登录与旧版本拒绝测试同步更新。v8 在 Handshake 阶段拒绝，不提供兼容 decoder。

玩家 schema v3、区块 schema v4 不变；metadata v2 是唯一磁盘格式变化。旧程序能在写入前读取 v1，但遇到 v2 会按未来版本拒绝，不得覆盖。

### 8. scenario v11 只标记真实 workload 变化

协议长度、每区块 512 字节派生状态、mesher job 输入和三个 renderer uniform 都改变 benchmark 语义，因此 producer 标记 v11，比较器只保留唯一显式 `10:11` 迁移。历史 v6-v10 继续按各自规则读取；`9:10` 和更早迁移参数退役。2560x1440、still/flying、RSS、tick、2048 GPU 样本、Memory/TCP parity、绝对门禁和 20% 阈值全部不变。

M4F v10 基线和归档未完成时不得开始 M4G 正式链。实现、测试、文档和计划提交后冻结候选 HEAD，经用户一次性授权，以全新路径只执行一次 Memory；通过后只执行一次 TCP。两者通过后才提升 M5 Memory 精确字节，M2 文件保持不变。

### 9. 受影响文件与依赖方向

- `internal/world/chunk.go`、`neighborhood.go`：固定高度派生状态与不可变邻域查询。
- `internal/mesh/greedy.go`：用直射天空光替换固定值。
- `internal/client/mirror.go`、`mesher.go`：高度跨度 dirty、快照与 revision 失效。
- `internal/sim/engine.go`、`player.go`：权威时间所有权与发布。
- `internal/storage/types.go`、`metadata.go`、`memory.go`、`disk.go`：metadata v2 与原子保存。
- `internal/server/server.go`、`persistence.go`、`shutdown.go`、`publication.go`：有界 metadata 调度、重试和协议映射。
- `internal/network/message.go`、`codec.go`、`packet.go`：协议 v9 固定字段。
- `internal/render/renderer.go`、avatar/item-drop renderer 与 WGSL、`cmd/mcgo/app.go`：固定昼夜 uniform。
- `cmd/mcgo` benchmark、`cmd/perfcheck`、README 与性能记录：scenario v11 和兼容说明。

依赖方向保持不变：`world` 不读取 assets/network，`sim` 不读取 storage/render，`server` 完成装配，只有 `render` 触达 `gfx` 封装。无需新增包、第三方依赖或 archcheck 白名单。

## Risks / Trade-offs

- [直射模型不会让洞口侧光横向进入] → 明确作为 M4G 上限；有透明方块或光源后另建洪泛 change，不在本批伪装完整传播。
- [实体在屋顶下仍只受全局昼夜而不受遮挡] → 地形先提供空间差异；实体光照等通用光场出现后再统一，避免现在复制半套采样。
- [移除极高列顶会扫描 384 格并标脏最多 96 个区段] → 两者都是固定上限，dirty 合并和现有调度限流继续控制帧工作量，并用极端列测试与 benchmark 验证。
- [每个区块增加 512 字节，mesher job 复制九份高度表] → 对 4225 个区块约为 2.1 MiB/镜像，远低于现有预算；用 RSS、分配断言和 scenario v11 门禁核实，不放宽阈值。
- [metadata 与区块不是同一原子事务] → 世界时间独立于区块内容；自动保存各自提交边界快照，关服则从同一冻结状态取得最终快照；每份文件仍单独原子且失败可重试，不引入跨文件事务。
- [metadata v2 使旧程序无法直接回退] → 部署前正常关服并备份整个世界目录；回退必须恢复首次写入 v2 前的备份。
- [M4F 尚未归档导致基线或 delta spec 起点漂移] → tasks 第一项硬检查 M4F v10 基线、协议 v8 和归档主规格，条件不满足不进入实现。

## Migration Plan

1. 等待 M4F scenario v10 正式基线、规格同步和归档完成，以归档后的主规格和代码重新核对本 change；若 M4F 收尾改变已冻结契约，先更新本 change 产物。
2. 先加入 metadata v1/v2 双读与 v2 原子写测试，再接入权威时间；同一发布分支完成协议 v9 的客户端和服务端，不发布混合版本。
3. 加入高度表、mesher 天空光、增量 dirty 和昼夜 renderer；保持 chunk schema v4 和网络 snapshot payload 不变。
4. 发布前正常关服并备份完整世界。新程序读取 v1 后只在正常自动保存/关服时写 v2；任何失败保留旧文件并阻止无证据的成功关服。
5. 完成全仓无窗口门禁并冻结候选，按正式规则建立 M5 scenario v11 基线，然后同步 delta specs 和归档 change。
6. 回退时停止 v9 程序，恢复首次写入 metadata v2 前的世界备份，再启动 v8 程序；协议、M5 v11 基线和代码提交一并回退，M2 基线不动。
