# scenario v8 GPU 完成门禁修复实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在不提高任何阈值的前提下，把 `remote_gpu_complete` 纠正为传输收尾完成后的 `Submit + Poll(true)` 2048 样本 scenario v8 指标，并建立不含 M4D 实现的 M5 v8 基线。

**Architecture:** 在 M4D 实现前基点 `0eace21` 的隔离 worktree 中修改共用性能报告契约、benchmark producer、服务端可信观察者阶段屏障和 perfcheck validator。正式 Memory/TCP 基线链通过后，只把后续代码与基线提交线性带回当前 `main`，再恢复 M4D 验收。

**Tech Stack:** Go 1.26.0（GVM）、`internal/gfx` WebGPU 抽象、Metal headless device、OpenSpec、Go 标准测试工具。

## Global Constraints

- 所有 Go 命令 MUST 先执行 `gvm use go1.26.0`，不得下载或安装另一套 Go。
- 自动测试与 benchmark MUST 使用 headless device 和离屏纹理，不得创建、启动或聚焦游戏窗口。
- `remote_gpu_complete` MUST 恰好采集 2048 个样本；still/flying workload、2560x1440 framebuffer、20% 相对阈值和全部绝对门禁保持不变。
- 每个经授权的精确 HEAD 正式链中，Memory/TCP 报告各执行恰好一次；旧 HEAD 失败后不得重跑、筛选输出或覆盖基线，只有阶段屏障代码提交后才能按新 HEAD、新路径和新授权开启新链。
- 不新增依赖、GPU timestamp query、重试、窗口中位数、原始样本文件或交互运行时分支。
- 当前 `main` 的 `README.md`、`docs/notes/lan-server.md`、`internal/server/tcp_integration_test.go`、M4D `tasks.md` 和 `midscene_run/` MUST 保持原样，直到性能分支集成完成。
- 修改任何 Go symbol 前执行 GitNexus upstream impact；服务不可用时记录证据，并用 `rg` 列出全部调用者。提交前执行 `detect_changes`；工具不可用时使用精确 staged diff 和调用链清单替代。

---

### Task 1: 创建 M4D 前隔离工作区

**Files:**
- Existing plan commit: `5e264af`
- Base commit: `0eace21`
- Worktree: `/Users/chen/chenwork/minecraft-go/.worktrees/stabilize-remote-gpu-completion-gate`

**Interfaces:**
- Consumes: 已批准的 `stabilize-remote-gpu-completion-gate` OpenSpec。
- Produces: 不含 M4D 生产代码的 `codex/stabilize-remote-gpu-completion-gate` 分支。

- [ ] **Step 1: 使用 worktree 技能检查目录策略**

读取并执行 `superpowers:using-git-worktrees`。确认 `.worktrees` 已被 Git 忽略；若未忽略，先按技能修复并单独提交，不能继续创建 worktree。

- [ ] **Step 2: 从精确基点创建分支**

```bash
git worktree add .worktrees/stabilize-remote-gpu-completion-gate -b codex/stabilize-remote-gpu-completion-gate 0eace21
```

预期：新 worktree 的 `HEAD` 为 `0eace21`，主工作区仍位于 `main` 且脏文件不变。

- [ ] **Step 3: 带入已批准规划**

在新 worktree 中执行：

```bash
git cherry-pick 5e264af
```

预期：只新增 `openspec/changes/stabilize-remote-gpu-completion-gate/`；不得出现 M4D 的 core/network/storage/render 生产文件。

- [ ] **Step 4: 验证隔离边界**

```bash
git status --short
git diff --name-only 0eace21..HEAD
openspec validate --all --strict --no-interactive
```

预期：worktree 干净，名称清单只包含本 change 的规划文件，OpenSpec strict 通过。

---

### Task 2: TDD 纠正 scenario 和 GPU 计时边界

**Files:**
- Modify: `internal/client/perf.go`
- Modify: `cmd/mcgo/benchmark.go`
- Modify: `cmd/mcgo/multiplayer_benchmark.go`
- Modify: `cmd/mcgo/benchmark_v6_test.go`
- Modify test utility: `cmd/mcgo/app_test.go`

**Interfaces:**
- Consumes: `client.LatencyRecorder.Add(time.Duration)`、`gfx.Device.Submit(...gfx.CommandBuffer)`、`gfx.Device.Poll(bool)`。
- Produces: `client.ScenarioV8GPUCompletionSamples = 2048`；`multiplayerClientProbe.now func() time.Time`；scenario v8 report。

