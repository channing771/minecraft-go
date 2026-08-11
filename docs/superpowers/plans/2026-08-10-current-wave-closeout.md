# 当前波次串行收尾 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在不混入共享历史和过期视觉基线的前提下，先完成发光方块固定配方 PR，再基于其已合入的 `main` 完成程序化方块云 PR。

**Architecture:** 不重复已经完成的功能实现。发光配方复用现有干净 worktree，通过 rebase、单图视觉确认、全量门禁和 OpenSpec 归档完成交付；程序化云从最终 `origin/main` 新建 worktree，只重放八个云专属提交，修正 active OpenSpec 的视觉契约后统一生成、人工确认并提交全部受天空影响的 golden。两个 PR 严格串行，后一个只从前一个已合入的远端主线开始。

**Tech Stack:** Go 1.26（用户现有 gvm）、Git worktree、OpenSpec、WGSL/WebGPU、无窗口视觉 capture、GitHub CLI。

**Source Design:** `docs/superpowers/specs/2026-08-10-parallel-building-world-wave-design.md` 第 10 节。原有 `2026-08-10-light-block-recipe.md` 与 `2026-08-10-procedural-block-clouds.md` 保留为功能实现历史，本计划仅接管最终集成、视觉、归档和发布。

## Global Constraints

- 严格顺序：发光配方验证、归档、PR、CI、合入完成后，才创建程序化云发布 worktree。
- 执行前使用 `superpowers:using-git-worktrees`；实现阶段使用 `superpowers:subagent-driven-development`，每个任务完成后独立评审。
- 使用用户现有 gvm Go 1.26；不得下载或安装另一份 Go。
- 所有视觉命令必须使用无窗口 `--capture` 路径，不得启动或聚焦前台游戏窗口。
- 性能数值只记录，不决定退出状态；报告完整性、真实 overflow、数据丢失、固定资源上限、零分配行为测试和普通测试失败仍是门禁。
- 不放宽视觉阈值，不重试以筛选偶然结果，不使用 `--force` 或强制推送。
- 根工作树的三份用户日志始终保留且不得暂存：
  - `midscene_run/log/ios-device.log`
  - `midscene_run/log/mcp-base-tools.log`
  - `midscene_run/log/webdriver-client.log`
- 根工作树本地 `main` 上的规划提交不进入两个功能 PR；发布分支只以 fresh `origin/main` 为基线。
- 任何 rebase/cherry-pick 冲突、意外文件变化、意外 golden 变化或真实测试失败都立即停止并诊断，不自动绕过。
- 注释、GoDoc、测试说明、OpenSpec 与 PR 描述使用中文。

---

## File Map

### 发光方块固定配方

- Existing worktree: `/Users/chen/chenwork/minecraft-go/.worktrees/light-block-recipe-pr`
- Existing branch: `codex/light-block-recipe-pr`
- Existing commits to preserve: `7389643`、`4892d2c`、`9d066d0`（rebase 后 SHA 会变化，提交标题与内容保持）。
- Modify: `cmd/mcgo/capture.go` — `inventory-crafting` 夹具加入 4 个玻璃。
- Modify: `cmd/mcgo/testdata/golden/inventory-crafting.png` — 唯一允许变化的视觉基线。
- Modify: `openspec/changes/light-block-recipe/tasks.md` — 完成视觉与收尾任务。
- Archive: `openspec/changes/archive/2026-08-10-light-block-recipe/**`。
- Modify on sync/archive:
  - `openspec/specs/authoritative-crafting/spec.md`
  - `openspec/specs/static-block-light/spec.md`
  - `openspec/specs/voxel-visual-presentation/spec.md`

### 程序化方块云

