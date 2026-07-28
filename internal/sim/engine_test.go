package sim_test

import (
	"context"
	"math"
	"reflect"
	"testing"
	"time"

	"minecraft-go/internal/core"
	"minecraft-go/internal/sim"
)

func TestEngineSortsCommandsAndDeduplicatesSequence(t *testing.T) {
	engine, session, chunkPos := readyFlatEngine(t)
	yaw := float32(math.Pi)

	engine.Enqueue(sim.Command{
		Session: session, Sequence: 4, Kind: sim.CommandPlaceBlock,
		Yaw: yaw, Block: core.DirtID,
	})
	engine.Enqueue(sim.Command{
		Session: session, Sequence: 2, Kind: sim.CommandPlaceBlock,
		Yaw: yaw, Block: core.StoneID,
	})
	engine.Enqueue(sim.Command{
		Session: session, Sequence: 3, Kind: sim.CommandBreakBlock,
		Yaw: yaw,
	})
	result := engine.Step()
	if len(result.Rejected) != 0 {
		t.Fatalf("命令被拒绝: %+v", result.Rejected)
	}
	if len(result.Changes) != 1 || len(result.Changes[0].Changes) != 1 {
		t.Fatalf("Changes = %+v", result.Changes)
	}

	chunk, revision, ok := engine.CloneReadyChunk(core.ChunkKey{
		Dimension: core.Overworld,
		Pos:       chunkPos,
	})
	if !ok || revision != 2 {
		t.Fatalf("CloneReadyChunk revision = %d, ok=%v", revision, ok)
	}
	if got := chunk.BlockAt(0, 2, 4); got != core.DirtID {
		t.Fatalf("最终 block = %d，想要 dirt", got)
	}

	engine.Enqueue(sim.Command{
		Session: session, Sequence: 4, Kind: sim.CommandBreakBlock,
		Yaw: yaw,
	})
	duplicate := engine.Step()
	if len(duplicate.Changes) != 0 || len(duplicate.Rejected) != 0 {
		t.Fatalf("重复 sequence 产生了结果: %+v", duplicate)
	}
	_, revision, _ = engine.CloneReadyChunk(core.ChunkKey{
		Dimension: core.Overworld,
		Pos:       chunkPos,
	})
	if revision != 2 {
		t.Fatalf("重复 sequence 把 revision 改为 %d", revision)
	}
}

func TestEngineBatchesChunkRevisionOncePerTick(t *testing.T) {
	engine, session, chunkPos := readyFlatEngine(t)
	for sequence := uint64(2); sequence <= 4; sequence++ {
		engine.Enqueue(sim.Command{
			Session: session, Sequence: sequence, Kind: sim.CommandPlaceBlock,
			Yaw: float32(math.Pi), Block: core.StoneID,
		})
	}
	result := engine.Step()
	if len(result.Changes) != 1 {
		t.Fatalf("change batches = %d，想要 1", len(result.Changes))
	}
	batch := result.Changes[0]
	if batch.BaseRevision != 1 || batch.NewRevision != 2 {
		t.Fatalf("revision = %d→%d，想要 1→2", batch.BaseRevision, batch.NewRevision)
	}
	if len(batch.Changes) != 3 {
		t.Fatalf("changes = %+v", batch.Changes)
	}
	wantPositions := []core.BlockPos{
		{X: 0, Y: 2, Z: 2},
		{X: 0, Y: 2, Z: 3},
		{X: 0, Y: 2, Z: 4},
	}
	for index, change := range batch.Changes {
		if change.Position != wantPositions[index] {
			t.Fatalf("changes 未按 block index 排序: %+v", batch.Changes)
		}
	}
	_, revision, ok := engine.CloneReadyChunk(core.ChunkKey{
		Dimension: core.Overworld,
		Pos:       chunkPos,
	})
	if !ok || revision != 2 {
		t.Fatalf("authoritative revision = %d, ok=%v", revision, ok)
	}
}

func TestEngineReplayIsDeterministic(t *testing.T) {
	type replayState struct {
		hash     [32]byte
		revision uint64
		tick     uint64
	}
	run := func() replayState {
		engine, session, chunkPos := readyFlatEngine(t)
		engine.Enqueue(sim.Command{
			Session: session, Sequence: 2, Kind: sim.CommandPlaceBlock,
			Yaw: float32(math.Pi), Block: core.GrassID,
		})
		engine.Step()
		hash, revision, ok := engine.ChunkHash(core.ChunkKey{
			Dimension: core.Overworld,
			Pos:       chunkPos,
		})
		if !ok {
			t.Fatal("权威区块 hash 不可用")
		}
		return replayState{
			hash:     hash,
			revision: revision,
			tick:     engine.TickCount(),
		}
	}
	if first, second := run(), run(); !reflect.DeepEqual(first, second) {
		t.Fatalf("两次 replay 不同: %v != %v", first, second)
	}
}

