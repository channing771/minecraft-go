package core

// ChestsPerChunk 是单个区块可持有的固定权威箱子槽数。
const ChestsPerChunk = 16

// ChestSlots 是单个箱子的固定物品格数。
const ChestSlots = 27

// 箱子界面的统一栏位：0..35 是玩家物品栏，36..62 是箱子的 27 个格子。
const (
	ChestFirstSlot = InventorySlots
	ChestViewSlots = InventorySlots + ChestSlots
)
