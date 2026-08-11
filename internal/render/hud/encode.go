package hud

import (
	"encoding/binary"
	"math"
)

const (
	hotbarInstanceBytes  = 48
	hotbarViewportOffset = 0
	hotbarViewportBytes  = 16
	hotbarQuadOffset     = 256
	hotbarQuadSize       = maxHotbarQuads * hotbarInstanceBytes
	hotbarGlyphOffset    = (hotbarQuadOffset + hotbarQuadSize + 255) &^ 255
	hotbarGlyphSize      = maxHotbarGlyphs * hotbarInstanceBytes
	hotbarUploadBytes    = hotbarGlyphOffset + hotbarGlyphSize
)

func encodeHotbarViewport(dst []byte, width, height float32) []byte {
	out := dst[:hotbarViewportBytes]
	for index, value := range [4]float32{width, height, 0, 0} {
		binary.LittleEndian.PutUint32(out[index*4:], math.Float32bits(value))
	}
	return out
}
func encodeHotbarInstances(dst []byte, instances []hotbarInstance) []byte {
	out := dst[:len(instances)*hotbarInstanceBytes]
	for index, instance := range instances {
		values := [12]float32{
			instance.X, instance.Y, instance.Width, instance.Height,
			instance.U0, instance.V0, instance.U1, instance.V1,
			instance.Color[0], instance.Color[1], instance.Color[2], instance.Color[3],
		}
		base := index * hotbarInstanceBytes
		for offset, value := range values {
			binary.LittleEndian.PutUint32(out[base+offset*4:], math.Float32bits(value))
		}
	}
	return out
}
