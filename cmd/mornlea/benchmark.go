//go:build darwin

package main

import (
	"fmt"
	"math"
	"runtime"
	"time"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/server"
)

const (
	benchmarkSeed            = 20260726
	benchmarkMessageDrainMax = 4096
	// scenarioVersion 是 benchmark producer 的场景身份。v16 → v17 的理由是
	// fluid-presentation-survival 改变了被测进程本身，即便 benchmark 的世界内容
	// 没变（它把 FluidEnabled 钉死为 false）：mesh registry 条目从 16 字节扩到
	// 18、quad 位布局改写、光照 BFS 改为按方块查衰减表、渲染器多出一条 water
	// pass 与一块 32 MiB 的固定水面实例缓冲、StepInput 头版本升到 v2。这些都
	// 落在 RSS、管线数量与每帧工作量上，v16 与 v17 的数字因此不可直接比较。
	scenarioVersion = 17
)

var (
	warmupDuration = 10 * time.Second
	stillDuration  = 60 * time.Second
	flyDuration    = 120 * time.Second
	// benchmarkCooldown 是阶段之间的固定冷却，让 GPU 从满载回落。
	// 它只影响阶段之间的时间轴，不改变任何被采集指标的定义、样本数或阶段时长。
	benchmarkCooldown = 30 * time.Second
)

