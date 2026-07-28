package core_test

import (
	"errors"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
)

func TestRaycastBlocksAxisAndNegativeCoordinates(t *testing.T) {
	solidAt := core.BlockPos{X: -3, Y: 5, Z: 2}
	hit, ok, err := core.RaycastBlocks(
		mgl32.Vec3{0.5, 5.5, 2.5},
		mgl32.Vec3{-1, 0, 0},
		6,
		func(p core.BlockPos) (bool, error) { return p == solidAt, nil },
	)
	if err != nil || !ok {
		t.Fatalf("RaycastBlocks = (%+v,%v,%v)，想要命中", hit, ok, err)
	}
	if hit.Block != solidAt || hit.Face != core.BlockFacePosX || hit.Distance != 2.5 {
		t.Fatalf("hit = %+v", hit)
	}
}

func TestRaycastBlocksIncludesExactReachEndpointAndRejectsNextFloatBeyond(t *testing.T) {
	target := core.BlockPos{X: 6, Y: 0, Z: 0}
	solid := func(position core.BlockPos) (bool, error) {
		return position == target, nil
	}

	hit, ok, err := core.RaycastBlocks(
		mgl32.Vec3{0, 0.5, 0.5},
		mgl32.Vec3{1, 0, 0},
		6,
		solid,
	)
	if err != nil || !ok || hit.Block != target || hit.Distance != 6 ||
		hit.Point != (mgl32.Vec3{6, 0.5, 0.5}) {
		t.Fatalf("exact 6.0 endpoint hit=%+v ok=%v err=%v", hit, ok, err)
	}

	nextDistance := math.Nextafter32(6, float32(math.Inf(1)))
	origin := mgl32.Vec3{6 - nextDistance, 0.5, 0.5}
	if hit, ok, err := core.RaycastBlocks(origin, mgl32.Vec3{1, 0, 0}, 6, solid); err != nil || ok {
		t.Fatalf("next-float outside endpoint hit=%+v ok=%v err=%v distance=%v",
			hit, ok, err, nextDistance)
	}
}

func TestRaycastBlocksOriginInsideSolid(t *testing.T) {
	hit, ok, err := core.RaycastBlocks(
		mgl32.Vec3{1.25, 2.5, 3.75}, mgl32.Vec3{0, 1, 0}, 6,
		func(p core.BlockPos) (bool, error) {
			return p == (core.BlockPos{X: 1, Y: 2, Z: 3}), nil
		},
	)
	if err != nil || !ok || hit.Distance != 0 || hit.Face != core.BlockFaceNone {
		t.Fatalf("起点命中 = (%+v,%v,%v)", hit, ok, err)
	}
}

func TestRaycastBlocksRejectsInvalidInputs(t *testing.T) {
	lookup := func(core.BlockPos) (bool, error) { return false, nil }
	for _, tc := range []struct {
		name string
		o, d mgl32.Vec3
		max  float32
	}{
		{"NaN origin", mgl32.Vec3{float32(math.NaN()), 0, 0}, mgl32.Vec3{1, 0, 0}, 6},
		{"infinite direction", mgl32.Vec3{}, mgl32.Vec3{float32(math.Inf(1)), 0, 0}, 6},
		{"zero direction", mgl32.Vec3{}, mgl32.Vec3{}, 6},
		{"non-positive max", mgl32.Vec3{}, mgl32.Vec3{1, 0, 0}, 0},
		{"infinite max", mgl32.Vec3{}, mgl32.Vec3{1, 0, 0}, float32(math.Inf(1))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := core.RaycastBlocks(tc.o, tc.d, tc.max, lookup); err == nil {
				t.Fatal("想要输入错误")
			}
		})
	}
}

func TestRaycastBlocksPropagatesUnavailableCell(t *testing.T) {
	want := errors.New("chunk not ready")
	_, _, err := core.RaycastBlocks(
		mgl32.Vec3{0.5, 0.5, 0.5}, mgl32.Vec3{1, 0, 0}, 6,
		func(p core.BlockPos) (bool, error) {
			if p.X == 2 {
				return false, want
			}
			return false, nil
		},
	)
	if !errors.Is(err, want) {
		t.Fatalf("err = %v，想要 %v", err, want)
	}
}

func TestRaycastBlocksUsesXYZTiePriority(t *testing.T) {
	var visited []core.BlockPos
	_, _, err := core.RaycastBlocks(
		mgl32.Vec3{0.5, 0.5, 0.5}, mgl32.Vec3{1, 1, 1}, 1,
		func(p core.BlockPos) (bool, error) {
			visited = append(visited, p)
			return false, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []core.BlockPos{
		{X: 0, Y: 0, Z: 0},
		{X: 1, Y: 0, Z: 0},
		{X: 1, Y: 1, Z: 0},
		{X: 1, Y: 1, Z: 1},
	}
	if len(visited) != len(want) {
		t.Fatalf("访问顺序 = %+v，想要 %+v", visited, want)
	}
	for i := range want {
		if visited[i] != want[i] {
			t.Fatalf("访问顺序 = %+v，想要 %+v", visited, want)
		}
	}
}
