## 1. 锁定 M4G 起点

- [x] 1.1 确认 M4G 已完成性能接受、规格同步和归档；在干净工作区运行 `git status --short --branch`、`openspec list --json`、`rg -n "ProtocolVersion|scenarioVersion|currentPlayerSchema|currentChunkSchema|metadataFormatVersion" internal/network internal/storage cmd/mcgo`，核对协议 v9、scenario v12、玩家 schema v3、区块 schema v4、metadata v2 与 M5 v12 当前基线。任一落号不同都先更新本 change 的 proposal、三份 delta specs、design 和 tasks，并运行 `openspec validate --all --strict --no-interactive`，不得直接编码。
- [x] 1.2 在 M4G 归档 HEAD 上运行 `go test ./internal/render ./cmd/mcgo ./cmd/perfcheck -race -count=1` 与 `go test ./internal/archcheck -count=1`，确认本 change 的渲染和性能起点无已有失败，并记录精确 `git rev-parse HEAD` 供后续差异核对。

## 2. 固定权威天体参数

- [x] 2.1 在 `internal/render/daylight_test.go` 先增加失败测试，覆盖 `0/6000/12000/18000` 四个相位的太阳方向、相反月亮方向、既有 `Sun/Daylight/ClearColor` 不漂移及星空只在近地平线/夜间平滑出现；再最小扩展 `internal/render/daylight.go` 的 `DayNightAt` 返回值。运行 `go test ./internal/render -run 'Test(DayNight|Celestial)' -race -count=1`。
- [x] 2.2 在 `cmd/mcgo/app_test.go` 增加失败测试，证明 app 仍只在接受更新 `ServerTick` 时推进 `worldTimeTicks`，旧/重复状态不会改变传给天空的天体参数，Memory/TCP 相同状态得到相同结果；复用现有接收路径，不新增消息或时钟。运行 `go test ./cmd/mcgo -run 'Test.*(WorldTime|DayNight|Celestial)' -race -count=1`。

## 3. 加入最小天空 draw

- [x] 3.1 在 `internal/render/sky_test.go` 先用 fake gfx 增加失败测试，固定 sky pipeline 的 color/depth format、关闭 depth write、单一 bind group、一次 `Draw(3, 1)`、sky-before-terrain 顺序和幂等资源释放；再在 `internal/render/renderer.go` 与 `internal/render/shader/sky.wgsl` 实现同一 terrain pass 内的 fullscreen sky。运行 `go test ./internal/render -run 'TestSky(Renderer|Pipeline|Draw|Release)' -race -count=1`。
- [x] 3.2 在 `internal/render/sky_test.go` 与 `hot_path_allocation_test.go` 先增加失败测试，固定 96 字节 sky uniform、太阳方向/亮度/星空可见度字段和稳定 Render 零分配；再让 Renderer 持有固定 terrain/sky 编码数组，把现有分配式 `cameraBytes` 改为写入调用方切片。运行 `go test ./internal/render -run 'Test(SkyUniform|RendererRenderDoesNotAllocate)' -count=1`。
- [x] 3.3 在 `internal/render/sky_test.go` 增加可跳过无 GPU 环境的 headless 像素测试，覆盖正午天顶太阳、午夜天顶月亮与星空、相机平移无视差、旋转往返图案一致以及地形覆盖天体；修正 WGSL 固定渐变、4° 圆盘、世界方向 hash 和地平线遮罩直到通过。运行 `go test ./internal/render -run 'TestSkyHeadless' -race -count=1`。

## 4. 接入客户端与 spike

- [x] 4.1 在 `cmd/mcgo/app_test.go` 先增加失败测试，通过真实 `renderFrame` 的 uniform 写入证明 terrain、avatar、item-drop 共享同一正向矩阵与 `Daylight`，sky 使用由该矩阵派生的 inverse 和权威天体参数；再修改 `cmd/mcgo/app.go`，让一帧只调用一次 `Camera.ViewProj()` 并对该局部值调用一次 `Inv()`。不得为计数加入 production hook、接口或回调，不改 renderer 构造依赖或掉落路径；源码评审同时核对单次计算结构。运行 `go test ./cmd/mcgo -run 'Test.*RenderFrame.*(Camera|Daylight|Sky)' -race -count=1`。
- [x] 4.2 修改 `cmd/gfxspike/main.go`，让固定演示相机提供 inverse ViewProj 并使用正午 `DayNightAt(6000)`；自动验证只运行 `go test ./cmd/gfxspike ./cmd/mcgo -race -count=1` 和 `go build ./cmd/gfxspike ./cmd/mcgo`，不得启动或聚焦窗口。

