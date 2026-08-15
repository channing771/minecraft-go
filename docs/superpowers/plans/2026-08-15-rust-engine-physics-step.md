# rust-engine-physics-step Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把物理 tick 积分（加速度/摩擦/重力/跳跃/速度裁剪）从 Go 迁入 Rust `mornlea_engine`，生产 `physics.Step` 变为单次 `mornlea_physics_step` native 调用，行为与现有实现逐位一致。

**Architecture:** 新增 MGP1 physics step ABI（128 字节 header + 每 cell 196 字节 → 32 字节输出）；三角函数留在 Go（yaw_sin/yaw_cos 传入）；Go 计算位移凸包 sweep bounds 构建 prism，Rust 积分后自检位移在界内；`mornlea_collision_resolve` 保留为测试专用差分入口。旧 Go 积分逻辑移入 `_test.go` 作逐位奇偶 oracle。

**Tech Stack:** Go 1.26（cgo darwin/linux）、Rust 1.97.1（engine/rust-toolchain.toml，edition 2024）、`github.com/go-gl/mathgl v1.2.0`（mgl32）。

**Spec:** `docs/superpowers/specs/2026-08-15-rust-engine-physics-step-design.md`（本计划的规格来源；执行者与评审者同时阅读两者）

## Global Constraints

- Go 1.26；Rust 固定 1.97.1，cargo 命令必须带 `--locked`；Rust 侧跑 `make rust` / `make rust-check`（cargo fmt/clippy/test 全通过）。
- 代码注释与 GoDoc 必须用中文；Go 标识符、wire magic（`MGP1`/`MGC1`）、协议字段名保留英文。
- 只有 `internal/nativeabi` 接触 engine C ABI；生产路径无 Go fallback；旧 Go 积分只能存在于 `_test.go`。
- 差分门禁逐位（`math.Float32bits` 比较），不得改为容差比较。
- 协议 v16、玩家 schema v6、区块 schema v8、世界 metadata v2、`companions.ai` v1、benchmark scenario v16 全部不变；M2 v15/M5 v14 基线保持原字节。
- 自动测试不得启动或聚焦前台游戏窗口。
- 每任务结束：`gofmt -l .` 无输出；受影响包 `go test -race -count=1` 通过；Hook 失败修根因，不得绕过。
- 提交信息用中文 + conventional 前缀（docs/refactor/feat/test），与仓库既有风格一致。

## 文件结构

- 新建 `openspec/changes/rust-engine-physics-step/`：proposal.md、design.md、specs/rust-engine-physics-step/spec.md、tasks.md（Task 1）。
- 新建 `engine/crates/mornlea_engine/src/step.rs`：输入解析/校验、积分镜像、输出编码、Rust 单测（Task 3/4/5）。
- 修改 `engine/crates/mornlea_engine/src/collision.rs`：拆分 `resolve_collision_parts`（Task 2）。
- 修改 `engine/crates/mornlea_engine/src/ffi.rs`：`mornlea_physics_step` 入口 + 校验 + `ABI_VERSION` 1→2（Task 5）。
- 修改 `engine/crates/mornlea_engine/src/lib.rs`：`mod step;`（Task 3）。
- 修改 `engine/include/mornlea_engine.h`：声明 + `MORNLEA_ENGINE_ABI_VERSION` 1→2（Task 5）。
- 修改 `internal/nativeabi/native.go`：`PhysicsStep` 绑定 + pragma + 文案映射（Task 6）。
- 修改 `internal/nativeabi/native_test.go`：pragma 清单 + 原子失败测试（Task 6）。
- 新建 `internal/physics/step.go`：生产 `Step`、`stepSweepBounds`、`movementTargetFromYaw`、`encodeStepInput`、`decodeStepOutput`（Task 8）。
- 修改 `internal/physics/motion.go`：删除积分主体，保留 `moveToward`/`validate`/`finiteVector`（Task 8）。
- 修改 `internal/physics/collision.go`：删除生产 resolve/encode/decode，prism 泛化为 `stepPrismFor`（Task 8）。
- 新建 `internal/physics/motion_oracle_test.go`：旧积分 oracle + `oracleStep`（Task 7）。
- 修改 `internal/physics/collision_native_test.go`：差分断言改用 `oracleStep`（Task 7）。
- 新建 `internal/physics/step_native_test.go`：布局测试 + step 级差分语料（Task 8/9）。
- 修改 `docs/notes/progress.md`、`AGENTS.md`、`CLAUDE.md`、`openspec/config.yaml`：基线段落（Task 10）。

---

### Task 1: OpenSpec change `rust-engine-physics-step` 脚手架

**Files:**
- Create: `openspec/changes/rust-engine-physics-step/proposal.md`
- Create: `openspec/changes/rust-engine-physics-step/design.md`
- Create: `openspec/changes/rust-engine-physics-step/specs/rust-engine-physics-step/spec.md`
- Create: `openspec/changes/rust-engine-physics-step/tasks.md`

**Interfaces:**
- Consumes: `docs/superpowers/specs/2026-08-15-rust-engine-physics-step-design.md`（已批准）。
- Produces: active change 骨架；后续任务每完成一个勾选 tasks.md 对应条目。

- [ ] **Step 1: 写 proposal.md**

```markdown
# Change: rust-engine-physics-step

## Why

物理 tick 积分（加速度/摩擦/重力/跳跃/速度裁剪）仍在 Go。上一波已把 collision resolver
与 raycast DDA 迁入 Rust `mornlea_engine`；本变更把积分也迁入，使物理核心整体位于
engine，Go 只保留领域 API 与编码。行为必须与现有实现逐位一致（float32 bit 级）。

## What Changes

- Rust：新增 `mornlea_physics_step`（magic `MGP1`，128 字节 header + 每 cell 196 字节，
  输出固定 32 字节）；engine ABI version 1→2；`mornlea_collision_resolve` 保留为测试专用入口。
- Go：`physics.Step` 改为薄封装（输入校验、tunables 快照、yaw 三角、位移凸包 sweep
  bounds、prism 构建与编码、一次 native 调用、输出解码）；旧积分逻辑移入 `_test.go` oracle。
- 行为零变化：协议 v16、存档 schema、benchmark scenario v16 均不变。

## Impact

- 受影响包：internal/physics、internal/nativeabi、engine/crates/mornlea_engine、engine/include。
- 兼容性：engine ABI +1，Go binary 与 `libmornlea_engine.so` 仍为不可跨版本混装的
  release unit（`$ORIGIN` 约定不变）；既有 mesh/raycast/collision ABI 不动。
- 性能：每 tick 仍一次 native 调用；header 增 80 字节、输出 32 字节，benchmark/perfcheck
  数值只记录，不改变退出状态。
- 并发：Step 保持纯函数快照语义；SetTunables 原子换指针语义不变。

## 非目标

不改动碰撞 kernel 行为；不迁移世界生成、存档编解码、光照传播；不引入生产 Go fallback。
```

- [ ] **Step 2: 写 design.md（OpenSpec 精简版）**

```markdown
# Design: rust-engine-physics-step

完整设计见 `docs/superpowers/specs/2026-08-15-rust-engine-physics-step-design.md`。要点：

- ABI：magic `MGP1`、layout v1、128 字节 header（position/velocity、on_ground/jump/
  move_x/move_z、yaw_sin/yaw_cos、fixed_delta_seconds、tunables×8、sweep bounds×6、
  prism origin/dimensions）+ 每 cell 196 字节（沿用 MGC1 cell 格式）；输出 32 字节
  （position/velocity、clipped mask、on_ground/used_step/hit_unknown、reserved）。
- 三角函数留在 Go：`math.Sin/Cos` 算 yaw_sin/yaw_cos 传入，规避跨语言 libm 末位差异。
- 位移凸包 sweep bounds：水平轴 `[min(0,v,t)·dt, max(0,v,t)·dt]`，垂直轴按
  jump/fallen/−terminal 分支取凸包；Rust 积分后自检位移在界内，违约返回 StatusInput。
- 运算顺序逐条镜像 Go：mgl32 `Len` 为 f32 平方和 → f64 Sqrt → f32；`Normalize` 为
  `1/Len` 再乘分量；`max`/`min` 符号零语义（+0 胜出）两侧一致；sqrt 正确舍入无双重舍入。
- collision.rs 拆分 `resolve_collision_parts`，cells 切片零拷贝复用输入尾部。
- 依赖方向不变：physics → nativeabi → C ABI；archcheck 无需登记新边。
- 回退：单 PR revert 恢复旧布局；oracle 即旧实现副本，可随时移回生产。
```

- [ ] **Step 3: 写 delta spec**

```markdown
# rust-engine-physics-step Delta Spec

## ADDED Requirements

### Requirement: 物理 tick 积分由 Rust engine 独占生产

物理固定步的积分（移动目标、加速/摩擦、跳跃、重力、终端速度裁剪）与碰撞解析、速度裁剪
在单次 `mornlea_physics_step` native 调用内完成；Go 生产路径不得包含积分实现，旧 Go
实现只允许存在于测试 oracle。

#### Scenario: 生产 Step 与 Go oracle 逐位一致

- GIVEN 任意合法 State、Input、CollisionSource 与运行时 Tunables
- WHEN 调用 physics.Step
- THEN 结果 State（Position/Velocity/OnGround）、UsedStep、HitUnknown 与测试内 Go
  oracle 实现逐位一致（float32 bit 级）

#### Scenario: 对角输入无斜向加速增益

- GIVEN 地面玩家，OnGround=true，MoveX=1，MoveZ=1，默认 tunables
- WHEN 执行一个固定步
- THEN 水平速度模长 ≈ 2.0（acceleration×dt，误差 < 1e-5）

#### Scenario: 跳跃与重力使用固定常量

- GIVEN 地面玩家，Jump=true，默认 tunables
- WHEN 执行一个固定步
- THEN 垂直速度等于 JumpSpeed 且 OnGround=false
- GIVEN 空中玩家垂直速度 −78
- WHEN 执行一个固定步
- THEN 垂直速度不低于 −TerminalFallSpeed

### Requirement: 运行时 tunables 每步生效

Step 在入口取一次 ActiveTunables 快照并随调用传入 engine；SetTunables 之后的下一次
Step 使用新参数。

#### Scenario: SetTunables 后下一步生效

- GIVEN 任意合法状态与碰撞源
- WHEN SetTunables(增大 StepHeight) 后调用 physics.Step
- THEN 该步 prism 尺寸与结果反映新 StepHeight（4096-cell 上限用例保持通过）

### Requirement: sweep bounds 违约拒绝

Go 按位移凸包界构建 prism 并在输入中携带 sweep bounds；Rust 积分后自检位移落在界内，
违约返回 StatusInput，Go 以稳定中文 panic 文案报告，不得产出静默漂移结果。

#### Scenario: 位移越界被拒绝

- GIVEN 合法 state/input 但输入 sweep bounds 不含该步积分位移
- WHEN 调用 native physics_step
- THEN 返回 StatusInput，且输出缓冲不被修改

### Requirement: 碰撞差分入口保留

`mornlea_collision_resolve` 继续可用且行为不变，仅供测试差分；生产路径只调用
`mornlea_physics_step`。

#### Scenario: 碰撞差分测试继续通过

- GIVEN 现有碰撞级差分语料
- WHEN 调用 nativeabi.CollisionResolve
- THEN 与 Go 碰撞 oracle 逐位一致
```

- [ ] **Step 4: 写 tasks.md**

```markdown
# Tasks: rust-engine-physics-step

- [ ] 1. OpenSpec 脚手架 → openspec validate --all --strict --no-interactive
- [ ] 2. Rust collision.rs 拆分 resolve_collision_parts → cargo test --workspace --locked
- [ ] 3. Rust step.rs 输入解析与校验（含单测）→ cargo test --workspace --locked
- [ ] 4. Rust step.rs 积分镜像（含锚点单测）→ cargo test --workspace --locked
- [ ] 5. Rust ffi 入口 + C header + ABI v2 → make rust && cargo test --workspace --locked
- [ ] 6. Go nativeabi.PhysicsStep 绑定（含原子失败测试）→ go test ./internal/nativeabi -race -count=1
- [ ] 7. Go 积分 oracle 提取 + 差分断言重构 → go test ./internal/physics -race -count=1
- [ ] 8. Go 生产 Step 切 native（布局测试 TDD）→ go test ./internal/physics ./internal/nativeabi -race -count=1
- [ ] 9. step 级差分语料扩展 → go test ./internal/physics -race -count=1
- [ ] 10. 收尾验证与基线文档 → make rust-check; go test ./... -race; go vet ./...; gofmt -l .; openspec validate --all --strict --no-interactive
```

