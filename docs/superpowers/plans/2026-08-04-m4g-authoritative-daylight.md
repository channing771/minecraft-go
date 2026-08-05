# M4G 权威昼夜与直射天空光实施计划

> **供执行代理：** 必须使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans，逐任务执行本计划；所有步骤使用复选框（`- [ ]`）跟踪。

**目标：** 在现有 20 Hz 服务端权威循环中加入可持久化、可经 Memory/TCP 同步的 24000 tick 昼夜时间，并用每列最高非空气方块派生直射天空光，使露天、遮蔽空间、白天和夜晚形成有界且可验证的视觉差异。

**架构：** `sim.Engine` 单写绝对世界时间，`server` 把本 tick 最终值附加到既有 `PlayerState`，并复用现有有界保存 worker 异步写 metadata v2。`world.Chunk` 维护固定 512 字节列顶表，客户端 mesher 只读带 revision 印章的九区块高度快照；时间推进只更新固定 uniform，不触发重网格。

**技术栈：** Go 1.26、现有 Memory/TCP binary codec、simulation owner、固定保存 worker、客户端 Mirror/Mesher、WebGPU/WGSL、OpenSpec、Go race/fuzz/benchmark、`cmd/perfcheck`。

## 全局约束

- [ ] M4F 的协议 v8、metadata v1、玩家 schema v3、区块 schema v4、scenario v10、M5 v10 正式基线、delta specs 同步与归档全部完成后才可开始任务 1。
- [ ] 全程通过 `zsh -ic 'gvm use go1.26.0 >/dev/null && ...'` 使用现有 GVM Go，不下载或安装另一份 Go。
- [ ] 每个实现任务执行 RED → GREEN → REFACTOR → 受影响包 race → archcheck → gofmt/diff check → 单独提交。
- [ ] 自动验证只使用无窗口测试和 benchmark；不得启动或聚焦交互式客户端。
- [ ] 保留用户未跟踪目录 `midscene_run/`，任何提交都不得暂存它。
- [ ] 不新增第三方依赖、内部包、光照 worker、动态传播队列、独立世界时间消息或第二个世界级状态文件。
- [ ] 空气是唯一透光方块；直射天空光只有 `0/15`，不实现横向洪泛、方块光、火把、透明方块、天气、天体、阴影或怪物规则。
- [ ] 玩家 schema 保持 v3、区块 schema 保持 v4、chunk snapshot payload 与 packet ID 保持不变；只升级 metadata v2、协议 v9 和 benchmark scenario v11。
- [ ] 每区块高度表恰好 512 字节；列顶下降最多扫描 384 格；单次方块变化最多产生 96 个天空光 dirty section key。
- [ ] `p=t%24000`，`sun=max(0,sin(2πp/24000))`，`daylight=0.15+0.85*sun`，terrain 基础亮度为 `0.08+(sky/15)*(daylight-0.08)`。
- [ ] 既有 2560×1440、still/flying、RSS、tick、2048 GPU 样本、Memory/TCP、绝对门禁和 `20%` 相对阈值不得放宽。
- [ ] 新增或修改的注释、测试说明和开发文档使用中文；Go 标识符、wire 字段和约定俗成技术术语保留英文。
- [ ] Hook 失败只修根因，不关闭、改写或绕过 `scripts/agent-hooks/guard.mjs`。

---

## 文件与稳定接口映射

### 世界派生状态与网格输入

修改：

- `internal/world/chunk.go`
- `internal/world/neighborhood.go`
- `internal/mesh/greedy.go`
- `internal/client/snapshot.go`
- `internal/client/mirror.go`
- `internal/client/mesher.go`

测试：

- `internal/world/chunk_test.go`
- `internal/world/neighborhood_test.go`
- `internal/mesh/greedy_test.go`
- `internal/client/snapshot_test.go`
- `internal/client/mirror_test.go`
- `internal/client/mesher_test.go`
- `internal/client/mesher_bench_test.go`

稳定新增接口：

```go
package world

type HeightMap [core.SectionSize * core.SectionSize]int16

func (c *Chunk) HighestOpaque(lx, lz int) int32
func (c *Chunk) HeightMap() HeightMap
func (c *Chunk) RebuildHeightMap()

type Neighborhood struct {
    Center       *Section
    Around       [3][3][3]*Section
    SectionIndex int32
    Heights      [3][3]HeightMap
    HeightPresent [3][3]bool
}

func (n *Neighborhood) DirectSkyAt(x, y, z int) uint8
```

`HeightMap` 是值快照；mesher worker 不得回读可变 `Mirror`。`Chunk.Hash`、`PayloadBytes`、区块 codec 与网络 snapshot 不包含高度表。

### metadata v2 与权威时间

修改：

- `internal/storage/types.go`
- `internal/storage/metadata.go`
- `internal/storage/memory.go`
- `internal/storage/disk.go`
- `internal/storage/world_files.go`
- `internal/sim/engine.go`
- `internal/sim/player.go`
- `internal/sim/command.go`
- `internal/server/server.go`
- `internal/server/publication.go`

测试：

- `internal/storage/metadata_test.go`
- `internal/storage/memory_test.go`
- `internal/storage/disk_test.go`
- `internal/sim/engine_test.go`
- `internal/sim/bench_test.go`
- `internal/server/publication_test.go`

稳定接口：

```go
package storage

type Metadata struct {
    FormatVersion  uint32
    Seed           int64
    SpawnDimension core.DimensionID
    SpawnAnchor    core.ChunkPos
    WorldTimeTicks uint64
}

type Store interface {
    Metadata() Metadata
    SaveMetadata(context.Context, Metadata) error
    LoadChunk(context.Context, core.ChunkKey) (StoredChunk, error)
    SaveBatch(context.Context, []ChunkSave) (SaveResult, error)
    Sync(context.Context) error
    Close() error
}
```

```go
package sim

func NewEngine(viewRadius int, worldTimeTicks uint64) *Engine

type TickResult struct {
    // 既有字段保持顺序与语义。
    Tick           uint64
    WorldTimeTicks uint64
}

type PlayerUpdate struct {
    // 既有字段保持顺序与语义。
    WorldTimeTicks uint64
}
```

metadata v2 payload 在 v1 的 20 字节 payload 后追加 little-endian `uint64 WorldTimeTicks`，总 payload 为 28 字节。decoder 双读 v1/v2；v1 返回规范 v2 值且时间为零；encoder 只写 v2。

### 协议与客户端确认状态

修改：

- `internal/network/packet.go`
- `internal/network/message.go`
- `internal/network/codec.go`
- `internal/client/predictor_test.go`
- `cmd/mcgo/app.go`

测试：

- `internal/network/packet_test.go`
- `internal/network/message_test.go`
- `internal/network/codec_test.go`
- `internal/network/login_test.go`
- `internal/network/benchmark_test.go`
- `cmd/mcgo/app_test.go`

稳定 wire 变化：

```go
const ProtocolVersion uint32 = 9

type PlayerState struct {
    // 既有 73 字节字段保持原顺序。
    WorldTimeTicks uint64 // 固定 payload 末尾；v9 总长 81 字节。
}
```

`application` 增加 `worldTimeTicks uint64`；只有 `ServerTick` 严格更新的有效 `PlayerState` 才能更新它。不得新增 packet ID、版本协商或 v8 decoder。

### 有界 metadata 保存状态

修改：

- `internal/server/persistence.go`
- `internal/server/shutdown.go`
- `internal/server/server.go`

测试：

- `internal/server/persistence_test.go`
- `internal/server/persistence_integration_test.go`
- `internal/server/shutdown_test.go`
- `internal/server/multiplayer_restart_test.go`

私有固定状态：

```go
type saveJobKind uint8

const (
    saveJobChunks saveJobKind = iota
    saveJobMetadata
)

type metadataPersistenceState struct {
    base          storage.Metadata
    latest        uint64
    persisted     uint64
    pending       bool
    inFlight      bool
    attempt       uint32
    nextRetryTick uint64
    lastError     error
}
```

`saveJob` 增加 `Kind saveJobKind` 与 `Metadata storage.Metadata`；chunk job 继续使用既有 `Region/Snapshots/RetryID`。metadata 不进入 region retry map，始终最多一个 in-flight；队列满时保留 `pending`。

`Server` 只增加一个 `metadataState metadataPersistenceState` 字段。普通 tick 只更新 `latest`；只有 autosave 边界、失败重试或关服冻结才设置 `pending`。

### 渲染与装配

修改：

- `internal/render/lighting.go`（新增）
- `internal/render/renderer.go`
- `internal/render/avatar.go`
- `internal/render/drop.go`
- `internal/render/shader/terrain.wgsl`
- `internal/render/shader/avatar.wgsl`
- `cmd/mcgo/app.go`
- `cmd/gfxspike/main.go`

测试：

