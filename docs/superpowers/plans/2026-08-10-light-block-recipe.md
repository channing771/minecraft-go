# 发光方块固定配方 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 追加稳定 recipe ID `8`（4 玻璃→4 发光方块），让服务端权威合成、Memory/TCP、持久化和八行背包 HUD 完整贯通。

**Architecture:** 只在现有 `core.Recipe` switch 末尾追加一条单输入配方，继续复用 `Inventory.Craft` 的副本扣料、`AddStack` 和原子提交；客户端仍只发送 recipe ID。HUD 固定单列从七行扩为八行，同步三个容量常量与命中几何，不建立目录、滚动或分页。

**Tech Stack:** Go 1.26、现有 core recipe/inventory、Memory/TCP server integration、WebGPU HUD、无窗口 visual capture、OpenSpec。

## Global Constraints

- 从设计提交 `dc61422` 创建独立 worktree 与分支 `codex/light-block-recipe`；执行时先使用 `superpowers:using-git-worktrees`。
- 使用用户现有 gvm Go 1.26，不下载 Go。
- `RecipeLightBlock` 必须稳定等于 `8`；输入精确为 4 个 `ItemGlass`，输出精确为 4 个 `ItemLightBlock`。
- 继续使用单输入 `CraftingRecipe` 和 `Inventory.Craft`；不得新增多输入模型、recipe registry、分页、滚动或两列布局。
- protocol 保持 v15、player schema 保持 v6、chunk schema 保持 v8、metadata 保持 v2；不改 wire 字段或存档字节。
- Memory/TCP 必须复用同一服务端权威路径；客户端发送后不得预测扣料或产出。
- HUD 固定值同步为 `recipeQuads=1+8*9`、`recipeGlyphs=20`、`openHUDHeight=670`；640×360 必须完整缩放且命中与绘制一致。
- 只更新 `inventory-crafting.png`；所有视觉命令无窗口运行，不得启动前台客户端。
- 注释、GoDoc、测试说明与文档使用中文。

---

## File Map

- Create: `openspec/changes/light-block-recipe/**` — proposal、design、三份 delta spec、tasks。
- Modify: `internal/core/recipe.go` — 稳定 ID 8 与固定映射。
- Modify: `internal/core/recipe_test.go` — 映射、原子成功/失败与 ID 边界。
- Modify: `internal/render/hotbar.go` — 八行固定容量、ID 列表与 HUD 高度。
- Modify: `internal/render/hotbar_test.go` — 八行绘制、glyph、按钮命中与窄屏边界。
- Modify: `cmd/mcgo/app_test.go` — 已确认玻璃状态发送 recipe 8，不本地预测。
- Modify: `internal/server/material_processing_integration_test.go` — Memory/TCP 合成与持久化 parity。
- Modify: `cmd/mcgo/capture.go` — `inventory-crafting` 夹具加入 4 玻璃。
- Modify: `cmd/mcgo/testdata/golden/inventory-crafting.png` — 八行 HUD 基线。
- Archive: `openspec/changes/archive/2026-08-10-light-block-recipe/`。
- Modify on archive: `openspec/specs/authoritative-crafting/spec.md`、`openspec/specs/static-block-light/spec.md`、`openspec/specs/voxel-visual-presentation/spec.md`。

### Task 1: 建立发光方块配方 OpenSpec change

**Files:**
- Create: `openspec/changes/light-block-recipe/**`

**Interfaces:**
- Consumes: `core.Recipe`、`Inventory.Craft`、`network.CraftRecipe` 现有契约。
- Produces: 三份相互一致的 delta spec；明确不升级任何版本。

- [ ] **Step 1: 创建 change**

```bash
openspec new change light-block-recipe
openspec instructions proposal --change light-block-recipe --json
```

- [ ] **Step 2: 写 proposal、design 与 delta specs**

`authoritative-crafting`：七条改八条，新增 ID 8 的精确输入输出；未知范围改为 `0` 或 `>8`；Memory/TCP/原子失败语义保持。