- [ ] **Step 5: 运行 OpenSpec 校验**

Run: `openspec validate --all --strict --no-interactive`
Expected: 全部通过（0 errors）。

- [ ] **Step 6: 提交**

```bash
git add openspec/changes/rust-engine-physics-step
git commit -m "docs: 建立 rust-engine-physics-step OpenSpec change"
```

---

### Task 2: Rust collision.rs 拆分 resolve_collision_parts

**Files:**
- Modify: `engine/crates/mornlea_engine/src/collision.rs:26-140`

**Interfaces:**
- Consumes: 现有 `CollisionInput`/`resolve_move`/`resolve_step_move`/`encode_result`（不变）。
- Produces: `pub(crate) fn resolve_collision_parts(position: [f32; 3], displacement: [f32; 3], began_grounded: bool, step_height: f32, origin: [i32; 3], dimensions: [u32; 3], cells: &[u8]) -> [u8; 16]`；`resolve_collision(bytes)` 保留为 64 字节 MGC1 入口的薄包装。Task 5 的 step.rs 依赖 `resolve_collision_parts`。

- [ ] **Step 1: 重构 CollisionInput 构造**

`engine/crates/mornlea_engine/src/collision.rs` 中把 `CollisionInput::decode` 改为两个构造函数（其余字段解析代码不变）：

```rust
impl<'a> CollisionInput<'a> {
    fn decode(bytes: &'a [u8]) -> Self {
        let position = [read_f32(bytes, 8), read_f32(bytes, 12), read_f32(bytes, 16)];
        let displacement = [read_f32(bytes, 20), read_f32(bytes, 24), read_f32(bytes, 28)];
        let began_grounded = bytes[32] == 1;
        let step_height = read_f32(bytes, COLLISION_STEP_HEIGHT_OFFSET);
        let origin = [read_i32(bytes, 40), read_i32(bytes, 44), read_i32(bytes, 48)];
        let dimensions = [read_u32(bytes, 52), read_u32(bytes, 56), read_u32(bytes, 60)];
        Self::from_parts(position, displacement, began_grounded, step_height, origin, dimensions, &bytes[HEADER_BYTES..])
    }

    fn from_parts(
        position: Vector,
        displacement: Vector,
        began_grounded: bool,
        step_height: f32,
        origin: [i32; 3],
        dimensions: [u32; 3],
        cells: &'a [u8],
    ) -> Self {
        Self {
            bytes: cells,
            position,
            displacement,
            began_grounded,
            step_height,
            origin,
            dimensions,
        }
    }
}
```

把 `cell` 方法的偏移从 header 相对改为 cells 相对（cells 切片从 0 开始）：

```rust
    fn cell(&self, position: [i32; 3]) -> Cell<'a> {
        // ...（x/y/z 与 origin/dimensions 的 debug_assert 保持不变）
        let offset = index * CELL_BYTES; // 原为 HEADER_BYTES + index * CELL_BYTES
        Cell {
            bytes: &self.bytes[offset..offset + CELL_BYTES],
            position,
        }
    }
```

- [ ] **Step 2: 拆分 resolve_collision_parts**

`resolve_collision(bytes)` 的算法主体（ordinary/step 判定与 `encode_result`，现第 122–140 行）原样保留，只把签名拆成：

```rust
pub(crate) fn resolve_collision(bytes: &[u8]) -> [u8; 16] {
    let input = CollisionInput::decode(bytes);
    resolve_collision_input(input)
}

pub(crate) fn resolve_collision_parts(
    position: Vector,
    displacement: Vector,
    began_grounded: bool,
    step_height: f32,
    origin: [i32; 3],
    dimensions: [u32; 3],
    cells: &[u8],
) -> [u8; 16] {
    let input = CollisionInput::from_parts(
        position, displacement, began_grounded, step_height, origin, dimensions, cells,
    );
    resolve_collision_input(input)
}

fn resolve_collision_input(input: CollisionInput<'_>) -> [u8; 16] {
    // 现有 122–140 行主体原样移入
}
```

- [ ] **Step 3: 运行 Rust 测试确认无行为变化**

Run: `cd engine && cargo test --workspace --locked`
Expected: PASS（既有 collision 单测与 ffi 测试全部通过；这是纯重构）。

- [ ] **Step 4: 提交**

```bash
git add engine/crates/mornlea_engine/src/collision.rs
git commit -m "refactor: 拆分 collision kernel 为 from_parts 视图"
```

---

### Task 3: Rust step.rs 输入解析与校验

**Files:**
- Create: `engine/crates/mornlea_engine/src/step.rs`
- Modify: `engine/crates/mornlea_engine/src/lib.rs`（加 `mod step;`）

**Interfaces:**
- Consumes: `ffi.rs` 的 `read_u32/read_i32/read_f32` 均为私有（`ffi.rs` 内 fn）——注意 step.rs 需要自己的读字节 helper（见 Step 1，复制同款三个小函数为 `step.rs` 私有函数），不能跨模块引用。
- Produces: `pub(crate) const STEP_HEADER_BYTES: usize = 128;`、`pub(crate) const STEP_OUTPUT_BYTES: usize = 32;`、`pub(crate) struct StepInput<'a>`、`pub(crate) fn step_input_is_valid(bytes: &[u8]) -> bool`、`pub(crate) fn integrate(...)`（Task 4）、`pub(crate) fn physics_step(bytes: &[u8]) -> Result<[u8; STEP_OUTPUT_BYTES], StepError>`（Task 5）。

- [ ] **Step 1: 写失败测试（step.rs 底部 `#[cfg(test)] mod tests`）**

```rust
const CELL_BYTES: usize = 196;

fn write_f32(bytes: &mut [u8], offset: usize, value: f32) {
    bytes[offset..offset + 4].copy_from_slice(&value.to_bits().to_le_bytes());
}

fn valid_step_bytes() -> Vec<u8> {
    let mut bytes = vec![0u8; STEP_HEADER_BYTES + CELL_BYTES];
    bytes[0..4].copy_from_slice(b"MGP1");
    bytes[4..8].copy_from_slice(&1u32.to_le_bytes());
    write_f32(&mut bytes, 8, 0.5);  // position x
    write_f32(&mut bytes, 12, 1.0); // position y
    write_f32(&mut bytes, 16, 0.5); // position z
    write_f32(&mut bytes, 20, 0.0); // velocity x
    write_f32(&mut bytes, 24, -1.6); // velocity y
    write_f32(&mut bytes, 28, 0.0); // velocity z
    bytes[32] = 1; // on_ground
    bytes[34] = 1; // move_x
    write_f32(&mut bytes, 36, 0.0);  // yaw_sin
    write_f32(&mut bytes, 40, 1.0);  // yaw_cos
    write_f32(&mut bytes, 44, 0.05); // fixed_delta_seconds
    for (index, value) in [0.6f32, 4.3, 40.0, 50.0, 8.0, 8.4, 32.0, 78.4]
        .iter()
        .enumerate()
    {
        write_f32(&mut bytes, 48 + index * 4, *value);
    }
    write_f32(&mut bytes, 80, 0.0);       // dx_min
    write_f32(&mut bytes, 84, 4.3 * 0.05); // dx_max
    write_f32(&mut bytes, 88, -1.6 * 0.05); // dy_min
    write_f32(&mut bytes, 92, 0.05);       // dy_max
    write_f32(&mut bytes, 96, 0.0);        // dz_min
    write_f32(&mut bytes, 100, 0.0);       // dz_max
    for index in 0..3 {
        bytes[104 + index * 4..108 + index * 4].copy_from_slice(&0u32.to_le_bytes()); // origin
        bytes[116 + index * 4..120 + index * 4].copy_from_slice(&1u32.to_le_bytes()); // dimensions
    }
    bytes[STEP_HEADER_BYTES] = 1; // cell loaded
    bytes
}

#[test]
fn accepts_valid_input() {
    assert!(step_input_is_valid(&valid_step_bytes()));
}

#[test]
fn rejects_bad_magic() {
    let mut bytes = valid_step_bytes();
    bytes[0] = b'X';
    assert!(!step_input_is_valid(&bytes));
}

#[test]
fn rejects_bad_layout() {
    let mut bytes = valid_step_bytes();
    bytes[4] = 0;
    assert!(!step_input_is_valid(&bytes));
}

#[test]
fn rejects_move_out_of_range() {
    let mut bytes = valid_step_bytes();
    bytes[34] = 2; // move_x = 2
    assert!(!step_input_is_valid(&bytes));
}

#[test]
fn rejects_non_finite_tunable() {
    let mut bytes = valid_step_bytes();
    write_f32(&mut bytes, 48, f32::NAN);
    assert!(!step_input_is_valid(&bytes));
}

#[test]
fn rejects_non_finite_sweep_bounds() {
    let mut bytes = valid_step_bytes();
    write_f32(&mut bytes, 84, f32::INFINITY);
    assert!(!step_input_is_valid(&bytes));
}

#[test]
fn rejects_inverted_sweep_bounds() {
    let mut bytes = valid_step_bytes();
    write_f32(&mut bytes, 84, -1.0); // dx_max < dx_min
    assert!(!step_input_is_valid(&bytes));
}

#[test]
fn rejects_wrong_length() {
    let mut bytes = valid_step_bytes();
    bytes.pop();
    assert!(!step_input_is_valid(&bytes));
}

#[test]
fn rejects_too_many_cells() {
    let mut bytes = valid_step_bytes();
    // dimensions 33×8×16 = 4224 > 4096
    for (index, dimension) in [33u32, 8, 16].iter().enumerate() {
        bytes[116 + index * 4..120 + index * 4].copy_from_slice(&dimension.to_le_bytes());
    }
    assert!(!step_input_is_valid(&bytes));
}

#[test]
fn rejects_invalid_cell() {
    let mut bytes = valid_step_bytes();
    bytes[STEP_HEADER_BYTES + 1] = 9; // cell count > 8
    assert!(!step_input_is_valid(&bytes));
}

#[test]
fn decodes_fields() {
    let bytes = valid_step_bytes();
    let input = StepInput::decode(&bytes);
    assert_eq!(input.position, [0.5, 1.0, 0.5]);
    assert_eq!(input.velocity, [0.0, -1.6, 0.0]);
    assert!(input.on_ground);
    assert!(!input.jump);
    assert_eq!(input.move_x, 1);
    assert_eq!(input.move_z, 0);
    assert_eq!(input.yaw_sin, 0.0);
    assert_eq!(input.yaw_cos, 1.0);
    assert_eq!(input.fixed_delta_seconds, 0.05);
    assert_eq!(input.step_height, 0.6);
    assert_eq!(input.dimensions, [1, 1, 1]);
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd engine && cargo test --workspace --locked`
Expected: FAIL（`step_input_is_valid`、`StepInput` 未定义，编译失败）。

- [ ] **Step 3: 实现 step.rs 解析与校验**

