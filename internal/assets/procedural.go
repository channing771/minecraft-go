// Package assets 提供方块注册表与程序化材质。
package assets

const texSize = 16

type rgb struct{ R, G, B uint8 }

func hash2(x, y, salt uint32) uint32 {
	h := x*374761393 + y*668265263 + salt*2246822519
	h = (h ^ (h >> 13)) * 1274126177
	return h ^ (h >> 16)
}

func noisyTexture(base rgb, spread int32, salt uint32) []byte {
	px := make([]byte, texSize*texSize*4)
	for y := 0; y < texSize; y++ {
		for x := 0; x < texSize; x++ {
			n := int32(hash2(uint32(x), uint32(y), salt)%uint32(2*spread+1)) - spread
			i := (y*texSize + x) * 4
			px[i] = clamp8(int32(base.R) + n)
			px[i+1] = clamp8(int32(base.G) + n)
			px[i+2] = clamp8(int32(base.B) + n)
			px[i+3] = 255
		}
	}
	return px
}

func stoneTexture() []byte {
	px := noisyTexture(rgb{R: 128, G: 128, B: 128}, 14, 0x2545)
	for _, point := range [][2]int{{2, 3}, {3, 3}, {3, 4}, {9, 10}, {10, 10}, {10, 11}, {11, 11}} {
		paint(px, point[0], point[1], rgb{R: 92, G: 94, B: 98})
	}
	return px
}

func dirtTexture() []byte {
	px := noisyTexture(rgb{R: 134, G: 96, B: 67}, 10, 0x1B87)
	for _, point := range [][2]int{{2, 2}, {3, 2}, {10, 5}, {10, 6}, {6, 11}, {7, 11}, {13, 14}, {14, 14}} {
		paint(px, point[0], point[1], rgb{R: 166, G: 111, B: 68})
	}
	return px
}

func grassTopTexture() []byte {
	px := noisyTexture(rgb{R: 88, G: 140, B: 60}, 14, 0x9E37)
	for y := 0; y < texSize; y++ {
		for x := 0; x < texSize; x++ {
			if hash2(uint32(x), uint32(y), 0x51ED)%5 != 0 {
				continue
			}
			i := (y*texSize + x) * 4
			px[i+1] = clamp8(int32(px[i+1]) + 30)
		}
	}
	for _, point := range [][2]int{
		{-1, 4}, {0, 4}, {1, 4},
		{6, 9}, {6, 10}, {11, 2}, {12, 2}, {13, 12}, {14, 12},
	} {
		paintWrapped(px, point[0], point[1], rgb{R: 60, G: 108, B: 45})
	}
	for _, point := range [][2]int{
		{4, 2}, {5, 2}, {5, 3},
		{8, 7}, {9, 7}, {8, 8},
		{2, 12}, {3, 12}, {3, 13},
		{10, -1}, {10, 0}, {10, 1},
	} {
		paintWrapped(px, point[0], point[1], rgb{R: 105, G: 174, B: 72})
	}
	return px
}

func grassSideTexture() []byte {
	px := dirtTexture()
	depths := [...]int{3, 4, 4, 5, 6, 6, 5, 4, 3, 3, 4, 5, 5, 4, 3, 3}
	for x := 0; x < texSize; x++ {
		depth := depths[x]
		for y := 0; y < depth; y++ {
			n := int32(hash2(uint32(x), uint32(y), 0x77C1)%25) - 12
			i := (y*texSize + x) * 4
			px[i] = clamp8(88 + n)
			px[i+1] = clamp8(140 + n)
			px[i+2] = clamp8(60 + n)
		}
		paint(px, x, depth-1, rgb{R: 60, G: 108, B: 45})
	}
	return px
}

func stoneBrickTexture() []byte {
	px := noisyTexture(rgb{R: 126, G: 122, B: 116}, 8, 0x77B1)
	mortar := rgb{R: 72, G: 72, B: 74}
	for x := 0; x < texSize; x++ {
		paint(px, x, 7, mortar)
		paint(px, x, 15, mortar)
	}
	for y := 0; y < 7; y++ {
		paint(px, 4, y, mortar)
	}
	for y := 8; y < 15; y++ {
		paint(px, 12, y, mortar)
	}
	return px
}

