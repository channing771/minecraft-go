# minecraft-go M3A 世界存档持久化设计

- 日期：2026-07-28
- 状态：已评审通过（待实施）
- 上位设计：`docs/superpowers/specs/2026-07-26-minecraft-go-design.md`
- 前置阶段：`docs/superpowers/specs/2026-07-27-m2b-authoritative-player-movement-design.md`
- 范围：完整区块存档、区域文件、格式迁移、崩溃恢复、自动保存与安全关服

---

## 1. 目标与边界

M3A 是 M3“存档与联机”的第一个子项目。它把 M2B 进程内的临时世界改造成第一个可恢复、可迁移、可跨重启继续游玩的持久世界：

1. `cmd/mcgo` 默认打开 `worlds/default`，正常关服再启动后，已探索地形和方块修改保持不变。
2. 首次生成的完整区块会进入存档；以后即使地形生成器改变，已探索区域也不会重新生成。
3. 32×32 区块写入一个区域文件，避免海量小文件。
4. 区块与世界元数据都有显式格式版本；旧格式经连续迁移链读入。
5. 区域文件提交过程中任意异常退出，都只能留下旧提交或新提交，不能破坏先前有效数据。
6. 加载、压缩和磁盘 I/O 不进入权威 tick goroutine。
7. 自动保存失败时继续运行、保留脏状态并退避重试；关服必须向调用方返回真实保存结果。
8. 测试和性能场景使用相同语义的内存存储，不接触真实世界目录。

### 1.1 本阶段明确包含

- 世界目录创建与独占打开
- 世界元数据的版本化编码、校验和原子替换
- 按维度与区域坐标组织的 32×32 区域文件
- 双索引头、copy-on-write 扇区提交与空间整理
- 完整区块的显式二进制编码、zstd 压缩与有界解码
- 区块格式迁移函数链与固定迁移夹具
- 先异步加载、未命中才生成的区块获取流程
- 首次生成、方块修改、卸载、定时自动保存和关服刷盘
- revision 驱动的脏状态与过期保存确认防护
- 磁盘失败重试、未保存内存上限和加载背压
- 默认持久单机世界与显式内存模式
- 存档 roundtrip、故障注入、重启一致性和性能门禁

### 1.2 本阶段明确不包含

- 玩家位置、速度、视角、物品栏或身份数据的存档
- 玩家 UUID、账户、登录或认证
- TCP、二进制联网协议或多玩家会话
- 跨区域或全世界的原子快照
- 云同步、加密、存档选择 UI、手动备份管理或恢复 UI
- M4 的光照、实体、方块实体、生物群系、地物与方块 tick 数据
- 多进程共享写入同一个世界
- 在损坏区块上静默重新生成并覆盖旧数据

玩家数据依赖 M3B 的稳定身份与登录状态机，不能在 M3A 用固定本地 session ID 制造临时格式。M3A 的区块 payload 留有版本化扩展点，但不为空想中的 M4 字段写空容器。

---

## 2. 方案选择

### 2.1 选定：双索引头 COW 区域文件

每个区域文件保存 32×32 个完整区块。更新区块时先把新 payload 写入当前有效索引未引用的扇区并同步，再把新索引写入非活动索引头并同步。两个索引头都有世代号和校验和，启动时选择世代最高的有效头。

这同时提供：

- 单区块或小批次增量写，不必复制整个区域；
- 写入中断时回退到先前索引；
- 旧扇区只有在新索引提交后才可回收；
- 后台整理可用临时文件加原子替换完成。

### 2.2 否决：每批重写整个区域

完整临时文件加原子 `rename` 的恢复模型最简单，但修改一个区块也要复制区域内全部有效 payload。随着 32 区块视距和自动保存运行，写放大会成为持续 I/O 成本，因此只把此技术用于低频区域整理和小型元数据文件。

### 2.3 否决：嵌入式数据库

SQLite、Badger 等能提供成熟事务，但会引入与项目用途不成比例的依赖、缓存与文件格式，并放弃上位设计已经确定的区域文件。M3A 的事务边界固定且狭窄，定制格式更容易做严格的有界解码与性能验证。

