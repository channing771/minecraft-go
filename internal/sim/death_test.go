package sim_test

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/sim"
	"github.com/channing771/mornlea/internal/world"
)

// wornPickaxe 是死亡掉落用的已磨损铁镐，耐久必须原样出现在掉落物里。
var wornPickaxe = core.ItemStack{
	Item: core.ItemIronPickaxe, Count: 1, Durability: 149,
}

// TestDeathDropsInventoryIntoWorldAndClearsSlots 覆盖"死亡清空背包并掉在世界里"：
// 36 个格全部清空、原有物品作为掉落物出现在死亡所在区块，
// 且被写入的区块在同一 tick 内发布零方块 revision barrier，镜像与持久化才看得见新掉落物。
func TestDeathDropsInventoryIntoWorldAndClearsSlots(t *testing.T) {
	inventory := core.Inventory{}
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 10}
	inventory.Backpack[0] = core.ItemStack{Item: core.ItemCoal, Count: 5}
	inventory.Backpack[core.BackpackSlots-1] = wornPickaxe
	engine, session := readyFlatWorld(t, 0, inventory)

	died := killByFallResult(t, engine, session)

	if len(died.Changes) != 1 || died.Changes[0].Chunk != (core.ChunkPos{}) ||
		len(died.Changes[0].Changes) != 0 {
		t.Fatalf("死亡掉落应当为死亡区块发布零方块 revision barrier: %+v", died.Changes)
	}
	drops := activeDrops(t, engine, core.ChunkKey{Dimension: core.Overworld})
	want := map[core.ItemStack]bool{
		{Item: core.ItemStone, Count: 10}: true,
		{Item: core.ItemCoal, Count: 5}:   true,
		wornPickaxe:                       true,
	}
	if len(drops) != len(want) {
		t.Fatalf("死亡掉落物 = %+v，想要 %d 堆", drops, len(want))
	}
	for _, drop := range drops {
		if !want[drop.Stack] {
			t.Fatalf("意外的死亡掉落物 %+v，想要其一 %+v", drop.Stack, want)
		}
		delete(want, drop.Stack)
	}

	respawnPlayer(t, engine, session)
	snapshot, ok := engine.PlayerSnapshot(session)
	if !ok {
		t.Fatal("重生后没有玩家快照")
	}
	for slot := uint8(0); slot < core.InventorySlots; slot++ {
		stack, _ := snapshot.Inventory.Slot(slot)
		if stack != (core.ItemStack{}) {
			t.Fatalf("重生后第 %d 格 = %+v，想要空", slot, stack)
		}
	}
}

// TestDeathDropsWornPickaxeWithoutLosingDurability 覆盖"带耐久的工具无损掉落"。
func TestDeathDropsWornPickaxeWithoutLosingDurability(t *testing.T) {
	inventory := core.Inventory{}
	inventory.Hotbar.Slots[3] = wornPickaxe
	engine, session := readyFlatWorld(t, 0, inventory)

	killByFall(t, engine, session)

	drops := activeDrops(t, engine, core.ChunkKey{Dimension: core.Overworld})
	if len(drops) != 1 || drops[0].Stack != wornPickaxe {
		t.Fatalf("死亡掉落物 = %+v，想要恰好一把 %+v", drops, wornPickaxe)
	}
}

// TestDeathReturnsToSpawnAnchorAtFullHealthWithZeroVelocity 覆盖
// "死亡后回到出生锚点并满血"：玩家在**远离出生点**的区块 (1,1) 摔死，
// 掉落物必须留在死亡所在区块，而玩家速度归零、生命值回满、位置回到出生锚点。
// 死亡点与出生锚点分处不同区块，环心是"死亡区块"而不是原点才可判别。
func TestDeathReturnsToSpawnAnchorAtFullHealthWithZeroVelocity(t *testing.T) {
	inventory := core.Inventory{}
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 3}
	engine, session := readyFlatWorld(t, 1, inventory)

	died := killByFallAt(t, engine, session, 20.5, 20.5)

	deathChunk := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: 1, Z: 1}}
	drops := activeDrops(t, engine, deathChunk)
	if len(drops) != 1 ||
		drops[0].Stack != (core.ItemStack{Item: core.ItemStone, Count: 3}) {
		t.Fatalf("死亡区块 (1,1) 掉落物 = %+v，想要 3 个石头", drops)
	}
	if origin := activeDrops(t, engine, core.ChunkKey{Dimension: core.Overworld}); len(origin) != 0 {
		t.Fatalf("出生区块 (0,0) 被写入 %+v，掉落应当留在死亡区块", origin)
	}

	if died.State.Velocity != (mgl32.Vec3{}) {
		t.Fatalf("死亡当 tick 速度 = %+v，想要零", died.State.Velocity)
	}
	if died.State.Position.X() != 0.5 || died.State.Position.Z() != 0.5 {
		t.Fatalf("死亡当 tick 位置 = %+v，想要出生锚点所在列", died.State.Position)
	}

	respawned := respawnPlayer(t, engine, session)
	if respawned.State.Position != (mgl32.Vec3{0.5, 1, 0.5}) {
		t.Fatalf("重生位置 = %+v，想要出生锚点", respawned.State.Position)
	}
	if respawned.State.Velocity != (mgl32.Vec3{}) {
		t.Fatalf("重生速度 = %+v，想要零", respawned.State.Velocity)
	}
	assertPlayerHealth(t, engine, session, core.MaxHealth)
}

