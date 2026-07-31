package server

import (
	"context"
	"testing"
	"time"

	"minecraft-go/internal/network"
)

func TestHostTCPLoginDisconnectAndShutdown(t *testing.T) {
	store := newHostTestStore()
	host := newTestHostWithStore(t, store)
	listener, err := network.ListenTCP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- host.Run(runCtx, listener) }()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 2*time.Second)
	stream, err := network.DialTCP(dialCtx, listener.Addr())
	cancelDial()
	if err != nil {
		t.Fatal(err)
	}
	identity := playerIdentity(11)
	loginCtx, cancelLogin := context.WithTimeout(context.Background(), 2*time.Second)
	client, err := network.LoginClient(loginCtx, stream, identity)
	cancelLogin()
	if err != nil {
		t.Fatal(err)
	}
	waitReady(t, host, testLogin{Client: client, Identity: identity})
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	waitForNoActiveLogin(t, host)

	cancelRun()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run shutdown error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not complete TCP shutdown")
	}
	if store.syncCount() != 1 || store.closeCount() != 1 {
		t.Fatalf("TCP host store shutdown counts = sync %d close %d", store.syncCount(), store.closeCount())
	}
}
