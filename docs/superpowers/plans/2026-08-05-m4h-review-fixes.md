# M4H 审核问题修复实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复工具地面堆无法拾取，以及同 tick 放置与主动丢弃反转全局序号的两个 M4H 缺陷。

**Architecture:** 地面掉落堆继续保持通用 `1..64` 数量边界，`Inventory.AddStack` 负责按物品单格上限拆入固定 36 格。`Engine.Step` 复用本 tick 有界命令切片，把放置、选栏和主动丢弃放到同一个按序交互阶段，并在其后推进掉落物。

**Tech Stack:** Go 1.26、现有 `internal/core`/`internal/world`/`internal/sim`、OpenSpec、Go 原生测试与 race detector。

## Global Constraints

- 服务端继续是玩家与世界状态的唯一权威，客户端不预测主动丢弃。
- 协议保持 v10，玩家 schema v3、区块 schema v4、metadata v2、benchmark scenario v12 均不改变。
- 每条命令和每次拾取只扫描固定命令数、36 个背包栏位与 32 个掉落物槽；不得新增锁、goroutine、无界队列或同步 I/O。
- Go 注释和测试说明使用中文；实现先写失败测试，修改后执行 `gofmt`。
- 自动验证不得启动或聚焦前台游戏窗口。

---

### Task 1: 工具地面堆按栏位上限拆分拾取

**Files:**
- Modify: `internal/sim/drop_test.go`
- Modify: `internal/core/inventory.go`

**Interfaces:**
- Consumes: `Inventory.AddStack(stack ItemStack) (Inventory, ItemStack)`、`ItemStackLimit(item ItemID) (uint8, bool)`。
- Produces: `AddStack` 接受已注册、数量在 `1..64` 的来源堆，同时保证每个输出栏位继续满足 `ItemStack.Valid`。

- [ ] **Step 1: 写失败测试**

在 `internal/sim/drop_test.go` 增加 `TestToolDropPileSplitsAcrossInventorySlots`：向玩家脚下写入 `ItemStonePickaxe × 2`、零拾取延迟的活动掉落堆，推进一个 tick，断言快捷栏 0 和 1 各得到一把石镐、地面槽清空且只发布一次最终背包。

- [ ] **Step 2: 运行测试并确认 RED**

Run: `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim -run TestToolDropPileSplitsAcrossInventorySlots -count=1'`

Expected: FAIL；当前 `AddStack` 把 `Count=2` 的工具来源堆视为非法，玩家背包保持为空。

- [ ] **Step 3: 写最小实现**

把 `AddStack` 的入口校验改为：`ItemStackLimit` 必须存在、`Count` 必须在 `1..MaxStackCount`；后续 `fillPhase` 继续使用返回的单格上限分配，不修改 `ItemStack.Valid`。

- [ ] **Step 4: 运行测试并确认 GREEN**

Run: `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/core ./internal/sim -race -count=1'`

Expected: PASS；工具拆入两个合法栏位，现有普通物品装入、合成与部分拾取测试保持通过。

- [ ] **Step 5: 提交**

```bash
git add internal/core/inventory.go internal/sim/drop_test.go
git commit -m "fix: 支持工具掉落堆拆分拾取"
```

### Task 2: 同 tick 交互按全局序号执行

**Files:**
- Modify: `internal/sim/drop_test.go`
- Modify: `internal/sim/engine.go`

**Interfaces:**
- Consumes: 已按 Session/Sequence 排序的 `[]Command`、`executePlacement`、`dropSelectedItem`、`advanceDrops`。
- Produces: 放置、选栏和主动丢弃共享同一条有界有序交互阶段，拒绝仍写入既有 `TickResult.Rejected`。

- [ ] **Step 1: 写失败测试**

在 `internal/sim/drop_test.go` 增加 `TestPlaceBeforeDropUsesGlobalSequenceOrder`：选中栏只有一个可成功放置的石头，同 tick 入队较早的 `CommandPlaceBlock` 和较晚的 `CommandDropSelectedItem`，断言方块放置成功、丢弃以 `invalid_slot` 拒绝、背包清空且地面没有掉落物。

- [ ] **Step 2: 运行测试并确认 RED**

Run: `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim -run TestPlaceBeforeDropUsesGlobalSequenceOrder -count=1'`

Expected: FAIL；当前丢弃在命令循环中先消耗物品，放置在 tick 尾部得到 `invalid_block`。

- [ ] **Step 3: 写最小实现**

将现有 `placements` 切片改为 `interactions`：放置继续在命令阶段校验视角，选栏与丢弃继续在命令阶段校验玩家和栏位边界，三者按已排序顺序追加。玩家推进与订阅收敛后依次执行三种交互，再调用 `advanceDrops`，确保新掉落物仍在创建 tick 推进一次。

- [ ] **Step 4: 运行测试并确认 GREEN**

Run: `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim ./internal/world ./internal/core -race -count=1'`

Expected: PASS；现有选栏、放置、主动丢弃、40 tick 延迟和稳定 revision 测试保持通过。

- [ ] **Step 5: 提交**

```bash
git add internal/sim/engine.go internal/sim/drop_test.go
git commit -m "fix: 保持主动丢弃命令序号"
```

### Task 3: M4H 收尾门禁

**Files:**
- Modify: `openspec/changes/m4h-authoritative-item-dropping/tasks.md`

**Interfaces:**
- Consumes: 两个已通过的回归测试及 M4H 现有验证命令。
- Produces: 可继续执行主规格同步与归档的审核修复候选。

- [ ] **Step 1: 运行受影响包验证**

Run: `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/core ./internal/world ./internal/sim ./internal/server ./internal/network ./internal/client ./cmd/mcgo -race -count=1'`

- [ ] **Step 2: 运行性能与静态门禁**

Run: `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim -run "^$" -bench DropSelectedItem -benchmem -count=3 && go test ./internal/archcheck -count=1 && go vet ./... && gofmt -l .'`

- [ ] **Step 3: 运行全仓与 OpenSpec 门禁**

Run: `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race'`

Run: `openspec validate --all --strict --no-interactive`

Run: `git diff --check`

- [ ] **Step 4: 勾选审核修复任务并提交**

```bash
git add openspec/changes/m4h-authoritative-item-dropping/tasks.md
git commit -m "chore: 冻结 M4H 审核修复候选"
```
