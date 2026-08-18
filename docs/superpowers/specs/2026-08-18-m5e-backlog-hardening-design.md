# M5E 欠账硬化（m5e-backlog-hardening）设计

日期：2026-08-18 · 基线：M5D（origin/main `1b1ecb8`，协议 v19、companions.ai schema v4）
流程：brainstorming → 本设计 → writing-plans → OpenSpec change → subagent-driven-development

## 1. 目标

一次性清偿 M5B/M5C/M5D 三个里程碑终审遗留的全部打磨欠账：

- 考古重建的 29 条 minor（原清单随 ledger 覆盖丢失，2026-08-18 由三个只读评审子代理对当前 main 重建并逐条核实行号；三面各报 10 条，服务端与协议面撞条 1 条去重）；
- 一项契约收紧：`planWireStep` 把 JSON 显式 `null` 视为字段出现（详见 §5）；
- 双实现交叉锁测试：`buildPlanPlaceBlocks()` 与 `BlockDrop()` 两张独立维护表的一致性；
- M5D 延期五项（M5D ledger 终审汇总原文）；
- 负载 flake 治理两项（TestScenarioV7、TestChatCommandAddresses，均只改测试）；
- 流程修复：里程碑 backlog 必须沉淀进 git 跟踪的 change 产物，不再只活在被覆盖的 ledger 里。

## 2. 非目标

- 不改协议（v19 保持）、不改存档 schema（v4 保持）、不改任何 wire 格式与长度上限、不动视觉 golden 与 benchmark scenario（v16 保持）。
- 不新增功能、不引入新 message kind、不触碰 Planner/Dialogue 并发模型。
- 不做 M6 方向性工作；不做与本清单无关的重构。

唯一的行为变化见 §5，其余全部条目为零可观察行为变化的注释/命名/测试/常量同源/防御顺序级打磨。

## 3. 裁决记录

| 裁决 | 结论 | 依据 |
|---|---|---|
| M5B/M5C 丢失欠账的处置 | 考古重建 + 完整硬化 | 用户 2026-08-18 选定「考古重建 + 完整硬化（推荐）」 |
| 里程碑结构 | 前置考古 → 单一全已知 change | 用户 2026-08-18 选定「A 前置考古 → 单 change（推荐）」 |
| 29 条 minor 清单 | 按推荐全做，无放弃项 | 控制会话裁决（发问未获回答；用户本里程碑内三次均选完整/推荐项，且纯注释类条目契合本仓「注释必须丰富且有信息量」的既有约定；用户可在设计评审推翻） |
| `planWireStep` null 语义 | 收紧：显式 null 视为字段出现，排他矩阵拒绝 | 控制会话裁决（同上未获回答）：与 M5D `"summary":null` 裁决（null=出现）同心智，且 `decodePlanStep` 的 GoDoc 已承诺字段排他——收紧兑现既有承诺，而非改文档迁就松散行为 |

## 4. 工作项全集（39 项）

考古条目位置以 2026-08-18 main 为准；实现前 implementer 须复核行号仍成立，不成立即在 ledger 记录「已失效」。

### 4.1 A 面：计划/寻路/模拟（10 条）

| # | 位置 | 动作 |
|---|---|---|
| A1 | `internal/sim/companion_action.go:85` + `companion_action_test.go:441` | 注释与测试名去掉「放置校验属后续任务」的 M5B 骨架期措辞，指向同 tick 的 `settleCompanionPlacements` 校验链；测试更名 `...DefensiveBoundary` |
| A2 | `internal/sim/actor.go:23-24` | 伙伴背包注释改为现状描述（MineHold 产物入包、Place 扣料写并置 inventoryDirty），删除里程碑叙事 |
| A3 | `internal/companion/planner.go:68` | 系统提示的 `[-64,319]` 改用 `core.MinY`/`core.MaxY-1` 拼接 |
| A4 | `internal/companion/planner_test.go:962` | 引入 `ItemIDMax` 哨兵常量并以之为穷举界，守护断言锁定枚举末项（与 E2 交叉锁同任务实施） |
| A5 | `internal/companion/pathfind.go:22` ↔ `plan_types.go:33` | 寻路窗口半径与观察半径共用同一常量，删除「同值仅靠约定」的漂移面 |
| A6 | `planner_test.go:89-100` ↔ `pathfind_test.go:33-44` | `wantPlanError`/`wantPathError` 合并为单个通用哨兵断言 helper |
| A7 | `internal/sim/companion_mining_test.go:64-117` | 抽出公共 `newCompanionMiningScene`，`ViaActions` 变体在其上补 action 链路 |
| A8 | 同上 `:77、155` | 删除两处 `entry.pitch = 0` 死赋值（伙伴采掘射线不使用 pitch） |
| A9 | `internal/sim/companion_action.go:44` | 容量注释改写为「全局每 tick 至多 MaxActive 条」，注明同伙伴重复提交可饿死其他伙伴 |
| A10 | `internal/companion/plan_types.go:21` ↔ network/chat 散落字面量 | 指令/聊天上限常量同源化（并入 E7，见 §7 依赖方向约定） |

