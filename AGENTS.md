# Mornlea 项目指南

## 项目定位

本仓库是 Go 1.26 编写的独立体素游戏 Mornlea，Go module 为 `github.com/channing771/mornlea`，包含自研客户端、权威服务端、世界存储、物理和 Rust wgpu 渲染。它不追求兼容官方 Minecraft 的协议、存档或版权资源。

当前代码基线是 M5D，已经包含协议 v19、Memory/TCP 共用登录与权威模拟、TCP 直连、无图形专用服务端、稳定玩家身份、玩家 schema v6、区块 schema v8、世界 metadata v2、独立 `companions.ai` schema v4、最多八名玩家的局域网同步与远端玩家呈现，以及最多四个可配置、服务端权威的具名伙伴（M5C 起可经任务移动、跟随、采掘与放置，M5D 起具备性格台词与近期记忆）。伙伴使用独立 `CompanionID`，active 与 inactive 身体记录合计最多 64 条；客户端通过统一 Avatar/NameTag pass 呈现伙伴（移动按远端玩家同机制插值），并提供有界 Unicode 聊天输入与 HUD。M5B 交付第一个 AI-native 闭环：`@伙伴名 指令` 寻址成功后进入每伙伴 16 条持久 FIFO（满员 QueueFull 同步拒绝），OpenAI-compatible Planner 在 worker 上把有界观察快照转为严格 JSON `go_to` 计划（30 秒超时不重试、64 KiB 上限、密钥只从环境变量解析），确定性整数 A*（4096 节点、固定邻居序，归属 Go）与路径点重验驱动 Task Runner 经 `sim.CompanionAction`（玩家命令后按 ID 字节序、统一物理积分、复用 Rust 出口）移动伙伴；任务状态机每次迁移广播 ChatEvent，deadline 用持久化 WorldTimeTicks；`companions.ai` schema v2 落盘当前任务与 FIFO（文件上界 350,208 bytes、v1 只读迁移）。M5C 交付伙伴世界交互与持续跟随：计划步骤全集扩展为 go_to/follow/mine/place 四 kind（观察快照追加在线玩家集合），follow 是最后一步的无限期任务（4 格水平跟随距离内停止移动、deadline 豁免、目标离线按 TaskFailWorldChanged 失败），mine 先走近再复用玩家权威计时采掘规则、完成 tick 内方块改空气、扣工具耐久与产物入背包三方原子（背包无空位以 TaskFailInventoryFull 失败且不破坏方块，容器与多掉落方块被计划与模拟双重拒绝），place 先走近再在单一权威 tick 内按玩家放置规则原子扣料写入；`@伙伴名 停止`（精确文本）是唯一绕过 FIFO 的控制指令，作用于持续跟随任务时转入 Stopped 终态、广播 TaskStopped 并立即执行原队首任务，否则只向发令者同步拒绝（NotFollowing）；协议 v18 追加 TaskStopped/NotFollowing 与 TaskFailReason 扩展，`companions.ai` schema v3 任务区步骤按 kind 变长（13/15/17 bytes、文件上界 430,080 bytes、v2/v1 只读迁移）。M5D 交付性格与近期记忆（完全旁路的表达平面，任务事实平面零语义变化）：`ai.companions[].persona` 可选内联人设（≤4,096 bytes）或配置同目录 `personas/<canonical 名称>.txt` 外部文件（内联优先、双源告警、损坏降级空人设；`Definition.Persona` 是磁盘镜像、`ResolvedPersona`（`json:"-"`）是生效值，Save/迁移绝不吸收外部文件或清除越界原文）；Dialogue worker 与 Planner 共用 endpoint/model 但提示与输入完全隔离（人设 + 最近摘要 + 事实节点 + 极小环境摘要），台词触发节点确定性（进入 Running 一次、按计划长度均匀选择 ≤6 个步骤完成节点、终态一次——末个选中步骤折入终止节点，每任务 ≤8 次；follow 仅开始/首次到达跟随距离/终止三节点），共享全服 4 模型槽（Planner tick 边界重试、Dialogue 无槽即跳过不排队）、每伙伴 1 在途、世代过时即丢、模型失败只跳过台词绝不改任务状态；终态响应捎带 ≤2 KiB 最近对话摘要（失败保留旧摘要、只喂后续 Dialogue，persona 与摘要绝不进入 Planner）；协议 v19 追加 `CompanionSpeech`（伙伴身份 + ≤256 bytes 台词、广播全部在线玩家，任务事件仍不携带模型自由文本），客户端以 `伙伴名：台词原文` 一行呈现（唯一显示模型文本的位置）；`companions.ai` schema v4 每记录可选摘要区（文件上界 438,280 bytes、v3/v2/v1 只读迁移、inactive 不存摘要、解码 payload 读空门禁）。既有能力还包括权威快捷栏、持久掉落物、固定 36 格背包、固定合成、确定性矿石、多人共享权威熔炉、权威计时采掘、权威单件原地丢弃、服务端权威工具耐久、损坏物品、共享箱子、权威生命值与死亡结算和确认伤害红色边缘反馈、14 种常见块状材料与缺失玩家一次性材料包、世界坐标 terrain UV、玻璃/树叶单 pass alpha cutout、无窗口 `materials-showcase` 与 `ai-companion`、确定性自然材料与橡树生成、材料加工闭环、目标方块反馈、发光块固定配方、24000 tick 权威昼夜、客户端派生的 `0..15` 传播天空光与静态方块光，以及程序化方块云。M5A 继承 M4Q 的 Mornlea 项目身份和 M4P 固定 Rust 1.97.1 `mornlea_engine` cdylib；Rust 是 mesh/light、collision resolver、raycast、物理 tick 积分与世界生成（地形/矿石/橡树，engine ABI v3）的唯一生产实现，客户端窗口、事件循环与全部 GPU 渲染（terrain/sky/云/culling/HiZ/实体/文本/HUD，窗口 surface 与离屏）由独立 `mornlea_client` cdylib（winit + wgpu，client ABI v4）独占生产、Go 已无 GLFW 与 WebGPU 依赖，`internal/gfx` 已删除；Go 仍拥有 app、world、sim、network、storage、render 的 CPU 半部（布局、编码、上传调度）、物理 state/input/tunable/snapshot 编码、yaw 三角与 prism 构建、worldgen seed→perm 播种与 `world.Chunk` 回写，旧 Go 积分与 worldgen 实现只作测试 oracle，只有 `internal/nativeabi` 接触 engine C ABI，`internal/mesh` 与 `internal/physics` 是领域调用方，且没有生产 Go fallback。客户端命令为 `mornlea`，专用服务端为 `mornlea-server`；专服保持无图形，但 Linux 发布必须把相邻 `libmornlea_engine.so` 作为同一不可跨版本混装的 release unit。benchmark scenario 为 v16；当前唯一显式场景迁移是 `15:16`，性能数值只记录，报告完整性、身份、真实 overflow、数据丢失和 I/O 错误仍是门禁。M2 v15 与 M5 v14 基线保持原字节。TCP 仅面向可信局域网且没有认证或加密。已交付里程碑与协议/存档版本演进见 `docs/notes/progress.md`；`docs/superpowers/` 是历史背景，当前行为必须以代码、测试与 OpenSpec 主规格核实。

