## Purpose

为常见标准立方体材料定义稳定编号、权威放置采掘、缺失玩家初始材料、协议与存档兼容，以及玻璃和树叶的可见性、遮挡和完整碰撞行为。

## ADDED Requirements

### Requirement: 稳定材料注册表

系统 SHALL 在 `LightBlockID` / `ItemLightBlock` 之后按以下顺序只追加稳定方块与物品编号：`CobblestoneID` / `ItemCobblestone`、`SmoothStoneID` / `ItemSmoothStone`、`SandID` / `ItemSand`、`GravelID` / `ItemGravel`、`OakLogID` / `ItemOakLog`、`OakPlanksID` / `ItemOakPlanks`、`LeavesID` / `ItemLeaves`、`GlassID` / `ItemGlass`、`BrickID` / `ItemBrick`、`WhiteWoolID` / `ItemWhiteWool`、`RoofTileID` / `ItemRoofTile`、`ClayID` / `ItemClay`、`SnowBlockID` / `ItemSnowBlock`、`MossyCobblestoneID` / `ItemMossyCobblestone`。这些编号 MUST NOT 重排或复用；每种物品的堆叠上限 MUST 为 `64`。协议、存档和物品入口 SHALL 作为信任边界拒绝未知编号；terrain 的 `Material` 只作为已通过 `RegisteredBlock` 与 `FaceVisible` 校验后的内部材质选择器。

#### Scenario: 追加固定材料清单
- **GIVEN** 当前稳定编号止于 `LightBlockID` / `ItemLightBlock`
- **WHEN** 注册 14 种材料
- **THEN** 编号 MUST 只按批准清单追加，`RegisteredBlock`、`RegisteredItem`、`ItemPlacement`、`BlockDrop` MUST 一一对应，未知编号 MUST 被拒绝

#### Scenario: 未知编号不能降级为既有材料
- **WHEN** 协议、存档或物品信任边界收到未注册的方块或物品编号
- **THEN** 系统 MUST 返回可读错误，且 MUST NOT 把未知编号解释为石头、空气或其他已注册材料

#### Scenario: 未知方块不进入材质选择
- **GIVEN** terrain 网格读取到未注册的当前方块编号
- **WHEN** 判断该方块是否产生可绘制面
- **THEN** `RegisteredBlock` 与 `FaceVisible` MUST 在调用 `Material` 前拒绝该面
- **AND** 未知方块 MUST NOT 产生 terrain quad，也 MUST NOT 因 defensive fallback 实际渲染成石头

### Requirement: 权威放置采掘与掉落

14 种材料 SHALL 继续由服务端权威执行放置、计时采掘、背包扣除和掉落实体生成。沙子、砾石、树叶、玻璃、白色羊毛、黏土和雪块 MUST 在 `5` tick 后允许任意手持物采收；原木和木板 MUST 在 `15` tick 后允许任意手持物采收；圆石、平滑石、砖块、红色瓦块和苔藓圆石 MUST 复用 `StoneID` 规则：空手或损坏工具为 `30` tick 且可采收，石镐为 `15` tick，铁镐为 `8` tick，其他普通物品为 `30` tick 且不可采收。不增加新的工具类型或工具门槛。

#### Scenario: 放置并采收新材料
- **GIVEN** 任一新材料物品
- **WHEN** 服务端放置并完成可采收的采掘
- **THEN** 世界 MUST 写入对应方块、扣除一件并掉落自身，且三组固定采掘 tick/工具规则 MUST 与设计一致

#### Scenario: 普通物品不能采收石质材料
- **GIVEN** 玩家手持非工具普通物品采掘任一石质规则的新材料
- **WHEN** 权威采掘持续 `30` tick
- **THEN** 方块 MUST 被移除但 MUST NOT 产生材料掉落

### Requirement: 缺失玩家材料包

系统 SHALL 只在玩家存档明确不存在时构造一次初始材料包：固定 27 格背包的前 14 格 MUST 依稳定材料清单顺序各包含 `64` 个对应物品，九格快捷栏 MUST 保持为空。材料包 MUST 通过现有背包合法性规则，并仍由服务端权威确认与持久化。

