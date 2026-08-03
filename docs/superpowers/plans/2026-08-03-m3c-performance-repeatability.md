# M3C Performance Repeatability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the M3C scenario v6 one-shot acceptance flow compare only meaningful stable metrics while preserving the real 10-second eight-session probe, every report field, every existing absolute threshold, and the frozen accepted v5 baseline until all gates pass.

**Architecture:** Split v6 comparison into cross-transport parity and same-transport regression profiles inside `cmd/perfcheck` while leaving v5 and v5→v6 behavior intact. Add a Darwin-only epoch controller that gates tick/interest recording and uses Host tick-completion signals instead of a second 50 ms ticker. Commit all executable Task 17 sources before formal reports so JSON `git_commit` provenance identifies the code that ran.

**Tech Stack:** Go 1.26.0, `sync/atomic`, existing `client.LatencyRecorder`, `server.Host`, memory packet streams, table-driven tests, race detector, fuzzing, GitNexus, jq, SHA-256, headless Metal/WebGPU.

---

## File responsibilities

- `cmd/perfcheck/main.go` selects the v6 relative-comparison profile.
- `cmd/perfcheck/main_test.go` locks parity, regression, legacy-v5, exact-20%, schema, and absolute-gate behavior.
- `cmd/mcgo/multiplayer_probe_epoch.go` owns warm-up/measured/done state, tick signals, recorder gates, exact counts, and overflow state.
- `cmd/mcgo/multiplayer_probe_epoch_test.go` proves epoch behavior without a 10-second workload.
- `cmd/mcgo/multiplayer_benchmark.go` integrates the epoch with the real eight-login Host probe, tick-driven input, Stats/RSS sampling, canonical bytes, and cleanup.
- `cmd/mcgo/benchmark_v6_test.go` keeps the real 10-second integration proof and requires exactly `200/1600` samples.
- `Makefile`, `.github/workflows/ci.yml`, controlling plans, and the SDD ledger keep focused gates and policy text aligned.
- `docs/notes/perf-baseline.json` and `docs/notes/perf-baseline.md` remain frozen until all Step 6 gates pass.

---

### Task 1: Freeze evidence and run pre-edit GitNexus impact

**Files:**
- Read: `docs/superpowers/specs/2026-08-03-m3c-performance-repeatability-design.md`
- Read: `.superpowers/sdd/2026-08-01-m3c-multiplayer-sync/task-17-report.md`
- Read: `/tmp/mcgo-m3c-memory-b58d8bd.json`
- Read: `/tmp/mcgo-m3c-tcp-b58d8bd.json`
- Read: `/tmp/mcgo-m3c-step6-compare-b58d8bd.log`

- [ ] **Step 1: Verify the failed artifacts and frozen baseline**

```bash
cd /Users/sheepzhao/WorkSpace/minecraft-go/.worktrees/m3c-multiplayer-sync
shasum -a 256 \
  /tmp/mcgo-m3c-memory-b58d8bd.json \
  /tmp/mcgo-m3c-tcp-b58d8bd.json \
  /tmp/mcgo-m3c-step6-compare-b58d8bd.log \
  docs/notes/perf-baseline.json \
  docs/notes/perf-baseline.md
test "$(shasum -a 256 /tmp/mcgo-m3c-memory-b58d8bd.json | awk '{print $1}')" = \
  a86285d45a00e85f2bb0eb0ae960b3d4efd04beeecb31c917a852f6537ffbe01
test "$(shasum -a 256 /tmp/mcgo-m3c-tcp-b58d8bd.json | awk '{print $1}')" = \
  12886882b273dd2e0712e78dc2d5f6fb0587c0aacca215c86162f581b0308771
test "$(shasum -a 256 /tmp/mcgo-m3c-step6-compare-b58d8bd.log | awk '{print $1}')" = \
  875e4533728f3c4bbcaed153bb1af821f4970e066ea36b3ae7be0d8ba69aeef4
test "$(shasum -a 256 docs/notes/perf-baseline.json | awk '{print $1}')" = \
  428e9b61bd8a8bf782fdc4e8d54f488d544bee4b7da948873638fe40ba60a191
test "$(shasum -a 256 docs/notes/perf-baseline.md | awk '{print $1}')" = \
  ac4dfffd78fc3a31d56b3cf728651e805535b60d302d2c2f55273d23f79ecfdb
jq -r '[.scenario_version,.git_commit,.transport,.ticks.frames,.multiplayer.interest_diff.samples] | @tsv' \
  /tmp/mcgo-m3c-memory-b58d8bd.json \
  /tmp/mcgo-m3c-tcp-b58d8bd.json
git diff --exit-code -- docs/notes/perf-baseline.json docs/notes/perf-baseline.md
! pgrep -f '(^|/)(mcgo|mcgod)( |$)|go run ./cmd/(mcgo|mcgod)'
```

Expected: formal hashes remain `a86285d4…be01`, `12886882…771e`, and `875e4533…eef4`; baseline hashes remain `428e9b6…a191` and `ac4dfff…cfdb`; both reports show scenario 6, `200/1600`; no `mcgo` process.

- [ ] **Step 2: Refresh GitNexus if the index is stale or a required dirty-worktree symbol is absent**

```bash
npx gitnexus analyze
```

Expected: exit 0 and repository `minecraft-go` indexed from this worktree. Because several Task 17 symbols currently exist only in the dirty worktree, a “symbol not found” result is treated like a stale index: run this command once, then rerun the context/impact lookup before editing.

- [ ] **Step 3: Run upstream impact for every existing symbol to be edited**

```text
gitnexus_impact({target:"compareReportsWithScenarioUpgrade",file_path:"cmd/perfcheck/main.go",direction:"upstream",includeTests:true})
gitnexus_impact({target:"comparisonSuccessMessage",file_path:"cmd/perfcheck/main.go",direction:"upstream",includeTests:true})
gitnexus_impact({target:"appendMultiplayerRegressions",file_path:"cmd/perfcheck/main.go",direction:"upstream",includeTests:true})
gitnexus_impact({target:"measureMultiplayerServerProbe",file_path:"cmd/mcgo/multiplayer_benchmark.go",direction:"upstream",includeTests:true})
gitnexus_impact({target:"canonicalCountingServerStream",file_path:"cmd/mcgo/multiplayer_benchmark.go",kind:"Struct",direction:"upstream",includeTests:true})
gitnexus_impact({target:"Send",file_path:"cmd/mcgo/multiplayer_benchmark.go",kind:"Method",direction:"upstream",includeTests:true})
gitnexus_impact({target:"TestComparisonSuccessMessageDescribesComparisonMode",file_path:"cmd/perfcheck/main_test.go",direction:"upstream",includeTests:true})
gitnexus_impact({target:"TestPerfcheckMultiplayerChecksEverySharedMetricStrictlyAboveTwentyPercent",file_path:"cmd/perfcheck/main_test.go",direction:"upstream",includeTests:true})
gitnexus_impact({target:"TestScenarioV6EightSessionServerProbeIsRealAndBounded",file_path:"cmd/mcgo/benchmark_v6_test.go",direction:"upstream",includeTests:true})
```

Expected: each result lists direct callers, affected processes, modules, and risk. Report the blast radius before edits. Any HIGH/CRITICAL result stops Task 1 and requires explicit user approval.

- [ ] **Step 4: Record preflight in the local SDD ledger**

Use `apply_patch` to append artifact hashes, baseline hashes, impact results, and authorization state to `.superpowers/sdd/2026-08-01-m3c-multiplayer-sync/progress.md`.

Expected: no source, baseline, or `/tmp` artifact changes.

---

### Task 2: Implement v6 comparison profiles with TDD

**Files:**
- Modify: `cmd/perfcheck/main_test.go:318-448`
- Modify: `cmd/perfcheck/main.go:39-228`
- Modify: `cmd/perfcheck/main.go:304-410`

- [ ] **Step 1: Write cross-transport and same-transport RED tests**

Replace the broad “all shared metrics” v6 test with these profile contracts:

```go
func TestPerfcheckV6CrossTransportIgnoresRawTailAndIndependentServerProbe(t *testing.T) {
	baseline := completeV6ComparableReport("memory")
	current := completeV6ComparableReport("tcp")
	baseline.Persistence = client.PersistenceSummary{
		Snapshots: 10, P50MS: 1, P95MS: 2, P99MS: 3, MaxMS: 4,
	}
	current.Persistence = baseline.Persistence
	current.Ticks = client.PhaseSummary{P50MS: 2, P95MS: 3, P99MS: 3.7, MaxMS: 4.9}
	current.Persistence.MaxMS = 5
	for name, phase := range current.Phases {
		phase.MaxMS = 5
		current.Phases[name] = phase
	}
	current.PlayerPersistence.MaxMS = 0.049
	current.Multiplayer.InterestDiff.P99MS = 3.7
	current.Multiplayer.InterestDiff.MaxMS = 5
	current.Multiplayer.AvatarSubmit.MaxMS = 5
	current.Multiplayer.ServerOutboundBytes = 121
	current.Multiplayer.OutboxHighWater = 13
	current.Multiplayer.PlayerJobsHighWater = 13
	current.Multiplayer.PlayerDoneHighWater = 2
	current.Multiplayer.PeakRSSBytes = 121

	failures, err := compareReports(baseline, current, 0.20)
	if err != nil || len(failures) != 0 {
		t.Fatalf("cross-transport neutral/tail failures=%v err=%v", failures, err)
	}
}

func TestPerfcheckV6CrossTransportChecksStableTransportMetrics(t *testing.T) {
	for _, test := range []struct {
		name, want string
		mutate     func(*client.PerfReport)
	}{
		{name: "phase p99", want: "still p99_ms", mutate: func(report *client.PerfReport) {
			phase := report.Phases["still"]
			phase.P99MS *= 1.201
			report.Phases["still"] = phase
		}},
		{name: "persistence p95", want: "persistence p95_ms", mutate: func(report *client.PerfReport) {
			report.Persistence.P95MS *= 1.201
		}},
		{name: "avatar p99", want: "avatar_submit p99_ms", mutate: func(report *client.PerfReport) {
			report.Multiplayer.AvatarSubmit.P99MS *= 1.201
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			baseline := completeV6ComparableReport("memory")
			baseline.Persistence = client.PersistenceSummary{
				Snapshots: 10, P50MS: 1, P95MS: 2, P99MS: 3, MaxMS: 4,
			}
			current := completeV6ComparableReport("tcp")
			current.Persistence = baseline.Persistence
			test.mutate(&current)
			failures, err := compareReports(baseline, current, 0.20)
			if err != nil || !strings.Contains(strings.Join(failures, "\n"), test.want) {
				t.Fatalf("failures=%v err=%v want=%q", failures, err, test.want)
			}
		})
	}

	baseline := completeV6ComparableReport("memory")
	current := completeV6ComparableReport("tcp")
	current.Multiplayer.RemoteStateEncode.P99MS *= 1.20
	if failures, err := compareReports(baseline, current, 0.20); err != nil || len(failures) != 0 {
		t.Fatalf("exact 20%% must pass: failures=%v err=%v", failures, err)
	}
}

func TestPerfcheckV6CrossTransportCoversApprovedStableFieldMatrix(t *testing.T) {
	for _, test := range []struct {
		name, want string
		mutate     func(*client.PerfReport)
	}{
		{name: "load", want: "load_seconds", mutate: func(report *client.PerfReport) {
			report.LoadSeconds *= 1.201
		}},
		{name: "snapshot", want: "snapshot_seconds", mutate: func(report *client.PerfReport) {
			report.SnapshotSeconds *= 1.201
		}},
		{name: "still p50", want: "still p50_ms", mutate: func(report *client.PerfReport) {
			phase := report.Phases["still"]
			phase.P50MS, phase.P95MS, phase.P99MS, phase.MaxMS = 1.201, 2, 2, 2
			report.Phases["still"] = phase
		}},
		{name: "still p95", want: "still p95_ms", mutate: func(report *client.PerfReport) {
			phase := report.Phases["still"]
			phase.P95MS, phase.P99MS, phase.MaxMS = 1.201, 2, 2
			report.Phases["still"] = phase
		}},
		{name: "flying p99", want: "flying p99_ms", mutate: func(report *client.PerfReport) {
			phase := report.Phases["flying"]
			phase.P99MS, phase.MaxMS = 1.201, 2
			report.Phases["flying"] = phase
		}},
		{name: "phase rss", want: "still peak_rss_bytes", mutate: func(report *client.PerfReport) {
			phase := report.Phases["still"]
			phase.PeakRSSBytes = 2
			report.Phases["still"] = phase
		}},
		{name: "persistence p50", want: "persistence p50_ms", mutate: func(report *client.PerfReport) {
			report.Persistence.P50MS *= 1.201
		}},
		{name: "persistence p99", want: "persistence p99_ms", mutate: func(report *client.PerfReport) {
			report.Persistence.P99MS *= 1.201
		}},
		{name: "protocol encode", want: "protocol encode_p99_ms", mutate: func(report *client.PerfReport) {
			report.Protocol.EncodeP99MS *= 1.201
		}},
		{name: "protocol decode", want: "protocol decode_p99_ms", mutate: func(report *client.PerfReport) {
			report.Protocol.DecodeP99MS *= 1.201
		}},
		{name: "protocol bytes", want: "protocol bytes", mutate: func(report *client.PerfReport) {
			report.Protocol.Bytes = 121
		}},
		{name: "player persistence p50", want: "player_persistence p50_ms", mutate: func(report *client.PerfReport) {
			report.PlayerPersistence.P50MS *= 1.201
		}},
		{name: "player persistence p95", want: "player_persistence p95_ms", mutate: func(report *client.PerfReport) {
			report.PlayerPersistence.P95MS *= 1.201
		}},
		{name: "player persistence p99", want: "player_persistence p99_ms", mutate: func(report *client.PerfReport) {
			report.PlayerPersistence.P99MS *= 1.201
		}},
		{name: "remote encode", want: "remote_state_encode p99_ms", mutate: func(report *client.PerfReport) {
			report.Multiplayer.RemoteStateEncode.P99MS *= 1.201
		}},
		{name: "remote decode", want: "remote_state_decode p99_ms", mutate: func(report *client.PerfReport) {
			report.Multiplayer.RemoteStateDecode.P99MS *= 1.201
		}},
		{name: "roster", want: "roster_apply p99_ms", mutate: func(report *client.PerfReport) {
			report.Multiplayer.RosterApply.P99MS *= 1.201
		}},
		{name: "interpolation", want: "interpolation p99_ms", mutate: func(report *client.PerfReport) {
			report.Multiplayer.Interpolation.P99MS *= 1.201
		}},
		{name: "avatar", want: "avatar_submit p99_ms", mutate: func(report *client.PerfReport) {
			report.Multiplayer.AvatarSubmit.P99MS *= 1.201
		}},
		{name: "name tag", want: "name_tag_submit p99_ms", mutate: func(report *client.PerfReport) {
			report.Multiplayer.NameTagSubmit.P99MS *= 1.201
		}},
		{name: "gpu", want: "remote_gpu_complete p99_ms", mutate: func(report *client.PerfReport) {
			report.Multiplayer.RemoteGPUComplete.P99MS *= 1.201
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			baseline := completeV6ComparableReport("memory")
			baseline.Persistence = client.PersistenceSummary{
				Snapshots: 10, P50MS: 1, P95MS: 2, P99MS: 3, MaxMS: 4,
			}
			current := completeV6ComparableReport("tcp")
			current.Persistence = baseline.Persistence
			test.mutate(&current)
			failures, err := compareReports(baseline, current, 0.20)
			if err != nil || !strings.Contains(strings.Join(failures, "\n"), test.want) {
				t.Fatalf("failures=%v err=%v want=%q", failures, err, test.want)
			}
		})
	}

	baseline := completeV6ComparableReport("memory")
	current := completeV6ComparableReport("tcp")
	for _, report := range []*client.PerfReport{&baseline, &current} {
		phase := report.Phases["still"]
		phase.FPS = 200
		report.Phases["still"] = phase
	}
	phase := current.Phases["still"]
	phase.FPS = 159.8
	current.Phases["still"] = phase
	failures, err := compareReports(baseline, current, 0.20)
	if err != nil || !strings.Contains(strings.Join(failures, "\n"), "still fps") {
		t.Fatalf("fps failures=%v err=%v", failures, err)
	}
}

func TestPerfcheckV6SameTransportChecksStableServerProbeOnly(t *testing.T) {
	for _, test := range []struct {
		name, want string
		mutate     func(*client.PerfReport)
	}{
		{name: "tick p99", want: "ticks p99_ms", mutate: func(report *client.PerfReport) {
			report.Ticks.P99MS *= 1.201
		}},
		{name: "interest p99", want: "interest_diff p99_ms", mutate: func(report *client.PerfReport) {
			report.Multiplayer.InterestDiff.P99MS *= 1.201
		}},
		{name: "outbound", want: "server_outbound_bytes", mutate: func(report *client.PerfReport) {
			report.Multiplayer.ServerOutboundBytes = 121
		}},
		{name: "rss", want: "multiplayer peak_rss_bytes", mutate: func(report *client.PerfReport) {
			report.Multiplayer.PeakRSSBytes = 121
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			baseline := completeV6ComparableReport("memory")
			current := completeV6ComparableReport("memory")
			test.mutate(&current)
			failures, err := compareReports(baseline, current, 0.20)
			if err != nil || !strings.Contains(strings.Join(failures, "\n"), test.want) {
				t.Fatalf("failures=%v err=%v want=%q", failures, err, test.want)
			}
		})
	}

	baseline := completeV6ComparableReport("memory")
	current := completeV6ComparableReport("memory")
	current.Ticks.MaxMS *= 1.201
	current.Multiplayer.InterestDiff.MaxMS *= 1.201
	current.Multiplayer.OutboxHighWater = 13
	current.Multiplayer.PlayerJobsHighWater = 13
	current.Multiplayer.PlayerDoneHighWater = 2
	if failures, err := compareReports(baseline, current, 0.20); err != nil || len(failures) != 0 {
		t.Fatalf("same-transport raw tail/high-water failures=%v err=%v", failures, err)
	}
}
```

Add absolute and legacy guards:

```go
func TestPerfcheckV6ProfileKeepsAbsoluteGates(t *testing.T) {
	baseline := completeV6ComparableReport("memory")
	for _, test := range []struct {
		name, want string
		mutate     func(*client.PerfReport)
	}{
		{name: "tick max", want: "tick max", mutate: func(report *client.PerfReport) {
			report.Ticks.MaxMS = 50
		}},
		{name: "jobs limit", want: "player jobs high-water", mutate: func(report *client.PerfReport) {
			report.Multiplayer.PlayerJobsHighWater = 17
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			current := completeV6ComparableReport("tcp")
			test.mutate(&current)
			failures, err := compareReports(baseline, current, 0.20)
			if err != nil || !strings.Contains(strings.Join(failures, "\n"), test.want) {
				t.Fatalf("failures=%v err=%v want=%q", failures, err, test.want)
			}
		})
	}
}

func TestPerfcheckV5SameScenarioKeepsLegacyMaxComparison(t *testing.T) {
	baseline := completeV5ComparableReport("memory")
	current := completeV5ComparableReport("tcp")
	baseline.Persistence = client.PersistenceSummary{
		Snapshots: 10, P50MS: 1, P95MS: 2, P99MS: 3, MaxMS: 4,
	}
	current.Persistence = baseline.Persistence
	current.Persistence.MaxMS = 4.804
	failures, err := compareReports(baseline, current, 0.20)
	if err != nil || !strings.Contains(strings.Join(failures, "\n"), "persistence max_ms") {
		t.Fatalf("legacy failures=%v err=%v", failures, err)
	}
}
```

Update `TestComparisonSuccessMessageDescribesComparisonMode` to expect:

```go
"同场景性能比较通过：适用的稳定指标退化均未超过阈值且绝对门禁通过"
```

- [ ] **Step 2: Run focused tests and verify RED**

```bash
TERM=xterm-256color zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/perfcheck -run "Test(PerfcheckV6|PerfcheckV5SameScenario|ComparisonSuccessMessage)" -count=1'
```

Expected: FAIL because the old comparator still checks raw max, high-water, ticks, and independent server-probe fields across transports.

- [ ] **Step 3: Add stable-summary helpers**

Add to `cmd/perfcheck/main.go`:

```go
func appendStableSummaryRegressions(
	failures []string,
	prefix string,
	baselineP50, baselineP95, baselineP99 float64,
	currentP50, currentP95, currentP99 float64,
	threshold float64,
) []string {
	for _, metric := range []struct {
		name              string
		baseline, current float64
	}{
		{name: "p50_ms", baseline: baselineP50, current: currentP50},
		{name: "p95_ms", baseline: baselineP95, current: currentP95},
		{name: "p99_ms", baseline: baselineP99, current: currentP99},
	} {
		failures = appendRegression(
			failures, prefix, metric.name, metric.baseline, metric.current, threshold,
		)
	}
	return failures
}

func appendM3BStableLatencyRegressions(
	failures []string,
	prefix string,
	baselineP50, baselineP95, baselineP99 float64,
	currentP50, currentP95, currentP99 float64,
	threshold float64,
) []string {
	for _, metric := range []struct {
		name              string
		baseline, current float64
	}{
		{name: "p50_ms", baseline: baselineP50, current: currentP50},
		{name: "p95_ms", baseline: baselineP95, current: currentP95},
		{name: "p99_ms", baseline: baselineP99, current: currentP99},
	} {
		if metric.baseline < m3bLatencyNoiseFloorMS {
			continue
		}
		failures = appendRegression(
			failures, prefix, metric.name, metric.baseline, metric.current, threshold,
		)
	}
	return failures
}
```

Replace `appendMultiplayerRegressions` with:

```go
func appendV6MultiplayerRegressions(
	failures []string,
	baseline client.MultiplayerSummary,
	current client.MultiplayerSummary,
	threshold float64,
	includeServerProbe bool,
) []string {
	latencies := []struct {
		name              string
		baseline, current client.LatencySummary
	}{
		{name: "remote_state_encode", baseline: baseline.RemoteStateEncode, current: current.RemoteStateEncode},
		{name: "remote_state_decode", baseline: baseline.RemoteStateDecode, current: current.RemoteStateDecode},
		{name: "roster_apply", baseline: baseline.RosterApply, current: current.RosterApply},
		{name: "interpolation", baseline: baseline.Interpolation, current: current.Interpolation},
		{name: "avatar_submit", baseline: baseline.AvatarSubmit, current: current.AvatarSubmit},
		{name: "name_tag_submit", baseline: baseline.NameTagSubmit, current: current.NameTagSubmit},
		{name: "remote_gpu_complete", baseline: baseline.RemoteGPUComplete, current: current.RemoteGPUComplete},
	}
	if includeServerProbe {
		latencies = append(latencies, struct {
			name              string
			baseline, current client.LatencySummary
		}{name: "interest_diff", baseline: baseline.InterestDiff, current: current.InterestDiff})
	}
	for _, latency := range latencies {
		failures = appendStableSummaryRegressions(
			failures, latency.name,
			latency.baseline.P50MS, latency.baseline.P95MS, latency.baseline.P99MS,
			latency.current.P50MS, latency.current.P95MS, latency.current.P99MS,
			threshold,
		)
	}
	if includeServerProbe {
		failures = appendRegression(
			failures, "multiplayer", "server_outbound_bytes",
			float64(baseline.ServerOutboundBytes), float64(current.ServerOutboundBytes), threshold,
		)
		failures = appendRegression(
			failures, "multiplayer", "peak_rss_bytes",
			float64(baseline.PeakRSSBytes), float64(current.PeakRSSBytes), threshold,
		)
	}
	return failures
}
```