func oreTexture(ore rgb) []byte {
	px := noisyTexture(rgb{R: 124, G: 124, B: 126}, 12, 0x5C3D)
	points := [...][2]int{
		{2, 3}, {3, 3}, {3, 4}, {4, 4},
		{10, 2}, {10, 3}, {11, 3},
		{7, 9}, {8, 9}, {8, 10}, {9, 10},
		{3, 13}, {4, 13}, {4, 14}, {12, 12}, {12, 13},
	}
	for _, point := range points {
		paint(px, point[0], point[1], ore)
	}
	return px
}

func furnaceTexture() []byte {
	px := noisyTexture(rgb{R: 100, G: 98, B: 100}, 8, 0x41D7)
	frame := rgb{R: 122, G: 120, B: 124}
	for i := 0; i < texSize; i++ {
		paint(px, i, 0, frame)
		paint(px, i, 15, frame)
		paint(px, 0, i, frame)
		paint(px, 15, i, frame)
	}
	fill(px, 4, 5, 12, 13, rgb{R: 40, G: 40, B: 44})
	for x := 5; x < 11; x += 2 {
		paint(px, x, 11, rgb{R: 212, G: 94, B: 36})
	}
	return px
}

func ironBlockTexture() []byte {
	px := noisyTexture(rgb{R: 218, G: 220, B: 224}, 6, 0x2E95)
	frame := rgb{R: 154, G: 158, B: 166}
	for i := 0; i < texSize; i++ {
		paint(px, i, 0, frame)
		paint(px, i, 15, frame)
		paint(px, 0, i, frame)
		paint(px, 15, i, frame)
	}
	for _, point := range [][2]int{{2, 2}, {13, 2}, {2, 13}, {13, 13}} {
		paint(px, point[0], point[1], rgb{R: 118, G: 122, B: 132})
	}
	return px
}

func chestTexture() []byte {
	px := noisyTexture(rgb{R: 156, G: 108, B: 58}, 10, 0x9C4E)
	seam := rgb{R: 86, G: 54, B: 30}
	for x := 0; x < texSize; x++ {
		paint(px, x, 5, seam)
		paint(px, x, 11, seam)
	}
	fill(px, 7, 7, 9, 10, rgb{R: 214, G: 178, B: 74})
	return px
}

func lightBlockTexture() []byte {
	px := noisyTexture(rgb{R: 238, G: 196, B: 76}, 8, 0x4C17)
	frame := rgb{R: 164, G: 106, B: 30}
	for i := 0; i < texSize; i++ {
		paint(px, i, 0, frame)
		paint(px, i, texSize-1, frame)
		paint(px, 0, i, frame)
		paint(px, texSize-1, i, frame)
	}
	fill(px, 4, 4, 12, 12, rgb{R: 255, G: 226, B: 112})
	return px
}

func leavesTexture() []byte {
	px := make([]byte, texSize*texSize*4)
	colors := [...]rgb{
		{R: 48, G: 108, B: 44}, {R: 62, G: 126, B: 54},
		{R: 70, G: 136, B: 58}, {R: 78, G: 144, B: 62},
	}
	for y := 0; y < texSize; y++ {
		for x := 0; x < texSize; x++ {
			color := colors[hash2(uint32(x/2), uint32(y/2), 0x1EA5)%uint32(len(colors))]
			paint(px, x, y, color)
			if hash2(uint32(x), uint32(y), 0x1EA5)%10 < 3 {
				px[(y*texSize+x)*4+3] = 0
			}
		}
	}
	return px
}

func glassTexture() []byte {
	px := make([]byte, texSize*texSize*4)
	frame := rgb{R: 188, G: 222, B: 226}
	for i := 0; i < texSize; i++ {
		paint(px, i, 0, frame)
		paint(px, i, texSize-1, frame)
		paint(px, 0, i, frame)
		paint(px, texSize-1, i, frame)
	}
	for _, p := range [][2]int{{0, 0}, {15, 0}, {0, 15}, {15, 15}} {
		paint(px, p[0], p[1], rgb{R: 142, G: 184, B: 190})
	}
	for i := 3; i < 7; i++ {
		paint(px, i, i, rgb{R: 224, G: 244, B: 246})
	}
	for i := 9; i < 13; i++ {
		paint(px, i, 14-i, rgb{R: 224, G: 244, B: 246})
	}
	return px
}

// waterAlpha 是水材质层的固定 alpha。
//
// 取 160/255：足够低使水底地形透过水面可见，又足够高让水面本身呈现可辨识
// 的水色。水面走 alpha blend 而非 cutout，因此这里**不能**是 0 或 255——
// 前者让整片水消失，后者让水变成不透明蓝方块。
const waterAlpha = 160

