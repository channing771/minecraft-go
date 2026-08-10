package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	v14 := completeV14ComparableReport("memory")
	v15 := completeV15ComparableReport("memory")
	if _, err := compareReports(v14, v15, 0.20); err == nil || !strings.Contains(err.Error(), "scenario_version") {
		t.Fatalf("default cross-scenario comparison error=%v", err)
	}
	if failures, err := compareReportsWithScenarioUpgrade(v14, v15, 0.20, "14:15"); err != nil || len(failures) != 0 {
		t.Fatalf("explicit 14:15 migration failures=%v error=%v", failures, err)
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
			baseline, current := v14, v15
			test.clear(&baseline)
			test.clear(&current)
			if _, err := compareReportsWithScenarioUpgrade(baseline, current, 0.20, "14:15"); err == nil ||
				!strings.Contains(err.Error(), test.name) {
				t.Fatalf("empty %s provenance error=%v", test.name, err)
			}
		})
	}
	for _, test := range []struct {
		name, allow       string
		baseline, current client.PerfReport
	}{
		{name: "reverse", allow: "14:15", baseline: v15, current: v14},
		{name: "retired 13:14", allow: "13:14", baseline: completeV13ComparableReport("memory"), current: v14},
		{name: "skip 13:15", allow: "13:15", baseline: completeV13ComparableReport("memory"), current: v15},
		{name: "retired 10:12", allow: "10:12", baseline: completeV10ComparableReport("memory"), current: completeV12ComparableReport("memory")},
		{name: "retired 11:12", allow: "11:12", baseline: completeV11ComparableReport("memory"), current: completeV12ComparableReport("memory")},
		{name: "retired 10:11", allow: "10:11", baseline: completeV10ComparableReport("memory"), current: completeV11ComparableReport("memory")},
		{name: "retired 5:6", allow: "5:6", baseline: completeV5ComparableReport("memory"), current: completeV6ComparableReport("memory")},
		{name: "retired 6:7", allow: "6:7", baseline: completeV6ComparableReport("memory"), current: completeV7ComparableReport("memory")},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := compareReportsWithScenarioUpgrade(test.baseline, test.current, 0.20, test.allow); err == nil {
				t.Fatalf("invalid migration %q unexpectedly accepted", test.allow)
			}
		})
	}
	v15.Hardware = "different"
	if _, err := compareReportsWithScenarioUpgrade(v14, v15, 0.20, "14:15"); err == nil ||
		!strings.Contains(err.Error(), "硬件标识不同") {
		t.Fatalf("cross-hardware migration error=%v", err)
	}
}

func TestPerfcheckScenarioUpgradeMatrix(t *testing.T) {
	tests := []struct {
		baseline int
		current  int
		allow    string
		wantErr  bool
	}{
		{14, 15, "", true},       // 无授权跨场景拒绝
		{14, 15, "14:15", false}, // 唯一允许的显式迁移
		{15, 14, "14:15", true},  // 反向参数拒绝
		{6, 15, "6:15", true},    // 独立基线不得伪装成迁移
		{13, 14, "13:14", true},  // 历史迁移退役
		{13, 15, "13:15", true},  // 跳级迁移拒绝
		{12, 13, "12:13", true},  // 旧迁移退役
		{12, 14, "12:14", true},  // 跳级迁移拒绝
		{11, 14, "11:14", true},  // 跳级迁移拒绝
		{11, 12, "11:12", true},  // 旧迁移退役
		{10, 12, "10:12", true},  // 旧迁移退役
		{10, 11, "10:11", true},  // 旧迁移退役
		{9, 10, "9:10", true},    // 旧迁移退役
		{8, 9, "8:9", true},      // 旧迁移退役
		{12, 12, "10:12", true},  // 同场景不得忽略退役授权
		{14, 14, "14:15", true},  // 同场景不得忽略未使用授权
		{15, 15, "14:15", true},  // 同场景不得忽略未使用授权
		{15, 15, "", false},      // v15 同场景
		{14, 14, "", false},      // v14 同场景
		{13, 13, "", false},      // v13 同场景
		{12, 12, "", false},      // v12 同场景
		{11, 11, "", false},      // v11 同场景
	}
	for _, test := range tests {
		baseline := scenarioComparableReport(test.baseline, "memory")
		current := scenarioComparableReport(test.current, "memory")
		_, err := compareReportsWithScenarioUpgrade(baseline, current, 0.20, test.allow)
		if (err != nil) != test.wantErr {
			t.Errorf("baseline=%d current=%d allow=%q error=%v, wantErr=%v", test.baseline, test.current, test.allow, err, test.wantErr)
		}
	}
}

