# Rust Collision/Raycast 与 CI 稳定化设计

日期：2026-08-14

## 1. 背景

当前 `main@88558088beecf74d76669d23d83622358b87a73f` 已合入 M5A。Rust 1.97.1
目前只通过 `engine/crates/mornlea_mesh` 的单一 `cdylib` 接管 mesh、AO、天空光和
静态方块光；Go 仍持有权威世界、玩家状态、physics、raycast、网络、存储和主循环。

PR #41 的同一代码树在 GitHub Actions 中出现两种偶发失败，而合并后的同树运行又成功：

- run `31813426121` 的 `TestReconnectContinuesWorldTimeWithoutRollback` 在关闭旧客户端后
  立即用同一身份登录，被 Host 以“玩家已在线”拒绝；
- run `31813364557` 的 `TestHostHeartbeatTimeoutCleanupIsIsolated` 在 30 秒后报告健康玩家
  未 Ready；
- 合并后 run `31814398960` 通过，且失败与成功运行使用相同 tree；结合失败断言与代码中
  缺失的同步边界，判定问题来自测试缺失 happens-before，而不是代码树差异。

长期设计已经确定 Rust 最终持有底层引擎，阶段 2 从纯计算模块开始。本设计把 CI 根因
修复与下一批 Rust 下沉放在同一个 OpenSpec change 中，但仍按严格先后顺序实施：先让
门禁可靠，再用可靠门禁验收 Rust 迁移。

## 2. 已确认决策

- 使用一个 combined change：`rust-engine-collision-raycast-ci-stability`。
- CI 只修 PR #41 实际出现的两个偶发失败；不夹带 50ms benchmark 契约旧债或全仓
  deadline 清理。
- 本轮同时迁移 collision 和 raycast，但使用两个独立 ABI，不制造通用 world-query
  格式。
- 现有 `mornlea_mesh` crate/dylib 原子重命名为单一 `mornlea_engine`；不新增第二个
  Rust crate 或动态库。
- Apple Silicon/macOS 客户端继续使用同目录 `.dylib`；Linux amd64 CI 原生构建
  headless server 与同目录 `.so` bundle。
- `mornlea-server` 从本阶段起允许依赖 Rust/CGO，但仍不得依赖客户端、WebGPU、窗口、
  `gfx` 或 `render`。
- 不再承诺从 macOS 以 `CGO_ENABLED=0 GOOS=linux` 直接交叉编译单文件专服。
- Go 继续持有全部可变世界、玩家状态和权威 tick；Rust 只执行无全局可变状态的纯
  kernel。
- Rust 是唯一生产实现；Go collision/DDA 只允许作为 `_test.go` oracle，不提供生产
  fallback。
- C ABI major 保持 `1`。现有 mesh symbol、布局和 status 数值不变；collision/raycast
  是 additive entry points，各自使用独立 v1 input/cursor layout。

## 3. 目标与非目标

### 3.1 目标

- 消除同身份 TCP 重连测试与心跳隔离测试的调度竞态，不放宽生产或测试期限。
- 把现有 Rust 产物从 mesh 专用身份收敛为可继续承载底层 kernel 的 engine 身份。
- Rust 接管玩家移动中的碰撞解析、跨步选择、ground probe 与 unknown 边界判定。
- Rust 接管体素 raycast 的 DDA traversal，同时保持现有 Go callback 的惰性、顺序和
  error identity。
- 保持客户端预测和服务端权威使用同一生产 kernel。
- 证明 macOS arm64 与 Linux amd64 上的确定性输出、资源上限和现有 gameplay 行为不
  漂移。

### 3.2 非目标

- 不迁移完整 `physics.Step` 的输入积分、加速度、重力、tunables 或三角函数。
- 不迁移 worldgen、网络、存储、渲染、主循环或 Rust-owned world snapshot。
- 不建立 ECS、async runtime、backend interface、通用 engine-core 层或多个 native
  library。
