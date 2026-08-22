//go:build darwin

package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/physics"
	"github.com/channing771/mornlea/internal/storage"
)

func (probe *multiplayerClientProbe) measureGPUCompletionAfterTransportClose(
	app *application,
) error {
	serverCloseErr := app.server.CloseTrustedObserver()
	app.closeClientSession(nil)
	if err := errors.Join(serverCloseErr, app.clientCloseErr); err != nil {
		return fmt.Errorf("关闭 GPU 探针传输: %w", err)
	}
	return probe.measureGPUCompletion(app)
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
		FormatVersion: 2, Seed: benchmarkSeed, SpawnDimension: core.Overworld,
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
	wantedChunks := loadedChunkTarget(app)
	lastLog := time.Time{}
	for {
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("固定场景在 %s 内未完成加载：chunks=%d/%d mesher=%+v pending=%d",
				timeout, len(app.loadedChunks), wantedChunks, app.mesher.Stats(),
				app.scheduler.PendingUploads())
		}
		if app.window != nil {
			app.window.Poll()
			if app.window.ShouldClose() {
				app.window.CancelClose()
			}
		}
		rendered, err := app.frame(benchmarkMessageDrainMax, benchmarkMessageDrainMax, physics.FixedDelta)
		if err != nil {
			return 0, err
		}
		if !rendered {
			continue
		}
		if snapshotDuration == 0 && len(app.loadedChunks) == wantedChunks {
			snapshotDuration = time.Since(started)
		}
		if applicationLoadComplete(app, wantedChunks) {
			return snapshotDuration, nil
		}
		if time.Since(lastLog) >= 5*time.Second {
			stats := app.mesher.Stats()
			fmt.Printf("加载中：chunks=%d/%d queued=%d active=%d ready=%d pending=%d\n",
				len(app.loadedChunks), wantedChunks, stats.QueuedJobs, stats.InFlightJobs,
				stats.ReadyResults, app.scheduler.PendingUploads())
			lastLog = time.Now()
		}
	}
}

// loadedChunkTarget 返回当前视距完整初始快照应包含的列数。服务端额外发送
// 一圈边界列，故半径是 `ViewDistance + 1`，这个定义与 `waitUntilLoaded`
// 的历史收敛判据保持完全一致。
func loadedChunkTarget(app *application) int {
	viewDistance := app.render.ViewDistance
	return (2*(viewDistance+1) + 1) * (2*(viewDistance+1) + 1)
}

// applicationLoadComplete 判断初始快照与近环网格上传是否全部收敛。远环
// LOD 的单独收敛仍由 `captureSceneImage` 负责；这里刻意保持 benchmark 的
// 原判据，避免 control 的预加载与正式抓帧有不同的近环就绪定义。
func applicationLoadComplete(app *application, wantedChunks int) bool {
	stats := app.mesher.Stats()
	return len(app.loadedChunks) == wantedChunks &&
		stats.QueuedJobs == 0 &&
		stats.InFlightJobs == 0 &&
		stats.ReadyResults == 0 &&
		stats.DirtySections == 0 &&
		app.scheduler.PendingUploads() == 0
}

// waitUntilLoadedPair 在同一 goroutine 交错推进 LOD on/off control，直到
// 两者的既有加载判据都成立。即便一侧先完成，也继续推进它直到另一侧完成，
// 因为其内嵌 Host 仍会持续发送消息，停止 drain 会让 `client.Receiver`
// 的有界 inbox 溢出。这里不并发调用 renderer，控制次序固定为 on 后 off。
func waitUntilLoadedPair(lodOn, lodOff *application, timeout time.Duration) error {
	return waitUntilLoadedPairWithStep(
		lodOn, lodOff, timeout,
		func(app *application) (bool, error) {
			rendered, err := app.frame(
				benchmarkMessageDrainMax, benchmarkMessageDrainMax, physics.FixedDelta,
			)
			if err != nil || !rendered {
				return false, err
			}
			return applicationLoadComplete(app, loadedChunkTarget(app)), nil
		},
	)
}

// waitUntilLoadedPairWithStep 是 paired control 的最小调度内核。`step` 保留
// 仅用于无 GPU 单元测试的 seam；生产路径始终由 `waitUntilLoadedPair` 注入
// 真实 `application.frame`，从而完整应用服务端消息和网格上传。
func waitUntilLoadedPairWithStep(
	lodOn, lodOff *application,
	timeout time.Duration,
	step func(*application) (bool, error),
) error {
	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("LOD on/off control 在 %s 内未完成加载", timeout)
		}
		lodOnReady, err := step(lodOn)
		if err != nil {
			return fmt.Errorf("LOD-on control: %w", err)
		}
		lodOffReady, err := step(lodOff)
		if err != nil {
			return fmt.Errorf("LOD-off control: %w", err)
		}
		if lodOnReady && lodOffReady {
			return nil
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
		if _, err := app.frame(benchmarkMessageDrainMax, benchmarkMessageDrainMax, physics.FixedDelta); err != nil {
			return err
		}
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
		rendered, err := app.frame(benchmarkMessageDrainMax, steadyFrameMeshWorkMax, physics.FixedDelta)
		if err != nil {
			return err
		}
		if !rendered {
			continue
		}
	}
	return nil
}

func measurePhase(
	app *application,
	multiplayer *multiplayerClientProbe,
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
		multiplayer.tick++
		if err := multiplayer.sampleFrame(app, multiplayer.tick); err != nil {
			return client.PhaseSummary{}, fmt.Errorf("%s 多人 probe tick %d: %w", name, multiplayer.tick, err)
		}
		rendered, err := app.frame(benchmarkMessageDrainMax, steadyFrameMeshWorkMax, fixedBenchmarkFrameDuration)
		if err != nil {
			return client.PhaseSummary{}, err
		}
		if !rendered {
			if time.Since(lastRendered) > 5*time.Second {
				return client.PhaseSummary{}, fmt.Errorf("%s 阶段连续 5 秒取不到 surface 帧", name)
			}
			continue
		}
		lastRendered = time.Now()
		frameMS := float64(time.Since(frameStarted).Microseconds()) / 1000
		stats := app.lastFrameStats
		sampler.Add(client.FrameSample{
			FrameMS:           frameMS,
			CandidateSections: stats.CandidateSections,
			CandidateBytes:    stats.CandidateBytes,
			CandidateFaces:    stats.CandidateFaces,
			PendingUploads:    app.scheduler.PendingUploads(),
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
