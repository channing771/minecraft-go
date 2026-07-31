# minecraft-go M3C 多人同步与远端玩家呈现设计

- 日期：2026-07-31
- 状态：已批准
- 前置里程碑：M3B 二进制协议、TCP 直连与稳定玩家身份
- 后续里程碑：M4 生存闭环

## 1. 背景

M3B 已交付协议 v1、TCP Transport、`Handshake → Login → Play` 状态机、稳定
`PlayerID`、专用服务端、动态 `SessionID` 注册表和单玩家存档。它有意把 Play
在线槽限制为 1，避免在没有玩家广播、远端插值和断线清理时暴露半成品多人行为。

M3C 是 M3“存档与联机”的最后一个子项目。它解除单 Play 槽限制，让最多 8 个可信
局域网玩家进入同一权威世界，并完成玩家间可见性、移动呈现、断线清理和多身份存档。
M3C 完成后，M3 的出口条件“两个客户端连入同一世界，改动实时同步，重启后世界还在”
完整闭合。

## 2. 目标与出口场景

M3C 必须交付：

- `server.Host` 可配置同时在线人数 `1..8`，默认 8；
- 相同 `PlayerID` 已在线或正在登录时拒绝新连接，保留旧会话；
- 每个客户端只接收同维度、落在自身区块视距内的远端玩家；
- 进入兴趣范围产生 spawn，离开范围或断线产生 despawn；
- 可见远端玩家按 20 Hz 接收权威快照，客户端以 100 ms 缓冲插值；
- 远端玩家以简化方块人和 Unicode 昵称标签呈现；
- 多玩家存档并发、失败隔离、重试、重连和关服 flush 保持有界；
- Memory 与 TCP 继续共用状态机和业务语义；
- 协议提升为 v2，旧 v1 客户端稳定拒绝；
- 2-client 真实 TCP 纵向证明和 8-client 有界压力门禁；
- 服务器重启后恢复全部 8 份玩家状态。

人工可见出口场景为：启动一个 `mcgod`，两个 `mcgo --connect` 客户端使用不同
`PlayerID` 进入同一世界。双方能看到对方的方块人和昵称，移动平滑，挖掘/放置仍按
现有区块 publication 实时同步。一方离开或超出视距后，另一方不保留 ghost。服务器
重启后，两名玩家回到各自保存的位置。

## 3. 非目标

以下能力不属于 M3C：

- 聊天、队伍、好友、权限组；
- 玩家皮肤下载、肢体动画、装备显示；
- 玩家间碰撞、PvP、伤害或攻击判定；
- 自动重连、连接菜单或服务器列表；
- 账号认证、TLS、公网暴露安全、跨服务器传送；
- 通用 ECS 或任意实体同步；
- 怪物、掉落物和其他 M4 实体；
- 新维度；M3C 仍只接受 `core.Overworld`。

昵称不是身份。不同 `PlayerID` 可以使用相同显示昵称，服务端和客户端的所有实体索引
始终使用 `PlayerID`。

## 4. 已接受的硬上限

| 项目 | 上限或默认值 |
|---|---|
| Play 在线与 pending 登录合计 | 配置 `1..8`，默认 8 |
| pre-login 连接 | 16，沿用 M3B |
| 玩家持久化 cache | 16 个身份，固定硬上限 |
| 玩家保存 worker | 2 |
| 玩家保存 job queue | 16 |
| 玩家保存 completion queue | 2，与 worker 数一致 |
| 单观察者远端玩家数 | 7 |
| 单个远端状态 batch | 1..7 条，payload `<512 bytes` |
| 每个远端玩家插值快照 | 4 |
| 插值延迟 | 2 tick / 100 ms |
| 吸附位移阈值 | 单次位移严格大于 8 格 |
| 昵称 | 1..32 Unicode rune，UTF-8 最多 128 bytes，沿用 M3B |
| 字形 atlas | 1024×1024 R8，32 px cell，1024 slots |

