package sim

import (
	"fmt"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
)

// companionMiningFixture 是一组伙伴采掘用例的公共场景：平地 3x3 区块上一个
// 已激活的伙伴站在 (4.5, 1, 8.5)，目标方块固定在 (4, 1, 5)，两者水平距离约
// 3 格、视线无遮挡，位于默认 InteractionReach 之内。伙伴手握指定工具
// （hotbar 栏位 0 选中），采掘意图直接写入共享的 actorState 字段——与既有
// 玩家采掘测试直接写 player.miningHeld 完全对称，action 载荷分派另行由
// TestCompanionActionMiningPayloadsSetAndClearIntent 与发布用例经完整 Step 覆盖。
type companionMiningFixture struct {
	engine *Engine
	id     companion.ID
	entry  *companionState
	target core.BlockPos
}

// companionItemCount 统计伙伴完整背包中某物品的总数。
func companionItemCount(entry *companionState, item core.ItemID) uint8 {
	var total uint8
	for slot := range entry.inventory.Hotbar.Slots {
		if entry.inventory.Hotbar.Slots[slot].Item == item {
			total += entry.inventory.Hotbar.Slots[slot].Count
		}
	}
	for slot := range entry.inventory.Backpack {
		if entry.inventory.Backpack[slot].Item == item {
			total += entry.inventory.Backpack[slot].Count
		}
	}
	return total
}

// companionMiningBlockAt 读取目标方块的当前值，区块未就绪时直接失败。
func companionMiningBlockAt(t *testing.T, fixture companionMiningFixture) core.BlockID {
	t.Helper()
	record := miningTargetRecord(t, fixture.engine, fixture.target)
	x, _, z := fixture.target.Local()
	return record.Chunk.BlockAt(x, fixture.target.Y, z)
}

// fillCompanionInventory 把伙伴背包除工具栏位外的全部格子填满同类物品，
// 用于构造"无容量"场景。
func fillCompanionInventory(entry *companionState, item core.ItemID) {
	stack := core.ItemStack{Item: item, Count: core.MaxStackCount}
	for slot := 1; slot < core.HotbarSlots; slot++ {
		entry.inventory.Hotbar.Slots[slot] = stack
	}
	for slot := range entry.inventory.Backpack {
		entry.inventory.Backpack[slot] = stack
	}
}

// readyCompanionMining 构造一个手握指定工具、面向目标方块、已置采掘意图的
// 伙伴采掘场景。
func readyCompanionMining(
	t *testing.T,
	block core.BlockID,
	tool core.ItemID,
) companionMiningFixture {
	t.Helper()
	engine := NewEngine(0, 0)
	loadCompanionFlatChunks(t, engine, core.ChunkPos{}, 1)
	id := companionTestID(1)
	activateCompanionAt(t, engine, id, mgl32.Vec3{4.5, 1, 8.5})
	target := core.BlockPos{X: 4, Y: 1, Z: 5}
	engine.SetBlockForTest(target, block)
	entry := engine.companions[id]
	entry.pitch = 0
	if tool == core.ItemNone {
		entry.inventory.Hotbar.Slots[0] = core.ItemStack{}
	} else {
		full, _ := core.ItemMaxDurability(tool)
		entry.inventory.Hotbar.Slots[0] = core.ItemStack{
			Item: tool, Count: 1, Durability: full,
		}
	}
	entry.inventory.Hotbar.Selected = 0
	entry.miningHeld = true
	entry.miningTarget = target
	return companionMiningFixture{engine: engine, id: id, entry: entry, target: target}
}

// readyCompanionMiningAtTarget 与 readyCompanionMining 相同，但经完整 Step 与
// MineHold action 建立采掘意图，用于验证 action 载荷与 CompanionUpdate 发布。
func readyCompanionMiningViaActions(
	t *testing.T,
	block core.BlockID,
	tool core.ItemID,
) companionMiningFixture {
	t.Helper()
	engine := NewEngine(0, 0)
	loadCompanionFlatChunks(t, engine, core.ChunkPos{}, 1)
	id := companionTestID(1)
	activateCompanionAt(t, engine, id, mgl32.Vec3{4.5, 1, 8.5})
	target := core.BlockPos{X: 4, Y: 1, Z: 5}
	engine.SetBlockForTest(target, block)
	entry := engine.companions[id]
	if tool == core.ItemNone {
		entry.inventory.Hotbar.Slots[0] = core.ItemStack{}
	} else {
		full, _ := core.ItemMaxDurability(tool)
		entry.inventory.Hotbar.Slots[0] = core.ItemStack{
			Item: tool, Count: 1, Durability: full,
		}
	}
	entry.inventory.Hotbar.Selected = 0
	return companionMiningFixture{engine: engine, id: id, entry: entry, target: target}
}

