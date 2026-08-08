# CI 稳定性与合并门禁设计

日期：2026-08-07
状态：设计已按实测证据重写；原始诊断被证伪的过程见 §7

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

## 3. P2：CI 假失败的真实构成

最近 25 次 CI 运行中 6 次红。**必须看断言而不是只看测试名**——本设计的第一版就栽在这上面（见 §7）。

| 运行 | 测试 | 耗时 | 实际断言 | 类别 |
| --- | --- | --- | --- | --- |
| 31108168593 | `TestScenarioV7...ProbeIsRealAndBounded` | — | 样本收集不足 | **采样预算** |
| 31143784278 | 同上 | — | 同上 | **采样预算** |
| 31144256852 | 同上 | — | 同上 | **采样预算** |
| 31169485835 | `TestTCPPlayerAndWorldFailureMatrix...` | 0.02s | `wait ready Recv: network: transport closed` | **连接被关闭** |
| 31197146703 | `TestHealthSevenSurvivesDiskRestart` | 1.46s | `wait ready Recv: network: transport closed` | **连接被关闭** |
| 31197146703 | `TestHostRejectsDuplicatePlayerBeforeLoad` | 5.51s | `player did not become ready` | **期限耗尽** |
| 31197258080 | `TestCraftingSurvivesV2DiskRestart...` | 0.12s | `wait ready Recv: network: transport closed` | **连接被关闭** |

三类，不是一类：

- **采样预算**（3 次）：`TestScenarioV7` 的收集预算跑在被调用方允许的下限上。诊断确凿，见 §4。
- **连接被关闭**（3 次）：登录/ready 期间连接断开，耗时 0.02s–1.46s。**抬高期限对它们零作用**。根因未知，见 §6。
- **期限耗尽**（1 次）：`waitReady` 的 5s 轮询期限在慢 runner 上不够。见 §5。

**主导形态是"连接被关闭"，不是期限不足。** 本变更只能处理另外两类。

## 4. 采样预算：ScenarioV7

```go
// cmd/mcgo/multiplayer_benchmark.go
func measureMultiplayerServerProbe(duration time.Duration) (...) {
	if duration < 10*time.Second {
		return ..., fmt.Errorf("多人服务端探针时长 %s < 10s", duration)
	}
```

而测试传入的正是 `10 * time.Second`——**跑在函数允许的下限上，按构造零余量**。这解释了它为什么是历史上最常红的一个。

该 `duration` 是采样收集预算，被喂进 `context.WithTimeout(ctx, duration + warmup + 15s)`。真正的界限断言是 `OutboxHighWater > benchmarkOutboxLimit`、`InterestDiff.Samples`、`ticks.Frames`、`PeakRSSBytes` 上限——**加宽预算一个都不动**。

因此把 `10s` 改成 `30s` 不是放宽阈值，符合"不得放宽既有正确性、资源上限或性能门禁"的约束。

## 5. 期限耗尽：活性等待的余量

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

三种形态在 `internal/server` 的分布是 `context.WithTimeout` 98、`time.Now().Add` 73、`time.After` 101（另有 30 处嵌套/复合写法）。**`time.After` 是最大的一群且最容易被漏**：其中 77 处是 `time.After(time.Second)`。

### 四类期限，处置完全不同

混为一谈、统一抬高，就会在治理假失败的同时悄悄阉割真门禁。

| 类别 | 形态 | 例子 | 处置 |
| --- | --- | --- | --- |
| **活性等待** | 轮询到条件成立，到点 `t.Fatal` | `waitReady` 的 5s、`waitIntegrationState` 的 5s | **抬高** |
| **缺席断言** | 等一小段确认什么都没发生 | `assertNoServerMessage` 的 20ms | **不动** |
| **超时触发断言** | 故意给极短期限，断言超时确实发生 | `shutdown_test.go` 用 20ms 验证 Shutdown 超时后冻结 Step | **不动** |
| **性能门禁** | 测量耗时，断言小于上限 | `TestPerformanceThresholdsRejectTickP99AtTenMilliseconds` | **绝不动** |