Raycast 生产路径同样由 Rust `mornlea_engine` 独占 DDA 遍历；`internal/core` 只保留输入校验、一次归一化、64-record batch 驱动、惰性 callback 与 `RayHit.Point`，旧 Go DDA 只是测试 oracle，没有生产 fallback。

## 开始工作前

1. 阅读 `openspec/config.yaml`。
2. 若任务属于某个 OpenSpec change，依次阅读该 change 的 `proposal.md`、delta specs、`design.md` 和 `tasks.md`。
3. 只读取 `docs/superpowers/` 中与当前变更直接相关的资料，再用代码和测试核实现状。
4. 检查 `git status`，保留用户已有及与任务无关的改动。
5. clean checkout 先运行相应 Make Rust target，再执行直接的 focused Go 命令。

复杂功能、新模块、跨包重构、存档/协议变更和性能契约变更应走 OpenSpec。小型拼写修复、纯格式修改和一次性实验可以直接修改，但仍须完成相称的验证。具体流程见 `docs/openspec.md`。

## 架构边界

- 服务端是世界和玩家状态的唯一权威；客户端持有只读镜像并进行预测/呈现。
- 单机模式和远程模式必须复用同一套模拟与校验逻辑，不能为单机绕过传输边界。
- 内部包的允许依赖以 `internal/archcheck/dependency_test.go` 为准；新增包必须同步登记并证明依赖方向合理。
- GPU 只经 Rust `mornlea_client` 渲染器;任何 Go 包不得导入 WebGPU 绑定(archcheck 全仓禁止)。
- `sim` 不得依赖渲染包，`world` 不得依赖 `network`。
- 跨 goroutine 发送成功后的消息及其切片视为不可变；重 CPU、磁盘和网络工作不得阻塞权威 tick 或渲染热路径。
- 不得放宽既有正确性、资源上限、报告完整性、真实 overflow 或数据丢失门禁来让测试通过；benchmark 与 `perfcheck` 的性能数值只保存记录，不改变退出状态。
- 仓库不得加入 Mojang 版权材质或其他未经授权的二进制美术资源。

