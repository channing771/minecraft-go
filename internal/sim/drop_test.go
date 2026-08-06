package sim_test

import (
	"fmt"
	"math"
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/world"
)

// fullTestInventory 返回快捷栏与背包都装满的完整物品状态。
func fullTestInventory() core.Inventory {
	var inventory core.Inventory
	for slot := range inventory.Hotbar.Slots {
		inventory.Hotbar.Slots[slot] = core.ItemStack{
			Item: core.ItemStone, Count: core.MaxStackCount,
		}
	}
	for slot := range inventory.Backpack {
		inventory.Backpack[slot] = core.ItemStack{
			Item: core.ItemStone, Count: core.MaxStackCount,
		}
	}
	return inventory
}

// dropTargetIndex 是俯视挖掘命中的方块 (0,0,0) 在区块内的索引。
func dropTargetIndex(t *testing.T) uint32 {
	t.Helper()
	index, ok := world.ChunkBlockIndex(core.BlockPos{})
	if !ok {
		t.Fatal("目标方块没有区块索引")
	}
	return index
}

func onlyDrop(t *testing.T, engine *sim.Engine) (int, world.DropSlot) {
	t.Helper()
	chunk, _, ok := engine.CloneReadyChunk(core.ChunkKey{Dimension: core.Overworld})
	if !ok {
		t.Fatal("中心区块不可用")
	}
	found := -1
	var drop world.DropSlot
	for slot := range core.DropsPerChunk {
		if chunk.Drop(slot).Active {
			if found >= 0 {
				t.Fatalf("活动掉落物多于一个：槽 %d 与 %d", found, slot)
			}
			found, drop = slot, chunk.Drop(slot)
		}
	}
	if found < 0 {
		t.Fatal("没有活动掉落物")
	}
	return found, drop
}

func TestMiningCreatesDropWithoutTouchingHotbar(t *testing.T) {
	engine, session := readyFlatPlayer(t)
	sequence := uint64(1)
	result := mineUntilComplete(t, engine, session, &sequence, 0, lookDown, 5)
	if len(result.Rejected) != 0 || len(result.Changes) != 1 {
		t.Fatalf("挖掘 result=%+v", result)
	}
	if len(result.Inventories) != 0 {
		t.Fatalf("挖掘直接修改了快捷栏: %+v", result.Inventories)
	}
	slot, drop := onlyDrop(t, engine)
	if slot != 0 || drop.Stack != (core.ItemStack{Item: core.ItemGrass, Count: 1}) ||
		drop.BlockIndex != dropTargetIndex(t) || drop.Generation != 1 {
		t.Fatalf("掉落物槽 %d = %+v", slot, drop)
	}
	if drop.PickupDelayTicks != sim.DropPickupDelayTicks {
		t.Fatalf("拾取延迟 = %d，想要 %d", drop.PickupDelayTicks, sim.DropPickupDelayTicks)
	}
	if got := currentInventory(t, engine, session).Hotbar; got != (core.Hotbar{}) {
		t.Fatalf("快捷栏 = %+v，想要保持为空", got)
	}
}

func TestMiningMergesIntoExistingDropAtSamePosition(t *testing.T) {
	engine, session := readyFlatPlayer(t)
	sequence := uint64(1)
	mineUntilComplete(t, engine, session, &sequence, 0, lookDown, 5)
	// 第二次挖掘同一列的下一格方块会落在不同位置，因此改为直接注入同位置堆。
	engine.SetChunkDropForTest(core.ChunkKey{Dimension: core.Overworld}, 0, world.DropSlot{
		Generation: 1, Active: true,
		Stack:      core.ItemStack{Item: core.ItemDirt, Count: 63},
		BlockIndex: dropTargetIndex(t), PickupDelayTicks: sim.DropPickupDelayTicks,
	})
	engine.SetBlockForTest(core.BlockPos{}, core.DirtID)

	result := mineUntilComplete(t, engine, session, &sequence, 0, lookDown, 5)
	if len(result.Rejected) != 0 {
		t.Fatalf("合并挖掘被拒绝: %+v", result.Rejected)
	}
	slot, drop := onlyDrop(t, engine)
	if slot != 0 || drop.Generation != 1 || drop.Stack.Count != core.MaxStackCount {
		t.Fatalf("合并后槽 %d = %+v，想要 64 个泥土且 ID 不变", slot, drop)
	}
}

