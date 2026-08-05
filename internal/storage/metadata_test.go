package storage

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"

	"minecraft-go/internal/core"
)

func TestMetadataEncodingIsDeterministic(t *testing.T) {
	metadata := Metadata{
		FormatVersion:  currentMetadataVersion,
		Seed:           -42,
		SpawnDimension: core.DimensionID(-3),
		SpawnAnchor:    core.ChunkPos{X: 7, Z: -11},
	}

	one, err := encodeMetadata(metadata)
	if err != nil {
		t.Fatal(err)
	}
	two, err := encodeMetadata(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(one, two) {
		t.Fatal("same metadata encoded differently")
	}

	// 精确字节由 TestMetadataV2GoldenBytes 覆盖，这里只保证确定性与往返。
	wantLength := metadataHeaderLength + int(metadataPayloadLength) + metadataChecksumLength
	if len(one) != wantLength {
		t.Fatalf("metadata length = %d, want %d", len(one), wantLength)
	}
	checksumOffset := wantLength - metadataChecksumLength
	wantCRC := crc32.Checksum(one[:checksumOffset], crc32.MakeTable(crc32.Castagnoli))
	if got := binary.LittleEndian.Uint32(one[checksumOffset:]); got != wantCRC {
		t.Fatalf("metadata CRC32C = %#x, want %#x", got, wantCRC)
	}

	decoded, err := decodeMetadata(one)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != metadata {
		t.Fatalf("metadata roundtrip = %+v, want %+v", decoded, metadata)
	}
}

func TestMetadataRejectsMalformedBytes(t *testing.T) {
	valid, err := encodeMetadata(Metadata{
		FormatVersion:  currentMetadataVersion,
		Seed:           42,
		SpawnDimension: core.Overworld,
		SpawnAnchor:    core.ChunkPos{X: 3, Z: -2},
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		bytes   func() []byte
		wantErr error
	}{
		{
			name: "CRC corruption",
			bytes: func() []byte {
				corrupt := bytes.Clone(valid)
				corrupt[12] ^= 0xff
				return corrupt
			},
			wantErr: ErrCorrupt,
		},
		{
			name: "future version",
			bytes: func() []byte {
				future := bytes.Clone(valid)
				binary.LittleEndian.PutUint32(future[4:8], currentMetadataVersion+1)
				return future
			},
			wantErr: ErrFutureVersion,
		},
		{
			name: "past version",
			bytes: func() []byte {
				past := bytes.Clone(valid)
				binary.LittleEndian.PutUint32(past[4:8], 0)
				return past
			},
			wantErr: ErrCorrupt,
		},
		{
			name:    "short",
			bytes:   func() []byte { return bytes.Clone(valid[:len(valid)-1]) },
			wantErr: ErrCorrupt,
		},
		{
			name: "trailing",
			bytes: func() []byte {
				return append(bytes.Clone(valid), 0)
			},
			wantErr: ErrCorrupt,
		},
		{
			name: "wrong payload length",
			bytes: func() []byte {
				wrongLength := bytes.Clone(valid)
				binary.LittleEndian.PutUint32(wrongLength[8:12], 19)
				return wrongLength
			},
			wantErr: ErrCorrupt,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := decodeMetadata(tc.bytes()); !errors.Is(err, tc.wantErr) {
				t.Fatalf("decodeMetadata error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestMetadataAtomicReplaceFailurePreservesPreviousFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "world.meta")
	if err := os.WriteFile(path, []byte("previous"), 0o640); err != nil {
		t.Fatal(err)
	}
	bystander := filepath.Join(root, ".world.meta.tmp-keep")
	if err := os.WriteFile(bystander, []byte("unrelated"), 0o600); err != nil {
		t.Fatal(err)
	}

	rename := func(string, string) error { return errors.New("injected rename failure") }
	if err := replaceFileAtomicallyWithHooks(
		path,
		[]byte("replacement"),
		0o600,
		atomicReplaceHooks{rename: rename, openDirectory: openMetadataDirectory},
	); err == nil {
		t.Fatal("replaceFileAtomically unexpectedly succeeded")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "previous" {
		t.Fatalf("previous file = %q after failed replace", got)
	}
	got, err = os.ReadFile(bystander)
	if err != nil {
		t.Fatalf("bystander temp was removed: %v", err)
	}
	if string(got) != "unrelated" {
		t.Fatalf("bystander temp = %q", got)
	}
	matches, err := filepath.Glob(filepath.Join(root, ".world.meta.tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0] != bystander {
		t.Fatalf("temporary files after failed replace = %v, want only %q", matches, bystander)
	}
}

type injectedMetadataDirectory struct {
	syncErr  error
	closeErr error
}

func (directory *injectedMetadataDirectory) Sync() error {
	return directory.syncErr
}

func (directory *injectedMetadataDirectory) Close() error {
	return directory.closeErr
}

func TestMetadataAtomicReplaceReleasesTempPathAfterRename(t *testing.T) {
	tests := []struct {
		name          string
		openDirectory func(error) func(string) (metadataDirectory, error)
	}{
		{
			name: "directory open failure",
			openDirectory: func(injected error) func(string) (metadataDirectory, error) {
				return func(string) (metadataDirectory, error) { return nil, injected }
			},
		},
		{
			name: "directory sync failure",
			openDirectory: func(injected error) func(string) (metadataDirectory, error) {
				return func(string) (metadataDirectory, error) {
					return &injectedMetadataDirectory{syncErr: injected}, nil
				}
			},
		},
		{
			name: "directory close failure",
			openDirectory: func(injected error) func(string) (metadataDirectory, error) {
				return func(string) (metadataDirectory, error) {
					return &injectedMetadataDirectory{closeErr: injected}, nil
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "world.meta")
			if err := os.WriteFile(path, []byte("previous"), 0o600); err != nil {
				t.Fatal(err)
			}

			var reusedPath string
			rename := func(oldPath, newPath string) error {
				if err := os.Rename(oldPath, newPath); err != nil {
					return err
				}
				reusedPath = oldPath
				return os.WriteFile(reusedPath, []byte("bystander"), 0o600)
			}
			injected := errors.New("injected " + tc.name)
			err := replaceFileAtomicallyWithHooks(
				path,
				[]byte("replacement"),
				0o600,
				atomicReplaceHooks{
					rename:        rename,
					openDirectory: tc.openDirectory(injected),
				},
			)
			if !errors.Is(err, injected) {
				t.Fatalf("replace error = %v, want injected directory error", err)
			}

			canonical, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(canonical) != "replacement" {
				t.Fatalf("canonical after successful rename = %q", canonical)
			}
			bystander, readErr := os.ReadFile(reusedPath)
			if readErr != nil {
				t.Fatalf("reused temp path was removed: %v", readErr)
			}
			if string(bystander) != "bystander" {
				t.Fatalf("reused temp path = %q", bystander)
			}
		})
	}
}
