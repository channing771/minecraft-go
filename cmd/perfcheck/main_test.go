package main

import (
	"strings"
	"testing"

	"minecraft-go/internal/client"
)

func TestCompareReportsChecksPersistenceWhenBothHaveSamples(t *testing.T) {
	baseline := comparableReport()
	current := comparableReport()
	baseline.Persistence = client.PersistenceSummary{
		Snapshots: 10, P50MS: 1, P95MS: 2, P99MS: 3, MaxMS: 4,
	}
	current.Persistence = client.PersistenceSummary{
		Snapshots: 12, P50MS: 1.3, P95MS: 2.5, P99MS: 3.7, MaxMS: 5,
	}
	failures, err := compareReports(baseline, current, 0.20)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(failures, "\n")
	for _, metric := range []string{"persistence p50_ms", "persistence p95_ms", "persistence p99_ms", "persistence max_ms"} {
		if !strings.Contains(joined, metric) {
			t.Fatalf("failures=%q，缺少 %q", joined, metric)
		}
	}
}

func TestCompareReportsKeepsOldReportCompatibility(t *testing.T) {
	baseline := comparableReport()
	current := comparableReport()
	current.Persistence = client.PersistenceSummary{
		Snapshots: 1, P50MS: 100, P95MS: 100, P99MS: 100, MaxMS: 100,
	}
	failures, err := compareReports(baseline, current, 0.20)
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 0 {
		t.Fatalf("old report compatibility failures=%v", failures)
	}
}

func TestCompareReportsRejectsScenarioAndHardwareMismatch(t *testing.T) {
	baseline := comparableReport()
	current := comparableReport()
	current.ScenarioVersion++
	if _, err := compareReports(baseline, current, 0.20); err == nil ||
		!strings.Contains(err.Error(), "scenario_version") {
		t.Fatalf("scenario mismatch error=%v", err)
	}

	current = comparableReport()
	current.Hardware = "different"
	if _, err := compareReports(baseline, current, 0.20); err == nil ||
		!strings.Contains(err.Error(), "硬件标识不同") {
		t.Fatalf("hardware mismatch error=%v", err)
	}
}

func TestCompareReportsChecksProtocolAndPlayerPersistenceV5(t *testing.T) {
	baseline := comparableReport()
	baseline.ScenarioVersion = 5
	baseline.Transport = "memory"
	baseline.Protocol = client.ProtocolSummary{EncodeP99MS: 1, DecodeP99MS: 2, Bytes: 100}
	baseline.PlayerPersistence = client.PersistenceSummary{
		Snapshots: 10, P50MS: 1, P95MS: 2, P99MS: 3, MaxMS: 4,
	}
	current := baseline
	current.Transport = "tcp"
	current.Protocol = client.ProtocolSummary{EncodeP99MS: 1.3, DecodeP99MS: 2.5, Bytes: 125}
	current.PlayerPersistence = client.PersistenceSummary{
		Snapshots: 10, P50MS: 1.3, P95MS: 2.5, P99MS: 3.7, MaxMS: 5,
	}
	failures, err := compareReports(baseline, current, 0.20)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(failures, "\n")
	for _, metric := range []string{
		"protocol encode_p99_ms", "protocol decode_p99_ms", "protocol bytes",
		"player_persistence p50_ms", "player_persistence p95_ms",
		"player_persistence p99_ms", "player_persistence max_ms",
	} {
		if !strings.Contains(joined, metric) {
			t.Fatalf("failures=%q，缺少 %q", joined, metric)
		}
	}
}

func TestCompareReportsM3BFieldsUseOldReportFallback(t *testing.T) {
	baseline := comparableReport()
	current := comparableReport()
	current.Protocol = client.ProtocolSummary{EncodeP99MS: 100, DecodeP99MS: 100, Bytes: 100}
	current.PlayerPersistence = client.PersistenceSummary{Snapshots: 1, P99MS: 100, MaxMS: 100}
	failures, err := compareReports(baseline, current, 0.20)
	if err != nil || len(failures) != 0 {
		t.Fatalf("old-report fallback failures=%v error=%v", failures, err)
	}
}