- [ ] **Step 4: Select the profile inside the existing comparison flow**

Keep validation, hardware checks, current-v6 absolute gates, and the migration early return unchanged. Replace the relative-comparison body after that early return with:

```go
v6Pair := baseline.ScenarioVersion == 6 && current.ScenarioVersion == 6
crossTransportV6 := v6Pair && baseline.Transport != current.Transport
for _, metric := range []struct {
	name              string
	baseline, current float64
}{
	{name: "load_seconds", baseline: baseline.LoadSeconds, current: current.LoadSeconds},
	{name: "snapshot_seconds", baseline: baseline.SnapshotSeconds, current: current.SnapshotSeconds},
} {
	failures = appendRegression(
		failures, "", metric.name, metric.baseline, metric.current, maxRegression,
	)
}
if v6Pair {
	if !crossTransportV6 {
		failures = appendStableSummaryRegressions(
			failures,
			"ticks",
			baseline.Ticks.P50MS,
			baseline.Ticks.P95MS,
			baseline.Ticks.P99MS,
			current.Ticks.P50MS,
			current.Ticks.P95MS,
			current.Ticks.P99MS,
			maxRegression,
		)
	}
} else {
	failures = appendSummaryRegressions(
		failures,
		"ticks",
		baseline.Ticks.P50MS,
		baseline.Ticks.P95MS,
		baseline.Ticks.P99MS,
		baseline.Ticks.MaxMS,
		current.Ticks.P50MS,
		current.Ticks.P95MS,
		current.Ticks.P99MS,
		current.Ticks.MaxMS,
		maxRegression,
	)
}
if baseline.Persistence.Snapshots > 0 && current.Persistence.Snapshots > 0 {
	if v6Pair {
		failures = appendStableSummaryRegressions(
			failures,
			"persistence",
			baseline.Persistence.P50MS,
			baseline.Persistence.P95MS,
			baseline.Persistence.P99MS,
			current.Persistence.P50MS,
			current.Persistence.P95MS,
			current.Persistence.P99MS,
			maxRegression,
		)
	} else {
		failures = appendSummaryRegressions(
			failures,
			"persistence",
			baseline.Persistence.P50MS,
			baseline.Persistence.P95MS,
			baseline.Persistence.P99MS,
			baseline.Persistence.MaxMS,
			current.Persistence.P50MS,
			current.Persistence.P95MS,
			current.Persistence.P99MS,
			current.Persistence.MaxMS,
			maxRegression,
		)
	}
}
if baseline.Protocol.EncodeP99MS >= m3bLatencyNoiseFloorMS &&
	current.Protocol.EncodeP99MS > 0 {
	failures = appendRegression(
		failures, "protocol", "encode_p99_ms",
		baseline.Protocol.EncodeP99MS, current.Protocol.EncodeP99MS, maxRegression,
	)
}
if baseline.Protocol.DecodeP99MS >= m3bLatencyNoiseFloorMS &&
	current.Protocol.DecodeP99MS > 0 {
	failures = appendRegression(
		failures, "protocol", "decode_p99_ms",
		baseline.Protocol.DecodeP99MS, current.Protocol.DecodeP99MS, maxRegression,
	)
}
if baseline.Protocol.Bytes > 0 && current.Protocol.Bytes > 0 {
	failures = appendRegression(
		failures, "protocol", "bytes",
		float64(baseline.Protocol.Bytes), float64(current.Protocol.Bytes), maxRegression,
	)
}
if baseline.PlayerPersistence.Snapshots > 0 &&
	current.PlayerPersistence.Snapshots > 0 {
	if v6Pair {
		failures = appendM3BStableLatencyRegressions(
			failures,
			"player_persistence",
			baseline.PlayerPersistence.P50MS,
			baseline.PlayerPersistence.P95MS,
			baseline.PlayerPersistence.P99MS,
			current.PlayerPersistence.P50MS,
			current.PlayerPersistence.P95MS,
			current.PlayerPersistence.P99MS,
			maxRegression,
		)
	} else {
		failures = appendM3BLatencyRegressions(
			failures,
			"player_persistence",
			baseline.PlayerPersistence.P50MS,
			baseline.PlayerPersistence.P95MS,
			baseline.PlayerPersistence.P99MS,
			baseline.PlayerPersistence.MaxMS,
			current.PlayerPersistence.P50MS,
			current.PlayerPersistence.P95MS,
			current.PlayerPersistence.P99MS,
			current.PlayerPersistence.MaxMS,
			maxRegression,
		)
	}
}
if v6Pair {
	failures = appendV6MultiplayerRegressions(
		failures,
		baseline.Multiplayer,
		current.Multiplayer,
		maxRegression,
		!crossTransportV6,
	)
}
phaseNames := make([]string, 0, len(baseline.Phases))
for name := range baseline.Phases {
	phaseNames = append(phaseNames, name)
}
sort.Strings(phaseNames)
for _, name := range phaseNames {
	basePhase := baseline.Phases[name]
	currentPhase, ok := current.Phases[name]
	if !ok {
		failures = append(failures, fmt.Sprintf("当前报告缺少阶段 %q", name))
		continue
	}
	if v6Pair {
		failures = appendStableSummaryRegressions(
			failures,
			name,
			basePhase.P50MS,
			basePhase.P95MS,
			basePhase.P99MS,
			currentPhase.P50MS,
			currentPhase.P95MS,
			currentPhase.P99MS,
			maxRegression,
		)
	} else {
		failures = appendSummaryRegressions(
			failures,
			name,
			basePhase.P50MS,
			basePhase.P95MS,
			basePhase.P99MS,
			basePhase.MaxMS,
			currentPhase.P50MS,
			currentPhase.P95MS,
			currentPhase.P99MS,
			currentPhase.MaxMS,
			maxRegression,
		)
	}
	failures = appendRegression(
		failures,
		name,
		"peak_rss_bytes",
		float64(basePhase.PeakRSSBytes),
		float64(currentPhase.PeakRSSBytes),
		maxRegression,
	)
	if basePhase.FPS > 0 && currentPhase.FPS < basePhase.FPS*(1-maxRegression) {
		failures = append(failures, fmt.Sprintf(
			"%s fps 退化 %.1f%%：基线=%.3f 当前=%.3f",
			name,
			(1-currentPhase.FPS/basePhase.FPS)*100,
			basePhase.FPS,
			currentPhase.FPS,
		))
	}
}
return failures, nil
```

Change the success text to:

```go
return "同场景性能比较通过：适用的稳定指标退化均未超过阈值且绝对门禁通过"
```

- [ ] **Step 5: Run GREEN, race, and mutation checks**

```bash
TERM=xterm-256color zsh -ic 'gvm use go1.26.0 >/dev/null && gofmt -w cmd/perfcheck/main.go cmd/perfcheck/main_test.go && go test ./cmd/perfcheck -count=1'
TERM=xterm-256color zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/perfcheck -race -count=1'
git diff --check -- cmd/perfcheck/main.go cmd/perfcheck/main_test.go
```

Expected: all exit 0. Then temporarily force `crossTransportV6 := false` and verify the cross-transport ignore test fails; restore with `apply_patch`. Temporarily add jobs high-water back to relative comparison and verify the same test fails; restore and rerun `go test ./cmd/perfcheck -count=1`.

- [ ] **Step 6: Detect scope and commit**

Invoke `gitnexus_detect_changes({scope:"unstaged",repo:"minecraft-go"})`, review every affected symbol/process, then:

```bash
git add cmd/perfcheck/main.go cmd/perfcheck/main_test.go
git diff --cached --check
git commit -m "fix: 区分 M3C 性能比较模式"
```

Expected: commit contains only the two perfcheck files.

---

### Task 3: Build the deterministic server-probe epoch with TDD

**Files:**
- Create: `cmd/mcgo/multiplayer_probe_epoch.go`
- Create: `cmd/mcgo/multiplayer_probe_epoch_test.go`

- [ ] **Step 1: Write the failing epoch contract tests**

Create `cmd/mcgo/multiplayer_probe_epoch_test.go`:

```go
//go:build darwin

package main

import (
	"testing"
	"time"
)

func TestBenchmarkServerEpochIgnoresWarmupAndStopsAtExactWindow(t *testing.T) {
	epoch := newBenchmarkServerEpoch()
	epoch.beginWarmup()
	for range benchmarkServerWarmupTicks {
		for range 8 {
			epoch.observeInterest(9 * time.Millisecond)
		}
		epoch.observeTick(9 * time.Millisecond)
		if signal := <-epoch.signals; signal.measured {
			t.Fatal("warm-up tick marked measured")
		}
	}
	if err := epoch.beginMeasurement(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	for tick := 1; tick <= benchmarkServerMeasuredTicks; tick++ {
		for range 8 {
			epoch.observeInterest(time.Duration(tick) * time.Microsecond)
		}
		epoch.observeTick(time.Duration(tick) * time.Microsecond)
		if signal := <-epoch.signals; !signal.measured {
			t.Fatalf("tick %d not marked measured", tick)
		}
	}

	epoch.observeInterest(time.Second)
	epoch.observeTick(time.Second)
	if got := epoch.ticks.Summary().Samples; got != benchmarkServerMeasuredTicks {
		t.Fatalf("tick samples=%d want=%d", got, benchmarkServerMeasuredTicks)
	}
	if got := epoch.interest.Summary().Samples; got != benchmarkServerInterestSamples {
		t.Fatalf("interest samples=%d want=%d", got, benchmarkServerInterestSamples)
	}
	select {
	case signal := <-epoch.signals:
		t.Fatalf("done epoch emitted signal: %+v", signal)
	default:
	}
	if epoch.overflow.Load() {
		t.Fatal("complete epoch reported overflow")
	}
}

func TestBenchmarkServerEpochDropsStaleWarmupSignalsBeforeMeasurement(t *testing.T) {
	epoch := newBenchmarkServerEpoch()
	epoch.beginWarmup()
	epoch.observeTick(time.Millisecond)
	if err := epoch.beginMeasurement(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	epoch.observeTick(2 * time.Millisecond)
	if signal := <-epoch.signals; !signal.measured {
		t.Fatalf("stale warm-up signal survived reset: %+v", signal)
	}
	if got := epoch.ticks.Summary().Samples; got != 1 {
		t.Fatalf("measured samples=%d want=1", got)
	}
}

func TestBenchmarkServerEpochReportsSignalOverflowWithoutBlocking(t *testing.T) {
	epoch := newBenchmarkServerEpoch()
	epoch.beginWarmup()
	for range cap(epoch.signals) + 1 {
		epoch.observeTick(time.Microsecond)
	}
	if !epoch.overflow.Load() {
		t.Fatal("signal overflow not reported")
	}
}

func TestBenchmarkServerEpochArmsInputBeforeMeasurementGate(t *testing.T) {
	epoch := newBenchmarkServerEpoch()
	epoch.beginWarmup()
	armed := false
	err := epoch.beginMeasurement(func() error {
		if epoch.measuring() {
			t.Fatal("measurement gate opened before input arm")
		}
		epoch.observeInterest(time.Second)
		epoch.observeTick(time.Second)
		select {
		case signal := <-epoch.signals:
			t.Fatalf("idle arm recorded a tick: %+v", signal)
		default:
		}
		armed = true
		return nil
	})
	if err != nil || !armed || !epoch.measuring() {
		t.Fatalf("beginMeasurement err=%v armed=%v measuring=%v", err, armed, epoch.measuring())
	}
	epoch.observeTick(time.Millisecond)
	if signal := <-epoch.signals; !signal.measured {
		t.Fatalf("first post-arm tick not measured: %+v", signal)
	}
	if got := epoch.ticks.Summary().Samples; got != 1 {
		t.Fatalf("post-arm samples=%d want=1", got)
	}
}
```

