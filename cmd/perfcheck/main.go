package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"minecraft-go/internal/client"
)

const m3bLatencyNoiseFloorMS = 0.01

func main() {
	baselinePath := flag.String("baseline", "", "基线 JSON")
	currentPath := flag.String("current", "", "当前 JSON")
	maxRegression := flag.Float64("max-regression", 0.20, "允许的最大相对退化")
	allowScenarioUpgrade := flag.String("allow-scenario-upgrade", "", "只允许显式的 5:6 或 6:7 场景迁移")
	flag.Parse()

	if *baselinePath == "" || *currentPath == "" {
		fail("-baseline 与 -current 都必须提供")
	}
	baseline := readReport(*baselinePath)
	current := readReport(*currentPath)
	failures, err := compareReportsWithScenarioUpgrade(
		baseline, current, *maxRegression, *allowScenarioUpgrade,
	)
	if err != nil {
		fail("%v", err)
	}
	for _, failure := range failures {
		fmt.Fprintln(os.Stderr, failure)
	}
	if len(failures) != 0 {
		os.Exit(1)
	}
	fmt.Println(comparisonSuccessMessage(baseline.ScenarioVersion, current.ScenarioVersion))
}

func comparisonSuccessMessage(baselineVersion, currentVersion int) string {
	if baselineVersion != currentVersion {
		return fmt.Sprintf("场景迁移验证通过：报告完整、硬件一致且当前 v%d 绝对门禁通过", currentVersion)
	}
	return "同场景性能比较通过：适用的稳定指标退化均未超过阈值且绝对门禁通过"
}

func compareReports(
	baseline client.PerfReport,
	current client.PerfReport,
	maxRegression float64,
) ([]string, error) {
	return compareReportsWithScenarioUpgrade(baseline, current, maxRegression, "")
}

