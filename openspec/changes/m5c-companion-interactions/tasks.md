# Tasks: m5c-companion-interactions

## 1. 协议 v18 停止与容量枚举

- [ ] 1.1 在 `internal/network` 及协议测试先写失败测试：`ProtocolVersion` 为 18 且 v17 登录明确拒绝、既有 message ID 冻结、`ChatEventTaskStopped` 与 `ChatRejectNotFollowing`、`TaskFailInventoryFull` 的字段组合校验、任务事件 reason 非法组合原子拒绝、wire 上限不变（1026/1328/178/173）、codec golden 更新与 fuzz 种子扩展；运行 `go test ./internal/network ./cmd/mornlea ./cmd/mornlea-server -run 'Test(Chat|Companion|Protocol|Handshake|ServerProtocol)' -race -count=1` 确认 RED。
- [ ] 1.2 最小实现枚举扩展与 `Validate` 组合规则；TCP 与 Memory 共用同一 codec。
- [ ] 1.3 运行 1.1 命令（含 Memory/TCP 变体）、`go test ./internal/network -run '^$' -fuzz FuzzCompanionMessageCodec -fuzztime=5s`、`go test ./internal/archcheck -count=1`、`go test ./internal/network ./cmd/mornlea ./cmd/mornlea-server -race -count=1`、`go vet ./internal/network ./cmd/mornlea ./cmd/mornlea-server`、`test -z "$(gofmt -l internal/network cmd/mornlea cmd/mornlea-server)"`、`openspec validate m5c-companion-interactions --strict --no-interactive`；临时允许 TaskStopped 缺少指令字段，确认第一条命令 RED 后恢复并重跑至 PASS。

## 2. sim 采掘共享与伙伴采掘原子完成

- [ ] 2.1 在 `internal/sim` 先写失败测试：playerMiningState 上移 actorState 后玩家移动/采掘/放置/背包序列逐 tick 差分不变、伙伴与玩家同工具同方块同 tick 完成且耐久扣减一致、伙伴完成 tick 三方原子（方块/耐久/产物直入背包）、背包无容量不结算且方块不变、容器与多掉落方块防御拒绝、采掘中目标被替换进度失效、伙伴采掘进度进入 CompanionUpdate 发布；运行 `go test ./internal/sim -run 'Test(CompanionMining|ActorState)' -race -count=1` 确认 RED。
- [ ] 2.2 最小实现采掘状态上移、`CompanionAction` 判别载荷（Move/MineHold/MineRelease/Place）、applyCompanionActions 按载荷分派、完成分叉（玩家掉落物/伙伴直入背包含容量前验）、`miningRule` 与容器拒绝的共用校验；三阶段顺序与既有探针不变。
- [ ] 2.3 运行 2.1 命令、`go test ./internal/sim -race -count=1`、`go test ./internal/sim -run '^$' -bench 'Benchmark.*Step' -benchmem -count=5`、`go test ./internal/archcheck -count=1`、`go vet ./internal/sim`、`test -z "$(gofmt -l internal/sim)"`、`openspec validate m5c-companion-interactions --strict --no-interactive`；临时允许伙伴采掘绕过容量前验，确认原子测试 RED 后恢复并重跑至 PASS。

## 3. sim 伙伴放置原子路径

- [ ] 3.1 在 `internal/sim` 先写失败测试：放置 action 经既有玩家放置校验（空气目标/合法性/碰撞/Ready）、成功同 tick 扣一件并写方块、校验失败不扣料、物品不足由 action 语义拒绝；运行 `go test ./internal/sim -run 'TestCompanionPlace' -race -count=1` 确认 RED。
- [ ] 3.2 最小实现放置载荷处理（复用 engine_placement 的校验路径，扣料与写方块同 tick 原子提交）。
- [ ] 3.3 运行 `go test ./internal/sim -run 'TestCompanionPlace' -race -count=1`、`go test ./internal/sim -race -count=1`、`go vet ./internal/sim`、`test -z "$(gofmt -l internal/sim)"`、`openspec validate m5c-companion-interactions --strict --no-interactive`；临时把扣料移到校验前，确认失败不扣料测试 RED 后恢复并重跑至 PASS。

## 4. companion 计划步骤全集与快照玩家集合

- [ ] 4.1 在 `internal/companion` 先写失败测试（httptest）：`PlanStep` 扩展（Block/PlayerID 字段）后四 kind 解码矩阵——follow 非最后一步失败、follow 目标不在快照在线集合失败、mine 目标越界/容器/多掉落失败、place block 非注册表或背包未持有失败、go_to 既有语义不变；快照在线玩家集合 ≤8 且构造有界；运行 `go test ./internal/companion -run 'Test(Planner|PlanSnapshot|PlanDecode)' -race -count=1` 确认 RED。
- [ ] 4.2 最小实现步骤结构扩展、四 kind 契约校验、快照玩家集合字段。
- [ ] 4.3 运行 4.1 命令、`go test ./internal/companion -race -count=1`、`go test ./internal/archcheck -count=1`、`go vet ./internal/companion`、`test -z "$(gofmt -l internal/companion)"`、`openspec validate m5c-companion-interactions --strict --no-interactive`；临时允许 follow 出现在非最后一步，确认解码测试 RED 后恢复并重跑至 PASS。

## 5. Stopped 终态与停止旁路

