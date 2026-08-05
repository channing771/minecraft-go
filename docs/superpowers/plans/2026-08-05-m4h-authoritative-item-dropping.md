# M4H 权威主动丢弃 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 Ready 玩家用 `Q` 的有效按键边沿请求把权威选中快捷栏中的一个物品原地转移为持久掉落物，并让 Memory/TCP、多人发布、正常关服重启和协议 v10 得到同一结果。

**Architecture:** `cmd/mcgo` 只发送携带全局 `Sequence` 的低频请求；`internal/server` 只做封闭消息到 `sim.Command` 的映射；`sim.Engine.Step` 在单写者 tick 内先预检权威玩家、栏位、脚底 Ready 区块和固定 32 槽容量，再提交快捷栏副本与现有区块掉落物。成功后复用既有 inventory/drop publication 和玩家/区块持久化，客户端不预测背包扣减或掉落物。

**Tech Stack:** Go 1.26、现有 `core.Inventory`/`world.Chunk`/`sim.Engine`、Memory/TCP binary codec、GLFW 输入、OpenSpec、Go race/fuzz/benchmark。

## Global Constraints

- [ ] 以 `openspec/changes/m4h-authoritative-item-dropping/` 下的 proposal、两份 delta spec、design 和 tasks 为规范来源；本计划只展开执行细节。若二者不一致，先更新 OpenSpec 与本计划，再写代码。
- [ ] M4G 已归档，当前基线必须是协议 v9、玩家 schema v3、区块 schema v4、metadata v2、benchmark scenario v12。
- [ ] 全程通过 `zsh -ic 'gvm use go1.26.0 >/dev/null && ...'` 使用现有 Go 1.26，不下载工具链或新增依赖。
- [ ] 每个实现任务遵循 RED → GREEN → REFACTOR → 定向 race → archcheck → gofmt/diff check → 独立提交；只有验证通过后才勾选对应 OpenSpec task。
- [ ] 自动验证不得启动或聚焦前台游戏窗口；只运行单元、集成、fuzz、benchmark 和无图形构建。
- [ ] 保留用户现有 stash 与任何无关改动；只暂存当前 M4H 任务的文件。
- [ ] 不新增内部包、接口、worker、goroutine、队列、锁、renderer、HUD、镜像字段、存档文件或第三方依赖。
- [ ] 不实现整组丢弃、数量选择、投掷速度、掉落物连续坐标、重力、碰撞、所有者、死亡掉落、客户端预测、WAL、跨文件事务、ECS 或 benchmark 场景升级。
- [ ] Play client packet ID `1` 继续未分配；既有 packet ID 与 payload 字节不重排，只新增 ID `11`。
- [ ] 主动丢弃固定拾取延迟为 `40` 个活动 tick；采掘与方块破坏保持 `10`；合并保留 ID、generation、年龄和 6000 tick 寿命时间线，并把剩余延迟提升为来源延迟与旧值的较大者。
- [ ] 玩家 schema v3、区块 schema v4、metadata v2 与 scenario v12 不变；不生成迁移器、baseline 或额外状态文件。
- [ ] 新增或修改的注释、测试说明和开发文档使用中文；Go 标识符、wire 字段和既有技术术语保留英文。
- [ ] Hook 或门禁失败只修根因，不改写、关闭或绕过 `scripts/agent-hooks/guard.mjs`。

---

## Files and Stable Interface Map

### Protocol v10 and registered-item boundary

Modify:

- `internal/network/message.go`
- `internal/network/registry.go`
- `internal/network/codec.go`
- `internal/network/packet.go`

Tests:

- `internal/network/codec_test.go`
- `internal/network/registry_test.go`
- `internal/network/packet_test.go`
- `internal/network/worldtime_test.go`
- `internal/network/drop_test.go`
- `internal/network/login_test.go`
- `internal/network/benchmark_test.go`

Stable additions:

```go
package network

const ProtocolVersion uint32 = 10

type DropSelectedItem struct {
	Sequence uint64
}

func (DropSelectedItem) clientMessage() {}
func (DropSelectedItem) clientPacket()  {}
func (DropSelectedItem) Validate() error { return nil }
```

Wire contract: Play client packet ID `11`; payload is exactly eight little-endian bytes containing `Sequence`. `ItemDrop.validate` accepts `core.RegisteredItem`, while unknown IDs still fail the whole upsert batch.

### Drop merge and authoritative state transition

Modify:

- `internal/world/drop.go`
- `internal/sim/command.go`
- `internal/sim/engine.go`
- `internal/sim/drop.go`

Tests:

- `internal/world/drop_test.go`
- `internal/sim/drop_test.go`
- `internal/sim/drop_command_test.go` (new, package `sim` for unreachable/corrupt-state guards)
- `internal/sim/bench_test.go`

Stable addition:

```go
package sim

const PlayerDropPickupDelayTicks = 40

const CommandDropSelectedItem CommandKind = 10
```

Private helper:

```go
func (engine *Engine) dropSelectedItem(
	session *sessionState,
	pending map[core.ChunkKey]*pendingChunkChanges,
) (RejectReason, bool)
```

The helper consumes no client slot or position. It reads `player.inventory.Hotbar.Selected` and floors `player.state.Position` on all three axes. It calls `PrepareDrop` before either value is committed, then assigns the `Hotbar.Consume` result, calls `CommitDrop`, sets `inventoryDirty`, and calls `touchChunk`.

