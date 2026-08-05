## 1. M4F 前置与规划复核

- [x] 1.1 在开始实现前确认 M4F 已完成 scenario v10 正式基线、delta specs 同步与归档；用 `openspec list --json`、`jq -e '.scenario_version == 10' docs/notes/perf-baseline-m5.json` 和 `rg` 核对协议 v8、metadata v1、玩家 schema v3、区块 schema v4，任一不满足则停止。
- [x] 1.2 重新读取本 change 的 proposal、三份 delta specs、design 和 tasks，并与归档后的 M4F 主规格及代码逐项比对；若 M4F 收尾改变起点，先更新本 change，再运行 `openspec validate m4g-authoritative-daylight --strict --no-interactive`。

## 2. 固定列顶与直射天空光

- [x] 2.1 在 `internal/world` 先写失败测试，覆盖空列 `MinY-1`、升序写入、非列顶修改、移除列顶、384 格最坏扫描、Clone 和从 section 重建后的 `[256]int16` 最高遮挡一致性。
- [x] 2.2 修改 `internal/world/chunk.go`，增加恰好 512 字节的固定高度表、查询和重建；让 `NewChunk`、`SetBlock`、`Clone` 及 snapshot/存档装入路径维护派生值，不改变区块 Hash、payload 或 schema。
- [x] 2.3 在 `internal/world/neighborhood_test.go` 与 `internal/mesh/greedy_test.go` 先写失败测试，覆盖露天 `0xF0`、屋顶下 `0x00`、缺失邻区为暗、跨区块列查询以及不同天空光 quad 不合并。
- [x] 2.4 修改 `internal/world/neighborhood.go` 和 `internal/mesh/greedy.go`，让不可变邻域携带九个固定高度快照并由相邻空气决定 `Quad.Light`；不得引入每面向上扫描、动态光照表或新 goroutine。
- [x] 2.5 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/world ./internal/mesh -race -count=1'`、`go test ./internal/archcheck -count=1`、`gofmt -l .` 与 `git diff --check`，通过后提交 `feat: 派生直射天空光`。

## 3. metadata v2 与原子保存接口

- [x] 3.1 在 `internal/storage` 先写失败的 golden、往返和故障注入测试，覆盖 metadata v1→v2 时间零迁移、v2 `WorldTimeTicks`、CRC、截断、未来版本拒绝；临时写入、临时文件 fsync 或 rename 失败必须保留旧文件，rename 后目录 fsync 失败则重开必须得到 CRC 有效的完整旧版或完整新版。
- [x] 3.2 修改 `internal/storage/types.go`、`metadata.go`、`memory.go`、`disk.go` 和 world files 装配：追加 metadata v2 字段与 `Store` 原子保存方法，Memory/Disk 保持相同值语义；不新增第二个世界状态文件。
- [x] 3.3 更新受影响的测试 Store 夹具和 metadata golden；验证旧程序边界只由版本拒绝表达，玩家 schema v3、区块 schema v4 及既有 chunk/player golden 字节保持不变。
- [x] 3.4 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage -race -count=1'`、archcheck、gofmt 与 diff check，通过后提交 `feat: 持久化 metadata v2 世界时间`。

## 4. 权威世界时间与协议 v9

- [ ] 4.1 在 `internal/sim` 先写失败测试，覆盖构造时恢复绝对时间、每次 `Step` 恰好加一、23999→24000 显示周期、八名玩家同 tick 发布相同时间、稳定推进零分配和重放确定性。
- [ ] 4.2 修改 `internal/sim/engine.go`、`player.go` 及构造调用，让 simulation owner 唯一持有时间并把本 tick 最终值放入固定玩家更新；不得读取墙钟、storage、network 或 render。
- [ ] 4.3 在 `internal/network` 先写失败的 codec golden、固定 payload、Validate、registry 和登录测试：协议为 v9、`PlayerState` 末尾携带 `uint64 WorldTimeTicks`、packet ID 不变、v8 在 Play 前拒绝。
- [ ] 4.4 修改 `internal/network/message.go`、`codec.go`、`packet.go`，并在 `internal/server/publication.go`、`internal/client` 与 `cmd/mcgo` 只加入编译和状态映射所需字段；客户端继续按 `ServerTick` 忽略旧状态，不增加独立时间消息。
- [ ] 4.5 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim ./internal/network ./internal/client ./internal/server -race -count=1 && go test ./internal/network -run "^$" -fuzz FuzzSmallPacketCodec -fuzztime=10s && go test ./internal/network -run "^$" -bench SmallPacketCodec -benchmem -count=3'`，再运行 archcheck、gofmt 与 diff check，通过后提交 `feat: 同步协议 v9 权威世界时间`。

## 5. 有界 metadata 自动保存与关服屏障

