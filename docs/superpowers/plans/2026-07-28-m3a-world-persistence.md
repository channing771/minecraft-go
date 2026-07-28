# M3A World Persistence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist every explored full chunk and later block change in crash-safe, versioned 32×32 region files while keeping disk work outside the authoritative tick and preserving headless tests and benchmarks.

**Architecture:** Add a `storage` package that owns deterministic chunk/metadata codecs, memory and disk stores, dual-bank COW region files, migration, locking, recovery, and compaction. Replace `sim`'s temporary overlay persistence with revision-based dirty/in-flight/unload state; `server` performs load-before-generate acquisition, budgeted snapshots, asynchronous saves, retry/backpressure, and retryable shutdown. Interactive `mcgo` opens `worlds/default`; tests and the offscreen benchmark inject the memory store.

**Tech Stack:** Go 1.26 through the user's GVM installation, existing `core`/`world` paletted snapshots, `github.com/klauspost/compress/zstd` v1.19.1, `github.com/gofrs/flock` v0.13.0, CRC32C, explicit little-endian binary encoding, deterministic 20 TPS orchestration, property/fuzz/subprocess crash tests, race detector, headless WebGPU/Metal.

## Global Constraints

- Use the GVM-managed Go 1.26.0 toolchain through `zsh -ic 'gvm use go1.26.0 >/dev/null && ...'`; never install or download another Go distribution.
- Use `github.com/klauspost/compress` exactly at `v1.19.1` and `github.com/gofrs/flock` exactly at `v0.13.0`; do not add a database or reflection-based serializer.
- `internal/storage` may import only `internal/core` and `internal/world`; it never imports `server`, `sim`, `network`, `client`, `render`, or `gfx`.
- Storage messages and files own their slices and chunks; never share mutable `world.Chunk`, palette, or packed-word storage across authority/store boundaries.
- Every generated chunk begins `current_revision=1`, `persisted_revision=0`; dirty means `current > persisted || needsRewrite`.
- A load error triggers generation only when `errors.Is(err, storage.ErrChunkNotFound)`; corruption, permissions, cancellation, and future versions never regenerate silently.
- The region geometry is exactly 32×32 chunks; sector size is 4096 bytes; sector 0 is the superblock, sectors 1–7 bank A, sectors 8–14 bank B, and payloads start at sector 15.
- Region entries are 24 bytes and contain `offset_sector uint32`, `sector_count uint32`, `payload_length uint32`, `chunk_revision uint64`, and `payload_crc32c uint32`.
- Compressed chunk payloads are at most 1 MiB and decoded logical payloads at most 2 MiB; all lengths are checked before allocation.
- Autosave is exactly 6000 ticks (5 minutes at 20 TPS); retry delays are 20, 40, 80… ticks capped at 1200 ticks; unsaved memory defaults to 512 MiB.
- World saves are atomic per region-bank commit, not globally atomic across regions.
- `cmd/mcgo` defaults to `worlds/default`; benchmarks and tests use `storage.NewMemory` and never touch that directory.
- M3A does not add player persistence, UUIDs, TCP, login, multiplayer, entities, lighting, cloud sync, save-selection UI, or manual backup management.
- Interactive or benchmark verification must never bring a window to the foreground. The real performance command may only use the existing `--benchmark` headless/offscreen path.
- Every implementation task follows red → green → refactor, runs its focused package with `-race -count=1`, receives review, and creates exactly one isolated commit before the next task.
- Final gates remain ≥100 FPS, frame p99 `<12 ms`, RSS `<2 GiB`, server tick p99 `<10 ms`, tick max `<50 ms`, and physics `0 allocs/op`.

## File and Responsibility Map

| Path | Responsibility after M3A |
|---|---|
| `internal/storage/types.go` | Stable store contract, metadata/chunk values, results, rewrite reasons, classified errors |
| `internal/storage/coords.go` | Negative-safe chunk→region/slot mapping and deterministic key ordering |
| `internal/storage/memory.go` | Deep-copy in-memory Store with the same monotonic revision semantics as disk |
| `internal/storage/chunk_codec.go` | Explicit v1 chunk DTO, little-endian encoding, bounded zstd envelope |
| `internal/storage/migration.go` | Pure, continuous chunk-schema migration registry |
| `internal/storage/metadata.go` | `world.meta` codec, CRC32C, atomic temp+sync+rename |
| `internal/storage/world_files.go` | World directory creation, exclusive lock, metadata authority, directory sync |
| `internal/storage/region_format.go` | Superblock/index-bank binary format and structural validation |
| `internal/storage/region.go` | Region open, dual-bank selection, COW load/save commit and recovery |
| `internal/storage/region_space.go` | Free-sector allocation, orphan reclamation and compaction |
| `internal/storage/disk.go` | Multi-region DiskStore, region cache, grouping, Sync and Close |
| `internal/sim/world.go` | Loading/generating/ready/failed/unloading lifecycle and persistence fields |
| `internal/sim/persistence.go` | Budgeted immutable save snapshots, confirmations, failures and stats |
| `internal/sim/engine.go` | Acquisition result ordering, dirty state, unload cancellation and tick integration |
| `internal/server/acquire.go` | Storage-first load work and generation only after typed miss |
| `internal/server/persistence.go` | Region-grouped save workers, autosave, retry, backpressure and status |
| `internal/server/shutdown.go` | Frozen retryable `Shutdown(ctx)` and resource close ordering |
| `internal/server/config.go` | Save/load worker counts, budgets, autosave/retry/cap/shutdown defaults |
| `cmd/mcgo/app.go` | Store injection, persistent interactive assembly, error-returning cleanup |
| `cmd/mcgo/main.go` | `--world`, default path, benchmark-memory selection and nonzero save failure exit |
| `internal/server/persistence_integration_test.go` | Real restart, generator-upgrade, corruption, retry and mirror consistency proof |
| `docs/notes/perf-baseline.*` | Scenario v4 headless results and storage microbenchmark baseline |

---

### Task 1: Define Storage Values, Region Coordinates, and the Memory Store

**Files:**
- Create: `internal/storage/types.go`
- Create: `internal/storage/coords.go`
- Create: `internal/storage/coords_test.go`
- Create: `internal/storage/memory.go`
- Create: `internal/storage/memory_test.go`
- Modify: `internal/archcheck/deps_test.go`

**Interfaces:**
- Consumes: `core.ChunkKey`, `core.DimensionID`, `world.Chunk`, `world.Chunk.Hash`, `world.Chunk.Clone`
- Produces:

```go
var (
	ErrChunkNotFound   = errors.New("storage: chunk not found")
	ErrWorldLocked     = errors.New("storage: world locked")
	ErrCorrupt         = errors.New("storage: corrupt data")
	ErrFutureVersion   = errors.New("storage: future version")
	ErrRevisionConflict = errors.New("storage: revision conflict")
)

type Metadata struct {
	FormatVersion  uint32
	Seed           int64
	SpawnDimension core.DimensionID
	SpawnAnchor    core.ChunkPos
}

type StoredChunk struct {
	Key               core.ChunkKey
	Revision          uint64
	PersistedRevision uint64
	NeedsRewrite      bool
	Recovered         bool
	Chunk             *world.Chunk
}

type ChunkSave struct {
	Key      core.ChunkKey
	Revision uint64
	Chunk    *world.Chunk
}

type SaveResult struct {
	Committed map[core.ChunkKey]uint64
}

type Store interface {
	Metadata() Metadata
	LoadChunk(context.Context, core.ChunkKey) (StoredChunk, error)
	SaveBatch(context.Context, []ChunkSave) (SaveResult, error)
	Sync(context.Context) error
	Close() error
}

type RegionKey struct {
	Dimension core.DimensionID
	X, Z      int32
}

func RegionFor(core.ChunkKey) (RegionKey, int)
func NewMemory(Metadata) *MemoryStore
```

- [ ] **Step 1: Write failing coordinate and ownership tests**

Create `internal/storage/coords_test.go`:

```go
package storage_test

func TestRegionForUsesFloorDivision(t *testing.T) {
	tests := []struct {
		chunk, region, local int32
	}{
		{-33, -2, 31}, {-32, -1, 0}, {-31, -1, 1}, {-1, -1, 31},
		{0, 0, 0}, {1, 0, 1}, {31, 0, 31}, {32, 1, 0}, {33, 1, 1},
	}
	for _, tc := range tests {
		region, slot := storage.RegionFor(core.ChunkKey{
			Dimension: core.Overworld,
			Pos:       core.ChunkPos{X: tc.chunk, Z: tc.chunk},
		})
		if region.X != tc.region || region.Z != tc.region ||
			slot != int(tc.local*32+tc.local) {
			t.Fatalf("chunk=%d -> region=%+v slot=%d", tc.chunk, region, slot)
		}
	}
}
```

Create `internal/storage/memory_test.go` with these exact assertions:

```go
func TestMemoryStoreOwnsSavedAndLoadedChunks(t *testing.T) {
	store := storage.NewMemory(storage.Metadata{FormatVersion: 1, Seed: 42})
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -1, Z: 32}}
	chunk := world.NewChunk(key.Pos)
	chunk.SetBlock(1, 0, 2, core.StoneID)

	result, err := store.SaveBatch(context.Background(), []storage.ChunkSave{{
		Key: key, Revision: 1, Chunk: chunk,
	}})
	if err != nil || result.Committed[key] != 1 {
		t.Fatalf("SaveBatch = %+v, %v", result, err)
	}
	chunk.SetBlock(1, 0, 2, core.DirtID)

	loaded, err := store.LoadChunk(context.Background(), key)
	if err != nil || loaded.Chunk.BlockAt(1, 0, 2) != core.StoneID {
		t.Fatalf("LoadChunk = %+v, %v", loaded, err)
	}
	loaded.Chunk.SetBlock(1, 0, 2, core.GrassID)
	again, _ := store.LoadChunk(context.Background(), key)
	if again.Chunk.BlockAt(1, 0, 2) != core.StoneID {
		t.Fatal("loaded chunk aliases store state")
	}
}
```

Also assert: missing keys wrap `ErrChunkNotFound`; lower revisions do not replace higher data; equal revision/same hash is idempotent; equal revision/different hash returns `ErrRevisionConflict`; canceled contexts do not mutate the store; `Close` is idempotent.

- [ ] **Step 2: Run the new package and verify it does not exist**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage -race -count=1'
```

Expected: FAIL because `internal/storage` and its types do not exist.

- [ ] **Step 3: Implement exact values, floor mapping, and deep-copy memory semantics**

Use negative-safe division, not `%` on a negative dividend:

```go
func floorDiv32(value int32) int32 {
	if value >= 0 {
		return value / 32
	}
	return -((-value + 31) / 32)
}

func RegionFor(key core.ChunkKey) (RegionKey, int) {
	rx, rz := floorDiv32(key.Pos.X), floorDiv32(key.Pos.Z)
	lx, lz := key.Pos.X-rx*32, key.Pos.Z-rz*32
	return RegionKey{Dimension: key.Dimension, X: rx, Z: rz}, int(lz*32 + lx)
}
```

`MemoryStore.SaveBatch` first validates every non-nil chunk, matching `Chunk.Pos`, nonzero revision, and revision/content rule under one mutex; only then clone and apply the batch. `LoadChunk` returns a fresh clone with `Revision == PersistedRevision`, `NeedsRewrite=false`, and `Recovered=false`.

Register `internal/storage` in `internal/archcheck/deps_test.go` with only `internal/core` and `internal/world` allowed.

- [ ] **Step 4: Run focused tests and architecture checks**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage ./internal/archcheck -race -count=1'
```

