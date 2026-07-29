# minecraft-go M3B 二进制协议、TCP 直连与稳定玩家身份设计

- 日期：2026-07-29
- 状态：设计内容已逐段确认，等待书面复核
- 前置里程碑：M3A 世界存档持久化
- 后续里程碑：M3C 多 session 同步与远端玩家呈现

---

## 1. 目标

M3B 是 M3“存档与联机”的第二个子项目。它把 M3A 仅能通过进程内
`MemoryTransport` 游玩的单 session 世界，推进为具有稳定线上契约、可通过
TCP 直连、可运行专用服务端，并能按稳定玩家身份恢复位置的系统。

M3B 必须交付以下能力：

- 端无关的 `Handshake → Login → Play` 协议状态机；
- 严格有界、可 fuzz 的版本化二进制编解码；
- TCP 客户端与服务端端点；
- `mcgo --connect host:port` 远程直连；
- 无窗口、无图形依赖的 `mcgod` 专用服务端；
- 本机全局玩家档案和稳定 128 位 `PlayerID`；
- 以 `PlayerID` 为键的最小玩家状态存档；
- 单机内存传输与远程 TCP 传输的行为一致性门禁；
- 一次只允许一个 Play 玩家，第二个登录得到明确的 `ServerFull`；
- 断线、重连、关服和保存失败均具有可验证的生命周期。

M3B 完成后，应能在同一局域网内运行一个 `mcgod`，由一个 `mcgo` 客户端通过
IP 地址加入、移动、挖掘和放置；客户端断开或服务端重启后，世界修改和玩家位置
都能恢复。

---

## 2. 明确不做

以下能力不属于 M3B：

- 两名及以上玩家同时进入 Play；
- 远端玩家实体、插值、名字显示和玩家间广播；
- 局域网服务器自动发现、服务器列表和连接历史；
- 游戏内连接菜单或断线后的自动重连；
- TLS、流量加密、账号密码、中心账号服务、公钥挑战或反冒充；
- NAT 穿透、中继、UPnP 和公网部署支持；
- 跨协议版本兼容、协议降级或运行时版本迁移；
- 物品栏、生命、饥饿和其他 M4 生存数据；
- 云存档、玩家档案同步和多人权限管理；
- 将内存传输改成必须经过二进制序列化。

M3B 的离线 `PlayerID` 由客户端声明，不构成身份认证。局域网中的恶意客户端可以
复制别人的 ID 并冒充该玩家。因此 M3B 服务端不得被描述为可安全暴露到不可信
互联网；公网安全必须另行设计。

---

## 3. 核心术语与不变式

### 3.1 身份与会话

- `PlayerID`：16 字节随机稳定身份，跨客户端重启、服务器重启和不同世界保持不变。
- `SessionID`：由服务端在一次成功登录后分配的临时标识，仅在该 Play 会话内有效。
- 昵称：可修改的显示字段，不参与身份比较，也不得用作文件名。

必须保持：

- 客户端从不发送或选择 `SessionID`；
- 权威命令的 `SessionID` 由已完成登录绑定的服务端连接上下文附加；
- 修改昵称不会创建新玩家，也不会改变 `PlayerID`；
- 玩家文件路径只由规范化 `PlayerID` 推导，任何昵称都不能影响路径。

### 3.2 协议状态

每条连接恰好处于以下一个状态：

```text
Connected → Handshake → Login → Play → Closing → Closed
```

协议状态只能向右推进。任何倒退、跳跃、在错误状态发送数据包，或关闭后继续发送，
都属于协议错误。

### 3.3 数据所有权

- TCP 解码得到的新消息及其切片由接收方独占；
- 内存传输成功发送后，发送方不得继续修改消息引用的切片；
- 服务端发布区块快照和增量前必须完成拥有权转移或深拷贝；
- 单机与 TCP 的消费者都只能看到不可变消息值。

这一约束延续总设计中的“内存传输跳过序列化，但不能共享可变对象”。

---

## 4. 总体架构

M3B 将当前“`Server` 绑定一个 endpoint、固定 `localSessionID`”的结构拆成四层。

### 4.1 协议层

`internal/network` 负责：

- 协议消息类型；
- 状态与方向对应的数据包注册表；
- 帧编码与解码；
- payload 编码与解码；
- 输入上限和结构校验；
- 共享的客户端/服务端协议状态机。

协议层不依赖 `server`、`client`、`sim` 或 `storage`。

### 4.2 传输层

传输层提供两种实现：

- 内存端点：传递不可变的类型值，不做二进制编解码和 zstd；
- TCP 端点：在独立 I/O goroutine 中完成 framing、编解码和区块快照压缩。

两者调用同一状态机和消息校验器。TCP 不是第二套游戏协议，只是类型消息的线上表示。

共享协议 driver 包裹传输特有的 packet stream。Handshake/Login 完成前，原始 stream 不暴露
给 `client`、`server` 或 `sim`；成功后 driver 才返回被类型收窄为 Play 消息集合的
`ClientEndpoint` / `ServerEndpoint`。因此上层不可能绕过状态机发送登录前 Play 包。