func TestMiningRejectsWhenChunkDropsAreFull(t *testing.T) {
	engine, session := readyFlatPlayer(t)
	key := core.ChunkKey{Dimension: core.Overworld}
	elsewhere, ok := world.ChunkBlockIndex(core.BlockPos{X: 5, Y: 0, Z: 5})
	if !ok {
		t.Fatal("占位方块没有区块索引")
	}
	for slot := range core.DropsPerChunk {
		engine.SetChunkDropForTest(key, slot, world.DropSlot{
			Generation: 1, Active: true,
			Stack:      core.ItemStack{Item: core.ItemStone, Count: core.MaxStackCount},
			BlockIndex: elsewhere,
		})
	}

	sequence := uint64(1)
	result := mineUntilComplete(t, engine, session, &sequence, 0, lookDown, 5)
	if len(result.Rejected) != 1 || result.Rejected[0].Reason != sim.RejectDropCapacity {
		t.Fatalf("满掉落物槽 result=%+v", result)
	}
	if len(result.Changes) != 0 {
		t.Fatalf("被拒绝的挖掘修改了世界: %+v", result.Changes)
	}
	chunk, _, _ := engine.CloneReadyChunk(key)
	if chunk.BlockAt(0, 0, 0) != core.GrassID {
		t.Fatal("被拒绝的挖掘破坏了方块")
	}
}

func TestMiningSucceedsWithFullInventory(t *testing.T) {
	full := fullTestInventory()
	engine, session := readyFlatPlayerWithInventory(t, full)
	sequence := uint64(1)
	result := mineUntilComplete(t, engine, session, &sequence, 0, lookDown, 5)
	if len(result.Rejected) != 0 || len(result.Changes) != 1 {
		t.Fatalf("满快捷栏挖掘 result=%+v", result)
	}
	if _, drop := onlyDrop(t, engine); drop.Stack.Item != core.ItemGrass {
		t.Fatalf("满快捷栏挖掘没有产生掉落物: %+v", drop)
	}
	if got := currentInventory(t, engine, session); got != full {
		t.Fatalf("满物品状态被修改: %+v", got)
	}
}

func TestDropPickupWaitsForDelayThenFillsHotbar(t *testing.T) {
	engine, session := readyFlatPlayer(t)
	sequence := uint64(1)
	mineUntilComplete(t, engine, session, &sequence, 0, lookDown, 5)
	sequence++
	engine.Enqueue(sim.Command{
		Session: session, Sequence: sequence, Kind: sim.CommandPlayerInput,
		Pitch: lookDown, Mining: false,
	})

	for tick := range sim.DropPickupDelayTicks - 1 {
		engine.Step()
		if _, drop := onlyDrop(t, engine); !drop.Active {
			t.Fatalf("第 %d 个延迟 tick 掉落物被提前拾取", tick)
		}
		if got := currentInventory(t, engine, session).Hotbar; got != (core.Hotbar{}) {
			t.Fatalf("第 %d 个延迟 tick 快捷栏被修改: %+v", tick, got)
		}
	}

	result := engine.Step()
	chunk, _, _ := engine.CloneReadyChunk(core.ChunkKey{Dimension: core.Overworld})
	for slot := range core.DropsPerChunk {
		if chunk.Drop(slot).Active {
			t.Fatalf("延迟结束后掉落物未被拾取: 槽 %d = %+v", slot, chunk.Drop(slot))
		}
	}
	want := core.ItemStack{Item: core.ItemGrass, Count: 1}
	if got := currentInventory(t, engine, session).Hotbar; got.Slots[0] != want {
		t.Fatalf("拾取后快捷栏 = %+v，想要栏位 0 得到 1 个草", got)
	}
	if len(result.Inventories) != 1 {
		t.Fatalf("拾取应当发布一次快捷栏更新: %+v", result.Inventories)
	}
}

