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
	// 列数按合法物品编号的独占上界 ItemIDMax 预留：追加新物品时图集自动扩出
	// 空白列，与 core 枚举末项守护（item_test.go）保持同一穷举界，不再依赖
	// 「某个具体物品恰为枚举末项」的脆弱假设。
	hotbarTextureColumns = hotbarBlockColumnOffset + int(core.ItemIDMax)
	hotbarTextureWidth   = hotbarTextureColumns * hotbarTextureSize
)

func buildHotbarTextureAtlas(registry *assets.Registry) []byte {
	pixels := make([]byte, hotbarTextureWidth*hotbarTextureSize*4)
	paintHotbarHeart(pixels, hotbarEmptyHeartColumn, false)
	paintHotbarHeart(pixels, hotbarFullHeartColumn, true)
	// 物品列按 ItemID 穷举到独占上界 ItemIDMax（与列数预留同一穷举界），
	// 不可放置的物品没有对应列内容，保持空白。
	for item := core.ItemStone; item < core.ItemIDMax; item++ {
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