### 4.2 B 面：服务端编排（10 条）

| # | 位置 | 动作 |
|---|---|---|
| B1 | `internal/server/companion_manager.go:1197` | wire 枚举注释 16..19 → 16..20，点名 v18 追加项（考古两评审员独立上报，并条） |
| B2 | 同上 `:1015-1019` | `restoredIssuerIdentity` 的 0x40/0x80 魔法字节补出处注释（`core.PlayerID.Valid()` 的 UUIDv4 最小满足形态） |
| B3 | 同上 `:937-941` | `submitPathRequest` 手写三次 `math.Floor` 改为复用 `standingCellOf(body.Position)` |
| B4 | 同上 `:1097-1098` | 删除对 M5B change「任务 7」的注释引用，改为现行 `restoreQueues` 行为描述 |
| B5 | `internal/server/companion_chat.go:60-63` | 删除 `len(chan)` clamp 死防御（语言级恒 `len ≤ cap`，全部构造点缓冲即上限） |
| B6 | `companion_manager.go:259-265` | `enqueueCommand` 防御分支广播 Accepted 的取舍补全注释（不改行为：路径仅配置缺陷可达） |
| B7 | `internal/server/companion_interact.go:292-296` | `observeTickResult` 的 mining 条目「刻意不清除」补生命周期注释，说明消费方 body-active gate 兜底 |
| B8 | `companion_manager.go:200-203` | `dialogueEffects` 注释如实更新（D6 后仍被 companion_dialogue_test 消费，未退役） |
| B9 | 同上 `:833-837` | issuers 空失配检查前移到 `BeginHead()` 之前，消除「队列已占用槽位但防御分支行为未定义」的次生态（本里程碑唯一编排代码级改动，防御路径不可达性以推理+注释锁定） |
| B10 | `internal/server/companion_persistence.go:576-580` | `sortCompanionBodies` 统一到 `slices.SortFunc`，删除 `sort` import |

### 4.3 C 面：协议/存档/客户端（9 条）

| # | 位置 | 动作 |
|---|---|---|
| C1 | `internal/storage/companion_codec.go:223-224` | schema 白名单显式列 `companionSchemaV4`，使入口与 `:308` 的 `schema >= companionSchemaV4` 前瞻口径一致（v5 落地时 v4 文件不再被入口误拒） |
| C2 | `cmd/mornlea/chat.go:140-153` | `ChatEventAccepted` 显式 case 化，default 分支改为带注释的防御兜底（当前唯一 default 承载者就是 Accepted；防新增 kind 漏 case 静默落入寻址格式） |
| C3 | `internal/storage/companion_restore_test.go:329` | 测试名与 doc 的 V3 口径改为 V4（断言已是 V4，属漏改） |
| C4 | `cmd/mornlea/chat.go:158` | `taskFailReasonText` 签名改收 `network.TaskFailReason`，转换上移到唯一调用点 |
| C5 | `chat.go:14` + `message_companion.go:281,296` | 裸 `1024/256` 与 companion 导出常量同源化（并入 E7） |
| C6 | `chat.go:175-182` | `truncateChatLine` 补中文 doc：32 rune 出处、第 32 rune 替换为 `…`、不做字节级截断 |
| C7 | `internal/network/message_companion.go:18-25` | 五个 wire 上限常量按 storage 侧 `maxCompanionFileLength` 风格补字节构成推导注释 |
| C8 | `message_companion_test.go:256,425,861` | kind 偏移表达式提取 `chatEventKindOffset` 测试 helper |
| C9 | `internal/client/chat.go:10` ↔ `cmd/mornlea/app.go:73` | client 侧导出容量常量供 cmd 复用，消除 `[32]` 字面跨包重复；`chatLines [6]string` 的 6 补出处注释 |

