//go:build darwin

package gfx

import (
	"testing"

	"github.com/oliverbestmann/webgpu/wgpu"
)

func TestTextureFormatR8(t *testing.T) {
	if FormatR8Unorm != 7 {
		t.Fatalf("FormatR8Unorm = %d, want 7", FormatR8Unorm)
	}
	if got := toFormat(FormatR8Unorm); got != wgpu.TextureFormatR8Unorm {
		t.Fatalf("toFormat(FormatR8Unorm) = %v, want %v", got, wgpu.TextureFormatR8Unorm)
	}
	if got, ok := fromFormat(wgpu.TextureFormatR8Unorm); !ok || got != FormatR8Unorm {
		t.Fatalf("fromFormat(TextureFormatR8Unorm) = (%v, %t), want (%v, true)", got, ok, FormatR8Unorm)
	}
}

func TestBlendModeAlpha(t *testing.T) {
	got := toBlendState(BlendAlpha)
	if got.Color != (wgpu.BlendComponent{
		Operation: wgpu.BlendOperationAdd,
		SrcFactor: wgpu.BlendFactorSrcAlpha,
		DstFactor: wgpu.BlendFactorOneMinusSrcAlpha,
	}) {
		t.Fatalf("alpha color blend = %#v", got.Color)
	}
	if got.Alpha != (wgpu.BlendComponent{
		Operation: wgpu.BlendOperationAdd,
		SrcFactor: wgpu.BlendFactorOne,
		DstFactor: wgpu.BlendFactorOneMinusSrcAlpha,
	}) {
		t.Fatalf("alpha alpha blend = %#v", got.Alpha)
	}
}

func TestBlendModeReplace(t *testing.T) {
	if got := toBlendState(BlendReplace); got != wgpu.BlendStateReplace {
		t.Fatalf("replace blend = %#v, want %#v", got, wgpu.BlendStateReplace)
	}
}

func TestDepthCompareDefaultsToLessAndSupportsLessEqual(t *testing.T) {
	if got := toDepthCompare(false); got != wgpu.CompareFunctionLess {
		t.Fatalf("默认 depth compare = %v，想要 %v", got, wgpu.CompareFunctionLess)
	}
	if got := toDepthCompare(true); got != wgpu.CompareFunctionLessEqual {
		t.Fatalf("LessEqual depth compare = %v，想要 %v", got, wgpu.CompareFunctionLessEqual)
	}
}
