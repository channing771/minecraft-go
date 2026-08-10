package server

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/storage"
)

type testLogin struct {
	Client   network.ClientEndpoint
	Done     <-chan error
	Identity network.Identity
}

func newTestHost(t *testing.T) *Host {
	t.Helper()
	return newTestHostWithStore(t, newHostTestStore())
}

func newTestHostWithStore(t *testing.T, store storage.WorldStore) *Host {
	t.Helper()
	host := NewHost(hostTestConfig(), flatTestGenerator{}, store)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
		defer cancel()
		if err := host.Shutdown(ctx); err != nil {
			t.Errorf("Host cleanup Shutdown: %v", err)
		}
	})
	return host
}

func hostTestConfig() Config {
	config := DefaultConfig(42)
	config.MaxPlayers = 1
	config.ViewRadius = 0
	config.Workers = 1
	config.SaveWorkers = 1
	config.SnapshotChunks = 16
	config.SnapshotBytes = 1 << 20
	config.OutboxCapacity = 256
	return config
}

func startMemoryLogin(t *testing.T, host *Host, identity network.Identity) testLogin {
	t.Helper()
	clientStream, serverStream := network.NewMemoryStreamPair(256)
	done := make(chan error, 1)
	go func() { done <- host.AcceptStream(context.Background(), serverStream) }()
	client, err := network.LoginClient(context.Background(), clientStream, identity)
	if err != nil {
		t.Fatalf("LoginClient: %v", err)
	}
	return testLogin{Client: client, Done: done, Identity: identity}
}

func playerIdentity(number byte) network.Identity {
	return network.Identity{
		PlayerID:    core.PlayerID{0, 0, 0, 0, 0, 0, 0x40, 0, 0x80, 0, 0, 0, 0, 0, 0, number},
		DisplayName: fmt.Sprintf("player-%d", number),
	}
}

func waitReady(t *testing.T, host *Host, login testLogin) {
	t.Helper()
	deadline := time.Now().Add(waitDeadline)
	for time.Now().Before(deadline) {
		host.mu.Lock()
		active := host.activeByPlayer[login.Identity.PlayerID]
		host.mu.Unlock()
		if active != nil {
			if state, ok := host.world.PlayerStateFor(active.Session); ok && state.Ready {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("player did not become ready")
}

func waitForPreLoginCount(t *testing.T, host *Host, want int) {
	t.Helper()
	deadline := time.Now().Add(waitDeadline)
	for time.Now().Before(deadline) {
		if len(host.preLogin) == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("pre-login count = %d, want %d", len(host.preLogin), want)
}

func waitForNoActiveLogin(t *testing.T, host *Host) {
	t.Helper()
	deadline := time.Now().Add(waitDeadline)
	for time.Now().Before(deadline) {
		host.mu.Lock()
		active := len(host.activeByPlayer)
		host.mu.Unlock()
		if active == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("active login was not released")
}

func startMultiHost(t *testing.T, store storage.WorldStore) (*Host, func()) {
	t.Helper()
	config := hostTestConfig()
	config.MaxPlayers = 8
	return startHostWithConfig(t, config, store)
}

func startHostWithConfig(t *testing.T, config Config, store storage.WorldStore) (*Host, func()) {
	t.Helper()
	host := NewHost(config, flatTestGenerator{}, store)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- host.Run(ctx, nil) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("Host Run cleanup: %v", err)
				}
			case <-time.After(waitDeadline):
				t.Error("Host Run cleanup timed out")
			}
		})
	}
	t.Cleanup(stop)
	return host, stop
}

type endpointProgress struct {
	login        testLogin
	nextSequence uint64
	nextMovement [2]int8
	states       <-chan network.PlayerState
	err          <-chan error
	cancel       context.CancelFunc
}

