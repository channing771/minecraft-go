package core

// BlockID 是跨客户端/服务端稳定的全局方块编号。
type BlockID uint16

// DimensionID 标识一个世界维度。
type DimensionID int32

// BlockFace 标识方块的六个轴对齐面。
type BlockFace uint8

// ChunkKey 在指定维度中唯一定位一个区块。
type ChunkKey struct {
	Dimension DimensionID
	Pos       ChunkPos
}

// SectionKey 在指定维度中唯一定位一个区段。
type SectionKey struct {
	Dimension DimensionID
	Pos       SectionPos
}

// Overworld 是 M2A 使用的主世界维度。
const Overworld DimensionID = 0

// 方块 ID 是协议稳定值，不能重排。
const (
	AirID BlockID = iota
	BarrierID
	StoneID
	DirtID
	GrassID
	BedrockID
	StoneBrickID
	CoalOreID
	IronOreID
	FurnaceID
	IronBlockID
	ChestID
	CobblestoneID
	SmoothStoneID
	SandID
	GravelID
	OakLogID
	OakPlanksID
	LeavesID
	GlassID
	BrickID
	WhiteWoolID
	RoofTileID
	ClayID
	SnowBlockID
	MossyCobblestoneID
)

// RegisteredBlock 报告 id 是否是已注册的稳定方块编号。
func RegisteredBlock(id BlockID) bool {
	return id <= MossyCobblestoneID
}

const (
	BlockFaceNegX BlockFace = iota
	BlockFacePosX
	BlockFaceNegY
	BlockFacePosY
	BlockFaceNegZ
	BlockFacePosZ
	BlockFaceNone BlockFace = 0xff
)

// Opposite 返回相对的方块面。
func (f BlockFace) Opposite() BlockFace {
	if f == BlockFaceNone {
		return BlockFaceNone
	}
	if f > BlockFacePosZ {
		panic("core: invalid BlockFace")
	}
	return f ^ 1
}
