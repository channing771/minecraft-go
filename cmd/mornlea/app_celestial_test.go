//go:build darwin

package main

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/render"
)

func TestApplicationCelestialParametersKeepLastAcceptedWorldTime(t *testing.T) {
	app, serverEndpoint := newInteractiveTestApplication(t)
	newest := network.PlayerState{
		ServerTick: 2, WorldTimeTicks: 6000, Dimension: core.Overworld,
		Position: mgl32.Vec3{0.5, 10, 0.5}, OnGround: true, Ready: true,
	}
	sendInteractiveServerMessage(t, serverEndpoint, newest)
	app.drainServerMessages(1)
	want := render.DayNightAt(newest.WorldTimeTicks)
	if got := render.DayNightAt(app.worldTimeTicks); got != want {
		t.Fatalf("接受新状态后的天体参数 = %+v，想要 %+v", got, want)
	}
	if got := want.SunDirection; math.Abs(float64(got[1]-1)) > 1e-5 || math.Abs(float64(got[0])) > 1e-5 || math.Abs(float64(got[2])) > 1e-5 {
		t.Fatalf("接受新状态后的太阳方向 = %v，想要正午天顶", got)
	}

	for _, stale := range []network.PlayerState{
		{ServerTick: 1, WorldTimeTicks: 18000, Dimension: core.Overworld, Position: newest.Position, OnGround: true, Ready: true},
		{ServerTick: 2, WorldTimeTicks: 12000, Dimension: core.Overworld, Position: newest.Position, OnGround: true, Ready: true},
	} {
		sendInteractiveServerMessage(t, serverEndpoint, stale)
		app.drainServerMessages(1)
		if app.worldTimeTicks != newest.WorldTimeTicks {
			t.Fatalf("旧或重复状态将世界时间改为 %d，想要 %d", app.worldTimeTicks, newest.WorldTimeTicks)
		}
		if got := render.DayNightAt(app.worldTimeTicks); got != want {
			t.Fatalf("旧或重复状态改变天体参数 = %+v，想要 %+v", got, want)
		}
	}
}

func TestApplicationCelestialParametersMatchMemoryAndTCP(t *testing.T) {
	state := network.PlayerState{
		ServerTick: 1, WorldTimeTicks: 18000, Dimension: core.Overworld,
		Position: mgl32.Vec3{0.5, 10, 0.5}, OnGround: true, Ready: true,
	}
	var memory render.DayNight
	for _, transport := range []string{"memory", "tcp"} {
		t.Run(transport, func(t *testing.T) {
			clientEndpoint, serverEndpoint := celestialTestEndpoints(t, transport)
			app := &application{
				clientEndpoint: clientEndpoint,
				receiver:       client.NewReceiver(clientEndpoint, 8),
				mirror:         client.NewMirror(),
				predictor:      client.NewPredictor(),
				serverCancel:   func() {},
			}
			sendInteractiveServerMessage(t, serverEndpoint, state)
			deadline := time.Now().Add(time.Second)
			for app.serverTick != state.ServerTick && time.Now().Before(deadline) {
				app.drainServerMessages(1)
				time.Sleep(time.Millisecond)
			}
			if app.serverTick != state.ServerTick {
				t.Fatalf("等待权威世界时间超时: receiver error=%v", app.receiver.Err())
			}
			got := render.DayNightAt(app.worldTimeTicks)
			if math.Abs(float64(got.SunDirection[1]+1)) > 1e-5 || math.Abs(float64(got.SunDirection[0])) > 1e-5 || math.Abs(float64(got.SunDirection[2])) > 1e-5 {
				t.Fatalf("午夜太阳方向 = %v，想要地平线下方", got.SunDirection)
			}
			if transport == "memory" {
				memory = got
				return
			}
			if got != memory {
				t.Fatalf("TCP 天体参数 = %+v，想要与 Memory 相同的 %+v", got, memory)
			}
		})
	}
}

func celestialTestEndpoints(t *testing.T, transport string) (network.ClientEndpoint, network.ServerEndpoint) {
	t.Helper()
	if transport == "memory" {
		clientEndpoint, serverEndpoint := network.NewMemoryPair(8)
		t.Cleanup(func() {
			_ = clientEndpoint.Close()
			_ = serverEndpoint.Close()
		})
		return clientEndpoint, serverEndpoint
	}
	listener, err := network.ListenTCP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	dialed := make(chan struct {
		stream network.ClientPacketStream
		err    error
	}, 1)
	go func() {
		stream, err := network.DialTCP(context.Background(), listener.Addr())
		dialed <- struct {
			stream network.ClientPacketStream
			err    error
		}{stream, err}
	}()
	serverStream, err := listener.Accept(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan struct {
		endpoint network.ServerEndpoint
		err      error
	}, 1)
	go func() {
		pending, err := network.BeginServerLogin(context.Background(), serverStream, 0)
		if err != nil {
			accepted <- struct {
				endpoint network.ServerEndpoint
				err      error
			}{err: err}
			return
		}
		var endpoint network.ServerEndpoint
		err = pending.Accept(context.Background(), func(attached network.ServerEndpoint) error {
			endpoint = attached
			return nil
		})
		accepted <- struct {
			endpoint network.ServerEndpoint
			err      error
		}{endpoint, err}
	}()
	clientStream := <-dialed
	if clientStream.err != nil {
		t.Fatal(clientStream.err)
	}
	clientEndpoint, err := network.LoginClient(context.Background(), clientStream.stream, network.Identity{
		PlayerID: integrationPlayerID(9), DisplayName: "Celestial",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := <-accepted
	if server.err != nil {
		t.Fatal(server.err)
	}
	t.Cleanup(func() {
		_ = clientEndpoint.Close()
		_ = server.endpoint.Close()
	})
	return clientEndpoint, server.endpoint
}
