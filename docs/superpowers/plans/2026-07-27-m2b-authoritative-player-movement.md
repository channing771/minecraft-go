# M2B Authoritative Player Movement Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the free-flying local camera with a server-authoritative Minecraft-like player that walks, falls, jumps, collides, steps over 0.6-block obstacles, predicts locally, reconciles from server state, and performs interactions from the authoritative eye position.

**Architecture:** Add a pure `internal/physics` package shared by `sim` and `client`, then give each registered server session an authoritative player lifecycle and fixed-step state. Extend the value-only protocol with input intents and player snapshots; the client predicts against its Mirror, replays unacknowledged inputs after corrections, and keeps presentation smoothing separate from simulation. Normal subscriptions and interaction rays derive only from authority; the existing high-speed render benchmark moves through a benchmark-only trusted observer path.

**Tech Stack:** Go 1.26 through the user's GVM installation, `go-gl/mathgl` float32 vectors, existing paletted chunks and MemoryTransport, deterministic 20 TPS simulation, table/property/fuzz tests, race detector, headless WebGPU/Metal, GitHub Actions.

## Global Constraints

- Use the GVM-managed Go 1.26 toolchain through `zsh -ic`; never install or download another Go distribution.
- Preserve the user's local `.claude/settings.local.json`; it is ignored and must never be staged.
- Player physics runs at exactly 20 Hz with a fixed 50 ms step.
- Player width is `0.6`, height `1.8`, eye height `1.62`, and step height `0.6` blocks.
- Walk speed is `4.3` blocks/s; ground acceleration is `40`, ground deceleration `50`, air acceleration `8`, jump speed `8.4`, gravity `32`, and terminal fall speed `78.4` blocks/s.
- M2B does not add sprinting, crouching, swimming, flying, spectator gameplay, damage, health, inventory, TCP, or persistence.
- The normal client never sends position, velocity, grounded state, ray origin, or subscription center.
- Render radius remains 32 chunks and the normal server subscription radius remains 33, derived from the authoritative player or PendingSpawn anchor.
- Interaction reach remains exactly `6.0` blocks; placement rejects overlap with the complete authoritative player AABB.
- `internal/physics` depends only on `internal/core`; `sim` never imports `client`, `network`, `render`, or `gfx`; only `internal/gfx` imports WebGPU.
- Unknown/unloaded collision cells are closed blockers on both client and server.
- Untrusted protocol values return stable errors or rejections and never panic; internal invariant violations may panic.
- Interactive or benchmark verification must never bring a window to the foreground. The performance command must use the existing `--benchmark` headless/offscreen path.
- Every implementation task follows red → green → refactor; verification-only gates first run against current code, and every task verifies its focused package with `-race` before creating one isolated commit.
- Final gates remain ≥100 FPS, frame p99 `<12 ms`, RSS `<2 GiB`, server tick p99 `<10 ms`, and server tick max `<50 ms`.

## File and Responsibility Map

| Path | Responsibility after M2B |
|---|---|
| `internal/core/geom.go` | Shared AABB overlap primitive; no game or world dependency |
| `internal/physics/types.go` | Player dimensions, constants, state/input/result and collision-source contract |
| `internal/physics/motion.go` | Horizontal acceleration, jump, gravity and fixed-step orchestration |
| `internal/physics/collision.go` | Deterministic Y→X→Z swept AABB resolution and unknown blockers |
| `internal/physics/step.go` | Ordinary-path versus 0.6-block step-path selection |
| `internal/network/message.go` | Player input/action intents, player snapshots and stable reject values |
| `internal/sim/player.go` | Registered player lifecycle, authoritative state and player hash |
| `internal/sim/spawn.go` | Deterministic PendingSpawn candidate ordering and safety recovery |
| `internal/sim/engine.go` | Tick ordering, input coalescing, one physics step/tick and authority-derived view center |
| `internal/sim/command.go` | Protocol-neutral movement and action commands/results |
| `internal/server/server.go` | Session registration, message translation and trusted benchmark observer gate |
| `internal/server/publication.go` | FIFO publication of `PlayerState` with snapshots/deltas/rejections |
| `internal/client/collision.go` | Mirror-backed `physics.CollisionSource` |
| `internal/client/predictor.go` | Fixed-step prediction, bounded history, reconciliation and presentation offset |
| `internal/client/input.go` | Digital WASD/jump state plus existing click edges/block selection |
| `cmd/mcgo/app.go` | Embedded-server/player assembly and message dispatch |
| `cmd/mcgo/main.go` | Interactive ground controls; benchmark remains non-interactive/headless |
| `cmd/mcgo/benchmark.go` | Scenario v3 trusted observer, final world hash and unchanged absolute gates |
| `internal/server/player_integration_test.go` | Real MemoryTransport + delayed player-state convergence scenario |
| `docs/notes/perf-baseline.*` | Scenario v3 same-machine measurements and reproducible command |

---

### Task 1: Define the Shared Player Model and Free-Space Kinematics

**Files:**
- Create: `internal/physics/types.go`
- Create: `internal/physics/motion.go`
- Create: `internal/physics/motion_test.go`
- Modify: `internal/core/geom.go`
- Modify: `internal/core/geom_test.go`
- Modify: `internal/archcheck/deps_test.go`

**Interfaces:**
- Consumes: `core.AABB`, `core.BlockPos`, `mgl32.Vec3`
- Produces:

```go
const FixedDelta = 50 * time.Millisecond
const FixedDeltaSeconds float32 = 0.05

type State struct {
	Position mgl32.Vec3
	Velocity mgl32.Vec3
	OnGround bool
}

type Input struct {
	MoveX int8
	MoveZ int8
	Jump  bool
	Yaw   float32
}

type CollisionBoxSet struct {
	Loaded bool
	Count  uint8
	Boxes  [8]core.AABB
}

type CollisionSource interface {
	CollisionBoxes(core.BlockPos) CollisionBoxSet
}

type StepResult struct {
	State      State
	UsedStep   bool
	HitUnknown bool
}

func PlayerBounds(mgl32.Vec3) core.AABB
func BlockCollisionBoxes(core.BlockID, bool) CollisionBoxSet
func Step(State, Input, CollisionSource) StepResult
func (a core.AABB) Overlaps(b core.AABB) bool
```

- [ ] **Step 1: Write failing AABB and free-motion tests**

Add to `internal/core/geom_test.go`:

```go
func TestAABBOverlapUsesStrictVolume(t *testing.T) {
	a := core.AABB{Min: mgl32.Vec3{0, 0, 0}, Max: mgl32.Vec3{1, 1, 1}}
	if !a.Overlaps(core.AABB{Min: mgl32.Vec3{0.5, 0, 0}, Max: mgl32.Vec3{1.5, 1, 1}}) {
		t.Fatal("有体积交叠的 AABB 未命中")
	}
	if a.Overlaps(core.AABB{Min: mgl32.Vec3{1, 0, 0}, Max: mgl32.Vec3{2, 1, 1}}) {
		t.Fatal("仅接触边界的 AABB 不应算交叠")
	}
}
```

Create `internal/physics/motion_test.go`:

```go
package physics_test

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"minecraft-go/internal/core"
	"minecraft-go/internal/physics"
)

type emptySource struct{}

func (emptySource) CollisionBoxes(core.BlockPos) physics.CollisionBoxSet {
	return physics.CollisionBoxSet{Loaded: true}
}

func TestPlayerBoundsUseFeetCenter(t *testing.T) {
	bounds := physics.PlayerBounds(mgl32.Vec3{10, 20, -4})
	wantMin := mgl32.Vec3{9.7, 20, -4.3}
	wantMax := mgl32.Vec3{10.3, 21.8, -3.7}
	if !bounds.Min.ApproxEqualThreshold(wantMin, 1e-6) ||
		!bounds.Max.ApproxEqualThreshold(wantMax, 1e-6) {
		t.Fatalf("bounds=%+v，想要 min=%v max=%v", bounds, wantMin, wantMax)
	}
}

func TestDiagonalInputAcceleratesWithoutDiagonalBoost(t *testing.T) {
	got := physics.Step(physics.State{OnGround: true}, physics.Input{
		MoveX: 1, MoveZ: 1,
	}, emptySource{}).State
	horizontal := float32(math.Hypot(float64(got.Velocity.X()), float64(got.Velocity.Z())))
	if math.Abs(float64(horizontal-2.0)) > 1e-5 {
		t.Fatalf("首步水平速度=%f，想要 acceleration*dt=2", horizontal)
	}
}

func TestJumpAndGravityUseFixedConstants(t *testing.T) {
	jump := physics.Step(physics.State{OnGround: true}, physics.Input{Jump: true}, emptySource{}).State
	if jump.Velocity.Y() != physics.JumpSpeed || jump.OnGround {
		t.Fatalf("jump=%+v", jump)
	}
	fall := physics.Step(physics.State{Velocity: mgl32.Vec3{0, -78, 0}}, physics.Input{}, emptySource{}).State
	if fall.Velocity.Y() != -physics.TerminalFallSpeed {
		t.Fatalf("terminal velocity=%f", fall.Velocity.Y())
	}
}
```

