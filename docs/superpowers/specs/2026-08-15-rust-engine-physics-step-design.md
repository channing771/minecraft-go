# 2026-08-15 rust-engine-physics-step 设计

状态：已获用户批准（架构级 brainstorming 三节设计逐节确认）。

## 1. 背景与目标

Rust engine 迁移序列：M4P 把 mesh/light 迁入 `mornlea_mesh`，上一波把 collision resolver
与 raycast DDA 迁入改名的 `mornlea_engine`，并确立"Go 只留领域 API 与编码、旧 Go kernel
降级为逐位奇偶 oracle"的模式。本设计是序列的下一步：把物理 tick 的**积分**部分
（加速度/摩擦/重力/跳跃/速度裁剪）从 Go 迁入 Rust，让物理核心整体位于 engine。

目标：

- 新增单个 native 入口 `mornlea_physics_step`，一次调用完成积分 + 碰撞解析 + 速度裁剪；
- 可观察行为与现有 Go `physics.Step` **逐位一致**（float32 bit 级），协议、存档、场景零变化；
- Go 保留领域 API、State/Input/Tunables 类型、snapshot 编码、prism 构建与输入校验。

非目标：不改行为数值；不动 mesh/raycast/collision ABI 的既有消费者；不动 sim 装配与
tick 结构；不引入生产 Go fallback；不动存档与线上协议。

## 2. 现状边界（同步 main 至 6c50c02 后核实）

- Go 生产：`internal/physics/motion.go`（100 行）做 movementTarget / moveToward /
  加速摩擦 / 跳跃重力 / 位移与速度裁剪；`internal/physics/collision.go`（214 行）做
  prism 构建、196 字节/cell 编码、16 字节输出解码；`internal/nativeabi` 是唯一 C ABI 接触点。
- Rust 生产：`engine/crates/mornlea_engine/src/collision.rs`（669 行）已有碰撞 kernel，
  `ffi.rs` 已有 `mornlea_collision_resolve` 等入口。
- 调用方：`internal/sim/player.go`、`internal/client/predictor_advance.go`、
  `internal/client/predictor_reconcile.go`，都通过公开 `physics.Step` 使用，签名不变即可零改动。
- 门禁：碰撞差分测试用 `math.Float32bits` 逐位比较 native 与 Go oracle
  （`internal/physics/collision_native_test.go`）；`motion_test.go` 含
  `TestJumpAndGravityUseFixedConstants` 的精确相等断言；4096-cell 精确 prism 用例
  （`TestCollisionSnapshotAllows4096Cells`，速度 5088.5 → 256×16×1）必须保持通过。

## 3. 关键决策

### 3.1 三角函数留在 Go

Go oracle 的 `movementTarget` 用 `math.Sin(float64(yaw))`（Go fdlibm 实现）。Rust 的
`f32::sin`/`f64::sin` 与 Go `math.Sin` 可能差最后一位，逐位奇偶门禁会被打破。决策：
Go 每步算一次 `yaw_sin`/`yaw_cos`（两个 float32）传入 native；Rust 只用传入值做向量运算。
剩余运算（加减乘除、sqrt）均为 IEEE 确定性：sqrt 双舍入安全（Go 的
`float32(math.Sqrt(float64(x)))` 与 Rust `f32::sqrt` 同为正确舍入），可保持逐位一致。

### 3.2 单次 physics_step + 保留碰撞差分入口

新增 `mornlea_physics_step` 一次完成积分+碰撞+裁剪；生产路径不再调用
`mornlea_collision_resolve`，但该入口与 `nativeabi.CollisionResolve` 绑定保留，
供现有 680 行碰撞级差分测试继续使用。否决的两段式（integrate 单独小 ABI + 现有
collision，每 tick 两次 native 调用）整合收益小；否决"碰撞差分上移"（只留 physics_step）
会丢失细粒度碰撞边界用例的可达性。

### 3.3 sweep bounds 悖论与凸包解

