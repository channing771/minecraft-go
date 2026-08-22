package sim

import (
	"testing"

	"github.com/channing771/mornlea/internal/core"
)

// eatingTestSession 是本文件全部夹具共用的会话号。每条用例各建一个引擎，
// 号码不会互相干扰。
const eatingTestSession = SessionID(71)

// readyEatingPlayer 返回一名已激活、站在地面上、生命值满的玩家，并把三层饥饿
// 状态置到指定夹具值。
//
// 生命值取满是**承重条件**而不是随手写的默认值：非满血的玩家会在
// `advanceHealthRegen` 满足延迟后自然回血，一次回血累积 6000 疲劳（大于阈值
// 4000），当场把饥饿值扣下去——那样"进食前后饥饿值精确不变/精确 +5"的断言
// 就会被回血的副作用污染，读数不再归因于进食。
func readyEatingPlayer(t *testing.T, hunger uint8, saturationMilli uint16) (*Engine, *playerState) {
	t.Helper()
	engine := readyRegenPlayer(t, eatingTestSession, core.MaxHealth)
	player := engine.sessions[eatingTestSession].player
	player.hunger = hunger
	player.saturationMilli = saturationMilli
	player.exhaustionMilli = 0
	return engine, player
}

// setEatingSlot 直接写一格快捷栏并选中它。夹具走权威结构体而不是命令：进食
// 状态机的输入是"选中格里是什么"，命令层的选中路径由既有用例覆盖。
func setEatingSlot(player *playerState, slot uint8, stack core.ItemStack) {
	player.inventory.Hotbar.Slots[slot] = stack
	player.inventory.Hotbar.Selected = slot
}

// hotbarCount 读出一格快捷栏当前的数量，供"精确不变"类断言使用。
func hotbarCount(player *playerState, slot uint8) uint8 {
	return player.inventory.Hotbar.Slots[slot].Count
}

// TestEatingSettlesExactlyAtEatingTicksWithFixedValues 覆盖 Scenario「持续进食
// 到时结算」与「饱和度不超过饥饿值」。
//
// 三条断言的位置性来自同一个形状：第 `EatingTicks - 1` tick **逐字段精确不变**，
// 第 `EatingTicks` tick 才结算。只断言"第 32 tick 扣了料"的用例在"第 31 tick
// 就结算"的实现下同样全绿，那正是本变更规定必须钉死的那一 tick。
//
// 三组夹具各自承重一条钳制规则：
//   - 饥饿 10 / 饱和 0 是 spec Scenario 的直接编码（两条钳制都不触发）；
//   - 饥饿 12 / 饱和 12000 让"先加饥饿再钳饱和"成为可读数事实：加满 6000 后是
//     18000，超过更新后饥饿值对应的 17000，**不钳就会读出 18000**；
//   - 饥饿 17 / 饱和 0 让饥饿值上限承重：17+5=22 必须钳到 20。
func TestEatingSettlesExactlyAtEatingTicksWithFixedValues(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		hunger         uint8
		saturation     uint16
		wantHunger     uint8
		wantSaturation uint16
	}{
		{"spec 场景:饥饿10饱和0", 10, 0, 15, 6000},
		{"饱和被更新后的饥饿值钳住", 12, 12000, 17, 17000},
		{"饥饿被上限钳住", 17, 0, 20, 6000},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			engine, player := readyEatingPlayer(t, testCase.hunger, testCase.saturation)
			setEatingSlot(player, 0, core.ItemStack{Item: core.ItemBread, Count: 2})
			player.eatingHeld = true

			for range defaultEatingTicks - 1 {
				engine.Step()
			}
			if got := hotbarCount(player, 0); got != 2 {
				t.Fatalf("第 %d tick 面包数=%d，想要精确保持 2", defaultEatingTicks-1, got)
			}
			if player.hunger != testCase.hunger || player.saturationMilli != testCase.saturation {
				t.Fatalf("第 %d tick (饥饿,饱和)=(%d,%d)，想要精确保持 (%d,%d)",
					defaultEatingTicks-1, player.hunger, player.saturationMilli,
					testCase.hunger, testCase.saturation)
			}
			if player.eating.progressTicks != defaultEatingTicks-1 {
				t.Fatalf("第 %d tick 进度=%d，想要 %d（夹具没有连续推进就测不到结算 tick）",
					defaultEatingTicks-1, player.eating.progressTicks, defaultEatingTicks-1)
			}

			engine.Step()
			if got := hotbarCount(player, 0); got != 1 {
				t.Fatalf("第 %d tick 面包数=%d，想要 1", defaultEatingTicks, got)
			}
			if player.hunger != testCase.wantHunger ||
				player.saturationMilli != testCase.wantSaturation {
				t.Fatalf("结算后 (饥饿,饱和)=(%d,%d)，想要 (%d,%d)",
					player.hunger, player.saturationMilli,
					testCase.wantHunger, testCase.wantSaturation)
			}
			if player.eating != (eatingState{}) {
				t.Fatalf("结算后进食状态=%+v，想要清空", player.eating)
			}
		})
	}
}

