## Why

`TestScenarioV7EightSessionServerProbeIsRealAndBounded` 是 CI 上证据最硬的一类红灯——已观察到的 8 条 CI 失败里占 4 条，全部报同一条断言：

| 运行 | 耗时 | 断言 |
| --- | --- | --- |
| 31108168593 | 7.75s | `measured tick 134: server input boundary 已错过 50ms tick deadline` |
| 31143784278 | 2.43s | `measured tick 27: ...同上` |
| 31144256852 | 3.26s | `measured tick 44: ...同上` |
| 31234686146 | 4.82s | `measured tick 74: ...同上` |

本地连跑 5 次全过，CI 上四次红，且本地复现不出 CI 的失败形态（前一变更已实测确认：`GOMAXPROCS=1` 是悬崖不是梯度，满载多核也复现不出）。

**现在的报错只说"错过了 50ms"，不说这 50 毫秒被谁吃掉了。** 预算覆盖的链条是「服务端调度 tick → 执行 tick → 回调 observer → 非阻塞塞进带缓冲 channel → 测试 goroutine 取出」，至少三个候选机制，性质完全不同：tick 自身慢是**真性能问题**，信号在缓冲里排队是**测量无效**，两者的正确处置相反。

前一个变更（`ci-stability-merge-gate`）正是在缺少这份数据的情况下，从代码结构推出一个自洽的故事（"采样预算按构造零余量"）并据此施工，结果无效。**本变更不重蹈覆辙：先拿数据，不做判定。**

## What Changes

- `benchmarkServerTickSignal` 增加 `published`（observer 回调内的时间戳）与 `duration`（tick 自身耗时，服务端已作为回调参数给出但未向下传递）两个字段。
- 新增纯函数 `formatTickBoundaryOverrun`，把一次超时拆成：总耗时、超出量、tick 自身耗时、调度→发布、发布→收到、收到时的队列深度。
- 四处 tick 边界失败站点改用该函数组装消息。

不改协议、不改存档格式、不改 benchmark 场景定义，因此无迁移。

**明确不做**：任何判定逻辑的改变。`fixedBenchmarkFrameDuration` 保持 `50ms`，界限断言逐字不动，通过与失败的条件与改动前完全一致。**本变更不修任何红灯**，只让下一次红说清楚话。

**明确不做**："测量无效 vs 越界"的分离。那是拿到数据之后的下一个变更；现在做等于据假说施工，正是前一变更的错误。

**明确不做**：用临时改小 `fixedBenchmarkFrameDuration` 的方式触发失败来验证。该常量还被 `probe.roster.Advance` 与客户端帧预算使用，改它会连带改变无关行为。验证靠纯函数单测。

## Capabilities

### New Capabilities

（无。）

### Modified Capabilities

- `bounded-benchmark-workload`: 新增一条要求——探针因错过 tick 输入边界而失败时，错误必须报出足以区分"被测系统慢"与"测量无效"的时间分解。既有的 8 条 Requirement 全部不变。

## Impact

- `cmd/mcgo/multiplayer_probe_epoch.go`：信号结构与 `observeScheduledTick`。
- `cmd/mcgo/multiplayer_benchmark.go`：新增格式化函数；`benchmarkServerInputDeadline` 签名增加队列深度参数；四处失败站点。
- `cmd/mcgo/multiplayer_benchmark_test.go`（可能新建）：格式化函数的单测。
- 依赖：不新增任何依赖。
- 性能：成功路径零额外开销，新增字段只在失败信息里被读取。填充 `published` 是每 tick 一次 `time.Now()`，在 observer 回调内，与既有的 `epoch.ticks.Add(duration)` 同一量级。
- **验证的局限**：本变更无法通过"CI 变绿"验证——它不修任何东西。判据是纯函数单测通过、变异验证有效、本地 ScenarioV7 耗时不变，以及下一次 CI 变红时报错能回答"这 50 毫秒被谁吃掉了"。最后一条只能等。
