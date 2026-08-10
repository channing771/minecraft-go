package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"minecraft-go/internal/network"
)

func TestHostCleanupUsesEntryIdentity(t *testing.T) {
	config := hostTestConfig()
	config.MaxPlayers = 8
	host := NewHost(config, flatTestGenerator{}, newHostTestStore())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
		defer cancel()
		if err := host.Shutdown(ctx); err != nil {
			t.Errorf("Host cleanup Shutdown: %v", err)
		}
	})
	id := playerIdentity(31).PlayerID
	old, err := host.reserveLogin(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.promoteLogin(old, 1, 1); err != nil {
		t.Fatal(err)
	}
	host.releaseLogin(old)
	successor, err := host.reserveLogin(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.promoteLogin(successor, 2, 2); err != nil {
		t.Fatal(err)
	}
	host.releaseLogin(old)
	host.mu.Lock()
	byPlayer := host.activeByPlayer[id]
	bySession := host.activeBySession[2]
	host.mu.Unlock()
	if byPlayer != successor || bySession != successor {
		t.Fatalf("delayed cleanup removed successor: player=%p session=%p want=%p", byPlayer, bySession, successor)
	}
}

func TestHostMalformedSessionCleanupIsIsolated(t *testing.T) {
	config := hostTestConfig()
	config.MaxPlayers = 8
	host, stop := startHostWithConfig(t, config, newHostTestStore())
	defer stop()
	healthy := loginHealthyMemoryPlayers(t, host, 7, 200)

	identity := playerIdentity(8)
	clientStream, serverStream := network.NewMemoryStreamPair(32)
	done := make(chan error, 1)
	go func() {
		done <- host.AcceptStream(context.Background(), &playErrorServerStream{
			ServerPacketStream: serverStream,
			err:                errors.New("malformed play packet"),
		})
	}()
	client, err := network.LoginClient(context.Background(), clientStream, identity)
	if err != nil {
		t.Fatal(err)
	}
	_ = client.Close()
	waitLoginDone(t, done)
	waitForPlayerReleased(t, host, identity.PlayerID)
	assertHealthyHostProgress(t, host, healthy)
}

func TestHostHeartbeatTimeoutCleanupIsIsolated(t *testing.T) {
	config := hostTestConfig()
	config.MaxPlayers = 8
	config.HeartbeatInterval = 20 * time.Millisecond
	config.HeartbeatTimeout = 150 * time.Millisecond
	host, stop := startHostWithConfig(t, config, newHostTestStore())
	defer stop()
	healthy := loginHealthyMemoryPlayers(t, host, 7, 300)

	timedOut := startMemoryLogin(t, host, playerIdentity(8))
	waitForPlayerReleased(t, host, timedOut.Identity.PlayerID)
	waitLoginDone(t, timedOut.Done)
	assertHealthyHostProgress(t, host, healthy)
}

func TestHostSlowClientCleanupIsIsolated(t *testing.T) {
	config := hostTestConfig()
	config.MaxPlayers = 8
	config.OutboxCapacity = 4
	host, stop := startHostWithConfig(t, config, newHostTestStore())
	defer stop()
	healthy := loginHealthyMemoryPlayers(t, host, 7, 400)

	slow := startMemoryLoginWithCapacity(t, host, playerIdentity(8), 1)
	waitReady(t, host, slow)
	waitForPlayerReleased(t, host, slow.Identity.PlayerID)
	waitLoginDone(t, slow.Done)
	assertHealthyHostProgress(t, host, healthy)
}

func TestHostDisconnectPersistsReleasesSlotAndKeepsTicking(t *testing.T) {
	store := newHostTestStore()
	host := newTestHostWithStore(t, store)
	runCtx, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- host.Run(runCtx, nil) }()

	first := startMemoryLogin(t, host, playerIdentity(5))
	waitReady(t, host, first)
	firstSession := activeLoginForPlayer(t, host, first.Identity.PlayerID).Session
	tickBefore := host.world.TickCount()
	if err := first.Client.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-first.Done:
	case <-time.After(waitDeadline):
		t.Fatal("AcceptStream did not return after disconnect")
	}
	waitForNoActiveLogin(t, host)
	waitForPlayerSave(t, store)
	waitForTickAfter(t, host, tickBefore)

	second := startMemoryLogin(t, host, playerIdentity(5))
	waitReady(t, host, second)
	secondSession := activeLoginForPlayer(t, host, second.Identity.PlayerID).Session
	if secondSession <= firstSession {
		t.Fatalf("second session ID = %d, want > %d", secondSession, firstSession)
	}
	_ = second.Client.Close()
	select {
	case <-second.Done:
	case <-time.After(waitDeadline):
		t.Fatal("second AcceptStream did not return")
	}
	cancelRun()
	select {
	case <-runDone:
	case <-time.After(waitDeadline):
		t.Fatal("Run did not return after cancellation")
	}
	shutdownHostComponentsForTest(t, host)
}

// 捕获：Host 在已确认 session 退出并完成最后一次 Observe 后遗漏 Deactivate，使 cache 永久保留 active。
func TestHostDisconnectDeactivatesCachedPlayer(t *testing.T) {
	store := newHostTestStore()
	host := newTestHostWithStore(t, store)
	runCtx, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- host.Run(runCtx, nil) }()

	identity := playerIdentity(16)
	login := startMemoryLogin(t, host, identity)
	waitReady(t, host, login)
	if err := login.Client.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-login.Done:
	case <-time.After(waitDeadline):
		t.Fatal("AcceptStream did not return after disconnect")
	}

	host.players.mu.Lock()
	cached := host.players.cache[identity.PlayerID]
	active := cached != nil && cached.active
	host.players.mu.Unlock()
	if active {
		t.Fatal("disconnected player remained active in persistence cache")
	}

	cancelRun()
	select {
	case <-runDone:
	case <-time.After(waitDeadline):
		t.Fatal("Run did not return after cancellation")
	}
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
	case <-time.After(waitDeadline):
		t.Fatal("first disconnect did not finish")
	}
	waitForPlayerSave(t, store)

	second := startMemoryLogin(t, host, identity)
	waitReady(t, host, second)
	_ = second.Client.Close()
	select {
	case <-second.Done:
	case <-time.After(waitDeadline):
		t.Fatal("same-identity reconnect did not finish")
	}
	different := startMemoryLogin(t, host, playerIdentity(13))
	waitReady(t, host, different)
	_ = different.Client.Close()
	select {
	case <-different.Done:
	case <-time.After(waitDeadline):
		t.Fatal("different-identity login was blocked by another player's retry")
	}

	cancelRun()
	select {
	case err := <-runDone:
		if !errors.Is(err, saveErr) {
			t.Fatalf("first Run cleanup error = %v, want retryable save error", err)
		}
	case <-time.After(waitDeadline):
		t.Fatal("Run cleanup timed out")
	}
	store.setSaveError(nil)
	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
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

	deadline := time.Now().Add(shortWaitDeadline)
	for store.saveCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := store.saveCount(); got == 0 {
		t.Fatal("active player was not autosaved")
	}

	_ = login.Client.Close()
	select {
	case <-login.Done:
	case <-time.After(waitDeadline):
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
	loginCtx, cancelLogin := context.WithTimeout(context.Background(), waitDeadline)
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
	case <-time.After(waitDeadline):
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
	case <-time.After(waitDeadline):
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

	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
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
	case <-time.After(waitDeadline):
		t.Fatal("login worker did not exit during shutdown")
	}
	select {
	case <-runDone:
	case <-time.After(waitDeadline):
		t.Fatal("Run did not exit during shutdown")
	}
}
