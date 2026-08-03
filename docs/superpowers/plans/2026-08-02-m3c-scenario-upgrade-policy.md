# M3C Scenario Upgrade Policy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make explicit v5→v6 performance migration validate report integrity, hardware identity, and v6 absolute gates while applying the approved strict 20% stable-metric profiles to same-scenario reports, then close the M3C performance acceptance chain.

**Architecture:** `compareReportsWithScenarioUpgrade` first separates the allowed `5:6` migration from same-scenario comparison, then selects the v6 cross-transport or same-transport stable-metric profile. Every mode retains versioned schema checks, hardware matching, and current-v6 absolute gates. The CLI reports the mode accurately, and the accepted v6 baseline is updated only after a new Memory report, explicit migration validation, and a new same-scenario Memory→TCP comparison all pass.

**Tech Stack:** Go 1.26.0, existing `internal/client.PerfReport`, `cmd/perfcheck`, headless WebGPU benchmark, Memory/TCP transports, `jq`, GitNexus, Go test/race/fuzz/vet tooling.

## Global Constraints

- Work only in `/Users/sheepzhao/WorkSpace/minecraft-go/.worktrees/m3c-multiplayer-sync` on branch `codex/m3c-multiplayer-sync`.
- Execute tasks sequentially; never run benchmark reports or other performance-heavy commands in parallel.
- Preserve v6 absolute gates exactly: still/flying FPS `>=100`, frame p99 `<12ms`, phase and multiplayer RSS `<2GiB`, tick p99 `<10ms`, tick max `<50ms`, outbox `<=512`, player jobs `<=16`, player done `<=2`.
- Preserve strict 20% comparison for every metric selected by the applicable legacy, v6 cross-transport, or v6 same-transport profile; exactly 20% passes and strictly greater than 20% fails.
- Only exact `--allow-scenario-upgrade 5:6` may bypass cross-scenario relative comparisons. Missing, reverse, skipped, or different upgrades fail.
- Both reports must pass their versioned schema/field/sample validation and have identical non-empty hardware identifiers before either mode succeeds.
- A failed performance run or comparison is authoritative: stop immediately, do not rerun, do not change thresholds, and do not overwrite the accepted baseline.
- Do not change benchmark workload, report schema, Host outbox, renderer, client/server gameplay behavior, or v6 absolute thresholds.
- Before editing any existing function, method, class, or struct, run worktree-qualified `gitnexus_impact` upstream and report direct callers, affected processes, and risk. Stop for user approval on HIGH or CRITICAL.
- Before every commit, run worktree-qualified `gitnexus_detect_changes`; never commit generated GitNexus pollution.
- Task 17 production/test changes already share dirty files, so policy code remains uncommitted until the frozen final Task 17 commit `chore: 关闭 M3C 多人同步里程碑`.

## 2026-08-03 Superseding Repeatability Policy

This section supersedes the earlier blanket “all common same-scenario metrics” wording elsewhere in this historical plan:

- v6 Memory→TCP：比较 transport 相关稳定 p50/p95/p99、FPS、RSS、load/snapshot、protocol 与 persistence；raw max、queue high-water 和独立内存 server probe 不做跨 transport 相对比较。
- v6 同 transport：额外比较 server tick/interest p50/p95/p99、outbound 与 multiplayer RSS；raw max 和 queue high-water 仍只执行既有绝对门禁。
- server probe：8 登录完成后 warm-up 20 ticks，再由 TickObserver 信号驱动 200 measured ticks/1600 interest samples，不再使用第二个 50 ms ticker。

The rejected formal artifacts remain diagnostic-only and byte-identical:

```text
a86285d45a00e85f2bb0eb0ae960b3d4efd04beeecb31c917a852f6537ffbe01  /tmp/mcgo-m3c-memory-b58d8bd.json
12886882b273dd2e0712e78dc2d5f6fb0587c0aacca215c86162f581b0308771  /tmp/mcgo-m3c-tcp-b58d8bd.json
875e4533728f3c4bbcaed153bb1af821f4970e066ea36b3ae7be0d8ba69aeef4  /tmp/mcgo-m3c-step6-compare-b58d8bd.log
428e9b61bd8a8bf782fdc4e8d54f488d544bee4b7da948873638fe40ba60a191  docs/notes/perf-baseline.json
ac4dfffd78fc3a31d56b3cf728651e805535b60d302d2c2f55273d23f79ecfdb  docs/notes/perf-baseline.md
```

The last two entries remain the byte-frozen accepted v5 baseline until the replacement Step 6 chain passes in full.

## File Map

