# Mornlea Project Rename Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在保留 M4P Rust mesh/light 与最新主线程序化方块云的前提下，把当前项目完整改名为 Mornlea，并安全迁移默认本机配置与玩家档案，不改变玩法、协议、存档、视觉或性能工作负载。

**Architecture:** 第一项工作只合并 `origin/main` 到 M4P 基线并冻结统一 artifact；随后归档已完成的 M4O/M4P，建立单一 M4Q OpenSpec change。数据迁移仍留在现有 `config`/`profile` 包内，复用现有编码器和原子临时文件流程，只为默认路径发布使用 stdlib `os.Link` 的 no-clobber 语义。Go module、两个命令目录、Rust crate/C ABI、Make/CI/Hook 和 archcheck 在一个原子任务中机械切换；历史资料与兼容 magic 保持原文/原值。

**Tech Stack:** Go 1.26、Rust 1.97.1 edition 2024、Cargo workspace、C ABI/cgo、Make、GitHub Actions、OpenSpec 1.7.0、macOS `os.UserConfigDir`/`os.Link`、GitHub CLI、GitNexus 1.6.3。

## Global Constraints

- 实施分支为 `codex/mornlea-project-rename`；设计提交为 `6fbbb970d017d5fc2d3fe05cff61d0ed81cb18cc`。
- 本计划必须在 Task 1 前作为独立 planning commit 跟踪；实施者先用 `git ls-files --error-unmatch docs/superpowers/plans/2026-08-12-mornlea-project-rename.md` 机械确认，不得在后续 Files 范围中顺带提交它。
- 功能基线必须包含 M4P final `4c77539280d7e093001c2c9f3fc02da513fd3715` 与执行 Task 1 时最新的 `origin/main`；执行前已冻结主线为 `e15541686b184df97d7ec2c74efc3e20979507a7`。
- 第一项实施提交只允许合并主线，不得夹带品牌、路径、文档或数据迁移修改。若执行时 `origin/main` 已前进，先更新本计划与 M4Q 基线证据再合并。
- 最终品牌为 `Mornlea`，机器标识为 `mornlea`，GitHub 仓库为 `channing771/mornlea`，Go module 为 `github.com/channing771/mornlea`。
- 客户端入口/产物为 `cmd/mornlea` / `bin/mornlea`；专用服务端为 `cmd/mornlea-server` / `bin/mornlea-server`。不得保留 `mcgo`、`mcgod` wrapper、symlink、module alias、旧 C symbol 或旧环境变量 fallback。
- 新默认用户目录为 `os.UserConfigDir()/mornlea`；只在新默认文件缺失时读取并复制旧 `minecraft-go` 文件。旧目录与旧文件不得移动、覆盖或删除。
- 新默认文件存在即为权威来源；读取、解码或权限失败必须报错，不得回退旧文件。显式 `--config` 必须完全跳过默认路径迁移。
- 迁移不得建立通用 migration framework、单实现 interface、后台同步或双向同步。config/profile 各自复用已有 codec；no-clobber 发布使用同目录临时文件加 stdlib `os.Link`。
- 保持协议 v15、区块 schema v8、玩家 schema v6、world metadata v2、benchmark scenario v15、ABI version 1、status 0..9、packed quad、fixture、golden、baseline 与阈值不变。
- 保持 `CHNK`、`MCGC`、`MCGM`、`MCPL`、`MCGR`、`MCGB`、`MGM1` 与 `.mcgo-world-backup-v1.json` 不变；这些是兼容身份，不是品牌残留。
- `docs/superpowers/**`、`openspec/changes/archive/**` 与明确列出的历史性能证据保持原文。不得批量替换历史资料或 Git 历史。
- benchmark/perfcheck 的性能数值只记录；身份/provenance、报告结构、真实 overflow、数据丢失、I/O、fixture/hash、构建、并发与非数值测试失败仍阻断。
- 自动测试不得打开或聚焦交互式游戏窗口；视觉验证只用既有 headless capture，绝不运行 `make visual-update`。
- Go 代码、测试说明、错误和当前开发文档使用中文；标识符、C ABI、协议字段与外部 API 名保留英文。
- 每个任务只提交其 `Files` 列表。发现计划外生产文件、接口、依赖或行为时立即停止，先更新 M4Q proposal/spec/design/tasks 和本计划。
- 每个任务完成前写报告到 `.superpowers/sdd/2026-08-12-m4q-mornlea-project-rename/task-N-report.md`；Task 1 先把该精确目录写入 Git common-dir `info/exclude` 并用 `git check-ignore` 验证，报告不进入提交。
- GitHub 仓库改名、本地根目录移动和 GitNexus 重建只能在源码 PR 合并后执行，不属于源码提交。

## Target File Map

| 路径 | 职责 |
| --- | --- |
| `internal/config/config.go` | 新默认 config 路径、默认迁移、现有 config decode/save |
| `internal/config/migration_test.go` | config 默认迁移、权限、失败与并发 no-clobber 测试 |
| `internal/profile/profile.go` | 新默认 profile 路径、默认迁移/创建、既有 rename 覆盖语义 |
| `internal/profile/atomic.go` | profile 既有 replace publisher 与新增 exclusive publisher 共用临时文件/fsync 流程 |
| `internal/profile/profile_test.go` | profile 迁移、身份保真、并发创建与失败原子性测试 |
| `cmd/mornlea/**` | Darwin 客户端、benchmark/capture 与 10 张原样移动的 golden |
| `cmd/mornlea-server/**` | 无图形 TCP 服务端与材料迁移入口 |
| `go.mod` | `github.com/channing771/mornlea` module identity |
| `engine/crates/mornlea_mesh/**` | 改名后的唯一 Rust mesh/light crate |
| `engine/include/mornlea_engine.h` | 改名后的 C ABI 唯一声明来源 |
| `internal/mesh/native_abi.go` | 改名后的唯一 cgo bridge；ABI 数值和行为不变 |
| `Makefile` | `bin/mornlea` + sibling `libmornlea_mesh.dylib` canonical build |
| `.github/workflows/ci.yml` | 新命令路径、Rust-first 与 Linux server closure |
| `scripts/agent-hooks/guard.mjs` | 新 crate/header/环境变量/消息前缀路由 |
| `internal/archcheck/{helpers,platform,source_guards}_test.go` | 新 module 与命令路径守卫 |
| `internal/archcheck/identity_test.go` | 当前 tracked-tree 旧身份精确扫描与兼容白名单 |
| `README.md`、`AGENTS.md`、`CLAUDE.md` | 当前项目身份与开发入口 |
| `docs/notes/mornlea-migration.md` | 新旧命令/module/路径/环境变量及单向复制说明 |
| `openspec/changes/m4q-mornlea-project-rename/**` | 本次可观察契约、设计、顺序和验收账本 |

---

### Task 1: 合并最新主线并冻结统一功能基线

**Files:**
- Merge only: all files changed by `origin/main` relative to the M4P merge base
- Report only, ignored: `.superpowers/sdd/2026-08-12-m4q-mornlea-project-rename/task-1-report.md`

**Interfaces:**
- Consumes: M4P final `4c77539280d7e093001c2c9f3fc02da513fd3715`, design commit `6fbbb970d017d5fc2d3fe05cff61d0ed81cb18cc`, current `origin/main`.
- Produces: a merge commit containing M4P Rust mesh/light and main's procedural block clouds, with no rename diff.

- [ ] **Step 1: Install the local report exclusion, then freeze exact heads**

Run:

```bash
report_pattern='/.superpowers/sdd/2026-08-12-m4q-mornlea-project-rename/'
exclude_file="$(git rev-parse --git-common-dir)/info/exclude"
grep -Fxq "$report_pattern" "$exclude_file" || printf '\n%s\n' "$report_pattern" >> "$exclude_file"
git check-ignore -q .superpowers/sdd/2026-08-12-m4q-mornlea-project-rename/task-1-report.md
git ls-files --error-unmatch docs/superpowers/plans/2026-08-12-mornlea-project-rename.md

git status --short --branch
git fetch origin --prune
mornlea_invariants="$(git rev-parse --git-common-dir)/mornlea-m4q-evidence"
mkdir -p "$mornlea_invariants"
origin_main=$(git rev-parse 'origin/main^{commit}')
printf '%s\n' "$origin_main" > "$mornlea_invariants/task1-origin-main"
git rev-parse HEAD "$origin_main" codex/m4p-rust-engine-mesh
git merge-base HEAD "$origin_main"
git log -1 --format='%H %s' "$origin_main"
openspec status --change m4o-responsibility-oriented-code-organization --json
openspec status --change m4p-rust-engine-mesh --json
openspec validate --all --strict --no-interactive
```

Expected: the exact report directory is locally ignored; tracked worktree clean; HEAD descends from M4P final; M4O/M4P both complete; strict validation PASS. The fetched `origin/main` commit is persisted once under Git common-dir and every later Task 1 step consumes that immutable OID. If it is not `e15541686b184df97d7ec2c74efc3e20979507a7`, record the new head and inspect that exact commit before continuing.

- [ ] **Step 2: Inspect the merge before mutating**

Run:

```bash
mornlea_invariants="$(git rev-parse --git-common-dir)/mornlea-m4q-evidence"
origin_main=$(cat "$mornlea_invariants/task1-origin-main")
git rev-parse "$origin_main^{commit}"
set +e
git merge-tree --write-tree --name-only --messages HEAD "$origin_main" > /private/tmp/mornlea-merge-tree.txt 2>&1
merge_tree_rc=$?
set -e
case "$merge_tree_rc" in
  0) printf '%s\n' 'merge-tree: clean' ;;
  1) printf '%s\n' 'merge-tree: conflicts found'; cat /private/tmp/mornlea-merge-tree.txt ;;
  *) cat /private/tmp/mornlea-merge-tree.txt; exit "$merge_tree_rc" ;;
esac
git diff --name-status "$(git merge-base HEAD "$origin_main")..$origin_main"
```

Expected: modern merge-tree distinguishes clean, conflicts and command errors without marker grep. All upstream paths are understood. Any conflict must be resolved declaration-by-declaration; never take an entire side for `cmd/mcgo`, render/shader, OpenSpec, fixtures, or golden.

- [ ] **Step 3: Enter an uncommitted merge and resolve exactly**

Run:

```bash
mornlea_invariants="$(git rev-parse --git-common-dir)/mornlea-m4q-evidence"
origin_main=$(cat "$mornlea_invariants/task1-origin-main")
git merge --no-ff --no-commit "$origin_main"
git status --short --branch
```

Expected: Git stops before creating the merge commit. Resolve any conflict declaration-by-declaration, stage every resolved path, require zero unmerged entries with `git diff --name-only --diff-filter=U`, then continue. Do not commit yet.

- [ ] **Step 4: Verify both feature lines together**

Run:

