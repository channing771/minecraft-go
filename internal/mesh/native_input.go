package mesh

import (
	"encoding/binary"
	"fmt"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

const (
	nativeNeighborhoodSections = 3 * 3 * 3
	nativeHeightColumns        = 3 * 3
	nativeRegistryEntryBytes   = 2 + 1 + 1 + 6*2
	nativeMaxRegistryEntries   = int(core.MossyCobblestoneID) + 1
	nativeMaxRegistryWords     = (nativeMaxRegistryEntries + 63) / 64
	nativeLightVolume          = 48 * 48 * 48
	nativeScratchPadding       = (4 - nativeLightVolume%4) % 4
	nativeScratchBytes         = nativeLightVolume + nativeScratchPadding + nativeLightVolume*4
	maxNativeQuads             = 6 * core.BlocksPerSection
	maxNativeInputBytes        = 16 +
		nativeNeighborhoodSections*core.BlocksPerSection*2 +
		nativeHeightColumns + nativeHeightColumns*core.SectionSize*core.SectionSize*2 +
		nativeMaxRegistryEntries*nativeRegistryEntryBytes +
		nativeMaxRegistryEntries*nativeMaxRegistryWords*8
)

// encodeNativeInput 把 neighborhood 与 registry snapshot 编码为 ABI v1 小端输入。
func encodeNativeInput(dst []byte, n *world.Neighborhood, snapshot RegistrySnapshot) (int, error) {
	if n == nil || n.Center == nil || n.Center.Blocks == nil {
		return 0, fmt.Errorf("mesh: neighborhood 或中心区段为空")
	}
	if n.SectionY < 0 || n.SectionY >= core.SectionsPerChunk {
		return 0, fmt.Errorf("mesh: section Y=%d 越界", n.SectionY)
	}
	count := len(snapshot.Blocks)
	if count == 0 || count > nativeMaxRegistryEntries {
		return 0, fmt.Errorf("mesh: registry entry 数=%d 越界", count)
	}
	air, barrier := false, false
	for i, block := range snapshot.Blocks {
		if i > 0 && block.ID <= snapshot.Blocks[i-1].ID {
			return 0, fmt.Errorf("mesh: registry block ID 未严格递增")
		}
		if block.Emission > 15 {
			return 0, fmt.Errorf("mesh: block %d emission=%d 超过 15", block.ID, block.Emission)
		}
		air = air || block.ID == core.AirID
		barrier = barrier || block.ID == core.BarrierID
	}
	if !air || !barrier {
		return 0, fmt.Errorf("mesh: registry 缺少 air 或 barrier")
	}
	wordsPerRow := (count + 63) / 64
	if len(snapshot.Visibility) != count*wordsPerRow {
		return 0, fmt.Errorf("mesh: visibility words=%d，想要 %d", len(snapshot.Visibility), count*wordsPerRow)
	}
	length := 16 + nativeNeighborhoodSections*core.BlocksPerSection*2 +
		nativeHeightColumns + nativeHeightColumns*core.SectionSize*core.SectionSize*2 +
		count*nativeRegistryEntryBytes + len(snapshot.Visibility)*8
	if length > maxNativeInputBytes || len(dst) < length {
		return 0, fmt.Errorf("mesh: input buffer=%d，想要至少 %d", len(dst), length)
	}

	copy(dst[0:4], "MGM1")
	binary.LittleEndian.PutUint32(dst[4:8], uint32(int32(core.MinY+n.SectionY*core.SectionSize)))
	binary.LittleEndian.PutUint16(dst[8:10], uint16(count))
	binary.LittleEndian.PutUint16(dst[10:12], uint16(wordsPerRow))
	binary.LittleEndian.PutUint16(dst[12:14], uint16(core.AirID))
	binary.LittleEndian.PutUint16(dst[14:16], uint16(core.BarrierID))
	offset := 16
	for cx := range 3 {
		for cy := range 3 {
			for cz := range 3 {
				section := n.Around[cx][cy][cz]
				if cx == 1 && cy == 1 && cz == 1 {
					section = n.Center
				}
				for y := range core.SectionSize {
					for z := range core.SectionSize {
						for x := range core.SectionSize {
							id := world.BlockID(core.BarrierID)
							if section != nil {
								id = section.Blocks.Get(x, y, z)
							}
							binary.LittleEndian.PutUint16(dst[offset:offset+2], uint16(id))
							offset += 2
						}
					}
				}
			}
		}
	}
	for cx := range 3 {
		for cz := range 3 {
			if n.HeightsPresent[cx][cz] {
				dst[offset] = 1
			} else {
				dst[offset] = 0
			}
			offset++
		}
	}
	for cx := range 3 {
		for cz := range 3 {
			for z := range core.SectionSize {
				for x := range core.SectionSize {
					var height int16
					if n.HeightsPresent[cx][cz] {
						height = n.Heights[cx][cz][z<<core.SectionShift|x]
					}
					binary.LittleEndian.PutUint16(dst[offset:offset+2], uint16(height))
					offset += 2
				}
			}
		}
	}
	for _, block := range snapshot.Blocks {
		binary.LittleEndian.PutUint16(dst[offset:offset+2], uint16(block.ID))
		offset += 2
		if block.Opaque {
			dst[offset] = 1
		} else {
			dst[offset] = 0
		}
		dst[offset+1] = block.Emission
		offset += 2
		for _, material := range block.Materials {
			binary.LittleEndian.PutUint16(dst[offset:offset+2], material)
			offset += 2
		}
	}
	for _, word := range snapshot.Visibility {
		binary.LittleEndian.PutUint64(dst[offset:offset+8], word)
		offset += 8
	}
	return offset, nil
}
