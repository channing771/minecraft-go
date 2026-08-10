package sim_test

import (
	"math"
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/world"
)

// placeYaw 让玩家朝向 +Z，与既有放置测试使用同一命中方式。
var placeYaw = float32(math.Pi)

func moveFurnaceCommand(
	session sim.SessionID,
	sequence uint64,
	ref core.FurnaceRef,
	from, to uint8,
) sim.Command {
	return sim.Command{
		Session:  session,
		Sequence: sequence,
		Kind:     sim.CommandMoveFurnaceStack,
		Furnace:  ref,
		Slot:     from,
		ToSlot:   to,
	}
}

// openedFurnace 构造一个已 Ready 玩家并打开中心区块的熔炉，返回其引用。
func openedFurnace(
	t *testing.T,
	inventory core.Inventory,
	slot world.FurnaceSlot,
) (*sim.Engine, sim.SessionID, core.FurnaceRef) {
	t.Helper()
	engine, session := readyFlatPlayerWithInventory(t, inventory)
	key := core.ChunkKey{Dimension: core.Overworld}
	index := dropTargetIndex(t)
	engine.SetBlockForTest(core.BlockPos{}, core.FurnaceID)
	slot.BlockIndex = index
	slot.Active = true
	if slot.Generation == 0 {
		slot.Generation = 1
	}
	engine.SetChunkFurnaceForTest(key, 0, slot)

	engine.Enqueue(openFurnaceCommand(session, 2))
	result := engine.Step()
	if len(result.Furnaces) != 1 {
		t.Fatalf("打开熔炉失败: %+v / %+v", result.Rejected, result.Furnaces)
	}
	return engine, session, result.Furnaces[0].Furnace
}

func TestMoveIntoFurnaceMaterialsAcceptOnlyAllowedItems(t *testing.T) {
	cases := []struct {
		name   string
		item   core.ItemID
		target uint8
		ok     bool
	}{
		{"粗铁进输入", core.ItemRawIron, core.FurnaceInputSlot, true},
		{"沙子进输入", core.ItemSand, core.FurnaceInputSlot, true},
		{"黏土块进输入", core.ItemClay, core.FurnaceInputSlot, true},
		{"煤炭进燃料", core.ItemCoal, core.FurnaceFuelSlot, true},
		{"煤炭进输入", core.ItemCoal, core.FurnaceInputSlot, false},
		{"粗铁进燃料", core.ItemRawIron, core.FurnaceFuelSlot, false},
		{"石头进输入", core.ItemStone, core.FurnaceInputSlot, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var inventory core.Inventory
			inventory.Hotbar.Slots[0] = core.ItemStack{Item: tc.item, Count: 8}
			engine, session, ref := openedFurnace(t, inventory, world.FurnaceSlot{})

			engine.Enqueue(moveFurnaceCommand(session, 3, ref, 0, tc.target))
			result := engine.Step()
			if tc.ok {
				if len(result.Rejected) != 0 {
					t.Fatalf("合法移动被拒绝: %+v", result.Rejected)
				}
				got := currentFurnace(t, engine, 0)
				moved := got.Input
				if tc.target == core.FurnaceFuelSlot {
					moved = got.Fuel
				}
				if moved != (core.ItemStack{Item: tc.item, Count: 8}) {
					t.Fatalf("熔炉格 = %+v，想要整堆移入", moved)
				}
				if currentInventory(t, engine, session).Hotbar.Slots[0] != (core.ItemStack{}) {
					t.Fatal("来源格未清空")
				}
				return
			}
			if len(result.Rejected) != 1 || result.Rejected[0].Reason != sim.RejectInvalidInput {
				t.Fatalf("非法移动 result=%+v", result)
			}
			if got := currentFurnace(t, engine, 0); got.Input.Count != 0 || got.Fuel.Count != 0 {
				t.Fatalf("被拒绝的移动修改了熔炉: %+v", got)
			}
			if currentInventory(t, engine, session).Hotbar.Slots[0].Count != 8 {
				t.Fatal("被拒绝的移动修改了玩家物品")
			}
		})
	}
}

