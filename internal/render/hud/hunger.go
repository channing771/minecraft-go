package hud

import "github.com/channing771/mornlea/internal/core"

// hungerQuads 是饥饿条在固定上传布局里占的 quad 数：十格常驻空鸡腿加最多十格填充。
//
// 刻意复用 `healthSegmentCount`（以及下方几何用的 `healthHeartSize`、
// `healthHeartGap`）而不是另写一套同值常量：饥饿条是生命条的右下镜像，两条 bar
// 的格数与格尺寸必须严格相等，各写一份迟早会有一侧被改动而另一侧漂移。
const hungerQuads = healthSegmentCount * 2

// HungerOverlay 是服务端已确认的饥饿值。它是 render 本地值，由 app 从
// Predictor 的已确认镜像转换；Confirmed 为 false 时表示尚未收到权威状态，
// 渲染器不画任何饥饿元素——饥饿是权威值，客户端绝不显示预测或陈旧的数值。
type HungerOverlay struct {
	Confirmed bool
	Value     uint8
}

// appendHungerBar 在 framebuffer 右下角绘制一排鸡腿，与左下的生命条严格镜像。
//
// 三条契约：
//
//   - **满时仍然显示**：饥饿是常态资源，条本身永远在，玩家靠它读「还剩多少」。
//     这与氧气条「未满才出现」相反——氧气是异常态，只在水下才该占用界面。
//     因此这里没有「满值提前返回」那一句，写在这里是为了防止后来者照氧气条
//     补一句「优化」。
//   - **复用既有绘制阶段**：quad 追加进同一份 `hotbarLayout`，与快捷栏、生命条、
//     氧气条走同一个 HUD pass、同一份实例缓冲、同一张 HUD 图集（鸡腿只是新占
//     两列），没有第二条管线。
//   - **半格粒度**：每格两点，奇数饥饿值末格画半个。因为整条是右下镜像、填充
//     从右向左推进，半格露出的是鸡腿的**右**半边（U0 取中点、X 右移半格），
//     与左下生命条的半颗爱心（露左半边）恰好对称。
func appendHungerBar(dst *hotbarLayout, hunger HungerOverlay, width, height float32) {
	if !hunger.Confirmed || width <= 0 || height <= 0 {
		return
	}
	// 与生命条共用一次 hudScale(false, …)：打开背包不改变它的尺度或位置。
	scale := hudScale(false, width, height)
	size := healthHeartSize * scale
	gap := healthHeartGap * scale
	right := width - hudEdgeMargin*scale
	y := height - (hudEdgeMargin+healthHeartSize)*scale
	// segmentX 返回自右向左第 segment 格的左边沿。
	segmentX := func(segment int) float32 {
		return right - float32(segment+1)*size - float32(segment)*gap
	}
	emptyUV := hotbarTextureUV(hotbarEmptyDrumstickColumn)
	for segment := range healthSegmentCount {
		dst.quads = append(dst.quads, hotbarInstance{
			X: segmentX(segment), Y: y,
			Width: size, Height: size,
			U0: emptyUV[0], V0: emptyUV[1], U1: emptyUV[2], V1: emptyUV[3],
			Color: [4]float32{1, 1, 1, 1},
		})
	}
	value := min(hunger.Value, core.MaxHunger)
	filled := (int(value) + 1) / 2
	fullUV := hotbarTextureUV(hotbarFullDrumstickColumn)
	for segment := range filled {
		x := segmentX(segment)
		fillWidth := size
		fillU0 := fullUV[0]
		if segment == filled-1 && value%2 != 0 {
			fillWidth *= 0.5
			fillU0 = (fullUV[0] + fullUV[2]) * 0.5
			x += size * 0.5
		}
		dst.quads = append(dst.quads, hotbarInstance{
			X: x, Y: y,
			Width: fillWidth, Height: size,
			U0: fillU0, V0: fullUV[1], U1: fullUV[2], V1: fullUV[3],
			Color: [4]float32{1, 1, 1, 1},
		})
	}
}

// paintHotbarDrumstick 把一格鸡腿画进 HUD 图集的指定列，full 区分满格与空槽。
//
// 与 `paintHotbarHeart` 同处同法：程序化生成、不进 `internal/assets`。HUD 图集
// 不在材质包的覆盖范围内（材质包动的是方块 layer），把 HUD 图标放这里既避开了
// 与它的冲突，也沿用了爱心已经跑了几个里程碑的先例。
//
// 空槽画成同一轮廓的深色剪影而不是留白，这样十格底一直勾勒出满值刻度，玩家
// 一眼能读出「还差几格」。
func paintHotbarDrumstick(dst []byte, column int, full bool) {
	for y := range hotbarTextureSize {
		for x := range hotbarTextureSize {
			if !hotbarDrumstickPixel(x, y) {
				continue
			}
			border := !hotbarDrumstickPixel(x-1, y) || !hotbarDrumstickPixel(x+1, y) ||
				!hotbarDrumstickPixel(x, y-1) || !hotbarDrumstickPixel(x, y+1)
			// 空槽：深色剪影，边缘稍亮一档勾出轮廓。
			color := [4]byte{30, 22, 16, 255}
			if border {
				color = [4]byte{74, 52, 38, 255}
			}
			switch {
			case !full:
			case border:
				color = [4]byte{92, 48, 22, 255}
			case hotbarDrumstickBonePixel(x, y):
				color = [4]byte{240, 228, 196, 255}
			default:
				color = [4]byte{176, 96, 46, 255}
				// 左上一小片高光，让肉的体积在 16px 下也读得出来。
				if x <= 8 && y >= 7 && y <= 9 {
					color = [4]byte{214, 138, 74, 255}
				}
			}
			offset := (y*hotbarTextureWidth + column*hotbarTextureSize + x) * 4
			copy(dst[offset:offset+4], color[:])
		}
	}
}

// hotbarDrumstickPixel 报告鸡腿轮廓（骨头或肉）是否覆盖该像素。
// 空槽与满格共用同一轮廓，两者只差配色，因此填充半格时左右两半严丝合缝。
func hotbarDrumstickPixel(x, y int) bool {
	return hotbarDrumstickBonePixel(x, y) || hotbarDrumstickMeatPixel(x, y)
}

// hotbarDrumstickBonePixel 是左上角斜向的骨柄：一条 ±1 像素宽的对角线加一个圆骨节。
func hotbarDrumstickBonePixel(x, y int) bool {
	if x < 1 || y < 1 || x > 8 || y > 8 {
		return false
	}
	if diff := x - y; diff >= -1 && diff <= 1 {
		return true
	}
	dx, dy := x-2, y-2
	return dx*dx+dy*dy <= 2
}

// hotbarDrumstickMeatPixel 是右下角的肉：以 (9,9) 为心、半径约 5.5 的整数圆盘。
func hotbarDrumstickMeatPixel(x, y int) bool {
	dx, dy := x-9, y-9
	return dx*dx+dy*dy <= 30
}