### 2.4 否决：只保存稀疏 overlay

M2A/M2B 的 overlay 只保存相对确定性基础地形的修改。若继续把它当正式存档，生成器升级会改变已探索但未修改的地形；长期保留所有历史生成器又会把代码维护变成存档兼容负担。M3A 保存完整区块，并删除 overlay 作为持久化机制的职责。

---

## 3. 包边界与依赖方向

新增 `internal/storage`。它只依赖：

```text
storage → core, world
```

它不得依赖 `server`、`sim`、`network`、`client` 或 `render`。`storage` 负责磁盘格式和持久值，不知道 session、tick、订阅或消息。

现有依赖方向扩展为：

```text
server → sim, storage, network, worldgen
sim     → core, world, physics
storage → core, world
```

依赖方向门禁必须加入 `storage` 规则。

### 3.1 存储接口

磁盘与内存实现共享同一语义接口。下列代码表达边界，不要求实施时逐字采用名称：

```go
type Metadata struct {
    FormatVersion uint32
    Seed          int64
    SpawnDimension core.DimensionID
    SpawnAnchor   core.ChunkPos
}

type StoredChunk struct {
    Key               core.ChunkKey
    Revision          uint64
    PersistedRevision uint64
    NeedsRewrite      bool
    Recovered         bool
    Chunk             *world.Chunk
}

type ChunkSave struct {
    Key      core.ChunkKey
    Revision uint64
    Chunk    *world.Chunk
}

type Store interface {
    Metadata() Metadata
    LoadChunk(context.Context, core.ChunkKey) (StoredChunk, error)
    SaveBatch(context.Context, []ChunkSave) (SaveResult, error)
    Sync(context.Context) error
    Close() error
}
```

`LoadChunk` 的未命中使用可被 `errors.Is` 识别的 `ErrChunkNotFound`。锁冲突、损坏、未来格式、容量上限、权限和 I/O 错误使用稳定的分类错误并保留底层根因。

接口传入与返回的 `world.Chunk` 都是独立所有权的深拷贝，不与存储缓存或权威世界共享可变切片。`SaveBatch` 的原子性以单个区域文件的一次索引提交为边界；跨区域批次允许部分区域已提交、部分仍是旧版本，不承诺全世界事务。

### 3.2 服务端与模拟职责

- `sim` 继续独占权威 `world.Chunk`，跟踪当前 revision、已持久 revision 与卸载意图，但不调用存储。
- `server` 把区块请求翻译成 load-or-generate 工作，把不可变快照交给保存 worker，并把加载、生成和保存完成结果送回 tick。
- `storage` 对每个区域串行提交；不同区域可并行。
- `worldgen` 只在存储明确返回 `ErrChunkNotFound` 时运行。

M2A/M2B 的 `Dimension.overlays`、`BaseBlockLookup` 和“卸载后靠 overlay 恢复修改”的职责在 M3A 移除。完整存档成为卸载后恢复世界状态的唯一来源。

---

## 4. 世界目录与元数据

### 4.1 目录布局

```text
worlds/default/
├── world.lock
├── world.meta
└── dimensions/
    └── 0/
        └── regions/
            └── r.<region-x>.<region-z>.region
```

维度目录使用稳定的十进制 `DimensionID`；v1 只有 `0`（Overworld），但路径和 API 不烧入单维度假设。区域文件名中的坐标使用带符号十进制。

`worlds/` 加入仓库 `.gitignore`。测试必须使用 `t.TempDir()` 或内存 Store，不能在仓库里留下世界数据。

### 4.2 独占世界锁

打开磁盘世界时先取得 `world.lock` 的非阻塞操作系统级独占锁，并持有到 Store 成功关闭。第二个进程打开同一路径立即返回 `ErrWorldLocked`，不得等待，也不得退化为无锁模式。

进程异常退出后操作系统自动释放锁；不能只依赖容易残留的“创建锁文件”协议。支持平台通过小型平台适配文件实现相同语义，具体系统调用在实施计划的技术验证任务中锁定。

### 4.3 `world.meta`

