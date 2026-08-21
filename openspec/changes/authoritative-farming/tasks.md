# 任务

## 1. 编号、容量与哨兵

- [x] 1.1 `internal/core` 追加 10 个稳定方块编号(`FarmlandDryID` / `FarmlandWetID` / `WheatStage0..7ID`)与 4 个物品编号(`ItemStoneHoe` / `ItemIronHoe` 及其损坏形态、`ItemWheatSeeds`、`ItemWheat`),并补 `RegisteredBlock` / `RegisteredItem` / `ItemPlacement` / `BlockDrop` 对应项。验证:`go test ./internal/core -race -count=1`
- [x] 1.2 mesh registry 上限 `35 → 48`:Rust `MAX_REGISTRY_ENTRIES`、Go `nativeMaxRegistryEntries`、`maxNativeInputBytes` 三处同批。**`BLOCKS_BYTES` 里的 27 是 3×3×3 邻域区段数,不得一起改。** 验证:`make rust` 与 `go test ./internal/mesh -race -count=1`
- [x] 1.3 **扫全仓以 `WaterLevel7ID` 为界的哨兵与循环上界**并逐处修正。F1 同类哨兵曾在五个包里失效(其中一处是真实行为回归),F2 终审又发现 `TestCompanionManagerPathBlockTableMatchesCollisionOracle` 的循环上界写死。**必须同时覆盖常量定义与循环上界。** 验证:`go test ./... -race -count=1`
- [x] 1.4 变异验证:把 Rust 侧上限改回 35,确认有测试变红且诊断指向 registry 容量。

## 2. 材质与交叉面几何

- [x] 2.1 `internal/assets` 新增程序化纹理层:耕地干(深褐)/湿(近黑)、小麦 8 阶段(嫩芽绿→成熟金黄)。视觉按 MC 约定看齐,**不引入任何二进制美术资源**。验证:`go test ./internal/assets -race -count=1`
- [x] 2.2 定义植物 material 集合,`Opaque` 排除植物,`FaceVisible` 让植物不出轴向面。验证:`go test ./internal/assets ./internal/mesh -race -count=1`
- [x] 2.3 Rust mesh:植物格出 4 片 quad(两条对角线 × 正反两面),**不贪心合并**,`w`/`h` 强制为 1。**quad 位布局零变更**。验证:`make rust` 与 `cargo test -p mornlea_engine`
- [x] 2.4 着色器按 `face ∈ {6,7}` 分支把顶点摆到对角面(Ruling 13:`face∈{6,7} ⟺ material ∈ 植物区间` 在 Go 解包与 Rust 发射端双向强制,shader 只信已校验的流;`cull.wgsl` 同步按植物法线判可见,Ruling 14);光照取上方相邻格。走既有 terrain pass 的 alpha cutout,**零新 pass**。验证:`make rust` 与离屏对照测试
- [x] 2.5 覆盖 `plant-visual-presentation` 的 Scenario:四个水平方向都可见、相邻植物不合并、不新增绘制阶段、面实例仍 8 字节且每格有固定上界、预热后零每帧分配、光照取上方格。验证:`go test ./internal/mesh ./internal/render -race -count=1`
- [x] 2.6 变异验证两条:去掉「按 material 判别」让植物退回轴向面,确认可见性 Scenario 变红;把每格面数上界断言的夹具改到应当拒绝的规模,确认守卫会红。

## 3. 碰撞与耕地形状

- [x] 3.1 `physics.BlockCollisionBoxes`:作物返回零碰撞体(复用流体分支形状),耕地返回高 `15/16` 的单盒。prism 每格本就携带任意 AABB 数组,**无需 FFI 编码变更**。验证:`go test ./internal/physics -race -count=1`
- [x] 3.2 覆盖两条 Scenario:玩家穿过作物、站上耕地低于站上完整方块。验证:`go test ./internal/physics ./internal/sim -race -count=1`
- [x] 3.3 复核出生点选取与伙伴寻路是否被新编号意外影响(作物零碰撞体会让它成为可站立空间的一部分),**结论写进报告**;伙伴对流体的既有豁免不得被本变更改动。验证:`go test ./internal/sim ./internal/server -race -count=1`

