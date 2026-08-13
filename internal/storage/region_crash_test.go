package storage

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/world"
)

const regionCrashExitCode = 86

const (
	regionCommitMutationPoints = 4
	regionCrashHelperTimeout   = 10 * time.Second
)

var errInjectedRegionMutation = errors.New("injected region mutation failure")

type mutatingRegionFile struct {
	regionFile
	failAt     int
	crashAfter int
	calls      int
}

func (f *mutatingRegionFile) WriteAt(data []byte, offset int64) (int, error) {
	call, err := f.beforeMutation()
	if err != nil {
		return 0, err
	}
	written, err := f.regionFile.WriteAt(data, offset)
	f.crashAfterMutation(call, err)
	return written, err
}

func (f *mutatingRegionFile) Sync() error {
	call, err := f.beforeMutation()
	if err != nil {
		return err
	}
	err = f.regionFile.Sync()
	f.crashAfterMutation(call, err)
	return err
}

func (f *mutatingRegionFile) Truncate(size int64) error {
	call, err := f.beforeMutation()
	if err != nil {
		return err
	}
	err = f.regionFile.Truncate(size)
	f.crashAfterMutation(call, err)
	return err
}

func (f *mutatingRegionFile) beforeMutation() (int, error) {
	f.calls++
	if f.calls == f.failAt {
		return f.calls, errInjectedRegionMutation
	}
	return f.calls, nil
}

func (f *mutatingRegionFile) crashAfterMutation(call int, err error) {
	if err == nil && call == f.crashAfter {
		os.Exit(regionCrashExitCode)
	}
}

func TestRegionCommitFailureAlwaysReopensOldOrNew(t *testing.T) {
	mutationPoints := countRegionCommitMutationPoints(t)
	for failAt := 1; failAt <= mutationPoints; failAt++ {
		t.Run(strconv.Itoa(failAt), func(t *testing.T) {
			path, key, chunkKey, oldHash, newHash := seededRegion(t)
			r, fault := openRegionWithMutationHooks(t, path, key, failAt, 0)
			_, err := r.save(context.Background(), []ChunkSave{changedSave(chunkKey, 2)})
			if !errors.Is(err, errInjectedRegionMutation) {
				t.Fatalf("save at mutation %d error = %v, want injected failure", failAt, err)
			}
			if fault.calls != failAt {
				t.Fatalf("mutating calls = %d, want failure at %d", fault.calls, failAt)
			}
			_ = r.file.Close() // Simulate process loss: do not sync or call region.close.

			assertRegionReopensOldOrNew(t, path, key, chunkKey, oldHash, newHash)
		})
	}
}

func TestRegionCrashSubprocessAlwaysReopensOldOrNew(t *testing.T) {
	mutationPoints := countRegionCommitMutationPoints(t)
	for crashAfter := 1; crashAfter <= mutationPoints; crashAfter++ {
		t.Run(strconv.Itoa(crashAfter), func(t *testing.T) {
			path, key, chunkKey, oldHash, newHash := seededRegion(t)
			ctx, cancel := context.WithTimeout(context.Background(), regionCrashHelperTimeout)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRegionCrashHelper$")
			command.Env = append(os.Environ(),
				"MORNLEA_REGION_CRASH_AFTER="+strconv.Itoa(crashAfter),
				"MORNLEA_REGION_CRASH_PATH="+path,
			)
			output, err := command.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("crash helper timed out after %s: %v, output=%s", regionCrashHelperTimeout, ctx.Err(), output)
			}
			var exitError *exec.ExitError
			if !errors.As(err, &exitError) || exitError.ExitCode() != regionCrashExitCode {
				t.Fatalf("crash helper error = %v, output=%s", err, output)
			}

			assertRegionReopensOldOrNew(t, path, key, chunkKey, oldHash, newHash)
		})
	}
}

func TestRegionCrashHelper(t *testing.T) {
	rawCrashAfter := os.Getenv("MORNLEA_REGION_CRASH_AFTER")
	if rawCrashAfter == "" {
		t.Skip("subprocess crash helper")
	}
	crashAfter, err := strconv.Atoi(rawCrashAfter)
	if err != nil || crashAfter < 1 {
		t.Fatalf("invalid MORNLEA_REGION_CRASH_AFTER %q", rawCrashAfter)
	}
	path := os.Getenv("MORNLEA_REGION_CRASH_PATH")
	if path == "" {
		t.Fatal("MORNLEA_REGION_CRASH_PATH is empty")
	}
	key, chunkKey := crashRegionKeys()
	r, _ := openRegionWithMutationHooks(t, path, key, 0, crashAfter)
	if _, err := r.save(context.Background(), []ChunkSave{changedSave(chunkKey, 2)}); err != nil {
		t.Fatalf("save before crash point %d: %v", crashAfter, err)
	}
	t.Fatalf("save returned without crashing after mutation %d", crashAfter)
}

