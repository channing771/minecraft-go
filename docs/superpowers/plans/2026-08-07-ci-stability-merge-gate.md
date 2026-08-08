# CI 稳定性与合并门禁 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 `internal/server` 测试的活性等待期限收敛到三个命名常量并留足余量，并修正 `cmd/mcgo` ScenarioV7 跑在采样预算下限上的问题，消除 CI 六次红中的四次。

**Architecture:** 期限按断言意图分四类，只抬高"活性等待"一类。禁改区（缺席断言、超时触发断言）的判据是断言 `errors.Is(err, context.DeadlineExceeded)`，可 grep 机械识别，且已核实全部落在毫秒档，与秒档零重叠——秒档因此可机械替换，毫秒档人工逐处判。产品代码零改动。

**Tech Stack:** Go 1.26，标准库 `testing` / `context` / `time`。不新增任何依赖。

## 本变更不解决什么

CI 六次红的构成（依据**断言**而非测试名）：

| 类别 | 次数 | 断言 | 本变更 |
| --- | --- | --- | --- |
| 采样预算不足 | 3 | ScenarioV7 样本收集不足 | ✅ Task 4 |
| **连接被关闭** | 3 | `wait ready Recv: network: transport closed`，0.02s–1.46s | ❌ 根因未知，另开变更 |
| 期限耗尽 | 1 | `player did not become ready`，5.51s | ✅ Task 1–3 |

**主导形态是"连接被关闭"，本变更不处理。** 不得把本变更描述为"修好了 CI"。

## Global Constraints

- Go 命令一律经 `zsh -ic 'gvm use go1.26.0 >/dev/null && <cmd>'` 执行。模块拉取超时时用 `GOPROXY=https://goproxy.cn,direct`。
- **测试一律前台跑**，用 Bash 工具的 `timeout` 参数给足时间（`internal/server` 全包 `-race` 约 120 秒，给 `timeout: 600000`）。不要用 `run_in_background`，不要用 monitor——会陷入等一个不会到来的通知。
- **禁止使用 `GOMAXPROCS=1` 做验证。** 已实测证伪：它是悬崖不是梯度（1 处永不收敛、2 处秒过），触发的是单核 goroutine 饿死，与 CI 的多核慢不同构。用它的结果做诊断会得出错误结论。
- **产品代码零改动。** 本变更只改 `*_test.go`。若某处失败的根因在产品代码，停手并报告。
- **禁改区**（`internal/server` 内 10 处**断言**）：用 `grep -rn "context.DeadlineExceeded" internal/server/*_test.go` 定位。注意引入常量后这条 grep 会返回 11，多出的一行是常量 GoDoc 里解释禁改区时引用的字样，不是断言——按断言计数应始终是 10。这些站点及其所属的期限字面量一律不动。
- 不得放宽任何性能阈值、资源上限或高水位上限。
- 不得引入环境变量倍率旋钮。
- 断言内容与等待无关的失败（如连接被关闭）不得用抬高期限来"修"。
- 代码注释与 GoDoc 用中文；Go 标识符保留英文。Go 代码必须经 `gofmt`。
- 自动测试不得启动或聚焦前台游戏窗口。
- `git add` 必须逐个点名文件。工作树里的 `midscene_run/log/*.log` 与 `mcgo` 可执行文件是无关噪声，**绝不能提交**。

---

### Task 1: 命名常量

**Files:**
- Create: `internal/server/deadline_test.go`
- Create: `internal/server/deadline_external_test.go`
- Modify: `internal/server/host_test.go`（`waitReady` 的 5s 轮询期限）

**Interfaces:**
- Produces: 常量 `shortWaitDeadline`、`waitDeadline`、`longWaitDeadline`，`time.Duration` 类型，在 `package server` 与 `package server_test` 各定义一份，两份取值必须逐字相同。后续任务引用这三个名字。

- [ ] **Step 1: 写入 `package server` 的常量定义**

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
// 超时触发断言（故意给极短期限、断言超时确实发生）、性能门禁
// （测耗时并断言小于上限）。抬高前两类只会拖慢测试，抬高第三类
// 等于放宽门禁。前两类的判据是断言期望超时发生，可用
// context.DeadlineExceeded 这个标识符 grep 出来。
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

- [ ] **Step 2: 写入 `package server_test` 的常量定义**

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

- [ ] **Step 3: 替换 CI 上确实失败过的那一处**

`internal/server/host_test.go` 的 `waitReady`：

