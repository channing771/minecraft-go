# CI 稳定性与合并门禁 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 `internal/server` 测试的活性等待期限收敛到三个命名常量并留足余量，消除 CI 上约 25% 的假失败，为打开"PR 必须与 main 同步后重跑才能合并"的分支保护扫清前置条件。

**Architecture:** 期限按断言意图分四类，只抬高"活性等待"一类。禁改区（缺席断言、超时触发断言）的判据是断言 `errors.Is(err, context.DeadlineExceeded)`，可 grep 机械识别，且已核实全部落在毫秒档，与秒档零重叠——秒档因此可机械替换，毫秒档人工逐处判。产品代码零改动。

**Tech Stack:** Go 1.26，标准库 `testing` / `context` / `time`。不新增任何依赖。

## Global Constraints

- Go 命令一律经 `zsh -ic 'gvm use go1.26.0 >/dev/null && <cmd>'` 执行。模块拉取超时时用 `GOPROXY=https://goproxy.cn,direct`，不得降级依赖绕过。
- **产品代码零改动。** 本变更只改 `*_test.go`。若某处失败的根因在产品代码，停手并报告，不在本变更内修。
- **禁改区**（`internal/server` 内 10 处）：`host_test.go:413`、`integration_test.go:200`、`host_stats_test.go:171`、`persistence_integration_test.go:540`、`publication_test.go:411`、`publication_test.go:426`、`shutdown_test.go:122`、`shutdown_test.go:192`、`shutdown_test.go:224`、`shutdown_test.go:509`。这些站点及其所属的期限字面量一律不动。行号会随改动漂移，每次改动前用 `grep -rn "context.DeadlineExceeded" internal/server/*_test.go` 重新定位。
- 不得放宽任何性能阈值、资源上限或高水位上限。
- 不得引入环境变量倍率旋钮。
- 不得用抬高期限的方式处理亚秒失败。
- 代码注释与 GoDoc 用中文；Go 标识符保留英文。
- Go 代码必须经 `gofmt`。
- 自动测试不得启动或聚焦前台游戏窗口。
- 遇 `macos-latest` 已知时序假失败按已知抖动处理，**不得改阈值**。

## A/B 基线（改动前，必须先复现确认）

```
GOMAXPROCS=1 go test ./internal/server -count=1
--- FAIL: TestDropSurvivesShutdownAndRestart               (5.21s)
--- FAIL: TestDroppedItemSurvivesShutdownAndRestart        (5.23s)
--- FAIL: TestAuthoritativeMiningMemoryLifecycle           (5.02s)
--- FAIL: TestOpenFurnaceSendsStateOnlyToViewer            (0.04s)
--- FAIL: TestWorldPersistsAcrossRestartAndGeneratorUpgrade (0.17s)
```

前三个由 Task 1–2 处理，后两个由 Task 5 处理。

---

### Task 1: 命名常量与三个活性超时的验证闭环

**Files:**
- Create: `internal/server/deadline_test.go`
- Create: `internal/server/deadline_external_test.go`
- Modify: `internal/server/persistence_integration_test.go:509`（`stepUntil` 的 5s）
- Modify: 其余令 A/B 基线前三个测试变红的活性等待站点

**Interfaces:**
- Produces: 常量 `shortWaitDeadline`、`waitDeadline`、`longWaitDeadline`，`time.Duration` 类型，在 `package server` 与 `package server_test` 各定义一份，两份取值必须逐字相同。后续所有任务引用这三个名字。

- [ ] **Step 1: 复现 A/B 基线**

Run: `zsh -ic 'gvm use go1.26.0 >/dev/null && GOMAXPROCS=1 go test ./internal/server -count=1' 2>&1 | grep -E "^--- FAIL|^ok|^FAIL"`

Expected: 上面「A/B 基线」列出的 5 个 FAIL。若实际失败集合不同，**停止并报告**——基线对不上说明环境有别的变量，继续做下去无法判断改动是否有效。

- [ ] **Step 2: 写入 `package server` 的常量定义**

