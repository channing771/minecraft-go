# 确定性橡树生成 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 使用现有橡木原木与树叶，在未保存地形中生成稀疏、确定、跨区块无接缝的橡树，并增加一个无窗口 `oak-grove` 视觉场景。

**Architecture:** 每个 `8×8` 世界网格只计算一个候选树；世界种子和网格坐标通过现有 `oreHash` 选定生成门槛、根位置和 `4..6` 格树高。单点查询与整区块生成共用同一候选和树形判断；整区块只枚举可能覆盖该 chunk 的少量候选，不引入结构框架或缓存。树叶只填原始空气，原木覆盖树叶，保证遍历顺序不影响结果。

**Tech Stack:** Go 1.26、现有 `internal/worldgen`、现有 chunk palette、无窗口 capture、OpenSpec。

## Global Constraints

- 从设计提交 `dc61422` 创建独立 worktree 与分支 `codex/deterministic-oak-trees`；执行时先使用 `superpowers:using-git-worktrees`。
- 使用用户现有 gvm Go 1.26，不下载 Go。
- 复用 `core.OakLogID`、`core.LeavesID` 和 `oreHash`；不得新增 block/item ID、方块状态、协议、schema、迁移或通用 structure abstraction。
- 已保存区块不改写；只有尚未生成或未保存的区块采用新规则。
- `BaseBlockAt` 与 `GenerateChunk` 必须逐方块一致；负坐标、跨 chunk 树冠和候选遍历顺序必须确定。
- 树干高度固定为 `4..6`；树冠轮廓固定，不提供配置项。
- 视觉验证只运行无窗口 `make visual-update` / `make visual-check`，不得启动前台客户端。
- 性能只记录，不因数值变差直接失败；测试失败、真实 overflow、数据损坏和结构错误仍是门禁。
- 注释、GoDoc、测试说明与文档使用中文；保留 Go 标识符和既有技术术语。

---

## File Map

- Create: `openspec/changes/deterministic-oak-trees/**` — proposal、design、delta specs 与 tasks。
- Create: `openspec/changes/deterministic-oak-trees/specs/deterministic-tree-generation/spec.md` — 树候选、树形与一致性契约。
- Create: `openspec/changes/deterministic-oak-trees/specs/visual-verification/spec.md` — `oak-grove` 无窗口场景。
- Modify: `internal/worldgen/generator.go` — 把树覆盖加入单点与整区块生成。
- Create: `internal/worldgen/tree.go` — 最小纯树候选与树形逻辑。
- Create: `internal/worldgen/tree_test.go` — 固定候选、树形、阻挡、顺序和跨 chunk 测试。
- Modify: `internal/worldgen/generator_test.go` — parity、determinism、golden 与 benchmark。
- Modify: `internal/worldgen/testdata/golden_seed42.txt` — 审核后的新生成结果。
- Create: `cmd/mcgo/capture_oak_grove.go` — 从真实生成器装入固定橡树林镜像。
- Create: `cmd/mcgo/capture_oak_grove_test.go` — 场景夹具与注册顺序测试。
- Modify: `cmd/mcgo/capture.go` — 在末尾追加 `oak-grove`。
- Modify: `cmd/mcgo/capture_test.go` — 场景总数/末尾场景断言。
- Create: `cmd/mcgo/testdata/golden/oak-grove.png` — 经人工确认的无窗口基线。
- Modify: `cmd/mcgo/testdata/golden/README.md` — 登记新场景。
- Archive: `openspec/changes/archive/2026-08-10-deterministic-oak-trees/`，并同步主规格。

### Task 1: 建立确定性树木 OpenSpec change

**Files:**
- Create: `openspec/changes/deterministic-oak-trees/**`

**Interfaces:**
- Consumes: `Generator.HeightAt`、现有自然材料规则、`BaseBlockAt` / `GenerateChunk`。
- Produces: 新 capability `deterministic-tree-generation`，以及 `visual-verification` 的 `oak-grove` 增量。

- [ ] **Step 1: 创建并填写 change**

```bash
openspec new change deterministic-oak-trees
openspec instructions proposal --change deterministic-oak-trees --json
```

`proposal.md` 明确：只影响新生成地形；复用现有 ID；没有旧区块迁移。`design.md` 固化下列常量和优先级：

```go
const (
	oakTreeCellShift = 3 // 8×8
	oakTreeSalt      = uint64(0xA24BAED4963EE407)
)
```

