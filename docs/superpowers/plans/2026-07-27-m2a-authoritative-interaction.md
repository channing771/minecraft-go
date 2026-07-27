# M2A Authoritative Interaction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the first authoritative client/server vertical slice: a 20 TPS embedded server owns terrain and block state, the client renders an independent subscribed mirror, and the player can instantly break and place blocks through validated ray commands.

**Architecture:** Add transport-neutral domain types and a bounded in-memory protocol boundary, then put all authoritative writes behind a deterministic `sim.Engine`. `server.Server` owns generation, subscriptions, snapshot publication, and the session overlay; `client.Mirror` and `client.Mesher` replace client-side terrain generation while leaving `render` and `gfx` unaware of networking.

**Tech Stack:** Go 1.26 via the user's GVM installation, `go-gl/mathgl` for vectors, existing paletted world storage and greedy mesher, bounded channels, `context`, `log/slog`, GLFW, WebGPU/Metal, Go tests/benchmarks/race detector.

## Global Constraints

- Use the GVM-managed Go toolchain through `zsh -ic`; do not install or download another Go distribution.
- Preserve the untracked `.claude/` directory and never stage it.
- The authoritative simulation runs at exactly 20 TPS; production tick interval is 50 ms.
- Render radius is 32 chunks; server subscription radius is 33 chunks and cannot be chosen by the client.
- Interaction reach is exactly 6.0 blocks.
- M2A uses instant breaking and unlimited placement of stone, dirt, and grass selected with `1/2/3`.
- Client and server must never share mutable chunk, section, container, palette, or packed-data slices.
- The client must not call `worldgen.GenerateChunk` after the migration task.
- `internal/network` depends only on `internal/core`; `world` never imports `network`; `sim` never imports `network`, `client`, `render`, or `gfx`.
- Only `internal/gfx` may import the WebGPU binding.
- Untrusted messages and snapshot data return errors and never panic; invariant violations may panic.
- Worker panics are isolated with `recover`, logged with coordinates, and cannot terminate the process.
- Every implementation task follows red → green → refactor, runs its targeted tests, runs relevant race tests, and commits only its own files.
- Final 2560×1440 gates remain ≥100 fps, frame p99 <12 ms, RSS <2 GiB; server tick p99 <10 ms and no tick may reach 50 ms.

---

### Task 1: Move End-Neutral Block and Dimension Types into `core`

**Files:**
- Create: `internal/core/block.go`
- Create: `internal/core/block_test.go`
- Modify: `internal/world/palette.go`
- Modify: `internal/world/section.go`
- Modify: `internal/worldgen/generator.go`
- Modify: `internal/assets/blocks.go`
- Modify: tests that construct `world.BlockID`

**Interfaces:**
- Consumes: existing numeric IDs `0..5`
- Produces:
  - `type core.BlockID uint16`
  - `type core.DimensionID int32`
  - `type core.BlockFace uint8`
  - `type core.ChunkKey struct`
  - `type core.SectionKey struct`
  - canonical block ID constants and `core.Overworld`
  - compatibility alias `type world.BlockID = core.BlockID`

- [ ] **Step 1: Write tests for stable IDs and face opposites**

Create `internal/core/block_test.go`:

```go
package core_test

import (
	"testing"

	"minecraft-go/internal/core"
)

func TestCanonicalBlockIDsStayStable(t *testing.T) {
	got := []core.BlockID{
		core.AirID,
		core.BarrierID,
		core.StoneID,
		core.DirtID,
		core.GrassID,
		core.BedrockID,
	}
	for i, id := range got {
		if id != core.BlockID(i) {
			t.Fatalf("ID[%d] = %d，协议要求固定为 %d", i, id, i)
		}
	}
}

func TestBlockFaceOpposite(t *testing.T) {
	cases := []struct {
		in, want core.BlockFace
	}{
		{core.BlockFaceNegX, core.BlockFacePosX},
		{core.BlockFacePosX, core.BlockFaceNegX},
		{core.BlockFaceNegY, core.BlockFacePosY},
		{core.BlockFacePosY, core.BlockFaceNegY},
		{core.BlockFaceNegZ, core.BlockFacePosZ},
		{core.BlockFacePosZ, core.BlockFaceNegZ},
		{core.BlockFaceNone, core.BlockFaceNone},
	}
	for _, tc := range cases {
		if got := tc.in.Opposite(); got != tc.want {
			t.Fatalf("%v.Opposite() = %v，想要 %v", tc.in, got, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run the tests and verify they fail**

Run:

```bash
zsh -ic 'go test ./internal/core -run "TestCanonicalBlockIDsStayStable|TestBlockFaceOpposite" -v'
```

Expected: FAIL because `BlockID`, IDs, and `BlockFace` do not exist in `core`.

- [ ] **Step 3: Add canonical domain types**

Create `internal/core/block.go`:

```go
package core

type BlockID uint16
type DimensionID int32
type BlockFace uint8
type ChunkKey struct {
	Dimension DimensionID
	Pos       ChunkPos
}
type SectionKey struct {
	Dimension DimensionID
	Pos       SectionPos
}

const Overworld DimensionID = 0

const (
	AirID BlockID = iota
	BarrierID
	StoneID
	DirtID
	GrassID
	BedrockID
)

const (
	BlockFaceNegX BlockFace = iota
	BlockFacePosX
	BlockFaceNegY
	BlockFacePosY
	BlockFaceNegZ
	BlockFacePosZ
	BlockFaceNone BlockFace = 0xff
)

func (f BlockFace) Opposite() BlockFace {
	if f == BlockFaceNone {
		return BlockFaceNone
	}
	if f > BlockFacePosZ {
		panic("core: invalid BlockFace")
	}
	return f ^ 1
}
```

In `world`, replace the concrete definition with aliases:

```go
type BlockID = core.BlockID

const (
	AirID     = core.AirID
	BarrierID = core.BarrierID
)
```

In `worldgen`, keep source compatibility while making `core` canonical:

```go
const (
	IDStone   = core.StoneID
	IDDirt    = core.DirtID
	IDGrass   = core.GrassID
	IDBedrock = core.BedrockID
)
```

Update `assets.Registry` to switch on `core.StoneID`, `core.DirtID`, `core.GrassID`, and `core.BedrockID`; remove its `worldgen` import.

- [ ] **Step 4: Run the focused and full tests**

Run:

```bash
zsh -ic 'go test ./internal/core ./internal/world ./internal/worldgen ./internal/assets -race'
zsh -ic 'go test ./...'
```

Expected: PASS. Existing golden terrain hashes must remain unchanged because numeric IDs do not move.

- [ ] **Step 5: Commit**

```bash
git add internal/core/block.go internal/core/block_test.go internal/world internal/worldgen internal/assets
git commit -m "refactor: 下沉端无关方块与维度类型"
```

---

### Task 2: Implement Deterministic Voxel DDA Raycasting

**Files:**
- Create: `internal/core/raycast.go`
- Create: `internal/core/raycast_test.go`
- Create: `internal/core/raycast_fuzz_test.go`
- Create: `internal/core/raycast_bench_test.go`

**Interfaces:**
- Consumes: `core.BlockPos`, `core.BlockFace`
- Produces:

```go
type RayHit struct {
	Block    core.BlockPos
	Face     core.BlockFace
	Distance float32
	Point    mgl32.Vec3
}