- `internal/render/lighting_test.go`（新增）
- `internal/render/renderer_test.go`
- `internal/render/avatar_test.go`
- `internal/render/drop_test.go`
- `cmd/mcgo/app_test.go`

稳定新增值：

```go
package render

type WorldLighting struct {
    Sun        float32
    Daylight   float32
    ClearColor [4]float32
}

func WorldLightingAt(worldTimeTicks uint64) WorldLighting
```

三个世界空间 renderer 接收同一 `WorldLighting`：

```go
func (r *Renderer) Render(enc gfx.CommandEncoder, target, depth gfx.TextureView, cam Camera, lighting WorldLighting)
func (r *AvatarRenderer) Render(enc gfx.CommandEncoder, target, depth gfx.TextureView, cam Camera, lighting WorldLighting, avatars []Avatar)
func (r *ItemDropRenderer) Render(enc gfx.CommandEncoder, target, depth gfx.TextureView, cam Camera, lighting WorldLighting, serverTick uint64, drops []ItemDrop)
```

日间 clear color 保持 `[0.42, 0.68, 0.92, 1]`；夜间固定为 `[0.02, 0.03, 0.08, 1]`，按 `Sun` 线性插值。HUD 与 name tag 接口不变。

---

## 任务 0：等待 M4F 归档并重新绑定基线

**文件：**

- 读取：`openspec/config.yaml`
- 读取：归档后的 M4F 主规格与 `docs/notes/perf-baseline-m5.json`
- 读取：`openspec/changes/m4g-authoritative-daylight/{proposal.md,design.md,tasks.md}`
- 读取：`openspec/changes/m4g-authoritative-daylight/specs/authoritative-daylight/spec.md`
- 读取：`openspec/changes/m4g-authoritative-daylight/specs/bounded-benchmark-workload/spec.md`
- 读取：`openspec/changes/m4g-authoritative-daylight/specs/hardware-performance-baselines/spec.md`

**接口：**

- 输入：M4F 归档后的代码和主规格。
- 输出：已确认不漂移的 M4G 起点；本任务不修改功能代码。

- [ ] **步骤 1：确认 M4F 已无 active change。**

运行：

```bash
openspec list --json
```

预期：列表中没有 `m4f-authoritative-mining-tools`；若仍 active，停止 M4G。

- [ ] **步骤 2：确认冻结版本与基线。**

运行：

```bash
jq -e '.scenario_version == 10' docs/notes/perf-baseline-m5.json
rg -n 'ProtocolVersion uint32 = 8|currentMetadataVersion uint32 = 1|currentPlayerSchema.*3|currentChunkSchema.*4' internal
```

预期：命令全部成功，M5 是 scenario v10，协议/metadata/玩家/区块起点分别为 8/1/3/4。

- [ ] **步骤 3：逐项比对 M4G OpenSpec。**

检查 M4F 收尾是否改变 `PlayerState` 尾部、metadata 原子替换、save worker、Mirror dirty、mesher stamp、renderer uniform 或 scenario 迁移入口。若改变，先修订 M4G proposal/spec/design/tasks 与本计划中的确切签名，再执行：

```bash
openspec validate m4g-authoritative-daylight --strict --no-interactive
git diff --check
```

- [ ] **步骤 4：为实现创建隔离工作树。**

执行时先使用 `superpowers:using-git-worktrees`，创建 `codex/m4g-authoritative-daylight-impl`；确认 `midscene_run/` 未被复制进暂存范围。本任务没有提交，下一任务从该工作树开始。

---

## 任务 1：加入固定列顶派生状态

**文件：**

- 修改：`internal/world/chunk_test.go`
- 修改：`internal/world/chunk.go`
- 修改：`internal/client/snapshot_test.go`
- 修改：`internal/client/snapshot.go`
- 修改：`internal/storage/chunk_codec_test.go`
- 修改：`internal/storage/chunk_codec.go`

**接口：**

- 输入：现有 `Chunk.SetBlock`、snapshot 和 chunk schema v4 decoder。
- 输出：`world.HeightMap`、`HighestOpaque`、`HeightMap`、`RebuildHeightMap`；后续 mesher 与 dirty 任务只依赖这些接口。

- [ ] **步骤 1：写列顶 RED 测试。**

在 `internal/world/chunk_test.go` 增加：

```go
func TestChunkHeightMapTracksHighestOpaque(t *testing.T) {
    chunk := world.NewChunk(core.ChunkPos{})
    if got := chunk.HighestOpaque(3, 5); got != core.MinY-1 {
        t.Fatalf("空列最高遮挡=%d，想要 %d", got, core.MinY-1)
    }
    chunk.SetBlock(3, 64, 5, core.StoneID)
    chunk.SetBlock(3, 80, 5, core.StoneID)
    chunk.SetBlock(3, 70, 5, core.DirtID)
    if got := chunk.HighestOpaque(3, 5); got != 80 {
        t.Fatalf("列顶=%d，想要 80", got)
    }
    chunk.SetBlock(3, 80, 5, core.AirID)
    if got := chunk.HighestOpaque(3, 5); got != 70 {
        t.Fatalf("移除列顶后=%d，想要 70", got)
    }
    clone := chunk.Clone()
    if clone.HeightMap() != chunk.HeightMap() {
        t.Fatal("Clone 未复制高度表")
    }
}

func TestHeightMapHasFixed512Bytes(t *testing.T) {
    if got := unsafe.Sizeof(world.HeightMap{}); got != 512 {
        t.Fatalf("HeightMap=%d 字节，想要 512", got)
    }
}
```

再加最坏列测试：世界顶部放一块后移除，用包装计数或直接断言重算结果为 `MinY-1`；循环上限固定为 `core.MaxY-core.MinY == 384`。

- [ ] **步骤 2：运行 RED。**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/world -run "HeightMap|HighestOpaque" -count=1'
```

预期：`HeightMap` 与方法尚不存在，编译失败。

- [ ] **步骤 3：实现最小高度表。**

在 `internal/world/chunk.go` 增加：

```go
type HeightMap [core.SectionSize * core.SectionSize]int16

type Chunk struct {
    Pos      core.ChunkPos
    sections [core.SectionsPerChunk]*Section
    drops    [core.DropsPerChunk]DropSlot
    furnaces [core.FurnacesPerChunk]FurnaceSlot
    heights  HeightMap
}

func heightIndex(lx, lz int) int { return lz*core.SectionSize + lx }

func (c *Chunk) HighestOpaque(lx, lz int) int32 {
    return int32(c.heights[heightIndex(lx, lz)])
}

func (c *Chunk) HeightMap() HeightMap { return c.heights }
```

`NewChunk` 把 256 项初始化为 `int16(core.MinY-1)`。`SetBlock` 在写入前保存旧方块：非空气写到更高处时 O(1) 抬高；只有“旧值非空气、新值空气、且 Y 等于列顶”时从 `wy-1` 向 `core.MinY` 扫描。不得把非空气方块细分为新的属性表。

`Clone` 用值赋值复制 `heights`。`RebuildHeightMap` 对每列从 `core.MaxY-1` 向下找到第一个非空气方块；找不到则保持 `MinY-1`。

- [ ] **步骤 4：让直接装入 section 的路径重建。**

在 `internal/client/snapshot.go:chunkFromSnapshot` 所有 section 完成替换后、返回前调用：

```go
chunk.RebuildHeightMap()
```

在 `internal/storage/chunk_codec.go:chunkFromDTO` 同一位置调用相同方法。为两条路径分别增加一个 roof-at-Y=80 fixture，断言解码后的 `HighestOpaque` 为 80。

- [ ] **步骤 5：证明格式与哈希未变化。**

在 `internal/storage/chunk_codec_test.go` 用同一 v4 fixture 解码再编码，断言 golden 字节不变；在 `internal/world/chunk_test.go` 断言只重建高度表不会改变 `Hash()` 或 `PayloadBytes()`。

- [ ] **步骤 6：运行 GREEN 与提交。**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/world ./internal/client ./internal/storage -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
gofmt -w internal/world/chunk.go internal/world/chunk_test.go internal/client/snapshot.go internal/client/snapshot_test.go internal/storage/chunk_codec.go internal/storage/chunk_codec_test.go
gofmt -l .
git diff --check
git add internal/world/chunk.go internal/world/chunk_test.go internal/client/snapshot.go internal/client/snapshot_test.go internal/storage/chunk_codec.go internal/storage/chunk_codec_test.go
git commit -m "feat: 维护固定区块列顶"
```

---

## 任务 2：用不可变高度快照生成直射天空光

**文件：**

- 修改：`internal/world/neighborhood_test.go`
- 修改：`internal/world/neighborhood.go`
- 修改：`internal/mesh/greedy_test.go`
- 修改：`internal/mesh/greedy.go`
- 修改：`internal/client/mesher_test.go`
- 修改：`internal/client/mesher.go`