- [ ] **Step 2: Run the test and verify RED**

```bash
TERM=xterm-256color zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo -run "^TestBenchmarkServerEpoch" -count=1'
```

Expected: build failure because the epoch symbols are undefined.

- [ ] **Step 3: Implement the epoch**

Create `cmd/mcgo/multiplayer_probe_epoch.go`:

```go
//go:build darwin

package main

import (
	"sync/atomic"
	"time"

	"minecraft-go/internal/client"
)

const (
	benchmarkServerWarmupTicks     = 20
	benchmarkServerMeasuredTicks   = 200
	benchmarkServerInterestSamples = 8 * benchmarkServerMeasuredTicks
	benchmarkServerSignalCapacity  = benchmarkServerWarmupTicks + benchmarkServerMeasuredTicks + 16
)

type benchmarkServerEpochPhase uint32

const (
	benchmarkServerEpochIdle benchmarkServerEpochPhase = iota
	benchmarkServerEpochWarmup
	benchmarkServerEpochMeasuring
	benchmarkServerEpochDone
)

type benchmarkServerTickSignal struct {
	measured bool
}

type benchmarkServerEpoch struct {
	phase         atomic.Uint32
	measuredTicks atomic.Int64
	overflow      atomic.Bool
	signals       chan benchmarkServerTickSignal
	ticks         *client.LatencyRecorder
	interest      *client.LatencyRecorder
}

func newBenchmarkServerEpoch() *benchmarkServerEpoch {
	return &benchmarkServerEpoch{
		signals:  make(chan benchmarkServerTickSignal, benchmarkServerSignalCapacity),
		ticks:    client.NewLatencyRecorder(512),
		interest: client.NewLatencyRecorder(4096),
	}
}

func (epoch *benchmarkServerEpoch) beginWarmup() {
	epoch.drainSignals()
	epoch.overflow.Store(false)
	epoch.phase.Store(uint32(benchmarkServerEpochWarmup))
}

func (epoch *benchmarkServerEpoch) beginMeasurement(armInput func() error) error {
	epoch.phase.Store(uint32(benchmarkServerEpochIdle))
	epoch.drainSignals()
	epoch.ticks.Reset()
	epoch.interest.Reset()
	epoch.measuredTicks.Store(0)
	epoch.overflow.Store(false)
	if armInput != nil {
		if err := armInput(); err != nil {
			epoch.phase.Store(uint32(benchmarkServerEpochDone))
			return err
		}
	}
	epoch.phase.Store(uint32(benchmarkServerEpochMeasuring))
	return nil
}

func (epoch *benchmarkServerEpoch) measuring() bool {
	return benchmarkServerEpochPhase(epoch.phase.Load()) == benchmarkServerEpochMeasuring
}

func (epoch *benchmarkServerEpoch) observeInterest(duration time.Duration) {
	if epoch.measuring() {
		epoch.interest.Add(duration)
	}
}

func (epoch *benchmarkServerEpoch) observeTick(duration time.Duration) {
	phase := benchmarkServerEpochPhase(epoch.phase.Load())
	if phase != benchmarkServerEpochWarmup && phase != benchmarkServerEpochMeasuring {
		return
	}
	measured := phase == benchmarkServerEpochMeasuring
	if measured {
		epoch.ticks.Add(duration)
		if epoch.measuredTicks.Add(1) == benchmarkServerMeasuredTicks {
			epoch.phase.CompareAndSwap(
				uint32(benchmarkServerEpochMeasuring),
				uint32(benchmarkServerEpochDone),
			)
		}
	}
	select {
	case epoch.signals <- benchmarkServerTickSignal{measured: measured}:
	default:
		epoch.overflow.Store(true)
	}
}

func (epoch *benchmarkServerEpoch) drainSignals() {
	for {
		select {
		case <-epoch.signals:
		default:
			return
		}
	}
}
```

- [ ] **Step 4: Run GREEN, race, formatting, and mutation checks**

```bash
TERM=xterm-256color zsh -ic 'gvm use go1.26.0 >/dev/null && gofmt -w cmd/mcgo/multiplayer_probe_epoch.go cmd/mcgo/multiplayer_probe_epoch_test.go && go test ./cmd/mcgo -run "^TestBenchmarkServerEpoch" -count=1'
TERM=xterm-256color zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo -run "^TestBenchmarkServerEpoch" -race -count=1'
git diff --check -- cmd/mcgo/multiplayer_probe_epoch.go cmd/mcgo/multiplayer_probe_epoch_test.go
```

Expected: exit 0 without sleeps. Temporarily open the measuring gate before `armInput` and verify `TestBenchmarkServerEpochArmsInputBeforeMeasurementGate` fails; restore. Temporarily make `observeInterest` unconditional and verify the exact-window test fails above 1600 samples; restore. Temporarily remove `drainSignals` from `beginMeasurement` and verify the stale-signal test fails; restore and rerun.

- [ ] **Step 5: Detect scope and commit the epoch unit**

Invoke `gitnexus_detect_changes({scope:"unstaged",repo:"minecraft-go"})`, review the affected symbols/processes, then:

```bash
git add cmd/mcgo/multiplayer_probe_epoch.go cmd/mcgo/multiplayer_probe_epoch_test.go
git diff --cached --check
git commit -m "test: 固定多人性能探针测量 epoch"
```

Expected: commit contains only the epoch unit and its tests.

---

### Task 4: Integrate the epoch into the real eight-session probe

**Files:**
- Modify: `cmd/mcgo/multiplayer_benchmark.go:256-498`
- Modify: `cmd/mcgo/benchmark_v6_test.go:128-139`

- [ ] **Step 1: Strengthen the real integration assertion**

Add `"context"` and `"minecraft-go/internal/server"` to the existing test imports, then add this fast contract before the real integration test:

```go
func TestBenchmarkServerMeasuredWindowSendsOneSequencePerCompletedTick(t *testing.T) {
	epoch := newBenchmarkServerEpoch()
	testCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	sent := make(chan uint64, 1)
	var sequences []uint64
	var events []string
	var statsCalls, rssCalls int
	sendInputs := func(_ context.Context, sequence uint64) error {
		events = append(events, "send")
		sequences = append(sequences, sequence)
		sent <- sequence
		return nil
	}
	if err := epoch.beginMeasurement(func() error {
		return sendInputs(testCtx, 1)
	}); err != nil {
		t.Fatal(err)
	}
	publisherDone := make(chan struct{})
	go func() {
		defer close(publisherDone)
		for range benchmarkServerMeasuredTicks {
			select {
			case <-sent:
				epoch.signals <- benchmarkServerTickSignal{measured: true}
			case <-testCtx.Done():
				return
			}
		}
	}()
	summary, err := runBenchmarkServerMeasuredWindow(
		testCtx,
		epoch,
		8,
		sendInputs,
		func() server.HostStats {
			events = append(events, "tick")
			statsCalls++
			return server.HostStats{
				ActivePlayers: 8, MaxSessionOutboxDepth: 5,
				PlayerSaveJobDepth: 6, PlayerSaveDoneDepth: 1,
			}
		},
		func() (uint64, error) {
			rssCalls++
			return 123, nil
		},
	)
	<-publisherDone
	if err != nil {
		t.Fatal(err)
	}
	if len(sequences) != benchmarkServerMeasuredTicks {
		t.Fatalf("input sequences=%d want=%d", len(sequences), benchmarkServerMeasuredTicks)
	}
	for index, sequence := range sequences {
		if want := uint64(index + 1); sequence != want {
			t.Fatalf("sequence[%d]=%d want=%d", index, sequence, want)
		}
	}
	if len(events) != 2*benchmarkServerMeasuredTicks {
		t.Fatalf("events=%d want=%d", len(events), 2*benchmarkServerMeasuredTicks)
	}
	for index, event := range events {
		want := "send"
		if index%2 == 1 {
			want = "tick"
		}
		if event != want {
			t.Fatalf("event[%d]=%q want=%q; inputs are not tick-driven", index, event, want)
		}
	}
	if statsCalls != benchmarkServerMeasuredTicks || rssCalls != 10 {
		t.Fatalf("stats/rss calls=%d/%d want=%d/10", statsCalls, rssCalls, benchmarkServerMeasuredTicks)
	}
	if summary != (benchmarkServerWindowSummary{
		outboxHigh: 5, jobsHigh: 6, doneHigh: 1, peakRSS: 123,
	}) {
		t.Fatalf("summary=%+v", summary)
	}
}
```

Replace the loose sample checks with:

```go
if multiplayer.ServerOutboundBytes == 0 ||
	multiplayer.InterestDiff.Samples != benchmarkServerInterestSamples ||
	ticks.Frames != benchmarkServerMeasuredTicks ||
	multiplayer.OutboxHighWater > benchmarkOutboxLimit ||
	multiplayer.PlayerJobsHighWater > 16 || multiplayer.PlayerDoneHighWater > 2 ||
	multiplayer.PeakRSSBytes == 0 || multiplayer.PeakRSSBytes >= 2<<30 {
	t.Fatalf("incomplete bounded server probe: multiplayer=%+v ticks=%+v", multiplayer, ticks)
}
```

- [ ] **Step 2: Run the fast controller test and verify RED**

```bash
TERM=xterm-256color zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo -run "^TestBenchmarkServerMeasuredWindow" -count=1'
```

Expected: build failure because `runBenchmarkServerMeasuredWindow` and `benchmarkServerWindowSummary` do not exist.

- [ ] **Step 3: Gate canonical outbound counting**

Extend `canonicalCountingServerStream`:

```go
type canonicalCountingServerStream struct {
	inner network.ServerPacketStream
	codec *network.Codec
	bytes *atomic.Uint64
	epoch *benchmarkServerEpoch
	once  sync.Once
}
```

After successful `inner.Send`:

```go
if stream.epoch == nil || stream.epoch.measuring() {
	stream.bytes.Add(uint64(logicalBytes))
}
```

- [ ] **Step 4: Wire observers, warm-up, and exact measured loop**

Use:

```go
epoch := newBenchmarkServerEpoch()
config := server.DefaultConfig(benchmarkSeed)
config.MaxPlayers = 8
config.ViewRadius = 0
config.Workers = 1
config.SaveWorkers = 2
config.OutboxCapacity = benchmarkOutboxLimit
config.AutosaveTicks = 20
config.HeartbeatInterval = time.Hour
config.HeartbeatTimeout = time.Hour
config.TickObserver = epoch.observeTick
config.InterestObserver = epoch.observeInterest
```

Replace the stream construction with:

```go
counting := &canonicalCountingServerStream{
	inner: serverStream, codec: codec, bytes: &outbound, epoch: epoch,
}
```

Include warm-up in the outer fault timeout:

```go
runCtx, cancelRun := context.WithTimeout(
	context.Background(),
	duration+benchmarkServerWarmupTicks*50*time.Millisecond+15*time.Second,
)
```

Extract the current input body into:

```go
func sendMultiplayerBenchmarkInputs(
	ctx context.Context,
	clients []multiplayerServerClient,
	sequence uint64,
) error {
	for index, connected := range clients {
		sendCtx, cancelSend := context.WithTimeout(ctx, time.Second)
		err := connected.endpoint.Send(sendCtx, network.PlayerInput{
			Sequence: sequence,
			MoveX:    int8((index+int(sequence))%3 - 1),
			MoveZ:    int8((index*2+int(sequence))%3 - 1),
			Jump:     sequence%40 == uint64(index),
			Yaw:      float32(sequence%360) * math.Pi / 180,
			Pitch:    float32(index-3) * 0.03,
		})
		cancelSend()
		if err != nil {
			return fmt.Errorf("发送固定脚本 player %d tick %d: %w", index, sequence, err)
		}
	}
	return nil
}
```

After all logins:

```go
loginReadyCtx, cancelLoginReady := context.WithTimeout(runCtx, 5*time.Second)
loginPoll := time.NewTicker(10 * time.Millisecond)
for {
	stats := host.Stats()
	if stats.ActivePlayers == len(identities) {
		break
	}
	if stats.ActivePlayers > len(identities) {
		loginPoll.Stop()
		cancelLoginReady()
		return client.MultiplayerSummary{}, client.PhaseSummary{}, fmt.Errorf(
			"多人服务端登录数越界: active=%d want=%d",
			stats.ActivePlayers, len(identities),
		)
	}
	select {
	case <-loginPoll.C:
	case <-loginReadyCtx.Done():
		loginPoll.Stop()
		err := loginReadyCtx.Err()
		cancelLoginReady()
		return client.MultiplayerSummary{}, client.PhaseSummary{}, fmt.Errorf(
			"等待多人服务端登录稳定: active=%d want=%d: %w",
			stats.ActivePlayers, len(identities), err,
		)
	}
}
loginPoll.Stop()
cancelLoginReady()
epoch.beginWarmup()
for tick := 0; tick < benchmarkServerWarmupTicks; tick++ {
	select {
	case signal := <-epoch.signals:
		if signal.measured {
			return client.MultiplayerSummary{}, client.PhaseSummary{},
				fmt.Errorf("warm-up tick %d 被标记为 measured", tick+1)
		}
		if stats := host.Stats(); stats.ActivePlayers != len(identities) {
			return client.MultiplayerSummary{}, client.PhaseSummary{}, fmt.Errorf(
				"多人服务端 warm-up tick %d 玩家提前退出: active=%d want=%d",
				tick+1, stats.ActivePlayers, len(identities),
			)
		}
	case <-runCtx.Done():
		return client.MultiplayerSummary{}, client.PhaseSummary{}, runCtx.Err()
	}
}
if stats := host.Stats(); stats.ActivePlayers != len(identities) {
	return client.MultiplayerSummary{}, client.PhaseSummary{}, fmt.Errorf(
		"多人服务端 warm-up 后登录不完整: active=%d want=%d",
		stats.ActivePlayers, len(identities),
	)
}
sendInputs := func(ctx context.Context, sequence uint64) error {
	return sendMultiplayerBenchmarkInputs(ctx, clients, sequence)
}
if err := epoch.beginMeasurement(func() error {
	return sendInputs(runCtx, 1)
}); err != nil {
	return client.MultiplayerSummary{}, client.PhaseSummary{}, err
}
```

Add the measured-window controller:

```go
type benchmarkServerWindowSummary struct {
	outboxHigh int
	jobsHigh   int
	doneHigh   int
	peakRSS    uint64
}

func runBenchmarkServerMeasuredWindow(
	ctx context.Context,
	epoch *benchmarkServerEpoch,
	wantPlayers int,
	sendInputs func(context.Context, uint64) error,
	readStats func() server.HostStats,
	readRSS func() (uint64, error),
) (benchmarkServerWindowSummary, error) {
	var result benchmarkServerWindowSummary
	for completed := 1; completed <= benchmarkServerMeasuredTicks; completed++ {
		select {
		case signal := <-epoch.signals:
			if !signal.measured {
				return result, fmt.Errorf(
					"measured tick %d 收到 warm-up signal", completed,
				)
			}
		case <-ctx.Done():
			return result, ctx.Err()
		}
		stats := readStats()
		if stats.ActivePlayers != wantPlayers {
			return result, fmt.Errorf(
				"多人服务端 measured tick %d 玩家提前退出: active=%d want=%d",
				completed, stats.ActivePlayers, wantPlayers,
			)
		}
		result.outboxHigh = max(result.outboxHigh, stats.MaxSessionOutboxDepth)
		result.jobsHigh = max(result.jobsHigh, stats.PlayerSaveJobDepth)
		result.doneHigh = max(result.doneHigh, stats.PlayerSaveDoneDepth)
		if completed%20 == 0 {
			rss, err := readRSS()
			if err != nil {
				return result, err
			}
			result.peakRSS = max(result.peakRSS, rss)
		}
		if completed < benchmarkServerMeasuredTicks {
			if err := sendInputs(ctx, uint64(completed+1)); err != nil {
				return result, err
			}
		}
	}
	return result, nil
}
```

Replace the deadline/ticker loop with this call, then convert the epoch recorders without changing the report schema:

```go
window, err := runBenchmarkServerMeasuredWindow(
	runCtx,
	epoch,
	len(clients),
	sendInputs,
	host.Stats,
	client.ProcessRSSBytes,
)
if err != nil {
	return client.MultiplayerSummary{}, client.PhaseSummary{}, err
}
outboxHigh, jobsHigh, doneHigh, peakRSS :=
	window.outboxHigh, window.jobsHigh, window.doneHigh, window.peakRSS
interestSummary := epoch.interest.Summary()
tickLatency := epoch.ticks.Summary()
tickSummary := client.PhaseSummary{
	Frames: tickLatency.Samples,
	P50MS:  tickLatency.P50MS,
	P95MS:  tickLatency.P95MS,
	P99MS:  tickLatency.P99MS,
	MaxMS:  tickLatency.MaxMS,
}
if epoch.overflow.Load() || outbound.Load() == 0 ||
	interestSummary.Samples != benchmarkServerInterestSamples ||
	tickSummary.Frames != benchmarkServerMeasuredTicks ||
	outboxHigh > benchmarkOutboxLimit || jobsHigh > 16 || doneHigh > 2 ||
	peakRSS == 0 || peakRSS >= 2<<30 {
	return client.MultiplayerSummary{}, client.PhaseSummary{}, fmt.Errorf(
		"多人服务端探针不完整: overflow=%v outbound=%d interest=%+v ticks=%+v queues=%d/%d/%d rss=%d",
		epoch.overflow.Load(), outbound.Load(), interestSummary, tickSummary,
		outboxHigh, jobsHigh, doneHigh, peakRSS,
	)
}
if err := cleanup(); err != nil {
	return client.MultiplayerSummary{}, client.PhaseSummary{}, err
}
cleaned = true
if stats := host.Stats(); stats != (server.HostStats{}) {
	return client.MultiplayerSummary{}, client.PhaseSummary{}, fmt.Errorf(
		"多人服务端 cleanup 队列未归零: %+v", stats,
	)
}
return client.MultiplayerSummary{
	InterestDiff:        interestSummary,
	ServerOutboundBytes: outbound.Load(),
	OutboxHighWater:     outboxHigh,
	PlayerJobsHighWater: jobsHigh,
	PlayerDoneHighWater: doneHigh,
	PeakRSSBytes:        peakRSS,
}, tickSummary, nil
```

This replaces the old loose `<1000/<100` checks and the old summary/return block while preserving the existing bounded cleanup path.

- [ ] **Step 5: Run focused, real integration, race, and mutation checks**

```bash
TERM=xterm-256color zsh -ic 'gvm use go1.26.0 >/dev/null && gofmt -w cmd/mcgo/multiplayer_benchmark.go cmd/mcgo/benchmark_v6_test.go && go test ./cmd/mcgo -run "^TestBenchmarkServer(Epoch|MeasuredWindow)" -count=1'
TERM=xterm-256color zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo -run "^TestScenarioV6EightSessionServerProbeIsRealAndBounded$" -count=1'
TERM=xterm-256color zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo -run "Test(BenchmarkServer(Epoch|MeasuredWindow)|ScenarioV6EightSessionServerProbeIsRealAndBounded)" -race -count=1'
git diff --check -- cmd/mcgo/multiplayer_benchmark.go cmd/mcgo/benchmark_v6_test.go
```

Expected: exact `200/1600`, cleanup zero, race clean. Mutation: remove the `sendInputs(ctx, uint64(completed+1))` call and verify `TestBenchmarkServerMeasuredWindowSendsOneSequencePerCompletedTick` fails; make interest recording unconditional and verify the fast exact-window test fails; remove stale-signal drain and verify its unit test fails. Also temporarily expect `benchmarkServerMeasuredTicks+1` in the real integration assertion and verify the 10-second test fails; this is not a formal report run and writes no JSON. Restore every mutation with `apply_patch` and rerun the focused tests.

- [ ] **Step 6: Detect scope and commit**

Invoke `gitnexus_detect_changes({scope:"unstaged",repo:"minecraft-go"})`, review the affected symbols/processes, then:

```bash
git add cmd/mcgo/multiplayer_benchmark.go cmd/mcgo/benchmark_v6_test.go
git diff --cached --check
git commit -m "perf: 同步 M3C 多人探针采样窗口"
```

Expected: commit succeeds; epoch files were committed in Task 3.

---

### Task 5: Align focused gates and controlling documents; request review

**Files:**
- Modify: `Makefile:8-40`
- Modify: `.github/workflows/ci.yml:20-30`
- Modify: `docs/superpowers/plans/2026-08-01-m3c-multiplayer-sync.md:1629-1708`
- Modify: `docs/superpowers/plans/2026-08-02-m3c-scenario-upgrade-policy.md`
- Modify locally: `.superpowers/sdd/2026-08-01-m3c-multiplayer-sync/task-17-brief.md`
- Modify locally: `.superpowers/sdd/2026-08-01-m3c-multiplayer-sync/task-17-report.md`
- Modify locally: `.superpowers/sdd/2026-08-01-m3c-multiplayer-sync/progress.md`

- [ ] **Step 1: Extend focused test selectors**

Make Makefile and CI execute:

```bash
go test ./internal/client ./internal/server ./cmd/mcgo ./cmd/perfcheck \
  -run 'Test(PerfReportV6|ScenarioV6|PerfcheckV6|PerfcheckV5SameScenario|PerformanceThresholds|InterestObserver|HostStats|BenchmarkServerEpoch|BenchmarkServerMeasuredWindow)' \
  -count=1
```

- [ ] **Step 2: Apply the approved policy text**

Use `apply_patch` so every controlling document states:

```text
v6 Memory→TCP：比较 transport 相关稳定 p50/p95/p99、FPS、RSS、load/snapshot、protocol 与 persistence；raw max、queue high-water 和独立内存 server probe 不做跨 transport 相对比较。
v6 同 transport：额外比较 server tick/interest p50/p95/p99、outbound 与 multiplayer RSS；raw max 和 queue high-water 仍只执行既有绝对门禁。
server probe：8 登录完成后 warm-up 20 ticks，再由 TickObserver 信号驱动 200 measured ticks/1600 interest samples，不再使用第二个 50 ms ticker。
```

