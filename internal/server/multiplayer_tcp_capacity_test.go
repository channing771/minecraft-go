package server

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
)

func TestEightTCPClientsSoakIsBounded(t *testing.T) {
	runEightTCPClientsSoakIsBounded(t)
}

func TestConcurrentLoginFailureDrainsAndClosesEverySuccessfulClient(t *testing.T) {
	first := task16CleanupProbeClient("first")
	late := task16CleanupProbeClient("late")
	wantErr := errors.New("injected login failure")
	results := make(chan task16ConcurrentLoginResult, 3)
	results <- task16ConcurrentLoginResult{index: 0, client: first}
	results <- task16ConcurrentLoginResult{index: 1, err: wantErr}
	results <- task16ConcurrentLoginResult{index: 2, client: late}
	workersDone := make(chan struct{})
	close(workersDone)
	ctx, cancel := context.WithCancel(context.Background())
	collectCtx, collectCancel := context.WithTimeout(context.Background(), waitDeadline)
	defer collectCancel()

	clients, err := collectTask16ConcurrentLoginResults(t, ctx, cancel, collectCtx, results, workersDone, 3, 3)
	if !errors.Is(err, wantErr) {
		t.Fatalf("collected login error=%v, want %v", err, wantErr)
	}
	if len(clients) != 3 || clients[0] != first || clients[2] != late {
		t.Fatalf("collected clients=%v, want first/late retained by index", clients)
	}
	if !first.closed || !late.closed {
		t.Fatalf("successful clients were not closed after failure: first=%t late=%t", first.closed, late.closed)
	}
}

func TestCollectorAbortClosesLateSuccessfulClientAndJoinsWorker(t *testing.T) {
	late := task16CleanupProbeClient("late-after-abort")
	t.Cleanup(func() { _ = closeTask16Client(late) })
	results := make(chan task16ConcurrentLoginResult, 1)
	workersDone := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	collectCtx, abort := context.WithCancel(context.Background())
	abort()
	go func() {
		<-ctx.Done()
		publishTask16ConcurrentLoginResult(ctx, results, task16ConcurrentLoginResult{index: 0, client: late})
		close(workersDone)
	}()

	_, err := collectTask16ConcurrentLoginResults(t, ctx, cancel, collectCtx, results, workersDone, 1, 1)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("collector abort error=%v, want context.Canceled", err)
	}
	joined, stop := context.WithTimeout(context.Background(), waitDeadline)
	defer stop()
	select {
	case <-workersDone:
	case <-joined.Done():
		t.Fatalf("late-success worker was not joined: %v", joined.Err())
	}
	if !late.closed {
		t.Fatal("late successful client remained live after collector abort")
	}
}

func TestClaimBeforeCancelKeepsConcurrentLoginClientTransferredAndJoinsWorker(t *testing.T) {
	connected := task16CleanupProbeClient("claimed-before-cancel")
	ctx, cancel := context.WithCancel(context.Background())
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseWorker := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(func() {
		cancel()
		releaseWorker()
		_ = closeTask16Client(connected)
	})

	results := make(chan task16ConcurrentLoginResult, 1)
	waiting := make(chan struct{})
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		publishTask16ConcurrentLoginResult(ctx, results, task16ConcurrentLoginResult{
			index: 0, client: connected,
			awaitOwnership: func(context.Context, <-chan struct{}) bool {
				close(waiting)
				<-release
				return false
			},
		})
	}()

	deadline, stop := context.WithTimeout(context.Background(), waitDeadline)
	defer stop()
	var result task16ConcurrentLoginResult
	select {
	case result = <-results:
	case <-deadline.Done():
		t.Fatalf("receive published login result: %v", deadline.Err())
	}
	select {
	case <-waiting:
	case <-deadline.Done():
		t.Fatalf("worker did not reach ownership wait: %v", deadline.Err())
	}
	claimTask16ConcurrentLoginResult(result)
	cancel()
	releaseWorker()
	select {
	case <-workerDone:
	case <-deadline.Done():
		t.Fatalf("claimed-result worker was not joined: %v", deadline.Err())
	}
	if connected.closed {
		t.Fatal("client closed after ownership transferred before cancellation")
	}
	if err := connected.endpoint.Send(deadline, network.PlayerInput{}); err != nil {
		t.Fatalf("transferred client is unusable: Send=%v", err)
	}
	if err := closeTask16Client(connected); err != nil {
		t.Fatalf("close transferred client: %v", err)
	}
	if err := connected.endpoint.Send(deadline, network.PlayerInput{}); !errors.Is(err, network.ErrClosed) {
		t.Fatalf("client remained live after final cleanup: Send=%v, want network.ErrClosed", err)
	}
}