## 4. 锄头、翻地命令与协议 v22

- [x] 4.1 石锄/铁锄物品与损坏形态接进 `tool-durability` 既有体系;新增 recipe ID `9`/`10`,**既有 ID 1..8 语义不得位移**。验证:`go test ./internal/core -race -count=1`
- [x] 4.2 协议 v21 → v22:新增翻地命令 kind,`RejectReason` **只追加不重排**(目标不可翻地、上方非空气、手持非锄头)。wire golden 与 fuzz 同步扩展。验证:`go test ./internal/network -race -count=1`
- [x] 4.3 `internal/sim` 实现翻地:走 `openContainer` 同形路径,触及距离校验,成功后改方块并扣一点耐久。验证:`go test ./internal/sim -race -count=1`
- [x] 4.4 **同组内同步 `AGENTS.md` 与 `CLAUDE.md` 的协议版本号**(契约版本改动必须与基线文档同批,否则 archcheck 会从本组一直红到最后)。**两份必须逐字节相同。** 验证:`go test ./internal/archcheck -count=1`
- [x] 4.5 覆盖 `authoritative-farming` 翻地四条 Scenario 与 `tool-durability` 的两条新 Scenario(翻地成功扣耐久且方块未被移除、翻地被拒不磨损)。验证:`go test ./internal/sim ./internal/network -race -count=1`
- [x] 4.6 变异验证:去掉翻地成功后的耐久扣减,确认变红;把拒绝路径改成也扣耐久,确认变红。
- [x] 4.7 `cmd/mornlea` 输入层(Ruling 23 计划缺口):手持锄头对泥土/草按「使用」键时发翻地命令而非放置;其余手持物行为不变。客户端只读镜像 + 发命令,不越权威边界。验证:`go test ./cmd/mornlea -race -count=1`

## 5. 种植与收获

- [x] 5.1 种植复用放置路径:放置校验加「种子只能放在耕地正上方的空气格」。验证:`go test ./internal/sim -race -count=1`
- [x] 5.2 收获复用采掘路径:采掘表加作物(任意手持 `1` tick,Ruling 30:`0` 是既有「不可采掘」哨兵)与耕地(`5` tick)条目,掉落按方块编号分支。验证:`go test ./internal/sim -race -count=1`
- [x] 5.3 覆盖 `authoritative-mining` 三条新 Scenario 与 `authoritative-farming` 的种植/收获 Scenario。验证:`go test ./internal/sim -race -count=1`
- [x] 5.4 **显式拒绝伙伴种地与收获**(Ruling 5:多掉落在 core 表达不出,巧合性安全不成立):`internal/sim/mining.go`、`internal/companion/plan_types.go` 与 `internal/sim/companion_placement.go`(Ruling 8:BlockDrop→ItemPlacement 往返二重校验已放行种子,须一并显式拒绝)三处防御清单同改并加断言;planner 白名单方向由组 1 的 `planPlaceExempt` 已钉。验证:`go test ./internal/server ./internal/companion -race -count=1`
- [x] 5.5 变异验证:去掉「种子只能种在耕地上」的校验,确认变红;让未成熟作物不掉种子,确认「误挖不亏种子」变红。

## 6. 生长机制与环境判定

