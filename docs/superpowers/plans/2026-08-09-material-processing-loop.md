# 基础材料加工闭环 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 追加橡木原木到木板的固定合成，并让粗铁、沙子和黏土共用既有权威熔炉形成铁锭、玻璃和砖块的最小加工闭环。

**Architecture:** `core.SmeltingOutput` 是唯一输入到产物映射；world 存档模型、network 信任边界、sim 推进与容器栏位共同查询它。继续使用现有固定配方 switch、熔炉三个栏位、煤炭与计时，不增加数据驱动目录、协议字段或 schema。

**Tech Stack:** Go 1.26（使用用户已有 gvm）、现有 core/world/sim/network、固定 HUD renderer、无窗口视觉抓帧、OpenSpec。

## Global Constraints

- [ ] 共同前置：`m4n-static-block-light` 已归档并合入 `main`；同步远程后冻结与另外两项相同的 `BASE_SHA`。

```bash
cd /Users/chen/chenwork/minecraft-go
BASE_SHA=$(git rev-parse main)
git worktree add .worktrees/material-processing-loop -b codex/material-processing-loop "$BASE_SHA"
cd /Users/chen/chenwork/minecraft-go/.worktrees/material-processing-loop
test "$(git rev-parse HEAD)" = "$BASE_SHA"
```

- [ ] 本分支不得修改 `AGENTS.md`、`CLAUDE.md`、`README.md`、`openspec/config.yaml`、世界生成/迁移代码、目标轮廓代码或非 `inventory-crafting` 的视觉 golden。
- [ ] Task 6 独占 `cmd/mcgo/testdata/golden/inventory-crafting.png`；其他 golden 若发生漂移，停止并定位，不纳入提交。
- [ ] 不下载 Go；使用现有 gvm。不得启动前台窗口；视觉验证只走 `--capture`/Makefile 无窗口链路。
- [ ] 不升级 `network.ProtocolVersion=15`、player schema v6、chunk schema v8 或 metadata v2；不重排任何 ItemID/BlockID/RecipeID。
- [ ] 每完成一个任务组即验证、勾选、提交并自动继续。

---

## Task 1: 创建材料加工 OpenSpec change

**Files:**
- Create: `openspec/changes/material-processing-loop/proposal.md`
- Create: `openspec/changes/material-processing-loop/design.md`
- Create: `openspec/changes/material-processing-loop/specs/authoritative-crafting/spec.md`
- Create: `openspec/changes/material-processing-loop/specs/authoritative-furnaces/spec.md`
- Create: `openspec/changes/material-processing-loop/specs/voxel-visual-presentation/spec.md`
- Create: `openspec/changes/material-processing-loop/tasks.md`

- [ ] 1.1 读取批准设计及三份主规格，用 `openspec-propose` 建立 change。
- [ ] 1.2 `authoritative-crafting` 修改固定配方要求为 ID 1..7，并明确 ID 7 是 `1 OakLog -> 4 OakPlanks`；`authoritative-furnaces` 把固定映射改为三条并写明冲突输出全暂停、切换输入清零 Progress/保留 Burn；`voxel-visual-presentation` 把固定合成区域改为七行且 640×360 全部可见。
- [ ] 1.3 design 明确 `core.SmeltingOutput` 唯一映射、network/world 只做栏位集合校验、sim 决定当前产物、没有版本升级；tasks 对应本计划 Task 2–7。
- [ ] 1.4 严格验证并提交：

```bash
openspec validate material-processing-loop --strict --no-interactive
openspec validate --all --strict --no-interactive
git diff --check
git add openspec/changes/material-processing-loop
git commit -m "docs: 规划基础材料加工闭环"
```

---

## Task 2: 追加稳定木板配方和唯一熔炼映射

**Files:**
- Modify: `internal/core/recipe.go`
- Modify: `internal/core/recipe_test.go`
- Modify: `internal/core/chest_test.go`
- Modify: `internal/core/light_block_test.go`
- Create: `internal/core/smelting.go`
- Create: `internal/core/smelting_test.go`

- [ ] 2.1 写 RED：锁定 `RecipeOakPlanks == RecipeChest+1 == 7`、`Recipe(7)` 是一根原木到四块木板；成功、原料不足和满背包产物放不下时沿用 `Inventory.Craft` 原子语义；未知 8 拒绝。
- [ ] 2.2 写 `SmeltingOutput` RED 表：

