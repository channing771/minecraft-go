# Design: m5b-companion-planning-fifo

## Context

M5A（已归档）交付了 idle 伙伴、协议 v16 聊天寻址与 `companions.ai` schema v1；`@名称 指令` 只产生寻址事实。本变更是 2026-08-13 已批准的 AI-native 伙伴设计（`docs/superpowers/specs/2026-08-13-ai-native-companions-design.md`）第 15 节分批 2，交付第一个可玩闭环：Planner + 持久 FIFO + `go_to`。伙伴身体、Avatar/NameTag 呈现、聊天 HUD 与 3×3 兴趣自 M5A 已存在；本批不依赖渲染实现载体，与并行的 rust-client-render-cutover（R2c）文件级零交集。

## Goals / Non-Goals

- Goals：指令进入持久 FIFO 并严格按序执行；OpenAI-compatible Planner 生成受限 `go_to` 计划；确定性寻路与权威物理移动；任务生命周期公开事件；schema v2 持久化与恢复；慢模型/磁盘不阻塞权威 tick。
- Non-Goals：`follow`/`停止`/`mine`/`place`（M5C）；persona/台词/摘要（M5D）；通用玩家聊天；寻路或 Planner 迁 Rust；渲染、golden、benchmark scenario 与性能门禁变化。

## Decisions

### 归属裁决：寻路留 Go，物理复用 Rust（依据 docs/notes/go-rust-division.md）

按判定规则顺序自问：

1. 寻路不是窗口/事件/GPU/shader，规则 1 不命中。
2. 规则 2（≥数千元素同一数值变换）不命中：A* 是带优先队列的图搜索，节点逻辑异构（支撑判定、间隙跨越、跳跃），不是对 voxel/cell/quad 的同构数值变换。
3. 规则 3（跨平台位级确定性）的关切是 FPU 行为：本寻路只用整数坐标与整数代价，配合固定邻居展开序，确定性由构造保证，不依赖 FPU 语义。
4. 规则 5 是决定性反证：寻路消费的数据（`world.Chunk` 体素）在 Go 侧。若迁 Rust，每次查询都要把水平 16 × 垂直 4 快照搬过 FFI 再搬回，恰好制造 division 文档禁止的"数据搬过边界再搬回来"。

因此寻路实现为 `internal/companion` 内的 Go worker：不可变快照 + 整数 A* + ≤4096 节点 + 固定邻居序。**被否决的替代方案**：engine ABI 新增 `mornlea_path_find` 出口——需要新的 ABI 面积与快照编码，且无法给出性能证据（性能数值只记录不门禁），违背规则 5。

伙伴移动物理**必须**复用既有 Rust engine 积分出口：伙伴与玩家共用 `actorState` 后进入同一批量物理 step，不新写 Go 积分，engine ABI 零变化。

### actorState 提取范围（最小化）

M5 设计 §5.2 允许按批提取。M5B 只需要移动，因此 `actorState` 只容纳运动、朝向与背包（两者身体记录本就同构）；采掘状态、交互校验共享留给 M5C 首次需要时再提取。`playerState` 保留生命、重生与输入序号；`companionState` 保留 `CompanionID`。提取正确性以"玩家全行为逐 tick 差分不变"为门禁（既有 oracle 测试机制）。

### CompanionAction 与 tick 顺序

`sim` 新增有界 inbox（容量按 4 伙伴 × 1 action/tick 定容）；action 只含 `CompanionID` 与规范移动输入。`Engine.Step` 顺序固定：玩家命令 → 按 `CompanionID` 字节序的伙伴 action → 统一物理积分与世界变更。Task Runner 每 tick 每伙伴最多提交一个移动输入，实际位移由权威物理决定——LLM 永远不选择每 tick 输入。

### Planner worker 并发模型

- 每伙伴最多 1 个在途规划请求；全服务端最多 4 个模型请求并发（信号量）。M5B 无 Dialogue，该上限只服务 Planner。
- 请求在 worker goroutine 发起，只读 tick 边界构造的不可变快照副本；结果经容量 4 的 channel 回送，携带（伙伴、任务、世代）身份。
- 结果只在 tick 边界应用；任务已终态或世代不符即丢弃。HTTP 客户端固定 30s 超时、无重试、响应上限 64 KiB（头与正文分别设限）。
- key 只在请求构造时从环境变量读取；错误包装只保留状态码与类别，不保留正文。

### 协议 v17：枚举扩展，wire 形状不变

`ChatEventKind` 追加 5 个任务 kind、`ChatRejectReason` 追加 `QueueFull`，全部复用既有字段槽位（kind/reason 均为 uint8 枚举），**不新增 wire 字段**，ChatEvent 1328 bytes 上限不变。协议 v16→v17 BREAKING（仓库惯例：无跨版本互通），v16 登录明确拒绝。任务事件不携带模型文本（M5D 前模型自由文本不上屏）。

**benchmark scenario 保持 v16**：`bounded-benchmark-workload` 的场景版本语义是"固定工作负载变化才升版本"，M5B 的固定输入（七名远端玩家、零伙伴、无聊天）与全部上传布局未变；scenario 版本独立于协议版本演进。由此 `cmd/mornlea` benchmark 文件、`cmd/perfcheck` 的 `15:16` 迁移授权与 `hardware-performance-baselines` 全部不动。

