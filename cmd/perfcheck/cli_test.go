package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/channing771/mornlea/internal/client"
)

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
