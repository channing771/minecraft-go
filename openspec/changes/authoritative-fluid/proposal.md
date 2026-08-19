## Why

世界里没有水。`worldgen` 的 `SEA_LEVEL = 64` 只是噪声偏移，海平面以下的格全部留空气，所以所有低洼地形都是干盆地——这是 M4 系列基础玩法里最显眼的结构性空洞。M5 走完了 AI 伙伴支线，现在回补基础玩法，流体是第一块：它自身是可玩内容，也是后续溺水与游泳、水桶、岩浆、农业与船的共同前提。

本变更只交付**权威侧**的流体：方块编码、流动规则、调度与预算、worldgen 注水、存档与协议。呈现（半透明水面、斜面几何、水下光衰减）与生存（浸没物理、溺水、氧气 HUD）属后续变更 `fluid-presentation-survival`。为了让 main 在两个变更之间不处于「有水但没有水的呈现」状态，worldgen 注水挂在默认关闭的 `fluidEnabled` 开关后，由后续变更翻开。

## What Changes

- **方块编码**：`internal/core` 追加 8 个稳定 BlockID——`WaterSourceID`（源，满格）与 `WaterLevel1ID`..`WaterLevel7ID`（流动水，1 最强、7 最弱）。新增纯查询 `IsFluid` 与 `FluidLevel`。流体不进物品表（`ItemIDMax` 不变），采掘水不产出任何物品。
- **流动规则**：源方块永不因规则消失；流动水的存活取决于上方是水或存在 level 更小的水平邻居；下方可替换时垂直优先并停止水平传播；水平传播写 `WaterLevel(N+1)`，越过 7 即停止。
- **调度器**：新建 `internal/fluid`，以显式待更新队列（`(BlockPos, dueTick)`）推进流动，全扫描不可行。每 tick 按 `(dueTick, ChunkKey, y, z, x)` 全序排序处理，受 `FluidUpdatesPerTick` 预算约束，超预算项按序留到下一 tick。队列**不持久化**，重启后对已加载区块做一次流体边界重扫收敛回同一平衡态。
- **权威集成**：`sim.Engine.Step` 在 `advanceFurnaces` 之后新增 `advanceFluids` 阶段，只推进活动兴趣范围内的区块，变更经既有 `touchChunk` 汇入同一批 `pendingChunkChanges`，复用现成的区块广播与存盘路径。
- **worldgen 注水**：Rust `Materials` 表 13 → 14 项（追加 `water`），`y <= SEA_LEVEL` 且按现有分层判定为 air 的格写入 `WaterSourceID`。地表分层、矿石与橡树逻辑不变。**BREAKING**：engine ABI v3 → v4（header 布局变化）。
- **配置**：新增 `fluidEnabled`（默认 `false`）门控 worldgen 注水；新增 tunable `FluidFlowDelayTicks`（默认 5）与 `FluidUpdatesPerTick`（默认 512）。config version 保持 1。
- **协议**：v19 → v20，方块 ID 集合扩展 8 项。wire 形状与长度上限不变。
- **存档**：区块 schema v8 → v9。v8 → v9 是**恒等迁移**——旧区块结构合法，只是不含流体格。已生成的旧区块保持干燥，不做迁移注水。

## Capabilities

### New Capabilities

- `authoritative-fluid`：权威流体的完整行为契约——流体方块编码与语义、流动规则全集、调度顺序与预算、重扫收敛、worldgen 注水与门控、存档与协议演进。

### Modified Capabilities

无。既有主规格描述的行为均原样成立：`deterministic-ore-generation` 与 `natural-material-generation` 的生成规则不变（流体只填充原本为 air 的格）；`bounded-benchmark-workload` 的固定工作负载不变（`fluidEnabled` 默认关闭，benchmark 世界内容与当前基线逐位一致，scenario 保持 v16）。

## Impact

- **受影响包**：`internal/core`（方块编码与流体查询）、`internal/fluid`（新建）、`internal/sim`（Step 阶段与入队点）、`internal/world`（方块写入触发入队）、`internal/worldgen`（材料表编码与测试 oracle）、`internal/nativeabi`（engine ABI v4 绑定）、`internal/storage`（chunk schema v9）、`internal/network`（协议 v20）、`internal/config`（`fluidEnabled`）、`internal/mesh`（新方块的 registry 条目，`Opaque=false`）、`internal/archcheck`（登记新包）、`engine/crates/mornlea_engine`（worldgen 注水 + Materials 表）、`engine/include`（engine 头文件）。
- **兼容性**：协议 v19 → v20；区块 schema v8 → v9（恒等迁移，v8 只读兼容）；engine ABI v3 → v4。玩家 schema v6、`companions.ai` schema v4、世界 metadata v2、client ABI v4 均不变。benchmark scenario 保持 v16，M2 v15 / M5 v14 基线保持原字节。
- **并发**：流体推进在单写者权威 tick 内串行执行，与掉落物、熔炉同构，不引入新的 goroutine 或锁。
- **性能**：流动受每 tick 预算硬约束，溃坝无法打出 tick 尖峰；代价是大水体收敛变慢，这是刻意取舍。benchmark 与 `perfcheck` 数值只记录。
- **回退**：`fluidEnabled=false`（默认）即行为级回退——不注水则世界中不存在流体格，调度队列恒为空。整支 revert 回到当前基线；已按新 schema 写入的区块需重新生成世界。

## 非目标

- 不做水桶、取水与放水（玩家无法创造或移除水源）。
- 不做「两个源相邻生成新源」的无限水源规则——没有水桶时该规则无从触发，随水桶一并交付。
- 不做岩浆、造石与黑曜石。
- 不做流体的任何呈现改动：不做半透明水面、斜水面几何、水下光衰减与水下 tint。本变更只为流体登记 `Opaque=false` 的方块属性以保证 mesh 与光照不报错，水会沿用既有透明方块的呈现路径而没有任何专属处理；默认配置下世界中不会出现水，正确呈现由后续变更交付。
- 不做浸没物理、游泳、浮力、溺水与氧气。
- 不做水流对实体的推力与水流方向动画。
- 不对已生成的旧区块做迁移注水，接受新旧区块之间的干湿边界。
