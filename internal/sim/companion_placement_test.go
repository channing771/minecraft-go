package sim

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
)

// companionPlacementFixture 是一组伙伴放置用例的公共场景：平地 3x3 区块上一个
// 已激活的伙伴站在 (4.5, 1, 8.5)，放置目标固定在 (4, 1, 5)（空气、视线无遮挡）。
// 伙伴背包按用例自行填充；放置经完整 Step 与 Place action 建立，覆盖 action
// 分派到结算的整条链路。
type companionPlacementFixture struct {
	engine *Engine
	id     companion.ID
	entry  *companionState
	target core.BlockPos
}

// readyCompanionPlacement 构造一个站在标准位置、面向空气目标的伙伴放置场景。
func readyCompanionPlacement(t *testing.T) companionPlacementFixture {
	t.Helper()
	engine := NewEngine(0, 0, 0)
	loadCompanionFlatChunks(t, engine, core.ChunkPos{}, 1)
	id := companionTestID(1)
	activateCompanionAt(t, engine, id, mgl32.Vec3{4.5, 1, 8.5})
	target := core.BlockPos{X: 4, Y: 1, Z: 5}
	return companionPlacementFixture{
		engine: engine, id: id, entry: engine.companions[id], target: target,
	}
}

// placeCompanionAction 提交一个 Place action 并步进一个完整 tick。
func placeCompanionAction(
	t *testing.T,
	fixture companionPlacementFixture,
	block core.BlockID,
	target core.BlockPos,
) TickResult {
	t.Helper()
	if !fixture.engine.EnqueueCompanionAction(CompanionAction{
		ID: fixture.id, Kind: CompanionActionPlace, Target: target, Block: block,
	}) {
		t.Fatal("Place action 未入队")
	}
	return fixture.engine.Step()
}

// companionPlaceBlockAt 读取目标方块的当前值，区块未就绪时直接失败。
func companionPlaceBlockAt(t *testing.T, engine *Engine, target core.BlockPos) core.BlockID {
	t.Helper()
	record := miningTargetRecord(t, engine, target)
	x, _, z := target.Local()
	return record.Chunk.BlockAt(x, target.Y, z)
}

// assertCompanionPlaceRejected 断言一次放置被零副作用拒绝：目标方块不变、背包
// 不变、不标脏、不发布区块变更，也不产生任何拒绝记录（伙伴 action 的拒绝不进入
// result.Rejected，"任务失败"判定属于 Task 7 的 Manager）。
func assertCompanionPlaceRejected(
	t *testing.T,
	fixture companionPlacementFixture,
	wantBlock core.BlockID,
	result TickResult,
) {
	t.Helper()
	if got := companionPlaceBlockAt(t, fixture.engine, fixture.target); got != wantBlock {
		t.Fatalf("被拒绝的放置改变了世界=%d，想要 %d", got, wantBlock)
	}
	if !fixture.entry.inventory.Valid() {
		t.Fatal("被拒绝的放置破坏了背包规范性")
	}
	if fixture.entry.inventoryDirty {
		t.Fatal("被拒绝的放置标记了 inventoryDirty")
	}
	if len(result.Changes) != 0 {
		t.Fatalf("被拒绝的放置发布了区块变更=%+v", result.Changes)
	}
	if len(result.Rejected) != 0 {
		t.Fatalf("伙伴 action 产生了拒绝记录=%+v", result.Rejected)
	}
}

// TestCompanionPlaceAtomicSingleTick 锁定放置成功的单 tick 原子性：目标出现方块、
// 背包对应堆恰好减少一件、inventoryDirty 置位与区块变更发布必须在同一权威 tick
// 内全部成立；放置没有进度语义，绝不跨 tick。
func TestCompanionPlaceAtomicSingleTick(t *testing.T) {
	fixture := readyCompanionPlacement(t)
	fixture.entry.inventory.Hotbar.Slots[1] = core.ItemStack{
		Item: core.ItemOakPlanks, Count: 3,
	}

	result := placeCompanionAction(t, fixture, core.OakPlanksID, fixture.target)

	if got := companionPlaceBlockAt(t, fixture.engine, fixture.target); got != core.OakPlanksID {
		t.Fatalf("目标方块=%d，想要橡木板", got)
	}
	stack := fixture.entry.inventory.Hotbar.Slots[1]
	if stack.Item != core.ItemOakPlanks || stack.Count != 2 {
		t.Fatalf("对应堆应恰好减少一件: got=%+v", stack)
	}
	if got := companionItemCount(fixture.entry, core.ItemOakPlanks); got != 2 {
		t.Fatalf("全背包橡木板=%d，想要 2", got)
	}
	if !fixture.entry.inventory.Valid() {
		t.Fatal("放置后的背包不规范")
	}
	if !fixture.entry.inventoryDirty {
		t.Fatal("放置成功没有标记 inventoryDirty")
	}
	if len(result.Changes) != 1 || len(result.Changes[0].Changes) != 1 ||
		result.Changes[0].Changes[0].Position != fixture.target ||
		result.Changes[0].Changes[0].Block != core.OakPlanksID {
		t.Fatalf("放置未在同一 tick 发布单一区块变更=%+v", result.Changes)
	}
	if len(result.Rejected) != 0 {
		t.Fatalf("成功放置产生了拒绝记录=%+v", result.Rejected)
	}
}

