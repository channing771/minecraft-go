package storage

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/world"
)

type compactChunkExpectation struct {
	key      core.ChunkKey
	revision uint64
	hash     [32]byte
}

func TestRegionCompactReplacesFragmentedFileWithoutChangingChunks(t *testing.T) {
	r, path, key, expected := seededFragmentedRegion(t)
	defer r.close()

	policy := regionSpacePolicy{WasteRatio: 0.20, MinWaste: sectorSize}
	if !r.shouldCompact(policy) {
		t.Fatal("fragmented region did not meet compaction policy")
	}
	if r.shouldCompact(regionSpacePolicy{WasteRatio: 0.51, MinWaste: sectorSize}) {
		t.Fatal("region compacted below the waste-ratio threshold")
	}
	if r.shouldCompact(regionSpacePolicy{WasteRatio: 0.20, MinWaste: 3 * sectorSize}) {
		t.Fatal("region compacted below the minimum-waste threshold")
	}

	wantGeneration := r.bank.Generation
	wantSize := compactRegionSize(r.bank)
	if err := r.compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.activeBank != 0 || r.bank.Generation != wantGeneration {
		t.Fatalf(
			"compacted bank = %d generation %d, want bank A generation %d",
			r.activeBank, r.bank.Generation, wantGeneration,
		)
	}
	assertCompactChunks(t, r, expected)
	info, err := r.file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != wantSize {
		t.Fatalf("compacted size = %d, want %d", info.Size(), wantSize)
	}
	assertNoRegionCompactTemps(t, path, "")

	if err := r.close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openRegion(context.Background(), path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()
	assertCompactChunks(t, reopened, expected)
}

func TestRegionCompactFailureReopensCompleteCanonical(t *testing.T) {
	tests := []struct {
		name          string
		install       func(*region, error)
		wantCompacted bool
	}{
		{
			name: "before temp sync",
			install: func(r *region, injected error) {
				r.compactionHooks.beforeTempSync = func() error { return injected }
			},
		},
		{
			name: "rename",
			install: func(r *region, injected error) {
				r.compactionHooks.rename = func(string, string) error { return injected }
			},
		},
		{
			name: "directory sync",
			install: func(r *region, injected error) {
				r.compactionHooks.syncDirectory = func(string) error { return injected }
			},
			wantCompacted: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, path, key, expected := seededFragmentedRegion(t)
			defer r.close()
			before, err := r.file.Stat()
			if err != nil {
				t.Fatal(err)
			}
			compactedSize := compactRegionSize(r.bank)
			bystander := filepath.Join(
				filepath.Dir(path), "."+filepath.Base(path)+".compact-keep",
			)
			if err := os.WriteFile(bystander, []byte("bystander"), 0o600); err != nil {
				t.Fatal(err)
			}

			injected := errors.New("injected " + tc.name + " failure")
			tc.install(r, injected)
			if err := r.compact(context.Background()); !errors.Is(err, injected) {
				t.Fatalf("compact error = %v, want injected failure", err)
			}

			// The in-memory region must be usable even when compact closed the old
			// descriptor before the injected replacement failure.
			assertCompactChunks(t, r, expected)
			info, err := r.file.Stat()
			if err != nil {
				t.Fatal(err)
			}
			wantSize := before.Size()
			if tc.wantCompacted {
				wantSize = compactedSize
			}
			if info.Size() != wantSize {
				t.Fatalf("canonical size after failure = %d, want %d", info.Size(), wantSize)
			}
			assertNoRegionCompactTemps(t, path, bystander)

			if err := r.close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := openRegion(context.Background(), path, key)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.close()
			assertCompactChunks(t, reopened, expected)
		})
	}
}

type closeObservedRegionFile struct {
	regionFile
	closed bool
}

func (file *closeObservedRegionFile) Close() error {
	err := file.regionFile.Close()
	file.closed = true
	return err
}