## 5. 升级性能场景而不放宽门禁

- [x] 5.1 在 `cmd/mcgo/benchmark_v5_test.go`、`benchmark_v6_test.go` 和相关 scenario 测试先增加失败断言，要求 producer 标记 scenario v13、still/flying 继续调用真实 `renderFrame` 并包含天空 draw，同时保持 2560x1440、阶段时长、样本数、mesher/message 上限、冷却和 `remote_gpu_complete` 定义不变；再最小修改 `cmd/mcgo/benchmark.go`。运行 `go test ./cmd/mcgo -run 'Test.*ScenarioV13|TestBenchmark.*' -race -count=1`。
- [x] 5.2 在 `cmd/perfcheck/main_test.go` 先增加失败矩阵，覆盖唯一 `12:13` 迁移、无授权/反向/跳版本/旧参数拒绝、v6-v12 历史报告可读、v13 同场景完整门禁与跨硬件拒绝；再修改 `cmd/perfcheck/main.go`，不得改变绝对阈值、`20%` 回归线或最小有意义增量。运行 `go test ./cmd/perfcheck -race -count=1`。
- [x] 5.3 更新 `README.md` 与 `docs/notes/perf-baseline.md` 的天空能力、非目标、scenario v13、唯一 `12:13` 迁移和回退说明；在正式报告通过前不得改写 `docs/notes/perf-baseline-m5.json` 的接受字节。运行 `rg -n "scenario v13|12:13|太阳|月亮|星空" README.md docs/notes/perf-baseline.md` 人工核对。

## 6. 冻结候选前验证

- [x] 6.1 运行受影响范围门禁：`go test ./internal/render ./cmd/mcgo ./cmd/perfcheck -race -count=1`、`go test ./internal/archcheck -count=1`、`go test ./internal/render -run '^$' -bench 'BenchmarkRemoteAvatarNameTag' -benchmem`，并确认天空相关稳定 Render 测试报告零分配且既有 benchmark 无失败。
- [x] 6.2 运行完整无窗口门禁：先执行 `gofmt -w internal/render/daylight.go internal/render/daylight_test.go internal/render/renderer.go internal/render/sky_test.go cmd/mcgo/app.go cmd/mcgo/app_test.go cmd/mcgo/benchmark.go cmd/mcgo/benchmark_v5_test.go cmd/mcgo/benchmark_v6_test.go cmd/gfxspike/main.go cmd/perfcheck/main.go cmd/perfcheck/main_test.go`，随后 `gofmt -l .` 必须无输出，再运行 `go test ./... -race`、`go vet ./...`、`CGO_ENABLED=0 GOOS=linux go build ./cmd/mcgod` 和 `openspec validate --all --strict --no-interactive`；任何失败修复根因，不修改 Hook 或放宽门禁。
- [x] 6.3 核对 `git diff --check`、`git status --short` 和 `git diff --name-only`，确认没有修改 `internal/sim/drop.go`、`internal/client/drops.go`、`internal/render/drop.go`、协议、存档格式或二进制资产；提交候选后记录精确 `git rev-parse HEAD`，此后正式链前不得改动候选代码或规格。

## 7. 记录首个正式候选失败

- [x] 7.1 候选 `f7d8f261e910863e189666f6e2181e606996f42f` 在完整门禁后自然冷却 `2026-08-05T23:51:51+0800` 至 `23:56:51+0800`，并于 `23:56:59`、`23:57:47` 完成两次只读静稳预检；两次均为 AC、100% 电量、低电量模式关闭、无遗留 `mcgo`/`perfcheck`，load 1m/5m 分别为 `2.81/2.78` 与 `2.25/2.63`。绑定两个全新 v13 路径后取得一次性正式授权。
- [x] 7.2 只执行一次 Memory producer。still 为 `196.4 FPS`、p99 `5.702ms`、RSS `1378.9MiB`；flying 为 `309.9 FPS`、p99 `12.175ms`、RSS `2280.9MiB`；GPU 采样后 RSS 峰值 `2452.2MiB`，八会话服务端探针因 `rss=2571304960` 超过 `2GiB` 返回 exit 1。producer 未生成 JSON，未运行迁移 `perfcheck` 或 TCP，且未重跑。
- [x] 7.3 核对两个正式输出路径均不存在、无遗留进程、工作树仍在精确失败候选且干净，`docs/notes/perf-baseline-m5.json` 与 M2 baseline 哈希仍为 `9eef96e0…e5313f`、`b2d04877…cb7f93`；旧候选与失败步骤从此只作证据。

