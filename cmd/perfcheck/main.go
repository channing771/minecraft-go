package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"

	"minecraft-go/internal/client"
)

func main() {
	baselinePath := flag.String("baseline", "", "基线 JSON")
	currentPath := flag.String("current", "", "当前 JSON")
	maxRegression := flag.Float64("max-regression", 0.20, "允许的最大相对退化")
	flag.Parse()

	if *baselinePath == "" || *currentPath == "" {
		fail("-baseline 与 -current 都必须提供")
	}
	baseline := readReport(*baselinePath)
	current := readReport(*currentPath)
	failures, err := compareReports(baseline, current, *maxRegression)
	if err != nil {
		fail("%v", err)
	}
	for _, failure := range failures {
		fmt.Fprintln(os.Stderr, failure)
	}
	if len(failures) != 0 {
		os.Exit(1)
	}
	fmt.Println("性能比较通过：所有阶段退化均未超过阈值")
}

func compareReports(
	baseline client.PerfReport,
	current client.PerfReport,
	maxRegression float64,
) ([]string, error) {
	if baseline.ScenarioVersion != current.ScenarioVersion {
		return nil, fmt.Errorf(
			"scenario_version 不同：基线=%d 当前=%d",
			baseline.ScenarioVersion,
			current.ScenarioVersion,
		)
	}
	if baseline.Hardware != current.Hardware {
		return nil, fmt.Errorf(
			"硬件标识不同，拒绝比较：基线=%q 当前=%q",
			baseline.Hardware,
			current.Hardware,
		)
	}

	var failures []string
	for _, metric := range []struct {
		name              string
		baseline, current float64
	}{
		{name: "load_seconds", baseline: baseline.LoadSeconds, current: current.LoadSeconds},
		{name: "snapshot_seconds", baseline: baseline.SnapshotSeconds, current: current.SnapshotSeconds},
	} {
		failures = appendRegression(failures, "", metric.name, metric.baseline, metric.current, maxRegression)
	}
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
	if baseline.Persistence.Snapshots > 0 && current.Persistence.Snapshots > 0 {
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
