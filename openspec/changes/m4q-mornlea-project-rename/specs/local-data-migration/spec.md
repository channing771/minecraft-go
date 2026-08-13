## Purpose

让客户端与专用服务端在采用 Mornlea 新默认本机目录时安全继承既有配置和玩家身份，并在失败或并发启动时避免覆盖、回退和身份丢失。

## ADDED Requirements

### Requirement: 默认本机数据安全迁移到 Mornlea
系统 MUST 对 config 与 profile 独立使用 `os.UserConfigDir()/mornlea` 作为新默认目录，并仅在新文件缺失时读取、校验和规范化复制旧 `minecraft-go` 文件。

#### Scenario: 新文件优先
- **GIVEN** 新默认文件存在
- **WHEN** 启动客户端或专用服务端
- **THEN** MUST 只读取并校验新文件
- **AND** 新文件失败 MUST 终止且不得回退旧文件

#### Scenario: 仅旧文件存在
- **GIVEN** 新默认文件缺失且旧文件有效
- **WHEN** 加载默认 config 或 profile
- **THEN** MUST 以 0700 父目录、0600 文件和原子 no-clobber 发布规范化副本
- **AND** MUST 从发布后的新文件返回结果并保持旧文件逐字节不变
- **AND** 仅实际发布者 MUST 记录一条含 `legacy_path` 与 `current_path` 的结构化迁移成功日志

#### Scenario: 旧文件非法
- **GIVEN** 新默认文件缺失且旧文件非法
- **WHEN** 加载默认 config 或 profile
- **THEN** MUST 返回指向旧路径的错误
- **AND** MUST NOT 创建新默认文件或生成新 PlayerID

#### Scenario: 并发发布已有赢家
- **GIVEN** 两个进程同时迁移或首次创建同一文件
- **WHEN** 其中一个先发布目标
- **THEN** 另一个 MUST 不覆盖目标并读取校验赢家
- **AND** 所有 profile 调用方 MUST 返回同一 PlayerID
- **AND** requested name 相同或均未提供时，所有调用方 MUST 返回同一份完整 Profile
- **AND** 不同 requested name 仍沿用既有 replace-on-save 语义，不承诺并发显示名的返回顺序
- **AND** 发布 loser MUST NOT 记录迁移成功日志

#### Scenario: 两边都不存在
- **WHEN** config 新旧文件都不存在
- **THEN** MUST 返回编译默认值且不创建文件
- **WHEN** profile 新旧文件都不存在
- **THEN** MUST 原子创建一份 UUIDv4 profile，所有并发调用方读取同一 PlayerID

#### Scenario: 显式配置跳过迁移
- **GIVEN** 用户传入 `--config PATH`
- **WHEN** 加载配置
- **THEN** MUST 只读取显式路径并完全跳过默认目录迁移
