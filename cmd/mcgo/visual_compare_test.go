package main

import (
	"image"
	"testing"
)

func solidNRGBA(width, height int, r, g, b byte) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for i := 0; i < width*height; i++ {
		img.Pix[i*4+0] = r
		img.Pix[i*4+1] = g
		img.Pix[i*4+2] = b
		img.Pix[i*4+3] = 255
	}
	return img
}

func TestCompareImagesIdentical(t *testing.T) {
	a := solidNRGBA(4, 4, 10, 20, 30)
	b := solidNRGBA(4, 4, 10, 20, 30)
	diff, _, err := compareImages(a, b)
	if err != nil {
		t.Fatalf("比对失败: %v", err)
	}
	if diff.MaxChannelDelta != 0 || diff.DiffPixels != 0 {
		t.Fatalf("全等图的差异 = %+v，想要全零", diff)
	}
}

// TestCompareImagesSinglePixelSpike 覆盖"局部高差值"——接缝漏光的形态。
// 占比极小，只有 MaxChannelDelta 能抓到它。
func TestCompareImagesSinglePixelSpike(t *testing.T) {
	a := solidNRGBA(10, 10, 0, 0, 0)
	b := solidNRGBA(10, 10, 0, 0, 0)
	b.Pix[b.PixOffset(5, 5)+1] = 200 // 单个像素的 G 通道拉高
	diff, _, err := compareImages(a, b)
	if err != nil {
		t.Fatalf("比对失败: %v", err)
	}
	if diff.MaxChannelDelta != 200 {
		t.Fatalf("MaxChannelDelta = %d，想要 200", diff.MaxChannelDelta)
	}
	if diff.DiffPixels != 1 {
		t.Fatalf("DiffPixels = %d，想要 1", diff.DiffPixels)
	}
	if diff.TotalPixels != 100 {
		t.Fatalf("TotalPixels = %d，想要 100", diff.TotalPixels)
	}
}

// TestCompareImagesWideFaintShift 覆盖"大面积微差"——LSB 噪声的形态。
// 每个像素只差 1，必须能被阈值放过，否则 CI 上会变成第二个假失败源。
func TestCompareImagesWideFaintShift(t *testing.T) {
	a := solidNRGBA(10, 10, 100, 100, 100)
	b := solidNRGBA(10, 10, 101, 101, 101)
	diff, _, err := compareImages(a, b)
	if err != nil {
		t.Fatalf("比对失败: %v", err)
	}
	if diff.MaxChannelDelta != 1 {
		t.Fatalf("MaxChannelDelta = %d，想要 1", diff.MaxChannelDelta)
	}
	if !diff.withinThreshold(diffThreshold{MaxChannelDelta: 2, MaxDiffPixelRatio: 0.01}) {
		t.Fatalf("每通道差 1 应当在阈值内，实际 %+v", diff)
	}
}

func TestCompareImagesRejectsSizeMismatch(t *testing.T) {
	// 尺寸不匹配直接失败，不做缩放后比对——缩放会引入插值，
	// 把"分辨率配错了"这个真问题伪装成"有一点点色差"。
	if _, _, err := compareImages(solidNRGBA(4, 4, 0, 0, 0), solidNRGBA(8, 8, 0, 0, 0)); err == nil {
		t.Fatal("尺寸不匹配想要报错，实际通过")
	}
}

func TestDiffPixelRatioExceedsThreshold(t *testing.T) {
	a := solidNRGBA(10, 10, 0, 0, 0)
	b := solidNRGBA(10, 10, 50, 50, 50)
	diff, _, err := compareImages(a, b)
	if err != nil {
		t.Fatalf("比对失败: %v", err)
	}
	if diff.withinThreshold(diffThreshold{MaxChannelDelta: 2, MaxDiffPixelRatio: 0.01}) {
		t.Fatalf("整图差 50 应当超阈值，实际 %+v", diff)
	}
}
