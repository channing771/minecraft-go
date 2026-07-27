package render

import (
	"testing"

	"minecraft-go/internal/gfx"
)

func TestHiZPadsNonPowerOfTwoAndBuildsOnGPU(t *testing.T) {
	dev, err := gfx.NewHeadlessDevice()
	if err != nil {
		t.Skipf("本机无可用 GPU 适配器: %v", err)
	}
	defer dev.Release()

	z := newHiZ(dev, 13, 7)
	defer z.Release()
	if z.paddedW != 16 || z.paddedH != 8 {
		t.Fatalf("13×7 padding = %d×%d，想要 16×8", z.paddedW, z.paddedH)
	}
	if z.levels != 5 {
		t.Fatalf("16×8 金字塔级数 = %d，想要 5（16,8,4,2,1）", z.levels)
	}

	depth := dev.CreateTexture(gfx.TextureDesc{
		Label: "hi-z test depth",
		Width: 13, Height: 7,
		Format:    gfx.FormatDepth32Float,
		Dimension: gfx.TextureDimension2D,
		Usage:     gfx.TextureUsageRenderTarget | gfx.TextureUsageBinding,
	})
	defer depth.Release()
	view := depth.View(gfx.TextureViewDesc{
		Dimension: gfx.TextureViewDimension2D,
		Aspect:    gfx.AspectDepthOnly,
	})
	defer view.Release()

	enc := dev.CreateCommandEncoder()
	z.build(enc, view)
	cmd := enc.Finish()
	dev.Submit(cmd)
	cmd.Release()
	dev.Poll(true)
	if !z.valid {
		t.Fatal("成功构建后 Hi-Z 应标记为 valid")
	}
}
