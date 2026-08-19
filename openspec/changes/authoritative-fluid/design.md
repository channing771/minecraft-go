## Context

见 `proposal.md` 的 Why。本节只记录塑造实现方案的现状与约束。

- **方块存储**：`world.Section` 用 `PalettedContainer` 保存 `BlockID`，新增方块编号的存储成本为零，不需要为流体引入按格元数据。
- **权威 tick 形状**：`sim.Engine.Step` 已有 `advanceDrops` / `advanceFurnaces` 两个「按活动兴趣区块顺序推进、经 `touchChunk` 汇入 `pendingChunkChanges`」的世界推进阶段，流体推进与之同构。
- **区块体积**：单区块 16×16×384 = 98,304 格。每 tick 全扫描活动兴趣范围不可行，流体必须由显式待更新队列驱动。
- **worldgen 材料表**：Rust `Materials` 是 13 项、经 FFI header 传入的结构，且 FFI 层校验 13 项两两互异。追加 `water` 会改变 header 布局。
- **worldgen 双实现**：Go `worldgen` 保留 seed→perm 播种与材料表编码，旧 Go 生成实现作为测试 oracle，与 Rust 逐位交叉锁。
- **存档迁移机制**：`storage` 已有 `currentChunkSchema` / `oldestChunkSchema` 与 `migrateChunk(schema, dto)` 只读迁移路径，并用 `Migrated` 标记「下次保存需改写」。

## Goals / Non-Goals

**Goals:**

- 流动推进在给定初始状态与预算下**可证明确定**，且不依赖任何哈希遍历顺序。
- 流动的每 tick 成本**有硬上界**，溃坝不能打出权威 tick 尖峰。
- 流体状态的持久化面**尽可能小**：只持久化方块本身，不持久化任何调度中间态。
- 复用既有的区块变更、广播与存盘路径，**不新增协议消息类型**。

**Non-Goals:**

- 见 `proposal.md` 的非目标节。此处补充两条设计级边界：
- 不为流体引入按格元数据（nibble 数组）通道——即使它能为将来的楼梯朝向、门开合铺路。
- 不把流动推进下沉到 Rust engine。

## Decisions

### D1：水位用 8 个稳定 BlockID 编码，而非按格元数据

`WaterSourceID` 与 `WaterLevel1ID`..`WaterLevel7ID` 追加在 `MossyCobblestoneID` 之后，`RegisteredBlock` 上界随之扩展。

**理由**：`PalettedContainer` 让新增编号的存储成本为零；方块同步、存档编解码、mesh 输入与光照传播全部按 `BlockID` 工作，8 个新编号**零成本复用整条既有管线**。

**否决 · 按格元数据（4-bit nibble 数组）**：需要同时改区块 schema 的物理布局、mesh 的输入格式与协议的区块 payload。它唯一的额外收益是为将来的方块朝向/状态铺路，而当前没有任何需求要求它——按 YAGNI 否决。将来真的需要方块状态时，那是一次独立的、有明确需求驱动的变更。

### D2：流动调度放在 Go 的新包 `internal/fluid`，不下沉 Rust

**理由**：Rust `mornlea_engine` 当前的全部导出（mesh、light、collision、raycast、step、worldgen）都是**无状态纯函数**。流动推进需要跨 tick 的待更新队列与区块写回，把它放进 engine 会破坏这个模型，并让 engine 反向依赖世界数据所有权。权威模拟的数据所有权在 Go `sim`，流动是权威模拟的一部分。

**依赖方向**：`sim` → `fluid` → `core` / `world`。`fluid` MUST NOT 依赖 `sim`、`network`、`render`，由 `archcheck` 登记锁定。

**包的形状**：`fluid` 不持有世界。对外接口是 `Advance(now uint64, w FluidWorld, budget int) []core.BlockPos`，`FluidWorld` 只暴露单格读写。这样调度逻辑可独立单测，也让「重扫不动点」这类性质测试不需要装配整个 `sim.Engine`。

**否决 · 直接写在 `sim` 内部**：流动规则与调度是本变更最需要密集性质测试的部分，独立包让它能脱离权威引擎被穷举测试；`advanceFurnaces` 那种「规则简单、状态在区块槽位里」的形状不适用于流体。

### D3：显式待更新队列 + 全序排序，而非扫描或 map 遍历

- 队列项为 `(core.BlockPos, dueTick)`。
- **入队点**四个：方块放置、方块采掘、流动自身写入的格及其六邻、区块加载时的边界重扫。
- 每 tick 取出 `dueTick <= now` 的全部项，按 **`(dueTick, ChunkKey, y, z, x)` 全序**排序后处理。

**理由**：全扫描的成本见 Context。而 Go map 的遍历顺序是随机的，直接用 map 集合去重再遍历会让每 tick 的处理顺序不可复现，破坏 Memory/TCP parity 与存档可复现性。去重仍可用 map，但**处理前必须落到全序**。

**流动延迟**：`FluidFlowDelayTicks`（默认 5）在入队时写入 `dueTick`。它同时是「水看起来在流动而不是瞬移」的观感来源与天然的合并窗口。

### D4：每 tick 预算，超额顺延而非丢弃

`FluidUpdatesPerTick`（默认 512）限制单 tick 处理的格数；超额项按原全序保留到下一 tick。

**理由**：溃坝的待更新量是无界的，权威 tick 不能被无界工作阻塞（架构约束）。顺延而非丢弃保证平衡态与不受限预算下一致——这一点由规格的「预算不改变平衡态」场景锁定。

**取舍**：大水体收敛变慢。这是刻意的：正确性（最终状态一致）优先于收敛速度。

### D5：待更新队列不持久化，重启用边界重扫恢复

