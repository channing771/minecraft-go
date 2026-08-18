# companion-persona Specification

## Purpose
TBD - created by archiving change m5d-companion-persona-dialogue. Update Purpose after archive.
## Requirements
### Requirement: 内联 persona 可选且有界

`ai.companions[].persona` SHALL 是可选的伙伴人设自由文本，MUST 是有效 UTF-8、不超过 4,096 bytes 且不含 NUL。缺省（字段缺失或空串）MUST 等价于空人设：该伙伴的台词照常触发，只是没有风格约束。配置 schema MUST 保持 v1；persona 的存在与否 MUST NOT 影响启动要求（endpoint/model/key 校验不变）。内联 persona 超过 4,096 bytes、含 NUL 或非法 UTF-8 时 MUST 按既有配置宽松纪律 `slog.Warn` 后按空人设处理，MUST NOT 阻止启动。

#### Scenario: 有界内联人设被接受

- **GIVEN** 一份 config v1 文件的两个伙伴分别带 4,096-byte 与 4,097-byte 的 persona 文本
- **WHEN** 服务端加载配置
- **THEN** 第一个 persona MUST 被接受并进入该伙伴的 Dialogue 输入，第二个 MUST 被告警后按空人设处理，启动 MUST 不失败

#### Scenario: 无 persona 伙伴正常工作

- **GIVEN** 一份有效配置的伙伴定义没有 persona 字段，也没有对应外部文件
- **WHEN** 该伙伴执行任务
- **THEN** 台词触发节点与预算 MUST 不变，Dialogue 请求 MUST 携带空人设，MUST NOT 产生任何配置告警

### Requirement: persona 可从约定目录的外部文件读取

服务端 SHALL 在启动加载配置时按约定目录解析外部人设文件：目录为配置文件所在目录下的 `personas/`，文件名为 `<canonical 伙伴名称>.txt`。内联 `persona` 字段存在时 MUST 优先生效；此时若同名外部文件也存在，MUST `slog.Warn` 提示该文件被忽略。内联缺失时 MUST 读取该文件（若存在）；文件不存在 MUST 静默得到空人设。文件不可读、超过 4,096 bytes、非法 UTF-8 或含 NUL 时 MUST `slog.Warn` 后按空人设处理，MUST NOT 阻止启动。外部文件 MUST 只在启动时读取一次，运行期 MUST NOT 热更新；文件名 MUST 由已验证的 canonical 名称构成，MUST NOT 接受路径穿越。

#### Scenario: 外部文件提供人设

- **GIVEN** 配置文件位于 `world/config.json`，伙伴 `阿木` 无内联 persona，且 `world/personas/阿木.txt` 存在且为 1 KiB 合法文本
- **WHEN** 服务端启动加载配置
- **THEN** `阿木` 的 Dialogue 请求 MUST 携带该文件内容作为人设

#### Scenario: 内联优先并告警忽略文件

- **GIVEN** 伙伴 `阿木` 同时配置了内联 persona 与存在的 `personas/阿木.txt`
- **WHEN** 服务端启动加载配置
- **THEN** 内联 persona MUST 生效，日志 MUST 告警外部文件被忽略，启动 MUST 不失败

#### Scenario: 损坏文件按空人设降级

- **GIVEN** `personas/阿木.txt` 为 5 KiB 或含 NUL 字节
- **WHEN** 服务端启动加载配置
- **THEN** 系统 MUST 告警精确文件路径并按空人设继续，启动 MUST 不失败

### Requirement: persona 只进入 Dialogue 输入

人设文本（无论内联或文件来源）MUST 只出现在 Dialogue 请求输入中，MUST NOT 进入 Planner 的观察快照或请求正文，MUST NOT 写入世界存档、日志常规输出或任何性能报告。persona 文本 MUST 视为服主控制的不可信数据处理：服务端 MUST NOT 执行其中出现的代码、URL、工具名或任意函数调用。

#### Scenario: 人设绝不进入规划请求

- **GIVEN** 一个带非空 persona（内联与文件来源各验证一次）的伙伴进入 Planning
- **WHEN** Planner worker 发起规划请求
- **THEN** 请求正文 MUST 不含 persona 文本或其任何子串特征，快照 MUST 不含人设字段

#### Scenario: 人设不落盘不外泄

- **GIVEN** 一个带非空 persona 的服务端运行并安全关服
- **WHEN** 检查 `companions.ai` 与运行日志
- **THEN** 存档 MUST 不含 persona 文本，日志 MUST 只在配置解析告警中引用其路径而不回显全文