type multiplayerQueueSample struct {
	outbox, outboxCapacity           int
	playerJobs, playerJobsCapacity   int
	completions, completionsCapacity int
}

type task16ConcurrentLoginRequest struct {
	index    int
	identity network.Identity
}

type task16ConcurrentLoginResult struct {
	index          int
	client         *multiplayerTCPClient
	err            error
	ownership      chan struct{}
	awaitOwnership func(context.Context, <-chan struct{}) bool
}

func runEightTCPClientsSoakIsBounded(t *testing.T) {
	t.Helper()
	baseline := runtime.NumGoroutine()
	clients := make([]*multiplayerTCPClient, multiplayerClientCount)
	listener, err := network.ListenTCP("127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenTCP loopback: %v", err)
	}
	config := hostTestConfig()
	config.MaxPlayers = multiplayerClientCount
	config.ViewRadius = 0
	config.OutboxCapacity = 512
	config.AutosaveTicks = 6000
	host := mustNewHost(t, config, multiplayerManualGenerator{}, newHostTestStore())
	hostDone := make(chan error, 1)
	go func() { hostDone <- host.Run(context.Background(), listener) }()
	var cleanupOnce sync.Once
	var cleanupErr error
	cleanup := func() error {
		cleanupOnce.Do(func() {
			cleanupErr = cleanupTask16TCPHost(host, listener, hostDone, clients)
		})
		return cleanupErr
	}
	t.Cleanup(func() {
		if err := cleanup(); err != nil {
			t.Errorf("cleanup eight-player TCP soak: %v", err)
		}
	})

	requests := make([]task16ConcurrentLoginRequest, multiplayerClientCount)
	for index := 0; index < multiplayerClientCount; index++ {
		requests[index] = task16ConcurrentLoginRequest{
			index: index, identity: multiplayerIdentity(byte(0x80+index), multiplayerNames[index]),
		}
	}
	clients, err = connectTask16ConcurrentClients(t, listener.Addr(), requests, longWaitDeadline)
	if err != nil {
		t.Fatalf("concurrent login: %v", err)
	}

	readyCtx, readyCancel := context.WithTimeout(context.Background(), longWaitDeadline)
	for !eightTCPClientsReady(clients) {
		drainAllMultiplayerAvailable(t, clients)
		if err := readyCtx.Err(); err != nil {
			readyCancel()
			t.Fatalf("eight TCP clients ready/roster: %v\n%s", err, multiplayerDiagnosticsMany(clients))
		}
		runtime.Gosched()
	}
	readyCancel()
	stableGoroutines := runtime.NumGoroutine()

	highWater := multiplayerQueueSample{}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	soakTimer := time.NewTimer(10 * time.Second)
	defer soakTimer.Stop()
	tick := uint64(0)
	soaking := true
	for soaking {
		select {
		case <-soakTimer.C:
			soaking = false
		case <-ticker.C:
			tick++
			for _, step := range fixedEightPlayerScriptForTick(tick) {
				var message network.ClientMessage
				switch {
				case step.Input != nil:
					message = *step.Input
				case step.Place != nil:
					message = *step.Place
				}
				ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
				err := clients[step.Player].endpoint.Send(ctx, message)
				cancel()
				if err != nil {
					t.Fatalf("soak tick %d player %d Send(%T): %v\n%s", tick, step.Player, message, err, multiplayerDiagnosticsMany(clients))
				}
			}
			drainAllMultiplayerAvailable(t, clients)
			for index, connected := range clients {
				if roster := len(connected.remotes.Presentations()); roster > multiplayerClientCount-1 {
					t.Fatalf("soak tick %d player %d roster=%d, want <=7", tick, index, roster)
				}
			}
			sample := sampleMultiplayerQueues(host)
			if sample.outbox > highWater.outbox {
				highWater.outbox = sample.outbox
			}
			highWater.outboxCapacity = sample.outboxCapacity
			if sample.playerJobs > highWater.playerJobs {
				highWater.playerJobs = sample.playerJobs
			}
			highWater.playerJobsCapacity = sample.playerJobsCapacity
			if sample.completions > highWater.completions {
				highWater.completions = sample.completions
			}
			highWater.completionsCapacity = sample.completionsCapacity
		}
	}
	if tick < 190 {
		t.Fatalf("10 second soak executed only %d 50ms ticks", tick)
	}
	if highWater.outbox > highWater.outboxCapacity || highWater.playerJobs > highWater.playerJobsCapacity ||
		highWater.completions > highWater.completionsCapacity {
		t.Fatalf("queue high-water exceeded capacity: %+v", highWater)
	}

	fallCtx, fallCancel := context.WithTimeout(context.Background(), waitDeadline)
	for {
		drainAllMultiplayerAvailable(t, clients)
		sample := sampleMultiplayerQueues(host)
		if sample.outbox == 0 && sample.playerJobs == 0 && sample.completions == 0 {
			break
		}
		if err := fallCtx.Err(); err != nil {
			fallCancel()
			t.Fatalf("queues did not return to zero: %v sample=%+v\n%s", err, sample, multiplayerDiagnosticsMany(clients))
		}
		runtime.Gosched()
	}
	fallCancel()
	drainAllMultiplayerAvailable(t, clients)
	for index, connected := range clients {
		assertTCPSoakBusiness(t, index, connected)
	}
	assertEightMirrorConvergence(t, clients, multiplayerManualTarget.Chunk())
	if got := runtime.NumGoroutine(); got > stableGoroutines+4 {
		t.Fatalf("goroutines after soak=%d, stable=%d (+%d)", got, stableGoroutines, got-stableGoroutines)
	}

	if err := cleanup(); err != nil {
		t.Fatalf("cleanup eight-player TCP soak: %v", err)
	}
	goroutineCtx, goroutineCancel := context.WithTimeout(context.Background(), waitDeadline)
	for runtime.NumGoroutine() > baseline+4 && goroutineCtx.Err() == nil {
		runtime.Gosched()
	}
	remaining := runtime.NumGoroutine()
	goroutineCancel()
	if remaining > baseline+4 {
		t.Fatalf("goroutines after cleanup=%d, baseline=%d (+%d)", remaining, baseline, remaining-baseline)
	}
}