### 4.4 D：契约收紧（1 项，唯一行为变化，见 §5）

### 4.5 E：负载 flake 治理（2 项，只改测试）

- **TestScenarioV7**（`cmd/mornlea/benchmark_server_test.go:297`，darwin-only）：新增 `//go:build race` 辅助文件定义 `raceEnabled`，测试开头在 race 下 skip——50ms 实时调度门禁在 race 确定性开销下测的是机器负载而非产品行为（CI 文档已实测 4 次假失败纯由调度延迟）；非 race 路径门禁原样保留。
- **TestChatCommandAddresses**（`internal/server/tcp_integration_helpers_test.go:236`）：共享 helper `waitIntegrationCondition` 的 `runtime.Gosched()` 热轮询改 sleep 退避（100µs~1ms），47 个调用点全部受益；`waitForIncomingChatDepth` 条件改 `>= want`（现有调用点零行为差异）；60s 窗不再抬。

### 4.6 F：已知精确项（6 项）

1. **交叉锁测试**：对 `buildPlanPlaceBlocks()` 全集逐条断言 `BlockDrop(B) == (I, true)`——放置消耗的物品与采掘产出必须往返同一（防止「放置扣 X、采掘产 Y」的复制/丢失类漂移）；连同 A4 的 `ItemIDMax` 穷举界。
2. **ResolvedPersona tag 单测锁**：锁定 `Definition.Persona` 序列化可见、`ResolvedPersona` `json:"-"` 永不落盘（行为级已由 M5D D2 兜底，补 tag 级反射断言）。
3. **`ErrDialogueInvalidResponse` 命名拆分**：请求构造错误与响应解码错误拆为两个哨兵，调用方错误处理与日志可区分。
4. **decode UTF-8 wire 突变**：ChatEvent 共享文本槽解码补无效 UTF-8 wire 突变用例（fuzz 已覆盖，显式矩阵钉住）。
5. **chat 呈现负向断言收紧至 29 rune**（M5D D7 遗留，`cmd/mornlea` chat 测试）。
6. **阶段验收哑参数风格 + parity 投影复用**（M5D D8 遗留，server 测试）。

### 4.7 流程修复（1 项）

**backlog 沉淀纪律**：任何 change 归档前，其 ledger 中全部未决 minor/延期项必须全文誊入该 change `proposal.md` 的「延期与放弃」节（git 跟踪、随归档持久），ledger 只做执行记录不做唯一载体。M5E 自身归档时首个执行该纪律。

## 5. 唯一行为变化：planWireStep 的 null 语义

现状：`internal/companion/planner.go` 的 `planWireStep` 用 `*int32`/`*string` 指针区分「字段缺席」，但 JSON 显式 `null` 同样解出 nil，排他矩阵把 null 与缺席折叠——`{"kind":"follow","player_id":"<合法>","x":null}` 与 `{"kind":"go_to","x":1,"y":2,"z":3,"block":null}` 均被接受，违反 `decodePlanStep` GoDoc 承诺的字段排他；测试矩阵只有 `"x":null` 单形态。

收紧后：显式 null 视为字段出现。follow 携带数值字段（含 null）、go_to/mine 携带 `block`/`player_id`（含 null）、place 携带排他字段（含 null）一律以非法计划失败，任务进入 `Failed`，公开稳定失败原因不变。实现可选 map 中间形或自定义 UnmarshalJSON（与 M5D dialogue 的 map+isJSONNull 方案同构，具体由 implementer 按 TDD 选型）。测试矩阵补「专属外字段为 null」负向用例全集，GoDoc 同步校准。

