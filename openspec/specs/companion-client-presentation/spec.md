# companion-client-presentation Specification

## Purpose

让客户端只通过服务端 v16 消息观察、插值并呈现具名伙伴与聊天事实，同时复用既有实体和 HUD 管线并在断线时彻底清除陈旧状态。

## Requirements

### Requirement: 客户端伙伴镜像只读且批次原子

客户端 SHALL 维护独立于玩家且最多四项的伙伴只读镜像，并 MUST 只响应 `CompanionSpawn`、`CompanionStates` 与 `CompanionDespawn`。spawn 前对应脚下区块 snapshot MUST 已发布；状态批次 MUST 全部验证后原子应用。客户端 MUST NOT 预测伙伴移动或产生伙伴世界写入。

#### Scenario: snapshot 后才出现伙伴

- **GIVEN** 一个伙伴进入观察者兴趣，但其脚下区块 snapshot 尚未发送
- **WHEN** 服务端发布该 tick
- **THEN** 客户端 MUST 不收到该伙伴 Spawn；snapshot 发送完成后的首次 Spawn MUST 建立镜像，后续 tick 才应用 state

#### Scenario: 非法批次不部分更新

- **GIVEN** `CompanionStates` 中至少一项未知、重复、过时或批次超过四项
- **WHEN** 客户端验证该消息
- **THEN** 整批 MUST 被拒绝，所有既有伙伴镜像和呈现状态 MUST 保持不变

### Requirement: 伙伴复用统一 Avatar 与 NameTag 呈现

客户端 SHALL 在同一 Avatar pass 与同一 NameTag pass 中呈现玩家和伙伴。相同 16 bytes 的玩家、伙伴和目标标签 MUST 仍作为不同对象独立呈现。Avatar MUST 容纳 7 名远端玩家加 4 个伙伴共 11 个 actor；NameTag MUST 容纳这些 actor 加 1 个目标标签共 12 个标签，overflow MUST 在任何帧上传、绘制或部分呈现状态变化前原子失败。既有玩家配色 MUST 保持不变。

#### Scenario: 十一个 actor 单 pass 呈现

- **GIVEN** 客户端同时观察七名远端玩家和四个伙伴
- **WHEN** 渲染一帧
- **THEN** 全部 11 个 actor MUST 在一个 Avatar pass 和一个 NameTag pass 中呈现，伙伴名称 MUST 可见且玩家原有配色 MUST 不变

#### Scenario: 超出固定容量在副作用前失败

- **GIVEN** Avatar 输入有 12 个 actor 或 NameTag 输入有 13 个标签
- **WHEN** 客户端准备呈现该帧
- **THEN** 操作 MUST 返回 overflow 错误，且不得上传或绘制部分帧，也不得留下部分呈现状态变化

### Requirement: 聊天输入和事件显示固定有界

客户端 SHALL 只发送 `@伙伴名 指令` 所需的聊天输入：Enter 打开/发送、Esc 取消、Backspace 删除一个 Unicode rune。编辑缓冲 MUST 最多接受 1024 UTF-8 bytes；输入 overflow MUST sticky 到取消或重新打开，且不得发送截断前缀。客户端 MUST 保存最近 32 条严格递增 EventID 的 ChatEvent，并在 HUD 显示最近最多 6 条、每条最多 32 rune 的稳定事实文本和一条输入。聊天打开时 MUST 抑制移动、采掘、放置、背包和快捷栏动作。

#### Scenario: Unicode 输入不按字节破坏

- **GIVEN** 玩家打开聊天并输入包含中文的合法命令
- **WHEN** 玩家按一次 Backspace 后提交
- **THEN** 输入 MUST 删除一个完整 Unicode rune，剩余文本 MUST 仍为有效 UTF-8 且仅在不超过 1024 bytes 时发送

#### Scenario: overflow 不发送截断前缀

- **GIVEN** 一次粘贴使输入超过 1024 UTF-8 bytes
- **WHEN** 玩家随后 Backspace 并按 Enter
- **THEN** 本次编辑 MUST 仍保持无效且不得发送任何 ChatCommand，直到 Esc 或重新打开重置输入

#### Scenario: 打开聊天抑制游戏动作

- **GIVEN** 聊天输入已打开
- **WHEN** 玩家按移动、采掘、放置、背包或快捷栏按键
- **THEN** 客户端 MUST 只处理聊天相关输入，并 MUST 向权威服务端保持中性移动而不发送这些游戏动作

### Requirement: 断线清空伙伴和聊天呈现

客户端会话关闭或协议错误时 SHALL 清空伙伴镜像、插值状态、ChatEvent 环、格式化行缓存和未发送输入，并恢复一致的光标/鼠标基线。重新连接 MUST NOT 显示上一会话的伙伴或聊天残影。

#### Scenario: 重连从空镜像开始

- **GIVEN** 当前会话已有伙伴、聊天事件和打开但未发送的输入
- **WHEN** endpoint 断开后客户端建立新会话
- **THEN** 新会话 MUST 在收到新 Spawn/Event 前显示零伙伴、零聊天行和关闭的空输入，且首帧视角 MUST 不跳变