func RaycastBlocks(
	origin, direction mgl32.Vec3,
	maxDistance float32,
	solid func(core.BlockPos) (bool, error),
) (core.RayHit, bool, error)
```

- [ ] **Step 1: Write table-driven ray tests**

Create `internal/core/raycast_test.go` with at least these exact cases:

```go
package core_test

import (
	"errors"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
)

func TestRaycastBlocksAxisAndNegativeCoordinates(t *testing.T) {
	solidAt := core.BlockPos{X: -3, Y: 5, Z: 2}
	hit, ok, err := core.RaycastBlocks(
		mgl32.Vec3{0.5, 5.5, 2.5},
		mgl32.Vec3{-1, 0, 0},
		6,
		func(p core.BlockPos) (bool, error) { return p == solidAt, nil },
	)
	if err != nil || !ok {
		t.Fatalf("RaycastBlocks = (%+v,%v,%v)，想要命中", hit, ok, err)
	}
	if hit.Block != solidAt || hit.Face != core.BlockFacePosX || hit.Distance != 2.5 {
		t.Fatalf("hit = %+v", hit)
	}
}

func TestRaycastBlocksOriginInsideSolid(t *testing.T) {
	hit, ok, err := core.RaycastBlocks(
		mgl32.Vec3{1.25, 2.5, 3.75}, mgl32.Vec3{0, 1, 0}, 6,
		func(p core.BlockPos) (bool, error) {
			return p == (core.BlockPos{X: 1, Y: 2, Z: 3}), nil
		},
	)
	if err != nil || !ok || hit.Distance != 0 || hit.Face != core.BlockFaceNone {
		t.Fatalf("起点命中 = (%+v,%v,%v)", hit, ok, err)
	}
}

func TestRaycastBlocksRejectsInvalidInputs(t *testing.T) {
	lookup := func(core.BlockPos) (bool, error) { return false, nil }
	for _, tc := range []struct {
		name string
		o, d mgl32.Vec3
		max  float32
	}{
		{"NaN origin", mgl32.Vec3{float32(math.NaN()), 0, 0}, mgl32.Vec3{1, 0, 0}, 6},
		{"zero direction", mgl32.Vec3{}, mgl32.Vec3{}, 6},
		{"non-positive max", mgl32.Vec3{}, mgl32.Vec3{1, 0, 0}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := core.RaycastBlocks(tc.o, tc.d, tc.max, lookup); err == nil {
				t.Fatal("想要输入错误")
			}
		})
	}
}

func TestRaycastBlocksPropagatesUnavailableCell(t *testing.T) {
	want := errors.New("chunk not ready")
	_, _, err := core.RaycastBlocks(
		mgl32.Vec3{0.5, 0.5, 0.5}, mgl32.Vec3{1, 0, 0}, 6,
		func(p core.BlockPos) (bool, error) {
			if p.X == 2 {
				return false, want
			}
			return false, nil
		},
	)
	if !errors.Is(err, want) {
		t.Fatalf("err = %v，想要 %v", err, want)
	}
}
```

- [ ] **Step 2: Run and verify red**

```bash
zsh -ic 'go test ./internal/core -run Raycast -v'
```

Expected: compile failure because `RaycastBlocks` and `RayHit` are undefined.

- [ ] **Step 3: Implement the DDA**

Create `internal/core/raycast.go`. The implementation must:

1. reject non-finite inputs and direction length `<1e-6`;
2. normalize direction;
3. floor origin to the starting `BlockPos`;
4. test the starting cell with `BlockFaceNone`;
5. calculate `step`, `tDelta`, and first `tMax` independently per axis;
6. use X, then Y, then Z priority for equal `tMax`;
7. propagate lookup errors unchanged;
8. stop before testing a cell whose entry distance exceeds `maxDistance`.

The central loop must have this concrete shape:

```go
for {
	axis := 0
	if tMax[1] < tMax[axis] {
		axis = 1
	}
	if tMax[2] < tMax[axis] {
		axis = 2
	}
	distance := tMax[axis]
	if distance > maxDistance {
		return RayHit{}, false, nil
	}
	cell[axis] += step[axis]
	tMax[axis] += tDelta[axis]
	face := entryFace(axis, step[axis])
	pos := BlockPos{X: cell[0], Y: cell[1], Z: cell[2]}
	occupied, err := solid(pos)
	if err != nil {
		return RayHit{}, false, err
	}
	if occupied {
		return RayHit{
			Block:    pos,
			Face:     face,
			Distance: distance,
			Point:    origin.Add(direction.Mul(distance)),
		}, true, nil
	}
}
```

- [ ] **Step 4: Add a fuzz/property test**

Create `internal/core/raycast_fuzz_test.go`. Seed positive and negative origins and assert for finite inputs:

- returned distance is within `[0,maxDistance]`;
- returned block is reported solid by the lookup;
- the call never panics.

Limit generated coordinates to `[-1024,1024]` and max distance to `[0.01,32]` so fuzzing remains bounded.

- [ ] **Step 5: Run tests and benchmark**

```bash
zsh -ic 'go test ./internal/core -run Raycast -race -v'
zsh -ic 'go test ./internal/core -fuzz FuzzRaycastBlocks -fuzztime=10s'
zsh -ic 'go test ./internal/core -run "^$" -bench BenchmarkRaycastBlocks -benchmem'
```

Add `BenchmarkRaycastBlocks` before running the last command; it should raycast through a fixed sparse 16×16×16 fixture and report zero allocations after warm-up.

- [ ] **Step 6: Commit**

```bash
git add internal/core/raycast.go internal/core/raycast_test.go internal/core/raycast_fuzz_test.go internal/core/raycast_bench_test.go
git commit -m "feat: 确定性体素 DDA 射线拾取"
```

---

### Task 3: Add Validated Paletted Snapshots, Chunk Cloning, and Logical Hashes

**Files:**
- Create: `internal/world/snapshot.go`
- Create: `internal/world/snapshot_test.go`
- Modify: `internal/world/chunk.go`
- Modify: `internal/world/chunk_test.go`
- Modify: `internal/world/palette.go`

**Interfaces:**
- Consumes: current three-state `PalettedContainer`
- Produces:

```go
type StorageKind uint8
type ContainerSnapshot struct {
	Kind    StorageKind
	Single  core.BlockID
	Bits    uint8
	Palette []core.BlockID
	Packed  []uint64
}

