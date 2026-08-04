# 稳定 interest 相对回归门禁实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 从 scenario v6 及后续同 transport 相对比较中移除不可重复的 `interest_diff` 摘要，同时保留报告完整性、其他性能门禁和 M4D 一次性正式证据链。

**Architecture:** `cmd/perfcheck` 继续解析并完整校验 `interest_diff`，但构造同 transport 的稳定延迟列表时不再追加该历史字段。producer、JSON schema、scenario v8 与 M5 baseline 不变；修复后复判既有不可变 Memory JSON，再从原始 M4D 精确生产提交生成唯一一次 headless TCP 报告。

**Tech Stack:** Go 1.26.0（仅通过 GVM）、标准库 `testing`、OpenSpec、现有 `cmd/perfcheck`、macOS headless WebGPU benchmark、Git。

## Global Constraints

- 所有 Go 命令 MUST 通过 `zsh -ic 'gvm use go1.26.0 >/dev/null && ...'` 执行，不下载或安装 Go。
- 自动验证和正式 benchmark MUST 使用现有 headless 路径，不启动或聚焦前台游戏窗口。
- 不修改 benchmark producer、服务端发布路径、报告 schema、scenario v8、20% 阈值、任何绝对门禁或 `docs/notes/perf-baseline-m5.json`。
- `/tmp/mcgo-m4d-v8-6d275a81688e-memory.json` 不得重跑、改写或替换；执行前后 SHA-256 都 MUST 为 `a878195ac2e37bf5d1fe19cc67b833fcd7371073586d768f7cdf6834b2e84d95`。
- TCP 报告只允许从精确生产提交 `6d275a81688e8b53263ae17ecc7754b02c9ba601` 生成一次；失败立即停止，不重跑。
- 保留 `README.md`、`docs/notes/lan-server.md`、`internal/server/tcp_integration_test.go`、现有 M4D tasks 改动和 `midscene_run/`；只有指定步骤可追加 M4D 5.5 证据。
- GitNexus 已从仓库移除且当前无可调用工具；每次要求 impact/detect_changes 时 MUST 记录精确 `rg`/diff fallback，不得静默跳过。

---

## 文件边界

- Modify: `cmd/perfcheck/main_test.go` — 锁定 `interest_diff` 仅保留完整性、不参与同 transport 相对比较，并保留其他指标反例。
- Modify: `cmd/perfcheck/main.go:469-512` — 从 `appendV6MultiplayerRegressions` 的相对延迟列表中移除 `interest_diff`，保留 server outbound bytes 与 RSS。
- Modify: `openspec/changes/stabilize-interest-diff-regression-gate/tasks.md` — 随每个已验证步骤勾选并记录精确证据。
- Modify after formal pass only: `openspec/changes/m4d-authoritative-crafting/tasks.md` — 追加 Memory 复判与 TCP 一次性结果并勾选 5.5。
- No change: `cmd/mcgo/**`、`internal/server/**`、`internal/client/perf.go`、`docs/notes/perf-baseline-m5.json`。

### Task 1: 锁定比较器影响范围

**Files:**
- Read: `cmd/perfcheck/main.go`
- Read: `cmd/perfcheck/main_test.go`
- Modify: `openspec/changes/stabilize-interest-diff-regression-gate/tasks.md`

**Interfaces:**
- Consumes: `compareReports(...)` 对 scenario v6 及后续同场景报告选择的比较 profile。
- Produces: 编辑前 blast-radius 记录；不产生代码变化。

- [ ] **Step 1: 尝试 GitNexus impact 并记录 fallback**

先检查当前工具列表。若仍没有 GitNexus，执行：

```bash
rg -n 'appendV6MultiplayerRegressions\(' cmd/perfcheck
rg -n 'includeServerProbe|crossTransportStable|stablePair' cmd/perfcheck/main.go cmd/perfcheck/main_test.go
```

Expected: `appendV6MultiplayerRegressions` 只有一个生产调用点，来自 `compareReportsWithScenarioUpgrade`；`includeServerProbe` 只在同 scenario、同 transport 时为 true。向用户报告风险为 MEDIUM：影响 scenario v6 及后续同 transport 性能判定，但不影响报告生成、跨 transport profile 或运行时游戏代码。

