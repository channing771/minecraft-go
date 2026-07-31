package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"runtime"
	"sync"
	"testing"
	"time"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/storage"
)

type testLogin struct {
	Client   network.ClientEndpoint
	Done     <-chan error
	Identity network.Identity
}

func TestHostAllowsExactlyOneConcurrentLogin(t *testing.T) {
	host := newTestHost(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go host.Run(ctx, nil)

	first := startMemoryLogin(t, host, playerIdentity(1))
	waitReady(t, host, first)

	secondStream, secondServer := network.NewMemoryStreamPair(8)
	go host.AcceptStream(context.Background(), secondServer)
	_, err := network.LoginClient(context.Background(), secondStream, playerIdentity(2))
	var remote *network.RemoteError
	if !errors.As(err, &remote) || remote.State != network.StateLogin ||
		network.LoginRejectCode(remote.Code) != network.LoginServerFull {
		t.Fatalf("second login err=%v", err)
	}
}

func TestHostClosesSeventeenthPreLoginImmediately(t *testing.T) {
	host := newTestHost(t)
	clients := make([]network.ClientPacketStream, 0, hostPreLoginCapacity)
	cancels := make([]context.CancelFunc, 0, hostPreLoginCapacity)
	done := make([]<-chan error, 0, hostPreLoginCapacity)
	for range hostPreLoginCapacity {
		client, server := network.NewMemoryStreamPair(1)
		streamCtx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() { result <- host.AcceptStream(streamCtx, server) }()
		clients = append(clients, client)
		cancels = append(cancels, cancel)
		done = append(done, result)
	}
	t.Cleanup(func() {
		for _, cancel := range cancels {
			cancel()
		}
		for _, client := range clients {
			_ = client.Close()
		}
		for _, result := range done {
			select {
			case <-result:
			case <-time.After(time.Second):
				t.Error("pre-login worker did not exit")
			}
		}
	})
	waitForPreLoginCount(t, host, hostPreLoginCapacity)

	seventeenth, server := network.NewMemoryStreamPair(1)
	result := make(chan error, 1)
	go func() { result <- host.AcceptStream(context.Background(), server) }()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := seventeenth.Recv(ctx, network.StateHandshake); !errors.Is(err, network.ErrClosed) {
		_ = seventeenth.Close()
		t.Fatalf("seventeenth pre-login Recv error = %v, want immediate transport close", err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, network.ErrClosed) {
			t.Fatalf("seventeenth AcceptStream error = %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("seventeenth AcceptStream did not return immediately")
	}
}

func TestHostListenerBoundsPreLoginGoroutines(t *testing.T) {
	previousProcs := runtime.GOMAXPROCS(1)
	t.Cleanup(func() { runtime.GOMAXPROCS(previousProcs) })

	const connections = 128
	clients := make([]network.ClientPacketStream, 0, connections)
	servers := make([]network.ServerPacketStream, 0, connections)
	for range connections {
		client, server := network.NewMemoryStreamPair(1)
		clients = append(clients, client)
		servers = append(servers, server)
	}
	t.Cleanup(func() {
		for _, client := range clients {
			_ = client.Close()
		}
	})

	baseline := runtime.NumGoroutine()
	listener := newBurstHostListener(servers)
	host := newTestHost(t)
	runCtx, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- host.Run(runCtx, listener) }()
	select {
	case <-listener.acceptedAll:
	case <-time.After(time.Second):
		t.Fatal("listener did not accept burst")
	}
	if got, limit := listener.maxGoroutines-baseline, hostPreLoginCapacity+12; got > limit {
		cancelRun()
		<-runDone
		t.Fatalf("listener burst created %d goroutines above baseline, want <= %d", got, limit)
	}
	cancelRun()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run cleanup: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run cleanup timed out")
	}
}

func TestLoginDeadlineCancelsBlockedHostPlayerLoad(t *testing.T) {
	store := newHostTestStore()
	store.blockLoads()
	defer store.releaseLoads()
	host := newTestHostWithStore(t, store)
	client, server := network.NewMemoryStreamPair(8)
	outer, cancelOuter := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancelOuter()
	serverDone := make(chan error, 1)
	started := time.Now()
	go func() { serverDone <- host.AcceptStream(outer, server) }()
	_, _ = network.LoginClient(outer, client, playerIdentity(14))
	select {
	case <-serverDone:
	case <-time.After(3 * time.Second):
		t.Fatal("AcceptStream outlived outer deadline")
	}
	elapsed := time.Since(started)
	if elapsed < network.LoginTimeout-500*time.Millisecond ||
		elapsed > network.LoginTimeout+1500*time.Millisecond {
		t.Fatalf("blocked player load held login for %s, want about %s", elapsed, network.LoginTimeout)
	}
	waitForNoActiveLogin(t, host)
	if got := len(host.preLogin); got != 0 {
		t.Fatalf("pre-login permits after deadline = %d, want 0", got)
	}

	store.releaseLoads()
	second, err := attemptMemoryLogin(host, playerIdentity(15))
	if err != nil {
		t.Fatalf("login after deadline slot release: %v", err)
	}
	_ = second.Close()
}

func TestHostMapsPlayerLoadErrors(t *testing.T) {
	tests := []struct {
		name    string
		loadErr error
		want    network.LoginRejectCode
	}{
		{name: "corrupt", loadErr: storage.ErrCorrupt, want: network.LoginPlayerDataCorrupt},
		{name: "future", loadErr: storage.ErrFutureVersion, want: network.LoginPlayerDataCorrupt},
		{name: "io", loadErr: io.ErrUnexpectedEOF, want: network.LoginStoreUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newHostTestStore()
			store.loadErr = test.loadErr
			host := newTestHostWithStore(t, store)
			_, err := attemptMemoryLogin(host, playerIdentity(3))
			var remote *network.RemoteError
			if !errors.As(err, &remote) || remote.State != network.StateLogin ||
				network.LoginRejectCode(remote.Code) != test.want {
				t.Fatalf("LoginClient error = %v, want login reject %d", err, test.want)
			}
			if got := store.loadCount(); got != 1 {
				t.Fatalf("LoadPlayer calls = %d, want 1", got)
			}
		})
	}
}

