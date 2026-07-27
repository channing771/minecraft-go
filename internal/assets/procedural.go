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
	return px
}

func grassSideTexture() []byte {
	px := noisyTexture(rgb{R: 134, G: 96, B: 67}, 12, 0x1B87)
	for y := 0; y < 4; y++ {
		for x := 0; x < texSize; x++ {
			n := int32(hash2(uint32(x), uint32(y), 0x77C1)%25) - 12
			i := (y*texSize + x) * 4
			px[i] = clamp8(88 + n)
			px[i+1] = clamp8(140 + n)
			px[i+2] = clamp8(60 + n)
		}
	}
	return px
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
