package network

import (
	"bytes"
	"encoding/binary"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"

	"minecraft-go/internal/core"
)

var updateProtocolFixtures = flag.Bool(
	"update-protocol-fixtures", false, "重写已提交的协议 fixture",
)

func TestChunkSnapshotV1Fixture(t *testing.T) {
	snapshot := fixtureSnapshot(core.ChunkPos{X: -3, Z: 7}, 19)
	codec, err := NewCodec()
	if err != nil {
		t.Fatal(err)
	}
	defer codec.Close()

	id, encoded, err := codec.EncodeServer(StatePlay, snapshot)
	if err != nil || id != 0 {
		t.Fatalf("id=%d err=%v", id, err)
	}
	path := filepath.Join("testdata", "chunk-snapshot-v1.bin")
	if *updateProtocolFixtures {
		if err := os.WriteFile(path, encoded, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, want) {
		t.Fatal("v1 snapshot fixture drift; bump ProtocolVersion")
	}
	round, err := codec.DecodeServer(StatePlay, id, want)
	if err != nil || !reflect.DeepEqual(round, snapshot) {
		t.Fatalf("round=%+v err=%v", round, err)
	}
}

func TestChunkSnapshotLogicalWireCoversAllStorages(t *testing.T) {
	snapshot := fixtureSnapshot(core.ChunkPos{X: -3, Z: 7}, 19)
	logical, err := encodeLogicalSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := testLogicalSnapshot(snapshot)
	if !bytes.Equal(logical, want) {
		t.Fatalf("logical wire mismatch\n got=%x\nwant=%x", logical, want)
	}
	round, err := decodeLogicalSnapshot(logical)
	if err != nil || !reflect.DeepEqual(round, snapshot) {
		t.Fatalf("round=%+v err=%v", round, err)
	}
}

func TestLogicalSnapshotSizeMatchesWire(t *testing.T) {
	indexed := func(bits uint8, paletteSize int) ChunkSnapshot {
		palette := make([]core.BlockID, paletteSize)
		for index := range palette {
			palette[index] = core.BlockID(index)
		}
		return repeatedSnapshot(SectionData{
			Storage: SectionIndexed,
			Bits:    bits,
			Palette: palette,
			Packed:  testPacked(bits, paletteSize, 0),
		})
	}
	direct := repeatedSnapshot(SectionData{Storage: SectionSingle})
	direct.Sections[0] = SectionData{
		Y:       0,
		Storage: SectionDirect,
		Bits:    15,
		Packed:  testPacked(15, int(core.MossyCobblestoneID)+1, 0),
	}

	tests := []struct {
		name     string
		snapshot ChunkSnapshot
		want     int
	}{
		{"all single", repeatedSnapshot(SectionData{Storage: SectionSingle}), 117},
		{"all 4-bit indexed", indexed(4, 16), 50085},
		{"all 8-bit indexed palette 25", indexed(8, 25), 99669},
		{"all 8-bit indexed palette 26", indexed(8, 26), 99717},
		{"direct", direct, 8310},
		{"all storages", fixtureSnapshot(core.ChunkPos{X: -3, Z: 7}, 19), 86271},
		{"worst legal", worstLegalBenchmarkSnapshot(), 196749},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			logical, err := encodeLogicalSnapshot(tc.snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if got := logicalSnapshotSize(tc.snapshot); got != tc.want || got != len(logical) {
				t.Fatalf("logicalSnapshotSize = %d，wire = %d，想要 %d", got, len(logical), tc.want)
			}
		})
	}
}