- Modify `cmd/perfcheck/main.go`: classify comparison mode, stop explicit `5:6` before relative comparisons, and emit mode-accurate success text.
- Modify `cmd/perfcheck/main_test.go`: TDD for migration-only validation, retained absolute/schema/hardware gates, v6 cross/same-transport 20% profiles, legacy behavior, success text, and mutation-sensitive boundaries.
- Modify `docs/superpowers/plans/2026-08-01-m3c-multiplayer-sync.md`: replace the superseded cross-scenario comparison wording and Step 6 expected result with the approved policy.
- Modify `.superpowers/sdd/2026-08-01-m3c-multiplayer-sync/task-17-brief.md`: mirror the approved migration contract for the active task.
- Modify `.superpowers/sdd/2026-08-01-m3c-multiplayer-sync/task-17-report.md` and `progress.md`: append exact RED/GREEN, review, command, artifact, hash, gate, and stop evidence.
- Modify `docs/notes/perf-baseline.json`: exact byte-for-byte v6 Memory artifact, only after Task 2 Step 8 permits it.
- Modify `docs/notes/perf-baseline.md`: accepted v6 provenance, commands, hashes, metrics, and benchmark results copied from actual artifacts only.
- No new production source file is needed.

---

### Task 1: Implement the two-mode comparison contract with TDD

**Files:**
- Modify: `cmd/perfcheck/main.go:15-217`
- Modify: `cmd/perfcheck/main_test.go:238-387`
- Modify: `docs/superpowers/plans/2026-08-01-m3c-multiplayer-sync.md:1631-1708`
- Modify: `.superpowers/sdd/2026-08-01-m3c-multiplayer-sync/task-17-brief.md:68-145`
- Modify: `.superpowers/sdd/2026-08-01-m3c-multiplayer-sync/task-17-report.md`
- Modify: `.superpowers/sdd/2026-08-01-m3c-multiplayer-sync/progress.md`

**Interfaces:**
- Consumes: `compareReportsWithScenarioUpgrade(baseline client.PerfReport, current client.PerfReport, maxRegression float64, allowScenarioUpgrade string) ([]string, error)` and existing versioned validators.
- Produces: unchanged comparison signature and a package-local `comparisonSuccessMessage(baselineVersion, currentVersion int) string` used by `main`.
- Preserves: `compareReports` remains the no-upgrade wrapper; legacy v5 comparisons, thresholds, schema validation, and absolute gates remain unchanged.

- [ ] **Step 1: Run pre-edit impact analysis and report blast radius**

Run worktree-qualified upstream impact for both existing functions that will change:

```text
gitnexus_impact(repo="/Users/sheepzhao/WorkSpace/minecraft-go/.worktrees/m3c-multiplayer-sync", target="main", file_path="cmd/perfcheck/main.go", kind="Function", direction="upstream")
gitnexus_impact(repo="/Users/sheepzhao/WorkSpace/minecraft-go/.worktrees/m3c-multiplayer-sync", target="compareReportsWithScenarioUpgrade", file_path="cmd/perfcheck/main.go", kind="Function", direction="upstream")
```

Expected: record direct callers and affected flows in `task-17-report.md`. If either result is HIGH or CRITICAL, stop before file edits and request approval. New `comparisonSuccessMessage` has no pre-existing symbol impact.

- [ ] **Step 2: Write the failing migration-policy tests**

Add the following behavior to `cmd/perfcheck/main_test.go` before production edits:

```go
func TestPerfcheckScenarioUpgradeSkipsRelativeRegressions(t *testing.T) {
	baseline := completeV5ComparableReport("memory")
	baseline.Persistence = client.PersistenceSummary{
		Snapshots: 10, P50MS: 1, P95MS: 1, P99MS: 1, MaxMS: 1,
	}
	current := completeV6ComparableReport("memory")
	current.LoadSeconds = 2
	current.SnapshotSeconds = 2
	current.Ticks = client.PhaseSummary{P50MS: 2, P95MS: 3, P99MS: 4, MaxMS: 5}
	current.Persistence = client.PersistenceSummary{
		Snapshots: 10, P50MS: 2, P95MS: 3, P99MS: 4, MaxMS: 5,
	}
	current.Protocol = client.ProtocolSummary{EncodeP99MS: 0.02, DecodeP99MS: 0.02, Bytes: 200}
	current.PlayerPersistence = client.PersistenceSummary{
		Snapshots: 10, P50MS: 0.02, P95MS: 0.03, P99MS: 0.04, MaxMS: 0.05,
	}
	for _, name := range []string{"still", "flying"} {
		current.Phases[name] = client.PhaseSummary{
			FPS: 100, P50MS: 2, P95MS: 3, P99MS: 4, MaxMS: 5, PeakRSSBytes: 2,
		}
	}

	failures, err := compareReportsWithScenarioUpgrade(baseline, current, 0.20, "5:6")
	if err != nil || len(failures) != 0 {
		t.Fatalf("explicit migration failures=%v err=%v", failures, err)
	}
}

func TestPerfcheckScenarioUpgradeKeepsAbsoluteAndSchemaGates(t *testing.T) {
	baseline := completeV5ComparableReport("memory")
	t.Run("absolute", func(t *testing.T) {
		current := completeV6ComparableReport("memory")
		phase := current.Phases["still"]
		phase.P99MS = 12
		current.Phases["still"] = phase
		failures, err := compareReportsWithScenarioUpgrade(baseline, current, 0.20, "5:6")
		if err != nil || !strings.Contains(strings.Join(failures, "\n"), "still p99") {
			t.Fatalf("absolute migration failures=%v err=%v", failures, err)
		}
	})
	t.Run("schema", func(t *testing.T) {
		current := completeV6ComparableReport("memory")
		current.Multiplayer.RosterApply = client.LatencySummary{}
		if _, err := compareReportsWithScenarioUpgrade(baseline, current, 0.20, "5:6"); err == nil ||
			!strings.Contains(err.Error(), "current") {
			t.Fatalf("schema migration error=%v", err)
		}
	})
}
```

Keep the existing tests for missing flags, reverse/skip upgrades, hardware mismatch, low samples, applicable-profile strict `>20%`, and exact `20%` boundary.

- [ ] **Step 3: Run the focused tests and verify RED**

Run:

```bash
go test ./cmd/perfcheck -run 'TestPerfcheck(ScenarioUpgrade.*|MultiplayerScenarioUpgradeAndProvenanceRules|MultiplayerChecksEverySharedMetricStrictlyAboveTwentyPercent)$' -count=1
```

Expected: FAIL only because `TestPerfcheckScenarioUpgradeSkipsRelativeRegressions` receives relative-regression failures from the current implementation. The absolute and schema subtests should already fail safely when their invalid input is used.

- [ ] **Step 4: Implement the minimal migration early return**

At the start of `compareReportsWithScenarioUpgrade`, compute the mode once and reuse it in the existing version check:

```go
scenarioUpgrade := baseline.ScenarioVersion != current.ScenarioVersion
if scenarioUpgrade &&
	!(baseline.ScenarioVersion == 5 && current.ScenarioVersion == 6 && allowScenarioUpgrade == "5:6") {
	return nil, fmt.Errorf(
		"scenario_version 不同：基线=%d 当前=%d",
		baseline.ScenarioVersion,
		current.ScenarioVersion,
	)
}
```

After both versioned validators, hardware equality, and `appendV6AbsoluteFailures`, add:

```go
if scenarioUpgrade {
	return failures, nil
}
```

Do not move or change any relative-comparison helper. Returning `failures` rather than `nil` preserves v6 absolute failures.

- [ ] **Step 5: Run the focused tests and verify GREEN**

Run the Step 3 command again.

Expected: PASS; explicit `5:6` ignores relative regressions, while the existing invalid-upgrade and applicable-profile 20% tests remain green.

- [ ] **Step 6: Write the failing success-message test**

Add:

```go
func TestComparisonSuccessMessageDescribesComparisonMode(t *testing.T) {
	if got := comparisonSuccessMessage(5, 6); got !=
		"场景迁移验证通过：报告完整、硬件一致且当前 v6 绝对门禁通过" {
		t.Fatalf("migration message=%q", got)
	}
	if got := comparisonSuccessMessage(6, 6); got !=
		"同场景性能比较通过：适用的稳定指标退化均未超过阈值且绝对门禁通过" {
		t.Fatalf("same-scenario message=%q", got)
	}
}
```

- [ ] **Step 7: Run the message test and verify RED**

Run:

```bash
go test ./cmd/perfcheck -run '^TestComparisonSuccessMessageDescribesComparisonMode$' -count=1
```

Expected: build FAIL with `undefined: comparisonSuccessMessage`.

- [ ] **Step 8: Implement the minimal success-message helper and wire it into main**

Add:

```go
func comparisonSuccessMessage(baselineVersion, currentVersion int) string {
	if baselineVersion != currentVersion {
		return "场景迁移验证通过：报告完整、硬件一致且当前 v6 绝对门禁通过"
	}
	return "同场景性能比较通过：适用的稳定指标退化均未超过阈值且绝对门禁通过"
}
```

Replace the existing unconditional success print with:

```go
fmt.Println(comparisonSuccessMessage(baseline.ScenarioVersion, current.ScenarioVersion))
```

- [ ] **Step 9: Verify GREEN and kill both policy mutations**

Run:

```bash
go test ./cmd/perfcheck -count=1
go test -race ./cmd/perfcheck -count=1
```

Expected: both PASS.

Mutation A: temporarily remove only the `if scenarioUpgrade { return failures, nil }` block. Run `TestPerfcheckScenarioUpgradeSkipsRelativeRegressions`; it must FAIL with relative regression messages. Restore the exact hunk and verify PASS.

Mutation B: temporarily select the same-transport profile for a v6 cross-transport comparison. Run `TestPerfcheckV6CrossTransportIgnoresRawTailAndIndependentServerProbe`; it must FAIL because independent server-probe regressions reappear. Restore the exact hunk and verify PASS. Record before/after hashes so no mutation remains.

- [ ] **Step 10: Update the authoritative plan and active brief**

Use `apply_patch` to make these exact semantic replacements:

- Task 17 Step 1: explicit `5:6` validates versioned report completeness, hardware identity, and current-v6 absolute gates; it does not compare cross-scenario relative metrics.
- Task 17 Step 4: strict 20% comparison applies when hardware and scenario are the same.
- Task 17 Step 6: run Memory first, execute explicit v5→v6 migration validation, then run TCP and the same-v6 Memory→TCP comparison. Update baseline only after all three gates pass.
- Task 17 Step 7: retain strict accepted-v6→current-v6 comparison unchanged.

Apply the same wording to `task-17-brief.md`. Do not change v6 thresholds, output paths, report schema, or benchmark commands.

- [ ] **Step 11: Append evidence and request independent task review**

Append exact impact, RED, GREEN, mutation, focused, race, and diff-check evidence to `task-17-report.md` and `progress.md`. Run:

```bash
gofmt -w cmd/perfcheck/main.go cmd/perfcheck/main_test.go
go test ./cmd/perfcheck -count=1
go test -race ./cmd/perfcheck -count=1
go test ./... -count=1
git diff --check
```

Expected: all commands exit 0. Dispatch a fresh reviewer with read-only scope covering design compliance and code quality. Any finding returns to the same Task 1 implementer for a maximum five-round fix/re-review loop. Stop before Memory/TCP/current/baseline/full gates until review is Approved.

- [ ] **Step 12: Preserve the final-commit boundary**

Do not commit Task 1 separately because `cmd/perfcheck/main.go` and `main_test.go` already contain the active uncommitted Task 17 implementation. Confirm the accepted v5 baseline hashes remain:

```text
perf-baseline.json  428e9b61bd8a8bf782fdc4e8d54f488d544bee4b7da948873638fe40ba60a191
perf-baseline.md    ac4dfffd78fc3a31d56b3cf728651e805535b60d302d2c2f55273d23f79ecfdb
```

Expected: no baseline diff, Host/default benchmark outbox remain 512, and no `mcgo` process exists.

---

### Task 2: Execute the one-shot v6 performance acceptance chain

> **2026-08-03 归档声明：** 本 Task 2 及其 Step 6/Step 7 的 `/tmp/mcgo-m3c-policy-*.json` 固定路径、`.ticks.frames >= 100`、interest `>= 1000` 与原 baseline 提升命令均不可再执行，只作为被取代流程的审计记录。正式执行只能遵循 `2026-08-03-m3c-performance-repeatability.md` 的 Task 6–8，使用 commit 派生的 collision-safe 路径并验证精确 `200 measured ticks/1600 interest samples`；任何正式失败都必须停止且不得重跑。

**Files:**
- Read: `docs/notes/perf-baseline.json`
- Modify after gates only: `docs/notes/perf-baseline.json`
- Modify after gates only: `docs/notes/perf-baseline.md`
- Modify: `.superpowers/sdd/2026-08-01-m3c-multiplayer-sync/task-17-report.md`
- Modify: `.superpowers/sdd/2026-08-01-m3c-multiplayer-sync/progress.md`

**Interfaces:**
- Consumes: reviewed `cmd/perfcheck` two-mode contract and unchanged `cmd/mcgo --benchmark` scenario v6 generator.
- Produces: `/tmp/mcgo-m3c-policy-memory.json`, `/tmp/mcgo-m3c-policy-tcp.json`, `/tmp/mcgo-m3c-policy-current.json`, and an accepted v6 `docs/notes/perf-baseline.json` only after all pre-baseline gates pass.
- Preserves: every performance command is run at most once; a failed command stops Task 2 immediately.

