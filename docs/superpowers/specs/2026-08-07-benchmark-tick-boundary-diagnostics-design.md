# 50ms tick 边界失败的时间分解设计

日期：2026-08-07
状态：设计已批准，待实施

## 1. 起因

`TestScenarioV7EightSessionServerProbeIsRealAndBounded` 是 CI 上证据最硬的一类红灯——已观察到的 8 条 CI 失败里占 4 条：

| 运行 | 耗时 | 断言 |
| --- | --- | --- |
| 31108168593 | 7.75s | `measured tick 134: server input boundary 已错过 50ms tick deadline` |
| 31143784278 | 2.43s | `measured tick 27: ...同上` |
| 31144256852 | 3.26s | `measured tick 44: ...同上` |
| 31234686146 | 4.82s | `measured tick 74: ...同上` |

本地连跑 5 次全过（每次约 11 秒），CI 上四次红。

上一个变更（`ci-stability-merge-gate`）曾把这四次误判为"采样收集预算不足"，据此放宽预算——**无效**，因为四次失败全部发生在原预算之内。误判的成因是没读断言原文、从代码结构推了一个自洽的故事。详见该变更设计文档 §4 与 §7 错误四。

**这一次不重蹈覆辙：在拿到数据之前不做任何判定。**

## 2. 已知的结构事实

这些是读代码得到的，不是推测：

`benchmarkServerInputDeadline`（`cmd/mcgo/multiplayer_benchmark.go`）在**收到 tick 信号后立刻**执行，位于 `readStats()` 与 `readRSS()` **之前**：

```go
for completed := 1; completed <= benchmarkServerMeasuredTicks; completed++ {
    select {
    case signal = <-epoch.signals:      // ← 收到信号
    case <-ctx.Done():
        return result, ctx.Err()
    }
    ...
    inputDeadline, err = benchmarkServerInputDeadline(signal)   // ← 报错在这里
    ...
    stats := readStats()                // ← 采样在报错之后
```

因此**这 50 毫秒不是被测试侧的采样工作吃掉的**。CI 上那四条报错走的正是这一条路径（`fmt.Errorf("measured tick %d: %w", ...)` 包裹 `benchmarkServerInputDeadline` 的错误）。

预算覆盖的完整链条：

```
服务端调度 tick(scheduled)
  → 服务端执行 tick(duration)
  → 回调 ScheduledTickObserver
  → 非阻塞塞进带缓冲 channel epoch.signals
  → 测试 goroutine 从 channel 收到
  → 检查 now < scheduled + 50ms
```

`epoch.signals` 是**有缓冲**的，发送用 `select`/`default` 非阻塞，溢出只置 `overflow` 标志（`multiplayer_probe_epoch.go` 的 `observeScheduledTick`）。

## 3. 三个候选机制

| 机制 | 含义 | 性质 |
| --- | --- | --- |
| **tick 自身慢**（`duration` 大） | 服务端真跟不上 50ms 周期 | **真性能问题** |
| **调度→发布延迟大** | 服务端侧回调链路被晾 | 环境噪声 |
| **信号在缓冲里排队** | 测试 goroutine 落后，取到的是陈旧信号 | 测量无效，非被测系统之过 |

第三个机制在结构上是成立的：channel 有缓冲、发送不阻塞，测试 goroutine 一旦被调度器晾住，信号就会积压；等它恢复，取出的信号 `scheduled` 早已过期。

**但这仍然只是假说。** 本设计不据此做任何判定——那正是上一轮犯的错。

## 4. 改什么

`benchmarkServerTickSignal` 增加两个字段：

- `published time.Time`——在 `observeScheduledTick` 回调内打的时间戳。
- `duration time.Duration`——tick 自身耗时，服务端已经作为参数传进回调，此前未向下传递。

失败信息从

```
measured tick 74: server input boundary 已错过 50ms tick deadline
```

改为带四段分解加一项队列深度：

- **总耗时** `now - scheduled`——超了多少
- **tick 自身** `duration`——服务端执行该 tick 花了多久
- **调度→发布** `published - scheduled`——服务端侧总共花了多久
- **发布→收到** `now - published`——信号从入队到被取出花了多久
- **收到时的队列深度** `len(epoch.signals)`

## 5. 队列深度是最有判别力的一项

它单独一项就能坐实或排除机制三：

- **深度 > 0** ⇒ 缓冲里还压着别的信号 ⇒ 测试 goroutine 确实落后了 ⇒ 测量无效。
- **深度 = 0** ⇒ 测试侧是及时的，时间花在服务端侧 ⇒ 看 `duration` 与"调度→发布"哪一段大。

其余四项负责在"深度 = 0"时进一步区分"tick 自身慢"与"回调链路被晾"。

## 6. 拿到数据后怎么判

