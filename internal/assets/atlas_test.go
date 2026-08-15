//go:build darwin

package assets

import (
	"testing"

	"github.com/channing771/mornlea/internal/gfx"
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

func TestCutoutMipChainKeepsOpaqueCoverage(t *testing.T) {
	r := NewRegistry()
	for _, layer := range []uint16{LayerLeaves, LayerGlass} {
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

func TestUploadToHeadlessGPU(t *testing.T) {
	dev, err := gfx.NewHeadlessDevice()
	if err != nil {
		t.Skipf("本机无可用 GPU 适配器: %v", err)
	}
	defer dev.Release()

	tex, sampler := NewRegistry().UploadTo(dev)
	defer tex.Release()
	defer sampler.Release()

	view := tex.View(gfx.TextureViewDesc{Dimension: gfx.TextureViewDimension2DArray})
	view.Release()
	dev.Poll(true)
}
