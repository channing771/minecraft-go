//go:build darwin

package hud

import (
	"github.com/channing771/mornlea/internal/assets"
	"github.com/channing771/mornlea/internal/render"
)

// FrameStreams 返回 Prepare 之后已编码的 HUD viewport/quad/字形字节
// (只读视图,下一次 Prepare 前有效),供平行 Rust 渲染器帧装配。
func (renderer *HotbarRenderer) FrameStreams() (viewport, quads, glyphs []byte) {
	viewport = renderer.upload[hotbarViewportOffset : hotbarViewportOffset+hotbarViewportBytes]
	quads = renderer.upload[hotbarQuadOffset : hotbarQuadOffset+
		len(renderer.layout.quads)*hotbarInstanceBytes]
	glyphs = renderer.upload[hotbarGlyphOffset : hotbarGlyphOffset+
		len(renderer.layout.glyphs)*hotbarInstanceBytes]
	return viewport, quads, glyphs
}

// AtlasPixels 导出与构建 HUD 贴图相同的像素与尺寸,供平行渲染器上传。
func (renderer *HotbarRenderer) AtlasPixels() (width, height int, pixels []byte) {
	return hotbarTextureWidth, hotbarTextureSize, renderer.hudPixels
}

// NewHotbarLayout 创建 layout-only 的 HUD renderer:只支持 Prepare、
// FrameStreams 与 AtlasPixels,不创建任何 GPU 资源。
func NewHotbarLayout(atlas render.GlyphSource, blocks *assets.Registry) *HotbarRenderer {
	return &HotbarRenderer{
		atlas:  atlas,
		upload: make([]byte, hotbarUploadBytes),
		layout: hotbarLayout{
			quads:  make([]hotbarInstance, 0, maxHotbarQuads),
			glyphs: make([]hotbarInstance, 0, maxHotbarGlyphs),
		},
		hudPixels: buildHotbarTextureAtlas(blocks),
	}
}