- [ ] 5.1 在 `internal/server` 先写失败测试，覆盖自动保存边界投递最新时间、最多一个 in-flight、普通 tick 不重复投递、in-flight 期间跨过新保存边界时合并最新值、队列满不阻塞 Step、失败按现有 tick 退避和状态可观察。
- [ ] 5.2 扩展 `internal/server/server.go` 与 `persistence.go` 的固定 save job/completion，使现有 worker/channel 处理一份 metadata 快照；metadata 使用独立固定状态，不进入 region retry map，不新增 worker 或无界队列。
- [ ] 5.3 在 `internal/server/shutdown_test.go` 和重启集成测试先覆盖最终时间 flush、失败可重试关服、context 超时、Store.Sync/Close 顺序、v1 世界迁移及 v2 重启延续，再修改 `shutdown.go` 与装配代码闭合屏障。
- [ ] 5.4 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server ./internal/storage -race -count=1'`，确认故障注入后 metadata 可重开为完整旧版或完整新版且无遗留 goroutine，再运行 archcheck、gofmt 与 diff check，通过后提交 `feat: 异步保存权威世界时间`。

## 6. 增量天空光重网格

- [ ] 6.1 在 `internal/client` 先写失败测试，覆盖 snapshot 重建高度、非列顶变化只走既有 dirty、屋顶放置/移除只标记新旧高度跨度、chunk 边/角最多跨四个 chunk、单变化不超过 96 个 section key。
- [ ] 6.2 修改 `internal/client/mirror.go`，在应用 block change 前后比较最高遮挡并追加精确垂直/水平 dirty；保持 revision、resync、forget 和只读镜像语义不变。
- [ ] 6.3 扩展 `internal/client/mesher.go` 的 clone job 和 stale-result 测试，使九个高度表与既有 ChunkStamp 同代；覆盖 in-flight 时屋顶变化只接受最新光照结果、邻区到达消除暗边界。
- [ ] 6.4 增加 `BenchmarkSkyDirtyRange`、`BenchmarkMesherSkySnapshot`，覆盖最坏高度跨度、重复 dirty 合并和稳定网格分配/调度，确认现有 job/result 容量与每帧 64 调度/回收上限不变。
- [ ] 6.5 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/client ./internal/world ./internal/mesh -race -count=1 && go test ./internal/client -run "^$" -bench "Mesher|Sky" -benchmem -count=3'`，再运行 archcheck、gofmt 与 diff check，通过后提交 `feat: 增量更新直射天空光网格`。

## 7. 固定昼夜渲染

- [ ] 7.1 在 `internal/render` 先写失败测试，精确覆盖相位 0/6000/12000/18000、`sun/daylight/terrain` 公式、日夜 clear color、有限值和周期性；新增 `TestTerrainDaylightHeadlessDraw` 并覆盖 HUD/name tag 不接收世界亮度。
- [ ] 7.2 修改 terrain renderer、camera uniform 和 WGSL，用 quad 天空光与 `daylight` 计算 8% 室内/15% 夜间露天/100% 正午亮度；只更新固定 uniform，不重新网格化或新增 pipeline/纹理。
- [ ] 7.3 修改 avatar 与 item-drop renderer/uniform/WGSL 使用相同 `daylight`，保持 name-tag、hotbar、inventory、furnace 和 mining overlay 颜色不变。
- [ ] 7.4 修改 `cmd/mcgo/app.go`，只从最后确认的 `PlayerState.WorldTimeTicks` 计算一次帧光照并传给三个世界空间 renderer；reset、重连和旧状态不得回退相位。
- [ ] 7.5 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/render ./cmd/mcgo ./internal/client -race -count=1 && go test ./internal/render -run "Test(TerrainDaylightHeadlessDraw|AvatarRendererHeadlessDraw|ItemDropRendererHeadlessDraw|HotbarRendererHeadlessBlendOverExistingColor)$" -count=1'`，确认没有创建或聚焦窗口，再运行 archcheck、gofmt 与 diff check，通过后提交 `feat: 呈现权威昼夜循环`。

## 8. Memory/TCP、重启与兼容闭环

- [ ] 8.1 在 `internal/server` 与 `internal/client` 增加 Memory/TCP 纵向测试：两客户端同 tick 时间一致、v8 登录拒绝、重连延续；把收到的权威 snapshot/change 应用到 Mirror/Mesher，证明屋顶放置后下方变暗、移除后恢复且最终 chunk hash 和时间一致。
- [ ] 8.2 增加磁盘纵向测试：v1 metadata 启动为零、自动保存迁移 v2、正常关服后的精确时间重启延续、metadata 保存失败不覆盖旧文件且关服返回错误。
- [ ] 8.3 核对协议 packet ID、玩家 schema v3、区块 schema v4、chunk snapshot payload 和历史 golden 不变；用 `rg`/测试证明高度表及光照没有进入网络或区块存档。
- [ ] 8.4 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server ./internal/network ./internal/storage ./internal/client ./internal/render -race -count=1'`、archcheck、gofmt 与 diff check，通过后提交 `test: 闭合多人昼夜与重启语义`。

