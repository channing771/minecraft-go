# companion-identity-configuration Specification

## Purpose

为每个世界提供数量有界、名称无歧义且独立于玩家身份的静态伙伴定义，使服务端、存档、协议和客户端能够稳定引用同一个伙伴。

## Requirements

### Requirement: AI 伙伴配置可选且数量有界

配置 schema SHALL 保持 v1，并 MAY 包含可选的 `ai.companions`。缺少 `ai`、`ai` 为 `null`、缺少 `companions` 或伙伴列表为空时，AI MUST 关闭；非空列表 MUST 包含 `1..4` 个有效定义。M5A MUST 只识别每个定义的 `id` 与 `name`，未来 AI 字段 MUST 按既有未知字段纪律告警后忽略，不得成为 M5A 启动条件。

#### Scenario: 旧配置保持 AI 关闭

- **GIVEN** 一份有效的 config v1 文件没有 `ai.companions` 或其列表为空
- **WHEN** 内置服务端或专用服务端读取该配置
- **THEN** 服务端 MUST 保持 AI 关闭，不创建伙伴，也不得要求 endpoint、model、key、timeout 或 persona

#### Scenario: 超过四个定义被拒绝

- **GIVEN** 一份 config v1 文件包含五个分别有效的伙伴定义
- **WHEN** 服务端验证配置
- **THEN** 启动 MUST 失败，且不得只激活前四个伙伴

#### Scenario: 后续字段不提前启用

- **GIVEN** 一个伙伴定义除 `id` 与 `name` 外还包含未来字段，或 `ai` 包含未来模型字段
- **WHEN** M5A 读取该配置
- **THEN** 系统 MUST 对精确字段路径告警并忽略这些字段，且有效的 `id/name` 结果 MUST 不变

### Requirement: CompanionID 是独立规范身份

每个伙伴 SHALL 使用独立的 16-byte `CompanionID`。文本形式 MUST 是 canonical UUIDv4；零值、非 v4、非 canonical 或重复 ID MUST 被拒绝。`CompanionID` MUST NOT 被当作 `PlayerID`、`SessionID` 或网络会话身份。

#### Scenario: canonical UUIDv4 往返稳定

- **GIVEN** 一个 canonical UUIDv4 伙伴 ID
- **WHEN** 系统解析、序列化并再次解析该 ID
- **THEN** 两次得到的 16-byte `CompanionID` MUST 相同，输出文本 MUST 与 canonical 输入相同

#### Scenario: 重复或非法身份被拒绝

- **GIVEN** 配置包含重复 ID、零 ID、非 UUIDv4 或非 canonical UUID 文本之一
- **WHEN** 系统验证伙伴定义
- **THEN** 整组定义 MUST 被拒绝，且不得创建部分伙伴

### Requirement: 伙伴名称规范且大小写敏感唯一

伙伴名称 MUST 是 canonical、有效 UTF-8，包含 `1..32` 个 Unicode 字符且不超过 128 UTF-8 bytes，并 MUST NOT 含 Unicode control 或 Unicode whitespace。唯一性 SHALL 区分大小写，因此 `A` 与 `a` MAY 同时存在；完全相同的名称 MUST 被拒绝。

#### Scenario: 大小写不同的名称可共存

- **GIVEN** 两个 ID 不同且名称分别为 `A` 与 `a` 的伙伴定义
- **WHEN** 系统验证整组定义
- **THEN** 两个定义 MUST 同时有效，后续寻址 MUST 按精确大小写区分它们

#### Scenario: 非 canonical 或含空白名称被拒绝

- **GIVEN** 名称包含普通空格、其他 Unicode whitespace、control 字符或非 canonical Unicode 表示之一
- **WHEN** 系统验证伙伴定义
- **THEN** 整组定义 MUST 被拒绝，且不得把名称静默改写后接受

#### Scenario: 字符和字节上限同时生效

- **GIVEN** 名称超过 32 个 Unicode 字符或超过 128 UTF-8 bytes
- **WHEN** 系统验证伙伴定义
- **THEN** 该名称 MUST 被拒绝，即使另一个上限尚未超过