// runBenchmarkCooldown 在阶段之间等待固定时长，期间只泵送窗口事件，
// 不提交任何渲染工作，也不推进相机脚本。
func runBenchmarkCooldown(app *application, duration time.Duration) {
	// 冷却是让系统回落的窗口：顺带回收上一阶段产生的对象，
	// 避免它们把后续阶段的 RSS 峰值推高。
	runtime.GC()
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		if app.window != nil {
			app.window.Poll()
			if app.window.ShouldClose() {
				app.window.CancelClose()
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// printMemoryBreakdown 打印进程 RSS 峰值与 Go 运行时的内存构成。
//
// RSS 取自 ru_maxrss，是进程生命周期的历史峰值，回收之后不会下降；
// 把它与 Go 堆分开显示，才能判断峰值来自 Go 堆还是原生分配。
func printMemoryBreakdown(label string) {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	const mib = 1 << 20
	rss, err := client.ProcessRSSBytes()
	if err != nil {
		return
	}
	fmt.Printf(
		"%s 内存：RSS 峰值 %.1fMiB｜Go 堆在用 %.1fMiB｜Go 堆保留 %.1fMiB｜Go 运行时合计 %.1fMiB｜非 Go %.1fMiB\n",
		label,
		float64(rss)/mib,
		float64(stats.HeapAlloc)/mib,
		float64(stats.HeapSys)/mib,
		float64(stats.Sys)/mib,
		(float64(rss)-float64(stats.Sys))/mib,
	)
}

// benchmarkReportSkeleton 返回只含固定运行参数的报告骨架，供测试断言这些参数被记录。
func benchmarkReportSkeleton() client.PerfReport {
	return client.PerfReport{
		ScenarioVersion: scenarioVersion,
		CooldownSeconds: benchmarkCooldown.Seconds(),
	}
}

// gpuCompletionMinSamples 返回该场景下 remote_gpu_complete 的最小样本数。
// v8–v11 逐次计时取 2048；v12–v14 改为批量分摊，样本数相应减少。
func gpuCompletionMinSamples(scenario int) int {
	switch {
	case scenario >= 12:
		return client.ScenarioV12GPUCompletionSamples
	case scenario >= 8:
		return client.ScenarioV8GPUCompletionSamples
	default:
		return 256
	}
}

func runBenchmark(app *application, outputPath string) error {
	width, height := app.framebufferSize()
	if width != 2560 || height != 1440 {
		return fmt.Errorf("benchmark framebuffer=%dx%d，要求精确 2560x1440", width, height)
	}

	loadStarted := time.Now()
	snapshotDuration, err := waitUntilLoaded(app, 5*time.Minute)
	if err != nil {
		return err
	}
	loadSeconds := time.Since(loadStarted).Seconds()
	fmt.Printf("固定场景加载完成，用时 %.2f 秒；开始预热\n", loadSeconds)

	if err := runWarmup(app, warmupDuration); err != nil {
		return err
	}
	runBenchmarkCooldown(app, benchmarkCooldown)
	multiplayerProbe, err := newMultiplayerClientProbe(app)
	if err != nil {
		return fmt.Errorf("创建多人客户端性能探针: %w", err)
	}
	defer multiplayerProbe.Close()
	app.ticks.reset()
	app.saves.reset()
	still, err := measurePhase(app, multiplayerProbe, "still", stillDuration, nil)
	if err != nil {
		return err
	}
	printMemoryBreakdown("still 后")
	runBenchmarkCooldown(app, benchmarkCooldown)
	flyingStart := app.camera.Pos
	probe := server.NewTerrainProbe(benchmarkSeed)
	flying, err := measurePhase(app, multiplayerProbe, "flying", flyDuration, func(elapsed time.Duration) {
		seconds := float32(elapsed.Seconds())
		app.camera.Pos[0] = flyingStart[0] + seconds*48
		app.camera.Pos[2] = flyingStart[2] + float32(math.Sin(float64(seconds)*0.1))*96
		x := int32(math.Floor(float64(app.camera.Pos[0])))
		z := int32(math.Floor(float64(app.camera.Pos[2])))
		app.camera.Pos[1] = float32(probe.HeightAt(x, z)) + 3.5
		app.camera.Pitch = -float32(math.Pi)/2 + 0.02
		app.updateCenter()
	})
	if err != nil {
		return err
	}
	printMemoryBreakdown("flying 后")
	finalCenter := app.center
	if err := waitForBenchmarkCenterConsistency(
		app,
		finalCenter,
		app.observerFloor,
		10*time.Second,
	); err != nil {
		return err
	}
	authoritativeHash, authoritativeRevision, authoritativeOK := app.server.ChunkHash(
		core.Overworld, finalCenter,
	)
	mirrorHash, mirrorRevision, mirrorOK := app.mirror.Hash(
		core.Overworld, finalCenter,
	)
	if !authoritativeOK || !mirrorOK || authoritativeRevision != mirrorRevision ||
		authoritativeHash != mirrorHash {
		return fmt.Errorf("最终 trusted observer 中心权威/镜像不一致: center=%+v server=(%x,%d,%v) mirror=(%x,%d,%v)",
			finalCenter,
			authoritativeHash, authoritativeRevision, authoritativeOK,
			mirrorHash, mirrorRevision, mirrorOK)
	}
	// GPU 采样不得紧接 flying 的满载尾部。
	runBenchmarkCooldown(app, benchmarkCooldown)
	if err := multiplayerProbe.measureGPUCompletionAfterTransportClose(app); err != nil {
		return fmt.Errorf("测量远端 GPU 完成时间: %w", err)
	}
	printMemoryBreakdown("GPU 采样后")
	// GPU 采样同样是满载阶段，其后也要冷却并回收，才轮到服务端探针。
	runBenchmarkCooldown(app, benchmarkCooldown)
	serverMultiplayer, ticks, err := measureMultiplayerServerProbe(10 * time.Second)
	if err != nil {
		return fmt.Errorf("测量八会话服务端: %w", err)
	}
	multiplayer := multiplayerProbe.Summary()
	multiplayer.InterestDiff = serverMultiplayer.InterestDiff
	multiplayer.ServerOutboundBytes = serverMultiplayer.ServerOutboundBytes
	multiplayer.OutboxHighWater = serverMultiplayer.OutboxHighWater
	multiplayer.PlayerJobsHighWater = serverMultiplayer.PlayerJobsHighWater
	multiplayer.PlayerDoneHighWater = serverMultiplayer.PlayerDoneHighWater
	multiplayer.PeakRSSBytes = serverMultiplayer.PeakRSSBytes
	persistence := app.saves.summary()
	protocol, err := measureProtocolSummary()
	if err != nil {
		return err
	}
	playerPersistence, err := measurePlayerPersistenceSummary()
	if err != nil {
		return err
	}

	report := client.PerfReport{
		ScenarioVersion: scenarioVersion,
		Transport:       app.benchmarkTransport,
		Hardware:        hardwareID(),
		OS:              osID(),
		GoVersion:       runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH,
		GitCommit:       commandOutput("git", "rev-parse", "HEAD"),
		Framebuffer:     app.framebufferLabel(),
		LoadSeconds:     loadSeconds,
		CooldownSeconds: benchmarkCooldown.Seconds(),
		SnapshotSeconds: snapshotDuration.Seconds(),
		Phases: map[string]client.PhaseSummary{
			"still":  still,
			"flying": flying,
		},
		Ticks:             ticks,
		Persistence:       persistence,
		Protocol:          protocol,
		PlayerPersistence: playerPersistence,
		Multiplayer:       multiplayer,
	}
	if err := writeBenchmarkReport(outputPath, report); err != nil {
		return err
	}
	for _, record := range benchmarkPerformanceRecords(report) {
		fmt.Println("性能记录:", record)
	}
	fmt.Printf("性能报告已写入 %s\n", outputPath)
	return nil
}