func TestMoveOutOfFurnaceOutput(t *testing.T) {
	engine, session, ref := openedFurnace(t, core.Inventory{}, world.FurnaceSlot{
		Output: core.ItemStack{Item: core.ItemIronIngot, Count: 5},
	})

	engine.Enqueue(moveFurnaceCommand(session, 3, ref, core.FurnaceOutputSlot, 0))
	result := engine.Step()
	if len(result.Rejected) != 0 {
		t.Fatalf("从输出取出被拒绝: %+v", result.Rejected)
	}
	if got := currentFurnace(t, engine, 0); got.Output != (core.ItemStack{}) {
		t.Fatalf("输出格未清空: %+v", got.Output)
	}
	want := core.ItemStack{Item: core.ItemIronIngot, Count: 5}
	if got := currentInventory(t, engine, session).Hotbar.Slots[0]; got != want {
		t.Fatalf("玩家物品 = %+v，想要 %+v", got, want)
	}
	if len(result.Inventories) != 1 {
		t.Fatalf("跨容器移动应当发布一次完整物品状态: %+v", result.Inventories)
	}
}

func TestMoveOutOfFurnaceMaterialOutputKeepsRemainder(t *testing.T) {
	for _, material := range furnaceMaterials {
		t.Run(material.name, func(t *testing.T) {
			output := material.output
			var inventory core.Inventory
			inventory.Hotbar.Slots[0] = core.ItemStack{Item: output, Count: core.MaxStackCount - 1}
			engine, session, ref := openedFurnace(t, inventory, world.FurnaceSlot{
				Output: core.ItemStack{Item: output, Count: 5},
			})

			engine.Enqueue(moveFurnaceCommand(session, 3, ref, core.FurnaceOutputSlot, 0))
			result := engine.Step()
			if len(result.Rejected) != 0 {
				t.Fatalf("取出产物 %d 被拒绝: %+v", output, result.Rejected)
			}
			if got := currentFurnace(t, engine, 0).Output; got != (core.ItemStack{Item: output, Count: 4}) {
				t.Fatalf("输出余量 = %+v，想要 4", got)
			}
			if got := currentInventory(t, engine, session).Hotbar.Slots[0].Count; got != core.MaxStackCount {
				t.Fatalf("玩家栏位数量 = %d，想要 %d", got, core.MaxStackCount)
			}
		})
	}
}

func TestFurnaceInputKindSwitchResetsProgress(t *testing.T) {
	var inventory core.Inventory
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemSand, Count: 3}
	engine, session, ref := openedFurnace(t, inventory, world.FurnaceSlot{
		Input:         core.ItemStack{Item: core.ItemRawIron, Count: 2},
		Output:        core.ItemStack{Item: core.ItemGlass, Count: 1},
		ProgressTicks: 137,
		BurnTicks:     1463,
	})

	engine.Enqueue(moveFurnaceCommand(session, 3, ref, 0, core.FurnaceInputSlot))
	result := engine.Step()
	if len(result.Rejected) != 0 {
		t.Fatalf("切换输入被拒绝: %+v", result.Rejected)
	}
	got := currentFurnace(t, engine, 0)
	if got.Input != (core.ItemStack{Item: core.ItemSand, Count: 3}) ||
		got.ProgressTicks != 0 || got.BurnTicks != 1463 {
		t.Fatalf("切换输入后熔炉 = %+v", got)
	}
	if stack := currentInventory(t, engine, session).Hotbar.Slots[0]; stack != (core.ItemStack{Item: core.ItemRawIron, Count: 2}) {
		t.Fatalf("换出的输入 = %+v，想要 2 个粗铁", stack)
	}
}

func TestFurnaceInputKindRemovalAndReinsertResetsProgress(t *testing.T) {
	engine, session, ref := openedFurnace(t, core.Inventory{}, world.FurnaceSlot{
		Input:         core.ItemStack{Item: core.ItemSand, Count: 2},
		Output:        core.ItemStack{Item: core.ItemIronIngot, Count: 1},
		ProgressTicks: 137,
		BurnTicks:     1463,
	})

	engine.Enqueue(moveFurnaceCommand(session, 3, ref, core.FurnaceInputSlot, 0))
	if result := engine.Step(); len(result.Rejected) != 0 {
		t.Fatalf("移除输入被拒绝: %+v", result.Rejected)
	}
	removed := currentFurnace(t, engine, 0)
	if removed.Input != (core.ItemStack{}) || removed.ProgressTicks != 0 || removed.BurnTicks != 1463 {
		t.Fatalf("移除输入后熔炉 = %+v", removed)
	}

	engine.Enqueue(moveFurnaceCommand(session, 4, ref, 0, core.FurnaceInputSlot))
	if result := engine.Step(); len(result.Rejected) != 0 {
		t.Fatalf("放回输入被拒绝: %+v", result.Rejected)
	}
	reinserted := currentFurnace(t, engine, 0)
	if reinserted.Input != (core.ItemStack{Item: core.ItemSand, Count: 2}) ||
		reinserted.ProgressTicks != 0 || reinserted.BurnTicks != 1463 {
		t.Fatalf("放回输入后熔炉 = %+v", reinserted)
	}
}

