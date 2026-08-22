package network

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// TestProtocolV23LoginSuccessCarriesWorldSeed 验证种子字段恰好追加在
// `PlayerID` 之后：固定 16 字节 UUID + little-endian uint64，全值域可往返，
// 任何截断或尾随字节都必须被拒绝，不产生部分包。
func TestProtocolV23LoginSuccessCarriesWorldSeed(t *testing.T) {
	id := mustCodecPlayerID(t)
	packet := LoginSuccess{PlayerID: id, WorldSeed: 0x1122334455667788}
	gotID, payload, err := encodeServerControlPayload(StateLogin, packet)
	if err != nil || gotID != 0 {
		t.Fatalf("encode = (id %d, %v)", gotID, err)
	}
	want := append(append([]byte(nil), id[:]...),
		0x88, 0x77, 0x66, 0x55, 0x44, 0x33, 0x22, 0x11)
	if string(payload) != string(want) {
		t.Fatalf("payload = %x，想要 %x（UUID 后跟 LE uint64 种子）", payload, want)
	}

	round, err := decodeServerControlPayload(StateLogin, gotID, payload)
	if err != nil {
		t.Fatal(err)
	}
	if got := round.(LoginSuccess); got.PlayerID != id || got.WorldSeed != packet.WorldSeed {
		t.Fatalf("往返 = %#v，想要 %#v", got, packet)
	}

	// payload 是固定长度 24 字节：所有截断与尾随字节都必须被拒绝。
	for length := 0; length < len(payload); length++ {
		if _, err := decodeServerControlPayload(StateLogin, gotID, payload[:length]); err == nil {
			t.Fatalf("截断到 %d 字节被接受", length)
		}
	}
	if _, err := decodeServerControlPayload(StateLogin, gotID, append(payload, 0)); err == nil {
		t.Fatal("多出尾随字节被接受")
	}
}

// TestProtocolV23LoginSuccessWorldSeedAcceptsFullRange 验证 uint64 全值域
// （含 0 与最大值）都不携带任何隐式校验：种子的语义解释属于客户端远环
// 播种（任务 5.2），wire 层只负责无损搬运。
func TestProtocolV23LoginSuccessWorldSeedAcceptsFullRange(t *testing.T) {
	id := mustCodecPlayerID(t)
	for _, seed := range []uint64{0, 1, 0x7fff_ffff_ffff_ffff, ^uint64(0)} {
		packet := LoginSuccess{PlayerID: id, WorldSeed: seed}
		gotID, payload, err := encodeServerControlPayload(StateLogin, packet)
		if err != nil {
			t.Fatalf("种子 %d 编码失败：%v", seed, err)
		}
		round, err := decodeServerControlPayload(StateLogin, gotID, payload)
		if err != nil {
			t.Fatalf("种子 %d 解码失败：%v", seed, err)
		}
		if got := round.(LoginSuccess); got.WorldSeed != seed {
			t.Fatalf("往返种子 = %d，想要 %d", got.WorldSeed, seed)
		}
	}
}

