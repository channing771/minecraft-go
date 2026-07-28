package sim_test

import (
	"math"
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/sim"
)

func TestEngineBreakValidation(t *testing.T) {
	tests := []struct {
		name      string
		yaw       float32
		pitch     float32
		rejected  bool
		reason    sim.RejectReason
		wantBlock core.BlockID
		position  core.BlockPos
	}{
		{
			name:      "grass breaks to air",
			pitch:     -float32(math.Pi)/2 + 0.01,
			wantBlock: core.AirID,
			position:  core.BlockPos{X: 0, Y: 0, Z: 0},
		},
		{
			name:      "invalid look",
			pitch:     float32(math.Pi) / 2,
			rejected:  true,
			reason:    sim.RejectInvalidInput,
			wantBlock: core.GrassID,
			position:  core.BlockPos{X: 0, Y: 0, Z: 0},
		},
		{
			name:      "no target",
			pitch:     float32(math.Pi)/2 - 0.01,
			rejected:  true,
			reason:    sim.RejectNoTarget,
			wantBlock: core.GrassID,
			position:  core.BlockPos{X: 0, Y: 0, Z: 0},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			engine, session, chunkPos := readyFlatEngine(t)
			engine.Enqueue(sim.Command{
				Session: session, Sequence: 2, Kind: sim.CommandBreakBlock,
				Yaw: tc.yaw, Pitch: tc.pitch,
			})
			result := engine.Step()
			if tc.rejected {
				assertRejected(t, result, tc.reason)
			} else {
				if len(result.Rejected) != 0 || len(result.Changes) == 0 {
					t.Fatalf("成功命令结果 = %+v", result)
				}
			}
			chunk, _, ok := engine.CloneReadyChunk(core.ChunkKey{
				Dimension: core.Overworld,
				Pos:       chunkPos,
			})
			if !ok {
				t.Fatal("中心区块未 Ready")
			}
			x, _, z := tc.position.Local()
			if got := chunk.BlockAt(x, tc.position.Y, z); got != tc.wantBlock {
				t.Fatalf("block = %d，想要 %d", got, tc.wantBlock)
			}
		})
	}
}

func TestEnginePlaceValidationAndWhitelist(t *testing.T) {
	t.Run("invalid block", func(t *testing.T) {
		engine, session, _ := readyFlatEngine(t)
		engine.Enqueue(sim.Command{
			Session: session, Sequence: 2, Kind: sim.CommandPlaceBlock,
			Yaw: float32(math.Pi), Block: core.BarrierID,
		})
		assertRejected(t, engine.Step(), sim.RejectInvalidBlock)
	})

	t.Run("origin inside solid is occupied", func(t *testing.T) {
		engine, session, _ := readyFlatEngine(t)
		engine.Enqueue(sim.Command{
			Session: session, Sequence: 2, Kind: sim.CommandPlaceBlock,
			Pitch: -float32(math.Pi)/2 + 0.01, Block: core.StoneID,
		})
		assertRejected(t, engine.Step(), sim.RejectOccupied)
	})

	t.Run("stone dirt grass succeed", func(t *testing.T) {
		blocks := []core.BlockID{core.StoneID, core.DirtID, core.GrassID}
		for index, block := range blocks {
			engine, session, chunkPos := readyFlatEngine(t)
			engine.Enqueue(sim.Command{
				Session: session, Sequence: 2, Kind: sim.CommandPlaceBlock,
				Yaw: float32(math.Pi), Block: block,
			})
			result := engine.Step()
			if len(result.Rejected) != 0 {
				t.Fatalf("block[%d]=%d 合法放置被拒绝: %+v", index, block, result.Rejected)
			}
			chunk, _, _ := engine.CloneReadyChunk(core.ChunkKey{
				Dimension: core.Overworld,
				Pos:       chunkPos,
			})
			if got := chunk.BlockAt(0, 2, 4); got != block {
				t.Fatalf("block[%d] 放置结果 = %d，想要 %d", index, got, block)
			}
		}
	})
}

func assertRejected(
	t *testing.T,
	result sim.TickResult,
	want sim.RejectReason,
) {
	t.Helper()
	if len(result.Rejected) != 1 || result.Rejected[0].Reason != want {
		t.Fatalf("Rejected = %+v，想要 reason %v", result.Rejected, want)
	}
	if len(result.Changes) != 0 {
		t.Fatalf("被拒绝命令产生修改: %+v", result.Changes)
	}
}
