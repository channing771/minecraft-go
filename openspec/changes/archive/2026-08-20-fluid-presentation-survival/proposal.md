## Why

`authoritative-fluid` 交付了服务端权威的水，但把 `fluidEnabled` 默认关闭——因为水**没有任何呈现**。现在 main 上带着一个打不开的功能：开启后水下全黑、水体周围地形不出几何（看穿到虚空）、水下无法瞄准或采掘任何方块。

这是有时钟的债。每往后做一个子系统，翻开这个开关就更难一分：新写的寻路、生成与交互代码都会在「世界里没有水」的假设下写成，等真要开时要改的地方比现在多。

本变更让水**可以被打开**：补齐呈现（斜水面几何、半透明水面、水下光衰减与水色）与生存（浸没物理、溺水与氧气），清偿 `authoritative-fluid` 归档 `design.md` 里记录的五项待办，并把 `fluidEnabled` 默认值翻为 `true`。

## What Changes

- **斜水面几何**：水面按相邻水位插值成连续斜面，不再是台阶。水的顶面/侧面因角高度随邻域变化**本就无法贪心合并**，于是 `w`/`h` 两个 4-bit 字段成为冗余位——它们连同 quad packing 现有的 9 个空闲位（合计 17 bit）容纳四角高度（4×4 = 16 bit）。**quad 的 u64 格式不变**，判别条件是 `material ∈ 水集合` 且 face，零额外标志位。
- **单格流体高度**：4-bit 值 `v` 表示高度 `(v+1)/16`；`h_raw(level) = 14 - level`（源 = 14 → 15/16，level7 = 7 → 8/16），上方为流体的格取 `15`（满格，水柱内部无斜面）。角高度取该角相邻四格中流体格 `h_raw` 的**整数平均**，任一参与格上方为流体则直接取 15。全整数运算。
- **水面 pass**：新增 alpha blend 的 water pass，排在 terrain pass 之后、HiZ build 之前；深度测试开、**深度写关**；按 section 距离由远及近，**不做 per-quad 排序**；**不接 GPU culling**（走普通 `draw_indexed`）。新 `water.wgsl` 与 terrain 共享世界坐标 UV 与昼夜 tint 来源。
- **水下视觉**：相机在水中时叠全屏水色 tint 并压低远裁剪雾。判定复用第 2 项的权威 `EyeInFluid` 标志——**同一个标志同时驱动视觉与溺水**，不允许两套判定。
- **水下光衰减（只作用于天空光）**：`RegistryView` 增加 `light_attenuation`；流体透光但每格额外衰减 1，且**竖直向下也不再无损**。**列顶判定不变**（仍是「最高非空气方块」）——初稿把「列顶判定忽略流体」也列为修复项，任务组 4 评审的四配置矩阵实测证明那是**误诊**并已回退，理由见 `design.md`。BFS 结构不变，只把每步固定的扣减改成按方块查表。**方块光不变**：水与玻璃一样阻断方块光，与既有模型一致。
- **浸没标志**：`physics.Input` 增加 `BodyInFluid`（AABB 与任意流体格相交）与 `EyeInFluid`（眼睛所在格是流体），由调用方从各自的方块镜像用同一纯函数算出。**不扩 prism 编码**——prism 只携带碰撞盒，加逐格流体数组是不必要的成本。
- **水中物理**：重力衰减、垂直终端速度压低、`Jump` 从跳跃冲量改为持续上浮、水平速度乘阻力系数、入水重置摔落峰值（水中不产生摔落伤害）。全部走新 tunable。
- **溺水与氧气**：权威 `oxygen`，满值 `MaxOxygenTicks = 300`（15 秒 @ 20 tps）。`EyeInFluid` 时每 tick −1；归零后每 `DrownDamageIntervalTicks`（默认 20）扣 1 点血，**走既有伤害入口**（与摔落同路径，同样重置回血计时）。眼睛出水立即回满。**不入存档**——氧气是秒级瞬态，玩家 schema v6 不变。
- **HUD 氧气条**：写进现有 hotbar 布局，画在生命值条上方，复用同一 HUD 图集与 HUD pass，**零新 pipeline**；仅在 `oxygen < MaxOxygenTicks` 时出现。
- **清偿 F1 五项待办**：mesh registry 上限扩容（流体纳入快照）、天空光穿过流体时衰减、raycast 目标判定、出生点选取、伙伴寻路对流体的豁免。详见 Impact。
- **`fluidEnabled` 默认值 `false` → `true`**：呈现与生存补齐后，水成为默认世界的一部分。**BREAKING**：默认世界内容改变。

## Capabilities

### New Capabilities