func TestHostReservesSlotBeforeSinglePlayerLoad(t *testing.T) {
	store := newHostTestStore()
	store.blockLoads()
	host := newTestHostWithStore(t, store)
	firstClient, firstServer := network.NewMemoryStreamPair(32)
	firstDone := make(chan error, 1)
	go func() { firstDone <- host.AcceptStream(context.Background(), firstServer) }()
	if err := firstClient.Send(context.Background(), network.StateHandshake,
		network.ClientHello{ProtocolVersion: network.ProtocolVersion}); err != nil {
		t.Fatal(err)
	}
	if _, err := firstClient.Recv(context.Background(), network.StateHandshake); err != nil {
		t.Fatal(err)
	}
	identity := playerIdentity(9)
	if err := firstClient.Send(context.Background(), network.StateLogin, network.LoginStart{
		PlayerID: identity.PlayerID, DisplayName: identity.DisplayName,
	}); err != nil {
		t.Fatal(err)
	}
	store.waitLoadStarted(t)

	_, secondErr := attemptMemoryLogin(host, playerIdentity(10))
	var remote *network.RemoteError
	if !errors.As(secondErr, &remote) ||
		network.LoginRejectCode(remote.Code) != network.LoginServerFull {
		t.Fatalf("second login error = %v, want server full", secondErr)
	}
	if got := store.loadCount(); got != 1 {
		t.Fatalf("LoadPlayer calls while slot reserved = %d, want 1", got)
	}
	noResponseCtx, cancelNoResponse := context.WithTimeout(context.Background(), 25*time.Millisecond)
	if _, err := firstClient.Recv(noResponseCtx, network.StateLogin); !errors.Is(err, context.DeadlineExceeded) {
		cancelNoResponse()
		t.Fatalf("first login response before player load completed: %v", err)
	}
	cancelNoResponse()

	store.releaseLoads()
	packet, err := firstClient.Recv(context.Background(), network.StateLogin)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := packet.(network.LoginSuccess); !ok {
		t.Fatalf("first login packet = %#v, want LoginSuccess", packet)
	}
	_ = firstClient.Close()
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first login worker did not exit")
	}
}

