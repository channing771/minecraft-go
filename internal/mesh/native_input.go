package mesh

import (
	"encoding/binary"
	"fmt"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/world"
)

const (
	nativeNeighborhoodSections = 3 * 3 * 3
	nativeHeightColumns        = 3 * 3
	nativeRegistryEntryBytes   = 2 + 1 + 1 + 6*2
	// nativeMaxRegistryEntries 必须与 Rust 端硬编码的
	// engine/crates/mornlea_engine/src/input.rs:5 的 MAX_REGISTRY_ENTRIES
	// (=27) 保持一致——两侧各自独立定义，没有共享常量或生成步骤，全靠人
	// 手动同步。当前用 `int(core.MossyCobblestoneID)+1` 推导出 27，恰好与
	// Rust 侧一致，但这只是巧合：MossyCobblestoneID 是「流体加入前最后一个
	// 已注册方块」，而 Rust 常量是「原生 registry 条目表的容量上限」，二者
	// 概念不同。流体编号追加后 core.RegisteredBlock 的上界已经变成
	// WaterLevel7ID，但 internal/assets.NewRegistry() 仍刻意只把
	// AirID..MossyCobblestoneID 纳入 mesh snapshot 的 ids 范围（见
	// internal/assets/blocks.go 的 Opaque/FaceVisible 注释），snapshot 条目
	// 数因此仍是 27，这里维持用 MossyCobblestoneID 推导没有问题。
	// **不得**顺手把它改成 `int(core.WaterLevel7ID)+1`——那会让 Go 端允许
	// 35 条 registry 记录，而 Rust 端仍然只接受 27 条，一旦真的喂进 35 条，
	// Rust 侧的 registry_count > MAX_REGISTRY_ENTRIES 校验会直接拒绝整次
	// mesh 调用。真要扩容，必须先改 Rust 常量、重新编译 mornlea_engine（这
	// 是一次 engine ABI 跨语言变更），再回来同步改这里，两侧永远一起动。
	// 顺带一提：input.rs:1 的 BLOCKS_BYTES = 27*4096*2 里也有一个 27，但那
	// 是 3×3×3 邻域区段数，跟这里的 registry 条目数上限只是数字撞了，两者
	// 无关，改一个不需要牵动另一个。
	nativeMaxRegistryEntries = int(core.MossyCobblestoneID) + 1
	nativeMaxRegistryWords   = (nativeMaxRegistryEntries + 63) / 64
	nativeLightVolume        = 48 * 48 * 48
	nativeScratchPadding     = (4 - nativeLightVolume%4) % 4
	nativeScratchBytes       = nativeLightVolume + nativeScratchPadding + nativeLightVolume*4
	maxNativeQuads           = 6 * core.BlocksPerSection
	maxNativeInputBytes      = 16 +
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
			return 0, fmt.Errorf("mesh: 方块发光等级超过 15")
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
