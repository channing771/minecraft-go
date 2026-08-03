package server

import (
	"context"
	"sync"
	"testing"
	"time"

	"minecraft-go/internal/network"
)

func TestLoginTCPMultiplayer(t *testing.T) {
	store := newHostTestStore()
	config := hostTestConfig()
	config.MaxPlayers = 8
	host := NewHost(config, flatTestGenerator{}, store)
	listener, err := network.ListenTCP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- host.Run(runCtx, listener) }()
	t.Cleanup(func() {
		cancelRun()
		select {
		case err := <-runDone:
			if err != nil {
				t.Errorf("Host Run cleanup: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("Host Run cleanup timed out")
		}
	})

	type result struct {
		login testLogin
		err   error
	}
	results := make(chan result, 8)
	var group sync.WaitGroup
	for number := byte(1); number <= 8; number++ {
		identity := playerIdentity(number)
		group.Add(1)
		go func() {
			defer group.Done()
			endpoint, loginErr := dialAndLoginTCP(listener.Addr(), identity)
			results <- result{login: testLogin{Client: endpoint, Identity: identity}, err: loginErr}
		}()
	}
	group.Wait()
	close(results)
	logins := make([]testLogin, 0, 8)
	for got := range results {
		if got.err != nil {
			t.Fatalf("concurrent TCP login: %v", got.err)
		}
		logins = append(logins, got.login)
	}
	for _, login := range logins {
		waitReady(t, host, login)
		t.Cleanup(func() { _ = login.Client.Close() })
	}

	_, duplicateErr := dialAndLoginTCP(listener.Addr(), playerIdentity(1))
	assertLoginRejectCode(t, duplicateErr, network.LoginAlreadyOnline)
	_, fullErr := dialAndLoginTCP(listener.Addr(), playerIdentity(9))
	assertLoginRejectCode(t, fullErr, network.LoginServerFull)
}

func dialAndLoginTCP(address string, identity network.Identity) (network.ClientEndpoint, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := network.DialTCP(ctx, address)
	if err != nil {
		return nil, err
	}
	endpoint, err := network.LoginClient(ctx, stream, identity)
	if err != nil {
		_ = stream.Close()
		return nil, err
	}
	return endpoint, nil
}
