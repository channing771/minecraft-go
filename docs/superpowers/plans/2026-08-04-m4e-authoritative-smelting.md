# M4E 权威熔炼 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 先把 M4D 与两个已完成性能变更完整归档到主规格，再交付“采矿石、合成并放置熔炉、用煤把粗铁熔成铁锭、合成并放置铁块”的权威、持久、多人共享闭环。

**Architecture:** 熔炉是 `world.Chunk` 内固定 32 槽状态，由 `sim.Engine` 的单写者 tick 串行推进；客户端只发送打开、整堆移动与关闭意图，并显示服务端完整状态。实现复用现有区块 revision、掉落物槽、36 格 Inventory、Memory/TCP codec、session outbox 与 HotbarRenderer，不引入通用容器框架、全局熔炉 map、注册器或每熔炉 goroutine。

**Tech Stack:** Go 1.26.0（仅通过 GVM）、标准库、现有 WebGPU/gfx 封装、OpenSpec 1.7、现有 Memory/TCP 传输、现有 headless benchmark/perfcheck、Git/GitHub。

## Global Constraints

- 所有 Go 命令 MUST 通过 `zsh -ic 'gvm use go1.26.0 >/dev/null && ...'` 执行；不得下载或安装另一份 Go。
- 自动测试与正式 benchmark MUST 使用无窗口/headless 路径，不启动或聚焦前台游戏窗口。
- 开始每个任务前读取 `openspec/config.yaml`；进入 M4E 实现后还要依次读取 `m4e-authoritative-smelting` 的 `proposal.md`、delta specs、`design.md` 与 `tasks.md`。
- 严格按 RED → GREEN → REFACTOR；只在相应验证通过后勾选 OpenSpec task、提交本组并自动进入下一组。
- 保留用户的 `midscene_run/` 与任何无关改动；每次暂存使用精确文件路径，不执行宽范围清理、reset 或强制推送。
- 不放宽现有正确性、容量、race、性能绝对门禁、20% 回归阈值或慢客户端断开策略。
- 稳定 ID 只能在末尾追加：协议升级为 v7，区块 schema 升级为 v4，benchmark scenario 升级为 v9；玩家 schema 保持 v3。
- 方块、掉落物与熔炉状态必须由同一 chunk snapshot 原子保存；存储失败不得留下部分新状态。
- 新增开发者文档、规格、任务说明与测试说明使用中文；Go 标识符、wire magic 与固定技术术语保留英文。
- Ponytail：复用已有 switch、定长数组、scratch、renderer 与发布链；不增加单实现 interface、factory、DSL、配置项或“为以后准备”的抽象。

---

## 文件与接口地图

### 阶段 A：主线规划收尾

- Move/Archive: `openspec/changes/m4d-authoritative-crafting/**`
- Move/Archive: `openspec/changes/stabilize-remote-gpu-completion-gate/**`
- Move/Archive: `openspec/changes/stabilize-interest-diff-regression-gate/**`
- Modify: `openspec/specs/bounded-benchmark-workload/spec.md`
- Modify: `openspec/specs/hardware-performance-baselines/spec.md`
- Existing commit to preserve: `ec8fc44`（M4D 归档）

### 阶段 B：M4E 规划与实现

- Create: `openspec/changes/m4e-authoritative-smelting/{proposal.md,design.md,tasks.md}`
- Create: `openspec/changes/m4e-authoritative-smelting/specs/deterministic-ore-generation/spec.md`
- Create: `openspec/changes/m4e-authoritative-smelting/specs/authoritative-furnaces/spec.md`
- Modify delta: `openspec/changes/m4e-authoritative-smelting/specs/authoritative-inventory/spec.md`
- Modify delta: `openspec/changes/m4e-authoritative-smelting/specs/bounded-benchmark-workload/spec.md`
- Create: `internal/core/furnace.go`; Modify: `internal/core/{block.go,item.go,inventory.go,recipe.go}` and focused tests
- Modify: `internal/world/{chunk.go,drop.go}`; Create: `internal/world/furnace.go` and tests
- Modify: `internal/worldgen/generator.go`, golden/test files
- Modify: `internal/assets/blocks.go`, `internal/assets/procedural.go`, focused tests
- Modify: `internal/render/{hotbar.go,drop.go}` and focused tests
- Modify: `internal/storage/{migration.go,chunk_codec.go}` and chunk migration/golden/fuzz tests
- Modify: `internal/network/{packet.go,message.go,registry.go,codec.go}` and golden/fuzz tests
- Create: `internal/client/furnace.go`; Modify: `internal/client/input.go` and tests
- Create: `internal/sim/furnace.go`; Modify: `internal/sim/{command.go,engine.go,drop.go,player.go}` and tests
- Modify: `internal/server/{session.go,publication.go}`; Create: `internal/server/furnace_publication.go` and integration tests
- Modify: `cmd/mcgo/{app.go,main.go,benchmark.go}` and headless tests
- Modify: `cmd/perfcheck/{main.go,main_test.go}` for explicit `8:9` upgrade handling
- Modify: `README.md`, `docs/notes/lan-server.md`, `docs/notes/perf-baseline.md`
- Replace only after both formal runs pass: `docs/notes/perf-baseline-m5.json`

### Stable interfaces to add

`FurnaceRef` 是设计中“熔炉 ID”的 Go 表达；使用 `Ref` 后缀是因为 `core.FurnaceID` 已按稳定资源命名保留给熔炉方块 ID，不能在同一包重名。

```go
// internal/core/furnace.go
type FurnaceRef struct {
	Dimension  DimensionID
	Chunk      ChunkPos
	Slot       uint8
	Generation uint32
}

// internal/world/furnace.go
type FurnaceSlot struct {
	Generation    uint32
	Active        bool
	BlockIndex    uint32
	Input         core.ItemStack
	Fuel          core.ItemStack
	Output        core.ItemStack
	ProgressTicks uint16
	BurnTicks     uint16
}

type FurnaceState struct {
	Furnace       core.FurnaceRef
	Input         core.ItemStack
	Fuel          core.ItemStack
	Output        core.ItemStack
	ProgressTicks uint16
	BurnTicks     uint16
}
```

```go
// internal/network/message.go
type OpenFurnace struct { Sequence uint64; Yaw, Pitch float32 }
type MoveFurnaceStack struct { Sequence uint64; Furnace core.FurnaceRef; From, To uint8 }
type CloseFurnace struct { Sequence uint64 }
type FurnaceState struct { Furnace core.FurnaceRef; Input, Fuel, Output core.ItemStack; ProgressTicks, BurnTicks uint16 }
type FurnaceClosed struct { Furnace core.FurnaceRef }
```

```go
// internal/sim/command.go
type FurnaceUpdate struct { Session SessionID; State world.FurnaceState }
type FurnaceClose struct { Session SessionID; Furnace core.FurnaceRef }
// TickResult 追加 Furnaces []FurnaceUpdate 与 ClosedFurnaces []FurnaceClose。
```

---

## 阶段 A：归档已完成变更

### Task 1: 把 M4D 归档提交带回最新主线