- [ ] **Step 2: Run the tests and verify the missing package/API failure**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/core ./internal/physics -run "TestAABBOverlapUsesStrictVolume|TestPlayerBoundsUseFeetCenter|TestDiagonalInputAcceleratesWithoutDiagonalBoost|TestJumpAndGravityUseFixedConstants" -v'
```

Expected: FAIL because `internal/physics`, `AABB.Overlaps`, and player constants do not exist.

- [ ] **Step 3: Add exact constants, types, and free-space velocity integration**

Implement `AABB.Overlaps` with strict volume overlap:

```go
func (a AABB) Overlaps(b AABB) bool {
	return a.Min.X() < b.Max.X() && a.Max.X() > b.Min.X() &&
		a.Min.Y() < b.Max.Y() && a.Max.Y() > b.Min.Y() &&
		a.Min.Z() < b.Max.Z() && a.Max.Z() > b.Min.Z()
}
```

Define these exported constants in `physics/types.go`:

```go
const (
	FixedDelta              = 50 * time.Millisecond
	FixedDeltaSeconds       = float32(0.05)
	PlayerWidth             = float32(0.6)
	PlayerHeight            = float32(1.8)
	EyeHeight               = float32(1.62)
	StepHeight              = float32(0.6)
	WalkSpeed               = float32(4.3)
	GroundAcceleration      = float32(40)
	GroundDeceleration      = float32(50)
	AirAcceleration         = float32(8)
	JumpSpeed               = float32(8.4)
	Gravity                 = float32(32)
	TerminalFallSpeed       = float32(78.4)
	CollisionEpsilon        = float32(1e-5)
	GroundProbe             = float32(1e-4)
)
```

`BlockCollisionBoxes` returns `Loaded:false` unchanged, zero boxes for loaded air, and one local `[0,1]³` box for every other current block. Implement horizontal intent using yaw-only forward/right vectors, normalize the combined vector, use vector `moveToward`, apply jump/gravity, and temporarily integrate free-space position. Invalid `MoveX/MoveZ` or non-finite state/input is an internal programmer error and must panic in this package; protocol callers validate first.

Register the new dependency rules:

```go
"internal/physics": {"internal/core"},
"internal/sim":     {"internal/core", "internal/physics", "internal/world"},
"internal/client":  {"internal/core", "internal/physics", "internal/network", "internal/world", "internal/mesh", "internal/assets", "internal/render", "internal/gfx"},
```

- [ ] **Step 4: Run focused tests, architecture gate, and race detector**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/core ./internal/physics ./internal/archcheck -race -count=1'
```

Expected: PASS; diagonal first-step speed is exactly `2.0` within tolerance and the new dependency whitelist is accepted.

- [ ] **Step 5: Commit**

```bash
git add internal/core/geom.go internal/core/geom_test.go internal/physics internal/archcheck/deps_test.go
git commit -m "feat: 添加共享玩家运动模型"
```

---

### Task 2: Resolve Deterministic Block Collisions and Unknown Boundaries

**Files:**
- Create: `internal/physics/collision.go`
- Create: `internal/physics/collision_test.go`
- Modify: `internal/physics/motion.go`

**Interfaces:**
- Consumes: Task 1 `State`, `CollisionSource`, `PlayerBounds`
- Produces:

```go
type moveResult struct {
	position   mgl32.Vec3
	clipped    [3]bool
	onGround   bool
	hitUnknown bool
}

func resolveMove(State, mgl32.Vec3, CollisionSource) moveResult
```

- [ ] **Step 1: Write floor, wall, ceiling, ledge, and unknown-cell tests**

Create a map-backed test source whose boxes are block-local and add these exact assertions:

```go
func TestCollisionStopsOnFloorAndWall(t *testing.T) {
	world := boxes(
		block(0, 0, 0, fullCube),
		block(1, 1, 0, fullCube),
	)
	state := physics.State{
		Position: mgl32.Vec3{0.5, 1.2, 0.5},
		Velocity: mgl32.Vec3{10, -10, 0},
	}
	got := physics.Step(state, physics.Input{}, world).State
	if math.Abs(float64(got.Position.Y()-1)) > 1e-5 || !got.OnGround {
		t.Fatalf("未落在 y=1: %+v", got)
	}
	if got.Position.X() > 0.7+1e-5 || got.Velocity.X() != 0 {
		t.Fatalf("穿过 x=1 墙: %+v", got)
	}
}

func TestUnknownBlockIsClosedBoundary(t *testing.T) {
	world := unknownAt(core.BlockPos{X: 1, Y: 1, Z: 0})
	got := physics.Step(physics.State{
		Position: mgl32.Vec3{0.5, 1, 0.5},
		Velocity: mgl32.Vec3{10, 0, 0},
		OnGround: true,
	}, physics.Input{}, world)
	if !got.HitUnknown || got.State.Position.X() > 0.7+1e-5 {
		t.Fatalf("unknown 未阻挡: %+v", got)
	}
}

func TestWalkingOffLedgeClearsGroundInSameStep(t *testing.T) {
	world := boxes(block(0, 0, 0, fullCube))
	got := physics.Step(physics.State{
		Position: mgl32.Vec3{1.25, 1, 0.5},
		Velocity: mgl32.Vec3{4.3, 0, 0},
		OnGround: true,
	}, physics.Input{MoveX: 1}, world).State
	if got.OnGround {
		t.Fatalf("离开悬崖后仍 OnGround: %+v", got)
	}
}
```

Also test head collision clears positive Y velocity, negative world coordinates, and the deterministic Y→X→Z corner result.

- [ ] **Step 2: Run focused tests and verify free-space integration fails them**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/physics -run "TestCollision|TestUnknownBlockIsClosedBoundary|TestWalkingOffLedgeClearsGroundInSameStep" -v'
```

Expected: FAIL because Task 1 moves through all blocks.

- [ ] **Step 3: Implement swept axis clipping without allocations**

In `collision.go`, implement these rules exactly:

```go
var axisOrder = [...]int{1, 0, 2} // Y, X, Z

func blockBounds(position core.BlockPos, local core.AABB) core.AABB {
	offset := mgl32.Vec3{float32(position.X), float32(position.Y), float32(position.Z)}
	return core.AABB{Min: local.Min.Add(offset), Max: local.Max.Add(offset)}
}
```

For each axis, build the broad phase from the current player AABB and that axis's requested delta. Enumerate block coordinates with nested `for y`, `for x`, `for z` loops in ascending order. A loaded box clips only when the other two axes overlap strictly; an unloaded cell contributes one full-cube box and sets `hitUnknown=true`.

Positive motion is clipped to `collider.Min[axis] - player.Max[axis]`; negative motion is clipped to `collider.Max[axis] - player.Min[axis]`. Apply an epsilon only when comparing candidate distances; do not shrink the player's stored dimensions. After Y/X/Z resolution, use a `GroundProbe` downward query to preserve ground while standing and clear it in the same step after leaving support.

Replace Task 1's free-space position integration with `resolveMove`, and clear velocity components whose displacement was clipped. Downward Y clipping sets `OnGround=true`; upward Y clipping only clears velocity.

- [ ] **Step 4: Run focused tests and the race detector**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/physics -race -count=1'
```

Expected: PASS for floor, wall, ceiling, ledge, corner, negative-coordinate, and unknown-boundary cases.

- [ ] **Step 5: Commit**

```bash
git add internal/physics/collision.go internal/physics/collision_test.go internal/physics/motion.go
git commit -m "feat: 确定性玩家方块碰撞"
```

---

### Task 3: Add the 0.6-Block Automatic Step Path

**Files:**
- Create: `internal/physics/step.go`
- Create: `internal/physics/step_test.go`
- Modify: `internal/physics/motion.go`
- Modify: `internal/physics/collision.go`

**Interfaces:**
- Consumes: Task 2 `resolveMove`
- Produces: `physics.Step` ordinary/step path choice and `StepResult.UsedStep`

- [ ] **Step 1: Write failing step-selection tests**

Use a `0.5`-high local box `{Min:{0,0,0}, Max:{1,0.5,1}}` and assert:

```go
func TestStepClimbsHalfBlock(t *testing.T) {
	world := floorWithObstacle(0.5, true)
	got := physics.Step(physics.State{
		Position: mgl32.Vec3{0.5, 1, 0.5},
		Velocity: mgl32.Vec3{4.3, 0, 0},
		OnGround: true,
	}, physics.Input{MoveX: 1}, world)
	if !got.UsedStep || got.State.Position.Y() < 1.49 {
		t.Fatalf("未跨上半格: %+v", got)
	}
}

func TestStepDoesNotClimbFullBlock(t *testing.T) {
	got := physics.Step(groundedTowardObstacle(), physics.Input{MoveX: 1}, floorWithObstacle(1, true))
	if got.UsedStep || got.State.Position.X() > 0.7+1e-5 {
		t.Fatalf("越过整格障碍: %+v", got)
	}
}
```

