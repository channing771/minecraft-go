package sim

import "github.com/channing771/mornlea/internal/core"

// defaultEatingTicks 是吃完一件食物需要连续保持进食输入的 tick 数。
// 唯一读取入口是 `Tunables` 快照，不得再以导出常量暴露——见 internal/archcheck
// 的 `TestTunableConstantsAreNotExported`。
//
// 取 32 与参考实现同值：权威 tick 是 20 TPS，32 tick 是 1.6 秒。它必须足够长，
// 让"受伤打断进食"成为一件真会发生的事（否则中断规则形同虚设），又必须短到
// 玩家愿意在战斗间隙吃一口。
const defaultEatingTicks uint16 = 32

// eatingState 是玩家进食的权威进度状态机，形状与 `miningState` 同构：记录
// 开始进食时的快捷栏位、那一格里的物品与已连续推进的 tick 数。
//
// **为什么必须记 `(slot, item)` 而不是只记进度**：只记进度的话，玩家可以在
// 第 31 tick 切到另一格再让结算落在新格上——"吃 A 扣 B"。逐 tick 核对这两项
// 是"切换栏位/栏位物品变化即中断"这条规则的唯一实现。
//
// 零值就是"没有在进食"：`progressTicks == 0` 是状态机的空态哨兵，因此进度从
// 1 起而不是从 0 起（见 `advanceEating`）。
//
// 它是瞬态字段，不持久化、不进入快照/哈希：进食是按住不放的当下动作，跨重启
// 保留半口面包没有任何语义。
type eatingState struct {
	slot          uint8
	item          core.ItemID
	progressTicks uint16
}

// advanceEating 推进一名玩家一个 tick 的进食状态机，与 `advanceStarvation`
// 同形：固定整数运算、不分配、由调用方传入本 tick 的 tunable 快照值。
//
// 规则（spec authoritative-hunger「进食是持续输入驱动的权威动作」）：
//
//   - 中断：进食输入未按住、位置跳变（`reset`）、选中格里不是食物、或饥饿值
//     已满——四条里任一条成立就清空状态且**不扣料**。它们合并成同一个判断，
//     因为"中断"与"根本没开始"在观察上是同一件事：进度归零、背包不变。
//   - 开始/推进：`(slot, item)` 与记录的一致且已在进行中就 `progressTicks++`，
//     否则从这一格重新开始并把进度置 1。**开始的那一 tick 就算第 1 tick**，
//     与 `stepMiningProgress` 逐字同构：两个持续输入状态机的"第 N tick"必须
//     是同一个意思，否则采掘进度条与进食进度在客户端上永远差一格。
//   - 结算：进度达到 `EatingTicks` 时在**这一个 tick 内原子完成**扣 1 件、
//     加饥饿、加饱和、清空状态。
//
// 结算的次序是**先加饥饿再钳饱和**：饱和度的上界是"当前饥饿值"，用吃之前的
// 饥饿值去钳会凭空少给一截饱和度（吃满一块面包时差 5000）。
//
// 中间量走 uint32：`hunger + 恢复量` 与 `saturationMilli + 恢复量` 都在各自
// 类型的上界之内，但那是"当前食物表只有面包"这个事实的推论，不是类型保证——
// 加一行恢复值更大的食物就会静默回绕。
//
// 受伤与死亡这两条中断挂在别处：受伤在 `applyDamage` 的扣血分支，死亡/位置
// 跳变在 `beginReset`。它们不在本函数里，因为那两条是**事件**而不是每 tick
// 复检的条件。
//
// eatingTicks 取 max(…, 1)：配置层已把下限钳到 1，但 sim 按架构约束不得导入
// config，取 0 会让进度永远够不到结算（`progressTicks` 从 1 起）。
func (player *playerState) advanceEating(eatingTicks uint16) {
	selected := player.inventory.Hotbar.Selected
	item := player.inventory.Hotbar.Slots[selected].Item
	hungerGain, saturationGain, edible := core.FoodValue(item)
	if !player.eatingHeld || player.reset || !edible || player.hunger >= core.MaxHunger {
		player.eating = eatingState{}
		return
	}
	if player.eating.slot == selected && player.eating.item == item &&
		player.eating.progressTicks != 0 {
		player.eating.progressTicks++
	} else {
		player.eating = eatingState{slot: selected, item: item, progressTicks: 1}
	}
	if player.eating.progressTicks < max(eatingTicks, 1) {
		return
	}
	next, consumed := player.inventory.Hotbar.Consume(selected)
	// `Consume` 只在栏位越界或为空时失败，而这两种情况上面的食物判定已经排除；
	// 这里仍然检查而不是丢弃返回值：漏检会在将来某次改动后变成"没扣料却回饱"。
	if !consumed {
		player.eating = eatingState{}
		return
	}
	player.inventory.Hotbar = next
	player.inventoryDirty = true
	hunger := min(uint32(player.hunger)+uint32(hungerGain), uint32(core.MaxHunger))
	player.hunger = uint8(hunger)
	player.saturationMilli = uint16(min(
		uint32(player.saturationMilli)+uint32(saturationGain),
		hunger*uint32(core.SaturationMilliPerPoint),
	))
	player.eating = eatingState{}
}
