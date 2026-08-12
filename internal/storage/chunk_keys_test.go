package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"minecraft-go/internal/core"
)

func TestChunkKeysEnumeratesPersistedChunksInOrder(t *testing.T) {
	root := t.TempDir()
	store, err := OpenDisk(context.Background(), root, OpenOptions{
		Create: Metadata{FormatVersion: 2, Seed: 42},
	})
	if err != nil {
		t.Fatal(err)
	}
	saved := []core.ChunkKey{
		{Dimension: 0, Pos: core.ChunkPos{X: -33, Z: 32}},
		{Dimension: 0, Pos: core.ChunkPos{X: 0, Z: 0}},
		{Dimension: 1, Pos: core.ChunkPos{X: 5, Z: -2}},
		{Dimension: 0, Pos: core.ChunkPos{X: 1, Z: 0}},
		{Dimension: 0, Pos: core.ChunkPos{X: 0, Z: 1}},
	}
	if _, err := store.SaveBatch(context.Background(), diskSavesFor(saved, 7)); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenDisk(context.Background(), root, OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.ChunkKeys(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []core.ChunkKey{
		{Dimension: 0, Pos: core.ChunkPos{X: -33, Z: 32}},
		{Dimension: 0, Pos: core.ChunkPos{X: 0, Z: 0}},
		{Dimension: 0, Pos: core.ChunkPos{X: 0, Z: 1}},
		{Dimension: 0, Pos: core.ChunkPos{X: 1, Z: 0}},
		{Dimension: 1, Pos: core.ChunkPos{X: 5, Z: -2}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ChunkKeys() = %v，期望 %v", got, want)
	}
	if len(reopened.regions) != 0 {
		t.Fatalf("ChunkKeys 缓存了 %d 个 region", len(reopened.regions))
	}
	for _, key := range saved {
		stored, err := reopened.LoadChunk(context.Background(), key)
		if err != nil {
			t.Fatal(err)
		}
		if stored.Revision != 7 || stored.PersistedRevision != 7 {
			t.Fatalf("枚举后区块 %v revision=(%d,%d)，期望 (7,7)", key, stored.Revision, stored.PersistedRevision)
		}
	}
}

func TestChunkKeysEmptyWorldDoesNotCreateDimensions(t *testing.T) {
	root := t.TempDir()
	store, err := OpenDisk(context.Background(), root, OpenOptions{
		Create: Metadata{FormatVersion: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	keys, err := store.ChunkKeys(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 0 {
		t.Fatalf("空世界 ChunkKeys() = %v", keys)
	}
	if _, err := os.Stat(filepath.Join(root, "dimensions")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("枚举空世界创建了 dimensions：%v", err)
	}
}

func TestChunkKeysIgnoresNonCanonicalAliasesAndUnrelatedFiles(t *testing.T) {
	root := t.TempDir()
	store, err := OpenDisk(context.Background(), root, OpenOptions{
		Create: Metadata{FormatVersion: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	key := core.ChunkKey{Dimension: 0, Pos: core.ChunkPos{X: 0, Z: 0}}
	if _, err := store.SaveBatch(context.Background(), diskSavesFor([]core.ChunkKey{key}, 1)); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	canonical := filepath.Join(root, "dimensions", "0", "regions", "r.0.0.region")
	encoded, err := os.ReadFile(canonical)
	if err != nil {
		t.Fatal(err)
	}
	for _, alias := range []string{
		filepath.Join(root, "dimensions", "+0", "regions", "r.0.0.region"),
		filepath.Join(root, "dimensions", "00", "regions", "r.0.0.region"),
		filepath.Join(root, "dimensions", "0", "regions", "r.+0.0.region"),
		filepath.Join(root, "dimensions", "0", "regions", "r.00.0.region"),
		filepath.Join(root, "dimensions", "0", "regions", "r.0.+0.region"),
		filepath.Join(root, "dimensions", "0", "regions", "r.0.00.region"),
	} {
		if err := os.MkdirAll(filepath.Dir(alias), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(alias, encoded, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, unrelated := range []string{
		filepath.Join(root, "dimensions", "说明.txt"),
		filepath.Join(root, "dimensions", "0", "regions", "README"),
		filepath.Join(root, "dimensions", "0", "regions", "r.x.0.region"),
		filepath.Join(root, "dimensions", "0", "regions", "r.0.0.region.bak"),
	} {
		if err := os.WriteFile(unrelated, []byte("不是 region"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	reopened, err := OpenDisk(context.Background(), root, OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got, err := reopened.ChunkKeys(context.Background()); err != nil {
		t.Fatal(err)
	} else if want := []core.ChunkKey{key}; !reflect.DeepEqual(got, want) {
		t.Fatalf("包含 alias 的 ChunkKeys() = %v，期望 %v", got, want)
	}
}

func TestChunkKeysRejectsCorruptRegion(t *testing.T) {
	root := t.TempDir()
	store, err := OpenDisk(context.Background(), root, OpenOptions{
		Create: Metadata{FormatVersion: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	key := core.ChunkKey{Dimension: 0, Pos: core.ChunkPos{X: -33, Z: 32}}
	if _, err := store.SaveBatch(context.Background(), diskSavesFor([]core.ChunkKey{key}, 1)); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "dimensions", "0", "regions", "r.-2.1.region")
	if err := os.WriteFile(path, []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenDisk(context.Background(), root, OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := reopened.ChunkKeys(context.Background()); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("损坏 region 的 ChunkKeys error = %v，期望 %v", err, ErrCorrupt)
	}
}

func TestChunkKeysReturnsCanceledContext(t *testing.T) {
	store := openTestDiskStore(t)
	defer store.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.ChunkKeys(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("已取消 ChunkKeys error = %v，期望 %v", err, context.Canceled)
	}
}