```bash
mornlea_invariants="$(git rev-parse --git-common-dir)/mornlea-m4q-evidence"
origin_main=$(cat "$mornlea_invariants/task1-origin-main")
make rust-check
make build
go test ./internal/mesh ./internal/render ./cmd/mcgo -race -count=1
go test ./internal/archcheck -count=1

visual_home=$(mktemp -d /private/tmp/mornlea-merge-home.XXXXXX)
hardware_chip="$(system_profiler SPHardwareDataType | sed -n 's/^[[:space:]]*Chip: //p')"
test -n "$hardware_chip"
printf '视觉验证硬件 Chip: %s\n' "$hardware_chip"
legacy_profile_dir="$visual_home/Library/Application Support/minecraft-go"
mkdir -p "$legacy_profile_dir"
chmod 0700 "$legacy_profile_dir"
printf '%s' '{"version":1,"player_id":"00112233-4455-4677-8899-aabbccddeeff","display_name":"Capture"}' > "$legacy_profile_dir/profile.json"
chmod 0600 "$legacy_profile_dir/profile.json"

merged_visual=$(mktemp -d /private/tmp/mornlea-merge-visual.XXXXXX)
set +e
HOME="$visual_home" VISUAL_OUT="$merged_visual" make visual-check > "$merged_visual/run.log" 2>&1
merged_visual_rc=$?
set -e

baseline_home=$(mktemp -d /private/tmp/mornlea-main-home.XXXXXX)
baseline_profile_dir="$baseline_home/Library/Application Support/minecraft-go"
mkdir -p "$baseline_profile_dir"
chmod 0700 "$baseline_profile_dir"
cp "$legacy_profile_dir/profile.json" "$baseline_profile_dir/profile.json"
chmod 0600 "$baseline_profile_dir/profile.json"
baseline_visual=$(mktemp -d /private/tmp/mornlea-main-visual.XXXXXX)
(
  baseline_tree=$(mktemp -d /private/tmp/mornlea-main-tree.XXXXXX)
  rmdir "$baseline_tree"
  restore_tree() {
    test ! -e "$baseline_tree/.git" || git worktree remove "$baseline_tree"
  }
  trap 'restore_tree' EXIT
  trap 'restore_tree; exit 129' HUP
  trap 'restore_tree; exit 130' INT
  trap 'restore_tree; exit 143' TERM
  git worktree add --detach "$baseline_tree" "$origin_main"
  set +e
  (cd "$baseline_tree" && HOME="$baseline_home" VISUAL_OUT="$baseline_visual" make visual-check > "$baseline_visual/run.log" 2>&1)
  baseline_visual_rc=$?
  set -e
  restore_tree
  trap - EXIT HUP INT TERM
)

for scene in terrain-noon hud-hotbar-health avatar-nametag inventory-crafting debug-panel skylight-tunnel block-light-room materials-showcase target-block-feedback oak-grove; do
  cmp "$merged_visual/${scene}.png" "$baseline_visual/${scene}.png"
done
case "$hardware_chip" in
  'Apple M2')
    test "$merged_visual_rc" -ne 0
    test "$baseline_visual_rc" -ne 0
    for artifact in materials-showcase-actual.png materials-showcase-diff.png oak-grove-actual.png oak-grove-diff.png; do
      cmp "$merged_visual/$artifact" "$baseline_visual/$artifact"
    done
    rg '^已抓取场景 (materials-showcase|oak-grove):' "$merged_visual/run.log" > "$merged_visual/failures.txt"
    rg '^已抓取场景 (materials-showcase|oak-grove):' "$baseline_visual/run.log" > "$baseline_visual/failures.txt"
    cmp "$merged_visual/failures.txt" "$baseline_visual/failures.txt"
    rg -Fx '已抓取场景 materials-showcase: 最大通道差 1，差异像素 26/230400（0.0113%），首个差异像素在 (172,26)' "$merged_visual/failures.txt"
    rg -Fx '已抓取场景 oak-grove: 最大通道差 47，差异像素 10/230400（0.0043%），首个差异像素在 (89,86)' "$merged_visual/failures.txt"
    test "$(find "$merged_visual" -maxdepth 1 -name '*-actual.png' | wc -l | tr -d ' ')" = 2
    test "$(find "$merged_visual" -maxdepth 1 -name '*-diff.png' | wc -l | tr -d ' ')" = 2
    ;;
  *)
    test "$merged_visual_rc" -eq 0
    test "$baseline_visual_rc" -eq 0
    test "$(find "$merged_visual" -maxdepth 1 \( -name '*-actual.png' -o -name '*-diff.png' \) | wc -l | tr -d ' ')" = 0
    test "$(find "$baseline_visual" -maxdepth 1 \( -name '*-actual.png' -o -name '*-diff.png' \) | wc -l | tr -d ' ')" = 0
    ;;
esac

git diff --cached --check
git commit -m "merge: 同步 Mornlea 改名前主线"
test "$(git rev-parse HEAD^2)" = "$origin_main"
git show --stat --oneline --summary HEAD
```

Expected: Rust mesh/light, cloud render tests, archcheck and build PASS before the isolated merge commit. The script records `system_profiler SPHardwareDataType`'s exact Chip. On Apple M2/macOS, only the two inherited failures above are accepted: merged and raw main outputs must match for all 10 scene PNGs and all four failure artifacts, with exact summaries; the other eight scenes must not produce failure artifacts. On every non-M2 Chip, merged and raw main must first match byte-for-byte for all 10 scene PNGs, both `visual-check` commands must exit 0, and neither output may contain `*-actual.png` or `*-diff.png`. Any extra failure, byte drift or summary drift blocks. Never update golden, thresholds or capture code.

- [ ] **Step 5: Freeze post-merge invariant hashes**

Run:

```bash
mornlea_invariants="$(git rev-parse --git-common-dir)/mornlea-m4q-evidence"
mkdir -p "$mornlea_invariants"
origin_main=$(cat "$mornlea_invariants/task1-origin-main")
test "$(git rev-parse HEAD^2)" = "$origin_main"
git rev-parse HEAD > "$mornlea_invariants/task1-head"

git ls-files -z \
  'docs/notes/perf-baseline*.json' \
  'internal/network/testdata/**' \
  'internal/storage/testdata/**' \
  'internal/worldgen/testdata/**' |
  xargs -0 shasum -a 256 > "$mornlea_invariants/static.sha256"

for file in cmd/mcgo/testdata/golden/*.png; do
  hash=$(shasum -a 256 "$file" | awk '{print $1}')
  printf '%s  %s\n' "$hash" "${file##*/}"
done | LC_ALL=C sort > "$mornlea_invariants/golden.sha256"

test "$(wc -l < "$mornlea_invariants/golden.sha256" | tr -d ' ')" = 10
```

Expected: static and 10-golden manifests plus exact Task 1/origin heads persist under Git common-dir, outside the worktree and across task sessions. Record that directory in the ignored report.

---

### Task 2: 归档已完成的 M4O 与 M4P

**Files:**
- Move: `openspec/changes/m4o-responsibility-oriented-code-organization/**` → `openspec/changes/archive/<archive-date>-m4o-responsibility-oriented-code-organization/**`, where `<archive-date>` is the local execution date used by OpenSpec
- Move: `openspec/changes/m4p-rust-engine-mesh/**` → `openspec/changes/archive/<archive-date>-m4p-rust-engine-mesh/**`, where `<archive-date>` is the local execution date used by OpenSpec
- Create: `openspec/specs/repository-code-organization/spec.md`
- Create: `openspec/specs/rust-engine-mesh/spec.md`
- Report only, ignored: `.superpowers/sdd/2026-08-12-m4q-mornlea-project-rename/task-2-report.md`

**Interfaces:**
- Consumes: merged green Task 1 base and two completed active changes.
- Produces: stable main specs for repository organization and Rust mesh, with historical change text preserved under archive.

- [ ] **Step 1: Reconfirm archive eligibility**

Run:

```bash
openspec status --change m4o-responsibility-oriented-code-organization --json
openspec status --change m4p-rust-engine-mesh --json
openspec validate --all --strict --no-interactive
git status --short
```

Expected: both changes report all tasks complete; validation PASS; tracked worktree clean.

- [ ] **Step 2: Archive M4O and validate**

Run:

```bash
test -z "$(find openspec/changes/archive -maxdepth 1 -type d -name '*-m4o-responsibility-oriented-code-organization' -print)"
openspec archive m4o-responsibility-oriented-code-organization --yes
m4o_archive=$(find openspec/changes/archive -maxdepth 1 -type d -name '*-m4o-responsibility-oriented-code-organization' -print)
test "$(printf '%s\n' "$m4o_archive" | wc -l | tr -d ' ')" = 1
test -f openspec/specs/repository-code-organization/spec.md
test -f "$m4o_archive/tasks.md"
openspec validate --all --strict --no-interactive
git diff --check
git add -A -- openspec/changes/m4o-responsibility-oriented-code-organization "$m4o_archive" openspec/specs/repository-code-organization
git diff --cached --check
git commit -m "docs: 归档 M4O 代码组织变更"
```

Expected: only the M4O move and merged main spec are committed.

- [ ] **Step 3: Archive M4P and validate**

Run:

```bash
test -z "$(find openspec/changes/archive -maxdepth 1 -type d -name '*-m4p-rust-engine-mesh' -print)"
openspec archive m4p-rust-engine-mesh --yes
m4p_archive=$(find openspec/changes/archive -maxdepth 1 -type d -name '*-m4p-rust-engine-mesh' -print)
test "$(printf '%s\n' "$m4p_archive" | wc -l | tr -d ' ')" = 1
test -f openspec/specs/rust-engine-mesh/spec.md
test -f "$m4p_archive/tasks.md"
openspec validate --all --strict --no-interactive
git diff --check
git add -A -- openspec/changes/m4p-rust-engine-mesh "$m4p_archive" openspec/specs/rust-engine-mesh
git diff --cached --check
git commit -m "docs: 归档 M4P Rust 网格迁移"
```

Expected: only the M4P move and new main spec are committed. Do not rewrite either archive for the Mornlea name.

- [ ] **Step 4: Verify lifecycle state**

Run:

```bash
openspec list --json
openspec validate --all --strict --no-interactive
git status --short --branch
```

Expected: no active M4O/M4P changes; both main specs validate; worktree clean.

---

### Task 3: 建立 M4Q OpenSpec 改名与数据迁移契约

**Files:**
- Create: `openspec/changes/m4q-mornlea-project-rename/.openspec.yaml`
- Create: `openspec/changes/m4q-mornlea-project-rename/proposal.md`
- Create: `openspec/changes/m4q-mornlea-project-rename/design.md`
- Create: `openspec/changes/m4q-mornlea-project-rename/specs/project-identity/spec.md`
- Create: `openspec/changes/m4q-mornlea-project-rename/specs/local-data-migration/spec.md`
- Create: `openspec/changes/m4q-mornlea-project-rename/specs/natural-material-generation/spec.md`
- Create: `openspec/changes/m4q-mornlea-project-rename/specs/repository-code-organization/spec.md`
- Create: `openspec/changes/m4q-mornlea-project-rename/specs/rust-engine-mesh/spec.md`
- Create: `openspec/changes/m4q-mornlea-project-rename/tasks.md`
- Report only, ignored: `.superpowers/sdd/2026-08-12-m4q-mornlea-project-rename/task-3-report.md`

**Interfaces:**
- Consumes: approved design `docs/superpowers/specs/2026-08-12-mornlea-project-rename-design.md` and archived stable specs.
- Produces: one active change with implementation tasks 4–10 and strict validation status.

- [ ] **Step 1: Scaffold the active change**

Run:

```bash
openspec new change m4q-mornlea-project-rename
openspec status --change m4q-mornlea-project-rename --json
```

Expected: new `spec-driven` change exists and does not alter main specs.

- [ ] **Step 2: Write proposal and design**

`proposal.md` must contain this capability map and no gameplay claim:

```markdown
## Why

当前 `minecraft-go`、`mcgo` 与 `mcgod` 容易暗示官方兼容关系；项目实际是独立体素游戏。M4Q 将当前产品与技术身份统一为 Mornlea，并保持既有玩家身份和配置连续可用。

## What Changes

- 把当前仓库、Go module、客户端/服务端命令、Rust crate/C ABI、构建入口和当前文档切换为 Mornlea。
- 新默认用户目录为 `mornlea`；仅在新文件缺失时校验并复制旧 `minecraft-go` config/profile。
- 保持协议、存档、ABI 数值、benchmark scenario、fixture、golden 与性能 baseline 不变。

## Non-Goals

- 不保留旧命令、module、C symbol 或环境变量兼容层。
- 不改写历史设计、归档 change、性能证据或 Git 历史。
- 不删除旧用户目录，也不建立新旧目录双向同步。

## Capabilities

### New Capabilities
- `project-identity`: 当前产品、module、命令、构建和开发入口统一使用 Mornlea。
- `local-data-migration`: 新默认本机数据路径安全继承旧 config/profile。

### Modified Capabilities
- `natural-material-generation`: 离线迁移命令改为 `mornlea-server`。
- `repository-code-organization`: 允许且仅允许 6 个命令身份测试改名和 1 个新身份守卫，同时保持其余入口与统一基线 artifact。
- `rust-engine-mesh`: crate、header、C symbol、dylib 与客户端产物改为 Mornlea 身份。
```

`design.md` must record exact name mapping, `os.Link` no-clobber publication, default-vs-explicit boundary, exact history/compatibility allowlists, one atomic identity switch, post-merge external operations and rollback. Do not introduce a migration framework, compatibility wrapper or new schema.

It must also carry forward the hardware-conditioned same-machine visual contract from the archived repository-organization contract, recording `system_profiler SPHardwareDataType`'s exact Chip: on Apple M2/macOS, raw Task 1 `origin/main` and the Mornlea branch must emit byte-identical PNGs for all 10 scenes; only `materials-showcase` (delta 1, 26 pixels, 0.0113%) and `oak-grove` (delta 47, 10 pixels, 0.0043%) may retain their exact inherited failures, while the other eight scenes pass tracked golden. On every non-M2 Chip, the two trees must emit byte-identical PNGs for all 10 scenes, both non-update captures must exit 0, and neither tree may emit `*-actual.png` or `*-diff.png`. This never permits a golden, threshold or capture-code update.

- [ ] **Step 3: Write observable delta specs**

`specs/project-identity/spec.md` must include:

