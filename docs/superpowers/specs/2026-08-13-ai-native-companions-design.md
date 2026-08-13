# AI-native 具名伙伴设计

**日期：** 2026-08-13
**状态：** 已批准
**目标基线：** M4Q（协议 v15、玩家 schema v6、区块 schema v8、世界 metadata v2）

## 1. 背景

Mornlea 已有服务端权威移动、采掘、放置、背包、持久世界、Memory/TCP 统一传输和远端玩家呈现，但没有游戏内 AI actor。现有历史设计提过传统怪物 AI，代码中尚无 NPC、LLM 或 Agent 运行时。

本阶段把游戏推进到第一个 AI-native 闭环：玩家不再只通过固定按键操作世界，也可以用自然语言与有性格、可见、可持久化的伙伴协作。LLM 负责理解意图和生成台词；世界行为仍完全由现有权威服务端校验和执行。

## 2. 用户体验

服主最多配置四个具名伙伴。任意在线玩家都可以在游戏内聊天框输入：

```text
@小木 去 12 65 -4，然后在那里放一个发光块
```

服务端公开广播指令的接受、排队、开始、关键进展和结果。伙伴以人形实体出现在世界中，能够：

- 前往指定坐标；
- 持续跟随指定玩家；
- 挖掘指定普通方块，并把产物直接收入自己的 36 格背包；
- 从自己的背包中扣除材料并放置指定方块。

每个伙伴有服主配置的自由文本人设。人设只影响说话方式，不参与计划生成，也不能改变动作权限。任务开始、关键进展、完成或失败时，伙伴可以根据现场异步生成一句台词。例如：

```text
Chen → 小木：去洞口挖那块铁矿
小木：那个黑黢黢的洞？行，但它得叫“别回头矿坑”。
小木：到洞口了。这里的风声可不像欢迎仪式。
小木：拿到了。别回头矿坑，零比一。
```

## 3. 已确认的产品决策

### 3.1 伙伴与权限

- 每个世界最多四个伙伴，由服主静态配置；首版不允许玩家创建或删除伙伴。
- 伙伴不占现有最多八名玩家的登录名额；上限是八名玩家加四个伙伴。
- 所有在线玩家都能指挥所有伙伴；首版没有伙伴所有者或权限组。
- 伙伴是独立 `CompanionID` 实体，不伪装成玩家、网络会话或 `PlayerID`。
- 伙伴没有生命、受伤、战斗或死亡；它只复用玩家的移动、采掘、放置和背包规则。

### 3.2 指令与队列

- 客户端只发送 `@伙伴名 指令`；不顺带建设通用玩家聊天。
- 普通指令按服务端接收顺序进入每伙伴独立 FIFO，最多保留 16 条。
- 同一伙伴同一时刻只规划或执行一条普通任务。
- `@伙伴名 停止` 是唯一绕过 FIFO 的控制指令，只终止当前持续跟随；终止后立即执行原队首任务，不清空队列。
- 当前任务和全部待执行 FIFO 必须跨重启恢复。

### 3.3 计划与执行时间

- LLM 只返回受限 JSON 计划，允许的步骤只有 `follow`、`go_to`、`mine` 和 `place`。
- 计划不设置显式步骤数量上限，但响应正文最多 64 KiB；这个字节上限同时给解析工作和实际步骤数提供硬边界。
- 单条玩家指令最多 1 KiB UTF-8；模型规划请求 30 秒超时且不自动重试。
- 普通任务执行时长由服主配置，默认 10 分钟，上限 60 分钟。
- 持续跟随不受普通任务时长限制，只能由“停止”结束。

### 3.4 失败语义

- 模型不可用、超时或返回非法计划时，当前任务失败并公开说明，随后处理下一条；不重试、不降级猜测。
- 路径不可达、目标变化、发令玩家离线、材料不足或动作被权威规则拒绝时，当前任务停止；Task Runner 不擅自改写计划。
- 配置非法、AI 存档损坏或未来版本令服务器拒绝启动，绝不静默丢弃伙伴状态。
- 运行期 AI 存档失败保留旧磁盘文件和内存 dirty 状态并按既有保存纪律重试；安全关服仍无法持久化时，关服返回错误。