func (c *PalettedContainer) Snapshot() ContainerSnapshot
func NewPalettedContainerFromSnapshot(ContainerSnapshot) (*PalettedContainer, error)
func (c *Chunk) Clone() *Chunk
func (c *Chunk) Hash() [32]byte
func ChunkBlockIndex(core.BlockPos) (uint32, bool)
func BlockPosFromChunkIndex(core.ChunkPos, uint32) (core.BlockPos, bool)
```

- [ ] **Step 1: Write round-trip and rejection tests**

Create tests that build:

- a single all-air container;
- a 4-bit indexed container;
- an 8-bit indexed container with 17 IDs;
- a direct container with 257 IDs.

For each, snapshot → import → compare all 4096 logical values. Also assert:

```go
func TestContainerSnapshotDoesNotAliasSource(t *testing.T) {
	c := world.NewPalettedContainer(core.AirID)
	c.Set(1, 2, 3, core.StoneID)
	snapshot := c.Snapshot()
	c.Set(1, 2, 3, core.DirtID)
	restored, err := world.NewPalettedContainerFromSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if got := restored.Get(1, 2, 3); got != core.StoneID {
		t.Fatalf("restored = %d，想要 stone", got)
	}
}
```

Malformed cases must include illegal bits, short packed data, palette index out of range, duplicate/empty indexed palette, BlockIDs exceeding 15 bits in single/indexed data, and non-zero unused high bits in packed words.

- [ ] **Step 2: Verify red**

```bash
zsh -ic 'go test ./internal/world -run "Snapshot|ChunkBlockIndex|ChunkHash" -v'
```

Expected: compile failure because snapshot APIs do not exist.

- [ ] **Step 3: Implement snapshot export/import**

Expose public `StorageSingle`, `StorageIndexed`, and `StorageDirect` constants while leaving mutation internals private. `Snapshot()` must clone `Palette` and `Packed`. Import must validate the complete snapshot into temporary values before constructing a container.

For indexed data, scan all 4096 raw slots during validation and reject any index `>=len(Palette)`. Never install partially validated slices.

- [ ] **Step 4: Implement chunk clone, hash, and compact index conversion**

`Chunk.Clone` deep-clones all 24 sections. `Chunk.Hash` writes logical BlockIDs in deterministic section/Y/Z/X order into SHA-256; it must not hash palette layout or map order.

Use this compact index:

```go
index := uint32(sectionIndex*core.BlocksPerSection + localY*256 + localZ*16 + localX)
```

Valid values are `0..98_303`. `ChunkBlockIndex` rejects out-of-height positions; `BlockPosFromChunkIndex` rejects out-of-range indices and reconstructs X/Z from the supplied chunk coordinate.

- [ ] **Step 5: Run correctness and allocation tests**

```bash
zsh -ic 'go test ./internal/world -race -v'
zsh -ic 'go test ./internal/world -run "^$" -bench "Benchmark(Export|Import)ChunkSnapshot" -benchmem'
```

Add export/import benchmarks using a fixed generated chunk. Snapshot import/export may allocate for ownership isolation; record the expected bytes rather than forcing zero allocations.

- [ ] **Step 6: Commit**

```bash
git add internal/world/snapshot.go internal/world/snapshot_test.go internal/world/chunk.go internal/world/chunk_test.go internal/world/palette.go
git commit -m "feat: 可验证调色板快照与区块逻辑哈希"
```

---

### Task 4: Define the Transport-Neutral Message Protocol

**Files:**
- Create: `internal/network/message.go`
- Create: `internal/network/message_test.go`
- Create: `internal/network/snapshot.go`
- Create: `internal/network/snapshot_test.go`
- Modify: `internal/archcheck/deps_test.go`

**Interfaces:**
- Consumes: `core` IDs, coordinates, faces, and vectors
- Produces:
  - sealed `ClientMessage` and `ServerMessage` interfaces
  - all M2A command/event structs from the design
  - `SectionData.Validate()`, `ChunkSnapshot.Validate()`, and `BlockChanges.Validate()`
  - stable `RejectReason` values

- [ ] **Step 1: Write protocol shape and validation tests**

Tests must compile against these concrete types:

```go
clientMessages := []network.ClientMessage{
	network.SetViewCenter{Sequence: 1, Dimension: core.Overworld},
	network.BreakRay{Sequence: 2, Direction: mgl32.Vec3{0, -1, 0}},
	network.PlaceRay{Sequence: 3, Direction: mgl32.Vec3{0, -1, 0}, Block: core.StoneID},
	network.RequestChunkResync{Sequence: 4, HaveRevision: 7},
}
serverMessages := []network.ServerMessage{
	network.ChunkSnapshot{},
	network.BlockChanges{},
	network.ForgetChunks{},
	network.CommandRejected{Reason: network.RejectInvalidRay},
}
```

Validation tests must reject:

- section count other than 24;
- duplicate or missing Y;
- illegal storage kind or bits;
- wrong packed word count;
- out-of-range indexed slots;
- BlockIDs above 32767 and non-zero unused packed bits;
- block changes outside the message's chunk or world height.

- [ ] **Step 2: Run and verify red**

```bash
zsh -ic 'go test ./internal/network -v'
```

Expected: package does not exist.

- [ ] **Step 3: Implement sealed message types**

Use unexported marker methods:

```go
type ClientMessage interface{ clientMessage() }
type ServerMessage interface{ serverMessage() }
```

Define every field exactly as in the approved design. `RejectReason` is a string-backed type with:

```go
const (
	RejectInvalidRay     RejectReason = "invalid_ray"
	RejectNoTarget       RejectReason = "no_target"
	RejectChunkNotReady  RejectReason = "chunk_not_ready"
	RejectProtectedBlock RejectReason = "protected_block"
	RejectInvalidBlock   RejectReason = "invalid_block"
	RejectOccupied       RejectReason = "occupied"
)
```

- [ ] **Step 4: Implement snapshot validation**

Duplicate the wire/storage constraints in `network` without importing `world`. Keep validation helpers private. `SectionData.PayloadBytes()` must return:

```go
len(s.Palette)*2 + len(s.Packed)*8
```

Single sections return 2 bytes for the single BlockID. `ChunkSnapshot.PayloadBytes()` sums all sections.

- [ ] **Step 5: Extend the architecture gate**

Add:

```go
"internal/network": {"internal/core"},
```

Do not add `world`, `server`, or `client` to the network allowlist.

- [ ] **Step 6: Run tests and commit**

```bash
zsh -ic 'go test ./internal/network ./internal/archcheck -race -v'
zsh -ic 'go test ./...'
git add internal/network internal/archcheck/deps_test.go
git commit -m "feat: 定义 M2A 端无关消息协议"
```

---

### Task 5: Implement the Bounded In-Memory Transport

**Files:**
- Create: `internal/network/transport.go`
- Create: `internal/network/memory.go`
- Create: `internal/network/memory_test.go`

**Interfaces:**
- Consumes: sealed protocol messages
- Produces:

```go
var ErrClosed = errors.New("network: transport closed")

type ClientEndpoint interface {
	Send(context.Context, ClientMessage) error
	Recv(context.Context) (ServerMessage, error)
	Close() error
}

type ServerEndpoint interface {
	Send(context.Context, ServerMessage) error
	Recv(context.Context) (ClientMessage, error)
	Close() error
}