```go
package server

import "time"

// 活性等待期限：轮询直到条件成立、到点判失败的那一类等待用的期限。
//
// 这类期限是"放弃期限"而不是 sleep——条件一成立循环立刻返回，
// 因此在快机器上取大值零成本，唯一代价是真挂死时报错慢一些。
// 取值按等待角色分三档并保持数量级区分，使"哪一类等待挂了"
// 可以直接从报错耗时上读出来。
//
// 不适用于三类禁改站点：缺席断言（等一小段确认什么都没发生）、
// 超时触发断言（故意给极短期限、断言超时确实发生，形如
// errors.Is(err, context.DeadlineExceeded)）、性能门禁（测耗时
// 并断言小于上限）。抬高前两类只会拖慢测试，抬高第三类等于放宽门禁。
//
// internal/server 的测试跨 package server 与 package server_test
// 两个包，未导出标识符无法共享，因此本组常量在
// deadline_external_test.go 中另有一份逐字相同的定义。改动时必须同步。
const (
	// shortWaitDeadline 用于单次保存启动等亚秒本机事件（原 100ms–500ms）。
	shortWaitDeadline = 5 * time.Second
	// waitDeadline 用于登录 ready、收到某条消息、库存达到某状态（原 1s–5s）。
	//
	// 1 秒档归这里而不是 shortWaitDeadline：它有 95 处、是本包最紧
	// 也最密集的一档，只抬到 5s 仅 5× 余量，覆盖不住共享 runner 的减速。
	waitDeadline = 30 * time.Second
	// longWaitDeadline 用于关服屏障、磁盘重启、八人会话等复合等待（原 10s–30s）。
	longWaitDeadline = 60 * time.Second
)
```

- [ ] **Step 3: 写入 `package server_test` 的常量定义**

```go
package server_test

import "time"

// 活性等待期限，与 deadline_test.go 中的定义逐字相同。
//
// internal/server 的测试跨 package server 与 package server_test
// 两个包，未导出标识符无法跨包共享；为三个常量新建 internal 包并到
// internal/archcheck/deps_test.go 登记依赖，机械成本高于它解决的问题，
// 因此选择重复定义。两份必须同步改动。
//
// 完整的分类说明与禁改区定义见 deadline_test.go。
const (
	// shortWaitDeadline 用于单次保存启动等亚秒本机事件（原 100ms–500ms）。
	shortWaitDeadline = 5 * time.Second
	// waitDeadline 用于登录 ready、收到某条消息、库存达到某状态（原 1s–5s）。
	//
	// 1 秒档归这里而不是 shortWaitDeadline：它有 95 处、是本包最紧
	// 也最密集的一档，只抬到 5s 仅 5× 余量，覆盖不住共享 runner 的减速。
	waitDeadline = 30 * time.Second
	// longWaitDeadline 用于关服屏障、磁盘重启、八人会话等复合等待（原 10s–30s）。
	longWaitDeadline = 60 * time.Second
)
```

- [ ] **Step 4: 定位令前三个测试变红的具体期限站点**

Run:
```
zsh -ic 'gvm use go1.26.0 >/dev/null && GOMAXPROCS=1 go test ./internal/server \
  -run "TestDropSurvivesShutdownAndRestart|TestDroppedItemSurvivesShutdownAndRestart|TestAuthoritativeMiningMemoryLifecycle" \
  -count=1' 2>&1 | grep -E "_test.go:[0-9]+"
```

失败信息里的 `文件:行` 就是超时的等待站点。顺着它找到对应的 `context.WithTimeout` / `time.Now().Add` / `time.After`。

- [ ] **Step 5: 只替换这几处，验证三个测试转绿**

把定位到的站点按下表替换（`stepUntil` 的 `5 * time.Second` → `waitDeadline` 是已知的一处）：

| 原值 | 替换为 |
| --- | --- |
| `100ms` – `500ms` | `shortWaitDeadline` |
| `time.Second` – `5 * time.Second` | `waitDeadline` |
| `10 * time.Second` – `30 * time.Second` | `longWaitDeadline` |

