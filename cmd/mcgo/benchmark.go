//go:build darwin

package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"minecraft-go/internal/client"
	"minecraft-go/internal/core"
	"minecraft-go/internal/gfx"
	"minecraft-go/internal/server"
)

const (
	benchmarkSeed   = 20260726
	scenarioVersion = 4
)

var (
	warmupDuration = 10 * time.Second
	stillDuration  = 60 * time.Second
	flyDuration    = 120 * time.Second
)

func runBenchmark(app *application, outputPath string) error {
	width, height := app.framebufferSize()
	if width != 2560 || height != 1440 {
		return fmt.Errorf("benchmark framebuffer=%dx%d，要求精确 2560x1440", width, height)
	}
	if app.surface != nil {
		if err := app.surface.SetPresentMode(gfx.PresentModeAutoNoVSync); err != nil {
			return fmt.Errorf("关闭 VSync: %w", err)
		}
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
	app.ticks.reset()
	app.saves.reset()
	still, err := measurePhase(app, "still", stillDuration, nil)
	if err != nil {
		return err
	}
	flyingStart := app.camera.Pos
	probe := server.NewTerrainProbe(benchmarkSeed)
	flying, err := measurePhase(app, "flying", flyDuration, func(elapsed time.Duration) {
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
	ticks := app.ticks.summary()
	persistence := app.saves.summary()

	report := client.PerfReport{
		ScenarioVersion: scenarioVersion,
		Hardware:        hardwareID(),
		OS:              osID(),
		GoVersion:       runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH,
		GitCommit:       commandOutput("git", "rev-parse", "HEAD"),
		Framebuffer:     app.framebufferLabel(),
		LoadSeconds:     loadSeconds,
		SnapshotSeconds: snapshotDuration.Seconds(),
		Phases: map[string]client.PhaseSummary{
			"still":  still,
			"flying": flying,
		},
		Ticks:       ticks,
		Persistence: persistence,
	}
	if report.Persistence.Snapshots == 0 {
		return validateBenchmarkReport(report)
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("编码性能报告: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(outputPath, data, 0o644); err != nil {
		return fmt.Errorf("写性能报告: %w", err)
	}
	fmt.Printf("性能报告已写入 %s\n", outputPath)
	return validateBenchmarkReport(report)
}

func validateBenchmarkReport(report client.PerfReport) error {
	var failures []string
	if report.Persistence.Snapshots == 0 {
		failures = append(failures, "persistence snapshots=0")
	}
	for name, phase := range report.Phases {
		if phase.FPS < 100 {
			failures = append(failures, fmt.Sprintf("%s fps %.1f < 100", name, phase.FPS))
		}
		if phase.P99MS >= 12 {
			failures = append(failures, fmt.Sprintf("%s p99 %.3f ms >= 12 ms", name, phase.P99MS))
		}
		if phase.PeakRSSBytes >= 2<<30 {
			failures = append(failures, fmt.Sprintf("%s 峰值 RSS %.1f MiB >= 2048 MiB",
				name, float64(phase.PeakRSSBytes)/(1<<20)))
		}
	}
	if report.Ticks.P99MS >= 10 {
		failures = append(failures, fmt.Sprintf("tick p99 %.3f ms >= 10 ms", report.Ticks.P99MS))
	}
	if report.Ticks.MaxMS >= 50 {
		failures = append(failures, fmt.Sprintf("tick max %.3f ms >= 50 ms", report.Ticks.MaxMS))
	}
	if len(failures) > 0 {
		return fmt.Errorf("%s", strings.Join(failures, "；"))
	}
	return nil
}

func waitUntilLoaded(app *application, timeout time.Duration) (time.Duration, error) {
	deadline := time.Now().Add(timeout)
	started := time.Now()
	var snapshotDuration time.Duration
	wantedChunks := (2*(viewDistance+1) + 1) * (2*(viewDistance+1) + 1)
	lastLog := time.Time{}
	for {
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("固定场景在 %s 内未完成加载：chunks=%d/%d mesher=%+v pending=%d",
				timeout, len(app.loadedChunks), wantedChunks, app.mesher.Stats(),
				app.renderer.PendingUploads())
		}
		if app.window != nil {
			app.window.Poll()
			if app.window.ShouldClose() {
				app.window.CancelClose()
			}
		}
		if !app.frame(4096) {
			continue
		}
		stats := app.mesher.Stats()
		if snapshotDuration == 0 && len(app.loadedChunks) == wantedChunks {
			snapshotDuration = time.Since(started)
		}
		if len(app.loadedChunks) == wantedChunks &&
			stats.QueuedJobs == 0 &&
			stats.InFlightJobs == 0 &&
			stats.ReadyResults == 0 &&
			stats.DirtySections == 0 &&
			app.renderer.PendingUploads() == 0 {
			return snapshotDuration, nil
		}
		if time.Since(lastLog) >= 5*time.Second {
			fmt.Printf("加载中：chunks=%d/%d queued=%d active=%d ready=%d pending=%d\n",
				len(app.loadedChunks), wantedChunks, stats.QueuedJobs, stats.InFlightJobs,
				stats.ReadyResults, app.renderer.PendingUploads())
			lastLog = time.Now()
		}
	}
}

func waitForBenchmarkCenterConsistency(
	app *application,
	center core.ChunkPos,
	afterSequence uint64,
	timeout time.Duration,
) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		app.frame(4096)
		appliedDimension, appliedCenter, appliedSequence, appliedOK :=
			app.server.AppliedTrustedObserverCenter()
		if !appliedOK || appliedDimension != core.Overworld ||
			appliedCenter != center || appliedSequence <= afterSequence {
			continue
		}
		authoritativeHash, authoritativeRevision, authoritativeOK := app.server.ChunkHash(
			core.Overworld, center,
		)
		mirrorHash, mirrorRevision, mirrorOK := app.mirror.Hash(core.Overworld, center)
		if authoritativeOK && mirrorOK && authoritativeRevision == mirrorRevision &&
			authoritativeHash == mirrorHash {
			return nil
		}
	}
	appliedDimension, appliedCenter, appliedSequence, appliedOK :=
		app.server.AppliedTrustedObserverCenter()
	return fmt.Errorf(
		"最终 trusted observer 中心在 %s 内未收敛: center=%+v afterSequence=%d applied=(%d,%+v,%d,%v)",
		timeout,
		center,
		afterSequence,
		appliedDimension,
		appliedCenter,
		appliedSequence,
		appliedOK,
	)
}

func runWarmup(app *application, duration time.Duration) error {
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		if app.window != nil {
			app.window.Poll()
			if app.window.ShouldClose() {
				app.window.CancelClose()
			}
		}
		if !app.frame(4096) {
			continue
		}
	}
	return nil
}