```markdown
## ADDED Requirements

### Requirement: 当前项目身份统一为 Mornlea
系统 MUST 以 `Mornlea` 作为当前产品名，以 `github.com/channing771/mornlea` 作为 Go module，以 `mornlea` 和 `mornlea-server` 作为客户端与专用服务端命令。

#### Scenario: clean checkout 构建新入口
- **WHEN** 从 clean checkout 执行 canonical build
- **THEN** MUST 生成 `bin/mornlea` 与同目录 `libmornlea_mesh.dylib`
- **AND** `cmd/mornlea-server` MUST 继续可在 Linux 无 CGO 下独立构建

#### Scenario: 旧入口不再发布
- **WHEN** 枚举当前 module、命令、native ABI、构建和 Hook 身份
- **THEN** MUST 不存在 `mcgo`/`mcgod` wrapper、旧 C symbol、旧 dylib 或旧环境变量 fallback

### Requirement: 改名保持固定行为与 artifact
系统 MUST 保持协议 v15、区块 schema v8、玩家 schema v6、metadata v2、benchmark scenario v15、ABI version/status、fixture、golden 与性能 baseline 不变。

#### Scenario: 改名前后不变量逐字节一致
- **GIVEN** 已在合并主线后的统一基线冻结固定 artifact
- **WHEN** 完成身份切换
- **THEN** 所有静态 fixture/baseline hash 与按 basename 比较的 10 张 golden MUST 完全一致

#### Scenario: Apple M2 已批准的同环境视觉基线不掩盖改名漂移
- **GIVEN** Apple M2/macOS 上的原始 Task 1 `origin/main` 仅有 `materials-showcase` 和 `oak-grove` 两个精确已知失败
- **WHEN** 原始主线与 Mornlea 分支在同一隔离 HOME 下运行非更新 capture
- **THEN** 两边 10 个场景 PNG 与两个失败的 actual/diff MUST 逐字节一致
- **AND** 失败摘要 MUST 精确保持 `materials-showcase` 最大差 1/26 像素/0.0113% 与 `oak-grove` 最大差 47/10 像素/0.0043%
- **AND** 其余 8 个场景 MUST 通过 tracked golden，不得修改 golden、阈值或 capture 代码

#### Scenario: 非 Apple M2 的同环境视觉基线不掩盖改名漂移
- **GIVEN** `system_profiler SPHardwareDataType` 的 Chip 不是 `Apple M2`
- **WHEN** 原始主线与 Mornlea 分支在同一隔离 HOME 下运行非更新 capture
- **THEN** 两边 10 个场景 PNG MUST 逐字节一致，且两次 `visual-check` MUST 退出 0
- **AND** 两边都 MUST 不产生 `*-actual.png` 或 `*-diff.png`，不得修改 golden、阈值或 capture 代码
```

`specs/local-data-migration/spec.md` must include:

```markdown
## ADDED Requirements

### Requirement: 默认本机数据安全迁移到 Mornlea
系统 MUST 对 config 与 profile 独立使用 `os.UserConfigDir()/mornlea` 作为新默认目录，并仅在新文件缺失时读取、校验和规范化复制旧 `minecraft-go` 文件。

#### Scenario: 新文件优先
- **GIVEN** 新默认文件存在
- **WHEN** 启动客户端或专用服务端
- **THEN** MUST 只读取并校验新文件
- **AND** 新文件失败 MUST 终止且不得回退旧文件

#### Scenario: 仅旧文件存在
- **GIVEN** 新默认文件缺失且旧文件有效
- **WHEN** 加载默认 config 或 profile
- **THEN** MUST 以 0700 父目录、0600 文件和原子 no-clobber 发布规范化副本
- **AND** MUST 从发布后的新文件返回结果并保持旧文件逐字节不变
- **AND** 仅实际发布者 MUST 记录一条含 `legacy_path` 与 `current_path` 的结构化迁移成功日志

#### Scenario: 旧文件非法
- **GIVEN** 新默认文件缺失且旧文件非法
- **WHEN** 加载默认 config 或 profile
- **THEN** MUST 返回指向旧路径的错误
- **AND** MUST NOT 创建新默认文件或生成新 PlayerID

#### Scenario: 并发发布已有赢家
- **GIVEN** 两个进程同时迁移或首次创建同一文件
- **WHEN** 其中一个先发布目标
- **THEN** 另一个 MUST 不覆盖目标并读取校验赢家
- **AND** 所有 profile 调用方 MUST 返回同一 PlayerID
- **AND** requested name 相同或均未提供时，所有调用方 MUST 返回同一份完整 Profile
- **AND** 不同 requested name 仍沿用既有 replace-on-save 语义，不承诺并发显示名的返回顺序
- **AND** 发布 loser MUST NOT 记录迁移成功日志

#### Scenario: 两边都不存在
- **WHEN** config 新旧文件都不存在
- **THEN** MUST 返回编译默认值且不创建文件
- **WHEN** profile 新旧文件都不存在
- **THEN** MUST 原子创建一份 UUIDv4 profile，所有并发调用方读取同一 PlayerID

#### Scenario: 显式配置跳过迁移
- **GIVEN** 用户传入 `--config PATH`
- **WHEN** 加载配置
- **THEN** MUST 只读取显式路径并完全跳过默认目录迁移
```

`natural-material-generation` MODIFIED requirement changes only the observable command from `mcgod` to `mornlea-server`. `rust-engine-mesh` MODIFIED requirements change only `mcgo_mesh`/header/C symbols/dylib/client/server names while preserving ABI version/status, ownership, parity, toolchain and packaging behavior.

`repository-code-organization` MODIFIED requirements must define the fixed-entry baseline as the Task 7 initial HEAD, after Tasks 4–6 have already added their migration/routing tests. Relative to that persisted pre-identity manifest, permit exactly this identity-only entry delta:

```text
TestMCGodHasNoGraphicsDependencies -> TestMornleaServerHasNoGraphicsDependencies
TestMcgoUsesLoginStreamsInsteadOfAttachedServerEndpoints -> TestMornleaUsesLoginStreamsInsteadOfAttachedServerEndpoints
TestMcgoBenchmarkTCPPathUsesTheSharedLoginStateMachine -> TestMornleaBenchmarkTCPPathUsesTheSharedLoginStateMachine
TestMCGodProcess -> TestMornleaServerProcess
TestMCGodProcessReleasesWorldLockAfterSIGTERM -> TestMornleaServerProcessReleasesWorldLockAfterSIGTERM
TestMCGodProcessSaveFailureExitsNonzero -> TestMornleaServerProcessSaveFailureExitsNonzero
added: TestMornleaCurrentIdentity
```

All other Test/Benchmark/Fuzz entries and post-Task-1 fixed artifacts remain unchanged.

- [ ] **Step 4: Write ordered tasks and validate**

`tasks.md` must contain unchecked items 4.1–9.1 matching Tasks 4–9 below, including each exact file group and focused command. It must state that Task 9 stops at independent review, Task 10 owns approval closure/archive and therefore is not a self-referential OpenSpec checkbox, and external Task 11 is post-merge operator work rather than an OpenSpec implementation checkbox.

Run:

```bash
openspec validate m4q-mornlea-project-rename --strict --no-interactive
openspec validate --all --strict --no-interactive
git diff --check
git add openspec/changes/m4q-mornlea-project-rename
git diff --cached --check
git commit -m "docs: 规划 M4Q Mornlea 项目改名"
```

Expected: strict validation PASS; commit contains only the nine M4Q artifact groups.

---

### Task 4: TDD 实现 config 默认路径迁移

**Files:**
- Modify: `internal/config/config.go`
- Create: `internal/config/migration_test.go`
- Modify: `openspec/changes/m4q-mornlea-project-rename/tasks.md`
- Report only, ignored: `.superpowers/sdd/2026-08-12-m4q-mornlea-project-rename/task-4-report.md`

**Interfaces:**
- Preserve: `func Load(path string) (Config, error)` for explicit/custom paths and debug-panel use.
- Preserve: `func (Config) Save(path string) error` replace-on-save semantics.
- Preserve temporarily: `func DefaultPath() (string, error)` still returns the legacy path until Task 6 switches it atomically with command routing.
- Add: `func LoadDefault() (Config, error)`.
- Private: `defaultPaths() (current, legacy string, err error)` returns `.../mornlea/config.json` and `.../minecraft-go/config.json`; `LoadDefault` uses this directly rather than public `DefaultPath`.
- Private: `decodeConfig(path string, contents []byte) (Config, error)` extracted from existing `Load`.
- Private: `publishConfigExclusively(path string, contents []byte) (published bool, err error)` using same-directory temp + `os.Link`.
- Private test seam: `loadDefaultFromPaths(current, legacy string, publish func(string, []byte) (bool, error)) (Config, error)`.
- Private failure seam: `publishConfigExclusivelyWithLink(path string, contents []byte, link func(string, string) error) (published bool, err error)`; production passes `os.Link`.

- [ ] **Step 1: Write RED path and precedence tests**

Create package-internal `internal/config/migration_test.go` with:

```go
func TestLoadDefaultUsesMornleaCurrentAndMinecraftGoLegacy(t *testing.T)
func TestLoadDefaultPrefersExistingMornleaConfig(t *testing.T)
func TestLoadDefaultMigratesLegacyConfigAndPreservesSource(t *testing.T)
func TestLoadDefaultRejectsInvalidAuthoritativeConfig(t *testing.T)
func TestLoadDefaultRejectsInvalidLegacyConfigWithoutCreatingCurrent(t *testing.T)
func TestLoadDefaultMissingBothReturnsDefaultsWithoutCreatingFile(t *testing.T)
```

Use `t.Setenv("HOME", t.TempDir())` on Darwin for the public `LoadDefault` path assertion; use explicit temporary `current`/`legacy` paths for lower-level migration tests. Require `LoadDefault` to read the new Mornlea path first and use the old minecraft-go path only as legacy. In the precedence test, make legacy content invalid so any accidental legacy read fails. The dedicated invalid-legacy test leaves current absent, requires an error containing the legacy path, byte-identical legacy content, no current and no temp. In the migration test, assert semantic equality, canonical JSON, current mode 0600, current parent 0700 and byte-identical legacy content. Assert separately that public `DefaultPath` still names the old path in this task, preventing a deployable half-switch before Task 6.

Run:

```bash
go test ./internal/config -run 'LoadDefault|DefaultPath' -count=1
```

Expected: compile FAIL because `LoadDefault`/private seam do not exist; the legacy public-path control stays GREEN.

- [ ] **Step 2: Implement the smallest decode and precedence flow**

Refactor existing `Load` without behavior changes:

```go
func Load(path string) (Config, error) {
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Defaults(), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("config: 读取配置文件 %s: %w", path, err)
	}
	return decodeConfig(path, contents)
}
```

Implement default precedence through private `defaultPaths`: inspect the new app directory/current file permissions before reading (existing parent must be private and existing target must be 0600), read current first, and only exact `os.ErrNotExist` examines legacy. Missing both returns `Defaults()`; an existing unsafe or invalid current never falls back. Serialize the validated legacy `Config` with the existing canonical JSON shape before publication, then re-check new parent/target permissions and read/decode current after success or concurrent loss. Do not change public `DefaultPath` in this task.

Run the same focused command. Expected: precedence/missing tests GREEN; migration publication tests may remain RED until Step 4.

- [ ] **Step 3: Write RED permission, failure and concurrency tests**

Add:

```go
func TestLoadDefaultRejectsUnsafeParentPermissions(t *testing.T)
func TestLoadDefaultRejectsUnsafeTargetPermissions(t *testing.T)
func TestLoadDefaultReadsConcurrentWinnerWithoutOverwritingIt(t *testing.T)
func TestLoadDefaultRejectsUnsafeConcurrentWinnerPermissions(t *testing.T)
func TestLoadDefaultPublishFailurePreservesLegacyAndCleansTemporary(t *testing.T)
func TestPublishConfigExclusivelyAllowsExactlyOneConcurrentWinner(t *testing.T)
func TestLoadDefaultLogsOnlySuccessfulMigrationPublisher(t *testing.T)
```

Assertions must include returned error identity/path, old bytes unchanged, target absent or equal to winner, mode 0700/0600, and zero `config-*.json.tmp` files. The unsafe-parent test creates an existing 0755 current parent with valid legacy content and requires failure before any current/temp creation, with the parent still 0755 and legacy bytes unchanged; the unsafe-target test covers an existing 0644 current file. Inject a sentinel through `publishConfigExclusivelyWithLink` to prove link-failure cleanup. Inject a losing high-level publisher that installs a valid 0644 winner then returns `published=false`; `loadDefaultFromPaths` must reject it before decode, proving the loser repeats default permission validation. The publisher race uses two different valid canonical bodies and requires exactly one `published=true`; this kills `Stat+Rename` and `os.Rename` replacement. The log test temporarily captures the package's existing stdlib `slog` default, restores it with `t.Cleanup`, and requires exactly one success record from the actual publisher with exact `legacy_path` and `current_path`; the injected loser emits none. Do not run that global logger test in parallel. The implementation review, rather than this race alone, enforces that the target is not directly exposed with `O_EXCL` while bytes are incomplete.

Run:

```bash
go test ./internal/config -run 'UnsafeParent|UnsafeTarget|UnsafeConcurrentWinner|ConcurrentWinner|PublishFailure|PublishConfigExclusively' -race -count=1
```

