package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/channing771/mornlea/internal/client"
)

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

func TestScenarioUpgradeStillRejectsIncompleteReport(t *testing.T) {
	baseline := completeV14ComparableReport("memory")
	current := completeV15ComparableReport("memory")
	current.Phases["still"] = client.PhaseSummary{}
	if _, err := compareReportsWithScenarioUpgrade(baseline, current, 0.20, "14:15"); err == nil {
		t.Fatal("不完整场景升级报告未被拒绝")
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