func measurePhase(
	app *application,
	name string,
	duration time.Duration,
	update func(time.Duration),
) (client.PhaseSummary, error) {
	sampler := client.NewPerfSampler(300_000)
	started := time.Now()
	deadline := started.Add(duration)
	nextRSS := started
	lastRendered := started
	var peakRSS uint64
	for time.Now().Before(deadline) {
		frameStarted := time.Now()
		if app.window != nil {
			app.window.Poll()
			if app.window.ShouldClose() {
				app.window.CancelClose()
			}
		}
		if update != nil {
			update(frameStarted.Sub(started))
		}
		rendered := app.frame(4096)
		if !rendered {
			if time.Since(lastRendered) > 5*time.Second {
				return client.PhaseSummary{}, fmt.Errorf("%s 阶段连续 5 秒取不到 surface 帧", name)
			}
			continue
		}
		lastRendered = time.Now()
		frameMS := float64(time.Since(frameStarted).Microseconds()) / 1000
		stats := app.renderer.LastFrameStats()
		sampler.Add(client.FrameSample{
			FrameMS:           frameMS,
			CandidateSections: stats.CandidateSections,
			CandidateBytes:    stats.CandidateBytes,
			CandidateFaces:    stats.CandidateFaces,
			PendingUploads:    app.renderer.PendingUploads(),
		})
		if time.Now().After(nextRSS) {
			rss, err := client.ProcessRSSBytes()
			if err != nil {
				return client.PhaseSummary{}, err
			}
			peakRSS = max(peakRSS, rss)
			nextRSS = nextRSS.Add(time.Second)
		}
	}
	summary := sampler.Summary(peakRSS)
	fmt.Printf("%s: fps=%.1f p50=%.3fms p95=%.3fms p99=%.3fms max=%.3fms RSS=%.1fMiB\n",
		name, summary.FPS, summary.P50MS, summary.P95MS, summary.P99MS,
		summary.MaxMS, float64(summary.PeakRSSBytes)/(1<<20))
	return summary, nil
}

func hardwareID() string {
	cpu := commandOutput("sysctl", "-n", "machdep.cpu.brand_string")
	memory := commandOutput("sysctl", "-n", "hw.memsize")
	if bytes, err := strconv.ParseUint(memory, 10, 64); err == nil {
		memory = fmt.Sprintf("%dGiB", bytes>>30)
	}
	return strings.TrimSpace(cpu + " / " + memory)
}

func osID() string {
	version := commandOutput("sw_vers", "-productVersion")
	return "macOS " + version
}

func commandOutput(name string, args ...string) string {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}