// TestDeathNeverPublishesZeroHealth 覆盖"生命值为零的状态不对外发布"：
// 从摔落到重生的每一个 tick，对外可见的权威生命值都不得为 0。
func TestDeathNeverPublishesZeroHealth(t *testing.T) {
	engine, session := readyFlatWorld(t, 0, core.Inventory{})
	engine.SetPlayerPositionForTest(session, mgl32.Vec3{0.5, 1 + lethalFallHeight, 0.5})

	died := false
	for tick := range 400 {
		update := onlyPlayer(t, engine.Step())
		if snapshot, ok := engine.PlayerSnapshot(session); ok && snapshot.Health == 0 {
			t.Fatalf("第 %d tick 对外发布了生命值 0 的中间状态", tick)
		}
		if !update.Ready {
			died = true
		}
		if died && update.Ready {
			assertPlayerHealth(t, engine, session, core.MaxHealth)
			return
		}
	}
	t.Fatal("玩家未在 400 tick 内完成摔死与重生")
}

// TestDeathDropPlacementIsDeterministic 覆盖"放置顺序可复现"：
// 两次相同初始状态、相同死亡位置的结算必须给出完全相同的掉落分布。
func TestDeathDropPlacementIsDeterministic(t *testing.T) {
	first := deathDropLayout(t)
	second := deathDropLayout(t)
	if len(first) != len(second) {
		t.Fatalf("两次结算的区块数不同: %d 与 %d", len(first), len(second))
	}
	for key, hash := range first {
		if second[key] != hash {
			t.Fatalf("区块 %+v 的掉落分布在两次结算间不同", key)
		}
	}
}

// TestDeathDropsPreferDeathChunkAndMerge 覆盖"优先放在死亡区块"：
// 死亡区块有空位时全部落在死亡区块，同类物品按堆叠上限合并，邻近区块不被写入。
func TestDeathDropsPreferDeathChunkAndMerge(t *testing.T) {
	inventory := core.Inventory{}
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 10}
	inventory.Hotbar.Slots[5] = core.ItemStack{Item: core.ItemStone, Count: 10}
	inventory.Backpack[7] = core.ItemStack{Item: core.ItemStone, Count: 10}
	engine, session := readyFlatWorld(t, 1, inventory)

	killByFall(t, engine, session)

	deathKey := core.ChunkKey{Dimension: core.Overworld}
	drops := activeDrops(t, engine, deathKey)
	if len(drops) != 1 ||
		drops[0].Stack != (core.ItemStack{Item: core.ItemStone, Count: 30}) {
		t.Fatalf("死亡区块掉落物 = %+v，想要恰好一堆 30 个石头", drops)
	}
	for _, key := range ringOneKeys() {
		if neighbors := activeDrops(t, engine, key); len(neighbors) != 0 {
			t.Fatalf("邻近区块 %+v 被写入 %+v", key.Pos, neighbors)
		}
	}
}