func NewMemoryPair(capacity int) (ClientEndpoint, ServerEndpoint)
```

- [ ] **Step 1: Write ordering, backpressure, cancellation, and close tests**

Required tests:

1. client→server and server→client preserve 100-message order;
2. capacity 1 blocks the second send until receive;
3. canceled context releases a blocked send;
4. either endpoint close wakes both directions;
5. queued messages drain before `ErrClosed`;
6. 100 concurrent `Close` calls do not panic under `-race`.

The close wakeup test must use a timeout:

```go
ctx, cancel := context.WithTimeout(context.Background(), time.Second)
defer cancel()
_, err := server.Recv(ctx)
if !errors.Is(err, network.ErrClosed) {
	t.Fatalf("Recv after peer close = %v", err)
}
```

- [ ] **Step 2: Run and verify failures**

```bash
zsh -ic 'go test ./internal/network -run "Memory|Transport" -race -v'
```

Expected: compile failure because endpoints are undefined.

- [ ] **Step 3: Implement paired directional pipes**

Use a shared `done` channel closed by `sync.Once`, but never close the data channels. Each receive first attempts a non-blocking queue drain, then waits on data/context/done; after done it performs one final drain before `ErrClosed`.

Treat a send that entered the channel concurrently with `Close` as linearized before close. Do not recover from send-on-closed because data channels are never closed.

Reject `capacity < 1` with a panic: it is a constructor invariant, not untrusted network data.

- [ ] **Step 4: Run stress tests**

```bash
zsh -ic 'go test ./internal/network -race -count=100'
```

Expected: PASS without deadlock or goroutine leak.

- [ ] **Step 5: Commit**

```bash
git add internal/network/transport.go internal/network/memory.go internal/network/memory_test.go
git commit -m "feat: 有界可关闭内存传输"
```

---

### Task 6: Build Authoritative Dimension State, Lifecycle, Revisions, and Overlay

**Files:**
- Create: `internal/sim/world.go`
- Create: `internal/sim/world_test.go`
- Create: `internal/sim/overlay.go`
- Create: `internal/sim/overlay_test.go`
- Modify: `internal/archcheck/deps_test.go`

**Interfaces:**
- Consumes: generated `*world.Chunk` ownership transfers
- Produces:

```go
type ChunkState uint8
const (
	ChunkAbsent ChunkState = iota
	ChunkGenerating
	ChunkReady
	ChunkFailed
	ChunkUnloading
)

type ChunkRecord struct {
	State    ChunkState
	Chunk    *world.Chunk
	Revision uint64
	Err      error
}

type ChunkInfo struct {
	State    ChunkState
	Revision uint64
	Err      error
}

type BaseBlockLookup func(core.BlockPos) core.BlockID

type Dimension struct
func NewDimension(id core.DimensionID, base BaseBlockLookup) *Dimension
func (d *Dimension) BeginGeneration(core.ChunkPos) bool
func (d *Dimension) ApplyGenerated(core.ChunkPos, *world.Chunk) error
func (d *Dimension) MarkFailed(core.ChunkPos, error)
func (d *Dimension) Unload(core.ChunkPos) error
func (d *Dimension) Info(core.ChunkPos) (ChunkInfo, bool)
func (d *Dimension) CloneReadyChunk(core.ChunkPos) (*world.Chunk, uint64, bool)
func (d *Dimension) BlockAt(core.BlockPos) (core.BlockID, bool)
func (d *Dimension) SetBlock(core.BlockPos, core.BlockID) (old core.BlockID, changed bool, err error)
func (d *Dimension) OverlayEntries(core.ChunkPos) int
```

- [ ] **Step 1: Write lifecycle transition tests**

Cover the exact legal transitions:

```text
Absent→Generating→Ready→Unloading→Absent
Generating→Failed
Failed→Generating
```

Assert illegal transitions panic. `ApplyGenerated` must set revision 1 and take ownership of a clone-free generated chunk.

- [ ] **Step 2: Write overlay reload tests**

Generate a fixed chunk, modify one block to air and another to stone, unload, regenerate the base, and apply it again. Assert both modifications survive.

Then set each position back to `BaseBlockLookup(pos)` and assert `OverlayEntries(chunk)==0`.

- [ ] **Step 3: Run and verify red**

```bash
zsh -ic 'go test ./internal/sim -run "Lifecycle|Overlay" -v'
```

Expected: package does not exist.

- [ ] **Step 4: Implement the state and overlay**

Store overlays separately from loaded records:

```go
records  map[core.ChunkPos]*ChunkRecord
overlays map[core.ChunkPos]map[uint32]core.BlockID
```

`ApplyGenerated` applies overlay indices in ascending numeric order before setting Ready. `SetBlock` requires Ready, updates the loaded chunk, and either writes the final value into the overlay or deletes it when it equals `BaseBlockLookup`.

Do not put mutexes in `Dimension`; single-writer ownership is enforced by `Engine`.

- [ ] **Step 5: Add the dependency gate and run race tests**

Add:

```go
"internal/sim": {"internal/core", "internal/world"},
```

Run:

```bash
zsh -ic 'go test ./internal/sim ./internal/archcheck -race -v'
```

- [ ] **Step 6: Commit**

```bash
git add internal/sim internal/archcheck/deps_test.go
git commit -m "feat: 权威区块生命周期与会话覆盖层"
```

---

### Task 7: Implement the Deterministic 20 TPS Engine and Block Commands

**Files:**
- Create: `internal/sim/engine.go`
- Create: `internal/sim/engine_test.go`
- Create: `internal/sim/command.go`
- Create: `internal/sim/interaction_test.go`
- Create: `internal/sim/bench_test.go`

**Interfaces:**
- Consumes: `Dimension`, `core.RaycastBlocks`
- Produces:

```go
type SessionID uint64
type CommandKind uint8
const (
	CommandSetViewCenter CommandKind = iota
	CommandBreakRay
	CommandPlaceRay
	CommandResync
)

type RejectReason uint8
const (
	RejectInvalidRay RejectReason = iota
	RejectNoTarget
	RejectChunkNotReady
	RejectProtectedBlock
	RejectInvalidBlock
	RejectOccupied
)

type Command struct {
	Session   SessionID
	Sequence  uint64
	Kind      CommandKind
	Dimension core.DimensionID
	Center    core.ChunkPos
	Origin    mgl32.Vec3
	Direction mgl32.Vec3
	Block     core.BlockID
}

type GeneratedChunk struct {
	Dimension core.DimensionID
	Pos       core.ChunkPos
	Chunk     *world.Chunk
	Err       error
}

type ChunkChangeBatch struct {
	Dimension    core.DimensionID
	Chunk        core.ChunkPos
	BaseRevision uint64
	NewRevision  uint64
	Changes      []BlockChange
}

type BlockChange struct {
	Position core.BlockPos
	Block    core.BlockID
}

type Rejection struct {
	Session  SessionID
	Sequence uint64
	Reason   RejectReason
}

type TickResult struct {
	Generate   []core.ChunkKey
	Forget     map[SessionID][]core.ChunkKey
	Ready      []core.ChunkKey
	Changes    []ChunkChangeBatch
	Rejected   []Rejection
	Tick       uint64
}

