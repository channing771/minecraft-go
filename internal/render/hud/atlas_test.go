package hud

import (
	"bytes"
	"testing"

	"github.com/channing771/mornlea/internal/assets"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/mesh"
	"github.com/channing771/mornlea/internal/render"
)

// 杀死变异：重新用近似色块或复制错误方块面的像素，都无法通过逐像素来源核对。
func TestHotbarTextureAtlasCopiesRegisteredBlockTopFaces(t *testing.T) {
	registry := assets.NewRegistry()
	pixels := buildHotbarTextureAtlas(registry)
	for _, item := range []core.ItemID{
		core.ItemStone, core.ItemDirt, core.ItemGrass, core.ItemStoneBrick,
		core.ItemFurnace, core.ItemIronBlock, core.ItemChest,
		core.ItemCobblestone, core.ItemSmoothStone, core.ItemSand, core.ItemGravel,
		core.ItemOakLog, core.ItemOakPlanks, core.ItemLeaves, core.ItemGlass,
		core.ItemBrick, core.ItemWhiteWool, core.ItemRoofTile, core.ItemClay,
		core.ItemSnowBlock, core.ItemMossyCobblestone,
	} {
		block, ok := core.ItemPlacement(item)
		if !ok {
			t.Fatalf("测试物品 %d 不可放置", item)
		}
		source := registry.LayerRGBA(int(registry.Material(block, mesh.FacePosY)))
		column := hotbarBlockColumnOffset + int(item)
		for _, point := range [][2]int{{0, 0}, {5, 7}, {15, 15}} {
			x, y := point[0], point[1]
			src := (y*hotbarTextureSize + x) * 4
			dst := (y*hotbarTextureWidth + column*hotbarTextureSize + x) * 4
			if got, want := [4]byte(pixels[dst:dst+4]), [4]byte(source[src:src+4]); got != want {
				t.Fatalf("物品 %d 像素 (%d,%d)=%v，想要注册表材质 %v", item, x, y, got, want)
			}
		}
	}
}

// 杀死变异：不可放置物品误采样空白图集会让工具和材料消失。
func TestNonBlockItemsKeepProgrammaticTiles(t *testing.T) {
	for _, item := range []core.ItemID{core.ItemCoal, core.ItemIronIngot, core.ItemStonePickaxe} {
		var layout hotbarLayout
		appendItemTile(&layout, item, 0, 0, 1)
		if len(layout.quads) != 2 {
			t.Fatalf("物品 %d quads=%d，想要暗边和内层色块", item, len(layout.quads))
		}
		assertHotbarItemFace(t, layout.quads[1], item)
	}
}

// 杀死变异：损坏工具落入默认分支会得到全零色，与完好工具同色则无法表达损坏状态。
func TestBrokenToolColorsAreVisibleAndDistinct(t *testing.T) {
	pairs := [][2]core.ItemID{
		{core.ItemBrokenStonePickaxe, core.ItemStonePickaxe},
		{core.ItemBrokenIronPickaxe, core.ItemIronPickaxe},
	}
	var brokenColors [2][4]float32
	for index, pair := range pairs {
		brokenColors[index] = render.ItemColor(pair[0])
		if brokenColors[index] == ([4]float32{}) || brokenColors[index][3] != 1 {
			t.Fatalf("损坏工具 %d 颜色=%v，想要可见且 alpha=1", pair[0], brokenColors[index])
		}
		if brokenColors[index] == render.ItemColor(pair[1]) {
			t.Fatalf("损坏工具 %d 与完好工具颜色相同", pair[0])
		}
	}
	if brokenColors[0] == brokenColors[1] {
		t.Fatal("两种损坏工具颜色相同")
	}
}

// 杀死变异：箱子物品落入默认分支会得到全零色，无法在 HUD 与掉落物中可见。
func TestChestItemColorIsVisible(t *testing.T) {
	color := render.ItemColor(core.ItemChest)
	if color == ([4]float32{}) || color[3] != 1 {
		t.Fatalf("箱子颜色 = %v，想要可见且 alpha=1", color)
	}
	if color == render.ItemColor(core.ItemDirt) {
		t.Fatal("箱子颜色与泥土相同")
	}
}