- [ ] **Step 1: 记录 blast radius**

对 `runBenchmark`、`measureGPUCompletion` 和 `LatencyRecorder.Summary` 执行 upstream impact。GitNexus 不可用时运行：

```bash
rg -n "runBenchmark\(|measureGPUCompletion\(|LatencyRecorder|scenarioVersion" cmd internal
```

预期：生产调用者只在 benchmark 路径；`LatencyRecorder` 的其他使用者只消费通用摘要，不受样本常量影响。

- [ ] **Step 2: 先让 scenario v8 测试失败**

在 `cmd/mcgo/benchmark_v6_test.go` 对首个场景测试应用精确改动：

```diff
-func TestScenarioV7ContainsSevenSortedUnicodeRemotePlayers(t *testing.T) {
+func TestScenarioV8ContainsSevenSortedUnicodeRemotePlayers(t *testing.T) {
-	if scenarioVersion != 7 {
-		t.Fatalf("scenarioVersion=%d, want 7", scenarioVersion)
+	if scenarioVersion != 8 {
+		t.Fatalf("scenarioVersion=%d, want 8", scenarioVersion)
```

运行：

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo -run TestScenarioV8ContainsSevenSortedUnicodeRemotePlayers -count=1'
```

预期：FAIL，错误包含 `scenarioVersion=7, want 8`。

- [ ] **Step 3: 最小升级场景并定义共享常量**

在 `internal/client/perf.go` 的报告类型附近增加：

```go
const ScenarioV8GPUCompletionSamples = 2048
```

在 `cmd/mcgo/benchmark.go` 修改：

```go
const scenarioVersion = 8
```

重新运行 Step 2 命令，预期 PASS。

- [ ] **Step 4: 写计时边界失败测试**

给 `integrationRenderDevice` 增加仅供测试观察的 `events []string`；让 `Submit`、`Poll` 和 `integrationEncoder.Finish()` 返回的 command `Release` 分别追加 `submit`、`poll`、`release`。然后在 `cmd/mcgo/benchmark_v6_test.go` 增加：

```go
func TestScenarioV8GPUCompletionTimesOnlySubmitAndPoll(t *testing.T) {
	app, dev := newRemoteRenderApplication(t, &integrationGlyphSource{})
	probe, err := newMultiplayerClientProbe(app)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(probe.Close)

	clockReads := 0
	probe.now = func() time.Time {
		dev.events = append(dev.events, "now")
		clockReads++
		return time.Unix(0, int64(clockReads)*int64(time.Millisecond))
	}
	dev.events = nil
	if err := probe.measureGPUCompletion(app); err != nil {
		t.Fatal(err)
	}
	if got := probe.gpuComplete.Summary().Samples; got != 2048 {
		t.Fatalf("GPU samples=%d, want 2048", got)
	}
	want := []string{"now", "submit", "poll", "now", "release"}
	for sample := range 2048 {
		start := sample * len(want)
		if got := dev.events[start : start+len(want)]; !reflect.DeepEqual(got, want) {
			t.Fatalf("sample %d events=%v, want=%v", sample, got, want)
		}
	}
}
```

运行：

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo -run TestScenarioV8GPUCompletionTimesOnlySubmitAndPoll -count=1'
```

预期：编译 FAIL，指出 `probe.now` 尚不存在；这证明测试确实约束新的计时接口。

- [ ] **Step 5: 最小实现 2048 样本计时**

在 `multiplayerClientProbe` 增加：

```go
now func() time.Time
```

构造时设置：

```go
gpuComplete: client.NewLatencyRecorder(client.ScenarioV8GPUCompletionSamples),
now:         time.Now,
```

把 `measureGPUCompletion` 的核心循环改为：

```go
for range client.ScenarioV8GPUCompletionSamples {
	if err := app.nameTagRenderer.Prepare(tags, app.renderer.UploadBudget()); err != nil {
		return err
	}
	encoder := app.dev.CreateCommandEncoder()
	app.avatarRenderer.Render(encoder, app.colorView, app.depth.view, render.Camera{
		ViewProj: app.camera.ViewProj(), Pos: app.camera.Pos,
	}, avatars)
	app.nameTagRenderer.Render(encoder, app.colorView, app.depth.view, benchmarkBillboardCamera(app))
	command := encoder.Finish()
	started := probe.now()
	app.dev.Submit(command)
	app.dev.Poll(true)
	probe.gpuComplete.Add(probe.now().Sub(started))
	command.Release()
}
```

