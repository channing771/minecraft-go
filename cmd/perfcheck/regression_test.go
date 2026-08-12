package main

import (
	"strings"
	"testing"

	"minecraft-go/internal/client"
)

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
