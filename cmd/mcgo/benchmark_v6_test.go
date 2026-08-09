//go:build darwin

package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"math"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/client"
	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/render"
	"minecraft-go/internal/server"
	"minecraft-go/internal/storage"
	"minecraft-go/internal/worldgen"
)

func readFloat32(data []byte, offset int) float32 {
	return math.Float32frombits(binary.LittleEndian.Uint32(data[offset:]))
}

type benchmarkBlockingServerStream struct {
	entered chan struct{}
	release chan struct{}
}

func (stream *benchmarkBlockingServerStream) Send(
	ctx context.Context,
	_ network.State,
	_ network.ServerPacket,
) error {
	stream.entered <- struct{}{}
	select {
	case <-stream.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (*benchmarkBlockingServerStream) Recv(
	context.Context,
	network.State,
) (network.ClientPacket, error) {
	return nil, errors.New("unused benchmark Recv")
}

func (*benchmarkBlockingServerStream) Peer() string { return "benchmark-blocking" }
func (*benchmarkBlockingServerStream) Close() error { return nil }

func TestScenarioV14ContainsSevenSortedUnicodeRemotePlayers(t *testing.T) {
	if scenarioVersion != 14 {
		t.Fatalf("scenarioVersion=%d, want 14", scenarioVersion)
	}
	scenario := newMultiplayerBenchmarkScenario()
	if !scenario.LocalPlayerID.Valid() {
		t.Fatalf("local PlayerID invalid: %x", scenario.LocalPlayerID)
	}
	if len(scenario.Spawns) != 7 || len(scenario.Tags) != 7 {
		t.Fatalf("spawns/tags = %d/%d, want 7/7", len(scenario.Spawns), len(scenario.Tags))
	}
	for index, spawn := range scenario.Spawns {
		if spawn.PlayerID == scenario.LocalPlayerID || !spawn.PlayerID.Valid() ||
			spawn.DisplayName == "" || len([]rune(spawn.DisplayName)) == len(spawn.DisplayName) {
			t.Fatalf("spawn[%d] does not prove distinct Unicode identity: %+v", index, spawn)
		}
		if scenario.Tags[index].PlayerID != spawn.PlayerID ||
			scenario.Tags[index].Text != spawn.DisplayName {
			t.Fatalf("tag[%d]=%+v does not match spawn=%+v", index, scenario.Tags[index], spawn)
		}
	}
	batch := scenario.States(42)
	if batch.ServerTick != 42 || len(batch.Players) != 7 {
		t.Fatalf("tick batch = %+v, want tick 42/count 7", batch)
	}
	for index := 1; index < len(batch.Players); index++ {
		if bytes.Compare(batch.Players[index-1].PlayerID[:], batch.Players[index].PlayerID[:]) >= 0 {
			t.Fatalf("states not strictly sorted at %d", index)
		}
	}
	if err := batch.Validate(); err != nil {
		t.Fatalf("scenario batch invalid: %v", err)
	}
	for _, tag := range scenario.Tags {
		if !strings.ContainsAny(tag.Text, "界月星河山海云") {
			t.Fatalf("tag is not the fixed Unicode fixture: %q", tag.Text)
		}
	}
}

func TestScenarioV8GPUCompletionStopsWhenTransportCloseFails(t *testing.T) {
	serverCloseErr := errors.New("注入服务端关闭失败")
	clientCloseErr := errors.New("注入客户端关闭失败")
	for _, test := range []struct {
		name      string
		serverErr error
		clientErr error
		want      error
	}{
		{name: "服务端", serverErr: serverCloseErr, want: serverCloseErr},
		{name: "客户端", clientErr: clientCloseErr, want: clientCloseErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			app, _ := newRemoteRenderApplication(t, &integrationGlyphSource{})
			config := server.DefaultConfig(benchmarkSeed)
			config.TrustedObserver = true
			running := server.NewWorld(
				config,
				worldgen.New(benchmarkSeed),
				storage.NewMemory(storage.Metadata{FormatVersion: 2, Seed: benchmarkSeed, SpawnDimension: core.Overworld}),
			)
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := running.Shutdown(ctx); err != nil {
					t.Errorf("关闭测试服务端: %v", err)
				}
			})

			rawClient, rawServer := network.NewMemoryPair(8)
			clientEndpoint := &benchmarkCloseErrorClientEndpoint{ClientEndpoint: rawClient, err: test.clientErr}
			serverEndpoint := &benchmarkCloseErrorServerEndpoint{ServerEndpoint: rawServer, err: test.serverErr}
			if err := running.AttachTrustedObserver(serverEndpoint); err != nil {
				t.Fatal(err)
			}
			app.server = running
			app.clientEndpoint = clientEndpoint

			probe, err := newMultiplayerClientProbe(app)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(probe.Close)
			clockReads := 0
			probe.now = func() time.Time {
				clockReads++
				return time.Unix(0, int64(clockReads)*int64(time.Millisecond))
			}

			err = probe.measureGPUCompletionAfterTransportClose(app)
			if !errors.Is(err, test.want) {
				t.Fatalf("measureGPUCompletionAfterTransportClose error=%v，想要 %v", err, test.want)
			}
			if clockReads != 0 {
				t.Fatalf("关闭失败后 GPU 时钟读取=%d，想要 0", clockReads)
			}
			if got := serverEndpoint.closeCalls.Load(); got != 1 {
				t.Fatalf("服务端 Close 调用=%d，想要 1", got)
			}
			if got := clientEndpoint.closeCalls.Load(); got != 1 {
				t.Fatalf("客户端 Close 调用=%d，想要 1", got)
			}
		})
	}
}

