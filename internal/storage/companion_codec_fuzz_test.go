package storage

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/channing771/mornlea/internal/companion"
)

func FuzzDecodeCompanions(f *testing.F) {
	if fixture, err := os.ReadFile(filepath.Join("testdata", "companions-v1.bin")); err == nil {
		for length := range len(fixture) + 1 {
			f.Add(bytes.Clone(fixture[:length]))
		}
	}
	records := make([]companion.Body, companion.MaxStored)
	for index := range records {
		records[index].ID = fixtureCompanionID(byte(index))
	}
	maximum, err := encodeCompanions(CompanionSave{Revision: 1, Records: records})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(maximum)
	oversized := make([]byte, 32)
	copy(oversized, "MCAI")
	binary.LittleEndian.PutUint32(oversized[4:], 1)
	binary.LittleEndian.PutUint32(oversized[8:], 1)
	binary.LittleEndian.PutUint64(oversized[12:], 1)
	binary.LittleEndian.PutUint32(oversized[20:], companion.MaxStored+1)
	binary.LittleEndian.PutUint32(oversized[24:], (companion.MaxStored+1)*221)
	f.Add(oversized)
	f.Fuzz(func(t *testing.T, payload []byte) {
		got, err := decodeCompanions(payload)
		if err != nil {
			return
		}
		if got.Revision == 0 || len(got.Records) > companion.MaxStored {
			t.Fatalf("successful decode escaped bounds: %+v", got)
		}
		for index, body := range got.Records {
			if !body.ID.Valid() || !body.Inventory.Valid() || index > 0 && bytes.Compare(got.Records[index-1].ID[:], body.ID[:]) >= 0 {
				t.Fatalf("successful decode returned invalid records: %+v", got.Records)
			}
		}
		encoded, err := encodeCompanions(CompanionSave{Revision: got.Revision, Records: got.Records})
		if err != nil || !bytes.Equal(encoded, payload) {
			t.Fatalf("successful decode is not canonical: encode error=%v", err)
		}
	})
}
