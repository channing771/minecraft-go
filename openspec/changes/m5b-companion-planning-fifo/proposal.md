# Change: m5b-companion-planning-fifo

## Why

M5A 交付了可配置、可持久、可见但保持 idle 的伙伴与确定性 `@名称 指令` 寻址，指令只产生寻址事实。M5B 按 2026-08-13 已批准的 AI-native 伙伴设计（第 15 节分批 2）交付第一个可玩闭环：寻址成功的指令进入每伙伴持久 FIFO，由 OpenAI-compatible Planner 在 worker 上生成受限 JSON `go_to` 计划，确定性 Task Runner 再经权威物理把伙伴移动到目标，任务生命周期以公开 ChatEvent 广播。伙伴身体与呈现自 M5A 已就绪，本批不依赖渲染实现，可与 rust-client-render-cutover 并行。

## What Changes

- 配置 `ai` 组新增 `endpoint`、`model`、`apiKeyEnv` 与 `taskTimeoutMinutes`（config version 保持 1）；非空伙伴配置缺少 endpoint/model 或所需密钥环境变量为空时，内置与专用服务端启动失败；密钥只从环境变量读取，不写入配置、日志、模型错误文本、性能报告或存档。
- **BREAKING**：线上协议 v16 升到 v17；全部既有 message ID 保持不变；`ChatEvent` 追加任务生命周期 kind（`TaskStarted`、`TaskProgress`、`TaskCompleted`、`TaskFailed`、`TaskTimedOut`）与稳定任务失败原因枚举，`ChatRejectReason` 追加 `QueueFull`；v16 与 v17 不跨版本互通。benchmark scenario 保持 v16：固定工作负载（七名远端玩家、零伙伴、无聊天）未变化。
- 寻址成功的指令进入每伙伴独立 FIFO（容量 16、按接收顺序），同一伙伴同一时刻只有一个非终态任务；第 17 条待执行指令以 `QueueFull` 同步拒绝且不调用模型。
- 新增 OpenAI-compatible Planner HTTP worker：tick 边界构造有界不可变观察快照（伙伴水平 16 格、垂直 8 格、最多 256 个按坐标排序的暴露/特殊方块、区块 revision、世界时间、发令玩家视线事实），30 秒超时且不自动重试、响应正文上限 64 KiB、严格 JSON 解码；M5B 计划只接受 `go_to` 步骤，出现其他步骤即任务失败，不重试、不降级猜测。
- 新增确定性有界寻路：Go worker 在不可变快照上执行整数代价 A*（水平 16 格、垂直 4 格窗口、单次最多 4096 节点、固定邻居展开顺序、可跨一格水平间隙、可跳上一格、不挖改方块），路径点提交前重验，失效按固定冷却重算，连续三次失败令任务以路径不可达失败。
- `internal/sim` 从 `playerState` 提取不导出 `actorState`（运动、朝向、背包），既有玩家可观察行为逐 tick 不变；新增按 `CompanionID` 寻址的有界 `CompanionAction` inbox，`Engine.Step` 先处理玩家命令、再按 `CompanionID` 字节序处理伙伴 action、最后统一推进全部 actor 物理；伙伴物理积分复用既有 Rust engine 出口，不新写 Go 积分。伙伴 `3×3` 兴趣随脚下区块滑动。
- 任务状态机 `Queued → Planning → Validating → Running → Completed/Failed/TimedOut`；普通任务 deadline 以持久化 `WorldTimeTicks` 记录（默认 10 分钟、`1..60` 可配），安全关服期间不消耗执行时长；模型计划只在 `Validating` 成功后落盘。
- `companions.ai` schema v1 升到 v2：active 记录追加当前任务（原始指令、go_to 计划、步骤索引、状态、开始 tick、deadline）与最多 16 条 FIFO；v1 只读迁移，未来版本拒绝，64 条身体记录上限与原子替换保存纪律不变。
- 客户端：移动伙伴按与远端玩家相同的插值机制帧间平滑呈现；任务生命周期事件进入既有 ChatEvent 环与 HUD 行；断线清理语义不变。
- 自动测试全部使用 `httptest` 假模型服务，不访问真实模型、不打开前台游戏窗口、不改任何 golden 与性能门禁。

## Capabilities

### New Capabilities

- `companion-planner`: 定义 Planner 输入的有界不可变快照、HTTP 调用边界、严格 JSON `go_to` 计划契约与失败语义。
- `companion-task-queue`: 定义每伙伴 FIFO、任务状态机、世界时间超时与 tick 边界编排。
- `companion-pathfinding`: 定义确定性有界网格寻路、路径重验与三次失败终止。

### Modified Capabilities

- `companion-chat-protocol`: 协议 v17、任务生命周期事件广播与 QueueFull 同步拒绝。
- `companion-persistence`: `companions.ai` schema v2 保存任务与 FIFO，v1 只读迁移与恢复重验。
- `authoritative-companion-entities`: 伙伴从静态实体升级为权威移动 actor（actorState、CompanionAction 与滑动兴趣）。
- `companion-client-presentation`: 移动伙伴插值呈现与任务事件 HUD 行。
- `companion-identity-configuration`: `ai` 组模型运行时配置、endpoint 边界与密钥纪律。

## Impact

- 受影响包：`internal/companion`（Planner/FIFO/寻路/任务状态机）、`internal/config`、`internal/sim`、`internal/server`、`internal/storage`、`internal/network`、`internal/client`（仅伙伴镜像与接收）、`cmd/mornlea`（仅聊天事件展示装配）、`cmd/mornlea-server`、`internal/archcheck`（依赖登记随包内新增文件更新，不新增跨包依赖方向）。
- 兼容性：协议 v16→v17 BREAKING；companion schema v1→v2（v1 只读迁移、未来版本拒绝）；config version 保持 1；玩家 schema v6、区块 schema v8、世界 metadata v2、benchmark scenario v16、perfcheck `15:16` 迁移授权与全部 golden 基线字节不变。
- 并发与热路径：权威 tick 是伙伴身体、任务状态与世界动作的唯一写者；Planner、寻路与存档 I/O 全部在 worker goroutine 上处理不可变值，结果经有界 channel 在 tick 边界应用；最多四个模型请求并发、每伙伴最多一个在途 Planner 请求；慢 HTTP/磁盘不阻塞权威 tick。
- 出站 HTTP 仅面向配置的 OpenAI-compatible endpoint（无 userinfo/query/fragment 的 https，或 loopback http）；不引入第三方 SDK；TCP 仍只面向可信局域网，不新增认证或加密承诺。
- 与 rust-client-render-cutover 并行约束：本变更 MUST NOT 修改 `internal/render`、`internal/render/hud`、`internal/gfx`、`internal/client` 渲染与窗口文件、`cmd/mornlea` 帧循环/抓帧/benchmark 文件以及 `engine/crates/mornlea_client`；两个 active change 并存期间 `openspec validate --all --strict --no-interactive` 各自保持全绿。

## 非目标

- 不实现 `follow`、`停止` 旁路、`mine`、`place` 与伙伴采掘/放置状态共享（M5C）。
- 不实现 persona、节点台词、对话摘要与模型文本上屏（M5D）。
- 不实现通用玩家聊天、完整聊天历史落盘、自主目标、自动拾取/合成/熔炼/容器、怪物 AI、伙伴生命/战斗/死亡。
- 不改变任何渲染输出、视觉场景、golden、benchmark scenario、性能阈值与 `15:16` 迁移授权。
- 不迁移 Planner、寻路或任务编排到 Rust engine，不新增 engine/client ABI 面积。
