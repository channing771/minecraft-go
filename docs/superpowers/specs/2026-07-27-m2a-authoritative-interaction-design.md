# minecraft-go M2A 权威交互闭环设计

- 日期：2026-07-27
- 状态：待书面评审
- 上位设计：`docs/superpowers/specs/2026-07-26-minecraft-go-design.md`
- 范围：权威 tick、内存协议边界、服务端区块订阅、客户端只读镜像、射线拾取、瞬时挖掘与无限方块放置

---

## 1. 目标与边界

M2A 把 M1 的“客户端自行生成并渲染地形”改造成第一个完整的客户端—服务端垂直切片：

1. 内置服务端是区块生成与方块状态的唯一权威。
2. 客户端只能通过不可变消息发送意图、接收区块快照与方块增量。
3. 客户端持有独立的只读世界镜像，不与服务端共享可变对象。
4. 玩家能用射线瞬时挖掘，并用数字键选择无限方块后放置。
5. 区块卸载并重新加载后，本次进程会话中的修改仍然存在。
6. M1 的 32 区块视距与性能指标不得回退。

### 1.1 本阶段明确包含

- 固定 20 TPS 的单写者模拟循环
- 确定性的命令排序与 worker 结果应用
- 有界内存 Transport
- 服务端区块生成、订阅、卸载与失败隔离
- 调色板压缩区段快照
- 区块 revision 与断档重同步
- 会话内方块修改覆盖层
- 客户端镜像与增量网格重建
- 6 方块距离的体素 DDA 射线
- 左键瞬时挖掘
- `1/2/3` 选择石头、泥土、草方块，右键放置
- headless 确定性测试、端到端一致性测试和固定交互性能基准

### 1.2 本阶段明确不包含

- 玩家重力、碰撞箱、行走、跳跃
- 客户端移动预测、服务端姿态校验、回滚重放
- 生存模式挖掘时间、工具、耐久、掉落物
- 物品栏、快捷栏数量与方块消耗
- TCP、登录状态机、二进制编解码、zstd
- 磁盘存档与崩溃恢复
- 光照传播、地物装饰、方块更新、实体
- 多客户端广播语义
- 方块选中框、破坏动画和音效

这些能力分别属于 M2B、M3 或 M4。M2A 不用临时实现冒充完整系统。

---

## 2. 架构与所有权

### 2.1 三条所有权边界

**服务端权威**

`sim/server` 的 world goroutine 是世界状态的唯一写者。区块生成 worker 只产生候选结果，必须把结果交回 tick 应用；任何 worker 都不能直接修改服务端世界。

**协议只传值**

`network` 消息不得包含 `*world.Chunk`、`*world.Section`、`*world.PalettedContainer` 或发送方仍会修改的切片。内存传输与未来 TCP 使用同一组语义消息。

**客户端只读镜像**

客户端只在主线程应用服务端消息。网格 worker 读取深拷贝的不可变邻域快照；渲染器只消费网格与 connectivity，不感知协议或服务端。

### 2.2 包与依赖

新增包：

| 包 | 职责 | 允许的内部依赖 |
|---|---|---|
| `internal/network` | 消息定义、内存 Transport、关闭与背压语义 | `core` |
| `internal/sim` | tick、命令排序、权威修改、区块 revision | `core`, `world` |
| `internal/server` | 会话装配、订阅、worldgen、覆盖层、消息发布 | `core`, `network`, `world`, `worldgen`, `sim` |

现有包调整：

- `core`：接收端无关的 `BlockID`、`DimensionID`、`BlockFace`、射线类型与命中结果。
- `world`：`BlockID` 改为 `core.BlockID` 的类型别名；提供不依赖协议包的 `ContainerSnapshot` 导入/导出与区块 hash。
- `client`：新增镜像和网格调度器；删除客户端地形生成职责。
- `render` / `gfx`：接口不感知本次服务端改造。

依赖仍保持单向：

