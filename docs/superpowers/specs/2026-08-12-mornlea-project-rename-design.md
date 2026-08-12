# Mornlea 项目改名设计

日期：2026-08-12

## 1. 背景

项目当前以 `minecraft-go`、`mcgo` 和 `mcgod` 作为仓库、Go module、命令与本机数据目录名称。这些名称容易让人误以为项目兼容或隶属于官方 Minecraft；实际项目是拥有自研客户端、权威服务端、存储、物理、WebGPU 渲染和 Rust mesh/light 引擎的独立体素游戏。

项目正式品牌改为 **Mornlea**。名称由 `morn` 与 `lea` 组合，表达晨光下的草野。本次变更同时收敛对外品牌和内部技术身份，但不借改名修改玩法、协议、存档格式、性能工作负载或视觉结果。

实现以 M4P Rust mesh/light 最终提交 `4c77539280d7e093001c2c9f3fc02da513fd3715` 为功能基础。撰写本设计时最新 `main` 为 `dfdc71b`，另含程序化方块云。两条分支从 `2346ded` 分叉；正式改名前先合并并验证两边能力，再开始身份切换。

## 2. 已确认决策

- 产品名为 `Mornlea`。
- GitHub 仓库最终为 `channing771/mornlea`。
- Go module 最终为 `github.com/channing771/mornlea`。
- 图形客户端命令和产物为 `mornlea`。
- 无图形专用服务端命令和产物为 `mornlea-server`。
- 不保留 `mcgo`、`mcgod` 或旧环境变量的兼容包装器。
- 新默认用户目录为 `mornlea`；新文件不存在时从旧 `minecraft-go` 目录安全复制配置和玩家档案。
- 历史设计、归档 OpenSpec 和性能证据保留原文；当前代码、当前文档和开发入口统一使用新名。
- 协议版本、存档 schema、二进制 magic、benchmark scenario、golden 与性能 baseline 不因改名变化。
- 采用一个 OpenSpec change、一个 PR、多个可独立验证的任务提交。

## 3. 目标与非目标

### 3.1 目标

- 让游戏、仓库、module、命令、Rust ABI、构建入口和当前文档共享同一 `Mornlea` 身份。
- 保持已有玩家 PlayerID、显示名和本机配置连续可用。
- 保持客户端、内置服务端、TCP 专用服务端、Rust mesh/light、程序化方块云和现有自动化行为不变。
- 让改名后的仓库可从干净 checkout 构建、测试、打包和运行。
- 保留旧目录与历史证据，使源码和用户数据都可回退。

### 3.2 非目标

- 不改变游戏玩法、图像、材质、场景或 UI 布局。
- 不改变协议 v15、区块 schema v8、玩家 schema v6、metadata v2 或 benchmark scenario v15。
- 不改变 `CHNK`、`MCGC`、`MCGM`、`MCPL`、`MCGR`、`MCGB` 等协议或存档身份。
- 不批量改写 `docs/superpowers/**`、已归档 OpenSpec、历史性能记录或提交历史。
- 不保留旧命令 wrapper、symlink、Go module alias 或双 module 支持。
- 不自动删除旧用户目录、新用户目录、世界或任何玩家数据。
- 不在源码 change 中自动执行 GitHub 仓库改名、本地根目录移动或 GitNexus 索引重建。

## 4. 基线与变更边界

### 4.1 功能基线

从 M4P 最终提交建立改名分支，第一项工作只合并最新 `main`。合并提交不得夹带品牌替换。合并后先验证 Rust mesh/light 与程序化方块云同时成立，再冻结为 M4Q 的实现基线。

预检未发现 M4P 与 `dfdc71b` 的文本冲突，但这不替代语义验证。若合并时出现新主线提交或冲突，必须逐声明保留双方功能，不得用整文件偏向任一侧。

### 4.2 OpenSpec 生命周期

M4O 和 M4P 均已完成任务，但在 M4P 工作树中仍是 active change。合并基线并完成各自最终验证后，先按 OpenSpec 归档流程沉淀它们，再创建独立 change：

```text
m4q-mornlea-project-rename
```

M4Q 只描述项目身份与数据路径迁移，不重开 M4O/M4P 的实现任务。历史 change 中出现的旧路径和旧命令是当时真实证据，保持原文。

## 5. 最终命名映射

| 当前身份 | 最终身份 |
|---|---|
| `minecraft-go` | `Mornlea`（叙述）/ `mornlea`（机器标识） |
| `module minecraft-go` | `module github.com/channing771/mornlea` |
| `minecraft-go/internal/...` | `github.com/channing771/mornlea/internal/...` |
| `cmd/mcgo` | `cmd/mornlea` |
| `cmd/mcgod` | `cmd/mornlea-server` |
| `bin/mcgo` | `bin/mornlea` |
| `mcgo` | `mornlea` |
| `mcgod` | `mornlea-server` |
| `mcgo_mesh` crate | `mornlea_mesh` crate |
| `libmcgo_mesh.dylib` | `libmornlea_mesh.dylib` |
| `mcgo_engine.h` | `mornlea_engine.h` |
| `mcgo_engine_abi_version` | `mornlea_engine_abi_version` |
| `mcgo_mesh_section` | `mornlea_mesh_section` |
| `MCGO_ENGINE_*` / `MCGO_STATUS_*` | `MORNLEA_ENGINE_*` / `MORNLEA_STATUS_*` |
| `MINECRAFT_GO_*` / 测试专用 `MCGO_*` | 对应 `MORNLEA_*` |
| 用户目录 `minecraft-go` | 用户目录 `mornlea` |

