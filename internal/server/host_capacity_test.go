package server

import (
	"context"
	"errors"
	"io"
	"math"
	"runtime"
	"testing"
	"time"

	"minecraft-go/internal/network"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/storage"
)

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

func TestHostAllowsEightPlayers(t *testing.T) {
	host, stop := startMultiHost(t, newHostTestStore())
	defer stop()
	logins := make([]testLogin, 0, 8)
	for number := byte(1); number <= 8; number++ {
		login := startMemoryLogin(t, host, playerIdentity(number))
		logins = append(logins, login)
	}
	for index, login := range logins {
		waitReady(t, host, login)
		entry := activeLoginForPlayer(t, host, login.Identity.PlayerID)
		if want := sim.SessionID(index + 1); entry.Session != want {
			t.Fatalf("player %d session = %d, want %d", index+1, entry.Session, want)
		}
	}
	host.mu.Lock()
	players, sessions := len(host.activeByPlayer), len(host.activeBySession)
	host.mu.Unlock()
	if players != 8 || sessions != 8 {
		t.Fatalf("active indexes = players %d sessions %d, want 8/8", players, sessions)
	}
}

func TestHostRejectsNinthPlayer(t *testing.T) {
	store := newHostTestStore()
	host, stop := startMultiHost(t, store)
	defer stop()
	logins := loginEightMemoryPlayers(t, host)
	_, err := attemptMemoryLogin(host, playerIdentity(9))
	assertLoginRejectCode(t, err, network.LoginServerFull)
	if got := store.loadCount(); got != 8 {
		t.Fatalf("LoadPlayer calls after full reject = %d, want 8", got)
	}
	assertLoginCanAdvance(t, logins[0], 101)
}

func TestHostRejectsDuplicatePlayerBeforeLoad(t *testing.T) {
	store := newHostTestStore()
	host, stop := startMultiHost(t, store)
	defer stop()
	logins := loginEightMemoryPlayers(t, host)
	_, err := attemptMemoryLogin(host, logins[3].Identity)
	assertLoginRejectCode(t, err, network.LoginAlreadyOnline)
	if got := store.loadCount(); got != 8 {
		t.Fatalf("duplicate called LoadPlayer: calls=%d, want 8", got)
	}
	assertLoginCanAdvance(t, logins[3], 102)
}

func TestHostAllowsDuplicateDisplayName(t *testing.T) {
	host, stop := startMultiHost(t, newHostTestStore())
	defer stop()
	firstIdentity := playerIdentity(21)
	secondIdentity := playerIdentity(22)
	secondIdentity.DisplayName = firstIdentity.DisplayName
	first := startMemoryLogin(t, host, firstIdentity)
	second := startMemoryLogin(t, host, secondIdentity)
	waitReady(t, host, first)
	waitReady(t, host, second)
	firstEntry := activeLoginForPlayer(t, host, firstIdentity.PlayerID)
	secondEntry := activeLoginForPlayer(t, host, secondIdentity.PlayerID)
	if firstEntry.Name != secondEntry.Name || firstEntry.Session == secondEntry.Session {
		t.Fatalf("same-name entries = %+v and %+v", firstEntry, secondEntry)
	}
}

func TestHostMiddleDisconnectFreesCapacityWithoutReusingSessionID(t *testing.T) {
	host, stop := startMultiHost(t, newHostTestStore())
	defer stop()
	logins := loginEightMemoryPlayers(t, host)
	middle := logins[3]
	oldSession := activeLoginForPlayer(t, host, middle.Identity.PlayerID).Session
	if err := middle.Client.Close(); err != nil {
		t.Fatal(err)
	}
	waitLoginDone(t, middle.Done)
	waitForPlayerReleased(t, host, middle.Identity.PlayerID)
	replacement := startMemoryLogin(t, host, playerIdentity(9))
	waitReady(t, host, replacement)
	newSession := activeLoginForPlayer(t, host, replacement.Identity.PlayerID).Session
	if newSession != 9 || newSession <= oldSession {
		t.Fatalf("replacement session = %d, want 9 and > %d", newSession, oldSession)
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
			case <-time.After(waitDeadline):
				t.Error("pre-login worker did not exit")
			}
		}
	})
	waitForPreLoginCount(t, host, hostPreLoginCapacity)

	seventeenth, server := network.NewMemoryStreamPair(1)
	result := make(chan error, 1)
	go func() { result <- host.AcceptStream(context.Background(), server) }()
	ctx, cancel := context.WithTimeout(context.Background(), shortWaitDeadline)
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
	case <-time.After(shortWaitDeadline):
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
	case <-time.After(waitDeadline):
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
	case <-time.After(waitDeadline):
		t.Fatal("Run cleanup timed out")
	}
}

func TestLoginDeadlineCancelsBlockedHostPlayerLoad(t *testing.T) {
	store := newHostTestStore()
	store.blockLoads()
	defer store.releaseLoads()
	host := newTestHostWithStore(t, store)
	client, server := network.NewMemoryStreamPair(8)
	outer, cancelOuter := context.WithTimeout(context.Background(), longWaitDeadline)
	defer cancelOuter()
	serverDone := make(chan error, 1)
	started := time.Now()
	go func() { serverDone <- host.AcceptStream(outer, server) }()
	_, _ = network.LoginClient(outer, client, playerIdentity(14))
	select {
	case <-serverDone:
	case <-time.After(waitDeadline):
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
	case <-time.After(waitDeadline):
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
