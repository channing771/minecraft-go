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

	"minecraft-go/internal/client"
	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/physics"
	"minecraft-go/internal/storage"
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
	viewDistance := app.render.ViewDistance
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
		rendered, err := app.frame(benchmarkMessageDrainMax, benchmarkMessageDrainMax, physics.FixedDelta)
		if err != nil {
			return 0, err
		}
		if !rendered {
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
