package main

import (
	"fmt"
	"sort"

	"github.com/channing771/mornlea/internal/client"
)

func comparisonSuccessMessage(baselineVersion, currentVersion int) string {
	if baselineVersion != currentVersion {
		return fmt.Sprintf("场景迁移性能记录完成：报告完整、硬件一致，当前 v%d", currentVersion)
	}
	return "同场景性能记录完成"
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
	// 饥饿又一次改变了被测进程（HUD 新增饥饿条，quad 容量 247→267 使固定 GPU
	// 上传布局、glyph offset 与每帧写入字节数移动；HUD 图集多出两列鸡腿；
	// 权威 tick 多出饥饿三层状态的推进），因此当前唯一迁移是 v18 到 v19。
	// 历史的 17:18 已退役——它只作为归档证据留在 docs/notes，不再是授权值。
	allowedScenarioUpgrade := baseline.ScenarioVersion == 18 && current.ScenarioVersion == 19 &&
		allowScenarioUpgrade == "18:19"
	if allowScenarioUpgrade != "" && !allowedScenarioUpgrade {
		return nil, fmt.Errorf("场景迁移授权 %q 无效：只允许 v18 到 v19 使用 18:19", allowScenarioUpgrade)
	}
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
	if baseline.Transport != current.Transport && baseline.ScenarioVersion != current.ScenarioVersion {
		return nil, fmt.Errorf(
			"跨 transport scenario_version 不同，拒绝比较：基线=%d 当前=%d",
			baseline.ScenarioVersion,
			current.ScenarioVersion,
		)
	}
	if baseline.Transport != current.Transport && baseline.GitCommit != current.GitCommit {
		return nil, fmt.Errorf(
			"跨 transport git_commit 不同，拒绝比较：基线=%q 当前=%q",
			baseline.GitCommit,
			current.GitCommit,
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
				regressionFloors{},
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
				persistenceFloors(),
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
				regressionFloors{},
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
