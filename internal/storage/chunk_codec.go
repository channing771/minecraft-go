package storage

import (
	"fmt"

	"github.com/klauspost/compress/zstd"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/world"
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