判据不靠人工判断，**可 grep 机械识别**：后两类的断言形态一律是 `errors.Is(err, context.DeadlineExceeded)`——它们要的就是超时发生；活性等待恰好相反，超时即 `t.Fatal`。

```bash
grep -rn "context.DeadlineExceeded" internal/server/*_test.go
```

`internal/server` 内命中 10 处，分布在 6 个文件。**这 10 处及其所属的期限字面量是禁改区**，且已逐处核对——全部落在毫秒档，与秒档零重叠。秒档因此可机械替换。

> 注意：引入常量后这条 grep 会返回 11，多出的一行是常量 GoDoc 里解释禁改区时引用的 `context.DeadlineExceeded` 字样，不是断言。做门禁时应排除注释行。

### 抬高活性等待在快机器上是零成本的

活性等待是**放弃期限**（give-up deadline），不是 `sleep`。条件一成立循环立刻返回，因此把 5s 改成 30s，**通过的测试耗时一秒不变**；唯一代价是真挂死时报错慢 25 秒。

这个结论砍掉了整套复杂度：**不需要环境变量倍率旋钮，不需要新包，不需要 CI 专用配置。** 旋钮的实际作用只是让本地和 CI 跑两套行为——那本身就是 bug 温床，而它买来的"本地更快"是不存在的收益。

### 取值

`internal/server` 测试内的期限实测分布：

| 原值 | `WithTimeout` | 轮询 | `time.After` |
| --- | --- | --- | --- |
| `1s` | — | 18 | 77 |
| `5s` | 37 | 38 | 1 |
| `2s` | 20 | 7 | 10 |
| `10s` | 12 | 4 | — |
| `3s` | 7 | 2 | 1 |
| `15s`/`20s`/`12s`/`30s` | 12 | — | — |
| 毫秒档 | 9 | 3 | 11 |
| `10min` | 1 | — | — |

映射到三个命名常量：

| 常量 | 取值 | 覆盖的原字面量 | 余量 |
| --- | --- | --- | --- |
| `shortWaitDeadline` | `5s` | 原 100ms–500ms 中的活性站点 | 10×–50× |
| `waitDeadline` | `30s` | 原 1s–5s | 6×–30× |
| `longWaitDeadline` | `60s` | 原 10s–30s | 2×–6× |

**1 秒档必须归 `waitDeadline` 而不是 `shortWaitDeadline`。** 它有 95 处（77 处 `time.After` + 18 处轮询），既最密集又最紧；只抬到 5s 仅有 5× 余量。按名字直觉把"1 秒"归进"short"是本设计最容易踩的分类错误。

**常量定义两份，不建共享包。** `internal/server` 的测试跨两个 Go 包——41 个文件是 `package server`，15 个是 `package server_test`——未导出标识符无法跨包共享。为三个常量新建 `internal/` 包再去 `internal/archcheck/deps_test.go` 登记依赖，机械成本高于它解决的问题。

### 收益的诚实估计

本项只能消除 CI 上**六分之一**的观察到的失败（`TestHostRejectsDuplicatePlayerBeforeLoad` 那一次）。它的正当性在于零成本与零风险，不在于它能解决假失败问题。

**不得把本变更描述为"修好了 CI"。** 主导形态是 §6 的连接被关闭，未解决。

## 6. 连接被关闭：根因未知，另开变更

三次 CI 失败的断言都是 `wait ready Recv: network: transport closed`，发生在登录/ready 期间，耗时 0.02s–1.46s。

已确认存在一个能关闭 session 的机制：

```go
// internal/server/session.go:251 —— enqueue 永不等待 writer；满队列会关闭慢 session。
default:
	slog.Warn("慢客户端 outbox 已满，关闭 session", "session", current.id)
	current.fail(errSessionOutboxFull)
```

`OutboxCapacity` 默认 512。**但这个机制解释不了亚秒失败**：512 槽位按每 tick 两条消息计需要十几秒才填满，而失败发生在 0.02s 与 0.12s。

