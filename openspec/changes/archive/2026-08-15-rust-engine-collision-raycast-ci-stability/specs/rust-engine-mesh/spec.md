## MODIFIED Requirements

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