- Source worktree: `/Users/chen/chenwork/minecraft-go/.worktrees/procedural-block-clouds`
- New worktree: `/Users/chen/chenwork/minecraft-go/.worktrees/procedural-block-clouds-pr`
- New branch: `codex/procedural-block-clouds-pr`
- Replay exactly: `d300245`、`47081c7`、`d4a3a96`、`f99180d`、`6ca72b0`、`4f40e30`、`71ff5cf`、`4bab100`。
- Modify: `internal/render/daylight.go`、`internal/render/daylight_test.go` — 固定时间偏移及测试。
- Modify: `internal/render/renderer.go`、`internal/render/sky_test.go` — sky uniform 与无窗口契约。
- Modify: `internal/render/shader/sky.wgsl` — 固定程序化云算法。
- Modify: `cmd/mcgo/app.go` — 权威世界时间与相机坐标传递。
- Modify: `openspec/changes/procedural-block-clouds/proposal.md`、`design.md`、`tasks.md` — 订正视觉收尾契约。
- Verify unchanged: `openspec/changes/procedural-block-clouds/specs/celestial-sky-presentation/spec.md` — 现有运行时契约已经完整，不向行为 spec 混入交付流程。
- Modify only after manual approval:
  - `cmd/mcgo/testdata/golden/terrain-noon.png`
  - `cmd/mcgo/testdata/golden/hud-hotbar-health.png`
  - `cmd/mcgo/testdata/golden/avatar-nametag.png`
  - `cmd/mcgo/testdata/golden/inventory-crafting.png`
  - `cmd/mcgo/testdata/golden/debug-panel.png`
  - `cmd/mcgo/testdata/golden/materials-showcase.png`
  - `cmd/mcgo/testdata/golden/oak-grove.png`
- Must remain byte-exact:
  - `cmd/mcgo/testdata/golden/skylight-tunnel.png`
  - `cmd/mcgo/testdata/golden/block-light-room.png`
  - `cmd/mcgo/testdata/golden/target-block-feedback.png`
- Archive: `openspec/changes/archive/2026-08-10-procedural-block-clouds/**`。
- Modify on sync/archive: `openspec/specs/celestial-sky-presentation/spec.md`。

---

### Task 1: 将发光配方分支重放到 fresh `origin/main`

**Files:**
- Preserve: existing three commits and two uncommitted files in the light worktree.

**Interfaces:**
- Consumes: current remote `main` with deterministic oak trees and container world-Y fixes.
- Produces: the same three light feature commits rebased onto fresh `origin/main`, plus the same two intentional unstaged edits.

- [ ] **Step 1: 使用现有隔离 worktree 并核对唯一脏范围**

```bash
cd /Users/chen/chenwork/minecraft-go/.worktrees/light-block-recipe-pr
git status --short --branch
git diff --name-only
git diff --check
```

Expected: 仅 `cmd/mcgo/capture.go` 与 `openspec/changes/light-block-recipe/tasks.md` 未提交；branch 为 `codex/light-block-recipe-pr`。

- [ ] **Step 2: fresh fetch 后以原生 autostash rebase**

```bash
git fetch origin main
git rebase --autostash origin/main
```

Expected: 三个提交无冲突重放；autostash 自动恢复两处未提交改动。若出现冲突，保持现场并停止，不执行 `rebase --skip`、重置或强推。

- [ ] **Step 3: 核对 rebase 后提交和文件边界**

```bash
git log --format='%s' origin/main..HEAD
git diff --name-only
git diff --name-status origin/main...HEAD
git diff --check
openspec validate light-block-recipe --strict --no-interactive
```

Expected: 三个提交标题依次仍为“规划发光方块固定配方”“增加发光方块固定配方”“展示八条固定合成配方”；未提交文件仍只有两处；feature diff 不含共享批次提交或其他 golden。

### Task 2: 生成并人工确认发光配方唯一视觉变化

**Files:**
- Modify: `cmd/mcgo/capture.go`
- Modify after approval: `cmd/mcgo/testdata/golden/inventory-crafting.png`
- Modify: `openspec/changes/light-block-recipe/tasks.md`

