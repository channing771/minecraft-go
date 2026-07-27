package core_test

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
)

func FuzzRaycastBlocks(f *testing.F) {
	f.Add(
		float32(0.5), float32(1.5), float32(2.5),
		float32(1), float32(0), float32(0), float32(6),
	)
	f.Add(
		float32(-10.25), float32(-3.5), float32(-7.75),
		float32(-1), float32(0.5), float32(-0.25), float32(32),
	)

	f.Fuzz(func(
		t *testing.T,
		ox, oy, oz, dx, dy, dz, maxDistance float32,
	) {
		values := [...]float32{ox, oy, oz, dx, dy, dz, maxDistance}
		for _, value := range values {
			if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
				t.Skip()
			}
		}
		if ox < -1024 || ox > 1024 ||
			oy < -1024 || oy > 1024 ||
			oz < -1024 || oz > 1024 ||
			maxDistance < 0.01 || maxDistance > 32 {
			t.Skip()
		}
		length := math.Hypot(math.Hypot(float64(dx), float64(dy)), float64(dz))
		if length < 1e-6 {
			t.Skip()
		}

		solid := func(p core.BlockPos) (bool, error) {
			return (p.X*31+p.Y*17+p.Z*13)&15 == 0, nil
		}
		hit, ok, err := core.RaycastBlocks(
			mgl32.Vec3{ox, oy, oz},
			mgl32.Vec3{dx, dy, dz},
			maxDistance,
			solid,
		)
		if err != nil {
			t.Fatalf("有限且有效的输入返回错误: %v", err)
		}
		if !ok {
			return
		}
		if hit.Distance < 0 || hit.Distance > maxDistance {
			t.Fatalf("命中距离 %f 不在 [0,%f]", hit.Distance, maxDistance)
		}
		occupied, err := solid(hit.Block)
		if err != nil || !occupied {
			t.Fatalf("返回的方块 %+v 不是实心", hit.Block)
		}
	})
}