`world.Chunk.CommitDrop` and `PrepareDropBatch` both apply this existing-source rule when merging:

```go
drop.PickupDelayTicks = max(drop.PickupDelayTicks, pickupDelay)
```

No drop generation, age, block index, item, or ID field is reset on merge.

### Server mapping and existing publication

Modify:

- `internal/server/session.go`

Tests:

- `internal/server/player_test.go`
- `internal/server/drop_publication_test.go`
- `internal/server/multiplayer_memory_integration_test.go`
- `internal/server/tcp_integration_test.go`

Stable mapping:

```go
case network.DropSelectedItem:
	return sim.Command{
		Session:  id,
		Sequence: message.Sequence,
		Kind:     sim.CommandDropSelectedItem,
	}, true
```

`server` must not revalidate inventory or duplicate state. Existing `publishInventories`, `publishDrops`, `networkRejectReason`, save scheduling and shutdown barriers remain the only publication/persistence paths.

### Q edge input and no-prediction client

Modify:

- `internal/client/window.go`
- `internal/client/input.go`
- `cmd/mcgo/main.go`
- `cmd/mcgo/app.go`

Tests:

- `internal/client/input_test.go`
- `cmd/mcgo/main_test.go`
- `cmd/mcgo/app_test.go`

Stable input shape:

```go
type Actions struct {
	Mining         bool
	Place          bool
	Select         bool
	SelectSlot     uint8
	ToggleInventory bool
	Click          bool
	Drop           bool
}

type InputState struct {
	primaryDown   bool
	secondaryDown bool
	numberDown    int
	inventoryDown bool
	dropDown      bool
}

func (state *InputState) Update(
	primary, secondary bool,
	number int,
	inventoryKey, dropKey, inventoryOpen bool,
) Actions
```

`InputState.Update` always records physical Q state, including while a container is open or the cursor is not captured. `application.dropSelectedItem` only checks predictor Ready, allocates `nextSequence()`, and sends `network.DropSelectedItem`; it does not read or mutate inventory/drop mirrors.

### Compatibility, persistence and docs

Modify tests/docs only as needed:

- `internal/server/drop_publication_test.go`
- `internal/server/tcp_integration_test.go`
- `internal/storage/chunk_drop_test.go`
- `internal/storage/player_codec_test.go`
- `internal/storage/metadata_test.go`
- `README.md`
- `docs/notes/lan-server.md`

Frozen values:

```text
protocol=10
player schema=3
chunk schema=4
metadata=2
benchmark scenario=12
```

Normal shutdown must flush both existing player and chunk save paths. Abnormal termination remains two independently atomic files with no cross-file transaction guarantee.

---

## Task 1: Freeze the M4G baseline and reconcile M4H artifacts

**Files:**

- Verify: `openspec/changes/archive/2026-08-05-m4g-authoritative-daylight/`
- Verify: `openspec/changes/m4h-authoritative-item-dropping/`
- Verify: `internal/network/packet.go`
- Verify: `internal/storage/player_codec.go`
- Verify: `internal/storage/chunk_codec.go`
- Verify: `internal/storage/metadata.go`
- Verify: `cmd/mcgo/benchmark.go`
- Verify: `docs/notes/perf-baseline-m5.json`

**Consumes:** approved M4H proposal, delta specs, design and tasks.

**Produces:** a clean, validated starting point; no code changes.

- [ ] Run the baseline checks:

```bash
openspec list --json
openspec validate --all --strict --no-interactive
test -d openspec/changes/archive/2026-08-05-m4g-authoritative-daylight
rg -n 'ProtocolVersion uint32 = 9|currentPlayerSchema.*= 3|currentChunkSchema.*= 4|currentMetadataVersion.*= 2|scenarioVersion[[:space:]]*= 12|"scenario_version": 12' internal cmd docs/notes/perf-baseline-m5.json
git status --short --branch
```

Expected: strict validation passes; M4G archive exists; all five frozen version values match; tracked worktree contains no unrelated edits.

- [ ] Read the four artifact classes in order and map every Requirement to Tasks 2–8 of this plan:

```bash
sed -n '1,260p' openspec/changes/m4h-authoritative-item-dropping/proposal.md
sed -n '1,340p' openspec/changes/m4h-authoritative-item-dropping/specs/authoritative-item-dropping/spec.md
sed -n '1,260p' openspec/changes/m4h-authoritative-item-dropping/specs/persistent-item-drops/spec.md
sed -n '1,360p' openspec/changes/m4h-authoritative-item-dropping/design.md
sed -n '1,300p' openspec/changes/m4h-authoritative-item-dropping/tasks.md
```

- [ ] Confirm the only implementation scope is: Q single-item request, protocol v10, registered-item drop validation, source-delay merge, server-authoritative state transfer, Memory/TCP publication, normal restart persistence and docs. Stop if physics, full-stack drop, owner cooldown, WAL/ECS, new UI or scenario v13 appears.

- [ ] No commit is required for a read-only baseline task.

## Task 2: Add protocol v10 and fix the shared registered-item validator

**Files:**

- Modify: `internal/network/message.go`
- Modify: `internal/network/registry.go`
- Modify: `internal/network/codec.go`
- Modify: `internal/network/packet.go`
- Modify: `internal/network/codec_test.go`
- Modify: `internal/network/registry_test.go`
- Modify: `internal/network/packet_test.go`
- Modify: `internal/network/worldtime_test.go`
- Modify: `internal/network/drop_test.go`
- Modify: `internal/network/login_test.go`
- Modify: `internal/network/benchmark_test.go`

