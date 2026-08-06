package sim

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

// 这些守卫覆盖公开玩法无法正常构造的状态：脚底区块不可用、玩家未 Active。
// 它们必须在任何写入之前失败，且不留下部分生效的转移。

func TestDropSelectedItemRejectsUnavailableFootChunkAtomically(t *testing.T) {
	engine, session := readyMovementPlayer(t)
	state := engine.sessions[session]
	state.player.inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 2}
	state.player.inventory.Hotbar.Selected = 0
	// 脚底落在尚未加载的相邻区块。直接调用权威转移而不经过 Step，
	// 避免玩家重生流程的副作用掩盖本次拒绝的原子性。
	state.player.state.Position = mgl32.Vec3{16.5, 1, 0.5}
	before := state.player.inventory
	dirtyBefore := state.player.inventoryDirty
	pending := make(map[core.ChunkKey]*pendingChunkChanges)

	reason, rejected := engine.dropSelectedItem(state, pending)

	if !rejected || reason != RejectChunkNotReady {
		t.Fatalf("拒绝 = (%v,%v)，想要 chunk_not_ready", reason, rejected)
	}
	if state.player.inventory != before {
		t.Fatalf("拒绝后背包被修改：%+v", state.player.inventory)
	}
	if state.player.inventoryDirty != dirtyBefore {
		t.Fatal("拒绝路径改变了 inventoryDirty")
	}
	if len(pending) != 0 {
		t.Fatalf("拒绝后产生了待提交区块变更：%+v", pending)
	}
}

func TestDropSelectedItemRejectsInactivePlayer(t *testing.T) {
	engine, session := readyMovementPlayer(t)
	state := engine.sessions[session]
	state.player.inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 1}
	state.player.lifecycle = PlayerPendingSpawn
	before := state.player.inventory
	pending := make(map[core.ChunkKey]*pendingChunkChanges)

	reason, rejected := engine.dropSelectedItem(state, pending)

	if !rejected || reason != RejectPlayerNotReady {
		t.Fatalf("拒绝 = (%v,%v)，想要 player_not_ready", reason, rejected)
	}
	if state.player.inventory != before {
		t.Fatalf("拒绝后背包被修改：%+v", state.player.inventory)
	}
	if len(pending) != 0 {
		t.Fatalf("拒绝后产生了待提交区块变更：%+v", pending)
	}
}

func TestDropSelectedItemRejectsFullDropCapacityAtomically(t *testing.T) {
	engine, session := readyMovementPlayer(t)
	state := engine.sessions[session]
	state.player.inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 2}
	state.player.inventory.Hotbar.Selected = 0
	chunk := engine.dimensions[state.dimension].records[core.ChunkPos{}].Chunk
	// 用满堆且物品不匹配的槽占满 32 个容量：既不能合并也不能新建。
	for slot := range core.DropsPerChunk {
		chunk.SetDrop(slot, world.DropSlot{
			Active:     true,
			Stack:      core.ItemStack{Item: core.ItemDirt, Count: core.MaxStackCount},
			BlockIndex: uint32(slot),
		})
	}
	before := state.player.inventory
	pending := make(map[core.ChunkKey]*pendingChunkChanges)

	reason, rejected := engine.dropSelectedItem(state, pending)

	if !rejected || reason != RejectDropCapacity {
		t.Fatalf("拒绝 = (%v,%v)，想要 drop_capacity", reason, rejected)
	}
	if state.player.inventory != before {
		t.Fatalf("容量不足时背包被修改：%+v", state.player.inventory)
	}
	if len(pending) != 0 {
		t.Fatalf("容量不足时产生了待提交区块变更：%+v", pending)
	}
}

// benchFootBlockIndex 返回玩家脚底方块在其区块内的紧凑索引。
func benchFootBlockIndex(state *sessionState) uint32 {
	position := core.BlockPos{
		X: int32(state.player.state.Position.X()),
		Y: int32(state.player.state.Position.Y()),
		Z: int32(state.player.state.Position.Z()),
	}
	index, _ := world.ChunkBlockIndex(position)
	return index
}

func BenchmarkDropSelectedItem(b *testing.B) {
	// benchmark 直接调用权威转移，避免整个 tick 的开销掩盖固定预检成本。
	newBenchEngine := func() (*Engine, *sessionState, *world.Chunk) {
		engine, session := readyMovementPlayerForBench(b)
		state := engine.sessions[session]
		state.player.inventory.Hotbar.Selected = 0
		record := engine.dimensions[state.dimension].records[core.ChunkPos{}]
		return engine, state, record.Chunk
	}

	b.Run("create", func(b *testing.B) {
		engine, state, chunk := newBenchEngine()
		pending := make(map[core.ChunkKey]*pendingChunkChanges)
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			b.StopTimer()
			// 每次迭代重置为固定状态：一件物品加空掉落物槽。
			state.player.inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 1}
			for slot := range core.DropsPerChunk {
				chunk.SetDrop(slot, world.DropSlot{})
			}
			clear(pending)
			b.StartTimer()
			engine.dropSelectedItem(state, pending)
		}
	})

	b.Run("merge", func(b *testing.B) {
		engine, state, chunk := newBenchEngine()
		pending := make(map[core.ChunkKey]*pendingChunkChanges)
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			b.StopTimer()
			// 每次迭代重置为固定状态：脚底已有同物品的未满堆，必然走合并路径。
			state.player.inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 1}
			for slot := range core.DropsPerChunk {
				chunk.SetDrop(slot, world.DropSlot{})
			}
			chunk.SetDrop(0, world.DropSlot{
				Generation: 1, Active: true,
				Stack:      core.ItemStack{Item: core.ItemStone, Count: 1},
				BlockIndex: benchFootBlockIndex(state),
			})
			clear(pending)
			b.StartTimer()
			engine.dropSelectedItem(state, pending)
		}
	})

	b.Run("capacity_reject", func(b *testing.B) {
		engine, state, chunk := newBenchEngine()
		// 用满堆且物品不匹配的槽占满容量：既不能合并也不能新建。
		for slot := range core.DropsPerChunk {
			chunk.SetDrop(slot, world.DropSlot{
				Active:     true,
				Stack:      core.ItemStack{Item: core.ItemDirt, Count: core.MaxStackCount},
				BlockIndex: uint32(slot),
			})
		}
		pending := make(map[core.ChunkKey]*pendingChunkChanges)
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			state.player.inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 1}
			if _, rejected := engine.dropSelectedItem(state, pending); !rejected {
				b.Fatal("容量已满仍然接受了丢弃")
			}
		}
	})
}