func closeTask16Client(connected *multiplayerTCPClient) error {
	if connected == nil {
		return nil
	}
	connected.task16CloseOnce.Do(func() {
		if connected.closed {
			return
		}
		connected.closed = true
		if err := connected.receiver.Close(); err != nil && !errors.Is(err, network.ErrClosed) {
			connected.task16CloseErr = err
		}
	})
	return connected.task16CloseErr
}

func task16CleanupProbeClient(name string) *multiplayerTCPClient {
	clientEndpoint, _ := network.NewMemoryPair(1)
	return &multiplayerTCPClient{
		identity: network.Identity{DisplayName: name}, endpoint: clientEndpoint,
		receiver: client.NewReceiver(clientEndpoint, 1), mirror: client.NewMirror(), drops: client.NewItemDrops(),
		remotes: client.NewRemotePlayers(),
	}
}

func connectTask16ConcurrentClients(
	t *testing.T,
	address string,
	requests []task16ConcurrentLoginRequest,
	timeout time.Duration,
) ([]*multiplayerTCPClient, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	results := make(chan task16ConcurrentLoginResult, len(requests))
	var workers sync.WaitGroup
	workers.Add(len(requests))
	for _, request := range requests {
		go func(request task16ConcurrentLoginRequest) {
			defer workers.Done()
			connected, err := connectMultiplayerTCPClient(ctx, address, request.identity)
			publishTask16ConcurrentLoginResult(ctx, results, task16ConcurrentLoginResult{
				index: request.index, client: connected, err: err,
			})
		}(request)
	}
	workersDone := make(chan struct{})
	go func() {
		workers.Wait()
		close(workersDone)
	}()
	collectCtx, collectCancel := context.WithTimeout(context.Background(), longWaitDeadline)
	defer collectCancel()
	return collectTask16ConcurrentLoginResults(t, ctx, cancel, collectCtx, results, workersDone, len(requests), len(requests))
}

