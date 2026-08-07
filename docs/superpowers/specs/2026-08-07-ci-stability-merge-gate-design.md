# CI 稳定性与合并门禁设计

日期：2026-08-07
状态：设计已批准，待实施

## 1. 起因

2026-08-07 两条分支相继合入 `main`，各自的 PR 检查都是绿的，合并后的 `main` 连续两次红：

| 运行 | 分支/事件 | 结果 | 失败内容 |
| --- | --- | --- | --- |
| 31196474496 | `codex/m4i-authoritative-celestial-sky` PR | 绿 | — |
| 31196646656 | `visual-verification` PR | 绿 | — |
| 31197146703 | `main`（合入 PR #11） | 红 | `TestHealthSevenSurvivesDiskRestart`、`TestHostRejectsDuplicatePlayerBeforeLoad` |
| 31197258080 | `main`（合入 PR #15） | 红 | 编译失败 + `TestCraftingSurvivesV2DiskRestartAndReconnectOrder` |

两次合并相隔 83 秒，第一次的红灯根本来不及被观察到。

这里面是**两个互相独立的问题**，必须分开治，且有严格的先后顺序。

## 2. P1：语义合并冲突进入 main

### 现象

`visual-verification` 给 `gfx.Texture` 接口增加了 `ReadLayer`；`m4i-authoritative-celestial-sky` 新增了测试替身 `skyTestTexture`。两条分支改的是不同文件，`git merge` 没有任何文本冲突，但合并结果不再满足接口，`go test ./...` 直接编译失败。

### 根因

GitHub 的 `pull_request` 事件检查的是"PR 与**当时的** main"的合并结果。`visual-verification` 的绿灯产生于 16:14，PR #11 在 16:21 落地，PR #15 在 16:22 合并——那个绿灯验证的是一棵已经不存在的树。

这不是疏忽，是流程缺口：**没有任何环节验证过真正要落地的那棵树。**

### 处置

打开分支保护的 **Require branches to be up to date before merging**，配合必需状态检查。main 移动后，PR 必须重新同步并重跑才能合并。

这是 GitHub 仓库设置，不在代码仓库内，需要仓库管理员手动开启。

### 前置约束

**P2 不先解决，这条门禁立不住。** 在约 25% 假失败率下要求"必须绿才能合"，实际效果是训练所有人反复点 re-run，最终以"已知抖动"为由申请豁免——门禁一旦被例行绕过就等于不存在。

## 3. P2：CI 假失败

### 现象

最近 25 次 CI 运行中 6 次红，失败测试几乎次次不同：

| 运行 | 失败测试 |
| --- | --- |
| 31108168593 | `TestScenarioV7EightSessionServerProbeIsRealAndBounded` |
| 31143784278 | 同上 |
| 31144256852 | 同上 |
| 31169485835 | `TestTCPPlayerAndWorldFailureMatrixProtocolVersionAndUnknownPacket` |
| 31197146703 | `TestHealthSevenSurvivesDiskRestart`、`TestHostRejectsDuplicatePlayerBeforeLoad` |
| 31197258080 | `TestCraftingSurvivesV2DiskRestartAndReconnectOrder` |

失败集合每次不同，是期限抖动的特征而非回归的特征。全部落在 `internal/server` 与 `cmd/mcgo`。

### 期限普查

期限有**三种语法形态**，必须一起数，只查其中一种会严重低估：

```bash
grep -rhoE "context\.WithTimeout\([^,]*, [^)]*\)|time\.Now\(\)\.Add\([^)]*\)|time\.After\([^)]*\)" --include='*_test.go' <包>
```

| 包 | 期限站点 |
| --- | --- |
| `internal/server` | **302** |
| `internal/network` | 36 |
| `cmd/mcgo` | 18 |
| `internal/client` | 8 |

`internal/server` 内另有 75 个 `wait*`/`assert*` 助手。