// TestCompanionPlaceValidationFailuresKeepInventory 锁定校验失败不扣料：目标被占、
// 与放置者自身碰撞、区块未 Ready、方块不在可放置注册表内（注册过但不可由物品
// 还原，如矿石）时，放置必须零副作用——世界不变、背包不变、不标脏、无变更发布。
func TestCompanionPlaceValidationFailuresKeepInventory(t *testing.T) {
	t.Run("目标被其他方块占据", func(t *testing.T) {
		fixture := readyCompanionPlacement(t)
		fixture.engine.SetBlockForTest(fixture.target, core.StoneID)
		fixture.entry.inventory.Hotbar.Slots[1] = core.ItemStack{
			Item: core.ItemOakPlanks, Count: 3,
		}
		before := fixture.entry.inventory

		result := placeCompanionAction(t, fixture, core.OakPlanksID, fixture.target)

		if fixture.entry.inventory != before {
			t.Fatalf("目标被占仍扣料: got=%+v", fixture.entry.inventory)
		}
		assertCompanionPlaceRejected(t, fixture, core.StoneID, result)
	})

	t.Run("目标与放置者自身碰撞", func(t *testing.T) {
		fixture := readyCompanionPlacement(t)
		// (4,1,8) 是伙伴脚下的空气格，放置实体方块会与伙伴身体重叠。
		collision := core.BlockPos{X: 4, Y: 1, Z: 8}
		fixture.entry.inventory.Hotbar.Slots[1] = core.ItemStack{
			Item: core.ItemStone, Count: 3,
		}
		before := fixture.entry.inventory

		result := placeCompanionAction(t, fixture, core.StoneID, collision)

		if fixture.entry.inventory != before {
			t.Fatalf("碰撞目标仍扣料: got=%+v", fixture.entry.inventory)
		}
		if got := companionPlaceBlockAt(t, fixture.engine, collision); got != core.AirID {
			t.Fatalf("碰撞目标被写入方块=%d，想要空气", got)
		}
		if fixture.entry.inventoryDirty || len(result.Changes) != 0 || len(result.Rejected) != 0 {
			t.Fatalf("碰撞放置产生副作用: dirty=%v changes=%+v rejected=%+v",
				fixture.entry.inventoryDirty, result.Changes, result.Rejected)
		}
	})

	t.Run("目标区块未 Ready", func(t *testing.T) {
		fixture := readyCompanionPlacement(t)
		// chunk (2,2) 在 fixture 的 3x3 平地之外，始终未 Ready。
		target := core.BlockPos{X: 36, Y: 1, Z: 36}
		fixture.entry.inventory.Hotbar.Slots[1] = core.ItemStack{
			Item: core.ItemOakPlanks, Count: 3,
		}
		before := fixture.entry.inventory

		result := placeCompanionAction(t, fixture, core.OakPlanksID, target)

		if fixture.entry.inventory != before {
			t.Fatalf("未 Ready 目标仍扣料: got=%+v", fixture.entry.inventory)
		}
		if len(result.Changes) != 0 || len(result.Rejected) != 0 {
			t.Fatalf("未 Ready 放置产生副作用: changes=%+v rejected=%+v",
				result.Changes, result.Rejected)
		}
		if fixture.entry.inventoryDirty {
			t.Fatal("未 Ready 放置标记了 inventoryDirty")
		}
	})

	t.Run("方块不可由物品放置还原", func(t *testing.T) {
		fixture := readyCompanionPlacement(t)
		// 煤矿石是已注册方块，但没有物品能放置成它——必须被注册表拒绝。
		fixture.entry.inventory.Hotbar.Slots[1] = core.ItemStack{
			Item: core.ItemCoal, Count: 3,
		}
		before := fixture.entry.inventory

		result := placeCompanionAction(t, fixture, core.CoalOreID, fixture.target)

		if fixture.entry.inventory != before {
			t.Fatalf("不可放置方块仍扣料: got=%+v", fixture.entry.inventory)
		}
		assertCompanionPlaceRejected(t, fixture, core.AirID, result)
	})
}