这些上限是协议、内存和测试边界，不允许由输入扩容。`MaxPlayers` 是唯一新增可配置容量；
持久化 cache 和网络 batch 上限保持固定，避免错误配置制造无界状态。

## 5. 总体架构

采用“Server 按 session 计算兴趣广播”的方案。

### 5.1 `sim.Engine`

`sim.Engine` 继续是玩家位置、朝向、Ready 状态和权威 tick 的唯一来源。它只认识
`SessionID`，不 import `network`、`storage`，也不感知 `PlayerID`、昵称或 GPU。

### 5.2 `server.Server`

`Server` 把 `SessionID` 对应的 `PlayerID` 和 `DisplayName` 存入 session 元数据。每个
权威 tick 后，publication 层为每个观察者计算远端玩家可见集合，生成 spawn、despawn
和状态 batch。区块和玩家兴趣判断在同一 `stepMu` 所有权下完成，避免 Host 轮询复制
订阅规则。

### 5.3 `server.Host`

`Host` 负责身份接纳、最多 8 个槽、`SessionID` 分配、attach/detach 和多身份存档。
它不计算视距、不生成远端消息、不做客户端插值。

### 5.4 `network`

`network` 定义协议 v2 的消息、packet ID、codec 和不可信输入校验。它保持端无关，
不 import `server`、`client`、`sim`、`storage` 或渲染包。

### 5.5 `client`

`client.RemotePlayers` 维护 `PlayerID → RemotePlayer` roster，验证消息生命周期并产生
插值后的纯值 `RemotePresentation`。本地玩家仍走现有 Predictor/和解路径，绝不进入
remote roster。

### 5.6 `render`

远端呈现拆成 `AvatarRenderer` 和 `NameTagRenderer`，不把实体和字体职责塞入现有
terrain renderer。两者只消费 `RemotePresentation` 和相机值，不读取网络或权威状态。

## 6. 协议 v2

### 6.1 版本策略

`network.ProtocolVersion` 从 1 提升到 2。M3C 不做版本范围、特性协商或 v1 解码。
v1 客户端在 Handshake 收到带服务器版本 2 的稳定 `VersionMismatch`。所有 v1 golden
升级为 v2 golden；历史 v1 字节只保留为“必须拒绝”的夹具。

现有 packet ID 不重排。Play serverbound 继续使用 0..4；Play clientbound 0..6 保持
原消息，新消息追加：

| State | Direction | ID | Packet |
|---|---|---:|---|
| Login | clientbound | 1 | `LoginReject`，新增 code 7 |
| Play | clientbound | 7 | `RemotePlayerSpawn` |
| Play | clientbound | 8 | `RemotePlayerDespawn` |
| Play | clientbound | 9 | `RemotePlayerStates` |

`LoginRejectCode(7)` 固定为 `LoginAlreadyOnline`。满员继续使用
`LoginServerFull(1)`，存档 cache 满继续使用 `LoginStoreUnavailable(4)`。

### 6.2 消息值

```go
type RemotePlayerSpawn struct {
    PlayerID    core.PlayerID
    DisplayName string
    ServerTick  uint64
    Dimension   core.DimensionID
    Position    mgl32.Vec3
    Yaw, Pitch  float32
}

type RemotePlayerDespawn struct {
    PlayerID core.PlayerID
}

type RemotePlayerStates struct {
    ServerTick uint64
    Players    []RemotePlayerState
}

type RemotePlayerState struct {
    PlayerID   core.PlayerID
    Dimension  core.DimensionID
    Position   mgl32.Vec3
    Yaw, Pitch float32
    Reset      bool
}
```

所有三类值同时实现 `ServerPacket` 和 `ServerMessage`。客户端没有声明、生成或回传远端
玩家状态的 packet。

### 6.3 线上布局

字段严格按下列顺序编码：

- Spawn：16-byte PlayerID、bounded UTF-8 string、`u64 ServerTick`、`i32 Dimension`、
  3×IEEE-754 little-endian `f32 Position`、`f32 Yaw`、`f32 Pitch`；
