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

**P2 不先解决，这条门禁立不住。** 仓库当前红灯率约 24%（最近 25 次运行 6 次红），其中假失败的比例略低于此——6 次红里至少有一次（`31197258080`）包含真实的编译失败，那是真回归，不应计入假失败率的分子。在这个红灯率下要求"必须绿才能合"，实际效果是训练所有人反复点 re-run，最终以"已知抖动"为由申请豁免——门禁一旦被例行绕过就等于不存在。

## 3. P2：CI 假失败的真实构成

> **本节已按实测断言重写（2026-08-07）。** 原版把三次 `TestScenarioV7` 失败归为"样本收集不足"，
> 那是**从代码推出来的猜测，从未与真实失败核对过**。逐条读断言后发现无一如此。订正经过见 §7 错误四。

最近 25 次 CI 运行中 6 次红，另加本变更 PR 检查的 1 次红，合计 **8 条失败**。

| 运行 | 测试 | 耗时 | 实际断言 | 类别 |
| --- | --- | --- | --- | --- |
| 31108168593 | `TestScenarioV7...ProbeIsRealAndBounded` | 7.75s | `measured tick 134: server input boundary 已错过 50ms tick deadline` | **50ms tick 边界** |
| 31143784278 | 同上 | 2.43s | `measured tick 27: ...同上` | **50ms tick 边界** |
| 31144256852 | 同上 | 3.26s | `measured tick 44: ...同上` | **50ms tick 边界** |
| 31234686146 | 同上（本变更 PR 检查） | 4.82s | `measured tick 74: ...同上` | **50ms tick 边界** |
| 31169485835 | `TestTCPPlayerAndWorldFailureMatrix...` | 0.02s | `wait ready Recv: network: transport closed` | **连接被关闭** |
| 31197146703 | `TestHealthSevenSurvivesDiskRestart` | 1.46s | `wait ready Recv: network: transport closed` | **连接被关闭** |
| 31197146703 | `TestHostRejectsDuplicatePlayerBeforeLoad` | 5.51s | `player did not become ready` | **期限耗尽** |
| 31197258080 | `TestCraftingSurvivesV2DiskRestart...` | 0.12s | `wait ready Recv: network: transport closed` | **连接被关闭** |

三类，不是一类：

- **50ms tick 边界**（4 条）：测量循环要求"从服务端调度该 tick 起 50ms 内走到输入边界"，在共享 runner 上错过。断言在 `cmd/mcgo/multiplayer_benchmark.go:376`/`:448`，属**产品代码**。见 §4。
- **连接被关闭**（3 条）：登录/ready 期间连接断开，耗时 0.02s–1.46s。**抬高期限对它们零作用**。根因未知，见 §6。
- **期限耗尽**（1 条）：`waitReady` 的 5s 轮询期限在慢 runner 上不够。见 §5。

**本变更只解决第三类，1 条。** 前两类各自需要独立变更。

注意"失败条数"不等于"红灯次数"：运行 `31197146703` 一次红里含两条失败（一条在范围内、一条不在），修完仍是红。

## 4. ScenarioV7 的真实失败：50ms tick 边界

> **本节整节推翻重写（2026-08-07）。** 原版把这四次失败归为"采样预算不足"，据此把预算从 `10s` 放宽到 `30s`。
> 实测四次失败的断言全是 50ms tick 边界，且全部发生在 2.43–7.75 秒——**远在原预算之内，预算从来不是绑定约束，放宽它什么都没修**。

### 真实断言

```go
// cmd/mcgo/multiplayer_benchmark.go:371
func benchmarkServerInputDeadline(signal benchmarkServerTickSignal) (time.Time, error) {
	deadline := signal.scheduled.Add(fixedBenchmarkFrameDuration)  // +50ms
	if !time.Now().Before(deadline) {
		return time.Time{}, errors.New("server input boundary 已错过 50ms tick deadline")
	}
```

