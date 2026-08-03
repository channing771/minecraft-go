//go:build darwin

package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
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

const (
	fixedBenchmarkFrameDuration = 50 * time.Millisecond
	benchmarkOutboxLimit        = 512
	benchmarkLatencyCapacity    = 131_072
)

type multiplayerRenderTiming struct {
	avatarSubmit  *client.LatencyRecorder
	nameTagSubmit *client.LatencyRecorder
}

func newMultiplayerRenderTiming() *multiplayerRenderTiming {
	return &multiplayerRenderTiming{
		avatarSubmit:  client.NewLatencyRecorder(benchmarkLatencyCapacity),
		nameTagSubmit: client.NewLatencyRecorder(benchmarkLatencyCapacity),
	}
}

func (timing *multiplayerRenderTiming) recordAvatar(duration time.Duration) {
	timing.avatarSubmit.Add(duration)
}

func (timing *multiplayerRenderTiming) recordNameTag(duration time.Duration) {
	timing.nameTagSubmit.Add(duration)
}

func (timing *multiplayerRenderTiming) Summaries() (client.LatencySummary, client.LatencySummary) {
	return timing.avatarSubmit.Summary(), timing.nameTagSubmit.Summary()
}

type multiplayerBenchmarkScenario struct {
	LocalPlayerID core.PlayerID
	Spawns        []network.RemotePlayerSpawn
	Tags          []render.NameTag
}

func newMultiplayerBenchmarkScenario() multiplayerBenchmarkScenario {
	local := benchmarkPlayerID(0)
	names := [...]string{"星野", "月河", "云山", "海界", "星河", "月海", "云野"}
	spawns := make([]network.RemotePlayerSpawn, len(names))
	tags := make([]render.NameTag, len(names))
	for index, name := range names {
		angle := float64(index) * 2 * math.Pi / float64(len(names))
		position := mgl32.Vec3{float32(math.Cos(angle)) * 4, 80, float32(math.Sin(angle))*4 - 8}
		playerID := benchmarkPlayerID(index + 1)
		spawns[index] = network.RemotePlayerSpawn{
			PlayerID: playerID, DisplayName: name, ServerTick: 1,
			Dimension: core.Overworld, Position: position,
		}
		tags[index] = render.NameTag{
			PlayerID: playerID, Text: name,
			Anchor: position.Add(mgl32.Vec3{0, 2.05, 0}),
		}
	}
	return multiplayerBenchmarkScenario{LocalPlayerID: local, Spawns: spawns, Tags: tags}
}

func (scenario multiplayerBenchmarkScenario) States(tick uint64) network.RemotePlayerStates {
	states := make([]network.RemotePlayerState, len(scenario.Spawns))
	for index, spawn := range scenario.Spawns {
		phase := float64(tick)*0.035 + float64(index)*2*math.Pi/float64(len(scenario.Spawns))
		position := spawn.Position.Add(mgl32.Vec3{
			float32(math.Sin(phase)) * 1.5,
			0,
			float32(math.Cos(phase)) * 1.5,
		})
		states[index] = network.RemotePlayerState{
			PlayerID: spawn.PlayerID, Dimension: core.Overworld, Position: position,
			Yaw:   float32(math.Atan2(math.Sin(phase), math.Cos(phase))),
			Pitch: float32(math.Sin(phase*0.5)) * 0.15,
		}
	}
	return network.RemotePlayerStates{ServerTick: tick, Players: states}
}

func benchmarkPlayerID(index int) core.PlayerID {
	return core.PlayerID{
		0x10, 0, 0, byte(index + 1), 0x20, 0x30, 0x40, byte(index + 1),
		0x80, 0x50, 0x60, 0x70, 0x80, 0x90, 0xa0, byte(index + 1),
	}
}