**Files:**
- Read: `docs/superpowers/specs/2026-08-04-m4e-authoritative-smelting-design.md`
- Move via existing commit: `openspec/changes/m4d-authoritative-crafting/**`
- Preserve: `midscene_run/`

**Interfaces:**
- Consumes: 最新 `origin/main` 与已验证提交 `ec8fc44`。
- Produces: 规划分支同时包含 M4E 设计/计划和 M4D 归档，不改运行时代码。

- [ ] **Step 1: 用隔离工作区规则核对分支与所有权**

调用 `superpowers:using-git-worktrees`。若当前 `codex/m4e-authoritative-smelting-planning` 已是隔离且干净的任务分支，可直接复用；否则从最新 `origin/main` 创建 `codex/archive-m4d-and-regression-changes` worktree。执行：

```bash
git status --short --branch
git diff --name-only
git diff --cached --name-only
git fetch origin main
git log -1 --oneline origin/main
```

Expected: 除 `midscene_run/` 外没有未知工作树修改，没有 staged 文件；不得覆盖未知改动。

- [ ] **Step 2: 合入最新主线并检查 M4D 归档是否缺失**

```bash
git merge --no-edit origin/main
git merge-base --is-ancestor ec8fc44 HEAD
test -d openspec/changes/archive/2026-08-04-m4d-authoritative-crafting
```

Expected on the recorded starting point: merge 成功；后两条至少一条失败，证明 M4D 归档尚未进入当前分支。若两条都成功，跳过 cherry-pick；不得制造重复归档。

- [ ] **Step 3: 带入既有 M4D 归档提交**

```bash
git cherry-pick ec8fc44
openspec validate --all --strict --no-interactive
git diff --check
git status --short --branch
```

Expected: cherry-pick 成功；M4D 只存在于 `openspec/changes/archive/2026-08-04-m4d-authoritative-crafting/`，主规格包含权威合成行为，严格校验通过；`midscene_run/` 仍未跟踪。该 cherry-pick 自身就是本任务提交，然后自动进入 Task 2。

### Task 2: 归档 remote GPU completion 门禁

**Files:**
- Move: `openspec/changes/stabilize-remote-gpu-completion-gate/**`
- Modify: `openspec/specs/bounded-benchmark-workload/spec.md`
- Modify: `openspec/specs/hardware-performance-baselines/spec.md`

**Interfaces:**
- Consumes: 已完成且 `openspec status` 为 complete 的 remote GPU change。
- Produces: 主规格沉淀 headless GPU 完成边界及基线门禁，active change 消失。

- [ ] **Step 1: 读取并核对产物与任务完成度**

```bash
openspec status --change stabilize-remote-gpu-completion-gate --json
rg -n '^[-*] \[ \]' openspec/changes/stabilize-remote-gpu-completion-gate/tasks.md
openspec validate --all --strict --no-interactive
```

Expected: status `isComplete: true`；未勾选搜索无输出；全量严格校验通过。若 delta 与当前代码/测试不一致，停止并修正文档根因后再归档。

- [ ] **Step 2: 执行归档并核对主规格差异**

```bash
openspec archive stabilize-remote-gpu-completion-gate --yes
openspec list --json
git diff -- openspec/specs openspec/changes
openspec validate --all --strict --no-interactive
git diff --check
```

Expected: active 列表不再包含该 change；归档目录存在；只合入其 delta 要求，没有覆盖 M4D 或 interest change。

- [ ] **Step 3: 精确提交**

```bash
git add -A -- openspec/specs/bounded-benchmark-workload/spec.md openspec/specs/hardware-performance-baselines/spec.md openspec/changes/stabilize-remote-gpu-completion-gate openspec/changes/archive/2026-08-04-stabilize-remote-gpu-completion-gate
git diff --cached --check
git commit -m "docs: 归档 GPU 完成回归门禁"
```

Expected: 提交成功，`midscene_run/` 未暂存；自动进入 Task 3。

### Task 3: 归档 interest 门禁、合并并重新同步 main

**Files:**
- Move: `openspec/changes/stabilize-interest-diff-regression-gate/**`
- Modify: `openspec/specs/hardware-performance-baselines/spec.md`
- Preserve: all M4E planning docs already on branch

**Interfaces:**
- Consumes: Task 2 已归档后的主规格。
- Produces: 无已完成但未归档的 active change；远端 main 包含 M4E 规划基础。

- [ ] **Step 1: 核对并按依赖顺序归档 interest change**

```bash
openspec status --change stabilize-interest-diff-regression-gate --json
rg -n '^[-*] \[ \]' openspec/changes/stabilize-interest-diff-regression-gate/tasks.md
openspec archive stabilize-interest-diff-regression-gate --yes
openspec list --json
openspec validate --all --strict --no-interactive
git diff --check
```

Expected: status 原先 complete；归档后 active 列表为空，且 `hardware-performance-baselines` 同时保留 GPU completion 与 interest 稳定比较契约。

- [ ] **Step 2: 提交 interest 归档**

```bash
git add -A -- openspec/specs/hardware-performance-baselines/spec.md openspec/changes/stabilize-interest-diff-regression-gate openspec/changes/archive/2026-08-04-stabilize-interest-diff-regression-gate
git diff --cached --check
git commit -m "docs: 归档 interest 差分回归门禁"
```

Expected: 提交成功；规划文档若已在更早提交中则不重复暂存。

- [ ] **Step 3: 最终文档门禁并推送评审**

```bash
openspec validate --all --strict --no-interactive
git diff --check
git status --short --branch
git log --oneline origin/main..HEAD
git push -u origin HEAD
```

Expected: 校验通过；只有 `midscene_run/` 未跟踪；推送成功。创建 ready PR，确认 checks 通过后使用普通 merge，不 squash、不 force push。

- [ ] **Step 4: 合并后同步 main 并建立 M4E 实现分支**

```bash
git switch main
git pull --ff-only origin main
openspec list --json
git switch -c codex/m4e-authoritative-smelting
```

Expected: main 与 origin/main 相同；active change 为空；新实现分支从合并后的主线创建。自动进入 Task 4。

---

## 阶段 B：M4E 权威熔炼

### Task 4: 创建并严格校验 M4E OpenSpec change

**Files:**
- Create: `openspec/changes/m4e-authoritative-smelting/proposal.md`
- Create: `openspec/changes/m4e-authoritative-smelting/design.md`
- Create: `openspec/changes/m4e-authoritative-smelting/tasks.md`
- Create: `openspec/changes/m4e-authoritative-smelting/specs/deterministic-ore-generation/spec.md`
- Create: `openspec/changes/m4e-authoritative-smelting/specs/authoritative-furnaces/spec.md`
- Create: `openspec/changes/m4e-authoritative-smelting/specs/authoritative-inventory/spec.md`
- Create: `openspec/changes/m4e-authoritative-smelting/specs/bounded-benchmark-workload/spec.md`

**Interfaces:**
- Consumes: confirmed design document and archived main specs.
- Produces: 可逐项勾选的唯一 active change `m4e-authoritative-smelting`。

- [ ] **Step 1: 调用 OpenSpec propose workflow**

调用 `openspec-propose`，以确认设计文档为输入。proposal 明确目标、非目标、协议 v7、chunk v4、player v3、scenario v9、无窗口验证和回退；design 保持 `world.Chunk` 固定槽 + `sim.Engine` 单写者；tasks 与本计划 Task 5–15 一一对应。

