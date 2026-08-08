# M4M Propagated Skylight Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在不修改服务端、协议或存档的前提下，让客户端从权威区块镜像派生 `0..15` 的有界横向天空光，并用无窗口视觉场景和 scenario v14 正式性能链完成验收。

**Architecture:** `client.Mesher` 继续克隆现有 `3×3×3` 不可变区段邻域；`mesh` 在每个 worker 复用的固定 `48³` scratch 中执行多源 BFS，再把相邻空气位置的亮度写入 `Quad.Light` 高四位。Mirror 只扩大有界 dirty 体积，现有 revision stamp、任务队列、过期淘汰和 terrain shader 均复用。

**Tech Stack:** Go 1.26（用户现有 gvm）、标准库、现有 `internal/world`/`mesh`/`client`/`render`/`gfx`、OpenSpec、WebGPU 无窗口 capture、`cmd/perfcheck`。

## Global Constraints

- 开始实现前，`main` MUST 已包含 `chore/archive-tick-boundary-diagnostics@f72fa71` 或等价归档提交，且 `openspec list --json` 不得再列出 `benchmark-tick-boundary-diagnostics`。
- Go MUST 使用用户现有的 gvm Go 1.26；不得下载或安装另一份 Go。
- 代码注释、GoDoc、测试说明和文档 MUST 使用中文；Go 标识符及既有英文协议名保留英文。
- 自动测试 MUST 全程无窗口；只有本计划明确的 `--capture`/`--benchmark` 无头路径可执行，不得启动交互式客户端。
- 线上协议 MUST 保持 v13，玩家 schema MUST 保持 v5，区块 schema MUST 保持 v6，metadata MUST 保持 v2。
- 方块光低四位 MUST 保持 `0`；不得加入火把、透明方块、长期客户端光照缓存、独立 worker pool、第三方依赖或服务端光照状态。
- 直射起点计为第 1 格时亮度为 `15`，第 15 格 MUST 为 `1`，下一格 MUST 为 `0`；未知邻区 MUST 按实心且黑暗处理。
- 传播 scratch MUST 固定为 `48³` 单元并由每个现有 Mesher worker 复用；不得因亮度数组或 BFS 队列产生逐任务堆分配。
- 普通方块变化 dirty 集合 MUST 不超过 27 个区段；列顶最坏变化 MUST 不超过 216 个区段；既有队列和每帧工作上限不得放宽。
- benchmark MUST 升为 scenario v14，只允许显式 `13:14` 迁移；分辨率、阶段时长、样本、指标、绝对门禁和 `20%` 相对阈值不得改变。
- M5 Memory/TCP 正式 producer 各只能执行一次，必须在冻结 HEAD、静稳预检和用户明确授权后进行；失败立即停止。M2 基线 MUST 保持字节不变。
- 每个任务按 red → green → refactor 执行，验证通过后只提交该任务文件；不得改 Hook 或门禁规避失败。

## File Structure

- Create: `openspec/changes/m4m-propagated-skylight/.openspec.yaml` — change 元数据。
- Create: `openspec/changes/m4m-propagated-skylight/{proposal.md,design.md,tasks.md}` — OpenSpec 规划产物。
- Create: `openspec/changes/m4m-propagated-skylight/specs/{authoritative-daylight,visual-verification,bounded-benchmark-workload,hardware-performance-baselines}/spec.md` — 四份 delta spec。
- Create: `internal/mesh/skylight.go` — 固定 scratch、索引和多源 BFS。
- Create: `internal/mesh/skylight_internal_test.go` — 队列边界与稳定零 scratch 分配测试。
- Modify: `internal/world/neighborhood.go` — 把 `At`/`SkyLight` 采样扩到 `[-16,31]`。
- Modify: `internal/world/neighborhood_test.go` — 完整 halo、缺失邻区和越界测试。
- Modify: `internal/mesh/greedy.go` — `MeshSection` 消费复用 scratch。
- Modify: `internal/mesh/{greedy_test.go,skylight_test.go}` — 传播等级、遮挡和跨边界测试。
- Modify: `internal/client/{mesher.go,mirror.go,skylight_test.go}` — worker scratch 生命周期、dirty 体积和过期结果。
- Modify: `cmd/gfxspike/main.go`, `internal/render/bench_test.go`, `internal/server/daylight_integration_test.go` — 更新 `MeshSection` 调用并补动态闭环。
- Modify: `cmd/mcgo/{capture.go,capture_test.go}` — 固定 tunnel 夹具、收敛检查与第四个视觉场景。
- Create: `cmd/mcgo/testdata/golden/skylight-tunnel.png` — 人工确认后的第四张基线。
- Modify: `cmd/mcgo/benchmark.go`, `cmd/mcgo/{benchmark_v5_test.go,benchmark_v6_test.go}` — producer scenario v14。
- Modify: `cmd/perfcheck/{main.go,main_test.go}` — 唯一 `13:14` 迁移与 v14 校验。
- Modify: `docs/notes/perf-baseline-m5.json`, `docs/notes/perf-baseline.md` — 通过正式链后的 M5 v14 字节与证据。
- Modify: `README.md`, `AGENTS.md`, `CLAUDE.md`, `openspec/config.yaml` — M4M 能力、限制、版本和当前基线。
- Modify: `openspec/specs/{authoritative-daylight,visual-verification,bounded-benchmark-workload,hardware-performance-baselines}/spec.md` — 归档前同步稳定契约。