**Consumes:** `Sequence uint64`, fixed packet ID `11`, existing closed ClientMessage/ClientPacket sets.

**Produces:** protocol v10 codec/registry/login behavior and one shared `RegisteredItem` validation boundary.

- [ ] Write the failing wire and compatibility tests first. Add this golden row to `TestProtocolV1SmallPacketGolden` and update version-only fixtures from `09` to `0a`:

```go
{"drop selected item", StatePlay, DropSelectedItem{
	Sequence: 0x1122334455667788,
}, 11, "8877665544332211"},
```

- [ ] Extend the frozen registry test without changing existing IDs:

```go
assertClientRegistry(t, []struct {
	state  State
	packet ClientPacket
	id     uint32
}{
	{StatePlay, DropSelectedItem{}, 11},
})
if _, ok := clientPacketForID(StatePlay, 1); ok {
	t.Fatal("Play client packet ID 1 必须保持未分配")
}
```

- [ ] Add a registered-item regression to `internal/network/drop_test.go`:

```go
func TestItemDropUpsertsAcceptEveryRegisteredItem(t *testing.T) {
	for _, item := range []core.ItemID{
		core.ItemCoal,
		core.ItemRawIron,
		core.ItemIronIngot,
		core.ItemStonePickaxe,
		core.ItemIronPickaxe,
	} {
		message := ItemDropUpserts{Drops: []ItemDrop{{
			ID: dropTestID(0, 1), BlockIndex: 9, Item: item, Count: 1,
		}}}
		if err := message.Validate(); err != nil {
			t.Fatalf("已注册物品 %d 被拒绝: %v", item, err)
		}
		packetID, payload, err := encodeServerControlPayload(StatePlay, message)
		if err != nil {
			t.Fatalf("编码已注册物品 %d: %v", item, err)
		}
		if _, err := decodeServerControlPayload(StatePlay, packetID, payload); err != nil {
			t.Fatalf("解码已注册物品 %d: %v", item, err)
		}
	}
}
```

- [ ] Update `worldtime_test.go`, `packet_test.go` and login tests so `ProtocolVersion == 10` and v9 is explicitly the immediately previous version rejected before Login/Play.

- [ ] Run RED and confirm failures name the missing type/ID/version and the current `ItemPlacement` restriction:

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "DropSelectedItem|RegisteredItem|ProtocolVersion|RejectsVersion" -count=1'
```

- [ ] Add the message and validators in `message.go`/`packet.go`:

```go
type DropSelectedItem struct {
	Sequence uint64
}

func (DropSelectedItem) clientMessage() {}
func (DropSelectedItem) clientPacket()  {}
func (DropSelectedItem) Validate() error { return nil }
```

```go
case DropSelectedItem:
	return clientPacket.Validate()
```

- [ ] Register ID `11` in both directions in `registry.go`, leaving ID `1` absent:

```go
case DropSelectedItem:
	return 11, true
```

```go
case 11:
	return DropSelectedItem{}, true
```

- [ ] Encode/decode exactly one `uint64` in `codec.go`:

```go
case DropSelectedItem:
	e.u64(message.Sequence)
```

```go
case 11:
	var drop DropSelectedItem
	drop.Sequence, err = d.u64()
	packet = drop
```

- [ ] Update `sameClientPacket`, `sameClientPacketType`, fuzz seeds/closed-set helpers and `BenchmarkSmallPacketCodec` with `DropSelectedItem{Sequence: 12}`.

- [ ] Fix the shared root cause in `ItemDrop.validate`:

```go
if !core.RegisteredItem(drop.Item) {
	return errors.New("network: unknown item drop item")
}
```

- [ ] Run GREEN, fuzz, benchmark and package gates:

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "^$" -fuzz FuzzSmallPacketCodec -fuzztime=10s'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "^$" -bench SmallPacketCodec -benchmem -count=3'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
gofmt -w internal/network/message.go internal/network/registry.go internal/network/codec.go internal/network/packet.go internal/network/codec_test.go internal/network/registry_test.go internal/network/packet_test.go internal/network/worldtime_test.go internal/network/drop_test.go internal/network/login_test.go internal/network/benchmark_test.go
gofmt -l .
git diff --check
```

- [ ] Commit only the protocol/validator slice:

```bash
git add internal/network
git commit -m "feat: 新增主动丢弃协议"
```

## Task 3: Make every drop-source merge preserve the longer pickup delay

**Files:**

- Modify: `internal/world/drop.go`
- Modify: `internal/world/drop_test.go`

**Consumes:** incoming source delay passed to `CommitDrop` or `PrepareDropBatch`.

**Produces:** one deterministic merge rule for single and batch drop creation.

- [ ] Replace the old “merge preserves delay unchanged” assertion with a table covering shorter/equal/longer incoming delays and preservation of generation/age:

```go
func TestChunkCommitDropMergeUsesLongerPickupDelay(t *testing.T) {
	for _, test := range []struct {
		name     string
		old      uint8
		incoming uint8
		want     uint8
	}{
		{name: "incoming shorter", old: 40, incoming: 10, want: 40},
		{name: "incoming equal", old: 10, incoming: 10, want: 10},
		{name: "incoming longer", old: 5, incoming: 40, want: 40},
	} {
		t.Run(test.name, func(t *testing.T) {
			chunk := dropTestChunk(t)
			index := dropTestIndex(t, core.BlockPos{X: 16, Y: 3, Z: -32})
			chunk.SetDrop(2, world.DropSlot{
				Generation: 7, Active: true,
				Stack: core.ItemStack{Item: core.ItemDirt, Count: 63},
				BlockIndex: index, AgeTicks: 99, PickupDelayTicks: test.old,
			})
			slot, ok := chunk.PrepareDrop(core.ItemDirt, index)
			if !ok || slot != 2 {
				t.Fatalf("预检 = (%d,%v)，想要 (2,true)", slot, ok)
			}
			generation := chunk.CommitDrop(slot, core.ItemDirt, index, test.incoming)
			got := chunk.Drop(slot)
			if generation != 7 || got.Generation != 7 || got.AgeTicks != 99 ||
				got.Stack.Count != 64 || got.PickupDelayTicks != test.want {
				t.Fatalf("合并结果 = %+v generation=%d", got, generation)
			}
		})
	}
}
```

- [ ] Add the matching batch regression because furnace destruction already uses `PrepareDropBatch`:

```go
func TestChunkPrepareDropBatchMergeUsesLongerPickupDelay(t *testing.T) {
	chunk := dropTestChunk(t)
	index := dropTestIndex(t, core.BlockPos{X: 16, Y: 3, Z: -32})
	chunk.SetDrop(0, world.DropSlot{
		Generation: 3, Active: true,
		Stack: core.ItemStack{Item: core.ItemCoal, Count: 1},
		BlockIndex: index, AgeTicks: 17, PickupDelayTicks: 2,
	})
	var stacks [4]core.ItemStack
	stacks[0] = core.ItemStack{Item: core.ItemCoal, Count: 1}
	next, ok := chunk.PrepareDropBatch(stacks, index, 10)
	if !ok {
		t.Fatal("batch 预检被拒绝")
	}
	got := next[0]
	if got.Generation != 3 || got.AgeTicks != 17 || got.Stack.Count != 2 ||
		got.PickupDelayTicks != 10 {
		t.Fatalf("batch 合并 = %+v", got)
	}
}
```

- [ ] Run RED:

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/world -run "MergeUsesLongerPickupDelay" -count=1'
```

- [ ] Apply the two one-line root fixes, without a new helper or abstraction:

```go
if drop.Active {
	drop.Stack.Count++
	drop.PickupDelayTicks = max(drop.PickupDelayTicks, pickupDelay)
	c.drops[slot] = drop
	return drop.Generation
}
```

```go
if drop.Active {
	space := core.MaxStackCount - drop.Stack.Count
	taken := min(space, remaining)
	drop.Stack.Count += taken
	drop.PickupDelayTicks = max(drop.PickupDelayTicks, pickupDelay)
	remaining -= taken
	next[slot] = drop
	continue
}
```

- [ ] Run GREEN and gates:

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/world ./internal/sim -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
gofmt -w internal/world/drop.go internal/world/drop_test.go
gofmt -l .
git diff --check
```

- [ ] Commit:

```bash
git add internal/world/drop.go internal/world/drop_test.go
git commit -m "fix: 保留较长掉落拾取延迟"
```

## Task 4: Implement the atomic authoritative single-item transfer

**Files:**

- Modify: `internal/sim/command.go`
- Modify: `internal/sim/engine.go`
- Modify: `internal/sim/drop.go`
- Modify: `internal/sim/drop_test.go`
- Add: `internal/sim/drop_command_test.go`
- Modify: `internal/sim/bench_test.go`

**Consumes:** `CommandDropSelectedItem`, current session/player state and one shared `pending` map.

**Produces:** atomic inventory decrement plus drop create/merge, existing reject reasons, dirty inventory and one chunk revision barrier.

- [ ] Add a public-package success table in `drop_test.go` using the existing `readyFlatPlayerWithInventory`, `onlyDrop`, `PlayerSnapshot` and `CloneReadyChunk` helpers. Cover count `3→2`, count `1→empty`, coal and both pickaxes:

```go
func TestDropSelectedItemTransfersOneAuthoritativeItem(t *testing.T) {
	for _, item := range []core.ItemID{
		core.ItemStone,
		core.ItemCoal,
		core.ItemStonePickaxe,
		core.ItemIronPickaxe,
	} {
		t.Run(fmt.Sprint(item), func(t *testing.T) {
			inventory := core.Inventory{Hotbar: core.Hotbar{Selected: 2}}
			inventory.Hotbar.Slots[2] = core.ItemStack{Item: item, Count: 1}
			engine, session := readyFlatPlayerWithInventory(t, inventory)
			engine.Enqueue(sim.Command{
				Session: session, Sequence: 1, Kind: sim.CommandDropSelectedItem,
			})
			result := engine.Step()
			if len(result.Rejected) != 0 || len(result.Inventories) != 1 ||
				len(result.Changes) != 1 || len(result.Changes[0].Changes) != 0 {
				t.Fatalf("result = %+v", result)
			}
			if got := currentInventory(t, engine, session).Hotbar.Slots[2]; got != (core.ItemStack{}) {
				t.Fatalf("来源栏位 = %+v", got)
			}
			_, drop := onlyDrop(t, engine)
			if drop.Stack != (core.ItemStack{Item: item, Count: 1}) ||
				drop.PickupDelayTicks != sim.PlayerDropPickupDelayTicks-1 {
				t.Fatalf("主动掉落 = %+v", drop)
			}
		})
	}
}
```

