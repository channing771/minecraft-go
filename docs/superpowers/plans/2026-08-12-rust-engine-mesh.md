# M4P Rust Engine Mesh Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建立可重复的 Rust/Go native 构建与 ABI，并让 Rust 成为 `internal/mesh` 贪心网格、AO、天空光和方块光的唯一生产实现，同时保持现有 Go 行为、视觉和下游 API。

**Architecture:** Go 继续暂时持有应用和世界状态，把一个 `world.Neighborhood` 与不可变 registry snapshot 编成版本化小端字节块；每个 section 经一次 C ABI 调用进入无全局状态的 Rust `mcgo_mesh` static library。Go 拥有 input、scratch 和 packed-output 缓冲，Rust 只在调用期间借用；旧 Go 算法仅编译进测试作为逐位 oracle。

**Tech Stack:** Go 1.26、Rust 1.97.1 edition 2024、Cargo workspace、C ABI/cgo、Make、GitHub Actions、OpenSpec 1.7.0。

## Global Constraints

- 基线为 `main@2346dede27c5322045ea8c0bd467736e182a50a7`；实施分支为 `codex/m4p-rust-engine-mesh`。
- 只实施设计文档 [`docs/superpowers/specs/2026-08-12-rust-engine-go-rules-design.md`](../specs/2026-08-12-rust-engine-go-rules-design.md) 的第一阶段。
- Rust 1.97.1 必须由 `engine/rust-toolchain.toml` 固定；不使用 floating `stable`。
- 第一阶段只有一个 Rust crate `mcgo_mesh`；不得新增 ECS、async runtime、通用 engine core、backend interface 或第二个 crate。
- `mcgo_mesh` 不得新增第三方 Rust dependency；只使用 `std`。
- Rust 是 `MeshSection` 唯一生产实现；不得保留生产 Go fallback、动态开关或 build tag 双实现。
- 旧 Go greedy/light 只允许存在于 `_test.go` oracle；oracle 删除不属于本计划。
- 不跨 ABI 保存 Go/Rust 指针，不从 Rust 返回需由 Go 释放的内存，不允许 panic/unwind 穿过 C ABI。
- 每个 section 恰好一次 native mesh 调用；output 容量固定为 `6*16*16*16 = 24576` 个 packed `uint64`。
- `48³` light levels 和 `48³` queue 使用调用方持有的固定 scratch；registry 只有当前 27 项，Rust 直接二分查询，不增加每次调用都要清零的 65536 项 ID map。
- 保持 `MeshSection` 的 `[]Quad` 结果、face/slice/row 顺序、`Quad.Pack` 位布局、AO/light、missing-neighbor barrier 和 unknown-ID 语义。
- 保持 client mesher generation、dirty/requeue、worker 数量及 panic recovery 语义。
- 不改变协议 v15、区块 schema v8、玩家 schema v6、world metadata v2、benchmark scenario v15、fixture、golden、性能 baseline 或阈值。
- benchmark/perfcheck 数值只记录；ABI 身份、报告完整性、真实 overflow、数据丢失、I/O、构建和非数值测试失败仍阻断。
- 第一阶段正式客户端验收平台为 Apple Silicon/macOS；`CGO_ENABLED=0 GOOS=linux go build ./cmd/mcgod` 必须继续通过且不得链接 Rust、WebGPU 或窗口依赖。
- canonical clean-checkout 入口为 `make build`、`make test`、`make test-race`；它们必须先执行 `cargo build --locked --release`。
- Rust native artifact、Cargo `target/`、visual output 和 task report 不得提交。
- Go 注释、Rust 注释、测试说明、错误与文档使用中文；标识符、ABI magic、协议字段和外部 API 保留英文。
- 每个任务只提交其 Files 列表；若实际实现需要计划外生产文件、接口、依赖或行为，立即停止并先修订设计/OpenSpec/本计划。

## Target File Map

| 路径 | 职责 |
| --- | --- |
| `engine/Cargo.toml` | workspace 与 release panic 策略 |
| `engine/Cargo.lock` | locked Rust dependency identity |
| `engine/rust-toolchain.toml` | 固定 Rust 1.97.1、rustfmt、clippy |
| `engine/include/mcgo_engine.h` | C ABI 唯一声明来源 |
| `engine/crates/mcgo_mesh/src/ffi.rs` | 版本、状态码、输入验证、panic 边界与 exported symbols |
| `engine/crates/mcgo_mesh/src/input.rs` | 小端 input 与 registry snapshot 解码 |
| `engine/crates/mcgo_mesh/src/light.rs` | `48³` sky/block light 与有界 queue |
| `engine/crates/mcgo_mesh/src/greedy.rs` | visible mask、AO 与 greedy merge |
| `engine/crates/mcgo_mesh/src/quad.rs` | Face、Quad 与精确 64-bit pack |
| `internal/mesh/registry.go` | Go registry reader、不可变 snapshot 与校验 |
| `internal/mesh/native_abi.go` | 唯一 cgo/C ABI bridge 与 status 映射 |
| `internal/mesh/native_input.go` | neighborhood/height/registry 小端编码 |
| `internal/mesh/native.go` | `LightScratch` 固定缓冲与生产 `MeshSection` |
| `internal/mesh/greedy_oracle_test.go` | test-only 旧 Go greedy/AO oracle |
| `internal/mesh/light_oracle_test.go` | test-only 旧 Go light/queue oracle |
| `internal/mesh/native_parity_test.go` | Rust/Go 逐位、错误、并发与 fuzz parity |
| `internal/assets/blocks.go` | 缓存并提供 immutable mesh registry snapshot |
| `Makefile` | canonical Rust-first build/test targets |
| `.github/workflows/ci.yml` | clean CI 的 Rust fmt/clippy/test/build 前置门禁 |
| `scripts/agent-hooks/guard.mjs` | Rust/Go 混合改动的共享 Stop 门禁 |
| `scripts/agent-hooks/guard.test.mjs` | Hook 路由与命令顺序单测 |
| `AGENTS.md`、`CLAUDE.md`、`README.md` | toolchain、canonical 命令与所有权现状 |

---

### Task 1: 建立 M4P OpenSpec 契约并冻结基线

**Files:**
- Create: `openspec/changes/m4p-rust-engine-mesh/.openspec.yaml`
- Create: `openspec/changes/m4p-rust-engine-mesh/proposal.md`
- Create: `openspec/changes/m4p-rust-engine-mesh/design.md`
- Create: `openspec/changes/m4p-rust-engine-mesh/specs/rust-engine-mesh/spec.md`
- Create: `openspec/changes/m4p-rust-engine-mesh/tasks.md`
- Report only, ignored: `.superpowers/sdd/2026-08-12-m4p-rust-engine-mesh/task-1-report.md`

**Interfaces:**
- Consumes: approved design at `docs/superpowers/specs/2026-08-12-rust-engine-go-rules-design.md`.
- Produces: active change `m4p-rust-engine-mesh` with implementation tasks 2–9 and strict validation status.

- [ ] **Step 1: Verify the immutable starting point**

Run:

```bash
git status --short --branch
git merge-base --is-ancestor 2346dede27c5322045ea8c0bd467736e182a50a7 HEAD
openspec list --json
go test ./internal/mesh ./internal/client ./internal/render -race -count=1
go test ./internal/archcheck -count=1
go test ./internal/mesh -run '^$' -bench BenchmarkMeshTerrainSection -benchmem -count=5
```

Expected: clean branch, ancestor check exit 0, M4O complete, and every Go command PASS. Record benchmark values without deriving thresholds.

- [ ] **Step 2: Write the proposal and design artifacts**

`proposal.md` must state:

```markdown
## Why

Go 当前拥有所有引擎层。M4P 通过只迁移确定性区段网格与传播光照，建立首个可回退的 Rust 边界。

## What Changes

- 添加固定版本的 Rust workspace 与 Rust-first canonical 构建链。
- 让 Rust 成为 mesh/light 的唯一生产实现。
- 将旧 Go 实现保留为仅测试编译的精确 oracle。
- 保持既有 gameplay、wire、storage、visual、concurrency 与 packed-quad 行为。

## Non-Goals

- 不迁移 physics、world、sim、network、storage、render 或 process entry。
- 不提供生产 Go fallback、不提交 native binary，也不改变性能阈值。
```

`design.md` must copy the approved ownership, ABI buffer lifecycle, fixed capacities, error mapping, build workflow and rollback decisions without adding later-stage API.

- [ ] **Step 3: Write the delta spec**

`specs/rust-engine-mesh/spec.md` must contain these exact observable requirements and at least the named scenarios:

```markdown
## ADDED Requirements

### Requirement: Rust mesh 保持现有输出契约
系统 MUST 让 Rust 生产实现对同一 neighborhood 与 registry 产生与冻结 Go oracle 数量、顺序和 packed bits 完全相同的 quads。

#### Scenario: 固定与随机输入逐位一致
- **GIVEN** 现有夹具和固定种子随机 neighborhood
- **WHEN** Go oracle 与 Rust 生产实现分别网格化
- **THEN** packed `uint64` 序列 MUST 完全一致

### Requirement: native 边界具有单一所有权
系统 MUST 由 Go 持有 input、scratch 与 output，并禁止任一语言在调用结束后保留对方指针。

#### Scenario: 多 worker 并发
- **GIVEN** 每个 worker 拥有独立 scratch
- **WHEN** 并发网格化不同区段
- **THEN** 结果 MUST 确定且不得共享可变 native 状态

### Requirement: ABI 失败不得产生部分网格
系统 MUST 对版本、长度、registry、emission、output overflow 与 Rust panic 返回可判定失败。

#### Scenario: 非法输入被原子拒绝
- **WHEN** native 调用收到任一非法输入
- **THEN** output length MUST 为 0
- **AND** panic/unwind MUST NOT 穿过 C ABI

### Requirement: clean checkout 使用 Rust-first 构建
系统 MUST 通过 canonical Make、CI 与 Hook 使用固定的 Rust 1.97.1，在 Go 验证前执行 `cargo build --locked --release` 构建 pinned Rust static library；workspace MUST 仅含 `mcgo_mesh`，并且该 crate 的 normal dependency MUST 只使用 `std`。

#### Scenario: 无预编译 artifact 的构建
- **GIVEN** clean checkout 不含 Cargo target 或 native library
- **WHEN** 运行 `make test-race`
- **THEN** 系统 MUST 先以 Rust 1.97.1 执行 `cargo build --locked --release`，再执行 Go race tests
- **AND** `cargo metadata --no-deps --format-version 1 --manifest-path engine/Cargo.toml` MUST 只报告 workspace member `mcgo_mesh`
- **AND** `cargo tree --manifest-path engine/Cargo.toml --workspace --edges normal` MUST 只含 workspace root，且不得报告第三方 dependency

### Requirement: Rust 客户端边界不污染无图形服务端
系统 MUST 保持 `cmd/mcgod` 不依赖 CGO、Rust static library、WebGPU 或窗口包。

#### Scenario: Linux 无 CGO 构建
- **GIVEN** clean checkout 没有 Rust build artifact
- **WHEN** 以 `CGO_ENABLED=0 GOOS=linux` 构建 `./cmd/mcgod`
- **THEN** 构建 MUST 成功且依赖闭包 MUST 不包含 client、mesh、render 或 gfx
```

- [ ] **Step 4: Write ordered OpenSpec tasks**

`tasks.md` must contain unchecked items matching Tasks 2–9 in this plan. Each item names exact files and its focused command; Task 9 owns final full gates and review. Do not mark Task 1 implementation work as complete beyond the planning checkbox itself.

- [ ] **Step 5: Validate and commit the planning contract**

Run:

```bash
openspec validate --all --strict --no-interactive
git diff --check
git add openspec/changes/m4p-rust-engine-mesh
git diff --cached --check
git commit -m "docs: 规划 M4P Rust 网格迁移"
```

Expected: strict validation PASS; commit contains only the five M4P artifact groups.

---

### Task 2: 建立 pinned Cargo、ABI identity 与最小链接闭环

**Files:**
- Create: `engine/Cargo.toml`
- Create: `engine/Cargo.lock`
- Create: `engine/rust-toolchain.toml`
- Create: `engine/crates/mcgo_mesh/Cargo.toml`
- Create: `engine/crates/mcgo_mesh/src/lib.rs`
- Create: `engine/crates/mcgo_mesh/src/ffi.rs`
- Create: `engine/include/mcgo_engine.h`
- Create: `internal/mesh/native_abi.go`
- Create: `internal/mesh/native_abi_test.go`
- Modify: `.gitignore`
- Modify: `Makefile`
- Modify: `openspec/changes/m4p-rust-engine-mesh/tasks.md`

**Interfaces:**
- Consumes: ABI version `1`; Rust 1.97.1; current Darwin+cgo client boundary.
- Produces:
  - C `uint32_t mcgo_engine_abi_version(void)`.
  - Go `func nativeABIVersion() uint32`.
  - Make target `rust` building `engine/target/release/libmcgo_mesh.a`.

- [ ] **Step 1: Write the failing Go ABI identity test**

Create `internal/mesh/native_abi_test.go`:

```go
//go:build darwin && cgo

package mesh

import "testing"

func TestNativeABIVersionMatchesGo(t *testing.T) {
	if got := nativeABIVersion(); got != nativeABIVersionCurrent {
		t.Fatalf("native ABI version=%d，想要 %d", got, nativeABIVersionCurrent)
	}
}
```

- [ ] **Step 2: Run RED before creating the Rust workspace**

Run:

```bash
go test ./internal/mesh -run '^TestNativeABIVersionMatchesGo$' -count=1
```

Expected: compile FAIL because `nativeABIVersion` and `nativeABIVersionCurrent` do not exist.

- [ ] **Step 3: Add the minimal pinned workspace**

Create `engine/Cargo.toml`:

```toml
[workspace]
members = ["crates/mcgo_mesh"]
resolver = "3"

[profile.release]
panic = "unwind"
```

Create `engine/rust-toolchain.toml`:

```toml
[toolchain]
channel = "1.97.1"
profile = "minimal"
components = ["clippy", "rustfmt"]
```

Create `engine/crates/mcgo_mesh/Cargo.toml`:

```toml
[package]
name = "mcgo_mesh"
version = "0.1.0"
edition = "2024"
publish = false

[lib]
crate-type = ["rlib", "staticlib"]
```

Run `cargo generate-lockfile --manifest-path engine/Cargo.toml` and commit the generated `engine/Cargo.lock` unchanged.

- [ ] **Step 4: Implement only the version symbol and header**

Create `engine/crates/mcgo_mesh/src/lib.rs`:

```rust
mod ffi;
```

Create `engine/crates/mcgo_mesh/src/ffi.rs`:

```rust
pub(crate) const ABI_VERSION: u32 = 1;

#[unsafe(no_mangle)]
pub extern "C" fn mcgo_engine_abi_version() -> u32 {
    ABI_VERSION
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn exported_version_is_one() {
        assert_eq!(mcgo_engine_abi_version(), 1);
    }
}
```

Create `engine/include/mcgo_engine.h`:

```c
#ifndef MCGO_ENGINE_H
#define MCGO_ENGINE_H

#include <stddef.h>
#include <stdint.h>

#define MCGO_ENGINE_ABI_VERSION 1u

uint32_t mcgo_engine_abi_version(void);

#endif
```

- [ ] **Step 5: Add the minimal Make and cgo link path**

