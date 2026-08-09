> 本清单逐项对应 `docs/superpowers/plans/2026-08-09-m4n-static-block-light.md` 的 Task 2–9；实现发现计划与规格不一致时先更新本 change，不自动归档。

## 2. 追加发光块资源、物品与采掘规则

- [x] 2.1 在 `internal/core/light_block_test.go`、`internal/assets/blocks_test.go` 与 `internal/sim/mining_test.go` 先锁定稳定 ID、`64` 堆叠、放置/掉落、无第七配方、独立材质、`Emission=15` 与 `30/15/8` tick，并以 `zsh -ic 'go test ./internal/core ./internal/assets ./internal/sim -run "LightBlock|MiningRule" -count=1'` 确认红灯。
- [x] 2.2 最小修改 `internal/core/{block.go,item.go}`、`internal/assets/{blocks.go,procedural.go}` 与 `internal/sim/mining.go`，复用既有 switch 和石砖采掘分支；以 `zsh -ic 'go test ./internal/core ./internal/assets ./internal/sim -race -count=1'` 验证资源、错误工具无掉落和容量不足原子性。
- [x] 2.3 对 Task 2 文件执行 `gofmt -w`，再运行 `zsh -ic 'go test ./internal/archcheck -count=1'` 与 `gofmt -l internal/core internal/assets internal/sim`；后者 MUST 无输出。

## 3. 升级协议 v14 与区块 schema v7

- [x] 3.1 在 `internal/network` 与 `internal/storage` 先锁定 v14 握手、v13 拒绝、v13→v14 所有 packet payload 长度不变且保留既有生命值字节、packet golden、v6→v7 no-op、v7 发光块/掉落 roundtrip、玩家 v5 与 v6 fixture hash；以 `zsh -ic 'go test ./internal/network ./internal/storage -run "Protocol|CodecGolden|ChunkV6|ChunkV7|LightBlock" -count=1'` 确认红灯。
- [x] 3.2 只把 `network.ProtocolVersion` 升为 `14`、`currentChunkSchema` 升为 `7` 并注册 `6→7` no-op；生成 `internal/storage/testdata/chunk-v7.bin`、保留 v6 fixture 字节并追加 fuzz seed，以 `zsh -ic 'go test ./internal/storage -run "ChunkV6|ChunkV7|PlayerSchemaV5" -count=1'` 验证。
- [x] 3.3 对 Task 3 Go 文件执行 `gofmt -w`，运行 `zsh -ic 'go test ./internal/network ./internal/storage -race -count=1'`、`zsh -ic 'go test ./internal/storage -run "Future|CRC|Trunc|Trailing|Migration" -count=1'`、`zsh -ic 'go test ./internal/storage -run=^$ -fuzz=FuzzDecodeChunkPayload -fuzztime=10s'`、`zsh -ic 'go test ./internal/archcheck -count=1'`、`gofmt -l internal/network internal/storage` 与 `git diff --check`。

## 4. 在固定 scratch 中实现 packed 双通道传播

- [x] 4.1 将 `internal/mesh/skylight*.go` 机械更名为 `light*.go`，同步 `LightScratch`/`NewLightScratch` 调用点并保持天空光算法不变；以 `zsh -ic 'go test ./internal/mesh ./internal/client ./internal/render ./cmd/gfxspike -run "SkyLight|Mesh|Mesher" -count=1'` 验证纯重命名。
- [x] 4.2 在 `internal/mesh/light_test.go` 与 `light_internal_test.go` 先覆盖 `15/14/1/0`、仅 `AirID` 传播、未来被标记为透明的非空气方块仍阻断、多源最大值、跨区段/区块、缺失邻区、确定性、精确 `48³` 容量、一次 emission 扫描、队列复用和零分配；以 `zsh -ic 'go test ./internal/mesh -run "BlockLight|PackedSky|LightScratch" -count=1'` 确认方块光红灯。
- [x] 4.3 在 `internal/mesh` 实现一个 packed levels/queue、先天空光后所有光源统一入队且只进入 `AirID` 邻格的方块光 BFS，并让 `MeshSection` 原样写 packed byte；只同步 `internal/client/mesher.go`、`cmd/gfxspike/main.go` 与 `internal/render/bench_test.go` 的必要调用点。
- [x] 4.4 对 Task 4 Go 文件执行 `gofmt -w`，运行 `zsh -ic 'go test ./internal/mesh -race -count=1'`、`zsh -ic 'go test ./internal/client ./internal/render ./cmd/gfxspike -race -count=1'`、`zsh -ic 'go test ./internal/mesh -run ^$ -bench BenchmarkMeshTerrainSection -benchmem -count=5'`、`zsh -ic 'go test ./internal/archcheck -count=1'`、`gofmt -l internal/mesh internal/client cmd/gfxspike internal/render`，并以 `rg -n "SkyLightScratch|NewSkyLightScratch|skylight.go" . --glob '!docs/superpowers/**'` 确认无代码命中。

