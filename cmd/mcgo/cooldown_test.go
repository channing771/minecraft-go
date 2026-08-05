package main

import (
	"testing"
	"time"
)

func TestBenchmarkCooldownIsFixedAndNonZero(t *testing.T) {
	if benchmarkCooldown <= 0 {
		t.Fatalf("冷却时长 = %v，想要正值", benchmarkCooldown)
	}
	// 冷却只在阶段之间发生，不得长到改变 benchmark 的整体量级。
	if benchmarkCooldown > time.Minute {
		t.Fatalf("冷却时长 = %v，想要不超过 1 分钟", benchmarkCooldown)
	}
}

func TestBenchmarkCooldownDoesNotSubmitRenderWork(t *testing.T) {
	app, dev := newRemoteRenderApplication(t, &integrationGlyphSource{})
	dev.events = nil

	// 冷却期间只允许窗口事件泵，不得提交任何渲染工作。
	runBenchmarkCooldown(app, 10*time.Millisecond)

	for _, event := range dev.events {
		switch event {
		case "submit", "finish", "poll":
			t.Fatalf("冷却期间提交了渲染工作：%v", dev.events)
		}
	}
}

func TestBenchmarkCooldownIsRecordedInReport(t *testing.T) {
	report := benchmarkReportSkeleton()
	if got := report.CooldownSeconds; got != benchmarkCooldown.Seconds() {
		t.Fatalf("报告中的冷却秒数 = %v，想要 %v", got, benchmarkCooldown.Seconds())
	}
}
