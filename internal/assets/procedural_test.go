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

func TestCommonMaterialTexturesHaveFixedStructures(t *testing.T) {
	r := assets.NewRegistry()

	for _, tt := range []struct {
		name        string
		layer       uint16
		seam, body  [][2]int
		minContrast int
	}{
		{"圆石", assets.LayerCobblestone, [][2]int{{2, 5}, {8, 11}, {4, 2}}, [][2]int{{2, 2}, {8, 8}, {13, 13}}, 18},
		{"砖块", assets.LayerBrick, [][2]int{{2, 5}, {8, 11}, {5, 2}}, [][2]int{{2, 2}, {8, 8}, {13, 13}}, 18},
		{"红色瓦块", assets.LayerRoofTile, [][2]int{{2, 5}, {8, 11}, {5, 2}}, [][2]int{{2, 2}, {8, 8}, {13, 13}}, 18},
	} {
		px := r.LayerRGBA(int(tt.layer))
		if seam, body := pointsBrightness(px, tt.seam), pointsBrightness(px, tt.body); seam+tt.minContrast > body {
			t.Fatalf("%s暗缝亮度=%d，主体=%d，想要至少 %d 对比", tt.name, seam, body, tt.minContrast)
		}
	}

	smooth := r.LayerRGBA(int(assets.LayerSmoothStone))
	mean := areaBrightness(smooth, 0, 0, 16, 16)
	for y := 0; y < 16; y++ {
		if row := rowBrightness(smooth, y); row+18 < mean {
			t.Fatalf("平滑石第 %d 行亮度=%d，整体=%d，不应有贯穿暗缝", y, row, mean)
		}
	}

	for _, tt := range []struct {
		name  string
		layer uint16
		grain func([3]byte) bool
	}{
		{"沙子", assets.LayerSand, func(p [3]byte) bool { return p[0] >= 232 && p[1] >= 214 }},
		{"砾石", assets.LayerGravel, func(p [3]byte) bool { return p[0] <= 88 || p[0] >= 138 }},
	} {
		if got := adjacentPixels(r.LayerRGBA(int(tt.layer)), tt.grain); got < 6 {
			t.Fatalf("%s相邻颗粒=%d，想要至少 6", tt.name, got)
		}
	}

	logSide := r.LayerRGBA(int(assets.LayerOakLogSide))
	for _, x := range []int{2, 6, 11, 14} {
		if band, neighbor := columnBrightness(logSide, x), columnBrightness(logSide, x-1); band+18 > neighbor {
			t.Fatalf("原木侧面 x=%d 暗带亮度=%d，邻列=%d", x, band, neighbor)
		}
	}
	logTop := r.LayerRGBA(int(assets.LayerOakLogTop))
	for _, radius := range []int{5, 11} {
		if ring, inside := ringBrightness(logTop, radius), ringBrightness(logTop, radius-2); ring < inside+18 {
			t.Fatalf("原木顶面半径 %d 年轮亮度=%d，内侧=%d", radius, ring, inside)
		}
	}

	planks := r.LayerRGBA(int(assets.LayerOakPlanks))
	if seam, body := (rowBrightness(planks, 5)+rowBrightness(planks, 11))/2, rowBrightness(planks, 8); seam+18 > body {
		t.Fatalf("木板横缝亮度=%d，主体=%d", seam, body)
	}
	if knot, body := areaBrightness(planks, 12, 2, 14, 4), areaBrightness(planks, 8, 1, 11, 4); knot+18 > body {
		t.Fatalf("木板结疤亮度=%d，主体=%d", knot, body)
	}

	wool := r.LayerRGBA(int(assets.LayerWhiteWool))
	if lo, hi := brightnessRange(wool); hi-lo > 24 {
		t.Fatalf("白色羊毛亮度范围=%d..%d，想要低对比纤维", lo, hi)
	}
	if got := adjacentPixels(wool, func(p [3]byte) bool { return brightness(p) <= 218 }); got < 4 {
		t.Fatalf("白色羊毛相邻纤维像素=%d，想要至少 4", got)
	}

	clay := averageColor(r.LayerRGBA(int(assets.LayerClay)))
	if clay[1] < clay[0]+8 || clay[2] < clay[0]+15 {
		t.Fatalf("黏土平均色=%v，想要冷灰蓝", clay)
	}
	snowTop := areaBrightness(r.LayerRGBA(int(assets.LayerSnowTop)), 0, 0, 16, 16)
	snowSide := areaBrightness(r.LayerRGBA(int(assets.LayerSnowSide)), 0, 0, 16, 16)
	if snowTop < snowSide+15 {
		t.Fatalf("雪顶亮度=%d，侧面=%d，想要顶面更亮", snowTop, snowSide)
	}

	cobble := r.LayerRGBA(int(assets.LayerCobblestone))
	mossy := r.LayerRGBA(int(assets.LayerMossyCobblestone))
	green := 0
	for i := 0; i < len(cobble); i += 4 {
		base := (int(cobble[i]) + int(cobble[i+1]) + int(cobble[i+2])) / 3
		if base < 90 && [3]byte(mossy[i:i+3]) != [3]byte(cobble[i:i+3]) {
			t.Fatalf("苔藓覆盖了圆石暗缝像素 %d", i/4)
		}
		if int(mossy[i+1]) >= int(mossy[i])+18 && int(mossy[i+1]) >= int(mossy[i+2])+8 {
			green++
		}
	}
	if green < 12 {
		t.Fatalf("苔藓圆石绿色覆盖=%d，想要至少 12", green)
	}
}

