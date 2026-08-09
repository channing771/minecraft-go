## Task 2–19 通用执行协议

开始每项任务前 MUST 运行 `git status --short --branch`、`git merge-base --is-ancestor 96c4aae HEAD` 和 `openspec list --json`，确认分支为 `codex/m4o-code-organization`、工作树干净、HEAD 包含 `96c4aae`、M4O 是唯一实施中的 active change，且 `m4n-static-block-light` 仍为 complete。每项声明搬迁 MUST 遵循 baseline green → move → green；只有 focused 门禁通过后才能勾选该任务，并单独提交该任务列出的文件与对应 `tasks.md` checkbox。不得新增第三方依赖，不得关闭、改写 Hook 或设置豁免变量绕过失败。

## 2. 架构守卫支持职责文件族

- [x] 2.1 将 `internal/archcheck/deps_test.go` 按 source/dependency/platform/helper 拆分，让 mcgo、server 与 session 守卫扫描完整生产文件集合并保持原断言；focused：`zsh -ic 'go test ./internal/archcheck -race -count=1'`、`zsh -ic 'go test ./cmd/mcgo ./internal/server -run "LoginStreams|Session" -count=1'`。

## 3. WebGPU 后端按资源职责拆分

- [x] 3.1 将 `internal/gfx/wgpu.go` 原样拆为 convert/pipeline/resource/surface/encoder 文件，保留 Darwin build tag、CGO bridge 与 release 顺序；focused：`zsh -ic 'go test ./internal/gfx -race -count=1'`、`zsh -ic 'go test ./cmd/gfxspike ./cmd/mcgo -run "Headless|GPU|Render" -count=1'`。

## 4. network 按消息领域和方向拆分

- [x] 4.1 拆分 `internal/network/message.go`、`codec.go` 与 `codec_test.go`，保持 packet ID、payload bytes、校验顺序和错误文本；focused：`zsh -ic 'go test ./internal/network -race -count=1'`、`zsh -ic 'go test ./internal/network -run "Golden|ProtocolV14|Malformed|Semantic" -count=1'`、`zsh -ic 'go test ./internal/network -run=^$ -fuzz=FuzzSmallPacketCodec -fuzztime=10s'`。

## 5. 区块 codec 按 envelope 与逻辑载荷拆分

- [x] 5.1 拆分 `internal/storage/chunk_codec.go` 与测试，保持 schema v7、envelope、Zstd、CRC、字段顺序、fixture 和错误文本；focused：`zsh -ic 'go test ./internal/storage -race -count=1'`、`zsh -ic 'go test ./internal/storage -run "ChunkPayload|FutureSchema|Migration" -count=1'`、`zsh -ic 'go test ./internal/storage -run=^$ -fuzz=FuzzDecodeChunkPayload -fuzztime=10s'`。

## 6. 模拟引擎按 tick 阶段拆分

- [x] 6.1 将 `internal/sim/engine.go` 原样拆为 step/run/subscription/placement/changes 文件，保持 tick 调用顺序与权威状态；focused：`zsh -ic 'go test ./internal/sim -race -count=1'`、`zsh -ic 'go test ./internal/server ./internal/client -run "Tick|Placement|Subscription|Predictor" -count=1'`。

## 7. session、Host 与发布职责拆分

- [ ] 7.1 拆分 `internal/server/session.go`、`host.go`、`publication.go`，保持 session generation、heartbeat、interest、outbox、容量和关闭顺序；focused：`zsh -ic 'go test ./internal/server -race -count=1'`、`zsh -ic 'go test ./internal/server -run "Session|Heartbeat|Host|Publication|Interest" -count=1'`。

## 8. 世界持久化调度拆分

- [ ] 8.1 拆分 `internal/server/persistence.go` 与 `persistence_test.go` 为 worker/metadata/schedule/retry/status 及场景测试，保持 autosave/retry/backpressure 和 snapshot 所有权；focused：`zsh -ic 'go test ./internal/server -run "Persistence|Save|Autosave|Retry" -race -count=1'`、`zsh -ic 'go test ./internal/server -race -count=1'`。

## 9. 玩家持久化生命周期拆分

- [ ] 9.1 拆分 `player_persistence.go` 与相关 persistence/flush 测试，复用现有 scheduler 并保持 save revision 身份；focused：`zsh -ic 'go test ./internal/server -run "PlayerPersistence|PlayerSave|PlayerFlush" -race -count=1'`、`zsh -ic 'go test ./internal/server -race -count=1'`。

## 10. 服务端大型集成测试按场景拆分

- [ ] 10.1 将 TCP、Host、多人 Memory/TCP 大型测试拆成 restart/parity/furnace/capacity/lifecycle/gameplay/mining/cancel 场景和同包 helper，保持全部测试名；focused：`zsh -ic 'go test ./internal/server -race -count=1'`、`zsh -ic 'go test ./internal/server -run "TCPPlayer|Parity|Mining|Furnace|Host|Multiplayer" -count=1'`，并对比拆分前后 `func Test` 名单。

## 11. 客户端 mesher 与 predictor 拆分