- [x] 6.1 `sampleCells` 纯函数:`hash(worldSeed, tick, chunkX, chunkZ, sectionY, i) mod 4096`,**不用全局 RNG、不用浮点**。验证:`go test ./internal/sim -race -count=1`
- [x] 6.2 `growCrop` 纯函数,**穷举测试** 8 阶段 × 湿/干 × 露天/遮蔽 = 32 种输入。验证:`go test ./internal/sim -race -count=1`
- [x] 6.3 接进 `sim.Engine.Step`:按 `(ChunkKey, sectionY)` **全序**枚举活动兴趣范围内的已加载 section,**绝不遍历 map**;每 section 抽 3 格;`RandomTicksPerSection`(默认 3)与生长概率 tunable 进 `physics/sim` tunable 体系并登记 `internal/config` 的 `Fields()` 钳制(Ruling 2:不登记等于新参数永久漏出配置钳制)。验证:`go test ./internal/sim -race -count=1`
- [x] 6.4 环境判定:露天读 `heights` 列顶图,湿润扫水平切比雪夫距离 ≤ 4、同层或上一层;耕地干湿双向转换。两者均为**纯查询**,不写 `internal/world` 与 `internal/fluid` 的状态。验证:`go test ./internal/sim ./internal/world -race -count=1`
- [x] 6.5 覆盖 `authoritative-farming` 的生长四条与确定性两条 Scenario。**端到端行为测试把生长概率 tunable 设成 100%**,否则用例会因「恰好没抽中」而绿。验证:`go test ./internal/sim -race -count=1`
- [x] 6.6 变异验证三条:让抽样依赖 map 遍历顺序,确认「相同输入重放一致」变红;去掉露天判定,确认「被遮挡的作物不生长」变红;让单 tick 考察量正比于作物数,确认「作物数量增加不改变单 tick 考察量」变红。

## 7. 初始材料包与端到端

- [x] 7.1 缺失玩家材料包加入起步种子。**不得向已有玩家补发**。验证:`go test ./internal/sim ./internal/server -race -count=1`
- [x] 7.2 端到端完整循环:翻地 → 种植 → 生长到成熟 → 收获 → 用收获的种子再种。断言循环**自持**(收获产出的种子数不少于种下的)。验证:`go test ./internal/server -race -count=1`
- [x] 7.3 变异验证:让材料包不含种子,确认「首次进入的玩家持有种子」变红;让成熟作物只掉 1 种子而不掉小麦,确认端到端断言变红。
- [x] 7.4 HUD 合成面板加 recipe 9/10 两行(Ruling 24 计划缺口:`hud.inventoryRecipeIDs` 写死 8 行,锄头在 UI 不可达)。若 `inventory-crafting` capture golden 因布局变化,重生成并逐场景说明差异来源;其余 golden 必须逐字节不变。验证:`go test ./internal/render/hud ./cmd/mornlea -race -count=1` 与 capture 比对

## 8. 收尾门禁与归档准备

