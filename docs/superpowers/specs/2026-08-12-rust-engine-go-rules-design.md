# Rust 引擎与 Go 规则分层设计

日期：2026-08-12

## 1. 背景

当前 `minecraft-go` 以 Go 1.26 实现客户端、权威服务端、世界状态、物理、网格、网络、存储与 WebGPU 渲染。M4O 已完成职责化代码整理，当前主线基线为 `2346ded`。客户端目前以 Apple Silicon/macOS 为图形验收平台，Linux 无图形专用服务端继续作为受支持构建目标。

本设计确定长期语言边界：Rust 最终持有完整底层引擎，Go 只保留上层游戏规则。迁移必须渐进执行，每个阶段都保持现有游戏可运行，不以一次性重写替换当前实现。

这个目标横跨多个独立子系统，不作为单个 change 实施。本设计只详细定义第一阶段“Rust 基础设施与 mesh/light 迁移”；后续阶段只记录顺序和边界，每阶段仍需单独设计、OpenSpec change、计划与评审。

## 2. 已确认决策

- Rust 最终拥有主循环、可变世界和玩家状态、调度、物理、网格与光照、渲染、网络和存储。
- Go 最终只实现方块、物品、配方、交互、伤害等纯游戏规则。
- 最终由 Rust 可执行程序作为主进程，静态链接 Go 规则库。
- Rust 与 Go 使用版本化 C ABI 和批量消息交互，不使用细粒度 getter/setter 回调，也不拆成两个进程。
- 采用渐进替换；每次只迁移一个可独立验证的子系统。
- 第一阶段先支持 Apple Silicon/macOS 客户端；现有 Linux 无图形服务端继续可用。
- 第一批迁移 `internal/mesh` 的贪心网格、天空光和静态方块光。
- 第一阶段完成时 Rust 是生产默认实现，旧 Go 实现只编译进测试作为临时 oracle。
- canonical 构建入口改为 Makefile、CI 与 Hook：先构建 Rust，再运行 Go；不提交预编译 native library。

## 3. 目标

### 3.1 长期目标

- 建立 Rust 引擎、Go 规则的稳定单向所有权边界。
- 让 Rust 独占全部可变引擎状态和平台资源生命周期。
- 让 Go 规则能够以确定性批输入/批输出方式独立测试。
- 在迁移期间保持协议、存档、视觉、权威语义和现有命令可用。

### 3.2 第一阶段目标

- 建立最小 Cargo workspace、C ABI、Go 绑定和可重复构建链。
- Rust 实现现有 mesh/light 算法并成为默认生产路径。
- 保持 `internal/mesh` 的现有 Go 调用方和 `[]mesh.Quad` 输出契约。
- 证明 Rust 与旧 Go oracle 的 packed quad 数量、顺序和每一位完全一致。
- 保持多 worker 并发、固定 scratch 容量、视觉结果和错误语义。

## 4. 非目标

第一阶段不做以下工作：

- 不迁移物理、世界生成、世界状态、模拟、网络、存储、渲染或主循环。
- 不引入 ECS、脚本语言、插件系统或通用游戏引擎框架。
- 不重新设计 block、section、quad 或 registry 的游戏语义。
- 不修改协议版本、存档 schema、benchmark scenario、视觉 golden 或性能 baseline。
- 不要求第一阶段产生性能提升；性能数值仅记录。
- 不使用 Rust/Go 共享对象图、跨语言 goroutine/thread 回调或长期裸指针。
- 不加入 Protobuf、FlatBuffers、cbindgen、bindgen 或异步运行时；第一阶段 ABI 足够小，手写并验证即可。
- 不提交 `.a`、`.dylib`、Cargo `target/` 或其他平台构建产物。

## 5. 最终架构

### 5.1 Rust 所有权

最终 Rust 引擎拥有：

- 进程入口与主循环；
- 可变世界、区块、实体和玩家状态；
- tick 调度、并发任务与资源生命周期；
- 物理、碰撞、射线、网格和光照；
- 客户端渲染、GPU、窗口和输入；
- 网络传输、协议编解码、存档与恢复。

### 5.2 Go 所有权

最终 Go 规则库拥有纯规则代码和不可变规则数据：

- 方块、物品与配方规则；
- 放置、采掘、容器、伤害与死亡判定；
- 其他不执行 I/O、不持有权威状态的玩法计算。

Go 规则库不得启动后台 goroutine、读写文件或网络、持有 Rust 内存地址，亦不得成为第二份权威状态。

### 5.3 最终调用模型