func TestCompareReportsIgnoresSubPointZeroOneMillisecondM3BLatencyNoise(t *testing.T) {
	baseline := comparableReport()
	baseline.ScenarioVersion = 5
	baseline.Transport = "memory"
	baseline.Protocol = client.ProtocolSummary{
		EncodeP99MS: 0.000375,
		DecodeP99MS: 0.000042,
		Bytes:       38912,
	}
	baseline.PlayerPersistence = client.PersistenceSummary{
		Snapshots: 256,
		P50MS:     0.000291,
		P95MS:     0.000458,
		P99MS:     0.000958,
		MaxMS:     0.017750,
	}
	current := baseline
	current.Transport = "tcp"
	current.Protocol = client.ProtocolSummary{
		EncodeP99MS: 0.000417,
		DecodeP99MS: 0.000083,
		Bytes:       38912,
	}
	current.PlayerPersistence = client.PersistenceSummary{
		Snapshots: 256,
		P50MS:     0.000292,
		P95MS:     0.000625,
		P99MS:     0.001792,
		MaxMS:     0.019042,
	}

	failures, err := compareReports(baseline, current, 0.20)
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 0 {
		t.Fatalf("sub-0.01ms M3B latency noise failures=%v", failures)
	}
}

func TestCompareReportsKeepsTwentyPercentRuleAtM3BLatencyNoiseFloor(t *testing.T) {
	baseline := comparableReport()
	baseline.ScenarioVersion = 5
	baseline.Transport = "memory"
	baseline.Protocol = client.ProtocolSummary{
		EncodeP99MS: 0.01,
		DecodeP99MS: 0.01,
		Bytes:       100,
	}
	baseline.PlayerPersistence = client.PersistenceSummary{
		Snapshots: 10,
		P50MS:     0.01,
		P95MS:     0.01,
		P99MS:     0.01,
		MaxMS:     0.01,
	}
	current := baseline
	current.Transport = "tcp"
	current.Protocol = client.ProtocolSummary{
		EncodeP99MS: 0.0121,
		DecodeP99MS: 0.0121,
		Bytes:       100,
	}
	current.PlayerPersistence = client.PersistenceSummary{
		Snapshots: 10,
		P50MS:     0.0121,
		P95MS:     0.0121,
		P99MS:     0.0121,
		MaxMS:     0.0121,
	}

	failures, err := compareReports(baseline, current, 0.20)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(failures, "\n")
	for _, metric := range []string{
		"protocol encode_p99_ms",
		"protocol decode_p99_ms",
		"player_persistence p50_ms",
		"player_persistence p95_ms",
		"player_persistence p99_ms",
		"player_persistence max_ms",
	} {
		if !strings.Contains(joined, metric) {
			t.Fatalf("failures=%q，缺少 floor 以上的 %q", joined, metric)
		}
	}
}

func TestCompareReportsRejectsIncompleteV5NewFields(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*client.PerfReport)
	}{
		{name: "missing transport", mutate: func(report *client.PerfReport) { report.Transport = "" }},
		{name: "invalid transport", mutate: func(report *client.PerfReport) { report.Transport = "udp" }},
		{name: "protocol encode zero", mutate: func(report *client.PerfReport) { report.Protocol.EncodeP99MS = 0 }},
		{name: "protocol decode zero", mutate: func(report *client.PerfReport) { report.Protocol.DecodeP99MS = 0 }},
		{name: "protocol bytes zero", mutate: func(report *client.PerfReport) { report.Protocol.Bytes = 0 }},
		{name: "player snapshots zero", mutate: func(report *client.PerfReport) { report.PlayerPersistence.Snapshots = 0 }},
		{name: "player snapshots negative", mutate: func(report *client.PerfReport) { report.PlayerPersistence.Snapshots = -1 }},
		{name: "player p50 zero", mutate: func(report *client.PerfReport) { report.PlayerPersistence.P50MS = 0 }},
		{name: "player p95 zero", mutate: func(report *client.PerfReport) { report.PlayerPersistence.P95MS = 0 }},
		{name: "player p99 zero", mutate: func(report *client.PerfReport) { report.PlayerPersistence.P99MS = 0 }},
		{name: "player max zero", mutate: func(report *client.PerfReport) { report.PlayerPersistence.MaxMS = 0 }},
	}
	for _, test := range cases {
		for _, side := range []string{"baseline", "current"} {
			t.Run(side+"/"+test.name, func(t *testing.T) {
				baseline := completeV5ComparableReport("memory")
				current := completeV5ComparableReport("tcp")
				if side == "baseline" {
					test.mutate(&baseline)
				} else {
					test.mutate(&current)
				}
				if _, err := compareReports(baseline, current, 0.20); err == nil ||
					!strings.Contains(err.Error(), side) {
					t.Fatalf("%s incomplete v5 error=%v", side, err)
				}
			})
		}
	}
}