// waterTexture 生成半透明的蓝色水面材质。
//
// 结构：以噪声给出细碎的深浅变化（避免大面积同色带来的塑料感），再叠两条
// 错开的亮色波纹让水面在世界坐标 UV 下有可辨认的流向。全部像素共用
// waterAlpha，逐像素蓝色主导（B 严格大于 R 与 G），守卫见
// TestWaterTextureIsTranslucentBlue。
func waterTexture() []byte {
	px := noisyTexture(rgb{R: 42, G: 96, B: 186}, 12, 0x57A2)
	for _, point := range [][2]int{
		{1, 3}, {2, 3}, {3, 4}, {4, 4}, {5, 3}, {6, 3},
		{9, 10}, {10, 10}, {11, 11}, {12, 11}, {13, 10}, {14, 10},
	} {
		paint(px, point[0], point[1], rgb{R: 96, G: 158, B: 226})
	}
	for i := 3; i < len(px); i += 4 {
		px[i] = waterAlpha
	}
	return px
}

func cobblestoneTexture() []byte {
	px := noisyTexture(rgb{R: 116, G: 118, B: 120}, 10, 0xC0B1)
	seam := rgb{R: 70, G: 72, B: 74}
	for x := 0; x < texSize; x++ {
		paint(px, x, 5, seam)
		paint(px, x, 11, seam)
	}
	for y := 0; y < 5; y++ {
		paint(px, 4, y, seam)
		paint(px, 11, y, seam)
	}
	for y := 6; y < 11; y++ {
		paint(px, 7, y, seam)
		paint(px, 13, y, seam)
	}
	for y := 12; y < texSize; y++ {
		paint(px, 3, y, seam)
		paint(px, 10, y, seam)
	}
	return px
}

func smoothStoneTexture() []byte {
	px := noisyTexture(rgb{R: 142, G: 142, B: 140}, 6, 0x5A10)
	for cellY := 0; cellY < 4; cellY++ {
		for cellX := 0; cellX < 4; cellX++ {
			x := cellX*4 + int(hash2(uint32(cellX), uint32(cellY), 0x5A10)%3)
			y := cellY*4 + int(hash2(uint32(cellY), uint32(cellX), 0x5A10)%3)
			paint(px, x, y, rgb{R: 132, G: 134, B: 134})
		}
	}
	return px
}

func sandTexture() []byte {
	px := noisyTexture(rgb{R: 218, G: 202, B: 146}, 8, 0x5A2D)
	bright := rgb{R: 240, G: 226, B: 168}
	for _, point := range [][2]int{
		{2, 3}, {3, 3}, {8, 7}, {8, 8}, {12, 12}, {13, 12},
		{5, 5}, {6, 5}, {10, 14}, {11, 14}, {14, 9}, {14, 10},
	} {
		paint(px, point[0], point[1], bright)
	}
	for _, point := range [][2]int{{5, 1}, {10, 4}, {4, 10}, {14, 6}, {1, 14}} {
		paint(px, point[0], point[1], rgb{R: 196, G: 180, B: 126})
	}
	return px
}

func gravelTexture() []byte {
	px := noisyTexture(rgb{R: 112, G: 108, B: 106}, 12, 0x6A41)
	for index, origin := range [][2]int{{2, 2}, {10, 3}, {5, 9}, {12, 12}} {
		color := rgb{R: 82, G: 80, B: 80}
		if index%2 != 0 {
			color = rgb{R: 144, G: 140, B: 136}
		}
		fill(px, origin[0], origin[1], origin[0]+2, origin[1]+2, color)
	}
	return px
}

func oakLogSideTexture() []byte {
	px := noisyTexture(rgb{R: 112, G: 76, B: 42}, 8, 0x0A61)
	for _, x := range []int{2, 6, 11, 14} {
		for y := 0; y < texSize; y++ {
			paint(px, x, y, rgb{R: 72, G: 46, B: 28})
		}
	}
	return px
}

func oakLogTopTexture() []byte {
	px := noisyTexture(rgb{R: 166, G: 126, B: 72}, 6, 0x0A62)
	for y := 0; y < texSize; y++ {
		for x := 0; x < texSize; x++ {
			radius := max(absInt(2*x-15), absInt(2*y-15))
			if radius == 5 || radius == 11 {
				paint(px, x, y, rgb{R: 202, G: 158, B: 90})
			}
		}
	}
	return px
}

