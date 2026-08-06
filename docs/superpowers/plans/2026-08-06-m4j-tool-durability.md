# M4J 工具耐久 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让石镐与铁镐带有耐久，成功破坏方块时消耗一点，耗尽后变为对应的损坏物品，并让耐久随工具走遍背包、掉落物、存档与 Memory/TCP 协议。

**Architecture:** 耐久是 `core.ItemStack` 的第三个字段，由 `core` 的两张纯查表（耐久上限、损坏形态）定义语义。`sim.completeMining` 在全部预检通过、方块实际移除之后扣减，归零时把栏位整体替换为损坏物品并复用既有 `inventoryDirty` 发布路径。所有携带 `ItemStack` 的编码点统一改用一对共享的编解码函数，避免某条路径漏带耐久。

**Tech Stack:** Go 1.26、现有 `core.Inventory` / `world.Chunk` / `sim.Engine`、Memory/TCP binary codec、WebGPU 快捷栏渲染、OpenSpec、Go race/fuzz/benchmark。

## Global Constraints

- 以 `docs/superpowers/specs/2026-08-06-m4j-tool-durability-design.md` 为规范来源。二者不一致时先更新设计文档与本计划，再写代码。
- 起点基线必须是：协议 v10、玩家 schema v3、区块 schema v4、metadata v2、benchmark scenario v12，且 M4H 已归档。
- 全程通过 `zsh -ic 'gvm use go1.26.0 >/dev/null && ...'` 使用现有 Go 1.26，不下载工具链、不新增第三方依赖。
- 每个实现任务遵循 RED → GREEN → 定向 race → archcheck → gofmt/diff check → 独立提交；只有验证通过后才勾选。
- 自动验证不得启动或聚焦前台游戏窗口。
- 保留用户既有的未跟踪文件与无关改动；只暂存当前任务的文件。
- 不新增内部包、接口、worker、goroutine、队列、锁或第三方依赖。
- 不实现工具修复、附魔、损坏回收、耐久影响采掘速度、新工具材质等级。
- 物品 ID 只能追加到 `iota` 末尾，绝不插入既有值中间。
- 既有 packet ID 不重排、不复用；废止的 Play client packet ID `1` 保持未分配。
- **benchmark scenario 保持 v12**，不生成、提升或放宽任何性能基线；`docs/notes/perf-baseline.json` 与 `docs/notes/perf-baseline-m5.json` 字节不得改变。
- 新增或修改的注释、测试说明与开发文档使用中文；Go 标识符、wire 字段与既有技术术语保留英文。
- Hook 或门禁失败只修根因，不改写、关闭或绕过 `scripts/agent-hooks/guard.mjs`。

---

## Files and Stable Interface Map

### 物品语义（`core`）

Modify:
- `internal/core/item.go`
- `internal/core/item_test.go`
- `internal/core/recipe.go`（`Recipe(id) (CraftingRecipe, bool)`，字段为 `Input` / `Output`）
- `internal/core/recipe_test.go`

Stable additions:

```go
package core

// 追加在 ItemID 的 iota 末尾，位于 ItemIronPickaxe 之后
const (
    ItemBrokenStonePickaxe
    ItemBrokenIronPickaxe
)

type ItemStack struct {
    Item       ItemID
    Count      uint8
    Durability uint16
}

// ItemMaxDurability 返回工具的耐久上限；没有耐久的物品返回 0, false。
func ItemMaxDurability(item ItemID) (uint16, bool)

// ItemBrokenForm 返回工具耗尽后的损坏形态；不会损坏的物品返回 ItemNone, false。
func ItemBrokenForm(item ItemID) (ItemID, bool)
```

耐久上限：`ItemStonePickaxe` 为 `131`，`ItemIronPickaxe` 为 `250`。
`ItemStackLimit` 追加 `ItemBrokenStonePickaxe, ItemBrokenIronPickaxe` 到返回 `1` 的分支。
`ItemStack.Valid()` 追加一条：没有耐久上限的物品其 `Durability` 必须为 `0`；有上限的物品其 `Durability` 必须在 `1..上限`。

### 掉落物合并（`world`）

Modify:
- `internal/world/drop.go`
- `internal/world/drop_test.go`

`prepareDropSlot` 的合并判据由硬编码 `core.MaxStackCount` 改为查 `core.ItemStackLimit`。

### 权威转移（`sim`）

Modify:
- `internal/sim/mining.go`
- `internal/sim/mining_test.go`

复用既有测试辅助，不要另写驱动循环：`readyMiningPlayers(t, count) (*Engine, []SessionID, []core.BlockPos)`、`setMiningHeldItem(player, item)`、`advanceMiningOnce(engine) TickResult`、`fillMiningDrops(engine, target)`、`miningTargetRecord(t, engine, target) *ChunkRecord`、`miningDropTotals(chunk) map[core.ItemID]int`。

Stable addition:

```go
package sim

// consumeToolDurability 在成功破坏方块后扣减选中工具的耐久。
// 归零时把栏位整体替换为损坏形态，并返回 true 表示背包已变化。
func consumeToolDurability(player *playerState) bool
```

### 共享编解码（`network`）

Modify:
- `internal/network/codec.go`
- `internal/network/message.go`
- `internal/network/packet.go`
- 对应测试

Stable additions:

```go
package network

// encodeItemStack 是所有携带物品堆的消息共用的固定长度编码：
// Item(u16) + Count(u8) + Durability(u16)，共 5 字节。
func encodeItemStack(e *byteEncoder, stack core.ItemStack)

// decodeItemStack 是 encodeItemStack 的逆操作。
func decodeItemStack(d *byteDecoder) (core.ItemStack, error)

const ProtocolVersion uint32 = 11
```

`InventoryState`、`FurnaceState`、`ItemDrop` 三处编码全部改用这一对函数。

### 持久化（`storage`）

Modify:
- `internal/storage/player_codec.go`
- `internal/storage/player_migration.go`
- `internal/storage/chunk_codec.go`
- `internal/storage/migration.go`
- 对应测试与 `testdata`

Frozen values after this batch:

```text
protocol=11
player schema=4
chunk schema=5
metadata=2
benchmark scenario=12
```

### 快捷栏呈现（`render` / `cmd`）

Modify:
- `internal/render/hotbar.go`
- `internal/render/hotbar_test.go`

耐久条照既有 `appendMiningBar` 的模式实现，只新增 quad，不新增 pipeline 或纹理。呈现完全由 `layoutInventory` 从已有的 `core.Inventory` 推导，`cmd/mcgo` 无需改动。几何常量沿用 `hotbarSlotSize` / `hotbarSlotGap` / `inventorySlotOrigin`。

---

## Task 0: 建立 active OpenSpec change（已完成）

M4J 修改 `internal/network/codec.go`、`internal/network/packet.go`、`internal/storage/player_codec.go`、`internal/storage/chunk_codec.go`、`internal/storage/player_migration.go`，全部命中 `scripts/agent-hooks/guard.mjs` 的 `highRiskPatterns`。没有 active change 时，Stop hook 会从第一个 Go 提交起持续失败，且**不得**用 `MINECRAFT_GO_HOOKS_ALLOW_NO_SPEC=1` 绕过。因此这必须是第一个任务。

**Files:**
- Create: `openspec/changes/m4j-tool-durability/proposal.md`
- Create: `openspec/changes/m4j-tool-durability/design.md`
- Create: `openspec/changes/m4j-tool-durability/tasks.md`
- Create: `openspec/changes/m4j-tool-durability/specs/tool-durability/spec.md`

**Interfaces:**
- Consumes: `openspec/config.yaml` 的 rules；`docs/superpowers/specs/2026-08-06-m4j-tool-durability-design.md`。
- Produces: 一个通过 strict 校验的 active change，解除后续所有任务的 Stop hook 阻塞。

- [x] **Step 1: 参照已归档的 M4H 结构**

```bash
find openspec/changes/archive/2026-08-05-m4h-authoritative-item-dropping -type f
sed -n '1,80p' openspec/changes/archive/2026-08-05-m4h-authoritative-item-dropping/proposal.md
sed -n '1,60p' openspec/changes/archive/2026-08-05-m4h-authoritative-item-dropping/specs/authoritative-item-dropping/spec.md
cat openspec/config.yaml
```

按 `openspec/config.yaml` 的 `rules` 撰写，全部使用中文，保留 OpenSpec 要求的英文结构标题与 SHALL/MUST。

- [x] **Step 2: 写 proposal.md**

背景、目标、非目标直接取自设计文档的对应章节。必须明确写出兼容性影响：协议 v10→v11、玩家 schema v3→v4、区块 schema v4→v5、metadata 保持 v2、benchmark scenario 保持 v12 且不重建性能基线；旧存档单向无损升级（旧工具视为满耐久），回退到 v10 程序必须恢复升级前的备份。

- [x] **Step 3: 写 delta spec**

`specs/tool-durability/spec.md` 只描述可观察行为，每条 Requirement 至少一个 Given/When/Then Scenario。至少覆盖：

- 工具带耐久，成功破坏方块消耗一点
- 三条拒绝路径（受保护方块、区块未就绪、掉落物容量已满）不消耗耐久
- 耐久归零转为损坏物品，且最后一次破坏仍然生效
- 损坏物品的采掘行为等同空手
- 工具离手中断采掘进度
- 耐久跨背包、掉落物、存档与 Memory/TCP 协议无损传递
- 掉落物合并遵守单格上限（工具永不合并）
- 旧存档迁移把旧工具视为满耐久

实现选择（字段布局、查表函数、编码字节数）**不要**写进 spec.md，放 design.md。

- [x] **Step 4: 写 design.md 与 tasks.md**

`design.md` 记录数据所有权、依赖方向、受影响文件、迁移与回退方案，以及设计文档里三个被否决的替代方案（并行耐久表、耐久只存背包、耐久编码进 `Count` 高位）及其理由。

`tasks.md` 按本计划的 Task 1..9 逐条列出，每项写明目标包与验证命令。收尾任务必须包含 `gofmt`、`go vet ./...`、`go test ./... -race` 与 OpenSpec 严格校验。

