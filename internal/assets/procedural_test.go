package assets_test

import (
	"testing"

	"minecraft-go/internal/assets"
	"minecraft-go/internal/core"
	"minecraft-go/internal/mesh"
	"minecraft-go/internal/world"
)

func TestRegistryAirIsTransparent(t *testing.T) {
	r := assets.NewRegistry()
	if r.Opaque(world.AirID) {
		t.Fatal("空气不应是不透明的")
	}
	if !r.Opaque(core.StoneID) {
		t.Fatal("石头应是不透明的")
	}
	if r.Opaque(core.GlassID) {
		t.Fatal("玻璃不应完全遮挡 AO 或天空光")
	}
	if r.Opaque(core.LeavesID) {
		t.Fatal("树叶不应完全遮挡 AO 或天空光")
	}
}

func TestGrassHasDistinctTopAndSide(t *testing.T) {
	r := assets.NewRegistry()
	top := r.Material(core.GrassID, mesh.FacePosY)
	side := r.Material(core.GrassID, mesh.FaceNegX)
	bottom := r.Material(core.GrassID, mesh.FaceNegY)
	if top == side {
		t.Fatal("草方块的顶面与侧面材质相同")
	}
	if bottom == top {
		t.Fatal("草方块的底面与顶面材质相同")
	}
	if bottom != r.Material(core.DirtID, mesh.FacePosY) {
		t.Fatal("草方块底面应复用泥土材质")
	}
}

func TestEveryLayerIsFullSize(t *testing.T) {
	r := assets.NewRegistry()
	if r.LayerCount() == 0 {
		t.Fatal("材质层数为 0")
	}
	for i := 0; i < r.LayerCount(); i++ {
		if got := len(r.LayerRGBA(i)); got != 16*16*4 {
			t.Fatalf("第 %d 层大小 = %d 字节，想要 %d", i, got, 16*16*4)
		}
	}
}

func TestProceduralTexturesAreDeterministic(t *testing.T) {
	a, b := assets.NewRegistry(), assets.NewRegistry()
	for i := 0; i < a.LayerCount(); i++ {
		pa, pb := a.LayerRGBA(i), b.LayerRGBA(i)
		for j := range pa {
			if pa[j] != pb[j] {
				t.Fatalf("第 %d 层第 %d 字节不一致: %d vs %d", i, j, pa[j], pb[j])
			}
		}
	}
}

func TestTexturesAreNotFlat(t *testing.T) {
	r := assets.NewRegistry()
	for i := 0; i < r.LayerCount(); i++ {
		px := r.LayerRGBA(i)
		distinct := map[[3]byte]struct{}{}
		for j := 0; j < len(px); j += 4 {
			distinct[[3]byte{px[j], px[j+1], px[j+2]}] = struct{}{}
		}
		if len(distinct) < 4 {
			t.Fatalf("第 %d 层只有 %d 种颜色，材质太平", i, len(distinct))
		}
	}
}

func TestCutoutLayersUseBinaryAlpha(t *testing.T) {
	r := assets.NewRegistry()
	for _, layer := range []uint16{assets.LayerLeaves, assets.LayerGlass} {
		px := r.LayerRGBA(int(layer))
		opaque, transparent := 0, 0
		for i := 3; i < len(px); i += 4 {
			switch px[i] {
			case 0:
				transparent++
			case 255:
				opaque++
			default:
				t.Fatalf("cutout 层 %d 含非二值 alpha %d", layer, px[i])
			}
		}
		if opaque == 0 || transparent == 0 {
			t.Fatalf("cutout 层 %d 不同时包含透明与不透明像素", layer)
		}
	}
}