func oakPlanksTexture() []byte {
	px := noisyTexture(rgb{R: 174, G: 124, B: 68}, 8, 0x0A63)
	seam := rgb{R: 100, G: 66, B: 38}
	for x := 0; x < texSize; x++ {
		paint(px, x, 5, seam)
		paint(px, x, 11, seam)
	}
	for y := 0; y < 5; y++ {
		paint(px, 5, y, seam)
	}
	for y := 6; y < 11; y++ {
		paint(px, 11, y, seam)
	}
	for y := 12; y < texSize; y++ {
		paint(px, 4, y, seam)
	}
	fill(px, 12, 2, 14, 4, rgb{R: 86, G: 54, B: 32})
	return px
}

func brickTexture() []byte {
	px := noisyTexture(rgb{R: 154, G: 74, B: 58}, 8, 0xB21C)
	paintStaggeredSeams(px, rgb{R: 72, G: 66, B: 64})
	return px
}

func whiteWoolTexture() []byte {
	px := noisyTexture(rgb{R: 226, G: 222, B: 210}, 4, 0x7001)
	for index, origin := range [][2]int{{2, 2}, {8, 4}, {4, 10}, {11, 12}} {
		color := rgb{R: 214, G: 212, B: 204}
		if index%2 != 0 {
			color = rgb{R: 232, G: 228, B: 216}
		}
		fill(px, origin[0], origin[1], origin[0]+2, origin[1]+2, color)
	}
	return px
}

func roofTileTexture() []byte {
	px := noisyTexture(rgb{R: 138, G: 62, B: 46}, 6, 0x711E)
	paintStaggeredSeams(px, rgb{R: 72, G: 32, B: 28})
	for _, point := range [][2]int{{1, 3}, {2, 4}, {7, 3}, {8, 4}, {3, 9}, {4, 10}, {11, 9}, {12, 10}} {
		paint(px, point[0], point[1], rgb{R: 174, G: 78, B: 54})
	}
	return px
}

func clayTexture() []byte {
	px := noisyTexture(rgb{R: 132, G: 150, B: 158}, 6, 0xC1A7)
	for _, origin := range [][2]int{{2, 3}, {9, 2}, {5, 10}, {12, 12}} {
		fill(px, origin[0], origin[1], origin[0]+2, origin[1]+2, rgb{R: 126, G: 146, B: 156})
	}
	return px
}

func snowTopTexture() []byte {
	px := noisyTexture(rgb{R: 244, G: 246, B: 244}, 4, 0x5A09)
	for _, point := range [][2]int{{2, 3}, {7, 1}, {12, 4}, {4, 9}, {10, 11}, {14, 14}} {
		paint(px, point[0], point[1], rgb{R: 255, G: 255, B: 255})
	}
	return px
}

func snowSideTexture() []byte {
	px := noisyTexture(rgb{R: 214, G: 228, B: 236}, 5, 0x5A0D)
	for x := 0; x < texSize; x++ {
		paint(px, x, 5, rgb{R: 198, G: 216, B: 228})
		paint(px, x, 11, rgb{R: 202, G: 220, B: 230})
	}
	return px
}

func mossyCobblestoneTexture() []byte {
	px := cobblestoneTexture()
	for y := 0; y < texSize; y++ {
		for x := 0; x < texSize; x++ {
			i := (y*texSize + x) * 4
			brightness := (int(px[i]) + int(px[i+1]) + int(px[i+2])) / 3
			if brightness >= 90 && hash2(uint32(x), uint32(y), 0xA055)%5 == 0 {
				paint(px, x, y, rgb{R: 86, G: 126, B: 70})
			}
		}
	}
	return px
}

func paintStaggeredSeams(px []byte, seam rgb) {
	for x := 0; x < texSize; x++ {
		paint(px, x, 5, seam)
		paint(px, x, 11, seam)
	}
	for y := 0; y < 5; y++ {
		paint(px, 5, y, seam)
		paint(px, 13, y, seam)
	}
	for y := 6; y < 11; y++ {
		paint(px, 9, y, seam)
	}
	for y := 12; y < texSize; y++ {
		paint(px, 3, y, seam)
		paint(px, 11, y, seam)
	}
}
func fill(px []byte, left, top, right, bottom int, color rgb) {
	for y := top; y < bottom; y++ {
		for x := left; x < right; x++ {
			paint(px, x, y, color)
		}
	}
}

