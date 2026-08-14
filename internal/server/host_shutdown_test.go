package server

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/storage"
)

// 捕获：Shutdown 在 accept-loop 仍可能 Add 时等待共享 WaitGroup，或遗留未读完握手的 stream。
func TestHostShutdownPendingLoginClosesUnreadHandshakeAndWaitsAcceptLoop(t *testing.T) {
	store := newHostTestStore()
	host := mustNewHost(t, hostTestConfig(), flatTestGenerator{}, store)
	listener := newHostTestListener()
	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	runDone := make(chan error, 1)
	go func() { runDone <- host.Run(runCtx, listener) }()
	client, server := network.NewMemoryStreamPair(8)
	listener.streams <- server
	waitForPreLoginCount(t, host, 1)

	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
	defer cancel()
	if err := host.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown error=%v", err)
	}
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run error=%v", err)
		}
	case <-time.After(waitDeadline):
		t.Fatal("Run did not wait for accept-loop shutdown")
	}
	host.mu.Lock()
	pending := len(host.preLoginStreams)
	host.mu.Unlock()
	if pending != 0 || len(host.preLogin) != 0 {
		t.Fatalf("pending lifecycle: streams=%d tokens=%d, want 0/0", pending, len(host.preLogin))
	}
	recvCtx, recvCancel := context.WithTimeout(context.Background(), waitDeadline)
	defer recvCancel()
	if _, err := client.Recv(recvCtx, network.StateHandshake); !errors.Is(err, network.ErrClosed) {
		t.Fatalf("unread handshake Recv error=%v, want network.ErrClosed", err)
	}
}

// 捕获：关闭 stream 没有取消 PendingLogin.Context，导致 reservation 阻塞在 LoadPlayer。
func TestHostShutdownPendingLoginCancelsBlockedReservationLoad(t *testing.T) {
	store := newHostTestStore()
	store.blockLoads()
	host := mustNewHost(t, hostTestConfig(), flatTestGenerator{}, store)
	runCtx, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- host.Run(runCtx, nil) }()
	client, server := network.NewMemoryStreamPair(8)
	serverDone := make(chan error, 1)
	go func() { serverDone <- host.AcceptStream(context.Background(), server) }()
	clientDone := make(chan error, 1)
	go func() {
		_, err := network.LoginClient(context.Background(), client, playerIdentity(1))
		clientDone <- err
	}()
	store.waitLoadStarted(t)

	cleanup := func() {
		store.releaseLoads()
		store.setSaveError(nil)
		ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
		_ = host.Shutdown(ctx)
		cancel()
		cancelRun()
	}
	t.Cleanup(cleanup)

	shutdownDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
		defer cancel()
		shutdownDone <- host.Shutdown(ctx)
	}()
	waitForHostClosing(t, host)
	_, rejectedServer := network.NewMemoryStreamPair(1)
	if err := host.AcceptStream(context.Background(), rejectedServer); !errors.Is(err, network.ErrClosed) {
		t.Fatalf("post-closing AcceptStream error=%v, want network.ErrClosed", err)
	}
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("Shutdown error=%v", err)
		}
	case <-time.After(waitDeadline):
		t.Fatal("Shutdown did not cancel blocked reservation LoadPlayer")
	}
	select {
	case <-serverDone:
	case <-time.After(waitDeadline):
		t.Fatal("pending server handler did not exit")
	}
	select {
	case <-clientDone:
	case <-time.After(waitDeadline):
		t.Fatal("pending client login did not exit")
	}
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run error=%v", err)
		}
	case <-time.After(waitDeadline):
		t.Fatal("Run did not exit")
	}
	assertHostIndexesEmpty(t, host)
}

