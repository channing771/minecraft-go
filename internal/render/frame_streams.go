//go:build darwin

package render

// 本文件为 rust-client-render-entities 提供最小导出面:把各 pass 已有的
// CPU 编码结果(80B avatar 实例、64B 名牌实例、48B 面板实例等)以只读
// 字节流暴露给平行 Rust 渲染器的帧装配。所有函数只是既有内部逻辑的
// 复用出口,不改变任何渲染行为。

// EncodeAvatarInstances 把插值后的 avatars 编码为 80 字节/实例的字节流,
// 与 AvatarRenderer.Render 的内部编码逐字节一致。dst 会被重置复用。
func EncodeAvatarInstances(dst []byte, avatars []Avatar) []byte {
	ordered := orderedAvatarsInto(nil, avatars)
	parts := buildOrderedAvatarParts(nil, ordered)
	dst = growEncodeBuffer(dst, len(parts)*avatarInstanceBytes)
	encodeAvatarPartsInto(dst, parts)
	return dst
}

// EncodeItemDropInstances 把掉落物编码为 80 字节/实例的字节流,
// 与 ItemDropRenderer.Render 的内部编码逐字节一致。
func EncodeItemDropInstances(dst []byte, serverTick uint64, drops []ItemDrop) []byte {
	parts := buildItemDropParts(nil, serverTick, drops)
	dst = growEncodeBuffer(dst, len(parts)*avatarInstanceBytes)
	encodeAvatarPartsInto(dst, parts)
	return dst
}

// EncodeBlockOutlineInstances 把目标方块轮廓编码为 12×80 字节实例流;
// 不可见时返回空。
func EncodeBlockOutlineInstances(dst []byte, outline BlockOutline) []byte {
	if !outline.Visible {
		return dst[:0]
	}
	parts := buildBlockOutlineParts(nil, outline.Position)
	dst = growEncodeBuffer(dst, len(parts)*avatarInstanceBytes)
	encodeAvatarPartsInto(dst, parts)
	return dst
}

// EncodeBillboardCameraBytes 导出名牌 billboard 相机的 96 字节编码。
func EncodeBillboardCameraBytes(dst []byte, camera BillboardCamera) []byte {
	dst = growEncodeBuffer(dst, nameTagCameraBytes)
	encodeBillboardCamera(dst, camera)
	return dst
}

// FrameStreams 返回 Prepare 之后已编码的名牌背景与字形实例字节
// (只读视图,下一次 Prepare 前有效)。
func (renderer *NameTagRenderer) FrameStreams() (backgrounds, glyphs []byte) {
	backgrounds = renderer.upload[nameTagBackgroundOffset : nameTagBackgroundOffset+
		len(renderer.layout.backgrounds)*nameTagInstanceBytes]
	glyphs = renderer.upload[nameTagGlyphOffset : nameTagGlyphOffset+
		len(renderer.layout.glyphs)*nameTagInstanceBytes]
	return backgrounds, glyphs
}

// FrameStreams 返回 Prepare 之后已编码的面板 viewport/quad/字形字节。
func (renderer *DebugPanelRenderer) FrameStreams() (viewport, quads, glyphs []byte) {
	viewport = renderer.upload[panelViewportOffset : panelViewportOffset+panelViewportBytes]
	quads = renderer.upload[panelQuadOffset : panelQuadOffset+
		len(renderer.layout.quads)*panelInstanceBytes]
	glyphs = renderer.upload[panelGlyphOffset : panelGlyphOffset+
		len(renderer.layout.glyphs)*panelInstanceBytes]
	return viewport, quads, glyphs
}

// AtlasPixels 回读整张字形图集(R8,1024×1024),供平行渲染器同步同一份
// 字形内容。仅测试与装配路径使用。
func (atlas *GlyphAtlas) AtlasPixels() []byte {
	return atlas.texture.ReadLayer(0, 0)
}

// GlyphAtlasSize 导出字形图集边长,供装配方校验。
const GlyphAtlasSize = glyphAtlasSize

func growEncodeBuffer(dst []byte, size int) []byte {
	if cap(dst) < size {
		return make([]byte, size)
	}
	return dst[:size]
}

// NewNameTagLayouter 创建 layout-only 的名牌 renderer:只支持 Prepare 与
// FrameStreams,不创建任何 GPU 资源(生产切换后由 Rust 渲染器绘制)。
func NewNameTagLayouter(atlas GlyphSource) *NameTagRenderer {
	return &NameTagRenderer{
		atlas:   atlas,
		ordered: make([]NameTag, 0, maxNameTags),
		upload:  make([]byte, nameTagUploadBytes),
		layout: nameTagLayout{
			glyphs:      make([]nameTagGlyph, 0, maxNameTagGlyphs),
			backgrounds: make([]nameTagBackground, 0, maxNameTags),
		},
	}
}

// NewDebugPanelLayouter 创建 layout-only 的调试面板 renderer,同上。
func NewDebugPanelLayouter(atlas GlyphSource) *DebugPanelRenderer {
	return &DebugPanelRenderer{
		atlas:  atlas,
		upload: make([]byte, panelUploadBytes),
		layout: panelLayout{
			quads:  make([]panelInstance, 0, maxPanelQuads),
			glyphs: make([]panelInstance, 0, maxPanelGlyphs),
		},
	}
}