Append `/engine/target/` to `.gitignore`.

Add to `Makefile`:

```make
CARGO := cargo
RUST_MANIFEST := engine/Cargo.toml

.PHONY: rust

rust:
	$(CARGO) build --manifest-path $(RUST_MANIFEST) --locked --release
```

Create `internal/mesh/native_abi.go`:

```go
//go:build darwin && cgo

package mesh

/*
#cgo CFLAGS: -I${SRCDIR}/../../engine/include
#cgo LDFLAGS: ${SRCDIR}/../../engine/target/release/libmcgo_mesh.a
#include "mcgo_engine.h"
*/
import "C"

const nativeABIVersionCurrent = uint32(C.MCGO_ENGINE_ABI_VERSION)

func nativeABIVersion() uint32 {
	return uint32(C.mcgo_engine_abi_version())
}
```

- [ ] **Step 6: Build and run GREEN**

Run:

```bash
make rust
cargo fmt --manifest-path engine/Cargo.toml --check
cargo clippy --manifest-path engine/Cargo.toml --workspace --all-targets -- -D warnings
cargo test --manifest-path engine/Cargo.toml --workspace --locked
go test ./internal/mesh -run '^TestNativeABIVersionMatchesGo$' -count=1
```

Expected: all PASS; `git status --short` must not list `engine/target`.

- [ ] **Step 7: Commit the ABI identity slice**

```bash
git add .gitignore Makefile engine internal/mesh/native_abi.go internal/mesh/native_abi_test.go openspec/changes/m4p-rust-engine-mesh/tasks.md
git diff --cached --check
git commit -m "build: 建立 Rust 网格 ABI 链接"
```

---

### Task 3: 定义 registry snapshot 与版本化 neighborhood 输入

**Files:**
- Create: `engine/crates/mcgo_mesh/src/input.rs`
- Create: `internal/mesh/registry.go`
- Create: `internal/mesh/native_input.go`
- Create: `internal/mesh/native_input_test.go`
- Modify: `engine/crates/mcgo_mesh/src/lib.rs`
- Modify: `engine/crates/mcgo_mesh/src/ffi.rs`
- Modify: `engine/include/mcgo_engine.h`
- Modify: `internal/mesh/native_abi.go`
- Modify: `internal/mesh/greedy.go`
- Modify: `internal/assets/blocks.go`
- Modify: `internal/assets/blocks_test.go`
- Modify: mesh test registry declarations in `internal/mesh/*_test.go`
- Modify: `openspec/changes/m4p-rust-engine-mesh/tasks.md`

**Interfaces:**
- Consumes: Task 2 ABI identity and static library.
- Produces:
  - Go `type RegistryReader`, `type Registry`, `type RegistrySnapshot`, `type BlockProperties`.
  - Go `func BuildRegistrySnapshot(ids []world.BlockID, reader RegistryReader) (RegistrySnapshot, error)`.
  - Go `func encodeNativeInput(dst []byte, n *world.Neighborhood, snapshot RegistrySnapshot) (int, error)`.
  - C/Rust `mcgo_mesh_section(...)` returning status and initially zero quads for valid input.

- [ ] **Step 1: Write RED snapshot tests**

Create `internal/mesh/native_input_test.go` with these exact tests:

```go
package mesh

func TestBuildRegistrySnapshotSortsAndFreezesVisibility(t *testing.T) {
	snapshot, err := BuildRegistrySnapshot(
		[]world.BlockID{core.StoneID, core.AirID, core.GlassID},
		internalTestRegistry{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := []world.BlockID{snapshot.Blocks[0].ID, snapshot.Blocks[1].ID, snapshot.Blocks[2].ID};
		!slices.Equal(got, []world.BlockID{core.AirID, core.StoneID, core.GlassID}) {
		t.Fatalf("snapshot IDs=%v", got)
	}
	if !snapshot.FaceVisible(core.StoneID, core.AirID) {
		t.Fatal("stone -> air 应可见")
	}
}

func TestBuildRegistrySnapshotRejectsDuplicateIDs(t *testing.T) {
	_, err := BuildRegistrySnapshot([]world.BlockID{core.AirID, core.AirID}, internalTestRegistry{})
	if err == nil {
		t.Fatal("重复 block ID 未被拒绝")
	}
}

func TestNativeInputValidAirNeighborhoodReturnsZeroQuads(t *testing.T) {
	n := fullyLoadedAirNeighborhood()
	status, count := callNativeForTest(t, n, (internalTestRegistry{}).MeshSnapshot())
	if status != nativeStatusOK || count != 0 {
		t.Fatalf("status=%v count=%d，想要 OK/0", status, count)
	}
}
```

Add `MeshSnapshot() RegistrySnapshot` to every mesh test registry; use `BuildRegistrySnapshot` with the exact IDs used by that registry. Registries that override embedded behavior must override the snapshot too: `overbrightRegistry` publishes emission 16, `nonOpaqueBlockRegistry` publishes its custom opacity, and `countingRegistry.MeshSnapshot` panics so the uniform-Air fast-path test proves the method was never reached. `materialCallRegistry` may inherit `assets.Registry` because its override only counts and returns the same material.

- [ ] **Step 2: Run RED**

Run:

```bash
go test ./internal/mesh -run 'RegistrySnapshot|NativeInput' -count=1
```

Expected: compile FAIL for missing snapshot/input/status types.

- [ ] **Step 3: Implement immutable Go snapshot types**

Create `internal/mesh/registry.go` with these signatures:

```go
type RegistryReader interface {
	Opaque(world.BlockID) bool
	FaceVisible(id world.BlockID, adjacent world.BlockID) bool
	Material(id world.BlockID, face Face) uint16
	Emission(world.BlockID) uint8
}

type Registry interface {
	RegistryReader
	MeshSnapshot() RegistrySnapshot
}

type BlockProperties struct {
	ID        world.BlockID
	Opaque    bool
	Emission  uint8
	Materials [6]uint16
}

type RegistrySnapshot struct {
	Blocks     []BlockProperties
	Visibility []uint64
}
```

`BuildRegistrySnapshot` must copy and ascending-sort `ids`, reject duplicates, reject emission above 15, fill all six materials, and create a row-major bit matrix with `wordsPerRow := (len(ids)+63)/64`. `RegistrySnapshot.FaceVisible` binary-searches IDs and returns false for any absent ID.

- [ ] **Step 4: Make assets.Registry cache the production snapshot**

Add `meshSnapshot mesh.RegistrySnapshot` to `assets.Registry`. At the end of `NewRegistry`, build once from the complete ascending range `core.AirID..core.MossyCobblestoneID` and panic only if that static table is invalid:

```go
ids := make([]world.BlockID, 0, int(core.MossyCobblestoneID)+1)
for id := core.AirID; id <= core.MossyCobblestoneID; id++ {
	ids = append(ids, id)
}
snapshot, err := mesh.BuildRegistrySnapshot(ids, r)
if err != nil {
	panic("assets: 构建 mesh registry snapshot: " + err.Error())
}
r.meshSnapshot = snapshot
```

Implement `func (r *Registry) MeshSnapshot() mesh.RegistrySnapshot { return r.meshSnapshot }` and add a test comparing every cached opaque/emission/material/visibility value with the existing methods.

- [ ] **Step 5: Define one exact byte layout in Go and Rust**

Use this layout, with every integer little-endian:

```text
0..3      magic "MGM1"
4..7      sectionOriginY i32
8..9      registryCount u16
10..11    registryWordsPerRow u16
12..13    airID u16
14..15    barrierID u16
16..      27 * 4096 block IDs as u16; center occupies neighborhood slot [1][1][1]
...       9 height-presence bytes
...       9 * 256 height values as i16
...       registryCount entries: id u16, opaque u8, emission u8, material[6] u16
...       registryCount * registryWordsPerRow visibility words as u64
```

