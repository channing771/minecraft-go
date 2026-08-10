package storage

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

const (
	currentChunkSchema uint32 = 8
	maxCompressedChunk        = 1 << 20
	maxDecodedChunk           = 2 << 20

	chunkEnvelopeVersion uint32 = 1
	compressionZstd      uint32 = 1
)

var (
	chunkEnvelopeMagic = [4]byte{'C', 'H', 'N', 'K'}
	chunkLogicalMagic  = [4]byte{'M', 'C', 'G', 'C'}
)

type decodedPayload struct {
	Key      core.ChunkKey
	Revision uint64
	Schema   uint32
	Chunk    *world.Chunk
	// Migrated 表示记录读自旧 schema，需要在下次正常保存时改写。
	Migrated bool
}

// encodeChunkPayload serializes one full chunk as a bounded, versioned zstd envelope.
func encodeChunkPayload(save ChunkSave) ([]byte, error) {
	if save.Chunk == nil {
		return nil, fmt.Errorf("%w: nil chunk", ErrCorrupt)
	}
	if save.Revision == 0 {
		return nil, fmt.Errorf("%w: zero revision", ErrCorrupt)
	}
	if save.Chunk.Pos != save.Key.Pos {
		return nil, fmt.Errorf("%w: chunk position does not match key", ErrCorrupt)
	}

	logical, err := encodeLogicalChunk(save)
	if err != nil {
		return nil, err
	}
	if len(logical) > maxDecodedChunk {
		return nil, fmt.Errorf("%w: decoded chunk exceeds %d bytes", ErrCorrupt, maxDecodedChunk)
	}

	encoder, err := zstd.NewWriter(nil,
		zstd.WithEncoderConcurrency(1),
		zstd.WithEncoderCRC(true),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: create zstd encoder: %v", ErrCorrupt, err)
	}
	defer encoder.Close()

	compressed := encoder.EncodeAll(logical, nil)
	if len(compressed) > maxCompressedChunk {
		return nil, fmt.Errorf("%w: compressed chunk exceeds %d bytes", ErrCorrupt, maxCompressedChunk)
	}

	payload := make([]byte, 0, 44+len(compressed))
	payload = append(payload, chunkEnvelopeMagic[:]...)
	payload = appendU32(payload, chunkEnvelopeVersion)
	payload = appendU32(payload, currentChunkSchema)
	payload = appendU32(payload, uint32(save.Key.Dimension))
	payload = appendU32(payload, uint32(save.Key.Pos.X))
	payload = appendU32(payload, uint32(save.Key.Pos.Z))
	payload = appendU64(payload, save.Revision)
	payload = appendU32(payload, compressionZstd)
	payload = appendU32(payload, uint32(len(logical)))
	payload = appendU32(payload, uint32(len(compressed)))
	payload = append(payload, compressed...)
	return payload, nil
}

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

// validateFurnaceSlots 检查全部熔炉槽的固定约束：
// 活动槽的方块索引唯一、位于区块内且对应位置必须是熔炉方块。
func validateFurnaceSlots(chunk *world.Chunk) error {
	seen := make(map[uint32]int, core.FurnacesPerChunk)
	for slot := range core.FurnacesPerChunk {
		furnace := chunk.Furnace(slot)
		if !furnace.Valid() {
			return fmt.Errorf("furnace slot %d is not a valid fixed slot", slot)
		}
		if !furnace.Active {
			continue
		}
		if furnace.BlockIndex >= core.SectionsPerChunk*core.BlocksPerSection {
			return fmt.Errorf("furnace slot %d block index %d is outside the chunk",
				slot, furnace.BlockIndex)
		}
		if other, duplicate := seen[furnace.BlockIndex]; duplicate {
			return fmt.Errorf("furnace slots %d and %d share block index %d",
				other, slot, furnace.BlockIndex)
		}
		seen[furnace.BlockIndex] = slot
		pos, ok := world.BlockPosFromChunkIndex(chunk.Pos, furnace.BlockIndex)
		if !ok {
			return fmt.Errorf("furnace slot %d block index %d has no position",
				slot, furnace.BlockIndex)
		}
		lx, y, lz := pos.Local()
		if chunk.BlockAt(lx, int32(y), lz) != core.FurnaceID {
			return fmt.Errorf("furnace slot %d does not point at a furnace block", slot)
		}
	}
	return nil
}

