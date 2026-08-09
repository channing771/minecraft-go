package storage

import (
	"encoding/binary"
	"fmt"
	"io"
)

func appendU32(dst []byte, value uint32) []byte {
	return binary.LittleEndian.AppendUint32(dst, value)
}

func appendU64(dst []byte, value uint64) []byte {
	return binary.LittleEndian.AppendUint64(dst, value)
}

type byteDecoder struct {
	data   []byte
	offset int
}

func (d *byteDecoder) u8() (uint8, error) {
	bytes, err := d.take(1)
	if err != nil {
		return 0, err
	}
	return bytes[0], nil
}

func (d *byteDecoder) u16() (uint16, error) {
	bytes, err := d.take(2)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint16(bytes), nil
}

func (d *byteDecoder) u32() (uint32, error) {
	if d.remaining() < 4 {
		return 0, io.ErrUnexpectedEOF
	}
	value := binary.LittleEndian.Uint32(d.data[d.offset:])
	d.offset += 4
	return value, nil
}

func (d *byteDecoder) u64() (uint64, error) {
	if d.remaining() < 8 {
		return 0, io.ErrUnexpectedEOF
	}
	value := binary.LittleEndian.Uint64(d.data[d.offset:])
	d.offset += 8
	return value, nil
}

func (d *byteDecoder) magic(want [4]byte) error {
	got, err := d.take(len(want))
	if err != nil {
		return err
	}
	for index := range want {
		if got[index] != want[index] {
			return fmt.Errorf("unexpected magic")
		}
	}
	return nil
}

func (d *byteDecoder) take(length int) ([]byte, error) {
	if length < 0 || length > d.remaining() {
		return nil, io.ErrUnexpectedEOF
	}
	value := d.data[d.offset : d.offset+length]
	d.offset += length
	return value, nil
}

func (d *byteDecoder) remaining() int {
	return len(d.data) - d.offset
}

func corrupt(field string, err error) error {
	return fmt.Errorf("%w: %s: %v", ErrCorrupt, field, err)
}