因此 outbox 满**不是**已确认的根因，只是一个已确认存在的机制。真实根因需要独立调查，作为单独的 systematic-debugging 变更处理。在根因查清前，任何针对它的改动都是猜。

**这才是合并门禁的真正前置条件。**

## 7. 被证伪的原始诊断（保留作为方法论记录）

本设计的第一版把 CI 假失败**整体**归因为"期限余量不足"，并以 `GOMAXPROCS=1` 作为慢 runner 的确定性 A/B 模拟器。两条都是错的。

### 错误一：只看测试名，不看断言

第一版从 CI 日志里提取了失败的测试名就下了结论。补看断言后才发现四分之三是 `transport closed`，与期限无关。

**教训：失败测试的名字不携带失败的原因。**

### 错误二：`GOMAXPROCS=1` 建模了错误的对象

实测三组数据：

| 条件 | 结果 |
| --- | --- |
| `GOMAXPROCS=1`，期限 5s | 失败在 5.2s |
| `GOMAXPROCS=1`，期限抬到 30s | **仍失败在 30.23s** |
| `GOMAXPROCS=2` | `ok` 2.6s |
| `GOMAXPROCS=4` | `ok` 2.4s |
| 默认并行度 + 24 个 spinner，load average 82.9（10 核 8 倍超售） | **全包 `ok` 89s** |

关键证据在失败输出里：`LastInputSequence` 恒为 `0`，而 `WorldTimeTicks` 正常推进 592→603。**服务端在 tick，客户端输入一条都没被消费**——这是 goroutine 饿死，不是变慢，等多久都没用。

`GOMAXPROCS=1` 是**悬崖而不是梯度**：1 处永不收敛，2 处秒过。它触发的是单核调度饿死，与 CI（多核但慢）不同构。而满载多核连一个失败都复现不出——**本地根本没有 CI 失败形态的复现手段**。

### 错误三：把饿死的伪装当成独立的测试缺陷

第一版据 `GOMAXPROCS=1` 的结果诊断出两个"测试顺序假设 bug"（`TestWorldPersistsAcrossRestartAndGeneratorUpgrade` 与 `TestOpenFurnaceSendsStateOnlyToViewer`），并写进了计划。实测两者在 `GOMAXPROCS=2` 下 `-count=3` 全绿（1.26s）——它们也是同一个饿死缺陷的伪装。

三个错误同一根源：**采信了一个未经校验的模拟器**。

### 修正后的验证方法

没有本地复现手段，因此：

1. **推理**：活性等待是放弃期限，抬高在快机器上零成本、零风险。
2. **反向验证**：把常量临时改成 `1ms`，确认对应测试变红——证明期限确实在生效，不是死代码。
3. **统计观察**：落地后连续观察 CI 运行。这是唯一能证明收益的手段，且它只能证伪不能证实。

**不再声称拥有 A/B 手段。** 承认"无法本地复现"比伪造一个错误的复现要有价值得多。

## 8. 副产物：单核输入饿死

`GOMAXPROCS=1` 下 `internal/server` 的客户端输入投递会完全停滞（`LastInputSequence` 恒为 0），tick 循环照常推进。这是一个**已验证的真实产品并发缺陷**，与 CI 假失败无关（runner 是多核）。

`cmd/mcgod` 面向 Linux 容器构建，1 vCPU 是真实部署场景。**记录为已知问题，暂不修**；专用服务端正式上线前应当查清。

## 9. 范围

**范围内**

- `internal/server` 的期限四分类与活性类修正
- `cmd/mcgo` 的 ScenarioV7 收集预算
- 反向验证与实测数据回填

**范围外**

- **连接被关闭的根因调查**（§6）——独立的 systematic-debugging 变更，是合并门禁的真正前置条件。
- **单核输入饿死**（§8）——记录不修。
- `internal/client`、`internal/network`、`cmd/mcgo` 其余包的期限——见红再改。
- 消除墙钟依赖（假时钟注入）——需重写整套集成测试，回归风险超过收益。
- 分支保护开关本身（GitHub 设置，非代码）。
