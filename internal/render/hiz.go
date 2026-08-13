package render

import (
	_ "embed"
	"math"

	"github.com/channing771/mornlea/internal/gfx"
)

//go:embed shader/hiz_copy.wgsl
var hiZCopyShader string

//go:embed shader/hiz_build.wgsl
var hiZBuildShader string

type hiZ struct {
	dev gfx.Device

	tex      gfx.Texture
	fullView gfx.TextureView
	views    []gfx.TextureView

	copyPipeline  gfx.ComputePipeline
	buildPipeline gfx.ComputePipeline
	copyUniforms  gfx.Buffer
	copyLayout    gfx.BindGroupLayout
	buildLayout   gfx.BindGroupLayout
	buildBinds    []gfx.BindGroup

	viewportW, viewportH uint32
	paddedW, paddedH     uint32
	levels               uint32
	valid                bool
}

func newHiZ(dev gfx.Device, w, h uint32) *hiZ {
	z := &hiZ{
		dev:       dev,
		viewportW: w,
		viewportH: h,
		paddedW:   nextPowerOfTwo(max(w, 1)),
		paddedH:   nextPowerOfTwo(max(h, 1)),
	}
	z.levels = uint32(bitsNeeded(max(z.paddedW, z.paddedH)))
	z.tex = dev.CreateTexture(gfx.TextureDesc{
		Label:     "hi-z pyramid",
		Width:     z.paddedW,
		Height:    z.paddedH,
		MipLevels: z.levels,
		Format:    gfx.FormatR32Float,
		Dimension: gfx.TextureDimension2D,
		Usage:     gfx.TextureUsageBinding | gfx.TextureUsageStorage,
	})
	z.fullView = z.tex.View(gfx.TextureViewDesc{Dimension: gfx.TextureViewDimension2D})
	z.views = make([]gfx.TextureView, z.levels)
	for level := uint32(0); level < z.levels; level++ {
		z.views[level] = z.tex.View(gfx.TextureViewDesc{
			BaseMipLevel:  level,
			MipLevelCount: 1,
			Dimension:     gfx.TextureViewDimension2D,
		})
	}

	z.copyUniforms = dev.CreateBuffer(gfx.BufferDesc{
		Label: "hi-z copy uniforms",
		Size:  8,
		Usage: gfx.BufferUsageUniform | gfx.BufferUsageCopyDst,
	})
	z.copyUniforms.Write(0, uint32sToBytes([]uint32{w, h}))

	z.copyLayout = gfx.BindGroupLayout{
		Label: "hi-z copy layout",
		Entries: []gfx.BindGroupLayoutEntry{
			{
				Binding: 0, Type: gfx.BindingDepthTexture,
				VisibleIn: gfx.StageCompute, ViewDimension: gfx.TextureViewDimension2D,
			},
			{
				Binding: 1, Type: gfx.BindingStorageTextureWrite,
				VisibleIn: gfx.StageCompute, ViewDimension: gfx.TextureViewDimension2D,
				StorageFormat: gfx.FormatR32Float,
			},
			{Binding: 2, Type: gfx.BindingUniformBuffer, VisibleIn: gfx.StageCompute},
		},
	}
	copyModule := dev.CreateShaderModule(hiZCopyShader)
	z.copyPipeline = dev.CreateComputePipeline(gfx.ComputePipelineDesc{
		Label:      "hi-z copy depth",
		Shader:     copyModule,
		Entry:      "cs_main",
		BindGroups: []gfx.BindGroupLayout{z.copyLayout},
	})
	copyModule.Release()

	z.buildLayout = gfx.BindGroupLayout{
		Label: "hi-z build layout",
		Entries: []gfx.BindGroupLayoutEntry{
			{
				Binding: 0, Type: gfx.BindingSampledTextureUnfilterableFloat,
				VisibleIn: gfx.StageCompute, ViewDimension: gfx.TextureViewDimension2D,
			},
			{
				Binding: 1, Type: gfx.BindingStorageTextureWrite,
				VisibleIn: gfx.StageCompute, ViewDimension: gfx.TextureViewDimension2D,
				StorageFormat: gfx.FormatR32Float,
			},
		},
	}
	buildModule := dev.CreateShaderModule(hiZBuildShader)
	z.buildPipeline = dev.CreateComputePipeline(gfx.ComputePipelineDesc{
		Label:      "hi-z reduce",
		Shader:     buildModule,
		Entry:      "cs_main",
		BindGroups: []gfx.BindGroupLayout{z.buildLayout},
	})
	buildModule.Release()

	z.buildBinds = make([]gfx.BindGroup, max(int(z.levels)-1, 0))
	for level := 1; level < int(z.levels); level++ {
		z.buildBinds[level-1] = dev.CreateBindGroup(gfx.BindGroupDesc{
			Label:  "hi-z reduce level",
			Layout: z.buildLayout,
			Entries: []gfx.BindGroupEntry{
				{Binding: 0, Texture: z.views[level-1]},
				{Binding: 1, Texture: z.views[level]},
			},
		})
	}
	return z
}

