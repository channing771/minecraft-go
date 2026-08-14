# Rust Collision/Raycast 与 CI 稳定化 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复 PR #41 的两项真实 CI 调度竞态，并把碰撞解析与方块射线遍历迁入单一 `mornlea_engine` Rust 动态库，同时保持现有玩法、公开 Go API、协议、存档、视觉和性能证据不漂移。

**Architecture:** Go 继续拥有 tunables、玩家/世界状态、权威 tick、callback 与结果发布；Rust 1.97.1 只执行无全局可变状态的 collision 和 raycast 纯 kernel。`internal/nativeabi` 是唯一 engine C ABI/cgo 叶子，collision 使用有界 dense prism，raycast 使用独立 64-record cursor batch；macOS 发布同目录 dylib，Linux amd64 发布同目录 server/so bundle。

**Tech Stack:** Go 1.26、Rust 1.97.1 edition 2024、Cargo workspace、C ABI/cgo、Make、GitHub Actions、OpenSpec 1.7.0。Cargo canonical 命令从 `engine/` workspace root 执行；不新增 Rust dependency。

## Global Constraints

- 实现基线包含已批准设计提交 `3c23b03`，设计文档为 [`docs/superpowers/specs/2026-08-14-rust-engine-collision-raycast-ci-stability-design.md`](../specs/2026-08-14-rust-engine-collision-raycast-ci-stability-design.md)。
- active change 固定命名为 `rust-engine-collision-raycast-ci-stability`；代码前必须完成 proposal、delta specs、design、tasks 与 strict validation。
- CI 只修 run `31813426121` 与 `31813364557` 的两个真实失败；不得夹带 50ms benchmark 契约、其他一秒等待或 workflow retry/去重。
- workspace 只含一个 std-only crate `mornlea_engine`；不得新增第二个 crate/dylib、第三方 Rust dependency、`staticlib`、ECS、async runtime、backend interface 或通用 world-query abstraction。
- ABI major 保持 `1`；现有 `mornlea_engine_abi_version`、`mornlea_mesh_section`、mesh layout 与 status `0..9` 不变，只追加 `mornlea_collision_resolve` 与 `mornlea_raycast_batch`。
- Go 拥有 input、cursor、scratch 与 output；Rust 不保存 Go 指针、不回调 Go、不返回需释放的内存、不启动线程，panic/unwind 不得穿过 C ABI。
- Rust 是 collision/raycast 唯一生产实现；旧 Go kernel 只留在 `_test.go` oracle，不得提供 build-tag fallback、运行时开关或重试。
- `physics.Step`、`core.RaycastBlocks` 及所有生产调用者签名不变；客户端预测与服务端权威自然复用同一 kernel。
- collision 原始 `CollisionBoxSet.Count` 继续 clamp 到 8；snapshot 最多 4096 cell，超限在 source 查询、分配、FFI 和状态发布前原子 panic。
- raycast 每批固定最多 64 record，无新增 `maxDistance` 上限；callback 仍按顺序惰性调用，首个 error 保持 identity。
- macOS arm64 使用相邻 `libmornlea_engine.dylib`；Linux amd64 使用相邻 `mornlea-server + libmornlea_engine.so` 与 `$ORIGIN`。专服允许 Rust/CGO，但仍禁止 client、mesh、render、gfx、WebGPU、GLFW、字体和窗口依赖。
- 不修改协议 v16、玩家/区块/伙伴/world schema、scenario v16、M2 v15/M5 v14 baseline、11 张 visual golden、阈值或 capture fixture。
- benchmark/perf 数值只记录；报告身份/完整性、真实 overflow、数据丢失、I/O、native 加载和所有正确性失败仍阻断。
- 本 Apple workspace 的每个 fresh shell cell 在运行下列命令前先执行 `export PATH="/opt/homebrew/opt/rustup/bin:$PATH"` 并确认 `command -v rustup cargo`；路径缺失则停止且不得下载工具链。GitHub Ubuntu job 使用其 pinned Rust setup 提供的 PATH。
- 每次首次运行 Go 测试前先运行 `make rust`；不提交 `engine/target`、`bin`、visual/perf output 或 ignored task report。
- 自动测试不得启动或聚焦前台窗口；视觉只运行现有 headless capture。
- Go/Rust 注释、测试说明、错误与文档使用中文；标识符、wire/ABI magic、协议字段和外部 API 保留英文。
- 每个任务只提交其 Files；需要计划外生产文件、API、依赖或行为时立即停止，先同步 OpenSpec、设计和本计划。

## Target File Map

| 路径 | 单一职责 |
| --- | --- |
| `openspec/changes/rust-engine-collision-raycast-ci-stability/**` | 本变更唯一 active contract、任务与归档输入 |
| `engine/crates/mornlea_engine/src/ffi.rs` | ABI version/status、指针/范围/overlap 校验与 panic containment |
| `engine/crates/mornlea_engine/src/collision.rs` | collision v1 借用解析与纯 kernel |
| `engine/crates/mornlea_engine/src/raycast.rs` | raycast v1 cursor/batch 与纯 DDA |
| `engine/include/mornlea_engine.h` | 唯一 C ABI 声明；旧 mesh 声明保持原位，只在末尾追加 |
| `internal/nativeabi/native.go` | 唯一 engine header、cgo、link、status 与 raw byte/POD 调用 |
| `internal/mesh/native_abi.go` | mesh 领域的薄兼容包装和现有错误文案 |
| `internal/physics/collision.go` | collision prism 编码、native 调用与固定 output 解码 |
| `internal/physics/*_oracle_test.go` | 旧 Go collision/step test-only oracle |
| `internal/core/raycast.go` | Go 输入校验/归一化、native batch 驱动、callback 与 Point |
| `internal/core/raycast_oracle_test.go` | 旧 Go DDA test-only oracle |
| `Makefile` | macOS client/server bundle、Linux amd64 server bundle 与 canonical gates |
| `.github/workflows/ci.yml` | macOS 全仓门禁与 Linux amd64 原生 bundle job |
| `internal/archcheck/{dependency,platform,identity}_test.go` | native 依赖方向、无图形专服与当前 artifact 身份 |
| `scripts/agent-hooks/guard.{mjs,test.mjs}` | Rust-required 路由与受影响 native 下游门禁 |
| `AGENTS.md`、`CLAUDE.md`、`README*.md`、`docs/notes/{lan-server,progress}.md`、`openspec/config.yaml` | 当前已实现所有权、构建与发布事实 |

---

### Task 1: 建立 combined OpenSpec 契约

**Files:**
- Create: `openspec/changes/rust-engine-collision-raycast-ci-stability/.openspec.yaml`
- Create: `openspec/changes/rust-engine-collision-raycast-ci-stability/proposal.md`
- Create: `openspec/changes/rust-engine-collision-raycast-ci-stability/design.md`
- Create: `openspec/changes/rust-engine-collision-raycast-ci-stability/tasks.md`
- Create: `openspec/changes/rust-engine-collision-raycast-ci-stability/specs/rust-engine-mesh/spec.md`
- Create: `openspec/changes/rust-engine-collision-raycast-ci-stability/specs/project-identity/spec.md`
- Create: `openspec/changes/rust-engine-collision-raycast-ci-stability/specs/rust-engine-collision-raycast/spec.md`
- Report only, ignored: `.superpowers/sdd/2026-08-14-rust-engine-collision-raycast-ci-stability/task-1-report.md`

**Interfaces:**
- Consumes: approved design `3c23b03`; current `rust-engine-mesh`, `project-identity`, `test-timing-discipline` main specs.
- Produces: active spec-driven change whose unchecked tasks map to Tasks 2–10 below and include one final Linux PR-CI item consumed by Task 11.

- [ ] **Step 1: Verify the immutable starting point and source evidence**

Run:

```bash
git status --short --branch
git merge-base --is-ancestor 88558088beecf74d76669d23d83622358b87a73f HEAD
openspec list --json
gh run view 31813426121 --log-failed |
  rg -C 4 'TestReconnectContinuesWorldTimeWithoutRollback|玩家已在线'
gh run view 31813364557 --log-failed |
  rg -C 4 'TestHostHeartbeatTimeoutCleanupIsIsolated|player did not become ready'
```

Expected: worktree clean; ancestor check exit 0; no active change; logs contain these exact RED observations:

```text
--- FAIL: TestReconnectContinuesWorldTimeWithoutRollback (0.65s)
    daylight_multiplayer_test.go:116: LoginClient 阿明: network: remote error in state 2 (code 7): 玩家已在线
--- FAIL: TestHostHeartbeatTimeoutCleanupIsIsolated (30.00s)
    host_lifecycle_test.go:82: player did not become ready
```

Record that there is no deterministic local isomorphic reproducer. Do not describe a locally green `-count=100` run as absence of RED; the cited CI logs are the source RED, and mutation checks later prove the synchronization edges matter.

- [ ] **Step 2: Create the OpenSpec metadata and proposal**

Create `.openspec.yaml` exactly as:

```yaml
schema: spec-driven
created: 2026-08-14
```

`proposal.md` must contain concrete `Why`, `What Changes`, `Non-Goals`, `Compatibility`, and `Affected Areas` sections. It must name both run IDs/assertions, the single `mornlea_engine` library, collision/raycast as independent ABIs, Linux binary+so delivery, ABI v1 additive compatibility, and the unchanged protocol/storage/scenario/golden/baseline identities. It must explicitly exclude the 50ms probe and general deadline cleanup.

- [ ] **Step 3: Write the two MODIFIED deltas**

In `specs/rust-engine-mesh/spec.md`, use `## MODIFIED Requirements` and reproduce the complete current Requirement blocks before changing them:

```markdown
### Requirement: clean checkout 使用 Rust-first 构建
系统 MUST 通过 canonical Make、CI 与 Hook 从 `engine/` workspace root 使用固定的 Rust 1.97.1，在 Go 验证前执行 `cargo build --locked --release` 构建 pinned Rust `cdylib`；workspace MUST 仅含 `mornlea_engine`，并且该 crate 的 normal dependency MUST 只使用 `std`。

#### Scenario: 无预编译 artifact 的构建
- **GIVEN** clean checkout 不含 Cargo target 或 native library
- **WHEN** 运行 `make test-race`
- **THEN** 系统 MUST 先在 `engine/` 目录中以 Rust 1.97.1 执行 `cargo build --locked --release`，再执行 Go race tests
- **AND** `cd engine && rustup show active-toolchain` MUST 报告 `1.97.1` directory override
- **AND** `cargo metadata --no-deps --format-version 1 --manifest-path engine/Cargo.toml` MUST 只报告 workspace member `mornlea_engine`
- **AND** `cargo tree --manifest-path engine/Cargo.toml --workspace --edges normal` MUST 只含 workspace root，且不得报告第三方 dependency

#### Scenario: 本地客户端产物不依赖 Cargo target 位置
- **GIVEN** `make build` 已生成本地客户端产物
- **WHEN** 临时移开 `engine/target`
- **THEN** `bin/mornlea -h` MUST 从同目录 `libmornlea_engine.dylib` 进入 Go 参数解析
- **AND** MUST 以 exit 1 与 `flag: help requested` 证明解析路径已运行
- **AND** 输出 MUST 不含 `dyld` 或 `Library not loaded`
- **AND** 产物 MUST 不包含指向 Cargo 临时 `deps` 目录的 load path

### Requirement: Rust 客户端边界不污染无图形服务端
系统 MUST 允许 `mornlea-server` 经共享 physics/core 依赖固定 Rust engine 与 CGO，但 MUST 保持无客户端、无 WebGPU、无窗口、无 `gfx`、无 `render`。

#### Scenario: Linux amd64 原生 bundle
- **GIVEN** Ubuntu amd64 clean checkout
- **WHEN** 原生构建并移开 Cargo target
- **THEN** MUST 生成同目录 `mornlea-server` 与 `libmornlea_engine.so`
- **AND** server MUST 通过 `$ORIGIN` 加载相邻 `.so` 并进入 Go 参数解析
- **AND** 依赖闭包 MUST 不含 client、mesh、render、gfx、WebGPU、GLFW、字体或窗口包
```

In `specs/project-identity/spec.md`, use `## MODIFIED Requirements` and reproduce the full current `当前项目身份统一为 Mornlea` Requirement with these updated scenarios:

Keep its system sentence naming product `Mornlea`, module `github.com/channing771/mornlea`, and commands `mornlea`/`mornlea-server` unchanged, then replace only its scenarios with:

```markdown
#### Scenario: clean checkout 构建当前入口
- **WHEN** 在 Apple Silicon/macOS 执行 canonical build
- **THEN** MUST 生成 `bin/mornlea`、`bin/mornlea-server` 与同目录 `libmornlea_engine.dylib`

#### Scenario: Linux 专服发布为同目录 bundle
- **WHEN** 在 Linux amd64 原生执行 canonical server build
- **THEN** MUST 生成同目录 `mornlea-server` 与 `libmornlea_engine.so`
- **AND** 两者 MUST 作为一个不可混装的发布单元升级

#### Scenario: 旧入口不再发布
- **WHEN** 枚举当前 module、命令、native ABI、构建和 Hook 身份
- **THEN** MUST 不存在 `mcgo`/`mcgod` wrapper、旧 `mcgo` C symbol、`libmornlea_mesh.dylib`、`libmornlea_mesh.so` 或旧环境变量 fallback
- **AND** additive ABI v1 的 `mornlea_mesh_section` MUST 继续保留
```

Do not edit `openspec/specs/*` directly; archive owns the main-spec merge.

- [ ] **Step 4: Write the new collision/raycast capability delta**

`specs/rust-engine-collision-raycast/spec.md` uses `## ADDED Requirements` and includes these exact observable contracts:

```markdown
### Requirement: Rust collision 保持共享物理结果
系统 MUST 让 Rust 唯一生产 kernel 对同一 state、displacement 与 collision snapshot 产生与冻结 Go oracle 逐位一致的位置、clipped mask、OnGround、UsedStep 与 HitUnknown；解析顺序 MUST 为 Y/X/Z，unknown MUST 作为闭合边界，step 只在水平进度严格更大时选中。

#### Scenario: 被拒绝的 step 不污染最终 unknown
- **GIVEN** ordinary path 全部已知且备选 step path 遇到 unknown 后被拒绝
- **WHEN** 解析同一 movement
- **THEN** MUST 返回 ordinary path
- **AND** 最终 HitUnknown MUST 为 false

#### Scenario: 原始 box count 保持 clamp 语义
- **GIVEN** CollisionBoxSet.Count 为 8、9 或 255
- **WHEN** Go 编码 snapshot
- **THEN** MUST 只编码前八个 AABB
- **AND** 9 或 255 MUST NOT 因 count 本身被拒绝

### Requirement: collision snapshot 具有硬资源上限
系统 MUST 在 source 查询、分配、native 调用和状态发布前以 checked arithmetic 计算完整 prism，并 MUST 原子拒绝超过 4096 cell、整数溢出或不可表示的输入。

#### Scenario: 上限内完整编码
- **WHEN** prism 恰含 4096 cell
- **THEN** 系统 MUST 完整编码且不得截断

#### Scenario: 超过上限在查询前 panic
- **WHEN** prism 需要 4097 cell
- **THEN** 系统 MUST 在零次 CollisionSource 查询后稳定 panic

### Requirement: Rust raycast 保持惰性 callback 契约
系统 MUST 让 Rust 以最多 64 record 的 caller-owned cursor batch 执行 DDA，同时由 Go 按现有顺序调用 callback、传播第一个 error 并计算最终 Point。

#### Scenario: batch 预取不改变惰性
- **GIVEN** Rust 已生成包含多个候选格的一个 batch
- **WHEN** 第一条 callback 返回 sentinel error 或首个 solid
- **THEN** Go MUST 立即返回且 MUST NOT 调用后续 record
- **AND** error identity MUST 原样保留

#### Scenario: 多 batch 保持遍历语义
- **GIVEN** 合法射线跨越超过 64 个候选格
- **WHEN** 使用 caller-owned cursor 继续下一批
- **THEN** origin cell、负坐标 floor、XYZ tie、精确 endpoint 与 int32 wrapping MUST 与冻结 Go oracle 一致

### Requirement: additive native ABI 原子且无跨调用所有权
系统 MUST 保持 ABI version 1、旧 mesh symbol/layout/status 不变，并由 Go 独占所有 input、scratch、cursor 与 output；Rust MUST 不保存地址、不回调 Go、不启动后台线程，且任一非法输入或 panic MUST 不发布部分 collision/raycast 结果。

#### Scenario: 两个平台逐位一致
- **WHEN** macOS arm64 与 Linux amd64 对固定、随机和 fuzz corpus 调用 collision/raycast
- **THEN** 两个平台 MUST 与 test-only Go oracle 逐位一致
- **AND** 常规 collision/raycast bridge MUST 保持零 Go heap allocation

#### Scenario: 非法 ABI 输入不发布部分结果
- **GIVEN** collision/raycast 调用的 version、指针范围、长度、overlap、magic、layout、reserved byte、cursor state 或内容非法
- **WHEN** native bridge 拒绝该调用
- **THEN** 在两个 result metadata 指针本身有效时，raycast 的 `output_count`/`done` MUST 清零
- **AND** collision output 与 raycast output/cursor MUST 保持调用前字节
- **AND** Go State MUST 不发布部分结果

#### Scenario: Rust panic 不跨越 C ABI
- **GIVEN** test-only fault injection 使 collision/raycast kernel panic
- **WHEN** 调用对应 C ABI
- **THEN** unwind MUST 不跨越 C ABI
- **AND** 在两个 result metadata 指针本身有效时，raycast 的 `output_count`/`done` MUST 清零
- **AND** collision output 与 raycast output/cursor、Go State MUST 保持未发布
```

- [ ] **Step 5: Write implementation design and ordered tasks**

`design.md` copies the approved ownership, exact layouts, failure atomicity, platform bundle, rejected alternatives and rollback decisions without adding later-stage world ownership. `tasks.md` contains unchecked groups corresponding exactly to Tasks 2–9, Task 10 cumulative local gates/review, and one final real Linux PR-CI item consumed by Task 11. Archive itself is an outer plan action and MUST NOT become a self-referential active checkbox.

- [ ] **Step 6: Validate and commit the active contract**

```bash
openspec validate rust-engine-collision-raycast-ci-stability --strict --no-interactive
openspec validate --all --strict --no-interactive
git diff --check
git add -- openspec/changes/rust-engine-collision-raycast-ci-stability
git diff --cached --check
git commit -m "docs: 规划 Rust 碰撞射线与 CI 稳定化"
```

Expected: strict validation PASS; commit contains only the seven active-change artifact groups.

---

### Task 2: 修复同身份 TCP 重连 fixture

**Files:**
- Modify: `internal/server/daylight_multiplayer_test.go:115-116`
- Modify: `openspec/changes/rust-engine-collision-raycast-ci-stability/tasks.md`
- Consume only: `internal/server/host_test_helpers_test.go:177-197`
- Report only, ignored: `.superpowers/sdd/2026-08-14-rust-engine-collision-raycast-ci-stability/task-2-report.md`

**Interfaces:**
- Consumes: existing `func waitForPlayerReleased(*testing.T, *Host, core.PlayerID)` checking both active indexes.
- Produces: reconnect test with an explicit Host release happens-before edge; no production change.

- [ ] **Step 1: Preserve the real CI RED as the failure proof**

Record run `31813426121`, duration `0.65s`, and exact `LoginAlreadyOnline/玩家已在线` assertion in the ignored report. Do not add a sleep, login retry, production hook or deadline change merely to manufacture local RED.

- [ ] **Step 2: Add the minimal release barrier**

Change only the reconnect sequence:

```go
mustCloseMultiplayerTCPClient(t, first)
waitForPlayerReleased(t, host.Host, identity.PlayerID)
second = mustConnectMultiplayerTCPClient(t, deadline, host.Addr, identity)
```

Do not change `mustCloseMultiplayerTCPClient`; closing a receiver must not be redefined as waiting for Host registry cleanup.

- [ ] **Step 3: Run repeated GREEN and mutation RED**

```bash
make rust
go test ./internal/server \
  -run '^TestReconnectContinuesWorldTimeWithoutRollback$' \
  -race -count=100
```

Then temporarily remove only `waitForPlayerReleased`, run the same command with `-count=100`, and record any recurrence. If scheduling does not reproduce locally, record that honestly and retain the real CI RED; restore the barrier before continuing. The original world-time nonrollback assertions must remain unchanged.

- [ ] **Step 4: Verify scope and commit**

```bash
go test ./internal/server -run '^TestReconnectContinuesWorldTimeWithoutRollback$' -race -count=1
git diff --check
git add -- internal/server/daylight_multiplayer_test.go \
  openspec/changes/rust-engine-collision-raycast-ci-stability/tasks.md
git diff --cached --check
git commit -m "test: 等待重连身份完成释放"
```

---

### Task 3: 修复健康 Memory endpoint 的 reader 顺序

**Files:**
- Modify: `internal/server/host_lifecycle_test.go:380-397`
- Modify: `openspec/changes/rust-engine-collision-raycast-ci-stability/tasks.md`
- Report only, ignored: `.superpowers/sdd/2026-08-14-rust-engine-collision-raycast-ci-stability/task-3-report.md`

**Interfaces:**
- Consumes: existing `startMemoryLogin`, `monitorEndpointProgress`, `waitReady`; production KeepAlive reply remains driven by `Recv`.
- Produces: healthy endpoint reader and cleanup registered before Ready wait.

- [ ] **Step 1: Preserve the real CI RED as the failure proof**

Record run `31813364557`, duration `30.00s`, and exact `player did not become ready` assertion. The tested `HeartbeatInterval=20ms`, `HeartbeatTimeout=150ms` and shared `waitDeadline` are not liveness margins to relax and must remain byte-unchanged.

- [ ] **Step 2: Reorder only the helper setup**

Replace the loop body with:

```go
login := startMemoryLogin(t, host, playerIdentity(byte(index+1)))
current := monitorEndpointProgress(
	login,
	sequenceBase+uint64(index),
	movements[index],
)
t.Cleanup(current.cancel)
waitReady(t, host, login)
progress = append(progress, current)
```

Cleanup registration precedes `waitReady`, so a fatal Ready failure cannot leak the reader goroutine. Do not modify production heartbeat/network code, add retries or change workflow timeout.

- [ ] **Step 3: Run focused repeated GREEN and mutation RED**

```bash
make rust
go test ./internal/server \
  -run '^TestHostHeartbeatTimeoutCleanupIsIsolated$' \
  -race -count=100
```

Temporarily restore reader-after-Ready and run the same focused `-race -count=100` command exactly once; report whether the original failure recurs without claiming a non-recurrence disproves the cited CI RED. Restore reader-before-Ready.

- [ ] **Step 4: Run the combined CI-fixture gate and commit**

```bash
go test ./internal/server \
  -run 'Test(ReconnectContinuesWorldTimeWithoutRollback|HostHeartbeatTimeoutCleanupIsIsolated)' \
  -race -count=100
go test ./internal/server -race -count=1
git diff --check
git add -- internal/server/host_lifecycle_test.go \
  openspec/changes/rust-engine-collision-raycast-ci-stability/tasks.md
git diff --cached --check
git commit -m "test: 提前启动健康会话读取器"
```

Expected: all PASS; no `.github/workflows/ci.yml` change in Tasks 2–3.

---

### Task 4: 将唯一 Rust crate/library 纯改名为 `mornlea_engine`

**Files:**
- Move: `engine/crates/mornlea_mesh/**` → `engine/crates/mornlea_engine/**`
- Modify: `engine/Cargo.toml`
- Modify: `engine/Cargo.lock`
- Modify: `engine/crates/mornlea_engine/Cargo.toml`
- Modify: `engine/crates/mornlea_engine/build.rs`
- Modify: `internal/mesh/native_abi.go`
- Modify: `internal/archcheck/identity_test.go`
- Modify: `scripts/agent-hooks/guard.test.mjs`
- Modify: `Makefile`
- Modify: `AGENTS.md`
- Modify: `CLAUDE.md`
- Modify: `README.md`
- Modify: `README.en.md`
- Modify: `openspec/config.yaml`
- Modify: `docs/notes/progress.md`
- Modify: `openspec/changes/rust-engine-collision-raycast-ci-stability/tasks.md`
- Test: existing `internal/mesh/native_abi_test.go`, `internal/mesh/native_input_test.go`, `internal/mesh/native_parity_test.go`
- Report only, ignored: `.superpowers/sdd/2026-08-14-rust-engine-collision-raycast-ci-stability/task-4-report.md`

**Interfaces:**
- Preserves exactly: ABI version `1`, status `0..9`, `mornlea_engine_abi_version`, `mornlea_mesh_section`, mesh input/output layout and all mesh algorithms.
- Changes only: Cargo package/workspace member and native artifact basename from `mornlea_mesh` to `mornlea_engine`.

- [ ] **Step 1: Add a permanent identity RED before moving files**

Add `TestNativeEngineLibraryIdentity` in `internal/archcheck/identity_test.go` so it requires:

```text
engine/crates/mornlea_engine/Cargo.toml exists
engine/crates/mornlea_mesh does not exist
workspace member == crates/mornlea_engine
package name == mornlea_engine
build install name == @rpath/libmornlea_engine.dylib
Make/cgo/current docs contain libmornlea_engine, not libmornlea_mesh
```