func TestRegionCompactClosesCanonicalBeforeRename(t *testing.T) {
	seeded, path, key, expected := seededFragmentedRegion(t)
	if err := seeded.close(); err != nil {
		t.Fatal(err)
	}

	var old *closeObservedRegionFile
	r, err := openRegionWithHooks(
		context.Background(), path, key,
		regionFileHooks{Open: func(name string, flag int, mode fs.FileMode) (regionFile, error) {
			file, err := os.OpenFile(name, flag, mode)
			if err != nil {
				return nil, err
			}
			observed := &closeObservedRegionFile{regionFile: file}
			if old == nil {
				old = observed
			}
			return observed, nil
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer r.close()
	r.compactionHooks.rename = func(temporary, canonical string) error {
		if !old.closed {
			return errors.New("canonical descriptor remained open during rename")
		}
		return os.Rename(temporary, canonical)
	}

	if err := r.compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertCompactChunks(t, r, expected)
}

func seededFragmentedRegion(
	t *testing.T,
) (*region, string, RegionKey, []compactChunkExpectation) {
	t.Helper()
	key := RegionKey{Dimension: core.Overworld, X: 0, Z: 0}
	path := filepath.Join(t.TempDir(), "r.0.0.region")
	r, err := createRegion(context.Background(), path, key)
	if err != nil {
		t.Fatal(err)
	}
	keys := []core.ChunkKey{
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: 1, Z: 1}},
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: 2, Z: 2}},
	}
	for revision := uint64(1); revision <= 3; revision++ {
		saves := make([]ChunkSave, 0, len(keys))
		for index, chunkKey := range keys {
			chunk := world.NewChunk(chunkKey.Pos)
			block := core.StoneID
			if (revision+uint64(index))%2 == 0 {
				block = core.DirtID
			}
			chunk.SetBlock(index+1, 0, index+1, block)
			saves = append(saves, ChunkSave{Key: chunkKey, Revision: revision, Chunk: chunk})
		}
		if _, err := r.save(context.Background(), saves); err != nil {
			r.close()
			t.Fatal(err)
		}
	}

	expected := make([]compactChunkExpectation, 0, len(keys))
	for _, chunkKey := range keys {
		stored, err := r.load(context.Background(), chunkKey)
		if err != nil {
			r.close()
			t.Fatal(err)
		}
		expected = append(expected, compactChunkExpectation{
			key: chunkKey, revision: stored.PersistedRevision, hash: stored.Chunk.Hash(),
		})
	}
	return r, path, key, expected
}

func compactRegionSize(bank regionBank) int64 {
	size := int64(dataStartSector * sectorSize)
	for _, entry := range bank.Entries {
		size += int64(entry.SectorCount) * sectorSize
	}
	return size
}

func assertCompactChunks(t *testing.T, r *region, expected []compactChunkExpectation) {
	t.Helper()
	for _, want := range expected {
		got, err := r.load(context.Background(), want.key)
		if err != nil {
			t.Fatal(err)
		}
		if got.Revision != want.revision || got.PersistedRevision != want.revision ||
			got.Chunk.Hash() != want.hash {
			t.Fatalf("chunk %v after compact = %+v hash=%x, want revision %d hash=%x",
				want.key, got, got.Chunk.Hash(), want.revision, want.hash)
		}
	}
}

func assertNoRegionCompactTemps(t *testing.T, path, bystander string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(
		filepath.Dir(path), "."+filepath.Base(path)+".compact-*",
	))
	if err != nil {
		t.Fatal(err)
	}
	if bystander == "" {
		if len(matches) != 0 {
			t.Fatalf("temporary compact files remain: %v", matches)
		}
		return
	}
	if len(matches) != 1 || matches[0] != bystander {
		t.Fatalf("compact temps = %v, want only bystander %q", matches, bystander)
	}
	got, err := os.ReadFile(bystander)
	if err != nil {
		t.Fatalf("read bystander: %v", err)
	}
	if string(got) != "bystander" {
		t.Fatalf("bystander contents = %q", got)
	}
}