func TestToolDropsKeepDurabilityAcrossInventorySlots(t *testing.T) {
	engine, session := readyFlatPlayer(t)
	first := core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: 73}
	second := core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: 41}
	engine.SetChunkDropForTest(core.ChunkKey{Dimension: core.Overworld}, 0, world.DropSlot{
		Generation: 1, Active: true,
		Stack:      first,
		BlockIndex: dropTargetIndex(t),
	})
	engine.SetChunkDropForTest(core.ChunkKey{Dimension: core.Overworld}, 1, world.DropSlot{
		Generation: 1, Active: true,
		Stack:      second,
		BlockIndex: dropTargetIndex(t),
	})

	result := engine.Step()
	chunk, _, ok := engine.CloneReadyChunk(core.ChunkKey{Dimension: core.Overworld})
	if !ok {
		t.Fatal("中心区块不可用")
	}
	for slot := range 2 {
		if drop := chunk.Drop(slot); drop.Active {
			t.Fatalf("拾取后掉落物槽 %d 仍活动: %+v", slot, drop)
		}
	}
	if got := currentInventory(t, engine, session).Hotbar; got.Slots[0] != first || got.Slots[1] != second {
		t.Fatalf("拾取后快捷栏 = %+v，想要分别保留耐久 %+v / %+v", got, first, second)
	}
	if len(result.Inventories) != 1 {
		t.Fatalf("拾取应当只发布一次最终背包: %+v", result.Inventories)
	}
}

func TestDropPartialPickupKeepsRemainder(t *testing.T) {
	// 快捷栏与背包都装满，只在一格留下 2 个空间。
	nearlyFull := fullTestInventory()
	nearlyFull.Hotbar.Slots[3] = core.ItemStack{
		Item: core.ItemGrass, Count: core.MaxStackCount - 2,
	}
	engine, session := readyFlatPlayerWithInventory(t, nearlyFull)
	engine.SetChunkDropForTest(core.ChunkKey{Dimension: core.Overworld}, 0, world.DropSlot{
		Generation: 1, Active: true,
		Stack:      core.ItemStack{Item: core.ItemGrass, Count: 5},
		BlockIndex: dropTargetIndex(t),
	})

	engine.Step()
	slot, drop := onlyDrop(t, engine)
	if slot != 0 || drop.Stack.Count != 3 || drop.Generation != 1 {
		t.Fatalf("部分拾取后槽 %d = %+v，想要保留 3 个草", slot, drop)
	}
	if got := currentInventory(t, engine, session).Hotbar; got.Slots[3].Count != core.MaxStackCount {
		t.Fatalf("部分拾取后快捷栏 = %+v，想要栏位 3 装满", got)
	}
}

func TestDropDoesNotMoveWhenInventoryIsFull(t *testing.T) {
	full := fullTestInventory()
	engine, session := readyFlatPlayerWithInventory(t, full)
	engine.SetChunkDropForTest(core.ChunkKey{Dimension: core.Overworld}, 0, world.DropSlot{
		Generation: 1, Active: true,
		Stack:      core.ItemStack{Item: core.ItemGrass, Count: 2},
		BlockIndex: dropTargetIndex(t),
	})

	engine.Step()
	if _, drop := onlyDrop(t, engine); drop.Stack.Count != 2 {
		t.Fatalf("满快捷栏改变了掉落物数量: %+v", drop)
	}
	if got := currentInventory(t, engine, session); got != full {
		t.Fatalf("满物品状态被修改: %+v", got)
	}
}

func TestDropExpiresAfterLifetime(t *testing.T) {
	engine, session := readyFlatPlayerRestored(t, nil, core.Hotbar{})
	// 用满快捷栏之外的方式避免拾取：把掉落物放在拾取范围之外。
	far, ok := world.ChunkBlockIndex(core.BlockPos{X: 8, Y: 0, Z: 8})
	if !ok {
		t.Fatal("远端方块没有区块索引")
	}
	engine.SetChunkDropForTest(core.ChunkKey{Dimension: core.Overworld}, 0, world.DropSlot{
		Generation: 1, Active: true,
		Stack:      core.ItemStack{Item: core.ItemGrass, Count: 1},
		BlockIndex: far, AgeTicks: sim.DropLifetimeTicks - 2,
	})

	engine.Step()
	if _, drop := onlyDrop(t, engine); drop.AgeTicks != sim.DropLifetimeTicks-1 {
		t.Fatalf("寿命推进错误: %+v", drop)
	}
	result := engine.Step()
	chunk, _, _ := engine.CloneReadyChunk(core.ChunkKey{Dimension: core.Overworld})
	if chunk.Drop(0).Active {
		t.Fatalf("到达寿命后掉落物未移除: %+v", chunk.Drop(0))
	}
	if got := currentInventory(t, engine, session).Hotbar; got != (core.Hotbar{}) {
		t.Fatalf("过期向快捷栏添加了物品: %+v", got)
	}
	if len(result.Changes) != 1 || len(result.Changes[0].Changes) != 0 {
		t.Fatalf("过期应当发布零方块 revision barrier: %+v", result.Changes)
	}
}

