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

func TestClientMemoryLimitLeavesHeadroomAboveLiveHeap(t *testing.T) {
	// 实测 flying 阶段的活跃堆峰值约 1252MiB。软上限必须明显高于它，
	// 否则 GC 会长期贴着上限运行，把 CPU 与帧时间拖垮。
	const observedLiveHeapMiB = 1252
	limitMiB := clientMemoryLimit / (1 << 20)
	if limitMiB <= observedLiveHeapMiB {
		t.Fatalf("内存上限 %dMiB 不高于实测活跃堆 %dMiB", limitMiB, observedLiveHeapMiB)
	}
	if headroom := float64(limitMiB-observedLiveHeapMiB) / observedLiveHeapMiB; headroom < 0.15 {
		t.Fatalf("内存上限相对活跃堆只有 %.1f%% 余量，想要至少 15%%", headroom*100)
	}

	// 上限加上非 Go 分配仍须明显低于既有 2GiB RSS 门禁。
	const observedNonGoMiB = 230
	const rssGateMiB = 2048
	if projected := limitMiB + observedNonGoMiB; projected >= rssGateMiB {
		t.Fatalf("预计 RSS 峰值 %dMiB 未低于门禁 %dMiB", projected, rssGateMiB)
	}
}