- 以 `(cellX, 0, cellZ)` 调用 `oreHash`；`hash&1 == 0` 才生成。
- 候选 X 使用 `(hash>>1)&7`，Z 使用 `(hash>>4)&7`，高度使用 `4+(hash>>7)%3`。
- 根位置是草地表上方第一格；树干顶 `topY = root.Y + height - 1`。
- `topY-2` 与 `topY-1`：去掉四角的 `5×5`；`topY`：完整 `3×3`；`topY+1`：中心与四个水平相邻格。
- 树叶只覆盖原始空气；原木优先；候选顺序不影响输出。

- [ ] **Step 2: 写可判定 delta 场景**

`deterministic-tree-generation/spec.md` 至少覆盖：固定候选/50% 门槛、草地限制、树干原始空气、世界上界、固定四层树冠、叶块碰撞省略、原木优先、负坐标、跨 chunk、单点/整块一致、旧区块不迁移。

`visual-verification/spec.md` 要求在全部现有场景末尾追加 `oak-grove`，使用固定 seed、固定生成 chunk、固定正午/相机并走正常 mirror→mesher→renderer→upload 路径。

- [ ] **Step 3: 严格校验并提交规划**

```bash
openspec validate deterministic-oak-trees --strict --no-interactive
openspec validate --all --strict --no-interactive
git diff --check
git add openspec/changes/deterministic-oak-trees
git commit -m "docs: 规划确定性橡树生成"
```

### Task 2: 用固定候选和树形测试建立 RED

**Files:**
- Create: `internal/worldgen/tree_test.go`

**Interfaces:**
- Produces: 对候选哈希、固定树形和生成边界的精确回归；实现缺失时只因树能力缺失而 RED。

- [ ] **Step 1: 写固定 seed 42 候选表**

测试使用 `package worldgen` 直接锁内部纯逻辑：

```go
func TestOakTreeCandidateIsStable(t *testing.T) {
	tests := []struct {
		cellX, cellZ int32
		spawn         bool
		rootX, rootZ  int32
		height        int32
	}{
		{cellX: 0, cellZ: 0, spawn: false},
		{cellX: -1, cellZ: -1, spawn: true, rootX: -4, rootZ: -4, height: 5},
		{cellX: 1, cellZ: 0, spawn: true, rootX: 13, rootZ: 4, height: 5},
	}
	// 调用 oakTreeForCell，逐字段比较。
}
```

第一项即使不生成，也只断言 `spawn=false`；不要把无意义根坐标变成契约。

- [ ] **Step 2: 写固定树形和碰撞 RED**

用一个手工 `oakTree{Root:{X:0,Y:65,Z:0}, Height:5}` 对 `oakTreeBlockAt` 断言：

- `Y=65..69` 中心都是 `OakLogID`；
- `Y=67,68` 的半径 2 平面有 21 个叶/原木占位，四角为空；
- `Y=69` 是完整 `3×3`；
- `Y=70` 只有十字 5 格；
- 轮廓外为空；中心重叠位置返回原木。

再用真实 `Generator` 固定坐标断言：非草表、树干受阻、`topY+1 >= core.MaxY` 时整棵树缺失；叶目标处原始地形为实心时只省略该叶，不取消树干。

- [ ] **Step 3: 写跨 chunk、负坐标与 parity RED**

枚举 seed 42 的 `(-1,-1)` 候选树以及一个树冠跨 `x=15/16` 或 `z=15/16` 的固定候选。对涉及的所有世界位置比较：

```go
got := generator.BaseBlockAt(position)
chunk := generator.GenerateChunk(position.Chunk())
lx, _, lz := position.Local()
want := chunk.BlockAt(lx, position.Y, lz)
```

另外以正序和逆序候选应用到两个相同 chunk，逐方块比较，锁定“原木优先且顺序无关”。

- [ ] **Step 4: 确认 RED**

```bash
go test ./internal/worldgen -run 'TestOakTree|TestBaseBlockAtMatchesGeneratedChunkWithOakTrees' -count=1
```

Expected: FAIL 原因是树 helper/树块尚不存在，不得由坐标表错误或编译外问题造成。

### Task 3: 实现最小纯树候选与树形

**Files:**
- Create: `internal/worldgen/tree.go`
- Modify: `internal/worldgen/generator.go`
- Test: `internal/worldgen/tree_test.go`

**Interfaces:**
- Produces: `oakTreeForCell`、`oakTreeBlockAt`、`treeBlockAt`、`applyOakTrees` 四个包内实现点；不导出新 API。

- [ ] **Step 1: 实现候选计算和有效性**