- [ ] **Step 2: 核对工作区所有权**

Run:

```bash
git status --short --branch
git diff --name-only
git diff --cached --name-only
```

Expected: 没有 staged 文件；用户既有四个 M4D 文件和 `midscene_run/` 保持原样。若 `cmd/perfcheck` 已有未知改动，停止并报告，不覆盖。

- [ ] **Step 3: 勾选 Task 1 并校验规划差异**

只更新本 change 的 `tasks.md` 1.1；执行：

```bash
openspec validate --all --strict --no-interactive
git diff --check
```

Expected: OpenSpec 全部通过，diff check 无输出。此只读影响分析不单独提交，与 Task 2 的 TDD 提交一起交付。

### Task 2: 用 TDD 移除不稳定相对字段

**Files:**
- Modify: `cmd/perfcheck/main_test.go:680-720`
- Modify: `cmd/perfcheck/main_test.go:426-489`
- Modify: `cmd/perfcheck/main.go:469-512`
- Modify: `openspec/changes/stabilize-interest-diff-regression-gate/tasks.md`

**Interfaces:**
- Consumes: `compareReports(baseline client.PerfReport, current client.PerfReport, maxRegression float64) ([]string, error)`。
- Produces: `appendV6MultiplayerRegressions` 不再把 `InterestDiff` 送入 `appendStableSummaryRegressions`；`validateV6Report` 行为不变。

- [ ] **Step 1: 写入在旧实现上失败的相对比较测试**

在 `TestPerfcheckV6SameTransportChecksStableServerProbeOnly` 之后加入：

```go
func TestPerfcheckV8SameTransportIgnoresInterestPublicationLatency(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*client.LatencySummary)
	}{
		{name: "p50", mutate: func(summary *client.LatencySummary) {
			summary.P50MS *= 1.201
		}},
		{name: "p95", mutate: func(summary *client.LatencySummary) {
			summary.P95MS *= 1.201
		}},
		{name: "p99", mutate: func(summary *client.LatencySummary) {
			summary.P99MS *= 1.201
		}},
		{name: "max", mutate: func(summary *client.LatencySummary) {
			summary.MaxMS *= 1.201
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			baseline := completeV8ComparableReport("memory")
			current := completeV8ComparableReport("memory")
			test.mutate(&current.Multiplayer.InterestDiff)
			failures, err := compareReports(baseline, current, 0.20)
			if err != nil || len(failures) != 0 {
				t.Fatalf("interest publication latency failures=%v err=%v", failures, err)
			}
		})
	}
}
```

从 `TestPerfcheckV6SameTransportChecksStableServerProbeOnly` 的 table 删除旧的 `interest p99` 必须失败案例；保留 tick p99、outbound 和 RSS 三个反例。

- [ ] **Step 2: 补齐完整性反例**

在 `TestPerfcheckScenarioUpgradeRejectsIncompleteV6CoreReport` 的 table 加入：

```go
{name: "interest percentile zero", want: "interest_diff", mutate: func(report *client.PerfReport) {
	report.Multiplayer.InterestDiff.P50MS = 0
}},
{name: "interest percentile non-monotonic", want: "interest_diff", mutate: func(report *client.PerfReport) {
	report.Multiplayer.InterestDiff.P50MS = report.Multiplayer.InterestDiff.P95MS + 1
}},
```

既有 `interest sample count` 与 `low interest samples` 案例保持不变。

- [ ] **Step 3: 运行测试确认 RED**

