# 任务

## 1. 核心状态、疲劳表与回血门控

- [x] 1.1 `internal/core` 新增 `ItemBread`(末项前追加,`ItemIDMax` 随之;`RegisteredItem`/堆叠 64/不可放置)、食物表 `FoodValue(item) (hunger uint8, saturationMilli uint16, ok bool)`(只有面包 5/6000)、recipe ID `11`(3 小麦 → 1 面包,既有 1..10 不位移)。**按 Ruling 27 普查**以 `ItemWheat`/`RecipeIronHoe` 为末项的断言与「末项+1」字面值。验证:`go test ./internal/core -race -count=1`
- [x] 1.2 `internal/sim` 三层状态进 `playerState`(`hunger uint8`、`saturationMilli uint16`、`exhaustionMilli uint16`),`applyExhaustion(milli)` 纯函数:累积 ≥4000 归零并扣饱和(饱和 0 则扣饥饿,饥饿 0 不动)。**穷举式单测**覆盖三层边界。验证:`go test ./internal/sim -race -count=1`
- [x] 1.3 疲劳来源表接进五个判定点(起跳 / 游泳按位移 / 采掘完成 / 翻地完成 / 回血每 HP),**只在玩家路径**,每个判定点一条用例(成功累积 + 拒绝/中断不累积)。起跳信号若物理侧没有现成导出,以「`Jump` 输入且上 tick `OnGround`」在 sim 判定,写明。验证:`go test ./internal/sim ./internal/physics -race -count=1`
- [x] 1.4 回血门控:`advanceHealthRegen` 入口加 `hunger >= 18`;每回 1 HP 累积 6000。既有四条回血用例在饥饿 ≥18 夹具下原样通过。验证:`go test ./internal/sim -race -count=1`
- [x] 1.5 饥饿伤害:饥饿 0 时每 `StarvationDamageIntervalTicks`(默认 80)经 `applyDamage` 扣 1,`health <= 1` 停。验证:`go test ./internal/sim -race -count=1`
- [x] 1.6 tunable:`StarvationDamageIntervalTicks`、`ExhaustionThresholdMilli`、`RegenHungerThreshold` 进 `sim.Tunables` + `internal/config` `Fields()` 钳制 + archcheck 禁导出清单。验证:`go test ./internal/config ./internal/archcheck -race -count=1`
- [x] 1.7 覆盖 `authoritative-hunger` 前四条 Requirement 的 Scenario(三层 4 条、疲劳表 3 条、回血门控 3 条、饥饿伤害 2 条)与 `authoritative-health` MODIFIED 的两条新 Scenario。**饥饿 17 与 18 成对夹具;每条「消耗」用例跨过阈值并断言精确值。**
- [x] 1.8 变异验证(三条):疲劳阈值改为 8000 → 「疲劳先消耗饱和度」红;去掉回血门控 → 「饥饿值 17 不回血」红;饥饿伤害去掉 `health <= 1` 停止 → 「止于一点生命」红。

## 2. 玩家存档 v6 → v7

- [x] 2.1 `storage.PlayerSave` 加三字段;`currentPlayerSchema` 6 → 7;v6 读入迁移 `Hunger=20, Saturation=5000, Exhaustion=0`;v5→v6→v7 链仍可读;v8+ `ErrFutureVersion`。wire golden 与 fuzz 同步。验证:`go test ./internal/storage -race -count=1`
- [x] 2.2 sim ↔ storage 接线:登录读入三层、保存写出三层;重生回初值。覆盖「跨重启保留」「旧存档按初值迁移」「重生后饥饿回满」。验证:`go test ./internal/server ./internal/sim -race -count=1`
- [x] 2.3 **同组同步 `AGENTS.md`/`CLAUDE.md` 的玩家 schema 版本号**(两份逐字节相同)。验证:`go test ./internal/archcheck -count=1`
- [x] 2.4 变异验证:v6 迁移把 `Hunger` 初值改 0 → 「旧存档按初值迁移」红;保存路径漏写 `Saturation` → 「跨重启保留」红(夹具饱和非零)。

## 3. 协议 v23 → v24

- [x] 3.1 `PlayerInput.Eating bool`、`PlayerState.Hunger uint8`(`Validate` 拒 >20);`ProtocolVersion` 24(main 已是 v23);wire golden 与 fuzz 同步;v23 握手被拒。**Ruling 27 普查**尾部偏移与魔数(`22`/`23`/`14`/`15` 等字面值 + 末项名)。验证:`go test ./internal/network -race -count=1`
- [x] 3.2 sim 填 `PlayerState.Hunger`、读 `PlayerInput.Eating`;客户端镜像接收。验证:`go test ./internal/sim ./internal/client -race -count=1`
- [x] 3.3 **同组同步基线文档协议版本号**。验证:`go test ./internal/archcheck -count=1`
- [x] 3.4 变异验证:`Validate` 去掉 >20 拒绝 → 对应 golden/invalid 用例红;`PlayerState` 编码漏写 `Hunger` → 往返用例红。

## 4. 进食状态机

