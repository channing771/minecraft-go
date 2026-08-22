# M5E 递延清偿设计（m5e-deferred-clearing）

日期：2026-08-22；基线：origin/main @ 08932d9（texture-pack 归档后）。
工作区：`.worktrees/m5e-deferred-clearing`，分支 `claude/m5e-deferred-clearing`。

## 1. 背景与目标

M5E（2026-08-18 归档）确立了 backlog 沉淀纪律：递延项全文誊入归档 proposal 的「延期与放弃」节，
后续里程碑逐步清偿。本变更是该纪律的第一次兑现：从 M5E 的 8 项递延中清偿 6 项。

同时本变更是第三条并行道：`authoritative-hunger`（主工作区，执行中）与
`bedrock-survival-hud`（`.worktrees/bedrock-survival-hud`，提案就绪待执行）各占一片改动面，
本变更的任务选择以**零文件交集**为硬约束（见 §2）。

全部 6 项已在基线上逐行核实仍然有效（M5E 归档至今 main 经历 fluid/farming/LOD/texture-pack
四个变更，均未顺带修复这些项）。

## 2. 并行冲突矩阵

| 改动面 | hunger 流 | HUD 流 | 本变更 |
|---|---|---|---|
| `internal/sim`、`internal/core`、`internal/storage`（玩家 v7）、`internal/network`（协议 v24）、`internal/config`、`internal/archcheck` | ✓ | — | — |
| `internal/render/hud`、`cmd/mornlea` usage/capture/golden | ✓ | ✓ | — |
| `internal/companion`、`cmd/mornlea/{interactive.go,chat.go,chat_test.go}`、`internal/server/companion_*` | — | — | ✓ |
| `AGENTS.md`/`CLAUDE.md` | ✓（版本号） | ✓（归档时） | **零改动**（无能力/版本演进） |

两项 M5E 递延**再递延并写明归属**：

- 递延 4（`capture_scene.go:52`、`capture_ai_companion_test.go:185` 的 `[32]network.ChatEvent`
  字面）：capture 场景与 golden 是 HUD 流领地，hunger 组 5/7 也会触；
- 递延 5（`internal/network/codec_client.go` 的 1024 字面与错误文案同源化）：network 包是
  hunger 流领地（协议 v24 在改）。

二者由各自领地的流或其后续清偿，本变更不碰这两个文件。

## 3. 裁决记录

| # | 裁决 | 来源 |
|---|---|---|
| 1 | 方向选择：M5E 递延清偿（对 B/C/D 的排除理由见会话记录：B/D 与活跃流正面冲突，C 需先定方向且触 golden） | 用户批准 |
| 2 | 递延 8 加固走**生产侧**（令牌释放前移到 channel 发送之前），不做测试侧「边泵 tick 边等」绕开——前者消除整类窗口，后者只掩盖当前测试 | 用户批准 |
| 3 | 递延 8 的同类窗口在 `dialogueWorker` 同样存在（`companion_dialogue.go:120` defer 释放后置于 `:128` 发送），按裁决 2 的「消除窗口类」语义一并处理 | 控制器裁决（设计期，终审可复核） |
| 4 | M5E 放弃项 3 条维持放弃，不在本变更重开 | 沿用 M5E 归档 |

## 4. 工作清单

### D1（M5E 递延 1）planner 必填字段 null 拒绝分支的锁用例

现状：`internal/companion/planner.go` 的排他矩阵以 `appeared` 记录字段出现事实；M5E 的 null
负向全集（`planner_test.go:825-836`）覆盖**专属外字段** null 与 `go_to` 坐标 null
（`:608`），但 `follow`+`player_id:null` 与 `place`+`block:null` 两条**必填字段为 null** 的
拒绝分支无直接用例。这两个分支兼是 nil 解引用防 panic 屏障（若 null 被折叠为缺席，后续按
非 nil 解引用即 panic），误删分支时现有测试不变红。

