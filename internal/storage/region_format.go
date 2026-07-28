package storage

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"math"
	"sort"

	"minecraft-go/internal/core"
)

const (
	sectorSize                  = 4096
	bankSectors                 = 7
	dataStartSector             = 15
	regionSlots                 = 32 * 32
	currentRegionVersion uint32 = 1

	bankAStartSector = 1
	bankBStartSector = 8

	regionBankSize       = bankSectors * sectorSize
	regionBankHeaderSize = 64
	regionEntrySize      = 24

	superMagicOffset       = 0
	superVersionOffset     = 4
	superSectorSizeOffset  = 8
	superDimensionOffset   = 12
	superRegionXOffset     = 16
	superRegionZOffset     = 20
	superBankAStartOffset  = 24
	superBankBStartOffset  = 28
	superBankSectorsOffset = 32
	superDataStartOffset   = 36
	superReservedOffset    = 40
	superCRCOffset         = sectorSize - 4

	bankMagicOffset      = 0
	bankVersionOffset    = 4
	bankSectorSizeOffset = 8
	bankDimensionOffset  = 12
	bankRegionXOffset    = 16
	bankRegionZOffset    = 20
	bankGenerationOffset = 24
	bankEntryCountOffset = 32
	bankEntrySizeOffset  = 36
	bankSectorsOffset    = 40
	bankDataStartOffset  = 44
	bankReservedOffset   = 48
	bankCRCOffset        = regionBankHeaderSize - 4
	bankEntriesOffset    = regionBankHeaderSize
	bankPaddingOffset    = bankEntriesOffset + regionSlots*regionEntrySize

	entryOffsetSectorOffset  = 0
	entrySectorCountOffset   = 4
	entryPayloadLengthOffset = 8
	entryRevisionOffset      = 12
	entryPayloadCRCOffset    = 20
)

var (
	regionSuperblockMagic = [4]byte{'M', 'C', 'G', 'R'}
	regionBankMagic       = [4]byte{'M', 'C', 'G', 'B'}
	regionCRCTable        = crc32.MakeTable(crc32.Castagnoli)
)

type regionEntry struct {
	OffsetSector  uint32
	SectorCount   uint32
	PayloadLength uint32
	Revision      uint64
	PayloadCRC32C uint32
}

type regionBank struct {
	Generation uint64
	Entries    [regionSlots]regionEntry
}

type sectorRange struct {
	first uint64
	end   uint64
}

func encodeSuperblock(key RegionKey) [sectorSize]byte {
	var encoded [sectorSize]byte
	copy(encoded[superMagicOffset:], regionSuperblockMagic[:])
	putRegionU32(encoded[:], superVersionOffset, currentRegionVersion)
	putRegionU32(encoded[:], superSectorSizeOffset, sectorSize)
	putRegionU32(encoded[:], superDimensionOffset, uint32(key.Dimension))
	putRegionU32(encoded[:], superRegionXOffset, uint32(key.X))
	putRegionU32(encoded[:], superRegionZOffset, uint32(key.Z))
	putRegionU32(encoded[:], superBankAStartOffset, bankAStartSector)
	putRegionU32(encoded[:], superBankBStartOffset, bankBStartSector)
	putRegionU32(encoded[:], superBankSectorsOffset, bankSectors)
	putRegionU32(encoded[:], superDataStartOffset, dataStartSector)
	putRegionU32(
		encoded[:], superCRCOffset,
		crc32.Checksum(encoded[:superCRCOffset], regionCRCTable),
	)
	return encoded
}

func decodeSuperblock(key RegionKey, encoded []byte) error {
	if len(encoded) != sectorSize {
		return fmt.Errorf("%w: superblock length %d, want %d", ErrCorrupt, len(encoded), sectorSize)
	}
	if !bytes.Equal(encoded[superMagicOffset:superVersionOffset], regionSuperblockMagic[:]) {
		return fmt.Errorf("%w: superblock magic", ErrCorrupt)
	}
	version := regionU32(encoded, superVersionOffset)
	if version > currentRegionVersion {
		return fmt.Errorf("%w: region version %d", ErrFutureVersion, version)
	}
	if version != currentRegionVersion {
		return fmt.Errorf("%w: unsupported region version %d", ErrCorrupt, version)
	}
	wantCRC := regionU32(encoded, superCRCOffset)
	gotCRC := crc32.Checksum(encoded[:superCRCOffset], regionCRCTable)
	if gotCRC != wantCRC {
		return fmt.Errorf("%w: superblock CRC32C", ErrCorrupt)
	}
	if regionU32(encoded, superSectorSizeOffset) != sectorSize ||
		regionU32(encoded, superBankAStartOffset) != bankAStartSector ||
		regionU32(encoded, superBankBStartOffset) != bankBStartSector ||
		regionU32(encoded, superBankSectorsOffset) != bankSectors ||
		regionU32(encoded, superDataStartOffset) != dataStartSector {
		return fmt.Errorf("%w: superblock fixed geometry", ErrCorrupt)
	}
	if core.DimensionID(int32(regionU32(encoded, superDimensionOffset))) != key.Dimension ||
		int32(regionU32(encoded, superRegionXOffset)) != key.X ||
		int32(regionU32(encoded, superRegionZOffset)) != key.Z {
		return fmt.Errorf("%w: superblock region key mismatch", ErrCorrupt)
	}
	if !regionBytesZero(encoded[superReservedOffset:superCRCOffset]) {
		return fmt.Errorf("%w: superblock reserved bytes", ErrCorrupt)
	}
	return nil
}

