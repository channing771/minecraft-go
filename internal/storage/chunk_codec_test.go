package storage

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

func TestFutureSchemaIsRejectedWithoutMutation(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	payload, err := encodeChunkPayload(ChunkSave{
		Key: key, Revision: 19, Chunk: codecFixtureChunk(key.Pos),
	})
	if err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint32(payload[8:], currentChunkSchema+1)
	before := bytes.Clone(payload)

	got, err := decodeChunkPayload(key, 19, payload)
	if !errors.Is(err, ErrFutureVersion) {
		t.Fatalf("decode future schema error = %v, want ErrFutureVersion", err)
	}
	if !bytes.Equal(payload, before) {
		t.Fatal("future-schema decode mutated payload")
	}
	if got.Key != (core.ChunkKey{}) || got.Revision != 0 || got.Schema != 0 || got.Chunk != nil {
		t.Fatalf("decode future schema returned data: %+v", got)
	}
}

func TestChunkPayloadRoundTripsDeterministically(t *testing.T) {
	key := core.ChunkKey{
		Dimension: core.Overworld,
		Pos:       core.ChunkPos{X: -3, Z: 7},
	}
	chunk := codecFixtureChunk(key.Pos)
	save := ChunkSave{Key: key, Revision: 19, Chunk: chunk}

	one, err := encodeChunkPayload(save)
	if err != nil {
		t.Fatal(err)
	}
	two, err := encodeChunkPayload(save)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(one, two) {
		t.Fatal("same chunk encoded differently")
	}

	got, err := decodeChunkPayload(key, 19, one)
	if err != nil {
		t.Fatal(err)
	}
	if got.Key != key || got.Revision != 19 || got.Chunk.Hash() != chunk.Hash() {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
}

func TestChunkSchemaV8RoundTripsCommonBlockMaterialPalette(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	chunk := world.NewChunk(key.Pos)
	want := []core.BlockID{
		core.CobblestoneID, core.SmoothStoneID, core.SandID, core.GravelID,
		core.OakLogID, core.OakPlanksID, core.LeavesID, core.GlassID,
		core.BrickID, core.WhiteWoolID, core.RoofTileID, core.ClayID,
		core.SnowBlockID, core.MossyCobblestoneID,
	}
	for index, id := range want {
		setFixtureBlock(chunk, 0, index, id)
	}
	encoded, err := encodeChunkPayload(ChunkSave{Key: key, Revision: 23, Chunk: chunk})
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeChunkPayload(key, 23, encoded)
	if err != nil {
		t.Fatal(err)
	}
	if currentChunkSchema != 8 || got.Schema != 8 || got.Migrated {
		t.Fatalf("区块 schema=%d decoded=%d migrated=%v，想要 8/8/false", currentChunkSchema, got.Schema, got.Migrated)
	}
	for index, id := range want {
		x := index & core.SectionMask
		z := index >> core.SectionShift & core.SectionMask
		if block := got.Chunk.BlockAt(x, core.MinY, z); block != id {
			t.Fatalf("新材料 %d 往返 = %d，想要 %d", index, block, id)
		}
	}
}

func TestChunkPayloadRejectsInvalidSave(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	valid := ChunkSave{Key: key, Revision: 19, Chunk: codecFixtureChunk(key.Pos)}

	tests := []struct {
		name   string
		mutate func(*ChunkSave)
	}{
		{
			name:   "nil chunk",
			mutate: func(save *ChunkSave) { save.Chunk = nil },
		},
		{
			name:   "zero revision",
			mutate: func(save *ChunkSave) { save.Revision = 0 },
		},
		{
			name:   "key chunk position mismatch",
			mutate: func(save *ChunkSave) { save.Chunk.Pos.X++ },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			save := valid
			tc.mutate(&save)
			if _, err := encodeChunkPayload(save); err == nil {
				t.Fatal("invalid save was encoded")
			}
		})
	}
}