Add separate cases for insufficient headroom, airborne players, unknown obstacle cells, and equal horizontal-distance tie retaining the ordinary path.

- [ ] **Step 2: Run the step tests and verify they fail**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/physics -run "TestStep" -v'
```

Expected: half-block test FAIL because only the ordinary collision path exists.

- [ ] **Step 3: Implement the alternative path and strict winner rule**

Add a resolver that executes exactly:

```go
func horizontalDistanceSquared(from, to mgl32.Vec3) float32 {
	dx, dz := to.X()-from.X(), to.Z()-from.Z()
	return dx*dx + dz*dz
}
```

1. Resolve up by at most `StepHeight`.
2. Resolve the original X and Z displacement from the raised box.
3. Resolve down by `actualRise + max(0, -requestedY)`.
4. Require collision-free final bounds and ground support.
5. Reject the path if any lookup is unknown.
6. Choose it only when `stepHorizontalSquared > ordinaryHorizontalSquared`.

Try this path only when ordinary X/Z was clipped, the player began grounded or ordinary motion touched ground, and requested horizontal displacement is nonzero. Preserve the chosen path's clipped velocity components and set `UsedStep=true` only for the alternative.

- [ ] **Step 4: Run all physics tests with race detection**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/physics -race -count=1'
```

Expected: PASS; half block climbs, full block/headroom/airborne/unknown cases do not.

- [ ] **Step 5: Commit**

```bash
git add internal/physics/step.go internal/physics/step_test.go internal/physics/motion.go internal/physics/collision.go
git commit -m "feat: 支持零点六格自动跨步"
```

---

### Task 4: Lock Physics Invariants, Determinism, and Zero Allocations

**Files:**
- Create: `internal/physics/physics_fuzz_test.go`
- Create: `internal/physics/physics_bench_test.go`
- Modify: `internal/physics/types.go`
- Modify: `internal/physics/collision.go`
- Modify: `internal/physics/step.go`

**Interfaces:**
- Consumes: completed `physics.Step`
- Produces:

```go
func ValidState(State) bool
```

and fuzz invariants plus three stable benchmark names.

- [ ] **Step 1: Add deterministic/property tests and allocation assertions**

Create `physics_fuzz_test.go` with seeded finite states and inputs. For each accepted case, run the same step twice and assert equal structs, all fields finite, horizontal speed `<= WalkSpeed+1e-5`, vertical speed `>= -TerminalFallSpeed`, and final bounds do not overlap loaded colliders.

Add a table test requiring `ValidState` to accept an ordinary finite state and reject NaN/Inf in every position or velocity component.

Include this direct allocation gate in `physics_bench_test.go`:

```go
func TestStepPlayerDoesNotAllocate(t *testing.T) {
	state := physics.State{Position: mgl32.Vec3{0.5, 1, 0.5}, OnGround: true}
	input := physics.Input{MoveZ: 1}
	world := benchmarkWorld{}
	allocs := testing.AllocsPerRun(1000, func() {
		_ = physics.Step(state, input, world)
	})
	if allocs != 0 {
		t.Fatalf("physics.Step allocs/op=%f，想要 0", allocs)
	}
}
```

Define exact benchmarks:

```go
func BenchmarkStepPlayerFlat(b *testing.B)
func BenchmarkStepPlayerColliding(b *testing.B)
func BenchmarkStepPlayerStepping(b *testing.B)
```

Each benchmark calls `b.ReportAllocs()` and assigns the result to a package-level sink.

- [ ] **Step 2: Run the new tests and benchmarks**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/physics -run "TestStepPlayerDoesNotAllocate|TestStepDeterministic" -count=1 -v'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/physics -run "^$" -bench "BenchmarkStepPlayer" -benchmem -benchtime=1000x'
```

Expected: compile FAIL because `ValidState` does not exist. After adding that API in Step 3, the allocation test must report exactly zero; a nonzero measurement is also a failing gate.

- [ ] **Step 3: Add state validation and lock stack-only collision storage**

Implement `ValidState` with `math.IsNaN`/`math.IsInf` checks over all six vector components. Keep collision candidates in stack values and direct nested loops: collision boxes stay in `CollisionBoxSet.Boxes`, no `[]core.AABB` is built, and fixed axis order remains an array. Do not add pools or `sync.Pool`; one-player collision needs neither.

- [ ] **Step 4: Run fuzz smoke, race tests, and allocation gates**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/physics -fuzz FuzzStepKeepsFiniteNonOverlappingState -fuzztime=5s'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/physics -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/physics -run "^$" -bench "BenchmarkStepPlayer" -benchmem -benchtime=1000x'
```

Expected: fuzz finds no failure, package tests PASS, all three benchmarks print `0 B/op` and `0 allocs/op`.

- [ ] **Step 5: Commit**

```bash
git add internal/physics
git commit -m "test: 锁定玩家物理不变式与分配"
```

---

### Task 5: Add the M2B Value-Only Player Protocol

**Files:**
- Modify: `internal/network/message.go`
- Modify: `internal/network/message_test.go`
- Modify: `internal/client/mirror.go`
- Modify: `internal/client/mirror_test.go`

**Interfaces:**
- Consumes: existing sealed `ClientMessage` / `ServerMessage`
- Produces:

```go
type PlayerInput struct {
	Sequence uint64
	MoveX    int8
	MoveZ    int8
	Jump     bool
	Yaw      float32
	Pitch    float32
}

type BreakBlock struct { Sequence uint64; Yaw, Pitch float32 }
type PlaceBlock struct { Sequence uint64; Yaw, Pitch float32; Block core.BlockID }

type PlayerState struct {
	ServerTick        uint64
	LastInputSequence uint64
	Dimension         core.DimensionID
	Position          mgl32.Vec3
	Velocity          mgl32.Vec3
	Yaw, Pitch        float32
	OnGround          bool
	Ready             bool
	Reset             bool
}
```

- [ ] **Step 1: Extend sealed-message and stable-value tests**

Update `TestProtocolMessageShapesImplementSealedInterfaces` to include the three new client messages and `network.PlayerState`. Extend `TestRejectReasonsAreStableProtocolValues` with:

```go
{network.RejectInvalidInput, "invalid_input"},
{network.RejectPlayerNotReady, "player_not_ready"},
```

Add a Mirror test proving `network.PlayerState` is deliberately not world state:

```go
func TestMirrorDoesNotConsumePlayerState(t *testing.T) {
	_, err := client.NewMirror().Apply(network.PlayerState{Ready: false})
	if err == nil || !strings.Contains(err.Error(), "unsupported server message") {
		t.Fatalf("Mirror.Apply PlayerState err=%v", err)
	}
}
```

- [ ] **Step 2: Run focused tests and verify new types are missing**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network ./internal/client -run "TestProtocolMessageShapesImplementSealedInterfaces|TestRejectReasonsAreStableProtocolValues|TestMirrorDoesNotConsumePlayerState" -v'
```

Expected: FAIL because M2B messages and reject values do not exist.

- [ ] **Step 3: Add new messages without removing M2A messages yet**

Add the exact structs and sealed marker methods to `message.go`, plus:

```go
const (
	RejectInvalidInput   RejectReason = "invalid_input"
	RejectPlayerNotReady RejectReason = "player_not_ready"
)
```

Keep `SetViewCenter`, `BreakRay`, and `PlaceRay` temporarily so every intermediate commit compiles; mark them with a comment that Task 14 removes them after app and benchmark migration. Semantic validators stay in `sim` and `client`, not `network`, because they depend on state/direction meaning.

Add the two new reject values to `client.rejectionUpdate`, while preserving all M2A reasons.

- [ ] **Step 4: Run protocol, client, and ownership tests**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network ./internal/client -race -count=1'
```

Expected: PASS; the sealed sets include both transitional legacy messages and all M2B messages.

- [ ] **Step 5: Commit**

```bash
git add internal/network/message.go internal/network/message_test.go internal/client/mirror.go internal/client/mirror_test.go
git commit -m "feat: 定义 M2B 玩家状态协议"
```

---

### Task 6: Register Authoritative Players and Implement PendingSpawn

**Files:**
- Create: `internal/sim/player.go`
- Create: `internal/sim/player_test.go`
- Create: `internal/sim/spawn.go`
- Create: `internal/sim/spawn_test.go`
- Modify: `internal/sim/engine.go`
- Modify: `internal/sim/command.go`

**Interfaces:**
- Consumes: `physics.State`, `Dimension.BlockAt`, existing chunk generation lifecycle
- Produces:

```go
type PlayerLifecycle uint8

const (
	PlayerPendingSpawn PlayerLifecycle = iota
	PlayerActive
)

type PlayerUpdate struct {
	Session           SessionID
	Dimension         core.DimensionID
	ViewCenter        core.ChunkPos
	State             physics.State
	Yaw, Pitch        float32
	LastInputSequence uint64
	Ready             bool
	Reset             bool
}

func (e *Engine) RegisterSession(SessionID, core.DimensionID, core.ChunkPos)
func (e *Engine) Player(SessionID) (PlayerUpdate, bool)
```

