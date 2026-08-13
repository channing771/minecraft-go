package server

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/sim"
)

const testSessionID sim.SessionID = 1

func TestNewWorldStartsWithoutAttachedPlayer(t *testing.T) {
	running := NewWorld(registryTestConfig(), playerTestGenerator{}, testStore())
	t.Cleanup(func() { shutdownServerForTest(t, running) })
	if state, ok := running.PlayerStateFor(testSessionID); ok {
		t.Fatalf("NewWorld 注册了玩家: %+v", state)
	}
}

func TestCompatibilityNewAttachesLocalPlayer(t *testing.T) {
	_, endpoint := network.NewMemoryPair(8)
	running := newAttachedWorldForTest(registryTestConfig(), endpoint, playerTestGenerator{}, testStore())
	t.Cleanup(func() { shutdownServerForTest(t, running) })
	if state, ok := running.PlayerStateFor(testSessionID); !ok ||
		state.Session != testSessionID {
		t.Fatalf("兼容 New 的玩家 = (%+v, %v)", state, ok)
	}
}

func TestRunTicksCancellationDoesNotCloseWorldStore(t *testing.T) {
	store := newShutdownTestStore()
	running := NewWorld(registryTestConfig(), playerTestGenerator{}, store)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := running.RunTicks(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("RunTicks error = %v", err)
	}
	if syncCalls, closeCalls := store.lifecycleCalls(); syncCalls != 0 || closeCalls != 0 {
		t.Fatalf("RunTicks cancellation lifecycle Sync=%d Close=%d", syncCalls, closeCalls)
	}
	if !store.worldOwned() {
		t.Fatal("RunTicks cancellation 释放了 Store ownership")
	}
	shutdownServerForTest(t, running)
}

func TestRunTicksReportsTickerScheduledTime(t *testing.T) {
	type tickSample struct {
		scheduled time.Time
		completed time.Time
		duration  time.Duration
	}
	config := registryTestConfig()
	samples := make(chan tickSample, 1)
	config.ScheduledTickObserver = func(scheduled time.Time, duration time.Duration) {
		select {
		case samples <- tickSample{
			scheduled: scheduled,
			completed: time.Now(),
			duration:  duration,
		}:
		default:
		}
	}
	running := NewWorld(config, playerTestGenerator{}, testStore())
	t.Cleanup(func() { shutdownServerForTest(t, running) })

	running.stepMu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- running.RunTicks(ctx) }()
	time.Sleep(120 * time.Millisecond)
	running.stepMu.Unlock()

	var sample tickSample
	select {
	case sample = <-samples:
	case <-time.After(waitDeadline):
		cancel()
		t.Fatal("RunTicks did not report a scheduled tick")
	}
	cancel()
	if err := <-runDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("RunTicks error = %v", err)
	}
	if sample.scheduled.IsZero() {
		t.Fatal("RunTicks reported a zero scheduled time")
	}
	stepStarted := sample.completed.Add(-sample.duration)
	if blockedFor := stepStarted.Sub(sample.scheduled); blockedFor < 20*time.Millisecond {
		t.Fatalf(
			"scheduled tick drifted to lock acquisition: blocked=%s scheduled=%s started=%s",
			blockedFor, sample.scheduled, stepStarted,
		)
	}
}

