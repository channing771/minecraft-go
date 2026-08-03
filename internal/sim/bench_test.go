package sim_test

import (
	"math"
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/world"
)

func BenchmarkEngineStepIdle(b *testing.B) {
	engine := sim.NewEngine(0)
	b.ReportAllocs()
	for b.Loop() {
		engine.Step()
	}
}

func BenchmarkEngineStepPlayer(b *testing.B) {
	engine := sim.NewEngine(0)
	engine.RegisterSession(1, core.Overworld, core.ChunkPos{})
	requested := engine.Step()
	submitAcquiredMisses(engine, requested.Acquire)
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
	engine := sim.NewEngine(0)
	session := sim.SessionID(1)
	engine.RegisterSession(session, core.Overworld, core.ChunkPos{})
	requested := engine.Step()
	submitAcquiredMisses(engine, requested.Acquire)
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
	// 先挖掘再放置，使放置总能从栏位 0 取到刚采集的物品。
	placing := false
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		command := sim.Command{
			Session: session, Sequence: sequence, Yaw: float32(math.Pi),
		}
		if placing {
			command.Kind = sim.CommandPlaceBlock
			command.Slot = 0
		} else {
			command.Kind = sim.CommandBreakBlock
		}
		engine.Enqueue(command)
		engine.Step()
		sequence++
		placing = !placing
	}
}

// BenchmarkItemDropWorstCaseScan 覆盖 8 名玩家 × 25 区块 × 32 槽的最坏合法扫描。
func BenchmarkItemDropWorstCaseScan(b *testing.B) {
	engine := sim.NewEngine(sim.DropInterestRadius)
	const session = sim.SessionID(1)
	engine.RegisterSession(session, core.Overworld, core.ChunkPos{})
	for range 8 {
		result := engine.Step()
		submitAcquiredMisses(engine, result.Acquire)
		for _, key := range result.Generate {
			engine.SubmitGenerated(sim.GeneratedChunk{
				Dimension: key.Dimension, Pos: key.Pos, Chunk: generateFlatChunk(key.Pos),
			})
		}
	}
	index, ok := world.ChunkBlockIndex(core.BlockPos{X: 8, Y: 0, Z: 8})
	if !ok {
		b.Fatal("测试方块没有区块索引")
	}
	for dx := int32(-sim.DropInterestRadius); dx <= sim.DropInterestRadius; dx++ {
		for dz := int32(-sim.DropInterestRadius); dz <= sim.DropInterestRadius; dz++ {
			key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: dx, Z: dz}}
			for slot := range core.DropsPerChunk {
				engine.SetChunkDropForTest(key, slot, world.DropSlot{
					Generation: 1, Active: true,
					Stack:      core.ItemStack{Item: core.ItemStone, Count: 1},
					BlockIndex: index, PickupDelayTicks: 200,
				})
			}
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		engine.Step()
	}
}

// BenchmarkItemDropSessionSnapshot 覆盖单会话 800 项快照的复用路径。
func BenchmarkItemDropSessionSnapshot(b *testing.B) {
	engine := sim.NewEngine(sim.DropInterestRadius)
	const session = sim.SessionID(1)
	engine.RegisterSession(session, core.Overworld, core.ChunkPos{})
	for range 8 {
		result := engine.Step()
		submitAcquiredMisses(engine, result.Acquire)
		for _, key := range result.Generate {
			engine.SubmitGenerated(sim.GeneratedChunk{
				Dimension: key.Dimension, Pos: key.Pos, Chunk: generateFlatChunk(key.Pos),
			})
		}
	}
	snapshot := make([]sim.DropSnapshot, 0, core.MaxSessionDrops)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		snapshot = engine.AppendSessionDrops(session, snapshot[:0])
	}
}