- [ ] 11.1 拆分 `internal/client/mesher.go`、`predictor.go` 与 predictor 测试，复用现有 ready queue 并保持 fixed-step/reconciliation 顺序；focused：`zsh -ic 'go test ./internal/client -race -count=1'`、`zsh -ic 'go test ./internal/client -run "Mesher|Predictor|Reconcile|Presentation" -count=1'`、`zsh -ic 'go test ./internal/mesh ./internal/render -run "Mesh|Light|Render" -count=1'`。

## 12. Renderer 上传与绘制职责拆分

- [ ] 12.1 在 `internal/render/renderer.go` 保留核心 lifecycle，只将 upload 和 draw 职责原样拆到 `renderer_upload.go`、`renderer_draw.go`，保持 upload budget、slot origin、render pass 与 release 顺序；focused：`zsh -ic 'go test ./internal/render -race -count=1'`、`zsh -ic 'go test ./internal/render -run "Renderer|Upload|Render|Allocation" -count=1'`。

## 13. 完整 HUD 提取为唯一新包

- [ ] 13.1 将 `internal/render/hotbar.go`、测试和 shader 移至 `internal/render/hud` 的 renderer/layout/container/health/atlas/encode 职责文件，迁移 mcgo 调用方并登记精确白名单；focused：`zsh -ic 'go test ./internal/render ./internal/render/hud ./cmd/mcgo -race -count=1'`、`zsh -ic 'go test ./internal/render/hud -run "Hotbar|Inventory|Furnace|Chest|Health|Recipe" -count=1'`、`zsh -ic 'go test ./internal/archcheck -race -count=1'`、`VISUAL_OUT=/private/tmp/mcgo-m4o-hud-visual make visual-check`，且移动前后 shader hash 必须相同。

## 14. mcgo 生产装配按应用生命周期拆分

- [ ] 14.1 将 `cmd/mcgo/app.go` 原样拆为 dependencies/metrics/startup/lifecycle/frame/messages/input/render 文件并保留 Darwin build tag；focused：`zsh -ic 'go test ./cmd/mcgo -race -count=1'`、`zsh -ic 'go test ./cmd/mcgo -run "Application|Frame|Connection|Inventory|Render" -count=1'`、`zsh -ic 'go test ./internal/archcheck -race -count=1'`。

## 15. mcgo 应用测试按场景拆分

- [ ] 15.1 将 `cmd/mcgo/app_test.go` 拆为 protocol/render/connection/input/celestial/helper 文件，保留 Darwin build tag、测试名、fake、timeout 和资源断言；focused：`zsh -ic 'go test ./cmd/mcgo -race -count=1'`、`zsh -ic 'go test ./internal/archcheck -count=1'`，并对比拆分前后 `func Test` 名单。

## 16. mcgo 入口、交互与视觉 capture 拆分

- [ ] 16.1 拆分 `main.go`/`main_test.go` 与 `capture.go`/`capture_test.go` 为 options/run/interactive/capture scene/image 文件，严格保留各自 build tag 状态；focused：`zsh -ic 'go test ./cmd/mcgo -race -count=1'`、`zsh -ic 'go test ./cmd/mcgo -run "ParseMainOptions|RunWithDependencies|Interactive|Capture|Golden|PNG" -count=1'`、`VISUAL_OUT=/private/tmp/mcgo-m4o-capture-visual make visual-check`。

## 17. benchmark 场景、测量与报告职责拆分

- [ ] 17.1 拆分 `benchmark.go`、`multiplayer_benchmark.go` 与大型测试，保持 scenario v15、固定 workload、sample count、绝对门禁、报告 JSON 和 baseline 字节；focused：`zsh -ic 'go test ./cmd/mcgo -race -count=1'`、`zsh -ic 'go test ./cmd/mcgo -run "ScenarioV15|BenchmarkServer|BenchmarkReport|PerformanceThresholds" -count=1'`、`zsh -ic 'go test ./internal/render ./internal/server -run ^$ -bench "RemoteAvatarNameTag|EightPlayerInterest" -benchmem -count=3'`。

## 18. perfcheck 比较与阈值职责拆分

- [ ] 18.1 拆分 `cmd/perfcheck/main.go` 与测试为 compare/validate/regression/CLI 等职责文件，保持唯一迁移 `14:15`、20% 相对比较、噪声与输出文本；focused：`zsh -ic 'go test ./cmd/perfcheck -race -count=1'`、`zsh -ic 'go test ./cmd/perfcheck -run "ScenarioUpgrade|CrossTransport|Threshold|NoiseFloor|PersistenceTail" -count=1'`、`zsh -ic 'go run ./cmd/perfcheck --baseline docs/notes/perf-baseline.json --current docs/notes/perf-baseline.json --max-regression 0.20'`。

## 19. 全仓审计、artifact 保真与最终门禁

- [ ] 19.1 逐包确认计划点名文件为 split/move、其余基线文件为 keep，核对 386 个文件、测试/Benchmark/Fuzz 入口名以及 storage fixture、视觉 golden、性能 baseline 字节；随后运行 `zsh -ic 'go test ./internal/archcheck -count=1'`、`zsh -ic 'go test ./... -race'`、`zsh -ic 'go vet ./...'`、`gofmt -l .`、`openspec validate --all --strict --no-interactive`、`git diff --check`，完成独立 review 后保持 change active。
