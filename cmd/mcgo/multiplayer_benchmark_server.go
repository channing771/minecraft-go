//go:build darwin

package main

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"minecraft-go/internal/client"
	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/server"
	"minecraft-go/internal/storage"
	"minecraft-go/internal/worldgen"
)

type benchmarkServerWindowSummary struct {
	outboxHigh int
	jobsHigh   int
	doneHigh   int
	peakRSS    uint64
}

// formatTickBoundaryOverrun 把一次 input boundary 超时拆成可判读的时间分解。
//
// 抽成纯函数是因为这段代码在本地永远不会执行：CI 上的失败形态本地复现不出来
// （见 docs/superpowers/specs/2026-08-07-ci-stability-merge-gate-design.md §7），
// 若把格式化埋在探针运行路径里，它就没有任何验证手段——一个从不执行的诊断
// 分支等于没写。
//
// 队列深度是这几项里判别力最强的一项：大于 0 说明缓冲里还压着别的信号、
// 测试 goroutine 确实落后了；等于 0 则说明时间花在服务端侧。取的是取出信号
// 之后的 len(epoch.signals)，与"取出那一刻"差一个，但方向与判别力不变。
//
// 这套判别力只在信号刚取出、测试侧还没做任何工作时成立——即消息里不带站点
// 标记的那一档（`measured tick %d: ...`）。带"（采样后、boundary 前）"标记的
// 调用点前面刚做完一次非阻塞 drain，深度结构性地几乎恒为 0；带"（boundary
// 完成后）"标记的调用点里，下一 tick 的合法发布会让深度变成 1。这两处不能
// 直接套用 docs/superpowers/specs/2026-08-07-benchmark-tick-boundary-diagnostics-design.md
// §6 的判定表。
func formatTickBoundaryOverrun(
	signal benchmarkServerTickSignal,
	now time.Time,
	queueDepth int,
) string {
	total := now.Sub(signal.scheduled)
	overrun := total - fixedBenchmarkFrameDuration
	if signal.published.IsZero() {
		return fmt.Sprintf(
			"server input boundary 已错过 50ms tick deadline："+
				"总耗时 %v（超出 %v）；tick 自身 %v；发布时刻缺失；收到时队列深度 %d",
			total, overrun, signal.duration, queueDepth,
		)
	}
	return fmt.Sprintf(
		"server input boundary 已错过 50ms tick deadline："+
			"总耗时 %v（超出 %v）；tick 自身 %v；调度→发布 %v；发布→收到 %v；"+
			"收到时队列深度 %d",
		total, overrun, signal.duration,
		signal.published.Sub(signal.scheduled),
		now.Sub(signal.published),
		queueDepth,
	)
}

func benchmarkServerInputDeadline(
	signal benchmarkServerTickSignal,
	queueDepth int,
) (time.Time, error) {
	if signal.scheduled.IsZero() {
		return time.Time{}, errors.New("server tick 缺少调度时间")
	}
	deadline := signal.scheduled.Add(fixedBenchmarkFrameDuration)
	now := time.Now()
	if !now.Before(deadline) {
		return time.Time{}, errors.New(
			formatTickBoundaryOverrun(signal, now, queueDepth),
		)
	}
	return deadline, nil
}