Expected: FAIL until exclusive publication and permission checks exist.

- [ ] **Step 4: Implement no-clobber publication with stdlib only**

Use the existing `Config.Save` write sequence, but keep its replace semantics. For exclusive publish: `MkdirAll(parent, 0700)`, verify an existing default app directory has no group/other permission, create/chmod 0600 temp in the same directory, write all bytes, `Sync`, `Close`, then `os.Link(temp, target)`. On `fs.ErrExist`, delete the temp and return `published=false`; on any other link error, fail without fallback. Remove the temporary hardlink before syncing the parent directory after a successful link. Remember `published`, perform the final permission check and decode first, and only then emit one `slog.Info` record with `legacy_path` and `current_path` when that legacy call actually published. Missing-both defaults, concurrent losers and any post-publish validation failure do not log success. Do not introduce an interface or dependency.

After either `published=true` or `published=false`, call the same default parent/target 0700/0600 validator again before the final read. This is required because a concurrent winner is outside the initial check; never route the loser through ordinary explicit-path `Load` alone.

Also change `Config.Save` creation mode from 0755 to 0700 for newly created directories, but do not reject an existing custom `--config` parent merely because it is shared.

Run:

```bash
go test ./internal/config -race -count=1
go test ./internal/archcheck -count=1
gofmt -w internal/config/config.go internal/config/migration_test.go
gofmt -l internal/config
git diff --check
```

Expected: all config tests/race/archcheck PASS and no temp artifacts remain.

- [ ] **Step 5: Mutation-check dangerous fallbacks and commit**

Temporarily verify each mutation fails, restoring production after each run:

1. fall back to legacy after invalid current → `TestLoadDefaultRejectsInvalidAuthoritativeConfig` RED;
2. turn invalid legacy into `Defaults()` → `TestLoadDefaultRejectsInvalidLegacyConfigWithoutCreatingCurrent` RED;
3. replace `os.Link` with `os.Rename` → exclusive race RED;
4. return migrated legacy value without re-reading a concurrent winner → `TestLoadDefaultReadsConcurrentWinnerWithoutOverwritingIt` RED;
5. delete the default-parent mode check → `TestLoadDefaultRejectsUnsafeParentPermissions` RED;
6. log on `published=false` or omit path fields → `TestLoadDefaultLogsOnlySuccessfulMigrationPublisher` RED.

Then run:

```bash
go test ./internal/config -race -count=1
go vet ./internal/config
openspec validate --all --strict --no-interactive
git diff --check
git add internal/config openspec/changes/m4q-mornlea-project-rename/tasks.md
git diff --cached --check
git commit -m "feat: 迁移 Mornlea 默认配置"
```

Expected: only Task 4 files; task 4.1 checked; no mutation left in production.

---

### Task 5: TDD 实现 profile 默认迁移与并发创建

**Files:**
- Modify: `internal/profile/profile.go`
- Modify: `internal/profile/atomic.go`
- Modify: `internal/profile/profile_test.go`
- Modify: `openspec/changes/m4q-mornlea-project-rename/tasks.md`
- Report only, ignored: `.superpowers/sdd/2026-08-12-m4q-mornlea-project-rename/task-5-report.md`

**Interfaces:**
- Preserve: `func LoadOrCreate(Options) (Profile, error)` and `Options` fields.
- Preserve temporarily: `func DefaultPath() (string, error)` still returns the legacy path until Task 6 switches it atomically with command routing.
- Preserve: custom `Options.Path != ""` and requested-name update use replace-on-save.
- Private: `defaultPaths() (current, legacy string, err error)` returns `.../mornlea/profile.json` and `.../minecraft-go/profile.json`; empty-path `LoadOrCreate` uses it directly.
- Private: `loadOrCreateAtPath(path string, options Options) (Profile, error)` for explicit/custom behavior.
- Private test seam: `loadOrCreateDefaultFromPaths(current, legacy string, options Options, publish func(string, []byte) (bool, error)) (Profile, error)`.
- Private: `publishProfileExclusively(path string, contents []byte) (published bool, err error)`.
- Modify the private writer hook so one existing temp/write/fsync flow accepts a final publisher `func(temp, target string) (published bool, err error)`; replace wraps `os.Rename`, exclusive wraps `os.Link`. No new public abstraction.

- [ ] **Step 1: Write RED default precedence and identity-preservation tests**

Add to package-internal `profile_test.go`:

```go
func TestLoadOrCreateDefaultUsesMornleaCurrentAndMinecraftGoLegacy(t *testing.T)
func TestLoadOrCreateDefaultPrefersExistingMornleaProfile(t *testing.T)
func TestLoadOrCreateDefaultMigratesLegacyProfileExactly(t *testing.T)
func TestLoadOrCreateDefaultMigrationAppliesRequestedName(t *testing.T)
func TestLoadOrCreateDefaultRejectsInvalidAuthoritativeProfile(t *testing.T)
func TestLoadOrCreateCustomPathSkipsDefaultMigration(t *testing.T)
```

For the public empty-path call, set a temporary HOME and require lookup through new Mornlea current + old minecraft-go legacy paths while public `DefaultPath` remains old in this task. For migration, assert exact `Version`, `PlayerID`, `DisplayName`, canonical encoded bytes at the new path, new 0700/0600 modes and unchanged legacy bytes. The requested-name case starts from legacy `Chen`, passes `RequestedName=Alex`, and requires the same PlayerID, new file name `Alex`, and byte-identical legacy file. For invalid legacy/current, wrap `Options.Random` with a counting reader and require zero reads so failure cannot silently generate a new identity.

Run:

```bash
go test ./internal/profile -run 'DefaultPath|LoadOrCreateDefault|CustomPathSkips' -count=1
```

Expected: FAIL because empty-path default migration does not exist; the public legacy-path control stays GREEN.

- [ ] **Step 2: Preserve explicit behavior and implement default lookup**

Change `LoadOrCreate` only at its outer routing boundary:

```go
func LoadOrCreate(options Options) (Profile, error) {
	if options.Path != "" {
		return loadOrCreateAtPath(options.Path, options)
	}
	return loadOrCreateDefault(options)
}
```

Move the current read/name-update/create body into `loadOrCreateAtPath` without changing semantics. Default mode checks current parent/file permissions first, validates current, and applies requested-name replacement if needed; only exact missing current examines legacy. Legacy is decoded/encoded through existing helpers before exclusive publication. After migration or concurrent loss, route the authoritative current file back through `loadOrCreateAtPath` so requested-name updates preserve existing replace semantics.

Do not change public `DefaultPath` in this task. The current client still passes it as an explicit `Options.Path`; preserving the old value prevents an intermediate commit from creating a new Mornlea profile/PlayerID before Task 6 wires empty-path migration.

Run the focused command. Expected: precedence/custom tests GREEN; concurrent first-create tests remain to be added.

- [ ] **Step 3: Write RED concurrent create/migrate and failure tests**

Add:

```go
func TestLoadOrCreateDefaultMissingBothCreatesSingleUUIDv4(t *testing.T)
func TestLoadOrCreateDefaultConcurrentCreationReturnsSingleWinner(t *testing.T)
func TestLoadOrCreateDefaultReadsConcurrentMigrationWinner(t *testing.T)
func TestLoadOrCreateDefaultPublishFailureDoesNotGenerateIdentity(t *testing.T)
func TestLoadOrCreateDefaultRejectsUnsafeParentPermissions(t *testing.T)
func TestLoadOrCreateDefaultRejectsUnsafeTargetPermissions(t *testing.T)
func TestLoadOrCreateDefaultLogsOnlySuccessfulMigrationPublisher(t *testing.T)
```

Drive default creation/migration through `loadOrCreateDefaultFromPaths` so tests can inject a barrier publisher: both creators first produce different deterministic candidate IDs, then exactly one current file wins. With the same requested name (or nil), both returned Profiles must equal that winner. For migration loser re-read, have the publisher install a different valid current Profile and return `published=false`; the caller must return that winner rather than its legacy candidate. The unsafe-parent test creates an existing 0755 current parent with valid legacy content and requires failure before random reads or current/temp creation, with the parent still 0755 and legacy bytes unchanged; the unsafe-target and loser-permission cases cover 0644 current files and require rejection before decode. Failure injection uses a sentinel publisher and asserts `errors.Is`, zero random reads, legacy unchanged, no current and no `.profile.tmp-*` file. The log test captures and restores the stdlib `slog` default without `t.Parallel`; only the actual legacy publisher emits one success record with exact `legacy_path`/`current_path`, while a concurrent loser emits none. First creation without legacy is not a migration and emits no migration-success log.

Run:

```bash
go test ./internal/profile -run 'Concurrent|PublishFailure|UnsafeParent|UnsafeTarget|MissingBoth' -race -count=1
```

Expected: FAIL until exclusive publication and loser re-read exist.

- [ ] **Step 4: Reuse the atomic writer with an exclusive final action**

Keep `writeProfileAtomically` using `os.Rename` so `--name` updates still replace the profile. Replace only the private `atomicWriteHooks.rename` callback with a publisher callback returning `published`; the common writer always removes its temp path after the final action (a no-op after rename, required after link), then syncs the parent. The exclusive wrapper maps `fs.ErrExist` to `published=false`. Remember the legacy publication result, then recheck permissions and decode/apply the requested name; only a fully successful call with `published=true` logs migration success with the two paths. Do not use `Stat+Rename`, direct target `O_EXCL`, or a new interface.

For missing-both creation, generate one candidate, exclusively publish it, then always re-check default parent/target permissions and decode the current file; concurrent losers must not retry UUID generation. Use the same permission re-check after migration loses to an existing winner. Preserve existing explicit/custom path behavior without imposing the default-directory policy on it.

Run:

```bash
go test ./internal/profile -race -count=1
go test ./internal/archcheck -count=1
gofmt -w internal/profile/profile.go internal/profile/atomic.go internal/profile/profile_test.go
gofmt -l internal/profile
git diff --check
```

Expected: existing name-change/atomic-failure tests and all new tests PASS.

- [ ] **Step 5: Mutation-check identity safety and commit**

Verify and restore:

1. generate a new UUID after invalid legacy → invalid-authoritative test RED via random-reader count;
2. use replace publication for first create → concurrent winner test RED;
3. return losing candidate without current re-read → concurrent creation/migration tests RED;
4. delete the default-parent mode check → `TestLoadOrCreateDefaultRejectsUnsafeParentPermissions` RED;
5. log migration success from a loser → `TestLoadOrCreateDefaultLogsOnlySuccessfulMigrationPublisher` RED.

Then:

```bash
go test ./internal/profile -race -count=1
go vet ./internal/profile
openspec validate --all --strict --no-interactive
git diff --check
git add internal/profile openspec/changes/m4q-mornlea-project-rename/tasks.md
git diff --cached --check
git commit -m "feat: 迁移 Mornlea 玩家档案"
```

Expected: only Task 5 files; task 5.1 checked.

---

