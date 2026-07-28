package storage

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

func TestRegionCreateWritesTwoBanksAndEmptyRegionMisses(t *testing.T) {
	ctx := context.Background()
	key := RegionKey{Dimension: core.Overworld, X: -1, Z: 2}
	path := filepath.Join(t.TempDir(), "r.-1.2.region")

	r, err := createRegion(ctx, path, key)
	if err != nil {
		t.Fatal(err)
	}
	if r.activeBank != 0 || r.bank.Generation != 1 {
		t.Fatalf("active bank = %d generation %d, want A generation 1", r.activeBank, r.bank.Generation)
	}
	if err := r.close(); err != nil {
		t.Fatal(err)
	}

	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) != dataStartSector*sectorSize {
		t.Fatalf("region size = %d, want %d", len(encoded), dataStartSector*sectorSize)
	}
	if err := decodeSuperblock(key, encoded[:sectorSize]); err != nil {
		t.Fatalf("decode superblock: %v", err)
	}
	bankA, err := decodeRegionBank(
		key,
		encoded[bankOffset(0):bankOffset(0)+regionBankSize],
		int64(len(encoded)),
	)
	if err != nil || bankA.Generation != 1 {
		t.Fatalf("decode bank A = generation %d, %v", bankA.Generation, err)
	}
	bankB, err := decodeRegionBank(
		key,
		encoded[bankOffset(1):bankOffset(1)+regionBankSize],
		int64(len(encoded)),
	)
	if err != nil || bankB.Generation != 0 {
		t.Fatalf("decode bank B = generation %d, %v", bankB.Generation, err)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".r.-1.2.region.create-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary region files remain: %v", matches)
	}

	reopened, err := openRegion(ctx, path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()
	chunkKey := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -1, Z: 64}}
	if _, err := reopened.load(ctx, chunkKey); !errors.Is(err, ErrChunkNotFound) {
		t.Fatalf("empty region load error = %v, want %v", err, ErrChunkNotFound)
	}
}