func TestHostRejectsSessionIDOverflowWithoutWrapping(t *testing.T) {
	host := newTestHost(t)
	host.nextSession = sim.SessionID(math.MaxUint64)
	_, err := attemptMemoryLogin(host, playerIdentity(4))
	var remote *network.RemoteError
	if !errors.As(err, &remote) || remote.State != network.StateLogin ||
		network.LoginRejectCode(remote.Code) != network.LoginInternalError {
		t.Fatalf("overflow LoginClient error = %v", err)
	}
	if host.nextSession != sim.SessionID(math.MaxUint64) {
		t.Fatalf("nextSession wrapped to %d", host.nextSession)
	}
}

func TestHostDisconnectPersistsReleasesSlotAndKeepsTicking(t *testing.T) {
	store := newHostTestStore()
	host := newTestHostWithStore(t, store)
	runCtx, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- host.Run(runCtx, nil) }()

	first := startMemoryLogin(t, host, playerIdentity(5))
	waitReady(t, host, first)
	host.mu.Lock()
	firstSession := host.active.Session
	host.mu.Unlock()
	tickBefore := host.world.TickCount()
	if err := first.Client.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-first.Done:
	case <-time.After(time.Second):
		t.Fatal("AcceptStream did not return after disconnect")
	}
	waitForNoActiveLogin(t, host)
	waitForPlayerSave(t, store)
	waitForTickAfter(t, host, tickBefore)

	second := startMemoryLogin(t, host, playerIdentity(5))
	waitReady(t, host, second)
	host.mu.Lock()
	secondSession := host.active.Session
	host.mu.Unlock()
	if secondSession <= firstSession {
		t.Fatalf("second session ID = %d, want > %d", secondSession, firstSession)
	}
	_ = second.Client.Close()
	select {
	case <-second.Done:
	case <-time.After(time.Second):
		t.Fatal("second AcceptStream did not return")
	}
	cancelRun()
	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after cancellation")
	}
	shutdownHostComponentsForTest(t, host)
}

func TestHostFailedDisconnectSaveAllowsSameIdentityOnly(t *testing.T) {
	store := newHostTestStore()
	saveErr := errors.New("disk unavailable")
	store.setSaveError(saveErr)
	host := newTestHostWithStore(t, store)
	runCtx, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- host.Run(runCtx, nil) }()

	identity := playerIdentity(12)
	first := startMemoryLogin(t, host, identity)
	waitReady(t, host, first)
	_ = first.Client.Close()
	select {
	case <-first.Done:
	case <-time.After(time.Second):
		t.Fatal("first disconnect did not finish")
	}
	waitForPlayerSave(t, store)

	second := startMemoryLogin(t, host, identity)
	waitReady(t, host, second)
	_ = second.Client.Close()
	select {
	case <-second.Done:
	case <-time.After(time.Second):
		t.Fatal("same-identity reconnect did not finish")
	}
	_, err := attemptMemoryLogin(host, playerIdentity(13))
	var remote *network.RemoteError
	if !errors.As(err, &remote) ||
		network.LoginRejectCode(remote.Code) != network.LoginStoreUnavailable {
		t.Fatalf("different-identity login error = %v, want store unavailable", err)
	}

	store.setSaveError(nil)
	cancelRun()
	select {
	case err := <-runDone:
		if !errors.Is(err, saveErr) {
			t.Fatalf("first Run cleanup error = %v, want retryable save error", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run cleanup timed out")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := host.Shutdown(ctx); err != nil {
		t.Fatalf("retry Shutdown after healed store: %v", err)
	}
}

func TestHostAutosavesActivePlayer(t *testing.T) {
	store := newHostTestStore()
	config := hostTestConfig()
	config.AutosaveTicks = 1
	host := NewHost(config, flatTestGenerator{}, store)
	runCtx, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- host.Run(runCtx, nil) }()
	login := startMemoryLogin(t, host, playerIdentity(6))
	waitReady(t, host, login)

	deadline := time.Now().Add(500 * time.Millisecond)
	for store.saveCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := store.saveCount(); got == 0 {
		t.Fatal("active player was not autosaved")
	}

	_ = login.Client.Close()
	select {
	case <-login.Done:
	case <-time.After(time.Second):
		t.Fatal("AcceptStream did not return")
	}
	cancelRun()
	<-runDone
	shutdownHostComponentsForTest(t, host)
}

