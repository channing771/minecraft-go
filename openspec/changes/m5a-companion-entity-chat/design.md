## Context

见 [proposal.md](proposal.md) 的动机。当前 M4Q 只有玩家 session 驱动的权威 actor、协议 v15、玩家/区块/metadata 存档和七名远端玩家容量的 Avatar/NameTag 上传布局。M5A 必须同时改变配置、协议、存档、模拟、服务端发布、客户端镜像、渲染、HUD、视觉与 benchmark 契约，但不能提前引入 M5B–D 的 Planner、FIFO、路径或世界动作。

现有架构要求服务端为唯一世界权威，Memory/TCP 共用登录和模拟，`sim` 不依赖渲染，`world` 不依赖网络，只有 `internal/gfx` 直接接触 WebGPU；重 CPU、磁盘和网络 I/O 不得阻塞 20 Hz tick。跨 goroutine 发送后的消息切片视为不可变。

## Goals / Non-Goals

**Goals:**

- 以独立 16-byte `CompanionID` 和 config v1 的 `ai.companions[].id/name` 定义最多四个具名伙伴。
- 交付静态 idle 权威身体、固定 `3×3` 兴趣、独立 `8 players + 4 companions` 容量和确定性发布。
- 交付协议 v16 的伙伴同步及 `@名称 指令` 确定性寻址，Memory/TCP 语义一致。
- 交付 `companions.ai` schema v1、64 条 active+inactive 保留、异步聚合保存与可重试安全关服。
- 交付只读客户端镜像、统一 Avatar/NameTag、固定聊天 HUD、无窗口 `ai-companion` 和诚实的 scenario v16 记录。

**Non-Goals:**

- 不实现 Planner、HTTP/SDK、persona、摘要、FIFO、任务恢复、`go_to`、`follow`、`mine`、`place`、停止旁路或任意工具执行。
- 不把伙伴建成虚拟玩家，不抽取通用 `actorState`，不创建第二套 renderer/shader/resource。
- 不改变玩家 schema v6、区块 schema v8、世界 metadata v2、最多八名玩家、TCP 信任模型或既有 M2 v15/M5 v14 基线字节。

## Decisions

### 1. 独立 companion 领域值，复用现有基础解析

新增 `internal/companion`，只放 `ID`、`Definition`、`Body`、固定容量和验证。`ID` 为 `[16]byte`，解析复用 `core.ParsePlayerID` 的 UUIDv4/canonical 规则后显式转换，不复制 UUID parser；类型仍独立，禁止转成玩家或 session 身份。名称先经既有 display-name canonical 校验，再要求输出与输入完全相同并逐 rune 拒绝 `unicode.IsSpace`。

配置 schema 保持 v1。`ai` 缺失、`null` 或 `companions` 为空即关闭；M5A 只解析 `id/name`。AI 直属和条目内未知字段沿用精确路径告警，未来 endpoint/model/key/timeout/persona 不成为启动条件。配置定义使用 `slices.Clone` 交给内置/专用服务端；远程客户端、benchmark 和 capture 不注入真实配置。

否决把伙伴复用为 `PlayerID`：这会污染登录、心跳、玩家容量、死亡和存档语义。否决新建 UUID/clone 工具：标准库与既有 parser 已覆盖。

### 2. M5A 只增加最小静态 companionState

`internal/sim` 使用按 `CompanionID` 索引的独立 `companionState`，只包含维度、`physics.State`、朝向、36 格背包、恢复/出生状态。伙伴不接玩家 command、移动、采掘、生命或 session。注册最多四个，重复 ID 直接视为编程错误；`TickResult.Companions` 和 `CompanionBodies` 按 ID 排序。

恢复复用现有碰撞、出生候选和 revision-retry 私有逻辑。候选、pending 与 active 兴趣都限制在一格区块半径：脚下区块周围 `dx/dz=-1..1`。伙伴兴趣直接并入 subscription union，不伪造 SessionID；距离优先级按最近 active 位置或恢复/出生 anchor 计算。

否决现在抽 `actorState`：M5A 没有共享移动/动作调用者；等 M5B 首次移动时再抽真实共有字段。

### 3. 协议 v16 只追加固定 ID，codec 先验有界

`ProtocolVersion=16`，不提供 v15 兼容登录。保留所有 v15 ID；追加 Client `ChatCommand=12`，Server `ChatEvent=16`、`CompanionSpawn=17`、`CompanionStates=18`、`CompanionDespawn=19`。最大 wire 长度分别锁定为 ChatCommand 1026、ChatEvent 1328、Spawn 178、States 173 bytes。