// TestCompanionPlaceInsufficientItemsRejectedZeroSideEffects 锁定物品不足由 action
// 语义拒绝：背包没有对应物品时放置是零副作用的不结算，sim 侧无 Rejection 记录，
// "任务失败"判定属于 Task 7 的 Manager。
func TestCompanionPlaceInsufficientItemsRejectedZeroSideEffects(t *testing.T) {
	fixture := readyCompanionPlacement(t)
	// 背包只有泥土，没有橡木板。
	fixture.entry.inventory.Hotbar.Slots[0] = core.ItemStack{
		Item: core.ItemDirt, Count: 2,
	}
	before := fixture.entry.inventory

	result := placeCompanionAction(t, fixture, core.OakPlanksID, fixture.target)

	if fixture.entry.inventory != before {
		t.Fatalf("物品不足仍改变背包: got=%+v", fixture.entry.inventory)
	}
	assertCompanionPlaceRejected(t, fixture, core.AirID, result)
}

// TestCompanionPlaceConsumesFirstMatchingStack 锁定物品选择语义：按统一索引
// （快捷栏 0..8 在前、背包 9..35 在后）扣除首个对应物品堆，跳过其他物品的堆，
// 后续同物品堆保持不变；首个对应堆也可以出现在背包段。
func TestCompanionPlaceConsumesFirstMatchingStack(t *testing.T) {
	t.Run("多堆时扣统一索引首个", func(t *testing.T) {
		fixture := readyCompanionPlacement(t)
		entry := fixture.entry
		entry.inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemDirt, Count: 2}
		entry.inventory.Hotbar.Slots[2] = core.ItemStack{Item: core.ItemOakPlanks, Count: 5}
		entry.inventory.Backpack[3] = core.ItemStack{Item: core.ItemOakPlanks, Count: 7}

		placeCompanionAction(t, fixture, core.OakPlanksID, fixture.target)

		if got := entry.inventory.Hotbar.Slots[0]; got.Item != core.ItemDirt || got.Count != 2 {
			t.Fatalf("无关物品堆被动过: %+v", got)
		}
		if got := entry.inventory.Hotbar.Slots[2]; got.Count != 4 {
			t.Fatalf("首个对应堆未扣一件: %+v", got)
		}
		if got := entry.inventory.Backpack[3]; got.Count != 7 {
			t.Fatalf("后续同物品堆被动过: %+v", got)
		}
	})

	t.Run("对应堆只在背包时扣背包首堆", func(t *testing.T) {
		fixture := readyCompanionPlacement(t)
		entry := fixture.entry
		entry.inventory.Backpack[1] = core.ItemStack{Item: core.ItemSand, Count: 1}
		entry.inventory.Backpack[4] = core.ItemStack{Item: core.ItemSand, Count: 9}

		placeCompanionAction(t, fixture, core.SandID, fixture.target)

		if got := entry.inventory.Backpack[1]; got != (core.ItemStack{}) {
			t.Fatalf("唯一一件应清空栏位: %+v", got)
		}
		if got := entry.inventory.Backpack[4]; got.Count != 9 {
			t.Fatalf("后续堆被动过: %+v", got)
		}
		if got := companionItemCount(entry, core.ItemSand); got != 9 {
			t.Fatalf("扣除后总数=%d，想要 9", got)
		}
	})
}

// TestCompanionPlaceThenMineClosesLoop 抽查放置与采掘的链路闭环：伙伴放置的方块
// 随后可被同一套玩家采掘规则破坏，产物按伙伴完成分叉直入背包——放置写入的方块
// 与天然方块在采掘语义下不可区分。
func TestCompanionPlaceThenMineClosesLoop(t *testing.T) {
	fixture := readyCompanionPlacement(t)
	entry := fixture.entry
	entry.inventory.Hotbar.Slots[1] = core.ItemStack{Item: core.ItemSand, Count: 1}

	placed := placeCompanionAction(t, fixture, core.SandID, fixture.target)
	if len(placed.Changes) != 1 {
		t.Fatalf("放置未成交: %+v", placed.Changes)
	}
	if got := companionItemCount(entry, core.ItemSand); got != 0 {
		t.Fatalf("放置后背包应无沙子: %d", got)
	}

	// 空手采掘沙子 5 tick；经 MineHold action 走完整链路。
	for tick := 1; tick <= 5; tick++ {
		result := holdCompanionMineAction(t, companionMiningFixture{
			engine: fixture.engine, id: fixture.id, entry: entry, target: fixture.target,
		})
		if tick < 5 {
			if got := companionPlaceBlockAt(t, fixture.engine, fixture.target); got != core.SandID {
				t.Fatalf("tick %d 沙子提前破坏=%d", tick, got)
			}
			continue
		}
		if len(result.Changes) != 1 || result.Changes[0].Changes[0].Block != core.AirID {
			t.Fatalf("完成 tick 未发布空气变更: %+v", result.Changes)
		}
	}
	if got := companionPlaceBlockAt(t, fixture.engine, fixture.target); got != core.AirID {
		t.Fatalf("采掘完成后方块=%d，想要空气", got)
	}
	if got := companionItemCount(entry, core.ItemSand); got != 1 {
		t.Fatalf("采掘产物未直入背包: %d，想要 1", got)
	}
}
