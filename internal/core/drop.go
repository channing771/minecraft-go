package core

// DropsPerChunk 是单个区块可持有的固定权威掉落物槽数。
const DropsPerChunk = 32

// DropInterestRadius 是掉落物 tick 与同步的固定区块半径。
const DropInterestRadius = 2

// MaxSessionDrops 是单个会话镜像的固定上限：25 个兴趣区块 × 每区块 32 槽。
const MaxSessionDrops = (2*DropInterestRadius + 1) * (2*DropInterestRadius + 1) * DropsPerChunk

// DropID 在掉落物堆的生命周期内唯一且稳定地标识它。
// 槽位复用时 Generation 递增，因此旧 ID 不会与新堆冲突。
type DropID struct {
	Dimension  DimensionID
	Chunk      ChunkPos
	Slot       uint8
	Generation uint32
}

// Valid 报告 ID 是否落在固定槽位范围内且指向一个已启用过的槽。
func (id DropID) Valid() bool {
	return id.Slot < DropsPerChunk && id.Generation != 0
}

// Compare 按维度、区块坐标、槽位、generation 给出稳定全序。
func (id DropID) Compare(other DropID) int {
	if id.Dimension != other.Dimension {
		return compareInt32(int32(id.Dimension), int32(other.Dimension))
	}
	if id.Chunk.X != other.Chunk.X {
		return compareInt32(id.Chunk.X, other.Chunk.X)
	}
	if id.Chunk.Z != other.Chunk.Z {
		return compareInt32(id.Chunk.Z, other.Chunk.Z)
	}
	if id.Slot != other.Slot {
		return compareInt32(int32(id.Slot), int32(other.Slot))
	}
	if id.Generation != other.Generation {
		if id.Generation < other.Generation {
			return -1
		}
		return 1
	}
	return 0
}

func compareInt32(left, right int32) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}