// 捕获：多 session 关闭未等待最终 SessionExit/force Observe，或首个玩家 Flush 失败后提前关闭 world/workers。
func TestHostShutdownMultiplayerFlushFailurePreservesRuntimeAndRetries(t *testing.T) {
	baseline := runtime.NumGoroutine()
	oneErr, threeErr := errors.New("player one failed"), errors.New("player three failed")
	store := newStrictShutdownStore()
	config := hostTestConfig()
	config.MaxPlayers = 3
	host := mustNewHost(t, config, flatTestGenerator{}, store)
	runCtx, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- host.Run(runCtx, nil) }()
	store.setFailures(map[core.PlayerID]error{
		playerID(1): oneErr,
		playerID(3): threeErr,
	})
	logins := make([]testLogin, 0, 3)
	wantSnapshots := make(map[core.PlayerID]storage.PlayerSave, 3)
	for number := byte(1); number <= 3; number++ {
		login := startMemoryLogin(t, host, playerIdentity(number))
		waitReady(t, host, login)
		wantSnapshots[login.Identity.PlayerID] = setShutdownPlayerRotation(
			t,
			host,
			login,
			uint64(100+number),
			float32(number)*15,
			-float32(number)*5,
		)
		logins = append(logins, login)
	}
	host.mu.Lock()
	pendingStreams := len(host.preLoginStreams)
	host.mu.Unlock()
	if pendingStreams != 0 || len(host.preLogin) != 0 {
		t.Fatalf("promoted Play sessions retained pending lifecycle: streams=%d tokens=%d",
			pendingStreams, len(host.preLogin))
	}
	t.Cleanup(func() {
		store.setFailures(nil)
		ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
		_ = host.Shutdown(ctx)
		cancel()
		cancelRun()
	})

	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
	err := host.Shutdown(ctx)
	cancel()
	if !errors.Is(err, oneErr) || !errors.Is(err, threeErr) {
		t.Fatalf("first Shutdown error=%v, want both player roots", err)
	}
	store.assertAttemptedPlayerIDs(t, playerID(1), playerID(2), playerID(3))
	if store.syncCount() != 0 || store.closeCount() != 0 {
		t.Fatalf("failed Flush closed world: sync=%d close=%d", store.syncCount(), store.closeCount())
	}
	select {
	case <-host.players.done:
		t.Fatal("failed Flush closed player workers")
	default:
	}
	assertHostIndexesEmpty(t, host)
	for _, login := range logins {
		waitLoginDone(t, login.Done)
	}
	tick := host.world.TickCount()
	waitForTickAfter(t, host, tick)

	store.setFailures(nil)
	ctx, cancel = context.WithTimeout(context.Background(), waitDeadline)
	if err := host.Shutdown(ctx); err != nil {
		cancel()
		t.Fatalf("retry Shutdown error=%v", err)
	}
	cancel()
	if store.syncCount() != 1 || store.closeCount() != 1 {
		t.Fatalf("successful retry lifecycle: sync=%d close=%d, want 1/1", store.syncCount(), store.closeCount())
	}
	for id, want := range wantSnapshots {
		got, ok := store.latestSavedPlayer(id)
		if !ok || got.Current != want.Current || got.Yaw != want.Yaw || got.Pitch != want.Pitch {
			t.Fatalf("final saved player %s=%+v found=%t, want snapshot %+v", id, got, ok, want)
		}
	}
	select {
	case <-host.players.done:
	default:
		t.Fatal("successful Shutdown returned before player workers exited")
	}
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run error=%v", err)
		}
	case <-time.After(waitDeadline):
		t.Fatal("Run did not exit after successful Shutdown")
	}
	assertHostIndexesEmpty(t, host)
	attemptsBefore, postCloseBefore := store.attemptSnapshot()
	time.Sleep(75 * time.Millisecond)
	attemptsAfter, postCloseAfter := store.attemptSnapshot()
	if !equalPlayerSaveAttempts(attemptsBefore, attemptsAfter) || postCloseBefore != 0 || postCloseAfter != 0 {
		t.Fatalf("Store called after shutdown: before=%v/%d after=%v/%d",
			attemptsBefore, postCloseBefore, attemptsAfter, postCloseAfter)
	}
	waitForGoroutineCeiling(t, baseline+2)
}