// TestEatingReleaseKeepsFoodAndRestartsFromZero 覆盖 Scenario「中途松手不扣料」。
//
// 松手那一 tick 只断言"面包数不变"是不够的：进度若只是停住而没有清零，再按住
// 一 tick 就会立刻结算。因此这里在松手之后**重新按住整整 `EatingTicks - 1`
// tick 并断言仍未结算**，最后一 tick 才允许结算——重新计时是从 0 起而不是从 17 起。
func TestEatingReleaseKeepsFoodAndRestartsFromZero(t *testing.T) {
	engine, player := readyEatingPlayer(t, 12, 0)
	setEatingSlot(player, 0, core.ItemStack{Item: core.ItemBread, Count: 2})
	player.eatingHeld = true

	const releasedAt = 17
	for range releasedAt {
		engine.Step()
	}
	// 夹具自证：中断必须发生在 (0, EatingTicks) 的开区间内，否则测的是
	// "没开始"或"已结算"，不是中断。
	if player.eating.progressTicks != releasedAt {
		t.Fatalf("松手前进度=%d，想要 %d", player.eating.progressTicks, releasedAt)
	}

	player.eatingHeld = false
	engine.Step()
	if player.eating != (eatingState{}) {
		t.Fatalf("松手后进食状态=%+v，想要清空", player.eating)
	}
	if got := hotbarCount(player, 0); got != 2 {
		t.Fatalf("松手后面包数=%d，想要精确保持 2", got)
	}
	if player.hunger != 12 || player.saturationMilli != 0 {
		t.Fatalf("松手后 (饥饿,饱和)=(%d,%d)，想要精确保持 (12,0)",
			player.hunger, player.saturationMilli)
	}

	player.eatingHeld = true
	for range defaultEatingTicks - 1 {
		engine.Step()
	}
	if got := hotbarCount(player, 0); got != 2 {
		t.Fatalf("重按第 %d tick 面包数=%d，想要精确保持 2（重按必须从 0 重新计时）",
			defaultEatingTicks-1, got)
	}
	engine.Step()
	if got := hotbarCount(player, 0); got != 1 {
		t.Fatalf("重按第 %d tick 面包数=%d，想要 1", defaultEatingTicks, got)
	}
	if player.hunger != 17 {
		t.Fatalf("重按结算后饥饿=%d，想要 17", player.hunger)
	}
}

