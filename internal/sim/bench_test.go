package sim_test

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/sim"
)

func BenchmarkEngineStepIdle(b *testing.B) {
	engine := sim.NewEngine(flatBaseBlock, 0)
	b.ReportAllocs()
	for b.Loop() {
		engine.Step()
	}
}

func BenchmarkEngineStepPlayer(b *testing.B) {
	engine := sim.NewEngine(flatBaseBlock, 0)
	engine.RegisterSession(1, core.Overworld, core.ChunkPos{})
	engine.Step()
	engine.SubmitGenerated(sim.GeneratedChunk{
		Dimension: core.Overworld,
		Chunk:     generateFlatChunk(core.ChunkPos{}),
	})
	result := engine.Step()
	if len(result.Players) != 1 || !result.Players[0].Ready {
		b.Fatalf("玩家未 Ready: %+v", result.Players)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		engine.Step()
	}
}

func BenchmarkEngineStepBlockChanges(b *testing.B) {
	engine := sim.NewEngine(flatBaseBlock, 0)
	session := sim.SessionID(1)
	engine.Enqueue(sim.Command{
		Session:   session,
		Sequence:  1,
		Kind:      sim.CommandSetViewCenter,
		Dimension: core.Overworld,
		Center:    core.ChunkPos{},
	})
	engine.Step()
	engine.SubmitGenerated(sim.GeneratedChunk{
		Dimension: core.Overworld,
		Pos:       core.ChunkPos{},
		Chunk:     generateFlatChunk(core.ChunkPos{}),
	})
	engine.Step()

	sequence := uint64(2)
	placing := true
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		command := sim.Command{
			Session: session, Sequence: sequence,
			Dimension: core.Overworld,
			Origin:    mgl32.Vec3{0.5, 2.5, 0.5},
			Direction: mgl32.Vec3{0, -1, 0},
		}
		if placing {
			command.Kind = sim.CommandPlaceRay
			command.Block = core.StoneID
		} else {
			command.Kind = sim.CommandBreakRay
		}
		engine.Enqueue(command)
		engine.Step()
		sequence++
		placing = !placing
	}
}