### companions.ai schema v2 布局与字节上界

文件结构沿用 MCAI envelope：32-byte header + payload。v2 记录 = v1 身体（221 bytes）+ 可选任务区 + 可选 FIFO 区（仅 active 记录）：

- 任务区：原始指令 ≤1,024+2、计划步骤数 ≤5,000 × 13 bytes（kind 1 + 坐标各 4）、步骤索引 4、状态 1、开始 tick 8、deadline 8，约 66,050 bytes；
- FIFO 区：16 × (1,024+2) = 16,416 bytes。

5,000 步骤数是防御性二进制上界：设计明确不设步骤数上限而以 64 KiB 响应正文为天然界限（最密 `go_to` JSON step ≥30 bytes，实际 ≤ ~2,200 步）。

物理文件最大长度 = 32 + 4 × (221 + 66,050 + 16,416) + 60 × 221 ≈ 344,044，取整上界 **350,208 bytes**（342 KiB）。解码在分配前按此常量拒绝超长；v1 文件（≤14,176）只读迁移，首次保存写 v2。未来版本拒绝、CRC32C、temp/Sync/Rename/parent-Sync 原子替换纪律全部沿用。

### 恢复语义

- `Planning`/`Validating` 关服：计划未落盘，恢复为 `Queued` 并保留原始指令，重启后重新规划。
- `Running` 恢复：保留步骤索引，下一动作前重验目标与路径点；失败走既有重算/三次失败语义，不盲走旧路径。
- deadline 用持久化 `WorldTimeTicks`，关服期间世界时间不推进，不消耗执行时长。

## 依赖与并发

- `internal/companion` 保持只依赖 `core`/`companion` 既有方向，新增 Planner HTTP 客户端只用标准库 `net/http`；不导入 `server`、`network`、`render`、`storage`。
- 权威 tick 是伙伴身体、任务状态与世界动作的唯一写者；Planner/寻路/存档 worker 只处理不可变值，经有界 channel 回 tick 边界。
- 跨 goroutine 发送后的快照与计划切片视为不可变；模型响应解码后复制为拥有值。
- `internal/client` 只改 `companions.go`/`receiver.go` 相关镜像与插值；`cmd/mornlea` 只改聊天事件展示装配，不触碰帧循环与抓帧文件。

## 与 rust-client-render-cutover 的并行纪律

- 本变更**不得**修改：`internal/render`、`internal/render/hud`、`internal/gfx`、`internal/client` 的 `render.go`/`window.go`、`cmd/mornlea` 的 `app_frame.go`/`app_render.go`/`capture*`/`benchmark*`/`visual_compare*`、`engine/crates/mornlea_client`、`go.mod`。
- 两分支都保持 golden 字节零改动；R2c 的核心门禁（golden 不变）与本变更无交集。
- 合并顺序：任一分支先并入 main 均可，后并入者在合并前重放全量门禁（`go test ./... -race`、`go vet`、`gofmt`、archcheck、`openspec validate --all --strict`）；两个 active change 目录互不重叠，validate 天然并存。
- 唯一理论交叠是 `internal/archcheck/dependency_test.go`（两侧都可能更新依赖表）：本变更预计零依赖方向变化，若实现中确需登记，合并时以文本合并解决。

## 安全边界

- 玩家指令文本、方块名与模型输出全部视为不可信数据；系统提示声明其为数据，权限边界只有本地 JSON schema 白名单。
- 不执行模型返回的代码、URL、工具名、shell 或模板调用。
- key 不写入配置、日志、错误文本、性能报告或存档；错误正文不回显。
- TCP 仍只面向可信局域网；"所有玩家可指挥所有伙伴"沿用 M5A 产品选择。

## 验证方法

- `internal/companion`：httptest 假模型（成功/超时/5xx/超大/未知字段/尾随 JSON/context 取消/prompt 隔离）；寻路确定性重放、4096 预算、跨/跳、revision 失效、三次失败；FIFO 顺序与容量；状态机全路径。
- `internal/sim`：actorState 提取后玩家行为逐 tick 差分；CompanionAction 顺序；伙伴物理与玩家同出口；3×3 滑动兴趣。
- `internal/network`：v17 codec golden/fuzz、枚举边界、v16 拒绝。
- `internal/storage`：v2 round-trip/golden、v1 迁移、损坏/未来/截断/超长、原子替换故障注入。
- `internal/server`：tick 边界编排、慢 HTTP/磁盘不阻塞 tick（race）、关服顺序、Memory/TCP parity。
- 收尾：`go test ./... -race`、`go vet ./...`、`gofmt -l .` 无输出、archcheck、`openspec validate --all --strict --no-interactive`；自动测试不访问真实模型、不打开前台窗口。

## 回退方案

- `ai.companions` 为空时除协议 v17 外行为与 M5A 一致。
- 存档回退限制：M5B 写出 schema v2 后，M5A 二进制按"未来版本拒绝"原则拒读 v2 文件；回退到 M5A 需要服主接受任务状态不可读（身体数据仍在 v2 文件内，重新启用 M5B 可恢复）。此为 schema 单向演进的既有纪律，非本变更新增风险。
- 协议回退：v17 与 v16 不互通，回退需客户端与服务端同版本，与既有 BREAKING 惯例一致。
