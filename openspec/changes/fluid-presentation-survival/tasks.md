## 1. registry 上限扩容与补偿分支回收

- [x] 1.1 Rust：`engine/crates/mornlea_engine/src/input.rs` 的 `MAX_REGISTRY_ENTRIES` 27 → 35。**不得**同时改同文件 `BLOCKS_BYTES = 27*4096*2` 里的 27——那是 3×3×3 邻域区段数，与条目上限只是数字巧合（design D6）。验证：`make rust && cargo test --manifest-path engine/crates/mornlea_engine/Cargo.toml`
- [x] 1.2 Go：`internal/mesh/native_input.go` 的 `nativeMaxRegistryEntries` 与 `maxNativeInputBytes` 随之扩容，注释同步更新绑定关系。验证：`go test ./internal/mesh -race -count=1`
- [x] 1.3 `internal/assets/blocks.go` 的快照 ids 上界扩到 `core.WaterLevel7ID`，流体纳入 mesh registry 快照。验证：`go test ./internal/assets ./internal/mesh -race -count=1`
- [x] 1.4 **删除** `assets.FaceVisible` 的两处 `core.IsFluid` 补偿分支（它们只为「流体不在快照里」存在，纳入后会让水永远不出面且测试仍绿）。**保留** `assets.Opaque` 的流体排除——`internal/mesh/visibility.go` 另有直接对活体 Section 数据调用的路径，与快照范围无关。验证：`go test ./internal/assets ./internal/mesh -race -count=1`
- [x] 1.5 **变异验证**：临时删掉 `assets.Opaque` 的流体排除，确认 `internal/mesh` 的活体路径测试变红；恢复。报告里写明改坏了什么、是否如期失败。验证：`go test ./internal/mesh -race -count=1`

## 2. 斜水面几何（engine）

- [x] 2.0 **registry 条目扩两个每方块字节**：`REGISTRY_ENTRY_BYTES` 16 → 18，追加 `fluid_height`（流体格的 `h_raw`，非流体用哨兵）与 `light_attenuation`（任务 4.2 用）。二者与既有 `Emission` 同形状（每方块一字节），`BlockProperties` 同步加字段。**这是 2.1/2.2 的前提**：Rust 的 mesher 目前**没有任何途径**从 block id 知道「是不是流体、是第几级」——ABI 条目只有 id/opaque/emission/6×material，而 8 个流体编号共用一个 material（任务 3.0），material 只能当「是不是水」的判别位、推不出 0..7。把 `h_raw` 直接做成每方块属性，Rust 就不必知道 level，等级→高度的映射留在 Go 一处。**在 Ruling 2 预留的「同一个 v5 内扩 ABI 面」额度内完成，不再升版**。**本项为任务组 1 评审发现的计划缺口补入**。验证：`make rust && go test ./internal/nativeabi ./internal/mesh ./internal/assets -race -count=1`
- [x] 2.0b 修正 `internal/mesh/native_parity_test.go` 的 doc comment：现注释称「只要两者对『水面是否出面』给出不同答案，parity 就变红」，但对**规则类**变异这不可能发生——Go oracle 与 Rust 位图同源于 `assets.FaceVisible`，规则一起坏、差值恒等。真正承重的是其后的计数守卫。改成如实描述，免得后人以为 parity 断言在守规则、从而放心删掉守卫。验证：`go test ./internal/mesh -race -count=1`
- [x] 2.1 先写失败测试：`h_raw(level) = 14 - level`、上方为流体取 15 的单格高度纯函数，对 8 个流体编号穷举断言。验证：`cargo test --manifest-path engine/crates/mornlea_engine/Cargo.toml`
- [x] 2.2 角高度：该角相邻四格中流体格 `h_raw` 的整数平均（向下取整），任一参与格上方为流体则取 15。**全整数运算，不得引入浮点**。用 `MeshInput.block()` 的既有 3×3×3 邻域读跨 section 邻居。验证：同上
- [x] 2.3 水的顶面/侧面按 1×1 出面（不贪心合并），角高度打进 `w`/`h` 释放的 8 bit 加现有 9 个空闲位。**quad 仍 MUST 是 `u64` / 8 字节**。补 pack/unpack 往返测试，对 level 组合穷举。验证：同上
- [x] 2.4 流体出面规则：同为流体的相邻面不可见、流体对空气可见、流体在实心方块下方的面不可见。穷举测试。**规则的落点是 `assets.FaceVisible`**——Rust 的 `RegistryView::face_visible` 只对 Go 烘焙的 Visibility 位图查表、自身不含规则，也拿不到「哪些编号是流体」的信息；在 Rust 里另写一套会制造第二个真值源。验证：`go test ./internal/assets ./internal/mesh -race -count=1`（含跨语言 parity 夹具 `TestNativeOracleParityWaterSurface`）
- [x] 2.5 覆盖 spec `fluid-presentation` 的「相邻不同水位之间连续过渡」「水柱内部没有斜面」「等级越弱高度越低」「高度派生是确定的」四个 Scenario。验证：同上