// TestEatingSlotSwitchRestartsAndConsumesNeitherSlot 覆盖 Scenario「中途切换
// 栏位不扣料」。
//
// 目标格里放的是**另一块面包**，不是空格也不是非食物：换成空格或小麦，这条
// 用例测到的就只是"非食物不可进食"，与"切栏位"毫无关系（那是另一条 Scenario）。
// 两格都是食物时，唯一能让"两格都不扣"成立的实现就是把 `(slot, item)` 记进
// 状态并逐 tick 核对。
//
// 切换后再推进 `EatingTicks - releasedAt` tick，让**总握持 tick 数恰好等于
// `EatingTicks`**：不核对 `(slot, item)` 的实现会在这一 tick 从第 17 tick 的
// 进度上直接结算并扣掉目标格的面包，正确实现则刚重新计到第 15 tick。
func TestEatingSlotSwitchRestartsAndConsumesNeitherSlot(t *testing.T) {
	engine, player := readyEatingPlayer(t, 12, 0)
	setEatingSlot(player, 1, core.ItemStack{Item: core.ItemBread, Count: 3})
	setEatingSlot(player, 0, core.ItemStack{Item: core.ItemBread, Count: 2})
	player.eatingHeld = true

	const switchedAt = 17
	for range switchedAt {
		engine.Step()
	}
	if player.eating.progressTicks != switchedAt {
		t.Fatalf("切格前进度=%d，想要 %d", player.eating.progressTicks, switchedAt)
	}

	player.inventory.Hotbar.Selected = 1
	for range defaultEatingTicks - switchedAt {
		engine.Step()
	}
	if got := hotbarCount(player, 0); got != 2 {
		t.Fatalf("切格后原栏位面包数=%d，想要精确保持 2", got)
	}
	if got := hotbarCount(player, 1); got != 3 {
		t.Fatalf("切格后新栏位面包数=%d，想要精确保持 3", got)
	}
	if player.hunger != 12 || player.saturationMilli != 0 {
		t.Fatalf("切格后 (饥饿,饱和)=(%d,%d)，想要精确保持 (12,0)",
			player.hunger, player.saturationMilli)
	}
	want := eatingState{
		slot: 1, item: core.ItemBread,
		progressTicks: defaultEatingTicks - switchedAt,
	}
	if player.eating != want {
		t.Fatalf("切格后进食状态=%+v，想要 %+v（新栏位必须从 0 重新计时）",
			player.eating, want)
	}
}

// TestEatingDoesNotStartWhenHungerIsFull 覆盖 Scenario「饥饿已满不推进」：
// 按住远超一次进食所需的 tick 数，进度必须**逐 tick**恒为零。
//
// 逐 tick 检查而不是只看末态：只看末态的话，"推进到 32 结算一次又清空"的实现
// 会在 64 tick 之后同样读出零进度，只有面包数会露馅——而面包数在
// `Consume` 之外的错误路径上未必变化。
func TestEatingDoesNotStartWhenHungerIsFull(t *testing.T) {
	engine, player := readyEatingPlayer(t, core.MaxHunger, core.InitialSaturationMilli)
	setEatingSlot(player, 0, core.ItemStack{Item: core.ItemBread, Count: 2})
	player.eatingHeld = true

	for tick := 1; tick <= 64; tick++ {
		engine.Step()
		if player.eating != (eatingState{}) {
			t.Fatalf("饥饿已满时第 %d tick 进食状态=%+v，想要恒为空", tick, player.eating)
		}
	}
	if got := hotbarCount(player, 0); got != 2 {
		t.Fatalf("饥饿已满时面包数=%d，想要精确保持 2", got)
	}
	if player.hunger != core.MaxHunger || player.saturationMilli != core.InitialSaturationMilli {
		t.Fatalf("饥饿已满时 (饥饿,饱和)=(%d,%d)，想要精确保持 (%d,%d)",
			player.hunger, player.saturationMilli, core.MaxHunger, core.InitialSaturationMilli)
	}
}