func TestLeavesTextureHasFixedCoverageAndClusters(t *testing.T) {
	r := assets.NewRegistry()
	leaves := r.LayerRGBA(int(assets.LayerLeaves))
	transparent := 0
	for i := 3; i < len(leaves); i += 4 {
		if leaves[i] == 0 {
			transparent++
		}
	}
	percent := transparent * 100 / (16 * 16)
	if percent < 25 || percent > 35 {
		t.Fatalf("树叶透明率=%d%%，想要 25%%..35%%", percent)
	}
	if got := adjacentOpaqueSameColor(leaves); got < 24 {
		t.Fatalf("树叶相邻同色叶簇=%d，想要至少 24", got)
	}
}

func TestGlassTextureHasTransparentCenterFrameAndHighlights(t *testing.T) {
	glass := assets.NewRegistry().LayerRGBA(int(assets.LayerGlass))
	if alphaAt(glass, 8, 8) != 0 {
		t.Fatalf("玻璃中心 alpha=%d，想要透明", alphaAt(glass, 8, 8))
	}
	for _, edge := range [][2]int{{8, 0}, {8, 15}, {0, 8}, {15, 8}} {
		if alphaAt(glass, edge[0], edge[1]) != 255 {
			t.Fatalf("玻璃边框 (%d,%d) alpha=%d，想要不透明", edge[0], edge[1], alphaAt(glass, edge[0], edge[1]))
		}
	}
	for _, segment := range []struct {
		name                     string
		left, top, right, bottom int
	}{
		{"左上高光", 3, 3, 7, 7},
		{"右上高光", 9, 2, 13, 6},
	} {
		if got := matchingPixels(glass, segment.left, segment.top, segment.right, segment.bottom, func(p [3]byte) bool {
			return p[0] >= 220 && p[1] >= 240 && p[2] >= 240
		}); got < 4 {
			t.Fatalf("玻璃%s像素=%d，想要至少 4", segment.name, got)
		}
	}
}

func TestGrassTexturesWrapAcrossPeriodicBoundaries(t *testing.T) {
	r := assets.NewRegistry()
	side := r.LayerRGBA(int(assets.LayerGrassSide))
	depths := make([]int, 16)
	for x := range depths {
		for y := 0; y < 8; y++ {
			p := pixel(side, x, y)
			if int(p[1])-int(p[0]) < 24 {
				break
			}
			depths[x]++
		}
	}
	for x := range depths {
		next := (x + 1) % len(depths)
		if delta := intAbs(depths[x] - depths[next]); delta > 1 {
			t.Fatalf("草侧列 %d→%d 深度=%d→%d，差=%d", x, next, depths[x], depths[next], delta)
		}
	}

	top := r.LayerRGBA(int(assets.LayerGrassTop))
	if !hasMatchingWrapCluster(top, true) {
		t.Fatal("草顶左右边缘没有跨边界连续同色草簇")
	}
	if !hasMatchingWrapCluster(top, false) {
		t.Fatal("草顶上下边缘没有跨边界连续同色草簇")
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

func pointsBrightness(px []byte, points [][2]int) int {
	total := 0
	for _, point := range points {
		total += brightness(pixel(px, point[0], point[1]))
	}
	return total / len(points)
}

func columnBrightness(px []byte, x int) int {
	return areaBrightness(px, x, 0, x+1, 16)
}

func ringBrightness(px []byte, radius int) int {
	total, count := 0, 0
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			if max(intAbs(2*x-15), intAbs(2*y-15)) != radius {
				continue
			}
			total += brightness(pixel(px, x, y))
			count++
		}
	}
	return total / count
}

func brightnessRange(px []byte) (int, int) {
	lo, hi := 255, 0
	for i := 0; i < len(px); i += 4 {
		value := (int(px[i]) + int(px[i+1]) + int(px[i+2])) / 3
		lo, hi = min(lo, value), max(hi, value)
	}
	return lo, hi
}

func averageColor(px []byte) [3]int {
	var color [3]int
	for i := 0; i < len(px); i += 4 {
		color[0] += int(px[i])
		color[1] += int(px[i+1])
		color[2] += int(px[i+2])
	}
	for i := range color {
		color[i] /= len(px) / 4
	}
	return color
}

func alphaAt(px []byte, x, y int) byte {
	return px[(y*16+x)*4+3]
}

func hasMatchingWrapCluster(px []byte, horizontal bool) bool {
	for i := 0; i < 16; i++ {
		var a, b, neighbor [3]byte
		if horizontal {
			a, b, neighbor = pixel(px, 15, i), pixel(px, 0, i), pixel(px, 1, i)
		} else {
			a, b, neighbor = pixel(px, i, 15), pixel(px, i, 0), pixel(px, i, 1)
		}
		if a == b && b == neighbor {
			return true
		}
	}
	return false
}

func brightness(p [3]byte) int {
	return (int(p[0]) + int(p[1]) + int(p[2])) / 3
}

func intAbs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func adjacentOpaqueSameColor(px []byte) int {
	count := 0
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			if alphaAt(px, x, y) == 0 {
				continue
			}
			current := pixel(px, x, y)
			if x+1 < 16 && alphaAt(px, x+1, y) == 255 && current == pixel(px, x+1, y) ||
				y+1 < 16 && alphaAt(px, x, y+1) == 255 && current == pixel(px, x, y+1) {
				count++
			}
		}
	}
	return count
}

func matchingPixels(px []byte, left, top, right, bottom int, matches func([3]byte) bool) int {
	count := 0
	for y := top; y < bottom; y++ {
		for x := left; x < right; x++ {
			if matches(pixel(px, x, y)) {
				count++
			}
		}
	}
	return count
}