测量循环走 `benchmarkServerMeasuredTicks = 200` 个 tick，每个 tick 都要求：**从服务端调度该 tick 的时刻起，50 毫秒内测试侧必须走到输入边界**。四次失败分别停在第 27、44、74、134 个 tick。

在满载的共享 runner 上，测试 goroutine 被调度器晾超过 50 毫秒毫不稀奇。这**确实是期限余量问题**——只不过期限在 `multiplayer_benchmark.go` 这个**非测试文件**里，落在本变更"产品代码零改动"自设边界之外。

### 那次预算放宽为什么无效

`duration` 只喂进 `context.WithTimeout(ctx, duration + warmup*50ms + 15s)`，是整个探针运行的上限。测量 200 个 50ms tick 至少需要 10 秒墙钟，而原来的运行上限已是 `10 + warmup + 15 ≈ 25s`——**本就有约 15 秒余量**。四次失败都在 7.75 秒以内触发，两种预算下结果完全一样。

原版据"函数下限是 10s、测试恰好传 10s"推断出"按构造零余量"，逻辑本身成立，但它描述的是一个**从未发生过的失败模式**。

### 现状

`10s → 30s` 的改动已随本变更合入，**保留但不计入收益**：预算等于被调用方下限确实是不健康的构造（`spec.md` 的「采样收集预算必须留有余量」作为通用规则仍然成立），只是它不是这四次红的成因。相关代码注释已随本次订正改为如实表述。

**ScenarioV7 会继续红**，直到 50ms tick 边界被单独处理。

## 5. 期限耗尽：活性等待的余量

### 期限普查：不要用正则枚举，用超集

本设计先后尝试用正则枚举期限的语法形态，**连续漏了四次**：

| 轮次 | 漏掉的形态 | 处数 |
| --- | --- | --- |
| 1 | `time.After(...)`（完全没数） | 101 |
| 2 | 期限作参数传给助手（多参数调用） | 31 |
| 3 | 单参数助手调用（无逗号，正则要求逗号） | 2 |
| 4 | `time.NewTimer(...)` 当放弃期限（被过滤器当周期性用途排除） | 4 |

每一轮都是"再加一条正则"，每一轮都以为这次齐了。**语法枚举无法穷尽语义类别**——这是本设计学到的最实用的一条。

正确做法是从超集出发，逐处判定：

```bash
grep -rn "time\.\(Second\|Millisecond\|Minute\)" internal/server/*_test.go
```

命中 79 处（含 6 处常量定义本身），73 处真实站点。这个集合按构造不可能漏，代价是必须人工逐处给出"改"或"为什么不改"的依据。走完它才能说"这个包里没有过紧的活性等待"，而不是"我们的正则没抓到别的"。

原先按语法形态分类的普查数据保留在下方，作为各形态相对规模的参考——但它**不是**完整清单。

```bash
# 前三种：直接构造期限
grep -rhoE "context\.WithTimeout\([^,]*, [^)]*\)|time\.Now\(\)\.Add\([^)]*\)|time\.After\([^)]*\)" --include='*_test.go' <包>

# 第四种：期限作为参数传给测试助手（最易漏）
grep -rnoE "[a-zA-Z][a-zA-Z0-9_]*\([^()]*, *[0-9]* ?\*? ?time\.(Second|Millisecond|Minute)\)" --include='*_test.go' <包> \
  | grep -vE "context\.With(Timeout|Deadline)|time\.(After|Now|Sleep|NewTimer|NewTicker)"
```

第四种在 `internal/server` 命中 32 处（实测订正：brief 原估 31 处，正则里 `[^()]*` 排除了实参含括号的调用，漏掉 `connectTask16ConcurrentClients(t, listener.Addr(), requests, 10*time.Second)` 一处）：`shutdownWithDeadline` 27（brief 原估 28，逐处核对后订正）、`nextTimer` 3、`connectTask16ConcurrentClients` 2。

