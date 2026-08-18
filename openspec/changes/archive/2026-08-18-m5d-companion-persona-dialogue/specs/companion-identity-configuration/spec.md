# companion-identity-configuration Specification

## MODIFIED Requirements

### Requirement: AI 伙伴配置可选且数量有界

配置 schema SHALL 保持 v1，并 MAY 包含可选的 `ai` 组与 `ai.companions`。缺少 `ai`、`ai` 为 `null`、缺少 `companions` 或伙伴列表为空时，AI MUST 关闭，且 MUST NOT 要求 endpoint、model、key 或 timeout；非空列表 MUST 包含 `1..4` 个有效定义。M5B 起 `ai` 组 SHALL 识别 `endpoint`、`model`、`apiKeyEnv` 与 `taskTimeoutMinutes`：非空伙伴配置缺少 `endpoint` 或 `model`，或远程 `https` endpoint 缺少 `apiKeyEnv` 或对应环境变量为空时，内置服务端与专用服务端 MUST 启动失败。`taskTimeoutMinutes` MUST 是 `1..60` 的整数，缺省为 10。M5D 起 `ai.companions[]` SHALL 识别可选 `persona` 字段，其有界性、外部文件读取与用途约束由 `companion-persona` 规格定义。尚未交付的其他字段 MUST 按既有未知字段纪律告警后忽略。

#### Scenario: 旧配置保持 AI 关闭

- **GIVEN** 一份有效的 config v1 文件没有 `ai.companions` 或其列表为空
- **WHEN** 内置服务端或专用服务端读取该配置
- **THEN** 服务端 MUST 保持 AI 关闭，不创建伙伴，也不得要求 endpoint、model、key、timeout 或 persona

#### Scenario: 超过四个定义被拒绝

- **GIVEN** 一份 config v1 文件包含五个分别有效的伙伴定义
- **WHEN** 服务端验证配置
- **THEN** 启动 MUST 失败，且不得只激活前四个伙伴

#### Scenario: 缺模型配置的伙伴被拒绝启动

- **GIVEN** 一份配置包含两个有效伙伴定义但 `ai` 组缺少 `model`
- **WHEN** 内置或专用服务端启动
- **THEN** 启动 MUST 失败并给出可定位的错误，MUST NOT 以关闭 AI 或移除伙伴的方式继续运行

#### Scenario: 任务时长边界生效

- **GIVEN** `taskTimeoutMinutes` 分别为 0、1、60 与 61
- **WHEN** 服务端验证配置
- **THEN** 0 与 61 MUST 被拒绝，1 与 60 MUST 被接受，缺省值 MUST 为 10

#### Scenario: 后续字段不提前启用

- **GIVEN** 一个伙伴定义或 `ai` 组包含 persona 之外的其他未交付字段
- **WHEN** 读取该配置
- **THEN** 系统 MUST 对精确字段路径告警并忽略这些字段，且有效结果 MUST 不变；`persona` 字段 MUST 按 `companion-persona` 规则解析而不再被忽略