## 4. 方案比较

### 4.1 采用：服务端伙伴编排器

服务端持有伙伴、任务队列和执行器。模型调用在 worker goroutine 中进行，只读取不可变快照并返回计划。确定性 Task Runner 再把计划逐步转换成权威模拟动作。

优点：

- 直接复用现有 `sim.Command`、物理、交互验证、区块兴趣和持久化纪律；
- LLM 延迟和非确定性不会进入 20 Hz 权威 tick；
- Memory/TCP 客户端观察到相同权威结果；
- 计划、执行和失败都可用假 Planner 做确定性测试。

### 4.2 否决：AI 作为外部虚拟网络玩家

外部 Bot 进程隔离强，也天然经过网络协议，但需要在游戏外重复建设附近世界观察、任务队列、伙伴存档和恢复协议。首阶段代码更多，仍不能解决独立伙伴容量和人设状态，因此不采用。

### 4.3 否决：LLM 直接进入权威 tick

模型调用会阻塞 tick，模型输出不可重放，也难以隔离超时和非法行为。这与服务端权威、确定性模拟和热路径边界冲突，因此禁止。

## 5. 总体架构

```text
玩家客户端
  └─ ChatCommand("@小木 ...")
       ↓
服务端 Companion Manager
  ├─ 名称解析、容量检查、FIFO 与任务生命周期
  ├─ 从权威状态取得不可变有限快照
  ├─ Planner worker ──HTTP──> OpenAI-compatible API
  ├─ Dialogue worker ─HTTP──> OpenAI-compatible API
  ├─ Task Runner ───────────> sim.CompanionAction
  └─ CompanionStore ────────> companions.ai
       ↓
既有 sim / world / storage 权威边界
       ↓
ChatEvent + CompanionSpawn/States/Despawn
       ↓
所有客户端的只读镜像与人形呈现
```

### 5.1 包职责

- `internal/companion`：`CompanionID`、配置后的稳定定义、任务/步骤值、严格 JSON 计划、FIFO 状态机、HTTP Planner/Dialogue 客户端和有界寻路。它不导入 `server`、`network` 或 `render`。
- `internal/sim`：M5A 先用独立 `companionState` 保存静态身体；M5B 首次加入移动时再把玩家移动/碰撞/采掘/放置需要的字段抽成不导出的 `actorState`。伙伴保存在按 `CompanionID` 索引的独立 map 中，并明确跳过玩家生命、死亡和登录语义。
- `internal/server`：唯一编排者；在完整 tick 边界取得快照、接收 worker 结果、推进 Task Runner、发布伙伴与聊天状态、调度 AI 状态保存。
- `internal/storage`：定义并实现有界 `CompanionStore`，为 Memory/Disk 世界提供相同的加载、原子保存、同步和关闭行为。
- `internal/network`：协议 v16 的 Chat 与 Companion 消息、codec、golden、fuzz 和长度门禁。
- `internal/client`：只读伙伴镜像、插值和聊天事件；不执行计划、不预测伙伴世界写入。
- `internal/render`：复用远端玩家的人形 mesh、名牌和上传缓冲，加入独立 companion presentation 类型。
- `cmd/mornlea`：聊天输入 UI、消息装配和无窗口视觉场景。
- `cmd/mornlea-server`：加载 AI 配置、从环境变量取得 API key，并装配 Planner/Dialogue 客户端。

### 5.2 Actor 复用边界

M5A 的伙伴不会移动、采掘或放置，只用最小 `companionState` 保存独立身体，不为尚未存在的共同行为预先改造 `playerState`。M5B 首次加入 `go_to` 时，`sim` 再从现有 `playerState` 提取不导出的 `actorState`，只容纳两类 actor 真正共有的运动、朝向、背包和采掘状态；`playerState` 保留健康、重生和玩家输入序号，`companionState` 保留稳定 `CompanionID`。两者届时共用：

