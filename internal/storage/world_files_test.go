package storage

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"minecraft-go/internal/core"
)

func TestExistingMetadataOverridesCreateOptions(t *testing.T) {
	root := t.TempDir()
	first, err := openWorldFiles(context.Background(), root, OpenOptions{
		Create: Metadata{FormatVersion: 1, Seed: 42, SpawnDimension: core.Overworld,
			SpawnAnchor: core.ChunkPos{X: 3, Z: -2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.close(); err != nil {
		t.Fatal(err)
	}

	second, err := openWorldFiles(context.Background(), root, OpenOptions{
		Create: Metadata{FormatVersion: 1, Seed: 999},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer second.close()
	if second.metadata.Seed != 42 || second.metadata.SpawnAnchor != (core.ChunkPos{X: 3, Z: -2}) {
		t.Fatalf("existing metadata not authoritative: %+v", second.metadata)
	}
}

func TestWorldLockRejectsConcurrentOpenBeforeMetadataRead(t *testing.T) {
	root := t.TempDir()
	first, err := openWorldFiles(context.Background(), root, OpenOptions{
		Create: Metadata{FormatVersion: 1, Seed: 42},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer first.close()

	metadataPath := filepath.Join(root, "world.meta")
	corrupt, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	corrupt[12] ^= 0xff
	if err := os.WriteFile(metadataPath, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}

	result := make(chan error, 1)
	go func() {
		_, err := openWorldFiles(context.Background(), root, OpenOptions{})
		result <- err
	}()
	select {
	case err := <-result:
		if !errors.Is(err, ErrWorldLocked) {
			t.Fatalf("concurrent open error = %v, want ErrWorldLocked", err)
		}
	case <-time.After(time.Second):
		t.Fatal("concurrent open blocked instead of returning immediately")
	}
}

func TestWorldLockCloseReleasesLock(t *testing.T) {
	root := t.TempDir()
	first, err := openWorldFiles(context.Background(), root, OpenOptions{
		Create: Metadata{FormatVersion: 1, Seed: 42},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.close(); err != nil {
		t.Fatal(err)
	}

	second, err := openWorldFiles(context.Background(), root, OpenOptions{})
	if err != nil {
		t.Fatalf("reopen after close: %v", err)
	}
	if err := second.close(); err != nil {
		t.Fatal(err)
	}
}

func TestMetadataOpenRejectsCorruptionWithoutRewriting(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func([]byte)
		wantErr error
	}{
		{
			name:    "CRC corruption",
			mutate:  func(data []byte) { data[12] ^= 0xff },
			wantErr: ErrCorrupt,
		},
		{
			name: "future version",
			mutate: func(data []byte) {
				binary.LittleEndian.PutUint32(data[4:8], currentMetadataVersion+1)
			},
			wantErr: ErrFutureVersion,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			files, err := openWorldFiles(context.Background(), root, OpenOptions{
				Create: Metadata{FormatVersion: 1, Seed: 42},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := files.close(); err != nil {
				t.Fatal(err)
			}

			path := filepath.Join(root, "world.meta")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			tc.mutate(before)
			if err := os.WriteFile(path, before, 0o600); err != nil {
				t.Fatal(err)
			}

			opened, err := openWorldFiles(context.Background(), root, OpenOptions{
				Create: Metadata{FormatVersion: 1, Seed: 999},
			})
			if opened != nil {
				opened.close()
				t.Fatal("invalid metadata returned world files")
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("open invalid metadata error = %v, want %v", err, tc.wantErr)
			}
			after, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !bytes.Equal(after, before) {
				t.Fatal("failed open rewrote invalid metadata")
			}
		})
	}
}

func TestMetadataCanceledCreateLeavesNoFile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "new-world")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	files, err := openWorldFiles(ctx, root, OpenOptions{
		Create: Metadata{FormatVersion: 1, Seed: 42},
	})
	if files != nil {
		files.close()
		t.Fatal("canceled create returned world files")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled create error = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(filepath.Join(root, "world.meta")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("world.meta after canceled create: %v", err)
	}
}