type benchmarkCloseErrorClientEndpoint struct {
	network.ClientEndpoint
	err        error
	closeCalls atomic.Int32
}

func (endpoint *benchmarkCloseErrorClientEndpoint) Close() error {
	endpoint.closeCalls.Add(1)
	return errors.Join(endpoint.ClientEndpoint.Close(), endpoint.err)
}

type benchmarkCloseErrorServerEndpoint struct {
	network.ServerEndpoint
	err        error
	closeCalls atomic.Int32
}

func (endpoint *benchmarkCloseErrorServerEndpoint) Close() error {
	endpoint.closeCalls.Add(1)
	return errors.Join(endpoint.ServerEndpoint.Close(), endpoint.err)
}

func TestScenarioV8GPUCompletionStartsAfterTransportTeardown(t *testing.T) {
	app, _ := newRemoteRenderApplication(t, &integrationGlyphSource{})
	config := server.DefaultConfig(benchmarkSeed)
	config.TrustedObserver = true
	config.ViewRadius = 0
	config.Workers = 1
	config.SaveWorkers = 1
	running := server.NewWorld(
		config,
		worldgen.New(benchmarkSeed),
		storage.NewMemory(storage.Metadata{
			FormatVersion:  2,
			Seed:           benchmarkSeed,
			SpawnDimension: core.Overworld,
		}),
	)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := running.Shutdown(ctx); err != nil {
			t.Errorf("关闭测试服务端: %v", err)
		}
	})
	rawClient, serverEndpoint := network.NewMemoryPair(8)
	clientEndpoint := &connectionTestEndpoint{ClientEndpoint: rawClient}
	if err := running.AttachTrustedObserver(serverEndpoint); err != nil {
		t.Fatal(err)
	}
	app.server = running
	app.clientEndpoint = clientEndpoint

	probe, err := newMultiplayerClientProbe(app)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(probe.Close)
	clockReads := 0
	probe.now = func() time.Time {
		if clockReads == 0 {
			if err := running.SetTrustedObserverCenter(core.Overworld, core.ChunkPos{}); !errors.Is(err, server.ErrTrustedObserverDisabled) {
				t.Fatalf("首个 GPU 时钟读取时 trusted observer 仍挂载: %v", err)
			}
			if got := clientEndpoint.closeCalls.Load(); got != 1 {
				t.Fatalf("首个 GPU 时钟读取时客户端 Close 调用=%d，想要 1", got)
			}
		}
		clockReads++
		return time.Unix(0, int64(clockReads)*int64(time.Millisecond))
	}

	if err := probe.measureGPUCompletionAfterTransportClose(app); err != nil {
		t.Fatal(err)
	}
	if clockReads == 0 {
		t.Fatal("GPU 探针未读取时钟")
	}
}

