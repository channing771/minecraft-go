## 1. 流体方块编码

- [x] 1.1 在 `internal/core/block.go` 末尾追加 `WaterSourceID` 与 `WaterLevel1ID`..`WaterLevel7ID`，扩展 `RegisteredBlock` 上界；补中文注释说明编号不可重排。验证：`go test ./internal/core -race -count=1`
- [x] 1.2 在 `internal/core` 新增纯查询 `IsFluid(BlockID) bool` 与 `FluidLevel(BlockID) uint8`（源为 0，`WaterLevelN` 为 N），并为二者写穷举测试（对 `BlockID` 全域断言分类正确）。验证：`go test ./internal/core -race -count=1`
- [x] 1.3 断言流体不进物品表：`BlockToItem` 对全部流体编号返回 false，`ItemIDMax` 数值不变；补枚举末项守护断言。验证：`go test ./internal/core -race -count=1`
- [x] 1.4 在 `internal/mesh` 的方块属性注册表补齐 8 个流体编号（`Opaque=false`、`Emission=0`），确保 mesh 输入构造遇到流体不报错。验证：`go test ./internal/mesh -race -count=1`
- [x] 1.5 `internal/physics/types.go` 的 `BlockCollisionBoxes` 对流体编号返回空碰撞体集合（`Loaded: true` 但无 box），使实体可穿行；补测试覆盖 spec Scenario「流体不阻挡通行与光照」。**本项为评审发现的计划遗漏补入**：原计划无任何任务认领该 Scenario，而现实现对一切非空气方块返回满格碰撞体。验证：`go test ./internal/physics ./internal/core -race -count=1`

## 2. 流动规则与调度器（新包 `internal/fluid`）

- [ ] 2.1 先写失败测试：在 `internal/fluid` 定义 `FluidWorld` 单格读写接口与内存测试替身，覆盖「可替换判定」（空气可替换、更大 level 的流动水可替换、源与实心不可替换）。验证：`go test ./internal/fluid -race -count=1`
- [ ] 2.2 实现单格流动规则求值：源存活、流动存活（上方是水或存在 level 更小的水平邻居）、垂直优先、水平传播递减、level 7 停止。逐条对应 spec 场景写测试。验证：`go test ./internal/fluid -race -count=1`
- [ ] 2.3 实现待更新队列：项为 `(core.BlockPos, dueTick)`，入队按 `FluidFlowDelayTicks` 写 `dueTick`，去重可用 map 但**处理前必须按 `(dueTick, ChunkKey, y, z, x)` 全序排序**。测试断言排序全序性与去重后不丢项。验证：`go test ./internal/fluid -race -count=1`
- [ ] 2.4 实现 `Advance(now uint64, w FluidWorld, budget int) []core.BlockPos`：本 tick 读取的存活判定只看 tick 起始状态，写入一次性提交；超预算项按原序保留。验证：`go test ./internal/fluid -race -count=1`
- [ ] 2.5 写「源沿平整地面铺开恰好 7 格、第 8 格为空气」的端到端规则测试。验证：`go test ./internal/fluid -race -count=1`

## 3. 决策验证关口（D5 成立与否在此裁决）

- [ ] 3.1 **重扫不动点**：水体推进至无变更 → 清空队列 → 边界重扫（流体格及其空气邻居入队）→ 断言后续推进零变更。这是「队列不持久化」决策的唯一依据。验证：`go test ./internal/fluid -race -count=1 -run Rescan`
- [ ] 3.2 **未平衡态重启收敛**：未到平衡时清空队列并重扫，断言最终平衡态与不清空时逐格一致。验证：`go test ./internal/fluid -race -count=1 -run Rescan`
- [ ] 3.3 **预算等价**：同一溃坝在 `budget=512` 与不受限预算下推进至无变更，断言最终状态逐格一致。验证：`go test ./internal/fluid -race -count=1 -run Budget`
- [ ] 3.4 **入队序无关**：同一组待更新格以两种不同入队顺序推进，断言每 tick 变更集与最终状态逐格一致。验证：`go test ./internal/fluid -race -count=1 -run Order`
- [ ] 3.5 **有限收敛**：随机初始水体（固定种子）在有限 tick 内到达不动点，断言无振荡。验证：`go test ./internal/fluid -race -count=1 -run Converge`
- [ ] 3.6 关口裁决：若 3.1 或 3.2 不成立，**停止后续任务**，回头修订 `design.md` 的 D5 与 `specs/authoritative-fluid/spec.md` 的对应场景（改为持久化队列），不得弱化测试。

## 4. 权威模拟集成

- [ ] 4.1 在 `internal/sim` 新增 `advanceFluids(pending map[core.ChunkKey]*pendingChunkChanges)`，仿 `advanceFurnaces` 的形状：只遍历 `activeInterestKeys()` 的 `ChunkReady` 区块，变更经 `touchChunk` 汇入。验证：`go test ./internal/sim -race -count=1`
- [ ] 4.2 在 `internal/sim/engine_step.go` 的 `Step` 中把 `engine.advanceFluids(pending)` 插在 `advanceFurnaces` 之后、`containerMoves` 之前；为阶段顺序补 `stepPhase` 探针断言。验证：`go test ./internal/sim -race -count=1`
- [ ] 4.3 接上入队点：方块放置、方块采掘写入后把该格及其六邻入队；区块进入 `ChunkReady` 时执行一次边界重扫入队。验证：`go test ./internal/sim -race -count=1`
- [ ] 4.4 测试「兴趣范围外不推进」与「区块重新进入兴趣范围后继续收敛」两个 spec 场景。验证：`go test ./internal/sim -race -count=1`
- [ ] 4.5 测试流体变更经既有区块变更通道广播，客户端只读镜像逐格一致（不新增消息类型）。验证：`go test ./internal/sim ./internal/server -race -count=1`

