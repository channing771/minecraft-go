## Context

M4M 已从客户端接受的权威方块镜像派生传播天空光，并把它写入地形实例光照的高四位；低四位固定为零。M4N 需要在服务端仍只权威拥有方块与物品、Memory/TCP 共用同一模拟路径、mesher 固定资源上限不放宽的条件下补齐静态方块光。动机与可观察契约见 `proposal.md` 和本 change 的七份 delta spec。

## Goals / Non-Goals

**Goals:**

- 以末尾追加的稳定 ID 提供一个完整不透明、固定发光等级的方块与物品，并复用普通放置、采掘、掉落和持久化路径。
- 在每个 mesher worker 的固定工作内存中先派生天空光，再执行多源方块光传播，保持已有 dirty、过期结果拒绝和零稳定态分配边界。
- 只升级合法语义集合和格式版本，不改变 packet、payload、玩家或 metadata 布局。
- 用 headless shader、视觉、Memory/TCP、golden/fuzz 和 scenario v15 门禁锁定纵向闭环。

**Non-Goals:**

- 不设计真实火把、透明或半透明透光、彩色或可调光源、燃烧熔炉等动态光。
- 不增加配方、初始发放、世界生成、管理命令、新 packet、服务端光照状态、独立光照存档或第二套单机逻辑。
- 不新增 pipeline、bind group、uniform、纹理文件、draw call、goroutine 或动态传播容器。

## Decisions

### 1. 稳定资源追加并复用既有权威玩法

`internal/core` 精确追加 `LightBlockID = ChestID + 1` 与 `ItemLightBlock = ItemChest + 1`；不重排既有编号。`ItemLightBlock` 单格上限为 `64`，放置映射到 `LightBlockID`，`BlockDrop` 反向映射；`RecipeChest` 继续是最后一个 recipe ID，六条配方不变。`internal/assets` 为完整不透明发光块提供独立程序化材质，并固定 `Emission(LightBlockID) = 15`，其他已知或未知方块返回 `0`。

放置复用普通整格方块的权威射线、Ready、目标为空、玩家 AABB、sequence 与原子扣减规则。采掘把发光块加入石砖、熔炉、箱子同档：无正确镐 `30` tick 且无掉落，石镐 `15` tick、铁镐 `8` tick，正确镐掉落一个发光块物品。这样 Memory 与 TCP 自然共用同一模拟和校验逻辑。

否决为发光块增加专用放置、采掘或配方系统：既有共享路径已覆盖全部需要的状态转换，新增分支只会制造语义漂移。

### 2. 一个 packed scratch 顺序构建两条光照通道

`SkyLightScratch` 更名为 `LightScratch`。每个 mesher worker 独占并跨任务复用一个 `48³` 的 `uint8` `levels` 和一个 `48³` 的 `uint32` FIFO `queue`，固定 head/tail；不得增加第二份数组或动态容器。`levels` 高四位保存天空光、低四位保存方块光。

构建顺序固定为：清零 `levels` 并先按现有规则完成天空光；只清空队列索引；扫描同一 `48³` 邻域，把所有光源同时设为低四位 `15` 并全部入队；最后执行六向多源方块光 BFS。传播只进入 `AirID` 邻格，每格衰减 `1`；任何其他方块即使未来被标记为透明也阻断方块光，缺失邻区按非空气且无发光处理，多源取最大值。所有等级 `15` 的源在传播前统一入队，保证 FIFO 首次到达是最短路径并维持单队列容量证明。`MeshSection` 采样可见面相邻空气后原样写出 packed byte。

否决独立 `BlockLightScratch`：它会复制 `48³` levels、queue 和邻域扫描，而高低四位本来就是同一个实例字段。也否决服务端权威光照数组：静态光完全由权威方块确定，再保存或传输会引入可失真的第二份状态与新协议负载。

### 3. 复用既有失效、调度和并发边界

普通 dirty 继续 `<= 27` 且必须完整覆盖所有实际受影响区段，列顶 dirty 继续 `<= 216`；方块光等级 `15` 的传播半径不超过 `14` 格，既有普通集合应覆盖其影响范围，测试同时验证上限与覆盖完整性。区块加载或遗忘继续失效相邻区段，不另建方块光集合。worker 完成时若镜像 revision、generation 或 presence 已变化，结果必须拒绝并重新排队。

服务端 tick、网络与存档不执行光照工作。跨 goroutine 发送后的 neighborhood 与 mesher 结果继续不可变；每个 worker 只操作自己的 scratch，因此不增加锁或 goroutine。

### 4. shader 只解码低四位并按最大值合光

terrain shader 精确使用 `max(0.08 + sky*(daylight-0.08), block)`，其中 `sky = high_nibble/15`、`block = low_nibble/15`；随后继续乘既有面朝向与 AO。方块光不受昼夜影响，天空光继续使用权威世界时间，完全黑暗处仍保留 `0.08`。

否决新的 uniform 或光照 pass：packed 实例字段和现有 fragment 阶段已经携带全部输入，增加渲染资源不会产生新的可观察能力。

