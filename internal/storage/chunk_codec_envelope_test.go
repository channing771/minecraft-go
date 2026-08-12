package storage

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"

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
