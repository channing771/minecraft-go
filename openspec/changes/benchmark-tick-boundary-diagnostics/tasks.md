> 完整步骤级代码见 `docs/superpowers/plans/2026-08-07-benchmark-tick-boundary-diagnostics.md`。
> 本文件是可勾选的执行顺序与验证命令。三个任务组与该计划的 Task 1–3 一一对应。

> **本变更不修任何红灯。** 它不改变任何通过/失败的条件，只让已有的失败说清楚话。
> 因此**不能用"CI 变绿"验证**——判据是纯函数单测通过、变异验证有效、本地 ScenarioV7 耗时不变。

## 1. 信号字段与纯格式化函数

- [ ] 1.1 在 `cmd/mcgo/multiplayer_benchmark_test.go` 先写失败测试：构造 `scheduled` 已过期 100ms 的信号，断言消息里六项（总耗时、超出量、tick 自身、调度→发布、发布→收到、队列深度）数值都正确；另一条断言发布时刻为零值时标注"发布时刻缺失"且不报出依赖它的分段。
- [ ] 1.2 确认失败原因是断言而非编译错误——先加最小桩再跑。编译失败不算有效的红。
- [ ] 1.3 `benchmarkServerTickSignal` 增加 `published time.Time` 与 `duration time.Duration`，注释说明只在失败信息里读取。
- [ ] 1.4 `observeScheduledTick` 的发送处填充这两个字段，其余逻辑一行不动。
- [ ] 1.5 实现 `formatTickBoundaryOverrun(signal, now, queueDepth) string`，含发布时刻缺失的分支；注释写明队列深度取的是取出信号**之后**的 `len`，与"取出那一刻"差一个，但方向与判别力不变。
- [ ] 1.6 验证转绿：`go test ./cmd/mcgo -run "^TestFormatTickBoundaryOverrun" -count=1` 为 `ok`。
- [ ] 1.7 **变异验证**：把"调度→发布"与"发布→收到"两个实参对调，确认测试变红——**仍然通过说明单测没真正区分这两段，必须加强断言**。恢复后 `git diff` 干净。提交 `feat: 增加 tick 边界超时的时间分解格式化`。

## 2. 接入四个失败站点

- [ ] 2.1 `benchmarkServerInputDeadline` 签名增加 `queueDepth int`，失败分支改用 `formatTickBoundaryOverrun`。判定条件必须与改动前语义一致（只允许把 `time.Now()` 提取为局部变量复用）。
- [ ] 2.2 两个调用点传 `len(epoch.signals)`：测量循环内（约 412 行）、warm-up 后首组（约 663 行）。两处 `epoch` 都在作用域内。
- [ ] 2.3 两处内联检查（约 446、471 行）同样改用该函数；第二处在消息里标注"boundary 完成后"，以便日志区分是哪一段超的。
- [ ] 2.4 验证：`go build ./cmd/mcgo` 与 `go test ./cmd/mcgo -count=1` 为 `ok`。
- [ ] 2.5 **确认判定逻辑零改动**：逐条核对 `git diff cmd/mcgo/multiplayer_benchmark.go`，每一处 `if` 的判定表达式必须与改动前语义等价；出现任何比较运算符、阈值或分支结构的改变即回退重做。确认 `fixedBenchmarkFrameDuration` 仍是 `50 * time.Millisecond`。
- [ ] 2.6 **确认界限断言未被触碰**：`benchmark_v6_test.go` 里两处直接调用 `benchmarkServerInputDeadline` 的单测（`...UsesScheduledTickTime`、`...RejectsDelayedStepStart`）必须补第二个参数 `0`，否则整包编译不过；该文件的 diff **只应有这两处各多一个 `, 0`**。`TestScenarioV7...` 里那段界限断言必须逐字不变，diff 里出现那些标识符即回退重做。
- [ ] 2.7 验证：`go test ./cmd/mcgo -run "^TestScenarioV7EightSessionServerProbeIsRealAndBounded$" -count=3` 为 `ok`，单次约 11 秒。**耗时显著变长说明新增字段的填充进入了热路径**，违反"成功路径零额外开销"。实测耗时写进报告。提交 `feat: 四处 tick 边界失败改用时间分解消息`。

## 3. 收尾门禁与文档

- [ ] 3.1 回填设计文档 §8：变异验证结论、ScenarioV7 三次实测耗时。任何与实测不符的表述一并订正。
- [ ] 3.2 确认改动范围：`git diff --stat main...HEAD` 只应含 `multiplayer_benchmark.go`、`multiplayer_probe_epoch.go`、`multiplayer_benchmark_test.go`、`benchmark_v6_test.go`（仅两处 `, 0` 实参）与 docs/openspec 文件。
- [ ] 3.3 收尾门禁：`go test ./... -race`、`go test ./internal/archcheck -count=1`、`go vet ./...`、`gofmt -l .` 无输出、`git diff --check` 无输出、`openspec validate --all --strict --no-interactive`。若 `go test ./... -race` 失败先看是不是 `TestDropSurvivesShutdownAndRestart`——那是已知的既有偶发挂起，不是本变更引入的。
- [ ] 3.4 提交 `docs: 回填 tick 边界分解的实测结果`。
- [ ] 3.5 **明确报告本变更不会让 CI 变绿**，并说明下一步：等 CI 上 ScenarioV7 再次变红、读取分解数据、再按设计文档 §6 的判定表决定做哪一种修复。
