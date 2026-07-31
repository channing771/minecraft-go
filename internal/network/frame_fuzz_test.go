package network

import (
	"bytes"
	"testing"
)

func FuzzReadFrame(f *testing.F) {
	for _, seed := range [][]byte{
		nil,
		{0},
		{1, 0},
		{2, 1, 9},
		{0x80},
		{0xff, 0xff, 0xff, 0xff, 0x1f},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, wire []byte) {
		_, _, _ = ReadFrame(bytes.NewReader(wire))
	})
}