Record the rejected formal artifact paths/hashes and the still-frozen v5 baseline.

- [ ] **Step 3: Run focused and documentation gates**

```bash
TERM=xterm-256color zsh -ic 'gvm use go1.26.0 >/dev/null && make test-multiplayer'
TERM=xterm-256color zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/perfcheck ./cmd/mcgo -race -count=1'
git diff --check
rg -n '200 measured|1600|跨 transport|同 transport|raw max|queue high-water' \
  docs/superpowers/plans/2026-08-01-m3c-multiplayer-sync.md \
  docs/superpowers/plans/2026-08-02-m3c-scenario-upgrade-policy.md \
  .superpowers/sdd/2026-08-01-m3c-multiplayer-sync/task-17-brief.md
```

Expected: tests/race/diff exit 0 and policy appears in all three documents.

- [ ] **Step 4: Dispatch independent reviewers**

Dispatch:

```text
Spec reviewer: Compare efcee05..HEAD with docs/superpowers/specs/2026-08-03-m3c-performance-repeatability-design.md. Report Critical/Important/Minor findings only; do not edit.
Code reviewer: Review profile logic, epoch concurrency, signal overflow, exact 200/1600 accounting, cleanup, legacy v5 behavior, and tests. Cite file/line evidence; do not edit.
```

Expected: no Critical/Important findings. Fix any accepted blocker via a new RED test, rerun focused/race gates, and request follow-up review.

- [ ] **Step 5: Commit policy and focused-gate changes**

Invoke `gitnexus_detect_changes({scope:"unstaged",repo:"minecraft-go"})`, review the affected symbols/processes, then:

```bash
git add \
  Makefile \
  .github/workflows/ci.yml \
  docs/superpowers/plans/2026-08-01-m3c-multiplayer-sync.md \
  docs/superpowers/plans/2026-08-02-m3c-scenario-upgrade-policy.md
git diff --cached --check
git commit -m "docs: 明确 M3C 稳定性能比较门禁"
```

Expected: local `.superpowers/sdd` evidence stays uncommitted unless already tracked.

---

### Task 6: Run a fresh Step 5 gate and create the implementation checkpoint

**Files:**
- Verify: all Task 17 executable source and tests
- Preserve unchanged: `docs/notes/perf-baseline.json`
- Preserve unchanged: `docs/notes/perf-baseline.md`

- [ ] **Step 1: Format and run the complete test suite**

```bash
TERM=xterm-256color zsh -ic 'gvm use go1.26.0 >/dev/null && files=("${(@f)$(git ls-files -co --exclude-standard "*.go")}"); if (( ${#files} > 0 )); then gofmt -w -- $files; fi'
TERM=xterm-256color zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -count=1'
git diff --check
```

Expected: every command exits 0. If `gofmt` changes an unexpected file, inspect and correct scope before proceeding.

- [ ] **Step 2: Run race, fuzz, vet, architecture, and Linux build gates**

```bash
TERM=xterm-256color zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network ./internal/server ./internal/client ./internal/render ./cmd/mcgo ./cmd/perfcheck -race -count=1'
TERM=xterm-256color zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "^$" -fuzz "^FuzzSmallPacketCodec$" -fuzztime=10s'
TERM=xterm-256color zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "^$" -fuzz "^FuzzReadFrame$" -fuzztime=10s'
TERM=xterm-256color zsh -ic 'gvm use go1.26.0 >/dev/null && go vet ./...'
TERM=xterm-256color zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
test ! -e /tmp/mcgod-linux-m3c-repeatability || exit 125
TERM=xterm-256color zsh -ic 'gvm use go1.26.0 >/dev/null && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /tmp/mcgod-linux-m3c-repeatability ./cmd/mcgod'
```

Expected: all gates pass. A pre-existing Linux output path is a hard stop; never overwrite it.

- [ ] **Step 3: Recreate the physics evidence**

```bash
test ! -e /tmp/mcgo-m3c-physics-repeatability.txt || exit 125
set -o pipefail
TERM=xterm-256color zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/physics -run "^$" -bench "^BenchmarkStepPlayer" -benchmem -count=1' | tee /tmp/mcgo-m3c-physics-repeatability.txt
test "$(rg -c '^BenchmarkStepPlayer.*[[:space:]]0 B/op[[:space:]]+0 allocs/op$' /tmp/mcgo-m3c-physics-repeatability.txt)" -eq 3
shasum -a 256 /tmp/mcgo-m3c-physics-repeatability.txt
```

Expected: exactly three benchmark rows and a recorded SHA-256. If the destination already exists, stop and choose a new explicit evidence path before executing the benchmark.

- [ ] **Step 4: Reconfirm the frozen baseline and process state**

```bash
git diff --exit-code -- docs/notes/perf-baseline.json docs/notes/perf-baseline.md
jq -e '.scenario_version == 5' docs/notes/perf-baseline.json
shasum -a 256 docs/notes/perf-baseline.json docs/notes/perf-baseline.md
! pgrep -f '(^|/)(mcgo|mcgod)( |$)|go run ./cmd/(mcgo|mcgod)'
```

Expected: baseline remains v5 with the previously recorded hashes, and no benchmark process is running.

- [ ] **Step 5: Detect scope, stage the remaining Task 17 implementation, and commit it**

Invoke `gitnexus_detect_changes({scope:"unstaged",repo:"minecraft-go"})` first. Review every reported symbol and flow against the approved design. Then stage only the remaining Task 17 files:

```bash
git add \
  .gitignore \
  README.md \
  docs/notes/lan-server.md \
  cmd/mcgo/app.go \
  cmd/mcgo/app_test.go \
  cmd/mcgo/benchmark.go \
  cmd/mcgo/benchmark_v5_test.go \
  cmd/mcgo/multiplayer_capacity_test.go \
  cmd/mcgo/presentation_conversion_test.go \
  internal/archcheck/deps_test.go \
  internal/client/perf.go \
  internal/client/perf_test.go \
  internal/client/remote_players.go \
  internal/client/presentation_allocation_test.go \
  internal/gfx/gfx.go \
  internal/gfx/wgpu.go \
  internal/gfx/bind_group_range_test.go \
  internal/network/benchmark_test.go \
  internal/render/avatar.go \
  internal/render/avatar_test.go \
  internal/render/font_atlas.go \
  internal/render/name_tag.go \
  internal/render/name_tag_test.go \
  internal/render/dynamic_upload_test.go \
  internal/render/hot_path_allocation_test.go \
  internal/render/multiplayer_bench_test.go \
  internal/server/config.go \
  internal/server/host.go \
  internal/server/publication.go \
  internal/server/host_stats_test.go \
  internal/server/multiplayer_bench_test.go \
  docs/superpowers/plans/2026-08-02-mesher-bounded-scheduling.md
git diff --cached --check
test -z "$(git diff --cached --name-only | rg '^docs/notes/perf-baseline\.(json|md)$' || true)"
git diff --cached --stat
git commit -m "feat: 完成 M3C scenario v6 验收实现"
```

Expected: all executable Task 17 work is now represented by commits. Do not stage `AGENTS.md`, `CLAUDE.md`, `.claude/`, local `.superpowers/sdd` state, rejected `/tmp` artifacts, or either frozen baseline file in this commit.

- [ ] **Step 6: Record the checkpoint and request the one-shot authorization**

```bash
git status --short
test -z "$(git status --porcelain --untracked-files=no)"
git log --oneline --decorate -8
git rev-parse HEAD
git diff --exit-code -- docs/notes/perf-baseline.json docs/notes/perf-baseline.md
! pgrep -f '(^|/)(mcgo|mcgod)( |$)|go run ./cmd/(mcgo|mcgod)'
```

Report the Step 5 evidence, reviewer status, exact implementation commit, clean tracked-state result, frozen baseline hashes, and absence of benchmark processes. Then stop and ask for explicit authorization to execute the single formal Step 6 run. A generic “continue” from before this checkpoint is not authorization.

---

### Task 7: Execute the one-shot formal Step 6 and promote the v6 baseline

**Files:**
- Modify: `docs/notes/perf-baseline.json`
- Modify: `docs/notes/perf-baseline.md`
- Modify locally: `.superpowers/sdd/2026-08-01-m3c-multiplayer-sync/task-17-report.md`
- Modify locally: `.superpowers/sdd/2026-08-01-m3c-multiplayer-sync/progress.md`
- Create evidence: collision-safe `/tmp/mcgo-m3c-*` paths

> Preconditions: Task 6 is fully green, tracked state is clean, the baseline is still v5, no benchmark process is running, and the user has explicitly authorized this exact one-shot execution. Never retry a failed formal command in this task.

- [ ] **Step 1: Resolve collision-safe evidence paths and run the final preflight**

```bash
mcgo_m3c_commit="$(git rev-parse --short=12 HEAD)"
mcgo_m3c_memory="/tmp/mcgo-m3c-memory-${mcgo_m3c_commit}.json"
mcgo_m3c_memory_log="/tmp/mcgo-m3c-memory-${mcgo_m3c_commit}.log"
mcgo_m3c_tcp="/tmp/mcgo-m3c-tcp-${mcgo_m3c_commit}.json"
mcgo_m3c_tcp_log="/tmp/mcgo-m3c-tcp-${mcgo_m3c_commit}.log"
mcgo_m3c_compare="/tmp/mcgo-m3c-compare-${mcgo_m3c_commit}.log"
mcgo_m3c_migration="/tmp/mcgo-m3c-migration-${mcgo_m3c_commit}.log"
mcgo_m3c_micro="/tmp/mcgo-m3c-micro-${mcgo_m3c_commit}.txt"
mcgo_m3c_v5_backup="/tmp/mcgo-m3c-baseline-v5-${mcgo_m3c_commit}.json"
test -z "$(git status --porcelain --untracked-files=no)"
jq -e '.scenario_version == 5' docs/notes/perf-baseline.json
test "$(shasum -a 256 docs/notes/perf-baseline.json | awk '{print $1}')" = \
  428e9b61bd8a8bf782fdc4e8d54f488d544bee4b7da948873638fe40ba60a191
test "$(shasum -a 256 docs/notes/perf-baseline.md | awk '{print $1}')" = \
  ac4dfffd78fc3a31d56b3cf728651e805535b60d302d2c2f55273d23f79ecfdb
! pgrep -f '(^|/)(mcgo|mcgod)( |$)|go run ./cmd/(mcgo|mcgod)'
for mcgo_m3c_path in \
  "$mcgo_m3c_memory" "$mcgo_m3c_memory_log" \
  "$mcgo_m3c_tcp" "$mcgo_m3c_tcp_log" \
  "$mcgo_m3c_compare" "$mcgo_m3c_migration" "$mcgo_m3c_micro" \
  "$mcgo_m3c_v5_backup"; do
  test ! -e "$mcgo_m3c_path" || exit 125
done
```

Expected: preflight exits 0 and the accepted v5 baseline remains untouched and unbacked-up. If any check fails, stop without launching a benchmark.

- [ ] **Step 2: Run the Memory report exactly once**

