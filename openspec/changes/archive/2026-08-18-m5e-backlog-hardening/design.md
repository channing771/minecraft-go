# Design: m5e-backlog-hardening

背景设计：`docs/superpowers/specs/2026-08-18-m5e-backlog-hardening-design.md`（brainstorm 全文，含裁决记录）。本文件是执行侧自包含工作清单——归档后它是欠账清偿的持久凭证。

## 1. 裁决要点

| 裁决 | 结论 |
|---|---|
| 考古清单处置 | 29 条全做，无放弃项（用户选「考古重建 + 完整硬化」；控制会话裁决补全清单处置） |
| `planWireStep` null 语义 | 收紧：显式 null = 字段出现，排他矩阵拒绝（与 M5D `"summary":null` 裁决同心智；`decodePlanStep` GoDoc 已承诺排他——收紧兑现承诺而非改文档迁就松散行为） |
| 结构 | 单一 change，12 任务组（E1..E12），SDD 执行 |

红线（沿用 M5D 自查）：persona/摘要不进 Planner 输入、密钥只走环境变量、模型输出视为不可信数据、golden 原字节、门禁只紧不松、协议 v19 与 schema v4 不变。

## 2. 工作项全集（39 项）

考古条目行号以 2026-08-18 main（`1b1ecb8`）为准；implementer 逐条复核，失效项记 ledger 不强改。

### 2.1 A 面：计划/寻路/模拟（10 条）

| # | 位置 | 动作 |
|---|---|---|
| A1 | `internal/sim/companion_action.go:85` + `companion_action_test.go:441` | 注释与测试名去掉「放置校验属后续任务」M5B 骨架期措辞，指向同 tick `settleCompanionPlacements` 校验链；测试更名 `...DefensiveBoundary` |
| A2 | `internal/sim/actor.go:23-24` | 伙伴背包注释改现状描述（MineHold 产物入包、Place 扣料写并置 inventoryDirty），删里程碑叙事 |
| A3 | `internal/companion/planner.go:68` | 系统提示 `[-64,319]` 改 `core.MinY`/`core.MaxY-1` 拼接 |
| A4 | `internal/companion/planner_test.go:962` | 引入 `ItemIDMax` 哨兵并以之为穷举界 + 枚举末项守护断言（与 E2 同任务） |
| A5 | `internal/companion/pathfind.go:22` ↔ `plan_types.go:33` | 寻路窗口半径与观察半径共用同一常量 |
| A6 | `planner_test.go:89-100` ↔ `pathfind_test.go:33-44` | `wantPlanError`/`wantPathError` 合并为单个通用哨兵断言 helper |
| A7 | `internal/sim/companion_mining_test.go:64-117` | 抽公共 `newCompanionMiningScene`，`ViaActions` 在其上补 action 链路 |
| A8 | 同上 `:77、155` | 删除两处 `entry.pitch = 0` 死赋值（伙伴采掘射线不用 pitch） |
| A9 | `internal/sim/companion_action.go:44` | 容量注释改「全局每 tick 至多 MaxActive 条」，注明同伙伴重复提交可饿死其他伙伴 |
| A10 | `internal/companion/plan_types.go:21` ↔ network/chat 散落字面量 | 指令/聊天上限常量同源化（并入 E7） |

### 2.2 B 面：服务端编排（10 条）

| # | 位置 | 动作 |
|---|---|---|
| B1 | `internal/server/companion_manager.go:1197` | wire 枚举注释 16..19 → 16..20，点名 v18 追加项（考古两评审员独立上报，并条） |
| B2 | 同上 `:1015-1019` | `restoredIssuerIdentity` 0x40/0x80 魔法字节补出处注释（`core.PlayerID.Valid()` UUIDv4 最小满足形态） |
| B3 | 同上 `:937-941` | `submitPathRequest` 手写三次 `math.Floor` 改复用 `standingCellOf(body.Position)` |
| B4 | 同上 `:1097-1098` | 删 M5B change「任务 7」注释引用，改现行 `restoreQueues` 行为描述 |
| B5 | `internal/server/companion_chat.go:60-63` | 删 `len(chan)` clamp 死防御（语言级恒 `len ≤ cap`） |
| B6 | `companion_manager.go:259-265` | `enqueueCommand` 防御分支广播 Accepted 的取舍补全注释（不改行为） |
| B7 | `internal/server/companion_interact.go:292-296` | `observeTickResult` mining 条目「刻意不清除」补生命周期注释（消费方 body-active gate 兜底） |
| B8 | `companion_manager.go:200-203` | `dialogueEffects` 注释如实更新（仍被 companion_dialogue_test 消费，未退役） |
| B9 | 同上 `:833-837` | issuers 空失配检查前移到 `BeginHead()` 之前，消除「队列已占槽但防御分支行为未定义」次生态；正常路径零变化（失配仅 Enqueue/restore 缺陷可达），论证以注释固化 |
| B10 | `internal/server/companion_persistence.go:576-580` | `sortCompanionBodies` 统一 `slices.SortFunc`，删 `sort` import |

### 2.3 C 面：协议/存档/客户端（9 条）