- 碰撞、重力、移动输入和跳跃；
- 视线、交互距离和 Ready 区块校验；
- 工具耐久、方块采掘规则和放置规则；
- 36 格背包值语义与原子扣料。

伙伴不进入登录 session、心跳、玩家容量、玩家可见性表、PlayerStore、生命恢复、伤害、死亡或死亡掉落。现有只按八名玩家定长的 scratch 必须改为显式“八玩家 + 四伙伴”上限，并保留玩家上限测试。

玩家网络消息继续翻译成既有 `sim.Command`。Task Runner 通过独立、有界的 `sim.CompanionAction` inbox 提交伙伴输入；action 以 `CompanionID` 寻址，不能携带 `SessionID`。`Engine.Step` 先按现有规则处理玩家命令，再按 `CompanionID` 字节序处理本 tick 的伙伴 action，最后统一推进所有 actor 的物理与世界变更，给同一 tick 建立固定顺序。

每个伙伴只保持一格区块半径，即最多 `3×3` 个区块的独立兴趣范围，不继承玩家默认完整视距。这样四个空闲伙伴最多额外保持 36 个区块，而不是四份完整玩家视距。移动到边界时继续复用区块 acquire/generate/persistence 流程。

## 6. 配置与密钥

现有配置增加可选 `ai` 组。旧配置缺失该组时 AI 功能关闭，原有行为不变：

```json
{
  "version": 1,
  "ai": {
    "endpoint": "https://example.invalid/v1",
    "model": "model-name",
    "apiKeyEnv": "MORNLEA_AI_API_KEY",
    "taskTimeoutMinutes": 10,
    "companions": [{
      "id": "123e4567-e89b-42d3-a456-426614174000",
      "name": "小木",
      "persona": "乐观但有点怕黑，喜欢给洞穴取名字。说话简短，不嘲讽玩家，不声称发生过不存在的事。"
    }]
  }
}
```

配置同样按交付批次增长：M5A 只识别 `ai.companions` 中的 `id` 与 `name`；M5B 增加 endpoint、model、apiKeyEnv 与 taskTimeoutMinutes；M5D 再增加 persona。每一批都保持 config version 1，并把尚未实现的字段按既有未知字段纪律告警后忽略，不能提前让模型配置成为 M5A 的启动条件。

约束：

- 伙伴数 `1..4`；ID 必须是规范 UUIDv4 且唯一。
- 名称为 `1..32` 个 Unicode 字符、最多 128 字节、无控制字符或空白，且大小写敏感唯一，保证 `@名称` 无歧义。
- 人设是有效 UTF-8、最多 4 KiB、无 NUL。
- `taskTimeoutMinutes` 范围 `1..60`，缺失时为 10。
- endpoint 是 OpenAI-compatible base URL；客户端在其后追加 `/chat/completions`。它只接受无 userinfo/query/fragment 的 `https` URL，以及 hostname 经 `net.ParseIP` 判定为 loopback 的 `http` URL，从而同时支持云端和本地模型。
- 远程 `https` endpoint 必须配置 `apiKeyEnv` 且对应环境变量非空；loopback `http` 可以省略 key。非空伙伴配置缺少 endpoint、model 或所需 key 时，专用服务端和内置服务端均启动失败。
- key 不写配置、日志、模型错误、性能报告或世界存档。

首版直接使用 Go `net/http` 调 OpenAI-compatible Chat Completions，不引入 SDK。HTTP 客户端有固定响应头/正文上限、30 秒超时和 context 取消；错误正文不得原样回显给玩家或日志。