func TestWorstLegalLogicalSnapshotHasOneExactAllocation(t *testing.T) {
	snapshot := worstLegalBenchmarkSnapshot()
	want := 196749
	allocs := testing.AllocsPerRun(100, func() {
		logical, err := encodeLogicalSnapshot(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		if len(logical) != want || cap(logical) != want {
			t.Fatalf("logical len/cap = %d/%d，想要 %d/%d", len(logical), cap(logical), want, want)
		}
		benchmarkPayload = logical
	})
	if allocs != 1 {
		t.Fatalf("encodeLogicalSnapshot allocations = %.0f，想要 1", allocs)
	}
}

func TestChunkSnapshotEncodeRejectsNonOverworldDimension(t *testing.T) {
	codec := mustNewCodec(t)
	defer codec.Close()
	snapshot := fixtureSnapshot(core.ChunkPos{X: -3, Z: 7}, 19)
	snapshot.Dimension = core.DimensionID(1)

	if _, _, err := codec.EncodeServer(StatePlay, snapshot); err == nil {
		t.Fatal("non-Overworld snapshot encoded")
	} else if !strings.Contains(err.Error(), "snapshot") || !strings.Contains(err.Error(), "dimension") {
		t.Fatalf("error %q lacks snapshot/dimension context", err)
	}
}

func TestChunkSnapshotDecodeRejectsNonOverworldDimension(t *testing.T) {
	codec := mustNewCodec(t)
	defer codec.Close()
	snapshot := fixtureSnapshot(core.ChunkPos{X: -3, Z: 7}, 19)
	snapshot.Dimension = core.DimensionID(1)
	logical, _ := testLogicalSnapshot(snapshot)
	payload := testZstdEnvelope(t, logical, uint32(len(logical)))

	if packet, err := codec.DecodeServer(StatePlay, 0, payload); err == nil {
		got := packet.(ChunkSnapshot)
		t.Fatalf("non-Overworld logical payload decoded with dimension %d", got.Dimension)
	} else if !strings.Contains(err.Error(), "snapshot") || !strings.Contains(err.Error(), "dimension") {
		t.Fatalf("error %q lacks snapshot/dimension context", err)
	} else if strings.Contains(err.Error(), string(logical)) {
		t.Fatalf("error contains raw logical payload: %q", err)
	}
}

func TestSnapshotBounds(t *testing.T) {
	codec := mustNewCodec(t)
	defer codec.Close()

	t.Run("compressed length limit minus one and limit reach zstd", func(t *testing.T) {
		for _, size := range []int{MaxCompressedSnapshot - 1, MaxCompressedSnapshot} {
			payload := testEnvelope(1, bytes.Repeat([]byte{0}, size))
			if _, err := codec.DecodeServer(StatePlay, 0, payload); err == nil {
				t.Fatalf("invalid %d-byte zstd payload accepted", size)
			} else if strings.Contains(err.Error(), "compressed length") && strings.Contains(err.Error(), "exceeds") {
				t.Fatalf("compressed length %d rejected as over limit: %v", size, err)
			}
		}
	})

	t.Run("compressed length limit plus one is rejected before zstd", func(t *testing.T) {
		payload := testEnvelope(1, make([]byte, MaxCompressedSnapshot+1))
		if _, err := codec.DecodeServer(StatePlay, 0, payload); err == nil ||
			!strings.Contains(err.Error(), "compressed length") {
			t.Fatalf("error=%v; want compressed length rejection", err)
		}
	})

	t.Run("decoded length limit minus one and limit are admitted", func(t *testing.T) {
		for _, size := range []int{MaxDecodedSnapshot - 1, MaxDecodedSnapshot} {
			payload := testZstdEnvelope(t, bytes.Repeat([]byte{0}, size), uint32(size))
			if _, err := codec.DecodeServer(StatePlay, 0, payload); err == nil {
				t.Fatalf("non-snapshot decoded payload of size %d accepted", size)
			} else if strings.Contains(err.Error(), "decoded length") && strings.Contains(err.Error(), "exceeds") {
				t.Fatalf("decoded length %d rejected as over limit: %v", size, err)
			}
		}
	})

	t.Run("decoded length limit plus one is rejected before zstd", func(t *testing.T) {
		payload := testEnvelope(MaxDecodedSnapshot+1, []byte{0})
		if _, err := codec.DecodeServer(StatePlay, 0, payload); err == nil ||
			!strings.Contains(err.Error(), "decoded length") {
			t.Fatalf("error=%v; want decoded length rejection", err)
		}
	})

	t.Run("small payload limit", func(t *testing.T) {
		for _, size := range []int{MaxSmallPayload - 1, MaxSmallPayload} {
			if _, err := codec.DecodeClient(StateHandshake, 99, make([]byte, size)); err == nil {
				t.Fatalf("unknown packet with %d-byte payload accepted", size)
			} else if strings.Contains(err.Error(), "exceeds 64 KiB") {
				t.Fatalf("small payload size %d rejected as over limit: %v", size, err)
			}
		}
		if _, err := codec.DecodeClient(StateHandshake, 99, make([]byte, MaxSmallPayload+1)); err == nil ||
			!strings.Contains(err.Error(), "exceeds 64 KiB") {
			t.Fatalf("error=%v; want small payload limit rejection", err)
		}
	})
}

func TestChunkSnapshotRejectsMalformedEnvelope(t *testing.T) {
	codec := mustNewCodec(t)
	defer codec.Close()
	logical, _ := testLogicalSnapshot(fixtureSnapshot(core.ChunkPos{X: -3, Z: 7}, 19))
	valid := testZstdEnvelope(t, logical, uint32(len(logical)))

	tests := []struct {
		name    string
		payload func() []byte
	}{
		{"short header", func() []byte { return valid[:7] }},
		{"compressed length mismatch", func() []byte {
			got := append([]byte(nil), valid...)
			binary.LittleEndian.PutUint32(got[4:8], binary.LittleEndian.Uint32(got[4:8])+1)
			return got
		}},
		{"outer trailing byte", func() []byte { return append(append([]byte(nil), valid...), 0) }},
		{"actual decoded shorter than declared", func() []byte {
			got := append([]byte(nil), valid...)
			binary.LittleEndian.PutUint32(got[:4], uint32(len(logical)+1))
			return got
		}},
		{"actual decoded longer than declared", func() []byte {
			got := append([]byte(nil), valid...)
			binary.LittleEndian.PutUint32(got[:4], uint32(len(logical)-1))
			return got
		}},
		{"zstd checksum corruption", func() []byte {
			got := append([]byte(nil), valid...)
			got[len(got)-1] ^= 0xff
			return got
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if packet, err := codec.DecodeServer(StatePlay, 0, tc.payload()); err == nil {
				t.Fatalf("malformed envelope decoded as %#v", packet)
			}
		})
	}
}

func TestChunkSnapshotRejectsMalformedLogicalPayload(t *testing.T) {
	codec := mustNewCodec(t)
	defer codec.Close()
	logical, offsets := testLogicalSnapshot(fixtureSnapshot(core.ChunkPos{X: -3, Z: 7}, 19))

	tests := []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{"section count", func(data []byte) []byte { data[20] = core.SectionsPerChunk - 1; return data }},
		{"section order", func(data []byte) []byte { data[offsets[4].sectionY] = 5; return data }},
		{"unknown storage", func(data []byte) []byte { data[offsets[0].storage] = 3; return data }},
		{"indexed bits", func(data []byte) []byte { data[offsets[1].bits] = 5; return data }},
		{"palette count before allocation", func(data []byte) []byte { data[offsets[1].paletteCount] = 17; return data }},
		{"duplicate palette block ID", func(data []byte) []byte {
			copy(data[offsets[1].firstPalette+2:offsets[1].firstPalette+4], data[offsets[1].firstPalette:offsets[1].firstPalette+2])
			return data
		}},
		{"invalid palette block ID", func(data []byte) []byte {
			binary.LittleEndian.PutUint16(data[offsets[1].firstPalette:], 1<<15)
			return data
		}},
		{"invalid palette slot", func(data []byte) []byte {
			word := binary.LittleEndian.Uint64(data[offsets[1].firstWord:])
			binary.LittleEndian.PutUint64(data[offsets[1].firstWord:], word|0xf)
			return data
		}},
		{"indexed word count before allocation", func(data []byte) []byte { data[offsets[1].wordCount] = 0; return data }},
		{"direct bits", func(data []byte) []byte { data[offsets[3].bits] = 14; return data }},
		{"direct word count before allocation", func(data []byte) []byte { data[offsets[3].wordCount] = 0; return data }},
		{"direct unused high bits", func(data []byte) []byte {
			word := binary.LittleEndian.Uint64(data[offsets[3].firstWord:])
			binary.LittleEndian.PutUint64(data[offsets[3].firstWord:], word|1<<60)
			return data
		}},
		{"logical trailing byte", func(data []byte) []byte { return append(data, 0) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			malformed := tc.mutate(append([]byte(nil), logical...))
			payload := testZstdEnvelope(t, malformed, uint32(len(malformed)))
			if packet, err := codec.DecodeServer(StatePlay, 0, payload); err == nil {
				t.Fatalf("malformed logical payload decoded as %#v", packet)
			}
		})
	}
}

