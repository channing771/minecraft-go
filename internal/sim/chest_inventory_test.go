package sim_test

import (
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/world"
)

// openedChest 构造一个已 Ready 玩家并打开中心区块的箱子，返回其引用。
func openedChest(
	t *testing.T,
	inventory core.Inventory,
	slot world.ChestSlot,
) (*sim.Engine, sim.SessionID, core.ContainerRef) {
	t.Helper()
	engine, session := readyFlatPlayerWithInventory(t, inventory)
	key := core.ChunkKey{Dimension: core.Overworld}
	index := dropTargetIndex(t)
	engine.SetBlockForTest(core.BlockPos{}, core.ChestID)
	slot.BlockIndex = index
	slot.Active = true
	if slot.Generation == 0 {
		slot.Generation = 1
	}
	engine.SetChunkChestForTest(key, 0, slot)

	engine.Enqueue(openFurnaceCommand(session, 2))
	result := engine.Step()
	if len(result.Chests) != 1 {
		t.Fatalf("打开箱子失败: %+v / %+v", result.Rejected, result.Chests)
	}
	return engine, session, result.Chests[0].Chest
}

func TestMoveIntoChestAcceptsAnyRegisteredItemIncludingTools(t *testing.T) {
	stoneFull, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	cases := []struct {
		name  string
		stack core.ItemStack
	}{
		{"石头", core.ItemStack{Item: core.ItemStone, Count: 8}},
		{"煤炭", core.ItemStack{Item: core.ItemCoal, Count: 8}},
		{"粗铁", core.ItemStack{Item: core.ItemRawIron, Count: 8}},
		{"铁锭", core.ItemStack{Item: core.ItemIronIngot, Count: 8}},
		{"箱子本体", core.ItemStack{Item: core.ItemChest, Count: 2}},
		{"带耐久的石镐", core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: stoneFull / 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var inventory core.Inventory
			inventory.Hotbar.Slots[0] = tc.stack
			engine, session, ref := openedChest(t, inventory, world.ChestSlot{})

			engine.Enqueue(moveFurnaceCommand(session, 3, ref, 0, core.ChestFirstSlot))
			result := engine.Step()
			if len(result.Rejected) != 0 {
				t.Fatalf("箱子拒绝已注册物品: %+v", result.Rejected)
			}
			if got := currentChest(t, engine, 0).Items[0]; got != tc.stack {
				t.Fatalf("箱子格 = %+v，想要整堆移入 %+v", got, tc.stack)
			}
			if currentInventory(t, engine, session).Hotbar.Slots[0] != (core.ItemStack{}) {
				t.Fatal("来源格未清空")
			}
		})
	}
}

func TestMoveOutOfChestPreservesToolDurability(t *testing.T) {
	ironFull, _ := core.ItemMaxDurability(core.ItemIronPickaxe)
	chest := world.ChestSlot{}
	chest.Items[0] = core.ItemStack{Item: core.ItemIronPickaxe, Count: 1, Durability: ironFull - 17}
	engine, session, ref := openedChest(t, core.Inventory{}, chest)

	engine.Enqueue(moveFurnaceCommand(session, 3, ref, core.ChestFirstSlot, 0))
	result := engine.Step()
	if len(result.Rejected) != 0 {
		t.Fatalf("从箱子取出带耐久工具被拒绝: %+v", result.Rejected)
	}
	want := core.ItemStack{Item: core.ItemIronPickaxe, Count: 1, Durability: ironFull - 17}
	if got := currentInventory(t, engine, session).Hotbar.Slots[0]; got != want {
		t.Fatalf("玩家物品 = %+v，想要 %+v", got, want)
	}
	if got := currentChest(t, engine, 0).Items[0]; got != (core.ItemStack{}) {
		t.Fatalf("箱子格未清空: %+v", got)
	}
	if len(result.Inventories) != 1 {
		t.Fatalf("跨容器移动应当发布一次完整物品状态: %+v", result.Inventories)
	}
}