注意 `time.Second`（1 秒）归 `waitDeadline` 而不是 `shortWaitDeadline`：它是本包最紧且最密集的一档（77 处），5× 余量不够。

Run:
```
zsh -ic 'gvm use go1.26.0 >/dev/null && GOMAXPROCS=1 go test ./internal/server \
  -run "TestDropSurvivesShutdownAndRestart|TestDroppedItemSurvivesShutdownAndRestart|TestAuthoritativeMiningMemoryLifecycle" \
  -count=3' 2>&1 | tail -3
```

Expected: `ok`

- [ ] **Step 6: 确认没有误伤禁改区**

Run: `grep -rn "context.DeadlineExceeded" internal/server/*_test.go | wc -l`

Expected: `10`（数量不变）。再逐处确认这 10 处附近的期限仍是毫秒档字面量，没有被换成命名常量。

- [ ] **Step 7: 常规并行度下仍然全绿**

Run: `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -race -count=1' 2>&1 | tail -3`

Expected: `ok`

- [ ] **Step 8: 提交**

```bash
gofmt -l internal/server
git add internal/server/deadline_test.go internal/server/deadline_external_test.go \
        internal/server/persistence_integration_test.go
# 加上 Step 5 实际改动的其他文件
git commit -m "test: 增加活性等待期限常量并修复三处磁盘重启超时"
```

---

### Task 2: 秒档站点的机械替换

**Files:**
- Modify: `internal/server/*_test.go` 中所有剩余的秒档活性等待站点（约 30 个文件）

**Interfaces:**
- Consumes: Task 1 定义的 `shortWaitDeadline` / `waitDeadline` / `longWaitDeadline`。

- [ ] **Step 1: 列出全部秒档站点**

Run:
```
grep -rnE "context\.WithTimeout\([^,]*, [0-9]* ?\*? ?time\.Second\)|time\.Now\(\)\.Add\([0-9]* ?\*? ?time\.Second\)|time\.After\([0-9]* ?\*? ?time\.Second\)" \
  internal/server/*_test.go | wc -l
```

记下这个数字。替换完成后它必须归零。

- [ ] **Step 2: 确认秒档与禁改区零重叠**

Run:
```
for loc in $(grep -rn "context.DeadlineExceeded" internal/server/*_test.go | cut -d: -f1,2); do
  f=${loc%%:*}; l=${loc##*:}
  awk -v L=$l 'NR>=L-10 && NR<=L && /time\.Second/ {print FILENAME":"NR": "$0}' $f
done
```

Expected: **无输出**。有输出说明某处禁改区确实由秒档期限守护，该处必须从替换清单中排除，并在提交信息里说明。

- [ ] **Step 3: 按档替换**

三种语法形态都要覆盖。逐文件执行，每改一个文件立即 `go build ./internal/server` 确认编译通过。

| 原值 | 替换为 |
| --- | --- |
| `time.Second`、`2`/`3`/`5 * time.Second` | `waitDeadline` |
| `10`/`12`/`15`/`20`/`30 * time.Second` | `longWaitDeadline` |

`time.Second` 裸写法（77 处 `time.After(time.Second)`）最容易漏——Step 1 的正则里 `[0-9]* ?\*? ?` 三段都可为空正是为了匹配它。

`10 * time.Minute`（`multiplayer_bench_test.go:22`）已足够长，不动。

- [ ] **Step 4: 确认秒档字面量归零**

Run: Step 1 的同一条命令

Expected: `0`

- [ ] **Step 5: 确认禁改区数量未变**

Run: `grep -rn "context.DeadlineExceeded" internal/server/*_test.go | wc -l`

Expected: `10`

- [ ] **Step 6: A/B 验证——只剩两个亚秒失败**

Run: `zsh -ic 'gvm use go1.26.0 >/dev/null && GOMAXPROCS=1 go test ./internal/server -count=1' 2>&1 | grep -E "^--- FAIL|^ok|^FAIL"`