```bash
mcgo_m3c_commit="$(git rev-parse --short=12 HEAD)"
mcgo_m3c_full_commit="$(git rev-parse HEAD)"
mcgo_m3c_memory="/tmp/mcgo-m3c-memory-${mcgo_m3c_commit}.json"
mcgo_m3c_memory_log="/tmp/mcgo-m3c-memory-${mcgo_m3c_commit}.log"
set -o pipefail
TERM=xterm-256color zsh -ic "gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport memory --perf-output '$mcgo_m3c_memory'" | tee "$mcgo_m3c_memory_log"
jq -e --arg commit "$mcgo_m3c_full_commit" \
  '.scenario_version == 6 and .transport == "memory" and .git_commit == $commit and
   ((.hardware | length) > 0) and ((.os | length) > 0) and
   ((.go_version | length) > 0) and ((.framebuffer | length) > 0) and
   .ticks.frames == 200 and .ticks.p50_ms > 0 and
   .ticks.p50_ms <= .ticks.p95_ms and .ticks.p95_ms <= .ticks.p99_ms and
   .ticks.p99_ms <= .ticks.max_ms and
   .multiplayer.interest_diff.samples == 1600 and
   .multiplayer.interest_diff.p50_ms <= .multiplayer.interest_diff.p95_ms and
   .multiplayer.interest_diff.p95_ms <= .multiplayer.interest_diff.p99_ms and
   .multiplayer.interest_diff.p99_ms <= .multiplayer.interest_diff.max_ms' \
  "$mcgo_m3c_memory"
test "$(tail -c 1 "$mcgo_m3c_memory" | od -An -t u1 | tr -d '[:space:]')" = 10
shasum -a 256 "$mcgo_m3c_memory" "$mcgo_m3c_memory_log"
```

Expected: benchmark exits 0; schema, transport, exact tick count, and exact interest count match. On any failure, preserve evidence and stop—do not retry.

- [ ] **Step 3: Run the v5→v6 migration comparison exactly once**

```bash
mcgo_m3c_commit="$(git rev-parse --short=12 HEAD)"
mcgo_m3c_memory="/tmp/mcgo-m3c-memory-${mcgo_m3c_commit}.json"
mcgo_m3c_migration="/tmp/mcgo-m3c-migration-${mcgo_m3c_commit}.log"
test ! -e "$mcgo_m3c_migration" || exit 125
set -o pipefail
TERM=xterm-256color zsh -ic "gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck --baseline docs/notes/perf-baseline.json --current '$mcgo_m3c_memory' --max-regression 0.20 --allow-scenario-upgrade 5:6" | tee "$mcgo_m3c_migration"
shasum -a 256 "$mcgo_m3c_migration"
```

Expected: migration succeeds using schema/hardware/current absolute gates only. Any failure ends the formal run.

- [ ] **Step 4: Run the TCP report exactly once**

```bash
mcgo_m3c_commit="$(git rev-parse --short=12 HEAD)"
mcgo_m3c_full_commit="$(git rev-parse HEAD)"
mcgo_m3c_memory="/tmp/mcgo-m3c-memory-${mcgo_m3c_commit}.json"
mcgo_m3c_tcp="/tmp/mcgo-m3c-tcp-${mcgo_m3c_commit}.json"
mcgo_m3c_tcp_log="/tmp/mcgo-m3c-tcp-${mcgo_m3c_commit}.log"
mcgo_m3c_hardware="$(jq -r '.hardware' "$mcgo_m3c_memory")"
set -o pipefail
TERM=xterm-256color zsh -ic "gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport tcp --perf-output '$mcgo_m3c_tcp'" | tee "$mcgo_m3c_tcp_log"
jq -e --arg commit "$mcgo_m3c_full_commit" --arg hardware "$mcgo_m3c_hardware" \
  '.scenario_version == 6 and .transport == "tcp" and .git_commit == $commit and
   .hardware == $hardware and ((.os | length) > 0) and
   ((.go_version | length) > 0) and ((.framebuffer | length) > 0) and
   .ticks.frames == 200 and .ticks.p50_ms > 0 and
   .ticks.p50_ms <= .ticks.p95_ms and .ticks.p95_ms <= .ticks.p99_ms and
   .ticks.p99_ms <= .ticks.max_ms and
   .multiplayer.interest_diff.samples == 1600 and
   .multiplayer.interest_diff.p50_ms <= .multiplayer.interest_diff.p95_ms and
   .multiplayer.interest_diff.p95_ms <= .multiplayer.interest_diff.p99_ms and
   .multiplayer.interest_diff.p99_ms <= .multiplayer.interest_diff.max_ms' \
  "$mcgo_m3c_tcp"
test "$(tail -c 1 "$mcgo_m3c_tcp" | od -An -t u1 | tr -d '[:space:]')" = 10
shasum -a 256 "$mcgo_m3c_tcp" "$mcgo_m3c_tcp_log"
```

Expected: benchmark exits 0 and exact sample counts match. On any failure, stop without retry.

- [ ] **Step 5: Compare Memory→TCP exactly once**

```bash
mcgo_m3c_commit="$(git rev-parse --short=12 HEAD)"
mcgo_m3c_memory="/tmp/mcgo-m3c-memory-${mcgo_m3c_commit}.json"
mcgo_m3c_tcp="/tmp/mcgo-m3c-tcp-${mcgo_m3c_commit}.json"
mcgo_m3c_compare="/tmp/mcgo-m3c-compare-${mcgo_m3c_commit}.log"
set -o pipefail
TERM=xterm-256color zsh -ic "gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck --baseline '$mcgo_m3c_memory' --current '$mcgo_m3c_tcp' --max-regression 0.20" | tee "$mcgo_m3c_compare"
shasum -a 256 "$mcgo_m3c_compare"
```

Expected: only approved cross-transport stable metrics receive relative comparison. Raw max, queue high-water, and independent Memory server-probe fields are absent from relative-regression failures. Any failure stops the formal run.

- [ ] **Step 6: Run post-acceptance microbenchmarks**

Only after Step 5 passes:

```bash
mcgo_m3c_commit="$(git rev-parse --short=12 HEAD)"
mcgo_m3c_micro="/tmp/mcgo-m3c-micro-${mcgo_m3c_commit}.txt"
test ! -e "$mcgo_m3c_micro" || exit 125
set -o pipefail
TERM=xterm-256color zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network ./internal/server ./internal/render -run "^$" -bench "^(BenchmarkRemotePlayerStateCodec|BenchmarkEightPlayerInterest|BenchmarkRemoteAvatarNameTag)$" -benchmem -count=3' | tee "$mcgo_m3c_micro"
test "$(rg -c '^Benchmark(RemotePlayerStateCodec/(Encode|Decode)|EightPlayerInterest|RemoteAvatarNameTag)-[0-9]+' "$mcgo_m3c_micro")" -eq 12
shasum -a 256 "$mcgo_m3c_micro"
```

Expected: benchmark command exits 0 and its hash is recorded. A failure does not authorize rerunning the formal Memory/TCP reports.

- [ ] **Step 7: Promote the accepted Memory report and update human-readable evidence**

```bash
mcgo_m3c_commit="$(git rev-parse --short=12 HEAD)"
mcgo_m3c_full_commit="$(git rev-parse HEAD)"
mcgo_m3c_memory="/tmp/mcgo-m3c-memory-${mcgo_m3c_commit}.json"
mcgo_m3c_v5_backup="/tmp/mcgo-m3c-baseline-v5-${mcgo_m3c_commit}.json"
test ! -e "$mcgo_m3c_v5_backup" || exit 125
cp docs/notes/perf-baseline.json "$mcgo_m3c_v5_backup"
test "$(shasum -a 256 "$mcgo_m3c_v5_backup" | awk '{print $1}')" = \
  428e9b61bd8a8bf782fdc4e8d54f488d544bee4b7da948873638fe40ba60a191
cp "$mcgo_m3c_memory" docs/notes/perf-baseline.json
jq -e --arg commit "$mcgo_m3c_full_commit" \
  '.scenario_version == 6 and .transport == "memory" and .git_commit == $commit and .ticks.frames == 200 and .multiplayer.interest_diff.samples == 1600' \
  docs/notes/perf-baseline.json
cmp -s "$mcgo_m3c_memory" docs/notes/perf-baseline.json
test "$(tail -c 1 docs/notes/perf-baseline.json | od -An -t u1 | tr -d '[:space:]')" = 10
shasum -a 256 docs/notes/perf-baseline.json
```

Generate the exact acceptance block from the artifacts:

```bash
mcgo_m3c_commit="$(git rev-parse --short=12 HEAD)"
mcgo_m3c_memory="/tmp/mcgo-m3c-memory-${mcgo_m3c_commit}.json"
mcgo_m3c_memory_log="/tmp/mcgo-m3c-memory-${mcgo_m3c_commit}.log"
mcgo_m3c_tcp="/tmp/mcgo-m3c-tcp-${mcgo_m3c_commit}.json"
mcgo_m3c_tcp_log="/tmp/mcgo-m3c-tcp-${mcgo_m3c_commit}.log"
mcgo_m3c_migration="/tmp/mcgo-m3c-migration-${mcgo_m3c_commit}.log"
mcgo_m3c_compare="/tmp/mcgo-m3c-compare-${mcgo_m3c_commit}.log"
mcgo_m3c_micro="/tmp/mcgo-m3c-micro-${mcgo_m3c_commit}.txt"
mcgo_m3c_v5_backup="/tmp/mcgo-m3c-baseline-v5-${mcgo_m3c_commit}.json"
jq -nr \
  --slurpfile memory "$mcgo_m3c_memory" \
  --slurpfile tcp "$mcgo_m3c_tcp" \
  --rawfile migration "$mcgo_m3c_migration" \
  --rawfile comparison "$mcgo_m3c_compare" \
  --rawfile micro "$mcgo_m3c_micro" \
  --arg memory_path "$mcgo_m3c_memory" \
  --arg memory_sha "$(shasum -a 256 "$mcgo_m3c_memory" | awk '{print $1}')" \
  --arg memory_log_path "$mcgo_m3c_memory_log" \
  --arg memory_log_sha "$(shasum -a 256 "$mcgo_m3c_memory_log" | awk '{print $1}')" \
  --arg tcp_path "$mcgo_m3c_tcp" \
  --arg tcp_sha "$(shasum -a 256 "$mcgo_m3c_tcp" | awk '{print $1}')" \
  --arg tcp_log_path "$mcgo_m3c_tcp_log" \
  --arg tcp_log_sha "$(shasum -a 256 "$mcgo_m3c_tcp_log" | awk '{print $1}')" \
  --arg migration_path "$mcgo_m3c_migration" \
  --arg migration_sha "$(shasum -a 256 "$mcgo_m3c_migration" | awk '{print $1}')" \
  --arg comparison_path "$mcgo_m3c_compare" \
  --arg comparison_sha "$(shasum -a 256 "$mcgo_m3c_compare" | awk '{print $1}')" \
  --arg micro_path "$mcgo_m3c_micro" \
  --arg micro_sha "$(shasum -a 256 "$mcgo_m3c_micro" | awk '{print $1}')" \
  --arg backup_path "$mcgo_m3c_v5_backup" \
  --arg backup_sha "$(shasum -a 256 "$mcgo_m3c_v5_backup" | awk '{print $1}')" \
  '
  "## M3C scenario v6 accepted baseline\n\n" +
  "| Evidence | Value |\n|---|---|\n" +
  "| implementation commit | `\($memory[0].git_commit)` |\n" +
  "| toolchain | `\($memory[0].go_version)` |\n" +
  "| hardware | `\($memory[0].hardware)` |\n" +
  "| Memory report | `\($memory_path)` — `\($memory_sha)` |\n" +
  "| Memory log | `\($memory_log_path)` — `\($memory_log_sha)` |\n" +
  "| TCP report | `\($tcp_path)` — `\($tcp_sha)` |\n" +
  "| TCP log | `\($tcp_log_path)` — `\($tcp_log_sha)` |\n" +
  "| v5 backup | `\($backup_path)` — `\($backup_sha)` |\n" +
  "| migration | `\($migration_path)` — `\($migration_sha)` |\n" +
  "| Memory→TCP | `\($comparison_path)` — `\($comparison_sha)` |\n" +
  "| microbench | `\($micro_path)` — `\($micro_sha)` |\n\n" +
  "Samples: Memory `\($memory[0].ticks.frames)/\($memory[0].multiplayer.interest_diff.samples)`; " +
  "TCP `\($tcp[0].ticks.frames)/\($tcp[0].multiplayer.interest_diff.samples)`.\n\n" +
  "Policy: v6 cross-transport compares stable transport-related p50/p95/p99, FPS, RSS, load/snapshot, protocol, and persistence; raw max, queue high-water, and the independent Memory server probe are absolute-only. Same-transport additionally compares server tick/interest p50/p95/p99, outbound, and multiplayer RSS.\n\n" +
  "No formal command was retried.\n\n" +
  "### Memory multiplayer\n\n```json\n" + ($memory[0].multiplayer | tojson) +
  "\n```\n\n### TCP multiplayer\n\n```json\n" + ($tcp[0].multiplayer | tojson) +
  "\n```\n\n### Migration output\n\n```text\n" + $migration +
  "```\n\n### Memory→TCP output\n\n```text\n" + $comparison +
  "```\n\n### Microbenchmarks\n\n```text\n" + $micro + "```"
  '