Rust 把一批已排序的规则事件编码为 `EventBatch`，Go 在一次 ABI 调用中计算并返回 `CommandBatch`。Rust 验证命令并应用到唯一权威状态。批量协议包含显式版本、长度和确定性排序，不使用逐实体 FFI 调用。

迁移前期仍由现有 Go 程序启动，并调用 Rust native library。迁移到状态层后使用同样的批量数据约定；最后才把进程入口反转为 Rust 可执行程序静态链接 Go 规则库。入口反转不得重新发明第二套语言协议。

## 6. 分阶段路线

### 阶段 1：Rust 基础设施与 mesh/light

建立 Cargo、ABI 和构建链；Rust 接管贪心网格、AO、天空光和方块光。本文详细定义该阶段。

### 阶段 2：纯计算模块

依次迁移碰撞/射线、物理和世界生成。Go 仍持有权威状态，每个模块继续使用批量快照输入与确定性输出。

### 阶段 3：状态与规则分离

Rust 接管 world/player 可变状态和 deterministic step executor；Go 规则正式收敛为 `EventBatch → CommandBatch`。此阶段必须单独设计事件身份、命令校验和回放语义。

### 阶段 4：网络、存储与服务端运行时

Rust 接管 transport、wire codec、存档和服务端调度，保持现有协议、schema、fixture、故障注入与 Memory/TCP parity。

### 阶段 5：客户端、渲染与入口反转

Rust 接管 client application、`gfx/render`、窗口和主循环，静态链接 Go 规则库；删除剩余 Go 引擎实现。

阶段 2–5 不在本文预定义 crate、类型或 API，避免为未来制造错误抽象。

第一阶段实施前必须建立独立 OpenSpec change，建议名称为 `m4p-rust-engine-mesh`；后续阶段不得复用该 change 扩大范围。

## 7. 第一阶段代码组织

建议新增：

```text
engine/
  Cargo.toml
  Cargo.lock
  rust-toolchain.toml
  crates/
    mcgo_mesh/
      Cargo.toml
      src/
        lib.rs
        ffi.rs
        light.rs
        greedy.rs
        quad.rs
  include/
    mcgo_engine.h
```

第一阶段只有一个 Rust crate。`mcgo_mesh` 输出 static library；不建立通用 `engine-core`、抽象 backend 或共享 utility crate。

Go 侧继续由 `internal/mesh` 拥有对外入口。只有该包的 native bridge 可以接触 C ABI；`internal/client`、`internal/render` 与 `cmd/` 不直接调用 Rust 符号。

## 8. 第一阶段 ABI

### 8.1 单次调用

每个 section 使用一次 ABI 调用，逻辑等价于：

```text
status = mcgo_mesh_section(
    abi_version,
    input_ptr, input_len,
    scratch_ptr, scratch_len,
    output_ptr, output_capacity,
    output_len_ptr,
)
```

ABI 只暴露整数、指针和长度。`mcgo_engine.h` 是 C 声明的唯一来源；Rust 和 Go 各自使用编译期尺寸检查及运行时 ABI 版本检查。

### 8.2 所有权

- Go 为 input、scratch 和 output 分配并复用固定缓冲区。
- Rust 只在调用期间借用这些缓冲区，不保存地址。
- Rust 不返回需由 Go 释放的内存。
- Go 不读取 Rust 对象或保存 Rust 指针。
- Rust 稳定路径不执行 heap allocation。

### 8.3 输入

输入为显式版本化的小端字节布局，包含：

- `3×3×3` section neighborhood 的方块 ID；
- 9 个 height map 及 presence 位；
- 当前 section Y；
- 不可变 `MeshRegistrySnapshot`。

`MeshRegistrySnapshot` 只表达网格所需信息：已注册 ID、opaque、emission、六个面的 material，以及已注册 ID 之间的 face visibility。它在 Go 侧从现有 registry 生成，Rust 不回调 `Registry` 方法。

第一阶段给 `mesh.Registry` 增加必需的 snapshot 方法；`assets.Registry` 与测试 registry 都显式实现。现有 `Opaque`、`FaceVisible`、`Material` 和 `Emission` 方法暂时保留，供 connectivity 与 test-only Go oracle 使用。生产 mesh 不允许因为 registry 缺少 snapshot 而回落到 Go。

未知 block ID、缺失 section 和缺失 height map 必须保持当前语义。快照的排序、重复 ID、范围和 emission 上限在跨 ABI 前后都要验证。

### 8.4 scratch 与输出