**接口：**

- 输入：任务 1 的 `HeightMap()` 与 `HighestOpaque()`。
- 输出：`Neighborhood.DirectSkyAt` 与 `Quad.Light` 的稳定 `0x00/0xF0`；后续 dirty 任务复用相同高度语义。

- [ ] **步骤 1：写邻域天空查询 RED 测试。**

覆盖中心列、跨东/西/南/北区块、缺失邻区和世界上下边界：

```go
func TestNeighborhoodDirectSkyUsesHighestOpaque(t *testing.T) {
    chunk := world.NewChunk(core.ChunkPos{})
    chunk.SetBlock(4, 64, 7, core.StoneID)
    n := world.NeighborhoodAt(func(core.ChunkPos) *world.Chunk { return chunk }, chunk.Pos, 8)
    if got := n.DirectSkyAt(4, 0, 7); got != 15 { // section 8 起点 Y=64
        t.Fatalf("列顶上方天空光=%d，想要 15", got)
    }
    if got := n.DirectSkyAt(4, -1, 7); got != 0 {
        t.Fatalf("列顶处天空光=%d，想要 0", got)
    }
}
```

缺失 chunk 的任何查询必须返回 0。

- [ ] **步骤 2：运行邻域 RED。**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/world -run DirectSky -count=1'
```

预期：`DirectSkyAt` 尚不存在。

- [ ] **步骤 3：实现不可变高度邻域。**

`NeighborhoodAt` 设置 `SectionIndex=int32(si)`，并在每个已加载的 `dx/dz` chunk 上复制：

```go
n.Heights[dx+1][dz+1] = ch.HeightMap()
n.HeightPresent[dx+1][dz+1] = true
```

`DirectSkyAt` 只接受 `x/z=-1..16` 与 `y=-1..16`；把 x/z 映射到 `[3][3]` chunk cell 和 0..15 列，再计算：

```go
worldY := core.MinY + n.SectionIndex*core.SectionSize + int32(y)
if worldY > int32(n.Heights[cellX][cellZ][heightIndex]) {
    return 15
}
return 0
```

缺失、越界或 nil receiver 返回 0。

- [ ] **步骤 4：写 mesher RED 测试。**

在 `internal/mesh/greedy_test.go` 构造四种可见面：露天、Y=80 屋顶下、邻区缺失边界、同材质但光值不同的相邻面。分别断言 `Quad.Light == 0xF0`、`0x00`、`0x00`，最后一种产生两个 quad 而不是错误合并。

- [ ] **步骤 5：替换写死光值。**

在 `MeshSection` 已确认相邻位置 `q` 非 opaque 后写：

```go
light := n.DirectSkyAt(q[0], q[1], q[2]) << 4
mask[vi][ui] = maskCell{
    used: true,
    mat: reg.Material(id, face),
    ao: computeAO(n, reg, p, axis, u, v, step),
    light: light,
}
```

低四位保持零；不增加向上扫描或光数组。

- [ ] **步骤 6：让客户端 clone job 复制同代高度。**

在 `cloneNeighborhood` 创建值后设置 `SectionIndex=key.Pos.Y`，每个已加载 chunk 在复制 stamp 的同一分支复制 `HeightMap` 与 present bit。增加阻塞 worker 测试：任务排队后改变屋顶和 revision，旧结果必须被 `stampsMatch` 丢弃，新任务产生正确 light。

- [ ] **步骤 7：运行 GREEN 与提交。**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/world ./internal/mesh ./internal/client -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
gofmt -w internal/world/neighborhood.go internal/world/neighborhood_test.go internal/mesh/greedy.go internal/mesh/greedy_test.go internal/client/mesher.go internal/client/mesher_test.go
gofmt -l .
git diff --check
git add internal/world/neighborhood.go internal/world/neighborhood_test.go internal/mesh/greedy.go internal/mesh/greedy_test.go internal/client/mesher.go internal/client/mesher_test.go
git commit -m "feat: 派生直射天空光"
```

---

## 任务 3：升级 metadata v2 并提供原子保存接口

**文件：**

- 修改：`internal/storage/types.go`
- 修改：`internal/storage/metadata.go`
- 修改：`internal/storage/metadata_test.go`
- 修改：`internal/storage/memory.go`
- 修改：`internal/storage/memory_test.go`
- 修改：`internal/storage/disk.go`
- 修改：`internal/storage/disk_test.go`
- 修改：`internal/storage/world_files.go`

**接口：**

- 输入：metadata v1 的 20 字节 payload 和既有 `replaceFileAtomicallyWithPatternAndHooks`。
- 输出：任务 4/6 使用的 `Metadata.WorldTimeTicks` 与 `Store.SaveMetadata`。

- [ ] **步骤 1：冻结 v1 fixture 并写 v2 RED 测试。**

在 `internal/storage/metadata_test.go` 保留一份显式 v1 字节（含 CRC），并增加：

```go
func TestMetadataV1MigratesToV2AtDawn(t *testing.T) {
    got, err := decodeMetadata(metadataV1Fixture(t))
    if err != nil {
        t.Fatal(err)
    }
    if got.FormatVersion != 2 || got.WorldTimeTicks != 0 {
        t.Fatalf("v1 迁移=%+v，想要 v2/time=0", got)
    }
}

func TestMetadataV2RoundTripWorldTime(t *testing.T) {
    want := Metadata{FormatVersion: 2, Seed: -42, SpawnDimension: core.Overworld, SpawnAnchor: core.ChunkPos{X: 7, Z: -11}, WorldTimeTicks: 12345}
    encoded, err := encodeMetadata(want)
    if err != nil {
        t.Fatal(err)
    }
    if got := binary.LittleEndian.Uint32(encoded[8:12]); got != 28 {
        t.Fatalf("v2 payload=%d，想要 28", got)
    }
    round, err := decodeMetadata(encoded)
    if err != nil || round != want {
        t.Fatalf("v2 round=%+v err=%v，想要 %+v", round, err, want)
    }
}
```

fixture helper 使用冻结 v1 前缀并现场计算 CRC：

```go
func metadataV1Fixture(t *testing.T) []byte {
    t.Helper()
    prefix := []byte{
        'M', 'C', 'G', 'M',
        1, 0, 0, 0,
        20, 0, 0, 0,
        0xd6, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
        0xfd, 0xff, 0xff, 0xff,
        7, 0, 0, 0,
        0xf5, 0xff, 0xff, 0xff,
    }
    checksum := crc32.Checksum(prefix, crc32.MakeTable(crc32.Castagnoli))
    return binary.LittleEndian.AppendUint32(prefix, checksum)
}
```

表驱动错误测试覆盖 v1/v2 截断、CRC、错误 payload 长度和 v3 `ErrFutureVersion`。

- [ ] **步骤 2：运行 codec RED。**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage -run Metadata -count=1'
```

预期：v2 字段/长度尚不存在。

- [ ] **步骤 3：实现双读单写 codec。**

使用固定常量：

```go
const (
    oldestMetadataVersion  uint32 = 1
    currentMetadataVersion uint32 = 2
    metadataV1PayloadLength uint32 = 20
    metadataV2PayloadLength uint32 = 28
)
```

`encodeMetadata` 只接受 version 2，并在 spawn anchor 后追加 `WorldTimeTicks`。`decodeMetadata` 先根据 version 选择精确 payload 长度、验证完整长度与 CRC，再解析共同 20 字节；version 1 把返回值的 `FormatVersion` 设为 2 且时间为 0，version 2 读取尾部 8 字节。

- [ ] **步骤 4：写 Memory/Disk SaveMetadata RED 测试。**

Memory 测试：保存 `WorldTimeTicks=77` 后 `Metadata()` 返回值更新，调用方随后改写自己的副本不影响 Store。Disk 测试：保存后关闭重开仍为 77；注入 temp write、temp sync、rename 失败时旧文件字节不变；注入 rename 后目录 sync 失败时 `world.meta` 必须可解码且等于完整旧值或完整新值。

- [ ] **步骤 5：实现 Store 方法。**

`Store` 增加：

```go
SaveMetadata(context.Context, Metadata) error
```

`NewMemory` 收到 v1 时把 `FormatVersion` 规范为 2 并强制 `WorldTimeTicks=0`，收到 v2 时保留值，其他版本因构造器没有 error 返回而明确 panic；为三条分支各留测试。`MemoryStore.Metadata` 改为加锁值返回；`SaveMetadata` 检查 context、调用 `encodeMetadata` 做同一语义验证，再在锁内替换值。`DiskStore.SaveMetadata` 在 `closing/closed/context` 检查和 `mu` 内编码，通过：

```go
replaceFileAtomicallyWithPatternAndHooks(
    filepath.Join(store.files.root, "world.meta"),
    ".world.meta.tmp-*",
    encoded,
    0o600,
    hooks,
)
```

`DiskStore` 增加 `metadataReplaceHooks atomicReplaceHooks`，方法内使用 `hooks := store.metadataReplaceHooks` 并设置 `hooks.beforeRename = ctx.Err`。成功后才更新 `store.files.metadata`。失败返回带 `save world metadata` 上下文的错误；rename 后目录同步失败允许 canonical 已是完整新版，但内存值仍保持旧值，下一次重试重新提交相同快照。

- [ ] **步骤 6：更新创建路径与接口断言。**

`openWorldFiles` 新建世界只写 v2。把生产和测试中作为“新建 metadata”的 `FormatVersion: 1` 改为 2；v1 只保留在迁移 fixture。补齐 `loadResultStore`、`blockingLoadStore`、`shutdownTestStore`、`persistenceTestStore` 的 `SaveMetadata` 测试实现，禁止通过嵌入空接口绕过。

- [ ] **步骤 7：运行 GREEN 与提交。**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage ./internal/server ./cmd/mcgo -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
gofmt -w $(git diff --name-only --diff-filter=ACM -- '*.go')
gofmt -l .
git diff --check
git add internal/storage internal/server cmd/mcgo
git commit -m "feat: 持久化 metadata v2 世界时间"
```

