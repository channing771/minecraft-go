package server

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"minecraft-go/internal/network"
)

func TestHeartbeatDetachStopsEveryTimer(t *testing.T) {
	clock := newManualHeartbeatClock()
	config := heartbeatTestConfig(clock)
	running := NewWorld(config, playerTestGenerator{}, testStore())
	endpoint := newHeartbeatEndpoint()
	exit, err := running.AttachSession(registrySessionSpec(7, 1, endpoint))
	if err != nil {
		t.Fatal(err)
	}

	interval := clock.nextTimer(t, config.HeartbeatInterval)
	interval.fire()
	nextInterval := clock.nextTimer(t, config.HeartbeatInterval)
	_ = clock.nextTimer(t, config.HeartbeatTimeout)
	if message := endpoint.nextSent(t); message != (network.KeepAlive{Token: 1}) {
		t.Fatalf("heartbeat = %#v", message)
	}

	if !running.DetachSession(7, 1, nil) {
		t.Fatal("DetachSession = false")
	}
	<-exit
	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
	defer cancel()
	if err := running.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if !nextInterval.stopped() {
		t.Fatal("detach 后下一 interval timer 仍处于活动状态")
	}
	if active := clock.activeTimers(); active != 0 {
		t.Fatalf("detach 后 active heartbeat timers = %d", active)
	}
}

func TestHeartbeatDefaultScheduleUsesFiveAndFifteenSeconds(t *testing.T) {
	clock := newManualHeartbeatClock()
	config := registryTestConfig()
	config.heartbeatClock = clock
	running := NewWorld(config, playerTestGenerator{}, testStore())
	t.Cleanup(func() { shutdownServerForTest(t, running) })
	endpoint := newHeartbeatEndpoint()
	if _, err := running.AttachSession(registrySessionSpec(7, 1, endpoint)); err != nil {
		t.Fatal(err)
	}
	clock.nextTimer(t, 5*time.Second).fire()
	_ = clock.nextTimer(t, 5*time.Second)
	if message := endpoint.nextSent(t); message != (network.KeepAlive{Token: 1}) {
		t.Fatalf("default heartbeat = %#v", message)
	}
	_ = clock.nextTimer(t, 15*time.Second)
}

func TestHeartbeatAllowsOnlyOneOutstandingAndUsesMonotonicTokens(t *testing.T) {
	clock := newManualHeartbeatClock()
	config := heartbeatTestConfig(clock)
	running := NewWorld(config, playerTestGenerator{}, testStore())
	t.Cleanup(func() { shutdownServerForTest(t, running) })
	endpoint := newHeartbeatEndpoint()
	_, err := running.AttachSession(registrySessionSpec(7, 1, endpoint))
	if err != nil {
		t.Fatal(err)
	}
	current := running.sessions[7]

	firstInterval := clock.nextTimer(t, config.HeartbeatInterval)
	firstInterval.fire()
	secondInterval := clock.nextTimer(t, config.HeartbeatInterval)
	if message := endpoint.nextSent(t); message != (network.KeepAlive{Token: 1}) {
		t.Fatalf("first heartbeat = %#v", message)
	}
	firstTimeout := clock.nextTimer(t, config.HeartbeatTimeout)

	secondInterval.fire()
	thirdInterval := clock.nextTimer(t, config.HeartbeatInterval)
	select {
	case message := <-endpoint.sent:
		t.Fatalf("outstanding token 未回复时又发送了 %#v", message)
	default:
	}

	endpoint.recv <- network.KeepAliveReply{Token: 1}
	waitHeartbeatOutstanding(t, current, 0)
	thirdInterval.fire()
	_ = clock.nextTimer(t, config.HeartbeatInterval)
	if message := endpoint.nextSent(t); message != (network.KeepAlive{Token: 2}) {
		t.Fatalf("second heartbeat = %#v", message)
	}
	_ = clock.nextTimer(t, config.HeartbeatTimeout)
	if !firstTimeout.stopped() {
		t.Fatal("匹配回复后首个 timeout timer 未停止")
	}
}