func compareReportsWithScenarioUpgrade(
	baseline client.PerfReport,
	current client.PerfReport,
	maxRegression float64,
	allowScenarioUpgrade string,
) ([]string, error) {
	scenarioUpgrade := baseline.ScenarioVersion != current.ScenarioVersion
	allowedScenarioUpgrade := baseline.ScenarioVersion == 5 && current.ScenarioVersion == 6 && allowScenarioUpgrade == "5:6" ||
		baseline.ScenarioVersion == 6 && current.ScenarioVersion == 7 && allowScenarioUpgrade == "6:7"
	if scenarioUpgrade && !allowedScenarioUpgrade {
		return nil, fmt.Errorf(
			"scenario_version 不同：基线=%d 当前=%d",
			baseline.ScenarioVersion,
			current.ScenarioVersion,
		)
	}
	if baseline.ScenarioVersion >= 5 {
		if err := validateV5Report("baseline", baseline); err != nil {
			return nil, err
		}
		if err := validateV5Report("current", current); err != nil {
			return nil, err
		}
	}
	if current.ScenarioVersion >= 5 {
		if err := validateV5Report("current", current); err != nil {
			return nil, err
		}
	}
	if baseline.ScenarioVersion >= 6 {
		if err := validateV6Report("baseline", baseline); err != nil {
			return nil, err
		}
	}
	if current.ScenarioVersion >= 6 {
		if err := validateV6Report("current", current); err != nil {
			return nil, err
		}
	}
	if err := validateReportProvenance("baseline", baseline); err != nil {
		return nil, err
	}
	if err := validateReportProvenance("current", current); err != nil {
		return nil, err
	}
	if baseline.Hardware != current.Hardware {
		return nil, fmt.Errorf(
			"硬件标识不同，拒绝比较：基线=%q 当前=%q",
			baseline.Hardware,
			current.Hardware,
		)
	}

	var failures []string
	if current.ScenarioVersion >= 6 {
		failures = appendV6AbsoluteFailures(failures, current)
	}
	if scenarioUpgrade {
		return failures, nil
	}
	stablePair := baseline.ScenarioVersion >= 6 && baseline.ScenarioVersion == current.ScenarioVersion
	crossTransportStable := stablePair && baseline.Transport != current.Transport
	for _, metric := range []struct {
		name              string
		baseline, current float64
	}{
		{name: "load_seconds", baseline: baseline.LoadSeconds, current: current.LoadSeconds},
		{name: "snapshot_seconds", baseline: baseline.SnapshotSeconds, current: current.SnapshotSeconds},
	} {
		failures = appendRegression(failures, "", metric.name, metric.baseline, metric.current, maxRegression)
	}
	if stablePair {
		if !crossTransportStable {
			failures = appendStableSummaryRegressions(
				failures,
				"ticks",
				baseline.Ticks.P50MS,
				baseline.Ticks.P95MS,
				baseline.Ticks.P99MS,
				current.Ticks.P50MS,
				current.Ticks.P95MS,
				current.Ticks.P99MS,
				maxRegression,
			)
		}
	} else {
		failures = appendSummaryRegressions(
			failures,
			"ticks",
			baseline.Ticks.P50MS,
			baseline.Ticks.P95MS,
			baseline.Ticks.P99MS,
			baseline.Ticks.MaxMS,
			current.Ticks.P50MS,
			current.Ticks.P95MS,
			current.Ticks.P99MS,
			current.Ticks.MaxMS,
			maxRegression,
		)
	}
	if baseline.Persistence.Snapshots > 0 && current.Persistence.Snapshots > 0 {
		if stablePair {
			failures = appendStableSummaryRegressions(
				failures,
				"persistence",
				baseline.Persistence.P50MS,
				baseline.Persistence.P95MS,
				baseline.Persistence.P99MS,
				current.Persistence.P50MS,
				current.Persistence.P95MS,
				current.Persistence.P99MS,
				maxRegression,
			)
		} else {
			failures = appendSummaryRegressions(
				failures,
				"persistence",
				baseline.Persistence.P50MS,
				baseline.Persistence.P95MS,
				baseline.Persistence.P99MS,
				baseline.Persistence.MaxMS,
				current.Persistence.P50MS,
				current.Persistence.P95MS,
				current.Persistence.P99MS,
				current.Persistence.MaxMS,
				maxRegression,
			)
		}
	}
	if baseline.Protocol.EncodeP99MS >= m3bLatencyNoiseFloorMS && current.Protocol.EncodeP99MS > 0 {
		failures = appendRegression(
			failures, "protocol", "encode_p99_ms",
			baseline.Protocol.EncodeP99MS, current.Protocol.EncodeP99MS, maxRegression,
		)
	}
	if baseline.Protocol.DecodeP99MS >= m3bLatencyNoiseFloorMS && current.Protocol.DecodeP99MS > 0 {
		failures = appendRegression(
			failures, "protocol", "decode_p99_ms",
			baseline.Protocol.DecodeP99MS, current.Protocol.DecodeP99MS, maxRegression,
		)
	}
	if baseline.Protocol.Bytes > 0 && current.Protocol.Bytes > 0 {
		failures = appendRegression(
			failures, "protocol", "bytes",
			float64(baseline.Protocol.Bytes), float64(current.Protocol.Bytes), maxRegression,
		)
	}
	if baseline.PlayerPersistence.Snapshots > 0 && current.PlayerPersistence.Snapshots > 0 {
		if stablePair {
			failures = appendM3BStableLatencyRegressions(
				failures,
				"player_persistence",
				baseline.PlayerPersistence.P50MS,
				baseline.PlayerPersistence.P95MS,
				baseline.PlayerPersistence.P99MS,
				current.PlayerPersistence.P50MS,
				current.PlayerPersistence.P95MS,
				current.PlayerPersistence.P99MS,
				maxRegression,
			)
		} else {
			failures = appendM3BLatencyRegressions(
				failures,
				"player_persistence",
				baseline.PlayerPersistence.P50MS,
				baseline.PlayerPersistence.P95MS,
				baseline.PlayerPersistence.P99MS,
				baseline.PlayerPersistence.MaxMS,
				current.PlayerPersistence.P50MS,
				current.PlayerPersistence.P95MS,
				current.PlayerPersistence.P99MS,
				current.PlayerPersistence.MaxMS,
				maxRegression,
			)
		}
	}
	if stablePair {
		failures = appendV6MultiplayerRegressions(
			failures,
			baseline.Multiplayer,
			current.Multiplayer,
			maxRegression,
			!crossTransportStable,
		)
	}
	phaseNames := make([]string, 0, len(baseline.Phases))
	for name := range baseline.Phases {
		phaseNames = append(phaseNames, name)
	}
	sort.Strings(phaseNames)
	for _, name := range phaseNames {
		basePhase := baseline.Phases[name]
		currentPhase, ok := current.Phases[name]
		if !ok {
			failures = append(failures, fmt.Sprintf("当前报告缺少阶段 %q", name))
			continue
		}
		if stablePair {
			failures = appendStableSummaryRegressions(
				failures,
				name,
				basePhase.P50MS,
				basePhase.P95MS,
				basePhase.P99MS,
				currentPhase.P50MS,
				currentPhase.P95MS,
				currentPhase.P99MS,
				maxRegression,
			)
		} else {
			failures = appendSummaryRegressions(
				failures,
				name,
				basePhase.P50MS,
				basePhase.P95MS,
				basePhase.P99MS,
				basePhase.MaxMS,
				currentPhase.P50MS,
				currentPhase.P95MS,
				currentPhase.P99MS,
				currentPhase.MaxMS,
				maxRegression,
			)
		}
		failures = appendRegression(
			failures,
			name,
			"peak_rss_bytes",
			float64(basePhase.PeakRSSBytes),
			float64(currentPhase.PeakRSSBytes),
			maxRegression,
		)
		if basePhase.FPS > 0 && currentPhase.FPS < basePhase.FPS*(1-maxRegression) {
			failures = append(failures, fmt.Sprintf(
				"%s fps 退化 %.1f%%：基线=%.3f 当前=%.3f",
				name,
				(1-currentPhase.FPS/basePhase.FPS)*100,
				basePhase.FPS,
				currentPhase.FPS,
			))
		}
	}
	return failures, nil
}

