package storage

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"minecraft-go/internal/core"
)

const (
	currentMetadataVersion uint32 = 1
	metadataPayloadLength  uint32 = 20
	metadataHeaderLength          = 12
	metadataChecksumLength        = 4
)

var (
	metadataMagic    = [4]byte{'M', 'C', 'G', 'M'}
	metadataCRCTable = crc32.MakeTable(crc32.Castagnoli)
)

type metadataDirectory interface {
	Sync() error
	Close() error
}

type atomicReplaceFile interface {
	Name() string
	Chmod(fs.FileMode) error
	Write([]byte) (int, error)
	Sync() error
	Close() error
}

type atomicReplaceHooks struct {
	createTemp    func(string, string) (atomicReplaceFile, error)
	beforeRename  func() error
	rename        func(string, string) error
	openDirectory func(string) (metadataDirectory, error)
}

func encodeMetadata(metadata Metadata) ([]byte, error) {
	if metadata.FormatVersion > currentMetadataVersion {
		return nil, fmt.Errorf("%w: metadata version %d", ErrFutureVersion, metadata.FormatVersion)
	}
	if metadata.FormatVersion != currentMetadataVersion {
		return nil, fmt.Errorf("%w: unsupported metadata version %d", ErrCorrupt, metadata.FormatVersion)
	}

	encoded := make([]byte, 0, metadataHeaderLength+metadataPayloadLength+metadataChecksumLength)
	encoded = append(encoded, metadataMagic[:]...)
	encoded = binary.LittleEndian.AppendUint32(encoded, metadata.FormatVersion)
	encoded = binary.LittleEndian.AppendUint32(encoded, metadataPayloadLength)
	encoded = binary.LittleEndian.AppendUint64(encoded, uint64(metadata.Seed))
	encoded = binary.LittleEndian.AppendUint32(encoded, uint32(metadata.SpawnDimension))
	encoded = binary.LittleEndian.AppendUint32(encoded, uint32(metadata.SpawnAnchor.X))
	encoded = binary.LittleEndian.AppendUint32(encoded, uint32(metadata.SpawnAnchor.Z))
	encoded = binary.LittleEndian.AppendUint32(
		encoded, crc32.Checksum(encoded, metadataCRCTable),
	)
	return encoded, nil
}

func decodeMetadata(encoded []byte) (Metadata, error) {
	if len(encoded) < metadataHeaderLength {
		return Metadata{}, fmt.Errorf("%w: metadata header is short", ErrCorrupt)
	}
	if string(encoded[:len(metadataMagic)]) != string(metadataMagic[:]) {
		return Metadata{}, fmt.Errorf("%w: metadata magic", ErrCorrupt)
	}

	version := binary.LittleEndian.Uint32(encoded[4:8])
	if version > currentMetadataVersion {
		return Metadata{}, fmt.Errorf("%w: metadata version %d", ErrFutureVersion, version)
	}
	if version != currentMetadataVersion {
		return Metadata{}, fmt.Errorf("%w: unsupported metadata version %d", ErrCorrupt, version)
	}

	payloadLength := binary.LittleEndian.Uint32(encoded[8:12])
	if payloadLength != metadataPayloadLength {
		return Metadata{}, fmt.Errorf("%w: metadata payload length %d", ErrCorrupt, payloadLength)
	}
	wantLength := metadataHeaderLength + int(payloadLength) + metadataChecksumLength
	if len(encoded) != wantLength {
		return Metadata{}, fmt.Errorf(
			"%w: metadata length %d, want %d", ErrCorrupt, len(encoded), wantLength,
		)
	}

	checksumOffset := wantLength - metadataChecksumLength
	wantChecksum := binary.LittleEndian.Uint32(encoded[checksumOffset:])
	gotChecksum := crc32.Checksum(encoded[:checksumOffset], metadataCRCTable)
	if gotChecksum != wantChecksum {
		return Metadata{}, fmt.Errorf("%w: metadata CRC32C", ErrCorrupt)
	}

	payload := encoded[metadataHeaderLength:checksumOffset]
	return Metadata{
		FormatVersion:  version,
		Seed:           int64(binary.LittleEndian.Uint64(payload[0:8])),
		SpawnDimension: core.DimensionID(int32(binary.LittleEndian.Uint32(payload[8:12]))),
		SpawnAnchor: core.ChunkPos{
			X: int32(binary.LittleEndian.Uint32(payload[12:16])),
			Z: int32(binary.LittleEndian.Uint32(payload[16:20])),
		},
	}, nil
}

func replaceFileAtomically(
	path, pattern string,
	data []byte,
	mode fs.FileMode,
) error {
	return replaceFileAtomicallyWithPatternAndHooks(
		path, pattern, data, mode, atomicReplaceHooks{},
	)
}

func replaceFileAtomicallyWithHooks(
	path string,
	data []byte,
	mode fs.FileMode,
	hooks atomicReplaceHooks,
) error {
	return replaceFileAtomicallyWithPatternAndHooks(
		path, ".world.meta.tmp-*", data, mode, hooks,
	)
}

func replaceFileAtomicallyWithPatternAndHooks(
	path, pattern string,
	data []byte,
	mode fs.FileMode,
	hooks atomicReplaceHooks,
) error {
	if hooks.createTemp == nil {
		hooks.createTemp = func(directory, pattern string) (atomicReplaceFile, error) {
			return os.CreateTemp(directory, pattern)
		}
	}
	if hooks.rename == nil {
		hooks.rename = os.Rename
	}
	if hooks.openDirectory == nil {
		hooks.openDirectory = openMetadataDirectory
	}

	parent := filepath.Dir(path)
	temporary, err := hooks.createTemp(parent, pattern)
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		_ = temporary.Close()
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()

	if err := temporary.Chmod(mode); err != nil {
		return fmt.Errorf("chmod temporary file: %w", err)
	}
	for remaining := data; len(remaining) > 0; {
		written, err := temporary.Write(remaining)
		if err != nil {
			return fmt.Errorf("write temporary file: %w", err)
		}
		if written == 0 {
			return fmt.Errorf("write temporary file: %w", io.ErrShortWrite)
		}
		remaining = remaining[written:]
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	if hooks.beforeRename != nil {
		if err := hooks.beforeRename(); err != nil {
			return fmt.Errorf("before replacing file: %w", err)
		}
	}
	if err := hooks.rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace file: %w", err)
	}
	removeTemporary = false

	directory, err := hooks.openDirectory(parent)
	if err != nil {
		return fmt.Errorf("open containing directory: %w", err)
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if err := errors.Join(syncErr, closeErr); err != nil {
		return fmt.Errorf("sync containing directory: %w", err)
	}

	return nil
}

func openMetadataDirectory(path string) (metadataDirectory, error) {
	return os.Open(path)
}