### Task 6: TDD 接线客户端与专用服务端默认迁移

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/migration_test.go`
- Modify: `internal/profile/profile.go`
- Modify: `internal/profile/profile_test.go`
- Modify: `cmd/mcgo/main.go`
- Modify: `cmd/mcgo/options.go`
- Modify: `cmd/mcgo/run_test.go`
- Modify: `cmd/mcgod/main.go`
- Modify: `cmd/mcgod/main_test.go`
- Modify: `openspec/changes/m4q-mornlea-project-rename/tasks.md`
- Report only, ignored: `.superpowers/sdd/2026-08-12-m4q-mornlea-project-rename/task-6-report.md`

**Interfaces:**
- Client `resolveConfig`: benchmark/capture → `config.Defaults`; explicit `ConfigPath` → `config.Load(path)`; empty path → `config.LoadDefault()`.
- Client `resolveConfigPath`: still resolves explicit or new default path only for debug-panel F5 save destination.
- Client `loadApplicationIdentity`: `profile.LoadOrCreate(profile.Options{RequestedName: requestedName})`; do not pre-resolve `DefaultPath` into `Options.Path`.
- Server `resolveConfig`: explicit `opts.Config` → `config.Load`; empty → `config.LoadDefault()`.
- Server material migration continues returning before config/server assembly.
- Atomically switch `config.DefaultPath()` and `profile.DefaultPath()` from `minecraft-go` to `mornlea` in the same commit that stops commands from treating those default paths as explicit paths.

- [ ] **Step 1: Write RED client routing tests**

First add the public path assertions to `internal/config/migration_test.go` and `internal/profile/profile_test.go`:

```go
func TestDefaultPathUsesMornleaDirectory(t *testing.T)
func TestDefaultProfilePathUsesMornleaDirectory(t *testing.T)
```

They must fail while Task 4/5 deliberately retain legacy public paths. Then add to `cmd/mcgo/run_test.go`:

```go
func TestResolveConfigUsesDefaultMigration(t *testing.T)
func TestResolveConfigExplicitPathSkipsDefaultMigration(t *testing.T)
func TestLoadApplicationIdentityUsesDefaultMigration(t *testing.T)
```

Use a temporary HOME. For default config/profile, place only valid legacy files and assert new files are created and values/PlayerID preserved. For explicit config, put invalid default legacy data beside a valid explicit file; explicit load must succeed without creating new default data. Keep the existing benchmark/capture controls only for config: both paths use `config.Defaults`; capture still loads a profile, so every later visual command must use an isolated HOME with a deterministic profile. The two public path tests and command routing tests are one RED/GREEN slice and must be committed together.

Run:

```bash
make rust
go test ./internal/config ./internal/profile -run 'DefaultPathUsesMornlea|DefaultProfilePathUsesMornlea' -count=1
go test ./cmd/mcgo -run 'ResolveConfig|LoadApplicationIdentity|IgnoresUserConfig' -count=1
```

Expected: FAIL because public paths still name `minecraft-go`, empty config paths resolve to `DefaultPath` then call ordinary `Load`, and identity passes an explicit profile path.

- [ ] **Step 2: Write RED server routing tests**

Add to `cmd/mcgod/main_test.go`:

```go
func TestResolveConfigUsesDefaultMigration(t *testing.T)
func TestResolveConfigExplicitPathSkipsDefaultMigration(t *testing.T)
```

Use the same legacy-only and explicit-bypass pattern. Retain `TestRunMigrateMaterialsReturnsBeforeConfigAndServerAssembly` as the control proving offline migration does not touch default config.

Run:

```bash
go test ./cmd/mcgod -run 'ResolveConfig|MigrateMaterialsReturnsBeforeConfig' -count=1
```

Expected: default migration test FAIL before the wiring change; explicit and early-return controls remain GREEN.

- [ ] **Step 3: Implement the minimal routing changes**

Change both public `DefaultPath` functions to the Mornlea path. In both commands, branch on whether the CLI supplied a path rather than always converting empty to a path first. Keep `resolveConfigPath` for interactive save destination only. Change identity loading to omit `Options.Path`, which is the signal for default migration. These edits are atomic: never commit the public-path switch without the command routing switch.

Run:

```bash
make rust
go test ./cmd/mcgo ./cmd/mcgod -run 'ResolveConfig|LoadApplicationIdentity|IgnoresUserConfig|MigrateMaterialsReturnsBeforeConfig' -race -count=1
go test ./internal/config ./internal/profile -race -count=1
gofmt -w internal/config/config.go internal/config/migration_test.go internal/profile/profile.go internal/profile/profile_test.go cmd/mcgo/main.go cmd/mcgo/options.go cmd/mcgo/run_test.go cmd/mcgod/main.go cmd/mcgod/main_test.go
gofmt -l cmd/mcgo cmd/mcgod
git diff --check
```

Expected: all focused commands PASS; benchmark/capture still avoid config migration, material migration returns before config, and interactive identity uses the default profile migration path. Capture profile access remains contained by the isolated-HOME visual commands in Tasks 1 and 9.

- [ ] **Step 4: Commit command wiring**

Run:

```bash
make rust
go test ./cmd/mcgo ./cmd/mcgod ./internal/config ./internal/profile -race -count=1
go test ./internal/archcheck -count=1
go vet ./cmd/mcgo ./cmd/mcgod ./internal/config ./internal/profile
openspec validate --all --strict --no-interactive
git add internal/config/config.go internal/config/migration_test.go internal/profile/profile.go internal/profile/profile_test.go cmd/mcgo/main.go cmd/mcgo/options.go cmd/mcgo/run_test.go cmd/mcgod/main.go cmd/mcgod/main_test.go openspec/changes/m4q-mornlea-project-rename/tasks.md
git diff --cached --check
git commit -m "feat: 接入 Mornlea 默认数据迁移"
```

Expected: only Task 6 files; task 6.1 checked.

Before leaving Task 6, persist the pre-identity test-entry baseline used by Task 7:

```bash
mornlea_invariants="$(git rev-parse --git-common-dir)/mornlea-m4q-evidence"
rg -n '^func (Test|Benchmark|Fuzz)[A-Za-z0-9_]+' --glob '*_test.go' \
  | sed -E 's/:([0-9]+):func /:/' \
  | sed -E 's/\(.*//' \
  | LC_ALL=C sort > "$mornlea_invariants/task6-test-entries"
git rev-parse HEAD > "$mornlea_invariants/task6-head"
```

Expected: this durable manifest includes all approved migration/routing tests and becomes the sole entry baseline for the identity-only Task 7 delta.

---

### Task 7: 原子切换 Go、命令、Rust ABI、构建与守卫身份

**Files:**
- Modify: `go.mod`
- Modify: every tracked Go file whose import path is exactly `minecraft-go/internal/...`
- Move: `cmd/mcgo/**` → `cmd/mornlea/**`
- Move: `cmd/mcgod/**` → `cmd/mornlea-server/**`
- Move: `engine/crates/mcgo_mesh/**` → `engine/crates/mornlea_mesh/**`
- Move: `engine/include/mcgo_engine.h` → `engine/include/mornlea_engine.h`
- Modify: `engine/Cargo.toml`
- Modify: `engine/Cargo.lock`
- Modify: `engine/crates/mornlea_mesh/Cargo.toml`
- Modify: `engine/crates/mornlea_mesh/build.rs`
- Modify: `engine/crates/mornlea_mesh/src/ffi.rs`
- Modify: `internal/mesh/native_abi.go`
- Modify: `Makefile`
- Modify: `.github/workflows/ci.yml`
- Modify: `.gitignore`
- Modify: `.codex/hooks.json`
- Modify: `scripts/agent-hooks/guard.mjs`
- Modify: `scripts/agent-hooks/guard.test.mjs`
- Modify: `internal/archcheck/helpers_test.go`
- Modify: `internal/archcheck/platform_test.go`
- Modify: `internal/archcheck/source_guards_test.go`
- Create: `internal/archcheck/identity_test.go`
- Modify: `cmd/gfxspike/main.go`
- Modify: `internal/client/perf.go`
- Modify: `internal/logging/logging.go`
- Modify: `internal/render/avatar_test.go`
- Modify: `internal/storage/region_crash_test.go`
- Modify: `openspec/changes/m4q-mornlea-project-rename/tasks.md`
- Report only, ignored: `.superpowers/sdd/2026-08-12-m4q-mornlea-project-rename/task-7-report.md`

**Interfaces:**
- Go module: `module github.com/channing771/mornlea`.
- Client/server packages: `./cmd/mornlea`, `./cmd/mornlea-server`.
- Rust package/library: `mornlea_mesh`, `libmornlea_mesh.dylib`.
- C header/symbols: `mornlea_engine.h`, `MORNLEA_ENGINE_ABI_VERSION`, `MORNLEA_STATUS_*`, `mornlea_engine_abi_version`, `mornlea_mesh_section`.
- Build artifacts: `bin/mornlea`, `bin/libmornlea_mesh.dylib`.
- Dedicated server artifact: `bin/mornlea-server`, built without CGO.
- Hook escape variable: `MORNLEA_HOOKS_ALLOW_NO_SPEC`; message prefix `[mornlea hook]`.
- Test helper env: `MORNLEA_SERVER_PROCESS*`, `MORNLEA_AVATAR_COLOR_HELPER`, `MORNLEA_REGION_CRASH_*`.
- Preserve ABI numeric version/status, `MGM1`, algorithms, exported Go package APIs and command behavior.

- [ ] **Step 1: Freeze path counts and golden bytes**

Run:

```bash
test "$(git status --porcelain | wc -l | tr -d ' ')" = 0
mornlea_invariants="$(git rev-parse --git-common-dir)/mornlea-m4q-evidence"
test "$(git rev-parse HEAD)" = "$(cat "$mornlea_invariants/task6-head")"
old_import_count=$(git grep -l '"minecraft-go/internal/' -- '*.go' | wc -l | tr -d ' ')
test "$old_import_count" -gt 0
git ls-files 'cmd/mcgo/**' | LC_ALL=C sort > "$mornlea_invariants/client-files.before"
git ls-files 'cmd/mcgod/**' | LC_ALL=C sort > "$mornlea_invariants/server-files.before"
git ls-files 'engine/crates/mcgo_mesh/**' | LC_ALL=C sort > "$mornlea_invariants/rust-files.before"

cp "$mornlea_invariants/task6-test-entries" "$mornlea_invariants/test-entries.before"

for file in cmd/mcgo/testdata/golden/*.png; do
  hash=$(shasum -a 256 "$file" | awk '{print $1}')
  printf '%s  %s\n' "$hash" "${file##*/}"
done | LC_ALL=C sort > "$mornlea_invariants/goldens.before"
cmp "$mornlea_invariants/golden.sha256" "$mornlea_invariants/goldens.before"
```

Expected: nonzero import count; complete file/entry manifests; exactly 10 golden hashes matching Task 1's frozen manifest.

- [ ] **Step 2: Add the RED current-identity guard**

Create `internal/archcheck/identity_test.go` with `TestMornleaCurrentIdentity`, using one table-driven filesystem scan over explicit current code/build roots (`go.mod`, `cmd`, `internal`, `engine/Cargo.toml`, `engine/Cargo.lock`, `engine/crates`, `engine/include`, `Makefile`, `.github/workflows/ci.yml`, `.codex/hooks.json`, `scripts/agent-hooks`, `.gitignore`). Do not walk `.git`, `engine/target`, `bin`, `.superpowers` or documentation/history roots. The guard must scan its own source. Therefore construct every forbidden old token from fragments so the complete token never appears as a source literal, for example:

```go
const modulePath = "github.com/channing771/mornlea"

var forbiddenCurrentIdentity = []string{
	"module minecraft" + "-go",
	`"minecraft` + `-go/internal/`,
	"github.com/channing771/minecraft" + "-go",
	"cmd/mc" + "go",
	"cmd/mc" + "god",
	"bin/mc" + "go",
	"mc" + "go_mesh",
	"libmc" + "go_mesh",
	"mc" + "go_engine",
	"MC" + "GO_ENGINE_",
	"MC" + "GO_STATUS_",
	"MINECRAFT" + "_GO_",
	"MC" + "GOD_",
}
```

Use technical patterns above as zero-tolerance checks in every code/build root. Construct the separate case-insensitive bare-token regex from fragments too; it may allow only legacy data constants/tests in exact `internal/config` and `internal/profile` files plus `.mcgo-world-backup-v1.json` in `internal/storage/backup.go` and its test. It must not suppress an old import, command, C symbol, env variable or comment merely because it occurs in an allowed file. Do not ban broad `MCG` or the sentence explaining non-compatibility with Minecraft.

Run:

```bash
go test ./internal/archcheck -run 'Mornlea|Identity' -count=1
```

Expected: RED with current module/command/Rust/build/hook paths.

- [ ] **Step 3: Perform mechanical Go and directory moves**

Use `git mv` for entire command/crate/header trees. Replace the exact Go module declaration and exact import prefix only; do not reformat algorithms or edit golden bytes. Recount after the move:

```bash
test ! -e cmd/mcgo
test ! -e cmd/mcgod
test ! -e engine/crates/mcgo_mesh
test ! -e engine/include/mcgo_engine.h
test -d cmd/mornlea
test -d cmd/mornlea-server
test -d engine/crates/mornlea_mesh
test -f engine/include/mornlea_engine.h
test -z "$(git grep -l '\"minecraft-go/internal/' -- '*.go' || true)"
```

Within moved command code, mechanically update Command comments, FlagSet names, error/log prefixes, window title, `captureGoldenDir`, test identifiers and helper env names. Use `mornleaServerHost`/`mornleaServer*` for former `mcgod*` identifiers. Do not change scenario, timings, fixtures or test entry set except identity-driven test renames.

- [ ] **Step 4: Switch Rust ABI and packaging identity**

Apply the exact one-to-one mapping:

```text
mcgo_mesh                     -> mornlea_mesh
libmcgo_mesh.dylib            -> libmornlea_mesh.dylib
mcgo_engine.h                 -> mornlea_engine.h
MCGO_ENGINE_H                 -> MORNLEA_ENGINE_H
MCGO_ENGINE_ABI_VERSION       -> MORNLEA_ENGINE_ABI_VERSION
MCGO_STATUS_*                 -> MORNLEA_STATUS_*
mcgo_engine_abi_version       -> mornlea_engine_abi_version
mcgo_mesh_section             -> mornlea_mesh_section
-lmcgo_mesh                   -> -lmornlea_mesh
```

Update Cargo workspace/package/lock and install name. `greedy.rs`, `input.rs`, `light.rs`, `quad.rs` and algorithm bodies move unchanged. Update `native_abi.go` include/link/constants/calls only. ABI version stays 1; status values stay 0..9.

- [ ] **Step 5: Switch Make, CI, Hook and archcheck**

Update Make variables and help to build `bin/mornlea`, `bin/mornlea-server` and `MORNLEA_DYLIB := bin/libmornlea_mesh.dylib`; the server build uses `CGO_ENABLED=0`. Update CI and Make package paths to new client/server commands. Keep Rust-first ordering and client `@loader_path` flags unchanged. Add the single exact `/.gitnexus/` rule to `.gitignore`, so the post-merge GitNexus CLI cannot dirty tracked files.

Update Hook header/crate routes, escape variable and output prefix; update all Hook tests including deleted-Rust-path routing. `.claude/settings.json` has no old identity and must remain unchanged.

Update archcheck module trimming, WebGPU unique path, source scan roots and Linux server closure. Rename identity-specific tests/error text but preserve their assertions.

- [ ] **Step 6: Compare moved artifacts and run focused GREEN**

Run:

```bash
mornlea_invariants="$(git rev-parse --git-common-dir)/mornlea-m4q-evidence"
make rust