`TickResult` gains `Players []PlayerUpdate` in ascending session order.

- [ ] **Step 1: Write registration, spawn-order, and first-Ready-state tests**

Create tests that register session 1 at Overworld anchor `(0,0)`, then assert:

```go
func TestRegisteredSessionRequestsAnchorBeforeClientInput(t *testing.T) {
	engine := sim.NewEngine(flatBaseBlock, 0)
	engine.RegisterSession(1, core.Overworld, core.ChunkPos{})
	result := engine.Step()
	want := []core.ChunkKey{{Dimension: core.Overworld, Pos: core.ChunkPos{}}}
	if !reflect.DeepEqual(result.Generate, want) {
		t.Fatalf("Generate=%+v，想要 %+v", result.Generate, want)
	}
	if len(result.Players) != 1 || result.Players[0].Ready {
		t.Fatalf("初始 player update=%+v", result.Players)
	}
}

func TestPendingSpawnActivatesAtDeterministicSurface(t *testing.T) {
	engine := sim.NewEngine(flatBaseBlock, 0)
	engine.RegisterSession(1, core.Overworld, core.ChunkPos{})
	engine.Step()
	engine.SubmitGenerated(sim.GeneratedChunk{
		Dimension: core.Overworld,
		Pos: core.ChunkPos{},
		Chunk: generateFlatChunk(core.ChunkPos{}),
	})
	result := engine.Step()
	player := onlyPlayer(t, result)
	if !player.Ready || !player.Reset || player.State.Position != (mgl32.Vec3{0.5, 1, 0.5}) || !player.State.OnGround {
		t.Fatalf("spawn=%+v", player)
	}
	if next := onlyPlayer(t, engine.Step()); next.Reset {
		t.Fatal("Reset 只能出现一次")
	}
}
```

Add `TestSpawnCandidatesOrderByDistanceThenXZ`, `TestSpawnWaitsForEarlierUnknownCandidate`, `TestExhaustedSpawnRetriesOnlyAfterRevisionChange`, and `TestRegisterSessionRejectsDuplicateOrUnknownDimension`.

- [ ] **Step 2: Run the sim tests and verify registration APIs are missing**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim -run "TestRegisteredSession|TestPendingSpawn|TestSpawnCandidates|TestExhaustedSpawn|TestRegisterSession" -v'
```

Expected: FAIL because registered players, spawn candidates, and `TickResult.Players` do not exist.

- [ ] **Step 3: Implement a player-bearing session without breaking legacy view-only tests**

Keep `sessionState` usable by transitional M2A tests, but add an optional `player *playerState`. `RegisterSession` creates the session eagerly, panics on duplicate IDs/unknown dimensions, marks its authoritative view as the anchor, and precomputes candidate columns for radius 16.

Generate candidates with this stable comparison:

```go
sort.Slice(candidates, func(i, j int) bool {
	li := int64(candidates[i].X-anchorX)*int64(candidates[i].X-anchorX) +
		int64(candidates[i].Z-anchorZ)*int64(candidates[i].Z-anchorZ)
	lj := int64(candidates[j].X-anchorX)*int64(candidates[j].X-anchorX) +
		int64(candidates[j].Z-anchorZ)*int64(candidates[j].Z-anchorZ)
	if li != lj { return li < lj }
	if candidates[i].X != candidates[j].X { return candidates[i].X < candidates[j].X }
	return candidates[i].Z < candidates[j].Z
})
```

For the current candidate, scan from `core.MaxY-1` down through `core.MinY`. If any queried cell or neighboring cell needed by `physics.PlayerBounds` is not Ready, stop and retry the same candidate next tick. A valid support is non-air with a collision box; validate the entire player AABB above it using a `dimensionCollisionSource` adapter and activate at `(x+0.5, supportY+1, z+0.5)`.

For PendingSpawn publication, use finite placeholder position `(anchor.X*16+0.5, core.MaxY+1, anchor.Z*16+0.5)`, `Ready=false`, and `Reset=false`. On activation clear velocity/input, set ground, and emit `Reset=true` exactly once.

Precompute the unique candidate chunks in X/Z order. If all columns are invalid, store their Ready revisions in a parallel slice, remain PendingSpawn, and log one warning every 100 engine ticks (five seconds at 20 TPS). Do not rescan until one recorded state/revision changes; then restart from the first candidate. This makes retry deterministic and avoids scanning 1,089 columns every tick.

Sort `TickResult.Players` by `Session`, and make `Player` return a copy rather than internal pointers.

- [ ] **Step 4: Run sim race tests and existing lifecycle tests**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim -race -count=1'
```

Expected: PASS; M2A view-only test helpers still work, while registered sessions spawn without receiving client input.

- [ ] **Step 5: Commit**

```bash
git add internal/sim/player.go internal/sim/player_test.go internal/sim/spawn.go internal/sim/spawn_test.go internal/sim/engine.go internal/sim/command.go
git commit -m "feat: 建立权威玩家出生生命周期"
```

---

### Task 7: Apply Validated Player Input Once per Authoritative Tick

**Files:**
- Modify: `internal/sim/command.go`
- Modify: `internal/sim/engine.go`
- Modify: `internal/sim/player.go`
- Create: `internal/sim/movement_test.go`
- Modify: `internal/sim/engine_test.go`
- Modify: `internal/sim/bench_test.go`

**Interfaces:**
- Consumes: Task 6 registered Active player and Task 1–4 `physics.Step`
- Produces:

```go
const CommandPlayerInput CommandKind = 4 // appended after the four transitional M2A kinds

// Existing Command gains:
MoveX int8
MoveZ int8
Jump  bool
Yaw   float32
Pitch float32

const (
	RejectInvalidInput   RejectReason = 6
	RejectPlayerNotReady RejectReason = 7
)

func (e *Engine) PlayerHash(SessionID) ([32]byte, bool)
```

- [ ] **Step 1: Write failing coalescing, validation, recovery, and hash tests**

Add tests with an Active flat-world player:

```go
func TestEngineAppliesOnlyLatestPlayerInputOncePerTick(t *testing.T) {
	engine, session := readyFlatPlayer(t)
	before, _ := engine.Player(session)
	engine.Enqueue(sim.Command{Session: session, Sequence: 2, Kind: sim.CommandPlayerInput, MoveZ: 1})
	engine.Enqueue(sim.Command{Session: session, Sequence: 3, Kind: sim.CommandPlayerInput, MoveX: 1})
	result := engine.Step()
	after := onlyPlayer(t, result)
	if after.LastInputSequence != 3 {
		t.Fatalf("ack=%d，想要 3", after.LastInputSequence)
	}
	if after.State.Position.Z() != before.State.Position.Z() {
		t.Fatalf("较早 MoveZ 被执行: before=%v after=%v", before.State, after.State)
	}
	if after.State.Position.X() <= before.State.Position.X() {
		t.Fatalf("最新 MoveX 未执行: before=%v after=%v", before.State, after.State)
	}
}

func TestInvalidLatestInputIsAckedAndNeutral(t *testing.T) {
	engine, session := readyFlatPlayer(t)
	engine.Enqueue(sim.Command{Session: session, Sequence: 2, Kind: sim.CommandPlayerInput, MoveZ: 2})
	result := engine.Step()
	if onlyPlayer(t, result).LastInputSequence != 2 || len(result.Rejected) != 1 || result.Rejected[0].Reason != sim.RejectInvalidInput {
		t.Fatalf("result=%+v", result)
	}
}
```

Also add:

- no new input reuses the previous valid held state;
- an Active player with feet below `MinY-16` returns to PendingSpawn;
- a player intersecting a solid block moves up by the first free `1/16` step when possible;
- unresolved or unknown overlap returns to PendingSpawn;
- two identical movement scripts produce identical `PlayerHash` and world hash;
- `BenchmarkEngineStepPlayer` executes one registered player tick.

- [ ] **Step 2: Run movement tests and verify missing command behavior**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim -run "TestEngineAppliesOnlyLatestPlayerInputOncePerTick|TestInvalidLatestInputIsAckedAndNeutral|TestPlayerRecovery|TestPlayerReplay" -v'
```

Expected: FAIL because movement commands and recovery are not implemented.

- [ ] **Step 3: Extend the fixed tick in the approved order**

During sorted command ingestion:

- Reject `MoveX/MoveZ` outside `[-1,1]`, non-finite yaw/pitch, or pitch outside `[-π/2+0.01, π/2-0.01]`.
- For an invalid newest input, store neutral axes/Jump, consume its sequence, set `LastInputSequence`, and append `RejectInvalidInput`.
- For each valid input, overwrite the session's held `physics.Input`, normalized yaw, pitch, and `LastInputSequence`; sorted order naturally leaves only the newest state.
- Do not call physics during ingestion.

After applying generated chunks and trying PendingSpawn, process each Active player once:

```go
if !finitePlayer(player.state) || player.state.Position.Y() < core.MinY-16 {
	player.beginReset()
} else if !engine.tryUnstick(player) {
	player.beginReset()
} else {
	result := physics.Step(player.state, player.input, dimensionCollisionSource{dimension})
	player.state = result.State
}
```

Then derive `session.center` only from the PendingSpawn anchor or authoritative `player.state.Position`. Reconcile subscriptions after movement. Keep old view-only sessions working until Task 14.

Implement `PlayerHash` by feeding dimension, lifecycle, position/velocity float32 bits, yaw/pitch, OnGround, input and last input sequence into SHA-256 in fixed little-endian order. Never hash maps or padding bytes.

- [ ] **Step 4: Run sim tests, benchmarks, and race detection**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim -run "^$" -bench "BenchmarkEngineStep(Player|Idle)" -benchmem -benchtime=1000x'
```

