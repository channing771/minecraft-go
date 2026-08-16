# 项目实现进度

本文件记录已交付里程碑、当前基线与下一步方向。可观察行为一律以代码、测试与 `openspec/specs/` 主规格为准；`docs/superpowers/` 中的设计文档写了某项能力，不代表代码已经实现。

## 里程碑

已完成的 M3 多人里程碑支持最多八名玩家通过局域网专用服务端同步移动、角色和 Unicode 昵称；M4A–M4D 依次增加权威快捷栏、持久掉落物、36 格背包与固定石砖配方；M4E 新增煤矿、铁矿、共享权威熔炉、铁锭与铁块资源链；M4F 新增服务端权威的按住采掘、石镐、铁镐与五条固定配方；M4G 新增由服务端权威推进并持久化的 24000 tick 昼夜与直射天空光，并把世界 metadata 升级到 v2；M4H 新增权威单件原地丢弃；M4I 新增消费权威世界时间的天体天空呈现；M4J 为石镐和铁镐加入权威耐久、损坏形态及跨 TCP/存档的耐久保真；M4K 新增由区块拥有的固定容量共享箱子（每区块 16 个、每箱 27 格），把熔炉与箱子的查看生命周期收敛为共用实现，统一容器界面扩展为 `0..62`，并把线上协议升级到 v12、区块存档升级到 schema v6；M4L 新增由服务端唯一权威、跨重连与重启保真的生命值（满值 20），实现摔落伤害、未受伤自动回复与死亡时的背包掉落和重生结算，并把线上协议升级到 v13、玩家存档升级到 schema v5；M4M 增加客户端从权威方块镜像派生的 `0..15` 传播天空光；M4N 增加客户端派生的静态方块光和发光块，并把线上协议升级到 v14、区块存档升级到 schema v7；随后的常见块状材料批以协议 v15、玩家 schema v6、区块 schema v8 交付 14 种常见块状材料、缺失玩家一次性材料包、世界坐标 terrain UV、玻璃/树叶单 pass alpha cutout 与无窗口材料展示；此后的自然材料与建造批次继续交付沙子/砾石/黏土/雪块自然生成、确定性橡树、材料加工闭环（橡木板配方与三条熔炼映射）、目标方块轮廓与中文名反馈、发光方块固定配方和程序化方块云；M4O 完成全仓职责导向代码组织。

## 当前基线

当前 M5A 基于 M4Q 的 Mornlea 项目身份和 M4P 固定 Rust 1.97.1 `mornlea_engine` cdylib，交付最多四个可配置、服务端权威且保持 idle 的具名伙伴；伙伴独立于八名玩家容量，使用协议 v16 和独立 `companions.ai` schema v1，active 与 inactive 身体记录合计最多 64 条。客户端通过统一 Avatar/NameTag pass 呈现伙伴，并提供有界 Unicode 聊天输入与 HUD；`@伙伴名 指令` 只在权威 tick 边界确认大小写精确的寻址事实。无窗口视觉场景新增唯一末场景 `ai-companion`，benchmark producer 升到 scenario v16，M2 v15/M5 v14 基线保持不变。

当前 Rust 动态库已从 `mornlea_mesh` 原子改名为 `mornlea_engine`；现有 mesh ABI v1、`mornlea_mesh_section`、layout 与 status `0..9` 保持不变。

当前 `mornlea_engine` 也是 client prediction 与 authoritative simulation 共用的 collision resolver 唯一生产实现；Go 保留 physics state/input/tunable 与 checked snapshot 编码，旧 Go resolver 只作测试 oracle。无图形专服的 Linux amd64 canonical bundle 由 `make build-linux-server` 原生构建，binary 通过 `$ORIGIN` 加载同目录 `libmornlea_engine.so`；二者是不可跨版本混装的 release unit。

当前 `mornlea_engine` 同时是方块射线 DDA 的唯一生产实现；Rust 以 caller-owned 64-record cursor batch 生成候选格，Go `core.RaycastBlocks` 仍保留输入校验、单次归一化、惰性 callback、首个 error identity 和 `RayHit.Point`。旧 Go DDA 只作测试 oracle，生产无 fallback。

当前 `mornlea_engine` 同时是物理 tick 积分的唯一生产实现；Go `physics.Step` 保留输入校验、tunables 快照、yaw 三角、位移凸包 sweep bounds 与 prism 编码，旧 Go 积分只作测试 oracle，生产无 fallback。

当前 `mornlea_engine` 同时是世界生成（高度图地形、地表分层、矿石、橡树）的唯一生产实现；engine ABI 升到 v3，新增 `mornlea_worldgen_chunk` 整区块 dense 生成与 `mornlea_worldgen_probe` 64-record 单点查询。Go `worldgen` 保留 seed→perm 表播种（`math/rand` 语义）、材料表编码与 `world.Chunk` 回写，旧 Go 噪声/地形/矿石/橡树实现只作测试 oracle，生产无 fallback；同种子世界与迁移前逐位一致，差分门禁全平台启用。