暂存前用 `git diff --name-only --cached` 确认没有 `midscene_run/` 或 M4G 以外文件。

---

## 任务 4：让 simulation owner 推进绝对世界时间

**文件：**

- 修改：`internal/sim/engine_test.go`
- 修改：`internal/sim/bench_test.go`
- 修改：`internal/sim/engine.go`
- 修改：`internal/sim/player.go`
- 修改：`internal/sim/command.go`
- 修改：所有 `sim.NewEngine` 调用点

**接口：**

- 输入：任务 3 的 `Metadata.WorldTimeTicks`。
- 输出：`TickResult.WorldTimeTicks` 与每名 `PlayerUpdate.WorldTimeTicks`；任务 5/6 只读取这些值。

- [ ] **步骤 1：写权威推进 RED 测试。**

```go
func TestEngineWorldTimeRestoresAndAdvancesOncePerStep(t *testing.T) {
    engine := sim.NewEngine(0, 23999)
    first := engine.Step()
    if first.WorldTimeTicks != 24000 {
        t.Fatalf("首 tick 世界时间=%d，想要 24000", first.WorldTimeTicks)
    }
    second := engine.Step()
    if second.WorldTimeTicks != 24001 {
        t.Fatalf("次 tick 世界时间=%d，想要 24001", second.WorldTimeTicks)
    }
}
```

再注册八名玩家，完成 spawn 后断言同一 `TickResult.Players` 中所有 `WorldTimeTicks` 等于 `result.WorldTimeTicks`。用两份相同起始时间/命令流引擎断言每 tick 结果时间一致。

- [ ] **步骤 2：写零分配门禁。**

在 `internal/sim/bench_test.go` 对完成 warmup、无保存边界和无方块变化的八玩家 engine 使用 `testing.AllocsPerRun(1000, func(){ engine.Step() })`；记录 M4F 既有分配基线，断言 M4G 时间字段不增加分配。不要为了通过而放宽现有上限。

- [ ] **步骤 3：运行 RED。**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim -run "WorldTime|Allocation" -count=1'
```

预期：新构造参数和字段尚不存在。

- [ ] **步骤 4：实现单写时间。**

`Engine` 增加普通 `uint64 worldTimeTicks`，构造时赋初值；在 `Step` 完成所有模拟变化、即将发布结果时执行：

```go
result.Tick = engine.tick.Add(1)
engine.worldTimeTicks++
result.WorldTimeTicks = engine.worldTimeTicks
engine.publishInventories(&result)
engine.publishFurnaces(&result)
engine.publishPlayers(&result)
```

`playerState.update` 增加 `worldTimeTicks uint64` 参数并写入 `PlayerUpdate`；`publishPlayers` 传 `result.WorldTimeTicks`。不得读取 `time.Now` 或创建独立 clock。

- [ ] **步骤 5：更新所有构造调用。**

`internal/server.NewWorld` 使用：

```go
metadata := store.Metadata()
engine: sim.NewEngine(config.ViewRadius, metadata.WorldTimeTicks)
```

测试/benchmark 中没有恢复需求的调用统一传 0。禁止保留第二个旧构造器。

- [ ] **步骤 6：运行 GREEN 与提交。**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim ./internal/server ./cmd/mcgo -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
gofmt -w $(git diff --name-only --diff-filter=ACM -- '*.go')
gofmt -l .
git diff --check
git add internal/sim internal/server cmd/mcgo
git commit -m "feat: 推进权威世界时间"
```

---

## 任务 5：把世界时间附加到协议 v9 玩家状态

**文件：**

- 修改：`internal/network/packet.go`
- 修改：`internal/network/message.go`
- 修改：`internal/network/codec.go`
- 修改：`internal/network/packet_test.go`
- 修改：`internal/network/message_test.go`
- 修改：`internal/network/codec_test.go`
- 修改：`internal/network/login_test.go`
- 修改：`internal/network/benchmark_test.go`
- 修改：`internal/server/publication.go`
- 修改：`internal/server/publication_test.go`
- 修改：`cmd/mcgo/app.go`
- 修改：`cmd/mcgo/app_test.go`
- 修改：`internal/client/predictor_test.go`

**接口：**

- 输入：任务 4 的 `PlayerUpdate.WorldTimeTicks`。
- 输出：协议 v9 的 `PlayerState.WorldTimeTicks` 和 `application.worldTimeTicks`；任务 8 的 renderer 装配读取最后确认值。

- [ ] **步骤 1：写 protocol/version RED 测试。**

把冻结版本断言改为：

```go
func TestProtocolVersionIsNine(t *testing.T) {
    if network.ProtocolVersion != 9 {
        t.Fatalf("ProtocolVersion=%d，想要 9", network.ProtocolVersion)
    }
}
```

保留 v8 raw hello fixture，断言服务端在 Handshake 返回 `HandshakeVersionMismatch`、`ServerProtocolVersion=9`，且不会进入 Login/Play。

- [ ] **步骤 2：写固定 payload RED 测试。**

在既有 PlayerState golden 值末尾设 `WorldTimeTicks: 0x0102030405060708`，断言 payload 长度为 81 且最后八字节为：

```go
[]byte{0x08, 0x07, 0x06, 0x05, 0x04, 0x03, 0x02, 0x01}
```

decode 后必须完整往返；截去任一尾字节、附加 trailing byte 都必须失败。更新 small-packet benchmark fixture，但不改 packet ID。

- [ ] **步骤 3：运行 network RED。**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "ProtocolVersion|PlayerState|Handshake" -count=1'
```

预期：版本仍为 8，字段尚未编码。

- [ ] **步骤 4：实现协议 v9。**

`packet.go` 的注释改为 M4G，版本设 9。`PlayerState` 末尾追加 `WorldTimeTicks uint64`；`encodeServerControlPayload` 在 `MiningHarvestable` 后调用：

```go
e.u64(message.WorldTimeTicks)
```

decoder 在读取 `MiningHarvestable` 后调用 `d.u64()`。`Validate` 不限制 `uint64` 的合法范围；任何绝对值都有效。registry 与 packet ID 表不得改变。

- [ ] **步骤 5：映射服务端发布值。**

在 `publishLocalResult` 构造 `network.PlayerState` 时加入：

```go
WorldTimeTicks: playerUpdate.WorldTimeTicks,
```

测试同一 tick 两个 session 收到相同值；Ready=false 与 Reset=true 状态也必须携带本 tick 时间。

- [ ] **步骤 6：只保存最后确认的客户端时间。**

`application` 增加 `worldTimeTicks uint64`。在 `drainServerMessages` 通过 `state.ServerTick > a.serverTick` 和 `predictor.ApplyPlayerState` 验证后，与 `a.serverTick` 同一位置写：

```go
a.serverTick = state.ServerTick
a.worldTimeTicks = state.WorldTimeTicks
```

旧/重复 PlayerState 必须同时不能回退 server tick、世界时间和 mining overlay。`closeClientSession` 清零 `serverTick` 与 `worldTimeTicks`；不得把时间塞进 Predictor 或 Mirror。

- [ ] **步骤 7：运行 codec fuzz、benchmark 与提交。**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network ./internal/server ./internal/client ./cmd/mcgo -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "^$" -fuzz FuzzSmallPacketCodec -fuzztime=10s'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "^$" -bench SmallPacketCodec -benchmem -count=3'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
gofmt -w $(git diff --name-only --diff-filter=ACM -- '*.go')
gofmt -l .
git diff --check
git add internal/network internal/server internal/client cmd/mcgo
git commit -m "feat: 同步协议 v9 权威世界时间"
```

---

## 任务 6：复用保存 worker 异步持久化最新时间

