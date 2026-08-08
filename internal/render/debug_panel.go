package render

import (
	_ "embed"
	"encoding/binary"
	"fmt"
	"math"
	"strconv"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/gfx"
)

const (
	// maxPanelRows 是参数行的固定上限。config.Fields() 目前约 20 项，
	// 留出余量到 64；超出部分不绘制，测试守住这条上限。
	maxPanelRows = 64
	// maxPanelReadoutRows 是顶部读数行数：帧时、坐标、朝向、tick、时刻、区块数、模式。
	maxPanelReadoutRows = 7
	// 每行最多的字形数：标签与数值各截断到 maxPanelRunesPerSide。
	maxPanelRunesPerSide = 24

	maxPanelQuads  = 1 + maxPanelRows // 面板背景 + 每行至多一个选中高亮
	maxPanelGlyphs = (maxPanelRows + maxPanelReadoutRows) * maxPanelRunesPerSide * 2

	panelInstanceBytes  = 48
	panelViewportOffset = 0
	panelViewportBytes  = 16
	panelQuadOffset     = 256
	panelQuadSize       = maxPanelQuads * panelInstanceBytes
	panelGlyphOffset    = (panelQuadOffset + panelQuadSize + 255) &^ 255
	panelGlyphSize      = maxPanelGlyphs * panelInstanceBytes
	panelUploadBytes    = panelGlyphOffset + panelGlyphSize

	panelMarginX        = float32(16)
	panelMarginY        = float32(16)
	panelPaddingX       = float32(10)
	panelPaddingY       = float32(8)
	panelRowHeight      = float32(18)
	panelLabelWidth     = float32(260)
	panelWidth          = float32(460)
	panelSectionGap     = float32(6)
	panelHighlightInset = float32(2)
)

//go:embed shader/debug_panel.wgsl
var debugPanelShader string

// PanelRow 是调试面板中一条参数行：标签、当前值，以及是否只读、是否被选中。
// 按键交互（移动选中项、编辑数值）属于后续任务，本渲染器只按传入状态绘制。
type PanelRow struct {
	Label, Value       string
	ReadOnly, Selected bool
}

// PanelReadout 是面板顶部固定的只读状态读数：一帧耗时、玩家位置与朝向、
// 权威 tick、世界时刻、已加载区块数与当前模式名。
type PanelReadout struct {
	FrameMillis  float64
	Position     mgl32.Vec3
	Yaw, Pitch   float32
	Tick         uint64
	WorldTime    uint64
	LoadedChunks int
	Mode         string
}

type panelInstance struct {
	X, Y, Width, Height float32
	U0, V0, U1, V1      float32
	Color               [4]float32
}

type panelLayout struct {
	quads  []panelInstance
	glyphs []panelInstance
}

// DebugPanelRenderer 以固定容量绘制屏幕空间调试面板：顶部只读读数区，
// 下方最多 maxPanelRows 行参数。它只消费调用方已经准备好的数据，不做任何
// 按键或指针输入处理。
type DebugPanelRenderer struct {
	atlas GlyphSource

	dynamic       gfx.Buffer
	quadPipeline  gfx.RenderPipeline
	glyphPipeline gfx.RenderPipeline
	bind          gfx.BindGroup
	sampler       gfx.Sampler

	layout panelLayout
	upload []byte
}

