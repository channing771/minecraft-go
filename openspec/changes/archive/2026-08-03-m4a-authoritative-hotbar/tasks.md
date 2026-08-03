> 执行规则：严格按组完成 red → green → refactor；每组验证通过且所有直接依赖包可编译后创建恰好一个聚焦提交，再自动进入下一组。所有验证均为 headless，不运行或聚焦 `mcgo` 窗口。所有 Go 命令使用本机 GVM 的 Go 1.26.0，不下载 Go。

## 1. 固定物品与快捷栏值模型

- [x] 1.1 在 `internal/core/item_test.go` 先写失败测试，覆盖稳定 `ItemID`、9 格/64 堆叠校验、同类优先与最低空位 `Add`、规范化 `Consume`、石头/泥土/草双向映射，以及未知值拒绝。
- [x] 1.2 在 `internal/core/item.go` 实现最小固定值模型和穷举映射；不增加注册表、接口、slice、map 或第三方依赖。
- [x] 1.3 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && gofmt -w internal/core/item.go internal/core/item_test.go && go test ./internal/core -race -count=1'`，并用 `git diff --check` 验证补丁。
- [x] 1.4 仅提交本组文件，提交信息为 `feat: 定义固定权威快捷栏`。

## 2. 权威模拟采集、选择与放置消耗

- [x] 2.1 在 `internal/sim/hotbar_test.go` 及现有交互/生命周期测试先写失败场景：初始发布、选择序列、最低栏位采集、满栏不破坏、成功放置才扣除、失败不扣除、同 tick 顺序、快照恢复和玩家哈希包含快捷栏。
- [x] 2.2 修改 `internal/sim/command.go`、`internal/sim/engine.go`、`internal/sim/player.go` 及交互实现，使快捷栏归 `Engine` 单写者所有；世界和快捷栏只通过值副本原子提交，每名 dirty 玩家每 tick 最多发布一份最终状态。
- [x] 2.3 更新直接依赖的模拟测试调用点，删除模拟层对客户端声明 `BlockID` 的信任；不得把扣除逻辑移到 `server`。
- [x] 2.4 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && gofmt -w internal/sim && go test ./internal/sim -race -count=1 && go test ./internal/sim -race -run "Hotbar|Interaction|PlayerRestore|PlayerLifecycle" -count=20'`，再运行 `git diff --check`。
- [x] 2.5 仅提交本组语义和测试，提交信息为 `feat: 权威采集并消耗快捷栏`。

## 3. 固定有界协议 v3

- [x] 3.1 在 `internal/network/message_test.go`、`packet_test.go`、`registry_test.go`、`codec_test.go` 和 `login_test.go` 先冻结失败测试：v3 packet ID 表、`SelectHotbar`、按栏位放置、完整 `HotbarState` golden、非法栏位/物品/数量、截断/尾随负载以及 v2 登录拒绝。
- [x] 3.2 修改 `internal/network/message.go`、`packet.go`、`registry.go`、`codec.go` 及必要编解码辅助，把唯一协议版本升为 3；所有快捷栏负载保持固定大小，不加入 v2 兼容、协商、delta 或单独 revision。
- [x] 3.3 只做保证仓库可编译所需的调用点更新；最终 `PlaceBlock` 不再携带 `BlockID`，Memory 传输继续按不可变值复制。
- [x] 3.4 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && gofmt -w internal/network && go test ./internal/network ./internal/sim ./internal/server ./internal/client ./cmd/mcgo -race -count=1 && go test ./internal/network -run "Protocol|Packet|Codec|Login" -count=20'`，再运行 `git diff --check`。
- [x] 3.5 仅提交协议和必要调用点，提交信息为 `feat: 升级快捷栏协议 v3`。

## 4. 玩家存档 schema v2

- [x] 4.1 在 `internal/storage/player_codec_test.go`、`player_migration_test.go`、`player_store_test.go` 和 fuzz seeds 先写失败测试：v2 roundtrip/golden、v1 空快捷栏迁移、非法栏位/物品/数量、未来 schema、原子写失败保留旧文件及迁移重写。
- [x] 4.2 修改 `internal/storage/player_types.go`、`player_codec.go`、`player_migration.go`，在原 envelope 中追加固定快捷栏 payload；保留 v1 fixture 并新增 v2 fixture，世界和区块 schema 不变。
- [x] 4.3 把快捷栏值贯穿 `StoredPlayer`、`PlayerSave`、模拟恢复/快照和现有 player persistence 转换/比较/克隆路径；继续使用玩家整体 revision、worker、重试和 flush。
- [x] 4.4 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && gofmt -w internal/storage internal/server internal/sim && go test ./internal/storage ./internal/server ./internal/sim -race -count=1 && go test ./internal/storage -run "Player.*(Codec|Migration|Store)" -count=20'`，再运行 `git diff --check`。
- [x] 4.5 仅提交本组存档和传值改动，提交信息为 `feat: 持久化玩家快捷栏`。

