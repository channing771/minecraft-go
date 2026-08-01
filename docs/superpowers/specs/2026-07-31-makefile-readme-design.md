# Makefile 与 README 设计

- 日期：2026-07-31
- 状态：已确认
- 范围：新增根目录 Makefile 与 README，不修改业务代码和 CI

## 目标

为当前项目提供一组容易记忆的本地开发入口，并让新贡献者能够从 README 了解项目定位、运行条件、基本操作和目录结构。

## Makefile

Makefile 保持轻量，只封装仓库当前已经使用的 Go 命令，不引入额外工具：

- `help`：默认目标，列出可用命令及用途；
- `run`：运行 `cmd/mcgo`，并通过 `ARGS` 透传可选命令行参数；
- `build`：将客户端构建到 `bin/mcgo`；
- `test`：运行 `go test ./...`；
- `fmt`：使用 `gofmt` 格式化仓库内 Go 文件；
- `clean`：删除 Makefile 生成的 `bin` 目录。

所有目标声明为 phony。目录和输出文件使用变量集中定义，调用者只需在需要时设置 `ARGS`，例如 `make run ARGS="--world worlds/demo"`。

## README

README 使用中文，内容以当前实现为准，包含：

1. 项目定位和当前已实现能力；
2. 当前仅支持 macOS、需要 Go 1.26 和可用 CGO 工具链的环境要求；
3. 克隆后通过 `make run` 启动的快速开始；
4. 六个 Make 目标和 `ARGS` 示例；
5. WASD、空格、鼠标、数字键与 Esc 的操作说明；
6. 默认存档目录与自定义世界目录说明；
7. `cmd`、`internal`、`docs` 等主要目录结构；
8. 当前平台和功能边界，避免把 M3B 设计稿中的未实现能力写成现状。

README 不复制完整架构设计或性能基线，只链接仓库内已有文档，避免形成重复且容易过期的信息源。

## 验证

实施后执行以下检查：

- `make help` 能正确列出六个目标；
- `make fmt` 执行成功且 `gofmt -l .` 无输出；
- `make test` 通过；
- `make build` 生成 `bin/mcgo`；
- `make clean` 仅删除生成的 `bin` 目录；
- README 中的命令、参数、按键和路径与源码一致。
