# Design: m5d-companion-persona-dialogue

## 设计总览

M5D 在 M5C 的任务闭环之上加一层完全旁路的「表达平面」：任务事实平面（状态机、FIFO、世界动作、事实 ChatEvent）一行代码不改语义，Dialogue 只读取事实并产出两条纯呈现输出——`CompanionSpeech` 台词事件与最近对话摘要。这一分层是全部并发与安全论证的基础：表达平面任何环节的失败最多表现为「少一句台词」，不可能改变世界或任务。

## 数据所有权与依赖方向

- `internal/companion`：新增 `persona.go`（Persona 类型、`ValidatePersona` 4,096-byte 边界）、`dialogue_types.go`（DialogueRequest 输入构造、DialogueResponse 严格解码、line ≤256/summary ≤2,048 校验）、触发节点纯函数 `dialogue_nodes.go`（`SelectProgressSteps(plan) []int` 均匀选择、follow 三节点常量）。全部纯函数、无 I/O。
- `internal/config`：`applyAI` 识别 `persona` 字段；新增 `resolvePersonas(configPath, definitions)` 在启动时解析 `personas/<canonical 名称>.txt`（内联优先 + 双源告警 + 损坏降级空人设）。config 只依赖 os/filepath 与 companion 类型，方向不变。
- `internal/network`：`ChatEventKind` 追加 `ChatEventCompanionSpeech = 9`；ChatEvent 组合校验扩展（Speech 携带伙伴 + 台词、reason None、广播语义由 server 决定）。wire 上限 1328 不变：256 台词 + 伙伴身份 + event ID 远小于上限，golden 锁定。
- `internal/storage`：schema v4。记录布局 = v3 记录 + 可选 2-byte 长度前缀摘要区（仅 active、仅有摘要时写入）；`maxCompanionFileLength` = 430,080 + 4×(2+2,048) = 438,280。v3/v2/v1 迁移读入后摘要为空、重写为 v4。`StoredCompanionQueue`/`StoredCompanions` 增加 `Summary []byte`（或独立 map），inactive 编码时丢弃。
- `internal/server`：`companion_dialogue.go` 新增 Dialogue worker（结构对齐 Planner worker：不可变请求值、有界结果 channel、30 秒超时、无重试）；`companion_manager.go` 在 tick 边界评估节点、应用结果。并发槽：现有 Planner 槽位抽象扩展为共享 4 槽信号量，Planner 获取路径改为等待、Dialogue 路径为 try-acquire 失败即跳过。
- `cmd/mornlea/chat.go`：`formatChatEvent` 增加 Speech 分支 → `名称：台词`。

依赖方向全部落在既有 archcheck 白名单内（config→companion、server→companion/network/storage、client→network），无新增包。

## 触发节点确定性算法

普通任务（计划 n 步）：开始节点 = 进入 Running；进展节点集合 = `SelectProgressSteps`：当 n ≤ 6 时全选，否则按 `i*(n)/6` 向下取整的等距索引去重选择，恰好 ≤6 个；终态节点 = 四种终态其一。总请求 ≤ 1+6+1 = 8。follow 任务：开始、首次进入跟随距离（Task Runner 已有该事实）、终止。节点在计划校验成功后一次性预计算并随任务保存于内存（不落盘——重启后从当前进度重新导出，预算以「本进程已发起数」计数，重启后从头计，这是可接受的松弛：预算的目的是限制模型调用量，不是持久事实）。

**裁决**：预算计数不持久化。摘要才是持久记忆；重启后任务最多多花 ≤8 次请求，属可接受的模型调用量上界。

## Dialogue 请求与响应契约

请求（worker goroutine 构造，不可变）：persona（≤4,096）、summary（≤2,048）、node（kind: start/progress/terminal、step kind、稳定原因枚举 terminal 时携带）、env digest（复用观察快照的环境摘要构造器，输出同构的有界值，绝不包含 key）。系统提示为固定模板，明确 persona/摘要/节点都是数据。

响应严格解码矩阵（对齐 Planner 纪律）：`json.Decoder` 单 object、`DisallowUnknownFields`、拒绝尾随数据、64 KiB 分配前上限；`line` 1..256 bytes 有效 UTF-8 无 NUL/control；终态必须额外有 `summary` ≤2,048 bytes，非终态出现 `summary` 即非法。任何失败 → 跳过台词（终态同时保留旧摘要），debug 级结构化日志（不含响应正文原文与 key）。

