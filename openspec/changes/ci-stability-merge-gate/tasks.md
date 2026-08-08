> 完整步骤级代码见 `docs/superpowers/plans/2026-08-07-ci-stability-merge-gate.md`。
> 本文件是可勾选的执行顺序与验证命令。

> **本变更没有本地复现手段。** `GOMAXPROCS=1` 曾被选作慢 runner 的 A/B 模拟器，已实测证伪
> （1 处永不收敛、2 处秒过，是悬崖不是梯度；满载多核复现不出任何失败），**不得使用**。
> 详见设计文档 §7。验证靠推理、反向验证（常量改 1ms 必须变红）与 CI 统计观察。

> **本变更只处理 CI 六次红中的四次**（3 次采样预算 + 1 次期限耗尽）。
> 另外三次是登录期 `transport closed`，根因未知，需独立的 systematic-debugging 变更。
> **不得把本变更描述为"修好了 CI"。**

## 1. 命名常量与已知的活性超时站点

- [ ] 1.1 新建 `internal/server/deadline_test.go`（`package server`）定义 `shortWaitDeadline = 5s`、`waitDeadline = 30s`、`longWaitDeadline = 60s`，GoDoc 说明四类期限的分类与禁改区。
- [ ] 1.2 新建 `internal/server/deadline_external_test.go`（`package server_test`）定义逐字相同的一份。跨包无法共享未导出标识符，两份必须同步。
- [ ] 1.3 把 CI 上确实因期限耗尽而失败的站点换成常量：`host_test.go` 的 `waitReady`（5s 轮询，对应 `TestHostRejectsDuplicatePlayerBeforeLoad` 的 `player did not become ready`）。
- [ ] 1.4 验证：`go test ./internal/server -race -count=1` 为 `ok`；`grep -rn "context.DeadlineExceeded" internal/server/*_test.go` 的**断言**站点仍为 10 处（常量 GoDoc 里的引用不算）。提交 `test: 增加活性等待期限常量`。

## 2. 秒档机械替换

- [ ] 2.1 统计秒档站点总数，替换完成后必须归零。
- [ ] 2.2 核对秒档与禁改区零重叠（10 处禁改区已核实全部落在毫秒档）；有重叠则该处排除并在提交信息说明。
- [ ] 2.3 三种语法形态（`context.WithTimeout` / `time.Now().Add` / `time.After`）全部覆盖。`time.Second` 裸写法 77 处最容易漏，且归 `waitDeadline` 而非 `shortWaitDeadline`。`10 * time.Minute` 不动。
- [ ] 2.4 验证：秒档字面量归零；禁改区断言仍为 10 处；`go test ./internal/server -race -count=1` 为 `ok` 且耗时与改前同量级。**耗时显著变长即说明有站点被误分类**，正在白等。提交 `test: 秒档活性等待收敛到命名常量`。

## 3. 毫秒档与助手参数形态逐处核对

- [ ] 3.1a 列出约 23 处毫秒档站点。
- [ ] 3.1b 列出"期限作参数传给助手"形态（Task 2 的正则漏掉了这一种，命中 31 处）：`shutdownWithDeadline` 28 处（多数是活性等待，给关服只留 1 秒，是全包最紧的一档）、`clock.nextTimer` 3 处（**时长值断言，绝不动**），另有 2 处 `connectTask16ConcurrentClients` 需一并判定。
- [ ] 3.2 逐处按断言形态分类：期望 `errors.Is(err, context.DeadlineExceeded)` → 超时触发断言，不动；断言没收到消息 → 缺席断言，不动；超时即 `t.Fatal` → 活性等待，毫秒档换 `shortWaitDeadline`、1 秒档换 `waitDeadline`；断言耗时小于上限 → 性能门禁，不动；**时长被助手拿去做比较而非做期限 → 时长值断言，绝不动**。
- [ ] 3.2b 时长值断言没有机械判据：`clock.nextTimer(t, 5*time.Second)` 在语法上与传期限完全一致，但假时钟里它断言的是"被测代码调度了一个 5 秒定时器"，替换后测试**仍然通过**、只是不再测原本的行为。因此 3.1b 的每一处**必须先读该助手的实现**，确认时长是被拿去 `context.WithTimeout` 还是被拿去比较。
- [ ] 3.2c `shutdownWithDeadline` 的 7 处 `!errors.Is(err, wantErr)` 与 `recoverShutdownAfterExpectedFailure` 的三次循环期望关服返回**注入的错误**。若注入失败会让关服卡住，抬高期限会让每处多等 30 秒、循环处多等 90 秒——判定前读该助手实现确认。
- [ ] 3.3 把每一处的 `文件:行`、判定类别与依据写成一张表放进**报告文件**（评审者读得到；`git log --oneline` 读不到完整提交信息）。提交信息放一句摘要。**这张表是本任务的主要产出。**
- [ ] 3.4 只替换判定为活性等待的站点。
- [ ] 3.5 验证：`go test ./internal/server -race -count=1` 为 `ok` 且耗时未显著变长。提交 `test: 逐处核对毫秒档期限并只抬高其中的活性等待`。

## 4. ScenarioV7 采样收集预算

- [ ] 4.1 确认 `measureMultiplayerServerProbe` 要求 `duration >= 10s` 而测试传入的正是 `10s`——按构造零余量。
- [ ] 4.2 抄下改前的全部界限断言（`ServerOutboundBytes`、`InterestDiff.Samples`、`ticks.Frames`、`OutboxHighWater`、`PlayerJobsHighWater`、`PlayerDoneHighWater`、`PeakRSSBytes`），改后必须逐字不变。
- [ ] 4.3 把收集预算改为 `30 * time.Second` 并加注释说明这是预算而非阈值。
- [ ] 4.4 验证：`git diff cmd/mcgo/benchmark_v6_test.go` 中**不得触及任何 `!=` / `>` / `>=` 断言**，触及即回退重做；`go test ./cmd/mcgo -run "^TestScenarioV7EightSessionServerProbeIsRealAndBounded$" -count=3` 为 `ok`。提交 `test: 放宽 ScenarioV7 采样收集预算，界限断言不变`。

## 5. 反向验证、收尾门禁与文档

- [ ] 5.1 反向验证：把两份 `waitDeadline` 临时改成 `1 * time.Millisecond`，确认失败数大于 0——**等于 0 说明常量根本没被用上**，替换工作有问题。恢复后 `git diff` 干净。
- [ ] 5.2 确认两份常量定义逐字一致。
- [ ] 5.3 回填设计文档的实测数据；任何与实测不符的表述一并订正。
- [ ] 5.4 确认产品代码零改动：`git diff --stat main...HEAD -- ':!*_test.go' ':!docs' ':!openspec'` 无输出。
- [ ] 5.5 收尾门禁：`go test ./... -race`、`go test ./internal/archcheck -count=1`、`go vet ./...`、`gofmt -l .` 无输出、`git diff --check` 无输出、`openspec validate --all --strict --no-interactive`。
- [ ] 5.6 推分支后观察 CI。**本变更预期只消除采样预算与期限耗尽两类失败，`transport closed` 仍会出现**——观察时必须按断言分类统计，不得把仍然出现的 `transport closed` 当作本变更失败，也不得因为它消失了就归功于本变更。
