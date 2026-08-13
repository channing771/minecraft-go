package sim_test

import (
	"testing"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/sim"
	"github.com/channing771/mornlea/internal/world"
)

func TestPlaceChestActivatesLowestSlot(t *testing.T) {
	var inventory core.Inventory
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemChest, Count: 2}
	engine, session := readyFlatPlayerInventoryWithBlocks(t, map[core.BlockPos]core.BlockID{
		{X: 0, Y: 2, Z: 3}: core.StoneID,
	}, inventory)

	engine.Enqueue(sim.Command{
		Session: session, Sequence: 2, Kind: sim.CommandPlaceBlock,
		Yaw: placeYaw, Slot: 0,
	})
	result := engine.Step()
	if len(result.Rejected) != 0 || len(result.Changes) != 1 {
		t.Fatalf("放置箱子 result=%+v", result)
	}
	got := currentChest(t, engine, 0)
	if !got.Active || got.Generation != 1 {
		t.Fatalf("放置未启用最低槽: %+v", got)
	}
	for i, stack := range got.Items {
		if stack != (core.ItemStack{}) {
			t.Fatalf("新箱子格 %d 非空: %+v", i, stack)
		}
	}
	if inv := currentInventory(t, engine, session); inv.Hotbar.Slots[0].Count != 1 {
		t.Fatalf("放置未扣除一个箱子: %+v", inv.Hotbar.Slots[0])
	}
}

func TestPlaceChestRejectsWhenSlotsAreFull(t *testing.T) {
	var inventory core.Inventory
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemChest, Count: 2}
	engine, session := readyFlatPlayerInventoryWithBlocks(t, map[core.BlockPos]core.BlockID{
		{X: 0, Y: 2, Z: 3}: core.StoneID,
	}, inventory)
	key := core.ChunkKey{Dimension: core.Overworld}
	for slot := range core.ChestsPerChunk {
		position := core.BlockPos{X: int32(slot%16) + 4, Y: 3, Z: int32(slot/16) + 4}
		index, ok := world.ChunkBlockIndex(position)
		if !ok {
			t.Fatal("占位箱子没有区块索引")
		}
		engine.SetBlockForTest(position, core.ChestID)
		engine.SetChunkChestForTest(key, slot, world.ChestSlot{
			Generation: 1, Active: true, BlockIndex: index,
		})
	}
	revisionBefore := chunkRevision(t, engine, key)

	engine.Enqueue(sim.Command{
		Session: session, Sequence: 2, Kind: sim.CommandPlaceBlock,
		Yaw: placeYaw, Slot: 0,
	})
	result := engine.Step()
	if len(result.Rejected) != 1 || result.Rejected[0].Reason != sim.RejectContainerCapacity {
		t.Fatalf("第 17 个箱子 result=%+v", result)
	}
	if len(result.Changes) != 0 {
		t.Fatalf("被拒绝的放置修改了世界: %+v", result.Changes)
	}
	if inv := currentInventory(t, engine, session); inv.Hotbar.Slots[0].Count != 2 {
		t.Fatalf("被拒绝的放置扣除了物品: %+v", inv.Hotbar.Slots[0])
	}
	if got := chunkRevision(t, engine, key); got != revisionBefore {
		t.Fatalf("被拒绝的放置改变了 revision: 之前 %d 之后 %d", revisionBefore, got)
	}
}

// TestPlaceIronBlockDoesNotAllocateContainerSlots 是回归测试：
// 普通方块（铁块）的放置既不能分配箱子槽，也不能触碰熔炉槽。
func TestPlaceIronBlockDoesNotAllocateContainerSlots(t *testing.T) {
	var inventory core.Inventory
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemIronBlock, Count: 1}
	engine, session := readyFlatPlayerInventoryWithBlocks(t, map[core.BlockPos]core.BlockID{
		{X: 0, Y: 2, Z: 3}: core.StoneID,
	}, inventory)

	engine.Enqueue(sim.Command{
		Session: session, Sequence: 2, Kind: sim.CommandPlaceBlock,
		Yaw: placeYaw, Slot: 0,
	})
	if result := engine.Step(); len(result.Rejected) != 0 {
		t.Fatalf("放置铁块被拒绝: %+v", result.Rejected)
	}
	if got := currentChest(t, engine, 0); got.Active {
		t.Fatalf("铁块占用了箱子槽: %+v", got)
	}
	if got := currentFurnace(t, engine, 0); got.Active {
		t.Fatalf("铁块占用了熔炉槽: %+v", got)
	}
}

func TestBreakEmptyChestDropsOnlyBody(t *testing.T) {
	var inventory core.Inventory
	stoneFull, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: stoneFull}
	engine, session := readyFlatPlayerWithInventory(t, inventory)
	key := core.ChunkKey{Dimension: core.Overworld}
	index := dropTargetIndex(t)
	engine.SetBlockForTest(core.BlockPos{}, core.ChestID)
	engine.SetChunkChestForTest(key, 0, world.ChestSlot{
		Generation: 3, Active: true, BlockIndex: index,
	})

	sequence := uint64(1)
	result := mineUntilComplete(t, engine, session, &sequence, 0, lookDown, 15)
	if len(result.Rejected) != 0 || len(result.Changes) != 1 {
		t.Fatalf("破坏空箱子 result=%+v", result)
	}
	if got := currentChest(t, engine, 0); got.Active || got.Generation != 3 {
		t.Fatalf("停用槽 = %+v，想要保留 generation 3", got)
	}

	chunk, _, _ := engine.CloneReadyChunk(key)
	total := uint8(0)
	for slot := range core.DropsPerChunk {
		if drop := chunk.Drop(slot); drop.Active {
			if drop.Stack.Item != core.ItemChest {
				t.Fatalf("空箱子掉落了非本体物品: %+v", drop)
			}
			total += drop.Stack.Count
		}
	}
	if total != 1 {
		t.Fatalf("空箱子本体掉落数量 = %d，想要 1", total)
	}
}