## 5. 所属会话同步与客户端只读镜像

- [x] 5.1 在 `internal/server/session_registry_test.go`、`player_persistence_test.go`、`multiplayer_memory_integration_test.go` 和新增 `internal/client/hotbar_test.go` 先写失败测试：登录初始状态先于 Ready、变化时每 tick 最多一份、只发所属会话、慢会话隔离、reset 清空、无效状态整包拒绝和未确认操作不修改镜像。
- [x] 5.2 修改 `internal/server/server.go`、publication/会话与 player persistence 路径，按 `SessionID` 定向发送完整 `HotbarState`；不得进入远端玩家广播，outbox 满继续只关闭错误会话。
- [x] 5.3 在 `internal/client/hotbar.go` 实现固定只读镜像并接入 receiver 消息分发；连接关闭或 reset 后不继承上个会话状态。
- [x] 5.4 增加 Memory/TCP 同脚本 parity、两玩家隔离和重连恢复验证，运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && gofmt -w internal/server internal/client && go test ./internal/server ./internal/client ./internal/network -race -count=1 && go test ./internal/server -race -run "Hotbar|SessionRegistry|Multiplayer" -count=20'`，再运行 `git diff --check`。
- [x] 5.5 仅提交本组同步和镜像改动，提交信息为 `feat: 定向同步玩家快捷栏`。

## 6. 数字键输入与固定 HUD

- [x] 6.1 在 `internal/client/input_test.go`、`window` 映射测试和 `cmd/mcgo/app_test.go` 先写失败测试：数字键 `1..9` 只发送选择请求、放置引用最后确认栏位、发送失败或未确认时不预测数量。
- [x] 6.2 在新增 `internal/render/hotbar_test.go` 先写失败测试：固定 9 格布局、选中框、三个物品色块、最多 18 个数字、单次 dynamic upload、固定 draw 上限、空/非空状态、幂等 Release、warmed 零分配和 headless blend。
- [x] 6.3 修改 `internal/client/input.go`、`window.go` 和 `cmd/mcgo` 装配，移除本地 `selectedBlock`；新增 `internal/render/hotbar.go` 与 `internal/render/shader/hotbar.wgsl`，复用现有 `GlyphAtlas` 和 `gfx` 接口，不建立通用 UI 框架或新增依赖。
- [x] 6.4 确保 HUD 在 terrain/avatar/name-tag 后绘制，framebuffer resize 正确，资源按现有应用生命周期释放；自动测试不得调用 `make run`、`go run ./cmd/mcgo` 或启动窗口。
- [x] 6.5 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && gofmt -w internal/client internal/render cmd/mcgo && go test ./internal/client ./internal/render ./cmd/mcgo -race -count=1 && go test ./internal/render -run "Hotbar|Allocation|Headless" -count=20'`，再运行 `git diff --check`。
- [x] 6.6 仅提交本组输入、HUD 和应用装配，提交信息为 `feat: 显示权威快捷栏 HUD`。

## 7. 纵向验收、兼容说明与最终门禁

- [x] 7.1 在 `internal/server` 纵向测试中覆盖两名玩家独立采集/消耗、满栏拒绝、Memory/TCP parity、正常断线重连、DiskStore 关服重启、flush 失败后重试和 v2 客户端拒绝；所有测试使用受控 tick/deadline，不使用真实 sleep 或前台窗口。
- [x] 7.2 更新 `README.md` 和 `docs/notes/lan-server.md`：操作键改为 `1..9`、说明直接入栏/满栏行为、协议 v3、玩家 schema v2、部署前备份与旧程序回退边界；不把未来掉落物、背包或合成写成已实现。
- [x] 7.3 运行协议与存档 fuzz：`zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "^$" -fuzz=FuzzSmallPacketCodec -fuzztime=10s && go test ./internal/storage -run "^$" -fuzz=FuzzDecodePlayer -fuzztime=10s'`。
- [x] 7.4 运行最终 headless 门禁：`zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race && go vet ./... && go test ./internal/archcheck -count=1 && make test-multiplayer && go test ./internal/network ./internal/server ./internal/render -run "^$" -bench "(RemotePlayerStateCodec|EightPlayerInterest|RemoteAvatarNameTag)" -benchmem -count=3 && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /tmp/mcgod-m4a-linux ./cmd/mcgod && gofmt -l .'`；`gofmt -l .` 必须无输出，现有门禁不得放宽。
- [x] 7.5 运行 `openspec validate --all --strict --no-interactive`、`git diff --check` 和最终规格/代码评审；若实现偏离契约先更新本 change，确认工作树只含 M4A 预期文件。
- [x] 7.6 勾选已完成任务并创建唯一收尾提交，提交信息为 `chore: 关闭 M4A 权威快捷栏`。