// TestNonFoodNeverAdvancesEating 覆盖 Scenario「非食物不可进食」：手持小麦
// （农业闭环里最像食物的那个物品——它是面包的原料）按住 64 tick，数量与饥饿
// 值都必须精确不变，进度逐 tick 恒零。
func TestNonFoodNeverAdvancesEating(t *testing.T) {
	engine, player := readyEatingPlayer(t, 12, 0)
	setEatingSlot(player, 0, core.ItemStack{Item: core.ItemWheat, Count: 3})
	player.eatingHeld = true
	// 夹具自证：这格确实不是食物，否则本用例测的是别的东西。
	if _, _, edible := core.FoodValue(core.ItemWheat); edible {
		t.Fatal("小麦被判为食物，夹具选错了物品")
	}

	for tick := 1; tick <= 64; tick++ {
		engine.Step()
		if player.eating != (eatingState{}) {
			t.Fatalf("手持小麦第 %d tick 进食状态=%+v，想要恒为空", tick, player.eating)
		}
	}
	if got := hotbarCount(player, 0); got != 3 {
		t.Fatalf("手持小麦 64 tick 后数量=%d，想要精确保持 3", got)
	}
	if player.hunger != 12 || player.saturationMilli != 0 {
		t.Fatalf("手持小麦 64 tick 后 (饥饿,饱和)=(%d,%d)，想要精确保持 (12,0)",
			player.hunger, player.saturationMilli)
	}
}

// TestDamageInterruptsEatingOnlyWhenHealthActuallyDrops 覆盖「受伤中断」，
// 并把中断挂点钉在 `applyDamage` 的**扣血分支**上而不是它的入口。
//
// 两条子用例成对：`applyDamage(1)` 必须清空进度，`applyDamage(0)` 必须**不**
// 清空。只写前者的话，把清空写在 `applyDamage` 的第一行（非正伤害也清）同样
// 全绿——而摔落曲线在安全高度每次落地都会算出负值，那种实现会让"跳一下就
// 打断进食"，且没有任何信号。
func TestDamageInterruptsEatingOnlyWhenHealthActuallyDrops(t *testing.T) {
	const interruptedAt = 17

	t.Run("真正扣血清空进度且不扣料", func(t *testing.T) {
		engine, player := readyEatingPlayer(t, 12, 0)
		setEatingSlot(player, 0, core.ItemStack{Item: core.ItemBread, Count: 2})
		player.eatingHeld = true
		for range interruptedAt {
			engine.Step()
		}
		if player.eating.progressTicks != interruptedAt {
			t.Fatalf("受伤前进度=%d，想要 %d", player.eating.progressTicks, interruptedAt)
		}

		healthBefore := player.health
		player.applyDamage(1)
		if player.health != healthBefore-1 {
			t.Fatalf("受伤后生命值=%d，想要 %d（夹具必须真的扣血）",
				player.health, healthBefore-1)
		}
		if player.eating != (eatingState{}) {
			t.Fatalf("受伤后进食状态=%+v，想要清空", player.eating)
		}
		if got := hotbarCount(player, 0); got != 2 {
			t.Fatalf("受伤中断后面包数=%d，想要精确保持 2", got)
		}
		if player.hunger != 12 || player.saturationMilli != 0 {
			t.Fatalf("受伤中断后 (饥饿,饱和)=(%d,%d)，想要精确保持 (12,0)",
				player.hunger, player.saturationMilli)
		}
	})

	t.Run("零伤害不中断", func(t *testing.T) {
		engine, player := readyEatingPlayer(t, 12, 0)
		setEatingSlot(player, 0, core.ItemStack{Item: core.ItemBread, Count: 2})
		player.eatingHeld = true
		for range interruptedAt {
			engine.Step()
		}

		healthBefore := player.health
		player.applyDamage(0)
		if player.health != healthBefore {
			t.Fatalf("零伤害后生命值=%d，想要保持 %d", player.health, healthBefore)
		}
		if player.eating.progressTicks != interruptedAt {
			t.Fatalf("零伤害后进度=%d，想要保持 %d（非正伤害是 no-op，不是中断）",
				player.eating.progressTicks, interruptedAt)
		}
	})
}