Go 内部变量或 test fake 中以 `mcgo`/`mcgod` 为前缀的标识符也应机械改为新名，避免当前源码继续传播旧身份。协议与存档 magic 不属于品牌标识，必须保持不变；扫描门禁需带精确白名单，不能用“仓库中不允许出现 `MCG`”这类过宽规则。

## 6. 用户数据迁移

### 6.1 路径与优先级

新默认路径为：

```text
os.UserConfigDir()/mornlea/config.json
os.UserConfigDir()/mornlea/profile.json
```

每个文件独立按以下顺序处理：

1. 新文件存在：只读取并校验新文件，不读取旧文件。
2. 新文件不存在且旧文件存在：读取并用现有解码器完整校验旧文件，再安全复制到新路径，最后从新路径读取。
3. 新旧文件都不存在：`config` 继续返回编译默认值且不创建文件；`profile` 按现有规则生成新 UUIDv4 并保存。

新文件一旦存在即为唯一权威来源。其内容非法、权限不安全或读取失败时必须终止启动，不得回退旧文件。旧文件非法或迁移失败时也必须终止启动，不得静默创建新档案；否则玩家会以新 PlayerID 登录，表现为位置、背包和其他权威玩家状态丢失。

### 6.2 安全发布

迁移复用现有编码器和私有目录约束，不引入通用迁移框架。`config` 与 `profile` 各自在所属包内完成最小逻辑：

- 校验旧内容后，创建权限为 `0700` 的新父目录；既有父目录权限不安全时失败。
- 在新目录内创建权限为 `0600` 的临时文件，写入完整内容并 `Sync`。
- 使用不覆盖的原子发布操作把临时文件发布为目标文件。
- 若并发进程已经发布目标文件，当前进程删除自己的临时文件，再读取并校验已发布的新文件；不得覆盖它。
- 成功后记录一条包含旧、新路径的结构化日志。
- 任一失败路径清理自身临时文件，但不修改旧文件或已经发布的新文件。

这里复制经过验证的规范化内容，而不是移动旧文件。旧 `minecraft-go` 目录始终保留，旧版本仍可直接使用。

### 6.3 显式配置路径

`--config <path>` 表示用户显式选择文件，完全跳过默认配置迁移。专用服务端与客户端遵循同一规则。玩家档案当前没有显式 CLI 路径，始终使用上述默认迁移流程。

配置和 profile schema 继续是 v1；路径变化不构成 schema 升级。

## 7. 代码、构建与文档切换

### 7.1 原子代码身份切换

Go module、全仓 import、两个 `cmd` 目录、Rust crate/ABI、Makefile、CI、Hook、架构守卫和当前测试必须在同一任务提交中切换，使每个提交末端都能构建。命令目录整体移动，包括测试与 `testdata/golden`；PNG 只移动路径，字节不得改变。

`internal/archcheck` 中硬编码的 module 前缀、命令目录、依赖白名单和源码扫描目标同步更新。`mornlea-server` 继续满足 Linux `CGO_ENABLED=0`、无客户端、mesh、render、gfx、GLFW、WebGPU 和字体依赖。

M4P 的动态库打包同步改名。`make build` 必须生成同目录的：

```text
bin/mornlea
bin/libmornlea_mesh.dylib
```

动态库 install name 继续是 `@rpath/libmornlea_mesh.dylib`，可执行文件继续带 `LC_RPATH @loader_path`；移开 `engine/target` 后仍能从相邻 dylib 启动到 Go 参数解析。

### 7.2 当前文档

以下当前身份来源同步更新：

- `README.md`、`AGENTS.md`、`CLAUDE.md`；
- `openspec/config.yaml`；
- 仍生效的主规格，例如自然材料迁移命令；
- `.github/workflows/ci.yml`、`Makefile`、`.gitignore`；
- `.codex/hooks.json`、`.claude/settings.json` 共用的 Hook 文本与路由；
- `docs/notes/lan-server.md` 等描述当前使用方法的文档。

新增一份简短迁移说明，列出新旧品牌、命令、module、用户路径、环境变量及“旧数据只复制不删除”的行为。

### 7.3 历史证据

以下内容默认保持原文：

- `docs/superpowers/**` 中既有设计与计划；
- `openspec/changes/archive/**`；
- 已完成并归档的 M4O/M4P change；
- 历史性能记录、旧命令输出、hash 与基线说明；
- Git 提交历史。