```text
core ← network
core ← world ← worldgen / sim
server → core, network, world, worldgen, sim
client → core, network, world, mesh, render
gfx ← render ← client
```

`world` 不得 import `network`，`sim` 不得 import `client` 或 `render`。

### 2.3 运行时数据流

```text
窗口输入
  → ClientMessage（不可变命令）
  → MemoryTransport
  → server inbox
  → 20 TPS Engine.Step
  → 权威 world 修改
  → ServerMessage（快照 / 增量 / 卸载 / 拒绝）
  → client mirror
  → dirty section + 不可变邻域快照
  → mesh worker
  → Renderer.QueueSection
```

单机不允许绕过 Transport 直接调用服务端游戏逻辑。

---

## 3. 端无关类型与协议

### 3.1 公共类型下沉

`BlockID` 从 `world` 下沉到 `core`：

```go
package core

type BlockID uint16
type DimensionID int32
type BlockFace uint8

const Overworld DimensionID = 0

const (
    BlockFaceNegX BlockFace = iota
    BlockFacePosX
    BlockFaceNegY
    BlockFacePosY
    BlockFaceNegZ
    BlockFacePosZ
    BlockFaceNone BlockFace = 0xff
)
```

迁移期间 `world.BlockID` 保留别名：

```go
type BlockID = core.BlockID
```

空气固定为 ID `0`。M2A 允许客户端请求放置的 ID 只有石头、泥土和草方块；服务端使用白名单验证，不能相信客户端传来的任意 ID。

### 3.2 客户端到服务端

```go
type SetViewCenter struct {
    Sequence  uint64
    Dimension core.DimensionID
    Center    core.ChunkPos
}

type BreakRay struct {
    Sequence  uint64
    Dimension core.DimensionID
    Origin    mgl32.Vec3
    Direction mgl32.Vec3
}

type PlaceRay struct {
    Sequence  uint64
    Dimension core.DimensionID
    Origin    mgl32.Vec3
    Direction mgl32.Vec3
    Block     core.BlockID
}

type RequestChunkResync struct {
    Sequence     uint64
    Dimension    core.DimensionID
    Chunk        core.ChunkPos
    HaveRevision uint64
}
```

`mgl32` 是外部数学库，不破坏内部依赖方向。协议编解码进入 M3 时将向量写成三个 IEEE-754 `float32`。

### 3.3 服务端到客户端

```go
type ChunkSnapshot struct {
    Dimension core.DimensionID
    Chunk     core.ChunkPos
    Revision  uint64
    Sections  []SectionData
}

type BlockChanges struct {
    Dimension    core.DimensionID
    Chunk        core.ChunkPos
    BaseRevision uint64
    NewRevision  uint64
    Changes      []BlockChange
}

type BlockChange struct {
    Position core.BlockPos
    Block    core.BlockID
}

type ForgetChunks struct {
    Dimension core.DimensionID
    Chunks    []core.ChunkPos
}

type CommandRejected struct {
    Sequence uint64
    Reason   RejectReason
}
```

同一 tick 对同一区块的全部方块修改合并成一个 `BlockChanges`：

- `BaseRevision` 是 tick 开始时的 revision。
- `NewRevision = BaseRevision + 1`，无论该批包含一个还是多个方块。
- `Changes` 按区块内 block index 递增排列。
- 被拒绝或写入相同值的命令不增加 revision。

### 3.4 压缩区段快照

`SectionData` 是 `network` 自己的 transport-neutral 值，不暴露 `world` 的私有字段：

```go
type SectionStorage uint8

const (
    SectionSingle SectionStorage = iota
    SectionIndexed
    SectionDirect
)

type SectionData struct {
    Y       int32
    Storage SectionStorage
    Single  core.BlockID
    Bits    uint8
    Palette []core.BlockID
    Packed  []uint64
}
```

约束：