func NewDebugPanelRenderer(
	dev gfx.Device,
	colorFormat gfx.TextureFormat,
	atlas GlyphSource,
) *DebugPanelRenderer {
	renderer := &DebugPanelRenderer{
		atlas:  atlas,
		upload: make([]byte, panelUploadBytes),
		layout: panelLayout{
			quads:  make([]panelInstance, 0, maxPanelQuads),
			glyphs: make([]panelInstance, 0, maxPanelGlyphs),
		},
	}
	renderer.dynamic = dev.CreateBuffer(gfx.BufferDesc{
		Label: "debug panel dynamic upload",
		Size:  panelUploadBytes,
		Usage: gfx.BufferUsageUniform | gfx.BufferUsageStorage | gfx.BufferUsageCopyDst,
	})
	layout := gfx.BindGroupLayout{
		Label: "debug panel layout",
		Entries: []gfx.BindGroupLayoutEntry{
			{Binding: 0, Type: gfx.BindingUniformBuffer, VisibleIn: gfx.StageVertex},
			{Binding: 1, Type: gfx.BindingStorageBufferRO, VisibleIn: gfx.StageVertex},
			{Binding: 2, Type: gfx.BindingStorageBufferRO, VisibleIn: gfx.StageVertex},
			{
				Binding: 3, Type: gfx.BindingSampledTextureFloat,
				VisibleIn: gfx.StageFragment, ViewDimension: gfx.TextureViewDimension2D,
			},
			{Binding: 4, Type: gfx.BindingSampler, VisibleIn: gfx.StageFragment},
		},
	}
	module := dev.CreateShaderModule(debugPanelShader)
	renderer.quadPipeline = dev.CreateRenderPipeline(panelPipelineDesc(
		"debug panel quad", module, colorFormat, layout, "quad_vs", "quad_fs",
	))
	renderer.glyphPipeline = dev.CreateRenderPipeline(panelPipelineDesc(
		"debug panel glyph", module, colorFormat, layout, "glyph_vs", "glyph_fs",
	))
	module.Release()
	renderer.sampler = dev.CreateSampler(gfx.SamplerDesc{
		Label: "debug panel glyph sampler", MagFilter: gfx.FilterLinear, MinFilter: gfx.FilterLinear,
		MipFilter: gfx.FilterNearest, Address: gfx.AddressClampToEdge,
	})
	renderer.bind = dev.CreateBindGroup(gfx.BindGroupDesc{
		Label:  "debug panel resources",
		Layout: layout,
		Entries: []gfx.BindGroupEntry{
			{
				Binding: 0, Buffer: renderer.dynamic,
				Offset: panelViewportOffset, Size: panelViewportBytes,
			},
			{
				Binding: 1, Buffer: renderer.dynamic,
				Offset: panelQuadOffset, Size: panelQuadSize,
			},
			{
				Binding: 2, Buffer: renderer.dynamic,
				Offset: panelGlyphOffset, Size: panelGlyphSize,
			},
			{Binding: 3, Texture: atlas.TextureView()},
			{Binding: 4, Sampler: renderer.sampler},
		},
	})
	return renderer
}

func panelPipelineDesc(
	label string,
	module gfx.ShaderModule,
	colorFormat gfx.TextureFormat,
	layout gfx.BindGroupLayout,
	vertexEntry, fragmentEntry string,
) gfx.RenderPipelineDesc {
	return gfx.RenderPipelineDesc{
		Label: label, Shader: module,
		VertexEntry: vertexEntry, FragmentEntry: fragmentEntry,
		BindGroups:  []gfx.BindGroupLayout{layout},
		ColorFormat: colorFormat,
		Blend:       gfx.BlendAlpha,
	}
}

// Prepare 按可见性、只读读数与参数行重建固定布局。visible 为 false 时
// 只清空布局并立即返回：不请求字形、不做任何 GPU 或几何计算，这是面板
// 关闭时的零开销路径。rows 超过 maxPanelRows 的部分不绘制。
func (renderer *DebugPanelRenderer) Prepare(
	visible bool,
	readout PanelReadout,
	rows []PanelRow,
	width, height uint32,
	budget *UploadBudget,
) error {
	renderer.layout.quads = renderer.layout.quads[:0]
	renderer.layout.glyphs = renderer.layout.glyphs[:0]
	if !visible {
		return nil
	}
	if len(rows) > maxPanelRows {
		rows = rows[:maxPanelRows]
	}
	readoutRows := panelReadoutRows(readout)

	for _, row := range readoutRows {
		renderer.atlas.Request(truncatePanelText(row.Label))
		renderer.atlas.Request(truncatePanelText(row.Value))
	}
	for _, row := range rows {
		renderer.atlas.Request(truncatePanelText(row.Label))
		renderer.atlas.Request(truncatePanelText(row.Value))
	}
	if err := renderer.atlas.FlushUploads(budget); err != nil {
		return err
	}

	layoutPanel(&renderer.layout, renderer.atlas, readoutRows[:], rows, float32(width), float32(height))

	encodePanelViewport(
		renderer.upload[panelViewportOffset:panelViewportOffset+panelViewportBytes],
		float32(width), float32(height),
	)
	encodePanelInstances(
		renderer.upload[panelQuadOffset:panelQuadOffset+panelQuadSize],
		renderer.layout.quads,
	)
	encodePanelInstances(
		renderer.upload[panelGlyphOffset:panelUploadBytes],
		renderer.layout.glyphs,
	)
	return nil
}