**第五种：对时长数值本身的断言，不是期限。** `heartbeat_test.go` 使用假时钟，`clock.nextTimer(t, 5*time.Second).fire()` 断言的是"被测代码调度了一个 5 秒的定时器"。它在语法上与第四种无法区分，**只能靠读断言意图分辨**；盲替换会让测试静默失去意义而不报错——这是本变更最危险的一类误改，且 `context.DeadlineExceeded` 判据抓不到它。

| 包 | 期限站点 |
| --- | --- |
| `internal/server` | **302** |
| `internal/network` | 36 |
| `cmd/mcgo` | 18 |
| `internal/client` | 8 |

三种形态在 `internal/server` 的分布是 `context.WithTimeout` 98、`time.Now().Add` 73、`time.After` 101（另有 30 处嵌套/复合写法）。**`time.After` 是最大的一群且最容易被漏**：其中 77 处是 `time.After(time.Second)`。

### 五类期限，处置完全不同

混为一谈、统一抬高，就会在治理假失败的同时悄悄阉割真门禁。

| 类别 | 形态 | 例子 | 处置 |
| --- | --- | --- | --- |
| **活性等待** | 轮询到条件成立，到点 `t.Fatal` | `waitReady` 的 5s、`waitIntegrationState` 的 5s | **抬高** |
| **缺席断言** | 等一小段确认什么都没发生 | `assertNoServerMessage` 的 20ms | **不动** |
| **超时触发断言** | 故意给极短期限，断言超时确实发生 | `shutdown_test.go` 用 20ms 验证 Shutdown 超时后冻结 Step | **不动** |
| **性能门禁** | 测量耗时，断言小于上限 | `TestPerformanceThresholdsRejectTickP99AtTenMilliseconds` | **绝不动** |
| **时长值断言** | 断言被测代码使用了某个时长 | `clock.nextTimer(t, 5*time.Second)` | **绝不动** |

前四类的判据**可 grep 机械识别**：第二、三类的断言形态一律是 `errors.Is(err, context.DeadlineExceeded)`——它们要的就是超时发生；活性等待恰好相反，超时即 `t.Fatal`。

**第五类抓不到。** 时长值断言在语法上与普通的助手参数完全一致，且不涉及 `context.DeadlineExceeded`。它只能靠读断言意图分辨，是本变更唯一没有机械判据的类别，也是误改后最隐蔽的一类——测试仍然通过，只是不再测它本来要测的东西。凡遇到"把时长传给测试助手"的形态，**必须读该助手的实现**确认它是拿去做期限还是拿去做比较。

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

### 实测结果：套件耗时、逐处判定与反向验证

**套件耗时改前/改后对比**（`go test ./internal/server -race -count=1`）：

| 阶段 | 耗时 |
| --- | --- |
| Task 1 基线（引入常量后，尚未替换站点） | 115.256s |
| Task 2 后（秒档 272 处机械替换完成） | 115.624s |
| Task 3 期间五次跑 | 落在 114.2s–115.9s 区间 |

耗时全程无系统性变化，"耗时显著变长"这一误分类哨兵从未响过——没有站点因为被误判为活性等待而白等。

**Task 3 超集审计与分类结论。** `grep -rn "time\.\(Second\|Millisecond\|Minute\)" internal/server/*_test.go` 命中 79 处，减去 6 处常量定义本身，得到 **73 处真实待审站点**，逐处判定：

| 组 | 站点数 | 判定为活性等待并抬高 | 保持原值 |
| --- | --- | --- | --- |
| 毫秒档 | 24 | 6 | 18 |
| 助手参数形态 | 32 | 29 | 3（时长值断言与被测配置，绝不动） |
| 超集审计新增 | 6 | 6 | 0 |
| 小计（活性等待抬高） | — | **41** | — |

另有 **7 处 `config.ShutdownTimeout` 死配置被删除**——这 7 行不是活性等待，抬高与否都不影响任何计时行为（`ShutdownTimeout` 在这些测试路径上从未被消费），删除是清理死代码，**不修复任何抖动**，不计入"抬高"也不计入假失败修复。

