package storage

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"minecraft-go/internal/core"
)

func FuzzDecodeChunkPayload(f *testing.F) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "chunk-v1.bin"))
	if err != nil {
		f.Fatal(err)
	}

	for length := range len(fixture) + 1 {
		f.Add(bytes.Clone(fixture[:length]))
	}
	for _, name := range []string{"chunk-v2.bin", "chunk-v3.bin", "chunk-v4.bin", "chunk-v5.bin", "chunk-v6.bin", "chunk-v7.bin"} {
		older, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			continue
		}
		for length := range len(older) + 1 {
			f.Add(bytes.Clone(older[:length]))
		}
	}
	for _, offset := range []int{36, 40} {
		for _, length := range []uint32{
			maxCompressedChunk - 1, maxCompressedChunk, maxCompressedChunk + 1,
			maxDecodedChunk - 1, maxDecodedChunk, maxDecodedChunk + 1,
		} {
			payload := bytes.Clone(fixture)
			binary.LittleEndian.PutUint32(payload[offset:], length)
			f.Add(payload)
		}
	}

	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	f.Fuzz(func(t *testing.T, payload []byte) {
		got, err := decodeChunkPayload(key, 19, payload)
		if err == nil && (got.Key != key || got.Revision != 19) {
			t.Fatalf("successful decode changed identity: %+v", got)
		}
	})
}