func validateReportProvenance(label string, report client.PerfReport) error {
	for _, field := range []struct {
		name, value string
	}{
		{name: "hardware", value: report.Hardware},
		{name: "os", value: report.OS},
		{name: "go_version", value: report.GoVersion},
		{name: "git_commit", value: report.GitCommit},
		{name: "framebuffer", value: report.Framebuffer},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%s provenance %s 为空", label, field.name)
		}
	}
	return nil
}

func appendV6AbsoluteFailures(failures []string, report client.PerfReport) []string {
	for _, name := range []string{"still", "flying"} {
		phase, ok := report.Phases[name]
		if !ok {
			failures = append(failures, fmt.Sprintf("当前 v6 报告缺少阶段 %q", name))
			continue
		}
		if phase.FPS < 100 {
			failures = append(failures, fmt.Sprintf("%s fps %.1f < 100", name, phase.FPS))
		}
		if phase.P99MS >= 12 {
			failures = append(failures, fmt.Sprintf("%s p99 %.3f ms >= 12 ms", name, phase.P99MS))
		}
		if phase.PeakRSSBytes >= 2<<30 {
			failures = append(failures, fmt.Sprintf("%s peak RSS %d >= 2GiB", name, phase.PeakRSSBytes))
		}
	}
	if report.Ticks.P99MS >= 10 {
		failures = append(failures, fmt.Sprintf("tick p99 %.3f ms >= 10 ms", report.Ticks.P99MS))
	}
	if report.Ticks.MaxMS >= 50 {
		failures = append(failures, fmt.Sprintf("tick max %.3f ms >= 50 ms", report.Ticks.MaxMS))
	}
	if report.Protocol.EncodeP99MS >= 1 {
		failures = append(failures, fmt.Sprintf(
			"protocol encode p99 %.3f ms >= 1 ms", report.Protocol.EncodeP99MS,
		))
	}
	if report.Protocol.DecodeP99MS >= 1 {
		failures = append(failures, fmt.Sprintf(
			"protocol decode p99 %.3f ms >= 1 ms", report.Protocol.DecodeP99MS,
		))
	}
	if report.PlayerPersistence.P99MS >= 5 {
		failures = append(failures, fmt.Sprintf(
			"player persistence p99 %.3f ms >= 5 ms", report.PlayerPersistence.P99MS,
		))
	}
	if report.PlayerPersistence.MaxMS >= 20 {
		failures = append(failures, fmt.Sprintf(
			"player persistence max %.3f ms >= 20 ms", report.PlayerPersistence.MaxMS,
		))
	}
	multiplayer := report.Multiplayer
	if multiplayer.PeakRSSBytes >= 2<<30 {
		failures = append(failures, fmt.Sprintf("multiplayer peak RSS %d >= 2GiB", multiplayer.PeakRSSBytes))
	}
	if multiplayer.OutboxHighWater > 512 {
		failures = append(failures, fmt.Sprintf("outbox high-water %d > 512", multiplayer.OutboxHighWater))
	}
	if multiplayer.PlayerJobsHighWater > 16 {
		failures = append(failures, fmt.Sprintf("player jobs high-water %d > 16", multiplayer.PlayerJobsHighWater))
	}
	if multiplayer.PlayerDoneHighWater > 2 {
		failures = append(failures, fmt.Sprintf("player done high-water %d > 2", multiplayer.PlayerDoneHighWater))
	}
	return failures
}

