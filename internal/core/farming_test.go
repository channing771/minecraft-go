package core_test

import (
	"testing"

	"github.com/channing771/mornlea/internal/core"
)

// TestFarmingBlockIDsAppendAfterFluids 锁定 10 个农业方块编号的稳定位次：它们
// 必须紧随 WaterLevel7ID 连续追加（顺序固定为干耕地、湿耕地、小麦阶段 0..7），
// 全部已注册且都有中文显示名。位次是协议稳定值——重排会让既有存档与线上字节
// 指向别的方块。
func TestFarmingBlockIDsAppendAfterFluids(t *testing.T) {
	ordered := []core.BlockID{
		core.FarmlandDryID, core.FarmlandWetID,
		core.WheatStage0ID, core.WheatStage1ID, core.WheatStage2ID, core.WheatStage3ID,
		core.WheatStage4ID, core.WheatStage5ID, core.WheatStage6ID, core.WheatStage7ID,
	}
	for i, id := range ordered {
		if want := core.WaterLevel7ID + 1 + core.BlockID(i); id != want {
			t.Fatalf("农业方块 %d 的编号 = %d，想要紧随流体之后的 %d", i, id, want)
		}
		if !core.RegisteredBlock(id) {
			t.Fatalf("农业方块 %d 未注册", id)
		}
		if core.IsFluid(id) {
			t.Fatalf("农业方块 %d 被判成流体", id)
		}
		if name, ok := core.BlockDisplayName(id); !ok || name == "" {
			t.Fatalf("农业方块 %d 没有显示名", id)
		}
	}
}

// TestBlockIDMaxGuardsExhaustiveEnumeration 锁定 BlockIDMax 独占哨兵与枚举末项
// 的关系（与 ItemIDMax 同形）：当前最后一个合法方块必须是 WheatStage7ID。
// 方块演进纪律是只能在哨兵之前追加；将来追加新编号时第一个断言变红，迫使开发者
// 同步审视全部以「id < BlockIDMax」为穷举界的测试与哨兵，而不是让它们静默退化
// 成子集——历史上以 MossyCobblestoneID、WaterLevel7ID 为界写死的循环上界正是这
// 样在五个包里失效过。
func TestBlockIDMaxGuardsExhaustiveEnumeration(t *testing.T) {
	if core.WheatStage7ID != core.BlockIDMax-1 {
		t.Fatalf("BlockID 枚举末项不再是 WheatStage7ID（BlockIDMax-1 = %d）；"+
			"新增方块必须同步审视全部以 BlockIDMax 为穷举界的测试与哨兵", core.BlockIDMax-1)
	}
	// 哨兵之外不得再出现已注册方块：若有人把新编号追加在哨兵之后，穷举界会
	// 静默漏掉它，这里用有界前瞻扫描兜底报警。扫描宽度只作绊线不是完备证明。
	for id := core.BlockIDMax; id < core.BlockIDMax+64; id++ {
		if core.RegisteredBlock(id) {
			t.Fatalf("方块 %d 注册在 BlockIDMax 哨兵之外，独占穷举界失效", id)
		}
	}
}