`static-block-light`：删除“没有正常获取入口/固定六条”的历史约束，改为正常生存流程可由 4 玻璃合成 4 发光方块；方块光、协议 v15、chunk schema v8 和派生语义不变。

`voxel-visual-presentation`：固定配方单列从七行改八行；640×360 整体缩放且按钮几何与绘制共源；不要求分页或滚动。

`design.md` 固化：只追加 switch case；`recipeQuads=73`、`recipeGlyphs=20`、`openHUDHeight=670`；`inventory-crafting` 是唯一视觉 golden。

- [ ] **Step 3: 校验并提交规划**

```bash
openspec validate light-block-recipe --strict --no-interactive
openspec validate --all --strict --no-interactive
git diff --check
git add openspec/changes/light-block-recipe
git commit -m "docs: 规划发光方块固定配方"
```

### Task 2: 用 core 原子合成测试建立 RED

**Files:**
- Modify: `internal/core/recipe_test.go`

**Interfaces:**
- Produces: `TestRecipeLightBlockIsFixedAndAtomic`；ID 缺失、数量漂移、部分扣料或容量错误均稳定失败。

- [ ] **Step 1: 添加固定映射和成功路径测试**

```go
func TestRecipeLightBlockIsFixedAndAtomic(t *testing.T) {
	if core.RecipeLightBlock != 8 {
		t.Fatalf("RecipeLightBlock=%d，想要稳定 ID 8", core.RecipeLightBlock)
	}
	recipe, ok := core.Recipe(core.RecipeLightBlock)
	want := core.CraftingRecipe{
		Input:  core.ItemStack{Item: core.ItemGlass, Count: 4},
		Output: core.ItemStack{Item: core.ItemLightBlock, Count: 4},
	}
	if !ok || recipe != want {
		t.Fatalf("发光方块配方=%+v,%v，想要 %+v", recipe, ok, want)
	}

	var inventory core.Inventory
	inventory.Hotbar.Slots[2] = core.ItemStack{Item: core.ItemGlass, Count: 1}
	inventory.Backpack[4] = core.ItemStack{Item: core.ItemGlass, Count: 3}
	next, ok := inventory.Craft(core.RecipeLightBlock)
	if !ok || next.Hotbar.Slots[0] != (core.ItemStack{Item: core.ItemLightBlock, Count: 4}) {
		t.Fatalf("发光方块合成=%+v,%v", next, ok)
	}
	if inventory.Hotbar.Slots[2].Count != 1 || inventory.Backpack[4].Count != 3 {
		t.Fatal("Craft 改写了调用方原值")
	}
}
```

- [ ] **Step 2: 添加失败不变测试**

表驱动覆盖 3 玻璃原料不足、36 格满且扣料后仍不能接收完整产物、未知 ID 9。每项断言 `ok=false` 且返回值逐字段等于原 inventory。

- [ ] **Step 3: 确认 RED**

```bash
go test ./internal/core -run 'TestRecipeLightBlock' -count=1
```

Expected: 编译或断言因 `RecipeLightBlock` 尚未实现而 FAIL；其余 core 测试不参与。

### Task 3: 追加最小权威配方并贯通服务端 parity

**Files:**
- Modify: `internal/core/recipe.go`
- Modify: `internal/core/recipe_test.go`
- Modify: `internal/server/material_processing_integration_test.go`

**Interfaces:**
- Produces: recipe ID 8；不改 `Inventory.Craft` 算法或 network message。

- [ ] **Step 1: 在稳定枚举末尾追加 ID**

```go
// RecipeLightBlock 用 4 个玻璃合成 4 个发光方块。
RecipeLightBlock
```

在 `Recipe` switch 末尾追加：

```go
case RecipeLightBlock:
	return CraftingRecipe{
		Input:  ItemStack{Item: ItemGlass, Count: 4},
		Output: ItemStack{Item: ItemLightBlock, Count: 4},
	}, true
```

不要修改 `Inventory.Craft`。