Run:

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/perfcheck -run "TestPerfcheckV8SameTransportIgnoresInterestPublicationLatency|TestPerfcheckScenarioUpgradeRejectsIncompleteV6CoreReport|TestPerfcheckV6SameTransportChecksStableServerProbeOnly" -count=1'
```

Expected: `p50`、`p95` 或 `p99` 子测试至少一个失败，failure 包含 `interest_diff ... 退化 20.1%`；完整性和其他稳定指标测试通过。若因分位数非单调而失败，先修正测试数据，不改生产代码。

- [ ] **Step 4: 写最小生产修改**

把 `appendV6MultiplayerRegressions` 中下列整个 block 删除：

```go
if includeServerProbe {
	latencies = append(latencies, struct {
		name              string
		baseline, current client.LatencySummary
	}{name: "interest_diff", baseline: baseline.InterestDiff, current: current.InterestDiff})
}
```

不要改 `validateV6Report`、`appendStableSummaryRegressions` 或紧随其后的 outbound/RSS `if includeServerProbe` block。

- [ ] **Step 5: 运行测试确认 GREEN**

Run:

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/perfcheck -run "TestPerfcheckV8SameTransportIgnoresInterestPublicationLatency|TestPerfcheckScenarioUpgradeRejectsIncompleteV6CoreReport|TestPerfcheckV6SameTransportChecksStableServerProbeOnly" -count=1'
```

Expected: PASS。

- [ ] **Step 6: 运行包级 race 与架构验证**

Run:

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/perfcheck -race -count=1 && go test ./internal/archcheck -count=1'
gofmt -l cmd/perfcheck
git diff --check
```

Expected: 全部 PASS；`gofmt` 和 diff check 无输出。

- [ ] **Step 7: detect_changes fallback 与代码提交**

若 GitNexus `detect_changes` 仍不可用，执行：

```bash
git diff --stat
git diff -- cmd/perfcheck/main.go cmd/perfcheck/main_test.go openspec/changes/stabilize-interest-diff-regression-gate/tasks.md
rg -n 'interest_diff.*p99_ms|InterestDiff' cmd/perfcheck/main.go cmd/perfcheck/main_test.go
```

Expected: 生产差异只有删除一个 `interest_diff` profile block；测试仍保留完整性覆盖，不修改通用比较器、producer 或基线。随后只暂存这三个文件：

```bash
git add cmd/perfcheck/main.go cmd/perfcheck/main_test.go openspec/changes/stabilize-interest-diff-regression-gate/tasks.md
git diff --cached --check
git commit -m "fix: 移除不稳定 interest 相对门禁"
```

Expected: commit 成功；其他 M4D 文件和 `midscene_run/` 未暂存。提交后自动进入 Task 3。

### Task 3: 复判不可变 M4D Memory 报告

**Files:**
- Read only: `/tmp/mcgo-m4d-v8-6d275a81688e-memory.json`
- Read only: `docs/notes/perf-baseline-m5.json`
- Modify: `openspec/changes/stabilize-interest-diff-regression-gate/tasks.md`

**Interfaces:**
- Consumes: 修复后的 `cmd/perfcheck` CLI 与既有 Memory JSON 原始字节。
- Produces: Memory 报告在新契约下通过的不可变证据；不生成新 Memory 报告。

- [ ] **Step 1: 校验报告存在、哈希和 provenance**

Run:

```bash
test -f /tmp/mcgo-m4d-v8-6d275a81688e-memory.json
shasum -a 256 /tmp/mcgo-m4d-v8-6d275a81688e-memory.json
jq -e '.scenario_version == 8 and .transport == "memory" and .git_commit == "6d275a81688e8b53263ae17ecc7754b02c9ba601" and .hardware == "Apple M5 / 24GiB" and .framebuffer == "2560x1440" and .multiplayer.remote_gpu_complete.samples == 2048 and .multiplayer.interest_diff.samples == 1600 and .ticks.frames == 200' /tmp/mcgo-m4d-v8-6d275a81688e-memory.json
```

Expected: SHA-256 精确为 `a878195ac2e37bf5d1fe19cc67b833fcd7371073586d768f7cdf6834b2e84d95`，`jq` exit 0。任一不符立即停止，不重新生成。

- [ ] **Step 2: 用新比较器复判一次**

Run:

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck --baseline docs/notes/perf-baseline-m5.json --current /tmp/mcgo-m4d-v8-6d275a81688e-memory.json --max-regression 0.20'
```