## 5. 验证 mesher 收敛并接入 shader 合光

- [x] 5.1 在 `internal/client/skylight_test.go` 先锁定发光块普通 dirty `<=27` 且所有实际受影响区段完整覆盖、列顶 dirty `<=216` 与移除后的 stale result 拒绝；运行 `zsh -ic 'go test ./internal/client -run "LightBlock|StaleBlockLight" -count=1'`，若既有 dirty 用例已绿则不修改 `mirror.go`。
- [x] 5.2 在 `internal/render/daylight_test.go` 先用 `0xf0`、`0x0f`、`0x88` 锁定正午/午夜、天空与方块光竞争及 AO/朝向；以 `zsh -ic 'go test ./internal/render -run TestTerrainDaylightHeadlessDraw -count=1'` 确认旧 shader 红灯。
- [x] 5.3 只修改 `internal/render/shader/terrain.wgsl` 为 `max(0.08 + sky*(daylight-0.08), block)`，不增加渲染资源；对测试执行 `gofmt -w` 后运行 `zsh -ic 'go test ./internal/client ./internal/render -race -count=1'`、`zsh -ic 'go test ./internal/archcheck -count=1'`、`gofmt -l internal/client internal/render` 与 `git diff --check`。

## 6. 增加 block-light-room 无窗口视觉场景

- [x] 6.1 在 `cmd/mcgo/capture_test.go` 先锁定 `block-light-room` 位于场景末尾、通过 mirror/mesher 构建封闭房间、唯一光源和 Apply 完整重置状态；以 `zsh -ic 'go test ./cmd/mcgo -run "BlockLightRoom|CaptureScene" -count=1'` 确认红灯。
- [x] 6.2 在 `cmd/mcgo/capture.go` 复用现有空气邻域 helper，按批准坐标构造午夜封闭房间并追加末场景，不创建场景 DSL；执行 `gofmt -w cmd/mcgo/capture.go cmd/mcgo/capture_test.go` 与 `zsh -ic 'go test ./cmd/mcgo -run "Capture|BlockLightRoom|Visual" -count=1'`。
- [x] 6.3 运行 `zsh -ic 'go run ./cmd/mcgo --capture /private/tmp/mcgo-m4n-capture --update-golden'` 生成唯一新增 `cmd/mcgo/testdata/golden/block-light-room.png`，再运行 `zsh -ic 'go run ./cmd/mcgo --capture /private/tmp/mcgo-m4n-capture-verify'`；人工确认中央暖色光源、近远衰减、午夜无天空伪亮且房外无边界漏光，不调整既有阈值。

## 7. 完成 Memory/TCP 放置、照明、挖回纵向闭环

- [x] 7.1 在 `internal/server/block_light_integration_test.go` 使用同一脚本和既有 parity transport，先断言放置前低四位为 `0`、放置后非零、挖回后为 `0`，并比较 Memory/TCP 的区块、revision、背包与掉落 hash；以 `zsh -ic 'go test ./internal/server -run TestStaticBlockLightMemoryTCPParity -count=1'` 确认红灯。
- [x] 7.2 测试只注入初始发光块物品与石镐，所有方块、物品、耐久、掉落和持久化变化走真实权威路径，方块光只通过生产 assets/mirror/mesher 读取；执行 `gofmt -w internal/server/block_light_integration_test.go`、`zsh -ic 'go test ./internal/server -run "StaticBlockLight|MiningMemoryTCPParity" -race -count=1'`、`zsh -ic 'go test ./internal/server -race -count=1'`、`zsh -ic 'go test ./internal/archcheck -count=1'` 与 `gofmt -l internal/server`。
- [x] 7.3 临时让 `Emission(LightBlockID)` 返回 `0`，确认 `TestStaticBlockLightMemoryTCPParity` 的 `PlacedLight` 断言失败；恢复生产实现并重跑 `zsh -ic 'go test ./internal/server -run TestStaticBlockLightMemoryTCPParity -race -count=1'` 为绿。

