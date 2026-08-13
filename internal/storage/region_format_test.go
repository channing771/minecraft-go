package storage

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math"
	"testing"

	"github.com/channing771/mornlea/internal/core"
)

func TestSuperblockExactLayoutAndRoundTrip(t *testing.T) {
	key := RegionKey{Dimension: core.DimensionID(-7), X: -2, Z: 3}
	encoded := encodeSuperblock(key)

	if string(encoded[0:4]) != "MCGR" {
		t.Fatalf("magic = %q, want MCGR", encoded[0:4])
	}
	fields := []struct {
		name   string
		offset int
		want   uint32
	}{
		{name: "version", offset: 4, want: 1},
		{name: "sector size", offset: 8, want: 4096},
		{name: "dimension", offset: 12, want: 0xfffffff9},
		{name: "region X", offset: 16, want: 0xfffffffe},
		{name: "region Z", offset: 20, want: 3},
		{name: "bank A sector", offset: 24, want: 1},
		{name: "bank B sector", offset: 28, want: 8},
		{name: "bank sectors", offset: 32, want: 7},
		{name: "data start sector", offset: 36, want: 15},
	}
	for _, field := range fields {
		if got := binary.LittleEndian.Uint32(encoded[field.offset:]); got != field.want {
			t.Errorf("%s = %d, want %d", field.name, got, field.want)
		}
	}
	if !allZero(encoded[40:4092]) {
		t.Fatal("superblock reserved bytes are nonzero")
	}
	wantCRC := crc32.Checksum(encoded[:4092], crc32.MakeTable(crc32.Castagnoli))
	if got := binary.LittleEndian.Uint32(encoded[4092:]); got != wantCRC {
		t.Fatalf("CRC32C = %#x, want %#x", got, wantCRC)
	}
	if err := decodeSuperblock(key, encoded[:]); err != nil {
		t.Fatalf("decodeSuperblock: %v", err)
	}
}

