## Context

动机见 `proposal.md`，行为契约见 `specs/authoritative-item-dropping/spec.md`。M4G 基线已经具备全局会话命令序号、固定 36 格权威背包、每区块 32 个持久掉落物槽、掉落物兴趣发布、玩家/区块异步保存与关服屏障。`Engine.Step` 是唯一世界写者，Memory/TCP 只负责传输同一封闭消息集合；图形客户端已经用 `InputState` 把放置、选栏和容器切换转为边沿动作。

现有掉落物网络校验仍用 `core.ItemPlacement` 判断合法物品，这与 `persistent-item-drops` 主规格要求的“已注册物品”以及世界、背包和渲染层的 `core.RegisteredItem` 语义不一致：煤炭、粗铁、铁锭和镐合法但不可放置。主动丢弃会稳定触发该缺口，必须在共享网络边界修正，而不是限制主动丢弃只接受方块物品。

## Goals / Non-Goals

**Goals:**

- 在既有单写者 tick 内按全局序号处理一次性主动丢弃，并使背包扣减与区块掉落写入对该命令全成或全不成。
- 复用既有掉落物数据、发布、渲染、存档和拾取路径，支持全部已注册物品。
- 让官方图形客户端只在有效游戏输入边沿发送请求，不引入客户端预测。
- 把协议升级、存档不变、固定资源上限和正常关服恢复写成可自动验证的边界。

**Non-Goals:**

- 不给掉落物增加连续坐标、速度、碰撞、重力、所有者或独立调度。
- 不增加整组丢弃、数量选择、死亡掉落或新 UI。
- 不把玩家文件与区块文件升级为跨文件事务；异常退出仍沿用既有独立原子文件边界。
- 不借机重构命令分发、背包、掉落物或实体架构。

## Decisions

### 1. 使用独立低频命令，不扩张高频 PlayerInput

新增 `network.DropSelectedItem{Sequence uint64}`，分配 Play 客户端 packet ID `11`；ID `0`、`2..10` 保持不变，废止 ID `1` 继续留空。payload 固定为八字节小端序 `Sequence`，协议常量从 v9 升为 v10，握手继续只接受完全匹配版本。

否决给每 tick 的 `PlayerInput` 追加 `Drop`：主动丢弃是边沿动作，扩张高频包会永久增加无动作流量并把去重语义混入移动确认。否决复用 `MoveInventoryStack` 的特殊目标栏位：魔数会让合法栏位校验和协议演进含糊。

`DropSelectedItem.Validate` 无额外字段可校验；状态机、registry、codec、golden、fuzz 和 small-packet benchmark 按现有封闭集合扩展。v9 登录拒绝测试替代旧的 v8→v9 断言；旧 packet ID 和现有 payload golden 不改字节。

### 2. 官方客户端复用 InputState 的按键边沿与现有序号

`internal/client/window.go` 只在最小按键表中追加 `KeyQ`。`InputState` 增加一个 `dropDown` 状态和 `Actions.Drop`，由 `Update` 在容器关闭时产生 `Q` 上升沿；即使界面打开或鼠标未捕获，仍更新物理按键状态，恢复有效输入时不得把一直按住的 `Q` 误判为新边沿。

`cmd/mcgo` 把 Q 状态传给 `InputState.Update`，并只在 `allowActions` 且 predictor Ready 时调用一个小的 `dropSelectedItem` 发送函数。函数使用现有 `nextSequence()` 和 `send`，不读取或修改本地背包，不创建本地掉落物。断线/reset 继续由现有镜像生命周期收敛。

否决在主循环另存一个 `qWasDown`：边沿与容器抑制已经由 `InputState` 统一负责，第二份状态容易在 UI 切换时分叉。否决新增 HUD 提示或动画：既有 `CommandRejected` 日志与掉落物 renderer 已足够闭合本批。

### 3. sim 在单写者中先预检，再提交两个现有值对象

`internal/server/session.go` 只把线上命令映射为 `sim.CommandDropSelectedItem`，不做业务判断。`Engine.Step` 继续先按 Session/Sequence 稳定排序并使用 `session.lastSequence` 丢弃重复或过期命令；新命令的业务函数放在 `internal/sim/drop.go`，不新增包或接口。

处理一条新命令时：

1. 确认会话有 Active 玩家，否则返回 `RejectPlayerNotReady`。
2. 读取权威 `Hotbar.Selected` 和该格；空格返回 `RejectInvalidSlot`。
3. 对权威脚底 `Position` 的 X/Y/Z 分别向下取整，使用 `world.ChunkBlockIndex` 得到所属区块与固定位置；越界、维度缺失或区块非 Ready 返回 `RejectChunkNotReady`。
4. 先调用现有 `Chunk.PrepareDrop(item, blockIndex)` 扫描最多 32 槽；失败返回 `RejectDropCapacity`，此时不改任何状态。
5. 用现有 `Hotbar.Consume(selected)` 得到背包副本，再 `CommitDrop` 一个物品，来源拾取延迟传 `40`；最后赋回背包、标记 `inventoryDirty`，并用现有 `touchChunk` 把掉落变化加入本 tick revision/persistence 批次。

这两个提交都发生在同一 goroutine，且预检阶段不修改原值，因此业务拒绝没有部分状态。新掉落物随后参加同 tick 的既有 `advanceDrops`；创建 tick 计作第一个活动 tick，恰在累计 40 个活动 tick 后允许拾取。

`world.Chunk.CommitDrop` 的共享合并规则调整为：启用空槽时年龄为零并采用来源延迟；合并旧堆时保留 ID、generation 和年龄，把剩余延迟设为 `max(old, incoming)`。这一次修改同时修复现有采掘物合并到已可拾取堆后跳过 10 tick 延迟的问题，又无需给每件物品保存独立计时。