// holdCompanionMineAction 提交一个 MineHold action 并步进一个完整 tick。
func holdCompanionMineAction(t *testing.T, fixture companionMiningFixture) TickResult {
	t.Helper()
	if !fixture.engine.EnqueueCompanionAction(CompanionAction{
		ID: fixture.id, Kind: CompanionActionMineHold, Target: fixture.target,
	}) {
		t.Fatal("MineHold action 未入队")
	}
	return fixture.engine.Step()
}

// TestCompanionMiningMatchesPlayerRuleAndTiming 是"伙伴与玩家共用同一采掘规则"
// 的差分证据：同引擎内一名玩家与一个伙伴以相同工具（石镐）对相同方块（煤矿石）
// 持续采掘，完成时机与耐久扣减必须完全一致，差别仅在产物去向——玩家得到掉落物、
// 伙伴产物直入背包。
func TestCompanionMiningMatchesPlayerRuleAndTiming(t *testing.T) {
	engine, _ := readyMovementPlayer(t)
	session := SessionID(1)

	id := companionTestID(1)
	activateCompanionAt(t, engine, id, mgl32.Vec3{4.5, 1, 8.5})
	playerTarget := core.BlockPos{X: 0, Y: 1, Z: 5}
	companionTarget := core.BlockPos{X: 4, Y: 1, Z: 5}
	engine.SetBlockForTest(playerTarget, core.CoalOreID)
	engine.SetBlockForTest(companionTarget, core.CoalOreID)

	player := engine.sessions[session].player
	player.state.Position = mgl32.Vec3{0.5, 1, 8.5}
	player.yaw = 0
	player.pitch = miningTestPitch
	player.miningHeld = true
	player.lastInputSequence = 10
	player.reset = false
	setMiningHeldItem(player, core.ItemStonePickaxe)

	entry := engine.companions[id]
	entry.pitch = 0
	full, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	entry.inventory.Hotbar.Slots[0] = core.ItemStack{
		Item: core.ItemStonePickaxe, Count: 1, Durability: full,
	}
	entry.inventory.Hotbar.Selected = 0
	entry.miningHeld = true
	entry.miningTarget = companionTarget

	const requiredTicks = 15
	for tick := 1; tick < requiredTicks; tick++ {
		advanceMiningOnce(engine)
		if got := engine.sessions[session].player.mining.progressTicks; got != uint16(tick) {
			t.Fatalf("tick %d 玩家进度=%d", tick, got)
		}
		if got := entry.mining.progressTicks; got != uint16(tick) {
			t.Fatalf("tick %d 伙伴进度=%d，想要与玩家一致", tick, got)
		}
		if got := player.inventory.Hotbar.Slots[0].Durability; got != full {
			t.Fatalf("tick %d 玩家耐久提前扣减=%d", tick, got)
		}
		if got := entry.inventory.Hotbar.Slots[0].Durability; got != full {
			t.Fatalf("tick %d 伙伴耐久提前扣减=%d", tick, got)
		}
	}
	advanceMiningOnce(engine)

	if got := player.inventory.Hotbar.Slots[0].Durability; got != full-1 {
		t.Fatalf("玩家完成耐久=%d，想要 %d", got, full-1)
	}
	if got := entry.inventory.Hotbar.Slots[0].Durability; got != full-1 {
		t.Fatalf("伙伴完成耐久=%d，想要与玩家一致 %d", got, full-1)
	}
	// 两个目标同在区块 (0,0)：世界掉落物必须恰好只有玩家那一份——
	// 伙伴的产物只能出现在背包里，绝不能额外进入世界。
	drops := miningDropTotals(miningTargetRecord(t, engine, companionTarget).Chunk)
	if drops[core.ItemCoal] != 1 || len(drops) != 1 {
		t.Fatalf("世界掉落物=%+v，想要恰好玩家的一份煤矿石", drops)
	}
	if got := companionItemCount(entry, core.ItemCoal); got != 1 {
		t.Fatalf("伙伴产物应直入背包: coal=%d", got)
	}
}