func NewEngine(base BaseBlockLookup, viewRadius int) *Engine
func (e *Engine) Enqueue(Command)
func (e *Engine) SubmitGenerated(GeneratedChunk)
func (e *Engine) Step() TickResult
func (e *Engine) Run(context.Context, Clock) error
func (e *Engine) CloneReadyChunk(core.ChunkKey) (*world.Chunk, uint64, bool)
func (e *Engine) ChunkHash(core.ChunkKey) ([32]byte, uint64, bool)
```

- [ ] **Step 1: Test sequence ordering and deduplication**

Enqueue sequence 3, 1, 2 for one session in the same tick. Use three writes to the same block and assert sequence 3 wins after stable sort. Re-enqueue sequence 3 next tick and assert it is ignored.

- [ ] **Step 2: Test tick revision batching**

Load a Ready test chunk, execute three real modifications in one tick, and assert:

- one `ChunkChangeBatch`;
- `BaseRevision == 1`;
- `NewRevision == 2`;
- changes sorted by compact block index;
- the chunk record revision is 2.

Commands writing the existing value must produce no batch and no revision increment.

- [ ] **Step 3: Test break/place validation**

Use a flat test chunk and rays with known targets. Required assertions:

- stone breaks to air;
- bedrock returns `RejectProtectedBlock`;
- invalid direction returns `RejectInvalidRay`;
- unloaded traversal returns `RejectChunkNotReady`;
- placement whitelist rejects BarrierID;
- placement into occupied target rejects `RejectOccupied`;
- valid stone/dirt/grass placements succeed.

- [ ] **Step 4: Run and verify red**

```bash
zsh -ic 'go test ./internal/sim -run "Engine|Revision|Break|Place" -v'
```

- [ ] **Step 5: Implement fixed Step ordering**

`Step` must:

1. snapshot queued commands and generated results;
2. stable-sort commands by `(Session,Sequence)`;
3. update subscription centers;
4. sort generated results by `(Dimension,X,Z)` and apply;
5. run interactions in command order;
6. compact dirty sections once;
7. build change batches and increment each touched chunk once;
8. return deterministic slices;
9. increment `Tick`.

Map `core.RaycastBlocks` lookup errors to the domain rejection enum declared in `sim`, then let `server` map it to `network.RejectReason`.

- [ ] **Step 6: Implement production clock behavior**

Define:

```go
type Clock interface {
	C() <-chan time.Time
	Stop()
}
```

Production uses a 50 ms ticker. `Run` executes at most five catch-up steps, logs lag, then rebases. It never starts overlapping `Step` calls.

- [ ] **Step 7: Add deterministic replay and benchmarks**

Run the same synchronous generated fixture and command script twice; compare dimension logical hash, overlays, revisions, and tick count.

Add:

- `BenchmarkEngineStepIdle`
- `BenchmarkEngineStepBlockChanges`

- [ ] **Step 8: Verify**

```bash
zsh -ic 'go test ./internal/sim -race -v'
zsh -ic 'go test ./internal/sim -run "^$" -bench BenchmarkEngineStep -benchmem'
```

- [ ] **Step 9: Commit**

```bash
git add internal/sim
git commit -m "feat: 确定性 20 TPS 权威命令循环"
```

---

### Task 8: Add Server Generation Workers and Subscription Orchestration

**Files:**
- Create: `internal/server/server.go`
- Create: `internal/server/config.go`
- Create: `internal/server/generator.go`
- Create: `internal/server/generator_test.go`
- Create: `internal/server/subscription_test.go`
- Modify: `internal/worldgen/generator.go`
- Modify: `internal/worldgen/generator_test.go`
- Modify: `internal/archcheck/deps_test.go`

**Interfaces:**
- Consumes: `network.ServerEndpoint`, `sim.Engine`, `worldgen.Generator`
- Produces:

```go
type Generator interface {
	GenerateChunk(core.ChunkPos) *world.Chunk
	BaseBlockAt(core.BlockPos) core.BlockID
}

type Config struct {
	Seed               int64
	ViewRadius         int
	Workers            int
	SnapshotChunks     int
	SnapshotBytes      int
	OutboxCapacity     int
}

func DefaultConfig(seed int64) Config
func New(Config, network.ServerEndpoint, Generator) *Server
func (s *Server) Step() sim.TickResult
func (s *Server) Run(context.Context) error
func (s *Server) Close()
```

- [ ] **Step 1: Add `BaseBlockAt` parity tests**

For fixed positive and negative world positions, assert:

```go
chunk := generator.GenerateChunk(pos.Chunk())
x, _, z := pos.Local()
if got, want := generator.BaseBlockAt(pos), chunk.BlockAt(x, pos.Y, z); got != want {
	t.Fatalf("BaseBlockAt(%+v) = %d，GenerateChunk = %d", pos, got, want)
}
```

Test air above terrain, grass surface, dirt layer, stone, and bedrock.

- [ ] **Step 2: Test subscription generation order and bounds**

Use `ViewRadius: 1`, send `SetViewCenter{Center:{0,0}}`, and assert generation requests cover exactly a 3×3 square in distance/X/Z order. A later center move must cancel stale queued markers and request only missing chunks.

Production `DefaultConfig` must return radius 33; client messages contain no radius.

- [ ] **Step 3: Test panic isolation**

Inject a generator that panics at `(1,1)`. Assert that chunk becomes Failed while at least five other chunks become Ready and workers keep running.

- [ ] **Step 4: Run and verify red**

```bash
zsh -ic 'go test ./internal/server ./internal/worldgen -run "BaseBlockAt|Generation|Subscription|Panic" -v'
```

- [ ] **Step 5: Implement generator pool and server adapter**

Use bounded job/result channels and a fixed worker count. Each task has a `recover` boundary that turns panic into `sim.GeneratedChunk{Err:...}`. Workers never mutate `sim.Engine`.

The endpoint reader translates network messages into `sim.Command` without making `sim` import `network`. It coalesces pending `SetViewCenter` messages to the newest center while preserving interaction and resync messages.

- [ ] **Step 6: Add dependency whitelist**

```go
"internal/server": {
	"internal/core", "internal/network", "internal/world",
	"internal/worldgen", "internal/sim",
},
```

- [ ] **Step 7: Verify and commit**

```bash
zsh -ic 'go test ./internal/server ./internal/worldgen ./internal/archcheck -race -v'
git add internal/server internal/worldgen internal/archcheck/deps_test.go
git commit -m "feat: 服务端区块生成与订阅调度"
```

---

### Task 9: Publish Budgeted Snapshots, Deltas, Resyncs, and Forget Events

**Files:**
- Create: `internal/server/snapshot.go`
- Create: `internal/server/snapshot_test.go`
- Create: `internal/server/session.go`
- Create: `internal/server/session_test.go`
- Modify: `internal/server/server.go`

**Interfaces:**
- Consumes: `sim.TickResult`, authoritative `world.Chunk`
- Produces:
  - strict FIFO session outbox
  - world→network snapshot conversion
  - initial snapshot state per subscribed chunk
  - revision-aware delta publication and resync priority

- [ ] **Step 1: Write snapshot conversion tests**

Generate a chunk, build `network.ChunkSnapshot`, mutate the authoritative chunk, and import the already-built message. Assert it retains the old logical hash.

Assert all 24 sections are Y-sorted and `Validate()` passes.

- [ ] **Step 2: Write budget/order tests**

With a small fixture:

- budget 2 chunks/tick publishes the two nearest chunks;
- byte budget stops before exceeding the limit, except for one oversized first chunk;
- a chunk's snapshot always precedes its first delta;
- a chunk leaving range is removed from pending snapshots before `ForgetChunks`;
- resync goes ahead of ordinary pending snapshots;
- `ForgetChunks.Chunks` is X/Z sorted.

- [ ] **Step 3: Write slow-consumer tests**

Use outbox capacity 1 and a blocked endpoint writer. A second enqueue must close the session, return from `Server.Step`, log the slow consumer, and leave the engine ticking.

- [ ] **Step 4: Run and verify red**

```bash
zsh -ic 'go test ./internal/server -run "Snapshot|Budget|Resync|Forget|SlowConsumer" -race -v'
```

- [ ] **Step 5: Implement ownership-safe conversion**

Convert each `world.ContainerSnapshot` into a newly owned `network.SectionData`; do not use unsafe slice conversion. Track per-session chunk publication state:

```go
type publication struct {
	snapshotSent bool
	lastRevision uint64
	resyncQueued bool
}
```

If a Ready chunk changes before its first snapshot is sent, snapshot the latest revision and suppress older deltas. Once sent, only publish contiguous deltas.

- [ ] **Step 6: Implement the outbox writer**

Capacity defaults to 512. Tick uses a non-blocking enqueue; full closes the session rather than dropping a message. Writer sends serially and listens for context cancellation and endpoint closure.

- [ ] **Step 7: Verify and commit**

```bash
zsh -ic 'go test ./internal/server ./internal/network -race -count=20'
git add internal/server
git commit -m "feat: 预算化区块快照与增量发布"
```

---

### Task 10: Implement the Revisioned Client Mirror

**Files:**
- Create: `internal/client/mirror.go`
- Create: `internal/client/mirror_test.go`
- Create: `internal/client/snapshot.go`
- Create: `internal/client/snapshot_test.go`
- Modify: `internal/archcheck/deps_test.go`

**Interfaces:**
- Consumes: `network.ChunkSnapshot`, `network.BlockChanges`, `network.ForgetChunks`
- Produces:

```go
type MirrorChunk struct {
	Revision uint64
	Chunk    *world.Chunk
	Desynced bool
}

