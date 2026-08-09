# minecraft-go 项目指南

## 项目定位

本仓库是 Go 1.26 编写的独立体素游戏，包含自研客户端、权威服务端、世界存储、物理和 WebGPU 渲染。它不追求兼容官方 Minecraft 的协议、存档或版权资源。

当前代码基线是 M4N，已经包含协议 v14、Memory/TCP 共用登录与权威模拟、TCP 直连、无图形专用服务端、稳定玩家身份、玩家 schema v5 与区块 schema v7 存档、世界 metadata v2、最多八名玩家的局域网同步与远端玩家呈现、权威快捷栏、持久掉落物、固定 36 格背包、固定合成、确定性矿石、多人共享权威熔炉、权威计时采掘、权威单件原地丢弃、服务端权威工具耐久、损坏物品、共享箱子、权威生命值与死亡结算，以及由服务端权威推进的 24000 tick 昼夜和客户端从权威方块镜像派生的 `0..15` 传播天空光与静态方块光。发光块可放置、可用石镐或铁镐挖回，但没有配方、初始发放、世界生成或管理命令等正常获取入口。benchmark scenario 为 v15；M2 当前基线是 Memory v15，TCP v15 独立记录，M2 v6 保留历史身份，M5 当前基线仍为 v14 并等待未来同硬件迁移。TCP 仅面向可信局域网且没有认证或加密。`docs/superpowers/` 同时保存了已实现里程碑和未来里程碑的设计/计划；文档写了某项能力不代表代码已经实现，判断现状时必须检查代码和测试。

## 开始工作前

1. 阅读 `openspec/config.yaml`。
2. 若任务属于某个 OpenSpec change，依次阅读该 change 的 `proposal.md`、delta specs、`design.md` 和 `tasks.md`。
3. 只读取 `docs/superpowers/` 中与当前变更直接相关的资料，再用代码和测试核实现状。
4. 检查 `git status`，保留用户已有及与任务无关的改动。

复杂功能、新模块、跨包重构、存档/协议变更和性能契约变更应走 OpenSpec。小型拼写修复、纯格式修改和一次性实验可以直接修改，但仍须完成相称的验证。具体流程见 `docs/openspec.md`。

## 架构边界

- 服务端是世界和玩家状态的唯一权威；客户端持有只读镜像并进行预测/呈现。
- 单机模式和远程模式必须复用同一套模拟与校验逻辑，不能为单机绕过传输边界。
- 内部包的允许依赖以 `internal/archcheck/deps_test.go` 为准；新增包必须同步登记并证明依赖方向合理。
- 只有 `internal/gfx` 可以直接导入 WebGPU 绑定。
- `sim` 不得依赖渲染包，`world` 不得依赖 `network`。
- 跨 goroutine 发送成功后的消息及其切片视为不可变；重 CPU、磁盘和网络工作不得阻塞权威 tick 或渲染热路径。
- 不得放宽既有正确性、资源上限、报告完整性、真实 overflow 或数据丢失门禁来让测试通过；benchmark 与 `perfcheck` 的性能数值只保存记录，不改变退出状态。
- 仓库不得加入 Mojang 版权材质或其他未经授权的二进制美术资源。

## 实现约定

- 修改保持聚焦，优先复用现有抽象，不顺手重构无关代码。
- 新增或修改行为时先写失败测试，再完成最小实现和重构。
- Go 代码必须经过 `gofmt`；错误要保留上下文，生命周期资源必须可关闭且关闭应安全。
- 代码注释和 GoDoc 说明必须使用中文；Go 标识符、wire magic、协议字段名、外部 API 名称和约定俗成的技术术语保留英文。
- 文档、测试说明和开发者可读文本优先使用中文；Go 标识符、wire magic 和约定俗成的技术术语保留英文。
- 涉及协议、存档格式或基准输出的变更必须说明兼容性与迁移策略，并保留 golden/fuzz/故障注入覆盖。
- 自动测试不得启动或聚焦前台游戏窗口；只有用户明确要求人工验收时才运行交互式客户端。
- `.codex/hooks.json` 与 `.claude/settings.json` 共用 `scripts/agent-hooks/guard.mjs`。Hook 失败时修复根因；不得关闭、改写或用 `MINECRAFT_GO_HOOKS_ALLOW_NO_SPEC=1` 绕过，除非用户明确批准例外。

## 验证

按风险从小到大执行：

```bash
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