做法：在既有非法步骤表追加两条用例，断言与相邻 null 用例同一错误身份
（`ErrPlannerInvalidPlan` 语义路径）。纯测试。

### D2（M5E 递延 7）planner 提示头段数字与窗口常量同源

现状：`plannerSystemPromptHead`（const 拼接）中「水平 16 格、垂直 8 格」为手抄数字；同包
`plan_types.go` 已有 `planEnvRadiusBlocks`（= `PathWindowHorizontalRadius` = 16）与
`planEnvVerticalBlocks` = 8。

做法：沿用 `plannerSystemPromptTail` 已确立的先例（包级 var 初始化期一次求值、常量插值、
运行期不可变），把头段改为 `fmt.Sprintf` 插值两个常量；**提示词字节保持逐位不变**（16/8
与现值相同），并加测试锁住完整头段字节（含插值结果）。若既有提示词锁测试存在则并入。

### D3（M5E 递延 2）`interactive.go` 输入缓冲与字节上限同源

现状：`cmd/mornlea/interactive.go:33` `var textInputBuffer [1024]rune` 是手写字面；
`chat.go:21` 的 `chatInput.runes` 已在 M5E E7 同源化为
`[companion.MaxPlanCommandBytes]rune`。两者巧合耦合：今日 rune ≥ byte 恒使字节上限不晚于
缓冲满触发，但上限一旦增大，逐帧 drain 会先静默截断。

做法：改为 `[companion.MaxPlanCommandBytes]rune`（编译期同步），注释说明推导（rune 编码
≥1 字节 ⇒ 满命令单帧到达也不在 drain 层截断）。一行重构。

### D4（M5E 递延 3）`formatChatEvent` 未知 kind 兜底的锁用例

现状：`cmd/mornlea/chat.go` 的 `formatChatEvent` kind switch **无 default 子句**——未知 kind
落入二级 reason switch 的 `default: return "未知事件"`（`chat.go:162-168`，E9/C2 防御兜底，
注释自证对今日协议不可达，因 `network.Validate` 拒绝未知 kind）。`chat_test.go:411` 的
"unknown" 是**已知**拒绝理由 `ChatRejectUnknownCompanion`（「未找到伙伴」），`:520` 表测试
覆盖全部已知 kind——兜底分支无直接用例，未来新增 kind 漏加 case 时现有测试不变红。

做法：直接构造 kind 为未占用枚举值的 `network.ChatEvent`（如
`network.ChatEventKind(200)`，绕过 wire 校验直调函数），断言返回「未知事件」；同用例可
附带断言 `ChatEventRejected` + 未知拒绝理由同走该兜底（同一 default 契约）。纯测试。

### D5（M5E 递延 6）验收测试两处忽略返回值收敛

现状：`internal/server/companion_stage_acceptance_test.go:45`
`durability, _ := core.ItemMaxDurability(...)` 与 `:65` `encoded, _ := json.Marshal(...)`。

做法：`stageAcceptanceSeedInventory` 与步骤构造改为携带 `*testing.T` 并在错误路径
`t.Fatalf`（调用点同步更新）。测试风格收敛。

### D6（M5E 递延 8）模型槽令牌释放前移，消除「结果已入队而名额未还」窗口

现状：`companion_manager.go` `plannerWorker` 尾部 `defer func() { <-m.semaphore }()`
先向 `plannerResults` 发送、函数返回时才释放令牌，两者间无屏障；`companion_dialogue.go`
`dialogueWorker` 同构（`:120` defer、`:128` 发送）。M5E CI 修复
（`TestCompanionDialogueSkippedWhenModelSlotsFull` 两段式等待）的确定性依赖「令牌释放先于
tick 线程 try-acquire」这一非同步调度事实，存在 ns 级残余窗口（未复现）。

