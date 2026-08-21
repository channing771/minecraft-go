package sim_test

import (
	"testing"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/sim"
	"github.com/channing771/mornlea/internal/world"
)

func moveCommand(session sim.SessionID, sequence uint64, from, to uint8) sim.Command {
	return sim.Command{
		Session:  session,
		Sequence: sequence,
		Kind:     sim.CommandMoveInventoryStack,
		Slot:     from,
		ToSlot:   to,
	}
}

func TestMoveStackIntoBackpackPublishesOnce(t *testing.T) {
	var stocked core.Inventory
	stocked.Hotbar.Slots[1] = core.ItemStack{Item: core.ItemStone, Count: 7}
	engine, session := readyFlatPlayerWithInventory(t, stocked)
	engine.Enqueue(moveCommand(session, 2, 1, core.HotbarSlots+4))

	result := engine.Step()
	if len(result.Rejected) != 0 || len(result.Inventories) != 1 {
		t.Fatalf("移动 result=%+v", result)
	}
	got := result.Inventories[0].Inventory
	if got.Hotbar.Slots[1] != (core.ItemStack{}) ||
		got.Backpack[4] != (core.ItemStack{Item: core.ItemStone, Count: 7}) {
		t.Fatalf("移动结果 = %+v", got)
	}
}

func TestMoveStackMergesAndSwaps(t *testing.T) {
	var stocked core.Inventory
	stocked.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 10}
	stocked.Backpack[0] = core.ItemStack{Item: core.ItemStone, Count: 60}
	stocked.Hotbar.Slots[2] = core.ItemStack{Item: core.ItemGrass, Count: 3}
	stocked.Backpack[1] = core.ItemStack{Item: core.ItemDirt, Count: 4}
	engine, session := readyFlatPlayerWithInventory(t, stocked)

	engine.Enqueue(moveCommand(session, 2, 0, core.HotbarSlots))
	engine.Enqueue(moveCommand(session, 3, 2, core.HotbarSlots+1))
	result := engine.Step()
	if len(result.Rejected) != 0 || len(result.Inventories) != 1 {
		t.Fatalf("同 tick 两次移动 result=%+v", result)
	}
	got := result.Inventories[0].Inventory
	if got.Backpack[0].Count != core.MaxStackCount ||
		got.Hotbar.Slots[0] != (core.ItemStack{Item: core.ItemStone, Count: 6}) {
		t.Fatalf("合并结果 = %+v", got)
	}
	if got.Hotbar.Slots[2] != (core.ItemStack{Item: core.ItemDirt, Count: 4}) ||
		got.Backpack[1] != (core.ItemStack{Item: core.ItemGrass, Count: 3}) {
		t.Fatalf("交换结果 = %+v", got)
	}
}

func TestMoveStackRejectsInvalidRequests(t *testing.T) {
	var stocked core.Inventory
	stocked.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 1}
	cases := []struct {
		name     string
		from, to uint8
		want     sim.RejectReason
	}{
		{"同格", 0, 0, sim.RejectInvalidInput},
		{"空来源", 5, 6, sim.RejectInvalidInput},
		{"来源越界", core.InventorySlots, 0, sim.RejectInvalidSlot},
		{"目标越界", 0, core.InventorySlots, sim.RejectInvalidSlot},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			engine, session := readyFlatPlayerWithInventory(t, stocked)
			engine.Enqueue(moveCommand(session, 2, tc.from, tc.to))

			result := engine.Step()
			if len(result.Rejected) != 1 || result.Rejected[0].Reason != tc.want {
				t.Fatalf("result=%+v，想要 %v", result, tc.want)
			}
			if len(result.Inventories) != 0 {
				t.Fatalf("被拒绝的移动仍发布状态: %+v", result.Inventories)
			}
			if got := currentInventory(t, engine, session); got != stocked {
				t.Fatalf("被拒绝的移动修改了物品状态: %+v", got)
			}
		})
	}
}

func TestMoveStackRejectsPendingSpawn(t *testing.T) {
	engine := sim.NewEngine(0, 0, 0)
	const session = sim.SessionID(1)
	engine.RegisterSession(session, core.Overworld, core.ChunkPos{})
	engine.Enqueue(moveCommand(session, 1, 0, 1))

	result := engine.Step()
	if len(result.Rejected) != 1 || result.Rejected[0].Reason != sim.RejectPlayerNotReady {
		t.Fatalf("PendingSpawn result=%+v", result)
	}
}