- [x] **Step 5: 校验**

```bash
openspec validate --all --strict --no-interactive
```

期望：退出码为零，新 change 被识别为完整 active change。

- [x] **Step 6: 提交**

```bash
git add openspec/changes/m4j-tool-durability
git commit -m "spec: 建立 M4J 工具耐久 change"
```

---

## Task 1: 冻结起点并核对契约（已完成）

**Files:**
- Verify: `internal/network/packet.go`
- Verify: `internal/storage/player_codec.go`
- Verify: `internal/storage/chunk_codec.go`
- Verify: `internal/storage/metadata.go`
- Verify: `cmd/mcgo/benchmark.go`
- Verify: `docs/notes/perf-baseline-m5.json`

**Interfaces:**
- Consumes: 已归档的 M4H 主规格。
- Produces: 一个干净、已验证的起点；无代码改动。

- [x] **Step 1: 核对五个冻结值与归档状态**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go version'
openspec validate --all --strict --no-interactive
test -d openspec/changes/archive/2026-08-05-m4h-authoritative-item-dropping && echo "M4H 已归档"
rg -n 'ProtocolVersion uint32 = 10|currentPlayerSchema.*= 3|currentChunkSchema.*= 4|currentMetadataVersion.*= 2|scenarioVersion[[:space:]]*= 12' internal cmd | rg -v '_test'
jq -r '.scenario_version' docs/notes/perf-baseline-m5.json
git status --short --branch
```

期望：strict 校验通过；M4H 归档存在；五个值分别为 10、3、4、2、12；`scenario_version` 为 12；tracked 工作树没有无关改动。任一条不满足就停下来先更新计划。

- [x] **Step 2: 记录两条 baseline 的哈希，供最后一个任务比对**

```bash
shasum -a 256 docs/notes/perf-baseline.json docs/notes/perf-baseline-m5.json
```

把输出抄进本任务的执行记录。Task 9 会再算一次并要求完全一致。

- [x] **Step 3: 通读设计文档，确认范围**

```bash
sed -n '1,200p' docs/superpowers/specs/2026-08-06-m4j-tool-durability-design.md
```

确认只做：耐久字段、两个损坏物品 ID、成功破坏才扣、三处 schema 升版、快捷栏耐久条、`prepareDropSlot` 缺陷修复。出现工具修复、耐久影响速度、新材质等级、scenario 升级就停下。

- [x] **Step 4: 只读任务，无需提交**

---

## Task 2: 修复掉落物合并不遵守单格上限（已完成）

这是耐久的阻塞性前置：两把不同耐久的镐一旦合并，耐久必然丢失。它本身也是 M4H 遗留的真实缺陷，独立成一个提交。

**Files:**
- Modify: `internal/world/drop.go`
- Modify: `internal/world/drop_test.go`

**Interfaces:**
- Consumes: 既有 `core.ItemStackLimit(item) (uint8, bool)`。
- Produces: `prepareDropSlot` 遵守单格上限；工具从此永不合并。

- [x] **Step 1: 写失败测试**

追加到 `internal/world/drop_test.go`：

```go
func TestChunkPrepareDropRespectsPerItemStackLimit(t *testing.T) {
	chunk := dropTestChunk(t)
	index := dropTestIndex(t, core.BlockPos{X: 16, Y: 3, Z: -32})
	// 镐的单格上限是 1，已有一把时不得再合并进同一槽。
	chunk.SetDrop(0, world.DropSlot{
		Generation: 5, Active: true,
		Stack:      core.ItemStack{Item: core.ItemStonePickaxe, Count: 1},
		BlockIndex: index,
	})

	slot, ok := chunk.PrepareDrop(core.ItemStonePickaxe, index)
	if !ok {
		t.Fatal("第二把镐应当占用新槽而不是被拒绝")
	}
	if slot == 0 {
		t.Fatal("第二把镐被合并进了已满的槽，违反单格上限 1")
	}

	chunk.CommitDrop(slot, core.ItemStonePickaxe, index, 10)
	if got := chunk.Drop(0).Stack.Count; got != 1 {
		t.Fatalf("原槽数量 = %d，想要保持 1", got)
	}
	if got := chunk.Drop(slot).Stack.Count; got != 1 {
		t.Fatalf("新槽数量 = %d，想要 1", got)
	}
}

func TestChunkPrepareDropStillMergesStackableItems(t *testing.T) {
	chunk := dropTestChunk(t)
	index := dropTestIndex(t, core.BlockPos{X: 16, Y: 3, Z: -32})
	// 可堆叠物品的合并行为不得被本次修复改变。
	chunk.SetDrop(0, world.DropSlot{
		Generation: 5, Active: true,
		Stack:      core.ItemStack{Item: core.ItemDirt, Count: 63},
		BlockIndex: index,
	})

	slot, ok := chunk.PrepareDrop(core.ItemDirt, index)
	if !ok || slot != 0 {
		t.Fatalf("可堆叠物品预检 = (%d,%v)，想要 (0,true)", slot, ok)
	}
	chunk.CommitDrop(slot, core.ItemDirt, index, 10)
	if got := chunk.Drop(0).Stack.Count; got != core.MaxStackCount {
		t.Fatalf("合并后数量 = %d，想要 64", got)
	}
}
```

- [x] **Step 2: 运行，确认第一个测试失败**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/world -run "PrepareDropRespectsPerItemStackLimit|PrepareDropStillMergesStackableItems" -count=1'
```

期望：`TestChunkPrepareDropRespectsPerItemStackLimit` 失败，信息为「第二把镐被合并进了已满的槽」；`TestChunkPrepareDropStillMergesStackableItems` 通过。

- [x] **Step 3: 改判据**

`internal/world/drop.go` 的 `prepareDropSlot`，把硬编码上限换成查表：

```go
func prepareDropSlot(
	drops [core.DropsPerChunk]DropSlot,
	item core.ItemID,
	blockIndex uint32,
) (int, bool) {
	// 合并必须遵守该物品自己的单格上限：工具上限为 1，因此永不合并。
	limit, ok := core.ItemStackLimit(item)
	if !ok {
		return 0, false
	}
	for slot := range drops {
		drop := drops[slot]
		if drop.Active && drop.Stack.Item == item && drop.BlockIndex == blockIndex &&
			drop.Stack.Count < limit {
			return slot, true
		}
	}
	for slot := range drops {
		if !drops[slot].Active && drops[slot].Generation != math.MaxUint32 {
			return slot, true
		}
	}
	return 0, false
}
```

- [x] **Step 4: 检查 `PrepareDropBatch` 的同一判据**

```bash
rg -n 'MaxStackCount' internal/world/drop.go
```

`PrepareDropBatch` 内的 `space := core.MaxStackCount - drop.Stack.Count` 同样要改为按物品上限计算：

```go
space := limit - drop.Stack.Count
```

其中 `limit` 由该批次物品的 `core.ItemStackLimit` 得到。若该函数目前没有 `limit` 变量，在循环外取一次。

- [x] **Step 5: 运行验证**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/world ./internal/sim ./internal/server -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && gofmt -l .'
git diff --check
```

期望：全部通过；`gofmt -l .` 无输出。

- [x] **Step 6: 提交**

```bash
git add internal/world/drop.go internal/world/drop_test.go
git commit -m "fix: 掉落物合并遵守单格上限"
```

---

## Task 3: 在 core 定义耐久语义（已完成并通过四轮审查修复）

> 实际实现还补齐了背包/掉落物/快照投影的耐久保真、v10/v3/v4 的过渡门禁，以及 M4H 遗留多件工具堆的确定性拆分与重写标记。对应提交为 `c37611f`、`4d4e61b`、`234bf09`、`3a7a84a`、`e5c51f6`；最终复审 clean。

**Files:**
- Modify: `internal/core/item.go`
- Modify: `internal/core/item_test.go`

**Interfaces:**
- Consumes: 既有 `ItemStackLimit`、`RegisteredItem`。
- Produces: `ItemStack.Durability uint16`；`ItemMaxDurability(ItemID) (uint16, bool)`；`ItemBrokenForm(ItemID) (ItemID, bool)`；两个新物品 ID；收紧后的 `ItemStack.Valid()`。

- [x] **Step 1: 写失败测试**

追加到 `internal/core/item_test.go`：

```go
func TestItemMaxDurabilityCoversToolsOnly(t *testing.T) {
	for _, test := range []struct {
		item core.ItemID
		want uint16
	}{
		{core.ItemStonePickaxe, 131},
		{core.ItemIronPickaxe, 250},
	} {
		got, ok := core.ItemMaxDurability(test.item)
		if !ok || got != test.want {
			t.Fatalf("物品 %d 耐久上限 = (%d,%v)，想要 (%d,true)", test.item, got, ok, test.want)
		}
	}
	// 非工具、损坏物品与未注册物品都没有耐久上限。
	for _, item := range []core.ItemID{
		core.ItemStone, core.ItemCoal, core.ItemIronIngot,
		core.ItemBrokenStonePickaxe, core.ItemBrokenIronPickaxe,
		core.ItemNone,
	} {
		if got, ok := core.ItemMaxDurability(item); ok || got != 0 {
			t.Fatalf("物品 %d 耐久上限 = (%d,%v)，想要 (0,false)", item, got, ok)
		}
	}
}

func TestItemBrokenFormMapsEachTool(t *testing.T) {
	for _, test := range []struct {
		item core.ItemID
		want core.ItemID
	}{
		{core.ItemStonePickaxe, core.ItemBrokenStonePickaxe},
		{core.ItemIronPickaxe, core.ItemBrokenIronPickaxe},
	} {
		got, ok := core.ItemBrokenForm(test.item)
		if !ok || got != test.want {
			t.Fatalf("物品 %d 损坏形态 = (%d,%v)，想要 (%d,true)", test.item, got, ok, test.want)
		}
	}
	// 损坏物品不会再损坏一次。
	for _, item := range []core.ItemID{
		core.ItemStone, core.ItemBrokenStonePickaxe, core.ItemBrokenIronPickaxe,
	} {
		if got, ok := core.ItemBrokenForm(item); ok || got != core.ItemNone {
			t.Fatalf("物品 %d 损坏形态 = (%d,%v)，想要 (ItemNone,false)", item, got, ok)
		}
	}
}