// TestDeathClearsEatingProgressAndResetsHunger 覆盖「死亡中断」：死亡结算那一
// tick 之后，进食进度与三层饥饿状态必须一起回到初态。
//
// 生命值**直接置零**而不是走 `applyDamage`：走伤害入口的话，清空进食状态的是
// 伤害路径，死亡路径漏清也照样全绿。这里要钉的是死亡/重生路径自己也清。
func TestDeathClearsEatingProgressAndResetsHunger(t *testing.T) {
	engine, player := readyEatingPlayer(t, 12, 0)
	setEatingSlot(player, 0, core.ItemStack{Item: core.ItemBread, Count: 2})
	player.eatingHeld = true
	for range 17 {
		engine.Step()
	}
	if player.eating.progressTicks != 17 {
		t.Fatalf("死亡前进度=%d，想要 17", player.eating.progressTicks)
	}

	player.health = 0
	engine.Step()
	if player.eating != (eatingState{}) {
		t.Fatalf("死亡结算后进食状态=%+v，想要清空", player.eating)
	}
	if player.hunger != core.MaxHunger || player.saturationMilli != core.InitialSaturationMilli {
		t.Fatalf("死亡结算后 (饥饿,饱和)=(%d,%d)，想要初值 (%d,%d)",
			player.hunger, player.saturationMilli,
			core.MaxHunger, core.InitialSaturationMilli)
	}
}

// TestCraftBreadFromWheatViaCommand 覆盖 Scenario「小麦合成面包」的 sim 层：
// 3 个小麦经既有 `CommandCraftRecipe` 原子换成 1 个面包。
//
// core 层的配方表由 internal/core 的用例覆盖；这里守的是"进食的食物真的能从
// 农业闭环的产物做出来"，也就是命令路径确实接了 `core.RecipeBread`。
func TestCraftBreadFromWheatViaCommand(t *testing.T) {
	engine, player := readyEatingPlayer(t, 12, 0)
	setEatingSlot(player, 0, core.ItemStack{Item: core.ItemWheat, Count: 3})

	engine.Enqueue(Command{
		Session: eatingTestSession, Sequence: 2,
		Kind: CommandCraftRecipe, Recipe: core.RecipeBread,
	})
	result := engine.Step()
	if len(result.Rejected) != 0 {
		t.Fatalf("合成面包被拒绝: %+v", result.Rejected)
	}
	want := core.ItemStack{Item: core.ItemBread, Count: 1}
	if got := player.inventory.Hotbar.Slots[0]; got != want {
		t.Fatalf("合成后 0 号格=%+v，想要 %+v（3 小麦原子换 1 面包）", got, want)
	}
}

// TestEatingTicksComesFromTunableSnapshot 钉住「所需 tick 数来自本 tick 的
// tunable 快照，不是写死的编译期常量」：同一份夹具在两个不同的 `EatingTicks`
// 下必须在**各自**的那一 tick 结算。
//
// 这条直接调用 `advanceEating`（不经引擎）：引擎级用例只跑得到默认值 32，
// 把 32 写死的实现在那里全绿。形状照 `TestApplyExhaustionReadsThresholdFromParameter`。
func TestEatingTicksComesFromTunableSnapshot(t *testing.T) {
	for _, ticks := range []uint16{8, defaultEatingTicks} {
		player := &playerState{hunger: 12, eatingHeld: true}
		player.inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemBread, Count: 2}
		for tick := uint16(1); tick < ticks; tick++ {
			player.advanceEating(ticks)
			if player.inventory.Hotbar.Slots[0].Count != 2 || player.hunger != 12 {
				t.Fatalf("EatingTicks=%d 时第 %d tick 已结算，想要未结算", ticks, tick)
			}
		}
		player.advanceEating(ticks)
		if player.inventory.Hotbar.Slots[0].Count != 1 || player.hunger != 17 {
			t.Fatalf("EatingTicks=%d 时第 %d tick (面包,饥饿)=(%d,%d)，想要 (1,17)",
				ticks, ticks, player.inventory.Hotbar.Slots[0].Count, player.hunger)
		}
	}
}
