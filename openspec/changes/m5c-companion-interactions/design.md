# Design: m5c-companion-interactions

## Context

M5B（已归档）交付了 `go_to` 闭环：FIFO、六态状态机、Planner（仅 go_to）、整数 A*、`sim.CompanionAction`（仅移动）、schema v2、协议 v17。本变更是 2026-08-13 已批准设计第 15 节分批 3：`follow`/`停止`/`mine`/`place` 与权威原子性。R2c 已并入 main（渲染全 Rust），本变更不触碰渲染面，golden 零改动纪律继续成立。

## Goals / Non-Goals

- Goals：四 kind 步骤全集；持续跟随与停止旁路；采掘/放置的玩家规则复用与 tick 原子性；schema v3 变长步骤；多人一致性。
- Non-Goals：persona/台词（M5D）；容器采掘、多掉落、自动拾取/整理/合成；为通路挖障碍或搭桥；寻路算法变更；渲染输出变化。

## Decisions

### 协议 v18：TaskStopped 与 NotFollowing 两个新枚举值

主动停止是新的可观察终态事实，不是失败——玩家预期"我让它停"与"它做不到"是两种语义，复用 `TaskFailed` 会污染失败统计与 M5D 台词触发。`NotFollowing` 加入 `ChatRejectReason`（值 5）服务停止旁路的同步拒绝。wire 形状与全部长度上限不变，v17 拒绝。**被否决**：`TaskFailed`+`TaskFailStopped`——语义错位，且停止后的 FIFO 推进行为与失败不同（队列不变、立即执行原队首，语义相同但事件语义应区分）。

### 状态机第五终态 Stopped 与 deadline 豁免

`Stopped` 只能从 Running 的持续跟随任务经停止指令进入。deadline 豁免的实现成本低但并非免费：M5B 的 `Task.Expired` 对零值 deadline 恒真（从未触发只因 FinishValidation 必设值），M5C 补 `DeadlineTicks != 0` 守卫后，follow 任务不写 deadline 即豁免，持久化层零值即"不保存 deadline"。

### follow 执行：复用寻路与距离边界

跟随不是每 tick 直线逼近，而是复用 M5B 寻路：目标 = 目标玩家脚下或邻近的合法站立格，玩家移动导致路径失效时按既有冷却重算、三连失败走既有语义。`CompanionFollowDistanceBlocks = 4`（水平距离，导出常量）：目标在距离内不提交移动输入（物理照常，重力/碰撞语义保持）；超出则恢复寻路。目标离线（会话消失）→ `FailRun(TaskFailWorldChanged)`——这为 M5B 预留枚举补上产生点。快照新增有界在线玩家集合（≤8，稳定 ID+位置），供 planner 校验 `follow` 目标。

### mine：采掘状态上移 + 完成分叉

`playerMiningState`（target/block/held/progressTicks/requiredTicks/harvestable）整体上移 `actorState`，玩家的 `miningRule` 计时与工具判定原样作用于伙伴。`CompanionAction` 采掘载荷是"按住语义"（与玩家 `Mining: true` 输入一致）：Manager 在交互距离内持续提交按住 action，进度由 sim 累积。完成 tick 的产物去向在 sim 内分叉：玩家完成走既有掉落物路径，伙伴完成直入背包且**先验证容量**——无空位则该 tick 不结算（进度保持），Manager 观察到"就绪但背包无容量"的稳定状态后 `FailRun`（稳定原因 `TaskFailWorldChanged`？否——新增语义归入 InvalidPlan 不合适；裁决：背包容量不足用 `TaskFailWorldChanged` 不贴切，**追加 `TaskFailInventoryFull = 20`**，wire/storage/展示同步登记）。容器与多掉落方块的双重拒绝：planner 契约 + sim 完成分叉处的防御检查（`BlockDrop` 唯一性既有表）。

### place：复用放置校验的单 tick 原子