func TestMoveMergesIntoChestAndKeepsRemainder(t *testing.T) {
	var inventory core.Inventory
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemCoal, Count: 10}
	chest := world.ChestSlot{}
	chest.Items[0] = core.ItemStack{Item: core.ItemCoal, Count: core.MaxStackCount - 4}
	engine, session, ref := openedChest(t, inventory, chest)

	engine.Enqueue(moveFurnaceCommand(session, 3, ref, 0, core.ChestFirstSlot))
	result := engine.Step()
	if len(result.Rejected) != 0 {
		t.Fatalf("合并移动被拒绝: %+v", result.Rejected)
	}
	if got := currentChest(t, engine, 0).Items[0].Count; got != core.MaxStackCount {
		t.Fatalf("箱子格数量 = %d，想要装满", got)
	}
	if got := currentInventory(t, engine, session).Hotbar.Slots[0].Count; got != 6 {
		t.Fatalf("来源余量 = %d，想要剩 6", got)
	}
}

// TestMoveIntoChestFailsToMergeAtStackLimit
// 是合并计算按目标物品自身堆叠上限（而非固定 64）计算空间的回归测试：
// 耐久工具上限是 1，箱子里已有一把同类工具时，第二把不能"合并"进同一格，
// 必须整条拒绝，不能把两把工具错误地叠成一格。
func TestMoveIntoChestFailsToMergeAtStackLimit(t *testing.T) {
	stoneFull, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	var inventory core.Inventory
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: stoneFull}
	chest := world.ChestSlot{}
	chest.Items[0] = core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: 1}
	engine, session, ref := openedChest(t, inventory, chest)

	engine.Enqueue(moveFurnaceCommand(session, 3, ref, 0, core.ChestFirstSlot))
	result := engine.Step()
	if len(result.Rejected) != 1 || result.Rejected[0].Reason != sim.RejectInvalidInput {
		t.Fatalf("同类满限额目标 result=%+v", result)
	}
	if got := currentChest(t, engine, 0).Items[0]; got.Durability != 1 {
		t.Fatalf("被拒绝的移动修改了箱子: %+v", got)
	}
	if got := currentInventory(t, engine, session).Hotbar.Slots[0]; got.Durability != stoneFull {
		t.Fatalf("被拒绝的移动修改了玩家物品: %+v", got)
	}
}

func TestMoveSwapsDifferentItemsBetweenChestAndInventory(t *testing.T) {
	var inventory core.Inventory
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemCoal, Count: 3}
	chest := world.ChestSlot{}
	chest.Items[0] = core.ItemStack{Item: core.ItemStone, Count: 5}
	engine, session, ref := openedChest(t, inventory, chest)

	engine.Enqueue(moveFurnaceCommand(session, 3, ref, 0, core.ChestFirstSlot))
	result := engine.Step()
	if len(result.Rejected) != 0 {
		t.Fatalf("异类交换被拒绝: %+v", result.Rejected)
	}
	if got := currentChest(t, engine, 0).Items[0]; got != (core.ItemStack{Item: core.ItemCoal, Count: 3}) {
		t.Fatalf("箱子格 = %+v，想要煤炭", got)
	}
	if got := currentInventory(t, engine, session).Hotbar.Slots[0]; got != (core.ItemStack{Item: core.ItemStone, Count: 5}) {
		t.Fatalf("物品栏格 = %+v，想要石头", got)
	}
}

func TestMoveOutOfChestRejectsWhenTargetSlotIsFull(t *testing.T) {
	var inventory core.Inventory
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: core.MaxStackCount}
	chest := world.ChestSlot{}
	chest.Items[0] = core.ItemStack{Item: core.ItemStone, Count: core.MaxStackCount}
	engine, session, ref := openedChest(t, inventory, chest)

	engine.Enqueue(moveFurnaceCommand(session, 3, ref, core.ChestFirstSlot, 0))
	result := engine.Step()
	if len(result.Rejected) != 1 || result.Rejected[0].Reason != sim.RejectInvalidInput {
		t.Fatalf("满同类目标取出 result=%+v", result)
	}
	if got := currentChest(t, engine, 0).Items[0].Count; got != core.MaxStackCount {
		t.Fatalf("被拒绝的移动修改了箱子: %d", got)
	}
	if got := currentInventory(t, engine, session).Hotbar.Slots[0].Count; got != core.MaxStackCount {
		t.Fatal("被拒绝的移动修改了玩家物品")
	}
}