type Mirror struct
func NewMirror() *Mirror
func (m *Mirror) Apply(network.ServerMessage) (MirrorUpdate, error)
func (m *Mirror) Chunk(core.DimensionID, core.ChunkPos) (*MirrorChunk, bool)
func (m *Mirror) BlockAt(core.DimensionID, core.BlockPos) (core.BlockID, bool)
func (m *Mirror) Hash(core.DimensionID, core.ChunkPos) ([32]byte, uint64, bool)
```

```go
type MirrorUpdate struct {
	Dirty     []core.SectionKey
	Forgotten []core.SectionKey
	Resync    *network.RequestChunkResync
	Rejected  *network.CommandRejected
}
```

`MirrorUpdate` returns dimension-aware dirty/forgotten section keys, one optional resync request, and command rejection metadata for the app. `Apply` returns a validation error for malformed or unsupported server data and leaves the previous mirror state untouched.

- [ ] **Step 1: Test atomic snapshot import**

Import a legal snapshot and assert all logical blocks/revision. Then alter one section to invalid packed data and apply it; assert the previous mirror chunk is unchanged.

- [ ] **Step 2: Test revision behavior**

Required sequence:

1. snapshot revision 3;
2. delta `3→4` applies;
3. duplicate delta `3→4` is ignored;
4. gap `5→6` marks desynced and returns one resync request;
5. another gap returns no second request;
6. snapshot revision 7 replaces data and clears desynced.

- [ ] **Step 3: Test dirty and forget results**

Changing a block at local `(15,15,15)` must dirty the containing section plus all loaded adjacent sections reached by the surrounding 3×3×3 block cube. `ForgetChunks` returns all 24 section positions for renderer removal, removes mirror data, and dirties every still-loaded section touching the forgotten chunk boundary.

- [ ] **Step 4: Run and verify red**

```bash
zsh -ic 'go test ./internal/client -run "Mirror|Snapshot|Revision|Dirty" -v'
```

- [ ] **Step 5: Implement validated conversion and apply**

Convert all 24 `network.SectionData` values to temporary `world.ContainerSnapshot` values and validate/import the complete chunk before replacing the old mirror entry.

Use nested maps by `DimensionID`; M2A commands use `core.Overworld`, but do not erase dimension from APIs.

- [ ] **Step 6: Update architecture gate**

Add `internal/network` to the client allowlist and keep `worldgen` temporarily until Task 13 removes the old Streamer.

- [ ] **Step 7: Verify and commit**

```bash
zsh -ic 'go test ./internal/client ./internal/network -race -v'
git add internal/client/mirror.go internal/client/mirror_test.go internal/client/snapshot.go internal/client/snapshot_test.go internal/archcheck/deps_test.go
git commit -m "feat: revision 驱动的客户端只读镜像"
```

---

### Task 11: Replace Generation Streaming with Revision-Stamped Meshing

**Files:**
- Create: `internal/client/mesher.go`
- Create: `internal/client/mesher_test.go`
- Modify: `internal/client/streamer_test.go` during migration
- Modify: `internal/world/neighborhood.go` only if a constructor from cloned sections is needed

**Interfaces:**
- Consumes: main-thread-owned `Mirror`, dirty `SectionPos`
- Produces:

```go
type ChunkStamp struct {
	Dimension core.DimensionID
	Chunk     core.ChunkPos
	Present   bool
	Revision  uint64
}

type MeshedSection struct {
	Dimension core.DimensionID
	Pos       core.SectionPos
	Quads     []mesh.Quad
	Conn      mesh.Connectivity
	Stamps    []ChunkStamp
}

