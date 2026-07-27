package world

import (
	"errors"
	"fmt"

	"minecraft-go/internal/core"
)

// StorageKind 是可传输调色板快照的存储形态。
type StorageKind uint8

const (
	StorageSingle StorageKind = iota
	StorageIndexed
	StorageDirect
)

// ContainerSnapshot 是 PalettedContainer 的独立、可验证值快照。
type ContainerSnapshot struct {
	Kind    StorageKind
	Single  core.BlockID
	Bits    uint8
	Palette []core.BlockID
	Packed  []uint64
}

// Snapshot 返回不与容器共享切片的压缩快照。
func (c *PalettedContainer) Snapshot() ContainerSnapshot {
	snapshot := ContainerSnapshot{}
	switch c.kind {
	case kindSingle:
		snapshot.Kind = StorageSingle
		snapshot.Single = c.single
	case kindIndexed:
		snapshot.Kind = StorageIndexed
		snapshot.Bits = c.bits
		snapshot.Palette = cloneBlockIDs(c.palette)
		snapshot.Packed = cloneUint64s(c.data)
	case kindDirect:
		snapshot.Kind = StorageDirect
		snapshot.Bits = c.bits
		snapshot.Packed = cloneUint64s(c.data)
	default:
		panic("world: invalid paletted container storage")
	}
	return snapshot
}

// NewPalettedContainerFromSnapshot 验证并导入一个独立快照。
func NewPalettedContainerFromSnapshot(
	snapshot ContainerSnapshot,
) (*PalettedContainer, error) {
	switch snapshot.Kind {
	case StorageSingle:
		if snapshot.Bits != 0 || len(snapshot.Palette) != 0 || len(snapshot.Packed) != 0 {
			return nil, errors.New("world: single snapshot has compressed payload")
		}
		if !validSnapshotBlockID(snapshot.Single) {
			return nil, fmt.Errorf("world: single block ID %d exceeds 15 bits", snapshot.Single)
		}
		return &PalettedContainer{
			kind:   kindSingle,
			single: snapshot.Single,
		}, nil

	case StorageIndexed:
		if snapshot.Bits != 4 && snapshot.Bits != 8 {
			return nil, fmt.Errorf("world: indexed snapshot has invalid bits %d", snapshot.Bits)
		}
		if snapshot.Single != 0 {
			return nil, errors.New("world: indexed snapshot has single value")
		}
		if len(snapshot.Palette) == 0 || len(snapshot.Palette) > 1<<snapshot.Bits {
			return nil, fmt.Errorf(
				"world: indexed palette length %d is invalid for %d bits",
				len(snapshot.Palette),
				snapshot.Bits,
			)
		}
		if len(snapshot.Packed) != wordsFor(snapshot.Bits) {
			return nil, fmt.Errorf(
				"world: indexed packed length %d, want %d",
				len(snapshot.Packed),
				wordsFor(snapshot.Bits),
			)
		}

		lookup := make(map[BlockID]uint32, len(snapshot.Palette))
		for index, id := range snapshot.Palette {
			if !validSnapshotBlockID(id) {
				return nil, fmt.Errorf("world: palette block ID %d exceeds 15 bits", id)
			}
			if _, duplicate := lookup[id]; duplicate {
				return nil, fmt.Errorf("world: duplicate palette block ID %d", id)
			}
			lookup[id] = uint32(index)
		}
		for index := 0; index < core.BlocksPerSection; index++ {
			slot := readPacked(snapshot.Packed, snapshot.Bits, index)
			if slot >= uint32(len(snapshot.Palette)) {
				return nil, fmt.Errorf(
					"world: palette slot %d at block %d exceeds palette length %d",
					slot,
					index,
					len(snapshot.Palette),
				)
			}
		}

		return &PalettedContainer{
			kind:    kindIndexed,
			palette: cloneBlockIDs(snapshot.Palette),
			lookup:  lookup,
			bits:    snapshot.Bits,
			data:    cloneUint64s(snapshot.Packed),
		}, nil

	case StorageDirect:
		if snapshot.Bits != directBits {
			return nil, fmt.Errorf("world: direct snapshot has invalid bits %d", snapshot.Bits)
		}
		if snapshot.Single != 0 || len(snapshot.Palette) != 0 {
			return nil, errors.New("world: direct snapshot has palette or single value")
		}
		if len(snapshot.Packed) != wordsFor(directBits) {
			return nil, fmt.Errorf(
				"world: direct packed length %d, want %d",
				len(snapshot.Packed),
				wordsFor(directBits),
			)
		}
		for index, word := range snapshot.Packed {
			if word>>60 != 0 {
				return nil, fmt.Errorf("world: direct packed word %d has unused high bits", index)
			}
		}
		return &PalettedContainer{
			kind: kindDirect,
			bits: directBits,
			data: cloneUint64s(snapshot.Packed),
		}, nil

	default:
		return nil, fmt.Errorf("world: unknown snapshot storage %d", snapshot.Kind)
	}
}

func validSnapshotBlockID(id core.BlockID) bool {
	return id < 1<<directBits
}

func readPacked(data []uint64, bits uint8, index int) uint32 {
	perWord := 64 / int(bits)
	shift := uint((index % perWord) * int(bits))
	mask := uint64(1)<<bits - 1
	return uint32((data[index/perWord] >> shift) & mask)
}

func cloneBlockIDs(source []core.BlockID) []core.BlockID {
	if source == nil {
		return nil
	}
	clone := make([]core.BlockID, len(source))
	copy(clone, source)
	return clone
}

func cloneUint64s(source []uint64) []uint64 {
	if source == nil {
		return nil
	}
	clone := make([]uint64, len(source))
	copy(clone, source)
	return clone
}
