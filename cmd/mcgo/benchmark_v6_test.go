//go:build darwin

package main

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/client"
	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/server"
	"minecraft-go/internal/storage"
	"minecraft-go/internal/worldgen"
)

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

func TestScenarioV8ContainsSevenSortedUnicodeRemotePlayers(t *testing.T) {
	if scenarioVersion != 8 {
		t.Fatalf("scenarioVersion=%d, want 8", scenarioVersion)
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

func TestScenarioV8GPUCompletionTimesOnlySubmitAndPoll(t *testing.T) {
	app, dev := newRemoteRenderApplication(t, &integrationGlyphSource{})
	probe, err := newMultiplayerClientProbe(app)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(probe.Close)

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
	if got := probe.gpuComplete.Summary().Samples; got != 2048 {
		t.Fatalf("GPU samples=%d, want 2048", got)
	}
	want := []string{"finish", "now", "submit", "poll", "now", "release"}
	if got, expected := len(dev.events), 2048*len(want); got != expected {
		t.Fatalf("GPU events=%d, want %d", got, expected)
	}
	for sample := range 2048 {
		start := sample * len(want)
		if got := dev.events[start : start+len(want)]; !reflect.DeepEqual(got, want) {
			t.Fatalf("sample %d events=%v, want=%v", sample, got, want)
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
				storage.NewMemory(storage.Metadata{FormatVersion: 1, Seed: benchmarkSeed, SpawnDimension: core.Overworld}),
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
			FormatVersion:  1,
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
	})
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
	})
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
	multiplayer, ticks, err := measureMultiplayerServerProbe(10 * time.Second)
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
	report := validBenchmarkReport()
	report.ScenarioVersion = 6
	report.Ticks.P99MS = 9.999
	report.Multiplayer = validMultiplayerSummary()
	if err := validateBenchmarkReport(report); err != nil {
		t.Fatalf("9.999ms tick p99 rejected: %v", err)
	}
	report.Ticks.P99MS = 10
	if err := validateBenchmarkReport(report); err == nil || !strings.Contains(err.Error(), ">= 10 ms") {
		t.Fatalf("10ms tick p99 boundary error=%v", err)
	}
}

func TestScenarioV8BenchmarkReportRequires2048GPUCompletionSamples(t *testing.T) {
	report := validBenchmarkReport()
	report.ScenarioVersion = 8
	report.Multiplayer = validMultiplayerSummary()
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
