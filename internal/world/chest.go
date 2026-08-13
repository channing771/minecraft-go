package world

import (
	"math"

	"github.com/channing771/mornlea/internal/core"
)

// ChestSlotBytes 是单个箱子槽的固定存档编码长度：
// generation(4) + active(1) + blockIndex(4) + 27 个物品格 × 5 字节（item u16 + count u8 + durability u16）。
const ChestSlotBytes = 4 + 1 + 4 + core.ChestSlots*5

// ChestSlot 是区块中的一个固定箱子槽。
// 槽始终保留 Generation，Active 为 false 时其余字段必须是零值。
type ChestSlot struct {
	Generation uint32
	Active     bool
	BlockIndex uint32
	Items      [core.ChestSlots]core.ItemStack
}

// Valid 报告该槽是否满足箱子的固定约束。箱子没有计时状态与物品类型限制，
// 27 个格子只要求每格是规范空值或已注册物品。
func (slot ChestSlot) Valid() bool {
	if !slot.Active {
		return slot == ChestSlot{Generation: slot.Generation}
	}
	if slot.Generation == 0 {
		return false
	}
	for _, stack := range slot.Items {
		if !stack.Valid() {
			return false
		}
	}
	return true
}

// Chest 返回指定槽的当前值；槽位越界时返回零值。
func (c *Chunk) Chest(slot int) ChestSlot {
	if slot < 0 || slot >= core.ChestsPerChunk {
		return ChestSlot{}
	}
	return c.chests[slot]
}

// SetChest 直接写入一个槽，供存档恢复与权威命令处理使用。
func (c *Chunk) SetChest(slot int, value ChestSlot) {
	if slot < 0 || slot >= core.ChestsPerChunk {
		return
	}
	c.chests[slot] = value
}

// ChestAt 返回该方块索引上的活动箱子槽。
func (c *Chunk) ChestAt(blockIndex uint32) (int, bool) {
	for slot := range c.chests {
		if c.chests[slot].Active && c.chests[slot].BlockIndex == blockIndex {
			return slot, true
		}
	}
	return 0, false
}

// PrepareChest 预检可用于该方块位置的槽：同位置已有活动箱子时拒绝，
// 否则返回最低的可复用空槽。它不修改区块，因此调用方可以先预检再原子提交。
func (c *Chunk) PrepareChest(blockIndex uint32) (int, bool) {
	if _, exists := c.ChestAt(blockIndex); exists {
		return 0, false
	}
	for slot := range c.chests {
		if !c.chests[slot].Active && c.chests[slot].Generation != math.MaxUint32 {
			return slot, true
		}
	}
	return 0, false
}

// CommitChest 启用 PrepareChest 返回的槽并返回新的 generation。
func (c *Chunk) CommitChest(slot int, blockIndex uint32) uint32 {
	if slot < 0 || slot >= core.ChestsPerChunk {
		return 0
	}
	generation := c.chests[slot].Generation + 1
	c.chests[slot] = ChestSlot{
		Generation: generation,
		Active:     true,
		BlockIndex: blockIndex,
	}
	return generation
}

// DeactivateChest 停用一个槽并保留其 generation，使旧引用不会再次生效。
func (c *Chunk) DeactivateChest(slot int) {
	if slot < 0 || slot >= core.ChestsPerChunk {
		return
	}
	c.chests[slot] = ChestSlot{Generation: c.chests[slot].Generation}
}
