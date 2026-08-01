package network

import (
	"errors"
	"fmt"
	"math"

	"minecraft-go/internal/core"
)

type SectionStorage uint8

const (
	SectionSingle SectionStorage = iota
	SectionIndexed
	SectionDirect
)

type SectionData struct {
	Y       int32
	Storage SectionStorage
	Single  core.BlockID
	Bits    uint8
	Palette []core.BlockID
	Packed  []uint64
}

// Validate 检查压缩区段是否可安全解码。
func (section SectionData) Validate() error {
	if section.Y < 0 || section.Y >= core.SectionsPerChunk {
		return fmt.Errorf("network: section Y %d is outside chunk", section.Y)
	}
	switch section.Storage {
	case SectionSingle:
		if section.Bits != 0 || len(section.Palette) != 0 || len(section.Packed) != 0 {
			return errors.New("network: single section has compressed payload")
		}
		if !validBlockID(section.Single) {
			return fmt.Errorf("network: single block ID %d exceeds 15 bits", section.Single)
		}
		return nil

	case SectionIndexed:
		if section.Bits != 4 && section.Bits != 8 {
			return fmt.Errorf("network: indexed section has invalid bits %d", section.Bits)
		}
		if section.Single != 0 {
			return errors.New("network: indexed section has single value")
		}
		if len(section.Palette) == 0 || len(section.Palette) > 1<<section.Bits {
			return fmt.Errorf(
				"network: palette length %d is invalid for %d bits",
				len(section.Palette),
				section.Bits,
			)
		}
		if len(section.Packed) != sectionWords(section.Bits) {
			return fmt.Errorf(
				"network: indexed packed length %d, want %d",
				len(section.Packed),
				sectionWords(section.Bits),
			)
		}
		seen := make(map[core.BlockID]struct{}, len(section.Palette))
		for _, id := range section.Palette {
			if !validBlockID(id) {
				return fmt.Errorf("network: palette block ID %d exceeds 15 bits", id)
			}
			if _, duplicate := seen[id]; duplicate {
				return fmt.Errorf("network: duplicate palette block ID %d", id)
			}
			seen[id] = struct{}{}
		}
		for index := 0; index < core.BlocksPerSection; index++ {
			slot := readSectionPacked(section.Packed, section.Bits, index)
			if slot >= uint32(len(section.Palette)) {
				return fmt.Errorf(
					"network: palette slot %d at block %d exceeds palette length %d",
					slot,
					index,
					len(section.Palette),
				)
			}
		}
		return nil

	case SectionDirect:
		if section.Bits != 15 {
			return fmt.Errorf("network: direct section has invalid bits %d", section.Bits)
		}
		if section.Single != 0 || len(section.Palette) != 0 {
			return errors.New("network: direct section has palette or single value")
		}
		if len(section.Packed) != sectionWords(15) {
			return fmt.Errorf(
				"network: direct packed length %d, want %d",
				len(section.Packed),
				sectionWords(15),
			)
		}
		for index, word := range section.Packed {
			if word>>60 != 0 {
				return fmt.Errorf("network: direct packed word %d has unused high bits", index)
			}
		}
		return nil

	default:
		return fmt.Errorf("network: unknown section storage %d", section.Storage)
	}
}

// PayloadBytes 返回区段压缩 payload 的字节数。
func (section SectionData) PayloadBytes() int {
	if section.Storage == SectionSingle {
		return 2
	}
	return len(section.Palette)*2 + len(section.Packed)*8
}

type ChunkSnapshot struct {
	Dimension core.DimensionID
	Chunk     core.ChunkPos
	Revision  uint64
	Sections  []SectionData
}

func (ChunkSnapshot) serverMessage() {}
func (ChunkSnapshot) serverPacket()  {}

// Validate 检查快照的 revision 与全部 24 个有序区段。
func (snapshot ChunkSnapshot) Validate() error {
	if snapshot.Revision == 0 {
		return errors.New("network: chunk snapshot revision is zero")
	}
	if len(snapshot.Sections) != core.SectionsPerChunk {
		return fmt.Errorf(
			"network: chunk snapshot has %d sections, want %d",
			len(snapshot.Sections),
			core.SectionsPerChunk,
		)
	}
	for index, section := range snapshot.Sections {
		if section.Y != int32(index) {
			return fmt.Errorf(
				"network: section at index %d has Y %d",
				index,
				section.Y,
			)
		}
		if err := section.Validate(); err != nil {
			return fmt.Errorf("network: section %d: %w", index, err)
		}
	}
	return nil
}

func (snapshot ChunkSnapshot) PayloadBytes() int {
	total := 0
	for _, section := range snapshot.Sections {
		total += section.PayloadBytes()
	}
	return total
}

type BlockChange struct {
	Position core.BlockPos
	Block    core.BlockID
}

type BlockChanges struct {
	Dimension    core.DimensionID
	Chunk        core.ChunkPos
	BaseRevision uint64
	NewRevision  uint64
	Changes      []BlockChange
}

func (BlockChanges) serverMessage() {}
func (BlockChanges) serverPacket()  {}

// Validate 检查 revision 连续性、区块归属和严格递增的 block index。
func (changes BlockChanges) Validate() error {
	if changes.BaseRevision == 0 || changes.BaseRevision == math.MaxUint64 ||
		changes.NewRevision != changes.BaseRevision+1 {
		return fmt.Errorf(
			"network: invalid revision transition %d -> %d",
			changes.BaseRevision,
			changes.NewRevision,
		)
	}
	if len(changes.Changes) < 1 || len(changes.Changes) > 4096 {
		return errors.New("network: block changes count is outside 1..4096")
	}
	var previous uint32
	for index, change := range changes.Changes {
		if !validBlockID(change.Block) {
			return fmt.Errorf("network: block ID %d exceeds 15 bits", change.Block)
		}
		if change.Position.Y < core.MinY || change.Position.Y >= core.MaxY {
			return fmt.Errorf("network: block Y %d is outside world", change.Position.Y)
		}
		if change.Position.Chunk() != changes.Chunk {
			return fmt.Errorf(
				"network: block %+v is outside chunk %+v",
				change.Position,
				changes.Chunk,
			)
		}
		blockIndex := chunkBlockIndex(change.Position)
		if index > 0 && blockIndex <= previous {
			return errors.New("network: block changes are not strictly index sorted")
		}
		previous = blockIndex
	}
	return nil
}

func validBlockID(id core.BlockID) bool {
	return id < 1<<15
}

func sectionWords(bits uint8) int {
	perWord := 64 / int(bits)
	return (core.BlocksPerSection + perWord - 1) / perWord
}

func readSectionPacked(data []uint64, bits uint8, index int) uint32 {
	perWord := 64 / int(bits)
	shift := uint((index % perWord) * int(bits))
	mask := uint64(1)<<bits - 1
	return uint32((data[index/perWord] >> shift) & mask)
}

func chunkBlockIndex(position core.BlockPos) uint32 {
	x, y, z := position.Local()
	return uint32(
		position.SectionIndex()*core.BlocksPerSection +
			y*core.SectionSize*core.SectionSize +
			z*core.SectionSize +
			x,
	)
}