**文件：**

- 修改：`internal/server/server.go`
- 修改：`internal/server/persistence.go`
- 修改：`internal/server/shutdown.go`
- 修改：`internal/server/persistence_test.go`
- 修改：`internal/server/persistence_integration_test.go`
- 修改：`internal/server/shutdown_test.go`

**接口：**

- 输入：任务 3 的 `Store.SaveMetadata`、任务 4 的 `TickResult.WorldTimeTicks`。
- 输出：自动保存边界与冻结关服都使用的单槽 metadata state；任务 9 的重启测试依赖它。

- [ ] **步骤 1：写调度 RED 测试。**

用容量可控的 `persistenceTestStore` 阻塞 `SaveMetadata`，覆盖：

1. 第一个 autosave 边界只投递当时最新时间；
2. in-flight 期间继续 Step 不阻塞且不产生第二个 metadata job；
3. 普通 tick 前进只更新 `latest`，不投递第二个 job；
4. in-flight 期间跨过新的 autosave 边界后 `pending` 保持 true，并在旧快照完成后提交合并后的最新值；
5. save channel 满时 tick 返回、`pending` 保留；
6. 失败使用 `RetryBaseTicks/RetryMaxTicks`，不增加 `retry` region cohort。

断言新增状态：

```go
status := running.PersistenceStatus()
if !status.MetadataPending || status.MetadataInFlight {
    t.Fatalf("metadata status=%+v", status)
}
```

- [ ] **步骤 2：运行 RED。**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "Metadata.*Save|Autosave.*Metadata" -count=1'
```

预期：save job 尚无 metadata kind，状态字段不存在。

- [ ] **步骤 3：加入固定 job kind 与 worker switch。**

`saveJob` 增加 `Kind` 和 `Metadata`；`groupSaveJobs` 显式生成 `Kind: saveJobChunks`。`saveWorker` 使用：

```go
switch job.Kind {
case saveJobChunks:
    result, err = server.store.SaveBatch(server.saveCtx, saves)
case saveJobMetadata:
    err = server.store.SaveMetadata(server.saveCtx, job.Metadata)
default:
    err = fmt.Errorf("server: unknown save job kind %d", job.Kind)
}
```

`SaveObserver` 继续包住实际 Store 调用，因此 scenario v11 会真实观察新增保存工作。

- [ ] **步骤 4：实现单槽 metadata 状态机。**

`NewWorld` 用 `store.Metadata()` 初始化：

```go
metadataState: metadataPersistenceState{
    base: metadata,
    latest: metadata.WorldTimeTicks,
    persisted: metadata.WorldTimeTicks,
},
```

每次 `engine.Step()` 后先执行 `server.metadataState.latest = result.WorldTimeTicks`。在 `tick%AutosaveTicks==0` 时置 `pending=true`。调度 helper 仅在 `pending && !inFlight && tick>=nextRetryTick` 时构造值快照：

```go
metadata := state.base
metadata.FormatVersion = 2
metadata.WorldTimeTicks = state.latest
attempt := state.attempt
if attempt < ^uint32(0) {
    attempt++
}
if attempt == 0 {
    attempt = 1
}
job := saveJob{Kind: saveJobMetadata, Metadata: metadata, Attempt: attempt}
```

非阻塞发送成功后 `pending=false/inFlight=true`；default 分支不改状态。

- [ ] **步骤 5：处理 completion 与有界重试。**

`applySaveCompletion` 先按 kind 分流。metadata 成功时设置 `persisted=job.Metadata.WorldTimeTicks`、清除 attempt/error/retry tick、更新 `base` 和 `lastSaveSuccess`；若新的 autosave 边界已把 pending 设回 true，不得清除它。失败时 `inFlight=false/pending=true`，并令 `state.attempt=job.Attempt`（job 创建时已做饱和加一），再使用既有：

```go
state.nextRetryTick = saturatingAddUint64(
    server.engine.TickCount(),
    retryDelay(server.config.RetryBaseTicks, server.config.RetryMaxTicks, state.attempt),
)
```

记录 `save metadata` 错误，不调用 `enqueueRetryCohort`。`PersistenceStatus` 增加 `MetadataPending`、`MetadataInFlight`；`AutosaveDrained` 只要求没有待提交边界或 in-flight，不因边界后的普通 tick 前进永久为 false。

- [ ] **步骤 6：把冻结后的最终值纳入可重试关服。**

`Shutdown` 冻结时保存最后一次 `engine.Step()` 结果并更新 `latest`，随后置 `pending=true`。`flushFrozen` 在每轮优先排出 chunk retry，再尝试 metadata job；drained 必须同时满足：

```go
!state.pending && !state.inFlight && state.persisted == state.latest
```

关服时忽略 tick 退避时间，允许下一次 `Shutdown(ctx)` 立即重试；首次失败返回错误并保持 server frozen、Store 未 Close。context 超时时不得泄漏 worker 或把未完成 metadata 报为成功。

- [ ] **步骤 7：运行故障注入与提交。**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server ./internal/storage -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
gofmt -w $(git diff --name-only --diff-filter=ACM -- '*.go')
gofmt -l .
git diff --check
git add internal/server
git commit -m "feat: 异步保存权威世界时间"
```

---

## 任务 7：按高度跨度标记增量天空光网格

**文件：**

- 修改：`internal/client/mirror_test.go`
- 修改：`internal/client/mirror.go`
- 修改：`internal/client/mesher_test.go`
- 新增或修改：`internal/client/mesher_bench_test.go`

**接口：**

- 输入：任务 1 的 `HighestOpaque`、现有 `addDirtyAround` 与 `sortedSectionKeys`。
- 输出：最高遮挡变化的精确、有界 dirty 集合；任务 9 的纵向测试直接观察网格 light。

- [ ] **步骤 1：写 dirty 范围 RED 测试。**

表驱动覆盖：

- 非列顶修改只产生既有方块/AO `±1` dirty；
- 同列 Y=64→80 产生会覆盖 Y=65..80 光样本的 section；
- Y=80→64 对称恢复；
- chunk 边和角的 3×3 方块邻域最多落入四个已加载 chunk；
- 从世界顶降到空列仍不超过 96 个唯一 `SectionKey`；
- 相同 change 重复加入由 map 合并。

核心断言：

```go
if got := len(update.Dirty); got > 96 {
    t.Fatalf("天空光 dirty=%d，超过 96", got)
}
```

- [ ] **步骤 2：运行 RED。**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/client -run "SkyDirty|RoofDirty" -count=1'
```

预期：当前只标记方块周围三个 Y 层，跨度用例失败。

- [ ] **步骤 3：实现 section 级跨度 helper。**

在每个 block change 写入前后读取同列 `oldTop/newTop`。先无条件调用现有 `addDirtyAround`；只有列顶变化时调用：

```go
func (mirror *Mirror) addSkyDirtyRange(
    dirty map[core.SectionKey]struct{},
    dimension core.DimensionID,
    position core.BlockPos,
    oldTop, newTop int32,
) {
    low, high := min(oldTop, newTop), max(oldTop, newTop)
    firstY := max(core.MinY, low)
    lastY := min(core.MaxY-1, high+1)
    firstSection := core.BlockPos{Y: firstY}.SectionIndex()
    lastSection := core.BlockPos{Y: lastY}.SectionIndex()
    for dz := int32(-1); dz <= 1; dz++ {
        for dx := int32(-1); dx <= 1; dx++ {
            column := core.BlockPos{X: position.X + dx, Y: firstY, Z: position.Z + dz}
            if _, loaded := mirror.Chunk(dimension, column.Chunk()); !loaded {
                continue
            }
            for sectionY := firstSection; sectionY <= lastSection; sectionY++ {
                dirty[core.SectionKey{Dimension: dimension, Pos: core.SectionPos{X: column.Chunk().X, Y: int32(sectionY), Z: column.Chunk().Z}}] = struct{}{}
            }
        }
    }
}
```

`SectionIndex()` 返回 `int`，写入 `SectionPos.Y` 时显式转为 `int32(sectionY)`。不得逐 Y 方块扫描来构造 dirty。`low+1..high` 是天空样本变化范围；owner face 的垂直邻接需要覆盖到 `low..high+1`，因此使用上述 clamp。

- [ ] **步骤 4：证明 snapshot/forget 与 stale result 仍正确。**

snapshot 到达和 forget 保持现有整 chunk 水平邻域 dirty。增加一个 worker in-flight 测试：屋顶变化后旧 stamps 失效，Drain 不接受旧 light；新任务完成后接受。世界时间字段变化不得调用 `Mesher.MarkDirty`。

- [ ] **步骤 5：增加最小 benchmark。**

在 `internal/client/mesher_bench_test.go` 增加：

```go
func BenchmarkSkyDirtyRange(b *testing.B) {
    mirror := NewMirror()
    chunks := mirror.dimension(core.Overworld)
    for z := int32(0); z <= 1; z++ {
        for x := int32(0); x <= 1; x++ {
            pos := core.ChunkPos{X: x, Z: z}
            chunks[pos] = &MirrorChunk{Revision: 1, Chunk: world.NewChunk(pos)}
        }
    }
    dirty := make(map[core.SectionKey]struct{}, 96)
    position := core.BlockPos{X: 15, Y: core.MaxY - 1, Z: 15}
    b.ReportAllocs()
    for b.Loop() {
        clear(dirty)
        mirror.addSkyDirtyRange(dirty, core.Overworld, position, core.MinY-1, core.MaxY-1)
    }
    if len(dirty) != 96 {
        b.Fatalf("dirty=%d，想要 96", len(dirty))
    }
}

