package main

import "github.com/channing771/mornlea/internal/client"

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
