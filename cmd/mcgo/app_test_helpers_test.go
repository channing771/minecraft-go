//go:build darwin

package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"minecraft-go/internal/client"
	"minecraft-go/internal/core"
	"minecraft-go/internal/gfx"
	"minecraft-go/internal/network"
)

func integrationPlayerID(last byte) core.PlayerID {
	return core.PlayerID{0: 0x12, 6: 0x40, 8: 0x80, 15: last}
}

type fakeInteractiveWindow struct {
	captured bool
}

func (window *fakeInteractiveWindow) SetCursorCaptured(captured bool) {
	window.captured = captured
}
func (*fakeInteractiveWindow) CursorPos() (float64, float64) { return 0, 0 }
func (*fakeInteractiveWindow) ShouldClose() bool             { return false }
func (*fakeInteractiveWindow) Poll()                         {}
func (*fakeInteractiveWindow) KeyDown(client.Key) bool       { return false }
func (*fakeInteractiveWindow) PrimaryButtonDown() bool       { return false }
func (*fakeInteractiveWindow) SecondaryButtonDown() bool     { return false }
func (window *fakeInteractiveWindow) CursorCaptured() bool   { return window.captured }
func (*fakeInteractiveWindow) FramebufferSize() (int, int)   { return 1, 1 }
func (*fakeInteractiveWindow) ContentSize() (int, int)       { return 1, 1 }
func (*fakeInteractiveWindow) SetContentSize(int, int)       {}
func (*fakeInteractiveWindow) CancelClose()                  {}
func (*fakeInteractiveWindow) NativeHandle() gfx.NativeWindowHandle {
	return gfx.NativeWindowHandle{}
}
func (*fakeInteractiveWindow) Close() {}

func newInteractiveTestApplication(
	t *testing.T,
) (*application, network.ServerEndpoint) {
	t.Helper()
	clientEndpoint, serverEndpoint := network.NewMemoryPair(8)
	t.Cleanup(func() { _ = clientEndpoint.Close() })
	return &application{
		clientEndpoint:  clientEndpoint,
		receiver:        client.NewReceiver(clientEndpoint, 8),
		mirror:          client.NewMirror(),
		itemDrops:       client.NewItemDrops(),
		inventorySource: -1,
		predictor:       client.NewPredictor(),
		serverCancel:    func() {},
	}, serverEndpoint
}
func sendInteractiveServerMessage(
	t *testing.T,
	endpoint network.ServerEndpoint,
	message network.ServerMessage,
) {
	t.Helper()
	if err := endpoint.Send(context.Background(), message); err != nil {
		t.Fatalf("发送服务端消息: %v", err)
	}
	// The application intentionally drains a non-blocking Receiver; let its sole
	// blocking reader hand this test message to the inbox before the frame drains.
	time.Sleep(time.Millisecond)
}

func receiveInteractiveClientMessage(
	t *testing.T,
	endpoint network.ServerEndpoint,
) network.ClientMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	message, err := endpoint.Recv(ctx)
	if err != nil {
		t.Fatalf("接收客户端消息: %v", err)
	}
	return message
}

func assertNoInteractiveClientMessage(t *testing.T, endpoint network.ServerEndpoint) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	message, err := endpoint.Recv(ctx)
	if err == nil {
		t.Fatalf("意外客户端消息: %#v", message)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("检查无客户端消息: %v", err)
	}
}
