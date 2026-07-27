package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

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
	if baseline.ScenarioVersion != current.ScenarioVersion {
		fail("scenario_version 不同：基线=%d 当前=%d",
			baseline.ScenarioVersion, current.ScenarioVersion)
	}
	if baseline.Hardware != current.Hardware {
		fail("硬件标识不同，拒绝比较：基线=%q 当前=%q", baseline.Hardware, current.Hardware)
	}

	failed := false
	for name, basePhase := range baseline.Phases {
		currentPhase, ok := current.Phases[name]
		if !ok {
			fmt.Fprintf(os.Stderr, "当前报告缺少阶段 %q\n", name)
			failed = true
			continue
		}
		for metric, values := range map[string][2]float64{
			"p50_ms":         {basePhase.P50MS, currentPhase.P50MS},
			"p95_ms":         {basePhase.P95MS, currentPhase.P95MS},
			"p99_ms":         {basePhase.P99MS, currentPhase.P99MS},
			"max_ms":         {basePhase.MaxMS, currentPhase.MaxMS},
			"peak_rss_bytes": {float64(basePhase.PeakRSSBytes), float64(currentPhase.PeakRSSBytes)},
		} {
			if regressed(values[0], values[1], *maxRegression) {
				fmt.Fprintf(os.Stderr, "%s %s 退化 %.1f%%：基线=%.3f 当前=%.3f\n",
					name, metric, (values[1]/values[0]-1)*100, values[0], values[1])
				failed = true
			}
		}
		if basePhase.FPS > 0 && currentPhase.FPS < basePhase.FPS*(1-*maxRegression) {
			fmt.Fprintf(os.Stderr, "%s fps 退化 %.1f%%：基线=%.3f 当前=%.3f\n",
				name, (1-currentPhase.FPS/basePhase.FPS)*100, basePhase.FPS, currentPhase.FPS)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
	fmt.Println("性能比较通过：所有阶段退化均未超过阈值")
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