func encodeRegionBank(key RegionKey, bank regionBank) ([regionBankSize]byte, error) {
	if err := validateRegionBank(bank, 0, false); err != nil {
		return [regionBankSize]byte{}, err
	}

	var encoded [regionBankSize]byte
	copy(encoded[bankMagicOffset:], regionBankMagic[:])
	putRegionU32(encoded[:], bankVersionOffset, currentRegionVersion)
	putRegionU32(encoded[:], bankSectorSizeOffset, sectorSize)
	putRegionU32(encoded[:], bankDimensionOffset, uint32(key.Dimension))
	putRegionU32(encoded[:], bankRegionXOffset, uint32(key.X))
	putRegionU32(encoded[:], bankRegionZOffset, uint32(key.Z))
	binary.LittleEndian.PutUint64(encoded[bankGenerationOffset:], bank.Generation)
	putRegionU32(encoded[:], bankEntryCountOffset, regionSlots)
	putRegionU32(encoded[:], bankEntrySizeOffset, regionEntrySize)
	putRegionU32(encoded[:], bankSectorsOffset, bankSectors)
	putRegionU32(encoded[:], bankDataStartOffset, dataStartSector)

	for slot, entry := range bank.Entries {
		offset := bankEntriesOffset + slot*regionEntrySize
		putRegionU32(encoded[:], offset+entryOffsetSectorOffset, entry.OffsetSector)
		putRegionU32(encoded[:], offset+entrySectorCountOffset, entry.SectorCount)
		putRegionU32(encoded[:], offset+entryPayloadLengthOffset, entry.PayloadLength)
		binary.LittleEndian.PutUint64(encoded[offset+entryRevisionOffset:], entry.Revision)
		putRegionU32(encoded[:], offset+entryPayloadCRCOffset, entry.PayloadCRC32C)
	}
	putRegionU32(encoded[:], bankCRCOffset, regionBankChecksum(encoded[:]))
	return encoded, nil
}

func decodeRegionBank(key RegionKey, encoded []byte, fileSize int64) (regionBank, error) {
	if len(encoded) != regionBankSize {
		return regionBank{}, fmt.Errorf("%w: region bank length %d, want %d", ErrCorrupt, len(encoded), regionBankSize)
	}
	if !bytes.Equal(encoded[bankMagicOffset:bankVersionOffset], regionBankMagic[:]) {
		return regionBank{}, fmt.Errorf("%w: region bank magic", ErrCorrupt)
	}
	version := regionU32(encoded, bankVersionOffset)
	if version > currentRegionVersion {
		return regionBank{}, fmt.Errorf("%w: region bank version %d", ErrFutureVersion, version)
	}
	if version != currentRegionVersion {
		return regionBank{}, fmt.Errorf("%w: unsupported region bank version %d", ErrCorrupt, version)
	}
	if regionBankChecksum(encoded) != regionU32(encoded, bankCRCOffset) {
		return regionBank{}, fmt.Errorf("%w: region bank CRC32C", ErrCorrupt)
	}
	if regionU32(encoded, bankSectorSizeOffset) != sectorSize ||
		regionU32(encoded, bankEntryCountOffset) != regionSlots ||
		regionU32(encoded, bankEntrySizeOffset) != regionEntrySize ||
		regionU32(encoded, bankSectorsOffset) != bankSectors ||
		regionU32(encoded, bankDataStartOffset) != dataStartSector {
		return regionBank{}, fmt.Errorf("%w: region bank fixed geometry", ErrCorrupt)
	}
	if core.DimensionID(int32(regionU32(encoded, bankDimensionOffset))) != key.Dimension ||
		int32(regionU32(encoded, bankRegionXOffset)) != key.X ||
		int32(regionU32(encoded, bankRegionZOffset)) != key.Z {
		return regionBank{}, fmt.Errorf("%w: region bank key mismatch", ErrCorrupt)
	}
	if !regionBytesZero(encoded[bankReservedOffset:bankCRCOffset]) {
		return regionBank{}, fmt.Errorf("%w: region bank reserved bytes", ErrCorrupt)
	}
	if !regionBytesZero(encoded[bankPaddingOffset:]) {
		return regionBank{}, fmt.Errorf("%w: region bank padding", ErrCorrupt)
	}

	bank := regionBank{Generation: binary.LittleEndian.Uint64(encoded[bankGenerationOffset:])}
	for slot := range bank.Entries {
		offset := bankEntriesOffset + slot*regionEntrySize
		bank.Entries[slot] = regionEntry{
			OffsetSector:  regionU32(encoded, offset+entryOffsetSectorOffset),
			SectorCount:   regionU32(encoded, offset+entrySectorCountOffset),
			PayloadLength: regionU32(encoded, offset+entryPayloadLengthOffset),
			Revision:      binary.LittleEndian.Uint64(encoded[offset+entryRevisionOffset:]),
			PayloadCRC32C: regionU32(encoded, offset+entryPayloadCRCOffset),
		}
	}
	if err := validateRegionBank(bank, fileSize, true); err != nil {
		return regionBank{}, err
	}
	return bank, nil
}

