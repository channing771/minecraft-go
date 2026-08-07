# M4I Task 1 基线核对报告

## 结论

- 状态：PASS。
- 精确 M4G 归档 HEAD：`cc1fe653b2f2d348f618851406495983e5fedac9`，与任务指定基线一致。
- 协议：v9（`internal/network/packet.go` 的 `ProtocolVersion`）。
- benchmark scenario：v12（`cmd/mcgo/benchmark.go` 的 `scenarioVersion`）。
- 玩家 schema：v3（`internal/storage/player_codec.go` 的 `currentPlayerSchema`）。
- 区块 schema：v4（`internal/storage/chunk_codec.go` 的 `currentChunkSchema`）。
- world metadata：v2（`internal/storage/metadata.go` 的 `currentMetadataVersion`）。
- M5 当前基线：`docs/notes/perf-baseline-m5.json` 的 `scenario_version` 为 v12；`docs/notes/perf-baseline.md` 记录其为当前已接受 M5 scenario v12 基线。

## 命令与结果

| 命令 | 结果 |
| --- | --- |
| `git status --short --branch` | `## codex/m4i-authoritative-celestial-sky`；开始时工作区干净。 |
| `git rev-parse HEAD` | `cc1fe653b2f2d348f618851406495983e5fedac9`。 |
| `openspec list --json` | `m4i-authoritative-celestial-sky` 为 `in-progress`，开始时 `completedTasks: 0`、`totalTasks: 20`。 |
| `rg -n "ProtocolVersion|scenarioVersion|currentPlayerSchema|currentChunkSchema|metadataFormatVersion" internal/network internal/storage cmd/mcgo` | 命中 protocol v9、scenario v12、玩家 schema v3、区块 schema v4；metadata 常量在 `internal/storage/metadata.go`，值 v2。 |
| `go test ./internal/render ./cmd/mcgo ./cmd/perfcheck -race -count=1` | PASS：render 4.917s、mcgo 21.370s、perfcheck 2.975s。 |
| `go test ./internal/archcheck -count=1` | PASS：0.696s。 |
| `openspec validate --all --strict --no-interactive` | PASS：11 passed，0 failed。 |

## 改动与自审

- 改动文件：`openspec/changes/m4i-authoritative-celestial-sky/tasks.md`（仅勾选 1.1、1.2）；本报告。
- 未修改实现代码、版本号、协议、存档或性能基线；未启动图形客户端。
- 自审：所有要求值与 M4G 基线匹配，规定的渲染、性能和架构测试通过；严格 OpenSpec 校验和 `git diff --check` 已通过。