- [ ] 5.1 在 `internal/companion` 与 `internal/server` 先写失败测试：Stopped 只能从 Running 持续跟随进入、停止事件字段、`@名称 停止` 精确文本旁路（大小写/带参数进入 FIFO）、非跟随/空闲 NotFollowing 单播、停止后立即执行原队首且队列不变、同 tick 多停止按顺序；运行 `go test ./internal/companion ./internal/server -run 'Test(TaskQueue|TaskStateMachine|ChatCommand.*Stop|CompanionManager.*Stop)' -race -count=1` 确认 RED。
- [ ] 5.2 最小实现状态机 Stopped 迁移、companion_chat 停止解析与旁路、事件发布。
- [ ] 5.3 运行 5.1 命令、`go test ./internal/companion ./internal/server -race -count=1`、`go vet ./internal/companion ./internal/server`、`test -z "$(gofmt -l internal/companion internal/server)"`、`openspec validate m5c-companion-interactions --strict --no-interactive`；临时允许普通 go_to 任务被停止，确认状态机测试 RED 后恢复并重跑至 PASS。

## 6. follow 执行与 deadline 豁免

- [ ] 6.1 在 `internal/server` 先写失败测试（httptest 假模型产出 follow 计划）：距离内停止提交移动、超出恢复寻路、目标离线以 `TaskFailWorldChanged` 失败并推进 FIFO、运行超过 timeout 不转 TimedOut、恢复的 follow 先验在线性（离线失败/在线继续）；运行 `go test ./internal/server -run 'TestCompanionManager.*Follow' -race -count=1` 确认 RED。
- [ ] 6.2 最小实现 `CompanionFollowDistanceBlocks` 常量、follow 步骤执行器（复用寻路/冷却/三连失败）、deadline 豁免（零值）、离线检测。
- [ ] 6.3 运行 6.1 命令、`go test ./internal/server -race -count=1`、`go vet ./internal/server`、`test -z "$(gofmt -l internal/server)"`、`openspec validate m5c-companion-interactions --strict --no-interactive`；临时让 follow 任务记录 deadline，确认豁免测试 RED 后恢复并重跑至 PASS。

## 7. mine/place 步骤编排

- [ ] 7.1 在 `internal/server` 先写失败测试（httptest 假模型产出 mine/place 计划）：走入交互距离后按住采掘至完成（事件序列 Accepted→TaskStarted→[TaskProgress]→TaskCompleted）、背包无容量以 `TaskFailInventoryFull` 失败且方块不变、放置成功原子扣料、物品耗尽失败且已成交变更保留、目标被改按目标变化语义处理；运行 `go test ./internal/server -run 'TestCompanionManager.*(Mine|Place)' -race -count=1` 确认 RED。
- [ ] 7.2 最小实现 mine/place 步骤执行器（距离验证→action 提交→完成观察→事件）、失败原因映射。
- [ ] 7.3 运行 7.1 命令、`go test ./internal/server -race -count=1`、`go test ./internal/server -run '^$' -bench BenchmarkChatRoutingFourCompanions -benchmem -count=5`、`go test ./internal/archcheck -count=1`、`go vet ./internal/server`、`test -z "$(gofmt -l internal/server)"`、`openspec validate m5c-companion-interactions --strict --no-interactive`；临时跳过采掘距离验证，确认编排测试 RED 后恢复并重跑至 PASS。

## 8. schema v3 变长步骤、恢复与客户端事实行

- [ ] 8.1 在 `internal/storage` 先写失败测试：v3 变长步骤 round-trip 与 golden（四 kind 各覆盖）、v2 只读迁移（既有 go_to 无损）、430,080 分配前门禁、follow 不保存 deadline、损坏矩阵与 5,000 步边界；在 `internal/client` 与 `cmd/mornlea` 先写 `TaskStopped`/`NotFollowing`/`TaskFailInventoryFull` 稳定中文事实行测试（模型文本不上屏）；运行 `go test ./internal/storage -run 'TestCompanionCodec|TestCompanionRestore|TestCompanionStore' -race -count=1` 与 `go test ./internal/client ./cmd/mornlea -run 'Test(Companion|ChatEvents)' -race -count=1` 确认 RED。
- [ ] 8.2 最小实现 v3 编解码（kind 变长：13/15/17）、v2 迁移、上界常量、`PlanStep` 存储布局、客户端事实行。
- [ ] 8.3 仅运行 `go test ./internal/storage -run '^TestCompanionCodecV3RoundTripAndGolden$' -update-storage-fixtures` 生成 `companions-v3.bin`；再运行两组 focused 命令、`go test ./internal/storage -run '^$' -fuzz FuzzDecodeCompanions -fuzztime=5s`、`shasum -a 256 internal/storage/testdata/companions-v3.bin`、`go test ./internal/storage ./internal/client ./cmd/mornlea -race -count=1`、`go test ./internal/archcheck -count=1`、vet 与 gofmt 对应包、`openspec validate m5c-companion-interactions --strict --no-interactive`；临时移除文件长度门禁，确认超长测试 RED 后恢复并重跑至 PASS。

## 9. 收尾门禁与多人一致性

- [ ] 9.1 多人一致性：至少两名玩家共同指挥（一名发 follow、另一名发 停止/指令）的 Memory/TCP parity 测试（任务状态、事件序列、世界结果一致）。
- [ ] 9.2 全量门禁：`gofmt -l .` 无输出、`go vet ./...`、`go test ./... -race -count=1`、`go test ./internal/archcheck -count=1`、`openspec validate --all --strict --no-interactive`；benchmark 冒烟只记录；golden 零改动核对（`git diff --name-only main...HEAD` 不含视觉 testdata）。
- [ ] 9.3 更新 `docs/notes/progress.md` 当前基线段落：协议 v18、schema v3、follow/停止/mine/place 交付事实与 M5D 后续方向。
