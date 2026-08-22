# 设计:饥饿、食物与进食

## D1 三层状态全用定点整数,不用浮点

**决策**:`Hunger uint8`(0..20)、`Saturation uint16`(千分位,≤ Hunger×1000)、`Exhaustion uint16`(千分位,阈值 4000)。

**理由**:MC 的疲劳与饱和是 float;本项目的确定性纪律(F1/F2/农业三次变更反复验证)是「权威推进不用浮点」——浮点在 Memory/TCP parity 与跨平台重放下逐位一致不可证。千分位整数表达 MC 的全部数值(0.05、0.005、6.0)无损。

**被否决的替代**:`float32` + 固定 epsilon——每条 parity 断言都要带容差,且容差本身成为新的假绿来源。

## D2 疲劳来源是一张固定表,只在玩家路径调用

| 动作 | 千分位 | 判定点 |
|---|---|---|
| 跳跃 | 50 | sim 侧判定 `input.Jump && wasOnGround && !input.BodyInFluid && 步末 !OnGround`(任务组 1,Ruling 3):`physics.Step` 对水中 `Jump` 走持续上浮而非起跳冲量,故排除浸没以免与游泳重复计费;撞低天花板的跳不计费是「步末离地」判据的代价,否则按住跳跃可逐 tick 刷疲劳 |
| 游泳 | 10 / 格 | `BodyInFluid` 且水平位移 > 0,按位移累加 |
| 采掘完成 | 5 | `completeMining` 成功路径 |
| 翻地完成 | 5 | `executeTillSoil` 成功路径 |
| 自然回血 | 6000 / HP | `advanceHealthRegen` 每回 1 HP |

**理由**:集中在一张表,新增来源是加一行;每个判定点都在既有的**成功路径**上(拒绝/中断不累积),与「拒绝路径不消耗耐久」同构。伙伴不调用该表——与 F2 氧气只对玩家同构,加断言钉住。

**冲刺与攻击不存在**,不列;存在时加行即可。

## D3 回血门控放在 `advanceHealthRegen` 入口,而非改它的计时

`advanceHealthRegen` 的计时逻辑不动,只在入口加 `Hunger >= 18` 前置;每回 1 HP 加 6000 疲劳。**饥饿值 < 18 时计时照常累积**(MC 同),饥饿回到 18 时若计时已满立即回血。

**理由**:最小改动;既有四条回血 Scenario 在饥饿 ≥ 18 的夹具下原样成立(MODIFIED delta 把它们整块带回)。

## D4 饥饿伤害走既有伤害入口,止于 1 HP

每 80 tick(`StarvationDamageIntervalTicks`,tunable)经 `applyDamage` 扣 1;`Health <= 1` 时不扣。走既有入口意味着自动触发「确认伤害红色边缘」与回血计时重置——与 F2 溺水伤害同构(Ruling:溺水走既有入口的变异曾证实这是承重的)。

**不致死**是 MC 普通难度;困难难度(致死)记遗留。

**重生回初值的「重生」= 死亡后重生**(`settleDeath`)。`beginReset`(掉出世界/卡住重置)不经死亡结算、不掉背包,若在此回满饥饿即成免费进食途径,故**不回满**(任务组 1,Ruling 5)。`starvationTicks` 在 `health <= 1` 时冻结而非照推。

## D5 进食是采掘同构的持续输入状态机

`PlayerInput.Eating bool`(与 `Mining` 同形);`playerState.eating{slot, item, progress}`。

- 开始:`Eating && 选中物品 ∈ 食物表 && Hunger < 20` → 记录 `(slot, item)`,progress=0;
- 推进:每 tick `Eating && 选中 slot 未变 && 该格物品 == 记录的 item` → progress++;
- 结算:progress == 32 → 单 tick 原子:扣 1、`Hunger = min(20, Hunger+5)`、`Saturation = min(Hunger×1000, Saturation+6000)`、progress=0;
- 中断:任一条件不满足 / 受伤 / 死亡 → progress=0,**不扣料**。

**为什么记录 `(slot, item)`**:否则 31 tick 时切格会「吃 A 扣 B」;MC 的进食也绑定开始时的物品。

**不新增 `RejectReason`**:持续输入没有命令级拒绝,与采掘一致。饥饿满时「不推进」是静默的——客户端按 HUD 自己判断。

## D6 协议只上线饥饿值(v23 → v24)

`PlayerState.Hunger uint8`;饱和与疲劳不上线。**理由**:HUD 只需饥饿;少一个 wire 字段少一份 golden/fuzz/对照;MC 客户端也不显示饱和(只有抖动提示,记遗留)。

**F2 教训**(Ruling 27):`PlayerState` 追加字段后,**同时 grep 末项名与末项+1 字面值**普查尾部偏移与魔数断言。

## D7 玩家存档 v6 → v7,三层全持久化

MC 持久化三层;不持久化疲劳会让「重登即清疲劳」成为无成本操作。v6 读入迁移:`Hunger=20, Saturation=5000, Exhaustion=0`(MC 新玩家初值);既有 v5→v6 链保持可读;v7 以上 `ErrFutureVersion`。重生回初值。