type Mesher struct
func NewMesher(*assets.Registry, int) *Mesher
func (m *Mesher) MarkDirty(...core.SectionKey)
func (m *Mesher) ForgetChunk(core.DimensionID, core.ChunkPos)
func (m *Mesher) Schedule(*Mirror, int)
func (m *Mesher) Drain(*Mirror, int) []MeshedSection
func (m *Mesher) Stats() MesherStats
func (m *Mesher) Close()
```

- [ ] **Step 1: Write initial and boundary remesh tests**

Load a 3×3 chunk neighborhood into Mirror, mark the center chunk's 24 sections dirty, schedule, and wait for 24 valid results.

Modify a corner block and assert all affected loaded section positions are scheduled, not only the center section.

- [ ] **Step 2: Write stale-result tests**

Schedule a job at revision 1, block its worker with a test hook, update one input neighbor to revision 2, then release the worker. `Drain` must discard the stale result and leave/re-add the center to dirty.

- [ ] **Step 3: Write panic and close tests**

Inject one panicking job and assert later jobs still complete. Fill the result queue, call `Close`, and assert it returns within one second.

- [ ] **Step 4: Run and verify red**

```bash
zsh -ic 'go test ./internal/client -run "Mesher|Remesh|Stale" -race -v'
```

- [ ] **Step 5: Implement immutable neighborhood jobs**

On the main thread, clone only the center section and its 26 adjacent sections into `world.Neighborhood`. Record the revision/presence of the at-most-nine horizontal chunks that own them.

Workers call only:

```go
mesh.MeshSection(job.neighborhood, registry)
mesh.ComputeConnectivity(job.neighborhood.Center, registry)
```

At drain time compare every stamp against Mirror. A mismatch discards and re-dirties; a match clears the matching dirty generation.

- [ ] **Step 6: Preserve bounded scheduling**

Use fixed worker count, bounded jobs/results, a queued/in-flight set, near-first scheduling, panic isolation, and closed-channel selects. Do not run meshing synchronously from `MarkDirty`.

- [ ] **Step 7: Verify**

```bash
zsh -ic 'go test ./internal/client -race -count=20'
zsh -ic 'go test ./internal/client -run "^$" -bench BenchmarkRemeshBoundaryEdit -benchmem'
```

- [ ] **Step 8: Commit**

```bash
git add internal/client/mesher.go internal/client/mesher_test.go internal/world/neighborhood.go
git commit -m "feat: revision 校验的客户端增量网格调度"
```

---

### Task 12: Add the Headless End-to-End Authoritative Consistency Test

**Files:**
- Create: `internal/server/integration_test.go`
- Create: `internal/server/test_generator_test.go`
- Modify: `internal/server/server.go`
- Modify: `internal/client/mirror.go` if a test-visible subscription hash helper is needed

**Interfaces:**
- Consumes: real `MemoryTransport`, `server.Server`, `client.Mirror`
- Produces: a deterministic end-to-end proof of the M2A consistency contract

- [ ] **Step 1: Write the complete scripted test**

Use `DefaultConfig` overridden to radius 1, synchronous generation, and explicit `Server.Step` calls:

1. send `SetViewCenter{Sequence:1, Center:{0,0}}`;
2. step until center snapshot revision 1 reaches Mirror;
3. break a known surface grass block with sequence 2;
4. place stone, dirt, grass at three distinct known-air targets with sequences 3, 4, 5;
5. assert each authoritative delta reaches Mirror contiguously;
6. move center far enough to unload with sequence 6;
7. assert `ForgetChunks` removes the original mirror chunk;
8. move back with sequence 7;
9. assert regenerated+overlay snapshot contains all edits;
10. compare authoritative and mirror hashes/revisions;
11. cancel and assert all transport/server goroutines exit.

- [ ] **Step 2: Run and verify red**

```bash
zsh -ic 'go test ./internal/server -run TestAuthoritativeInteractionRoundTrip -race -v'
```

Expected: FAIL until server publication, mirror application, and test stepping are correctly connected.

- [ ] **Step 3: Add only the missing orchestration hooks**

Expose deterministic test stepping without exposing mutable world pointers:

```go
func (s *Server) StepForTest() sim.TickResult
func (s *Server) ChunkHash(dim core.DimensionID, pos core.ChunkPos) ([32]byte, uint64, bool)
```

`ChunkHash` returns values, never a `*world.Chunk`.

- [ ] **Step 4: Run repeatedly**

```bash
zsh -ic 'go test ./internal/server -run TestAuthoritativeInteractionRoundTrip -race -count=50'
```

Expected: identical hashes and no flake.

- [ ] **Step 5: Commit**

```bash
git add internal/server/integration_test.go internal/server/test_generator_test.go internal/server/server.go internal/client/mirror.go
git commit -m "test: 锁定内置服务端端到端世界一致性"
```

---

### Task 13: Wire the Embedded Server, Mirror, Mesher, and Mouse Controls into `mcgo`

**Files:**
- Modify: `internal/client/window.go`
- Modify: `cmd/mcgo/app.go`
- Modify: `cmd/mcgo/main.go`
- Modify: `cmd/mcgo/benchmark.go`
- Delete: `internal/client/streamer.go`
- Delete: `internal/client/streamer_test.go`
- Modify: `internal/archcheck/deps_test.go`

**Interfaces:**
- Consumes: all prior M2A server/client components
- Produces: playable instant breaking and placement in the real Metal client

- [ ] **Step 1: Add window input tests or a small input-state unit**

GLFW itself is not headless-testable, so extract edge detection and block selection into a pure `client.InputState`:

```go
type Actions struct {
	Break         bool
	Place         bool
	SelectedBlock core.BlockID
}

