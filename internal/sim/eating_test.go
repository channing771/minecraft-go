package sim

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/physics"
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
	// 夹具自证：中断必须发生在 (0, `EatingTicks`) 的开区间内，否则测的是
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

// TestEatingSameSlotItemSwapRestartsAndConsumesNeither 覆盖 Scenario「栏位物品
// 变化即中断」的另一半：**不切格**，只把选中格里的东西换掉。
//
// 与 `TestEatingSlotSwitchRestartsAndConsumesNeitherSlot` 成对：那条动的是
// `Selected`，这条动的是 `Slots[selected].Item`。两条合起来才是
// `eatingState` 里 `(slot, item)` 这个二元组的完整覆盖——只测切格的话，
// 「按住不放时手里的东西被换掉」这条路一个 tick 都没跑过。
//
// 第二条子用例直接调用 `advanceEating` 而不经引擎，因为**今天的食物表只有
// 面包**（`core.FoodValue` 只对 `core.ItemBread` 返回 true）：经引擎换物品必然
// 换成非食物，会先被 `!edible` 那条中断吃掉，`item` 这一项在引擎路径上根本
// 到不了。它是为「食物表加第二项」准备的前置防御（那一天「吃 A 扣 B」会真的
// 可达），所以必须由构造出来的状态承重，否则这一项零覆盖、删掉全绿。
func TestEatingSameSlotItemSwapRestartsAndConsumesNeither(t *testing.T) {
	const swappedAt = 17

	t.Run("同格换成非食物再换回来必须从 0 重新计时", func(t *testing.T) {
		engine, player := readyEatingPlayer(t, 12, 0)
		setEatingSlot(player, 0, core.ItemStack{Item: core.ItemBread, Count: 2})
		player.eatingHeld = true
		for range swappedAt {
			engine.Step()
		}
		if player.eating.progressTicks != swappedAt {
			t.Fatalf("换物品前进度=%d，想要 %d", player.eating.progressTicks, swappedAt)
		}

		// 只换内容，不动 `Selected`：面包整摞离开这一格，换成 3 小麦。
		wheat := core.ItemStack{Item: core.ItemWheat, Count: 3}
		player.inventory.Hotbar.Slots[0] = wheat
		for range defaultEatingTicks - swappedAt {
			engine.Step()
		}
		if got := player.inventory.Hotbar.Slots[0]; got != wheat {
			t.Fatalf("换物品后 0 号格=%+v，想要精确保持 %+v", got, wheat)
		}
		if player.eating != (eatingState{}) {
			t.Fatalf("换物品后进食状态=%+v，想要清空", player.eating)
		}
		if player.hunger != 12 || player.saturationMilli != 0 {
			t.Fatalf("换物品后 (饥饿,饱和)=(%d,%d)，想要精确保持 (12,0)",
				player.hunger, player.saturationMilli)
		}

		// 换回面包并继续按住：面包数就是"原来那两块一件没少"的记账口——它已经
		// 离开过这一格，只能这样断言。重新计时必须从 0 起，因此第
		// `EatingTicks - 1` tick 仍不许结算。
		player.inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemBread, Count: 2}
		for range defaultEatingTicks - 1 {
			engine.Step()
		}
		if got := hotbarCount(player, 0); got != 2 {
			t.Fatalf("换回面包第 %d tick 面包数=%d，想要精确保持 2（换物品必须从 0 重新计时）",
				defaultEatingTicks-1, got)
		}
		if player.hunger != 12 || player.saturationMilli != 0 {
			t.Fatalf("换回面包第 %d tick (饥饿,饱和)=(%d,%d)，想要精确保持 (12,0)",
				defaultEatingTicks-1, player.hunger, player.saturationMilli)
		}
	})

	t.Run("记录物品与当前物品不一致时重新计时而不结算", func(t *testing.T) {
		player := &playerState{hunger: 12, eatingHeld: true}
		player.inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemBread, Count: 2}
		// 记录的是另一物品、进度差一 tick 就结算：核对 `item` 的实现从 1 重数，
		// 只核对 `slot` 的实现会在这一 tick 直接吃掉面包。
		player.eating = eatingState{
			slot: 0, item: core.ItemWheat, progressTicks: defaultEatingTicks - 1,
		}

		player.advanceEating(defaultEatingTicks)
		want := eatingState{slot: 0, item: core.ItemBread, progressTicks: 1}
		if player.eating != want {
			t.Fatalf("进食状态=%+v，想要 %+v", player.eating, want)
		}
		if got := player.inventory.Hotbar.Slots[0].Count; got != 2 {
			t.Fatalf("面包数=%d，想要精确保持 2（记录物品不一致不得结算）", got)
		}
		if player.hunger != 12 {
			t.Fatalf("饥饿=%d，想要精确保持 12", player.hunger)
		}
	})
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