| 数据形态 | 结论 | 下一步 |
| --- | --- | --- |
| 队列深度 > 0 | 测量无效，测试侧落后 | 做"测量无效 vs 越界"分离：探针区分两种返回，界限断言仍是硬失败，测量无效则有限重试后跳过并大声记录 |
| 深度 = 0，`duration` 接近或超过 50ms | **服务端真的慢** | 真性能问题，独立调查，**不得**用跳过掩盖 |
| 深度 = 0，`duration` 小，调度→发布大 | 服务端回调链路被晾 | 同样走"测量无效"分离 |

**"测量无效则重试"必须与"断言失败则重试"在代码里泾渭分明。** 前者是承认这次没测成，后者是掩盖回归。二者混淆会让门禁退化成"红了就再跑一次"。这条在实施时是硬约束，不是建议。

## 7. 范围

**范围内**

- `benchmarkServerTickSignal` 增加 `published` 与 `duration` 字段。
- `observeScheduledTick` 填充这两个字段。
- 失败路径输出五项分解。

**非目标**

- **任何判定逻辑的改变。** 50ms 不放宽，界限断言不动，通过与失败的条件与改动前逐字一致。**本变更不修任何红灯**，只让下一次红说清楚话。
- "测量无效 vs 越界"的分离——那是拿到数据之后的下一个变更，现在做等于据假说施工。
- `transport closed`（另一条独立线，8 条失败里占 3 条，根因未知）。
- 分支保护开关（GitHub 设置，需在假失败降下来后手动开启）。

## 8. 成功判据

本变更**无法**通过"CI 变绿"来验证——它不修任何东西。判据是：

1. 成功路径零行为改动、零额外开销：`duration` 与 `published` 只在失败信息里被读取。
2. 本地跑 `TestScenarioV7...` 仍通过，耗时与改动前同量级。
3. 下一次 CI 上该测试变红时，报错能直接回答"这 50 毫秒被谁吃掉了"。

第 3 条只能等，不能在本地制造——本地复现不出 CI 的失败形态（上一个变更已实测确认：`GOMAXPROCS=1` 是悬崖不是梯度，满载多核也复现不出）。

**因此必须用单元测试证明分解逻辑本身是对的**——一个从不执行的诊断分支等于没写。

做法：把分解信息的组装抽成一个纯函数（输入 `scheduled` / `published` / `duration` / 队列深度 / 当前时刻，输出消息字符串），对它直接写单测：构造一个 `scheduled` 已过期 100ms 的信号，断言输出里五项数值都正确且可读。

**不得**用"临时把 `fixedBenchmarkFrameDuration` 改小"来触发失败：该常量不是孤立旋钮，还被 `probe.roster.Advance`（`multiplayer_benchmark.go:199`）与客户端帧预算（`benchmark.go:681`）使用，改它会连带改变无关行为，验证结果不可信。这是设计自审时发现并否决的方案。

抽成纯函数还有个附带好处：分解逻辑与探针运行解耦，未来"测量无效 vs 越界"分离时可以直接复用同一份判据。

### 实测结果（Task 1–2 完成后回填）

**变异验证（判据 3：纯函数单测确实在测东西）**

把 `formatTickBoundaryOverrun` 里 `signal.published.Sub(signal.scheduled)`
与 `now.Sub(signal.published)` 两个实参对调后重跑
`TestFormatTickBoundaryOverrunReportsEachSegment`，测试在"调度→发布 30ms"
这一条具体断言上变红（`分解缺少 "调度→发布 30ms"`），不是笼统的整体失败；
另一条覆盖"发布时刻缺失"分支的测试不受影响，仍然通过——符合预期，它走的
是另一个分支。说明单测确实独立区分了"调度→发布"与"发布→收到"两段数值，
不是靠总耗时或宽松匹配蒙混过关。恢复对调后 `git diff` 干净，重跑转绿。

**判据 1（成功路径零开销）**

```
go test ./cmd/mcgo -count=1        → ok  14.777s
```

**判据 2（本地仍通过，耗时同量级）**

```
go test ./cmd/mcgo -run "^TestScenarioV7EightSessionServerProbeIsRealAndBounded$" -count=3
→ ok  33.721s
```

单次约 11 秒，与改动前本地基线（§1："本地连跑 5 次全过，每次约 11 秒"）
同量级，耗时哨兵未响，未见新增字段填充进入热路径。

**订正**：本节回填前，§8 判据 2、3 均为待验证的计划表述；以上为 Task 1–2
实施后的实测确认，均与设计预期一致，无需推翻已写内容。判据 3（"下一次 CI
上该测试变红时，报错能直接回答……"）仍待 CI 侧下一次红灯验证，不因本次回填
而改变——**本变更不修任何红灯，ScenarioV7 会继续红，直到拿到分解数据后再
决定按 §6 判定表做哪一种修复。**
