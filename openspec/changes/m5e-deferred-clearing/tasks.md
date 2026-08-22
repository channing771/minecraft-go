# 任务

三组串行派发（组间零依赖）；每组：全新 implementer（brief 为唯一需求来源）→ 独立评审
（SPEC + QUALITY 双裁决）→ 修复循环 ≤5 轮 → ledger 记录。精确步骤与代码见
`docs/superpowers/plans/2026-08-22-m5e-deferred-clearing.md`（执行者必须同时读取该计划与
`docs/superpowers/specs/2026-08-22-m5e-deferred-clearing-design.md`）。

## 1. companion：planner 必填 null 锁用例 + 提示头段常量同源（D1+D2）

- [x] 1.1 新增 `TestPlannerSystemPromptHeadBytesStable` 锁头段完整字节（先锁现状，绿）。
- [x] 1.2 planner 非法步骤表 null 全集末尾追加 `follow player_id null`、`place block null`
  两例（必填字段显式 null 与缺席同拒；分支兼是 nil 解引用防 panic 屏障），现状绿。
- [x] 1.3 变异验证：必填校验临时改指针判断 → 两例红；还原复绿。
- [x] 1.4 `plannerSystemPromptHead` 改 const intro + `fmt.Sprintf` 插值
  `planEnvRadiusBlocks`/`planEnvVerticalBlocks`（字节逐位不变），GoDoc 同步；
  1.1 锁测试与既有提示字节比对保持绿。
- [x] 1.5 验证：`go test ./internal/companion -race -count=1`、
  `go test ./internal/archcheck -count=1`、`gofmt -l internal/companion/` 无输出。

## 2. chat：输入缓冲同源化 + 未知 kind 兜底锁用例（D3+D4）

- [x] 2.1 新增 `TestFormatChatEventUnknownKindFallsBackToNeutralLine`：直构未知 kind 与
  未知拒绝理由（绕过 wire 校验），断言均返回「未知事件」，现状绿。
- [x] 2.2 变异验证：临时删除二级 reason switch 的 default → 用例红；还原复绿。
- [x] 2.3 `interactive.go` 的 `[1024]rune` 改 `[companion.MaxPlanCommandBytes]rune`
  （import 补 `internal/companion`），注释说明同源推导；`go build ./cmd/...` 通过。
- [x] 2.4 验证：`go test ./cmd/mornlea -run 'TestChat|TestFormatChatEvent|TestInteractive|TestRunInteractive' -race -count=1`
  （不跑整包——包级 GPU 测试有 hunger 7.2 记录的既有红灯）、
  `go test ./internal/archcheck -count=1`、`gofmt -l cmd/mornlea/` 无输出。

## 3. server：验收夹具错误断言 + 模型槽令牌释放前移（D5+D6）

- [x] 3.1 `stageAcceptanceSeedInventory`/`stageAcceptancePlanJSON` 携 `*testing.T` 显式
  断言错误（`ItemMaxDurability`、`json.Marshal`），两个调用点同步；行为零变化。
- [x] 3.2 `plannerWorker` 与同构 `dialogueWorker`：删除 defer 释放，改为模型调用返回后、
  结果发送前显式 `<-m.semaphore`，中文时序论证注释 + 两处 GoDoc + 文件头契约注释同步。
- [x] 3.3 验证：`go test ./internal/server -run 'TestCompanionDialogue|TestCompanionPlanner|TestCompanionManager' -race -count=1`、
  `TestCompanionDialogueSkippedWhenModelSlotsFull -race -count=5`、
  `go test ./internal/server -race -count=1`、`go test ./internal/archcheck -count=1`、
  `gofmt -l internal/server/` 无输出。

## 4. 收尾（控制会话）

- [x] 4.1 `git fetch && git rebase origin/main`；整支终审四件套 + `openspec validate
  m5e-deferred-clearing --strict --no-interactive`。
- [ ] 4.2 独立终审代理复核 D6 语义论证与三组提交；PR → 合并 → 归档
  （progress.md 小段 + 执行期欠账誊入「延期与放弃」）。