func appendLogicalFurnaceSlot(dst []byte, furnace world.FurnaceSlot) []byte {
	dst = appendU32(dst, furnace.Generation)
	active := byte(0)
	if furnace.Active {
		active = 1
	}
	dst = append(dst, active)
	dst = appendU32(dst, furnace.BlockIndex)
	for _, stack := range [3]core.ItemStack{furnace.Input, furnace.Fuel, furnace.Output} {
		dst = binary.LittleEndian.AppendUint16(dst, uint16(stack.Item))
		dst = append(dst, stack.Count)
	}
	dst = append(dst, furnace.ProgressTicks)
	return binary.LittleEndian.AppendUint16(dst, furnace.BurnTicks)
}

func decodeLogicalFurnaceSlot(d *byteDecoder) (world.FurnaceSlot, error) {
	var furnace world.FurnaceSlot
	generation, err := d.u32()
	if err != nil {
		return world.FurnaceSlot{}, err
	}
	furnace.Generation = generation
	active, err := d.u8()
	if err != nil {
		return world.FurnaceSlot{}, err
	}
	if active > 1 {
		return world.FurnaceSlot{}, fmt.Errorf("furnace active flag %d is not 0 or 1", active)
	}
	furnace.Active = active == 1
	blockIndex, err := d.u32()
	if err != nil {
		return world.FurnaceSlot{}, err
	}
	furnace.BlockIndex = blockIndex
	stacks := [3]*core.ItemStack{&furnace.Input, &furnace.Fuel, &furnace.Output}
	for _, stack := range stacks {
		item, err := d.u16()
		if err != nil {
			return world.FurnaceSlot{}, err
		}
		count, err := d.u8()
		if err != nil {
			return world.FurnaceSlot{}, err
		}
		stack.Item = core.ItemID(item)
		stack.Count = count
	}
	progress, err := d.u8()
	if err != nil {
		return world.FurnaceSlot{}, err
	}
	furnace.ProgressTicks = progress
	burn, err := d.u16()
	if err != nil {
		return world.FurnaceSlot{}, err
	}
	furnace.BurnTicks = burn
	if !furnace.Valid() {
		return world.FurnaceSlot{}, errors.New("furnace slot is not a valid fixed slot")
	}
	return furnace, nil
}

// validateChestSlots 检查全部箱子槽的固定约束：活动槽的方块索引唯一、
// 位于区块内且对应位置必须是箱子方块。箱子的 27 个格子不限制物品类型，
// 逐格调用 core.ItemStack.Valid() 已在 ChestSlot.Valid() 中完成。
func validateChestSlots(chunk *world.Chunk) error {
	seen := make(map[uint32]int, core.ChestsPerChunk)
	for slot := range core.ChestsPerChunk {
		chest := chunk.Chest(slot)
		if !chest.Valid() {
			return fmt.Errorf("chest slot %d is not a valid fixed slot", slot)
		}
		if !chest.Active {
			continue
		}
		if chest.BlockIndex >= core.SectionsPerChunk*core.BlocksPerSection {
			return fmt.Errorf("chest slot %d block index %d is outside the chunk",
				slot, chest.BlockIndex)
		}
		if other, duplicate := seen[chest.BlockIndex]; duplicate {
			return fmt.Errorf("chest slots %d and %d share block index %d",
				other, slot, chest.BlockIndex)
		}
		seen[chest.BlockIndex] = slot
		pos, ok := world.BlockPosFromChunkIndex(chunk.Pos, chest.BlockIndex)
		if !ok {
			return fmt.Errorf("chest slot %d block index %d has no position",
				slot, chest.BlockIndex)
		}
		lx, y, lz := pos.Local()
		if chunk.BlockAt(lx, int32(y), lz) != core.ChestID {
			return fmt.Errorf("chest slot %d does not point at a chest block", slot)
		}
	}
	return nil
}

func appendLogicalChestSlot(dst []byte, chest world.ChestSlot) []byte {
	dst = appendU32(dst, chest.Generation)
	active := byte(0)
	if chest.Active {
		active = 1
	}
	dst = append(dst, active)
	dst = appendU32(dst, chest.BlockIndex)
	for _, stack := range chest.Items {
		dst = binary.LittleEndian.AppendUint16(dst, uint16(stack.Item))
		dst = append(dst, stack.Count)
		dst = binary.LittleEndian.AppendUint16(dst, stack.Durability)
	}
	return dst
}

