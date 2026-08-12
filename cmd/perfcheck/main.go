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
	allowScenarioUpgrade := flag.String("allow-scenario-upgrade", "", "只允许显式的 14:15 场景迁移")
	flag.Parse()

	if *baselinePath == "" || *currentPath == "" {
		fail("-baseline 与 -current 都必须提供")
	}
	baseline := readReport(*baselinePath)
	current := readReport(*currentPath)
	records, err := compareReportsWithScenarioUpgrade(
		baseline, current, *maxRegression, *allowScenarioUpgrade,
	)
	if err != nil {
		fail("%v", err)
	}
	for _, record := range records {
		fmt.Fprintln(os.Stdout, "性能记录:", record)
	}
	fmt.Println(comparisonSuccessMessage(baseline.ScenarioVersion, current.ScenarioVersion))
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

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}
