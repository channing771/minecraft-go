# 50ms tick 边界时间分解 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 `TestScenarioV7EightSessionServerProbeIsRealAndBounded` 的 50ms tick 边界失败报出时间分解与队列深度，使下一次 CI 变红时能直接回答"这 50 毫秒被谁吃掉了"。

**Architecture:** 给 `benchmarkServerTickSignal` 补 `published` 与 `duration` 两个字段，在 `observeScheduledTick` 里填充；把失败信息的组装抽成纯函数以便直接单测；失败路径改用该函数。判定逻辑零改动。

**Tech Stack:** Go 1.26，标准库 `time` / `fmt`。不新增任何依赖。

## 本变更不修任何红灯

它不改变任何通过/失败的条件，只让已有的失败说清楚话。成功判据见设计文档 §8——**不能用"CI 变绿"来验证**。

## Global Constraints

- Go 命令一律经 `zsh -ic 'gvm use go1.26.0 >/dev/null && <cmd>'` 执行。
- **测试一律前台跑**，用 Bash 工具的 `timeout` 参数给足时间（`TestScenarioV7...` 单次约 11 秒，`cmd/mcgo` 全包更久，给 `timeout: 600000`）。不要用 `run_in_background`，不要用 monitor。
- **50ms 不得放宽。** `fixedBenchmarkFrameDuration` 保持 `50 * time.Millisecond`。
- **界限断言不得改动**：`benchmark_v6_test.go` 里 `ServerOutboundBytes`、`InterestDiff.Samples`、`ticks.Frames`、`OutboxHighWater`、`PlayerJobsHighWater`、`PlayerDoneHighWater`、`PeakRSSBytes` 那一段逐字不变。
- **通过/失败的条件必须与改动前完全一致**：只改错误消息的内容，不改任何 `if` 判定。
- **不得**用临时改小 `fixedBenchmarkFrameDuration` 的方式触发失败做验证——该常量还被 `probe.roster.Advance`（`multiplayer_benchmark.go:199`）与客户端帧预算（`benchmark.go:681`）使用，改它会连带改变无关行为。验证靠纯函数单测。
- 成功路径零额外开销：新增字段只在失败信息里被读取。
- 代码注释与 GoDoc 用中文；Go 标识符保留英文。Go 代码必须经 `gofmt`。
- `git add` 必须逐个点名文件。工作树里的 `midscene_run/log/*.log` 与 `mcgo` 可执行文件是无关噪声，**绝不能提交**，不要用 `git add .`。

---

### Task 1: 信号字段与纯格式化函数

**Files:**
- Modify: `cmd/mcgo/multiplayer_probe_epoch.go`（`benchmarkServerTickSignal` 结构、`observeScheduledTick`）
- Modify: `cmd/mcgo/multiplayer_benchmark.go`（新增 `formatTickBoundaryOverrun`）
- Test: `cmd/mcgo/multiplayer_benchmark_test.go`（若不存在则新建）

**Interfaces:**
- Produces: `formatTickBoundaryOverrun(signal benchmarkServerTickSignal, now time.Time, queueDepth int) string`，Task 2 的四个失败站点都调用它。
- Produces: `benchmarkServerTickSignal` 新增 `published time.Time` 与 `duration time.Duration` 两个字段。

- [ ] **Step 1: 写失败测试**

在 `cmd/mcgo/multiplayer_benchmark_test.go` 加入（文件不存在就新建，`package main`）：

```go
func TestFormatTickBoundaryOverrunReportsEachSegment(t *testing.T) {
	scheduled := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	signal := benchmarkServerTickSignal{
		measured:  true,
		scheduled: scheduled,
		published: scheduled.Add(30 * time.Millisecond),
		duration:  25 * time.Millisecond,
	}
	now := scheduled.Add(150 * time.Millisecond)
	got := formatTickBoundaryOverrun(signal, now, 3)
	for _, want := range []string{
		"总耗时 150ms",
		"超出 100ms",
		"tick 自身 25ms",
		"调度→发布 30ms",
		"发布→收到 120ms",
		"队列深度 3",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("分解缺少 %q，实际消息：%s", want, got)
		}
	}
}

func TestFormatTickBoundaryOverrunHandlesMissingPublishTime(t *testing.T) {
	scheduled := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	signal := benchmarkServerTickSignal{measured: true, scheduled: scheduled}
	got := formatTickBoundaryOverrun(signal, scheduled.Add(80*time.Millisecond), 0)
	if !strings.Contains(got, "发布时刻缺失") {
		t.Fatalf("发布时刻为零值时未标注，实际消息：%s", got)
	}
	if strings.Contains(got, "发布→收到") {
		t.Fatalf("发布时刻为零值时不应报出无意义的分段，实际消息：%s", got)
	}
}
```