```go
tests := []struct{ input, output core.ItemID; ok bool }{
	{core.ItemRawIron, core.ItemIronIngot, true},
	{core.ItemSand, core.ItemGlass, true},
	{core.ItemClay, core.ItemBrick, true},
	{core.ItemNone, core.ItemNone, false},
	{core.ItemStone, core.ItemNone, false},
}
```

- [ ] 2.3 运行 RED：

```bash
zsh -ic 'go test ./internal/core -run "OakPlanks|SmeltingOutput|Recipe" -count=1'
```

Expected: 新符号缺失或旧“最后配方为 6”断言失败。

- [ ] 2.4 最小实现：在 RecipeID 末尾追加 `RecipeOakPlanks` 和一个 switch case；新增唯一公共查询：

```go
func SmeltingOutput(input ItemID) (ItemID, bool) {
	switch input {
	case ItemRawIron:
		return ItemIronIngot, true
	case ItemSand:
		return ItemGlass, true
	case ItemClay:
		return ItemBrick, true
	default:
		return ItemNone, false
	}
}
```

不要建立 `RecipeRegistry`、slice 目录或运行时配置。

- [ ] 2.5 GREEN 与 mutation：交换 sand/clay 产物时表测试必须失败；恢复后运行：

```bash
gofmt -w internal/core/recipe.go internal/core/recipe_test.go internal/core/chest_test.go internal/core/light_block_test.go internal/core/smelting.go internal/core/smelting_test.go
zsh -ic 'go test ./internal/core -race -count=1'
git diff --check
```

- [ ] 2.6 勾选并提交：

```bash
git add internal/core openspec/changes/material-processing-loop/tasks.md
git commit -m "feat: 增加木板配方与熔炼映射"
```

---

## Task 3: 统一 world 与 network 的熔炉栏位信任边界

**Files:**
- Modify: `internal/world/furnace.go`
- Modify: `internal/world/furnace_test.go`
- Modify: `internal/network/message.go`
- Modify: `internal/network/furnace_test.go`

- [ ] 3.1 先扩展 RED：world/network 的输入栏分别接受 RawIron/Sand/Clay，输出栏分别接受 IronIngot/Glass/Brick；仍拒绝煤炭放输入、原料放输出、工具耐久污染、未知物品和数量越界；空栏保持规范。
- [ ] 3.2 运行 RED：

```bash
zsh -ic 'go test ./internal/world ./internal/network -run "Furnace.*Material|Furnace.*Invalid|FurnaceMessages" -count=1'
```

- [ ] 3.3 world 与 network 各保留两个小型私有 validator：输入通过 `core.SmeltingOutput(stack.Item)` 判断；输出只接受固定产物集合 `{IronIngot,Glass,Brick}`。燃料继续只接受 Coal。不要复制三条输入到输出映射，也不要让 output 必须和当前 input 匹配——输入耗尽后合法产物仍需留在输出格。
- [ ] 3.4 GREEN、wire 保真和 mutation：让 network 忽略新 input validator 时新 FurnaceState round-trip 必须失败；恢复后运行：

```bash
gofmt -w internal/world/furnace.go internal/world/furnace_test.go internal/network/message.go internal/network/furnace_test.go
zsh -ic 'go test ./internal/world ./internal/network -race -count=1'
zsh -ic 'go test ./internal/network -run "CodecGolden|Furnace" -count=1'
zsh -ic 'go test ./internal/archcheck -count=1'
git diff --check
```

- [ ] 3.5 勾选并提交：

```bash
git add internal/world/furnace.go internal/world/furnace_test.go internal/network/message.go internal/network/furnace_test.go openspec/changes/material-processing-loop/tasks.md
git commit -m "feat: 接受固定材料熔炉状态"
```

---

## Task 4: 让权威熔炉按当前输入产出并重置跨材料进度

**Files:**
- Modify: `internal/sim/furnace.go`
- Modify: `internal/sim/furnace_test.go`
- Modify: `internal/sim/furnace_inventory_test.go`

- [ ] 4.1 把 `smeltingFurnace` 改为接收 input/output 的表驱动 helper，先写三种输入的 RED：首次点火、199→完成、空输出创建正确产物、同产物未满累加、冲突产物与满产物整机暂停且 Progress/Burn/items 全不变。
- [ ] 4.2 写栏位移动 RED：从输入格移除一种材料或加入另一种材料时 `ProgressTicks` 归零；改变同种输入数量不清零；两种情况下 `BurnTicks` 都保留。
- [ ] 4.3 运行 RED：

