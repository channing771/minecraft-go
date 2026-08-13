package sim_test

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/sim"
	"github.com/channing771/mornlea/internal/world"
)

// stockedChest 返回一个已启用、装有若干物品的箱子槽。
func stockedChest(index uint32) world.ChestSlot {
	chest := world.ChestSlot{Generation: 1, Active: true, BlockIndex: index}
	chest.Items[0] = core.ItemStack{Item: core.ItemStone, Count: 12}
	return chest
}

// readyChestPlayer 构造一个已 Ready 的玩家，并在中心区块放置一个箱子。
func readyChestPlayer(t *testing.T, slot world.ChestSlot) (*sim.Engine, sim.SessionID, uint32) {
	t.Helper()
	engine, session := readyFlatPlayer(t)
	index := dropTargetIndex(t)
	key := core.ChunkKey{Dimension: core.Overworld}
	engine.SetBlockForTest(core.BlockPos{}, core.ChestID)
	slot.BlockIndex = index
	engine.SetChunkChestForTest(key, 0, slot)
	return engine, session, index
}

func currentChest(t *testing.T, engine *sim.Engine, slot int) world.ChestSlot {
	t.Helper()
	chunk, _, ok := engine.CloneReadyChunk(core.ChunkKey{Dimension: core.Overworld})
	if !ok {
		t.Fatal("中心区块不可用")
	}
	return chunk.Chest(slot)
}

func TestOpenChestRequiresAuthoritativeTarget(t *testing.T) {
	engine, session, _ := readyChestPlayer(t, stockedChest(0))
	engine.Enqueue(openFurnaceCommand(session, 2))

	result := engine.Step()
	if len(result.Rejected) != 0 {
		t.Fatalf("命中箱子仍被拒绝: %+v", result.Rejected)
	}
	if len(result.Chests) != 1 {
		t.Fatalf("打开后未发布箱子状态: %+v", result.Chests)
	}
	got := result.Chests[0]
	if got.Session != session || got.Chest.Kind != core.ContainerKindChest ||
		got.Chest.Slot != 0 || got.Chest.Generation != 1 {
		t.Fatalf("发布引用 = %+v", got.Chest)
	}
	if got.Items[0] != (core.ItemStack{Item: core.ItemStone, Count: 12}) {
		t.Fatalf("发布物品 = %+v，想要保留初始堆", got.Items[0])
	}
	if len(result.Furnaces) != 0 {
		t.Fatalf("箱子被误当成熔炉发布: %+v", result.Furnaces)
	}
}

func TestChestViewersShareOneAuthoritativeState(t *testing.T) {
	engine, first, _ := readyChestPlayer(t, stockedChest(0))
	const second = sim.SessionID(2)
	engine.RegisterPlayer(second, sim.PlayerRestore{
		SpawnDimension: core.Overworld,
		SpawnAnchor:    core.ChunkPos{},
	})
	engine.Step()

	engine.Enqueue(openFurnaceCommand(first, 3))
	engine.Enqueue(openFurnaceCommand(second, 3))
	result := engine.Step()
	if len(result.Chests) != 2 {
		t.Fatalf("两名查看者未同时收到箱子状态: %+v", result.Chests)
	}
	if result.Chests[0].Session != first || result.Chests[1].Session != second {
		t.Fatalf("发布顺序不稳定: %+v", result.Chests)
	}
	left, right := result.Chests[0], result.Chests[1]
	if left.Chest != right.Chest || left.Items != right.Items {
		t.Fatalf("两名查看者看到不同状态: %+v vs %+v", left, right)
	}
}

func TestCloseChestStopsPublishing(t *testing.T) {
	engine, session, _ := readyChestPlayer(t, stockedChest(0))
	engine.Enqueue(openFurnaceCommand(session, 2))
	engine.Step()

	engine.Enqueue(sim.Command{
		Session: session, Sequence: 3, Kind: sim.CommandCloseFurnace,
	})
	result := engine.Step()
	if len(result.Chests) != 0 || len(result.FurnaceEnds) != 0 {
		t.Fatalf("显式关闭后仍发布: %+v / %+v", result.Chests, result.FurnaceEnds)
	}
	if next := engine.Step(); len(next.Chests) != 0 {
		t.Fatalf("关闭后继续发布: %+v", next.Chests)
	}
}

func TestChestViewEndsExactlyOnceWhenTargetDies(t *testing.T) {
	engine, session, _ := readyChestPlayer(t, stockedChest(0))
	engine.Enqueue(openFurnaceCommand(session, 2))
	engine.Step()

	// 箱子被停用后 generation 不再匹配，查看关系必须精确关闭一次。
	engine.SetChunkChestForTest(core.ChunkKey{Dimension: core.Overworld}, 0,
		world.ChestSlot{Generation: 1})

	result := engine.Step()
	if len(result.FurnaceEnds) != 1 || result.FurnaceEnds[0].Session != session {
		t.Fatalf("目标失效未精确关闭: %+v", result.FurnaceEnds)
	}
	if result.FurnaceEnds[0].Furnace.Kind != core.ContainerKindChest {
		t.Fatalf("关闭通知的种类不是箱子: %+v", result.FurnaceEnds[0].Furnace)
	}
	if len(result.Chests) != 0 {
		t.Fatalf("失效后仍发布状态: %+v", result.Chests)
	}
	if next := engine.Step(); len(next.FurnaceEnds) != 0 {
		t.Fatalf("关闭通知被重复发送: %+v", next.FurnaceEnds)
	}
}

func TestChestViewEndsOnDisconnect(t *testing.T) {
	engine, session, _ := readyChestPlayer(t, stockedChest(0))
	engine.Enqueue(openFurnaceCommand(session, 2))
	engine.Step()

	engine.UnregisterSession(session)
	result := engine.Step()
	if len(result.Chests) != 0 {
		t.Fatalf("断线后仍向其发布状态: %+v", result.Chests)
	}
}