- [ ] **Step 1: Verify the frozen pre-run state**

Run:

```bash
test ! -e /tmp/mcgo-m3c-policy-memory.json
test ! -e /tmp/mcgo-m3c-policy-tcp.json
test ! -e /tmp/mcgo-m3c-policy-current.json
test ! -e /tmp/mcgo-m3c-v5-baseline-before-policy.json
git diff --exit-code -- docs/notes/perf-baseline.json docs/notes/perf-baseline.md
shasum -a 256 docs/notes/perf-baseline.json docs/notes/perf-baseline.md
rg -n 'OutboxCapacity:[[:space:]]+512|benchmarkOutboxLimit[[:space:]]*=[[:space:]]*512' internal/server/config.go cmd/mcgo/multiplayer_benchmark.go
ps -ax -o pid=,command= | rg '[m]cgo( |$)'
```

Expected: all four temporary paths are absent; baseline JSON/Markdown hashes are the frozen values from Task 1 Step 12; both outbox constants are 512; process search returns no output. If a path already exists or a process is running, stop and report rather than deleting/reusing it.

- [ ] **Step 2: Generate exactly one new Memory report**

Run once:

```bash
go run ./cmd/mcgo --benchmark --perf-output /tmp/mcgo-m3c-policy-memory.json
```

Expected: exit 0 and create one scenario 6 Memory JSON report. Do not rerun on failure.

- [ ] **Step 3: Validate the Memory artifact and absolute gates**

Run:

```bash
jq empty /tmp/mcgo-m3c-policy-memory.json
test "$(tail -c 1 /tmp/mcgo-m3c-policy-memory.json | od -An -tx1 | tr -d ' ')" = "0a"
shasum -a 256 /tmp/mcgo-m3c-policy-memory.json
jq -e '
  def monotonic:
    (.p50_ms > 0 and .p95_ms > 0 and .p99_ms > 0 and .max_ms > 0 and
     .p50_ms <= .p95_ms and .p95_ms <= .p99_ms and .p99_ms <= .max_ms);
  def latency($minimum): (.samples >= $minimum and monotonic);
  (.scenario_version == 6) and (.transport == "memory") and
  ((.hardware | type) == "string" and (.hardware | length) > 0) and
  ((.os | type) == "string" and (.os | length) > 0) and
  ((.go_version | type) == "string" and (.go_version | length) > 0) and
  ((.git_commit | type) == "string" and (.git_commit | length) > 0) and
  ((.framebuffer | type) == "string" and (.framebuffer | length) > 0) and
  (.load_seconds > 0) and (.snapshot_seconds > 0) and
  ((.phases | keys | sort) == ["flying", "still"]) and
  (all(.phases[];
    .frames > 0 and .fps >= 100 and monotonic and .p99_ms < 12 and
    .peak_rss_bytes > 0 and .peak_rss_bytes < 2147483648)) and
  (.ticks.frames >= 100 and .ticks.fps == 0 and (.ticks | monotonic) and
    .ticks.p99_ms < 10 and .ticks.max_ms < 50) and
  (.persistence.snapshots > 0 and (.persistence | monotonic)) and
  (.protocol.bytes > 0 and .protocol.encode_p99_ms > 0 and
    .protocol.decode_p99_ms > 0 and .protocol.encode_p99_ms < 1 and
    .protocol.decode_p99_ms < 1) and
  (.player_persistence.snapshots > 0 and (.player_persistence | monotonic) and
    .player_persistence.p99_ms < 5 and .player_persistence.max_ms < 20) and
  ([
    .multiplayer.remote_state_encode,
    .multiplayer.remote_state_decode,
    .multiplayer.roster_apply,
    .multiplayer.interpolation,
    .multiplayer.avatar_submit,
    .multiplayer.name_tag_submit,
    .multiplayer.remote_gpu_complete
  ] | all(.[]; latency(256))) and
  (.multiplayer.interest_diff | latency(1000)) and
  (.multiplayer.server_outbound_bytes > 0) and
  (.multiplayer.outbox_high_water >= 0 and .multiplayer.outbox_high_water <= 512) and
  (.multiplayer.player_jobs_high_water >= 0 and .multiplayer.player_jobs_high_water <= 16) and
  (.multiplayer.player_done_high_water >= 0 and .multiplayer.player_done_high_water <= 2) and
  (.multiplayer.peak_rss_bytes > 0 and .multiplayer.peak_rss_bytes < 2147483648)
' /tmp/mcgo-m3c-policy-memory.json
```

Expected: JSON/trailing-newline/hash checks exit 0 and `jq` prints `true`. Any failure stops Task 2.