func TestCompareReportsPreservesV4NewFieldFallback(t *testing.T) {
	baseline := comparableReport()
	current := comparableReport()
	if failures, err := compareReports(baseline, current, 0.20); err != nil || len(failures) != 0 {
		t.Fatalf("v4 fallback failures=%v error=%v", failures, err)
	}
}

func TestPerfcheckMultiplayerScenarioUpgradeAndProvenanceRules(t *testing.T) {
	v8 := completeV8ComparableReport("memory")
	v9 := completeV9ComparableReport("memory")
	if _, err := compareReports(v8, v9, 0.20); err == nil || !strings.Contains(err.Error(), "scenario_version") {
		t.Fatalf("default cross-scenario comparison error=%v", err)
	}
	if failures, err := compareReportsWithScenarioUpgrade(v8, v9, 0.20, "8:9"); err != nil || len(failures) != 0 {
		t.Fatalf("explicit 8:9 migration failures=%v error=%v", failures, err)
	}
	for _, test := range []struct {
		name  string
		clear func(*client.PerfReport)
	}{
		{name: "hardware", clear: func(report *client.PerfReport) { report.Hardware = "" }},
		{name: "os", clear: func(report *client.PerfReport) { report.OS = "" }},
		{name: "go_version", clear: func(report *client.PerfReport) { report.GoVersion = "" }},
		{name: "git_commit", clear: func(report *client.PerfReport) { report.GitCommit = "" }},
		{name: "framebuffer", clear: func(report *client.PerfReport) { report.Framebuffer = "" }},
	} {
		t.Run("empty "+test.name, func(t *testing.T) {
			baseline, current := v8, v9
			test.clear(&baseline)
			test.clear(&current)
			if _, err := compareReportsWithScenarioUpgrade(baseline, current, 0.20, "8:9"); err == nil ||
				!strings.Contains(err.Error(), test.name) {
				t.Fatalf("empty %s provenance error=%v", test.name, err)
			}
		})
	}
	for _, test := range []struct {
		name, allow       string
		baseline, current client.PerfReport
	}{
		{name: "reverse", allow: "9:8", baseline: v9, current: v8},
		{name: "skip", allow: "7:9", baseline: completeV7ComparableReport("memory"), current: v9},
		{name: "wrong flag", allow: "7:8", baseline: v8, current: v9},
		{name: "retired 5:6", allow: "5:6", baseline: completeV5ComparableReport("memory"), current: completeV6ComparableReport("memory")},
		{name: "retired 6:7", allow: "6:7", baseline: completeV6ComparableReport("memory"), current: completeV7ComparableReport("memory")},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := compareReportsWithScenarioUpgrade(test.baseline, test.current, 0.20, test.allow); err == nil {
				t.Fatalf("invalid migration %q unexpectedly accepted", test.allow)
			}
		})
	}
	v9.Hardware = "different"
	if _, err := compareReportsWithScenarioUpgrade(v8, v9, 0.20, "8:9"); err == nil ||
		!strings.Contains(err.Error(), "硬件标识不同") {
		t.Fatalf("cross-hardware migration error=%v", err)
	}
}

