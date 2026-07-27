package assets

import "minecraft-go/internal/gfx"

// UploadTo 把全部材质层建成一张带 mipmap 的 2D 数组纹理。
func (r *Registry) UploadTo(dev gfx.Device) (gfx.Texture, gfx.Sampler) {
	const mips = 5

	tex := dev.CreateTexture(gfx.TextureDesc{
		Label:     "block-textures",
		Width:     texSize,
		Height:    texSize,
		Layers:    uint32(r.LayerCount()),
		MipLevels: mips,
		Format:    gfx.FormatRGBA8Unorm,
		Dimension: gfx.TextureDimension2DArray,
		Usage:     gfx.TextureUsageBinding | gfx.TextureUsageCopyDst,
	})

	for layer := 0; layer < r.LayerCount(); layer++ {
		px := r.LayerRGBA(layer)
		size := texSize
		tex.WriteLayer(uint32(layer), 0, px)
		for mip := 1; mip < mips; mip++ {
			px = downsample(px, size)
			size /= 2
			tex.WriteLayer(uint32(layer), uint32(mip), px)
		}
	}

	smp := dev.CreateSampler(gfx.SamplerDesc{
		Label:     "block-sampler",
		MagFilter: gfx.FilterNearest,
		MinFilter: gfx.FilterLinear,
		MipFilter: gfx.FilterLinear,
	})
	return tex, smp
}

func downsample(src []byte, size int) []byte {
	half := size / 2
	dst := make([]byte, half*half*4)
	for y := 0; y < half; y++ {
		for x := 0; x < half; x++ {
			for c := 0; c < 4; c++ {
				sum := int(src[((y*2)*size+x*2)*4+c]) +
					int(src[((y*2)*size+x*2+1)*4+c]) +
					int(src[((y*2+1)*size+x*2)*4+c]) +
					int(src[((y*2+1)*size+x*2+1)*4+c])
				dst[(y*half+x)*4+c] = byte(sum / 4)
			}
		}
	}
	return dst
}