- Despawn：16-byte PlayerID；
- States：`u64 ServerTick`、canonical uvarint count、重复 count 次 State；
- State：16-byte PlayerID、`i32 Dimension`、3×`f32 Position`、`f32 Yaw`、
  `f32 Pitch`、canonical bool `Reset`。

States count 必须在 1..7。PlayerID 必须为 UUIDv4，按 16 个原始字节严格升序且不得
重复。Dimension 必须是 Overworld。位置和旋转必须有限。Spawn 的昵称必须通过
`core.NormalizeDisplayName` 且已经是规范值。States 的精确最大逻辑 payload 为
`8 + 1 + 7*(16+4+12+8+1) = 296 bytes`，小于 512-byte M3C 门禁和 64 KiB
小包上限。

## 7. Session 身份与接纳

`SessionSpec` 增加：

```go
PlayerID    core.PlayerID
DisplayName string
```

`AttachSession` 在注册 sim 玩家前验证 ID、规范昵称和非零 session/generation。session
保存这两个展示值，但 sim 仍只收到 SessionID 和 PlayerRestore。

Host 使用：

```go
activeByPlayer  map[core.PlayerID]*activeLogin
activeBySession map[sim.SessionID]*activeLogin
```

`activeByPlayer` 同时包含 pending 和 Play，`activeBySession` 只包含已分配 SessionID 的
登录。登录在同一把 Host mutex 内依次判断：

1. `PlayerID` 已存在：拒绝新连接 `AlreadyOnline`，保留旧会话；
2. `len(activeByPlayer) == MaxPlayers`：拒绝 `ServerFull`；
3. 预留 pending 身份和容量；
4. 用 PendingLogin 的 10 秒 context 加载玩家；
5. 成功后分配非零、单调递增且进程内不复用的 SessionID/generation；
6. attach、Activate、LoginSuccess、Confirm；
7. 任一失败按逆序释放，不留下 pending 身份或空 session 索引。

同名不同 ID 合法。SessionID 溢出保持 `LoginInternalError`，不回绕。相同 ID 竞争在任何
PlayerStore 读取前决定，只有一个请求能触发加载。

## 8. 玩家兴趣集合

每个 session 增加：

```go
visiblePlayers map[core.PlayerID]visiblePlayer

type visiblePlayer struct {
    Session    sim.SessionID
    Generation uint64
}
```

目标玩家对观察者可见必须同时满足：

- 观察者与目标都为 `Ready=true`；
- 目标不是观察者本人；
- 两者维度相同；
- 目标脚底位置所在 chunk 在观察者当前 wanted set；
- 该 chunk 的 publication 已向观察者成功发送完整快照；
- 目标 session 仍存在且 identity 元数据有效。

可见键使用 PlayerID，但 value 同时记录 SessionID/generation。同一个 PlayerID 断线后
快速重连不会被误判为连续实体；generation 变化必须执行旧 Despawn、再执行新 Spawn，
客户端重新建立插值缓冲。

每 tick 先按 PlayerID 计算规范目标集合，再与 `visiblePlayers` 差分：

- 旧有新无：Despawn；
- 旧有新有但 session/generation 改变：Despawn + Spawn；
- 旧无新有：Spawn；
- 两边相同：加入 States batch。

本 tick 新 Spawn 的玩家不重复进入 States；下一 tick 起进入 batch。没有稳定可见玩家时
不发送空 States。

## 9. Publication 顺序与背压

每个观察者每 tick 的消息顺序固定：

1. 远端 Despawn，按 PlayerID；
2. `ForgetChunks`；
3. 新 chunk snapshot 和 block delta 的既有规范顺序；
4. 远端 Spawn，按 PlayerID；
5. 一个 `RemotePlayerStates` batch；
6. 观察者本人的 `PlayerState` 和 command rejection；
7. heartbeat 仍由 session writer 独立管理。

实现可以在现有 chunk publication 内调整 snapshots/deltas 与 forget 的内部准备顺序，但
线上必须满足“Despawn 先于对应 Forget、chunk snapshot 先于对应 Spawn”。只有
`current.enqueue` 成功后才能更新 `visiblePlayers`，因此 outbox 失败不会提交半个 roster
状态。