The guard must not reject `mornlea_mesh_section`: that is the preserved ABI symbol, not the old library identity. Run and capture RED:

```bash
make rust
go test ./internal/archcheck -run '^TestNativeEngineLibraryIdentity$' -count=1
```

Expected: FAIL because the current crate/artifact is still `mornlea_mesh`.

- [ ] **Step 2: Perform the mechanical rename without touching algorithms**

Use `git mv` for the crate directory. Update only:

```toml
# engine/Cargo.toml
members = ["crates/mornlea_engine"]

# engine/crates/mornlea_engine/Cargo.toml
[package]
name = "mornlea_engine"
```

Regenerate the lockfile rather than editing it by hand:

```bash
(cd engine && cargo generate-lockfile)
```

Change the install name to `@rpath/libmornlea_engine.dylib`, the existing cgo linker basename to `mornlea_engine`, and the Make input/output artifact names. Do not change `engine/include/mornlea_engine.h`, `ffi.rs`, `greedy.rs`, `input.rs`, `light.rs`, `quad.rs`, the `crate-type = ["rlib", "cdylib"]` list, or add a dependency.

- [ ] **Step 3: Update only current identity documentation and Hook fixtures**

Update current facts in `AGENTS.md`, `CLAUDE.md`, both READMEs, `openspec/config.yaml`, and append the rename fact to `docs/notes/progress.md`. Keep `AGENTS.md` and `CLAUDE.md` byte-identical. Update hard-coded Hook test fixture paths from `engine/crates/mornlea_mesh` to `engine/crates/mornlea_engine`.

Do not rewrite historical `docs/notes/mornlea-migration.md`, `docs/superpowers/**`, `openspec/changes/archive/**`, or main specs in `openspec/specs/**`.

- [ ] **Step 4: Prove the renamed crate is still one std-only library**

```bash
make rust
(cd engine && rustup show active-toolchain | rg '^1\.97\.1')
(cd engine && cargo fmt --all -- --check)
(cd engine && cargo clippy --workspace --all-targets --locked -- -D warnings)
(cd engine && cargo test --workspace --locked)
(cd engine && cargo metadata --no-deps --format-version 1) |
  jq -e '.packages | length == 1 and .[0].name == "mornlea_engine"'
test -z "$(cd engine && cargo tree --workspace --locked --edges normal --prefix none | sed '1d')"
go test ./internal/mesh ./internal/client -race -count=1
go test ./internal/mesh \
  -run 'Test(NativeABIVersionMatchesGo|NativeInputStatusNumbersMatchABI|NativeOracleParity)' \
  -race -count=1
go test ./internal/mesh -run '^$' -bench '^BenchmarkMeshTerrainSection$' -benchmem -count=5
node --test scripts/agent-hooks/guard.test.mjs
go test ./internal/archcheck -count=1
cmp AGENTS.md CLAUDE.md
```

The Cargo-tree assertion deliberately permits only the root package line; any normal dependency makes it non-empty.

- [ ] **Step 5: Verify the detached macOS artifact and visual identity**

```bash
make build
otool -D bin/libmornlea_engine.dylib | rg -Fx '@rpath/libmornlea_engine.dylib'
otool -L bin/mornlea | rg -F '@rpath/libmornlea_engine.dylib'
otool -l bin/mornlea > /private/tmp/mornlea-engine-rename-otool.txt
rg -A2 LC_RPATH /private/tmp/mornlea-engine-rename-otool.txt | rg -F '@loader_path'
! rg '/engine/target/.*/deps' /private/tmp/mornlea-engine-rename-otool.txt
native_target_backup="$(mktemp -d)/target"
trap 'test -e engine/target || mv "$native_target_backup" engine/target' EXIT
mv engine/target "$native_target_backup"
set +e
bin/mornlea -h >/private/tmp/mornlea-engine-rename-help.txt 2>&1
native_help_rc=$?
set -e
mv "$native_target_backup" engine/target
trap - EXIT
test "$native_help_rc" -eq 1
rg -F 'flag: help requested' /private/tmp/mornlea-engine-rename-help.txt
! rg 'dyld|Library not loaded' /private/tmp/mornlea-engine-rename-help.txt
VISUAL_OUT="$(mktemp -d)" make visual-check
```

Expected: all 11 headless scenes pass existing thresholds; no golden update. The target restore must run before interpreting the detached smoke result; if any command fails, restore it manually before stopping.

- [ ] **Step 6: Scope-check and commit the pure rename**

```bash
git diff --check
git diff --name-only --diff-filter=ACMR
git diff --name-only --diff-filter=D
openspec validate rust-engine-collision-raycast-ci-stability --strict --no-interactive
git add -A -- engine internal/mesh/native_abi.go internal/archcheck/identity_test.go \
  scripts/agent-hooks/guard.test.mjs Makefile AGENTS.md CLAUDE.md README.md README.en.md \
  openspec/config.yaml docs/notes/progress.md \
  openspec/changes/rust-engine-collision-raycast-ci-stability/tasks.md
git diff --cached --check
git commit -m "refactor: 将 Rust 动态库收敛为 mornlea_engine"
```

---

### Task 5: 建立唯一 `internal/nativeabi` engine bridge

**Files:**
- Create: `internal/nativeabi/native.go`
- Create: `internal/nativeabi/native_test.go`
- Modify: `internal/mesh/native_abi.go`
- Modify: `internal/mesh/native_abi_test.go`
- Modify: `internal/server/deadline_external_test.go`
- Modify: `internal/archcheck/dependency_test.go`
- Modify: `internal/archcheck/platform_test.go`
- Modify: `scripts/agent-hooks/guard.mjs`
- Modify: `scripts/agent-hooks/guard.test.mjs`
- Modify: `AGENTS.md`
- Modify: `CLAUDE.md`
- Modify: `README.md`
- Modify: `README.en.md`
- Modify: `openspec/config.yaml`
- Modify: `openspec/changes/rust-engine-collision-raycast-ci-stability/tasks.md`
- Report only, ignored: `.superpowers/sdd/2026-08-14-rust-engine-collision-raycast-ci-stability/task-5-report.md`

**Interfaces:**
- New package API:

```go
type Status uint32

const (
	ABIVersion                           = uint32(C.MORNLEA_ENGINE_ABI_VERSION)
	StatusOK              Status = Status(C.MORNLEA_STATUS_OK)
	StatusABIVersion      Status = Status(C.MORNLEA_STATUS_ABI_VERSION)
	StatusInvalidArgument Status = Status(C.MORNLEA_STATUS_INVALID_ARGUMENT)
	StatusInput           Status = Status(C.MORNLEA_STATUS_INPUT)
	StatusScratch         Status = Status(C.MORNLEA_STATUS_SCRATCH)
	StatusRegistry        Status = Status(C.MORNLEA_STATUS_REGISTRY)
	StatusEmission        Status = Status(C.MORNLEA_STATUS_EMISSION)
	StatusOutputOverflow  Status = Status(C.MORNLEA_STATUS_OUTPUT_OVERFLOW)
	StatusQueueOverflow   Status = Status(C.MORNLEA_STATUS_QUEUE_OVERFLOW)
	StatusPanic           Status = Status(C.MORNLEA_STATUS_PANIC)
)

func EngineABIVersion() uint32
func MeshSection(version uint32, input []byte, scratch, output []uint64) (Status, int)
```

- `internal/mesh` retains its private native names as thin aliases/wrappers, so existing mesh callers and error text do not move.

- [ ] **Step 1: Write architecture and bridge RED tests**

Add tests requiring:

1. `internal/nativeabi` is a dependency leaf.
2. `mesh -> core, world, nativeabi`; do not authorize `core` or `physics` yet.
3. only `internal/nativeabi` may include `mornlea_engine.h`, link `mornlea_engine`, or reference `C.mornlea_*`.
4. `internal/gfx` remains allowed to use unrelated Cocoa/Metal cgo.
5. Any Rust/native bridge edit runs the fixed native downstream union even when changed Go files are also present; cover both Rust-only and mixed native-Go changes.
6. every current `internal/archcheck/deps_test.go` reference—including the server external-deadline comment—is corrected to the real `internal/archcheck/dependency_test.go`.

Run RED:

```bash
make rust
go test ./internal/archcheck -run 'Test(InternalDependenciesAreOneWay|NativeEngineBridgeBoundary)' -count=1
node --test scripts/agent-hooks/guard.test.mjs
```

Expected: FAIL because `nativeabi` does not exist and mesh still owns the engine cgo preamble.

- [ ] **Step 2: Move existing mesh cgo into the leaf package**

Create `internal/nativeabi/native.go` with build constraint:

```go
//go:build cgo && (darwin || linux)
```

Its cgo preamble is the only one that includes `mornlea_engine.h` and links the library. Use existing development rpath `engine/target/release`, with platform-specific `-lmornlea_engine`; annotate both existing engine functions with verified Go 1.26 directives:

```c
#cgo noescape mornlea_engine_abi_version
#cgo nocallback mornlea_engine_abi_version
#cgo noescape mornlea_mesh_section
#cgo nocallback mornlea_mesh_section
```

Move the exact current slice-pointer/length conversion and status call into `EngineABIVersion` and `MeshSection`. Do not introduce `error`, an interface, a pool, a backend selector, or a second wrapper layer.

- [ ] **Step 3: Reduce mesh to a domain wrapper**

`internal/mesh/native_abi.go` must stop importing `C` and `unsafe`. Change both it and `internal/mesh/native_abi_test.go` from the current Darwin-only constraint to `//go:build cgo && (darwin || linux)` so the same compatibility wrapper/tests compile in the Ubuntu job. Preserve its existing private API with aliases:

```go
type nativeStatus = nativeabi.Status

const (
	nativeABIVersionCurrent      = nativeabi.ABIVersion
	nativeStatusOK               = nativeabi.StatusOK
	nativeStatusABIVersion       = nativeabi.StatusABIVersion
	nativeStatusInvalidArgument  = nativeabi.StatusInvalidArgument
	nativeStatusInput            = nativeabi.StatusInput
	nativeStatusScratch          = nativeabi.StatusScratch
	nativeStatusRegistry         = nativeabi.StatusRegistry
	nativeStatusEmission         = nativeabi.StatusEmission
	nativeStatusOutputOverflow   = nativeabi.StatusOutputOverflow
	nativeStatusQueueOverflow    = nativeabi.StatusQueueOverflow
	nativeStatusPanic            = nativeabi.StatusPanic
)

func nativeABIVersion() uint32 {
	return nativeabi.EngineABIVersion()
}

func nativeMeshSection(input []byte, scratch, output []uint64) (nativeStatus, int) {
	return nativeMeshSectionVersion(nativeABIVersionCurrent, input, scratch, output)
}

func nativeMeshSectionVersion(version uint32, input []byte, scratch, output []uint64) (nativeStatus, int) {
	return nativeabi.MeshSection(version, input, scratch, output)
}
```

Preserve this complete private surface so existing callers compile without churn. Keep `nativeStatusPanicText` and mesh-specific error formatting in `internal/mesh`; do not move domain language into `nativeabi`.

Change Hook control flow so `needsRustValidation` always unions the fixed native packages with any changed-Go packages instead of using the current mutually exclusive `if goFiles ... else if needsRustValidation` branches. At this task the fixed union is `internal/nativeabi`, `internal/mesh`, and `internal/client`; later tasks expand it only when they introduce new native consumers. Test both a Rust-only path and a mixed `internal/nativeabi` Go + Rust path.

Update current ownership text in `AGENTS.md`, `CLAUDE.md`, both READMEs, and `openspec/config.yaml`: only `internal/nativeabi` directly owns the engine C header/symbol bridge, while `mesh` is a domain caller. Keep `AGENTS.md` and `CLAUDE.md` byte-identical; do not edit historical milestone documents.

- [ ] **Step 4: Prove ABI, atomicity and zero-allocation forwarding**

`internal/nativeabi/native_test.go` must directly cover ABI version/status values, nil and undersized slices, canaries around caller-owned buffers, overlap rejection and failure atomicity using the existing mesh entry point. Extend the existing mesh test with preallocated valid input/scratch/output:

```go
allocs := testing.AllocsPerRun(1000, func() {
	status, count := nativeMeshSectionVersion(nativeABIVersionCurrent, input, scratch, output)
	if status != nativeStatusOK || count == 0 {
		panic("native mesh 调用失败")
	}
})
if allocs != 0 {
	t.Fatalf("native mesh bridge allocations = %v, want 0", allocs)
}
```

Also audit the source for each exact `#cgo noescape`/`#cgo nocallback` declaration, then use Go compiler diagnostics plus the runnable allocation test; do not attempt to “mutation test” compiler directives as if they were runtime branches.

- [ ] **Step 5: Run focused and structural GREEN**

