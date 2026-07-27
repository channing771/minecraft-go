package sim_test

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/sim"
)

func TestEngineBreakValidation(t *testing.T) {
	tests := []struct {
		name      string
		origin    mgl32.Vec3
		direction mgl32.Vec3
		rejected  bool
		reason    sim.RejectReason
		wantBlock core.BlockID
		position  core.BlockPos
	}{
		{
			name:      "stone breaks to air",
			origin:    mgl32.Vec3{0.5, -0.5, 0.5},
			direction: mgl32.Vec3{0, -1, 0},
			wantBlock: core.AirID,
			position:  core.BlockPos{X: 0, Y: -1, Z: 0},
		},
		{
			name:      "bedrock is protected",
			origin:    mgl32.Vec3{0.5, -63.5, 0.5},
			direction: mgl32.Vec3{0, -1, 0},
			rejected:  true,
			reason:    sim.RejectProtectedBlock,
			wantBlock: core.BedrockID,
			position:  core.BlockPos{X: 0, Y: core.MinY, Z: 0},
		},
		{
			name:      "invalid direction",
			origin:    mgl32.Vec3{0.5, 2.5, 0.5},
			direction: mgl32.Vec3{},
			rejected:  true,
			reason:    sim.RejectInvalidRay,
			wantBlock: core.GrassID,
			position:  core.BlockPos{X: 0, Y: 0, Z: 0},
		},
		{
			name:      "no target",
			origin:    mgl32.Vec3{0.5, 10.5, 0.5},
			direction: mgl32.Vec3{0, 1, 0},
			rejected:  true,
			reason:    sim.RejectNoTarget,
			wantBlock: core.GrassID,
			position:  core.BlockPos{X: 0, Y: 0, Z: 0},
		},
		{
			name:      "unloaded traversal",
			origin:    mgl32.Vec3{15.5, 10.5, 0.5},
			direction: mgl32.Vec3{1, 0, 0},
			rejected:  true,
			reason:    sim.RejectChunkNotReady,
			wantBlock: core.GrassID,
			position:  core.BlockPos{X: 0, Y: 0, Z: 0},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			engine, session, chunkPos := readyFlatEngine(t)
			engine.Enqueue(sim.Command{
				Session: session, Sequence: 2, Kind: sim.CommandBreakRay,
				Dimension: core.Overworld,
				Origin:    tc.origin,
				Direction: tc.direction,
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
			Session: session, Sequence: 2, Kind: sim.CommandPlaceRay,
			Dimension: core.Overworld,
			Origin:    mgl32.Vec3{0.5, 2.5, 0.5},
			Direction: mgl32.Vec3{0, -1, 0},
			Block:     core.BarrierID,
		})
		assertRejected(t, engine.Step(), sim.RejectInvalidBlock)
	})

	t.Run("origin inside solid is occupied", func(t *testing.T) {
		engine, session, _ := readyFlatEngine(t)
		engine.Enqueue(sim.Command{
			Session: session, Sequence: 2, Kind: sim.CommandPlaceRay,
			Dimension: core.Overworld,
			Origin:    mgl32.Vec3{0.5, 0.5, 0.5},
			Direction: mgl32.Vec3{0, -1, 0},
			Block:     core.StoneID,
		})
		assertRejected(t, engine.Step(), sim.RejectOccupied)
	})

	t.Run("stone dirt grass succeed", func(t *testing.T) {
		engine, session, chunkPos := readyFlatEngine(t)
		blocks := []core.BlockID{core.StoneID, core.DirtID, core.GrassID}
		for index, block := range blocks {
			engine.Enqueue(sim.Command{
				Session: session, Sequence: uint64(index + 2), Kind: sim.CommandPlaceRay,
				Dimension: core.Overworld,
				Origin:    mgl32.Vec3{float32(index) + 0.5, 2.5, 0.5},
				Direction: mgl32.Vec3{0, -1, 0},
				Block:     block,
			})
		}
		result := engine.Step()
		if len(result.Rejected) != 0 {
			t.Fatalf("合法放置被拒绝: %+v", result.Rejected)
		}
		chunk, _, _ := engine.CloneReadyChunk(core.ChunkKey{
			Dimension: core.Overworld,
			Pos:       chunkPos,
		})
		for index, want := range blocks {
			if got := chunk.BlockAt(index, 1, 0); got != want {
				t.Fatalf("x=%d block = %d，想要 %d", index, got, want)
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