func TestScenarioV7RenderFrameSamplesExistingRemotePassesExactlyOnce(t *testing.T) {
	app, dev := newRemoteRenderApplication(t, &integrationGlyphSource{})
	if err := app.remotePlayers.Apply(remoteSpawn(1, "星河", 1, mgl32.Vec3{0, 0, -4})); err != nil {
		t.Fatal(err)
	}
	timing := newMultiplayerRenderTiming()
	app.multiplayerRenderTiming = timing
	var nowCalls int
	app.multiplayerRenderNow = func() time.Time {
		nowCalls++
		return time.Unix(0, int64(nowCalls)*int64(time.Millisecond))
	}
	rendered, err := app.renderFrame(1)
	if err != nil || !rendered {
		t.Fatalf("renderFrame=(%v,%v)", rendered, err)
	}
	if got, want := dev.lastPasses(), []string{"terrain pass", "avatar pass", "name-tag pass"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("passes=%v want exactly one existing pass each: %v", got, want)
	}
	avatar, nameTag := timing.Summaries()
	if avatar != (client.LatencySummary{Samples: 1, P50MS: 1, P95MS: 1, P99MS: 1, MaxMS: 1}) {
		t.Fatalf("avatar timing=%+v, want one 1ms actual Render sample", avatar)
	}
	if nameTag != (client.LatencySummary{Samples: 1, P50MS: 2, P95MS: 2, P99MS: 2, MaxMS: 2}) {
		t.Fatalf("name-tag timing=%+v, want Prepare+Render sum of 2ms", nameTag)
	}
	if nowCalls != 6 {
		t.Fatalf("clock reads=%d, want exactly three start/end pairs", nowCalls)
	}
}

func TestScenarioV7NilRenderTimingNeverReadsBenchmarkClock(t *testing.T) {
	app, dev := newRemoteRenderApplication(t, &integrationGlyphSource{})
	if err := app.remotePlayers.Apply(remoteSpawn(1, "月海", 1, mgl32.Vec3{0, 0, -4})); err != nil {
		t.Fatal(err)
	}
	app.multiplayerRenderTiming = nil
	app.multiplayerRenderNow = func() time.Time {
		panic("nil benchmark timing read wall clock")
	}
	if rendered, err := app.renderFrame(1); err != nil || !rendered {
		t.Fatalf("nil timing renderFrame=(%v,%v)", rendered, err)
	}
	if got, want := dev.lastPasses(), []string{"terrain pass", "avatar pass", "name-tag pass"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("nil timing changed passes=%v want=%v", got, want)
	}
}

func TestScenarioV7NameTagFailurePublishesNoRenderTimingSample(t *testing.T) {
	wantErr := errors.New("injected glyph flush failure")
	app, dev := newRemoteRenderApplication(t, &integrationGlyphSource{flushErr: wantErr})
	if err := app.remotePlayers.Apply(remoteSpawn(1, "云野", 1, mgl32.Vec3{0, 0, -4})); err != nil {
		t.Fatal(err)
	}
	timing := newMultiplayerRenderTiming()
	app.multiplayerRenderTiming = timing
	app.multiplayerRenderNow = time.Now
	rendered, err := app.renderFrame(1)
	if rendered || !errors.Is(err, wantErr) {
		t.Fatalf("renderFrame=(%v,%v), want wrapped glyph error", rendered, err)
	}
	avatar, nameTag := timing.Summaries()
	if avatar.Samples != 0 || nameTag.Samples != 0 {
		t.Fatalf("failed frame published successful timing: avatar=%+v nameTag=%+v", avatar, nameTag)
	}
	if got := dev.lastPasses(); len(got) != 0 {
		t.Fatalf("glyph failure encoded passes=%v, want none", got)
	}
}

