//go:build darwin

package main

import (
	"strings"
	"sync"
	"testing"
	"time"

	"minecraft-go/internal/client"
)

func TestBenchmarkScenarioVersionIncludesPersistencePath(t *testing.T) {
	if scenarioVersion != 4 {
		t.Fatalf("scenarioVersion=%d，想要包含存档快照/确认路径的 v4", scenarioVersion)
	}
}

func TestValidateBenchmarkReportRequiresPersistenceSamples(t *testing.T) {
	report := client.PerfReport{
		Phases: map[string]client.PhaseSummary{
			"still":  {FPS: 100, P99MS: 11, PeakRSSBytes: 1},
			"flying": {FPS: 100, P99MS: 11, PeakRSSBytes: 1},
		},
		Ticks: client.PhaseSummary{P99MS: 9, MaxMS: 49},
	}
	err := validateBenchmarkReport(report)
	if err == nil || !strings.Contains(err.Error(), "persistence snapshots=0") {
		t.Fatalf("zero persistence validation error=%v", err)
	}
	report.Persistence.Snapshots = 1
	if err := validateBenchmarkReport(report); err != nil {
		t.Fatalf("one persistence sample unexpectedly rejected: %v", err)
	}
}

func TestSaveRecorderPercentilesAndReset(t *testing.T) {
	recorder := newSaveRecorder(100)
	for millisecond := 1; millisecond <= 100; millisecond++ {
		recorder.add(time.Duration(millisecond) * time.Millisecond)
	}
	got := recorder.summary()
	if got.Snapshots != 100 || got.P50MS != 50 || got.P95MS != 95 ||
		got.P99MS != 99 || got.MaxMS != 100 {
		t.Fatalf("save summary=%+v，想要 100 samples 与 50/95/99/100ms", got)
	}

	recorder.reset()
	if got := recorder.summary(); got.Snapshots != 0 || got.P50MS != 0 ||
		got.P95MS != 0 || got.P99MS != 0 || got.MaxMS != 0 {
		t.Fatalf("reset 后 save summary=%+v，想要全零", got)
	}
}

func TestSaveRecorderIsConcurrencySafeAndBounded(t *testing.T) {
	const workers = 8
	const samplesPerWorker = 1000
	recorder := newSaveRecorder(workers * samplesPerWorker)
	var group sync.WaitGroup
	for worker := range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			for sample := range samplesPerWorker {
				recorder.add(time.Duration(worker+sample+1) * time.Microsecond)
			}
		}()
	}
	group.Wait()
	got := recorder.summary()
	if got.Snapshots != workers*samplesPerWorker {
		t.Fatalf("concurrent snapshots=%d，想要 %d", got.Snapshots, workers*samplesPerWorker)
	}
	if got.P50MS <= 0 || got.P95MS < got.P50MS || got.P99MS < got.P95MS ||
		got.MaxMS < got.P99MS {
		t.Fatalf("concurrent percentiles 非单调: %+v", got)
	}

	bounded := newSaveRecorder(2)
	bounded.add(time.Millisecond)
	bounded.add(2 * time.Millisecond)
	bounded.add(3 * time.Millisecond)
	if got := bounded.summary(); got.Snapshots != 2 || got.P50MS != 2 || got.MaxMS != 3 {
		t.Fatalf("bounded recorder=%+v，想要仅保留最新 2 个样本", got)
	}
}