func TestBreakFullChestDropsBodyAndContents(t *testing.T) {
	var inventory core.Inventory
	stoneFull, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: stoneFull}
	engine, session := readyFlatPlayerWithInventory(t, inventory)
	key := core.ChunkKey{Dimension: core.Overworld}
	index := dropTargetIndex(t)
	engine.SetBlockForTest(core.BlockPos{}, core.ChestID)

	chest := world.ChestSlot{Generation: 5, Active: true, BlockIndex: index}
	for i := range chest.Items {
		chest.Items[i] = core.ItemStack{Item: core.ItemStone, Count: 1}
	}
	engine.SetChunkChestForTest(key, 0, chest)

	sequence := uint64(1)
	result := mineUntilComplete(t, engine, session, &sequence, 0, lookDown, 15)
	if len(result.Rejected) != 0 || len(result.Changes) != 1 {
		t.Fatalf("破坏满箱子 result=%+v", result)
	}
	if got := currentChest(t, engine, 0); got.Active || got.Generation != 5 {
		t.Fatalf("停用槽 = %+v，想要保留 generation 5", got)
	}
	for i, stack := range currentChest(t, engine, 0).Items {
		if stack != (core.ItemStack{}) {
			t.Fatalf("停用槽的格 %d 未清零: %+v", i, stack)
		}
	}

	chunk, _, _ := engine.CloneReadyChunk(key)
	seen := map[core.ItemID]int{}
	for slot := range core.DropsPerChunk {
		if drop := chunk.Drop(slot); drop.Active {
			seen[drop.Stack.Item] += int(drop.Stack.Count)
		}
	}
	if seen[core.ItemChest] != 1 {
		t.Fatalf("箱子本体掉落 = %d，想要 1", seen[core.ItemChest])
	}
	if seen[core.ItemStone] != core.ChestSlots {
		t.Fatalf("箱子内容掉落 = %d，想要 %d", seen[core.ItemStone], core.ChestSlots)
	}
}

func TestBreakChestRejectsWhenDropsAreFull(t *testing.T) {
	var inventory core.Inventory
	stoneFull, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: stoneFull}
	engine, session := readyFlatPlayerWithInventory(t, inventory)
	key := core.ChunkKey{Dimension: core.Overworld}
	index := dropTargetIndex(t)
	elsewhere, ok := world.ChunkBlockIndex(core.BlockPos{X: 9, Y: 5, Z: 9})
	if !ok {
		t.Fatal("占位掉落物没有区块索引")
	}
	engine.SetBlockForTest(core.BlockPos{}, core.ChestID)
	chest := world.ChestSlot{Generation: 4, Active: true, BlockIndex: index}
	chest.Items[0] = core.ItemStack{Item: core.ItemStone, Count: 3}
	engine.SetChunkChestForTest(key, 0, chest)
	for slot := range core.DropsPerChunk {
		engine.SetChunkDropForTest(key, slot, world.DropSlot{
			Generation: 1, Active: true,
			Stack:      core.ItemStack{Item: core.ItemStone, Count: core.MaxStackCount},
			BlockIndex: elsewhere,
		})
	}
	revisionBefore := chunkRevision(t, engine, key)

	sequence := uint64(1)
	result := mineUntilComplete(t, engine, session, &sequence, 0, lookDown, 15)
	if len(result.Rejected) != 1 || result.Rejected[0].Reason != sim.RejectDropCapacity {
		t.Fatalf("掉落容量不足 result=%+v", result)
	}
	if len(result.Changes) != 0 {
		t.Fatalf("被拒绝的破坏修改了世界: %+v", result.Changes)
	}
	after, _, _ := engine.CloneReadyChunk(key)
	for slot := range core.DropsPerChunk {
		if got := after.Drop(slot); got.Stack.Item != core.ItemStone || got.BlockIndex != elsewhere {
			t.Fatalf("被拒绝的破坏写入了掉落物: 槽 %d = %+v", slot, got)
		}
	}
	if got := currentChest(t, engine, 0); !got.Active || got.Items[0].Count != 3 {
		t.Fatalf("被拒绝的破坏修改了箱子: %+v", got)
	}
	if after.BlockAt(0, 0, 0) != core.ChestID {
		t.Fatal("被拒绝的破坏清除了方块")
	}
	if got := chunkRevision(t, engine, key); got != revisionBefore {
		t.Fatalf("被拒绝的破坏改变了 revision: 之前 %d 之后 %d", revisionBefore, got)
	}
}

// chunkRevision 读取指定区块当前的存档 revision，供"全不变"断言比较前后值。
func chunkRevision(t *testing.T, engine *sim.Engine, key core.ChunkKey) uint64 {
	t.Helper()
	_, revision, ok := engine.CloneReadyChunk(key)
	if !ok {
		t.Fatal("中心区块不可用")
	}
	return revision
}
