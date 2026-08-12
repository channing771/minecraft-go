//go:build darwin

package gfx

import (
	"fmt"

	"github.com/oliverbestmann/webgpu/wgpu"
)

// ---------------------------------------------------------------------------
// 枚举映射：gfx 的中立枚举 → 绑定的枚举
// ---------------------------------------------------------------------------

func toBufferUsage(u BufferUsage) wgpu.BufferUsage {
	var out wgpu.BufferUsage
	for gfxBit, wgpuBit := range map[BufferUsage]wgpu.BufferUsage{
		BufferUsageVertex:   wgpu.BufferUsageVertex,
		BufferUsageIndex:    wgpu.BufferUsageIndex,
		BufferUsageUniform:  wgpu.BufferUsageUniform,
		BufferUsageStorage:  wgpu.BufferUsageStorage,
		BufferUsageIndirect: wgpu.BufferUsageIndirect,
		BufferUsageCopySrc:  wgpu.BufferUsageCopySrc,
		BufferUsageCopyDst:  wgpu.BufferUsageCopyDst,
		BufferUsageMapRead:  wgpu.BufferUsageMapRead,
	} {
		if u&gfxBit != 0 {
			out |= wgpuBit
		}
	}
	return out
}

func toTextureUsage(u TextureUsage) wgpu.TextureUsage {
	var out wgpu.TextureUsage
	for gfxBit, wgpuBit := range map[TextureUsage]wgpu.TextureUsage{
		TextureUsageBinding:      wgpu.TextureUsageTextureBinding,
		TextureUsageRenderTarget: wgpu.TextureUsageRenderAttachment,
		TextureUsageCopyDst:      wgpu.TextureUsageCopyDst,
		TextureUsageStorage:      wgpu.TextureUsageStorageBinding,
		TextureUsageCopySrc:      wgpu.TextureUsageCopySrc,
	} {
		if u&gfxBit != 0 {
			out |= wgpuBit
		}
	}
	return out
}

func toShaderStage(s ShaderStage) wgpu.ShaderStage {
	var out wgpu.ShaderStage
	if s&StageVertex != 0 {
		out |= wgpu.ShaderStageVertex
	}
	if s&StageFragment != 0 {
		out |= wgpu.ShaderStageFragment
	}
	if s&StageCompute != 0 {
		out |= wgpu.ShaderStageCompute
	}
	return out
}

func toFormat(f TextureFormat) wgpu.TextureFormat {
	switch f {
	case FormatUndefined:
		return wgpu.TextureFormatUndefined
	case FormatBGRA8Unorm:
		return wgpu.TextureFormatBGRA8Unorm
	case FormatBGRA8UnormSrgb:
		return wgpu.TextureFormatBGRA8UnormSrgb
	case FormatRGBA8Unorm:
		return wgpu.TextureFormatRGBA8Unorm
	case FormatDepth32Float:
		return wgpu.TextureFormatDepth32Float
	case FormatR32Float:
		return wgpu.TextureFormatR32Float
	case FormatR32Uint:
		return wgpu.TextureFormatR32Uint
	case FormatR8Unorm:
		return wgpu.TextureFormatR8Unorm
	}
	panic(fmt.Errorf("gfx: 未知的纹理格式 %d", f))
}

// fromFormat 是 toFormat 的反向映射，只覆盖 gfx 认识的格式。
// 用于把 surface 报回来的格式翻译成中立枚举。
func fromFormat(f wgpu.TextureFormat) (TextureFormat, bool) {
	switch f {
	case wgpu.TextureFormatBGRA8Unorm:
		return FormatBGRA8Unorm, true
	case wgpu.TextureFormatBGRA8UnormSrgb:
		return FormatBGRA8UnormSrgb, true
	case wgpu.TextureFormatRGBA8Unorm:
		return FormatRGBA8Unorm, true
	case wgpu.TextureFormatDepth32Float:
		return FormatDepth32Float, true
	case wgpu.TextureFormatR32Float:
		return FormatR32Float, true
	case wgpu.TextureFormatR32Uint:
		return FormatR32Uint, true
	case wgpu.TextureFormatR8Unorm:
		return FormatR8Unorm, true
	}
	return FormatUndefined, false
}

// toBlendState 把 gfx 混合模式映射成 WebGPU 的颜色附件状态。
func toBlendState(mode BlendMode) wgpu.BlendState {
	switch mode {
	case BlendReplace:
		return wgpu.BlendStateReplace
	case BlendAlpha:
		return wgpu.BlendState{
			Color: wgpu.BlendComponent{
				Operation: wgpu.BlendOperationAdd,
				SrcFactor: wgpu.BlendFactorSrcAlpha,
				DstFactor: wgpu.BlendFactorOneMinusSrcAlpha,
			},
			Alpha: wgpu.BlendComponent{
				Operation: wgpu.BlendOperationAdd,
				SrcFactor: wgpu.BlendFactorOne,
				DstFactor: wgpu.BlendFactorOneMinusSrcAlpha,
			},
		}
	}
	panic(fmt.Errorf("gfx: 未知的混合模式 %d", mode))
}

func toDepthCompare(lessEqual bool) wgpu.CompareFunction {
	if lessEqual {
		return wgpu.CompareFunctionLessEqual
	}
	return wgpu.CompareFunctionLess
}

func toViewDimension(d TextureViewDimension) wgpu.TextureViewDimension {
	switch d {
	case TextureViewDimensionAuto:
		return wgpu.TextureViewDimensionUndefined
	case TextureViewDimension2D:
		return wgpu.TextureViewDimension2D
	case TextureViewDimension2DArray:
		return wgpu.TextureViewDimension2DArray
	}
	panic(fmt.Errorf("gfx: 未知的视图维度 %d", d))
}

func toAspect(a TextureAspect) wgpu.TextureAspect {
	if a == AspectDepthOnly {
		return wgpu.TextureAspectDepthOnly
	}
	return wgpu.TextureAspectAll
}

func toFilter(f FilterMode) wgpu.FilterMode {
	if f == FilterLinear {
		return wgpu.FilterModeLinear
	}
	return wgpu.FilterModeNearest
}

func toMipFilter(f FilterMode) wgpu.MipmapFilterMode {
	if f == FilterLinear {
		return wgpu.MipmapFilterModeLinear
	}
	return wgpu.MipmapFilterModeNearest
}

func toAddressMode(m AddressMode) wgpu.AddressMode {
	if m == AddressRepeat {
		return wgpu.AddressModeRepeat
	}
	return wgpu.AddressModeClampToEdge
}

func toVertexFormat(f VertexFormat) wgpu.VertexFormat {
	switch f {
	case VertexFormatUint32x2:
		return wgpu.VertexFormatUint32x2
	case VertexFormatFloat32x3:
		return wgpu.VertexFormatFloat32x3
	}
	panic(fmt.Errorf("gfx: 未知的顶点格式 %d", f))
}

func toLoadOp(clear bool) wgpu.LoadOp {
	if clear {
		return wgpu.LoadOpClear
	}
	return wgpu.LoadOpLoad
}