type multiplayerClientProbe struct {
	app      *application
	scenario multiplayerBenchmarkScenario
	codec    *network.Codec
	roster   *client.RemotePlayers

	encode       *client.LatencyRecorder
	decode       *client.LatencyRecorder
	rosterApply  *client.LatencyRecorder
	interpolate  *client.LatencyRecorder
	renderTiming *multiplayerRenderTiming
	gpuComplete  *client.LatencyRecorder
	tick         uint64
}

func newMultiplayerClientProbe(app *application) (*multiplayerClientProbe, error) {
	codec, err := network.NewCodec()
	if err != nil {
		return nil, err
	}
	probe := &multiplayerClientProbe{
		app:          app,
		scenario:     newMultiplayerBenchmarkScenario(),
		codec:        codec,
		roster:       client.NewRemotePlayers(),
		encode:       client.NewLatencyRecorder(benchmarkLatencyCapacity),
		decode:       client.NewLatencyRecorder(benchmarkLatencyCapacity),
		rosterApply:  client.NewLatencyRecorder(benchmarkLatencyCapacity),
		interpolate:  client.NewLatencyRecorder(benchmarkLatencyCapacity),
		renderTiming: newMultiplayerRenderTiming(),
		gpuComplete:  client.NewLatencyRecorder(256),
		tick:         1,
	}
	for _, spawn := range probe.scenario.Spawns {
		if err := probe.roster.Apply(spawn); err != nil {
			probe.Close()
			return nil, err
		}
		if err := app.remotePlayers.Apply(spawn); err != nil {
			probe.Close()
			return nil, err
		}
	}
	app.multiplayerRenderTiming = probe.renderTiming
	app.multiplayerRenderNow = time.Now
	return probe, nil
}

func (probe *multiplayerClientProbe) Close() {
	if probe != nil && probe.app != nil && probe.app.multiplayerRenderTiming == probe.renderTiming {
		probe.app.multiplayerRenderTiming = nil
		probe.app.multiplayerRenderNow = nil
		probe.app = nil
	}
	if probe != nil && probe.codec != nil {
		probe.codec.Close()
		probe.codec = nil
	}
}

func (probe *multiplayerClientProbe) sampleFrame(app *application, tick uint64) error {
	batch := probe.statesNearCamera(tick, app.camera.Pos)
	started := time.Now()
	packetID, payload, err := probe.codec.EncodeServer(network.StatePlay, batch)
	probe.encode.Add(time.Since(started))
	if err != nil {
		return fmt.Errorf("编码远端状态: %w", err)
	}
	started = time.Now()
	decoded, err := probe.codec.DecodeServer(network.StatePlay, packetID, payload)
	probe.decode.Add(time.Since(started))
	if err != nil {
		return fmt.Errorf("解码远端状态: %w", err)
	}
	message, ok := decoded.(network.ServerMessage)
	if !ok {
		return fmt.Errorf("解码远端状态得到非 ServerMessage: %T", decoded)
	}
	started = time.Now()
	if err := app.remotePlayers.Apply(message); err != nil {
		return fmt.Errorf("应用远端 roster: %w", err)
	}
	probe.rosterApply.Add(time.Since(started))
	if err := probe.roster.Apply(message); err != nil {
		return fmt.Errorf("应用 GPU 样本 roster: %w", err)
	}
	started = time.Now()
	probe.roster.Advance(fixedBenchmarkFrameDuration)
	probe.interpolate.Add(time.Since(started))
	return nil
}

func (probe *multiplayerClientProbe) statesNearCamera(tick uint64, camera mgl32.Vec3) network.RemotePlayerStates {
	batch := probe.scenario.States(tick)
	for index := range batch.Players {
		base := probe.scenario.Spawns[index].Position
		batch.Players[index].Position = batch.Players[index].Position.Sub(base).Add(camera).Add(
			mgl32.Vec3{0, -1.6, -6},
		)
	}
	return batch
}