heartbeat 可以在上述 tick publication 消息之间穿插，但不得改变 publication 消息彼此的
相对顺序，也不参与 roster 提交。

任一 session outbox 满只 detach 该慢客户端，错误为稳定的 slow-client disconnect；其他
session 和 world tick 继续。单观察者每 tick 最多一个 States batch，不为每个玩家创建
独立高频 packet。

## 10. 多身份持久化

单缓存 `playerPersistence` 改为：

```go
cache map[core.PlayerID]*cachedPlayer
jobs chan playerSaveJob        // capacity 16
completions chan playerSaveCompletion // capacity 2
```

保留两个固定保存 worker。每个身份独立维护 persisted revision、最新 snapshot、dirty、
inFlight、retry 和 active/pending 状态；同一身份最多一个 SavePlayer 在途，不同身份允许
由两个 worker 并行保存。

规则：

- active、pending、dirty、inFlight 或 retry 的条目计入 16-entry 上限；
- clean 且非 active/pending 的条目立即驱逐；
- `Prepare` 对 cache miss 必须先在 persistence mutex 内驱逐可驱逐项、检查容量并插入
  pending placeholder，再释放锁执行 `LoadPlayer`；因此并发 cache miss 不可能共同越过
  16-entry 上限，也不会在容量已满时启动 Store 读取；
- `LoadPlayer` 失败删除本次 placeholder；后续 `Abort` 按相同规则删除无 snapshot、clean、
  非 active 的 placeholder，不能留下占位泄漏；
- 缺失身份在 LoginSuccess/Confirm 前不变 dirty，不保存候选昵称；
- 新身份在 16 个条目均不可驱逐时返回 `ErrPlayerPersistenceBackpressure`，映射为
  `StoreUnavailable`；
- 已在 cache 的身份即使 dirty/retry 仍可 Prepare，用最新内存快照重连；
- A 保存失败不阻塞已缓存 B 的重连、观察或保存；
- completion 按 PlayerID 归还对应 cache，绝不依赖“当前唯一玩家”；
- autosave tick 到达时按 PlayerID 排序调度，queue 满则留 dirty 到下一次 Poll；
- retry 继续使用 M3B 的 `20, 40, 80 ... 1200 tick` 上限并重用相同 revision/bytes；
- Flush 忽略 backoff，按 PlayerID 排序排空。一次 Flush 中，每个失败身份的同一 revision
  只尝试一次：记录该次终态错误、继续处理其他身份，不立即重投形成忙循环；失败身份保留
  dirty/retry，供下一次 Shutdown 重试。只有保存成功期间又观察到更高 revision 时，才在
  同一次 Flush 继续调度该身份；多个终态错误以稳定 PlayerID 顺序 Join。

所有 Store 调用在 mutex 外执行，且不在 world tick goroutine。cache map、job queue 和
completion queue 都有硬上限。

## 11. 断线与关服

单 session 退出：

1. Server 按 SessionID/generation 注销 sim 玩家并生成 SessionExit 最终快照；
2. Host force Observe 对应 PlayerID；
3. 从 activeBySession 和 activeByPlayer 删除；
4. 下一 world tick 的兴趣差分向观察者发送 Despawn；
5. 保存失败留在该身份 cache 内重试，不停止 Host。

Host Shutdown：

1. 标记 closing，关闭 listener，拒绝新 pre-login；
2. 关闭并等待所有 pre-login stream；
3. 按 SessionID 升序 detach 全部 Play session；
4. 收集每个 SessionExit 并 force Observe；
5. Flush 全部玩家；
6. 玩家 flush 成功后才停止 RunTicks 并调用 world Shutdown；
7. world Store sync/close 成功后停止玩家保存 worker。

任一玩家 flush 失败时保留 world、Store、RunTicks 和 player worker，使第二次 Shutdown
可以重试。Shutdown gate 继续串行并发调用。非法 packet、heartbeat timeout、slow client
或渲染端协议错误只清理对应 session。

