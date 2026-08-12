## Context

当前 `internal/mesh` 的 Go 实现同时承担贪心网格、AO、天空光与静态方块光。M4P 的动机见 [proposal.md](proposal.md)。本 change 仅建立 Rust-first 构建链及 mesh/light 的可回退生产边界；`cmd/mcgod` 仍必须可作为无 CGO 的 Linux 专用服务端构建。

## Goals / Non-Goals

**Goals:**

- 以一个 pinned Rust workspace、一个 `mcgo_mesh` static library 和一个版本化 C ABI 替换生产 mesh/light，实现既有 `[]mesh.Quad` 输出契约。
- 固定 Go 对 input、scratch 和 packed quad output 的所有权，且使 Rust 调用无全局可变状态。
- 保留 Go 实现为仅测试编译的精确 oracle，以逐位 parity 锁定迁移行为。

**Non-Goals:**

- 不引入后续阶段的 Rust 主循环、可变世界、规则 ABI、renderer 直连 packed quad 或跨语言回调 API。
- 不迁移 physics、world、sim、network、storage、render 或进程入口，也不引入 Go production fallback。

## Decisions

### Rust 仅拥有确定性 mesh/light 算法

`engine/rust-toolchain.toml` 必须固定 Rust 1.97.1。新增且仅新增一个 `engine/crates/mcgo_mesh` crate，输出 static library；它不得引入第三方 Rust dependency，只使用 `std`。Go 的 `internal/mesh` 继续持有对外入口；只有该包 native bridge 接触 C ABI，`internal/client`、`internal/render` 和 `cmd/` 不直接调用 Rust 符号。Rust 接管贪心网格、AO、天空光和静态方块光，旧 Go 实现移动为 test-only oracle。

采用该边界是因为它可独立验证且保持现有调用方和 `[]mesh.Quad` API 不变。否决通用 engine crate、抽象 backend 与细粒度 getter/setter FFI：它们会在未迁移子系统上预建抽象，并扩大 ABI 和生命周期风险。

### ABI v1 使用一次 section 调用与 Go-owned 固定缓冲区

ABI 只暴露整数、指针和长度；`engine/include/mcgo_engine.h` 是 C 声明唯一来源。一次 section 调用接收 ABI 版本、显式版本化小端输入、Go 分配的 scratch 与 output buffer，并写入 output length。Rust 只在调用期间借用指针，不保存地址、不返回需 Go 释放的内存，稳定路径不执行 heap allocation。

输入包含 `3×3×3` section neighborhood、9 个带 presence 位的 height map、section Y 与只读 `MeshRegistrySnapshot`。Go 侧从现有 registry 缓存 snapshot；Rust 不回调 `Registry`。scratch 固定覆盖 `48³` packed light level 和 `48³` 队列项；output 固定最多 `6 × 16³ = 24576` 个 `uint64`。成功后 Go 用既有 `UnpackQuad` 恢复 `[]Quad`。

选择复制与固定缓冲区是为了明确单一所有权及保留有界 worker；否决共享 Go 对象内存和 Rust 分配返回值，二者都会制造跨语言生命周期或释放责任。以后 renderer 迁移前不增加直接消费 packed quad 的 API。

### 失败以原子 ABI 状态码表达

ABI 显式区分 ABI 版本、input/scratch 长度、非法 registry snapshot、emission 超过 15、quad output overflow 和 Rust panic。所有 `extern "C"` 入口捕获 panic，禁止 unwind 穿过 C ABI；任一失败把 output length 置零，Go 映射为稳定中文错误或既有 panic 文本，调用方不得消费 output。

这比 bool 或生产 Go fallback 更可诊断，并避免隐蔽地绕过 Rust 路径。OOM 等进程级故障不伪造可恢复 fallback。

### 并发边界维持每 worker 独占 scratch

`LightScratch` 仍是单 worker 复用对象，内部换为 ABI input、Rust light/queue scratch 与 packed output。相同 scratch 不可并发使用；不同 worker 的 scratch 完全独立。Rust ABI 无全局可变状态，因此不同 worker 调用可并发且确定。

### canonical 构建先锁定 Rust 再验证 Go

`engine/rust-toolchain.toml` 固定 Rust 1.97.1。`make build`、`make test` 与 `make test-race` 先执行精确命令 `cargo build --locked --release`，再执行对应 Go 命令；CI 与共享 Hook 使用同一构建步骤。native 产物写入 ignored 目录，不提交 `.a`、`.dylib` 或 Cargo `target/`。清洁 checkout 可用 canonical 入口重建；已构建后仍可直接运行 focused Go tests。

选择统一 Make/CI/Hook 是为避免本机和 CI 的构建漂移；否决提交预编译 library 与独立手工流程，因为它们破坏可重复构建。Linux `cmd/mcgod` 保持无 CGO，并不得获得 Rust、WebGPU 或窗口依赖。

## Risks / Trade-offs

- [跨语言展平与解包产生迁移期开销] → 保留该有界复制以换取所有权清晰；性能只记录，不改 baseline 或阈值。
- [双实现短期语义漂移] → Go oracle 仅在测试中编译，并以固定夹具、随机输入和并发 parity 逐位比较。
- [ABI 错误难定位] → 锁定版本、长度、registry、emission、overflow 与 panic 状态，且失败不暴露部分 output。
- [平台链接差异] → Apple Silicon/macOS 是客户端正式验收平台；pure CPU crate 在 CI host 构建，Linux 专用服务端继续不链接 Rust。
- [固定容量不足或意外分配] → overflow 立即失败且测试覆盖 `48³` 最坏队列与 `24576` output 上限；不截断或扩容。

## Migration Plan

1. 先落地 pinned workspace、locked 构建链和 CI/Hook 复用，确认 clean checkout 不需要预编译产物。
2. 添加 ABI 和 Go bridge，以 test-only oracle 固定输入、输出、错误与并发契约。
3. 分别迁移 Rust light 与 greedy mesh，再切换 Rust 为唯一生产路径；每一步保留逐位 parity、无窗口视觉与既有 mesher requeue 语义。
4. 任何无法解释的 parity、visual、overflow、构建或并发失败均回退相应独立提交；不得更新期望值或启用 Go fallback。
5. Rust production 切换合入后，在 clean checkout 重跑 Rust、Go、视觉与 OpenSpec 门禁；oracle 删除另开 change/提交评审。

## Compatibility and Verification

不改变网络协议、packet ID、存档 schema、fixture、world metadata、benchmark scenario、workload、阈值、visual golden、GPU packed layout 或客户端 mesher generation/dirty/requeue/worker 语义。ABI v1 仅面向同一仓库同步构建的组件，版本不匹配直接失败，不承诺跨发布 native library 兼容。

实现阶段至少验证 Rust `fmt`/`clippy`/测试、Go parity 与 race、`make test-race` clean checkout、`CGO_ENABLED=0 GOOS=linux go build ./cmd/mcgod`、`go test ./internal/archcheck -count=1`、`go vet ./...`、`gofmt -l .`、既有 mesh benchmark 与 10 个无窗口视觉场景，以及 OpenSpec strict 和 diff check。