func TestChestViewEndsExactlyOnceWhenOutOfReach(t *testing.T) {
	engine, session, _ := readyChestPlayer(t, stockedChest(0))
	engine.Enqueue(openFurnaceCommand(session, 2))
	if result := engine.Step(); len(result.Chests) != 1 {
		t.Fatalf("打开箱子失败: %+v", result)
	}

	// 朝 +Z 走出六格交互范围；箱子中心与玩家出生点都在同一区块内，
	// 因此移动全程都在已加载区块内，不会触发区块未就绪。
	engine.Enqueue(sim.Command{
		Session: session, Sequence: 3, Kind: sim.CommandPlayerInput,
		MoveZ: 1, Yaw: placeYaw,
	})

	ends := 0
	var lastChests int
	for range 120 {
		result := engine.Step()
		ends += len(result.FurnaceEnds)
		lastChests = len(result.Chests)
		if ends > 0 {
			break
		}
	}
	if ends != 1 {
		t.Fatalf("超距未精确关闭一次: ends=%d", ends)
	}
	if lastChests != 0 {
		t.Fatalf("关闭的同一 tick 仍发布箱子状态: %d", lastChests)
	}
	if next := engine.Step(); len(next.FurnaceEnds) != 0 || len(next.Chests) != 0 {
		t.Fatalf("超距关闭后仍有发布: %+v", next)
	}
}

func TestOpeningChestEndsExistingFurnaceView(t *testing.T) {
	engine, session, index := readyFurnacePlayer(t, smeltingFurnace(0))
	engine.Enqueue(openFurnaceCommand(session, 2))
	if result := engine.Step(); len(result.Furnaces) != 1 {
		t.Fatalf("打开熔炉失败: %+v", result)
	}

	// 同一位置的方块换成箱子，玩家仍然朝下看，再次打开命中箱子。
	key := core.ChunkKey{Dimension: core.Overworld}
	engine.SetBlockForTest(core.BlockPos{}, core.ChestID)
	engine.SetChunkChestForTest(key, 0, world.ChestSlot{
		Generation: 1, Active: true, BlockIndex: index,
	})

	engine.Enqueue(openFurnaceCommand(session, 3))
	result := engine.Step()
	if len(result.Rejected) != 0 {
		t.Fatalf("切换到箱子被拒绝: %+v", result.Rejected)
	}
	if len(result.Chests) != 1 || result.Chests[0].Session != session {
		t.Fatalf("未建立箱子查看关系: %+v", result.Chests)
	}
	if len(result.Furnaces) != 0 {
		t.Fatalf("切换后仍发布熔炉状态: %+v", result.Furnaces)
	}

	// 原熔炉查看关系已经结束，之后的 tick 不会再发布它的状态。
	if next := engine.Step(); len(next.Furnaces) != 0 {
		t.Fatalf("原熔炉查看关系未结束: %+v", next.Furnaces)
	}
}

func TestOpeningFurnaceEndsExistingChestView(t *testing.T) {
	engine, session, index := readyChestPlayer(t, stockedChest(0))
	engine.Enqueue(openFurnaceCommand(session, 2))
	if result := engine.Step(); len(result.Chests) != 1 {
		t.Fatalf("打开箱子失败: %+v", result)
	}

	key := core.ChunkKey{Dimension: core.Overworld}
	engine.SetBlockForTest(core.BlockPos{}, core.FurnaceID)
	engine.SetChunkFurnaceForTest(key, 0, world.FurnaceSlot{
		Generation: 1, Active: true, BlockIndex: index,
	})

	engine.Enqueue(openFurnaceCommand(session, 3))
	result := engine.Step()
	if len(result.Rejected) != 0 {
		t.Fatalf("切换到熔炉被拒绝: %+v", result.Rejected)
	}
	if len(result.Furnaces) != 1 || result.Furnaces[0].Session != session {
		t.Fatalf("未建立熔炉查看关系: %+v", result.Furnaces)
	}
	if len(result.Chests) != 0 {
		t.Fatalf("切换后仍发布箱子状态: %+v", result.Chests)
	}

	if next := engine.Step(); len(next.Chests) != 0 {
		t.Fatalf("原箱子查看关系未结束: %+v", next.Chests)
	}
}

// TestChestViewEndsExactlyOnceWhenPlayerResets 覆盖"维度 reset"这条失效路径：
// 玩家跌出世界高度下界会触发 beginReset，把 lifecycle 打回 PlayerPendingSpawn，
// publishContainers 里 `lifecycle != PlayerActive` 分支必须精确关闭一次查看关系。
func TestChestViewEndsExactlyOnceWhenPlayerResets(t *testing.T) {
	engine, session, _ := readyChestPlayer(t, stockedChest(0))
	engine.Enqueue(openFurnaceCommand(session, 2))
	if result := engine.Step(); len(result.Chests) != 1 {
		t.Fatalf("打开箱子失败: %+v", result)
	}

	engine.SetPlayerPositionForTest(session, mgl32.Vec3{0.5, float32(core.MinY) - 20, 0.5})

	result := engine.Step()
	if len(result.FurnaceEnds) != 1 || result.FurnaceEnds[0].Session != session {
		t.Fatalf("维度 reset 未精确关闭一次: %+v", result.FurnaceEnds)
	}
	if len(result.Chests) != 0 {
		t.Fatalf("reset 后仍发布箱子状态: %+v", result.Chests)
	}
	if next := engine.Step(); len(next.FurnaceEnds) != 0 {
		t.Fatalf("关闭通知被重复发送: %+v", next.FurnaceEnds)
	}
}