- `SectionSingle` 必须 `Bits == 0`，`Palette` 与 `Packed` 为空。
- `SectionIndexed` 的 `Bits` 只能为 `4` 或 `8`。
- `SectionDirect` 的 `Bits` 固定为 `15`，`Palette` 为空。
- 解码前验证 Y、位宽、调色板长度、packed word 数和所有索引。
- 一个 `ChunkSnapshot` 必须包含 24 个 Y 不重复且覆盖 `0..23` 的区段。
- `Sections` 按 Y 从 0 到 23 排列，`Palette` 保持容器中首次出现方块的确定性顺序。
- 不可信快照只返回 error，绝不 panic。

内存 Transport 采取所有权转移：发送方为消息中的每个切片独立分配，成功发送后不再读写；接收方取得唯一所有权。专门的测试在发送后改写源容器，证明接收消息不受影响。

`world` 定义字段等价的 `ContainerSnapshot` 与经过验证的导入/导出方法，但不 import `network`。`server` 负责从 `world.ContainerSnapshot` 构造 `network.SectionData`，`client` 负责反向转换；两处都显式转移或复制切片所有权。

---

## 4. Transport 与背压

### 4.1 接口

本阶段只有一对本地端点，但接口按双向传输定义：

```go
type ClientEndpoint interface {
    Send(context.Context, ClientMessage) error
    Recv(context.Context) (ServerMessage, error)
    Close() error
}

type ServerEndpoint interface {
    Send(context.Context, ServerMessage) error
    Recv(context.Context) (ClientMessage, error)
    Close() error
}

func NewMemoryPair(capacity int) (ClientEndpoint, ServerEndpoint)
```

`ClientMessage` 和 `ServerMessage` 是封闭消息接口；只有 `network` 包内定义的具体消息可以实现。

### 4.2 关闭语义

- 任一端 `Close` 可重复调用。
- 关闭后所有阻塞的 `Send` / `Recv` 必须唤醒并返回 `ErrClosed`。
- 已经成功进入 channel 的消息允许接收端先排空，再观察到关闭。
- context 取消优先返回 `context.Canceled` 或 `context.DeadlineExceeded`。
- 禁止通过关闭一个仍可能被其他 goroutine 发送的裸 channel 实现，避免 send-on-closed panic。

### 4.3 服务端 outbox

tick 不直接阻塞调用 Transport：

- 每个会话有容量 512 条消息的 outbox。
- tick 只做非阻塞 enqueue。
- writer goroutine 串行取 outbox 并调用 `ServerEndpoint.Send`，保持严格 FIFO。
- outbox 满时关闭慢会话并记录 `slog.Warn`；不得丢一部分增量后继续运行。
- writer 中任何 panic 被会话边界捕获，不能拖垮 server tick。

客户端发往本地服务端的输入队列容量为 256。连续的 `SetViewCenter` 可在客户端侧合并成最新值；挖掘、放置和 resync 不得静默丢弃，队列满时向调用方返回明确错误。

---

## 5. 权威 tick 与确定性

### 5.1 Engine 形态

```go
type Engine struct {
    Tick uint64
    // world、命令 inbox、dirty set、ready worker results
}

func (e *Engine) Step() TickResult
func (e *Engine) Run(ctx context.Context, clock Clock) error
```

测试直接调用 `Step()`，不等待真实时间。生产运行的 `Clock` 每 50 ms 触发一次，共 20 TPS；落后时不并发执行 tick，也不无限追赶。最多连续补跑 5 tick，仍落后则记录告警并重新以当前时间为基准。

### 5.2 固定 tick 顺序

每个 tick 严格按以下顺序：

1. 截取本 tick 的客户端命令；`server` 已把 network 消息转换成不依赖协议包的 `sim.Command`。
2. 按 `(sessionID, sequence)` 稳定排序并去重。
3. 应用 `SetViewCenter`，计算订阅进入和离开集合。
4. 将 worker 完成的区块结果按 `(dimension, chunkX, chunkZ)` 排序后应用。
5. 依命令顺序执行挖掘、放置和 resync。
6. 对本 tick 修改过的区段调用一次 `Compact()`。
7. 按区块聚合 `BlockChanges`，递增 revision。
8. 在快照预算内由近到远发布待发送快照。
9. 发布增量、卸载与拒绝消息。
10. `Tick++`。