func countRegionCommitMutationPoints(t *testing.T) int {
	t.Helper()
	path, key, chunkKey, _, _ := seededRegion(t)
	r, tracked := openRegionWithMutationHooks(t, path, key, 0, 0)
	if _, err := r.save(context.Background(), []ChunkSave{changedSave(chunkKey, 2)}); err != nil {
		t.Fatal(err)
	}
	if err := r.close(); err != nil {
		t.Fatal(err)
	}
	if tracked.calls != regionCommitMutationPoints {
		t.Fatalf(
			"region commit mutating calls = %d, want %d",
			tracked.calls, regionCommitMutationPoints,
		)
	}
	return tracked.calls
}

func openRegionWithMutationHooks(
	t *testing.T,
	path string,
	key RegionKey,
	failAt int,
	crashAfter int,
) (*region, *mutatingRegionFile) {
	t.Helper()
	var wrapped *mutatingRegionFile
	hooks := regionFileHooks{
		Open: func(name string, flag int, mode fs.FileMode) (regionFile, error) {
			file, err := os.OpenFile(name, flag, mode)
			if err != nil {
				return nil, err
			}
			wrapped = &mutatingRegionFile{
				regionFile: file,
				failAt:     failAt,
				crashAfter: crashAfter,
			}
			return wrapped, nil
		},
	}
	r, err := openRegionWithHooks(context.Background(), path, key, hooks)
	if err != nil {
		t.Fatal(err)
	}
	if wrapped == nil {
		t.Fatal("region hook did not wrap the opened file")
	}
	return r, wrapped
}

func seededRegion(t *testing.T) (string, RegionKey, core.ChunkKey, [32]byte, [32]byte) {
	t.Helper()
	key, chunkKey := crashRegionKeys()
	path := filepath.Join(t.TempDir(), "r.0.0.region")
	r, err := createRegion(context.Background(), path, key)
	if err != nil {
		t.Fatal(err)
	}
	old := changedSave(chunkKey, 1)
	if _, err := r.save(context.Background(), []ChunkSave{old}); err != nil {
		t.Fatal(err)
	}
	if err := r.close(); err != nil {
		t.Fatal(err)
	}
	newSave := changedSave(chunkKey, 2)
	return path, key, chunkKey, old.Chunk.Hash(), newSave.Chunk.Hash()
}

func crashRegionKeys() (RegionKey, core.ChunkKey) {
	key := RegionKey{Dimension: core.Overworld, X: 0, Z: 0}
	chunkKey := core.ChunkKey{
		Dimension: core.Overworld,
		Pos:       core.ChunkPos{X: 1, Z: 1},
	}
	return key, chunkKey
}

func changedSave(key core.ChunkKey, revision uint64) ChunkSave {
	chunk := world.NewChunk(key.Pos)
	block := core.StoneID
	if revision > 1 {
		block = core.DirtID
	}
	chunk.SetBlock(1, 0, 1, block)
	return ChunkSave{Key: key, Revision: revision, Chunk: chunk}
}

func assertRegionReopensOldOrNew(
	t *testing.T,
	path string,
	key RegionKey,
	chunkKey core.ChunkKey,
	oldHash [32]byte,
	newHash [32]byte,
) {
	t.Helper()
	reopened, err := openRegion(context.Background(), path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()
	got, err := reopened.load(context.Background(), chunkKey)
	if err != nil {
		t.Fatal(err)
	}
	hash := got.Chunk.Hash()
	if hash != oldHash && hash != newHash {
		t.Fatalf("mixed commit hash=%x", hash)
	}
	if hash == oldHash && got.PersistedRevision != 1 {
		t.Fatalf("old hash persisted revision = %d, want 1", got.PersistedRevision)
	}
	if hash == newHash && got.PersistedRevision != 2 {
		t.Fatalf("new hash persisted revision = %d, want 2", got.PersistedRevision)
	}
}