func TestPerfcheckV9SameScenarioComparesMemoryAndTCP(t *testing.T) {
	baseline := completeV9ComparableReport("memory")
	sameScenarioCurrent := completeV9ComparableReport("tcp")
	still := sameScenarioCurrent.Phases["still"]
	still.MaxMS *= 2
	sameScenarioCurrent.Phases["still"] = still
	if failures, err := compareReports(baseline, sameScenarioCurrent, 0.20); err != nil || len(failures) != 0 {
		t.Fatalf("v9 Memory/TCP comparison failures=%v error=%v", failures, err)
	}
}

func TestPerfcheckV8Requires2048GPUCompletionSamples(t *testing.T) {
	baseline := completeV8ComparableReport("memory")
	current := completeV8ComparableReport("tcp")
	current.Multiplayer.RemoteGPUComplete.Samples = 2047
	if _, err := compareReports(baseline, current, 0.20); err == nil ||
		!strings.Contains(err.Error(), "remote_gpu_complete") {
		t.Fatalf("v8 low GPU samples error=%v", err)
	}

	current.Multiplayer.RemoteGPUComplete.Samples = 2048
	if failures, err := compareReports(baseline, current, 0.20); err != nil || len(failures) != 0 {
		t.Fatalf("v8 comparison failures=%v err=%v", failures, err)
	}

	v7 := completeV7ComparableReport("memory")
	v7.Multiplayer.RemoteGPUComplete.Samples = 256
	v7Current := completeV7ComparableReport("memory")
	v7Current.Multiplayer.RemoteGPUComplete.Samples = 256
	if failures, err := compareReports(v7, v7Current, 0.20); err != nil || len(failures) != 0 {
		t.Fatalf("v7 compatibility failures=%v err=%v", failures, err)
	}
	if _, err := compareReports(v7, current, 0.20); err == nil ||
		!strings.Contains(err.Error(), "scenario_version") {
		t.Fatalf("v7/v8 mismatch error=%v", err)
	}
}

func TestPerfcheckScenarioUpgradeSkipsRelativeRegressions(t *testing.T) {
	baseline := completeV8ComparableReport("memory")
	baseline.Persistence = client.PersistenceSummary{
		Snapshots: 10, P50MS: 1, P95MS: 1, P99MS: 1, MaxMS: 1,
	}
	current := completeV9ComparableReport("memory")
	current.LoadSeconds = 2
	current.SnapshotSeconds = 2
	current.Ticks = client.PhaseSummary{Frames: 200, P50MS: 2, P95MS: 3, P99MS: 4, MaxMS: 5}
	current.Persistence = client.PersistenceSummary{
		Snapshots: 10, P50MS: 2, P95MS: 3, P99MS: 4, MaxMS: 5,
	}
	current.Protocol = client.ProtocolSummary{EncodeP99MS: 0.02, DecodeP99MS: 0.02, Bytes: 200}
	current.PlayerPersistence = client.PersistenceSummary{
		Snapshots: 10, P50MS: 0.02, P95MS: 0.03, P99MS: 0.04, MaxMS: 0.05,
	}
	for _, name := range []string{"still", "flying"} {
		current.Phases[name] = client.PhaseSummary{
			Frames: 1000, FPS: 100, P50MS: 2, P95MS: 3, P99MS: 4, MaxMS: 5, PeakRSSBytes: 2,
		}
	}

	failures, err := compareReportsWithScenarioUpgrade(baseline, current, 0.20, "8:9")
	if err != nil || len(failures) != 0 {
		t.Fatalf("explicit migration failures=%v err=%v", failures, err)
	}
}