Missing neighbor sections encode every cell as `core.BarrierID`. Missing height maps retain zero bytes but their presence byte is 0. Reject nil neighborhood/center, section Y outside `0..core.SectionsPerChunk-1`, empty snapshot, a snapshot missing `core.AirID` or `core.BarrierID`, unsorted IDs, duplicate IDs and any length arithmetic overflow.

The 27 sections are encoded in `cx, cy, cz` order, and every section uses the existing `y, z, x` block index. The center slot always comes from `n.Center`; the other 26 slots come from `n.Around`. Encode height presence/maps in `cx, cz` order and `z, x` column order. Encode `sectionOriginY := core.MinY + n.SectionY*core.SectionSize` so Rust does not duplicate Go world-height constants.

Use these compile-time bounds in `native_input.go`:

```go
const (
	nativeNeighborhoodSections = 3 * 3 * 3
	nativeHeightColumns        = 3 * 3
	nativeRegistryEntryBytes   = 2 + 1 + 1 + 6*2
	nativeMaxRegistryEntries   = int(core.MossyCobblestoneID) + 1
	nativeMaxRegistryWords     = (nativeMaxRegistryEntries + 63) / 64
	nativeLightVolume          = 48 * 48 * 48
	nativeScratchPadding       = (4 - nativeLightVolume%4) % 4
	nativeScratchBytes         = nativeLightVolume + nativeScratchPadding + nativeLightVolume*4
	maxNativeQuads             = 6 * core.BlocksPerSection
	maxNativeInputBytes        = 16 +
		nativeNeighborhoodSections*core.BlocksPerSection*2 +
		nativeHeightColumns + nativeHeightColumns*core.SectionSize*core.SectionSize*2 +
		nativeMaxRegistryEntries*nativeRegistryEntryBytes +
		nativeMaxRegistryEntries*nativeMaxRegistryWords*8
)
```

Arbitrary `uint16` IDs are allowed inside that 27-entry count. A larger registry returns an input error instead of growing the per-worker buffer; this ceiling changes only with a later ABI revision when the production block registry grows. `callNativeForTest` supplies a `nativeScratchBytes` buffer and `maxNativeQuads` output, so the no-op entry already rejects null, undersized and misaligned pointers before Task 4 starts using them.

- [ ] **Step 6: Add safe Rust parsing and a no-op mesh entry**

`input.rs` must expose immutable views, never cast an unaligned byte slice:

```rust
pub(crate) struct MeshInput<'a> {
    pub section_origin_y: i32,
    pub air_id: u16,
    pub barrier_id: u16,
    pub blocks: &'a [u8],
    pub heights_present: &'a [u8],
    pub heights: &'a [u8],
    pub registry: RegistryView<'a>,
}

impl MeshInput<'_> {
    pub(crate) fn block(&self, x: i32, y: i32, z: i32) -> u16 {
        let Some((cx, lx)) = neighbor_cell(x) else { return self.barrier_id };
        let Some((cy, ly)) = neighbor_cell(y) else { return self.barrier_id };
        let Some((cz, lz)) = neighbor_cell(z) else { return self.barrier_id };
        let section = (cx * 3 + cy) * 3 + cz;
        let cell = (ly << 8) | (lz << 4) | lx;
        read_u16(self.blocks, (section * 4096 + cell) * 2)
    }

    pub(crate) fn sky_light(&self, x: i32, y: i32, z: i32) -> u8 {
        let Some((cx, lx)) = neighbor_cell(x) else { return 0 };
        if neighbor_cell(y).is_none() { return 0; }
        let Some((cz, lz)) = neighbor_cell(z) else { return 0 };
        let column = cx * 3 + cz;
        if self.heights_present[column] == 0 { return 0; }
        let highest = read_i16(self.heights, (column * 256 + (lz << 4) + lx) * 2);
        u8::from(self.section_origin_y + y > i32::from(highest)) * 15
    }
}
```

`neighbor_cell` accepts only `-16..=31`; `read_u16` and `read_i16` copy two bytes into `from_le_bytes` after `MeshInput::parse` has validated all slice lengths. `RegistryView` must binary-search raw entry IDs without heap allocation and read visibility bits by dense indices. An absent current or adjacent ID makes `face_visible` false; absent IDs are non-opaque, emit zero and never select a material, matching `assets.Registry`.

Add to the header and `ffi.rs`:

```c
#define MCGO_STATUS_OK 0u
#define MCGO_STATUS_ABI_VERSION 1u
#define MCGO_STATUS_INVALID_ARGUMENT 2u
#define MCGO_STATUS_INPUT 3u
#define MCGO_STATUS_SCRATCH 4u
#define MCGO_STATUS_REGISTRY 5u
#define MCGO_STATUS_EMISSION 6u
#define MCGO_STATUS_OUTPUT_OVERFLOW 7u
#define MCGO_STATUS_QUEUE_OVERFLOW 8u
#define MCGO_STATUS_PANIC 9u

uint32_t mcgo_mesh_section(
    uint32_t abi_version,
    const uint8_t *input,
    size_t input_len,
    uint8_t *scratch,
    size_t scratch_len,
    uint64_t *output,
    size_t output_capacity,
    size_t *output_len);
```

Declare the same numeric constants in Rust and Go tests. For this task, a valid parsed input returns `MCGO_STATUS_OK` and `output_len=0`; invalid version, null pointer, short/long input or malformed registry returns its corresponding exact status and also sets `output_len=0`.

- [ ] **Step 7: Run focused GREEN and commit**

```bash
make rust
cargo test --manifest-path engine/Cargo.toml --workspace --locked
go test ./internal/assets ./internal/mesh -run 'Registry|NativeInput' -count=1
go test ./internal/mesh ./internal/assets -race -count=1
git add engine internal/mesh internal/assets openspec/changes/m4p-rust-engine-mesh/tasks.md
git diff --cached --check
git commit -m "feat: 定义 Rust 网格输入契约"
```

---

### Task 4: 在 Rust 中实现有界天空光与方块光

**Files:**
- Create: `engine/crates/mcgo_mesh/src/light.rs`
- Modify: `engine/crates/mcgo_mesh/src/lib.rs`
- Modify: `engine/crates/mcgo_mesh/src/input.rs`
- Modify: `engine/crates/mcgo_mesh/src/ffi.rs`
- Modify: `openspec/changes/m4p-rust-engine-mesh/tasks.md`

**Interfaces:**
- Consumes: `MeshInput::block`, `MeshInput::sky_light`, `RegistryView::{opaque, emission}`.
- Produces:
  - `pub(crate) const LIGHT_SIDE: usize = 48` and `LIGHT_VOLUME`.
  - `pub(crate) struct LightScratch<'a>` over caller-owned `levels` and `queue`.
  - `pub(crate) fn build_light(input: &MeshInput, registry: &RegistryView, scratch: &mut LightScratch) -> Result<(), MeshError>`.

- [ ] **Step 1: Write Rust RED tests before the port**

Add tests in `light.rs` for these exact contracts:

```rust
#[test]
fn single_block_light_reaches_fourteen_next_door() {
    let input = fixture_with_light_block(8, 8, 8);
    let mut storage = ScratchFixture::new();
    build_light(&input.mesh, &input.mesh.registry, &mut storage.light).unwrap();
    assert_eq!(storage.light.at(8, 8, 8) & 0x0f, 15);
    assert_eq!(storage.light.at(9, 8, 8) & 0x0f, 14);
}

#[test]
fn all_sources_fill_exact_queue_without_overflow() {
    let input = fixture_all_light_blocks();
    let mut storage = ScratchFixture::new();
    build_light(&input.mesh, &input.mesh.registry, &mut storage.light).unwrap();
    assert_eq!(storage.light.tail(), LIGHT_VOLUME);
}

#[test]
fn emission_sixteen_is_rejected() {
    let input = fixture_with_emission(16);
    let mut storage = ScratchFixture::new();
    assert_eq!(
        build_light(&input.mesh, &input.mesh.registry, &mut storage.light),
        Err(MeshError::EmissionOutOfRange),
    );
}
```