Expected: PASS, including all nine negative-coordinate boundaries and ownership tests.

- [ ] **Step 5: Commit**

```bash
git add internal/storage internal/archcheck/deps_test.go
git commit -m "feat: 定义世界存储契约与内存实现"
```

---

### Task 2: Add the Deterministic v1 Chunk Codec and Bounded zstd Envelope

**Files:**
- Create: `internal/storage/chunk_codec.go`
- Create: `internal/storage/chunk_codec_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**
- Consumes: Task 1 `ChunkSave`, `StoredChunk`; `world.ContainerSnapshot`, `world.NewPalettedContainerFromSnapshot`
- Produces:

```go
const currentChunkSchema uint32 = 1
const maxCompressedChunk = 1 << 20
const maxDecodedChunk = 2 << 20

type decodedPayload struct {
	Key       core.ChunkKey
	Revision  uint64
	Schema    uint32
	Chunk     *world.Chunk
}

func encodeChunkPayload(ChunkSave) ([]byte, error)
func decodeChunkPayload(core.ChunkKey, uint64, []byte) (decodedPayload, error)
```

- [ ] **Step 1: Write failing deterministic roundtrip and limit tests**

Create `internal/storage/chunk_codec_test.go` in package `storage` so it can test the unexported disk codec:

```go
func TestChunkPayloadRoundTripsDeterministically(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	chunk := codecFixtureChunk(key.Pos) // one Single, one 4-bit, one 8-bit, one Direct section
	save := ChunkSave{Key: key, Revision: 19, Chunk: chunk}

	one, err := encodeChunkPayload(save)
	if err != nil { t.Fatal(err) }
	two, err := encodeChunkPayload(save)
	if err != nil { t.Fatal(err) }
	if !bytes.Equal(one, two) { t.Fatal("same chunk encoded differently") }

	got, err := decodeChunkPayload(key, 19, one)
	if err != nil { t.Fatal(err) }
	if got.Key != key || got.Revision != 19 || got.Chunk.Hash() != chunk.Hash() {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
}
```

Add table cases for nil chunk, zero revision, key/`Chunk.Pos` mismatch, wrong requested key/revision, unknown compression ID, compressed length over 1 MiB, decoded length over 2 MiB, truncated sections, invalid palette lengths, and a direct word with high unused bits.

- [ ] **Step 2: Run the codec tests and verify symbols are missing**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage -run "TestChunkPayload" -v'
```

Expected: FAIL because `encodeChunkPayload` and `decodeChunkPayload` do not exist.

- [ ] **Step 3: Pin zstd and implement explicit v1 bytes**

Pin the dependency without changing the Go toolchain:

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go get github.com/klauspost/compress@v1.19.1'
```

Use manual append/read helpers; never `binary.Write` a Go struct:

```go
func appendU32(dst []byte, value uint32) []byte {
	return binary.LittleEndian.AppendUint32(dst, value)
}

type byteDecoder struct { data []byte; offset int }

func (d *byteDecoder) u32() (uint32, error) {
	if len(d.data)-d.offset < 4 { return 0, io.ErrUnexpectedEOF }
	value := binary.LittleEndian.Uint32(d.data[d.offset:])
	d.offset += 4
	return value, nil
}
```

Encode the logical record in this exact order: `MCGC`, schema, dimension, X, Z, revision, section count, then each section index, kind, bits, single, palette length/IDs, packed length/words. Require exactly 24 ascending section indexes and no trailing bytes.

Wrap it as `CHNK`, envelope version 1, schema, key, revision, compression ID 1, decoded length, compressed length, then zstd bytes. Construct zstd with one encoder/decoder worker and bounded decoder memory:

```go
encoder, err := zstd.NewWriter(nil,
	zstd.WithEncoderConcurrency(1),
	zstd.WithEncoderCRC(true),
)
decoder, err := zstd.NewReader(nil,
	zstd.WithDecoderConcurrency(1),
	zstd.WithDecoderMaxMemory(maxDecodedChunk),
)
```

Reject all declared lengths before slicing or allocating. Convert decoded snapshots only through `world.NewPalettedContainerFromSnapshot`.

- [ ] **Step 4: Run codec, world snapshot, and race tests**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage ./internal/world -race -count=1'
```

Expected: PASS; deterministic bytes are stable and malformed data returns errors without panic.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/storage/chunk_codec.go internal/storage/chunk_codec_test.go
git commit -m "feat: 编码有界版本化区块存档"
```

---

### Task 3: Freeze v1 Fixtures, Migration Continuity, and Hostile-Byte Fuzzing

**Files:**
- Create: `internal/storage/migration.go`
- Create: `internal/storage/migration_test.go`
- Create: `internal/storage/chunk_codec_fuzz_test.go`
- Create: `internal/storage/testdata/chunk-v1.bin`
- Modify: `internal/storage/chunk_codec.go`
- Modify: `internal/storage/chunk_codec_test.go`

**Interfaces:**
- Consumes: Task 2 `decodedPayload`, `currentChunkSchema`, v1 logical decoder
- Produces:

```go
type chunkDTO struct {
	Key      core.ChunkKey
	Revision uint64
	Sections [core.SectionsPerChunk]world.ContainerSnapshot
}

type chunkMigration func(chunkDTO) (chunkDTO, error)

var chunkMigrations = map[uint32]chunkMigration{}

func migrateChunk(from uint32, dto chunkDTO) (chunkDTO, bool, error)
```

- [ ] **Step 1: Write failing continuity, fixture, future-version, and fuzz tests**

Add a fixture gate with an explicit update flag:

```go
var updateStorageFixtures = flag.Bool(
	"update-storage-fixtures", false, "rewrite committed storage fixtures",
)

func TestChunkV1Fixture(t *testing.T) {
	want := codecFixtureChunk(core.ChunkPos{X: -3, Z: 7})
	encoded, err := encodeChunkPayload(ChunkSave{
		Key: core.ChunkKey{Dimension: core.Overworld, Pos: want.Pos},
		Revision: 19,
		Chunk: want,
	})
	if err != nil { t.Fatal(err) }
	path := filepath.Join("testdata", "chunk-v1.bin")
	if *updateStorageFixtures {
		if err := os.WriteFile(path, encoded, 0o644); err != nil { t.Fatal(err) }
	}
	got, err := os.ReadFile(path)
	if err != nil { t.Fatal(err) }
	if !bytes.Equal(got, encoded) { t.Fatal("v1 fixture drift; change schema version") }
}
```

`TestMigrationRegistryIsContinuous` must walk every integer schema from the oldest supported value through `currentChunkSchema-1`, rejecting missing or extra registrations. `TestFutureSchemaIsRejectedWithoutMutation` changes only the encoded schema field and asserts `errors.Is(err, ErrFutureVersion)`.

Seed `FuzzDecodeChunkPayload` with the committed v1 bytes, every prefix length, and payloads whose compressed/decoded lengths are `limit-1`, `limit`, and `limit+1`. The fuzz function only asserts bounded return, no panic, and exact key/revision on success.

- [ ] **Step 2: Run tests and verify the migration/fixture gates fail**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage -run "TestMigration|TestChunkV1Fixture|TestFuture" -v'
```

Expected: FAIL because the migration registry and fixture do not exist.

- [ ] **Step 3: Separate DTO decode from migration and generate the fixed v1 fixture**

Decode bytes into `chunkDTO`, validate v1, then call:

```go
func migrateChunk(from uint32, dto chunkDTO) (chunkDTO, bool, error) {
	if from > currentChunkSchema {
		return chunkDTO{}, false, fmt.Errorf("%w: chunk schema %d", ErrFutureVersion, from)
	}
	migrated := false
	for version := from; version < currentChunkSchema; version++ {
		migration, ok := chunkMigrations[version]
		if !ok { return chunkDTO{}, false, fmt.Errorf("storage: missing migration %d", version) }
		var err error
		dto, err = migration(cloneChunkDTO(dto))
		if err != nil { return chunkDTO{}, false, fmt.Errorf("migrate %d: %w", version, err) }
		migrated = true
	}
	return dto, migrated, nil
}
```

For M3A, oldest and current are both v1, so `chunkMigrations` is intentionally empty and the continuity test passes. Generate and immediately verify the binary fixture:

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage -run TestChunkV1Fixture -update-storage-fixtures -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage -run TestChunkV1Fixture -count=1'
```

- [ ] **Step 4: Run fuzz smoke and race tests**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage -run "TestMigration|TestChunkV1Fixture|TestFuture" -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage -run "^$" -fuzz FuzzDecodeChunkPayload -fuzztime=5s'
```

Expected: PASS; fuzzing reports no panic, excessive allocation, or successful key/revision mismatch.

- [ ] **Step 5: Commit**

```bash
git add internal/storage/migration.go internal/storage/migration_test.go internal/storage/chunk_codec.go internal/storage/chunk_codec_test.go internal/storage/chunk_codec_fuzz_test.go internal/storage/testdata/chunk-v1.bin
git commit -m "test: 冻结区块格式迁移与恶意输入边界"
```

---

### Task 4: Add Atomic World Metadata and Exclusive Directory Locking

**Files:**
- Create: `internal/storage/metadata.go`
- Create: `internal/storage/metadata_test.go`
- Create: `internal/storage/world_files.go`
- Create: `internal/storage/world_files_test.go`
- Modify: `go.mod`
- Modify: `go.sum`
- Modify: `.gitignore`

**Interfaces:**
- Consumes: Task 1 `Metadata`, `ErrWorldLocked`, `ErrCorrupt`, `ErrFutureVersion`
- Produces:

```go
const currentMetadataVersion uint32 = 1

type OpenOptions struct {
	Create Metadata
}

type worldFiles struct {
	root     string
	metadata Metadata
	lock     *flock.Flock
}

func openWorldFiles(context.Context, string, OpenOptions) (*worldFiles, error)
func (files *worldFiles) close() error
func encodeMetadata(Metadata) ([]byte, error)
func decodeMetadata([]byte) (Metadata, error)
func replaceFileAtomically(string, []byte, fs.FileMode) error
```

- [ ] **Step 1: Write failing metadata, authority, atomicity, and lock tests**

```go
func TestExistingMetadataOverridesCreateOptions(t *testing.T) {
	root := t.TempDir()
	first, err := openWorldFiles(context.Background(), root, OpenOptions{
		Create: Metadata{FormatVersion: 1, Seed: 42, SpawnDimension: core.Overworld,
			SpawnAnchor: core.ChunkPos{X: 3, Z: -2}},
	})
	if err != nil { t.Fatal(err) }
	if err := first.close(); err != nil { t.Fatal(err) }

	second, err := openWorldFiles(context.Background(), root, OpenOptions{
		Create: Metadata{FormatVersion: 1, Seed: 999},
	})
	if err != nil { t.Fatal(err) }
	defer second.close()
	if second.metadata.Seed != 42 || second.metadata.SpawnAnchor != (core.ChunkPos{X: 3, Z: -2}) {
		t.Fatalf("existing metadata not authoritative: %+v", second.metadata)
	}
}
```

Also test: the second concurrent opener gets `ErrWorldLocked` immediately; closing releases the lock; CRC corruption and future version fail without rewriting; canceled create leaves no `world.meta`; the metadata bytes are deterministic; a failure before rename preserves the previous file using an injected replace hook.

- [ ] **Step 2: Run the focused tests and verify open/codec symbols are missing**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage -run "TestExistingMetadata|TestWorldLock|TestMetadata" -v'
```

Expected: FAIL because metadata and world-file functions do not exist.

- [ ] **Step 3: Pin the lock dependency and implement durable metadata replacement**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go get github.com/gofrs/flock@v0.13.0'
```

Encode `MCGM`, version, payload length, seed, spawn dimension/X/Z, and trailing CRC32C in little endian. Reject non-v1 future versions and any trailing/short bytes.

Acquire the lock before reading/creating metadata:

```go
guard := flock.New(filepath.Join(root, "world.lock"))
locked, err := guard.TryLock()
if err != nil { return nil, fmt.Errorf("lock world %q: %w", root, err) }
if !locked { return nil, fmt.Errorf("%w: %s", ErrWorldLocked, root) }
```

`replaceFileAtomically` must use `os.CreateTemp(parent, ".world.meta.tmp-*")`, chmod, full-write loop, `Sync`, `Close`, `Rename`, then open and `Sync` the parent directory. On every failure close/remove only the exact temp path.

Add `/worlds/` to `.gitignore`; retain the existing `/saves/` entry for compatibility.

- [ ] **Step 4: Run metadata, lock, and full storage race tests**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage -race -count=1'
```

Expected: PASS; two openers cannot write one world and reopen after close succeeds.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum .gitignore internal/storage/metadata.go internal/storage/metadata_test.go internal/storage/world_files.go internal/storage/world_files_test.go
git commit -m "feat: 原子创建并独占打开世界目录"
```

---

### Task 5: Encode and Validate the Fixed Region Superblock and Index Banks

**Files:**
- Create: `internal/storage/region_format.go`
- Create: `internal/storage/region_format_test.go`

**Interfaces:**
- Consumes: Task 1 `RegionKey`, Task 2 payload limits
- Produces:

```go
const (
	sectorSize        = 4096
	bankSectors       = 7
	dataStartSector   = 15
	regionSlots       = 32 * 32
	currentRegionVersion uint32 = 1
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

func encodeSuperblock(RegionKey) [sectorSize]byte
func decodeSuperblock(RegionKey, []byte) error
func encodeRegionBank(RegionKey, regionBank) ([bankSectors * sectorSize]byte, error)
func decodeRegionBank(RegionKey, []byte, int64) (regionBank, error)
func selectRegionBank(regionBank, error, regionBank, error) (regionBank, int, error)
```

- [ ] **Step 1: Write failing exact-layout and corruption tests**

```go
func TestRegionBankRoundTripAndSelection(t *testing.T) {
	key := RegionKey{Dimension: core.Overworld, X: -2, Z: 3}
	want := regionBank{Generation: 9}
	want.Entries[31] = regionEntry{
		OffsetSector: 15, SectorCount: 2, PayloadLength: 5000,
		Revision: 7, PayloadCRC32C: 0x12345678,
	}
	encoded, err := encodeRegionBank(key, want)
	if err != nil { t.Fatal(err) }
	got, err := decodeRegionBank(key, encoded[:], 17*sectorSize)
	if err != nil || got != want { t.Fatalf("decode = %+v, %v", got, err) }
	selected, bank, err := selectRegionBank(regionBank{Generation: 8}, nil, got, nil)
	if err != nil || bank != 1 || selected.Generation != 9 { t.Fatalf("select = %+v,%d,%v", selected, bank, err) }
}
```

Add cases for wrong magic/version/coordinates/sector size, invalid bank CRC, generation 0 with any non-empty entry, offset inside headers, zero sector count, payload >1 MiB, extent past EOF, uint32 overflow, overlapping extents, absent entry with nonzero tail fields, both banks invalid, one valid bank, and equal nonzero generation with different bytes. Generation 0 is valid only for the completely empty standby bank created with a new region.

- [ ] **Step 2: Run format tests and verify the codec is absent**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage -run "TestRegionBank|TestSuperblock" -v'
```

Expected: FAIL because region format types/functions do not exist.

- [ ] **Step 3: Implement fixed-size manual encoding and whole-bank validation**

Use exact offsets rather than unsafe struct conversion. A bank starts with a 64-byte header, then 1024 entries × 24 bytes, then zero padding to 28672 bytes. The last four header bytes hold CRC32C over the complete bank with that field zeroed.

Validate extents by sorting non-empty ranges:

```go
type sectorRange struct { first, end uint64 }
sort.Slice(ranges, func(i, j int) bool { return ranges[i].first < ranges[j].first })
for i, r := range ranges {
	if r.first < dataStartSector || r.end > uint64(fileSize/sectorSize) || r.first >= r.end {
		return regionBank{}, fmt.Errorf("%w: invalid extent", ErrCorrupt)
	}
	if i > 0 && ranges[i-1].end > r.first {
		return regionBank{}, fmt.Errorf("%w: overlapping extents", ErrCorrupt)
	}
}
```

When generations tie and are nonzero, accept only byte-identical banks; otherwise return `ErrCorrupt` rather than selecting nondeterministically. Reject `math.MaxUint64` only when preparing a subsequent commit, not while reading.

- [ ] **Step 4: Run storage format tests with race detection**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage -race -count=1'
```

Expected: PASS; corrupted headers are rejected before any payload read.

- [ ] **Step 5: Commit**

```bash
git add internal/storage/region_format.go internal/storage/region_format_test.go
git commit -m "feat: 定义双头区域文件格式"
```

---

### Task 6: Implement COW Region Creation, Load, and Save

**Files:**
- Create: `internal/storage/region.go`
- Create: `internal/storage/region_test.go`

**Interfaces:**
- Consumes: Tasks 2–3 `encodeChunkPayload`/`decodeChunkPayload`; Task 5 region format
- Produces:

```go
type region struct {
	mu         sync.RWMutex
	key        RegionKey
	path       string
	file       *os.File
	activeBank int
	bank       regionBank
}

func createRegion(context.Context, string, RegionKey) (*region, error)
func openRegion(context.Context, string, RegionKey) (*region, error)
func (r *region) load(context.Context, core.ChunkKey) (StoredChunk, error)
func (r *region) save(context.Context, []ChunkSave) (SaveResult, error)
func (r *region) sync(context.Context) error
func (r *region) close() error
```

- [ ] **Step 1: Write failing create/reopen/two-generation tests**

```go
func TestRegionSaveReopenAndAdvanceBank(t *testing.T) {
	ctx := context.Background()
	key := RegionKey{Dimension: core.Overworld, X: -1, Z: 2}
	path := filepath.Join(t.TempDir(), "r.-1.2.region")
	r, err := createRegion(ctx, path, key)
	if err != nil { t.Fatal(err) }

	chunkKey := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -1, Z: 64}}
	first := world.NewChunk(chunkKey.Pos)
	first.SetBlock(1, 0, 1, core.StoneID)
	if _, err := r.save(ctx, []ChunkSave{{Key: chunkKey, Revision: 1, Chunk: first}}); err != nil {
		t.Fatal(err)
	}
	if r.activeBank != 1 || r.bank.Generation != 2 { t.Fatalf("bank=%d generation=%d", r.activeBank, r.bank.Generation) }

	second := first.Clone()
	second.SetBlock(1, 0, 1, core.DirtID)
	if _, err := r.save(ctx, []ChunkSave{{Key: chunkKey, Revision: 2, Chunk: second}}); err != nil {
		t.Fatal(err)
	}
	if err := r.close(); err != nil { t.Fatal(err) }

	reopened, err := openRegion(ctx, path, key)
	if err != nil { t.Fatal(err) }
	defer reopened.close()
	got, err := reopened.load(ctx, chunkKey)
	if err != nil || got.Revision != 2 || got.Chunk.Hash() != second.Hash() {
		t.Fatalf("load = %+v, %v", got, err)
	}
}
```

Also test empty region miss, create writes two valid banks, lower-revision save is skipped and reported as committed at the disk revision, equal revision/same logical hash is idempotent, equal revision/different logical hash returns `ErrRevisionConflict`, canceled operations do not switch the in-memory bank, and key/region mismatch fails.

- [ ] **Step 2: Run region tests and verify the region object is missing**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage -run "TestRegionSave|TestRegionCreate|TestRegionLoad" -v'
```

Expected: FAIL because `region` create/open/load/save do not exist.

- [ ] **Step 3: Implement the first append-only COW commit path**

`createRegion` creates a unique same-directory `.<base>.create-*` file with superblock, bank A generation 1, bank B generation 0, and zero padding through sector 14. It syncs and closes the complete temp, atomically renames it to the canonical path, syncs the directory, then opens the canonical file. On failure it removes only its exact temp; a crash can therefore leave no region or a complete empty region, never a partial canonical header. `openRegion` reads the fixed header area, decodes both banks, and selects the highest valid generation.

Task 6 may allocate every new payload at aligned EOF; Task 8 adds reuse. The commit order must already be final:

```go
for _, save := range sortedSaves {
	payload, err := encodeChunkPayload(save)
	if err != nil { return result, err }
	offset := alignSector(fileSize)
	if err := writeFullAt(r.file, payload, offset); err != nil { return result, err }
	entry := regionEntry{
		OffsetSector: uint32(offset / sectorSize),
		SectorCount: uint32((len(payload)+sectorSize-1)/sectorSize),
		PayloadLength: uint32(len(payload)),
		Revision: save.Revision,
		PayloadCRC32C: crc32.Checksum(payload, crcTable),
	}
	next.Entries[slot] = entry
}
if err := r.file.Sync(); err != nil { return result, fmt.Errorf("sync payloads: %w", err) }
next.Generation = r.bank.Generation + 1
encoded, err := encodeRegionBank(r.key, next)
if err != nil { return result, err }
if err := writeFullAt(r.file, encoded[:], bankOffset(1-r.activeBank)); err != nil { return result, err }
if err := r.file.Sync(); err != nil { return result, fmt.Errorf("sync index bank: %w", err) }
r.bank, r.activeBank = next, 1-r.activeBank
```

Pad each allocated extent with zeros so a later larger stale tail cannot be interpreted. Hold the region write lock across allocation, both syncs, and active-bank switch. Loads hold the read lock through CRC, bounded decode, and clone ownership.

- [ ] **Step 4: Run all storage tests with race detection**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage -race -count=1'
```

Expected: PASS; reopen selects the latest fully synced bank and reads the latest complete chunk.

- [ ] **Step 5: Commit**

```bash
git add internal/storage/region.go internal/storage/region_test.go
git commit -m "feat: 原子提交区域区块版本"
```

---

### Task 7: Prove Every Region Crash Boundary and Recover from the Old Bank

**Files:**
- Create: `internal/storage/region_crash_test.go`
- Create: `internal/storage/region_recovery_test.go`
- Modify: `internal/storage/region.go`
- Modify: `internal/storage/types.go`

**Interfaces:**
- Consumes: Task 6 region commit sequence
- Produces:

```go
type regionFile interface {
	ReadAt([]byte, int64) (int, error)
	WriteAt([]byte, int64) (int, error)
	Stat() (os.FileInfo, error)
	Sync() error
	Truncate(int64) error
	Close() error
}

type regionFileHooks struct {
	Open func(string, int, fs.FileMode) (regionFile, error)
}

func (r *region) loadEntry(context.Context, core.ChunkKey, int, regionEntry) (decodedPayload, error)
```

- [ ] **Step 1: Write failing fault-point and recovery tests**

Wrap an actual `*os.File` and fail exactly the Nth mutating call:

```go
func TestRegionCommitFailureAlwaysReopensOldOrNew(t *testing.T) {
	mutationPoints := countRegionCommitMutationPoints(t)
	for failAt := 1; failAt <= mutationPoints; failAt++ {
		t.Run(strconv.Itoa(failAt), func(t *testing.T) {
			path, key, chunkKey, oldHash, newHash := seededRegion(t)
			r := openRegionWithHooks(t, path, key, failingHooks(failAt))
			_, _ = r.save(context.Background(), []ChunkSave{changedSave(chunkKey, 2)})
			_ = r.file.Close() // simulate process loss; do not call region.close/sync

			reopened, err := openRegion(context.Background(), path, key)
			if err != nil { t.Fatal(err) }
			defer reopened.close()
			got, err := reopened.load(context.Background(), chunkKey)
			if err != nil { t.Fatal(err) }
			hash := got.Chunk.Hash()
			if hash != oldHash && hash != newHash {
				t.Fatalf("mixed commit hash=%x", hash)
			}
		})
	}
}
```

Create `TestRegionRecoversOldPayloadAndPromotesRevision`: save revisions 1 and 2, flip one byte in the active revision-2 payload, reopen/load, and assert old logical hash, `Revision==3`, `PersistedRevision==1`, `NeedsRewrite`, and `Recovered`. Save that result at revision 3, reopen, and assert a normal clean revision-3 load. Add `math.MaxUint64` active corruption and assert `ErrCorrupt` with no rewrite.

Add a subprocess helper selected by `MCGO_REGION_CRASH_AFTER`; it calls `os.Exit(86)` immediately after chosen write/sync hooks. The parent runs it with `exec.Command(os.Args[0], "-test.run=TestRegionCrashHelper")`, then reopens and checks old-or-new.

- [ ] **Step 2: Run crash/recovery tests and verify failure injection exposes incomplete behavior**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage -run "TestRegionCommitFailure|TestRegionRecovers|TestRegionCrashSubprocess" -v'
```

Expected: FAIL because region I/O is not injectable and active-payload corruption has no old-bank recovery.

- [ ] **Step 3: Add narrow I/O hooks and exact old-bank promotion**

Default hooks wrap `os.OpenFile`; production callers cannot replace them after Store creation. In `region.load`, first load the active entry. On CRC/decode failure, load the inactive bank's different entry:

```go
old, oldErr := r.loadEntry(ctx, key, slot, inactive.Entries[slot])
if oldErr != nil || activeEntry.Revision == math.MaxUint64 {
	return StoredChunk{}, fmt.Errorf("%w: active=%v fallback=%v", ErrCorrupt, activeErr, oldErr)
}
return StoredChunk{
	Key: key,
	Revision: activeEntry.Revision + 1,
	PersistedRevision: old.Revision,
	NeedsRewrite: true,
	Recovered: true,
	Chunk: old.Chunk,
}, nil
```

The inactive entry is eligible only when present, structurally valid, different from the active extent, and its own CRC/decode passes. Never modify the file during recovery load.

- [ ] **Step 4: Run crash tests repeatedly and under race detection**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage -run "TestRegionCommitFailure|TestRegionRecovers|TestRegionCrashSubprocess" -count=10'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage -race -count=1'
```

Expected: PASS; every iteration yields a complete old or new version and recovery writes revision 3 over corrupt revision 2.

- [ ] **Step 5: Commit**

```bash
git add internal/storage/types.go internal/storage/region.go internal/storage/region_crash_test.go internal/storage/region_recovery_test.go
git commit -m "test: 锁定区域崩溃恢复边界"
```

---

### Task 8: Reuse Free Sectors and Atomically Compact Fragmented Regions

**Files:**
- Create: `internal/storage/region_space.go`
- Create: `internal/storage/region_space_test.go`
- Create: `internal/storage/region_compact_test.go`
- Modify: `internal/storage/region.go`
- Modify: `internal/storage/world_files.go`

**Interfaces:**
- Consumes: Task 7 injectable region I/O and dual-bank recovery
- Produces:

```go
type sectorExtent struct { First, Count uint32 }

type regionSpacePolicy struct {
	WasteRatio float64
	MinWaste   int64
}

var productionRegionSpacePolicy = regionSpacePolicy{
	WasteRatio: 0.25,
	MinWaste:   8 << 20,
}

func freeSectorExtents(regionBank, int64) ([]sectorExtent, error)
func allocateExtent([]sectorExtent, uint32, uint32) (sectorExtent, []sectorExtent)
func (r *region) shouldCompact(regionSpacePolicy) bool
func (r *region) writeCompactedFile(context.Context, *os.File) (regionBank, error)
func (r *region) reopenCanonical() error
func (r *region) compact(context.Context) error
```

- [ ] **Step 1: Write failing allocator and compaction replacement tests**

```go
func TestAllocatorNeverUsesActiveExtentsAndUsesFirstFit(t *testing.T) {
	bank := regionBank{Generation: 4}
	bank.Entries[0] = regionEntry{OffsetSector: 15, SectorCount: 2, PayloadLength: 5000, Revision: 1}
	bank.Entries[1] = regionEntry{OffsetSector: 20, SectorCount: 1, PayloadLength: 100, Revision: 1}
	free, err := freeSectorExtents(bank, 24*sectorSize)
	if err != nil { t.Fatal(err) }
	extent, _ := allocateExtent(free, 3, 24)
	if extent != (sectorExtent{First: 17, Count: 3}) {
		t.Fatalf("extent=%+v", extent)
	}
}
```

Create a region with several versions and a test policy `{WasteRatio:0.20, MinWaste:sectorSize}`. Force compaction and assert: same hashes/revisions for every slot, compacted size equals fixed headers plus live aligned extents, temp file is gone, and reopen succeeds. Inject failure before temp sync, rename, and directory sync; after each failure reopening the canonical path must yield a complete old or compacted file.

- [ ] **Step 2: Run allocator/compaction tests and verify append-only growth fails them**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage -run "TestAllocator|TestRegionCompact" -v'
```

Expected: FAIL because free-space calculation and compaction do not exist.

- [ ] **Step 3: Implement active-bank allocation and canonical temp replacement**

Build an occupancy bitmap from **only the active bank**, reserving sectors 0–14. Coalesce ascending free runs. First-fit an extent large enough for the payload; append at aligned EOF only when none fits. A write may overwrite an inactive-bank-only extent because the active bank remains the complete fallback until the new bank commits.

Compaction must write a brand-new file in slot order:

```go
temp, err := os.CreateTemp(filepath.Dir(r.path), "."+filepath.Base(r.path)+".compact-*")
next, err := r.writeCompactedFile(ctx, temp)
if err != nil { cleanup(); return err }
if err := temp.Sync(); err != nil { cleanup(); return err }
if err := temp.Close(); err != nil { cleanup(); return err }
if err := r.file.Sync(); err != nil { cleanup(); return err }
if err := r.file.Close(); err != nil { cleanup(); return err }
if err := os.Rename(temp.Name(), r.path); err != nil {
	cleanup()
	return errors.Join(err, r.reopenCanonical())
}
if err := syncDirectory(filepath.Dir(r.path)); err != nil {
	return errors.Join(err, r.reopenCanonical())
}
if err := r.reopenCanonical(); err != nil { return err }
r.bank, r.activeBank = next, 0
```

Closing the old descriptor before rename keeps replacement valid on platforms that reject replacing an open file. `reopenCanonical` opens and validates the complete canonical path before installing its descriptor. If rename or directory sync fails, reopen whichever complete canonical file is present before returning the error. Do not use a glob for cleanup; remove only the exact temp created by this call.

- [ ] **Step 4: Run storage race and repeated crash tests**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage -run TestRegionCompact -count=10'
```

Expected: PASS; active extents are never overwritten and both sides of rename remain valid.

- [ ] **Step 5: Commit**

```bash
git add internal/storage/region.go internal/storage/region_space.go internal/storage/region_space_test.go internal/storage/region_compact_test.go internal/storage/world_files.go
git commit -m "feat: 回收并整理区域文件空间"
```

---

### Task 9: Assemble the Multi-Region DiskStore and Storage Benchmarks

**Files:**
- Create: `internal/storage/disk.go`
- Create: `internal/storage/disk_test.go`
- Create: `internal/storage/bench_test.go`
- Modify: `internal/storage/world_files.go`
- Modify: `internal/storage/types.go`

**Interfaces:**
- Consumes: Task 4 world files; Tasks 6–8 regions; Task 1 `Store`
- Produces:

```go
type DiskStore struct {
	mu      sync.Mutex
	files   *worldFiles
	regions map[RegionKey]*region
	closed  bool
}

func OpenDisk(context.Context, string, OpenOptions) (*DiskStore, error)
func (store *DiskStore) Metadata() Metadata
func (store *DiskStore) LoadChunk(context.Context, core.ChunkKey) (StoredChunk, error)
func (store *DiskStore) SaveBatch(context.Context, []ChunkSave) (SaveResult, error)
func (store *DiskStore) Sync(context.Context) error
func (store *DiskStore) Close() error
```

- [ ] **Step 1: Write failing multi-region, lock lifetime, and partial-result tests**

```go
func TestDiskStorePersistsNegativeAndMultipleRegions(t *testing.T) {
	root := t.TempDir()
	store, err := OpenDisk(context.Background(), root, OpenOptions{
		Create: Metadata{FormatVersion: 1, Seed: 42, SpawnDimension: core.Overworld},
	})
	if err != nil { t.Fatal(err) }
	keys := []core.ChunkKey{
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: -33, Z: -1}},
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: 32, Z: 64}},
	}
	if _, err := store.SaveBatch(context.Background(), savesFor(keys, 1)); err != nil { t.Fatal(err) }
	if err := store.Close(); err != nil { t.Fatal(err) }

	reopened, err := OpenDisk(context.Background(), root, OpenOptions{Create: Metadata{Seed: 999}})
	if err != nil { t.Fatal(err) }
	defer reopened.Close()
	if reopened.Metadata().Seed != 42 { t.Fatal("stored metadata seed lost") }
	for _, key := range keys {
		if _, err := reopened.LoadChunk(context.Background(), key); err != nil { t.Fatal(err) }
	}
}
```

Also assert: loading from a nonexistent region does not create a file; groups are ordered by dimension/X/Z; a forced second-region failure returns first-region entries in `SaveResult.Committed`; `Sync` visits every opened region; `Close` joins region-close errors and releases the world lock exactly once.

- [ ] **Step 2: Run DiskStore tests and verify no Store implementation exists**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage -run "TestDiskStore" -v'
```

Expected: FAIL because `OpenDisk` and `DiskStore` do not exist.

- [ ] **Step 3: Implement lazy region caching and region-scoped partial commits**

Use the exact path:

```go
func (store *DiskStore) regionPath(key RegionKey) string {
	return filepath.Join(
		store.files.root,
		"dimensions", strconv.FormatInt(int64(key.Dimension), 10),
		"regions", fmt.Sprintf("r.%d.%d.region", key.X, key.Z),
	)
}
```

`LoadChunk` checks for `os.ErrNotExist` without creating a region and wraps `ErrChunkNotFound`. `SaveBatch` validates all snapshots, groups by `RegionKey`, sorts keys, and calls each region; merge every successful `Committed` map even if a later region returns an error. Region creation uses `MkdirAll(..., 0o755)` and the Task 6 durable empty-file sequence.

`Close` blocks new calls and closes every region in sorted key order. Release `world.lock` only after all region closes succeed; on failure retain the lock and retry only unfinished closes on the next call. Use `errors.Join`, and never double-close a successful region or double-unlock the world.

- [ ] **Step 4: Add and run storage microbenchmarks**

Create benchmarks using one deterministic worldgen chunk and a temp region:

```go
func BenchmarkChunkEncode(b *testing.B) { /* encodeChunkPayload, b.ReportAllocs */ }
func BenchmarkChunkDecode(b *testing.B) { /* decodeChunkPayload, b.ReportAllocs */ }
func BenchmarkDiskStoreSave32(b *testing.B) { /* 32 chunks in one region per iteration */ }
func BenchmarkDiskStoreColdLoad(b *testing.B) { /* reopen then LoadChunk */ }
```

Run:

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage -run "^$" -bench "Benchmark(Chunk|DiskStore)" -benchtime=3x -benchmem'
```

Expected: tests PASS and every benchmark completes without exceeding codec hard limits.

- [ ] **Step 5: Commit**

```bash
git add internal/storage/disk.go internal/storage/disk_test.go internal/storage/bench_test.go internal/storage/world_files.go internal/storage/types.go
git commit -m "feat: 组装多区域磁盘存储"
```

---

### Task 10: Replace Overlay Persistence with Revisioned Chunk Lifecycle State

**Files:**
- Create: `internal/sim/block.go`
- Create: `internal/sim/persistence_lifecycle_test.go`
- Delete: `internal/sim/overlay.go`
- Delete: `internal/sim/overlay_test.go`
- Modify: `internal/sim/world.go`
- Modify: `internal/sim/world_test.go`
- Modify: `internal/sim/engine.go`
- Modify: `internal/sim/engine_test.go`
- Modify: `internal/sim/bench_test.go`
- Modify: `internal/sim/interaction_test.go`
- Modify: `internal/sim/movement_test.go`
- Modify: `internal/sim/player_interaction_test.go`
- Modify: `internal/sim/player_test.go`
- Modify: `internal/sim/spawn_test.go`
- Modify: `internal/server/server.go`
- Modify: `internal/server/generator.go`

**Interfaces:**
- Consumes: existing `world.Chunk` ownership and M2B subscriptions/interactions
- Produces:

```go
type ChunkRecord struct {
	State                ChunkState
	Chunk                *world.Chunk
	Revision             uint64
	PersistedRevision    uint64
	NeedsRewrite         bool
	Recovered            bool
	UnloadRequested      bool
	SaveInFlightRevision uint64
	Err                  error
}

func NewEngine(viewRadius int) *Engine
func NewDimension(core.DimensionID) *Dimension
func (dimension *Dimension) BeginLoading(core.ChunkPos) bool
func (dimension *Dimension) DropLoading(core.ChunkPos)
func (dimension *Dimension) MarkGenerating(core.ChunkPos) bool
func (dimension *Dimension) MarkLoadFailed(core.ChunkPos, error)
func (dimension *Dimension) ApplyLoaded(core.ChunkPos, *world.Chunk, uint64, uint64, bool, bool) error
func (dimension *Dimension) RequestUnload(core.ChunkPos) bool
func (dimension *Dimension) CancelUnload(core.ChunkPos) bool
func (record *ChunkRecord) Dirty() bool
```

- [ ] **Step 1: Write failing generated/loaded/dirty/unload tests**

```go
func TestGeneratedChunkIsDirtyUntilPersisted(t *testing.T) {
	d := NewDimension(core.Overworld)
	pos := core.ChunkPos{X: 2, Z: -4}
	if !d.BeginGeneration(pos) { t.Fatal("generation not started") }
	if err := d.ApplyGenerated(pos, world.NewChunk(pos)); err != nil { t.Fatal(err) }
	record := d.records[pos]
	if record.Revision != 1 || record.PersistedRevision != 0 || !record.Dirty() {
		t.Fatalf("generated record=%+v", record)
	}
	if unloaded := d.RequestUnload(pos); unloaded || record.State != ChunkUnloading || record.Chunk == nil {
		t.Fatalf("dirty chunk was discarded: %+v", record)
	}
}

func TestLoadedChunkKeepsPersistedRevisionAndCancelsUnload(t *testing.T) {
	d := NewDimension(core.Overworld)
	pos := core.ChunkPos{}
	if !d.BeginLoading(pos) { t.Fatal("load not started") }
	if err := d.ApplyLoaded(pos, world.NewChunk(pos), 7, 7, false, false); err != nil { t.Fatal(err) }
	if !d.RequestUnload(pos) { t.Fatal("clean loaded chunk should unload immediately") }

	dirty := NewDimension(core.Overworld)
	if !dirty.BeginLoading(pos) { t.Fatal("dirty load not started") }
	if err := dirty.ApplyLoaded(pos, world.NewChunk(pos), 8, 7, false, false); err != nil { t.Fatal(err) }
	if unloaded := dirty.RequestUnload(pos); unloaded { t.Fatal("dirty loaded chunk was discarded") }
	if record := dirty.records[pos]; record.State != ChunkUnloading || record.Chunk == nil {
		t.Fatalf("dirty unload=%+v", record)
	}
	if !dirty.CancelUnload(pos) || dirty.records[pos].State != ChunkReady {
		t.Fatalf("cancel unload=%+v", dirty.records[pos])
	}
}
```

Add recovery (`current=3,persisted=1,needsRewrite,recovered`), migration rewrite (`current=persisted=7,needsRewrite`), invalid persisted>current panic/error, dirty block changes, and generation result that arrives after all subscriptions leave but remains retained for saving.

- [ ] **Step 2: Run sim lifecycle tests and verify overlays/current lifecycle fail**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim -run "TestGeneratedChunkIsDirty|TestLoadedChunkKeeps|TestPersistenceLifecycle" -v'
```

Expected: FAIL because persistence fields, loading, and dirty unload retention do not exist.

- [ ] **Step 3: Remove overlay authority and add orthogonal persistence state**

Move only `SetBlock` to `block.go`:

```go
func (dimension *Dimension) SetBlock(position core.BlockPos, block core.BlockID) (core.BlockID, bool, error) {
	record, ok := dimension.records[position.Chunk()]
	if !ok || record.State != ChunkReady { return core.AirID, false, ErrChunkNotReady }
	x, _, z := position.Local()
	old := record.Chunk.BlockAt(x, position.Y, z)
	if old == block { return old, false, nil }
	record.Chunk.SetBlock(x, position.Y, z, block)
	return old, true, nil
}
```

Delete `Dimension.base`, `Dimension.overlays`, `BaseBlockLookup`, `ChunkOverlay`, `OverlayEntries`, and `applyOverlay`. Change `NewEngine(base, radius)` to `NewEngine(radius)` and `Generator` to require only `GenerateChunk`.

`RequestUnload` deletes a clean Ready record immediately and returns true. Dirty/in-flight Ready records keep the chunk, become `ChunkUnloading`, set `UnloadRequested`, and return false. `CancelUnload` changes Unloading back to Ready without cloning or reloading.

Update all listed tests to construct `NewEngine(radius)`/`NewDimension(id)` and replace overlay expectations with full-chunk dirty lifecycle expectations. Extra `BaseBlockAt` methods on test generators may remain temporarily, but no interface may require them.

- [ ] **Step 4: Run sim, server, and full race tests**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim ./internal/server -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race -count=1'
```

Expected: PASS; no code references overlay persistence, and generated chunks are retained dirty rather than lost on unload.

- [ ] **Step 5: Commit**

```bash
git add internal/sim internal/server/server.go internal/server/generator.go
git commit -m "refactor: 以完整区块修订替代临时覆盖层"
```

---

### Task 11: Add Budgeted Save Snapshots and Stale-Confirmation Protection in sim

**Files:**
- Create: `internal/sim/persistence.go`
- Create: `internal/sim/persistence_test.go`
- Modify: `internal/sim/world.go`
- Modify: `internal/sim/engine.go`
- Modify: `internal/world/chunk.go`
- Modify: `internal/world/chunk_test.go`

**Interfaces:**
- Consumes: Task 10 persistence fields and immutable chunk cloning
- Produces:

```go
type SaveMode uint8
const (
	SaveUrgent SaveMode = iota
	SaveAll
)

type ChunkSaveSnapshot struct {
	Key            core.ChunkKey
	Revision       uint64
	EstimatedBytes int
	Chunk          *world.Chunk
}

type PersistedChunk struct {
	Key      core.ChunkKey
	Revision uint64
}

type PersistenceStats struct {
	DirtyChunks       int
	EstimatedBytes    int64
	InFlightChunks    int
	UnloadWaiting     int
}

func (engine *Engine) PersistenceSnapshots(int, int, SaveMode) []ChunkSaveSnapshot
func (engine *Engine) ApplyPersisted([]PersistedChunk)
func (engine *Engine) FailPersistence([]ChunkSaveSnapshot)
func (engine *Engine) PersistenceStats() PersistenceStats
func (chunk *world.Chunk) PayloadBytes() int
```

- [ ] **Step 1: Write failing priority, budget, mutation, and stale-ack tests**

```go
func TestPersistenceSnapshotBudgetAndStaleAck(t *testing.T) {
	engine := dirtyPersistenceEngine(t, []core.ChunkPos{{X: 0}, {X: 1}})
	engine.dimensions[core.Overworld].records[core.ChunkPos{X: 1}].UnloadRequested = true

	snapshots := engine.PersistenceSnapshots(1, 1, SaveAll) // first oversized item is allowed
	if len(snapshots) != 1 || snapshots[0].Key.Pos.X != 1 {
		t.Fatalf("priority snapshots=%+v", snapshots)
	}
	snapshots[0].Chunk.SetBlock(0, 0, 0, core.DirtID)
	if got, _ := engine.dimensions[core.Overworld].BlockAt(core.BlockPos{}); got == core.DirtID {
		t.Fatal("save snapshot aliases authority")
	}

	changeAuthoritativeChunk(t, engine, snapshots[0].Key)
	engine.ApplyPersisted([]PersistedChunk{{Key: snapshots[0].Key, Revision: snapshots[0].Revision}})
	record := engine.dimensions[core.Overworld].records[snapshots[0].Key.Pos]
	if !record.Dirty() || record.PersistedRevision != snapshots[0].Revision {
		t.Fatalf("stale ack cleared newer dirty state: %+v", record)
	}
}
```

Also assert: `SaveUrgent` selects only `UnloadRequested`; `SaveAll` orders unload first then `ChunkKey`; one key has at most one in-flight snapshot; a failure clears only matching in-flight revision; a successful same-revision migration rewrite clears `NeedsRewrite`; an ack above current panics; clean unload deletes after ack; re-subscribed unload cancellation retains the chunk; `PayloadBytes` is deterministic.

`PersistenceStats.EstimatedBytes` must count the authoritative dirty chunk once and, when `SaveInFlightRevision != 0`, count the immutable in-flight clone a second time. This is the memory actually retained during save/retry and is the value used by server backpressure.

- [ ] **Step 2: Run persistence tests and verify APIs are absent**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim ./internal/world -run "TestPersistence|TestChunkPayloadBytes" -v'
```

Expected: FAIL because snapshot/ack/stat APIs do not exist.

- [ ] **Step 3: Implement deterministic selection and exact acknowledgement rules**

Implement `world.Chunk.PayloadBytes()` as the sum of all 24 existing `PalettedContainer.PayloadBytes()` results plus a fixed 512-byte envelope allowance; it must not call `Snapshot` or allocate. Sort candidates by `UnloadRequested` descending, then dimension/X/Z ascending. Stop before a budget overflow unless zero snapshots have been selected.

On selection:

```go
record.SaveInFlightRevision = record.Revision
snapshot := ChunkSaveSnapshot{
	Key: key, Revision: record.Revision,
	EstimatedBytes: estimateChunkBytes(record.Chunk),
	Chunk: record.Chunk.Clone(),
}
```

On success, require `ack.Revision <= record.Revision`; set `PersistedRevision=max(...)`. Clear `SaveInFlightRevision` only when it equals the ack. Clear `NeedsRewrite` when the ack equals current. Delete an Unloading record only when clean and no save is in flight. `FailPersistence` clears only an exact matching in-flight revision and never advances persisted state.

- [ ] **Step 4: Run sim/world and full race tests**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/world ./internal/sim -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race -count=1'
```

Expected: PASS; save snapshots never alias authority and stale completions cannot clear newer changes.

- [ ] **Step 5: Commit**

```bash
git add internal/sim/persistence.go internal/sim/persistence_test.go internal/sim/world.go internal/sim/engine.go internal/world/chunk.go internal/world/chunk_test.go
git commit -m "feat: 预算化权威区块保存快照"
```

---

### Task 12: Load from Storage Before Falling Back to Generation

**Files:**
- Create: `internal/server/acquire.go`
- Create: `internal/server/acquire_test.go`
- Modify: `internal/sim/command.go`
- Modify: `internal/sim/engine.go`
- Modify: `internal/sim/engine_test.go`
- Modify: `internal/server/server.go`
- Modify: `internal/server/generator.go`
- Modify: `internal/server/generator_test.go`
- Modify: `internal/server/integration_test.go`
- Modify: `internal/server/player_integration_test.go`
- Modify: `internal/server/player_test.go`
- Modify: `internal/server/publication_test.go`
- Modify: `internal/server/subscription_test.go`
- Modify: `internal/server/tick_test.go`
- Modify: `cmd/mcgo/app.go`

**Interfaces:**
- Consumes: Task 1 `Store`/`NewMemory`; Task 10 Loading/Generating transitions
- Produces:

```go
type AcquiredChunk struct {
	Key               core.ChunkKey
	Chunk             *world.Chunk
	Revision          uint64
	PersistedRevision uint64
	NeedsRewrite      bool
	Recovered         bool
	Missing           bool
	Err               error
}

func (engine *Engine) SubmitAcquired(AcquiredChunk)

type TickResult struct {
	Acquire  []core.ChunkKey
	Generate []core.ChunkKey
	Forget   map[SessionID][]core.ChunkKey
	Ready    []core.ChunkKey
	Changes  []ChunkChangeBatch
	Rejected []Rejection
	Resync   []ResyncRequest
	Players  []PlayerUpdate
	Tick     uint64
}

func New(Config, network.ServerEndpoint, Generator, storage.Store) *Server
func NewMemory(Config, network.ServerEndpoint, Generator) *Server
func NewEmbedded(Config, network.ServerEndpoint, storage.Store) *Server
func NewEmbeddedMemory(Config, network.ServerEndpoint) *Server
```

- [ ] **Step 1: Write failing hit/miss/error ordering tests**

```go
func TestAcquireLoadsBeforeGenerating(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{}}
	store := storage.NewMemory(storage.Metadata{FormatVersion: 1, Seed: 42})
	want := world.NewChunk(key.Pos)
	want.SetBlock(0, 0, 0, core.DirtID)
	_, _ = store.SaveBatch(context.Background(), []storage.ChunkSave{{Key:key, Revision:7, Chunk:want}})
	generator := &countingGenerator{}
	running, client := newAcquireServer(t, store, generator)

	stepUntilServer(t, running, func(result sim.TickResult) bool {
		_, revision, ready := running.ChunkHash(core.Overworld, key.Pos)
		return ready && revision == 7
	})
	if generator.Calls() != 0 { t.Fatalf("storage hit generated %d times", generator.Calls()) }
	_ = client
}
```

Add tests proving: initial subscription emits `Acquire`, not `Generate`; a typed `ErrChunkNotFound` result transitions Loading→Generating and emits exactly one `Generate`; a permission/corruption error becomes `ChunkFailed` with zero generator calls; loaded revision/rewrite/recovered flags reach `ChunkRecord`; results are sorted by `ChunkKey`; a forgotten queued acquire is canceled; a late result after cancellation creates no duplicate authority.

- [ ] **Step 2: Run acquisition tests and verify direct generation fails them**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server ./internal/sim -run "TestAcquire|TestSubscription" -v'
```

Expected: FAIL because subscriptions still emit generation directly and Server has no Store.

- [ ] **Step 3: Add acquisition inbox semantics to sim**

`reconcileSubscriptions` calls `BeginLoading` and appends to `result.Acquire`. At the beginning of `Engine.Step`, sort and apply acquired results before advancing pending players:

```go
switch {
case acquired.Err != nil:
	dimension.MarkLoadFailed(acquired.Key.Pos, acquired.Err)
case acquired.Missing:
	if _, wanted := engine.wanted[acquired.Key]; !wanted {
		dimension.DropLoading(acquired.Key.Pos)
	} else if dimension.MarkGenerating(acquired.Key.Pos) {
		result.Generate = append(result.Generate, acquired.Key)
	}
default:
	err := dimension.ApplyLoaded(acquired.Key.Pos, acquired.Chunk,
		acquired.Revision, acquired.PersistedRevision,
		acquired.NeedsRewrite, acquired.Recovered)
	if err != nil { dimension.MarkLoadFailed(acquired.Key.Pos, err) }
}
```

If a clean loaded chunk is no longer wanted, unload it immediately. If a generated/recovered/rewrite-dirty chunk is no longer wanted, retain it as Unloading for persistence.

- [ ] **Step 4: Add Store-backed worker jobs and migrate constructors**

Use one chunk-worker pool with explicit job kinds:

```go
type chunkJobKind uint8
const (
	chunkJobLoad chunkJobKind = iota
	chunkJobGenerate
)
type chunkJob struct { Kind chunkJobKind; Key core.ChunkKey }
```

For load jobs:

```go
stored, err := server.store.LoadChunk(server.ctx, job.Key)
result := sim.AcquiredChunk{Key: job.Key}
switch {
case err == nil:
	result.Chunk, result.Revision = stored.Chunk, stored.Revision
	result.PersistedRevision = stored.PersistedRevision
	result.NeedsRewrite, result.Recovered = stored.NeedsRewrite, stored.Recovered
case errors.Is(err, storage.ErrChunkNotFound):
	result.Missing = true
default:
	result.Err = fmt.Errorf("load %v: %w", job.Key, err)
}
```

`NewMemory` constructs metadata from config and delegates to `New`. `NewEmbedded` replaces config seed/spawn values from `store.Metadata()` before constructing `worldgen.New(metadata.Seed)`. Update all listed server tests to use `NewMemory` for custom generators and `NewEmbeddedMemory` for built-in generation; update `cmd/mcgo/app.go` to `NewEmbeddedMemory` until Task 16 wires disk.

- [ ] **Step 5: Run sim/server and full race tests**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim ./internal/server -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race -count=1'
```

Expected: PASS; only typed storage misses invoke generation, and all existing world/player tests use memory persistence.

- [ ] **Step 6: Commit**

```bash
git add internal/sim/command.go internal/sim/engine.go internal/sim/engine_test.go internal/server cmd/mcgo/app.go
git commit -m "feat: 存档未命中后再生成区块"
```

---

### Task 13: Add Region-Grouped Save Workers and 6000-Tick Autosave

**Files:**
- Create: `internal/server/persistence.go`
- Create: `internal/server/persistence_test.go`
- Modify: `internal/server/config.go`
- Modify: `internal/server/server.go`

**Interfaces:**
- Consumes: Task 11 snapshot/confirmation API; Task 1 `RegionFor` and `Store.SaveBatch`
- Produces:

```go
type saveJob struct {
	Region    storage.RegionKey
	Snapshots []sim.ChunkSaveSnapshot
}

type saveCompletion struct {
	Job    saveJob
	Result storage.SaveResult
	Err    error
}

type Config struct {
	Seed             int64
	ViewRadius       int
	Workers          int
	SnapshotChunks   int
	SnapshotBytes    int
	OutboxCapacity   int
	TickObserver     func(time.Duration)
	SpawnDimension   core.DimensionID
	SpawnAnchor      core.ChunkPos
	TrustedObserver  bool
	SaveWorkers      int
	SaveChunks       int
	SaveBytes        int
	AutosaveTicks    uint64
	UnsavedBytes     int64
	ShutdownTimeout time.Duration
	SaveObserver     func(time.Duration)
}
```

Production defaults are `SaveWorkers=2`, `SaveChunks=8`, `SaveBytes=4<<20`, `AutosaveTicks=6000`, `UnsavedBytes=512<<20`, and `ShutdownTimeout=30*time.Second`.

- [ ] **Step 1: Write failing urgent-save, autosave, and nonblocking tests**

```go
func TestAutosaveBeginsAtConfiguredTickWithoutBlockingStep(t *testing.T) {
	store := newGatedStore(t)
	config := DefaultConfig(42)
	config.ViewRadius, config.Workers, config.SaveWorkers = 0, 1, 1
	config.AutosaveTicks = 2
	running := newPersistenceServer(t, config, store)
	waitGeneratedDirty(t, running)

	running.StepForTest()
	started := make(chan struct{})
	go func() { running.StepForTest(); close(started) }()
	select {
	case <-started:
	case <-time.After(time.Second): t.Fatal("Step blocked on gated Store.SaveBatch")
	}
	if !store.WaitSaveStarted(time.Second) { t.Fatal("autosave was not dispatched") }
}
```

Add: dirty Unloading snapshots dispatch before autosave; same-region snapshots form one sorted job; different regions form separate jobs; successful result is applied only when drained by the next Step; partial `SaveResult.Committed` applies only listed keys; normal snapshots honor chunk/byte budgets and allow one oversized first item.

- [ ] **Step 2: Run persistence orchestration tests and verify no save workers exist**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "TestAutosave|TestUrgentSave|TestSaveJobs" -v'
```

Expected: FAIL because Server never requests persistence snapshots or calls Store.SaveBatch.

- [ ] **Step 3: Implement dedicated save workers and tick scheduling**

Create `saveJobs`/`saveCompletions` channels sized to `SaveWorkers*2` and a separate save wait group. A save worker converts immutable sim snapshots without sharing authority:

```go
saves := make([]storage.ChunkSave, len(job.Snapshots))
for i, snapshot := range job.Snapshots {
	saves[i] = storage.ChunkSave{Key:snapshot.Key, Revision:snapshot.Revision, Chunk:snapshot.Chunk}
}
result, err := server.store.SaveBatch(server.saveCtx, saves)
if server.config.SaveObserver != nil {
	server.config.SaveObserver(time.Since(started))
}
server.saveCompletions <- saveCompletion{Job:job, Result:result, Err:err}
```

Capture `started := time.Now()` immediately before `SaveBatch`. `Config.validate` rejects nonpositive save workers/chunks/bytes, zero autosave ticks, nonpositive unsaved bytes, and nonpositive shutdown timeout; `SaveObserver` may be nil.

At Step start, drain completions before `engine.Step`. Apply committed revisions first. For this commit, call `engine.FailPersistence` for every uncommitted snapshot on error so no key is stuck in flight; Task 14 replaces that immediate re-eligibility with retained exponential-backoff retries.

After publication/acquisition scheduling, always request `SaveUrgent`. When `result.Tick%AutosaveTicks==0`, set `autosaveActive=true`; while active request `SaveAll` each tick until stats show no dirty or in-flight chunks, then clear it. Group selected snapshots by `storage.RegionFor`, sort region keys and snapshots, and enqueue without blocking; if a save channel is full, call `FailPersistence` on the undispatched group so a later tick can retry.

- [ ] **Step 4: Run server and full race tests**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race -count=1'
```

Expected: PASS; a blocked Store does not block Step and autosave drains through completion messages.

- [ ] **Step 5: Commit**

```bash
git add internal/server/config.go internal/server/server.go internal/server/persistence.go internal/server/persistence_test.go
git commit -m "feat: 异步自动保存权威区块"
```

---

### Task 14: Retain Failed Saves, Back Off, Bound Memory, and Expose Status

**Files:**
- Modify: `internal/server/persistence.go`
- Modify: `internal/server/persistence_test.go`
- Modify: `internal/server/config.go`
- Modify: `internal/server/server.go`
- Modify: `internal/sim/persistence.go`
- Modify: `internal/sim/persistence_test.go`

**Interfaces:**
- Consumes: Task 13 save jobs/completions and Task 11 stats
- Produces:

```go
type retrySave struct {
	Job       saveJob
	Attempts  uint32
	NextTick  uint64
	LastError error
}

type PersistenceStatus struct {
	DirtyChunks        int
	EstimatedBytes     int64
	InFlightChunks     int
	Backpressured      bool
	LastSuccess        time.Time
	LastError          string
	LastErrorAt        time.Time
	AutosaveDrained    bool
}

func (server *Server) PersistenceStatus() PersistenceStatus
```

Config gains test-overridable `RetryBaseTicks` and `RetryMaxTicks`; production defaults are exactly 20 and 1200.

- [ ] **Step 1: Write failing retry/coalescing/backpressure/status tests**

```go
func TestSaveFailureRetriesWithBoundedBackoffAndKeepsDirty(t *testing.T) {
	store := newFailThenSucceedStore(2)
	config := tinyPersistenceConfig()
	config.RetryBaseTicks, config.RetryMaxTicks = 1, 4
	running := newPersistenceServer(t, config, store)
	waitGeneratedDirty(t, running)

	for range 8 { running.StepForTest() }
	if got := store.SaveCalls(); got != 3 { t.Fatalf("save calls=%d, want 3", got) }
	status := running.PersistenceStatus()
	if status.DirtyChunks != 0 || status.LastSuccess.IsZero() || status.LastError == "" {
		t.Fatalf("status=%+v", status)
	}
}
```

Add: failures occur at tick delays 1 then 2 then capped 4; a new mutation while a retry is pending does not enqueue another snapshot for that key; after the old retry commits, the newer revision remains dirty and gets a new job; partial commits are acked while uncommitted keys retry; pending/dirty/in-flight estimated bytes trigger backpressure; while backpressured queued Acquire jobs are not dispatched and unknown cells remain barriers; recovery below the cap resumes acquisition.

- [ ] **Step 2: Run retry/backpressure tests and verify Task 13 drops failed snapshots**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "TestSaveFailure|TestPersistenceBackpressure|TestPersistenceStatus" -v'
```

Expected: FAIL because failures clear in-flight state immediately and no retry/status/backpressure exists.

- [ ] **Step 3: Implement per-region retained retries and exponential tick delay**

On a failed completion, apply any `Result.Committed` entries, remove those snapshots from the job, and retain the rest under `retry[Region]`. Keep their sim in-flight revisions unchanged so no duplicate snapshot is created. Coalesce only by replacing a retry snapshot when the same key and revision are identical; never mutate the cloned payload.

Compute delay without overflow:

```go
func retryDelay(base, maximum uint64, attempts uint32) uint64 {
	delay := base
	for i := uint32(1); i < attempts && delay < maximum; i++ {
		if delay > maximum/2 { return maximum }
		delay *= 2
	}
	return min(delay, maximum)
}
```

When a retry succeeds, apply ack; if authority has a newer revision it remains dirty and becomes eligible for a fresh snapshot. Do not retain an unbounded history.

Before dispatching chunk load/generation, read `engine.PersistenceStats()`. Set backpressure when estimated bytes are `>= UnsavedBytes`; clear it only when below 90% of the cap to avoid oscillation. Backpressure leaves pending acquisition keys queued but unscheduled.

Guard status with `stepMu` and return copied strings/times only. Log error operation, world path when available, region, attempt, next tick, and wrapped root cause.

- [ ] **Step 4: Run retry tests, server race tests, and memory-bound regression**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server ./internal/sim -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race -count=1'
```

Expected: PASS; failed saves retain one snapshot/key, status is race-free, and acquisition resumes after recovery.

- [ ] **Step 5: Commit**

```bash
git add internal/server/config.go internal/server/server.go internal/server/persistence.go internal/server/persistence_test.go internal/sim/persistence.go internal/sim/persistence_test.go
git commit -m "feat: 存档失败退避与内存背压"
```

---

### Task 15: Replace Fire-and-Forget Close with Retryable Safe Shutdown

**Files:**
- Create: `internal/server/shutdown.go`
- Create: `internal/server/shutdown_test.go`
- Modify: `internal/server/server.go`
- Modify: `internal/server/session.go`
- Modify: `internal/server/config.go`
- Modify: `internal/server/generator_test.go`
- Modify: `internal/server/integration_test.go`
- Modify: `internal/server/player_integration_test.go`
- Modify: `internal/server/player_test.go`
- Modify: `internal/server/publication_test.go`
- Modify: `internal/server/session_test.go`
- Modify: `internal/server/subscription_test.go`
- Modify: `internal/server/tick_test.go`

**Interfaces:**
- Consumes: Task 14 retained retries/status; Store `Sync`/`Close`
- Produces:

```go
func (server *Server) Shutdown(context.Context) error
```

Server owns separate runtime and save contexts/wait groups; canceling runtime work must not cancel the Store flush needed by shutdown.

- [ ] **Step 1: Write failing flush/retry/idempotence/leak tests**

```go
func TestShutdownFailureFreezesAndCanRetry(t *testing.T) {
	store := newSwitchableFailStore()
	running := dirtyServer(t, store)
	store.Fail(errors.New("disk full"))

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := running.Shutdown(ctx)
	if err == nil || !strings.Contains(err.Error(), "disk full") { t.Fatalf("Shutdown=%v", err) }
	if running.PersistenceStatus().DirtyChunks == 0 { t.Fatal("failed shutdown lost dirty state") }
	if result := running.StepForTest(); result.Tick != 0 { t.Fatalf("frozen server stepped: %+v", result) }

	store.Recover()
	if err := running.Shutdown(ctx); err != nil { t.Fatal(err) }
	if err := running.Shutdown(ctx); err != nil { t.Fatalf("idempotent shutdown=%v", err) }
}
```

Add tests for context timeout, final buffered command tick, Store.Sync before Store.Close, lock release only after success, Run using a fresh 30-second context after run-context cancellation, persistence error taking precedence over `context.Canceled`, and all goroutines exiting within one shared 1-second deadline.

- [ ] **Step 2: Run shutdown tests and verify Close discards dirty data**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "TestShutdown|TestServerRun" -v'
```

Expected: FAIL because current `Close` cancels workers without persistence and cannot return/retry errors.

- [ ] **Step 3: Implement a frozen, retryable lifecycle**

Split lifecycle state:

```go
type serverLifecycle uint8
const (
	serverRunning serverLifecycle = iota
	serverClosing
	serverClosed
)
```

The first Shutdown call locks `stepMu`, transitions to Closing, drains already-owned input/acquire/generation/save completions, executes one final `engine.Step`, and never publishes or schedules new acquisition afterward. Then close the endpoint/session, cancel runtime workers, and wait for them outside `stepMu`.

While frozen, repeatedly:

```go
server.drainSaveCompletionsLocked()
server.dispatchPersistenceLocked(sim.SaveAll)
if server.engine.PersistenceStats().DirtyChunks == 0 && len(server.retries) == 0 {
	break
}
select {
case completion := <-server.saveCompletions:
	server.applySaveCompletionLocked(completion)
case <-ctx.Done():
	return errors.Join(ctx.Err(), server.lastPersistenceError())
}
```

On a non-context save error during shutdown, return it with Store and frozen authority intact for a later retry; do not wait for normal backoff. On success call `store.Sync(ctx)`, `store.Close`, cancel save workers, wait, mark Closed, and cache nil for idempotent calls.

Delete `Server.Close`. Update all test cleanup to call a helper with a one-second context and fail the test on non-nil error. `Run` on context cancellation calls `Shutdown` with `context.WithTimeout(context.Background(), config.ShutdownTimeout)` and returns shutdown error first, otherwise the original context error.

- [ ] **Step 4: Run shutdown repetitions and full race tests**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "TestShutdown|TestServerRun" -count=10'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race -count=1'
```

Expected: PASS; a failed shutdown is retryable, success flushes before unlock, and no goroutine leak remains.

- [ ] **Step 5: Commit**

```bash
git add internal/server
git commit -m "feat: 可重试安全关服并刷盘"
```

---

### Task 16: Wire the Default Persistent World and Keep Benchmark Storage In-Memory

**Files:**
- Modify: `cmd/mcgo/app.go`
- Modify: `cmd/mcgo/app_test.go`
- Modify: `cmd/mcgo/main.go`
- Modify: `cmd/mcgo/benchmark.go`
- Create: `cmd/mcgo/storage_test.go`

**Interfaces:**
- Consumes: Task 9 `OpenDisk`/`NewMemory`; Task 12 `NewEmbedded`; Task 15 `Shutdown`
- Produces:

```go
type applicationOptions struct {
	Seed      int64
	Benchmark bool
	WorldPath string
}

func openApplicationStore(context.Context, applicationOptions) (storage.Store, error)
func newApplication(applicationOptions) (*application, error)
func (a *application) Close() error
```

- [ ] **Step 1: Write failing store-selection and close-error tests**

```go
func TestApplicationStoreSelection(t *testing.T) {
	worldPath := filepath.Join(t.TempDir(), "chosen-world")
	disk, err := openApplicationStore(context.Background(), applicationOptions{
		Seed: 42, WorldPath: worldPath,
	})
	if err != nil { t.Fatal(err) }
	if _, ok := disk.(*storage.DiskStore); !ok { t.Fatalf("interactive store=%T", disk) }
	if err := disk.Close(); err != nil { t.Fatal(err) }

	memory, err := openApplicationStore(context.Background(), applicationOptions{
		Seed: benchmarkSeed, Benchmark: true,
		WorldPath: filepath.Join(t.TempDir(), "must-not-exist"),
	})
	if err != nil { t.Fatal(err) }
	if _, ok := memory.(*storage.MemoryStore); !ok { t.Fatalf("benchmark store=%T", memory) }
}
```

Assert benchmark mode does not create `WorldPath`; an existing disk world's metadata seed overrides option seed; `application.Close` returns the server persistence error while still releasing mesher/renderer/GPU resources once; argument parsing defaults to `worlds/default` and accepts `--world`.

- [ ] **Step 2: Run command tests without launching the application**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo -run "TestApplicationStore|TestApplicationClose|TestMainOptions" -v'
```

Expected: FAIL because the application always constructs an implicit memory server and Close returns no error.

- [ ] **Step 3: Inject storage before server construction and propagate shutdown failure**

`openApplicationStore` returns memory for benchmark and otherwise calls:

```go
storage.OpenDisk(ctx, options.WorldPath, storage.OpenOptions{
	Create: storage.Metadata{
		FormatVersion: 1,
		Seed: options.Seed,
		SpawnDimension: core.Overworld,
		SpawnAnchor: core.ChunkPos{},
	},
})
```

Pass the Store to `server.NewEmbedded`. If any later app construction step fails, close the Store or Shutdown the started server before returning the wrapped error.

Refactor `application.Close` to capture an error outside `sync.Once`; cancel the run context, wait for `serverDone`, treat a successful-shutdown `context.Canceled` as normal, and return any persistence error after all graphics resources are released.

Refactor main into an error-returning `run(args []string) error`. Add `--world` defaulting to `worlds/default`. Main logs the returned error and exits nonzero; do not use a deferred Close whose error is discarded.

Keep benchmark entirely headless and memory-backed. Change the window title to `minecraft-go — M3A persistent world`, but do not launch it during this task.

- [ ] **Step 4: Run command and full race tests only**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo ./internal/server ./internal/storage -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race -count=1'
```

Expected: PASS. No command in this task creates a GLFW window or runs `go run ./cmd/mcgo`.

- [ ] **Step 5: Commit**

```bash
git add cmd/mcgo/app.go cmd/mcgo/app_test.go cmd/mcgo/main.go cmd/mcgo/benchmark.go cmd/mcgo/storage_test.go
git commit -m "feat: 单机默认打开持久世界"
```

---

### Task 17: Prove Restart Consistency and Establish M3A Final Gates

**Files:**
- Create: `internal/server/persistence_integration_test.go`
- Modify: `internal/server/integration_test.go`
- Modify: `internal/server/player_integration_test.go`
- Modify: `internal/storage/bench_test.go`
- Modify: `internal/client/perf.go`
- Modify: `internal/client/perf_test.go`
- Modify: `cmd/mcgo/app.go`
- Modify: `cmd/mcgo/benchmark.go`
- Modify: `cmd/perfcheck/main.go`
- Modify: `.github/workflows/ci.yml`
- Modify: `docs/notes/perf-baseline.md`
- Modify: `docs/notes/perf-baseline.json`

**Interfaces:**
- Consumes: every prior M3A task and existing MemoryTransport/Mirror integration helpers
- Produces: deterministic restart proof, scenario v4 offscreen baseline, storage benchmark baseline, and final CI gates

```go
type persistentHarness struct {
	t             *testing.T
	root          string
	clientEndpoint network.ClientEndpoint
	running       *server.Server
	mirror        *client.Mirror
	generator     *countingPersistenceGenerator
}

func newPersistentHarness(*testing.T, string, server.Generator) *persistentHarness
func (h *persistentHarness) waitReady()
func (h *persistentHarness) placeAndBreakScript()
func (h *persistentHarness) authoritativeChunk(core.ChunkPos) ([32]byte, uint64)
func (h *persistentHarness) assertMirrorMatches(core.ChunkPos)
func (h *persistentHarness) moveViewToUnvisitedChunk()
func (h *persistentHarness) assertGeneratorBWasUsedOnlyForUnvisited()
func (h *persistentHarness) shutdown()
```

- [ ] **Step 1: Write the disk restart and generator-upgrade vertical-slice gate**

Create a test harness that owns a temp world path but constructs a fresh Store/Server/MemoryTransport/Mirror for each process lifetime:

```go
func TestWorldPersistsAcrossRestartAndGeneratorUpgrade(t *testing.T) {
	root := t.TempDir()
	first := newPersistentHarness(t, root, generatorA{})
	first.waitReady()
	first.placeAndBreakScript()
	wantHash, wantRevision := first.authoritativeChunk(core.ChunkPos{})
	first.shutdown()

	second := newPersistentHarness(t, root, generatorB{})
	second.waitReady()
	gotHash, gotRevision := second.authoritativeChunk(core.ChunkPos{})
	if gotHash != wantHash || gotRevision != wantRevision {
		t.Fatalf("restart=(%x,%d), want=(%x,%d)", gotHash, gotRevision, wantHash, wantRevision)
	}
	second.assertMirrorMatches(core.ChunkPos{})
	second.moveViewToUnvisitedChunk()
	second.assertGeneratorBWasUsedOnlyForUnvisited()
	second.shutdown()
}
```

Add end-to-end cases for: dirty unload/reload; saved migration rewrite; corrupt active payload recovering old bank and committing promoted revision; both payload copies corrupt yielding `ChunkFailed` and zero generator calls; disk-full shutdown error followed by recovery/retry; final goroutine count returning within one deadline.

- [ ] **Step 2: Run the vertical slice as a fresh final integration gate**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "TestWorldPersists|TestCorruptStoredChunk|TestPersistentShutdown" -v'
```

Expected: PASS using Tasks 1–16. If it fails, invoke `superpowers:systematic-debugging`, reproduce the failing boundary in its focused package, add a regression test there, make the minimal correction, and rerun this vertical slice before proceeding.

- [ ] **Step 3: Lock CI, benchmark fields, and scenario v4**

Update CI's architecture/protocol gate to include storage:

```yaml
- name: 架构、存储与协议门禁
  run: go test ./internal/archcheck ./internal/storage ./internal/network ./internal/physics -v
```

Increase `scenarioVersion` from 3 to 4 because tick ordering and the save snapshot/confirmation path changed. The scenario remains a 2560×1440 headless device, 10-second warmup, 60-second still phase, 120-second 48-block/s flying phase, render radius 32, halo 33, and final server/Mirror hash check.

Extend `client.PerfReport` with a required `Persistence` field while leaving existing phase fields unchanged:

```go
type PersistenceSummary struct {
	Snapshots uint64  `json:"snapshots"`
	P50MS     float64 `json:"p50_ms"`
	P95MS     float64 `json:"p95_ms"`
	P99MS     float64 `json:"p99_ms"`
	MaxMS     float64 `json:"max_ms"`
}

Persistence PersistenceSummary `json:"persistence"`
```

The memory Store must exercise cloning, dispatch, and acknowledgement; it must not perform disk I/O or zstd compression in the render scenario. Add storage encode/decode/save/load benchmark numbers to `perf-baseline.md`; `perfcheck` compares new persistence timing fields only when both reports contain them and still rejects scenario/hardware mismatch.

Add a concurrency-safe save recorder to `application`, wire `Config.SaveObserver` to it, reset it with the tick recorder after warmup, and require `Persistence.Snapshots > 0` before accepting a report. This records memory-Store save jobs during the flying/unload path without adding disk I/O.

- [ ] **Step 4: Run all platform-independent final gates**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go vet ./...'
zsh -ic 'gvm use go1.26.0 >/dev/null && test -z "$(gofmt -l .)"'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -run "^$" -bench=. -benchtime=1x'
git diff --check
```

Expected: all exit zero; race reports no races, formatting prints nothing, every benchmark is executable, and the worktree contains only intended M3A changes.

- [ ] **Step 5: Run the real scenario v4 gate offscreen without a foreground window**

Create a temporary output path outside the repository, then run only the existing headless flag:

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --perf-output /tmp/mcgo-m3a-perf.json'
```

Expected: no GLFW window is created; still/flying FPS are ≥100, frame p99 `<12 ms`, RSS `<2 GiB`, tick p99 `<10 ms`, tick max `<50 ms`, and final server/Mirror hash/revision match.

Copy the accepted report fields into `docs/notes/perf-baseline.json` and describe exact storage microbenchmarks plus the scenario v4 result in `docs/notes/perf-baseline.md`. Then compare a second unchanged run on the same machine:

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --perf-output /tmp/mcgo-m3a-current.json'
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck -baseline docs/notes/perf-baseline.json -current /tmp/mcgo-m3a-current.json -max-regression 0.20'
```

Expected: `性能比较通过：所有阶段退化均未超过阈值`.

- [ ] **Step 6: Request final code review and resolve findings with focused tests**

Review the complete diff against `docs/superpowers/specs/2026-07-28-m3a-world-persistence-design.md`. For each accepted finding, add or tighten a failing focused test, implement the smallest correction, rerun the affected package with `-race`, and keep all corrections in this final task commit. Reject suggestions that add player data, TCP, global transactions, backup UI, or unrelated refactors.

- [ ] **Step 7: Re-run final verification after all review corrections**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go vet ./...'
zsh -ic 'gvm use go1.26.0 >/dev/null && test -z "$(gofmt -l .)"'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck ./internal/storage ./internal/network ./internal/physics -count=1'
git diff --check
git status --short
```

Expected: all checks exit zero and status lists only the final gate/test/baseline files intended for this task.

- [ ] **Step 8: Commit**

```bash
git add internal/server/persistence_integration_test.go internal/server/integration_test.go internal/server/player_integration_test.go internal/storage/bench_test.go internal/client/perf.go internal/client/perf_test.go cmd/mcgo/app.go cmd/mcgo/benchmark.go cmd/perfcheck/main.go .github/workflows/ci.yml docs/notes/perf-baseline.md docs/notes/perf-baseline.json
git commit -m "chore: M3A 存档重启与性能门禁"
```

---

## M3A Completion Checklist

- [ ] Every generated chunk is dirty until a complete full-chunk save commits.
- [ ] Existing disk metadata controls seed/spawn; tests and benchmark use memory storage.
- [ ] Negative coordinates map to the specified 32×32 region slot.
- [ ] Region payloads and banks are explicitly encoded, checksummed, bounded, and versioned.
- [ ] Every injected write/sync/bank/rename crash reopens a complete old or new state.
- [ ] Old-bank recovery promotes revision above a corrupt active entry and rewrites cleanly.
- [ ] Migration continuity, v1 fixture, future-version rejection, and fuzz limits are enforced.
- [ ] Storage hits bypass generation; only `ErrChunkNotFound` generates.
- [ ] Dirty unload, re-subscribe cancellation, one-in-flight save, and stale ack rules pass.
- [ ] Autosave, retry, partial results, memory backpressure, and status are race-free.
- [ ] `Shutdown(ctx)` reports failure, retains state for retry, syncs before unlock, and leaks no goroutines.
- [ ] Interactive default is `worlds/default`; save errors exit nonzero; benchmark never opens disk or a foreground window.
- [ ] Restart, generator upgrade, corruption, disk-full recovery, and Mirror consistency pass end to end.
- [ ] Full race, vet, gofmt, dependency, fuzz smoke, benchmark smoke, and scenario v4 gates pass.
- [ ] Every task is one reviewed commit and the final worktree is clean.
