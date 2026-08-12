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
