# M5E 递延清偿（m5e-deferred-clearing）

## Why

M5E（2026-08-18 归档）确立了 backlog 沉淀纪律：递延项全文誊入归档 proposal 的「延期与放弃」
节、后续逐步清偿，ledger 不做唯一载体。本变更是该纪律的第一次兑现：清偿其 8 项递延中的
6 项。同时本变更作为与 `authoritative-hunger`、`bedrock-survival-hud` 并行的第三条工作流，
任务选择以与两条活跃流**零文件交集**为硬约束。

## What Changes

- **D1（递延 1）**：`internal/companion/planner_test.go` 补 `follow`+`player_id:null`、
  `place`+`block:null` 两条必填字段显式 null 用例——两个拒绝分支兼是 nil 解引用防 panic
  屏障，现有 null 全集只覆盖专属外字段与坐标。
- **D2（递延 7）**：`plannerSystemPromptHead` 中「水平 16 格、垂直 8 格」手抄数字改为引用
  `planEnvRadiusBlocks`/`planEnvVerticalBlocks` 的插值（沿用 `plannerSystemPromptTail` 的
  包级 var 先例），**提示词字节逐位不变**，新增头段字节锁测试。
- **D3（递延 2）**：`cmd/mornlea/interactive.go` 的 `[1024]rune` 输入缓冲改为
  `[companion.MaxPlanCommandBytes]rune`，与 `chatInput.runes` 的 E7 同源化收口。
- **D4（递延 3）**：直接构造未知 kind 与未知拒绝理由的 `ChatEvent`，锁定
  `formatChatEvent` 的「未知事件」防御兜底。
- **D5（递延 6）**：`companion_stage_acceptance_test.go` 两处 `x, _ :=` 忽略返回值改为
  携 `t` 的显式错误断言。
- **D6（递延 8）**：`plannerWorker` 与同构的 `dialogueWorker` 把模型槽令牌
  （`m.semaphore`，约束在途模型调用数）释放从 defer 后置改为「模型调用返回后、结果
  channel 发送前」显式执行——消除「结果已入队而名额未还」的 ns 级调度窗口，令
  M5E CI 修复的两段式等待成为严格 happens-before 确定；语义论证见 design.md。

全部 6 项已在基线 origin/main @ 08932d9 上逐行核实仍然有效。

## 非目标

- 不改任何 wire 可观察行为；不迁移协议 v23、玩家/区块/世界/伙伴存档 schema、engine ABI
  v6、client ABI v7、benchmark scenario v18 与配置格式。
- 不触 `internal/network/**`、`internal/render/hud/**`、`cmd/mornlea/capture_*.go`、
  golden 基线与 `AGENTS.md`/`CLAUDE.md`（并行流领地或最热共享文件）。
- 不重开 M5E 放弃项 3 条（map 迭代序错误文本、散文数字批量回改、位次锁组合取舍）。

## Impact

- 受影响文件：`internal/companion/planner.go`、`planner_test.go`、
  `cmd/mornlea/interactive.go`、`chat_test.go`、
  `internal/server/companion_stage_acceptance_test.go`、`companion_manager.go`、
  `companion_dialogue.go`——与两条活跃流的改动面零交集。
- D6 是唯一生产改动（两处并发时序重排 + 注释），D2/D3 是字节不变的同源化重构，
  D1/D4/D5 为测试与夹具硬化。
- 实施细节见 `docs/superpowers/plans/2026-08-22-m5e-deferred-clearing.md` 与
  `docs/superpowers/specs/2026-08-22-m5e-deferred-clearing-design.md`。

## 延期与放弃

誊录 M5E 归档递延项中不在本变更清偿范围的部分（编号沿用 M5E 归档 proposal）：

- **递延 4**（`capture_scene.go:52`、`capture_ai_companion_test.go:185` 的
  `[32]network.ChatEvent` 字面量同源化）：capture 场景与 golden 是 bedrock-survival-hud
  与 authoritative-hunger（组 5/7）的活跃领地，本变更不触；待两流合并后由领地方向的后续
  change 清偿。
- **递延 5**（`internal/network/codec_client.go:73/:238` 的 1024 字面与 `:88` 错误文案
  同源化）：network 包是 authoritative-hunger（协议 v24）的活跃领地；待其合并后清偿。

执行期新产生的递延（归档前按 M5E 沉淀纪律誊录，终审已逐条裁决）：

- **执行期递延 1**（终审裁决 defer）：planner 非法计划宿主测试只断言错误身份类别，「显式 null 视为字段出现」在错误**消息**层未被锁定——单独把出现判定换成指针判定（保留拒绝语义）不会变红；消息级区分需调整宿主断言形态，与收益不成比例。
- **执行期递延 2**（权衡记录，终审裁决 defer）：worker 令牌释放改为裸语句后，`Plan`/`Do` 内未恢复的 panic 将跳过释放（旧 defer 会释放）——worker goroutine panic 即终止进程，泄漏令牌不可观察；若未来引入 worker 级 panic 恢复需重估该结构。