Expected: exit 0，并输出 `同场景性能比较通过：适用的稳定指标退化均未超过阈值且绝对门禁通过`。若出现任何其他 failure，立即停止，不执行 TCP。

- [ ] **Step 3: 再次校验不可变哈希**

Run:

```bash
shasum -a 256 /tmp/mcgo-m4d-v8-6d275a81688e-memory.json
```

Expected: 仍为 `a878195ac2e37bf5d1fe19cc67b833fcd7371073586d768f7cdf6834b2e84d95`。

- [ ] **Step 4: 记录通过证据**

在本 change `tasks.md` 2.1 下追加精确命令、success 文本、执行时间和前后相同哈希并勾选 2.1。运行：

```bash
openspec validate --all --strict --no-interactive
git diff --check
```

Expected: PASS。此证据与 Task 4 的 TCP 结果一起提交，不单独提交；验证后自动进入 Task 4。

### Task 4: 从原始生产提交生成唯一一次 TCP 报告

**Files:**
- Create outside repository: `/tmp/mcgo-m4d-v8-6d275a81688e-tcp.json`
- Create isolated worktree at execution time: `.worktrees/m4d-v8-perf-6d275a8`
- Modify after pass: `openspec/changes/m4d-authoritative-crafting/tasks.md`
- Modify: `openspec/changes/stabilize-interest-diff-regression-gate/tasks.md`

**Interfaces:**
- Consumes: `cmd/mcgo --benchmark --benchmark-transport tcp` at commit `6d275a8`。
- Produces: 一份 scenario v8 TCP JSON 及其 SHA-256；不修改仓库 baseline。

- [ ] **Step 1: 使用 worktree skill 创建隔离环境**

在执行本 Task 前调用 `superpowers:using-git-worktrees`。目标必须是 detached、精确提交：

```bash
git worktree add --detach /Users/chen/chenwork/minecraft-go/.worktrees/m4d-v8-perf-6d275a8 6d275a81688e8b53263ae17ecc7754b02c9ba601
```

Expected: worktree 创建成功；随后：

```bash
git -C /Users/chen/chenwork/minecraft-go/.worktrees/m4d-v8-perf-6d275a8 rev-parse HEAD
git -C /Users/chen/chenwork/minecraft-go/.worktrees/m4d-v8-perf-6d275a8 status --short
```

Expected: HEAD 精确为完整 `6d275a8...`，status 无输出。若路径已存在，先只读检查；不是精确干净 worktree 时停止，不删除或复用未知目录。

- [ ] **Step 2: 执行 headless 与唯一目标 preflight**

Run:

```bash
test ! -e /tmp/mcgo-m4d-v8-6d275a81688e-tcp.json
pgrep -fl '(^|/)mcgo( |$)|(^|/)mcgod( |$)'
rg -n 'newHeadlessDevice: gfx.NewHeadlessDevice|dependencies.newHeadlessDevice' /Users/chen/chenwork/minecraft-go/.worktrees/m4d-v8-perf-6d275a8/cmd/mcgo/app.go
```

Expected: TCP 路径不存在；`pgrep` 无匹配（exit 1 可接受）；两个 headless 绑定点存在。任一实际游戏进程存在或目标文件存在时停止。

- [ ] **Step 3: 恰好执行一次 TCP benchmark**

在隔离 worktree 执行：

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport tcp --perf-output /tmp/mcgo-m4d-v8-6d275a81688e-tcp.json'
```

Expected: exit 0，并且只生成指定 JSON。该命令无论成功或失败都不得再次执行。

- [ ] **Step 4: 校验 TCP 报告完整性并记录哈希**

Run:

```bash
shasum -a 256 /tmp/mcgo-m4d-v8-6d275a81688e-tcp.json
jq -e '.scenario_version == 8 and .transport == "tcp" and .git_commit == "6d275a81688e8b53263ae17ecc7754b02c9ba601" and .hardware == "Apple M5 / 24GiB" and .framebuffer == "2560x1440" and .multiplayer.remote_gpu_complete.samples == 2048 and .multiplayer.interest_diff.samples == 1600 and .ticks.frames == 200' /tmp/mcgo-m4d-v8-6d275a81688e-tcp.json
```

Expected: `jq` exit 0，记录实际 SHA-256。失败立即停止，不重跑。

- [ ] **Step 5: 对 M5 baseline 执行修复后的门禁**

回到主 worktree 执行：

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck --baseline docs/notes/perf-baseline-m5.json --current /tmp/mcgo-m4d-v8-6d275a81688e-tcp.json --max-regression 0.20'
```