#### Scenario: 缺失玩家获得固定材料包
- **GIVEN** `LoadPlayer` 返回 `ErrPlayerNotFound`
- **WHEN** 准备玩家快照
- **THEN** 快捷栏 MUST 为空且背包前 14 格 MUST 按固定顺序各含 64 个材料

#### Scenario: 已有玩家与未确认登录不被补发
- **GIVEN** 已有玩家或未确认登录
- **WHEN** 恢复、迁移或断开
- **THEN** 已有背包 MUST 不变且未确认材料包 MUST 不持久化或累加

#### Scenario: 确认后的新玩家不会重复获得材料
- **GIVEN** 新玩家已确认登录并保存包含初始材料包的快照
- **WHEN** 该玩家再次登录
- **THEN** 系统 MUST 恢复已保存背包，且 MUST NOT 再次填充或累加材料

### Requirement: 协议与存档语义版本

新增稳定编号集合上线时，线上协议语义版本 SHALL 升至 v15，玩家存档 schema SHALL 升至 v6，区块存档 schema SHALL 升至 v8；三者字节布局 MUST 保持不变。玩家 v5→v6 与区块 v7→v8 MUST 执行 identity migration，保留既有背包、耐久、位置、生命值、静态发光块、palette、掉落物、熔炉、箱子和 revision，且不得向已有玩家注入材料包。

#### Scenario: 新注册表使用固定语义版本
- **WHEN** 新注册表上线
- **THEN** `ProtocolVersion` MUST 为 15、player schema MUST 为 6、chunk schema MUST 为 8，旧数据 MUST identity migrate 且 future/unknown 数据 MUST 明确拒绝

#### Scenario: 旧客户端在握手阶段被拒绝
- **GIVEN** 客户端协议版本为 v14 而服务端协议版本为 v15
- **WHEN** Memory 或 TCP transport 执行登录握手
- **THEN** 服务端 MUST 返回明确的版本不匹配，且 MUST NOT 让会话进入 Play

#### Scenario: 旧存档迁移保持原始状态
- **GIVEN** 一份完整有效的玩家 v5 或区块 v7 存档
- **WHEN** 当前程序读取并迁移该存档
- **THEN** 除语义版本和既有重写标记外的全部状态 MUST 保持不变

#### Scenario: future schema 被旧程序拒绝
- **WHEN** 程序读取高于自身支持版本的玩家或区块 schema
- **THEN** 读取 MUST 明确失败，且 MUST NOT 把 future 数据当作旧布局继续解码或覆盖

### Requirement: cutout 方块语义

玻璃和树叶 SHALL 是使用标准完整方块碰撞的可绘制 cutout 方块。它们 MUST NOT 完全遮挡 AO 或客户端派生的天空光与静态方块光；相同玻璃之间和相同树叶之间 MUST 剔除内部面。不透明方块与 cutout 方块相邻时 MUST 保留可透过孔洞观察到的不透明方块面；两种不同 cutout 方块相接时 MUST 不生成重叠的共面内部表面。

#### Scenario: cutout 方块可见但不完全遮挡
- **GIVEN** 玻璃或树叶
- **WHEN** 网格、AO、天空光、静态方块光与碰撞查询它
- **THEN** 它 MUST 可绘制、保持完整碰撞、不得完全遮挡 AO/天空光/静态方块光，并 MUST 剔除同类内部面

#### Scenario: 不透明邻面保持可见
- **GIVEN** 一个不透明方块与玻璃或树叶相邻
- **WHEN** 构建两者交界处的 terrain 网格
- **THEN** 不透明方块朝向 cutout 方块的面 MUST 保留，以便通过透明像素观察该面

#### Scenario: 不同 cutout 材料不生成重叠内面
- **GIVEN** 玻璃与树叶相邻
- **WHEN** 构建两者交界处的 terrain 网格
- **THEN** 两种材料之间 MUST 不生成重叠的共面内部表面