func TestBrokenToolsAreRegisteredAndUnstackable(t *testing.T) {
	for _, item := range []core.ItemID{
		core.ItemBrokenStonePickaxe, core.ItemBrokenIronPickaxe,
	} {
		if !core.RegisteredItem(item) {
			t.Fatalf("损坏物品 %d 未注册", item)
		}
		if limit, ok := core.ItemStackLimit(item); !ok || limit != 1 {
			t.Fatalf("损坏物品 %d 单格上限 = (%d,%v)，想要 (1,true)", item, limit, ok)
		}
	}
}

func TestItemStackValidEnforcesDurabilityDomain(t *testing.T) {
	// 有耐久上限的物品：耐久必须落在 1..上限。
	full, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	for _, test := range []struct {
		name  string
		stack core.ItemStack
		want  bool
	}{
		{"满耐久工具", core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: full}, true},
		{"半耐久工具", core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: 1}, true},
		{"零耐久工具", core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: 0}, false},
		{"超上限工具", core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: full + 1}, false},
		{"非工具带耐久", core.ItemStack{Item: core.ItemStone, Count: 1, Durability: 1}, false},
		{"非工具零耐久", core.ItemStack{Item: core.ItemStone, Count: 1}, true},
		{"损坏物品带耐久", core.ItemStack{Item: core.ItemBrokenStonePickaxe, Count: 1, Durability: 1}, false},
		{"损坏物品零耐久", core.ItemStack{Item: core.ItemBrokenStonePickaxe, Count: 1}, true},
		{"空栏位", core.ItemStack{}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.stack.Valid(); got != test.want {
				t.Fatalf("Valid() = %v，想要 %v", got, test.want)
			}
		})
	}
}
```

- [x] **Step 2: 运行，确认编译失败**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/core -run "Durability|BrokenForm|BrokenTools" -count=1'
```

期望：编译失败，提示 `ItemBrokenStonePickaxe`、`ItemMaxDurability`、`ItemBrokenForm` 与 `Durability` 字段未定义。

- [x] **Step 3: 追加物品 ID 与字段**

`internal/core/item.go`，在 `ItemIronPickaxe` 之后**追加**（不得插入中间）：

```go
	ItemStonePickaxe
	ItemIronPickaxe
	// 以下是工具耐久耗尽后的形态，只能追加在末尾：
	// 插入会平移后续物品 ID，破坏既有存档与线上字节。
	ItemBrokenStonePickaxe
	ItemBrokenIronPickaxe
)
```

`ItemStack` 追加字段：

```go
// ItemStack 是一个快捷栏栏位的值；空栏位是零值。
type ItemStack struct {
	Item  ItemID
	Count uint8
	// Durability 只对有耐久上限的工具有意义，其余物品恒为 0。
	Durability uint16
}
```

- [x] **Step 4: 加两张查表并收紧 Valid**

```go
// ItemMaxDurability 返回工具的耐久上限；没有耐久的物品返回 0 与 false。
func ItemMaxDurability(item ItemID) (uint16, bool) {
	switch item {
	case ItemStonePickaxe:
		return 131, true
	case ItemIronPickaxe:
		return 250, true
	default:
		return 0, false
	}
}

// ItemBrokenForm 返回工具耐久耗尽后的形态；不会损坏的物品返回 ItemNone 与 false。
func ItemBrokenForm(item ItemID) (ItemID, bool) {
	switch item {
	case ItemStonePickaxe:
		return ItemBrokenStonePickaxe, true
	case ItemIronPickaxe:
		return ItemBrokenIronPickaxe, true
	default:
		return ItemNone, false
	}
}
```

`ItemStackLimit` 的上限 1 分支追加两个损坏物品：

```go
	case ItemStonePickaxe, ItemIronPickaxe,
		ItemBrokenStonePickaxe, ItemBrokenIronPickaxe:
		return 1, true
```

`ItemStack.Valid()` 追加耐久域校验：

```go
func (s ItemStack) Valid() bool {
	limit, ok := ItemStackLimit(s.Item)
	if !ok {
		return s.Item == ItemNone && s.Count == 0 && s.Durability == 0
	}
	if s.Count == 0 || s.Count > limit {
		return false
	}
	maxDurability, hasDurability := ItemMaxDurability(s.Item)
	if !hasDurability {
		// 没有耐久概念的物品必须保持零值，否则同物品的两个栈会因无意义字段拒绝合并。
		return s.Durability == 0
	}
	return s.Durability >= 1 && s.Durability <= maxDurability
}
```

- [x] **Step 5: 运行验证**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/core -race -count=1'
```

期望：通过。若既有测试因为构造了 `ItemStack{Item: ItemStonePickaxe, Count: 1}`（耐久为 0）而失败，这是本次收紧的**预期结果**——把它们改为携带满耐久，不要放宽 `Valid`。

- [x] **Step 6: 在本任务内修完全仓的工具构造点**

`ItemStack` 加字段不破坏编译（字段有零值），但 `Valid()` 收紧后，**任何构造工具栈却不给耐久的地方都成了非法值**。这些点散落在四个包里，不能推迟到后续任务——本计划的全局约束要求每个任务独立通过验证。

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go build ./... && go test ./... -count=1 2>&1 | rg -v '^ok' | head -40'
rg -n 'ItemStonePickaxe|ItemIronPickaxe' --type go | rg -v 'internal/core/item(_test)?\.go'
```

已知需要补 `Durability` 的位置（数字是该文件的引用数，不是待改行数）：`internal/sim/mining_test.go`(17)、`internal/core/inventory_test.go`(10)、`internal/sim/drop_test.go`(4)、`internal/server/tcp_integration_test.go`(3)、`internal/storage/player_codec_test.go`(2)、`internal/sim/furnace_inventory_test.go`(2)、`internal/server/integration_test.go`(2)、`internal/network/drop_test.go`(2)、`internal/core/recipe_test.go`(2)。

统一改法是给构造出的镐补满耐久：

```go
	full, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	stack := core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: full}
```

**不要**为了让这些测试通过而放宽 `Valid()`——收紧本身就是本任务的交付物。若某个测试是**故意**构造非法栈来验证拒绝路径，保持它不变并确认它断言的是「被拒绝」。

`internal/sim/mining_test.go` 的 `setMiningHeldItem` 是集中构造点，改它一处即可覆盖该文件的大部分引用（具体见 Task 7 Step 1，两处任选其一先做，另一处届时确认已生效即可）。

- [x] **Step 7: 运行全仓验证**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race'
zsh -ic 'gvm use go1.26.0 >/dev/null && go vet ./...'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && gofmt -l .'
git diff --check
```

期望：全部通过。本任务是唯一一个需要全仓测试的中间任务——`Valid()` 的收紧影响面就是全仓。

- [x] **Step 8: 提交**

```bash
git add -u
git commit -m "feat: 定义工具耐久与损坏形态"
```

只用 `git add -u` 暂存已跟踪文件的改动，避免带入用户的未跟踪文件。

---

## Task 4: 合成产出满耐久（已折叠进 Task 3 审查修复）

> `AddStack` 改为严格校验完整物品堆后，零耐久配方会让工具合成失效；因此本任务作为 Task 3 Important finding 的必要依赖在 `4d4e61b` 中完成，并已随 Task 3 一起复审。

**Files:**
- Modify: `internal/core/recipe.go`
- Modify: `internal/core/recipe_test.go`

**Interfaces:**
- Consumes: `ItemMaxDurability`。
- Produces: 合成是耐久的唯一来源；两条镐配方产出满耐久工具。

- [x] **Step 1: 写失败测试**

追加到 `internal/core/recipe_test.go`：

```go
func TestPickaxeRecipesOutputFullDurability(t *testing.T) {
	for _, test := range []struct {
		recipe core.RecipeID
		item   core.ItemID
	}{
		{core.RecipeStonePickaxe, core.ItemStonePickaxe},
		{core.RecipeIronPickaxe, core.ItemIronPickaxe},
	} {
		recipe, ok := core.Recipe(test.recipe)
		if !ok {
			t.Fatalf("配方 %d 不存在", test.recipe)
		}
		want, _ := core.ItemMaxDurability(test.item)
		if recipe.Output.Item != test.item || recipe.Output.Count != 1 {
			t.Fatalf("配方产出 = %+v，想要一个 %d", recipe.Output, test.item)
		}
		if recipe.Output.Durability != want {
			t.Fatalf("配方产出耐久 = %d，想要满耐久 %d", recipe.Output.Durability, want)
		}
		if !recipe.Output.Valid() {
			t.Fatalf("配方产出不是合法物品堆：%+v", recipe.Output)
		}
	}
}

func TestNonToolRecipesOutputZeroDurability(t *testing.T) {
	for _, id := range []core.RecipeID{
		core.RecipeStoneBricks, core.RecipeFurnace, core.RecipeIronBlock,
	} {
		recipe, ok := core.Recipe(id)
		if !ok {
			t.Fatalf("配方 %d 不存在", id)
		}
		if recipe.Output.Durability != 0 {
			t.Fatalf("非工具配方 %d 产出带耐久 %d", id, recipe.Output.Durability)
		}
	}
}
```

- [x] **Step 2: 运行，确认失败**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/core -run "RecipesOutput" -count=1'
```

期望：`TestPickaxeRecipesOutputFullDurability` 失败，产出耐久为 0 而非 131/250。

- [x] **Step 3: 让两条镐配方带满耐久**

`internal/core/recipe.go` 的 `Recipe`（返回 `CraftingRecipe`）：