func TestMoveChestRejectsOutOfBoundsUnifiedSlot(t *testing.T) {
	var inventory core.Inventory
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 4}
	engine, session, ref := openedChest(t, inventory, world.ChestSlot{})

	engine.Enqueue(moveFurnaceCommand(session, 3, ref, 0, core.ChestViewSlots))
	result := engine.Step()
	if len(result.Rejected) != 1 || result.Rejected[0].Reason != sim.RejectInvalidInput {
		t.Fatalf("越界统一索引 result=%+v", result)
	}
	if currentInventory(t, engine, session).Hotbar.Slots[0].Count != 4 {
		t.Fatal("被拒绝的移动修改了玩家物品")
	}
	if got := currentChest(t, engine, 0).Items[0]; got != (core.ItemStack{}) {
		t.Fatalf("被拒绝的移动修改了箱子: %+v", got)
	}
}

func TestMoveRejectsStaleChestReference(t *testing.T) {
	var inventory core.Inventory
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 4}
	engine, session, ref := openedChest(t, inventory, world.ChestSlot{})

	stale := ref
	stale.Generation = ref.Generation + 1
	engine.Enqueue(moveFurnaceCommand(session, 3, stale, 0, core.ChestFirstSlot))
	result := engine.Step()
	if len(result.Rejected) != 1 || result.Rejected[0].Reason != sim.RejectInvalidInput {
		t.Fatalf("过期引用 result=%+v", result)
	}
	if got := currentChest(t, engine, 0).Items[0]; got != (core.ItemStack{}) {
		t.Fatalf("过期引用修改了箱子: %+v", got)
	}
	if currentInventory(t, engine, session).Hotbar.Slots[0].Count != 4 {
		t.Fatal("过期引用修改了玩家物品")
	}
}

func TestMoveIgnoresStaleSequenceForChest(t *testing.T) {
	var inventory core.Inventory
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 4}
	engine, session, ref := openedChest(t, inventory, world.ChestSlot{})

	// openedChest 内部已经用 sequence 2 打开箱子；重放同一个 sequence 必须被静默忽略。
	engine.Enqueue(moveFurnaceCommand(session, 2, ref, 0, core.ChestFirstSlot))
	result := engine.Step()
	if len(result.Rejected) != 0 {
		t.Fatalf("过期 sequence 不应产生拒绝: %+v", result.Rejected)
	}
	if got := currentChest(t, engine, 0).Items[0]; got != (core.ItemStack{}) {
		t.Fatalf("过期 sequence 修改了箱子: %+v", got)
	}
	if currentInventory(t, engine, session).Hotbar.Slots[0].Count != 4 {
		t.Fatal("过期 sequence 修改了玩家物品")
	}
}

func TestMoveChestMultiPlayerSameTickStableOrder(t *testing.T) {
	chest := world.ChestSlot{}
	chest.Items[0] = core.ItemStack{Item: core.ItemStone, Count: 1}
	engine, first, ref := openedChest(t, core.Inventory{}, chest)

	const second = sim.SessionID(2)
	engine.RegisterPlayer(second, sim.PlayerRestore{
		SpawnDimension: core.Overworld,
		SpawnAnchor:    core.ChunkPos{},
	})
	engine.Step()
	engine.Enqueue(openFurnaceCommand(second, 3))
	result := engine.Step()
	if len(result.Chests) != 2 || result.Chests[0].Session != first || result.Chests[1].Session != second {
		t.Fatalf("第二名玩家打开箱子失败: %+v", result.Chests)
	}

	// 同一 tick 内两名玩家都想取走箱子里唯一的一堆石头：
	// 必须按 session 稳定串行，先处理的会话拿到物品，后处理的会话看到空来源被拒绝。
	engine.Enqueue(moveFurnaceCommand(first, 4, ref, core.ChestFirstSlot, 0))
	engine.Enqueue(moveFurnaceCommand(second, 4, ref, core.ChestFirstSlot, 0))
	result = engine.Step()

	if len(result.Rejected) != 1 || result.Rejected[0].Session != second ||
		result.Rejected[0].Reason != sim.RejectInvalidInput {
		t.Fatalf("串行顺序不稳定: %+v", result.Rejected)
	}
	want := core.ItemStack{Item: core.ItemStone, Count: 1}
	if got := currentInventory(t, engine, first).Hotbar.Slots[0]; got != want {
		t.Fatalf("先处理的会话未取到物品: %+v", got)
	}
	if got := currentInventory(t, engine, second).Hotbar.Slots[0]; got != (core.ItemStack{}) {
		t.Fatalf("后处理的会话不应取到物品: %+v", got)
	}
	if got := currentChest(t, engine, 0).Items[0]; got != (core.ItemStack{}) {
		t.Fatalf("箱子最终状态 = %+v，想要清空", got)
	}
}