计时开始前必须已经 `Finish`，计时结束后才允许 `Release`。

- [ ] **Step 6: 锁定 producer 报告完整性**

在 `validateBenchmarkReport` 的多人 latency 校验中，对 `remote_gpu_complete` 使用：

```go
minimum := 256
if name == "remote_gpu_complete" && report.ScenarioVersion >= 8 {
	minimum = client.ScenarioV8GPUCompletionSamples
}
```

增加测试：scenario v8 的 `RemoteGPUComplete.Samples=2047` 必须报样本过低；改为 2048 后必须通过。scenario v7 的 256 样本仍通过。

```go
func TestScenarioV8BenchmarkReportRequires2048GPUCompletionSamples(t *testing.T) {
	report := validBenchmarkReport()
	report.ScenarioVersion = 8
	report.Multiplayer = validMultiplayerSummary()
	report.Multiplayer.RemoteGPUComplete.Samples = 2047
	if err := validateBenchmarkReport(report); err == nil ||
		!strings.Contains(err.Error(), "remote_gpu_complete") {
		t.Fatalf("2047 GPU samples error=%v", err)
	}
	report.Multiplayer.RemoteGPUComplete.Samples = 2048
	if err := validateBenchmarkReport(report); err != nil {
		t.Fatalf("2048 GPU samples rejected: %v", err)
	}
	report.ScenarioVersion = 7
	report.Multiplayer.RemoteGPUComplete.Samples = 256
	if err := validateBenchmarkReport(report); err != nil {
		t.Fatalf("v7 256 GPU samples rejected: %v", err)
	}
}
```

- [ ] **Step 7: 验证 producer 绿色**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo ./internal/client -race -count=1'
```

预期：PASS，测试不创建窗口。

---

### Task 3: TDD 更新 perfcheck 的 v8 契约

**Files:**
- Modify: `cmd/perfcheck/main.go`
- Modify: `cmd/perfcheck/main_test.go`

**Interfaces:**
- Consumes: `client.ScenarioV8GPUCompletionSamples` 和 `client.PerfReport.ScenarioVersion`。
- Produces: v6/v7 最少 256、v8 最少 2048 的完整性校验；普通 v7/v8 比较仍拒绝。

- [ ] **Step 1: 写 v8 失败测试**

在 `cmd/perfcheck/main_test.go` 增加：

```go
func completeV8ComparableReport(transport string) client.PerfReport {
	report := completeV7ComparableReport(transport)
	report.ScenarioVersion = 8
	report.Multiplayer.RemoteGPUComplete.Samples = 2048
	return report
}

func TestPerfcheckV8Requires2048GPUCompletionSamples(t *testing.T) {
	baseline := completeV8ComparableReport("memory")
	current := completeV8ComparableReport("tcp")
	current.Multiplayer.RemoteGPUComplete.Samples = 2047
	if _, err := compareReports(baseline, current, 0.20); err == nil ||
		!strings.Contains(err.Error(), "remote_gpu_complete") {
		t.Fatalf("v8 low GPU samples error=%v", err)
	}

	v7 := completeV7ComparableReport("memory")
	v7.Multiplayer.RemoteGPUComplete.Samples = 256
	v7Current := completeV7ComparableReport("memory")
	v7Current.Multiplayer.RemoteGPUComplete.Samples = 256
	if failures, err := compareReports(v7, v7Current, 0.20); err != nil || len(failures) != 0 {
		t.Fatalf("v7 compatibility failures=%v err=%v", failures, err)
	}
}
```

运行：

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/perfcheck -run TestPerfcheckV8Requires2048GPUCompletionSamples -count=1'
```

预期：FAIL，因为 2047 仍被现有 256 下限接受。

- [ ] **Step 2: 最小实现按场景选择下限**

在 `validateV6Report` 构造 latency 清单前增加：

```go
remoteGPUCompletionSamples := 256
if report.ScenarioVersion >= 8 {
	remoteGPUCompletionSamples = client.ScenarioV8GPUCompletionSamples
}
```

并把 `remote_gpu_complete` 的 `minSamples` 改为该变量。不要修改 `appendV6MultiplayerRegressions` 或任何阈值。

- [ ] **Step 3: 锁定 v8 同场景与跨场景行为**

补充两个断言：

