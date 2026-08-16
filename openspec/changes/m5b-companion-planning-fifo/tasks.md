# Tasks: m5b-companion-planning-fifo

## 1. AI 模型运行时配置与密钥边界

- [x] 1.1 在 `internal/config`、`internal/server`、`cmd/mornlea` 与 `cmd/mornlea-server` 先写失败测试：`ai` 组 `endpoint`/`model`/`apiKeyEnv`/`taskTimeoutMinutes` 解析与校验（endpoint 无 userinfo/query/fragment 的 https 或 loopback http、timeout `1..60` 缺省 10）、非空伙伴缺少模型配置或所需 key 环境变量为空时启动失败、AI 关闭时不要求任何模型字段、persona 等未交付字段告警忽略、密钥不出现在错误与日志文本；运行 `go test ./internal/config ./internal/server ./cmd/mornlea ./cmd/mornlea-server -run 'Test(ConfigAI|ServerConfigCompanions|Run.*AI)' -race -count=1` 确认 RED。
- [x] 1.2 最小实现 config v1 的四个 `ai` 字段、启动失败边界与密钥读取纪律；既有 `id/name` 语义与未知字段告警保持；调试面板保存继续原样保留 `ai` 值。
- [x] 1.3 运行 `go test ./internal/config ./internal/server ./cmd/mornlea ./cmd/mornlea-server -run 'Test(ConfigAI|ServerConfigCompanions|Run.*AI)' -race -count=1`、`go test ./internal/archcheck -count=1`、`go test ./internal/config ./internal/server ./cmd/mornlea ./cmd/mornlea-server -race -count=1`、`go vet ./internal/config ./internal/server ./cmd/mornlea ./cmd/mornlea-server`、`test -z "$(gofmt -l internal/config internal/server cmd/mornlea cmd/mornlea-server)"`、`openspec validate m5b-companion-planning-fifo --strict --no-interactive`；临时把 timeout 上限改为 61，确认第一条命令 RED 后恢复并重跑至 PASS。

## 2. 协议 v17 任务生命周期消息

- [x] 2.1 在 `internal/network` 及协议测试先写失败测试：`ProtocolVersion` 为 17 且 v16 登录明确拒绝、既有 message ID 冻结、`ChatEventKind` 任务枚举（`TaskStarted`/`TaskProgress`/`TaskCompleted`/`TaskFailed`/`TaskTimedOut`）与 `QueueFull` 拒绝原因的字段组合校验、kind 与 reason 非法组合原子拒绝、wire 上限不变（1026/1328/178/173）、codec golden 更新与 fuzz；运行 `go test ./internal/network ./cmd/mornlea ./cmd/mornlea-server -run 'Test(Chat|Companion|Protocol|Handshake|ServerProtocol)' -race -count=1` 确认 RED。
- [x] 2.2 最小实现枚举扩展与 `Validate` 组合规则：任务事件必须携带伙伴身份与原始指令、`TaskFailed` 原因枚举、任务事件不携带模型文本；TCP 与 Memory 共用同一 codec 与验证，不绕过。
- [x] 2.3 运行 `go test ./internal/network ./cmd/mornlea ./cmd/mornlea-server -run 'Test(Chat|Companion|Protocol|Handshake|Memory|TCP|ServerProtocol)' -race -count=1`、`go test ./internal/network -run '^$' -fuzz FuzzCompanionMessageCodec -fuzztime=5s`、`go test ./internal/archcheck -count=1`、`go test ./internal/network ./cmd/mornlea ./cmd/mornlea-server -race -count=1`、`go vet ./internal/network ./cmd/mornlea ./cmd/mornlea-server`、`test -z "$(gofmt -l internal/network cmd/mornlea cmd/mornlea-server)"`、`openspec validate m5b-companion-planning-fifo --strict --no-interactive`；临时允许 TaskStarted 缺少伙伴身份，确认第一条命令 RED 后恢复并重跑至 PASS。

## 3. Planner 客户端与严格计划解码

- [x] 3.1 在 `internal/companion` 先写失败测试（全部使用 `httptest.Server` 假模型）：观察快照字段有界与 256 方块排序、prompt 不含 key 与无关文本、30 秒超时不重试、5xx/超大响应失败且错误不含正文、严格 JSON 拒绝未知字段/尾随数据/超 64 KiB、非 `go_to` 步骤与非法坐标令任务失败、context 取消干净返回；运行 `go test ./internal/companion -run 'Test(Planner|PlanSnapshot|PlanDecode)' -race -count=1` 确认 RED。
- [x] 3.2 最小实现快照值类型、标准库 HTTP 客户端（固定上限/超时/无重试）与 `json.Decoder` 严格解码；模型输出视为不可信数据，不执行任何返回的代码、URL 或工具名。
- [x] 3.3 运行 `go test ./internal/companion -run 'Test(Planner|PlanSnapshot|PlanDecode)' -race -count=1`、`go test ./internal/companion -race -count=1`、`go test ./internal/archcheck -count=1`、`go vet ./internal/companion`、`test -z "$(gofmt -l internal/companion)"`、`openspec validate m5b-companion-planning-fifo --strict --no-interactive`；临时允许尾随 JSON 数据，确认第一条命令 RED 后恢复并重跑至 PASS。

