package network

import (
	"bytes"
	"math"
	"testing"
)

func TestCanonicalUvarintBoundaries(t *testing.T) {
	tests := []struct {
		value   uint32
		encoded []byte
	}{
		{0, []byte{0x00}},
		{1, []byte{0x01}},
		{127, []byte{0x7f}},
		{128, []byte{0x80, 0x01}},
		{math.MaxUint32, []byte{0xff, 0xff, 0xff, 0xff, 0x0f}},
	}
	for _, tc := range tests {
		var e byteEncoder
		e.uvarint(tc.value)
		if !bytes.Equal(e.data, tc.encoded) {
			t.Fatalf("%d => %x", tc.value, e.data)
		}
		d := byteDecoder{data: tc.encoded}
		got, err := d.uvarint()
		if err != nil || got != tc.value || d.done() != nil {
			t.Fatalf("got=%d err=%v", got, err)
		}
	}
	for _, bad := range [][]byte{{0x80}, {0x81, 0x00}, {0xff, 0xff, 0xff, 0xff, 0x1f}} {
		d := byteDecoder{data: bad}
		if _, err := d.uvarint(); err == nil {
			t.Fatalf("accepted %x", bad)
		}
	}
}

func TestPrimitiveFixedWidthEncodingIsLittleEndian(t *testing.T) {
	var e byteEncoder
	e.u8(0xab)
	e.i8(-2)
	e.u16(0x1234)
	e.u32(0x12345678)
	e.i32(-2)
	e.u64(0x0123456789abcdef)
	want := []byte{0xab, 0xfe, 0x34, 0x12, 0x78, 0x56, 0x34, 0x12, 0xfe, 0xff, 0xff, 0xff, 0xef, 0xcd, 0xab, 0x89, 0x67, 0x45, 0x23, 0x01}
	if !bytes.Equal(e.data, want) {
		t.Fatalf("encoding = %x, want %x", e.data, want)
	}
}

func TestPrimitiveFixedWidthDecodingIsLittleEndian(t *testing.T) {
	d := byteDecoder{data: []byte{0xab, 0xfe, 0x34, 0x12, 0x78, 0x56, 0x34, 0x12, 0xfe, 0xff, 0xff, 0xff, 0xef, 0xcd, 0xab, 0x89, 0x67, 0x45, 0x23, 0x01}}
	if got, err := d.u8(); err != nil || got != 0xab {
		t.Fatalf("u8 = %x, %v", got, err)
	}
	if got, err := d.i8(); err != nil || got != -2 {
		t.Fatalf("i8 = %d, %v", got, err)
	}
	if got, err := d.u16(); err != nil || got != 0x1234 {
		t.Fatalf("u16 = %x, %v", got, err)
	}
	if got, err := d.u32(); err != nil || got != 0x12345678 {
		t.Fatalf("u32 = %x, %v", got, err)
	}
	if got, err := d.i32(); err != nil || got != -2 {
		t.Fatalf("i32 = %d, %v", got, err)
	}
	if got, err := d.u64(); err != nil || got != 0x0123456789abcdef {
		t.Fatalf("u64 = %x, %v", got, err)
	}
	if err := d.done(); err != nil {
		t.Fatalf("done = %v", err)
	}
}

func TestPrimitiveFixedWidthRejectsShortInput(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input []byte
		read  func(*byteDecoder) error
	}{
		{"u8", nil, func(d *byteDecoder) error { _, err := d.u8(); return err }},
		{"i8", nil, func(d *byteDecoder) error { _, err := d.i8(); return err }},
		{"u16", []byte{0}, func(d *byteDecoder) error { _, err := d.u16(); return err }},
		{"u32", []byte{0, 0, 0}, func(d *byteDecoder) error { _, err := d.u32(); return err }},
		{"i32", []byte{0, 0, 0}, func(d *byteDecoder) error { _, err := d.i32(); return err }},
		{"u64", []byte{0, 0, 0, 0, 0, 0, 0}, func(d *byteDecoder) error { _, err := d.u64(); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.read(&byteDecoder{data: tc.input}); err == nil {
				t.Fatal("accepted short input")
			}
		})
	}
}

func TestPrimitiveBoolOnlyAcceptsZeroOrOne(t *testing.T) {
	for _, tc := range []struct {
		input []byte
		want  bool
		err   bool
	}{
		{[]byte{0}, false, false},
		{[]byte{1}, true, false},
		{[]byte{2}, false, true},
		{[]byte{0xff}, false, true},
	} {
		d := byteDecoder{data: tc.input}
		got, err := d.bool()
		if (err != nil) != tc.err || (!tc.err && got != tc.want) {
			t.Fatalf("bool(%x) = %t, %v", tc.input, got, err)
		}
	}
}

func TestPrimitiveStringRejectsInvalidOrOversizedInput(t *testing.T) {
	for _, tc := range []struct {
		name     string
		input    []byte
		maxBytes int
		maxRunes int
	}{
		{"declared bytes exceed maximum", []byte{3, 'a', 'b', 'c'}, 2, 3},
		{"declared bytes exceed remaining input", []byte{5, 'a'}, 5, 5},
		{"invalid UTF-8", []byte{1, 0xff}, 1, 1},
		{"too many runes", []byte{2, 'a', 'b'}, 2, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := byteDecoder{data: tc.input}
			if _, err := d.string(tc.maxBytes, tc.maxRunes); err == nil {
				t.Fatal("accepted invalid string")
			}
		})
	}

	var e byteEncoder
	e.string("\xff", 2)
	before := len(e.data)
	e.u8(1)
	if e.err == nil || len(e.data) != before {
		t.Fatal("invalid encoder string did not preserve first error and stop")
	}
}

func TestPrimitiveFloatRejectsNonFiniteValues(t *testing.T) {
	for _, bits := range []uint32{0x7fc00000, 0x7f800000, 0xff800000} {
		d := byteDecoder{data: []byte{byte(bits), byte(bits >> 8), byte(bits >> 16), byte(bits >> 24)}}
		if _, err := d.f32(); err == nil {
			t.Fatalf("accepted non-finite bits %08x", bits)
		}
	}

	var e byteEncoder
	e.f32(float32(math.Inf(1)))
	before := len(e.data)
	e.u8(1)
	if e.err == nil || len(e.data) != before {
		t.Fatal("non-finite encoder float did not preserve first error and stop")
	}
}

func TestPrimitiveDoneRejectsTrailingBytes(t *testing.T) {
	d := byteDecoder{data: []byte{1}}
	if err := d.done(); err == nil {
		t.Fatal("accepted trailing bytes")
	}
}