func BenchmarkMesherSkySnapshot(b *testing.B) {
    mirror := NewMirror()
    chunks := mirror.dimension(core.Overworld)
    for z := int32(-1); z <= 1; z++ {
        for x := int32(-1); x <= 1; x++ {
            pos := core.ChunkPos{X: x, Z: z}
            chunks[pos] = &MirrorChunk{Revision: 1, Chunk: world.NewChunk(pos)}
        }
    }
    key := core.SectionKey{Dimension: core.Overworld, Pos: core.SectionPos{Y: 8}}
    b.ReportAllocs()
    for b.Loop() {
        neighborhood, stamps, ok := cloneNeighborhood(mirror, key)
        if !ok {
            b.Fatal("cloneNeighborhood 返回 false")
        }
        runtime.KeepAlive(neighborhood)
        runtime.KeepAlive(stamps)
    }
}
```

每次 benchmark 报告 allocs；确认 job/result channel 容量与 app 每帧 64 调度/回收上限不变。

- [ ] **步骤 6：运行 GREEN 与提交。**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/client ./internal/world ./internal/mesh -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/client -run "^$" -bench "Mesher|Sky" -benchmem -count=3'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
gofmt -w $(git diff --name-only --diff-filter=ACM -- '*.go')
gofmt -l .
git diff --check
git add internal/client
git commit -m "feat: 增量更新天空光网格"
```

---

## 任务 8：加入固定昼夜渲染状态

**文件：**

- 新增：`internal/render/lighting.go`
- 新增：`internal/render/lighting_test.go`
- 修改：`internal/render/renderer.go`
- 修改：`internal/render/renderer_test.go`
- 修改：`internal/render/avatar.go`
- 修改：`internal/render/avatar_test.go`
- 修改：`internal/render/drop.go`
- 修改：`internal/render/drop_test.go`
- 修改：`internal/render/shader/terrain.wgsl`
- 修改：`internal/render/shader/avatar.wgsl`
- 修改：`cmd/mcgo/app.go`
- 修改：`cmd/mcgo/app_test.go`
- 修改：`cmd/gfxspike/main.go`

**接口：**

- 输入：任务 5 的 `application.worldTimeTicks`、任务 2 的 `Quad.Light` 高四位。
- 输出：`WorldLightingAt` 和三个世界空间 renderer 的固定 uniform；HUD/name tag 不变。

- [ ] **步骤 1：写纯函数 RED 测试。**

```go
func TestWorldLightingAtCardinalPhases(t *testing.T) {
    tests := []struct{ tick uint64; sun, daylight float32 }{
        {0, 0, 0.15},
        {6000, 1, 1},
        {12000, 0, 0.15},
        {18000, 0, 0.15},
        {24000, 0, 0.15},
    }
    for _, test := range tests {
        got := render.WorldLightingAt(test.tick)
        if math.Abs(float64(got.Sun-test.sun)) > 1e-6 ||
            math.Abs(float64(got.Daylight-test.daylight)) > 1e-6 {
            t.Errorf("tick=%d lighting=%+v", test.tick, got)
        }
    }
}
```

再断言所有分量有限、周期性成立、正午 clear 等于既有日色、午夜 clear 等于固定夜色。

- [ ] **步骤 2：运行 RED 并实现 `WorldLightingAt`。**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/render -run WorldLighting -count=1'
```

实现：

```go
func WorldLightingAt(worldTimeTicks uint64) WorldLighting {
    phase := float64(worldTimeTicks % 24000)
    sun := max(0, math.Sin(2*math.Pi*phase/24000))
    daylight := 0.15 + 0.85*sun
    night := [4]float32{0.02, 0.03, 0.08, 1}
    day := [4]float32{0.42, 0.68, 0.92, 1}
    clear := night
    for i := 0; i < 3; i++ {
        clear[i] += float32(sun) * (day[i] - night[i])
    }
    return WorldLighting{Sun: float32(sun), Daylight: float32(daylight), ClearColor: clear}
}
```

- [ ] **步骤 3：扩展 terrain uniform 与 shader。**

terrain camera buffer 从 80 增至 96 字节；`cameraBytes(cam, lighting.Daylight)` 在原 20 个 float 后追加 `daylight,0,0,0`。WGSL：

```wgsl
struct Camera {
    view_proj: mat4x4f,
    cam_pos: vec4f,
    daylight: vec4f,
};

let sky = f32((light >> 4u) & 0xFu) / 15.0;
let base_light = 0.08 + sky * (camera.daylight.x - 0.08);
out.shade = face_shade(face) * ao_factor * base_light;
```

render pass 的 `ClearColor` 使用 `lighting.ClearColor`。新增 `TestTerrainDaylightHeadlessDraw`：创建 64×64 RGBA8/depth 离屏纹理、正午/午夜各提交一次并 `dev.Poll(true)`；不得创建 window。

- [ ] **步骤 4：扩展 avatar/drop uniform。**

`avatarCameraBytes` 从 64 增至 80，`avatarInstanceOffset` 仍为 256。上传矩阵后把 `lighting.Daylight` 写到 offset 64，余下三个 float 为零。`avatar.wgsl` 的 Camera 追加 `daylight: vec4f`，fragment 返回：

```wgsl
return vec4f(in.color.rgb * diffuse * camera.daylight.x, in.color.a);
```

Avatar 与 ItemDrop 都复用该 shader 和相同上传布局。更新 headless draw 测试传入 `WorldLightingAt(6000)`；增加上传字节断言，确认 daylight 位于 offset 64。

- [ ] **步骤 5：应用层每帧只计算一次。**

在 `renderFrame` 中创建一次：

```go
camera := render.Camera{ViewProj: a.camera.ViewProj(), Pos: a.camera.Pos}
lighting := render.WorldLightingAt(a.worldTimeTicks)
```

把同一 `camera/lighting` 传给 terrain、avatar、item-drop；name-tag 和 hotbar 调用完全不变。`cmd/gfxspike` 显式使用 `WorldLightingAt(6000)` 保持现有全亮演示。

- [ ] **步骤 6：证明时间不触发网格或 HUD 变暗。**

在 `cmd/mcgo/app_test.go` 连续发送两个只有 `ServerTick/WorldTimeTicks` 不同的 PlayerState，断言 `Mesher.Stats().DirtySections` 不变。保留并运行 `TestHotbarRendererHeadlessBlendOverExistingColor` 与 name-tag 测试，证明接口和颜色未接收 `WorldLighting`。

- [ ] **步骤 7：运行 GREEN 与提交。**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/render ./cmd/mcgo ./internal/client -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/render -run "Test(TerrainDaylightHeadlessDraw|AvatarRendererHeadlessDraw|ItemDropRendererHeadlessDraw|HotbarRendererHeadlessBlendOverExistingColor)$" -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
gofmt -w $(git diff --name-only --diff-filter=ACM -- '*.go')
gofmt -l .
git diff --check
git add internal/render cmd/mcgo
git commit -m "feat: 呈现权威昼夜循环"
```

---

## 任务 9：闭合 Memory/TCP、重启与兼容纵向语义

**文件：**

- 修改：`internal/server/multiplayer_memory_integration_test.go`
- 修改：`internal/server/tcp_integration_test.go`
- 修改：`internal/server/multiplayer_restart_test.go`
- 修改：`internal/server/persistence_integration_test.go`
- 修改：`internal/client/mirror_test.go`
- 修改：`internal/client/mesher_test.go`

**接口：**

- 输入：任务 1–8 的最终接口。
- 输出：同一套场景在 Memory/TCP 与磁盘重启前后收敛；无新生产接口。

- [ ] **步骤 1：写共享 transport 场景。**

场景固定为两个 Ready 玩家、同一 Y=64 地面列：

1. 两客户端接收相同 `WorldTimeTicks`；
2. 服务端在 Y=80 放屋顶并发布 block change；
3. 两边把 snapshot/change 应用到 Mirror，调度 Mesher，断言屋顶下目标 quad `Light=0x00`；
4. 服务端移除屋顶，断言最终 `Light=0xF0`；
5. 两边最终 chunk hash、revision 和世界时间一致。

