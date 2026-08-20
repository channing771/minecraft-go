package hud

import "github.com/channing771/mornlea/internal/core"

const (
	// 氧气条：一条背景 + 一条按权威比例填充的前景，共两个 quad。
	oxygenQuads = 2
	// 氧气条与生命条等宽（十颗爱心加九条间隙），因此两者左右边沿严格对齐。
	oxygenBarWidth  = healthSegmentCount*healthHeartSize + (healthSegmentCount-1)*healthHeartGap
	oxygenBarHeight = float32(6)
	// 氧气条下沿到生命条上沿的间隔。
	oxygenBarGap = float32(4)
)

// OxygenOverlay 是服务端已确认的氧气。它是 render 本地值，由 app 从 Predictor 的
// 已确认镜像转换；Confirmed 为 false 时表示尚未收到权威状态，渲染器不画任何氧气
// 元素——氧气是权威值，客户端绝不显示预测或陈旧的数值。
type OxygenOverlay struct {
	Confirmed bool
	Value     uint16
}

// appendOxygenBar 在生命条正上方绘制氧气条。
//
// 三条契约（spec fluid-presentation-survival 的「氧气同步到客户端并在耗损时可见」）：
//
//   - **仅在未满时出现**：满氧（含未确认）时一个 quad 都不追加，界面完全不被占用。
//     这不是"画一条满格的条"，两者在 quad 数上可区分。
//   - **复用既有绘制阶段**：quad 追加进同一份 hotbarLayout，与快捷栏、生命条、
//     采掘条走同一个 HUD pass 与同一份实例缓冲，没有第二条管线、第二个图集或
//     第二块上传缓冲。它与 appendMiningBar 同形，是纯色 quad，因此连图集列都不占。
//   - **呈现随权威值变化**：填充宽度与 oxygen/MaxOxygenTicks 成正比，不同的权威值
//     给出不同的填充宽度。
func appendOxygenBar(dst *hotbarLayout, oxygen OxygenOverlay, width, height float32) {
	if !oxygen.Confirmed || width <= 0 || height <= 0 {
		return
	}
	value := min(oxygen.Value, core.MaxOxygenTicks)
	if value >= core.MaxOxygenTicks {
		return
	}
	// 与生命条共用一次 hudScale(false, …)：打开背包不改变它的尺度或位置。
	scale := hudScale(false, width, height)
	x := hudEdgeMargin * scale
	healthTop := height - (hudEdgeMargin+healthHeartSize)*scale
	y := healthTop - (oxygenBarGap+oxygenBarHeight)*scale
	barWidth := oxygenBarWidth * scale
	barHeight := oxygenBarHeight * scale
	dst.quads = append(dst.quads, hotbarInstance{
		X: x, Y: y, Width: barWidth, Height: barHeight,
		Color: [4]float32{0.05, 0.07, 0.12, 0.78},
	})
	fraction := float32(value) / float32(core.MaxOxygenTicks)
	dst.quads = append(dst.quads, hotbarInstance{
		X: x, Y: y, Width: barWidth * fraction, Height: barHeight,
		Color: [4]float32{0.42, 0.78, 1, 0.95},
	})
}