```go
type oakTree struct {
	Root   core.BlockPos
	Height int32
}

func (g *Generator) oakTreeForCell(cellX, cellZ int32) (oakTree, bool) {
	hash := oreHash(g.seed, core.BlockPos{X: cellX, Z: cellZ}, oakTreeSalt)
	if hash&1 != 0 {
		return oakTree{}, false
	}
	x := (cellX << oakTreeCellShift) + int32((hash>>1)&7)
	z := (cellZ << oakTreeCellShift) + int32((hash>>4)&7)
	height := int32(4 + (hash>>7)%3)
	surface := g.HeightAt(x, z)
	root := core.BlockPos{X: x, Y: surface + 1, Z: z}
	if g.generatedBlockAt(core.BlockPos{X: x, Y: surface, Z: z}, surface) != core.GrassID ||
		root.Y+height >= core.MaxY {
		return oakTree{}, false
	}
	for y := root.Y; y < root.Y+height; y++ {
		if g.generatedBlockAt(core.BlockPos{X: x, Y: y, Z: z}, surface) != core.AirID {
			return oakTree{}, false
		}
	}
	return oakTree{Root: root, Height: height}, true
}
```

括号必须保留，避免 shift 与加法可读性歧义；实现后由 `gofmt` 固化格式。

- [ ] **Step 2: 实现固定树形**

`oakTreeBlockAt(tree, pos)` 先判树干中心列并返回 `OakLogID`；再按 `dy := pos.Y-(tree.Root.Y+tree.Height-1)` 判断 `-2/-1/0/1` 四层。`-2/-1` 接受 `abs(dx)<=2 && abs(dz)<=2 && !(abs(dx)==2 && abs(dz)==2)`；`0` 接受半径 1 方形；`1` 接受 `abs(dx)+abs(dz)<=1`。不建立 shape slice 或通用结构接口。

- [ ] **Step 3: 接入单点查询**

在 `BaseBlockAt` 先计算现有 base；base 非空气直接返回。仅对空气位置扫描可能覆盖该点的网格：

```go
for cellZ := (pos.Z - 2) >> oakTreeCellShift; cellZ <= (pos.Z+2)>>oakTreeCellShift; cellZ++ {
	for cellX := (pos.X - 2) >> oakTreeCellShift; cellX <= (pos.X+2)>>oakTreeCellShift; cellX++ {
		// 原木命中立即返回；树叶只记录，最后返回。
	}
}
```

算术右移提供负坐标向负无穷的 8 格划分。不得使用截断向零的 `/8`。

- [ ] **Step 4: 接入整 chunk 生成**

保留现有地形填充循环，随后 `applyOakTrees(c)` 枚举 chunk X/Z 边界向外扩 2 格后覆盖到的候选 cell。对每棵有效树只遍历固定树干和四层树冠；目标不在当前 chunk 时跳过。叶只在 `c.BlockAt(...) == AirID` 时写入；原木在当前值为 Air/Leaves 时写入。最后沿用一次 `c.Compact()`。

- [ ] **Step 5: GREEN、mutation 与 race**

```bash
gofmt -w internal/worldgen/generator.go internal/worldgen/tree.go internal/worldgen/tree_test.go
go test ./internal/worldgen -run 'TestOakTree|TestBaseBlockAtMatchesGeneratedChunkWithOakTrees' -count=1
go test ./internal/worldgen -race -count=1
```

Mutation：把 cell 计算临时改成 `/8`，负坐标测试必须 FAIL；恢复。再把 log 优先改为 leaf 先返回，顺序/中心测试必须 FAIL；恢复。mutation 不提交。

- [ ] **Step 6: 更新生成 golden 并记录 benchmark**

```bash
go test ./internal/worldgen -run TestGenerateChunkGolden -update -count=1
go test ./internal/worldgen -run TestGenerateChunkGolden -count=1
go test ./internal/worldgen -run '^$' -bench 'GenerateChunk' -benchmem -count=5
```

逐行核对 `golden_seed42.txt` 只因树加入而变化。在 `generator_test.go` 增加 `BenchmarkGenerateChunkWithOakTrees`，循环直接调用 `worldgen.New(42).GenerateChunk(core.ChunkPos{X: -1, Z: -1})` 并 `b.ReportAllocs()`；不加 benchmark framework。性能数值写入本地任务报告，只记录。

- [ ] **Step 7: 提交生成逻辑**

```bash
git add internal/worldgen/generator.go internal/worldgen/tree.go \
  internal/worldgen/tree_test.go internal/worldgen/generator_test.go \
  internal/worldgen/testdata/golden_seed42.txt \
  openspec/changes/deterministic-oak-trees/tasks.md
git commit -m "feat: 生成确定性橡树"
```

### Task 4: 增加无窗口 oak-grove 场景

