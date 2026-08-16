package render

import (
	"testing"

	"github.com/channing771/mornlea/internal/core"
)

func testItemDrops(count int) []ItemDrop {
	drops := make([]ItemDrop, count)
	items := [3]core.ItemID{core.ItemStone, core.ItemDirt, core.ItemGrass}
	for index := range drops {
		drops[index] = ItemDrop{
			ID: core.DropID{
				Dimension:  core.Overworld,
				Chunk:      core.ChunkPos{X: int32(index / core.DropsPerChunk)},
				Slot:       uint8(index % core.DropsPerChunk),
				Generation: 1,
			},
			Block: core.BlockPos{X: int32(index), Y: 3, Z: int32(index)},
			Item:  items[index%len(items)],
		}
	}
	return drops
}

// Mutation killed: exceeding the fixed instance budget or rendering unknown
// items would break the固定 800 实例上限。
func TestItemDropPartsStayWithinFixedCapacity(t *testing.T) {
	parts := buildItemDropParts(nil, 0, testItemDrops(maxItemDrops+16))
	if len(parts) != maxItemDrops {
		t.Fatalf("实例数 = %d，想要固定上限 %d", len(parts), maxItemDrops)
	}

	unknown := []ItemDrop{{
		ID:   core.DropID{Slot: 0, Generation: 1},
		Item: core.ItemID(4242),
	}}
	if got := buildItemDropParts(nil, 0, unknown); len(got) != 0 {
		t.Fatalf("未注册物品产生了实例: %+v", got)
	}
}

// Mutation killed: swapping item colors would render the wrong swatch.
func TestItemDropColorsMatchProceduralBlocks(t *testing.T) {
	for _, item := range []core.ItemID{core.ItemStone, core.ItemDirt, core.ItemGrass} {
		got, ok := itemDropColor(item)
		if !ok || got != ItemColor(item) {
			t.Fatalf("物品 %d 颜色 = %v, %v，想要与 HUD 一致", item, got, ok)
		}
	}
	if _, ok := itemDropColor(core.ItemNone); ok {
		t.Fatal("空物品得到了颜色")
	}
}

// Mutation killed: deriving animation from wall-clock time or mutating the
// mirror would break determinism across identical ticks.
func TestItemDropAnimationIsDeterministicAndTickDriven(t *testing.T) {
	drops := testItemDrops(2)
	first := append([]avatarPart(nil), buildItemDropParts(nil, 7, drops)...)
	repeat := buildItemDropParts(nil, 7, drops)
	if len(first) != len(repeat) {
		t.Fatalf("同一 tick 实例数不同: %d vs %d", len(first), len(repeat))
	}
	for index := range first {
		if first[index] != repeat[index] {
			t.Fatalf("同一 tick 实例 %d 不稳定", index)
		}
	}

	later := buildItemDropParts(nil, 8, drops)
	if later[0] == first[0] {
		t.Fatal("server tick 前进后动画相位未变化")
	}
	// 权威方块位置不受动画影响。
	if drops[0].Block != (core.BlockPos{X: 0, Y: 3, Z: 0}) {
		t.Fatalf("动画改写了权威位置: %+v", drops[0].Block)
	}
}

// Mutation killed: distinct drops sharing a phase would visibly synchronise.
func TestItemDropPhaseVariesWithStableID(t *testing.T) {
	base := core.DropID{Dimension: core.Overworld, Slot: 1, Generation: 1}
	other := base
	other.Slot = 2
	if dropAnimationPhase(3, base) == dropAnimationPhase(3, other) {
		t.Fatal("不同槽位得到相同动画相位")
	}
}

func TestItemDropColorCoversRegisteredNonPlaceableItems(t *testing.T) {
	for _, item := range []core.ItemID{
		core.ItemCoal, core.ItemRawIron, core.ItemIronIngot,
		core.ItemFurnace, core.ItemIronBlock, core.ItemChest,
		core.ItemBrokenStonePickaxe, core.ItemBrokenIronPickaxe,
	} {
		color, ok := itemDropColor(item)
		if !ok {
			t.Fatalf("已注册物品 %d 无法绘制掉落物", item)
		}
		if color == ([4]float32{}) {
			t.Fatalf("物品 %d 的掉落物颜色为零值", item)
		}
	}
	if _, ok := itemDropColor(core.ItemNone); ok {
		t.Fatal("空物品被绘制")
	}
	if _, ok := itemDropColor(core.ItemID(4242)); ok {
		t.Fatal("未知物品被绘制")
	}
}