- [x] 4.1 `playerState.eating{slot, item, progress}`;开始/推进/结算/中断四类规则按 design D5;结算单 tick 原子(扣 1、饥饿 +5 ≤20、饱和 +6000 ≤ 饥饿×1000)。`EatingTicks`(默认 32)进 tunable。验证:`go test ./internal/sim -race -count=1`
- [x] 4.2 覆盖「进食」Requirement 五条 + 「食物表」两条 Scenario;**中断用例在 progress ∈ (0,32) 触发,断言面包数精确不变**;受伤与死亡中断各一条。验证:`go test ./internal/sim -race -count=1`
- [x] 4.3 Memory/TCP parity 脚本:登录(夹具播种饥饿 12、满血——非满血的自然回血每 HP 累积 6000 疲劳会在 32 tick 内污染读数;跳跃 +50/次要 80 次才扣 1 饱和,脚本不现实)→ 夹具直给 3 小麦(材料包只有种子)→ 合成面包 → 选中 → 长按 32 tick → 从 wire 上的 `PlayerState.Hunger` 读回升,两传输 transcript 逐字段比。验证:`go test ./internal/server -race -count=1`
- [x] 4.4 伙伴不接:`companionState` 无三层状态、疲劳表判定点不在伙伴路径,各一条断言(能在有人接线时变红)。验证:`go test ./internal/server ./internal/sim -race -count=1`
- [x] 4.5 变异验证(三条):去掉 `(slot,item)` 核对 → 「切换栏位不扣料」红;结算放到 progress==31 → 「到时结算」红(断言精确 tick);饱和不钳 → 「饱和度不超过饥饿值」红。

## 5. 客户端输入与 HUD

- [x] 5.1 `cmd/mornlea` 使用键:手持食物时置 `Eating`(按住 true / 松开 false),其余手持物行为不变;无本地预测。覆盖「手持面包置位 / 手持小麦不置位 / 手持锄头仍翻地」。验证:`go test ./cmd/mornlea -race -count=1`
- [x] 5.2 `internal/render/hud`:`paintHotbarDrumstick`(空/满两列,与 `paintHotbarHeart` 同法)+ `appendHungerBar`(右下镜像生命条,10 格半格粒度,满时仍显示)。复用同一图集与 pass,零新 pipeline;**若 `maxHotbarQuads` 因此移动,按 `bounded-benchmark-workload` 升 scenario 并在报告写明**。验证:`go test ./internal/render/hud -race -count=1`
- [x] 5.3 覆盖「界面显示权威饥饿值」;HUD 单元测试断言至少两个不同饥饿值给出**不同**的填充(不只「非空」)。**本组跑 capture 比对并记录哪些场景变、不重生成**(重生成归组 7)。验证:`go test ./internal/render/hud ./cmd/mornlea -race -count=1`
- [x] 5.4 变异验证:鸡腿填充比例写死 → HUD 用例红;客户端食物分支去掉 → 「手持面包置位」红。

## 6. 端到端

- [x] 6.1 `internal/server` 端到端(Memory):从农业闭环接力——缺失玩家登录(20/5000/0,零播种)→ 真实摔落 10 格(−7 血)→ 自然回血每 HP 累积 6000 疲劳,6 次回血把饱和烧光并把饥饿拉到 16(跳跃 +50/次需 500+ 次,不现实;第 6 次回血在 4000 阈值上连跨两格,18 直接到 16,17 只是 `applyExhaustion` 循环内的中间态,「17 不回血」由组 1 的 sim 成对夹具覆盖)→ 回血停摆 220 tick 逐 tick 精确不变 → 夹具直给 3 小麦(农业闭环已证小麦可得)→ 合成面包 → 长按 32 tick → 饥饿 16 → 20 → 回血恢复。**每步精确数列、全部从 wire 读、每次回血钉在算出的 tick 上**。验证:`go test ./internal/server -race -count=1`
- [x] 6.2 变异验证:去掉回血门控 → 端到端「跳到饿后回血停止」那一步红;面包饥饿值改 0 → 「进食后回升」红。

## 7. 收尾门禁、golden 与归档准备

- [ ] 7.0 **`git fetch && git rebase origin/main`**;若 `mornlea-texture-pack` 已合并,记录其 golden 基线;解 `AGENTS.md`/`CLAUDE.md` 与 `config.Fields()` 冲突(两份文档逐字节相同)。
- [ ] 7.1 capture golden:先比对列出变化场景(应只有含 HUD 的场景),逐场景说明差异来源(饥饿条入画),再重生成;其余逐字节不变。验证:`make build && ./bin/mornlea --capture <tmp>` EXIT=0
- [ ] 7.2 `make rust && make rust-check`;`go test ./... -race -count=1`(已知既有红灯:`TestChatCommand...AtTickBoundary` 单跑隔离缺陷、两条 90s 超时、dialogue 双负载抖动、`cmd/mornlea` 并发 GPU 包级超时——不修不改阈值,如实记录);`go vet ./...`;`gofmt -l .`;`openspec validate --all --strict --no-interactive`。
- [ ] 7.3 核对 tasks 与三个 delta spec 逐条一致,**Requirement 正文限定词逐个核承重**(农业 Ruling 39);偏离先改产物。按 Ruling 46 机械核对两份 MODIFIED 的 Scenario 集合为主规格超集。
- [ ] 7.4 遗留清单落纸(design.md 1–8 + 执行期新增);`docs/notes/progress.md` 里程碑条目(不以「当前」开头);基线文档能力描述同步(只陈述现存事实)。
- [ ] 7.5 若 HUD 布局或 tick 热路径读数变化,运行 benchmark/perfcheck 并记录;scenario 版本按 5.2 的判定。
