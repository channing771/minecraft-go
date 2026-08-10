package worldgen

import (
	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

const (
	oakTreeCellShift = 3
	oakTreeSalt      = uint64(0xA24BAED4963EE407)
)

type oakTree struct {
	Root   core.BlockPos
	Height int32
}

// oakTreeForCell 返回固定候选格中的有效橡树。
func (g *Generator) oakTreeForCell(cellX, cellZ int32) (oakTree, bool) {
	hash := oreHash(g.seed, core.BlockPos{X: cellX, Z: cellZ}, oakTreeSalt)
	if hash&1 != 0 {
		return oakTree{}, false
	}
	x := (cellX << oakTreeCellShift) + int32((hash>>1)&7)
	z := (cellZ << oakTreeCellShift) + int32((hash>>4)&7)
	height := int32(4 + (hash>>7)%3)
	surface := g.HeightAt(x, z)
	root := core.BlockPos{X: x, Y: surface + 1, Z: z}
	if g.generatedBlockAt(core.BlockPos{X: x, Y: surface, Z: z}, surface) != core.GrassID ||
		root.Y+height >= core.MaxY {
		return oakTree{}, false
	}
	for y := root.Y; y < root.Y+height; y++ {
		if g.generatedBlockAt(core.BlockPos{X: x, Y: y, Z: z}, surface) != core.AirID {
			return oakTree{}, false
		}
	}
	return oakTree{Root: root, Height: height}, true
}

// oakTreeBlockAt 返回树形在指定世界坐标的方块，树干优先于树叶。
func oakTreeBlockAt(tree oakTree, pos core.BlockPos) core.BlockID {
	if tree.Root.Y < core.MinY || tree.Root.Y+tree.Height >= core.MaxY {
		return core.AirID
	}
	topY := tree.Root.Y + tree.Height - 1
	if pos.X == tree.Root.X && pos.Z == tree.Root.Z && pos.Y >= tree.Root.Y && pos.Y <= topY {
		return core.OakLogID
	}
	dx := pos.X - tree.Root.X
	dz := pos.Z - tree.Root.Z
	switch pos.Y - topY {
	case -2, -1:
		if absInt32(dx) <= 2 && absInt32(dz) <= 2 && !(absInt32(dx) == 2 && absInt32(dz) == 2) {
			return core.LeavesID
		}
	case 0:
		if absInt32(dx) <= 1 && absInt32(dz) <= 1 {
			return core.LeavesID
		}
	case 1:
		if absInt32(dx)+absInt32(dz) <= 1 {
			return core.LeavesID
		}
	}
	return core.AirID
}

// treeBlockAt 合并所有可能覆盖目标位置的候选树，保持原木优先。
func (g *Generator) treeBlockAt(pos core.BlockPos) core.BlockID {
	leaf := false
	for cellZ := (pos.Z - 2) >> oakTreeCellShift; cellZ <= (pos.Z+2)>>oakTreeCellShift; cellZ++ {
		for cellX := (pos.X - 2) >> oakTreeCellShift; cellX <= (pos.X+2)>>oakTreeCellShift; cellX++ {
			tree, ok := g.oakTreeForCell(cellX, cellZ)
			if !ok {
				continue
			}
			switch oakTreeBlockAt(tree, pos) {
			case core.OakLogID:
				return core.OakLogID
			case core.LeavesID:
				leaf = true
			}
		}
	}
	if leaf {
		return core.LeavesID
	}
	return core.AirID
}

// applyOakTrees 把覆盖当前区块的有效候选树写入已生成的原始地形。
func (g *Generator) applyOakTrees(chunk *world.Chunk) {
	baseX := chunk.Pos.X << core.SectionShift
	baseZ := chunk.Pos.Z << core.SectionShift
	for cellZ := (baseZ - 2) >> oakTreeCellShift; cellZ <= (baseZ+core.SectionSize+1)>>oakTreeCellShift; cellZ++ {
		for cellX := (baseX - 2) >> oakTreeCellShift; cellX <= (baseX+core.SectionSize+1)>>oakTreeCellShift; cellX++ {
			tree, ok := g.oakTreeForCell(cellX, cellZ)
			if !ok {
				continue
			}
			for y := tree.Root.Y; y <= tree.Root.Y+tree.Height; y++ {
				for z := tree.Root.Z - 2; z <= tree.Root.Z+2; z++ {
					for x := tree.Root.X - 2; x <= tree.Root.X+2; x++ {
						pos := core.BlockPos{X: x, Y: y, Z: z}
						if pos.Chunk() != chunk.Pos {
							continue
						}
						block := oakTreeBlockAt(tree, pos)
						if block == core.AirID {
							continue
						}
						lx, _, lz := pos.Local()
						current := chunk.BlockAt(lx, y, lz)
						if block == core.OakLogID && (current == core.AirID || current == core.LeavesID) {
							chunk.SetBlock(lx, y, lz, block)
						}
						if block == core.LeavesID && current == core.AirID {
							chunk.SetBlock(lx, y, lz, block)
						}
					}
				}
			}
		}
	}
}

func absInt32(value int32) int32 {
	if value < 0 {
		return -value
	}
	return value
}