// TestCompanionMiningCompletionIsThreeWayAtomic 锁定完成 tick 的三方原子性：
// 完成前的每个 tick 方块、耐久与背包都必须保持不变；进度的最后一个 tick 内
// 方块变空气、耐久扣减、产物入包必须同时成立，区块变更发布也在同一批
// pending 变更中。
func TestCompanionMiningCompletionIsThreeWayAtomic(t *testing.T) {
	fixture := readyCompanionMining(t, core.CoalOreID, core.ItemStonePickaxe)
	entry := fixture.entry
	full, _ := core.ItemMaxDurability(core.ItemStonePickaxe)

	for tick := 1; tick < 15; tick++ {
		result := advanceMiningOnce(fixture.engine)
		if got := companionMiningBlockAt(t, fixture); got != core.CoalOreID {
			t.Fatalf("tick %d 方块提前破坏=%d", tick, got)
		}
		if got := entry.inventory.Hotbar.Slots[0].Durability; got != full {
			t.Fatalf("tick %d 耐久提前扣减=%d", tick, got)
		}
		if got := companionItemCount(entry, core.ItemCoal); got != 0 {
			t.Fatalf("tick %d 产物提前入包=%d", tick, got)
		}
		if len(result.Changes) != 0 {
			t.Fatalf("tick %d 提前发布区块变更=%+v", tick, result.Changes)
		}
	}

	result := advanceMiningOnce(fixture.engine)
	if got := companionMiningBlockAt(t, fixture); got != core.AirID {
		t.Fatalf("完成 tick 方块=%d，想要空气", got)
	}
	if got := entry.inventory.Hotbar.Slots[0].Durability; got != full-1 {
		t.Fatalf("完成 tick 耐久=%d，想要 %d", got, full-1)
	}
	if got := companionItemCount(entry, core.ItemCoal); got != 1 {
		t.Fatalf("完成 tick 产物未入包: coal=%d", got)
	}
	if len(result.Changes) != 1 || len(result.Changes[0].Changes) != 1 ||
		result.Changes[0].Changes[0].Position != fixture.target ||
		result.Changes[0].Changes[0].Block != core.AirID {
		t.Fatalf("完成 tick 区块变更=%+v，想要单一空气变更", result.Changes)
	}
	if !entry.inventoryDirty {
		t.Fatal("完成 tick 没有标记 inventoryDirty")
	}
	if entry.mining != (miningState{}) {
		t.Fatalf("完成后进度未清零: %+v", entry.mining)
	}
}

// TestCompanionMiningInventoryFullKeepsBlockAndSaturatesProgress 锁定容量前验：
// 伙伴背包无容量时完成 tick 整体不结算——方块不变、耐久不变、背包不变，进度
// 保持满格（就绪但无容量的稳定可观察状态，"任务失败"判定属 Manager，不在此处）。
func TestCompanionMiningInventoryFullKeepsBlockAndSaturatesProgress(t *testing.T) {
	fixture := readyCompanionMining(t, core.CoalOreID, core.ItemStonePickaxe)
	entry := fixture.entry
	full, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	fillCompanionInventory(entry, core.ItemDirt)
	before := entry.inventory

	for tick := 0; tick < 25; tick++ {
		advanceMiningOnce(fixture.engine)
		if got := companionMiningBlockAt(t, fixture); got != core.CoalOreID {
			t.Fatalf("tick %d 无容量却破坏了方块=%d", tick, got)
		}
		if got := entry.inventory.Hotbar.Slots[0].Durability; got != full {
			t.Fatalf("tick %d 无容量却扣减耐久=%d", tick, got)
		}
	}
	if entry.inventory != before {
		t.Fatalf("无容量期间背包被修改: got=%+v want=%+v", entry.inventory, before)
	}
	if entry.inventoryDirty {
		t.Fatal("无容量期间标记了 inventoryDirty")
	}
	if entry.mining.requiredTicks != 15 || entry.mining.progressTicks != entry.mining.requiredTicks {
		t.Fatalf("无容量时进度没有保持满格: %+v", entry.mining)
	}
}

// TestCompanionMiningRejectsContainerTargets 锁定容器防御拒绝：箱子与熔炉的
// 破坏会掉落多份内容物，超出"单一产物直入背包"的结算形状，权威模拟在进度
// 累积之前就拒绝，方块绝不被破坏。
func TestCompanionMiningRejectsContainerTargets(t *testing.T) {
	for _, block := range []core.BlockID{core.ChestID, core.FurnaceID} {
		t.Run(fmt.Sprintf("block=%d", block), func(t *testing.T) {
			fixture := readyCompanionMining(t, block, core.ItemIronPickaxe)
			entry := fixture.entry
			full, _ := core.ItemMaxDurability(core.ItemIronPickaxe)
			for range 40 {
				advanceMiningOnce(fixture.engine)
			}
			if got := companionMiningBlockAt(t, fixture); got != block {
				t.Fatalf("容器被破坏=%d，想要 %d", got, block)
			}
			if entry.mining != (miningState{}) {
				t.Fatalf("容器目标却累积了进度: %+v", entry.mining)
			}
			if got := entry.inventory.Hotbar.Slots[0].Durability; got != full {
				t.Fatalf("容器目标却扣减了耐久=%d", got)
			}
		})
	}
}