三种形态在 `internal/server` 的分布是 `context.WithTimeout` 98、`time.Now().Add` 73、`time.After` 101（另有 30 处嵌套/复合写法）。**`time.After` 是最大的一群且最容易被漏**：其中 77 处是 `time.After(time.Second)`——1 秒的活性等待，既最多又最紧，是抖动的首要嫌疑。

### 核心判据：三类期限，处置完全不同

这 299 处**不是一类东西**。混为一谈、统一抬高，就会在治理抖动的同时悄悄阉割真门禁。

| 类别 | 形态 | 例子 | 处置 |
| --- | --- | --- | --- |
| **活性等待** | 轮询到条件成立，到点 `t.Fatal` | `waitReady` 的 5s、`waitClientReadyFor` 的 10s、`waitIntegrationState` 的 5s | **抬高** |
| **缺席断言** | 等一小段确认什么都没发生 | `assertNoServerMessage` 的 20ms | **不动** |
| **超时触发断言** | 故意给极短期限，断言超时确实发生 | `shutdown_test.go` 用 20ms 上下文验证 Shutdown 超时后冻结 Step | **不动** |
| **性能门禁** | 测量耗时，断言小于上限 | `TestPerformanceThresholdsRejectTickP99AtTenMilliseconds` | **绝不动** |

判据不靠人工判断，**可 grep 机械识别**：

后两类的断言形态一律是 `errors.Is(err, context.DeadlineExceeded)`——它们要的就是超时发生。活性等待恰好相反，超时即 `t.Fatal`。

```bash
grep -rn "context.DeadlineExceeded" internal/server/*_test.go
```

`internal/server` 内命中 10 处，分布在 6 个文件。**这 10 处及其所属的期限字面量是禁改区**。抬高它们不会让断言失败（门永远不开，仍会超时），但会把 20ms 变成 30s，凭空给测试套件加上分钟级耗时——纯粹的浪费，且掩盖了这些站点的真实意图。

### 关键结论：抬高活性等待在快机器上是零成本的

活性等待是**放弃期限**（give-up deadline），不是 `sleep`。条件一成立循环立刻返回：

```go
func waitIntegrationState(t *testing.T, connected integrationClient, condition func(network.ServerMessage) bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		message, err := connected.Endpoint.Recv(ctx)
		if err != nil {
			t.Fatalf("Recv after %v: %v", seen, err)
		}
		if condition(message) {
			return   // ← 条件成立即返回，从不等满 5s
		}
	}
}
```

把 5s 改成 30s，**通过的测试耗时一秒不变**；唯一代价是真挂死时报错慢 25 秒。

这个结论砍掉了整套复杂度：**不需要 `MCGO_TEST_DEADLINE_SCALE` 之类的环境变量倍率，不需要新包，不需要 CI 专用配置。** 倍率旋钮的实际作用只是让本地和 CI 跑两套行为——那本身就是 bug 温床，而它买来的"本地更快"是不存在的收益。

### 取值

活性等待**不逐处乘 6**，而是按角色归入一小组命名常量。逐处乘 6 会把 `10s` 变成 `60s`、`20ms` 变成 `120ms`，既保留了原有的随意取值，又谈不上统一。

`internal/server` 测试内的期限实测分布：

| 原值 | `WithTimeout` | 轮询 | `time.After` | 归属 |
| --- | --- | --- | --- | --- |
| `1s` | — | 18 | 77 | 活性 |
| `5s` | 37 | 38 | 1 | 活性 |
| `2s` | 20 | 7 | 10 | 活性 |
| `10s` | 12 | 4 | — | 活性 |
| `3s` | 7 | 2 | 1 | 活性 |
| `15s` / `20s` / `12s` / `30s` | 12 | — | — | 活性 |
| 毫秒档 | 9 | 3 | 11 | **混合，逐处核对** |
| `10min` | 1 | — | — | 已足够长，不动 |