```rust
//! physics step ABI 的输入解析、校验、积分与输出编码。
//!
//! 输入布局与偏移以 `docs/superpowers/specs/2026-08-15-rust-engine-physics-step-design.md`
//! 第 4 节为准：128 字节 header（magic MGP1 + layout v1）+ 每 cell 196 字节。

pub(crate) const STEP_HEADER_BYTES: usize = 128;
pub(crate) const STEP_OUTPUT_BYTES: usize = 32;
pub(crate) const STEP_MAX_CELLS: usize = 4096;

const CELL_BYTES: usize = 196;

fn read_u32(bytes: &[u8], offset: usize) -> u32 {
    u32::from_le_bytes(bytes[offset..offset + 4].try_into().expect("validated range"))
}

fn read_i32(bytes: &[u8], offset: usize) -> i32 {
    i32::from_le_bytes(bytes[offset..offset + 4].try_into().expect("validated range"))
}

fn read_f32(bytes: &[u8], offset: usize) -> f32 {
    f32::from_bits(read_u32(bytes, offset))
}

pub(crate) struct StepInput<'a> {
    pub(crate) bytes: &'a [u8],
    pub(crate) position: [f32; 3],
    pub(crate) velocity: [f32; 3],
    pub(crate) on_ground: bool,
    pub(crate) jump: bool,
    pub(crate) move_x: i8,
    pub(crate) move_z: i8,
    pub(crate) yaw_sin: f32,
    pub(crate) yaw_cos: f32,
    pub(crate) fixed_delta_seconds: f32,
    pub(crate) step_height: f32,
    pub(crate) walk_speed: f32,
    pub(crate) ground_acceleration: f32,
    pub(crate) ground_deceleration: f32,
    pub(crate) air_acceleration: f32,
    pub(crate) jump_speed: f32,
    pub(crate) gravity: f32,
    pub(crate) terminal_fall_speed: f32,
    pub(crate) sweep_min: [f32; 3],
    pub(crate) sweep_max: [f32; 3],
    pub(crate) origin: [i32; 3],
    pub(crate) dimensions: [u32; 3],
}

impl<'a> StepInput<'a> {
    pub(crate) fn decode(bytes: &'a [u8]) -> Self {
        let mut tunables = [0.0f32; 8];
        for (index, slot) in tunables.iter_mut().enumerate() {
            *slot = read_f32(bytes, 48 + index * 4);
        }
        let mut sweep_min = [0.0f32; 3];
        let mut sweep_max = [0.0f32; 3];
        for axis in 0..3 {
            sweep_min[axis] = read_f32(bytes, 80 + axis * 8);
            sweep_max[axis] = read_f32(bytes, 84 + axis * 8);
        }
        let mut origin = [0i32; 3];
        let mut dimensions = [0u32; 3];
        for axis in 0..3 {
            origin[axis] = read_i32(bytes, 104 + axis * 4);
            dimensions[axis] = read_u32(bytes, 116 + axis * 4);
        }
        Self {
            bytes: &bytes[STEP_HEADER_BYTES..],
            position: [read_f32(bytes, 8), read_f32(bytes, 12), read_f32(bytes, 16)],
            velocity: [read_f32(bytes, 20), read_f32(bytes, 24), read_f32(bytes, 28)],
            on_ground: bytes[32] == 1,
            jump: bytes[33] == 1,
            move_x: bytes[34] as i8,
            move_z: bytes[35] as i8,
            yaw_sin: read_f32(bytes, 36),
            yaw_cos: read_f32(bytes, 40),
            fixed_delta_seconds: read_f32(bytes, 44),
            step_height: tunables[0],
            walk_speed: tunables[1],
            ground_acceleration: tunables[2],
            ground_deceleration: tunables[3],
            air_acceleration: tunables[4],
            jump_speed: tunables[5],
            gravity: tunables[6],
            terminal_fall_speed: tunables[7],
            sweep_min,
            sweep_max,
            origin,
            dimensions,
        }
    }
}

pub(crate) fn step_input_is_valid(bytes: &[u8]) -> bool {
    if bytes.len() < STEP_HEADER_BYTES
        || &bytes[0..4] != b"MGP1"
        || read_u32(bytes, 4) != 1
        || bytes[32] > 1
        || bytes[33] > 1
        || !(-1..=1).contains(&(bytes[34] as i8))
        || !(-1..=1).contains(&(bytes[35] as i8))
    {
        return false;
    }
    // position/velocity（8..32）、yaw_sin/yaw_cos/dt（36/40/44）、tunables 与 sweep bounds（48..104）必须全部有限
    for offset in (8..32)
        .step_by(4)
        .chain((36..=44).step_by(4))
        .chain((48..104).step_by(4))
    {
        if !read_f32(bytes, offset).is_finite() {
            return false;
        }
    }
    for axis in 0..3 {
        if read_f32(bytes, 80 + axis * 8) > read_f32(bytes, 84 + axis * 8) {
            return false;
        }
    }
    let mut cell_count: usize = 1;
    for axis in 0..3 {
        let dimension = read_u32(bytes, 116 + axis * 4);
        let origin = read_i32(bytes, 104 + axis * 4);
        if dimension == 0 || origin.checked_add((dimension - 1) as i32).is_none() {
            return false;
        }
        let Some(next) = cell_count.checked_mul(dimension as usize) else {
            return false;
        };
        cell_count = next;
    }
    if cell_count > STEP_MAX_CELLS {
        return false;
    }
    let Some(expected_length) = STEP_HEADER_BYTES.checked_add(cell_count * CELL_BYTES) else {
        return false;
    };
    if expected_length != bytes.len() {
        return false;
    }
    for cell in bytes[STEP_HEADER_BYTES..].chunks_exact(CELL_BYTES) {
        if cell[0] > 1 || cell[1] > 8 || cell[2] != 0 || cell[3] != 0 {
            return false;
        }
        for box_index in 0..cell[1] as usize {
            let box_offset = 4 + box_index * 24;
            for component in 0..6 {
                if !read_f32(cell, box_offset + component * 4).is_finite() {
                    return false;
                }
            }
        }
    }
    true
}
```

- [ ] **Step 4: 注册模块并运行测试**

在 `engine/crates/mornlea_engine/src/lib.rs` 中加一行 `mod step;`。