// TestCompanionMiningTargetReplacedInvalidatesProgress 锁定目标替换语义：
// 采掘中目标方块被替换后既有进度失效、从 1 重新计时（对齐玩家的"方块 ID
// 变化"语义），新方块不得继承进度被提前破坏；替换为不可采掘方块则进度清零。
func TestCompanionMiningTargetReplacedInvalidatesProgress(t *testing.T) {
	t.Run("替换为同可采掘方块重新计时", func(t *testing.T) {
		// 原木任何工具 15 tick；石头配铁镐 8 tick。
		fixture := readyCompanionMining(t, core.OakLogID, core.ItemIronPickaxe)
		entry := fixture.entry
		for range 7 {
			advanceMiningOnce(fixture.engine)
		}
		if got := entry.mining.progressTicks; got != 7 {
			t.Fatalf("前置进度=%d，想要 7", got)
		}

		engine := fixture.engine
		engine.SetBlockForTest(fixture.target, core.StoneID)
		advanceMiningOnce(engine)
		if got := entry.mining.progressTicks; got != 1 || entry.mining.block != core.StoneID {
			t.Fatalf("替换后进度=%+v，想要按新方块从 1 重新开始", entry.mining)
		}

		for range 6 {
			advanceMiningOnce(engine)
		}
		if got := companionMiningBlockAt(t, fixture); got != core.StoneID {
			t.Fatalf("新方块被继承的进度提前破坏=%d", got)
		}
		advanceMiningOnce(engine)
		if got := companionMiningBlockAt(t, fixture); got != core.AirID {
			t.Fatalf("按新方块自身计时完成后=%d，想要空气", got)
		}
		if got := companionItemCount(entry, core.ItemStone); got != 1 {
			t.Fatalf("新方块产物未入包: stone=%d", got)
		}
	})

	t.Run("替换为基岩清零且永不破坏", func(t *testing.T) {
		fixture := readyCompanionMining(t, core.StoneID, core.ItemIronPickaxe)
		entry := fixture.entry
		for range 4 {
			advanceMiningOnce(fixture.engine)
		}
		fixture.engine.SetBlockForTest(fixture.target, core.BedrockID)
		for range 20 {
			advanceMiningOnce(fixture.engine)
		}
		if got := companionMiningBlockAt(t, fixture); got != core.BedrockID {
			t.Fatalf("基岩被破坏=%d", got)
		}
		if entry.mining != (miningState{}) {
			t.Fatalf("不可采掘目标没有清零进度: %+v", entry.mining)
		}
	})
}

// TestCompanionMiningProgressPublishesInCompanionUpdate 锁定进度发布：伙伴采掘
// 进度必须进入 CompanionUpdate.Mining（对齐玩家 MiningUpdate 语义），完成 tick
// 之后回到零值。
func TestCompanionMiningProgressPublishesInCompanionUpdate(t *testing.T) {
	fixture := readyCompanionMiningViaActions(t, core.CoalOreID, core.ItemStonePickaxe)
	entry := fixture.entry

	for tick := 1; tick <= 15; tick++ {
		result := holdCompanionMineAction(t, fixture)
		if len(result.Companions) != 1 || result.Companions[0].ID != fixture.id {
			t.Fatalf("tick %d Companions=%+v", tick, result.Companions)
		}
		update := result.Companions[0]
		if tick < 15 {
			want := MiningUpdate{
				Active: true, Target: fixture.target, ProgressTicks: uint16(tick),
				RequiredTicks: 15, Harvestable: true,
			}
			if update.Mining != want {
				t.Fatalf("tick %d 采掘进度=%+v，想要 %+v", tick, update.Mining, want)
			}
		} else if update.Mining != (MiningUpdate{}) {
			t.Fatalf("完成 tick 后进度未清零: %+v", update.Mining)
		}
	}
	if got := companionItemCount(entry, core.ItemCoal); got != 1 {
		t.Fatalf("完成后产物未入包: coal=%d", got)
	}
	if got := companionMiningBlockAt(t, fixture); got != core.AirID {
		t.Fatalf("完成后方块=%d，想要空气", got)
	}
}