func TestDropAgePausesOutsideInterestRadius(t *testing.T) {
	// 地形视距大于掉落物兴趣半径，使远处区块保持 Ready 但不参与掉落物 tick。
	engine, _ := readyWideViewPlayer(t, sim.DropInterestRadius+1)
	far := core.ChunkPos{X: sim.DropInterestRadius + 1}
	key := core.ChunkKey{Dimension: core.Overworld, Pos: far}
	index, ok := world.ChunkBlockIndex(core.BlockPos{X: far.X << core.SectionShift, Y: 0})
	if !ok {
		t.Fatal("远端方块没有区块索引")
	}
	engine.SetChunkDropForTest(key, 0, world.DropSlot{
		Generation: 1, Active: true,
		Stack:      core.ItemStack{Item: core.ItemGrass, Count: 1},
		BlockIndex: index, AgeTicks: 40, PickupDelayTicks: 3,
	})

	for range 5 {
		engine.Step()
	}
	chunk, _, ok := engine.CloneReadyChunk(key)
	if !ok {
		t.Fatal("远端区块不可用")
	}
	drop := chunk.Drop(0)
	if drop.AgeTicks != 40 || drop.PickupDelayTicks != 3 {
		t.Fatalf("兴趣范围外寿命仍在推进: %+v", drop)
	}
}

// readyWideViewPlayer 构造一个视距大于掉落物半径且全部区块已 Ready 的引擎。
func readyWideViewPlayer(t *testing.T, viewRadius int) (*sim.Engine, sim.SessionID) {
	t.Helper()
	engine := sim.NewEngine(viewRadius, 0)
	const session = sim.SessionID(1)
	engine.RegisterSession(session, core.Overworld, core.ChunkPos{})
	for range 4 * viewRadius {
		result := engine.Step()
		submitAcquiredMisses(engine, result.Acquire)
		for _, key := range result.Generate {
			engine.SubmitGenerated(sim.GeneratedChunk{
				Dimension: key.Dimension, Pos: key.Pos, Chunk: generateFlatChunk(key.Pos),
			})
		}
	}
	for range 4 {
		result := engine.Step()
		submitAcquiredMisses(engine, result.Acquire)
		for _, key := range result.Generate {
			engine.SubmitGenerated(sim.GeneratedChunk{
				Dimension: key.Dimension, Pos: key.Pos, Chunk: generateFlatChunk(key.Pos),
			})
		}
	}
	if player, ok := engine.Player(session); !ok || !player.Ready {
		t.Fatalf("玩家未 Ready: %+v", player)
	}
	return engine, session
}

func TestPlaceBeforeDropUsesGlobalSequenceOrder(t *testing.T) {
	var hotbar core.Hotbar
	hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 1}
	target := core.BlockPos{X: 0, Y: 2, Z: 3}
	want := core.BlockPos{X: 0, Y: 2, Z: 2}
	engine, session := readyFlatPlayerRestored(t, map[core.BlockPos]core.BlockID{
		target: core.StoneID,
	}, hotbar)
	engine.Enqueue(sim.Command{
		Session: session, Sequence: 2, Kind: sim.CommandPlaceBlock,
		Yaw: float32(math.Pi), Slot: 0,
	})
	engine.Enqueue(sim.Command{
		Session: session, Sequence: 3, Kind: sim.CommandDropSelectedItem,
	})

	result := engine.Step()
	if len(result.Rejected) != 1 || result.Rejected[0] != (sim.Rejection{
		Session: session, Sequence: 3, Reason: sim.RejectInvalidSlot,
	}) {
		t.Fatalf("拒绝 = %+v，想要丢弃以 invalid_slot 拒绝", result.Rejected)
	}
	if len(result.Changes) != 1 || len(result.Changes[0].Changes) != 1 ||
		result.Changes[0].Changes[0] != (sim.BlockChange{Position: want, Block: core.StoneID}) {
		t.Fatalf("放置变更 = %+v", result.Changes)
	}
	if got := currentInventory(t, engine, session); got != (core.Inventory{}) {
		t.Fatalf("背包 = %+v，想要清空", got)
	}
	chunk, _, ok := engine.CloneReadyChunk(core.ChunkKey{Dimension: core.Overworld})
	if !ok {
		t.Fatal("中心区块不可用")
	}
	for slot := range core.DropsPerChunk {
		if drop := chunk.Drop(slot); drop.Active {
			t.Fatalf("地面不应有掉落物：槽 %d = %+v", slot, drop)
		}
	}
}

