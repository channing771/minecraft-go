package storage

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"math"
	"slices"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
)

const (
	companionEnvelopeVersion uint32 = 1
	currentCompanionSchema   uint32 = 1
	companionHeaderLength           = 32
	companionRecordLength           = 221
	maxCompanionFileLength          = companionHeaderLength + companion.MaxStored*companionRecordLength
)

var (
	companionEnvelopeMagic = [4]byte{'M', 'C', 'A', 'I'}
	companionCRCTable      = crc32.MakeTable(crc32.Castagnoli)
)

func encodeCompanions(save CompanionSave) ([]byte, error) {
	if save.Revision == 0 {
		return nil, fmt.Errorf("%w: zero companion revision", ErrCorrupt)
	}
	if len(save.Records) > companion.MaxStored {
		return nil, fmt.Errorf("%w: companion count %d exceeds limit", ErrCorrupt, len(save.Records))
	}
	records := slices.Clone(save.Records)
	slices.SortFunc(records, func(a, b companion.Body) int {
		return bytes.Compare(a.ID[:], b.ID[:])
	})
	for index, body := range records {
		if err := validateCompanionBody(body); err != nil {
			return nil, fmt.Errorf("companion record %d: %w", index, err)
		}
		if index > 0 && records[index-1].ID == body.ID {
			return nil, fmt.Errorf("%w: duplicate companion ID", ErrCorrupt)
		}
	}

	payloadLength := len(records) * companionRecordLength
	encoded := make([]byte, 0, companionHeaderLength+payloadLength)
	encoded = append(encoded, companionEnvelopeMagic[:]...)
	encoded = appendU32(encoded, companionEnvelopeVersion)
	encoded = appendU32(encoded, currentCompanionSchema)
	encoded = appendU64(encoded, save.Revision)
	encoded = appendU32(encoded, uint32(len(records)))
	encoded = appendU32(encoded, uint32(payloadLength))
	encoded = appendU32(encoded, 0)
	for _, body := range records {
		encoded = appendCompanionBody(encoded, body)
	}
	binary.LittleEndian.PutUint32(encoded[28:], companionChecksum(encoded))
	return encoded, nil
}

func decodeCompanions(data []byte) (StoredCompanions, error) {
	if len(data) > maxCompanionFileLength {
		return StoredCompanions{}, fmt.Errorf("%w: companion file length %d exceeds limit", ErrCorrupt, len(data))
	}
	header := byteDecoder{data: data}
	if err := header.magic(companionEnvelopeMagic); err != nil {
		return StoredCompanions{}, corrupt("companion envelope magic", err)
	}
	version, err := header.u32()
	if err != nil {
		return StoredCompanions{}, corrupt("companion envelope version", err)
	}
	if version != companionEnvelopeVersion {
		if version > companionEnvelopeVersion {
			return StoredCompanions{}, fmt.Errorf("%w: companion envelope version %d", ErrFutureVersion, version)
		}
		return StoredCompanions{}, fmt.Errorf("%w: unsupported companion envelope version %d", ErrCorrupt, version)
	}
	schema, err := header.u32()
	if err != nil {
		return StoredCompanions{}, corrupt("companion schema", err)
	}
	if schema != currentCompanionSchema {
		if schema > currentCompanionSchema {
			return StoredCompanions{}, fmt.Errorf("%w: companion schema %d", ErrFutureVersion, schema)
		}
		return StoredCompanions{}, fmt.Errorf("%w: unsupported companion schema %d", ErrCorrupt, schema)
	}
	revision, err := header.u64()
	if err != nil {
		return StoredCompanions{}, corrupt("companion revision", err)
	}
	if revision == 0 {
		return StoredCompanions{}, fmt.Errorf("%w: zero companion revision", ErrCorrupt)
	}
	count, err := header.u32()
	if err != nil {
		return StoredCompanions{}, corrupt("companion count", err)
	}
	if count > companion.MaxStored {
		return StoredCompanions{}, fmt.Errorf("%w: companion count %d exceeds limit", ErrCorrupt, count)
	}
	payloadLength, err := header.u32()
	if err != nil {
		return StoredCompanions{}, corrupt("companion payload length", err)
	}
	if payloadLength != count*companionRecordLength {
		return StoredCompanions{}, fmt.Errorf("%w: companion payload length does not match count", ErrCorrupt)
	}
	wantCRC, err := header.u32()
	if err != nil {
		return StoredCompanions{}, corrupt("companion CRC32C", err)
	}
	if uint64(header.remaining()) != uint64(payloadLength) {
		return StoredCompanions{}, fmt.Errorf("%w: companion payload length does not match file", ErrCorrupt)
	}
	if companionChecksum(data) != wantCRC {
		return StoredCompanions{}, fmt.Errorf("%w: companion CRC32C", ErrCorrupt)
	}

	records := make([]companion.Body, int(count))
	for index := range records {
		body, err := decodeCompanionBody(&header)
		if err != nil {
			return StoredCompanions{}, fmt.Errorf("companion record %d: %w", index, err)
		}
		if index > 0 && bytes.Compare(records[index-1].ID[:], body.ID[:]) >= 0 {
			return StoredCompanions{}, fmt.Errorf("%w: companion IDs are not strictly sorted", ErrCorrupt)
		}
		records[index] = body
	}
	return StoredCompanions{Revision: revision, Records: records}, nil
}

