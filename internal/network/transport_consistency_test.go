package network

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"minecraft-go/internal/core"
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
				pending, err := BeginServerLogin(context.Background(), serverStream)
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
				pending, err := BeginServerLogin(context.Background(), serverStream)
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
				pending, err := BeginServerLogin(context.Background(), serverStream)
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

func TestProtocolVersion13HandshakeRejectMatchesMemoryAndTCP(t *testing.T) {
	for _, open := range transportOpeners {
		t.Run(open.name, func(t *testing.T) {
			client, server := open.open(t)
			t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
			serverDone := make(chan error, 1)
			go func() {
				_, err := BeginServerLogin(context.Background(), server)
				serverDone <- err
			}()

			sendProtocol13Hello(t, client)
			packet, err := client.Recv(context.Background(), StateHandshake)
			reject, ok := packet.(HandshakeReject)
			if err != nil || !ok || reject.Code != HandshakeVersionMismatch || reject.ServerProtocolVersion != 14 {
				t.Fatalf("v13 拒绝 = (%#v, %v)，想要服务端 v14 HandshakeVersionMismatch", packet, err)
			}
			if err := <-serverDone; err == nil {
				t.Fatal("v13 握手意外进入登录")
			}
		})
	}
}

func sendProtocol13Hello(t *testing.T, client ClientPacketStream) {
	t.Helper()
	switch client := client.(type) {
	case *memoryClientStream:
		if err := memorySend(t.Context(), client.pair, client.pair.clientToServer, ClientPacket(ClientHello{ProtocolVersion: 13})); err != nil {
			t.Fatal(err)
		}
	case *tcpClientStream:
		if err := WriteFrame(client.stream.conn, 0, []byte{13}); err != nil {
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
