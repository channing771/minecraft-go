//go:build darwin

package main

import (
	"context"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/gfx"
	"github.com/channing771/mornlea/internal/network"
)

type fakeInteractiveWindow struct {
	captured bool
}

func (window *fakeInteractiveWindow) SetCursorCaptured(captured bool) {
	window.captured = captured
}
func (*fakeInteractiveWindow) CursorPos() (float64, float64) { return 0, 0 }
func (*fakeInteractiveWindow) ShouldClose() bool             { return false }
func (*fakeInteractiveWindow) Poll()                         {}
func (*fakeInteractiveWindow) DrainTextInput(dst []rune) ([]rune, bool) {
	return dst, false
}
func (*fakeInteractiveWindow) KeyDown(client.Key) bool     { return false }
func (*fakeInteractiveWindow) PrimaryButtonDown() bool     { return false }
func (*fakeInteractiveWindow) SecondaryButtonDown() bool   { return false }
func (window *fakeInteractiveWindow) CursorCaptured() bool { return window.captured }
func (*fakeInteractiveWindow) FramebufferSize() (int, int) { return 1, 1 }
func (*fakeInteractiveWindow) ContentSize() (int, int)     { return 1, 1 }
func (*fakeInteractiveWindow) SetContentSize(int, int)     {}
func (*fakeInteractiveWindow) CancelClose()                {}
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