当前 darwin 客户端的窗口与事件循环由新的 Rust `mornlea_client` cdylib(winit,client ABI v1)独占生产;Go `internal/client` 保留 `Window` 领域 API、快照解码与帧内缓存,每帧一次 FFI 输入快照取代逐键轮询,`go-gl/glfw` 依赖已移除。client 库与 engine 库 ABI 互不耦合,Linux 专服不链接 client 库。这是"GPU 与窗口迁 Rust"路线图(R1 窗口→R2 渲染核心→R3 HUD 呈现)的第一步。

R2a(rust-client-render-terrain)在 `mornlea_client` 内交付纯离屏的 Rust wgpu 世界渲染器(terrain/sky/云/GPU culling/HiZ,client ABI v2),WGSL 与 Go 渲染器单源共享;oak grove 双后端对照门禁以零像素差异通过(最大通道差 0)。生产渲染仍由 Go `internal/render` 执行,golden 基线不变;渲染切换与实体/文本迁移属后续 R2b/R2c。

R2b(rust-client-render-entities)把实体与文本 pass(avatar、掉落物、目标方块轮廓、名牌 billboard、伤害红边、HUD 家族、调试面板)补齐到 Rust 离屏渲染器:frame payload v2 以 TLV pass 段单次 FFI 过境全部帧数据,字形图集(R8 增量矩形)与 HUD 图集字节与 Go 同源(client ABI v3)。完整帧双后端对照(地形+实体+名牌+HUD+面板)以零像素差异通过;生产渲染仍由 Go 执行,golden 不变。剩余 R2c:surface 接线、入口切换与删除 Go 渲染栈。

R2c(rust-client-render-cutover)完成生产切换:client ABI 升到 v4(`render_create_windowed` 把 winit 窗口接为 wgpu surface、`render_resize`、acquire 失败返回 SKIPPED 跳帧),`cmd/mornlea` 窗口与离屏帧循环均改为装配 `client.RenderFrame` 单次 FFI;随后删除 `internal/gfx` 全包、`oliverbestmann/webgpu` 依赖与 render/hud 的 GPU 半部,Go 保留布局、编码、字形 worker 与 `SectionScheduler` 上传调度等 CPU 半部,WGSL 移入 `mornlea_client` crate。全部 capture golden 字节不变,archcheck 改为全仓禁止 Go WebGPU 绑定;双后端对照测试随 Go 渲染器退役,golden 成为长期视觉门禁。

M5B（m5b-companion-planning-fifo）交付第一个可玩的 AI-native 闭环：寻址成功的指令进入每伙伴持久 FIFO（16 条、严格按序、满员 `QueueFull` 同步拒绝），OpenAI-compatible Planner 在 worker 上把有界不可变观察快照转换为严格 JSON `go_to` 计划（30 秒超时不重试、64 KiB 上限、M5B 只接受 go_to 步骤），确定性整数 A*（水平 ±16/垂直 ±4 窗口、4096 节点预算、固定邻居序，归属裁决留 Go）与路径点重验驱动 Task Runner 经 `sim.CompanionAction`（玩家命令后按 ID 字节序、统一物理积分、复用 Rust 出口）移动伙伴；任务状态机 Queued→Planning→Validating→Running→各终态的每次迁移广播 ChatEvent，deadline 用持久化 WorldTimeTicks（1..60 分钟、缺省 10）。协议升到 v17（ChatEvent 追加任务生命周期 kind 与 TaskFailReason 16..19、Rejected 追加 QueueFull，wire 形状与长度上限不变）；`companions.ai` 升到 schema v2（active 记录追加当前任务与最多 16 条 FIFO，文件上界 350,208 bytes，v1 只读迁移，Planning/Validating 关服按 Queued 恢复，Running 精确恢复且不盲走）；`ai` 组新增 endpoint/model/apiKeyEnv/taskTimeoutMinutes（config version 保持 1，https 或 loopback http，密钥只从环境变量解析、不入配置日志存档）。benchmark scenario 保持 v16（固定工作负载未变，场景版本独立于协议演进），全部视觉 golden 字节不变。

M5C（跟随与交互：`follow`/`停止`旁路/`mine`/`place` 与伙伴采掘放置状态共享）与 M5D（persona/台词/摘要）仍是后续方向，开始真实变更前以新的 OpenSpec 为准。历史[设计](../superpowers/specs/2026-08-13-ai-native-companions-design.md)与[实施计划](../superpowers/plans/2026-08-13-m5a-companion-entity-chat.md)仅作决策背景。
