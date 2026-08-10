# 容器世界 Y 校验修复 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复 chunk codec 把 section-local Y 当作世界 Y，导致合法高层熔炉与箱子被误判损坏的问题。

**Architecture:** `world.BlockPosFromChunkIndex` 继续作为紧凑索引到世界位置的唯一还原入口；容器校验仅从 `BlockPos.Local()` 取局部 X/Z，并把 `BlockPos.Y` 传给要求世界 Y 的 `Chunk.BlockAt`。修复不增加 helper、迁移或格式版本，只在共享信任边界一次性修正两类容器。

**Tech Stack:** Go 1.26、现有 `internal/storage` chunk codec、OpenSpec、Go race detector。

## Global Constraints

- 从设计提交 `dc61422` 创建独立 worktree 与分支 `codex/fix-container-world-y`；执行时先使用 `superpowers:using-git-worktrees`。
- 使用用户现有 gvm Go 1.26 环境，不下载或安装另一份 Go。
- 只修改本计划列出的 storage、OpenSpec 与归档文件；保留三份 `midscene_run/log/*.log` 用户改动。
- chunk schema 保持 `8`、协议保持 `15`，磁盘字节、索引定义与 migration registry 均不变。
- 越界、重复、非法槽和错误方块引用继续返回 `storage.ErrCorrupt`，不得放宽信任边界。
- Go 注释、GoDoc、测试说明与文档使用中文；Go 标识符和既有 wire/schema 术语保留英文。
- 不关闭或绕过 Hook；不启动前台游戏窗口。

---

## File Map

- Create: `openspec/changes/fix-container-world-y/.openspec.yaml` — change 元数据。
- Create: `openspec/changes/fix-container-world-y/proposal.md` — 问题、目标、非目标与兼容性。
- Create: `openspec/changes/fix-container-world-y/design.md` — 世界坐标还原与信任边界设计。
- Create: `openspec/changes/fix-container-world-y/specs/authoritative-furnaces/spec.md` — 高层熔炉可持久化场景。
- Create: `openspec/changes/fix-container-world-y/specs/authoritative-chests/spec.md` — 高层箱子可持久化场景。
- Create: `openspec/changes/fix-container-world-y/tasks.md` — 本计划的可勾选任务。
- Create: `internal/storage/chunk_container_height_test.go` — 熔炉和箱子跨垂直 section 的共同回归。
- Modify: `internal/storage/chunk_codec.go:151-158,251-258` — 使用 `pos.Y` 读取容器方块。
- Archive: `openspec/changes/archive/2026-08-10-fix-container-world-y/` — 验证后归档的 change。
- Modify on archive: `openspec/specs/authoritative-furnaces/spec.md`、`openspec/specs/authoritative-chests/spec.md`。

### Task 1: 建立独立 OpenSpec change

**Files:**
- Create: `openspec/changes/fix-container-world-y/**`

**Interfaces:**
- Consumes: `world.BlockPosFromChunkIndex(core.ChunkPos, uint32) (core.BlockPos, bool)` 与 `(*world.Chunk).BlockAt(int, int32, int) core.BlockID` 的现有契约。
- Produces: 一个 strict-valid active change；后续实现只能修世界 Y 传递，不得改格式。

- [ ] **Step 1: 创建 change 骨架**

Run:

```bash
openspec new change fix-container-world-y
openspec instructions proposal --change fix-container-world-y --json
```

Expected: `openspec/changes/fix-container-world-y/` 存在，未修改其他 active change。

- [ ] **Step 2: 写 proposal、design 与 delta specs**

`proposal.md` 必须明确：合法非零 section 容器被误拒绝；只修 codec 校验；schema v8、protocol v15 与字节布局不变；不增加离线扫描。

两份 delta spec 分别加入以下可判定场景，方块类型替换为 Furnace/Chest：

```markdown
### Requirement: 容器索引按世界高度校验
系统 SHALL 把持久化容器的紧凑方块索引还原为完整世界坐标，并以该世界 Y 验证对应方块。

#### Scenario: 非零垂直 section 的合法容器可往返
- **GIVEN** 一个活动容器位于 `core.MinY` 与 `core.MaxY` 之间的任意合法垂直 section
- **WHEN** 当前 chunk schema 编码并解码该区块
- **THEN** 容器、方块索引和内容 MUST 无损往返，且 MUST NOT 因 section-local Y 被误判损坏

#### Scenario: 索引仍必须指向正确方块
- **WHEN** 活动容器索引越界、重复或没有指向对应容器方块
- **THEN** codec MUST 返回 `ErrCorrupt`，且 MUST NOT 接受或改写该记录
```

`tasks.md` 只列：RED 回归、两行 GREEN 修复、mutation、storage/full gates、同步归档。

- [ ] **Step 3: 严格校验 active change**

Run:

```bash
openspec validate fix-container-world-y --strict --no-interactive
openspec validate --all --strict --no-interactive
git diff --check
```

Expected: change 与全量 OpenSpec 均通过，无格式错误。

- [ ] **Step 4: 提交 change 产物**

```bash
git add openspec/changes/fix-container-world-y
git commit -m "docs: 规划容器世界 Y 校验修复"
```

### Task 2: 用跨 section 往返测试复现根因

**Files:**
- Create: `internal/storage/chunk_container_height_test.go`

**Interfaces:**
- Consumes: 同包测试 helper `furnaceBlockIndex`、`encodeChunkPayload`、`decodeChunkPayload`。
- Produces: `TestChunkCodecRoundTripsContainersAcrossWorldSections`，恢复 local-Y 旧实现时稳定失败。

- [ ] **Step 1: 写熔炉和箱子的表驱动 RED 测试**