// 捕获：world Sync/Close 失败分支错误关闭 player jobs，使二次 Shutdown 无法先重试完整生命周期。
func TestHostShutdownWorldFailureKeepsPlayerWorkersRetryable(t *testing.T) {
	tests := []struct {
		name            string
		inject          func(*strictShutdownStore, error)
		clear           func(*strictShutdownStore)
		firstSyncs      int
		firstCloses     int
		successfulSyncs int
		successfulClose int
	}{
		{
			name: "sync",
			inject: func(store *strictShutdownStore, err error) {
				store.setSyncError(err)
			},
			clear: func(store *strictShutdownStore) {
				store.setSyncError(nil)
			},
			firstSyncs:      1,
			firstCloses:     0,
			successfulSyncs: 2,
			successfulClose: 1,
		},
		{
			name: "close",
			inject: func(store *strictShutdownStore, err error) {
				store.setCloseError(err)
			},
			clear: func(store *strictShutdownStore) {
				store.setCloseError(nil)
			},
			firstSyncs:      1,
			firstCloses:     1,
			successfulSyncs: 1,
			successfulClose: 2,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			baseline := runtime.NumGoroutine()
			wantErr := errors.New(test.name + " world failure")
			store := newStrictShutdownStore()
			host := mustNewHost(t, hostTestConfig(), flatTestGenerator{}, store)
			runCtx, cancelRun := context.WithCancel(context.Background())
			runDone := make(chan error, 1)
			go func() { runDone <- host.Run(runCtx, nil) }()
			login := startMemoryLogin(t, host, playerIdentity(6))
			waitReady(t, host, login)
			test.inject(store, wantErr)
			t.Cleanup(func() {
				store.setSyncError(nil)
				store.setCloseError(nil)
				ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
				_ = host.Shutdown(ctx)
				cancel()
				cancelRun()
			})

			ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
			err := host.Shutdown(ctx)
			cancel()
			if !errors.Is(err, wantErr) {
				t.Fatalf("first Shutdown error=%v, want %v", err, wantErr)
			}
			if store.syncCount() != test.firstSyncs || store.closeCount() != test.firstCloses {
				t.Fatalf("first lifecycle sync=%d close=%d, want %d/%d",
					store.syncCount(), store.closeCount(), test.firstSyncs, test.firstCloses)
			}
			if store.isClosed() {
				t.Fatal("failed world shutdown released Store ownership")
			}
			select {
			case <-host.players.done:
				t.Fatal("world failure closed player workers")
			default:
			}
			host.players.scheduler.submitMu.RLock()
			jobsClosed := host.players.scheduler.closed
			host.players.scheduler.submitMu.RUnlock()
			if jobsClosed {
				t.Fatal("world failure closed player save jobs")
			}
			waitLoginDone(t, login.Done)
			select {
			case err := <-runDone:
				if err != nil {
					t.Fatalf("Run error=%v", err)
				}
			case <-time.After(waitDeadline):
				t.Fatal("Run did not stop after world entered retryable closing state")
			}
			assertHostLifecycleEmpty(t, host)

			if err := host.players.Observe(
				login.Identity.PlayerID,
				login.Identity.DisplayName,
				testPlayerSnapshot(20),
				host.world.TickCount(),
				true,
			); err != nil {
				t.Fatalf("Observe after world failure: %v", err)
			}
			ctx, cancel = context.WithTimeout(context.Background(), waitDeadline)
			if err := host.players.Flush(ctx); err != nil {
				cancel()
				t.Fatalf("player workers unusable after world failure: %v", err)
			}
			cancel()
			latest, ok := store.latestSavedPlayer(login.Identity.PlayerID)
			if !ok || latest.Current.Position != [3]float32{20, 70, -20} {
				t.Fatalf("post-failure player save=%+v found=%t", latest, ok)
			}

			test.clear(store)
			ctx, cancel = context.WithTimeout(context.Background(), waitDeadline)
			if err := host.Shutdown(ctx); err != nil {
				cancel()
				t.Fatalf("retry Shutdown error=%v", err)
			}
			cancel()
			if store.syncCount() != test.successfulSyncs || store.closeCount() != test.successfulClose {
				t.Fatalf("successful lifecycle sync=%d close=%d, want %d/%d",
					store.syncCount(), store.closeCount(), test.successfulSyncs, test.successfulClose)
			}
			select {
			case <-host.players.done:
			default:
				t.Fatal("successful retry did not close player workers")
			}
			assertHostLifecycleEmpty(t, host)
			attemptsBefore, postCloseBefore := store.attemptSnapshot()
			syncBefore, closeBefore := store.syncCount(), store.closeCount()
			runtime.Gosched()
			attemptsAfter, postCloseAfter := store.attemptSnapshot()
			if !equalPlayerSaveAttempts(attemptsBefore, attemptsAfter) ||
				postCloseBefore != 0 || postCloseAfter != 0 ||
				store.syncCount() != syncBefore || store.closeCount() != closeBefore {
				t.Fatalf("Store called after shutdown: saves=%v/%d -> %v/%d lifecycle=%d/%d -> %d/%d",
					attemptsBefore, postCloseBefore, attemptsAfter, postCloseAfter,
					syncBefore, closeBefore, store.syncCount(), store.closeCount())
			}
			waitForGoroutineCeiling(t, baseline+2)
		})
	}
}