func validateV6Report(label string, report client.PerfReport) error {
	if report.LoadSeconds <= 0 {
		return fmt.Errorf("%s v6 load_seconds 必须大于零: %f", label, report.LoadSeconds)
	}
	if report.SnapshotSeconds <= 0 {
		return fmt.Errorf("%s v6 snapshot_seconds 必须大于零: %f", label, report.SnapshotSeconds)
	}
	for _, name := range []string{"still", "flying"} {
		phase, ok := report.Phases[name]
		if !ok {
			return fmt.Errorf("%s v6 缺少 %s 阶段", label, name)
		}
		if phase.Frames <= 0 || phase.FPS <= 0 || phase.P50MS <= 0 || phase.P95MS <= 0 ||
			phase.P99MS <= 0 || phase.MaxMS <= 0 || phase.PeakRSSBytes == 0 {
			return fmt.Errorf("%s v6 %s 阶段指标不完整: %+v", label, name, phase)
		}
		if phase.P50MS > phase.P95MS || phase.P95MS > phase.P99MS || phase.P99MS > phase.MaxMS {
			return fmt.Errorf("%s v6 %s 阶段分位数非单调: %+v", label, name, phase)
		}
	}
	if len(report.Phases) != 2 {
		return fmt.Errorf("%s v6 phases 必须精确包含 still/flying: %v", label, report.Phases)
	}
	if report.Ticks.Frames != 200 {
		return fmt.Errorf("%s v6 ticks frames 必须为 200: %d", label, report.Ticks.Frames)
	}
	if report.Ticks.FPS != 0 {
		return fmt.Errorf("%s v6 ticks fps 必须为零: %f", label, report.Ticks.FPS)
	}
	if report.Ticks.P50MS <= 0 || report.Ticks.P95MS <= 0 || report.Ticks.P99MS <= 0 ||
		report.Ticks.MaxMS <= 0 {
		return fmt.Errorf("%s v6 ticks 指标不完整: %+v", label, report.Ticks)
	}
	if report.Ticks.P50MS > report.Ticks.P95MS || report.Ticks.P95MS > report.Ticks.P99MS ||
		report.Ticks.P99MS > report.Ticks.MaxMS {
		return fmt.Errorf("%s v6 ticks 分位数非单调: %+v", label, report.Ticks)
	}
	persistence := report.Persistence
	if persistence.Snapshots <= 0 || persistence.P50MS <= 0 || persistence.P95MS <= 0 ||
		persistence.P99MS <= 0 || persistence.MaxMS <= 0 {
		return fmt.Errorf("%s v6 persistence 指标不完整: %+v", label, persistence)
	}
	if persistence.P50MS > persistence.P95MS || persistence.P95MS > persistence.P99MS ||
		persistence.P99MS > persistence.MaxMS {
		return fmt.Errorf("%s v6 persistence 分位数非单调: %+v", label, persistence)
	}
	if report.Multiplayer.InterestDiff.Samples != 1600 {
		return fmt.Errorf(
			"%s v6 interest_diff samples 必须为 1600: %d",
			label,
			report.Multiplayer.InterestDiff.Samples,
		)
	}
	remoteGPUCompletionSamples := 256
	if report.ScenarioVersion >= 8 {
		remoteGPUCompletionSamples = client.ScenarioV8GPUCompletionSamples
	}
	latencies := []struct {
		name       string
		summary    client.LatencySummary
		minSamples int
	}{
		{name: "remote_state_encode", summary: report.Multiplayer.RemoteStateEncode, minSamples: 256},
		{name: "remote_state_decode", summary: report.Multiplayer.RemoteStateDecode, minSamples: 256},
		{name: "interest_diff", summary: report.Multiplayer.InterestDiff, minSamples: 1600},
		{name: "roster_apply", summary: report.Multiplayer.RosterApply, minSamples: 256},
		{name: "interpolation", summary: report.Multiplayer.Interpolation, minSamples: 256},
		{name: "avatar_submit", summary: report.Multiplayer.AvatarSubmit, minSamples: 256},
		{name: "name_tag_submit", summary: report.Multiplayer.NameTagSubmit, minSamples: 256},
		{name: "remote_gpu_complete", summary: report.Multiplayer.RemoteGPUComplete, minSamples: remoteGPUCompletionSamples},
	}
	for _, latency := range latencies {
		value := latency.summary
		if value.Samples < latency.minSamples || value.P50MS <= 0 || value.P95MS <= 0 ||
			value.P99MS <= 0 || value.MaxMS <= 0 {
			return fmt.Errorf("%s v6 %s 指标不完整或样本过低: %+v", label, latency.name, value)
		}
		if value.P50MS > value.P95MS || value.P95MS > value.P99MS || value.P99MS > value.MaxMS {
			return fmt.Errorf("%s v6 %s 分位数非单调: %+v", label, latency.name, value)
		}
	}
	multiplayer := report.Multiplayer
	if multiplayer.ServerOutboundBytes == 0 || multiplayer.OutboxHighWater < 0 ||
		multiplayer.PlayerJobsHighWater < 0 || multiplayer.PlayerDoneHighWater < 0 ||
		multiplayer.PeakRSSBytes == 0 {
		return fmt.Errorf("%s v6 multiplayer 标量指标不完整: %+v", label, multiplayer)
	}
	return nil
}

