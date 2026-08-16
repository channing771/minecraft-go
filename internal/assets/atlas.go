//go:build darwin

package assets

// atlasMips 是材质 atlas 的 mip 层数;与 Rust 渲染器的 ATLAS_MIPS 必须一致。
const atlasMips = 5

// layerMipChain 生成一个 layer 的完整 mip 链(含 mip 0),
// UploadTo 与 AtlasPixels 共用,保证两个后端消费同一份字节。
func (r *Registry) layerMipChain(layer int) [][]byte {
	chain := make([][]byte, 0, atlasMips)
	px := r.LayerRGBA(layer)
	size := texSize
	chain = append(chain, px)
	for mip := 1; mip < atlasMips; mip++ {
		if layer == int(LayerLeaves) || layer == int(LayerGlass) {
			px = downsampleCutout(px, size)
		} else {
			px = downsample(px, size)
		}
		size /= 2
		chain = append(chain, px)
	}
	return chain
}

// AtlasPixels 导出与 UploadTo 写入 GPU 完全一致的逐 layer、逐 mip RGBA
// 字节流,供 Rust 渲染器上传同一份材质(材质所有权保持在 Go)。
func (r *Registry) AtlasPixels() (int, []byte) {
	var out []byte
	for layer := 0; layer < r.LayerCount(); layer++ {
		for _, px := range r.layerMipChain(layer) {
			out = append(out, px...)
		}
	}
	return r.LayerCount(), out
}

func downsampleCutout(src []byte, size int) []byte {
	dst := downsample(src, size)
	half := size / 2
	for y := 0; y < half; y++ {
		for x := 0; x < half; x++ {
			a := byte(0)
			for dy := 0; dy < 2; dy++ {
				for dx := 0; dx < 2; dx++ {
					a = max(a, src[((y*2+dy)*size+x*2+dx)*4+3])
				}
			}
			dst[(y*half+x)*4+3] = a
		}
	}
	return dst
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