func loginHealthyMemoryPlayers(t *testing.T, host *Host, count int, sequenceBase uint64) []endpointProgress {
	t.Helper()
	movements := [][2]int8{
		{-1, -1}, {-1, 0}, {-1, 1}, {0, -1}, {0, 1}, {1, -1}, {1, 0}, {1, 1},
	}
	progress := make([]endpointProgress, 0, count)
	for index := 0; index < count; index++ {
		login := startMemoryLogin(t, host, playerIdentity(byte(index+1)))
		waitReady(t, host, login)
		current := monitorEndpointProgress(
			login,
			sequenceBase+uint64(index),
			movements[index],
		)
		t.Cleanup(current.cancel)
		progress = append(progress, current)
	}
	return progress
}

func monitorEndpointProgress(
	login testLogin,
	nextSequence uint64,
	nextMovement [2]int8,
) endpointProgress {
	ctx, cancel := context.WithCancel(context.Background())
	states := make(chan network.PlayerState, 1)
	errResult := make(chan error, 1)
	go func() {
		for {
			message, err := login.Client.Recv(ctx)
			if err != nil {
				if ctx.Err() == nil {
					errResult <- err
				}
				return
			}
			if state, ok := message.(network.PlayerState); ok {
				select {
				case states <- state:
				default:
					select {
					case <-states:
					default:
					}
					states <- state
				}
			}
		}
	}()
	return endpointProgress{
		login:        login,
		nextSequence: nextSequence,
		nextMovement: nextMovement,
		states:       states,
		err:          errResult,
		cancel:       cancel,
	}
}

func assertHealthyHostProgress(t *testing.T, host *Host, healthy []endpointProgress) {
	t.Helper()
	for index := range healthy {
		progress := &healthy[index]
		sequence := progress.nextSequence
		ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
		if err := progress.login.Client.Send(ctx, network.PlayerInput{
			Sequence: sequence,
			MoveX:    progress.nextMovement[0],
			MoveZ:    progress.nextMovement[1],
		}); err != nil {
			cancel()
			t.Fatalf("healthy endpoint post-cleanup send: %v", err)
		}
		acknowledged := false
		for !acknowledged {
			select {
			case state := <-progress.states:
				if state.LastInputSequence >= sequence {
					acknowledged = true
				}
			case err := <-progress.err:
				cancel()
				t.Fatalf("healthy endpoint post-cleanup recv: %v", err)
			case <-ctx.Done():
				cancel()
				t.Fatalf("healthy endpoint did not acknowledge post-cleanup sequence %d", sequence)
			}
		}
		cancel()
		progress.nextSequence++
	}
	host.mu.Lock()
	players, sessions := len(host.activeByPlayer), len(host.activeBySession)
	host.mu.Unlock()
	if players != len(healthy) || sessions != len(healthy) {
		t.Fatalf("healthy indexes = players %d sessions %d, want %d/%d", players, sessions, len(healthy), len(healthy))
	}
	tick := host.world.TickCount()
	waitForTickAfter(t, host, tick)
}

func startMemoryLoginWithCapacity(t *testing.T, host *Host, identity network.Identity, capacity int) testLogin {
	t.Helper()
	clientStream, serverStream := network.NewMemoryStreamPair(capacity)
	done := make(chan error, 1)
	go func() { done <- host.AcceptStream(context.Background(), serverStream) }()
	client, err := network.LoginClient(context.Background(), clientStream, identity)
	if err != nil {
		t.Fatalf("LoginClient: %v", err)
	}
	return testLogin{Client: client, Done: done, Identity: identity}
}

type playErrorServerStream struct {
	network.ServerPacketStream
	err error
}

func (stream *playErrorServerStream) Recv(ctx context.Context, state network.State) (network.ClientPacket, error) {
	if state == network.StatePlay {
		return nil, stream.err
	}
	return stream.ServerPacketStream.Recv(ctx, state)
}

func loginEightMemoryPlayers(t *testing.T, host *Host) []testLogin {
	t.Helper()
	logins := make([]testLogin, 0, 8)
	for number := byte(1); number <= 8; number++ {
		login := startMemoryLogin(t, host, playerIdentity(number))
		waitReady(t, host, login)
		logins = append(logins, login)
	}
	return logins
}

func activeLoginForPlayer(t *testing.T, host *Host, id core.PlayerID) activeLogin {
	t.Helper()
	host.mu.Lock()
	entry := host.activeByPlayer[id]
	if entry == nil {
		host.mu.Unlock()
		t.Fatalf("active login for %s not found", id)
	}
	got := *entry
	host.mu.Unlock()
	return got
}