### 5. 协议与存档只做语义版本门禁

线上协议升级为 v14，区块 schema 升级为 v7；packet ID、payload 长度和字段布局不变。迁移 registry 增加 v6→v7 no-op，后续保存写 v7。玩家 schema 保持 v5，metadata 保持 v2；旧程序遇到含 `ItemLightBlock` 的玩家 v5 记录沿用整体拒绝且不覆盖的边界。线上与磁盘都不保存光照数组。

### 6. workload 与证据升级为 scenario v15

固定 `48³` 光源扫描与有光源时的 BFS 改变生产 mesher workload，因此 benchmark 使用 scenario v15。当前 Apple M2 上生成完整 Memory v15 报告后，先以报告自比较完成同场景完整性、硬件身份和绝对数据门禁，再把精确字节提升到既有 M2 基线路径；原 M2 v6 只保留提交与哈希历史证据。TCP v15 独立记录，仅显式请求时跨 transport 比较。M5 v14 基线字节不变并等待未来在相同 M5 硬件上使用唯一迁移 `14:15`；不得增加 `6:15` 或跨硬件例外。性能数值继续 record-only，报告结构、身份、真实 overflow、数据丢失和 I/O 错误仍是门禁。

无窗口场景列表末尾追加 `block-light-room`：午夜封闭房间内只有一个发光块，经镜像、dirty、mesher 和 upload 收敛后抓取；房外漏光、未收敛或既有双阈值超限均失败。

## Risks / Trade-offs

- 固定扫描增加每次网格构建 CPU 工作 → scenario 升为 v15 并在当前 M2 记录 Memory/TCP，M5 v14 等待未来同硬件迁移；稳定构建仍须证明零分配。
- 多源传播若重复提升可能超过单队列容量 → 所有满亮源先统一入队，以 exact-capacity、最坏多源与 panic 边界测试证明容量不变式，不放宽 overflow 门禁。
- 客户端 v14 与旧世界程序不兼容 → 握手在 Play 前拒绝，区块使用显式 v6→v7 迁移，升级前要求正常停服并备份完整世界。
- 玩家 schema v5 未升版会让旧程序无法识别新物品 → 旧程序必须整体拒绝且不得覆盖；回退只允许恢复备份，不提供降级写回。
- dirty 上限复用若覆盖不足会残留旧光 → 普通变化同时断言 `<=27` 与实际受影响区段完整覆盖，列顶变化断言 `<=216`，并用快速放拆、邻区到达和 stale result 测试锁定收敛。
- 程序化视觉可能显示出边界漏光或不合理衰减 → `block-light-room` 使用既有阈值并人工复核唯一新增 golden，不通过放宽阈值掩盖问题。

## Migration Plan

1. 在实现前冻结新增 ID、协议 v14、区块 v7、玩家 v5、metadata v2 和 scenario v15 的测试。
2. 先交付资源与 v6→v7 迁移，再接入 packed 双通道、客户端收敛、shader、纵向和视觉验证。
3. 在当前 M2 生成完整 Memory v15 报告，自比较通过后精确提升既有 M2 基线路径；再独立生成并自比较 TCP v15，保留 M2 v6 历史身份与未改动的 M5 v14 基线。
4. 发布前正常关服并备份完整世界目录；客户端与服务端同时升级到协议 v14。
5. 回退时先停服，恢复升级前完整备份，再运行协议 v13、区块 v6 程序；不得让旧程序打开已写成 v7 或含新物品的世界后继续写入。

## Verification

- 资源与玩法：稳定 ID、无第七配方、放置/掉落、三档采掘、掉落容量和 Memory/TCP 最终状态一致。
- 光照与并发：`15/14/1/0`、仅 `AirID` 传播、任一非空气方块阻断、多源最大值、跨区段/区块、缺失邻区、一个 `48³` levels/queue、零分配、普通 dirty `<=27` 且完整覆盖、列顶 `<=216`、过期结果拒绝。
- 兼容与存档：v14 握手与 v13 拒绝、packet layout、v6→v7 no-op、v6/v7 golden/fuzz、玩家 v5、metadata v2、CRC/future/truncation。
- 渲染与证据：headless shader 合光、`block-light-room`、scenario v15 producer、`14:15` 唯一迁移、M2 Memory v15 精确基线、M5 v14 字节保真、TCP 独立记录及全仓 race/vet/archcheck/OpenSpec strict 门禁。

## Affected Files

- `internal/core`、`internal/assets`、`internal/sim`、`internal/server`：稳定资源、放置、采掘与纵向闭环。
- `internal/mesh`、`internal/client`、`internal/render`、`cmd/mcgo`：packed 派生、失效、shader、capture 与 benchmark producer。
- `internal/network`、`internal/storage`：协议 v14、区块 schema v7、golden/fuzz 与玩家 v5 保真。
- `cmd/perfcheck`、`docs/notes`、README、LAN 文档和项目现状文件：scenario v15、基线记录、兼容与已交付能力。