```bash
make rust
go test ./internal/nativeabi ./internal/mesh ./internal/client -race -count=1
go test ./internal/server -run '^$' -race -count=1
go test ./internal/mesh -run '^TestNativeMeshBridgeDoesNotAllocate$' -count=1
go test -gcflags='-m=2' ./internal/nativeabi ./internal/mesh 2>&1 |
  tee /private/tmp/mornlea-nativeabi-escape.txt
go test ./internal/archcheck -count=1
node --test scripts/agent-hooks/guard.test.mjs
go vet ./internal/nativeabi ./internal/mesh
test -z "$(gofmt -l internal/nativeabi internal/mesh)"
cmp AGENTS.md CLAUDE.md
git diff --check
```

Inspect `/private/tmp/mornlea-nativeabi-escape.txt` for the bridge parameters; the allocation test is the behavioral gate. Do not fail merely because unrelated test scaffolding escapes.

- [ ] **Step 6: Commit the unique bridge**

```bash
git add -- internal/nativeabi internal/mesh/native_abi.go internal/mesh/native_abi_test.go \
  internal/server/deadline_external_test.go \
  internal/archcheck/dependency_test.go internal/archcheck/platform_test.go \
  scripts/agent-hooks/guard.mjs scripts/agent-hooks/guard.test.mjs \
  AGENTS.md CLAUDE.md README.md README.en.md openspec/config.yaml \
  openspec/changes/rust-engine-collision-raycast-ci-stability/tasks.md
git diff --cached --check
openspec validate rust-engine-collision-raycast-ci-stability --strict --no-interactive
git commit -m "refactor: 统一 native engine ABI 入口"
```

---

### Task 6: 实现 collision ABI 与 test-only Go oracle parity

**Files:**
- Modify: `engine/include/mornlea_engine.h`
- Modify: `engine/crates/mornlea_engine/src/lib.rs`
- Modify: `engine/crates/mornlea_engine/src/ffi.rs`
- Create: `engine/crates/mornlea_engine/src/collision.rs`
- Modify: `internal/nativeabi/native.go`
- Modify: `internal/nativeabi/native_test.go`
- Create: `internal/physics/collision_native_test.go`
- Create: `internal/physics/collision_oracle_test.go`
- Modify: `internal/physics/collision_test.go`
- Modify: `internal/physics/step_test.go`
- Modify: `internal/physics/physics_fuzz_test.go`
- Modify: `openspec/changes/rust-engine-collision-raycast-ci-stability/tasks.md`
- Consume only: `internal/physics/collision.go`, `internal/physics/step.go`, `internal/physics/motion.go`, `internal/physics/types.go`
- Report only, ignored: `.superpowers/sdd/2026-08-14-rust-engine-collision-raycast-ci-stability/task-6-report.md`

**Interfaces:**

```c
uint32_t mornlea_collision_resolve(
    uint32_t abi_version,
    const uint8_t *input,
    size_t input_len,
    uint8_t *output,
    size_t output_len);
```

```go
func CollisionResolve(input, output []byte)
```

This task adds and proves the native function but keeps production `physics.Step` on the existing Go kernel. That avoids switching `mornlea-server` to CGO before the Linux bundle and CI job exist. The temporary encoder/oracle live only in `_test.go`; Task 7 moves the proven encoder into production and deletes the old production kernel in the same commit.

- [ ] **Step 1: Freeze the collision v1 byte layout in failing tests**

Add `TestCollisionInputLayoutV1` and Rust `collision_layout_v1_is_stable` with these literal offsets; use little-endian bytes, never a C struct:

```text
header = 64 bytes
  0..3   magic "MGC1"
  4..7   u32 layout_version = 1
  8..19  f32 position[x,y,z]
 20..31  f32 displacement[x,y,z]
 32      u8 began_grounded
 33..35  zero
 36..39  f32 step_height
 40..51  i32 prism_origin[x,y,z]
 52..63  u32 prism_dims[x,y,z]

cell = 196 bytes
  0      u8 loaded
  1      u8 box_count
  2..3   zero
  4..195 8 * {f32 min_x,min_y,min_z,max_x,max_y,max_z}

output = 16 bytes
  0..11  f32 position[x,y,z]
 12      u8 clipped_mask: X=1, Y=2, Z=4
 13      u8 on_ground
 14      u8 used_step
 15      u8 hit_unknown
```

The exact maximum encoded input is `64 + 4096*196 = 802880` bytes; there is no second redundant byte-limit contract.

Cells are encoded Y/X/Z with flat index `((y*dimX)+x)*dimZ+z`. Boolean bytes accept only `0` or `1`; all reserved bytes must be zero. Add `TestCollisionInputLayoutV1` and `TestCollisionRawFailureAtomicity`, then run RED:

```bash
make rust
go test ./internal/nativeabi ./internal/physics \
  -run '^TestCollision(InputLayoutV1|RawFailureAtomicity)$' -race -count=1
```

Expected: FAIL because neither the native entry point nor test encoder exists.

- [ ] **Step 2: Add the raw FFI entry point with failure atomicity**

Append the declaration to the existing header without moving or changing old declarations. In `ffi.rs`, validate ABI version, nil/length/overlap, exact input size `64 + cells*196`, exact 16-byte output, magic/layout/reserved bytes, dimensions/product, bools, required finite floats, and only the six finite components of each encoded/in-count AABB before traversal. Do not invent `min <= max` or `[0,1]` box restrictions absent from the current Go contract. Reuse statuses `0..9`: ABI mismatch is 1, invalid pointer/range/alignment/overlap is 2, malformed input/content is 3, output shorter than 16 bytes is 7, and panic remains 9; an oversized output is invalid argument rather than silently changing the layout.

Before validation completes, do not mutate caller output. Decode borrowed slices, run the kernel into a local 16-byte result, then copy once on success. Wrap the exported function in the existing `catch_unwind` pattern so Rust unwind never crosses C.

Add to `internal/nativeabi/native.go`:

```c
#cgo noescape mornlea_collision_resolve
#cgo nocallback mornlea_collision_resolve
```

```go
func CollisionResolve(input, output []byte) {
	status := collisionResolveVersion(ABIVersion, input, output)
	if status != StatusOK {
		panic(collisionStatusPanicText(status))
	}
}
```

Keep `collisionResolveVersion` and stable panic-text mapping unexported; `internal/nativeabi` tests use the raw function for version/status/canary checks. Do not add an `error` return or expose a domain type from this leaf package.

- [ ] **Step 3: Freeze the Go oracle and write semantic RED tests before the kernel**

Mechanically copy the current Go `collision.go` and `step.go` algorithms into `collision_oracle_test.go`; rename every copied private declaration (`moveResult`, vars, functions and helper constants) with an `oracle` prefix so it cannot collide with production, and make no algorithmic cleanup. In `collision_native_test.go`, build the prism using:

```text
x=[min(px,px+dx)-PlayerWidth/2-Epsilon,
   max(px,px+dx)+PlayerWidth/2+Epsilon]
z=same as x
y=[py+min(0,dy,stepHeight)-GroundProbe-Epsilon,
   py+max(0,dy,stepHeight)+PlayerHeight+Epsilon]
```

Convert each endpoint with checked `floor` to an inclusive block range. Before querying `CollisionSource`, use checked dimension/product/byte arithmetic and reject unrepresentable coordinates or more than 4096 cells. Query each cell exactly once in Y/X/Z order. Encode `box_count = min(int(set.Count), 8)` so raw counts 8, 9 and 255 all encode at most the first eight boxes.

Stable configured walk/terminal displacement plus step height must fit a 135-cell stack input buffer of `64 + 135*196 = 26524` bytes. Tests beyond 135 and through 4096 use one exact-size allocation; this task does not introduce a pool or general buffer abstraction.

Implement these exact tests before writing `collision.rs`:

```text
TestCollisionSnapshotUsesYXZOrderAndQueriesEachCellOnce
TestCollisionSnapshotClampsBoxCount
TestCollisionSnapshotAllows4096Cells
TestCollisionSnapshotRejects4097BeforeQuery
TestCollisionConfiguredMaximumFitsRegularBuffer
TestNativeCollisionMatchesGoOracle
TestNativeCollisionRejectedUnknownStepKeepsOrdinaryHitUnknownFalse
TestNativeCollisionMatchesGoOracleDeterministicCorpus
TestNativeCollisionConcurrentCalls
TestNativeCollisionBridgeDoesNotAllocate
FuzzNativeCollisionMatchesGoOracle
```

`TestCollisionConfiguredMaximumFitsRegularBuffer` uses the conservative supported extrema (`dx=1`, `dz=1`, `dy=-10`, `stepHeight=1.5`) and asserts both `cells <= 135` and `encodedBytes <= 26524`. The deterministic corpus includes floor, wall, corner, negative coordinates, unknown, walking off an edge, valid step, blocked headroom, rejected landing, equal horizontal progress, Nextafter32 boundaries, and raw Count 8/9/255. Compare float bits and all four output flags, not an epsilon.

Run focused RED:

```bash
go test ./internal/physics \
  -run 'Test(CollisionSnapshot|CollisionConfiguredMaximum|NativeCollision)' \
  -race -count=1
```

Expected: the layout/encoder checks may pass, but native parity/semantics fail because the Rust kernel does not exist yet. Do not implement a temporary Go fallback or fake successful output.

- [ ] **Step 4: Implement the Rust collision kernel without allocation**

Port only current `resolveMove`, `resolveStepMove`, ground probes and final overlap behavior into `collision.rs`. Hard-code the already-frozen physics constants used by the Go oracle:

```text
PlayerWidth = 0.6
PlayerHeight = 1.8
Epsilon = 1e-5
GroundProbe = 1e-4
```

Ordinary resolution order is Y, then X, then Z. Unknown cells are closed. Compute the step candidate in a separate local result and select it only when its horizontal progress is strictly greater than ordinary. If a step candidate sees unknown/headroom/landing failure and is rejected, discard its `hit_unknown`; only the selected path publishes flags. Use borrowed cell/box views and fixed local values—no `Vec`, `Box`, `String`, thread or retained address.

Each encoded AABB is local to its cell; before overlap/clip arithmetic Rust translates it by the cell's absolute block coordinates derived from `prism_origin` and the Y/X/Z index. Unknown cells use the same absolute full-cube bounds as the Go oracle. Rust inline tests cover layout, malformed atomicity, Y/X/Z, closed unknown, rejected-step flag isolation and Nextafter bit parity.

- [ ] **Step 5: Turn the semantic suite GREEN**

Run the Step 3 focused command plus `TestNativeCollisionBridgeDoesNotAllocate`; all must pass before mutation testing. The direct bridge allocation test uses pre-encoded stack buffers, not the still-Go production `Step` path.

- [ ] **Step 6: Demonstrate four targeted mutation REDs**

Temporarily apply and restore each mutation separately:

1. Rust axis order Y/X/Z → X/Y/Z: corner/parity test fails.
2. unknown treated as air: the native unknown parity subtest fails.
3. step comparison `>` → `>=`: equal-horizontal-distance test fails.
4. merge rejected step `hit_unknown` into ordinary: rejected-step isolation test fails.

After each RED, restore production Rust and rerun its focused test GREEN. Never leave a mutation in the tree.

- [ ] **Step 7: Run collision ABI verification and commit**

```bash
make rust
(cd engine && cargo fmt --all -- --check)
(cd engine && cargo clippy --workspace --all-targets --locked -- -D warnings)
(cd engine && cargo test --workspace --locked)
go test ./internal/nativeabi -race -count=1
go test ./internal/physics \
  -run 'Test(CollisionSnapshot|CollisionConfiguredMaximum|NativeCollision)' \
  -race -count=1
go test ./internal/physics -run '^TestNativeCollisionBridgeDoesNotAllocate$' -count=1
go test ./internal/physics -run=^$ \
  -fuzz=FuzzNativeCollisionMatchesGoOracle -fuzztime=30s
go test ./internal/archcheck -count=1
go vet ./internal/nativeabi ./internal/physics
test -z "$(gofmt -l internal/nativeabi internal/physics)"
git diff --check
openspec validate rust-engine-collision-raycast-ci-stability --strict --no-interactive
git add -- engine/include/mornlea_engine.h engine/crates/mornlea_engine/src \
  internal/nativeabi internal/physics/collision_native_test.go \
  internal/physics/collision_oracle_test.go internal/physics/collision_test.go \
  internal/physics/step_test.go internal/physics/physics_fuzz_test.go \
  openspec/changes/rust-engine-collision-raycast-ci-stability/tasks.md
git diff --cached --check
git commit -m "feat: 实现 Rust 碰撞解析 ABI"
```

---

### Task 7: 将生产 collision 切到 Rust 并交付 Linux server bundle