---

### Task 1: 固化 M4M OpenSpec change

**Files:**
- Create: `openspec/changes/m4m-propagated-skylight/.openspec.yaml`
- Create: `openspec/changes/m4m-propagated-skylight/proposal.md`
- Create: `openspec/changes/m4m-propagated-skylight/design.md`
- Create: `openspec/changes/m4m-propagated-skylight/tasks.md`
- Create: `openspec/changes/m4m-propagated-skylight/specs/authoritative-daylight/spec.md`
- Create: `openspec/changes/m4m-propagated-skylight/specs/visual-verification/spec.md`
- Create: `openspec/changes/m4m-propagated-skylight/specs/bounded-benchmark-workload/spec.md`
- Create: `openspec/changes/m4m-propagated-skylight/specs/hardware-performance-baselines/spec.md`

**Interfaces:**
- Consumes: `docs/superpowers/specs/2026-08-08-m4m-propagated-skylight-design.md` 的已确认行为。
- Produces: active change `m4m-propagated-skylight`；后续任务只实现其中契约。

- [ ] **Step 1: 验证前置归档与基线**

Run:

```bash
git fetch origin main
git merge-base --is-ancestor f72fa71 origin/main
openspec list --json
git status --short --branch
zsh -ic 'go version'
```

Expected: `f72fa71` 是 `origin/main` 祖先；OpenSpec active change 列表为空；工作树只有本规划分支允许的文件；Go 输出 `go1.26.0`。任一不满足则停止，不在功能分支代替用户合并归档 PR。

- [ ] **Step 2: 用批准设计生成完整 change**

加载并使用 `openspec-propose` skill；生成 change 时使用以下精确输入，不添加其他能力：

```text
change: m4m-propagated-skylight
目标：客户端从权威方块镜像派生 0..15 横向天空光；直射起点为 15，每传播一格减 1；未知邻区为暗；异步有界重算；新增 skylight-tunnel 无窗口视觉场景；benchmark 升到 scenario v14 并只允许 13:14；通过一次性 M5 Memory/TCP 链后更新基线。
修改能力：authoritative-daylight、visual-verification、bounded-benchmark-workload、hardware-performance-baselines。
非目标：协议/存档/metadata 变更、方块光、火把、透明方块、服务端光照、长期缓存、新 worker pool、门禁放宽。
实现约束：3×3×3 不可变快照、48³ 固定 scratch、普通 dirty<=27、列顶 dirty<=216、每 worker 复用、现有 revision stamp、中文产物。
```

`authoritative-daylight` delta MUST 修改既有“直射天空光”要求，使总天空光允许侧向传播，并把 dirty 上限 `96` 改为 `216`；`visual-verification` MUST 增加 tunnel 场景和收敛失败；`bounded-benchmark-workload` MUST 把 v13→v14 与唯一 `13:14` 写清；`hardware-performance-baselines` MUST 要求 M5 v14 正式 Memory/TCP 链且 M2 不变。

- [ ] **Step 3: 严格校验且逐条对照设计**

Run:

```bash
openspec validate --all --strict --no-interactive
rg -n "48³|27|216|scenario v14|13:14|协议 v13|schema v5|schema v6|metadata v2|skylight-tunnel" openspec/changes/m4m-propagated-skylight
git diff --check
```

Expected: strict 全通过；每项冻结常量都命中；不存在未定项或未批准范围。

- [ ] **Step 4: 提交 OpenSpec change**

```bash
git add openspec/changes/m4m-propagated-skylight
git commit -m "docs: 建立 M4M 天空光传播变更"
```

### Task 2: 扩展 Neighborhood 的固定 halo 采样

**Files:**
- Modify: `internal/world/neighborhood.go:23`
- Modify: `internal/world/neighborhood_test.go:22`

**Interfaces:**
- Consumes: 现有 `Neighborhood.Center`、`Around[3][3][3]`、`Heights[3][3]`。
- Produces: `(*Neighborhood).At(x,y,z int) BlockID` 与 `SkyLight(x,y,z int) uint8` 支持每轴 `[-16,31]`；域外保持 Barrier/0。

- [ ] **Step 1: 写完整 halo 的失败测试**

在 `internal/world/neighborhood_test.go` 增加：

```go
func TestNeighborhoodSamplesWholeThreeByThreeByThreeHalo(t *testing.T) {
	center := world.NewChunk(core.ChunkPos{})
	west := world.NewChunk(core.ChunkPos{X: -1})
	east := world.NewChunk(core.ChunkPos{X: 1})
	west.SetBlock(0, 64, 3, core.StoneID)
	east.SetBlock(15, 64, 4, core.DirtID)
	n := world.NeighborhoodAt(chunkGetter(center, west, east), center.Pos, sectionIndexFor(64))
	localY := int(64-core.MinY) & core.SectionMask
	if got := n.At(-16, localY, 3); got != core.StoneID {
		t.Fatalf("西侧 halo 方块=%d，想要 stone", got)
	}
	if got := n.At(31, localY, 4); got != core.DirtID {
		t.Fatalf("东侧 halo 方块=%d，想要 dirt", got)
	}
	if got := n.At(-17, localY, 3); got != world.BarrierID {
		t.Fatalf("halo 外方块=%d，想要 barrier", got)
	}
}
```

