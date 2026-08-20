package render

// 水下视觉的两个固定常量。它们只影响呈现，不进入任何权威判定。
const (
	// underwaterVisibleRadius 是相机浸没时的可见 section 搜索半径（区段）。
	//
	// 取 4 是"压低远处可见度"的落点：水下能看到的地形被压到约 4 个区段
	// （64 格）以内，更远的区段本帧根本不进可见列表。这条压制刻意放在 Go 侧的
	// 可见性搜索里而不是着色器里——它不改动任何已上传的 section，也不卸载区块，
	// 出水的那一帧半径立即恢复，没有任何区块抖动。
	underwaterVisibleRadius = 4
	// underwaterTintAlpha 是水色全屏叠加的不透明度。取 0.45：足以让画面明确
	// 读作"在水里"，又低到能看清脚下的地形与手上的准星。
	underwaterTintAlpha = 0.45
)

// underwaterTintRGB 是水色叠加的颜色，与 assets 的水材质同色系（偏青的蓝）。
var underwaterTintRGB = [3]float32{0.12, 0.34, 0.52}

// UnderwaterView 是一帧的水下视觉参数。
//
// 它完全由「相机所在格是不是流体」这一个布尔决定，没有任何可变状态，因此可以
// 逐帧无成本地重算。
type UnderwaterView struct {
	// Tint 是叠加在整幅画面上的水色 RGBA。A 为 0 表示本帧不叠加任何东西，
	// 呈现与本变更之前逐位一致。
	Tint [4]float32
	// VisibleRadius 是本帧可见 section 搜索半径（区段）。
	VisibleRadius int
}

// UnderwaterViewFor 按相机是否浸没算出本帧的水下视觉参数。
//
// eyeInFluid **必须**是调用方从 physics.SubmersionFlags 拿到的那一个眼睛浸没标志
// ——驱动氧气消耗的也是它。规格明确要求水下视觉与溺水判定不得存在第二套独立判定：
// 两处独立镜像同一规则时，「一起写错」不会被任何 parity 断言抓到（差值恒等），
// 这正是本项目历轮评审反复抓到的假绿模式。因此本函数刻意只收一个布尔，
// 不接收位置、不接收方块视图，从签名上就没有另起一套判定的可能。
//
// baseRadius 是相机不在流体时应当使用的可见半径，原样返回。
func UnderwaterViewFor(eyeInFluid bool, baseRadius int) UnderwaterView {
	if !eyeInFluid {
		return UnderwaterView{VisibleRadius: baseRadius}
	}
	return UnderwaterView{
		Tint: [4]float32{
			underwaterTintRGB[0], underwaterTintRGB[1], underwaterTintRGB[2],
			underwaterTintAlpha,
		},
		// 水下半径只压不抬：调用方给的基础半径若本来就更小（例如低配视距），
		// 保持它，绝不因为入水反而让人看得更远。
		VisibleRadius: min(baseRadius, underwaterVisibleRadius),
	}
}