Expected: 只剩
```
--- FAIL: TestOpenFurnaceSendsStateOnlyToViewer            (0.0Xs)
--- FAIL: TestWorldPersistsAcrossRestartAndGeneratorUpgrade (0.1Xs)
```

出现任何**新的**失败测试，或前三个测试重新变红，**停止并报告**。

- [ ] **Step 7: 常规并行度全绿且耗时未显著变长**

Run: `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -race -count=1' 2>&1 | tail -3`

Expected: `ok`，且耗时与改动前同量级。**耗时显著变长说明有站点被误分类**——活性等待抬高不该让通过的测试变慢，变慢意味着某处实际是缺席断言或超时触发断言，正在白等。

- [ ] **Step 8: 提交**

```bash
gofmt -l internal/server
git add internal/server
git commit -m "test: 秒档活性等待收敛到命名常量"
```

---

### Task 3: 毫秒档逐处核对

**Files:**
- Modify: `internal/server/*_test.go` 中毫秒档站点里**确属活性等待**的少数几处

- [ ] **Step 1: 列出全部毫秒档站点**

Run:
```
grep -rnE "context\.WithTimeout\([^,]*, [0-9]* ?\*? ?time\.Millisecond\)|time\.Now\(\)\.Add\([0-9]* ?\*? ?time\.Millisecond\)|time\.After\([0-9]* ?\*? ?time\.Millisecond\)" \
  internal/server/*_test.go
```

约 23 处。**这一档不得机械替换**——它同时混着活性等待与禁改区，是本变更唯一必须人工逐处判的部分。

- [ ] **Step 2: 逐处分类**

对每一处，读它所服务的断言，按下表判定：

| 断言形态 | 类别 | 处置 |
| --- | --- | --- |
| `errors.Is(err, context.DeadlineExceeded)` 为**期望** | 超时触发断言 | 不动 |
| 超时后什么都不做 / 断言没收到消息 | 缺席断言 | 不动 |
| 超时即 `t.Fatal` | 活性等待 | 换 `shortWaitDeadline` |
| 断言耗时小于某上限 | 性能门禁 | 不动 |

已知归入"不动"的 10 处见 Global Constraints。剩余约 13 处需要判定。

把判定结果写进提交信息：每一处的 `文件:行`、判定类别、依据。**这份记录是本任务的主要产出**，比代码改动本身更重要——它让评审者能核对分类而不必重做一遍。

- [ ] **Step 3: 只替换判定为活性等待的那几处**

- [ ] **Step 4: 验证**

Run:
```
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -race -count=1' 2>&1 | tail -3
zsh -ic 'gvm use go1.26.0 >/dev/null && GOMAXPROCS=1 go test ./internal/server -count=1' 2>&1 | grep -E "^--- FAIL|^ok|^FAIL"
```

Expected: 常规跑 `ok`；`GOMAXPROCS=1` 仍只剩那两个亚秒失败，且**耗时未显著变长**。

- [ ] **Step 5: 提交**

```bash
gofmt -l internal/server
git add internal/server
git commit -m "test: 逐处核对毫秒档期限并只抬高其中的活性等待"
```

---

### Task 4: ScenarioV7 采样收集预算

**Files:**
- Modify: `cmd/mcgo/benchmark_v6_test.go:598`

- [ ] **Step 1: 确认现状是"跑在下限上"**

Run: `grep -n -A4 "func measureMultiplayerServerProbe" cmd/mcgo/multiplayer_benchmark.go`

确认函数开头是 `if duration < 10*time.Second { 报错 }`，而 `benchmark_v6_test.go` 传入的正是 `10 * time.Second`。

- [ ] **Step 2: 记录改前的界限断言**

抄下 `TestScenarioV7EightSessionServerProbeIsRealAndBounded` 中的全部断言：

```go
if multiplayer.ServerOutboundBytes == 0 ||
    multiplayer.InterestDiff.Samples != benchmarkServerInterestSamples ||
    ticks.Frames != benchmarkServerMeasuredTicks ||
    multiplayer.OutboxHighWater > benchmarkOutboxLimit ||
    multiplayer.PlayerJobsHighWater > 16 || multiplayer.PlayerDoneHighWater > 2 ||
    multiplayer.PeakRSSBytes == 0 || multiplayer.PeakRSSBytes >= 2<<30 {
```