func appendV6MultiplayerRegressions(
	failures []string,
	baseline client.MultiplayerSummary,
	current client.MultiplayerSummary,
	threshold float64,
	includeServerProbe bool,
) []string {
	latencies := []struct {
		name              string
		baseline, current client.LatencySummary
	}{
		{name: "remote_state_encode", baseline: baseline.RemoteStateEncode, current: current.RemoteStateEncode},
		{name: "remote_state_decode", baseline: baseline.RemoteStateDecode, current: current.RemoteStateDecode},
		{name: "roster_apply", baseline: baseline.RosterApply, current: current.RosterApply},
		{name: "interpolation", baseline: baseline.Interpolation, current: current.Interpolation},
		{name: "avatar_submit", baseline: baseline.AvatarSubmit, current: current.AvatarSubmit},
		{name: "name_tag_submit", baseline: baseline.NameTagSubmit, current: current.NameTagSubmit},
		{name: "remote_gpu_complete", baseline: baseline.RemoteGPUComplete, current: current.RemoteGPUComplete},
	}
	if includeServerProbe {
		latencies = append(latencies, struct {
			name              string
			baseline, current client.LatencySummary
		}{name: "interest_diff", baseline: baseline.InterestDiff, current: current.InterestDiff})
	}
	for _, latency := range latencies {
		failures = appendStableSummaryRegressions(
			failures, latency.name,
			latency.baseline.P50MS, latency.baseline.P95MS, latency.baseline.P99MS,
			latency.current.P50MS, latency.current.P95MS, latency.current.P99MS,
			threshold,
		)
	}
	if includeServerProbe {
		failures = appendRegression(
			failures, "multiplayer", "server_outbound_bytes",
			float64(baseline.ServerOutboundBytes), float64(current.ServerOutboundBytes), threshold,
		)
		failures = appendRegression(
			failures, "multiplayer", "peak_rss_bytes",
			float64(baseline.PeakRSSBytes), float64(current.PeakRSSBytes), threshold,
		)
	}
	return failures
}

func validateV5Report(label string, report client.PerfReport) error {
	if report.Transport != "memory" && report.Transport != "tcp" {
		return fmt.Errorf("%s v5 transport 无效: %q", label, report.Transport)
	}
	if report.Protocol.EncodeP99MS <= 0 || report.Protocol.DecodeP99MS <= 0 || report.Protocol.Bytes == 0 {
		return fmt.Errorf("%s v5 protocol 指标不完整: %+v", label, report.Protocol)
	}
	player := report.PlayerPersistence
	if player.Snapshots <= 0 || player.P50MS <= 0 || player.P95MS <= 0 ||
		player.P99MS <= 0 || player.MaxMS <= 0 {
		return fmt.Errorf("%s v5 player_persistence 指标不完整: %+v", label, player)
	}
	if player.P50MS > player.P95MS || player.P95MS > player.P99MS || player.P99MS > player.MaxMS {
		return fmt.Errorf("%s v5 player_persistence 分位数非单调: %+v", label, player)
	}
	return nil
}

