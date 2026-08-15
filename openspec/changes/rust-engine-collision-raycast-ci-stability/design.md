## Context

见 [proposal.md](proposal.md) 的动机。本 change 以 `3c23b03` 批准设计为唯一实现边界：当前 Rust 1.97.1 `mornlea_mesh` cdylib 已持有 mesh/light，Go 持有权威世界、玩家状态、physics、raycast、网络、存储和 tick。GitHub run `31813426121` 与 `31813364557` 是 CI source RED；没有确定性的本地同构复现，后续 mutation 只证明同步边和 kernel 语义确实被测试覆盖，不能替代这两条 RED。

## Goals / Non-Goals

**Goals:**

- 仅用测试同步边修复两个已记录 CI fixture：关闭 TCP client 后等待 Host 的两个 active index 删除；Memory client 在 `waitReady` 前启动 reader 并登记 cleanup。生产登录、心跳、20ms/150ms 与既有 deadline 均不变。
- 将一个 std-only Rust crate/dylib 原子改名为 `mornlea_engine`，让 mesh、collision、raycast 共享 ABI v1 的一个部署单元；旧 mesh symbol、layout、status `0..9` 不变。
- 让 Go 持有 input、scratch、cursor、output、world/玩家状态与 callback，Rust 只借用一次调用内数据并执行无全局可变状态的 collision/DDA kernel。
- 发布 macOS 同目录 dylib 与 Linux amd64 同目录 server/so bundle，并以 native Linux CI 验证 `$ORIGIN` 加载和无图形依赖。

**Non-Goals:**

- 不引入 Rust-owned world/mirror、world ownership、通用 snapshot、第二个 crate/dylib、ECS、async runtime、backend interface、pool、生产 Go fallback 或运行时开关。
- 不迁移完整 physics 积分、worldgen、网络、存储、渲染或主循环；不处理 50ms probe、一般 deadline cleanup、其他 liveness wait、workflow retry/去重。
- 不修改 protocol v16、玩家/区块/伙伴/world metadata schema、scenario v16、M2 v15/M5 v14 baseline、11 张 visual golden、capture fixture、性能阈值或 baseline。

## Decisions

### CI fixture 同步

`mustCloseMultiplayerTCPClient` 只关闭 endpoint/receiver，不能证明 Host 已运行 deferred `releaseLogin`。重连 fixture 复用 `waitForPlayerReleased`，它同时检查 `activeByPlayer` 与 `activeBySession`；不新增 sleep、重试或 production hook。健康 Memory endpoint 的 `Recv` reader 会驱动 KeepAlive reply，因此固定为 `LoginClient → monitorEndpointProgress/Recv → register cleanup → waitReady`；不改变 heartbeat 或等待值。

### 一个 crate、一个 bridge、两个 ABI

workspace 只保留 `mornlea_engine` 与 `std` normal dependency。`internal/nativeabi` 是唯一可 `import "C"`、include `mornlea_engine.h`、链接 native library、检查 ABI/status 的叶子包；`core`、`physics`、`mesh` 只编码/解释各自 POD。该包不依赖领域包，架构守卫登记 `core/physics/mesh → nativeabi`。所有 native entry point 经已审计的 Go 1.26 `#cgo noescape`/`#cgo nocallback`；Rust 不保存 Go 地址、不回调 Go、不启动线程、不返回待释放对象。

collision 与 raycast 是独立 ABI：collision 需要 swept AABB dense prism，raycast 需要按 DDA 顺序的 cursor batch。把它们并入通用 world-query 只会让一方携带另一方的冗余状态，故不采用。

### Collision snapshot、layout 与发布

Go 在调用前读取唯一 tunables、校验领域输入、计算 displacement，并以 checked arithmetic 计算完整 prism。每个 cell 仅查询一次且按 Y/X/Z 编码；原始 `CollisionBoxSet.Count` 先 clamp 到 8。超过 4096 cells、整数溢出或不可表示坐标在 source 查询、分配、FFI 和 State 发布前稳定 panic；常规 135-cell 路径使用栈上 `26524` bytes，136..4096 才精确分配，无截断、pool 或 fallback。

collision input 为 little-endian：64-byte header（`MGC1`、layout v1、position/displacement、began_grounded、step_height、prism origin/dimensions）加每 cell 196 bytes（loaded、box_count、reserved、8 个 6-float AABB），扁平索引 `((y*dimX)+x)*dimZ+z`。output 固定 16 bytes：position、X/Y/Z clipped mask、on_ground、used_step、hit_unknown。Rust 以 Y/X/Z、unknown closed boundary、`Nextafter` 安全距离和严格更大水平进度完成 ordinary/step；候选 step 被拒绝时丢弃其 `HitUnknown`。Go 只有成功解码完整 output 后才发布 State；旧 Go kernel 仅移入 `_test.go` oracle。

### Raycast cursor、layout 与惰性

