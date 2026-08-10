package world

import (
	"math"

	"minecraft-go/internal/core"
)

// FurnaceSlotBytes 是单个熔炉槽的固定存档编码长度。
const FurnaceSlotBytes = 4 + 1 + 4 + 3*3 + 1 + 2

// FurnaceSlot 是区块中的一个固定熔炉槽。
// 槽始终保留 Generation，Active 为 false 时其余字段必须是零值。
type FurnaceSlot struct {
	Generation    uint32
	Active        bool
	BlockIndex    uint32
	Input         core.ItemStack
	Fuel          core.ItemStack
	Output        core.ItemStack
	ProgressTicks uint8
	BurnTicks     uint16
}

// Valid 报告该槽是否满足熔炉的固定约束。
func (slot FurnaceSlot) Valid() bool {
	if !slot.Active {
		return slot == FurnaceSlot{Generation: slot.Generation}
	}
	if slot.Generation == 0 {
		return false
	}
	if slot.ProgressTicks >= core.FurnaceSmeltTicks || slot.BurnTicks > core.FurnaceBurnTicks {
		return false
	}
	return validFurnaceInput(slot.Input) &&
		slot.Fuel.Valid() && (slot.Fuel.Item == core.ItemNone || slot.Fuel.Item == core.ItemCoal) &&
		validFurnaceOutput(slot.Output)
}

// validFurnaceInput 报告输入格是否为空或装着已注册的熔炼输入。
func validFurnaceInput(stack core.ItemStack) bool {
	if !stack.Valid() {
		return false
	}
	if stack.Item == core.ItemNone {
		return true
	}
	_, ok := core.SmeltingOutput(stack.Item)
	return ok
}

// validFurnaceOutput 报告输出格是否为空或装着固定熔炼产物。
func validFurnaceOutput(stack core.ItemStack) bool {
	if !stack.Valid() {
		return false
	}
	switch stack.Item {
	case core.ItemNone, core.ItemIronIngot, core.ItemGlass, core.ItemBrick:
		return true
	default:
		return false
	}
}

// Furnace 返回指定槽的当前值；槽位越界时返回零值。
func (c *Chunk) Furnace(slot int) FurnaceSlot {
	if slot < 0 || slot >= core.FurnacesPerChunk {
		return FurnaceSlot{}
	}
	return c.furnaces[slot]
}

// SetFurnace 直接写入一个槽，供存档恢复与权威 tick 更新使用。
func (c *Chunk) SetFurnace(slot int, value FurnaceSlot) {
	if slot < 0 || slot >= core.FurnacesPerChunk {
		return
	}
	c.furnaces[slot] = value
}

// FurnaceAt 返回该方块索引上的活动熔炉槽。
func (c *Chunk) FurnaceAt(blockIndex uint32) (int, bool) {
	for slot := range c.furnaces {
		if c.furnaces[slot].Active && c.furnaces[slot].BlockIndex == blockIndex {
			return slot, true
		}
	}
	return 0, false
}

// PrepareFurnace 预检可用于该方块位置的槽：同位置已有活动熔炉时拒绝，
// 否则返回最低的可复用空槽。它不修改区块，因此调用方可以先预检再原子提交。
func (c *Chunk) PrepareFurnace(blockIndex uint32) (int, bool) {
	if _, exists := c.FurnaceAt(blockIndex); exists {
		return 0, false
	}
	for slot := range c.furnaces {
		if !c.furnaces[slot].Active && c.furnaces[slot].Generation != math.MaxUint32 {
			return slot, true
		}
	}
	return 0, false
}

// CommitFurnace 启用 PrepareFurnace 返回的槽并返回新的 generation。
func (c *Chunk) CommitFurnace(slot int, blockIndex uint32) uint32 {
	if slot < 0 || slot >= core.FurnacesPerChunk {
		return 0
	}
	generation := c.furnaces[slot].Generation + 1
	c.furnaces[slot] = FurnaceSlot{
		Generation: generation,
		Active:     true,
		BlockIndex: blockIndex,
	}
	return generation
}

// DeactivateFurnace 停用一个槽并保留其 generation，使旧引用不会再次生效。
func (c *Chunk) DeactivateFurnace(slot int) {
	if slot < 0 || slot >= core.FurnacesPerChunk {
		return
	}
	c.furnaces[slot] = FurnaceSlot{Generation: c.furnaces[slot].Generation}
}
