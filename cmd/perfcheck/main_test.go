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