Go 保留有限值/正距离/callback 校验、一次 nested `math.Hypot` 归一化、callback 顺序和 `RayHit.Point`；Rust 只产生 record。input 固定 40 bytes（`MGR1`、layout v1、origin、normalized direction、max distance、reserved）；cursor 固定 64 bytes（`MRC1`、layout/state、cell、step、t_delta、t_max、reserved）；record 为 20 bytes（block、face、reserved、distance），output 固定 64 records/1280 bytes。

Rust 对一个 caller-owned cursor 最多生成 64 条，使用负坐标 floor、严格 `<` 以保留 X/Y/Z tie、精确 endpoint inclusion 和 int32 wrapping；floor 超出 int32 可表示域时统一映射为 `MinInt32`，不依赖目标平台的 float-to-int 转换。首条是 origin/FaceNone/0。Go 逐条验证并调用 callback，首个 error 原样返回，首个 solid 立即返回，绝不消费 batch 余项；跨 batch 无新 distance cap。cursor/output 先在 Rust local storage 中完成，再一次发布。

公开 input 的 origin、direction 与 max distance 仍必须有限；test-only Go oracle 除用最小 helper 明确上述越界 floor 规则外，继续机械保留旧 DDA。若这些有限值在冻结 float32 DDA 的 boundary/delta 运算中可推导出 `-Inf`/`NaN` cursor 或 record，Rust 只放行与同一 input 相符的该类状态，Go 仍按 oracle 顺序先调用对应 callback。cursor delta 必须与同一 input 的 `1/abs(direction)` 逐 bit 相同，普通输入下伪造的非有限 cursor mutation 继续拒绝，不能把可推导溢出泛化为关闭数值校验。

### ABI 原子性、平台与兼容

Rust 与 Go 双层检查 ABI version、magic/layout、长度、对齐、范围、overlap、reserved bytes、cursor state 与数值域。invalid input/panic 均不发布 collision output、raycast output/cursor 或 Go State；当 raycast 两个 result metadata 指针有效时先清零 `output_count`/`done`。panic 用 `catch_unwind` 围住，绝不越过 C ABI。ABI major 固定 `1`，mesh 的 `mornlea_engine_abi_version`、`mornlea_mesh_section`、layout/status 保持，collision/raycast 只追加 symbol。

Rust 在清零 raycast result metadata 前 MUST 对每个非空 input/cursor/output 按固定的 40/64/1280-byte footprint 完成 checked range 与 metadata overlap 检查，不得使用 caller-supplied length。

macOS 以 `@rpath` 与 `@loader_path` 发布 `bin/mornlea`、`bin/mornlea-server` 和相邻 `libmornlea_engine.dylib`。Linux amd64 原生构建 `mornlea-server + libmornlea_engine.so`，ELF runpath 为 `$ORIGIN`；专服允许 Rust/CGO，但依赖闭包仍排除 client、mesh、render、gfx、WebGPU、GLFW、字体和窗口包。文件名切换是原子发布；旧 binary/new library 不能混装。

### 被否决的替代方案

- 通用 world-query snapshot：collision/raycast 的访问模型不同，只有表面复用。
- Rust 只读 world mirror：引入同步、generation、失效和生命周期，超出纯 kernel 范围。
- 多 crate/dylib：复制 ABI、runpath、发布和错误边界，当前无收益。
- 继续使用 `mornlea_mesh`：第二个领域进入后职责名已失真。
- CGO0 server fallback：形成双生产实现与持续 parity 负担。
- 同批迁移完整 physics/worldgen：引入跨平台浮点与大量非 kernel 所有权问题。

## Risks / Trade-offs

- [CI 调度没有本地同构 RED] → 把两个 GitHub run 的精确断言/耗时记录为 source RED，并以 release/reader mutation 证明新增边有实际约束。
- [ABI/layout 漂移或部分发布] → 固定字节 layout、canary/overlap/panic 测试、逐位 oracle 与只在完整成功后发布。
- [平台加载或图形泄漏] → macOS/Linux 脱离 Cargo target smoke、symbol/runpath/`go list -deps` 审计和 Ubuntu native CI job。
- [热路径分配或实现重复] → 栈缓冲与 `AllocsPerRun` 门禁；旧 Go 算法仅测试 oracle，绝不作为 fallback。
- [性能波动] → benchmark 仅记录；报告完整性、真实 overflow、数据丢失、I/O、native loading 与正确性失败仍阻断。

## Migration Plan

1. 先提交 active OpenSpec contract，再单独修复两个 fixture，同步不影响生产行为。
2. 纯改名为 `mornlea_engine` 并在无新 kernel 时证明 mesh/build/visual 无漂移；再建立唯一 nativeabi bridge。
3. 先增加 collision ABI、oracle/parity/atomicity，再将 `physics.Step` 切至 Rust 并交付 Linux bundle；随后以同一方式迁移 raycast。
4. 在冻结 producer HEAD 上做 race、fuzz、benchmark、visual 与 macOS 证据，独立评审后由真实 Linux PR-CI 完成最终 gate。
5. 任一 parity、race、allocation、visual 或加载失败，仅回退对应生产 switch；不得启用 Go fallback、更新期望值或放宽门禁。归档时才将 delta 合入主 specs。