**关键性质：10 处禁改区全部落在毫秒档，秒档零重叠。**（已逐处核对：20ms×7、25ms×2、1ms×1）

这把工作切成了一刀两断的两半：秒档可机械替换，毫秒档必须人工逐处判。

映射到三个命名常量：

| 常量 | 取值 | 覆盖的原字面量 | 余量 | 用途 |
| --- | --- | --- | --- | --- |
| `shortWaitDeadline` | `5s` | 原 100ms–500ms 中的活性站点 | 10×–50× | 单次保存启动等亚秒本机事件 |
| `waitDeadline` | `30s` | 原 1s–5s | 6×–30× | 登录 ready、收到某条消息、库存达到某状态 |
| `longWaitDeadline` | `60s` | 原 10s–30s | 2×–6× | 涉及关服屏障、磁盘重启、八人会话的复合等待 |

对最常见的 `5s` 一档（76 处），这正好是 6×。

**1 秒档必须归 `waitDeadline` 而不是 `shortWaitDeadline`。** 它有 95 处（77 处 `time.After` + 18 处轮询），既最密集又最紧，是抖动的首要嫌疑；只抬到 5s 仅有 5× 余量，覆盖不住共享 runner 的减速。按名字直觉把"1 秒"归进"short"是本设计最容易踩的分类错误。

毫秒档必须逐处核对：它同时混着活性等待与禁改区，是本变更唯一不能机械替换的一档。

命名常量的价值不在于少打字，而在于后人读到的是"活性期限"而不是魔术数字 `5s`，新写的测试自然继承正确值。

三个常量之间保持数量级区分，是为了让"哪一类等待挂了"从报错耗时上就能看出来。

### ScenarioV7 单独处置

`TestScenarioV7EightSessionServerProbeIsRealAndBounded` 是历史上最常红的一个，成因是构造性的：

```go
// cmd/mcgo/multiplayer_benchmark.go
func measureMultiplayerServerProbe(duration time.Duration) (...) {
	if duration < 10*time.Second {
		return ..., fmt.Errorf("多人服务端探针时长 %s < 10s", duration)
	}
```

而测试传入的正是 `10 * time.Second`——**跑在函数允许的下限上，按构造零余量**。

该 `duration` 是采样收集预算，被喂进 `context.WithTimeout(ctx, duration + warmup + 15s)`。真正的界限断言是 `OutboxHighWater > benchmarkOutboxLimit`、`InterestDiff.Samples`、`ticks.Frames`、`PeakRSSBytes` 上限——**加宽预算一个都不动**。

因此把 `10s` 改成 `30s` 不是放宽阈值，符合"不得放宽既有正确性、资源上限或性能门禁"的约束。

## 4. 验证方法：A/B，不靠运气

抖动是统计现象，"跑一次绿了"证明不了任何事。本设计采用可复现的 A/B。

### 慢 runner 模拟器

`GOMAXPROCS=1` 是确定性的慢机器模拟。已实测：

```
GOMAXPROCS=1 go test ./internal/server -run TestCraftingSurvivesV2DiskRestartAndReconnectOrder -count=5
→ 5/5 全红
```

正是 CI 上失败的那个测试，且本机默认并行度下 `-count=10` 全绿。

### 全包基线（改动前）

```
GOMAXPROCS=1 go test ./internal/server -count=1
--- FAIL: TestCraftingSurvivesV2DiskRestartAndReconnectOrder (5.22s)
--- FAIL: TestDropSurvivesShutdownAndRestart                 (5.21s)
--- FAIL: TestDroppedItemSurvivesShutdownAndRestart          (5.23s)
--- FAIL: TestAuthoritativeMiningMemoryLifecycle             (5.02s)
--- FAIL: TestOpenFurnaceSendsStateOnlyToViewer              (0.04s)
--- FAIL: TestWorldPersistsAcrossRestartAndGeneratorUpgrade  (0.17s)
```