func appendCompanionBody(dst []byte, body companion.Body) []byte {
	dst = append(dst, body.ID[:]...)
	dst = appendU32(dst, uint32(body.Dimension))
	for _, value := range body.Position {
		dst = appendF32(dst, value)
	}
	dst = appendF32(dst, body.Yaw)
	dst = appendF32(dst, body.Pitch)
	dst = append(dst, body.Inventory.Hotbar.Selected)
	for _, stack := range body.Inventory.Hotbar.Slots {
		dst = appendPlayerStack(dst, stack)
	}
	for _, stack := range body.Inventory.Backpack {
		dst = appendPlayerStack(dst, stack)
	}
	return dst
}

func decodeCompanionBody(decoder *byteDecoder) (companion.Body, error) {
	idBytes, err := decoder.take(len(companion.ID{}))
	if err != nil {
		return companion.Body{}, corrupt("companion ID", err)
	}
	var body companion.Body
	copy(body.ID[:], idBytes)
	dimension, err := decoder.u32()
	if err != nil {
		return companion.Body{}, corrupt("companion dimension", err)
	}
	body.Dimension = core.DimensionID(int32(dimension))
	for index := range body.Position {
		body.Position[index], err = decodeF32(decoder)
		if err != nil {
			return companion.Body{}, corrupt("companion position", err)
		}
	}
	if body.Yaw, err = decodeF32(decoder); err != nil {
		return companion.Body{}, corrupt("companion yaw", err)
	}
	if body.Pitch, err = decodeF32(decoder); err != nil {
		return companion.Body{}, corrupt("companion pitch", err)
	}
	if body.Inventory.Hotbar.Selected, err = decoder.u8(); err != nil {
		return companion.Body{}, corrupt("companion selected slot", err)
	}
	for index := range body.Inventory.Hotbar.Slots {
		body.Inventory.Hotbar.Slots[index], err = decodePlayerStack(decoder)
		if err != nil {
			return companion.Body{}, corrupt("companion hotbar slot", err)
		}
	}
	for index := range body.Inventory.Backpack {
		body.Inventory.Backpack[index], err = decodePlayerStack(decoder)
		if err != nil {
			return companion.Body{}, corrupt("companion backpack slot", err)
		}
	}
	if err := validateCompanionBody(body); err != nil {
		return companion.Body{}, err
	}
	return body, nil
}

func validateCompanionBody(body companion.Body) error {
	if !body.ID.Valid() {
		return fmt.Errorf("%w: invalid companion ID", ErrCorrupt)
	}
	if body.Dimension != core.Overworld {
		return fmt.Errorf("%w: unsupported companion dimension %d", ErrCorrupt, body.Dimension)
	}
	for _, value := range body.Position {
		if !finitePlayerFloat(value) {
			return fmt.Errorf("%w: non-finite companion position", ErrCorrupt)
		}
	}
	if !finitePlayerFloat(body.Yaw) {
		return fmt.Errorf("%w: non-finite companion yaw", ErrCorrupt)
	}
	if !finitePlayerFloat(body.Pitch) || body.Pitch < -math.Pi/2 || body.Pitch > math.Pi/2 {
		return fmt.Errorf("%w: invalid companion pitch", ErrCorrupt)
	}
	if !body.Inventory.Valid() {
		return fmt.Errorf("%w: invalid companion inventory", ErrCorrupt)
	}
	return nil
}

func companionChecksum(data []byte) uint32 {
	hasher := crc32.New(companionCRCTable)
	_, _ = hasher.Write(data[8:28])
	_, _ = hasher.Write(data[companionHeaderLength:])
	return hasher.Sum32()
}
