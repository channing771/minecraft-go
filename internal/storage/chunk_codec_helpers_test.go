package storage

import (
	"encoding/binary"

	"github.com/klauspost/compress/zstd"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/world"
)

const testSingleSectionLength = 4 + 1 + 1 + 2 + 4 + 4

func testLogicalChunk(
	key core.ChunkKey,
	revision uint64,
	section func(int) world.ContainerSnapshot,
) []byte {
	logical := make([]byte, 0, 32+core.SectionsPerChunk*testSingleSectionLength)
	logical = append(logical, "MCGC"...)
	logical = testAppendU32(logical, currentChunkSchema)
	logical = testAppendU32(logical, uint32(key.Dimension))
	logical = testAppendU32(logical, uint32(key.Pos.X))
	logical = testAppendU32(logical, uint32(key.Pos.Z))
	logical = testAppendU64(logical, revision)
	logical = testAppendU32(logical, core.SectionsPerChunk)
	for index := 0; index < core.SectionsPerChunk; index++ {
		logical = testAppendSection(logical, uint32(index), section(index))
	}
	return logical
}

func testAppendSection(dst []byte, index uint32, snapshot world.ContainerSnapshot) []byte {
	dst = testAppendU32(dst, index)
	dst = append(dst, byte(snapshot.Kind), snapshot.Bits)
	dst = binary.LittleEndian.AppendUint16(dst, uint16(snapshot.Single))
	dst = testAppendU32(dst, uint32(len(snapshot.Palette)))
	for _, id := range snapshot.Palette {
		dst = binary.LittleEndian.AppendUint16(dst, uint16(id))
	}
	dst = testAppendU32(dst, uint32(len(snapshot.Packed)))
	for _, word := range snapshot.Packed {
		dst = testAppendU64(dst, word)
	}
	return dst
}

func testEnvelope(key core.ChunkKey, revision uint64, logical []byte) []byte {
	return testEnvelopeForSchema(key, revision, currentChunkSchema, logical)
}

func testEnvelopeForSchema(
	key core.ChunkKey,
	revision uint64,
	schema uint32,
	logical []byte,
) []byte {
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1), zstd.WithEncoderCRC(true))
	if err != nil {
		panic(err)
	}
	defer encoder.Close()
	compressed := encoder.EncodeAll(logical, nil)

	payload := make([]byte, 0, 44+len(compressed))
	payload = append(payload, "CHNK"...)
	payload = testAppendU32(payload, 1)
	payload = testAppendU32(payload, schema)
	payload = testAppendU32(payload, uint32(key.Dimension))
	payload = testAppendU32(payload, uint32(key.Pos.X))
	payload = testAppendU32(payload, uint32(key.Pos.Z))
	payload = testAppendU64(payload, revision)
	payload = testAppendU32(payload, 1)
	payload = testAppendU32(payload, uint32(len(logical)))
	payload = testAppendU32(payload, uint32(len(compressed)))
	return append(payload, compressed...)
}

func testAppendU32(dst []byte, value uint32) []byte {
	return binary.LittleEndian.AppendUint32(dst, value)
}

func testAppendU64(dst []byte, value uint64) []byte {
	return binary.LittleEndian.AppendUint64(dst, value)
}

func codecFixtureChunk(pos core.ChunkPos) *world.Chunk {
	chunk := world.NewChunk(pos)

	for index := 0; index < 15; index++ { // air + 15 IDs = 16 palette entries.
		setFixtureBlock(chunk, 1, index, core.BlockID(index+1))
	}
	for index := 0; index < 16; index++ { // air + 16 IDs = 17 palette entries.
		setFixtureBlock(chunk, 2, index, core.BlockID(index+1))
	}
	for index := 0; index < 256; index++ { // 先用不同值推进到 direct storage。
		setFixtureBlock(chunk, 3, index, core.BlockID(index+1))
	}
	for index := 0; index < 256; index++ { // 冻结旧夹具的 0..28 direct 值域。
		setFixtureBlock(chunk, 3, index, core.BlockID((index+1)%int(core.MossyCobblestoneID)))
	}
	return chunk
}

func setFixtureBlock(chunk *world.Chunk, section, index int, id core.BlockID) {
	x := index & core.SectionMask
	z := index >> core.SectionShift & core.SectionMask
	y := index >> (2 * core.SectionShift)
	chunk.SetBlock(x, int32(core.MinY+section*core.SectionSize+y), z, id)
}