Expected: PASS; the player benchmark executes and one server tick never applies more than one movement step.

- [ ] **Step 5: Commit**

```bash
git add internal/sim/command.go internal/sim/engine.go internal/sim/player.go internal/sim/movement_test.go internal/sim/engine_test.go internal/sim/bench_test.go
git commit -m "feat: 权威玩家移动与安全恢复"
```

---

### Task 8: Derive Breaking and Placement from the Authoritative Pose

**Files:**
- Modify: `internal/sim/command.go`
- Modify: `internal/sim/engine.go`
- Modify: `internal/sim/interaction_test.go`
- Create: `internal/sim/player_interaction_test.go`

**Interfaces:**
- Consumes: Active authoritative player, `core.RaycastBlocks`, `core.AABB.Overlaps`
- Produces:

```go
const (
	CommandBreakBlock CommandKind = 5
	CommandPlaceBlock CommandKind = 6
)

func LookDirection(yaw, pitch float32) mgl32.Vec3
```

- [ ] **Step 1: Write tests that cannot supply a ray origin**

Create `player_interaction_test.go` with:

```go
func TestBreakBlockUsesAuthoritativeEye(t *testing.T) {
	engine, session := readyFlatPlayer(t)
	engine.Enqueue(sim.Command{
		Session: session, Sequence: 2, Kind: sim.CommandBreakBlock,
		Yaw: 0, Pitch: -float32(math.Pi)/2 + 0.01,
	})
	result := engine.Step()
	if len(result.Changes) != 1 || result.Changes[0].Changes[0].Position != (core.BlockPos{X: 0, Y: 0, Z: 0}) {
		t.Fatalf("权威眼睛射线 changes=%+v", result.Changes)
	}
}

func TestPlaceBlockRejectsCompletePlayerAABBOverlap(t *testing.T) {
	engine, session := readyFlatPlayerWithTarget(t)
	engine.Enqueue(sim.Command{
		Session: session, Sequence: 2, Kind: sim.CommandPlaceBlock,
		Yaw: 0, Pitch: -float32(math.Pi)/2 + 0.01,
		Block: core.StoneID,
	})
	result := engine.Step()
	if len(result.Rejected) != 1 || result.Rejected[0].Reason != sim.RejectOccupied {
		t.Fatalf("result=%+v", result)
	}
}
```

Add cases for PendingSpawn → `RejectPlayerNotReady`, invalid action look → `RejectInvalidInput`, six-block reach, protected bedrock, valid adjacent placement, and two same-tick actions observing sequence order.

- [ ] **Step 2: Run the new interaction tests and verify missing command kinds**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim -run "TestBreakBlockUsesAuthoritativeEye|TestPlaceBlockRejectsCompletePlayerAABBOverlap|TestPlayerInteraction" -v'
```

Expected: FAIL because only client-supplied ray commands exist.

- [ ] **Step 3: Add intent actions while retaining transitional M2A paths**

During command ingestion, validate action yaw/pitch and update the player's yaw, pitch, and held `physics.Input.Yaw` for valid actions before the movement phase; invalid actions append `RejectInvalidInput` and do not update look. Collect valid action commands in sequence order. Updating held yaw ensures this tick's horizontal movement uses the final valid look even when a click occurs after the latest `PlayerInput`.

After physics and subscription reconciliation, build the ray only from:

```go
origin := player.state.Position.Add(mgl32.Vec3{0, physics.EyeHeight, 0})
direction := LookDirection(command.Yaw, command.Pitch)
```

Use the existing reach and block whitelist. Replace `blockContains(target, origin)` for the new placement path with the world-space target collision boxes tested against `physics.PlayerBounds(player.state.Position)` via `AABB.Overlaps`.

Keep `CommandBreakRay`/`CommandPlaceRay` only as transitional branches for existing tests and app; Task 14 removes them. New intent commands must never read `Command.Origin` or `Command.Direction`.

- [ ] **Step 4: Run all sim tests with race detection**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim -race -count=1'
```

Expected: PASS for new authority tests and existing M2A regression tests.

- [ ] **Step 5: Commit**

```bash
git add internal/sim/command.go internal/sim/engine.go internal/sim/interaction_test.go internal/sim/player_interaction_test.go
git commit -m "feat: 由权威玩家姿态执行交互"
```

---

### Task 9: Wire Player Commands and State Publication Through the Embedded Server

**Files:**
- Modify: `internal/server/config.go`
- Modify: `internal/server/server.go`
- Modify: `internal/server/publication.go`
- Modify: `internal/server/session.go`
- Create: `internal/server/player_test.go`
- Modify: `internal/server/integration_test.go`
- Modify: `internal/server/publication_test.go`
- Modify: `internal/server/session_test.go`

**Interfaces:**
- Consumes: Task 5 network messages and Task 6–8 sim commands/results
- Produces:

```go
// Config gains trusted server-owned spawn fields:
SpawnDimension core.DimensionID
SpawnAnchor    core.ChunkPos

func (s *Server) PlayerState() (sim.PlayerUpdate, bool)
```

- [ ] **Step 1: Write translation and FIFO publication tests**

Test that `server.New` registers local session 1 without a client message, and that the first `StepForTest` produces generation requests around `SpawnAnchor`.

Add a real MemoryTransport test:

```go
func TestServerPublishesPlayerStateAndInputAck(t *testing.T) {
	clientEndpoint, serverEndpoint := network.NewMemoryPair(32)
	config := server.DefaultConfig(42)
	config.ViewRadius, config.Workers = 0, 1
	running := server.New(config, serverEndpoint, flatTestGenerator{})
	waitForReadyPlayer(t, running, clientEndpoint)
	sendClientMessage(t, clientEndpoint, network.PlayerInput{
		Sequence: 1, MoveZ: 1, Yaw: 0, Pitch: 0,
	})
	running.StepForTest()
	state := receivePlayerState(t, clientEndpoint)
	if !state.Ready || state.LastInputSequence != 1 || state.ServerTick == 0 {
		t.Fatalf("state=%+v", state)
	}
}
```

Also assert `BreakBlock`/`PlaceBlock` translate without origin/direction, new reject reasons map exactly, and each tick's `PlayerState` is enqueued after that tick's snapshots/deltas/rejections while preserving FIFO.

- [ ] **Step 2: Run server tests and verify no M2B translation/publication exists**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "TestServerPublishesPlayerStateAndInputAck|TestPlayerMessageTranslation|TestPlayerStatePublicationOrder" -v'
```

Expected: FAIL because server construction does not register a player and does not translate/publish M2B messages.

- [ ] **Step 3: Register the player, translate commands, and publish snapshots**

Set defaults:

```go
SpawnDimension: core.Overworld,
SpawnAnchor:    core.ChunkPos{},
```

Validate that `SpawnDimension == core.Overworld` for M2B. In `server.New`, call `engine.RegisterSession(localSessionID, config.SpawnDimension, config.SpawnAnchor)` before starting reader/workers.

Translate new messages field-for-field to `sim.CommandPlayerInput`, `sim.CommandBreakBlock`, and `sim.CommandPlaceBlock`. Extend `networkRejectReason` for `RejectInvalidInput` and `RejectPlayerNotReady`.

At the start of `publish`, find the local `PlayerUpdate`, update `session.viewDimension/viewCenter` for snapshot-distance ordering, and enqueue this value after world messages:

```go
network.PlayerState{
	ServerTick:        result.Tick,
	LastInputSequence: player.LastInputSequence,
	Dimension:         player.Dimension,
	Position:          player.State.Position,
	Velocity:          player.State.Velocity,
	Yaw:               player.Yaw,
	Pitch:             player.Pitch,
	OnGround:          player.State.OnGround,
	Ready:             player.Ready,
	Reset:             player.Reset,
}
```

Update server test drains to route `network.PlayerState` to test collectors instead of `Mirror.Apply`; Mirror must continue rejecting it. Existing snapshot/delta tests should explicitly ignore player messages when they are not under test.

Implement `Server.PlayerState` under `stepMu` as a copy-returning wrapper around `engine.Player(localSessionID)` so integration tests never read mutable Engine fields directly.

- [ ] **Step 4: Run server, network, sim, and client race tests**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server ./internal/network ./internal/sim ./internal/client -race -count=1'
```

Expected: PASS; a new server autonomously starts PendingSpawn and publishes one player state per tick.