func TestPerfcheckHistoricalV6ToV9ReportsRemainComparable(t *testing.T) {
	tests := []struct {
		name   string
		report client.PerfReport
	}{
		{"v6", completeV6ComparableReport("memory")},
		{"v7", completeV7ComparableReport("memory")},
		{"v8", completeV8ComparableReport("memory")},
		{"v9", completeV9ComparableReport("memory")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if failures, err := compareReports(test.report, test.report, 0.20); err != nil || len(failures) != 0 {
				t.Fatalf("历史报告比较 failures=%v error=%v", failures, err)
			}
		})
	}
}

func TestPerfcheckV10SameScenarioComparesMemoryAndTCP(t *testing.T) {
	baseline := completeV10ComparableReport("memory")
	sameScenarioCurrent := completeV10ComparableReport("tcp")
	still := sameScenarioCurrent.Phases["still"]
	still.MaxMS *= 2
	sameScenarioCurrent.Phases["still"] = still
	if failures, err := compareReports(baseline, sameScenarioCurrent, 0.20); err != nil || len(failures) != 0 {
		t.Fatalf("v10 Memory/TCP comparison failures=%v error=%v", failures, err)
	}
	// v10 的 remote_gpu_complete 是逐次计时，分辨率不足以支撑相对判定，
	// 因此同样幅度的变化不再报告相对回归；v12 的批量分摊指标仍然报告。
	sameScenarioCurrent.Multiplayer.RemoteGPUComplete.P99MS *= 1.201
	sameScenarioCurrent.Multiplayer.RemoteGPUComplete.MaxMS = sameScenarioCurrent.Multiplayer.RemoteGPUComplete.P99MS
	if failures, err := compareReports(baseline, sameScenarioCurrent, 0.20); err != nil || len(failures) != 0 {
		t.Fatalf("v10 量化 GPU 指标不应报告相对回归：failures=%v error=%v", failures, err)
	}

	v12Baseline := completeV12ComparableReport("memory")
	v12Current := completeV12ComparableReport("tcp")
	v12Current.Multiplayer.RemoteGPUComplete.P99MS *= 1.201
	v12Current.Multiplayer.RemoteGPUComplete.MaxMS = v12Current.Multiplayer.RemoteGPUComplete.P99MS
	if failures, err := compareReports(v12Baseline, v12Current, 0.20); err != nil ||
		!strings.Contains(strings.Join(failures, "\n"), "remote_gpu_complete p99_ms") {
		t.Fatalf("v12 stable regression failures=%v error=%v", failures, err)
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
	baseline := completeV14ComparableReport("memory")
	baseline.Persistence = client.PersistenceSummary{
		Snapshots: 10, P50MS: 1, P95MS: 1, P99MS: 1, MaxMS: 1,
	}
	current := completeV15ComparableReport("memory")
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

	failures, err := compareReportsWithScenarioUpgrade(baseline, current, 0.20, "14:15")
	if err != nil || len(failures) != 0 {
		t.Fatalf("explicit migration failures=%v err=%v", failures, err)
	}
}

func TestPerfcheckScenarioUpgradeKeepsAbsoluteAndSchemaGates(t *testing.T) {
	baseline := completeV14ComparableReport("memory")
	t.Run("absolute", func(t *testing.T) {
		current := completeV15ComparableReport("memory")
		phase := current.Phases["still"]
		phase.P99MS = 12
		phase.MaxMS = 12
		current.Phases["still"] = phase
		failures, err := compareReportsWithScenarioUpgrade(baseline, current, 0.20, "14:15")
		if err != nil || !strings.Contains(strings.Join(failures, "\n"), "still p99") {
			t.Fatalf("absolute migration failures=%v err=%v", failures, err)
		}
	})
	t.Run("schema", func(t *testing.T) {
		current := completeV15ComparableReport("memory")
		current.Multiplayer.RosterApply = client.LatencySummary{}
		if _, err := compareReportsWithScenarioUpgrade(baseline, current, 0.20, "14:15"); err == nil ||
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
			baseline := completeV14ComparableReport("memory")
			current := completeV15ComparableReport("memory")
			test.mutate(&current)

			failures, err := compareReportsWithScenarioUpgrade(baseline, current, 0.20, "14:15")
			if err != nil || !strings.Contains(strings.Join(failures, "\n"), test.want) {
				t.Fatalf("absolute producer gate failures=%v err=%v want=%q", failures, err, test.want)
			}
		})
	}
}