Also cover direct sky seed 15, one-step attenuation 14, opaque blocking, missing-height darkness, queue reuse between sky/block passes and coordinates outside `-16..31` returning 0.

- [ ] **Step 2: Run Rust RED**

```bash
cargo test --manifest-path engine/Cargo.toml -p mcgo_mesh light -- --nocapture
```

Expected: compile FAIL because `LightScratch` and `build_light` do not exist.

- [ ] **Step 3: Port the exact bounded algorithm**

Implement these constants and invariants:

```rust
pub(crate) const LIGHT_MIN: i32 = -16;
pub(crate) const LIGHT_SIDE: usize = 48;
pub(crate) const LIGHT_VOLUME: usize = LIGHT_SIDE * LIGHT_SIDE * LIGHT_SIDE;
const SKY_MASK: u8 = 0xf0;
const BLOCK_MASK: u8 = 0x0f;
const DIRECTIONS: [(i32, i32, i32); 6] = [
    (-1, 0, 0), (1, 0, 0),
    (0, -1, 0), (0, 1, 0),
    (0, 0, -1), (0, 0, 1),
];
```

`build_light` must:

1. zero all levels and reset queue;
2. scan x/y/z in current Go order for sky seeds, then breadth-first propagate only through non-opaque cells;
3. reset head/tail without reallocating;
4. scan x/y/z once for emission seeds, reject values above 15, then propagate only into cells equal to `input.air_id`;
5. return `MeshError::QueueOverflow` instead of writing beyond `LIGHT_VOLUME`.

Use index conversion identical to Go:

```rust
fn light_index(x: i32, y: i32, z: i32) -> usize {
    (((x - LIGHT_MIN) as usize * LIGHT_SIDE + (y - LIGHT_MIN) as usize) * LIGHT_SIDE)
        + (z - LIGHT_MIN) as usize
}
```

- [ ] **Step 4: Parse caller scratch without unaligned casts**

The Go scratch pointer is 8-byte aligned. `ffi.rs` must verify pointer alignment and exact minimum bytes before splitting it into:

```text
levels: LIGHT_VOLUME bytes
padding to 4-byte boundary
queue: LIGHT_VOLUME u32 values
```

Use `slice::from_raw_parts_mut` only after null, size, alignment and non-overlap checks. Registry lookup keeps using Task 3's binary search over the current 27-entry ceiling. No Rust allocation is allowed after entry.

- [ ] **Step 5: Run GREEN, clippy and commit**

```bash
cargo fmt --manifest-path engine/Cargo.toml --check
cargo clippy --manifest-path engine/Cargo.toml --workspace --all-targets -- -D warnings
cargo test --manifest-path engine/Cargo.toml -p mcgo_mesh light
make rust
go test ./internal/mesh -run '^TestNativeInputValidAirNeighborhoodReturnsZeroQuads$' -count=1
git add engine openspec/changes/m4p-rust-engine-mesh/tasks.md
git diff --cached --check
git commit -m "feat: 用 Rust 计算传播光照"
```

---

### Task 5: 在 Rust 中实现 AO、greedy mesh 与 packed quad

**Files:**
- Create: `engine/crates/mcgo_mesh/src/quad.rs`
- Create: `engine/crates/mcgo_mesh/src/greedy.rs`
- Modify: `engine/crates/mcgo_mesh/src/lib.rs`
- Modify: `engine/crates/mcgo_mesh/src/ffi.rs`
- Modify: `engine/include/mcgo_engine.h`
- Modify: `openspec/changes/m4p-rust-engine-mesh/tasks.md`

**Interfaces:**
- Consumes: Task 4 light levels and Task 3 input/registry views.
- Produces:
  - `Face` values `NegX..PosZ = 0..5`.
  - `Quad::pack() -> u64` matching Go `Quad.Pack`.
  - `mesh_section(input, light, output) -> Result<usize, MeshError>`.
  - completed C `mcgo_mesh_section` with atomic `output_len`.

- [ ] **Step 1: Write RED Rust contract tests**

Create `quad.rs` tests:

```rust
#[test]
fn pack_matches_go_layout() {
    let quad = Quad {
        x: 3, y: 4, z: 5, w: 6, h: 7,
        face: Face::PosY, material: 0x1234, ao: 0xa5, light: 0xbc,
    };
    let want = 3u64
        | 4u64 << 4 | 5u64 << 8
        | 5u64 << 12 | 6u64 << 16
        | (Face::PosY as u64) << 20
        | 0x1234u64 << 23 | 0xa5u64 << 39 | 0xbcu64 << 47;
    assert_eq!(quad.pack(), want);
}
```

Create `greedy.rs` tests for: isolated block = six unit quads; flat 16×16 top = one quad; two materials = two quads; stone/glass retains only stone internal boundary; missing neighbor blocks boundary face; AO corners match `0xff` and occluded fixtures; output capacity one below required returns `OutputOverflow` with no published count.

- [ ] **Step 2: Run Rust RED**

```bash
cargo test --manifest-path engine/Cargo.toml -p mcgo_mesh --lib -- --nocapture
```

Expected: compile FAIL for missing `Quad`/`mesh_section`.

- [ ] **Step 3: Implement Face and exact pack**

Declare `Face` with `#[repr(u8)]` and exact values `NegX..PosZ = 0..5`. Define `Quad` fields as `x, y, z, w, h: u8`, `face: Face`, `material: u16`, `ao, light: u8`. Use these fixed shifts and assert `w/h` are `1..=16` before packing:

```rust
const SHIFT_X: u32 = 0;
const SHIFT_Y: u32 = 4;
const SHIFT_Z: u32 = 8;
const SHIFT_W: u32 = 12;
const SHIFT_H: u32 = 16;
const SHIFT_FACE: u32 = 20;
const SHIFT_MATERIAL: u32 = 23;
const SHIFT_AO: u32 = 39;
const SHIFT_LIGHT: u32 = 47;
```

Do not add serde or bytemuck.

- [ ] **Step 4: Port AO and greedy ordering exactly**

Implement `MaskCell` as `Copy + Clone + Default + Eq + PartialEq` with fields `used`, `material`, `ao`, `light`. For faces `0..6`, use:

```rust
let axis = (face as usize) >> 1;
let u = (axis + 1) % 3;
let v = (axis + 2) % 3;
let step = if (face as u8) & 1 == 1 { 1 } else { -1 };
```

For each slice `0..16`, fill a `[MaskCell; 256]` in `vi` then `ui` order. Width grows along `ui`; height grows along `vi`; clear exactly the merged rectangle; append one packed quad before advancing `ui += width`. AO must preserve the Go corner order `(-1,-1),(1,-1),(1,1),(-1,1)` and 2-bit shifts.

Before all work, return zero output for a center section containing only AirID. Do not invoke registry decoding/light work on that fast path.

- [ ] **Step 5: Complete the panic-safe FFI**

Use:

```rust
let result = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
    let parsed = MeshInput::parse(input)?;
    let mut scratch = parse_scratch(scratch_ptr, scratch_len, &parsed.registry)?;
    build_light(&parsed, &parsed.registry, &mut scratch.light)?;
    mesh_section(&parsed, &scratch.light, output)
}));
```

Set `*output_len = 0` before any validation. Publish the successful count only after the whole operation returns `Ok(count)`. Map each `MeshError` to one stable `MCGO_STATUS_*` constant declared in both `ffi.rs` and `mcgo_engine.h`; map caught panic to `MCGO_STATUS_PANIC`.