再增加 `SkyLight(-16,...)`、`SkyLight(31,...)`、缺失邻区为 `0`、`y=-17/32` 为 `0` 的断言。

- [ ] **Step 2: 运行测试确认红灯**

Run: `zsh -ic 'go test ./internal/world -run "TestNeighborhood(SamplesWhole|SkyLight)" -count=1'`

Expected: `At(-16,...)` 或 `At(31,...)` 返回 Barrier，新增测试失败。

- [ ] **Step 3: 用一个坐标映射 helper 完成最小实现**

在 `internal/world/neighborhood.go` 用同一 helper 替换只处理 `-1/16` 的分支：

```go
const neighborhoodHaloMin = -core.SectionSize
const neighborhoodHaloMax = 2*core.SectionSize - 1

func neighborCell(v int) (cell, local int) {
	if v < neighborhoodHaloMin || v > neighborhoodHaloMax {
		return -1, 0
	}
	shifted := v + core.SectionSize
	return shifted >> core.SectionShift, shifted & core.SectionMask
}
```

`At` 对三个轴都调用 `neighborCell`；任一 `cell < 0` 返回 `BarrierID`，`[1][1][1]` 继续读 `Center`，其余读 `Around`。`SkyLight` 先校验 y 也在 halo，再用 `neighborCell` 映射 x/z。

- [ ] **Step 4: 验证 world 包并做变异检查**

Run:

```bash
gofmt -w internal/world/neighborhood.go internal/world/neighborhood_test.go
zsh -ic 'go test ./internal/world -race -count=1'
gofmt -l internal/world
```

Mutation: 暂时把 `neighborhoodHaloMin` 改为 `-15`，确认西侧 `-16` 用例失败；恢复后重跑为 `ok`。

- [ ] **Step 5: 提交**

```bash
git add internal/world/neighborhood.go internal/world/neighborhood_test.go
git commit -m "feat: 扩展天空光邻域采样范围"
```

### Task 3: 实现固定 scratch 的多源天空光传播

**Files:**
- Create: `internal/mesh/skylight.go`
- Create: `internal/mesh/skylight_internal_test.go`
- Modify: `internal/mesh/greedy.go:20`
- Modify: `internal/mesh/greedy_test.go`
- Modify: `internal/mesh/skylight_test.go`
- Modify: `internal/client/mesher.go:305`
- Modify: `cmd/gfxspike/main.go:114`
- Modify: `internal/render/bench_test.go:38`
- Modify: `internal/server/daylight_integration_test.go:78`

**Interfaces:**
- Produces: `mesh.NewSkyLightScratch() *mesh.SkyLightScratch`。
- Produces: `mesh.MeshSection(n *world.Neighborhood, reg mesh.Registry, scratch *mesh.SkyLightScratch) []mesh.Quad`。
- Consumes: Task 2 的完整 halo 采样；现有 `Registry.Opaque`。

- [ ] **Step 1: 写传播等级、遮挡与边界失败测试**

在 `internal/mesh/skylight_test.go` 增加一个带单格天窗和封闭屋顶的固定邻域，采样地面顶面 quad：

```go
tests := []struct {
	name string
	x    int
	want uint8
}{
	{"直射", 0, 15},
	{"一步", 1, 14},
	{"从起点计第十五格", 14, 1},
	{"下一格归零", 15, 0},
}
```

另加三条：在路径中间放 `StoneID` 后其后方为 `0`；天窗位于相邻区块边缘时亮度跨区块连续；相同输入连续两次 `reflect.DeepEqual(quadsA, quadsB)`。

- [ ] **Step 2: 写 scratch 容量与分配失败测试**

新建 `internal/mesh/skylight_internal_test.go`，使用 `package mesh`：

```go
func TestSkyLightScratchExactCapacityAndStableBuildDoesNotAllocate(t *testing.T) {
	if got, want := len(new(SkyLightScratch).levels), 48*48*48; got != want {
		t.Fatalf("levels=%d，想要 %d", got, want)
	}
	if got, want := len(new(SkyLightScratch).queue), 48*48*48; got != want {
		t.Fatalf("queue=%d，想要 %d", got, want)
	}
	n := fullyLoadedAirNeighborhood()
	scratch := NewSkyLightScratch()
	scratch.build(n, internalTestRegistry{})
	if got := testing.AllocsPerRun(100, func() { scratch.build(n, internalTestRegistry{}) }); got != 0 {
		t.Fatalf("稳定传播分配=%v，想要 0", got)
	}
}
```

`fullyLoadedAirNeighborhood` 必须填满 27 个非 nil 空气 Section 和九份 `HeightsPresent=true`，让 110592 个单元都成为合法直射起点，证明队列恰好容纳最坏输入。

- [ ] **Step 3: 运行红灯**

Run: `zsh -ic 'go test ./internal/mesh -run "Test(SkyLightScratch|MeshSectionPropagated)" -count=1'`

Expected: `SkyLightScratch` 未定义或传播亮度仍只有 `0/15`。

- [ ] **Step 4: 实现固定数组与 BFS**