scratch 至少覆盖现有两组固定容量状态：

- `48³` 个 packed light level；
- `48³` 个传播队列项。

输出由 Go 预留最多 `6 × 16³ = 24576` 个 `uint64`。Rust 按当前 face、slice、row 和 greedy merge 顺序写入 `Quad.Pack()` 的精确位布局。超过容量视为实现错误并立即失败，不截断、不重新分配。

Go 在调用成功后使用现有 `UnpackQuad` 转成 `[]Quad`，因此当前 client/render API 和 GPU 数据格式保持不变。这个额外解包只属于迁移阶段；是否在 Rust 接管 renderer 后直接消费 packed quad，由未来阶段决定。

## 9. Go 适配层

`MeshSection` 的外部行为保持不变。现有 `LightScratch` 继续作为每个 mesher worker 独占的复用对象，但内部改为持有：

- ABI 输入缓冲；
- Rust light/queue scratch；
- 固定 packed quad 输出；
- registry snapshot 缓存。

同一个 `LightScratch` 不允许被并发使用；多个 worker 的 scratch 完全独立。Rust ABI 本身无全局可变状态，因此不同 worker 可并发调用。

旧 Go light/greedy 实现移入 test-only 文件并重命名为 oracle。生产构建不存在静默 Go fallback；Rust 失败时沿用当前 mesher panic 恢复与重新排队路径。

## 10. 错误模型

Rust ABI 至少区分：

- ABI 版本不匹配；
- 输入或 scratch 长度错误；
- registry snapshot 非法；
- emission 超过 15；
- quad 输出溢出；
- Rust 内部 panic。

所有 `extern "C"` 入口必须捕获 panic，禁止 unwind 穿过 C ABI。Go 将状态码映射为稳定的中文错误或当前已有 panic 文本；第一阶段不修改 `MeshSection` 返回签名。OOM 等无法可靠恢复的进程级故障不制造虚假的 fallback。

输入错误不得产生部分有效结果。失败时 `output_len` 必须为 0，调用方不得消费 output 内容。

## 11. 构建与开发流程

Go 工具链不会自动编译 Rust static library。第一阶段把以下入口设为 canonical：

```bash
make build
make test
make test-race
```

`rust-toolchain.toml` 固定经过验收的 Rust 版本。上述入口先运行 `cargo build --locked`，再运行对应 Go 命令。CI 和共享 Hook 使用同一构建步骤，避免本机与 CI 各自维护脚本。Rust 产物写入忽略目录，不能提交平台二进制。

执行过 Rust 构建后，开发者仍可直接运行受影响 Go package 的 focused `go test`。干净 checkout 的文档和 AGENTS 必须明确先使用 canonical 入口。

第一阶段 Rust crate 为纯 CPU 代码，应能在 CI host 上构建和测试；Apple Silicon/macOS 是客户端正式验收平台。Linux `cmd/mcgod` 继续保持无图形能力，不得因 mesh static library 引入 WebGPU、窗口或 Rust runtime 启动依赖。

## 12. 测试策略

### 12.1 Rust 单元测试

Rust 直接覆盖：

- light index 与边界；
- 天空光和方块光传播；
- `48³` 最坏队列容量；
- AO 四角编码；
- face、material、尺寸与 packed quad 位布局；
- greedy merge 顺序；
- 非法长度、版本、emission 和输出容量；
- panic 不跨 ABI。

### 12.2 跨语言 parity

test-only Go oracle 与 Rust 默认实现对同一输入运行，要求 packed quads 的数量、顺序和全部 64 位完全一致。语料至少包含：

- 现有全部 mesh/light 测试夹具；
- 所有已注册方块和六个面；
- air、opaque、glass、leaves 和未知 ID；
- 缺邻区、缺 height map、区段上下边界；
- 单光源、最坏多光源和日光传播；
- 固定种子生成的随机 neighborhood；
- 多 worker 并发调用。

现有测试名尽量保留并改为验证 Rust 生产路径。涉及旧 Go 内部 scratch 字段的断言，在 Rust 单测中锁定相同不变量，Go 侧继续锁定可观察结果和 ABI 状态。

### 12.3 仓库门禁

第一阶段至少运行：

```bash
cargo fmt --check
cargo clippy --workspace --all-targets -- -D warnings
cargo test --workspace --locked
make test-race
go test ./internal/archcheck -count=1
go vet ./...
gofmt -l .
openspec validate --all --strict --no-interactive
git diff --check
```

还必须运行现有 mesh benchmark、客户端 mesher focused 测试和 10 个无窗口视觉场景。不得更新 golden 或性能 baseline。