worker 完成时间只能影响区块在哪个 tick 变为 Ready，不能影响同一批结果的应用顺序或最终内容。

### 5.3 命令序号

- 每个会话的 sequence 从 1 开始严格递增。
- sequence 小于等于已应用值时视为重复，幂等忽略。
- 出现跳号允许处理；M2A 的内存 Transport 保序，跳号用于未来重连诊断。
- 同一 sequence 只能对应一条命令；协议层不提供覆盖语义。

---

## 6. 服务端区块与会话覆盖层

### 6.1 M2A 生命周期

```text
Absent → Generating → Ready → Unloading → Absent
                    ↘ Failed
Failed → Generating（退避后显式重试）
```

本阶段没有地物、光照、持续方块 tick 和磁盘保存，因此不加入空的 `Populated`、`Lit`、`Ticking`、`Saved` 阶段。对应功能进入项目时扩展状态机。

非法状态转换是程序员错误，直接 panic。生成失败是任务失败：worker 捕获 panic，返回带上下文的失败结果，区块进入 `Failed`。

### 6.2 订阅半径

- 客户端渲染半径固定为 32。
- 服务端订阅半径固定为 33；额外一圈供面判断和 AO。
- 客户端不能通过消息放大半径。
- `SetViewCenter` 只携带中心坐标。
- 进入集合按到中心的距离平方、X、Z 排序。
- 离开集合按 X、Z 排序后发送 `ForgetChunks`，同时从待发快照队列删除；客户端立即卸载镜像和 GPU 资源。

服务端区块缓存保留所有会话订阅集合的并集。M2A 只有一个本地会话，但集合结构不烧入“永远只有一个订阅者”的假设。

### 6.3 快照预算

每 tick 最多发送：

- 64 个区块快照，且
- 按调色板切片逻辑大小估算的 payload 总计不超过 1 MiB。

任一条件先达到即停止，余下区块留到下一 tick。单个区块即使超过 1 MiB 仍允许单独发送，避免永久饥饿。

快照在 tick 内从权威区块深拷贝。M1 实测渲染半径 32 的调色板 payload 为 20.57 MiB；半径 33 的订阅集合约 22 MiB。1 MiB/tick 的预算可在约 1.1 秒的发布预算内完成，实际首次可交互时间主要由地形生成决定。

### 6.4 Revision

- 新生成区块 revision 从 1 开始。
- 同一 tick 的实际修改使 revision 加 1。
- 快照在 outbox 中排在该区块后续增量之前。
- 尚未发送初始快照的区块不单独发送增量；快照直接捕获发布 tick 的最新 revision。
- resync 请求把该区块重新放入高优先级快照队列。

### 6.5 会话内覆盖层

覆盖层按 `(dimension, chunk)` 保存最终方块值：

```go
type ChunkOverlay map[uint32]core.BlockID
```

键是区块柱内的紧凑 block index，范围为 `0..98_303`，覆盖 X/Z 0..15 与 24 个区段的 Y，因此必须使用 `uint32`。权威修改同时更新已加载区块和覆盖层。

区块卸载后释放完整 `world.Chunk`，覆盖层继续保留。重新加载流程：

1. 按种子确定性生成基础区块。
2. 按 block index 升序应用覆盖层。
3. 对受影响区段 `Compact()`。
4. 进入 Ready 并发布快照。

若新值等于确定性基础地形值，可从覆盖层删除该条目。M2A 进程退出后覆盖层丢弃；M3 用磁盘存档替换它。

---

## 7. 射线、挖掘与放置

### 7.1 体素 DDA

`core.RaycastBlocks` 对任意方块查询函数工作：

```go
type RayHit struct {
    Block    BlockPos
    Face     BlockFace
    Distance float32
    Point    mgl32.Vec3
}

func RaycastBlocks(
    origin, direction mgl32.Vec3,
    maxDistance float32,
    solid func(BlockPos) (bool, error),
) (RayHit, bool, error)
```