**Files:**
- Modify: `internal/physics/collision.go`
- Modify: `internal/physics/motion.go`
- Delete: `internal/physics/step.go`
- Modify: `internal/physics/collision_native_test.go`
- Modify: `internal/physics/collision_oracle_test.go`
- Modify: `internal/physics/collision_test.go`
- Modify: `internal/physics/step_test.go`
- Modify: `internal/physics/physics_fuzz_test.go`
- Modify: `internal/physics/physics_bench_test.go`
- Modify: `internal/archcheck/dependency_test.go`
- Modify: `internal/archcheck/platform_test.go`
- Modify: `internal/archcheck/identity_test.go`
- Modify: `scripts/agent-hooks/guard.mjs`
- Modify: `scripts/agent-hooks/guard.test.mjs`
- Modify: `Makefile`
- Modify: `.github/workflows/ci.yml`
- Modify: `AGENTS.md`
- Modify: `CLAUDE.md`
- Modify: `README.md`
- Modify: `README.en.md`
- Modify: `openspec/config.yaml`
- Modify: `docs/notes/lan-server.md`
- Modify: `docs/notes/progress.md`
- Modify: `openspec/changes/rust-engine-collision-raycast-ci-stability/tasks.md`
- Consume only: `internal/client/predictor_advance.go`, `internal/client/predictor_reconcile.go`, `internal/sim/player.go`
- Report only, ignored: `.superpowers/sdd/2026-08-14-rust-engine-collision-raycast-ci-stability/task-7-report.md`

**Interfaces:**
- `func Step(state State, input Input, source CollisionSource) StepResult` remains byte-for-byte unchanged.
- Go retains validation, tunable snapshot, movement target, acceleration, gravity, velocity and final state publication.
- One native call returns position, clipped mask, OnGround, UsedStep and HitUnknown from one immutable collision snapshot.

- [ ] **Step 1: Add the unique-production and Linux-bundle RED guards**

Before switching production, add structural tests requiring:

```text
internal/physics imports internal/nativeabi
internal/physics/step.go does not exist
resolveMove, clipAxis and resolveStepMove exist only in *_test.go oracle files
cmd/mornlea-server dependency closure contains nativeabi but no client/mesh/render/gfx/window stack
Linux identity context is GOOS=linux, GOARCH=amd64, CgoEnabled=true
Make exposes build-linux-server and CI has an Ubuntu native bundle job
```

Run RED:

```bash
make rust
go test ./internal/archcheck \
  -run 'Test(InternalDependenciesAreOneWay|MornleaServerHasNoGraphicsDependencies|PhysicsUsesOnlyNativeCollision|MornleaCurrentIdentity)' \
  -count=1
node --test scripts/agent-hooks/guard.test.mjs
```

Expected: FAIL because production physics still uses the Go kernel and the Linux build context still disables CGO.

- [ ] **Step 2: Move the proven snapshot encoder into production**

Replace `internal/physics/collision.go` with the Task 6 checked prism/encoder, one `nativeabi.CollisionResolve` call and exact 16-byte decoder. Keep a fixed `[26524]byte` normal-path input and `[16]byte` output on the Go stack; only prisms of 136..4096 cells allocate an exact-size input slice. Do not add a pool, cache, interface or fallback.

The production result is:

```go
type moveResult struct {
	position   mgl32.Vec3
	clipped    [3]bool
	onGround   bool
	usedStep   bool
	hitUnknown bool
}

func resolveCollision(
	state State,
	displacement mgl32.Vec3,
	source CollisionSource,
	beganGrounded bool,
	stepHeight float32,
) moveResult
```

Clamp `CollisionBoxSet.Count` before indexing. All checked range/product/length failures panic before the first `CollisionBoxes` call. Decode only the exact success output and reject non-boolean flag bytes; do not publish a partially decoded result.

- [ ] **Step 3: Make `Step` call the native kernel exactly once**

In `motion.go`, preserve `beganGrounded := state.OnGround` before jump handling, then replace the old ordinary/step selection block with:

```go
move := resolveCollision(
	state,
	displacement,
	source,
	beganGrounded,
	tunables.StepHeight,
)
state.Position = move.position
state.OnGround = move.onGround
for axis, clipped := range move.clipped {
	if clipped {
		state.Velocity[axis] = 0
	}
}
return StepResult{
	State:      state,
	UsedStep:   move.usedStep,
	HitUnknown: move.hitUnknown,
}
```

Delete production `step.go`. The Task 6 oracle already contains the mechanically frozen old functions; do not retain renamed production copies or a build-tag fallback.

- [ ] **Step 4: Wire the real dependency and Hook scope**

In `internal/archcheck/dependency_test.go`, authorize only `physics -> core,nativeabi`; keep `nativeabi` a leaf. Update `platform_test.go` source guards so the engine header/symbol remains exclusive to `nativeabi` and collision production cannot contain the old Go resolver.

Expand the Task 5 fixed native Hook union to `nativeabi`, `physics`, `mesh`, `client`, `sim`, `server`, and `cmd/mornlea-server`. It must run for both Rust-only and mixed native-Go changes, not only the `goFiles.length === 0` branch. Keep the existing `internal/gfx` cgo exception and add no environment bypass.

- [ ] **Step 5: Change the canonical bundles without graphics leakage**

Update `Makefile`:

```make
RUST_DYLIB := $(RUST_DIR)/target/release/libmornlea_engine.dylib
RUST_SO := $(RUST_DIR)/target/release/libmornlea_engine.so
MORNLEA_DYLIB := bin/libmornlea_engine.dylib
MORNLEA_SO := bin/libmornlea_engine.so

.PHONY: build-linux-server

build:
	@mkdir -p $(dir $(BINARY))
	$(GO) build -ldflags='-extldflags=-Wl,-rpath,@loader_path' -o $(BINARY) $(APP)
	$(GO) build -ldflags='-extldflags=-Wl,-rpath,@loader_path' -o $(SERVER_BINARY) $(SERVER)
	cp $(RUST_DYLIB) $(MORNLEA_DYLIB)

build-linux-server: rust
	@mkdir -p $(dir $(SERVER_BINARY))
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 $(GO) build \
		-ldflags='-extldflags=-Wl,-rpath,$$ORIGIN' \
		-o $(SERVER_BINARY) $(SERVER)
	cp $(RUST_SO) $(MORNLEA_SO)
```

Add the exact `make help` entry `make build-linux-server 构建 Linux amd64 专服与同目录 Rust .so` so the new canonical target is discoverable.

`internal/nativeabi` supplies platform-specific development link paths. The Make target supplies release runpaths. Do not statically link, vendor the `.so`, or commit generated binaries.

- [ ] **Step 6: Replace the false cross-build with a native Ubuntu job**

Keep the existing macOS test job. Remove only its `CGO_ENABLED=0 GOOS=linux` server step, and add `linux-server` on `ubuntu-latest` with Go 1.26 and the pinned `engine/rust-toolchain.toml`. Its commands are:

```bash
make rust
go test ./internal/nativeabi ./internal/core ./internal/physics ./internal/mesh -race -count=1
make build-linux-server
go test ./internal/archcheck -count=1
test -z "$(CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go list -deps ./cmd/mornlea-server |
  rg 'internal/(client|mesh|render|gfx)|glfw|webgpu|x/image/font')"
readelf -d bin/mornlea-server | rg -F 'libmornlea_engine.so'
readelf -d bin/mornlea-server | rg '\$ORIGIN'
nm -D --defined-only bin/libmornlea_engine.so > /tmp/mornlea-engine-symbols.txt
for symbol in mornlea_engine_abi_version mornlea_mesh_section mornlea_collision_resolve; do
  awk '{print $NF}' /tmp/mornlea-engine-symbols.txt | rg -Fx "$symbol"
done
```

Then detach Cargo output and prove the adjacent `.so` is sufficient:

```bash
native_target_backup="$(mktemp -d)/target"
trap 'test -e engine/target || mv "$native_target_backup" engine/target' EXIT
mv engine/target "$native_target_backup"
ldd bin/mornlea-server > /tmp/mornlea-server-ldd.txt
rg -F "$(pwd)/bin/libmornlea_engine.so" /tmp/mornlea-server-ldd.txt
set +e
bin/mornlea-server -h >/tmp/mornlea-server-help.txt 2>&1
native_help_rc=$?
set -e
mv "$native_target_backup" engine/target
trap - EXIT
test "$native_help_rc" -eq 1
rg -F 'flag: help requested' /tmp/mornlea-server-help.txt
! rg 'error while loading shared libraries|cannot open shared object' /tmp/mornlea-server-help.txt
```

Do not add retry, Docker, a custom packaging script, or a second workflow for the same bundle.

- [ ] **Step 7: Update current ownership/build documentation only**

Update `AGENTS.md` and `CLAUDE.md` identically, both READMEs, `openspec/config.yaml`, `docs/notes/lan-server.md`, and append the completed collision/Linux bundle fact to `docs/notes/progress.md`. State that the dedicated server is still headless but now requires the adjacent Rust `.so`; release units are not cross-version mixable. Do not rewrite old milestone plans, archived changes or history notes.

- [ ] **Step 8: Prove production parity, allocations and downstream behavior**

The Task 6 parity suite must now call real `Step`/production encoder. Add an explicit 4097-cell source whose query counter remains zero and keep the 8/9/255 Count test. Run:

```bash
make rust
go test ./internal/physics -race -count=1
go test ./internal/physics -run '^TestStepPlayer.*DoesNotAllocate$' -count=1
go test ./internal/client ./internal/sim ./internal/server ./cmd/mornlea ./cmd/mornlea-server \
  -race -count=1
go test ./internal/archcheck -count=1
node --test scripts/agent-hooks/guard.test.mjs
go test ./internal/physics -run '^$' \
  -bench '^BenchmarkStepPlayer(Flat|Colliding|Stepping)$' -benchmem -count=5
```

Expected: existing public physics tests and all three zero-allocation checks PASS; downstream prediction and authoritative simulation share the same native implementation.

After the production switch, temporarily mutate Rust unknown handling to air and prove the public `TestUnknownBlockIsClosedBoundary` now fails through `Step`; restore Rust and rerun it GREEN. This is the production-path mutation evidence that Task 6 intentionally could not provide.

- [ ] **Step 9: Verify macOS release loading and commit**

```bash
make rust-check
make build
otool -L bin/mornlea-server | rg -F '@rpath/libmornlea_engine.dylib'
otool -l bin/mornlea-server | rg -A2 LC_RPATH | rg -F '@loader_path'
nm -gU bin/libmornlea_engine.dylib > /private/tmp/mornlea-engine-symbols.txt
for symbol in mornlea_engine_abi_version mornlea_mesh_section mornlea_collision_resolve; do
  awk '{print $NF}' /private/tmp/mornlea-engine-symbols.txt | rg -Fx "_$symbol"
done
! otool -L bin/mornlea bin/mornlea-server bin/libmornlea_engine.dylib | rg -F 'libmornlea_mesh'
native_target_backup="$(mktemp -d)/target"
trap 'test -e engine/target || mv "$native_target_backup" engine/target' EXIT
mv engine/target "$native_target_backup"
set +e
bin/mornlea-server -h >/private/tmp/mornlea-collision-server-help.txt 2>&1
native_server_rc=$?
set -e
mv "$native_target_backup" engine/target
trap - EXIT
test "$native_server_rc" -eq 1
rg -F 'flag: help requested' /private/tmp/mornlea-collision-server-help.txt
! rg 'dyld|Library not loaded' /private/tmp/mornlea-collision-server-help.txt
go vet ./internal/nativeabi ./internal/physics ./internal/client ./internal/sim ./internal/server \
  ./cmd/mornlea-server
test -z "$(gofmt -l internal/nativeabi internal/physics internal/archcheck)"
cmp AGENTS.md CLAUDE.md
git diff --check
openspec validate --all --strict --no-interactive
git add -A -- internal/physics internal/archcheck scripts/agent-hooks Makefile \
  .github/workflows/ci.yml AGENTS.md CLAUDE.md README.md README.en.md \
  openspec/config.yaml docs/notes/lan-server.md docs/notes/progress.md \
  openspec/changes/rust-engine-collision-raycast-ci-stability/tasks.md
git diff --cached --check
git commit -m "feat: 用 Rust 解析共享碰撞"
```

The Linux commands are required CI evidence on Ubuntu, not emulated on macOS. If the job exposes a platform-specific bug, fix it in this task and rerun both platform gates before continuing.

---

### Task 8: 将 `core.RaycastBlocks` 的 DDA 遍历切到 Rust