prism 必须由 Go 在调用前构建（世界数据在 Go 侧），而 prism 边界取决于积分后的位移——
积分迁入 Rust 后 Go 不再知道精确位移。解法：Go 计算**位移扫描界（sweep bounds）**构建
prism，Rust 积分后自检位移确实落在界内，违约返回 `StatusInput`（不静默漂移）。

- 简单对称界 `±max(|v|, walkSpeed)·dt` 不可行：`TestCollisionSnapshotAllows4096Cells`
  （v=5088.5）会被撑成 511 列 × 16 行而 panic，打破既有门禁。
- 正确界是凸包式单侧界：水平轴 `[min(0, v, t)·dt, max(0, v, t)·dt]`（t 为目标速度分量，
  因 moveToward 结果落在 v 与 t 之间），垂直轴取
  `[min(0, vy, vy−g·dt, −terminal, jump)·dt, max(...)]`。该界精确复现 256×16×1。
- 代价：Go 保留约 15 行运动目标/垂直速度算式专用于算界（movementTarget 用传入的
  yaw_sin/cos、moveToward、重力裁剪）；差分测试锁定该算式与 Rust 积分一致。

### 3.4 tunables 每步传快照

Tunables 是运行时可变参数（`SetTunables` 原子换指针，调试面板可调），Rust 不能硬编码。
决策：Go 在 Step 入口取 `ActiveTunables()` 快照，把 8 个相关参数作为 f32 写入 header；
Rust 只消费传入值，不持有任何参数状态。

## 4. ABI 契约：mornlea_physics_step

```
Status mornlea_physics_step(uint32 abi_version,
                            const uint8_t* input, size_t input_len,
                            uint8_t* output, size_t output_len)
```

与现有 collision/raycast 入口同风格：caller-owned 缓冲区、返回 `Status`、Rust 侧
catch_unwind 映射 `StatusPanic`、Go 侧 status→中文 panic 文案。

### 4.1 输入：128 字节 header + 每 cell 196 字节

| 偏移 | 大小 | 字段 |
|---|---|---|
| 0 | 4 | magic `MGP1` |
| 4 | 4 | layout u32 = 1 |
| 8 | 12 | position f32×3（脚底中心） |
| 20 | 12 | velocity f32×3 |
| 32 | 1 | on_ground u8（0/1） |
| 33 | 1 | jump u8（0/1） |
| 34 | 1 | move_x i8（two's complement 原样写入，值 ∈ [−1,1]） |
| 35 | 1 | move_z i8（同上） |
| 36 | 4 | yaw_sin f32（Go `math.Sin` 计算） |
| 40 | 4 | yaw_cos f32（Go `math.Cos` 计算） |
| 44 | 4 | fixed_delta_seconds f32 |
| 48 | 32 | tunables f32×8：step_height、walk_speed、ground_acceleration、ground_deceleration、air_acceleration、jump_speed、gravity、terminal_fall_speed |
| 80 | 24 | sweep bounds f32×6：dx_min、dx_max、dy_min、dy_max、dz_min、dz_max（凸包式，见 3.3） |
| 104 | 12 | prism origin i32×3 |
| 116 | 12 | prism dimensions u32×3 |
| 128 | — | cell ×196 字节，沿用现有格式（loaded/count/reserved/8×AABB，little-endian） |

约束（沿用现状）：cell 数 ≤ 4096；input_len 必须等于 128 + cells×196；所有 float 必须有限；
on_ground/jump ∈ {0,1}；sweep bounds 每轴 min ≤ max。

### 4.2 输出：固定 32 字节

| 偏移 | 大小 | 字段 |
|---|---|---|
| 0 | 12 | position f32×3 |
| 12 | 12 | velocity f32×3（裁剪后完整速度） |
| 24 | 1 | clipped mask（bit 0/1/2 = X/Y/Z） |
| 25 | 1 | on_ground |
| 26 | 1 | used_step |
| 27 | 1 | hit_unknown |
| 28 | 4 | reserved = 0 |

