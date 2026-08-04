## Why

M4C 已提供固定 36 格权威背包，但采集到的资源仍只能保存、整理和原样放置，无法形成资源转换闭环。M4D 以一条固定配方和一个可放置产物验证最小服务端权威合成路径，为以后增加配方留下稳定但不过度抽象的边界。

## What Changes

- 新增一个稳定配方：消耗 4 个石头并产出 4 个石砖；新增稳定的石砖方块与物品 ID，石砖可放置、挖掘并以程序化材质显示。
- 客户端可按配方 ID 请求合成一次；服务端按玩家命令序列处理，只从当前 36 格权威物品状态按最低栏位索引扣除原料，并把产物按现有 `AddStack` 规则放回背包。
- 合成在完整物品状态副本上原子计算；未知配方、原料不足、产物无法完全放入、非法状态或过期序列 MUST 稳定拒绝，原料和产物均不得部分变化。
- 现有 `E` 背包界面新增固定石砖配方入口和可合成状态；一次有效点击只发送一次请求，客户端不预测扣料或产物，继续只显示服务端确认的完整状态。
- **BREAKING**：线上协议从 v5 升为 v6，新增固定长度 `CraftRecipe` 请求；v5 及更早版本在进入 Play 前稳定拒绝，不提供协商或降级解码。
- 玩家存档保持 schema v3；区块存档从 schema v2 升为 v3，payload 字段布局不变，有效 v2 无损迁移并在下次正常保存时改写。这样旧程序会把含石砖的 v3 区块当作未来版本稳定拒绝；首次合成或放置前仍需备份世界目录。
- 本批不实现 2×2/3×3 网格、工作台、配方发现、批量合成、合成队列、拆分堆、工具与耐久、熔炼或其他新物品。

## Capabilities

### New Capabilities

- `authoritative-crafting`: 定义固定配方、确定性原料扣除、产物插入、失败原子性、私有确认和 Memory/TCP 等价行为。

### Modified Capabilities

- `authoritative-inventory`: 背包界面增加固定配方入口，完整权威物品状态确认合成结果，并把唯一支持的线上协议升级为 v6。
- `authoritative-hotbar`: 新增可采集、可放置的石砖物品，并把共享物品协议契约升级为 v6。
- `persistent-item-drops`: 掉落物可承载新增的已注册石砖物品，并把唯一支持的线上协议升级为 v6。

## Impact

- 受影响包：`internal/core`、`internal/assets`、`internal/world`、`internal/storage`、`internal/sim`、`internal/network`、`internal/server`、`internal/client`、`internal/render`、`cmd/mcgo` 与 `internal/archcheck`；`cmd/mcgod` 只复用共享服务端能力。
- 数据所有权：`sim` 单写者拥有配方执行和完整 Inventory 提交；客户端只发送 recipe ID 并消费最后确认镜像，不能声明原料、产物或数量。
- 兼容性：协议 v6 拒绝 v5；玩家 schema v3 布局不变，区块 schema v3 读取 v2 后无损迁移；旧程序拒绝未来区块 schema，回退必须恢复备份。
- 并发与性能：配方数固定为 1，每次请求最多执行 108 次固定栏位检查且只发布一次完整状态；不得在权威 tick、网络或渲染热路径增加无界工作、阻塞 I/O 或动态资源增长。
- 依赖与资源：复用现有 `Inventory`、`AddStack`、命令序列、完整状态发布、背包 renderer 和程序化材质；不新增第三方依赖、通用配方注册器、容器接口或二进制美术资源。
- 验证：补充配方原子性、协议 golden/fuzz、Memory/TCP 等价、多人隔离、存档重启、UI 命中和固定分配测试；全部自动验证保持 headless，不启动或聚焦游戏窗口。