// TestDeathDropsOverflowToNearestLoadedChunk 覆盖"死亡区块放不下时溢出到邻近区块"：
// 溢出目标必须是环形外扩第一圈中稳定顺序的首个已加载区块，且背包对应格被清空。
func TestDeathDropsOverflowToNearestLoadedChunk(t *testing.T) {
	inventory := core.Inventory{}
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 7}
	engine, session := readyFlatWorld(t, 1, inventory)
	fillChunkDrops(t, engine, core.ChunkKey{Dimension: core.Overworld})

	killByFall(t, engine, session)

	wantKey := core.ChunkKey{
		Dimension: core.Overworld, Pos: core.ChunkPos{X: -1, Z: -1},
	}
	for _, key := range ringOneKeys() {
		drops := activeDrops(t, engine, key)
		if key != wantKey {
			if len(drops) != 0 {
				t.Fatalf("非首选邻近区块 %+v 被写入 %+v", key.Pos, drops)
			}
			continue
		}
		if len(drops) != 1 ||
			drops[0].Stack != (core.ItemStack{Item: core.ItemStone, Count: 7}) {
			t.Fatalf("溢出区块 %+v 掉落物 = %+v，想要 7 个石头", key.Pos, drops)
		}
	}

	respawnPlayer(t, engine, session)
	snapshot, _ := engine.PlayerSnapshot(session)
	if snapshot.Inventory.Hotbar.Slots[0] != (core.ItemStack{}) {
		t.Fatalf("溢出成功后快捷栏第 0 格 = %+v，想要空", snapshot.Inventory.Hotbar.Slots[0])
	}
}

// TestDeathKeepsUnplaceableStacksAndSkipsUnloadedChunks 覆盖
// "不写未加载区块"与"扫完仍装不下时保留在背包"：只加载死亡区块且其掉落槽已满时，
// 死亡仍然成立、物品不被销毁、也不为掉落触发任何区块加载。
func TestDeathKeepsUnplaceableStacksAndSkipsUnloadedChunks(t *testing.T) {
	inventory := core.Inventory{}
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 7}
	engine, session := readyFlatWorld(t, 0, inventory)
	deathKey := core.ChunkKey{Dimension: core.Overworld}
	fillChunkDrops(t, engine, deathKey)

	died := killByFallResult(t, engine, session)
	if len(died.Acquire) != 0 || len(died.Generate) != 0 {
		t.Fatalf(
			"死亡当 tick 触发了区块加载: Acquire=%+v Generate=%+v",
			died.Acquire, died.Generate,
		)
	}
	for _, drop := range activeDrops(t, engine, deathKey) {
		if drop.Stack.Item == core.ItemStone {
			t.Fatalf("已满的死亡区块仍被写入石头掉落物 %+v", drop)
		}
	}

	respawnPlayer(t, engine, session)
	snapshot, ok := engine.PlayerSnapshot(session)
	if !ok {
		t.Fatal("重生后没有玩家快照")
	}
	if snapshot.Inventory.Hotbar.Slots[0] != (core.ItemStack{Item: core.ItemStone, Count: 7}) {
		t.Fatalf(
			"放不下的物品 = %+v，想要原样保留在背包",
			snapshot.Inventory.Hotbar.Slots[0],
		)
	}
	assertPlayerHealth(t, engine, session, core.MaxHealth)
}

// TestDeathDropsSurviveDeathChunkLeavingSubscriptionOnTheSameTick 覆盖
// "死亡区块在死亡当 tick 脱离订阅"：玩家在远离出生锚点、且从磁盘干净读回
// （Revision == PersistedRevision）的区块里摔死。死亡掉落写入该区块的同一 tick 里
// 玩家被传回出生锚点，订阅并集随之收缩到锚点周围，干净区块会被立即删除而不是
// 转入 Unloading。死亡结算必须与其余区块写者一样站在 reconcileSubscriptions 之后，
// 否则 finishChanges 取不到 record，既崩服又丢掉落物。
func TestDeathDropsSurviveDeathChunkLeavingSubscriptionOnTheSameTick(t *testing.T) {
	inventory := core.Inventory{}
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 10}
	engine, session := readyFlatWorld(t, 1, inventory)

	// 视距 1 时出生锚点 (0,0) 的订阅只覆盖 (-1..1)²，因此死亡区块 (2,2)
	// 在玩家被传回锚点的当 tick 必然离开订阅并集。
	walkToCleanChunk(t, engine, session, core.ChunkPos{X: 1, Z: 1})
	walkToCleanChunk(t, engine, session, core.ChunkPos{X: 2, Z: 2})

	deathKey := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: 2, Z: 2}}
	died := killByFallFrom(t, engine, session, 32.5, 32.5)

	barrier := false
	for _, batch := range died.Changes {
		if batch.Chunk == deathKey.Pos && len(batch.Changes) == 0 {
			barrier = true
		}
	}
	if !barrier {
		t.Fatalf(
			"死亡当 tick 未为死亡区块 %+v 发布 revision barrier: %+v",
			deathKey.Pos, died.Changes,
		)
	}
	drops := activeDrops(t, engine, deathKey)
	if len(drops) != 1 ||
		drops[0].Stack != (core.ItemStack{Item: core.ItemStone, Count: 10}) {
		t.Fatalf("死亡区块 %+v 掉落物 = %+v，想要 10 个石头", deathKey.Pos, drops)
	}
}