改动后这一段 MUST 逐字不变。

- [ ] **Step 3: 放宽收集预算**

```go
	// 收集预算而非阈值：measureMultiplayerServerProbe 要求 >= 10s，
	// 此前恰好传 10s，按构造零余量——这是该测试长期成为 CI 首要
	// 假失败源的成因。放宽预算不动下面任何一条界限断言。
	multiplayer, ticks, err := measureMultiplayerServerProbe(30 * time.Second)
```

- [ ] **Step 4: 验证断言段逐字未变**

Run: `git diff cmd/mcgo/benchmark_v6_test.go`

Expected: diff 中只有 `10 * time.Second` → `30 * time.Second` 与新增注释。**若 diff 触及任何 `!=` / `>` / `>=` 断言，回退重做。**

- [ ] **Step 5: 跑该测试**

Run: `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo -run "^TestScenarioV7EightSessionServerProbeIsRealAndBounded$" -count=3' 2>&1 | tail -3`

Expected: `ok`

- [ ] **Step 6: 提交**

```bash
gofmt -l cmd/mcgo
git add cmd/mcgo/benchmark_v6_test.go
git commit -m "test: 放宽 ScenarioV7 采样收集预算，界限断言不变"
```

---

### Task 5: 两处顺序假设修复

**Files:**
- Modify: `internal/server/persistence_integration_test.go:466-479`
- Modify: `internal/server/furnace_publication_test.go:118-135`

**这两处不得用抬高期限处理。** 它们在期限的百分之一以内就失败，抬高期限对它们毫无作用，只会掩盖问题。

- [ ] **Step 1: 确认 `moveViewToUnvisitedChunk` 的失败是输入未被消费**

Run:
```
zsh -ic 'gvm use go1.26.0 >/dev/null && GOMAXPROCS=1 go test ./internal/server \
  -run TestWorldPersistsAcrossRestartAndGeneratorUpgrade -count=3' 2>&1 | grep "LastInputSequence"
```

Expected: `LastInputSequence` 远小于 600，而 `WorldTimeTicks` 大于 600。这证明 tick 推进了但输入没跟上。

- [ ] **Step 2: 改为等待输入被消费**

现状是按迭代次数计数，每次迭代只 `step()` 一次：

```go
func (h *persistentHarness) moveViewToUnvisitedChunk() {
	h.t.Helper()
	for range 600 {
		h.sequence++
		sendClientMessage(h.t, h.clientEndpoint, network.PlayerInput{
			Sequence: h.sequence,
			MoveZ:    1,
		})
		h.step()
		if h.generator.totalCalls() > 0 {
			return
		}
	}
	h.t.Fatalf("移动 600 tick 后 generator B 未用于未探索区块；player=%+v", h.playerSummary())
}
```

改为等本条输入被权威消费后再进入下一轮：

```go
func (h *persistentHarness) moveViewToUnvisitedChunk() {
	h.t.Helper()
	for range 600 {
		h.sequence++
		sendClientMessage(h.t, h.clientEndpoint, network.PlayerInput{
			Sequence: h.sequence,
			MoveZ:    1,
		})
		// 输入投递是异步的，只 step 一次并不保证本条已被权威消费。
		// 按迭代次数计数会在调度争抢下提前耗尽 600 次预算——实测
		// GOMAXPROCS=1 下 600 次迭代只消费了 175 条输入，玩家根本
		// 没走到未探索区块，generator B 自然没被调用。
		h.stepUntil(func() bool {
			state, ok := playerStateForExternalTest(h.running)
			return ok && state.LastInputSequence >= h.sequence
		})
		if h.generator.totalCalls() > 0 {
			return
		}
	}
	h.t.Fatalf("移动 600 tick 后 generator B 未用于未探索区块；player=%+v", h.playerSummary())
}
```