- [ ] **Step 4: Run exactly one explicit v5→v6 migration validation**

Run once:

```bash
go run ./cmd/perfcheck --baseline docs/notes/perf-baseline.json --current /tmp/mcgo-m3c-policy-memory.json --max-regression 0.20 --allow-scenario-upgrade 5:6
```

Expected exact success text:

```text
场景迁移验证通过：报告完整、硬件一致且当前 v6 绝对门禁通过
```

Any nonzero exit stops Task 2 without TCP or baseline changes.

- [ ] **Step 5: Generate exactly one new TCP report**

Run once:

```bash
go run ./cmd/mcgo --benchmark --benchmark-transport tcp --perf-output /tmp/mcgo-m3c-policy-tcp.json
```

Expected: exit 0 and create one scenario 6 TCP JSON report. Do not rerun on failure.

- [ ] **Step 6: Validate TCP provenance and compare same-scenario Memory→TCP**

Run:

```bash
jq empty /tmp/mcgo-m3c-policy-tcp.json
test "$(tail -c 1 /tmp/mcgo-m3c-policy-tcp.json | od -An -tx1 | tr -d ' ')" = "0a"
test "$(jq -r '.scenario_version' /tmp/mcgo-m3c-policy-tcp.json)" = "6"
test "$(jq -r '.transport' /tmp/mcgo-m3c-policy-tcp.json)" = "tcp"
test "$(jq -r '.hardware' /tmp/mcgo-m3c-policy-memory.json)" = "$(jq -r '.hardware' /tmp/mcgo-m3c-policy-tcp.json)"
shasum -a 256 /tmp/mcgo-m3c-policy-tcp.json
go run ./cmd/perfcheck --baseline /tmp/mcgo-m3c-policy-memory.json --current /tmp/mcgo-m3c-policy-tcp.json --max-regression 0.20
```

Expected: every command exits 0 and perfcheck prints:

```text
同场景性能比较通过：适用的稳定指标退化均未超过阈值且绝对门禁通过
```

Any failure stops Task 2 and preserves the v5 accepted baseline.

- [ ] **Step 7: Preserve a recoverable v5 copy and accept the exact Memory JSON**

Run:

```bash
cp docs/notes/perf-baseline.json /tmp/mcgo-m3c-v5-baseline-before-policy.json
shasum -a 256 /tmp/mcgo-m3c-v5-baseline-before-policy.json
cp /tmp/mcgo-m3c-policy-memory.json docs/notes/perf-baseline.json
cmp /tmp/mcgo-m3c-policy-memory.json docs/notes/perf-baseline.json
test "$(tail -c 1 docs/notes/perf-baseline.json | od -An -tx1 | tr -d ' ')" = "0a"
```

Expected: backup hash equals `428e9b61bd8a8bf782fdc4e8d54f488d544bee4b7da948873638fe40ba60a191`; `cmp` exits 0. This overwrite is authorized only because Steps 2–6 passed.

- [ ] **Step 8: Generate exactly one current v6 Memory report**

Run once:

```bash
go run ./cmd/mcgo --benchmark --perf-output /tmp/mcgo-m3c-policy-current.json
```

Expected: exit 0. Do not rerun on failure.

- [ ] **Step 9: Run the strict accepted-v6→current-v6 regression gate**

Run once:

```bash
jq empty /tmp/mcgo-m3c-policy-current.json
test "$(tail -c 1 /tmp/mcgo-m3c-policy-current.json | od -An -tx1 | tr -d ' ')" = "0a"
shasum -a 256 docs/notes/perf-baseline.json /tmp/mcgo-m3c-policy-memory.json /tmp/mcgo-m3c-policy-tcp.json /tmp/mcgo-m3c-policy-current.json
cmp docs/notes/perf-baseline.json /tmp/mcgo-m3c-policy-memory.json
go run ./cmd/perfcheck --baseline docs/notes/perf-baseline.json --current /tmp/mcgo-m3c-policy-current.json --max-regression 0.20
```

Expected: `cmp docs/notes/perf-baseline.json /tmp/mcgo-m3c-policy-memory.json` exits 0 and perfcheck prints the same-scenario success message. A failure stops Task 2; the newly accepted baseline remains the exact Step 7 Memory artifact because its pre-acceptance gates passed.

- [ ] **Step 10: Update baseline documentation from observed artifacts**

Only after the current same-scenario gate passes, extract exact values and run the three short microbenchmarks:

```bash
jq '{git_commit,hardware,os,go_version,framebuffer,scenario_version,transport,load_seconds,snapshot_seconds,phases,ticks,persistence,protocol,player_persistence,multiplayer}' /tmp/mcgo-m3c-policy-memory.json
shasum -a 256 /tmp/mcgo-m3c-policy-memory.json /tmp/mcgo-m3c-policy-tcp.json /tmp/mcgo-m3c-policy-current.json
go test ./internal/network ./internal/server ./internal/render -run '^$' -bench '(RemotePlayerStateCodec|EightPlayerInterest|RemoteAvatarNameTag)' -benchmem -count=3
```

Use `apply_patch` to replace the accepted-baseline section in `docs/notes/perf-baseline.md` with those observed values. Record the explicit migration command, Memory→TCP command, accepted-v6→current-v6 command, all success messages, all three artifact hashes, every multiplayer metric, and the three complete benchmark outputs. Do not round JSON, invent values, or copy metrics from an older report.

- [ ] **Step 11: Record the full acceptance evidence and request review**

Append all commands, exits, report byte sizes, trailing-newline checks, SHA-256 values, phase/tick/persistence/protocol/player-persistence/multiplayer metrics, comparison output, and stop decisions to `task-17-report.md` and `progress.md`. Confirm no `mcgo` process remains. Dispatch a fresh read-only reviewer to verify artifact provenance, one-shot execution, baseline byte identity, documentation fidelity, and threshold preservation. Fix documentation-only findings in place; any finding requiring a new benchmark run is a blocker and must be reported rather than rerun.

---

### Task 3: Run final gates, reviews, cleanup, and the frozen Task 17 commit

**Files:**
- Verify: all Task 17 production, tests, CI, docs, and accepted baseline files.
- Clean known generated pollution only: `.gitignore`, `.claude/`, `AGENTS.md`, `CLAUDE.md`.
- Modify: `.superpowers/sdd/2026-08-01-m3c-multiplayer-sync/task-17-report.md`
- Modify: `.superpowers/sdd/2026-08-01-m3c-multiplayer-sync/progress.md`

**Interfaces:**
- Consumes: Approved Task 1 code review and Approved Task 2 artifact review.
- Produces: clean final gates, GitNexus change map, broad whole-branch review, and commit `chore: 关闭 M3C 多人同步里程碑`.
- Preserves: the separate approved design commit `1758473`; no generated GitNexus setup files enter Task 17.

- [ ] **Step 1: Remove only proven GitNexus setup pollution recoverably**

Inspect:

```bash
git diff -- .gitignore
git status --short -- .gitignore .claude AGENTS.md CLAUDE.md
```

Expected: `.gitignore` contains only the agent-generated GitNexus ignore hunk, and `.claude/`, `AGENTS.md`, `CLAUDE.md` are the known untracked index setup files. If content differs, stop and ask before cleanup.

Use `apply_patch` to remove only the generated `.gitignore` hunk. Move the three untracked targets recoverably:

```bash
MCGO_POLLUTION_DIR=$(mktemp -d /tmp/mcgo-m3c-gitnexus-pollution.XXXXXX)
mv .claude AGENTS.md CLAUDE.md "$MCGO_POLLUTION_DIR"/
test -d "$MCGO_POLLUTION_DIR/.claude"
test -f "$MCGO_POLLUTION_DIR/AGENTS.md"
test -f "$MCGO_POLLUTION_DIR/CLAUDE.md"
```

Report the resolved `MCGO_POLLUTION_DIR` path.

- [ ] **Step 2: Run formatting, diff, unit, race, fuzz, vet, architecture, build, and physics gates**

Run sequentially:

```bash
gofmt -w $(git ls-files -co --exclude-standard '*.go')
git diff --check
go test ./... -count=1
go test ./internal/network ./internal/server ./internal/client ./internal/render ./cmd/mcgo -race -count=1
go test ./internal/network -run '^$' -fuzz '^FuzzSmallPacketCodec$' -fuzztime=10s
go test ./internal/network -run '^$' -fuzz '^FuzzReadFrame$' -fuzztime=10s
go vet ./...
go test ./internal/archcheck -count=1
CGO_ENABLED=0 GOOS=linux go build -o /tmp/mcgod-linux ./cmd/mcgod
set -o pipefail; go test ./internal/physics -run '^$' -bench '^BenchmarkStepPlayer' -benchmem | tee /tmp/mcgo-m3c-physics.txt
```

Expected: every command exits 0; fuzz finds no crash; Linux build succeeds without client/render/gfx dependencies; all three physics benchmark lines report `0 B/op` and `0 allocs/op`. A failure returns to the responsible task and does not proceed to commit.

- [ ] **Step 3: Run focused performance-policy and multiplayer verification**

Run:

```bash
go test ./cmd/perfcheck -count=1
go test -race ./cmd/perfcheck -count=1
go test ./internal/client ./cmd/mcgo ./internal/render ./internal/server ./internal/network -count=1
go test -race ./internal/client ./cmd/mcgo ./internal/render ./internal/server ./internal/network -count=1
test -z "$(go list -deps ./cmd/mcgod | rg 'internal/(client|render|gfx)|glfw|webgpu|x/image/font')"
test "$(rg -c 'BenchmarkStepPlayer.*0 B/op +0 allocs/op' /tmp/mcgo-m3c-physics.txt)" -eq 3
```

- [ ] **Step 4: Run GitNexus change detection and inspect every affected flow**

Run `gitnexus_detect_changes(repo="/Users/sheepzhao/WorkSpace/minecraft-go/.worktrees/m3c-multiplayer-sync", scope="all")`. Record changed symbols, direct dependents, affected processes, and risk in `task-17-report.md`. For every HIGH/CRITICAL changed symbol already approved earlier, confirm the diff stays within its recorded scope; any new HIGH/CRITICAL blast radius stops before commit.

- [ ] **Step 5: Dispatch task and broad whole-branch reviews**

First dispatch a fresh Task 17 reviewer using the task brief, design addendum, implementation plan, complete report, base `5958c85c8a2d6f954debdd94c881893280de3e18`, and current worktree diff. Require separate spec-compliance and code-quality verdicts.

After Task 17 is Approved, dispatch a different fresh reviewer for the whole branch from the original M3C base `0469e6882931c884aaadd5baeaa485d363f93014` through HEAD plus the uncommitted Task 17 diff. One fix dispatch and one scoped re-review are allowed; unresolved load-bearing findings stop the commit.

- [ ] **Step 6: Stage the exact Task 17 scope**

Stage only the reviewed Task 17 files:

```bash
git add \
  .github/workflows/ci.yml Makefile README.md \
  cmd/mcgo/app.go cmd/mcgo/app_test.go cmd/mcgo/benchmark.go \
  cmd/mcgo/benchmark_v5_test.go cmd/mcgo/benchmark_v6_test.go \
  cmd/mcgo/multiplayer_benchmark.go cmd/mcgo/multiplayer_capacity_test.go \
  cmd/mcgo/presentation_conversion_test.go \
  cmd/perfcheck/main.go cmd/perfcheck/main_test.go \
  docs/notes/lan-server.md docs/notes/perf-baseline.json docs/notes/perf-baseline.md \
  docs/superpowers/plans/2026-08-01-m3c-multiplayer-sync.md \
  docs/superpowers/plans/2026-08-02-m3c-scenario-upgrade-policy.md \
  internal/archcheck/deps_test.go \
  internal/client/perf.go internal/client/perf_test.go internal/client/remote_players.go \
  internal/client/presentation_allocation_test.go \
  internal/gfx/gfx.go internal/gfx/wgpu.go internal/gfx/bind_group_range_test.go \
  internal/network/benchmark_test.go \
  internal/render/avatar.go internal/render/avatar_test.go internal/render/font_atlas.go \
  internal/render/name_tag.go internal/render/name_tag_test.go \
  internal/render/dynamic_upload_test.go internal/render/hot_path_allocation_test.go \
  internal/render/multiplayer_bench_test.go \
  internal/server/config.go internal/server/host.go internal/server/publication.go \
  internal/server/host_stats_test.go internal/server/multiplayer_bench_test.go
```

The design spec is already committed in `1758473` and must not appear staged. `.superpowers/` ledger files are local execution records and remain outside git unless `git check-ignore -v` proves the repository tracks them. Verify `git diff --cached --name-only` contains no `.gitignore`, `.claude/`, `AGENTS.md`, `CLAUDE.md`, `/tmp` artifact, or unrelated file.

- [ ] **Step 7: Run staged change detection and commit**

Run worktree-qualified `gitnexus_detect_changes(scope="staged")`, inspect all results, then:

```bash
git diff --cached --check
git status --short
git commit -m "chore: 关闭 M3C 多人同步里程碑"
```

Expected: commit succeeds and includes only the reviewed Task 17 scope. Do not amend `1758473`.

- [ ] **Step 8: Verify post-commit state and transition to branch finishing**

Run:

```bash
git log -3 --oneline
git status --short
go test ./... -count=1
```

Expected: design commit `1758473` precedes the final Task 17 commit; no tracked or untracked Task 17 residue remains; full suite exits 0. Invoke `superpowers:verification-before-completion`, then `superpowers:finishing-a-development-branch` to present integration options.