### 4.3 语义

与现有 Go `Step` 逐位一致：`movementTarget`（用传入 yaw_sin/cos）→ `moveToward` →
空中速度钳制 → 跳跃/重力 → 位移 → 现有 collision kernel（Y/X/Z、unknown closed
boundary、Nextafter 安全距离、step 判定）→ 按 clipped mask 清零对应速度分量 → 输出。
Rust 在碰撞前自检：积分位移必须落在 sweep bounds 内（含 NaN 拒绝），否则 `StatusInput`。

### 4.4 ABI version

`mornlea_engine.h` 增加声明，`MORNLEA_ENGINE_ABI_VERSION` +1；Go binary 与
`libmornlea_engine.so` 仍为不可跨版本混装的 release unit（`$ORIGIN` 约定不变）。

## 5. 两侧改造

### 5.1 Go 生产侧（internal/physics、internal/nativeabi）

- `motion.go`：积分主体删除。`Step` 变为薄封装：`validate` → tunables 快照 →
  `math.Sin/Cos` 算 yaw_sin/yaw_cos → 用保留的算式算 sweep bounds → 构建 prism →
  编码 128 字节 header + cell → 一次 `nativeabi.PhysicsStep` → 解码 32 字节 → 返回
  `StepResult`。`Step` 签名与 `StepResult` 语义不变，三个调用方零改动。
- `collision.go`：prism 构建/校验（4096 cell 上限、panic 文案"physics: collision prism
  超过 4096 cells"等）保留复用；仅服务旧 collision ABI 的生产编码/解码函数删除
  （测试文件自带独立副本，不受影响）。
- `nativeabi/native.go`：新增 `PhysicsStep(input, output []byte)`，cgo pragma
  `noescape`/`nocallback` 与现有条目一致；status→中文 panic 文案映射模式复用。

### 5.2 oracle 迁移

- 旧积分逻辑（movementTarget / moveToward / 加速摩擦 / 跳跃重力 / 速度裁剪）移入
  `internal/physics/motion_oracle_test.go`，与现有 `oracleResolveCollision` 合成
  Go oracle 版 `oracleStep`。
- 新增 `step_native_test.go`：随机+边界输入下 `physics.Step`（native）与 `oracleStep`
  逐位比较 position 与 velocity（`math.Float32bits`），严格度与现有碰撞差分同级；
  `physics_fuzz_test.go` 沿用。

### 5.3 Rust 侧（engine/crates/mornlea_engine/）

- 新增 `step.rs`：解析校验（magic/layout/长度/有限性/sweep bounds 自检）→ 积分
  （运算顺序逐条对齐 Go oracle，见 3.3）→ 调用现有 `collision.rs` resolver → 速度裁剪 →
  输出编码。
- `ffi.rs` 新增 `mornlea_physics_step`（catch_unwind 护栏）；`lib.rs` 注册模块；
  `mornlea_engine.h` 同步声明并 bump ABI version；`mornlea_collision_resolve` 保留并
  注释为测试专用入口。

## 6. 测试与验证

新增：

- Rust 单元测试：step 输入解析/边界校验（magic 错、长度错、bounds 违约、NaN）、
  积分算式锚定用例（`make rust` 内含 cargo test）。
- Go step 级差分：`step_native_test.go` 逐位比较；`motion_oracle_test.go` 承载旧积分 oracle。
- nativeabi 布局测试：header/output 各字段偏移与 bit 级断言（沿用
  `TestCollisionInputLayoutV1` 模式）。

保留门禁（必须原样通过）：碰撞级差分 680 行、`TestCollisionSnapshotAllows4096Cells`、
`TestCollisionSnapshotRejects4097BeforeQuery`、`TestDiagonalInputAcceleratesWithoutDiagonalBoost`、
`TestJumpAndGravityUseFixedConstants`、`physics_fuzz_test.go`、`internal/archcheck`。

验证顺序（AGENTS.md 由小到大）：

