# Mornlea 项目指南

## 项目定位

本仓库是 Go 1.26 编写的独立体素游戏 Mornlea，Go module 为 `github.com/channing771/mornlea`，包含自研客户端、权威服务端、世界存储、物理和 WebGPU 渲染。它不追求兼容官方 Minecraft 的协议、存档或版权资源。

当前代码基线是 M5A，已经包含协议 v16、Memory/TCP 共用登录与权威模拟、TCP 直连、无图形专用服务端、稳定玩家身份、玩家 schema v6、区块 schema v8、世界 metadata v2、独立 `companions.ai` schema v1、最多八名玩家的局域网同步与远端玩家呈现，以及最多四个可配置、服务端权威且保持 idle 的具名伙伴。M5A 伙伴使用独立 `CompanionID`，active 与 inactive 身体记录合计最多 64 条；客户端通过统一 Avatar/NameTag pass 呈现伙伴，并提供有界 Unicode 聊天输入与 HUD。`@伙伴名 指令` 只在权威 tick 边界产生确定性寻址事实，不创建 Planner、HTTP 请求、FIFO、路径、采掘、放置、跟随、persona 或摘要。既有能力还包括权威快捷栏、持久掉落物、固定 36 格背包、固定合成、确定性矿石、多人共享权威熔炉、权威计时采掘、权威单件原地丢弃、服务端权威工具耐久、损坏物品、共享箱子、权威生命值与死亡结算和确认伤害红色边缘反馈、14 种常见块状材料与缺失玩家一次性材料包、世界坐标 terrain UV、玻璃/树叶单 pass alpha cutout、无窗口 `materials-showcase` 与 `ai-companion`、确定性自然材料与橡树生成、材料加工闭环、目标方块反馈、发光块固定配方、24000 tick 权威昼夜、客户端派生的 `0..15` 传播天空光与静态方块光，以及程序化方块云。M5A 继承 M4Q 的 Mornlea 项目身份和 M4P 固定 Rust 1.97.1 `mornlea_engine` cdylib；Rust 是 mesh/light、collision resolver、raycast 与物理 tick 积分的唯一生产实现，Go 仍拥有 app、world、sim、network、storage、render、物理 state/input/tunable/snapshot 编码、yaw 三角与 prism 构建，旧 Go 积分只作测试 oracle，只有 `internal/nativeabi` 接触 engine C ABI，`internal/mesh` 与 `internal/physics` 是领域调用方，且没有生产 Go fallback。客户端命令为 `mornlea`，专用服务端为 `mornlea-server`；专服保持无图形，但 Linux 发布必须把相邻 `libmornlea_engine.so` 作为同一不可跨版本混装的 release unit。benchmark scenario 为 v16；当前唯一显式场景迁移是 `15:16`，性能数值只记录，报告完整性、身份、真实 overflow、数据丢失和 I/O 错误仍是门禁。M2 v15 与 M5 v14 基线保持原字节。TCP 仅面向可信局域网且没有认证或加密。已交付里程碑与协议/存档版本演进见 `docs/notes/progress.md`；`docs/superpowers/` 是历史背景，当前行为必须以代码、测试与 OpenSpec 主规格核实。

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
- 只有 `internal/gfx` 可以直接导入 WebGPU 绑定。
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
