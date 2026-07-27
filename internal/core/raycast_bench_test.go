package core_test

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
)

func BenchmarkRaycastBlocks(b *testing.B) {
	var fixture [16][16][16]bool
	for i := 0; i < 16; i += 3 {
		fixture[i][(i*5)&15][(i*11)&15] = true
	}
	solid := func(p core.BlockPos) (bool, error) {
		if p.X < 0 || p.X >= 16 ||
			p.Y < 0 || p.Y >= 16 ||
			p.Z < 0 || p.Z >= 16 {
			return false, nil
		}
		return fixture[p.X][p.Y][p.Z], nil
	}
	origin := mgl32.Vec3{0.5, 0.5, 0.5}
	direction := mgl32.Vec3{1, 0.73, 0.41}

	b.ReportAllocs()
	for b.Loop() {
		_, _, err := core.RaycastBlocks(origin, direction, 32, solid)
		if err != nil {
			b.Fatal(err)
		}
	}
}