func decodeLogicalChestSlot(d *byteDecoder) (world.ChestSlot, error) {
	var chest world.ChestSlot
	generation, err := d.u32()
	if err != nil {
		return world.ChestSlot{}, err
	}
	chest.Generation = generation
	active, err := d.u8()
	if err != nil {
		return world.ChestSlot{}, err
	}
	if active > 1 {
		return world.ChestSlot{}, fmt.Errorf("chest active flag %d is not 0 or 1", active)
	}
	chest.Active = active == 1
	blockIndex, err := d.u32()
	if err != nil {
		return world.ChestSlot{}, err
	}
	chest.BlockIndex = blockIndex
	for index := range chest.Items {
		item, err := d.u16()
		if err != nil {
			return world.ChestSlot{}, err
		}
		count, err := d.u8()
		if err != nil {
			return world.ChestSlot{}, err
		}
		durability, err := d.u16()
		if err != nil {
			return world.ChestSlot{}, err
		}
		chest.Items[index] = core.ItemStack{Item: core.ItemID(item), Count: count, Durability: durability}
	}
	if !chest.Valid() {
		return world.ChestSlot{}, errors.New("chest slot is not a valid fixed slot")
	}
	return chest, nil
}

// validateDropSlot 检查活动槽的固定字段上限；非活动槽只保留 generation。
func validateDropSlot(drop world.DropSlot) error {
	if !drop.Active {
		return nil
	}
	if drop.Generation == 0 {
		return errors.New("active drop slot has zero generation")
	}
	if !drop.Stack.Valid() {
		return errors.New("drop stack is invalid")
	}
	if drop.BlockIndex >= core.SectionsPerChunk*core.BlocksPerSection {
		return fmt.Errorf("drop block index %d is outside the chunk", drop.BlockIndex)
	}
	return nil
}

func appendLogicalDropSlot(dst []byte, drop world.DropSlot) []byte {
	dst = appendU32(dst, drop.Generation)
	active := byte(0)
	if drop.Active {
		active = 1
	}
	dst = append(dst, active)
	dst = binary.LittleEndian.AppendUint16(dst, uint16(drop.Stack.Item))
	dst = append(dst, drop.Stack.Count)
	dst = binary.LittleEndian.AppendUint16(dst, drop.Stack.Durability)
	dst = appendU32(dst, drop.BlockIndex)
	dst = appendU32(dst, drop.AgeTicks)
	return append(dst, drop.PickupDelayTicks)
}

func decodeLogicalDropSlot(d *byteDecoder) (world.DropSlot, error) {
	var drop world.DropSlot
	var err error
	if drop.Generation, err = d.u32(); err != nil {
		return world.DropSlot{}, err
	}
	active, err := d.u8()
	if err != nil {
		return world.DropSlot{}, err
	}
	if active > 1 {
		return world.DropSlot{}, fmt.Errorf("invalid drop active flag %d", active)
	}
	drop.Active = active == 1
	item, err := d.u16()
	if err != nil {
		return world.DropSlot{}, err
	}
	drop.Stack.Item = core.ItemID(item)
	if drop.Stack.Count, err = d.u8(); err != nil {
		return world.DropSlot{}, err
	}
	if drop.Stack.Durability, err = d.u16(); err != nil {
		return world.DropSlot{}, err
	}
	if drop.BlockIndex, err = d.u32(); err != nil {
		return world.DropSlot{}, err
	}
	if drop.AgeTicks, err = d.u32(); err != nil {
		return world.DropSlot{}, err
	}
	if drop.PickupDelayTicks, err = d.u8(); err != nil {
		return world.DropSlot{}, err
	}
	if err := validateDropSlot(drop); err != nil {
		return world.DropSlot{}, err
	}
	return drop, nil
}