func TestPerfcheckScenarioUpgradeKeepsAbsoluteAndSchemaGates(t *testing.T) {
	baseline := completeV8ComparableReport("memory")
	t.Run("absolute", func(t *testing.T) {
		current := completeV9ComparableReport("memory")
		phase := current.Phases["still"]
		phase.P99MS = 12
		phase.MaxMS = 12
		current.Phases["still"] = phase
		failures, err := compareReportsWithScenarioUpgrade(baseline, current, 0.20, "8:9")
		if err != nil || !strings.Contains(strings.Join(failures, "\n"), "still p99") {
			t.Fatalf("absolute migration failures=%v err=%v", failures, err)
		}
	})
	t.Run("schema", func(t *testing.T) {
		current := completeV9ComparableReport("memory")
		current.Multiplayer.RosterApply = client.LatencySummary{}
		if _, err := compareReportsWithScenarioUpgrade(baseline, current, 0.20, "8:9"); err == nil ||
			!strings.Contains(err.Error(), "current") {
			t.Fatalf("schema migration error=%v", err)
		}
	})
}

func TestPerfcheckScenarioUpgradeKeepsProducerAbsoluteGates(t *testing.T) {
	for _, test := range []struct {
		name, want string
		mutate     func(*client.PerfReport)
	}{
		{name: "protocol encode", want: "protocol encode p99", mutate: func(report *client.PerfReport) {
			report.Protocol.EncodeP99MS = 1
		}},
		{name: "protocol decode", want: "protocol decode p99", mutate: func(report *client.PerfReport) {
			report.Protocol.DecodeP99MS = 1
		}},
		{name: "player persistence p99", want: "player persistence p99", mutate: func(report *client.PerfReport) {
			report.PlayerPersistence.P99MS = 5
			report.PlayerPersistence.MaxMS = 5
		}},
		{name: "player persistence max", want: "player persistence max", mutate: func(report *client.PerfReport) {
			report.PlayerPersistence.MaxMS = 20
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			baseline := completeV8ComparableReport("memory")
			current := completeV9ComparableReport("memory")
			test.mutate(&current)

			failures, err := compareReportsWithScenarioUpgrade(baseline, current, 0.20, "8:9")
			if err != nil || !strings.Contains(strings.Join(failures, "\n"), test.want) {
				t.Fatalf("absolute producer gate failures=%v err=%v want=%q", failures, err, test.want)
			}
		})
	}
}

func TestPerfcheckScenarioUpgradeRejectsIncompleteV9Report(t *testing.T) {
	for _, test := range []struct {
		name, want string
		mutate     func(*client.PerfReport)
	}{
		{name: "load zero", want: "load_seconds", mutate: func(report *client.PerfReport) {
			report.LoadSeconds = 0
		}},
		{name: "snapshot zero", want: "snapshot_seconds", mutate: func(report *client.PerfReport) {
			report.SnapshotSeconds = 0
		}},
		{name: "missing still", want: "still", mutate: func(report *client.PerfReport) {
			delete(report.Phases, "still")
		}},
		{name: "unexpected phase", want: "phases", mutate: func(report *client.PerfReport) {
			report.Phases["unexpected"] = report.Phases["still"]
		}},
		{name: "still frames zero", want: "still", mutate: func(report *client.PerfReport) {
			phase := report.Phases["still"]
			phase.Frames = 0
			report.Phases["still"] = phase
		}},
		{name: "still percentile non-monotonic", want: "still", mutate: func(report *client.PerfReport) {
			phase := report.Phases["still"]
			phase.P50MS = phase.P95MS + 1
			report.Phases["still"] = phase
		}},
		{name: "ticks frame count", want: "ticks frames", mutate: func(report *client.PerfReport) {
			report.Ticks.Frames = 199
		}},
		{name: "ticks fps nonzero", want: "ticks fps", mutate: func(report *client.PerfReport) {
			report.Ticks.FPS = 1
		}},
		{name: "ticks percentile zero", want: "ticks", mutate: func(report *client.PerfReport) {
			report.Ticks.P50MS = 0
		}},
		{name: "ticks percentile non-monotonic", want: "ticks", mutate: func(report *client.PerfReport) {
			report.Ticks.P50MS = report.Ticks.P95MS + 1
		}},
		{name: "persistence samples zero", want: "persistence", mutate: func(report *client.PerfReport) {
			report.Persistence.Snapshots = 0
		}},
		{name: "persistence percentile zero", want: "persistence", mutate: func(report *client.PerfReport) {
			report.Persistence.P50MS = 0
		}},
		{name: "persistence percentile non-monotonic", want: "persistence", mutate: func(report *client.PerfReport) {
			report.Persistence.P50MS = report.Persistence.P95MS + 1
		}},
		{name: "interest sample count", want: "interest_diff samples", mutate: func(report *client.PerfReport) {
			report.Multiplayer.InterestDiff.Samples = 1599
		}},
		{name: "interest percentile zero", want: "interest_diff", mutate: func(report *client.PerfReport) {
			report.Multiplayer.InterestDiff.P50MS = 0
		}},
		{name: "interest percentile non-monotonic", want: "interest_diff", mutate: func(report *client.PerfReport) {
			report.Multiplayer.InterestDiff.P50MS = report.Multiplayer.InterestDiff.P95MS + 1
		}},
		{name: "GPU completion samples zero", want: "remote_gpu_complete", mutate: func(report *client.PerfReport) {
			report.Multiplayer.RemoteGPUComplete.Samples = 0
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			baseline := completeV8ComparableReport("memory")
			current := completeV9ComparableReport("memory")
			test.mutate(&current)

			if _, err := compareReportsWithScenarioUpgrade(baseline, current, 0.20, "8:9"); err == nil ||
				!strings.Contains(err.Error(), "current") || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("incomplete v9 error=%v want=%q", err, test.want)
			}
		})
	}
}