- [ ] **Step 2: GREEN、mutation 与 core race**

```bash
gofmt -w internal/core/recipe.go internal/core/recipe_test.go
go test ./internal/core -run 'TestRecipeLightBlock|TestCraft' -race -count=1
go test ./internal/core -race -count=1
```

Mutation：把输入临时改为 3 或输出改为 1，固定映射测试必须 FAIL；恢复并确认 PASS。mutation 不提交。

- [ ] **Step 3: 扩展 Memory/TCP 材料加工脚本**

在初始背包加入一个互不干扰的栏位：

```go
initial.Backpack[0] = core.ItemStack{Item: core.ItemGlass, Count: 4}
```

在原木合成后、打开熔炉前执行 `CraftRecipe{Sequence:2, Recipe:RecipeLightBlock}`，断言原木配方结果保留、4 玻璃消失、4 发光方块按稳定插入顺序进入 Hotbar 4。把后续命令 sequence 机械顺延 1，并把原脚本取出熔炼玻璃的目标从 Hotbar 4 改为 Hotbar 5；最终状态明确断言 Hotbar 4 是 4 个发光方块、Hotbar 5 是 1 个玻璃，`materialProcessingResult` 和持久化 hash 继续由同一 Memory/TCP 测试比较。

- [ ] **Step 4: 运行服务端 parity**

```bash
go test ./internal/server -run TestMaterialProcessingMemoryTCPParity -race -count=1
```

Expected: Memory 与 TCP 的最终 inventory、furnace、revision 和持久化 hash 完全相等。

- [ ] **Step 5: 提交 core 与服务端闭环**

```bash
git add internal/core/recipe.go internal/core/recipe_test.go \
  internal/server/material_processing_integration_test.go \
  openspec/changes/light-block-recipe/tasks.md
git commit -m "feat: 增加发光方块固定配方"
```

### Task 4: 扩展八行 HUD 与客户端命中

**Files:**
- Modify: `internal/render/hotbar.go`
- Modify: `internal/render/hotbar_test.go`
- Modify: `cmd/mcgo/app_test.go`

**Interfaces:**
- Produces: 八行固定单列 HUD；最后一行点击只发送 recipe 8。

- [ ] **Step 1: 先把 HUD 测试改为八行 RED**

在 `TestInventoryLayoutDrawsAllFixedRecipeRows`：

- 末项改为 `RecipeLightBlock`；
- glyph 期望从 16 改 20；
- `wantItems` 追加 `{ItemGlass, ItemLightBlock}`；
- Y 列表追加 `56`；
- 按钮数量期望自然使用 `len(inventoryRecipeIDs)==8`；
- 加 4 玻璃 inventory，断言只启用第 8 个按钮。

在 `TestRecipeButtonHitTestMatchesDrawnGeometry` 追加 `{"发光方块", 57, RecipeLightBlock}`。保留 640×360 的全 quad/glyph 边界和命中测试。

```bash
go test ./internal/render -run 'TestInventoryLayoutDrawsAllFixedRecipeRows|TestRecipeButtonHitTestMatchesDrawnGeometry|TestOpenInventoryFitsAndHitsAt640x360' -count=1
```

Expected: FAIL，原因是第八行尚不存在。

- [ ] **Step 2: 修改四个固定值**

```go
// 八条固定配方：面板 + 每行两个栏位与双层物品色块、按钮和加号。
recipeQuads  = 1 + 8*9
recipeGlyphs = 20

openHUDHeight = float32(670)
```

并在 `inventoryRecipeIDs` 末尾追加 `core.RecipeLightBlock`；把附近 ponytail 注释改为“当前只有八条固定配方”。不改布局函数或引入动态容量。

- [ ] **Step 3: 扩展客户端已确认状态测试**

在 `TestCraftRecipeClickUsesConfirmedInventory` 表中追加：

```go
{"发光方块", core.RecipeLightBlock, core.ItemStack{Item: core.ItemGlass, Count: 4}},
```