**Interfaces:**
- Produces: fresh post-main `inventory-crafting` candidate；其他九个场景 byte-exact。

- [ ] **Step 1: 运行 feature-focused 无窗口测试**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/core ./internal/render ./cmd/mcgo -run "Recipe|Craft|Inventory|HUD|Capture" -race -count=1'
```

Expected: exit 0；不得出现前台窗口。

- [ ] **Step 2: 只生成 candidate，不直接覆盖 golden**

在同一 shell 中执行一次：

```bash
export LIGHT_VISUAL_DIR="$(mktemp -d /private/tmp/mcgo-light-final.XXXXXX)"
set +e
zsh -ic 'gvm use go1.26.0 >/dev/null && make visual-check VISUAL_OUT="$LIGHT_VISUAL_DIR"'
LIGHT_VISUAL_EXIT=$?
set -e
test "$LIGHT_VISUAL_EXIT" -eq 2
```

Expected: 因尚未更新的 `inventory-crafting.png` 唯一差异返回 2；这是预期 candidate 证据，不重跑、不放宽阈值。

- [ ] **Step 3: 机械核对变化集合**

```bash
for LIGHT_GOLDEN in cmd/mcgo/testdata/golden/*.png; do
  LIGHT_NAME="${LIGHT_GOLDEN##*/}"
  if ! cmp -s "$LIGHT_GOLDEN" "$LIGHT_VISUAL_DIR/$LIGHT_NAME"; then
    echo "$LIGHT_NAME"
  fi
done
```

Expected: 只输出 `inventory-crafting.png`。若出现其他文件，停止并诊断共享场景状态，不能顺手接受。

- [ ] **Step 4: 人工视觉确认点**

使用 `view_image` 只读打开 `$LIGHT_VISUAL_DIR/inventory-crafting.png`，向用户展示绝对路径与 SHA-256。必须看见完整八行、末行为 4 玻璃 → 4 发光方块、无重叠、无裁切；收到用户明确确认后才继续。

- [ ] **Step 5: 只复制已确认的一张 PNG 并验证**

```bash
cp "$LIGHT_VISUAL_DIR/inventory-crafting.png" cmd/mcgo/testdata/golden/inventory-crafting.png
git diff --name-only -- cmd/mcgo/testdata/golden
export LIGHT_CHECK_DIR="$(mktemp -d /private/tmp/mcgo-light-check.XXXXXX)"
zsh -ic 'gvm use go1.26.0 >/dev/null && make visual-check VISUAL_OUT="$LIGHT_CHECK_DIR"'
git diff --check
```

Expected: golden diff 只有 `inventory-crafting.png`；十个场景全部在阈值内，命令 exit 0。

- [ ] **Step 6: 精确提交视觉结果**

```bash
git add cmd/mcgo/capture.go \
  cmd/mcgo/testdata/golden/inventory-crafting.png \
  openspec/changes/light-block-recipe/tasks.md
