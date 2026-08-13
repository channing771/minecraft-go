package render_test

import (
	"testing"

	"github.com/channing771/mornlea/internal/render"
)

func TestHiZLevelSelectionIsConservative(t *testing.T) {
	cases := []struct{ px, wantLevel float64 }{
		{1, 0}, {2, 1}, {3, 2}, {4, 2}, {5, 3}, {8, 3}, {9, 4},
	}
	for _, c := range cases {
		got := render.HiZLevelForTest(c.px)
		if got < c.wantLevel {
			t.Fatalf("包围盒 %g 像素选了 mip %g，至少需要 %g", c.px, got, c.wantLevel)
		}
	}
}

func TestNothingIsCulledWhenDepthIsFar(t *testing.T) {
	for _, minZ := range []float32{0, 0.5, 0.999, 1.0} {
		if render.OccludedForTest(minZ, 1.0) {
			t.Fatalf("min_z=%g、深度=1.0 时不应判为被遮挡", minZ)
		}
	}
	if !render.OccludedForTest(0.9, 0.5) {
		t.Fatal("min_z=0.9 比记录深度 0.5 更远，应判为被遮挡")
	}
}

func TestHiZUsesAllCoveredTexels(t *testing.T) {
	depth := render.Max4ForTest(0.2, 0.3, 1.0, 0.4)
	if depth != 1.0 {
		t.Fatalf("四纹素最大深度 = %g，想要 1.0", depth)
	}
	if render.OccludedForTest(0.9, depth) {
		t.Fatal("覆盖范围内存在深度 1.0 时不得错误剔除")
	}
}