保持断言：只发送一次 `network.CraftRecipe`，本地已确认镜像完全不变，`inventorySource` 清除。

- [ ] **Step 4: GREEN、mutation 与 race**

```bash
gofmt -w internal/render/hotbar.go internal/render/hotbar_test.go cmd/mcgo/app_test.go
go test ./internal/render ./cmd/mcgo -run 'TestInventoryLayoutDrawsAllFixedRecipeRows|TestRecipeButtonHitTestMatchesDrawnGeometry|TestOpenInventoryFitsAndHitsAt640x360|TestCraftRecipeClickUsesConfirmedInventory' -race -count=1
go test ./internal/render ./cmd/mcgo -race -count=1
```

Mutation：临时把 `openHUDHeight` 恢复 618，640×360 边界测试必须 FAIL；恢复 670。再移除 ID8 列表项，行数/命中测试必须 FAIL；恢复。

- [ ] **Step 5: 提交 HUD**

```bash
git add internal/render/hotbar.go internal/render/hotbar_test.go cmd/mcgo/app_test.go \
  openspec/changes/light-block-recipe/tasks.md
git commit -m "feat: 展示八条固定合成配方"
```

### Task 5: 更新唯一视觉基线

**Files:**
- Modify: `cmd/mcgo/capture.go`
- Modify: `cmd/mcgo/testdata/golden/inventory-crafting.png`

- [ ] **Step 1: 让第八行在夹具中可用**

在 `inventory-crafting` 的空闲栏位加入：

```go
inventory.Backpack[6] = core.ItemStack{Item: core.ItemGlass, Count: 4}
```

- [ ] **Step 2: 无窗口更新并限制 golden 范围**

```bash
make visual-update
git diff --name-only -- cmd/mcgo/testdata/golden
make visual-check
```

Expected: golden 目录只显示 `inventory-crafting.png`。只读目检必须看见完整八行、末行玻璃→发光方块、无重叠/裁切，640×360 输出边界正常。若其他图片变化，先查共享状态漂移，不接受顺手更新。

- [ ] **Step 3: 提交视觉结果**

```bash
git add cmd/mcgo/capture.go cmd/mcgo/testdata/golden/inventory-crafting.png \
  openspec/changes/light-block-recipe/tasks.md
git commit -m "test: 更新八行合成视觉基线"
```

### Task 6: 全量验证、评审、同步与归档

**Files:**
- Modify: `openspec/changes/light-block-recipe/tasks.md`
- Archive: `openspec/changes/archive/2026-08-10-light-block-recipe/**`
- Modify: 三份主规格。

- [ ] **Step 1: 运行完整门禁与记录 benchmark**

```bash
go test ./internal/core ./internal/render ./internal/client ./internal/network ./internal/server ./cmd/mcgo -race -count=1
go test ./internal/archcheck -count=1
go test ./... -race
go vet ./...
go test ./internal/core -run '^$' -bench BenchmarkInventoryCraftWorstCase -benchmem -count=5
make visual-check
gofmt -l .
git diff --check
openspec validate --all --strict --no-interactive
```

Expected: 全部 exit 0，benchmark 只记录；当前九个无窗口视觉场景全部在阈值内。

- [ ] **Step 2: 请求独立代码与视觉评审**

评审重点：ID8 稳定、输入输出精确、失败原子、Memory/TCP parity、无版本变化、八行容量与命中、唯一 golden。发现必须修复并复验。

- [ ] **Step 3: 归档并提交**

```bash
openspec archive light-block-recipe -y
openspec validate --all --strict --no-interactive
openspec list --json
git diff --check
git add openspec/changes/archive/2026-08-10-light-block-recipe \
  openspec/specs/authoritative-crafting/spec.md \
  openspec/specs/static-block-light/spec.md \
  openspec/specs/voxel-visual-presentation/spec.md
git commit -m "docs: 归档发光方块固定配方"
git status --short --branch
```

Expected: active list 不含该 change；除用户原有日志外 worktree clean，可独立推送和创建 PR。