func decodeLegacyLogicalDropSlot(d *byteDecoder) (world.DropSlot, error) {
	var drop world.DropSlot
	var err error
	if drop.Generation, err = d.u32(); err != nil {
		return world.DropSlot{}, err
	}
	active, err := d.u8()
	if err != nil {
		return world.DropSlot{}, err
	}
	if active > 1 {
		return world.DropSlot{}, fmt.Errorf("invalid drop active flag %d", active)
	}
	drop.Active = active == 1
	item, err := d.u16()
	if err != nil {
		return world.DropSlot{}, err
	}
	drop.Stack.Item = core.ItemID(item)
	if drop.Stack.Count, err = d.u8(); err != nil {
		return world.DropSlot{}, err
	}
	if drop.BlockIndex, err = d.u32(); err != nil {
		return world.DropSlot{}, err
	}
	if drop.AgeTicks, err = d.u32(); err != nil {
		return world.DropSlot{}, err
	}
	if drop.PickupDelayTicks, err = d.u8(); err != nil {
		return world.DropSlot{}, err
	}
	if !drop.Active {
		return drop, nil
	}
	if drop.Generation == 0 {
		return world.DropSlot{}, errors.New("active drop slot has zero generation")
	}
	if drop.BlockIndex >= core.SectionsPerChunk*core.BlocksPerSection {
		return world.DropSlot{}, fmt.Errorf("drop block index %d is outside the chunk", drop.BlockIndex)
	}
	if _, ok := core.ItemMaxDurability(drop.Stack.Item); ok {
		if drop.Stack.Count < 1 || drop.Stack.Count > core.MaxStackCount {
			return world.DropSlot{}, errors.New("legacy tool drop stack is invalid")
		}
		return drop, nil
	}
	if !drop.Stack.Valid() {
		return world.DropSlot{}, errors.New("drop stack is invalid")
	}
	return drop, nil
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

// decodeChunkPayload verifies the envelope and reconstructs an independent chunk.
func decodeChunkPayload(
	wantKey core.ChunkKey,
	wantRevision uint64,
	payload []byte,
) (decodedPayload, error) {
	if wantRevision == 0 {
		return decodedPayload{}, fmt.Errorf("%w: zero requested revision", ErrCorrupt)
	}

	envelope := byteDecoder{data: payload}
	if err := envelope.magic(chunkEnvelopeMagic); err != nil {
		return decodedPayload{}, corrupt("envelope magic", err)
	}
	version, err := envelope.u32()
	if err != nil {
		return decodedPayload{}, corrupt("envelope version", err)
	}
	if version != chunkEnvelopeVersion {
		if version > chunkEnvelopeVersion {
			return decodedPayload{}, fmt.Errorf("%w: envelope version %d", ErrFutureVersion, version)
		}
		return decodedPayload{}, fmt.Errorf("%w: unsupported envelope version %d", ErrCorrupt, version)
	}
	schema, err := envelope.u32()
	if err != nil {
		return decodedPayload{}, corrupt("envelope schema", err)
	}
	if schema > currentChunkSchema {
		return decodedPayload{}, fmt.Errorf("%w: chunk schema %d", ErrFutureVersion, schema)
	}
	if schema < oldestChunkSchema {
		return decodedPayload{}, fmt.Errorf("%w: unsupported chunk schema %d", ErrCorrupt, schema)
	}
	key, err := decodeKey(&envelope)
	if err != nil {
		return decodedPayload{}, corrupt("envelope key", err)
	}
	revision, err := envelope.u64()
	if err != nil {
		return decodedPayload{}, corrupt("envelope revision", err)
	}
	if revision == 0 {
		return decodedPayload{}, fmt.Errorf("%w: zero revision", ErrCorrupt)
	}
	if key != wantKey || revision != wantRevision {
		return decodedPayload{}, fmt.Errorf("%w: envelope key or revision does not match request", ErrCorrupt)
	}
	compression, err := envelope.u32()
	if err != nil {
		return decodedPayload{}, corrupt("compression ID", err)
	}
	if compression != compressionZstd {
		return decodedPayload{}, fmt.Errorf("%w: unknown compression ID %d", ErrCorrupt, compression)
	}
	decodedLength, err := envelope.u32()
	if err != nil {
		return decodedPayload{}, corrupt("decoded length", err)
	}
	if decodedLength > maxDecodedChunk {
		return decodedPayload{}, fmt.Errorf("%w: decoded length %d exceeds limit", ErrCorrupt, decodedLength)
	}
	compressedLength, err := envelope.u32()
	if err != nil {
		return decodedPayload{}, corrupt("compressed length", err)
	}
	if compressedLength > maxCompressedChunk {
		return decodedPayload{}, fmt.Errorf("%w: compressed length %d exceeds limit", ErrCorrupt, compressedLength)
	}
	if uint64(envelope.remaining()) != uint64(compressedLength) {
		return decodedPayload{}, fmt.Errorf("%w: compressed length does not match envelope", ErrCorrupt)
	}
	compressed, err := envelope.take(int(compressedLength))
	if err != nil {
		return decodedPayload{}, corrupt("compressed bytes", err)
	}

	decoder, err := zstd.NewReader(nil,
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderMaxMemory(maxDecodedChunk),
	)
	if err != nil {
		return decodedPayload{}, fmt.Errorf("%w: create zstd decoder: %v", ErrCorrupt, err)
	}
	decompressed, err := decoder.DecodeAll(compressed, make([]byte, 0, int(decodedLength)))
	decoder.Close()
	if err != nil {
		return decodedPayload{}, fmt.Errorf("%w: decompress chunk: %v", ErrCorrupt, err)
	}
	if len(decompressed) != int(decodedLength) {
		return decodedPayload{}, fmt.Errorf("%w: decoded length does not match envelope", ErrCorrupt)
	}

	dto, err := decodeLogicalChunk(key, revision, schema, decompressed)
	if err != nil {
		return decodedPayload{}, err
	}
	dto, migrated, err := migrateChunk(schema, dto)
	if err != nil {
		return decodedPayload{}, err
	}
	chunk, err := chunkFromDTO(dto)
	if err != nil {
		return decodedPayload{}, err
	}
	return decodedPayload{
		Key: key, Revision: revision, Schema: currentChunkSchema,
		Chunk: chunk, Migrated: migrated,
	}, nil
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

func chunkFromDTO(dto chunkDTO) (*world.Chunk, error) {
	chunk := world.NewChunk(dto.Key.Pos)
	for index, snapshot := range dto.Sections {
		container, err := world.NewPalettedContainerFromSnapshot(snapshot)
		if err != nil {
			return nil, fmt.Errorf("%w: section %d: %v", ErrCorrupt, index, err)
		}
		chunk.Section(index).Blocks = container
	}
	// section 是直接装入的，派生高度表需要一次性重建。
	chunk.RebuildHeights()
	for slot, drop := range dto.Drops {
		chunk.SetDrop(slot, drop)
	}
	for slot, furnace := range dto.Furnaces {
		chunk.SetFurnace(slot, furnace)
	}
	for slot, chest := range dto.Chests {
		chunk.SetChest(slot, chest)
	}
	if err := validateFurnaceSlots(chunk); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	if err := validateChestSlots(chunk); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	return chunk, nil
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

func appendU32(dst []byte, value uint32) []byte {
	return binary.LittleEndian.AppendUint32(dst, value)
}

func appendU64(dst []byte, value uint64) []byte {
	return binary.LittleEndian.AppendUint64(dst, value)
}

type byteDecoder struct {
	data   []byte
	offset int
}

func (d *byteDecoder) u8() (uint8, error) {
	bytes, err := d.take(1)
	if err != nil {
		return 0, err
	}
	return bytes[0], nil
}

func (d *byteDecoder) u16() (uint16, error) {
	bytes, err := d.take(2)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint16(bytes), nil
}

func (d *byteDecoder) u32() (uint32, error) {
	if d.remaining() < 4 {
		return 0, io.ErrUnexpectedEOF
	}
	value := binary.LittleEndian.Uint32(d.data[d.offset:])
	d.offset += 4
	return value, nil
}

func (d *byteDecoder) u64() (uint64, error) {
	if d.remaining() < 8 {
		return 0, io.ErrUnexpectedEOF
	}
	value := binary.LittleEndian.Uint64(d.data[d.offset:])
	d.offset += 8
	return value, nil
}

func (d *byteDecoder) magic(want [4]byte) error {
	got, err := d.take(len(want))
	if err != nil {
		return err
	}
	for index := range want {
		if got[index] != want[index] {
			return fmt.Errorf("unexpected magic")
		}
	}
	return nil
}

func (d *byteDecoder) take(length int) ([]byte, error) {
	if length < 0 || length > d.remaining() {
		return nil, io.ErrUnexpectedEOF
	}
	value := d.data[d.offset : d.offset+length]
	d.offset += length
	return value, nil
}

func (d *byteDecoder) remaining() int {
	return len(d.data) - d.offset
}

func corrupt(field string, err error) error {
	return fmt.Errorf("%w: %s: %v", ErrCorrupt, field, err)
}