// TestFarmingItemsAppendBeforeSentinel 锁定 6 个农业物品的稳定位次与形状：
// 两把锄头各带损坏形态（沿用镐的模式：单栈、有耐久上限），种子与小麦是普通
// 可堆叠物品（上限 64）。
func TestFarmingItemsAppendBeforeSentinel(t *testing.T) {
	ordered := []core.ItemID{
		core.ItemStoneHoe, core.ItemIronHoe,
		core.ItemBrokenStoneHoe, core.ItemBrokenIronHoe,
		core.ItemWheatSeeds, core.ItemWheat,
	}
	for i, item := range ordered {
		if want := core.ItemMossyCobblestone + 1 + core.ItemID(i); item != want {
			t.Fatalf("农业物品 %d 的编号 = %d，想要紧随既有序列之后的 %d", i, item, want)
		}
		if !core.RegisteredItem(item) {
			t.Fatalf("农业物品 %d 未注册", item)
		}
	}
	for _, tool := range []core.ItemID{core.ItemStoneHoe, core.ItemIronHoe} {
		limit, ok := core.ItemStackLimit(tool)
		if !ok || limit != 1 {
			t.Fatalf("锄头 %d 的 ItemStackLimit = (%d,%v)，想要 (1,true)", tool, limit, ok)
		}
		if _, ok := core.ItemMaxDurability(tool); !ok {
			t.Fatalf("锄头 %d 没有耐久上限", tool)
		}
		if _, ok := core.ItemBrokenForm(tool); !ok {
			t.Fatalf("锄头 %d 没有损坏形态", tool)
		}
	}
	// 石锄/铁锄与同材质镐同耐久：翻地与采掘每次都恰好扣 1 点，同一材质两种
	// 工具给不同数值只会制造第二套无来源的数值。
	if got, _ := core.ItemMaxDurability(core.ItemStoneHoe); got != 131 {
		t.Fatalf("石锄耐久 = %d，想要与石镐同为 131", got)
	}
	if got, _ := core.ItemMaxDurability(core.ItemIronHoe); got != 250 {
		t.Fatalf("铁锄耐久 = %d，想要与铁镐同为 250", got)
	}
	for _, broken := range []core.ItemID{core.ItemBrokenStoneHoe, core.ItemBrokenIronHoe} {
		if _, ok := core.ItemMaxDurability(broken); ok {
			t.Fatalf("损坏锄头 %d 不应有耐久上限", broken)
		}
	}
	for _, item := range []core.ItemID{core.ItemWheatSeeds, core.ItemWheat} {
		limit, ok := core.ItemStackLimit(item)
		if !ok || limit != core.MaxStackCount {
			t.Fatalf("物品 %d 的 ItemStackLimit = (%d,%v)，想要 (64,true)", item, limit, ok)
		}
		if _, ok := core.ItemMaxDurability(item); ok {
			t.Fatalf("物品 %d 不应有耐久上限", item)
		}
	}
}

// TestFarmingPlacementAndDrops 锁定农业编号的放置与掉落映射：
//   - 种子放置成 WheatStage0ID（刚种下的阶段），是唯一能写出农业方块的物品；
//   - 耕地与作物本身没有对应的方块物品，MUST NOT 可作为普通物品放置；
//   - 未成熟作物掉 1 种子（误挖不亏种子），耕地两种形态都掉 1 泥土。
func TestFarmingPlacementAndDrops(t *testing.T) {
	if got, ok := core.ItemPlacement(core.ItemWheatSeeds); !ok || got != core.WheatStage0ID {
		t.Fatalf("ItemPlacement(种子) = (%d,%v)，想要 (%d,true)", got, ok, core.WheatStage0ID)
	}
	if _, ok := core.ItemPlacement(core.ItemWheat); ok {
		t.Fatal("小麦不是可放置物品")
	}
	// 耕地与未成熟作物没有对应的方块物品：反查掉落物再放置不得还原成同一方块，
	// 否则伙伴放置路径（core.BlockDrop → core.ItemPlacement 往返）会把它们当成
	// 可放置方块。
	for _, block := range []core.BlockID{core.FarmlandDryID, core.FarmlandWetID} {
		item, ok := core.BlockDrop(block)
		if !ok || item != core.ItemDirt {
			t.Fatalf("BlockDrop(耕地 %d) = (%d,%v)，想要 (泥土 %d,true)", block, item, ok, core.ItemDirt)
		}
		if placed, ok := core.ItemPlacement(item); ok && placed == block {
			t.Fatalf("耕地 %d 可由物品 %d 直接放置", block, item)
		}
	}
	for stage := core.WheatStage0ID; stage <= core.WheatStage6ID; stage++ {
		item, ok := core.BlockDrop(stage)
		if !ok || item != core.ItemWheatSeeds {
			t.Fatalf("BlockDrop(未成熟小麦 %d) = (%d,%v)，想要 (种子 %d,true)",
				stage, item, ok, core.ItemWheatSeeds)
		}
	}
	if item, ok := core.BlockDrop(core.WheatStage7ID); !ok || item != core.ItemWheat {
		t.Fatalf("BlockDrop(成熟小麦) = (%d,%v)，想要 (小麦 %d,true)", item, ok, core.ItemWheat)
	}
}