`internal/mesh/skylight.go` 使用以下固定形状，不增加 interface：

```go
const (
	skyLightMin    = -core.SectionSize
	skyLightSide   = 3 * core.SectionSize
	skyLightVolume = skyLightSide * skyLightSide * skyLightSide
)

type SkyLightScratch struct {
	levels [skyLightVolume]uint8
	queue  [skyLightVolume]uint32
	head   int
	tail   int
}

func NewSkyLightScratch() *SkyLightScratch { return new(SkyLightScratch) }
```

`build` 必须先 `clear(s.levels[:])` 并清零 head/tail，再按 x/y/z 固定顺序把 `n.SkyLight(...) == 15 && !reg.Opaque(n.At(...))` 的单元全部设为 15 入队。随后固定按 `-X,+X,-Y,+Y,-Z,+Z` 扩散：候选 `current-1` 为 0、域外、不透明或已有亮度不低于候选时跳过；否则写入并入队。`tail == len(queue)` 时 panic，中文说明内部队列溢出。

- [ ] **Step 5: 让 MeshSection 显式消费 scratch**

修改签名并在生成 mask 前构建一次光场：

```go
func MeshSection(n *world.Neighborhood, reg Registry, light *SkyLightScratch) []Quad {
	if light == nil {
		panic("mesh: nil sky light scratch")
	}
	light.build(n, reg)
	// 既有 greedy 逻辑保持不变。
}
```

把原来的 `n.SkyLight(q...) << 4` 改为 `light.at(q...) << 4`。低四位不得写入任何值。

- [ ] **Step 6: 每个 Mesher worker 只创建一次 scratch**

修改 `internal/client/mesher.go`：

```go
func (mesher *Mesher) work() {
	defer mesher.wg.Done()
	light := mesh.NewSkyLightScratch()
	// 循环保持原样，领取任务后调用 mesher.handle(job, light)。
}

func (mesher *Mesher) handle(job mesherJob, light *mesh.SkyLightScratch) {
	// panic 隔离保持原样。
	quads := mesh.MeshSection(job.neighborhood, mesher.registry, light)
	// 组装 result 保持原样。
}
```

更新其余直接调用者：长循环在循环外创建一份 scratch；测试 helper 每次显式创建。不得增加兼容 wrapper 或 `sync.Pool`。

- [ ] **Step 7: 验证与变异**

Run:

```bash
gofmt -w internal/mesh/skylight.go internal/mesh/skylight_internal_test.go internal/mesh/greedy.go internal/mesh/greedy_test.go internal/mesh/skylight_test.go internal/client/mesher.go cmd/gfxspike/main.go internal/render/bench_test.go internal/server/daylight_integration_test.go
zsh -ic 'go test ./internal/mesh ./internal/client ./internal/render ./internal/server ./cmd/gfxspike -race -count=1'
zsh -ic 'go test ./internal/mesh -run "^$" -bench "SkyLight|MeshTerrain" -benchmem -count=3'
gofmt -l internal/mesh internal/client internal/render internal/server cmd/gfxspike
```

Mutation: 暂时把候选改成 `current`，确认“一步=14”测试失败；恢复后全绿。

- [ ] **Step 8: 提交**

```bash
git add internal/mesh internal/client/mesher.go cmd/gfxspike/main.go internal/render/bench_test.go internal/server/daylight_integration_test.go
git commit -m "feat: 增加有界天空光传播"
```

### Task 4: 扩大且限制 Mirror dirty 体积

**Files:**
- Modify: `internal/client/mirror.go:164-310`
- Modify: `internal/client/skylight_test.go:49-280`

**Interfaces:**
- Produces: 普通变化最多 27 个已加载区段；列顶跨度最多 216 个。
- Consumes: 现有 `MirrorUpdate.Dirty`、dirty map 去重和 Mesher MarkDirty。

- [ ] **Step 1: 先改测试为新契约并确认红灯**

把旧的“非列顶不超过 9”“最坏不超过 96/4 区块”测试替换为：

```go
func TestMirrorNonTopChangeDirtiesPropagatedSkyVolume(t *testing.T) {
	// 在局部 x/z=8、区段中部改变方块，加载完整 3×3 邻区。
	// 枚举位置每轴 ±16 相交的 27 个区段，逐个断言存在且 len==27。
}

func TestMirrorSkyDirtyStaysWithinNineChunksAndTwoHundredSixteenSections(t *testing.T) {
	// 列顶从 MinY 升到 MaxY-1。
	// 加载完整 3×3 邻区，枚举九个 chunk 的全部 24 个区段，逐个断言存在且 len==216。
}
```

Run: `zsh -ic 'go test ./internal/client -run "TestMirror(NonTop|SkyDirty)" -count=1'`

Expected: 旧实现只标脏 9/96 范围，至少一条覆盖断言失败。

- [ ] **Step 2: 用一个 helper 替换两个旧 helper**

删除 `addDirtyAround` 与 `addSkyDirtySpan`，新增：