`ai` 是可选、向后兼容的配置组：当前配置 `version` 保持 1。旧版程序会按既有规则警告并忽略未知 `ai` 组；新版程序读取缺少 `ai` 的 v1 文件时保持 AI 关闭。调试面板保存配置时必须原样保留已经解析的 `ai` 值，但首版不在面板中编辑 key、endpoint、人设或伙伴清单。

## 7. 协议 v16 与客户端交互

协议从 v15 升到 v16，不提供跨版本兼容。新增消息：

- C→S `ChatCommand`：规范 UTF-8 文本，最大 1 KiB；只允许 `@伙伴名 指令`。
- S→C `ChatEvent`：稳定 event ID、发令玩家 ID/显示名、伙伴 ID/名称、事件 kind、服务端事实原因和有界模型文本。
- S→C `CompanionSpawn`：ID、名称、tick、维度、位置和朝向。
- S→C `CompanionStates`：单批 `1..4` 个按 ID 排序的状态。
- S→C `CompanionDespawn`：ID。

客户端按既有远端玩家的规则，在对应区块快照已经发布且伙伴位于观察者兴趣范围内时接收 spawn/state。伙伴消息使用独立类型和客户端 map；不得把 `CompanionID` 填进 `RemotePlayerSpawn`。

聊天 UI 只提供本闭环需要的输入和滚动事件：`Enter` 打开/发送、`Esc` 取消、固定历史条数与固定文本长度。首版不发送普通玩家聊天，也不保存完整聊天记录。

## 8. Planner 输入与计划契约

### 8.1 不可变有限观察

服务端在 tick 边界生成 Planner 快照，之后 worker 只能读取副本。快照包含：

- 发令玩家的稳定 ID、位置、朝向和视线命中的方块；
- 伙伴 ID、位置、朝向、背包和当前任务状态；
- 伙伴周围水平 16 格、垂直 8 格范围的确定性环境摘要；
- 该范围的高度信息、最多 256 个按坐标排序的暴露/特殊方块，以及相关区块 revision；
- 当前世界时间。

Planner 不接收伙伴人设、对话摘要、API key、其他玩家聊天或世界存档路径。人设不能通过提示注入影响动作规划。

### 8.2 严格 JSON

Planner 返回单一 JSON object：

```json
{
  "summary": "去目标位置并放置发光块",
  "steps": [
    {"kind": "go_to", "x": 12, "y": 65, "z": -4},
    {"kind": "place", "x": 12, "y": 65, "z": -5, "block": "light_block"}
  ]
}
```

解码使用 `json.Decoder`、拒绝未知字段、拒绝尾随数据，并在分配前限制正文 64 KiB。坐标必须在世界边界内；玩家 ID、block 名和所有 enum 必须来自当前快照与固定注册表。空计划、未知 step、非法数值或不规范文本令当前任务失败。

支持的 step：

- `follow(player_id)`：持续跟随当前在线玩家；只能作为计划最后一步。
- `go_to(x,y,z)`：走到目标方块邻近的合法站立点。
- `mine(x,y,z)`：走入交互距离后挖明确目标。
- `place(x,y,z,block)`：走入交互距离后从伙伴背包扣除对应物品并放置。

首版 `mine` 只接受具有单一 `BlockDrop` 且不是箱子或熔炉的普通方块。容器可能包含多组物品，与“产物直接进入伙伴背包”的原子容量语义不同，因此明确拒绝，留给后续单独设计。

## 9. Task Runner 与寻路

### 9.1 状态机

```text
Queued → Planning → Validating → Running
                                ├→ Completed
                                ├→ Failed
                                └→ TimedOut
```

任一终态都会产生规范 `ChatEvent`、标记 AI 状态 dirty，并推进 FIFO 下一项。模型计划只在 `Validating` 成功后落盘；重启恢复的 `Running` 任务从已完成步骤之后继续，并在下一动作前重新校验世界状态。普通任务的 deadline 使用持久化的 `WorldTimeTicks`，因此安全关服期间不消耗执行时长。

### 9.2 有界路径

移动使用确定性有界网格寻路，不允许 LLM 选择每 tick 输入：