func waitForPlayerReleased(t *testing.T, host *Host, id core.PlayerID) {
	t.Helper()
	deadline := time.Now().Add(waitDeadline)
	for time.Now().Before(deadline) {
		host.mu.Lock()
		byPlayer := host.activeByPlayer[id]
		bySession := false
		for _, entry := range host.activeBySession {
			if entry.PlayerID == id {
				bySession = true
				break
			}
		}
		host.mu.Unlock()
		if byPlayer == nil && !bySession {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("active login for %s was not released from both indexes", id)
}

func waitLoginDone(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(waitDeadline):
		t.Fatal("login worker did not exit")
	}
}

func assertLoginRejectCode(t *testing.T, err error, want network.LoginRejectCode) {
	t.Helper()
	var remote *network.RemoteError
	if !errors.As(err, &remote) || remote.State != network.StateLogin ||
		network.LoginRejectCode(remote.Code) != want {
		t.Fatalf("login error = %v, want code %d", err, want)
	}
}

func assertLoginCanAdvance(t *testing.T, login testLogin, sequence uint64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
	defer cancel()
	if err := login.Client.Send(ctx, network.PlayerInput{Sequence: sequence}); err != nil {
		t.Fatalf("send on existing login: %v", err)
	}
	for {
		message, err := login.Client.Recv(ctx)
		if err != nil {
			t.Fatalf("recv on existing login: %v", err)
		}
		if state, ok := message.(network.PlayerState); ok && state.LastInputSequence >= sequence {
			return
		}
	}
}

func waitForPlayerSave(t *testing.T, store *hostTestStore) {
	t.Helper()
	deadline := time.Now().Add(waitDeadline)
	for store.saveCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if store.saveCount() == 0 {
		t.Fatal("player save did not start")
	}
}

func waitForTickAfter(t *testing.T, host *Host, tick uint64) {
	t.Helper()
	deadline := time.Now().Add(shortWaitDeadline)
	for host.world.TickCount() <= tick && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := host.world.TickCount(); got <= tick {
		t.Fatalf("world tick = %d after disconnect, want > %d", got, tick)
	}
}

func shutdownHostComponentsForTest(t *testing.T, host *Host) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
	defer cancel()
	if err := host.players.Flush(ctx); err != nil {
		t.Errorf("player Flush: %v", err)
	}
	if err := host.world.Shutdown(ctx); err != nil {
		t.Errorf("world Shutdown: %v", err)
	}
	host.players.CloseWorker()
}

func attemptMemoryLogin(host *Host, identity network.Identity) (network.ClientEndpoint, error) {
	client, server := network.NewMemoryStreamPair(32)
	done := make(chan error, 1)
	go func() { done <- host.AcceptStream(context.Background(), server) }()
	endpoint, err := network.LoginClient(context.Background(), client, identity)
	if err != nil {
		<-done
	}
	return endpoint, err
}

type hostTestStore struct {
	*storage.MemoryStore
	mu              sync.Mutex
	loads           int
	saves           int
	syncs           int
	closes          int
	loadErr         error
	saveErr         error
	events          []string
	loadStarted     chan struct{}
	loadRelease     chan struct{}
	loadStartOnce   sync.Once
	loadReleaseOnce sync.Once
}

func (store *hostTestStore) SavePlayer(ctx context.Context, save storage.PlayerSave) (uint64, error) {
	store.mu.Lock()
	store.saves++
	store.events = append(store.events, "save")
	err := store.saveErr
	store.mu.Unlock()
	if err != nil {
		return 0, err
	}
	return store.MemoryStore.SavePlayer(ctx, save)
}

func (store *hostTestStore) Sync(ctx context.Context) error {
	store.mu.Lock()
	store.syncs++
	store.events = append(store.events, "sync")
	store.mu.Unlock()
	return store.MemoryStore.Sync(ctx)
}

