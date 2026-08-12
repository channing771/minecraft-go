package main

import (
	"errors"
	"image"
	"os"
	"path/filepath"
	"testing"
)

// TestBGRAToNRGBASwapsChannels 钉住通道顺序。
// offscreen 纹理是 BGRA8UnormSrgb，PNG 要的是 RGBA；写反了图像整体偏色，
// 但结构完整，肉眼扫一眼极易放过。
func TestBGRAToNRGBASwapsChannels(t *testing.T) {
	// 单像素：B=1, G=2, R=3, A=4
	got := bgraToNRGBA([]byte{1, 2, 3, 4}, 1, 1)
	want := []byte{3, 2, 1, 255} // R=3, G=2, B=1, A 强制 255
	if len(got.Pix) != len(want) {
		t.Fatalf("Pix 长度 = %d，想要 %d", len(got.Pix), len(want))
	}
	for i := range want {
		if got.Pix[i] != want[i] {
			t.Fatalf("Pix[%d] = %d，想要 %d（完整值 %v）", i, got.Pix[i], want[i], got.Pix)
		}
	}
}

// TestBGRAToNRGBAKeepsRowOrder 用两行两列确认没有行列错位。
func TestBGRAToNRGBAKeepsRowOrder(t *testing.T) {
	pixels := []byte{
		10, 0, 0, 0, 20, 0, 0, 0, // 第 0 行：B=10, B=20
		30, 0, 0, 0, 40, 0, 0, 0, // 第 1 行：B=30, B=40
	}
	img := bgraToNRGBA(pixels, 2, 2)
	if img.Bounds() != image.Rect(0, 0, 2, 2) {
		t.Fatalf("bounds = %v，想要 2x2", img.Bounds())
	}
	for _, tc := range []struct {
		x, y  int
		wantB byte
	}{
		{0, 0, 10}, {1, 0, 20}, {0, 1, 30}, {1, 1, 40},
	} {
		offset := img.PixOffset(tc.x, tc.y)
		if got := img.Pix[offset+2]; got != tc.wantB {
			t.Fatalf("(%d,%d) 的 B = %d，想要 %d", tc.x, tc.y, got, tc.wantB)
		}
	}
}

// solidColorImage 构造一张纯色 NRGBA 图，供 golden 比对测试使用。
func solidColorImage(width, height int, r, g, b byte) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for i := 0; i < width*height; i++ {
		offset := i * 4
		img.Pix[offset+0], img.Pix[offset+1], img.Pix[offset+2], img.Pix[offset+3] = r, g, b, 255
	}
	return img
}

// TestCompareAgainstGoldenMissingGoldenErrors 钉住"golden 缺失且未传
// --update-golden 时必须报错，绝不静默创建基线"——否则第一次运行就会把
// 错误结果冻成基线，此后永远比对不出问题。
func TestCompareAgainstGoldenMissingGoldenErrors(t *testing.T) {
	goldenDir, outDir := t.TempDir(), t.TempDir()
	img := solidColorImage(2, 2, 10, 20, 30)
	if _, err := compareAgainstGolden(goldenDir, outDir, "missing", img, captureThresholds); err == nil {
		t.Fatal("golden 缺失时想要报错，实际通过")
	}
	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("golden 缺失时不应写出任何文件，实际有 %v", entries)
	}
}

// TestCompareAgainstGoldenWithinThresholdDoesNotWriteDiffFiles 覆盖阈值内的通过路径：
// compareAgainstGolden 本身不写实拍图或差异图——那些文件只在失败时才有意义。
// outDir 预先放好场景图，模拟 captureOne 在调用 compareAgainstGolden 之前
// 已经无条件写出的 <scene>.png；本测试只断言 compareAgainstGolden 不会
// 额外追加 -actual/-diff 文件，不再断言目录为空。
func TestCompareAgainstGoldenWithinThresholdDoesNotWriteDiffFiles(t *testing.T) {
	goldenDir, outDir := t.TempDir(), t.TempDir()
	golden := solidColorImage(4, 4, 100, 100, 100)
	if err := writePNG(filepath.Join(goldenDir, "scene.png"), golden); err != nil {
		t.Fatal(err)
	}
	got := solidColorImage(4, 4, 100, 100, 100)
	if err := writePNG(filepath.Join(outDir, "scene.png"), got); err != nil {
		t.Fatal(err)
	}
	diff, err := compareAgainstGolden(goldenDir, outDir, "scene", got, captureThresholds)
	if err != nil {
		t.Fatalf("全等图像想要通过，实际报错: %v", err)
	}
	if diff.DiffPixels != 0 {
		t.Fatalf("diff.DiffPixels = %d，想要 0", diff.DiffPixels)
	}
	for _, name := range []string{"scene-actual.png", "scene-diff.png"} {
		if _, statErr := os.Stat(filepath.Join(outDir, name)); !os.IsNotExist(statErr) {
			t.Fatalf("通过阈值时不应写出 %s，实际 statErr = %v", name, statErr)
		}
	}
}

// TestCompareAgainstGoldenExceedsThresholdWritesActualAndDiff 覆盖超阈值路径：
// 必须报错，且把实拍图与差异图写进 outDir——只报比例数字等于让人盲修。
func TestCompareAgainstGoldenExceedsThresholdWritesActualAndDiff(t *testing.T) {
	goldenDir, outDir := t.TempDir(), t.TempDir()
	golden := solidColorImage(4, 4, 0, 0, 0)
	if err := writePNG(filepath.Join(goldenDir, "scene.png"), golden); err != nil {
		t.Fatal(err)
	}
	got := solidColorImage(4, 4, 255, 255, 255)
	tight := diffThreshold{MaxChannelDelta: 1, MaxDiffPixelRatio: 0}
	_, err := compareAgainstGolden(goldenDir, outDir, "scene", got, tight)
	if err == nil {
		t.Fatal("超阈值时想要报错，实际通过")
	}
	for _, name := range []string{"scene-actual.png", "scene-diff.png"} {
		if _, statErr := os.Stat(filepath.Join(outDir, name)); statErr != nil {
			t.Fatalf("想要写出 %s，实际: %v", name, statErr)
		}
	}
}

// TestReadPNGRoundTripsWritePNG 钉住 readPNG 与 writePNG 的往返：
// golden 基线要靠这一对函数原样写入、原样读回，任何一端悄悄改变通道语义
// 都会让比对结果失真。
func TestReadPNGRoundTripsWritePNG(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "round-trip.png")
	want := solidColorImage(3, 2, 1, 128, 255)
	if err := writePNG(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := readPNG(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Bounds() != want.Bounds() {
		t.Fatalf("bounds = %v，想要 %v", got.Bounds(), want.Bounds())
	}
	for i := range want.Pix {
		if got.Pix[i] != want.Pix[i] {
			t.Fatalf("Pix[%d] = %d，想要 %d", i, got.Pix[i], want.Pix[i])
		}
	}
}

// TestReadPNGMissingFilePropagatesError 确认基线文件不存在时错误可被
// errors.Is(os.ErrNotExist) 识别，调用方（compareAgainstGolden）依赖这一点
// 生成"先加 --update-golden"的提示信息。
func TestReadPNGMissingFilePropagatesError(t *testing.T) {
	_, err := readPNG(filepath.Join(t.TempDir(), "missing.png"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v，想要包裹 os.ErrNotExist", err)
	}
}
