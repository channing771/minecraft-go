package network

import (
	"encoding/binary"
	"errors"
	"math"
	"unicode/utf8"
)

var (
	errInvalidUvarint = errors.New("network: invalid uvarint")
	errShortInput     = errors.New("network: short input")
	errInvalidBool    = errors.New("network: invalid boolean")
	errInvalidString  = errors.New("network: invalid string")
	errInvalidFloat   = errors.New("network: invalid float32")
	errTrailingBytes  = errors.New("network: trailing bytes")
)

type byteEncoder struct {
	data []byte
	err  error
}

func (e *byteEncoder) fail(err error) {
	if e.err == nil {
		e.err = err
	}
}

func (e *byteEncoder) uvarint(value uint32) {
	if e.err != nil {
		return
	}
	for value >= 1<<7 {
		e.data = append(e.data, byte(value)|0x80)
		value >>= 7
	}
	e.data = append(e.data, byte(value))
}

func (e *byteEncoder) u8(value uint8) {
	if e.err == nil {
		e.data = append(e.data, value)
	}
}

func (e *byteEncoder) i8(value int8) { e.u8(uint8(value)) }

func (e *byteEncoder) u16(value uint16) {
	if e.err != nil {
		return
	}
	e.data = binary.LittleEndian.AppendUint16(e.data, value)
}

func (e *byteEncoder) u32(value uint32) {
	if e.err != nil {
		return
	}
	e.data = binary.LittleEndian.AppendUint32(e.data, value)
}

func (e *byteEncoder) i32(value int32) { e.u32(uint32(value)) }

func (e *byteEncoder) u64(value uint64) {
	if e.err != nil {
		return
	}
	e.data = binary.LittleEndian.AppendUint64(e.data, value)
}

func (e *byteEncoder) bool(value bool) {
	if value {
		e.u8(1)
		return
	}
	e.u8(0)
}

func (e *byteEncoder) string(value string, maxBytes int) {
	if e.err != nil {
		return
	}
	if maxBytes < 0 || len(value) > maxBytes || !utf8.ValidString(value) {
		e.fail(errInvalidString)
		return
	}
	e.uvarint(uint32(len(value)))
	if e.err == nil {
		e.data = append(e.data, value...)
	}
}

func (e *byteEncoder) f32(value float32) {
	if e.err != nil {
		return
	}
	if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
		e.fail(errInvalidFloat)
		return
	}
	e.u32(math.Float32bits(value))
}

type byteDecoder struct {
	data   []byte
	offset int
}

func (d *byteDecoder) take(n int) ([]byte, error) {
	if d.offset < 0 || d.offset > len(d.data) || n < 0 || n > len(d.data)-d.offset {
		return nil, errShortInput
	}
	result := d.data[d.offset : d.offset+n]
	d.offset += n
	return result, nil
}

func canonicalUvarintLength(value uint32) int {
	switch {
	case value < 1<<7:
		return 1
	case value < 1<<14:
		return 2
	case value < 1<<21:
		return 3
	case value < 1<<28:
		return 4
	default:
		return 5
	}
}

func (d *byteDecoder) uvarint() (uint32, error) {
	var value uint32
	for i := 0; i < 5; i++ {
		part, err := d.take(1)
		if err != nil {
			return 0, errInvalidUvarint
		}
		b := part[0]
		if i == 4 && b&0xf0 != 0 {
			return 0, errInvalidUvarint
		}
		value |= uint32(b&0x7f) << (7 * i)
		if b&0x80 == 0 {
			if canonicalUvarintLength(value) != i+1 {
				return 0, errInvalidUvarint
			}
			return value, nil
		}
	}
	return 0, errInvalidUvarint
}

func (d *byteDecoder) u8() (uint8, error) {
	data, err := d.take(1)
	if err != nil {
		return 0, err
	}
	return data[0], nil
}

func (d *byteDecoder) i8() (int8, error) {
	value, err := d.u8()
	return int8(value), err
}

func (d *byteDecoder) u16() (uint16, error) {
	data, err := d.take(2)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint16(data), nil
}

func (d *byteDecoder) u32() (uint32, error) {
	data, err := d.take(4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(data), nil
}

func (d *byteDecoder) i32() (int32, error) {
	value, err := d.u32()
	return int32(value), err
}

func (d *byteDecoder) u64() (uint64, error) {
	data, err := d.take(8)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(data), nil
}

func (d *byteDecoder) bool() (bool, error) {
	value, err := d.u8()
	if err != nil {
		return false, err
	}
	switch value {
	case 0:
		return false, nil
	case 1:
		return true, nil
	default:
		return false, errInvalidBool
	}
}

func (d *byteDecoder) string(maxBytes, maxRunes int) (string, error) {
	if maxBytes < 0 || maxRunes < 0 {
		return "", errInvalidString
	}
	length, err := d.uvarint()
	if err != nil {
		return "", err
	}
	if uint64(length) > uint64(maxBytes) || uint64(length) > uint64(len(d.data)-d.offset) {
		return "", errInvalidString
	}
	data, err := d.take(int(length))
	if err != nil {
		return "", err
	}
	if !utf8.Valid(data) || utf8.RuneCount(data) > maxRunes {
		return "", errInvalidString
	}
	return string(data), nil
}

func (d *byteDecoder) f32() (float32, error) {
	bits, err := d.u32()
	if err != nil {
		return 0, err
	}
	value := math.Float32frombits(bits)
	if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
		return 0, errInvalidFloat
	}
	return value, nil
}

func (d *byteDecoder) done() error {
	if d.offset != len(d.data) {
		return errTrailingBytes
	}
	return nil
}
