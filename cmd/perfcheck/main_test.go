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