`network.Listener` 抽象负责接收 server packet stream、返回对端地址和关闭监听。TCP 实现
在 `network` 内部封装 `net.Listener`；`server.Host` 只依赖该抽象。内存模式通过显式注入
packet stream 接入 Host，不伪造 socket，也不要求 Host import `net`。

### 4.3 Host 与世界运行时

新增长期存活的 `server.Host`：

- 持有世界模拟、生成 worker、区块与玩家存储；
- 在专用模式持有 listener，在单机模式接收内存连接；
- 管理握手、登录、在线槽和 session 生命周期；
- 将稳定 `PlayerID` 映射到临时 `SessionID`；
- 在 tick 边界接入或注销玩家；
- 在断线、自动保存和关服时调度玩家存档。

世界运行时不再因唯一 endpoint 断开而结束。专用服务端的玩家离线后仍继续运行并等待
下一次登录；内置 Host 则随客户端关闭而安全关服。

### 4.4 命令入口

- `cmd/mcgo`：图形客户端；默认单机，也可远程直连；
- `cmd/mcgod`：无渲染专用服务端；
- `cmd/perfcheck`：继续承担基线比较，不引入前台窗口。

`mcgod` 不得 import `client`、`render`、`gfx`、GLFW 或 WebGPU。

---

## 5. 连接与登录数据流

### 5.1 正常流程

```text
建立 Memory 或 TCP 连接
  → ClientHello(protocolVersion)
  → ServerHello(protocolVersion)
  → LoginStart(playerID, displayName)
  → 原子占用唯一 Play 槽
  → 从内存脏缓存或磁盘加载玩家
  → 在 tick 边界分配 SessionID 并注册玩家
  → LoginSuccess(playerID)
  → Play
  → 加载玩家周边区块
  → PlayerState(Ready=false)
  → 位置校验完成
  → PlayerState(Ready=true, Reset=true)
```

`LoginSuccess` 表示服务端已经完成槽位占用和 session 注册，而不是“稍后尝试”。在它发出
之前，不得向该连接发送任何 Play 数据包。

### 5.2 登录失败

登录失败必须返回稳定错误码和有界的人类可读消息：

- `VersionMismatch`：协议版本不同；
- `ServerFull`：已有 Play 玩家或另一个登录已占用槽位；
- `InvalidIdentity`：ID 或昵称非法；
- `PlayerDataCorrupt`：玩家文件损坏或版本过新；
- `StoreUnavailable`：玩家存储暂时不可用；
- `ProtocolViolation`：状态或数据包非法；
- `InternalError`：未分类的服务端内部失败。

人类可读消息只用于日志或终端显示；客户端行为由错误码决定。服务端不得把路径、堆栈、
底层系统错误或其他敏感内部信息发给客户端。

### 5.3 断线

任意 reader、writer、心跳或显式关闭路径只产生一次 session 关闭事件：

1. 立即停止接受该连接的新输入；
2. 取消 endpoint I/O；
3. 在 tick 边界注销 `SessionID`；
4. 生成最新玩家存档快照并标记 dirty；
5. 释放 Play 在线槽；
6. 后台保存玩家；失败则保留内存记录并退避重试。

旧连接缓冲中的数据在 session generation 失效后必须被丢弃，不能影响后来重连的同一
`PlayerID`。

若断线玩家仍有未成功持久化的 dirty 记录，Host 只允许同一 `PlayerID` 从内存最新状态
重连；其他 ID 暂时得到 `StoreUnavailable`。dirty 成功刷盘后恢复普通接纳。这个退化模式
把离线脏玩家缓存严格限制为一份，避免磁盘故障期间恶意轮换 ID 造成无界内存增长。

---

## 6. 二进制帧格式

### 6.1 帧

每个 TCP 数据包使用：

```text
[uvarint frameLength] [uvarint packetID] [payload]
```

`frameLength` 是 `packetID + payload` 的字节数，不包含自身。规则如下：

- `frameLength` 必须大于零且不超过 2 MiB；
- `uvarint` 最多 5 字节，并且必须是最短规范编码；
- 收到完整长度前不得按 payload 内字段分配；
- TCP 拆包、粘包和短读不改变语义；
- 一条连接同方向只允许一个 writer，保证帧不交错。

除区块快照外，任何单包 payload 不得超过 64 KiB。区块快照压缩部分不得超过 1 MiB，
声明的解压长度不得超过 2 MiB。

### 6.2 基础类型

| 类型 | 编码 |
|---|---|
| `bool` | 单字节 `0` 或 `1`，其他值非法 |
| `int8` / `uint8` | 单字节 |
| `uint16` / `uint32` / `uint64` | 小端固定宽度，除明确声明为 uvarint 的字段 |
| `int32` / `int64` | 对应无符号位模式的小端固定宽度 |
| `float32` | IEEE 754 小端；NaN 和 Inf 非法 |
| 计数/长度 | 规范 `uvarint`，先验上限后分配 |
| `PlayerID` | 原始 16 字节 |
| 字符串 | `uvarint byteLength + UTF-8 bytes` |