所有 decoder 先读入局部值，在分配前检查字符串长度和 States `1..4` count，完整执行 UTF-8、枚举、ID、数值、排序和重复校验后才返回消息。`CompanionStates` 必须 ID 严格升序。Memory/TCP 继续共用 registry 和 codec；伙伴不得走 `RemotePlayerSpawn`。

`ChatCommand` 只承载 `1..1024` bytes 规范文本。`ChatEvent` 只有 Accepted/Rejected 事实：Accepted 携带玩家、伙伴和 trim 后命令；InvalidFormat 清空伙伴/命令；UnknownCompanion 只保留合法目标名称。

### 4. 聊天在 tick 边界寻址，不进入 sim

endpoint reader 在既有 client message 翻译前识别 `ChatCommand`，放入与现有输入同容量的独立 bounded channel，并携带 session generation。`Server.step` 在 `engine.Step` 前按 channel 顺序 drain，丢弃 stale generation，解析第一个 Unicode whitespace 分隔的 `@名称 指令`，使用配置 map 做大小写精确匹配，并递增进程内 event ID。

Accepted 广播全部 active sessions；InvalidFormat/UnknownCompanion 只回复发令者。处理只产生 delivery，不构造 `sim.Command`、不修改伙伴身体、不保存聊天文本。这样 Memory/TCP 的差异只在 transport，权威顺序相同。

否决通用玩家聊天和 FIFO：M5A 只需证明具名寻址；保存或执行文本会提前承诺 M5B 任务语义。

### 5. MCAI v1 是单文件固定记录格式

`companions.ai` 的 header 固定 32 bytes：

```text
0..3   magic "MCAI"
4..7   envelope uint32 = 1
8..11  schema uint32 = 1
12..19 aggregate revision uint64 > 0
20..23 count uint32 <= 64
24..27 payloadBytes uint32 == count * 221
28..31 CRC32C(header[8:28] + payload)
```

每条 221-byte record 为 ID16、dimension4、position12、yaw4、pitch4、selected1 和 36 个 `(item uint16, count uint8, durability uint16)`。编码 clone 后按 ID 排序，不修改调用者切片；解码在分配前检查 `count<=64`、总长最多 14,176、精确 payload、CRC、严格 ID 顺序、有限浮点、维度、pitch 和 inventory。future schema 返回 future-version error，其他结构错误包装 corrupt error。名称和任何任务文本不入 v1；M5B 增加任务时必须升 v2。

`MemoryStore` 与 `DiskStore` 实现相同 `CompanionStore` revision/idempotency/conflict 语义。Disk 固定根文件名，读取用 `max+1` limit，保存先校验正式文件，再复用 temp write、file Sync、Rename、parent Sync 原子替换；正式文件损坏/future/超大时不覆写。既有 backup 已复制正规根文件并跳过 temp，只增加契约测试。

### 6. NewHost 负责同步加载边界，Server 负责运行期保存

`NewHost(ctx, config, generator, store) (*Host, error)` 只在任何 worker 启动前用传入 ctx 同步验证配置、加载并合并 `companions.ai`。配置为空时不调用 CompanionStore；非空时 missing 视为空，corrupt/future/64 条并集溢出返回 error。已配置已存 ID 恢复，新 ID 从 metadata spawn anchor 创建空背包，未配置旧 ID 留作 inactive。

`NewHost` 失败不关闭 caller-owned store。应用 caller 关闭自己已打开的 store；专用服务端 caller 还关闭自己已打开的 listener，避免 constructor 内隐所有权和 double-close。旧 `NewWorld` 只供 AI-disabled benchmark/test；若传入非空伙伴配置则 panic 并指向 `NewHost`。

单个 `companionPersistence` worker 使用 capacity-1 jobs/completions channel 和内部 `context.WithCancel(context.Background())`；不继承 constructor/signal ctx。tick 在 `engine.Step` 后持 `stepMu` 时只 Observe clone 和非阻塞 Poll，store I/O 始终在 mutex 与 tick 外。任一时刻最多一份 in-flight，job/retry 各持独立 frozen clone；保存 N 完成不能清除 N 期间出现的 newer dirty。