func TestTrustedObserverIsSeparateAndHasNoHeartbeat(t *testing.T) {
	clock := newManualHeartbeatClock()
	config := heartbeatTestConfig(clock)
	config.TrustedObserver = true
	running := NewWorld(config, playerTestGenerator{}, testStore())
	endpoint := newHeartbeatEndpoint()
	if err := running.AttachTrustedObserver(endpoint); err != nil {
		t.Fatal(err)
	}
	if len(running.sessions) != 0 {
		t.Fatalf("trusted observer 进入玩家 registry: %+v", running.sessions)
	}
	if _, ok := running.PlayerStateFor(trustedObserverSessionID); ok {
		t.Fatal("trusted observer 注册成玩家")
	}
	select {
	case timer := <-clock.created:
		t.Fatalf("trusted observer 启动了 heartbeat timer: %v", timer.duration)
	default:
	}
	if err := running.SetTrustedObserverCenter(
		core.Overworld,
		core.ChunkPos{X: 4},
	); err != nil {
		t.Fatal(err)
	}
	if result := running.StepForTest(); !containsChunk(result.Acquire, core.ChunkPos{X: 4}) {
		t.Fatalf("trusted observer Acquire = %+v", result.Acquire)
	}
	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
	defer cancel()
	if err := running.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestCloseTrustedObserverSynchronouslyDetachesAndClosesEndpoint(t *testing.T) {
	config := registryTestConfig()
	config.TrustedObserver = true
	running := NewWorld(config, playerTestGenerator{}, testStore())
	t.Cleanup(func() { shutdownServerForTest(t, running) })

	clientEndpoint, serverEndpoint := network.NewMemoryPair(8)
	t.Cleanup(func() { _ = clientEndpoint.Close() })
	wantCloseErr := errors.New("注入 observer 关闭失败")
	endpoint := &countingCloseServerEndpoint{ServerEndpoint: serverEndpoint, closeErr: wantCloseErr}
	if err := running.AttachTrustedObserver(endpoint); err != nil {
		t.Fatal(err)
	}
	center := core.ChunkPos{X: 4}
	key := core.ChunkKey{Dimension: core.Overworld, Pos: center}
	if err := running.SetTrustedObserverCenter(core.Overworld, center); err != nil {
		t.Fatal(err)
	}
	running.StepForTest()
	if !running.engine.SessionWantsChunk(trustedObserverSessionID, key) {
		t.Fatal("trusted observer 没有建立区块订阅")
	}

	if err := running.CloseTrustedObserver(); !errors.Is(err, wantCloseErr) {
		t.Fatalf("CloseTrustedObserver error=%v，想要 %v", err, wantCloseErr)
	}
	if got := endpoint.closeCalls.Load(); got != 1 {
		t.Fatalf("endpoint Close 调用=%d，想要 1", got)
	}
	if running.engine.SessionWantsChunk(trustedObserverSessionID, key) {
		t.Fatal("显式关闭返回后 trusted observer 订阅仍存在")
	}
	if err := running.SetTrustedObserverCenter(core.Overworld, center); !errors.Is(err, ErrTrustedObserverDisabled) {
		t.Fatalf("显式关闭返回后 SetTrustedObserverCenter error=%v", err)
	}

	running.CloseTrustedObserver()
	if got := endpoint.closeCalls.Load(); got != 1 {
		t.Fatalf("重复关闭后的 endpoint Close 调用=%d，想要 1", got)
	}
}

func TestTrustedObserverWriterFailureDetachesAndAllowsReattach(t *testing.T) {
	config := registryTestConfig()
	config.TrustedObserver = true
	running := NewWorld(config, playerTestGenerator{}, testStore())
	t.Cleanup(func() { shutdownServerForTest(t, running) })

	failed := &failingSendServerEndpoint{
		err:     errors.New("observer send failed"),
		started: make(chan struct{}),
	}
	if err := running.AttachTrustedObserver(failed); err != nil {
		t.Fatal(err)
	}
	center := core.ChunkPos{X: 4}
	key := core.ChunkKey{Dimension: core.Overworld, Pos: center}
	if err := running.SetTrustedObserverCenter(core.Overworld, center); err != nil {
		t.Fatal(err)
	}
	running.StepForTest()
	if !running.engine.SessionWantsChunk(trustedObserverSessionID, key) {
		t.Fatal("trusted observer 没有建立区块订阅")
	}

	running.stepMu.Lock()
	observer := running.trustedObserver
	running.stepMu.Unlock()
	if !observer.enqueue(network.CommandRejected{Sequence: 1}) {
		t.Fatal("writer failure 消息未入队")
	}
	select {
	case <-failed.started:
	case <-time.After(waitDeadline):
		t.Fatal("writer 没有尝试发送")
	}

	deadline := time.Now().Add(waitDeadline)
	for time.Now().Before(deadline) {
		running.stepMu.Lock()
		detached := running.trustedObserver == nil
		running.stepMu.Unlock()
		if detached {
			break
		}
		time.Sleep(time.Millisecond)
	}
	running.stepMu.Lock()
	detached := running.trustedObserver == nil
	running.stepMu.Unlock()
	if !detached {
		t.Fatal("writer failure 后 trusted observer 仍挂载")
	}

	running.StepForTest()
	if running.engine.SessionWantsChunk(trustedObserverSessionID, key) {
		t.Fatal("writer failure 后 trusted observer 订阅仍存在")
	}
	_, replacement := network.NewMemoryPair(8)
	if err := running.AttachTrustedObserver(replacement); err != nil {
		t.Fatalf("writer failure 后重新挂载: %v", err)
	}
}

func TestTrustedObserverFullOutboxDetachesAndAllowsReattach(t *testing.T) {
	config := registryTestConfig()
	config.TrustedObserver = true
	config.OutboxCapacity = 1
	running := NewWorld(config, playerTestGenerator{}, testStore())
	t.Cleanup(func() { shutdownServerForTest(t, running) })

	blocked := newBlockingServerEndpoint()
	if err := running.AttachTrustedObserver(blocked); err != nil {
		t.Fatal(err)
	}
	center := core.ChunkPos{X: 5}
	key := core.ChunkKey{Dimension: core.Overworld, Pos: center}
	if err := running.SetTrustedObserverCenter(core.Overworld, center); err != nil {
		t.Fatal(err)
	}
	running.StepForTest()

	running.stepMu.Lock()
	observer := running.trustedObserver
	running.stepMu.Unlock()
	if !observer.enqueue(network.CommandRejected{Sequence: 1}) {
		t.Fatal("首条消息未入队")
	}
	select {
	case <-blocked.sendStarted:
	case <-time.After(waitDeadline):
		t.Fatal("writer 没有进入阻塞 Send")
	}

	running.stepMu.Lock()
	running.publish(sim.TickResult{Rejected: []sim.Rejection{{
		Session: trustedObserverSessionID, Sequence: 2,
	}}})
	running.publish(sim.TickResult{Rejected: []sim.Rejection{{
		Session: trustedObserverSessionID, Sequence: 3,
	}}})
	detached := running.trustedObserver == nil
	running.stepMu.Unlock()
	if !detached {
		t.Fatal("满 outbox 后 trusted observer 仍挂载")
	}

	running.StepForTest()
	if running.engine.SessionWantsChunk(trustedObserverSessionID, key) {
		t.Fatal("满 outbox 后 trusted observer 订阅仍存在")
	}
	_, replacement := network.NewMemoryPair(8)
	if err := running.AttachTrustedObserver(replacement); err != nil {
		t.Fatalf("满 outbox 后重新挂载: %v", err)
	}
}

func TestTrustedObserverLateFailureCannotDetachReplacement(t *testing.T) {
	config := registryTestConfig()
	config.TrustedObserver = true
	running := NewWorld(config, playerTestGenerator{}, testStore())
	t.Cleanup(func() { shutdownServerForTest(t, running) })

	_, firstEndpoint := network.NewMemoryPair(8)
	if err := running.AttachTrustedObserver(firstEndpoint); err != nil {
		t.Fatal(err)
	}
	running.stepMu.Lock()
	oldGeneration := running.trustedObserver.generation
	running.stepMu.Unlock()
	if !running.detachTrustedObserver(
		trustedObserverSessionID,
		oldGeneration,
		network.ErrClosed,
	) {
		t.Fatal("旧 observer detach 失败")
	}

	_, replacementEndpoint := network.NewMemoryPair(8)
	if err := running.AttachTrustedObserver(replacementEndpoint); err != nil {
		t.Fatal(err)
	}
	running.stepMu.Lock()
	replacement := running.trustedObserver
	running.stepMu.Unlock()
	if running.detachTrustedObserver(
		trustedObserverSessionID,
		oldGeneration,
		errors.New("late writer failure"),
	) {
		t.Fatal("延迟失败错误地 detach 新 observer")
	}
	running.stepMu.Lock()
	stillAttached := running.trustedObserver == replacement
	running.stepMu.Unlock()
	if !stillAttached {
		t.Fatal("延迟失败移除了新 observer")
	}
}

type failingSendServerEndpoint struct {
	err     error
	started chan struct{}
	once    sync.Once
}

type countingCloseServerEndpoint struct {
	network.ServerEndpoint
	closeCalls atomic.Int32
	closeErr   error
}

func (endpoint *countingCloseServerEndpoint) Close() error {
	endpoint.closeCalls.Add(1)
	return errors.Join(endpoint.ServerEndpoint.Close(), endpoint.closeErr)
}

func (endpoint *failingSendServerEndpoint) Send(
	_ context.Context,
	_ network.ServerMessage,
) error {
	endpoint.once.Do(func() { close(endpoint.started) })
	return endpoint.err
}

func (endpoint *failingSendServerEndpoint) Recv(
	ctx context.Context,
) (network.ClientMessage, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (endpoint *failingSendServerEndpoint) Close() error {
	return nil
}
