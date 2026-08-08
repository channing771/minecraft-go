package network

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

// oneByteReader simulates a transport that splits every frame byte into a
// separate Read call.
type oneByteReader struct {
	data   []byte
	offset int
}

func (r *oneByteReader) Read(p []byte) (int, error) {
	if r.offset == len(r.data) {
		return 0, io.EOF
	}
	p[0] = r.data[r.offset]
	r.offset++
	return 1, nil
}

type oneByteWriter struct{ bytes.Buffer }

func (w *oneByteWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return w.Buffer.Write(p[:1])
}

type stalledWriter struct{}

func (stalledWriter) Write([]byte) (int, error) { return 0, nil }

var frameReadSink struct {
	id      uint32
	payload []byte
}

func TestFrameSplitAndCoalescedReads(t *testing.T) {
	var wire bytes.Buffer
	if err := WriteFrame(&wire, 3, []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if err := WriteFrame(&wire, 4, []byte{5, 6}); err != nil {
		t.Fatal(err)
	}
	r := &oneByteReader{data: wire.Bytes()}
	id, payload, err := ReadFrame(r)
	if err != nil || id != 3 || !bytes.Equal(payload, []byte{1, 2, 3}) {
		t.Fatalf("first frame = id %d, payload %x, err %v", id, payload, err)
	}
	id, payload, err = ReadFrame(r)
	if err != nil || id != 4 || !bytes.Equal(payload, []byte{5, 6}) {
		t.Fatalf("second frame = id %d, payload %x, err %v", id, payload, err)
	}
}

func TestFrameReadRejectsInvalidLengthsBeforeFrame(t *testing.T) {
	for _, tc := range []struct {
		name string
		wire []byte
	}{
		{"zero", []byte{0}},
		{"over maximum", []byte{0x81, 0x80, 0x80, 0x01}}, // 2 MiB + 1
		{"truncated varint", []byte{0x80}},
		{"noncanonical varint", []byte{0x81, 0x00}},
		{"overflow varint", []byte{0xff, 0xff, 0xff, 0xff, 0x1f}},
		{"more than five bytes", []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := ReadFrame(bytes.NewReader(tc.wire)); err == nil {
				t.Fatal("accepted invalid frame length")
			}
		})
	}
}

func TestFrameReadRejectsShortFrameAndInvalidPacketID(t *testing.T) {
	for _, tc := range []struct {
		name string
		wire []byte
	}{
		{"declared bytes unavailable", []byte{2, 1}},
		{"truncated packet ID", []byte{1, 0x80}},
		{"overflow packet ID", []byte{5, 0xff, 0xff, 0xff, 0xff, 0x1f}},
		{"noncanonical packet ID", []byte{2, 0x81, 0x00}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := ReadFrame(bytes.NewReader(tc.wire)); err == nil {
				t.Fatal("accepted malformed frame")
			}
		})
	}
}

func TestFrameReadAcceptsPacketIDOnly(t *testing.T) {
	id, payload, err := ReadFrame(bytes.NewReader([]byte{1, 7}))
	if err != nil || id != 7 || payload == nil || len(payload) != 0 {
		t.Fatalf("frame = id %d, payload %#v, err %v", id, payload, err)
	}
}

func TestFrameReadOwnsSingleFrameAllocation(t *testing.T) {
	packetOnly := []byte{1, 7}
	var packetOnlyReader bytes.Reader
	ownedFrameAllocs := testing.AllocsPerRun(100, func() {
		packetOnlyReader.Reset(packetOnly)
		id, got, err := ReadFrame(&packetOnlyReader)
		if err != nil {
			panic(err)
		}
		frameReadSink.id = id
		frameReadSink.payload = got
	})

	payload := bytes.Repeat([]byte{0x5a}, 192<<10)
	var wire bytes.Buffer
	if err := WriteFrame(&wire, 128, payload); err != nil {
		t.Fatal(err)
	}
	encoded := wire.Bytes()
	var reader bytes.Reader
	allocs := testing.AllocsPerRun(100, func() {
		reader.Reset(encoded)
		id, got, err := ReadFrame(&reader)
		if err != nil {
			panic(err)
		}
		frameReadSink.id = id
		frameReadSink.payload = got
	})
	if allocs != ownedFrameAllocs {
		t.Fatalf("large ReadFrame allocations = %.0f, packet-ID-only owned frame = %.0f", allocs, ownedFrameAllocs)
	}
	if frameReadSink.id != 128 || !bytes.Equal(frameReadSink.payload, payload) {
		t.Fatalf("frame = id %d, payload length %d", frameReadSink.id, len(frameReadSink.payload))
	}
	clear(encoded)
	if !bytes.Equal(frameReadSink.payload, payload) {
		t.Fatal("returned payload aliases reader backing bytes")
	}
}

func TestFrameWriteHandlesShortWritesAndBounds(t *testing.T) {
	t.Run("short writer", func(t *testing.T) {
		var writer oneByteWriter
		if err := WriteFrame(&writer, 128, []byte{1, 2, 3}); err != nil {
			t.Fatal(err)
		}
		if got, want := writer.Bytes(), []byte{5, 0x80, 0x01, 1, 2, 3}; !bytes.Equal(got, want) {
			t.Fatalf("wire = %x, want %x", got, want)
		}
	})

	t.Run("stalled writer", func(t *testing.T) {
		err := WriteFrame(stalledWriter{}, 1, nil)
		if !errors.Is(err, io.ErrShortWrite) {
			t.Fatalf("error = %v, want io.ErrShortWrite", err)
		}
	})

	t.Run("maximum payload", func(t *testing.T) {
		payload := make([]byte, MaxFrameBytes-1)
		if err := WriteFrame(io.Discard, 0, payload); err != nil {
			t.Fatalf("maximum payload rejected: %v", err)
		}
	})

	t.Run("payload making frame too large", func(t *testing.T) {
		if err := WriteFrame(io.Discard, 0, make([]byte, MaxFrameBytes)); err == nil {
			t.Fatal("accepted payload whose packet ID makes frame too large")
		}
	})
}
