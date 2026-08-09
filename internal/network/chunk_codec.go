package network

import (
	"errors"
	"fmt"
	"sync"

	"github.com/klauspost/compress/zstd"

	"minecraft-go/internal/core"
)

const (
	MaxCompressedSnapshot = 1 << 20
	MaxDecodedSnapshot    = 2 << 20
)

type Codec struct {
	encoder *zstd.Encoder
	decoder *zstd.Decoder

	mu       sync.Mutex
	closed   bool
	closeErr error
}

func NewCodec() (*Codec, error) {
	encoder, err := zstd.NewWriter(nil,
		zstd.WithEncoderConcurrency(1),
		zstd.WithEncoderCRC(true),
	)
	if err != nil {
		return nil, fmt.Errorf("network: create zstd encoder: %w", err)
	}
	decoder, err := zstd.NewReader(nil,
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderMaxMemory(MaxDecodedSnapshot),
		zstd.WithDecodeAllCapLimit(true),
	)
	if err != nil {
		_ = encoder.Close()
		return nil, fmt.Errorf("network: create zstd decoder: %w", err)
	}
	return &Codec{encoder: encoder, decoder: decoder}, nil
}

func (c *Codec) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return c.closeErr
	}
	c.closed = true
	if c.encoder != nil {
		c.closeErr = c.encoder.Close()
		c.encoder = nil
	}
	if c.decoder != nil {
		c.decoder.Close()
		c.decoder = nil
	}
	return c.closeErr
}

func (c *Codec) EncodeClient(state State, packet ClientPacket) (uint32, []byte, error) {
	return encodeClientPacketPayload(state, packet)
}

func (c *Codec) DecodeClient(state State, packetID uint32, payload []byte) (ClientPacket, error) {
	return decodeClientPacketPayload(state, packetID, payload)
}

func (c *Codec) EncodeServer(state State, packet ServerPacket) (uint32, []byte, error) {
	if state != StatePlay {
		return encodeServerControlPayload(state, packet)
	}
	snapshot, ok := packet.(ChunkSnapshot)
	if !ok {
		return encodeServerControlPayload(state, packet)
	}
	logical, err := encodeLogicalSnapshot(snapshot)
	if err != nil {
		return 0, nil, codecError("encode server", state, 0, err)
	}
	compressed, err := c.compress(logical)
	if err != nil {
		return 0, nil, codecError("encode server", state, 0, err)
	}
	if len(compressed) > MaxCompressedSnapshot {
		return 0, nil, codecError("encode server", state, 0,
			fmt.Errorf("compressed snapshot length %d exceeds limit %d", len(compressed), MaxCompressedSnapshot))
	}
	var envelope byteEncoder
	envelope.u32(uint32(len(logical)))
	envelope.u32(uint32(len(compressed)))
	envelope.data = append(envelope.data, compressed...)
	return 0, envelope.data, nil
}

func (c *Codec) DecodeServer(state State, packetID uint32, payload []byte) (ServerPacket, error) {
	if state != StatePlay || packetID != 0 {
		return decodeServerControlPayload(state, packetID, payload)
	}
	snapshot, err := c.decodeSnapshotEnvelope(payload)
	if err != nil {
		return nil, codecError("decode server", state, packetID, err)
	}
	return snapshot, nil
}

