## 1. 冻结 M4G 前置与 M4H 契约

- [x] 1.1 在实现前运行 `openspec list --json`、`openspec validate --all --strict --no-interactive`、`test -d openspec/changes/archive/2026-08-05-m4g-authoritative-daylight`，并用 `rg -n 'ProtocolVersion uint32 = 9|currentPlayerSchema.*= 3|currentChunkSchema.*= 4|currentMetadataVersion.*= 2|"scenario_version": 12' internal docs/notes/perf-baseline-m5.json` 核对 M4G 基线；任一条件不满足则停止并先更新本 change 产物。
- [x] 1.2 对 `proposal.md`、两份 delta spec、`design.md`、`tasks.md` 做一次范围映射并运行 `openspec validate --all --strict --no-interactive`，确认只包含单件原地丢弃、协议 v10、来源延迟合并和共享掉落物合法性修正，不包含物理、整组丢弃、死亡掉落、新 UI、WAL/ECS 或 benchmark 场景升级。

## 2. 协议 v10 与已注册掉落物边界

- [x] 2.1 在 `internal/network` 先写失败测试：固定 `ProtocolVersion == 10`，v9 在 Play 前拒绝，`DropSelectedItem{Sequence: 0x1122334455667788}` 使用 Play client packet ID `11` 与 payload `8877665544332211`，ID `1` 保持未分配，既有 ID/payload golden 不变；同时证明煤炭和铁镐 upsert 合法、未知 ItemID 仍整体拒绝。
- [x] 2.2 修改 `internal/network/message.go`、`registry.go`、`codec.go`、`packet.go` 及最少相关测试，加入只含 `Sequence` 的封闭消息/packet 并把协议升为 v10；把 `ItemDrop.validate` 的合法性根因统一为 `core.RegisteredItem`，不得增加第二份物品白名单或改动其他 packet ID。
- [x] 2.3 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -race -count=1 && go test ./internal/network -run "^$" -fuzz FuzzSmallPacketCodec -fuzztime=10s && go test ./internal/network -run "^$" -bench SmallPacketCodec -benchmem -count=3'`，再运行 `go test ./internal/archcheck -count=1`、`gofmt -l .` 与 `git diff --check`；通过后提交协议与校验修正。

## 3. 权威单件丢弃状态转移

- [x] 3.1 在 `internal/world/drop_test.go`、`internal/sim/drop_test.go`、`engine_test.go` 和 `bench_test.go` 先写失败测试，覆盖 Active 成功、最后一件清空、煤炭/镐、同位置合并时保留 ID/年龄且延迟取 `max(old,incoming)`、空栏位、区块非 Ready、32 槽满、重复/过期序号、同 tick 选栏/丢弃顺序、40 tick 延迟及稳定哈希；所有拒绝必须证明背包、掉落物、revision 和 persistence pending 无变化。
- [x] 3.2 在 `internal/world/drop.go`、`internal/sim/command.go`、`engine.go`、`drop.go` 做最小实现：把 `CommitDrop` 的共享合并延迟改为 `max(old,incoming)`，新增 `CommandDropSelectedItem`，在既有全局序号分支内用权威选中格和脚底 floor 坐标，先调用 `PrepareDrop` 再用 `Hotbar.Consume`/`CommitDrop` 提交，复用 `touchChunk` 与 `inventoryDirty`；不得新增锁、goroutine、接口、动态队列或客户端字段。
- [x] 3.3 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim ./internal/world ./internal/core -race -count=1 && go test ./internal/sim -run "^$" -bench "DropSelectedItem" -benchmem -count=3'`，确认成功、合并和满容量路径保持固定上限，再运行 archcheck、gofmt 与 diff check；通过后提交权威状态转移。

## 4. 服务端映射与 Memory 多人闭环

- [x] 4.1 在 `internal/server/session_test.go`、`drop_publication_test.go` 与 `multiplayer_memory_integration_test.go` 先写失败测试，覆盖线上命令只映射为同序号 sim 命令、成功时所属玩家收到完整 `InventoryState`、兴趣玩家收到相同 `ItemDropUpserts`、失败只向发起者返回既有拒绝，慢/异常会话不阻塞健康会话。
- [x] 4.2 修改 `internal/server/session.go` 的封闭消息分发并复用现有 `networkRejectReason`、inventory/drop publication 和保存调度；不得在 server 保存第二份背包或掉落物真相，也不得新增消息、worker 或队列。
- [x] 4.3 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server ./internal/sim ./internal/network -race -count=1'`、archcheck、gofmt 与 diff check；通过后提交服务端 Memory 闭环。

## 5. Q 边沿输入与无预测客户端

