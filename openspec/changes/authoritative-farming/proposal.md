## Why

生存循环目前只有一半:有摔落、溺水、伤害与死亡结算,却**没有任何可消耗与恢复的资源**——自动回血无条件生效,玩家除了别摔死之外没有任何经营压力。补齐生存循环的第一步是**产出端**:先有可种可收的东西,下一个变更才能把饥饿接上去消费它。

先做农业而不是先做饥饿,是因为顺序反过来会制造一段**游戏不可玩的窗口**:饿了没东西吃,只能等死。

## What Changes

- 新增 **10 个稳定方块编号**:`FarmlandDryID` / `FarmlandWetID` 与 `WheatStage0..7ID`。沿用流体的「每阶段一个编号」模式,因而**区块 schema 不变**——阶段本就存在方块编号里,采掘表、碰撞表与 mesh registry 快照天然覆盖。
- **BREAKING(内部容量)**:mesh registry 条目上限 `35 → 48`。当前方块编号 `0..34` 恰好占满 35 个,加任何方块都必须先扩容。Rust `MAX_REGISTRY_ENTRIES`、Go `nativeMaxRegistryEntries` 与 `maxNativeInputBytes` 三处必须同批移动。
- 新增 **4 个物品**:`ItemStoneHoe` / `ItemIronHoe`(各含损坏形态)、`ItemWheatSeeds`、`ItemWheat`,锄头接现有耐久与固定配方体系。
- 新增**翻地命令**(协议 v21 → **v22**)。手持锄头对泥土/草作用,目标格变耕地并扣一点耐久。这是三个农业动作里**唯一**需要新命令的——种植复用放置、收获复用采掘。
- **随机 tick 生长**:每权威 tick 按 `(ChunkKey, sectionY)` 全序枚举活动兴趣范围内的已加载 section,每 section 用纯哈希抽 3 格;抽中作物且满足条件则推进一阶段。成本正比于 section 数,**与作物数量无关**。
- **生长条件为露天与湿润**:露天读既有 `heights` 列顶图,湿润读水平切比雪夫距离 ≤ 4、同层或上一层的流体方块。两者都是**纯查询**,不写这两个系统的状态。
- **交叉斜面植物几何**:每株作物出 4 片 quad(两条对角线 × 正反两面),判别靠 `material ∈ 植物集合`,走既有 terrain pass 的 alpha cutout。**quad 位布局零变更**(bit 63 保持空闲),**零新 pass**。这条几何路径是通用基础设施,以后的花草树苗直接复用。
- **耕地高 15/16**,作物零碰撞体。prism 每格本就携带任意 AABB 数组,无需任何 FFI 编码变更。
- **纹理全部程序化自绘**,视觉按 MC 约定看齐但非像素级一致——仓库不引入任何二进制美术资源。

## Capabilities

### New Capabilities

- `authoritative-farming`: 耕地与翻地、种子放置前置条件、作物随机 tick 生长的确定性与成本边界、露天与湿润判定、收获掉落规则。
- `plant-visual-presentation`: 交叉斜面植物几何——每格 4 片 quad、按 material 判别、走既有 cutout、不贪心合并、光照取上方相邻格。

### Modified Capabilities

- `authoritative-crafting`: 固定配方集合由八条扩为十条,加入石锄与铁锄。
- `authoritative-mining`: 采掘时长与掉落等级表加入作物与耕地条目。
- `tool-durability`: 现行条文把耐久消耗绑定在「目标方块被实际移除之后」,而翻地不移除方块;需覆盖「翻地成功同样扣减恰好一点耐久」,拒绝路径仍不扣。
- `common-block-materials`: 缺失玩家材料包加入初始种子,使玩家在草丛存在之前也能开始耕种。
- `bounded-benchmark-workload`: benchmark scenario v17 → **v18**(实现期实测判定,见下)。

**不需要 delta 的既有能力**(与 F1 加入 8 个流体编号时同理):`common-block-materials` 的「稳定材料注册表」与「权威放置采掘与掉落」是 M4 材料批次的历史快照,追加新编号不改变其断言;「协议与存档语义版本」同样记录的是 v15/v6/v8 那次上线,本变更的协议 v22 由 `internal/archcheck` 的基线版本门禁覆盖;「cutout 方块语义」只约束玻璃与树叶,未声明排他。`voxel-visual-presentation` 的有界渲染成本条文植物全部满足(仍走既有 terrain pass、仍 8 字节),边界由新能力 `plant-visual-presentation` 自陈。

## Impact

- **受影响包**:`internal/core`(方块与物品编号、掉落表、配方)、`internal/sim`(生长阶段推进、翻地命令、种植与收获校验)、`internal/mesh` 与 `internal/assets`(registry 扩容、植物 material 集合、程序化纹理层)、`internal/physics`(作物零碰撞体、耕地 15/16)、`internal/network`(协议 v22 与新命令 kind)、`internal/world`(`heights` 只读查询)、`internal/config`(生长与湿润 tunable)、`internal/archcheck`(基线版本与依赖登记)、`engine/crates/mornlea_engine`(交叉面几何)、`engine/crates/mornlea_client`(着色器顶点变形)、`cmd/mornlea`(手持锄头时「使用」键发翻地命令)。
- **兼容性**:协议 v21 → v22。**区块 schema v9、玩家 schema v6、`companions.ai` v4、世界 metadata v2、engine/client ABI v5 全部不变**——农业状态完全落在方块编号里。benchmark scenario **v17 → v18**:实现期实测确认本变更改变了被测进程本身——mesh registry 条目上限 35 → 48(实际烘焙条目 35 → 45,每次 mesh 调用的 FFI 输入 910 → 1170 bytes)、合成面板 8 → 10 行使 Hotbar HUD 固定上传布局移动(quad 容量 238 → 247、glyph offset 11776 → 12288、总容量 45376 → 45888 bytes、空聊天帧每帧实际写入 11776 → 12288 bytes)、权威 tick 新增每 tick 枚举全部区段的 `advanceCrops` 阶段(满编 200 区块实测约 113 µs/tick)。其中「改变固定 GPU 上传布局、offset 与每帧写入字节数」正是主规格判定 v15 → v16 时用的同一条条文。benchmark 的固定输入与被测世界一格未动(仍七名远端玩家、零伙伴、不注水、不含农业方块),唯一显式迁移改为 `17:18`,`16:17` 退为归档证据。
- **并发**:生长推进在单写者权威 tick 内串行,与流体、掉落物、熔炉同构,不引入新 goroutine 或锁。
- **性能**:每 tick 触及 = `section 数 × 3`,由兴趣范围约束;湿润扫描是 `9×9 × 抽中的耕地数`,全部因子为常数或已有上界,**不随农田规模增长**。
- **回退**:整支 revert 即可。农业方块编号一旦写入区块,回退后会被读成未知编号——与既有未知方块处理路径一致,不产生存档损坏。

## 非目标

- 不做饥饿、饱食度与进食——那是下一个变更,本变更只交付产出端。
- 不做多种作物、骨粉、踩踏破坏耕地、干耕地退回泥土。
- 不做水冲毁作物(要改流体规则)。
- 不做地下农场——服务端 **MUST NOT 计算光照**,只能用列顶判露天。
- 不做伙伴种田与收获。
- 不引入贴图资源管线——纹理继续程序化生成。