```go
package storage

import (
	"fmt"
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

func TestChunkCodecRoundTripsContainersAcrossWorldSections(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	for _, y := range []int32{core.MinY + 1, 18, core.MaxY - 2} {
		t.Run(fmt.Sprintf("y_%d", y), func(t *testing.T) {
			chunk := world.NewChunk(key.Pos)
			furnaceIndex := furnaceBlockIndex(t, key.Pos, 1, y, 2)
			chestIndex := furnaceBlockIndex(t, key.Pos, 3, y, 4)
			chunk.SetBlock(1, y, 2, core.FurnaceID)
			chunk.SetBlock(3, y, 4, core.ChestID)
			chunk.SetFurnace(0, world.FurnaceSlot{
				Generation: 1, Active: true, BlockIndex: furnaceIndex,
			})
			chunk.SetChest(0, world.ChestSlot{
				Generation: 1, Active: true, BlockIndex: chestIndex,
			})

			encoded, err := encodeChunkPayload(ChunkSave{Key: key, Revision: 7, Chunk: chunk})
			if err != nil {
				t.Fatalf("编码 Y=%d 的合法容器: %v", y, err)
			}
			decoded, err := decodeChunkPayload(key, 7, encoded)
			if err != nil {
				t.Fatalf("解码 Y=%d 的合法容器: %v", y, err)
			}
			if decoded.Chunk.Furnace(0) != chunk.Furnace(0) ||
				decoded.Chunk.Chest(0) != chunk.Chest(0) {
				t.Fatalf("Y=%d 的容器槽往返改变", y)
			}
		})
	}
}
```

三个 Y 都刻意满足 `pos.Local().Y != pos.Y`。

- [ ] **Step 2: 运行测试并确认 RED 原因准确**

Run:

```bash
go test ./internal/storage -run TestChunkCodecRoundTripsContainersAcrossWorldSections -count=1
```

Expected: FAIL，第一项错误包含 `furnace slot 0 does not point at a furnace block`；不得出现编译错误或无关失败。

### Task 3: 在共享校验边界做两行 GREEN 修复

**Files:**
- Modify: `internal/storage/chunk_codec.go:151-158,251-258`
- Test: `internal/storage/chunk_container_height_test.go`

**Interfaces:**
- Consumes: Task 2 的失败测试。
- Produces: 熔炉与箱子都以 world Y 校验；其余 codec 语义不变。

- [ ] **Step 1: 修改两处坐标使用**

两处都使用同一形态：

```go
lx, _, lz := pos.Local()
if chunk.BlockAt(lx, pos.Y, lz) != core.FurnaceID { // 箱子分支使用 core.ChestID
	return fmt.Errorf("furnace slot %d does not point at a furnace block", slot)
}
```

不得抽取新 helper；两个调用点已经是完整修复范围。

- [ ] **Step 2: 运行 focused 与 storage race 测试**

Run:

```bash
gofmt -w internal/storage/chunk_codec.go internal/storage/chunk_container_height_test.go
go test ./internal/storage -run 'TestChunkCodecRoundTripsContainersAcrossWorldSections|TestChunkCodecRejectsInvalid(Furnace|Chest)Slots' -race -count=1
go test ./internal/storage -race -count=1
```

Expected: 全部 PASS。

- [ ] **Step 3: 做 mutation 证伪并恢复**

临时把任一分支的绑定恢复为 `lx, y, lz := pos.Local()`，并把读取改回 `chunk.BlockAt(lx, int32(y), lz)`；运行 focused 测试，Expected: FAIL。立即恢复 `_` 与 `pos.Y`，重新运行 focused 测试，Expected: PASS。mutation 不得提交。

- [ ] **Step 4: 提交测试与修复**

```bash
git add internal/storage/chunk_codec.go internal/storage/chunk_container_height_test.go openspec/changes/fix-container-world-y/tasks.md
git commit -m "fix: 使用世界高度校验持久容器"
```

### Task 4: 全量验证、同步与归档

**Files:**
- Modify: `openspec/changes/fix-container-world-y/tasks.md`
- Archive: `openspec/changes/archive/2026-08-10-fix-container-world-y/**`
- Modify: `openspec/specs/authoritative-furnaces/spec.md`
- Modify: `openspec/specs/authoritative-chests/spec.md`

**Interfaces:**
- Consumes: 已通过 mutation 的两行修复。
- Produces: clean worktree、归档 change、可独立合入的分支。

- [ ] **Step 1: 运行完整门禁**

```bash
go test ./internal/storage -race -count=1
go test ./internal/archcheck -count=1
go test ./... -race
go vet ./...
gofmt -l .
git diff --check
openspec validate --all --strict --no-interactive
```

Expected: 所有命令 exit 0，`gofmt -l .` 无输出。

- [ ] **Step 2: 勾选 tasks 并归档**

确认 `tasks.md` 全部为 `[x]` 后运行：

```bash
openspec archive fix-container-world-y -y
openspec validate --all --strict --no-interactive
openspec list --json
git diff --check
```

Expected: active list 不再包含该 change；归档目录日期为 `2026-08-10`；两份主规格包含世界 Y 场景。

- [ ] **Step 3: 提交归档结果**

```bash
git add openspec/changes/archive/2026-08-10-fix-container-world-y \
  openspec/specs/authoritative-furnaces/spec.md \
  openspec/specs/authoritative-chests/spec.md
git commit -m "docs: 归档容器世界 Y 校验修复"
```

- [ ] **Step 4: 最终状态检查**

```bash
git status --short --branch
git log --oneline --decorate -4
```

Expected: 除用户原有日志外无未提交改动；分支可独立推送与创建 PR。