- 每次路径快照覆盖伙伴附近水平 16 格、垂直 4 格；
- 可在有支撑的站立格移动，可跨越一格水平间隙，可跳上一格；
- 不游泳、不攀爬、不搭桥、不为了通路挖方块；
- 单次最多考察 4096 个节点，邻居顺序固定，保证结果可重放；
- 寻路在 worker 上操作不可变区块快照，结果带相关 revision；
- Task Runner 在使用每个路径点前重新验证；失效时按固定冷却重新规划，连续三次无路则失败。

### 9.3 动作原子性

- `go_to` 和 `follow` 每 tick 只提交一个规范移动输入，权威物理决定实际位置。
- `follow` 以固定距离跟随目标玩家；玩家离线则任务失败。“停止”清空当前移动输入并进入终态。
- `mine` 开始前先验证目标、工具和背包容量。成功完成时，在同一权威 tick 内把方块改为空气、扣工具耐久并把可收获产物加入伙伴背包；任一步不能完成则不修改方块或背包。
- `place` 在同一权威 tick 内验证目标、碰撞和物品后，原子扣除一件并写入方块；失败不扣料。
- 模型不能自动插入清障、拾取、整理、合成或换工具动作；计划中没有的行为永远不发生。

## 10. 性格、台词与近期记忆

Dialogue worker 与 Planner 使用同一 OpenAI-compatible endpoint/model，但提示和输入完全隔离。它只接收：

- 伙伴自由文本人设；
- 最近对话摘要；
- 当前事实节点和规范成功/失败原因；
- 一个极小的附近环境摘要。

触发节点包括开始、确定性选出的关键进展、完成和失败。一个任务最多发起八次台词请求：开始一次、终态一次，其余最多六次按计划长度均匀选择的步骤完成节点。持续跟随只有开始、首次到达跟随距离和终止节点。

台词请求永不阻塞任务：

- 每伙伴最多一个台词请求在途；新节点到来时仍有请求则跳过新台词。
- 全服务端最多四个模型请求并发；Planner 等待槽位，Dialogue 取不到槽位就跳过。
- 返回结果携带任务 ID 与节点 ID；任务或节点已过时则丢弃。
- 模型失败只跳过台词，不改变任务状态。
- 失败事件始终附服务端产生的规范原因；模型文本不能替换或隐藏事实。

终态台词响应同时返回一段新的近期摘要，避免为摘要额外调用模型。摘要最多 2 KiB，完整聊天不落盘；终态请求失败时保留旧摘要。摘要只用于后续 Dialogue，不进入 Planner，因而不能改变世界行为。

## 11. 持久化

世界目录新增单一 `companions.ai`，随分批交付显式升级 schema：

- M5A schema v1 只保存伙伴身体记录：位置、朝向和 36 格背包；
- M5B schema v2 增加当前任务、计划、步骤索引、状态、开始 tick、deadline 与最多 16 条 FIFO；
- M5D schema v3 增加最近对话摘要。

每次升级都保留旧 schema 的只读迁移覆盖；未来 schema 必须拒绝，不能降级写回。文件最多保存 64 条身体记录，其中同时激活的仍最多四条。配置移除的记录计入 64 条总上限；达到上限时拒绝引入新 ID，绝不自动删除旧记录。

最终 schema 保存：

- 配置中每个 CompanionID 的位置、朝向、36 格背包；
- 当前任务的原始指令、计划、步骤索引、状态、开始 tick 和 deadline；
- 最多 16 条 FIFO 指令；
- 最近对话摘要；
- revision 与必要的恢复身份。

文件使用明确 magic、版本、长度和 CRC32C，并复用现有临时文件、文件 `Sync`、原子 `Rename`、父目录 `Sync` 的替换纪律。MemoryStore 与 DiskStore 实现相同 `CompanionStore` 语义；`WorldStore` 组合该接口。所有字符串、集合和步骤都在解码分配前检查上限。