- [ ] **Step 3: 验证该测试在 `GOMAXPROCS=1` 下转绿**

Run:
```
zsh -ic 'gvm use go1.26.0 >/dev/null && GOMAXPROCS=1 go test ./internal/server \
  -run TestWorldPersistsAcrossRestartAndGeneratorUpgrade -count=5' 2>&1 | tail -3
```

Expected: `ok`

若仍红，**停止并报告**——可能根因在产品代码的输入投递路径，那属于另一个变更。

- [ ] **Step 4: 诊断熔炉测试的真实退出条件**

现状的等待循环用 `Generation != 0` 作为"状态已就绪"的代理：

```go
	deadline := time.Now().Add(5 * time.Second)
	var opened network.FurnaceState
	for opened.Furnace.Generation == 0 {
		if time.Now().After(deadline) {
			t.Fatal("等待熔炉状态超时")
		}
		messages, _ := step()
		if len(messages.states) > 0 {
			opened = messages.states[len(messages.states)-1]
		}
	}
	if opened.Input.Item != core.ItemRawIron {
		t.Fatalf("状态输入 = %+v", opened.Input)
	}
```

失败信息是 `状态输入 = {Item:0 Count:0 Durability:0}`——退出循环时拿到的状态里输入槽是空的。

先加一条临时打印，确认拿到的是哪一版状态：

```go
		if len(messages.states) > 0 {
			opened = messages.states[len(messages.states)-1]
			t.Logf("收到熔炉状态 generation=%d input=%+v fuel=%+v",
				opened.Furnace.Generation, opened.Input, opened.Fuel)
		}
```

Run: `zsh -ic 'gvm use go1.26.0 >/dev/null && GOMAXPROCS=1 go test ./internal/server -run TestOpenFurnaceSendsStateOnlyToViewer -count=1 -v' 2>&1 | grep "收到熔炉状态"`

- [ ] **Step 5: 让退出条件直接表达要等的东西**

把代理条件换成真正的目标条件——等到输入槽反映出建世时放入的原铁：

```go
	// 原来的退出条件是 Generation != 0，那只是"收到过状态"的代理：
	// 满足它的第一条状态未必已经反映放料结果，在调度争抢下会读到
	// 更早的一版，输入槽为空。改为直接等目标条件。
	deadline := time.Now().Add(waitDeadline)
	var opened network.FurnaceState
	for opened.Input.Item != core.ItemRawIron {
		if time.Now().After(deadline) {
			t.Fatalf("等待熔炉状态超时，最后一版 = %+v", opened)
		}
		messages, _ := step()
		if len(messages.states) > 0 {
			opened = messages.states[len(messages.states)-1]
		}
	}
	if opened.Furnace.Slot != 0 || opened.Furnace.Chunk != (core.ChunkPos{}) {
		t.Fatalf("熔炉引用 = %+v", opened.Furnace)
	}
```

删掉 Step 4 加的临时 `t.Logf`。

若 Step 4 的打印显示状态**从来没有**带上原铁（而不是"先空后有"），说明根因不在测试的等待条件而在发布路径，**停止并报告**。

- [ ] **Step 6: 验证**

Run:
```
zsh -ic 'gvm use go1.26.0 >/dev/null && GOMAXPROCS=1 go test ./internal/server -run TestOpenFurnaceSendsStateOnlyToViewer -count=5' 2>&1 | tail -3
zsh -ic 'gvm use go1.26.0 >/dev/null && GOMAXPROCS=1 go test ./internal/server -count=1' 2>&1 | grep -E "^--- FAIL|^ok|^FAIL"
```

Expected: 第一条 `ok`；第二条**全包全绿**。

- [ ] **Step 7: 提交**

```bash
gofmt -l internal/server
git add internal/server/persistence_integration_test.go internal/server/furnace_publication_test.go
git commit -m "test: 修复两处等待条件的顺序假设"
```

---

### Task 6: 反向验证、收尾门禁与文档