字符串必须是有效 UTF-8。昵称去除首尾 Unicode 空白后为 1–32 个 Unicode 标量，禁止
控制字符；编码后不得超过 128 字节。协议错误消息不得超过 256 字节。

### 6.3 数据包注册表

数据包 ID 在“状态 + 方向”内唯一，不承诺跨状态含义相同。

| 状态 | 方向 | ID | 消息 |
|---|---|---:|---|
| Handshake | C→S | 0 | `ClientHello` |
| Handshake | S→C | 0 | `ServerHello` |
| Handshake | S→C | 1 | `HandshakeReject` |
| Login | C→S | 0 | `LoginStart` |
| Login | S→C | 0 | `LoginSuccess` |
| Login | S→C | 1 | `LoginReject` |
| Play | C→S | 0 | `PlayerInput` |
| Play | C→S | 1 | `BreakBlock` |
| Play | C→S | 2 | `PlaceBlock` |
| Play | C→S | 3 | `RequestChunkResync` |
| Play | C→S | 4 | `KeepAliveReply` |
| Play | S→C | 0 | `ChunkSnapshot` |
| Play | S→C | 1 | `BlockChanges` |
| Play | S→C | 2 | `ForgetChunks` |
| Play | S→C | 3 | `PlayerState` |
| Play | S→C | 4 | `CommandRejected` |
| Play | S→C | 5 | `KeepAlive` |
| Play | S→C | 6 | `Disconnect` |

注册表由测试冻结：同一状态和方向不能重复 ID；每个封闭消息类型必须恰好注册一次；未知
ID 一律是协议错误。

### 6.4 Payload 布局

下表中的字段严格按书写顺序编码。`dim` 为小端 `int32`，M3B 只接受 `core.Overworld`；
`chunkX/chunkZ` 和方块坐标为小端 `int32`；`blockID` 为小端 `uint16` 且必须小于 `1<<15`。

| 消息 | Payload |
|---|---|
| `ClientHello` | `protocolVersion uvarint` |
| `ServerHello` | `protocolVersion uvarint` |
| `HandshakeReject` | `serverProtocolVersion uvarint, code uint8, message string` |
| `LoginStart` | `playerID [16]byte, displayName string` |
| `LoginSuccess` | `playerID [16]byte` |
| `LoginReject` | `code uint8, message string` |
| `PlayerInput` | `sequence uint64, moveX int8, moveZ int8, jump bool, yaw float32, pitch float32` |
| `BreakBlock` | `sequence uint64, yaw float32, pitch float32` |
| `PlaceBlock` | `sequence uint64, yaw float32, pitch float32, blockID uint16` |
| `RequestChunkResync` | `sequence uint64, dim int32, chunkX int32, chunkZ int32, haveRevision uint64` |
| `ForgetChunks` | `dim int32, count uvarint, count × (chunkX int32, chunkZ int32)` |
| `PlayerState` | `serverTick uint64, lastInputSequence uint64, dim int32, position 3×float32, velocity 3×float32, yaw float32, pitch float32, onGround bool, ready bool, reset bool` |
| `CommandRejected` | `sequence uint64, reason uint8` |
| `KeepAlive` / `KeepAliveReply` | `token uint64` |
| `Disconnect` | `code uint8, message string` |

`BlockChanges` 使用：

```text
dim int32
chunkX int32, chunkZ int32
baseRevision uint64, newRevision uint64
changeCount uvarint
changeCount × (blockX int32, blockY int32, blockZ int32, blockID uint16)
```

`changeCount` 为 1–4096，change 必须按现有 chunk block index 严格递增，并保持
`newRevision == baseRevision + 1`。更大的权威批次不是合法 M3B 消息；未来需要时必须设计
不会破坏 revision 原子性的分片协议。

`ForgetChunks.count` 为 1–4096。单次需要忘记更多区块时，服务端按规范 `ChunkPos` 顺序拆成
多个消息。所有 count 都先检查数据包剩余字节和类型上限，再进行切片分配。

`ChunkSnapshot` 的外层 payload 为：

```text
decodedLength uint32
compressedLength uint32
compressedBytes [compressedLength]byte
```

解压后的规范逻辑 payload 为：

```text
dim int32
chunkX int32, chunkZ int32
revision uint64
sectionCount uvarint                       // 必须等于 24
section[0..23]，每个先写 sectionY uint8, storage uint8
  Single:  blockID uint16
  Indexed: bits uint8, paletteCount uvarint, palette × uint16,
           wordCount uvarint, packed × uint64
  Direct:  bits uint8, wordCount uvarint, packed × uint64
```

`sectionY` 必须依次为 0–23；Indexed bits 只能为 4 或 8；Direct bits 只能为 15；palette、
word count、未使用高位和 palette slot 必须满足现有 `SectionData.Validate`。解码后不允许尾随
字节。

线上错误码使用固定 `uint8`，不直接编码 Go 字符串枚举：