约束：

- M2A `maxDistance` 固定为 `6.0`。
- 服务端重新归一化 direction。
- origin/direction 含 NaN 或 Inf、方向长度小于 `1e-6`、距离非正时返回 error。
- 轴分量为零时该轴的步进时间视为正无穷，不做除零。
- 起点位于实心方块内时立即命中，距离为 0。
- 起点命中的 `Face` 为 `BlockFaceNone`；这种命中可以挖掘，但不能用于放置。
- 同时跨过两个或三个轴边界时按 X、Y、Z 的固定顺序推进，保证跨平台确定性。
- 查询超出世界高度时按空气处理。
- 查询函数返回的错误原样终止射线；服务端以此区分“没有目标”和“路径经过未 Ready 区块”。

客户端可用同一函数做准星目标判断；服务端始终独立重算。

### 7.2 M2A 姿态信任边界

M2A 没有权威玩家物理。`BreakRay` / `PlaceRay` 的 origin 与 direction 来自客户端，服务端验证：

- 数值有限且方向有效；
- origin 所在 chunk 位于当前会话订阅范围；
- 射线只查询 Ready 区块；
- 命中距离不超过 6。

这保证内存本地会话的正确性，但不构成联网反作弊。M2B 引入权威玩家姿态、移动校验和预测和解后，交互射线改从服务端姿态产生。

### 7.3 瞬时挖掘

左键上升沿发送一条 `BreakRay`。服务端：

1. 在权威世界重做 DDA。
2. 无命中则拒绝 `no_target`。
3. 射线路径经过未 Ready 区块则拒绝 `chunk_not_ready`。
4. 空气不产生修改。
5. 基岩拒绝 `protected_block`。
6. 其他方块写为空气并进入本 tick 的 change batch。

M2A 不产生掉落物。

### 7.4 无限方块放置

数字键只改变客户端选中 ID：

- `1`：石头
- `2`：泥土
- `3`：草方块

右键上升沿发送 `PlaceRay`。服务端：

1. 验证 BlockID 在白名单。
2. 在权威世界重做 DDA。
3. 命中面为 `BlockFaceNone` 时拒绝 `occupied`；否则取命中面的相邻方块为目标。
4. 验证目标在世界高度内且其区块 Ready。
5. 验证目标为空气。
6. 若目标方块 AABB 包含射线 origin，拒绝 `occupied`。
7. 写入选中方块并进入本 tick 的 change batch。

没有连续按住重复放置；每个鼠标按下沿只产生一条命令。

---

## 8. 客户端镜像与网格重建

### 8.1 Mirror

`client.Mirror` 由渲染主线程独占写：

```go
type Mirror struct {
    dimensions map[core.DimensionID]map[core.ChunkPos]*MirrorChunk
}

type MirrorChunk struct {
    Revision uint64
    Chunk    *world.Chunk
}
```

服务端与客户端的 `world.Chunk` 是不同实例。导入 `ChunkSnapshot` 时客户端验证全部 `SectionData` 后再原子替换该镜像区块；验证失败不留下半导入状态。

### 8.2 Revision 应用

收到 `BlockChanges`：

- 区块不存在：不应用，发送一次 resync。
- `BaseRevision == current`：按消息顺序应用全部 change，最后设置 `NewRevision`。
- `NewRevision <= current`：视为重复或过期，幂等忽略。
- 其他情况：标记 `desynced`，停止应用该区块后续增量并发送一次 resync。

收到合法快照会清除 `desynced` 和 resync-in-flight 标记。

### 8.3 Dirty 区段

一个方块变化会影响邻面剔除和角落 AO。客户端枚举被修改方块周围 `[-1,+1]³` 的 27 个方块坐标，把它们所属的区段加入 dirty set。该集合在普通位置只有一个区段，在三轴区段边界最多覆盖 8 个区段。