func TestHeartbeatStaleReplyNotificationDoesNotStopNewTokenTimeout(t *testing.T) {
	clock := newManualHeartbeatClock()
	config := heartbeatTestConfig(clock)
	running := NewWorld(config, playerTestGenerator{}, testStore())
	t.Cleanup(func() { shutdownServerForTest(t, running) })
	endpoint := newHeartbeatEndpoint()
	exit, err := running.AttachSession(registrySessionSpec(7, 1, endpoint))
	if err != nil {
		t.Fatal(err)
	}
	current := running.sessions[7]

	clock.nextTimer(t, config.HeartbeatInterval).fire()
	secondInterval := clock.nextTimer(t, config.HeartbeatInterval)
	if message := endpoint.nextSent(t); message != (network.KeepAlive{Token: 1}) {
		t.Fatalf("first heartbeat = %#v", message)
	}
	firstTimeout := clock.nextTimer(t, config.HeartbeatTimeout)

	current.mu.Lock()
	current.outstandingToken = 0
	current.mu.Unlock()
	secondInterval.fire()
	_ = clock.nextTimer(t, config.HeartbeatInterval)
	if message := endpoint.nextSent(t); message != (network.KeepAlive{Token: 2}) {
		t.Fatalf("second heartbeat = %#v", message)
	}
	secondTimeout := clock.nextTimer(t, config.HeartbeatTimeout)
	if !firstTimeout.stopped() {
		t.Fatal("token2 建立时没有停止 token1 timeout")
	}

	current.heartbeatReply <- uint64(1)
	waitHeartbeatReplyConsumed(t, current)
	if secondTimeout.stopped() {
		t.Fatal("token1 的迟到通知停止了 token2 timeout")
	}
	secondTimeout.fire()
	if got := waitSessionExit(t, exit); !errors.Is(got.Err, errHeartbeatTimeout) {
		t.Fatalf("token2 timeout exit = %+v", got)
	}
}

func TestHeartbeatRejectsMismatchedAndDuplicateTokens(t *testing.T) {
	tests := []struct {
		name    string
		replies []uint64
	}{
		{name: "mismatched", replies: []uint64{2}},
		{name: "duplicate", replies: []uint64{1, 1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clock := newManualHeartbeatClock()
			config := heartbeatTestConfig(clock)
			running := NewWorld(config, playerTestGenerator{}, testStore())
			t.Cleanup(func() { shutdownServerForTest(t, running) })
			endpoint := newHeartbeatEndpoint()
			exit, err := running.AttachSession(registrySessionSpec(7, 1, endpoint))
			if err != nil {
				t.Fatal(err)
			}
			current := running.sessions[7]

			clock.nextTimer(t, config.HeartbeatInterval).fire()
			_ = clock.nextTimer(t, config.HeartbeatInterval)
			if message := endpoint.nextSent(t); message != (network.KeepAlive{Token: 1}) {
				t.Fatalf("heartbeat = %#v", message)
			}
			_ = clock.nextTimer(t, config.HeartbeatTimeout)
			for index, token := range test.replies {
				endpoint.recv <- network.KeepAliveReply{Token: token}
				if index == 0 && token == 1 {
					waitHeartbeatOutstanding(t, current, 0)
				}
			}
			got := waitSessionExit(t, exit)
			if got.Err == nil || got.ID != 7 || got.Generation != 1 {
				t.Fatalf("invalid heartbeat exit = %+v", got)
			}
		})
	}
}

func TestHeartbeatTimeoutDetachesOnlyThatSession(t *testing.T) {
	clock := newManualHeartbeatClock()
	config := heartbeatTestConfig(clock)
	running := NewWorld(config, playerTestGenerator{}, testStore())
	t.Cleanup(func() { shutdownServerForTest(t, running) })
	failingEndpoint := newHeartbeatEndpoint()
	healthyEndpoint := newHeartbeatEndpoint()
	failingExit, err := running.AttachSession(registrySessionSpec(7, 1, failingEndpoint))
	if err != nil {
		t.Fatal(err)
	}

	clock.nextTimer(t, config.HeartbeatInterval).fire()
	_ = clock.nextTimer(t, config.HeartbeatInterval)
	_ = failingEndpoint.nextSent(t)
	timeout := clock.nextTimer(t, config.HeartbeatTimeout)
	if _, err := running.AttachSession(registrySessionSpec(8, 1, healthyEndpoint)); err != nil {
		t.Fatal(err)
	}
	_ = clock.nextTimer(t, config.HeartbeatInterval)
	timeout.fire()

	got := waitSessionExit(t, failingExit)
	if got.Err == nil {
		t.Fatalf("timeout exit = %+v", got)
	}
	if _, ok := running.PlayerStateFor(8); !ok {
		t.Fatal("一个 session timeout 关闭了健康 session")
	}
}

func waitHeartbeatReplyConsumed(t *testing.T, current *session) {
	t.Helper()
	deadline := time.NewTimer(waitDeadline)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if len(current.heartbeatReply) == 0 {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("heartbeat reply notification 未被消费")
		case <-ticker.C:
		}
	}
}