- Handshake：`1=VersionMismatch`；
- Login：`1=ServerFull`、`2=InvalidIdentity`、`3=PlayerDataCorrupt`、
  `4=StoreUnavailable`、`5=ProtocolViolation`、`6=InternalError`；
- Play Disconnect：`1=ProtocolViolation`、`2=Timeout`、`3=ServerShutdown`、
  `4=SlowClient`、`5=InternalError`；
- Command rejection：按现有八种 `RejectReason` 的声明顺序固定为 1–8。

错误码 `0` 和所有未知值非法。Go 域类型可继续使用可读字符串，但 wire codec 必须通过显式
表做双向映射；不得依赖枚举默认序号或反射。

### 6.5 协议版本

M3B 定义唯一当前协议版本 `1`。`ClientHello` 和 `ServerHello` 都携带版本号，成功握手
必须精确相等。

M3B 不做版本范围、特性协商或旧版本解码。未来修改线上字节或消息语义时提升版本号；
是否建立兼容窗口由当时的独立设计决定。

---

## 7. 区块快照压缩

只有 TCP 的 `ChunkSnapshot` payload 使用 zstd。编码顺序为：

1. 对类型消息执行完整结构验证；
2. 生成独立的规范化区块快照逻辑字节；
3. 确认逻辑字节不超过 2 MiB；
4. 使用单并发 zstd encoder 和 checksum 压缩；
5. 确认压缩字节不超过 1 MiB；
6. 写入 `decodedLength + compressedLength + compressedBytes`。

解码器在创建输出前验证两个长度，为 zstd decoder 设置 2 MiB 内存/输出上限，并确认
实际解压长度与声明值完全一致。解压后仍需执行区段数量、顺序、调色板、位宽、方块 ID
和 revision 的现有验证。

线上快照格式与 M3A 磁盘 chunk envelope 相互独立。`network` 不得 import `storage`，也不
直接复用磁盘格式；这样存档迁移与线上协议升级可以分别演进。

内存传输继续直接传 `ChunkSnapshot` 类型值，不做一次无意义的压缩/解压，但必须调用与
TCP 相同的消息验证函数。

---

## 8. TCP 传输

### 8.1 连接选项

TCP 连接启用：

- `TCP_NODELAY`，避免小型输入包被 Nagle 算法合并延迟；
- 操作系统 TCP keepalive，作为应用心跳之外的兜底；
- 5 秒 Handshake deadline；
- 10 秒 Login deadline；
- 可取消的 read/write deadline。

deadline 到期、context 取消、peer close 和本地 close 都归一化为稳定错误，并保证阻塞的
Send/Recv 被唤醒。

### 8.2 应用心跳

Play 状态下，服务端每 5 秒发送带非零、单调递增 token 的 `KeepAlive`。客户端必须原样
回复 `KeepAliveReply`；15 秒内未收到匹配回复则关闭连接。token 在每个 session 内从 1
开始，溢出前主动关闭连接，不回绕复用。

每条连接最多有一个未确认 token。错误 token、重复回复或客户端主动发送未被请求的回复
属于协议错误。心跳只检测连接活性，不参与模拟 tick，也不用于时间同步。

### 8.3 接纳上限

专用 Host 可以并发处理少量 pre-login 连接，但必须具有固定上限；M3B 上限为 16。超过
上限的新连接立即关闭。每个 pre-login 连接均受 deadline 约束，不能无限占用 goroutine。

Play 槽只有一个。槽位通过比较并交换式状态转换占用，确保并发登录最多一个成功。失败
连接收到 `ServerFull`，不能触发玩家文件读取、区块加载或模拟注册。

### 8.4 背压与关闭

- 每个 session 保留有界 outbox；满队列关闭慢客户端；
- reader 不直接写世界，只把已经验证并附加 `SessionID` 的命令送入有界输入队列；
- writer 独占 TCP 写方向，并在 I/O goroutine 编码和压缩；
- reader 独占 TCP 读方向，并在 I/O goroutine 解码和解压；
- Close 幂等，并能唤醒 reader、writer、心跳和等待登录的 goroutine；
- 任一 goroutine 出错都通过一次性关闭协调器收敛，不允许双重注销或泄漏。

---

## 9. 本机玩家档案

### 9.1 路径与格式

客户端调用 `os.UserConfigDir()`，在其下创建：

```text
minecraft-go/profile.json
```

目录权限为 `0700`，文件权限为 `0600`。JSON 仅包含：

```json
{
  "version": 1,
  "player_id": "xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx",
  "display_name": "Player"
}
```

上例中的 `x/y` 只是格式说明，实际文件必须是完整的小写 UUID v4 文本；文档和实现不得把
示例值当作固定身份。

ID 使用 `crypto/rand` 生成，并设置 UUID v4 version/variant 位。项目不为此增加 UUID
第三方依赖。

### 9.2 创建与更新