func TestPerfcheckScenarioUpgradeRejectsIncompleteV15Report(t *testing.T) {
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
			baseline := completeV14ComparableReport("memory")
			current := completeV15ComparableReport("memory")
			test.mutate(&current)

			if _, err := compareReportsWithScenarioUpgrade(baseline, current, 0.20, "14:15"); err == nil ||
				!strings.Contains(err.Error(), "current") || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("incomplete v15 error=%v want=%q", err, test.want)
			}
		})
	}
}

func TestComparisonSuccessMessageDescribesComparisonMode(t *testing.T) {
	if got := comparisonSuccessMessage(14, 15); got !=
		"场景迁移性能记录完成：报告完整、硬件一致，当前 v15" {
		t.Fatalf("migration message=%q", got)
	}
	if got := comparisonSuccessMessage(6, 6); got !=
		"同场景性能记录完成" {
		t.Fatalf("same-scenario message=%q", got)
	}
}

func TestPerformanceChangesProduceRecordsWithoutFailure(t *testing.T) {
	baseline := completeV14ComparableReport("memory")
	current := completeV14ComparableReport("memory")
	phase := current.Phases["still"]
	phase.FPS = 1
	phase.P99MS = 99
	phase.MaxMS = 99
	phase.PeakRSSBytes = 3 << 30
	current.Phases["still"] = phase
	current.Ticks.P99MS = 99
	current.Ticks.MaxMS = 99
	current.Multiplayer.OutboxHighWater = 999
	if records, err := compareReports(baseline, current, 0.20); err != nil || len(records) == 0 {
		t.Fatalf("性能退化应只产生记录: records=%v err=%v", records, err)
	}
}