当前元数据只保存：

- magic 与元数据格式版本；
- 世界种子；
- 出生维度；
- 出生锚点区块；
- payload 长度与 CRC32C。

不保存当前尚不存在的世界时间或玩家数据。整数固定使用小端编码，不能序列化 Go struct 内存布局。

新世界创建流程：

1. 创建世界目录并取得独占锁。
2. 用调用方提供的种子和出生配置编码元数据。
3. 在同目录创建唯一临时文件，完整写入并同步。
4. 原子改名为 `world.meta`。
5. 同步父目录。

打开已有世界时，元数据中的种子和出生配置是权威值；命令行的新世界种子不得覆盖它。元数据损坏或版本过新时打开失败，不猜测默认值。

元数据更新沿用“同目录临时文件 → 文件同步 → 原子替换 → 目录同步”。临时文件必须使用唯一名称并以排他方式创建；成功打开时可以清理本世界目录下由本格式明确命名的过期临时文件，不能用宽泛 glob 删除未知文件。

### 4.4 单机与内存模式

`cmd/mcgo` 默认世界路径为 `worlds/default`，并提供显式 `--world <path>`。新世界使用当前默认种子；已有世界忽略新世界种子并使用 `world.meta`。

服务端测试、fuzz、微基准和离屏性能场景通过配置注入内存 Store。内存实现保留深拷贝、revision 单调性、加载未命中和保存确认等语义，但不执行磁盘 I/O 或压缩；这既避免污染磁盘，也让现有性能场景继续完全离屏、不会启动前台窗口。

---

## 5. 坐标映射

区域边长固定为 32 区块。负坐标必须使用向负无穷方向的 floor division，不能依赖 Go 整数除法向零截断。

对区块 `(cx, cz)`：

```text
rx = floorDiv(cx, 32)
rz = floorDiv(cz, 32)
lx = cx - rx*32  // 0..31
lz = cz - rz*32  // 0..31
slot = lz*32 + lx
```

固定测试至少覆盖 `-33, -32, -31, -1, 0, 1, 31, 32, 33`。索引槽顺序是格式契约，未来不得随 map 遍历或实现重构改变。

---

## 6. 区域文件格式

### 6.1 固定布局

区域文件使用 4096 字节扇区：

```text
sector 0       superblock
sector 1..7    index bank A
sector 8..14   index bank B
sector 15..    chunk payload sectors
```

superblock 包含：

- magic `MCGR`；
- 区域格式版本；
- 扇区大小；
- region X/Z；
- 固定索引 bank 位置和大小；
- superblock CRC32C。

打开文件时必须验证文件路径推导出的区域坐标与 superblock 坐标一致。

### 6.2 索引 bank

每个 bank 有自己的 magic、区域格式版本、region X/Z、`uint64` 世代号、固定 1024 个索引项和 bank CRC32C。未使用尾部字节必须写零并计入校验，保证编码确定性。

每个 24 字节索引项固定包含：

```text
offset_sector   uint32
sector_count    uint32
payload_length  uint32
chunk_revision  uint64
payload_crc32c  uint32
```

`offset_sector == 0` 表示不存在；此时其余字段必须全零。存在项必须满足：

- offset 不落入 superblock 或两个 bank；
- sector count 非零，且能容纳 payload length；
- payload length 不超过 M3A 上限；
- offset + count 不溢出且不超过文件大小；
- revision 非零；
- 同一有效 bank 的两个槽不能引用重叠扇区。

bank CRC 计算时把 CRC 字段视为零。打开时先验证固定头和 CRC，再验证全部索引项的结构边界；不必为未请求区块预先解压全部 payload。

### 6.3 区块 payload

payload 外层记录：

- magic `CHNK`；
- payload envelope 版本；
- 区块 schema 版本；
- dimension ID 与绝对 chunk X/Z；
- revision；
- 压缩算法 ID；
- 未压缩长度、压缩长度；
- 压缩字节。

索引项的 CRC32C 覆盖实际 `payload_length` 字节，包括 envelope 和压缩内容。payload 中的 key、revision 必须与请求、路径和索引项一致。