**Files:**
- Modify: `engine/include/mornlea_engine.h`
- Modify: `engine/crates/mornlea_engine/src/lib.rs`
- Modify: `engine/crates/mornlea_engine/src/ffi.rs`
- Create: `engine/crates/mornlea_engine/src/raycast.rs`
- Modify: `internal/nativeabi/native.go`
- Modify: `internal/nativeabi/native_test.go`
- Modify: `internal/core/raycast.go`
- Create: `internal/core/raycast_oracle_test.go`
- Create: `internal/core/raycast_native_test.go`
- Modify: `internal/core/raycast_test.go`
- Modify: `internal/core/raycast_fuzz_test.go`
- Modify: `internal/core/raycast_bench_test.go`
- Modify: `internal/archcheck/dependency_test.go`
- Modify: `internal/archcheck/platform_test.go`
- Modify: `scripts/agent-hooks/guard.mjs`
- Modify: `scripts/agent-hooks/guard.test.mjs`
- Modify: `.github/workflows/ci.yml`
- Modify: `AGENTS.md`
- Modify: `CLAUDE.md`
- Modify: `README.md`
- Modify: `README.en.md`
- Modify: `openspec/config.yaml`
- Modify: `docs/notes/progress.md`
- Modify: `openspec/changes/rust-engine-collision-raycast-ci-stability/tasks.md`
- Consume only: `cmd/mornlea/app_input.go`, `cmd/mornlea/target_block.go`, `internal/sim/container.go`, `internal/sim/mining.go`, `internal/sim/engine_placement.go`
- Report only, ignored: `.superpowers/sdd/2026-08-14-rust-engine-collision-raycast-ci-stability/task-8-report.md`

**Interfaces:**

```c
uint32_t mornlea_raycast_batch(
    uint32_t abi_version,
    const uint8_t *input,
    size_t input_len,
    uint8_t *cursor,
    size_t cursor_len,
    uint8_t *output,
    size_t output_len,
    size_t *output_count,
    uint8_t *done);
```

```go
func RaycastBatch(input, cursor, output []byte) (count int, done bool)
```

`core.RaycastBlocks` keeps its public signature, validation errors, one-time nested-`math.Hypot` normalization, callback ownership and `RayHit.Point` calculation.

- [ ] **Step 1: Freeze input, cursor and record layouts with RED tests**

Add `TestRaycastInputCursorAndRecordLayoutV1`, `TestRaycastBatchRawFailureAtomicity`, `TestRaycastBatchRejectsInvalidSuccessMetadata`, and Rust `raycast_layout_v1_is_stable` with exact little-endian layouts:

```text
input = 40 bytes
  0..3  magic "MGR1"
  4..7  u32 layout_version = 1
  8..19 f32 origin[x,y,z]
 20..31 f32 normalized_direction[x,y,z]
 32..35 f32 max_distance
 36..39 zero

cursor = 64 bytes
  0..3  magic "MRC1"
  4..7  u32 layout_version = 1
  8     u8 state: 0=fresh, 1=active, 2=done
  9..11 zero
 12..23 i32 cell[x,y,z]
 24..35 i32 step[x,y,z]
 36..47 f32 t_delta[x,y,z]
 48..59 f32 t_max[x,y,z]
 60..63 zero

record = 20 bytes, output = 64 records = 1280 bytes
  0..11 i32 block[x,y,z]
 12     u8 face: 0..5, origin=0xff
 13..15 zero
 16..19 f32 distance
```

Run RED:

```bash
make rust
go test ./internal/nativeabi ./internal/core \
  -run '^TestRaycast(InputCursorAndRecordLayoutV1|BatchRawFailureAtomicity|BatchRejectsInvalidSuccessMetadata)$' \
  -race -count=1
```

Expected: FAIL because the native batch function and layouts do not exist.

- [ ] **Step 2: Add an atomic total batch FFI**

Append the header declaration after existing symbols. Add verified directives in `internal/nativeabi`:

```c
#cgo noescape mornlea_raycast_batch
#cgo nocallback mornlea_raycast_batch
```

The unexported raw Go wrapper returns status/count/raw-done for `internal/nativeabi` tests; exported `RaycastBatch` panics with stable leaf-package text on non-OK status or an impossible success result. Before traversal, Rust validates pointers, exact 40/64/1280 lengths, pairwise overlap, input/cursor magic/layout/state/reserved bytes and the input/cursor numeric domain. Generated face bytes and caller result storage are outputs, not prevalidation inputs. Reuse status 1 for ABI mismatch, 2 for invalid result pointers/ranges/alignment/overlap or oversized fixed output, 3 for malformed input/cursor, 7 for output shorter than 1280 bytes, and 9 for panic. It sets `output_count=0` and `done=0` once those two result pointers are valid, but leaves cursor/output unchanged on every failure or panic.

For a valid call, copy the 64-byte cursor to a local value, generate at most 64 records into a local `[u8; 1280]`, then publish output and cursor once. Rust construction guarantees face/count domains. The leaf wrapper still rejects `count > 64`, raw `done` outside `0/1`, and `count == 0 && done == 0` so a malformed success can never spin the Go loop. Unit-test this postcondition helper with raw count/done values. After prevalidation the Rust batch path is total and cannot report a later data-dependent error. Do not allocate, retain state globally, or add a distance cap.

- [ ] **Step 3: Freeze the Go oracle and write semantic/production RED tests**

Mechanically copy current `internal/core/raycast.go` DDA to `raycast_oracle_test.go` with `package core`; rename the entry point and every copied private helper with an `oracle` prefix, and do not clean up its arithmetic. Before implementing `raycast.rs` or switching production, add all of these tests:

```text
TestCoreUsesOnlyNativeRaycast
TestNativeRaycastMatchesGoOracle
TestNativeRaycastMatchesGoOracleDeterministicCorpus
TestRaycastBlocksPreservesCallbackErrorIdentity
TestRaycastBlocksStopsAfterFirstSolidInPrefetchedBatch
TestRaycastBlocksContinuesAcrossNativeBatchBoundary
TestRaycastBlocksNearInt32BoundaryPreservesFirstCallbackError
TestNativeRaycastConcurrentCalls
TestRaycastBlocksDoesNotAllocate
FuzzNativeRaycastMatchesGoOracle
```

The deterministic corpus includes origin solid with signed zero, a voxel-boundary origin where the adjacent real-face record also has distance zero, six axis faces, negative coordinates, zero components/infinity cursor fields, diagonal XYZ ties, exact and Nextafter endpoint, >64 records and near-int32 wrapping. Error identity uses `err == sentinel`, not only `errors.Is`. The prefetch test counts callbacks and proves no record after the first solid/error is observed.

Run focused RED:

```bash
go test ./internal/core -run '^Test(NativeRaycast|RaycastBlocks)' -race -count=1
go test ./internal/archcheck -run '^TestCoreUsesOnlyNativeRaycast$' -count=1
```

Expected: native parity fails because the Rust kernel is absent, and `TestCoreUsesOnlyNativeRaycast` fails because production still contains Go DDA. Do not add a Go fallback or fake batch.

- [ ] **Step 4: Port only DDA cursor advancement to Rust and turn raw parity GREEN**

On a fresh cursor, emit the origin cell first with face `0xff` and distance `0`; initialize per-axis step/tDelta/tMax using the normalized direction. Zero direction components intentionally use positive infinity and are valid. Axis selection uses only strict `<`, preserving X then Y then Z tie priority. Stop only when `distance > max_distance`, so the exact endpoint remains included. Advance the cell with wrapping i32 addition and persist cursor state across batches.

An active cursor rejects NaN and negative infinity, but permits positive infinity on zero-direction axes and does not impose an invented nonnegative constraint on `t_max`; Rust must consume the Go-normalized direction without renormalizing it.

Rust does not call the solid callback and does not compute `RayHit.Point`. Add inline tests for origin, negative floor, XYZ tie, exact/next-float endpoint, continuation after 64 records, i32 wrapping, failure atomicity and allocation-free fixed-array execution.

- [ ] **Step 5: Replace Go traversal with a fixed-buffer batch driver**

Production `RaycastBlocks` retains current validation and performs nested-Hypot normalization exactly once. It encodes `[40]byte`, initializes `[64]byte` cursor and uses `[1280]byte` output on the stack:

```go
firstRecord := true
for {
	count, done := nativeabi.RaycastBatch(input[:], cursor[:], output[:])
	for index := range count {
		record := decodeRaycastRecord(output[index*20 : (index+1)*20])
		if (firstRecord && (record.face != BlockFaceNone || record.distance != 0)) ||
			(!firstRecord && record.face == BlockFaceNone) {
			panic("core: native raycast origin record 非法")
		}
		occupied, err := solid(record.block)
		if err != nil {
			return RayHit{}, false, err
		}
		if occupied {
			point := origin.Add(direction.Mul(record.distance))
			if record.face == BlockFaceNone {
				point = origin
			}
			return RayHit{
				Block: record.block, Face: record.face,
				Distance: record.distance, Point: point,
			}, true, nil
		}
		firstRecord = false
	}
	if done {
		return RayHit{}, false, nil
	}
}
```

Only the unique global first `BlockFaceNone`/distance-zero record assigns `point = origin` to preserve signed-zero bits. A later real-face record at distance zero still executes the existing `origin.Add(direction.Mul(0))` path. Decode and validate each consumed record's face/reserved/distance in order and stop immediately; never decode/callback the remainder after an error or solid hit. Rerun the Step 3 suite and the zero-allocation test GREEN.

- [ ] **Step 6: Update the real dependency, Hook route and Linux symbol gate**

Authorize only `core -> nativeabi` and retain `physics -> core,nativeabi`. Update engine-specific source guards for the new symbol. Expand the fixed native Hook union with `core` and `cmd/mornlea`, still unconditionally for Rust-only and mixed native-Go changes. Extend the existing Ubuntu workflow's per-symbol `.so` loop to require `mornlea_raycast_batch` as the fourth symbol. Do not create a generic native-backend abstraction.

- [ ] **Step 7: Demonstrate raycast mutation REDs**

Apply and restore separately:

1. Rust XYZ tie uses `<=`: `TestRaycastBlocksUsesXYZTiePriority` fails.
2. endpoint test uses `>=`: exact/Nextafter test fails.
3. cursor marks done after record 64: cross-batch test fails.
4. Go consumes a full batch before honoring callback result: lazy/error identity tests fail.

Restore each mutation and rerun the focused test GREEN before proceeding.

- [ ] **Step 8: Run focused/downstream verification and commit**

```bash
make rust
make rust-check
go test ./internal/nativeabi ./internal/core -race -count=1
go test ./internal/core -run '^Test(RaycastBlocks|NativeRaycast)' -race -count=1
go test ./internal/core -run '^TestRaycastBlocksDoesNotAllocate$' -count=1
go test ./internal/core -run=^$ \
  -fuzz=FuzzNativeRaycastMatchesGoOracle -fuzztime=30s
go test ./internal/core -run '^$' -bench '^BenchmarkRaycastBlocks$' -benchmem -count=5
go test ./internal/physics ./internal/client ./internal/sim ./internal/server \
  ./cmd/mornlea ./cmd/mornlea-server -race -count=1
go test ./internal/archcheck -count=1
node --test scripts/agent-hooks/guard.test.mjs
go vet ./internal/nativeabi ./internal/core ./internal/physics ./internal/sim ./internal/server \
  ./cmd/mornlea ./cmd/mornlea-server
test -z "$(gofmt -l internal/nativeabi internal/core internal/archcheck)"
cmp AGENTS.md CLAUDE.md
git diff --check
openspec validate --all --strict --no-interactive
git add -- engine/include/mornlea_engine.h engine/crates/mornlea_engine/src \
  internal/nativeabi internal/core/raycast.go internal/core/raycast_oracle_test.go \
  internal/core/raycast_native_test.go internal/core/raycast_test.go \
  internal/core/raycast_fuzz_test.go internal/core/raycast_bench_test.go \
  internal/archcheck scripts/agent-hooks .github/workflows/ci.yml \
  AGENTS.md CLAUDE.md README.md README.en.md \
  openspec/config.yaml docs/notes/progress.md \
  openspec/changes/rust-engine-collision-raycast-ci-stability/tasks.md
git diff --cached --check
git commit -m "feat: 用 Rust 遍历方块射线"
```

---

### Task 9: 冻结当前 HEAD 并生成累计正确性、视觉与性能证据

**Files:**
- Modify: `docs/notes/perf-baseline.md`
- Modify: `docs/notes/perf-baseline-m5.md`
- Modify: `openspec/changes/rust-engine-collision-raycast-ci-stability/tasks.md`
- Preserve byte-for-byte: `docs/notes/perf-baseline.json`
- Preserve byte-for-byte: `docs/notes/perf-baseline-m5.json`
- Preserve byte-for-byte: `cmd/mornlea/testdata/golden/*.png`
- Report only, ignored: `.superpowers/sdd/2026-08-14-rust-engine-collision-raycast-ci-stability/final-report.md`
- Temporary only: `/private/tmp/mornlea-rust-kernel-*`
- Temporary ignored: `.superpowers/sdd/2026-08-14-rust-engine-collision-raycast-ci-stability/producer-head.txt`