**基线是分布而非定值。** 首次记录只有 5 个，`TestCraftingSurvivesV2DiskRestartAndReconnectOrder` 是在后续运行里才稳定出现的——`GOMAXPROCS=1` 只把边缘用例的失败概率推高，没有推到 1。

把某次运行的失败集合写成确定性门禁是方法论错误：它会在边缘用例时有时无时制造假警报，也会诱使人把"复现出同样的集合"误当成验收标准。正确的规则是**缺少表内测试可继续、多出的同类活性超时一并纳入、多出的非活性超时才停手**，而真正的验收标准只有一条：改动后全绿。

### 这个基线暴露了两个群体

前三个在 ~5.2s 失败，是活性超时，抬高期限直接命中。

**后两个在亚秒内失败，抬高期限对它们毫无作用。** 已单独诊断，两个都是真的测试 bug——测试假设了不被保证的顺序：

**`TestWorldPersistsAcrossRestartAndGeneratorUpgrade`**（`persistence_integration_test.go:58`）

三次运行的 `LastInputSequence` 分别是 175、404、0，而 `WorldTimeTicks` 是 671、694、709。tick 确实推进了 600+，但玩家输入只被消费了一部分甚至一条都没有。测试等的是 tick 数，然后假设 600 条输入都已生效、玩家已经移动到未探索区块——在争抢下这个假设不成立，玩家没动，generator B 自然没被调用。

正解是等 `LastInputSequence` 达到目标值，而不是等 tick 数。

**`TestOpenFurnaceSendsStateOnlyToViewer`**（`furnace_publication_test.go:134`）

读到的状态输入槽是空的。等待循环的退出条件是 `opened.Furnace.Generation != 0`，取的是批次里的最后一条状态——但满足 `Generation != 0` 的第一条状态未必已经反映放料结果。

正解是让退出条件直接表达真正要等的东西，而不是用 `Generation` 做代理。

**明确纪律：不得用抬高期限的方式"修"这两个测试。** 亚秒失败被期限改动掩盖是本设计最需要防的失败模式。

**范围阀门**：这两个测试从未在 CI 上红过，只在 `GOMAXPROCS=1` 下暴露。若诊断深入后发现根因在 `internal/server` 产品代码而非测试代码，**停手另开 change**，不在本变更内修——那是权威模拟的正确性问题，与 CI 稳定性不是一回事。

### 工具的局限

`GOMAXPROCS=1` 与 CI runner 的慢法不完全同构：它压的是并行度，CI 压的是绝对速度与 I/O。所以：

- A/B 全绿是**必要条件**，不是充分条件。
- 落地后仍需连续观察 CI，达到 10 次连绿才动分支保护（该次数见 §6，是拍的）。

### 反向验证

把某个活性等待改成 `1ms`，确认对应测试变红，再恢复。证明期限确实在生效，不是改了一段死代码。

## 5. 范围

**范围内**

- `internal/server` 的期限三分类与活性类修正
- `cmd/mcgo` 的 ScenarioV7 收集预算
- 两个亚秒失败的独立诊断
- A/B 基线的记录与复跑方式

**范围外**

- `internal/client`(46)、`internal/network`(24)、`cmd/mcgo` 其余(20) 的期限——见红再改，没有证据支撑的预防性改动只是扩大 diff。
- **消除墙钟依赖**（给 tick 循环注入假时钟、测试显式步进）。这是唯一的正解，但需要重写整套 `internal/server` 集成测试，改动量本身的回归风险超过收益。记录为后续里程碑。
- 分支保护开关本身（GitHub 设置，非代码）。

## 6. 未决问题

- 两个亚秒失败的性质未定，诊断结论出来前不能假设它们属于抖动。
- 分支保护开启前需要多少次连续绿？建议 10 次，但这是拍的，没有数据支撑；可在观察中调整。
