package sim_test

import (
	"math"
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/sim"
)

func TestEngineMiningValidation(t *testing.T) {
	tests := []struct {
		name      string
		yaw       float32
		pitch     float32
		rejected  bool
		reason    sim.RejectReason
		wantBlock core.BlockID
		position  core.BlockPos
		ticks     int
		changed   bool
	}{
		{
			name:      "grass breaks to air",
			pitch:     -float32(math.Pi)/2 + 0.01,
			wantBlock: core.AirID,
			position:  core.BlockPos{X: 0, Y: 0, Z: 0},
			ticks:     5,
			changed:   true,
		},
		{
			name:      "invalid look",
			pitch:     float32(math.Pi) / 2,
			rejected:  true,
			reason:    sim.RejectInvalidInput,
			wantBlock: core.GrassID,
			position:  core.BlockPos{X: 0, Y: 0, Z: 0},
			ticks:     1,
		},
		{
			name:      "no target",
			pitch:     float32(math.Pi)/2 - 0.01,
			wantBlock: core.GrassID,
			position:  core.BlockPos{X: 0, Y: 0, Z: 0},
			ticks:     1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			engine, session, chunkPos := readyFlatEngine(t)
			sequence := uint64(1)
			result := mineUntilComplete(t, engine, session, &sequence, tc.yaw, tc.pitch, tc.ticks)
			if tc.rejected {
				assertRejected(t, result, tc.reason)
			} else {
				if len(result.Rejected) != 0 || (len(result.Changes) != 0) != tc.changed {
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
	t.Run("empty slot", func(t *testing.T) {
		engine, session, _ := readyFlatEngine(t)
		engine.Enqueue(sim.Command{
			Session: session, Sequence: 2, Kind: sim.CommandPlaceBlock,
			Yaw: float32(math.Pi), Slot: 0,
		})
		assertRejected(t, engine.Step(), sim.RejectInvalidBlock)
	})

	t.Run("origin inside solid is occupied", func(t *testing.T) {
		engine, session, _ := readyFlatEngineStocked(t, stockedHotbar(core.ItemStone))
		engine.Enqueue(sim.Command{
			Session: session, Sequence: 2, Kind: sim.CommandPlaceBlock,
			Pitch: -float32(math.Pi)/2 + 0.01, Slot: 0,
		})
		assertRejected(t, engine.Step(), sim.RejectOccupied)
	})

	t.Run("stone dirt grass succeed", func(t *testing.T) {
		items := []core.ItemID{core.ItemStone, core.ItemDirt, core.ItemGrass}
		for index, item := range items {
			block, _ := core.ItemPlacement(item)
			engine, session, chunkPos := readyFlatEngineStocked(t, stockedHotbar(item))
			engine.Enqueue(sim.Command{
				Session: session, Sequence: 2, Kind: sim.CommandPlaceBlock,
				Yaw: float32(math.Pi), Slot: 0,
			})
			result := engine.Step()
			if len(result.Rejected) != 0 {
				t.Fatalf("item[%d]=%d 合法放置被拒绝: %+v", index, item, result.Rejected)
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

func TestPlayerIntentDoesNotModifyReadyChunkWhenRayEntersUnknownChunk(t *testing.T) {
	tests := []struct {
		name       string
		command    sim.Command
		wantReject bool
	}{
		{name: "mine", command: sim.Command{Kind: sim.CommandPlayerInput, Mining: true}},
		{name: "place", command: sim.Command{Kind: sim.CommandPlaceBlock, Slot: 0}, wantReject: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine := sim.NewEngine(0, 0)
			const session = sim.SessionID(1)
			anchor := core.ChunkPos{X: 1}
			engine.RegisterPlayer(session, sim.PlayerRestore{
				SpawnDimension: core.Overworld,
				SpawnAnchor:    anchor,
				Inventory:      core.Inventory{Hotbar: stockedHotbar(core.ItemStone)},
			})
			requested := engine.Step()
			if len(requested.Acquire) != 1 || requested.Acquire[0] != (core.ChunkKey{
				Dimension: core.Overworld,
				Pos:       anchor,
			}) {
				t.Fatalf("Acquire=%+v", requested.Acquire)
			}
			submitAcquiredMisses(engine, requested.Acquire)
			engine.Step()
			engine.SubmitGenerated(sim.GeneratedChunk{
				Dimension: core.Overworld,
				Pos:       anchor,
				Chunk:     generateFlatChunk(anchor),
			})
			ready := engine.Step()
			if player := onlyPlayer(t, ready); !player.Ready {
				t.Fatalf("玩家未在边界 chunk 激活: %+v", player)
			}
			beforeHash, beforeRevision, ok := engine.ChunkHash(core.ChunkKey{
				Dimension: core.Overworld,
				Pos:       anchor,
			})
			if !ok {
				t.Fatal("边界 chunk hash 不可用")
			}

			command := test.command
			command.Session = session
			command.Sequence = 2
			command.Yaw = float32(math.Pi) / 2
			engine.Enqueue(command)
			result := engine.Step()
			if test.wantReject {
				assertRejected(t, result, sim.RejectChunkNotReady)
			} else if len(result.Rejected) != 0 || onlyPlayer(t, result).Mining.Active {
				t.Fatalf("未知 chunk 采掘没有正常清零: %+v", result)
			}

			afterHash, afterRevision, ok := engine.ChunkHash(core.ChunkKey{
				Dimension: core.Overworld,
				Pos:       anchor,
			})
			if !ok || afterHash != beforeHash || afterRevision != beforeRevision {
				t.Fatalf("未知 chunk 交互修改已 Ready chunk: hash=%x→%x revision=%d→%d ok=%v",
					beforeHash, afterHash, beforeRevision, afterRevision, ok)
			}
		})
	}
}

// stockedHotbar 返回栏位 0 装满该物品的快捷栏。
// TestInteractionReachTunableGatesPlacement 证明 InteractionReach 确实经快照送达
// 权威射线判定，而不是停在编译期默认值上。
//
// readyFlatEngineStocked 摆的石块在射线方向约 5 格外：默认交互距离 6 能够到，
// 收到 2 格就必须变成"没有目标"。把 engine.tunables.InteractionReach 改回
// defaultInteractionReach 时，其余 sim 测试无一会变红，只有这一条会。
func TestInteractionReachTunableGatesPlacement(t *testing.T) {
	t.Cleanup(func() { sim.SetTunables(sim.DefaultTunables()) })

	place := func(t *testing.T, reach float32) sim.TickResult {
		t.Helper()
		tunables := sim.DefaultTunables()
		tunables.InteractionReach = reach
		sim.SetTunables(tunables)
		engine, session, _ := readyFlatEngineStocked(t, stockedHotbar(core.ItemStone))
		engine.Enqueue(sim.Command{
			Session: session, Sequence: 2, Kind: sim.CommandPlaceBlock,
			Yaw: float32(math.Pi), Slot: 0,
		})
		return engine.Step()
	}

	if result := place(t, sim.DefaultTunables().InteractionReach); len(result.Rejected) != 0 {
		t.Fatalf("默认交互距离下的合法放置被拒绝: %+v", result.Rejected)
	}
	assertRejected(t, place(t, 2), sim.RejectNoTarget)
}

func stockedHotbar(item core.ItemID) core.Hotbar {
	var hotbar core.Hotbar
	hotbar.Slots[0] = core.ItemStack{Item: item, Count: core.MaxStackCount}
	return hotbar
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