M3A 只定义压缩算法 `zstd`。当前格式的硬上限为：

- 单区块压缩 payload 不超过 1 MiB；
- 解压后逻辑 payload 不超过 2 MiB；
- 区段数必须恰好为 `core.SectionsPerChunk`；
- 每个调色板、packed words 数和 block ID 必须通过 `world.ContainerSnapshot` 的既有验证。

解码器必须在分配前验证所有外层长度，并为 zstd 设置输出上限。即使文件由不可信来源提供，畸形长度和压缩炸弹也不能造成无界内存分配。

### 6.4 区块 schema v1

v1 逻辑 payload 使用显式小端字段，依次保存：

- schema magic 与版本；
- 绝对 key 与 revision；
- 固定 24 个按 section index 升序排列的区段；
- 每个区段的 `StorageKind`、`Single`、`Bits`、palette 长度与内容、packed 长度与内容。

编码前对每个容器取独立 `ContainerSnapshot`。解码时经 `world.NewPalettedContainerFromSnapshot` 重建，不访问未导出的内存字段，也不把网络消息 DTO 当作磁盘格式。网络和存档可以共享 `world.ContainerSnapshot` 的逻辑验证，但两者的 wire 格式与版本生命周期彼此独立。

---

## 7. 区域提交与恢复

### 7.1 打开与选择有效索引

打开区域文件时：

1. 验证 superblock。
2. 分别验证 A/B bank 的头、CRC 与索引结构。
3. 两者都有效时选择世代号较高者。
4. 只有一个有效时选择它。
5. 两者都无效时返回区域损坏错误，不重新生成其中区块。

新区域创建时写入有效的空 bank A（generation 1）和有效的空 bank B（generation 0），同步文件后才允许返回成功。

世代号达到 `math.MaxUint64` 时拒绝继续写入并报告明确错误；不得回绕后错误选择旧 bank。

### 7.2 保存批次

同一区域的一次保存批次执行：

1. 按 slot 排序并去重；同一 key 只保留最高 revision。
2. 对每个快照做结构验证、编码和独立 zstd 压缩。
3. 当前磁盘 revision 更高时跳过旧快照；revision 相同且逻辑内容相同允许格式改写或幂等重试，revision 相同但逻辑内容不同视为不变式错误。
4. 从当前有效 bank 未引用的扇区中分配空间，不足则追加文件。
5. 写完全部新 payload，并同步文件数据。
6. 复制当前索引、替换本批次条目、增加 generation，编码到非活动 bank。
7. 写完整个非活动 bank 并再次同步文件。
8. 内存中的活动 bank 只在第二次同步成功后切换。

在新 bank 提交前，任何当前有效索引引用的扇区都禁止覆盖。旧 bank 独占、但当前 bank 不再引用的扇区可以作为 COW 工作空间；写入期间当前 bank 始终足以恢复。

保存结果逐 key 返回实际已提交 revision。服务端只能依据这个值产生保存确认，不能把“worker 已编码”或“payload 已写”当成持久成功。

### 7.3 崩溃结果

提交边界具有以下确定结果：

| 中断位置 | 重启结果 |
|---|---|
| 新 payload 写完前 | 当前 bank 与旧 payload 有效 |
| payload 写完但首次同步前 | 当前 bank 有效；新数据不可见 |
| 首次同步后、bank 提交前 | 当前 bank 有效；新 payload 是可回收孤儿 |
| 非活动 bank 部分写入 | 新 bank CRC 失败，当前 bank 有效 |
| 新 bank 写完但第二次同步前 | 重启可见旧或新 bank，二者都必须自洽 |
| 第二次同步成功后 | 新 bank 是最高有效 generation |

该保证是“每个区域提交不会产生混合索引或半区块”，不是跨多个区域的全世界原子快照。异常退出可能让区域 A 保留新批次、区域 B 保留旧批次；两者各自都必须完整有效。

### 7.4 payload 损坏与旧 bank 回退

正常写入不会覆盖当前 bank 引用的数据，因此写入中断不能损坏当前提交。加载某个区块时若当前 bank 的 payload CRC 或解码失败，可检查旧 bank 对同一 slot 的不同条目：

