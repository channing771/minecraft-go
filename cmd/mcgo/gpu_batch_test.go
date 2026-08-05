package main

import (
	"reflect"
	"testing"
	"time"

	"minecraft-go/internal/client"
)

func TestScenarioV12GPUCompletionAmortizesOverFixedBatch(t *testing.T) {
	app, dev := newRemoteRenderApplication(t, &integrationGlyphSource{})
	probe, err := newMultiplayerClientProbe(app)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(probe.Close)

	// 每次读钟推进 1ms：一批的总耗时因此恒为 1ms，样本值必须是 1ms / 批次数量。
	clockReads := 0
	probe.now = func() time.Time {
		dev.events = append(dev.events, "now")
		clockReads++
		return time.Unix(0, int64(clockReads)*int64(time.Millisecond))
	}
	dev.events = nil
	if err := probe.measureGPUCompletion(app); err != nil {
		t.Fatal(err)
	}

	summary := probe.gpuComplete.Summary()
	if summary.Samples != client.ScenarioV12GPUCompletionSamples {
		t.Fatalf("样本数 = %d，想要 %d", summary.Samples, client.ScenarioV12GPUCompletionSamples)
	}
	// 样本按 time.Duration 的整数纳秒摊薄，期望值同样取整。
	wantMS := float64(time.Millisecond/client.ScenarioV12GPUCompletionBatch) / float64(time.Millisecond)
	if summary.P50MS != wantMS {
		t.Fatalf("样本 p50 = %v ms，想要摊薄后的 %v ms", summary.P50MS, wantMS)
	}

	// 每个样本只允许一次 submit/poll 与一对读钟；标签准备与释放位于计时区间之外。
	want := []string{"finish", "now", "submit", "poll", "now", "release"}
	if got, expected := len(dev.events), client.ScenarioV12GPUCompletionSamples*len(want); got != expected {
		t.Fatalf("GPU 事件数 = %d，想要 %d", got, expected)
	}
	for sample := range client.ScenarioV12GPUCompletionSamples {
		start := sample * len(want)
		if got := dev.events[start : start+len(want)]; !reflect.DeepEqual(got, want) {
			t.Fatalf("样本 %d 事件 = %v，想要 %v", sample, got, want)
		}
	}
}

func TestScenarioV12GPUCompletionBatchIsLargeEnoughToAmortizePollTick(t *testing.T) {
	// 实测 Poll 的固定节拍约为 1.28ms。批次数量必须让节拍摊薄到
	// 远小于 20% 判定阈值，否则分位数会重新被量化。
	const observedPollTickMS = 1.28
	const perDrawMS = 0.06

	amortized := observedPollTickMS / float64(client.ScenarioV12GPUCompletionBatch)
	if share := amortized / perDrawMS; share > 0.05 {
		t.Fatalf(
			"节拍摊薄后占每次绘制成本的 %.1f%%，想要不超过 5%%（批次数量 = %d）",
			share*100, client.ScenarioV12GPUCompletionBatch,
		)
	}
}

func TestScenarioV12GPUCompletionBatchIsRecordedInReport(t *testing.T) {
	app, _ := newRemoteRenderApplication(t, &integrationGlyphSource{})
	probe, err := newMultiplayerClientProbe(app)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(probe.Close)
	if err := probe.measureGPUCompletion(app); err != nil {
		t.Fatal(err)
	}

	summary := probe.Summary()
	if got := summary.RemoteGPUCompleteBatch; got != client.ScenarioV12GPUCompletionBatch {
		t.Fatalf("报告中的批次数量 = %d，想要 %d", got, client.ScenarioV12GPUCompletionBatch)
	}
}