Run: `cd engine && cargo test --workspace --locked`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add engine/crates/mornlea_engine/src/step.rs engine/crates/mornlea_engine/src/lib.rs
git commit -m "feat: 新增 physics step 输入解析与校验"
```

---

### Task 4: Rust step.rs 积分镜像

**Files:**
- Modify: `engine/crates/mornlea_engine/src/step.rs`

**Interfaces:**
- Consumes: Task 3 的 `StepInput`/`step_input_is_valid`。
- Produces: `pub(crate) fn integrate(input: &StepInput<'_>) -> ([f32; 3], [f32; 3])`——返回（积分后 velocity，displacement）。Task 5 的 `physics_step` 依赖它。

运算顺序约束（与 Go 逐位一致的硬要求，写进注释）：mgl32 `Len` = f32 平方和（左结合）→ `float64` 转换 → `math.Sqrt`（正确舍入）→ 转回 f32；`Normalize` = `1.0/Len` 再逐分量乘；`moveToward` = `current + delta*(maximumDelta/Len(delta))`；重力 = `velocity.y − gravity*dt` 后与 `−terminal` 取 `max`（IEEE 符号零：+0 胜出，Go 内建 max 与 Rust `f32::max` 语义一致）。

- [ ] **Step 1: 写失败测试（追加到 `#[cfg(test)] mod tests`）**

```rust
fn diagonal_input_accelerates_without_boost() {
    let mut bytes = valid_step_bytes();
    bytes[35] = 1; // move_z = 1，真正的对角输入
    let input = StepInput::decode(Box::leak(bytes.into_boxed_slice()));
    let (velocity, _) = integrate(&input);
    let horizontal = (velocity[0] * velocity[0] + velocity[2] * velocity[2]).sqrt();
    assert!((horizontal - 2.0).abs() < 1e-5);
}

#[test]
fn jump_uses_jump_speed() {
    let mut bytes = valid_step_bytes();
    bytes[33] = 1; // jump
    let input = StepInput::decode(Box::leak(bytes.into_boxed_slice()));
    let (velocity, _) = integrate(&input);
    assert_eq!(velocity[1].to_bits(), 8.4f32.to_bits());
}

#[test]
fn gravity_clamps_to_terminal() {
    let mut bytes = valid_step_bytes();
    bytes[32] = 0; // 空中
    write_f32(&mut bytes, 24, -78.0);
    let input = StepInput::decode(Box::leak(bytes.into_boxed_slice()));
    let (velocity, _) = integrate(&input);
    assert_eq!(velocity[1].to_bits(), (-78.4f32).to_bits());
}

#[test]
fn zero_input_on_ground_decelerates() {
    let mut bytes = valid_step_bytes();
    bytes[34] = 0; // move_x = 0
    write_f32(&mut bytes, 20, 10.0); // velocity x = 10
    let input = StepInput::decode(Box::leak(bytes.into_boxed_slice()));
    let (velocity, _) = integrate(&input);
    assert_eq!(velocity[0].to_bits(), 7.5f32.to_bits()); // 10 − 50*0.05
}
```

（说明：`valid_step_bytes` 的 move_x=1、velocity y=-1.6、sweep dy=[-0.08,0.05] 与积分后的 dy=-0.16 不一致，只对纯 `integrate` 单测成立；涉及 `physics_step` 的 Task 5 测试会使用各自的专用夹具，不受此影响。）

- [ ] **Step 2: 运行测试确认失败**

Run: `cd engine && cargo test --workspace --locked`
Expected: FAIL（`integrate` 未定义）。

- [ ] **Step 3: 实现 integrate**

```rust
type Vector = [f32; 3];

// 与 Go mgl32.Vec3.Len 逐位一致：f32 平方和（左结合）→ f64 sqrt → f32。
fn vec3_len(v: Vector) -> f32 {
    let sum = v[0] * v[0] + v[1] * v[1] + v[2] * v[2];
    ((sum as f64).sqrt()) as f32
}

// 与 Go mgl32.Vec3.Normalize 逐位一致：l = 1.0/Len，再逐分量乘。
fn vec3_normalize(v: Vector) -> Vector {
    let l = 1.0f32 / vec3_len(v);
    [v[0] * l, v[1] * l, v[2] * l]
}

fn vec3_scale(v: Vector, c: f32) -> Vector {
    [v[0] * c, v[1] * c, v[2] * c]
}

// 与 Go moveToward 逐位一致：delta = target−current；len <= max → target；
// 否则 current + delta*(max/len)。
fn move_toward(current: Vector, target: Vector, maximum_delta: f32) -> Vector {
    let delta = [
        target[0] - current[0],
        target[1] - current[1],
        target[2] - current[2],
    ];
    let length = vec3_len(delta);
    if length <= maximum_delta {
        return target;
    }
    let scale = maximum_delta / length;
    [
        current[0] + delta[0] * scale,
        current[1] + delta[1] * scale,
        current[2] + delta[2] * scale,
    ]
}

// 与 Go movementTarget 逐位一致（三角已由 Go 算好传入）：
// right.Mul(f32(MoveX)).Add(forward.Mul(f32(MoveZ)))，Normalize().Mul(walkSpeed)。
fn movement_target(
    move_x: i8,
    move_z: i8,
    walk_speed: f32,
    yaw_sin: f32,
    yaw_cos: f32,
) -> Vector {
    let forward = [-yaw_sin, 0.0, -yaw_cos];
    let right = [yaw_cos, 0.0, -yaw_sin];
    let intent = [
        right[0] * move_x as f32 + forward[0] * move_z as f32,
        right[1] * move_x as f32 + forward[1] * move_z as f32,
        right[2] * move_x as f32 + forward[2] * move_z as f32,
    ];
    if vec3_len(intent) == 0.0 {
        return [0.0; 3];
    }
    vec3_scale(vec3_normalize(intent), walk_speed)
}

// integrate 返回（积分后 velocity，displacement）。运算顺序逐条镜像 Go 旧 Step 实现。
pub(crate) fn integrate(input: &StepInput<'_>) -> (Vector, Vector) {
    let dt = input.fixed_delta_seconds;
    let mut velocity = input.velocity;
    let target = movement_target(
        input.move_x,
        input.move_z,
        input.walk_speed,
        input.yaw_sin,
        input.yaw_cos,
    );
    let mut horizontal = [velocity[0], 0.0, velocity[2]];
    if input.on_ground {
        if vec3_len(target) == 0.0 {
            horizontal = move_toward(horizontal, [0.0; 3], input.ground_deceleration * dt);
        } else {
            horizontal = move_toward(horizontal, target, input.ground_acceleration * dt);
        }
    } else {
        horizontal = move_toward(horizontal, target, input.air_acceleration * dt);
        if vec3_len(horizontal) > input.walk_speed {
            horizontal = vec3_scale(vec3_normalize(horizontal), input.walk_speed);
        }
    }
    velocity[0] = horizontal[0];
    velocity[2] = horizontal[2];
    if input.on_ground && input.jump {
        velocity[1] = input.jump_speed;
    } else {
        velocity[1] = (velocity[1] - input.gravity * dt).max(-input.terminal_fall_speed);
    }
    let displacement = [velocity[0] * dt, velocity[1] * dt, velocity[2] * dt];
    (velocity, displacement)
}
```

- [ ] **Step 4: 运行测试**

Run: `cd engine && cargo test --workspace --locked`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add engine/crates/mornlea_engine/src/step.rs
git commit -m "feat: 实现 physics step 积分镜像"
```

---

### Task 5: Rust ffi 入口 + C header + ABI v2

**Files:**
- Modify: `engine/crates/mornlea_engine/src/step.rs`（`physics_step` + `StepError` + 输出编码）
- Modify: `engine/crates/mornlea_engine/src/ffi.rs`（`physics_step_input_is_valid`、`mornlea_physics_step`、`ABI_VERSION` 1→2）
- Modify: `engine/include/mornlea_engine.h`（声明 + `MORNLEA_ENGINE_ABI_VERSION` 1u→2u）

**Interfaces:**
- Consumes: Task 3/4 的 `StepInput`/`step_input_is_valid`/`integrate`；Task 2 的 `resolve_collision_parts`。
- Produces: `pub(crate) fn physics_step(bytes: &[u8]) -> Result<[u8; STEP_OUTPUT_BYTES], StepError>`（`StepError::DisplacementOutOfBounds`）；`mornlea_physics_step` extern 符号。Task 6 的 Go 绑定依赖此符号与 ABI v2。

- [ ] **Step 1: 写失败测试（追加到 step.rs 测试模块）**

```rust
// empty_prism_bytes 构造"空中 + 空世界"夹具：position {0.5,1,0.5}、velocity 0、
// on_ground=0、move_x=0、sweep dy=[-0.08,0.05]、prism {1,4,1} 全空 cell。
// 积分结果：velocity {0,-1.6,0}，displacement {0,-0.08,0}，位置落到 y=0.92。
fn empty_prism_bytes() -> Vec<u8> {
    let dimensions = [1u32, 4, 1];
    let cell_count = (dimensions[0] * dimensions[1] * dimensions[2]) as usize;
    let mut bytes = vec![0u8; STEP_HEADER_BYTES + cell_count * CELL_BYTES];
    bytes[0..4].copy_from_slice(b"MGP1");
    bytes[4..8].copy_from_slice(&1u32.to_le_bytes());
    write_f32(&mut bytes, 8, 0.5);
    write_f32(&mut bytes, 12, 1.0);
    write_f32(&mut bytes, 16, 0.5);
    // velocity 保持 0
    bytes[32] = 0; // 空中
    // move_x = 0
    write_f32(&mut bytes, 36, 0.0);  // yaw_sin
    write_f32(&mut bytes, 40, 1.0);  // yaw_cos
    write_f32(&mut bytes, 44, 0.05); // dt
    for (index, value) in [0.6f32, 4.3, 40.0, 50.0, 8.0, 8.4, 32.0, 78.4]
        .iter()
        .enumerate()
    {
        write_f32(&mut bytes, 48 + index * 4, *value);
    }
    write_f32(&mut bytes, 80, 0.0);  // dx_min
    write_f32(&mut bytes, 84, 0.0);  // dx_max
    write_f32(&mut bytes, 88, -0.08); // dy_min（积分 dy = -0.08）
    write_f32(&mut bytes, 92, 0.05); // dy_max
    write_f32(&mut bytes, 96, 0.0);  // dz_min
    write_f32(&mut bytes, 100, 0.0); // dz_max
    for index in 0..3 {
        bytes[104 + index * 4..108 + index * 4].copy_from_slice(&0u32.to_le_bytes());
        bytes[116 + index * 4..120 + index * 4].copy_from_slice(&dimensions[index].to_le_bytes());
    }
    for cell in bytes[STEP_HEADER_BYTES..].chunks_exact_mut(CELL_BYTES) {
        cell[0] = 1; // loaded、count 0
    }
    bytes
}

#[test]
fn physics_step_rejects_displacement_outside_sweep_bounds() {
    // 基础夹具 on_ground=1、velocity y=-1.6、sweep dy=[-0.08,0.05]，
    // 积分 dy = -0.16 越界 → DisplacementOutOfBounds。
    let input_bytes = Box::leak(valid_step_bytes().into_boxed_slice());
    assert!(matches!(
        physics_step(input_bytes),
        Err(StepError::DisplacementOutOfBounds)
    ));
}

#[test]
fn physics_step_encodes_output_layout() {
    let bytes = Box::leak(empty_prism_bytes().into_boxed_slice());
    let output = physics_step(bytes).expect("valid input");
    assert_eq!(output.len(), STEP_OUTPUT_BYTES);
    assert_eq!(read_f32(&output, 0), 0.5);  // position x
    assert_eq!(read_f32(&output, 4), 0.92); // position y = 1 − 0.08
    assert_eq!(read_f32(&output, 8), 0.5);  // position z
    assert_eq!(read_f32(&output, 12), 0.0); // velocity x
    assert_eq!(read_f32(&output, 16), -1.6); // velocity y（重力 −1.6）
    assert_eq!(read_f32(&output, 20), 0.0); // velocity z
    assert_eq!(output[24], 0); // clipped mask
    assert_eq!(output[25], 0); // on_ground（0.92 处无支撑）
    assert_eq!(output[26], 0); // used_step
    assert_eq!(output[27], 0); // hit_unknown
    assert_eq!(&output[28..32], &[0, 0, 0, 0]); // reserved
}

#[test]
fn physics_step_lands_on_floor_and_clips_velocity() {
    let mut bytes = empty_prism_bytes();
    // 地板：cell (0,0,0)（prism Y/X/Z 顺序的第一个 cell）全立方
    bytes[STEP_HEADER_BYTES + 1] = 1;
    let box_offset = STEP_HEADER_BYTES + 4;
    write_f32(&mut bytes, box_offset, 0.0);
    write_f32(&mut bytes, box_offset + 4, 0.0);
    write_f32(&mut bytes, box_offset + 8, 0.0);
    write_f32(&mut bytes, box_offset + 12, 1.0);
    write_f32(&mut bytes, box_offset + 16, 1.0);
    write_f32(&mut bytes, box_offset + 20, 1.0);
    let input_bytes = Box::leak(bytes.into_boxed_slice());
    let output = physics_step(input_bytes).expect("valid input");
    assert_eq!(read_f32(&output, 4), 1.0); // position y 落回 1.0
    assert_eq!(read_f32(&output, 16), 0.0); // velocity y 被裁剪清零
    assert_eq!(output[25], 1); // on_ground
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd engine && cargo test --workspace --locked`
Expected: FAIL（`physics_step`/`StepError` 未定义）。

- [ ] **Step 3: 实现 step.rs 的 physics_step 与输出编码**

```rust
pub(crate) enum StepError {
    DisplacementOutOfBounds,
}

fn write_f32_output(output: &mut [u8], offset: usize, value: f32) {
    output[offset..offset + 4].copy_from_slice(&value.to_bits().to_le_bytes());
}

// physics_step 一次完成积分 + 碰撞解析 + 速度裁剪。
// 积分位移必须落在输入 sweep bounds 内，否则返回 DisplacementOutOfBounds（调用方映射 StatusInput）。
pub(crate) fn physics_step(bytes: &[u8]) -> Result<[u8; STEP_OUTPUT_BYTES], StepError> {
    let input = StepInput::decode(bytes);
    let (velocity, displacement) = integrate(&input);
    for axis in 0..3 {
        if !(input.sweep_min[axis] <= displacement[axis]
            && displacement[axis] <= input.sweep_max[axis])
        {
            return Err(StepError::DisplacementOutOfBounds);
        }
    }
    let result = crate::collision::resolve_collision_parts(
        input.position,
        displacement,
        input.on_ground,
        input.step_height,
        input.origin,
        input.dimensions,
        input.bytes,
    );
    // result 布局沿用 collision ABI：position(0..12)、clipped mask(12)、on_ground(13)、used_step(14)、hit_unknown(15)
    let clipped = result[12];
    let mut velocity = velocity;
    for axis in 0..3 {
        if clipped & (1 << axis) != 0 {
            velocity[axis] = 0.0;
        }
    }
    let mut output = [0u8; STEP_OUTPUT_BYTES];
    for axis in 0..3 {
        let result_component: [u8; 4] = result[axis * 4..axis * 4 + 4]
            .try_into()
            .expect("collision result 4 bytes");
        write_f32_output(&mut output, axis * 4, f32::from_le_bytes(result_component));
        write_f32_output(&mut output, 12 + axis * 4, velocity[axis]);
    }
    output[24] = clipped;
    output[25] = result[13];
    output[26] = result[14];
    output[27] = result[15];
    Ok(output)
}
```

- [ ] **Step 4: ffi.rs 增加入口并升 ABI_VERSION**

在 `engine/crates/mornlea_engine/src/ffi.rs`：

```rust
use crate::step::{STEP_HEADER_BYTES, STEP_OUTPUT_BYTES, physics_step};

pub(crate) const ABI_VERSION: u32 = 2; // 原为 1

const PHYSICS_STEP_HEADER_BYTES: usize = STEP_HEADER_BYTES;
const PHYSICS_STEP_OUTPUT_BYTES: usize = STEP_OUTPUT_BYTES;

fn physics_step_input_is_valid(bytes: &[u8]) -> bool {
    crate::step::step_input_is_valid(bytes)
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn mornlea_physics_step(
    abi_version: u32,
    input: *const u8,
    input_len: usize,
    output: *mut u8,
    output_len: usize,
) -> u32 {
    // SAFETY: C 调用方提供原始缓冲区；helper 会在解引用前验证指针、范围、长度与重叠。
    unsafe {
        physics_step_with(abi_version, input, input_len, output, output_len)
    }
}

unsafe fn physics_step_with(
    abi_version: u32,
    input: *const u8,
    input_len: usize,
    output: *mut u8,
    output_len: usize,
) -> u32 {
    if abi_version != ABI_VERSION {
        return MORNLEA_STATUS_ABI_VERSION;
    }
    if input.is_null() || output.is_null() {
        return MORNLEA_STATUS_INVALID_ARGUMENT;
    }
    if output_len < PHYSICS_STEP_OUTPUT_BYTES {
        return MORNLEA_STATUS_OUTPUT_OVERFLOW;
    }
    if output_len != PHYSICS_STEP_OUTPUT_BYTES
        || !byte_range_is_valid(input, input_len)
        || !byte_range_is_valid(output, output_len)
    {
        return MORNLEA_STATUS_INVALID_ARGUMENT;
    }
    if ranges_overlap(input.addr(), input_len, output.addr(), output_len) {
        return MORNLEA_STATUS_INVALID_ARGUMENT;
    }

    let result = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
        // SAFETY: input 非空，范围不超过 isize::MAX，地址加法不回绕且不与 output 重叠。
        let bytes = unsafe { std::slice::from_raw_parts(input, input_len) };
        if !physics_step_input_is_valid(bytes) {
            return Err(MORNLEA_STATUS_INPUT);
        }
        physics_step(bytes).map_err(|_| MORNLEA_STATUS_INPUT)
    }));
    match result {
        Ok(Ok(result)) => {
            // SAFETY: output 非空、范围有效且与 input 不重叠；只在完整成功后一次发布。
            unsafe { std::ptr::copy_nonoverlapping(result.as_ptr(), output, result.len()) };
            MORNLEA_STATUS_OK
        }
        Ok(Err(status)) => status,
        Err(_) => MORNLEA_STATUS_PANIC,
    }
}
```

- [ ] **Step 5: 更新 C header**

`engine/include/mornlea_engine.h`：`#define MORNLEA_ENGINE_ABI_VERSION 1u` 改为 `2u`，并在 `mornlea_raycast_batch` 声明后追加：

