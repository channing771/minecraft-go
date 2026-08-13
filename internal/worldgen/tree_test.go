package worldgen

import (
	"testing"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/world"
)

// TestOakTreeHashSelectorUsesLowBitAndHalfGate 锁定候选选择器，而非把地表
// 或树干过滤误当成 parity 拒绝。若反转 hash&1 选择器，此测试必须失败。
func TestOakTreeHashSelectorUsesLowBitAndHalfGate(t *testing.T) {
	generator := New(42)
	tests := []struct {
		name         string
		cellX, cellZ int32
		hash         uint64
		selected     bool
	}{
		{name: "odd grass", cellX: 0, cellZ: 0, hash: 0xe5176d2daaceb9fb, selected: false},
		{name: "even sand", cellX: 1, cellZ: 0, hash: 0x18658ce70a1c6fca, selected: true},
		{name: "even negative grass", cellX: -1, cellZ: -1, hash: 0x327293006fcc3648, selected: true},
		{name: "even positive grass", cellX: 2, cellZ: 2, hash: 0xddbd74fd046a28a0, selected: true},
		{name: "odd grass north", cellX: 0, cellZ: 1, hash: 0x5a6d7124c2c05c4b, selected: false},
		{name: "odd grass east", cellX: 1, cellZ: 1, hash: 0xb097452e1eeaf1d1, selected: false},
		{name: "odd negative grass", cellX: -2, cellZ: -2, hash: 0x26036128ae922e11, selected: false},
		{name: "even sand south", cellX: 0, cellZ: -1, hash: 0x55eccbec4fa392e4, selected: true},
	}
	selected := 0
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			hash := oreHash(42, core.BlockPos{X: test.cellX, Z: test.cellZ}, oakTreeSalt)
			if hash != test.hash {
				t.Fatalf("oreHash(cell=(%d,%d))=%016x，想要 %016x", test.cellX, test.cellZ, hash, test.hash)
			}
			if got := hash&1 == 0; got != test.selected {
				t.Fatalf("hash&1==0=%v，想要 %v", got, test.selected)
			}
			if test.selected {
				selected++
			}
		})
	}
	if selected != len(tests)/2 {
		t.Fatalf("%d 个固定候选中 selected=%d，想要半数 %d", len(tests), selected, len(tests)/2)
	}
	if _, spawned := generator.oakTreeForCell(0, 0); spawned {
		t.Fatal("草地上的 odd-parity 候选错误生成橡树")
	}
	if _, spawned := generator.oakTreeForCell(-1, -1); !spawned {
		t.Fatal("草地上的 even-parity 候选未生成橡树")
	}
}

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

// TestOakTreeCandidateUsesFullHalfGateAndMinimumHeight 锁定两个实际能生成的
// 草地候选。若把 50% selector 收窄为 hash&3，或删掉高度 4，此测试必须失败。
func TestOakTreeCandidateUsesFullHalfGateAndMinimumHeight(t *testing.T) {
	generator := New(42)
	tests := []struct {
		name         string
		cellX, cellZ int32
		hash         uint64
		root         core.BlockPos
		height       int32
	}{
		{
			name:  "grass candidate with low two bits 10",
			cellX: -6, cellZ: -8,
			hash: 0x4bf962227963310e,
			root: core.BlockPos{X: -41, Y: 65, Z: -64}, height: 5,
		},
		{
			name:  "grass candidate with minimum height four",
			cellX: -7, cellZ: 0,
			hash: 0x81e502aff310dc3e,
			root: core.BlockPos{X: -49, Y: 78, Z: 3}, height: 4,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := oreHash(42, core.BlockPos{X: test.cellX, Z: test.cellZ}, oakTreeSalt); got != test.hash {
				t.Fatalf("oreHash(cell=(%d,%d))=%016x，想要 %016x", test.cellX, test.cellZ, got, test.hash)
			}
			tree, spawned := generator.oakTreeForCell(test.cellX, test.cellZ)
			if !spawned {
				t.Fatalf("oakTreeForCell(%d,%d) 未生成橡树", test.cellX, test.cellZ)
			}
			if tree.Root != test.root || tree.Height != test.height {
				t.Fatalf("oakTreeForCell(%d,%d)=%+v，想要 root=%+v height=%d",
					test.cellX, test.cellZ, tree, test.root, test.height)
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

// TestApplyOakTreesKeepsIntersectingLogsIndependentOfOrder 覆盖两棵不同橡树
// 的叶/干交叉：先写哪棵树都必须得到原木。若 applyOakTrees 的原木覆盖退回
// 成“只覆盖空气”，真实区块的交叉格会保留先前树叶而失败。
func TestApplyOakTreesKeepsIntersectingLogsIndependentOfOrder(t *testing.T) {
	chunkPos := core.ChunkPos{Z: 4}
	leafTree := oakTree{Root: core.BlockPos{X: -1, Y: 79, Z: 71}, Height: 6}
	logTree := oakTree{Root: core.BlockPos{X: 0, Y: 79, Z: 72}, Height: 5}
	intersection := core.BlockPos{X: 0, Y: 82, Z: 72}
	if got := oakTreeBlockAt(leafTree, intersection); got != core.LeavesID {
		t.Fatalf("leaf tree at intersection=%d，想要 LeavesID", got)
	}
	if got := oakTreeBlockAt(logTree, intersection); got != core.OakLogID {
		t.Fatalf("log tree at intersection=%d，想要 OakLogID", got)
	}

	forward, reverse := world.NewChunk(chunkPos), world.NewChunk(chunkPos)
	applyOakTree(forward, leafTree)
	applyOakTree(forward, logTree)
	applyOakTree(reverse, logTree)
	applyOakTree(reverse, leafTree)
	assertChunksHaveSameBlocks(t, forward, reverse)
	lx, _, lz := intersection.Local()
	if got := forward.BlockAt(lx, intersection.Y, lz); got != core.OakLogID {
		t.Fatalf("forward intersection=%d，想要 OakLogID", got)
	}

	generator := New(42)
	generated := generatedChunkWithoutTrees(generator, chunkPos)
	generator.applyOakTrees(generated)
	if got := generated.BlockAt(lx, intersection.Y, lz); got != core.OakLogID {
		t.Fatalf("applyOakTrees intersection=%d，想要 OakLogID", got)
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

func assertChunksHaveSameBlocks(t *testing.T, left, right *world.Chunk) {
	t.Helper()
	for y := int32(core.MinY); y < core.MaxY; y++ {
		for z := 0; z < core.SectionSize; z++ {
			for x := 0; x < core.SectionSize; x++ {
				if got, want := left.BlockAt(x, y, z), right.BlockAt(x, y, z); got != want {
					t.Fatalf("chunk local=(%d,%d,%d)=%d，想要 %d", x, y, z, got, want)
				}
			}
		}
	}
}
