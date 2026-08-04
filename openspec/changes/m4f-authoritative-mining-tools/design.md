## Context

动机见 `proposal.md`。当前客户端预测器已经按 20 Hz 固定步发送 `PlayerInput`，服务端在 simulation owner 中按 session、sequence 应用最后有效输入并推进玩家物理；左键破坏则仍通过独立 `BreakBlock` 一次性命令直接完成。完整物品状态是固定 36 格值，所有物品共享单格上限 64；固定配方是单输入、单输出 switch；方块破坏、掉落物与熔炉内容已经能在同一区块 revision 内原子提交。

本 change 受 `specs/` 下八份 delta specs 约束。实现必须保持服务端权威、Memory/TCP 同逻辑、simulation owner 单写、固定资源上限和无窗口自动验证。

## Goals / Non-Goals

**Goals:**

- 在不增加新包或依赖的前提下，以现有持续输入和单写者 tick 实现两级权威计时采掘。
- 让工具单格上限、合成、协议、HUD、熔炉内容保全和性能场景形成可单独评审、验证和回退的闭环。
- 保留玩家 schema v3、区块 schema v4 与已有世界内容，明确协议 v8 和 M5 scenario v10 的迁移边界。

**Non-Goals:**

- 不把物品堆改为带 metadata 的可变结构，不实现耐久或通用工具系统。
- 不扩展单输入配方模型，不加入木材资源链、裂纹材质或客户端采掘预测。
- 不建立共享方块 damage 表、每目标 goroutine、离线采掘或动态配置系统。

## Decisions

### 1. 物品上限由一个端无关查询统一决定

在现有稳定 `ItemID` 和 `RecipeID` 末尾追加石镐、铁镐及两条配方。`internal/core` 增加一个无分配的物品单格上限查询，未知物品返回无效结果；`ItemStack.Valid`、`Hotbar.Add`、`Inventory.AddStack`、`Inventory.MoveStack` 与 `Inventory.Craft` 都使用该查询。普通物品仍为 64，两把镐为 1。

这样只改变现有固定值算法，不引入物品定义注册表、接口或 metadata map。否决只在 UI 限制工具堆叠，因为网络、存档和模拟仍会接受非法数量；也否决立即加入耐久字段，因为它会改变所有栏位的协议与存档布局，超出本批范围。

### 2. 采掘意图复用 PlayerInput，进度只归 simulation owner

`PlayerInput` 在既有固定字段末尾追加 `Mining bool`。客户端 `Control` 和输入路由传递主键按住状态，预测历史仍只保存物理输入；预测器的 neutral/suspended 路径必须发送 `Mining=false`。

`playerState` 增加 `miningHeld bool` 与一个固定大小 `playerMiningState`，后者记录 active、目标、开始时方块、工具、progress、required 和 harvestable。非法输入、session reset 与注销直接清零。采掘状态不进入 `physics.Input`、玩家快照或磁盘存档。

否决开始/停止双命令，因为服务端仍需每 tick 使用最新视角和位置，额外命令只增加生命周期；否决客户端完成请求，因为它让客户端决定权威完成时机。

### 3. 在移动和既有世界推进后按稳定 session 顺序采掘

每个 `Engine.Step` 继续先应用命令、推进玩家物理与订阅，再推进掉落物和熔炉、执行跨容器移动与放置，随后调用采掘推进，最后统一完成区块变化并发布状态。采掘使用移动后的权威眼睛位置、当前 yaw/pitch、当前选中格和当前世界方块；熔炉在被破坏前仍先完成本 tick 既有推进，新增掉落物不会在创建 tick 提前老化。

采掘推进收集最多八个活动玩家的 session ID 到固定数组并排序，不直接遍历 map 决定竞争顺序。每人最多一次六格 `RaycastBlocks`。同一目标与同一工具递增；目标、方块 ID 或工具变化时从 1 重新开始；松键、无命中、未就绪、基岩、reset 或断开时归零。

不使用共享方块进度 map。共享 map 会引入清理、跨区块生命周期和多人合并规则，而且与已经确认的每玩家独立语义不符。

### 4. 把方块完成逻辑从即时命令中抽成单一原子入口

删除客户端与模拟层的即时 `BreakBlock` 行为，把现有破坏分支拆为按已验证目标位置执行的内部 helper。采掘达到 required 后向该 helper 传入 harvestable：

- harvestable 普通方块沿用 `BlockDrop` 与 `PrepareDrop`；
- 非 harvestable 普通方块不预留掉落槽，直接提交空气；
- harvestable 熔炉按本体、输入、燃料、输出预演；
- 非 harvestable 熔炉只按输入、燃料、输出预演，空内容不创建掉落；
- 任何应保全物品放不下时不写方块、不停用熔炉、不改掉落物或 revision。