```

Copy the command's complete emitted block exactly into `docs/notes/perf-baseline.md` and the local SDD report/progress with `apply_patch`; do not hand-transcribe paths, hashes, metrics, or raw outputs.

- [ ] **Step 8: Detect scope and commit the promoted baseline**

Invoke `gitnexus_detect_changes({scope:"unstaged",repo:"minecraft-go"})`. It must show documentation/data scope only, with no unexpected executable flow impact. Then:

```bash
git add \
  docs/notes/perf-baseline.json \
  docs/notes/perf-baseline.md
git diff --cached --check
jq -e '.scenario_version == 6 and .transport == "memory"' docs/notes/perf-baseline.json
git commit -m "perf: 建立 M3C scenario v6 性能基线"
```

Expected: v6 Memory baseline and its ledger are committed together; local SDD files remain local unless already tracked.

---

### Task 8: Run the one-shot current-vs-baseline check and close Task 17

**Files:**
- Modify: `docs/notes/perf-baseline.md`
- Modify: `docs/superpowers/plans/2026-08-01-m3c-multiplayer-sync.md`
- Modify locally: `.superpowers/sdd/2026-08-01-m3c-multiplayer-sync/task-17-report.md`
- Modify locally: `.superpowers/sdd/2026-08-01-m3c-multiplayer-sync/progress.md`

- [ ] **Step 1: Preflight a new collision-safe current artifact**

```bash
mcgo_m3c_current_commit="$(git rev-parse --short=12 HEAD)"
mcgo_m3c_current="/tmp/mcgo-m3c-current-${mcgo_m3c_current_commit}.json"
mcgo_m3c_current_log="/tmp/mcgo-m3c-current-${mcgo_m3c_current_commit}.log"
mcgo_m3c_current_compare="/tmp/mcgo-m3c-current-compare-${mcgo_m3c_current_commit}.log"
test -z "$(git status --porcelain --untracked-files=no)"
jq -e '.scenario_version == 6 and .transport == "memory"' docs/notes/perf-baseline.json
! pgrep -f '(^|/)(mcgo|mcgod)( |$)|go run ./cmd/(mcgo|mcgod)'
for mcgo_m3c_path in \
  "$mcgo_m3c_current" "$mcgo_m3c_current_log" "$mcgo_m3c_current_compare"; do
  test ! -e "$mcgo_m3c_path" || exit 125
done
```

Expected: clean tracked state, v6 Memory baseline, no benchmark process, and no path collisions. Any failure is a stop.

- [ ] **Step 2: Run current Memory exactly once**

```bash
mcgo_m3c_current_commit="$(git rev-parse --short=12 HEAD)"
mcgo_m3c_current_full_commit="$(git rev-parse HEAD)"
mcgo_m3c_current="/tmp/mcgo-m3c-current-${mcgo_m3c_current_commit}.json"
mcgo_m3c_current_log="/tmp/mcgo-m3c-current-${mcgo_m3c_current_commit}.log"
mcgo_m3c_baseline_hardware="$(jq -r '.hardware' docs/notes/perf-baseline.json)"
set -o pipefail
TERM=xterm-256color zsh -ic "gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport memory --perf-output '$mcgo_m3c_current'" | tee "$mcgo_m3c_current_log"
jq -e --arg commit "$mcgo_m3c_current_full_commit" --arg hardware "$mcgo_m3c_baseline_hardware" \
  '.scenario_version == 6 and .transport == "memory" and .git_commit == $commit and
   .hardware == $hardware and ((.os | length) > 0) and
   ((.go_version | length) > 0) and ((.framebuffer | length) > 0) and
   .ticks.frames == 200 and .ticks.p50_ms > 0 and
   .ticks.p50_ms <= .ticks.p95_ms and .ticks.p95_ms <= .ticks.p99_ms and
   .ticks.p99_ms <= .ticks.max_ms and
   .multiplayer.interest_diff.samples == 1600 and
   .multiplayer.interest_diff.p50_ms <= .multiplayer.interest_diff.p95_ms and
   .multiplayer.interest_diff.p95_ms <= .multiplayer.interest_diff.p99_ms and
   .multiplayer.interest_diff.p99_ms <= .multiplayer.interest_diff.max_ms' \
  "$mcgo_m3c_current"
test "$(tail -c 1 "$mcgo_m3c_current" | od -An -t u1 | tr -d '[:space:]')" = 10
shasum -a 256 "$mcgo_m3c_current" "$mcgo_m3c_current_log"
```

Expected: exact sample counts and all absolute gates pass. On failure, preserve evidence and stop without retry.

- [ ] **Step 3: Compare v6 Memory baseline→current exactly once**

```bash
mcgo_m3c_current_commit="$(git rev-parse --short=12 HEAD)"
mcgo_m3c_current="/tmp/mcgo-m3c-current-${mcgo_m3c_current_commit}.json"
mcgo_m3c_current_compare="/tmp/mcgo-m3c-current-compare-${mcgo_m3c_current_commit}.log"
set -o pipefail
TERM=xterm-256color zsh -ic "gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck --baseline docs/notes/perf-baseline.json --current '$mcgo_m3c_current' --max-regression 0.20" | tee "$mcgo_m3c_current_compare"
shasum -a 256 "$mcgo_m3c_current_compare"
```

Expected: same-transport stable metrics, including server tick/interest p50/p95/p99, outbound, and multiplayer RSS, stay within the 20% relative threshold. Raw max and queue high-water remain absolute-only. Never retry this formal current run.

- [ ] **Step 4: Update the final ledger**

Generate the exact current-check block:

```bash
mcgo_m3c_current_commit="$(git rev-parse --short=12 HEAD)"
mcgo_m3c_current="/tmp/mcgo-m3c-current-${mcgo_m3c_current_commit}.json"
mcgo_m3c_current_log="/tmp/mcgo-m3c-current-${mcgo_m3c_current_commit}.log"
mcgo_m3c_current_compare="/tmp/mcgo-m3c-current-compare-${mcgo_m3c_current_commit}.log"
jq -nr \
  --slurpfile baseline docs/notes/perf-baseline.json \
  --slurpfile current "$mcgo_m3c_current" \
  --rawfile comparison "$mcgo_m3c_current_compare" \
  --arg current_path "$mcgo_m3c_current" \
  --arg current_sha "$(shasum -a 256 "$mcgo_m3c_current" | awk '{print $1}')" \
  --arg current_log_path "$mcgo_m3c_current_log" \
  --arg current_log_sha "$(shasum -a 256 "$mcgo_m3c_current_log" | awk '{print $1}')" \
  --arg comparison_path "$mcgo_m3c_current_compare" \
  --arg comparison_sha "$(shasum -a 256 "$mcgo_m3c_current_compare" | awk '{print $1}')" \
  '
  "## M3C v6 same-transport current check\n\n" +
  "| Evidence | Value |\n|---|---|\n" +
  "| accepted baseline code commit | `\($baseline[0].git_commit)` |\n" +
  "| current report commit | `\($current[0].git_commit)` |\n" +
  "| current report | `\($current_path)` — `\($current_sha)` |\n" +
  "| current log | `\($current_log_path)` — `\($current_log_sha)` |\n" +
  "| baseline→current | `\($comparison_path)` — `\($comparison_sha)` |\n\n" +
  "Samples: `\($current[0].ticks.frames)/\($current[0].multiplayer.interest_diff.samples)`. " +
  "Same-transport stable metrics and every absolute gate passed. No formal command was retried.\n\n" +
  "### Current multiplayer\n\n```json\n" + ($current[0].multiplayer | tojson) +
  "\n```\n\n### Baseline→current output\n\n```text\n" + $comparison + "```"
  '
```

Copy the complete emitted block exactly into `docs/notes/perf-baseline.md` and the local SDD report/progress with `apply_patch`. Only after every gate above is green, use `apply_patch` to change the Task 17 Step 5–9 checkboxes and the matching M3C completion checklist items in `docs/superpowers/plans/2026-08-01-m3c-multiplayer-sync.md` from `[ ]` to `[x]`; leave every unrelated task untouched.

- [ ] **Step 5: Request final independent reviews**

Dispatch:

```text
Spec reviewer: Verify the final commits and evidence against the approved M3C repeatability design, including migration, cross-transport, same-transport, exact sampling, provenance, and no-retry rules. Report Critical/Important/Minor only.
Code reviewer: Review efcee05..HEAD for correctness, concurrency, legacy compatibility, gate coverage, and reproducibility. Cite file/line evidence; do not edit.
```

Expected: no Critical/Important findings. Any accepted blocker reopens the relevant implementation task and invalidates completion; it does not authorize another formal performance run.

- [ ] **Step 6: Run final non-performance checks and commit closure**

```bash
TERM=xterm-256color zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -count=1'
TERM=xterm-256color zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network ./internal/server ./internal/client ./internal/render ./cmd/mcgo ./cmd/perfcheck -race -count=1'
git diff --check
```

Invoke `gitnexus_detect_changes({scope:"unstaged",repo:"minecraft-go"})`, confirm only expected documentation/evidence changed since the v6 baseline commit, then:

```bash
git add \
  docs/notes/perf-baseline.md \
  docs/superpowers/plans/2026-08-01-m3c-multiplayer-sync.md
git diff --cached --check
git commit -m "docs: 完成 M3C 多人同步验收"
git status --short
git log --oneline --decorate -12
```

Expected: Task 17 closes with executable code, the v6 baseline, formal evidence, comparison logs, and the final ledger all tied to explicit commits.

---

## Stop Conditions

Stop immediately and preserve all evidence when any of these occurs:

- GitNexus reports HIGH or CRITICAL impact before an edit and the user has not approved that risk.
- A RED test does not fail for the intended reason.
- Exact `200/1600` sample accounting, signal overflow, cleanup, or absolute threshold checks fail.
- A reviewer reports an unresolved Critical or Important finding.
- The baseline changes before the approved promotion step.
- A formal evidence path already exists.
- A benchmark process is already running.
- Any formal Memory, migration, TCP, cross-transport, current, or same-transport command fails.
- Tracked state is dirty at a formal preflight.

There is no automatic formal retry. Diagnose with non-formal tests or new collision-safe diagnostic artifacts, fix through the RED/GREEN/review flow, rerun the complete Step 5 gate, and obtain new explicit user authorization before any new formal performance execution.