```go
baseline := completeV8ComparableReport("memory")
current := completeV8ComparableReport("tcp")
if failures, err := compareReports(baseline, current, 0.20); err != nil || len(failures) != 0 {
	t.Fatalf("v8 comparison failures=%v err=%v", failures, err)
}
if _, err := compareReports(completeV7ComparableReport("memory"), current, 0.20); err == nil ||
	!strings.Contains(err.Error(), "scenario_version") {
	t.Fatalf("v7/v8 mismatch error=%v", err)
}
```

- [ ] **Step 4: 验证比较器绿色且阈值未变**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/perfcheck -race -count=1'
```

预期：PASS；既有“精确 20% 通过、20.1% 失败”和 flying p99 `12ms` 失败测试保持通过。

- [ ] **Step 5: 更新任务并提交 v8 代码组**

勾选 OpenSpec 1.1–1.4，执行：

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo ./cmd/perfcheck ./internal/client -race -count=1 && go test ./internal/archcheck -count=1'
gofmt -l cmd/mcgo cmd/perfcheck internal/client
openspec validate --all --strict --no-interactive
git diff --check
```

预期：全部通过且 `gofmt -l` 无输出。完成 `detect_changes` 或 fallback 后，只暂存上述代码、测试、本 change 和任务勾选，提交：

```bash
git commit -m "fix: 稳定 GPU 完成性能门禁"
```

---

### Task 4: 修复探针阶段屏障并执行新的 M5 v8 正式链

**Files:**
- Modify: `internal/server/server.go`
- Modify: `internal/server/attached_test.go`
- Modify: `cmd/mcgo/benchmark.go`
- Modify: `cmd/mcgo/benchmark_v6_test.go`
- Modify after successful formal chain: `docs/notes/perf-baseline-m5.json`
- Modify after successful formal chain: `docs/notes/perf-baseline-m5.md`
- Modify: `openspec/changes/stabilize-remote-gpu-completion-gate/tasks.md`

**Interfaces:**
- Consumes: 已提交的 scenario v8 producer/validator，以及首轮失败报告。
- Produces: 同步 `Server.CloseTrustedObserver` 屏障、不含 M4D 的 M5 v8 Memory 基线及一次 TCP parity 证据。

- [x] **Step 1: 冻结首轮正式链失败证据**

提交 `4bda1bf309b4dfe3dbbc4d64c58772a5bbf6d48c` 上的 Memory/TCP 各执行一次。Memory 自检通过，TCP 报告生成成功，但跨 transport 门禁因 GPU p99 `1.338333ms → 2.549958ms`（`90.5%`）失败。Memory/TCP SHA-256 分别为 `a2156dde788e35f26d47fd3b1ed5e0b81ac047761114e8d4b9b1598a50ffd005` 与 `e427a24d493a90d762ae15cea329aa6325093248d1e9ae3afa05ad66d361500f`。不得重跑、复用或提升这两份报告。

- [ ] **Step 2: 先写同步 observer 收尾失败测试**

对 `runBenchmark`、`closeClientSession` 和 `detachTrustedObserverLocked` 完成 impact/fallback。新增 server 测试，要求显式关闭在返回前移除 observer、关闭 endpoint 且重复调用安全；新增 headless benchmark 测试，要求 observer 收尾发生在首个 GPU 时钟读取前。先运行定向测试并确认红灯。

- [ ] **Step 3: 最小实现并提交阶段屏障**

在 `Server` 增加幂等 `CloseTrustedObserver`，只复用 `stepMu` 与 `detachTrustedObserverLocked`。benchmark 在 `measureGPUCompletion` 前先调用它，再调用 `closeClientSession`；不增加 sleep、轮询、重试、依赖或 scenario 版本。执行：

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server ./cmd/mcgo ./cmd/perfcheck ./internal/client -race -count=1 && go test ./internal/archcheck -count=1'
gofmt -l internal/server cmd/mcgo cmd/perfcheck internal/client
openspec validate --all --strict --no-interactive
git diff --check
```

完成 `detect_changes` 或 fallback，勾选 1.6–1.8，只暂存屏障代码、测试与本 change，提交 `fix: 隔离 GPU 探针传输收尾`。

- [ ] **Step 4: 全量验证并生成新的精确正式路径**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race -count=1 && go vet ./... && go test ./internal/archcheck -count=1 && gofmt -l .'
openspec validate --all --strict --no-interactive
git diff --check
```