语义论证：`m.semaphore`（cap = `companion.MaxActive` = 4，Planner/Dialogue 共享）约束的是
**在途模型调用数**。`Plan`/`Dialogue` 返回即模型调用结束，此刻释放令牌不放宽任何不变量：
即便 outcome 尚未送入 channel，也不存在第 5 个在途模型调用；「至多 4 个待投递结果」不是
任何代码依赖的不变量（结果 channel 的消费在 tick 边界，世代过时即丢）。

做法：两个 worker 均改为「调用返回后、发送前显式 `<-m.semaphore`，移除 defer 释放（保留
`waitGroup.Done` 的 defer）」；同步更新两处 worker GoDoc 与 `companion_manager.go:9` 的
共享状态注释。现有测试零改动，其前置观察（HTTP 层计数）经 happens-before 链
（Plan 返回 → 释放 → 发送 → tick 消费 → dialogue 派发 → HTTP 到达 → 测试观察）成为严格
确定。生产改动共约 6 行 + 注释。

## 5. 非目标

- 不改任何 wire 可观察行为、协议/存档/ABI/scenario 版本；不触 `internal/network`、capture
  场景与 golden、`internal/render/hud`。
- 不重开 M5E 放弃项 3 条（map 迭代序错误文本二选一、散文数字批量回改、位次锁组合取舍）。
- 不做递延 4/5（归属活跃流领地，见 §2）。
- 不动 `AGENTS.md`/`CLAUDE.md`（无基线演进；归档时 progress.md 增一小段）。

## 6. 任务分组与依赖

| 组 | 内容 | 文件 | 依赖 |
|---|---|---|---|
| T1 | D1 + D2 | `internal/companion/planner.go`、`planner_test.go` | 无 |
| T2 | D3 + D4 | `cmd/mornlea/interactive.go`、`chat_test.go` | 无 |
| T3 | D5 + D6 | `internal/server/companion_stage_acceptance_test.go`、`companion_manager.go`、`companion_dialogue.go`（及其对话相关测试如受注释/行为影响） | 无 |

组间零依赖，按 T1→T2→T3 顺序派发（SDD 串行）。每组：全新 implementer（brief 为唯一需求
来源）→ 独立评审（SPEC + QUALITY 双裁决）→ 修复循环 ≤5 轮 → ledger 记录。

## 7. OpenSpec 形态与验证门禁

- change `m5e-deferred-clearing`：无 delta spec（无可观察行为变化），`.openspec.yaml` 置
  `skip_specs: true`（M5E 探明并验证过的机制）；proposal.md 含 Why/What Changes/非目标/
  Impact 与「延期与放弃」节（誊录再递延 4/5 及执行期新欠账）。
- 每组验证：对应包聚焦 `-race` 测试 + `go test ./internal/archcheck -count=1` +
  `gofmt -l`；T3 另跑 `go test ./internal/server -race -count=1`（item 8 属并发时序）。
- 整支终审：`make rust`（无 Rust 改动，作门禁例行）+ `go test ./... -race` + `go vet` +
  `gofmt -l .` + `openspec validate --all --strict --no-interactive`。
- 已知既有红灯沿用 hunger 任务 7.2 的记录口径（不修不改阈值，如实记录）。

## 8. 风险与权衡

- D6 是唯一生产改动：释放点前移改变「名额占用时长」的观测语义（结果投递不再占名额），经
  §4-D6 论证不放宽模型并发不变量；终审复核点为「是否存在依赖『至多 4 个待投递结果』的
  代码」与 ctx 取消路径（取消时先释放、发送走 `<-m.ctx.Done()` 分支，行为不变）。
- D2 把提示头段从 const 降为 var：有 `plannerSystemPromptTail` 先例与字节锁测试兜底，
  提示词内容零变化。
- 三条流并行期间 main 会移动：本分支按 hunger 同款纪律在收尾组执行
  `git fetch && git rebase origin/main`，冲突预期仅 progress.md（hunger/HUD 归档段落）。
