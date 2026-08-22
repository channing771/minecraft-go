package core_test

import (
	"testing"

	"github.com/channing771/mornlea/internal/core"
)

// TestHungerScaleConstants 锁定饥饿三层状态的两个刻度常量。
//
// 它们是定点整数表达的全部依据：`MaxHunger` 决定饥饿值与饱和度的公共上界，
// `SaturationMilliPerPoint` 决定「一点饱和度」在千分位整数里是多少。任何一个
// 被改动，spec 里「饱和度 5 → 4」这类以「点」计的验收场景就会失去意义。
func TestHungerScaleConstants(t *testing.T) {
	if core.MaxHunger != 20 {
		t.Fatalf("MaxHunger = %d，想要 20", core.MaxHunger)
	}
	if core.SaturationMilliPerPoint != 1000 {
		t.Fatalf("SaturationMilliPerPoint = %d，想要 1000", core.SaturationMilliPerPoint)
	}
	// 饱和度以千分位 uint16 存储，上界必须是 MaxHunger 点：越界会在权威侧
	// 静默截断，而截断后的读数与合法读数无法区分。
	if got := uint32(core.MaxHunger) * uint32(core.SaturationMilliPerPoint); got != 20000 {
		t.Fatalf("饱和度上界 = %d 千分位，想要 20000", got)
	}
}

// TestValidHunger 覆盖饥饿值合法区间的两侧边界。
func TestValidHunger(t *testing.T) {
	for _, hunger := range []uint8{0, 1, core.MaxHunger} {
		if !core.ValidHunger(hunger) {
			t.Fatalf("ValidHunger(%d) = false，想要 true", hunger)
		}
	}
	for _, hunger := range []uint8{core.MaxHunger + 1, 255} {
		if core.ValidHunger(hunger) {
			t.Fatalf("ValidHunger(%d) = true，想要 false", hunger)
		}
	}
}

// TestBreadIsRegisteredStackableAndNotPlaceable 锁定面包的物品属性：
// 已注册、堆叠上限 64、没有耐久、**不可放置**。
//
// 「不可放置」这条是承重的：面包若意外落进 ItemPlacement，玩家就能把食物砌成
// 墙，伙伴的 place 注册表交叉校验（internal/companion）也会因为「可放置却没有
// 名字」立刻变红。
func TestBreadIsRegisteredStackableAndNotPlaceable(t *testing.T) {
	if !core.RegisteredItem(core.ItemBread) {
		t.Fatal("ItemBread 未注册")
	}
	if limit, ok := core.ItemStackLimit(core.ItemBread); !ok || limit != core.MaxStackCount {
		t.Fatalf("ItemStackLimit(ItemBread) = (%d,%v)，想要 (%d,true)", limit, ok, core.MaxStackCount)
	}
	if durability, ok := core.ItemMaxDurability(core.ItemBread); ok || durability != 0 {
		t.Fatalf("ItemMaxDurability(ItemBread) = (%d,%v)，想要 (0,false)", durability, ok)
	}
	if block, ok := core.ItemPlacement(core.ItemBread); ok || block != core.AirID {
		t.Fatalf("ItemPlacement(ItemBread) = (%d,%v)，想要 (AirID,false)：面包不可放置", block, ok)
	}
	// 面包不是任何方块的掉落物：它只能合成，不能从世界里挖出来。
	for block := core.BlockID(0); block < core.BlockIDMax; block++ {
		if item, ok := core.BlockDrop(block); ok && item == core.ItemBread {
			t.Fatalf("方块 %d 掉落面包，面包只应由合成获得", block)
		}
	}
}

// TestFoodValueCoversOnlyBread 是食物表的穷举守护：全部合法物品里只有面包是
// 食物，且它的两个恢复值精确等于 (5, 6000 千分位)。
//
// 穷举界用 ItemIDMax 独占哨兵而不是「<= 枚举末项」：追加新物品时 core 的枚举
// 末项守护断言会先变红，迫使开发者回来审视这条穷举是否还表达了「只有面包」。
func TestFoodValueCoversOnlyBread(t *testing.T) {
	for item := core.ItemID(0); item < core.ItemIDMax; item++ {
		hunger, saturation, ok := core.FoodValue(item)
		if item == core.ItemBread {
			if !ok || hunger != 5 || saturation != 6000 {
				t.Fatalf("FoodValue(ItemBread) = (%d,%d,%v)，想要 (5,6000,true)", hunger, saturation, ok)
			}
			continue
		}
		if ok || hunger != 0 || saturation != 0 {
			t.Fatalf("FoodValue(%d) = (%d,%d,%v)，想要 (0,0,false)：面包是唯一食物",
				item, hunger, saturation, ok)
		}
	}
	// 哨兵之外的未注册编号同样不得是食物。
	for item := core.ItemIDMax; item < core.ItemIDMax+8; item++ {
		if _, _, ok := core.FoodValue(item); ok {
			t.Fatalf("未注册物品 %d 被登记为食物", item)
		}
	}
}
