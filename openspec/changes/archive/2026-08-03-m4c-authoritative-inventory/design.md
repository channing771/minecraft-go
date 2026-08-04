## Context

动机见 `proposal.md`。当前 `core.Hotbar` 是唯一玩家物品容器，`sim.playerState` 直接持有它；掉落物逐件调用 `Hotbar.Add`；服务端以完整 `HotbarState` 私发；玩家 schema v2 在 v1 payload 后追加快捷栏；图形客户端以独立只读镜像驱动固定 9 格 HUD。交互循环默认捕获鼠标，`Escape` 释放，下一次左键重新捕获。

M4C 同时改变权威状态、命令、协议、玩家存档、客户端输入与 HUD，但不改变区块 schema、掉落物区块模型或架构依赖方向。`sim` 仍是单写者，`network` 与 `storage` 只依赖 `core` 值，`render` 不参与权威规则，`cmd/mcgod` 不接触图形包。

## Goals / Non-Goals

**Goals:**

- 用一个固定值替代“快捷栏与背包分别维护”的可能性，使拾取、移动、放置、同步和存档共享校验规则。
- 所有操作最多扫描 36 格，完整状态固定长度，并保持每玩家私有发布和现有命令顺序。
- 在不暂停服务端的情况下提供可自动测试的背包输入与固定容量界面。
- 让 v2 玩家无损迁移现有快捷栏，并为后续合成留下稳定的 36 格输入状态。

**Non-Goals:**

- 不设计通用容器接口、窗口协议、槽位策略对象或交易事务；本批只有玩家自身一个容器。
- 不让服务端跟踪“背包界面是否打开”；该状态只影响本地输入路由。
- 不增加部分堆操作、拖拽、快捷键搬运、合成格、装备格或容器并发访问。

## Decisions

### 1. `core.Inventory` 包含现有快捷栏和固定背包

新增：

```go
const BackpackSlots = 27
const InventorySlots = HotbarSlots + BackpackSlots

type Inventory struct {
    Hotbar   Hotbar
    Backpack [BackpackSlots]ItemStack
}
```

统一线上索引 `0..8` 映射快捷栏，`9..35` 映射背包。`Inventory.Valid` 复用 `Hotbar.Valid` 与 `ItemStack.Valid`。`AddStack` 接收完整 `ItemStack`，按快捷栏同类、快捷栏空格、背包同类、背包空格的阶段顺序一次扫描并返回剩余堆；现有 `Hotbar.Add` 保留给只操作一个物品的调用和兼容测试。`MoveStack` 在值副本上完成移动、合并或交换，成功后返回完整新值；失败返回原值。

来源等于目标、越界、空来源或同类满目标统一属于无效物品移动，复用现有 `RejectInvalidInput`；越界仍可使用 `RejectInvalidSlot`。不新增只服务于 UI 的拒绝枚举。

否决方案：把 36 格扁平为单数组会迫使放置、选择和 HUD 到处做索引切片；为 Hotbar/Backpack 建通用接口只有一个实现且增加间接调用；逐件循环添加最多会重复扫描 `64×36`，直接处理整堆更短也更稳定。

### 2. `sim` 只持有一份 Inventory

`playerState.hotbar` 改为 `inventory`，`hotbarDirty` 改为 `inventoryDirty`；选择和放置继续访问 `inventory.Hotbar`，拾取调用 `Inventory.AddStack`，移动命令调用 `MoveStack`。所有命令继续按 `SessionID`、sequence 排序，在单写者 tick 中原子替换整个值；成功变化只产生一次玩家 dirty 与完整状态更新。

新增 `CommandMoveInventoryStack`，只携带来源和目标。它不携带物品、数量或客户端 revision，因此客户端声明不能决定结果。过期 sequence 继续由现有会话规则丢弃；玩家未 Ready、非法索引和非法移动走现有拒绝发布路径。

掉落物竞争顺序、年龄、区块 revision 和原子存档保持不变。一次拾取先计算玩家新 Inventory 和掉落物余量，再提交两者；全满时两者都不变，部分成功时玩家状态与掉落物数量在同一个 tick 更新。

否决方案：单独维护 backpackDirty 与 HotbarState 会产生两份可观察快照和发布顺序问题；为物品操作建立通用事务日志不符合固定 36 格、单写者模型。

### 3. 协议 v5 替换状态包而不保留双轨

`ProtocolVersion` 升为 5。客户端 packet 表在现有命令后追加 `MoveInventoryStack{Sequence uint64, From, To uint8}`。服务端 packet ID 10 的 `HotbarState` 直接替换为 `InventoryState{Inventory core.Inventory}`；v4 无法进入 Play，因此不需要同时保留旧 payload。固定状态 payload 为 `selected(1) + 36×(item uint16 + count uint8) = 109` 字节。

codec 逐格编码并验证精确长度、索引和状态，继续拒绝尾随字节。Memory 与 TCP 复用同一消息接口、registry 和校验。服务端在 Ready `PlayerState` 前发布首次 `InventoryState`，以后只在 `inventoryDirty` 时私发完整值。

否决方案：增量槽位消息需要客户端 revision、丢包恢复和批次原子性；同时发送 `HotbarState` 与 `InventoryState` 会产生重复真相；可变长度背包没有收益，因为 27 格是硬契约。

### 4. 玩家 schema v3 在 v2 后追加固定背包

