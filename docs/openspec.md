# OpenSpec 工作流

本项目使用 OpenSpec 管理复杂变更的需求边界、行为规格、技术设计和实施任务。核心原则是：先让人和 AI 对“做什么、如何验收”达成一致，再编码；完成后把稳定行为归档为可复用的项目上下文。

## 初始化结果

仓库使用 OpenSpec `spec-driven` schema 和 core profile，并为两种 AI 工具生成了入口：

- Claude Code：`.claude/commands/opsx/` 与 `.claude/skills/openspec-*`
- Codex：`.codex/skills/openspec-*`
- 项目上下文与产物规则：`openspec/config.yaml`
- 项目常驻规则：`AGENTS.md`
- CI 硬门禁：`openspec validate --all --strict --no-interactive`

首次使用前需要 Node.js 20.19 或更高版本，并安装 CLI：

```bash
npm install -g @fission-ai/openspec@1.7.0
openspec --version
```

升级 OpenSpec 时，应同时更新 CI 中的固定版本，并运行 `openspec update` 重新生成 Claude Code/Codex 集成文件。

## 何时使用

以下变更默认使用 OpenSpec：

- 新里程碑、新游戏系统或新业务能力
- 跨多个内部包的功能或重构
- 网络协议、存档格式、并发模型或资源所有权变化
- 会影响兼容性、性能阈值或架构边界的改动
- 预计持续两天以上、需要多人或多个 AI 会话协作的需求

拼写修复、纯格式调整、一次性验证脚本等低风险改动可以直接完成。不要为了形式给低信息密度任务制造整套产物。

## 标准流程

### 1. 探索

需求或现状不清楚时，先让 AI 读取相关代码、测试和历史设计：

```text
Claude Code: /opsx:explore
Codex:       $openspec-explore
```

`docs/superpowers/specs/` 和 `docs/superpowers/plans/` 是背景材料，不等于当前实现。只引用当前变更涉及的章节，并以代码和测试核实。

### 2. 提案

用一个清晰、聚焦的需求生成 proposal、delta specs、design 和 tasks：

```text
Claude Code: /opsx:propose add-example-feature
Codex:       $openspec-propose add-example-feature
```

这些指令输入在 AI 对话中，不是在 shell 中执行。终端中的 `openspec` CLI 用于查看、校验和维护项目状态。

编码前由人检查：

- 目标、非目标和改动范围是否准确
- Requirement 与 Scenario 是否可观察、可判定
- 架构、数据所有权、兼容性与性能影响是否明确
- tasks 是否可逐项提交、验证和回退

产物都是普通 Markdown，可以直接修改。不要把未经评审的首稿当成批准结果。

### 3. 实现

```text
Claude Code: /opsx:apply
Codex:       $openspec-apply-change
```

实现阶段遵循 `tasks.md`，每个行为先写失败测试，再完成最小实现。只有验证通过后才勾选任务。

实现执行必须采用 `subagent-driven-development` 规范：每个任务派发独立的 implementer 子代理并以任务 brief 为唯一需求来源；任务完成后经规格合规与代码质量双裁决的任务评审，评审发现进入修复循环（单任务最多 5 轮）；全部任务完成后进行整分支终审。执行进度、评审结论与裁决记录在 ledger；控制会话只做协调与裁决，不直接写实现。

如果开发中发现需求、边界或技术方案变化，先修改对应的 proposal/spec/design/tasks，再继续实现。长时间运行的 change 若需要提前把 delta 合入主规格，可使用：

```text
Claude Code: /opsx:sync
Codex:       $openspec-sync-specs
```

通常归档时会完成同步，不需要在每次代码修改后运行 sync。

### 4. 校验与归档

完成代码和项目测试后运行：

```bash
openspec status --change <change-name>
openspec validate --all --strict --no-interactive
```

确认实现与规格一致、tasks 已完成后，在 AI 对话中归档：

```text
Claude Code: /opsx:archive
Codex:       $openspec-archive-change
```

归档会把 delta 合入 `openspec/specs/<capability>/spec.md`，并把完整 change 移到 `openspec/changes/archive/`。主规格由真实变更逐步增长，不对现有代码库做一次性全量补写。

## 日常终端命令

```bash
openspec list
openspec list --specs
openspec status --change <change-name>
openspec show <change-name>
openspec validate --all --strict --no-interactive
openspec doctor
```

OpenSpec 产物、归档和生成的项目级 AI 集成文件都应与代码一起进入版本控制。

## 自动 Hook 约束

Claude Code 与 Codex 使用同一个实现：`scripts/agent-hooks/guard.mjs`。配置分别位于 `.claude/settings.json` 和 `.codex/hooks.json`。

| 生命周期 | 自动约束 |
|---|---|
| `PreToolUse` | 拦截 `git reset --hard`、强制 `git clean`、强制推送，以及针对 `/`、仓库根目录或主目录的递归强制删除 |
| `PostToolUse` | 编辑文件后检查所有改动中的 Go 文件是否已执行 `gofmt` |
| `Stop` | 执行 `git diff --check`；需要时校验 OpenSpec、架构依赖、受影响包 race 测试和 `go vet` |

以下改动在停止前必须存在包含 proposal、delta specs 和 tasks 的 active OpenSpec change：

- 协议、存档格式、golden、性能基线或架构依赖门禁变化
- 同时跨越两个及以上 `internal/*` 或 `cmd/*` 实现组件的 Go 修改

首次加载项目 Hook：

- Codex：打开 `/hooks`，检查来源和命令后信任当前 `.codex/hooks.json`；文件内容变化后需要重新审查。
- Claude Code：打开 `/hooks`，确认 Project 来源的三类 Hook 已加载。

只有用户明确批准某次无 Spec 例外时，才能在启动 AI 工具前设置 `MORNLEA_HOOKS_ALLOW_NO_SPEC=1`。该变量只跳过 OpenSpec change 要求，不会关闭破坏性命令或质量检查。

修改 Hook 策略后运行：

```bash
node --test scripts/agent-hooks/guard.test.mjs
```
