// Package mesh 把区段方块数据转换成 GPU 实例数据。
//
// 本包是纯函数：区段快照进、四边形数组出，不碰 GPU。
package mesh

// Face 是方块的 6 个面之一。编号规则：axis = Face>>1（0=X,1=Y,2=Z），
// 正方向 = Face&1 == 1。着色器依赖这个编码。
type Face uint8

const (
	FaceNegX Face = iota
	FacePosX
	FaceNegY
	FacePosY
	FaceNegZ
	FacePosZ
)

// Axis 返回该面的法线所在轴：0=X, 1=Y, 2=Z。
func (f Face) Axis() int { return int(f) >> 1 }

// Positive 返回该面法线是否指向轴的正方向。
func (f Face) Positive() bool { return f&1 == 1 }

// Quad 是一个贪心合并后的矩形面，也是 GPU 的一条实例数据。
type Quad struct {
	X, Y, Z uint8
	W, H    uint8
	Face    Face
	Mat     uint16
	AO      uint8
	Light   uint8
	// Corners 是水面 quad 四个顶点的 4-bit 高度原值，实际高度 (v+1)/16，
	// 顺序与环境光遮蔽的角顺序一致：局部 (u,v) 的 (0,0) (1,0) (1,1) (0,1)。
	// 只有落在该格顶面那一层的顶点带高度，其余顶点在方块底面、记 0；
	// 非水面 quad 四项全 0。详见 engine 的 quad.rs。
	Corners [4]uint8
}

const (
	shiftX     = 0
	shiftY     = 4
	shiftZ     = 8
	shiftW     = 12
	shiftH     = 16
	shiftFace  = 20
	shiftMat   = 23
	shiftAO    = 39
	shiftLight = 47
	// 水面 quad 的角高度位布局，与 engine 的 quad.rs 逐位对应：
	// 角 0 占 bit 12..15、角 1 占 bit 16..19（借走恒为 1 的 W/H），
	// 角 2 占 bit 55..58、角 3 占 bit 59..62（quad 布局仅存的空闲位）。
	// bit 63 仍然留空，**quad 实例保持 8 字节**。
	shiftCorner2 = 55
	shiftCorner3 = 59
)

// Pack 把四边形压成 8 字节，供 GPU 实例缓冲直接使用。
//
// 带角高度的水面 quad 借走 W/H 的 8 bit，因此必须是 1×1——水面本就不贪心合并。
func (q Quad) Pack() uint64 {
	low, high := uint64(q.W-1)<<shiftW|uint64(q.H-1)<<shiftH, uint64(0)
	if q.Corners != [4]uint8{} {
		if q.W != 1 || q.H != 1 {
			panic("mesh: 带角高度的 quad 必须是 1×1")
		}
		// 每个角只有 4 bit：越界值会串进 bit 16/20/59/63，静默破坏相邻字段。
		// 与 engine quad.rs 的 pack 断言同口径。
		for _, corner := range q.Corners {
			if corner > 15 {
				panic("mesh: 角高度超过 15")
			}
		}
		low = uint64(q.Corners[0])<<shiftW | uint64(q.Corners[1])<<shiftH
		high = uint64(q.Corners[2])<<shiftCorner2 | uint64(q.Corners[3])<<shiftCorner3
	}
	return uint64(q.X)<<shiftX |
		uint64(q.Y)<<shiftY |
		uint64(q.Z)<<shiftZ |
		low |
		uint64(q.Face)<<shiftFace |
		uint64(q.Mat)<<shiftMat |
		uint64(q.AO)<<shiftAO |
		uint64(q.Light)<<shiftLight |
		high
}

// UnpackQuad 是 Pack 的逆运算，也是 Rust 网格化结果进入 Go 的唯一入口，
// 因此它必须无损：任何被丢掉的位都会在重新 Pack 上传时变成数据丢失。
//
// 判别靠角 2（bit 55..58）非零：角 2 在任何面朝向下都是顶面顶点，而真流体高度
// 恒 >= 7，普通 quad 的这 4 bit 则恒为 0，于是不必额外花标志位。
func UnpackQuad(v uint64) Quad {
	q := Quad{
		X:     uint8(v>>shiftX) & 0xF,
		Y:     uint8(v>>shiftY) & 0xF,
		Z:     uint8(v>>shiftZ) & 0xF,
		W:     uint8(v>>shiftW)&0xF + 1,
		H:     uint8(v>>shiftH)&0xF + 1,
		Face:  Face(uint8(v>>shiftFace) & 0x7),
		Mat:   uint16(v >> shiftMat),
		AO:    uint8(v >> shiftAO),
		Light: uint8(v >> shiftLight),
	}
	if corner2 := uint8(v>>shiftCorner2) & 0xF; corner2 != 0 {
		q.W, q.H = 1, 1
		q.Corners = [4]uint8{
			uint8(v>>shiftW) & 0xF,
			uint8(v>>shiftH) & 0xF,
			corner2,
			uint8(v>>shiftCorner3) & 0xF,
		}
	}
	return q
}