git diff --cached --check
git diff --cached --name-only
git commit -m "test: 更新八行合成视觉基线"
```

Expected: staged/commit 范围精确为上述三文件。

### Task 3: 发光配方完整门禁、独立评审和 OpenSpec 归档

**Files:**
- Modify: `openspec/changes/light-block-recipe/tasks.md`
- Sync: three main specs listed in File Map.
- Archive: `openspec/changes/archive/2026-08-10-light-block-recipe/**`

- [ ] **Step 1: 运行受影响包和架构门禁**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/core ./internal/render ./internal/client ./internal/network ./internal/server ./cmd/mcgo -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
```

Expected: 全部 exit 0；失败即诊断，不静默重跑。

- [ ] **Step 2: 运行全仓 correctness 门禁**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go vet ./...'
gofmt -l .
git diff --check
openspec validate light-block-recipe --strict --no-interactive
openspec validate --all --strict --no-interactive
```

Expected: 所有命令 exit 0；`gofmt -l .` 无输出。

- [ ] **Step 3: 记录相关 benchmark，不用数值阻止交付**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/core -run "^$" -bench BenchmarkInventoryCraftWorstCase -benchmem -count=5'
```

记录五轮结果。性能升降只写报告，不改变退出状态；普通测试、真实 overflow 或数据丢失仍须通过。

- [ ] **Step 4: 请求 fresh 独立累计评审**

评审范围为 `origin/main...HEAD`，重点核对：稳定 ID 8、4 Glass → 4 LightBlock、失败原子性、Memory/TCP parity、协议/四类 schema 不变、八行固定容量与命中、唯一 golden。发现必须先修复、复验并做 scoped re-review；评审 clean 前不得归档。

- [ ] **Step 5: 勾选 5.1/5.2 并同步三份主规格**

使用 `openspec-sync-specs`，只读取一次同步 instructions：

```bash
openspec instructions specs --change light-block-recipe --json
```

按返回的 `existingOutputPaths` 将三个 delta 智能合并至主规格，不覆盖主规格中其他已归档 change 的内容。然后运行：

```bash
openspec validate light-block-recipe --strict --no-interactive
openspec validate --all --strict --no-interactive
git diff --check
```

- [ ] **Step 6: 归档并提交**

使用 `openspec-archive-change`：

```bash
openspec archive light-block-recipe -y
test -f openspec/changes/archive/2026-08-10-light-block-recipe/.openspec.yaml
openspec list --json
openspec validate --all --strict --no-interactive
git diff --check
git status --short --branch
git add openspec/changes/archive/2026-08-10-light-block-recipe \
  openspec/specs/authoritative-crafting/spec.md \
  openspec/specs/static-block-light/spec.md \
  openspec/specs/voxel-visual-presentation/spec.md
git diff --cached --check
git commit -m "docs: 归档发光方块固定配方"
```

Expected: active list 不含 `light-block-recipe`；归档 tasks 全勾；`.openspec.yaml` 保留；worktree clean。

### Task 4: 发布并先合入发光配方 PR

- [ ] **Step 1: 发布前 fresh 范围核对**

```bash
git fetch origin main
git status --short --branch
git diff --name-status origin/main...HEAD
git log --oneline origin/main..HEAD
```

Expected: 只含发光配方功能、测试、唯一视觉 PNG、active change 归档和三份主规格同步；不含程序化云、共享批次提交或根工作树日志。

- [ ] **Step 2: 普通 push 并创建 ready PR**

```bash
git push -u origin codex/light-block-recipe-pr
gh pr create --base main --head codex/light-block-recipe-pr --fill
gh pr checks --watch --fail-fast
```

Expected: push 无 force；PR base 为 `main`；CI 全绿。若 remote `main` 在 push 前进，先回到 Task 1 rebase 和验证，不直接合并过期分支。

- [ ] **Step 3: 用户授权后合入，并锁定新的远端主线**

```bash
gh pr merge --merge --delete-branch
git fetch origin main
git log -1 --oneline origin/main
```

Expected: 发光配方 PR head 已成为 `origin/main` 祖先。根工作树及其本地规划提交、三份用户日志均不移动。

### Task 5: 从已含发光配方的远端主线重建程序化云发布分支

**Files:**
- Create worktree/branch described in File Map.
- Replay only eight cloud-specific commits.

- [ ] **Step 1: 确认严格串行前置条件**

```bash
cd /Users/chen/chenwork/minecraft-go
git fetch origin main
test "$(gh pr view codex/light-block-recipe-pr --json state --jq .state)" = "MERGED"
test ! -e /Users/chen/chenwork/minecraft-go/.worktrees/procedural-block-clouds-pr
test -z "$(git branch --list codex/procedural-block-clouds-pr)"
```

Expected: light PR 为 merged；目标 worktree 和 branch 均不存在。若已存在，不删除，先停止核对来源。

- [ ] **Step 2: 从 fresh `origin/main` 新建 worktree**

```bash
git worktree add /Users/chen/chenwork/minecraft-go/.worktrees/procedural-block-clouds-pr \
  -b codex/procedural-block-clouds-pr origin/main
cd /Users/chen/chenwork/minecraft-go/.worktrees/procedural-block-clouds-pr
git status --short --branch
```

Expected: clean，HEAD 精确等于 fresh `origin/main`。

- [ ] **Step 3: 按固定顺序只重放八个云提交**

```bash
git cherry-pick d300245 47081c7 d4a3a96 f99180d 6ca72b0 4f40e30 71ff5cf 4bab100
```

Expected: 全部无冲突。发生任何冲突立即停止，不使用 `--theirs`、`--ours`、`--skip` 或强推。

- [ ] **Step 4: 核对重建范围**

```bash
git status --short --branch
git diff --name-status origin/main...HEAD
git log --format='%s' origin/main..HEAD
openspec validate procedural-block-clouds --strict --no-interactive
```

Expected: diff 仅含 File Map 中六个 Go/WGSL 文件和 active cloud change；不含旧共享设计提交、不含任何 PNG。

### Task 6: 修正程序化云 OpenSpec 的视觉收尾契约

**Files:**
- Modify: `openspec/changes/procedural-block-clouds/proposal.md`
- Modify: `openspec/changes/procedural-block-clouds/design.md`
- Modify: `openspec/changes/procedural-block-clouds/tasks.md`
- Verify unchanged: `openspec/changes/procedural-block-clouds/specs/celestial-sky-presentation/spec.md`

- [ ] **Step 1: 使用 `openspec-update-change` 修正三处规划产物**

做最小一致性修改：

1. `proposal.md` 明确：在 storage、oak、light 均合入后的最终 `main` 统一生成全部受天空影响的视觉基线，并逐张人工确认。
2. `design.md` 删除 Non-Goal 中“不改变 golden 资产”的错误表述，改为“不改变视觉阈值、不接受非天空漂移”；Migration 改为“无需协议/存档迁移，但必须在最终集成基线上更新受天空影响的 golden”。
3. `tasks.md` 保留已完成 1–4；将原 5.1 拆为“最终视觉 candidate 与人工确认”“全量门禁与独立评审”“同步归档”三个未完成任务。
4. `spec.md` 已完整描述可观察云行为；golden 更新是交付流程，不向行为 spec 添加流程性要求。

- [ ] **Step 2: 校验产物一致性并提交**

```bash
openspec validate procedural-block-clouds --strict --no-interactive
openspec validate --all --strict --no-interactive
git diff --check
git add openspec/changes/procedural-block-clouds/proposal.md \
  openspec/changes/procedural-block-clouds/design.md \
  openspec/changes/procedural-block-clouds/tasks.md
git diff --cached --check
git commit -m "docs: 对齐程序化云视觉收尾契约"
```

Expected: active change strict-valid；提交只含三份规划文档。

### Task 7: 在最终主线生成并人工确认全部云视觉基线

**Files:**
- Modify after approval: seven sky-visible golden files listed in File Map.
- Preserve byte-exact: three non-sky/occluded golden files listed in File Map.

- [ ] **Step 1: 运行云 focused correctness 与固定资源测试**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/render ./cmd/mcgo -run "Cloud|Sky|Daylight|Capture|RendererRenderDoesNotAllocate" -race -count=1'
```

Expected: exit 0；固定哈希计数 `72/69/62/53`、活动 `184`、填充 `920/4096`、X/Z 视差、遮挡、112-byte uniform 和零分配行为仍成立。

- [ ] **Step 2: 记录 sky benchmark**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/render -run "^$" -bench "^BenchmarkSkyRender$" -benchmem -count=5'
```

记录五轮数据；数值变化不作为门禁。零分配正确性由上一步行为测试负责。

- [ ] **Step 3: 只生成 fresh candidate，不覆盖 golden**

```bash
export CLOUD_VISUAL_DIR="$(mktemp -d /private/tmp/mcgo-cloud-final.XXXXXX)"
set +e
zsh -ic 'gvm use go1.26.0 >/dev/null && make visual-check VISUAL_OUT="$CLOUD_VISUAL_DIR"'
CLOUD_VISUAL_EXIT=$?
set -e
test "$CLOUD_VISUAL_EXIT" -eq 2
```

Expected: 因程序化云的预期视觉变化返回 2；只运行一次，不筛选结果。

- [ ] **Step 4: 精确核对变化和不变集合**

```bash
for CLOUD_NAME in terrain-noon.png hud-hotbar-health.png avatar-nametag.png \
  inventory-crafting.png debug-panel.png materials-showcase.png oak-grove.png; do
  cmp -s "cmd/mcgo/testdata/golden/$CLOUD_NAME" "$CLOUD_VISUAL_DIR/$CLOUD_NAME" && exit 1
done
for CLOUD_NAME in skylight-tunnel.png block-light-room.png target-block-feedback.png; do
  cmp -s "cmd/mcgo/testdata/golden/$CLOUD_NAME" "$CLOUD_VISUAL_DIR/$CLOUD_NAME"
done
```

Expected: 七个含天空场景确实变化；三个室内或天空被遮挡场景 byte-exact。任一集合不符即停止诊断。

- [ ] **Step 5: 七图人工视觉确认点**

逐一使用 `view_image` 打开 `$CLOUD_VISUAL_DIR` 下七张变化图片，提供每张绝对路径与 SHA-256。确认：方块云世界锚定观感合理、无遮挡错误、无地形穿透、HUD/背包/调试面板无裁切、oak-grove 树冠正常、materials-showcase 仅天空区域出现云。收到用户对全部七图的明确确认后才继续。

- [ ] **Step 6: 只复制七张已确认 PNG 并验证全部场景**

```bash
for CLOUD_NAME in terrain-noon.png hud-hotbar-health.png avatar-nametag.png \
  inventory-crafting.png debug-panel.png materials-showcase.png oak-grove.png; do
  cp "$CLOUD_VISUAL_DIR/$CLOUD_NAME" "cmd/mcgo/testdata/golden/$CLOUD_NAME"
done
git diff --name-only -- cmd/mcgo/testdata/golden
export CLOUD_CHECK_DIR="$(mktemp -d /private/tmp/mcgo-cloud-check.XXXXXX)"
zsh -ic 'gvm use go1.26.0 >/dev/null && make visual-check VISUAL_OUT="$CLOUD_CHECK_DIR"'
git diff --check
```

Expected: golden diff 精确为七张；十个视觉场景全部通过。

- [ ] **Step 7: 勾选视觉任务并提交**

```bash
git add cmd/mcgo/testdata/golden/terrain-noon.png \
  cmd/mcgo/testdata/golden/hud-hotbar-health.png \
  cmd/mcgo/testdata/golden/avatar-nametag.png \
  cmd/mcgo/testdata/golden/inventory-crafting.png \
  cmd/mcgo/testdata/golden/debug-panel.png \
  cmd/mcgo/testdata/golden/materials-showcase.png \
  cmd/mcgo/testdata/golden/oak-grove.png \
  openspec/changes/procedural-block-clouds/tasks.md
git diff --cached --check
git diff --cached --name-only
git commit -m "test: 更新程序化云视觉基线"
```

### Task 8: 程序化云全量门禁、独立评审和归档

**Files:**
- Modify: `openspec/changes/procedural-block-clouds/tasks.md`
- Sync: `openspec/specs/celestial-sky-presentation/spec.md`
- Archive: `openspec/changes/archive/2026-08-10-procedural-block-clouds/**`

- [ ] **Step 1: 运行受影响包、架构和全仓门禁**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/render ./cmd/mcgo -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go vet ./...'
gofmt -l .
git diff --check
openspec validate procedural-block-clouds --strict --no-interactive
openspec validate --all --strict --no-interactive
```

Expected: 全部 exit 0；`gofmt -l .` 无输出；失败即诊断，不重跑筛选。

- [ ] **Step 2: 请求 fresh 独立累计代码与视觉评审**

评审范围为 `origin/main...HEAD`，必须覆盖：固定哈希输入与精确样本计数、Y=192/cell16/alpha0.82、X/Z 视差、112-byte uniform、星/月/日与 terrain 遮挡顺序、稳定帧零分配、七张变化 golden、三张 byte-exact golden，以及 OpenSpec 视觉契约。发现先修复、复验并做 scoped re-review；评审 clean 前不得归档。

- [ ] **Step 3: 勾选剩余任务并同步唯一主规格**

使用 `openspec-sync-specs`，只读取一次同步 instructions：

```bash
openspec instructions specs --change procedural-block-clouds --json
```

将 delta 智能合并至 `openspec/specs/celestial-sky-presentation/spec.md`，保留主规格中其他已归档 change 的内容。然后运行：

```bash
openspec validate procedural-block-clouds --strict --no-interactive
openspec validate --all --strict --no-interactive
git diff --check
```

- [ ] **Step 4: 归档并提交**

使用 `openspec-archive-change`：

```bash
openspec archive procedural-block-clouds -y
test -f openspec/changes/archive/2026-08-10-procedural-block-clouds/.openspec.yaml
openspec list --json
openspec validate --all --strict --no-interactive
git diff --check
git add openspec/changes/archive/2026-08-10-procedural-block-clouds \
  openspec/specs/celestial-sky-presentation/spec.md
git diff --cached --check
git commit -m "docs: 归档程序化方块云"
git status --short --branch
```

Expected: active list 不含 `procedural-block-clouds`；归档 tasks 全勾；`.openspec.yaml` 保留；worktree clean。

### Task 9: 发布程序化云 PR 并完成波次

- [ ] **Step 1: 发布前 fresh 范围核对**

```bash
git fetch origin main
git status --short --branch
git diff --name-status origin/main...HEAD
git log --oneline origin/main..HEAD
```

Expected: 只含程序化云代码、测试、OpenSpec 修订/归档、唯一主规格同步和七张已确认 golden；不含容器、发光配方实现提交、共享批次提交或根工作树日志。

- [ ] **Step 2: 普通 push 并创建 ready PR**

```bash
git push -u origin codex/procedural-block-clouds-pr
gh pr create --base main --head codex/procedural-block-clouds-pr --fill
gh pr checks --watch --fail-fast
```

Expected: push 无 force；PR base 为已经包含发光配方的 `main`；CI 全绿。

- [ ] **Step 3: 用户授权后合入并核对远端主线**

```bash
gh pr merge --merge --delete-branch
git fetch origin main
git log -1 --oneline origin/main
git merge-base --is-ancestor HEAD origin/main
```

Expected: 程序化云 PR head 已成为 `origin/main` 祖先；两个功能 PR 都已完成，根工作树本地规划提交和三份用户日志仍未被触碰。

---

## Final Self-Review Checklist

- [ ] 发光配方只更新一张 golden，程序化云只更新七张含天空 golden。
- [ ] 两个 PR 都从各自执行时的 fresh `origin/main` 开始，云 PR 明确在 light PR 合入后创建。
- [ ] 没有把本地 `main` 的规划提交、共享历史或三份用户日志带进功能 PR。
- [ ] 所有视觉 candidate 都先生成、机械核对、人工确认，再复制为 golden。
- [ ] active OpenSpec 在归档前与实现和视觉流程一致；同步使用 `existingOutputPaths`，归档保留 `.openspec.yaml`。
- [ ] 性能数值只记录；正确性、固定资源、overflow、数据丢失、race、vet、架构和 OpenSpec 仍全绿。
- [ ] 没有前台测试、阈值放宽、静默重跑、强推或无关重构。