## 并发模型

- 共享模型槽：现有 Planner 4 槽并发闸改造为 `modelGateway`（命名以实现为准）：`acquire(ctx)`（阻塞等待，Planner 用）与 `tryAcquire()`（非阻塞，Dialogue 用）。关服 cancel 同时唤醒两者。
- Dialogue worker 池与 Planner 相同的扇入模式：每个请求一个 goroutine？不——每伙伴至多一个在途由 manager 侧 `dialogueInFlight map[ID]struct{}` 在 tick 边界保证（新节点到来时若在途即跳过，不取消在途）。worker 只处理已接受的请求。
- 结果 channel 有界（容量 = 伙伴上限 4），满时丢弃最旧结果（结果本就可能过时，丢弃语义与过时丢弃一致）。
- 台词文本在 tick 边界复制进 ChatEvent 后广播；摘要写入 manager 持有的 `summaries map[ID][]byte` 并标记 dirty。

## 兼容与迁移

- 协议 v18→v19：v18 客户端被拒；golden 字节更新，无 wire 形状变化（新 kind 复用既有变体编码）。
- 存档 schema v3→v4：只读迁移 + 首次保存重写；golden `companions-v4.bin` 新增，v3/v2/v1 golden 保持原字节可读；fuzz 语料沿用并断言新界。解码侧新增「payload 读空」门禁：记录循环结束后剩余字节非零即拒绝（合法历史编码器均精确写长，无兼容风险；该门禁同时封死 v2/v3 时代变长区缩小错位的残留字节洞）。
- config v1 不变：persona 从未知字段转为已识别字段，旧配置零影响。
- 回退：M5D 整体可按里程碑独立回退（关掉 persona/Dialogue 路径后行为等同 M5C）；存档 v4 对 M5C 是未来版本会被拒绝——回退需连带存档回退，符合「未来版本必须拒绝，不能降级写回」纪律。

## 风险与验证

- 提示注入（persona/摘要/模型输出）：三重边界——输入只进 Dialogue 提示、输出只经 line/summary 白名单解码、台词只是显示文本；测试锁定 persona/摘要不进 Planner 请求（既有 planner_test 扩展两个来源）。
- 热路径阻塞：Dialogue 全链路在 worker/tick 边界之外无分配进入 tick；沿用 M5B 的「慢模型不阻塞 tick」测试模式（4 伙伴在途）。
- 槽位饥饿：Dialogue try-acquire 保证 Planner 可无限抢占；反向饥饿（Planner 长期占满导致台词全跳过）是产品可接受降级，测试锁定「跳过不排队」。
- 摘要与任务事实混淆：schema v4 摘要区独立于任务区；测试锁定 inactive 丢弃、终态失败保留旧摘要。

## 被否决的替代方案

- **台词挂在既有 TaskProgress/TaskCompleted 上附带文本**：破坏「任务事件不携带模型自由文本」的不变量，客户端无法区分事实与生成文本；否决，改独立 `CompanionSpeech`。
- **摘要专用模型调用**：每任务多一次模型请求且终态时机已有一次请求；否决，终态响应捎带 `summary`。
- **persona 按 UUID 文件名**（`personas/<uuid>.txt`）：UUID 对服主不可读；canonical 名称已验证无空白与路径字符，直接可用；否决 UUID 方案。
- **Dialogue 排队等槽**：台词是尽力而为的表达，排队会让迟到台词在错误语境出现；否决，无槽即跳过。
- **预算计数持久化**：把限流事实混入任务持久状态、增加 schema 字段；重启松弛可接受；否决。

## 受影响文件（实现清单级）

`internal/companion/{persona,dialogue_types,dialogue_nodes}.go`、`internal/config/config.go`、`internal/network/{message_companion,packet}.go`、`internal/storage/{companion_types,companion_codec}.go`、`internal/server/{companion_dialogue,companion_manager,companion_worker}.go`（槽位改造）、`cmd/mornlea/chat.go`、各对应 `_test.go` 与 `internal/storage/testdata/companions-v4.bin`。