- `fluid-presentation`：水的可观察呈现契约——斜水面几何与角高度、半透明水面与绘制顺序、水下视觉、水下光衰减。
- `fluid-survival`：浸没状态、水中物理与溺水/氧气的行为契约。

### Modified Capabilities

- `authoritative-fluid`：`fluidEnabled` 默认值改为 `true`；流体方块从「未纳入 mesh registry 快照」改为「纳入」。
- `authoritative-daylight`：**天空光的「空气是唯一透光方块」这条硬约束必须放宽**。现规格写「不透明方块 MUST 阻断传播；**当前版本空气仍是唯一透光方块**」——改为：流体透光但每格额外衰减，竖直向下也不再无损。**列顶判定那条不动**：初稿曾把「每个世界 X/Z 列严格高于最高非空气方块的空气单元 MUST 是亮度 15 的直射起点」也列为要放宽的一条，任务组 4 评审实测证伪——抬高列顶只会**减少**直射起点、从不制造黑暗，水面之上的空气照样是 15 的起点。「水下全黑」的唯一根因是「空气是唯一透光方块」，故列顶判定保持原样，本变更只改「谁能透光、透多少」。
- `voxel-visual-presentation`：**现规格明确禁止本变更要做的事**——「quad 实例格式 MUST 保持 `8` 字节，**不得增加第二个透明 pass、透明排序**或每帧材质资源创建」。water pass 正是第二个透明 pass 加按 section 距离的排序，必须显式放宽，并把放宽的边界写死（只允许这一个额外透明 pass、排序粒度只到 section、不得引入每帧动态资源、quad 实例仍 MUST 保持 8 字节）。
- `bounded-benchmark-workload`：被测进程因流体呈现与生存而改变（registry 条目加宽、quad 位布局、光照衰减查表、water pass、`StepInput` 头版本），benchmark scenario v16 → v17（新增显式迁移 `16:17`）；benchmark 的世界内容仍与注水默认值解耦，其注水门控被钉死为关闭。

**不修改 `static-block-light`**：其现规格写「光在六个轴向上 MUST 仅向 `AirID` 相邻格传播……任何其他方块即使未来被标记为透明也 MUST 阻断方块光」。水阻断方块光与既有模型**一致**（玻璃同样阻断），因此本变更不动它——水下在夜间没有直射天空光时仍然是暗的，与玻璃后方的现有行为同构。

## Impact

- **受影响包**：`engine/crates/mornlea_engine`（角高度与斜面 quad、registry `light_attenuation`、`MAX_REGISTRY_ENTRIES` 27→35、`StepInput` 浸没标志与水中积分）、`engine/crates/mornlea_client`（water pass、`water.wgsl`、水下 tint）、`engine/include` 两个头文件、`internal/nativeabi`、`internal/mesh`（水 quad 按 material 分流上传、常量随 registry 上限扩容）、`internal/assets`（流体纳入快照 ids，**删除** `FaceVisible` 的两处 `IsFluid` 补偿分支；`Opaque` 的排除是永久事实、**不得删**）、`internal/physics`（浸没标志与 Input 编码）、`internal/sim`（氧气、溺水、出生点、raycast solid 谓词）、`internal/server`（伙伴寻路豁免复核）、`internal/network`（协议 v21）、`internal/render/hud`（氧气条）、`internal/config`（默认值翻转）、`cmd/mornlea`（capture 场景）。
- **兼容性**：协议 v20 → v21（`PlayerState` 追加 `Oxygen`）；engine ABI v4 → v5（registry 布局 + `StepInput` header）；client ABI v4 → v5（water pass）；benchmark scenario v16 → v17。玩家 schema v6、区块 schema v9、世界 metadata v2、`companions.ai` schema v4 **均不变**。
- **视觉门禁**：全部 capture golden **必然重新生成**（水入画、水下变暗），这是预期变化不是回归；新增水景场景。
- **性能**：`authoritative-fluid` 留下的残余风险需复测——`Queue.Advance` 的成本正比于队列规模而非预算（约 176–256 ns/项/tick，队列约 20 万项时单独吃满 50 ms）。默认开启注水后，溃坝与挖穿大坝这类**流动前沿**场景必须实测；数值只记录，但若发现权威 tick 被无界工作阻塞则是门禁。
- **回退**：`fluidEnabled=false` 仍是行为级回退（回到 F1 的默认态）。整支 revert 回到 F1 基线；区块 schema 不变故存档不需重建。

## 非目标

- 不做水桶、取水与放水；不做「两个源相邻生成新源」的无限水源规则。
- 不做岩浆、造石与黑曜石。
- 不做水流对实体的推力与水流方向动画——斜面只是几何。
- 不做水下的独立呼吸装备、附魔或水下视野增强。
- 不做游泳姿态动画与第三人称呈现。
- 不做流体音效。