func benchmarkBillboardCamera(app *application) render.BillboardCamera {
	right := mgl32.Vec3{
		float32(math.Cos(float64(app.camera.Yaw))), 0,
		-float32(math.Sin(float64(app.camera.Yaw))),
	}
	return render.BillboardCamera{
		ViewProj: app.camera.ViewProj(), Right: right,
		Up: right.Cross(app.camera.Forward()).Normalize(),
	}
}

func (probe *multiplayerClientProbe) measureGPUCompletion(app *application) error {
	avatars, tags := remoteRenderPresentations(probe.roster.Presentations())
	for range 256 {
		started := time.Now()
		if err := app.nameTagRenderer.Prepare(tags, app.renderer.UploadBudget()); err != nil {
			return err
		}
		encoder := app.dev.CreateCommandEncoder()
		app.avatarRenderer.Render(encoder, app.colorView, app.depth.view, render.Camera{
			ViewProj: app.camera.ViewProj(), Pos: app.camera.Pos,
		}, avatars)
		app.nameTagRenderer.Render(encoder, app.colorView, app.depth.view, benchmarkBillboardCamera(app))
		command := encoder.Finish()
		app.dev.Submit(command)
		app.dev.Poll(true)
		command.Release()
		probe.gpuComplete.Add(time.Since(started))
	}
	return nil
}

func (probe *multiplayerClientProbe) Summary() client.MultiplayerSummary {
	avatarSubmit, nameTagSubmit := probe.renderTiming.Summaries()
	return client.MultiplayerSummary{
		RemoteStateEncode: probe.encode.Summary(),
		RemoteStateDecode: probe.decode.Summary(),
		RosterApply:       probe.rosterApply.Summary(),
		Interpolation:     probe.interpolate.Summary(),
		AvatarSubmit:      avatarSubmit,
		NameTagSubmit:     nameTagSubmit,
		RemoteGPUComplete: probe.gpuComplete.Summary(),
	}
}

type canonicalCountingServerStream struct {
	inner network.ServerPacketStream
	codec *network.Codec
	bytes *atomic.Uint64
	epoch *benchmarkServerEpoch
	once  sync.Once
}

func (stream *canonicalCountingServerStream) Send(
	ctx context.Context,
	state network.State,
	packet network.ServerPacket,
) error {
	packetID, payload, err := stream.codec.EncodeServer(state, packet)
	if err != nil {
		return err
	}
	packetBytes := uvarintBytes(uint64(packetID)) + len(payload)
	logicalBytes := uvarintBytes(uint64(packetBytes)) + packetBytes
	if err := stream.inner.Send(ctx, state, packet); err != nil {
		return err
	}
	if stream.epoch == nil || stream.epoch.measuring() {
		stream.bytes.Add(uint64(logicalBytes))
	}
	return nil
}

func (stream *canonicalCountingServerStream) Recv(
	ctx context.Context,
	state network.State,
) (network.ClientPacket, error) {
	return stream.inner.Recv(ctx, state)
}

func (stream *canonicalCountingServerStream) Peer() string { return stream.inner.Peer() }

func (stream *canonicalCountingServerStream) Close() error {
	var err error
	stream.once.Do(func() {
		err = stream.inner.Close()
		stream.codec.Close()
	})
	return err
}

func uvarintBytes(value uint64) int {
	var storage [binary.MaxVarintLen64]byte
	return binary.PutUvarint(storage[:], value)
}

type multiplayerServerClient struct {
	endpoint   network.ClientEndpoint
	serverDone <-chan error
	drainDone  <-chan error
}

func sendMultiplayerBenchmarkInputs(
	ctx context.Context,
	clients []multiplayerServerClient,
	sequence uint64,
) error {
	for index, connected := range clients {
		sendCtx, cancelSend := context.WithTimeout(ctx, time.Second)
		err := connected.endpoint.Send(sendCtx, network.PlayerInput{
			Sequence: sequence,
			MoveX:    int8((index+int(sequence))%3 - 1),
			MoveZ:    int8((index*2+int(sequence))%3 - 1),
			Jump:     sequence%40 == uint64(index),
			Yaw:      float32(sequence%360) * math.Pi / 180,
			Pitch:    float32(index-3) * 0.03,
		})
		cancelSend()
		if err != nil {
			return fmt.Errorf("发送固定脚本 player %d tick %d: %w", index, sequence, err)
		}
	}
	return nil
}

