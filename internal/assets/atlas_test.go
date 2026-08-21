//go:build darwin

package assets

import (
	"testing"
)

func TestDownsampleHalvesSizeAndAveragesRGBA(t *testing.T) {
	src := []byte{
		10, 20, 30, 40, 30, 40, 50, 60,
		50, 60, 70, 80, 70, 80, 90, 100,
	}
	got := downsample(src, 2)
	want := []byte{40, 50, 60, 70}
	if len(got) != len(want) {
		t.Fatalf("2×2 降采样长度 = %d，想要 %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("通道 %d = %d，想要 %d", i, got[i], want[i])
		}
	}
}

func TestDownsampleCutoutPreservesCoverageAndRGBMean(t *testing.T) {
	src := []byte{
		10, 20, 30, 255, 30, 40, 50, 0,
		50, 60, 70, 0, 70, 80, 90, 0,
	}
	if got := downsample(src, 2); got[3] != 63 {
		t.Fatalf("普通降采样 alpha = %d，想要 63", got[3])
	}
	got := downsampleCutout(src, 2)
	want := []byte{40, 50, 60, 255}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("cutout 通道 %d = %d，想要 %d", i, got[i], want[i])
		}
	}

	transparent := downsampleCutout(make([]byte, 2*2*4), 2)
	if transparent[3] != 0 {
		t.Fatalf("全透明 cutout 降采样 alpha = %d，想要 0", transparent[3])
	}
}

// TestWheatLayersTakeTheCutoutMipPath 是**位置性**断言：小麦层必须真的走
// downsampleCutout 那条分支，而不是仅仅"存在一条 mip 链"。
//
// 只断言链长或最后一层非空是恒真的——两条分支都给得出。这里比的是同一层在两条
// 分支下的产物：麦秆细到 1 像素，盒式降采样把 alpha 平均到 64 上下，一过
// `c.a < 0.5` 的 discard 远处整片作物就消失了，而保覆盖率降采样给 255。
func TestWheatLayersTakeTheCutoutMipPath(t *testing.T) {
	r := NewRegistry()
	for layer := LayerWheat0; layer <= LayerWheat7; layer++ {
		chain := r.layerMipChain(int(layer))
		if len(chain) != atlasMips {
			t.Fatalf("小麦层 %d 的 mip 链长度 = %d，想要 %d", layer, len(chain), atlasMips)
		}
		src := r.LayerRGBA(int(layer))
		wantCutout := downsampleCutout(src, texSize)
		wantPlain := downsample(src, texSize)
		if string(chain[1]) != string(wantCutout) {
			t.Fatalf("小麦层 %d 的 mip 1 不是 downsampleCutout 的产物", layer)
		}
		// 夹具承重守卫排在真实断言之后：两条分支必须真的不同，否则上面那条
		// 断言在"随便哪条分支"下都成立。
		if string(wantCutout) == string(wantPlain) {
			t.Fatalf("小麦层 %d 的两条降采样分支产物相同，位置性断言退化为恒真", layer)
		}
	}
}

func TestCutoutMipChainKeepsOpaqueCoverage(t *testing.T) {
	r := NewRegistry()
	layers := []uint16{LayerLeaves, LayerGlass}
	for layer := LayerWheat0; layer <= LayerWheat7; layer++ {
		layers = append(layers, layer)
	}
	for _, layer := range layers {
		px, size := r.LayerRGBA(int(layer)), texSize
		for size > 1 {
			px = downsampleCutout(px, size)
			size /= 2
		}
		if px[3] != 255 {
			t.Fatalf("cutout 层 %d 的 1×1 mip alpha = %d，想要 255", layer, px[3])
		}
	}
}

func TestDownsampleMipChainEndsAtOnePixel(t *testing.T) {
	px := make([]byte, 16*16*4)
	size := 16
	for size > 1 {
		px = downsample(px, size)
		size /= 2
		if len(px) != size*size*4 {
			t.Fatalf("mip %dx%d 长度 = %d", size, size, len(px))
		}
	}
}
