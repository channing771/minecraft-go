package storage

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func FuzzDecodePlayer(f *testing.F) {
	for _, name := range []string{
		"player-v1.bin", "player-v2.bin", "player-v3.bin", "player-v4.bin",
		"player-v5.bin", "player-v6.bin", "player-v7.bin",
	} {
		fixture, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			continue
		}
		for length := range len(fixture) + 1 {
			f.Add(bytes.Clone(fixture[:length]))
		}
	}
	for _, length := range []uint32{maxPlayerPayload - 1, maxPlayerPayload, maxPlayerPayload + 1} {
		payload := make([]byte, playerEnvelopeLength)
		copy(payload, "MCPL")
		binary.LittleEndian.PutUint32(payload[4:], playerEnvelopeVersion)
		binary.LittleEndian.PutUint32(payload[8:], currentPlayerSchema)
		id := fixturePlayerID()
		copy(payload[12:28], id[:])
		binary.LittleEndian.PutUint64(payload[28:], 1)
		binary.LittleEndian.PutUint32(payload[36:], length)
		f.Add(payload)
	}
	id := fixturePlayerID()
	f.Fuzz(func(t *testing.T, payload []byte) {
		got, err := decodePlayer(id, payload)
		if err == nil && (got.PlayerID != id || got.Revision == 0) {
			t.Fatalf("successful decode changed identity: %+v", got)
		}
	})
}