放置 action 携带目标与方块，sim 在既有玩家放置校验（空气目标、放置合法性、碰撞、Ready）通过后同 tick 扣一件并写入。扣料与写方块在同一权威 tick 内完成——sim 的 pendingChunkChanges 机制天然保证原子提交。物品不足 → Manager 侧 `FailRun(TaskFailInventoryFull)`。

### CompanionAction 判别载荷

```go
type CompanionActionKind uint8 // Move / MineHold / MineRelease / Place
type CompanionAction struct {
    ID    companion.ID
    Kind  CompanionActionKind
    Input physics.Input       // Move
    Target core.BlockPos      // MineHold/MineRelease/Place
    Block  core.BlockID       // Place
}
```
inbox 容量、满员即弃、三阶段顺序、每 tick 每伙伴一个 action 全部不变；applyCompanionActions 按 Kind 分派。**被否决**：拆成多个独立 inbox——顺序保证（同一伙伴的移动与采掘互斥）会复杂化。

### schema v3：步骤变长编码与文件上界 430,080

步骤编码按 kind 变长：`go_to`/`mine` 13 bytes（kind+3×int32）、`place` 15（+block uint16）、`follow` 17（+16-byte player ID）。上界推导：任务区 ≤ 5,000 步 × 17 = 85,000 + 指令 1,024 + 计时/状态 21 + FIFO 16×1,026 = 16,416 → active 记录 ≈ 102,461+221 → 4 active ≈ 410,728 + 60 inactive×221 = 13,260 + envelope 32 → ≈ 424,020，取整上界 **430,080 bytes（420 KiB）**，解码分配前拒绝。v2 迁移：既有步骤全部 `go_to`（13B），按旧布局读、按 v3 写出；`PlanStep` 值结构扩展 `Block` 与 `PlayerID` 字段（按 kind 使用）。v1 文件经 v2 规则同样无损到达 v3。

### 停止指令解析与多人语义

`companion_chat` 寻址成功后指令文本精确等于 `停止`（大小写敏感、无参数）→ 旁路：作用于 Running 的 follow 任务 → `Stop()`（清移动输入、转 Stopped、广播、立即执行原队首）；否则 `NotFollowing` 单播。任何在线玩家可发停止（与"所有玩家可指挥"一致）；同 tick 多条停止按聊天接收顺序处理，第一条生效。

## 依赖与并发

- 权威 tick 唯一写者不变；采掘/放置 action 经既有 inbox，无新并发面。
- `internal/companion` 不新增依赖；`internal/sim` 采掘/放置复用既有 helper；archcheck 预计零变化（server→physics 已在 M5B 登记）。
- 快照构造新增在线玩家集合读取（tick 边界、不可变拷贝），无锁竞争。

## 兼容与回退

- 协议 v17→v18、存档 v2→v3 双 BREAKING；v2/v1 存档只读迁移，回退到 M5B 二进制会拒绝 v3 文件（未来版本纪律，与 M5B 相同）。
- `ai` 配置、寻路、渲染、benchmark scenario、golden 全部不变；性能数值只记录。
- 回退：整支 PR revert 即回 M5B 基线（v3 存档需服主接受任务状态不可读，身体数据保留）。

## 验证方法

- sim：采掘共享差分（玩家行为逐 tick 不变）、伙伴采掘与玩家同规则同 tick 数、三方原子/无容量不结算、放置原子/失败不扣料、action 判别载荷顺序。
- companion：四 kind 解码矩阵（follow 最后一步/目标在线、mine 目标约束、place 注册表+持有）、Stopped 状态机、停止旁路。
- server：follow 距离边界/离线失败/超时豁免、停止事件序列与 FIFO 保持、mine/place 编排端到端（httptest 假模型）、慢模型不阻塞 tick。
- storage：v3 变长 round-trip/golden、v2/v1 迁移、430,080 门禁、恢复重验（离线目标失败）。
- 收尾：全量 race、vet、gofmt、archcheck、openspec strict、Memory/TCP parity、golden 零改动核对。