| # | 位置 | 动作 |
|---|---|---|
| C1 | `internal/storage/companion_codec.go:223-224` | schema 白名单显式列 `companionSchemaV4`，入口与 `:308` `schema >= companionSchemaV4` 前瞻口径一致（v5 落地时 v4 文件不被入口误拒） |
| C2 | `cmd/mornlea/chat.go:140-153` | `ChatEventAccepted` 显式 case 化，default 改带注释防御兜底（防新增 kind 漏 case 静默落入寻址格式） |
| C3 | `internal/storage/companion_restore_test.go:329` | 测试名/doc 的 V3 口径改 V4（断言已是 V4，属漏改） |
| C4 | `cmd/mornlea/chat.go:158` | `taskFailReasonText` 签名改收 `network.TaskFailReason`，转换上移唯一调用点 |
| C5 | `chat.go:14` + `message_companion.go:281,296` | 裸 `1024/256` 与 companion 导出常量同源化（并入 E7） |
| C6 | `chat.go:175-182` | `truncateChatLine` 补中文 doc：32 rune 出处、第 32 rune 替换 `…`、不做字节级截断 |
| C7 | `internal/network/message_companion.go:18-25` | 五个 wire 上限常量按 storage `maxCompanionFileLength` 风格补字节构成推导注释 |
| C8 | `message_companion_test.go:256,425,861` | kind 偏移表达式提取 `chatEventKindOffset` helper |
| C9 | `internal/client/chat.go:10` ↔ `cmd/mornlea/app.go:73` | client 导出容量常量供 cmd 复用，消 `[32]` 字面跨包重复；`chatLines [6]string` 的 6 补出处注释 |

### 2.4 D：契约收紧（1 项，唯一行为变化）

`internal/companion/planner.go` `planWireStep` 显式 null 视为字段出现（delta spec 场景已随 change 创建）。实现选型由 implementer 按 TDD 决定：map 中间形（`map[string]json.RawMessage` + 手工未知字段拒绝 + null 检测）或自定义 `UnmarshalJSON`，与 M5D dialogue 的 map+isJSONNull 方案同构。测试矩阵补「专属外字段为 null」负向全集；`decodePlanStep` GoDoc 同步校准。

### 2.5 E：负载 flake 治理（2 项，只改测试）

- `cmd/mornlea/benchmark_server_test.go:297` TestScenarioV7（darwin-only）：新增 `//go:build race` 辅助文件定义 `raceEnabled=true`（无 tag 文件 false），测试开头 race 下 `t.Skip`；非 race 门禁原样。
- `internal/server/tcp_integration_helpers_test.go:236` `waitIntegrationCondition`：`runtime.Gosched()` 热轮询改 `time.Sleep(500µs)` 固定退避（该 helper 21 个调用点受益，`waitForIncomingChatDepth` 另有 26 个调用点经其同享；E11 评审勘误 brief 的 47 计数）；`waitForIncomingChatDepth` 条件改 `>= want`；60s 窗不抬。

### 2.6 F：已知精确项（6 项）

1. 交叉锁测试：`buildPlanPlaceBlocks()` 全集逐条 `BlockDrop(B)==(I,true)`（防「放置扣 X、采掘产 Y」复制/丢失漂移）+ A4 `ItemIDMax`。
2. `ResolvedPersona` tag 单测锁：`Definition.Persona` 序列化可见、`ResolvedPersona` `json:"-"` 永不落盘（反射级断言）。
3. `ErrDialogueInvalidResponse` 命名拆分：请求构造错误与响应解码错误两个哨兵，调用方可区分。
4. ChatEvent 共享文本槽 decode 补无效 UTF-8 wire 突变用例（fuzz 已覆盖，显式矩阵钉住）。
5. chat 呈现负向断言收紧至 29 rune（M5D D7 遗留）。
6. 阶段验收哑参数风格 + parity 投影复用（M5D D8 遗留）。

### 2.7 流程修复（1 项）

backlog 沉淀纪律：归档前 ledger 未决项全文誊入 proposal.md「延期与放弃」节。E12 执行。

## 3. 依赖方向约定（E7/C9）

- 常量同源优先 `internal/network`、`cmd/mornlea` 复用 `internal/companion` 导出常量（考古确认 network 已导入 companion）；若 archcheck 否决该方向，退为「两侧常量 + 跨包相等断言」交叉锁形态，不强行下沉新公共包。
- C9 `internal/client` 导出容量常量供 `cmd/mornlea`，cmd→client 既有方向，无新增反向依赖。

## 4. 任务分组与 RED 预期

| 任务 | 内容 | RED |
|---|---|---|
| E1 | D（null 收紧）+ GoDoc | 有（null 负向矩阵先红） |
| E2 | F-1 交叉锁 + A4 | 有（交叉锁先红） |
| E3 | A1、A2、A3、A5、A8、A9 | 无（注释/常量/死赋值；A3/A5 编译级验证） |
| E4 | A6、A7、C8 | 无（测试重构，断言全集保持绿） |
| E5 | B1、B2、B4、B5、B6、B7、B8、B10 | 无（B5 可先白盒断言 clamp 从未生效） |
| E6 | B3、B9 | 无（防御路径不可达，推理+注释+全量回归锁定） |
| E7 | A10、C5、C7 | 有（先写跨包常量相等断言再同源化） |
| E8 | C1、C3 | 无（C1 以 v5 假想 schema 单测锁定前瞻口径） |
| E9 | C2、C4、C6、C9 | 无（C2 以既有呈现测试锁定等价） |
| E10 | F-2..F-6 | F-2 有（tag 反射断言先红）；其余测试级 |
| E11 | §2.5 flake 两项 | 无（race 下单测/整包复跑验证） |
| E12 | 全量门禁 + progress.md + backlog 沉淀 + 基线核对 | — |

## 5. 风险

- 考古行号漂移：implementer 复核，失效记 ledger。
- E6/B9 触及生产代码但均在防御/复用层，行为等价由全量回归 + 任务评审双锁。
- 39 项 ≈ M5D 体量，12 组控制单任务评审半径；修复循环 ≤5 轮/任务。