// walkToCleanChunk 把玩家瞬移到目标区块的中心列并推进到订阅稳定。沿途新订阅的
// 区块一律按"从磁盘干净读回"供给（Revision == PersistedRevision），它们离开订阅
// 并集时会被 RequestUnload 立即删除，而不是像 SubmitGenerated 产出的脏区块那样
// 转入 Unloading 后仍然留在 records 里。
func walkToCleanChunk(
	t *testing.T,
	engine *sim.Engine,
	session sim.SessionID,
	pos core.ChunkPos,
) {
	t.Helper()
	engine.SetPlayerPositionForTest(session, mgl32.Vec3{
		float32(pos.X<<core.SectionShift) + 0.5,
		1,
		float32(pos.Z<<core.SectionShift) + 0.5,
	})
	for range 20 {
		result := engine.Step()
		for _, key := range result.Acquire {
			engine.SubmitAcquired(sim.AcquiredChunk{
				Key:               key,
				Chunk:             generateFlatChunk(key.Pos),
				Revision:          5,
				PersistedRevision: 5,
			})
		}
		if len(result.Generate) != 0 {
			t.Fatalf("干净区块已供给，却仍要求生成 %+v", result.Generate)
		}
		if len(result.Acquire) == 0 && onlyPlayer(t, result).ViewCenter == pos {
			return
		}
	}
	t.Fatalf("玩家未在 20 tick 内稳定到区块 %+v", pos)
}

// lethalFallHeight 是从满血摔死所需的落差：伤害 = floor(落差) − 3 = 20。
const lethalFallHeight = float32(23)

// deathDropLayout 用固定初始状态跑一次死亡结算，返回全部已加载区块的掉落物哈希。
// 死亡区块只留两个空槽，因此结算必然跨区块外扩，掉落分布才真正依赖遍历顺序。
func deathDropLayout(t *testing.T) map[core.ChunkKey][32]byte {
	t.Helper()
	inventory := core.Inventory{}
	for slot := range 6 {
		inventory.Hotbar.Slots[slot] = core.ItemStack{
			Item: core.ItemStone, Count: core.MaxStackCount,
		}
	}
	engine, session := readyFlatWorld(t, 1, inventory)
	deathKey := core.ChunkKey{Dimension: core.Overworld}
	fillChunkDropsExcept(t, engine, deathKey, 2)

	killByFall(t, engine, session)

	layout := make(map[core.ChunkKey][32]byte)
	for _, key := range append(ringOneKeys(), deathKey) {
		chunk, _, ok := engine.CloneReadyChunk(key)
		if !ok {
			t.Fatalf("区块 %+v 不可读", key.Pos)
		}
		layout[key] = chunk.DropsHash()
	}
	return layout
}

// ringOneKeys 返回死亡区块 (0,0) 外扩第一圈的八个区块键，按 sortChunkKeys 顺序。
func ringOneKeys() []core.ChunkKey {
	keys := make([]core.ChunkKey, 0, 8)
	for x := int32(-1); x <= 1; x++ {
		for z := int32(-1); z <= 1; z++ {
			if x == 0 && z == 0 {
				continue
			}
			keys = append(keys, core.ChunkKey{
				Dimension: core.Overworld, Pos: core.ChunkPos{X: x, Z: z},
			})
		}
	}
	return keys
}

// fillChunkDrops 占满一个区块的全部掉落槽，使其再也放不下新的堆。
func fillChunkDrops(t *testing.T, engine *sim.Engine, key core.ChunkKey) {
	t.Helper()
	fillChunkDropsExcept(t, engine, key, 0)
}

// fillChunkDropsExcept 占满一个区块除末尾 free 个槽以外的全部掉落槽。
// 占位堆用远离玩家的方块位置且已达堆叠上限，因此既不会被拾取也不会与新堆合并。
func fillChunkDropsExcept(
	t *testing.T,
	engine *sim.Engine,
	key core.ChunkKey,
	free int,
) {
	t.Helper()
	index, ok := world.ChunkBlockIndex(core.BlockPos{
		X: key.Pos.X<<core.SectionShift + 15,
		Y: 1,
		Z: key.Pos.Z<<core.SectionShift + 15,
	})
	if !ok {
		t.Fatalf("区块 %+v 的占位方块没有索引", key.Pos)
	}
	for slot := range core.DropsPerChunk - free {
		engine.SetChunkDropForTest(key, slot, world.DropSlot{
			Generation: 1,
			Active:     true,
			Stack:      core.ItemStack{Item: core.ItemCoal, Count: core.MaxStackCount},
			BlockIndex: index,
		})
	}
}