- 旧条目完整有效时，返回旧逻辑内容并报告已发生恢复。假设损坏的当前索引 revision 为 `R`，加载结果的 current revision 提升为 `R+1`，persisted revision 保留为旧条目的 revision；该区块因此保持 dirty，随后能以高于损坏索引的 revision 写回。
- 两份都指向同一损坏 payload，或旧条目也无效时，返回损坏错误。

若 `R == math.MaxUint64` 无法安全提升 revision，则返回不可自动恢复错误，保留原文件不变。不能让 revision 回绕。

不能因一个区块损坏而重新生成并覆盖它，也不能在没有用户工具和明确选择时删除损坏数据。

### 7.5 空间整理

活动索引之外的 payload 是可回收空间。普通保存优先复用合适的连续空闲扇区，找不到时追加。

同时满足以下条件时安排后台整理：

- 可回收空间至少占文件数据区 25%；
- 可回收空间至少 8 MiB。

整理持有该区域的独占写锁，按 slot 顺序把全部活动 payload 写入同目录唯一临时文件，构建新的有效索引，完整同步后原子替换原区域文件并同步目录。中断发生在替换前则旧文件有效；替换后则新文件有效。启动时只清理符合本格式临时命名且能够确认属于对应区域的残留文件。

区域读取持有区域级读锁直到 payload CRC 与解码完成；保存和整理持有写锁。这样已提交后被回收的旧扇区不会与尚未完成的读取竞争。

---

## 8. 格式迁移

区分三种版本：

- `world.meta` 格式版本；
- 区域容器格式版本；
- 区块 schema 版本。

三者独立推进，不能用一个全局数字暗示所有格式同时改变。

区块迁移以不依赖 `world.Chunk` 内部实现的值 DTO 为输入输出：

```text
decode vN bytes → validate vN → migrate vN→vN+1 → ... → current DTO
→ validate current → construct world.Chunk
```

规则：

- 每个受支持版本到下一版本必须恰好有一个迁移函数。
- 迁移是纯函数，不读时钟、随机数、文件系统或全局注册表。
- 迁移不得原地修改调用方切片。
- 任一步失败都带 world path、dimension、chunk 坐标和源版本上下文。
- 遇到比当前实现新的版本，返回稳定的 future-version 错误。
- 读取迁移后不会立即阻塞加载去写盘；区块标记 dirty，在正常保存预算内写为当前版本。

每个历史版本在 `testdata` 中保留至少一个固定二进制夹具及其当前逻辑 hash。CI 检查迁移注册表从最老受支持版本到 current 连续，无重复、无断点。M3A 建立 v1 框架和 v1 夹具；没有旧版本时迁移链为空是合法状态，但连续性测试仍必须存在。

区域容器或元数据若未来需要迁移，使用“读旧文件 → 写当前格式临时文件 → 同步 → 原子替换”的离线单文件迁移；不能原地改写未知旧布局。

---

## 9. 服务端区块生命周期

### 9.1 获取流程

M2B 的 `TickResult.Generate` 实际表示“权威世界需要这个区块”。M3A 将该语义改名为 acquire/request，避免服务端误以为所有缺失区块都应直接生成。

```text
Absent
  → Loading
      → found and valid → Ready(clean)
      → not found       → Generating → Ready(dirty)
      → load error      → Failed
```

只有 `ErrChunkNotFound` 能进入生成。权限、I/O、损坏、未来版本或取消均不能伪装成未命中。

加载和生成结果都携带 `ChunkKey`。加载结果分别携带 current revision、最后可用 persisted revision、`needsRewrite` 与是否从旧 bank 恢复；生成结果初始 current revision 为 1，persisted revision 为 0。提交结果时必须验证当前状态仍期待该 key，过期 worker 结果直接丢弃。

### 9.2 脏状态

每个 Ready 记录至少有：

```text
current_revision
persisted_revision
unload_requested
needs_rewrite
```

定义：

```text
dirty := current_revision > persisted_revision || needs_rewrite
```

