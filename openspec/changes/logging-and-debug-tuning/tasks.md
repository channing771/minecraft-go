> 完整步骤级代码见 `docs/superpowers/plans/2026-08-08-logging-and-debug-tuning.md`。
> 本文件是可勾选的执行顺序与验证命令。以下 10 个任务组与该计划的 Task 2–11 一一对应
> （计划的 Task 1 即为建立本 OpenSpec change 本身，已完成，不在此文件中重复勾选）。

## 1. `internal/logging` 分模块 handler

- [ ] 1.1 在 `internal/archcheck/deps_test.go` 的 `allowed` 表登记 `"internal/logging": {}`
- [ ] 1.2 先写失败测试 `internal/logging/logging_test.go`：全局等级过滤、单模块放宽/收紧、不泄漏到其他模块、`Enabled` 快速门、PC 反查模块名、`ParseLevel`、`WithAttrs` 保持过滤
  Run: `go test ./internal/logging -run . -count=1`
  Expected: 编译失败（`undefined: New` 等）
- [ ] 1.3 实现 `internal/logging/logging.go`：`Config`、`New`、`Install`、`ParseLevel`、基于调用点 PC 反查包路径的模块识别
  Run: `go test ./internal/logging ./internal/archcheck -race -count=1`
  Expected: PASS
- [ ] 1.4 变异验证：临时把 `Enabled` 改为恒真，确认 `TestGlobalLevelDropsLowerRecords` 变红；恢复后确认 `git diff internal/logging/logging.go` 干净
- [ ] 1.5 提交 `feat: 增加按模块分级过滤的 slog handler`

## 2. 统一日志到 slog

- [ ] 2.1 `internal/gfx/wgpu.go` 的 4 处 `log.Printf` 改为 `slog.Info`/`slog.Warn`
- [ ] 2.2 `cmd/mcgo/main.go`、`cmd/mcgo/app.go` 共 16 处 `log.Printf` 改为 `slog`
- [ ] 2.3 `cmd/gfxspike/main.go:58` 一处改为 `slog.Info`；同文件 `log.Fatalf`（3 处）保留不动
- [ ] 2.4 确认没有遗漏
  Run: `grep -rn "log\.Printf" --include="*.go" internal cmd`
  Expected: 无输出
  Run: `grep -rn "log\.Fatalf" --include="*.go" internal cmd`
  Expected: 只剩 `cmd/gfxspike/main.go` 三处
- [ ] 2.5 回归
  Run: `go test ./internal/gfx ./cmd/mcgo ./cmd/gfxspike -count=1 && go vet ./... && gofmt -l .`
  Expected: 测试 PASS，`go vet` 与 `gofmt -l .` 均无输出
- [ ] 2.6 提交 `refactor: 把剩余 log.Printf 统一到 slog`

## 3. `physics.Tunables` 管道

- [ ] 3.1 先写失败测试 `internal/physics/tunables_test.go`：默认值逐字段等于旧编译常量、`ActiveTunables` 默认等于 `DefaultTunables`、`SetTunables` 影响 `Step` 的结果、单次固定步内快照不变、`-race` 下并发读写无竞争
  Run: `go test ./internal/physics -run Tunables -count=1`
  Expected: 编译失败（`undefined: physics.Tunables`）
- [ ] 3.2 实现 `internal/physics/tunables.go`：`Tunables`、`DefaultTunables`、`SetTunables`、`ActiveTunables`（原有导出常量本步保留，不删除）
- [ ] 3.3 `internal/physics/motion.go`（`Step`、`movementTarget`）与 `internal/physics/step.go`（`resolveStepMove`）改为消费函数入口取的一次快照
  Run: `go test ./internal/physics -race -count=1`
  Expected: PASS；既有物理测试全部通过且不需要修改期望
- [ ] 3.4 全仓回归
  Run: `go test ./internal/sim ./internal/client ./internal/server -race -count=1`
  Expected: PASS
- [ ] 3.5 提交 `feat: 增加物理参数运行时快照管道`

## 4. `sim.Tunables` 管道

- [ ] 4.1 先写失败测试 `internal/sim/tunables_test.go`：默认值逐字段等于旧编译常量、`Engine` 在 tick 入口刷新快照且同 tick 内不变
  Run: `go test ./internal/sim -run Tunables -count=1`
  Expected: 编译失败（`undefined: DefaultTunables`）
