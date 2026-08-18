# Proposal: m5e-backlog-hardening

## 为什么

M5B/M5C/M5D 三个里程碑的终审各遗留一批 minor 级打磨项（M5D ledger 终审汇总为「M5B/M5C 遗留约 20 条 minor + 双实现交叉锁测试 + M5D 延期五项 + 极端负载假失败观察」），其中 M5B/M5C 的原始清单随 SDD ledger 覆盖而丢失。2026-08-18 三个只读评审子代理对当前 main 的伙伴面（计划/寻路/模拟、服务端编排、协议/存档/客户端呈现）做了针对性考古复审，重建出 29 条仍成立的 minor，并发现一项契约缺口：`planWireStep` 用指针区分「字段缺席」，但 JSON 显式 `null` 同样解出 nil，排他矩阵可被 `{"kind":"follow","player_id":"<合法>","x":null}` 等形态绕过，违反 `decodePlanStep` 自身 GoDoc 承诺的字段排他。本变更一次性清偿全部欠账，并把「backlog 必须沉淀进 git 跟踪产物」固化为纪律，防止清单再次丢失。

## 变更内容

- 收紧 `planWireStep` null 语义：显式 JSON null 视为字段出现，排他矩阵对 null 与非 null 非法字段一视同仁（本变更唯一行为变化，delta spec 见 `specs/companion-planner/`）。
- 新增双实现交叉锁测试：`buildPlanPlaceBlocks()` 全集逐条断言 `BlockDrop(B) == (I, true)`（放置消耗物品与采掘产出往返同一），连同 `ItemIDMax` 哨兵与物品穷举守护断言。
- 29 条考古 minor 全部执行：过期/误导注释、魔法数出处、死防御删除、跨测试 helper 去重、测试名与断言锋利度、上限常量同源化、schema 白名单前瞻口径、防御检查前移（完整清单见 `design.md` 工作项全集）。
- M5D 延期五项：`ResolvedPersona` tag 单测锁、`ErrDialogueInvalidResponse` 命名拆分、ChatEvent decode 无效 UTF-8 wire 突变用例、chat 呈现负向断言收紧至 29 rune、阶段验收哑参数风格与 parity 投影复用。
- 负载 flake 治理（只改测试）：`TestScenarioV7` 在 `-race` 构建 tag 下 skip（50ms 实时调度门禁在 race 确定性开销下测的是机器负载而非产品行为，CI 文档已实测 4 次假失败）；共享 helper `waitIntegrationCondition` 的 `runtime.Gosched()` 热轮询改 sleep 退避，`waitForIncomingChatDepth` 深度断言放宽为 `>=`。
- 流程修复：任何 change 归档前，其 ledger 中全部未决 minor/延期项必须全文誊入该 change proposal.md 的「延期与放弃」节；ledger 只做执行记录不做唯一载体。本变更归档时首个执行该纪律。

## 非目标

- 不改协议版本（v19 保持）、存档 schema（v4 保持）、任何 wire 格式与长度上限、视觉 golden、benchmark scenario（v16 保持）。
- 不新增功能、message kind、模型并发槽；不改变 Planner/Dialogue 并发模型与提示词语义（`[-64,319]` 改 core 常量拼接属机械替换）。
- 不做 M6 方向工作；不做与清单无关的重构。

## 用户可观察结果

- 模型返回步骤在专属外字段携带显式 null 时，任务以非法计划失败（原会被接受执行）；除此以外一切玩家可见、wire 可见、存档可见行为逐字节不变。
- `go test ./... -race` 全仓并行下的既有假失败面缩小：TestScenarioV7 不再在 race 下假红，47 个调用热轮询 helper 的集成测试相互干扰降低。

## 影响

- Go 代码：`internal/companion`、`internal/server`、`internal/sim`、`internal/network`、`internal/storage`、`internal/client`、`cmd/mornlea`；测试为主，生产代码改动限于注释/常量同源/死防御删除/防御检查前移。
- 规格：companion-planner 一个 requirement 追加 null 场景，其余 capability 零 delta。
- 无 Rust 面改动。

## 延期与放弃

按本 change 确立的 backlog 沉淀纪律，执行期产生的未决项全文誊录于此（ledger 只做执行记录）。考古清单 29 项与既定 10 项全部执行完毕，以下为执行期新产生的递延/放弃项：

**递延（后续里程碑候选）**：

1. `internal/companion/planner.go` 排他矩阵中 `follow`+`player_id:null`、`place`+`block:null` 两个拒绝分支兼是 nil 解引用防 panic 屏障，但无直接用例锁定（E1 评审 Minor-1；误删分支时现有测试不变红）。
2. `cmd/mornlea/interactive.go:33` 的 `[1024]rune` 输入缓冲与指令字节上限存在巧合耦合（今日 rune ≥ byte 恒不先触发；上限增大时才需收敛）（E7 评审）。
3. `cmd/mornlea` 的 `formatChatEvent` 防御兜底「未知事件」契约可补直接构造未知 kind 的单测锁定（E9 评审 Minor-2）。
4. `cmd/mornlea/capture_scene.go:52`、`capture_ai_companion_test.go:185` 的 `[32]network.ChatEvent` 字面（编译器类型匹配结构性锁定；彻底消字面留后续）（E9 concern）。
5. `internal/network/codec_client.go:73/:238` ChatCommand 编解码的 1024 字面与 `:88` 错误文案（E7 锁测试已行为级覆盖；字面同源化留后续 change 收敛）。
6. `internal/server/companion_stage_acceptance_test.go:45,65` 两处 `x, _ :=` 忽略返回值风格（E10 评审 Minor-2，风格备注）。
7. `internal/companion/planner.go` 系统提示头段「水平 16 格、垂直 8 格」仍为文本手抄数字，与 A5 合并后的代码常量无关联（E3 评审观察；窗口常量参数化时连带清理）。

**放弃（带理由）**：

1. planWireStep map 中间形的错误文本在「未知字段+类型错误并存」时因 map 迭代序二选一非确定——错误类别（ErrPlannerInvalidPlan）与全部门禁不受影响，接受现状（E1 评审 Minor-2）。
2. 注释内嵌散文数字（如 "1..256/1024 bytes"）在常量演进时会过时——由常量同源化后的引用式注释逐步替代，不批量回改（E7 评审观察）。
3. E2 评审的「位次锁测试单独不区分新旧 issuers 守卫条件」——完整覆盖由位次锁+空闲态回归锁两条测试组合承担，属设计取舍（E6 复审 Minor）。