`import` 需要 `strings`、`testing`、`time`。

- [ ] **Step 2: 跑测试确认失败原因是断言而非编译错误**

先加一个最小桩，让它能编译：

```go
func formatTickBoundaryOverrun(
	signal benchmarkServerTickSignal,
	now time.Time,
	queueDepth int,
) string {
	return ""
}
```

以及在 `benchmarkServerTickSignal` 里补上两个字段（见 Step 3）。

Run: `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo -run "^TestFormatTickBoundaryOverrun" -count=1' 2>&1 | tail -5`

Expected: FAIL，报"分解缺少 ..."。**若失败原因是编译错误，先补桩再重跑**——编译失败不算有效的红。

- [ ] **Step 3: 给信号加字段**

`cmd/mcgo/multiplayer_probe_epoch.go`：

```go
type benchmarkServerTickSignal struct {
	measured  bool
	scheduled time.Time
	// published 是 observeScheduledTick 回调内打的时间戳，用于把
	// "服务端侧耗时"与"信号在缓冲里排队的耗时"分开。只在失败信息里读取。
	published time.Time
	// duration 是该 tick 自身的执行耗时，服务端已作为回调参数给出，
	// 此前未向下传递。只在失败信息里读取。
	duration time.Duration
}
```

- [ ] **Step 4: 在观察者里填充**

同文件的 `observeScheduledTick`，把发送处改为：

```go
	select {
	case epoch.signals <- benchmarkServerTickSignal{
		measured:  measured,
		scheduled: scheduled,
		published: time.Now(),
		duration:  duration,
	}:
	default:
		epoch.overflow.Store(true)
	}
```

其余逻辑一行不动。

- [ ] **Step 5: 实现格式化函数**

`cmd/mcgo/multiplayer_benchmark.go`，放在 `benchmarkServerInputDeadline` 之前：

```go
// formatTickBoundaryOverrun 把一次 input boundary 超时拆成可判读的时间分解。
//
// 抽成纯函数是因为这段代码在本地永远不会执行：CI 上的失败形态本地复现不出来
// （见 docs/superpowers/specs/2026-08-07-ci-stability-merge-gate-design.md §7），
// 若把格式化埋在探针运行路径里，它就没有任何验证手段——一个从不执行的诊断
// 分支等于没写。
//
// 队列深度是这几项里判别力最强的一项：大于 0 说明缓冲里还压着别的信号、
// 测试 goroutine 确实落后了；等于 0 则说明时间花在服务端侧。
func formatTickBoundaryOverrun(
	signal benchmarkServerTickSignal,
	now time.Time,
	queueDepth int,
) string {
	total := now.Sub(signal.scheduled)
	overrun := total - fixedBenchmarkFrameDuration
	if signal.published.IsZero() {
		return fmt.Sprintf(
			"server input boundary 已错过 50ms tick deadline："+
				"总耗时 %v（超出 %v）；tick 自身 %v；发布时刻缺失；收到时队列深度 %d",
			total, overrun, signal.duration, queueDepth,
		)
	}
	return fmt.Sprintf(
		"server input boundary 已错过 50ms tick deadline："+
			"总耗时 %v（超出 %v）；tick 自身 %v；调度→发布 %v；发布→收到 %v；"+
			"收到时队列深度 %d",
		total, overrun, signal.duration,
		signal.published.Sub(signal.scheduled),
		now.Sub(signal.published),
		queueDepth,
	)
}
```

- [ ] **Step 6: 跑测试确认转绿**

Run: `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo -run "^TestFormatTickBoundaryOverrun" -count=1' 2>&1 | tail -3`

Expected: `ok`

- [ ] **Step 7: 变异验证——证明单测真的钉住了分解**