- 不修改协议 v16、区块/玩家/伙伴/world metadata schema、fixture、scenario v16、
  性能 baseline、visual golden 或阈值。
- 不修复 bounded benchmark 50ms tick 越界的独立契约偏差。
- 不统一 M5A 或其他测试中的自定义一秒 liveness wait。
- 不以性能提升作为完成条件；性能值只记录。

## 4. 总体架构与实施顺序

同一个 change 内按以下顺序实施，每一阶段形成独立可回退提交：

1. 建立 OpenSpec 契约并修复两项 CI fixture；
2. 纯重命名 `mornlea_mesh` → `mornlea_engine`，先证明 mesh 输出与视觉零漂移；
3. 增加 collision ABI、parity 与 production switch；
4. 增加 raycast batch ABI、parity 与 production switch；
5. 建立 macOS/Linux bundle 门禁并完成累计验证。

所有 native 调用遵循同一所有权规则：Go 分配并持有 input、scratch、cursor 和 output；
Rust 只在调用期间借用，不保存 Go 地址，不返回需要 Go 释放的 Rust 内存，也不启动后台
线程。

## 5. CI 稳定化

### 5.1 同身份 TCP 重连

`mustCloseMultiplayerTCPClient` 只证明客户端 endpoint/receiver 已关闭，不能证明 Host 的
deferred `releaseLogin` 已删除两个 active index。当前测试在关闭后立即登录，因此调度
较慢时会合法收到 `LoginAlreadyOnline`。

修复只改变测试同步：

```text
close first client
→ waitForPlayerReleased(t, host.Host, identity.PlayerID)
→ connect second client with the same identity
```

`waitForPlayerReleased` 已存在并同时检查 `activeByPlayer` 与 `activeBySession`；不得新增
sleep、重试登录或生产 hook。原有“重连后 world time 不回退”断言保持不变。

### 5.2 心跳隔离

`loginHealthyMemoryPlayers` 当前逐个执行：

```text
LoginClient → waitReady → monitorEndpointProgress/Recv
```

Memory client 只有在读取 `KeepAlive` 后才自动发送 reply。在 `HeartbeatInterval=20ms`、
`HeartbeatTimeout=150ms` 的真实被测配置下，race runner 可能在 reader 启动前淘汰本应
健康的客户端，外层 `waitReady` 随后空等 30 秒。

修复顺序为：

```text
LoginClient → monitorEndpointProgress/Recv → register cleanup → waitReady
```

20ms、150ms、`waitDeadline` 和生产心跳代码全部不变。

### 5.3 CI 验收

- 两个 focused test 在 `-race -count=100` 下通过；
- `internal/server` 全包 race 通过；
- 删除 release barrier 或恢复 reader-after-ready 的 mutation 重新暴露相同失败形态；
- 不以 workflow 去重、统一 retry 或延长 deadline 冒充根因修复。

## 6. Rust crate、构建与发布

目标结构保持一个 crate，现有 mesh 模块不做无关重排：

```text
engine/
  Cargo.toml
  Cargo.lock
  rust-toolchain.toml
  crates/
    mornlea_engine/
      Cargo.toml
      build.rs
      src/
        lib.rs
        ffi.rs
        input.rs
        light.rs
        greedy.rs
        quad.rs
        collision.rs
        raycast.rs
  include/
    mornlea_engine.h
```

约束：

- workspace 只有 `mornlea_engine`；normal dependency 继续只有 `std`；
- `cargo build --locked --release` 是 Go build/test 前置；
- 不恢复 `staticlib`，避免与客户端已有 Rust runtime/链接边界冲突；
- 不提交 `.dylib`、`.so`、Cargo target 或其他 native artifact。

### 6.1 macOS arm64

- 构建 `libmornlea_engine.dylib`；
- `make build` 把 dylib 与 `bin/mornlea` 放在同一目录；
- install name/runpath 使用 `@rpath`；
- 临时移开 `engine/target` 后，`bin/mornlea -h` 仍必须进入 Go 参数解析，输出不得包含
  `dyld` 或 `Library not loaded`。

