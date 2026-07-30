package network

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestProtocolTranscriptSuccessMatchesMemoryAndTCP(t *testing.T) {
	for _, open := range []struct {
		name string
		open func(*testing.T) (ClientPacketStream, ServerPacketStream)
	}{
		{"memory", func(t *testing.T) (ClientPacketStream, ServerPacketStream) { return NewMemoryStreamPair(8) }},
		{"tcp", openTCPStreamPair},
	} {
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
	for _, open := range []struct {
		name string
		open func(*testing.T) (ClientPacketStream, ServerPacketStream)
	}{
		{"memory", func(t *testing.T) (ClientPacketStream, ServerPacketStream) { return NewMemoryStreamPair(8) }},
		{"tcp", openTCPStreamPair},
	} {
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
	for _, open := range []struct {
		name string
		open func(*testing.T) (ClientPacketStream, ServerPacketStream)
	}{
		{"memory", func(t *testing.T) (ClientPacketStream, ServerPacketStream) { return NewMemoryStreamPair(8) }},
		{"tcp", openTCPStreamPair},
	} {
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
