package storage

import (
	"encoding/binary"
	"fmt"
	"io"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

func encodeLogicalChunk(save ChunkSave) ([]byte, error) {
	logical := make([]byte, 0, 32)
	logical = append(logical, chunkLogicalMagic[:]...)
	logical = appendU32(logical, currentChunkSchema)
	logical = appendU32(logical, uint32(save.Key.Dimension))
	logical = appendU32(logical, uint32(save.Key.Pos.X))
	logical = appendU32(logical, uint32(save.Key.Pos.Z))
	logical = appendU64(logical, save.Revision)
	logical = appendU32(logical, core.SectionsPerChunk)

	for index := 0; index < core.SectionsPerChunk; index++ {
		section := save.Chunk.Section(index)
		if section == nil || section.Blocks == nil {
			return nil, fmt.Errorf("%w: nil section %d", ErrCorrupt, index)
		}
		snapshot := section.Blocks.Snapshot()
		if _, err := world.NewPalettedContainerFromSnapshot(snapshot); err != nil {
			return nil, fmt.Errorf("%w: section %d: %v", ErrCorrupt, index, err)
		}
		logical = appendLogicalSection(logical, uint32(index), snapshot)
	}
	for slot := range core.DropsPerChunk {
		drop := save.Chunk.Drop(slot)
		if err := validateDropSlot(drop); err != nil {
			return nil, fmt.Errorf("%w: drop slot %d: %v", ErrCorrupt, slot, err)
		}
		logical = appendLogicalDropSlot(logical, drop)
	}
	if err := validateFurnaceSlots(save.Chunk); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	for slot := range core.FurnacesPerChunk {
		logical = appendLogicalFurnaceSlot(logical, save.Chunk.Furnace(slot))
	}
	if err := validateChestSlots(save.Chunk); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	for slot := range core.ChestsPerChunk {
		logical = appendLogicalChestSlot(logical, save.Chunk.Chest(slot))
	}
	return logical, nil
}

func appendLogicalSection(dst []byte, index uint32, snapshot world.ContainerSnapshot) []byte {
	dst = appendU32(dst, index)
	dst = append(dst, byte(snapshot.Kind), snapshot.Bits)
	dst = binary.LittleEndian.AppendUint16(dst, uint16(snapshot.Single))
	dst = appendU32(dst, uint32(len(snapshot.Palette)))
	for _, id := range snapshot.Palette {
		dst = binary.LittleEndian.AppendUint16(dst, uint16(id))
	}
	dst = appendU32(dst, uint32(len(snapshot.Packed)))
	for _, word := range snapshot.Packed {
		dst = appendU64(dst, word)
	}
	return dst
}

func decodeLogicalChunk(
	wantKey core.ChunkKey,
	wantRevision uint64,
	wantSchema uint32,
	data []byte,
) (chunkDTO, error) {
	logical := byteDecoder{data: data}
	if err := logical.magic(chunkLogicalMagic); err != nil {
		return chunkDTO{}, corrupt("logical magic", err)
	}
	schema, err := logical.u32()
	if err != nil {
		return chunkDTO{}, corrupt("logical schema", err)
	}
	if schema != wantSchema {
		return chunkDTO{}, fmt.Errorf("%w: logical schema does not match envelope", ErrCorrupt)
	}
	key, err := decodeKey(&logical)
	if err != nil {
		return chunkDTO{}, corrupt("logical key", err)
	}
	revision, err := logical.u64()
	if err != nil {
		return chunkDTO{}, corrupt("logical revision", err)
	}
	if key != wantKey || revision != wantRevision {
		return chunkDTO{}, fmt.Errorf("%w: logical key or revision does not match envelope", ErrCorrupt)
	}
	count, err := logical.u32()
	if err != nil {
		return chunkDTO{}, corrupt("section count", err)
	}
	if count != core.SectionsPerChunk {
		return chunkDTO{}, fmt.Errorf("%w: section count %d", ErrCorrupt, count)
	}

	dto := chunkDTO{Key: key, Revision: revision}
	for index := 0; index < core.SectionsPerChunk; index++ {
		sectionIndex, err := logical.u32()
		if err != nil {
			return chunkDTO{}, corrupt("section index", err)
		}
		if sectionIndex != uint32(index) {
			return chunkDTO{}, fmt.Errorf("%w: section index %d at position %d", ErrCorrupt, sectionIndex, index)
		}
		snapshot, err := decodeContainerSnapshot(&logical)
		if err != nil {
			return chunkDTO{}, fmt.Errorf("%w: section %d: %v", ErrCorrupt, index, err)
		}
		dto.Sections[index] = snapshot
	}
	if schema >= 2 {
		for slot := range core.DropsPerChunk {
			var drop world.DropSlot
			if schema >= 5 {
				drop, err = decodeLogicalDropSlot(&logical)
			} else {
				drop, err = decodeLegacyLogicalDropSlot(&logical)
			}
			if err != nil {
				return chunkDTO{}, fmt.Errorf("%w: drop slot %d: %v", ErrCorrupt, slot, err)
			}
			dto.Drops[slot] = drop
		}
	}
	if schema >= 4 {
		for slot := range core.FurnacesPerChunk {
			furnace, err := decodeLogicalFurnaceSlot(&logical)
			if err != nil {
				return chunkDTO{}, fmt.Errorf("%w: furnace slot %d: %v", ErrCorrupt, slot, err)
			}
			dto.Furnaces[slot] = furnace
		}
	}
	if schema >= 6 {
		for slot := range core.ChestsPerChunk {
			chest, err := decodeLogicalChestSlot(&logical)
			if err != nil {
				return chunkDTO{}, fmt.Errorf("%w: chest slot %d: %v", ErrCorrupt, slot, err)
			}
			dto.Chests[slot] = chest
		}
	}
	if logical.remaining() != 0 {
		return chunkDTO{}, fmt.Errorf("%w: trailing logical bytes", ErrCorrupt)
	}
	return dto, nil
}

func decodeContainerSnapshot(d *byteDecoder) (world.ContainerSnapshot, error) {
	kind, err := d.u8()
	if err != nil {
		return world.ContainerSnapshot{}, err
	}
	bits, err := d.u8()
	if err != nil {
		return world.ContainerSnapshot{}, err
	}
	single, err := d.u16()
	if err != nil {
		return world.ContainerSnapshot{}, err
	}
	paletteLength, err := d.u32()
	if err != nil {
		return world.ContainerSnapshot{}, err
	}
	if paletteLength > 1<<8 {
		return world.ContainerSnapshot{}, fmt.Errorf("palette length %d exceeds bound", paletteLength)
	}
	if uint64(d.remaining()) < uint64(paletteLength)*2+4 {
		return world.ContainerSnapshot{}, io.ErrUnexpectedEOF
	}
	palette := make([]core.BlockID, int(paletteLength))
	for index := range palette {
		id, err := d.u16()
		if err != nil {
			return world.ContainerSnapshot{}, err
		}
		palette[index] = core.BlockID(id)
	}
	packedLength, err := d.u32()
	if err != nil {
		return world.ContainerSnapshot{}, err
	}
	if packedLength > core.BlocksPerSection/4 {
		return world.ContainerSnapshot{}, fmt.Errorf("packed length %d exceeds bound", packedLength)
	}
	if uint64(d.remaining()) < uint64(packedLength)*8 {
		return world.ContainerSnapshot{}, io.ErrUnexpectedEOF
	}
	packed := make([]uint64, int(packedLength))
	for index := range packed {
		word, err := d.u64()
		if err != nil {
			return world.ContainerSnapshot{}, err
		}
		packed[index] = word
	}
	return world.ContainerSnapshot{
		Kind:    world.StorageKind(kind),
		Bits:    bits,
		Single:  core.BlockID(single),
		Palette: palette,
		Packed:  packed,
	}, nil
}

func decodeKey(d *byteDecoder) (core.ChunkKey, error) {
	dimension, err := d.u32()
	if err != nil {
		return core.ChunkKey{}, err
	}
	x, err := d.u32()
	if err != nil {
		return core.ChunkKey{}, err
	}
	z, err := d.u32()
	if err != nil {
		return core.ChunkKey{}, err
	}
	return core.ChunkKey{
		Dimension: core.DimensionID(int32(dimension)),
		Pos:       core.ChunkPos{X: int32(x), Z: int32(z)},
	}, nil
}