func TestHostListenerContinuesAfterBadConnection(t *testing.T) {
	store := newHostTestStore()
	host := newTestHostWithStore(t, store)
	listener := newHostTestListener()
	runCtx, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- host.Run(runCtx, listener) }()

	badClient, badServer := network.NewMemoryStreamPair(1)
	_ = badClient.Close()
	listener.streams <- badServer
	client, server := network.NewMemoryStreamPair(256)
	listener.streams <- server
	loginCtx, cancelLogin := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelLogin()
	endpoint, err := network.LoginClient(loginCtx, client, playerIdentity(7))
	if err != nil {
		t.Fatalf("valid login after bad connection: %v", err)
	}
	_ = endpoint.Close()
	cancelRun()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run after cancellation = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not shut down")
	}
}

func TestHostRunCancellationCompletesOwnedShutdown(t *testing.T) {
	store := newHostTestStore()
	host := newTestHostWithStore(t, store)
	runCtx, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- host.Run(runCtx, nil) }()
	cancelRun()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run cancellation error = %v, want nil after complete shutdown", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return")
	}
	if store.syncCount() != 1 || store.closeCount() != 1 {
		t.Fatalf("store shutdown counts = sync %d close %d, want 1/1", store.syncCount(), store.closeCount())
	}
}

func TestHostShutdownRetriesPlayerFlushBeforeWorldClose(t *testing.T) {
	saveErr := errors.New("player save failed")
	store := newHostTestStore()
	store.setSaveError(saveErr)
	host := newTestHostWithStore(t, store)
	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	runDone := make(chan error, 1)
	go func() { runDone <- host.Run(runCtx, nil) }()
	login := startMemoryLogin(t, host, playerIdentity(8))
	waitReady(t, host, login)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := host.Shutdown(ctx); !errors.Is(err, saveErr) {
		t.Fatalf("first Shutdown error = %v, want %v", err, saveErr)
	}
	if store.syncCount() != 0 || store.closeCount() != 0 {
		t.Fatalf("world store closed after failed player flush: sync=%d close=%d", store.syncCount(), store.closeCount())
	}
	tick := host.world.TickCount()
	waitForTickAfter(t, host, tick)

	store.setSaveError(nil)
	if err := host.Shutdown(ctx); err != nil {
		t.Fatalf("retry Shutdown: %v", err)
	}
	if store.syncCount() != 1 || store.closeCount() != 1 {
		t.Fatalf("world store shutdown counts = sync %d close %d, want 1/1", store.syncCount(), store.closeCount())
	}
	events := store.eventsSnapshot()
	if len(events) < 4 || events[len(events)-2] != "sync" || events[len(events)-1] != "close" {
		t.Fatalf("persistence order = %v, want saves followed by sync/close", events)
	}
	for _, event := range events[:len(events)-2] {
		if event != "save" {
			t.Fatalf("persistence order = %v, world persistence preceded player saves", events)
		}
	}
	select {
	case <-login.Done:
	case <-time.After(time.Second):
		t.Fatal("login worker did not exit during shutdown")
	}
	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("Run did not exit during shutdown")
	}
}

func newTestHost(t *testing.T) *Host {
	t.Helper()
	return newTestHostWithStore(t, newHostTestStore())
}

func newTestHostWithStore(t *testing.T, store storage.WorldStore) *Host {
	t.Helper()
	host := NewHost(hostTestConfig(), flatTestGenerator{}, store)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := host.Shutdown(ctx); err != nil {
			t.Errorf("Host cleanup Shutdown: %v", err)
		}
	})
	return host
}

func hostTestConfig() Config {
	config := DefaultConfig(42)
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
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		host.mu.Lock()
		active := host.active
		host.mu.Unlock()
		if active != nil && active.PlayerID == login.Identity.PlayerID {
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
	deadline := time.Now().Add(time.Second)
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
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		host.mu.Lock()
		active := host.active
		host.mu.Unlock()
		if active == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("active login was not released")
}

func waitForPlayerSave(t *testing.T, store *hostTestStore) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for store.saveCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if store.saveCount() == 0 {
		t.Fatal("player save did not start")
	}
}

func waitForTickAfter(t *testing.T, host *Host, tick uint64) {
	t.Helper()
	deadline := time.Now().Add(200 * time.Millisecond)
	for host.world.TickCount() <= tick && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := host.world.TickCount(); got <= tick {
		t.Fatalf("world tick = %d after disconnect, want > %d", got, tick)
	}
}

func shutdownHostComponentsForTest(t *testing.T, host *Host) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
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
		FormatVersion:  1,
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
	case <-time.After(time.Second):
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
