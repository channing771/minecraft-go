## 1. 锁定 M4G 起点

- [x] 1.1 确认 M4G 已完成性能接受、规格同步和归档；在干净工作区运行 `git status --short --branch`、`openspec list --json`、`rg -n "ProtocolVersion|scenarioVersion|currentPlayerSchema|currentChunkSchema|metadataFormatVersion" internal/network internal/storage cmd/mcgo`，核对协议 v9、scenario v12、玩家 schema v3、区块 schema v4、metadata v2 与 M5 v12 当前基线。任一落号不同都先更新本 change 的 proposal、三份 delta specs、design 和 tasks，并运行 `openspec validate --all --strict --no-interactive`，不得直接编码。
- [x] 1.2 在 M4G 归档 HEAD 上运行 `go test ./internal/render ./cmd/mcgo ./cmd/perfcheck -race -count=1` 与 `go test ./internal/archcheck -count=1`，确认本 change 的渲染和性能起点无已有失败，并记录精确 `git rev-parse HEAD` 供后续差异核对。

## 2. 固定权威天体参数

- [ ] 2.1 在 `internal/render/daylight_test.go` 先增加失败测试，覆盖 `0/6000/12000/18000` 四个相位的太阳方向、相反月亮方向、既有 `Sun/Daylight/ClearColor` 不漂移及星空只在近地平线/夜间平滑出现；再最小扩展 `internal/render/daylight.go` 的 `DayNightAt` 返回值。运行 `go test ./internal/render -run 'Test(DayNight|Celestial)' -race -count=1`。
- [ ] 2.2 在 `cmd/mcgo/app_test.go` 增加失败测试，证明 app 仍只在接受更新 `ServerTick` 时推进 `worldTimeTicks`，旧/重复状态不会改变传给天空的天体参数，Memory/TCP 相同状态得到相同结果；复用现有接收路径，不新增消息或时钟。运行 `go test ./cmd/mcgo -run 'Test.*(WorldTime|DayNight|Celestial)' -race -count=1`。

## 3. 加入最小天空 draw

- [ ] 3.1 在 `internal/render/sky_test.go` 先用 fake gfx 增加失败测试，固定 sky pipeline 的 color/depth format、关闭 depth write、单一 bind group、一次 `Draw(3, 1)`、sky-before-terrain 顺序和幂等资源释放；再在 `internal/render/renderer.go` 与 `internal/render/shader/sky.wgsl` 实现同一 terrain pass 内的 fullscreen sky。运行 `go test ./internal/render -run 'TestSky(Renderer|Pipeline|Draw|Release)' -race -count=1`。
- [ ] 3.2 在 `internal/render/sky_test.go` 与 `hot_path_allocation_test.go` 先增加失败测试，固定 96 字节 sky uniform、太阳方向/亮度/星空可见度字段和稳定 Render 零分配；再让 Renderer 持有固定 terrain/sky 编码数组，把现有分配式 `cameraBytes` 改为写入调用方切片。运行 `go test ./internal/render -run 'Test(SkyUniform|RendererRenderDoesNotAllocate)' -count=1`。
- [ ] 3.3 在 `internal/render/sky_test.go` 增加可跳过无 GPU 环境的 headless 像素测试，覆盖正午天顶太阳、午夜天顶月亮与星空、相机平移无视差、旋转往返图案一致以及地形覆盖天体；修正 WGSL 固定渐变、4° 圆盘、世界方向 hash 和地平线遮罩直到通过。运行 `go test ./internal/render -run 'TestSkyHeadless' -race -count=1`。

## 4. 接入客户端与 spike

- [ ] 4.1 在 `cmd/mcgo/app_test.go` 先增加失败测试，证明一帧只计算一次 `ViewProj`/逆矩阵且 terrain、avatar、item-drop 继续共享同一正向矩阵与 `Daylight`；再修改 `cmd/mcgo/app.go` 填充 sky 所需的 `ViewProjInv` 和天体参数，不改 renderer 构造依赖或掉落路径。运行 `go test ./cmd/mcgo -run 'Test.*RenderFrame.*(Camera|Daylight|Sky)' -race -count=1`。
- [ ] 4.2 修改 `cmd/gfxspike/main.go`，让固定演示相机提供 inverse ViewProj 并使用正午 `DayNightAt(6000)`；自动验证只运行 `go test ./cmd/gfxspike ./cmd/mcgo -race -count=1` 和 `go build ./cmd/gfxspike ./cmd/mcgo`，不得启动或聚焦窗口。

## 5. 升级性能场景而不放宽门禁

