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

| 包 | `time.Second`/`time.Millisecond` 字面量 |
| --- | --- |
| `internal/server` | 204 |
| `internal/client` | 46 |
| `internal/network` | 24 |
| `cmd/mcgo` | 20 |
| 其余 | 3 |
| **合计** | **299** |

`internal/server` 内另有 136 处 `context.WithTimeout`、75 个 `wait*`/`assert*` 助手。

### 核心判据：三类期限，处置完全不同

这 299 处**不是一类东西**。混为一谈、统一抬高，就会在治理抖动的同时悄悄阉割真门禁。

| 类别 | 形态 | 例子 | 处置 |
| --- | --- | --- | --- |
| **活性等待** | 轮询到条件成立，到点 `t.Fatal` | `waitReady` 的 5s、`waitClientReadyFor` 的 10s、`waitIntegrationState` 的 5s | **抬高** |
| **缺席断言** | 等一小段确认什么都没发生 | `TestOpenFurnaceSendsStateOnlyToViewer` | **不动**（抬高只是浪费时间） |
| **性能门禁** | 测量耗时，断言小于上限 | `TestPerformanceThresholdsRejectTickP99AtTenMilliseconds` | **绝不动** |

判据可机械执行：看断言的对象是"条件成立了没"还是"耗时超了没"。

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

| 常量 | 取值 | 覆盖的原字面量 | 用途 |
| --- | --- | --- | --- |
| `shortWaitDeadline` | `5s` | 原 20ms–1s | 单个 tick 推进、单次保存启动等本地快事件 |
| `waitDeadline` | `30s` | 原 2s–5s | 登录 ready、收到某条消息、库存达到某状态 |
| `longWaitDeadline` | `60s` | 原 10s–30s | 涉及关服屏障、磁盘重启、八人会话的复合等待 |

对最常见的 `5s` 一档，这正好是 6×。

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
--- FAIL: TestDropSurvivesShutdownAndRestart               (5.21s)
--- FAIL: TestDroppedItemSurvivesShutdownAndRestart        (5.23s)
--- FAIL: TestAuthoritativeMiningMemoryLifecycle           (5.02s)
--- FAIL: TestOpenFurnaceSendsStateOnlyToViewer            (0.04s)
--- FAIL: TestWorldPersistsAcrossRestartAndGeneratorUpgrade (0.17s)
```

### 这个基线暴露了两个群体

前三个在 ~5.2s 失败，是活性超时，抬高期限直接命中。

**后两个在亚秒内失败，抬高期限对它们毫无作用。** `TestOpenFurnaceSendsStateOnlyToViewer` 从名称看属于缺席断言（class 2）。这两个必须单独诊断，可能是调度顺序依赖，也可能是真 bug。

**明确纪律：不得用抬高期限的方式"修"这两个测试。** 亚秒失败被期限改动掩盖是本设计最需要防的失败模式。

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