- [ ] **Step 2: 写可观察 delta specs**

每条 Requirement 使用 SHALL/MUST，并至少包含一个 Given/When/Then Scenario。必须覆盖：

- 固定种子、三维坐标、Y 上限、只替换石头以及 `BaseBlockAt`/`GenerateChunk` 一致；
- 最多 32 熔炉、generation 防 ABA、200/1600 tick、暂停不浪费、活动半径 2、多人共享；
- 36–38 槽规则、输出只取、跨容器原子性、批量掉落容量失败；
- v7 固定消息、旧版本拒绝、v4/v1-v3 迁移、重启暂停恢复；
- scenario v9 显式升级、M2 不变、M5 Memory/TCP 一次性链。

- [ ] **Step 3: 严格校验并提交**

```bash
openspec status --change m4e-authoritative-smelting --json
openspec validate --all --strict --no-interactive
rg -n 'T[B]D|T[O]DO|待定|稍后|同上|类[似]' openspec/changes/m4e-authoritative-smelting
git diff --check
git add openspec/changes/m4e-authoritative-smelting
git diff --cached --check
git commit -m "docs: 规定 M4E 权威熔炼"
```

Expected: 四类产物完整、无占位符、严格校验通过；自动进入 Task 5。

### Task 5: 追加稳定资源、固定配方、矿石生成与程序化外观

**Files:**
- Modify: `internal/core/block.go`
- Modify: `internal/core/item.go`
- Modify: `internal/core/inventory.go`
- Modify: `internal/core/recipe.go`
- Modify/Create tests: `internal/core/{item_test.go,inventory_test.go,recipe_test.go}`
- Modify: `internal/worldgen/generator.go`
- Modify: `internal/worldgen/generator_test.go`
- Modify: `internal/worldgen/testdata/golden_seed42.txt`
- Modify: `internal/assets/{blocks.go,procedural.go,blocks_test.go}`
- Modify: `internal/render/{hotbar.go,drop.go,hotbar_test.go,drop_test.go}`
- Modify: `openspec/changes/m4e-authoritative-smelting/tasks.md`

**Interfaces:**
- Produces stable IDs: blocks `CoalOreID=7`, `IronOreID=8`, `FurnaceID=9`, `IronBlockID=10`; items `ItemCoal=5`, `ItemRawIron=6`, `ItemIronIngot=7`, `ItemFurnace=8`, `ItemIronBlock=9`; recipes `RecipeFurnace=2`, `RecipeIronBlock=3`.
- Produces: `core.RegisteredItem(ItemID) bool`; `ItemPlacement` remains the only placement mapping.

- [ ] **Step 1: 写 core RED tests**

先锁定编号不可重排、所有新物品合法、煤/粗铁/铁锭不可放置、矿石掉落、熔炉/铁块放置与掉落，以及固定配方：

```go
func TestM4EResourceIDsAreStable(t *testing.T) {
	if core.CoalOreID != 7 || core.IronOreID != 8 || core.FurnaceID != 9 || core.IronBlockID != 10 {
		t.Fatal("M4E 方块 ID 漂移")
	}
	if core.ItemCoal != 5 || core.ItemRawIron != 6 || core.ItemIronIngot != 7 || core.ItemFurnace != 8 || core.ItemIronBlock != 9 {
		t.Fatal("M4E 物品 ID 漂移")
	}
}
```

补充 `Inventory.Craft` 的 8 石头→1 熔炉、9 铁锭→1 铁块、最低索引扣料、产物无容量原子失败测试。

- [ ] **Step 2: 运行 core RED**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/core -run "M4E|RegisteredItem|FurnaceRecipe|IronBlockRecipe" -count=1'
```

Expected: 因新 ID/函数/配方缺失而编译或断言失败。

- [ ] **Step 3: 写最小 core 实现**

在现有 enum 末尾追加 ID；新增单个 switch：

```go
func RegisteredItem(item ItemID) bool {
	return item >= ItemStone && item <= ItemIronBlock
}
```

让 `ItemStack.Valid`、`Hotbar.Add` 使用 `RegisteredItem`；`BlockDrop`、`ItemPlacement` 与 `Recipe` 仍使用现有 switch，只追加确切 case，不增加注册表。

- [ ] **Step 4: 写 worldgen RED tests**

覆盖同 seed 同坐标确定性、不同 seed 至少一个样本不同、煤只在 `Y<96`、铁只在 `Y<48`、矿石只替换 stone、铁碰撞优先、负坐标、`BaseBlockAt` 与整区块逐点一致、固定样本大致命中率与 golden 漂移。

实现前运行：

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/worldgen -run "Ore|BaseBlockAtMatchesGeneratedChunk|GenerateChunkGolden" -count=1'
```

Expected: 新矿石断言失败。

- [ ] **Step 5: 共享一个纯矿石判断并更新 golden**

在 `generator.go` 只增加三维整数 hash 与一处包装：

```go
func (g *Generator) generatedBlockAt(position core.BlockPos, height int32) core.BlockID {
	base := terrainBlockAt(position.Y, height)
	if base != core.StoneID { return base }
	if position.Y < 48 && oreHash(g.seed, position, ironSalt)%4096 == 0 { return core.IronOreID }
	if position.Y < 96 && oreHash(g.seed, position, coalSalt)%2048 == 0 { return core.CoalOreID }
	return base
}
```

`BaseBlockAt` 与 `GenerateChunk` 必须都调用它。先运行非 golden 测试，再用现有 `-update` 机制重写一次 golden 并复跑；不手工猜哈希。

- [ ] **Step 6: 用现有外观路径补齐资源**

为四个方块追加程序化 texture layer；`hotbarItemColor` 与 `itemDropColor` 支持全部注册物品，其中不可放置物品也能绘制。测试材质索引、颜色非零与 drop 渲染，不新增 shader/pipeline/二进制资源。

