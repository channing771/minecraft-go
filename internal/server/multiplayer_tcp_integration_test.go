package server

import (
	"context"
	"errors"
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/client"
	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
)

type multiplayerTCPHost struct {
	Host *Host
	Addr string
	Done <-chan error
}

type multiplayerEvent struct {
	message network.ServerMessage
}

type multiplayerTCPClient struct {
	identity        network.Identity
	endpoint        network.ClientEndpoint
	receiver        *client.Receiver
	mirror          *client.Mirror
	remotes         *client.RemotePlayers
	local           network.PlayerState
	transcript      []multiplayerEvent
	closed          bool
	task16CloseOnce sync.Once
	task16CloseErr  error
}

func TestMultiplayerTCPClientsSeeMoveEditAndDespawn(t *testing.T) {
	deadline, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	host := startMultiplayerTCPHost(t)
	var a, b *multiplayerTCPClient
	cleanupMultiplayerTCPTest(t, host, &a, &b)
	aIdentity := multiplayerIdentity(0xa1, "阿明")
	bIdentity := multiplayerIdentity(0xb2, "Builder")
	a = mustConnectMultiplayerTCPClient(t, deadline, host.Addr, aIdentity)
	b = mustConnectMultiplayerTCPClient(t, deadline, host.Addr, bIdentity)

	// Kills a driver that declares ready before applying the initial snapshot,
	// or a server that marks a login ready without sending the foot chunk.
	mustDrainMultiplayer(t, deadline, a, b, "both clients ready with foot snapshots", func() bool {
		return a.readyWithFootSnapshot() && b.readyWithFootSnapshot()
	})

	// Kills Spawn-before-snapshot ordering, duplicate Spawn, swapped identities,
	// and a driver that invents roster entries instead of consuming TCP messages.
	mustDrainMultiplayer(t, deadline, a, b, "each client sees the other after its snapshot", func() bool {
		return a.sawSingleSpawnAfterFootSnapshot(bIdentity) &&
			b.sawSingleSpawnAfterFootSnapshot(aIdentity)
	})
	// Kills a fixture that silently changes the fixed interaction block or starts
	// either mirror at a non-authoritative revision. Revision 1 is the snapshot.
	assertMirrorBlockAndRevision(t, a, playerIntegrationObstacle, core.StoneID, 1)
	assertMirrorBlockAndRevision(t, b, playerIntegrationObstacle, core.StoneID, 1)

	aStart := a.local.Position
	for sequence := uint64(1); sequence <= 8; sequence++ {
		mustSendMultiplayer(t, deadline, a, network.PlayerInput{
			Sequence: sequence,
			MoveZ:    1,
			Yaw:      0,
			Pitch:    -0.2,
		})
	}
	// Kills coalescing that reports stale/non-increasing ticks, movement that is
	// never replicated, and a driver that mistakes local PlayerState for remote state.
	mustDrainMultiplayer(t, deadline, a, b, "B sees A move on increasing ticks", func() bool {
		return b.sawRemoteMoveOnIncreasingTicks(aIdentity.PlayerID, aStart)
	})

	target := core.BlockPos{X: 0, Y: 1, Z: -6}
	mustSendMultiplayer(t, deadline, a, network.PlayerInput{
		Sequence: 9,
		Yaw:      0,
		Pitch:    -0.2,
		Mining:   true,
	})
	// Kills one-sided block fanout, incorrect block IDs, revision-only equality,
	// hash-only equality, and a driver that compares a mirror with itself.
	mustDrainMultiplayer(t, deadline, a, b, "both mirrors converge after break", func() bool {
		return mirrorsConvergedAt(a, b, target, core.AirID, 2)
	})

	mustCloseMultiplayerTCPClient(t, b)
	// Kills a missing or duplicate Despawn. The later post-disconnect local state
	// provides a stream marker after which the terminal ordering is checked again.
	mustDrainMultiplayer(t, deadline, a, b, "A sees exactly one terminal B despawn", func() bool {
		return a.sawExactlyOneTerminalDespawn(bIdentity.PlayerID)
	})

	beforeContinue := a.local
	mustSendMultiplayer(t, deadline, a, network.PlayerInput{
		Sequence: 10,
		MoveX:    1,
		Yaw:      0,
		Pitch:    -0.2,
	})
	// Kills a disconnect path that stops the shared world/session, and a driver
	// that accepts an old local state instead of a post-disconnect acknowledgement.
	mustDrainMultiplayer(t, deadline, a, b, "A world continues after B disconnects", func() bool {
		return a.local.LastInputSequence >= 10 &&
			a.local.ServerTick > beforeContinue.ServerTick &&
			a.local.Position != beforeContinue.Position
	})
	// Kills a server that resumes broadcasting B after Despawn, or emits a second
	// Despawn while A's session continues to consume later world ticks.
	if !a.sawExactlyOneTerminalDespawn(bIdentity.PlayerID) {
		t.Fatalf("B did not have one terminal Despawn after A continued\n%s", multiplayerDiagnostics(a, b))
	}
}

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
	collectCtx, collectCancel := context.WithTimeout(context.Background(), time.Second)
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
	joined, stop := context.WithTimeout(context.Background(), time.Second)
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

	deadline, stop := context.WithTimeout(context.Background(), time.Second)
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
	config.ShutdownTimeout = 5 * time.Second
	host := NewHost(config, multiplayerManualGenerator{}, newHostTestStore())
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
	clients, err = connectTask16ConcurrentClients(t, listener.Addr(), requests, 10*time.Second)
	if err != nil {
		t.Fatalf("concurrent login: %v", err)
	}

	readyCtx, readyCancel := context.WithTimeout(context.Background(), 10*time.Second)
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
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
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

	fallCtx, fallCancel := context.WithTimeout(context.Background(), 5*time.Second)
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
	goroutineCtx, goroutineCancel := context.WithTimeout(context.Background(), 5*time.Second)
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
		receiver: client.NewReceiver(clientEndpoint, 1), mirror: client.NewMirror(), remotes: client.NewRemotePlayers(),
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
	collectCtx, collectCancel := context.WithTimeout(context.Background(), 15*time.Second)
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
	workerJoinCtx, stopWorkerJoin := context.WithTimeout(context.Background(), 5*time.Second)
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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