- [ ] **Step 5: Commit**

```bash
git add internal/server
git commit -m "feat: 发布权威玩家状态"
```

---

### Task 10: Implement Mirror-Backed Fixed-Step Client Prediction

**Files:**
- Create: `internal/client/collision.go`
- Create: `internal/client/collision_test.go`
- Create: `internal/client/predictor.go`
- Create: `internal/client/predictor_test.go`

**Interfaces:**
- Consumes: `client.Mirror`, `physics.Step`, `network.PlayerInput/PlayerState`
- Produces:

```go
type Control struct {
	MoveX int8
	MoveZ int8
	Jump  bool
	Yaw   float32
	Pitch float32
}

type ReconcileResult struct {
	ResetView bool
	Yaw       float32
	Pitch     float32
}

type predictedInput struct {
	sequence uint64
	input    physics.Input
}

type Predictor struct {
	ready               bool
	dimension           core.DimensionID
	current, previous   physics.State
	accumulator          time.Duration
	history              []predictedInput
	lastServerTick       uint64
	maxSentInput         uint64
	suspended            bool
	suspendSequence      uint64
	displayOffset        mgl32.Vec3
	correctionRemaining  time.Duration
}

func NewPredictor() *Predictor
func (p *Predictor) Begin(network.PlayerState) error
func (p *Predictor) Advance(
	elapsed time.Duration,
	control Control,
	source physics.CollisionSource,
	nextSequence func() uint64,
	send func(network.PlayerInput) error,
) error
func (p *Predictor) State() (physics.State, bool)
func (p *Predictor) HistoryLen() int
func (p *Predictor) Suspended() bool

type MirrorCollisionSource struct {
	Mirror    *Mirror
	Dimension core.DimensionID
}
```

- [ ] **Step 1: Write collision adapter and fixed-step/history tests**

Assert loaded air returns `{Loaded:true, Count:0}`, loaded stone returns the Task 1 full cube, and missing Mirror chunks return `{Loaded:false}`.

Add predictor tests:

```go
func TestPredictorRunsAtMostFiveFixedStepsPerFrame(t *testing.T) {
	p := readyPredictor(t)
	var sent []network.PlayerInput
	var sequence uint64
	err := p.Advance(260*time.Millisecond, client.Control{MoveZ: 1}, flatClientWorld(),
		func() uint64 { sequence++; return sequence },
		func(input network.PlayerInput) error { sent = append(sent, input); return nil })
	if err != nil { t.Fatal(err) }
	if len(sent) != 5 || p.HistoryLen() != 5 {
		t.Fatalf("sent=%d history=%d", len(sent), p.HistoryLen())
	}
}

func TestPredictorStopsAtUnknownMirrorBoundary(t *testing.T) {
	p := predictorNearMissingChunk(t)
	advanceOneStep(t, p, client.Control{MoveX: 1})
	state, _ := p.State()
	if state.Position.X() > 15.7+1e-5 {
		t.Fatalf("预测进入未知区块: %+v", state)
	}
}
```

Fill the history with 256 unacknowledged steps, then assert the next call sets `Suspended` and emits a neutral input not added to history. A successful neutral send is not retried; when the sender returns an error, subsequent calls retry no faster than every 50 ms and never change predicted position.

- [ ] **Step 2: Run client tests and verify predictor APIs are missing**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/client -run "TestMirrorCollisionSource|TestPredictor" -v'
```

Expected: FAIL because collision adapter and predictor do not exist.

- [ ] **Step 3: Implement bounded fixed-step prediction**

`NewPredictor` preallocates `history` with length 0 and capacity 256. `Begin` validates a Ready finite `PlayerState`, initializes current/previous physics states, dimension, last server tick, last input ack, and clears accumulator/history/suspension without discarding that capacity.

`Advance` adds elapsed time, caps catch-up at five steps, and for each step:

```go
message := network.PlayerInput{
	Sequence: nextSequence(), MoveX: control.MoveX, MoveZ: control.MoveZ,
	Jump: control.Jump, Yaw: control.Yaw, Pitch: control.Pitch,
}
if err := send(message); err != nil { return err }
p.maxSentInput = message.Sequence
p.history = append(p.history, predictedInput{
	sequence: message.Sequence,
	input: physics.Input{MoveX: message.MoveX, MoveZ: message.MoveZ, Jump: message.Jump, Yaw: message.Yaw},
})
p.previous = p.current
p.current = physics.Step(p.current, p.history[len(p.history)-1].input, source).State
```

Reject invalid Control before allocating a sequence. If elapsed would require more than five steps, run exactly five and set accumulator to zero.

At history length 256, enter suspension before appending: send neutral axes/Jump using a fresh sequence and, only after successful send, store it in both `suspendSequence` and `maxSentInput`. Do not step. If sending fails, retry with a fresh sequence every fixed interval until one send succeeds; after success, stop retrying and wait for Task 11 to observe acknowledgement of that fixed `suspendSequence`.

- [ ] **Step 4: Run client and physics race tests**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/client ./internal/physics -race -count=1'
```

Expected: PASS; prediction is fixed-step, bounded, and cannot cross an absent Mirror chunk.

- [ ] **Step 5: Commit**

```bash
git add internal/client/collision.go internal/client/collision_test.go internal/client/predictor.go internal/client/predictor_test.go
git commit -m "feat: 客户端固定步玩家预测"
```

---

### Task 11: Reconcile Authoritative Player States and Smooth Presentation

**Files:**
- Modify: `internal/client/predictor.go`
- Modify: `internal/client/predictor_test.go`
- Create: `internal/client/predictor_fuzz_test.go`

**Interfaces:**
- Consumes: Task 10 bounded history and `network.PlayerState`
- Produces:

```go
func (p *Predictor) ApplyPlayerState(
	state network.PlayerState,
	source physics.CollisionSource,
) (ReconcileResult, error)

func (p *Predictor) PresentationPosition(
	frameElapsed time.Duration,
) (mgl32.Vec3, bool)
```

- [ ] **Step 1: Write failing acknowledgement, replay, validation, and smoothing tests**

Add these scenarios:

```go
func TestApplyPlayerStateReplaysOnlyUnacknowledgedInputs(t *testing.T) {
	p := readyPredictor(t)
	advanceSteps(t, p, 3, client.Control{MoveZ: 1})
	authority := network.PlayerState{
		ServerTick: 2, LastInputSequence: 1, Dimension: core.Overworld,
		Position: mgl32.Vec3{0.5, 1, 0.4}, OnGround: true, Ready: true,
	}
	_, err := p.ApplyPlayerState(authority, flatClientWorld())
	if err != nil { t.Fatal(err) }
	if p.HistoryLen() != 2 {
		t.Fatalf("history=%d，想要重放 sequence 2,3", p.HistoryLen())
	}
	want := replayFromAuthority(authority, inputs(2, 3), flatClientWorld())
	got, _ := p.State()
	assertStateNear(t, got, want, 1e-6)
}

func TestSmallCorrectionDecaysInExactlyHundredMilliseconds(t *testing.T) {
	p := readyPredictor(t)
	before, _ := p.PresentationPosition(0)
	state := authorityOffsetBy(t, p, mgl32.Vec3{0.25, 0, 0})
	if _, err := p.ApplyPlayerState(state, flatClientWorld()); err != nil { t.Fatal(err) }
	mid, _ := p.PresentationPosition(50 * time.Millisecond)
	end, _ := p.PresentationPosition(50 * time.Millisecond)
	if distance(mid, before) <= 0 || distance(mid, before) >= 0.25 {
		t.Fatalf("50ms 中点=%v before=%v", mid, before)
	}
	predicted, _ := p.State()
	if !end.ApproxEqualThreshold(predicted.Position, 1e-6) {
		t.Fatalf("100ms 后=%v，想要 %v", end, predicted.Position)
	}
}
```

Also test:

- error `<1/128` creates no display offset;
- error `>=0.5`, `Reset=true`, or dimension change snaps immediately;
- stale/equal `ServerTick` is ignored;
- NaN/Inf, invalid pitch, unknown dimension, ack greater than maximum sent input, and `Reset && !Ready` return error atomically;
- `Ready=false` clears active prediction and produces no presentation position;
- ordinary state does not request a view reset, while Reset/dimension change returns server yaw/pitch;
- suspended prediction resumes only when `LastInputSequence >= suspendSequence`;
- a correction arriving during decay recomputes from the actually displayed position.

- [ ] **Step 2: Run reconciliation tests and verify `ApplyPlayerState` is missing**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/client -run "TestApplyPlayerState|TestSmallCorrection|TestLargeCorrection|TestSuspendedPredictor|TestInvalidPlayerState" -v'
```

Expected: FAIL because Task 10 only initializes and advances prediction.

- [ ] **Step 3: Implement atomic validation, replay, and presentation offset**

Validate the complete incoming message into local temporaries before mutating `Predictor`. Ignore stale server ticks without error. For an ordinary Ready update:

```go
oldDisplayed := p.presentationPositionNoAdvance()
oldPredicted := p.current.Position

