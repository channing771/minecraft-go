## Why

M4E 已经打通矿石、合成与熔炼资源链，但采掘仍是一次点击立即破坏，煤矿、铁矿和铁块没有工具门槛，铁资源也缺少可感知的进阶价值。现在需要用一个有界、服务端权威的最小闭环加入石镐、铁镐和持续采掘，使资源获取、工具升级和掉落规则真正连成玩法。

## What Changes

- 新增石镐与铁镐两种单格物品，以及 `3 石头 → 1 石镐`、`3 铁锭 → 1 铁镐` 两条固定单输入配方。
- 把左键挖掘从一次性命令改为 `PlayerInput` 中的持续意图，由服务端按 20 Hz tick、权威位置、视角、选中工具和目标方块推进独立进度。
- 为土草、石质方块、矿石、熔炉、铁块和基岩定义固定采掘时长与掉落等级；错误工具可以完成破坏但不产生方块掉落，裸手石头保留启动例外。
- 在图形 HUD 中复用现有 pipeline 显示权威采掘进度与是否可掉落，并把背包固定配方从三行扩为五行。
- 保证错误工具破坏熔炉时内部物品仍原子掉落，容量不足时方块、熔炉、物品、掉落物和 revision 全部不变。
- **BREAKING**：线上协议升级为 v8，扩展固定长度 `PlayerInput` 与 `PlayerState`，废止即时 `BreakBlock` packet 且不复用其 ID；v7 或其他版本在登录前稳定拒绝。
- 玩家 schema 保持 v3、区块 schema 保持 v4；新工具使用现有物品字段，在途采掘不持久化。
- benchmark 升级为 scenario v10，只允许显式 `9:10` 迁移；M2 基线保持不变，M5 通过无窗口 Memory/TCP 各一次的正式链建立 v10 基线。

非目标：木材、木棍、木镐、多原料配方、工具耐久、裂纹贴图、多人共享进度、经验、附魔或通用物品元数据。

## Capabilities

### New Capabilities

- `authoritative-mining`: 定义持续采掘输入、权威目标与进度、方块时长、工具等级、取消规则、原子完成、多人顺序和 HUD 确认。

### Modified Capabilities

- `authoritative-hotbar`: 把统一 64 上限改为按物品上限校验，并使采掘掉落取决于选中工具和权威完成结果。
- `authoritative-inventory`: 支持单格工具、五条固定配方界面、容器打开时取消采掘，并把物品与采掘消息契约升级为协议 v8。
- `authoritative-crafting`: 追加石镐、铁镐两条稳定固定配方并保持原子扣料和产物容量规则。
- `deterministic-ore-generation`: 把煤矿与铁矿从无工具门槛改为至少石镐才能取得掉落。
- `authoritative-furnaces`: 定义错误工具破坏时本体不掉落但内部物品仍原子保全的语义。
- `bounded-benchmark-workload`: 将协议和权威 tick 变化后的工作负载标记为 scenario v10，并要求显式 v9→v10 迁移。
- `hardware-performance-baselines`: 规定 M5 v10 基线的一次性无窗口建立与 M2 基线不变边界。

## Impact

- 领域与模拟：`internal/core`、`internal/sim`。
- 协议与多人闭环：`internal/network`、`internal/server`、`internal/client`。
- 图形与输入：`internal/render`、`cmd/mcgo`。
- 性能契约：`cmd/mcgo` benchmark、`cmd/perfcheck`、M5 基线及性能记录。
- 存档：不改变二进制布局或 schema；旧程序遇到包含新工具 ID 的玩家文件时稳定拒绝且不得覆盖，回退必须恢复升级前备份。
- 并发：simulation owner 继续单写；每名玩家只增加一个固定大小状态，每 tick 最多八次六格射线，不新增 goroutine、动态目标表或无界队列。
- 依赖：不新增第三方库或内部包，架构依赖白名单无需扩展。
