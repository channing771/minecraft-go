package sim_test

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/world"
)

func TestBreakBlockUsesAuthoritativeEye(t *testing.T) {
	engine, session := readyFlatPlayer(t)
	engine.Enqueue(sim.Command{
		Session: session, Sequence: 2, Kind: sim.CommandBreakBlock,
		Yaw: 0, Pitch: -float32(math.Pi)/2 + 0.01,
	})

	result := engine.Step()
	if len(result.Rejected) != 0 || len(result.Changes) != 1 ||
		len(result.Changes[0].Changes) != 1 ||
		result.Changes[0].Changes[0].Position != (core.BlockPos{X: 0, Y: 0, Z: 0}) {
		t.Fatalf("权威眼睛射线 result=%+v", result)
	}
}

func TestPlaceBlockRejectsCompletePlayerAABBOverlap(t *testing.T) {
	var stocked core.Hotbar
	stocked.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 1}
	engine, session := readyFlatPlayerRestored(t, nil, stocked)
	engine.Enqueue(sim.Command{
		Session: session, Sequence: 2, Kind: sim.CommandPlaceBlock,
		Yaw: 0, Pitch: -float32(math.Pi)/2 + 0.01,
		Slot: 0,
	})

	result := engine.Step()
	if len(result.Rejected) != 1 || result.Rejected[0].Reason != sim.RejectOccupied ||
		len(result.Changes) != 0 {
		t.Fatalf("result=%+v", result)
	}
}

func TestPlayerInteractionRejectsPendingSpawn(t *testing.T) {
	engine := sim.NewEngine(0)
	const session = sim.SessionID(1)
	engine.RegisterSession(session, core.Overworld, core.ChunkPos{})
	engine.Enqueue(sim.Command{
		Session: session, Sequence: 1, Kind: sim.CommandBreakBlock,
		Pitch: -float32(math.Pi)/2 + 0.01,
	})

	result := engine.Step()
	if len(result.Rejected) != 1 || result.Rejected[0] != (sim.Rejection{
		Session: session, Sequence: 1, Reason: sim.RejectPlayerNotReady,
	}) || len(result.Changes) != 0 {
		t.Fatalf("PendingSpawn result=%+v", result)
	}
}

func TestPlayerInteractionInvalidLookDoesNotChangeHeldMovement(t *testing.T) {
	engine, session := readyFlatPlayer(t)
	engine.Enqueue(sim.Command{
		Session: session, Sequence: 1, Kind: sim.CommandPlayerInput,
		MoveZ: 1, Yaw: 0,
	})
	engine.Enqueue(sim.Command{
		Session: session, Sequence: 2, Kind: sim.CommandBreakBlock,
		Yaw: float32(math.Pi) / 2, Pitch: float32(math.Pi) / 2,
	})

	result := engine.Step()
	if len(result.Rejected) != 1 || result.Rejected[0].Reason != sim.RejectInvalidInput ||
		len(result.Changes) != 0 {
		t.Fatalf("invalid look result=%+v", result)
	}
	player := onlyPlayer(t, result)
	if player.Yaw != 0 || player.Pitch != 0 || player.State.Position.X() != 0.5 ||
		player.State.Position.Z() >= 0.5 {
		t.Fatalf("非法 action look 改写权威 look/held movement: %+v", player)
	}
}

func TestPlayerInteractionValidLookAffectsSameTickMovement(t *testing.T) {
	engine, session := readyFlatPlayer(t)
	engine.Enqueue(sim.Command{
		Session: session, Sequence: 1, Kind: sim.CommandPlayerInput,
		MoveZ: 1, Yaw: float32(math.Pi) / 2,
	})
	engine.Enqueue(sim.Command{
		Session: session, Sequence: 2, Kind: sim.CommandBreakBlock,
		Yaw: 0, Pitch: -float32(math.Pi)/2 + 0.01,
	})

	result := engine.Step()
	if len(result.Rejected) != 0 || len(result.Changes) != 1 {
		t.Fatalf("valid look result=%+v", result)
	}
	player := onlyPlayer(t, result)
	if player.Yaw != 0 || player.Pitch != -float32(math.Pi)/2+0.01 ||
		player.LastInputSequence != 1 || player.State.Position.X() != 0.5 ||
		player.State.Position.Z() >= 0.5 {
		t.Fatalf("action look 没有在 physics 前更新 held yaw: %+v", player)
	}
}