### 6.2 Linux amd64

- GitHub Ubuntu runner 原生安装/使用固定 Rust 1.97.1 与 Go 1.26；
- 构建 `libmornlea_engine.so` 与 `mornlea-server`；
- bundle 中二者同目录，ELF runpath 使用 `$ORIGIN`；
- 临时移开 Cargo target 后执行 server 启动/参数探针；
- `go list -deps`、binary dependency audit 与启动测试共同证明不含 client、render、gfx、
  WebGPU、GLFW 或其他窗口依赖。

## 7. 唯一 Go/C bridge

新增具体叶子包 `internal/nativeabi`，作为唯一允许以下行为的位置：

- `import "C"`；
- include `mornlea_engine.h`；
- 链接 `libmornlea_engine`；
- 检查 ABI version 并解释 status。

调用关系为：

```text
internal/mesh    ─┐
internal/physics ├─→ internal/nativeabi → mornlea_engine.h → Rust cdylib/so
internal/core    ─┘
```

`nativeabi` 只暴露 POD、`[]byte` 与数字数组，不依赖 `core`、`physics`、`mesh`、`world`
等领域类型。领域包各自负责布局编码与结果解释，避免 bridge 反向拥有 gameplay 语义。

mesh、collision 与 raycast 的 C 声明必须使用 Go 1.26 支持的 `#cgo noescape` 与
`#cgo nocallback`，前提是 Rust FFI 结构审计确认对应 entry point 不保存地址、不回调 Go。
Go escape diagnostics 与分配测试验证这些声明确实生效；这样常规栈上 input/output
不因进入 cgo 而逃逸到 heap。若编译器 escape 检查或
`AllocsPerRun` 证明该声明未生效，实施必须停下修复 bridge，不能用全局 pool 或放宽
零分配门禁替代。

现有 `internal/mesh/native_abi.go` 的 cgo 职责移入该包；mesh 的 Go API 和 packed quad
结果不变。架构守卫同步登记 `core/physics/mesh → nativeabi`，并继续禁止其他包直接接触
C header 或 native symbol。

## 8. Collision ABI 与数据流

`physics.Step` 的公开签名保持不变。Go 仍负责：

- 在函数入口读取唯一一份 tunables；
- 校验 State/Input；
- 计算 movement target、加速度、重力、velocity 与 displacement；
- 在 native 成功后应用 position/onGround，并按 clipped mask 清零 velocity。

### 8.1 保守 collision snapshot

Go 根据玩家起点、完整 displacement、`StepHeight`、玩家 AABB、collision epsilon 与
ground probe 计算一个覆盖普通 Y→X→Z 路径和潜在 step-up 路径的保守 dense prism。

编码逻辑等价于：

```text
header:
  magic/layout version
  position
  displacement
  beganGrounded
  stepHeight
  prism origin
  prism dimensions

cells in deterministic Y/X/Z order:
  loaded
  boxCount
  up to 8 local AABBs
```

每个格子在单次 Step 中只调用一次 `CollisionSource.CollisionBoxes`。本 change 明确
`CollisionSource` 的生产实现必须在单次 Step 内为纯、一致的只读查询；现有 client mirror
与 server dimension 满足该条件。

Go encoder 保持现有 `clipAxis` 的兼容语义：先把 `CollisionBoxSet.Count` clamp 到
`len(Boxes)==8`，只编码前八个 AABB。Rust 只接受编码后的 `boxCount<=8`；不得因为原始
Count 是 9 或 255 而把既有可接受输入改成 panic。

### 8.2 容量与分配

- 根据正式 config 的 tunable 上限推导并冻结常规 prism 容量；
- 默认、colliding 和 stepping 稳定路径使用栈上固定缓冲并继续满足 `0 allocs/op`；
- 合法但超出常规容量的测试/内部状态先以 checked arithmetic 计算完整长度，再使用临时
  动态缓冲；
