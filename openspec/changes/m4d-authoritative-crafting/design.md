## Context

动机见 `proposal.md`。当前 `core.Inventory` 是 36 格固定值，`sim` 在单写者 tick 内按 sequence 原子替换它，客户端只消费完整 `InventoryState`。物品是否有效目前由 `ItemPlacement` 判定，因此最小的新合成产物应同时是可放置方块；材质和物品图标均可沿用程序化路径。

当前协议为 v5，玩家 schema 为 v3，区块 schema 为 v2。区块迁移函数已经按版本串联，但成功读取旧 schema 时没有把 migration 结果传播为 `StoredChunk.NeedsRewrite`；新增可放置枚举若继续写 v2，旧程序还会把未知方块当作合法数值载入。M4D 因此必须同时升级协议与区块 schema，并修复共用的迁移重写信号。

## Goals / Non-Goals

**Goals:**

- 用一条固定配方证明服务端权威、失败原子、顺序稳定的资源转换闭环。
- 让同一纯函数同时驱动服务端提交和客户端“可合成”显示，避免两套规则漂移。
- 保持每次合成固定工作量、一次私有完整状态发布和 Memory/TCP 等价。
- 让新石砖物品与方块可安全存档，并让旧程序在可能误读前稳定拒绝新区块 schema。

**Non-Goals:**

- 不建立通用配方注册器、插件接口、配方 DSL、工作台或合成网格。
- 不支持多原料、多产物、批量合成、队列、拆分堆、工具耐久或熔炼。
- 不增加客户端预测、增量物品状态、服务端 UI open/close 状态或二进制美术资源。

## Decisions

### 1. 追加稳定枚举并用一个 switch 定义配方

在现有稳定 ID 末尾追加 `core.StoneBrickID` 与 `core.ItemStoneBrick`，新增 `core.RecipeID uint8` 和 `RecipeStoneBricks = 1`。`core.Recipe(id)` 用 switch 返回一个简单值：输入 `{ItemStone, 4}`、输出 `{ItemStoneBrick, 4}`；未知 ID 返回 false。客户端显示与服务端执行共用该值，不增加 map、注册生命周期、接口或配置文件。

`BlockDrop`、`ItemPlacement` 和稳定 ID 测试加入石砖；`internal/assets` 追加一个程序化石砖材质层，现有 `HotbarRenderer` 的物品颜色映射加入石砖。方块仍是普通不透明立方体，不改 `mesh`、碰撞或世界模型。

否决方案：通用配方表和多原料结构只服务一个配方；把产物做成不可放置物品则必须先拆开当前物品有效性与放置映射，扩大本批边界。

### 2. `Inventory.Craft` 在值副本上完成全部检查

在 `internal/core` 增加纯值操作 `Inventory.Craft(RecipeID) (Inventory, bool)`：

1. 验证原 Inventory 和 recipe ID。
2. 在副本上按统一索引 `0..35` 扣除输入，数量归零时写回规范空值。
3. 原料不足时返回原值和 false。
4. 对扣料后的副本调用现有 `AddStack` 放入完整产物；存在余量时返回原值和 false。
5. 只有全部成功才返回新值和 true。

一次原料扫描最多检查 36 格，`AddStack` 的四阶段合计最多检查 72 格，因此最坏为 108 次栏位检查且无堆分配。客户端对最后确认镜像调用同一函数取得可用状态，但丢弃返回的新值；不新增容易漂移的 `CanCraft` 实现。

否决方案：先修改权威 Inventory 再回滚会增加失败分支；逐件四次合成会重复扫描并可能部分成功；预留输出槽后再扣料会改变已确定的最低索引规则。

### 3. 合成作为现有玩家命令和 dirty 发布的一条分支

新增 `CommandCraftRecipe`，`sim.Command` 只增加 `Recipe core.RecipeID`。Engine 复用现有 Ready、session 和 sequence 校验，在单写者 tick 调用 `Inventory.Craft`；失败复用 `RejectInvalidInput`，成功原子替换玩家 Inventory 并只置一次既有 inventory dirty。`server` 只负责把网络请求翻译为命令，发布继续复用完整 `InventoryState` 私发路径。

不新增合成结果消息或专用 dirty 位。重复、过期命令继续由统一 sequence 规则过滤，多玩家隔离继续由 session 归属和既有发布注册表保证。

否决方案：客户端提交原料栏位或产物会扩大信任边界；专用结果包会制造与完整 InventoryState 并列的第二份真相；新增拒绝枚举对单一失败类没有用户价值。

### 4. 协议 v6 只追加一个固定请求