func TestChunkSnapshotRejectsZstdExpansionBeyondLimit(t *testing.T) {
	codec := mustNewCodec(t)
	defer codec.Close()
	bomb := bytes.Repeat([]byte{0}, MaxDecodedSnapshot+1)
	payload := testZstdEnvelope(t, bomb, MaxDecodedSnapshot)
	if len(payload)-8 > MaxCompressedSnapshot {
		t.Fatalf("test bomb compressed to %d bytes", len(payload)-8)
	}
	if packet, err := codec.DecodeServer(StatePlay, 0, payload); err == nil {
		t.Fatalf("zstd expansion bomb decoded as %#v", packet)
	}
}

func TestChunkSnapshotCodecOwnsSlices(t *testing.T) {
	codec := mustNewCodec(t)
	defer codec.Close()
	want := fixtureSnapshot(core.ChunkPos{X: -3, Z: 7}, 19)
	snapshot := fixtureSnapshot(core.ChunkPos{X: -3, Z: 7}, 19)
	id, encoded, err := codec.EncodeServer(StatePlay, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	stable := append([]byte(nil), encoded...)
	for index := range snapshot.Sections {
		clear(snapshot.Sections[index].Palette)
		clear(snapshot.Sections[index].Packed)
	}
	if !bytes.Equal(encoded, stable) {
		t.Fatal("encoded payload aliases snapshot slices")
	}
	if _, _, err := codec.EncodeServer(StatePlay, fixtureSnapshot(core.ChunkPos{}, 20)); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, stable) {
		t.Fatal("encoded payload aliases codec scratch storage")
	}

	firstPacket, err := codec.DecodeServer(StatePlay, id, stable)
	if err != nil {
		t.Fatal(err)
	}
	first := firstPacket.(ChunkSnapshot)
	for index := range first.Sections {
		clear(first.Sections[index].Palette)
		clear(first.Sections[index].Packed)
	}
	second, err := codec.DecodeServer(StatePlay, id, stable)
	if err != nil || !reflect.DeepEqual(second, want) {
		t.Fatalf("second decode=%+v err=%v; first decode aliases codec/input storage", second, err)
	}
}