```bash
zsh -ic 'go test ./internal/sim -run "Furnace.*Material|Furnace.*InputKind|FurnaceProduces" -count=1'
```

- [ ] 4.4 最小修改 `canSmelt`/完成产出：只调用一次 `core.SmeltingOutput(furnace.Input.Item)` 得到 `output`；输出必须为空或同 item 且 `<64`；完成时写入该 `output`。保持点火、tick 顺序和 active interest 不变。
- [ ] 4.5 在 `setFurnaceViewSlot` 的 input case 保存旧 `Item`，新 stack 通过 `SmeltingOutput` 或空值校验后赋值；仅当 `oldItem != stack.Item` 时 `ProgressTicks=0`，不改 `BurnTicks`。output case 接受固定三种产物但仍不能作为 move 目标。
- [ ] 4.6 GREEN 与两个 mutation：硬编码 IronIngot 时 sand/clay 产出失败；删除 input-kind reset 时切换测试失败。恢复后运行：

```bash
gofmt -w internal/sim/furnace.go internal/sim/furnace_test.go internal/sim/furnace_inventory_test.go
zsh -ic 'go test ./internal/sim -race -count=1'
zsh -ic 'go test ./internal/sim -run ^$ -bench BenchmarkAdvanceFurnaces6400 -benchmem -count=5'
git diff --check
```

Expected: 正确性全绿；benchmark 记录中位数与 allocs，不以快慢裁决，`TestAdvanceFurnacesDoesNotAllocate` 仍必须是 0 分配。

- [ ] 4.7 勾选并提交：

```bash
git add internal/sim/furnace.go internal/sim/furnace_test.go internal/sim/furnace_inventory_test.go openspec/changes/material-processing-loop/tasks.md
git commit -m "feat: 权威熔炼玻璃与砖块"
```

---

## Task 5: 锁定存档、重启与 Memory/TCP 一致性

**Files:**
- Modify: `internal/storage/chunk_furnace_test.go`
- Modify: `internal/client/furnace_test.go`
- Modify: `internal/server/furnace_publication_test.go`
- Create: `internal/server/material_processing_integration_test.go`

- [ ] 5.1 storage fixture 同时放入一个 Sand→Glass 和一个 Clay→Brick 熔炉，断言 schema v8 编解码保留 item/count/Progress/Burn/revision，既有 v1..v7 migration 与 future schema 拒绝不变。
- [ ] 5.2 client mirror 表驱动接受三种完整 FurnaceState，非法组合仍整包拒绝且不覆盖旧镜像。
- [ ] 5.3 在 `material_processing_integration_test.go` 复用现有 Memory/TCP 脚本装配，不新建 transport 抽象；相同 seed/初始背包依次合成木板、把 sand 熔成 glass、切换 clay 清零进度并熔成 brick，比较最终 inventory、furnace、chunk revision 与持久化 hash。
- [ ] 5.4 运行：

```bash
gofmt -w internal/storage/chunk_furnace_test.go internal/client/furnace_test.go internal/server/furnace_publication_test.go internal/server/material_processing_integration_test.go
zsh -ic 'go test ./internal/storage ./internal/client -run "Furnace|ChunkV|Future" -race -count=1'
zsh -ic 'go test ./internal/server -run "MaterialProcessing|FurnaceRestart" -race -count=1'
zsh -ic 'go test ./internal/server -race -count=1'
git diff --check
```

- [ ] 5.5 勾选并提交：

```bash
git add internal/storage/chunk_furnace_test.go internal/client/furnace_test.go internal/server/furnace_publication_test.go internal/server/material_processing_integration_test.go openspec/changes/material-processing-loop/tasks.md
git commit -m "test: 验证材料加工跨传输持久化"
```

---

## Task 6: 把普通背包固定配方扩为七行

**Files:**
- Modify: `internal/render/hotbar.go`
- Modify: `internal/render/hotbar_test.go`
- Modify: `cmd/mcgo/app_test.go`
- Modify: `cmd/mcgo/capture.go`
- Modify: `cmd/mcgo/testdata/golden/inventory-crafting.png`

- [ ] 6.1 写 RED：固定数组末尾是 `RecipeOakPlanks`；行 Y 为 `{420,368,316,264,212,160,108}`；item pair 末项 `{OakLog,OakPlanks}`；新按钮中心 `(513,109)` 命中 ID 7；空背包 recipe glyph 为 16；640×360 全部 quads/glyphs 在屏内；最坏固定容量无 overflow/预热后零分配。
- [ ] 6.2 运行 RED：