// build 在 terrain render pass 之后录制，生成供下一帧使用的金字塔。
func (z *hiZ) build(enc gfx.CommandEncoder, depth gfx.TextureView) {
	copyBind := z.dev.CreateBindGroup(gfx.BindGroupDesc{
		Label:  "hi-z depth source",
		Layout: z.copyLayout,
		Entries: []gfx.BindGroupEntry{
			{Binding: 0, Texture: depth},
			{Binding: 1, Texture: z.views[0]},
			{Binding: 2, Buffer: z.copyUniforms},
		},
	})
	pass := enc.BeginComputePass("hi-z copy pass")
	pass.SetPipeline(z.copyPipeline)
	pass.SetBindGroup(0, copyBind)
	pass.Dispatch(divCeil(z.paddedW, 8), divCeil(z.paddedH, 8), 1)
	pass.End()
	copyBind.Release()

	w, h := z.paddedW, z.paddedH
	for _, bind := range z.buildBinds {
		w = max(w/2, 1)
		h = max(h/2, 1)
		pass := enc.BeginComputePass("hi-z reduce pass")
		pass.SetPipeline(z.buildPipeline)
		pass.SetBindGroup(0, bind)
		pass.Dispatch(divCeil(w, 8), divCeil(h, 8), 1)
		pass.End()
	}
	z.valid = true
}

func (z *hiZ) Release() {
	for _, bind := range z.buildBinds {
		if bind != nil {
			bind.Release()
		}
	}
	if z.buildPipeline != nil {
		z.buildPipeline.Release()
	}
	if z.copyPipeline != nil {
		z.copyPipeline.Release()
	}
	if z.copyUniforms != nil {
		z.copyUniforms.Release()
	}
	for _, view := range z.views {
		if view != nil {
			view.Release()
		}
	}
	if z.fullView != nil {
		z.fullView.Release()
	}
	if z.tex != nil {
		z.tex.Release()
	}
}

func nextPowerOfTwo(v uint32) uint32 {
	v--
	v |= v >> 1
	v |= v >> 2
	v |= v >> 4
	v |= v >> 8
	v |= v >> 16
	return v + 1
}

func bitsNeeded(v uint32) int {
	n := 0
	for v > 0 {
		n++
		v >>= 1
	}
	return n
}

func divCeil(v, d uint32) uint32 { return (v + d - 1) / d }

// HiZLevelForTest 与 cull.wgsl 的 mip 选择公式等价。
func HiZLevelForTest(sizePx float64) float64 {
	return math.Ceil(math.Log2(math.Max(sizePx, 1)))
}

func OccludedForTest(minZ, hizDepth float32) bool { return minZ > hizDepth }

func Max4ForTest(a, b, c, d float32) float32 {
	return max(max(a, b), max(c, d))
}