func appendM3BStableLatencyRegressions(
	failures []string,
	prefix string,
	baselineP50, baselineP95, baselineP99 float64,
	currentP50, currentP95, currentP99 float64,
	threshold float64,
) []string {
	for _, metric := range []struct {
		name              string
		baseline, current float64
	}{
		{name: "p50_ms", baseline: baselineP50, current: currentP50},
		{name: "p95_ms", baseline: baselineP95, current: currentP95},
		{name: "p99_ms", baseline: baselineP99, current: currentP99},
	} {
		if metric.baseline < m3bLatencyNoiseFloorMS {
			continue
		}
		failures = appendRegression(
			failures, prefix, metric.name, metric.baseline, metric.current, threshold,
		)
	}
	return failures
}

func appendM3BLatencyRegressions(
	failures []string,
	prefix string,
	baselineP50, baselineP95, baselineP99, baselineMax float64,
	currentP50, currentP95, currentP99, currentMax float64,
	threshold float64,
) []string {
	for _, metric := range []struct {
		name              string
		baseline, current float64
	}{
		{name: "p50_ms", baseline: baselineP50, current: currentP50},
		{name: "p95_ms", baseline: baselineP95, current: currentP95},
		{name: "p99_ms", baseline: baselineP99, current: currentP99},
		{name: "max_ms", baseline: baselineMax, current: currentMax},
	} {
		if metric.baseline < m3bLatencyNoiseFloorMS {
			continue
		}
		failures = appendRegression(
			failures, prefix, metric.name, metric.baseline, metric.current, threshold,
		)
	}
	return failures
}

func appendStableSummaryRegressions(
	failures []string,
	prefix string,
	baselineP50, baselineP95, baselineP99 float64,
	currentP50, currentP95, currentP99 float64,
	threshold float64,
) []string {
	for _, metric := range []struct {
		name              string
		baseline, current float64
	}{
		{name: "p50_ms", baseline: baselineP50, current: currentP50},
		{name: "p95_ms", baseline: baselineP95, current: currentP95},
		{name: "p99_ms", baseline: baselineP99, current: currentP99},
	} {
		failures = appendRegression(
			failures, prefix, metric.name, metric.baseline, metric.current, threshold,
		)
	}
	return failures
}

func appendSummaryRegressions(
	failures []string,
	prefix string,
	baselineP50, baselineP95, baselineP99, baselineMax float64,
	currentP50, currentP95, currentP99, currentMax float64,
	threshold float64,
) []string {
	for _, metric := range []struct {
		name              string
		baseline, current float64
	}{
		{name: "p50_ms", baseline: baselineP50, current: currentP50},
		{name: "p95_ms", baseline: baselineP95, current: currentP95},
		{name: "p99_ms", baseline: baselineP99, current: currentP99},
		{name: "max_ms", baseline: baselineMax, current: currentMax},
	} {
		failures = appendRegression(
			failures, prefix, metric.name, metric.baseline, metric.current, threshold,
		)
	}
	return failures
}

func appendRegression(
	failures []string,
	prefix string,
	metric string,
	baseline float64,
	current float64,
	threshold float64,
) []string {
	if !regressed(baseline, current, threshold) {
		return failures
	}
	label := metric
	if prefix != "" {
		label = prefix + " " + metric
	}
	return append(failures, fmt.Sprintf(
		"%s 退化 %.1f%%：基线=%.3f 当前=%.3f",
		label,
		(current/baseline-1)*100,
		baseline,
		current,
	))
}

func readReport(path string) client.PerfReport {
	data, err := os.ReadFile(path)
	if err != nil {
		fail("读取 %s: %v", path, err)
	}
	var report client.PerfReport
	if err := json.Unmarshal(data, &report); err != nil {
		fail("解析 %s: %v", path, err)
	}
	return report
}

func regressed(baseline, current, threshold float64) bool {
	return baseline > 0 && current > baseline*(1+threshold)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}