func runBenchmarkServerMeasuredWindow(
	ctx context.Context,
	epoch *benchmarkServerEpoch,
	wantPlayers int,
	inputBoundary benchmarkServerInputBoundary,
	sendInputs func(context.Context, uint64) error,
	readStats func() server.HostStats,
	readRSS func() (uint64, error),
) (benchmarkServerWindowSummary, error) {
	var result benchmarkServerWindowSummary
	completedWindow := false
	defer func() {
		if !completedWindow {
			epoch.abortMeasurement()
		}
	}()
	for completed := 1; completed <= benchmarkServerMeasuredTicks; completed++ {
		var signal benchmarkServerTickSignal
		select {
		case signal = <-epoch.signals:
		case <-ctx.Done():
			return result, ctx.Err()
		}
		if !signal.measured {
			return result, fmt.Errorf(
				"measured tick %d 收到 warm-up signal", completed,
			)
		}
		var inputDeadline time.Time
		if completed < benchmarkServerMeasuredTicks {
			var err error
			inputDeadline, err = benchmarkServerInputDeadline(signal, len(epoch.signals))
			if err != nil {
				return result, fmt.Errorf("measured tick %d: %w", completed, err)
			}
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
			if inputBoundary == nil {
				return result, errors.New("缺少服务端 input boundary")
			}
			select {
			case next := <-epoch.signals:
				return result, fmt.Errorf(
					"Host.Stats/采样未在 measured tick %d 边界完成，下一 tick 已推进: measured=%v",
					completed, next.measured,
				)
			default:
			}
			if now := time.Now(); !now.Before(inputDeadline) {
				return result, fmt.Errorf(
					"measured tick %d（采样后、boundary 前）: %s", completed,
					formatTickBoundaryOverrun(signal, now, len(epoch.signals)),
				)
			}
			nextSequence := uint64(completed + 1)
			boundaryCtx, cancelBoundary := context.WithDeadline(ctx, inputDeadline)
			err := inputBoundary(boundaryCtx, nextSequence, func() error {
				select {
				case next := <-epoch.signals:
					return fmt.Errorf(
						"Host.Stats/采样未在 measured tick %d 边界完成，下一 tick 已推进: measured=%v",
						completed, next.measured,
					)
				default:
				}
				return sendInputs(boundaryCtx, nextSequence)
			})
			cancelBoundary()
			if err != nil {
				return result, fmt.Errorf(
					"measured tick %d 的 input boundary 未在 50ms deadline 前完成: %w",
					completed, err,
				)
			}
			if now := time.Now(); !now.Before(inputDeadline) {
				return result, fmt.Errorf(
					"measured tick %d（boundary 完成后）: %s", completed,
					formatTickBoundaryOverrun(signal, now, len(epoch.signals)),
				)
			}
		}
	}
	completedWindow = true
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
	config.ScheduledTickObserver = epoch.observeScheduledTick
	config.InterestObserver = epoch.observeInterest
	store := storage.NewMemory(storage.Metadata{
		FormatVersion: 2, Seed: benchmarkSeed,
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
	var lastWarmupSignal benchmarkServerTickSignal
	for tick := 0; tick < benchmarkServerWarmupTicks; tick++ {
		select {
		case signal := <-epoch.signals:
			lastWarmupSignal = signal
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
	inputBoundary := func(
		boundaryCtx context.Context,
		sequence uint64,
		action func() error,
	) error {
		return host.RunAtInputBoundary(boundaryCtx, sequence, len(clients), action)
	}
	firstInputDeadline, err := benchmarkServerInputDeadline(lastWarmupSignal, len(epoch.signals))
	if err != nil {
		return client.MultiplayerSummary{}, client.PhaseSummary{}, fmt.Errorf(
			"warm-up 后首组 input boundary: %w", err,
		)
	}
	firstInputCtx, cancelFirstInput := context.WithDeadline(runCtx, firstInputDeadline)
	err = epoch.beginMeasurement(firstInputCtx, inputBoundary, func() error {
		return sendInputs(firstInputCtx, 1)
	})
	cancelFirstInput()
	if err != nil {
		return client.MultiplayerSummary{}, client.PhaseSummary{}, err
	}
	if !time.Now().Before(firstInputDeadline) {
		return client.MultiplayerSummary{}, client.PhaseSummary{}, errors.New(
			"warm-up 后首组 input boundary 超过 50ms deadline",
		)
	}
	window, err := runBenchmarkServerMeasuredWindow(
		runCtx,
		epoch,
		len(clients),
		inputBoundary,
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
	invalid := !validBenchmarkServerProbe(
		epoch.overflow.Load(),
		outbound.Load(),
		interestSummary.Samples,
		tickSummary.Frames,
		peakRSS,
	)
	if invalid {
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

func validBenchmarkServerProbe(overflow bool, outbound uint64, interestSamples, tickFrames int, peakRSS uint64) bool {
	return !overflow && outbound != 0 &&
		interestSamples == benchmarkServerInterestSamples && tickFrames == benchmarkServerMeasuredTicks && peakRSS != 0
}
