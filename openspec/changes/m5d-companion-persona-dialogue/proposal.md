# Proposal: m5d-companion-persona-dialogue

## 背景

M5A 交付了具名伙伴实体与 `@伙伴名 指令` 寻址；M5B 交付了 FIFO、OpenAI-compatible Planner、整数 A* 与 `go_to` 执行；M5C 交付了 follow/停止旁路/mine/place 四 kind 步骤全集、协议 v18 与 `companions.ai` schema v3。伙伴现在能听懂并执行指令，但没有性格表达：任务事件只有服务器生成的稳定事实行，模型自由文本刻意不上屏，任务 `summary` 与世代也刻意不落盘（M5C 明确标注「M5D 前不上屏」）。

原始设计（`docs/superpowers/specs/2026-08-13-ai-native-companions-design.md` §10/§11/§15 批次 4）为 M5D 规划了性格与近期记忆：自由文本人设、非阻塞节点 Dialogue、八次预算、终态摘要、聊天呈现打磨和阶段总验收。两点需要修正：设计文档写「M5D schema v3」，但 v3 已被 M5C 变长步骤占用，本变更是 schema v4；设计文档成文于 Stopped 终态存在之前，本变更裁决 Stopped 也是终止台词节点。

## 目标

- `ai.companions[].persona` 可选内联自由文本人设（≤4 KiB），并支持从配置文件同目录 `personas/<canonical 名称>.txt` 读取，内联优先。
- Dialogue worker 复用既有 endpoint/model/apiKeyEnv/timeout 配置，与 Planner 提示与输入完全隔离：只接收 persona、最近对话摘要、当前事实节点（任务 ID、step kind、稳定原因枚举）与极小环境摘要。
- 触发节点确定：进入 Running 一次、按计划长度确定性均匀选择至多六个步骤完成节点、终态一次（Completed/Failed/TimedOut/Stopped 都是终止节点），每任务合计至多八次台词请求；持续跟随只有开始、首次到达跟随距离与终止三个节点。
- 台词请求永不阻塞任务：全服四模型槽共享（Planner 等待、Dialogue 无槽即跳过）、每伙伴最多一个在途、结果携带任务与节点身份过时即丢、模型失败只跳过台词且绝不改变任务状态。
- 终态台词响应同时返回新的最近对话摘要（≤2 KiB），不额外调用模型；完整聊天不落盘；终态请求失败保留旧摘要；摘要只喂后续 Dialogue，绝不进入 Planner。
- 协议升到 v19：新增 `CompanionSpeech` ChatEvent kind（≤256 bytes 有界台词、广播全部在线玩家）；任务事件仍不携带模型自由文本。
- `companions.ai` 升到 schema v4：每条记录可选最近对话摘要，v3/v2/v1 只读迁移，文件上界相应上调。
- 客户端聊天 HUD 以 `伙伴名：台词` 一行呈现台词（唯一显示模型文本的位置），Memory/TCP 同序。
- M5A–M5D 阶段总验收：端到端集成测试（httptest 假模型、重启恢复、Memory/TCP parity），全程不开前台窗口。

## 非目标

- 人设影响规划：persona 与摘要绝不进入 Planner 输入，动作权限不变。
- 完整聊天历史落盘：只保存最近摘要，不保存逐条台词或玩家聊天。
- M5B/M5C 终审遗留的约 20 条 minor 欠账（含双实现交叉锁测试）：留给 M5E 或独立小变更。
- 不引入新 message ID、不改变 ChatCommand/ChatEvent wire 上限、不新增模型并发槽。
- 不做 persona 运行期热更新：只启动时加载一次。

## 用户可观察结果

- 配置了 persona 的伙伴在任务开始、关键进展与终态时广播一句符合人设的台词（`阿木：……`），无 persona 的伙伴照常执行任务但没有台词风格约束。
- 台词迟到或被跳过不影响任务推进；模型故障只表现为「没有台词」，事实事件序列与 M5C 完全一致。
- 重启后伙伴保留最近对话摘要，后续台词请求以该摘要为近期记忆输入。
- v18 客户端被 v19 服务端按既有版本门禁拒绝。

## 受影响包与文档

- `internal/config`：persona 内联字段与 `personas/` 目录文件解析。
- `internal/companion`：persona 装载、Dialogue 请求构造与响应严格解码、摘要类型。
- `internal/network`：协议 v19、`CompanionSpeech` kind 与组合校验。
- `internal/storage`：schema v4（可选摘要字段、新文件上界、v3/v2/v1 只读迁移）。
- `internal/server`：Dialogue worker、并发槽共享、触发节点接线、CompanionSpeech 广播。
- `cmd/mornlea`：台词事实行格式化。
- `docs/notes/progress.md`：M5D 基线段落。
