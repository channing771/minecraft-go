package server

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/storage"
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

func assertLoginRejectCode(t *testing.T, err error, want network.LoginRejectCode) {
	t.Helper()
	var remote *network.RemoteError
	if !errors.As(err, &remote) || remote.State != network.StateLogin ||
		network.LoginRejectCode(remote.Code) != want {
		t.Fatalf("login error = %v, want code %d", err, want)
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
