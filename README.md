# minecraft-go

`minecraft-go` 是一个使用 Go 编写的独立体素游戏实验项目。项目自研客户端、权威服务端、世界存储和 WebGPU 渲染管线，不追求兼容官方 Minecraft 的协议、存档或资源。

项目仍处于早期开发阶段。目前已经具备程序化地形、GPU 地形渲染、玩家移动与碰撞、客户端预测、方块挖掘与放置、内置权威服务端、世界持久化、有界二进制协议、TCP 直连、无图形专用服务端以及稳定玩家状态存档；已完成的 M3 多人里程碑支持最多八名玩家通过局域网专用服务端同步移动、角色和 Unicode 昵称；M4A 增加了服务端权威的固定 9 格快捷栏；M4B 把挖掘所得改为落在地面的持久掉落物，由玩家走近拾取，并随所属区块一起存档；M4C 增加固定 27 格背包，与快捷栏组成 36 格权威物品状态，可用 `E` 打开界面整理。

## 环境要求

- macOS；客户端入口目前使用 Darwin 构建约束，主要在 Apple Silicon 上验证；
- Go 1.26；
- 可用的 CGO 与 C 编译工具链，macOS 可通过 Xcode Command Line Tools 提供；
- Make。

如本机尚未安装命令行开发工具，可执行：

```bash
xcode-select --install
```

## 快速开始

```bash
git clone https://github.com/channing771/minecraft-go.git
cd minecraft-go
make run
```

首次启动需要生成并加载视距内的地形，耗时会明显长于后续运行。默认世界保存在 `worlds/default`。

使用独立存档目录启动：

```bash
make run ARGS="--world worlds/demo"
```

也可以先构建再运行：

```bash
make build
./bin/mcgo --world worlds/default
```

## 常用命令

| 命令 | 说明 |
| --- | --- |
| `make help` | 显示 Makefile 帮助，也是默认目标 |
| `make run` | 运行客户端，可使用 `ARGS` 传递命令行参数 |
| `make build` | 构建客户端到 `bin/mcgo` |
| `make test` | 运行全部 Go 测试 |
| `make fmt` | 使用 `gofmt` 格式化仓库内的 Go 源码 |
| `make clean` | 删除 `bin` 目录，不会删除世界存档 |

## 操作方式

| 输入 | 操作 |
| --- | --- |
| `W` / `A` / `S` / `D` | 移动 |
| 空格 | 跳跃 |
| 移动鼠标 | 转动视角 |
| 鼠标左键 | 挖掘方块，在原地留下掉落物 |
| 鼠标右键 | 用当前选中栏位的物品放置方块 |
| `1` … `9` | 选择快捷栏栏位，由服务端确认后生效 |
| `E` | 打开/关闭背包界面；打开时释放鼠标并暂停移动、挖掘与放置 |
| 背包界面内左键 | 两次点击选择来源和目标，整堆移动 |
| `Esc` | 释放鼠标指针 |
| 释放指针后单击窗口 | 重新捕获鼠标指针 |

关闭游戏窗口时，内置服务端会停止并刷新待保存的世界数据。运行时生成的世界目录已在 `.gitignore` 中排除。

## 局域网多人联机（M3 已完成）

专用服务端、两个客户端的人工验收命令、身份语义、断线/关服存档行为与安全边界见[局域网专用服务端指南](docs/notes/lan-server.md)。最简启动方式：

```bash
go run ./cmd/mcgod --listen :25565 --world worlds/lan --seed 42 --max-players 8
go run ./cmd/mcgo --connect 127.0.0.1:25565 --name 玩家甲
```

## 项目结构

```text
.
├── cmd/
│   ├── mcgo/          游戏客户端与内置服务端装配
│   ├── mcgod/         无图形 TCP 专用服务端
│   ├── gfxspike/      WebGPU 地形渲染验证程序
│   └── perfcheck/     性能报告比较工具
├── internal/
│   ├── core/          坐标、几何与方块等公共领域类型
│   ├── profile/       本机稳定玩家身份与档案
│   ├── world/         区块和世界数据模型
│   ├── worldgen/      程序化地形生成
│   ├── physics/       玩家运动与碰撞
│   ├── sim/           权威世界模拟
│   ├── server/        服务端 Host、会话、发布与玩家持久化
│   ├── network/       二进制协议、登录状态机与 Memory/TCP 传输
│   ├── storage/       世界、区域文件与玩家状态持久化
│   ├── client/        输入、相机、预测与客户端镜像
│   ├── mesh/          区块网格生成
│   ├── render/        GPU 驱动渲染器
│   ├── gfx/           WebGPU 抽象层
│   └── assets/        方块定义与程序化材质
└── docs/              设计、实施计划和性能记录
```

整体架构与技术选型见[项目设计文档](docs/superpowers/specs/2026-07-26-minecraft-go-design.md)，当前性能基线见[性能记录](docs/notes/perf-baseline.md)。

## 当前限制

