package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/world"
)

func TestDiskStorePersistsNegativeAndMultipleRegions(t *testing.T) {
	root := t.TempDir()
	store, err := OpenDisk(context.Background(), root, OpenOptions{
		Create: Metadata{FormatVersion: 2, Seed: 42, SpawnDimension: core.Overworld},
	})
	if err != nil {
		t.Fatal(err)
	}
	keys := []core.ChunkKey{
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: -33, Z: -1}},
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: 32, Z: 64}},
	}
	if _, err := store.SaveBatch(context.Background(), diskSavesFor(keys, 1)); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(root, "dimensions", "0", "regions", "r.-2.-1.region"),
		filepath.Join(root, "dimensions", "0", "regions", "r.1.2.region"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("stat exact region path %q: %v", path, err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenDisk(context.Background(), root, OpenOptions{
		Create: Metadata{FormatVersion: 2, Seed: 999},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.Metadata().Seed != 42 {
		t.Fatal("stored metadata seed lost")
	}
	for _, key := range keys {
		stored, err := reopened.LoadChunk(context.Background(), key)
		if err != nil {
			t.Fatal(err)
		}
		if stored.Key != key || stored.Revision != 1 || stored.PersistedRevision != 1 {
			t.Fatalf("loaded chunk = %+v, want key %v revision 1", stored, key)
		}
	}
}

func TestDiskStoreLoadMissDoesNotCreateRegion(t *testing.T) {
	root := t.TempDir()
	store, err := OpenDisk(context.Background(), root, OpenOptions{
		Create: Metadata{FormatVersion: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	key := core.ChunkKey{Dimension: 3, Pos: core.ChunkPos{X: -65, Z: 96}}
	if _, err := store.LoadChunk(context.Background(), key); !errors.Is(err, ErrChunkNotFound) {
		t.Fatalf("missing chunk error = %v, want %v", err, ErrChunkNotFound)
	}
	path := filepath.Join(root, "dimensions", "3", "regions", "r.-3.3.region")
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing load created region %q: %v", path, err)
	}
	if len(store.regions) != 0 {
		t.Fatalf("missing load cached %d regions, want 0", len(store.regions))
	}
}

func TestDiskStoreSaveBatchValidatesAllChunksBeforeCreatingRegions(t *testing.T) {
	root := t.TempDir()
	store, err := OpenDisk(context.Background(), root, OpenOptions{
		Create: Metadata{FormatVersion: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	validKey := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: 1, Z: 1}}
	invalidKey := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: 33, Z: 1}}
	valid := world.NewChunk(validKey.Pos)
	result, err := store.SaveBatch(context.Background(), []ChunkSave{
		{Key: validKey, Revision: 1, Chunk: valid},
		{Key: invalidKey, Revision: 1, Chunk: nil},
	})
	if err == nil {
		t.Fatal("batch with nil chunk unexpectedly succeeded")
	}
	if len(result.Committed) != 0 {
		t.Fatalf("invalid batch committed chunks: %v", result.Committed)
	}
	for _, path := range []string{
		filepath.Join(root, "dimensions", "0", "regions", "r.0.0.region"),
		filepath.Join(root, "dimensions", "0", "regions", "r.1.0.region"),
	} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("invalid batch created region %q: %v", path, err)
		}
	}
}

func TestDiskStoreSaveBatchIgnoresConflictingLowerRevisionsInEveryOrder(t *testing.T) {
	key := core.ChunkKey{
		Dimension: core.Overworld,
		Pos:       core.ChunkPos{X: 1, Z: 1},
	}
	lowerA := world.NewChunk(key.Pos)
	lowerA.SetBlock(0, 0, 0, core.StoneID)
	lowerB := world.NewChunk(key.Pos)
	lowerB.SetBlock(0, 0, 0, core.DirtID)
	highest := world.NewChunk(key.Pos)
	highest.SetBlock(0, 0, 0, core.GrassID)
	permutations := threeSavePermutations([]ChunkSave{
		{Key: key, Revision: 1, Chunk: lowerA},
		{Key: key, Revision: 1, Chunk: lowerB},
		{Key: key, Revision: 2, Chunk: highest},
	})

	for index, saves := range permutations {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			normalized, err := validateAndNormalizeSaves(saves)
			if err != nil {
				t.Fatalf("normalize lower conflicts: %v", err)
			}
			if len(normalized) != 1 || normalized[0].Key != key ||
				normalized[0].Revision != 2 || normalized[0].Chunk.Hash() != highest.Hash() {
				t.Fatalf("normalized saves = %+v, want only revision 2 highest chunk", normalized)
			}

			store := openTestDiskStore(t)
			result, err := store.SaveBatch(context.Background(), saves)
			if err != nil {
				store.Close()
				t.Fatalf("save lower conflicts: %v", err)
			}
			if len(result.Committed) != 1 || result.Committed[key] != 2 {
				store.Close()
				t.Fatalf("committed = %v, want only revision 2", result.Committed)
			}
			stored, err := store.LoadChunk(context.Background(), key)
			if err != nil || stored.Revision != 2 || stored.Chunk.Hash() != highest.Hash() {
				store.Close()
				t.Fatalf("stored highest revision = %+v, %v", stored, err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDiskStoreSaveBatchAcceptsMaxRevisionEqualHashInEveryOrder(t *testing.T) {
	key := core.ChunkKey{
		Dimension: core.Overworld,
		Pos:       core.ChunkPos{X: 1, Z: 1},
	}
	lower := world.NewChunk(key.Pos)
	lower.SetBlock(0, 0, 0, core.StoneID)
	highest := world.NewChunk(key.Pos)
	highest.SetBlock(0, 0, 0, core.GrassID)
	permutations := threeSavePermutations([]ChunkSave{
		{Key: key, Revision: 1, Chunk: lower},
		{Key: key, Revision: 2, Chunk: highest},
		{Key: key, Revision: 2, Chunk: highest.Clone()},
	})

	for index, saves := range permutations {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			store := openTestDiskStore(t)
			result, err := store.SaveBatch(context.Background(), saves)
			if err != nil {
				store.Close()
				t.Fatal(err)
			}
			if len(result.Committed) != 1 || result.Committed[key] != 2 {
				store.Close()
				t.Fatalf("committed = %v, want only revision 2", result.Committed)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDiskStoreSaveBatchRejectsMaxRevisionConflictBeforeFilesystemEffects(t *testing.T) {
	firstKey := core.ChunkKey{
		Dimension: core.Overworld,
		Pos:       core.ChunkPos{X: -32, Z: 0},
	}
	conflictKey := core.ChunkKey{
		Dimension: core.Overworld,
		Pos:       core.ChunkPos{X: 32, Z: 0},
	}
	first := world.NewChunk(firstKey.Pos)
	first.SetBlock(0, 0, 0, core.StoneID)
	lower := world.NewChunk(conflictKey.Pos)
	lower.SetBlock(0, 0, 0, core.BedrockID)
	conflictA := world.NewChunk(conflictKey.Pos)
	conflictA.SetBlock(0, 0, 0, core.StoneID)
	conflictB := world.NewChunk(conflictKey.Pos)
	conflictB.SetBlock(0, 0, 0, core.DirtID)
	permutations := threeSavePermutations([]ChunkSave{
		{Key: conflictKey, Revision: 1, Chunk: lower},
		{Key: conflictKey, Revision: 2, Chunk: conflictA},
		{Key: conflictKey, Revision: 2, Chunk: conflictB},
	})

	for index, conflictSaves := range permutations {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			root := t.TempDir()
			store, err := OpenDisk(context.Background(), root, OpenOptions{
				Create: Metadata{FormatVersion: 2},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			saves := append([]ChunkSave{{Key: firstKey, Revision: 1, Chunk: first}}, conflictSaves...)
			result, err := store.SaveBatch(context.Background(), saves)
			if !errors.Is(err, ErrRevisionConflict) {
				t.Fatalf("conflicting batch error = %v, want %v", err, ErrRevisionConflict)
			}
			if len(result.Committed) != 0 {
				t.Fatalf("conflicting batch committed chunks: %v", result.Committed)
			}
			if len(store.regions) != 0 {
				t.Fatalf("conflicting batch cached regions: %v", store.regions)
			}
			if _, err := os.Stat(filepath.Join(root, "dimensions")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("conflicting batch created dimensions directory: %v", err)
			}
		})
	}
}

func TestValidateAndNormalizeSavesReturnsDeterministicChunkKeyOrder(t *testing.T) {
	keys := []core.ChunkKey{
		{Dimension: 1, Pos: core.ChunkPos{X: 0, Z: 0}},
		{Dimension: 0, Pos: core.ChunkPos{X: 5, Z: -2}},
		{Dimension: 0, Pos: core.ChunkPos{X: -1, Z: 9}},
		{Dimension: 0, Pos: core.ChunkPos{X: -1, Z: -3}},
	}
	want := []core.ChunkKey{
		{Dimension: 0, Pos: core.ChunkPos{X: -1, Z: -3}},
		{Dimension: 0, Pos: core.ChunkPos{X: -1, Z: 9}},
		{Dimension: 0, Pos: core.ChunkPos{X: 5, Z: -2}},
		{Dimension: 1, Pos: core.ChunkPos{X: 0, Z: 0}},
	}
	orders := [][]core.ChunkKey{keys, {keys[3], keys[2], keys[1], keys[0]}}
	for index, order := range orders {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			normalized, err := validateAndNormalizeSaves(diskSavesFor(order, 1))
			if err != nil {
				t.Fatal(err)
			}
			got := make([]core.ChunkKey, len(normalized))
			for index, save := range normalized {
				got[index] = save.Key
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("normalized key order = %v, want %v", got, want)
			}
		})
	}
}

func TestDiskStoreSaveBatchOrdersRegionGroups(t *testing.T) {
	store := openTestDiskStore(t)
	defer store.Close()
	keys := []core.ChunkKey{
		{Dimension: 1, Pos: core.ChunkPos{X: -64, Z: 96}},
		{Dimension: 0, Pos: core.ChunkPos{X: 64, Z: -96}},
		{Dimension: 0, Pos: core.ChunkPos{X: -32, Z: 160}},
		{Dimension: -1, Pos: core.ChunkPos{X: 288, Z: 64}},
		{Dimension: 0, Pos: core.ChunkPos{X: -32, Z: -64}},
	}
	if _, err := store.SaveBatch(context.Background(), diskSavesFor(keys, 1)); err != nil {
		t.Fatal(err)
	}

	events := make([]RegionKey, 0, len(keys)*2)
	for key, opened := range store.regions {
		opened.file = &observedRegionFile{
			regionFile: opened.file,
			key:        key,
			syncEvents: &events,
		}
	}
	if _, err := store.SaveBatch(context.Background(), diskSavesFor(keys, 2)); err != nil {
		t.Fatal(err)
	}
	want := []RegionKey{
		{Dimension: -1, X: 9, Z: 2},
		{Dimension: -1, X: 9, Z: 2},
		{Dimension: 0, X: -1, Z: -2},
		{Dimension: 0, X: -1, Z: -2},
		{Dimension: 0, X: -1, Z: 5},
		{Dimension: 0, X: -1, Z: 5},
		{Dimension: 0, X: 2, Z: -3},
		{Dimension: 0, X: 2, Z: -3},
		{Dimension: 1, X: -2, Z: 3},
		{Dimension: 1, X: -2, Z: 3},
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("region save sync order = %+v, want %+v", events, want)
	}
}

func TestDiskStoreSaveBatchReturnsEarlierRegionCommitsOnLaterFailure(t *testing.T) {
	store := openTestDiskStore(t)
	defer store.Close()
	first := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -32, Z: 0}}
	second := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: 32, Z: 0}}
	if _, err := store.SaveBatch(context.Background(), diskSavesFor([]core.ChunkKey{first, second}, 1)); err != nil {
		t.Fatal(err)
	}

	injected := errors.New("injected second-region sync failure")
	secondRegion, _ := RegionFor(second)
	opened := store.regions[secondRegion]
	opened.file = &observedRegionFile{
		regionFile: opened.file,
		key:        secondRegion,
		syncErrors: []error{injected},
	}
	result, err := store.SaveBatch(
		context.Background(), diskSavesFor([]core.ChunkKey{second, first}, 2),
	)
	if !errors.Is(err, injected) {
		t.Fatalf("second-region failure = %v, want injected error", err)
	}
	if len(result.Committed) != 1 || result.Committed[first] != 2 {
		t.Fatalf("partial committed = %v, want only %v at revision 2", result.Committed, first)
	}
	firstStored, err := store.LoadChunk(context.Background(), first)
	if err != nil || firstStored.Revision != 2 {
		t.Fatalf("first region after partial commit = %+v, %v", firstStored, err)
	}
	secondStored, err := store.LoadChunk(context.Background(), second)
	if err != nil || secondStored.Revision != 1 {
		t.Fatalf("failed second region = %+v, %v, want revision 1", secondStored, err)
	}
}

func TestDiskStoreSaveBatchRunsProductionCompactionAndPreservesCommitOnFailure(t *testing.T) {
	store := openTestDiskStore(t)
	defer store.Close()
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: 1, Z: 1}}
	for revision := uint64(1); revision <= 2; revision++ {
		if _, err := store.SaveBatch(
			context.Background(), diskSavesFor([]core.ChunkKey{key}, revision),
		); err != nil {
			t.Fatal(err)
		}
	}

	oldPolicy := productionRegionSpacePolicy
	productionRegionSpacePolicy = regionSpacePolicy{WasteRatio: 0.20, MinWaste: sectorSize}
	defer func() { productionRegionSpacePolicy = oldPolicy }()
	regionKey, _ := RegionFor(key)
	opened := store.regions[regionKey]
	if !opened.shouldCompact(productionRegionSpacePolicy) {
		t.Fatal("fragmented test region does not meet production compaction hook policy")
	}
	injected := errors.New("injected production compaction failure")
	opened.compactionHooks.beforeTempSync = func() error { return injected }

	result, err := store.SaveBatch(
		context.Background(), diskSavesFor([]core.ChunkKey{key}, 3),
	)
	if !errors.Is(err, injected) {
		t.Fatalf("compaction failure = %v, want injected error", err)
	}
	if result.Committed[key] != 3 {
		t.Fatalf("committed revision after compaction failure = %d, want 3", result.Committed[key])
	}
	stored, err := store.LoadChunk(context.Background(), key)
	if err != nil || stored.Revision != 3 {
		t.Fatalf("load committed chunk after compaction failure = %+v, %v", stored, err)
	}
}

func TestDiskStoreSyncVisitsAllRegionsInOrderAndJoinsErrors(t *testing.T) {
	store := openTestDiskStore(t)
	defer store.Close()
	keys := []core.ChunkKey{
		{Dimension: 1, Pos: core.ChunkPos{X: 0, Z: 0}},
		{Dimension: 0, Pos: core.ChunkPos{X: 32, Z: 0}},
		{Dimension: 0, Pos: core.ChunkPos{X: -32, Z: 0}},
	}
	if _, err := store.SaveBatch(context.Background(), diskSavesFor(keys, 1)); err != nil {
		t.Fatal(err)
	}

	firstErr := errors.New("injected first sync failure")
	lastErr := errors.New("injected last sync failure")
	events := make([]RegionKey, 0, len(keys))
	for key, opened := range store.regions {
		var syncErrors []error
		if key == (RegionKey{Dimension: 0, X: -1, Z: 0}) {
			syncErrors = []error{firstErr}
		}
		if key == (RegionKey{Dimension: 1, X: 0, Z: 0}) {
			syncErrors = []error{lastErr}
		}
		opened.file = &observedRegionFile{
			regionFile: opened.file,
			key:        key,
			syncEvents: &events,
			syncErrors: syncErrors,
		}
	}
	err := store.Sync(context.Background())
	if !errors.Is(err, firstErr) || !errors.Is(err, lastErr) {
		t.Fatalf("joined sync error = %v, want both injected errors", err)
	}
	want := []RegionKey{
		{Dimension: 0, X: -1, Z: 0},
		{Dimension: 0, X: 1, Z: 0},
		{Dimension: 1, X: 0, Z: 0},
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("region sync order = %+v, want %+v", events, want)
	}
}

func TestDiskStoreCloseRetriesOnlyFailuresAndRetainsLock(t *testing.T) {
	root := t.TempDir()
	store, err := OpenDisk(context.Background(), root, OpenOptions{
		Create: Metadata{FormatVersion: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	keys := []core.ChunkKey{
		{Dimension: 0, Pos: core.ChunkPos{X: 32, Z: 0}},
		{Dimension: 0, Pos: core.ChunkPos{X: -32, Z: 0}},
		{Dimension: 0, Pos: core.ChunkPos{X: 0, Z: 0}},
	}
	if _, err := store.SaveBatch(context.Background(), diskSavesFor(keys, 1)); err != nil {
		t.Fatal(err)
	}

	firstErr := errors.New("injected first close failure")
	lastErr := errors.New("injected last close failure")
	events := make([]RegionKey, 0, 3)
	observed := make(map[RegionKey]*observedRegionFile, len(keys))
	for key, opened := range store.regions {
		var closeErrors []error
		if key == (RegionKey{Dimension: 0, X: -1, Z: 0}) {
			closeErrors = []error{firstErr}
		}
		if key == (RegionKey{Dimension: 0, X: 1, Z: 0}) {
			closeErrors = []error{lastErr}
		}
		wrapped := &observedRegionFile{
			regionFile:  opened.file,
			key:         key,
			closeEvents: &events,
			closeErrors: closeErrors,
		}
		opened.file = wrapped
		observed[key] = wrapped
	}

	err = store.Close()
	if !errors.Is(err, firstErr) || !errors.Is(err, lastErr) {
		t.Fatalf("first close error = %v, want both injected errors", err)
	}
	wantFirst := []RegionKey{
		{Dimension: 0, X: -1, Z: 0},
		{Dimension: 0, X: 0, Z: 0},
		{Dimension: 0, X: 1, Z: 0},
	}
	if !reflect.DeepEqual(events, wantFirst) {
		t.Fatalf("first close order = %+v, want %+v", events, wantFirst)
	}
	if store.files.lock == nil {
		t.Fatal("failed region close released world lock")
	}
	if len(store.regions) != 2 {
		t.Fatalf("regions retained after failed close = %d, want 2", len(store.regions))
	}
	for _, key := range []RegionKey{
		{Dimension: 0, X: -1, Z: 0},
		{Dimension: 0, X: 1, Z: 0},
	} {
		if store.regions[key].file != nil {
			t.Fatalf("failed region %+v retained consumed file ownership", key)
		}
	}
	if _, err := OpenDisk(context.Background(), root, OpenOptions{}); !errors.Is(err, ErrWorldLocked) {
		t.Fatalf("open while close retry pending = %v, want %v", err, ErrWorldLocked)
	}
	if _, err := store.LoadChunk(context.Background(), keys[0]); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("load after close began = %v, want %v", err, os.ErrClosed)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("retry close: %v", err)
	}
	if !reflect.DeepEqual(events, wantFirst) {
		t.Fatalf("retry close touched consumed descriptors: %+v", events)
	}
	for key, file := range observed {
		if file.closeCalls != 1 {
			t.Fatalf("region %+v underlying close calls = %d, want 1", key, file.closeCalls)
		}
	}
	if store.files.lock != nil {
		t.Fatal("successful close retained world lock")
	}
	if err := store.Close(); err != nil {
		t.Fatalf("idempotent close: %v", err)
	}
	if !reflect.DeepEqual(events, wantFirst) {
		t.Fatalf("idempotent close touched regions: %+v", events)
	}

	reopened, err := OpenDisk(context.Background(), root, OpenOptions{})
	if err != nil {
		t.Fatalf("reopen after successful retry: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDiskStoreClosePublishesClosingBeforeWaitingForActiveOperation(t *testing.T) {
	store := openTestDiskStore(t)
	key := core.ChunkKey{
		Dimension: core.Overworld,
		Pos:       core.ChunkPos{X: 1, Z: 1},
	}
	if _, err := store.SaveBatch(
		context.Background(), diskSavesFor([]core.ChunkKey{key}, 1),
	); err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	regionKey, _ := RegionFor(key)
	opened := store.regions[regionKey]
	opened.file = &gatedReadRegionFile{
		regionFile: opened.file,
		started:    started,
		release:    release,
	}

	activeResult := make(chan error, 1)
	go func() {
		_, err := store.LoadChunk(context.Background(), key)
		activeResult <- err
	}()
	waitForTestSignal(t, started, "active load did not reach the read gate")

	closeResult := make(chan error, 1)
	go func() { closeResult <- store.Close() }()
	waitForDiskStoreClosing(t, store)

	laterLoad := make(chan error, 1)
	go func() {
		_, err := store.LoadChunk(context.Background(), key)
		laterLoad <- err
	}()
	if err := waitForTestError(t, laterLoad, "later load blocked behind active operation"); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("later load error = %v, want %v", err, os.ErrClosed)
	}

	laterSave := make(chan error, 1)
	go func() {
		_, err := store.SaveBatch(
			context.Background(), diskSavesFor([]core.ChunkKey{key}, 2),
		)
		laterSave <- err
	}()
	if err := waitForTestError(t, laterSave, "later save blocked behind active operation"); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("later save error = %v, want %v", err, os.ErrClosed)
	}

	releaseOnce.Do(func() { close(release) })
	if err := waitForTestError(t, activeResult, "active load did not finish"); err != nil {
		t.Fatalf("active load error = %v", err)
	}
	if err := waitForTestError(t, closeResult, "close did not finish"); err != nil {
		t.Fatalf("close error = %v", err)
	}
}

type observedRegionFile struct {
	regionFile
	key         RegionKey
	syncEvents  *[]RegionKey
	closeEvents *[]RegionKey
	syncErrors  []error
	closeErrors []error
	syncCalls   int
	closeCalls  int
}

type gatedReadRegionFile struct {
	regionFile
	started chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (file *gatedReadRegionFile) ReadAt(data []byte, offset int64) (int, error) {
	file.once.Do(func() { close(file.started) })
	<-file.release
	return file.regionFile.ReadAt(data, offset)
}

func (file *observedRegionFile) Sync() error {
	if file.syncEvents != nil {
		*file.syncEvents = append(*file.syncEvents, file.key)
	}
	index := file.syncCalls
	file.syncCalls++
	if index < len(file.syncErrors) && file.syncErrors[index] != nil {
		return file.syncErrors[index]
	}
	return file.regionFile.Sync()
}

func (file *observedRegionFile) Close() error {
	if file.closeEvents != nil {
		*file.closeEvents = append(*file.closeEvents, file.key)
	}
	index := file.closeCalls
	file.closeCalls++
	underlyingErr := file.regionFile.Close()
	if index < len(file.closeErrors) && file.closeErrors[index] != nil {
		return errors.Join(underlyingErr, file.closeErrors[index])
	}
	return underlyingErr
}

func openTestDiskStore(t *testing.T) *DiskStore {
	t.Helper()
	store, err := OpenDisk(context.Background(), t.TempDir(), OpenOptions{
		Create: Metadata{FormatVersion: 2, Seed: 42},
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func diskSavesFor(keys []core.ChunkKey, revision uint64) []ChunkSave {
	saves := make([]ChunkSave, 0, len(keys))
	for index, key := range keys {
		chunk := world.NewChunk(key.Pos)
		chunk.SetBlock(index&15, 0, index>>4&15, core.BlockID(revision%4+1))
		saves = append(saves, ChunkSave{Key: key, Revision: revision, Chunk: chunk})
	}
	return saves
}

func threeSavePermutations(saves []ChunkSave) [][]ChunkSave {
	return [][]ChunkSave{
		{saves[0], saves[1], saves[2]},
		{saves[0], saves[2], saves[1]},
		{saves[1], saves[0], saves[2]},
		{saves[1], saves[2], saves[0]},
		{saves[2], saves[0], saves[1]},
		{saves[2], saves[1], saves[0]},
	}
}

func waitForDiskStoreClosing(t *testing.T, store *DiskStore) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !store.closing.Load() {
		if time.Now().After(deadline) {
			t.Fatal("close did not publish closing state")
		}
		// 热轮询（runtime.Gosched）改为固定 sleep 退避：饱和并行 race 测试中
		// 空转等待抢核拖慢条件生产者并施压邻居测试（与 internal/server 测试
		// 同型治理保持一致）。
		time.Sleep(500 * time.Microsecond)
	}
}

func waitForTestSignal(t *testing.T, signal <-chan struct{}, failure string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal(failure)
	}
}

func waitForTestError(t *testing.T, result <-chan error, failure string) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(time.Second):
		t.Fatal(failure)
		return nil
	}
}
