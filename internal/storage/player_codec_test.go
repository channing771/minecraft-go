package storage

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math"
	"os"
	"path/filepath"
	"testing"

	"minecraft-go/internal/core"
)

func fixturePlayerID() core.PlayerID {
	return core.PlayerID{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x46, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
}

func fixturePlayerSave(id core.PlayerID, revision uint64) PlayerSave {
	safe := PlayerLocation{Dimension: core.Overworld, Position: [3]float32{1.5, 65, -2.5}}
	return PlayerSave{
		PlayerID: id, Revision: revision, DisplayName: "Chen",
		Current: PlayerLocation{Dimension: core.Overworld, Position: [3]float32{2.5, 70, -3.5}},
		Yaw:     1.25, Pitch: -0.5, Safe: &safe,
	}
}

func TestPlayerCodecRoundTrip(t *testing.T) {
	id := fixturePlayerID()
	want := fixturePlayerSave(id, 7)
	encoded, err := encodePlayer(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodePlayer(id, encoded)
	if err != nil || got.PlayerID != want.PlayerID || got.Revision != want.Revision ||
		got.DisplayName != want.DisplayName || got.Current != want.Current ||
		got.Yaw != want.Yaw || got.Pitch != want.Pitch || got.Safe == nil || *got.Safe != *want.Safe {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	if got.NeedsRewrite {
		t.Fatal("v1 player unexpectedly needs rewrite")
	}
	got.Safe.Position[0] = 99
	if want.Safe.Position[0] == 99 {
		t.Fatal("decoded safe location aliases save")
	}
}

func TestPlayerV1Fixture(t *testing.T) {
	encoded, err := encodePlayer(fixturePlayerSave(fixturePlayerID(), 19))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "player-v1.bin")
	if *updateStorageFixtures {
		if err := os.WriteFile(path, encoded, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, encoded) {
		t.Fatal("v1 fixture drift; change schema version")
	}
}

func TestPlayerCodecRejectsInvalidSave(t *testing.T) {
	valid := fixturePlayerSave(fixturePlayerID(), 1)
	tests := []struct {
		name   string
		mutate func(*PlayerSave)
	}{
		{"invalid player ID", func(save *PlayerSave) { save.PlayerID = core.PlayerID{} }},
		{"zero revision", func(save *PlayerSave) { save.Revision = 0 }},
		{"unnormalized name", func(save *PlayerSave) { save.DisplayName = " Chen " }},
		{"invalid dimension", func(save *PlayerSave) { save.Current.Dimension = 1 }},
		{"nonfinite current position", func(save *PlayerSave) { save.Current.Position[0] = float32(math.Inf(1)) }},
		{"nonfinite yaw", func(save *PlayerSave) { save.Yaw = float32(math.NaN()) }},
		{"nonfinite pitch", func(save *PlayerSave) { save.Pitch = float32(math.Inf(-1)) }},
		{"pitch too high", func(save *PlayerSave) { save.Pitch = float32(math.Pi/2) + 0.01 }},
		{"invalid safe dimension", func(save *PlayerSave) { save.Safe.Dimension = 1 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			save := valid
			if valid.Safe != nil {
				safe := *valid.Safe
				save.Safe = &safe
			}
			tc.mutate(&save)
			if _, err := encodePlayer(save); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("encode error = %v, want ErrCorrupt", err)
			}
		})
	}
}

func TestPlayerCodecRejectsCorruptEnvelope(t *testing.T) {
	id := fixturePlayerID()
	encoded, err := encodePlayer(fixturePlayerSave(id, 19))
	if err != nil {
		t.Fatal(err)
	}
	badFloat := func(offset int) []byte {
		payload := bytes.Clone(encoded)
		binary.LittleEndian.PutUint32(payload[offset:], math.Float32bits(float32(math.NaN())))
		repairPlayerCRC(payload)
		return payload
	}
	tests := []struct {
		name    string
		payload func() []byte
		want    error
	}{
		{"magic", func() []byte { p := bytes.Clone(encoded); p[0] ^= 1; return p }, ErrCorrupt},
		{"old envelope", func() []byte { p := bytes.Clone(encoded); binary.LittleEndian.PutUint32(p[4:], 0); return p }, ErrCorrupt},
		{"future envelope", func() []byte { p := bytes.Clone(encoded); binary.LittleEndian.PutUint32(p[4:], 2); return p }, ErrFutureVersion},
		{"invalid schema", func() []byte { p := bytes.Clone(encoded); binary.LittleEndian.PutUint32(p[8:], 0); return p }, ErrCorrupt},
		{"future schema", func() []byte {
			p := bytes.Clone(encoded)
			binary.LittleEndian.PutUint32(p[8:], currentPlayerSchema+1)
			return p
		}, ErrFutureVersion},
		{"invalid player ID", func() []byte { p := bytes.Clone(encoded); clear(p[12:28]); repairPlayerCRC(p); return p }, ErrCorrupt},
		{"mismatched player ID", func() []byte { p := bytes.Clone(encoded); p[27] ^= 1; repairPlayerCRC(p); return p }, ErrCorrupt},
		{"zero revision", func() []byte { p := bytes.Clone(encoded); clear(p[28:36]); repairPlayerCRC(p); return p }, ErrCorrupt},
		{"payload length mismatch", func() []byte {
			p := bytes.Clone(encoded)
			binary.LittleEndian.PutUint32(p[36:], uint32(len(p)))
			return p
		}, ErrCorrupt},
		{"CRC", func() []byte { p := bytes.Clone(encoded); p[40] ^= 1; return p }, ErrCorrupt},
		{"invalid nickname", func() []byte { p := bytes.Clone(encoded); p[48] = '\n'; repairPlayerCRC(p); return p }, ErrCorrupt},
		{"current dimension", func() []byte {
			p := bytes.Clone(encoded)
			binary.LittleEndian.PutUint32(p[52:], 1)
			repairPlayerCRC(p)
			return p
		}, ErrCorrupt},
		{"current x", func() []byte { return badFloat(56) }, ErrCorrupt},
		{"current y", func() []byte { return badFloat(60) }, ErrCorrupt},
		{"current z", func() []byte { return badFloat(64) }, ErrCorrupt},
		{"yaw", func() []byte { return badFloat(68) }, ErrCorrupt},
		{"pitch", func() []byte { return badFloat(72) }, ErrCorrupt},
		{"pitch outside range", func() []byte {
			p := bytes.Clone(encoded)
			binary.LittleEndian.PutUint32(p[72:], math.Float32bits(2))
			repairPlayerCRC(p)
			return p
		}, ErrCorrupt},
		{"safe flag", func() []byte { p := bytes.Clone(encoded); p[76] = 2; repairPlayerCRC(p); return p }, ErrCorrupt},
		{"safe dimension", func() []byte {
			p := bytes.Clone(encoded)
			binary.LittleEndian.PutUint32(p[77:], 1)
			repairPlayerCRC(p)
			return p
		}, ErrCorrupt},
		{"safe x", func() []byte { return badFloat(81) }, ErrCorrupt},
		{"safe y", func() []byte { return badFloat(85) }, ErrCorrupt},
		{"safe z", func() []byte { return badFloat(89) }, ErrCorrupt},
		{"trailing byte", func() []byte { return append(bytes.Clone(encoded), 0) }, ErrCorrupt},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodePlayer(id, tc.payload())
			if !errors.Is(err, tc.want) {
				t.Fatalf("decode error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestPlayerCodecRejectsPayloadOverLimitBeforeAllocation(t *testing.T) {
	id := fixturePlayerID()
	payload := make([]byte, playerEnvelopeLength)
	copy(payload, "MCPL")
	binary.LittleEndian.PutUint32(payload[4:], playerEnvelopeVersion)
	binary.LittleEndian.PutUint32(payload[8:], currentPlayerSchema)
	copy(payload[12:28], id[:])
	binary.LittleEndian.PutUint64(payload[28:], 1)
	binary.LittleEndian.PutUint32(payload[36:], maxPlayerPayload+1)
	if _, err := decodePlayer(id, payload); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("decode error = %v, want ErrCorrupt", err)
	}
}

func repairPlayerCRC(payload []byte) {
	hasher := crc32.New(crc32.MakeTable(crc32.Castagnoli))
	_, _ = hasher.Write(payload[8:40])
	_, _ = hasher.Write(payload[playerEnvelopeLength:])
	binary.LittleEndian.PutUint32(payload[40:], hasher.Sum32())
}