func TestChunkPayloadRejectsMalformedEnvelope(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	encoded, err := encodeChunkPayload(ChunkSave{
		Key: key, Revision: 19, Chunk: codecFixtureChunk(key.Pos),
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		payload  func() []byte
		key      core.ChunkKey
		revision uint64
		wantErr  string
	}{
		{
			name:    "wrong requested key",
			payload: func() []byte { return encoded },
			key: core.ChunkKey{
				Dimension: key.Dimension,
				Pos:       core.ChunkPos{X: key.Pos.X + 1, Z: key.Pos.Z},
			},
			revision: 19,
		},
		{
			name:     "wrong requested revision",
			payload:  func() []byte { return encoded },
			key:      key,
			revision: 20,
		},
		{
			name: "unknown compression ID",
			payload: func() []byte {
				payload := bytes.Clone(encoded)
				binary.LittleEndian.PutUint32(payload[32:], 99)
				return payload
			},
			key: key, revision: 19,
		},
		{
			name: "compressed length over limit",
			payload: func() []byte {
				payload := bytes.Clone(encoded)
				binary.LittleEndian.PutUint32(payload[40:], maxCompressedChunk+1)
				return payload
			},
			key: key, revision: 19,
		},
		{
			name: "decoded length over limit",
			payload: func() []byte {
				payload := bytes.Clone(encoded)
				binary.LittleEndian.PutUint32(payload[36:], maxDecodedChunk+1)
				return payload
			},
			key: key, revision: 19,
		},
		{
			name: "truncated sections",
			payload: func() []byte {
				logical := testLogicalChunk(key, 19, func(index int) world.ContainerSnapshot {
					return world.ContainerSnapshot{Kind: world.StorageSingle, Single: core.AirID}
				})
				return testEnvelope(key, 19, logical[:len(logical)-testSingleSectionLength])
			},
			key: key, revision: 19,
		},
		{
			name: "section count mismatch",
			payload: func() []byte {
				logical := testLogicalChunk(key, 19, func(int) world.ContainerSnapshot {
					return world.ContainerSnapshot{Kind: world.StorageSingle, Single: core.AirID}
				})
				binary.LittleEndian.PutUint32(logical[28:], core.SectionsPerChunk-1)
				return testEnvelope(key, 19, logical)
			},
			key: key, revision: 19, wantErr: "section count 23",
		},
		{
			name: "section order mismatch",
			payload: func() []byte {
				logical := testLogicalChunk(key, 19, func(int) world.ContainerSnapshot {
					return world.ContainerSnapshot{Kind: world.StorageSingle, Single: core.AirID}
				})
				binary.LittleEndian.PutUint32(logical[32:], 1)
				return testEnvelope(key, 19, logical)
			},
			key: key, revision: 19, wantErr: "section index 1 at position 0",
		},
		{
			name: "trailing logical bytes",
			payload: func() []byte {
				logical := testLogicalChunk(key, 19, func(int) world.ContainerSnapshot {
					return world.ContainerSnapshot{Kind: world.StorageSingle, Single: core.AirID}
				})
				return testEnvelope(key, 19, append(logical, 0))
			},
			key: key, revision: 19,
		},
		{
			name: "trailing envelope bytes",
			payload: func() []byte {
				return append(bytes.Clone(encoded), 0)
			},
			key: key, revision: 19,
		},
		{
			name: "invalid palette length",
			payload: func() []byte {
				logical := testLogicalChunk(key, 19, func(index int) world.ContainerSnapshot {
					if index == 0 {
						return world.ContainerSnapshot{
							Kind: world.StorageIndexed, Bits: 4,
							Palette: make([]core.BlockID, 17), Packed: make([]uint64, 256),
						}
					}
					return world.ContainerSnapshot{Kind: world.StorageSingle, Single: core.AirID}
				})
				return testEnvelope(key, 19, logical)
			},
			key: key, revision: 19,
		},
		{
			name: "direct word has unused high bits",
			payload: func() []byte {
				logical := testLogicalChunk(key, 19, func(index int) world.ContainerSnapshot {
					if index == 0 {
						words := make([]uint64, 1024)
						words[0] = uint64(1) << 63
						return world.ContainerSnapshot{
							Kind: world.StorageDirect, Bits: 15, Packed: words,
						}
					}
					return world.ContainerSnapshot{Kind: world.StorageSingle, Single: core.AirID}
				})
				return testEnvelope(key, 19, logical)
			},
			key: key, revision: 19,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeChunkPayload(tc.key, tc.revision, tc.payload())
			if err == nil {
				t.Fatal("malformed payload was accepted")
			}
			if tc.wantErr != "" && (!errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("decode error = %v, want corrupt %q", err, tc.wantErr)
			}
		})
	}
}

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
