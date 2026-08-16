package render

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
)

func TestBuildBlockOutlinePartsMakesTwelveExpandedEdges(t *testing.T) {
	position := core.BlockPos{X: 4, Y: 5, Z: 6}
	parts := buildBlockOutlineParts(nil, position)
	if len(parts) != 12 {
		t.Fatalf("轮廓实例数 = %d，想要 12", len(parts))
	}

	var longAxes [3]int
	wantColor := [4]float32{1, 1, 1, 0.86}
	for index, part := range parts {
		bounds := transformedUnitCubeBounds(part.transform)
		size := bounds.max.Sub(bounds.min)
		longAxis := -1
		for axis := range 3 {
			switch {
			case outlineFloatNear(size[axis], 1.006):
				if longAxis != -1 {
					t.Fatalf("实例 %d 有多个长轴: %v", index, size)
				}
				longAxis = axis
			case !outlineFloatNear(size[axis], 0.018):
				t.Fatalf("实例 %d 尺寸 = %v，想要一轴 1.006、两轴 0.018", index, size)
			}
		}
		if longAxis == -1 {
			t.Fatalf("实例 %d 没有 1.006 长轴: %v", index, size)
		}
		longAxes[longAxis]++
		if part.color != wantColor {
			t.Fatalf("实例 %d 颜色 = %v，想要 %v", index, part.color, wantColor)
		}
	}
	if longAxes != [3]int{4, 4, 4} {
		t.Fatalf("X/Y/Z 长边数 = %v，想要 [4 4 4]", longAxes)
	}

	bounds := avatarPartsBounds(parts)
	assertVec3Near(t, bounds.min, mgl32.Vec3{3.997, 4.997, 5.997})
	assertVec3Near(t, bounds.max, mgl32.Vec3{5.003, 6.003, 7.003})
}

func outlineFloatNear(got, want float32) bool {
	return math.Abs(float64(got-want)) <= 1e-5
}
