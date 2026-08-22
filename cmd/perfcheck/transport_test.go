package main

import (
	"strings"
	"testing"

	"github.com/channing771/mornlea/internal/client"
)

func TestCrossTransportComparisonRequiresMatchingCommit(t *testing.T) {
	baseline := completeV14ComparableReport("memory")
	current := completeV14ComparableReport("tcp")
	current.GitCommit = "other-commit"
	if _, err := compareReports(baseline, current, 0.20); err == nil || !strings.Contains(err.Error(), "git_commit") {
		t.Fatalf("跨 transport commit 不一致 error=%v", err)
	}
}

func TestCrossTransportComparisonRequiresMatchingScenario(t *testing.T) {
	baseline := completeV17ComparableReport("memory")
	current := completeV18ComparableReport("tcp")
	if _, err := compareReportsWithScenarioUpgrade(baseline, current, 0.20, "17:18"); err == nil ||
		!strings.Contains(err.Error(), "scenario_version") {
		t.Fatalf("跨 transport scenario 不一致 error=%v", err)
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