**Task 5 Step 1 反向验证。** 把两份 `deadline_test.go` / `deadline_external_test.go` 中的 `waitDeadline` 临时改成 `1 * time.Millisecond` 后，`go test ./internal/server -count=1` 出现 **99 处 `--- FAIL`**，证明该常量确实被大量测试引用、不是死代码；恢复后 `git diff --stat` 两份文件均无输出。

### 收益的诚实估计

本节讨论的期限治理**最多**只能消除 §3 表格里 **8 条失败中的 1 条**（`TestHostRejectsDuplicatePlayerBeforeLoad` 那一条期限耗尽），而且连这一条都不确定——见下。

注意这是失败条数不是运行次数：这条失败所在的那次运行（`31197146703`）里还有另一条独立失败（`TestHealthSevenSurvivesDiskRestart`，transport closed），即使期限耗尽这条被消除，**那次运行仍会因 transport closed 保持红**。也就是说，本变更**不能让任何一次已观察到的红灯变绿**。

它的正当性在于零成本、零风险，以及把"慢"与"挂"这个此前无法区分的信号变成决定性的（见下），不在于它能解决假失败问题。

**不得把本变更描述为"修好了 CI"。** 8 条失败里 4 条是 §4 的 50ms tick 边界、3 条是 §6 的连接被关闭，两类都未解决。

### 期限耗尽的失败无法区分"慢"与"挂"

一次发生在 5s 期限上、耗时 5.51s 的失败，可能是余量不足（再给一点时间就会成功），也可能是真的挂起（碰巧被期限截断）。**从失败本身看不出是哪一种。**

实测佐证：Task 3 期间观察到 `TestDropSurvivesShutdownAndRestart` 在 `-run "Shutdown|ServerRun|Metadata"` 过滤集下偶发失败，耗时 30.09s——正好跑满抬高后的 `waitDeadline`。该测试单独重复 20 次、同一过滤集重复 3 次均通过，是低频事件。它挂了 30 秒仍未完成，说明**抬高期限对它无效**：它不是慢，是挂。

这类挂起在抬高期限之前会伪装成"期限余量不足"的样子——1s 或 5s 的期限把它截断，看起来就是个普通的超时。

### 这带来一个意外的收益：信号变得决定性

抬高期限本身不修复挂起，但它把一个**模糊信号变成了决定性信号**：

- 抬高后仍在 30s/60s 上失败 ⇒ **确定是挂起**，不是余量问题，必须查根因。
- 抬高后该类失败消失 ⇒ 确定是余量问题，已解决。

这是本变更除消除假失败之外的第二项价值，且可能比第一项更大：它让"期限类失败"从此可以被明确归因，而不必每次都在"再等等就好"和"有东西卡住了"之间猜。

CI 观察阶段必须利用这条性质——见 §9 的观察规程。

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

前三个错误同一根源：**采信了一个未经校验的模拟器**。

### 错误四：把自己刚立的规矩犯了一遍（2026-08-07 发现）

前三个错误的修正，产出了本变更的第一条 Requirement——**「失败归因必须依据断言而非测试名」**，其 Scenario 明写「每条 MUST 附带断言原文与耗时」。

而 §3 表格里三次 `TestScenarioV7` 的断言与耗时，原版填的是「样本收集不足」与「—」。

**那个「样本收集不足」不是读来的，是推来的。** 我读了 `measureMultiplayerServerProbe` 的 `duration < 10s` 下限，看到测试恰好传 `10s`，构造出一个自洽的故事（"按构造零余量"），然后再没和真实失败核对过。耗时那一栏更被写成"CI 日志未记录，不可得"——**它离一条 `gh run view` 只有一步**，终审还专门追问过这一栏。

实测四次失败（含本变更 PR 检查那次）的断言全是 `server input boundary 已错过 50ms tick deadline`，耗时 2.43s–7.75s。据此做的 Task 4 整个是无效功。

这个错误和前三个不同源，也更难防：

- 前三个是**工具没校验**——可以靠"复现手段必须先自证同构"这类规则拦住。
- 这一个是**规则写了但自己没执行**。我对 `transport closed` 那组回头读了断言，对 ScenarioV7 这组没有，因为我"已经有一个说得通的解释了"。