## D8 HUD 鸡腿程序化画在 `hud/atlas.go`,不进 `internal/assets`

与 `paintHotbarHeart` 同处同法。**理由**:(a) 并行变更 `mornlea-texture-pack` 的主战场是 `internal/assets` 的 layer 与 atlas,HUD 图集不在其范围——避开冲突;(b) 爱心/氧气条的既有先例就是这么做的。饥饿条复用爱心的列尺寸(16 px × 10 格),HUD 固定上传布局的 quad 容量**不变**——若实现期实测 `maxHotbarQuads` 移动,按 `bounded-benchmark-workload` 条文升 scenario(proposal Impact 已留口)。

饥饿满时**仍显示**(常态资源,与 MC 同);氧气条「未满才出现」是因为它是异常态。

## D9 与 `mornlea-texture-pack` 的冲突排序

两者的交集只有三处,全部可控:

| 交集 | 策略 |
|---|---|
| capture golden(HUD 场景) | PNG 无法三方合并。本变更 golden 放**最后一组**,组前 `rebase origin/main`;材质包若已合并,在其基线上重生成并逐场景说明;若未合并,材质包那边在其收尾重生成 |
| `AGENTS.md` / `CLAUDE.md` 基线段 | 后合者解冲突,两份保持逐字节相同(archcheck 兜底) |
| `internal/config` `Fields()` | 不同行,文本合并即可 |

本变更**不碰** `internal/assets`、不改任何 layer、不改 mesh/shader。

## 遗留与简化清单

每条写清**是什么 / 为什么这次不做 / 后续如何承接**;执行期间新出现的简化一律追加。

| # | 简化 | 为什么这次不做 | 后续如何承接 |
|---|---|---|---|
| 1 | 只有面包一种食物 | YAGNI;一种足以验证三层机制与进食状态机 | 食物表加行;肉类需生物,熟食需熔炉食谱 |
| 2 | 无进食动画、音效、进度 HUD | 呈现层;进度 HUD 要新 HUD 元素 | 复用采掘进度条的呈现形状 |
| 3 | 饱和与疲劳不上线 | HUD 只需饥饿;MC 客户端也只在饱和归零时抖动图标 | 若要抖动提示,`PlayerState` 加 `SaturationZero` 一位 |
| 4 | 不做困难难度饿死 / 和平难度回满 | 无难度系统 | 难度系统交付时按难度分支 `applyStarvation` |
| 5 | 伙伴不饿不吃 | M5 范围;伙伴无疲劳来源 | 伙伴接三层状态 + 疲劳表 + 自动进食计划步骤 |
| 6 | 冲刺/攻击疲劳缺席 | 动作不存在 | 存在时疲劳表加行 |
| 7 | 饥饿条图标程序化,与材质包风格不对齐 | 材质包不覆盖 HUD 图集;HUD 贴图管线是独立议题 | 材质包 v2 若覆盖 HUD 图集,鸡腿与爱心一起换 |
| 8 | 饥饿值 < 18 时回血计时仍累积 | 与 MC 同;改为冻结计时要动既有回血状态机 | 若要冻结,`advanceHealthRegen` 入口改为不推进计时 |
| 9 | 既有:`playerPersistence.Flush` 的 `attempted` 去重键含 revision、重派递增 revision,「快照与存档永不相等」时只能靠 ctx 终止(关服 Flush 可能自旋) | 先于本变更存在;本组三字段在 `save`/`matchesSave`/`playerSnapshotsEqual` 三处对称,未引入恒脏态 | 独立修复:去重键去掉 revision 或给 Flush 加「连续 N 次重派无进展即放弃」 |
| 10 | 打开容器界面(箱子/熔炉)或视野尚未就绪(`hasView` 为假)时仍可继续进食 | spec 的中断清单只列松手/切格/受伤/死亡;采掘有 `viewContainer`/`hasView` 中断是因为采掘要瞄准,进食不需要;MC 打开 GUI 取消进食属呈现层惯例 | 若要对齐 MC,`advanceEating` 的中断条件加 `session.viewContainer`(与 `hasView`),并补「开箱中断不扣料」Scenario |

## 验证策略

- **每组强制变异验证**,守卫自证会红;判据:**存在性 vs 位置性**。
- 本变更的高危假绿点:**疲劳阈值夹具**——若夹具一次只累积远小于 4000 的量,扣不扣饱和读数相同;每条「消耗」用例必须跨过阈值并断言精确值。
- **进食状态机**的中断用例:夹具必须在 progress 在 (0, 32) 内时触发中断,并断言面包数**精确不变**(不是「≥0」)。
- 回血门控:夹具饥饿 17 与 18 **成对**(同夹具只改一个值)。
- `PlayerState`/`PlayerSave` 追加字段后按 Ruling 27 普查尾部偏移与魔数(末项名 + 末项+1 字面值)。
- 门禁按退出码;变异前先提交;capture 比对是 HUD 组与收尾组的门禁(农业 Ruling 42:改 HUD 布局的组必跑 capture)。