func TestComparisonSuccessMessageDescribesComparisonMode(t *testing.T) {
	if got := comparisonSuccessMessage(8, 9); got !=
		"场景迁移验证通过：报告完整、硬件一致且当前 v9 绝对门禁通过" {
		t.Fatalf("migration message=%q", got)
	}
	if got := comparisonSuccessMessage(6, 6); got !=
		"同场景性能比较通过：适用的稳定指标退化均未超过阈值且绝对门禁通过" {
		t.Fatalf("same-scenario message=%q", got)
	}
}

func TestPerfcheckV6CrossTransportIgnoresRawTailAndIndependentServerProbe(t *testing.T) {
	baseline := completeV6ComparableReport("memory")
	current := completeV6ComparableReport("tcp")
	baseline.Persistence = client.PersistenceSummary{
		Snapshots: 10, P50MS: 1, P95MS: 2, P99MS: 3, MaxMS: 4,
	}
	current.Persistence = baseline.Persistence
	current.Ticks = client.PhaseSummary{Frames: 200, P50MS: 2, P95MS: 3, P99MS: 3.7, MaxMS: 4.9}
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
			phase.MaxMS = phase.P99MS
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
			report.Ticks.MaxMS = report.Ticks.P99MS
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

func TestPerfcheckMultiplayerRejectsMissingAndLowSamples(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*client.PerfReport)
	}{
		{name: "missing latency", mutate: func(report *client.PerfReport) { report.Multiplayer.RosterApply = client.LatencySummary{} }},
		{name: "low client samples", mutate: func(report *client.PerfReport) { report.Multiplayer.AvatarSubmit.Samples = 255 }},
		{name: "low interest samples", mutate: func(report *client.PerfReport) { report.Multiplayer.InterestDiff.Samples = 999 }},
		{name: "missing outbound", mutate: func(report *client.PerfReport) { report.Multiplayer.ServerOutboundBytes = 0 }},
		{name: "missing rss", mutate: func(report *client.PerfReport) { report.Multiplayer.PeakRSSBytes = 0 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			baseline := completeV6ComparableReport("memory")
			current := completeV6ComparableReport("tcp")
			test.mutate(&current)
			if _, err := compareReports(baseline, current, 0.20); err == nil || !strings.Contains(err.Error(), "current") {
				t.Fatalf("incomplete report error=%v", err)
			}
		})
	}
}