- [ ] **Step 6: Run GREEN and commit**

```bash
cargo fmt --manifest-path engine/Cargo.toml --check
cargo clippy --manifest-path engine/Cargo.toml --workspace --all-targets -- -D warnings
cargo test --manifest-path engine/Cargo.toml --workspace --locked
make rust
go test ./internal/mesh -run 'NativeABI|NativeInput' -count=1
git add engine openspec/changes/m4p-rust-engine-mesh/tasks.md
git diff --cached --check
git commit -m "feat: 用 Rust 生成贪心网格"
```

---

### Task 6: 切换 Go 生产 MeshSection 并保留 test-only oracle

**Files:**
- Create: `internal/mesh/native.go`
- Create: `internal/mesh/greedy_oracle_test.go`
- Create: `internal/mesh/light_oracle_test.go`
- Modify: `internal/mesh/native_abi.go`
- Modify: `internal/mesh/native_input.go`
- Modify: `internal/mesh/greedy.go` (remove production algorithm after moving declarations)
- Modify: `internal/mesh/light.go` (remove production algorithm after moving declarations)
- Modify: `internal/mesh/light_internal_test.go`
- Modify: `internal/client/mesher_worker.go` only if the scratch constructor signature must remain exact; otherwise no diff allowed
- Modify: `cmd/gfxspike/main.go` only if required by the same signature rule; otherwise no diff allowed
- Modify: `openspec/changes/m4p-rust-engine-mesh/tasks.md`

**Interfaces:**
- Consumes: completed `mcgo_mesh_section`.
- Produces: existing `func NewLightScratch() *LightScratch` and `func MeshSection(*world.Neighborhood, Registry, *LightScratch) []Quad`, now backed only by Rust in production.

- [ ] **Step 1: Add a RED test proving the production path reaches Rust**

Expose a Rust test counter only under `cfg(test)` is not visible to Go staticlib, so use a behavior mutation guard instead. Add to `native_abi_test.go`:

```go
func TestMeshSectionRejectsNativeABIMismatch(t *testing.T) {
	n := fullyLoadedAirNeighborhood()
	scratch := NewLightScratch()
	defer func() {
		if recovered := recover(); recovered == nil || !strings.Contains(fmt.Sprint(recovered), "ABI") {
			t.Fatalf("recovered=%v，想要 ABI mismatch panic", recovered)
		}
	}()
	meshSectionNativeVersionForTest(n, internalTestRegistry{}, scratch, nativeABIVersionCurrent+1)
}
```

Run it before adding the Go wrapper; expected compile FAIL for `meshSectionNativeVersionForTest`.

- [ ] **Step 2: Move the old algorithms unchanged into test-only oracle files**

Move the old `maskCell`, `computeAO` and greedy function to `greedy_oracle_test.go` under `package mesh_test`, changing only names and required `mesh.` qualifiers:

```go
type oracleMaskCell struct {
	used  bool
	mat   uint16
	ao    uint8
	light uint8
}
func meshSectionGoOracle(n *world.Neighborhood, reg mesh.Registry, light *goLightScratch) []mesh.Quad
func computeAOOracle(n *world.Neighborhood, reg mesh.Registry, p [3]int, axis, u, v, step int) uint8
```

Move the old light constants/state/build methods to `light_oracle_test.go` under the same `mesh_test` package, renaming `LightScratch` to `goLightScratch` and `NewLightScratch` to `newGoLightScratch`. Do not change loop order, queries, panic text or capacity. Keeping both oracle files in the external test package lets parity tests reuse the existing deterministic fixtures without moving or duplicating them.

The production `greedy.go` may remain only if it owns `Registry` declarations not yet moved; move those declarations to `registry.go`, then delete empty files. No non-test Go file may contain the old light/greedy algorithm.

- [ ] **Step 3: Implement caller-owned production scratch**

Create `internal/mesh/native.go`:

```go
type LightScratch struct {
	input   []byte
	native  []uint64
	packed  [maxNativeQuads]uint64
}

func NewLightScratch() *LightScratch {
	return &LightScratch{
		input:  make([]byte, maxNativeInputBytes),
		native: make([]uint64, (nativeScratchBytes+7)/8),
	}
}
```

`native` uses `[]uint64` so its base pointer is at least 8-byte aligned. Keep the input and scratch sizes as compile-time expressions; a registry larger than the current 27-entry ABI ceiling fails before the call.

- [ ] **Step 4: Implement the single production call and status mapping**

`MeshSection` must:

1. panic on nil scratch as today;
2. preserve the uniform-Air early return before snapshot/encoding;
3. call `encodeNativeInput` into reusable input;
4. call `mcgo_mesh_section` exactly once;
5. map statuses to stable Chinese panic strings, including existing `mesh: 方块发光等级超过 15` and `mesh: 光照内部队列溢出` text;
6. allocate `[]Quad` at the successful count and fill it with `UnpackQuad(scratch.packed[i])`;
7. never run the Go oracle or retry Rust after failure.

Use one status table in `native_abi.go` and lock every text in `native_abi_test.go`:

| Status | Go panic text |
| --- | --- |
| `MCGO_STATUS_ABI_VERSION` | `mesh: native ABI 版本不匹配` |
| `MCGO_STATUS_INVALID_ARGUMENT` | `mesh: native 参数非法` |
| `MCGO_STATUS_INPUT` | `mesh: native 输入非法` |
| `MCGO_STATUS_SCRATCH` | `mesh: native scratch 非法` |
| `MCGO_STATUS_REGISTRY` | `mesh: registry snapshot 非法` |
| `MCGO_STATUS_EMISSION` | `mesh: 方块发光等级超过 15` |
| `MCGO_STATUS_OUTPUT_OVERFLOW` | `mesh: 四边形输出溢出` |
| `MCGO_STATUS_QUEUE_OVERFLOW` | `mesh: 光照内部队列溢出` |
| `MCGO_STATUS_PANIC` | `mesh: Rust 网格内部 panic` |

`meshSectionNativeVersionForTest` calls the same private function with an injected version; production `MeshSection` always uses `nativeABIVersionCurrent`.

- [ ] **Step 5: Adapt white-box light tests without weakening behavior**

Move Go-algorithm internal count assertions to tests of `goLightScratch`; add corresponding Rust unit assertions from Task 4. Keep public observable Go tests—light levels encoded in quads, missing neighbors, stable repeated build, cutout, emission panic—calling production `MeshSection`.

Run a declaration scan and fail if a non-test Go file still defines `buildSky`, `buildBlock`, `computeAO` or the greedy mask loops:

```bash
rg -n 'func .*buildSky|func .*buildBlock|func computeAO|type maskCell' internal/mesh --glob '*.go' --glob '!**/*_test.go'
```

Expected: no output.

- [ ] **Step 6: Run focused GREEN and commit**

```bash
make rust
go test ./internal/mesh -race -count=1
go test ./internal/client -run 'Mesher|Light|Dirty' -race -count=1
go test ./internal/render -run 'Mesh|Light|Cull' -race -count=1
go test ./cmd/gfxspike -count=1
go test ./internal/archcheck -count=1
gofmt -l internal/mesh internal/client/mesher_worker.go cmd/gfxspike/main.go
git add internal/mesh internal/client/mesher_worker.go cmd/gfxspike/main.go openspec/changes/m4p-rust-engine-mesh/tasks.md
git diff --cached --check
git commit -m "refactor: 默认使用 Rust 网格引擎"
```

Before staging, omit `internal/client/mesher_worker.go` and `cmd/gfxspike/main.go` if unchanged; staged scope must match actual diff.

---

### Task 7: 锁定跨语言逐位 parity、错误原子性与并发

