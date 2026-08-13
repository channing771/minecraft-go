## MODIFIED Requirements

### Requirement: ABI 失败不得产生部分网格
系统 MUST 通过 `mornlea_engine.h` 中的 `MORNLEA_ENGINE_*`、`MORNLEA_STATUS_*`、`mornlea_engine_abi_version` 与 `mornlea_mesh_section` 提供唯一 native ABI，并对版本、结构长度、非空区段使用的 registry、emission、output overflow 与 Rust panic 返回可判定失败。ABI version MUST 保持 `1`，status 数值 MUST 保持 `0..9`。

#### Scenario: 非法输入被原子拒绝
- **WHEN** native 调用收到任一非法输入
- **THEN** output length MUST 为 0
- **AND** panic/unwind MUST NOT 穿过 C ABI

#### Scenario: 全空气区段不读取 registry 语义
- **GIVEN** native 输入的 magic、长度、bounded count、visibility 行宽、presence 位与借用范围结构合法
- **AND** center section 的所有方块都等于 header 声明的 AirID
- **WHEN** registry 排序、air/barrier identity、opacity、emission 或 required-ID 语义本会校验失败
- **THEN** native 调用 MUST 在 registry 语义与传播光照之前返回成功
- **AND** output length MUST 为 0
- **AND** light scratch MUST 保持不变

#### Scenario: 旧 native 身份不再导出
- **WHEN** 检查发布的 header 与 dylib symbols
- **THEN** MUST 只存在 Mornlea C ABI 身份且 MUST 不存在旧 `mcgo` C symbol

### Requirement: clean checkout 使用 Rust-first 构建
系统 MUST 通过 canonical Make、CI 与 Hook 从 `engine/` workspace root 使用固定的 Rust 1.97.1，在 Go 验证前执行 `cargo build --locked --release` 构建 pinned Rust `cdylib`；workspace MUST 仅含 `mornlea_mesh`，并且该 crate 的 normal dependency MUST 只使用 `std`。

#### Scenario: 无预编译 artifact 的构建
- **GIVEN** clean checkout 不含 Cargo target 或 native library
- **WHEN** 运行 `make test-race`
- **THEN** 系统 MUST 先在 `engine/` 目录中以 Rust 1.97.1 执行 `cargo build --locked --release`，再执行 Go race tests
- **AND** `cd engine && rustup show active-toolchain` MUST 报告 `1.97.1` directory override
- **AND** `cargo metadata --no-deps --format-version 1 --manifest-path engine/Cargo.toml` MUST 只报告 workspace member `mornlea_mesh`
- **AND** `cargo tree --manifest-path engine/Cargo.toml --workspace --edges normal` MUST 只含 workspace root，且不得报告第三方 dependency

#### Scenario: 本地客户端产物不依赖 Cargo target 位置
- **GIVEN** `make build` 已生成本地客户端产物
- **WHEN** 临时移开 `engine/target`
- **THEN** `bin/mornlea` MUST 从同目录加载 `libmornlea_mesh.dylib` 并进入 Go 参数解析
- **AND** `bin/mornlea -h` MUST 以 exit 1 和 `flag: help requested` 诊断证明该解析路径已运行
- **AND** 输出 MUST 不含 `dyld` 或 `Library not loaded`
- **AND** 构建产物 MUST 不包含指向临时 Cargo `deps` 目录的 dylib load path

### Requirement: Rust 客户端边界不污染无图形服务端
系统 MUST 保持 `cmd/mornlea-server` 不依赖 CGO、Rust `cdylib`、WebGPU 或窗口包。

#### Scenario: Linux 无 CGO 构建
- **GIVEN** clean checkout 没有 Rust build artifact
- **WHEN** 以 `CGO_ENABLED=0 GOOS=linux` 构建 `./cmd/mornlea-server`
- **THEN** 构建 MUST 成功且依赖闭包 MUST 不包含 client、mesh、render 或 gfx
