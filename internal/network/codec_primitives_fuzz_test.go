package network

import "testing"

func FuzzPrimitiveDecoder(f *testing.F) {
	for _, seed := range [][]byte{
		nil,
		{0},
		{1},
		{0x80},
		{0xff, 0xff, 0xff, 0xff, 0x1f},
		{2, 'o', 'k'},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		reads := []func(*byteDecoder) error{
			func(d *byteDecoder) error { _, err := d.uvarint(); return err },
			func(d *byteDecoder) error { _, err := d.u8(); return err },
			func(d *byteDecoder) error { _, err := d.i8(); return err },
			func(d *byteDecoder) error { _, err := d.u16(); return err },
			func(d *byteDecoder) error { _, err := d.u32(); return err },
			func(d *byteDecoder) error { _, err := d.i32(); return err },
			func(d *byteDecoder) error { _, err := d.u64(); return err },
			func(d *byteDecoder) error { _, err := d.bool(); return err },
			func(d *byteDecoder) error { _, err := d.string(1024, 256); return err },
			func(d *byteDecoder) error { _, err := d.f32(); return err },
		}
		for _, read := range reads {
			d := byteDecoder{data: data}
			_ = read(&d)
			if d.offset < 0 || d.offset > len(data) {
				t.Fatalf("decoder offset %d outside input length %d", d.offset, len(data))
			}
		}
	})
}