func drainOneTask16(connected *multiplayerTCPClient) (bool, error) {
	message, ok := connected.receiver.TryRecv()
	if !ok {
		if err := connected.receiver.Err(); err != nil {
			return false, err
		}
		return false, nil
	}
	connected.transcript = append(connected.transcript, multiplayerEvent{message: message})
	switch message := message.(type) {
	case network.PlayerState:
		if err := message.Validate(); err != nil {
			return true, fmt.Errorf("PlayerState.Validate: %w", err)
		}
		connected.local = message
	case network.ChunkSnapshot, network.BlockChanges, network.ForgetChunks, network.CommandRejected:
		update, err := connected.mirror.Apply(message)
		if err != nil {
			return true, fmt.Errorf("Mirror.Apply(%T): %w", message, err)
		}
		if update.Resync != nil {
			return true, fmt.Errorf("Mirror.Apply(%T) requested resync %+v", message, *update.Resync)
		}
	case network.RemotePlayerSpawn, network.RemotePlayerStates, network.RemotePlayerDespawn:
		if err := connected.remotes.Apply(message); err != nil {
			return true, fmt.Errorf("RemotePlayers.Apply(%T): %w", message, err)
		}
	case network.KeepAlive:
		connected.transcript = connected.transcript[:len(connected.transcript)-1]
	}
	return true, nil
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

func startMultiplayerTCPHost(t *testing.T) multiplayerTCPHost {
	t.Helper()
	listener, err := network.ListenTCP("127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenTCP loopback: %v", err)
	}
	config := hostTestConfig()
	config.MaxPlayers = 2
	config.ViewRadius = 1
	config.OutboxCapacity = 512
	config.ShutdownTimeout = 5 * time.Second
	host := NewHost(config, flatTestGenerator{}, newHostTestStore())
	done := make(chan error, 1)
	go func() { done <- host.Run(context.Background(), listener) }()
	return multiplayerTCPHost{Host: host, Addr: listener.Addr(), Done: done}
}