- dynamic path 的硬上限为 `4096` 个 cell，必须在查询 `CollisionSource`、分配和 FFI
  前检查；
- 不截断、不把 unknown 当 air、不以 Go 算法 fallback；
- 超过 cell 上限、长度乘法溢出或无法表示的 prism 以稳定 panic 在任何查询、分配、
  native 调用和状态发布前失败。

`4096` 是一个完整 `16³` section 的 cell 数，显著覆盖 config 允许的 `walkSpeed<=20`、
`jumpSpeed<=30`、`terminalFallSpeed<=200`、`stepHeight<=1.5` 所需 swept volume，同时为
内部异常状态提供明确资源天花板。该上限属于 observable resource contract，必须写入
delta spec 并覆盖 `4096` 成功、`4097` 原子失败与超限时 source 零调用测试。

### 8.3 Rust collision kernel

Rust 使用 snapshot 完整执行：

- Y、X、Z 固定 axis order；
- collider overlap 与 endpoint 判定；
- float32 `Nextafter` 安全距离；
- unknown cell 作为封闭边界并设置 `HitUnknown`；
- ordinary move、ground probe；
- step rise、水平移动、下降、headroom/landing 检查；
- 水平进度严格更大时才选择 step path。

`hitUnknown` 只来自最终选中的 ordinary 或 step path。若备选 step path 遇到 unknown 后
被拒绝，最终返回的 ordinary 结果不得继承该备选路径的 unknown；这保持现有 Go
`resolveStepMove` 返回 `(moveResult{}, false)` 后丢弃临时结果的语义。

输出只包含：

```text
position
clipped mask
onGround
usedStep
hitUnknown
```

Go 在 status OK 前不得修改 State。旧 Go collision/step resolution 移到 `_test.go`，只作
oracle。

## 9. Raycast Batch ABI 与数据流

`core.RaycastBlocks` 的公开签名和 callback 保持不变。Go 保留：

- 有限值、正 `maxDistance` 与 non-nil callback 校验；
- 现有 nested `math.Hypot` 方向归一化；
- 顺序执行 `solid(BlockPos)`；
- 命中后以现有 Go float32 运算生成 `Point`。

保留归一化和最终 Point 计算可避免 macOS/Linux libm 在 sqrt/hypot 或 FMA 上产生末位
差异；真正的 DDA traversal 由 Rust 唯一生产实现。

### 9.1 Batch cursor

Rust entry point 逻辑为：

```text
mornlea_raycast_batch(
  origin,
  normalized_direction,
  max_distance,
  cursor,
  output[64],
) -> next_cursor, count, done
```

每条 output record 包含 `BlockPos`、entry `Face` 和 float32 `Distance`。第一条候选是
origin 所在格，命中时仍为 `FaceNone`、距离 0。

Rust 保持：

- 负坐标 floor 语义；
- XYZ tie priority；
- 精确 endpoint inclusion；
- 每轴 step、tDelta、tMax 与 float32 更新顺序。

Go 按 record 顺序执行 callback。首个 error 原样返回；首个 solid 立即形成 RayHit；本批
没有结果且 `done=false` 时，把 caller-owned cursor 传回下一次调用。

所有 input、cursor 与 output capacity 检查必须在 traversal 前一次完成。一个已验证的
batch 在生成最多 64 条 record 时是 total operation，不得因后续 cell 而返回新错误；
`int32` cell 前进使用与 Go 相同的 wrapping 更新。这样 Rust 预计算后续 record 不会抢在
本批第一条 callback 的 sentinel error 前发布另一种失败。

64 条覆盖生产 reach=6 和现有 benchmark reach=32。更长合法射线分批继续，不设置新
距离上限，也不复制 dense bounding prism。Rust 可以预计算本批后续 cell，但 Go 不会对
命中/错误后的 record 执行 callback，因此惰性可观察行为不变。