## 8. 定位并修复 v13 性能回归

- [ ] 8.1 先做不可提升的非正式诊断，不运行旧候选正式 producer：使用缩短阶段、独立 `diag` 路径和一次一个变量的临时 mutation，对比完整天空、跳过 sky draw、保留 draw 但简化 fragment 工作；结合现有阶段内存分解与 `GODEBUG=gctrace=1` 区分 Go 堆、Go runtime 和原生图形资源。每个 mutation 后恢复源码，诊断输出不得传给 `perfcheck` 或基线；把命令、HEAD、差异和结论记录到 `docs/notes/perf-baseline.md` 的失败证据段。
- [ ] 8.2 根据 8.1 的单一根因先写失败测试或可重复的最小性能检查，再最小修复 flying/GPU 采样期间的资源滞留；不得提高 `clientMemoryLimit`、`2GiB` RSS、消息/mesher/queue 上限或减少天空 workload。运行受影响包 race 测试、零分配测试与同一诊断检查，证明修复前后只有目标内存来源变化。
- [ ] 8.3 在 RSS 闭合后，用 headless 像素测试锁定正午/午夜、太阳/月亮方向、固定星图和地形遮挡，再最小减少等价 shader 工作；不得减少每帧一次 sky draw、降低 2560x1440 或加入纹理。运行 `go test ./internal/render -run 'TestSky|TestRendererRenderDoesNotAllocate' -race -count=1`，并用不可提升的 v13 诊断确认 flying p99 `<12ms`。
- [ ] 8.4 核对最终实现没有改变 draw 数量、固定分辨率、阶段时长、样本、场景运动或指标定义，保持 scenario v13 与唯一 `12:13` 迁移；若任一项必须改变，停止实现并先把 proposal、delta specs、design、tasks 和 producer 更新为 v14。

## 9. 冻结并验收新候选

- [ ] 9.1 运行受影响范围与完整无窗口门禁：`gofmt -l .`、`go test ./internal/render ./cmd/mcgo ./cmd/perfcheck -race -count=1`、`go test ./internal/archcheck -count=1`、`go test ./... -race`、`go vet ./...`、`CGO_ENABLED=0 GOOS=linux go build ./cmd/mcgod`、相关 benchmark 和 `openspec validate --all --strict --no-interactive`；确认掉落、协议、存档、二进制资产和两份现有 baseline 未改，再提交并记录新候选 HEAD。
- [ ] 9.2 新候选完整门禁退出并自然冷却至少 5 分钟后，间隔至少 30 秒采集两次 `uptime`、`pmset -g batt`、`pmset -g custom`、`pgrep -fl 'mcgo|perfcheck'`；通过后绑定新 HEAD 与两个全新路径请求新的精确一次性授权，不复用旧授权或旧路径。
- [ ] 9.3 获得新授权后只执行一次 Memory producer；成功后用 `--allow-scenario-upgrade 12:13` 执行迁移完整性与绝对门禁，失败则立即停止。仅在 Memory 通过后执行一次 TCP producer及同场景比较；通过后才把 Memory 精确字节提升为 M5 baseline，并记录 HEAD、命令、硬件、哈希、v12→v13 与旧候选失败证据。M2 baseline 保持不变。

## 10. 收尾验证

- [ ] 10.1 基线与文档更新后再次运行 `gofmt -l .`、`go test ./internal/archcheck -count=1`、`go test ./... -race`、`go vet ./...`、`CGO_ENABLED=0 GOOS=linux go build ./cmd/mcgod` 和 `openspec validate --all --strict --no-interactive`；`gofmt -l .` 必须无输出，所有命令必须成功。
- [ ] 10.2 复核 proposal、三份 delta specs、design 与已勾选 tasks 和最终实现一致，确认协议/metadata/玩家/区块版本仍为 M4G 归档值、scenario v13 及 M5 基线证据准确，再准备规格同步、评审与归档；自动验收全程不得启动交互式客户端。