func waitHeartbeatOutstanding(t *testing.T, current *session, want uint64) {
	t.Helper()
	deadline := time.NewTimer(waitDeadline)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		current.mu.Lock()
		got := current.outstandingToken
		current.mu.Unlock()
		if got == want {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("outstanding token = %d，想要 %d", got, want)
		case <-ticker.C:
		}
	}
}

func waitSessionExit(t *testing.T, exit <-chan SessionExit) SessionExit {
	t.Helper()
	select {
	case got := <-exit:
		return got
	case <-time.After(waitDeadline):
		t.Fatal("等待 session exit 超时")
		return SessionExit{}
	}
}

func heartbeatTestConfig(clock heartbeatClock) Config {
	config := registryTestConfig()
	config.HeartbeatInterval = 5 * time.Second
	config.HeartbeatTimeout = 15 * time.Second
	config.heartbeatClock = clock
	return config
}

type manualHeartbeatClock struct {
	mu      sync.Mutex
	timers  []*manualHeartbeatTimer
	created chan *manualHeartbeatTimer
}

func newManualHeartbeatClock() *manualHeartbeatClock {
	return &manualHeartbeatClock{
		created: make(chan *manualHeartbeatTimer, 32),
	}
}

func (clock *manualHeartbeatClock) NewTimer(
	duration time.Duration,
) heartbeatTimer {
	timer := &manualHeartbeatTimer{
		duration: duration,
		channel:  make(chan time.Time, 1),
	}
	clock.mu.Lock()
	clock.timers = append(clock.timers, timer)
	clock.mu.Unlock()
	clock.created <- timer
	return timer
}

func (clock *manualHeartbeatClock) nextTimer(
	t *testing.T,
	want time.Duration,
) *manualHeartbeatTimer {
	t.Helper()
	select {
	case timer := <-clock.created:
		if timer.duration != want {
			t.Fatalf("new timer duration = %v，想要 %v", timer.duration, want)
		}
		return timer
	case <-time.After(waitDeadline):
		t.Fatalf("等待 %v timer 超时", want)
		return nil
	}
}

func (clock *manualHeartbeatClock) activeTimers() int {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	active := 0
	for _, timer := range clock.timers {
		if !timer.stopped() && !timer.fired() {
			active++
		}
	}
	return active
}

type manualHeartbeatTimer struct {
	duration time.Duration
	channel  chan time.Time

	mu        sync.Mutex
	isFired   bool
	isStopped bool
}

func (timer *manualHeartbeatTimer) C() <-chan time.Time {
	return timer.channel
}

func (timer *manualHeartbeatTimer) Stop() {
	timer.mu.Lock()
	timer.isStopped = true
	timer.mu.Unlock()
}

func (timer *manualHeartbeatTimer) fire() {
	timer.mu.Lock()
	if timer.isStopped || timer.isFired {
		timer.mu.Unlock()
		return
	}
	timer.isFired = true
	timer.mu.Unlock()
	timer.channel <- time.Time{}
}

func (timer *manualHeartbeatTimer) stopped() bool {
	timer.mu.Lock()
	defer timer.mu.Unlock()
	return timer.isStopped
}

func (timer *manualHeartbeatTimer) fired() bool {
	timer.mu.Lock()
	defer timer.mu.Unlock()
	return timer.isFired
}

type heartbeatEndpoint struct {
	sent chan network.ServerMessage
	recv chan network.ClientMessage
	done chan struct{}
	once sync.Once
}

func newHeartbeatEndpoint() *heartbeatEndpoint {
	return &heartbeatEndpoint{
		sent: make(chan network.ServerMessage, 16),
		recv: make(chan network.ClientMessage, 16),
		done: make(chan struct{}),
	}
}

func (endpoint *heartbeatEndpoint) Send(
	ctx context.Context,
	message network.ServerMessage,
) error {
	select {
	case endpoint.sent <- message:
		return nil
	case <-endpoint.done:
		return network.ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (endpoint *heartbeatEndpoint) Recv(
	ctx context.Context,
) (network.ClientMessage, error) {
	select {
	case message := <-endpoint.recv:
		return message, nil
	case <-endpoint.done:
		return nil, network.ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (endpoint *heartbeatEndpoint) Close() error {
	endpoint.once.Do(func() { close(endpoint.done) })
	return nil
}

func (endpoint *heartbeatEndpoint) nextSent(t *testing.T) network.ServerMessage {
	t.Helper()
	select {
	case message := <-endpoint.sent:
		return message
	case <-time.After(waitDeadline):
		t.Fatal("等待 heartbeat 发送超时")
		return nil
	}
}

var _ heartbeatClock = (*manualHeartbeatClock)(nil)
var _ heartbeatTimer = (*manualHeartbeatTimer)(nil)
var _ network.ServerEndpoint = (*heartbeatEndpoint)(nil)
