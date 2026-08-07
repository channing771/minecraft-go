//go:build darwin

package gfx

import (
	"bytes"
	"testing"

	"github.com/oliverbestmann/webgpu/wgpu"
)

// TestTextureReadLayerRoundTripsUnalignedWidth 用非 256 对齐的行距验证紧缩逻辑。
// 100 × 4 = 400 字节/行，会被 WebGPU 填充到 512；若实现直接返回填充后的缓冲，
// 从第二行起每行都会整体错位 112 字节。
func TestTextureReadLayerRoundTripsUnalignedWidth(t *testing.T) {
	dev, err := NewHeadlessDevice()
	if err != nil {
		t.Skipf("本机无可用 GPU 适配器: %v", err)
	}
	defer dev.Release()

	const width, height = 100, 100
	tex := dev.CreateTexture(TextureDesc{
		Label:     "readback-unaligned",
		Width:     width,
		Height:    height,
		Format:    FormatRGBA8Unorm,
		Dimension: TextureDimension2D,
		Usage:     TextureUsageCopyDst | TextureUsageCopySrc,
	})
	defer tex.Release()

	want := make([]byte, width*height*4)
	for i := range want {
		// 251 是质数，与 4 和 100 均互质，保证行内与行间都不出现周期性重复，
		// 错位一行或错位若干字节都会被 bytes.Equal 抓到。
		want[i] = byte(i % 251)
	}
	tex.WriteLayer(0, 0, want)

	got := tex.ReadLayer(0, 0)
	if len(got) != len(want) {
		t.Fatalf("回读长度 = %d，想要 %d", len(got), len(want))
	}
	if bytes.Equal(got, want) {
		return
	}
	for row := 0; row < height; row++ {
		lo, hi := row*width*4, (row+1)*width*4
		if !bytes.Equal(got[lo:hi], want[lo:hi]) {
			t.Fatalf("第 %d 行不匹配：got[:8]=%v want[:8]=%v", row, got[lo:lo+8], want[lo:lo+8])
		}
	}
	t.Fatal("回读内容与写入不一致，但逐行比对未定位到差异行")
}

// TestTextureReadLayerRoundTripsAlignedWidth 覆盖行距恰好等于对齐边界的情形，
// 此时无填充，紧缩逻辑必须是恒等变换而不是少拷一行。
func TestTextureReadLayerRoundTripsAlignedWidth(t *testing.T) {
	dev, err := NewHeadlessDevice()
	if err != nil {
		t.Skipf("本机无可用 GPU 适配器: %v", err)
	}
	defer dev.Release()

	const width, height = 64, 8 // 64 × 4 = 256，恰好对齐
	tex := dev.CreateTexture(TextureDesc{
		Label:     "readback-aligned",
		Width:     width,
		Height:    height,
		Format:    FormatRGBA8Unorm,
		Dimension: TextureDimension2D,
		Usage:     TextureUsageCopyDst | TextureUsageCopySrc,
	})
	defer tex.Release()

	want := make([]byte, width*height*4)
	for i := range want {
		want[i] = byte(i % 251)
	}
	tex.WriteLayer(0, 0, want)

	if got := tex.ReadLayer(0, 0); !bytes.Equal(got, want) {
		t.Fatalf("对齐宽度回读不一致：len(got)=%d len(want)=%d", len(got), len(want))
	}
}

// TestTextureReadLayerPanicsWithStableMessages 验证越界的 layer/mip 被拒绝、
// 且不返回内容不确定的数据。越界检查发生在任何 GPU 调用之前，
// 因此不需要真实 GPU 设备，也不应该 t.Skipf。
func TestTextureReadLayerPanicsWithStableMessages(t *testing.T) {
	tex := textureForRegionValidation(1024, 1024, 1, 1, wgpu.TextureFormatR8Unorm)

	tests := []struct {
		name string
		call func()
		want string
	}{
		{
			name: "invalid layer",
			call: func() { tex.ReadLayer(1, 0) },
			want: "gfx: layer 1 超出纹理层数 1",
		},
		{
			name: "invalid mip",
			call: func() { tex.ReadLayer(0, 1) },
			want: "gfx: mip 1 超出纹理 mip 数 1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertPanic(t, tt.want, tt.call)
		})
	}
}