## 3. water pass（client）

- [x] 3.0 **给流体分配独立材质层**：`internal/assets` 现在对流体走 `Material` 的 `default: return LayerStone`，水面会以石头纹理出现在**不透明** terrain pass 里。新增水材质层与其程序化纹理，`Material` 对 8 个流体编号返回该层。**这是 3.1 的前提**——按 material 分流需要水有自己的 material。**本项为任务组 1 评审发现的计划缺口补入**：原计划从未分配水材质层。验证：`go test ./internal/assets ./internal/mesh -race -count=1`
- [x] 3.1 Go 上传路径按 material 把水 quad 分流到独立 buffer（mesh quad 流格式不变）。验证：`go test ./internal/mesh ./internal/render -race -count=1`
- [x] 3.2 `mornlea_client` 新增 water pass 与 `water.wgsl`：alpha blend、深度测试开、**深度写关**、排在 terrain pass 之后 HiZ build 之前、按 section 距离由远及近、**不接 GPU culling**（普通 `draw_indexed`）。顶点着色器按 material 与 face 解出角高度并下移顶点。验证：`make rust && cargo test --manifest-path engine/crates/mornlea_client/Cargo.toml`
- [x] 3.3 client ABI v4 → v5，头文件与 `internal/client` 绑定同批一致。验证：`make rust && go test ./internal/client -race -count=1`
- [x] 3.4 覆盖 spec 的「水面不遮挡其后的水面」「水面被不透明方块正确遮挡」「水面之下的地形可见」「排序粒度不细于区段」四个 Scenario。验证：离屏对照测试
- [x] 3.5 覆盖 `voxel-visual-presentation` MODIFIED 的「水面阶段不突破实例格式与资源边界」：quad 仍 8 字节、预热后零每帧动态资源、无第二个额外透明 pass。验证：`go test ./internal/render -race -count=1`

## 4. 天空光衰减与列顶语义

- [x] 4.2 `RegistryView` 增加 `light_attenuation`；BFS 结构不变，每步扣减改为按方块查表；流体额外衰减 1，竖直向下穿过流体不再无损。**方块光模型不动**（水与玻璃一样阻断）。验证：`make rust && cargo test --manifest-path engine/crates/mornlea_engine/Cargo.toml`
- [x] 4.3 覆盖 `authoritative-daylight` MODIFIED 的「水面之下随深度变暗但不立刻归零」，以及 `fluid-presentation` 的「水下随深度变暗」「浅水下方仍然可见」两条光照 Scenario。（原文另列的「流体不作为直射起点的遮挡」与「流体不再抬高直射起点」已随任务 4.1 的回退从 delta spec 删除——`openspec validate` 不交叉校验任务描述，故此处需人工同步。）验证：`go test ./internal/mesh -race -count=1`
- [x] 4.4 **回归守卫**：断言 `static-block-light` 的既有行为未变——方块光仍只经 `AirID` 传播，水与玻璃同样阻断。验证：`go test ./internal/mesh -race -count=1`

## 5. 浸没标志与水中物理

- [x] 5.1 新增计算浸没标志的纯函数（`BodyInFluid` = AABB 与任意流体格相交；`EyeInFluid` = 眼睛所在格是流体），服务端与客户端预测**共用同一实现**。验证：`go test ./internal/physics -race -count=1`
- [x] 5.2 `physics.Input` 增加两个 bool，`StepInput` 编码头版本递增；**不扩 prism**（design D4）。验证：`go test ./internal/physics -race -count=1`
- [x] 5.3 Rust `step.rs` 在 `BodyInFluid` 时切换积分常量：重力衰减、垂直终端速度压低、`Jump` 改为持续上浮、水平速度乘阻力。全部走新 tunable。**engine ABI 不再升版**——组 1 已升 v5，本项只是同一个 v5 内的 `StepInput` header v1 → v2 扩展（与任务 2.0 扩 registry 条目同理，Ruling 2 的额度内）。**摔落峰值重置落在 Go 侧 `internal/sim`**：峰值高度 `peakY` 是 sim 的瞬态字段，Rust step ABI 里没有它，也不应为此把伤害状态搬进 engine。验证：`make rust && go test ./internal/nativeabi ./internal/physics -race -count=1`
- [x] 5.4 覆盖 `fluid-survival` 的浸没判定三个 Scenario 与水中移动四个 Scenario（含「入水消除摔落伤害」）。验证：`go test ./internal/physics ./internal/sim -race -count=1`
- [x] 5.5 **权威与预测一致性**：同一方块镜像与位置下两侧算出的标志逐位相同。验证：`go test ./internal/sim ./internal/client -race -count=1`