func TestBenchmarkServerMeasuredWindowSendsOneSequencePerCompletedTick(t *testing.T) {
	epoch := newBenchmarkServerEpoch()
	testCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	sent := make(chan uint64, 1)
	statsObserved := make(chan struct{}, 1)
	var sequences []uint64
	var statsCalls, rssCalls int
	sendInputs := func(_ context.Context, sequence uint64) error {
		sequences = append(sequences, sequence)
		sent <- sequence
		return nil
	}
	if err := epoch.beginMeasurement(testCtx, runBenchmarkTestInputBoundary, func() error {
		return sendInputs(testCtx, 1)
	}); err != nil {
		t.Fatal(err)
	}
	publisherDone := make(chan struct{})
	go func() {
		defer close(publisherDone)
		for range benchmarkServerMeasuredTicks {
			select {
			case <-sent:
				epoch.observeTick(time.Millisecond)
				select {
				case <-statsObserved:
				case <-testCtx.Done():
					return
				}
				select {
				case <-time.After(2 * time.Millisecond):
				case <-testCtx.Done():
					return
				}
			case <-testCtx.Done():
				return
			}
		}
	}()
	summary, err := runBenchmarkServerMeasuredWindow(
		testCtx,
		epoch,
		8,
		runBenchmarkTestInputBoundary,
		sendInputs,
		func() server.HostStats {
			statsCalls++
			statsObserved <- struct{}{}
			return server.HostStats{
				ActivePlayers: 8, MaxSessionOutboxDepth: 5,
				PlayerSaveJobDepth: 6, PlayerSaveDoneDepth: 1,
			}
		},
		func() (uint64, error) {
			rssCalls++
			return 123, nil
		},
	)
	<-publisherDone
	if err != nil {
		t.Fatal(err)
	}
	if len(sequences) != benchmarkServerMeasuredTicks {
		t.Fatalf("input sequences=%d want=%d", len(sequences), benchmarkServerMeasuredTicks)
	}
	for index, sequence := range sequences {
		if want := uint64(index + 1); sequence != want {
			t.Fatalf("sequence[%d]=%d want=%d", index, sequence, want)
		}
	}
	if statsCalls != benchmarkServerMeasuredTicks || rssCalls != 10 {
		t.Fatalf("stats/rss calls=%d/%d want=%d/10", statsCalls, rssCalls, benchmarkServerMeasuredTicks)
	}
	if summary != (benchmarkServerWindowSummary{
		outboxHigh: 5, jobsHigh: 6, doneHigh: 1, peakRSS: 123,
	}) {
		t.Fatalf("summary=%+v", summary)
	}
}

func TestBenchmarkServerMeasuredWindowRejectsTickAdvanceBeforeStatsBoundary(t *testing.T) {
	epoch := newBenchmarkServerEpoch()
	testCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	sent := make(chan uint64, 1)
	sendInputs := func(_ context.Context, sequence uint64) error {
		sent <- sequence
		return nil
	}
	if err := epoch.beginMeasurement(testCtx, runBenchmarkTestInputBoundary, func() error {
		return sendInputs(testCtx, 1)
	}); err != nil {
		t.Fatal(err)
	}
	publisherDone := make(chan struct{})
	go func() {
		defer close(publisherDone)
		select {
		case <-sent:
			epoch.observeTick(time.Millisecond)
			epoch.observeTick(time.Millisecond)
		case <-testCtx.Done():
		}
	}()
	releaseStats := make(chan struct{})
	var statsCalls atomic.Int64
	controllerDone := make(chan error, 1)
	go func() {
		_, err := runBenchmarkServerMeasuredWindow(
			testCtx,
			epoch,
			8,
			runBenchmarkTestInputBoundary,
			sendInputs,
			func() server.HostStats {
				if statsCalls.Add(1) == 1 {
					<-releaseStats
				}
				return server.HostStats{ActivePlayers: 8}
			},
			func() (uint64, error) { return 1, nil },
		)
		controllerDone <- err
	}()
	var controllerErr error
	select {
	case controllerErr = <-controllerDone:
	case <-time.After(100 * time.Millisecond):
		close(releaseStats)
		controllerErr = <-controllerDone
	}
	<-publisherDone
	if controllerErr == nil || !strings.Contains(controllerErr.Error(), "Stats") {
		t.Fatalf("tick advanced before Stats boundary error=%v", controllerErr)
	}
	if got := statsCalls.Load(); got != 1 {
		t.Fatalf("stats calls=%d want=1 fail-fast sample", got)
	}
}

