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
	"minecraft-go/internal/gfx"
)

const (
	benchmarkSeed   = 20260726
	scenarioVersion = 1
)

var (
	warmupDuration = 10 * time.Second
	stillDuration  = 60 * time.Second
	flyDuration    = 120 * time.Second
)

func runBenchmark(app *application, outputPath string) error {
	app.window.SetFloating(true)
	app.window.Focus()
	width, height := app.window.FramebufferSize()
	if width != 2560 || height != 1440 {
		return fmt.Errorf("benchmark framebuffer=%dx%d，要求精确 2560x1440", width, height)
	}
	if err := app.surface.SetPresentMode(gfx.PresentModeAutoNoVSync); err != nil {
		return fmt.Errorf("关闭 VSync: %w", err)
	}

	loadStarted := time.Now()
	if err := waitUntilLoaded(app, 5*time.Minute); err != nil {
		return err
	}
	loadSeconds := time.Since(loadStarted).Seconds()
	fmt.Printf("固定场景加载完成，用时 %.2f 秒；开始预热\n", loadSeconds)

	if err := runWarmup(app, warmupDuration); err != nil {
		return err
	}
	still, err := measurePhase(app, "still", stillDuration, nil)
	if err != nil {
		return err
	}
	flyingStart := app.camera.Pos
	flying, err := measurePhase(app, "flying", flyDuration, func(elapsed time.Duration) {
		seconds := float32(elapsed.Seconds())
		app.camera.Pos[0] = flyingStart[0] + seconds*48
		app.camera.Pos[2] = flyingStart[2] + float32(math.Sin(float64(seconds)*0.1))*96
		app.camera.Yaw = -float32(math.Pi)/2 + float32(math.Sin(float64(seconds)*0.2))*0.9
		app.camera.Pitch = -0.2 + float32(math.Sin(float64(seconds)*0.13))*0.15
		app.updateCenter()
	})
	if err != nil {
		return err
	}

	report := client.PerfReport{
		ScenarioVersion: scenarioVersion,
		Hardware:        hardwareID(),
		OS:              osID(),
		GoVersion:       runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH,
		GitCommit:       commandOutput("git", "rev-parse", "HEAD"),
		Framebuffer:     app.framebufferLabel(),
		LoadSeconds:     loadSeconds,
		Phases: map[string]client.PhaseSummary{
			"still":  still,
			"flying": flying,
		},
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

	var failures []string
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
	if len(failures) > 0 {
		return fmt.Errorf("%s", strings.Join(failures, "；"))
	}
	return nil
}

func waitUntilLoaded(app *application, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	wantedChunks := (2*(viewDistance+1) + 1) * (2*(viewDistance+1) + 1)
	lastLog := time.Time{}
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("固定场景在 %s 内未完成加载：chunks=%d/%d mesher=%+v pending=%d",
				timeout, len(app.loadedChunks), wantedChunks, app.mesher.Stats(),
				app.renderer.PendingUploads())
		}
		app.window.Poll()
		if app.window.ShouldClose() {
			app.window.CancelClose()
		}
		if !app.frame(4096) {
			app.window.Focus()
		}
		stats := app.mesher.Stats()
		if len(app.loadedChunks) == wantedChunks &&
			stats.QueuedJobs == 0 &&
			stats.InFlightJobs == 0 &&
			stats.ReadyResults == 0 &&
			stats.DirtySections == 0 &&
			app.renderer.PendingUploads() == 0 {
			return nil
		}
		if time.Since(lastLog) >= 5*time.Second {
			fmt.Printf("加载中：chunks=%d/%d queued=%d active=%d ready=%d pending=%d\n",
				len(app.loadedChunks), wantedChunks, stats.QueuedJobs, stats.InFlightJobs,
				stats.ReadyResults, app.renderer.PendingUploads())
			lastLog = time.Now()
		}
	}
}

func runWarmup(app *application, duration time.Duration) error {
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		app.window.Poll()
		if app.window.ShouldClose() {
			app.window.CancelClose()
		}
		if !app.frame(4096) {
			app.window.Focus()
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
		app.window.Poll()
		if app.window.ShouldClose() {
			app.window.CancelClose()
		}
		if update != nil {
			update(frameStarted.Sub(started))
		}
		rendered := app.frame(4096)
		if !rendered {
			app.window.Focus()
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