```go
func waitReady(t *testing.T, host *Host, login testLogin) {
	t.Helper()
	deadline := time.Now().Add(waitDeadline)
	for time.Now().Before(deadline) {
```

这是 CI 上 `TestHostRejectsDuplicatePlayerBeforeLoad` 报 `player did not become ready`（5.51s）的那一处。

- [ ] **Step 4: 验证**

Run: `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -race -count=1' 2>&1 | tail -3`（前台，`timeout: 600000`）

Expected: `ok`

Run: `grep -rn "context.DeadlineExceeded" internal/server/*_test.go | grep -v "^internal/server/deadline" | wc -l`

Expected: `10`

Run: `gofmt -l internal/server`

Expected: 无输出

- [ ] **Step 5: 提交**

```bash
git add internal/server/deadline_test.go internal/server/deadline_external_test.go internal/server/host_test.go
git commit -m "test: 增加活性等待期限常量"
```

---

### Task 2: 秒档机械替换

**Files:**
- Modify: `internal/server/*_test.go` 中所有秒档活性等待站点（约 30 个文件）

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
for loc in $(grep -rn "context.DeadlineExceeded" internal/server/*_test.go | grep -v "^internal/server/deadline" | cut -d: -f1,2); do
  f=${loc%%:*}; l=${loc##*:}
  awk -v L=$l 'NR>=L-10 && NR<=L && /time\.Second/ {print FILENAME":"NR": "$0}' $f
done
```

Expected: **无输出**。有输出说明某处禁改区确实由秒档期限守护，该处必须从替换清单中排除，并在提交信息里说明。

- [ ] **Step 3: 按档替换**

三种语法形态都要覆盖。逐文件执行，每改一个文件立即 `zsh -ic 'gvm use go1.26.0 >/dev/null && go build ./internal/server'` 确认编译通过。

| 原值 | 替换为 |
| --- | --- |
| `time.Second`、`2`/`3`/`5 * time.Second` | `waitDeadline` |
| `10`/`12`/`15`/`20`/`30 * time.Second` | `longWaitDeadline` |

`time.Second` 裸写法（77 处 `time.After(time.Second)`）最容易漏——Step 1 的正则里 `[0-9]* ?\*? ?` 三段都可为空正是为了匹配它。

`10 * time.Minute`（`multiplayer_bench_test.go:22`）已足够长，不动。

- [ ] **Step 4: 确认秒档字面量归零**

Run: Step 1 的同一条命令 → Expected: `0`

- [ ] **Step 5: 确认禁改区断言数量未变**

Run: `grep -rn "context.DeadlineExceeded" internal/server/*_test.go | grep -v "^internal/server/deadline" | wc -l` → Expected: `10`

- [ ] **Step 6: 全绿且耗时未显著变长**

Run: `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -race -count=1' 2>&1 | tail -3`（前台，`timeout: 600000`）

Expected: `ok`，且耗时与改动前同量级。**耗时显著变长说明有站点被误分类**——活性等待抬高不该让通过的测试变慢，变慢意味着某处实际是缺席断言或超时触发断言，正在白等。把改前改后的耗时都写进报告。

- [ ] **Step 7: 提交**

```bash
gofmt -l internal/server
git add internal/server   # 只有本包的 *_test.go 会有改动，确认 git status 无其他文件
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
| 期望 `errors.Is(err, context.DeadlineExceeded)` | 超时触发断言 | 不动 |
| 超时后什么都不做 / 断言没收到消息 | 缺席断言 | 不动 |
| 超时即 `t.Fatal` | 活性等待 | 换 `shortWaitDeadline` |
| 断言耗时小于某上限 | 性能门禁 | 不动 |

把判定结果写成一张表放进你的**报告文件**，每行一处：`文件:行` | 判定类别 | 依据（引用它服务的那条断言）。提交信息里放一句话摘要即可。

**这张表是本任务的主要产出**，比代码改动本身更重要——它让评审者能核对分类而不必重做一遍。写进报告文件而不是只写提交信息，是因为评审者拿到的是 `git log --oneline` 与 diff，读不到完整提交信息，但会读报告文件。

- [ ] **Step 3: 只替换判定为活性等待的那几处**

- [ ] **Step 4: 验证**

Run: `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -race -count=1' 2>&1 | tail -3`（前台，`timeout: 600000`）

Expected: `ok`，且耗时未显著变长。

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
	// 此前恰好传 10s，按构造零余量——这是该测试成为 CI 首要假失败源
	// 的成因（六次红里占三次）。放宽预算不动下面任何一条界限断言。
	multiplayer, ticks, err := measureMultiplayerServerProbe(30 * time.Second)
```

- [ ] **Step 4: 验证断言段逐字未变**

Run: `git diff cmd/mcgo/benchmark_v6_test.go`

Expected: diff 中只有 `10 * time.Second` → `30 * time.Second` 与新增注释。**若 diff 触及任何 `!=` / `>` / `>=` 断言，回退重做。**

- [ ] **Step 5: 跑该测试**

Run: `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo -run "^TestScenarioV7EightSessionServerProbeIsRealAndBounded$" -count=3' 2>&1 | tail -3`（前台，`timeout: 600000`）

Expected: `ok`

- [ ] **Step 6: 提交**

```bash
gofmt -l cmd/mcgo
git add cmd/mcgo/benchmark_v6_test.go
git commit -m "test: 放宽 ScenarioV7 采样收集预算，界限断言不变"
```

---

### Task 5: 反向验证、收尾门禁与文档

**Files:**
- Modify: `docs/superpowers/specs/2026-08-07-ci-stability-merge-gate-design.md`（回填实测结果）
- Modify: `openspec/changes/ci-stability-merge-gate/tasks.md`（勾选）

- [ ] **Step 1: 反向验证——证明期限不是死代码**

把 `internal/server/deadline_test.go` 与 `deadline_external_test.go` 中的 `waitDeadline` 临时改成 `1 * time.Millisecond`。

Run: `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -count=1' 2>&1 | grep -cE "^--- FAIL"`（前台，`timeout: 600000`）

Expected: 大于 0。**等于 0 说明这些常量根本没被用上**，替换工作有问题，必须回查。把实际数字写进报告。

恢复后 Run: `git diff --stat internal/server/deadline_test.go internal/server/deadline_external_test.go` → Expected: 无输出。

- [ ] **Step 2: 两份常量定义逐字一致**

Run:
```
diff <(sed -n '/^const (/,/^)/p' internal/server/deadline_test.go) \
     <(sed -n '/^const (/,/^)/p' internal/server/deadline_external_test.go)
```

Expected: 无输出。

- [ ] **Step 3: 回填设计文档**

在 `docs/superpowers/specs/2026-08-07-ci-stability-merge-gate-design.md` 补上：Task 2 记录的套件耗时改前/改后对比、Task 3 的毫秒档分类结论、Task 5 Step 1 的反向验证失败数。

文档里任何与实测不符的表述必须一并订正。

- [ ] **Step 4: 确认产品代码零改动**

Run: `git diff --stat main...HEAD -- ':!*_test.go' ':!docs' ':!openspec'`

Expected: **无输出**。有输出说明改到了产品代码，必须逐处解释或回退。

- [ ] **Step 5: 收尾门禁**

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

- [ ] **Step 6: 提交**

```bash
git add docs/superpowers/specs/2026-08-07-ci-stability-merge-gate-design.md \
        openspec/changes/ci-stability-merge-gate/tasks.md
git commit -m "docs: 回填 CI 稳定性治理的实测结果"
```

---

## Self-Review

**规格覆盖**

| Requirement | 对应任务 |
| --- | --- |
| 墙钟期限按断言意图分类 | Task 2 Step 2、Task 3 Step 2 |
| 活性等待使用按角色命名的常量 | Task 1、Task 2 |
| 采样收集预算必须留有余量 | Task 4 |
| 失败归因必须依据断言而非测试名 | 「本变更不解决什么」一节的分类表 |
| 复现手段在被采信前必须先自证同构 | Global Constraints 的 `GOMAXPROCS=1` 禁令 |
| 期限改动必须证明其并非死代码 | Task 5 Step 1 |
| 不属期限类的失败不得用期限改动掩盖 | 「本变更不解决什么」一节 + Global Constraints |

**已知的计划风险**

- Task 3 的 23 处待判站点没有预先给出判定结果——这是有意的，预判会诱导实现者照抄而不是真去读断言。判定表是该任务的主要产出。
- Task 2 的"耗时未显著变长"是误分类的唯一哨兵，但它不精确：机器负载波动也会影响耗时。实现者应把改前改后的耗时都记录下来，由评审者判断差异是否可疑。
- 本变更的收益无法在本地证明，只能靠 CI 统计观察，且观察时必须按断言分类——`transport closed` 仍会出现，那不是本变更的失败。