p.current = physics.State{
	Position: state.Position,
	Velocity: state.Velocity,
	OnGround: state.OnGround,
}
p.previous = p.current
p.dropAcknowledged(state.LastInputSequence)
for _, entry := range p.history {
	p.previous = p.current
	p.current = physics.Step(p.current, entry.input, source).State
}
errorDistance := p.current.Position.Sub(oldPredicted).Len()
```

If the predictor is not Ready and a valid Ready state arrives, initialize through `Begin`, return `ResetView=true`, and use the server yaw/pitch. This is the normal first-spawn path and performs no history replay.

For `1/128 <= errorDistance < 0.5`, set `displayOffset = oldDisplayed - p.current.Position` and `correctionRemaining = 100*time.Millisecond`. `PresentationPosition` first linearly interpolates previous/current using `accumulator/physics.FixedDelta`, adds the current offset, then reduces offset linearly by `min(frameElapsed, correctionRemaining)/correctionRemaining`. At 100 ms the stored offset is exactly zero.

For Ready=false, clear state/history/accumulator/suspension and remember server tick. For Reset/dimension change, call the same reset path as `Begin`, return `ResetView=true`, and snap presentation. Ordinary updates retain local live view.

When suspended, ignore replay history until the state acknowledges `suspendSequence`; that state clears the stale history and resumes from authority.

- [ ] **Step 4: Run client fuzz smoke and race tests**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/client -fuzz FuzzPlayerStateValidationIsAtomic -fuzztime=5s'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/client ./internal/physics -race -count=1'
```

Expected: no fuzz failure and all client/physics tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/client/predictor.go internal/client/predictor_test.go internal/client/predictor_fuzz_test.go
git commit -m "feat: 客户端权威状态和解与平滑"
```

---

### Task 12: Replace Interactive Free Flight with Grounded Player Controls

**Files:**
- Modify: `internal/client/input.go`
- Modify: `internal/client/input_test.go`
- Modify: `internal/client/camera.go`
- Modify: `cmd/mcgo/app.go`
- Modify: `cmd/mcgo/main.go`

**Interfaces:**
- Consumes: Predictor, new protocol, existing Window keys and renderer
- Produces:

```go
type Movement struct {
	MoveX int8
	MoveZ int8
	Jump  bool
}

