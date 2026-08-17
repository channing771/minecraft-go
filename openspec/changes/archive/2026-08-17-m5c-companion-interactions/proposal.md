# Change: m5c-companion-interactions

## Why

M5B 交付了第一个可玩的 AI-native 闭环（`go_to` 移动），但计划步骤只有一种，伙伴与世界仍无交互。本变更按 2026-08-13 已批准的 AI-native 伙伴设计第 15 节分批 3，把伙伴推进到完整的跟随与世界交互：持续 `follow` 与唯一绕过 FIFO 的 `停止` 旁路、权威计时采掘 `mine` 与原子放置 `place`、伙伴背包与权威原子性，以及 Memory/TCP 多人一致性。M5B 预留的 `TaskFailWorldChanged` 失败原因在本批获得产生点（跟随目标离线）。

## What Changes

- **BREAKING**：线上协议 v17 升到 v18；全部既有 message ID 不变；`ChatEventKind` 追加 `TaskStopped`（玩家主动停止持续跟随是新的可观察终态事实，不是失败）；wire 形状与长度上限不变；v17 与 v18 不跨版本互通。benchmark scenario 保持 v16（固定工作负载未变）。
- 任务状态机追加第五终态 `Stopped`：只能从 Running 的持续跟随任务进入；`@伙伴名 停止`（精确文本、大小写敏感）是唯一绕过 FIFO 的控制指令——终止当前持续跟随、清空移动输入、立即执行原队首任务、不清空队列；对非跟随任务 `停止` 同步拒绝且回复发令者。停止指令对全部在线玩家可用（与既有"所有玩家可指挥"产品选择一致）。
- `follow` 计划步骤：携带目标 `player_id`（必须来自当前快照的在线玩家集合）；只能作为计划的最后一步；持续跟随在线玩家至固定距离内停止移动、超出距离恢复跟随；跟随移动复用 M5B 的寻路窗口、重算冷却与三连失败语义；持续跟随不受普通任务 deadline 限制；目标玩家离线则任务以 `TaskFailWorldChanged` 失败。
- `mine` 计划步骤：携带目标坐标；Task Runner 先走入交互距离，随后持续按住采掘——完全复用玩家的计时采掘规则（`miningRule` 工具判定与所需 tick）与交互距离/Ready 区块校验；采掘完成的同一权威 tick 内原子完成三方：方块改为空气、扣除工具耐久、可收获产物直接加入伙伴 36 格背包（任一不满足则该 tick 不变更任何一方）。首版仅接受具有单一 `BlockDrop` 且不是箱子或熔炉的普通方块（Planner 契约与 sim 防御双重拒绝）；背包无容量则任务失败。
- `place` 计划步骤：携带目标坐标与方块名；Task Runner 先走入交互距离，随后在单一权威 tick 内验证（目标为空气、放置规则与碰撞校验、伙伴背包持有对应物品至少一件）并原子扣除一件物品、写入方块；失败不扣料。放置规则复用玩家的权威路径。
- Planner 计划契约扩展：steps 追加 `follow(player_id)`、`mine(x,y,z)`、`place(x,y,z,block)`；`follow` MUST 是最后一步；`mine` 目标 MUST 在快照观察范围内且为可采掘普通方块；`place` 的 block 名 MUST 来自固定注册表且快照背包显示伙伴持有。解码严格性、64 KiB 上限与"不重试不降级不改写"语义不变。
- `internal/sim`：`actorState` 二次提取——玩家采掘状态（目标、进度、持握工具、可收获标志）与交互距离/Ready 区块校验上移共享；`CompanionAction` 扩展为移动/采掘按住/采掘释放/放置的判别载荷，仍按 `CompanionID` 寻址、玩家命令后按 ID 字节序处理、每 tick 每伙伴最多一个 action；三阶段顺序与既有玩家可观察行为不变。
- **BREAKING（存档）**：`companions.ai` schema v2 升到 v3——计划步骤从固定 13 bytes 改为按 kind 变长（`go_to`/`mine` 13、`place` 15 追加 block、`follow` 17 追加目标 player ID），物理文件上界相应从 350,208 调整为 430,080 bytes（推导见 design.md）；v2 只读迁移（既有任务全部为 `go_to` 步骤，无损升级），未来版本拒绝。恢复的持续跟随任务在下一动作前重验目标在线性，目标已离线则按 `TaskFailWorldChanged` 失败；停止是瞬时控制指令，无持久化状态。
- 客户端：`TaskStopped` 稳定中文事实行；跟随中的伙伴呈现完全复用既有移动插值；断线清理语义不变。
- 多人一致性：任意在线玩家的 `停止` 与指令在 Memory/TCP 两种传输下产生相同任务状态、事件序列与世界结果。

## Capabilities

### New Capabilities

- `companion-follow`: 持续跟随语义、固定距离边界、deadline 豁免、目标离线失败与停止旁路的全部行为契约。
- `companion-world-actions`: 伙伴采掘与放置的权威原子性、计时规则复用、产物直入背包与扣料不回滚语义。

### Modified Capabilities

- `companion-chat-protocol`: 协议 v18、`TaskStopped` kind、停止指令的同步拒绝语义。
- `companion-task-queue`: 第五终态 `Stopped` 与持续跟随的 deadline 豁免。
- `companion-planner`: 计划步骤集扩展为交付全集（`go_to`/`follow`/`mine`/`place`）及其契约。
- `authoritative-companion-entities`: `CompanionAction` 判别载荷与采掘/交互校验的 actorState 共享。
- `companion-persistence`: schema v3 变长步骤、持续跟随任务的恢复重验语义。
- `companion-client-presentation`: `TaskStopped` 事实行。

## Impact

- 受影响包：`internal/network`（协议 v18 枚举）、`internal/sim`（采掘/交互共享与 action 扩展）、`internal/companion`（planner 契约、状态机 Stopped、follow/mine/place 步骤域）、`internal/server`（编排扩展）、`internal/client` 与 `cmd/mornlea`（仅聊天事实行）、`internal/storage`（恢复语义确认，schema 不变）。
- 兼容性：协议 v17→v18 BREAKING；companion schema v2→v3（v2 只读迁移）；config version 保持 1；benchmark scenario 保持 v16；全部视觉 golden 字节不变。
- 并发与热路径约束不变：权威 tick 唯一写者、worker 只读不可变值、每伙伴一个在途 action、慢模型/磁盘不阻塞 tick。
- 自动测试全部使用 httptest 假模型，不访问真实模型、不打开前台游戏窗口。

## 非目标

- 不实现 persona、台词、对话摘要（M5D）。
- 不实现容器（箱子/熔炉）的伙伴采掘、多掉落方块、自动拾取/整理/合成/熔炼、搭桥或为通路挖障碍。
- 不实现玩家创建伙伴、伙伴所有权或权限组；`停止` 对所有在线玩家可用是明确产品选择。
- 不修改寻路算法本体、渲染输出、golden、benchmark scenario 与性能门禁。
