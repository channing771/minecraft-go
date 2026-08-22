package hud

import (
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/render"
)

// HotbarRenderer 以固定容量绘制 9 格快捷栏 HUD。
// 它只消费已确认的权威快捷栏值，不做任何本地预测。
type HotbarRenderer struct {
	atlas render.GlyphSource

	layout hotbarLayout
	upload []byte
	// hudPixels 保留构建贴图时的像素,供 AtlasPixels 导出同一份内容。
	hudPixels []byte
}

func (renderer *HotbarRenderer) Prepare(
	inventory core.Inventory,
	inventoryConfirmed bool,
	open bool,
	source int,
	overlay *FurnaceOverlay,
	chest *ChestOverlay,
	mining MiningOverlay,
	health HealthOverlay,
	oxygen OxygenOverlay,
	hunger HungerOverlay,
	chat ChatOverlay,
	width, height uint32,
	budget *render.UploadBudget,
) error {
	textRequested := false
	if inventoryConfirmed {
		renderer.atlas.Request(hotbarDigits)
		textRequested = true
	}
	textRequested = requestChatText(renderer.atlas, chat) || textRequested
	if textRequested {
		if err := renderer.atlas.FlushUploads(budget); err != nil {
			return err
		}
	}
	if inventoryConfirmed {
		layoutInventory(
			&renderer.layout, renderer.atlas, inventory, open, source, overlay, chest, mining,
			float32(width), float32(height),
		)
	} else {
		renderer.layout.quads = renderer.layout.quads[:0]
		renderer.layout.glyphs = renderer.layout.glyphs[:0]
		renderer.layout.scale = hudScale(open, float32(width), float32(height))
		renderer.layout.open = open
	}
	appendHealthBar(&renderer.layout, renderer.atlas, health, float32(width), float32(height))
	appendOxygenBar(&renderer.layout, oxygen, float32(width), float32(height))
	appendHungerBar(&renderer.layout, hunger, float32(width), float32(height))
	appendChatOverlay(&renderer.layout, renderer.atlas, chat, float32(width), float32(height))
	encodeHotbarViewport(
		renderer.upload[hotbarViewportOffset:hotbarViewportOffset+hotbarViewportBytes],
		float32(width), float32(height),
	)
	encodeHotbarInstances(
		renderer.upload[hotbarQuadOffset:hotbarQuadOffset+hotbarQuadSize],
		renderer.layout.quads,
	)
	encodeHotbarInstances(
		renderer.upload[hotbarGlyphOffset:hotbarUploadBytes],
		renderer.layout.glyphs,
	)
	return nil
}
