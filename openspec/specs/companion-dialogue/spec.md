# companion-dialogue Specification

## Purpose
TBD - created by archiving change m5d-companion-persona-dialogue. Update Purpose after archive.
## Requirements
### Requirement: Dialogue 输入有界且与 Planner 隔离

Dialogue worker SHALL 复用 `companion-identity-configuration` 定义的 endpoint、model、apiKeyEnv 与请求超时配置，调用同一 OpenAI-compatible `/chat/completions`，但系统提示与输入 MUST 与 Planner 完全隔离。一次 Dialogue 请求的输入 MUST 只包含：该伙伴的人设（≤4,096 bytes，可为空）、最近对话摘要（≤2,048 bytes，可为空）、当前事实节点（任务身份、step kind 或节点类型、服务器侧稳定成功/失败原因枚举）与一个极小的附近环境摘要。请求输入 MUST NOT 包含 API key、其他玩家聊天文本、世界存档路径或 Planner 系统提示。人设、摘要与节点文本都 MUST 视为不可信数据：服务端 MUST NOT 执行模型返回或输入中出现的代码、URL、工具名或任意函数调用。

#### Scenario: 请求输入只含四类有界数据

- **GIVEN** 一个带 persona 与既有摘要的伙伴任务到达台词触发节点
- **WHEN** Dialogue worker 发起请求
- **THEN** 请求正文 MUST 只包含人设、摘要、事实节点与附近环境摘要，MUST NOT 包含 key、其他玩家聊天或存档路径

#### Scenario: 输入不泄漏密钥

- **GIVEN** 一个配置了非空密钥环境变量的服务端与一次台词请求
- **WHEN** worker 发起请求并随后失败
- **THEN** 请求正文与错误上下文 MUST NOT 包含密钥值

### Requirement: 触发节点确定且每任务八次预算

台词触发节点 SHALL 完全由服务器确定性导出：任务进入 Running 时一次；普通任务按计划长度确定性均匀选择至多六个步骤完成节点；任务进入终态（`Completed`、`Failed`、`TimedOut`、`Stopped` 全部视为终止节点）时一次。同一任务全生命周期 MUST 最多发起八次台词请求。持续跟随任务 MUST 只有开始、首次到达跟随距离与终止三个节点。节点选择 MUST 只依赖计划与任务事实，MUST NOT 依赖模型输出或非确定状态。

#### Scenario: 八次预算上限

- **GIVEN** 一个包含十二个步骤的普通任务全部成功完成
- **WHEN** 任务从 Running 推进到 Completed
- **THEN** 台词请求次数 MUST 恰好为一加可触发的进展节点数加终态一次（末个选中步骤的完成迁移产出 `TaskCompleted` 而非 `TaskProgress`，其完成表达折入终止节点；十二步任务为一加五加一即七次）且不超过八次，进展节点的选中步骤集合 MUST 与按计划长度确定性均匀选择的集合一致

#### Scenario: 终态节点覆盖四种终态

- **GIVEN** 四个同类任务分别以 `Completed`、`Failed`、`TimedOut` 与 `Stopped` 终结
- **WHEN** 各任务到达终态
- **THEN** 每个任务 MUST 恰好发起一次终止节点台词请求，`Stopped` 终止 MUST NOT 被排除

#### Scenario: 持续跟随只有三个节点

- **GIVEN** 一个持续跟随任务成功开始、首次到达跟随距离并被停止指令终结
- **WHEN** 任务推进
- **THEN** 台词节点 MUST 恰好为开始、首次到达与终止三个，期间步骤进度 MUST NOT 产生台词请求

### Requirement: 并发受限且失败只跳过台词

Dialogue 请求 MUST 与 Planner 共享全服务端最多四个模型请求并发槽：Planner 等待槽位，Dialogue 取不到槽位 MUST 立即跳过该节点且 MUST NOT 排队。每个伙伴 MUST 最多一个在途 Dialogue 请求；新节点到来时仍有在途请求 MUST 跳过新台词。结果 MUST 经有界 channel 只在权威 tick 边界应用并携带任务与节点身份；任务或节点已过时 MUST 被丢弃。单次请求 MUST 使用 30 秒超时与 context 取消且 MUST NOT 自动重试；HTTP 失败、非 2xx、超时、超限或解码错误 MUST 只跳过该台词并记录 debug 级结构化原因，MUST NOT 改变任务状态、FIFO 或任何世界事实；事实事件 MUST 始终携带服务器产生的稳定原因，模型文本 MUST NOT 替换或隐藏事实。