func TestPlayerCommandsRejectRegisteredPendingPlayer(t *testing.T) {
	tests := []struct {
		name    string
		command sim.Command
	}{
		{
			name: "movement input",
			command: sim.Command{
				Kind: sim.CommandPlayerInput, MoveX: 1,
			},
		},
		{
			name: "interaction",
			command: sim.Command{
				Kind: sim.CommandBreakBlock,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			engine := sim.NewEngine(0)
			const session = sim.SessionID(1)
			engine.RegisterSession(session, core.Overworld, core.ChunkPos{})
			command := tc.command
			command.Session = session
			command.Sequence = 1
			engine.Enqueue(command)

			result := engine.Step()
			if len(result.Rejected) != 1 || result.Rejected[0] != (sim.Rejection{
				Session: session, Sequence: 1, Reason: sim.RejectPlayerNotReady,
			}) {
				t.Fatalf("Rejected=%+v", result.Rejected)
			}
			if player := onlyPlayer(t, result); player.LastInputSequence != 0 {
				t.Fatalf("PendingSpawn input 被错误确认: %+v", player)
			}
		})
	}
}

func TestPendingInteractionStaysRejectedWhenPlayerActivatesSameTick(t *testing.T) {
	engine := sim.NewEngine(0)
	const session = sim.SessionID(1)
	engine.RegisterSession(session, core.Overworld, core.ChunkPos{})
	engine.Step()
	engine.SubmitGenerated(sim.GeneratedChunk{
		Dimension: core.Overworld,
		Chunk:     generateFlatChunk(core.ChunkPos{}),
	})
	engine.Enqueue(sim.Command{
		Session: session, Sequence: 1, Kind: sim.CommandBreakBlock,
	})

	result := engine.Step()
	if len(result.Rejected) != 1 || result.Rejected[0].Reason != sim.RejectPlayerNotReady ||
		len(result.Changes) != 0 {
		t.Fatalf("PendingSpawn 期间摄取的交互在激活后执行: %+v", result)
	}
}

func TestEngineRunConsumesClockAndStopsIt(t *testing.T) {
	engine := sim.NewEngine(0)
	clock := &oneTickClock{
		ticks:   make(chan time.Time, 1),
		stopped: make(chan struct{}),
	}
	clock.ticks <- time.Now()
	close(clock.ticks)

	if err := engine.Run(context.Background(), clock); err != nil {
		t.Fatal(err)
	}
	if got := engine.TickCount(); got != 1 {
		t.Fatalf("TickCount = %d，想要 1", got)
	}
	select {
	case <-clock.stopped:
	default:
		t.Fatal("Run 没有 Stop clock")
	}
}

type oneTickClock struct {
	ticks   chan time.Time
	stopped chan struct{}
}

func (clock *oneTickClock) C() <-chan time.Time {
	return clock.ticks
}

func (clock *oneTickClock) Stop() {
	close(clock.stopped)
}

func readyFlatEngine(t *testing.T) (*sim.Engine, sim.SessionID, core.ChunkPos) {
	t.Helper()
	engine := sim.NewEngine(0)
	session := sim.SessionID(1)
	chunkPos := core.ChunkPos{}
	engine.RegisterSession(session, core.Overworld, chunkPos)
	requested := engine.Step()
	wantKey := core.ChunkKey{Dimension: core.Overworld, Pos: chunkPos}
	if !reflect.DeepEqual(requested.Generate, []core.ChunkKey{wantKey}) {
		t.Fatalf("Generate = %+v，想要 %+v", requested.Generate, wantKey)
	}
	chunk := generateFlatChunk(chunkPos)
	chunk.SetBlock(0, 2, 5, core.StoneID)
	engine.SubmitGenerated(sim.GeneratedChunk{
		Dimension: core.Overworld,
		Pos:       chunkPos,
		Chunk:     chunk,
	})
	ready := engine.Step()
	if !reflect.DeepEqual(ready.Ready, []core.ChunkKey{wantKey}) ||
		len(ready.Players) != 1 || !ready.Players[0].Ready {
		t.Fatalf("Ready = %+v Players=%+v，想要 %+v 与 active player", ready.Ready, ready.Players, wantKey)
	}
	return engine, session, chunkPos
}