- [ ] 4.2 实现 `internal/sim/tunables.go`（结构与 `internal/physics/tunables.go` 一致）
- [ ] 4.3 `Engine` 新增 `tunables`/`physicsTunables` 快照字段，在 `Step` 入口与构造函数中刷新
- [ ] 4.4 `engine.go`、`container.go`、`mining.go`、`drop.go`、`death.go`、`health_regen.go`、`furnace.go`、`spawn.go` 各读取点改用引擎快照或由调用方传参；自由函数内部不得再调 `ActiveTunables()`（会破坏"同 tick 一份快照"）；`spawn.go` 的容量分配依赖任务 5 的 `SpawnRadius` 区间钳制，须加注释说明该依赖
  Run: `go test ./internal/sim -race -count=1`
  Expected: PASS；既有 sim 测试全部通过且不需要修改期望
- [ ] 4.5 全仓回归
  Run: `go test ./... -race`
  Expected: PASS
- [ ] 4.6 提交 `feat: 增加模拟参数运行时快照管道`

## 5. `internal/config` 配置加载与保存

- [ ] 5.1 在 `allowed` 表登记 `"internal/config": {"internal/core", "internal/physics", "internal/sim", "internal/logging"}`（刻意不含 `internal/render`）
- [ ] 5.2 先写失败测试 `internal/config/config_test.go`：文件不存在取默认且不自动创建文件、字段缺失取默认、越界值被钳制、未知字段被忽略、JSON 语法错误报错、`version` 不认识报错、保存/加载 round-trip、保存产物是合法 JSON 且写入是原子的
- [ ] 5.3 实现 `internal/config/config.go`：`Config`、`Render`、`Defaults`、`DefaultPath`、`Load`、`Save`、`Apply`、`Field`/`Fields`（面板与钳制共用同一份区间定义）
  Run: `go test ./internal/config -count=1`
  Expected: PASS
- [ ] 5.4 依赖回归
  Run: `go test ./internal/config ./internal/archcheck -race -count=1`
  Expected: PASS
- [ ] 5.5 提交 `feat: 增加 JSON 配置文件加载、钳制与原子保存`

## 6. 删除旧导出常量并加 archcheck 守卫

- [ ] 6.1 先写两条守卫测试 `TestTunableConstantsAreNotExported`、`TestOnlyCommandsImportConfig`，确认此时为红
  Run: `go test ./internal/archcheck -run TunableConstants -count=1`
  Expected: FAIL，列出仍以导出常量暴露的可调参数
- [ ] 6.2 `internal/physics/types.go` 与 `internal/sim` 相关文件把可调常量降为未导出 `default*`；`tunables.go` 的 `DefaultTunables` 改引用未导出常量
- [ ] 6.3 修复因此断裂的外部读取点（`cmd/mcgo/main.go:354` 的相机视线高度改用 `physics.ActiveTunables().EyeHeight`）
  Run: `go build ./...`
  Expected: 编译通过
- [ ] 6.4 修复既有测试中对被降级常量的引用（只改取值方式，不改测试期望值本身）
  Run: `go test ./... -race`
  Expected: PASS
- [ ] 6.5 守卫测试转绿
  Run: `go test ./internal/archcheck -race -count=1`
  Expected: PASS
- [ ] 6.6 变异验证：临时加回一个导出常量确认 `TestTunableConstantsAreNotExported` 变红；临时让 `internal/sim` 导入 `internal/config` 确认 `TestOnlyCommandsImportConfig` 变红；两条都必须变红。恢复后确认 `git diff` 干净
- [ ] 6.7 提交 `refactor: 可调参数只经快照读取，并加 archcheck 守卫`

## 7. cmd 接线（`--config`、`--dev`、日志装配）

- [ ] 7.1 先写失败测试：`cmd/mcgo/main_test.go`（`--dev` 默认关闭、`--dev`/`--config` 被正确解析、`--benchmark` 与 `--capture-dir` 路径忽略本机配置）；`cmd/mcgod/main_test.go`（`--config` 被解析、默认空串表示使用默认路径）
  Run: `go test ./cmd/mcgo ./cmd/mcgod -run "Config|Dev" -count=1`
  Expected: 编译失败
- [ ] 7.2 `cmd/mcgo`：新增 `--dev`/`--config` flag、`resolveConfig`（benchmark 与抓帧路径强制 `config.Defaults()`）、启动时 `logging.Install` 与 `effective.Apply()`；`applicationOptions` 新增 `Dev`/`Render`，`viewDistance`/FOV/鼠标灵敏度改读配置
- [ ] 7.3 `cmd/mcgod`：新增 `--config` flag，启动时加载配置、装日志、`Apply()`，忽略渲染组
  Run: `go test ./cmd/mcgo ./cmd/mcgod -race -count=1`
  Expected: PASS
