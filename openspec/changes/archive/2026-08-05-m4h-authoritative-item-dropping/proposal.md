## Why

M4G 已经完成权威昼夜，但玩家仍无法主动把背包物品交给他人或腾出栏位；现有持久掉落物只能由采掘和熔炉破坏产生。下一批应复用已经稳定的背包、掉落物、多人发布与持久化路径，加入一个有界的权威主动丢弃闭环，而不提前实现掉落物物理或通用实体系统。

## What Changes

- 客户端在游戏输入有效时以 `Q` 的按下边沿请求丢弃权威选中栏位的一个物品；容器打开或鼠标未捕获时不得发送。
- 服务端按既有全局命令序号去重，在同一权威 tick 内先预检玩家、选中栏位、所属区块和掉落物容量，再原子扣除一个物品并在玩家脚下创建持久掉落物。
- 主动丢弃物使用固定 `40` tick 拾取延迟；合并到旧堆时保留 ID 和既有寿命时间线，并把整堆拾取禁止窗口延长到不少于 `40` 个活动 tick，随后复用既有拾取、兴趣同步、区块存档和多人呈现语义。
- **BREAKING**：线上协议升级为 v10，新增只携带 `Sequence` 的固定长度 `DropSelectedItem` Play 客户端包；v9 客户端在进入 Play 前稳定拒绝。既有 packet ID 不重排，废止 ID 不复用。
- 不改变玩家 schema v3、区块 schema v4、世界 metadata v2 或 benchmark scenario v12；不新增依赖、worker、队列、渲染 pipeline 或存档文件。
- 非目标：整组丢弃、丢弃数量选择、投掷速度、重力或水平移动、所有者专属拾取、死亡掉落、客户端预测、跨玩家/区块存档事务、通用实体或 ECS。

## Capabilities

### New Capabilities

- `authoritative-item-dropping`: 定义主动丢弃命令、服务端原子校验与状态转移、固定拾取延迟、Memory/TCP 一致性和持久化边界。

### Modified Capabilities

- `persistent-item-drops`: 把原先统一的 `10` tick 拾取延迟细化为采掘/方块破坏掉落 `10` tick、主动丢弃 `40` tick，其余活动 tick、拾取、寿命和持久化语义不变。

## Impact

- 协议与传输：`internal/network` 的消息、registry、codec、packet 状态机、golden/fuzz/基准与协议版本；Memory/TCP 继续共用同一消息语义。
- 世界与权威模拟：`internal/world` 让合并堆的拾取延迟取旧值与新来源的较大值；`internal/sim` 新增命令分支，复用 `core.Inventory`、`world.Chunk.PrepareDrop`/`CommitDrop`、既有拒绝原因、dirty revision 和发布路径。
- 服务端装配：`internal/server` 只负责把线上命令映射为 sim 命令，并复用现有 InventoryState、ItemDropUpserts、玩家/区块保存及关服屏障。
- 客户端：`cmd/mcgo` 增加 `Q` 边沿输入和既有输入抑制；`internal/client`、`internal/render` 不增加主动丢弃的预测或专用呈现状态。
- 兼容与回退：v10 与 v9 不互通；回退旧程序前只需正常关服并使用匹配协议客户端，存档无需迁移或恢复备份。异常进程退出仍沿用现有玩家文件与区块文件各自原子、彼此非事务的恢复边界。
- 性能与并发：每条命令只执行固定栏位查询和固定 32 槽预检；不改变无命令稳定 tick、默认 benchmark 工作负载或资源上限。
