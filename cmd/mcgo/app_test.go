//go:build darwin

package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/client"
	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/physics"
)

func TestInteractiveInputUsesDrainedReadyResetInSameFrame(t *testing.T) {
	app, serverEndpoint := newInteractiveTestApplication(t)
	app.camera.Pos = mgl32.Vec3{99, 99, 99}
	state := network.PlayerState{
		ServerTick: 1,
		Dimension:  core.Overworld,
		Position:   mgl32.Vec3{4.5, 20, -2.5},
		Yaw:        0.75,
		Pitch:      -0.2,
		OnGround:   true,
		Ready:      true,
		Reset:      true,
	}
	sendInteractiveServerMessage(t, serverEndpoint, state)

	app.drainServerMessages(1)
	app.applyInteractiveInput(0, client.Movement{}, client.Actions{Break: true}, true)

	wantPosition := mgl32.Vec3{4.5, 20 + physics.EyeHeight, -2.5}
	if app.camera.Pos != wantPosition || app.camera.Yaw != 0.75 || app.camera.Pitch != -0.2 {
		t.Fatalf("Ready Reset 同帧相机=%+v yaw=%v pitch=%v，想要 pos=%+v yaw=0.75 pitch=-0.2",
			app.camera.Pos, app.camera.Yaw, app.camera.Pitch, wantPosition)
	}
	message := receiveInteractiveClientMessage(t, serverEndpoint)
	breakBlock, ok := message.(network.BreakBlock)
	if !ok || breakBlock != (network.BreakBlock{Sequence: 1, Yaw: 0.75, Pitch: -0.2}) {
		t.Fatalf("Ready Reset 同帧动作=%#v", message)
	}
}

func TestInteractiveInputPresentsDrainedLargeCorrectionInSameFrame(t *testing.T) {
	app, serverEndpoint := newInteractiveTestApplication(t)
	begin := network.PlayerState{
		ServerTick: 1,
		Dimension:  core.Overworld,
		Position:   mgl32.Vec3{0.5, 10, 0.5},
		OnGround:   true,
		Ready:      true,
		Reset:      true,
	}
	sendInteractiveServerMessage(t, serverEndpoint, begin)
	app.drainServerMessages(1)
	app.applyInteractiveInput(0, client.Movement{}, client.Actions{}, false)

	corrected := begin
	corrected.ServerTick = 2
	corrected.Position = mgl32.Vec3{8.5, 30, -4.5}
	corrected.Reset = false
	sendInteractiveServerMessage(t, serverEndpoint, corrected)

	app.drainServerMessages(1)
	app.applyInteractiveInput(0, client.Movement{}, client.Actions{}, false)

	want := mgl32.Vec3{8.5, 30 + physics.EyeHeight, -4.5}
	if app.camera.Pos != want {
		t.Fatalf("大纠正同帧相机=%+v，想要 %+v", app.camera.Pos, want)
	}
}

func TestInteractiveInputUsesDrainedNotReadyForActionAndInputGate(t *testing.T) {
	app, serverEndpoint := newInteractiveTestApplication(t)
	ready := network.PlayerState{
		ServerTick: 1,
		Dimension:  core.Overworld,
		Position:   mgl32.Vec3{0.5, 10, 0.5},
		OnGround:   true,
		Ready:      true,
		Reset:      true,
	}
	sendInteractiveServerMessage(t, serverEndpoint, ready)
	app.drainServerMessages(1)
	app.applyInteractiveInput(0, client.Movement{}, client.Actions{}, false)

	notReady := ready
	notReady.ServerTick = 2
	notReady.Ready = false
	notReady.Reset = false
	sendInteractiveServerMessage(t, serverEndpoint, notReady)

	app.drainServerMessages(1)
	app.applyInteractiveInput(
		physics.FixedDelta,
		client.Movement{MoveZ: 1},
		client.Actions{Break: true},
		true,
	)

	if _, ready := app.predictor.State(); ready {
		t.Fatal("drain Ready=false 后 predictor 仍 Ready")
	}
	if app.sequence != 0 {
		t.Fatalf("Ready=false 同帧分配 sequence=%d", app.sequence)
	}
	assertNoInteractiveClientMessage(t, serverEndpoint)
}

func TestCursorReleaseSendsNeutralFixedStepAfterHeldInput(t *testing.T) {
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
	held := client.Movement{MoveX: 1, MoveZ: -1, Jump: true}
	app.applyInteractiveCursorInput(
		physics.FixedDelta, held, client.Actions{}, true, false,
	)
	first, ok := receiveInteractiveClientMessage(t, serverEndpoint).(network.PlayerInput)
	if !ok || first.Sequence != 1 || first.MoveX != held.MoveX ||
		first.MoveZ != held.MoveZ || first.Jump != held.Jump {
		t.Fatalf("captured held input=%+v", first)
	}

	app.applyInteractiveCursorInput(
		physics.FixedDelta, held, client.Actions{}, false, false,
	)
	neutral, ok := receiveInteractiveClientMessage(t, serverEndpoint).(network.PlayerInput)
	if !ok || neutral.Sequence != 2 || neutral.MoveX != 0 ||
		neutral.MoveZ != 0 || neutral.Jump {
		t.Fatalf("cursor release input=%+v，想要下一 fixed-step neutral", neutral)
	}
}

func newInteractiveTestApplication(
	t *testing.T,
) (*application, network.ServerEndpoint) {
	t.Helper()
	clientEndpoint, serverEndpoint := network.NewMemoryPair(8)
	t.Cleanup(func() { _ = clientEndpoint.Close() })
	return &application{
		clientEndpoint: clientEndpoint,
		mirror:         client.NewMirror(),
		predictor:      client.NewPredictor(),
		serverCancel:   func() {},
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
