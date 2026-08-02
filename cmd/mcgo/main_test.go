//go:build darwin

package main

import (
	"errors"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/client"
	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/profile"
)

func TestParseMainOptionsRejectsRemoteLocalConflicts(t *testing.T) {
	for _, args := range [][]string{
		{"--connect", "127.0.0.1:25565", "--world", "worlds/demo"},
		{"--connect", "127.0.0.1:25565", "--benchmark", "--perf-output", "x.json"},
		{"--benchmark", "--perf-output", "x.json", "--name", "Chen"},
	} {
		if _, err := parseMainOptions(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestParseMainOptionsAllowsRemoteWithDefaultWorld(t *testing.T) {
	options, err := parseMainOptions([]string{"--connect", "127.0.0.1:25565"})
	if err != nil {
		t.Fatal(err)
	}
	if options.Application.Connect != "127.0.0.1:25565" || options.Application.WorldPath != "worlds/default" {
		t.Fatalf("options=%+v", options.Application)
	}
}

func TestRunWithDependenciesLoadsProfileOnceForLocalAndRemote(t *testing.T) {
	for _, args := range [][]string{nil, {"--connect", "127.0.0.1:25565"}} {
		t.Run("mode", func(t *testing.T) {
			loads := 0
			identity := network.Identity{PlayerID: core.PlayerID{1}, DisplayName: "Chen"}
			err := runWithDependencies(args, runDependencies{
				loadIdentity: func(requested *string) (network.Identity, error) {
					loads++
					return identity, nil
				},
				newApplication: func(options applicationOptions) (*application, error) {
					if options.Identity == nil || *options.Identity != identity {
						t.Fatalf("application identity=%+v", options.Identity)
					}
					return nil, errors.New("stop before window")
				},
			})
			if err == nil || loads != 1 {
				t.Fatalf("run error=%v profile loads=%d, want construction error and 1", err, loads)
			}
		})
	}
}

func TestRunWithDependenciesBypassesProfileForBenchmark(t *testing.T) {
	loads := 0
	err := runWithDependencies([]string{"--benchmark", "--perf-output", "x.json"}, runDependencies{
		loadIdentity: func(*string) (network.Identity, error) {
			loads++
			return network.Identity{}, nil
		},
		newApplication: func(options applicationOptions) (*application, error) {
			if options.Identity != nil {
				t.Fatalf("benchmark identity=%+v, want nil", options.Identity)
			}
			return nil, errors.New("stop before window")
		},
	})
	if err == nil || loads != 0 {
		t.Fatalf("run error=%v profile loads=%d, want construction error and 0", err, loads)
	}
}

func TestRunWithDependenciesPassesExplicitNameToProfile(t *testing.T) {
	name := "Chen"
	var got *string
	err := runWithDependencies([]string{"--name", name}, runDependencies{
		loadIdentity: func(requested *string) (network.Identity, error) {
			got = requested
			return network.Identity{}, nil
		},
		newApplication: func(applicationOptions) (*application, error) {
			return nil, errors.New("stop before window")
		},
	})
	if err == nil || got == nil || *got != name {
		t.Fatalf("run error=%v requested name=%v", err, got)
	}
}

func TestParseMainOptionsBenchmarkTransport(t *testing.T) {
	defaults, err := parseMainOptions([]string{"--benchmark", "--perf-output", "x.json"})
	if err != nil {
		t.Fatal(err)
	}
	if defaults.Application.BenchmarkTransport != "memory" {
		t.Fatalf("default benchmark transport=%q, want memory", defaults.Application.BenchmarkTransport)
	}
	tcp, err := parseMainOptions([]string{
		"--benchmark", "--benchmark-transport", "tcp", "--perf-output", "x.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if tcp.Application.BenchmarkTransport != "tcp" {
		t.Fatalf("TCP benchmark transport=%q", tcp.Application.BenchmarkTransport)
	}
	for _, args := range [][]string{
		{"--benchmark-transport", "tcp"},
		{"--benchmark", "--benchmark-transport", "udp", "--perf-output", "x.json"},
	} {
		if _, err := parseMainOptions(args); err == nil {
			t.Fatalf("accepted invalid benchmark transport args %v", args)
		}
	}
}

// Mutation killed: ignoring renderFrame's error makes the interactive loop
// continue after a glyph worker failure instead of returning it immediately.
func TestRunInteractivePropagatesRemoteGlyphError(t *testing.T) {
	wantErr := errors.New("interactive glyph worker failure")
	app, _ := newRemoteRenderApplication(t, &integrationGlyphSource{flushErr: wantErr})
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

var _ = profile.Options{}