`network.ProtocolVersion` 升为 6，在现有客户端 Play packet 末尾追加 `CraftRecipe{Sequence uint64, Recipe core.RecipeID}`，payload 固定为 9 字节。codec、registry 和 packet validation 只接受已注册 recipe ID，拒绝截断、尾随和未知值；服务端与客户端仍通过登录版本检查隔离 v5，不保留双 codec。

Memory 与 TCP 继续使用同一消息类型、验证和 endpoint 翻译。现有 `InventoryState`、掉落物和其他 packet ID 与 payload 不变，只更新 v6 golden 与版本拒绝测试。

否决方案：在 v5 静默追加 packet 会让相同版本号的旧客户端与新服务端错误互认；发送可变配方定义没有必要且增加不可信长度。

### 5. 现有背包 renderer 增加一个固定配方入口

继续扩展 `HotbarRenderer` 的单一 pipeline、CPU slice 和 GPU buffer，在背包打开布局中增加固定的 `4 石头 → 4 石砖` 行、一次合成按钮和 enabled/disabled 颜色。容量按一个配方的最坏 quad/glyph 数固定扩大，不创建配方列表对象。

`internal/render` 增加与绘制共用几何的纯命中函数，返回 `RecipeStoneBricks` 或 false。`cmd/mcgo` 在背包点击路径中先处理配方区域：只有最后确认镜像调用 `Inventory.Craft` 成功时才发送一次请求，并清除尚未提交的来源选择；客户端不写镜像。栏位移动、`E`/`Escape`、鼠标捕获和中性移动输入保持现有行为。

否决方案：第二个 renderer 会重复 pipeline 与 buffer；通用滚动配方列表没有第二个条目；在客户端提前扣料会要求回滚。

### 6. 区块 schema v3 保持布局并传播迁移状态

玩家 schema v3 的字段已能保存任意已注册 ItemID，只需让当前程序识别石砖，不改变版本或 payload。区块 `currentChunkSchema` 升为 3，新增 `2→3` 的无数据变换 migration；v1 继续沿 `1→2→3` 链迁移。v3 的 section 与 drop payload 布局完全沿用 v2。

为保证旧记录最终重写，`decodeChunkPayload` 保留 `migrateChunk` 返回的 migrated 标志，`region.Load` 在正常 active bank 成功时也把它传播到 `StoredChunk.NeedsRewrite`；服务端已有 acquire/dirty/save 路径负责正常重写。这样 v2 世界无损升级，支持 v2 的旧程序看到 v3 envelope 会在解码方块前返回 future version。

新增 v2 fixture、v1 链式迁移、石砖方块/掉落物 roundtrip、故障写入和未来版本测试。任何 schema v3 写入后，旧程序回退必须恢复升级前备份。

否决方案：继续使用 v2 会让旧程序接受未知石砖方块并以默认材质呈现；修改 payload 布局没有新增字段依据；单独石砖存档会破坏区块原子性。

### 7. 验证和提交按可回退小组推进

实现依次分为：稳定枚举与纯合成、区块 schema v3、协议 v6、sim/server 纵向、UI 接线、重启闭环与最终门禁。每组先写失败测试，运行受影响包 race 与架构测试，通过后只提交该组。最终补充短时 codec/chunk fuzz、固定工作量 benchmark、Memory/TCP 多人等价和现有无窗口性能门禁；所有 Go 命令使用 GVM Go 1.26.0，自动验证不启动或聚焦窗口。

## Risks / Trade-offs

- [只有一个固定配方] → 这是刻意的最小闭环；出现第二个已批准配方时再把 switch 扩为固定表，不提前建设注册系统。
- [客户端用权威函数计算 enabled 仍可能因在途请求变旧] → enabled 仅作界面提示，服务端每次重新验证且失败不改状态。
- [协议 v6 与区块 v3 同批升级] → 先备份世界，客户端/服务端成套发布；任何一步失败停止写入并恢复备份。
- [迁移标志传播会触发现有旧区块重写] → 重写复用已有 dirty/原子 region 保存和故障重试，测试限制为实际加载的区块，不做全盘扫描。
- [新增常驻 UI 容量] → 只增加一个固定配方的最坏容量，用 allocation 与 buffer 边界测试锁定，不放宽性能门禁。

## Migration Plan

1. 发布前正常关服并备份完整世界目录；记录玩家 schema v3、区块 schema v2 和协议 v5 基线。
2. 先交付能读取 v1/v2、只写 v3 的区块迁移与新枚举，再在同一版本切换协议 v6 和合成路径；不发布混合组件。
3. 首次启动后只对实际加载且正常保存的旧区块重写 v3，不扫描或批量改写未加载区域。
4. 验证合成、放置、挖掘、重连和重启闭环后再接受新世界写入。
5. 回退时停止服务并恢复升级前备份；不得让旧程序打开已写入区块 schema v3 的世界目录。
