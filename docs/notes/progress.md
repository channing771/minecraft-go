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

M5A 不包含 Planner、HTTP 模型调用、FIFO、移动、采掘、放置、跟随、persona 或摘要；这些能力仍属于后续 M5B–M5D 方向，开始真实变更前以新的 OpenSpec 为准。历史[设计](../superpowers/specs/2026-08-13-ai-native-companions-design.md)与[实施计划](../superpowers/plans/2026-08-13-m5a-companion-entity-chat.md)仅作决策背景。