func MovementFromKeys(w, a, s, d, jump bool) Movement
```

- [ ] **Step 1: Write digital-control tests**

Extend `input_test.go`:

```go
func TestMovementFromKeysCancelsOpposites(t *testing.T) {
	tests := []struct {
		name string
		w, a, s, d, jump bool
		want client.Movement
	}{
		{"forward right jump", true, false, false, true, true, client.Movement{MoveX: 1, MoveZ: 1, Jump: true}},
		{"opposites cancel", true, true, true, true, false, client.Movement{}},
		{"back left", false, true, true, false, false, client.Movement{MoveX: -1, MoveZ: -1}},
	}
	for _, tc := range tests {
		if got := client.MovementFromKeys(tc.w, tc.a, tc.s, tc.d, tc.jump); got != tc.want {
			t.Fatalf("%s=%+v，想要 %+v", tc.name, got, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run input tests and verify movement mapping is missing**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/client -run "TestMovementFromKeys|TestInputStateUsesRisingEdges" -v'
```

Expected: FAIL because `MovementFromKeys` does not exist.

- [ ] **Step 3: Wire predictor, state dispatch, camera, and action intents**

Add `predictor *client.Predictor` and a single app-global sequence allocator:

```go
func (a *application) nextSequence() uint64 {
	a.sequence++
	return a.sequence
}
```

In `drainServerMessages`, dispatch `network.PlayerState` to `predictor.ApplyPlayerState` with `client.MirrorCollisionSource`. On error, close Transport and cancel server exactly like an invalid snapshot. Apply returned yaw/pitch only when `ResetView` is true. Continue sending all world messages to `Mirror.Apply`; never pass `PlayerState` to Mirror.

Replace `breakBlock`/`placeBlock` messages with:

```go
network.BreakBlock{Sequence: a.nextSequence(), Yaw: a.camera.Yaw, Pitch: a.camera.Pitch}
network.PlaceBlock{Sequence: a.nextSequence(), Yaw: a.camera.Yaw, Pitch: a.camera.Pitch, Block: a.selectedBlock}
```

In `runInteractive`, keep mouse rotation at render frequency, derive digital movement from WASD/Space, and call predictor Advance with the frame delta. Remove interactive `Camera.Move`, Shift descent, Control boost, and `updateCenter`. When prediction is Ready, set:

```go
feet, _ := app.predictor.PresentationPosition(dt)
app.camera.Pos = feet.Add(mgl32.Vec3{0, physics.EyeHeight, 0})
```

If cursor capture is released, feed neutral movement but continue fixed-step messages so the server cannot retain a held key. Keep `Camera.Move` available only for the benchmark script until Task 14 migrates it.

Before sending `BreakBlock` or `PlaceBlock`, require `predictor.State()` to report Ready; clicks during PendingSpawn remain local no-ops and cannot produce `player_not_ready` spam.

Change the window title/comment from M2A/free flight to M2B authoritative player. Do not launch the application during this task.

- [ ] **Step 4: Build the command and run client/server tests without opening a window**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/client ./internal/server ./cmd/mcgo -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go build ./cmd/mcgo'
```

Expected: PASS. `go test`/`go build` must not call `main` and must not create a foreground window.

- [ ] **Step 5: Commit**

```bash
git add internal/client/input.go internal/client/input_test.go internal/client/camera.go cmd/mcgo/app.go cmd/mcgo/main.go
git commit -m "feat: 接入地面玩家控制闭环"
```

---

### Task 13: Prove Delayed End-to-End Player and World Convergence

**Files:**
- Create: `internal/server/player_integration_test.go`
- Modify: `internal/server/test_generator_test.go`
- Modify: `internal/server/integration_test.go`

**Interfaces:**
- Consumes: real MemoryTransport, Server, Mirror, Predictor, authority interactions
- Produces: deterministic three-tick (`150 ms`) delayed-state convergence gate

- [ ] **Step 1: Write the full headless delayed-state scenario**

Create a deterministic test driver that:

- calls `Server.StepForTest` instead of sleeping;
- drains world messages immediately into Mirror;
- queues each `network.PlayerState` with `deliverAtTick = state.ServerTick + 3`;
- applies due states to Predictor before producing that client tick's input;
- uses real `ClientEndpoint.Send` for every player/action message.

The test sequence is exact:

```go
func TestAuthoritativePlayerConvergesAfterThreeTickStateDelay(t *testing.T) {
	h := newDelayedPlayerHarness(t, 3)
	h.waitReady()
	h.hold(client.Movement{MoveZ: 1}, 20) // accelerate and walk one second
	h.hold(client.Movement{}, 10)         // friction stop
	h.hold(client.Movement{MoveZ: 1, Jump: true}, 1)
	h.hold(client.Movement{MoveZ: 1}, 20) // clear the one-block test obstacle without tunneling
	h.clickPlaceDown(core.StoneID)        // target above intact support overlaps player: occupied
	h.clickBreakDown()                    // then break support through the authoritative eye ray
	h.hold(client.Movement{}, 10)
	h.flushAllStates()
	h.assertConverged(1e-5)
	h.assertWorldHashesEqual()
	h.closeAndAssertNoGoroutineLeak(time.Second)
}
```

Use a flat test generator that places a one-block obstacle far enough ahead for a legal jump. Record the `occupied` rejection but require the break command to produce exactly one contiguous block revision. After flush, assert predictor history is zero and client/server position, velocity, OnGround, dimension, world hash and revision agree.

Add a second replay test that runs this command script twice without real time and compares the final `Server.PlayerState`, `PlayerHash`, chunk hash, revision and rejection sequence exactly.

- [ ] **Step 2: Run the test and verify missing harness or convergence defects**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "TestAuthoritativePlayerConvergesAfterThreeTickStateDelay|TestAuthoritativePlayerReplayIsDeterministic" -v -count=1'
```

Expected: initial FAIL until all state routing, delayed replay, action ordering and final convergence helpers are correct.

- [ ] **Step 3: Fix only integration defects exposed by the test**

Keep the delay queue inside `_test.go`; do not add a production latency Transport. If world messages and `PlayerState` share a receive stream, classify by concrete type before applying delay. Ensure `flushAllStates` sends a neutral movement input, advances server/predictor until its sequence is acknowledged, then drains the three remaining delayed ticks.

If the test reveals an ordering issue, preserve the spec order: input validation → generated chunks → one player physics step → subscription reconciliation → actions → world/player publication. Do not loosen the `1e-5` final tolerance or hash equality.

- [ ] **Step 4: Run server/client/sim race tests**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server ./internal/client ./internal/sim -race -count=1'
```

Expected: PASS, including both delayed convergence and goroutine shutdown within one second.

- [ ] **Step 5: Commit**

```bash
git add internal/server/player_integration_test.go internal/server/test_generator_test.go internal/server/integration_test.go
git commit -m "test: 锁定权威玩家延迟收敛"
```

---

### Task 14: Retire Client-Controlled Rays and View Centers Behind a Trusted Benchmark Observer

**Files:**
- Modify: `internal/server/config.go`
- Modify: `internal/server/server.go`
- Modify: `internal/server/player_test.go`
- Modify: `internal/server/generator_test.go`
- Modify: `internal/server/publication_test.go`
- Modify: `internal/server/subscription_test.go`
- Modify: `internal/network/message.go`
- Modify: `internal/network/message_test.go`
- Modify: `internal/network/memory_test.go`
- Modify: `internal/archcheck/deps_test.go`
- Modify: `internal/sim/command.go`
- Modify: `internal/sim/engine.go`
- Modify: `internal/sim/engine_test.go`
- Modify: `internal/sim/interaction_test.go`
- Modify: `internal/sim/bench_test.go`
- Modify: `internal/server/integration_test.go`
- Modify: `cmd/mcgo/app.go`
- Modify: `cmd/mcgo/benchmark.go`

**Interfaces:**
- Consumes: normal authority path completed in Tasks 6–13
- Produces:

```go
var ErrTrustedObserverDisabled = errors.New("server: trusted observer disabled")

// Config gains a benchmark-only, default-false switch:
TrustedObserver bool

func (s *Server) SetTrustedObserverCenter(
	core.DimensionID,
	core.ChunkPos,
) error
```

- [ ] **Step 1: Write security-boundary and observer tests**

Add tests proving:

```go
func TestTrustedObserverIsDisabledByDefault(t *testing.T) {
	running := newDefaultTestServer(t)
	err := running.SetTrustedObserverCenter(core.Overworld, core.ChunkPos{X: 99})
	if !errors.Is(err, server.ErrTrustedObserverDisabled) {
		t.Fatalf("err=%v", err)
	}
}

func TestTrustedObserverCoalescesCenterAndDrivesGeneration(t *testing.T) {
	running := newTrustedObserverTestServer(t)
	for x := int32(1); x <= 3; x++ {
		if err := running.SetTrustedObserverCenter(core.Overworld, core.ChunkPos{X: x}); err != nil { t.Fatal(err) }
	}
	result := running.StepForTest()
	if !containsChunk(result.Generate, core.ChunkPos{X: 3}) || containsChunk(result.Generate, core.ChunkPos{X: 1}) {
		t.Fatalf("Generate=%+v", result.Generate)
	}
}
```

Update sealed-protocol tests so the final client message set contains only `PlayerInput`, `BreakBlock`, `PlaceBlock`, and `RequestChunkResync`. Add `TestLegacyPlayerAuthorityMessagesAreGone` to `internal/archcheck/deps_test.go`; it scans only `cmd/` and `internal/` Go sources and rejects the exact identifiers `SetViewCenter`, `BreakRay`, `PlaceRay`, `CommandBreakRay`, and `CommandPlaceRay`.

- [ ] **Step 2: Run focused tests and verify legacy messages still exist/observer is missing**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server ./internal/network ./internal/archcheck -run "TestTrustedObserver|TestProtocolMessageShapes|TestLegacyPlayerAuthorityMessagesAreGone" -v'
```

Expected: FAIL because trusted observer is absent and legacy messages remain.

- [ ] **Step 3: Add an internal observer queue and remove legacy authority paths**

When `TrustedObserver=false`, construct/register the normal player exactly as Task 9. When true, do not register a player; create a capacity-one trusted-center queue for the local publication session. `SetTrustedObserverCenter` validates Overworld, replaces any queued older center without blocking, and never writes Engine state directly.

At the beginning of `Server.Step`, drain the latest trusted center, increment a server-owned sequence, update snapshot-distance fields, and enqueue an internal `sim.CommandTrustedObserverCenter` for the view-only local session. This command remains in `sim` solely for the trusted server harness; it is not a `network.ClientMessage`.

After cleanup, rewrite the internal enum to this final order so no retired authority kind survives:

```go
const (
	CommandTrustedObserverCenter CommandKind = iota
	CommandPlayerInput
	CommandBreakBlock
	CommandPlaceBlock
	CommandResync
)
```

Delete:

- `network.SetViewCenter`, `network.BreakRay`, `network.PlaceRay` and marker methods;
- `sim.CommandSetViewCenter`; replace its trusted internal use with `sim.CommandTrustedObserverCenter`;
- `sim.CommandBreakRay`, `sim.CommandPlaceRay`, `Command.Origin`, `Command.Direction` and legacy execution branches;
- server translation branches for legacy messages;
- interactive `sendViewCenter` and all client Transport calls that choose a center;
- M2A tests that exercise client-supplied rays; rewrite them with registered players and intent actions.

Set `config.TrustedObserver = benchmark` in `newApplication`. In benchmark mode, `updateCenter` calls `server.SetTrustedObserverCenter` directly. Remove the per-second break/place script and accepted/rejected counters from the performance path. At the end, compare server/Mirror hash+revision for the final trusted center chunk. Set `scenarioVersion = 3`.

- [ ] **Step 4: Run full tests and verify no legacy identifier remains in code**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race -count=1'
test -z "$(rg -n --glob '!internal/archcheck/deps_test.go' 'SetViewCenter|BreakRay|PlaceRay|CommandBreakRay|CommandPlaceRay' cmd internal .github || true)"
zsh -ic 'gvm use go1.26.0 >/dev/null && go vet ./...'
```

Expected: tests and vet PASS; the source-scan substitution is empty and the shell expression exits zero. Do not run interactive `mcgo`.

- [ ] **Step 5: Commit**

```bash
git add internal/server internal/network internal/sim internal/archcheck cmd/mcgo/app.go cmd/mcgo/benchmark.go
git commit -m "refactor: 移除客户端位置与射线权限"
```

---

### Task 15: Establish Scenario v3 Performance and Final Consistency Gates

**Files:**
- Modify: `internal/client/perf_test.go`
- Modify: `.github/workflows/ci.yml`
- Modify: `docs/notes/perf-baseline.json`
- Modify: `docs/notes/perf-baseline.md`

**Interfaces:**
- Consumes: scenario v3 trusted observer and all M2B test/benchmark names
- Produces: reproducible v3 baseline and final M2B verification evidence

- [ ] **Step 1: Add v3 report and CI microbenchmark assertions**

Update `TestPerfReportJSONRoundTripIncludesTicks` to use `ScenarioVersion: 3`. Add a benchmark-report test that unmarshals a minimal v3 JSON and requires `still`, `flying`, tick metrics, and a non-empty framebuffer/hardware identifier.

Update the CI “架构与协议门禁” step to run:

```yaml
run: go test ./internal/archcheck ./internal/network ./internal/physics -v
```

Keep the microbenchmark step broad so all existing and new hot paths execute once:

```yaml
run: go test ./... -bench=. -benchtime=1x -run='^$'
```

- [ ] **Step 2: Run report/CI-equivalent tests before measuring**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/client ./internal/physics ./internal/sim ./internal/archcheck ./internal/network -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -bench=. -benchtime=1x -run="^$"'
```

Expected: PASS, and physics benchmarks report `0 allocs/op`.

- [ ] **Step 3: Run the full v3 Metal scenario headlessly and record exact evidence**

Run only the offscreen benchmark path; it must not create or focus a window:

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --perf-output /tmp/mcgo-m2b-perf.json'
```

Inspect the JSON and require:

- `scenario_version == 3`
- framebuffer exactly `2560x1440`
- both phases `fps >= 100`
- both phases `p99_ms < 12`
- both phases `peak_rss_bytes < 2147483648`
- tick `p99_ms < 10` and `max_ms < 50`
- benchmark's final server/Mirror hash check succeeded before the file was written

Use `apply_patch` to replace `docs/notes/perf-baseline.json` with the exact generated report plus a trailing newline. Update `perf-baseline.md` with the exact command, load/snapshot durations, phase FPS/p99/RSS, tick percentiles/max, physical microbenchmark ns/op and `0 B/op, 0 allocs/op`. Do not invent or round values beyond the displayed precision.

- [ ] **Step 4: Run final full verification and same-machine perf comparison**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go vet ./...'
zsh -ic 'gvm use go1.26.0 >/dev/null && test -z "$(gofmt -l .)"'
git diff --check
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck -baseline docs/notes/perf-baseline.json -current /tmp/mcgo-m2b-perf.json -max-regression 0.20'
```

Expected: all commands exit zero; race reports no races, gofmt prints nothing, diff check is clean, and perfcheck prints `性能比较通过：所有阶段退化均未超过阈值`.

- [ ] **Step 5: Commit**

```bash
git add internal/client/perf_test.go .github/workflows/ci.yml docs/notes/perf-baseline.json docs/notes/perf-baseline.md
git commit -m "chore: M2B 玩家场景性能与一致性门禁"
```

After the commit, run:

```bash
git status --short --branch
git log -15 --oneline
```

Expected: clean worktree and exactly one reviewable commit per Task 1–15.
