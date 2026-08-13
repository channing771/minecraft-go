package mesh

import "github.com/channing771/mornlea/internal/world"

// LightScratch 保存一次 native 区段网格化复用的输入、光照与输出缓冲。
type LightScratch struct {
	input  []byte
	native []uint64
	packed [maxNativeQuads]uint64
}

// NewLightScratch 创建调用方独占的固定容量 native scratch。
func NewLightScratch() *LightScratch {
	return &LightScratch{
		input:  make([]byte, maxNativeInputBytes),
		native: make([]uint64, (nativeScratchBytes+7)/8),
	}
}

// MeshSection 把一个区段交给 Rust 转换成贪心合并后的四边形集合。
func MeshSection(n *world.Neighborhood, reg Registry, scratch *LightScratch) []Quad {
	return meshSectionNative(n, reg, scratch, nativeABIVersionCurrent)
}

func meshSectionNativeVersionForTest(n *world.Neighborhood, reg Registry, scratch *LightScratch, version uint32) []Quad {
	return meshSectionNative(n, reg, scratch, version)
}

func meshSectionNative(n *world.Neighborhood, reg Registry, scratch *LightScratch, version uint32) []Quad {
	if scratch == nil {
		panic("mesh: nil light scratch")
	}
	if id, single := n.Center.Blocks.IsUniform(); single && id == world.AirID {
		return nil
	}

	inputLen, err := encodeNativeInput(scratch.input, n, reg.MeshSnapshot())
	if err != nil {
		panic(err)
	}
	status, count := nativeMeshSectionVersion(version, scratch.input[:inputLen], scratch.native, scratch.packed[:])
	if status != nativeStatusOK {
		panic(nativeStatusPanicText(status))
	}
	if count < 0 || count > len(scratch.packed) {
		panic(nativeStatusPanicText(nativeStatusOutputOverflow))
	}
	out := make([]Quad, count)
	for i := range out {
		out[i] = UnpackQuad(scratch.packed[i])
	}
	return out
}