func TestBenchmarkServerMeasuredWindowArmsInputsInsideStepBoundary(t *testing.T) {
	epoch := newBenchmarkServerEpoch()
	testCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	insideBoundary := false
	inputBoundary := func(_ context.Context, _ uint64, action func() error) error {
		insideBoundary = true
		defer func() { insideBoundary = false }()
		return action()
	}
	stopAfterAssertion := errors.New("stop after boundary assertion")
	sendInputs := func(_ context.Context, sequence uint64) error {
		if !insideBoundary {
			return errors.New("input sent outside step boundary")
		}
		if sequence == 2 {
			return stopAfterAssertion
		}
		return nil
	}
	if err := epoch.beginMeasurement(testCtx, inputBoundary, func() error {
		return sendInputs(testCtx, 1)
	}); err != nil {
		t.Fatal(err)
	}
	epoch.observeTick(time.Millisecond)
	_, err := runBenchmarkServerMeasuredWindow(
		testCtx,
		epoch,
		8,
		inputBoundary,
		sendInputs,
		func() server.HostStats { return server.HostStats{ActivePlayers: 8} },
		func() (uint64, error) { return 1, nil },
	)
	if !errors.Is(err, stopAfterAssertion) {
		t.Fatalf("input boundary error=%v, want assertion stop", err)
	}
}