区块快照首次到达时，其 24 个区段全部 dirty。相邻区块到达或卸载时，边界区段重新 dirty。

### 8.4 网格任务

每个网格任务携带：

- 中心 `SectionPos`
- 深拷贝的 3×3×3 `world.Section` 邻域
- 每个输入区段的 `(present, revision)` stamp

worker 只对不可变快照调用 `MeshSection` 和 `ComputeConnectivity`。结果回到主线程后重新比较全部 stamp：

- 完全一致：更新 renderer connectivity 与 mesh，清除 dirty。
- 任一不一致：丢弃结果；若中心仍加载则保持 dirty 并重新排队。

区块卸载会删除镜像、dirty、pending job 标记和对应 GPU section。已运行的 worker 无需强杀，其结果因 stamp 不匹配自然丢弃。

### 8.5 从现有 Streamer 迁移

当前 `client.Streamer` 同时负责生成、缓存、网格化。M2A 将其拆开：

- 生成、服务端 chunk cache、wanted 集合迁入 `server`。
- 客户端保留有界 mesh worker pool、结果队列、panic 隔离和关闭唤醒语义。
- 客户端不再 import `worldgen`。
- `cmd/mcgo` 启动 MemoryTransport、内置 server、Mirror 和 Mesher，再进入窗口循环。

---

## 9. 错误处理与关闭

### 9.1 稳定拒绝原因

```text
invalid_ray
no_target
chunk_not_ready
protected_block
invalid_block
occupied
```

被拒绝的命令不修改区块、revision 或覆盖层。拒绝消息携带原 sequence，客户端可与输入日志关联；M2A 不显示 UI toast，只写 debug 日志。

### 9.2 失败隔离

- 区块生成和网格任务各自 `recover`，记录 chunk/section 坐标与 panic。
- 生成失败进入 `Failed`，不发布半成品。
- 网格失败保持 dirty，可在退避后重试。
- 非法网络消息只关闭对应会话，不 panic server。
- server tick 自身的不变式 panic 允许进程快速失败；未来 M3 在退出前执行存档保护。

### 9.3 关闭顺序

1. 停止窗口产生新输入。
2. 关闭客户端 Transport 端点。
3. server 观察连接关闭，停止接收命令。
4. 取消 server tick 与区块生成 worker。
5. 取消 client mesh worker。
6. 排空已完成结果并停止主循环。
7. 释放 renderer、surface、device、window。

所有阻塞点必须同时监听 context/closed channel，确保 outbox 满、结果队列满或 worker 正在发送时也能退出。

---

## 10. 测试策略

### 10.1 `core`

- 表驱动 DDA：六轴方向、斜线、负坐标、起点实心、边界起点、零轴分量。
- 非有限输入与近零方向返回 error。
- 最大距离前命中、恰好距离处命中、距离后不命中。
- 属性测试：返回命中必须为 solid，距离必须在 `[0,maxDistance]`，沿射线更早的体素不得为 solid。

### 10.2 `network`

- 双向消息保持顺序。
- 任一端关闭会唤醒阻塞 Send/Recv。
- context 取消可中止阻塞操作。
- 容量耗尽产生背压，不丢消息。
- 所有权转移后的消息按原值、原顺序到达，不产生额外共享状态。

### 10.3 `sim`

- `Step()` 每次恰好推进一个 tick。
- 同一 tick 命令按 sequence 生效。
- 重复 sequence 不重复修改。
- 同一区块多次实际修改只递增一次 revision。
- 拒绝命令不改变 world hash。
- 同种子、同命令脚本运行两次，最终 world/overlay/revision hash 完全一致。

确定性重放测试使用同步生成器或先把指定区块推进到 Ready，再从相同 tick 开始输入脚本；它不把真实 goroutine 调度时机当成游戏状态的一部分。区块 hash 按固定坐标顺序读取逻辑 BlockID 和 revision，不依赖 map 遍历顺序或调色板内部编码。

### 10.4 `server`