## 13. 性能与资源契约

第一阶段不以性能提升作为完成条件，benchmark 和 perfcheck 数值只保存记录。以下仍是正确性门禁：

- Rust 稳定路径意外 heap allocation；
- scratch 或 quad output 真实 overflow；
- ABI 输入重复分配导致现有有界 worker 失效；
- 输出、视觉、报告身份或固定 artifact 漂移；
- 数据截断、I/O 错误或构建产物缺失。

邻域展平和 Go 侧 `UnpackQuad` 的复制是已知过渡成本。后续 Rust 接管 world/render 后可以消除，但第一阶段不为减少复制而共享 Go 对象内存。

## 14. 兼容性

- 游戏行为、quad 顺序、AO/light、GPU packed layout 和视觉结果保持不变。
- 不改变网络协议、packet ID、存档 schema、fixture 或 world metadata。
- 不改变 benchmark scenario、workload、阈值或 baseline。
- 不改变客户端 mesher 的 generation、dirty/requeue 或 worker 数量语义。
- 不改变 Linux 无图形服务端的网络、存储和模拟实现。

ABI v1 只用于同一仓库内同步构建的 Go/Rust 组件；版本不匹配直接失败，不提供跨发布 native library 兼容承诺。

## 15. 回退与 oracle 删除

第一阶段按“构建链 → ABI → Rust light → Rust greedy → 默认切换”的顺序拆成可独立验证提交。任一提交出现无法解释的 parity、visual、overflow、构建或并发失败时，回退该提交；不得更新期望值或启用静默 Go fallback。

旧 Go oracle 不在第一阶段删除。第一阶段合入 `main` 后，在干净 checkout 上再次完成全部 Rust、Go、视觉和 OpenSpec 门禁，再以独立 change/提交删除 oracle。这样 Rust 生产切换与历史实现删除能够分别评审和回退。

## 16. 风险与取舍

- **跨语言复制成本**：第一阶段每个 section 展平 neighborhood 并解包输出。接受该成本以换取清晰所有权；不共享 Go 内存对象。
- **构建流程变复杂**：clean checkout 需要 Cargo。统一由 Makefile、CI 和 Hook 封装，不提交 native artifact。
- **语义重复期间漂移**：Go oracle 与 Rust 实现短期并存。oracle 只编译进测试，并通过逐位 parity 约束；稳定后单独删除。
- **FFI 错误难诊断**：状态码、ABI 版本、输入长度和固定错误文本必须进入测试；禁止仅返回布尔值。
- **平台链接差异**：第一阶段只承诺 Apple Silicon/macOS 客户端正式验收，pure CPU crate 仍需在 CI host 构建；后续扩平台单独设计。
- **过早抽象**：第一阶段只有一个 crate、一个 mesh ABI 和一个 Go bridge；不为尚未迁移的子系统预建框架。

## 17. 被否决的方案

### 细粒度函数 FFI

为每个 block/entity 暴露 getter、setter 和回调会扩大 ABI、增加跨边界往返并制造生命周期问题，不采用。

### Rust 与 Go 双进程

本地 IPC 能隔离崩溃，但会增加部署、延迟、断线和重启状态；对单机游戏与高频规则交互没有必要，不采用。

### 一次性重写

同时重写渲染、服务端、网络和存储无法保持现有功能持续可用，也无法定位行为漂移，不采用。

### 提交预编译 native library

平台二进制会扩大仓库、引入来源与兼容问题，也违反可重复构建原则，不采用。

### 默认 Go fallback

生产失败后静默回到 Go 会掩盖 Rust 路径缺陷，并让两套引擎长期并存，不采用。

## 18. 第一阶段完成条件

- Cargo workspace、locked 依赖、C ABI 和 canonical 构建入口可在干净 checkout 重现。
- Rust 是 `MeshSection` 的唯一生产实现；Go 实现只存在于 test-only oracle。
- 所有 parity 语料的 packed quad 输出逐位一致。
- 现有 mesh/client/render 行为、10 个视觉场景和固定 artifact 不漂移。
- 多 worker 并发、panic 隔离、scratch/output 容量和 mesher requeue 语义保持成立。
- Rust fmt/clippy/test、Go race/vet/gofmt、archcheck、OpenSpec strict 和 diff check 全部通过。
- 性能记录完整且身份正确，不以修改基线或阈值掩盖变化。
- 独立 review 未发现 ABI 所有权、数据截断、无界分配或静默 fallback。
