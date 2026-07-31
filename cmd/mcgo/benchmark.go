//go:build darwin

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"minecraft-go/internal/client"
	"minecraft-go/internal/core"
	"minecraft-go/internal/gfx"
	"minecraft-go/internal/network"
	"minecraft-go/internal/server"
	"minecraft-go/internal/storage"
)

const (
	benchmarkSeed   = 20260726
	scenarioVersion = 5
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
		SnapshotSeconds: snapshotDuration.Seconds(),
		Phases: map[string]client.PhaseSummary{
			"still":  still,
			"flying": flying,
		},
		Ticks:             ticks,
		Persistence:       persistence,
		Protocol:          protocol,
		PlayerPersistence: playerPersistence,
	}
	if err := writeBenchmarkReport(outputPath, report); err != nil {
		return err
	}
	fmt.Printf("性能报告已写入 %s\n", outputPath)
	return nil
}

func writeBenchmarkReport(outputPath string, report client.PerfReport) error {
	return writeBenchmarkReportWithFS(outputPath, report, defaultBenchmarkReportFS())
}

type benchmarkReportFile interface {
	Write([]byte) (int, error)
	Sync() error
	Close() error
	Chmod(os.FileMode) error
	Name() string
}

type benchmarkReportDirectory interface {
	Sync() error
	Close() error
}

type benchmarkReportFS struct {
	createTemp    func(string, string) (benchmarkReportFile, error)
	rename        func(string, string) error
	remove        func(string) error
	readFile      func(string) ([]byte, error)
	openDirectory func(string) (benchmarkReportDirectory, error)
}

func defaultBenchmarkReportFS() benchmarkReportFS {
	return benchmarkReportFS{
		createTemp: func(directory, pattern string) (benchmarkReportFile, error) {
			return os.CreateTemp(directory, pattern)
		},
		rename:   os.Rename,
		remove:   os.Remove,
		readFile: os.ReadFile,
		openDirectory: func(path string) (benchmarkReportDirectory, error) {
			return os.Open(path)
		},
	}
}

func writeBenchmarkReportWithFS(
	outputPath string,
	report client.PerfReport,
	fs benchmarkReportFS,
) error {
	if err := validateBenchmarkReport(report); err != nil {
		return err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("编码性能报告: %w", err)
	}
	data = append(data, '\n')
	oldData, readErr := fs.readFile(outputPath)
	hadOld := readErr == nil
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return fmt.Errorf("读取现有性能报告: %w", readErr)
	}
	if err := writeSyncedBenchmarkTemp(outputPath, data, fs); err != nil {
		return err
	}
	if err := syncBenchmarkReportDirectory(filepath.Dir(outputPath), fs); err != nil {
		rollbackErr := rollbackBenchmarkReport(outputPath, oldData, hadOld, fs)
		return errors.Join(fmt.Errorf("同步性能报告目录: %w", err), rollbackErr)
	}
	return nil
}