```go
	case RecipeStonePickaxe:
		return CraftingRecipe{
			Input:  ItemStack{Item: ItemStone, Count: 3},
			Output: ItemStack{Item: ItemStonePickaxe, Count: 1, Durability: 131},
		}, true
	case RecipeIronPickaxe:
		return CraftingRecipe{
			Input:  ItemStack{Item: ItemIronIngot, Count: 3},
			Output: ItemStack{Item: ItemIronPickaxe, Count: 1, Durability: 250},
		}, true
```

耐久值直接写字面量以保持配方表是纯数据。

- [x] **Step 4: 运行验证**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/core ./internal/sim -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && gofmt -l .'
git diff --check
```

期望：全部通过。`internal/sim` 的合成测试若断言产出等于 `ItemStack{Item: ItemStonePickaxe, Count: 1}`，把期望改为携带满耐久。

- [x] **Step 5: 提交**

```bash
git add internal/core/recipe.go internal/core/recipe_test.go internal/sim
git commit -m "feat: 合成产出满耐久工具"
```

---

## Task 5: 协议 v11 与共享物品堆编解码

**Files:**
- Modify: `internal/network/codec.go`
- Modify: `internal/network/message.go`
- Modify: `internal/network/packet.go`
- Modify: `internal/network/codec_test.go`
- Modify: `internal/network/worldtime_test.go`
- Modify: `internal/network/packet_test.go`
- Modify: `internal/network/drop_test.go`

**Interfaces:**
- Consumes: `core.ItemStack`、`core.ItemMaxDurability`。
- Produces: `ProtocolVersion == 11`；`encodeItemStack` / `decodeItemStack`；三处消息统一 5 字节物品堆编码。

- [ ] **Step 1: 写失败测试**

先把版本相关的 golden 从 `0a` 改到 `0b`。在 `internal/network/codec_test.go` 中：

```go
{"hello", StateHandshake, ClientHello{ProtocolVersion: 11}, 0, "0b"},
```

```go
{"server hello", StateHandshake, ServerHello{ProtocolVersion: 11}, 0, "0b"},
{"handshake reject", StateHandshake, HandshakeReject{ServerProtocolVersion: 11, Code: HandshakeVersionMismatch, Message: "no"}, 1, "0b01026e6f"},
```

`internal/network/worldtime_test.go` 中把版本断言与拒绝矩阵推进一版：

把 `TestProtocolVersionIsTen`（`worldtime_test.go:10`）**改名并改值**，不要新增一个并存的版本：

```go
func TestProtocolVersionIsEleven(t *testing.T) {
	if ProtocolVersion != 11 {
		t.Fatalf("协议版本 = %d，想要 11", ProtocolVersion)
	}
}
```

同样把 `TestProtocolV10RejectsVersionNineBeforePlay`（`worldtime_test.go:16`）改名为下面这个，注意失败信息里的 "想要 v9" 也要跟着改成 v11：

```go
func TestProtocolV11RejectsVersionTenBeforePlay(t *testing.T) {
	// v10 是上一版本，必须和更早版本一样在 Handshake 阶段稳定拒绝。
	for _, version := range []uint32{1, 2, 3, 4, 5, 6, 7, 8, 9, 10} {
		stream := &staticClientHelloStream{version: version}
		if _, err := BeginServerLogin(t.Context(), stream); err == nil {
			t.Fatalf("v%d ClientHello 被接受", version)
		}
		reject, ok := stream.sent.(HandshakeReject)
		if !ok || reject.ServerProtocolVersion != ProtocolVersion ||
			reject.Code != HandshakeVersionMismatch {
			t.Fatalf("v%d 拒绝结果 = %#v，想要 v11 HandshakeReject", version, stream.sent)
		}
	}
}
```

`internal/network/packet_test.go` 中把 `if ProtocolVersion != 10` 改为 `!= 11`，失败信息同步。

再追加一条耐久往返测试到 `internal/network/drop_test.go`：

```go
func TestItemDropCarriesToolDurability(t *testing.T) {
	full, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	message := ItemDropUpserts{Drops: []ItemDrop{{
		ID: dropTestID(0, 1), BlockIndex: 9,
		Item: core.ItemStonePickaxe, Count: 1, Durability: full - 7,
	}}}
	if err := message.Validate(); err != nil {
		t.Fatalf("带耐久的工具掉落物被拒绝: %v", err)
	}
	packetID, payload, err := encodeServerControlPayload(StatePlay, message)
	if err != nil {
		t.Fatal(err)
	}
	round, err := decodeServerControlPayload(StatePlay, packetID, payload)
	if err != nil {
		t.Fatal(err)
	}
	got := round.(ItemDropUpserts).Drops[0]
	if got.Durability != full-7 {
		t.Fatalf("往返耐久 = %d，想要 %d", got.Durability, full-7)
	}
}

func TestItemDropRejectsDurabilityOnNonTool(t *testing.T) {
	// 伪造的 payload 不得让泥土带上耐久。
	message := ItemDropUpserts{Drops: []ItemDrop{{
		ID: dropTestID(0, 1), BlockIndex: 9,
		Item: core.ItemDirt, Count: 1, Durability: 5,
	}}}
	if err := message.Validate(); err == nil {
		t.Fatal("非工具带耐久被接受")
	}
}
```

- [ ] **Step 2: 运行，确认失败**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "ProtocolVersion|RejectsVersion|Durability" -count=1'
```

期望：版本断言失败（当前为 10），磨损的 `InventoryState` / `ItemDropUpserts` 仍被 v10 过渡门禁拒绝，证明 v11 尚未读取真实耐久字段。

- [ ] **Step 3: 升版本并解除 v10 过渡限制**

`internal/network/packet.go`：

```go
// ProtocolVersion 是 M4J 唯一支持的协议版本。
const ProtocolVersion uint32 = 11
```

`internal/network/message.go` 的 `ItemDrop.Durability`、`sim.DropSnapshot`、服务端发布镜像与客户端镜像已在 Task 3 审查修复中贯通，不再重复加字段。把 `ItemDrop.validate()` 当前这条 v10 限制删除：

```go
	if full, ok := core.ItemMaxDurability(drop.Item); ok && drop.Durability != full {
		return errors.New("network: protocol v10 item drop tool is not at full durability")
	}
```

完整 `core.ItemStack.Valid()` 域校验继续保留，因此非工具伪耐久、工具数量大于一和越界耐久仍会拒绝。v11 只解除“必须满耐久”，不能放宽真实物品堆校验。

- [ ] **Step 4: 加共享编解码并改三处调用点**

**先读现状——本步的起点与计划初稿的假设不同：**

`decodeItemStack` **已经存在**于 `internal/network/codec.go:677`，且已被 `InventoryState` 与 `FurnaceState` 的解码路径共用。它采用 error-threading 签名：

```go
func decodeItemStack(d *byteDecoder, err error) (core.ItemStack, error)
```

**不要**新建一个签名不同的同名函数，改造既有这个。编码侧则相反：没有共享函数，`e.u16(uint16(stack.Item)); e.u8(stack.Count)` 在四处内联展开（约 396、400、406 行的 Inventory/Furnace，419 行的 `ItemDrop`）。

**必须成对移除 Task 3 留下的全部 v10 过渡逻辑：**

1. `encodeServerControlPayload` 中 `InventoryState` 调用 `inventoryRepresentableV10` 的满耐久门禁，以及函数本身；
2. `decodeItemStack` 为工具补满耐久的 shim；
3. `ItemDrop.validate()` 的“protocol v10 item drop tool is not at full durability”限制；
4. `decodeItemDropUpserts` 为工具补满耐久的 shim。

其中 `decodeItemStack` 当前包含：

```go
	// 协议 v10 的物品负载只有 Item/Count，没有耐久字节；……
	if max, ok := core.ItemMaxDurability(stack.Item); ok {
		stack.Durability = max
	}
```

v11 起 wire 上有真实耐久字段，这四处**必须一起删除/改造**——漏掉任意 shim 都会把收到的真实耐久覆盖成满耐久；漏掉任意门禁则会让 v11 继续错误拒绝合法磨损工具。

新增编码函数（放在既有 `encodeFurnaceRef` 附近）：

```go
// encodeItemStack 是所有携带物品堆的消息共用的固定长度编码，共 5 字节。
// 统一走这一处可以避免某条消息漏带耐久。
func encodeItemStack(e *byteEncoder, stack core.ItemStack) {
	e.u16(uint16(stack.Item))
	e.u8(stack.Count)
	e.u16(stack.Durability)
}
```

改造既有解码函数，保持其 error-threading 签名不变：

```go
func decodeItemStack(d *byteDecoder, err error) (core.ItemStack, error) {
	// ……既有的 item / count 读取与 err 检查保持原样……
	durability, err := d.u16(err)
	if err != nil {
		return core.ItemStack{}, err
	}
	return core.ItemStack{
		Item: core.ItemID(item), Count: count, Durability: durability,
	}, nil
}
```

`d.u16` 的实际签名以文件里既有用法为准；照该文件已有的 error-threading 写法追加这一次读取即可。

把 `InventoryState` 编码改为：

```go
		case InventoryState:
			e.u8(message.Inventory.Hotbar.Selected)
			for _, stack := range message.Inventory.Hotbar.Slots {
				encodeItemStack(&e, stack)
			}
			for _, stack := range message.Inventory.Backpack {
				encodeItemStack(&e, stack)
			}
```

`FurnaceState` 编码改为：

```go
		case FurnaceState:
			encodeFurnaceRef(&e, message.Furnace)
			for _, stack := range [3]core.ItemStack{message.Input, message.Fuel, message.Output} {
				encodeItemStack(&e, stack)
			}
			e.u8(message.ProgressTicks)
			e.u16(message.BurnTicks)
```

熔炉当前只接受煤炭与粗铁，耐久恒为 0；统一编码是为了让「携带物品堆的消息」只有一种字节布局，而不是让熔炉支持工具。

对应的三处**解码**同样改用 `decodeItemStack`。`ItemDrop` 的编解码也改用同一对函数（注意 `ItemDrop` 的字段顺序须与编码一致），并删除 `decodeItemDropUpserts` 的补满逻辑。至少用一个非满耐久工具分别做 `InventoryState` 与 `ItemDropUpserts` 往返，断言精确保留，防止旧 shim 漏删。