func TestFurnaceInputKindSameMaterialKeepsProgress(t *testing.T) {
	var inventory core.Inventory
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemRawIron, Count: 3}
	engine, session, ref := openedFurnace(t, inventory, world.FurnaceSlot{
		Input:         core.ItemStack{Item: core.ItemRawIron, Count: 2},
		Output:        core.ItemStack{Item: core.ItemGlass, Count: 1},
		ProgressTicks: 137,
		BurnTicks:     1463,
	})

	engine.Enqueue(moveFurnaceCommand(session, 3, ref, 0, core.FurnaceInputSlot))
	result := engine.Step()
	if len(result.Rejected) != 0 {
		t.Fatalf("合并同种输入被拒绝: %+v", result.Rejected)
	}
	got := currentFurnace(t, engine, 0)
	if got.Input != (core.ItemStack{Item: core.ItemRawIron, Count: 5}) ||
		got.ProgressTicks != 137 || got.BurnTicks != 1463 {
		t.Fatalf("合并同种输入后熔炉 = %+v", got)
	}
}

func TestMoveIntoOutputSlotIsRejected(t *testing.T) {
	var inventory core.Inventory
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemIronIngot, Count: 3}
	engine, session, ref := openedFurnace(t, inventory, world.FurnaceSlot{})

	engine.Enqueue(moveFurnaceCommand(session, 3, ref, 0, core.FurnaceOutputSlot))
	result := engine.Step()
	if len(result.Rejected) != 1 || result.Rejected[0].Reason != sim.RejectInvalidInput {
		t.Fatalf("输出格作为目标 result=%+v", result)
	}
	if got := currentFurnace(t, engine, 0); got.Output.Count != 0 {
		t.Fatalf("输出格被写入: %+v", got.Output)
	}
}

func TestMoveMergesIntoFurnaceAndKeepsRemainder(t *testing.T) {
	var inventory core.Inventory
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemCoal, Count: 10}
	engine, session, ref := openedFurnace(t, inventory, world.FurnaceSlot{
		Fuel: core.ItemStack{Item: core.ItemCoal, Count: core.MaxStackCount - 4},
	})

	engine.Enqueue(moveFurnaceCommand(session, 3, ref, 0, core.FurnaceFuelSlot))
	result := engine.Step()
	if len(result.Rejected) != 0 {
		t.Fatalf("合并移动被拒绝: %+v", result.Rejected)
	}
	if got := currentFurnace(t, engine, 0); got.Fuel.Count != core.MaxStackCount {
		t.Fatalf("燃料格 = %+v，想要装满", got.Fuel)
	}
	if got := currentInventory(t, engine, session).Hotbar.Slots[0]; got.Count != 6 {
		t.Fatalf("来源余量 = %+v，想要剩 6", got)
	}
}

func TestMoveRejectsStaleFurnaceReference(t *testing.T) {
	var inventory core.Inventory
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemRawIron, Count: 4}
	engine, session, ref := openedFurnace(t, inventory, world.FurnaceSlot{})

	stale := ref
	stale.Generation = ref.Generation + 1
	engine.Enqueue(moveFurnaceCommand(session, 3, stale, 0, core.FurnaceInputSlot))
	result := engine.Step()
	if len(result.Rejected) != 1 || result.Rejected[0].Reason != sim.RejectInvalidInput {
		t.Fatalf("过期引用 result=%+v", result)
	}
	if got := currentFurnace(t, engine, 0); got.Input.Count != 0 {
		t.Fatalf("过期引用修改了熔炉: %+v", got)
	}
	if currentInventory(t, engine, session).Hotbar.Slots[0].Count != 4 {
		t.Fatal("过期引用修改了玩家物品")
	}
}