```c
uint32_t mornlea_physics_step(
    uint32_t abi_version,
    const uint8_t *input,
    size_t input_len,
    uint8_t *output,
    size_t output_len);
```

- [ ] **Step 6: 构建并跑全部 Rust 测试**

Run: `make rust && cd engine && cargo test --workspace --locked`
Expected: 构建成功；全部 PASS。（`make rust` 会重建 cdylib，使 Go 侧随后可见 ABI v2。）

- [ ] **Step 7: 提交**

```bash
git add engine/crates/mornlea_engine/src/step.rs engine/crates/mornlea_engine/src/ffi.rs engine/include/mornlea_engine.h engine/Cargo.lock
git commit -m "feat: 导出 mornlea_physics_step 并升 ABI 到 v2"
```

（若 `engine/Cargo.lock` 无变化则不加；`git status` 确认后再决定。）

---

### Task 6: Go nativeabi.PhysicsStep 绑定

**Files:**
- Modify: `internal/nativeabi/native.go`
- Modify: `internal/nativeabi/native_test.go`

**Interfaces:**
- Consumes: Task 5 的 `mornlea_physics_step` 符号（ABI v2）。
- Produces: `func PhysicsStep(input, output []byte)`——非法 status 时 panic 中文文案（`nativeabi: physics step ...`）。Task 8 的 `physics.Step` 依赖它。

- [ ] **Step 1: 写失败测试（native_test.go）**

先在 `TestEngineCgoDirectivesArePresent` 的指令清单里追加：

```go
		"#cgo noescape mornlea_physics_step",
		"#cgo nocallback mornlea_physics_step",
```

再新增测试（放在 `TestCollisionRawFailureAtomicity` 之后）：

```go
func testValidPhysicsStepInput() []byte {
	input := make([]byte, 128+196)
	copy(input[:4], "MGP1")
	binary.LittleEndian.PutUint32(input[4:8], 1)
	for _, offset := range []int{8, 12, 16, 20, 24, 28, 36, 40, 44} {
		binary.LittleEndian.PutUint32(input[offset:offset+4], math.Float32bits(0))
	}
	for index, value := range [...]float32{0.6, 4.3, 40, 50, 8, 8.4, 32, 78.4} {
		binary.LittleEndian.PutUint32(input[48+index*4:52+index*4], math.Float32bits(value))
	}
	for _, offset := range []int{80, 84, 88, 92, 96, 100} {
		binary.LittleEndian.PutUint32(input[offset:offset+4], math.Float32bits(0))
	}
	for index := range 3 {
		binary.LittleEndian.PutUint32(input[116+index*4:120+index*4], 1)
	}
	input[128] = 1 // cell loaded
	return input
}

func TestPhysicsStepRawFailureAtomicity(t *testing.T) {
	validInput := testValidPhysicsStepInput()
	malformed := slices.Clone(validInput)
	malformed[33] = 2 // jump 非法
	for _, test := range []struct {
		name    string
		version uint32
		input   []byte
		output  []byte
		want    Status
	}{
		{name: "ABI version", version: ABIVersion + 1, input: validInput, output: make([]byte, 32), want: StatusABIVersion},
		{name: "nil input", version: ABIVersion, output: make([]byte, 32), want: StatusInvalidArgument},
		{name: "short input", version: ABIVersion, input: validInput[:127], output: make([]byte, 32), want: StatusInput},
		{name: "long input", version: ABIVersion, input: append(slices.Clone(validInput), 0), output: make([]byte, 32), want: StatusInput},
		{name: "jump flag", version: ABIVersion, input: malformed, output: make([]byte, 32), want: StatusInput},
		{name: "short output", version: ABIVersion, input: validInput, output: make([]byte, 31), want: StatusOutputOverflow},
		{name: "long output", version: ABIVersion, input: validInput, output: make([]byte, 33), want: StatusInvalidArgument},
	} {
		t.Run(test.name, func(t *testing.T) {
			output := slices.Clone(test.output)
			status := physicsStepVersion(test.version, test.input, output)
			if status != test.want {
				t.Fatalf("status=%d，想要 %d", status, test.want)
			}
			if !slices.Equal(output, test.output) {
				t.Fatal("失败调用修改了 caller-owned output")
			}
		})
	}
}
```

注意：若测试输入会触发积分，`validInput` 的 sweep bounds 全 0 而 velocity 也全 0 → 位移全 0 ∈ [0,0]，不会走 DisplacementOutOfBounds 路径；全部失败用例都在校验层拒绝。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/nativeabi -race -count=1`
Expected: FAIL（`physicsStepVersion` 未定义、cgo 指令缺失）。

- [ ] **Step 3: 实现绑定（native.go）**

在 cgo 注释块 `#cgo noescape mornlea_raycast_batch` 之后追加：

```c
#cgo noescape mornlea_physics_step
#cgo nocallback mornlea_physics_step
```

在 `CollisionResolve` 之后追加：

```go
// PhysicsStep 把调用方拥有的 physics step ABI 缓冲区传给 engine。
func PhysicsStep(input, output []byte) {
	status := physicsStepVersion(ABIVersion, input, output)
	if status != StatusOK {
		panic(physicsStepStatusPanicText(status))
	}
}

func physicsStepVersion(version uint32, input, output []byte) Status {
	return Status(C.mornlea_physics_step(
		C.uint32_t(version),
		(*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(input))),
		C.size_t(len(input)),
		(*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(output))),
		C.size_t(len(output)),
	))
}

func physicsStepStatusPanicText(status Status) string {
	switch status {
	case StatusABIVersion:
		return "nativeabi: physics step ABI 版本不匹配"
	case StatusInvalidArgument:
		return "nativeabi: physics step 参数非法"
	case StatusInput:
		return "nativeabi: physics step 输入非法"
	case StatusOutputOverflow:
		return "nativeabi: physics step output 过短"
	case StatusPanic:
		return "nativeabi: physics step Rust panic"
	default:
		return "nativeabi: physics step 未知状态"
	}
}
```

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/nativeabi -race -count=1`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/nativeabi/native.go internal/nativeabi/native_test.go
git commit -m "feat: 新增 nativeabi PhysicsStep 绑定"
```

---

### Task 7: Go 积分 oracle 提取 + 差分断言重构

**Files:**
- Create: `internal/physics/motion_oracle_test.go`
- Modify: `internal/physics/collision_native_test.go`（462–527 行及语料调用点）

**Interfaces:**
- Consumes: 现有 `oracleResolveCollision`（collision_oracle_test.go）。
- Produces: `func oracleStep(state physics.State, input physics.Input, source physics.CollisionSource, tunables physics.Tunables) physics.StepResult`；`func testAssertProductionStepMatchesOracle(t *testing.T, state physics.State, input physics.Input, source physics.CollisionSource)`（签名去掉 integratedVelocity）。Task 8/9 依赖。

- [ ] **Step 1: 写 motion_oracle_test.go（旧积分代码原样迁入）**

把当前 `internal/physics/motion.go` 第 9–83 行的 `Step`、`movementTarget`、`moveToward` 原样复制为 oracle 版本（`Step` 改名为 `oracleStep`，其余改名 oracle 前缀），并把 `validate`/`finiteVector` 留在生产（oracle 不重复实现）：

```go
package physics_test

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/physics"
)

// oracleStep 是旧 Go 积分实现的逐字副本（生产曾位于 motion.go），
// 只用于与 native 生产路径做逐位奇偶断言。不得在生产代码引用。
func oracleStep(
	state physics.State,
	input physics.Input,
	source physics.CollisionSource,
	tunables physics.Tunables,
) physics.StepResult {
	beganGrounded := state.OnGround

	target := oracleMovementTarget(input, tunables.WalkSpeed)
	horizontal := mgl32.Vec3{state.Velocity.X(), 0, state.Velocity.Z()}
	if state.OnGround {
		if target.Len() == 0 {
			horizontal = oracleMoveToward(horizontal, mgl32.Vec3{}, tunables.GroundDeceleration*physics.FixedDeltaSeconds)
		} else {
			horizontal = oracleMoveToward(horizontal, target, tunables.GroundAcceleration*physics.FixedDeltaSeconds)
		}
	} else {
		horizontal = oracleMoveToward(horizontal, target, tunables.AirAcceleration*physics.FixedDeltaSeconds)
		if horizontal.Len() > tunables.WalkSpeed {
			horizontal = horizontal.Normalize().Mul(tunables.WalkSpeed)
		}
	}
	state.Velocity[0], state.Velocity[2] = horizontal.X(), horizontal.Z()

	if state.OnGround && input.Jump {
		state.Velocity[1] = tunables.JumpSpeed
		state.OnGround = false
	} else {
		state.Velocity[1] = max(
			state.Velocity.Y()-tunables.Gravity*physics.FixedDeltaSeconds,
			-tunables.TerminalFallSpeed,
		)
	}
	displacement := state.Velocity.Mul(physics.FixedDeltaSeconds)
	move := oracleResolveCollision(
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

	return physics.StepResult{
		State:      state,
		UsedStep:   move.usedStep,
		HitUnknown: move.hitUnknown,
	}
}

func oracleMovementTarget(input physics.Input, walkSpeed float32) mgl32.Vec3 {
	yawSin := float32(math.Sin(float64(input.Yaw)))
	yawCos := float32(math.Cos(float64(input.Yaw)))
	forward := mgl32.Vec3{-yawSin, 0, -yawCos}
	right := mgl32.Vec3{yawCos, 0, -yawSin}
	intent := right.Mul(float32(input.MoveX)).Add(forward.Mul(float32(input.MoveZ)))
	if intent.Len() == 0 {
		return mgl32.Vec3{}
	}
	return intent.Normalize().Mul(walkSpeed)
}

func oracleMoveToward(current, target mgl32.Vec3, maximumDelta float32) mgl32.Vec3 {
	delta := target.Sub(current)
	if length := delta.Len(); length <= maximumDelta {
		return target
	}
	return current.Add(delta.Mul(maximumDelta / delta.Len()))
}
```

- [ ] **Step 2: 重构差分断言与调用点（collision_native_test.go）**

`testAssertProductionStepMatchesOracle` 改为（删除 integratedVelocity 参数）：

```go
func testAssertProductionStepMatchesOracle(
	t *testing.T,
	state physics.State,
	input physics.Input,
	source physics.CollisionSource,
) {
	t.Helper()
	want := oracleStep(state, input, source, physics.ActiveTunables())
	got := physics.Step(state, input, source)
	for axis := range 3 {
		if math.Float32bits(got.State.Position[axis]) != math.Float32bits(want.State.Position[axis]) ||
			math.Float32bits(got.State.Velocity[axis]) != math.Float32bits(want.State.Velocity[axis]) {
			t.Fatalf("production Step axis %d=%+v，want oracle=%+v", axis, got, want)
		}
	}
	if got.State.OnGround != want.State.OnGround || got.UsedStep != want.UsedStep || got.HitUnknown != want.HitUnknown {
		t.Fatalf("production Step=%+v，want oracle=%+v", got, want)
	}
}
```

同步更新所有调用点（删除 `integratedVelocity` 字段与实参）：

- `TestStepProductionMatchesGoCollisionOracle`（462–495 行）：结构体删 `integratedVelocity mgl32.Vec3` 字段与三个用例的该行；调用改为 `testAssertProductionStepMatchesOracle(t, test.state, test.input, test.world)`。
- `TestStepProductionMatchesGoCollisionOracleDeterministicCorpus`（552–642 行）：语料循环里的 `integratedVelocity := test.displacement.Mul(1 / physics.FixedDeltaSeconds)`、`state.Velocity = integratedVelocity` 与末尾实参删除，改为 `testAssertProductionStepMatchesOracle(t, state, physics.Input{}, test.world)`。随机段同理。
- `TestCollisionSnapshotClampsBoxCount`（382 行）：删除末实参 `mgl32.Vec3{7.5, -1.6, 0}`。

（等价性依据：语料测试已把 acceleration/gravity 清零、WalkSpeed 设为 MaxFloat，oracle 积分后 velocity 保持原值、displacement = velocity×dt，与原来手工传入的 `displacement×(1/dt)` 逐位相同。）