## 8. 升级 scenario v15 并生成 M5 记录

- [ ] 8.1 在 `cmd/mcgo` 与 `cmd/perfcheck` 先锁定 producer v15、默认拒绝 v14/v15、唯一 `14:15`、拒绝 `13:14` 和跨 transport 迁移、同 commit v15 显式跨 transport；以 `zsh -ic 'go test ./cmd/mcgo ./cmd/perfcheck -run "ScenarioVersion|ScenarioUpgrade|StaticBlockLight" -count=1'` 确认红灯。
- [ ] 8.2 只把 `scenarioVersion` 改为 `15`、唯一授权字符串改为 `14:15` 并删除 active `13:14` 接受分支；对相关 Go 文件执行 `gofmt -w` 后运行 `zsh -ic 'go test ./cmd/mcgo ./cmd/perfcheck -race -count=1'`、`zsh -ic 'go test ./internal/client ./internal/mesh ./internal/render -race -count=1'` 与 `git diff --check`。
- [ ] 8.3 冻结 `/private/tmp/mcgo-m4n-v15` 与当前 HEAD，运行完整 Memory v15 producer，并以 `zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/perfcheck --baseline docs/notes/perf-baseline-m5.json --current '/private/tmp/mcgo-m4n-v15/memory-v15.json' --max-regression 0.20 --allow-scenario-upgrade 14:15"` 校验完整性和硬件身份。
- [ ] 8.4 将已验证 Memory 报告精确复制为 `docs/notes/perf-baseline-m5.json` 并用 `cmp -s` 证明字节相同；独立生成 `/private/tmp/mcgo-m4n-v15/tcp-v15.json`，只用它自身运行同版本 perfcheck，不自动执行 Memory/TCP 比较。
- [ ] 8.5 在 `docs/notes/perf-baseline.md` 与 `perf-baseline-m5.md` 记录 HEAD、硬件/OS/Go、正式命令、路径和 SHA-256，并以 `shasum -a 256 docs/notes/perf-baseline.json docs/notes/perf-baseline-m5.json /private/tmp/mcgo-m4n-v15/memory-v15.json /private/tmp/mcgo-m4n-v15/tcp-v15.json`、M5 baseline 自比较和 `git diff --check` 验证 M2 v6 未变及 M5 v15 可读取。

## 9. 更新现状文档并运行全部门禁

- [ ] 9.1 更新 `README.md`、`docs/notes/lan-server.md`、`AGENTS.md`、`CLAUDE.md` 与 `openspec/config.yaml` 的当前现状为 M4N/v14/chunk v7/player v5/metadata v2/scenario v15，并明确无正常获取入口、可信 LAN 和回退恢复备份；用 `rg -n "当前.*M4M|协议 v13|Protocol v13|区块 schema v6|scenario v14|13:14|skylight-tunnel.*末|低四位.*0" README.md docs/notes/lan-server.md AGENTS.md CLAUDE.md openspec/config.yaml` 审计仅剩历史语境。
- [ ] 9.2 根据实际证据逐项更新本 `tasks.md` checkbox；运行 focused race、`zsh -ic 'go test ./internal/archcheck -count=1'`、`zsh -ic 'go test ./... -race'`、`zsh -ic 'go vet ./...'`、`gofmt -l .`、`openspec validate --all --strict --no-interactive` 与 `git diff --check`，所有命令 MUST 成功且 gofmt MUST 无输出。
- [ ] 9.3 重跑 `zsh -ic 'go test ./internal/storage -count=1'`、10 秒 `FuzzDecodeChunkPayload`、`zsh -ic 'go run ./cmd/mcgo --capture /private/tmp/mcgo-m4n-final-capture'`、M5 baseline 自比较与 `cmp -s /private/tmp/mcgo-m4n-v15/memory-v15.json docs/notes/perf-baseline-m5.json`，证明 golden/fuzz/capture/perf artifact 未漂移。
- [ ] 9.4 用 `git status --short`、`git diff --stat main...HEAD`、`git diff --check main...HEAD` 和计划 Task 9 Step 6 的两组交付面/非目标 `rg` 做范围审计；请求独立代码评审并修复范围内问题后保持 change active，只有用户明确要求时才 sync/archive、推送或创建 PR。
