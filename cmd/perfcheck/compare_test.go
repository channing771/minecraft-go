package main

import (
	"strings"
	"testing"

	"github.com/channing771/mornlea/internal/client"
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

func TestPerfcheckRejectsDroppedSamples(t *testing.T) {
	report := completeV14ComparableReport("memory")
	report.Ticks.DroppedRingBufferSamples = 1
	if err := validateV6Report("current", report); err == nil || !strings.Contains(err.Error(), "dropped") {
		t.Fatalf("丢失环形缓冲样本 error=%v", err)
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