type benchmarkServerWindowSummary struct {
	outboxHigh int
	jobsHigh   int
	doneHigh   int
	peakRSS    uint64
}

func runBenchmarkServerMeasuredWindow(
	ctx context.Context,
	epoch *benchmarkServerEpoch,
	wantPlayers int,
	sendInputs func(context.Context, uint64) error,
	readStats func() server.HostStats,
	readRSS func() (uint64, error),
) (benchmarkServerWindowSummary, error) {
	var result benchmarkServerWindowSummary
	for completed := 1; completed <= benchmarkServerMeasuredTicks; completed++ {
		select {
		case signal := <-epoch.signals:
			if !signal.measured {
				return result, fmt.Errorf(
					"measured tick %d 收到 warm-up signal", completed,
				)
			}
		case <-ctx.Done():
			return result, ctx.Err()
		}
		stats := readStats()
		if stats.ActivePlayers != wantPlayers {
			return result, fmt.Errorf(
				"多人服务端 measured tick %d 玩家提前退出: active=%d want=%d",
				completed, stats.ActivePlayers, wantPlayers,
			)
		}
		result.outboxHigh = max(result.outboxHigh, stats.MaxSessionOutboxDepth)
		result.jobsHigh = max(result.jobsHigh, stats.PlayerSaveJobDepth)
		result.doneHigh = max(result.doneHigh, stats.PlayerSaveDoneDepth)
		if completed%20 == 0 {
			rss, err := readRSS()
			if err != nil {
				return result, err
			}
			result.peakRSS = max(result.peakRSS, rss)
		}
		if completed < benchmarkServerMeasuredTicks {
			if err := sendInputs(ctx, uint64(completed+1)); err != nil {
				return result, err
			}
		}
	}
	return result, nil
}

