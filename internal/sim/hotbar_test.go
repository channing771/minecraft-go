package sim_test

import (
	"math"
	"testing"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/sim"
)

const lookDown = -float32(math.Pi)/2 + 0.01

func TestHotbarPublishesInitialStateOnce(t *testing.T) {
	var restored core.Hotbar
	restored.Selected = 2
	restored.Slots[1] = core.ItemStack{Item: core.ItemStone, Count: 5}
	engine, session := readyFlatPlayerRestored(t, nil, restored)

	// readyFlatPlayerRestored 已经消费了玩家进入 Active 的那个 tick。
	if got := currentInventory(t, engine, session).Hotbar; got != restored {
		t.Fatalf("初始快捷栏 = %+v，想要 %+v", got, restored)
	}
	if result := engine.Step(); len(result.Inventories) != 0 {
		t.Fatalf("未变化时不应重复发布：%+v", result.Inventories)
	}
}

func TestHotbarSelectRequiresValidSlot(t *testing.T) {
	engine, session := readyFlatPlayer(t)
	engine.Enqueue(sim.Command{
		Session: session, Sequence: 2, Kind: sim.CommandSelectHotbar, Slot: 4,
	})

	result := engine.Step()
	if len(result.Rejected) != 0 || len(result.Inventories) != 1 ||
		result.Inventories[0].Session != session || result.Inventories[0].Inventory.Hotbar.Selected != 4 {
		t.Fatalf("合法选择 result=%+v", result)
	}

	engine.Enqueue(sim.Command{
		Session: session, Sequence: 3, Kind: sim.CommandSelectHotbar,
		Slot: core.HotbarSlots,
	})
	rejected := engine.Step()
	if len(rejected.Rejected) != 1 ||
		rejected.Rejected[0].Reason != sim.RejectInvalidSlot ||
		len(rejected.Inventories) != 0 {
		t.Fatalf("越界选择 result=%+v", rejected)
	}
	if got := currentInventory(t, engine, session).Hotbar; got.Selected != 4 {
		t.Fatalf("越界选择后 Selected = %d，想要 4", got.Selected)
	}
}

func TestHotbarSelectSameSlotDoesNotRepublish(t *testing.T) {
	engine, session := readyFlatPlayer(t)
	engine.Enqueue(sim.Command{
		Session: session, Sequence: 2, Kind: sim.CommandSelectHotbar, Slot: 0,
	})

	if result := engine.Step(); len(result.Inventories) != 0 || len(result.Rejected) != 0 {
		t.Fatalf("选中栏位未变化时不应发布：%+v", result)
	}
}

func TestHotbarMiningIgnoresBlockWithoutRule(t *testing.T) {
	position := core.BlockPos{X: 0, Y: 0, Z: 0}
	engine, session := readyFlatPlayerWithTarget(t, map[core.BlockPos]core.BlockID{
		position: core.BarrierID,
	})
	sequence := uint64(1)
	result := mineUntilComplete(t, engine, session, &sequence, 0, lookDown, 5)
	if len(result.Rejected) != 0 || len(result.Changes) != 0 ||
		len(result.Inventories) != 0 || onlyPlayer(t, result).Mining.Active {
		t.Fatalf("无掉落物方块 result=%+v", result)
	}
}

func TestHotbarPlaceConsumesOneItem(t *testing.T) {
	var stocked core.Hotbar
	stocked.Slots[3] = core.ItemStack{Item: core.ItemDirt, Count: 2}
	target := core.BlockPos{X: 0, Y: 2, Z: 3}
	want := core.BlockPos{X: 0, Y: 2, Z: 2}
	engine, session := readyFlatPlayerRestored(t, map[core.BlockPos]core.BlockID{
		target: core.StoneID,
	}, stocked)
	engine.Enqueue(sim.Command{
		Session: session, Sequence: 2, Kind: sim.CommandPlaceBlock,
		Yaw: float32(math.Pi), Slot: 3,
	})

	result := engine.Step()
	if len(result.Rejected) != 0 || len(result.Changes) != 1 ||
		result.Changes[0].Changes[0] != (sim.BlockChange{Position: want, Block: core.DirtID}) {
		t.Fatalf("放置 result=%+v", result)
	}
	if len(result.Inventories) != 1 {
		t.Fatalf("Inventories=%+v，想要恰好一份", result.Inventories)
	}
	got := result.Inventories[0].Inventory.Hotbar.Slots[3]
	if got != (core.ItemStack{Item: core.ItemDirt, Count: 1}) {
		t.Fatalf("栏位 3 = %+v，想要剩余 1 个泥土", got)
	}
}

func TestHotbarPlaceLastItemClearsSlot(t *testing.T) {
	var stocked core.Hotbar
	stocked.Slots[0] = core.ItemStack{Item: core.ItemDirt, Count: 1}
	target := core.BlockPos{X: 0, Y: 2, Z: 3}
	engine, session := readyFlatPlayerRestored(t, map[core.BlockPos]core.BlockID{
		target: core.StoneID,
	}, stocked)
	engine.Enqueue(sim.Command{
		Session: session, Sequence: 2, Kind: sim.CommandPlaceBlock,
		Yaw: float32(math.Pi), Slot: 0,
	})

	result := engine.Step()
	if len(result.Rejected) != 0 || len(result.Inventories) != 1 {
		t.Fatalf("放置 result=%+v", result)
	}
	if result.Inventories[0].Inventory.Hotbar.Slots[0] != (core.ItemStack{}) {
		t.Fatalf("栏位 0 = %+v，想要规范空栏位", result.Inventories[0].Inventory.Hotbar.Slots[0])
	}
}