**Files:**
- Modify: `docs/superpowers/specs/2026-08-07-ci-stability-merge-gate-design.md`（回填实测结果）
- Modify: `openspec/changes/ci-stability-merge-gate/tasks.md`（勾选）

- [ ] **Step 1: 反向验证——证明期限不是死代码**

把 `internal/server/deadline_test.go` 与 `deadline_external_test.go` 中的 `waitDeadline` 临时改成 `1 * time.Millisecond`。

Run: `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -count=1' 2>&1 | grep -cE "^--- FAIL"`

Expected: 大于 0。**等于 0 说明这些常量根本没被用上**，替换工作有问题，必须回查。

恢复后 Run: `git diff --stat internal/server/deadline_test.go internal/server/deadline_external_test.go`

Expected: 无输出。

- [ ] **Step 2: 两份常量定义逐字一致**

Run:
```
diff <(grep -A3 "^const (" internal/server/deadline_test.go) \
     <(grep -A3 "^const (" internal/server/deadline_external_test.go)
```

Expected: 无输出。

- [ ] **Step 3: A/B 最终结果**

Run: `zsh -ic 'gvm use go1.26.0 >/dev/null && GOMAXPROCS=1 go test ./internal/server -count=1' 2>&1 | tail -3`

Expected: `ok`

- [ ] **Step 4: 回填设计文档**

在 `docs/superpowers/specs/2026-08-07-ci-stability-merge-gate-design.md` 的验证章节补上：改动后 `GOMAXPROCS=1` 的结果、毫秒档逐处分类的结论、两处顺序假设修复的最终形态、常规并行度下的套件耗时对比（改前 vs 改后）。

文档里任何与实测不符的表述必须一并订正——包括普查数字、站点数量、类别归属。

- [ ] **Step 5: 收尾门禁**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go vet ./...'
gofmt -l .
git diff --check
openspec validate --all --strict --no-interactive
```

全部必须干净。`gofmt -l .` 与 `git diff --check` 无输出。

遇 `macos-latest` 已知时序假失败按已知抖动处理，**不得改阈值**。

- [ ] **Step 6: 确认产品代码零改动**

Run: `git diff --stat main...HEAD -- ':!*_test.go' ':!docs' ':!openspec'`

Expected: **无输出**。有输出说明改到了产品代码，必须逐处解释或回退。

- [ ] **Step 7: 提交**

```bash
git add docs/superpowers/specs/2026-08-07-ci-stability-merge-gate-design.md \
        openspec/changes/ci-stability-merge-gate/tasks.md
git commit -m "docs: 回填 CI 稳定性治理的实测结果"
```

- [ ] **Step 8: 观察 CI**

推分支后连续观察 CI 运行。达到 10 次连绿之前**不要**建议开启分支保护——那是本变更的目的，但也是唯一无法在本地验证的部分。

把观察到的连绿次数报告给用户，由用户决定何时开启 GitHub 的 `Require branches to be up to date before merging`。

---

## Self-Review

**规格覆盖**

| Requirement | 对应任务 |
| --- | --- |
| 墙钟期限按断言意图分类 | Task 2 Step 2、Task 3 Step 2 |
| 活性等待使用按角色命名的常量 | Task 1、Task 2 |
| 采样收集预算必须留有余量 | Task 4 |
| 假失败治理必须有可复现的 A/B 证据 | Task 1 Step 1、Task 6 Step 1/Step 3 |
| 亚秒失败不得用期限改动掩盖 | Task 5 |

**已知的计划风险**

- Task 1 Step 4 的站点定位依赖失败信息里的行号。若失败信息不含行号（例如超时发生在 helper 内且未标 `t.Helper()`），实现者需要顺着调用链人工定位。
- Task 3 的 13 处待判站点没有预先给出判定结果——这是有意的，预判会诱导实现者照抄而不是真去读断言。判定记录是该任务的主要产出。
- Task 5 Step 5 的熔炉修复形态依赖 Step 4 的打印结果。若打印显示状态从未带上原铁，计划给出的代码不适用，此时应停止并报告而不是硬改。