## 4. 有界确定性寻路

- [x] 4.1 在 `internal/companion` 先写失败测试：相同快照重放一致、固定邻居序、4096 节点预算终止、跨一格间隙与跳上一格、不修改任何方块、revision 失效触发固定冷却重算、连续三次失败终止；运行 `go test ./internal/companion -run 'TestPathfind' -race -count=1` 确认 RED。
- [x] 4.2 最小实现整数代价 A* 与不可变快照输入；寻路只在 worker goroutine 执行，结果携带相关区块 revision；不游泳、不攀爬、不搭桥、不挖改方块。
- [x] 4.3 运行 `go test ./internal/companion -run 'TestPathfind' -race -count=1`、`go test ./internal/companion -run '^$' -bench BenchmarkPathfind -benchmem -count=5`、`go test ./internal/companion -race -count=1`、`go vet ./internal/companion`、`test -z "$(gofmt -l internal/companion)"`、`openspec validate m5b-companion-planning-fifo --strict --no-interactive`；临时把预算上限改为 4097，确认预算测试 RED 后恢复并重跑至 PASS。

## 5. sim actorState 与 CompanionAction

- [x] 5.1 在 `internal/sim` 先写失败测试：actorState 提取后玩家移动/采掘/放置/背包序列逐 tick 差分不变、CompanionAction 按 `CompanionID` 字节序在玩家命令后处理、伙伴物理与玩家共用同一积分出口、inbox 有界且拒绝携带会话身份、3×3 兴趣随脚下区块滑动且单伙伴不超过九个区块；运行 `go test ./internal/sim -run 'Test(CompanionAction|ActorState|CompanionInterest)' -race -count=1` 确认 RED。
- [x] 5.2 从 `playerState` 提取不导出 `actorState`（仅运动/朝向/背包），新增有界 `CompanionAction` inbox 与统一物理推进顺序；不新写 Go 积分，复用既有 Rust engine 物理出口；不改既有玩家 oracle 测试语义。
- [x] 5.3 运行 `go test ./internal/sim -run 'Test(CompanionAction|ActorState|CompanionInterest)' -race -count=1`、`go test ./internal/sim -race -count=1`、`go test ./internal/sim -run '^$' -bench 'Benchmark.*Step' -benchmem -count=5`、`go test ./internal/archcheck -count=1`、`go vet ./internal/sim`、`test -z "$(gofmt -l internal/sim)"`、`openspec validate m5b-companion-planning-fifo --strict --no-interactive`；临时把伙伴 action 处理移到玩家命令之前，确认顺序测试 RED 后恢复并重跑至 PASS。

## 6. 任务状态机、FIFO 与 tick 边界编排

- [x] 6.1 在 `internal/companion` 与 `internal/server` 先写失败测试：FIFO 16 条容量与接收顺序、第 17 条 `QueueFull` 同步拒绝且不调模型、状态机全路径（Queued→Planning→Validating→Running→各终态）、deadline 用 `WorldTimeTicks` 且关服不消耗、Planner/寻路结果只在 tick 边界应用且过时丢弃、慢 HTTP/磁盘不阻塞权威 tick、每伙伴一个在途请求与全服四个并发上限、关服顺序（停聊天→取消模型→冻结→最终保存→关存储）、Memory/TCP 事件 parity；运行 `go test ./internal/companion ./internal/server -run 'Test(TaskQueue|TaskStateMachine|CompanionManager|CompanionShutdown|ChatCommand)' -race -count=1` 确认 RED。
- [x] 6.2 最小实现每伙伴 FIFO 与任务状态机、server 侧 Companion Manager 编排（tick 边界 drain/入队/快照/应用 worker 结果/推进 Runner/发布事件）、有界 channel 与信号量；Task Runner 每 tick 每伙伴最多提交一个移动输入，失败不重试不降级不改写计划。
- [x] 6.3 运行 `go test ./internal/companion ./internal/server -run 'Test(TaskQueue|TaskStateMachine|CompanionManager|CompanionShutdown|ChatCommand)' -race -count=1`、`go test ./internal/companion ./internal/server -race -count=1`、`go test ./internal/server -run '^$' -bench BenchmarkChatRoutingFourCompanions -benchmem -count=5`、`go test ./internal/archcheck -count=1`、`go vet ./internal/companion ./internal/server`、`test -z "$(gofmt -l internal/companion internal/server)"`、`openspec validate m5b-companion-planning-fifo --strict --no-interactive`；临时允许同伙伴在规划请求在途时并发发起第二个规划请求，确认第一条命令 RED 后恢复并重跑至 PASS。