- 可运行客户端目前仅支持 macOS；
- TCP 多人联机仅面向可信局域网，协议没有认证或加密，不能暴露到公网；
- 尚无服务器发现、游戏内连接菜单或断线自动重连；
- 程序化占位材质用于开发验证，仓库不包含官方 Minecraft 美术资源；
- 快捷栏固定 9 格、单格最多 64 个同类物品；快捷栏装满时仍可挖掘，物品留在地面；
- 掉落物属于所属区块：每区块最多 32 堆、同位置同物品最多合并到 64，区块掉落物已满时挖掘会被拒绝且方块保持不变；
- 掉落物生成 10 tick 后可被 1.25 格内的玩家拾取，累计 6000 活动 tick 后消失；只有玩家附近（区块半径 2）的掉落物才会推进寿命；
- 拾取先填快捷栏再填背包（同类未满格优先，其次最低空格），两者都满时剩余物品留在地面；
- 背包界面只支持整堆移动：空目标接收整堆、同物品合并到 64 并保留余量、不同物品交换；尚无拆分堆、拖拽、快捷搬运与丢弃；
- 尚无投掷/丢弃、掉落物物理与移动、背包界面与合成；
- 生存、合成、光照、怪物等完整玩法尚未完成。

## 兼容性与升级

- 线上协议为 v5：用完整 36 格 `InventoryState` 替换原快捷栏状态，并新增整堆移动请求；声明 v4 及更早版本的客户端会在进入 Play 前被稳定拒绝，不提供版本协商；
- 玩家存档 schema 升级为 v3，在 v2 负载后追加固定 27 格背包；有效的 v2 存档保留原快捷栏并迁移为空背包，v1 沿链迁移，均在下一次正常保存时写为 v3。区块存档 schema 保持 v2；
- 升级前请备份整个世界目录（玩家与区块存档）。回退到旧程序时，尚未写为 v2 的记录无需处理，已写为 v2 的必须从备份恢复；旧程序会把 v2 当作未来版本稳定拒绝，不会覆盖。

## 使用 OpenSpec 开发

本项目使用 OpenSpec 管理复杂变更。一次变更通常包含：

- `proposal.md`：为什么做、范围和非目标；
- `specs/<capability>/spec.md`：可观察行为和验收场景；
- `design.md`：技术方案、边界和取舍；
- `tasks.md`：可执行、可验证的任务清单。

新功能、跨包重构、网络协议或存档格式变化，以及影响架构和性能契约的修改，默认使用 OpenSpec。拼写、格式等低风险小改动可以直接完成。

### 1. 安装

需要 Node.js 20.19 或更高版本：

```bash
npm install -g @fission-ai/openspec@1.7.0
openspec --version
```

仓库已经为 Claude Code 和 Codex 生成集成文件，不需要再次运行 `openspec init`。

### 2. 探索和提案

命令输入在 AI 对话中，不是在 shell 中执行：

```text
Claude Code: /opsx:explore
Claude Code: /opsx:propose add-example-feature

Codex:       $openspec-explore
Codex:       $openspec-propose add-example-feature
```

提案生成到 `openspec/changes/add-example-feature/`。编码前应人工检查目标、非目标、Requirement/Scenario、技术方案和任务拆分；所有产物都是普通 Markdown，可以直接修改。

### 3. 按规格实现

```text
Claude Code: /opsx:apply
Codex:       $openspec-apply-change
```

AI 会按照 `tasks.md` 实现并验证。需求或设计发生变化时，先更新 change 产物，保持规格与实现一致：

```text
Claude Code: /opsx:update
Codex:       $openspec-update-change
```

### 4. 校验和归档

实现完成后在终端运行：

```bash
openspec status --change add-example-feature
openspec validate --all --strict --no-interactive
go test ./... -race
go vet ./...
```

确认实现、规格和任务状态一致后，在 AI 对话中归档：

```text
Claude Code: /opsx:archive
Codex:       $openspec-archive-change
```

归档会把 delta specs 合入 `openspec/specs/`，并把完整变更移至 `openspec/changes/archive/`。这些文件应和代码一起提交到版本控制。

`sync` 用于长期变更在归档前提前同步主规格：

```text
Claude Code: /opsx:sync
Codex:       $openspec-sync-specs
```

| 阶段 | Claude Code | Codex |
| --- | --- | --- |
| 探索 | `/opsx:explore` | `$openspec-explore` |
| 提案 | `/opsx:propose <change>` | `$openspec-propose <change>` |
| 实现 | `/opsx:apply` | `$openspec-apply-change` |
| 更新产物 | `/opsx:update` | `$openspec-update-change` |
| 提前同步主规格 | `/opsx:sync` | `$openspec-sync-specs` |
| 归档 | `/opsx:archive` | `$openspec-archive-change` |

项目上下文和产物约束见 [`openspec/config.yaml`](openspec/config.yaml)，AI 项目规则见 [`AGENTS.md`](AGENTS.md)，更完整的工作流说明见 [`docs/openspec.md`](docs/openspec.md)。

### 自动 Hooks 约束

Claude Code 与 Codex 共用 [`scripts/agent-hooks/guard.mjs`](scripts/agent-hooks/guard.mjs)，分别由 `.claude/settings.json` 和 `.codex/hooks.json` 加载：

- `PreToolUse`：阻止高破坏性 Git 命令、强制推送和宽范围递归删除；
- `PostToolUse`：编辑后检查改动中的 Go 文件是否已经 `gofmt`；
- `Stop`：检查 diff、OpenSpec、架构依赖、受影响包 race 测试和 `go vet`；
- 协议、存档、性能基线、架构边界或跨组件实现变更，没有完整 active OpenSpec change 时不得结束任务。

首次进入仓库后，在 Codex 或 Claude Code 中打开 `/hooks` 检查配置。Codex 会要求审查并信任项目 Hook；Hook 文件发生变化后需要重新审查。

Hook 策略测试：

```bash
node --test scripts/agent-hooks/guard.test.mjs
```

Hook 是机械护栏，不能替代 CI 和人工评审。详细触发规则与显式例外方式见 [`docs/openspec.md`](docs/openspec.md#自动-hook-约束)。
