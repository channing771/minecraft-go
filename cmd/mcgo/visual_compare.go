package main

import (
	"fmt"
	"image"
)

// diffThreshold 是视觉比对的双阈值。
// 刻意不做逐字节比对：sRGB 编解码、光栅化 tie-break、驱动与 GPU 型号差异
// 都会造成个位数 LSB 漂移，逐字节 golden 在共享 CI runner 上必然变成假失败源，
// 而假失败的真实代价是训练所有人无视门禁。
type diffThreshold struct {
	// MaxChannelDelta 是单个像素任一通道被视为"超差"的差值下限（含）以下算通过。
	MaxChannelDelta int
	// MaxDiffPixelRatio 是超差像素占全图的比例上限。
	MaxDiffPixelRatio float64
}

// imageDiff 是一次比对的量化结果。
type imageDiff struct {
	MaxChannelDelta int
	DiffPixels      int
	TotalPixels     int
	DiffPixelRatio  float64
}

func (d imageDiff) withinThreshold(t diffThreshold) bool {
	return d.MaxChannelDelta <= t.MaxChannelDelta && d.DiffPixelRatio <= t.MaxDiffPixelRatio
}

func (d imageDiff) String() string {
	return fmt.Sprintf("最大通道差 %d，超差像素 %d/%d（%.4f%%）",
		d.MaxChannelDelta, d.DiffPixels, d.TotalPixels, d.DiffPixelRatio*100)
}

// compareImages 比对两张同尺寸图，返回量化差异与一张差异可视化图。
// 可视化图把相同像素压暗、把超差像素画成红色，供人眼直接定位问题区域——
// 只报"差异 3.7% 超过阈值 1%"而不给图，等于让人盲修。
func compareImages(got, want *image.NRGBA) (imageDiff, *image.NRGBA, error) {
	if got.Bounds() != want.Bounds() {
		return imageDiff{}, nil, fmt.Errorf(
			"图像尺寸不匹配：实拍 %v，基线 %v", got.Bounds(), want.Bounds())
	}
	bounds := got.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	vis := image.NewNRGBA(bounds)
	result := imageDiff{TotalPixels: width * height}
	// 逐像素扫描，阈值判定留给调用方：比对器只负责测量，不负责裁决。
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			gi, wi := got.PixOffset(x, y), want.PixOffset(x, y)
			maxDelta := 0
			// 只比 RGB：alpha 在抓帧时已被恒定写成 255，比它没有信息量。
			for c := 0; c < 3; c++ {
				delta := int(got.Pix[gi+c]) - int(want.Pix[wi+c])
				if delta < 0 {
					delta = -delta
				}
				if delta > maxDelta {
					maxDelta = delta
				}
			}
			if maxDelta > result.MaxChannelDelta {
				result.MaxChannelDelta = maxDelta
			}
			vi := vis.PixOffset(x, y)
			if maxDelta > 0 {
				result.DiffPixels++
				vis.Pix[vi+0], vis.Pix[vi+1], vis.Pix[vi+2] = 255, 0, 0
			} else {
				dim := want.Pix[wi] / 4
				vis.Pix[vi+0], vis.Pix[vi+1], vis.Pix[vi+2] = dim, dim, dim
			}
			vis.Pix[vi+3] = 255
		}
	}
	if result.TotalPixels > 0 {
		result.DiffPixelRatio = float64(result.DiffPixels) / float64(result.TotalPixels)
	}
	return result, vis, nil
}