玩家 envelope 版本保持 1，payload schema 升为 3。v3 编码沿用完整 v2 payload，并在末尾追加 `27×3=81` 字节背包。解码按 schema 选择：v1 解析既有字段，v2 解析快捷栏，v3 再解析背包；迁移链 `v1→v2→v3` 只补零值字段。DTO、`PlayerSave`、`StoredPlayer` 和 sim snapshot 改为持有 `core.Inventory`，避免存储层重复组合规则。

CRC、最大 payload、revision 冲突、原子替换、失败重试与多身份隔离沿用现有实现。v2 首次加载设置 `NeedsRewrite`，正常保存才写 v3。区块 schema v2 完全不变。

否决方案：独立背包文件无法与玩家位置/快捷栏原子提交；修改 envelope 版本没有必要；原地覆盖 v2 字段会增加迁移风险。

### 5. 客户端以单一 InventoryMirror 驱动 HUD 与背包

`HotbarMirror` 替换为 `InventoryMirror`，只接受完整 `InventoryState`，提供完整状态和其中 Hotbar 的只读访问。断开、登录失败、重连和维度 reset 复用现有 reset 点清空镜像及本地选格。客户端发送移动请求后不修改镜像，直到服务端完整状态到达。

应用持有 `inventoryOpen` 和可选来源索引两个简单字段，不建立 UI 状态机包。`E` 的上升沿打开/关闭；打开时释放鼠标。第一次有效左键只记录来源，第二次有效左键发送一个移动请求并清除来源。点击界外不发送请求。`Escape` 在背包打开时只关闭背包并重新捕获；背包关闭时保留现有释放鼠标行为。

为了清除服务端可能保留的上一帧移动输入，打开背包时立即发送一次零移动/零跳跃输入，并在界面打开期间按现有输入 cadence 继续发送中性输入；不更新 yaw/pitch，不发送挖掘、放置或数字键选择。

否决方案：向服务端发送 open/close 没有权威用途；在客户端预测交换会产生回滚；停止发送输入会让服务端继续使用最后一次移动值。

### 6. 扩展现有 HotbarRenderer 绘制完整界面

保留一个 `HotbarRenderer` 和一套 pipeline/buffer，把固定容量扩大到 36 格界面的最坏 quads/glyphs。背包关闭时仍只布局底部 9 格 HUD；打开时布局 3×9 背包、间隔和 1×9 快捷栏，并显示来源高亮。物品仍用现有色块和最多两位数量字体，不增加贴图。

`internal/render` 提供与绘制共用几何常量的纯 `InventorySlotAt(cursor, framebuffer)` 命中函数，`cmd/mcgo` 用它把点击映射为 `0..35`；这样输入和可见格不会复制布局公式。每帧覆写预分配 slice 和固定 GPU buffer，不创建每格对象。

否决方案：新增第二个 renderer 会重复 pipeline、字体 buffer 和 draw；把 UI 坐标放进 `core` 会污染端无关领域；在 `cmd/mcgo` 复制布局常量容易让点击区与画面漂移。

### 7. 验证与提交边界

每组先写最小失败测试，再实现并运行受影响包 race 测试。重点覆盖 36 格校验、整堆余量、所有移动分支、拾取跨快捷栏/背包、玩家 v2→v3 golden/migration/fuzz/故障注入、协议 v5 golden/fuzz、Memory/TCP 等价、首次/私有发布、E/Escape/中性输入、点击命中和满 36 格稳定分配。

最终运行全仓 race、vet、archcheck、gofmt、OpenSpec strict、协议/玩家存档短时 fuzz、物品热路径微基准和现有不聚焦窗口性能门禁。所有 Go 命令使用现有 GVM Go 1.26.0。

## Risks / Trade-offs

- [完整状态每次固定发送 109 字节] → 8 名玩家和人工物品操作频率下远低于现有出站上限；只有实测带宽成为瓶颈才设计增量 revision。
- [背包打开时客户端仍可能有上一输入在途] → 打开立即发送中性输入并持续按 cadence 发送零值，服务端按 sequence 最终收敛。
- [两次点击没有部分堆能力] → 本批明确只做整堆，减少命令和 UI 分支；合成需要拆分时另开规格。
- [v3 玩家存档旧程序不可读] → 升级前备份完整世界目录，旧程序继续稳定拒绝未来 schema，不允许覆盖。
- [扩大 HotbarRenderer buffer 增加少量常驻显存] → 容量只有 36 格且固定；用 allocation/渲染测试锁定，不引入第二套 renderer。
- [用 packet ID 10 替换 payload 依赖握手隔离] → v4 必须在 Play 前拒绝，并用 raw TCP/Memory 测试证明旧连接不能进入新 codec。

## Migration Plan

1. 先交付 `core.Inventory` 与玩家 schema v3 读写，验证 v1/v2 fixtures 无损迁移且旧文件只在成功保存后改写。
2. 同一发布版本切换协议 v5、sim/server/client 状态类型和移动命令；不发布混合 v4/v5 组件。
3. 接入背包输入与渲染，运行 headless UI、Memory/TCP、多人隔离、重启恢复和性能门禁。
4. 上线前正常关服并备份世界目录。若尚未写入 v3，可直接回退旧程序；一旦写入 v3，回退必须停止服务并恢复备份，不能让旧程序处理新存档。