func multiplayerIdentity(last byte, displayName string) network.Identity {
	return network.Identity{
		PlayerID: core.PlayerID{
			0x31, 0x52, 0x73, 0x94, 0xb5, 0xd6, 0x47, 0xf8,
			0x89, 0xaa, 0xcb, 0xec, 0x0d, 0x2e, 0x4f, last,
		},
		DisplayName: displayName,
	}
}

func connectMultiplayerTCPClient(
	ctx context.Context,
	address string,
	identity network.Identity,
) (*multiplayerTCPClient, error) {
	stream, err := network.DialTCP(ctx, address)
	if err != nil {
		return nil, fmt.Errorf("DialTCP %s: %w", identity.DisplayName, err)
	}
	endpoint, err := network.LoginClient(ctx, stream, identity)
	if err != nil {
		_ = stream.Close()
		return nil, fmt.Errorf("LoginClient %s: %w", identity.DisplayName, err)
	}
	return &multiplayerTCPClient{
		identity: identity,
		endpoint: endpoint,
		receiver: client.NewReceiver(endpoint, 1024),
		mirror:   client.NewMirror(),
		remotes:  client.NewRemotePlayers(),
	}, nil
}

func mustConnectMultiplayerTCPClient(
	t *testing.T,
	ctx context.Context,
	address string,
	identity network.Identity,
) *multiplayerTCPClient {
	t.Helper()
	connected, err := connectMultiplayerTCPClient(ctx, address, identity)
	if err != nil {
		t.Fatal(err)
	}
	return connected
}

func (connected *multiplayerTCPClient) drainUntil(ctx context.Context, done func() bool) error {
	for !done() {
		progressed, err := connected.drainOne()
		if err != nil {
			return err
		}
		if progressed {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			runtime.Gosched()
		}
	}
	return nil
}

func (connected *multiplayerTCPClient) drainOne() (bool, error) {
	message, ok := connected.receiver.TryRecv()
	if !ok {
		if err := connected.receiver.Err(); err != nil {
			return false, err
		}
		return false, nil
	}
	connected.transcript = append(connected.transcript, multiplayerEvent{message: message})
	switch message := message.(type) {
	case network.PlayerState:
		if err := message.Validate(); err != nil {
			return true, fmt.Errorf("PlayerState.Validate: %w", err)
		}
		connected.local = message
	case network.ChunkSnapshot, network.BlockChanges, network.ForgetChunks, network.CommandRejected:
		update, err := connected.mirror.Apply(message)
		if err != nil {
			return true, fmt.Errorf("Mirror.Apply(%T): %w", message, err)
		}
		if update.Resync != nil {
			return true, fmt.Errorf("Mirror.Apply(%T) requested resync %+v", message, *update.Resync)
		}
		if update.Rejected != nil {
			return true, fmt.Errorf("command rejected: %+v", *update.Rejected)
		}
	case network.RemotePlayerSpawn, network.RemotePlayerStates, network.RemotePlayerDespawn:
		if err := connected.remotes.Apply(message); err != nil {
			return true, fmt.Errorf("RemotePlayers.Apply(%T): %w", message, err)
		}
	case network.KeepAlive:
		connected.transcript = connected.transcript[:len(connected.transcript)-1]
	}
	return true, nil
}

func mustDrainMultiplayer(
	t *testing.T,
	ctx context.Context,
	first, second *multiplayerTCPClient,
	label string,
	done func() bool,
) {
	t.Helper()
	if second == nil {
		if err := first.drainUntil(ctx, done); err != nil {
			t.Fatalf("%s: %v\n%s", label, err, multiplayerDiagnostics(first, second))
		}
		return
	}
	for !done() {
		progressed := false
		for _, connected := range []*multiplayerTCPClient{first, second} {
			got, err := connected.drainOne()
			if err != nil {
				t.Fatalf("%s for %s: %v\n%s", label, connected.identity.DisplayName, err, multiplayerDiagnostics(first, second))
			}
			progressed = progressed || got
			if done() {
				return
			}
		}
		if progressed {
			continue
		}
		select {
		case <-ctx.Done():
			t.Fatalf("%s: %v\n%s", label, ctx.Err(), multiplayerDiagnostics(first, second))
		default:
			runtime.Gosched()
		}
	}
}