func writeSyncedBenchmarkTemp(outputPath string, data []byte, fs benchmarkReportFS) (returnErr error) {
	directory := filepath.Dir(outputPath)
	pattern := "." + filepath.Base(outputPath) + ".tmp-*"
	file, err := fs.createTemp(directory, pattern)
	if err != nil {
		return fmt.Errorf("创建性能报告临时文件: %w", err)
	}
	tempPath := file.Name()
	closed := false
	promoted := false
	defer func() {
		if !closed {
			returnErr = errors.Join(returnErr, file.Close())
		}
		if !promoted {
			if removeErr := fs.remove(tempPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				returnErr = errors.Join(returnErr, removeErr)
			}
		}
	}()
	if err := file.Chmod(0o644); err != nil {
		return fmt.Errorf("设置性能报告临时文件权限: %w", err)
	}
	if err := writeBenchmarkReportBytes(file, data); err != nil {
		return fmt.Errorf("写性能报告临时文件: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("同步性能报告临时文件: %w", err)
	}
	if err := file.Close(); err != nil {
		closed = true
		return fmt.Errorf("关闭性能报告临时文件: %w", err)
	}
	closed = true
	if err := fs.rename(tempPath, outputPath); err != nil {
		return fmt.Errorf("替换性能报告: %w", err)
	}
	promoted = true
	return nil
}

func writeBenchmarkReportBytes(file io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := file.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

func syncBenchmarkReportDirectory(path string, fs benchmarkReportFS) error {
	directory, err := fs.openDirectory(path)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

func rollbackBenchmarkReport(
	outputPath string,
	oldData []byte,
	hadOld bool,
	fs benchmarkReportFS,
) error {
	var rollbackErr error
	if hadOld {
		rollbackErr = writeSyncedBenchmarkTemp(outputPath, oldData, fs)
	} else if err := fs.remove(outputPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		rollbackErr = err
	}
	return errors.Join(rollbackErr, syncBenchmarkReportDirectory(filepath.Dir(outputPath), fs))
}

func validateBenchmarkReport(report client.PerfReport) error {
	var failures []string
	if report.Persistence.Snapshots <= 0 {
		failures = append(failures, "persistence snapshots=0")
	}
	if report.Transport != "memory" && report.Transport != "tcp" {
		failures = append(failures, fmt.Sprintf("transport=%q", report.Transport))
	}
	if report.Protocol.Bytes == 0 || report.Protocol.EncodeP99MS <= 0 || report.Protocol.DecodeP99MS <= 0 {
		failures = append(failures, fmt.Sprintf("protocol 指标不完整: %+v", report.Protocol))
	}
	if report.Protocol.EncodeP99MS >= 1 || report.Protocol.DecodeP99MS >= 1 {
		failures = append(failures, fmt.Sprintf("protocol p99 超过 1ms: %+v", report.Protocol))
	}
	if report.PlayerPersistence.Snapshots <= 0 {
		failures = append(failures, "player_persistence snapshots=0")
	}
	if report.PlayerPersistence.P99MS >= 5 || report.PlayerPersistence.MaxMS >= 20 {
		failures = append(failures, fmt.Sprintf("player_persistence 超过 p99/max 5/20ms: %+v", report.PlayerPersistence))
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
	if report.Ticks.P99MS >= 15 {
		failures = append(failures, fmt.Sprintf("tick p99 %.3f ms >= 15 ms", report.Ticks.P99MS))
	}
	if report.Ticks.MaxMS >= 50 {
		failures = append(failures, fmt.Sprintf("tick max %.3f ms >= 50 ms", report.Ticks.MaxMS))
	}
	if len(failures) > 0 {
		return fmt.Errorf("%s", strings.Join(failures, "；"))
	}
	return nil
}

func measureProtocolSummary() (client.ProtocolSummary, error) {
	codec, err := network.NewCodec()
	if err != nil {
		return client.ProtocolSummary{}, err
	}
	defer codec.Close()
	packet := network.PlayerInput{Sequence: 1, MoveX: 1, MoveZ: -1, Jump: true, Yaw: 0.5, Pitch: -0.25}
	const samples = 2048
	encode := make([]float64, samples)
	decode := make([]float64, samples)
	var bytes uint64
	for index := range samples {
		started := time.Now()
		packetID, payload, encodeErr := codec.EncodeClient(network.StatePlay, packet)
		encode[index] = float64(time.Since(started).Nanoseconds()) / float64(time.Millisecond)
		if encodeErr != nil {
			return client.ProtocolSummary{}, encodeErr
		}
		bytes += uint64(len(payload))
		started = time.Now()
		if _, decodeErr := codec.DecodeClient(network.StatePlay, packetID, payload); decodeErr != nil {
			return client.ProtocolSummary{}, decodeErr
		}
		decode[index] = float64(time.Since(started).Nanoseconds()) / float64(time.Millisecond)
		packet.Sequence++
	}
	slices.Sort(encode)
	slices.Sort(decode)
	return client.ProtocolSummary{
		EncodeP99MS: durationP99(encode),
		DecodeP99MS: durationP99(decode),
		Bytes:       bytes,
	}, nil
}

func measurePlayerPersistenceSummary() (client.PersistenceSummary, error) {
	store := storage.NewMemory(storage.Metadata{
		FormatVersion: 1, Seed: benchmarkSeed, SpawnDimension: core.Overworld,
	})
	id := core.PlayerID{0xa1, 0x63, 0xd4, 0x99, 0x36, 0x55, 0x43, 0xd5, 0x87, 0x30, 0xe5, 0x9d, 0x11, 0x0c, 0x21, 0x76}
	recorder := newSaveRecorder(256)
	ctx := context.Background()
	for revision := uint64(1); revision <= 256; revision++ {
		started := time.Now()
		if _, err := store.SavePlayer(ctx, storage.PlayerSave{
			PlayerID: id, Revision: revision, DisplayName: "Benchmark",
			Current: storage.PlayerLocation{
				Dimension: core.Overworld,
				Position:  [3]float32{float32(revision) / 100, 80, -2},
			},
		}); err != nil {
			return client.PersistenceSummary{}, err
		}
		if _, err := store.LoadPlayer(ctx, id); err != nil {
			return client.PersistenceSummary{}, err
		}
		recorder.add(time.Since(started))
	}
	return recorder.summary(), nil
}

func durationP99(sorted []float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	index := int(math.Ceil(float64(len(sorted))*0.99)) - 1
	return sorted[max(0, min(index, len(sorted)-1))]
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
