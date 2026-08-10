package worldgen

import (
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

func TestOakTreeCandidateIsStable(t *testing.T) {
	generator := New(42)
	tests := []struct {
		name         string
		cellX, cellZ int32
		spawn        bool
		rootX, rootZ int32
		height       int32
	}{
		{name: "non-spawning origin", cellX: 0, cellZ: 0, spawn: false},
		{name: "sand surface candidate", cellX: 1, cellZ: 0, spawn: false},
		{name: "negative grass candidate", cellX: -1, cellZ: -1, spawn: true, rootX: -4, rootZ: -4, height: 5},
		{name: "positive grass candidate", cellX: 2, cellZ: 2, spawn: true, rootX: 16, rootZ: 18, height: 6},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tree, spawned := generator.oakTreeForCell(test.cellX, test.cellZ)
			if spawned != test.spawn {
				t.Fatalf("oakTreeForCell(%d,%d) spawned=%v，想要 %v", test.cellX, test.cellZ, spawned, test.spawn)
			}
			if !spawned {
				return
			}
			if tree.Root.X != test.rootX || tree.Root.Z != test.rootZ || tree.Height != test.height {
				t.Fatalf("oakTreeForCell(%d,%d)=%+v，想要 root=(%d,*,%d) height=%d", test.cellX, test.cellZ, tree, test.rootX, test.rootZ, test.height)
			}
			if tree.Root.Y != generator.HeightAt(test.rootX, test.rootZ)+1 {
				t.Fatalf("root Y=%d，想要地表上方 %d", tree.Root.Y, generator.HeightAt(test.rootX, test.rootZ)+1)
			}
		})
	}
}

func TestOakTreeBlockAtUsesFixedCrownAndLogPriority(t *testing.T) {
	tree := oakTree{Root: core.BlockPos{X: 0, Y: 65, Z: 0}, Height: 5}
	for y := int32(65); y <= 69; y++ {
		if got := oakTreeBlockAt(tree, core.BlockPos{Y: y}); got != core.OakLogID {
			t.Fatalf("trunk y=%d is %d，想要 OakLogID", y, got)
		}
	}

	for _, y := range []int32{67, 68} {
		occupied := 0
		for z := int32(-2); z <= 2; z++ {
			for x := int32(-2); x <= 2; x++ {
				got := oakTreeBlockAt(tree, core.BlockPos{X: x, Y: y, Z: z})
				if x == 0 && z == 0 {
					if got != core.OakLogID {
						t.Fatalf("crown center y=%d is %d，想要 OakLogID", y, got)
					}
					occupied++
					continue
				}
				if (x == -2 || x == 2) && (z == -2 || z == 2) {
					if got != core.AirID {
						t.Fatalf("crown corner (%d,%d,%d)=%d，想要空气", x, y, z, got)
					}
					continue
				}
				if got != core.LeavesID {
					t.Fatalf("crown leaf (%d,%d,%d)=%d，想要 LeavesID", x, y, z, got)
				}
				occupied++
			}
		}
		if occupied != 21 {
			t.Fatalf("crown y=%d occupied=%d，想要 21", y, occupied)
		}
	}

	for z := int32(-1); z <= 1; z++ {
		for x := int32(-1); x <= 1; x++ {
			want := core.LeavesID
			if x == 0 && z == 0 {
				want = core.OakLogID
			}
			if got := oakTreeBlockAt(tree, core.BlockPos{X: x, Y: 69, Z: z}); got != want {
				t.Fatalf("top (%d,69,%d)=%d，想要 %d", x, z, got, want)
			}
		}
	}
	for _, position := range []core.BlockPos{
		{X: 0, Y: 70, Z: 0}, {X: 1, Y: 70, Z: 0}, {X: -1, Y: 70, Z: 0}, {X: 0, Y: 70, Z: 1}, {X: 0, Y: 70, Z: -1},
	} {
		if got := oakTreeBlockAt(tree, position); got != core.LeavesID {
			t.Fatalf("top cross %+v=%d，想要 LeavesID", position, got)
		}
	}
	if got := oakTreeBlockAt(tree, core.BlockPos{X: 1, Y: 70, Z: 1}); got != core.AirID {
		t.Fatalf("top corner=%d，想要空气", got)
	}
	if got := oakTreeBlockAt(oakTree{Root: core.BlockPos{Y: core.MaxY - 4}, Height: 4}, core.BlockPos{Y: core.MaxY - 1}); got != core.AirID {
		t.Fatalf("world upper bound tree=%d，想要整棵树缺失", got)
	}
}

func TestApplyOakTreesPreservesSolidLeafTargets(t *testing.T) {
	generator := New(42)
	chunk := generatedChunkWithoutTrees(generator, core.ChunkPos{X: -1, Z: -1})
	tree, ok := generator.oakTreeForCell(-1, -1)
	if !ok {
		t.Fatal("seed 42 cell (-1,-1) 应有橡树")
	}
	leaf := core.BlockPos{X: tree.Root.X + 2, Y: tree.Root.Y + tree.Height - 3, Z: tree.Root.Z}
	lx, _, lz := leaf.Local()
	chunk.SetBlock(lx, leaf.Y, lz, core.StoneID)
	generator.applyOakTrees(chunk)
	if got := chunk.BlockAt(lx, leaf.Y, lz); got != core.StoneID {
		t.Fatalf("solid leaf target=%d，想要 StoneID", got)
	}
	rx, _, rz := tree.Root.Local()
	if got := chunk.BlockAt(rx, tree.Root.Y, rz); got != core.OakLogID {
		t.Fatalf("tree root=%d，想要 OakLogID", got)
	}
}

func TestBaseBlockAtMatchesGeneratedChunkWithOakTrees(t *testing.T) {
	generator := New(42)
	for _, chunkPos := range []core.ChunkPos{{X: -1, Z: -1}, {X: 0, Z: -1}, {X: 0, Z: 0}, {X: 1, Z: 0}, {X: 0, Z: 1}, {X: 1, Z: 1}} {
		chunk := generator.GenerateChunk(chunkPos)
		baseX := chunkPos.X << core.SectionShift
		baseZ := chunkPos.Z << core.SectionShift
		for z := int32(0); z < core.SectionSize; z++ {
			for x := int32(0); x < core.SectionSize; x++ {
				for y := int32(core.MinY); y < core.MaxY; y++ {
					position := core.BlockPos{X: baseX + x, Y: y, Z: baseZ + z}
					if got, want := generator.BaseBlockAt(position), chunk.BlockAt(int(x), y, int(z)); got != want {
						t.Fatalf("chunk=%+v BaseBlockAt(%+v)=%d，GenerateChunk=%d", chunkPos, position, got, want)
					}
				}
			}
		}
	}
}

func generatedChunkWithoutTrees(generator *Generator, position core.ChunkPos) *world.Chunk {
	chunk := world.NewChunk(position)
	baseX := position.X << core.SectionShift
	baseZ := position.Z << core.SectionShift
	for z := 0; z < core.SectionSize; z++ {
		for x := 0; x < core.SectionSize; x++ {
			height := generator.HeightAt(baseX+int32(x), baseZ+int32(z))
			for y := int32(core.MinY); y <= height; y++ {
				chunk.SetBlock(x, y, z, generator.generatedBlockAt(core.BlockPos{X: baseX + int32(x), Y: y, Z: baseZ + int32(z)}, height))
			}
		}
	}
	return chunk
}