func TestRegionSaveReopenAndAdvanceBank(t *testing.T) {
	ctx := context.Background()
	key := RegionKey{Dimension: core.Overworld, X: -1, Z: 2}
	path := filepath.Join(t.TempDir(), "r.-1.2.region")
	r, err := createRegion(ctx, path, key)
	if err != nil {
		t.Fatal(err)
	}

	chunkKey := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -1, Z: 64}}
	first := world.NewChunk(chunkKey.Pos)
	first.SetBlock(1, 0, 1, core.StoneID)
	result, err := r.save(ctx, []ChunkSave{{Key: chunkKey, Revision: 1, Chunk: first}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Committed[chunkKey] != 1 {
		t.Fatalf("committed revision = %d, want 1", result.Committed[chunkKey])
	}
	if r.activeBank != 1 || r.bank.Generation != 2 {
		t.Fatalf("bank=%d generation=%d", r.activeBank, r.bank.Generation)
	}
	assertRegionEntryPaddingZero(t, r)

	second := first.Clone()
	second.SetBlock(1, 0, 1, core.DirtID)
	result, err = r.save(ctx, []ChunkSave{{Key: chunkKey, Revision: 2, Chunk: second}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Committed[chunkKey] != 2 {
		t.Fatalf("committed revision = %d, want 2", result.Committed[chunkKey])
	}
	if r.activeBank != 0 || r.bank.Generation != 3 {
		t.Fatalf("bank=%d generation=%d, want A generation 3", r.activeBank, r.bank.Generation)
	}
	assertRegionEntryPaddingZero(t, r)
	if err := r.close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := openRegion(ctx, path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()
	if reopened.activeBank != 0 || reopened.bank.Generation != 3 {
		t.Fatalf("reopened bank=%d generation=%d", reopened.activeBank, reopened.bank.Generation)
	}
	got, err := reopened.load(ctx, chunkKey)
	if err != nil || got.Revision != 2 || got.PersistedRevision != 2 || got.Chunk.Hash() != second.Hash() {
		t.Fatalf("load = %+v, %v", got, err)
	}
}

func TestRegionSaveRevisionRules(t *testing.T) {
	ctx := context.Background()
	key := RegionKey{Dimension: core.Overworld, X: 0, Z: 0}
	path := filepath.Join(t.TempDir(), "r.0.0.region")
	r, err := createRegion(ctx, path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer r.close()

	chunkKey := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: 3, Z: 4}}
	stored := world.NewChunk(chunkKey.Pos)
	stored.SetBlock(1, 0, 1, core.StoneID)
	if _, err := r.save(ctx, []ChunkSave{{Key: chunkKey, Revision: 2, Chunk: stored}}); err != nil {
		t.Fatal(err)
	}

	wantBank, wantGeneration := r.activeBank, r.bank.Generation
	wantInfo, err := r.file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	lower := stored.Clone()
	lower.SetBlock(1, 0, 1, core.DirtID)
	result, err := r.save(ctx, []ChunkSave{{Key: chunkKey, Revision: 1, Chunk: lower}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Committed[chunkKey] != 2 {
		t.Fatalf("lower revision reported %d, want disk revision 2", result.Committed[chunkKey])
	}
	assertRegionUnchanged(t, r, wantBank, wantGeneration, wantInfo.Size())

	result, err = r.save(ctx, []ChunkSave{{Key: chunkKey, Revision: 2, Chunk: stored.Clone()}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Committed[chunkKey] != 2 {
		t.Fatalf("idempotent save reported %d, want 2", result.Committed[chunkKey])
	}
	assertRegionUnchanged(t, r, wantBank, wantGeneration, wantInfo.Size())

	conflict := stored.Clone()
	conflict.SetBlock(1, 0, 1, core.DirtID)
	if _, err := r.save(ctx, []ChunkSave{{Key: chunkKey, Revision: 2, Chunk: conflict}}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("equal revision conflict error = %v, want %v", err, ErrRevisionConflict)
	}
	assertRegionUnchanged(t, r, wantBank, wantGeneration, wantInfo.Size())
}

func TestRegionCanceledSaveDoesNotSwitchBank(t *testing.T) {
	key := RegionKey{Dimension: core.Overworld, X: 0, Z: 0}
	path := filepath.Join(t.TempDir(), "r.0.0.region")
	r, err := createRegion(context.Background(), path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer r.close()

	chunkKey := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: 1, Z: 1}}
	chunk := world.NewChunk(chunkKey.Pos)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	wantBank, wantGeneration := r.activeBank, r.bank.Generation
	if _, err := r.save(canceled, []ChunkSave{{Key: chunkKey, Revision: 1, Chunk: chunk}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled save error = %v, want %v", err, context.Canceled)
	}
	if r.activeBank != wantBank || r.bank.Generation != wantGeneration {
		t.Fatalf("canceled save switched bank to %d generation %d", r.activeBank, r.bank.Generation)
	}
}

func TestRegionRejectsChunkFromDifferentRegion(t *testing.T) {
	ctx := context.Background()
	key := RegionKey{Dimension: core.Overworld, X: 0, Z: 0}
	path := filepath.Join(t.TempDir(), "r.0.0.region")
	r, err := createRegion(ctx, path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer r.close()

	wrongKey := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: 32, Z: 0}}
	if _, err := r.load(ctx, wrongKey); err == nil {
		t.Fatal("load from a different region unexpectedly succeeded")
	}
	if _, err := r.save(ctx, []ChunkSave{{Key: wrongKey, Revision: 1, Chunk: world.NewChunk(wrongKey.Pos)}}); err == nil {
		t.Fatal("save to a different region unexpectedly succeeded")
	}
}

func TestRegionLoadChecksCRCAndReturnsOwnedChunk(t *testing.T) {
	ctx := context.Background()
	key := RegionKey{Dimension: core.Overworld, X: 0, Z: 0}
	path := filepath.Join(t.TempDir(), "r.0.0.region")
	r, err := createRegion(ctx, path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer r.close()

	chunkKey := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: 1, Z: 1}}
	chunk := world.NewChunk(chunkKey.Pos)
	chunk.SetBlock(1, 0, 1, core.StoneID)
	if _, err := r.save(ctx, []ChunkSave{{Key: chunkKey, Revision: 1, Chunk: chunk}}); err != nil {
		t.Fatal(err)
	}
	first, err := r.load(ctx, chunkKey)
	if err != nil {
		t.Fatal(err)
	}
	first.Chunk.SetBlock(1, 0, 1, core.DirtID)
	second, err := r.load(ctx, chunkKey)
	if err != nil {
		t.Fatal(err)
	}
	if second.Chunk.Hash() != chunk.Hash() {
		t.Fatal("mutating loaded chunk changed region-owned data")
	}

	_, slot := RegionFor(chunkKey)
	entry := r.bank.Entries[slot]
	if _, err := r.file.WriteAt([]byte{0xff}, int64(entry.OffsetSector)*sectorSize); err != nil {
		t.Fatal(err)
	}
	if _, err := r.load(ctx, chunkKey); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("corrupt payload error = %v, want %v", err, ErrCorrupt)
	}
}

func TestRegionSyncHonorsCancellation(t *testing.T) {
	key := RegionKey{Dimension: core.Overworld, X: 0, Z: 0}
	r, err := createRegion(context.Background(), filepath.Join(t.TempDir(), "r.0.0.region"), key)
	if err != nil {
		t.Fatal(err)
	}
	defer r.close()
	if err := r.sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.sync(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled sync error = %v, want %v", err, context.Canceled)
	}
}

func TestRegionLoadAfterCloseReturnsClosed(t *testing.T) {
	key, chunkKey := crashRegionKeys()
	r, err := createRegion(context.Background(), filepath.Join(t.TempDir(), "r.0.0.region"), key)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.close(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.load(context.Background(), chunkKey); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("load after close error = %v, want %v", err, os.ErrClosed)
	}
}

func TestRegionSaveAfterCloseReturnsClosed(t *testing.T) {
	key, chunkKey := crashRegionKeys()
	r, err := createRegion(context.Background(), filepath.Join(t.TempDir(), "r.0.0.region"), key)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.close(); err != nil {
		t.Fatal(err)
	}
	var saveErr error
	panicked := true
	func() {
		defer func() { _ = recover() }()
		_, saveErr = r.save(context.Background(), []ChunkSave{changedSave(chunkKey, 1)})
		panicked = false
	}()
	if panicked {
		t.Fatal("save after close panicked")
	}
	if !errors.Is(saveErr, os.ErrClosed) {
		t.Fatalf("save after close error = %v, want %v", saveErr, os.ErrClosed)
	}
}

func assertRegionEntryPaddingZero(t *testing.T, r *region) {
	t.Helper()
	for slot, entry := range r.bank.Entries {
		if entry.OffsetSector == 0 {
			continue
		}
		extent := make([]byte, int(entry.SectorCount)*sectorSize)
		if _, err := r.file.ReadAt(extent, int64(entry.OffsetSector)*sectorSize); err != nil {
			t.Fatalf("read extent for slot %d: %v", slot, err)
		}
		if !bytes.Equal(extent[entry.PayloadLength:], make([]byte, len(extent)-int(entry.PayloadLength))) {
			t.Fatalf("slot %d extent padding is nonzero", slot)
		}
	}
}

func assertRegionUnchanged(t *testing.T, r *region, bank int, generation uint64, size int64) {
	t.Helper()
	info, err := r.file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if r.activeBank != bank || r.bank.Generation != generation || info.Size() != size {
		t.Fatalf(
			"region changed: bank=%d generation=%d size=%d, want %d/%d/%d",
			r.activeBank, r.bank.Generation, info.Size(), bank, generation, size,
		)
	}
}