func TestBenchmarkServerMeasuredWindowRejectsInputBoundaryPastTickDeadline(t *testing.T) {
	epoch := newBenchmarkServerEpoch()
	testCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := epoch.beginMeasurement(testCtx, runBenchmarkTestInputBoundary, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	epoch.observeTick(fixedBenchmarkFrameDuration - 20*time.Millisecond)
	blockingBoundary := func(ctx context.Context, _ uint64, _ func() error) error {
		<-ctx.Done()
		return ctx.Err()
	}
	_, err := runBenchmarkServerMeasuredWindow(
		testCtx,
		epoch,
		8,
		blockingBoundary,
		func(context.Context, uint64) error { return nil },
		func() server.HostStats { return server.HostStats{ActivePlayers: 8} },
		func() (uint64, error) { return 1, nil },
	)
	if err == nil || !strings.Contains(err.Error(), "50ms") {
		t.Fatalf("input boundary deadline error=%v", err)
	}
}

func TestBenchmarkServerInputDeadlineUsesScheduledTickTime(t *testing.T) {
	scheduled := time.Now().Add(100 * time.Millisecond)
	deadline, err := benchmarkServerInputDeadline(benchmarkServerTickSignal{
		scheduled: scheduled,
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := scheduled.Add(fixedBenchmarkFrameDuration); !deadline.Equal(want) {
		t.Fatalf("input deadline=%s want scheduled deadline=%s", deadline, want)
	}
}

func TestBenchmarkServerInputDeadlineRejectsDelayedStepStart(t *testing.T) {
	_, err := benchmarkServerInputDeadline(benchmarkServerTickSignal{
		scheduled: time.Now().Add(-fixedBenchmarkFrameDuration),
	}, 0)
	if err == nil || !strings.Contains(err.Error(), "50ms") {
		t.Fatalf("delayed step deadline error=%v", err)
	}
}

func TestCanonicalCountingServerStreamFreezesMeasurementAtSendStart(t *testing.T) {
	for _, test := range []struct {
		name                       string
		measuringAtStart, atFinish bool
		wantCount                  bool
	}{
		{name: "measured send finishes after close", measuringAtStart: true, wantCount: true},
		{name: "warm-up send finishes after open", atFinish: true, wantCount: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			epoch := newBenchmarkServerEpoch()
			if test.measuringAtStart {
				epoch.phase.Store(uint32(benchmarkServerEpochMeasuring))
			}
			inner := &benchmarkBlockingServerStream{
				entered: make(chan struct{}, 1),
				release: make(chan struct{}),
			}
			codec, err := network.NewCodec()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = codec.Close() })
			var counted atomic.Uint64
			stream := &canonicalCountingServerStream{
				inner: inner, codec: codec, bytes: &counted, epoch: epoch,
			}
			done := make(chan error, 1)
			go func() {
				done <- stream.Send(
					context.Background(), network.StatePlay,
					network.PlayerState{ServerTick: 1},
				)
			}()
			<-inner.entered
			if test.atFinish {
				epoch.phase.Store(uint32(benchmarkServerEpochMeasuring))
			} else {
				epoch.phase.Store(uint32(benchmarkServerEpochDone))
			}
			close(inner.release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if got := counted.Load() > 0; got != test.wantCount {
				t.Fatalf("counted=%v want=%v bytes=%d", got, test.wantCount, counted.Load())
			}
		})
	}
}

func TestScenarioV7EightSessionServerProbeIsRealAndBounded(t *testing.T) {
	// 收集预算而非阈值。measureMultiplayerServerProbe 要求 >= 10s，此前恰好
	// 传 10s，预算等于被调用方下限是不健康的构造，因此放宽到 30s；放宽不动
	// 下面任何一条界限断言。
	//
	// 但要说清楚：**这不是本测试在 CI 上变红的成因**。实测四次红的断言都是
	// multiplayer_benchmark.go 的 "server input boundary 已错过 50ms tick
	// deadline"，耗时 2.43s–7.75s，远在原预算之内——预算从来不是绑定约束，
	// 放宽它对那一形态无效。真正的成因见
	// docs/superpowers/specs/2026-08-07-ci-stability-merge-gate-design.md §4，
	// 需要单独处理。
	multiplayer, ticks, err := measureMultiplayerServerProbe(30 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if multiplayer.ServerOutboundBytes == 0 ||
		multiplayer.InterestDiff.Samples != benchmarkServerInterestSamples ||
		ticks.Frames != benchmarkServerMeasuredTicks ||
		multiplayer.OutboxHighWater > benchmarkOutboxLimit ||
		multiplayer.PlayerJobsHighWater > 16 || multiplayer.PlayerDoneHighWater > 2 ||
		multiplayer.PeakRSSBytes == 0 || multiplayer.PeakRSSBytes >= 2<<30 {
		t.Fatalf("incomplete bounded server probe: multiplayer=%+v ticks=%+v", multiplayer, ticks)
	}
}

func TestPerformanceThresholdsRejectTickP99AtTenMilliseconds(t *testing.T) {
	report := completeBenchmarkReport()
	report.Ticks.P99MS = 9.999
	if err := validateBenchmarkReport(report); err != nil {
		t.Fatalf("9.999ms tick p99 rejected: %v", err)
	}
	report.Ticks.P99MS = 10
	if err := validateBenchmarkReport(report); err != nil {
		t.Fatalf("10ms tick p99 should only be recorded: %v", err)
	}
}

func TestWriteBenchmarkReportRecordsPerformanceOutsideThresholds(t *testing.T) {
	report := completeBenchmarkReport()
	for _, name := range []string{"still", "flying"} {
		phase := report.Phases[name]
		phase.FPS = 1
		phase.P99MS = 99
		phase.MaxMS = 99
		phase.PeakRSSBytes = 3 << 30
		report.Phases[name] = phase
	}
	report.Ticks.P99MS = 99
	report.Ticks.MaxMS = 99
	report.Protocol.EncodeP99MS = 9
	report.Protocol.DecodeP99MS = 9
	report.PlayerPersistence.P99MS = 99
	report.PlayerPersistence.MaxMS = 99
	report.Multiplayer.OutboxHighWater = 999
	report.Multiplayer.PlayerJobsHighWater = 999
	report.Multiplayer.PlayerDoneHighWater = 999
	report.Multiplayer.PeakRSSBytes = 3 << 30
	path := t.TempDir() + "/report.json"
	if err := writeBenchmarkReport(path, report); err != nil {
		t.Fatalf("性能数值越界的完整报告未写出: %v", err)
	}
	if records := benchmarkPerformanceRecords(report); len(records) == 0 {
		t.Fatal("越界性能数值未留下记录")
	}
}

func TestValidateBenchmarkReportStillRejectsIncompleteSamples(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*client.PerfReport)
	}{
		{name: "missing phase", mutate: func(report *client.PerfReport) { delete(report.Phases, "still") }},
		{name: "phase samples", mutate: func(report *client.PerfReport) {
			phase := report.Phases["flying"]
			phase.Frames = 0
			report.Phases["flying"] = phase
		}},
		{name: "provenance", mutate: func(report *client.PerfReport) { report.Hardware = "  " }},
		{name: "rss", mutate: func(report *client.PerfReport) { report.Phases["still"] = client.PhaseSummary{} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			report := completeBenchmarkReport()
			test.mutate(&report)
			if err := validateBenchmarkReport(report); err == nil {
				t.Fatal("不完整报告未被拒绝")
			}
		})
	}
}

