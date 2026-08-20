## Context

见 `proposal.md` 的 Why。本节只记录塑造实现方案的现状与约束，全部经代码核实。

- **quad packing**：`engine/crates/mornlea_engine/src/quad.rs` 把一个面压进 `u64`，占用 55 bit（x/y/z/w/h 各 4、face 3、material 16、ao 8、light 8），**空闲 9 bit**（55..63）。
- **8 字节是规格硬约束**：`voxel-visual-presentation` 明写「quad 实例格式 MUST 保持 `8` 字节」。任何把 quad 扩到 u128 的方案都会撞这条。
- **同一条规格还禁止本变更要做的事**：「不得增加第二个透明 pass、透明排序」。water pass 必须走 MODIFIED 显式放宽并写死边界。
- **mesh 输入已有 3×3×3 邻域**：`MeshInput.block()` 的索引是 `(cx*3+cy)*3+cz`，跨 section 读邻居水位**不需要任何输入格式变化**。
- **registry 上限是跨语言硬编码**：Rust `MAX_REGISTRY_ENTRIES = 27`、Go `nativeMaxRegistryEntries = int(core.MossyCobblestoneID)+1` 也是 27，`assets.NewRegistry()` 的 ids 止于 `MossyCobblestoneID`。加入 8 个流体编号需要 35。
- **terrain 是单次 indirect draw**：不透明与 cutout 同一次 `draw_indexed_indirect` 并接 GPU culling，alpha blend 无法混进去。
- **prism 只带碰撞盒**：`physics.CollisionSource` 提供的是 `CollisionBoxSet`，流体没有碰撞盒，Rust 无法从 prism 区分水与空气。
- **天空光的两处约束**：`authoritative-daylight` 现规格用「最高非空气方块」定直射起点，且「空气仍是唯一透光方块」。二者合起来就是水下全黑的根因。
- **方块光模型**：`static-block-light` 写死「仅向 `AirID` 相邻格传播……任何其他方块即使未来被标记为透明也 MUST 阻断」。玻璃同样阻断，水阻断与之一致。

## Goals / Non-Goals

**Goals:**

- 水面几何**不引入浮点不确定性**：角高度全整数运算，同一份方块数据逐次得到相同结果。
- 呈现改动**不突破既有资源边界**：quad 仍 8 字节，不新增字体图集/HUD 缓冲，预热后零分配。
- 浸没判定**只有一套**：视觉、物理与溺水共用同一对权威标志。
- 清偿 `authoritative-fluid` 记录的五项待办，使 `fluidEnabled` 可以默认开启。

**Non-Goals:**

- 见 `proposal.md` 的非目标节。此处补充三条设计级边界：
- 不做 per-quad 透明排序——排序粒度止于区段。
- 不把 water pass 接进 GPU culling / indirect 管线。
- 不改方块光模型（水与玻璃一样阻断）。

## Decisions

### D1：斜面角高度打进 quad 的冗余位，而非扩大 quad

水的顶面/侧面因角高度随邻域变化**本就无法贪心合并**，于是 `w`/`h` 恒为 1、两个 4-bit 字段成为冗余位。加上现有 9 个空闲位：

| quad 类型 | 可用位 | 需要 | 结论 |
|---|---|---|---|
| 水顶面 | 9 空闲 + 8（w/h）= 17 | 四角高度 4×4 = 16 | 放得下 |
| 水侧面 | 17 | 两个上角 2×4 = 8 | 放得下 |

判别条件是 `material ∈ 水集合` 且 face，**零额外标志位**。

**理由**：`voxel-visual-presentation` 把「quad 实例 8 字节」写成了 MUST。扩到 u128 不是「多花点带宽」，是**撞规格**。

**否决 · 把角高度作为第二条顶点流**：需要新的上传缓冲与 pass 状态，违反「不新增每帧动态资源」，且让水面与地形的上传路径分叉。

### D2：单格高度与角高度的整数规则

- 单格高度用 4-bit 值 `v` 表示实际高度 `(v+1)/16`。
- `h_raw(level) = 14 - level`：源（level 0）→ 14 即 15/16；level 7 → 7 即 8/16。**最弱流动仍有半格**，不会退化成零面。
- 上方为流体的格取 `h_raw = 15`（满格），使水柱内部无斜面、与上格无缝。
- 角高度 = 该角相邻四格中**流体格** `h_raw` 的整数平均（向下取整）；四格中任一格上方为流体则直接取 15。

**理由**：全整数、无浮点，满足「呈现不得引入浮点不确定性」。取平均而非取最大，是为了让相邻不同水位之间真正连续——取最大会让过渡退化回台阶。

**落地修正（任务组 2 上报）**：spec 初稿的「该边的高度 MUST 位于两格各自孤立高度之间」在四格平均下
**不是全域恒真**——第三、四格更低时平均可落到两者之外（反例：某角四格 `14,13,7,7` → 平均 10，
而共享边两格的孤立高度是 14 与 13）。承重的性质其实只有**连续性**：共享边两侧高度相等。
台阶渲染下两侧必然不等（各自水平在自己的 `h_raw`），所以连续性本身就足以排除台阶，
「介于之间」是多余且过强的断言。spec 已据此改为「连续 + 非各自水平」。

**位布局的下游后果**：生产路径是 Rust pack → Go `UnpackQuad` → render 重新 `Pack()`，
因此 **Go 侧也必须认识 bit 55..62 的角高度**——否则往返会丢数据，不只是「效果没出来」。
`mesh.Quad` 相应增加 `Corners` 字段，Go 对照 oracle 同步镜像整套推导。

### D3：water pass 独立、深度写关、按区段排序、不接 culling