func TestPerfcheckCLIPerformanceRecordsExitZero(t *testing.T) {
	baseline := completeV14ComparableReport("memory")
	current := completeV14ComparableReport("memory")
	phase := current.Phases["still"]
	phase.FPS = 1
	current.Phases["still"] = phase
	writeReport := func(name string, report client.PerfReport) string {
		path := filepath.Join(t.TempDir(), name)
		data, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	command := exec.Command("go", "run", ".",
		"-baseline", writeReport("baseline.json", baseline),
		"-current", writeReport("current.json", current),
	)
	var stdout strings.Builder
	var stderr strings.Builder
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("perfcheck exit=%v stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "性能记录") {
		t.Fatalf("perfcheck stdout=%q，缺少性能记录", stdout.String())
	}
}

func TestScenarioUpgradeStillRejectsIncompleteReport(t *testing.T) {
	baseline := completeV14ComparableReport("memory")
	current := completeV15ComparableReport("memory")
	current.Phases["still"] = client.PhaseSummary{}
	if _, err := compareReportsWithScenarioUpgrade(baseline, current, 0.20, "14:15"); err == nil {
		t.Fatal("不完整场景升级报告未被拒绝")
	}
}

func TestCrossTransportComparisonRequiresMatchingCommit(t *testing.T) {
	baseline := completeV14ComparableReport("memory")
	current := completeV14ComparableReport("tcp")
	current.GitCommit = "other-commit"
	if _, err := compareReports(baseline, current, 0.20); err == nil || !strings.Contains(err.Error(), "git_commit") {
		t.Fatalf("跨 transport commit 不一致 error=%v", err)
	}
}

func TestCrossTransportComparisonRequiresMatchingScenario(t *testing.T) {
	baseline := completeV14ComparableReport("memory")
	current := completeV15ComparableReport("tcp")
	if _, err := compareReportsWithScenarioUpgrade(baseline, current, 0.20, "14:15"); err == nil ||
		!strings.Contains(err.Error(), "scenario_version") {
		t.Fatalf("跨 transport scenario 不一致 error=%v", err)
	}
}

func TestPerfcheckRejectsDroppedSamples(t *testing.T) {
	report := completeV14ComparableReport("memory")
	report.Ticks.DroppedRingBufferSamples = 1
	if err := validateV6Report("current", report); err == nil || !strings.Contains(err.Error(), "dropped") {
		t.Fatalf("丢失环形缓冲样本 error=%v", err)
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
		// persistence 的尾分位数跨运行波动近两倍，已按实测豁免相对判定；
		// 中位数极稳定（1.04x），因此仍必须被判定。
		{name: "persistence p50", want: "persistence p50_ms", mutate: func(report *client.PerfReport) {
			report.Persistence.P50MS *= 1.201
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
		{name: "persistence 尾部大幅退化", want: "persistence p99_ms", mutate: func(report *client.PerfReport) {
			// 超过尾部固有波动的退化仍须失败；同时保持分位数单调。
			report.Persistence.P99MS += persistenceTailNoiseFloorMS * 2
			report.Persistence.MaxMS = report.Persistence.P99MS + 1
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
			// GPU 指标只有在批量分摊（v12 起）时才具备相对判定所需的分辨率。
			newReport := completeV6ComparableReport
			if test.name == "gpu" {
				newReport = completeV12ComparableReport
			}
			baseline := newReport("memory")
			baseline.Persistence = client.PersistenceSummary{
				Snapshots: 10, P50MS: 1, P95MS: 2, P99MS: 3, MaxMS: 4,
			}
			current := newReport("tcp")
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

// scenarioComparableReport 返回与给定场景版本一致的完整报告，
// 包括该场景要求的 remote_gpu_complete 样本数与批次数量。
func scenarioComparableReport(version int, transport string) client.PerfReport {
	switch version {
	case 15:
		return completeV15ComparableReport(transport)
	case 14:
		return completeV14ComparableReport(transport)
	case 13:
		return completeV13ComparableReport(transport)
	case 12:
		return completeV12ComparableReport(transport)
	case 11:
		return completeV11ComparableReport(transport)
	case 10:
		return completeV10ComparableReport(transport)
	case 9:
		return completeV9ComparableReport(transport)
	case 8:
		return completeV8ComparableReport(transport)
	default:
		report := completeV6ComparableReport(transport)
		report.ScenarioVersion = version
		return report
	}
}

func completeV15ComparableReport(transport string) client.PerfReport {
	report := completeV14ComparableReport(transport)
	report.ScenarioVersion = 15
	return report
}

func completeV14ComparableReport(transport string) client.PerfReport {
	report := completeV13ComparableReport(transport)
	report.ScenarioVersion = 14
	return report
}

func completeV13ComparableReport(transport string) client.PerfReport {
	report := completeV12ComparableReport(transport)
	report.ScenarioVersion = 13
	return report
}

func completeV12ComparableReport(transport string) client.PerfReport {
	report := completeV11ComparableReport(transport)
	report.ScenarioVersion = 12
	report.Multiplayer.RemoteGPUCompleteBatch = client.ScenarioV12GPUCompletionBatch
	report.Multiplayer.RemoteGPUComplete.Samples = client.ScenarioV12GPUCompletionSamples
	return report
}

func completeV11ComparableReport(transport string) client.PerfReport {
	report := completeV10ComparableReport(transport)
	report.ScenarioVersion = 11
	return report
}

func completeV10ComparableReport(transport string) client.PerfReport {
	report := completeV9ComparableReport(transport)
	report.ScenarioVersion = 10
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

func TestPerfcheckV12SameScenarioAndCrossTransportKeepExistingGates(t *testing.T) {
	// 同场景 v12 比较继续走既有稳定指标与绝对门禁。
	baseline := completeV12ComparableReport("memory")
	current := completeV12ComparableReport("memory")
	if failures, err := compareReports(baseline, current, 0.20); err != nil || len(failures) != 0 {
		t.Fatalf("v12 同场景比较 failures=%v err=%v", failures, err)
	}

	// 同硬件、同场景的 Memory→TCP 跨 transport 比较同样适用。
	tcp := completeV12ComparableReport("tcp")
	if failures, err := compareReports(baseline, tcp, 0.20); err != nil || len(failures) != 0 {
		t.Fatalf("v12 跨 transport 比较 failures=%v err=%v", failures, err)
	}

	// 绝对门禁不因场景升级而放宽。
	regressed := completeV12ComparableReport("memory")
	phase := regressed.Phases["flying"]
	phase.P99MS = 12
	phase.MaxMS = 12
	regressed.Phases["flying"] = phase
	failures, err := compareReports(baseline, regressed, 0.20)
	if err != nil || !strings.Contains(strings.Join(failures, "\n"), "flying p99") {
		t.Fatalf("v12 flying 绝对门禁 failures=%v err=%v", failures, err)
	}
}

func TestPerfcheckV13SameScenarioAndCrossTransportKeepExistingGates(t *testing.T) {
	// 同场景 v13 比较继续走既有稳定指标与绝对门禁。
	baseline := completeV13ComparableReport("memory")
	current := completeV13ComparableReport("memory")
	if failures, err := compareReports(baseline, current, 0.20); err != nil || len(failures) != 0 {
		t.Fatalf("v13 同场景比较 failures=%v err=%v", failures, err)
	}

	// 同硬件、同场景的 Memory→TCP 跨 transport 比较同样适用；
	// v13 沿用 v12 批量分摊定义，remote_gpu_complete 相对门禁必须仍然报告。
	tcp := completeV13ComparableReport("tcp")
	tcp.Multiplayer.RemoteGPUComplete.P99MS *= 1.201
	tcp.Multiplayer.RemoteGPUComplete.MaxMS = tcp.Multiplayer.RemoteGPUComplete.P99MS
	if failures, err := compareReports(baseline, tcp, 0.20); err != nil ||
		!strings.Contains(strings.Join(failures, "\n"), "remote_gpu_complete p99_ms") {
		t.Fatalf("v13 GPU 稳定回归 failures=%v err=%v", failures, err)
	}
	plain := completeV13ComparableReport("tcp")
	if failures, err := compareReports(baseline, plain, 0.20); err != nil || len(failures) != 0 {
		t.Fatalf("v13 跨 transport 比较 failures=%v err=%v", failures, err)
	}

	// 绝对门禁不因场景升级而放宽。
	regressed := completeV13ComparableReport("memory")
	phase := regressed.Phases["flying"]
	phase.P99MS = 12
	phase.MaxMS = 12
	regressed.Phases["flying"] = phase
	failures, err := compareReports(baseline, regressed, 0.20)
	if err != nil || !strings.Contains(strings.Join(failures, "\n"), "flying p99") {
		t.Fatalf("v13 flying 绝对门禁 failures=%v err=%v", failures, err)
	}
}

func TestPerfcheckV14SameScenarioAndCrossTransportKeepExistingGates(t *testing.T) {
	baseline := completeV14ComparableReport("memory")
	current := completeV14ComparableReport("memory")
	if failures, err := compareReports(baseline, current, 0.20); err != nil || len(failures) != 0 {
		t.Fatalf("v14 同场景比较 failures=%v err=%v", failures, err)
	}

	tcp := completeV14ComparableReport("tcp")
	tcp.Multiplayer.RemoteGPUComplete.P99MS *= 1.201
	tcp.Multiplayer.RemoteGPUComplete.MaxMS = tcp.Multiplayer.RemoteGPUComplete.P99MS
	if failures, err := compareReports(baseline, tcp, 0.20); err != nil ||
		!strings.Contains(strings.Join(failures, "\n"), "remote_gpu_complete p99_ms") {
		t.Fatalf("v14 GPU 稳定回归 failures=%v err=%v", failures, err)
	}
	plain := completeV14ComparableReport("tcp")
	if failures, err := compareReports(baseline, plain, 0.20); err != nil || len(failures) != 0 {
		t.Fatalf("v14 跨 transport 比较 failures=%v err=%v", failures, err)
	}

	regressed := completeV14ComparableReport("memory")
	phase := regressed.Phases["flying"]
	phase.P99MS = 12
	phase.MaxMS = 12
	regressed.Phases["flying"] = phase
	failures, err := compareReports(baseline, regressed, 0.20)
	if err != nil || !strings.Contains(strings.Join(failures, "\n"), "flying p99") {
		t.Fatalf("v14 flying 绝对门禁 failures=%v err=%v", failures, err)
	}
}

func TestPerfcheckV15SameCommitExplicitCrossTransportComparison(t *testing.T) {
	baseline := completeV15ComparableReport("memory")
	current := completeV15ComparableReport("tcp")
	if failures, err := compareReports(baseline, current, 0.20); err != nil || len(failures) != 0 {
		t.Fatalf("v15 同 commit 显式跨 transport 比较 failures=%v err=%v", failures, err)
	}
}

func TestPerfcheckHistoricalScenariosRemainReadable(t *testing.T) {
	// 历史报告按各自场景规则校验，不得被要求满足后续场景的新字段。
	for _, test := range []struct {
		version int
		report  client.PerfReport
	}{
		{6, completeV6ComparableReport("memory")},
		{7, completeV7ComparableReport("memory")},
		{8, completeV8ComparableReport("memory")},
		{9, completeV9ComparableReport("memory")},
		{10, completeV10ComparableReport("memory")},
		{11, completeV11ComparableReport("memory")},
		{12, completeV12ComparableReport("memory")},
		{13, completeV13ComparableReport("memory")},
		{14, completeV14ComparableReport("memory")},
		{15, completeV15ComparableReport("memory")},
	} {
		t.Run(fmt.Sprintf("v%d", test.version), func(t *testing.T) {
			if got := test.report.ScenarioVersion; got != test.version {
				t.Fatalf("scenario_version=%d，想要 %d", got, test.version)
			}
			if failures, err := compareReports(test.report, test.report, 0.20); err != nil || len(failures) != 0 {
				t.Fatalf("v%d 自比较 failures=%v err=%v", test.version, failures, err)
			}
		})
	}
}

func TestPerfcheckSkipsRelativeGateForQuantizedMetric(t *testing.T) {
	// 分辨率为 1.28ms 的指标：基线 1.30ms、当前 2.53ms 是跨越一个量化步长，
	// 不得报告相对回归；分辨率远细于阈值时同样幅度必须失败。
	const threshold = 0.20
	quantized := appendRegressionWithResolution(
		nil, "remote_gpu_complete", "p95_ms", 1.300, 2.527, threshold, 1.28,
	)
	if len(quantized) != 0 {
		t.Fatalf("量化指标报告了相对回归：%v", quantized)
	}

	fine := appendRegressionWithResolution(
		nil, "remote_gpu_complete", "p95_ms", 0.0613, 0.1192, threshold, 1.28/1024,
	)
	if len(fine) != 1 {
		t.Fatalf("高分辨率指标未报告相对回归：%v", fine)
	}
	if !strings.Contains(fine[0], "相对") {
		t.Fatalf("失败信息未指明判定类型：%q", fine[0])
	}
}

func TestPerfcheckQuantizedMetricKeepsCompletenessGate(t *testing.T) {
	// 跳过相对判定不得放松完整性门禁：样本不足仍必须失败。
	report := completeV11ComparableReport("memory")
	report.Multiplayer.RemoteGPUComplete.Samples = 1
	if err := validateV6Report("current", report); err == nil ||
		!strings.Contains(err.Error(), "remote_gpu_complete") {
		t.Fatalf("样本不足未被拒绝：%v", err)
	}
}

func TestPerfcheckLatencyNoiseFloorSuppressesSubMicrosecondJitter(t *testing.T) {
	// 实测的跨运行抖动：这些微秒级墙钟指标的相对变化远超 20%，
	// 但绝对增量只有 1-30µs，属于调度抖动而非性能退化。
	for _, test := range []struct {
		name              string
		baseline, current float64
	}{
		{"remote_state_encode", 0.007, 0.008},
		{"avatar_submit", 0.014, 0.021},
		{"remote_gpu_complete p95", 0.120, 0.150},
		{"remote_gpu_complete p99", 0.128, 0.156},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := appendRegressionWithResolution(
				nil, test.name, "p99_ms", test.baseline, test.current, 0.20, latencyNoiseFloorMS,
			)
			if len(got) != 0 {
				t.Fatalf("噪声级变化被判定为回归：%v", got)
			}
		})
	}
}

func TestPerfcheckLatencyNoiseFloorStillCatchesRealRegression(t *testing.T) {
	// 超过噪声地板的退化必须照常失败，否则门禁形同虚设。
	for _, test := range []struct {
		name              string
		baseline, current float64
		wantFail          bool
	}{
		{name: "明确在地板之内", baseline: 0.120, current: 0.120 + latencyNoiseFloorMS*0.8, wantFail: false},
		{name: "越过地板且超阈值", baseline: 0.120, current: 0.120 + latencyNoiseFloorMS*1.5, wantFail: true},
		{name: "远超地板", baseline: 0.120, current: 0.500, wantFail: true},
		{name: "越过地板但未超阈值", baseline: 10.0, current: 10.06, wantFail: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := appendRegressionWithResolution(
				nil, "metric", "p99_ms", test.baseline, test.current, 0.20, latencyNoiseFloorMS,
			)
			if failed := len(got) != 0; failed != test.wantFail {
				t.Fatalf("判定 = %v，想要 %v（failures=%v）", failed, test.wantFail, got)
			}
		})
	}
}

func TestPerfcheckPersistenceTailIsExemptButMedianIsNot(t *testing.T) {
	// 实测 11 次运行：persistence p50 的最大/最小为 1.04x，而 p95/p99 达 1.97x。
	// 尾分位数受页缓存与后台 flush 影响，跨运行波动本就接近两倍，
	// 20% 相对判定测的是磁盘状态而非代码退化；中位数则必须继续受判定。
	floors := persistenceFloors()

	tail := appendStableSummaryRegressions(
		nil, "persistence",
		4.1, 9.991, 12.0,
		4.1, 12.078, 16.5,
		0.20, floors,
	)
	if len(tail) != 0 {
		t.Fatalf("尾分位数的固有抖动被判定为回归：%v", tail)
	}

	median := appendStableSummaryRegressions(
		nil, "persistence",
		4.1, 9.991, 12.0,
		5.5, 9.991, 12.0,
		0.20, floors,
	)
	if len(median) != 1 || !strings.Contains(median[0], "p50_ms") {
		t.Fatalf("中位数退化未被判定：%v", median)
	}

	// 尾部的真实大幅退化仍须失败。
	severe := appendStableSummaryRegressions(
		nil, "persistence",
		4.1, 9.991, 12.0,
		4.1, 30.0, 40.0,
		0.20, floors,
	)
	if len(severe) == 0 {
		t.Fatal("尾部大幅退化未被判定")
	}
}