同 tick 的放置、选栏和主动丢弃复用一条按 Session/Sequence 排好序的有界交互切片，在玩家推进和订阅收敛后、掉落物 tick 前依次执行。这样既保留放置使用权威推进后位置的既有语义，也保证较早放置的物品消耗不会被较晚丢弃抢先执行；新主动丢弃仍在同 tick 参加 `advanceDrops`，创建 tick 继续计为第一个活动 tick。切片容量仍以本 tick 已有命令数为上限，不新增 per-command map、锁、goroutine 或事务对象。

否决只在官方客户端抑制同帧放置与丢弃：服务端仍会对其他合法客户端反转序号语义。否决重构全部命令阶段：容器移动与高频输入已有独立时序，本批只统一会竞争选中快捷栏物品的放置、选栏与主动丢弃。否决在客户端携带 Slot：服务端选中栏位才是权威值，客户端 slot 会制造额外陈旧状态与校验分支。否决在玩家视线前生成：掉落物当前只有方块索引，伪造投掷方向会把墙体、跨区块和碰撞问题偷偷带入本批。

### 4. 修正掉落物共享校验而不是增加主动丢弃特例

`network.ItemDrop.validate` 改用 `core.RegisteredItem`，使不可放置但已注册的矿物、锭和工具能通过所有现有 `ItemDropUpserts` 路径；未知 ID 仍整体拒绝。增加煤炭与铁镐的 codec/packet 回归用例，并保留未知物品拒绝测试。

这不是新的行为分支，而是让实现恢复到已经归档的 `persistent-item-drops` 契约。否决在主动丢弃入口拒绝非方块物品或维护第二份白名单：那会让背包合法性、世界槽、网络与渲染四处不一致。

世界掉落堆继续沿用每堆最多 `64` 件的固定格式，即使工具在玩家栏位中的单格上限为 `1`。`core.Inventory.AddStack` 因此把参数解释为待分配的来源堆：物品必须已注册、数量必须在 `1..64`，写入每个栏位时仍使用 `ItemStackLimit`。含多把工具的来源堆会在同一次固定 36 格扫描中拆入多个数量为 `1` 的合法栏位；背包和存档中的单个栏位仍必须通过 `ItemStack.Valid`，不放宽玩家 schema 或槽位合法性。

否决把工具地面堆限制为 `1`：这会让掉落物容量与合并规则按物品分叉，并改变既有“同位置同物品最多 64”的世界、网络和存档语义。否决在拾取循环中逐把调用 `AddStack`：最坏会把一次固定 36 格装入扩张成重复扫描，而共享装入函数已经能按物品上限拆分。

### 5. 发布和持久化不新增消息或文件

成功命令只设置现有玩家 `inventoryDirty` 和区块 pending revision。tick 末尾由现有 `InventoryState` 向所属会话发布完整背包，由掉落物 publication diff 向兴趣范围会话发布 `ItemDropUpserts`；失败只通过现有 `CommandRejected` 返回发起者。客户端 `InventoryMirror`、掉落物镜像和 renderer 无需增加状态。

玩家 schema v3、区块 schema v4 和 metadata v2 均已包含本批所需字段，不改编码。正常关服沿用玩家与区块两个既有屏障，测试证明重启后双方最终值一致。异常退出恰好落在两份独立文件提交之间时仍可能观察到既有拾取路径同类的跨文件不一致；本批不增加 WAL 或双文件事务。

### 6. 性能场景保持 v12

默认 benchmark 不发送 Q 或 `DropSelectedItem`，渲染、世界布局、网络背景流量和采样定义均不变，因此 scenario 保持 v12，M5/M2 基线不重录。新增命令每次最多扫描固定 32 槽；稳定无命令 tick 不增加状态扫描。用 sim benchmark 和 `-benchmem` 记录成功、合并与满容量拒绝成本，任何现有绝对或相对门禁均不得放宽。

## Risks / Trade-offs

- [玩家停在原地会在 40 tick 后捡回物品] → 固定延迟给玩家两秒离开；真正投掷和所有者冷却等出现玩法需求后另开 change。
- [玩家与区块分别保存，异常退出可能只提交一侧] → 与既有拾取边界一致；本批保证命令 tick 原子和正常关服恢复，并在 README 明示非事务边界。
- [协议 v10 要求客户端与服务端同时升级] → 握手在 Play 前稳定拒绝 v9，局域网部署文档列出升级顺序；不做版本协商。
- [同 tick 新掉落物立即推进一次年龄和延迟] → 明确定义创建 tick 为第一个活动 tick，以累计活动 tick 测试锁定，不引入跳过集合。
- [主动丢弃合并会让旧堆一起再等待最多 40 tick] → 这是单槽单计时模型下保证新物品不被立即拾取的最小确定性语义；需要逐件冷却时再引入不同数据模型。
- [修正网络物品校验扩大了可接受值集合] → 只扩大到 `core.RegisteredItem`，这与背包、世界槽、渲染和已归档主规格一致；未知 ID 继续拒绝并保留 fuzz/golden 覆盖。

## Migration Plan

1. 先完成 sim、network、server、client 的自动测试和协议 v10 固定值，再更新文档；不生成任何存档迁移器。
2. 部署时先正常停止 v9 客户端和服务端，再同时换成 v10 二进制；旧客户端会在握手阶段得到版本不匹配。
3. 回退时正常停止 v10 程序并换回匹配的 v9 客户端/服务端即可；玩家 v3、区块 v4、metadata v2 字节不变，无需恢复世界备份。
4. 若实现或验证显示默认 benchmark 工作负载发生变化，停止本 change 并先更新 proposal/design；不得静默改 scenario 或提升基线。