func selectRegionBank(
	bankA regionBank,
	errA error,
	bankB regionBank,
	errB error,
) (regionBank, int, error) {
	if errA == nil && bankA.Generation == 0 {
		errA = fmt.Errorf("%w: bank A is an uncommitted standby", ErrCorrupt)
	}
	if errB == nil && bankB.Generation == 0 {
		errB = fmt.Errorf("%w: bank B is an uncommitted standby", ErrCorrupt)
	}
	if errA != nil && errB != nil {
		return regionBank{}, -1, fmt.Errorf(
			"%w: both region banks invalid: bank A: %v; bank B: %v",
			ErrCorrupt, errA, errB,
		)
	}
	if errA != nil {
		return bankB, 1, nil
	}
	if errB != nil {
		return bankA, 0, nil
	}
	if bankA.Generation > bankB.Generation {
		return bankA, 0, nil
	}
	if bankB.Generation > bankA.Generation {
		return bankB, 1, nil
	}

	// Decoding accepts only canonical zero-filled headers and padding, so equal
	// decoded values represent byte-identical banks for a shared RegionKey.
	if bankA == bankB {
		return bankA, 0, nil
	}
	return regionBank{}, -1, fmt.Errorf(
		"%w: divergent region banks at generation %d", ErrCorrupt, bankA.Generation,
	)
}

func validateRegionBank(bank regionBank, fileSize int64, checkFileSize bool) error {
	if checkFileSize && fileSize < int64(dataStartSector*sectorSize) {
		return fmt.Errorf("%w: region file is shorter than fixed headers", ErrCorrupt)
	}

	ranges := make([]sectorRange, 0)
	for slot, entry := range bank.Entries {
		if entry.OffsetSector == 0 {
			if entry != (regionEntry{}) {
				return fmt.Errorf("%w: absent region entry %d has nonzero fields", ErrCorrupt, slot)
			}
			continue
		}
		if bank.Generation == 0 {
			return fmt.Errorf("%w: generation zero region bank is not empty", ErrCorrupt)
		}
		if entry.SectorCount == 0 {
			return fmt.Errorf("%w: region entry %d has zero sector count", ErrCorrupt, slot)
		}
		if entry.PayloadLength > maxCompressedChunk {
			return fmt.Errorf("%w: region entry %d payload exceeds limit", ErrCorrupt, slot)
		}
		if uint64(entry.PayloadLength) > uint64(entry.SectorCount)*sectorSize {
			return fmt.Errorf("%w: region entry %d payload exceeds extent", ErrCorrupt, slot)
		}
		if entry.Revision == 0 {
			return fmt.Errorf("%w: region entry %d has zero revision", ErrCorrupt, slot)
		}

		first := uint64(entry.OffsetSector)
		end := first + uint64(entry.SectorCount)
		if end > math.MaxUint32 {
			return fmt.Errorf("%w: region entry %d extent overflows uint32", ErrCorrupt, slot)
		}
		ranges = append(ranges, sectorRange{first: first, end: end})
	}

	sort.Slice(ranges, func(i, j int) bool { return ranges[i].first < ranges[j].first })
	for index, regionRange := range ranges {
		if regionRange.first < dataStartSector || regionRange.first >= regionRange.end {
			return fmt.Errorf("%w: invalid extent", ErrCorrupt)
		}
		if checkFileSize && regionRange.end > uint64(fileSize/sectorSize) {
			return fmt.Errorf("%w: invalid extent", ErrCorrupt)
		}
		if index > 0 && ranges[index-1].end > regionRange.first {
			return fmt.Errorf("%w: overlapping extents", ErrCorrupt)
		}
	}
	return nil
}

func regionBankChecksum(encoded []byte) uint32 {
	hasher := crc32.New(regionCRCTable)
	_, _ = hasher.Write(encoded[:bankCRCOffset])
	_, _ = hasher.Write([]byte{0, 0, 0, 0})
	_, _ = hasher.Write(encoded[bankCRCOffset+4:])
	return hasher.Sum32()
}

func putRegionU32(encoded []byte, offset int, value uint32) {
	binary.LittleEndian.PutUint32(encoded[offset:], value)
}

func regionU32(encoded []byte, offset int) uint32 {
	return binary.LittleEndian.Uint32(encoded[offset:])
}

func regionBytesZero(encoded []byte) bool {
	for _, value := range encoded {
		if value != 0 {
			return false
		}
	}
	return true
}