- [x] 5.1 在 `internal/client/input_test.go` 与 `cmd/mcgo/main_test.go`、`app_test.go` 先写失败测试，覆盖 Q 上升沿只触发一次、按住不重复、容器打开/鼠标未捕获/刚捕获/未 Ready 时不发送、无效阶段一直按住后恢复不会误触发，以及发送失败或拒绝不本地扣物品/造掉落物。
- [x] 5.2 修改 `internal/client/window.go`、`input.go`、`cmd/mcgo/main.go`、`app.go`：在既有最小按键表和 `InputState` 追加 Q/Drop 状态，`allowActions` 且 predictor Ready 时用 `nextSequence()` 发送 `DropSelectedItem`；不新增 renderer、HUD、镜像字段或独立按键状态。
- [x] 5.3 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/client ./cmd/mcgo -race -count=1'`，确认测试不创建或聚焦前台窗口，再运行 archcheck、gofmt 与 diff check；通过后提交图形客户端接线。

## 6. TCP、重启与存档不变证明

- [x] 6.1 在 `internal/server/tcp_integration_test.go` 增加真实 loopback 纵向测试：v10 客户端丢弃煤炭或镐，发起者与第二客户端最终观察相同背包、drop ID/value 和 revision；v9 握手在玩家加载前拒绝，容量失败后连接仍健康。
- [x] 6.2 在 `internal/server` 增加磁盘重启纵向测试：成功丢弃后正常关服，重开同一世界与玩家时背包少一件且区块保留掉落物年龄/剩余延迟；核对玩家 v3、区块 v4、metadata v2 golden 字节和文件集合不变，不新增迁移器或状态文件。
- [x] 6.3 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server ./internal/network ./internal/storage ./cmd/mcgod -race -count=1 && CGO_ENABLED=0 GOOS=linux go build -o /private/tmp/mcgod-m4h ./cmd/mcgod'`、archcheck、gofmt 与 diff check；通过后提交 TCP 与重启闭环。

## 7. 文档与性能场景保持 v12

- [x] 7.1 更新 `README.md` 与 `docs/notes/lan-server.md`：说明 Q 单件原地丢弃、40 tick 拾取延迟、无投掷物理、协议 v10、v9 拒绝、三种存档版本不变、正常关服/回退步骤和玩家/区块异常退出非事务边界。
- [x] 7.2 用 `cmd/mcgo`、`cmd/perfcheck` 测试和 `rg` 证明默认 workload、报告 schema 与 scenario v12 均未改变，M2/M5 baseline 文件字节不变；不得生成、提升或放宽任何性能基线。
- [x] 7.3 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo ./cmd/perfcheck ./cmd/mcgod ./internal/network -race -count=1'`、`openspec validate --all --strict --no-interactive`、gofmt 与 diff check；通过后提交文档与兼容说明。

## 8. 候选版本完整门禁

- [x] 8.1 逐条把 proposal、delta spec、design、tasks 映射到实现和测试；确认全部已注册物品可经线上掉落物传播，且没有物理、整组丢弃、死亡掉落、客户端预测、新 UI、WAL/ECS、额外存档负载或 scenario 变更。
- [x] 8.2 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race'`、`go vet ./...`、`go test ./internal/archcheck -count=1`、`gofmt -l .`、`git diff --check` 与 `openspec validate --all --strict --no-interactive`；任何失败只修根因，不放宽门禁或绕过 Hook。
- [x] 8.3 运行协议 fuzz/small-packet benchmark、`BenchmarkDropSelectedItem`，以及 Memory/TCP/磁盘重启定向测试；确认无前台窗口、无遗留进程、tracked 工作树只含 M4H 预期文件后，提交冻结候选。
- [ ] 8.4 审核修复先写失败测试：一个含两把同类镐的可拾取地面堆必须拆入两个合法栏位；同 tick 序号较早的有效放置必须先消耗最后一个物品，较晚主动丢弃返回 `invalid_slot` 且不创建掉落物。
- [ ] 8.5 最小修复共享根因：`Inventory.AddStack` 接受数量 `1..64` 的已注册来源堆并继续按 `ItemStackLimit` 写入合法栏位；`Engine.Step` 用既有有界命令切片按序执行放置、选栏和主动丢弃，再推进掉落物，不新增协议、存档字段、锁、goroutine 或无界状态。
- [ ] 8.6 运行 `internal/core`、`internal/world`、`internal/sim`、`internal/server`、`internal/network`、`internal/client` 与 `cmd/mcgo` 的无窗口 race 测试，再运行全仓 race、vet、archcheck、gofmt、diff check、OpenSpec strict validate 和相关 benchmark；通过后提交审核修复候选。

## 9. 主规格同步与归档

- [ ] 9.1 把 `authoritative-item-dropping` 新 capability 和 `persistent-item-drops` 修改 delta 同步到 `openspec/specs/`，用 `openspec validate --all --strict --no-interactive` 核对协议 v10、玩家 schema v3、区块 schema v4、metadata v2、scenario v12、来源延迟及已注册物品语义一致，并更新 `AGENTS.md`、`openspec/config.yaml` 的当前基线为 M4H。
- [ ] 9.2 在全部任务和验证通过后归档 `m4h-authoritative-item-dropping`，再次运行 `openspec validate --all --strict --no-interactive`、`git diff --check` 与最终 git 状态检查，再提交归档结果。