- [ ] **Step 3: 运行测试（生产未变，应全绿）**

Run: `go test ./internal/physics -race -count=1`
Expected: PASS。

- [ ] **Step 4: 提交**

```bash
git add internal/physics/motion_oracle_test.go internal/physics/collision_native_test.go
git commit -m "test: 提取 Go 积分 oracle 并重构差分断言"
```

---

### Task 8: Go 生产 Step 切 native（布局测试 TDD）

**Files:**
- Create: `internal/physics/step.go`
- Create: `internal/physics/step_native_test.go`（布局测试 + 差分语料基线）
- Modify: `internal/physics/motion.go`（删除积分主体）
- Modify: `internal/physics/collision.go`（删除 resolve/encode/decode，prism 泛化）

**Interfaces:**
- Consumes: Task 6 的 `nativeabi.PhysicsStep`；Task 7 的 `oracleStep`/差分断言。
- Produces: 生产 `physics.Step`（签名不变）；`stepPrismFor`、`encodeStepInput`、`decodeStepOutput`（测试引用）。

- [ ] **Step 1: 写 step_native_test.go（布局测试 + 差分语料基线）**

```go
package physics_test

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/physics"
)

const (
	testStepHeaderBytes  = 128
	testStepCellBytes    = 196
	testStepOutputBytes  = 32
	testStepMaxCells     = 4096
	testStepMaxBytes     = testStepHeaderBytes + testStepMaxCells*testStepCellBytes
	testStepRegularCells = 135
	testStepRegularBytes = testStepHeaderBytes + testStepRegularCells*testStepCellBytes
)

func putTestStepVec3(output []byte, value mgl32.Vec3) {
	for index := range 3 {
		binary.LittleEndian.PutUint32(output[index*4:index*4+4], math.Float32bits(value[index]))
	}
}

func putTestStepFloat(output []byte, value float32) {
	binary.LittleEndian.PutUint32(output, math.Float32bits(value))
}

// testEncodeStepInputInto 是生产 encodeStepInput 的镜像（测试自持副本，不依赖生产实现细节之外的 ABI）。
func testEncodeStepInputInto(
	input []byte,
	prism testStepPrism,
	state physics.State,
	moveX, moveZ int8,
	jump bool,
	yawSin, yawCos float32,
	tunables physics.Tunables,
	sweepMin, sweepMax mgl32.Vec3,
	source physics.CollisionSource,
) {
	clear(input)
	copy(input[:4], "MGP1")
	binary.LittleEndian.PutUint32(input[4:8], 1)
	putTestStepVec3(input[8:20], state.Position)
	putTestStepVec3(input[20:32], state.Velocity)
	if state.OnGround {
		input[32] = 1
	}
	if jump {
		input[33] = 1
	}
	input[34] = byte(moveX)
	input[35] = byte(moveZ)
	putTestStepFloat(input[36:40], yawSin)
	putTestStepFloat(input[40:44], yawCos)
	putTestStepFloat(input[44:48], physics.FixedDeltaSeconds)
	for index, value := range [...]float32{
		tunables.StepHeight, tunables.WalkSpeed, tunables.GroundAcceleration,
		tunables.GroundDeceleration, tunables.AirAcceleration, tunables.JumpSpeed,
		tunables.Gravity, tunables.TerminalFallSpeed,
	} {
		putTestStepFloat(input[48+index*4:52+index*4], value)
	}
	putTestStepVec3(input[80:92], sweepMin)
	putTestStepVec3(input[92:104], sweepMax)
	for index, value := range [...]int32{prism.origin.X, prism.origin.Y, prism.origin.Z} {
		binary.LittleEndian.PutUint32(input[104+index*4:108+index*4], uint32(value))
	}
	for index, value := range prism.dimensions {
		binary.LittleEndian.PutUint32(input[116+index*4:120+index*4], value)
	}
	offset := testStepHeaderBytes
	for y := uint32(0); y < prism.dimensions[1]; y++ {
		for x := uint32(0); x < prism.dimensions[0]; x++ {
			for z := uint32(0); z < prism.dimensions[2]; z++ {
				position := core.BlockPos{
					X: prism.origin.X + int32(x),
					Y: prism.origin.Y + int32(y),
					Z: prism.origin.Z + int32(z),
				}
				set := source.CollisionBoxes(position)
				if set.Loaded {
					input[offset] = 1
				}
				count := min(int(set.Count), len(set.Boxes))
				input[offset+1] = byte(count)
				for boxIndex := range count {
					box := set.Boxes[boxIndex]
					components := [...]float32{
						box.Min.X(), box.Min.Y(), box.Min.Z(),
						box.Max.X(), box.Max.Y(), box.Max.Z(),
					}
					for componentIndex, value := range components {
						putTestStepFloat(input[offset+4+boxIndex*24+componentIndex*4:], value)
					}
				}
				offset += testStepCellBytes
			}
		}
	}
	if offset != len(input) {
		panic("test step input 编码不完整")
	}
}

type testStepPrism struct {
	origin     core.BlockPos
	dimensions [3]uint32
	cells      int
	bytes      int
}

// testStepPrismFor 复刻生产 prism 构建（bounds 版本）。
func testStepPrismFor(position, sweepMin, sweepMax mgl32.Vec3, stepHeight float32) testStepPrism {
	halfWidth := physics.PlayerWidth / 2
	minimum := mgl32.Vec3{
		position.X() + sweepMin.X() - halfWidth - physics.CollisionEpsilon,
		position.Y() + min(float32(0), sweepMin.Y(), stepHeight) - physics.GroundProbe - physics.CollisionEpsilon,
		position.Z() + sweepMin.Z() - halfWidth - physics.CollisionEpsilon,
	}
	maximum := mgl32.Vec3{
		position.X() + sweepMax.X() + halfWidth + physics.CollisionEpsilon,
		position.Y() + max(float32(0), sweepMax.Y(), stepHeight) + physics.PlayerHeight + physics.CollisionEpsilon,
		position.Z() + sweepMax.Z() + halfWidth + physics.CollisionEpsilon,
	}
	origin := core.BlockPos{
		X: int32(math.Floor(float64(minimum.X()))),
		Y: int32(math.Floor(float64(minimum.Y()))),
		Z: int32(math.Floor(float64(minimum.Z()))),
	}
	end := core.BlockPos{
		X: int32(math.Floor(float64(maximum.X()))),
		Y: int32(math.Floor(float64(maximum.Y()))),
		Z: int32(math.Floor(float64(maximum.Z()))),
	}
	dimensions := [3]uint32{
		uint32(end.X - origin.X + 1),
		uint32(end.Y - origin.Y + 1),
		uint32(end.Z - origin.Z + 1),
	}
	cells := int(dimensions[0]) * int(dimensions[1]) * int(dimensions[2])
	return testStepPrism{
		origin:     origin,
		dimensions: dimensions,
		cells:      cells,
		bytes:      testStepHeaderBytes + cells*testStepCellBytes,
	}
}

func TestStepInputLayoutV1(t *testing.T) {
	state := physics.State{
		Position: mgl32.Vec3{0.5, 1, 0.5},
		Velocity: mgl32.Vec3{4.3, -1.6, 0},
		OnGround: true,
	}
	source := testCollisionWorld{}
	prism := testStepPrismFor(state.Position, mgl32.Vec3{}, mgl32.Vec3{0.2, 0.1, 0}, physics.DefaultTunables().StepHeight)
	input := make([]byte, prism.bytes)
	testEncodeStepInputInto(
		input, prism, state, 1, -1, true,
		0.25, 0.9682458,
		physics.DefaultTunables(),
		mgl32.Vec3{}, mgl32.Vec3{0.2, 0.1, 0},
		source,
	)
	if len(input) != prism.bytes || prism.bytes != testStepHeaderBytes+prism.cells*testStepCellBytes {
		t.Fatalf("step input 长度=%d，want %d", len(input), prism.bytes)
	}
	if got := string(input[:4]); got != "MGP1" {
		t.Fatalf("magic=%q，want MGP1", got)
	}
	if got := binary.LittleEndian.Uint32(input[4:8]); got != 1 {
		t.Fatalf("layout version=%d，want 1", got)
	}
	if input[32] != 1 || input[33] != 1 {
		t.Fatalf("on_ground/jump=%v，want 1,1", input[32:34])
	}
	if input[34] != 1 || input[35] != 255 {
		t.Fatalf("move_x/move_z=%d/%d，want 1/-1", input[34], int8(input[35]))
	}
	if got := math.Float32frombits(binary.LittleEndian.Uint32(input[36:40])); math.Float32bits(got) != math.Float32bits(0.25) {
		t.Fatalf("yaw_sin bits=%08x，want %08x", math.Float32bits(got), math.Float32bits(float32(0.25)))
	}
	if got := binary.LittleEndian.Uint32(input[44:48]); math.Float32bits(math.Float32frombits(got)) != math.Float32bits(physics.FixedDeltaSeconds) {
		t.Fatalf("dt bits=%08x，want %08x", got, math.Float32bits(physics.FixedDeltaSeconds))
	}
	for index, value := range [...]float32{
		physics.DefaultTunables().StepHeight, physics.DefaultTunables().WalkSpeed,
		physics.DefaultTunables().GroundAcceleration, physics.DefaultTunables().GroundDeceleration,
		physics.DefaultTunables().AirAcceleration, physics.DefaultTunables().JumpSpeed,
		physics.DefaultTunables().Gravity, physics.DefaultTunables().TerminalFallSpeed,
	} {
		if got := math.Float32frombits(binary.LittleEndian.Uint32(input[48+index*4 : 52+index*4])); math.Float32bits(got) != math.Float32bits(value) {
			t.Fatalf("tunable[%d] bits=%08x，want %08x", index, math.Float32bits(got), math.Float32bits(value))
		}
	}
	if got := math.Float32frombits(binary.LittleEndian.Uint32(input[28:32])); math.Float32bits(got) != math.Float32bits(0) {
		t.Fatalf("velocity z bits=%08x，want +0", math.Float32bits(got))
	}
}
```

（`TestStepInputLayoutV1` 只校验测试镜像的编码布局；生产与 native 的逐位往返由同一文件下方与 Task 9 的差分断言覆盖。）

同文件追加**差分语料基线**（在旧生产上先建立"全绿基线"，翻面后这些用例成为 native 与 oracle 逐位一致的门禁）：

```go
func TestStepProductionMatchesGoIntegrationOracle(t *testing.T) {
	previousTunables := physics.ActiveTunables()
	t.Cleanup(func() { physics.SetTunables(previousTunables) })
	physics.SetTunables(physics.DefaultTunables())

	floor := func() testCollisionWorld {
		world := testCollisionWorld{}
		for x := int32(-3); x <= 3; x++ {
			for z := int32(-3); z <= 3; z++ {
				world[core.BlockPos{X: x, Y: 0, Z: z}] = physics.CollisionBoxSet{Loaded: true, Count: 1, Boxes: [8]core.AABB{fullCube}}
			}
		}
		return world
	}
	negativeZeroZ := math.Float32frombits(1 << 31)

	tests := []struct {
		name  string
		state physics.State
		input physics.Input
		world testCollisionWorld
	}{
		{name: "grounded diagonal walk", state: physics.State{Position: mgl32.Vec3{0.5, 1, 0.5}, OnGround: true}, input: physics.Input{MoveX: 1, MoveZ: 1}, world: floor()},
		{name: "grounded decel to stop", state: physics.State{Position: mgl32.Vec3{0.5, 1, 0.5}, Velocity: mgl32.Vec3{10, 0, -3}, OnGround: true}, input: physics.Input{}, world: floor()},
		{name: "jump from ground", state: physics.State{Position: mgl32.Vec3{0.5, 1, 0.5}, OnGround: true}, input: physics.Input{Jump: true, MoveX: 1}, world: floor()},
		{name: "airborne gravity", state: physics.State{Position: mgl32.Vec3{0.5, 3.2, 0.5}, Velocity: mgl32.Vec3{4, 8.4, 0}}, input: physics.Input{MoveX: -1, Yaw: 1.25}, world: floor()},
		{name: "terminal fall clamp", state: physics.State{Position: mgl32.Vec3{0.5, 40, 0.5}, Velocity: mgl32.Vec3{0, -78, 0}}, input: physics.Input{}, world: floor()},
		{name: "negative zero z velocity", state: physics.State{Position: mgl32.Vec3{0.5, 1, 0.5}, Velocity: mgl32.Vec3{0, 0, negativeZeroZ}, OnGround: true}, input: physics.Input{MoveX: 1}, world: floor()},
		{name: "unknown cell blocks path", state: physics.State{Position: mgl32.Vec3{0.5, 1, 0.5}, Velocity: mgl32.Vec3{4.3, 0, 0}, OnGround: true}, input: physics.Input{MoveX: 1}, world: testCollisionWorld{{X: 1, Y: 1, Z: 0}: {}}},
		{name: "half block step", state: groundedTowardObstacle(), input: physics.Input{MoveX: 1}, world: testCollisionWorld{
			{X: 0, Y: 0, Z: 0}: {Loaded: true, Count: 1, Boxes: [8]core.AABB{fullCube}},
			{X: 1, Y: 0, Z: 0}: {Loaded: true, Count: 1, Boxes: [8]core.AABB{fullCube}},
			{X: 1, Y: 1, Z: 0}: {Loaded: true, Count: 1, Boxes: [8]core.AABB{{Max: mgl32.Vec3{1, 0.5, 1}}}},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			testAssertProductionStepMatchesOracle(t, test.state, test.input, test.world)
		})
	}
}
```

