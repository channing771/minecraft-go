package world

import "github.com/channing771/mornlea/internal/core"

// BarrierID 是一个内部专用的实心方块 ID，玩家永远看不到它。
//
// 它用来表示“这里的数据还没加载，但请当作实心”——见 Neighborhood.At。
const BarrierID = core.BarrierID

// Section 是一个 16³ 的方块区段。
type Section struct {
	Blocks *PalettedContainer
}

// NewSection 创建一个全空气的区段。
func NewSection() *Section {
	return &Section{Blocks: NewPalettedContainer(AirID)}
}

// Clone 返回深拷贝，供 COW 使用（spec §4.3）。
func (s *Section) Clone() *Section {
	return &Section{Blocks: s.Blocks.Clone()}
}