## 实现约定

- 修改保持聚焦，优先复用现有抽象，不顺手重构无关代码。
- 新增或修改行为时先写失败测试，再完成最小实现和重构。
- Go 代码必须经过 `gofmt`；错误要保留上下文，生命周期资源必须可关闭且关闭应安全。
- 代码注释、GoDoc 和 Rust doc comment 说明必须使用中文；Go/Rust 标识符、wire magic、协议字段名、外部 API 名称和约定俗成的技术术语保留英文。
- 注释必须丰富且有信息量：Go 的每个导出标识符都应有中文 GoDoc，Rust 的每个导出项都应有中文 doc comment（`///`）；关键算法、复杂逻辑、边界条件、并发约束、unsafe/FFI 边界、magic 数值和"为什么这么做"的设计决策必须配中文注释。注释应解释意图与权衡，而不是机械复述代码。
- 文档、测试说明和开发者可读文本优先使用中文；Go 标识符、wire magic 和约定俗成的技术术语保留英文。
- 涉及协议、存档格式或基准输出的变更必须说明兼容性与迁移策略，并保留 golden/fuzz/故障注入覆盖。
- 自动测试不得启动或聚焦前台游戏窗口；只有用户明确要求人工验收时才运行交互式客户端。
- `.codex/hooks.json` 与 `.claude/settings.json` 共用 `scripts/agent-hooks/guard.mjs`。Hook 失败时修复根因；不得关闭、改写或用 `MORNLEA_HOOKS_ALLOW_NO_SPEC=1` 绕过，除非用户明确批准例外。

## 验证

按风险从小到大执行：

```bash
make rust
go test ./path/to/affected/package -race -count=1
go test ./internal/archcheck -count=1
go test ./... -race
go vet ./...
gofmt -l .
```

`gofmt -l .` 应无输出。渲染、tick、存储或协议热路径发生变化时，还要运行对应 benchmark、fuzz/golden 测试或 `cmd/perfcheck`，其性能数值只记录；报告完整性、真实 overflow 和数据丢失仍是门禁。

OpenSpec 产物提交前执行：

```bash
openspec validate --all --strict --no-interactive
```

## OpenSpec 纪律

- `spec.md` 只描述可观察行为和验收场景；实现选择放在 `design.md`；执行顺序放在 `tasks.md`。
- 代码实现与计划不一致时，先更新 change 产物，不能只改代码让规格失真。
- 归档前完成验证并核对所有任务；归档的意义是把稳定行为沉淀到 `openspec/specs/`，不是简单清理目录。
- 旧的 `docs/superpowers/` 保留为历史背景，不批量迁移；后续主规格通过真实 change 的归档逐步形成。

## 自动 Hook 门禁

- `PreToolUse` 阻止高破坏性的 Git、强制推送和宽范围递归删除命令。
- `PostToolUse` 在文件编辑后检查本次改动中的 Go 文件是否已经 `gofmt`。
- `Stop` 检查 diff、OpenSpec、架构依赖、受影响包的 race 测试和 `go vet`；协议、存档、性能基线、依赖边界或跨组件实现改动必须关联完整 active change。
- Hook 是机械护栏而非安全沙箱；CI 仍是最终共享门禁，人工评审仍负责语义正确性。
