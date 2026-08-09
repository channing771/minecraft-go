//go:build darwin

package gfx

import (
	"fmt"

	"github.com/oliverbestmann/webgpu/wgpu"
)

// ---------------------------------------------------------------------------
// Buffer
// ---------------------------------------------------------------------------

type wgpuBuffer struct {
	device *wgpuDevice
	buf    *wgpu.Buffer
}

func (b *wgpuBuffer) Size() uint64 { return b.buf.GetSize() }

func (b *wgpuBuffer) Write(offset uint64, data []byte) {
	check(b.device.queue.TryWriteBuffer(b.buf, offset, data))
}

// ReadBack 按 WebGPU 的规矩绕一次 staging buffer：MapRead 只能与 CopyDst 组合，
// 带 Storage/Indirect 的工作缓冲不能直接映射。
func (b *wgpuBuffer) ReadBack() []byte {
	size := b.buf.GetSize()
	if size == 0 {
		return nil
	}
	dev := b.device

	staging := must(dev.device.TryCreateBuffer(&wgpu.BufferDescriptor{
		Label: "gfx readback staging",
		Size:  size,
		Usage: wgpu.BufferUsageMapRead | wgpu.BufferUsageCopyDst,
	}))
	defer staging.Release()

	encoder := must(dev.device.TryCreateCommandEncoder(&wgpu.CommandEncoderDescriptor{
		Label: "gfx readback encoder",
	}))
	defer encoder.Release()
	check(encoder.TryCopyBufferToBuffer(b.buf, 0, staging, 0, size))

	cmd := must(encoder.TryFinish(nil))
	defer cmd.Release()
	dev.queue.Submit(cmd)

	// MapAsync 的回调由 Poll 驱动；Poll(true) 会一直转到队列清空，
	// 返回时 status 一定已经被写过了。
	status := wgpu.MapAsyncStatusError
	check(staging.TryMapAsync(wgpu.MapModeRead, 0, size, func(s wgpu.MapAsyncStatus) {
		status = s
	}))
	dev.device.Poll(true, nil)
	if status != wgpu.MapAsyncStatusSuccess {
		panic(fmt.Errorf("gfx: 映射 staging buffer 失败: %v", status))
	}

	// GetMappedRange 返回的是映射内存上的视图，Unmap 之后就失效，必须拷出来。
	out := make([]byte, size)
	copy(out, staging.GetMappedRange(0, uint(size)))
	check(staging.TryUnmap())
	return out
}

func (b *wgpuBuffer) Release() {
	if b.buf != nil {
		b.buf.Release()
		b.buf = nil
	}
}

// ---------------------------------------------------------------------------
// 着色器、管线、bind group、采样器：清一色的薄壳
// ---------------------------------------------------------------------------

type wgpuShaderModule struct{ module *wgpu.ShaderModule }

func (m *wgpuShaderModule) Release() {
	if m.module != nil {
		m.module.Release()
		m.module = nil
	}
}

type wgpuRenderPipeline struct{ pipeline *wgpu.RenderPipeline }

func (p *wgpuRenderPipeline) Release() {
	if p.pipeline != nil {
		p.pipeline.Release()
		p.pipeline = nil
	}
}

type wgpuComputePipeline struct{ pipeline *wgpu.ComputePipeline }

func (p *wgpuComputePipeline) Release() {
	if p.pipeline != nil {
		p.pipeline.Release()
		p.pipeline = nil
	}
}

type wgpuBindGroup struct{ group *wgpu.BindGroup }

func (g *wgpuBindGroup) Release() {
	if g.group != nil {
		g.group.Release()
		g.group = nil
	}
}

type wgpuSampler struct{ sampler *wgpu.Sampler }

func (s *wgpuSampler) Release() {
	if s.sampler != nil {
		s.sampler.Release()
		s.sampler = nil
	}
}

type wgpuTextureView struct{ view *wgpu.TextureView }

func (v *wgpuTextureView) Release() {
	if v.view != nil {
		v.view.Release()
		v.view = nil
	}
}

// ---------------------------------------------------------------------------
// Texture
// ---------------------------------------------------------------------------

type wgpuTexture struct {
	device       *wgpuDevice
	texture      *wgpu.Texture
	writeTexture func(*wgpu.TexelCopyTextureInfo, []byte, *wgpu.TexelCopyBufferLayout, *wgpu.Extent3D) error
	width        uint32
	height       uint32
	layers       uint32
	mipLevels    uint32
	format       wgpu.TextureFormat
}

func (t *wgpuTexture) View(desc TextureViewDesc) TextureView {
	mipCount := uint32(wgpu.MipLevelCountUndefined)
	if desc.MipLevelCount != 0 {
		mipCount = desc.MipLevelCount
	}
	layerCount := uint32(wgpu.ArrayLayerCountUndefined)
	if desc.ArrayLayerCount != 0 {
		layerCount = desc.ArrayLayerCount
	}
	view := must(t.texture.TryCreateView(&wgpu.TextureViewDescriptor{
		Format:          t.format,
		Dimension:       toViewDimension(desc.Dimension),
		BaseMipLevel:    desc.BaseMipLevel,
		MipLevelCount:   mipCount,
		BaseArrayLayer:  desc.BaseArrayLayer,
		ArrayLayerCount: layerCount,
		Aspect:          toAspect(desc.Aspect),
	}))
	return &wgpuTextureView{view: view}
}

func (t *wgpuTexture) WriteLayer(layer, mip uint32, pixels []byte) {
	t.WriteRegion(layer, mip, 0, 0, mipSize(t.width, mip), mipSize(t.height, mip), pixels)
}