## 5. 世界生成注水

- [ ] 5.1 Rust：`engine/crates/mornlea_engine/src/worldgen.rs` 的 `Materials` 追加 `water` 字段，`as_array` 改为 14 项，FFI header 解析与互异性校验同步更新；补中文 doc comment。验证：`make rust && cargo test --manifest-path engine/crates/mornlea_engine/Cargo.toml`
- [ ] 5.2 Rust：在分层判定后加注水——`y <= SEA_LEVEL` 且该格判定为 air 时写 `materials.water`；断言矿石、树木、地表分层结果不受影响。验证：`cargo test --manifest-path engine/crates/mornlea_engine/Cargo.toml`
- [ ] 5.3 `engine/include` 更新 engine 头文件，ABI 版本 v3 → v4；`internal/nativeabi` 同步材料表编码与版本常量。验证：`make rust && go test ./internal/nativeabi -race -count=1`
- [ ] 5.4 Go 侧 `internal/worldgen`：材料表编码追加 `water`；门控实现为 **`fluidEnabled=false` 时 `water` 字段传 air 编号**（Rust 侧无分支）。验证：`go test ./internal/worldgen -race -count=1`
- [ ] 5.5 旧 Go 生成实现（测试 oracle）同步加注水规则，保持与 Rust 的逐位交叉锁。验证：`go test ./internal/worldgen -race -count=1`
- [ ] 5.6 测试「开关关闭时同种子世界与当前基线逐格一致」——这是关闭路径退化为现状的证明。验证：`go test ./internal/worldgen -race -count=1`
- [ ] 5.7 测试「开关开启时海平面及以下原 air 格全为源方块」与「非空气非流体格在开关两态下逐格一致」。验证：`go test ./internal/worldgen -race -count=1`

## 6. 存档演进

- [ ] 6.1 `internal/storage/chunk_codec.go`：`currentChunkSchema` 8 → 9，`migrateChunk` 新增 8 → 9 恒等分支（不改方块数据，置 `Migrated`）。验证：`go test ./internal/storage -race -count=1`
- [ ] 6.2 测试流体跨保存/加载逐格保真、旧 v8 区块按恒等迁移加载且不含流体、schema > 9 按 `ErrFutureVersion` 拒绝。验证：`go test ./internal/storage -race -count=1`
- [ ] 6.3 扩展既有区块编解码 golden 与 fuzz 覆盖到含流体的区块。验证：`go test ./internal/storage -race -count=1 -run Fuzz -fuzztime=30s`

## 7. 协议演进

- [ ] 7.1 `internal/network` 协议版本 v19 → v20；确认 wire 形状与长度上限不变（本次唯一变化是方块 ID 集合扩展）。验证：`go test ./internal/network -race -count=1`
- [ ] 7.2 测试旧版本客户端登录被拒绝并给出版本不匹配原因；扩展 wire golden 到含流体方块的区块 payload。验证：`go test ./internal/network -race -count=1`
- [ ] 7.3 Memory/TCP 双传输 parity：同一次溃坝在两种传输下每 tick 广播的方块变更逐格一致。验证：`go test ./internal/server -race -count=1`

## 8. 配置与 tunables

- [ ] 8.1 `internal/config` 新增 `fluidEnabled`（默认 `false`），config version 保持 1；补默认值与非法值测试。验证：`go test ./internal/config -race -count=1`
- [ ] 8.2 `internal/sim/tunables.go` 新增 `FluidFlowDelayTicks`（默认 5）与 `FluidUpdatesPerTick`（默认 512），按既有 tunable 约定接入快照。验证：`go test ./internal/sim -race -count=1`

## 9. 架构门禁

- [ ] 9.1 `internal/archcheck/dependency_test.go` 登记 `internal/fluid`：允许依赖 `core`、`world`；禁止依赖 `sim`、`network`、`render`、`storage`。验证：`go test ./internal/archcheck -count=1`

## 10. 收尾门禁

- [ ] 10.1 `make rust` 后运行 `go test ./... -race -count=1`，确认无回归。
- [ ] 10.2 运行 `go vet ./...` 与 `gofmt -l .`（后者应无输出）。
- [ ] 10.3 确认 benchmark scenario 保持 v16 且默认配置下工作负载未变：运行 `go run ./cmd/perfcheck` 相关基线比对，性能数值只记录，报告完整性/真实 overflow/数据丢失仍是门禁。
- [ ] 10.4 确认全部视觉 capture golden 字节不变（`fluidEnabled` 默认关闭，世界内容与基线一致）。
- [ ] 10.5 运行 `openspec validate --all --strict --no-interactive`。
- [ ] 10.6 核对 `tasks.md` 全部勾选、实现与 `specs/authoritative-fluid/spec.md` 一致；若实现过程中偏离规格，先修订 OpenSpec 产物再收尾。
