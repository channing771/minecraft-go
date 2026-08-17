# Tasks: m5d-companion-persona-dialogue

## D1 协议 v19 与 CompanionSpeech 线格式

- [x] D1.1 先写失败测试：`internal/network` 中 `ChatEventCompanionSpeech` kind 组合校验（伙伴身份 + 1..256 bytes 台词 + reason None 的合法/非法矩阵）、编码解码往返、ChatEvent 1326/1327/1328 边界保持、ProtocolVersion=18 登录被拒而 v19 通过
- [x] D1.2 最小实现：`ChatEventKind` 追加 `ChatEventCompanionSpeech = 9`、组合校验与编码分支、`ProtocolVersion` 升 19；更新既有协议 golden 字节
- [x] 验证：`go test ./internal/network -race -count=1` 全绿，golden 更新仅限新增 kind

## D2 persona 配置解析

- [x] D2.1 先写失败测试：`internal/config` 内联 persona 4,096/4,097 边界、NUL/非法 UTF-8 告警降级、`personas/<canonical 名称>.txt` 读取、内联优先 + 双源告警、文件损坏降级、目录不存在静默空人设、无 persona 不告警
- [x] D2.2 最小实现：`Definition` 增加 Persona 字段、`applyAI` 识别 persona、`resolvePersonas` 外部文件解析；确认未知字段纪律不再覆盖 persona
- [x] 验证：`go test ./internal/config -race -count=1`

## D3 Dialogue 类型与严格解码

- [x] D3.1 先写失败测试：`internal/companion` 响应矩阵（未知字段、尾随数据、>64 KiB、line 256/257、line 含 control、终态缺 summary、非终态带 summary）、`SelectProgressSteps` 确定性（n≤6 全选、n=12 等距 ≤6、去重）、follow 三节点常量、请求构造只含四类有界输入
- [x] D3.2 最小实现：`persona.go`、`dialogue_types.go`、`dialogue_nodes.go`
- [x] 验证：`go test ./internal/companion -race -count=1`

## D4 schema v4 摘要持久化

- [x] D4.1 先写失败测试：v4 编码解码往返（含摘要记录与无摘要记录混排）、438,280 上界拒绝、非法摘要长度拒绝、v3/v2/v1 只读迁移（摘要为空、首存 v4）、inactive 丢弃摘要、golden `companions-v4.bin`、fuzz 断言
- [x] D4.2 最小实现：`companion_types.go` 摘要字段、`companion_codec.go` v4 编码与迁移
- [x] 验证：`go test ./internal/storage -race -count=1`（含 fuzz 种子语料）

## D5 Dialogue worker 与共享模型槽

- [x] D5.1 先写失败测试：`internal/server` try-acquire 无槽即跳过且不排队、每伙伴 1 在途（新节点跳过）、结果过时丢弃（任务终态后）、30 秒超时不重试、失败只跳过台词不改任务状态、4 伙伴在途不阻塞权威 tick、关服取消在途请求
- [x] D5.2 最小实现：模型槽改造为共享 gateway（Planner 等待 / Dialogue try）、`companion_dialogue.go` worker
- [x] 验证：`go test ./internal/server -race -count=1`

## D6 触发节点接线与事件广播

- [x] D6.1 先写失败测试：开始/进展/终态节点各触发一次且总数 ≤8、终态含 Stopped、follow 仅三节点、CompanionSpeech 广播全部在线玩家、Memory/TCP 事件序一致、摘要更新标记 dirty、任务事实序列与 M5C 完全一致（无台词对照）
- [x] D6.2 最小实现：`companion_manager.go` 节点评估、结果应用、事件发布、摘要持有
- [x] 验证：`go test ./internal/server -race -count=1`

## D7 客户端台词呈现

- [x] D7.1 先写失败测试：`cmd/mornlea` Speech 事件生成 `名称：台词` 行、32 rune 截断、与任务事实行不混排、断线清空
- [x] D7.2 最小实现：`chat.go` Speech 分支
- [x] 验证：`go test ./cmd/mornlea -race -count=1`

## D8 M5A–M5D 阶段总验收

- [x] D8.1 端到端集成测试（httptest 假模型）：persona 文件配置 → `@指令` → 四 kind 计划 → 台词事件序列（开始/进展/终态）→ 终态摘要落盘 → 重启恢复 → 摘要进入下一次 Dialogue 请求输入；Memory/TCP parity；全程无前台窗口
- [x] 验证：`go test ./internal/server -race -count=1 -run 集成场景命名`

## D9 收尾门禁

- [ ] D9.1 `gofmt -l .` 无输出、`go vet ./...`、`go test ./internal/archcheck -count=1`
- [ ] D9.2 `go test ./... -race` 全绿；涉及 tick/存储/协议热路径，跑对应 benchmark 与 `cmd/perfcheck` 记录数值（只记录）
- [ ] D9.3 `openspec validate m5d-companion-persona-dialogue --strict --no-interactive` 通过；`docs/notes/progress.md` 补 M5D 段落