失败完成保留 dirty 和同 revision retry；后续 autosave/retry tick 再提交。Flush 先收割 in-flight：失败原样返回并可重试；成功但 latest 已变化时只再写最新 snapshot 一次。shutdown 最后 drain+step 后 Observe，再按 companion save、store Sync、store Close 排序；Flush 失败不得关闭 worker/store，第二次 Shutdown 可重试。

否决每伙伴一个文件/worker和通用 worker 抽象：单文件与最多四个 active/64 stored 的上限使一个聚合 worker 更小且能原子保留 inactive。

### 7. 发布先验证整批，并遵守 snapshot 顺序

每个 session 新增独立 `visibleCompanions`。发布先为整批 sim update 查 definition 并计算脚下 chunk；任一未知 definition 立即按既有 publication failure 关闭 session，在 enqueue 或 visibility mutation 前失败。

顺序固定为：远端玩家 despawn、伙伴 despawn、ForgetChunks、本 tick snapshot/delta、伙伴 spawn、一个已可见伙伴的排序 states batch，再保持其余既有发布顺序。首次可见必须在脚下 snapshot 已发送后 Spawn，新 Spawn 从下一 tick 才进入 States。玩家与伙伴可见集、容量和 16-byte key domain 独立。

否决通用 entity publisher：玩家和伙伴 wire/生命周期仍不同，只抽脚下 chunk 计算 helper。

### 8. 客户端镜像复用私有 remoteActor 和统一 renderer

从 `remotePlayer` 仅抽未导出的插值 `remoteActor`，玩家和伙伴各自组合；不新增公共 actor 包。伙伴 map 最多四项，每个 States 批次先验证全部 ID、tick、存在性和排序再推入 snapshot。ChatEvent 使用固定 `[32]` ring，EventID 严格递增。真实 frame 与 interactive loop 都与玩家同帧、恰好一次推进伙伴；断线/协议错误 Reset 伙伴、事件、格式化缓存和未发送输入。

Avatar 与 NameTag 引入含 `EntityKind+[16]byte` 的 key，保证相同 bytes 的玩家/伙伴/目标不冲突；玩家 FNV 配色保持旧向量，伙伴颜色加内部 domain tag。只扩现有 renderer：Avatar 上限 11、66 parts、instance 5280、indirect offset 5536、upload 5556；NameTag 上限 12、background 768、glyph offset 1024、glyph 24576、upload 25600。overflow 必须在排序、GPU write 或 atlas mutation 前返回 error。

否决第二套伙伴 renderer：相同人形和名牌语义应共享一个 pass、shader 与 resource。

### 9. 聊天输入复用同一 HUD pass

窗口 char callback 写固定 `[1024]rune` 队列并设置 sticky overflow；closed chat 每帧仍 drain 丢弃，避免残留。app 的 `chatInput` 跟踪 rune 数、UTF-8 bytes 和 sticky overflow：Enter 先 trim 后仅发送有效非空输入，Esc 重置，Backspace 删除一个 rune但不清除 overflow。打开 chat 时释放 cursor、发送中性移动并抑制游戏动作；关闭后捕获 cursor 并立即重置 mouse baseline。

Chat HUD 显示最近六条事实行和一条输入，每行最多 32 rune，末位 `…`；只在 EventID 变化时更新 app-owned 字符串缓存。渲染继续使用既有 Hotbar pass，聊天位于 inventory-confirmation 早退之外；未确认 inventory 不画伪造背包但聊天可见。固定增加 2 quads 和 448 glyph，最终上限 236/700，glyph offset 11776，总容量 45376；空聊天 benchmark 帧实际写入 11776 bytes，不新增 pass 或稳态分配。

### 10. 视觉末场景和 scenario v16 都只表达真实变化

capture 保持全部旧顺序并在 `oak-grove` 后追加 `ai-companion`；旧测试按 Name 查找，不再依赖 last/index。fixture 在装入固定伙伴、中文名牌、accepted 事件与打开输入前清空所有会影响画面的共享客户端状态。先生成候选并人工查看；确认后只允许新增 `ai-companion.png`，两次全场景 visual-check 都沿用现有双阈值，不增加逐字节门禁。