The post-step delay is `39` because the creation tick immediately runs the existing active drop tick and counts as the first of 40.

- [ ] Add rejection tests proving `PlayerHash`, `ChunkHash`/revision and `PersistenceStats` are unchanged for empty selected slot and full 32-slot capacity. Use mismatched full stacks so `PrepareDrop` cannot merge.

- [ ] Add ordering/sequence tests: `SelectHotbar(N)` followed by `DropSelectedItem` in the same tick uses N; reverse order uses the old selected slot; duplicate and stale sequences do not transfer a second item.

- [ ] Add `drop_command_test.go` in package `sim` to exercise guards that public gameplay cannot normally create:

```go
func TestDropSelectedItemRejectsUnavailableFootChunkAtomically(t *testing.T) {
	engine, session := readyMovementPlayer(t)
	player := engine.sessions[session].player
	player.inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 2}
	player.state.Position = mgl32.Vec3{16.5, 1, 0.5}
	before := player.inventory
	engine.Enqueue(Command{Session: session, Sequence: 1, Kind: CommandDropSelectedItem})
	result := engine.Step()
	if len(result.Rejected) != 1 || result.Rejected[0].Reason != RejectChunkNotReady {
		t.Fatalf("拒绝 = %+v", result.Rejected)
	}
	if player.inventory != before || len(result.Changes) != 0 {
		t.Fatalf("拒绝后 player=%+v changes=%+v", player.inventory, result.Changes)
	}
}
```

- [ ] Add a 40-active-tick test: the first 39 total active advances retain the drop and inventory decrement; the 40th makes it eligible for the existing stable pickup order. Add a merge case preserving ID/generation/age with delay reset to 40 before the same-tick decrement.

- [ ] Add `BenchmarkDropSelectedItem` with sub-benchmarks `create`, `merge`, and `capacity_reject`; reset one fixed engine state per iteration without changing fixed capacities, and report allocations.

- [ ] Run RED:

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim -run "DropSelectedItem" -count=1'
```

- [ ] Append `CommandDropSelectedItem` to `CommandKind`; do not insert it among existing values:

```go
const (
	CommandTrustedObserverCenter CommandKind = iota
	CommandPlayerInput
	CommandPlaceBlock
	CommandResync
	CommandSelectHotbar
	CommandMoveInventoryStack
	CommandCraftRecipe
	CommandOpenFurnace
	CommandCloseFurnace
	CommandMoveFurnaceStack
	CommandDropSelectedItem
)
```

- [ ] Create `pending` before the command loop in `Engine.Step`, then call the helper inside the existing sequence-sorted switch:

```go
pending := make(map[core.ChunkKey]*pendingChunkChanges)
```

```go
case CommandDropSelectedItem:
	if reason, rejected := engine.dropSelectedItem(session, pending); rejected {
		result.Rejected = append(result.Rejected, Rejection{
			Session: command.Session, Sequence: command.Sequence, Reason: reason,
		})
	}
```

Delete the later duplicate `pending := make(...)`; keep the existing `advanceDrops`, furnace, placement, mining and `finishChanges` order unchanged.

- [ ] Add the constant and minimum helper in `drop.go`:

```go
const PlayerDropPickupDelayTicks = 40
```

```go
func (engine *Engine) dropSelectedItem(
	session *sessionState,
	pending map[core.ChunkKey]*pendingChunkChanges,
) (RejectReason, bool) {
	if session.player == nil || session.player.lifecycle != PlayerActive {
		return RejectPlayerNotReady, true
	}
	player := session.player
	selected := player.inventory.Hotbar.Selected
	nextHotbar, ok := player.inventory.Hotbar.Consume(selected)
	if !ok {
		return RejectInvalidSlot, true
	}
	stack := player.inventory.Hotbar.Slots[selected]
	position := core.BlockPos{
		X: int32(math.Floor(float64(player.state.Position.X()))),
		Y: int32(math.Floor(float64(player.state.Position.Y()))),
		Z: int32(math.Floor(float64(player.state.Position.Z()))),
	}
	key := core.ChunkKey{Dimension: session.dimension, Pos: position.Chunk()}
	dimension := engine.dimensions[key.Dimension]
	if dimension == nil {
		return RejectChunkNotReady, true
	}
	record := dimension.records[key.Pos]
	if record == nil || record.State != ChunkReady || record.Chunk == nil {
		return RejectChunkNotReady, true
	}
	blockIndex, ok := world.ChunkBlockIndex(position)
	if !ok {
		return RejectChunkNotReady, true
	}
	dropSlot, ok := record.Chunk.PrepareDrop(stack.Item, blockIndex)
	if !ok {
		return RejectDropCapacity, true
	}
	record.Chunk.CommitDrop(
		dropSlot, stack.Item, blockIndex, PlayerDropPickupDelayTicks,
	)
	player.inventory.Hotbar = nextHotbar
	player.inventoryDirty = true
	engine.touchChunk(key, pending)
	return 0, false
}
```

Add `math` to `drop.go`; do not add a slot field, position field, transaction object or second capacity scan.

- [ ] Run GREEN, benchmark and package gates:

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim ./internal/world ./internal/core -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim -run "^$" -bench DropSelectedItem -benchmem -count=3'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
gofmt -w internal/sim/command.go internal/sim/engine.go internal/sim/drop.go internal/sim/drop_test.go internal/sim/drop_command_test.go internal/sim/bench_test.go
gofmt -l .
git diff --check
```