func waitForHostClosing(t *testing.T, host *Host) {
	t.Helper()
	deadline := time.Now().Add(waitDeadline)
	for time.Now().Before(deadline) {
		host.mu.Lock()
		closing := host.closing
		host.mu.Unlock()
		if closing {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("Host did not enter closing state")
}

func assertHostIndexesEmpty(t *testing.T, host *Host) {
	t.Helper()
	host.mu.Lock()
	players, sessions := len(host.activeByPlayer), len(host.activeBySession)
	host.mu.Unlock()
	if players != 0 || sessions != 0 {
		t.Fatalf("Host indexes=%d/%d, want 0/0", players, sessions)
	}
}

func assertHostLifecycleEmpty(t *testing.T, host *Host) {
	t.Helper()
	assertHostIndexesEmpty(t, host)
	host.mu.Lock()
	pending := len(host.preLoginStreams)
	host.mu.Unlock()
	if pending != 0 || len(host.preLogin) != 0 {
		t.Fatalf("Host pending lifecycle=%d/%d, want 0/0", pending, len(host.preLogin))
	}
}

func waitForGoroutineCeiling(t *testing.T, ceiling int) {
	t.Helper()
	deadline := time.Now().Add(waitDeadline)
	for time.Now().Before(deadline) {
		runtime.GC()
		if runtime.NumGoroutine() <= ceiling {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("goroutines=%d, want <=%d", runtime.NumGoroutine(), ceiling)
}

func setShutdownPlayerRotation(
	t *testing.T,
	host *Host,
	login testLogin,
	sequence uint64,
	yaw float32,
	pitch float32,
) storage.PlayerSave {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
	defer cancel()
	if err := login.Client.Send(ctx, network.PlayerInput{
		Sequence: sequence,
		Yaw:      yaw,
		Pitch:    pitch,
	}); err != nil {
		t.Fatalf("send final rotation for %s: %v", login.Identity.PlayerID, err)
	}
	for {
		message, err := login.Client.Recv(ctx)
		if err != nil {
			t.Fatalf("recv final rotation for %s: %v", login.Identity.PlayerID, err)
		}
		state, ok := message.(network.PlayerState)
		if !ok || state.LastInputSequence < sequence {
			continue
		}
		active := activeLoginForPlayer(t, host, login.Identity.PlayerID)
		snapshot, ok := host.world.PlayerSnapshotFor(active.Session)
		if !ok {
			t.Fatalf("snapshot for %s not found", login.Identity.PlayerID)
		}
		return storage.PlayerSave{
			PlayerID:    login.Identity.PlayerID,
			Revision:    1,
			DisplayName: login.Identity.DisplayName,
			Current: storage.PlayerLocation{
				Dimension: snapshot.Current.Dimension,
				Position:  [3]float32(snapshot.Current.Position),
			},
			Yaw:   snapshot.Yaw,
			Pitch: snapshot.Pitch,
		}
	}
}

type strictShutdownStore struct {
	*hostTestStore
	mu            sync.Mutex
	failures      map[core.PlayerID]error
	attempts      map[playerSaveKey]int
	saves         map[playerSaveKey]storage.PlayerSave
	postCloseSave int
	syncErr       error
	closeErr      error
	closed        bool
}

func newStrictShutdownStore() *strictShutdownStore {
	return &strictShutdownStore{
		hostTestStore: newHostTestStore(),
		failures:      make(map[core.PlayerID]error),
		attempts:      make(map[playerSaveKey]int),
		saves:         make(map[playerSaveKey]storage.PlayerSave),
	}
}

func (store *strictShutdownStore) SavePlayer(
	ctx context.Context,
	save storage.PlayerSave,
) (uint64, error) {
	store.mu.Lock()
	if store.closed {
		store.postCloseSave++
	}
	key := playerSaveKey{playerID: save.PlayerID, revision: save.Revision}
	store.attempts[key]++
	store.saves[key] = save
	err := store.failures[save.PlayerID]
	store.mu.Unlock()
	if err != nil {
		return 0, err
	}
	return store.MemoryStore.SavePlayer(ctx, save)
}

func (store *strictShutdownStore) Sync(ctx context.Context) error {
	store.mu.Lock()
	err := store.syncErr
	store.mu.Unlock()
	if err == nil {
		return store.hostTestStore.Sync(ctx)
	}
	store.hostTestStore.mu.Lock()
	store.hostTestStore.syncs++
	store.hostTestStore.events = append(store.hostTestStore.events, "sync")
	store.hostTestStore.mu.Unlock()
	return err
}

func (store *strictShutdownStore) Close() error {
	store.mu.Lock()
	err := store.closeErr
	if err != nil {
		store.mu.Unlock()
		store.hostTestStore.mu.Lock()
		store.hostTestStore.closes++
		store.hostTestStore.events = append(store.hostTestStore.events, "close")
		store.hostTestStore.mu.Unlock()
		return err
	}
	store.closed = true
	store.mu.Unlock()
	return store.hostTestStore.Close()
}

func (store *strictShutdownStore) setSyncError(err error) {
	store.mu.Lock()
	store.syncErr = err
	store.mu.Unlock()
}

func (store *strictShutdownStore) setCloseError(err error) {
	store.mu.Lock()
	store.closeErr = err
	store.mu.Unlock()
}

func (store *strictShutdownStore) isClosed() bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.closed
}

func (store *strictShutdownStore) setFailures(failures map[core.PlayerID]error) {
	store.mu.Lock()
	store.failures = make(map[core.PlayerID]error, len(failures))
	for id, err := range failures {
		store.failures[id] = err
	}
	store.mu.Unlock()
}

func (store *strictShutdownStore) assertAttemptedPlayerIDs(t *testing.T, want ...core.PlayerID) {
	t.Helper()
	got, _ := store.attemptSnapshot()
	wantIDs := make(map[core.PlayerID]struct{}, len(want))
	for _, id := range want {
		wantIDs[id] = struct{}{}
	}
	seen := make(map[core.PlayerID]struct{}, len(got))
	for key, count := range got {
		if key.revision != 1 || count < 1 {
			t.Fatalf("SavePlayer attempts=%v, want positive revision-1 attempts", got)
		}
		seen[key.playerID] = struct{}{}
	}
	if len(seen) != len(wantIDs) {
		t.Fatalf("SavePlayer IDs=%v, want %v", seen, wantIDs)
	}
	for id := range wantIDs {
		if _, ok := seen[id]; !ok {
			t.Fatalf("SavePlayer IDs=%v, missing %s", seen, id)
		}
	}
}

func (store *strictShutdownStore) attemptSnapshot() (map[playerSaveKey]int, int) {
	store.mu.Lock()
	defer store.mu.Unlock()
	attempts := make(map[playerSaveKey]int, len(store.attempts))
	for key, count := range store.attempts {
		attempts[key] = count
	}
	return attempts, store.postCloseSave
}

func (store *strictShutdownStore) latestSavedPlayer(id core.PlayerID) (storage.PlayerSave, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	var latest storage.PlayerSave
	found := false
	for key, save := range store.saves {
		if key.playerID == id && (!found || key.revision > latest.Revision) {
			latest = save
			found = true
		}
	}
	return latest, found
}

func equalPlayerSaveAttempts(left, right map[playerSaveKey]int) bool {
	if len(left) != len(right) {
		return false
	}
	for key, count := range left {
		if right[key] != count {
			return false
		}
	}
	return true
}