共享断言函数接收 transport harness，不复制规则常量；Memory 与 TCP 各运行一次同一表。

- [ ] **步骤 2：证明旧协议在 Play 前拒绝。**

TCP raw client 发送 v8 `ClientHello`，断言只收到 v9 `HandshakeReject`，没有 world load、session attach 或 PlayerState。Memory login 使用相同 state machine 断言。

- [ ] **步骤 3：写磁盘迁移与精确重启场景。**

先用冻结 v1 fixture 创建 world：`store.Metadata()` 必须规范为 v2/time=0，`Server.StepForTest()` 的首个结果必须为 1；跨 autosave 后等待 `AutosaveDrained`，读取 `world.meta` 断言 v2。正常 Shutdown 后从已保存 metadata 读取冻结最终时间 T，重开并完成一 tick，新结果必须为 T+1；随后登录玩家的首个状态必须等于其所在 `TickResult.WorldTimeTicks`，不得回到 0 或墙钟相位。

- [ ] **步骤 4：写失败恢复场景。**

注入 metadata rename 前失败：旧 v2 文件字节不变，首次 Shutdown 返回错误，第二次解除故障后成功。注入 rename 后目录 sync 失败：canonical 解码为完整旧/新版之一，Shutdown 返回错误；重试成功后精确保存冻结最终时间。两种路径都断言 Store.Sync/Close 只在所有保存成功后调用。

- [ ] **步骤 5：证明不新增格式负载。**

运行并补强现有 golden：

```bash
rg -n "HeightMap|HighestOpaque|DirectSky" internal/network internal/storage/chunk_codec.go
```

预期：network 与 chunk codec 无高度/光照字段；唯一 storage 命中只允许测试说明或 `chunk.RebuildHeightMap()`。packet ID、玩家 schema v3、区块 schema v4、snapshot bytes 保持冻结。

- [ ] **步骤 6：运行纵向门禁与提交。**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server ./internal/network ./internal/storage ./internal/client ./internal/render -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
gofmt -w $(git diff --name-only --diff-filter=ACM -- '*.go')
gofmt -l .
git diff --check
git add internal/server internal/network internal/storage internal/client internal/render
git commit -m "test: 闭合多人昼夜与重启语义"
```

---

## 任务 10：升级 benchmark scenario v11 与中文文档

**文件：**

- 修改：`cmd/mcgo/benchmark.go`
- 修改：`cmd/mcgo/benchmark_v5_test.go`
- 修改：`cmd/mcgo/benchmark_v6_test.go`
- 修改：`cmd/perfcheck/main.go`
- 修改：`cmd/perfcheck/main_test.go`
- 修改：`README.md`
- 修改：`docs/notes/lan-server.md`
- 修改：`docs/notes/perf-baseline.md`

**接口：**

- 输入：任务 1–9 已改变的协议、内存、mesher、uniform 与保存 workload。
- 输出：producer v11、唯一 `10:11` 迁移、中文运行/回退说明；正式基线文件暂不改。

- [ ] **步骤 1：写 scenario/version RED 测试。**

把 producer 断言改为 11：

```go
func TestBenchmarkScenarioVersionIncludesAuthoritativeDaylight(t *testing.T) {
    if scenarioVersion != 11 {
        t.Fatalf("scenarioVersion=%d，想要权威昼夜后的 v11", scenarioVersion)
    }
}
```

在 perfcheck 表中增加/修改：v10→v11 无授权拒绝、`10:11` 授权通过完整性与绝对门禁、`9:10`/`8:9` 等参数全部拒绝、v6-v10 单份历史报告仍能校验、v11 同场景 Memory→TCP 仍执行稳定指标。

- [ ] **步骤 2：运行 RED。**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo ./cmd/perfcheck -run "Scenario|Upgrade|Migration" -count=1'
```

预期：producer 仍为 10，允许参数仍为 `9:10`。

- [ ] **步骤 3：做最小版本替换。**

`cmd/mcgo/benchmark.go`：

```go
const scenarioVersion = 11
```

`cmd/perfcheck/main.go`：

```go
allowScenarioUpgrade := flag.String(
    "allow-scenario-upgrade", "", "只允许显式的 10:11 场景迁移",
)

allowedScenarioUpgrade := baseline.ScenarioVersion == 10 &&
    current.ScenarioVersion == 11 &&
    *allowScenarioUpgrade == "10:11"
```

删除对 `9:10` 的授权分支，不增加迁移表或配置结构。不同 scenario 仍先校验硬件一致，迁移只跳过相对回归；v11 当前报告必须通过现有完整性和绝对门禁。

- [ ] **步骤 4：冻结性能契约未变化。**

测试显式断言：2560×1440、still/flying 采样、RSS、tick p99 `<10ms`、flying p99 `<12ms`、2048 `remote_gpu_complete`、Memory/TCP parity、最大相对回归 20% 都与 v10 相同。不要改 `client.ScenarioV8GPUCompletionSamples` 名称或值。

- [ ] **步骤 5：更新中文文档。**

文档必须写明：

- 一个昼夜 24000 个服务端 tick，客户端不读墙钟；
- metadata v1 首次正常保存升级 v2，回退 v8 程序前必须恢复 v2 写入前的完整世界备份；
- 协议 v9 仅面向可信 LAN，v8 在登录前拒绝；
- 直射天空光只处理每列最高非空气方块，洞口侧光、方块光、火把和透明方块未实现；
- scenario v11 只允许显式 `10:11`，M2 基线不动，M5 正式链另行授权。

- [ ] **步骤 6：运行 GREEN 与提交。**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo ./cmd/perfcheck ./internal/network -race -count=1'
openspec validate --all --strict --no-interactive
gofmt -w $(git diff --name-only --diff-filter=ACM -- '*.go')
gofmt -l .
git diff --check
git add cmd/mcgo cmd/perfcheck README.md docs/notes/lan-server.md docs/notes/perf-baseline.md
git commit -m "feat: 升级 benchmark scenario v11"
```

---

## 任务 11：执行候选版本完整门禁并冻结 HEAD

**文件：**

- 修改：`openspec/changes/m4g-authoritative-daylight/tasks.md`
- 读取：本 change 的 proposal、三份 delta specs、design、tasks
- 读取：所有任务 1–10 的 tracked diff

**接口：**

- 输入：完整 M4G 实现。
- 输出：通过全部自动门禁的冻结候选提交；任务 12 只能在该 HEAD 上运行。

- [ ] **步骤 1：逐 requirement 建立覆盖表。**

在执行记录中逐项列出并核对：权威 tick、metadata v2/重启、协议 v9/旧状态、直射天空光、缺失邻区、96 dirty、固定曲线、HUD/name tag、512 字节/384 扫描/零稳定分配、scenario v11、M5/M2 边界。每项必须指向至少一个实现文件和测试名；发现空项先补测试/实现并单独提交。

- [ ] **步骤 2：扫描明确非目标。**

运行：

```bash
rg -n "torch|block.?light|light.?worker|weather|moon|sun.?mesh|transparent|flood" internal cmd
rg -n "HeightMap|DirectSky|WorldTimeTicks" internal/network internal/storage
```

人工核对命中：不得新增横向传播、方块光、透明注册表、天气/天体、独立 worker、额外 packet 或 chunk payload。第二条在 storage 只允许 metadata 字段和 chunk decode 后 `RebuildHeightMap`，network 只允许 `WorldTimeTicks`。

- [ ] **步骤 3：运行全仓正确性门禁。**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race'
zsh -ic 'gvm use go1.26.0 >/dev/null && go vet ./...'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
gofmt -l .
git diff --check
openspec validate --all --strict --no-interactive
```

预期：测试/校验退出 0，`gofmt -l .` 与 `git diff --check` 无输出。