- [ ] Commit:

```bash
git add internal/sim
git commit -m "feat: 实现权威单件丢弃"
```

## Task 5: Map the command and prove the Memory multiplayer loop

**Files:**

- Modify: `internal/server/session.go`
- Modify: `internal/server/player_test.go`
- Modify: `internal/server/drop_publication_test.go`
- Modify: `internal/server/multiplayer_memory_integration_test.go`

**Consumes:** `network.DropSelectedItem`.

**Produces:** one `sim.CommandDropSelectedItem`; existing `InventoryState`, `ItemDropUpserts`, `CommandRejected` and health behavior.

- [ ] Add this row to `TestTranslatePlayerMessage` in `player_test.go`:

```go
{
	name:    "drop selected item carries only the sequence",
	message: network.DropSelectedItem{Sequence: 17},
	want: sim.Command{
		Session: testSessionID, Sequence: 17, Kind: sim.CommandDropSelectedItem,
	},
},
```

- [ ] Add a Memory success test using the existing drop publication harness: stock selected coal, send sequence N, step once, and assert the owner receives a full inventory with one fewer coal while both interested sessions receive the same drop ID/value and revision barrier.

- [ ] Add a capacity-failure test: fill 32 incompatible drops, send the request, assert only the requester receives `CommandRejected{Sequence: N, Reason: RejectDropCapacity}`, neither inventory changes, no drop upsert/remove is published, and a second healthy command still advances.

- [ ] Extend the Memory transcript normalizer so `DropSelectedItem` inputs and resulting inventory/drop messages are compared by stable semantic fields; do not add a new server message.

- [ ] Run RED:

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "TranslatePlayerMessage|DropSelectedItem|Memory" -count=1'
```

- [ ] Add exactly one mapper branch in `session.go`:

```go
case network.DropSelectedItem:
	return sim.Command{
		Session: id, Sequence: message.Sequence, Kind: sim.CommandDropSelectedItem,
	}, true
```

- [ ] Reuse existing `networkRejectReason`, `publishInventories`, `publishDrops`, endpoint outboxes and slow-session close behavior unchanged.

- [ ] Run GREEN and gates:

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server ./internal/sim ./internal/network -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
gofmt -w internal/server/session.go internal/server/player_test.go internal/server/drop_publication_test.go internal/server/multiplayer_memory_integration_test.go
gofmt -l .
git diff --check
```

- [ ] Commit:

```bash
git add internal/server/session.go internal/server/player_test.go internal/server/drop_publication_test.go internal/server/multiplayer_memory_integration_test.go
git commit -m "feat: 接通主动丢弃服务端闭环"
```

## Task 6: Add Q rising-edge input without client prediction

**Files:**

- Modify: `internal/client/window.go`
- Modify: `internal/client/input.go`
- Modify: `internal/client/input_test.go`
- Modify: `cmd/mcgo/main.go`
- Modify: `cmd/mcgo/main_test.go`
- Modify: `cmd/mcgo/app.go`
- Modify: `cmd/mcgo/app_test.go`

**Consumes:** physical Q state plus the existing `allowActions`, predictor Ready, `nextSequence` and `send` gates.

**Produces:** at most one `DropSelectedItem` per valid Q press; no local inventory/drop mutation.

- [ ] Add an `InputState` regression that consumes edges even during suppression:

```go
func TestInputStateDropsOnlyOnValidQRisingEdge(t *testing.T) {
	var state client.InputState
	if got := state.Update(false, false, 0, false, true, false); !got.Drop {
		t.Fatalf("Q 上升沿 = %+v", got)
	}
	if got := state.Update(false, false, 0, false, true, false); got.Drop {
		t.Fatalf("按住 Q 重复丢弃 = %+v", got)
	}
	state.Update(false, false, 0, false, false, false)
	if got := state.Update(false, false, 0, false, true, true); got.Drop {
		t.Fatalf("背包打开时 Q 产生丢弃 = %+v", got)
	}
	if got := state.Update(false, false, 0, false, true, false); got.Drop {
		t.Fatalf("关闭背包但仍按住 Q 误触发 = %+v", got)
	}
}
```

- [ ] Add application tests for Ready/allowActions gates. When Ready and allowed, the first outgoing message is `DropSelectedItem{Sequence: 1}` followed only by existing predictor traffic. When not Ready or not allowed, no outgoing `DropSelectedItem` and no sequence allocation occurs. Snapshot inventory and `itemDrops.Presentations()` before send failure/rejection and assert they remain equal.

