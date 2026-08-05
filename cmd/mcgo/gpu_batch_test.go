package main

import (
	"os"
	"reflect"
	"strings"
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

	// 每个样本只允许一次 submit/poll 与一对读钟；编码、标签准备与释放都在区间外。
	chunks := client.ScenarioV12GPUCompletionBatch / client.ScenarioV12GPUCompletionChunk
	want := make([]string, 0, chunks*2+4)
	for range chunks {
		want = append(want, "finish")
	}
	want = append(want, "now", "submit", "poll", "now")
	for range chunks {
		want = append(want, "release")
	}
	want = append(want, "poll")
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

func TestScenarioV12GPUCompletionChunkStaysWithinCommandBufferBudget(t *testing.T) {
	// 每次绘制会开启 avatar 与 name tag 两个 render pass。
	// 单个 command buffer 的 pass 数必须留在 Metal 的 4096 预算之内。
	const metalCommandBufferBudget = 4096
	const passesPerDraw = 2
	if passes := client.ScenarioV12GPUCompletionChunk * passesPerDraw; passes*2 > metalCommandBufferBudget {
		t.Fatalf("单个 command buffer 的 pass 数 = %d，想要不超过预算的一半", passes)
	}
	if client.ScenarioV12GPUCompletionBatch%client.ScenarioV12GPUCompletionChunk != 0 {
		t.Fatalf("批次数量 %d 必须能被分块大小 %d 整除",
			client.ScenarioV12GPUCompletionBatch, client.ScenarioV12GPUCompletionChunk)
	}
}

func TestScenarioV12GPUCompletionBatchIsLargeEnoughToAmortizePollTick(t *testing.T) {
	// 实测 Poll 的固定节拍约为 1.28ms。批次数量必须让节拍摊薄到
	// 远小于 20% 判定阈值，否则分位数会重新被量化。
	const observedPollTickMS = 1.28
	const observedPerDrawMS = 0.09
	const relativeGateThreshold = 0.20

	amortized := observedPollTickMS / float64(client.ScenarioV12GPUCompletionBatch)
	// 摊薄后的节拍必须远小于相对判定阈值，否则分位数会重新被它主导。
	if share := amortized / observedPerDrawMS; share > relativeGateThreshold/2 {
		t.Fatalf(
			"节拍摊薄后占每次绘制成本的 %.1f%%，想要不超过判定阈值的一半（批次数量 = %d）",
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

func TestScenarioV12GPUCompletionReclaimsEverySample(t *testing.T) {
	// ru_maxrss 是进程生命周期的历史峰值，一旦被推高就无法降回。
	// 因此批量分摊产生的对象必须逐样本回收，不能等到阶段之间的冷却。
	source, err := os.ReadFile("multiplayer_benchmark.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(source)
	start := strings.Index(body, "func (probe *multiplayerClientProbe) measureGPUCompletion(")
	if start < 0 {
		t.Fatal("找不到 measureGPUCompletion")
	}
	end := strings.Index(body[start:], "\nfunc ")
	if end < 0 {
		end = len(body) - start
	}
	sampling := body[start : start+end]
	if !strings.Contains(sampling, "runtime.GC()") {
		t.Fatal("采样循环内没有逐样本回收，RSS 峰值会随样本数累积")
	}
}