排在 terrain pass 之后、HiZ build 之前；深度测试开、**深度写关**；按 section 距离由远及近；**不接 GPU culling**（走普通 `draw_indexed`）。

**理由**：terrain 是单次 indirect draw 且接 culling，alpha blend 混不进去。深度写关是为了让前后两片水面都可见——水面之间互相 depth-cull 会产生明显的空洞。不接 culling 是因为水的 quad 量远小于地形，接进去要改 cull shader 的段划分，代价不成比例。

**否决 · per-quad 排序**：水面基本共面，区段级排序足够；逐面排序是每帧的动态工作，与「预热后零分配」冲突。

### D4：浸没标志由调用方算好传入，不扩 prism

`physics.Input` 增加 `BodyInFluid` 与 `EyeInFluid` 两个 bool，服务端 sim 与客户端预测**从各自的方块镜像用同一纯函数**算出。`StepInput` 编码头版本随之递增。

**理由**：prism 只携带碰撞盒，而流体没有碰撞盒——要让 Rust 自己判断就得在 prism 里加一份逐格流体数组，那是为两个 bool 付整块数据的代价。parity 与现在的碰撞盒同构：两侧从同一方块数据用同一规则算，结果必然一致。

**否决 · 扩 prism 编码**：见上，成本不成比例，且会让 prism 的语义从「碰撞几何」滑向「通用方块视图」。

### D5：天空光放宽，方块光不动

天空光：列顶判定忽略流体；流体透光但每格额外衰减，竖直向下也不再无损。`RegistryView` 增加 `light_attenuation`，BFS 结构不变，只把每步固定的扣减改成按方块查表。

方块光：**不动**。

**理由**：水下全黑的根因只在天空光那两条约束。而 `static-block-light` 的「只有空气透方块光」是刻意写死的模型——玻璃同样阻断，水阻断与之一致。改方块光会同时动到玻璃的既有行为，超出本变更范围且没有需求驱动。

**取舍**：水下在夜间、无直射天空光时是暗的。这与玻璃后方的现有行为同构，属接受的边界。

### D6：registry 上限扩容与补偿分支的回收

`MAX_REGISTRY_ENTRIES` 27 → 35，Go 侧 `nativeMaxRegistryEntries` 与 `maxNativeInputBytes` 同批调整，流体纳入 `assets.NewRegistry()` 的快照 ids。

**必须同时删除** `assets.FaceVisible` 的两处 `core.IsFluid` 补偿分支——它们只为「流体不在快照里」而存在，纳入后会变成「水永远不出面」的错误，且**测试仍会全绿**。

**`assets.Opaque` 的流体排除不得删**：`internal/mesh/visibility.go` 另有一条直接对活体 Section 数据调用的路径，与快照范围无关，那处排除是永久事实。

**陷阱**：`engine/crates/mornlea_engine/src/input.rs` 的 `BLOCKS_BYTES = 27*4096*2` 里的 27 是 **3×3×3 邻域区段数**，与条目上限只是数字巧合，**不得一起改**。

## Risks / Trade-offs

- **[水 quad 不合并导致面数上升]** → 顶面/侧面按 1×1 出面。缓解：一个区段的水面上界是 256 个顶面 quad，相对地形量可忽略；且 `voxel-visual-presentation` 的 MODIFIED 已把「8 字节 + 无每帧动态资源」写成边界，实测超界即为门禁失败。
- **[删错补偿分支]** → `FaceVisible` 的两处必须删、`Opaque` 的一处不得删，方向相反且都不会让测试变红。缓解：D6 写明理由；任务清单要求对「删 `Opaque` 排除」做一次变异验证，确认会被 `internal/mesh` 的活体路径测试抓到。
- **[默认开启后流动前沿打尖峰]** → `authoritative-fluid` 遗留的残余风险：`Queue.Advance` 成本正比于队列规模而非预算（约 176–256 ns/项/tick，约 20 万项时单独吃满 50 ms）。缓解：任务清单要求实测溃坝与挖穿大坝两个场景；数值只记录，但权威 tick 被无界工作阻塞是门禁。
- **[视觉 golden 全量重生成掩盖真实回归]** → 水入画后所有 golden 必然变化，「重新生成」会顺手盖掉别的改动。缓解：要求先在 `fluidEnabled=false` 下确认 golden **逐字节不变**（证明非流体路径无回归），再翻开开关重生成。
- **[协议 v21 与 engine/client ABI v5 三者不同步]** → 三个契约同批变更。缓解：ABI 版本每次调用都校验；`internal/archcheck` 的 `TestBaselineVersionsMatchCode` 会在基线文档滞后时报红。

## Migration Plan

1. **协议**：v20 → v21（`PlayerState` 追加 `Oxygen`）。版本不匹配的客户端按既有登录拒绝路径处理，不做兼容层。
2. **engine ABI**：v4 → v5（registry 布局含 `light_attenuation`、`StepInput` header 含浸没标志）。
3. **client ABI**：v4 → v5（water pass）。
4. **benchmark scenario**：v16 → v17，新增唯一显式迁移 `16:17`，历史 `15:16` 退为归档证据。
5. **存档**：玩家 schema v6、区块 schema v9、世界 metadata v2、`companions.ai` schema v4 **均不变**——氧气不入存档，水面几何是纯派生。
6. **部署顺序**：先构建并替换两个动态库，再部署 Go 二进制；三者版本必须一致。
7. **回退**：`fluidEnabled=false` 回到 F1 的默认态，水不生成、water pass 无输入、浸没标志恒假。整支 revert 回到 F1 基线，**存档不需重建**（区块 schema 未变）。
