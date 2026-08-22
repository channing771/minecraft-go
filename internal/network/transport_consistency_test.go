package network

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/channing771/mornlea/internal/core"
)

var transportOpeners = []struct {
	name string
	open func(*testing.T) (ClientPacketStream, ServerPacketStream)
}{
	{"memory", func(t *testing.T) (ClientPacketStream, ServerPacketStream) { return NewMemoryStreamPair(8) }},
	{"tcp", openTCPStreamPair},
}

func TestProtocolTranscriptSuccessMatchesMemoryAndTCP(t *testing.T) {
	for _, open := range transportOpeners {
		t.Run(open.name, func(t *testing.T) {
			clientStream, serverStream := open.open(t)
			t.Cleanup(func() { _ = clientStream.Close(); _ = serverStream.Close() })
			serverDone := make(chan error, 1)
			go func() {
				pending, err := BeginServerLogin(context.Background(), serverStream, 0)
				if err != nil {
					serverDone <- err
					return
				}
				var endpoint ServerEndpoint
				err = pending.Accept(context.Background(), func(attached ServerEndpoint) error {
					endpoint = attached
					return nil
				})
				if err == nil {
					err = endpoint.Send(context.Background(), PlayerState{Ready: true})
				}
				serverDone <- err
			}()

			endpoint, err := LoginClient(context.Background(), clientStream, testIdentity(11))
			if err != nil {
				t.Fatal(err)
			}
			packet, err := endpoint.Recv(context.Background())
			if err != nil || packet != (PlayerState{Ready: true}) {
				t.Fatalf("play transcript = (%+v, %v)", packet, err)
			}
			if err := <-serverDone; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestProtocolTranscriptRejectMatchesMemoryAndTCP(t *testing.T) {
	for _, open := range transportOpeners {
		t.Run(open.name, func(t *testing.T) {
			clientStream, serverStream := open.open(t)
			t.Cleanup(func() { _ = clientStream.Close(); _ = serverStream.Close() })
			serverDone := make(chan error, 1)
			go func() {
				pending, err := BeginServerLogin(context.Background(), serverStream, 0)
				if err != nil {
					serverDone <- err
					return
				}
				serverDone <- pending.Reject(context.Background(), LoginServerFull, "server full")
			}()

			_, err := LoginClient(context.Background(), clientStream, testIdentity(12))
			var remote *RemoteError
			if !errors.As(err, &remote) || remote.State != StateLogin || remote.Code != uint8(LoginServerFull) || remote.Message != "server full" {
				t.Fatalf("reject transcript = %#v", err)
			}
			if err := <-serverDone; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestProtocolTranscriptRejectsEarlyPlayAcrossMemoryAndTCP(t *testing.T) {
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
				serverDone <- server.Send(context.Background(), StatePlay, PlayerState{})
			}()

			_, err := LoginClient(context.Background(), client, testIdentity(13))
			if err == nil || !strings.Contains(err.Error(), "protocol violation") {
				t.Fatalf("early Play transcript error = %v", err)
			}
			if err := <-serverDone; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPlaySemanticValidationMatchesMemoryAndTCP(t *testing.T) {
	packets := []struct {
		name   string
		packet ClientPacket
	}{
		{"place block slot out of range", PlaceBlock{Slot: core.HotbarSlots}},
		{"resync outside overworld", RequestChunkResync{Dimension: core.DimensionID(1)}},
	}
	for _, packet := range packets {
		t.Run(packet.name, func(t *testing.T) {
			for _, transport := range transportOpeners {
				t.Run(transport.name, func(t *testing.T) {
					client, server := transport.open(t)
					t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
					if err := client.Send(context.Background(), StatePlay, packet.packet); err == nil {
						t.Fatalf("%s accepted invalid %T", transport.name, packet.packet)
					}
				})
			}
		})
	}
}

func TestCommonBlockMaterialPlayTranscriptMatchesMemoryAndTCP(t *testing.T) {
	snapshot := repeatedSnapshot(SectionData{Storage: SectionSingle, Single: core.AirID})
	snapshot.Sections[0].Single = core.MossyCobblestoneID
	var inventory core.Inventory
	inventory.Backpack[0] = core.ItemStack{
		Item: core.ItemMossyCobblestone, Count: core.MaxStackCount,
	}
	want := []ServerMessage{snapshot, InventoryState{Inventory: inventory}}

	for _, open := range transportOpeners {
		t.Run(open.name, func(t *testing.T) {
			clientStream, serverStream := open.open(t)
			t.Cleanup(func() { _ = clientStream.Close(); _ = serverStream.Close() })
			serverDone := make(chan error, 1)
			go func() {
				pending, err := BeginServerLogin(context.Background(), serverStream, 0)
				if err != nil {
					serverDone <- err
					return
				}
				var endpoint ServerEndpoint
				err = pending.Accept(context.Background(), func(attached ServerEndpoint) error {
					endpoint = attached
					return nil
				})
				for _, message := range want {
					if err == nil {
						err = endpoint.Send(context.Background(), message)
					}
				}
				serverDone <- err
			}()

			endpoint, err := LoginClient(context.Background(), clientStream, testIdentity(14))
			if err != nil {
				t.Fatal(err)
			}
			for index, message := range want {
				got, err := endpoint.Recv(context.Background())
				if err != nil || !reflect.DeepEqual(got, message) {
					t.Fatalf("Play 消息 %d = (%#v, %v)，想要 %#v", index, got, err, message)
				}
			}
			if err := <-serverDone; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestProtocolOutdatedHandshakeRejectMatchesMemoryAndTCP(t *testing.T) {
	// v15、v16 与 v17 都是过时版本：Memory 与 TCP 必须产生相同的 v23 版本
	// 不匹配拒绝(变基重编:WorldSeed 段由 v18 重编为 v23,v22 为
	// authoritative-farming 已交付的翻地命令段,对本客户端同样是被拒的过时版本)。
	for _, version := range []uint32{15, 16, 17} {
		for _, open := range transportOpeners {
			t.Run(fmt.Sprintf("v%d/%s", version, open.name), func(t *testing.T) {
				client, server := open.open(t)
				t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
				serverDone := make(chan error, 1)
				go func() {
					_, err := BeginServerLogin(context.Background(), server, 0)
					serverDone <- err
				}()

				sendProtocolVersionHello(t, client, version)
				packet, err := client.Recv(context.Background(), StateHandshake)
				reject, ok := packet.(HandshakeReject)
				if err != nil || !ok || reject.Code != HandshakeVersionMismatch || reject.ServerProtocolVersion != ProtocolVersion {
					t.Fatalf("v%d 拒绝 = (%#v, %v)，想要服务端 v%d HandshakeVersionMismatch", version, packet, err, ProtocolVersion)
				}
				if err := <-serverDone; err == nil {
					t.Fatalf("v%d 握手意外进入登录", version)
				}
			})
		}
	}
}

func sendProtocolVersionHello(t *testing.T, client ClientPacketStream, version uint32) {
	t.Helper()
	switch client := client.(type) {
	case *memoryClientStream:
		if err := memorySend(t.Context(), client.pair, client.pair.clientToServer, ClientPacket(ClientHello{ProtocolVersion: version})); err != nil {
			t.Fatal(err)
		}
	case *tcpClientStream:
		if err := WriteFrame(client.stream.conn, 0, []byte{byte(version)}); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("未知 client stream %T", client)
	}
}

func openTCPStreamPair(t *testing.T) (ClientPacketStream, ServerPacketStream) {
	t.Helper()
	listener, err := ListenTCP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	clientDone := make(chan struct {
		stream ClientPacketStream
		err    error
	}, 1)
	go func() {
		stream, err := DialTCP(context.Background(), listener.Addr())
		clientDone <- struct {
			stream ClientPacketStream
			err    error
		}{stream, err}
	}()
	server, err := listener.Accept(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	client := <-clientDone
	if client.err != nil {
		t.Fatal(client.err)
	}
	return client.stream, server
}
