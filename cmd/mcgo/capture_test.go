package main

import (
	"image"
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
