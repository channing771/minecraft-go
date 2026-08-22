package server

import (
	"context"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/network"
)

// TestHostLoginSuccessCarriesStoreSeedAcrossTransports 验证 `Host` 在构造
// `LoginSuccess` 时填入存档 metadata 的真实世界种子（与 worldgen 播种同源），
// 且单机内存路径（`AcceptStream`）与 TCP 专用服务端路径（`acceptLoop`）走同一
// 编码：两个传输上的 `LoginSuccess.WorldSeed` 逐位一致。
func TestHostLoginSuccessCarriesStoreSeedAcrossTransports(t *testing.T) {
	store := newHostTestStore()
	const wantSeed = uint64(42)
	if got := uint64(store.Metadata().Seed); got != wantSeed {
		t.Fatalf("测试存档种子 = %d，想要 %d", got, wantSeed)
	}

	var logins []network.LoginSuccess
	for _, transport := range []struct {
		name string
		run  func(t *testing.T, host *Host) network.LoginSuccess
	}{
		{"memory", loginSeedOverMemory},
		{"tcp", loginSeedOverTCP},
	} {
		t.Run(transport.name, func(t *testing.T) {
			host := mustNewHost(t, hostTestConfig(), flatTestGenerator{}, store)
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
				defer cancel()
				if err := host.Shutdown(ctx); err != nil {
					t.Errorf("Host cleanup Shutdown: %v", err)
				}
			})
			success := transport.run(t, host)
			if success.WorldSeed != wantSeed {
				t.Fatalf("LoginSuccess.WorldSeed = %d，想要存档种子 %d", success.WorldSeed, wantSeed)
			}
			logins = append(logins, success)
		})
	}
	if len(logins) == 2 && logins[0] != logins[1] {
		t.Fatalf("Memory 与 TCP 登录应答不一致：memory=%+v tcp=%+v", logins[0], logins[1])
	}
}

// loginSeedOverMemory 复刻单机内置服务端的装配：`Host.Run` 驱动权威 tick，
// `AcceptStream` 接收内存流（与 cmd/mornlea 的 `assembleLocalApplicationConnection`
// 同构）。手工驱动客户端握手并在 Login 状态读取 `LoginSuccess`，因为
// `network.LoginClient` 有意不消费种子字段（客户端播种属于 5.2）。
func loginSeedOverMemory(t *testing.T, host *Host) network.LoginSuccess {
	t.Helper()
	runCtx, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- host.Run(runCtx, nil) }()
	t.Cleanup(func() {
		cancelRun()
		select {
		case err := <-runDone:
			if err != nil {
				t.Errorf("Host Run cleanup: %v", err)
			}
		case <-time.After(waitDeadline):
			t.Error("Host Run cleanup timed out")
		}
	})

	clientStream, serverStream := network.NewMemoryStreamPair(256)
	done := make(chan error, 1)
	go func() { done <- host.AcceptStream(context.Background(), serverStream) }()
	success := driveManualLogin(t, clientStream)
	// 读到应答后立即断开客户端：tick 中的世界按真实掉线语义收敛会话，
	// `AcceptStream` 随之返回，不必等心跳超时。
	if err := clientStream.Close(); err != nil {
		t.Fatal(err)
	}
	waitLoginDone(t, done)
	return success
}

// loginSeedOverTCP 走专用服务端同款路径：`Host.Run` 的 TCP `acceptLoop`。
func loginSeedOverTCP(t *testing.T, host *Host) network.LoginSuccess {
	t.Helper()
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
		case <-time.After(waitDeadline):
			t.Error("Host Run cleanup timed out")
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
	defer cancel()
	clientStream, err := network.DialTCP(ctx, listener.Addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientStream.Close() })
	return driveManualLogin(t, clientStream)
}

// driveManualLogin 以当前协议版本手工完成握手与登录，返回服务端下发的
// `LoginSuccess` 原文。
func driveManualLogin(t *testing.T, client network.ClientPacketStream) network.LoginSuccess {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
	defer cancel()
	identity := playerIdentity(31)
	if err := client.Send(ctx, network.StateHandshake, network.ClientHello{ProtocolVersion: network.ProtocolVersion}); err != nil {
		t.Fatal(err)
	}
	if packet, err := client.Recv(ctx, network.StateHandshake); err != nil || packet != (network.ServerHello{ProtocolVersion: network.ProtocolVersion}) {
		t.Fatalf("server hello = (%+v, %v)", packet, err)
	}
	if err := client.Send(ctx, network.StateLogin, network.LoginStart{PlayerID: identity.PlayerID, DisplayName: identity.DisplayName}); err != nil {
		t.Fatal(err)
	}
	packet, err := client.Recv(ctx, network.StateLogin)
	if err != nil {
		t.Fatal(err)
	}
	success, ok := packet.(network.LoginSuccess)
	if !ok || success.PlayerID != identity.PlayerID {
		t.Fatalf("login success = (%+v, %t)，想要 PlayerID=%s", packet, ok, identity.PlayerID)
	}
	return success
}