- 档案不存在：创建新 ID，昵称取合法的显式 `--name`，否则为 `Player`；
- 档案存在：严格解析并验证版本、ID、昵称和未知/重复 JSON 字段；
- 显式 `--name`：校验后原子更新昵称，ID 保持不变；
- 未显式传入名称：沿用已保存昵称；
- 格式损坏、版本过新、权限或 I/O 错误：启动失败并显示路径与可操作原因。

档案写入使用同目录临时文件、文件 sync、原子 rename 和目录 sync。不得在损坏时静默生成
新 ID，否则用户已有的服务器玩家存档会看似消失。

`PlayerID` 不是密钥。`0600` 是本地数据卫生措施，不把离线身份提升为认证机制。

---

## 10. 服务端玩家存档

### 10.1 路径

玩家文件位于：

```text
<world>/players/<canonical-player-id>.player
```

文件名只能由 16 字节 ID 格式化得到，固定小写、带连字符；加载接口不接受任意路径。
少量独立玩家文件不会产生区域区块的海量小文件问题，因此不放入 `.region`。

### 10.2 v1 文件内容

玩家 envelope v1 包含：

- magic `MCPL`；
- envelope version；
- player schema version；
- `PlayerID`；
- 单调递增的 player revision；
- payload length；
- CRC32C（Castagnoli）；
- payload。

CRC32C 覆盖 `schema | playerID | revision | payloadLength | payload` 的规范字节，不覆盖 magic、
envelope version 和 checksum 字段自身。magic/version 分别做精确校验，尾随字节一律拒绝。

v1 payload 包含：

- 最后一次合法昵称；
- 当前维度；
- 当前坐标；
- yaw、pitch；
- 是否存在安全落点；
- 安全落点维度与坐标。

当前 payload 上限为 1 MiB，为 M4 扩展预留空间；首版正常文件远小于该上限。所有长度都在
分配前验证，CRC、ID、revision 和尾随字节必须精确匹配。

速度、`OnGround`、输入 sequence 和临时 `SessionID` 不写盘。恢复时速度归零，接地状态由
权威物理重新计算，输入 sequence 从新会话重新开始。

### 10.3 schema 与迁移

玩家 schema 使用与区块相同的原则：

- current 和 oldest 版本常量；
- 从旧版本到 current 的连续迁移函数链；
- 每个历史版本保留二进制 fixture 和逻辑 hash；
- 旧版本成功迁移后标记 `NeedsRewrite`；
- 未来版本返回 `ErrFutureVersion`；
- 未知旧版本、断链或结构损坏返回 `ErrCorrupt`。

M3B current 与 oldest 均为 v1，迁移链为空是合法状态，但连续性门禁必须从第一版存在。

### 10.4 存储接口

区块 `Store` 的职责保持不变，新增独立 `PlayerStore` 契约：

```text
LoadPlayer(ctx, playerID) → StoredPlayer | ErrPlayerNotFound
SavePlayer(ctx, PlayerSave) → committedRevision
```

供 Host 使用的 `WorldStore` 组合区块 Store 与 PlayerStore。`DiskStore` 和 `MemoryStore` 都
实现该组合契约；协议和 sim 不 import storage。

同一玩家最多有一个落盘作业在途。新 revision 在旧作业期间只更新待保存快照；旧作业完成
后若仍 dirty，再提交最新 revision。存储拒绝 revision 倒退，防止迟到完成覆盖新状态。

---

## 11. 玩家恢复与安全落点

### 11.1 安全落点更新

当权威物理结果同时满足以下条件时，更新玩家的内存安全落点：

- 玩家 `OnGround`；
- 玩家 AABB 不与实心方块相交；
- 脚下有完整实心支撑面；
- AABB 与支撑检查所需区块均为 Ready；
- 坐标和朝向均为有限合法值。

安全落点是当前世界状态下最近一次已验证的落脚位置，不是客户端提供的数据。

### 11.2 登录恢复顺序

玩家文件通过结构校验后，Host 按顺序尝试：

1. 保存的当前坐标；
2. 保存的安全落点；
3. 世界元数据中的确定性出生点搜索结果。

每个候选都必须先加载碰撞查询需要的区块，再验证维度、世界高度、有限浮点数和完整玩家
AABB。当前坐标允许在空中恢复，但不能在方块内或世界边界外；安全落点还必须重新验证
支撑面。

文件字节、schema、ID 或数值字段损坏时拒绝登录，不以出生点掩盖损坏。只有数据结构合法、
但位置因后来世界修改而不再可用时，才进入后备候选。

候选确定前，玩家处于 registered/pending 状态：会收到 `Ready=false`，移动、挖掘和放置沿用
现有 `RejectPlayerNotReady`。激活时发送 `Ready=true, Reset=true`，使客户端清空旧预测历史。

---

## 12. 玩家保存生命周期

### 12.1 Dirty 与 revision

玩家在以下事件后标记 dirty：

- 登录后昵称与存档不同；
- 权威位置或朝向相对最近快照变化；
- 安全落点变化；
- 旧 schema 迁移完成；
- session 断开。

位置每 tick 变化不等于每 tick 写盘。Host 只维护最新不可变快照，在自动保存、断线或关服
边界分配新的 player revision 并提交。

