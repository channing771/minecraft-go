> 完整步骤级代码见 `docs/superpowers/plans/2026-08-07-ci-stability-merge-gate.md`。
> 本文件是可勾选的执行顺序与验证命令。六个任务组与该计划的 Task 1–6 一一对应。

> **改动前必须先复现 A/B 基线**：`GOMAXPROCS=1 go test ./internal/server -count=1`。
> 四个活性超时（`TestCraftingSurvivesV2DiskRestartAndReconnectOrder`、
> `TestDropSurvivesShutdownAndRestart`、`TestDroppedItemSurvivesShutdownAndRestart`、
> `TestAuthoritativeMiningMemoryLifecycle`）由任务 1–2 处理；两个顺序假设
> （`TestOpenFurnaceSendsStateOnlyToViewer`、`TestWorldPersistsAcrossRestartAndGeneratorUpgrade`）
> 由任务 5 处理。
>
> **基线是分布不是定值**：`GOMAXPROCS=1` 只把失败概率推高，不推到 1。缺少表内测试可以继续；
> 多出的表外测试若诊断为同类活性超时则一并纳入，不属活性超时才停手报告。真正的验收标准是
> 改动后全绿（任务 6.3），不是改动前恰好复现某个特定集合。

## 1. 命名常量与三个活性超时

- [ ] 1.1 复现 A/B 基线，确认失败集合与计划一致。
- [ ] 1.2 新建 `internal/server/deadline_test.go`（`package server`）定义 `shortWaitDeadline = 5s`、`waitDeadline = 30s`、`longWaitDeadline = 60s`，GoDoc 说明四类期限的分类与禁改区。
- [ ] 1.3 新建 `internal/server/deadline_external_test.go`（`package server_test`）定义逐字相同的一份。跨包无法共享未导出标识符，两份必须同步。
- [ ] 1.4 从失败信息定位令前三个测试超时的具体站点并替换（已知含 `persistence_integration_test.go` 的 `stepUntil`）。
- [ ] 1.5 验证：`GOMAXPROCS=1 go test ./internal/server -run "TestDropSurvivesShutdownAndRestart|TestDroppedItemSurvivesShutdownAndRestart|TestAuthoritativeMiningMemoryLifecycle" -count=3` 为 `ok`；`grep -rn "context.DeadlineExceeded" internal/server/*_test.go | wc -l` 仍为 `10`；`go test ./internal/server -race -count=1` 为 `ok`。提交 `test: 增加活性等待期限常量并修复三处磁盘重启超时`。

## 2. 秒档机械替换

- [ ] 2.1 统计秒档站点总数，替换完成后必须归零。
- [ ] 2.2 核对秒档与禁改区零重叠（禁改区 10 处已核实全部落在毫秒档）；有重叠则该处排除并在提交信息说明。
- [ ] 2.3 三种语法形态（`context.WithTimeout` / `time.Now().Add` / `time.After`）全部覆盖。`time.Second` 裸写法 77 处最容易漏，且归 `waitDeadline` 而非 `shortWaitDeadline`。`10 * time.Minute` 不动。
- [ ] 2.4 验证：秒档字面量归零；禁改区仍为 10 处；`GOMAXPROCS=1 go test ./internal/server -count=1` 只剩两个亚秒失败，**不得出现新的失败测试**。
- [ ] 2.5 验证：`go test ./internal/server -race -count=1` 为 `ok` 且耗时与改前同量级。**耗时显著变长即说明有站点被误分类**，正在白等。提交 `test: 秒档活性等待收敛到命名常量`。

## 3. 毫秒档逐处核对

- [ ] 3.1 列出约 23 处毫秒档站点。**本档不得机械替换**——它混着活性等待与禁改区，是唯一必须人工逐处判的部分。
- [ ] 3.2 逐处按断言形态分类：期望 `errors.Is(err, context.DeadlineExceeded)` → 超时触发断言，不动；断言没收到消息 → 缺席断言，不动；超时即 `t.Fatal` → 活性等待，换 `shortWaitDeadline`；断言耗时小于上限 → 性能门禁，不动。
- [ ] 3.3 把每一处的 `文件:行`、判定类别与依据写进提交信息。**这份判定记录是本任务的主要产出**，它让评审者能核对分类而不必重做一遍。
- [ ] 3.4 只替换判定为活性等待的站点。
- [ ] 3.5 验证：`go test ./internal/server -race -count=1` 为 `ok`；`GOMAXPROCS=1` 仍只剩两个亚秒失败且耗时未显著变长。提交 `test: 逐处核对毫秒档期限并只抬高其中的活性等待`。

