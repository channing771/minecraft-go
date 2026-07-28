package storage_test

import (
	"math"
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/storage"
)

func TestRegionForUsesFloorDivision(t *testing.T) {
	tests := []struct {
		chunk, region, local int32
	}{
		{-33, -2, 31}, {-32, -1, 0}, {-31, -1, 1}, {-1, -1, 31},
		{0, 0, 0}, {1, 0, 1}, {31, 0, 31}, {32, 1, 0}, {33, 1, 1},
	}
	for _, tc := range tests {
		region, slot := storage.RegionFor(core.ChunkKey{
			Dimension: core.Overworld,
			Pos:       core.ChunkPos{X: tc.chunk, Z: tc.chunk},
		})
		if region.X != tc.region || region.Z != tc.region ||
			slot != int(tc.local*32+tc.local) {
			t.Fatalf("chunk=%d -> region=%+v slot=%d", tc.chunk, region, slot)
		}
	}
}

func TestRegionForHandlesMinInt32(t *testing.T) {
	region, slot := storage.RegionFor(core.ChunkKey{
		Dimension: core.Overworld,
		Pos:       core.ChunkPos{X: math.MinInt32, Z: math.MinInt32},
	})
	if region.X != -67_108_864 || region.Z != -67_108_864 || slot != 0 {
		t.Fatalf("MinInt32 -> region=%+v slot=%d", region, slot)
	}
}
