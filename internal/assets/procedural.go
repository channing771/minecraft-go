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
	for _, point := range [][2]int{{1, 4}, {2, 4}, {6, 9}, {6, 10}, {11, 2}, {12, 2}, {13, 12}, {14, 12}} {
		paint(px, point[0], point[1], rgb{R: 60, G: 108, B: 45})
	}
	for _, point := range [][2]int{
		{4, 2}, {5, 2}, {5, 3},
		{8, 7}, {9, 7}, {8, 8},
		{2, 12}, {3, 12}, {3, 13},
	} {
		paint(px, point[0], point[1], rgb{R: 105, G: 174, B: 72})
	}
	return px
}

func grassSideTexture() []byte {
	px := dirtTexture()
	for x := 0; x < texSize; x++ {
		depth := 2 + int(hash2(uint32(x), 0, 0x77C1)%5)
		if x == 3 || x == 11 {
			depth = 7
		}
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

func clamp8(v int32) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}