func TestPlayerInteractionUsesSixBlockReach(t *testing.T) {
	tests := []struct {
		name       string
		targetZ    int32
		wantReject bool
	}{
		{name: "target within six", targetZ: 6},
		{name: "target beyond six", targetZ: 7, wantReject: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			position := core.BlockPos{X: 0, Y: 2, Z: tc.targetZ}
			engine, session := readyFlatPlayerWithTarget(t, map[core.BlockPos]core.BlockID{
				position: core.StoneID,
			})
			engine.Enqueue(sim.Command{
				Session: session, Sequence: 2, Kind: sim.CommandBreakBlock,
				Yaw: float32(math.Pi), Pitch: 0,
			})

			result := engine.Step()
			if tc.wantReject {
				if len(result.Rejected) != 1 || result.Rejected[0].Reason != sim.RejectNoTarget ||
					len(result.Changes) != 0 {
					t.Fatalf("六格外 result=%+v", result)
				}
				return
			}
			if len(result.Rejected) != 0 || len(result.Changes) != 1 ||
				result.Changes[0].Changes[0].Position != position {
				t.Fatalf("六格内 result=%+v", result)
			}
		})
	}
}

func TestPlayerInteractionProtectsBedrock(t *testing.T) {
	position := core.BlockPos{X: 0, Y: 0, Z: 0}
	engine, session := readyFlatPlayerWithTarget(t, map[core.BlockPos]core.BlockID{
		position: core.BedrockID,
	})
	engine.Enqueue(sim.Command{
		Session: session, Sequence: 2, Kind: sim.CommandBreakBlock,
		Pitch: -float32(math.Pi)/2 + 0.01,
	})

	result := engine.Step()
	if len(result.Rejected) != 1 || result.Rejected[0].Reason != sim.RejectProtectedBlock ||
		len(result.Changes) != 0 {
		t.Fatalf("bedrock result=%+v", result)
	}
}

func TestPlayerInteractionPlacesAdjacentBlock(t *testing.T) {
	target := core.BlockPos{X: 0, Y: 2, Z: 3}
	want := core.BlockPos{X: 0, Y: 2, Z: 2}
	var stocked core.Hotbar
	stocked.Slots[0] = core.ItemStack{Item: core.ItemDirt, Count: 1}
	engine, session := readyFlatPlayerRestored(t, map[core.BlockPos]core.BlockID{
		target: core.StoneID,
	}, stocked)
	engine.Enqueue(sim.Command{
		Session: session, Sequence: 2, Kind: sim.CommandPlaceBlock,
		Yaw: float32(math.Pi), Slot: 0,
	})

	result := engine.Step()
	if len(result.Rejected) != 0 || len(result.Changes) != 1 ||
		result.Changes[0].Changes[0] != (sim.BlockChange{Position: want, Block: core.DirtID}) {
		t.Fatalf("valid place result=%+v", result)
	}
}