func TestCodecDelegatesControlPacketsAndCloseIsIdempotent(t *testing.T) {
	codec := mustNewCodec(t)
	id, payload, err := codec.EncodeClient(StateHandshake, ClientHello{ProtocolVersion: ProtocolVersion})
	if err != nil || id != 0 || !bytes.Equal(payload, []byte{byte(ProtocolVersion)}) {
		t.Fatalf("client encode id=%d payload=%x err=%v", id, payload, err)
	}
	packet, err := codec.DecodeClient(StateHandshake, id, payload)
	if err != nil || !reflect.DeepEqual(packet, ClientHello{ProtocolVersion: ProtocolVersion}) {
		t.Fatalf("client decode=%#v err=%v", packet, err)
	}
	serverID, serverPayload, err := codec.EncodeServer(StatePlay, KeepAlive{Token: 7})
	if err != nil || serverID != 5 {
		t.Fatalf("server encode id=%d payload=%x err=%v", serverID, serverPayload, err)
	}
	if _, err := codec.DecodeServer(StatePlay, serverID, serverPayload); err != nil {
		t.Fatal(err)
	}
	if err := codec.Close(); err != nil {
		t.Fatal(err)
	}
	if err := codec.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func fixtureSnapshot(pos core.ChunkPos, revision uint64) ChunkSnapshot {
	sections := make([]SectionData, core.SectionsPerChunk)
	for y := range sections {
		switch y % 4 {
		case 0:
			sections[y] = SectionData{Y: int32(y), Storage: SectionSingle, Single: core.BlockID(y % 6)}
		case 1:
			palette := []core.BlockID{core.AirID, core.StoneID, core.DirtID}
			sections[y] = SectionData{Y: int32(y), Storage: SectionIndexed, Bits: 4, Palette: palette, Packed: testPacked(4, len(palette), y)}
		case 2:
			palette := []core.BlockID{core.AirID, core.BarrierID, core.StoneID, core.DirtID, core.GrassID, core.BedrockID}
			sections[y] = SectionData{Y: int32(y), Storage: SectionIndexed, Bits: 8, Palette: palette, Packed: testPacked(8, len(palette), y)}
		case 3:
			sections[y] = SectionData{Y: int32(y), Storage: SectionDirect, Bits: 15, Packed: testPacked(15, int(core.MossyCobblestoneID)+1, y)}
		}
	}
	return ChunkSnapshot{Dimension: core.Overworld, Chunk: pos, Revision: revision, Sections: sections}
}

func repeatedSnapshot(section SectionData) ChunkSnapshot {
	sections := make([]SectionData, core.SectionsPerChunk)
	for index := range sections {
		section.Y = int32(index)
		sections[index] = section
	}
	return ChunkSnapshot{Dimension: core.Overworld, Revision: 1, Sections: sections}
}

func testPacked(bits uint8, modulus, seed int) []uint64 {
	perWord := 64 / int(bits)
	words := make([]uint64, (core.BlocksPerSection+perWord-1)/perWord)
	for index := 0; index < core.BlocksPerSection; index++ {
		value := uint64((index + seed) % modulus)
		words[index/perWord] |= value << uint((index%perWord)*int(bits))
	}
	return words
}

type testSectionWireOffsets struct {
	sectionY, storage, bits    int
	paletteCount, firstPalette int
	wordCount, firstWord       int
}

func testLogicalSnapshot(snapshot ChunkSnapshot) ([]byte, map[int]testSectionWireOffsets) {
	data := make([]byte, 0, 200000)
	data = binary.LittleEndian.AppendUint32(data, uint32(snapshot.Dimension))
	data = binary.LittleEndian.AppendUint32(data, uint32(snapshot.Chunk.X))
	data = binary.LittleEndian.AppendUint32(data, uint32(snapshot.Chunk.Z))
	data = binary.LittleEndian.AppendUint64(data, snapshot.Revision)
	data = appendTestUvarint(data, uint32(len(snapshot.Sections)))
	offsets := make(map[int]testSectionWireOffsets, len(snapshot.Sections))
	for _, section := range snapshot.Sections {
		offset := testSectionWireOffsets{sectionY: len(data)}
		data = append(data, byte(section.Y))
		offset.storage = len(data)
		data = append(data, byte(section.Storage))
		switch section.Storage {
		case SectionSingle:
			data = binary.LittleEndian.AppendUint16(data, uint16(section.Single))
		case SectionIndexed:
			offset.bits = len(data)
			data = append(data, section.Bits)
			offset.paletteCount = len(data)
			data = appendTestUvarint(data, uint32(len(section.Palette)))
			offset.firstPalette = len(data)
			for _, id := range section.Palette {
				data = binary.LittleEndian.AppendUint16(data, uint16(id))
			}
			offset.wordCount = len(data)
			data = appendTestUvarint(data, uint32(len(section.Packed)))
			offset.firstWord = len(data)
			for _, word := range section.Packed {
				data = binary.LittleEndian.AppendUint64(data, word)
			}
		case SectionDirect:
			offset.bits = len(data)
			data = append(data, section.Bits)
			offset.wordCount = len(data)
			data = appendTestUvarint(data, uint32(len(section.Packed)))
			offset.firstWord = len(data)
			for _, word := range section.Packed {
				data = binary.LittleEndian.AppendUint64(data, word)
			}
		}
		offsets[int(section.Y)] = offset
	}
	return data, offsets
}

func appendTestUvarint(dst []byte, value uint32) []byte {
	for value >= 1<<7 {
		dst = append(dst, byte(value)|0x80)
		value >>= 7
	}
	return append(dst, byte(value))
}

func testEnvelope(decodedLength uint32, compressed []byte) []byte {
	payload := make([]byte, 0, 8+len(compressed))
	payload = binary.LittleEndian.AppendUint32(payload, decodedLength)
	payload = binary.LittleEndian.AppendUint32(payload, uint32(len(compressed)))
	payload = append(payload, compressed...)
	return payload
}

func testZstdEnvelope(t *testing.T, decoded []byte, declared uint32) []byte {
	t.Helper()
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1), zstd.WithEncoderCRC(true))
	if err != nil {
		t.Fatal(err)
	}
	compressed := encoder.EncodeAll(decoded, nil)
	if err := encoder.Close(); err != nil {
		t.Fatal(err)
	}
	return testEnvelope(declared, compressed)
}

func mustNewCodec(t *testing.T) *Codec {
	t.Helper()
	codec, err := NewCodec()
	if err != nil {
		t.Fatal(err)
	}
	return codec
}