// Render 在 HUD 与 name tag 之后以屏幕空间透明 pass 绘制调试面板。
func (renderer *DebugPanelRenderer) Render(encoder gfx.CommandEncoder, target gfx.TextureView) {
	if len(renderer.layout.quads) == 0 {
		return
	}
	uploadBytes := panelGlyphOffset + len(renderer.layout.glyphs)*panelInstanceBytes
	renderer.dynamic.Write(0, renderer.upload[:uploadBytes])
	pass := encoder.BeginRenderPass(gfx.RenderPassDesc{
		Label: "debug panel pass", ColorView: target, LoadClear: false,
	})
	pass.SetBindGroup(0, renderer.bind)
	pass.SetPipeline(renderer.quadPipeline)
	pass.Draw(6, uint32(len(renderer.layout.quads)))
	if len(renderer.layout.glyphs) != 0 {
		pass.SetPipeline(renderer.glyphPipeline)
		pass.Draw(6, uint32(len(renderer.layout.glyphs)))
	}
	pass.End()
}

// panelReadoutRows 把权威读数格式化成固定 7 行只读文本：帧时、坐标、朝向、
// tick、时刻、区块数、模式。数量与顺序由 maxPanelReadoutRows 固定。
func panelReadoutRows(readout PanelReadout) [maxPanelReadoutRows]PanelRow {
	return [maxPanelReadoutRows]PanelRow{
		{Label: "帧时", Value: fmt.Sprintf("%.2f ms", readout.FrameMillis), ReadOnly: true},
		{Label: "坐标", Value: fmt.Sprintf("%.1f, %.1f, %.1f",
			readout.Position[0], readout.Position[1], readout.Position[2]), ReadOnly: true},
		{Label: "朝向", Value: fmt.Sprintf("yaw %.1f pitch %.1f", readout.Yaw, readout.Pitch), ReadOnly: true},
		{Label: "Tick", Value: strconv.FormatUint(readout.Tick, 10), ReadOnly: true},
		{Label: "时刻", Value: strconv.FormatUint(readout.WorldTime, 10), ReadOnly: true},
		{Label: "区块数", Value: strconv.Itoa(readout.LoadedChunks), ReadOnly: true},
		{Label: "模式", Value: readout.Mode, ReadOnly: true},
	}
}

// layoutPanel 只依赖 framebuffer 尺寸、只读读数与参数行，产出固定上限的
// 实例：背景 1 个 + 每个被选中行 1 个高亮，加上每行标签与数值各自的字形。
// readoutRows 恒为只读，因此从不产生高亮，只有 rows 中标记 Selected 的行
// 才会追加高亮矩形，故整体矩形数不超过 maxPanelQuads。
func layoutPanel(
	dst *panelLayout,
	atlas GlyphSource,
	readoutRows []PanelRow,
	rows []PanelRow,
	width, height float32,
) panelLayout {
	if dst == nil {
		dst = &panelLayout{
			quads:  make([]panelInstance, 0, maxPanelQuads),
			glyphs: make([]panelInstance, 0, maxPanelGlyphs),
		}
	}
	dst.quads = dst.quads[:0]
	dst.glyphs = dst.glyphs[:0]
	if width <= 0 || height <= 0 {
		return *dst
	}

	totalRows := len(readoutRows) + len(rows)
	panelHeight := panelPaddingY*2 + float32(totalRows)*panelRowHeight
	if len(rows) > 0 {
		panelHeight += panelSectionGap
	}
	dst.quads = append(dst.quads, panelInstance{
		X: panelMarginX, Y: panelMarginY,
		Width: panelWidth, Height: panelHeight,
		Color: [4]float32{0.04, 0.04, 0.05, 0.82},
	})

	y := panelMarginY + panelPaddingY
	for _, row := range readoutRows {
		appendPanelRow(dst, atlas, row, y)
		y += panelRowHeight
	}
	if len(rows) > 0 {
		y += panelSectionGap
	}
	for _, row := range rows {
		appendPanelRow(dst, atlas, row, y)
		y += panelRowHeight
	}
	return *dst
}