**一个自洽的因果故事，是停止求证的最强诱因。** 规则拦不住这个——`transport closed` 那组我遵守了规则，ScenarioV7 这组我以为不需要。真正的防线是：**凡是写进产物的断言原文，必须能指出它是从哪条日志读来的**；写不出来源的，就标成"未核实"，而不是填一个听起来合理的。

### 修正后的验证方法

没有本地复现手段，因此：

1. **推理**：活性等待是放弃期限，抬高在快机器上零成本、零风险。
2. **反向验证**：把常量临时改成 `1ms`，确认对应测试变红——证明期限确实在生效，不是死代码。实测（Task 5 Step 1）：`waitDeadline` 改成 `1 * time.Millisecond` 后 `go test ./internal/server -count=1` 出现 99 处 `--- FAIL`，恢复后两份文件 `git diff` 均干净。
3. **统计观察**：落地后连续观察 CI 运行。这是唯一能证明收益的手段，且它只能证伪不能证实。

**不再声称拥有 A/B 手段。** 承认"无法本地复现"比伪造一个错误的复现要有价值得多。

## 8. 副产物：单核输入饿死

`GOMAXPROCS=1` 下 `internal/server` 的客户端输入投递会完全停滞（`LastInputSequence` 恒为 0），tick 循环照常推进。这是一个**已验证的真实产品并发缺陷**，与 CI 假失败无关（runner 是多核）。

`cmd/mcgod` 面向 Linux 容器构建，1 vCPU 是真实部署场景。**记录为已知问题，暂不修**；专用服务端正式上线前应当查清。

## 9. CI 观察规程

本变更的收益无法在本地证明，只能靠 CI 统计观察。观察时**必须按断言分类**，否则会得出错误结论：

| 观察到的现象 | 结论 |
| --- | --- |
| `transport closed` 仍然出现 | **预期之内**，不在本变更范围内（§6） |
| `transport closed` 消失了 | **不得归功于本变更**，它没碰任何相关代码 |
| **ScenarioV7 报 `50ms tick deadline` 仍然出现** | **预期之内**，不在本变更范围内（§4）。已实测该形态在本变更 PR 检查中再次出现 |
| ScenarioV7 报 `50ms tick deadline` 消失了 | **不得归功于本变更**，预算放宽对它无效 |
| 期限类失败（`player did not become ready` 等）消失 | 本变更生效 |
| **期限类失败仍出现，但耗时变成 30s/60s** | **确定是挂起而非余量问题**（§5），必须开独立调查，不得再抬期限 |
| ScenarioV7 改为在界限断言上失败（`OutboxHighWater`、`PeakRSSBytes` 等超上限） | **真回归**，与预算无关，必须查根因 |

倒数第二行是本变更为后续调查提供的新能力：在此之前，一次 5.51s 的期限失败无法判断是慢还是挂；在此之后，30s 上的失败就是确凿的挂起证据。

**统计观察的第一份数据（本变更自己的 PR 检查，运行 `31234686146`）已经到了：ScenarioV7 在 4.82s 报 `measured tick 74: server input boundary 已错过 50ms tick deadline`。** 按上表第三行，这是预期之内、不算本变更失败——但它同时证实了 §4 的订正结论：预算放宽对这一形态无效。

## 10. 范围

**范围内**

- `internal/server` 的期限五分类与活性类修正
- `cmd/mcgo` 的 ScenarioV7 收集预算
- 反向验证与实测数据回填

**范围外**

- **连接被关闭的根因调查**（§6）——独立的 systematic-debugging 变更，是合并门禁的真正前置条件。
- **单核输入饿死**（§8）——记录不修。
- `internal/client`、`internal/network`、`cmd/mcgo` 其余包的期限——见红再改。
- 消除墙钟依赖（假时钟注入）——需重写整套集成测试，回归风险超过收益。
- 分支保护开关本身（GitHub 设置，非代码）。
