package network

import (
	"reflect"
	"testing"

	"minecraft-go/internal/core"
)

func FuzzChunkSnapshotCodec(f *testing.F) {
	codec, err := NewCodec()
	if err != nil {
		f.Fatal(err)
	}
	defer codec.Close()

	_, valid, err := codec.EncodeServer(StatePlay, fixtureSnapshot(core.ChunkPos{X: -3, Z: 7}, 19))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(valid)
	f.Add([]byte(nil))
	f.Add(testEnvelope(MaxDecodedSnapshot, []byte{0}))

	f.Fuzz(func(t *testing.T, payload []byte) {
		packet, err := codec.DecodeServer(StatePlay, 0, payload)
		if err != nil {
			return
		}
		snapshot, ok := packet.(ChunkSnapshot)
		if !ok {
			t.Fatalf("decoded packet type %T", packet)
		}
		id, canonical, err := codec.EncodeServer(StatePlay, snapshot)
		if err != nil || id != 0 {
			t.Fatalf("re-encode id=%d err=%v", id, err)
		}
		round, err := codec.DecodeServer(StatePlay, id, canonical)
		if err != nil || !reflect.DeepEqual(round, snapshot) {
			t.Fatalf("canonical round=%+v err=%v", round, err)
		}
	})
}