- [ ] **Step 5: 更新受影响的 golden 与固定长度**

```bash
rg -n 'inventoryStateMaxPayload|furnaceStateBytes|goldenInventoryStateHex' internal/network
```

按 5 字节/堆重算所有固定长度常量与 golden 十六进制串。`InventoryState` 的 payload 从 `1 + 36*3 = 109` 变为 `1 + 36*5 = 181` 字节。

- [ ] **Step 6: 运行验证**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "^$" -fuzz FuzzSmallPacketCodec -fuzztime=10s'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "^$" -bench SmallPacketCodec -benchmem -count=3 -benchtime=200x'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && gofmt -l .'
git diff --check
```

期望：全部通过；fuzz 无新失败。

- [ ] **Step 7: 提交**

```bash
git add internal/network
git commit -m "feat: 协议 v11 携带工具耐久"
```

---

## Task 6: 玩家 schema v4 与区块 schema v5

**Files:**
- Modify: `internal/storage/player_codec.go`
- Modify: `internal/storage/player_migration.go`
- Modify: `internal/storage/chunk_codec.go`
- Modify: `internal/storage/migration.go`
- Modify: `internal/world/drop.go`
- Modify: 对应测试与 `internal/storage/testdata`

**Interfaces:**
- Consumes: `core.ItemMaxDurability`。
- Produces: `currentPlayerSchema = 4`、`currentChunkSchema = 5`、`world.DropSlotBytes = 19`；两条单向迁移把旧工具补为满耐久。

- [ ] **Step 1: 写失败迁移测试**

追加到 `internal/storage/player_migration_test.go`：

```go
func TestPlayerV3MigrationFillsFullToolDurability(t *testing.T) {
	// v3 没有耐久字段，迁移后旧工具一律视为满耐久。
	var dto playerDTO
	dto.Inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStonePickaxe, Count: 1}
	dto.Inventory.Backpack[0] = core.ItemStack{Item: core.ItemIronPickaxe, Count: 1}
	dto.Inventory.Hotbar.Slots[1] = core.ItemStack{Item: core.ItemStone, Count: 64}

	migrated, changed, err := migratePlayer(3, dto)
	if err != nil || !changed {
		t.Fatalf("migratePlayer(3) = (%v,%v)", changed, err)
	}
	stoneFull, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	ironFull, _ := core.ItemMaxDurability(core.ItemIronPickaxe)
	if got := migrated.Inventory.Hotbar.Slots[0].Durability; got != stoneFull {
		t.Fatalf("石镐迁移后耐久 = %d，想要 %d", got, stoneFull)
	}
	if got := migrated.Inventory.Backpack[0].Durability; got != ironFull {
		t.Fatalf("铁镐迁移后耐久 = %d，想要 %d", got, ironFull)
	}
	// 非工具不得被补上耐久。
	if got := migrated.Inventory.Hotbar.Slots[1].Durability; got != 0 {
		t.Fatalf("石头迁移后耐久 = %d，想要 0", got)
	}
}
```

追加到 `internal/storage/migration_test.go`：

```go
func TestChunkV4MigrationFillsFullToolDurability(t *testing.T) {
	var dto chunkDTO
	dto.Drops[0] = world.DropSlot{
		Generation: 3, Active: true,
		Stack: core.ItemStack{Item: core.ItemStonePickaxe, Count: 1},
	}
	dto.Drops[1] = world.DropSlot{
		Generation: 4, Active: true,
		Stack: core.ItemStack{Item: core.ItemCoal, Count: 9},
	}

	migrated, changed, err := migrateChunk(4, dto)
	if err != nil || !changed {
		t.Fatalf("migrateChunk(4) = (%v,%v)", changed, err)
	}
	full, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	if got := migrated.Drops[0].Stack.Durability; got != full {
		t.Fatalf("掉落镐迁移后耐久 = %d，想要 %d", got, full)
	}
	if got := migrated.Drops[1].Stack.Durability; got != 0 {
		t.Fatalf("掉落煤炭迁移后耐久 = %d，想要 0", got)
	}
}
```

再把 `internal/storage/chunk_drop_test.go` 已有的真实 v4 多件工具堆 fixture 下沉为 migration 测试：v4→v5 迁移必须稳定拆分、标记 changed；最低可复用槽、generation+1、字段复制与容量不足 `ErrCorrupt` 的语义保持不变。

上面两个测试若与本仓库实际的 DTO 字段名不符，先运行 `rg -n 'type playerDTO|type chunkDTO' -A 12 internal/storage` 确认后替换。

- [ ] **Step 2: 运行，确认失败**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage -run "MigrationFillsFullToolDurability" -count=1'
```

期望：失败，提示缺少 schema 3→4 与 4→5 的迁移函数。

- [ ] **Step 3: 升 schema 并写迁移**

`internal/storage/player_codec.go`：

```go
	currentPlayerSchema uint32 = 4
	// playerBackpackBytes 是背包负载长度；schema v4 起每格 5 字节。
	playerBackpackBytes = core.BackpackSlots * 5
```

`internal/storage/chunk_codec.go`：

```go
	currentChunkSchema uint32 = 5
```

`internal/world/drop.go`：

```go
// DropSlotBytes 是单个掉落物槽的固定线上/存档编码长度。
// schema v5 起物品堆为 5 字节（Item 2 + Count 1 + Durability 2）。
const DropSlotBytes = 4 + 1 + 2 + 1 + 2 + 4 + 4 + 1
```

迁移函数的类型是 `func(playerDTO) (playerDTO, error)` 与 `func(chunkDTO) (chunkDTO, error)`。在 `playerMigrations` 注册键 `3`，在 `chunkMigrations` 注册键 `4`，两者共用同一条规则：

```go
// fillFullDurability 把没有耐久的旧工具补为满耐久，非工具保持零值。
func fillFullDurability(stack core.ItemStack) core.ItemStack {
	full, ok := core.ItemMaxDurability(stack.Item)
	if !ok || stack.Durability != 0 {
		return stack
	}
	stack.Durability = full
	return stack
}
```

```go
	// v3 没有耐久字段，确定性迁移为把旧工具一律视为满耐久。
	3: func(dto playerDTO) (playerDTO, error) {
		for slot, stack := range dto.Inventory.Hotbar.Slots {
			dto.Inventory.Hotbar.Slots[slot] = fillFullDurability(stack)
		}
		for slot, stack := range dto.Inventory.Backpack {
			dto.Inventory.Backpack[slot] = fillFullDurability(stack)
		}
		return dto, nil
	},
```

```go
	// v4 的掉落物槽没有耐久字段，同样一律视为满耐久。
	4: func(dto chunkDTO) (chunkDTO, error) {
		for slot := range dto.Drops {
			dto.Drops[slot].Stack = fillFullDurability(dto.Drops[slot].Stack)
		}
		// 同时把 M4H 可能写出的 Count>1 工具堆稳定拆到最低可复用空槽；
		// 容量不足时返回 ErrCorrupt，不截断或覆盖任何活动槽。
		return normalizeLegacyToolDropStacks(dto)
	},
```

`fillFullDurability` 与拆分 helper 放在 `internal/storage/migration.go`。Task 3 当前 `chunk_codec.go` 中的 `normalizeV4LegacyToolDropStacks`、宽松 legacy 校验和解码后的过渡调用要移入/收敛到这条 migration，不能在 v5 当前格式上继续运行。熔炉槽只装煤炭与粗铁，不需要补齐。

- [ ] **Step 4: 改编解码、移除过渡 shim 并保留旧 golden**

玩家与区块的编解码里，每个物品堆由「u16 + u8」改为「u16 + u8 + u16」。

**必须移除 Task 3 留下的全部存档过渡门禁与 shim。** 玩家侧包括 `encodePlayer` 调用 `playerInventoryRepresentableV3` 的满耐久门禁（连同函数）和 `decodePlayerStack` 的补满 shim：

```go
	// schema v3 的物品负载只有 Item/Count，没有耐久字节；……
	if max, ok := core.ItemMaxDurability(stack.Item); ok {
		stack.Durability = max
	}
```

schema v4 起磁盘上有真实耐久字段，这些逻辑**必须删掉**——门禁会错误拒绝磨损工具，shim 会把读到的真实耐久覆盖成满耐久。删除后，"旧工具视为满耐久"只由 v3→v4 migration 承担。

区块侧同样删除/迁移以下 v4 过渡逻辑：`validateDropSlot` 的“schema v4 工具必须满耐久”限制、`decodeLogicalDropSlot` 的补满 shim、`validateLegacyDropSlot`、`decodeChunkPayload` 对 `normalizeV4LegacyToolDropStacks` 的直接调用。v5 解码必须读取真实耐久并走严格 `ItemStack.Valid()`；旧 v4 的补满与拆分只由 v4→v5 migration 承担。`DropsHash` 已采用 19 字节布局，`DropSlotBytes` 升为 19 后应与正式槽布局保持相同字段顺序。

**既有的 v3 玩家 golden 与 v4 区块 golden 字节不得修改**——它们证明迁移能读旧数据；为 v4 玩家与 v5 区块**各新增**一份 golden。

新增一条回归测试，钉死"解码器不再兜底"：

```go
func TestPlayerSchemaV4DecodeKeepsWornDurability(t *testing.T) {
	// 磨损工具经过一次编码/解码往返后耐久必须原样保留，
	// 不得被任何"补满耐久"的兜底逻辑覆盖。
	full, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	var inventory core.Inventory
	inventory.Hotbar.Slots[0] = core.ItemStack{
		Item: core.ItemStonePickaxe, Count: 1, Durability: full - 7,
	}
	// ……编码为 v4 payload 再解码，断言 Durability == full-7……
}
```

把注释替换成实际的编解码调用，函数名以该文件既有的编解码入口为准。