for file in cmd/mornlea/testdata/golden/*.png; do
  hash=$(shasum -a 256 "$file" | awk '{print $1}')
  printf '%s  %s\n' "$hash" "${file##*/}"
done | LC_ALL=C sort > "$mornlea_invariants/goldens.after"
cmp "$mornlea_invariants/goldens.before" "$mornlea_invariants/goldens.after"

sed 's#^cmd/mcgo/#cmd/mornlea/#' "$mornlea_invariants/client-files.before" > "$mornlea_invariants/client-files.expected"
git ls-files 'cmd/mornlea/**' | LC_ALL=C sort > "$mornlea_invariants/client-files.after"
diff -u "$mornlea_invariants/client-files.expected" "$mornlea_invariants/client-files.after"
sed 's#^cmd/mcgod/#cmd/mornlea-server/#' "$mornlea_invariants/server-files.before" > "$mornlea_invariants/server-files.expected"
git ls-files 'cmd/mornlea-server/**' | LC_ALL=C sort > "$mornlea_invariants/server-files.after"
diff -u "$mornlea_invariants/server-files.expected" "$mornlea_invariants/server-files.after"
sed 's#^engine/crates/mcgo_mesh/#engine/crates/mornlea_mesh/#' "$mornlea_invariants/rust-files.before" > "$mornlea_invariants/rust-files.expected"
git ls-files 'engine/crates/mornlea_mesh/**' | LC_ALL=C sort > "$mornlea_invariants/rust-files.after"
diff -u "$mornlea_invariants/rust-files.expected" "$mornlea_invariants/rust-files.after"

sed \
  -e 's#cmd/mcgo/#cmd/mornlea/#g' \
  -e 's#cmd/mcgod/#cmd/mornlea-server/#g' \
  -e 's/TestMCGodHasNoGraphicsDependencies/TestMornleaServerHasNoGraphicsDependencies/' \
  -e 's/TestMcgoUsesLoginStreamsInsteadOfAttachedServerEndpoints/TestMornleaUsesLoginStreamsInsteadOfAttachedServerEndpoints/' \
  -e 's/TestMcgoBenchmarkTCPPathUsesTheSharedLoginStateMachine/TestMornleaBenchmarkTCPPathUsesTheSharedLoginStateMachine/' \
  -e 's/TestMCGodProcessReleasesWorldLockAfterSIGTERM/TestMornleaServerProcessReleasesWorldLockAfterSIGTERM/' \
  -e 's/TestMCGodProcessSaveFailureExitsNonzero/TestMornleaServerProcessSaveFailureExitsNonzero/' \
  -e 's/TestMCGodProcess/TestMornleaServerProcess/' \
  "$mornlea_invariants/test-entries.before" > "$mornlea_invariants/test-entries.expected"
printf '%s\n' 'internal/archcheck/identity_test.go:TestMornleaCurrentIdentity' \
  >> "$mornlea_invariants/test-entries.expected"
LC_ALL=C sort -o "$mornlea_invariants/test-entries.expected" "$mornlea_invariants/test-entries.expected"

rg -n '^func (Test|Benchmark|Fuzz)[A-Za-z0-9_]+' --glob '*_test.go' \
  | sed -E 's/:([0-9]+):func /:/' \
  | sed -E 's/\(.*//' \
  | LC_ALL=C sort > "$mornlea_invariants/test-entries.after"
diff -u "$mornlea_invariants/test-entries.expected" "$mornlea_invariants/test-entries.after"

go list ./...
go test ./cmd/mornlea ./cmd/mornlea-server ./internal/mesh ./internal/archcheck -race -count=1
node --test scripts/agent-hooks/guard.test.mjs
```

Expected: golden hash cmp exact; new packages list/build; identity guard GREEN; old directories absent.

- [ ] **Step 7: Verify Rust/dylib and Linux closure**

Run:

```bash
make rust-check
make build
test -x bin/mornlea
test -x bin/mornlea-server
test -f bin/libmornlea_mesh.dylib
otool -D bin/libmornlea_mesh.dylib | rg -Fx '@rpath/libmornlea_mesh.dylib'
otool -L bin/mornlea | rg -F '@rpath/libmornlea_mesh.dylib'
otool -l bin/mornlea | rg -F 'path @loader_path'
nm -gU bin/libmornlea_mesh.dylib | rg 'mornlea_(engine_abi_version|mesh_section)'
! nm -gU bin/libmornlea_mesh.dylib | rg 'mcgo_'
go test ./internal/mesh -run 'Native|Parity|MeshSection' -race -count=1

CGO_ENABLED=0 GOOS=linux go build -o /private/tmp/mornlea-server ./cmd/mornlea-server
test -z "$(CGO_ENABLED=0 GOOS=linux go list -deps ./cmd/mornlea-server | rg 'internal/(client|mesh|render|gfx)|glfw|webgpu|x/image/font')"
```

Expected: all commands PASS; only new dylib symbols load; server remains pure Linux/no CGO.

- [ ] **Step 8: Run exact current-identity scans**

Run the permanent archcheck plus explicit tracked-tree diagnostics:

```bash
go test ./internal/archcheck -run 'Mornlea|Identity|WebGPU|Server|LoginStreams' -count=1

old_code_identity=$(git grep -n -I -i -E 'minecraft[-_]go|mcgo' -- \
  go.mod cmd internal engine Makefile .github .codex scripts .gitignore \
  ':!internal/config/**' \
  ':!internal/profile/**' \
  ':!internal/storage/backup.go' \
  ':!internal/storage/backup_test.go' || true)
test -z "$old_code_identity"
```

Expected: zero old identity outside the exact compatibility files. Current docs and stable OpenSpec specs are updated by Tasks 8 and 10 respectively; historical trees are never included in this code/build scan.

- [ ] **Step 9: Format, validate and commit the atomic switch**

Run:

```bash
gofmt -w internal/archcheck/identity_test.go $(git diff --name-only --diff-filter=ACMR -- '*.go')
cargo fmt --manifest-path engine/Cargo.toml -- --check
cargo clippy --manifest-path engine/Cargo.toml --workspace --all-targets --locked -- -D warnings
go test ./cmd/mornlea ./cmd/mornlea-server ./internal/mesh ./internal/archcheck -race -count=1
go vet ./...
test -z "$(gofmt -l .)"
openspec validate --all --strict --no-interactive
git diff --check
git add -A -- go.mod cmd engine internal Makefile .github .gitignore .codex scripts openspec/changes/m4q-mornlea-project-rename/tasks.md
git diff --cached --check
git commit -m "refactor: 切换 Mornlea 项目身份"
```

Expected: one buildable atomic commit; task 7.1 checked; no old compatibility wrapper or generated native artifact tracked.

---

### Task 8: 更新当前文档与迁移说明

**Files:**
- Modify: `README.md`
- Modify: `AGENTS.md`
- Modify: `CLAUDE.md`
- Modify: `openspec/config.yaml`
- Modify: `docs/openspec.md`
- Modify: `docs/notes/lan-server.md`
- Create: `docs/notes/mornlea-migration.md`
- Modify: `openspec/changes/m4q-mornlea-project-rename/tasks.md`
- Report only, ignored: `.superpowers/sdd/2026-08-12-m4q-mornlea-project-rename/task-8-report.md`

**Interfaces:**
- Current product narrative: `Mornlea`, repository `channing771/mornlea`, module `github.com/channing771/mornlea`.
- Current commands: `mornlea`, `mornlea-server`; current artifacts: `bin/mornlea`, `bin/mornlea-server`, `bin/libmornlea_mesh.dylib`.
- New data paths: `mornlea/config.json`, `mornlea/profile.json`.
- Historical design/archive/performance documents stay unchanged and are explained once by the migration note.

- [ ] **Step 1: Update only current user/developer entry points**

In README, update title, clone/module/commands/build artifacts/config paths/capture paths/tree layout and current architecture wording. Keep the statement that Mornlea is an independent voxel game and does not support official Minecraft protocols/saves/assets. Do not rewrite historical design links merely because their filenames contain the old name.

Update AGENTS/CLAUDE identically: project title/current M4Q Mornlea baseline (including inherited M4P Rust mesh and main's clouds), new commands/module/Rust identity, new Hook escape variable, and still-current architecture/gates. Update `openspec/config.yaml` context and `docs/openspec.md` escape variable. Update LAN examples to `mornlea-server`/`mornlea`.

- [ ] **Step 2: Write one concise migration note**

Create `docs/notes/mornlea-migration.md` with these exact sections:

```markdown
# Mornlea 改名迁移

## 名称映射
仓库、Go module、客户端、专用服务端、Rust dylib/header/C symbol 与 Hook 环境变量的新旧表。

## 本机数据
新默认目录为 `mornlea`。仅当新文件缺失时，程序校验并复制旧 `minecraft-go` config/profile；旧文件不移动、不删除。

## 回退与单向性
旧版本继续读取旧目录。新版本写入新目录后，两边不会自动同步；不要交替运行并假定状态会合并。

## 历史资料
`docs/superpowers/**`、归档 OpenSpec 与历史性能证据保留当时真实名称；当前使用方法以 README 和本说明为准。
```

- [ ] **Step 3: Scan current docs without broad history rewrites**

Run:

```bash
cmp AGENTS.md CLAUDE.md
current_doc_matches=$(mktemp /private/tmp/mornlea-doc-matches.XXXXXX)
git grep -n -I -i -E 'minecraft[-_]go|mcgo' -- \
  README.md AGENTS.md CLAUDE.md openspec/config.yaml docs/openspec.md docs/notes/lan-server.md \
  > "$current_doc_matches" || test $? = 1
historical_design_link='docs/superpowers/specs/2026-07-26-minecraft-go-design.md'
test "$(rg -o -F "$historical_design_link" "$current_doc_matches" | wc -l | tr -d ' ')" = 1
old_doc_tokens=$(rg -o -i 'minecraft[-_]go|mcgo' "$current_doc_matches")
test "$(printf '%s\n' "$old_doc_tokens" | wc -l | tr -d ' ')" = 1
test "$old_doc_tokens" = 'minecraft-go'
```

Expected: the single historical README link remains exact; removing only that literal leaves no old current identity anywhere on the same or another line. The intentional old/new map is in `docs/notes/mornlea-migration.md`, which is not part of this scan.

- [ ] **Step 4: Validate documentation against real commands**

Run:

```bash
make rust
make help
set +e
go run ./cmd/mornlea-server -h >/private/tmp/mornlea-server-help.txt 2>&1
server_help_rc=$?
set -e
test "$server_help_rc" = 1
rg -F 'flag: help requested' /private/tmp/mornlea-server-help.txt
go test ./cmd/mornlea ./cmd/mornlea-server ./internal/archcheck -count=1
openspec validate --all --strict --no-interactive
git diff --check
```

Expected: docs name only shipped commands/artifacts; no future behavior is presented as implemented.

- [ ] **Step 5: Commit current docs**

Run:

```bash
git add README.md AGENTS.md CLAUDE.md openspec/config.yaml docs/openspec.md docs/notes/lan-server.md docs/notes/mornlea-migration.md openspec/changes/m4q-mornlea-project-rename/tasks.md
git diff --cached --check
git commit -m "docs: 更新 Mornlea 当前入口"
```

Expected: only current docs/migration note/tasks; historical trees untouched; task 8.1 checked.

---

### Task 9: 不变量、全仓门禁与独立评审

**Files:**
- Modify only if a reviewed defect requires it: files explicitly approved through an M4Q plan update
- Modify after gates: `openspec/changes/m4q-mornlea-project-rename/design.md` for final hash/provenance evidence
- Report only, ignored: `.superpowers/sdd/2026-08-12-m4q-mornlea-project-rename/task-9-report.md`
- Do not modify: `openspec/changes/m4q-mornlea-project-rename/tasks.md` checkbox 9.1 before approval

**Interfaces:**
- Consumes: Tasks 1–8 commits and Task 1 invariant manifest.
- Produces: `READY_FOR_REVIEW` evidence; no completion claim or archive.

- [ ] **Step 1: Re-freeze scope and compare fixed artifacts**

Run:

```bash
git status --short --branch
mornlea_invariants="$(git rev-parse --git-common-dir)/mornlea-m4q-evidence"
task1_head=$(cat "$mornlea_invariants/task1-head")
test -n "$task1_head"
git rev-parse HEAD > "$mornlea_invariants/task9-producer-head"
shasum -a 256 -c "$mornlea_invariants/static.sha256"