**delta spec**：MODIFIED `companion-planner` 的「计划是严格 JSON 且步骤限定交付全集」requirement，追加场景「显式 null 视为字段出现」——排他矩阵对 null 与非 null 非法字段一视同仁。其余 capability 零 delta。

## 6. 任务分组（SDD，12 组）

| 任务 | 内容 | 预期 RED |
|---|---|---|
| E1 | §5 契约收紧 + delta spec 场景 + GoDoc | 有（null 负向矩阵先红） |
| E2 | F-1 交叉锁 + A4 ItemIDMax | 有（交叉锁先红：当前无一致性测试） |
| E3 | A1、A2、A3、A5、A8、A9（注释/常量/死赋值） | 无（注释级，验证=diff 复核；A3/A5 有编译级验证） |
| E4 | A6、A7、C8（测试 helper 去重） | 无（纯测试重构，现有断言全集保持绿） |
| E5 | B1、B2、B4、B5、B6、B7、B8、B10（注释与死代码） | 无（B5 删除死防御：白盒断言其从未生效可先行） |
| E6 | B3、B9（编排代码级：复用与前移） | 无（防御路径不可达，以推理+注释+全量回归锁定） |
| E7 | A10、C5、C7（上限常量同源 + wire 推导注释） | 有（先写跨包常量相等断言再同源化） |
| E8 | C1、C3（存档前瞻 + 测试名） | 无（C1 用 v5 假想 schema 的单测锁定前瞻口径） |
| E9 | C2、C4、C6、C9（客户端呈现批） | 无（C2 显式 case 化以既有呈现测试锁定等价） |
| E10 | F-2..F-6（M5D 延期五项） | F-2 有（tag 反射断言先红）；其余测试级 |
| E11 | §4.5 flake 治理两项 | 无（验证：race 下单测/整包复跑） |
| E12 | 收尾：全量门禁、progress.md、backlog 沉淀纪律执行、AGENTS.md 基线核对 | — |

每组一个全新 implementer 子代理 + 独立双裁决评审（规格合规 + 代码质量），修复循环 ≤5 轮，全部记录于 `.superpowers/sdd/tasks/progress.md`（M5E 段落覆盖 M5D 段落——M5D 记录的终局事实已誊入归档产物与本设计）。

## 7. 边界与依赖约定

- **常量同源方向**：E7 优先让 `internal/network` 与 `cmd/mornlea` 复用 `internal/companion` 导出常量（考古报告确认 network 已导入 companion）；若 archcheck 否决该方向，退为「两侧常量 + 跨包相等断言」的交叉锁形态，不强行下沉新公共包。
- **C9 导出面**：`internal/client` 导出容量常量供 `cmd/mornlea` 复用，依赖方向 cmd→client 既有，无新增反向依赖。
- **B9 安全论证**：检查前移后正常路径零变化（失配仅在 Enqueue/restore 缺陷下可达）；前移使队列不曾在缺陷态占用槽位，防御分支行为从「未定义」变为「跳过且无残留」，以注释固化该论证。
- 红线自查沿用 M5D：persona/摘要不进 Planner 输入、密钥只走环境变量、模型输出视为不可信数据、golden 原字节、门禁只紧不松。

## 8. 验证

- 每任务：focused `go test ./受影响包 -race -count=1`；E1/E2 另跑对应 fuzz/golden。
- E12 全量：`make rust`（如触及 Rust 面——预期不触及，跑一次确认）、`go test ./... -race -count=1`、`go test ./internal/archcheck`、`go vet ./...`、`gofmt -l .` 零输出、`openspec validate --all --strict`。
- E11 验证：race 下整包复跑受影响测试 + 一次全仓 `-race` 观察无新 flake。
- 性能数值只记录；报告完整性、真实 overflow、数据丢失仍是门禁。

## 9. 风险

- 考古行号漂移：implementer 逐条复核，失效项记 ledger 不强改。
- B9/E7 触及生产代码但均在防御/常量层，行为等价由全量回归 + 任务评审双锁。
- 39 项体量 ≈ M5D（9 任务），以 12 组分批控制单任务评审半径。