func TestDropPickupFallsBackToBackpack(t *testing.T) {
	var full core.Inventory
	for slot := range full.Hotbar.Slots {
		full.Hotbar.Slots[slot] = core.ItemStack{Item: core.ItemStone, Count: core.MaxStackCount}
	}
	engine, session := readyFlatPlayerWithInventory(t, full)
	engine.SetChunkDropForTest(core.ChunkKey{Dimension: core.Overworld}, 0, world.DropSlot{
		Generation: 1, Active: true,
		Stack:      core.ItemStack{Item: core.ItemGrass, Count: 4},
		BlockIndex: dropTargetIndex(t),
	})

	engine.Step()
	got := currentInventory(t, engine, session)
	if got.Backpack[0] != (core.ItemStack{Item: core.ItemGrass, Count: 4}) {
		t.Fatalf("满快捷栏时拾取未落入背包: %+v", got.Backpack[0])
	}
	if got.Hotbar != full.Hotbar {
		t.Fatalf("快捷栏被修改: %+v", got.Hotbar)
	}
}

func craftCommand(session sim.SessionID, sequence uint64, recipe core.RecipeID) sim.Command {
	return sim.Command{
		Session:  session,
		Sequence: sequence,
		Kind:     sim.CommandCraftRecipe,
		Recipe:   recipe,
	}
}

func TestCraftPublishesOnceAndConsumesInputs(t *testing.T) {
	var stocked core.Inventory
	stocked.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 5}
	engine, session := readyFlatPlayerWithInventory(t, stocked)
	engine.Enqueue(craftCommand(session, 2, core.RecipeStoneBricks))

	result := engine.Step()
	if len(result.Rejected) != 0 || len(result.Inventories) != 1 {
		t.Fatalf("合成 result=%+v", result)
	}
	got := result.Inventories[0].Inventory
	if got.Hotbar.Slots[0] != (core.ItemStack{Item: core.ItemStone, Count: 1}) {
		t.Fatalf("原料格 = %+v，想要剩余 1 个石头", got.Hotbar.Slots[0])
	}
	if got.Hotbar.Slots[1] != (core.ItemStack{Item: core.ItemStoneBrick, Count: 4}) {
		t.Fatalf("产物格 = %+v，想要 4 个石砖", got.Hotbar.Slots[1])
	}
}

func TestCraftRejectsWithoutChangingInventory(t *testing.T) {
	var stocked core.Inventory
	stocked.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 3}
	cases := []struct {
		name   string
		recipe core.RecipeID
	}{
		{"原料不足", core.RecipeStoneBricks},
		{"未知配方", core.RecipeID(200)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			engine, session := readyFlatPlayerWithInventory(t, stocked)
			engine.Enqueue(craftCommand(session, 2, tc.recipe))

			result := engine.Step()
			if len(result.Rejected) != 1 ||
				result.Rejected[0].Reason != sim.RejectInvalidInput {
				t.Fatalf("result=%+v，想要 invalid_input", result)
			}
			if len(result.Inventories) != 0 {
				t.Fatalf("失败的合成仍发布状态: %+v", result.Inventories)
			}
			if got := currentInventory(t, engine, session); got != stocked {
				t.Fatalf("失败的合成修改了物品状态: %+v", got)
			}
		})
	}
}

func TestCraftIgnoresStaleSequence(t *testing.T) {
	var stocked core.Inventory
	stocked.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 8}
	engine, session := readyFlatPlayerWithInventory(t, stocked)
	engine.Enqueue(craftCommand(session, 2, core.RecipeStoneBricks))
	engine.Step()

	// 重复使用同一 sequence，必须被既有去重规则丢弃。
	engine.Enqueue(craftCommand(session, 2, core.RecipeStoneBricks))
	result := engine.Step()
	if len(result.Inventories) != 0 || len(result.Rejected) != 0 {
		t.Fatalf("过期 sequence 被再次执行: %+v", result)
	}
	if got := currentInventory(t, engine, session).Hotbar.Slots[0].Count; got != 4 {
		t.Fatalf("原料被扣除两次: 剩余 %d，想要 4", got)
	}
}

func TestCraftRejectsPendingSpawn(t *testing.T) {
	engine := sim.NewEngine(0, 0, 0)
	const session = sim.SessionID(1)
	engine.RegisterSession(session, core.Overworld, core.ChunkPos{})
	engine.Enqueue(craftCommand(session, 1, core.RecipeStoneBricks))

	result := engine.Step()
	if len(result.Rejected) != 1 || result.Rejected[0].Reason != sim.RejectPlayerNotReady {
		t.Fatalf("PendingSpawn result=%+v", result)
	}
}