恢复规则：

1. 先验证静态配置，再读取存档。
2. 配置新增伙伴时从世界出生点创建空背包伙伴。
3. 存档里存在但配置移除的伙伴保留在文件中但不激活，防止误删配置造成数据丢失。
4. 恢复当前任务与 FIFO；在执行下一动作前重新校验目标与 revision。
5. AI 状态修改只标记 dirty，磁盘 I/O 由有界 save worker 执行，不占权威 tick。
6. 关服先停止接受新 ChatCommand，取消模型请求，冻结队列和 actor 快照，完成最终 AI 保存，再关闭世界存储。

这会按 M5A/M5B/M5D 分别发布 companion schema v1/v2/v3；玩家 schema v6、区块 schema v8 和世界 metadata v2 不变。

## 12. 并发、资源与安全边界

- 权威 tick 是伙伴身体、背包、队列执行状态和世界动作的唯一写者。
- Planner、Dialogue、Pathfinder 和存档 worker 只处理不可变值；返回结果经有界 channel 在 tick 边界应用。
- 最多四个伙伴、每伙伴 16 条队列、64 KiB 计划、4 KiB 人设、2 KiB 摘要、四个模型请求并发和固定路径节点预算均为硬门禁。
- 跨 goroutine 发送后的切片不可变；模型响应解码后复制为拥有值。
- 不执行模型返回的代码、URL、工具名、shell、模板或任意函数调用。
- 玩家文本、世界方块名和 Dialogue 摘要都视为不可信数据；系统提示明确其为数据，最终仍以本地 schema 白名单为唯一权限边界。
- TCP 仍只面向可信局域网且没有认证或加密；“所有玩家可指挥伙伴”是明确产品选择，不扩张为公网安全承诺。

## 13. 可观察失败与错误处理

- 队列满、伙伴不存在、文本非法或过长：同步拒绝且不调用模型。
- Planner HTTP/状态码/大小/JSON/schema 错误：记录不含 secret/响应正文的上下文，向玩家发规范失败事件，推进 FIFO。
- Dialogue 错误：静默跳过台词，仅保留 debug 级结构化原因。
- Pathfinder 预算耗尽或世界失效：按固定次数重算，最终公开“无法到达”。
- 权威动作拒绝：公开稳定的玩家可读原因，并保留底层 reject code 供测试和日志。
- CompanionStore 读取损坏或未来版本：拒绝启动；保存失败不得把旧文件当作已更新，也不得清除 dirty 状态。
- 慢客户端仍遵循既有 outbox 关闭纪律，不能反压权威 tick 或模型 worker。

## 14. 测试与验收

### 14.1 自动测试

1. **核心与配置**：CompanionID、名称/人设/endpoint/key 校验、四伙伴和各字节上限。
2. **协议 v16**：registry、codec golden、大小门禁、未知/非规范数据、Chat/Companion fuzz、v15 明确拒绝。
3. **存档 v1**：Memory/Disk 往返、CRC/未来版本/截断/超大集合、原子替换故障注入、旧文件保留、关服 flush 与重启恢复。
4. **Planner**：`httptest.Server` 覆盖成功、超时、5xx、超大响应、未知字段、尾随 JSON、prompt 隔离和 context 取消。
5. **状态机**：FIFO 顺序、16 条容量、当前任务恢复、普通超时、持续 follow/停止旁路、过时 worker 结果丢弃。
6. **寻路**：固定邻居顺序、跨/跳一格、4096 节点上限、revision 失效、三次失败和不自动挖障碍。
7. **权威动作**：go_to/follow 物理、mine/place 背包原子性、工具耐久、容器拒绝、目标变化、区块未 Ready。
8. **多人传输**：至少两名玩家共同指挥同一伙伴，Memory/TCP 的事件、实体状态、FIFO 和重启结果一致。
9. **并发与热路径**：慢 HTTP/磁盘不阻塞 tick，队列/关闭 race，全请求和路径预算有界。
10. **呈现**：人形伙伴、名牌和聊天框的 focused render 测试；新增无窗口 `ai-companion` 场景并由用户逐图确认 golden。