区块新增同级回归：v5 中一把非满耐久工具编码/解码后精确保留；旧 v4 多件工具堆仍能迁移并拆分；容量不足仍明确失败且原存档字节不变。

```bash
rg -n 'testdata' internal/storage/*_test.go | head
```

- [ ] **Step 5: 运行验证**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage ./internal/world -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage -run "^$" -fuzz FuzzDecodeChunkPayload -fuzztime=10s'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage -run "^$" -fuzz FuzzDecodePlayer -fuzztime=10s'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && gofmt -l .'
git diff --check
```

期望：全部通过；metadata 仍为 v2 未被触碰。

- [ ] **Step 6: 提交**

```bash
git add internal/storage internal/world/drop.go
git commit -m "feat: 玩家 schema v4 与区块 schema v5 持久化耐久"
```

---

## Task 7: 权威扣减与损坏转换

**Files:**
- Modify: `internal/sim/mining.go`
- Modify: `internal/sim/mining_test.go`

**Interfaces:**
- Consumes: `core.ItemMaxDurability`、`core.ItemBrokenForm`、既有 `completeMining` 与 `playerMiningState`。
- Produces: `consumeToolDurability(player *playerState) bool`；成功破坏扣 1、归零转损坏；`miningRule` 把损坏物品视同空手。

- [ ] **Step 1: 让 `setMiningHeldItem` 给工具满耐久**

Task 3 收紧 `ItemStack.Valid()` 之后，`Durability` 为 0 的镐是非法值。既有辅助 `internal/sim/mining_test.go:542` 正好构造了这种值：

```go
func setMiningHeldItem(player *playerState, item core.ItemID) {
	if item == core.ItemNone {
		player.inventory.Hotbar.Slots[0] = core.ItemStack{}
		return
	}
	// 工具必须带满耐久，否则是非法物品堆。
	durability, _ := core.ItemMaxDurability(item)
	player.inventory.Hotbar.Slots[0] = core.ItemStack{
		Item: item, Count: 1, Durability: durability,
	}
}
```

非工具的 `ItemMaxDurability` 返回 0，行为不变。

- [ ] **Step 2: 写失败测试**

追加到 `internal/sim/mining_test.go`（package `sim`，可直接访问内部状态）。复用既有辅助 `readyMiningPlayers` / `setMiningHeldItem` / `advanceMiningOnce` / `fillMiningDrops`，不要新写驱动循环：

```go
func TestMiningConsumesOneDurabilityPerBrokenBlock(t *testing.T) {
	engine, sessions, targets := readyMiningPlayers(t, 1)
	player := engine.sessions[sessions[0]].player
	engine.SetBlockForTest(targets[0], core.CoalOreID)
	setMiningHeldItem(player, core.ItemStonePickaxe)
	full, _ := core.ItemMaxDurability(core.ItemStonePickaxe)

	// 石镐采煤矿需要 15 tick。
	for range 15 {
		advanceMiningOnce(engine)
	}

	got := player.inventory.Hotbar.Slots[0]
	if got.Item != core.ItemStonePickaxe || got.Durability != full-1 {
		t.Fatalf("破坏一个方块后栏位 = %+v，想要石镐耐久 %d", got, full-1)
	}
	if !player.inventoryDirty {
		t.Fatal("扣减耐久没有标记 inventoryDirty")
	}
}

func TestMiningTurnsToolIntoBrokenFormAtZero(t *testing.T) {
	engine, sessions, targets := readyMiningPlayers(t, 1)
	player := engine.sessions[sessions[0]].player
	engine.SetBlockForTest(targets[0], core.CoalOreID)
	setMiningHeldItem(player, core.ItemStonePickaxe)
	// 只剩最后一点耐久。
	player.inventory.Hotbar.Slots[0].Durability = 1

	var result TickResult
	for range 15 {
		result = advanceMiningOnce(engine)
	}

	// 最后一次采掘仍然生效：方块破坏、掉落物照常产生。
	if len(result.Rejected) != 0 {
		t.Fatalf("最后一次采掘被拒绝 = %+v", result.Rejected)
	}
	record := miningTargetRecord(t, engine, targets[0])
	x, _, z := targets[0].Local()
	if got := record.Chunk.BlockAt(x, targets[0].Y, z); got != core.AirID {
		t.Fatalf("完成后方块 = %d，想要空气", got)
	}
	if got := miningDropTotals(record.Chunk)[core.ItemCoal]; got != 1 {
		t.Fatalf("煤炭掉落 = %d，想要 1", got)
	}
	got := player.inventory.Hotbar.Slots[0]
	if got.Item != core.ItemBrokenStonePickaxe || got.Count != 1 || got.Durability != 0 {
		t.Fatalf("耐久耗尽后栏位 = %+v，想要一个损坏的石镐", got)
	}
}

func TestMiningRejectionDoesNotConsumeDurability(t *testing.T) {
	engine, sessions, targets := readyMiningPlayers(t, 1)
	player := engine.sessions[sessions[0]].player
	engine.SetBlockForTest(targets[0], core.CoalOreID)
	setMiningHeldItem(player, core.ItemStonePickaxe)
	full, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	// 占满 32 个掉落物槽，使采掘完成时命中 RejectDropCapacity。
	fillMiningDrops(engine, targets[0])

	var result TickResult
	for range 15 {
		result = advanceMiningOnce(engine)
	}

	if len(result.Rejected) != 1 || result.Rejected[0].Reason != RejectDropCapacity {
		t.Fatalf("拒绝 = %+v，想要一次 RejectDropCapacity", result.Rejected)
	}
	if got := player.inventory.Hotbar.Slots[0].Durability; got != full {
		t.Fatalf("采掘被拒绝却扣了耐久：%d，想要保持 %d", got, full)
	}
	if player.inventoryDirty {
		t.Fatal("采掘被拒绝却标记了 inventoryDirty")
	}
}

func TestBrokenToolMinesLikeBareHand(t *testing.T) {
	// 损坏物品不提供任何采掘等级，行为必须与空手完全一致。
	// 完整版本见 Step 4——那里会遍历全部方块。
	ticks, harvest := miningRule(core.StoneID, core.ItemBrokenStonePickaxe)
	bareTicks, bareHarvest := miningRule(core.StoneID, core.ItemNone)
	if ticks != bareTicks || harvest != bareHarvest {
		t.Fatalf("损坏石镐 = (%d,%v)，空手 = (%d,%v)，两者必须一致",
			ticks, harvest, bareTicks, bareHarvest)
	}
}
```

用煤矿而非石头，是因为石头对空手也可采集，无法区分「工具生效」与「工具被忽略」。

- [ ] **Step 3: 运行，确认失败**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim -run "Durability|BrokenForm|BrokenTool" -count=1'
```

期望：耐久没有被扣减，前两个测试失败；`TestBrokenToolMinesLikeBareHand` 失败，因为 `miningRule` 尚未把损坏物品当空手。

- [ ] **Step 4: 让损坏物品等同空手**

这一步是真改动，不是回归确认。`miningRule` 的 default 分支**不等于**空手：只有 `core.StoneID` 单独给了 `core.ItemNone` 一条 `30, true`，其余持有物落入 `30, false`。若不改，损坏的镐会连石头都挖不动，而空手可以——违反设计的第四条不变量。

`internal/sim/mining.go` 的 `miningRule`，把两个损坏物品并入 `core.StoneID` 的空手分支：

```go
	case core.StoneID:
		switch held {
		// 损坏工具不提供任何采掘等级，行为与空手完全一致。
		case core.ItemNone, core.ItemBrokenStonePickaxe, core.ItemBrokenIronPickaxe:
			return 30, true
		case core.ItemStonePickaxe:
			return 15, true
		case core.ItemIronPickaxe:
			return 8, true
		default:
			return 30, false
		}
```

其余三个 block 分支无需改动：`core.ItemNone` 在那里本就落入 default，损坏物品落入同一处，两者已经一致。`core.DirtID`/`core.GrassID` 不看持有物。

既有的 `TestMiningRule`（`internal/sim/mining_test.go:15`）是一张显式表，补两行进去：

```go
		{core.StoneID, core.ItemBrokenStonePickaxe, 30, true},
		{core.CoalOreID, core.ItemBrokenIronPickaxe, 30, false},
```

`TestBrokenToolMinesLikeBareHand` 再扩展为遍历全部方块，确保这个「只有 StoneID 需要改」的判断被钉住：

```go
func TestBrokenToolMinesLikeBareHand(t *testing.T) {
	for _, block := range []core.BlockID{
		core.DirtID, core.GrassID, core.StoneID,
		core.StoneBrickID, core.FurnaceID, core.CoalOreID,
		core.IronOreID, core.IronBlockID,
	} {
		bareTicks, bareHarvest := miningRule(block, core.ItemNone)
		for _, broken := range []core.ItemID{
			core.ItemBrokenStonePickaxe, core.ItemBrokenIronPickaxe,
		} {
			ticks, harvest := miningRule(block, broken)
			if ticks != bareTicks || harvest != bareHarvest {
				t.Fatalf("方块 %d 损坏工具 %d = (%d,%v)，空手 = (%d,%v)，两者必须一致",
					block, broken, ticks, harvest, bareTicks, bareHarvest)
			}
		}
	}
}
```

- [ ] **Step 5: 加扣减函数并接到完成分支**

`internal/sim/mining.go` 新增：

```go
// consumeToolDurability 在成功破坏方块后扣减选中工具的耐久。
// 耐久归零时把栏位整体替换为损坏形态。返回背包是否发生变化。
func consumeToolDurability(player *playerState) bool {
	selected := player.inventory.Hotbar.Selected
	stack := player.inventory.Hotbar.Slots[selected]
	if _, ok := core.ItemMaxDurability(stack.Item); !ok {
		return false
	}
	if stack.Durability > 1 {
		stack.Durability--
		player.inventory.Hotbar.Slots[selected] = stack
		return true
	}
	broken, ok := core.ItemBrokenForm(stack.Item)
	if !ok {
		return false
	}
	player.inventory.Hotbar.Slots[selected] = core.ItemStack{Item: broken, Count: 1}
	return true
}
```