func publishTask16ConcurrentLoginResult(
	ctx context.Context,
	results chan<- task16ConcurrentLoginResult,
	result task16ConcurrentLoginResult,
) {
	result.ownership = make(chan struct{}, 1)
	results <- result
	claimed := false
	if result.awaitOwnership != nil {
		claimed = result.awaitOwnership(ctx, result.ownership)
	} else {
		select {
		case <-result.ownership:
			claimed = true
		case <-ctx.Done():
		}
	}
	if !claimed {
		select {
		case <-result.ownership:
			return
		default:
		}
		_ = closeTask16Client(result.client)
	}
}

func claimTask16ConcurrentLoginResult(result task16ConcurrentLoginResult) {
	if result.ownership != nil {
		result.ownership <- struct{}{}
	}
}

func collectTask16ConcurrentLoginResults(
	t *testing.T,
	ctx context.Context,
	cancel context.CancelFunc,
	collectCtx context.Context,
	results <-chan task16ConcurrentLoginResult,
	workersDone <-chan struct{},
	wantResults int,
	clientSlots int,
) ([]*multiplayerTCPClient, error) {
	t.Helper()
	clients := make([]*multiplayerTCPClient, clientSlots)
	var resultErr error
	failed := false
	closeTracked := func() {
		for _, connected := range clients {
			if err := closeTask16Client(connected); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("close successful login %s: %w", connected.identity.DisplayName, err))
			}
		}
	}
	fail := func(err error) {
		resultErr = errors.Join(resultErr, err)
		if failed {
			return
		}
		failed = true
		cancel()
		closeTracked()
	}

	received := 0
	collecting := true
	for received < wantResults && collecting {
		select {
		case result := <-results:
			received++
			claimTask16ConcurrentLoginResult(result)
			if result.err != nil {
				fail(fmt.Errorf("player %d login: %w", result.index, result.err))
				continue
			}
			if result.client == nil {
				fail(fmt.Errorf("player %d login returned nil client", result.index))
				continue
			}
			if result.index < 0 || result.index >= len(clients) || clients[result.index] != nil {
				_ = closeTask16Client(result.client)
				fail(fmt.Errorf("player login returned invalid/duplicate index %d", result.index))
				continue
			}
			clients[result.index] = result.client
			connected := result.client
			t.Cleanup(func() {
				if err := closeTask16Client(connected); err != nil {
					t.Errorf("cleanup concurrent client %s: %v", connected.identity.DisplayName, err)
				}
			})
			if failed || ctx.Err() != nil {
				if !failed {
					fail(fmt.Errorf("concurrent login context: %w", ctx.Err()))
				}
				if err := closeTask16Client(connected); err != nil {
					resultErr = errors.Join(resultErr, fmt.Errorf("close late successful login %s: %w", connected.identity.DisplayName, err))
				}
			}
		case <-collectCtx.Done():
			fail(fmt.Errorf("collect %d/%d login results: %w", received, wantResults, collectCtx.Err()))
			collecting = false
		}
	}
	workerJoinCtx, stopWorkerJoin := context.WithTimeout(context.Background(), waitDeadline)
	defer stopWorkerJoin()
	select {
	case <-workersDone:
	case <-workerJoinCtx.Done():
		fail(fmt.Errorf("join concurrent login workers: %w", workerJoinCtx.Err()))
	}
	return clients, resultErr
}

