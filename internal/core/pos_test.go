package core_test

import (
	"testing"

	"minecraft-go/internal/core"
)

func TestBlockPosChunkHandlesNegatives(t *testing.T) {
	cases := []struct {
		name string
		in   core.BlockPos
		want core.ChunkPos
	}{
		{"原点", core.BlockPos{X: 0, Y: 0, Z: 0}, core.ChunkPos{X: 0, Z: 0}},
		{"区块内最大", core.BlockPos{X: 15, Y: 0, Z: 15}, core.ChunkPos{X: 0, Z: 0}},
		{"跨到下一区块", core.BlockPos{X: 16, Y: 0, Z: 16}, core.ChunkPos{X: 1, Z: 1}},
		{"负一属于 -1 号区块", core.BlockPos{X: -1, Y: 0, Z: -1}, core.ChunkPos{X: -1, Z: -1}},
		{"负十六属于 -1 号区块", core.BlockPos{X: -16, Y: 0, Z: -16}, core.ChunkPos{X: -1, Z: -1}},
		{"负十七属于 -2 号区块", core.BlockPos{X: -17, Y: 0, Z: -17}, core.ChunkPos{X: -2, Z: -2}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.in.Chunk(); got != c.want {
				t.Fatalf("Chunk() = %+v，想要 %+v", got, c.want)
			}
		})
	}
}

func TestBlockPosLocalAlwaysInRange(t *testing.T) {
	for _, y := range []int32{core.MinY, -1, 0, 1, core.MaxY - 1} {
		for _, x := range []int32{-33, -17, -16, -1, 0, 15, 16, 31} {
			p := core.BlockPos{X: x, Y: y, Z: x}
			lx, ly, lz := p.Local()
			if lx < 0 || lx > 15 || ly < 0 || ly > 15 || lz < 0 || lz > 15 {
				t.Fatalf("Local() 越界: pos=%+v -> (%d,%d,%d)", p, lx, ly, lz)
			}
		}
	}
}

func TestBlockPosSectionIndexCoversWorldHeight(t *testing.T) {
	if got := (core.BlockPos{Y: core.MinY}).SectionIndex(); got != 0 {
		t.Fatalf("世界底部区段索引 = %d，想要 0", got)
	}
	if got := (core.BlockPos{Y: core.MaxY - 1}).SectionIndex(); got != core.SectionsPerChunk-1 {
		t.Fatalf("世界顶部区段索引 = %d，想要 %d", got, core.SectionsPerChunk-1)
	}
}

func TestSectionMinCornerRoundTrip(t *testing.T) {
	positions := []core.BlockPos{
		{X: 0, Y: core.MinY, Z: 0},
		{X: 15, Y: -1, Z: 15},
		{X: -1, Y: 0, Z: -17},
		{X: 31, Y: core.MaxY - 1, Z: -33},
	}
	for _, p := range positions {
		section := p.Section()
		min := section.MinCorner()
		lx, ly, lz := p.Local()
		got := core.BlockPos{
			X: min.X + int32(lx),
			Y: min.Y + int32(ly),
			Z: min.Z + int32(lz),
		}
		if got != p {
			t.Fatalf("%+v 经 Section/Local 往返后得到 %+v", p, got)
		}
	}
}