### 14.2 完成门禁

```bash
go test ./path/to/affected/package -race -count=1
go test ./internal/archcheck -count=1
go test ./... -race
go vet ./...
gofmt -l .
openspec validate --all --strict --no-interactive
```

协议、存档和 JSON parser 还需对应 golden/fuzz；tick、寻路、渲染和模型 worker 运行对应 benchmark，只记录性能数值。报告完整性、真实 overflow、数据丢失和 I/O 错误仍是门禁。自动测试只使用假 HTTP 服务，不访问真实模型，也不打开前台游戏窗口。

### 14.3 用户可观察完成标准

在 Memory 和 TCP 两种模式下，两名玩家都能：

- 看见同一个具名人形伙伴及其名牌；
- 用 `@名称` 连续提交多条复合指令并观察严格 FIFO；
- 让伙伴前往坐标、持续跟随并用“停止”结束、挖普通方块进自身背包、从背包放置方块；
- 看到不阻塞动作且符合人设的开始、进展和终态台词；
- 重启后继续当前任务和队列，并保留位置、背包与近期摘要；
- 在模型、路径或动作失败时得到真实原因，且世界和背包没有部分写入。

## 15. 分批交付

该目标横跨协议、存档、权威模拟、HTTP、客户端和渲染，不应作为一个不可评审的大提交。按依赖顺序拆成四个 OpenSpec change，每个 change 都独立设计、实现、验证、归档后再开始下一个：

1. **M5A — 伙伴实体与聊天基础**：CompanionID、静态配置、协议 v16 Chat/Companion 消息、最多四个独立 actor、`companions.ai` 身体状态、客户端镜像/人形呈现和无窗口场景。使用确定性测试入口证明聊天寻址，不接真实 Planner。
2. **M5B — 自然语言计划与持久 FIFO**：OpenAI-compatible Planner、有限观察、严格 JSON、队列/当前任务持久化、`go_to` 与普通任务超时。交付第一个真正可玩的 AI-native 闭环。
3. **M5C — 跟随与世界交互**：持续 `follow`/停止旁路、有界寻路完整边界、`mine`/`place`、伙伴背包与权威原子性、Memory/TCP 多人一致性。
4. **M5D — 性格与近期记忆**：自由文本人设、非阻塞节点 Dialogue、八次预算、终态摘要、聊天呈现打磨和阶段总验收。

每个 change 只沉淀当时已经实现的主规格，不预先把后续行为标为完成。

## 16. 非目标与后续方向

首阶段明确不实现：

- 自主决定目标、自动采集资源或自动建造；
- 怪物 AI、战斗、生命、装备、受伤和死亡；
- 自动拾取、背包整理、合成、熔炼或开启容器；
- 自动挖障碍、搭桥、游泳或无限世界寻路；
- 玩家创建伙伴、伙伴所有权、ACL 或计费系统；
- 完整聊天历史、向量数据库、RAG、长期人格演化；
- 多 Agent 框架、伙伴间自主协商、模型代码或工具执行；
- 空闲随机聊天、主动修改世界、动态任务和世界事件。

这些能力只有在 M5A–M5D 的真实玩家体验证明需要后，才分别进入新的 OpenSpec change。

## 17. 回退策略

- `ai.companions` 为空时不创建 manager/worker/actor，不发送 Companion 消息，除协议版本升级外行为与当前基线一致。
- M5A–M5D 每批均可在不读取后续字段的情况下独立回退；存档未来版本必须拒绝而不是降级写回。
- Planner 或 Dialogue 运行期不可用只影响对应任务/台词，不影响普通玩家世界循环。
- 若阶段需要完全关闭 AI，服主先安全关服并清空静态伙伴配置；`companions.ai` 原样保留，重新启用同 ID 时恢复。