- [ ] Run RED:

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/client ./cmd/mcgo -run "Q|DropSelectedItem|NoPrediction" -count=1'
```

- [ ] Append `KeyQ` to the minimal GLFW key table:

```go
const (
	KeyW Key = iota
	KeyA
	KeyS
	KeyD
	KeySpace
	KeyLeftShift
	KeyLeftControl
	KeyEscape
	Key1
	Key2
	Key3
	Key4
	Key5
	Key6
	Key7
	Key8
	Key9
	KeyE
	KeyQ
)
```

```go
KeyQ: glfw.KeyQ,
```

- [ ] Extend `Actions`, `InputState`, and `Update` with one boolean edge state:

```go
actions := Actions{
	ToggleInventory: inventoryKey && !state.inventoryDown,
	Drop:            dropKey && !state.dropDown && !inventoryOpen,
}
```

```go
state.dropDown = dropKey
```

- [ ] Pass Q through the existing sampler in `runInteractive`:

```go
actions := input.Update(
	clickDown,
	app.window.SecondaryButtonDown(),
	number,
	app.window.KeyDown(client.KeyE),
	app.window.KeyDown(client.KeyQ),
	app.inventoryOpen,
)
```

- [ ] Send only inside the existing action gate and Ready helper:

```go
if allowActions {
	if actions.Select {
		a.selectHotbarSlot(actions.SelectSlot)
	}
	if actions.Place {
		a.placeBlock()
	}
	if actions.Drop {
		a.dropSelectedItem()
	}
}
```

```go
func (a *application) dropSelectedItem() {
	if _, ready := a.predictor.State(); !ready {
		return
	}
	if err := a.send(network.DropSelectedItem{Sequence: a.nextSequence()}); err != nil {
		log.Printf("发送主动丢弃请求失败: %v", err)
	}
}
```

- [ ] Do not read `InventoryMirror`, mutate local slots, create a local drop, add a renderer/HUD branch, or keep a second `qWasDown` in `runInteractive`.

- [ ] Run GREEN and no-window gates:

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/client ./cmd/mcgo -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
gofmt -w internal/client/window.go internal/client/input.go internal/client/input_test.go cmd/mcgo/main.go cmd/mcgo/main_test.go cmd/mcgo/app.go cmd/mcgo/app_test.go
gofmt -l .
git diff --check
```

- [ ] Commit:

```bash
git add internal/client/window.go internal/client/input.go internal/client/input_test.go cmd/mcgo/main.go cmd/mcgo/main_test.go cmd/mcgo/app.go cmd/mcgo/app_test.go
git commit -m "feat: 接入 Q 主动丢弃输入"
```

## Task 7: Prove TCP parity and normal shutdown/restart persistence

**Files:**

- Modify: `internal/server/tcp_integration_test.go`
- Modify: `internal/server/drop_publication_test.go`
- Verify: `internal/storage/player_codec_test.go`
- Verify: `internal/storage/chunk_drop_test.go`
- Verify: `internal/storage/metadata_test.go`
- Verify: `cmd/mcgod/main_test.go`

**Consumes:** protocol v10, existing TCP login, player save barrier and chunk save barrier.

**Produces:** real-loopback multiplayer parity, pre-Play v9 rejection and a disk restart proof with unchanged schemas/files.

- [ ] Add a real TCP test using existing loopback harnesses: connect two v10 clients with stable identities, stock the first player's selected coal or iron pickaxe, send `DropSelectedItem`, and drain until both clients observe the same drop ID/value while only the owner receives the reduced inventory.

- [ ] In the same harness, fill drop capacity and assert `drop_capacity` does not close the connection; a later legal input/keepalive still succeeds.

- [ ] Add an explicit raw v9 handshake case before LoginStart. Assert `HandshakeReject{ServerProtocolVersion: 10, Code: HandshakeVersionMismatch}` and prove no player/world mutation by comparing the store/load counters already exposed by the test harness.

- [ ] Extend `TestDropSurvivesShutdownAndRestart` or add the adjacent focused test so the operation is manual drop rather than mining. Persist and compare both sides:

```go
before, ok := first.PlayerSnapshotFor(1)
if !ok {
	t.Fatal("玩家未 Active")
}
sendClientMessage(t, firstClient, network.DropSelectedItem{Sequence: 1})
```

After normal `Shutdown`/store close and reopen, assert player inventory is reduced by one and the chunk slot retains generation, item, count, block index, `AgeTicks`, and `PickupDelayTicks`.

- [ ] Compare file lists before/after and run existing storage golden tests to prove player v3, chunk v4 and metadata v2 layouts remain unchanged. Do not add a migration or state file.