// activeDrops 返回一个已加载区块当前的全部活动掉落物槽。
func activeDrops(
	t *testing.T,
	engine *sim.Engine,
	key core.ChunkKey,
) []world.DropSlot {
	t.Helper()
	chunk, _, ok := engine.CloneReadyChunk(key)
	if !ok {
		t.Fatalf("区块 %+v 不可读", key.Pos)
	}
	var drops []world.DropSlot
	for slot := range core.DropsPerChunk {
		if drop := chunk.Drop(slot); drop.Active {
			drops = append(drops, drop)
		}
	}
	return drops
}

// killByFall 让玩家在出生列从致死高度摔落，返回死亡当 tick 的权威玩家状态。
func killByFall(
	t *testing.T,
	engine *sim.Engine,
	session sim.SessionID,
) sim.PlayerUpdate {
	t.Helper()
	return onlyPlayer(t, killByFallResult(t, engine, session))
}

// killByFallAt 让玩家在指定水平位置从致死高度摔落，用于构造远离出生点的死亡。
func killByFallAt(
	t *testing.T,
	engine *sim.Engine,
	session sim.SessionID,
	x, z float32,
) sim.PlayerUpdate {
	t.Helper()
	return onlyPlayer(t, killByFallFrom(t, engine, session, x, z))
}

// killByFallResult 让玩家在出生列从致死高度摔落，返回死亡当 tick 的完整 TickResult。
func killByFallResult(
	t *testing.T,
	engine *sim.Engine,
	session sim.SessionID,
) sim.TickResult {
	t.Helper()
	return killByFallFrom(t, engine, session, 0.5, 0.5)
}

// killByFallFrom 把玩家瞬移到 (x, z) 列的致死高度并推进到死亡结算完成，
// 返回死亡当 tick 的完整 TickResult。死亡当 tick 玩家转入待重生，
// 因此以 Ready 由真变假作为死亡已结算的判据。
func killByFallFrom(
	t *testing.T,
	engine *sim.Engine,
	session sim.SessionID,
	x, z float32,
) sim.TickResult {
	t.Helper()
	engine.SetPlayerPositionForTest(session, mgl32.Vec3{x, 1 + lethalFallHeight, z})
	for range 400 {
		result := engine.Step()
		if !onlyPlayer(t, result).Ready {
			return result
		}
	}
	t.Fatal("玩家未在 400 tick 内因致死摔落而死亡")
	return sim.TickResult{}
}

// respawnPlayer 推进 tick 直到死亡玩家重新进入 Active，返回重生当 tick 的状态。
func respawnPlayer(
	t *testing.T,
	engine *sim.Engine,
	session sim.SessionID,
) sim.PlayerUpdate {
	t.Helper()
	for range 100 {
		update := onlyPlayer(t, engine.Step())
		if update.Ready {
			return update
		}
	}
	t.Fatal("玩家未在 100 tick 内重生")
	return sim.PlayerUpdate{}
}

// readyFlatWorld 构造一个已 Ready 的平坦世界玩家，视距半径可控，
// 因此测试可以决定死亡点周围有多少已加载区块。
func readyFlatWorld(
	t *testing.T,
	viewRadius int,
	inventory core.Inventory,
) (*sim.Engine, sim.SessionID) {
	t.Helper()
	engine := sim.NewEngine(viewRadius, 0, 0)
	const session = sim.SessionID(1)
	engine.RegisterPlayer(session, sim.PlayerRestore{
		SpawnDimension: core.Overworld,
		SpawnAnchor:    core.ChunkPos{},
		Inventory:      inventory,
	})
	requested := engine.Step()
	submitAcquiredMisses(engine, requested.Acquire)
	generating := engine.Step()
	for _, key := range generating.Generate {
		engine.SubmitGenerated(sim.GeneratedChunk{
			Dimension: key.Dimension,
			Pos:       key.Pos,
			Chunk:     generateFlatChunk(key.Pos),
		})
	}
	for range 20 {
		update := onlyPlayer(t, engine.Step())
		if !update.Ready {
			continue
		}
		if update.State.Position != (mgl32.Vec3{0.5, 1, 0.5}) {
			t.Fatalf("出生位置 = %+v，想要 (0.5, 1, 0.5)", update.State.Position)
		}
		return engine, session
	}
	t.Fatal("玩家未在 20 tick 内出生")
	return nil, 0
}