把 `formatTickBoundaryOverrun` 里的 `signal.published.Sub(signal.scheduled)` 与 `now.Sub(signal.published)` 两个实参对调，重跑 Step 6 的命令。

Expected: FAIL。**若仍然通过，说明单测没有真正区分这两段，必须加强断言再继续。**

恢复后 Run: `git diff --stat cmd/mcgo/multiplayer_benchmark.go` 确认改回原样。

- [ ] **Step 8: 提交**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go vet ./cmd/mcgo'
gofmt -l cmd/mcgo
git add cmd/mcgo/multiplayer_probe_epoch.go cmd/mcgo/multiplayer_benchmark.go cmd/mcgo/multiplayer_benchmark_test.go
git commit -m "feat: 增加 tick 边界超时的时间分解格式化"
```

---

### Task 2: 接入四个失败站点

**Files:**
- Modify: `cmd/mcgo/multiplayer_benchmark.go`（`benchmarkServerInputDeadline` 及其两个调用点、两处内联检查）

**Interfaces:**
- Consumes: Task 1 的 `formatTickBoundaryOverrun` 与信号新字段。
- Produces: `benchmarkServerInputDeadline(signal benchmarkServerTickSignal, queueDepth int) (time.Time, error)`——签名新增第二个参数。

- [ ] **Step 1: 改 `benchmarkServerInputDeadline`**

```go
func benchmarkServerInputDeadline(
	signal benchmarkServerTickSignal,
	queueDepth int,
) (time.Time, error) {
	if signal.scheduled.IsZero() {
		return time.Time{}, errors.New("server tick 缺少调度时间")
	}
	deadline := signal.scheduled.Add(fixedBenchmarkFrameDuration)
	now := time.Now()
	if !now.Before(deadline) {
		return time.Time{}, errors.New(
			formatTickBoundaryOverrun(signal, now, queueDepth),
		)
	}
	return deadline, nil
}
```

**判定条件与改动前完全一致**：原来是 `!time.Now().Before(deadline)`，现在是 `!now.Before(deadline)`，只是把 `time.Now()` 提出来复用，语义不变。

- [ ] **Step 2: 改两个调用点**

`multiplayer_benchmark.go:412` 附近（测量循环内）：

```go
			inputDeadline, err = benchmarkServerInputDeadline(signal, len(epoch.signals))
```

`multiplayer_benchmark.go:663` 附近（warm-up 后首组）：

```go
	firstInputDeadline, err := benchmarkServerInputDeadline(lastWarmupSignal, len(epoch.signals))
```

两处 `epoch` 都在作用域内（已核实）。

- [ ] **Step 3: 改两处内联检查**

`multiplayer_benchmark.go:446` 附近：

```go
			if now := time.Now(); !now.Before(inputDeadline) {
				return result, fmt.Errorf(
					"measured tick %d: %s", completed,
					formatTickBoundaryOverrun(signal, now, len(epoch.signals)),
				)
			}
```

`multiplayer_benchmark.go:471` 附近：

```go
			if now := time.Now(); !now.Before(inputDeadline) {
				return result, fmt.Errorf(
					"measured tick %d（boundary 完成后）: %s", completed,
					formatTickBoundaryOverrun(signal, now, len(epoch.signals)),
				)
			}
```

这两处发生在测试侧做完 `readStats`/`readRSS`/boundary 之后，与 `benchmarkServerInputDeadline` 那处语义不同（那处是收到信号即已超时）。第二处特意在消息里标注"boundary 完成后"，好在日志里区分是哪一段超的。

- [ ] **Step 4: 编译与全包测试**

Run: `zsh -ic 'gvm use go1.26.0 >/dev/null && go build ./cmd/mcgo && go test ./cmd/mcgo -count=1' 2>&1 | tail -5`（前台，`timeout: 600000`）

Expected: `ok`

- [ ] **Step 5: 确认判定逻辑零改动**

Run: `git diff cmd/mcgo/multiplayer_benchmark.go`

逐条核对 diff：**每一处 `if` 的判定表达式在语义上必须与改动前等价**。允许的变化只有「把 `time.Now()` 提取为局部变量 `now` 后复用」。若 diff 里出现任何比较运算符、阈值或分支结构的改变，回退重做。

同时确认 `fixedBenchmarkFrameDuration` 仍是 `50 * time.Millisecond`。

- [ ] **Step 6: 确认界限断言未被触碰**

Run: `git diff cmd/mcgo/benchmark_v6_test.go`

Expected: **无输出**（本任务不该碰这个文件）。

- [ ] **Step 7: ScenarioV7 仍通过且耗时同量级**

Run: `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo -run "^TestScenarioV7EightSessionServerProbeIsRealAndBounded$" -count=3' 2>&1 | tail -3`（前台，`timeout: 600000`）

