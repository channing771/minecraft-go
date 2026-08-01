# minecraft-go

`minecraft-go` 是一个使用 Go 编写的独立体素游戏实验项目。项目自研客户端、权威服务端、世界存储和 WebGPU 渲染管线，不追求兼容官方 Minecraft 的协议、存档或资源。

项目仍处于早期开发阶段。目前已经具备程序化地形、GPU 地形渲染、玩家移动与碰撞、客户端预测、方块挖掘与放置、内置权威服务端以及世界持久化等基础能力。

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
| 鼠标左键 | 挖掘方块 |
| 鼠标右键 | 放置方块 |
| `1` / `2` / `3` | 选择石头、泥土或草方块 |
| `Esc` | 释放鼠标指针 |
| 释放指针后单击窗口 | 重新捕获鼠标指针 |

关闭游戏窗口时，内置服务端会停止并刷新待保存的世界数据。运行时生成的世界目录已在 `.gitignore` 中排除。

## 项目结构

```text
.
├── cmd/
│   ├── mcgo/          游戏客户端与内置服务端装配
│   ├── gfxspike/      WebGPU 地形渲染验证程序
│   └── perfcheck/     性能报告比较工具
├── internal/
│   ├── core/          坐标、几何与方块等公共领域类型
│   ├── world/         区块和世界数据模型
│   ├── worldgen/      程序化地形生成
│   ├── physics/       玩家运动与碰撞
│   ├── sim/           权威世界模拟
│   ├── server/        服务端会话与区块发布
│   ├── network/       客户端与服务端传输消息
│   ├── storage/       世界持久化与区域文件
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
- 当前版本使用内置服务端和内存传输，TCP 多人联机仍在设计与实现阶段；
- 程序化占位材质用于开发验证，仓库不包含官方 Minecraft 美术资源；
- 生存、合成、光照、怪物等完整玩法尚未完成。