- [ ] 7.4 确认 `mcgod` 仍无图形依赖
  Run: `go test ./internal/archcheck -run MCGod -count=1`
  Expected: PASS
  Run: `GOOS=linux CGO_ENABLED=0 go build ./cmd/mcgod`
  Expected: 编译通过
- [ ] 7.5 提交 `feat: cmd 接入配置文件加载、日志分级装配与 --dev 门控`

## 8. 调试面板渲染器

- [ ] 8.1 先写失败测试 `internal/render/debug_panel_test.go`：面板行数与字形实例数不超预分配上限、关闭状态下不产出任何实例
  Run: `go test ./internal/render -run DebugPanel -count=1`
  Expected: 编译失败
- [ ] 8.2 实现 `internal/render/debug_panel.go` 与 `internal/render/shader/debug_panel.wgsl`：复用既有 `GlyphAtlas` 与 hotbar 的屏幕空间实例化模式，固定容量最大 64 行
  Run: `go test ./internal/render -race -count=1`
  Expected: PASS
- [ ] 8.3 性能验证：面板关闭时零额外开销
  Run: `go test ./internal/render -bench . -run '^$' -count=1`
  Expected: 与改动前基线相比无可测量退化
- [ ] 8.4 提交（信息覆盖"新增调试面板渲染器"这一变更，具体措辞按实现时的实际范围拟定）

## 9. 面板交互接线

- [ ] 9.1 `internal/client/window.go` 扩充 `Key` 枚举与 `glfwKeys` 表（F3/F5/F6、方向键、Enter、LeftAlt），追加在末尾以免改变既有常量的 iota 取值
- [ ] 9.2 先写失败测试 `cmd/mcgo/debug_panel_test.go`（纯逻辑，不建窗口、不初始化 GPU）：联机时 physics/sim 分组行只读、单机时可编辑
  Run: `go test ./cmd/mcgo -run Panel -count=1`
  Expected: 编译失败
- [ ] 9.3 实现 `cmd/mcgo/debug_panel.go`（`panelState`、`rows`、`handleKeys`、`save`）并接入 `app.go` 的渲染器字段/`Prepare`/`Render`/`Release`、`main.go` 的按键处理（方向键选行与步进、Shift 粗调、Alt 细调、Enter 重置、F5 保存、F6 全部重置）
  Run: `go test ./cmd/mcgo ./internal/client -race -count=1`
  Expected: PASS
  Run: `go test ./cmd/mcgo -run Panel -count=1 -v`
  Expected: PASS
- [ ] 9.4 提交（信息覆盖"接入面板方向键交互"这一变更，具体措辞按实现时的实际范围拟定）

## 10. 收尾门禁与文档

- [ ] 10.1 全量门禁
  Run: `gofmt -l .`
  Expected: 无输出
  Run: `go vet ./...`
  Expected: 无输出
  Run: `go test ./... -race`
  Expected: PASS
  Run: `go test ./internal/archcheck -count=1`
  Expected: PASS
- [ ] 10.2 性能门禁，记录改前改后数值
  Run: `go test ./internal/render ./internal/sim ./internal/physics -bench . -run '^$' -count=1`
  Expected: 与基线相比无退化
  Run: `go run ./cmd/perfcheck -baseline <基线> -current <本次>`（按既有用法）
  Expected: 无超阈退化
- [ ] 10.3 跨平台构建
  Run: `GOOS=linux CGO_ENABLED=0 go build ./cmd/mcgod`
  Expected: 通过
  Run: `go build ./...`
  Expected: 通过
- [ ] 10.4 人工验收（仅在用户明确要求时执行，自动测试不得启动前台窗口）：`--dev` 面板显隐、读数更新、方向键调参、Shift/Alt 步长、Enter 重置、F5 保存并在下次不带 `--dev` 启动时仍生效、联机时 physics/sim 灰显只读
- [ ] 10.5 更新 `README.md`：新增配置文件路径与三个分组、`--config`/`--dev` 旗标、日志等级配置方式，以及"配置文件始终生效，`--dev` 只控制面板可见性"这条语义
- [ ] 10.6 勾选本文件全部任务，做最终 OpenSpec 校验
  Run: `openspec validate --all --strict --no-interactive`
  Expected: 通过，无 error
- [ ] 10.7 提交 `docs: 补齐配置文件与调试面板说明并勾选任务`

---

## 归档

全部任务完成并通过门禁后，按 `docs/openspec.md` 的流程归档 change，把 `module-scoped-logging` 与 `tunable-constants` 两条能力合入 `openspec/specs/`。归档前再次确认任务状态、实现与 delta specs 三者一致。