func (s *InputState) Update(primary, secondary bool, number int) Actions
```

Test rising-edge-only clicks and `1/2/3` mapping before changing the window loop.

- [ ] **Step 2: Add secondary mouse and numeric keys**

Extend `Window` with:

```go
func (w *Window) SecondaryButtonDown() bool
```

Add `Key1`, `Key2`, and `Key3` to the existing key mapping.

- [ ] **Step 3: Replace application ownership**

`application` must own:

```go
clientEndpoint network.ClientEndpoint
server         *server.Server
serverCancel   context.CancelFunc
serverDone     chan error
mirror         *client.Mirror
mesher         *client.Mesher
sequence       uint64
selectedBlock  core.BlockID
```

Remove `streamer *client.Streamer` and the direct `worldgen.New` call from client setup. `newApplication` creates `network.NewMemoryPair(256)`, starts the embedded server with the seed, then sends initial `SetViewCenter`.

- [ ] **Step 4: Apply server messages before meshing each frame**

Each frame:

1. non-blocking drain a bounded number of server messages;
2. apply them to Mirror, logging and closing the embedded session on a validation error;
3. enqueue returned resync command;
4. mark dirty/forgotten sections in Mesher and Renderer;
5. schedule bounded mesh work;
6. drain valid mesh results into Renderer;
7. flush uploads and render.

Moving across a chunk sends a monotonic `SetViewCenter`; it no longer calls a client generator.

- [ ] **Step 5: Send interaction rays**

On click rising edge while cursor is captured:

```go
origin := app.camera.Pos
direction := app.camera.Forward()
```

Send `BreakRay` or `PlaceRay` with the next sequence. `1/2/3` update selection even when no click occurs. Do not mutate Mirror optimistically.

- [ ] **Step 6: Implement shutdown order**

Stop input, close the client endpoint, cancel/wait server, close Mesher, release renderer/GPU/window. Every wait has context cancellation; no fixed sleeps.

- [ ] **Step 7: Delete the old Streamer and tighten dependencies**

Delete the generation+meshing Streamer only after the app compiles and tests pass. Remove `internal/worldgen` from the client architecture allowlist and add `internal/network`.

Run:

```bash
rg -n "worldgen|GenerateChunk" internal/client cmd/mcgo
```

Expected: `internal/client` has no match; `cmd/mcgo` may refer only to the server seed/config, never call `GenerateChunk`.

- [ ] **Step 8: Run full automated validation**

```bash
zsh -ic 'go test ./... -race'
zsh -ic 'go vet ./...'
test -z "$(gofmt -l .)"
```

- [ ] **Step 9: Run a real Metal smoke test**

```bash
zsh -ic 'go run ./cmd/mcgo'
```

Verify:

- terrain arrives from the embedded server;
- WASD/free-flight still works;
- left click removes a targeted non-bedrock block after authority response;
- `1/2/3` + right click places the selected block;
- flying away and back preserves edits;
- terminal has no WebGPU validation errors.

- [ ] **Step 10: Commit**

```bash
git add internal/client internal/archcheck/deps_test.go cmd/mcgo
git commit -m "feat: 接入内置服务端与权威挖掘放置"
```

---

### Task 14: Add M2A Performance Gates, Scenario v2 Baseline, and CI Coverage

**Files:**
- Modify: `internal/client/perf.go`
- Modify: `internal/client/perf_test.go`
- Modify: `internal/render/bench_test.go`
- Modify: `internal/sim/bench_test.go`
- Modify: `cmd/mcgo/benchmark.go`
- Modify: `cmd/perfcheck/main.go`
- Modify: `.github/workflows/ci.yml`
- Modify: `docs/notes/perf-baseline.md`
- Modify: `docs/notes/perf-baseline.json`

**Interfaces:**
- Consumes: real M2A application and server tick measurements
- Produces:
  - `scenario_version = 2`
  - frame and tick percentile summaries
  - scripted successful break/place activity
  - updated absolute and relative gates

- [ ] **Step 1: Extend perf summary tests**

Add a `TickSummary` or reuse the existing percentile sampler with a distinct JSON field:

```go
type PerfReport struct {
	ScenarioVersion int                     `json:"scenario_version"`
	Hardware        string                  `json:"hardware"`
	OS              string                  `json:"os"`
	GoVersion       string                  `json:"go_version"`
	GitCommit       string                  `json:"git_commit"`
	Framebuffer     string                  `json:"framebuffer"`
	LoadSeconds     float64                 `json:"load_seconds"`
	SnapshotSeconds float64                 `json:"snapshot_seconds"`
	Phases          map[string]PhaseSummary `json:"phases"`
	Ticks           PhaseSummary            `json:"ticks"`
}
```

Test p50/p95/p99/max, zero samples, ring overwrite, and JSON roundtrip with tick data.

- [ ] **Step 2: Make benchmark interactions deterministic and successful**

Set `scenarioVersion = 2`. During the 120-second flying phase:

- compute terrain height from the fixed generator seed;
- keep camera three blocks above surface and aim down;
- once per second alternate break/place;
- cycle stone, dirt, grass placements;
- count accepted authoritative changes;
- fail if expected changes are rejected or if mirror/server final hashes differ.

Do not count initial generation/snapshot time in steady-state frame percentiles.

- [ ] **Step 3: Record tick durations**

Measure `Engine.Step` wall time inside server. Feed durations to a preallocated sampler without allocating per tick. Report p50/p95/p99/max and fail when:

- tick p99 ≥10 ms;
- any tick max ≥50 ms.

- [ ] **Step 4: Extend `perfcheck`**

Require identical hardware and scenario version. Compare tick p50/p95/p99/max and snapshot/load time in addition to frame phases and RSS. Any regression over the configured 20% fails.

- [ ] **Step 5: Run pure benchmarks**

```bash
zsh -ic 'go test ./... -bench=. -benchmem -run="^$"'
```

Record:

- raycast;
- idle and block-change tick;
- snapshot export/import;
- boundary remesh;
- existing generation, meshing, visibility, and palette metrics.

- [ ] **Step 6: Run the full fixed scene**

```bash
zsh -ic 'go run ./cmd/mcgo --benchmark --perf-output /tmp/mcgo-m2a-perf.json'
```

Expected:

- exact framebuffer `2560x1440`;
- both phases ≥100 fps;
- both frame p99 values <12 ms;
- RSS <2 GiB;
- tick p99 <10 ms and max <50 ms;
- all scripted interactions accepted;
- final authoritative/mirror hashes equal;
- no WebGPU validation errors.

- [ ] **Step 7: Update and verify the baseline**

Because scenario version changed, replace—not compare against—the v1 baseline:

```bash
zsh -ic 'go run ./cmd/perfcheck \
  -baseline docs/notes/perf-baseline.json \
  -current /tmp/mcgo-m2a-perf.json \
  -max-regression 0.20'
```

First inspect `/tmp/mcgo-m2a-perf.json`, then replace `docs/notes/perf-baseline.json` with its exact formatted JSON content using `apply_patch`. Update `perf-baseline.md` with date, hardware, Go version, microbench output, load/snapshot time, frame stats, tick stats, RSS, and a note that scenario v2 includes one authoritative interaction per second. Run the command above only after both files have been updated.

- [ ] **Step 8: Update CI**

Keep CI machine-independent:

```yaml
- name: 架构与协议门禁
  run: go test ./internal/archcheck ./internal/network -v

- name: 单元与端到端测试
  run: go test ./... -race

- name: 微基准与平台无关阈值
  run: go test ./... -bench=. -benchtime=1x -run='^$'
```

Do not claim GitHub-hosted runners enforce same-machine absolute frame/tick comparisons.

- [ ] **Step 9: Run final repository validation**

```bash
zsh -ic 'go test ./... -race -count=1'
zsh -ic 'go vet ./...'
test -z "$(gofmt -l .)"
git diff --check
git status --short
```

Expected: all checks pass; status contains only intended M2A files plus the untouched untracked `.claude/`.

- [ ] **Step 10: Commit**

```bash
git add .github/workflows/ci.yml cmd/mcgo/benchmark.go cmd/perfcheck \
  internal/client/perf.go internal/client/perf_test.go \
  internal/render/bench_test.go internal/sim/bench_test.go \
  docs/notes/perf-baseline.md docs/notes/perf-baseline.json
git commit -m "chore: M2A 交互场景性能与一致性门禁"
```

---

## M2A Completion Checklist

- [ ] All terrain visible to the client originates from the embedded server.
- [ ] No package under `internal/client` imports `worldgen`.
- [ ] Client and server share no mutable world objects or backing slices.
- [ ] Left click instantly breaks non-bedrock blocks after server confirmation.
- [ ] `1/2/3` select stone/dirt/grass and right click places the selected block.
- [ ] Session edits survive chunk unload and deterministic regeneration.
- [ ] Revision gaps trigger one resync and recover with an atomic snapshot.
- [ ] Stale mesh jobs never overwrite newer mirror state.
- [ ] End-to-end server and mirror hashes match at the same revision.
- [ ] Worker panic and full queue tests prove shutdown cannot deadlock.
- [ ] `go test ./... -race`, `go vet ./...`, format, architecture, and WebGPU isolation gates pass.
- [ ] Scenario v2 passes all frame, tick, RSS, interaction, and consistency gates on the benchmark machine.