for file in cmd/mornlea/testdata/golden/*.png; do
  hash=$(shasum -a 256 "$file" | awk '{print $1}')
  printf '%s  %s\n' "$hash" "${file##*/}"
done | LC_ALL=C sort > "$mornlea_invariants/golden-after.sha256"
diff -u "$mornlea_invariants/golden.sha256" "$mornlea_invariants/golden-after.sha256"

test -z "$(git diff --name-only "$task1_head"..HEAD -- \
  'internal/network/testdata/**' 'internal/storage/testdata/**' \
  'internal/worldgen/testdata/**' 'docs/notes/perf-baseline*.json')"
```

Expected: all static hashes and 10 basename golden hashes match; the exact Tasks 1–8 producer HEAD is durable; no fixed artifact differs from the Task 1 merged baseline.

- [ ] **Step 2: Run protocol/storage/scenario behavior gates**

Run:

```bash
make rust
go test ./internal/network -run 'Protocol|Golden|Fixture' -race -count=1
go test ./internal/storage -run 'Fixture|Golden|Schema|Magic|Future' -race -count=1
go test ./cmd/mornlea -run 'ScenarioV15|BenchmarkScenarioVersion|Capture' -race -count=1
go test ./internal/mesh -run 'Native|Parity|ABI|Light|Mesh' -race -count=1
```

Expected: protocol remains 15; chunk/player remain v8/v6; scenario remains 15; native ABI/parity unchanged.

- [ ] **Step 3: Run fuzz and non-update visual gates**

Run:

```bash
mornlea_invariants="$(git rev-parse --git-common-dir)/mornlea-m4q-evidence"
go test ./internal/network -run '^$' -fuzz '^FuzzSmallPacketCodec$' -fuzztime=10s
go test ./internal/storage -run '^$' -fuzz '^FuzzDecodeChunkPayload$' -fuzztime=10s
go test ./internal/mesh -run '^$' -fuzz '^FuzzNativeMeshRejectsMalformedInput$' -fuzztime=10s

current_visual=$(mktemp -d /private/tmp/mornlea-m4q-visual.XXXXXX)
current_home=$(mktemp -d /private/tmp/mornlea-m4q-home.XXXXXX)
hardware_chip="$(system_profiler SPHardwareDataType | sed -n 's/^[[:space:]]*Chip: //p')"
test -n "$hardware_chip"
printf '视觉验证硬件 Chip: %s\n' "$hardware_chip"
current_profile_dir="$current_home/Library/Application Support/mornlea"
mkdir -p "$current_profile_dir"
chmod 0700 "$current_profile_dir"
printf '%s' '{"version":1,"player_id":"00112233-4455-4677-8899-aabbccddeeff","display_name":"Capture"}' > "$current_profile_dir/profile.json"
chmod 0600 "$current_profile_dir/profile.json"
set +e
HOME="$current_home" VISUAL_OUT="$current_visual" make visual-check > "$current_visual/run.log" 2>&1
current_visual_rc=$?
set -e

baseline_visual=$(mktemp -d /private/tmp/mornlea-m4q-main-visual.XXXXXX)
baseline_home=$(mktemp -d /private/tmp/mornlea-m4q-main-home.XXXXXX)
baseline_profile_dir="$baseline_home/Library/Application Support/minecraft-go"
mkdir -p "$baseline_profile_dir"
chmod 0700 "$baseline_profile_dir"
cp "$current_profile_dir/profile.json" "$baseline_profile_dir/profile.json"
chmod 0600 "$baseline_profile_dir/profile.json"
(
  baseline_tree=$(mktemp -d /private/tmp/mornlea-m4q-main-tree.XXXXXX)
  rmdir "$baseline_tree"
  restore_tree() {
    test ! -e "$baseline_tree/.git" || git worktree remove "$baseline_tree"
  }
  trap 'restore_tree' EXIT
  trap 'restore_tree; exit 129' HUP
  trap 'restore_tree; exit 130' INT
  trap 'restore_tree; exit 143' TERM
  git worktree add --detach "$baseline_tree" "$(cat "$mornlea_invariants/task1-origin-main")"
  (
    cd "$baseline_tree"
    set +e
    HOME="$baseline_home" VISUAL_OUT="$baseline_visual" make visual-check > "$baseline_visual/run.log" 2>&1
    baseline_visual_rc=$?
    set -e
  )
  restore_tree
  trap - EXIT HUP INT TERM
)

for scene in terrain-noon hud-hotbar-health avatar-nametag inventory-crafting debug-panel skylight-tunnel block-light-room materials-showcase target-block-feedback oak-grove; do
  cmp "$current_visual/${scene}.png" "$baseline_visual/${scene}.png"
done
case "$hardware_chip" in
  'Apple M2')
    test "$current_visual_rc" -ne 0
    test "$baseline_visual_rc" -ne 0
    for artifact in materials-showcase-actual.png materials-showcase-diff.png oak-grove-actual.png oak-grove-diff.png; do
      cmp "$current_visual/$artifact" "$baseline_visual/$artifact"
    done
    rg '^已抓取场景 (materials-showcase|oak-grove):' "$current_visual/run.log" > "$current_visual/failures.txt"
    rg '^已抓取场景 (materials-showcase|oak-grove):' "$baseline_visual/run.log" > "$baseline_visual/failures.txt"
    cmp "$current_visual/failures.txt" "$baseline_visual/failures.txt"
    rg -Fx '已抓取场景 materials-showcase: 最大通道差 1，差异像素 26/230400（0.0113%），首个差异像素在 (172,26)' "$current_visual/failures.txt"
    rg -Fx '已抓取场景 oak-grove: 最大通道差 47，差异像素 10/230400（0.0043%），首个差异像素在 (89,86)' "$current_visual/failures.txt"
    test "$(find "$current_visual" -maxdepth 1 -name '*-actual.png' | wc -l | tr -d ' ')" = 2
    test "$(find "$current_visual" -maxdepth 1 -name '*-diff.png' | wc -l | tr -d ' ')" = 2
    ;;
  *)
    test "$current_visual_rc" -eq 0
    test "$baseline_visual_rc" -eq 0
    test "$(find "$current_visual" -maxdepth 1 \( -name '*-actual.png' -o -name '*-diff.png' \) | wc -l | tr -d ' ')" = 0
    test "$(find "$baseline_visual" -maxdepth 1 \( -name '*-actual.png' -o -name '*-diff.png' \) | wc -l | tr -d ' ')" = 0
    ;;
esac
```

Expected: all fuzzers complete without crash. The script records `system_profiler SPHardwareDataType`'s exact Chip, then the isolated-HOME current branch and raw Task 1 main must first have 10/10 byte-identical scene PNGs. On Apple M2/macOS, the four identical failure artifacts and only the two exact inherited nonzero summaries are required, so the other eight scenes pass tracked golden. On every non-M2 Chip, both `visual-check` commands must exit 0 and neither output may contain `*-actual.png` or `*-diff.png`. Any other failure or byte drift blocks. Never run `visual-update`, update golden, thresholds or capture code.

- [ ] **Step 4: Verify dylib portability and Linux server boundary**

Run:

```bash
make rust-check
make build
otool -D bin/libmornlea_mesh.dylib | rg -Fx '@rpath/libmornlea_mesh.dylib'
otool -L bin/mornlea | rg -F '@rpath/libmornlea_mesh.dylib'
otool -l bin/mornlea | rg -F 'path @loader_path'

(
  mornlea_target_backup=$(mktemp -d /private/tmp/mornlea-target.XXXXXX)
  restore_target() {
    if test -d "$mornlea_target_backup/target" && test ! -e engine/target; then
      mv "$mornlea_target_backup/target" engine/target
    fi
  }
  trap 'restore_target' EXIT
  trap 'restore_target; exit 129' HUP
  trap 'restore_target; exit 130' INT
  trap 'restore_target; exit 143' TERM
  mv engine/target "$mornlea_target_backup/target"
  set +e
  bin/mornlea -h >/private/tmp/mornlea-help.txt 2>&1
  mornlea_help_status=$?
  set -e
  test "$mornlea_help_status" = 1
  rg -F 'flag: help requested' /private/tmp/mornlea-help.txt
  ! rg -i 'dyld|Library not loaded' /private/tmp/mornlea-help.txt
  restore_target
  trap - EXIT HUP INT TERM
)

CGO_ENABLED=0 GOOS=linux go build -o /private/tmp/mornlea-server ./cmd/mornlea-server
test -z "$(CGO_ENABLED=0 GOOS=linux go list -deps ./cmd/mornlea-server | rg 'internal/(client|mesh|render|gfx)|glfw|webgpu|x/image/font')"
```

Expected: colocated dylib works with Cargo target absent; Linux server closure remains clean; target is restored even on failure.

- [ ] **Step 5: Record benchmarks without changing baselines**

Run:

```bash
mornlea_invariants="$(git rev-parse --git-common-dir)/mornlea-m4q-evidence"
go test ./internal/mesh -run '^$' -bench BenchmarkMeshTerrainSection -benchmem -count=5
go test ./internal/render -run '^$' -bench 'Mesh|Light' -benchmem -count=3