- [ ] **步骤 4：运行协议、热路径、故障与 headless 门禁。**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "^$" -fuzz FuzzSmallPacketCodec -fuzztime=10s'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "^$" -bench SmallPacketCodec -benchmem -count=3'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/world ./internal/client -run "^$" -bench "Height|Sky|Mesher" -benchmem -count=3'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage ./internal/server -run "Metadata|WorldTime" -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/render -run "Test(TerrainDaylightHeadlessDraw|AvatarRendererHeadlessDraw|ItemDropRendererHeadlessDraw)$" -count=1'
```

确认无窗口、无遗留 `mcgo`/`perfcheck` 进程、无阈值放宽。

- [ ] **步骤 5：勾选任务并冻结。**

把 OpenSpec tasks 1–10 的已完成项勾选；保留 11/12 未完成。再次执行 strict 与 diff check，然后：

```bash
git add openspec/changes/m4g-authoritative-daylight/tasks.md
git commit -m "chore: 冻结 M4G 权威昼夜候选"
git rev-parse HEAD
```

记录完整 HEAD。此后任何 producer、scenario、阈值、光照模型、协议、存档或热路径修改都必须新建修复提交并重新执行本任务。

---

## 任务 12：经明确授权建立一次性 M5 scenario v11 基线

**文件：**

- 通过后修改：`docs/notes/perf-baseline-m5.json`
- 通过后修改：`docs/notes/perf-baseline.md`
- 通过后修改或新增：`docs/notes/perf-baseline-m5.md`

**接口：**

- 输入：任务 11 的冻结 HEAD 与已归档 M4F scenario v10 M5 基线。
- 输出：只有 Memory 和 TCP 各一次正式报告都通过时才提升的 M5 v11 精确字节；M2 文件不变。

- [ ] **步骤 1：完成冷却与双采样只读预检。**

从任务 11 最后一个完整门禁进程退出起至少 5 分钟不再运行全仓 race、fuzz、benchmark 或 producer。随后运行：

```bash
date -u '+%Y-%m-%dT%H:%M:%SZ'
sysctl -n vm.loadavg
pmset -g batt
pmset -g custom
pgrep -fl 'mcgo|perfcheck'
```

至少相隔 30 秒重复一次。两组都必须满足：1/5 分钟 load average `<6.0`、AC Power、AC `lowpowermode=0`、电量 `>=50%`、没有遗留 `mcgo`/`perfcheck`。不主动终止用户进程、清缓存或改供电设置；条件不满足只停止并报告。

- [ ] **步骤 2：冻结身份、哈希与全新路径。**

```bash
git rev-parse HEAD
shasum -a 256 docs/notes/perf-baseline.json docs/notes/perf-baseline-m5.json
system_profiler SPHardwareDataType
sw_vers
zsh -ic 'gvm use go1.26.0 >/dev/null && go version'
M4G_HEAD=$(git rev-parse --short=12 HEAD)
M4G_MEMORY_REPORT=/tmp/mcgo-m5-v11-$M4G_HEAD-memory.json
M4G_MEMORY_LOG=/tmp/mcgo-m5-v11-$M4G_HEAD-memory.log
M4G_TCP_REPORT=/tmp/mcgo-m5-v11-$M4G_HEAD-tcp.json
M4G_TCP_LOG=/tmp/mcgo-m5-v11-$M4G_HEAD-tcp.log
test ! -e "$M4G_MEMORY_REPORT"
test ! -e "$M4G_MEMORY_LOG"
test ! -e "$M4G_TCP_REPORT"
test ! -e "$M4G_TCP_LOG"
```

任一路径存在就停止并请求新后缀，不删除或覆盖证据。

- [ ] **步骤 3：暂停并请求用户对精确边界授权。**

报告冻结 HEAD、M2/M5 哈希、硬件/OS/Go、两组静稳样本、四个全新路径，以及以下不可变规则：Memory 先、TCP 后；各只运行一次；任一步失败立即停止；不重跑、不放宽阈值、不覆盖基线。没有用户明确授权不得继续。

- [ ] **步骤 4：授权后复核并只运行一次 Memory。**

重新执行步骤 2 的变量赋值、路径不存在检查，以及一次 `git rev-parse HEAD`、load、供电、进程复核；任何变化都停止并重新请求授权。通过后运行：

```bash
TERM=xterm-256color zsh -ic "gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport memory --perf-output '$M4G_MEMORY_REPORT'" | tee "$M4G_MEMORY_LOG"
zsh -ic "gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck --baseline docs/notes/perf-baseline-m5.json --current '$M4G_MEMORY_REPORT' --max-regression 0.20 --allow-scenario-upgrade 10:11"
```

任一命令失败：保留证据、停止、不运行 TCP、不重跑、不改基线。

- [ ] **步骤 5：Memory 通过后只运行一次 TCP。**

```bash
TERM=xterm-256color zsh -ic "gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport tcp --perf-output '$M4G_TCP_REPORT'" | tee "$M4G_TCP_LOG"
zsh -ic "gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck --baseline '$M4G_MEMORY_REPORT' --current '$M4G_TCP_REPORT' --max-regression 0.20"
```

失败时停止并保留两份报告，不重跑或覆盖基线。

- [ ] **步骤 6：两步都通过后提升精确 Memory 字节。**

```bash
cp "$M4G_MEMORY_REPORT" docs/notes/perf-baseline-m5.json
cmp -s "$M4G_MEMORY_REPORT" docs/notes/perf-baseline-m5.json
shasum -a 256 "$M4G_MEMORY_REPORT" "$M4G_MEMORY_LOG" "$M4G_TCP_REPORT" "$M4G_TCP_LOG"
shasum -a 256 docs/notes/perf-baseline.json docs/notes/perf-baseline-m5.json
```

更新性能记录：冻结 HEAD、全部命令、报告/日志哈希、M5 身份、被替代 v10 身份、Memory→TCP 结果。M2 哈希必须与步骤 2 完全相同。

- [ ] **步骤 7：验证并提交基线。**

```bash
zsh -ic "gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck --baseline docs/notes/perf-baseline-m5.json --current '$M4G_MEMORY_REPORT' --max-regression 0.20"
openspec validate --all --strict --no-interactive
git diff --check
git add docs/notes/perf-baseline-m5.json docs/notes/perf-baseline.md docs/notes/perf-baseline-m5.md
git commit -m "chore: 建立 M5 scenario v11 基线"
```

---

## 任务 13：同步主规格并归档 M4G

**文件：**

- 修改：`openspec/specs/authoritative-daylight/spec.md`
- 修改：`openspec/specs/bounded-benchmark-workload/spec.md`
- 修改：`openspec/specs/hardware-performance-baselines/spec.md`
- 修改：`AGENTS.md`
- 修改：`openspec/config.yaml`
- 归档：`openspec/changes/m4g-authoritative-daylight`

**接口：**

- 输入：任务 12 已通过并提交的稳定行为和基线。
- 输出：主规格、项目基线说明与归档 change 一致；无功能代码变化。

- [ ] **步骤 1：完成最终门禁。**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race'
zsh -ic 'gvm use go1.26.0 >/dev/null && go vet ./...'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
gofmt -l .
git diff --check
openspec validate --all --strict --no-interactive
```

- [ ] **步骤 2：勾选剩余 OpenSpec tasks。**

确认任务 11 的一次性链证据和基线提交存在后，勾选 11.1–11.4；完成本任务同步核对后勾选 12.1–12.2。不得提前勾选归档步骤。

- [ ] **步骤 3：同步 delta specs。**

使用 `openspec-sync-specs`：新增 `authoritative-daylight` 主规格，并把 scenario v11、唯一 `10:11` 迁移、M5 v11/M2 不变边界合入两份既有主规格。逐字核对 metadata v2、协议 v9、玩家 schema v3、区块 schema v4 与所有非目标。

- [ ] **步骤 4：更新项目当前基线。**

`AGENTS.md` 与 `openspec/config.yaml` 只更新已经成为代码事实的版本和 M4G 能力；不得写入未来方块光、透明方块或怪物规则。

- [ ] **步骤 5：归档并严格验证。**

使用 `openspec-archive-change` 归档 `m4g-authoritative-daylight`，然后运行：

```bash
openspec validate --all --strict --no-interactive
git diff --check
git status --short --branch
```

预期：strict 全通过；tracked 工作树只含同步/归档预期文件，`midscene_run/` 仍未跟踪且未暂存。

- [ ] **步骤 6：提交归档。**

```bash
git add AGENTS.md openspec/config.yaml openspec/specs openspec/changes/archive
git commit -m "chore: 归档 M4G 权威昼夜"
```

提交后再次运行 `openspec validate --all --strict --no-interactive` 与 `git status --short --branch`，报告完整提交链、测试证据、正式报告哈希和剩余未跟踪用户文件；除非用户要求，不合并、不推送。

---

## 完成定义

- 服务端每个完成 tick 恰好推进一次绝对时间，Memory/TCP 客户端只使用最新有效 PlayerState，并在 metadata v2 重启后连续。
- 相同权威方块在服务端、Memory/TCP 镜像和重启后派生相同列顶；露天 quad 为 `0xF0`、遮蔽为 `0x00`，没有光照存档或网络 payload。
- 屋顶变化只重做必要高度跨度，单变化不超过 96 key；稳定时间推进不分配、不 I/O、不重网格。
- terrain、远端玩家、掉落物和 clear color 使用同一固定昼夜相位，HUD/name tag 不变。
- 协议 v9、metadata v2、scenario v11 与唯一 `10:11` 迁移生效；玩家 schema v3、区块 schema v4、M2 基线和所有既有阈值保持不变。
- 全仓 race/vet/archcheck/gofmt/OpenSpec/headless/适用 benchmark 全通过，M5 v11 正式链只在明确授权后各运行一次并留下可核验记录。