func measureMultiplayerServerProbe(duration time.Duration) (
	client.MultiplayerSummary,
	client.PhaseSummary,
	error,
) {
	if duration < 10*time.Second {
		return client.MultiplayerSummary{}, client.PhaseSummary{},
			fmt.Errorf("多人服务端探针时长 %s < 10s", duration)
	}
	epoch := newBenchmarkServerEpoch()
	config := server.DefaultConfig(benchmarkSeed)
	config.MaxPlayers = 8
	config.ViewRadius = 0
	config.Workers = 1
	config.SaveWorkers = 2
	config.OutboxCapacity = benchmarkOutboxLimit
	config.AutosaveTicks = 20
	config.HeartbeatInterval = time.Hour
	config.HeartbeatTimeout = time.Hour
	config.TickObserver = epoch.observeTick
	config.InterestObserver = epoch.observeInterest
	store := storage.NewMemory(storage.Metadata{
		FormatVersion: 1, Seed: benchmarkSeed,
		SpawnDimension: core.Overworld, SpawnAnchor: core.ChunkPos{},
	})
	host := server.NewHost(config, worldgen.New(benchmarkSeed), store)
	runCtx, cancelRun := context.WithTimeout(
		context.Background(),
		duration+benchmarkServerWarmupTicks*50*time.Millisecond+15*time.Second,
	)
	defer cancelRun()
	runDone := make(chan error, 1)
	go func() { runDone <- host.Run(runCtx, nil) }()

	var outbound atomic.Uint64
	clients := make([]multiplayerServerClient, 0, 8)
	cleanup := func() error {
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelCleanup()
		cleanupErr := host.Shutdown(cleanupCtx)
		for _, connected := range clients {
			cleanupErr = errors.Join(cleanupErr, connected.endpoint.Close())
		}
		for _, connected := range clients {
			select {
			case err := <-connected.drainDone:
				if err != nil && !errors.Is(err, network.ErrClosed) && !errors.Is(err, context.Canceled) {
					cleanupErr = errors.Join(cleanupErr, err)
				}
			case <-cleanupCtx.Done():
				cleanupErr = errors.Join(cleanupErr, cleanupCtx.Err())
			}
			select {
			case err := <-connected.serverDone:
				if err != nil && !errors.Is(err, network.ErrClosed) && !errors.Is(err, context.Canceled) {
					cleanupErr = errors.Join(cleanupErr, err)
				}
			case <-cleanupCtx.Done():
				cleanupErr = errors.Join(cleanupErr, cleanupCtx.Err())
			}
		}
		select {
		case err := <-runDone:
			cleanupErr = errors.Join(cleanupErr, err)
		case <-cleanupCtx.Done():
			cleanupErr = errors.Join(cleanupErr, cleanupCtx.Err())
		}
		return cleanupErr
	}
	cleaned := false
	defer func() {
		if !cleaned {
			_ = cleanup()
		}
	}()

	scenario := newMultiplayerBenchmarkScenario()
	identities := make([]network.Identity, 0, 8)
	identities = append(identities, network.Identity{PlayerID: scenario.LocalPlayerID, DisplayName: "本地玩家"})
	for _, spawn := range scenario.Spawns {
		identities = append(identities, network.Identity{PlayerID: spawn.PlayerID, DisplayName: spawn.DisplayName})
	}
	for _, identity := range identities {
		clientStream, serverStream := network.NewMemoryStreamPair(4096)
		codec, err := network.NewCodec()
		if err != nil {
			_ = clientStream.Close()
			return client.MultiplayerSummary{}, client.PhaseSummary{}, err
		}
		counting := &canonicalCountingServerStream{
			inner: serverStream, codec: codec, bytes: &outbound, epoch: epoch,
		}
		serverDone := make(chan error, 1)
		go func() { serverDone <- host.AcceptStream(runCtx, counting) }()
		loginCtx, cancelLogin := context.WithTimeout(runCtx, 5*time.Second)
		endpoint, err := network.LoginClient(loginCtx, clientStream, identity)
		cancelLogin()
		if err != nil {
			_ = counting.Close()
			return client.MultiplayerSummary{}, client.PhaseSummary{}, fmt.Errorf("登录 %s: %w", identity.DisplayName, err)
		}
		drainDone := make(chan error, 1)
		go func(endpoint network.ClientEndpoint) {
			for {
				_, err := endpoint.Recv(runCtx)
				if err != nil {
					drainDone <- err
					return
				}
			}
		}(endpoint)
		clients = append(clients, multiplayerServerClient{
			endpoint: endpoint, serverDone: serverDone, drainDone: drainDone,
		})
	}

	loginReadyCtx, cancelLoginReady := context.WithTimeout(runCtx, 5*time.Second)
	loginPoll := time.NewTicker(10 * time.Millisecond)
	for {
		stats := host.Stats()
		if stats.ActivePlayers == len(identities) {
			break
		}
		if stats.ActivePlayers > len(identities) {
			loginPoll.Stop()
			cancelLoginReady()
			return client.MultiplayerSummary{}, client.PhaseSummary{}, fmt.Errorf(
				"多人服务端登录数越界: active=%d want=%d",
				stats.ActivePlayers, len(identities),
			)
		}
		select {
		case <-loginPoll.C:
		case <-loginReadyCtx.Done():
			loginPoll.Stop()
			err := loginReadyCtx.Err()
			cancelLoginReady()
			return client.MultiplayerSummary{}, client.PhaseSummary{}, fmt.Errorf(
				"等待多人服务端登录稳定: active=%d want=%d: %w",
				stats.ActivePlayers, len(identities), err,
			)
		}
	}
	loginPoll.Stop()
	cancelLoginReady()
	epoch.beginWarmup()
	for tick := 0; tick < benchmarkServerWarmupTicks; tick++ {
		select {
		case signal := <-epoch.signals:
			if signal.measured {
				return client.MultiplayerSummary{}, client.PhaseSummary{},
					fmt.Errorf("warm-up tick %d 被标记为 measured", tick+1)
			}
			if stats := host.Stats(); stats.ActivePlayers != len(identities) {
				return client.MultiplayerSummary{}, client.PhaseSummary{}, fmt.Errorf(
					"多人服务端 warm-up tick %d 玩家提前退出: active=%d want=%d",
					tick+1, stats.ActivePlayers, len(identities),
				)
			}
		case <-runCtx.Done():
			return client.MultiplayerSummary{}, client.PhaseSummary{}, runCtx.Err()
		}
	}
	if stats := host.Stats(); stats.ActivePlayers != len(identities) {
		return client.MultiplayerSummary{}, client.PhaseSummary{}, fmt.Errorf(
			"多人服务端 warm-up 后登录不完整: active=%d want=%d",
			stats.ActivePlayers, len(identities),
		)
	}
	sendInputs := func(ctx context.Context, sequence uint64) error {
		return sendMultiplayerBenchmarkInputs(ctx, clients, sequence)
	}
	if err := epoch.beginMeasurement(func() error {
		return sendInputs(runCtx, 1)
	}); err != nil {
		return client.MultiplayerSummary{}, client.PhaseSummary{}, err
	}
	window, err := runBenchmarkServerMeasuredWindow(
		runCtx,
		epoch,
		len(clients),
		sendInputs,
		host.Stats,
		client.ProcessRSSBytes,
	)
	if err != nil {
		return client.MultiplayerSummary{}, client.PhaseSummary{}, err
	}
	outboxHigh, jobsHigh, doneHigh, peakRSS :=
		window.outboxHigh, window.jobsHigh, window.doneHigh, window.peakRSS
	interestSummary := epoch.interest.Summary()
	tickLatency := epoch.ticks.Summary()
	tickSummary := client.PhaseSummary{
		Frames: tickLatency.Samples,
		P50MS:  tickLatency.P50MS,
		P95MS:  tickLatency.P95MS,
		P99MS:  tickLatency.P99MS,
		MaxMS:  tickLatency.MaxMS,
	}
	if epoch.overflow.Load() || outbound.Load() == 0 ||
		interestSummary.Samples != benchmarkServerInterestSamples ||
		tickSummary.Frames != benchmarkServerMeasuredTicks ||
		outboxHigh > benchmarkOutboxLimit || jobsHigh > 16 || doneHigh > 2 ||
		peakRSS == 0 || peakRSS >= 2<<30 {
		return client.MultiplayerSummary{}, client.PhaseSummary{}, fmt.Errorf(
			"多人服务端探针不完整: overflow=%v outbound=%d interest=%+v ticks=%+v queues=%d/%d/%d rss=%d",
			epoch.overflow.Load(), outbound.Load(), interestSummary, tickSummary,
			outboxHigh, jobsHigh, doneHigh, peakRSS,
		)
	}
	if err := cleanup(); err != nil {
		return client.MultiplayerSummary{}, client.PhaseSummary{}, err
	}
	cleaned = true
	if stats := host.Stats(); stats != (server.HostStats{}) {
		return client.MultiplayerSummary{}, client.PhaseSummary{}, fmt.Errorf("多人服务端 cleanup 队列未归零: %+v", stats)
	}
	return client.MultiplayerSummary{
		InterestDiff:        interestSummary,
		ServerOutboundBytes: outbound.Load(),
		OutboxHighWater:     outboxHigh,
		PlayerJobsHighWater: jobsHigh,
		PlayerDoneHighWater: doneHigh,
		PeakRSSBytes:        peakRSS,
	}, tickSummary, nil
}
