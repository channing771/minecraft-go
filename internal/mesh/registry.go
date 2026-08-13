package mesh

import (
	"fmt"
	"slices"
	"sort"

	"github.com/channing771/mornlea/internal/world"
)

// RegistryReader 提供冻结网格 registry 所需的方块属性。
type RegistryReader interface {
	Opaque(world.BlockID) bool
	FaceVisible(id world.BlockID, adjacent world.BlockID) bool
	Material(id world.BlockID, face Face) uint16
	Emission(world.BlockID) uint8
}

// Registry 提供网格化需要的方块属性及其不可变快照。
type Registry interface {
	RegistryReader
	MeshSnapshot() RegistrySnapshot
}

// BlockProperties 是单个方块在网格化期间使用的冻结属性。
type BlockProperties struct {
	ID        world.BlockID
	Opaque    bool
	Emission  uint8
	Materials [6]uint16
}

// RegistrySnapshot 是按方块 ID 排序的网格 registry 快照。
type RegistrySnapshot struct {
	Blocks     []BlockProperties
	Visibility []uint64
}

// BuildRegistrySnapshot 复制并冻结指定方块 ID 的网格属性。
func BuildRegistrySnapshot(ids []world.BlockID, reader RegistryReader) (RegistrySnapshot, error) {
	sorted := slices.Clone(ids)
	slices.Sort(sorted)
	for i := 1; i < len(sorted); i++ {
		if sorted[i] == sorted[i-1] {
			return RegistrySnapshot{}, fmt.Errorf("mesh: 重复 block ID %d", sorted[i])
		}
	}

	blocks := make([]BlockProperties, len(sorted))
	for i, id := range sorted {
		block := BlockProperties{ID: id, Opaque: reader.Opaque(id), Emission: reader.Emission(id)}
		if block.Emission > 15 {
			return RegistrySnapshot{}, fmt.Errorf("mesh: block %d emission=%d 超过 15", id, block.Emission)
		}
		for face := Face(0); face < 6; face++ {
			block.Materials[face] = reader.Material(id, face)
		}
		blocks[i] = block
	}

	wordsPerRow := (len(sorted) + 63) / 64
	visibility := make([]uint64, len(sorted)*wordsPerRow)
	for i, id := range sorted {
		for j, adjacent := range sorted {
			if reader.FaceVisible(id, adjacent) {
				visibility[i*wordsPerRow+j/64] |= uint64(1) << (j % 64)
			}
		}
	}
	return RegistrySnapshot{Blocks: blocks, Visibility: visibility}, nil
}

// FaceVisible 返回快照中两个方块 ID 之间冻结的可见性。
func (s RegistrySnapshot) FaceVisible(id, adjacent world.BlockID) bool {
	i := sort.Search(len(s.Blocks), func(i int) bool { return s.Blocks[i].ID >= id })
	j := sort.Search(len(s.Blocks), func(i int) bool { return s.Blocks[i].ID >= adjacent })
	if i == len(s.Blocks) || s.Blocks[i].ID != id ||
		j == len(s.Blocks) || s.Blocks[j].ID != adjacent {
		return false
	}
	wordsPerRow := (len(s.Blocks) + 63) / 64
	word := i*wordsPerRow + j/64
	return word < len(s.Visibility) && s.Visibility[word]&(uint64(1)<<(j%64)) != 0
}