### 12.2 自动保存

玩家保存复用 M3A 的后台保存原则，但与区块 batch 分开：

- 编码、CRC、临时文件写入、sync 和 rename 都不在 tick goroutine；
- 自动保存周期与世界 autosave tick 对齐；
- 成功只清除不晚于已提交 revision 的 dirty 状态；
- 失败保留 dirty，按已有上下界退避；
- 保存错误记录时间和根因摘要，但不把内部路径发送给远端客户端。

### 12.3 断线与重连

断线会立即生成最新玩家快照并触发高优先级保存，但不等待磁盘 I/O 才释放 Play 槽。Host
保留该玩家的最新内存记录，直到成功落盘。dirty 未刷净期间只接纳同一 `PlayerID`，并拒绝
其他 ID；因此离线 dirty 缓存不会随登录身份数量增长。

若同一 `PlayerID` 在保存重试期间重连，恢复顺序优先读取这份内存记录，而不是较旧磁盘
文件。新会话继续推进同一 revision 线，不能回退或覆盖未保存位置。

### 12.4 关服

安全关服顺序为：

1. 停止 listener 和新登录；
2. 阻止新的 Play 输入进入模拟；
3. 在 tick 边界注销在线玩家并冻结最终玩家快照；
4. 刷净区块 dirty、retry 和玩家 dirty；
5. `Sync` 并关闭世界存储；
6. 等待 session、网络、生成和保存 worker 退出。

区块或玩家任一保存失败都使 `Shutdown(ctx)` 返回根因，且保留可重试状态。只有全部刷盘并
成功关闭存储后才进入 closed。`mcgod` 因保存失败退出时返回非零状态。

---

## 13. Server 重构边界

### 13.1 移除固定本地 session

M3B 删除 `localSessionID` 作为生产假设。服务端为每次成功登录分配非零、单调递增且本进程
不复用的 `SessionID`。

内部可以使用按 `SessionID` 索引的 session registry，即使 M3B admission 上限仍为 1。这样
发布、拒绝、forget、resync 和输入翻译都基于显式 session，不再把“本地玩家”等同于
“唯一玩家”。提高上限和增加远端玩家实体仍属于 M3C。

### 13.2 Attach/Detach

Host 不直接修改 sim。登录和断线分别产生有序的 attach/detach 控制命令，由 tick 单写者
应用：

- attach 注册 `SessionID`、恢复候选和 view；
- detach 使旧 session generation 失效、移除玩家订阅并生成 forget；
- 同一 tick 中 detach 必须先于后来 session 的 attach；
- session writer 只消费属于自己 generation 的输出。

玩家 ID 只由 Host 和玩家存储使用；sim 继续使用临时 `SessionID`，避免把网络身份概念烧进
端无关物理与世界逻辑。

### 13.3 发布

现有 snapshot、delta、forget、rejection 和 `PlayerState` 发布逻辑改为显式接收目标 session。
M3B 只有一个 session，因此不会广播其他玩家状态；但任何结果都不能再通过硬编码 ID 筛选。

---

## 14. 命令行与用户体验

### 14.1 mcgo

```text
mcgo
mcgo --world worlds/demo --name Chen
mcgo --connect 192.168.1.20:25565 --name Chen
```

- 无 `--connect`：创建内存连接和内置 Host，保持默认单机体验；
- 有 `--connect`：不打开本地世界，建立 TCP 连接；
- `--name`：验证后更新全局档案；
- 显式 `--world` 与 `--connect` 互斥；
- `--benchmark` 与 `--connect`、`--name` 互斥，基准不读写用户档案；
- 连接或协议失败在 stderr/log 中给出稳定错误并以非零状态结束。

远程断线后 M3B 不显示连接菜单也不自动重连。应用停止发送输入，关闭窗口，清理 GPU 与
网络资源后返回错误。

### 14.2 mcgod

```text
mcgod --listen :25565 --world worlds/demo --seed 42
```

- `--listen` 默认 `:25565`；
- `--world` 默认 `worlds/default`；
- `--seed` 默认 `42`，只在创建新世界时生效；
- 已有世界始终使用 metadata 中的 seed；
- 启动日志记录实际监听地址、规范化世界路径和协议版本；
- `SIGINT`、`SIGTERM` 触发带期限的安全关服；
- 无显示器、无窗口、无 CGO 环境下可构建运行。

由于默认 `:25565` 会监听可用网络接口，文档必须同时提示 M3B 没有认证与加密，只适合可信
局域网。需要更窄暴露面时可显式使用 `127.0.0.1:25565`。

---

## 15. 错误处理与日志

### 15.1 不可信网络输入

任何网络输入都按敌意数据处理：

- 解码、状态或域校验失败不得 panic；
- 能在当前状态安全发送错误时，先发送有界 reject/disconnect，再关闭；
- 帧结构已经不可信或写方向失败时直接关闭；
- 单个坏连接不能停止 listener、世界 tick 或其他 worker；
- 日志记录 peer、状态、错误分类和必要上下文，不记录原始任意 payload。