```go
const propagatedSkyDirtyRadius int32 = 16

func (mirror *Mirror) addSkyDirtyVolume(
	dirty map[core.SectionKey]struct{}, dimension core.DimensionID,
	position core.BlockPos, lowY, highY int32,
) {
	lowY = max(lowY, core.MinY)
	highY = min(highY, core.MaxY-1)
	if lowY > highY { return }
	minChunkX := (position.X - propagatedSkyDirtyRadius) >> core.SectionShift
	maxChunkX := (position.X + propagatedSkyDirtyRadius) >> core.SectionShift
	minChunkZ := (position.Z - propagatedSkyDirtyRadius) >> core.SectionShift
	maxChunkZ := (position.Z + propagatedSkyDirtyRadius) >> core.SectionShift
	// 只遍历上述最多 3×3 个已加载 chunk 与 lowSection..highSection。
}
```

每个 `BlockChange` 先调用一次 `position.Y±16`；若列顶改变，再调用一次 `min(before,after)-16` 到 `max(before,after)+16`。dirty map 自然合并重叠项，不再额外保留 ±1 helper。

- [ ] **Step 3: 验证 stale、加载/遗忘与 benchmark**

Run:

```bash
gofmt -w internal/client/mirror.go internal/client/skylight_test.go
zsh -ic 'go test ./internal/client -race -count=1'
zsh -ic 'go test ./internal/client -run "^$" -bench "SkyDirtyRange|MesherSkySnapshot" -benchmem -count=3'
gofmt -l internal/client
```

Expected: `TestMesherDiscardsStaleSkyLightAfterRoofChange` 仍证明旧结果被丢弃；benchmark 有固定输出且无队列上限改动。

- [ ] **Step 4: 提交**

```bash
git add internal/client/mirror.go internal/client/skylight_test.go
git commit -m "feat: 扩大传播天空光失效范围"
```

### Task 5: 权威方块变化的动态传播闭环

**Files:**
- Modify: `internal/server/daylight_integration_test.go:15-210`

**Interfaces:**
- Consumes: Memory transport、Mirror、Task 3 的 MeshSection、Task 4 的 dirty 规则。
- Produces: 权威封洞/开洞使 15→14→1→0 梯度消失和恢复的端到端证据。

- [ ] **Step 1: 扩展固定屋顶夹具与断言**

保持一格天窗，增加以下位置：

```go
underHole := core.BlockPos{X: 0, Y: groundTop, Z: 0}
oneStep := core.BlockPos{X: 1, Y: groundTop, Z: 0}
lastLit := core.BlockPos{X: 14, Y: groundTop, Z: 0}
firstDark := core.BlockPos{X: 15, Y: groundTop, Z: 0}
```

初始断言依次为 `15,14,1,0`；权威放置补洞后四处全为 `0`；权威挖开后恢复 `15,14,1,0`。最终 authoritative/mirror hash 与 revision 仍相等。

- [ ] **Step 2: 运行测试并用变异证明断言有效**

Run: `zsh -ic 'go test ./internal/server -run "^TestAuthoritativeRoofChangeDrivesMirrorSkyLight$" -count=1'`

Expected: Task 3–4 已完成时测试直接通过。随后暂时把 `MeshSection` 的亮度读取改回 `n.SkyLight(...)`，确认 `oneStep=14` 断言失败；立即恢复并重跑为 `ok`。不得提交变异。

- [ ] **Step 3: 用显式 scratch 更新测试 helper**

`topFaceSkyLight` 调用：

```go
scratch := mesh.NewSkyLightScratch()
for _, quad := range mesh.MeshSection(neighborhood, assets.NewRegistry(), scratch) {
	// 既有命中逻辑保持不变。
}
```

不新增服务端生产代码；若测试不绿，修复 Task 2–4 的共享根因。

- [ ] **Step 4: 验证并提交**

```bash
gofmt -w internal/server/daylight_integration_test.go
zsh -ic 'go test ./internal/server ./internal/client ./internal/mesh -race -count=1'
git add internal/server/daylight_integration_test.go
git commit -m "test: 覆盖权威天空光传播闭环"
```

### Task 6: 增加 skylight-tunnel 无窗口视觉场景

**Files:**
- Modify: `cmd/mcgo/capture.go:42-235`
- Modify: `cmd/mcgo/capture_test.go`
- Create: `cmd/mcgo/testdata/golden/skylight-tunnel.png`
- Modify: `cmd/mcgo/testdata/golden/{terrain-noon,hud-hotbar-health,avatar-nametag}.png` only if propagation legitimately changes pixels after human review

**Interfaces:**
- Produces: `captureScene.Prepare func(*application) error`，在既有 WarmupFrames 与最后一次服务端消息 drain 之后、`Apply` 之前装入镜像夹具。
- Produces: 表驱动场景 `skylight-tunnel`；全部四张图继续走同一 renderer/capture 路径。

- [ ] **Step 1: 写夹具和收敛失败测试**

在 `capture_test.go` 测试 `prepareSkylightTunnel`：九个 snapshot 都经 `Mirror.Apply` 成功；中心 tunnel 的入口/屋顶/地面方块值正确；返回的 dirty key 全部交给 Mesher。另写纯函数 `captureSettled` 的表驱动测试：仅当 `DirtySections/QueuedJobs/InFlightJobs/ReadyResults` 与 `PendingUploads` 全为 0 才返回 true。

- [ ] **Step 2: 运行红灯**

Run: `zsh -ic 'go test ./cmd/mcgo -run "Test(CaptureSkylightTunnel|CaptureSettled)" -count=1'`