func TestHotbarPlaceRejectsEmptyOrInvalidSlot(t *testing.T) {
	cases := []struct {
		name string
		slot uint8
		want sim.RejectReason
	}{
		{name: "空栏位", slot: 1, want: sim.RejectInvalidBlock},
		{name: "越界栏位", slot: core.HotbarSlots, want: sim.RejectInvalidSlot},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stocked core.Hotbar
			stocked.Slots[0] = core.ItemStack{Item: core.ItemDirt, Count: 1}
			target := core.BlockPos{X: 0, Y: 2, Z: 3}
			engine, session := readyFlatPlayerRestored(t, map[core.BlockPos]core.BlockID{
				target: core.StoneID,
			}, stocked)
			engine.Enqueue(sim.Command{
				Session: session, Sequence: 2, Kind: sim.CommandPlaceBlock,
				Yaw: float32(math.Pi), Slot: tc.slot,
			})

			result := engine.Step()
			if len(result.Rejected) != 1 || result.Rejected[0].Reason != tc.want ||
				len(result.Changes) != 0 || len(result.Inventories) != 0 {
				t.Fatalf("result=%+v", result)
			}
			if got := currentInventory(t, engine, session).Hotbar; got != stocked {
				t.Fatalf("失败放置改变了快捷栏：%+v", got)
			}
		})
	}
}

func TestHotbarFailedPlaceKeepsItem(t *testing.T) {
	var stocked core.Hotbar
	stocked.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 4}
	engine, session := readyFlatPlayerRestored(t, nil, stocked)
	engine.Enqueue(sim.Command{
		Session: session, Sequence: 2, Kind: sim.CommandPlaceBlock,
		Pitch: lookDown, Slot: 0,
	})

	result := engine.Step()
	if len(result.Rejected) != 1 || result.Rejected[0].Reason != sim.RejectOccupied ||
		len(result.Changes) != 0 || len(result.Inventories) != 0 {
		t.Fatalf("碰撞放置 result=%+v", result)
	}
	if got := currentInventory(t, engine, session).Hotbar; got != stocked {
		t.Fatalf("失败放置扣除了物品：%+v", got)
	}
}

func TestHotbarSameTickCommandsPublishFinalStateOnce(t *testing.T) {
	var stocked core.Hotbar
	stocked.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 1}
	target := core.BlockPos{X: 0, Y: 2, Z: 3}
	engine, session := readyFlatPlayerRestored(t, map[core.BlockPos]core.BlockID{
		target:             core.GrassID,
		{X: 0, Y: 2, Z: 4}: core.StoneID,
	}, stocked)
	sequence := uint64(1)
	mined := mineUntilComplete(t, engine, session, &sequence, float32(math.Pi), 0, 5)
	if len(mined.Rejected) != 0 || len(mined.Changes) != 1 {
		t.Fatalf("采掘 result=%+v", mined)
	}
	engine.Enqueue(sim.Command{
		Session: session, Sequence: 4, Kind: sim.CommandPlaceBlock,
		Yaw: float32(math.Pi), Slot: 0,
	})
	engine.Enqueue(sim.Command{
		Session: session, Sequence: 3, Kind: sim.CommandSelectHotbar, Slot: 7,
	})

	result := engine.Step()
	if len(result.Rejected) != 0 {
		t.Fatalf("同 tick 序列 result=%+v", result)
	}
	if len(result.Inventories) != 1 {
		t.Fatalf("Inventories=%+v，每 tick 最多一份最终状态", result.Inventories)
	}
	hotbar := result.Inventories[0].Inventory.Hotbar
	if hotbar.Selected != 7 || hotbar.Slots[0] != (core.ItemStack{}) {
		t.Fatalf("最终快捷栏 = %+v，想要选中 7 且放置已消耗物品", hotbar)
	}
	want := sim.BlockChange{Position: target, Block: core.StoneID}
	if len(result.Changes) != 1 || len(result.Changes[0].Changes) != 1 ||
		result.Changes[0].Changes[0] != want {
		t.Fatalf("同 tick 世界变更 =%+v", result.Changes)
	}
}

func TestHotbarSurvivesSnapshotRestore(t *testing.T) {
	var stocked core.Hotbar
	stocked.Selected = 6
	stocked.Slots[2] = core.ItemStack{Item: core.ItemGrass, Count: 9}
	engine, session := readyFlatPlayerRestored(t, nil, stocked)

	snapshot, ok := engine.PlayerSnapshot(session)
	if !ok || snapshot.Inventory.Hotbar != stocked {
		t.Fatalf("PlayerSnapshot=%+v ok=%v，想要 %+v", snapshot, ok, stocked)
	}
	unregistered, ok := engine.UnregisterSession(session)
	if !ok || unregistered.Inventory.Hotbar != stocked {
		t.Fatalf("UnregisterSession=%+v ok=%v", unregistered, ok)
	}
}

func TestPlayerHashCoversHotbar(t *testing.T) {
	var stocked core.Hotbar
	stocked.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 1}
	base, session := readyFlatPlayer(t)
	stockedEngine, stockedSession := readyFlatPlayerRestored(t, nil, stocked)

	baseHash, ok := base.PlayerHash(session)
	if !ok {
		t.Fatal("PlayerHash 应当可用")
	}
	stockedHash, ok := stockedEngine.PlayerHash(stockedSession)
	if !ok {
		t.Fatal("PlayerHash 应当可用")
	}
	if baseHash == stockedHash {
		t.Fatal("玩家哈希必须覆盖快捷栏")
	}
}

// currentInventory 读取引擎当前的完整权威物品状态。
func currentInventory(t *testing.T, engine *sim.Engine, session sim.SessionID) core.Inventory {
	t.Helper()
	snapshot, ok := engine.PlayerSnapshot(session)
	if !ok {
		t.Fatalf("会话 %d 没有权威快照", session)
	}
	return snapshot.Inventory
}