// 杀死变异：把功能方块退回仅有基色差异的随机噪声，会丢失这些可辨识结构。
func TestFunctionalBlocksHavePixelStructures(t *testing.T) {
	r := assets.NewRegistry()

	brick := r.LayerRGBA(int(assets.LayerStoneBrick))
	if mortar, body := rowBrightness(brick, 7), rowBrightness(brick, 5); mortar+20 >= body {
		t.Fatalf("石砖灰缝亮度=%d，砖面=%d，想要明显的水平错缝", mortar, body)
	}
	if pixel(brick, 4, 2) == pixel(brick, 12, 2) || pixel(brick, 4, 10) == pixel(brick, 12, 10) {
		t.Fatal("石砖上下两排缺少交错竖缝")
	}

	coal := r.LayerRGBA(int(assets.LayerCoalOre))
	if got := adjacentPixels(coal, func(p [3]byte) bool { return p[0] < 58 && p[1] < 58 && p[2] < 62 }); got < 4 {
		t.Fatalf("煤矿相邻深色矿点=%d，想要至少 4", got)
	}
	iron := r.LayerRGBA(int(assets.LayerIronOre))
	if got := adjacentPixels(iron, func(p [3]byte) bool {
		return int(p[0])-int(p[1]) >= 32 && int(p[1])-int(p[2]) >= 12
	}); got < 4 {
		t.Fatalf("铁矿相邻暖色矿点=%d，想要至少 4", got)
	}

	furnace := r.LayerRGBA(int(assets.LayerFurnace))
	if center, edge := areaBrightness(furnace, 4, 5, 12, 13), borderBrightness(furnace); center+24 >= edge {
		t.Fatalf("熔炉炉口亮度=%d，边框=%d，想要明显的深色炉口", center, edge)
	}

	ironBlock := r.LayerRGBA(int(assets.LayerIronBlock))
	if center, edge := areaBrightness(ironBlock, 4, 4, 12, 12), borderBrightness(ironBlock); edge+12 >= center {
		t.Fatalf("铁块边框亮度=%d，面板=%d，想要内亮外暗的金属面板", edge, center)
	}

	chest := r.LayerRGBA(int(assets.LayerChest))
	latch := pixel(chest, 7, 8)
	if latch[0] < 190 || latch[1] < 150 || latch[2] > 100 {
		t.Fatalf("箱子锁扣颜色=%v，想要可辨识的暖金属锁扣", latch)
	}
	if seam, plank := rowBrightness(chest, 5), rowBrightness(chest, 3); seam+18 >= plank {
		t.Fatalf("箱子板缝亮度=%d，木板=%d，想要明显的水平板缝", seam, plank)
	}
}

// 杀死变异：恢复整齐草带或仅用独立噪声会让自然地表失去像素簇与草缘变化。
func TestNaturalBlocksHaveClusteredSurfaceDetail(t *testing.T) {
	r := assets.NewRegistry()

	grass := r.LayerRGBA(int(assets.LayerGrassTop))
	if got := adjacentPixels(grass, func(p [3]byte) bool {
		return p[1] < 112 && int(p[1])-int(p[0]) >= 34
	}); got < 4 {
		t.Fatalf("草地相邻深色草簇=%d，想要至少 4", got)
	}
	if got := adjacentPixels(grass, func(p [3]byte) bool {
		return p[1] >= 164 && int(p[1])-int(p[0]) >= 42
	}); got < 6 {
		t.Fatalf("草地相邻亮色草簇=%d，想要至少 6", got)
	}

	side := r.LayerRGBA(int(assets.LayerGrassSide))
	depths := map[int]struct{}{}
	maxDepth := 0
	for x := 0; x < 16; x++ {
		depth := 0
		for y := 0; y < 8; y++ {
			p := pixel(side, x, y)
			if int(p[1])-int(p[0]) < 24 {
				break
			}
			depth++
		}
		depths[depth] = struct{}{}
		maxDepth = max(maxDepth, depth)
	}
	if len(depths) < 3 {
		t.Fatalf("草方块侧面草缘深度种类=%d，想要至少 3", len(depths))
	}
	if maxDepth < 6 {
		t.Fatalf("草方块侧面最深草缘=%d，想要至少 6 像素的下垂层次", maxDepth)
	}

	dirt := r.LayerRGBA(int(assets.LayerDirt))
	if got := adjacentPixels(dirt, func(p [3]byte) bool {
		return int(p[0])-int(p[1]) >= 48 && int(p[1])-int(p[2]) >= 18
	}); got < 4 {
		t.Fatalf("泥土相邻土块高光=%d，想要至少 4", got)
	}
}

func pixel(px []byte, x, y int) [3]byte {
	i := (y*16 + x) * 4
	return [3]byte{px[i], px[i+1], px[i+2]}
}

func rowBrightness(px []byte, y int) int {
	return areaBrightness(px, 0, y, 16, y+1)
}

func areaBrightness(px []byte, left, top, right, bottom int) int {
	total := 0
	for y := top; y < bottom; y++ {
		for x := left; x < right; x++ {
			p := pixel(px, x, y)
			total += int(p[0]) + int(p[1]) + int(p[2])
		}
	}
	return total / ((right - left) * (bottom - top) * 3)
}

func borderBrightness(px []byte) int {
	total := 0
	for i := 0; i < 16; i++ {
		for _, p := range [4][3]byte{pixel(px, i, 0), pixel(px, i, 15), pixel(px, 0, i), pixel(px, 15, i)} {
			total += int(p[0]) + int(p[1]) + int(p[2])
		}
	}
	return total / (16 * 4 * 3)
}

func adjacentPixels(px []byte, matches func([3]byte) bool) int {
	count := 0
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			if !matches(pixel(px, x, y)) {
				continue
			}
			if x+1 < 16 && matches(pixel(px, x+1, y)) || y+1 < 16 && matches(pixel(px, x, y+1)) {
				count++
			}
		}
	}
	return count
}