cursor 是固定 POD，包含继续 DDA 所需数值状态；Rust 不保存 cursor 地址。旧 Go DDA
移入 `_test.go` 作为 oracle。

## 10. 错误与原子性

所有 Rust entry point 均执行双层验证：Go 在编码前验证领域不变量，Rust 再验证 ABI
version、magic、长度、对齐、指针范围、buffer overlap、count、cursor 与 output capacity。

每个入口先清零 output metadata，再通过 `catch_unwind` 执行 kernel：

- callback 返回的 Go error 保持 identity，不经过 ABI；
- 非法用户 ray 输入继续返回现有稳定 Go error；
- ABI mismatch、Rust panic、非法编码或不可能的 output overflow 属于部署/编程错误，
  映射为稳定 panic；
- native 错误不得伪装成 `ErrChunkNotReady`、普通无命中、unknown cell 或空 mesh；
- collision 失败时 State 尚未修改；raycast 失败时尚未发布 hit；mesh 保持现有
  `output_len=0` 原子契约。

动态库不得创建全局 mutable state、后台线程或需要显式释放的 Rust object。

## 11. 测试策略

### 11.1 Rust unit tests

- ABI version、symbol、layout size/alignment 与 status 数值；
- nil、长度、对齐、overlap、capacity、cursor 与 panic；
- collision axis order、box overlap、unknown、step 选择与 ULP；
- raycast 起点、负坐标、tie、endpoint、batch boundary 与 cursor；
- stable collision/raycast kernel 不执行 heap allocation，Go bridge 的常规栈缓冲不逃逸。

### 11.2 Go/Rust parity

Go oracle 与 Rust 生产实现对同一输入逐位比较：

- 现有全部 collision、step、raycast fixture；
- 固定种子随机输入；
- fuzz seed 与独立 fuzz target；
- 负坐标、8 AABB、unknown、头顶不足、同高落点、极限 tunable/velocity；
- 原始 collision Count 为 8、9、255 时与现有 clamp 语义一致；
- ordinary path 已知、被拒绝的备选 step 遇到 unknown 时最终 `HitUnknown` 仍为 false；
- collision snapshot 4096-cell 成功、4097-cell 原子失败且 source 零调用；
- raycast callback error identity、64 条边界和多 batch；
- raycast 靠近 int32 边界、首条 callback 返回 sentinel error 时仍原样返回该 error；
- 多 goroutine 并发调用；
- macOS arm64 与 Linux amd64 各自对相同 golden/parity corpus 验证。

### 11.3 Mutation

至少证明以下 mutation 会失败：

- 删除 TCP release barrier；
- 恢复 heartbeat reader-after-ready；
- collision 改变 Y/X/Z 顺序；
- unknown cell 当作 air；
- step 水平距离由严格 `>` 改为 `>=`；
- raycast 改变 XYZ tie priority；
- 把精确 endpoint 改为排除；
- 在 64 条后错误标记 done 或丢失 cursor。

## 12. 验证与性能

按风险由小到大执行：

```bash
cargo fmt --check --manifest-path engine/Cargo.toml
cargo clippy --manifest-path engine/Cargo.toml --workspace --all-targets -- -D warnings
cargo test --manifest-path engine/Cargo.toml --workspace --locked
go test ./internal/core ./internal/physics ./internal/mesh -race -count=1
go test ./internal/server -run 'Test(ReconnectContinuesWorldTimeWithoutRollback|HostHeartbeatTimeoutCleanupIsIsolated)' -race -count=100
go test ./internal/server -race -count=1
go test ./internal/client ./internal/sim ./cmd/mornlea -race -count=1
go test ./internal/archcheck -count=1
go test ./... -race
go vet ./...
gofmt -l .
openspec validate --all --strict --no-interactive
git diff --check
```

还必须执行：