- 从存档加载：两者都等于存档 revision。
- 首次生成：`current=1, persisted=0`，保证探索过的完整地形会保存。
- 方块变化：tick 末 compact 相关区段并将 current 增加一次。
- 保存确认 revision `R`：`persisted=max(persisted,R)`，但不得超过 current。
- 迁移读取：current 与 persisted 都沿用存档 revision，并设置 `needsRewrite`；成功改写相同逻辑内容后清除该标记。
- 旧 bank 恢复：current 提升为损坏索引 revision 加一，persisted 保留旧条目 revision，并设置 `needsRewrite` 与恢复状态；这样修复提交不会被存储的单调 revision 防护拒绝。

同一区块最多一个保存任务在途。任务拿到独立快照和 revision 后，权威区块可继续修改。完成确认若小于 current，只推进 persisted，不会清除 dirty；调度器随后保存新 revision。

### 9.3 保存调度

保存触发源：

1. 固定 5 分钟自动保存周期；
2. dirty 区块收到卸载意图；
3. 迁移后的区块等待改写；
4. 正常关服。

每个 tick 创建保存快照的区块数和估算字节数都有配置预算。候选按以下优先级、再按 `ChunkKey` 排序：

1. 等待卸载；
2. 关服刷盘；
3. 普通自动保存和迁移改写。

深拷贝与任务投递在 tick 预算内完成；编码、压缩、扇区分配、写入和同步全部在 worker 中完成。禁止在 tick goroutine 调用 Store I/O。

### 9.4 卸载

最后一个订阅者离开时，客户端 forget 与磁盘保存分离：

- 客户端立即收到 forget，发布状态释放。
- clean 且无任务引用的区块可从权威内存删除。
- dirty 或保存中的区块标记 `unload_requested`，优先保存并保留到确认。
- 保存成功且 persisted 追上 current 后删除区块。
- 保存期间重新被订阅则清除卸载意图，继续使用同一权威对象，不重复加载。

加载、生成、保存或卸载期间的重复请求必须合并，同一 key 不能存在两个权威实例。

---

## 10. I/O 失败、背压与可观察性

### 10.1 自动保存失败

预期 I/O 失败不得 panic。保存批次失败时：

- 不推进 persisted revision；
- 不丢弃快照对应的权威 dirty 状态；
- 记录包含操作、世界路径、区域、区块范围和根因的结构化日志；
- 按 1s、2s、4s…退避，最大 1 分钟；成功后重置退避；
- 同一区域退避期间合并新 dirty revision，不排队无限多个旧快照。

格式不变式破坏仍应快速失败；磁盘满、只读文件系统、权限变化、短写和同步失败属于可返回错误。

### 10.2 未保存内存上限

脏区块不能为降低内存而丢弃。服务端按保存快照估算字节统计：

- dirty Ready 区块；
- 等待卸载的 dirty 区块；
- 保存 worker 在途快照。

默认上限为 512 MiB，并允许测试使用更小值。达到上限后停止发起新的区块加载和生成，未知边界继续由 M2B 的 Barrier 碰撞保护；已加载区域的 tick、移动和交互保持运行。恢复写盘并低于上限后自动恢复获取新区块。

上限不是删除策略。若玩家继续修改已加载区块，统计可以暂时超过阈值；服务端必须报警但仍保留数据。

### 10.3 状态查询

服务端提供只读值快照供测试、日志和未来 UI 使用，至少包含：

- dirty 区块数和估算字节；
- 保存任务在途数；
- 当前是否因持久化背压暂停新区块获取；
- 最近一次成功保存时间；
- 最近一次保存错误分类与时间；
- 最近一次完整自动保存是否排空当时的 dirty 集合。

这些状态不暴露 Store、文件句柄或可变区块指针。

---

## 11. 正常关服

新增显式的：

```go
func (server *Server) Shutdown(ctx context.Context) error
```

流程：

