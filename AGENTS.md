<!-- gitnexus:start -->
# GitNexus — Code Intelligence

This project is indexed by GitNexus as **minecraft-go** (9099 symbols, 23545 relationships, 300 execution flows). Use the GitNexus MCP tools to understand code, assess impact, and navigate safely.

> Index stale? Run `node .gitnexus/run.cjs analyze` from the project root — it auto-selects an available runner. No `.gitnexus/run.cjs` yet? `npx gitnexus analyze` (npm 11 crash → `npm i -g gitnexus`; #1939).

## Always Do

- **MUST run impact analysis before editing any symbol.** Before modifying a function, class, or method, run `impact({target: "symbolName", direction: "upstream"})` and report the blast radius (direct callers, affected processes, risk level) to the user.
- **MUST run `detect_changes()` before committing** to verify your changes only affect expected symbols and execution flows. For regression review, compare against the default branch: `detect_changes({scope: "compare", base_ref: "main"})`.
- **MUST warn the user** if impact analysis returns HIGH or CRITICAL risk before proceeding with edits.
- When exploring unfamiliar code, use `query({search_query: "concept"})` to find execution flows instead of grepping. It returns process-grouped results ranked by relevance.
- When you need full context on a specific symbol — callers, callees, which execution flows it participates in — use `context({name: "symbolName"})`.
- For security review, `explain({target: "fileOrSymbol"})` lists taint findings (source→sink flows; needs `analyze --pdg`).

## Never Do

- NEVER edit a function, class, or method without first running `impact` on it.
- NEVER ignore HIGH or CRITICAL risk warnings from impact analysis.
- NEVER rename symbols with find-and-replace — use `rename` which understands the call graph.
- NEVER commit changes without running `detect_changes()` to check affected scope.

## Resources

| Resource | Use for |
|----------|---------|
| `gitnexus://repo/minecraft-go/context` | Codebase overview, check index freshness |
| `gitnexus://repo/minecraft-go/clusters` | All functional areas |
| `gitnexus://repo/minecraft-go/processes` | All execution flows |
| `gitnexus://repo/minecraft-go/process/{name}` | Step-by-step execution trace |

## CLI

| Task | Read this skill file |
|------|---------------------|
| Understand architecture / "How does X work?" | `.claude/skills/gitnexus/gitnexus-exploring/SKILL.md` |
| Blast radius / "What breaks if I change X?" | `.claude/skills/gitnexus/gitnexus-impact-analysis/SKILL.md` |
| Trace bugs / "Why is X failing?" | `.claude/skills/gitnexus/gitnexus-debugging/SKILL.md` |
| Rename / extract / split / refactor | `.claude/skills/gitnexus/gitnexus-refactoring/SKILL.md` |
| Tools, resources, schema reference | `.claude/skills/gitnexus/gitnexus-guide/SKILL.md` |
| Index, status, clean, wiki CLI commands | `.claude/skills/gitnexus/gitnexus-cli/SKILL.md` |

<!-- gitnexus:end -->

# minecraft-go 项目指南

## 项目定位

本仓库是 Go 1.26 编写的独立体素游戏，包含自研客户端、权威服务端、世界存储、物理和 WebGPU 渲染。它不追求兼容官方 Minecraft 的协议、存档或版权资源。

当前代码基线是 M3C，已经包含有界版本化二进制协议、Memory/TCP 共用登录状态机、TCP 直连、无图形专用服务端、稳定玩家身份和玩家状态存档，以及最多八名玩家的局域网同步与远端玩家呈现。TCP 仅面向可信局域网且没有认证或加密。`docs/superpowers/` 同时保存了已实现里程碑和未来里程碑的设计/计划；文档写了某项能力不代表代码已经实现，判断现状时必须检查代码和测试。

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
- 不得放宽既有正确性、资源上限或性能门禁来让测试通过。
- 仓库不得加入 Mojang 版权材质或其他未经授权的二进制美术资源。

## 实现约定

- 修改保持聚焦，优先复用现有抽象，不顺手重构无关代码。
- 新增或修改行为时先写失败测试，再完成最小实现和重构。
- Go 代码必须经过 `gofmt`；错误要保留上下文，生命周期资源必须可关闭且关闭应安全。
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

`gofmt -l .` 应无输出。渲染、tick、存储或协议热路径发生变化时，还要运行对应 benchmark、fuzz/golden 测试或 `cmd/perfcheck` 门禁。

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
