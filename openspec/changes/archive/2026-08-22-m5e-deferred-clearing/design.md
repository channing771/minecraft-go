# Design — m5e-deferred-clearing

完整设计论证（并行冲突矩阵、裁决记录、D1–D6 逐项现状与修法、风险与权衡）见
`docs/superpowers/specs/2026-08-22-m5e-deferred-clearing-design.md`；实施步骤见
`docs/superpowers/plans/2026-08-22-m5e-deferred-clearing.md`。本文件只沉淀设计决策要点。

## 裁决

1. 方向：清偿 M5E 递延冷区项（用户批准）。
2. 递延 8 加固走生产侧（令牌释放前移），不做测试侧绕开（用户批准）。
3. `dialogueWorker` 与 `plannerWorker` 同构（`companion_dialogue.go:120` defer 释放后置
   于 `:128` 发送），按「消除窗口类」语义一并处理（控制器裁决，终审可复核）。
4. M5E 放弃项 3 条维持放弃。

## D6 语义论证（唯一生产改动）

`m.semaphore`（cap = `companion.MaxActive` = 4，Planner/Dialogue 共享）约束的是**在途模型
调用数**。`Plan`/`Dialogue` 返回即模型调用结束，此刻释放令牌不放宽任何不变量：即便
outcome 尚未送入 channel，也不存在第 5 个在途模型调用。两个结果 channel 均为
`companion.MaxActive` 缓冲（`companion_manager.go:237/:239`），发送不长期阻塞；「至多 4 个
待投递结果」不是任何代码依赖的不变量（结果在 tick 边界非阻塞排空，世代过时即丢）。ctx
取消路径行为不变：取消时同样先释放、发送走 `<-m.ctx.Done()` 分支放弃结果。

前移后，M5E CI 修复（`TestCompanionDialogueSkippedWhenModelSlotsFull` 两段式等待）的确定性
不再依赖「planner 令牌释放先于 tick 线程 try-acquire」这一非同步调度事实，而是
happens-before 链的严格推论。

## D2 字节不变性

头段拆为 const intro + `fmt.Sprintf` 插值 `planEnvRadiusBlocks`（16）/
`planEnvVerticalBlocks`（8）两段；插值常数与原手抄数字同值，拼结果逐位等于原文。
`TestPlannerSystemPromptHeadBytesStable` 锁完整头段字节；既有
`planner_test.go:418`（整提示 HTTP 级字节比对）与 `dialogue_client_test.go:151`
（dialogue 提示不得包含 planner 头段）继续承重。

## 验证口径

- 每任务组：聚焦 `-race` 测试 + `go test ./internal/archcheck -count=1` + `gofmt -l`。
- D1/D4 附带变异验证（破坏对应守卫/分支必须令新用例变红）。
- D6 附带 `TestCompanionDialogueSkippedWhenModelSlotsFull -count=5` 稳定性佐证与
  `./internal/server` 全包 race。
- 整支终审：`make rust` + `go test ./... -race` + `go vet` + `gofmt -l .` +
  `openspec validate --strict`；已知既有红灯沿用 hunger 任务 7.2 口径（不修不改阈值，
  如实记录）。