Expected: helper 尚不存在，测试编译失败；先加最小签名桩后确认断言红灯。

- [ ] **Step 3: 在 Prepare 阶段经正常 Mirror 边界装入夹具**

给 `captureScene` 增加可选 `Prepare`。`captureOne` 保留现有 WarmupFrames，随后完成最后一次 `drainServerMessages`，再依次调用 `Prepare` 与 `Apply`；之后只走现有 `renderFrame` 收敛循环，不再 drain 服务端消息，避免权威世界消息覆盖固定夹具。夹具先给 `[-1,1]×[-1,1]` 九个 chunk 应用 revision 1 的全空气 `SectionSingle` snapshot，再按 chunk 应用 revision 2、严格按 BlockIndex 排序的 `BlockChanges`，构造宽 5、高 4、长 20 的石质 tunnel：地面、屋顶、两侧墙固定，入口露天，深处超过 15 格。

每次 `Mirror.Apply` 后必须调用 `app.mesher.MarkDirty(update.Dirty...)`；不得直接写 Mirror 字段。WarmupFrames 后调用：

```go
func captureSettled(stats client.MesherStats, pending int) bool {
	return stats.DirtySections == 0 && stats.QueuedJobs == 0 &&
		stats.InFlightJobs == 0 && stats.ReadyResults == 0 && pending == 0
}
```

未收敛返回包含 stats/pending 的错误，不抓帧。

- [ ] **Step 4: 固定场景自身全部呈现状态**

场景追加在列表末尾，`Apply` 固定 `worldTimeTicks=6000`、相机位置/朝向，清空 inventory，并通过合法 `RemotePlayerDespawn` 清除上一场景玩家。不得依赖前一场景零值。

- [ ] **Step 5: 先产出候选图并请求人工确认**

Run:

```bash
gofmt -w cmd/mcgo/capture.go cmd/mcgo/capture_test.go
zsh -ic 'go test ./cmd/mcgo -race -count=1'
zsh -ic 'go run ./cmd/mcgo --capture /private/tmp/m4m-visual-candidate'
```

首次运行允许因新 golden 缺失或旧图合法变化而返回非零，但 `/private/tmp/m4m-visual-candidate/` MUST 有四张场景图。用 `view_image` 逐张检查并展示给用户；必须确认 tunnel 从入口到深处逐级变暗、无穿墙漏光、原三场景无非预期回归。没有用户确认不得执行 `--update-golden`。

- [ ] **Step 6: 冻结并复验全部 golden**

用户确认后运行：

```bash
zsh -ic 'go run ./cmd/mcgo --capture /private/tmp/m4m-visual-update --update-golden'
zsh -ic 'go run ./cmd/mcgo --capture /private/tmp/m4m-visual-check-1'
zsh -ic 'go run ./cmd/mcgo --capture /private/tmp/m4m-visual-check-2'
```

Expected: 两次 check 均通过现有 `MaxChannelDelta=2`、`MaxDiffPixelRatio=0.0001`；阈值字节不变。逐张再次查看仓库内四张 golden。

- [ ] **Step 7: 提交**

```bash
git add cmd/mcgo/capture.go cmd/mcgo/capture_test.go cmd/mcgo/testdata/golden
git commit -m "test: 增加天空光传播视觉场景"
```

### Task 7: 把性能契约升级到 scenario v14

**Files:**
- Modify: `cmd/mcgo/benchmark.go:27-100`
- Modify: `cmd/mcgo/benchmark_v5_test.go:18`
- Modify: `cmd/mcgo/benchmark_v6_test.go:62`
- Modify: `cmd/perfcheck/main.go:15-80`
- Modify: `cmd/perfcheck/main_test.go:235-335,950-1110`

**Interfaces:**
- Produces: producer `scenarioVersion = 14`。
- Produces: `perfcheck --allow-scenario-upgrade 13:14` 是唯一允许迁移；v6–v13 同场景仍可读/比。

- [ ] **Step 1: 先把测试推进到 v14**

新增 `completeV14ComparableReport`，更新 scenario matrix：

```go
func completeV14ComparableReport(transport string) client.PerfReport {
	report := completeV13ComparableReport(transport)
	report.ScenarioVersion = 14
	return report
}
```

矩阵 MUST 接受 `{13,14,"13:14"}`，拒绝无授权、反向、`12:13`、`12:14`、`11:14` 和同场景附带授权；历史自比较列表扩到 v14。新增 v14 同场景/cross transport/绝对门禁测试，结构与现有 v13 测试相同但不删除历史 v13 覆盖。

- [ ] **Step 2: 运行红灯**

Run: `zsh -ic 'go test ./cmd/mcgo ./cmd/perfcheck -run "ScenarioVersion|ScenarioUpgrade|V14|HistoricalScenarios" -count=1'`

Expected: producer 仍为 13，`13:14` 被拒绝。

- [ ] **Step 3: 最小更新 producer 与比较器**

把 `scenarioVersion` 改为 `14`；`gpuCompletionMinSamples` 逻辑不变，只更新注释为 v12–v14。比较器的唯一迁移改为：

```go
allowedScenarioUpgrade := baseline.ScenarioVersion == 13 &&
	current.ScenarioVersion == 14 && allowScenarioUpgrade == "13:14"
```

