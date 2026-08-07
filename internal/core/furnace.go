package core

// FurnacesPerChunk 是单个区块可持有的固定权威熔炉槽数。
const FurnacesPerChunk = 32

// 熔炼计时上限：每个铁锭需要 200 个进度 tick，每个煤炭提供 1600 个燃烧 tick。
const (
	FurnaceSmeltTicks = 200
	FurnaceBurnTicks  = 1600
)

// 熔炉界面的统一栏位：0..35 是玩家物品栏，36、37、38 分别是输入、燃料和输出。
const (
	FurnaceInputSlot  = InventorySlots
	FurnaceFuelSlot   = InventorySlots + 1
	FurnaceOutputSlot = InventorySlots + 2
	FurnaceViewSlots  = InventorySlots + 3
)

// FurnaceRef 是 ContainerRef 的类型别名：熔炉是容器种类的零值 ContainerKindFurnace，
// 因此 M4E/M4J 遗留的既有构造、比较与存档路径都不需要因为容器收敛而改动，
// 线上也只保留一份引用编解码 helper。
type FurnaceRef = ContainerRef