在 `advanceMining` 末尾，`completeMining` 返回**未被拒绝**时调用它。既有代码是：

```go
		player.mining = playerMiningState{}
		if rejected {
			result.Rejected = append(result.Rejected, Rejection{
				Session: id, Sequence: player.lastInputSequence, Reason: reason,
			})
		}
```

改为：

```go
		player.mining = playerMiningState{}
		if rejected {
			result.Rejected = append(result.Rejected, Rejection{
				Session: id, Sequence: player.lastInputSequence, Reason: reason,
			})
			continue
		}
		// 只有方块真正被破坏才磨损工具；三条拒绝路径一点耐久都不扣。
		if consumeToolDurability(player) {
			player.inventoryDirty = true
		}
```

保持 `player.mining = playerMiningState{}` 的位置不变。

**扣减不看 `harvestable`。** 用错工具时方块照样被破坏（`TestMiningCompletionUsesFixedToolAndDropRules` 的「石镐采铁块不掉落」已证明块变为空气），只是不掉落——工具确实干了活，就该磨损。这与「方块真正被破坏才扣」是同一条规则。

- [ ] **Step 6: 运行验证**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim ./internal/world ./internal/core -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && gofmt -l .'
git diff --check
```

期望：全部通过。

- [ ] **Step 7: 补三条中断采掘的回归测试**

既有状态机已经在 `held` 变化时重置进度，本步只是把行为钉死。既有的 `TestMiningTargetBlockAndToolChangesRestartAtOne`（`internal/sim/mining_test.go:82`）覆盖了换工具，这里补齐工具**消失**的三种方式。追加到 `internal/sim/mining_test.go`：

```go
func TestMiningResetsProgressWhenHeldToolLeavesHand(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(player *playerState)
	}{
		{name: "换到空栏位", mutate: func(player *playerState) {
			player.inventory.Hotbar.Selected = 5
		}},
		{name: "丢弃工具", mutate: func(player *playerState) {
			player.inventory.Hotbar.Slots[0] = core.ItemStack{}
		}},
		{name: "工具损坏", mutate: func(player *playerState) {
			player.inventory.Hotbar.Slots[0] = core.ItemStack{
				Item: core.ItemBrokenStonePickaxe, Count: 1,
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			engine, sessions, targets := readyMiningPlayers(t, 1)
			player := engine.sessions[sessions[0]].player
			engine.SetBlockForTest(targets[0], core.CoalOreID)
			setMiningHeldItem(player, core.ItemStonePickaxe)

			// 先累积三 tick 进度（石镐采煤矿共需 15 tick）。
			for range 3 {
				advanceMiningOnce(engine)
			}
			if player.mining.progressTicks != 3 {
				t.Fatalf("进度 = %d，想要 3", player.mining.progressTicks)
			}

			test.mutate(player)
			advanceMiningOnce(engine)

			// 手持物变化让状态机重置，本 tick 以新持有物重新从 1 开始。
			if player.mining.progressTicks != 1 {
				t.Fatalf("工具离手后进度 = %d，想要重新从 1 开始",
					player.mining.progressTicks)
			}
			if player.mining.held == core.ItemStonePickaxe {
				t.Fatal("状态机仍记录着已经离手的石镐")
			}
		})
	}
}
```

- [ ] **Step 8: 运行并提交**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim -run "^$" -bench "." -benchmem -count=1 -benchtime=100x'
git add internal/sim
git commit -m "feat: 采掘消耗工具耐久"
```

benchmark 一步只用来确认没有引入分配回归，不设阈值。

---

## Task 8: 快捷栏耐久条

**Files:**
- Modify: `internal/render/hotbar.go`
- Modify: `internal/render/hotbar_test.go`
- Modify: `cmd/mcgo/app.go`

**Interfaces:**
- Consumes: `core.ItemMaxDurability`、既有 `hotbarInstance` / `hotbarLayout` / `inventorySlotOrigin` / `hotbarSlotSize`。
- Produces: `appendDurabilityBar(dst *hotbarLayout, slot int, stack core.ItemStack, width, height float32)`；提高后的 `maxHotbarQuads`；不新增 pipeline 或纹理。

**先读这一条：quad 预算是硬上限。** `maxHotbarQuads = 2 + core.InventorySlots*2 + maxOverlayQuads`，它同时决定 GPU 上传缓冲的固定大小，且 `internal/render/hotbar_test.go:122` 断言满界面的 quad 数**精确等于**该常量。耐久条最多为 9 个快捷栏栏位各加 2 个 quad，因此常量必须同步提高，否则要么溢出缓冲、要么让既有断言失败。

- [ ] **Step 1: 写失败测试**

追加到 `internal/render/hotbar_test.go`：

```go
func TestDurabilityBarAppearsOnlyForWornTools(t *testing.T) {
	full, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	for _, test := range []struct {
		name  string
		stack core.ItemStack
		want  int
	}{
		{"满耐久工具不显示", core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: full}, 0},
		{"磨损工具显示两个 quad", core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: full / 2}, 2},
		{"损坏物品不显示", core.ItemStack{Item: core.ItemBrokenStonePickaxe, Count: 1}, 0},
		{"普通方块不显示", core.ItemStack{Item: core.ItemStone, Count: 64}, 0},
		{"空栏位不显示", core.ItemStack{}, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			var layout hotbarLayout
			appendDurabilityBar(&layout, 0, test.stack, 1920, 1080)
			if got := len(layout.quads); got != test.want {
				t.Fatalf("quad 数量 = %d，想要 %d", got, test.want)
			}
		})
	}
}

func TestDurabilityBarFillTracksRemaining(t *testing.T) {
	full, _ := core.ItemMaxDurability(core.ItemIronPickaxe)
	var low, high hotbarLayout
	appendDurabilityBar(&low, 0, core.ItemStack{
		Item: core.ItemIronPickaxe, Count: 1, Durability: 1,
	}, 1920, 1080)
	appendDurabilityBar(&high, 0, core.ItemStack{
		Item: core.ItemIronPickaxe, Count: 1, Durability: full - 1,
	}, 1920, 1080)

	if len(low.quads) != 2 || len(high.quads) != 2 {
		t.Fatalf("quad 数量 = %d / %d，想要各 2", len(low.quads), len(high.quads))
	}
	// 第二个 quad 是填充条，剩余耐久越少填充越短。
	if low.quads[1].Width >= high.quads[1].Width {
		t.Fatalf("低耐久填充宽度 %v 不小于高耐久 %v", low.quads[1].Width, high.quads[1].Width)
	}
	if low.quads[1].Width <= 0 {
		t.Fatalf("填充宽度 = %v，想要正值", low.quads[1].Width)
	}
}
```

- [ ] **Step 2: 运行，确认失败**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/render -run "DurabilityBar" -count=1'
```

期望：编译失败，`appendDurabilityBar` 未定义。

- [ ] **Step 3: 实现耐久条**

`internal/render/hotbar.go`，照既有 `appendMiningBar` 的模式新增。先在文件顶部的常量区加尺寸，并提高 quad 预算：

```go
	// 耐久条只画在 9 个快捷栏栏位上，每格最多两个 quad。
	maxHotbarQuads = 2 + core.InventorySlots*2 + core.HotbarSlots*2 + maxOverlayQuads
```

```go
	durabilityBarHeight = float32(3)
	durabilityBarInset  = float32(4)
```

然后：

```go
// appendDurabilityBar 在快捷栏栏位下沿绘制固定背景和剩余耐久比例填充。
// 只有存在耐久上限且尚未满耐久的物品才显示；损坏物品没有上限，因此不显示。
func appendDurabilityBar(
	dst *hotbarLayout,
	slot int,
	stack core.ItemStack,
	width, height float32,
) {
	max, ok := core.ItemMaxDurability(stack.Item)
	if !ok || max == 0 || stack.Durability == 0 || stack.Durability >= max {
		return
	}
	slotX, slotY := inventorySlotOrigin(slot, false, width, height)
	barWidth := hotbarSlotSize - durabilityBarInset*2
	x := slotX + durabilityBarInset
	y := slotY + hotbarSlotSize - durabilityBarInset - durabilityBarHeight
	dst.quads = append(dst.quads, hotbarInstance{
		X: x, Y: y, Width: barWidth, Height: durabilityBarHeight,
		Color: [4]float32{0.05, 0.05, 0.06, 0.85},
	})
	fraction := float32(stack.Durability) / float32(max)
	color := [4]float32{0.30, 0.78, 0.36, 0.95}
	if fraction < 0.25 {
		color = [4]float32{0.90, 0.35, 0.25, 0.95}
	}
	dst.quads = append(dst.quads, hotbarInstance{
		X: x, Y: y, Width: barWidth * fraction, Height: durabilityBarHeight,
		Color: color,
	})
}
```

- [ ] **Step 4: 在布局中逐格调用**

在 `layoutInventory` 中绘制完快捷栏图标之后，对九个快捷栏栏位各调用一次：

```go
	for slot, stack := range inventory.Hotbar.Slots {
		appendDurabilityBar(dst, slot, stack, width, height)
	}
```

放在选中框与图标之后、覆盖层之前，确保耐久条画在图标上方。

- [ ] **Step 5: 修正满界面 quad 断言**

`internal/render/hotbar_test.go:122` 要求满界面的 quad 数精确等于 `maxHotbarQuads`。提高常量后该断言会失败——**这是预期的**，不要把等号改成小于号（那会让缓冲上限失去证明）。正确修法是让该用例的快捷栏九格都放上磨损工具，使耐久条全部生效：

```go
	full, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	for slot := range inventory.Hotbar.Slots {
		inventory.Hotbar.Slots[slot] = core.ItemStack{
			Item: core.ItemStonePickaxe, Count: 1, Durability: full / 2,
		}
	}
