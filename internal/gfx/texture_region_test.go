//go:build darwin

package gfx

import (
	"fmt"
	"math"
	"testing"

	"github.com/oliverbestmann/webgpu/wgpu"
)

func TestTextureWriteRegionR8(t *testing.T) {
	dev, err := NewHeadlessDevice()
	if err != nil {
		t.Skipf("本机无可用 GPU 适配器: %v", err)
	}
	defer dev.Release()

	tex := dev.CreateTexture(TextureDesc{
		Label:  "region-r8",
		Width:  1024,
		Height: 1024,
		Format: FormatR8Unorm,
		Usage:  TextureUsageCopyDst | TextureUsageBinding,
	})
	defer tex.Release()

	tex.WriteRegion(0, 0, 32, 64, 32, 32, make([]byte, 32*32))
}

func TestTextureWriteRegionRGBA8ByteCount(t *testing.T) {
	dev, err := NewHeadlessDevice()
	if err != nil {
		t.Skipf("本机无可用 GPU 适配器: %v", err)
	}
	defer dev.Release()

	tex := dev.CreateTexture(TextureDesc{
		Label:  "region-rgba8",
		Width:  2,
		Height: 3,
		Format: FormatRGBA8Unorm,
		Usage:  TextureUsageCopyDst | TextureUsageBinding,
	})
	defer tex.Release()
	tex.WriteRegion(0, 0, 0, 0, 2, 3, make([]byte, 24))
}

func TestTextureWriteRegionPanicsWithStableMessages(t *testing.T) {
	tex := textureForRegionValidation(1024, 1024, 1, 1, wgpu.TextureFormatR8Unorm)

	tests := []struct {
		name string
		call func()
		want string
	}{
		{
			name: "zero width",
			call: func() { tex.WriteRegion(0, 0, 0, 0, 0, 1, nil) },
			want: "gfx: 写入 region 的宽和高必须大于零",
		},
		{
			name: "out of bounds",
			call: func() { tex.WriteRegion(0, 0, 1023, 0, 2, 1, make([]byte, 2)) },
			want: "gfx: region (1023, 0, 2, 1) 超出 mip 0 尺寸 1024x1024",
		},
		{
			name: "wrong byte count",
			call: func() { tex.WriteRegion(0, 0, 0, 0, 32, 32, make([]byte, 1023)) },
			want: "gfx: region payload 大小 = 1023，想要 1024",
		},
		{
			name: "invalid layer",
			call: func() { tex.WriteRegion(1, 0, 0, 0, 1, 1, []byte{0}) },
			want: "gfx: layer 1 超出纹理层数 1",
		},
		{
			name: "invalid mip",
			call: func() { tex.WriteRegion(0, 1, 0, 0, 1, 1, []byte{0}) },
			want: "gfx: mip 1 超出纹理 mip 数 1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertPanic(t, tt.want, tt.call)
		})
	}
}

func TestTextureWriteRegionRejectsOverflowSafeCoordinatesAndPayload(t *testing.T) {
	tex := textureForRegionValidation(1024, 1024, 1, 1, wgpu.TextureFormatR8Unorm)
	assertPanic(t, "gfx: region (4294967295, 0, 1, 1) 超出 mip 0 尺寸 1024x1024", func() {
		tex.WriteRegion(0, 0, math.MaxUint32, 0, 1, 1, []byte{0})
	})
	assertPanic(t, "gfx: region (0, 4294967295, 1, 1) 超出 mip 0 尺寸 1024x1024", func() {
		tex.WriteRegion(0, 0, 0, math.MaxUint32, 1, 1, []byte{0})
	})

	large := textureForRegionValidation(65536, 65536, 1, 1, wgpu.TextureFormatRGBA8Unorm)
	assertPanic(t, "gfx: region payload 大小 = 0，想要 17179869184", func() {
		large.WriteRegion(0, 0, 0, 0, 65536, 65536, nil)
	})
}

func TestTextureWriteLayerDelegates(t *testing.T) {
	tests := []struct {
		name       string
		mip        uint32
		pixels     []byte
		wantLayout wgpu.TexelCopyBufferLayout
		wantExtent wgpu.Extent3D
	}{
		{
			name:       "nonzero mip",
			mip:        2,
			pixels:     make([]byte, 8),
			wantLayout: wgpu.TexelCopyBufferLayout{BytesPerRow: 8, RowsPerImage: 1},
			wantExtent: wgpu.Extent3D{Width: 2, Height: 1, DepthOrArrayLayers: 1},
		},
		{
			name:       "mip clamps to one texel",
			mip:        3,
			pixels:     make([]byte, 4),
			wantLayout: wgpu.TexelCopyBufferLayout{BytesPerRow: 4, RowsPerImage: 1},
			wantExtent: wgpu.Extent3D{Width: 1, Height: 1, DepthOrArrayLayers: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := &textureWriteRecorder{}
			tex := textureForRegionValidation(8, 4, 2, 4, wgpu.TextureFormatRGBA8Unorm)
			tex.writeTexture = recorder.write

			tex.WriteLayer(1, tt.mip, tt.pixels)

			if recorder.info.Origin != (wgpu.Origin3D{X: 0, Y: 0, Z: 1}) {
				t.Fatalf("origin = %#v, want (0, 0, 1)", recorder.info.Origin)
			}
			if recorder.info.MipLevel != tt.mip {
				t.Fatalf("mip = %d, want %d", recorder.info.MipLevel, tt.mip)
			}
			if recorder.layout != tt.wantLayout {
				t.Fatalf("layout = %#v, want %#v", recorder.layout, tt.wantLayout)
			}
			if recorder.extent != tt.wantExtent {
				t.Fatalf("extent = %#v, want %#v", recorder.extent, tt.wantExtent)
			}
			if &recorder.pixels[0] != &tt.pixels[0] {
				t.Fatal("WriteLayer copied the payload")
			}
		})
	}
}

func textureForRegionValidation(width, height, layers, mipLevels uint32, format wgpu.TextureFormat) *wgpuTexture {
	return &wgpuTexture{
		width:     width,
		height:    height,
		layers:    layers,
		mipLevels: mipLevels,
		format:    format,
	}
}

type textureWriteRecorder struct {
	info   wgpu.TexelCopyTextureInfo
	pixels []byte
	layout wgpu.TexelCopyBufferLayout
	extent wgpu.Extent3D
}

func (r *textureWriteRecorder) write(info *wgpu.TexelCopyTextureInfo, pixels []byte, layout *wgpu.TexelCopyBufferLayout, extent *wgpu.Extent3D) error {
	r.info = *info
	r.pixels = pixels
	r.layout = *layout
	r.extent = *extent
	return nil
}

func assertPanic(t *testing.T, want string, call func()) {
	t.Helper()
	defer func() {
		got := recover()
		if got == nil {
			t.Fatal("没有 panic")
		}
		if gotText := fmt.Sprint(got); gotText != want {
			t.Fatalf("panic = %q, want %q", gotText, want)
		}
	}()
	call()
}