CLI help/error、成功消息测试与完整性测试同步 v14。不得修改任何绝对阈值、稳定指标列表或报告 schema。

- [ ] **Step 4: 验证并提交**

```bash
gofmt -w cmd/mcgo/benchmark.go cmd/mcgo/benchmark_v5_test.go cmd/mcgo/benchmark_v6_test.go cmd/perfcheck/main.go cmd/perfcheck/main_test.go
zsh -ic 'go test ./cmd/mcgo ./cmd/perfcheck -race -count=1'
zsh -ic 'go vet ./cmd/mcgo ./cmd/perfcheck'
gofmt -l cmd/mcgo cmd/perfcheck
git add cmd/mcgo/benchmark.go cmd/mcgo/benchmark_v5_test.go cmd/mcgo/benchmark_v6_test.go cmd/perfcheck/main.go cmd/perfcheck/main_test.go
git commit -m "perf: 升级天空光场景到 v14"
```

### Task 8: 冻结候选并建立一次性 M5 v14 基线

**Files:**
- Modify: `docs/notes/perf-baseline-m5.json` only after both formal runs pass
- Modify: `docs/notes/perf-baseline.md`
- Modify: `openspec/changes/m4m-propagated-skylight/tasks.md`

**Interfaces:**
- Consumes: Tasks 1–7 的干净精确 HEAD。
- Produces: 通过 `13:14` 迁移和 Memory→TCP parity 的 M5 v14 Memory 精确字节；M2 不变。

- [ ] **Step 1: 完整冻结门禁**

Run:

```bash
zsh -ic 'go test ./internal/world ./internal/mesh ./internal/client ./internal/server ./internal/render ./cmd/mcgo ./cmd/perfcheck -race -count=1'
zsh -ic 'go test ./internal/archcheck -count=1'
zsh -ic 'go test ./... -race'
zsh -ic 'go vet ./...'
gofmt -l .
git diff --check
openspec validate --all --strict --no-interactive
zsh -ic 'go run ./cmd/mcgo --capture /private/tmp/m4m-final-visual-check'
```

Expected: 全部 exit 0；`gofmt -l .` 无输出；无前台窗口。任何失败先修根因并形成新提交，原候选不得进入正式链。

- [ ] **Step 2: 记录不可变身份与基线哈希**

```bash
git status --short --branch
git rev-parse HEAD
shasum -a 256 docs/notes/perf-baseline.json docs/notes/perf-baseline-m5.json
zsh -ic 'go version'
sw_vers
system_profiler SPHardwareDataType
pmset -g batt
uptime
pgrep -fl 'mcgo|perfcheck'
```

Expected: tracked 工作树干净；M2 哈希为当前接受值 `b2d04877004c...520cb7f93`；M5 为 v13；无遗留 producer。

- [ ] **Step 3: 建立全新正式路径并请求授权**

在一个保持到 Step 5 结束的专用终端会话中执行：

```bash
export M4M_FORMAL_DIR="$(mktemp -d /private/tmp/mcgo-m4m-v14.XXXXXX)"
export M4M_MEMORY_PATH="$M4M_FORMAL_DIR/memory-v14.json"
export M4M_TCP_PATH="$M4M_FORMAL_DIR/tcp-v14.json"
printf 'formal_dir=%s\nmemory=%s\ntcp=%s\n' "$M4M_FORMAL_DIR" "$M4M_MEMORY_PATH" "$M4M_TCP_PATH"
test ! -e "$M4M_MEMORY_PATH"
test ! -e "$M4M_TCP_PATH"
```

记录打印出的完整路径；连续两次只读静稳快照满足既有规则后，向用户报告精确 HEAD、M2/M5 哈希、硬件/OS/Go、供电、负载、进程和两个路径，并请求一次性 Memory→迁移→TCP→同场景比较授权。未获授权不得继续。

- [ ] **Step 4: 只执行一次 Memory producer 与迁移门禁**

授权后在同一终端会话执行：

```bash
TERM=xterm-256color zsh -ic 'go run ./cmd/mcgo --benchmark --benchmark-transport memory --perf-output "$M4M_MEMORY_PATH"'
zsh -ic 'go run ./cmd/perfcheck --baseline docs/notes/perf-baseline-m5.json --current "$M4M_MEMORY_PATH" --max-regression 0.20 --allow-scenario-upgrade 13:14'
```

任一步失败立即停止，不执行 TCP、不重跑 Memory。

- [ ] **Step 5: Memory 通过后只执行一次 TCP 与 parity**

```bash
TERM=xterm-256color zsh -ic 'go run ./cmd/mcgo --benchmark --benchmark-transport tcp --perf-output "$M4M_TCP_PATH"'
zsh -ic 'go run ./cmd/perfcheck --baseline "$M4M_MEMORY_PATH" --current "$M4M_TCP_PATH" --max-regression 0.20'
```

失败立即停止；不得重跑或筛选。

- [ ] **Step 6: 两步都通过后提升 Memory 精确字节**

核对两份 JSON 的 `scenario_version=14`、transport、同一 git commit/hardware/OS/Go/framebuffer，计算 SHA-256。然后把 Memory JSON 精确复制到 `docs/notes/perf-baseline-m5.json`，更新 `docs/notes/perf-baseline.md`：HEAD、命令、正式路径、哈希、静稳证据、v13→v14、指标摘要、失败即停规则与回退说明。再次验证 M2 哈希未变。