- 订阅半径固定为 33，客户端无法扩大。
- 首批快照按距离、X、Z 稳定排序。
- 每 tick 快照数与字节预算生效。
- 构造快照后修改权威 world 容器，已构造消息与客户端导入结果保持不变。
- 畸形 `SectionData` 导入返回 error、不 panic，且不留下部分区块。
- 飞远后完整区块卸载。
- 修改覆盖层在重新生成后恢复。
- 恢复到基础值会移除覆盖项。
- 生成 panic 只使目标区块 Failed，其他区块继续 Ready。
- outbox 满关闭慢会话，tick 继续推进。

### 10.5 `client`

- 快照合法导入和非法原子拒绝。
- revision 连续应用、重复忽略、断档只发一次 resync。
- 方块在 X/Y/Z 边界修改时标记正确的相邻区段。
- 网格结果 stamp 过期时被丢弃。
- 卸载清理 mirror、dirty、pending 与 renderer。

### 10.6 端到端

用 MemoryTransport 启动真实 server 与 client mirror，执行固定脚本：

1. 订阅中心 `(0,0)` 并等待中心区块 revision 1。
2. 挖掉一个草方块。
3. 分别放置石头、泥土、草方块。
4. 把观察中心移动到足以卸载原区块的位置。
5. 移回原中心并等待重发快照。
6. 比较所有已同步订阅区块的服务端与客户端 hash。

测试同时断言覆盖层包含预期最终值、没有 revision 断档、所有 goroutine 在取消后退出。

---

## 11. 性能门禁

### 11.1 渲染与内存

扩展现有固定 2560×1440 场景：

- 预热 10 秒。
- 静止 60 秒。
- 飞行 120 秒。
- 飞行路径按固定种子的 `HeightAt` 保持在地表上方 3 个方块，视线向下；每秒执行一次必须成功的确定性挖掘或放置，交替使用三种方块。
- 记录首次中心区块 Ready 时间、快照完成时间、frame p50/p95/p99/max、server tick p50/p95/p99/max、峰值 RSS。

绝对门禁：

- 静止与飞行均 ≥100 fps。
- frame p99 <12 ms。
- 峰值 RSS <2 GiB。
- server tick p99 <10 ms。
- 不得出现单次 server tick ≥50 ms。
- 交互脚本结束时 server/client hash 一致。

M2A 固定场景使用新的 `scenario_version = 2`。基线比较继续要求相同硬件与相同 scenario version，任何帧时间、tick 时间或 RSS 指标退化超过 20% 判红。改变相机路径、交互脚本、快照预算或 tick 顺序必须再次提升 `scenario_version`。

### 11.2 微基准

新增：

- `BenchmarkRaycastBlocks`
- `BenchmarkEngineStepIdle`
- `BenchmarkEngineStepBlockChanges`
- `BenchmarkExportChunkSnapshot`
- `BenchmarkImportChunkSnapshot`
- `BenchmarkRemeshBoundaryEdit`

GitHub 托管 CI 只验证工具可运行和平台无关阈值，不比较跨机器绝对帧时间。

---

## 12. M2A 出口条件

以下条件必须同时满足：

- 32 区块视距的所有地形都由内置服务端生成和下发。
- 客户端代码不再调用 `worldgen.GenerateChunk`。
- 客户端与服务端没有共享可变 chunk/section/container。
- 左键能瞬时挖掘非基岩方块。
- `1/2/3` 能选择石头、泥土、草方块，右键能合法放置。
- 飞远卸载再返回后，会话内修改仍存在。
- revision 断档能自动恢复，而不是继续应用损坏状态。
- 固定端到端脚本结束后，同 revision 的 server/client 区块 hash 完全一致。
- `go test ./... -race` 通过。
- `go vet ./...` 与 `gofmt -l .` 通过。
- 依赖方向与 WebGPU 隔离门禁通过。
- 2560×1440 固定交互基准满足 §11 的全部绝对门禁。

M2A 完成后再单独设计 M2B：权威玩家物理、碰撞、移动输入、客户端预测与服务端和解。
