## Why

PR #41 的同一代码树在 GitHub Actions 中出现两条已核实的调度失败：run `31813426121` 的 `TestReconnectContinuesWorldTimeWithoutRollback` 以“玩家已在线”拒绝同身份重连，run `31813364557` 的 `TestHostHeartbeatTimeoutCleanupIsIsolated` 在 30 秒后报告 `player did not become ready`。同时，现有 Rust 生产边界只覆盖 mesh/light，碰撞与射线仍留在 Go；本 change 在先修复这两条真实同步边界的前提下，将这两个纯 kernel 纳入同一受控 native 部署单元。

## What Changes

- 为同身份 TCP 重连与健康 Memory endpoint 建立测试所需的 happens-before，同步边界只覆盖两个已记录 CI 失败。
- 将唯一 Rust crate/dylib 从 `mornlea_mesh` 原子收敛为 `mornlea_engine`，保持现有 mesh ABI v1、symbol、layout 与 status `0..9` 不变。
- 以独立的 additive ABI v1 分别提供 collision 和 raycast；Rust 成为二者唯一生产 kernel，Go 继续拥有权威状态、snapshot/cursor、callback 和结果发布。
- 交付 macOS 相邻 `libmornlea_engine.dylib`，以及 Linux amd64 相邻 `mornlea-server + libmornlea_engine.so` bundle；两者必须作为同一发布单元升级。
- 为 collision snapshot 的 4096-cell 上限、raycast 的 64-record batch、失败原子性、跨平台逐位 parity、零分配热路径和 Linux native bundle 建立验证。

## Non-Goals

- 不修复 50ms benchmark probe，也不开展一般 deadline cleanup、其他一秒等待治理、workflow retry 或去重。
- 不迁移完整 physics 积分、worldgen、网络、存储、渲染、主循环，且不创建 Rust-owned world、通用 world-query、ECS、async runtime、backend interface、第二个 crate/dylib 或生产 Go fallback。
- 不以性能提升为完成条件，不通过更新 baseline、golden、scenario 或阈值吸收差异。

## Compatibility

- ABI major 仍为 `1`；现有 `mornlea_engine_abi_version`、`mornlea_mesh_section`、mesh layout 与 status 数值保持不变，collision/raycast 仅追加独立 ABI。
- protocol v16、玩家/区块/伙伴/world metadata storage schema、scenario v16、M2 v15/M5 v14 baseline、11 张 visual golden 与 capture fixture 保持不变。
- library 文件名原子切换；旧 Go binary 与新 library 不支持混装。Linux 专服从单个 CGO0 binary 改为 binary + 相邻 `.so` bundle，仍保持无客户端、无 WebGPU、无窗口依赖。

## Affected Areas

- `internal/server` 的两个测试 fixture；不修改生产登录、心跳或 deadline 行为。
- `engine/`、`engine/include/mornlea_engine.h`、`internal/nativeabi`、`internal/mesh`、`internal/physics` 与 `internal/core` 的 native kernel/bridge 边界。
- `Makefile`、GitHub Actions、架构检查、Hook 与当前构建/发布文档。

## Capabilities

### New Capabilities

- `rust-engine-collision-raycast`: Rust collision 与 raycast 的可观察计算、资源、惰性和 ABI 原子性契约。

### Modified Capabilities

- `rust-engine-mesh`: Rust-first 单 crate 构建身份与 Linux headless server 原生 bundle 契约。
- `project-identity`: 当前 Mornlea native library 身份与 macOS/Linux 发布 artifact 契约。

## Impact

该 change 跨 Rust、Go、CGO、构建和 CI，但保持服务端的世界/玩家状态所有权与公开 `physics.Step`、`core.RaycastBlocks` 签名。先以测试同步修复可靠门禁，再按可独立回退的 rename、bridge、collision、raycast 与平台 bundle 阶段实施；不改变 wire、存储和冻结 artifact 身份。