存档只写方块本身。重启后对每个加载区块执行一次边界重扫：把所有流体格及其空气邻居入队。

**理由**：源方块与流动方块**本身就是持久化的 `BlockID`**，队列只是通往平衡态的中间态。重扫后重新推进会收敛到同一平衡态，因此持久化队列是纯粹的冗余——省掉一次 schema 结构扩展与一整套队列编解码/迁移/fuzz 覆盖。

**该决策的成立条件是一条可证明的性质**：平衡态必须是边界重扫的不动点。它被提升为规格场景与门禁测试；如果该性质不成立，本决策作废、必须改为持久化队列。

**否决 · 持久化队列**：多一个变长存档区、多一次 schema 迁移、多一套 fuzz 与损坏处理，换来的只是「重启后少跑几个 tick」。

### D6：worldgen 注水在 Rust 内完成，材料表 13 → 14

`Materials` 追加 `water`，FFI header 布局随之变化，**engine ABI v3 → v4**。生成规则：`y <= SEA_LEVEL` 且按现有分层判定为 air 的格写 `WaterSourceID`。

**理由**：注水必须与高度图、分层、矿石、树木在**同一次确定性生成**里完成，否则同种子世界要靠事后 pass 二次改写，既慢又难保证跨实现一致。

**门控位置**：`fluidEnabled` 在 **Go 侧**决定传入材料表的 `water` 字段是取真实流体编号还是取 `air`。这样 Rust 侧无分支、无开关语义，关闭时生成路径与当前基线**逐位一致**——这正是规格「开关关闭时世界与当前基线一致」场景所要求的。

**否决 · Rust 侧加布尔开关**：会在 worldgen 的热路径上引入一个跨 FFI 的行为分支，且需要在 Rust 侧单独测试两条路径；用材料表复用 `air` 编号则天然退化为现状。

### D7：v8 → v9 是恒等迁移，不做旧区块注水

`migrateChunk` 的 8 → 9 分支不改写任何方块数据，只抬升 schema 版本并置 `Migrated` 标记。

**理由**：注水 pass 需要对旧区块重新判定「哪些格原本是 air 且低于海平面」，而这些格可能已被玩家改动，无法区分「天然空腔」与「玩家挖出的地下室」——盲目注水会淹掉玩家建筑。

**取舍**：新旧区块之间出现干湿边界。本项目处于开发期，存档可重新生成，这是已接受的边界，记录在 `proposal.md` 的非目标。

### D8：不新增协议消息，流体变更走既有区块变更通道

流动写入的格经 `touchChunk(key, pending)` 汇入 `pendingChunkChanges`，与放置、采掘、掉落物、熔炉共用同一批变更的广播与存盘。协议 v20 的唯一变化是**方块 ID 集合扩展**，wire 形状与长度上限不变。

**理由**：流体变更在语义上就是普通方块变更，没有任何理由单开通道。复用还顺带保证了流体变更与同 tick 内其他方块变更的**原子性与顺序一致**。

## Risks / Trade-offs

- **[「平衡态是重扫不动点」不成立]** → 该性质是 D5 的全部依据。规格已把它写成可判定场景，任务清单把它排在调度器实现之后、sim 集成之前，作为**决策验证关口**：若性质不成立，立即改为持久化队列并回头修订 proposal 与 spec，而不是弱化测试。
- **[流动规则的存活判定产生振荡]** → 「流动方块存活 ⟺ 上方是水或存在 level 更小的水平邻居」在环状拓扑下可能反复生灭。缓解：存活判定只读取当前 tick 开始时的状态，写入在本 tick 内一次性提交，避免同 tick 内的读写交错；并用性质测试断言任意初始状态在有限 tick 内到达不动点。
- **[预算下大水体收敛过慢]** → 默认 512 是估计值，不是实测值。缓解：预算与延迟都是 tunable，可在实测后调整；规格只约束「预算不改变平衡态」，不约束具体数值。
- **[engine ABI v3 → v4 与 `rust-engine-lod-shell` 冲突]** → 该 change 也计划 ABI v3 → v4。缓解：已确定流体先行，`rust-engine-lod-shell` 的 proposal 需要在本变更合并后按新基线重写版本计划（它的版本计划本身已因 main 同步而失效）。
- **[流体方块在渲染上按不透明方块处理]** → 本变更不改渲染。缓解：`fluidEnabled` 默认关闭，默认配置下世界中不会出现流体；开启开关属于开发者显式操作，后续变更 `fluid-presentation-survival` 交付正确呈现。
- **[新方块编号一旦发布即不可重排]** → 与既有物品/方块编号约定一致。缓解：8 个编号一次性追加在末尾，并由「枚举末项守护」断言在将来追加时报警。

## Migration Plan

1. **存档**：区块 schema v8 → v9，v8 只读兼容并按恒等语义迁移，读到时置 `Migrated`、下次保存改写。schema 高于 v9 的记录按既有 `ErrFutureVersion` 拒绝。
2. **协议**：v19 → v20。版本不匹配的客户端按既有登录拒绝路径处理，不做兼容层。
3. **engine ABI**：v3 → v4。`mornlea_engine` 与 Go 侧是同一不可跨版本混装的 release unit，Linux 专服 bundle 同样按整体替换。
4. **部署顺序**：先构建并替换 engine 动态库，再部署 Go 二进制；两者版本必须一致。
5. **回退**：`fluidEnabled=false`（默认值）即行为级回退——不注水则世界中不存在流体格，调度队列恒为空，流体路径全部退化为空操作。整支 revert 回到当前基线；但已按 v9 写入的区块记录无法被 v8 代码读取，需要重新生成世界。