- [ ] Run the integration/storage slice:

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server ./internal/network ./internal/storage ./cmd/mcgod -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && CGO_ENABLED=0 GOOS=linux go build -o /private/tmp/mcgod-m4h ./cmd/mcgod'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
gofmt -w internal/server/tcp_integration_test.go internal/server/drop_publication_test.go
gofmt -l .
git diff --check
```

- [ ] Commit:

```bash
git add internal/server/tcp_integration_test.go internal/server/drop_publication_test.go
git commit -m "test: 覆盖主动丢弃传输与重启"
```

## Task 8: Update user-facing compatibility docs and freeze scenario v12

**Files:**

- Modify: `README.md`
- Modify: `docs/notes/lan-server.md`
- Verify: `cmd/mcgo/benchmark.go`
- Verify: `cmd/mcgo/benchmark_v5_test.go`
- Verify: `cmd/mcgo/benchmark_v6_test.go`
- Verify: `cmd/perfcheck/main.go`
- Verify: `docs/notes/perf-baseline-m2.json`
- Verify: `docs/notes/perf-baseline-m5.json`

**Consumes:** completed behavior and frozen compatibility values.

**Produces:** accurate Q/manual-drop docs with no baseline or scenario change.

- [ ] Update the control table with `Q`: drop exactly one item from the authoritative selected hotbar slot at the floored foot block; holding Q does not repeat.

- [ ] Replace “尚无丢弃” text with the implemented boundary: 40 active ticks, no velocity/gravity/owner/full-stack/death drop, server authority, 32 slots per chunk, registered non-placeable items supported.

- [ ] Update protocol guidance to v10/previous v9 rejection and ID 11. State player v3, chunk v4 and metadata v2 are byte-unchanged, so normal M4H→M4G rollback needs matching v9 binaries and a normal shutdown but no storage migration. Retain the separate warning that metadata v1-only programs still cannot open metadata v2.

- [ ] Document abnormal termination honestly: player and chunk files are each atomic, but a crash between their independent commits can preserve only one side; normal shutdown flushes both.

- [ ] Snapshot and prove the scenario/baselines are unchanged:

```bash
git diff --exit-code origin/main -- docs/notes/perf-baseline-m2.json docs/notes/perf-baseline-m5.json
rg -n 'scenarioVersion[[:space:]]*= 12|"scenario_version": 12' cmd/mcgo docs/notes/perf-baseline-m5.json
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo ./cmd/perfcheck ./cmd/mcgod ./internal/network -race -count=1'
openspec validate --all --strict --no-interactive
gofmt -l .
git diff --check
```

- [ ] Commit docs only:

```bash
git add README.md docs/notes/lan-server.md
git commit -m "docs: 说明主动丢弃与协议 v10"
```

## Task 9: Run the release-candidate gates, sync specs, and archive

**Files:**

- Update task checkboxes: `openspec/changes/m4h-authoritative-item-dropping/tasks.md`
- Sync: `openspec/specs/authoritative-item-dropping/spec.md`
- Sync: `openspec/specs/persistent-item-drops/spec.md`
- Update baseline text: `AGENTS.md`
- Update baseline text: `openspec/config.yaml`
- Archive: `openspec/changes/m4h-authoritative-item-dropping/`

**Consumes:** all passing implementation commits and verified docs.

**Produces:** M4H main specs, archived change, M4H project baseline and a clean candidate commit.

- [ ] Map every OpenSpec scenario to a concrete test name. Required matrix: valid edge/suppression, one/last/registered item, empty/full/not-ready chunk, stale sequence, create/merge/40 tick, stable pickup, Memory/TCP parity, failure isolation, v9 rejection, fixed ID/payload, unchanged schemas, bounded 32-slot scan and idle allocation behavior.

- [ ] Run all candidate gates without a window:

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race'
zsh -ic 'gvm use go1.26.0 >/dev/null && go vet ./...'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "^$" -fuzz FuzzSmallPacketCodec -fuzztime=10s'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "^$" -bench SmallPacketCodec -benchmem -count=3'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim -run "^$" -bench DropSelectedItem -benchmem -count=3'
gofmt -l .
git diff --check
openspec validate --all --strict --no-interactive
```

Expected: every command exits zero; `gofmt -l .` prints nothing; no threshold, capacity, scenario or baseline is relaxed.

- [ ] Inspect the final scope:

```bash
git status --short --branch
git diff --stat origin/main...HEAD
git diff --name-only origin/main...HEAD
```

Expected: only M4H protocol/sim/server/client/tests/docs/OpenSpec files; no binary asset, new dependency, generated baseline, foreground-process artifact or unrelated stash change.

- [ ] Mark implementation tasks complete only after their exact gates pass, then sync the two delta specs to `openspec/specs/`. Update `AGENTS.md` and `openspec/config.yaml` current baseline to M4H/protocol v10 while retaining player v3, chunk v4, metadata v2 and scenario v12.

- [ ] Validate synced specs, then archive only after every task is checked:

```bash
openspec validate --all --strict --no-interactive
openspec archive m4h-authoritative-item-dropping --yes
openspec validate --all --strict --no-interactive
git diff --check
```

- [ ] Commit the synchronized specs and archive result:

```bash
git add AGENTS.md openspec/config.yaml openspec/specs openspec/changes
git commit -m "chore: 归档 M4H 权威主动丢弃"
```

---

## Plan Self-Review Checklist

- [ ] No blank implementation markers or deferred implementation notes remain.
- [ ] Every OpenSpec Requirement and important failure scenario maps to at least one named test or explicit Task 9 verification item.
- [ ] `DropSelectedItem` is consistently `{Sequence uint64}`, packet ID `11`, payload `8877665544332211` for sequence `0x1122334455667788`, protocol v10.
- [ ] `CommandDropSelectedItem` is appended, not inserted; existing enum/packet/reject IDs are unchanged.
- [ ] `PlayerDropPickupDelayTicks` is consistently `40`; existing `DropPickupDelayTicks` remains `10`; creation tick semantics are asserted as post-step `39`.
- [ ] Both `CommitDrop` and `PrepareDropBatch` merge paths preserve ID/generation/age and take `max(old,incoming)`.
- [ ] All reject paths leave inventory, drop state, revision and persistence state unchanged.
- [ ] Client always updates Q physical state and never predicts inventory or drops.
- [ ] Player v3, chunk v4, metadata v2, scenario v12 and baseline files remain unchanged.
- [ ] No task introduces a new abstraction, dependency, worker, queue, renderer, UI, WAL/ECS or physical drop state.