```

`hotbar_test.go:133` 的关闭界面断言 `1+core.HotbarSlots*2+2` 同理：若该用例的快捷栏不含磨损工具，数字不变；含则需加上对应耐久条数量。按用例实际内容调整，不要放宽上限比较。

- [ ] **Step 6: 运行验证**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/render ./cmd/mcgo -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/render -run "Test(HotbarRendererHeadlessBlendOverExistingColor|DurabilityBar)$" -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && gofmt -l .'
git diff --check
```

期望：全部通过，且没有创建或聚焦窗口。

- [ ] **Step 7: 提交**

```bash
git add internal/render cmd/mcgo
git commit -m "feat: 快捷栏显示工具耐久"
```

---

## Task 9: 纵向闭环、文档与全门禁

**Files:**
- Modify: `internal/server/drop_restart_test.go`
- Modify: `internal/server/tcp_integration_test.go`
- Modify: `README.md`
- Modify: `docs/notes/lan-server.md`
- Verify: `docs/notes/perf-baseline.json`
- Verify: `docs/notes/perf-baseline-m5.json`

**Interfaces:**
- Consumes: 前八个任务的全部产出。
- Produces: Memory/TCP 一致性与重启持久化证明；准确的兼容性文档；冻结候选。

- [ ] **Step 1: 写 TCP 纵向测试**

追加到 `internal/server/drop_restart_test.go`（与它复用同一批 restart 辅助）。本测试不驱动采掘——采掘的扣减已由 Task 7 在 `sim` 层证明；这里要证明的是**耐久这个字段本身跨 TCP 与重启无损**，因此预置一个非满耐久的石镐即可。

先照 `inventoryCount` 的模式新增读取辅助：

```go
// inventoryDurability 返回最新权威背包中某物品所在栏位的耐久。
// 工具单格上限为 1，因此至多只会有一处匹配。
func (connected *multiplayerTCPClient) inventoryDurability(item core.ItemID) (uint16, bool) {
	for index := len(connected.transcript) - 1; index >= 0; index-- {
		state, ok := connected.transcript[index].message.(network.InventoryState)
		if !ok {
			continue
		}
		for _, slot := range state.Inventory.Hotbar.Slots {
			if slot.Item == item {
				return slot.Durability, true
			}
		}
		for _, slot := range state.Inventory.Backpack {
			if slot.Item == item {
				return slot.Durability, true
			}
		}
		return 0, false
	}
	return 0, false
}
```

```go
// TestTCPToolDurabilitySurvivesRestart 证明工具耐久是权威状态的一部分：
// 它随 InventoryState 抵达 TCP 客户端，并跨正常关服精确保留，
// 既不会被重置为满耐久，也不会被清零。
func TestTCPToolDurabilitySurvivesRestart(t *testing.T) {
	const seed int64 = 990012
	root := t.TempDir()
	identity := multiplayerIdentity(0xd8, "磨损者")
	full, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	// 非满且非零：满耐久无法区分「保留」与「被重置为满」。
	const wear = 5
	want := full - wear

	seedStore, err := storage.OpenDisk(context.Background(), root, storage.OpenOptions{Create: storage.Metadata{
		FormatVersion: 2, Seed: seed, SpawnDimension: core.Overworld,
	}})
	if err != nil {
		t.Fatalf("OpenDisk seed: %v", err)
	}
	location := storage.PlayerLocation{Dimension: core.Overworld, Position: [3]float32{8.5, 1.001, 8.5}}
	var inventory core.Inventory
	inventory.Hotbar.Selected = 0
	inventory.Hotbar.Slots[0] = core.ItemStack{
		Item: core.ItemStonePickaxe, Count: 1, Durability: want,
	}
	if _, err := seedStore.SavePlayer(context.Background(), storage.PlayerSave{
		PlayerID: identity.PlayerID, Revision: 1, DisplayName: identity.DisplayName,
		Current: location, Safe: &location, Inventory: inventory,
	}); err != nil {
		_ = seedStore.Close()
		t.Fatalf("预置玩家存档: %v", err)
	}
	if err := seedStore.Close(); err != nil {
		t.Fatalf("关闭种子 store: %v", err)
	}

	first := startMultiplayerRestartHost(t, root, seed)
	clients := connectRestartClients(t, first.addr, []network.Identity{identity}, nil)
	waitSingleRestartClientReady(t, clients[0])

	deadline := time.Now().Add(10 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("等待带耐久的背包抵达 TCP 客户端超时")
		}
		if _, err := clients[0].drainOne(); err != nil {
			t.Fatalf("drain: %v", err)
		}
		if got, ok := clients[0].inventoryDurability(core.ItemStonePickaxe); ok {
			if got != want {
				t.Fatalf("关服前 TCP 耐久 = %d，想要 %d", got, want)
			}
			break
		}
	}

	if err := shutdownRestartHost(first, clients); err != nil {
		t.Fatalf("首次关服: %v", err)
	}

	var reconnected []*multiplayerTCPClient
	second := startMultiplayerRestartHost(t, root, seed)
	t.Cleanup(func() {
		if err := shutdownRestartHost(second, reconnected); err != nil {
			t.Errorf("清理关服: %v", err)
		}
	})
	reconnected = connectRestartClients(t, second.addr, []network.Identity{identity}, nil)
	waitSingleRestartClientReady(t, reconnected[0])

	deadline = time.Now().Add(10 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("重启后未收到含石镐的背包")
		}
		if _, err := reconnected[0].drainOne(); err != nil {
			t.Fatalf("重连 drain: %v", err)
		}
		got, ok := reconnected[0].inventoryDurability(core.ItemStonePickaxe)
		if !ok {
			continue
		}
		if got != want {
			t.Fatalf("重启后 TCP 耐久 = %d，想要 %d", got, want)
		}
		return
	}
}
```

- [ ] **Step 2: 写 v10 拒绝测试**

在 `internal/server/tcp_integration_test.go` 的版本拒绝矩阵中把上一版本补进去：

```go
		for _, version := range []byte{7, 8, 9, 10, byte(network.ProtocolVersion + 1)} {
```

- [ ] **Step 3: 运行纵向测试**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server ./internal/network ./internal/storage ./cmd/mcgod -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && CGO_ENABLED=0 GOOS=linux go build -o /private/tmp/mcgod-m4j ./cmd/mcgod'
```

期望：全部通过；无 CGO Linux 构建成功。

- [ ] **Step 4: 更新文档**

`README.md` 的控制/限制清单加入耐久边界：石镐 `131`、铁镐 `250`，成功破坏方块扣 1，耗尽变为损坏物品且等同空手，损坏物品可丢弃、不可合成或熔炼，无修复与回收。

`README.md` 与 `docs/notes/lan-server.md` 的兼容性段落更新为：协议 v11（v10 在进入 Play 前拒绝）、玩家 schema v4、区块 schema v5、metadata v2 不变。写明升级前必须正常关服并备份整个世界目录，**回退到 v10 程序必须恢复升级前的备份**——本次存档格式变化不可逆向读取。

- [ ] **Step 5: 证明 scenario 与基线未变**

```bash
git diff --exit-code origin/main -- docs/notes/perf-baseline.json docs/notes/perf-baseline-m5.json && echo "两条 baseline 字节未变"
rg -n 'scenarioVersion[[:space:]]*= 12' cmd/mcgo/benchmark.go
jq -r '.scenario_version' docs/notes/perf-baseline-m5.json
shasum -a 256 docs/notes/perf-baseline.json docs/notes/perf-baseline-m5.json
```

期望：diff 为空；`scenarioVersion` 仍为 12；两个哈希与 Task 1 记录的完全一致。

- [ ] **Step 6: 全仓门禁**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race'
zsh -ic 'gvm use go1.26.0 >/dev/null && go vet ./...'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "^$" -fuzz FuzzSmallPacketCodec -fuzztime=10s'
zsh -ic 'gvm use go1.26.0 >/dev/null && gofmt -l .'
git diff --check
openspec validate --all --strict --no-interactive
```

期望：全部退出码为零；`gofmt -l .` 无输出；没有任何门禁、阈值或基线被放宽。

- [ ] **Step 7: 核对最终范围**

```bash
git status --short --branch
git diff --stat origin/main...HEAD
git diff --name-only origin/main...HEAD
```

期望：只含 `core`/`world`/`sim`/`network`/`storage`/`render`/`cmd`/测试/文档；没有二进制资源、新依赖、生成的基线文件或前台进程产物。确认**未触及** render 的天空、地形、昼夜文件与 `world` 光照、`mesh`——那是 M4I 的范围。

- [ ] **Step 8: 提交**

```bash
git add README.md docs/notes/lan-server.md internal/server
git commit -m "docs: 说明工具耐久与协议 v11"
```

---

## Plan Self-Review Checklist

- [ ] 设计文档的每条决策都能指向一个任务：耐久字段与查表 → Task 3；损坏物品 ID → Task 3；四条不变量 → Task 2、3、5、7；`prepareDropSlot` 缺陷 → Task 2；扣减规则 → Task 7；三处升版 → Task 5、6；耐久条 → Task 8；scenario v12 不变 → Task 9。
- [ ] 没有 TBD、TODO、"类似 Task N"、"添加适当的错误处理"这类占位表述。
- [ ] 类型与函数名前后一致，且与仓库现状核对过：`ItemMaxDurability`、`ItemBrokenForm`、`ItemBrokenStonePickaxe`、`ItemBrokenIronPickaxe`、`consumeToolDurability`、`encodeItemStack`、`decodeItemStack`、`appendDurabilityBar`、`fillFullDurability`；复用的既有符号为 `core.Recipe` / `CraftingRecipe`、`hotbarSlotSize`、`maxHotbarQuads`、`migratePlayer` / `migrateChunk`、`FuzzDecodePlayer` / `FuzzDecodeChunkPayload`、`readyMiningPlayers` / `setMiningHeldItem` / `advanceMiningOnce` / `fillMiningDrops`。
- [ ] 每个实现任务都以「独立可测的交付物 + 一次提交」结束。
- [ ] 玩家 schema 4、区块 schema 5、协议 11、metadata 2、scenario 12 在全文一致。
