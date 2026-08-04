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
	currentChunkSchema uint32 = 3
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
	return logical, nil
}

// validateDropSlot 检查活动槽的固定字段上限；非活动槽只保留 generation。
func validateDropSlot(drop world.DropSlot) error {
	if !drop.Active {
		return nil
	}
	if drop.Generation == 0 {
		return errors.New("active drop slot has zero generation")
	}
	if _, ok := core.ItemPlacement(drop.Stack.Item); !ok {
		return fmt.Errorf("unknown drop item %d", drop.Stack.Item)
	}
	if drop.Stack.Count < 1 || drop.Stack.Count > core.MaxStackCount {
		return fmt.Errorf("drop count %d is outside 1..64", drop.Stack.Count)
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
			drop, err := decodeLogicalDropSlot(&logical)
			if err != nil {
				return chunkDTO{}, fmt.Errorf("%w: drop slot %d: %v", ErrCorrupt, slot, err)
			}
			dto.Drops[slot] = drop
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
	for slot, drop := range dto.Drops {
		chunk.SetDrop(slot, drop)
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