- [ ] 5.1 在 `cmd/mcgo/benchmark_v5_test.go`、`benchmark_v6_test.go` 和相关 scenario 测试先增加失败断言，要求 producer 标记 scenario v13、still/flying 继续调用真实 `renderFrame` 并包含天空 draw，同时保持 2560x1440、阶段时长、样本数、mesher/message 上限、冷却和 `remote_gpu_complete` 定义不变；再最小修改 `cmd/mcgo/benchmark.go`。运行 `go test ./cmd/mcgo -run 'Test.*ScenarioV13|TestBenchmark.*' -race -count=1`。
- [ ] 5.2 在 `cmd/perfcheck/main_test.go` 先增加失败矩阵，覆盖唯一 `12:13` 迁移、无授权/反向/跳版本/旧参数拒绝、v6-v12 历史报告可读、v13 同场景完整门禁与跨硬件拒绝；再修改 `cmd/perfcheck/main.go`，不得改变绝对阈值、`20%` 回归线或最小有意义增量。运行 `go test ./cmd/perfcheck -race -count=1`。
- [ ] 5.3 更新 `README.md` 与 `docs/notes/perf-baseline.md` 的天空能力、非目标、scenario v13、唯一 `12:13` 迁移和回退说明；在正式报告通过前不得改写 `docs/notes/perf-baseline-m5.json` 的接受字节。运行 `rg -n "scenario v13|12:13|太阳|月亮|星空" README.md docs/notes/perf-baseline.md` 人工核对。

## 6. 冻结候选前验证

- [ ] 6.1 运行受影响范围门禁：`go test ./internal/render ./cmd/mcgo ./cmd/perfcheck -race -count=1`、`go test ./internal/archcheck -count=1`、`go test ./internal/render -run '^$' -bench 'BenchmarkRemoteAvatarNameTag' -benchmem`，并确认天空相关稳定 Render 测试报告零分配且既有 benchmark 无失败。
- [ ] 6.2 运行完整无窗口门禁：先执行 `gofmt -w internal/render/daylight.go internal/render/daylight_test.go internal/render/renderer.go internal/render/sky_test.go cmd/mcgo/app.go cmd/mcgo/app_test.go cmd/mcgo/benchmark.go cmd/mcgo/benchmark_v5_test.go cmd/mcgo/benchmark_v6_test.go cmd/gfxspike/main.go cmd/perfcheck/main.go cmd/perfcheck/main_test.go`，随后 `gofmt -l .` 必须无输出，再运行 `go test ./... -race`、`go vet ./...`、`CGO_ENABLED=0 GOOS=linux go build ./cmd/mcgod` 和 `openspec validate --all --strict --no-interactive`；任何失败修复根因，不修改 Hook 或放宽门禁。
- [ ] 6.3 核对 `git diff --check`、`git status --short` 和 `git diff --name-only`，确认没有修改 `internal/sim/drop.go`、`internal/client/drops.go`、`internal/render/drop.go`、协议、存档格式或二进制资产；提交候选后记录精确 `git rev-parse HEAD`，此后正式链前不得改动候选代码或规格。

## 7. 建立 M5 scenario v13 基线

- [ ] 7.1 候选完整门禁进程退出并自然冷却至少 5 分钟后，使用 `uptime`、`pmset -g batt`、`pmset -g custom` 和 `pgrep -fl 'mcgo|perfcheck'` 间隔至少 30 秒采集两次只读宿主状态；两次都满足 load、AC、电量、低电量模式和无遗留进程条件后，令任务变量 `candidate` 等于 `git rev-parse HEAD` 的精确输出，绑定该 HEAD 与两个全新 `/tmp/mcgo-m5-v13-${candidate}-{memory,tcp}.json` 路径请求用户一次性正式授权。预检失败不得启动 producer、清理缓存或结束用户进程。
- [ ] 7.2 获得精确授权后只执行一次 Memory producer：`go run ./cmd/mcgo --benchmark --benchmark-transport memory --benchmark-output "/tmp/mcgo-m5-v13-${candidate}-memory.json"`；成功后运行 `go run ./cmd/perfcheck --baseline docs/notes/perf-baseline-m5.json --current "/tmp/mcgo-m5-v13-${candidate}-memory.json" --max-regression 0.20 --allow-scenario-upgrade 12:13`。任一步失败立即停止，不重跑、不改报告、不运行 TCP。
- [ ] 7.3 仅在 Memory 迁移验证通过后执行一次 TCP producer：`go run ./cmd/mcgo --benchmark --benchmark-transport tcp --benchmark-output "/tmp/mcgo-m5-v13-${candidate}-tcp.json"`；随后以 Memory 报告为 baseline 运行同场景 `cmd/perfcheck`。通过后才把 Memory 报告精确字节提升为 `docs/notes/perf-baseline-m5.json`，并在 `docs/notes/perf-baseline-m5.md`、`docs/notes/perf-baseline.md` 记录 HEAD、命令、硬件、哈希和 v12→v13 证据；`cmp` 验证基线与 Memory 报告字节一致，M2 基线保持不变。

## 8. 收尾验证

- [ ] 8.1 基线与文档更新后再次运行 `gofmt -l .`、`go test ./internal/archcheck -count=1`、`go test ./... -race`、`go vet ./...`、`CGO_ENABLED=0 GOOS=linux go build ./cmd/mcgod` 和 `openspec validate --all --strict --no-interactive`；`gofmt -l .` 必须无输出，所有命令必须成功。
- [ ] 8.2 复核 proposal、三份 delta specs、design 与已勾选 tasks 和最终实现一致，确认协议/metadata/玩家/区块版本仍为 M4G 归档值、scenario v13 及 M5 基线证据准确，再准备规格同步、评审与归档；自动验收全程不得启动交互式客户端。
