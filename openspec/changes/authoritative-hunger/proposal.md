## Why

`authoritative-farming` 交付了生存循环的**产出端**(可种可收的小麦),但消费端仍然空着:自动回血无条件生效,玩家没有任何需要经营的资源,小麦收下来无处可用。本变更把**饥饿**接上去——回血变成有代价的,小麦变成有用的。

## What Changes

- **三层权威饥饿状态**(MC 完整模型):`Hunger`(0..20,显示层)、`Saturation`(缓冲层)、`Exhaustion`(累积层,满 4.000 归零并扣 1 饱和,饱和为 0 则扣 1 饥饿)。**全部定点整数**(千分位),不用浮点——跨平台逐位一致。
- **疲劳来源固定表**(MC 数值对齐,千分位):跳跃 50、游泳 10/格(复用 F2 `BodyInFluid`)、采掘完成 5、翻地完成 5、**自然回血 6000/HP**。
- **回血门控**:`Hunger >= 18` 才自然回血,每回 1 HP 加 6000 疲劳——既有「未受伤时自动回复」从无条件变为有代价。
- **饥饿归零**:每 80 tick 经**既有伤害入口**扣 1 血,**HP ≤ 1 时停止**(MC 普通难度,不死)。
- **进食(MC 式长按 32 tick)**:`PlayerInput` 加 `Eating` 持续输入位,权威状态机与采掘同构——记录开始时的 `(slot, item)`,每 tick 核对,松手/切格/物品变化/受伤/死亡即中断不扣料;第 32 tick 单 tick 原子结算(扣 1、饥饿 +5 ≤20、饱和 +6.000 ≤ 饥饿)。饥饿已满不推进。
- **食物**:只有 `ItemBread`(饥饿 +5 / 饱和 +6.0);固定配方 **11**:3 小麦 → 1 面包。
- **协议 v23 → v24**(main 合入 `rust-engine-lod-shell` 后已是 v23):`PlayerInput.Eating`、`PlayerState.Hunger`;饱和与疲劳**不上线**。
- **玩家存档 schema v6 → v7**:三字段持久化;v6 读入按 `Hunger=20 / Saturation=5.000 / Exhaustion=0` 迁移;重生饥饿回满。
- **HUD 饥饿条**:右下、快捷栏上方,10 个程序化鸡腿(画在 `hud/atlas.go`,与爱心同处,**不进 `internal/assets` 的 layer**),复用同一 HUD 图集与 pass,饥饿满时仍显示。
- 客户端「使用」键手持食物时置 `Eating`,不做任何本地预测。

## Capabilities

### New Capabilities

- `authoritative-hunger`: 三层饥饿状态的权威推进、疲劳来源、回血门控与饥饿伤害、进食状态机、食物与配方、存档与协议演进、HUD 呈现。

### Modified Capabilities

- `authoritative-health`: 「未受伤时生命值自动回复」从无条件改为受饥饿门控(≥18)且每 HP 消耗疲劳;新增饥饿伤害经既有入口且止于 1 HP。
- `authoritative-crafting`: 固定配方集合由十条扩为十一条,加入面包。

**不需要 delta 的既有能力**:`common-block-materials` 的「协议与存档语义版本」是 v15/v6/v8 那次上线的历史快照,本变更的协议 v23 / 玩家 schema v7 由 `internal/archcheck` 基线版本门禁覆盖;`voxel-visual-presentation` 的有界渲染成本条文饥饿条全部满足(复用 HUD pass 与图集,与 F2 氧气条同构),边界由新能力自陈;`authoritative-farming` 不变(面包只消费其产物)。

## Impact

- **受影响包**:`internal/core`(食物表、配方 11)、`internal/sim`(三层状态、疲劳表、回血门控、饥饿伤害、进食状态机)、`internal/physics`(起跳检测供疲劳表,若尚无现成信号)、`internal/network`(协议 v23)、`internal/storage`(玩家 schema v7 与 v6 迁移)、`internal/config`(tunable `Fields()`)、`internal/render/hud`(饥饿条)、`cmd/mornlea`(使用键食物分支)、`internal/archcheck`(基线版本)。
- **兼容性**:协议 v23 → v24;玩家 schema v6 → v7(v6 只读兼容迁移,v7 以上按 `ErrFutureVersion` 拒);区块 schema v9、`companions.ai` v4、世界 metadata v2、engine ABI v6、client ABI v7、benchmark scenario v18 **不变**(本变更不动 HUD 固定上传布局的 quad 容量——饥饿条复用爱心的列与尺寸;若实现期实测布局移动,按 `bounded-benchmark-workload` 条文升版)。
- **并发**:饥饿推进与进食在单写者权威 tick 内串行,不引入 goroutine 或锁。
- **性能**:每玩家每 tick O(1);无全局扫描。
- **与并行变更 `mornlea-texture-pack` 的关系**:本变更**不碰** `internal/assets`、不改 layer;饥饿条图标程序化画在 HUD 图集。两者都会改 capture golden(HUD 场景)与基线文档——**后合并者在对方基线上重生成 golden 并解文档冲突**,本变更的 golden 工作放在最后一组且组前 rebase 到 main。
- **回退**:整支 revert;已按 v7 写入的玩家存档无法被 v6 代码读取,需清玩家存档(与既有 schema 升版先例一致)。

## 非目标

- 不做面包以外的食物、不做烹饪/熔炉食谱。
- 不做进食动画、音效与进食进度 HUD。
- 不做饱和/疲劳的上线同步(HUD 只需饥饿)。
- 不做 MC 困难难度的饿死;不做和平难度的饥饿回满。
- 不做伙伴饥饿与伙伴进食。
- 不做冲刺、攻击等尚不存在的疲劳来源。