## 9. scenario v11 与中文文档

- [ ] 9.1 在 `cmd/mcgo` 与 `cmd/perfcheck` 先写失败测试：producer 标记 v11、v10/v11 默认拒绝、仅 `10:11` 可显式迁移、`9:10` 退役、历史 v6-v10 可读取、v11 同场景与跨 transport 继续执行原门禁。
- [ ] 9.2 修改 benchmark producer/comparator 为 scenario v11，不改变 2560x1440、still/flying、RSS、服务端 tick、2048 GPU 样本、Memory/TCP、绝对阈值或 `20%` 相对阈值；M2 与既有 M5 文件暂不改写。
- [ ] 9.3 更新 `README.md`、`docs/notes/lan-server.md`、`docs/notes/perf-baseline.md`，说明 24000 tick 昼夜、直射天空光上限、metadata v2、协议 v9、备份/回退、scenario v11 与未实现的方块光/传播。
- [ ] 9.4 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo ./cmd/perfcheck ./internal/network -race -count=1'`、`openspec validate --all --strict --no-interactive`、`gofmt -l .` 与 `git diff --check`，通过后提交 `feat: 升级 benchmark scenario v11`。

## 10. 候选版本完整门禁

- [ ] 10.1 对 proposal、三份 delta specs、design、tasks 与实现逐项映射；确认没有横向传播、方块光、透明方块、天气、天体、怪物规则、独立光照 worker 或额外存档负载。
- [ ] 10.2 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race'`、`go vet ./...`、`go test ./internal/archcheck -count=1`、`gofmt -l .`、`git diff --check` 和 `openspec validate --all --strict --no-interactive`；任何失败只修根因，不放宽门禁或绕过 Hook。
- [ ] 10.3 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "^$" -fuzz FuzzSmallPacketCodec -fuzztime=10s && go test ./internal/network -run "^$" -bench SmallPacketCodec -benchmem -count=3 && go test ./internal/world ./internal/client -run "^$" -bench "Height|Sky|Mesher" -benchmem -count=3 && go test ./internal/storage ./internal/server -run "Metadata|WorldTime" -race -count=1 && go test ./internal/render -run "Test(TerrainDaylightHeadlessDraw|AvatarRendererHeadlessDraw|ItemDropRendererHeadlessDraw)$" -count=1'`；确认无前台窗口、无遗留 benchmark 进程、tracked 工作树只含 M4G 预期文件。
- [ ] 10.4 勾选已完成任务并提交冻结候选 `chore: 关闭 M4G 权威昼夜`；提交后不修改 producer、场景、阈值、光照模型或热路径，除非新建修复提交并重新完成本组门禁。

## 11. 一次性 M5 scenario v11 基线

- [ ] 11.1 在冻结候选上再次证明 M4F v10 基线与归档存在，记录精确 HEAD、M2/M5 哈希、硬件/系统/Go、供电与负载，确认两个全新输出路径不存在且无遗留进程；向用户报告并取得 Memory/TCP 各一次、失败即停且不得重跑的明确授权。
- [ ] 11.2 仅通过现有无窗口 benchmark 生成一次 M5 Memory v11 报告；用 v10 M5 基线和显式 `10:11` 执行完整性与绝对门禁，失败立即停止，不生成 TCP、不重跑、不覆盖基线。
- [ ] 11.3 Memory 通过后生成一次同 HEAD 的 M5 TCP v11 报告，并执行 TCP 自校验及 Memory→TCP 同场景比较；失败立即停止，不重跑或覆盖基线。
- [ ] 11.4 两步都通过后，把 Memory 报告精确字节写入 `docs/notes/perf-baseline-m5.json`，更新性能记录的 HEAD、命令、哈希、环境和被替代 v10 身份；验证 M2 文件哈希未变后提交 `chore: 建立 M5 scenario v11 基线`。

## 12. 最终同步与归档

- [ ] 12.1 重新运行全仓 race、vet、archcheck、gofmt、diff check、OpenSpec strict 与适用性能比较，确认全部任务勾选且 tracked 工作树干净。
- [ ] 12.2 把三份 delta specs 同步到主规格，核对 `authoritative-daylight` purpose、metadata v2、协议 v9、玩家 schema v3、区块 schema v4、scenario v11 和 M5/M2 边界准确；同步更新 `AGENTS.md` 与 `openspec/config.yaml` 的当前基线。
- [ ] 12.3 归档 `m4g-authoritative-daylight` change，再次运行 `openspec validate --all --strict --no-interactive` 与 `git diff --check`，提交 `chore: 归档 M4G 权威昼夜`。
