package main

import "fmt"

const m3bLatencyNoiseFloorMS = 0.01

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

// pollCompletionTickMS 是宿主完成等待实现的固定节拍，实测约为 1.28ms。
// 逐次计时的样本被量化到它的整数倍；批量分摊后每个样本只含一次该节拍。
const pollCompletionTickMS = 1.28

// gpuCompletionResolutionMS 返回 remote_gpu_complete 的最小可分辨增量。
// batch 为 0 表示 v8–v11 的逐次计时，分辨率就是完整节拍；
// v12 起按批次数量摊薄。
func gpuCompletionResolutionMS(batch int) float64 {
	if batch <= 0 {
		return pollCompletionTickMS
	}
	return max(pollCompletionTickMS/float64(batch), latencyNoiseFloorMS)
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
	floors regressionFloors,
) []string {
	for _, metric := range []struct {
		name              string
		baseline, current float64
		floor             float64
	}{
		{name: "p50_ms", baseline: baselineP50, current: currentP50, floor: floors.p50},
		{name: "p95_ms", baseline: baselineP95, current: currentP95, floor: floors.p95},
		{name: "p99_ms", baseline: baselineP99, current: currentP99, floor: floors.p99},
	} {
		failures = appendRegressionWithResolution(
			failures, prefix, metric.name, metric.baseline, metric.current, threshold, metric.floor,
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
	return appendRegressionWithResolution(failures, prefix, metric, baseline, current, threshold, 0)
}

// latencyNoiseFloorMS 是墙钟延迟指标在运行之间的测量噪声。
//
// 调度抖动、缓存与频率变化让这个量级的差异无法在两次运行之间复现：实测同一
// 提交的两份报告里，remote_state_encode 差 1µs、avatar_submit 差 7µs、
// remote_gpu_complete 差 30µs，相对变化却分别达到 14%、50% 与 25%。
// 对微秒级指标施加相对判定，测到的是抖动而不是性能。
const latencyNoiseFloorMS = 0.05

// persistenceTailNoiseFloorMS 是区块保存尾延迟在运行之间的固有波动。
//
// 实测同一台机器上的 11 次运行：persistence p50 的最大/最小为 1.04x，而 p95 与
// p99 达到 1.97x（p95 跨度 6.713-13.242ms，p99 跨度 8.485-16.757ms）。尾延迟受
// 页缓存、后台 flush 与 SSD 内部回收影响，这个波动与代码无关，`20%` 阈值无法容纳。
// 中位数不设地板，继续接受相对判定。
const persistenceTailNoiseFloorMS = 7.0

// regressionFloors 是一组分位数各自的最小有意义增量。
// 同一指标的中位数与尾分位数可能有量级不同的固有波动，因此分别设定。
type regressionFloors struct {
	p50, p95, p99 float64
}

// uniformFloors 让三个分位数共用同一个下限。
func uniformFloors(value float64) regressionFloors {
	return regressionFloors{p50: value, p95: value, p99: value}
}

// persistenceFloors 是区块保存指标的下限：中位数照常判定，尾分位数按实测波动豁免。
func persistenceFloors() regressionFloors {
	return regressionFloors{p95: persistenceTailNoiseFloorMS, p99: persistenceTailNoiseFloorMS}
}

// appendRegressionWithResolution 只在变化超过指标的最小有意义增量时施加相对门禁。
//
// resolutionMS 是该指标的最小有意义增量：小于等于它的变化落在测量噪声或宿主的
// 量化步长之内，相对判定只会反映噪声而非真实退化，因此跳过；该指标的完整性与
// 绝对上限门禁不受影响。resolutionMS 为 0 表示不设下限，按普通相对判定处理。
//
// 这条规则不会放松对真实退化的敏感度：只要绝对增量越过下限，相对阈值照常生效。
func appendRegressionWithResolution(
	failures []string,
	prefix string,
	metric string,
	baseline float64,
	current float64,
	threshold float64,
	resolutionMS float64,
) []string {
	if resolutionMS > 0 && current-baseline <= resolutionMS {
		return failures
	}
	if !regressed(baseline, current, threshold) {
		return failures
	}
	label := metric
	if prefix != "" {
		label = prefix + " " + metric
	}
	return append(failures, fmt.Sprintf(
		"%s 相对退化 %.1f%%：基线=%.3f 当前=%.3f",
		label,
		(current/baseline-1)*100,
		baseline,
		current,
	))
}

func regressed(baseline, current, threshold float64) bool {
	return baseline > 0 && current > baseline*(1+threshold)
}
