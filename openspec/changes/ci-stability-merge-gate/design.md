> 完整证据链、普查数据与被否决的替代方案见
> `docs/superpowers/specs/2026-08-07-ci-stability-merge-gate-design.md`。
> 本文件只记录实现选择。

## 分类判据为什么可以机械执行

四类期限里，"缺席断言"与"超时触发断言"的断言形态是同一个：

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

## 期限的三种语法形态

只查其中一种会严重低估。`internal/server` 的分布：

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

## 两处顺序假设修复

**`persistence_integration_test.go`**：`moveViewToUnvisitedChunk` 发送输入后按 tick 数等待，然后假设输入都已生效。实测 `LastInputSequence` 为 175/404/0 而 `WorldTimeTicks` 为 671/694/709——tick 推进了，输入没跟上。改为等待 `LastInputSequence` 达到目标值。

**`furnace_publication_test.go`**：等待循环以 `opened.Furnace.Generation != 0` 退出并取批次最后一条状态，但满足该条件的第一条状态未必已反映放料结果。改为让退出条件直接表达真正要等的东西。

两者都只改测试。**若深入后发现根因在产品代码，停手另开 change**——这两个测试从未在 CI 上红过，只在 `GOMAXPROCS=1` 下暴露，不值得为它们扩大本变更的范围。

## 验证方法

`GOMAXPROCS=1` 作为慢 runner 的确定性模拟。改动前基线：

```
GOMAXPROCS=1 go test ./internal/server -count=1
--- FAIL: TestDropSurvivesShutdownAndRestart               (5.21s)
--- FAIL: TestDroppedItemSurvivesShutdownAndRestart        (5.23s)
--- FAIL: TestAuthoritativeMiningMemoryLifecycle           (5.02s)
--- FAIL: TestOpenFurnaceSendsStateOnlyToViewer            (0.04s)
--- FAIL: TestWorldPersistsAcrossRestartAndGeneratorUpgrade (0.17s)
```

前三个是活性超时，抬高期限直接命中；后两个是亚秒失败，由顺序假设修复处理。

**局限必须记录**：`GOMAXPROCS=1` 压的是并行度，CI 压的是绝对速度与 I/O，两者不同构。全绿是必要条件而非充分条件，落地后仍需观察 CI 连绿次数才能动分支保护。

## 被否决的替代方案

**环境变量倍率旋钮**（`MCGO_TEST_DEADLINE_SCALE`，本地 1×、CI 4×）：最初的设计，在论证"抬高放弃期限零成本"后否决。它买来的收益不存在，却让本地与 CI 跑两套行为。

**只改历史上实际红过的那几处**：diff 最小，但剩下的秒档站点仍是按 M5 调的字面量，下一个慢 runner 日会换一批测试红。历史数据支持这个判断——六次红里失败测试几乎次次不同。

**给 tick 循环注入假时钟**：唯一的正解，但需要重写整套 `internal/server` 集成测试，改动本身的回归风险超过收益。记录为后续里程碑。

**重试失败的测试**：会把真回归一并掩盖，直接否决。
