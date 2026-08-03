## Context

动机见 `proposal.md`。当前 `world.Chunk` 只持有方块区段；`sim.Dimension` 以单写者维护区块及一个同时用于存档和客户端方块镜像的 revision；存储将整块编码为 schema v1；网络协议 v3 没有掉落物消息。成功挖掘目前先调用 `Hotbar.Add`，再写空气。

本变更跨越世界模型、权威 tick、区块存档、协议、兴趣发布、客户端镜像和渲染，必须保持以下边界：`core` 不依赖其他内部包，`world` 不依赖 `network`，`sim` 不依赖客户端或渲染，只有 `gfx` 直接使用 WebGPU。Memory 与 TCP 继续经过同一消息模型和登录状态机。

## Goals / Non-Goals

**Goals:**

- 以固定上限的数据结构完成生成、合并、拾取、过期、同步、呈现和重启恢复。
- 让一次挖掘的方块修改与掉落物修改进入同一个区块 revision 和存储记录。
- 把每 tick 工作限制在 8 名玩家、每人 25 个区块、每区块 32 个堆之内，并复用现有背压和渲染资源模式。
- 对协议 v4 和区块 schema v2 提供严格解码、迁移、golden、fuzz 和故障测试。

**Non-Goals:**

- 不建立通用实体组件系统、空间索引、事件总线或独立掉落物数据库。
- 不插值或预测掉落物；客户端动画不属于权威状态。
- 不支持掉落物移动、跨区块迁移、玩家主动丢弃或离线墙钟过期。

## Decisions

### 1. `core.DropID` 描述位置槽位和 generation

`core` 新增可比较的 `DropID`，字段为 `Dimension`、`Chunk`、`Slot` 和 `Generation`。`Slot` 必须小于 32，`Generation` 必须非零。网络按字段显式编码，不依赖 Go 结构体布局。

`world.Chunk` 新增固定 `[32]DropSlot`。每槽始终保存 generation；活动槽另存 `ItemStack`、区块内 `BlockIndex`、`AgeTicks` 和 `PickupDelayTicks`。空槽再次启用时 generation 加一，首次为 1；generation 已达 `uint32` 上限的槽不再复用。`Clone` 和 `PayloadBytes` 覆盖槽数据。现有 `Chunk.Hash` 保持“仅逻辑方块”含义，避免破坏区块快照校验；另以固定槽顺序提供掉落物状态哈希供存档与集成测试使用。

同物品、同 `BlockIndex` 的最低槽优先合并，否则使用最低空槽。位置由 `BlockIndex` 和区块坐标确定为方块中心，不保存浮点坐标。

否决方案：随机 UUID 会引入随机源和额外 16 字节状态；全局自增 ID 需要新的世界级持久化事务；可变 slice/map 无法天然证明 32 项上限。

### 2. `sim` 单写者直接扫描固定槽

交互处理先在目标区块预检可合并槽或空槽，再在同一 tick 中把方块改为空气并提交掉落物；预检失败时两者都不写。满快捷栏不参与挖掘预检。掉落物拾取继续复用 `Hotbar.Add` 的最低栏位规则，循环添加到栏位满或堆为空，因此不复制快捷栏堆叠逻辑。

每个会话保留现有地形 `wanted`，另计算固定半径 2 的 `dropWanted`；全局区块获取集合取二者并集，地形快照仍只按原 `wanted` 发布。每 tick 将所有 Ready 玩家 `dropWanted` 去重并按 `ChunkKey` 排序，只扫描其中 Ready 区块的 32 个槽。玩家按 `SessionID`、掉落物按 `DropID` 排序；先使用本 tick 完成移动后的权威位置处理已有掉落物，再处理挖掘，因此新堆从后续活动 tick 开始扣减 10 tick 延迟。区块离开全部 `dropWanted` 后其年龄和延迟暂停，值直接保存在槽中。

拾取、过期、合并和创建都把所属区块标为变化。继续复用 `ChunkRecord.Revision`：含方块变化时发布正常 `BlockChanges`，仅掉落物变化时发布允许零条方块的 revision barrier。这样存储 revision 与现有客户端区块 revision 仍保持单调，无需第二套 revision 或事务。

否决方案：最小堆/时间轮会增加运行时索引和加载重建；按整个地形视距扫描会受可配置视距平方放大；第二套掉落物 revision 会复制恢复、存档和客户端同步状态机。固定上限下，每 tick 最多扫描 `8×25×32=6400` 个槽，直接扫描更小且可测。

### 3. 区块 schema v2 固定编码掉落物槽

逻辑区块 v2 在现有 24 个 section 后编码固定 32 个槽：generation、活动标记，以及活动槽的 item/count、`BlockIndex`、年龄和剩余拾取延迟。解码器先检查固定长度和每字段上限，再构造 `world.Chunk`；任何非法槽使整个 payload 失败。v1 DTO 迁移时生成 32 个 generation 为零的空槽，并保留 `NeedsRewrite` 行为。

存储仍只接收 `ChunkSave{Chunk: clone}`。方块和掉落物已位于同一 clone、同一压缩 envelope 和同一次 region 提交中，因此复用现有 revision 冲突、校验、原子写入及重试路径，不新增文件或事务接口。容量估算增加固定槽编码上限。

