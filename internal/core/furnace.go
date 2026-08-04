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

// FurnaceRef 在熔炉的生命周期内唯一且稳定地标识它。
// 槽位复用时 Generation 递增，因此旧引用不会与新熔炉冲突。
type FurnaceRef struct {
	Dimension  DimensionID
	Chunk      ChunkPos
	Slot       uint8
	Generation uint32
}