**Interfaces:**
- Consumes: Tasks 1–8 committed and clean.
- Produces: one frozen-code HEAD, complete local/macOS evidence, record-only scenario v16 Memory/TCP reports, and a small tracked provenance note; no baseline/golden promotion.

- [ ] **Step 1: Freeze the producer HEAD and immutable artifacts**

Run before any heavy command:

```bash
test -z "$(git status --short)"
producer_head="$(git rev-parse HEAD)"
mkdir -p .superpowers/sdd/2026-08-14-rust-engine-collision-raycast-ci-stability
printf '%s\n' "$producer_head" > \
  .superpowers/sdd/2026-08-14-rust-engine-collision-raycast-ci-stability/producer-head.txt
test "$(wc -c < docs/notes/perf-baseline.json)" -eq 3271
test "$(wc -c < docs/notes/perf-baseline-m5.json)" -eq 3270
test "$(shasum -a 256 docs/notes/perf-baseline.json | awk '{print $1}')" = \
  9691d9752f309795e77176c6f959c357c4c97f1f7daaa4a5a6fddff8bf164d78
test "$(shasum -a 256 docs/notes/perf-baseline-m5.json | awk '{print $1}')" = \
  5a34fe091cb1aacfee0172db90b5a7f66571202d230e7542660dd8e703132483
test "$(git hash-object docs/notes/perf-baseline.json)" = \
  b37b3470ffb55ddea85520dd8cf1c0104813b526
test "$(git hash-object docs/notes/perf-baseline-m5.json)" = \
  3563a7b3f951355864bce707ff2176cba889793b
test -z "$(git diff --name-status 88558088beecf74d76669d23d83622358b87a73f..HEAD -- cmd/mornlea/testdata/golden)"
test "$(find cmd/mornlea/testdata/golden -maxdepth 1 -type f -name '*.png' | wc -l | tr -d ' ')" -eq 11
go version
(
  cd engine
  rustup show active-toolchain
  "$(rustup which rustc)" --version
  "$(rustup which cargo)" --version
)
sw_vers
sysctl -n machdep.cpu.brand_string
sysctl -n hw.memsize
uptime
pgrep -fl 'mornlea|mornlea-server' || true
```

Expected: clean tree, Go 1.26, Rust 1.97.1, exact baseline bytes/SHA/blob IDs, 11 unchanged goldens. Hardware/load/process observations are provenance only and must not change command exit status.

- [ ] **Step 2: Run Rust, focused race, repeated CI fixtures and fuzz**

Run once in this order; stop on the first real gate failure:

```bash
make rust-check
make rust
go test ./internal/nativeabi ./internal/core ./internal/physics ./internal/mesh \
  -race -count=1
go test ./internal/server \
  -run 'Test(ReconnectContinuesWorldTimeWithoutRollback|HostHeartbeatTimeoutCleanupIsIsolated)' \
  -race -count=100
go test ./internal/server -race -count=1
go test ./internal/client ./internal/sim ./cmd/mornlea -race -count=1
go test ./internal/archcheck -count=1
node --test scripts/agent-hooks/guard.test.mjs
go test ./internal/mesh -run '^$' \
  -fuzz=FuzzNativeMeshRejectsMalformedInput -fuzztime=10s
go test ./internal/physics -run '^$' \
  -fuzz=FuzzNativeCollisionMatchesGoOracle -fuzztime=30s
go test ./internal/core -run '^$' \
  -fuzz=FuzzNativeRaycastMatchesGoOracle -fuzztime=30s
```

Record executions/new-interesting counts. A panic, race, incomplete fuzz result, or fixture failure blocks; do not retry, raise deadlines, update expectations, or skip the test.

- [ ] **Step 3: Record the exact hot-path microbenchmarks**

```bash
go test ./internal/mesh -run '^$' \
  -bench '^BenchmarkMeshTerrainSection$' -benchmem -count=5
go test ./internal/physics -run '^$' \
  -bench '^BenchmarkStepPlayer(Flat|Colliding|Stepping)$' -benchmem -count=5
go test ./internal/core -run '^$' \
  -bench '^BenchmarkRaycastBlocks$' -benchmem -count=5
```

Record all five runs and allocation counts. Performance numbers are record-only; a missing benchmark, panic, nonzero correctness exit or lost zero-allocation invariant is still a failure.

- [ ] **Step 4: Verify macOS symbols, detached loading and all visual goldens**

```bash
make build
nm -gU bin/libmornlea_engine.dylib > /private/tmp/mornlea-rust-kernel-symbols.txt
for symbol in mornlea_engine_abi_version mornlea_mesh_section mornlea_collision_resolve mornlea_raycast_batch; do
  awk '{print $NF}' /private/tmp/mornlea-rust-kernel-symbols.txt | rg -Fx "_$symbol"
done
! otool -L bin/mornlea bin/mornlea-server bin/libmornlea_engine.dylib | rg -F 'libmornlea_mesh'
otool -D bin/libmornlea_engine.dylib | rg -Fx '@rpath/libmornlea_engine.dylib'
otool -L bin/mornlea | rg -F '@rpath/libmornlea_engine.dylib'
otool -L bin/mornlea-server | rg -F '@rpath/libmornlea_engine.dylib'
otool -l bin/mornlea > /private/tmp/mornlea-rust-kernel-client-otool.txt
otool -l bin/mornlea-server > /private/tmp/mornlea-rust-kernel-server-otool.txt
rg -A2 LC_RPATH /private/tmp/mornlea-rust-kernel-client-otool.txt | rg -F '@loader_path'
rg -A2 LC_RPATH /private/tmp/mornlea-rust-kernel-server-otool.txt | rg -F '@loader_path'
! rg '/engine/target/.*/deps' /private/tmp/mornlea-rust-kernel-*-otool.txt
native_target_backup="$(mktemp -d)/target"
trap 'test -e engine/target || mv "$native_target_backup" engine/target' EXIT
mv engine/target "$native_target_backup"
set +e
bin/mornlea -h >/private/tmp/mornlea-rust-kernel-client-help.txt 2>&1
native_client_rc=$?
bin/mornlea-server -h >/private/tmp/mornlea-rust-kernel-server-help.txt 2>&1
native_server_rc=$?
set -e
mv "$native_target_backup" engine/target
trap - EXIT
test "$native_client_rc" -eq 1
test "$native_server_rc" -eq 1
rg -F 'flag: help requested' /private/tmp/mornlea-rust-kernel-client-help.txt
rg -F 'flag: help requested' /private/tmp/mornlea-rust-kernel-server-help.txt
! rg 'dyld|Library not loaded' /private/tmp/mornlea-rust-kernel-*-help.txt
producer_head="$(cat .superpowers/sdd/2026-08-14-rust-engine-collision-raycast-ci-stability/producer-head.txt)"
test "$(git rev-parse HEAD)" = "$producer_head"
visual_dir="/private/tmp/mornlea-rust-kernel-visual-$producer_head"
test ! -e "$visual_dir"
mkdir "$visual_dir"
VISUAL_OUT="$visual_dir" make visual-check
test "$(find "$visual_dir" -maxdepth 1 -type f -name '*.png' | wc -l | tr -d ' ')" -eq 11
test -z "$(find "$visual_dir" -maxdepth 1 -type f \( -name '*-actual.png' -o -name '*-diff.png' \) -print)"
```

The target restore is mandatory before judging the smoke. Visual verification uses existing dual thresholds and must not update or require byte-equality as a substitute for those thresholds.

- [ ] **Step 5: Generate fresh complete scenario v16 Memory/TCP reports**

Use one new directory and the frozen `producer_head`. Do not overwrite tracked baselines:

```bash
producer_head="$(cat .superpowers/sdd/2026-08-14-rust-engine-collision-raycast-ci-stability/producer-head.txt)"
test "$(git rev-parse HEAD)" = "$producer_head"
perf_dir="/private/tmp/mornlea-rust-kernel-v16-$producer_head"
test ! -e "$perf_dir"
mkdir "$perf_dir"
TERM=xterm-256color zsh -ic \
  "gvm use go1.26 >/dev/null && go run ./cmd/mornlea --benchmark --benchmark-transport memory --perf-output '$perf_dir/memory-v16.json'"
jq -e --arg head "$producer_head" \
  '.scenario_version == 16 and .transport == "memory" and .git_commit == $head' \
  "$perf_dir/memory-v16.json"
zsh -ic \
  "gvm use go1.26 >/dev/null && go run ./cmd/perfcheck --baseline '$perf_dir/memory-v16.json' --current '$perf_dir/memory-v16.json'"
TERM=xterm-256color zsh -ic \
  "gvm use go1.26 >/dev/null && go run ./cmd/mornlea --benchmark --benchmark-transport tcp --perf-output '$perf_dir/tcp-v16.json'"
jq -e --arg head "$producer_head" \
  '.scenario_version == 16 and .transport == "tcp" and .git_commit == $head' \
  "$perf_dir/tcp-v16.json"
zsh -ic \
  "gvm use go1.26 >/dev/null && go run ./cmd/perfcheck --baseline '$perf_dir/tcp-v16.json' --current '$perf_dir/tcp-v16.json'"
zsh -ic \
  "gvm use go1.26 >/dev/null && go run ./cmd/perfcheck --baseline '$perf_dir/memory-v16.json' --current '$perf_dir/tcp-v16.json'"
shasum -a 256 "$perf_dir/memory-v16.json" "$perf_dir/tcp-v16.json"
pgrep -fl 'mornlea|mornlea-server' || true
```

Expected: both full producers, both self-checks and the explicit cross-transport comparison exit 0 with the same scenario, commit and hardware identity. Performance values and host steadiness remain record-only; missing fields, incomplete samples, identity mismatch, real overflow, data loss or I/O failure block. Do not add a new scenario version or migration rule.

- [ ] **Step 6: Run final full gates and repeat immutable guards**

```bash
producer_head="$(cat .superpowers/sdd/2026-08-14-rust-engine-collision-raycast-ci-stability/producer-head.txt)"
go test ./... -race -count=1
go vet ./...
test -z "$(gofmt -l .)"
git diff --check
openspec validate rust-engine-collision-raycast-ci-stability --strict --no-interactive
openspec validate --all --strict --no-interactive
test "$(git rev-parse HEAD)" = "$producer_head"
test -z "$(git status --short)"
test "$(shasum -a 256 docs/notes/perf-baseline.json | awk '{print $1}')" = \
  9691d9752f309795e77176c6f959c357c4c97f1f7daaa4a5a6fddff8bf164d78
test "$(shasum -a 256 docs/notes/perf-baseline-m5.json | awk '{print $1}')" = \
  5a34fe091cb1aacfee0172db90b5a7f66571202d230e7542660dd8e703132483
test -z "$(git diff --name-status 88558088beecf74d76669d23d83622358b87a73f..HEAD -- cmd/mornlea/testdata/golden)"
```

- [ ] **Step 7: Record provenance without promoting a baseline**

Append a short top section to `docs/notes/perf-baseline-m5.md` containing the producer HEAD, Memory/TCP SHA-256, hardware, scenario 16, transport, complete phase result and record-only statement. Update the current-status link/text at the top of `docs/notes/perf-baseline.md`. Do not edit either JSON baseline.

Write `final-report.md` with every command, exit status, fuzz count, benchmark result, visual directory, report directory/hash, immutable guard and current HEAD. Then mark only the cumulative-gate active tasks complete and commit the evidence-only delta:

```bash
test -z "$(git diff --name-only -- docs/notes/perf-baseline.json \
  docs/notes/perf-baseline-m5.json cmd/mornlea/testdata/golden)"
git add -- docs/notes/perf-baseline.md docs/notes/perf-baseline-m5.md \
  openspec/changes/rust-engine-collision-raycast-ci-stability/tasks.md
git diff --cached --check
git commit -m "docs: 记录 Rust kernel 累计门禁"
```

Expected: the commit contains only two Markdown provenance notes and task checkboxes; report JSONs remain temporary.

---

### Task 10: 完成累计独立评审并交付 `READY_FOR_CI`

**Files:**
- Modify: `openspec/changes/rust-engine-collision-raycast-ci-stability/tasks.md`
- Possible review fixes: only files named by a verified finding; update this plan/OpenSpec first if a finding expands behavior or architecture
- Report only, ignored: `.superpowers/sdd/2026-08-14-rust-engine-collision-raycast-ci-stability/final-review.md`

**Interfaces:**
- Consumes: full branch range `88558088beecf74d76669d23d83622358b87a73f..HEAD` and all Task 1–9 reports.
- Produces: independent Critical/Important/Minor verdict, all local active tasks checked, and a clean branch ready for an authorized draft PR.

- [ ] **Step 1: Request a fresh cumulative review**

Use `superpowers:requesting-code-review`. The reviewer must be read-only and must inspect code/specs/tests rather than trusting task reports. Require this exact checklist:

```text
CI:
- reconnect waits for both Host indexes to release;
- healthy Memory reader starts and registers cleanup before Ready;
- no deadline, heartbeat, production login, retry or workflow-dedup relaxation.

Native identity/ABI:
- one std-only mornlea_engine crate/library; no old artifact name;
- ABI v1, status 0..9, mesh symbol/layout/semantics unchanged;
- nativeabi is the only engine header/cgo/status leaf;
- all four symbols have truthful noescape/nocallback declarations;
- no Go fallback, backend interface, pool, retained pointer or Rust global mutable state.

Collision:
- exact 64/196/16 layouts, Y/X/Z and one query per cell;
- 135 stack path, Count clamp, checked 4096/4097 boundary;
- unknown closed, strict step comparison, rejected-step unknown isolation;
- Step ownership/state publication and bitwise oracle/concurrency/zero-allocation coverage.

Raycast:
- exact 40/64/20×64 layouts and atomic cursor/output;
- one Go normalization, origin/signed-zero, negative floor, strict XYZ tie,
  exact endpoint, wrapping cell and unbounded multi-batch continuation;
- callback laziness, first error identity and zero allocation.

Build/platform/artifacts:
- macOS adjacent dylib and Linux amd64 adjacent server/so with $ORIGIN;
- server remains headless/no graphics dependency;
- current docs/Hook/archcheck agree; historical docs remain historical;
- protocol/storage/scenario/baselines/goldens unchanged;
- visual/perf evidence belongs to the frozen producer code.

Ponytail:
- no speculative abstraction, duplicate production implementation,
  extra crate/library/dependency, generic snapshot or packaging framework.
```

Reviewer writes strengths, then Critical/Important/Minor findings with `file:line`, cause and minimal fix, and a final `Ready: yes/no`.

- [ ] **Step 2: Resolve findings with evidence, not deference**

For each finding, first use `superpowers:receiving-code-review` to reproduce or disprove it. Accepted behavior fixes use TDD and a separate focused commit. Do not weaken parity, capacity, allocation, bundle, visual or report gates.

If a fix touches production Go/Rust, C header, Make/CI, native loading, benchmark producer or scenario workload, invalidate all Task 9 visual/perf evidence and rerun Task 9 Steps 1–7 from a new frozen producer HEAD. If it touches only tests/OpenSpec/docs, run the affected race/static/OpenSpec gates and record that the producer binary is unchanged. Re-review every fix wave until Critical/Important/Minor are all zero.

- [ ] **Step 3: Mark local active tasks complete and commit review state**

Only after `Ready: yes`, first mark the cumulative review/local-gate items complete, then prove the real Linux PR-CI item is the sole unchecked task:

```bash
remaining_tasks="$(awk '/^[[:space:]]*- \[ \]/{count++} END{print count+0}' \
  openspec/changes/rust-engine-collision-raycast-ci-stability/tasks.md)"
test "$remaining_tasks" -eq 1
rg -n '^[[:space:]]*- \[ \]' \
  openspec/changes/rust-engine-collision-raycast-ci-stability/tasks.md
```

The sole remaining active item must be the real Linux PR-CI gate; archive itself is not an active checkbox. Leave that Linux item unchecked, then:

```bash
openspec validate rust-engine-collision-raycast-ci-stability --strict --no-interactive
openspec validate --all --strict --no-interactive
git add -- openspec/changes/rust-engine-collision-raycast-ci-stability/tasks.md
git diff --cached --check
git commit -m "docs: 完成 Rust kernel 累计评审"
git status --short --branch
```

- [ ] **Step 4: Hand off as `READY_FOR_CI` without offering merge**

Report the clean branch and zero-finding review, then ask only whether the user authorizes pushing this branch and creating a draft PR to run the required Linux CI, or wants to leave the branch local. Do not invoke the branch-finishing merge choices while the Linux CI checkbox is still open. Do not push, open a PR, mark a PR ready, merge or archive without the user's selected external action.

---

### Task 11: 经授权验证 Linux CI、同步主规格并归档

**Files:**
- Modify: `openspec/changes/rust-engine-collision-raycast-ci-stability/tasks.md`
- Modify: `openspec/specs/rust-engine-mesh/spec.md`
- Modify: `openspec/specs/project-identity/spec.md`
- Create: `openspec/specs/rust-engine-collision-raycast/spec.md`
- Move: `openspec/changes/rust-engine-collision-raycast-ci-stability` → `openspec/changes/archive/2026-08-14-rust-engine-collision-raycast-ci-stability`
- Report only, ignored: `.superpowers/sdd/2026-08-14-rust-engine-collision-raycast-ci-stability/archive-report.md`
- Report only, ignored: `.superpowers/sdd/2026-08-14-rust-engine-collision-raycast-ci-stability/pr-body.md`

**Interfaces:**
- Consumes: explicit user selection to publish a branch/draft PR, then green macOS `test` and Linux `linux-server` jobs on the exact code HEAD.
- Produces: synced main specs, archived change, green final PR revision; does not merge the PR without separate authorization.

- [ ] **Step 1: Publish only after the user selects the PR path**

Use the authorized GitHub publishing workflow. With `gh` fallback, the exact operations are:

First use `apply_patch` to write the ignored `pr-body.md` with these concrete sections and facts: `Summary` names the two CI happens-before fixes and the collision/raycast migration; `Compatibility` states additive ABI v1, unchanged mesh symbol/layout/status plus unchanged protocol/storage/scenario/baselines/goldens, and the Linux adjacent server/so release-unit change; `Verification` lists the exact race/fuzz/benchmark/visual/macOS/Linux commands; `Visual/Performance Evidence` copies the concrete producer HEAD, directories and SHA-256 values from `final-report.md`; `Review` records the zero-finding verdict. Do not leave template markers or prose instructions in the body.

```bash
test -s .superpowers/sdd/2026-08-14-rust-engine-collision-raycast-ci-stability/pr-body.md
test -z "$(git status --short)"
branch_name="$(git branch --show-current)"
test -n "$branch_name"
test "$branch_name" != main
git push -u origin "$branch_name"
gh pr create --draft --base main --head "$branch_name" \
  --title 'Rust collision/raycast 与 CI 稳定化' \
  --body-file .superpowers/sdd/2026-08-14-rust-engine-collision-raycast-ci-stability/pr-body.md
pr_number="$(gh pr view --json number -q .number)"
gh pr checks "$pr_number" --watch --fail-fast
```

The ignored PR body must summarize compatibility, two real CI roots, one-library ABI, macOS/Linux bundles, tests and record-only evidence. Do not use force push.

- [ ] **Step 2: Validate the real Ubuntu bundle job**

After checks finish:

```bash
branch_name="$(git branch --show-current)"
pr_number="$(gh pr view --json number -q .number)"
run_id="$(gh run list --branch "$branch_name" --event pull_request --limit 1 --json databaseId -q '.[0].databaseId')"
expected_head="$(git rev-parse HEAD)"
gh run view "$run_id" --json status,conclusion,headSha,jobs > /private/tmp/mornlea-rust-kernel-pr-run.json
jq -e --arg head "$expected_head" \
  '.headSha == $head and .status == "completed" and .conclusion == "success" and
    ([.jobs[].name] | index("test") != null) and
    ([.jobs[].name] | index("linux-server") != null) and
    ([.jobs[] | select(.name == "test" or .name == "linux-server") | .conclusion] | all(. == "success"))' \
  /private/tmp/mornlea-rust-kernel-pr-run.json
gh run view "$run_id" --log > /private/tmp/mornlea-rust-kernel-pr-run.log
rg -F 'libmornlea_engine.so' /private/tmp/mornlea-rust-kernel-pr-run.log
rg -F '$ORIGIN' /private/tmp/mornlea-rust-kernel-pr-run.log
rg -F 'flag: help requested' /private/tmp/mornlea-rust-kernel-pr-run.log
```

Record run ID/job URLs and confirm both historical flaky tests passed in the macOS full-race job. A missing/skipped Linux job, loader error, graphics dependency, parity/race failure or incomplete log blocks; fix the root cause, rerun Task 9 proportionally, obtain fresh review, and wait for a new green run.

- [ ] **Step 3: Complete active tasks and archive with the official workflow**

Mark the final Linux CI item complete and prove no active task remains:

```bash
remaining_tasks="$(awk '/^[[:space:]]*- \[ \]/{count++} END{print count+0}' \
  openspec/changes/rust-engine-collision-raycast-ci-stability/tasks.md)"
test "$remaining_tasks" -eq 0
openspec validate rust-engine-collision-raycast-ci-stability --strict --no-interactive
```

Then use `openspec-archive-change` and run the canonical archive command once:

```bash
openspec archive rust-engine-collision-raycast-ci-stability --yes
```

The resulting main-spec delta must be exact:

```text
MODIFIED openspec/specs/rust-engine-mesh/spec.md
MODIFIED openspec/specs/project-identity/spec.md
ADDED    openspec/specs/rust-engine-collision-raycast/spec.md
RENAMED  openspec/changes/rust-engine-collision-raycast-ci-stability/**
         → openspec/changes/archive/2026-08-14-rust-engine-collision-raycast-ci-stability/**
```

Compare every modified Requirement block to the delta and prove all untouched main-spec Requirements/Scenarios remain. Do not edit other main specs or historical archives.

- [ ] **Step 4: Validate and commit the archive**

```bash
test -z "$(openspec list --json | rg 'rust-engine-collision-raycast-ci-stability' || true)"
test -f openspec/changes/archive/2026-08-14-rust-engine-collision-raycast-ci-stability/.openspec.yaml
openspec validate --all --strict --no-interactive
git diff --check
git add -A -- openspec/changes/rust-engine-collision-raycast-ci-stability \
  openspec/changes/archive/2026-08-14-rust-engine-collision-raycast-ci-stability \
  openspec/specs/rust-engine-mesh openspec/specs/project-identity \
  openspec/specs/rust-engine-collision-raycast
git diff --cached --name-status
git diff --cached --check
git commit -m "docs: 归档 Rust 碰撞射线与 CI 稳定化"
test -z "$(git status --short)"
git status --short --branch
```

Push the archive commit only under the already-authorized PR workflow, then require the new revision to pass again:

```bash
branch_name="$(git branch --show-current)"
pr_number="$(gh pr view --json number -q .number)"
git push origin "$branch_name"
gh pr checks "$pr_number" --watch --fail-fast
archive_head="$(git rev-parse HEAD)"
archive_run_id="$(gh run list --branch "$branch_name" --event pull_request --limit 1 --json databaseId -q '.[0].databaseId')"
gh run view "$archive_run_id" --json status,conclusion,headSha,jobs > \
  /private/tmp/mornlea-rust-kernel-archive-run.json
jq -e --arg head "$archive_head" \
  '.headSha == $head and .status == "completed" and .conclusion == "success" and
    ([.jobs[] | select(.name == "test" or .name == "linux-server") | .conclusion] | length == 2) and
    ([.jobs[] | select(.name == "test" or .name == "linux-server") | .conclusion] | all(. == "success"))' \
  /private/tmp/mornlea-rust-kernel-archive-run.json
```

Update `archive-report.md` with the final SHA and run IDs. Leave the PR draft/merge state to the user's explicit choice; do not merge automatically.

---

## Plan Self-Review Map

| Contract | Implemented/tested in |
| --- | --- |
| Two actual CI happens-before roots, no deadline relaxation | Tasks 1–3, 9, 11 |
| One std-only `mornlea_engine`, old mesh ABI unchanged | Tasks 4–6, 9–11 |
| Unique engine cgo/status leaf | Tasks 5–8, 10 |
| Collision Y/X/Z, Count clamp, unknown, strict step, 4096 cap | Tasks 6–7, 9–10 |
| Raycast 64-batch, lazy callback, error identity, tie/endpoint/wrap | Task 8, 9–10 |
| macOS dylib and Linux amd64 server/so bundle | Tasks 7, 9, 11 |
| No protocol/storage/scenario/baseline/golden drift | Tasks 1, 4, 7–11 |
| Full race/fuzz/bench/visual/perf and independent review | Tasks 9–10 |
| Main-spec sync and non-self-referential archive | Task 11 |

Before executing the plan, verify it contains no placeholder instruction and all named paths/tests/interfaces agree:

```bash
plan=docs/superpowers/plans/2026-08-14-rust-engine-collision-raycast-ci-stability.md
placeholder_pattern='T''BD|TO''DO|implement lat''er|fill i''n|similar t''o|as appropri''ate|占''位'
! rg -n "$placeholder_pattern" "$plan"
rg -n '^### Task [0-9]+:' "$plan"
test -z "$(git diff --no-index --check /dev/null "$plan" 2>&1 || true)"
```

Expected: 11 ordered tasks, no placeholder hit, clean diff. The intentional simplifications are fixed: one crate/library, one concrete bridge, two independent POD ABIs, Go-owned state/callbacks, no fallback/pool/framework.