**Files:**
- Create: `internal/mesh/native_parity_test.go`
- Modify: `internal/mesh/native_abi_test.go`
- Modify: Rust tests in `engine/crates/mcgo_mesh/src/{ffi,input,light,greedy,quad}.rs`
- Modify: `openspec/changes/m4p-rust-engine-mesh/tasks.md`

**Interfaces:**
- Consumes: Rust production `MeshSection` and test-only `meshSectionGoOracle`.
- Produces: exact parity corpus, malformed-input fuzz target and independent-scratch concurrency proof.

- [ ] **Step 1: Write fixed-corpus parity tests and observe any mismatch RED**

Create helper:

```go
func assertNativeOracleParity(t *testing.T, n *world.Neighborhood, reg mesh.Registry) {
	t.Helper()
	want := meshSectionGoOracle(n, reg, newGoLightScratch())
	got := mesh.MeshSection(n, reg, mesh.NewLightScratch())
	if len(got) != len(want) {
		t.Fatalf("quad count=%d，oracle=%d", len(got), len(want))
	}
	for i := range want {
		if got[i].Pack() != want[i].Pack() {
			t.Fatalf("quad[%d]=%#016x，oracle=%#016x\ngot=%+v\nwant=%+v",
				i, got[i].Pack(), want[i].Pack(), got[i], want[i])
		}
	}
}
```

Place `native_parity_test.go` in `package mesh_test`, use `mesh.Registry`, `mesh.MeshSection` and `mesh.NewLightScratch` in the helper, and reuse the existing `testRegistry`, `solidNeighbors`, `slabNeighbors` and light-world builders. Keep raw C ABI status, malformed-input and fuzz helpers in the existing `package mesh` `native_abi_test.go`, where unexported bridge symbols are visible.

Call it for every existing deterministic fixture: empty, isolated, full, flat slab, split material, glass/leaves, unknown ID, sky edge, block-light room, missing neighbor and world-height boundary.

Expected on first run: either PASS or a real Rust port mismatch. If mismatched, stop and fix Rust—not oracle or expected data—before proceeding.

- [ ] **Step 2: Add deterministic randomized parity**

Use `rand.New(rand.NewSource(0x4d3450))`. Generate exactly 64 neighborhoods; each of 27 sections is independently missing with probability 1/8, otherwise contains a deterministic mix of registered IDs; generate nine presence bits and heights within `core.MinY-1..core.MaxY-1`. Always preserve a non-nil center. Run parity for each seed index and include the index in failures.

Do not use wall clock, global randomness or `testing/quick` defaults.

- [ ] **Step 3: Add concurrent independent-scratch parity**

Run eight goroutines, each with its own `LightScratch`, over the same immutable neighborhood for 100 iterations. Compare every packed sequence with one frozen oracle and collect errors through a buffered channel. The test must run under `-race`; do not share a `LightScratch` because the API explicitly declares it worker-owned.

- [ ] **Step 4: Add malformed ABI fuzz seeds**

Add:

```go
func FuzzNativeMeshRejectsMalformedInput(f *testing.F) {
	valid := encodeValidInputForFuzz(f)
	f.Add([]byte{})
	f.Add([]byte("MGM1"))
	f.Add(valid[:len(valid)-1])
	f.Add(valid)
	f.Fuzz(func(t *testing.T, input []byte) {
		status, count := callNativeRawForTest(input, nativeABIVersionCurrent)
		if status != nativeStatusOK && count != 0 {
			t.Fatalf("status=%v published partial count=%d", status, count)
		}
	})
}
```

The fuzz target validates memory safety and atomic output only; it must not reinterpret arbitrary malformed bytes as a Go `Neighborhood` or call the oracle.

- [ ] **Step 5: Kill three required mutations**

Temporarily apply each mutation, run the named test to prove RED, then restore production code before continuing:

1. Rust block-light attenuation uses `current` instead of `current-1` → fixed light parity RED.
2. Rust greedy face loop reverses `0..6` → packed ordering parity RED.
3. FFI sets `output_len` before mesh completes → output-overflow atomicity test RED.

Record commands and failure lines in the ignored task report; do not commit mutation edits.

- [ ] **Step 6: Run all parity gates and commit**

```bash
make rust
cargo test --manifest-path engine/Cargo.toml --workspace --locked
go test ./internal/mesh -run 'Parity|Native|Light|Mesh' -race -count=1
go test ./internal/mesh -run '^$' -fuzz FuzzNativeMeshRejectsMalformedInput -fuzztime=10s
git add engine/crates/mcgo_mesh/src internal/mesh/*_test.go openspec/changes/m4p-rust-engine-mesh/tasks.md
git diff --cached --check
git commit -m "test: 锁定 Rust 与 Go 网格一致性"
```

---

### Task 8: 统一 Make、CI、Hook 与开发文档

**Files:**
- Modify: `Makefile`
- Modify: `.github/workflows/ci.yml`
- Modify: `scripts/agent-hooks/guard.mjs`
- Modify: `scripts/agent-hooks/guard.test.mjs`
- Modify: `AGENTS.md`
- Modify: `CLAUDE.md`
- Modify: `README.md`
- Modify: `openspec/config.yaml`
- Modify: `openspec/changes/m4p-rust-engine-mesh/tasks.md`

**Interfaces:**
- Consumes: `make rust` and completed Rust production path.
- Produces: clean-checkout `make build`, `make test`, `make test-race`; shared Hook Rust routing; CI Rust-first gates.

- [ ] **Step 1: Write RED Hook routing tests**

Export a pure helper `rustValidationRequired(paths)` and add:

```js
test("requires Rust validation for engine and native mesh changes", () => {
  assert.equal(rustValidationRequired(["engine/crates/mcgo_mesh/src/light.rs"]), true);
  assert.equal(rustValidationRequired(["internal/mesh/native.go"]), true);
  assert.equal(rustValidationRequired(["internal/server/session_ingress.go"]), false);
});

test("finds cargo through the login shell when PATH is incomplete", () => {
  const calls = [];
  const spawn = (command, argumentsList) => {
    calls.push([command, argumentsList]);
    if (
      command === "cargo" ||
      (command.endsWith("/cargo") && command !== "/toolchain/bin/cargo")
    ) {
      return { error: Object.assign(new Error("spawnSync cargo ENOENT"), { code: "ENOENT" }) };
    }
    if (command === "/bin/zsh") {
      return { status: 0, stdout: "/toolchain/bin/cargo\n" };
    }
    return { status: 0, stdout: "" };
  };

  const result = runCommand("cargo", ["fmt", "--check"], 30_000, spawn, {
    SHELL: "/bin/zsh",
    PATH: "/usr/bin:/bin",
  });

  assert.equal(result.status, 0);
  assert.deepEqual(calls[0], ["cargo", ["fmt", "--check"]]);
  assert.deepEqual(calls.at(-2), ["/bin/zsh", ["-lc", "command -v cargo"]]);
  assert.deepEqual(calls.at(-1), ["/toolchain/bin/cargo", ["fmt", "--check"]]);
});
```

Run `node --test scripts/agent-hooks/guard.test.mjs`; expected FAIL for missing helper/cargo route.

- [ ] **Step 2: Complete canonical Make targets**

Use prerequisites, not duplicated recipes:

```make
.PHONY: rust rust-check test-race

rust:
	$(CARGO) build --manifest-path $(RUST_MANIFEST) --locked --release

rust-check:
	$(CARGO) fmt --manifest-path $(RUST_MANIFEST) --check
	$(CARGO) clippy --manifest-path $(RUST_MANIFEST) --workspace --all-targets -- -D warnings
	$(CARGO) test --manifest-path $(RUST_MANIFEST) --workspace --locked

run build test test-multiplayer bench-multiplayer visual-check visual-update: rust
test-race: rust
	$(GO) test ./... -race
```