func mustSendMultiplayer(
	t *testing.T,
	ctx context.Context,
	connected *multiplayerTCPClient,
	message network.ClientMessage,
) {
	t.Helper()
	if err := connected.endpoint.Send(ctx, message); err != nil {
		t.Fatalf("%s Send(%T): %v\n%s", connected.identity.DisplayName, message, err, multiplayerDiagnostics(connected, nil))
	}
}

func mustCloseMultiplayerTCPClient(t *testing.T, connected *multiplayerTCPClient) {
	t.Helper()
	if connected == nil || connected.closed {
		return
	}
	connected.closed = true
	if err := connected.receiver.Close(); err != nil && !errors.Is(err, network.ErrClosed) {
		t.Fatalf("close client %s: %v", connected.identity.DisplayName, err)
	}
}

func cleanupMultiplayerTCPTest(
	t *testing.T,
	host multiplayerTCPHost,
	first, second **multiplayerTCPClient,
) {
	t.Helper()
	t.Cleanup(func() {
		for _, connected := range []*multiplayerTCPClient{*first, *second} {
			if connected == nil || connected.closed {
				continue
			}
			connected.closed = true
			if err := connected.receiver.Close(); err != nil && !errors.Is(err, network.ErrClosed) {
				t.Errorf("cleanup client %s: %v", connected.identity.DisplayName, err)
			}
		}

		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := host.Host.Shutdown(shutdown); err != nil {
			t.Errorf("cleanup Host.Shutdown: %v", err)
		}
		select {
		case err := <-host.Done:
			if err != nil {
				t.Errorf("unconsumed Host.Run error: %v", err)
			}
		case <-shutdown.Done():
			t.Errorf("Host.Run cleanup: %v", shutdown.Err())
		}
	})
}

func (connected *multiplayerTCPClient) readyWithFootSnapshot() bool {
	if !connected.local.Ready {
		return false
	}
	_, ok := connected.mirror.Chunk(connected.local.Dimension, vec3BlockPos(connected.local.Position).Chunk())
	return ok
}

func (connected *multiplayerTCPClient) sawSingleSpawnAfterFootSnapshot(want network.Identity) bool {
	spawnIndex := -1
	snapshotIndex := -1
	spawnCount := 0
	for index, event := range connected.transcript {
		switch message := event.message.(type) {
		case network.RemotePlayerSpawn:
			if message.PlayerID != want.PlayerID {
				continue
			}
			spawnCount++
			if message.DisplayName != want.DisplayName {
				return false
			}
			spawnIndex = index
			footChunk := vec3BlockPos(message.Position).Chunk()
			for candidate, prior := range connected.transcript[:index] {
				if snapshot, ok := prior.message.(network.ChunkSnapshot); ok &&
					snapshot.Dimension == message.Dimension && snapshot.Chunk == footChunk {
					snapshotIndex = candidate
				}
			}
		}
	}
	presentations := connected.remotes.Presentations()
	return spawnCount == 1 && snapshotIndex >= 0 && spawnIndex > snapshotIndex &&
		len(presentations) == 1 && presentations[0].PlayerID == want.PlayerID &&
		presentations[0].DisplayName == want.DisplayName
}

func (connected *multiplayerTCPClient) sawRemoteMoveOnIncreasingTicks(
	playerID core.PlayerID,
	start mgl32.Vec3,
) bool {
	var ticks []uint64
	position := start
	for _, event := range connected.transcript {
		states, ok := event.message.(network.RemotePlayerStates)
		if !ok {
			continue
		}
		for _, state := range states.Players {
			if state.PlayerID != playerID {
				continue
			}
			if len(ticks) > 0 && states.ServerTick <= ticks[len(ticks)-1] {
				return false
			}
			ticks = append(ticks, states.ServerTick)
			position = state.Position
		}
	}
	return len(ticks) >= 2 && position != start
}

func (connected *multiplayerTCPClient) sawExactlyOneTerminalDespawn(playerID core.PlayerID) bool {
	despawns := 0
	despawnIndex := -1
	for index, event := range connected.transcript {
		switch message := event.message.(type) {
		case network.RemotePlayerDespawn:
			if message.PlayerID == playerID {
				despawns++
				despawnIndex = index
			}
		case network.RemotePlayerStates:
			if despawnIndex < 0 || index <= despawnIndex {
				continue
			}
			for _, state := range message.Players {
				if state.PlayerID == playerID {
					return false
				}
			}
		}
	}
	return despawns == 1
}