**Files:**
- Create: `cmd/mcgo/capture_oak_grove.go`
- Create: `cmd/mcgo/capture_oak_grove_test.go`
- Modify: `cmd/mcgo/capture.go`
- Modify: `cmd/mcgo/capture_test.go`
- Create: `cmd/mcgo/testdata/golden/oak-grove.png`
- Modify: `cmd/mcgo/testdata/golden/README.md`

**Interfaces:**
- Consumes: `worldgen.New(42).GenerateChunk` 与现有 `applyCaptureMirror`。
- Produces: 场景表最后一个 `oak-grove`，没有专用渲染旁路。

- [ ] **Step 1: 写 RED 场景测试**

断言：`captureScenes[len(captureScenes)-1].Name == "oak-grove"`；`prepareOakGrove` 装入的固定 chunk 同时包含 Grass/OakLog/Leaves；场景 `worldTimeTicks=6000` 且显式设置相机位置、Yaw、Pitch。

- [ ] **Step 2: 实现最小生成器夹具**

`prepareOakGrove` 生成固定 seed 42、固定 `3×3` chunk；把每个 section 的现有 `Blocks.Snapshot()` 转为 `network.SectionData` 后通过 `applyCaptureMirror` 装入。转换代码放在 `capture_oak_grove.go` 的一个包内函数，不导出、不改 server API。场景只设置：

```go
app.worldTimeTicks = 6000
app.camera.Pos = mgl32.Vec3{-3.5, 70.5, 12.5}
app.camera.Yaw = 0
app.camera.Pitch = -0.12
```

测试逐字段锁定上述常量；若真实无窗口候选证明树体被裁切，只允许在提交 golden 前一次性调整这三个相机常量并同步测试，不得接受 CLI 参数。

- [ ] **Step 3: 追加场景并运行无窗口候选**

```bash
go test ./cmd/mcgo -run 'Test.*OakGrove|TestCaptureScenes' -race -count=1
make visual-update
make visual-check
```

Expected: `oak-grove.png` 新增，既有图片除共享地形确实改变者外不应无故变化。逐张只读检查变化图片：树干连续、四层树冠紧凑、跨 chunk 无裂缝、无漂浮错误叶块、没有 HUD/相机裁切。

- [ ] **Step 4: 提交视觉场景**

```bash
git add cmd/mcgo/capture.go cmd/mcgo/capture_test.go \
  cmd/mcgo/capture_oak_grove.go cmd/mcgo/capture_oak_grove_test.go \
  cmd/mcgo/testdata/golden cmd/mcgo/testdata/golden/README.md \
  openspec/changes/deterministic-oak-trees/tasks.md
git commit -m "test: 增加橡树林视觉场景"
```

### Task 5: 全量验证、评审、同步与归档

**Files:**
- Modify: `openspec/changes/deterministic-oak-trees/tasks.md`
- Archive: `openspec/changes/archive/2026-08-10-deterministic-oak-trees/**`
- Modify: `openspec/specs/deterministic-tree-generation/spec.md`
- Modify: `openspec/specs/visual-verification/spec.md`

- [ ] **Step 1: 运行完整门禁**

```bash
go test ./internal/worldgen ./internal/client ./internal/mesh ./internal/render ./cmd/mcgo -race -count=1
go test ./internal/archcheck -count=1
go test ./... -race
go vet ./...
make visual-check
gofmt -l .
git diff --check
openspec validate --all --strict --no-interactive
```

Expected: 全部 exit 0；`gofmt -l .` 无输出；所有视觉场景在阈值内。

- [ ] **Step 2: 请求独立代码与视觉评审**

评审必须核对固定哈希位、50% 门槛、树冠计数、负坐标、生成顺序、Base/Chunk parity，以及 `oak-grove` 实图。修复发现后重跑受影响门禁；不得以更新 golden 隐藏逻辑错误。

- [ ] **Step 3: 同步并归档**

全部 tasks 勾选后：

```bash
openspec archive deterministic-oak-trees -y
openspec validate --all --strict --no-interactive
openspec list --json
git diff --check
```

Expected: active list 不含该 change；主规格包含完整树生成与 `oak-grove` 场景；归档保留 `.openspec.yaml`。

- [ ] **Step 4: 提交归档并检查状态**

```bash
git add openspec/changes/archive/2026-08-10-deterministic-oak-trees \
  openspec/specs/deterministic-tree-generation/spec.md \
  openspec/specs/visual-verification/spec.md
git commit -m "docs: 归档确定性橡树生成"
git status --short --branch
```

Expected: 除用户原有日志外 worktree clean，可独立推送和创建 PR。