scenario 升到 v16 的唯一 workload 变化是 Avatar/NameTag/Hotbar HUD 固定上传布局；固定输入仍是七名远端玩家、零伙伴、空聊天，分辨率、时长、运动、采样、指标和阈值不变。perfcheck 当前唯一迁移为 `15:16`；v6..v15 保持同版本读取/比较，历史 `14:15` 不再授权新迁移，不接受 `14:16`。Memory/TCP v16 分别自比较，显式跨 transport 只接受同 scenario、同 commit、同硬件。性能数值只记录；完整性、身份、真实 overflow、数据丢失和 I/O 仍失败。M2 v15 与 M5 v14 基线文件不修改。

## Data Ownership and Concurrency

- 配置定义在启动时验证并 clone；此后作为不可变值在 server、sim 和 publication 使用。
- 权威 tick 是 active 伙伴身体和可见性推进的唯一写者；客户端只持服务端消息派生的镜像。
- endpoint reader 只写 bounded chat ingress；server tick 按 channel 顺序消费。跨 goroutine 的消息和值发送后不可修改。
- persistence worker 只处理 frozen bodies；所有 store 调用在 tick 与 worker mutex 外。NewHost ctx 只约束同步 Load，worker 生命周期独立。
- renderer/HUD 热路径只使用固定容量 slice/array/buffer；任何 overflow 在可观察副作用前失败。

## Affected Areas

- 新增：`internal/companion`，network companion messages，storage companion codec/store，sim companion state，server persistence/publication/chat，client companions/chat，HUD chat，capture fixture。
- 修改：`internal/config`、`internal/network` login/registry/codec、`internal/storage` WorldStore、`internal/sim` engine/subscription、`internal/server` Host/session/publication/shutdown、`internal/client` receiver/interpolation/window、`internal/render` Avatar/NameTag/HUD、`cmd/mornlea` app/capture/benchmark、`cmd/mornlea-server` startup、`cmd/perfcheck` migration、`internal/archcheck`。
- 架构白名单只登记实现真实需要的单向依赖，不预授权后续 AI 层。

## Risks / Trade-offs

- [配置清单误删伙伴] → inactive 记录计入 64 条并永久保留，除非后续显式迁移工具处理；清空配置不触碰文件。
- [损坏文件被新状态覆盖] → Load/Save 都先做长度、版本与 CRC 校验；corrupt/future 直接失败。
- [慢磁盘阻塞 tick 或关服丢最后状态] → 单 in-flight worker、frozen retry、step 后 Observe、Flush 后再 Sync/Close。
- [实体在区块前出现或批次部分应用] → 服务端 snapshot-before-spawn、整批 definition preflight；客户端整批先验再 mutation。
- [固定 GPU 容量静默截断] → Avatar/NameTag/HUD 在写入或 atlas mutation 前返回 overflow，并以边界测试锁定。
- [Unicode 输入截断成另一条有效命令] → 以 UTF-8 bytes 计数，overflow sticky，永不发送截断前缀。
- [scenario 升级掩盖无关 workload 变化] → v16 只允许三套固定上传布局变化，其他输入与口径由测试逐项锁定。

## Migration Plan

1. 先实现 config/ID、协议 v16 和 schema v1 codec/store；此时 AI 配置缺失仍保持关闭。
2. 加入 static sim、NewHost merge/persistence、发布和聊天 tick ingress；v15 客户端明确拒绝，部署时服务端与客户端必须同时升级。
3. 加入客户端镜像、统一 renderer/HUD 与无窗口 fixture；仅在人工确认后新增唯一 golden。
4. 把 benchmark producer/perfcheck 升到 v16 和唯一 `15:16`，生成全新临时 Memory/TCP record-only 报告；不覆写 M2 v15/M5 v14 基线。
5. 回退时安全关服并清空 `ai.companions`；旧程序按未知 `ai` 组告警忽略，`companions.ai` 原样保留。若回退到协议 v15，客户端与服务端必须一起回退。未来程序遇到 v1 之后 schema 必须拒绝，不得降级写回。

## Verification

- ID/config、协议 golden/fuzz、MCAI golden/fuzz/故障注入、Memory/Disk contract、sim 3×3/8+4、persistence race/retry/shutdown、Memory/TCP parity、客户端 atomic mirror/allocation、renderer/HUD overflow/allocation 均有 focused race 测试。
- `ai-companion` 候选必须人工确认 SHA-256，随后两次全场景 `make visual-check`；只新增一张 golden。
- 运行受影响包 race、archcheck、相关微基准、`go test ./... -race`、`go vet ./...`、`gofmt -l .` 和 OpenSpec strict。微基准与 scenario v16 性能数值只记录。
