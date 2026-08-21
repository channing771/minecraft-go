package server

import (
	"context"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/network"
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

	dialCtx, cancelDial := context.WithTimeout(context.Background(), waitDeadline)
	stream, err := network.DialTCP(dialCtx, listener.Addr())
	cancelDial()
	if err != nil {
		t.Fatal(err)
	}
	identity := playerIdentity(11)
	loginCtx, cancelLogin := context.WithTimeout(context.Background(), waitDeadline)
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
	case <-time.After(waitDeadline):
		t.Fatal("Run did not complete TCP shutdown")
	}
	if store.syncCount() != 1 || store.closeCount() != 1 {
		t.Fatalf("TCP host store shutdown counts = sync %d close %d", store.syncCount(), store.closeCount())
	}
}

// TestNewHostWiresEngineSeedFromStoreMetadata 断言 NewHost 把
// store.Metadata().Seed 原样传给了 sim.NewEngine：host.world.engine 的种子
// 必须与存档 metadata 的种子一致。newHostTestStore 用非零种子 42，避免和
// 「接线断了、engine 悄悄退回默认零值」这种失败混淆。
func TestNewHostWiresEngineSeedFromStoreMetadata(t *testing.T) {
	store := newHostTestStore()
	host := newTestHostWithStore(t, store)
	wantSeed := store.Metadata().Seed
	if wantSeed == 0 {
		t.Fatal("测试种子为 0，无法与默认零值区分")
	}
	if got := host.world.engine.SeedForTest(); got != wantSeed {
		t.Fatalf("engine 种子 = %d，想要 %d", got, wantSeed)
	}
}