Expected: exit 0，并输出同场景比较通过文本。任何 failure 立即停止，不修改报告、阈值或 baseline。

- [ ] **Step 6: 更新两份 tasks 的正式证据**

在 `m4d-authoritative-crafting/tasks.md` 的 5.5 下保留原始失败记录，再追加：

- 比较契约修复提交；
- Memory JSON 路径、前后相同 SHA-256 与复判通过文本；
- TCP JSON 路径、原始生产 commit、SHA-256 与比较通过文本；
- 未重跑 Memory、TCP 只执行一次、baseline 未覆盖；
- 将 5.5 勾选为完成。

同时勾选本 change tasks 2.1–2.4。执行：

```bash
openspec validate --all --strict --no-interactive
git diff --check
```

Expected: PASS。不要清理隔离 worktree，除非用户另行授权；直接进入 Task 5。

### Task 5: 全量验证、detect_changes fallback 与收尾提交

**Files:**
- Modify: `openspec/changes/stabilize-interest-diff-regression-gate/tasks.md`
- Modify: `openspec/changes/m4d-authoritative-crafting/tasks.md`
- Read only: all repository files during validation

**Interfaces:**
- Consumes: 已提交比较器修复、通过的 Memory/TCP 正式证据。
- Produces: 全量绿色验证、只含规格/证据勾选的收尾提交，并把执行权交回 M4D 5.6。

- [ ] **Step 1: 运行全仓 race**

Run:

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race -count=1'
```

Expected: PASS；不得出现前台窗口。失败时修复根因，不关闭或绕过 hook。

- [ ] **Step 2: 运行 vet、架构和格式门禁**

Run:

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go vet ./... && go test ./internal/archcheck -count=1 && gofmt -l .'
```

Expected: vet/archcheck PASS，`gofmt -l .` 无输出。

- [ ] **Step 3: 运行 OpenSpec 与 diff 门禁**

Run:

```bash
openspec validate --all --strict --no-interactive
git diff --check
```

Expected: 全部 OpenSpec 通过，diff check 无输出。

- [ ] **Step 4: 完成 detect_changes fallback**

若 GitNexus 仍不可用，执行：

```bash
git diff --stat HEAD
git diff --name-status HEAD
git diff HEAD -- cmd/perfcheck openspec/changes/stabilize-interest-diff-regression-gate openspec/changes/m4d-authoritative-crafting/tasks.md
git status --short
```

Expected: 代码修复已在前一提交；当前未提交差异只含两份 tasks 的证据/勾选。`README.md`、`docs/notes/lan-server.md`、`internal/server/tcp_integration_test.go` 和 `midscene_run/` 仍属于既有 M4D 工作区，不得暂存。

- [ ] **Step 5: 勾选收尾任务并提交证据**

勾选本 change 的 3.1–3.3，重复 OpenSpec strict 和 diff check。然后只暂存：

```bash
git add openspec/changes/stabilize-interest-diff-regression-gate/tasks.md openspec/changes/m4d-authoritative-crafting/tasks.md
git diff --cached --check
git diff --cached --name-status
git commit -m "chore: 关闭 interest 性能门禁修复"
```

Expected: commit 成功，staged scope 只有两份 tasks；其他用户/M4D 文件保持未暂存。

- [ ] **Step 6: 核对最终状态并自动返回 M4D**

Run:

```bash
git log -2 --oneline
git status --short --branch
openspec status --change stabilize-interest-diff-regression-gate
```

Expected: 本 change 任务全部完成，最近两次提交分别为比较器修复和收尾证据；随后读取 M4D proposal/specs/design/tasks，从 5.6 继续，不提前归档或覆盖现有 M4D 改动。