func assertMirrorBlockAndRevision(
	t *testing.T,
	connected *multiplayerTCPClient,
	position core.BlockPos,
	wantBlock core.BlockID,
	wantRevision uint64,
) {
	t.Helper()
	block, loaded := connected.mirror.BlockAt(core.Overworld, position)
	_, revision, hashed := connected.mirror.Hash(core.Overworld, position.Chunk())
	if !loaded || block != wantBlock || !hashed || revision != wantRevision {
		t.Fatalf("%s mirror block %+v=(id=%d loaded=%v revision=%d hashed=%v), want (%d,true,%d,true)",
			connected.identity.DisplayName, position, block, loaded, revision, hashed, wantBlock, wantRevision)
	}
}

func mirrorsConvergedAt(
	left, right *multiplayerTCPClient,
	position core.BlockPos,
	wantBlock core.BlockID,
	wantRevision uint64,
) bool {
	leftBlock, leftLoaded := left.mirror.BlockAt(core.Overworld, position)
	rightBlock, rightLoaded := right.mirror.BlockAt(core.Overworld, position)
	leftHash, leftRevision, leftHashed := left.mirror.Hash(core.Overworld, position.Chunk())
	rightHash, rightRevision, rightHashed := right.mirror.Hash(core.Overworld, position.Chunk())
	return leftLoaded && rightLoaded && leftBlock == wantBlock && rightBlock == wantBlock &&
		leftHashed && rightHashed && leftRevision == wantRevision && rightRevision == wantRevision &&
		leftHash == rightHash
}

func vec3BlockPos(position mgl32.Vec3) core.BlockPos {
	return core.BlockPos{
		X: int32(math.Floor(float64(position[0]))),
		Y: int32(math.Floor(float64(position[1]))),
		Z: int32(math.Floor(float64(position[2]))),
	}
}

func multiplayerDiagnostics(first, second *multiplayerTCPClient) string {
	var output strings.Builder
	for _, connected := range []*multiplayerTCPClient{first, second} {
		if connected == nil {
			continue
		}
		fmt.Fprintf(&output, "%s local=%+v roster=%+v transcript:\n", connected.identity.DisplayName, connected.local, connected.remotes.Presentations())
		for index, event := range connected.transcript {
			fmt.Fprintf(&output, "  %03d %s\n", index, multiplayerEventSummary(event.message))
		}
	}
	return output.String()
}

func multiplayerEventSummary(message network.ServerMessage) string {
	switch message := message.(type) {
	case network.PlayerState:
		return fmt.Sprintf("PlayerState tick=%d input=%d pos=%v ready=%v reset=%v mining=%t target=%+v progress=%d/%d harvestable=%t",
			message.ServerTick, message.LastInputSequence, message.Position, message.Ready, message.Reset,
			message.MiningActive, message.MiningTarget, message.MiningProgressTicks,
			message.MiningRequiredTicks, message.MiningHarvestable)
	case network.ChunkSnapshot:
		return fmt.Sprintf("ChunkSnapshot chunk=%+v revision=%d", message.Chunk, message.Revision)
	case network.BlockChanges:
		return fmt.Sprintf("BlockChanges chunk=%+v revision=%d->%d changes=%+v", message.Chunk, message.BaseRevision, message.NewRevision, message.Changes)
	case network.RemotePlayerSpawn:
		return fmt.Sprintf("RemotePlayerSpawn id=%s name=%q tick=%d pos=%v", message.PlayerID, message.DisplayName, message.ServerTick, message.Position)
	case network.RemotePlayerStates:
		return fmt.Sprintf("RemotePlayerStates tick=%d players=%+v", message.ServerTick, message.Players)
	case network.RemotePlayerDespawn:
		return fmt.Sprintf("RemotePlayerDespawn id=%s", message.PlayerID)
	default:
		return fmt.Sprintf("%T %+v", message, message)
	}
}