## 12. 客户端 roster 状态机

`client.RemotePlayers` 公开纯值接口：

```go
func NewRemotePlayers() *RemotePlayers
func (r *RemotePlayers) Apply(network.ServerMessage) error
func (r *RemotePlayers) Advance(time.Duration)
func (r *RemotePlayers) Presentations() []RemotePresentation
func (r *RemotePlayers) Reset()
```

roster 规则：

- Spawn 要求 ID 尚不存在；创建 metadata 和首个快照；
- Despawn 要求 ID 已存在；立即删除并释放标签引用；
- States 中每个 ID 必须已经 Spawn；
- States.ServerTick 必须严格大于该玩家最后接收 tick；
- 重复 Spawn、未知 Despawn、未知 State、tick 倒退或非法值返回协议错误；
- 客户端应用收到协议错误后关闭本地 Receiver/endpoint；服务端只清理对应连接，不影响
  其他 session；
- connection close、dimension reset 或应用 Close 调用 roster.Reset，下一连接不继承实体。

服务端保证 batch 已排序；客户端仍独立验证，不能把 Server 的正确性当作不可信网络输入
的安全边界。

## 13. 100 ms 插值

每个远端玩家持有最多 4 个按 tick 升序的快照。收到最新 tick `N` 后，记录从该 batch
到达开始的单调 elapsed，渲染目标为：

```text
targetTick = N + clamp(elapsed * 20, 0, 1) - 2
```

因此 target 在 `N-2 .. N-1` 间移动，使用环形缓冲中包围 target 的两帧插值。下一份
20 Hz snapshot 到达时，N 增加 1、elapsed 归零，target 连续。网络停顿超过一个 tick
时 target 不再前进，不预测、不外推。

查找包围 target 的样本时，低于最旧样本就保持最旧姿态，高于最新样本就保持最新姿态；
tick gap 本身不触发 reset，仍只在两份权威样本之间插值。这样即使批次 tick 不连续，
也不会越过任一端样本外推。

位置与 pitch 线性插值，yaw 走 `[-π, π]` 最短角度。以下情况清空旧缓冲、显示新姿态并
重新积累至少 3 帧后恢复插值：

- Spawn；
- `Reset=true`；
- Dimension 改变；
- 两份相邻状态的脚底位置距离严格大于 8 格。

缓冲只有 1 或 2 帧时保持最近姿态。浮点时间只用于展示，不反馈权威模拟或本地 Predictor。

## 14. 方块人渲染

`render.AvatarRenderer` 为每个远端玩家绘制静态头、身体、双臂、双腿，总边界约
0.6×1.8 格。它不做骨骼、皮肤、动画、碰撞或阴影。基础色由 PlayerID 的稳定 hash
映射到固定调色板，因此跨客户端和重启一致。

Avatar pass 使用独立 shader、instance buffer 和 pipeline，正常 depth test/depth write。
输入按 PlayerID 排序，最多 7 个 avatar；GPU buffer 按硬上限一次分配，不随玩家数量扩容。

## 15. Unicode 昵称标签

字体固定为官方 `NotoSansCJKsc-Regular.otf`。源文件路径：

`https://github.com/notofonts/noto-cjk/blob/main/Sans/OTF/SimplifiedChinese/NotoSansCJKsc-Regular.otf`

字体使用 SIL Open Font License 1.1，许可证随资产保存。项目不修改字体名称，不在构建期
联网。导入提交记录下载来源 tag/commit、文件 byte size 和 SHA-256。

字体解析与栅格化使用 `golang.org/x/image/font/opentype` v0.44.0。字体在应用启动时只
解析一次；单后台 worker 按需栅格化昵称所需 rune。Glyph result 进入有界队列，render
线程在现有上传预算内写入 1024×1024 R8 atlas。

