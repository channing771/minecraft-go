package sim_test

import (
	"math"
	"testing"

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
	engine.RegisterSession(session, core.Overworld, core.ChunkPos{})
	engine.Step()
	chunk := generateFlatChunk(core.ChunkPos{})
	chunk.SetBlock(0, 2, 5, core.StoneID)
	engine.SubmitGenerated(sim.GeneratedChunk{
		Dimension: core.Overworld,
		Pos:       core.ChunkPos{},
		Chunk:     chunk,
	})
	result := engine.Step()
	if len(result.Players) != 1 || !result.Players[0].Ready {
		b.Fatalf("玩家未 Ready: %+v", result.Players)
	}

	sequence := uint64(1)
	placing := true
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		command := sim.Command{
			Session: session, Sequence: sequence, Yaw: float32(math.Pi),
		}
		if placing {
			command.Kind = sim.CommandPlaceBlock
			command.Block = core.StoneID
		} else {
			command.Kind = sim.CommandBreakBlock
		}
		engine.Enqueue(command)
		engine.Step()
		sequence++
		placing = !placing
	}
}