### 15.2 本地不变式

重复 packet ID、非法状态内部跳转、revision 倒退、同一 generation 双重 attach 等程序员错误
属于不变式破坏，可在内部测试和生产中快速失败。来自网络的数据必须先被转换为已验证值，
不能直接触发这些 panic。

### 15.3 玩家存储错误

- not found：正常首次登录，使用出生点；
- corrupt/future：拒绝该玩家登录，不覆盖文件；
- 临时 I/O 错误：返回 `StoreUnavailable`，世界继续运行；
- 在线后的保存错误：保留 dirty、退避重试并在关服时上浮根因。

---

## 16. 并发与资源所有权

- world/tick goroutine：唯一修改 sim 玩家和世界状态；
- accept goroutine：只接受 TCP 并把连接交给有界 pre-login 管理器；
- 每个连接一个 reader、一个 writer；
- session 协调器：一次性收敛 reader/writer/heartbeat 退出；
- 玩家保存 worker：每个玩家最多一个在途任务；
- profile 文件只由客户端启动路径串行读写；
- `server.Host` 拥有 listener、世界 Store 和 session registry；
- endpoint 拥有底层 `net.Conn`，Close 幂等；
- 客户端网络 reader 把已验证消息放入有界队列，渲染线程只做预算化 drain，不阻塞读 socket。

锁只保护 admission、session 生命周期和保存元数据；不得在持锁时执行网络 I/O、磁盘 I/O、
压缩、等待 tick 或等待 goroutine 退出。

---

## 17. 测试策略

### 17.1 协议 golden 与 round-trip

每种双向消息至少有：

- 固定输入对应固定字节的 golden；
- encode → decode 精确相等；
- decode → encode 得到规范字节；
- 边界值和非法值表驱动测试；
- 注册表完整性与 ID 唯一性测试。

协议 golden 是线上格式兼容证据。修改 golden 必须伴随协议版本提升，不能把实现变化直接
当成“更新预期”。

### 17.2 Fuzz

分别 fuzz：

- frame length 与 packet ID；
- 每种 Handshake/Login payload；
- 每种 Play payload；
- `ChunkSnapshot` zstd envelope 与逻辑数据；
- 玩家 profile JSON；
- 玩家 envelope、CRC、schema 和 payload。

断言不 panic、不越界、不无界分配，并在合理时间内返回。语料包含空输入、逐字节截断、
超长 varint、未知 ID、声明长度边界、NaN/Inf 和压缩炸弹。

### 17.3 TCP 生命周期

用 `net.Pipe` 或受控连接覆盖：

- 任意位置短读短写；
- 多帧粘连和单帧拆分；
- context 取消；
- reader、writer、peer 并发 Close；
- Handshake/Login deadline；
- 正确与错误 heartbeat token；
- outbox 背压；
- pre-login 上限；
- 所有 goroutine 在共享 deadline 内退出。

### 17.4 玩家档案与存档

覆盖：

- profile 首次创建、再次读取和显式改名不换 ID；
- 损坏、未来版本、权限错误和原子写入中断；
- 玩家文件 not found、v1 round-trip、fixture、迁移连续性、未来版本和 CRC；
- revision 倒退和迟到完成；
- 自动保存、断线保存、失败退避与关服重试；
- 保存重试期间同 ID 重连优先使用最新内存状态；
- 当前坐标、安全落点和世界出生点三级恢复；
- 恢复区块未 Ready 时玩家不能提前激活。

### 17.5 端无关一致性

同一份脚本分别运行在 Memory 和 TCP 上：

1. 使用同一 `PlayerID` 登录；
2. 等待相同 Ready 边界；
3. 发送相同 sequence 的移动、挖掘、放置和 resync；
4. 在相同 tick 断开；
5. 比较有序消息类型与业务字段；
6. 比较服务端玩家 hash、区块 hash/revision 和客户端镜像 hash。

Handshake/Login/KeepAlive/Disconnect 等协议控制消息先被过滤；时间戳、连接地址、压缩字节
长度和临时 `SessionID` 也不进入业务一致性比较。剩余 Play 业务消息必须保持相同顺序和
字段。

### 17.6 真实纵向测试

在 `127.0.0.1:0` 和临时世界上启动真正 TCP Host，全程无窗口：

- 客户端完成握手登录并接收区块；
- 移动到不同位置并放置方块；
- 第二个客户端收到 `ServerFull`；
- 第一个客户端断开，玩家成功保存；
- 安全关闭并重启 Host；
- 同一 ID 重连后恢复玩家位置；
- 方块、revision、权威 hash 与镜像 hash 保持一致；
- listener、session、存档和 worker 无泄漏。

另做 `mcgod` 子进程 smoke test，验证参数、监听、信号关服和非零保存错误。端口只使用系统
分配值，测试不得依赖固定端口。

---

## 18. 性能与门禁

### 18.1 热路径约束