func cleanupTask16TCPHost(
	host *Host,
	listener network.Listener,
	done <-chan error,
	clients []*multiplayerTCPClient,
) error {
	var cleanupErr error
	for _, connected := range clients {
		if err := closeTask16Client(connected); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("close client %s: %w", connected.identity.DisplayName, err))
		}
	}
	if listener != nil {
		if err := listener.Close(); err != nil && !errors.Is(err, network.ErrClosed) {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("close listener: %w", err))
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), longWaitDeadline)
	defer cancel()
	if err := host.Shutdown(ctx); err != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("Host.Shutdown: %w", err))
	}
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, network.ErrClosed) {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("Host.Run: %w", err))
		}
	case <-ctx.Done():
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("Host.Run result: %w", ctx.Err()))
	}
	return cleanupErr
}

func fixedEightPlayerScriptForTick(tick uint64) []multiplayerScriptStep {
	all := fixedEightPlayerScript(tick)
	start := len(all)
	for start > 0 && all[start-1].Tick == tick {
		start--
	}
	return all[start:]
}

func drainAllMultiplayerAvailable(t *testing.T, clients []*multiplayerTCPClient) {
	t.Helper()
	for index, connected := range clients {
		for {
			progressed, err := drainOneTask16(connected)
			if err != nil {
				t.Fatalf("drain player %d: %v\n%s", index, err, multiplayerDiagnosticsMany(clients))
			}
			if !progressed {
				break
			}
		}
	}
}

func eightTCPClientsReady(clients []*multiplayerTCPClient) bool {
	for _, connected := range clients {
		if connected == nil || !connected.readyWithFootSnapshot() || len(connected.remotes.Presentations()) != 7 {
			return false
		}
	}
	return true
}

func sampleMultiplayerQueues(host *Host) multiplayerQueueSample {
	host.world.stepMu.Lock()
	sample := multiplayerQueueSample{}
	for _, current := range host.world.sessions {
		current.mu.Lock()
		if depth := len(current.outbox); depth > sample.outbox {
			sample.outbox = depth
		}
		if capacity := cap(current.outbox); capacity > sample.outboxCapacity {
			sample.outboxCapacity = capacity
		}
		current.mu.Unlock()
	}
	host.world.stepMu.Unlock()
	sample.playerJobs, sample.playerJobsCapacity = len(host.players.jobs), cap(host.players.jobs)
	sample.completions, sample.completionsCapacity = len(host.players.completions), cap(host.players.completions)
	return sample
}

func assertTCPSoakBusiness(t *testing.T, index int, connected *multiplayerTCPClient) {
	t.Helper()
	var ticks []uint64
	for _, event := range connected.transcript {
		states, ok := event.message.(network.RemotePlayerStates)
		if !ok {
			continue
		}
		if len(states.Players) < 1 || len(states.Players) > 7 {
			t.Fatalf("player %d RemotePlayerStates batch=%d, want 1..7", index, len(states.Players))
		}
		if len(ticks) != 0 && states.ServerTick <= ticks[len(ticks)-1] {
			t.Fatalf("player %d remote tick=%d after %d", index, states.ServerTick, ticks[len(ticks)-1])
		}
		ticks = append(ticks, states.ServerTick)
	}
	if len(ticks) < 150 {
		t.Fatalf("player %d received %d increasing remote ticks, want >=150", index, len(ticks))
	}
	if err := connected.receiver.Err(); err != nil {
		t.Fatalf("player %d receiver protocol error: %v", index, err)
	}
}

func assertEightMirrorConvergence(t *testing.T, clients []*multiplayerTCPClient, chunk core.ChunkPos) {
	t.Helper()
	wantHash, wantRevision, ok := clients[0].mirror.Hash(core.Overworld, chunk)
	if !ok {
		t.Fatalf("player 0 missing mirror chunk %+v", chunk)
	}
	for index := 1; index < len(clients); index++ {
		hash, revision, ok := clients[index].mirror.Hash(core.Overworld, chunk)
		if !ok || hash != wantHash || revision != wantRevision {
			t.Fatalf("player %d mirror=%x/%d/loaded=%t, want %x/%d/true", index, hash, revision, ok, wantHash, wantRevision)
		}
	}
}