## 4. ScenarioV7 采样收集预算

- [ ] 4.1 确认 `measureMultiplayerServerProbe` 要求 `duration >= 10s` 而测试传入的正是 `10s`——按构造零余量。
- [ ] 4.2 抄下改前的全部界限断言（`ServerOutboundBytes`、`InterestDiff.Samples`、`ticks.Frames`、`OutboxHighWater`、`PlayerJobsHighWater`、`PlayerDoneHighWater`、`PeakRSSBytes`），改后必须逐字不变。
- [ ] 4.3 把收集预算改为 `30 * time.Second` 并加注释说明这是预算而非阈值。
- [ ] 4.4 验证：`git diff cmd/mcgo/benchmark_v6_test.go` 中**不得触及任何 `!=` / `>` / `>=` 断言**，触及即回退重做；`go test ./cmd/mcgo -run "^TestScenarioV7EightSessionServerProbeIsRealAndBounded$" -count=3` 为 `ok`。提交 `test: 放宽 ScenarioV7 采样收集预算，界限断言不变`。

## 5. 两处顺序假设修复

> **这两处不得用抬高期限处理。** 它们在期限的百分之一以内就失败，抬高期限只会掩盖问题。
> 若任一处的根因在产品代码而非测试代码，**停手另开 change**——那是权威模拟的正确性问题。

- [ ] 5.1 确认 `TestWorldPersistsAcrossRestartAndGeneratorUpgrade` 的失败是输入未被消费：`LastInputSequence` 远小于 600 而 `WorldTimeTicks` 大于 600。
- [ ] 5.2 把 `moveViewToUnvisitedChunk` 的每轮 `h.step()` 改为等待本条输入被权威消费（`state.LastInputSequence >= h.sequence`），并注释说明按迭代次数计数为何在争抢下失效。
- [ ] 5.3 验证：`GOMAXPROCS=1 go test ./internal/server -run TestWorldPersistsAcrossRestartAndGeneratorUpgrade -count=5` 为 `ok`。仍红则停手报告。
- [ ] 5.4 用临时 `t.Logf` 确认 `TestOpenFurnaceSendsStateOnlyToViewer` 收到的是哪一版熔炉状态。
- [ ] 5.5 把等待循环的退出条件从代理条件 `Generation != 0` 换成目标条件（输入槽已反映原铁），删掉临时打印。若打印显示状态**从未**带上原铁，说明根因在发布路径而非等待条件，停手报告。
- [ ] 5.6 验证：`GOMAXPROCS=1 go test ./internal/server -count=1` **全包全绿**。提交 `test: 修复两处等待条件的顺序假设`。

## 6. 反向验证、收尾门禁与文档

- [ ] 6.1 反向验证：把两份 `waitDeadline` 临时改成 `1 * time.Millisecond`，确认失败数大于 0——**等于 0 说明常量根本没被用上**，替换工作有问题。恢复后 `git diff` 干净。
- [ ] 6.2 确认两份常量定义逐字一致。
- [ ] 6.3 回填 `docs/superpowers/specs/2026-08-07-ci-stability-merge-gate-design.md`：改后 A/B 结果、毫秒档分类结论、两处顺序假设修复的最终形态、套件耗时改前/改后对比。任何与实测不符的表述一并订正。
- [ ] 6.4 确认产品代码零改动：`git diff --stat main...HEAD -- ':!*_test.go' ':!docs' ':!openspec'` 无输出。
- [ ] 6.5 收尾门禁：`go test ./... -race`、`go test ./internal/archcheck -count=1`、`go vet ./...`、`gofmt -l .` 无输出、`git diff --check` 无输出、`openspec validate --all --strict --no-interactive`。遇 `macos-latest` 已知时序假失败按已知抖动处理，**不得改阈值**。
- [ ] 6.6 推分支后观察 CI 连绿次数并报告给用户。**达到 10 次连绿前不要建议开启分支保护**——那是本变更的目的，也是唯一无法在本地验证的部分，最终由用户在 GitHub 上开启 `Require branches to be up to date before merging`。