atlas 使用 32×32 cell，共 1024 slots。slot 0 固定为缺字方框；其余按首次请求顺序分配，
不驱逐、不扩容。单个同时在线集合的 8 个昵称最多 256 rune，加常用 ASCII 后低于容量；
但 atlas 生命周期覆盖整个客户端进程，连续更换不同昵称会累积 glyph。确定性保证仅覆盖
进程中最先成功分配的 1023 个不同 glyph，之后首次出现的合法 glyph 使用 slot 0；这属于
已接受的有界降级，而不是扩容或崩溃。字体本身缺少 glyph 时同样使用 slot 0。

`NameTagRenderer` 在头顶绘制 camera-facing billboard：depth test 开启、depth write
关闭，避免穿墙显示或污染地形深度。标签布局最多 32 rune，使用字体 advance/kerning，
背景为半透明深色矩形。字体 worker 或上传错误返回明确客户端错误；avatar 与权威世界
状态不受影响。

## 16. 测试策略

### 16.1 Network

- v2 全 packet ID 完整性和 golden；
- Spawn/Despawn/States 精确字节 golden 与 roundtrip；
- count 0/8、重复/乱序 ID、非法 UUID、非法 UTF-8、非规范昵称、非有限 float；
- v1 ClientHello 稳定 VersionMismatch；
- Memory/TCP 相同多人 transcript；
- codec/frame fuzz 不 panic、不超上限分配。

### 16.2 Server publication

使用手动 Step 和固定 session，覆盖观察者/目标 Ready 矩阵、同/异维度、视距边界、目标
chunk wanted 但尚未 snapshotSent、进入/离开、generation 替换和确定性排序。断言线上
顺序满足 Despawn→Forget 与 Snapshot→Spawn。

### 16.3 Host

- 8 个并发登录全部成功，第 9 个 ServerFull；
- 相同 ID 并发只有一个成功，失败方 AlreadyOnline，且只调用一次 PlayerStore Load；
- 同名不同 ID 均成功；
- session IDs 从 1 单调递增且不复用；
- 一个 session malformed/timeout/slow 不影响其余 7 个；
- 断线清理两个索引并 force Observe；
- shutdown 顺序、失败重试和 goroutine cleanup 在 race 下高重复。

### 16.4 Persistence

可控 Store 覆盖 16-entry 上限、两 worker 并行、每身份单在途、同 revision retry、最新
snapshot 合并、并发 miss 原子占位、clean eviction、completion queue 满时的有界背压、
同 ID 重连、跨 ID 失败隔离、单次 Flush 不忙循环和多错误稳定顺序。

### 16.5 Client

纯单元测试覆盖 roster 生命周期、4-slot ring、targetTick、yaw wrap、停顿 hold、重置条件、
协议错误和排序输出。测试使用手工常量，不用实现逻辑构造 expected。

### 16.6 Render/font

headless gfx 验证 avatar instance、固定 buffer 上限、pipeline depth 配置、glyph layout、
中文 rune、缺字 fallback、顺序昵称 churn 后 atlas 满的稳定 tofu、预算化 upload 和
Release。任何自动测试都不得创建或聚焦 GLFW window。

### 16.7 纵向测试

- 两个真实 TCP 客户端：相互 Spawn、移动 States、方块同步、Despawn；
- 8 个 headless TCP 客户端运行固定输入脚本 10 秒；
- Memory 手动推进 8 session 2000 tick；
- 8 玩家 DiskStore 关服重启，逐 ID 比较位置、安全落点、昵称和 revision；
- Memory/TCP 比较业务 transcript、玩家 hash、区块 hash 和镜像 hash；
- 所有场景在 race 下无泄漏，无前台窗口。

## 17. 性能与门禁

性能场景升级为 v6，包含本地玩家、7 个可见远端 avatar、Unicode 昵称标签和每 tick 一个
7-player States batch。新增记录：

- remote state encode/decode p50/p95/p99；
- interest diff p50/p95/p99；
- remote roster apply/interpolation p50/p95/p99；
- avatar/name-tag CPU submit 和 GPU frame contribution；
- 8-client server outbound bytes、queue high-water 和 RSS。