func paint(px []byte, x, y int, color rgb) {
	i := (y*texSize + x) * 4
	px[i] = color.R
	px[i+1] = color.G
	px[i+2] = color.B
	px[i+3] = 255
}

func paintWrapped(px []byte, x, y int, color rgb) {
	x = (x%texSize + texSize) % texSize
	y = (y%texSize + texSize) % texSize
	paint(px, x, y, color)
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func clamp8(v int32) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

// farmlandDryTexture 生成干耕地的顶面材质：比泥土更深的褐色，加三道犁沟。
//
// 干湿两种耕地**只靠明暗区分**（与 Minecraft 的约定一致），因此这里的基色必须
// 明显亮于 farmlandWetTexture，守卫见 TestFarmlandWetIsDarkerThanDry。
func farmlandDryTexture() []byte {
	px := noisyTexture(rgb{R: 108, G: 74, B: 46}, 9, 0x7A31)
	furrow := rgb{R: 78, G: 52, B: 32}
	for _, y := range [...]int{2, 7, 12} {
		for x := 0; x < texSize; x++ {
			paint(px, x, y, furrow)
		}
	}
	return px
}

// farmlandWetTexture 生成湿耕地的顶面材质：近黑的深褐，犁沟同样更深。
func farmlandWetTexture() []byte {
	px := noisyTexture(rgb{R: 56, G: 36, B: 22}, 7, 0x7A32)
	furrow := rgb{R: 34, G: 22, B: 14}
	for _, y := range [...]int{2, 7, 12} {
		for x := 0; x < texSize; x++ {
			paint(px, x, y, furrow)
		}
	}
	return px
}

// wheatStageCount 是小麦的生长阶段数，与 core.WheatStage0ID..WheatStage7ID 一一对应。
const wheatStageCount = 8

// lerp8 在 [from, to] 上按 step/span 线性插值，全整数运算、不引入浮点。
func lerp8(from, to uint8, step, span int) uint8 {
	return uint8(int32(from) + (int32(to)-int32(from))*int32(step)/int32(span))
}

// wheatTexture 生成小麦第 stage（0..7）阶段的 cutout 材质。
//
// 结构：5 根等距麦秆自图像**第 0 行**向上生长。第 0 行在世界坐标 UV 下对应方块
// 底面（terrain.wgsl 的植物分支取 uv = (world.x, world.y)，v = 0 即格子底面），
// 于是「阶段越高长得越高」在画面上就是从地面往上长。秆高 h = 2 + 2*stage，
// 阶段 7 恰好长满 16 行；颜色自嫩芽绿线性插值到成熟金黄，阶段 >= 5 起在顶端
// 三行左右各加宽一列作为麦穗。
//
// 其余像素 alpha 为 0，走 terrain pass 既有的 alpha cutout（`c.a < 0.5` 即
// discard），因此**不得**出现中间 alpha——守卫见 TestCutoutLayersUseBinaryAlpha。
func wheatTexture(stage int) []byte {
	px := make([]byte, texSize*texSize*4)
	young := rgb{R: 74, G: 142, B: 56}
	ripe := rgb{R: 206, G: 174, B: 74}
	base := rgb{
		R: lerp8(young.R, ripe.R, stage, wheatStageCount-1),
		G: lerp8(young.G, ripe.G, stage, wheatStageCount-1),
		B: lerp8(young.B, ripe.B, stage, wheatStageCount-1),
	}
	height := 2 + 2*stage
	salt := 0x3D57 + uint32(stage)
	speck := func(x, y int) {
		n := int32(hash2(uint32(x), uint32(y), salt)%25) - 12
		paint(px, x, y, rgb{
			R: clamp8(int32(base.R) + n),
			G: clamp8(int32(base.G) + n),
			B: clamp8(int32(base.B) + n),
		})
	}
	for _, x := range [...]int{1, 4, 7, 10, 13} {
		for y := 0; y < height; y++ {
			speck(x, y)
		}
		if stage < 5 {
			continue
		}
		// 麦穗：成熟阶段在顶端三行向两侧各加宽一列，读起来才像结了穗而不是草。
		for y := height - 3; y < height; y++ {
			speck(x-1, y)
			speck(x+1, y)
		}
	}
	return px
}