// appendPanelRow 在给定纵坐标绘制一行标签与数值：只读行使用暗色，选中行
// 额外绘制一个高亮背景矩形。调用方保证不超过固定容量。
func appendPanelRow(dst *panelLayout, atlas GlyphSource, row PanelRow, y float32) {
	if row.Selected {
		dst.quads = append(dst.quads, panelInstance{
			X: panelMarginX + panelHighlightInset, Y: y - panelHighlightInset,
			Width:  panelWidth - 2*panelHighlightInset,
			Height: panelRowHeight,
			Color:  [4]float32{0.30, 0.62, 0.95, 0.35},
		})
	}
	color := panelTextColor(row.ReadOnly)
	labelX := panelMarginX + panelPaddingX
	valueX := panelMarginX + panelLabelWidth
	appendPanelText(dst, atlas, truncatePanelText(row.Label), labelX, y, color)
	appendPanelText(dst, atlas, truncatePanelText(row.Value), valueX, y, color)
}

// panelTextColor 只读行用暗色绘制，便于一眼区分不可编辑的参数。
func panelTextColor(readOnly bool) [4]float32 {
	if readOnly {
		return [4]float32{0.5, 0.5, 0.5, 1}
	}
	return [4]float32{1, 1, 1, 1}
}

// appendPanelText 从 (x, y) 起沿基线绘制一行文本的字形。text 必须已经按
// rune 截断到 maxPanelRunesPerSide 以内，本函数不再做容量检查。
func appendPanelText(dst *panelLayout, atlas GlyphSource, text string, x, y float32, color [4]float32) {
	penX := x
	baseline := y + panelRowHeight - panelPaddingY*0.5
	for _, char := range text {
		glyph := atlas.Glyph(char)
		dst.glyphs = append(dst.glyphs, panelInstance{
			X:      penX + glyph.BearingX,
			Y:      baseline - glyph.BearingY,
			Width:  glyph.Width,
			Height: glyph.Height,
			U0:     glyph.U0, V0: glyph.V0, U1: glyph.U1, V1: glyph.V1,
			Color: color,
		})
		penX += glyph.Advance
	}
}

// truncatePanelText 按 rune（而非字节）把文本截断到 maxPanelRunesPerSide
// 个字符以内。标签多为中文，按字节截断会切坏多字节字符，因此必须逐 rune 计数。
func truncatePanelText(text string) string {
	runes := 0
	for index := range text {
		if runes == maxPanelRunesPerSide {
			return text[:index]
		}
		runes++
	}
	return text
}

func encodePanelViewport(dst []byte, width, height float32) []byte {
	out := dst[:panelViewportBytes]
	for index, value := range [4]float32{width, height, 0, 0} {
		binary.LittleEndian.PutUint32(out[index*4:], math.Float32bits(value))
	}
	return out
}

func encodePanelInstances(dst []byte, instances []panelInstance) []byte {
	out := dst[:len(instances)*panelInstanceBytes]
	for index, instance := range instances {
		values := [12]float32{
			instance.X, instance.Y, instance.Width, instance.Height,
			instance.U0, instance.V0, instance.U1, instance.V1,
			instance.Color[0], instance.Color[1], instance.Color[2], instance.Color[3],
		}
		base := index * panelInstanceBytes
		for offset, value := range values {
			binary.LittleEndian.PutUint32(out[base+offset*4:], math.Float32bits(value))
		}
	}
	return out
}

// QuadCount 返回当前布局中的矩形实例数；仅供测试断言布局用。
func (renderer *DebugPanelRenderer) QuadCount() int { return len(renderer.layout.quads) }

// GlyphCount 返回当前布局中的字形实例数；仅供测试断言布局用。
func (renderer *DebugPanelRenderer) GlyphCount() int { return len(renderer.layout.glyphs) }

// hasDimmedGlyph 报告布局中是否存在暗色字形（只读行使用的绘制颜色）；
// 仅供测试断言用。
func (renderer *DebugPanelRenderer) hasDimmedGlyph() bool {
	dim := panelTextColor(true)
	for _, glyph := range renderer.layout.glyphs {
		if glyph.Color == dim {
			return true
		}
	}
	return false
}

// Release 只释放渲染器自有句柄；字形 atlas 与其视图由应用持有。
func (renderer *DebugPanelRenderer) Release() {
	if renderer.bind != nil {
		renderer.bind.Release()
		renderer.bind = nil
	}
	if renderer.glyphPipeline != nil {
		renderer.glyphPipeline.Release()
		renderer.glyphPipeline = nil
	}
	if renderer.quadPipeline != nil {
		renderer.quadPipeline.Release()
		renderer.quadPipeline = nil
	}
	if renderer.sampler != nil {
		renderer.sampler.Release()
		renderer.sampler = nil
	}
	if renderer.dynamic != nil {
		renderer.dynamic.Release()
		renderer.dynamic = nil
	}
}