// TestCompanionActionMiningPayloadsSetAndClearIntent 锁定 MineHold/MineRelease
// 载荷的按住语义：MineHold 置意图、无 action 的 tick 意图保持（与玩家按住
// 一致）、MineRelease 同 tick 清零；越界目标的 MineHold 与零值 Kind 被确定性
// 丢弃且不产生任何副作用。
func TestCompanionActionMiningPayloadsSetAndClearIntent(t *testing.T) {
	fixture := readyCompanionMiningViaActions(t, core.CoalOreID, core.ItemStonePickaxe)
	entry := fixture.entry

	result := holdCompanionMineAction(t, fixture)
	if !entry.miningHeld || entry.miningTarget != fixture.target {
		t.Fatalf("MineHold 未建立采掘意图: held=%v target=%+v", entry.miningHeld, entry.miningTarget)
	}
	if got := entry.mining.progressTicks; got != 1 {
		t.Fatalf("MineHold 首 tick 进度=%d，想要 1", got)
	}
	if len(result.Rejected) != 0 {
		t.Fatalf("MineHold 产生拒绝=%+v", result.Rejected)
	}

	// 无 action 的 tick：中性输入，但按住意图保持，进度继续累积。
	idle := fixture.engine.Step()
	if len(idle.Companions) != 1 || idle.Companions[0].Mining.ProgressTicks != 2 {
		t.Fatalf("无 action tick 进度=%+v，想要保持按住并推进到 2", idle.Companions)
	}

	// 越界目标与零值 Kind：确定性丢弃，不触碰既有意图之外的任何状态。
	fixture.engine.EnqueueCompanionAction(CompanionAction{
		ID: fixture.id, Kind: CompanionActionMineHold, Target: core.BlockPos{Y: core.MaxY + 1},
	})
	fixture.engine.EnqueueCompanionAction(CompanionAction{ID: fixture.id})
	fixture.engine.Step()
	if got := entry.mining.progressTicks; got != 3 {
		t.Fatalf("非法 action 影响了进度: %+v", entry.mining)
	}

	// MineRelease：同 tick 清空意图与进度，对齐玩家松键语义。
	if !fixture.engine.EnqueueCompanionAction(CompanionAction{
		ID: fixture.id, Kind: CompanionActionMineRelease,
	}) {
		t.Fatal("MineRelease action 未入队")
	}
	released := fixture.engine.Step()
	if entry.miningHeld || entry.mining != (miningState{}) {
		t.Fatalf("MineRelease 后 held=%v mining=%+v，想要同 tick 清零", entry.miningHeld, entry.mining)
	}
	if len(released.Rejected) != 0 {
		t.Fatalf("MineRelease 产生拒绝=%+v", released.Rejected)
	}
}

// TestCompanionActionPlacePayloadSkeletonIsDefensiveOnly 锁定 Place 载荷的防御
// 边界：目标越界或方块未注册/空气的 Place action 被确定性丢弃，世界与背包都
// 不变。合法 Place 的放置结算本体属后续放置任务，此处不断言其结算结果。
func TestCompanionActionPlacePayloadSkeletonIsDefensiveOnly(t *testing.T) {
	fixture := readyCompanionMiningViaActions(t, core.StoneID, core.ItemNone)
	entry := fixture.entry
	entry.inventory.Hotbar.Slots[1] = core.ItemStack{Item: core.ItemStone, Count: 4}
	before := entry.inventory

	invalid := []CompanionAction{
		{ID: fixture.id, Kind: CompanionActionPlace,
			Target: core.BlockPos{Y: core.MinY - 1}, Block: core.StoneID},
		{ID: fixture.id, Kind: CompanionActionPlace, Target: fixture.target, Block: core.AirID},
		{ID: fixture.id, Kind: CompanionActionPlace, Target: fixture.target, Block: core.BlockID(9999)},
	}
	for _, action := range invalid {
		fixture.engine.EnqueueCompanionAction(action)
	}
	result := fixture.engine.Step()
	if got := companionMiningBlockAt(t, fixture); got != core.StoneID {
		t.Fatalf("非法 Place 改变了世界=%d", got)
	}
	if entry.inventory != before {
		t.Fatal("非法 Place 改变了背包")
	}
	if len(result.Rejected) != 0 || len(result.Changes) != 0 {
		t.Fatalf("非法 Place 产生副作用: rejected=%+v changes=%+v", result.Rejected, result.Changes)
	}
}