- TCP 编解码和 zstd 只在 I/O goroutine；
- tick goroutine 只处理已验证的类型值；
- render/main 线程只预算化消费已接收消息；
- 每条连接复用 encoder/decoder 和合理大小缓冲，不为每个小包创建 zstd 实例；
- 大帧缓冲按上限控制，不因对端声明值提前扩容；
- 心跳不进入 sim 命令队列。

### 18.2 新基准

建立以下 headless 基准：

- 每类小型 Play 消息的 encode/decode 与分配；
- 最坏合法 `ChunkSnapshot` 的 encode/zstd/decode；
- TCP loopback 的连续输入吞吐和区块快照吞吐；
- 玩家文件 encode/decode 与原子保存。

基准报告包含 ns/op、B/op、allocs/op、压缩前后字节数和吞吐。初次通过验收的同机结果进入
M3B 基线；后续相同机器退化超过 20% 判红。

### 18.3 既有门禁

M3A 已接受的物理、存档、tick、RSS 和离屏场景阈值不得放宽。M3B 场景版本提升后，需要
同时覆盖 Memory 和真实 TCP；若主机资源压力导致结果不可判定，必须保留原基线和原阈值，
不能用失败运行覆盖基线。

所有性能验证默认完全离屏，不启动或聚焦前台窗口。

---

## 19. CI 与架构门禁

CI 至少执行：

```text
go test ./... -race -count=1
go vet ./...
gofmt -l .
go test ./internal/archcheck ./internal/storage ./internal/network ./internal/physics -count=1
go test ./... -run '^$' -bench=. -benchtime=1x -count=1
CGO_ENABLED=0 GOOS=linux go build ./cmd/mcgod
```

本项目所有 Go 命令继续使用用户已有的 GVM `go1.26.0`，不得下载或安装另一套 Go。

依赖方向新增约束：

- `network` 不 import `server`、`client`、`sim`、`storage` 或渲染包；
- `profile` 不 import `server` 或 `storage`；
- `mcgod` 不 import 图形和客户端包；
- `sim`、`world` 不 import `network`；
- 只有 `network` 的 TCP stream/listener 实现 import `net`；`server.Host` 只依赖监听抽象；
- 只有线上快照 codec 与 storage 各自的 codec import zstd，二者不互相依赖。

---

## 20. M3B 出口条件

以下条件必须同时满足：

- `mcgo` 默认单机路径继续可玩，并通过完整回归；
- `mcgo --connect host:port` 能通过 TCP 登录并真实游玩；
- `mcgod` 无渲染运行，能在 Linux/无 CGO 构建；
- Handshake、Login 和 Play 的状态与包 ID 被版本 1 golden 冻结；
- 版本不匹配、服务器已满、非法身份和玩家损坏都有稳定拒绝；
- 所有网络长度、数量、字符串和解压输出均有硬上限；
- 畸形或恶意连接不会 panic、无界分配或停止世界；
- 同一时刻最多一个 Play 玩家，竞争登录最多一个成功；
- `PlayerID` 跨客户端和服务器重启稳定，昵称修改不换身份；
- 玩家当前位置与安全落点按 ID 原子保存；
- 断线后专用服务端继续运行，同 ID 重连不会回退到旧磁盘状态；
- 当前位置无效时依次回退到安全落点和确定性出生点；
- 玩家文件损坏或未来版本不会被静默覆盖；
- 区块和玩家在关服时都刷净，失败可重试并返回非零错误；
- Memory 与 TCP 脚本得到相同业务消息、玩家 hash、区块 hash 和镜像 hash；
- 真实 TCP + 磁盘重启纵向测试通过；
- session、连接、listener、保存任务和 worker 无泄漏、无 data race；
- 全量测试、race、vet、格式、依赖、benchmark 和 Linux build 门禁通过；
- 整个自动验证过程不启动前台窗口。

M3B 完成后单独设计 M3C：解除单 Play 槽限制，建立多 session 区块与玩家广播、远端玩家
快照/插值、加入离开事件、同 ID 冲突处理和断线清理，最终闭合 M3“存档与联机”。

---

## 21. 已知取舍

| 取舍 | 当前决定 | 原因 |
|---|---|---|
| 身份安全 | 客户端声明的离线 UUID | 局域网目标足够，避免提前引入账号或密钥体系 |
| 在线人数 | 1 | M3C 才实现完整多人同步，避免半成品多人行为 |
| 单机编码 | 类型值直传 | 保持单机低分配；以共享状态机和一致性测试保证语义一致 |
| 协议兼容 | 只接受版本 1 | 首版没有真实旧客户端，提前维护兼容分支没有收益 |
| 压缩 | 仅区块快照 zstd | 小包压缩头和 CPU 成本大于收益 |
| 玩家文件 | 每玩家独立原子文件 | 玩家数量远小于区块，简单事务边界优于区域化 |
| 断线重连 | 不自动重连 | 无连接菜单时自动重连会引入难以解释的输入与状态恢复 |
| 内部 session registry | 显式 SessionID 索引、admission 仍为 1 | 移除固定本地 ID，同时不越界实现 M3C 功能 |