Keep every existing Go recipe after the prerequisite line. Add `rust`, `rust-check`, and `test-race` to help text. `fmt` must run both Cargo fmt and current Go formatting, without touching `engine/target`.

Tighten the existing `archcheck` shell assertion to include `internal/mesh`:

```make
	test -z "$$($(GO) list -deps ./cmd/mcgod | rg 'internal/(client|mesh|render|gfx)|glfw|webgpu|x/image/font')"
```

- [ ] **Step 3: Make shared Hook build Rust before Go validation**

`rustValidationRequired` returns true for `engine/**/*.rs`, Cargo files, `engine/include/mcgo_engine.h`, `internal/mesh/native*.go`, `internal/mesh/registry.go`, Makefile, or CI changes.

In Stop handling:

1. If any changed Go file or `rustValidationRequired(paths)`, run `make rust` before archcheck/test/vet.
2. If `rustValidationRequired(paths)`, also run `cargo fmt --check`, clippy `-D warnings`, and cargo test.
3. If Rust changed without Go files, still run `go test ./internal/mesh ./internal/client -race -count=1` after `make rust`.
4. Preserve all existing OpenSpec, gofmt, archcheck and vet checks; do not add an escape variable.

Increase no timeout unless a measured clean Cargo build exceeds the existing 600-second Stop limit.

- [ ] **Step 4: Update CI before every direct Go command**

After checkout/setup-go/setup-node, add:

```yaml
      - name: Rust 工具链身份
        run: |
          rustup show active-toolchain
          rustc --version
          cargo --version

      - name: Rust 格式、静态检查与单测
        run: make rust-check

      - name: 构建 Rust static library
        run: make rust
```

Do not commit `engine/target` as an artifact and do not cache it in M4P. Keep all existing Go CI steps and the `CGO_ENABLED=0 GOOS=linux go build ./cmd/mcgod` step.

In that headless-server step, append the same dependency assertion used by `make archcheck`, including `internal/mesh`.

- [ ] **Step 5: Update documentation without claiming later stages**

`AGENTS.md` and identical `CLAUDE.md` must state:

- current baseline M4P only moves mesh/light to Rust;
- Rust 1.97.1 is pinned;
- Go still owns app/world/sim/network/storage/render;
- clean checkout uses Make targets before direct Go focused commands;
- only `internal/mesh` touches the new native ABI in this stage;
- no production Go fallback.

`README.md` prerequisites add Rust 1.97.1/rustup and common commands add `make rust-check`, `make test-race`. `openspec/config.yaml` updates current implementation context but must not claim final Rust host or Go rules library is already implemented.

Run `cmp AGENTS.md CLAUDE.md`; expected exit 0.

- [ ] **Step 6: Run clean workflow gates and commit**

Move the existing `engine/target` directory to a unique temporary backup, then run canonical commands to prove clean rebuild; restore nothing because Cargo reproduces it:

```bash
M4P_RUST_TARGET_BACKUP=$(mktemp -d /private/tmp/mcgo-m4p-rust-target.XXXXXX)
mv engine/target "$M4P_RUST_TARGET_BACKUP/target"
make rust-check
make test
make test-race
node --test scripts/agent-hooks/guard.test.mjs
CGO_ENABLED=0 GOOS=linux go build ./cmd/mcgod
cmp AGENTS.md CLAUDE.md
openspec validate --all --strict --no-interactive
git diff --check
```

Use an explicit temporary path validated as absent before `mv`; never use `git clean` or broad deletion.

Commit:

```bash
git add Makefile .github/workflows/ci.yml scripts/agent-hooks AGENTS.md CLAUDE.md README.md openspec/config.yaml openspec/changes/m4p-rust-engine-mesh/tasks.md
git diff --cached --check
git commit -m "build: 统一 Rust 与 Go 验证入口"
```

---

### Task 9: 下游保真、全仓门禁与独立评审

**Files:**
- Modify only after all gates and review: `openspec/changes/m4p-rust-engine-mesh/tasks.md`
- Report only, ignored: `.superpowers/sdd/2026-08-12-m4p-rust-engine-mesh/task-9-report.md`

**Interfaces:**
- Consumes: Tasks 1–8 completed commits.
- Produces: independently reviewed M4P change with every OpenSpec task checked; no implementation diff in final audit commit.

- [ ] **Step 1: Freeze final identity and scope**

Record:

```bash
git status --short --branch
git log --oneline --decorate 2346dede27c5322045ea8c0bd467736e182a50a7..HEAD
git diff --name-status 2346dede27c5322045ea8c0bd467736e182a50a7..HEAD
git diff --check 2346dede27c5322045ea8c0bd467736e182a50a7..HEAD
find engine -type f -not -path '*/target/*' -print | sort
```

Expected: only planned Rust/mesh/build/spec/doc files; no native artifact, golden, fixture, protocol, storage or baseline diff.

- [ ] **Step 2: Run fresh Rust, focused race, fuzz and architecture gates**

```bash
make rust-check
make rust
go test ./internal/mesh ./internal/assets ./internal/client ./internal/render -race -count=1
go test ./cmd/mcgo -run 'Capture|BlockLightRoom|MaterialsShowcase' -race -count=1
go test ./internal/mesh -run '^$' -fuzz FuzzNativeMeshRejectsMalformedInput -fuzztime=10s
go test ./internal/archcheck -count=1
CGO_ENABLED=0 GOOS=linux go build ./cmd/mcgod
test -z "$(CGO_ENABLED=0 GOOS=linux go list -deps ./cmd/mcgod | rg 'internal/(client|mesh|render|gfx)|glfw|webgpu|x/image/font')"
```

Expected: all exit 0. Any Rust panic, parity mismatch, output overflow or Linux server link to Rust is a blocker.

- [ ] **Step 3: Run visual and performance record gates**

```bash
M4P_VISUAL_OUT=$(mktemp -d /private/tmp/mcgo-m4p-rust-mesh-visual.XXXXXX)
VISUAL_OUT="$M4P_VISUAL_OUT" make visual-check
go test ./internal/mesh -run '^$' -bench BenchmarkMeshTerrainSection -benchmem -count=5
go test ./internal/render -run '^$' -bench 'Mesh|Light' -benchmem -count=3
```

Expected: all 10 tracked scenes pass without `--update-golden`; benchmark output is recorded with commit/toolchain identity but numeric differences do not fail. Any extra allocation must be explained in the report; real overflow or missing report fields fail.

- [ ] **Step 4: Run final shared repository gates**

```bash
make test-race
go vet ./...
gofmt -l .
cargo fmt --manifest-path engine/Cargo.toml --check
openspec validate --all --strict --no-interactive
git diff --check
cmp AGENTS.md CLAUDE.md
```

Expected: every command exit 0 and `gofmt -l .` prints nothing.

- [ ] **Step 5: Request independent specification and quality review**

Reviewer must inspect the commit object from `2346ded..HEAD`, not unrelated worktree state, and return:

- zero Critical/Important findings;
- explicit ABI ownership verdict;
- proof that production has no Go fallback;
- parity/mutation-quality verdict;
- build/CI/Hook clean-checkout verdict;
- artifact/protocol/storage/visual preservation verdict.

Fix any Critical/Important finding in a separate commit, rerun the affected focused gates, and request scoped re-review. Do not mark Task 9 complete before approval.

- [ ] **Step 6: Mark tasks complete and commit only audit metadata**

After approval, change all remaining M4P checkboxes to `[x]`, append real commit hashes and command results to the ignored report, then stage only `tasks.md`:

```bash
openspec validate --all --strict --no-interactive
git add openspec/changes/m4p-rust-engine-mesh/tasks.md
git diff --cached --check
git commit -m "docs: 完成 M4P Rust 网格迁移"
git status --short --branch
```

Expected: final commit contains only task completion metadata; tracked worktree is clean. Keep the OpenSpec change active unless the user separately requests archive.