- [ ] **Step 2: 运行测试建立基线**

Run: `go test ./internal/physics -race -count=1`
Expected: PASS。此刻生产仍是旧 Go 实现，`oracleStep` 与生产同源，差分断言全绿——这就是"翻面前的基线"。布局测试也通过（它只校验测试镜像的 ABI 布局）。

- [ ] **Step 3: 实现生产 step.go（翻面）**

```go
package physics

import (
	"encoding/binary"
	"math"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/nativeabi"
)

const (
	stepHeaderBytes  = 128
	stepOutputBytes  = 32
	stepRegularCells = 135
	stepRegularBytes = stepHeaderBytes + stepRegularCells*collisionCellBytes
)

// Step 推进一个固定步：校验与编码在 Go，积分 + 碰撞解析 + 速度裁剪在 Rust engine。
//
// 参数在函数入口取一次快照，全程使用该快照，因此单次固定步内参数不会中途变化。
func Step(state State, input Input, source CollisionSource) StepResult {
	tunables := ActiveTunables()
	validate(state, input)
	yawSin := float32(math.Sin(float64(input.Yaw)))
	yawCos := float32(math.Cos(float64(input.Yaw)))
	sweepMin, sweepMax := stepSweepBounds(state, input, tunables, yawSin, yawCos)
	prism := stepPrismFor(state.Position, sweepMin, sweepMax, tunables.StepHeight)
	var regular [stepRegularBytes]byte
	var bytes []byte
	if prism.bytes <= stepRegularBytes {
		bytes = regular[:prism.bytes]
	} else {
		bytes = make([]byte, prism.bytes)
	}
	encodeStepInput(bytes, prism, state, input, tunables, yawSin, yawCos, sweepMin, sweepMax, source)
	var output [stepOutputBytes]byte
	nativeabi.PhysicsStep(bytes, output[:])
	return decodeStepOutput(output[:])
}

// movementTargetFromYaw 与 Rust 的 movement_target 逐位一致（三角已由调用方算好）。
func movementTargetFromYaw(moveX, moveZ int8, walkSpeed, yawSin, yawCos float32) mgl32.Vec3 {
	forward := mgl32.Vec3{-yawSin, 0, -yawCos}
	right := mgl32.Vec3{yawCos, 0, -yawSin}
	intent := right.Mul(float32(moveX)).Add(forward.Mul(float32(moveZ)))
	if intent.Len() == 0 {
		return mgl32.Vec3{}
	}
	return intent.Normalize().Mul(walkSpeed)
}

// stepSweepBounds 计算积分位移的凸包界。Rust 积分后自检位移落在界内。
//
// 水平轴取 [min(0,v,t)·dt, max(0,v,t)·dt]（t 为 moveToward 后的目标速度分量）；
// 垂直轴按 jump/fallen/−terminal 分支取凸包。界必须与 Rust 积分逐位一致，
// 由 step 级差分测试锁定。
func stepSweepBounds(state State, input Input, tunables Tunables, yawSin, yawCos float32) (mgl32.Vec3, mgl32.Vec3) {
	target := movementTargetFromYaw(input.MoveX, input.MoveZ, tunables.WalkSpeed, yawSin, yawCos)
	horizontal := mgl32.Vec3{state.Velocity.X(), 0, state.Velocity.Z()}
	if state.OnGround {
		if target.Len() == 0 {
			horizontal = moveToward(horizontal, mgl32.Vec3{}, tunables.GroundDeceleration*FixedDeltaSeconds)
		} else {
			horizontal = moveToward(horizontal, target, tunables.GroundAcceleration*FixedDeltaSeconds)
		}
	} else {
		horizontal = moveToward(horizontal, target, tunables.AirAcceleration*FixedDeltaSeconds)
		if horizontal.Len() > tunables.WalkSpeed {
			horizontal = horizontal.Normalize().Mul(tunables.WalkSpeed)
		}
	}
	vx, vz := state.Velocity.X(), state.Velocity.Z()
	tx, tz := horizontal.X(), horizontal.Z()
	dt := FixedDeltaSeconds
	var minimum, maximum mgl32.Vec3
	minimum[0] = min3(0, vx, tx) * dt
	maximum[0] = max3(0, vx, tx) * dt
	minimum[2] = min3(0, vz, tz) * dt
	maximum[2] = max3(0, vz, tz) * dt
	vy := state.Velocity.Y()
	if state.OnGround && input.Jump {
		maximum[1] = tunables.JumpSpeed * dt
	} else {
		fallen := vy - tunables.Gravity*dt
		if fallen >= -tunables.TerminalFallSpeed {
			minimum[1] = min3(0, vy, fallen) * dt
			maximum[1] = max3(0, vy, fallen) * dt
		} else {
			minimum[1] = min3(0, vy, -tunables.TerminalFallSpeed) * dt
			maximum[1] = max3(0, vy, -tunables.TerminalFallSpeed) * dt
		}
	}
	return minimum, maximum
}

func min3(a, b, c float32) float32 { return min(a, min(b, c)) }
func max3(a, b, c float32) float32 { return max(a, max(b, c)) }

func encodeStepInput(
	bytes []byte,
	prism collisionPrism,
	state State,
	input Input,
	tunables Tunables,
	yawSin, yawCos float32,
	sweepMin, sweepMax mgl32.Vec3,
	source CollisionSource,
) {
	if len(bytes) != prism.bytes {
		panic("physics: step input 缓冲区长度非法")
	}
	clear(bytes)
	copy(bytes[:4], "MGP1")
	binary.LittleEndian.PutUint32(bytes[4:8], 1)
	putCollisionVec3(bytes[8:20], state.Position)
	putCollisionVec3(bytes[20:32], state.Velocity)
	if state.OnGround {
		bytes[32] = 1
	}
	if input.Jump {
		bytes[33] = 1
	}
	bytes[34] = byte(input.MoveX)
	bytes[35] = byte(input.MoveZ)
	putCollisionFloat(bytes[36:40], yawSin)
	putCollisionFloat(bytes[40:44], yawCos)
	putCollisionFloat(bytes[44:48], FixedDeltaSeconds)
	for index, value := range [...]float32{
		tunables.StepHeight, tunables.WalkSpeed, tunables.GroundAcceleration,
		tunables.GroundDeceleration, tunables.AirAcceleration, tunables.JumpSpeed,
		tunables.Gravity, tunables.TerminalFallSpeed,
	} {
		putCollisionFloat(bytes[48+index*4:52+index*4], value)
	}
	putCollisionVec3(bytes[80:92], sweepMin)
	putCollisionVec3(bytes[92:104], sweepMax)
	for index, value := range [...]int32{prism.origin.X, prism.origin.Y, prism.origin.Z} {
		binary.LittleEndian.PutUint32(bytes[104+index*4:108+index*4], uint32(value))
	}
	for index, value := range prism.dimensions {
		binary.LittleEndian.PutUint32(bytes[116+index*4:120+index*4], value)
	}

	offset := stepHeaderBytes
	for y := uint32(0); y < prism.dimensions[1]; y++ {
		for x := uint32(0); x < prism.dimensions[0]; x++ {
			for z := uint32(0); z < prism.dimensions[2]; z++ {
				position := core.BlockPos{
					X: prism.origin.X + int32(x),
					Y: prism.origin.Y + int32(y),
					Z: prism.origin.Z + int32(z),
				}
				set := source.CollisionBoxes(position)
				if set.Loaded {
					bytes[offset] = 1
				}
				count := min(int(set.Count), len(set.Boxes))
				bytes[offset+1] = byte(count)
				for boxIndex := range count {
					box := set.Boxes[boxIndex]
					components := [...]float32{
						box.Min.X(), box.Min.Y(), box.Min.Z(),
						box.Max.X(), box.Max.Y(), box.Max.Z(),
					}
					for componentIndex, value := range components {
						putCollisionFloat(bytes[offset+4+boxIndex*24+componentIndex*4:], value)
					}
				}
				offset += collisionCellBytes
			}
		}
	}
	if offset != len(bytes) {
		panic("physics: step input 编码不完整")
	}
}

func decodeStepOutput(output []byte) StepResult {
	if len(output) != stepOutputBytes || output[24]&^byte(7) != 0 ||
		output[25] > 1 || output[26] > 1 || output[27] > 1 ||
		output[28] != 0 || output[29] != 0 || output[30] != 0 || output[31] != 0 {
		panic("physics: native step output 非法")
	}
	return StepResult{
		State: State{
			Position: mgl32.Vec3{
				math.Float32frombits(binary.LittleEndian.Uint32(output[0:4])),
				math.Float32frombits(binary.LittleEndian.Uint32(output[4:8])),
				math.Float32frombits(binary.LittleEndian.Uint32(output[8:12])),
			},
			Velocity: mgl32.Vec3{
				math.Float32frombits(binary.LittleEndian.Uint32(output[12:16])),
				math.Float32frombits(binary.LittleEndian.Uint32(output[16:20])),
				math.Float32frombits(binary.LittleEndian.Uint32(output[20:24])),
			},
			OnGround: output[25] == 1,
		},
		UsedStep:   output[26] == 1,
		HitUnknown: output[27] == 1,
	}
}
```

注意：`PlayerWidth`/`PlayerHeight`/`CollisionEpsilon`/`GroundProbe` 已是导出常量；`putCollisionVec3`/`putCollisionFloat` 需从 collision.go 保留（见 Step 5）。

- [ ] **Step 4: 裁剪 motion.go**

删除 `motion.go` 中第 9–83 行的 `Step`、`movementTarget`、`moveToward`（已迁入 oracle 与 step.go），保留 `validate`/`finiteVector`/`finite`；保留 `moveToward`（stepSweepBounds 仍需要，与 oracleMoveToward 是同一实现的两份副本——生产一份、oracle 一份，符合"旧实现只活在测试里"且互不引用）。`motion.go` 最终只含 `moveToward`、`validate`、`finiteVector`、`finite` 与 import（`math`、`mgl32`）。

- [ ] **Step 5: 裁剪 collision.go**

删除生产碰撞路径专用代码：`resolveCollision`、`encodeCollisionInput`、`decodeCollisionOutput`，以及仅服务旧碰撞 ABI 的常量 `collisionHeaderBytes`、`collisionOutputBytes`、`collisionRegularCells`、`collisionRegularBytes`、`collisionMaxBytes`。保留：`collisionCellBytes`、`collisionMaxCells`（`collisionCheckedPrism` 上限校验仍用）、`collisionCheckedFloor`/`collisionCheckedDimension`/`collisionCheckedPrism`、`putCollisionVec3`/`putCollisionFloat`（step.go 复用）、`collisionPrism` 结构体。