既有门禁不得放宽：≥100 FPS、frame p99 `<12 ms`、RSS `<2 GiB`、server tick p99
`<10 ms`、tick max `<50 ms`、physics `0 allocs/op`。相同机器相对 M3B accepted
baseline 任一指标退化超过 20% 判红。8-client soak 中所有 outbox/job queue 必须回落，
goroutine 数不得随 tick 持续增长。

CI 继续运行全量 test/race/fuzz smoke/vet/gofmt/archcheck、无窗口 benchmark、
`CGO_ENABLED=0 GOOS=linux go build ./cmd/mcgod` 和 diff-check。

## 18. 依赖方向

- `network` 不 import `server`、`client`、`sim`、`storage` 或渲染包；
- `sim` 不 import `network`、`storage`；
- `server` 不 import `client`、`render`、`gfx`；
- `client` 可以 import `network` 和纯数学值，不 import `server`；
- `render` 可以 import `client` 的纯 presentation 值或由 cmd 转换后的 render 值，但不
  import `network`；实施计划优先用 render 自有 instance 值避免反向耦合；
- `cmd/mcgod` 不 import `client`、`render`、`gfx`、GLFW、WebGPU、font rasterizer；
- 只有客户端字体组件 import `golang.org/x/image/font/opentype`；
- 字体资产通过 `go:embed` 进入客户端，不进入 mcgod 的依赖闭包。

## 19. 退出条件

以下条件必须同时满足：

- 配置 1..8 在线人数有效，默认 8；
- 8 个不同 ID 可同时 Play，第 9 个稳定 ServerFull；
- 同 ID 新连接稳定 AlreadyOnline，旧会话不受影响；
- 同名不同 ID 正常显示并独立保存；
- PlayerID/SessionID 边界不混淆，客户端从未收到 SessionID；
- v2 协议 golden、fuzz 和 v1 拒绝门禁通过；
- 兴趣范围、Spawn/Despawn/States 顺序确定且有界；
- 客户端远端插值无外推，重置和停顿行为稳定；
- 典型 8-client Unicode 昵称能通过内嵌字体显示，长期 glyph churn 超过 atlas 上限时
  稳定降级为 tofu，字体许可证随仓库；
- 慢或恶意客户端只清理自身 session；
- 16-entry cache 和所有队列上限可证明；
- 玩家保存失败隔离，同 ID 可从最新缓存重连；
- 关服失败可重试，8 玩家状态跨重启一致；
- 两客户端真实 TCP 纵向场景闭合；
- 8-client soak、Memory/TCP parity、race、泄漏和性能门禁通过；
- 自动验证过程不启动前台窗口；
- M3 总里程碑“存档与联机”关闭，可以进入 M4。

## 20. 已知取舍

| 取舍 | 决定 | 原因 |
|---|---|---|
| 人数 | 默认/最大 8 | 可信 LAN 目标，足以证明多人边界且保持固定上限 |
| 可见性 | 服务端按区块视距 | 与现有 subscription 共用真值，避免全局泄漏和客户端过滤 |
| 远端状态 | 每观察者每 tick 一个 batch | 7 人规模下简单、有界且比逐人 packet 更稳定 |
| 插值 | 4 帧、100 ms、无外推 | 平滑并避免客户端替远端玩家预测权威行为 |
| 身份冲突 | 保留旧会话，拒绝新会话 | 不允许网络抖动或冒充者踢掉当前玩家 |
| 昵称 | 不唯一 | PlayerID 才是身份，昵称只是 Unicode 展示值 |
| 模型 | 静态方块人 | 闭合多人可见性，不提前进入 M4 动画/装备系统 |
| 字体 | 内嵌 Noto Sans CJK SC | 跨平台、构建离线、中文昵称确定性呈现 |
| 持久化 | 16 身份 cache、2 worker | 允许失败隔离，又防止磁盘故障下身份轮换造成无界内存 |
| 协议兼容 | 只支持 v2 | 首个多人版本无需同时维护无多人语义的 v1 |