从屏障修复后的新 HEAD 派生 `/tmp/mcgo-m5-v8-<head12>-{memory,tcp}.json`，确认路径不存在、无遗留进程，并重新记录硬件/OS/Go/电源/负载与 M2 哈希。向用户报告新 HEAD、展开后的路径及一次性边界，取得明确确认后才能继续。

- [ ] **Step 5: 使用新路径恰好运行一次 Memory 并自检**

使用 GVM 和 headless benchmark 生成新 Memory 报告，以自身作为 baseline/current 运行 `cmd/perfcheck --max-regression 0.20`。确认 scenario=8、transport=memory、hardware=`Apple M5 / 24GiB`、framebuffer=`2560x1440`、GPU samples=2048；任一失败立即停止，不执行 TCP。

- [ ] **Step 6: 使用新路径恰好运行一次 TCP 并比较**

只在 Step 5 通过后生成一次 TCP 报告，以新 Memory 为 baseline 执行相同 20% 门禁。确认同硬件、同场景、同 framebuffer 和 GPU samples=2048；任一失败立即停止且不重跑。

- [ ] **Step 7: 仅在新正式链全部通过后提升基线**

使用 `apply_patch` 把新 Memory JSON 的精确内容写入 `docs/notes/perf-baseline-m5.json`，不得手工修改数值。中文 provenance 同时记录 v7 波动、首轮 v8 失败证据、屏障修复提交、新正式链 HEAD/命令、Memory/TCP SHA-256、门禁输出，以及“每条正式链各执行一次、失败链未重跑、无窗口、M2 基线未改”。

- [ ] **Step 8: 验证精确字节并提交基线**

确认 M5 JSON 与新 Memory 临时报告逐字一致并通过自比较，M2 哈希未变；执行受影响测试、OpenSpec strict、`gofmt -l .` 与 `git diff --check`。勾选 2.5–3.2，完成 `detect_changes` 或 fallback，只暂存两个 M5 文件和本 change，提交 `chore: 建立 M5 scenario v8 基线`。

---

### Task 5: 线性带回 main 并交还 M4D

**Files:**
- Modify: `openspec/changes/stabilize-remote-gpu-completion-gate/tasks.md`
- Resume afterward: `openspec/changes/m4d-authoritative-crafting/tasks.md`

**Interfaces:**
- Consumes: 隔离分支的 `fix: 稳定 GPU 完成性能门禁`、`fix: 隔离 GPU 探针传输收尾` 与 `chore: 建立 M5 scenario v8 基线` 三个提交。
- Produces: 当前 `main` 上的 v8 代码/基线，以及可恢复执行的 M4D 5.5。

- [ ] **Step 1: 列出并确认待带入提交**

在主工作区执行：

```bash
git log --reverse --format='%H %s' 0eace21..codex/stabilize-remote-gpu-completion-gate
git status --short
```

跳过隔离分支上的规划 cherry-pick；只选择消息为 `fix: 稳定 GPU 完成性能门禁`、`fix: 隔离 GPU 探针传输收尾` 和 `chore: 建立 M5 scenario v8 基线` 的三个精确哈希。

- [ ] **Step 2: 一次一个线性带入**

依次对 Step 1 得到的三个精确哈希执行 `git cherry-pick`，并把该哈希作为唯一参数。每次后运行 `git status --short`，确认 M4D 四个未提交文件和 `midscene_run/` 仍存在且未暂存；冲突时停止并报告，不猜测覆盖方向。

- [ ] **Step 3: 在 main 全量验证**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race -count=1 && go vet ./... && go test ./internal/archcheck -count=1 && gofmt -l .'
openspec validate --all --strict --no-interactive
git diff --check
```

预期：全部通过，M5 baseline scenario=8、M2 哈希不变、无窗口出现。

- [ ] **Step 4: 关闭性能修复 change**

勾选 4.1–4.3，完成 `detect_changes` 或 fallback，只暂存该任务文件，提交：

```bash
git commit -m "chore: 关闭 scenario v8 GPU 门禁修复"
```

- [ ] **Step 5: 返回 M4D**

把 M4D 5.5 从 scenario v7 更新为 v8，并明确使用新 M5 v8 基线。随后重新读取 M4D proposal/specs/design/tasks，按其“一次 Memory、通过后一次 TCP、任一步失败停止”的规则继续，不能复用或提升之前失败的 `/tmp/mcgo-m4d-perf.pwI2ub/memory.json`。
