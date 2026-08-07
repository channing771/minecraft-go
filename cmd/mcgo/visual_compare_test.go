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

// variedNRGBA 创建非均匀像素值的图像，用于验证压暗逻辑依赖原图数据。
func variedNRGBA(width, height int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for i := 0; i < width*height; i++ {
		// 使用不同的 RGB 值确保压暗逻辑不能用常数混混过去。
		r := byte((i * 13) % 256)
		g := byte((i * 17) % 256)
		b := byte((i * 19) % 256)
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
	// C-2：验证 MaxChannelDelta 门能拦下单像素尖峰。
	// 占比门 0.5（> 0.01）能放过，但 delta 门 50（< 200）应该拦住。
	if diff.withinThreshold(diffThreshold{MaxChannelDelta: 50, MaxDiffPixelRatio: 0.5}) {
		t.Fatalf("单像素通道差 200 应当超阈值，实际 %+v", diff)
	}
}

// TestCompareImagesSparseFaintShift 覆盖"稀疏微差"——真实 LSB 噪声的形态。
// Task 3 实测：同机连续两次抓帧，230400 像素中仅 2 个相差 1，均在绿通道。
// 这类漂移必须被阈值放过，否则 CI 上会变成第二个假失败源。
func TestCompareImagesSparseFaintShift(t *testing.T) {
	a := solidNRGBA(100, 100, 100, 100, 100)
	b := solidNRGBA(100, 100, 100, 100, 100)
	// 10000 个像素里改 2 个，占比 0.0002，与实测量级一致。
	b.Pix[b.PixOffset(10, 10)+1] = 101
	b.Pix[b.PixOffset(70, 40)+1] = 101
	diff, _, err := compareImages(a, b)
	if err != nil {
		t.Fatalf("比对失败: %v", err)
	}
	if diff.MaxChannelDelta != 1 {
		t.Fatalf("MaxChannelDelta = %d，想要 1", diff.MaxChannelDelta)
	}
	if diff.DiffPixels != 2 {
		t.Fatalf("DiffPixels = %d，想要 2", diff.DiffPixels)
	}
	if !diff.withinThreshold(diffThreshold{MaxChannelDelta: 2, MaxDiffPixelRatio: 0.001}) {
		t.Fatalf("稀疏的每通道差 1 应当在阈值内，实际 %+v", diff)
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

// TestDiffPixelRatioGate 隔离验证占比门的拦截能力。
// I-1：delta 门宽松（255），只有占比门会失败。
func TestDiffPixelRatioGate(t *testing.T) {
	a := solidNRGBA(10, 10, 0, 0, 0)
	b := solidNRGBA(10, 10, 50, 50, 50)
	diff, _, err := compareImages(a, b)
	if err != nil {
		t.Fatalf("比对失败: %v", err)
	}
	// MaxChannelDelta=50, MaxDiffPixelRatio=0.001，只有占比（100% > 0.1%）超阈值。
	if diff.withinThreshold(diffThreshold{MaxChannelDelta: 255, MaxDiffPixelRatio: 0.001}) {
		t.Fatalf("占比 100%% 应当超阈值 0.1%%，实际 %+v", diff)
	}
}

// TestVisualizationRedMarking 验证差异可视化图正确标记红色。
// C-1：超差像素必须涂成红色 (255, 0, 0, 255)；无差异像素必须压暗成 (want/4, want/4, want/4, 255)。
func TestVisualizationRedMarking(t *testing.T) {
	a := variedNRGBA(4, 4)
	b := variedNRGBA(4, 4)
	// 在 (1, 1) 处制造单像素差异。
	b.Pix[b.PixOffset(1, 1)+0] += 10
	diff, vis, err := compareImages(a, b)
	if err != nil {
		t.Fatalf("比对失败: %v", err)
	}
	if diff.MaxChannelDelta != 10 {
		t.Fatalf("MaxChannelDelta = %d，想要 10", diff.MaxChannelDelta)
	}
	if diff.DiffPixels != 1 {
		t.Fatalf("DiffPixels = %d，想要 1", diff.DiffPixels)
	}
	// 验证 (1, 1) 处被涂成红色。
	vi := vis.PixOffset(1, 1)
	if vis.Pix[vi+0] != 255 || vis.Pix[vi+1] != 0 || vis.Pix[vi+2] != 0 || vis.Pix[vi+3] != 255 {
		t.Fatalf("差异像素 (1,1) 应该是红色 (255,0,0,255)，实际 (%d,%d,%d,%d)",
			vis.Pix[vi+0], vis.Pix[vi+1], vis.Pix[vi+2], vis.Pix[vi+3])
	}
	// 验证其他像素被压暗（依赖原图数据 want.R/4，三通道同值）。
	// I-2：用非退化像素 (1,0)（variedNRGBA 中 i=1，R=13, G=17, B=19 互不相等）
	// 来钉住除数必须是 4，且必须取 R 通道而非其他。
	vi10 := vis.PixOffset(1, 0)
	wi10 := b.PixOffset(1, 0)
	wantDim := b.Pix[wi10] / 4 // 取 R 通道，除以 4
	if vis.Pix[vi10+0] != wantDim || vis.Pix[vi10+1] != wantDim || vis.Pix[vi10+2] != wantDim || vis.Pix[vi10+3] != 255 {
		t.Fatalf("无差异像素 (1,0) 应该是压暗 (%d,%d,%d,255)，实际 (%d,%d,%d,%d)",
			wantDim, wantDim, wantDim, vis.Pix[vi10+0], vis.Pix[vi10+1], vis.Pix[vi10+2], vis.Pix[vi10+3])
	}
}
