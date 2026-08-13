package hud

import (
	"github.com/channing771/mornlea/internal/assets"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/mesh"
)

const (
	// HUD 图集前两格是代码生成的空心/实心爱心，后续格按 ItemID 放置真实方块顶面。
	hotbarTextureSize       = 16
	hotbarEmptyHeartColumn  = 0
	hotbarFullHeartColumn   = 1
	hotbarBlockColumnOffset = 2
	hotbarTextureColumns    = hotbarBlockColumnOffset + int(core.ItemMossyCobblestone) + 1
	hotbarTextureWidth      = hotbarTextureColumns * hotbarTextureSize
)

func buildHotbarTextureAtlas(registry *assets.Registry) []byte {
	pixels := make([]byte, hotbarTextureWidth*hotbarTextureSize*4)
	paintHotbarHeart(pixels, hotbarEmptyHeartColumn, false)
	paintHotbarHeart(pixels, hotbarFullHeartColumn, true)
	for item := core.ItemStone; item <= core.ItemMossyCobblestone; item++ {
		block, ok := core.ItemPlacement(item)
		if !ok {
			continue
		}
		layer := registry.Material(block, mesh.FacePosY)
		copyHotbarTextureCell(pixels, hotbarBlockColumnOffset+int(item), registry.LayerRGBA(int(layer)))
	}
	return pixels
}
func copyHotbarTextureCell(dst []byte, column int, src []byte) {
	for y := range hotbarTextureSize {
		dstStart := (y*hotbarTextureWidth + column*hotbarTextureSize) * 4
		srcStart := y * hotbarTextureSize * 4
		copy(dst[dstStart:dstStart+hotbarTextureSize*4], src[srcStart:srcStart+hotbarTextureSize*4])
	}
}
func hotbarTextureUV(column int) [4]float32 {
	left := float32(column*hotbarTextureSize) / float32(hotbarTextureWidth)
	right := float32((column+1)*hotbarTextureSize) / float32(hotbarTextureWidth)
	return [4]float32{left, 0, right, 1}
}
func hotbarItemUV(item core.ItemID) ([4]float32, bool) {
	if _, ok := core.ItemPlacement(item); !ok {
		return [4]float32{}, false
	}
	return hotbarTextureUV(hotbarBlockColumnOffset + int(item)), true
}