```bash
zsh -ic 'go test ./internal/render ./cmd/mcgo -run "Recipe|InventoryLayout|HUD.*640|Craft" -count=1'
```

- [ ] 6.3 最小机械更新：

```go
recipeQuads  = 1 + 7*9
recipeGlyphs = 16
openHUDHeight = float32(618)
```

把 `RecipeOakPlanks` 追加到 `inventoryRecipeIDs`；注释改为七条。不要引入滚动、分页或自适应目录。
- [ ] 6.4 在 `inventory-crafting` fixture 的下一个空背包格加入 `ItemOakLog, Count:1`，让新行可合成；不得改变相机、其他场景或抓帧阈值。
- [ ] 6.5 运行 GREEN 和无窗口 golden：

```bash
gofmt -w internal/render/hotbar.go internal/render/hotbar_test.go cmd/mcgo/app_test.go cmd/mcgo/capture.go
zsh -ic 'go test ./internal/render ./cmd/mcgo -race -count=1'
zsh -ic 'make visual-update VISUAL_OUT=/private/tmp/material-processing-visual-update'
test "$(git diff --name-only -- cmd/mcgo/testdata/golden | tr -d '\n')" = "cmd/mcgo/testdata/golden/inventory-crafting.png"
zsh -ic 'make visual-check VISUAL_OUT=/private/tmp/material-processing-visual-check'
git diff --check
```

Expected: 只改 `inventory-crafting.png`；检查全绿。人工只读查看该 PNG，确认七行完整、末行原木→木板、没有重叠或裁切。

- [ ] 6.6 勾选并提交：

```bash
git add internal/render/hotbar.go internal/render/hotbar_test.go cmd/mcgo/app_test.go cmd/mcgo/capture.go cmd/mcgo/testdata/golden/inventory-crafting.png openspec/changes/material-processing-loop/tasks.md
git commit -m "feat: 展示七条固定合成配方"
```

---

## Task 7: 全仓验证、归档与独立 PR

**Files:**
- Modify: `openspec/changes/material-processing-loop/tasks.md`
- Sync: `openspec/specs/authoritative-crafting/spec.md`
- Sync: `openspec/specs/authoritative-furnaces/spec.md`
- Sync: `openspec/specs/voxel-visual-presentation/spec.md`
- Archive: `openspec/changes/archive/2026-08-09-material-processing-loop/**`

- [ ] 7.1 完整门禁：

```bash
zsh -ic 'go test ./internal/core ./internal/world ./internal/sim ./internal/network ./internal/storage ./internal/client ./internal/render ./internal/server ./cmd/mcgo -race -count=1'
zsh -ic 'go test ./internal/archcheck -count=1'
zsh -ic 'go test ./... -race'
zsh -ic 'go vet ./...'
zsh -ic 'go test ./internal/sim -run ^$ -bench BenchmarkAdvanceFurnaces6400 -benchmem -count=5'
zsh -ic 'make visual-check VISUAL_OUT=/private/tmp/material-processing-final-visual'
gofmt -l .
git diff --check
openspec validate --all --strict --no-interactive
```

Expected: 全部成功、gofmt 无输出；benchmark 只记录，不比较快慢。

- [ ] 7.2 请求独立代码评审并修复本 change 范围 finding；再次运行受影响 race/visual/OpenSpec。
- [ ] 7.3 同步 delta、确认 tasks 全勾并归档：

```bash
openspec archive material-processing-loop --yes
openspec validate --all --strict --no-interactive
test -z "$(openspec list --json | grep material-processing-loop || true)"
git diff --check
```

- [ ] 7.4 提交、推送并创建 PR：

```bash
git add internal/core internal/world internal/sim internal/network internal/storage internal/client internal/render internal/server cmd/mcgo openspec
git commit -m "docs: 归档基础材料加工闭环"
git push -u origin codex/material-processing-loop
gh pr create --base main --head codex/material-processing-loop --title "feat: 补齐基础材料加工闭环" --body-file /private/tmp/material-processing-loop-pr.md
```

- [ ] 7.5 PR 描述明确 RecipeID 7、三条熔炼映射、输入切换计时语义、版本全部不变、只有 `inventory-crafting` golden 变化及完整验证证据。