func TestMoveRejectsWhenInventoryHasNoRoom(t *testing.T) {
	engine, session, ref := openedFurnace(t, fullTestInventory(), world.FurnaceSlot{
		Output: core.ItemStack{Item: core.ItemIronIngot, Count: 5},
	})

	engine.Enqueue(moveFurnaceCommand(session, 3, ref, core.FurnaceOutputSlot, 0))
	result := engine.Step()
	if len(result.Rejected) != 1 {
		t.Fatalf("满物品栏取出 result=%+v", result)
	}
	if got := currentFurnace(t, engine, 0); got.Output.Count != 5 {
		t.Fatalf("失败的取出修改了输出格: %+v", got.Output)
	}
}

func TestPlaceFurnaceActivatesLowestSlot(t *testing.T) {
	var inventory core.Inventory
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemFurnace, Count: 2}
	engine, session := readyFlatPlayerInventoryWithBlocks(t, map[core.BlockPos]core.BlockID{
		{X: 0, Y: 2, Z: 3}: core.StoneID,
	}, inventory)

	engine.Enqueue(sim.Command{
		Session: session, Sequence: 2, Kind: sim.CommandPlaceBlock,
		Yaw: placeYaw, Slot: 0,
	})
	result := engine.Step()
	if len(result.Rejected) != 0 || len(result.Changes) != 1 {
		t.Fatalf("放置熔炉 result=%+v", result)
	}
	got := currentFurnace(t, engine, 0)
	if !got.Active || got.Generation != 1 {
		t.Fatalf("放置未启用最低槽: %+v", got)
	}
	if inv := currentInventory(t, engine, session); inv.Hotbar.Slots[0].Count != 1 {
		t.Fatalf("放置未扣除一个熔炉: %+v", inv.Hotbar.Slots[0])
	}
}

func TestPlaceFurnaceRejectsWhenSlotsAreFull(t *testing.T) {
	var inventory core.Inventory
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemFurnace, Count: 2}
	engine, session := readyFlatPlayerInventoryWithBlocks(t, map[core.BlockPos]core.BlockID{
		{X: 0, Y: 2, Z: 3}: core.StoneID,
	}, inventory)
	key := core.ChunkKey{Dimension: core.Overworld}
	for slot := range core.FurnacesPerChunk {
		position := core.BlockPos{X: int32(slot%16) + 4, Y: 3, Z: int32(slot/16) + 4}
		index, ok := world.ChunkBlockIndex(position)
		if !ok {
			t.Fatal("占位熔炉没有区块索引")
		}
		engine.SetBlockForTest(position, core.FurnaceID)
		engine.SetChunkFurnaceForTest(key, slot, world.FurnaceSlot{
			Generation: 1, Active: true, BlockIndex: index,
		})
	}

	engine.Enqueue(sim.Command{
		Session: session, Sequence: 2, Kind: sim.CommandPlaceBlock,
		Yaw: placeYaw, Slot: 0,
	})
	result := engine.Step()
	if len(result.Rejected) != 1 || result.Rejected[0].Reason != sim.RejectContainerCapacity {
		t.Fatalf("第 33 个熔炉 result=%+v", result)
	}
	if len(result.Changes) != 0 {
		t.Fatalf("被拒绝的放置修改了世界: %+v", result.Changes)
	}
	if inv := currentInventory(t, engine, session); inv.Hotbar.Slots[0].Count != 2 {
		t.Fatalf("被拒绝的放置扣除了物品: %+v", inv.Hotbar.Slots[0])
	}
}

func TestPlaceIronBlockDoesNotAllocateFurnaceSlot(t *testing.T) {
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
	if got := currentFurnace(t, engine, 0); got.Active {
		t.Fatalf("铁块占用了熔炉槽: %+v", got)
	}
}