// TestCompanionsNeverEat 是「伙伴不接进食」的**运行时**守卫：一名伙伴手持
// 面包并被驱动完整条移动与采掘路径，远超一次进食所需的 tick 数之后，那两块
// 面包必须一件不少。
//
// 为什么不断言「`companionState` 没有进食字段」：那是存在性断言，编译期就
// 恒真，在「有人把 `advanceEating` 接进伙伴 tick」的世界里也可能成立（伙伴
// 完全可以复用 `playerState` 之外的另一份进度）。这里断言的是位置性事实——
// 伙伴的动作**没有任何途径**吃掉手里的食物。
//
// 它与源码守卫 `TestExhaustionTableIsNotWiredIntoCompanionCode`（其禁用清单
// 已含进食标识符）是互补的两条，**不得只保留其中一条**：源码守卫是名字驱动
// 的，看不见"换个名字重写一遍"；本用例是夹具驱动的，只覆盖被驱动到的路径。
func TestCompanionsNeverEat(t *testing.T) {
	engine, _, _ := readyMiningPlayers(t, 1)
	id := companionTestID(2)
	activateCompanionAt(t, engine, id, mgl32.Vec3{4.5, 1, 8.5})
	entry := engine.companions[id]
	entry.inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemBread, Count: 2}
	entry.inventory.Hotbar.Selected = 0
	// 夹具自证：面包确实是食物，否则本用例在"伙伴其实会吃"的世界里也会绿。
	if _, _, edible := core.FoodValue(core.ItemBread); !edible {
		t.Fatal("面包不是食物，夹具选错了物品")
	}

	// 伙伴采掘：原地走完整条权威采掘出口，tick 数远超一次进食（32）。采掘意图
	// 是按住语义、跨 tick 保持，直接装配即可。
	companionTarget := core.BlockPos{X: 4, Y: 1, Z: 5}
	engine.SetBlockForTest(companionTarget, core.CoalOreID)
	entry.miningTarget = companionTarget
	entry.miningHeld = true
	for range 64 {
		engine.Step()
	}

	// 伙伴移动：另起 64 tick，同样远超一次进食。移动必须逐 tick 经
	// `CompanionActionMove` 意图管线提交——`applyCompanionActions` 对没有 action
	// 的伙伴每 tick 用 `physics.Input{Yaw: entry.yaw}` 覆盖 `entry.input`，直接写
	// `entry.input` 的夹具会被这一步抹掉，伙伴一步不动，这半边就是空转的。
	//
	// 移动前松开采掘：伙伴会走出触及距离，继续按住只会让采掘每 tick 被距离校验
	// 拒绝，把上面那段已经跑通的采掘路径换成一条拒绝路径。位移跨区块，因此逐
	// tick 按既有 `feedCompanionActionRequests` 惯例供给新订阅的区块。
	entry.miningHeld = false
	start := entry.state.Position
	for tick := range 64 {
		if !engine.EnqueueCompanionAction(CompanionAction{
			ID: id, Kind: CompanionActionMove,
			Input: physics.Input{MoveX: 1, Jump: true},
		}) {
			t.Fatalf("tick %d 移动 action 未入队", tick)
		}
		feedCompanionActionRequests(t, engine, engine.Step())
	}
	// 夹具自证：伙伴确实位移了。移动这半边一旦被输入覆盖打回原地，本条断言先红，
	// 而不是让"伙伴不进食"在一个根本没动过的伙伴身上空绿。
	if entry.state.Position == start {
		t.Fatalf("伙伴位置=%+v，与起点相同（移动夹具空转）", entry.state.Position)
	}

	if got := companionItemCount(entry, core.ItemBread); got != 2 {
		t.Fatalf("伙伴的面包数=%d，想要精确保持 2（伙伴不进食）", got)
	}
}