1. 原子进入 closing，后续输入、Step 和新区块获取被拒绝。
2. 停止 endpoint reader 与生成/加载新任务，等待已经拥有的权威修改进入最终 tick 边界。
3. 冻结权威状态，对全部 dirty 区块按预算分批创建快照；关服不受 5 分钟周期限制。
4. 等待所有区域提交完成，并调用 Store `Sync`。
5. 成功后关闭 Store、端点和全部 worker。

若 context 到期或保存失败：

- 返回带根因的错误，不记录“保存成功”；
- 服务端保持 frozen，不再接受游戏输入；
- Store 与未保存权威状态保持可重试，调用方可以再次调用 `Shutdown`；
- 只有保存成功后才完成最终资源关闭。

成功后的重复 `Shutdown` 返回 nil。失败后的重试从仍 dirty 的 revision 继续，不重复覆盖更高版本。

`Run` 收到外部 context 取消时使用独立的、默认 30 秒关服 context，避免把已经取消的运行 context 传给刷盘。若刷盘失败，`Run` 优先返回持久化错误；正常取消且保存成功时返回原取消原因。

`cmd/mcgo` 必须把非 nil 的关服保存错误明确写到 stderr/日志并以非零状态退出。M3A 不新增存档错误 UI，但不能吞掉错误。

---

## 12. 测试策略

### 12.1 编解码与坐标

- 三种调色板形态和 24 个区段的精确 roundtrip。
- 空气、现有全部方块 ID、15-bit 边界、4→8→15 bit 容器。
- 负区块/区域坐标和 §5 的全部边界值。
- 编码相同逻辑快照两次必须逐字节一致。
- 原始 chunk hash、revision 和 key 在 roundtrip 后精确相等。

### 12.2 畸形与 fuzz

对元数据、superblock、bank、索引项、payload envelope、zstd 数据和逻辑 DTO 分层 fuzz：

- 任意截断与位翻转；
- 伪造长度、扇区溢出、重叠索引和错误坐标；
- 超大 palette/packed 长度与压缩炸弹；
- 未知存储形态、算法 ID 和未来版本。

断言无 panic、无越界、无超过硬上限的分配，并返回稳定分类错误。

### 12.3 区域故障注入

存储文件操作通过窄接口允许测试在每次 write、sync、rename 和 directory sync 前后注入短写或错误。对一次旧→新保存枚举全部中断点，重新打开真实临时目录并断言：

- 只能读到完整旧 hash/revision 或完整新 hash/revision；
- 不能出现新索引配旧 payload 等混合状态；
- 孤儿扇区不影响读取，后续可回收；
- 当前 bank 数据永远没有在提交前被覆盖。

另有子进程测试在选定阶段不执行 defer 直接退出，验证未调用正常 Close 时重开仍符合相同结果。

### 12.4 迁移

- 每个受支持 schema 版本都有固定二进制夹具和目标逻辑 hash。
- 注册表从最老版本到 current 连续。
- 迁移不修改输入，不依赖 map 顺序，重复执行结果一致。
- 迁移读取后的区块被标记改写；保存后重新打开不再执行旧迁移。
- 未来版本明确失败且原文件不变。

### 12.5 服务端集成

- 存档命中不会调用生成器；只有 `ErrChunkNotFound` 会调用。
- 生成器 A 保存的区块在生成器 B 下重启仍保持 A；未探索区块使用 B。
- 挖掘和放置后正常关服重启，权威 hash、revision 和客户端镜像一致。
- 保存中再次修改，旧确认不清除新 dirty。
- dirty 卸载等待保存；重新订阅取消卸载且不重复加载。
- 加载损坏不会静默生成。
- 磁盘满时保留脏状态、退避并触发上限背压；恢复后自动排空。
- `Shutdown` 超时返回错误并可重试；成功后全部 goroutine 在 1 秒内退出。
- 世界锁阻止第二个 Store，同进程关闭后可重新打开。

### 12.6 性能与回归

新增平台无关微基准：

- 单区块 v1 编码/解码及分配数；
- zstd 压缩/解压；
- 典型 32 区块区域批次提交；
- 冷加载与存储命中；
- tick 创建保存快照的预算执行时间。

现有门禁继续执行：

