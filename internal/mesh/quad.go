// Package mesh 把区段方块数据转换成 GPU 实例数据。
//
// 本包是纯函数：区段快照进、四边形数组出，不碰 GPU。
package mesh

// Face 是方块的 6 个轴向面之一，或植物的两条交叉斜面之一。
//
// 轴向面的编号规则：axis = Face>>1（0=X,1=Y,2=Z），正方向 = Face&1 == 1。
// 6 与 7 是 3 位 face 字段里此前空闲的两个取值，被交叉斜面占用（见
// FacePlantDiagA / FacePlantDiagB）；它们**没有轴向语义**，对它们调用
// Axis / Positive 的返回值没有意义。着色器依赖这个编码。
type Face uint8

const (
	FaceNegX Face = iota
	FacePosX
	FaceNegY
	FacePosY
	FaceNegZ
	FacePosZ
	// FacePlantDiagA / FacePlantDiagB 是植物的两条交叉斜面。
	//
	// 为什么落在 face 字段而不是新开一位：quad 实例 MUST 保持 8 字节，而 bit 63
	// 是布局里仅存的空闲位、必须留空（水面角高度已经吃掉 55..62）。face 是 3 位
	// 却只用了 0..5，6 与 7 天然空着，正好装下两条对角线，位布局因此零变更。
	//
	//   - FacePlantDiagA：水平方向自格内 (x, z) 走向 (x+1, z+1)；
	//   - FacePlantDiagB：水平方向自格内 (x+1, z) 走向 (x, z+1)。
	//
	// 两条斜面各出正、背两条 quad（正背由 PlantBack 区分），每格恰好 4 条。
	// 出 4 条而不是 2 条 + 关背面剔除，是因为剔除发生在 cull.wgsl 的 compute
	// 阶段、按 quad 法线逐条判定：只出 2 条时任一水平视角都会有一条被判成背面
	// 剔掉，植物就少一片。正背两条法线相反，于是任何视角恰好各留一条。
	FacePlantDiagA
	FacePlantDiagB
)

// Plant 报告该 face 是否是植物的交叉斜面。
func (f Face) Plant() bool { return f >= FacePlantDiagA }

// Axis 返回该面的法线所在轴：0=X, 1=Y, 2=Z。仅对轴向面有意义。
func (f Face) Axis() int { return int(f) >> 1 }

// Positive 返回该面法线是否指向轴的正方向。仅对轴向面有意义。
func (f Face) Positive() bool { return f&1 == 1 }

// PlantMaterialFirst / PlantMaterialLast 是植物材质层的闭区间。
//
// 「一格是不是植物」以 **material 为准**（design D8）：判别不占 quad 位，而 quad
// 位已经只剩 bit 63 一个且必须留空。三处必须一起对齐：
//
//   - Go：本对常量，由 internal/assets 的 LayerWheat0..LayerWheat7 提供数值，
//     两者相等由 assets 的 TestPlantMaterialLayersMatchMeshContract 钉住
//     （assets 依赖 mesh，反向不成立，所以常量住在这里、断言写在那边）；
//   - Rust engine：`greedy.rs` 的 PLANT_MATERIAL_FIRST / PLANT_MATERIAL_LAST，
//     它据此决定哪些格跳过轴向面、改出 4 条交叉斜面；跨语言一致性由真的喂一次
//     Rust mesher 的 TestNativeOracleParityWheatCrossPlanes 兜底；
//   - 着色器：terrain.wgsl 与 cull.wgsl **不**复制这段数值，改按 `face >= 6`
//     判别。二者等价——本文件的解包保证 `face ∈ {6,7} ⟺ material ∈ 植物区间`，
//     而少一份跨语言常量就少一处会静默分叉的地方。
const (
	PlantMaterialFirst uint16 = 31
	PlantMaterialLast  uint16 = 38
)

// PlantMaterial 报告某个材质层是否属于植物集合。
func PlantMaterial(mat uint16) bool {
	return mat >= PlantMaterialFirst && mat <= PlantMaterialLast
}

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
	// Back 只对植物 quad 有意义：同一条对角面的正面记 false、背面记 true。
	// 两者几何完全相同（terrain pass 的 cull_mode 是 None，正背都画），差别只在
	// cull.wgsl 用来做背面剔除的法线方向相反。
	Back bool
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
	// 植物 quad 的正/背面标志位：同样借走恒为 1 的 W/H 那 8 bit，只用最低一位。
	// bit 13..19 是保留位、MUST 为 0——留着给后续植物形态（例如高作物的上下半格）
	// 用，现在任何非零值都是编码错误，打包与解包两侧都当场拒绝。
	shiftPlantBack = shiftW
	// plantReservedMask 覆盖 bit 13..19，即 W/H 那 8 bit 里除正背标志之外的部分。
	plantReservedMask = 0xFF<<shiftW ^ 1<<shiftPlantBack
)

// Pack 把四边形压成 8 字节，供 GPU 实例缓冲直接使用。
//
// 带角高度的水面 quad 借走 W/H 的 8 bit，因此必须是 1×1——水面本就不贪心合并。
// 植物 quad 同理借走同一段位放正背标志，也必须是 1×1——每格独立出面、不合并。
func (q Quad) Pack() uint64 {
	low, high := uint64(q.W-1)<<shiftW|uint64(q.H-1)<<shiftH, uint64(0)
	switch {
	case q.Face.Plant():
		if q.W != 1 || q.H != 1 {
			panic("mesh: 植物 quad 必须是 1×1")
		}
		if q.Corners != [4]uint8{} {
			panic("mesh: 植物 quad 不得带角高度")
		}
		// face 6/7 只允许出现在植物 material 上：着色器与 cull 都据此把顶点摆到
		// 对角面，落在别的 material 上等于把普通方块画成一片穿模的斜板。
		if !PlantMaterial(q.Mat) {
			panic("mesh: face 6/7 只允许出现在植物 material 上")
		}
		low = 0
		if q.Back {
			low = 1 << shiftPlantBack
		}
	case q.Corners != [4]uint8{}:
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
	case q.Back:
		panic("mesh: 非植物 quad 不得设置 Back")
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
// 三条判别互斥、按顺序生效：
//
//  1. face ∈ {6,7} → 植物交叉斜面，W/H 那 8 bit 是正背标志加保留位；
//  2. 角 2（bit 55..58）非零 → 带角高度的水面。角 2 在任何面朝向下都是顶面顶点，
//     而真流体高度恒 >= 7，普通 quad 的这 4 bit 则恒为 0，于是不必额外花标志位；
//  3. 其余 → 普通 quad，W/H 照常解为 w-1 / h-1。
//
// 本函数同时是 native 结果的信任边界：非法编码当场 panic，而不是静默产出一条
// 会被画成穿模斜板或巨型石板的 quad。
func UnpackQuad(v uint64) Quad {
	// bit 63 是 quad 布局里仅存的空闲位，MUST 保持空闲——它一旦被占用，
	// 「quad 实例是 8 字节」就再没有余量，下一个特性只能扩宽实例格式。
	if v>>63 != 0 {
		panic("mesh: quad 占用了必须留空的 bit 63")
	}
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
	if q.Face.Plant() {
		if !PlantMaterial(q.Mat) {
			panic("mesh: face 6/7 只允许出现在植物 material 上")
		}
		if v&plantReservedMask != 0 {
			panic("mesh: 植物 quad 的保留位 13..19 必须为 0")
		}
		q.W, q.H = 1, 1
		q.Back = v>>shiftPlantBack&1 == 1
		return q
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
