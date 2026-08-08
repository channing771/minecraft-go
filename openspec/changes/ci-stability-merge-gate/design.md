> 完整证据链、普查数据与被否决的替代方案见
> `docs/superpowers/specs/2026-08-07-ci-stability-merge-gate-design.md`。
> 本文件只记录实现选择。

## 分类判据为什么可以机械执行

五类期限里，"缺席断言"与"超时触发断言"的断言形态是同一个：

```go
if !errors.Is(err, context.DeadlineExceeded) {
    t.Fatalf(...)
}
```

它们要的就是超时发生。活性等待恰好相反——超时即 `t.Fatal`。因此禁改区可以直接 grep：

```bash
grep -rn "context.DeadlineExceeded" internal/server/*_test.go
```

`internal/server` 命中 10 处，分布在 6 个文件（`host_test.go:413`、`integration_test.go:200`、`host_stats_test.go:171`、`persistence_integration_test.go:540`、`publication_test.go:411,426`、`shutdown_test.go:122,192,224,509`）。

**已逐处核对：这 10 处全部落在毫秒档（20ms×7、25ms×2、1ms×1），与秒档零重叠。**

这条性质是整个实现方案的支点：秒档可以机械替换而不必担心误伤，毫秒档必须人工逐处判。没有这条性质，302 处站点的改动就只能靠人工逐个通读，风险和工作量都不可接受。

## 期限普查：从超集出发，而非枚举语法形态

按语法形态枚举连续漏了四轮：`time.After(...)` 完全没数（101 处）、期限作参数传给助手的形态没覆盖到（31 处）、
单参数助手调用被漏掉（2 处，正则要求逗号排除了它）、`time.NewTimer(...)` 当放弃期限用被过滤器当周期性用途误排除
（4 处）。每一轮都以为这次齐了，结果又漏一种——**语法形态无法穷尽语义类别**，模式匹配得出的清单不能被当作完整清单。

因此普查改为从"所有时长字面量"这一超集出发逐处判定：`grep -rn "time\.\(Second\|Millisecond\|Minute\)" internal/server/*_test.go`
命中 79 处（含 6 处常量定义本身），73 处真实站点全部给出"已改动"或"保留及依据"的判定。这个集合按构造不可能漏，
代价是必须人工逐处过一遍。完整证据链见 `docs/superpowers/specs/2026-08-07-ci-stability-merge-gate-design.md` §5。

下表是各语法形态的相对规模，仅供参考，**不是**完整清单：

| 形态 | 数量 |
| --- | --- |
| `time.After(...)` | 101 |
| `context.WithTimeout(...)` | 98 |
| `time.Now().Add(...)` | 73 |
| 嵌套/复合写法 | 30 |

`time.After` 最多也最容易被漏，其中 77 处是 `time.After(time.Second)`——1 秒的活性等待，既最密集又最紧。

## 命名常量

| 常量 | 取值 | 覆盖 | 余量 |
| --- | --- | --- | --- |
| `shortWaitDeadline` | `5s` | 原 100ms–500ms 的活性站点 | 10×–50× |
| `waitDeadline` | `30s` | 原 1s–5s | 6×–30× |
| `longWaitDeadline` | `60s` | 原 10s–30s | 2×–6× |

**1 秒档归 `waitDeadline` 而非 `shortWaitDeadline`。** 它是本包最紧且最密集的一档（77 处 `time.After(time.Second)`），是抖动的首要嫌疑；只抬到 5s 只有 5× 余量，不足以覆盖共享 runner 的减速。

**定义两份，不建共享包。** `internal/server` 的测试跨两个 Go 包——41 个文件是 `package server`，15 个是 `package server_test`——未导出标识符无法跨包共享。三个常量各写两份（`deadline_test.go` 与 `deadline_external_test.go`），两边注释互相指认。

为三个常量新建一个 `internal/` 包、再去 `internal/archcheck/deps_test.go` 登记依赖，机械成本高于它解决的问题。六行重复的漂移风险由"两份定义必须逐字相同"这条评审要求兜住。

**不逐处乘 6**：那会把 `10s` 变成 `60s`、`1s` 变成 `6s`，既保留了原有的随意取值又谈不上统一。对最常见的 `5s` 一档，归并到 `30s` 正好是 6×。

**不建新包**：常量只服务 `internal/server`，放在包内测试文件即可，无需在 `internal/archcheck/deps_test.go` 登记新依赖。`cmd/mcgo` 的 ScenarioV7 只有一处，直接改字面量。等出现第三个消费者再考虑提取。

**不引入环境变量倍率**：活性等待是放弃期限而非 `sleep`，条件成立即返回，抬高它在快机器上零成本。旋钮买来的"本地更快"不存在，代价是本地与 CI 行为分叉。

## 验证方法：没有本地复现手段

**`GOMAXPROCS=1` 曾被选作慢 runner 的 A/B 模拟器，已实测证伪，不得使用。**

| 条件 | 结果 |
| --- | --- |
| `GOMAXPROCS=1`，期限 5s | 失败在 5.2s |
| `GOMAXPROCS=1`，期限抬到 30s | **仍失败在 30.23s** |
| `GOMAXPROCS=2` | `ok` 2.6s |
| `GOMAXPROCS=4` | `ok` 2.4s |
| 默认并行度 + 24 个 spinner，load average 82.9（10 核 8 倍超售） | **全包 `ok` 89s** |

失败输出里 `LastInputSequence` 恒为 `0` 而 `WorldTimeTicks` 正常推进 592→603——服务端在 tick，客户端输入一条都没被消费。这是 goroutine 饿死，不是变慢。

它是**悬崖而非梯度**：1 处永不收敛，2 处秒过。触发的是单核调度饿死，与 CI 的多核慢不同构。而满载多核连一个失败都复现不出。

**结论：本变更不具备本地复现手段。** 验证改为：

1. **推理**：活性等待是放弃期限，抬高在快机器上零成本、零风险。
2. **反向验证**：把常量临时改成 `1ms`，确认对应测试变红——证明期限确实在生效，不是死代码。
3. **统计观察**：落地后连续观察 CI。这是唯一能证明收益的手段，且只能证伪不能证实。

承认"无法本地复现"比伪造一个错误的复现有价值得多。

**局限必须记录**：本地全绿（`go test ./internal/server -race -count=1` 通过）是必要条件而非充分条件——它只证明期限没被改错，证明不了"慢 runner 上不再假失败"。真正的验证是落地后连续观察 CI，见 `docs/superpowers/specs/2026-08-07-ci-stability-merge-gate-design.md` §9 的观察规程。

## 被否决的替代方案

**环境变量倍率旋钮**（`MCGO_TEST_DEADLINE_SCALE`，本地 1×、CI 4×）：最初的设计，在论证"抬高放弃期限零成本"后否决。它买来的收益不存在，却让本地与 CI 跑两套行为。

**只改历史上实际红过的那一处**：CI 上只有 `TestHostRejectsDuplicatePlayerBeforeLoad` 一次是期限耗尽。但剩下的秒档站点仍是按 M5 调的字面量，抬高零成本零风险，没有理由留着。

**用 `GOMAXPROCS=1` 做 A/B**：已实测证伪，见上。它是本变更最初的验证支柱，连续导致三个错误结论（把 `transport closed` 误判为期限问题、把饿死误判为变慢、把饿死的伪装误判为两个独立的测试顺序 bug）。

**给 tick 循环注入假时钟**：唯一的正解，但需要重写整套 `internal/server` 集成测试，改动本身的回归风险超过收益。记录为后续里程碑。

**重试失败的测试**：会把真回归一并掩盖，直接否决。