否决方案：独立实体文件无法与方块写入原子提交；只编码活动槽会多出可变长度和重复/乱序处理，却最多只节省约 32 个小记录。

### 4. 协议 v4 使用两种有界批次

新增：

- `ItemDropUpserts{ServerTick, Drops}`：每项包含完整 `DropID`、`BlockIndex`、item 和 count，数量 `1..32`。
- `ItemDropRemoves{ServerTick, IDs}`：ID 数量 `1..32`。

批次必须按 `DropID` 严格递增且无重复；所有字段在分配切片前验证计数和剩余字节。upsert 对未知 ID 是新增、对已知 ID 是完整替换；remove 未知 ID 属于协议错误。packet ID 追加在现有表尾部。v4 同时允许 `BlockChanges` 使用零项作为 revision barrier；其他字段和 revision 规则不变。v3 在握手阶段拒绝，不做协商。

否决方案：把 800 项放进单包会增大慢连接峰值；为每项发送一包会放大队列压力；将掉落物塞进 `ChunkSnapshot` 会让客户端必须重建与方块快照耦合的镜像，且部分拾取仍需增量协议。

### 5. 服务端以当前快照做有界差分

`sim.Engine` 提供向调用方切片追加单会话当前掉落物快照的只读方法，结果按 ID 排序且最多 800 项。服务端会话维护其已发布掉落物 map 和复用的 scratch slice；每次 `publishSession` 在更新权威视图后取得当前快照，与上次镜像比较，先按 ID 发送 remove，再发送新增或状态变化的 upsert，每批最多 32 项。只有 enqueue 成功才更新已发布 map；失败沿用现有慢客户端关闭流程。

这套差分每 tick、每玩家最多比较 800 项，省去跨包掉落物事件类型、离开范围事件和重同步状态机。断开会话自然释放 map；客户端重连从空镜像接收当前快照。

否决方案：从 `sim.TickResult` 传播完整事件流需要额外处理兴趣进入、遗漏恢复和队列失败；每次无条件发送 800 项浪费带宽。

### 6. 客户端镜像和渲染复用现有模式

`internal/client` 新增独立 `ItemDrops` 镜像，使用预分配容量 800 的 map 和 presentation scratch；应用批次前先完整验证并预检容量，保证失败不部分修改。输出按 ID 排序，断开、重新登录和维度 reset 时清空。

`internal/render` 增加固定 800 instance 的掉落物渲染器，复用现有立方体几何、物品到方块/颜色映射和 `gfx` 缓冲接口，不加载贴图或新资源。`cmd/mcgo` 只负责消息分派、reset 和每帧传入 presentation。浮动/旋转由 `ServerTick` 与 `DropID` 的稳定整数混合得到相位，绝不写回镜像。`cmd/mcgod` 不增加图形依赖。

否决方案：为三种物品引入 atlas 或资源管线不属于本批；每个掉落物独立 GPU 对象会使资源数随镜像变化。

### 7. 验证和性能门禁

每组先写最小失败测试，再实现。重点覆盖：槽复用 generation、容量失败原子性、延迟/过期暂停、多人竞争与部分拾取、区块 v1→v2 migration/golden/fuzz/故障注入、v4 packet golden/fuzz、Memory/TCP 等价、兴趣进入/离开、客户端批次原子性以及 800 项稳定渲染分配。

最终执行受影响包 race 测试、`internal/archcheck`、`go test ./... -race`、`go vet ./...`、`gofmt -l .`、存储/协议 fuzz 或现有短时语料回放，以及 `cmd/perfcheck` 对应门禁。所有命令使用项目已有 GVM Go 1.26，自动测试保持 headless。

## Risks / Trade-offs

- [每 tick 快照差分是 O(玩家数×800)] → 上限固定为 8 名玩家和每人 800 项；加入 allocation/benchmark 门禁，只有实测超限才改为事件索引。
- [零方块 revision barrier 容易被旧验证器误判] → 在协议 v4 同步修改 codec、客户端镜像和 golden，并由 v3 握手拒绝阻止混用。
- [掉落物在无人兴趣范围时暂停寿命] → 将年龄直接持久化并用规格测试锁定；未来若需要离线墙钟过期再单独设计世界时间。
- [generation 最终耗尽会永久损失一个槽] → 需要同一槽超过 42 亿次复用才发生；稳定拒绝而不回绕，避免陈旧 ID 冲突。
- [v2 区块无法由旧程序读取] → 部署前备份世界目录，升级后先跑迁移/golden 和重启恢复验收；不降低旧程序的未来版本拒绝。
- [方块 delta 与掉落物消息是两个有序包] → 同一连接按 server tick 和发送顺序处理，revision barrier 先于掉落物差分；断线重连从权威快照重建，不尝试跨连接补包。

## Migration Plan

1. 先交付世界模型和 schema v2 读写；读取 v1 时只在内存生成空槽，下一次正常保存写为 v2。
2. 同一发布版本切换协议 v4 的服务端与客户端；v3 在进入 Play 前拒绝。
3. 上线前复制世界目录；运行 v1 fixture 迁移、v2 重启恢复、故障注入和完整 headless 门禁。
4. 若上线前验证失败，停止发布并继续使用旧二进制和原目录。若已有 v2 区块写入，回退必须停止服务并恢复升级前备份；不得让旧二进制覆盖 v2 世界。