同时更新 `collisionCheckedPrism` 的字节数公式与上限：编码字节改为 `stepHeaderBytes + cells*collisionCellBytes`，上限改为 `stepHeaderBytes + collisionMaxCells*collisionCellBytes`，panic 文案保持不变。把 `collisionPrismFor(position, displacement, stepHeight)` 改为：

```go
// stepPrismFor 按位移凸包界构建 prism；sweepMin/sweepMax 来自 stepSweepBounds。
func stepPrismFor(position, sweepMin, sweepMax mgl32.Vec3, stepHeight float32) collisionPrism {
	halfWidth := PlayerWidth / 2
	minimum := mgl32.Vec3{
		position.X() + sweepMin.X() - halfWidth - CollisionEpsilon,
		position.Y() + min(float32(0), sweepMin.Y(), stepHeight) - GroundProbe - CollisionEpsilon,
		position.Z() + sweepMin.Z() - halfWidth - CollisionEpsilon,
	}
	maximum := mgl32.Vec3{
		position.X() + sweepMax.X() + halfWidth + CollisionEpsilon,
		position.Y() + max(float32(0), sweepMax.Y(), stepHeight) + PlayerHeight + CollisionEpsilon,
		position.Z() + sweepMax.Z() + halfWidth + CollisionEpsilon,
	}
	origin := core.BlockPos{
		X: collisionCheckedFloor(minimum.X()),
		Y: collisionCheckedFloor(minimum.Y()),
		Z: collisionCheckedFloor(minimum.Z()),
	}
	end := core.BlockPos{
		X: collisionCheckedFloor(maximum.X()),
		Y: collisionCheckedFloor(maximum.Y()),
		Z: collisionCheckedFloor(maximum.Z()),
	}
	return collisionCheckedPrism(origin, [3]uint32{
		collisionCheckedDimension(origin.X, end.X),
		collisionCheckedDimension(origin.Y, end.Y),
		collisionCheckedDimension(origin.Z, end.Z),
	})
}
```

`collision.go` 的 import 删除 `encoding/binary` 与 `nativeabi`，保留 `math`、`mgl32`、`core`。

- [ ] **Step 6: 运行聚焦测试**

先更新 `TestCollisionConfiguredMaximumFitsRegularBuffer`（旧测试用的是已删除生产函数的镜像 `testCollisionPrismFor`）：改为调用 Task 8 Step 1 定义的 `testStepPrismFor(mgl32.Vec3{0, 64, 0}, mgl32.Vec3{0, -10, 0}, mgl32.Vec3{1, 0, 1}, 1.5)`，断言改为 `prism.cells <= testStepRegularCells && prism.bytes <= testStepRegularBytes`（108 cells / 128+108×196 bytes）；删除不再被引用的 `testCollisionPrismFor` 助手。

Run: `go test ./internal/physics ./internal/nativeabi -race -count=1`
Expected: PASS。关键既有门禁逐条核对（差分断言与 4096/4097-cell 用例都在本包内跑）：
- `TestStepProductionMatchesGoIntegrationOracle`（Task 8 新基线）与 `TestStepProductionMatchesGoCollisionOracle` / `...DeterministicCorpus`：生产 Step（native 积分）与 oracleStep 逐位一致；
- `TestCollisionSnapshotAllows4096Cells`：仍查询 4096 cells、位置 {254.8, 1.1, 0.5}；
- `TestCollisionSnapshotRejects4097BeforeQuery`：仍以"physics: collision prism 超过 4096 cells"panic 且 0 查询；
- `TestDiagonalInputAcceleratesWithoutDiagonalBoost`、`TestJumpAndGravityUseFixedConstants`、step_test.go 全部行为用例；
- `TestCollisionSnapshotUsesYXZOrderAndQueriesEachCellOnce`：查询顺序 Y/X/Z 不变。

若任何一项失败，先核对 Rust 积分与 Go oracle 的运算顺序差异，Rust 对齐 Go 现行为（禁止放宽断言）。

- [ ] **Step 7: 提交**

```bash
git add internal/physics/step.go internal/physics/step_native_test.go internal/physics/motion.go internal/physics/collision.go
git commit -m "feat: 生产 Step 切到 native physics_step"
```

---

### Task 9: step 级差分语料扩展

**Files:**
- Modify: `internal/physics/step_native_test.go`

**Interfaces:**
- Consumes: Task 7 的 `oracleStep`、`testAssertProductionStepMatchesOracle`；Task 8 的生产 `Step`。

- [ ] **Step 1: 写失败测试（新差分语料）**

追加到 `step_native_test.go`（基线用例已在 Task 8 落地，本任务扩展边界语料与随机差分）：

```go
func TestStepProductionMatchesGoIntegrationOracleExtended(t *testing.T) {
	previousTunables := physics.ActiveTunables()
	t.Cleanup(func() { physics.SetTunables(previousTunables) })
	physics.SetTunables(physics.DefaultTunables())

	floor := func() testCollisionWorld {
		world := testCollisionWorld{}
		for x := int32(-3); x <= 3; x++ {
			for z := int32(-3); z <= 3; z++ {
				world[core.BlockPos{X: x, Y: 0, Z: z}] = physics.CollisionBoxSet{Loaded: true, Count: 1, Boxes: [8]core.AABB{fullCube}}
			}
		}
		return world
	}

	tests := []struct {
		name  string
		state physics.State
		input physics.Input
		world testCollisionWorld
	}{
		{name: "airborne walk speed clamp", state: physics.State{Position: mgl32.Vec3{0.5, 5, 0.5}, Velocity: mgl32.Vec3{30, 0, -20}}, input: physics.Input{MoveX: 1, MoveZ: 1, Yaw: 0.75}, world: floor()},
		{name: "jump into ceiling", state: physics.State{Position: mgl32.Vec3{0.5, 1, 0.5}, OnGround: true}, input: physics.Input{Jump: true}, world: testCollisionWorld{
			{X: 0, Y: 0, Z: 0}: {Loaded: true, Count: 1, Boxes: [8]core.AABB{fullCube}},
			{X: 0, Y: 3, Z: 0}: {Loaded: true, Count: 1, Boxes: [8]core.AABB{fullCube}},
		}},
		{name: "yaw extreme", state: physics.State{Position: mgl32.Vec3{0.5, 1, 0.5}, OnGround: true}, input: physics.Input{MoveX: 1, MoveZ: 1, Yaw: -3.1415927}, world: floor()},
		{name: "negative zero x velocity", state: physics.State{Position: mgl32.Vec3{0.5, 1, 0.5}, Velocity: mgl32.Vec3{math.Float32frombits(1 << 31), 0, 0}, OnGround: true}, input: physics.Input{}, world: floor()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			testAssertProductionStepMatchesOracle(t, test.state, test.input, test.world)
		})
	}

	random := rand.New(rand.NewSource(991))
	for range 128 {
		world := floor()
		for x := int32(-2); x <= 2; x++ {
			for z := int32(-2); z <= 2; z++ {
				if random.Intn(4) == 0 {
					world[core.BlockPos{X: x, Y: 1, Z: z}] = physics.CollisionBoxSet{Loaded: true, Count: 1, Boxes: [8]core.AABB{{Max: mgl32.Vec3{1, float32(random.Intn(2)+1) / 2, 1}}}}
				} else if random.Intn(17) == 0 {
					world[core.BlockPos{X: x, Y: 1, Z: z}] = physics.CollisionBoxSet{}
				}
			}
		}
		state := physics.State{
			Position: mgl32.Vec3{float32(random.Intn(41)-20)/10 + 0.5, 1, float32(random.Intn(41)-20)/10 + 0.5},
			Velocity: mgl32.Vec3{float32(random.Intn(161)-80)/10, float32(random.Intn(161)-80)/10, float32(random.Intn(161)-80)/10},
			OnGround: random.Intn(2) == 0,
		}
		input := physics.Input{
			MoveX: int8(random.Intn(3) - 1),
			MoveZ: int8(random.Intn(3) - 1),
			Jump:  random.Intn(2) == 0,
			Yaw:   float32(random.Intn(629)-314) / 100,
		}
		t.Run("random", func(t *testing.T) {
			testAssertProductionStepMatchesOracle(t, state, input, world)
		})
	}
}
```

注意：`step_native_test.go` 顶部 import 需补 `math/rand`。

- [ ] **Step 2: 运行测试确认通过**

Run: `go test ./internal/physics -race -count=1`
Expected: PASS（新语料在生产已切 native 后直接起门禁作用；任何逐位漂移在此失败）。

- [ ] **Step 3: 提交**

```bash
git add internal/physics/step_native_test.go
git commit -m "test: 扩展 step 级差分语料"
```

---

### Task 10: 收尾验证与基线文档

**Files:**
- Modify: `openspec/changes/rust-engine-physics-step/tasks.md`（勾选 2–10）
- Modify: `docs/notes/progress.md`（当前基线追加 physics step 段）
- Modify: `AGENTS.md`、`CLAUDE.md`、`openspec/config.yaml`（物理所有权句子同步）

- [ ] **Step 1: 全量 Rust 验证**

Run: `make rust-check`
Expected: cargo fmt --check、clippy -D warnings、cargo test --workspace --locked 全部通过。

- [ ] **Step 2: 全量 Go 验证**

```bash
go test ./internal/archcheck -count=1
go test ./... -race
go vet ./...
gofmt -l .
```

Expected: archcheck 通过；`go test ./... -race` 全绿；`go vet` 无输出；`gofmt -l .` 无输出。

- [ ] **Step 3: 性能记录（只记录，不设阈值）**

```bash
go test ./internal/physics -run '^$' -bench . -benchmem -count=1
go run ./cmd/perfcheck --help   # 先看参数说明，再按说明跑基准对比
```

Expected: 命令成功退出；把本次数值与迁移前数值对照记录到 `docs/notes/perf-baseline-m5.md` 备注（数值只记录，不改变门禁与退出状态）。

- [ ] **Step 4: OpenSpec 严格校验**

Run: `openspec validate --all --strict --no-interactive`
Expected: 全部通过。

- [ ] **Step 5: 更新基线文档**

`docs/notes/progress.md` 当前基线追加一段（第 17 行 raycast 段之后）：

```markdown
当前 `mornlea_engine` 同时是物理 tick 积分的唯一生产实现；Go `physics.Step` 保留输入校验、tunables 快照、yaw 三角、位移凸包 sweep bounds 与 prism 编码，旧 Go 积分只作测试 oracle，生产无 fallback。
```

`AGENTS.md` 与 `CLAUDE.md`（同一句子）中：

> Rust 是 mesh/light 与 collision resolver 的唯一生产实现，Go 仍拥有 app、world、sim、network、storage、render、物理 state/input/tunable/snapshot 编码和领域 API

改为：

> Rust 是 mesh/light、collision resolver、raycast 与物理 tick 积分的唯一生产实现，Go 仍拥有 app、world、sim、network、storage、render、物理 state/input/tunable/snapshot 编码、yaw 三角与 prism 构建，旧 Go 积分只作测试 oracle

`openspec/config.yaml` 中同样的句子与第 9 行 `physics 拥有确定性物理状态与 snapshot 编码并调用 Rust collision resolver` 改为：

> physics 放确定性物理状态与 snapshot 编码，积分/碰撞/裁剪调用 Rust engine；sim 放权威模拟

- [ ] **Step 6: 勾选 tasks.md 并提交**

`openspec/changes/rust-engine-physics-step/tasks.md` 勾选条目 2–10（条目 1 在 Task 1 已勾选）。

```bash
git add -A openspec/changes/rust-engine-physics-step docs/notes/progress.md AGENTS.md CLAUDE.md openspec/config.yaml
git commit -m "docs: 收尾 rust-engine-physics-step 基线更新"
```

- [ ] **Step 7: 归档前终检（留给评审/后续归档 change）**

Run: `openspec validate --all --strict --no-interactive && git status --short`
Expected: 校验通过；工作区仅剩用户既有的无关改动（midscene_run 日志、mcgo）。本计划不执行 archive（归档由独立 change 流程处理）。