## 6. 溺水、氧气与 HUD

- [x] 6.1 `internal/sim` 新增权威 `oxygen`（满值 `MaxOxygenTicks = 300`）：`EyeInFluid` 时每 tick −1，归零后每 `DrownDamageIntervalTicks`（默认 20）扣 1 血并**走既有伤害入口**（重置回血计时），出水立即回满。**不入存档**。验证：`go test ./internal/sim -race -count=1`
- [x] 6.2 协议 v20 → v21：`PlayerState` 追加 `Oxygen`。wire golden 与 fuzz 同步扩展。验证：`go test ./internal/network -race -count=1`
- [x] 6.3 `internal/render/hud` 增加氧气条，写进现有 hotbar 布局、画在生命值条上方，复用同一 HUD 图集与 pass，**零新 pipeline**；仅在未满时出现。验证：`go test ./internal/render/hud -race -count=1`
- [x] 6.4 覆盖 `fluid-survival` 的氧气五个 Scenario（含「溺水可致死并走既有死亡结算」与「氧气不跨重启保留」）与同步两个 Scenario。验证：`go test ./internal/sim ./internal/server ./internal/render/hud -race -count=1`
- [x] 6.5 水下视觉：相机在流体格时叠水色 tint 并压低远处可见度，**判定复用同一个 `EyeInFluid`**，不得另起一套。覆盖「视觉与溺水判定一致」Scenario。验证：`go test ./internal/render ./internal/client -race -count=1`

## 7. 清偿 F1 剩余待办

- [x] 7.1 raycast 目标判定：八处 `core.RaycastBlocks` 调用点的 solid 谓词区分流体，使水下可以瞄准、采掘、开箱与正常放置。验证：`go test ./internal/sim ./internal/server ./cmd/mornlea -race -count=1`
- [x] 7.2 出生点选取：`internal/sim/spawn.go` 不得把流体格判为可站立落脚点。验证：`go test ./internal/sim -race -count=1`
- [x] 7.3 伙伴寻路对流体的豁免复核：`productionCompanionPassableBlocks` 当前刻意让流体不可通过；本变更交付浸没物理后重新评估并更新 `internal/server/companion_manager_test.go` 的显式豁免分支与其退出条件注释。验证：`go test ./internal/server -race -count=1`

## 8. 配置、场景与基线

- [ ] 8.1 `internal/config` 的 `fluidEnabled` 默认值 `false` → `true`；更新其文档注释（原文描述的三条后果已被本变更消除）。验证：`go test ./internal/config -race -count=1`
- [ ] 8.2 benchmark scenario v16 → v17，新增唯一显式迁移 `16:17`，历史 `15:16` 退为归档证据。验证：`go test ./cmd/perfcheck ./cmd/mornlea -race -count=1`
- [ ] 8.3 更新 `AGENTS.md` 与 `CLAUDE.md` 的基线版本与能力描述（协议 v21、engine/client ABI v5、scenario v17、流体已有呈现与生存）。**两份必须逐字节相同**。验证：`go test ./internal/archcheck -count=1`

## 9. 视觉门禁

- [ ] 9.1 **先在 `fluidEnabled=false` 下确认全部 capture golden 逐字节不变**——这是「非流体路径无回归」的证明，必须在重生成之前做。验证：`go run ./cmd/mornlea --capture ...` 比对
- [ ] 9.2 翻开开关后重新生成全部 capture golden，并新增水景场景（含水面斜坡、水下视角、水面之下的地形）。验证：同上
- [ ] 9.3 记录哪些 golden 因水入画而变化、哪些因水下变暗而变化，逐场景说明差异来源。

## 10. 性能复测

- [ ] 10.1 实测**流动前沿**两个场景的单 tick 最坏耗时：玩家挖穿大坝、注水世界里的瀑布。`authoritative-fluid` 遗留的残余风险是 `Queue.Advance` 成本正比于队列规模而非预算（约 176–256 ns/项/tick，约 20 万项时单独吃满 50 ms）。**数值只记录，但权威 tick 被无界工作阻塞是门禁。**
- [ ] 10.2 若 10.1 显示越界，先上报数字与根因，由控制会话裁决是否扩范围修 `Queue.Advance` 的分桶——**不得自行调 tunable 默认值让数字好看**。

## 11. 收尾门禁

- [ ] 11.1 `make rust` 后运行 `go test ./... -race -count=1`。
- [ ] 11.2 `go vet ./...` 与 `gofmt -l .`（后者应无输出）。
- [ ] 11.3 `openspec validate --all --strict --no-interactive`。
- [ ] 11.4 核对 `tasks.md` 全部勾选、实现与六个 delta spec 一致；偏离时先修订 OpenSpec 产物。