func TestBenchmarkServerProbeValidityIgnoresHighWaterButRejectsOverflow(t *testing.T) {
	report := completeBenchmarkReport()
	report.Multiplayer.OutboxHighWater = 999
	report.Multiplayer.PlayerJobsHighWater = 999
	report.Multiplayer.PlayerDoneHighWater = 999
	if err := validateBenchmarkReport(report); err != nil {
		t.Fatalf("队列高水位不应使完整报告失败: %v", err)
	}
	if !validBenchmarkServerProbe(false, 1, benchmarkServerInterestSamples, benchmarkServerMeasuredTicks, 1) {
		t.Fatal("完整服务端探针被拒绝")
	}
	if validBenchmarkServerProbe(true, 1, benchmarkServerInterestSamples, benchmarkServerMeasuredTicks, 1) {
		t.Fatal("真实 overflow 未被拒绝")
	}
	report.Multiplayer.ServerOutboundBytes = 0
	if err := validateBenchmarkReport(report); err == nil {
		t.Fatal("真实探针数据缺失未被拒绝")
	}
}

func TestValidateBenchmarkReportRejectsDroppedSamples(t *testing.T) {
	for _, name := range []string{"still", "flying", "ticks"} {
		t.Run(name, func(t *testing.T) {
			report := completeBenchmarkReport()
			if name == "ticks" {
				report.Ticks.DroppedRingBufferSamples = 1
			} else {
				phase := report.Phases[name]
				phase.DroppedRingBufferSamples = 1
				report.Phases[name] = phase
			}
			if err := validateBenchmarkReport(report); err == nil {
				t.Fatal("丢失环形缓冲样本未被拒绝")
			}
		})
	}
}

func TestScenarioV8BenchmarkReportRequires2048GPUCompletionSamples(t *testing.T) {
	report := completeBenchmarkReport()
	report.ScenarioVersion = 8
	report.Multiplayer.RemoteGPUComplete.Samples = 2047
	if err := validateBenchmarkReport(report); err == nil ||
		!strings.Contains(err.Error(), "remote_gpu_complete") {
		t.Fatalf("2047 GPU samples error=%v", err)
	}
	report.Multiplayer.RemoteGPUComplete.Samples = 2048
	if err := validateBenchmarkReport(report); err != nil {
		t.Fatalf("2048 GPU samples rejected: %v", err)
	}
	report.ScenarioVersion = 7
	report.Multiplayer.RemoteGPUComplete.Samples = 256
	if err := validateBenchmarkReport(report); err != nil {
		t.Fatalf("v7 256 GPU samples rejected: %v", err)
	}
}

func TestScenarioV13BenchmarkReportReusesV12GPUCompletionDefinition(t *testing.T) {
	report := completeBenchmarkReport()
	report.ScenarioVersion = 13
	report.Multiplayer.RemoteGPUComplete.Samples = client.ScenarioV12GPUCompletionSamples - 1
	if err := validateBenchmarkReport(report); err == nil ||
		!strings.Contains(err.Error(), "remote_gpu_complete") {
		t.Fatalf("v13 低于批量分摊样本数未被拒绝: %v", err)
	}
	report.Multiplayer.RemoteGPUComplete.Samples = client.ScenarioV12GPUCompletionSamples
	if err := validateBenchmarkReport(report); err != nil {
		t.Fatalf("v13 批量分摊样本数被拒绝: %v", err)
	}
}