func (c *Codec) compress(logical []byte) ([]byte, error) {
	if c == nil {
		return nil, errors.New("network: nil codec")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.encoder == nil {
		return nil, errors.New("network: codec is closed")
	}
	return c.encoder.EncodeAll(logical, nil), nil
}

func (c *Codec) decompress(compressed []byte, decodedLength uint32) ([]byte, error) {
	if c == nil {
		return nil, errors.New("network: nil codec")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.decoder == nil {
		return nil, errors.New("network: codec is closed")
	}
	decoded, err := c.decoder.DecodeAll(compressed, make([]byte, 0, int(decodedLength)))
	if err != nil {
		return nil, fmt.Errorf("decompress snapshot: %w", err)
	}
	return decoded, nil
}

func (c *Codec) decodeSnapshotEnvelope(payload []byte) (ChunkSnapshot, error) {
	if len(payload) < 8 {
		return ChunkSnapshot{}, errors.New("snapshot envelope is shorter than 8 bytes")
	}
	d := byteDecoder{data: payload}
	decodedLength, err := d.u32()
	if err != nil {
		return ChunkSnapshot{}, fmt.Errorf("decoded length: %w", err)
	}
	if decodedLength > MaxDecodedSnapshot {
		return ChunkSnapshot{}, fmt.Errorf(
			"decoded length %d exceeds limit %d", decodedLength, MaxDecodedSnapshot,
		)
	}
	compressedLength, err := d.u32()
	if err != nil {
		return ChunkSnapshot{}, fmt.Errorf("compressed length: %w", err)
	}
	if compressedLength > MaxCompressedSnapshot {
		return ChunkSnapshot{}, fmt.Errorf(
			"compressed length %d exceeds limit %d", compressedLength, MaxCompressedSnapshot,
		)
	}
	if d.offset > len(payload) || len(payload)-d.offset != int(compressedLength) {
		return ChunkSnapshot{}, errors.New("compressed length does not match snapshot envelope")
	}
	compressed, err := d.take(int(compressedLength))
	if err != nil {
		return ChunkSnapshot{}, fmt.Errorf("compressed bytes: %w", err)
	}
	decoded, err := c.decompress(compressed, decodedLength)
	if err != nil {
		return ChunkSnapshot{}, err
	}
	if len(decoded) != int(decodedLength) {
		return ChunkSnapshot{}, fmt.Errorf(
			"decoded length does not match snapshot envelope: got %d, want %d",
			len(decoded), decodedLength,
		)
	}
	snapshot, err := decodeLogicalSnapshot(decoded)
	if err != nil {
		return ChunkSnapshot{}, fmt.Errorf("logical snapshot: %w", err)
	}
	return snapshot, nil
}

func encodeLogicalSnapshot(snapshot ChunkSnapshot) ([]byte, error) {
	if err := validateSnapshotDimension(snapshot.Dimension); err != nil {
		return nil, err
	}
	if err := snapshot.Validate(); err != nil {
		return nil, err
	}
	size := logicalSnapshotSize(snapshot)
	if size > MaxDecodedSnapshot {
		return nil, fmt.Errorf("decoded snapshot length %d exceeds limit %d", size, MaxDecodedSnapshot)
	}
	e := byteEncoder{data: make([]byte, 0, size)}
	e.i32(int32(snapshot.Dimension))
	e.i32(snapshot.Chunk.X)
	e.i32(snapshot.Chunk.Z)
	e.u64(snapshot.Revision)
	e.uvarint(uint32(len(snapshot.Sections)))
	for _, section := range snapshot.Sections {
		e.u8(uint8(section.Y))
		e.u8(uint8(section.Storage))
		switch section.Storage {
		case SectionSingle:
			e.u16(uint16(section.Single))
		case SectionIndexed:
			e.u8(section.Bits)
			e.uvarint(uint32(len(section.Palette)))
			for _, id := range section.Palette {
				e.u16(uint16(id))
			}
			e.uvarint(uint32(len(section.Packed)))
			for _, word := range section.Packed {
				e.u64(word)
			}
		case SectionDirect:
			e.u8(section.Bits)
			e.uvarint(uint32(len(section.Packed)))
			for _, word := range section.Packed {
				e.u64(word)
			}
		}
	}
	if e.err != nil {
		return nil, e.err
	}
	return e.data, nil
}

func logicalSnapshotSize(snapshot ChunkSnapshot) int {
	size := 20 + canonicalUvarintLength(uint32(len(snapshot.Sections)))
	for _, section := range snapshot.Sections {
		size += 2 + section.PayloadBytes()
		switch section.Storage {
		case SectionIndexed:
			size += 1 + canonicalUvarintLength(uint32(len(section.Palette))) +
				canonicalUvarintLength(uint32(len(section.Packed)))
		case SectionDirect:
			size += 1 + canonicalUvarintLength(uint32(len(section.Packed)))
		}
	}
	return size
}

func decodeLogicalSnapshot(data []byte) (ChunkSnapshot, error) {
	d := byteDecoder{data: data}
	var snapshot ChunkSnapshot
	dimension, err := d.i32()
	if err != nil {
		return ChunkSnapshot{}, fmt.Errorf("dimension: %w", err)
	}
	snapshot.Dimension = core.DimensionID(dimension)
	if err := validateSnapshotDimension(snapshot.Dimension); err != nil {
		return ChunkSnapshot{}, err
	}
	if snapshot.Chunk.X, err = d.i32(); err != nil {
		return ChunkSnapshot{}, fmt.Errorf("chunk X: %w", err)
	}
	if snapshot.Chunk.Z, err = d.i32(); err != nil {
		return ChunkSnapshot{}, fmt.Errorf("chunk Z: %w", err)
	}
	if snapshot.Revision, err = d.u64(); err != nil {
		return ChunkSnapshot{}, fmt.Errorf("revision: %w", err)
	}
	sectionCount, err := d.uvarint()
	if err != nil {
		return ChunkSnapshot{}, fmt.Errorf("section count: %w", err)
	}
	if sectionCount != core.SectionsPerChunk {
		return ChunkSnapshot{}, fmt.Errorf("section count %d, want %d", sectionCount, core.SectionsPerChunk)
	}
	snapshot.Sections = make([]SectionData, core.SectionsPerChunk)
	for index := range snapshot.Sections {
		section, err := decodeLogicalSection(&d, index)
		if err != nil {
			return ChunkSnapshot{}, fmt.Errorf("section %d: %w", index, err)
		}
		snapshot.Sections[index] = section
	}
	if err := d.done(); err != nil {
		return ChunkSnapshot{}, err
	}
	if err := snapshot.Validate(); err != nil {
		return ChunkSnapshot{}, err
	}
	return snapshot, nil
}

func validateSnapshotDimension(dimension core.DimensionID) error {
	if dimension != core.Overworld {
		return fmt.Errorf("snapshot dimension %d is not overworld", dimension)
	}
	return nil
}

func decodeLogicalSection(d *byteDecoder, index int) (SectionData, error) {
	sectionY, err := d.u8()
	if err != nil {
		return SectionData{}, fmt.Errorf("Y: %w", err)
	}
	if int(sectionY) != index {
		return SectionData{}, fmt.Errorf("Y %d at position %d", sectionY, index)
	}
	storage, err := d.u8()
	if err != nil {
		return SectionData{}, fmt.Errorf("storage: %w", err)
	}
	section := SectionData{Y: int32(sectionY), Storage: SectionStorage(storage)}
	switch section.Storage {
	case SectionSingle:
		block, err := d.u16()
		if err != nil {
			return SectionData{}, fmt.Errorf("single block ID: %w", err)
		}
		section.Single = core.BlockID(block)

	case SectionIndexed:
		if err := decodeIndexedSection(d, &section); err != nil {
			return SectionData{}, err
		}

	case SectionDirect:
		bits, err := d.u8()
		if err != nil {
			return SectionData{}, fmt.Errorf("direct bits: %w", err)
		}
		if bits != 15 {
			return SectionData{}, fmt.Errorf("direct bits %d, want 15", bits)
		}
		section.Bits = bits
		wordCount, err := d.uvarint()
		if err != nil {
			return SectionData{}, fmt.Errorf("direct word count: %w", err)
		}
		wantWords := sectionWords(bits)
		if wordCount != uint32(wantWords) {
			return SectionData{}, fmt.Errorf("direct word count %d, want %d", wordCount, wantWords)
		}
		if err := requireRemaining(d, uint64(wordCount)*8, "direct words"); err != nil {
			return SectionData{}, err
		}
		section.Packed = make([]uint64, int(wordCount))
		for wordIndex := range section.Packed {
			section.Packed[wordIndex], err = d.u64()
			if err != nil {
				return SectionData{}, fmt.Errorf("direct word %d: %w", wordIndex, err)
			}
		}

	default:
		return SectionData{}, fmt.Errorf("unknown storage %d", storage)
	}
	if err := section.Validate(); err != nil {
		return SectionData{}, err
	}
	return section, nil
}

func decodeIndexedSection(d *byteDecoder, section *SectionData) error {
	bits, err := d.u8()
	if err != nil {
		return fmt.Errorf("indexed bits: %w", err)
	}
	if bits != 4 && bits != 8 {
		return fmt.Errorf("indexed bits %d, want 4 or 8", bits)
	}
	section.Bits = bits
	paletteCount, err := d.uvarint()
	if err != nil {
		return fmt.Errorf("palette count: %w", err)
	}
	maxPalette := uint32(1) << bits
	if paletteCount == 0 || paletteCount > maxPalette {
		return fmt.Errorf("palette count %d is outside 1..%d", paletteCount, maxPalette)
	}
	if err := requireRemaining(d, uint64(paletteCount)*2+1, "palette and word count"); err != nil {
		return err
	}
	section.Palette = make([]core.BlockID, int(paletteCount))
	seen := make(map[core.BlockID]struct{}, len(section.Palette))
	for paletteIndex := range section.Palette {
		block, err := d.u16()
		if err != nil {
			return fmt.Errorf("palette block %d: %w", paletteIndex, err)
		}
		id := core.BlockID(block)
		if !validBlockID(id) {
			return fmt.Errorf("palette block ID %d is unregistered", id)
		}
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("duplicate palette block ID %d", id)
		}
		seen[id] = struct{}{}
		section.Palette[paletteIndex] = id
	}
	wordCount, err := d.uvarint()
	if err != nil {
		return fmt.Errorf("indexed word count: %w", err)
	}
	wantWords := sectionWords(bits)
	if wordCount != uint32(wantWords) {
		return fmt.Errorf("indexed word count %d, want %d", wordCount, wantWords)
	}
	if err := requireRemaining(d, uint64(wordCount)*8, "indexed words"); err != nil {
		return err
	}
	section.Packed = make([]uint64, int(wordCount))
	for wordIndex := range section.Packed {
		section.Packed[wordIndex], err = d.u64()
		if err != nil {
			return fmt.Errorf("indexed word %d: %w", wordIndex, err)
		}
	}
	return nil
}

func requireRemaining(d *byteDecoder, need uint64, field string) error {
	if d.offset < 0 || d.offset > len(d.data) || need > uint64(len(d.data)-d.offset) {
		return fmt.Errorf("%s exceed remaining logical payload", field)
	}
	return nil
}
