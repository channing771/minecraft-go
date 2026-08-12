## Why

Go currently owns every engine layer. M4P establishes the first reversible Rust boundary by migrating only deterministic section meshing and propagated light.

## What Changes

- Add a pinned Rust workspace and Rust-first canonical build chain.
- Make Rust the only production implementation of mesh/light.
- Keep the old Go implementation test-only as an exact oracle.
- Preserve all existing gameplay, wire, storage, visual, concurrency and packed-quad behavior.

## Non-Goals

- No physics, world, sim, network, storage, render or process-entry migration.
- No production Go fallback, native binary commits or performance threshold changes.

## Capabilities

### New Capabilities

- `rust-engine-mesh`: Rust 生产网格与光照在既有可观察输出、并发和构建边界内替换 Go 实现。

### Modified Capabilities

- 无。

## Impact

- 受影响包：`internal/mesh` 及其 test-only Go oracle、`internal/client` 的既有 mesher 调用路径、Makefile、CI 与 Hook 构建入口。
- 新增受版本锁定的 Rust static library 构建链和窄 C ABI；Go 仍持有输入、scratch 与输出缓冲区。
- 不改变游戏玩法、线上协议、存档 schema、benchmark scenario、性能阈值或 GPU packed quad 格式；性能数值仅记录。
- Linux 无图形 `cmd/mcgod` 保持不依赖 CGO、Rust static library、WebGPU 或窗口包；多 worker 继续使用独占 scratch，不共享可变 native 状态。
