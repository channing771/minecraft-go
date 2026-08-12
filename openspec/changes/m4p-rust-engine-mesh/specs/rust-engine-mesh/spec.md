## Purpose

在保持现有游戏、协议、存档、视觉和资源上限不漂移的前提下，以可验证且可回退的 Rust 生产边界接管确定性区段网格和传播光照。

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
系统 MUST 通过 canonical Make、CI 与 Hook 在 Go 验证前构建 pinned Rust static library。

#### Scenario: 无预编译 artifact 的构建
- **GIVEN** clean checkout 不含 Cargo target 或 native library
- **WHEN** 运行 `make test-race`
- **THEN** 系统 MUST 先执行 locked Rust build 再执行 Go race tests

### Requirement: Rust 客户端边界不污染无图形服务端
系统 MUST 保持 `cmd/mcgod` 不依赖 CGO、Rust static library、WebGPU 或窗口包。

#### Scenario: Linux 无 CGO 构建
- **GIVEN** clean checkout 没有 Rust build artifact
- **WHEN** 以 `CGO_ENABLED=0 GOOS=linux` 构建 `./cmd/mcgod`
- **THEN** 构建 MUST 成功且依赖闭包 MUST 不包含 client、mesh、render 或 gfx