func TestSuperblockRejectsCorruption(t *testing.T) {
	key := RegionKey{Dimension: core.Overworld, X: -2, Z: 3}
	valid := encodeSuperblock(key)

	tests := []struct {
		name    string
		bytes   func() []byte
		key     RegionKey
		wantErr error
	}{
		{name: "wrong magic", bytes: func() []byte { return mutateSuperblock(valid, 0, 0xff) }, key: key, wantErr: ErrCorrupt},
		{name: "past version", bytes: func() []byte { return putSuperblockU32(valid, 4, 0) }, key: key, wantErr: ErrCorrupt},
		{name: "future version", bytes: func() []byte { return putSuperblockU32(valid, 4, 2) }, key: key, wantErr: ErrFutureVersion},
		{name: "wrong sector size", bytes: func() []byte { return putSuperblockU32(valid, 8, 2048) }, key: key, wantErr: ErrCorrupt},
		{name: "wrong dimension", bytes: func() []byte { return valid[:] }, key: RegionKey{Dimension: 1, X: -2, Z: 3}, wantErr: ErrCorrupt},
		{name: "wrong X", bytes: func() []byte { return valid[:] }, key: RegionKey{Dimension: core.Overworld, X: -1, Z: 3}, wantErr: ErrCorrupt},
		{name: "wrong Z", bytes: func() []byte { return valid[:] }, key: RegionKey{Dimension: core.Overworld, X: -2, Z: 4}, wantErr: ErrCorrupt},
		{name: "wrong bank A sector", bytes: func() []byte { return putSuperblockU32(valid, 24, 2) }, key: key, wantErr: ErrCorrupt},
		{name: "wrong bank B sector", bytes: func() []byte { return putSuperblockU32(valid, 28, 9) }, key: key, wantErr: ErrCorrupt},
		{name: "wrong bank size", bytes: func() []byte { return putSuperblockU32(valid, 32, 6) }, key: key, wantErr: ErrCorrupt},
		{name: "wrong data start", bytes: func() []byte { return putSuperblockU32(valid, 36, 14) }, key: key, wantErr: ErrCorrupt},
		{name: "nonzero reserved byte", bytes: func() []byte { return mutateSuperblockWithCRC(valid, 40, 1) }, key: key, wantErr: ErrCorrupt},
		{name: "invalid CRC", bytes: func() []byte { return mutateSuperblock(valid, 100, 1) }, key: key, wantErr: ErrCorrupt},
		{name: "short", bytes: func() []byte { return bytes.Clone(valid[:4095]) }, key: key, wantErr: ErrCorrupt},
		{name: "trailing", bytes: func() []byte { return append(bytes.Clone(valid[:]), 0) }, key: key, wantErr: ErrCorrupt},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := decodeSuperblock(tc.key, tc.bytes()); !errors.Is(err, tc.wantErr) {
				t.Fatalf("decodeSuperblock error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestRegionBankRoundTripAndSelection(t *testing.T) {
	key := RegionKey{Dimension: core.Overworld, X: -2, Z: 3}
	want := regionBank{Generation: 9}
	want.Entries[31] = regionEntry{
		OffsetSector: 15, SectorCount: 2, PayloadLength: 5000,
		Revision: 7, PayloadCRC32C: 0x12345678,
	}
	encoded, err := encodeRegionBank(key, want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeRegionBank(key, encoded[:], 17*sectorSize)
	if err != nil || got != want {
		t.Fatalf("decode = %+v, %v", got, err)
	}
	selected, bank, err := selectRegionBank(regionBank{Generation: 8}, nil, got, nil)
	if err != nil || bank != 1 || selected.Generation != 9 {
		t.Fatalf("select = %+v,%d,%v", selected, bank, err)
	}
}

func TestRegionBankExactLayout(t *testing.T) {
	key := RegionKey{Dimension: core.DimensionID(-7), X: -2, Z: 3}
	bank := regionBank{Generation: 9}
	bank.Entries[31] = regionEntry{
		OffsetSector: 15, SectorCount: 2, PayloadLength: 5000,
		Revision: 7, PayloadCRC32C: 0x12345678,
	}
	encoded, err := encodeRegionBank(key, bank)
	if err != nil {
		t.Fatal(err)
	}

	if string(encoded[0:4]) != "MCGB" {
		t.Fatalf("magic = %q, want MCGB", encoded[0:4])
	}
	fields := []struct {
		name   string
		offset int
		want   uint32
	}{
		{name: "version", offset: 4, want: 1},
		{name: "sector size", offset: 8, want: 4096},
		{name: "dimension", offset: 12, want: 0xfffffff9},
		{name: "region X", offset: 16, want: 0xfffffffe},
		{name: "region Z", offset: 20, want: 3},
		{name: "entry count", offset: 32, want: 1024},
		{name: "entry size", offset: 36, want: 24},
		{name: "bank sectors", offset: 40, want: 7},
		{name: "data start sector", offset: 44, want: 15},
	}
	for _, field := range fields {
		if got := binary.LittleEndian.Uint32(encoded[field.offset:]); got != field.want {
			t.Errorf("%s = %d, want %d", field.name, got, field.want)
		}
	}
	if got := binary.LittleEndian.Uint64(encoded[24:32]); got != 9 {
		t.Errorf("generation = %d, want 9", got)
	}
	if !allZero(encoded[48:60]) {
		t.Fatal("bank header reserved bytes are nonzero")
	}

	entryOffset := 64 + 31*24
	if got := binary.LittleEndian.Uint32(encoded[entryOffset:]); got != 15 {
		t.Errorf("entry offset sector = %d, want 15", got)
	}
	if got := binary.LittleEndian.Uint32(encoded[entryOffset+4:]); got != 2 {
		t.Errorf("entry sector count = %d, want 2", got)
	}
	if got := binary.LittleEndian.Uint32(encoded[entryOffset+8:]); got != 5000 {
		t.Errorf("entry payload length = %d, want 5000", got)
	}
	if got := binary.LittleEndian.Uint64(encoded[entryOffset+12:]); got != 7 {
		t.Errorf("entry revision = %d, want 7", got)
	}
	if got := binary.LittleEndian.Uint32(encoded[entryOffset+20:]); got != 0x12345678 {
		t.Errorf("entry payload CRC32C = %#x, want %#x", got, uint32(0x12345678))
	}
	if !allZero(encoded[64+1024*24:]) {
		t.Fatal("bank trailing padding is nonzero")
	}

	checksumInput := bytes.Clone(encoded[:])
	wantCRC := binary.LittleEndian.Uint32(checksumInput[60:64])
	clear(checksumInput[60:64])
	gotCRC := crc32.Checksum(checksumInput, crc32.MakeTable(crc32.Castagnoli))
	if gotCRC != wantCRC {
		t.Fatalf("bank CRC32C = %#x, want %#x", wantCRC, gotCRC)
	}
}

func TestRegionBankAcceptsEmptyGenerationZeroAndMaxGeneration(t *testing.T) {
	key := RegionKey{Dimension: core.Overworld, X: -2, Z: 3}
	for _, generation := range []uint64{0, math.MaxUint64} {
		bank := regionBank{Generation: generation}
		encoded, err := encodeRegionBank(key, bank)
		if err != nil {
			t.Fatalf("encode generation %d: %v", generation, err)
		}
		got, err := decodeRegionBank(key, encoded[:], dataStartSector*sectorSize)
		if err != nil || got != bank {
			t.Fatalf("decode generation %d = %+v, %v", generation, got, err)
		}
	}
}

func TestRegionBankRejectsCorruption(t *testing.T) {
	key := RegionKey{Dimension: core.Overworld, X: -2, Z: 3}
	base := regionBank{Generation: 9}
	base.Entries[0] = regionEntry{
		OffsetSector: 15, SectorCount: 2, PayloadLength: 5000,
		Revision: 7, PayloadCRC32C: 0x12345678,
	}
	valid, err := encodeRegionBank(key, base)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		bytes    func() []byte
		key      RegionKey
		fileSize int64
		wantErr  error
	}{
		{name: "wrong magic", bytes: func() []byte { return mutateBank(valid, 0, 1, false) }, key: key, fileSize: 17 * sectorSize, wantErr: ErrCorrupt},
		{name: "past version", bytes: func() []byte { return putBankU32(valid, 4, 0) }, key: key, fileSize: 17 * sectorSize, wantErr: ErrCorrupt},
		{name: "future version", bytes: func() []byte { return putBankU32(valid, 4, 2) }, key: key, fileSize: 17 * sectorSize, wantErr: ErrFutureVersion},
		{name: "wrong sector size", bytes: func() []byte { return putBankU32(valid, 8, 2048) }, key: key, fileSize: 17 * sectorSize, wantErr: ErrCorrupt},
		{name: "wrong dimension", bytes: func() []byte { return valid[:] }, key: RegionKey{Dimension: 1, X: -2, Z: 3}, fileSize: 17 * sectorSize, wantErr: ErrCorrupt},
		{name: "wrong X", bytes: func() []byte { return valid[:] }, key: RegionKey{Dimension: 0, X: -1, Z: 3}, fileSize: 17 * sectorSize, wantErr: ErrCorrupt},
		{name: "wrong Z", bytes: func() []byte { return valid[:] }, key: RegionKey{Dimension: 0, X: -2, Z: 4}, fileSize: 17 * sectorSize, wantErr: ErrCorrupt},
		{name: "wrong entry count", bytes: func() []byte { return putBankU32(valid, 32, 1023) }, key: key, fileSize: 17 * sectorSize, wantErr: ErrCorrupt},
		{name: "wrong entry size", bytes: func() []byte { return putBankU32(valid, 36, 20) }, key: key, fileSize: 17 * sectorSize, wantErr: ErrCorrupt},
		{name: "wrong bank sectors", bytes: func() []byte { return putBankU32(valid, 40, 6) }, key: key, fileSize: 17 * sectorSize, wantErr: ErrCorrupt},
		{name: "wrong data start", bytes: func() []byte { return putBankU32(valid, 44, 14) }, key: key, fileSize: 17 * sectorSize, wantErr: ErrCorrupt},
		{name: "nonzero header reserved byte", bytes: func() []byte { return mutateBank(valid, 48, 1, true) }, key: key, fileSize: 17 * sectorSize, wantErr: ErrCorrupt},
		{name: "invalid CRC", bytes: func() []byte { return mutateBank(valid, 100, 1, false) }, key: key, fileSize: 17 * sectorSize, wantErr: ErrCorrupt},
		{name: "generation zero with entry", bytes: func() []byte { return putBankU64(valid, 24, 0) }, key: key, fileSize: 17 * sectorSize, wantErr: ErrCorrupt},
		{name: "offset inside headers", bytes: func() []byte { return putBankEntryU32(valid, 0, 0, 14) }, key: key, fileSize: 17 * sectorSize, wantErr: ErrCorrupt},
		{name: "zero sector count", bytes: func() []byte { return putBankEntryU32(valid, 0, 4, 0) }, key: key, fileSize: 17 * sectorSize, wantErr: ErrCorrupt},
		{name: "payload over one MiB", bytes: func() []byte {
			oversized := putBankEntryU32(valid, 0, 4, 257)
			return putBankEntryU32Slice(oversized, 0, 8, (1<<20)+1)
		}, key: key, fileSize: 272 * sectorSize, wantErr: ErrCorrupt},
		{name: "payload exceeds extent", bytes: func() []byte { return putBankEntryU32(valid, 0, 4, 1) }, key: key, fileSize: 17 * sectorSize, wantErr: ErrCorrupt},
		{name: "extent past EOF", bytes: func() []byte { return putBankEntryU32(valid, 0, 0, 16) }, key: key, fileSize: 17 * sectorSize, wantErr: ErrCorrupt},
		{name: "uint32 extent overflow", bytes: func() []byte { return putBankEntryU32(valid, 0, 0, math.MaxUint32) }, key: key, fileSize: math.MaxInt64, wantErr: ErrCorrupt},
		{name: "overlapping extents", bytes: func() []byte {
			overlap := putBankEntryU32(valid, 1, 0, 16)
			overlap = putBankEntryU32Slice(overlap, 1, 4, 1)
			overlap = putBankEntryU32Slice(overlap, 1, 8, 1)
			overlap = putBankEntryU64Slice(overlap, 1, 12, 8)
			return overlap
		}, key: key, fileSize: 17 * sectorSize, wantErr: ErrCorrupt},
		{name: "absent entry with nonzero tail", bytes: func() []byte { return putBankEntryU32(valid, 0, 0, 0) }, key: key, fileSize: 17 * sectorSize, wantErr: ErrCorrupt},
		{name: "zero revision", bytes: func() []byte { return putBankEntryU64(valid, 0, 12, 0) }, key: key, fileSize: 17 * sectorSize, wantErr: ErrCorrupt},
		{name: "nonzero trailing padding", bytes: func() []byte { return mutateBank(valid, 64+1024*24, 1, true) }, key: key, fileSize: 17 * sectorSize, wantErr: ErrCorrupt},
		{name: "short", bytes: func() []byte { return bytes.Clone(valid[:len(valid)-1]) }, key: key, fileSize: 17 * sectorSize, wantErr: ErrCorrupt},
		{name: "trailing", bytes: func() []byte { return append(bytes.Clone(valid[:]), 0) }, key: key, fileSize: 17 * sectorSize, wantErr: ErrCorrupt},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := decodeRegionBank(tc.key, tc.bytes(), tc.fileSize); !errors.Is(err, tc.wantErr) {
				t.Fatalf("decodeRegionBank error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestEncodeRegionBankRejectsInvalidStructure(t *testing.T) {
	key := RegionKey{Dimension: core.Overworld, X: -2, Z: 3}
	tests := []struct {
		name string
		bank regionBank
	}{
		{name: "generation zero with entry", bank: bankWithEntries(0, regionEntry{OffsetSector: 15, SectorCount: 1, PayloadLength: 1, Revision: 1})},
		{name: "absent entry with tail", bank: bankWithEntries(1, regionEntry{SectorCount: 1})},
		{name: "zero sector count", bank: bankWithEntries(1, regionEntry{OffsetSector: 15, PayloadLength: 1, Revision: 1})},
		{name: "oversized payload", bank: bankWithEntries(1, regionEntry{OffsetSector: 15, SectorCount: 257, PayloadLength: (1 << 20) + 1, Revision: 1})},
		{name: "zero revision", bank: bankWithEntries(1, regionEntry{OffsetSector: 15, SectorCount: 1, PayloadLength: 1})},
		{name: "overlap", bank: bankWithEntries(1,
			regionEntry{OffsetSector: 15, SectorCount: 2, PayloadLength: 1, Revision: 1},
			regionEntry{OffsetSector: 16, SectorCount: 1, PayloadLength: 1, Revision: 2},
		)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := encodeRegionBank(key, tc.bank); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("encodeRegionBank error = %v, want %v", err, ErrCorrupt)
			}
		})
	}
}

func TestSelectRegionBankValidityAndTies(t *testing.T) {
	a := regionBank{Generation: 9}
	b := regionBank{Generation: 10}
	invalidA := errors.New("invalid A")
	invalidB := errors.New("invalid B")

	tests := []struct {
		name      string
		a         regionBank
		errA      error
		b         regionBank
		errB      error
		want      regionBank
		wantIndex int
		wantErr   error
	}{
		{name: "both invalid", a: a, errA: invalidA, b: b, errB: invalidB, wantIndex: -1, wantErr: ErrCorrupt},
		{name: "only A valid", a: a, b: b, errB: invalidB, want: a, wantIndex: 0},
		{name: "only B valid", a: a, errA: invalidA, b: b, want: b, wantIndex: 1},
		{name: "newer A", a: b, b: a, want: b, wantIndex: 0},
		{name: "newer B", a: a, b: b, want: b, wantIndex: 1},
		{name: "equal identical nonzero", a: a, b: a, want: a, wantIndex: 0},
		{name: "equal different nonzero", a: a, b: bankWithEntries(9, regionEntry{OffsetSector: 15, SectorCount: 1, PayloadLength: 1, Revision: 1}), wantIndex: -1, wantErr: ErrCorrupt},
		{name: "only structurally valid bank is generation zero", a: regionBank{}, b: b, errB: invalidB, wantIndex: -1, wantErr: ErrCorrupt},
		{name: "both generation zero", a: regionBank{}, b: regionBank{}, wantIndex: -1, wantErr: ErrCorrupt},
		{name: "generation zero standby and committed B", a: regionBank{}, b: a, want: a, wantIndex: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, index, err := selectRegionBank(tc.a, tc.errA, tc.b, tc.errB)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("selectRegionBank error = %v, want %v", err, tc.wantErr)
			}
			if index != tc.wantIndex || got != tc.want {
				t.Fatalf("selectRegionBank = %+v, %d, want %+v, %d", got, index, tc.want, tc.wantIndex)
			}
		})
	}
}

func allZero(data []byte) bool {
	return bytes.Equal(data, make([]byte, len(data)))
}

func bankWithEntries(generation uint64, entries ...regionEntry) regionBank {
	bank := regionBank{Generation: generation}
	copy(bank.Entries[:], entries)
	return bank
}

func mutateSuperblock(block [4096]byte, offset int, mask byte) []byte {
	mutated := bytes.Clone(block[:])
	mutated[offset] ^= mask
	return mutated
}

func mutateSuperblockWithCRC(block [4096]byte, offset int, mask byte) []byte {
	mutated := mutateSuperblock(block, offset, mask)
	rechecksumSuperblock(mutated)
	return mutated
}

func putSuperblockU32(block [4096]byte, offset int, value uint32) []byte {
	mutated := bytes.Clone(block[:])
	binary.LittleEndian.PutUint32(mutated[offset:], value)
	rechecksumSuperblock(mutated)
	return mutated
}

func rechecksumSuperblock(block []byte) {
	binary.LittleEndian.PutUint32(block[4092:], crc32.Checksum(block[:4092], crc32.MakeTable(crc32.Castagnoli)))
}

func mutateBank(bank [7 * 4096]byte, offset int, mask byte, rechecksum bool) []byte {
	mutated := bytes.Clone(bank[:])
	mutated[offset] ^= mask
	if rechecksum {
		rechecksumBank(mutated)
	}
	return mutated
}

func putBankU32(bank [7 * 4096]byte, offset int, value uint32) []byte {
	mutated := bytes.Clone(bank[:])
	binary.LittleEndian.PutUint32(mutated[offset:], value)
	rechecksumBank(mutated)
	return mutated
}

func putBankU64(bank [7 * 4096]byte, offset int, value uint64) []byte {
	mutated := bytes.Clone(bank[:])
	binary.LittleEndian.PutUint64(mutated[offset:], value)
	rechecksumBank(mutated)
	return mutated
}

func putBankEntryU32(bank [7 * 4096]byte, slot, fieldOffset int, value uint32) []byte {
	return putBankEntryU32Slice(bank[:], slot, fieldOffset, value)
}

func putBankEntryU32Slice(bank []byte, slot, fieldOffset int, value uint32) []byte {
	mutated := bytes.Clone(bank)
	binary.LittleEndian.PutUint32(mutated[64+slot*24+fieldOffset:], value)
	rechecksumBank(mutated)
	return mutated
}

func putBankEntryU64(bank [7 * 4096]byte, slot, fieldOffset int, value uint64) []byte {
	return putBankEntryU64Slice(bank[:], slot, fieldOffset, value)
}

func putBankEntryU64Slice(bank []byte, slot, fieldOffset int, value uint64) []byte {
	mutated := bytes.Clone(bank)
	binary.LittleEndian.PutUint64(mutated[64+slot*24+fieldOffset:], value)
	rechecksumBank(mutated)
	return mutated
}

func rechecksumBank(bank []byte) {
	clear(bank[60:64])
	binary.LittleEndian.PutUint32(bank[60:64], crc32.Checksum(bank, crc32.MakeTable(crc32.Castagnoli)))
}