func (t *wgpuTexture) WriteRegion(layer, mip, x, y, width, height uint32, pixels []byte) {
	if layer >= t.layers {
		panic(fmt.Errorf("gfx: layer %d 超出纹理层数 %d", layer, t.layers))
	}
	if mip >= t.mipLevels {
		panic(fmt.Errorf("gfx: mip %d 超出纹理 mip 数 %d", mip, t.mipLevels))
	}
	if width == 0 || height == 0 {
		panic("gfx: 写入 region 的宽和高必须大于零")
	}
	mipWidth := mipSize(t.width, mip)
	mipHeight := mipSize(t.height, mip)
	if width > mipWidth || x > mipWidth-width || height > mipHeight || y > mipHeight-height {
		panic(fmt.Errorf("gfx: region (%d, %d, %d, %d) 超出 mip %d 尺寸 %dx%d", x, y, width, height, mip, mipWidth, mipHeight))
	}
	bytesPerPixel := t.format.ByteSize()
	if bytesPerPixel == 0 {
		panic(fmt.Errorf("gfx: 格式 %v 的每像素字节数未知，无法写入", t.format))
	}
	want := uint64(width) * uint64(height) * uint64(bytesPerPixel)
	if uint64(len(pixels)) != want {
		panic(fmt.Errorf("gfx: region payload 大小 = %d，想要 %d", len(pixels), want))
	}
	writeTexture := t.writeTexture
	if writeTexture == nil {
		writeTexture = t.device.queue.TryWriteTexture
	}
	check(writeTexture(
		&wgpu.TexelCopyTextureInfo{
			Texture:  t.texture,
			MipLevel: mip,
			Origin:   wgpu.Origin3D{X: x, Y: y, Z: layer},
			Aspect:   wgpu.TextureAspectAll,
		},
		pixels,
		&wgpu.TexelCopyBufferLayout{
			BytesPerRow:  width * bytesPerPixel,
			RowsPerImage: height,
		},
		&wgpu.Extent3D{Width: width, Height: height, DepthOrArrayLayers: 1},
	))
}

// ReadLayer 按 WebGPU 的规矩绕一次 staging buffer，并把填充后的行距紧缩回
// 宽 × 每像素字节。CopyTextureToBuffer 要求 BytesPerRow 按
// wgpu.CopyBytesPerRowAlignment 对齐，因此除非行距恰好落在边界上，
// 否则底层缓冲每行都带尾部填充，必须逐行拷出。
func (t *wgpuTexture) ReadLayer(layer, mip uint32) []byte {
	if layer >= t.layers {
		panic(fmt.Errorf("gfx: layer %d 超出纹理层数 %d", layer, t.layers))
	}
	if mip >= t.mipLevels {
		panic(fmt.Errorf("gfx: mip %d 超出纹理 mip 数 %d", mip, t.mipLevels))
	}
	bytesPerPixel := t.format.ByteSize()
	if bytesPerPixel == 0 {
		panic(fmt.Errorf("gfx: 格式 %v 的每像素字节数未知，无法回读", t.format))
	}
	width := mipSize(t.width, mip)
	height := mipSize(t.height, mip)
	tightRow := width * bytesPerPixel
	const align = wgpu.CopyBytesPerRowAlignment
	paddedRow := (tightRow + align - 1) / align * align
	size := uint64(paddedRow) * uint64(height)

	dev := t.device
	staging := must(dev.device.TryCreateBuffer(&wgpu.BufferDescriptor{
		Label: "gfx texture readback staging",
		Size:  size,
		Usage: wgpu.BufferUsageMapRead | wgpu.BufferUsageCopyDst,
	}))
	defer staging.Release()

	encoder := must(dev.device.TryCreateCommandEncoder(&wgpu.CommandEncoderDescriptor{
		Label: "gfx texture readback encoder",
	}))
	defer encoder.Release()
	check(encoder.TryCopyTextureToBuffer(
		&wgpu.TexelCopyTextureInfo{
			Texture:  t.texture,
			MipLevel: mip,
			Origin:   wgpu.Origin3D{Z: layer},
			Aspect:   wgpu.TextureAspectAll,
		},
		&wgpu.TexelCopyBufferInfo{
			Buffer: staging,
			Layout: wgpu.TexelCopyBufferLayout{
				BytesPerRow:  paddedRow,
				RowsPerImage: height,
			},
		},
		&wgpu.Extent3D{Width: width, Height: height, DepthOrArrayLayers: 1},
	))

	cmd := must(encoder.TryFinish(nil))
	defer cmd.Release()
	dev.queue.Submit(cmd)

	// MapAsync 的回调由 Poll 驱动；Poll(true) 会一直转到队列清空。
	status := wgpu.MapAsyncStatusError
	check(staging.TryMapAsync(wgpu.MapModeRead, 0, size, func(s wgpu.MapAsyncStatus) {
		status = s
	}))
	dev.device.Poll(true, nil)
	if status != wgpu.MapAsyncStatusSuccess {
		panic(fmt.Errorf("gfx: 映射纹理 staging buffer 失败: %v", status))
	}

	// GetMappedRange 返回的是映射内存上的视图，Unmap 之后就失效，必须拷出来。
	mapped := staging.GetMappedRange(0, uint(size))
	out := make([]byte, uint64(tightRow)*uint64(height))
	for row := uint32(0); row < height; row++ {
		src := uint64(row) * uint64(paddedRow)
		dst := uint64(row) * uint64(tightRow)
		copy(out[dst:dst+uint64(tightRow)], mapped[src:src+uint64(tightRow)])
	}
	check(staging.TryUnmap())
	return out
}

func mipSize(size, mip uint32) uint32 {
	if mip >= 32 {
		return 1
	}
	return max(size>>mip, 1)
}

func (t *wgpuTexture) Release() {
	if t.texture != nil {
		t.texture.Release()
		t.texture = nil
	}
}
