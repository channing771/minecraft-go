package fluid

import "github.com/channing771/mornlea/internal/core"

// memWorld 是 FluidWorld 的内存测试替身：未显式写入的格视为空气。
// 只用于测试，不导出。
type memWorld struct {
	blocks map[core.BlockPos]core.BlockID
}

// newMemWorld 构造一个初始为空（全空气）的内存世界。
func newMemWorld() *memWorld {
	return &memWorld{blocks: make(map[core.BlockPos]core.BlockID)}
}

// BlockAt 实现 FluidWorld：未记录的格返回 core.AirID。
func (m *memWorld) BlockAt(pos core.BlockPos) core.BlockID {
	if id, ok := m.blocks[pos]; ok {
		return id
	}
	return core.AirID
}

// SetBlock 实现 FluidWorld。写入 core.AirID 时删除记录而非保留零值，
// 使内部 map 大小只随非空气格增长，避免测试断言里把“未写入”和
// “显式写成空气”混为一谈时产生歧义（两者在读取语义上完全等价）。
func (m *memWorld) SetBlock(pos core.BlockPos, id core.BlockID) {
	if id == core.AirID {
		delete(m.blocks, pos)
		return
	}
	m.blocks[pos] = id
}