// TestBeginServerLoginSendsConfiguredWorldSeed 验证服务端登录驱动把
// `BeginServerLogin` 收到的种子原样填进 `LoginSuccess`；`LoginClient` 仍只校验
// `PlayerID`（种子消费属于 5.2），因此手工在 Login 状态读取应答断言字段。
func TestBeginServerLoginSendsConfiguredWorldSeed(t *testing.T) {
	const worldSeed = uint64(0x5eed_cafe_beef_0000)
	client, server := NewMemoryStreamPair(8)
	t.Cleanup(func() { _ = client.Close() })
	identity := testIdentity(22)

	serverDone := make(chan error, 1)
	go func() {
		pending, err := BeginServerLogin(context.Background(), server, worldSeed)
		if err != nil {
			serverDone <- err
			return
		}
		serverDone <- pending.Accept(context.Background(), func(ServerEndpoint) error { return nil })
	}()

	if err := client.Send(context.Background(), StateHandshake, ClientHello{ProtocolVersion: ProtocolVersion}); err != nil {
		t.Fatal(err)
	}
	if packet, err := client.Recv(context.Background(), StateHandshake); err != nil || packet != (ServerHello{ProtocolVersion: ProtocolVersion}) {
		t.Fatalf("server hello = (%+v, %v)", packet, err)
	}
	if err := client.Send(context.Background(), StateLogin, LoginStart{PlayerID: identity.PlayerID, DisplayName: identity.DisplayName}); err != nil {
		t.Fatal(err)
	}
	packet, err := client.Recv(context.Background(), StateLogin)
	if err != nil {
		t.Fatal(err)
	}
	success, ok := packet.(LoginSuccess)
	if !ok || success.PlayerID != identity.PlayerID || success.WorldSeed != worldSeed {
		t.Fatalf("login success = (%+v, %t)，想要 PlayerID=%s WorldSeed=%d", packet, ok, identity.PlayerID, worldSeed)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

// TestLoginClientWithSeedSurfacesWorldSeed 验证 5.2 的种子消费入口:
// `LoginClientWithSeed` 走与 `LoginClient` 完全相同的登录状态机,并额外把
// `LoginSuccess.WorldSeed` 返回给调用方(cmd/mornlea 装配点用它播种远环
// 壳)。uint64 全值域(含 0 与 two's complement 负种子)都必须无损返回;
// 登录失败时不返回端点。
func TestLoginClientWithSeedSurfacesWorldSeed(t *testing.T) {
	for _, worldSeed := range []uint64{0, 42, 0x5eed_cafe_beef_0000, ^uint64(0)} {
		clientStream, serverStream := NewMemoryStreamPair(8)
		t.Cleanup(func() { _ = clientStream.Close() })
		identity := testIdentity(24)
		serverDone := make(chan error, 1)
		go func() {
			pending, err := BeginServerLogin(context.Background(), serverStream, worldSeed)
			if err != nil {
				serverDone <- err
				return
			}
			serverDone <- pending.Accept(context.Background(), func(ServerEndpoint) error { return nil })
		}()
		endpoint, gotSeed, err := LoginClientWithSeed(context.Background(), clientStream, identity)
		if err != nil {
			t.Fatalf("种子 %#x: LoginClientWithSeed: %v", worldSeed, err)
		}
		if gotSeed != worldSeed {
			t.Fatalf("种子 %#x: 返回 %#x, want 原样", worldSeed, gotSeed)
		}
		if endpoint == nil {
			t.Fatalf("种子 %#x: 登录成功必须返回端点", worldSeed)
		}
		if err := endpoint.Close(); err != nil {
			t.Fatal(err)
		}
		if err := <-serverDone; err != nil {
			t.Fatal(err)
		}
	}
}

// TestV24ClientRejectsPriorServerAcrossTransports 模拟一个只会说 v17 的
// 服务端（生产服务端已升 v24，只能以对端身份模拟；类型化 `Send` 会拒绝
// 非当前版本的 `ServerHello`，因此按传输写原始字节），验证当前客户端在
// Memory 与 TCP 上都拒绝它，不进入登录阶段，也不产生半兼容会话。
func TestV24ClientRejectsPriorServerAcrossTransports(t *testing.T) {
	const legacy = uint32(17)
	for _, open := range transportOpeners {
		t.Run(open.name, func(t *testing.T) {
			client, server := open.open(t)
			t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
			serverDone := make(chan error, 1)
			go func() {
				if _, err := server.Recv(context.Background(), StateHandshake); err != nil {
					serverDone <- err
					return
				}
				serverDone <- sendRawServerHello(server, legacy)
			}()

			_, err := LoginClient(context.Background(), client, testIdentity(23))
			// 两个传输都在接收侧校验版本并按 protocol violation 关闭连接；
			// `LoginClient` 自身的版本分支是纵深防御，两者都算正确拒绝。
			if err == nil || !strings.Contains(err.Error(), "protocol violation") ||
				!(strings.Contains(err.Error(), "server protocol version 17") ||
					strings.Contains(err.Error(), "server handshake version mismatch")) {
				t.Fatalf("v17 服务端登录结果 = %v，想要握手版本不匹配拒绝", err)
			}
			if err := <-serverDone; err != nil {
				t.Fatal(err)
			}
			// 客户端在握手失败后必须关闭连接，不允许退化进入 Play。
			if err := client.Send(context.Background(), StatePlay, PlayerInput{}); err == nil {
				t.Fatal("握手版本不匹配后客户端仍能发送 Play 包")
			}
		})
	}
}

// sendRawServerHello 绕过类型化校验，把一条任意版本的 `ServerHello` 原样
// 送进对端：Memory 直写 channel，TCP 直写 wire 帧，镜像
// `sendProtocolVersionHello` 的客户端版本。
func sendRawServerHello(server ServerPacketStream, version uint32) error {
	switch server := server.(type) {
	case *memoryServerStream:
		return memorySend(context.Background(), server.pair, server.pair.serverToClient, ServerPacket(ServerHello{ProtocolVersion: version}))
	case *tcpServerStream:
		return WriteFrame(server.stream.conn, 0, []byte{byte(version)})
	default:
		return fmt.Errorf("未知 server stream %T", server)
	}
}