// Mutation killed: still/flying 阶段若不再经过真实 renderFrame，terrain pass
// 中由 sky pipeline 发出的 fullscreen triangle draw 不会出现。
func TestScenarioV13StillFlyingFrameIncludesCelestialSkyDraw(t *testing.T) {
	for _, phase := range []struct {
		name   string
		flying bool
	}{
		{name: "still"},
		{name: "flying", flying: true},
	} {
		t.Run(phase.name, func(t *testing.T) {
			app, dev := newRemoteRenderApplication(t, &integrationGlyphSource{})
			probe, err := newMultiplayerClientProbe(app)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(probe.Close)

			updated := false
			var update func(time.Duration)
			if phase.flying {
				update = func(time.Duration) { updated = true }
			}
			summary, err := measurePhase(app, probe, phase.name, 50*time.Millisecond, update)
			if err != nil {
				t.Fatal(err)
			}
			if summary.Frames == 0 {
				t.Fatal("measurePhase 未执行真实 renderFrame")
			}
			if phase.flying && !updated {
				t.Fatal("flying measurePhase 未执行相机更新")
			}

			// sky fullscreen triangle 只由 terrain pass 发出；断言它先于地形 indirect draw。
			skyIndex, indirectIndex := -1, -1
			for i, draw := range dev.draws {
				if draw == "sky triangle" && skyIndex < 0 {
					skyIndex = i
				}
				if draw == "indirect" && indirectIndex < 0 {
					indirectIndex = i
				}
			}
			if skyIndex < 0 || indirectIndex < 0 || skyIndex > indirectIndex {
				t.Fatalf("draws=%v，sky/terrain draw 顺序=%d/%d", dev.draws, skyIndex, indirectIndex)
			}
			sky := dev.bufferByLabel(t, "sky uniform")
			dayNight := render.DayNightAt(app.worldTimeTicks)
			if got := readFloat32(sky.data, 76); got != dayNight.Daylight {
				t.Fatalf("sky Daylight=%v want=%v", got, dayNight.Daylight)
			}
		})
	}
}

func validMultiplayerSummary() client.MultiplayerSummary {
	latency := client.LatencySummary{Samples: 256, P50MS: 0.001, P95MS: 0.002, P99MS: 0.003, MaxMS: 0.004}
	return client.MultiplayerSummary{
		RemoteStateEncode: latency, RemoteStateDecode: latency,
		InterestDiff: client.LatencySummary{Samples: 1000, P50MS: 0.001, P95MS: 0.002, P99MS: 0.003, MaxMS: 0.004},
		RosterApply:  latency, Interpolation: latency, AvatarSubmit: latency,
		NameTagSubmit: latency, RemoteGPUComplete: latency,
		ServerOutboundBytes: 1, OutboxHighWater: 1, PlayerJobsHighWater: 1,
		PlayerDoneHighWater: 1, PeakRSSBytes: 1,
	}
}

func completeBenchmarkReport() client.PerfReport {
	report := validBenchmarkReport()
	report.ScenarioVersion = 14
	report.Hardware = "test-hardware"
	report.OS = "test-os"
	report.GoVersion = "test-go"
	report.GitCommit = "test-commit"
	report.Framebuffer = "2560x1440"
	report.Phases = map[string]client.PhaseSummary{
		"still":  {Frames: 1000, FPS: 100, P50MS: 1, P95MS: 2, P99MS: 3, MaxMS: 4, PeakRSSBytes: 1},
		"flying": {Frames: 1000, FPS: 100, P50MS: 1, P95MS: 2, P99MS: 3, MaxMS: 4, PeakRSSBytes: 1},
	}
	report.Ticks = client.PhaseSummary{Frames: 200, P50MS: 1, P95MS: 2, P99MS: 3, MaxMS: 4}
	report.Persistence = client.PersistenceSummary{Snapshots: 1, P50MS: 1, P95MS: 2, P99MS: 3, MaxMS: 4}
	report.Multiplayer = validMultiplayerSummary()
	return report
}