完成成功或失败后都清零该玩家进度。容量失败使用该玩家最后有效 `PlayerInput` sequence 发布一次既有容量拒绝；继续按住会从下一 tick 开始一轮新进度，不会逐 tick 重复拒绝。

### 5. 协议 v8 扩展既有固定消息并保留 ID 空洞

协议版本升为 8。`PlayerInput` 在线尾追加一个布尔值；`PlayerState` 在线尾追加 active、目标三坐标、progress、required 和 harvestable。非 active 时新增字段全零；active 时必须满足 `1 <= progress < required`。sim 使用自己的定长更新值，`internal/server` 只做字段映射，避免 sim 依赖 network。

删除 `BreakBlock` 的消息类型、codec 分支、服务端翻译和模拟命令。Play client packet ID `1` 从双向注册表移除但不分配给其他类型；其余 ID 保持不变。v7 客户端在 Handshake 阶段拒绝，因此不提供兼容 decoder。

否决新增单独 `MiningState` server packet：`PlayerState` 已经每 tick向所属玩家确认位置、视角和输入 sequence，追加固定字段可以避免第二条发布生命周期和额外 outbox 压力。

### 6. HUD 和五行配方复用现有固定渲染路径

`cmd/mcgo` 从最后确认的 `PlayerState` 读取采掘状态。`internal/render` 的 hotbar layout 增加两个固定 quad：背景与按 `progress/required` 裁剪的填充；harvestable 使用绿色，否则使用橙色。不新增 shader、纹理、glyph、pipeline 或运行时容量增长。

固定配方 ID 数组追加石镐和铁镐，配方 quad/glyph 上限从三行对应值调整为五行对应值；命中测试继续与绘制共享行几何。背包或熔炉打开时主键仍只形成界面点击上升沿，交给预测器的持续状态必须为 false。

### 7. 存档保持现有 schema，回退依赖升级前备份

工具只占用现有 `ItemID + Count`，玩家 schema 保持 v3；采掘状态是连接期状态，不写玩家存档。方块、熔炉和掉落物布局不变，区块 schema 保持 v4。新程序可以读取旧文件；旧程序遇到工具 ID 时通过既有完整状态校验拒绝并保持文件不变。

不为仅追加枚举值提升玩家 schema，沿用 M4E 新物品的现有策略，避免无负载变化的全量迁移。发布前正常关服并备份世界；回退旧程序时恢复首次保存工具之前的备份。

### 8. scenario v10 显式迁移且正式基线只执行一次

协议 payload 和权威 tick 工作量变化使报告不能与 v9 静默相对比较。benchmark 标记升为 v10，完整 workload、阈值、采样数和无窗口路径保持不变。`cmd/perfcheck` 在现有升级选择中追加唯一的 `9:10`，执行完整性与绝对门禁、跳过跨 workload 相对回归；相同 v10 仍执行完整回归与跨 transport 比较。

M2 基线及路径保持不变。M5 在实现、全仓门禁和计划提交后冻结候选 HEAD，经用户明确授权，用两个全新路径先执行一次 Memory，再在通过后执行一次 TCP；任一步失败停止且不重跑。全部通过后才把 Memory 精确字节写入 M5 基线并记录哈希和被替代的 v9 身份。

## Risks / Trade-offs

- [每 tick 最多八次射线增加 simulation 成本] → 固定八人和六格上限，独立 benchmark/分配断言，并用 scenario v10 正式链验证；不得放宽门禁。
- [目标切换时同 tick 从 1 开始可能与“先清零一帧”观感不同] → 规格明确旧进度丢弃且当前有效目标立即计第 1 tick，避免人为增加输入延迟。
- [错误工具可销毁方块，玩家可能误操作] → HUD 用橙色明确提示无掉落；熔炉内容始终保全，避免数据损失。
- [旧程序把新工具视为未知物品] → 记录兼容边界、升级前备份、回退恢复备份；旧程序必须拒绝且不覆盖。
- [移除 packet ID `1` 可能被误复用] → registry/golden 测试显式断言 ID 空洞，后续消息 ID 全部冻结。
- [固定五行配方未来会到达布局上限] → 本批只有五条，继续使用固定数组；真正需要分页或分类时再设计配方目录。

## Migration Plan

1. 先提交端无关工具 ID、单格上限和固定配方，保持协议与游戏输入不变。
2. 在同一发布分支内完成协议 v8、权威采掘、服务端 Memory/TCP 闭环和 UI；不发布混合 v7/v8 组件。
3. 更新 README、LAN 文档、OpenSpec、scenario v10 producer/comparator，并完成全仓无窗口验证。
4. 冻结候选提交、取得一次性授权、按 Memory 后 TCP 顺序建立 M5 v10 基线；两步都通过后才提升基线。
5. 部署前正常关服并备份整个世界目录。若回退，停止新程序并恢复首次保存工具之前的备份，再启动旧程序。