#### Scenario: 无可用槽位即跳过不排队

- **GIVEN** 四个模型并发槽全部被 Planner 请求占用且一个台词节点到来
- **WHEN** Dialogue worker 尝试发起请求
- **THEN** 该节点 MUST 被跳过，MUST NOT 入队等待，对应任务 MUST 不受影响继续推进

#### Scenario: 在途请求存在时新节点被跳过

- **GIVEN** 伙伴 `阿木` 的一条开始节点台词请求在途且一个步骤完成节点到来
- **WHEN** 服务器在 tick 边界评估该节点
- **THEN** 新节点 MUST 被跳过，在途请求 MUST NOT 被取消或替换，任务 MUST 正常推进

#### Scenario: 过时台词被丢弃

- **GIVEN** 一条台词请求在途期间其任务已进入终态且终态台词已发出
- **WHEN** 过时结果到达 tick 边界
- **THEN** 该结果 MUST 被丢弃，MUST NOT 广播第二条同节点台词

#### Scenario: 模型失败不影响任务状态

- **GIVEN** 一个假模型服务对台词请求持续返回 5xx
- **WHEN** 伙伴完成一个完整任务
- **THEN** 任务状态机、事实 ChatEvent 序列与 FIFO 推进 MUST 与无台词系统完全一致，仅缺少台词事件

#### Scenario: 慢台词不阻塞权威 tick

- **GIVEN** 四个伙伴各有在途台词请求且模型服务挂起
- **WHEN** 权威模拟连续推进多个 tick
- **THEN** 每个 tick MUST 按既有节拍完成，玩家命令与世界模拟 MUST 不受影响

### Requirement: 台词与摘要响应严格解码

Dialogue 响应正文 MUST 用 `json.Decoder` 严格解码为单一 JSON object：MUST 拒绝未知字段、拒绝尾随数据，并在分配前限制正文为 64 KiB。非终态响应 MUST 只包含 `line` 字段；终态响应 MUST 包含 `line` 与 `summary` 两个字段。`line` MUST 是不超过 256 bytes 的有效 UTF-8 且不含 NUL 或 Unicode control；`summary`（仅终态）MUST 不超过 2,048 bytes 的有效 UTF-8。任何解码或校验失败 MUST 只跳过该台词；终态响应失败 MUST 保留旧摘要。解码后的文本 MUST 复制为服务端拥有的值，MUST NOT 保留对响应缓冲的引用。

#### Scenario: 严格解码拒绝未知字段与尾随数据

- **GIVEN** 三份响应正文分别包含未知字段、JSON object 结束后的尾随数据与超过 64 KiB 的正文
- **WHEN** Dialogue 解码响应
- **THEN** 三者 MUST 全部只导致台词跳过，MUST NOT 产生部分应用的台词或摘要

#### Scenario: 超长台词被拒绝

- **GIVEN** 一份响应的 `line` 为 257 bytes 或含 Unicode control
- **WHEN** Dialogue 校验响应
- **THEN** 该台词 MUST 被跳过，MUST NOT 广播截断或清洗后的文本

#### Scenario: 终态失败保留旧摘要

- **GIVEN** 一个伙伴已有旧摘要且终态台词请求因超时失败
- **WHEN** 请求以超时结束
- **THEN** 该伙伴的摘要 MUST 保持旧值不变，终态事实事件 MUST 照常广播

### Requirement: 终态摘要持久且只喂 Dialogue

最近对话摘要 SHALL 只由终态 Dialogue 响应的 `summary` 字段更新，每伙伴一份、不超过 2,048 bytes，MUST NOT 额外发起摘要专用模型请求。完整玩家聊天与逐条台词 MUST NOT 落盘。摘要 MUST 只作为后续 Dialogue 请求的输入，MUST NOT 进入 Planner、事实事件或任何世界行为。摘要更新 MUST 标记 AI 存档 dirty 并随既有保存纪律落盘；inactive 记录 MUST NOT 保存摘要（重新激活后从空摘要开始）。

#### Scenario: 摘要跨重启进入台词输入

- **GIVEN** 一个伙伴的终态台词写入了新摘要并已落盘
- **WHEN** 服务端重启后该伙伴的下一次台词请求发起
- **THEN** 请求输入 MUST 携带恢复的摘要且不超过 2,048 bytes

#### Scenario: 摘要绝不进入规划

- **GIVEN** 一个伙伴持有非空摘要且新任务进入 Planning
- **WHEN** Planner 构造快照与请求
- **THEN** 二者 MUST 不包含摘要文本或其任何子串特征