MORNLEA_PERF_DIR=$(mktemp -d /private/tmp/mornlea-m4q-perf.XXXXXX)
go run ./cmd/mornlea --benchmark --benchmark-transport memory --perf-output "$MORNLEA_PERF_DIR/memory-v15.json"
go run ./cmd/perfcheck --baseline "$MORNLEA_PERF_DIR/memory-v15.json" --current "$MORNLEA_PERF_DIR/memory-v15.json" --max-regression 0.20
producer_head=$(cat "$mornlea_invariants/task9-producer-head")
test "$(jq -r .git_commit "$MORNLEA_PERF_DIR/memory-v15.json")" = "$producer_head"
cp "$MORNLEA_PERF_DIR/memory-v15.json" "$mornlea_invariants/task9-memory-v15.json"
cmp "$MORNLEA_PERF_DIR/memory-v15.json" "$mornlea_invariants/task9-memory-v15.json"
shasum -a 256 "$mornlea_invariants/task9-memory-v15.json" > "$mornlea_invariants/task9-perf.sha256"
```

Expected: report identity/scenario/structure and self-check PASS. Record numeric results only; do not copy over tracked baseline or compare Memory to TCP for a pure rename.

- [ ] **Step 6: Run shared final gates**

Run:

```bash
make test-race
go test ./internal/archcheck -count=1
go vet ./...
test -z "$(gofmt -l .)"
cargo fmt --manifest-path engine/Cargo.toml -- --check
cargo clippy --manifest-path engine/Cargo.toml --workspace --all-targets --locked -- -D warnings
node --test scripts/agent-hooks/guard.test.mjs
cmp AGENTS.md CLAUDE.md
openspec validate --all --strict --no-interactive
git diff --check
```

Expected: every command exit 0; `gofmt -l .` has no output.

- [ ] **Step 7: Run final identity/scope audit**

Run:

```bash
go test ./internal/archcheck -run 'Mornlea|Identity' -count=1
test ! -e cmd/mcgo
test ! -e cmd/mcgod
test ! -e engine/crates/mcgo_mesh
test ! -e engine/include/mcgo_engine.h
test ! -e bin/mcgo
test ! -e bin/libmcgo_mesh.dylib
git status --short --branch
```

Update `design.md` with the exact Task 1 merge/origin heads, frozen manifest hashes, Tasks 1–8 producer HEAD, performance report hash/embedded producer HEAD, exact dual-baseline visual evidence, archive state and external-operation permission fact. Do not add performance conclusions. Then publish that evidence before review:

```bash
openspec validate m4q-mornlea-project-rename --strict --no-interactive
git diff --check
git add openspec/changes/m4q-mornlea-project-rename/design.md
git diff --cached --check
git commit -m "docs: 记录 M4Q 最终验证证据"
test "$(git diff-tree --no-commit-id --name-only -r HEAD)" = 'openspec/changes/m4q-mornlea-project-rename/design.md'
test -z "$(git status --porcelain)"
```

Expected: the independent reviewer can inspect the exact evidence in `base..HEAD`; no uncommitted design artifact is deferred to archive.

- [ ] **Step 8: Request independent broad review and stop**

Use `superpowers:requesting-code-review`. Reviewer must inspect `$(cat "$(git rev-parse --git-common-dir)/mornlea-m4q-evidence/task1-head")..HEAD` plus Task 9 report and answer:

```text
Critical / Important / Minor findings
Spec compliance
Data-loss and concurrency safety
Identity/compatibility allowlist precision
Golden/fixture/baseline invariance
Build/packaging/server closure
Final verdict: APPROVED or NEEDS_FIXES
```

Expected: stop at `READY_FOR_REVIEW`; do not check 9.1, archive M4Q, merge or push. Review fixes require a scoped plan update and fresh affected gates.

---

### Task 10: 关闭评审、完成 M4Q 并归档稳定规格

**Files:**
- Modify: files explicitly approved for review fixes, if any
- Modify: `openspec/changes/m4q-mornlea-project-rename/tasks.md`
- Move: `openspec/changes/m4q-mornlea-project-rename/**` → `openspec/changes/archive/<archive-date>-m4q-mornlea-project-rename/**`, where `<archive-date>` is the local execution date used by OpenSpec
- Create: `openspec/specs/project-identity/spec.md`
- Create: `openspec/specs/local-data-migration/spec.md`
- Modify: `openspec/specs/natural-material-generation/spec.md`
- Modify: `openspec/specs/repository-code-organization/spec.md`
- Modify: `openspec/specs/rust-engine-mesh/spec.md`
- Report only, ignored: `.superpowers/sdd/2026-08-12-m4q-mornlea-project-rename/task-10-report.md`

**Interfaces:**
- Consumes: scoped independent `APPROVED`, zero unresolved Critical/Important, all Task 9 gates.
- Produces: completed/archived M4Q and current main specs using Mornlea identity.

- [ ] **Step 1: Resolve review findings with bounded RED/GREEN**

For each accepted finding, first reproduce it with a focused test/mutation, make the smallest root-cause fix, run affected race/arch/OpenSpec gates, and commit the fix separately. Record `CLOSED` evidence. Minor findings may be deferred only with explicit rationale in the report; Critical/Important cannot remain open.

- [ ] **Step 2: Obtain scoped re-review**

After the final review-fix commit, regenerate the Task 9 evidence against that exact HEAD and rerun the full final gates before re-review. At minimum rerun: fixed artifact/hash comparison from Task 9 Step 1; `make rust`; all focused race commands from Step 2; all three 10-second fuzzers and the isolated-HOME dual-baseline visual block from Step 3; the complete dylib target-absent/Linux block from Step 4; benchmark producer/self-check with exact `git_commit`; `make test-race`, archcheck, vet, Go/Rust format, clippy, Hook tests, docs cmp, OpenSpec strict, diff check and identity audit from Steps 6–7. Update and separately commit `design.md` evidence again before review. No old producer result may stand in for a changed HEAD.

Request the same reviewer to inspect the review-fix commits plus the refreshed evidence commit. Expected: `APPROVED`, zero new Critical/Important and all original blocking findings `CLOSED`.

- [ ] **Step 3: Check the final OpenSpec task**

Only after approval, change task 9.1 to `[x]`. Run:

```bash
openspec status --change m4q-mornlea-project-rename --json
openspec validate --all --strict --no-interactive
git diff --check
git add openspec/changes/m4q-mornlea-project-rename/tasks.md
git diff --cached --check
git commit -m "docs: 完成 M4Q Mornlea 项目改名"
```

Expected: active M4Q reports all tasks complete.

- [ ] **Step 4: Archive M4Q into current specs**

Run:

```bash
test -z "$(find openspec/changes/archive -maxdepth 1 -type d -name '*-m4q-mornlea-project-rename' -print)"
openspec archive m4q-mornlea-project-rename --yes
m4q_archive=$(find openspec/changes/archive -maxdepth 1 -type d -name '*-m4q-mornlea-project-rename' -print)
test "$(printf '%s\n' "$m4q_archive" | wc -l | tr -d ' ')" = 1
test ! -e openspec/changes/m4q-mornlea-project-rename
test -f "$m4q_archive/tasks.md"
test -f openspec/specs/project-identity/spec.md
test -f openspec/specs/local-data-migration/spec.md
rg -F 'mornlea-server' openspec/specs/natural-material-generation/spec.md
rg -F 'TestMornleaCurrentIdentity' openspec/specs/repository-code-organization/spec.md
! rg -F 'TestMCGodHasNoGraphicsDependencies' openspec/specs/repository-code-organization/spec.md
rg -F 'libmornlea_mesh.dylib' openspec/specs/rust-engine-mesh/spec.md
openspec validate --all --strict --no-interactive
git diff --check
git add -A -- \
  openspec/changes/m4q-mornlea-project-rename \
  "$m4q_archive" \
  openspec/specs/project-identity \
  openspec/specs/local-data-migration \
  openspec/specs/natural-material-generation \
  openspec/specs/repository-code-organization \
  openspec/specs/rust-engine-mesh
git diff --cached --check
git commit -m "docs: 归档 M4Q Mornlea 项目改名"
```

Expected: archive retains old/new migration terms as stable behavior; current natural-material/Rust specs use new command/native identity; M4O/M4P historical archives remain byte-untouched.

- [ ] **Step 5: Post-archive final verification**

Run:

```bash
openspec list --json
openspec validate --all --strict --no-interactive
make rust
go test ./internal/config ./internal/profile ./cmd/mornlea ./cmd/mornlea-server ./internal/mesh ./internal/archcheck -race -count=1
git diff --check
git status --short --branch
```

Expected: no active M4Q; all specs validate; focused code remains GREEN; tracked worktree clean. Stop before push/PR/external rename unless separately authorized.

---

### Task 11: PR 合并后的仓库外改名（操作手册，不属于源码实施）

**Files:**
- External state only: GitHub repository names, Git remotes, local workspace path, GitNexus index
- Do not modify tracked source files

**Interfaces:**
- Preconditions: source PR merged to `main`; all required worktrees clean and classified; GitHub admin permission available.
- Produces: `channing771/mornlea`, optional `chenyang-zz/mornlea`, local `/Users/sheepzhao/WorkSpace/mornlea`, GitNexus repo name `mornlea`.

- [ ] **Step 1: Verify merge and GitHub authority**

Run:

```bash
root=/Users/sheepzhao/WorkSpace/minecraft-go
test "$(git -C "$root" symbolic-ref --short HEAD)" = main
test -z "$(git -C "$root" status --porcelain)"
git -C /Users/sheepzhao/WorkSpace/minecraft-go fetch origin --prune
git -C /Users/sheepzhao/WorkSpace/minecraft-go pull --ff-only origin main
test "$(git -C "$root" rev-parse HEAD)" = "$(git -C "$root" rev-parse origin/main)"
test "$(sed -n 's/^module //p' "$root/go.mod")" = github.com/channing771/mornlea
test ! -e "$root/openspec/changes/m4q-mornlea-project-rename"
m4q_archive=$(git -C "$root" ls-files 'openspec/changes/archive/*-m4q-mornlea-project-rename/tasks.md')
test "$(printf '%s\n' "$m4q_archive" | wc -l | tr -d ' ')" = 1
test -f "$root/$m4q_archive"
test -f "$root/openspec/specs/project-identity/spec.md"
(cd "$root" && openspec validate --all --strict --no-interactive)
test "$(gh repo view channing771/minecraft-go --json viewerPermission -q .viewerPermission)" = ADMIN
```

Expected: local root is clean `main`, exactly equals `origin/main`, contains the archived M4Q/current Mornlea specs/module, and caller has ADMIN. Current known `gh` account `chenyang-zz` has only READ on origin; if still true, stop and switch to an authorized credential or have `channing771` perform the rename. After every assertion succeeds, stop once more for explicit human confirmation before changing GitHub external state.

- [ ] **Step 2: Rename GitHub repositories and remotes**

Run after ADMIN succeeds:

```bash
gh repo rename -R channing771/minecraft-go mornlea --yes
gh repo view channing771/mornlea --json nameWithOwner,url
git -C /Users/sheepzhao/WorkSpace/minecraft-go remote set-url origin https://github.com/channing771/mornlea.git

if test "$(gh repo view chenyang-zz/minecraft-go --json viewerPermission -q .viewerPermission)" = ADMIN; then
  gh repo rename -R chenyang-zz/minecraft-go mornlea --yes
  git -C /Users/sheepzhao/WorkSpace/minecraft-go remote set-url fork https://github.com/chenyang-zz/mornlea.git
fi

git -C /Users/sheepzhao/WorkSpace/minecraft-go remote -v
git -C /Users/sheepzhao/WorkSpace/minecraft-go ls-remote --symref origin HEAD
```

Expected: origin resolves to new repository. Do not rely on GitHub's old-URL redirect as the configured remote.

- [ ] **Step 3: Classify and safely remove linked worktrees**

First classify every linked worktree without changing it. A worktree with ignored files is never considered removable, even when tracked status is clean:

```bash
root=/Users/sheepzhao/WorkSpace/minecraft-go
git -C "$root" worktree list --porcelain |
awk '
  function emit() { if (path != "") print path "\t" branch }
  /^worktree / { emit(); path = substr($0, 10); branch = "DETACHED"; next }
  /^branch / { branch = substr($0, 8); next }
  END { emit() }
' |
while IFS=$'\t' read -r worktree_path branch_ref; do
  test "$worktree_path" = "$root" && continue
  tracked=$(git -C "$worktree_path" status --porcelain)
  ignored=$(git -C "$worktree_path" ls-files --others --ignored --exclude-standard)
  if test -n "$ignored"; then
    printf 'KEEP_IGNORED\t%s\t%s\n%s\n' "$worktree_path" "$branch_ref" "$ignored"
  elif test -n "$tracked"; then
    printf 'KEEP_DIRTY\t%s\t%s\n%s\n' "$worktree_path" "$branch_ref" "$tracked"
  elif test "$branch_ref" = DETACHED; then
    printf 'KEEP_DETACHED\t%s\t%s\n' "$worktree_path" "$branch_ref"
  elif git -C "$root" merge-base --is-ancestor "$branch_ref" main; then
    printf 'MERGED_CANDIDATE\t%s\t%s\n' "$worktree_path" "$branch_ref"
  else
    printf 'KEEP_UNMERGED\t%s\t%s\n' "$worktree_path" "$branch_ref"
  fi
done
```

There is intentionally no bulk-removal loop. For each `MERGED_CANDIDATE`, inspect its exact path and obtain path-specific human approval; only then assign that literal path to `approved_worktree_path` and run `git worktree remove "$approved_worktree_path"`. Any `KEEP_IGNORED` must first be backed up or explicitly declared disposable for that exact path; never infer permission from a clean tracked status and never use `--force`. Retain all other classifications.

Before moving the root, mechanically require that only the root worktree remains:

```bash
root=/Users/sheepzhao/WorkSpace/minecraft-go
git -C "$root" worktree list --porcelain
test "$(git -C "$root" worktree list --porcelain | rg '^worktree ' | wc -l | tr -d ' ')" = 1
git -C "$root" worktree prune --dry-run
```

- [ ] **Step 4: Move the local root and repair metadata**

Run from `/Users/sheepzhao/WorkSpace` only after linked worktrees are resolved:

```bash
test ! -e /Users/sheepzhao/WorkSpace/mornlea
mv /Users/sheepzhao/WorkSpace/minecraft-go /Users/sheepzhao/WorkSpace/mornlea
git -C /Users/sheepzhao/WorkSpace/mornlea worktree repair
git -C /Users/sheepzhao/WorkSpace/mornlea rev-parse --show-toplevel
git -C /Users/sheepzhao/WorkSpace/mornlea status --short --branch
```

Expected: new top-level path exact, clean main, no broken linked-worktree metadata.

- [ ] **Step 5: Rebuild GitNexus idempotently**

Current GitNexus has no `minecraft-go` index, so removal may legitimately report absent. Run:

```bash
gitnexus remove /Users/sheepzhao/WorkSpace/minecraft-go --force
gitnexus analyze /Users/sheepzhao/WorkSpace/mornlea --force --skip-agents-md --name mornlea
test -z "$(git -C /Users/sheepzhao/WorkSpace/mornlea status --porcelain)"
gitnexus list > /private/tmp/mornlea-gitnexus-list.txt
rg -A2 '^  mornlea$' /private/tmp/mornlea-gitnexus-list.txt | rg -F 'Path:    /Users/sheepzhao/WorkSpace/mornlea'
test "$(rg -F '/Users/sheepzhao/WorkSpace/mornlea' /private/tmp/mornlea-gitnexus-list.txt | wc -l | tr -d ' ')" = 1
```

Expected: `mornlea` is indexed exactly once at the new absolute path; repository files remain unchanged because Task 7 already added `/.gitnexus/` and `--skip-agents-md` prevents guide rewriting. `gitnexus remove` is idempotent for an unknown old path; any nonzero exit is a real error and is not swallowed.