- [ ] **Step 7: 提交正式基线**

```bash
git add docs/notes/perf-baseline-m5.json docs/notes/perf-baseline.md openspec/changes/m4m-propagated-skylight/tasks.md
git commit -m "perf: 接受 M5 scenario v14 基线"
```

### Task 9: 更新用户文档并同步主规格

**Files:**
- Modify: `README.md`
- Modify: `AGENTS.md`
- Modify: `CLAUDE.md`
- Modify: `openspec/config.yaml`
- Modify: `openspec/specs/authoritative-daylight/spec.md`
- Modify: `openspec/specs/visual-verification/spec.md`
- Modify: `openspec/specs/bounded-benchmark-workload/spec.md`
- Modify: `openspec/specs/hardware-performance-baselines/spec.md`
- Modify: `openspec/changes/m4m-propagated-skylight/tasks.md`

**Interfaces:**
- Consumes: 已通过 v14 正式链的稳定行为。
- Produces: 当前基线 M4M 的中文说明和与 delta 一致的主规格。

- [ ] **Step 1: 更新 README 与项目上下文**

README MUST 把“仅直射天空光”改为“直射 15、横向逐格衰减到 1、下一格为 0、未知邻区暗”；视觉场景列表加 `skylight-tunnel`；benchmark 改 v14、唯一 `13:14`、M5 v14/M2 v6 和回退到 v13 的要求。协议/存档版本保持原值。

AGENTS/CLAUDE 第一段和 `openspec/config.yaml` 当前基线改为 M4M，加入客户端派生天空光传播与 scenario v14；两个指南文件最终必须逐字一致。

- [ ] **Step 2: 使用 openspec-sync-specs skill 同步四份 delta**

同步后逐字核对：旧“屋顶下总天空光必为 0”和 dirty `96` 已被替换，不得与新传播契约并存；历史 scenario v13 仍可读，但唯一迁移是 `13:14`；M5 v14 与 M2 不变写入硬件规格。

- [ ] **Step 3: 验证文档一致性**

```bash
cmp AGENTS.md CLAUDE.md
rg -n "M4M|scenario v14|13:14|skylight-tunnel|216|协议 v13|schema v5|schema v6" AGENTS.md CLAUDE.md README.md openspec/config.yaml openspec/specs
rg -n "只实现直射天空光|dirty.*96|只允许.*12:13" AGENTS.md CLAUDE.md README.md openspec/config.yaml openspec/specs
openspec validate --all --strict --no-interactive
git diff --check
```

Expected: `cmp` exit 0；第二个 `rg` 对当前契约无命中（历史归档目录不在搜索范围）。

- [ ] **Step 4: 提交同步结果**

```bash
git add README.md AGENTS.md CLAUDE.md openspec/config.yaml openspec/specs openspec/changes/m4m-propagated-skylight/tasks.md
git commit -m "docs: 同步 M4M 天空光传播规格"
```

### Task 10: 最终审查、归档与交付

**Files:**
- Move: `openspec/changes/m4m-propagated-skylight/` → `openspec/changes/archive/2026-08-08-m4m-propagated-skylight/`

**Interfaces:**
- Consumes: 任务 1–9 的全部提交与主规格同步结果。
- Produces: 无 active M4M change、可评审的完整分支和任务报告。

- [ ] **Step 1: 逐条覆盖审查**

把设计文档 §2、§4–§9 的每一项映射到至少一个测试名和实现文件；核对所有 OpenSpec task 已勾选，除归档步骤外不得有遗漏。检查分支 diff 只含 M4M 的规划、实现、测试、文档与正式基线文件。

- [ ] **Step 2: 最终门禁**

```bash
zsh -ic 'go test ./... -race'
zsh -ic 'go vet ./...'
zsh -ic 'go test ./internal/archcheck -count=1'
gofmt -l .
git diff --check
openspec validate --all --strict --no-interactive
zsh -ic 'go run ./cmd/mcgo --capture /private/tmp/m4m-archive-visual-check'
```

Expected: 全部通过，无前台窗口；M2 baseline 哈希仍为冻结值。

- [ ] **Step 3: 使用 openspec-archive-change skill 归档**

归档日期固定为 `2026-08-08`。归档后运行：

```bash
openspec list --json
openspec validate --all --strict --no-interactive
test ! -e openspec/changes/m4m-propagated-skylight
test -e openspec/changes/archive/2026-08-08-m4m-propagated-skylight/.openspec.yaml
git diff --check
```

Expected: active 列表无 M4M，strict 全绿，归档元数据存在。

- [ ] **Step 4: 提交归档**

```bash
git add openspec/changes openspec/specs
git commit -m "docs: 归档 M4M 天空光传播"
```

- [ ] **Step 5: 最终状态报告**

报告分支、完整提交列表、全仓 race/vet/archcheck/gofmt/OpenSpec/visual 结果、M5 v14 与 M2 哈希、协议/存档未变、未实现的方块光/透明方块，并确认共享工作树的用户日志未被触碰。随后再决定推送和 PR，不在本任务中自动合并。