- `BenchmarkMeshTerrainSection`、三项 physics benchmark 与 `BenchmarkRaycastBlocks` 各 5 次；
- scenario v16 Memory/TCP producer、self/cross identity 与报告完整性检查；
- 11 个 headless `visual-check`；
- macOS dylib 与 Linux server/so 脱离 Cargo target 的启动探针；
- M2 v15、M5 v14 baseline 与现有 golden blob 守卫。

性能数值只记录，不改变退出状态。报告身份、缺字段、真实 overflow、数据丢失、I/O、
native 加载失败和非数值门禁仍阻断。不得更新 baseline、golden、scenario 或阈值来吸收
迁移差异。

## 13. OpenSpec 产物与兼容性

combined change 至少包含：

- `rust-engine-mesh` MODIFIED delta：workspace/library identity、Rust-first build、Linux
  headless server 的 Rust/CGO 新契约；
- `project-identity` MODIFIED delta：`libmornlea_engine.dylib` 身份以及 Linux
  `mornlea-server + libmornlea_engine.so` bundle，替换旧 CGO0 单文件承诺；
- 新增 `rust-engine-collision-raycast` capability：collision snapshot、raycast batch、
  parity、所有权和失败原子性；
- proposal/design/tasks 中的 CI root-cause 任务；已有 `test-timing-discipline` 已足够，
  不为两个 fixture 再造 deadline 规格。

归档时才把 delta 同步进 `openspec/specs/`。README、AGENTS、CI、Make 与 Hook 只更新当前
真实 crate 名、canonical build 和 Linux server bundle 说明；历史 `docs/superpowers`
不批量改写。

兼容性结论：

- C ABI v1 additive；既有 mesh layout/status 不变；
- library 文件名原子切换，发布物必须整包升级，不支持旧 Go binary 与新 library 混装；
- Linux 专服由单 CGO0 binary 变为 `binary + .so` bundle；
- wire v16、storage schema、world metadata、scenario v16 和所有固定 artifact 不变。

## 14. 回退策略

- CI fixture、library rename、collision switch、raycast switch 分别提交；
- rename 阶段必须在没有新 kernel 的情况下先证明 mesh/build/visual 零漂移；
- 任一 kernel 出现无法解释的 parity、race、allocation、visual 或平台加载失败时，只回退
  对应生产 switch；
- 不以启用 Go fallback、更新期望值或降低门禁作为回退；
- Go oracle 在本 change 合入后暂留 test-only，后续稳定观察完成再用独立 change 删除。

## 15. 被否决的方案

### 15.1 一个通用 world-query snapshot ABI

Collision 需要 swept AABB dense prism；raycast 需要沿 DDA 的稀疏有序访问。统一格式会让
collision 携带 cursor 或让 raycast 复制巨大 prism，只有表面复用，没有真实内聚。

### 15.2 Rust 持有只读 world mirror

这能减少复制，但已经进入状态同步、generation、失效与生命周期设计，远超纯 kernel
迁移范围。等 Rust world/state 边界单独设计后再评估。

### 15.3 多个 Rust crate/dylib

为 collision、raycast 各建 library 会复制 ABI identity、构建、runpath、发布和错误处理。
当前只有一个生产部署单元，单 crate 更短且足够。

### 15.4 继续把新 kernel 放进 `mornlea_mesh`

最小机械 diff 会换来立即失真的职责名，并把同一重命名推迟到完整 physics。第二个领域
进入时就是收敛为 engine identity 的最晚合理时点。

### 15.5 保留 CGO0 server fallback

客户端 Rust、服务端 Go 会形成两套权威相关实现和持续 parity 负担，违反 Rust 唯一生产
实现目标。Linux 改为原生构建 Rust bundle，而不是保留 fallback。

### 15.6 同批迁移完整 physics 或 worldgen

完整 physics 引入 trig/sqrt 的跨平台末位问题；worldgen 引入 float64 noise、矿石、树木和
chunk bulk import。它们都应在本 change 建立的 engine/平台边界稳定后独立设计。