## 7. companions.ai schema v2 与恢复

- [x] 7.1 在 `internal/storage` 先写失败测试：v2 round-trip 与 golden（任务区/FIFO/步骤索引/deadline）、v1 只读迁移且首次保存写 v2、任务与 FIFO 跨重启精确恢复、Planning/Validating 关服按 Queued 恢复、Running 恢复重验、CRC/future/截断/超 350,208 bytes/非法任务状态拒绝、5,000 步骤与 16 条 FIFO 边界、原子替换失败注入、active+inactive 64 条上限保留；运行 `go test ./internal/storage -run 'TestCompanionCodec|TestCompanionRestore|TestCompanionStore' -race -count=1` 确认 RED。
- [x] 7.2 最小实现 schema v2 编解码（v1 记录 221 bytes 布局不变）、350,208 bytes 分配前门禁、任务区与 FIFO 有界字段、v1 迁移路径与 future 拒绝；保存纪律（dirty/单 in-flight/重试/关服 flush）沿用既有 worker。
- [x] 7.3 仅运行 `go test ./internal/storage -run '^TestCompanionCodecV2RoundTripAndGolden$' -update-storage-fixtures` 生成 `companions-v2.bin`；再运行 `go test ./internal/storage -run 'TestCompanionCodec|TestCompanionRestore|TestCompanionStore' -race -count=1`、`go test ./internal/storage -run '^$' -fuzz FuzzDecodeCompanions -fuzztime=5s`、`shasum -a 256 internal/storage/testdata/companions-v2.bin`、`go test ./internal/storage -race -count=1`、`go test ./internal/archcheck -count=1`、`go vet ./internal/storage`、`test -z "$(gofmt -l internal/storage)"`、`openspec validate m5b-companion-planning-fifo --strict --no-interactive`；临时把文件长度门禁改为无上限，确认超长测试 RED 后恢复并重跑至 PASS。

## 8. 客户端移动插值与任务事件展示

- [x] 8.1 在 `internal/client` 与 `cmd/mornlea` 先写失败测试：移动伙伴在状态批次间按远端玩家同机制插值且不外推越界、非法批次原子拒绝保持、任务事件进入 32 条事件环与 6 行 ×32 rune HUD 稳定中文事实行、模型自由文本不上屏、断线清空插值与事件状态；只允许修改 `internal/client` 伙伴镜像/接收相关文件与 `cmd/mornlea` 聊天装配文件；运行 `go test ./internal/client ./cmd/mornlea -run 'Test(Companion|ChatEvents|CompanionInterpolation|ApplicationRoutesCompanion)' -race -count=1` 确认 RED。
- [x] 8.2 最小实现伙伴位置插值（复用既有 remote actor 插值机制）与任务事件事实行格式化；不预测伙伴移动、不产生伙伴世界写入、不触碰渲染与帧循环文件。
- [x] 8.3 运行 `go test ./internal/client ./cmd/mornlea -run 'Test(Companion|ChatEvents|CompanionInterpolation|ApplicationRoutesCompanion)' -race -count=1`、`go test ./internal/client -run 'TestCompanionPresentationHotPathAllocations' -count=1`、`go test ./internal/client ./cmd/mornlea -race -count=1`、`go test ./internal/archcheck -count=1`、`go vet ./internal/client ./cmd/mornlea`、`test -z "$(gofmt -l internal/client cmd/mornlea)"`、`openspec validate m5b-companion-planning-fifo --strict --no-interactive`；临时允许插值外推超过最新权威位置，确认插值测试 RED 后恢复并重跑至 PASS。

## 9. 收尾门禁与并行纪律核对

- [x] 9.1 全量门禁：`gofmt -l .` 无输出、`go vet ./...`、`go test ./... -race -count=1`、`go test ./internal/archcheck -count=1`、`openspec validate --all --strict --no-interactive`；benchmark 冒烟运行一次并只记录数值，M2 v15 与 M5 v14 基线字节保持不变。
- [x] 9.2 并行纪律核对：`git diff --name-only main...HEAD` 不包含 `internal/render`、`internal/render/hud`、`internal/gfx`、`internal/client/render.go`、`internal/client/window.go`、`cmd/mornlea/app_frame.go`、`cmd/mornlea/app_render.go`、`cmd/mornlea/capture*`、`cmd/mornlea/benchmark*`、`cmd/mornlea/visual_compare*`、`engine/crates/mornlea_client` 与 `go.mod`；全部 capture golden 零改动（`git status` 与 golden 目录比对）。
- [x] 9.3 更新 `docs/notes/progress.md` 的当前基线段落：协议 v17、companion schema v2、Planner/FIFO/`go_to` 交付事实与 M5C/M5D 后续方向；说明 benchmark scenario 保持 v16 的理由。