Expected: `ok`，总耗时约 33 秒（单次约 11 秒，与改动前一致）。把实测耗时写进报告——**显著变长说明新增字段的填充进入了热路径**，那违反"成功路径零额外开销"。

- [ ] **Step 8: 提交**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go vet ./cmd/mcgo'
gofmt -l cmd/mcgo
git add cmd/mcgo/multiplayer_benchmark.go
git commit -m "feat: 四处 tick 边界失败改用时间分解消息"
```

---

### Task 3: 收尾门禁与文档

**Files:**
- Modify: `openspec/changes/benchmark-tick-boundary-diagnostics/tasks.md`（勾选）
- Modify: `docs/superpowers/specs/2026-08-07-benchmark-tick-boundary-diagnostics-design.md`（回填实测）

- [ ] **Step 1: 回填设计文档**

在设计文档 §8 补上实测结果：Task 1 Step 7 的变异验证结论、Task 2 Step 7 的 ScenarioV7 三次耗时。文档里任何与实测不符的表述一并订正。

- [ ] **Step 2: 确认改动范围**

Run: `git diff --stat main...HEAD`

Expected: 只有 `cmd/mcgo/multiplayer_benchmark.go`、`cmd/mcgo/multiplayer_probe_epoch.go`、`cmd/mcgo/multiplayer_benchmark_test.go`、以及 docs 与 openspec 文件。出现其他文件必须解释。

- [ ] **Step 3: 收尾门禁**

前台逐条跑，每条 `timeout: 600000`：

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go vet ./...'
gofmt -l .
git diff --check
openspec validate --all --strict --no-interactive
```

全部必须干净。`gofmt -l .` 与 `git diff --check` 无输出。

若 `go test ./... -race` 出现失败，先看是不是 `TestDropSurvivesShutdownAndRestart`——那是已知的既有偶发挂起，不是本变更引入的。

- [ ] **Step 4: 提交**

```bash
git add docs/superpowers/specs/2026-08-07-benchmark-tick-boundary-diagnostics-design.md \
        openspec/changes/benchmark-tick-boundary-diagnostics/tasks.md
git commit -m "docs: 回填 tick 边界分解的实测结果"
```

- [ ] **Step 5: 报告下一步**

本变更落地后**不会让 CI 变绿**。把这一点明确报告给用户，并说明下一步是等 CI 上 ScenarioV7 再次变红、读取分解数据、再按设计文档 §6 的判定表决定做哪一种修复。

---

## Self-Review

**规格覆盖**

| 设计文档章节 | 对应任务 |
| --- | --- |
| §4 改什么（两个字段 + 五项分解） | Task 1 Step 3/4/5 |
| §5 队列深度 | Task 1 Step 5、Task 2 Step 2/3 |
| §7 范围（判定逻辑零改动） | Task 2 Step 5/6 |
| §8 成功判据 1（成功路径零开销） | Task 2 Step 7 |
| §8 成功判据 2（本地仍通过） | Task 2 Step 7 |
| §8 纯函数单测 | Task 1 Step 1–7 |
| §8 禁止改 `fixedBenchmarkFrameDuration` | Global Constraints |

**已知的计划风险**

- Task 1 Step 2 要求先加桩再跑红。若实现者直接写完整实现，就失去了"测试确实在测东西"的证据——Step 7 的变异验证是第二道保险。
- Task 2 Step 3 的两处内联检查在设计文档里没有单独点名（§4 只说"失败路径"）。把它们一并接入是为了消息一致；若实现者认为超出范围，应在报告里说明而不是默默跳过。
- 队列深度取的是 `len(epoch.signals)`，是取出信号**之后**的深度。这与"取出时缓冲里还压着多少"差一个，但方向与判别力不变——大于 0 仍然坐实测试侧落后。这一点应在实现时写进 `formatTickBoundaryOverrun` 的注释，避免日后误读。