func TestDropSelectedItemTransfersOneAuthoritativeItem(t *testing.T) {
	for _, item := range []core.ItemID{
		core.ItemStone,
		core.ItemCoal,
		core.ItemStonePickaxe,
		core.ItemIronPickaxe,
	} {
		t.Run(fmt.Sprint(item), func(t *testing.T) {
			durability, _ := core.ItemMaxDurability(item)
			if durability > 0 {
				durability--
			}
			stack := core.ItemStack{Item: item, Count: 1, Durability: durability}
			inventory := core.Inventory{Hotbar: core.Hotbar{Selected: 2}}
			inventory.Hotbar.Slots[2] = stack
			engine, session := readyFlatPlayerWithInventory(t, inventory)
			engine.Enqueue(sim.Command{
				Session: session, Sequence: 1, Kind: sim.CommandDropSelectedItem,
			})
			result := engine.Step()
			if len(result.Rejected) != 0 || len(result.Inventories) != 1 {
				t.Fatalf("result = %+v", result)
			}
			// 最后一件被丢弃后来源栏位清空。
			if got := currentInventory(t, engine, session).Hotbar.Slots[2]; got != (core.ItemStack{}) {
				t.Fatalf("来源栏位 = %+v", got)
			}
			_, drop := onlyDrop(t, engine)
			// 创建 tick 立即计入第一个活动 tick，因此 step 后剩余 39。
			if drop.Stack != stack ||
				drop.PickupDelayTicks != sim.PlayerDropPickupDelayTicks-1 {
				t.Fatalf("主动掉落 = %+v", drop)
			}
		})
	}
}

func TestDropSelectedItemKeepsRemainingCount(t *testing.T) {
	inventory := core.Inventory{Hotbar: core.Hotbar{Selected: 0}}
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 3}
	engine, session := readyFlatPlayerWithInventory(t, inventory)
	engine.Enqueue(sim.Command{
		Session: session, Sequence: 1, Kind: sim.CommandDropSelectedItem,
	})
	if result := engine.Step(); len(result.Rejected) != 0 {
		t.Fatalf("被拒绝 = %+v", result.Rejected)
	}
	if got := currentInventory(t, engine, session).Hotbar.Slots[0]; got.Count != 2 {
		t.Fatalf("剩余数量 = %+v，想要 2", got)
	}
	if _, drop := onlyDrop(t, engine); drop.Stack.Count != 1 {
		t.Fatalf("掉落数量 = %+v，想要 1", drop.Stack)
	}
}

func TestDropSelectedItemRejectionsLeaveAuthorityUnchanged(t *testing.T) {
	t.Run("空选中栏位", func(t *testing.T) {
		engine, session := readyFlatPlayerWithInventory(t, core.Inventory{})
		before, _ := engine.PlayerHash(session)
		engine.Enqueue(sim.Command{
			Session: session, Sequence: 1, Kind: sim.CommandDropSelectedItem,
		})
		result := engine.Step()
		if len(result.Rejected) != 1 || result.Rejected[0].Reason != sim.RejectInvalidSlot {
			t.Fatalf("拒绝 = %+v，想要 invalid_slot", result.Rejected)
		}
		if len(result.Inventories) != 0 {
			t.Fatalf("拒绝后发布了背包更新：%+v", result.Inventories)
		}
		if after, _ := engine.PlayerHash(session); after != before {
			t.Fatal("拒绝改变了权威玩家状态")
		}
	})
}