这些文件中的旧名称是历史事实。迁移说明在一个入口解释它们，不向每份历史文档插入重复提示。

## 8. 仓库外操作

源码 PR 合并并验证后，才执行仓库外改名：

1. 在 GitHub 把 `channing771/minecraft-go` 改为 `channing771/mornlea`。
2. 把 `origin` 与需要保留的 `fork` remote 更新到新 URL；平台旧 URL 重定向只是兜底。
3. 确认所有 linked worktree 已合并、保留或安全移除，再把本地根目录改为 `mornlea`。
4. 清理旧 GitNexus 索引并在新绝对路径重新分析。

这些步骤不由程序启动时自动执行，也不混入源码提交。任一步失败都可暂停在当前状态；不得为了目录改名破坏 linked worktree 元数据。

## 9. 错误处理与回退

- 合并 M4P 与最新 `main` 失败：保持可恢复 merge 状态，逐项解决；不开始品牌替换。
- 用户数据迁移失败：终止启动并保留旧文件、新文件和错误上下文；不生成替代 PlayerID。
- 代码身份切换失败：按任务提交回退；数据复制逻辑独立提交，已产生的新目录不自动删除。
- GitHub 仓库改名失败：代码仍可在旧仓库 URL 工作，可恢复仓库名并更新 remote。
- 本地目录或 GitNexus 操作失败：恢复原目录或重新索引，不影响源码与用户数据。

回退旧版本时，旧版本继续读取保留的 `minecraft-go` 目录。新版本写入 `mornlea` 后，两份数据不再自动双向同步；迁移说明必须明确这一点，避免用户交替运行新旧版本并误以为状态会合并。

## 10. 测试与验收

### 10.1 数据迁移测试

`internal/config` 与 `internal/profile` 至少覆盖：

- 新文件优先且旧文件不被读取；
- 仅旧文件存在时复制到新目录；
- profile 的 PlayerID、显示名和编码语义完全保真；
- 新文件非法、旧文件非法、父目录权限不安全、读写或发布失败时明确报错；
- 两个并发迁移者不覆盖目标，最终读取同一份有效文件；
- config 新旧都不存在时不创建文件；
- profile 新旧都不存在时只创建一份有效 UUIDv4 档案；
- 显式 `--config` 跳过默认迁移；
- 旧目录与旧文件在成功和失败路径都保持不变。

测试需杀死“新文件错误时回退旧文件”“复制失败后生成新 PlayerID”“覆盖并发发布结果”等危险变异。

### 10.2 身份与构建测试

- Go module 和当前源码 imports 仅使用 `github.com/channing771/mornlea`。
- `cmd/mornlea`、`cmd/mornlea-server`、Makefile 和 CI 使用新路径；旧命令目录不存在。
- Rust crate、header、C symbol、dylib 与打包探针使用 `mornlea` 身份。
- `mornlea-server` 继续通过 Linux 无 CGO 构建与依赖闭包检查。
- 非历史源码和当前文档不再出现旧 module、命令、品牌或开发环境变量；历史白名单必须是精确路径集合。
- `AGENTS.md` 与 `CLAUDE.md` 的共享内容保持一致。

### 10.3 不变量与全量门禁

改名前后必须冻结并比较：

- 协议版本和网络 fixture；
- chunk v8、player v6、metadata v2 fixture 与 magic；
- benchmark scenario v15 与 tracked baseline；
- 所有视觉 golden PNG；
- Rust mesh/light parity 与 dylib ABI 版本。

最终至少运行：

```bash
make rust-check
make build
make test-race
go test ./internal/archcheck -count=1
go vet ./...
gofmt -l .
cargo fmt --manifest-path engine/Cargo.toml -- --check
cargo clippy --manifest-path engine/Cargo.toml --workspace --all-targets -- -D warnings
openspec validate --all --strict --no-interactive
```

另运行两条已有 fuzz、非更新视觉校验、Rust dylib target-absent 搬运探针与相关 benchmark。性能数值只记录；报告身份、真实 overflow、数据丢失、I/O、fixture/hash 或视觉语义失败仍阻塞。

## 11. 实施分解

M4Q 采用一个 PR、按依赖顺序提交：

1. 合并 M4P 与最新 `main`，验证 Rust mesh/light 与程序化方块云共存；归档已完成 M4O/M4P。
2. 建立 M4Q OpenSpec proposal、delta specs、design 和 tasks。
3. TDD 实现 config/profile 默认路径迁移与失败语义。
4. 原子切换 Go module、imports、命令目录、Rust ABI、构建/CI/Hook 与架构守卫。
5. 更新当前文档并新增迁移说明，保留历史证据。
6. 运行不变量 hash、focused/full gates 与独立评审，完成 M4Q。
7. PR 合并后执行 GitHub、remote、本地目录和 GitNexus 的仓库外改名。

不并行修改同一工作树中的 module、cmd 目录和 Rust ABI；这些机械改名共享大量路径，串行执行更容易保持每个提交可构建。数据迁移、身份扫描和文档审查可由只读子代理并行复核。