- [ ] **Step 7: GREEN、包级验证并提交**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/core ./internal/worldgen ./internal/assets ./internal/render -race -count=1 && go test ./internal/archcheck -count=1'
gofmt -l internal/core internal/worldgen internal/assets internal/render
git diff --check
openspec validate --all --strict --no-interactive
```

Expected: 全部通过、gofmt 无输出。勾选对应 OpenSpec task，只暂存本组并提交：

```bash
git add internal/core/block.go internal/core/item.go internal/core/inventory.go internal/core/recipe.go internal/core/item_test.go internal/core/inventory_test.go internal/core/recipe_test.go internal/worldgen/generator.go internal/worldgen/generator_test.go internal/worldgen/testdata/golden_seed42.txt internal/assets/blocks.go internal/assets/procedural.go internal/assets/blocks_test.go internal/render/hotbar.go internal/render/drop.go internal/render/hotbar_test.go internal/render/drop_test.go openspec/changes/m4e-authoritative-smelting/tasks.md
git diff --cached --check
git commit -m "feat: 定义 M4E 资源与矿石生成"
```

自动进入 Task 6。

### Task 6: 在 Chunk 内实现固定熔炉槽与原子批量掉落

**Files:**
- Create: `internal/core/furnace.go`（`FurnaceRef`、`FurnacesPerChunk=32` 与统一 UI 槽常量）
- Create: `internal/world/furnace.go`
- Create: `internal/world/furnace_test.go`
- Modify: `internal/world/chunk.go`
- Modify: `internal/world/drop.go`
- Modify: `internal/world/drop_test.go`
- Modify: `openspec/changes/m4e-authoritative-smelting/tasks.md`

**Interfaces:**
- Produces: `Chunk.Furnace`, `SetFurnace`, `FurnaceAt`, `PrepareFurnace`, `DeactivateFurnace`.
- Produces: fixed `[4]core.ItemStack` batch preflight/commit for furnace body + input + fuel + output.

- [ ] **Step 1: 写熔炉槽 RED tests**

覆盖 32 槽最低索引分配、第 33 个失败、generation 初次为 1/复用递增/MaxUint32 不复用、方块索引唯一、inactive 仅保留 generation、Clone/PayloadBytes 包含熔炉状态、旧 ID 在复用后失效；现有 `Chunk.Hash` 继续只表示方块，不包含只向 viewer 发布的熔炉状态。测试直接逐槽比较，不新增只为测试服务的 hash API。

- [ ] **Step 2: 写批量掉落 RED tests**

用满 64 个 drop slot 的边界覆盖：四堆可完整合并时成功、任一堆无法完整容纳时所有 drop 字节不变、同物品同位置先合并、空 stack 忽略、不可放置但已注册的煤/粗铁/铁锭可掉落。

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/world -run "Furnace|DropBatch|RegisteredDrop" -count=1'
```

Expected: 新 API 缺失或旧 `PrepareDrop` 拒绝非放置物品。

- [ ] **Step 3: 最小实现固定槽**

`Chunk` 只追加：

```go
furnaces [core.FurnacesPerChunk]FurnaceSlot
```

`PrepareFurnace` 只扫描两次固定数组（现位置已有活动槽则拒绝，再找最低可复用空槽）；`Clone` 值复制；`PayloadBytes` 加固定字节。不要建立 map/index cache。

- [ ] **Step 4: 在 drop 数组副本上预演批量提交**

实现一个固定值返回：

```go
func (c *Chunk) PrepareDropBatch(
	stacks [4]core.ItemStack,
	blockIndex uint32,
	pickupDelay uint8,
) ([core.DropsPerChunk]DropSlot, bool)
```

函数复制 `c.drops`，按数组顺序完整合并/分配；任何余量失败都返回 false，不修改 chunk。`CommitDropBatch` 只赋值副本。现有单物品 `PrepareDrop`/`CommitDrop` 保留供普通方块路径使用，但合法性改用 `RegisteredItem`。