```bash
make rust
go test ./internal/physics ./internal/nativeabi -race -count=1
go test ./internal/archcheck -count=1
go test ./... -race
go vet ./...
gofmt -l .          # 应无输出
openspec validate --all --strict --no-interactive
```

性能只记录：`go test ./internal/physics -run '^$' -bench . -benchmem` 与
`cmd/perfcheck` 前后数值对照（不设硬阈值）。

## 7. 兼容性、风险与回退

### 7.1 兼容性

- 纯实现位置迁移，零行为变化：协议 v16、玩家 schema v6、区块 schema v8、世界 metadata
  v2、`companions.ai` v1、benchmark scenario v16 不动；M2 v15/M5 v14 基线保持原字节。
- 现有 ABI（mesh v1、raycast、collision）保留，仅新增入口；engine ABI version +1。
- 架构边界不变：唯一 C ABI 接触点仍是 `internal/nativeabi`；单机/远程复用同一实现；
  专服无图形与 `$ORIGIN` release unit 约定不变。

### 7.2 风险

1. **逐位奇偶漂移（最大风险）**：Rust 积分运算顺序必须与 Go oracle 完全一致
   （Go/Rust 编译器默认都不重组浮点；sqrt 正确舍入无双重舍入问题；sin/cos 已留 Go）。
   缓解：差分逐位断言 + fuzz；关键算式两侧注释锚定运算顺序；不一致时 Rust 对齐 Go
   现行为，而非反向。
2. **sweep bounds 误判**：界错导致合法输入被 `StatusInput` 拒绝或更糟。
   缓解：凸包公式由差分测试覆盖极端输入（5088.5 用例必须保持通过）；Go 侧 panic 门禁不动。
3. **性能回退**：header 增加 80 字节、输出 32 字节，编码量略增；每 tick 仍一次 native
   调用。缓解：benchmark/perfcheck 记录数值对照。
4. **Rust panic 跨 FFI**：catch_unwind + `StatusPanic` → 中文 panic 文案，与现有模式一致。
5. **平台差异（arm64 奇偶 vs amd64 1 ulp）**：奇偶差分门禁只在 arm64（开发机与 macOS CI）
   运行；amd64 上 Go bounds/oracle 不收缩而 Rust mul_add 融合（IEEE 正确舍入，跨平台一致），
   二者可差 ≤1 ulp，由 prism 1e-5 epsilon 边距与自检 1 ulp 余量兜底；生产跨平台确定性由
   Rust 单一内核保证。
6. **非有限 tunables 行为变化**：旧 Go 对非有限 tunables 静默产出 NaN；新路径 Rust 校验在
   输入阶段拒绝（fail-fast，返回 StatusInput 而非 NaN 结果）。

### 7.3 回退

- 单 PR 粒度：回退 = revert 合并提交，恢复"Go 积分 + native collision"的先前生产布局
  （不是新增 fallback）。
- oracle 本身就是旧实现的完整副本：任何时刻可把 oracle 移回生产文件恢复旧路径，
  这是结构性安全网。
- 阶段门禁逐级通过才推进，失败不静默跳过。

## 8. OpenSpec 落地

按 AGENTS.md，本变更是跨包重构，创建新 OpenSpec change `rust-engine-physics-step`：

- `proposal.md`：背景、目标、非目标、影响包（internal/physics、internal/nativeabi、
  engine/）、兼容性说明；
- delta spec `specs/rust-engine-physics-step/spec.md`：可观察行为与 Given/When/Then
  场景（生产路径单一 native 积分、逐位奇偶、运行时 tunables 生效、sweep bounds 违约
  拒绝、旧 collision 入口仅测试）；
- `design.md`：本设计的 OpenSpec 精简版（ABI 布局、数据所有权、依赖方向）；
- `tasks.md`：按 5/6 节拆分、可勾选、每项带验证命令。

实现经 tasks 逐项完成后归档到 `openspec/specs/`。