- `go test ./... -race -count=1`
- `go vet ./...`
- `gofmt -l .`
- 依赖方向与 WebGPU 隔离检查
- 物理热路径 `0 allocs/op`
- 完全离屏性能场景；运行时不得创建 GLFW 窗口或把应用带到前台

M3A 性能场景只使用内存 Store，但必须包含服务端保存快照和确认路径。磁盘绝对吞吐只记录同机基线，不作为跨机器 CI 阈值。相同机器上区块编码、压缩、区域批量保存或加载任一基准相对 M3A 基线退化超过 20% 判红。

---

## 13. 错误处理

遵循上位设计的三类错误：

1. **程序不变式破坏**：重复权威实例、persisted revision 超过 current、相同 revision 对应不同逻辑内容、非法状态迁移；panic 或测试失败。
2. **预期外部失败**：世界锁冲突、权限、磁盘满、短写、sync/rename 失败；返回并包装 error，自动保存按策略重试。
3. **不可信存档字节**：损坏校验、越界长度、未知编码、压缩炸弹、未来版本；绝不 panic，返回稳定分类错误且不改原文件。

worker 继续使用 panic 隔离；编码或 I/O worker panic 被转换为带 key/region 上下文的失败结果，不能让区块被错误标记 clean。区域文件路径只从已验证的数字坐标构造，不能把存档内容拼入路径。

---

## 14. 数据流顺序

### 14.1 正常 tick

```text
1. drain client commands
2. drain loaded/generated/save-complete results
3. apply authoritative simulation tick
4. reconcile subscriptions and unload intentions
5. publish player/world messages
6. enqueue bounded load-or-generate work
7. create bounded persistence snapshots
8. dispatch load/generate/save workers
```

save-complete 必须在本 tick 的命令修改前应用或在修改后按 revision 比较；无论选哪一顺序，旧确认都不能清除本 tick 的新修改。实施统一采用上述“先完成通知、后模拟修改”，并用同 tick 测试锁定。

### 14.2 首次世界启动

```text
open/lock world
  → create or validate metadata
  → build generator from metadata seed
  → start server
  → request spawn chunk
  → storage miss
  → generate
  → Ready(dirty)
  → publish snapshot
  → autosave/unload/shutdown persists full chunk
```

### 14.3 重启命中

```text
open/lock world
  → metadata seed
  → request spawn chunk
  → storage hit
  → validate/migrate
  → Ready(clean or needsRewrite)
  → publish snapshot
```

---

## 15. M3A 出口条件

以下条件必须同时满足：

- `cmd/mcgo` 默认创建或打开 `worlds/default`，可用 `--world` 指定其他目录。
- 同一世界只能由一个服务端进程写入。
- 首次生成的完整区块和后续修改都能跨正常关服重启恢复。
- 已探索区块不受后来生成器实现变化影响，未探索区块使用当前生成器。
- 负坐标区块映射到正确的 32×32 区域与索引槽。
- 区域保存任意注入中断后只能恢复完整旧提交或完整新提交。
- 当前/旧 bank payload 回退、孤儿扇区回收和区域整理均有测试。
- 区块 schema v1、版本注册表、迁移连续性和未来版本拒绝均有门禁。
- 所有存档解码均有界，fuzz 不产生 panic 或无界分配。
- 加载、压缩、写盘、sync 和整理不在 tick goroutine 执行。
- 自动保存失败保留 dirty、退避重试，并在积压上限触发新区块背压。
- `Shutdown(ctx)` 成功才报告持久化完成；失败返回根因且可重试。
- 内存 Store 与磁盘 Store 的核心加载/保存/revision 语义一致。
- 重启端到端测试最终得到一致的服务端与客户端镜像 hash/revision。
- `go test ./... -race -count=1`、`go vet ./...`、`gofmt -l .` 与依赖门禁通过。
- 物理微基准与完全离屏性能门禁不回退；整个验证过程不启动前台窗口。

M3A 完成后再单独设计 M3B：端无关二进制协议、TCP Transport、`Handshake → Login → Play` 状态机和稳定玩家身份。M3C 再建立多 session 玩家广播、远端玩家插值和断线清理，最终闭合 M3。