func TestPlayerInteractionActionsObserveSequenceOrder(t *testing.T) {
	var stocked core.Hotbar
	stocked.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 1}
	engine, session := readyFlatPlayerRestored(t, nil, stocked)
	pitch := -float32(math.Pi)/2 + 0.01
	engine.Enqueue(sim.Command{
		Session: session, Sequence: 3, Kind: sim.CommandPlaceBlock,
		Pitch: pitch, Slot: 0,
	})
	engine.Enqueue(sim.Command{
		Session: session, Sequence: 2, Kind: sim.CommandBreakBlock,
		Pitch: pitch,
	})

	result := engine.Step()
	want := sim.BlockChange{Position: core.BlockPos{X: 0, Y: 0, Z: 0}, Block: core.StoneID}
	if len(result.Rejected) != 0 || len(result.Changes) != 1 ||
		len(result.Changes[0].Changes) != 1 || result.Changes[0].Changes[0] != want {
		t.Fatalf("same-tick sequence result=%+v", result)
	}
}

func readyFlatPlayer(t *testing.T) (*sim.Engine, sim.SessionID) {
	t.Helper()
	return readyFlatPlayerWithTarget(t, nil)
}

func readyFlatPlayerWithTarget(
	t *testing.T,
	blocks map[core.BlockPos]core.BlockID,
) (*sim.Engine, sim.SessionID) {
	t.Helper()
	return readyFlatPlayerRestored(t, blocks, core.Hotbar{})
}

func readyFlatPlayerRestored(
	t *testing.T,
	blocks map[core.BlockPos]core.BlockID,
	hotbar core.Hotbar,
) (*sim.Engine, sim.SessionID) {
	t.Helper()
	return readyFlatPlayerInventory(t, blocks, core.Inventory{Hotbar: hotbar})
}

// readyFlatPlayerInventoryWithBlocks 用完整物品状态和额外方块构造已 Ready 的玩家。
func readyFlatPlayerInventoryWithBlocks(
	t *testing.T,
	blocks map[core.BlockPos]core.BlockID,
	inventory core.Inventory,
) (*sim.Engine, sim.SessionID) {
	t.Helper()
	return readyFlatPlayerInventory(t, blocks, inventory)
}

// readyFlatPlayerWithInventory 用完整物品状态构造一个已 Ready 的平坦世界玩家。
func readyFlatPlayerWithInventory(
	t *testing.T,
	inventory core.Inventory,
) (*sim.Engine, sim.SessionID) {
	t.Helper()
	return readyFlatPlayerInventory(t, nil, inventory)
}

func readyFlatPlayerInventory(
	t *testing.T,
	blocks map[core.BlockPos]core.BlockID,
	inventory core.Inventory,
) (*sim.Engine, sim.SessionID) {
	t.Helper()
	engine := sim.NewEngine(0)
	const session = sim.SessionID(1)
	engine.RegisterPlayer(session, sim.PlayerRestore{
		SpawnDimension: core.Overworld,
		SpawnAnchor:    core.ChunkPos{},
		Inventory:      inventory,
	})
	requested := engine.Step()
	if len(requested.Acquire) != 1 || requested.Acquire[0] != (core.ChunkKey{
		Dimension: core.Overworld,
	}) {
		t.Fatalf("Acquire=%+v", requested.Acquire)
	}
	submitAcquiredMisses(engine, requested.Acquire)
	engine.Step()
	chunk := generateFlatChunk(core.ChunkPos{})
	setTestBlocks(t, chunk, blocks)
	engine.SubmitGenerated(sim.GeneratedChunk{
		Dimension: core.Overworld,
		Chunk:     chunk,
	})
	spawned := onlyPlayer(t, engine.Step())
	if !spawned.Ready || spawned.State.Position != (mgl32.Vec3{0.5, 1, 0.5}) {
		t.Fatalf("spawned=%+v", spawned)
	}
	return engine, session
}

func setTestBlocks(
	t *testing.T,
	chunk *world.Chunk,
	blocks map[core.BlockPos]core.BlockID,
) {
	t.Helper()
	for position, block := range blocks {
		if position.Chunk() != chunk.Pos {
			t.Fatalf("测试方块 %+v 不在区块 %+v", position, chunk.Pos)
		}
		x, _, z := position.Local()
		chunk.SetBlock(x, position.Y, z, block)
	}
}
