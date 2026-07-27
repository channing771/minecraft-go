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
)

// Pack 把四边形压成 8 字节，供 GPU 实例缓冲直接使用。
func (q Quad) Pack() uint64 {
	return uint64(q.X)<<shiftX |
		uint64(q.Y)<<shiftY |
		uint64(q.Z)<<shiftZ |
		uint64(q.W-1)<<shiftW |
		uint64(q.H-1)<<shiftH |
		uint64(q.Face)<<shiftFace |
		uint64(q.Mat)<<shiftMat |
		uint64(q.AO)<<shiftAO |
		uint64(q.Light)<<shiftLight
}

// UnpackQuad 是 Pack 的逆运算，仅用于测试与调试。
func UnpackQuad(v uint64) Quad {
	return Quad{
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
}
