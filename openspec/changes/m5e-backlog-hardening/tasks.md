# Tasks: m5e-backlog-hardening

## E1 planWireStep 显式 null 契约收紧

- [x] E1.1 先写失败测试：`internal/companion` 排他矩阵 null 负向全集（`follow`+`"x":null`、`go_to`+`"block":null`/`"player_id":null`、`mine`+`"block":null`/`"player_id":null`、`place`+`"player_id":null`/坐标 null），既有 `"x":null` 用例保持
- [x] E1.2 最小实现：`planWireStep` 区分缺席与显式 null（map 中间形或自定义 `UnmarshalJSON`，与 dialogue 的 isJSONNull 同构），`decodePlanStep` GoDoc 校准
- [x\] 验证：`go test ./internal/companion -race -count=1` 全绿；`openspec validate m5e-backlog-hardening --strict` 通过（delta 已随 change 创建）

## E2 双实现交叉锁与物品穷举守护

- [x] E2.1 先写失败测试：`internal/companion` 对 `buildPlanPlaceBlocks()` 全集逐条断言 `BlockDrop(B) == (I, true)`；`internal/core` `ItemIDMax` 哨兵 + planner_test 穷举界改哨兵并加「枚举末项守护」断言
- [x] E2.2 最小实现：`core/item.go` 引入 `ItemIDMax`，`planner_test.go:962` 穷举界改用哨兵
- [x] 验证：`go test ./internal/companion ./internal/core -race -count=1`

## E3 sim/companion 注释与常量清理（A1、A2、A3、A5、A8、A9）

- [x] E3.1 `companion_action.go:85` + `companion_action_test.go:441`：去 M5B 骨架期措辞、测试更名 `...DefensiveBoundary`；`companion_action.go:44` 容量注释改全局口径并注明饿死后果
- [x] E3.2 `actor.go:23-24` 背包注释改现状；`companion_mining_test.go:77、155` 删两处 `entry.pitch = 0` 死赋值
- [x] E3.3 `planner.go:68` Y 域改 `core.MinY`/`core.MaxY-1` 拼接；`pathfind.go:22` ↔ `plan_types.go:33` 半径共用常量
- [x] E3.4 `internal/render/hud/atlas.go:15,23` 物品穷举界统一改 `core.ItemIDMax`（E2 评审发现的同型脆弱穷举，今日行为等值、追加物品时守护一致；控制会话裁决并入）
- [x] 验证：`go test ./internal/companion ./internal/sim -race -count=1` 全绿；`go test ./internal/render/... -race -count=1`；diff 复核零行为变化

## E4 跨测试 helper 去重（A6、A7、C8）

- [x] E4.1 `wantPlanError`/`wantPathError` 合并单 helper；`companion_mining_test.go` 抽 `newCompanionMiningScene`；`message_companion_test.go` 三处 kind 偏移提取 `chatEventKindOffset`
- [x] 验证：`go test ./internal/companion ./internal/sim ./internal/network -race -count=1` 全绿（断言全集保持）

## E5 server 注释与死代码（B1、B2、B4、B5、B6、B7、B8、B10）

- [x] E5.1 注释批：16..20 枚举（B1）、issuer 魔法字节出处（B2）、任务 7 引用（B4）、Accepted 取舍补全（B6）、mining 生命周期（B7）、dialogueEffects 现状（B8）
- [x] E5.2 代码批：删 `companion_chat.go:60-63` clamp 死防御（B5，先白盒断言从未生效）；`sortCompanionBodies` 统一 `slices.SortFunc`（B10）
- [x] 验证：`go test ./internal/server -race -count=1` 全绿

## E6 server 防御与复用（B3、B9）

- [x] E6.1 `submitPathRequest` 改复用 `standingCellOf`（B3）；issuers 空失配检查前移到 `BeginHead()` 之前（B9），安全论证入注释
- [x] 验证：`go test ./internal/server -race -count=1` 全绿；评审确认行为等价推理

## E7 上限常量同源化（A10、C5、C7）

- [x] E7.1 先写失败测试：跨包常量相等断言（network 聊天上限 == `companion.MaxPlanCommandBytes`/`MaxDialogueLineBytes` 对应项）
- [x] E7.2 最小实现：network 与 `cmd/mornlea/chat.go` 复用 companion 导出常量（archcheck 否决则退交叉锁形态）；`message_companion.go:18-25` 五常量补字节构成推导注释（C7）
- [x] 验证：`go test ./internal/network ./cmd/mornlea -race -count=1`；`go test ./internal/archcheck -count=1`

## E8 存档前瞻与测试名（C1、C3）

- [x] E8.1 `companion_codec.go:223` 白名单显式列 `companionSchemaV4`，以 v5 假想 schema 单测锁定前瞻口径（v4 文件不被入口误拒）
- [x] E8.2 `companion_restore_test.go:329` 测试名/doc V3 → V4
- [x] 验证：`go test ./internal/storage -race -count=1` 全绿；golden shasum 不变

## E9 客户端呈现批（C2、C4、C6、C9）

- [ ] E9.1 `chat.go` `ChatEventAccepted` 显式 case + default 防御兜底注释（C2）；`taskFailReasonText` 签名改 `network.TaskFailReason`（C4）；`truncateChatLine` 补中文 doc（C6）
- [ ] E9.2 `internal/client` 导出容量常量，`cmd/mornlea` 复用消 `[32]` 字面；`chatLines [6]string` 补 6 出处注释（C9）
- [ ] 验证：`go test ./cmd/mornlea ./internal/client -race -count=1` 全绿

## E10 M5D 延期五项（F-2..F-6）

- [ ] E10.1 先写失败测试：`ResolvedPersona` `json:"-"` 反射级 tag 锁（F-2，先红）
- [ ] E10.2 `ErrDialogueInvalidResponse` 拆分为请求构造/响应解码两哨兵并迁移调用方（F-3）；ChatEvent decode 补无效 UTF-8 wire 突变用例（F-4）；chat 呈现负向断言收紧 29 rune（F-5）；阶段验收哑参数风格 + parity 投影复用（F-6）
- [ ] 验证：`go test ./internal/companion ./internal/network ./cmd/mornlea ./internal/server -race -count=1` 全绿

## E11 负载 flake 治理（只改测试）

- [ ] E11.1 TestScenarioV7 race-skip：`//go:build race` 辅助文件 + 测试开头 skip；非 race 门禁原样
- [ ] E11.2 `waitIntegrationCondition` 热轮询改 sleep 退避；`waitForIncomingChatDepth` 改 `>=`
- [ ] 验证：`go test ./cmd/mornlea -race -count=1 -run TestScenarioV7`（skip 生效）；`go test ./internal/server -race -count=1`；一次全仓 `-race` 无新 flake

## E12 收尾门禁与流程沉淀

- [ ] E12.1 全量门禁：`go test ./... -race -count=1`、`go test ./internal/archcheck`、`go vet ./...`、`gofmt -l .` 零输出、`openspec validate --all --strict`
- [ ] E12.2 `docs/notes/progress.md` 补 M5E 段落；AGENTS.md 基线核对（无协议/schema 变化，预计仅进度引用）
- [ ] E12.3 backlog 沉淀纪律执行：ledger 未决项全文誊入 proposal.md「延期与放弃」节
