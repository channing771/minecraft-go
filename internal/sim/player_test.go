package sim_test

import (
	"math"
	"reflect"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/sim"
)

func TestRegisteredSessionRequestsAnchorBeforeClientInput(t *testing.T) {
	engine := sim.NewEngine(0)
	engine.RegisterSession(1, core.Overworld, core.ChunkPos{})
	result := engine.Step()
	want := []core.ChunkKey{{Dimension: core.Overworld, Pos: core.ChunkPos{}}}
	if !reflect.DeepEqual(result.Acquire, want) || len(result.Generate) != 0 {
		t.Fatalf("Acquire=%+v Generate=%+v，想要 acquire %+v", result.Acquire, result.Generate, want)
	}
	if len(result.Players) != 1 || result.Players[0].Ready {
		t.Fatalf("初始 player update=%+v", result.Players)
	}
	player := result.Players[0]
	if player.Dimension != core.Overworld || player.ViewCenter != (core.ChunkPos{}) ||
		player.Reset || !finiteVector(player.State.Position) ||
		player.State.Position != (mgl32.Vec3{0.5, core.MaxY + 1, 0.5}) {
		t.Fatalf("pending 占位状态=%+v", player)
	}
}

func TestPendingSpawnActivatesAtDeterministicSurface(t *testing.T) {
	engine := sim.NewEngine(0)
	engine.RegisterSession(1, core.Overworld, core.ChunkPos{})
	requested := engine.Step()
	submitAcquiredMisses(engine, requested.Acquire)
	engine.Step()
	engine.SubmitGenerated(sim.GeneratedChunk{
		Dimension: core.Overworld,
		Pos:       core.ChunkPos{},
		Chunk:     generateFlatChunk(core.ChunkPos{}),
	})
	result := engine.Step()
	player := onlyPlayer(t, result)
	if !player.Ready || !player.Reset ||
		player.State.Position != (mgl32.Vec3{0.5, 1, 0.5}) ||
		!player.State.OnGround {
		t.Fatalf("spawn=%+v", player)
	}
	if next := onlyPlayer(t, engine.Step()); next.Reset {
		t.Fatal("Reset 只能出现一次")
	}
}

func TestPlayerUpdatesSortBySession(t *testing.T) {
	engine := sim.NewEngine(0)
	engine.RegisterSession(9, core.Overworld, core.ChunkPos{})
	engine.RegisterSession(2, core.Overworld, core.ChunkPos{})

	players := engine.Step().Players
	if len(players) != 2 || players[0].Session != 2 || players[1].Session != 9 {
		t.Fatalf("Players 未按 session 排序: %+v", players)
	}
}

func TestPlayerReturnsCopyOfAuthoritativeSnapshot(t *testing.T) {
	engine := sim.NewEngine(0)
	if _, ok := engine.Player(1); ok {
		t.Fatal("未注册 session 返回了 player")
	}
	engine.RegisterSession(1, core.Overworld, core.ChunkPos{})

	first, ok := engine.Player(1)
	if !ok || first.Ready {
		t.Fatalf("注册后的 Player=(%+v,%v)", first, ok)
	}
	first.State.Position[0] = 99
	second, ok := engine.Player(1)
	if !ok || second.State.Position != (mgl32.Vec3{0.5, core.MaxY + 1, 0.5}) {
		t.Fatalf("修改返回值污染了权威状态: (%+v,%v)", second, ok)
	}
}

func TestRegisteredPlayerIgnoresTrustedObserverCenterCommands(t *testing.T) {
	cases := []struct {
		name      string
		dimension core.DimensionID
		center    core.ChunkPos
	}{
		{name: "known remote center", dimension: core.Overworld, center: core.ChunkPos{X: 8, Z: -3}},
		{name: "unknown dimension", dimension: core.DimensionID(99), center: core.ChunkPos{X: 2, Z: 4}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			engine := sim.NewEngine(0)
			engine.RegisterSession(1, core.Overworld, core.ChunkPos{})
			engine.Enqueue(sim.Command{
				Session:   1,
				Sequence:  1,
				Kind:      sim.CommandTrustedObserverCenter,
				Dimension: tc.dimension,
				Center:    tc.center,
			})

			result := engine.Step()
			wantAcquire := []core.ChunkKey{{Dimension: core.Overworld, Pos: core.ChunkPos{}}}
			if !reflect.DeepEqual(result.Acquire, wantAcquire) || len(result.Generate) != 0 {
				t.Fatalf("Acquire=%+v Generate=%+v，想要 anchor %+v", result.Acquire, result.Generate, wantAcquire)
			}
			player := onlyPlayer(t, result)
			if player.Dimension != core.Overworld || player.ViewCenter != (core.ChunkPos{}) {
				t.Fatalf("trusted observer 命令覆盖了玩家权威 anchor: %+v", player)
			}
		})
	}
}

func TestRegisterSessionRejectsDuplicateOrUnknownDimension(t *testing.T) {
	t.Run("duplicate", func(t *testing.T) {
		engine := sim.NewEngine(0)
		engine.RegisterSession(1, core.Overworld, core.ChunkPos{})
		assertPanics(t, "duplicate session", func() {
			engine.RegisterSession(1, core.Overworld, core.ChunkPos{})
		})
	})

	t.Run("unknown dimension", func(t *testing.T) {
		engine := sim.NewEngine(0)
		assertPanics(t, "unknown dimension", func() {
			engine.RegisterSession(1, core.DimensionID(99), core.ChunkPos{})
		})
	})
}

func onlyPlayer(t *testing.T, result sim.TickResult) sim.PlayerUpdate {
	t.Helper()
	if len(result.Players) != 1 {
		t.Fatalf("Players=%+v，想要恰好一个", result.Players)
	}
	return result.Players[0]
}

func finiteVector(vector mgl32.Vec3) bool {
	for _, component := range vector {
		if math.IsNaN(float64(component)) || math.IsInf(float64(component), 0) {
			return false
		}
	}
	return true
}