func TestPerformanceThresholdsPerfcheckRejectsTickP99AtTenMilliseconds(t *testing.T) {
	baseline := completeV6ComparableReport("memory")
	current := completeV6ComparableReport("tcp")
	baseline.Ticks.P99MS = 9.999
	baseline.Ticks.MaxMS = 9.999
	current.Ticks.P99MS = 9.999
	current.Ticks.MaxMS = 9.999
	if failures, err := compareReports(baseline, current, 0.20); err != nil || len(failures) != 0 {
		t.Fatalf("9.999ms current rejected: failures=%v err=%v", failures, err)
	}
	baseline.Ticks.P99MS = 10
	baseline.Ticks.MaxMS = 10
	current.Ticks.P99MS = 10
	current.Ticks.MaxMS = 10
	failures, err := compareReports(baseline, current, 0.20)
	if err != nil || !strings.Contains(strings.Join(failures, "\n"), "tick p99 10.000 ms >= 10 ms") {
		t.Fatalf("10ms current boundary failures=%v err=%v", failures, err)
	}
}

func completeV6ComparableReport(transport string) client.PerfReport {
	report := completeV5ComparableReport(transport)
	report.ScenarioVersion = 6
	latency := client.LatencySummary{Samples: 1000, P50MS: 1, P95MS: 2, P99MS: 3, MaxMS: 4}
	interestLatency := latency
	interestLatency.Samples = 1600
	report.Multiplayer = client.MultiplayerSummary{
		RemoteStateEncode: latency, RemoteStateDecode: latency, InterestDiff: interestLatency,
		RosterApply: latency, Interpolation: latency, AvatarSubmit: latency,
		NameTagSubmit: latency, RemoteGPUComplete: latency,
		ServerOutboundBytes: 100, OutboxHighWater: 10, PlayerJobsHighWater: 10,
		PlayerDoneHighWater: 1, PeakRSSBytes: 100,
	}
	report.Ticks.Frames = 200
	report.Persistence = client.PersistenceSummary{
		Snapshots: 10, P50MS: 1, P95MS: 2, P99MS: 3, MaxMS: 4,
	}
	still := report.Phases["still"]
	still.Frames = 1000
	report.Phases["still"] = still
	report.Phases["flying"] = report.Phases["still"]
	return report
}

func completeV7ComparableReport(transport string) client.PerfReport {
	report := completeV6ComparableReport(transport)
	report.ScenarioVersion = 7
	return report
}

func completeV8ComparableReport(transport string) client.PerfReport {
	report := completeV7ComparableReport(transport)
	report.ScenarioVersion = 8
	report.Multiplayer.RemoteGPUComplete.Samples = 2048
	return report
}

func completeV9ComparableReport(transport string) client.PerfReport {
	report := completeV8ComparableReport(transport)
	report.ScenarioVersion = 9
	return report
}

func completeV5ComparableReport(transport string) client.PerfReport {
	report := comparableReport()
	report.ScenarioVersion = 5
	report.Transport = transport
	report.Protocol = client.ProtocolSummary{EncodeP99MS: 0.01, DecodeP99MS: 0.01, Bytes: 100}
	report.PlayerPersistence = client.PersistenceSummary{
		Snapshots: 10, P50MS: 0.01, P95MS: 0.02, P99MS: 0.03, MaxMS: 0.04,
	}
	return report
}

func comparableReport() client.PerfReport {
	return client.PerfReport{
		ScenarioVersion: 4,
		Hardware:        "same-machine",
		OS:              "test-os",
		GoVersion:       "test-go",
		GitCommit:       "test-commit",
		Framebuffer:     "2560x1440",
		LoadSeconds:     1,
		SnapshotSeconds: 1,
		Ticks: client.PhaseSummary{
			P50MS: 1, P95MS: 1, P99MS: 1, MaxMS: 1,
		},
		Phases: map[string]client.PhaseSummary{
			"still": {FPS: 100, P50MS: 1, P95MS: 1, P99MS: 1, MaxMS: 1, PeakRSSBytes: 1},
		},
	}
}