func TestMiningFurnaceDropsBodyAndContents(t *testing.T) {
	var inventory core.Inventory
	stoneFull, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: stoneFull}
	engine, session := readyFlatPlayerWithInventory(t, inventory)
	key := core.ChunkKey{Dimension: core.Overworld}
	index := dropTargetIndex(t)
	engine.SetBlockForTest(core.BlockPos{}, core.FurnaceID)
	engine.SetChunkFurnaceForTest(key, 0, world.FurnaceSlot{
		Generation: 3, Active: true, BlockIndex: index,
		Input: core.ItemStack{Item: core.ItemRawIron, Count: 7},
		Fuel:  core.ItemStack{Item: core.ItemCoal, Count: 2},
		// 输出已满使熔炼暂停，因此本 tick 的燃料与输入不会被消耗。
		Output: core.ItemStack{Item: core.ItemIronIngot, Count: core.MaxStackCount},
	})

	sequence := uint64(1)
	result := mineUntilComplete(t, engine, session, &sequence, 0, lookDown, 15)
	if len(result.Rejected) != 0 || len(result.Changes) != 1 {
		t.Fatalf("破坏熔炉 result=%+v", result)
	}
	if got := currentFurnace(t, engine, 0); got.Active || got.Generation != 3 {
		t.Fatalf("停用槽 = %+v，想要保留 generation 3", got)
	}

	chunk, _, _ := engine.CloneReadyChunk(key)
	seen := map[core.ItemID]uint8{}
	for slot := range core.DropsPerChunk {
		if drop := chunk.Drop(slot); drop.Active {
			seen[drop.Stack.Item] += drop.Stack.Count
		}
	}
	want := map[core.ItemID]uint8{
		core.ItemFurnace: 1, core.ItemRawIron: 7,
		core.ItemCoal: 2, core.ItemIronIngot: core.MaxStackCount,
	}
	for item, count := range want {
		if seen[item] != count {
			t.Fatalf("物品 %d 掉落 %d，想要 %d", item, seen[item], count)
		}
	}
}

func TestMiningFurnaceRejectsWhenDropsAreFull(t *testing.T) {
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
	engine.SetBlockForTest(core.BlockPos{}, core.FurnaceID)
	engine.SetChunkFurnaceForTest(key, 0, world.FurnaceSlot{
		Generation: 3, Active: true, BlockIndex: index,
		Input: core.ItemStack{Item: core.ItemRawIron, Count: 7},
	})
	for slot := range core.DropsPerChunk {
		engine.SetChunkDropForTest(key, slot, world.DropSlot{
			Generation: 1, Active: true,
			Stack:      core.ItemStack{Item: core.ItemStone, Count: core.MaxStackCount},
			BlockIndex: elsewhere,
		})
	}

	sequence := uint64(1)
	result := mineUntilComplete(t, engine, session, &sequence, 0, lookDown, 15)
	if len(result.Rejected) != 1 || result.Rejected[0].Reason != sim.RejectDropCapacity {
		t.Fatalf("掉落容量不足 result=%+v", result)
	}
	if len(result.Changes) != 0 {
		t.Fatalf("被拒绝的破坏修改了世界: %+v", result.Changes)
	}
	after, _, _ := engine.CloneReadyChunk(key)
	// 年龄计数每 tick 递增，因此只断言掉落物组成没有被写入新物品。
	for slot := range core.DropsPerChunk {
		if got := after.Drop(slot); got.Stack.Item != core.ItemStone {
			t.Fatalf("被拒绝的破坏写入了掉落物: 槽 %d = %+v", slot, got)
		}
	}
	if got := currentFurnace(t, engine, 0); !got.Active || got.Input.Count != 7 {
		t.Fatalf("被拒绝的破坏修改了熔炉: %+v", got)
	}
	if after.BlockAt(0, 0, 0) != core.FurnaceID {
		t.Fatal("被拒绝的破坏清除了方块")
	}
}

// TestMoveIntoFurnaceRejectsChestOnlySlot 是 sim 层熔炉越界的回归测试：
// 箱子的统一栏位允许到 62（core.ChestViewSlots），但用熔炉引用发起移动时，
// sim 必须仍然用熔炉自己的 0..38 边界拒绝，不能依赖网络层已经按 Kind 校验过。
func TestMoveIntoFurnaceRejectsChestOnlySlot(t *testing.T) {
	var inventory core.Inventory
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemRawIron, Count: 4}
	engine, session, ref := openedFurnace(t, inventory, world.FurnaceSlot{})

	engine.Enqueue(moveFurnaceCommand(session, 3, ref, 0, core.ChestFirstSlot+5))
	result := engine.Step()
	if len(result.Rejected) != 1 || result.Rejected[0].Reason != sim.RejectInvalidInput {
		t.Fatalf("熔炉越界（箱子专属栏位）result=%+v", result)
	}
	if got := currentFurnace(t, engine, 0); got.Input.Count != 0 {
		t.Fatalf("越界移动修改了熔炉: %+v", got)
	}
	if currentInventory(t, engine, session).Hotbar.Slots[0].Count != 4 {
		t.Fatal("越界移动修改了玩家物品")
	}
}
