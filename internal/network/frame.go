package network

import (
	"fmt"
	"io"
)

// MaxFrameBytes is the largest combined canonical packet ID and payload.
const MaxFrameBytes = 2 << 20

// WriteFrame writes a length-prefixed packet ID and payload. The length prefix
// does not include itself.
func WriteFrame(w io.Writer, packetID uint32, payload []byte) error {
	var idEncoder byteEncoder
	idEncoder.uvarint(packetID)
	id := idEncoder.data

	frameLength := uint64(len(id)) + uint64(len(payload))
	if frameLength == 0 || frameLength > MaxFrameBytes {
		return fmt.Errorf("network: frame length %d exceeds bounds", frameLength)
	}

	var lengthEncoder byteEncoder
	lengthEncoder.uvarint(uint32(frameLength))
	if err := writeFull(w, lengthEncoder.data); err != nil {
		return err
	}
	if err := writeFull(w, id); err != nil {
		return err
	}
	return writeFull(w, payload)
}

// ReadFrame reads one bounded length-prefixed packet. It never allocates the
// declared frame storage until the canonical frame length has been validated.
func ReadFrame(r io.Reader) (packetID uint32, payload []byte, err error) {
	frameLength, err := readCanonicalUvarint(r)
	if err != nil {
		return 0, nil, err
	}
	if frameLength == 0 || frameLength > MaxFrameBytes {
		return 0, nil, fmt.Errorf("network: frame length %d exceeds bounds", frameLength)
	}

	frame := make([]byte, int(frameLength))
	if _, err := io.ReadFull(r, frame); err != nil {
		return 0, nil, err
	}
	id, used, err := decodeCanonicalUvarintPrefix(frame)
	if err != nil {
		return 0, nil, fmt.Errorf("network: packet ID: %w", err)
	}
	return id, frame[used:], nil
}

func writeFull(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if n < 0 || n > len(data) {
			return io.ErrShortWrite
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func readCanonicalUvarint(r io.Reader) (uint32, error) {
	var encoded [5]byte
	for i := range encoded {
		if _, err := io.ReadFull(r, encoded[i:i+1]); err != nil {
			return 0, err
		}
		if encoded[i]&0x80 == 0 {
			value, _, err := decodeCanonicalUvarintPrefix(encoded[:i+1])
			return value, err
		}
	}
	return 0, errInvalidUvarint
}

func decodeCanonicalUvarintPrefix(data []byte) (value uint32, used int, err error) {
	for i := 0; i < len(data) && i < 5; i++ {
		b := data[i]
		if i == 4 && b&0xf0 != 0 {
			return 0, 0, errInvalidUvarint
		}
		value |= uint32(b&0x7f) << (7 * i)
		if b&0x80 == 0 {
			if canonicalUvarintLength(value) != i+1 {
				return 0, 0, errInvalidUvarint
			}
			return value, i + 1, nil
		}
	}
	return 0, 0, errInvalidUvarint
}