// 杀死变异：把 hotbarTextureUV 的列宽算错、或让图集扩列时既有列的 UV 漂移到
// 相邻列，都会让某个物品的缩略图采到别的方块材质——而那在 capture golden 里
// 只表现为「有像素变了」，看不出变得对不对。这里把「列 UV 必须落在本列的
// 16 个纹素内」钉成机械断言：图集宽度随 ItemIDMax 增长，float32 的除法误差
// 允许亚纹素漂移，但绝不允许越过列边界。
func TestHotbarColumnUVStaysInsideItsOwnColumn(t *testing.T) {
	const tolerance = 0.01 // 纹素；实测漂移量级为 1e-6
	for column := range hotbarTextureColumns {
		uv := hotbarTextureUV(column)
		left := float64(uv[0]) * float64(hotbarTextureWidth)
		right := float64(uv[2]) * float64(hotbarTextureWidth)
		wantLeft := float64(column * hotbarTextureSize)
		wantRight := float64((column + 1) * hotbarTextureSize)
		if left < wantLeft-tolerance || left > wantLeft+tolerance {
			t.Fatalf("列 %d 左界 texel=%v，想要 %v±%v", column, left, wantLeft, tolerance)
		}
		if right < wantRight-tolerance || right > wantRight+tolerance {
			t.Fatalf("列 %d 右界 texel=%v，想要 %v±%v", column, right, wantRight, tolerance)
		}
	}
}

// TestHotbarDrumstickColumnsAreDistinctAndSelfContained 覆盖 HUD 图集新增的两列
// 鸡腿（任务 5.2）。
//
// 三条断言各杀一种变异：空/满两列若逐像素相同，饥饿条画什么都看不出差别；
// 画到轮廓之外会污染相邻列（图集是一条 16px 高的长带，越界即串味）；
// 写错列号会覆盖爱心列，让生命条跟着变。
func TestHotbarDrumstickColumnsAreDistinctAndSelfContained(t *testing.T) {
	pixels := make([]byte, hotbarTextureWidth*hotbarTextureSize*4)
	paintHotbarHeart(pixels, hotbarEmptyHeartColumn, false)
	paintHotbarHeart(pixels, hotbarFullHeartColumn, true)
	heartBefore := append([]byte(nil), pixels[:2*hotbarTextureSize*4]...)

	paintHotbarDrumstick(pixels, hotbarEmptyDrumstickColumn, false)
	paintHotbarDrumstick(pixels, hotbarFullDrumstickColumn, true)

	if got := pixels[:2*hotbarTextureSize*4]; !bytes.Equal(got, heartBefore) {
		t.Fatal("绘制鸡腿改写了爱心列的首行像素")
	}
	cell := func(column, x, y int) [4]byte {
		offset := (y*hotbarTextureWidth + column*hotbarTextureSize + x) * 4
		return [4]byte(pixels[offset : offset+4])
	}
	differing, painted := 0, 0
	for y := range hotbarTextureSize {
		for x := range hotbarTextureSize {
			empty, full := cell(hotbarEmptyDrumstickColumn, x, y), cell(hotbarFullDrumstickColumn, x, y)
			inside := hotbarDrumstickPixel(x, y)
			if !inside {
				if empty != ([4]byte{}) || full != ([4]byte{}) {
					t.Fatalf("轮廓外像素 (%d,%d) 被写入：空=%v 满=%v", x, y, empty, full)
				}
				continue
			}
			painted++
			if empty == ([4]byte{}) || full == ([4]byte{}) || empty[3] != 255 || full[3] != 255 {
				t.Fatalf("轮廓内像素 (%d,%d) 不可见：空=%v 满=%v", x, y, empty, full)
			}
			if empty != full {
				differing++
			}
		}
	}
	if painted < 64 {
		t.Fatalf("鸡腿轮廓只覆盖 %d 个像素，图标过小无法辨认", painted)
	}
	if differing*2 < painted {
		t.Fatalf("空/满两列只有 %d/%d 个像素不同，饱食度无法辨认", differing, painted)
	}
}