func (store *hostTestStore) Close() error {
	store.mu.Lock()
	store.closes++
	store.events = append(store.events, "close")
	store.mu.Unlock()
	return store.MemoryStore.Close()
}

func newHostTestStore() *hostTestStore {
	return &hostTestStore{MemoryStore: storage.NewMemory(storage.Metadata{
		FormatVersion:  2,
		Seed:           42,
		SpawnDimension: core.Overworld,
	})}
}

func (store *hostTestStore) LoadPlayer(ctx context.Context, id core.PlayerID) (storage.StoredPlayer, error) {
	store.mu.Lock()
	store.loads++
	err := store.loadErr
	started := store.loadStarted
	release := store.loadRelease
	store.mu.Unlock()
	if started != nil {
		store.loadStartOnce.Do(func() { close(started) })
		select {
		case <-release:
		case <-ctx.Done():
			return storage.StoredPlayer{}, ctx.Err()
		}
	}
	if err != nil {
		return storage.StoredPlayer{}, err
	}
	return store.MemoryStore.LoadPlayer(ctx, id)
}

func (store *hostTestStore) blockLoads() {
	store.mu.Lock()
	store.loadStarted = make(chan struct{})
	store.loadRelease = make(chan struct{})
	store.mu.Unlock()
}

func (store *hostTestStore) waitLoadStarted(t *testing.T) {
	t.Helper()
	store.mu.Lock()
	started := store.loadStarted
	store.mu.Unlock()
	select {
	case <-started:
	case <-time.After(waitDeadline):
		t.Fatal("LoadPlayer did not start")
	}
}

func (store *hostTestStore) releaseLoads() {
	store.mu.Lock()
	release := store.loadRelease
	store.mu.Unlock()
	store.loadReleaseOnce.Do(func() { close(release) })
}

func (store *hostTestStore) loadCount() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.loads
}

func (store *hostTestStore) saveCount() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.saves
}

func (store *hostTestStore) setSaveError(err error) {
	store.mu.Lock()
	store.saveErr = err
	store.mu.Unlock()
}

func (store *hostTestStore) syncCount() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.syncs
}

func (store *hostTestStore) closeCount() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.closes
}

func (store *hostTestStore) eventsSnapshot() []string {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]string(nil), store.events...)
}

type hostTestListener struct {
	streams chan network.ServerPacketStream
	closed  chan struct{}
	once    sync.Once
}

func newHostTestListener() *hostTestListener {
	return &hostTestListener{
		streams: make(chan network.ServerPacketStream, 8),
		closed:  make(chan struct{}),
	}
}

func (listener *hostTestListener) Accept(ctx context.Context) (network.ServerPacketStream, error) {
	select {
	case stream := <-listener.streams:
		return stream, nil
	case <-listener.closed:
		return nil, network.ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (listener *hostTestListener) Addr() string { return "test" }

func (listener *hostTestListener) Close() error {
	listener.once.Do(func() { close(listener.closed) })
	return nil
}

type burstHostListener struct {
	streams       []network.ServerPacketStream
	next          int
	maxGoroutines int
	acceptedAll   chan struct{}
	closed        chan struct{}
	closeOnce     sync.Once
	acceptedOnce  sync.Once
}

func newBurstHostListener(streams []network.ServerPacketStream) *burstHostListener {
	return &burstHostListener{
		streams:     streams,
		acceptedAll: make(chan struct{}),
		closed:      make(chan struct{}),
	}
}

func (listener *burstHostListener) Accept(ctx context.Context) (network.ServerPacketStream, error) {
	if listener.next < len(listener.streams) {
		if goroutines := runtime.NumGoroutine(); goroutines > listener.maxGoroutines {
			listener.maxGoroutines = goroutines
		}
		stream := listener.streams[listener.next]
		listener.next++
		if listener.next == len(listener.streams) {
			listener.acceptedOnce.Do(func() { close(listener.acceptedAll) })
		}
		return stream, nil
	}
	select {
	case <-listener.closed:
		return nil, network.ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (listener *burstHostListener) Addr() string { return "burst" }

func (listener *burstHostListener) Close() error {
	listener.closeOnce.Do(func() { close(listener.closed) })
	return nil
}
