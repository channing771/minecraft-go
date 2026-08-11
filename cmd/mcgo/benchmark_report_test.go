//go:build darwin

package main

import (
	"strings"
	"testing"

	"minecraft-go/internal/client"
)

func TestPerformanceThresholdsRejectTickP99AtTenMilliseconds(t *testing.T) {
	report := completeBenchmarkReport()
	report.Ticks.P99MS = 9.999
	if err := validateBenchmarkReport(report); err != nil {
		t.Fatalf("9.999ms tick p99 rejected: %v", err)
	}
	report.Ticks.P99MS = 10
	if err := validateBenchmarkReport(report); err != nil {
		t.Fatalf("10ms tick p99 should only be recorded: %v", err)
	}
}

func TestWriteBenchmarkReportRecordsPerformanceOutsideThresholds(t *testing.T) {
	report := completeBenchmarkReport()
	for _, name := range []string{"still", "flying"} {
		phase := report.Phases[name]
		phase.FPS = 1
		phase.P99MS = 99
		phase.MaxMS = 99
		phase.PeakRSSBytes = 3 << 30
		report.Phases[name] = phase
	}
	report.Ticks.P99MS = 99
	report.Ticks.MaxMS = 99
	report.Protocol.EncodeP99MS = 9
	report.Protocol.DecodeP99MS = 9
	report.PlayerPersistence.P99MS = 99
	report.PlayerPersistence.MaxMS = 99
	report.Multiplayer.OutboxHighWater = 999
	report.Multiplayer.PlayerJobsHighWater = 999
	report.Multiplayer.PlayerDoneHighWater = 999
	report.Multiplayer.PeakRSSBytes = 3 << 30
	path := t.TempDir() + "/report.json"
	if err := writeBenchmarkReport(path, report); err != nil {
		t.Fatalf("性能数值越界的完整报告未写出: %v", err)
	}
	if records := benchmarkPerformanceRecords(report); len(records) == 0 {
		t.Fatal("越界性能数值未留下记录")
	}
}

func TestValidateBenchmarkReportStillRejectsIncompleteSamples(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*client.PerfReport)
	}{
		{name: "missing phase", mutate: func(report *client.PerfReport) { delete(report.Phases, "still") }},
		{name: "phase samples", mutate: func(report *client.PerfReport) {
			phase := report.Phases["flying"]
			phase.Frames = 0
			report.Phases["flying"] = phase
		}},
		{name: "provenance", mutate: func(report *client.PerfReport) { report.Hardware = "  " }},
		{name: "rss", mutate: func(report *client.PerfReport) { report.Phases["still"] = client.PhaseSummary{} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			report := completeBenchmarkReport()
			test.mutate(&report)
			if err := validateBenchmarkReport(report); err == nil {
				t.Fatal("不完整报告未被拒绝")
			}
		})
	}
}

func TestValidateBenchmarkReportRejectsDroppedSamples(t *testing.T) {
	for _, name := range []string{"still", "flying", "ticks"} {
		t.Run(name, func(t *testing.T) {
			report := completeBenchmarkReport()
			if name == "ticks" {
				report.Ticks.DroppedRingBufferSamples = 1
			} else {
				phase := report.Phases[name]
				phase.DroppedRingBufferSamples = 1
				report.Phases[name] = phase
			}
			if err := validateBenchmarkReport(report); err == nil {
				t.Fatal("丢失环形缓冲样本未被拒绝")
			}
		})
	}
}

func TestScenarioV8BenchmarkReportRequires2048GPUCompletionSamples(t *testing.T) {
	report := completeBenchmarkReport()
	report.ScenarioVersion = 8
	report.Multiplayer.RemoteGPUComplete.Samples = 2047
	if err := validateBenchmarkReport(report); err == nil ||
		!strings.Contains(err.Error(), "remote_gpu_complete") {
		t.Fatalf("2047 GPU samples error=%v", err)
	}
	report.Multiplayer.RemoteGPUComplete.Samples = 2048
	if err := validateBenchmarkReport(report); err != nil {
		t.Fatalf("2048 GPU samples rejected: %v", err)
	}
	report.ScenarioVersion = 7
	report.Multiplayer.RemoteGPUComplete.Samples = 256
	if err := validateBenchmarkReport(report); err != nil {
		t.Fatalf("v7 256 GPU samples rejected: %v", err)
	}
}

func TestScenarioV13BenchmarkReportReusesV12GPUCompletionDefinition(t *testing.T) {
	report := completeBenchmarkReport()
	report.ScenarioVersion = 13
	report.Multiplayer.RemoteGPUComplete.Samples = client.ScenarioV12GPUCompletionSamples - 1
	if err := validateBenchmarkReport(report); err == nil ||
		!strings.Contains(err.Error(), "remote_gpu_complete") {
		t.Fatalf("v13 低于批量分摊样本数未被拒绝: %v", err)
	}
	report.Multiplayer.RemoteGPUComplete.Samples = client.ScenarioV12GPUCompletionSamples
	if err := validateBenchmarkReport(report); err != nil {
		t.Fatalf("v13 批量分摊样本数被拒绝: %v", err)
	}
}