- [x] 8.1 `make rust` 与 `make rust-check` 后运行 `go test ./... -race -count=1`。**已知既有红灯不得修改、不得改阈值**:`TestChatCommandAddressesExactConfiguredCompanionAtTickBoundary`(单独 `-run` 时约 9/10 失败、全包跑时通过,测试隔离缺陷)、`TestDroppedItemSurvivesShutdownAndRestart` 与 `TestAuthoritativeMiningMemoryLifecycle`(90 秒超时,macOS 时序抖动)。出现这三条之外的失败必须上报。 **前置**：`farmingLoginBudget` 600 → 3000（Ruling 44，实测卡点是异步区块生成，空闲机器约 202 tick、满载 300+；该预算唯一职责是把挂起变成可读失败）。
- [x] 8.2 `go vet ./...` 与 `gofmt -l .`(后者应无输出)。
- [x] 8.3 `openspec validate --all --strict --no-interactive`。
- [x] 8.4 核对 `tasks.md` 全部勾选、实现与六个 delta spec 一致;偏离时**先修订 OpenSpec 产物**。 **结论**：查出两处偏离并按「先修订 OpenSpec 产物」处理——(a) `authoritative-crafting` 的 MODIFIED 整块替换掉了主规格 11 条 Scenario 中的 10 条（归档即静默删除），已原样带回并把「未知配方大于 `8`」更新为「大于 `10`」；(b) 主规格对**新增配方**的限定词「相同初始状态与命令序列经 Memory 和 TCP MUST 得到相同结果」对 recipe 9/10 零承重，已把既有 `TestPlantSeedsMemoryTCPParity` 的开局改成脚本自己合成 recipe 9（新增 8 行）并补上对应 Scenario。其余 Requirement 正文的限定词逐条核对均有承重测试。
- [x] 8.5 **遗留与简化清单落纸**:核对 `design.md` 的清单已覆盖执行期间新出现的每一条简化。 本组新增残余 21（作物阶段的墙钟单价随耕地密度上升，上界由「抽样数 × 162」决定而非作物数）、22（既有缺陷：`TestPerfcheckV15SameCommitExplicitCrossTransportComparison` 的失败信息写成 v16）、23（升到 scenario v18 但未生成 v18 的 record-only 报告）。
- [x] 8.6 `docs/notes/progress.md` 追加里程碑条目;基线文档能力描述同步。 条目以变更名开头、不用「当前」；`AGENTS.md`/`CLAUDE.md` 基线名改为「`fluid-presentation-survival` 之后的 `authoritative-farming`」、能力写成现存事实、benchmark scenario 与迁移改为 v18/`17:18`，两份逐字节相同；`README.md`/`README.en.md` 与 `docs/notes/perf-baseline*.md` 的活文档句同步（快照节不动）。
- [x] 8.7 若渲染或 tick 热路径读数变化,运行对应 benchmark 并记录;**性能数值只记录,报告完整性与真实 overflow 仍是门禁**。判定 benchmark scenario 是否需要升版。**含(Ruling 37)**:为 `advanceCrops` 登记 `stepPhase` 探针,并补一条满编(活动兴趣范围内全部 section)的 `sim` benchmark,实测单 tick 作物阶段耗时与触及格数;数值只记录。 **结论**：`advanceCrops` 已登记 `phaseCropAdvance`（单独登记而非折进流体阶段，两者成本模型不同）。满编 200 区块（8 会话、兴趣范围互不重叠）在默认 tunable 下单 tick 约 113.9 µs、触及 14400 格、零分配；256 株与 51,200 株两档对照 cells/op 完全相等（Dense 慢 40% 是耕地单价差，见残余 21）。**benchmark scenario 判定为 v17 → v18（必须升）**：Hotbar HUD 固定上传布局移动（quad 238 → 247、glyph offset 11776 → 12288、总容量 45376 → 45888、空聊天帧写入 11776 → 12288），命中的正是主规格判定 v15 → v16 时用的同一条条文；另有 mesh registry 每次调用的 FFI 输入 910 → 1170 bytes 与新增的每 tick 枚举全部区段的作物阶段。唯一显式迁移改为 `17:18`、`16:17` 退为归档证据，新增 `bounded-benchmark-workload` delta spec，基线文档与 `perf-baseline*.md` 活文档句同步。
- [x] 8.8 `hud-hotbar-health` golden(Ruling 42):组 1 追加物品编号使 `ItemIDMax` 30→36、HUD 图集变宽、石头/石砖缩略图 UV 亚像素漂移(0.115%,最大通道差 47)。**先**像素级确认新缩略图采样的仍是石头/石砖层(与 `registry.LayerRGBA` 直接比对),**再**重生成该 golden 并说明差异来源;若采样到了邻层,那是缺陷,停下上报。验证:capture 比对 EXIT=0 **结论**：像素级确认通过——石头/石砖缩略图与 `registry.LayerRGBA` 逐层比对后最接近的仍是层 0 与层 5（排名 1，MAE 8.582 / 3.232，次优层 12.725 / 14.562），纹素内部像素 219/256 与 253/256 逐字节相等（不等者全部是叠加在缩略图上的数量数字），列 UV 的 texel 边界与整数列边界相差约 4e-6 纹素，**不是邻层**。确认后只重生成这一张，capture 比对回到 EXIT=0、其余 12 张逐字节不变；并新增 `TestHotbarColumnUVStaysInsideItsOwnColumn` 把「列 UV 不越界」变成机械断言（已变异验证）。

## 9.(可分离)注释标识符保真门禁

> 本组自成一体,可整组摘出转为独立变更,不影响前八组。

- [ ] 9.1 `internal/archcheck` 新增检查:**注释中提及的标识符必须存在**。F2 里此类失真出现**四次**,其中一次就在删掉该字段的同一个 commit 里,另一次在刚做过该文件专项检查的同一轮——已证实人工检查挡不住。
- [ ] 9.2 **先用已知坏样本自检检测器**再信任其「0 条」结论——一个恒真的扫描器与不扫描等价,而它看起来像扫过了。验证:`go test ./internal/archcheck -count=1`