- [ ] **Step 5: GREEN、race 与提交**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/world -race -count=1 && go test ./internal/archcheck -count=1'
gofmt -l internal/core internal/world
git diff --check
```

Expected: PASS。勾选 task 后精确提交：

```bash
git add internal/core/furnace.go internal/world/furnace.go internal/world/furnace_test.go internal/world/chunk.go internal/world/drop.go internal/world/drop_test.go openspec/changes/m4e-authoritative-smelting/tasks.md
git diff --cached --check
git commit -m "feat: 增加区块固定熔炉状态"
```

Expected: 提交成功，自动进入 Task 7。

### Task 7: 升级区块存档 schema v4

**Files:**
- Modify: `internal/storage/migration.go`
- Modify: `internal/storage/chunk_codec.go`
- Create: `internal/storage/chunk_furnace_test.go`
- Modify: `internal/storage/{chunk_codec_test.go,chunk_drop_test.go,migration_test.go,chunk_codec_fuzz_test.go}`
- Modify: `internal/storage/player_codec_test.go`
- Add/Modify fixture: `internal/storage/testdata/**`
- Modify: `internal/server/persistence_test.go`
- Modify: `openspec/changes/m4e-authoritative-smelting/tasks.md`

**Interfaces:**
- Produces: `currentChunkSchema=4`; logical payload order sections → 64 drops → 32 furnaces.
- Migration: v3→v4 initializes empty furnace array; v1/v2 continue existing chain.

- [ ] **Step 1: 写 migration/roundtrip/golden RED tests**

覆盖 v4 完整三格、progress/burn/generation roundtrip，v3 方块和 drops 无损迁移且 `NeedsRewrite=true`，v1/v2 链式迁移，v4 fixture 不迁移，未来 schema 拒绝。另用现有玩家 schema v3 roundtrip 确认煤、粗铁、铁锭、熔炉与铁块都可持久化，schema 常量保持 3，旧 v1-v3 fixture 不漂移。

- [ ] **Step 2: 写损坏与故障 RED tests**

覆盖 active generation=0、flag 非 0/1、重复 block index、索引越界、对应方块不是 furnace、错误 item、count、progress>199、burn>1600、inactive 残留字段、截断/尾随；DiskStore 部分写入失败后旧 v3/v4 记录仍可加载。

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage ./internal/server -run "ChunkV4|Furnace|Migration|Partial" -count=1'
```

Expected: schema 仍为 3 或字段缺失导致失败。

- [ ] **Step 3: 追加定长编码与链式迁移**

`chunkDTO` 追加 `[core.FurnacesPerChunk]world.FurnaceSlot`；`appendLogicalChunk` 末尾写 32 槽；`decodeLogicalChunk` 仅在 `schema>=4` 读槽；migration registry 只增加 `3: empty furnaces`。统一校验 encode/decode 两边，先重建 sections 后验证活动槽与 furnace block 对应。

- [ ] **Step 4: GREEN、fuzz、benchmark 与提交**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage ./internal/server -race -count=1 && go test ./internal/storage -run "^$" -fuzz FuzzDecodeChunkPayload -fuzztime=10s && go test ./internal/storage -run "^$" -bench "Chunk(Encode|Decode)" -benchmem -count=3 && go test ./internal/archcheck -count=1'
gofmt -l internal/storage internal/server
git diff --check
```

Expected: PASS/无 panic；旧 fixture 无损。勾选 task 后精确提交：

```bash
git add internal/storage/migration.go internal/storage/chunk_codec.go internal/storage/chunk_furnace_test.go internal/storage/chunk_codec_test.go internal/storage/chunk_drop_test.go internal/storage/migration_test.go internal/storage/chunk_codec_fuzz_test.go internal/storage/player_codec_test.go internal/storage/testdata/chunk-v4.bin internal/server/persistence_test.go openspec/changes/m4e-authoritative-smelting/tasks.md
git diff --cached --check
git commit -m "feat: 升级熔炉区块存档 schema v4"
```

Expected: 提交成功，自动进入 Task 8。

### Task 8: 追加协议 v7 熔炉消息与客户端只读镜像

**Files:**
- Modify: `internal/network/packet.go`
- Modify: `internal/network/message.go`
- Modify: `internal/network/registry.go`
- Modify: `internal/network/codec.go`
- Create: `internal/network/furnace_test.go`
- Modify: `internal/network/{packet_test.go,codec_test.go,codec_fuzz_test.go,tcp_test.go}`
- Create: `internal/client/furnace.go`
- Create: `internal/client/furnace_test.go`
- Modify: `openspec/changes/m4e-authoritative-smelting/tasks.md`

**Interfaces:**
- Client Play IDs append `OpenFurnace=8`, `MoveFurnaceStack=9`, `CloseFurnace=10`.
- Server Play IDs append `FurnaceState=13`, `FurnaceClosed=14`.
- `ProtocolVersion=7`; v6 login receives frozen version mismatch before Play.

- [ ] **Step 1: 写 packet/codec RED tests**

锁定 ID 与固定 payload：Open 16 bytes；Move 27 bytes；Close 8 bytes；State 30 bytes；Closed 17 bytes。覆盖 ID 的 dimension/chunk/slot/generation、有限 yaw/pitch、slot 0..38、合法 stack、progress/burn、截断、尾随、未知 packet 与 v6 登录拒绝。

- [ ] **Step 2: 写客户端镜像 RED tests**

`client.FurnaceMirror` 只接受 `FurnaceState`/`FurnaceClosed`：新 generation 替换，旧 state/旧 close 不影响当前界面，非法 state 返回 error，Reset 清空；State 返回值副本，客户端点击不改镜像。

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network ./internal/client -run "Furnace|ProtocolV7|VersionMismatch" -count=1'
```

Expected: 新类型/ID 缺失或版本仍为 6。

- [ ] **Step 3: 最小 codec 与 mirror 实现**

在既有 switch 末尾追加 message/packet case；增加小型 `encodeFurnaceRef`/`decodeFurnaceRef` 私有 helper，禁止可变长度集合。Mirror 只持有一个 `network.FurnaceState` 与 bool，不建立 map。

- [ ] **Step 4: GREEN、golden/fuzz 与提交**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network ./internal/client -race -count=1 && go test ./internal/network -run "^$" -fuzz FuzzSmallPacketCodec -fuzztime=10s && go test ./internal/network -run "^$" -bench SmallPacketCodec -benchmem -count=3 && go test ./internal/archcheck -count=1'
gofmt -l internal/network internal/client
git diff --check
```

Expected: PASS。勾选 task 后精确提交：

```bash
git add internal/network/packet.go internal/network/message.go internal/network/registry.go internal/network/codec.go internal/network/furnace_test.go internal/network/packet_test.go internal/network/codec_test.go internal/network/codec_fuzz_test.go internal/network/tcp_test.go internal/client/furnace.go internal/client/furnace_test.go openspec/changes/m4e-authoritative-smelting/tasks.md
git diff --cached --check
git commit -m "feat: 定义权威熔炉协议 v7"
```

Expected: 提交成功，自动进入 Task 9。

### Task 9: 实现有界熔炼 tick 与查看生命周期

**Files:**
- Create: `internal/sim/furnace.go`
- Create: `internal/sim/furnace_test.go`
- Modify: `internal/sim/command.go`
- Modify: `internal/sim/engine.go`
- Modify: `internal/sim/drop.go`（把 `dropInterestKeys` 改名并复用于 furnace）
- Modify: `internal/sim/player.go`
- Modify: `openspec/changes/m4e-authoritative-smelting/tasks.md`

**Interfaces:**
- `sim.session` 每人只保存一个 `core.FurnaceRef` + bool。
- `Engine.Step` 在互动后按稳定 chunk key/slot 顺序推进活动炉，并在 `finishChanges` 前用 `touchChunk` 合并 revision。
- `TickResult` 仅发布当前 viewer 的完整状态/关闭事件。

- [ ] **Step 1: 写状态机 RED tests**

覆盖：有效输入+煤时设置 1600 后同 tick 变 1599/progress=1；第 200 tick 出 1 铁锭；一煤恰好 8 锭；空输入、错误输入、输出满/异类时 progress 与 burn 都暂停；恢复继续；多人重叠半径只推进一次；半径 2 边界；区块卸载/无 Ready 玩家暂停；同区块多炉只升一次 revision。

- [ ] **Step 2: 写打开与失效 RED tests**

覆盖 Ready、sequence、有限视角、服务端六格 raycast、目标必须是活动且 generation 匹配的 furnace；一人最多一个、多人可同看；移动超距、方块改变、generation 变化、chunk unload、dimension reset 时产生精确一次 close；断线直接移除 viewer。

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim -run "FurnaceAdvance|OpenFurnace|FurnaceView|FurnacePause" -count=1'
```

Expected: command/state machine 缺失。

- [ ] **Step 3: 复用 interest scratch 实现热路径**

将 `dropInterestKeys` 最小重命名为 `activeInterestKeys`，继续使用 engine 已有 seen/scratch 和稳定 sort；`advanceDrops` 与 `advanceFurnaces` 顺序调用同一结果，不新增每 tick map。熔炉状态机只在值变化时 `SetFurnace` + `touchChunk`。

- [ ] **Step 4: 实现查看生命周期与完整状态收集**

Open 命令复用 `LookDirection`、`core.RaycastBlocks`、`interactionReach`；session 保存 ID。每 tick 在所有命令与 advance 后验证 viewer，再按 session ID 输出完整 `world.FurnaceState`。不要在 `server.session` 建第二份真相。

- [ ] **Step 5: 6400 槽 benchmark 与 allocation check**

新增 `BenchmarkAdvanceFurnaces6400`，构造 8 个 Ready 玩家、25 个重叠区块、每区块 32 槽，使用固定工作量并 `ReportAllocs`。测试用 `testing.AllocsPerRun` 锁定稳定态推进 0 alloc；若现有 `TickResult` 输出导致必要分配，只测不含 viewer 的推进 helper，不能通过缓存无界增长规避。

- [ ] **Step 6: GREEN、race 与提交**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim -race -count=1 && go test ./internal/sim -run "^$" -bench AdvanceFurnaces6400 -benchmem -count=3 && go test ./internal/archcheck -count=1'
gofmt -l internal/sim
git diff --check
```

Expected: PASS；无热路径意外分配。勾选 task 后精确提交：

```bash
git add internal/sim/furnace.go internal/sim/furnace_test.go internal/sim/command.go internal/sim/engine.go internal/sim/drop.go internal/sim/player.go openspec/changes/m4e-authoritative-smelting/tasks.md
git diff --cached --check
git commit -m "feat: 推进权威熔炉状态机"
```

Expected: 提交成功，自动进入 Task 10。

### Task 10: 实现跨容器移动、放置与原子破坏

**Files:**
- Modify: `internal/sim/furnace.go`
- Modify: `internal/sim/engine.go`
- Modify: `internal/sim/interaction_test.go`
- Create: `internal/sim/furnace_inventory_test.go`
- Modify: `internal/sim/drop_test.go`
- Modify: `internal/network/message.go`
- Modify: `internal/network/registry.go`
- Modify: `internal/network/{message_test.go,registry_test.go,codec_test.go}`
- Modify: `internal/server/publication.go` in Task 11 to translate the new sim reason
- Modify: `openspec/changes/m4e-authoritative-smelting/tasks.md`

**Interfaces:**
- Unified indices: inventory `0..35`, input `36`, fuel `37`, output `38`.
- Append exactly one new rejection: `RejectFurnaceCapacity`（sim 稳定值 11、network wire ID 12）；all invalid moves/stale refs reuse `RejectInvalidInput`/`RejectInvalidSlot`.

- [ ] **Step 1: 写跨容器事务 RED tests**

覆盖 inventory→input 只接受 raw iron，inventory→fuel 只接受 coal，output 不能作为目标但可作为来源，空目标整堆、同类合并留余量、合法异类交换、非法交换双方不变、满 inventory 取出失败、stale generation/sequence 不变、多玩家同 tick 按 session/sequence 稳定串行并让所有 viewer 看到同一最终值。

- [ ] **Step 2: 写放置/破坏 RED tests**

覆盖炉物品放置同时启用最低槽并扣 1；第 33 炉原子拒绝且不扣物品/不改 block/revision；普通铁块放置不分配炉槽。破坏空炉掉本体；满内容炉按本体→input→fuel→output 稳定预演；drop 容量不足时 block/furnace/drops/revision 全不变；成功后停用槽保留 generation。

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim -run "MoveFurnace|PlaceFurnace|BreakFurnace|FurnaceCapacity|SharedFurnace" -count=1'
```

Expected: 事务/交互行为缺失。

- [ ] **Step 3: 在值副本上计算后一次提交**

新增纯 helper：

```go
func moveFurnaceStack(
	inventory core.Inventory,
	furnace world.FurnaceSlot,
	from, to uint8,
) (core.Inventory, world.FurnaceSlot, bool)
```

先读取两个 slot、按现有 `Inventory.MoveStack` 语义计算、验证最终 input/fuel/output，成功才写 player inventory 与 chunk furnace。不要创建通用 `Container` interface。

- [ ] **Step 4: 把 furnace 特例放在共享交互原子点**

放置时先 `PrepareFurnace` 再 `SetBlock`/启用/扣 item；破坏时先 `PrepareDropBatch`，再清 block、停用 slot、提交 drop batch。所有变化仍通过现有 pending chunk change；炉内部变化用 `touchChunk`，不得重复 revision。

- [ ] **Step 5: GREEN、race 与提交**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim ./internal/world -race -count=1 && go test ./internal/archcheck -count=1'
gofmt -l internal/sim internal/world internal/network
git diff --check
```

Expected: PASS。勾选 task 后精确提交：

```bash
git add internal/sim/furnace.go internal/sim/engine.go internal/sim/interaction_test.go internal/sim/furnace_inventory_test.go internal/sim/drop_test.go internal/network/message.go internal/network/registry.go internal/network/message_test.go internal/network/registry_test.go internal/network/codec_test.go openspec/changes/m4e-authoritative-smelting/tasks.md
git diff --cached --check
git commit -m "feat: 原子操作共享熔炉物品"
```

Expected: 提交成功，自动进入 Task 11。

### Task 11: 接入服务端翻译、私有发布与 Memory/TCP 多人闭环

**Files:**
- Modify: `internal/server/session.go`
- Modify: `internal/server/publication.go`
- Create: `internal/server/furnace_publication.go`
- Modify: `internal/server/player_test.go`
- Create: `internal/server/furnace_publication_test.go`
- Modify: `internal/server/multiplayer_memory_integration_test.go`
- Modify: `internal/server/tcp_integration_test.go`
- Modify: `internal/server/persistence_test.go`
- Modify: `openspec/changes/m4e-authoritative-smelting/tasks.md`

**Interfaces:**
- `translateClientMessage` maps the three network messages into sim commands without owning furnace state.
- `publishLocalResult` sends only updates where `update.Session==current.id`; other subscribers receive nothing.

- [ ] **Step 1: 写翻译/发布 RED tests**

覆盖三个 client message 字段无损翻译；FurnaceState 只发给当前 viewer；两个 viewer 都收到相同完整状态；未打开者不收；FurnaceClosed 精确发一次；InventoryState 仍只发本人；outbox 满继续关闭慢 session 且不阻塞其他人。

- [ ] **Step 2: 写 Memory/TCP 纵向 RED tests**

使用同一脚本：两玩家登录→获取/注入资源→放炉→同时打开→交错移动 coal/raw iron→推进 200 tick→两端同见 1 iron ingot→一人取出→另一人见 output 空→旧 ID 命令拒绝。Memory 与 TCP 最终 chunk/inventory/rejection transcript 必须相同。

- [ ] **Step 3: 写 DiskStore 重启 RED test**

在 progress=137、burn=1463 时正常 flush/close/reopen；确认三格和 tick 原值恢复，停服墙钟不补算；重新进入半径后下一 tick 变 138/1462。注入 save failure 时旧完整记录可恢复，重试后才整体更新。

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "Furnace|Smelting|V4Restart|SlowFurnaceViewer" -count=1'
```

Expected: 翻译、发布或持久闭环缺失。

- [ ] **Step 4: 最小接线并 GREEN**

在现有 switch/publish 顺序追加处理；`server.session` 不保存炉状态或 viewer map。运行：

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -race -count=1 && go test ./internal/network ./internal/sim ./internal/storage -race -count=1 && go test ./internal/archcheck -count=1'
gofmt -l internal/server
git diff --check
```

Expected: PASS。勾选 task 后精确提交：

```bash
git add internal/server/session.go internal/server/publication.go internal/server/furnace_publication.go internal/server/player_test.go internal/server/furnace_publication_test.go internal/server/multiplayer_memory_integration_test.go internal/server/tcp_integration_test.go internal/server/persistence_test.go openspec/changes/m4e-authoritative-smelting/tasks.md
git diff --cached --check
git commit -m "feat: 接通多人权威熔炉服务端"
```

Expected: 提交成功，自动进入 Task 12。

### Task 12: 复用现有背包 renderer 接入熔炉 UI

**Files:**
- Modify: `internal/render/hotbar.go`
- Modify: `internal/render/hotbar_test.go`
- Modify: `internal/client/input.go`
- Modify: `internal/client/input_test.go`
- Modify: `cmd/mcgo/app.go`
- Modify: `cmd/mcgo/main.go`
- Modify: `cmd/mcgo/app_test.go`
- Modify: `openspec/changes/m4e-authoritative-smelting/tasks.md`

**Interfaces:**
- Add render-local value `FurnaceOverlay` with three stacks and two counters; renderer does not import network.
- `HotbarRenderer.Prepare` receives `*FurnaceOverlay`; nil keeps current inventory/crafting UI.
- Add `render.FurnaceSlotAt` sharing the exact layout constants used for drawing.

- [ ] **Step 1: 写 renderer/headless RED tests**

覆盖 36 inventory + 3 furnace slots、source 0..38 高亮、三种 stack/count、progress/burn bar at 0/boundaries、固定 quad/glyph capacity、命中边界、满布局 allocation/buffer bounds；普通 inventory recipe row 保持不变。

- [ ] **Step 2: 写 app/input RED tests**

覆盖本地 mirror raycast 命中 furnace 时只发一次 `OpenFurnace`，非 furnace 仍发 `PlaceBlock`；收到 State 才打开显示；两次点击发送一次 Move 且不改镜像；E/Escape 立即清 UI 并发 Close；FurnaceClosed、disconnect、PlayerState reset 清 UI/source；打开时抑制移动/视角/挖掘/放置/选择。测试只用 fake window/gfx，不调用交互式 `run()`。

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/render ./internal/client ./cmd/mcgo -run "Furnace|Inventory|Input" -count=1'
```

Expected: 布局/接线缺失。

- [ ] **Step 3: 扩容现有固定 buffer，不加 pipeline**

按最坏布局重新计算 `maxHotbarQuads/maxHotbarGlyphs`；`layoutInventory` 在 overlay 非 nil 时画三格与两条进度，不画合成行。`FurnaceOverlay` 是 render-local 值，app 从已确认 mirror 转换。

- [ ] **Step 4: 在现有右键与点击路径分流**

用 `core.RaycastBlocks` + `client.Mirror.BlockAt` 做本地 UX 命中；只决定发送 Open 还是 Place，服务端仍重新 raycast。统一 source 索引 0..38；显式 close 可本地关 UI，但任何物品变化仍等待服务端 State/InventoryState。

- [ ] **Step 5: GREEN、race 与提交**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/render ./internal/client ./cmd/mcgo -race -count=1 && go test ./internal/archcheck -count=1'
gofmt -l internal/render internal/client cmd/mcgo
git diff --check
```

Expected: PASS 且无窗口出现。勾选 task 后精确提交：

```bash
git add internal/render/hotbar.go internal/render/hotbar_test.go internal/client/input.go internal/client/input_test.go cmd/mcgo/app.go cmd/mcgo/main.go cmd/mcgo/app_test.go openspec/changes/m4e-authoritative-smelting/tasks.md
git diff --cached --check
git commit -m "feat: 接入权威熔炉界面"
```

Expected: 提交成功，自动进入 Task 13。

### Task 13: 升级 scenario v9、文档并冻结正式性能候选提交

**Files:**
- Modify: `cmd/mcgo/benchmark.go`
- Modify: `cmd/mcgo/benchmark_v5_test.go`
- Modify: `cmd/perfcheck/main.go`
- Modify: `cmd/perfcheck/main_test.go`
- Modify: `internal/client/perf.go` only if a v9 named constant is necessary
- Modify: `README.md`
- Modify: `docs/notes/lan-server.md`
- Modify: `docs/notes/perf-baseline.md`
- Modify: `openspec/changes/m4e-authoritative-smelting/tasks.md`

**Interfaces:**
- `scenarioVersion=9`; v8/v9 不可无参数静默比较。
- `cmd/perfcheck --allow-scenario-upgrade 8:9` is the only explicit upgrade path; thresholds and stable metrics stay unchanged.

- [ ] **Step 1: 写 perf RED tests**

覆盖 benchmark 报 v9；默认 v8→v9 拒绝；错误 allow 值拒绝；显式 `8:9` 执行完整性、绝对门禁及仍适用的稳定指标；v9 缺字段拒绝；v9 same-scenario Memory/TCP 继续跨 transport profile。

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo ./cmd/perfcheck -run "ScenarioVersion|V9|8To9" -count=1'
```

Expected: scenario 仍为 8 且升级 pair 不受支持。

- [ ] **Step 2: 最小升级 scenario/comparator**

只把 `scenarioVersion` 改为 9，并在现有 allow switch 追加 `8:9`；复用 v8 完整性与稳定 profile，除非报告确实新增字段，不复制 validator。

- [ ] **Step 3: 更新中文兼容文档**

说明新资源链、熔炉三格/暂停规则、多人共享、按键、协议 v7、chunk v4/v1-v3 迁移、player v3、新旧程序拒绝、备份/回退、TCP 仍仅可信 LAN，以及明确未实现工具/多配方/离线补算。

- [ ] **Step 4: 专项 fuzz/benchmark**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "^$" -fuzz FuzzSmallPacketCodec -fuzztime=10s && go test ./internal/storage -run "^$" -fuzz FuzzDecodeChunkPayload -fuzztime=10s'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/core ./internal/worldgen ./internal/world ./internal/network ./internal/storage ./internal/sim -run "^$" -bench "Craft|GenerateChunk|Furnace|Chunk(Encode|Decode)|SmallPacketCodec" -benchmem -count=3'
```

Expected: 无 panic、无失败语料、无意外无界分配；只修根因，不设机器相关微基准阈值。

- [ ] **Step 5: 全仓验证**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race'
zsh -ic 'gvm use go1.26.0 >/dev/null && go vet ./...'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
gofmt -l .
openspec validate --all --strict --no-interactive
git diff --check
```

Expected: 全部 PASS；`gofmt -l .` 与 diff check 无输出。失败必须定位并修复根因，不进入正式性能链。

- [ ] **Step 6: 勾选任务并冻结干净候选提交**

只在上述验证通过后勾选本组 task，并精确暂存：

```bash
git add cmd/mcgo/benchmark.go cmd/mcgo/benchmark_v5_test.go cmd/perfcheck/main.go cmd/perfcheck/main_test.go README.md docs/notes/lan-server.md docs/notes/perf-baseline.md openspec/changes/m4e-authoritative-smelting/tasks.md
git add internal/client/perf.go
git diff --cached --check
git commit -m "perf: 升级 M4E scenario v9"
git status --short --branch
```

Expected: 提交成功；除 `midscene_run/` 外工作树干净。这个精确 HEAD 是两次正式报告唯一允许使用的生产提交；自动进入 Task 14。

### Task 14: 建立一次性 M5 scenario v9 Memory/TCP 基线

**Files:**
- Read: `docs/notes/perf-baseline-m5.json`（v8，直到两条链都通过前保持字节不变）
- Read/Produce once: `/tmp/mcgo-m4e-v9-${m4e_sha12}-memory.json`
- Read/Produce once: `/tmp/mcgo-m4e-v9-${m4e_sha12}-tcp.json`
- Replace after both formal reports pass: `docs/notes/perf-baseline-m5.json`
- Modify: `docs/notes/perf-baseline.md`
- Modify: `openspec/changes/m4e-authoritative-smelting/tasks.md`

**Interfaces:**
- Consumes: Task 13 的干净精确 HEAD、Apple M5/24GiB 主机和现有 v8 M5 baseline。
- Produces: 由一次 Memory 与一次 TCP 报告共同证明的 v9 M5 baseline；M2 baseline 字节不变。

- [ ] **Step 1: 在运行前锁定硬件、提交、基线与唯一输出路径**

```bash
git status --short --branch
system_profiler SPHardwareDataType | rg 'Chip: Apple M5|Memory: 24 GB'
jq -e '.scenario_version == 8 and .hardware == "Apple M5 / 24GiB"' docs/notes/perf-baseline-m5.json
shasum -a 256 docs/notes/perf-baseline.json docs/notes/perf-baseline-m5.json
m4e_sha12="$(git rev-parse --short=12 HEAD)"
m4e_memory_report="/tmp/mcgo-m4e-v9-${m4e_sha12}-memory.json"
m4e_tcp_report="/tmp/mcgo-m4e-v9-${m4e_sha12}-tcp.json"
test ! -e "$m4e_memory_report"
test ! -e "$m4e_tcp_report"
```

Expected: 除 `midscene_run/` 外干净；硬件与 M5 baseline 匹配；两条目标路径都不存在。任一条件不满足立即停止，不运行 benchmark。

- [ ] **Step 2: 只执行一次正式 M5 headless Memory 报告**

在同一个 shell 中重新派生任务专用变量并执行：

```bash
m4e_sha12="$(git rev-parse --short=12 HEAD)"
m4e_memory_report="/tmp/mcgo-m4e-v9-${m4e_sha12}-memory.json"
test ! -e "$m4e_memory_report"
zsh -ic "gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport memory --perf-output $m4e_memory_report"
jq -e '.scenario_version == 9 and .transport == "memory" and .hardware == "Apple M5 / 24GiB" and .ticks.frames == 200 and .multiplayer.remote_gpu_complete.samples == 2048' "$m4e_memory_report"
shasum -a 256 "$m4e_memory_report"
zsh -ic "gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck --baseline docs/notes/perf-baseline-m5.json --current $m4e_memory_report --allow-scenario-upgrade 8:9 --max-regression 0.20"
shasum -a 256 docs/notes/perf-baseline-m5.json
```

Expected: 一次生成并通过完整性、绝对与显式升级门禁；旧 M5 baseline 哈希与 Step 1 相同。任一步失败立即停止：不重跑、不改报告、不执行 TCP、不覆盖 baseline。

- [ ] **Step 3: Memory 通过后只执行一次正式 M5 headless TCP 报告**

```bash
m4e_sha12="$(git rev-parse --short=12 HEAD)"
m4e_memory_report="/tmp/mcgo-m4e-v9-${m4e_sha12}-memory.json"
m4e_tcp_report="/tmp/mcgo-m4e-v9-${m4e_sha12}-tcp.json"
test -f "$m4e_memory_report"
test ! -e "$m4e_tcp_report"
zsh -ic "gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport tcp --perf-output $m4e_tcp_report"
jq -e '.scenario_version == 9 and .transport == "tcp" and .hardware == "Apple M5 / 24GiB" and .ticks.frames == 200 and .multiplayer.remote_gpu_complete.samples == 2048' "$m4e_tcp_report"
shasum -a 256 "$m4e_tcp_report"
zsh -ic "gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck --baseline $m4e_memory_report --current $m4e_tcp_report --max-regression 0.20"
shasum -a 256 docs/notes/perf-baseline-m5.json
```

Expected: TCP 一次生成并通过同 scenario/same hardware 跨 transport 门禁；旧 M5 baseline 哈希仍不变。失败立即停止，不重跑且不覆盖 baseline。

- [ ] **Step 4: 两条链都通过后更新基线并提交**

仅现在把 Memory v9 报告复制到 `docs/notes/perf-baseline-m5.json`，在 `tasks.md` 和 `perf-baseline.md` 记录 commit、硬件、分辨率、两份路径、SHA-256 与命令结果。然后：

```bash
m4e_sha12="$(git rev-parse --short=12 HEAD)"
m4e_memory_report="/tmp/mcgo-m4e-v9-${m4e_sha12}-memory.json"
cp "$m4e_memory_report" docs/notes/perf-baseline-m5.json
openspec validate --all --strict --no-interactive
git diff --check
git add docs/notes/perf-baseline.md docs/notes/perf-baseline-m5.json openspec/changes/m4e-authoritative-smelting/tasks.md
git diff --cached --check
git commit -m "perf: 建立 M4E scenario v9 基线"
```

Expected: baseline 的 `git_commit` 指向 Task 13 精确候选；M2 baseline 未改；提交成功后自动进入 Task 15。

### Task 15: 请求评审、归档 M4E 并交付 main

**Files:**
- Move: `openspec/changes/m4e-authoritative-smelting/**`
- Create/Modify: `openspec/specs/deterministic-ore-generation/spec.md`
- Create/Modify: `openspec/specs/authoritative-furnaces/spec.md`
- Modify: `openspec/specs/authoritative-inventory/spec.md`
- Modify: `openspec/specs/bounded-benchmark-workload/spec.md`

**Interfaces:**
- Produces: 无 M4E active change；main specs 成为后续里程碑的稳定事实来源。

- [ ] **Step 1: 按 verification-before-completion 重新取证**

不得引用旧输出；重新运行：

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race && go vet ./...'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
gofmt -l .
openspec status --change m4e-authoritative-smelting --json
openspec validate --all --strict --no-interactive
git diff --check
```

Expected: 全部 PASS、tasks 100% complete、格式命令无输出。

- [ ] **Step 2: 请求代码评审并处理有效问题**

调用 `superpowers:requesting-code-review`，以实现分支相对 main 的完整 diff 为范围。若收到意见，调用 `superpowers:receiving-code-review`，先复现/验证再修；每个修复执行相应包级 race 并独立提交。没有 actionable finding 才继续。

- [ ] **Step 3: 归档 M4E**

调用 `openspec-archive-change`，随后执行：

```bash
openspec archive m4e-authoritative-smelting --yes
openspec list --json
openspec validate --all --strict --no-interactive
git diff --check
```

Expected: active 列表为空；四项 delta 已合入主规格；archive 目录保存完整产物。

- [ ] **Step 4: 提交、推送、PR 与 main 同步**

```bash
git add -A -- openspec/specs/deterministic-ore-generation/spec.md openspec/specs/authoritative-furnaces/spec.md openspec/specs/authoritative-inventory/spec.md openspec/specs/bounded-benchmark-workload/spec.md openspec/changes/m4e-authoritative-smelting openspec/changes/archive/2026-08-04-m4e-authoritative-smelting
git diff --cached --check
git commit -m "docs: 归档 M4E 权威熔炼"
git push -u origin codex/m4e-authoritative-smelting
```

创建 ready PR，等待所有 CI 通过后普通合并；随后：

```bash
git switch main
git pull --ff-only origin main
git status --short --branch
openspec list --json
```

Expected: 本地 main 与 origin/main 一致；除 `midscene_run/` 外工作树干净；无 active change；M4E 闭环完成。

---

## 计划自检清单

- [ ] 设计中的资源 ID、生成概率/Y 边界、固定配方均映射到 Task 5。
- [ ] 固定 32 槽、generation、批量掉落、schema v4 均有 RED/GREEN、golden/fuzz/故障覆盖。
- [ ] 200/1600 tick、暂停不浪费、半径 2、6400 槽上限与零分配检查均映射到 Task 9。
- [ ] 统一 0..38 槽、输出只取、跨容器原子性、第 33 炉和破坏容量失败映射到 Task 10。
- [ ] 协议 v7、固定 packet ID/长度、Memory/TCP 一致、慢客户端和重启恢复映射到 Task 8/11。
- [ ] UI 不预测、复用 renderer、无窗口测试、close/reset/disconnect 映射到 Task 12。
- [ ] scenario v9 与全仓门禁映射到 Task 13；M2 不变、M5 Memory/TCP 各一次、失败不覆盖基线映射到 Task 14。
- [ ] 所有实现任务均有精确文件、RED 命令、GREEN 命令、提交点和自动进入下一任务说明。
- [ ] 无占位标记、含糊回指、未定义类型或单实现抽象；所有路径符合 `internal/archcheck/deps_test.go`。
