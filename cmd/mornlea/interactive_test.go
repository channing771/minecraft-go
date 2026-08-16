//go:build darwin

package main

import (
	"errors"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/physics"
)

// Mutation killed: ignoring renderFrame's error makes the interactive loop
// continue after a glyph worker failure instead of returning it immediately.
func TestRunInteractivePropagatesRemoteGlyphError(t *testing.T) {
	wantErr := errors.New("interactive glyph worker failure")
	app := newRemoteRenderApplication(t, &integrationGlyphSource{flushErr: wantErr})
	app.window = &oneFrameInteractiveWindow{delay: 25 * time.Millisecond}
	clientEndpoint, serverEndpoint := network.NewMemoryPair(4)
	app.clientEndpoint = clientEndpoint
	app.receiver = client.NewReceiver(clientEndpoint, 4)
	t.Cleanup(func() { _ = serverEndpoint.Close() })
	if err := app.remotePlayers.Apply(remoteSpawn(1, "Remote-1", 1, mgl32.Vec3{})); err != nil {
		t.Fatal(err)
	}
	for index, x := range []float32{4, 8} {
		if err := app.remotePlayers.Apply(network.RemotePlayerStates{
			ServerTick: uint64(index + 2),
			Players: []network.RemotePlayerState{{
				PlayerID: integrationPlayerID(1), Dimension: core.Overworld,
				Position: mgl32.Vec3{x, 0, 0},
			}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := runInteractive(app); !errors.Is(err, wantErr) {
		t.Fatalf("runInteractive error=%v want wrapped glyph error", err)
	}
	if x := app.remotePlayers.Presentations()[0].Position[0]; x < 1.5 || x > 3 {
		t.Fatalf("interactive interpolation x=%f want elapsed-driven midpoint range", x)
	}
}

func TestInteractiveInputCarriesMiningOnlyWhenActionsAllowed(t *testing.T) {
	app, serverEndpoint := newInteractiveTestApplication(t)
	if err := app.predictor.Begin(network.PlayerState{
		ServerTick: 1,
		Dimension:  core.Overworld,
		Position:   mgl32.Vec3{0.5, 10, 0.5},
		OnGround:   true,
		Ready:      true,
	}); err != nil {
		t.Fatal(err)
	}
	app.camera.Yaw = 0.75
	app.camera.Pitch = -0.2

	app.applyInteractiveInput(
		physics.FixedDelta, client.Movement{}, client.Actions{Mining: true}, true,
	)
	mining, ok := receiveInteractiveClientMessage(t, serverEndpoint).(network.PlayerInput)
	if !ok || !mining.Mining || mining.Yaw != 0.75 || mining.Pitch != -0.2 {
		t.Fatalf("允许操作时 fixed-step input=%+v", mining)
	}

	app.applyInteractiveInput(
		physics.FixedDelta, client.Movement{}, client.Actions{Mining: true}, false,
	)
	neutral, ok := receiveInteractiveClientMessage(t, serverEndpoint).(network.PlayerInput)
	if !ok || neutral.Mining {
		t.Fatalf("抑制操作时 fixed-step input=%+v，想要 Mining=false", neutral)
	}
}

func TestInteractiveCursorInputSuppressesStaleMiningAfterInventoryOpens(t *testing.T) {
	app, serverEndpoint := newInteractiveTestApplication(t)
	if err := app.predictor.Begin(network.PlayerState{
		ServerTick: 1,
		Dimension:  core.Overworld,
		Position:   mgl32.Vec3{0.5, 10, 0.5},
		OnGround:   true,
		Ready:      true,
	}); err != nil {
		t.Fatal(err)
	}

	// 模拟同帧采样到按住主键后，E 键才打开背包的顺序。
	app.setInventoryOpen(true)
	app.applyInteractiveCursorInput(
		physics.FixedDelta, client.Movement{}, client.Actions{Mining: true}, true, false,
	)
	input, ok := receiveInteractiveClientMessage(t, serverEndpoint).(network.PlayerInput)
	if !ok || input.Mining {
		t.Fatalf("打开背包后的 stale input=%+v，想要 Mining=false", input)
	}
}

type oneFrameInteractiveWindow struct {
	fakeInteractiveWindow
	polled bool
	delay  time.Duration
}

func (window *oneFrameInteractiveWindow) ShouldClose() bool { return window.polled }

func (window *oneFrameInteractiveWindow) Poll() {
	time.Sleep(window.delay)
	window.polled = true
}

func (*oneFrameInteractiveWindow) FramebufferSize() (int, int) { return 16, 16 }
