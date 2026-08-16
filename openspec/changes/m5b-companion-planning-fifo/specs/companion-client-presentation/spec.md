# companion-client-presentation Specification

## MODIFIED Requirements

### Requirement: 客户端伙伴镜像只读且批次原子

客户端 SHALL 维护独立于玩家且最多四项的伙伴只读镜像，并 MUST 只响应 `CompanionSpawn`、`CompanionStates` 与 `CompanionDespawn`。spawn 前对应脚下区块 snapshot MUST 已发布；状态批次 MUST 全部验证后原子应用。客户端 MUST NOT 预测伙伴移动或产生伙伴世界写入，伙伴位置变化 MUST 只来自已验证的 `CompanionStates` 批次。

#### Scenario: snapshot 后才出现伙伴

- **GIVEN** 一个伙伴进入观察者兴趣，但其脚下区块 snapshot 尚未发送
- **WHEN** 服务端发布该 tick
- **THEN** 客户端 MUST 不收到该伙伴 Spawn；snapshot 发送完成后的首次 Spawn MUST 建立镜像，后续 tick 才应用 state

#### Scenario: 非法批次不部分更新

- **GIVEN** `CompanionStates` 中至少一项未知、重复、过时或批次超过四项
- **WHEN** 客户端验证该消息
- **THEN** 整批 MUST 被拒绝，所有既有伙伴镜像和呈现状态 MUST 保持不变

### Requirement: 聊天输入和事件显示固定有界

客户端 SHALL 只发送 `@伙伴名 指令` 所需的聊天输入：Enter 打开/发送、Esc 取消、Backspace 删除一个 Unicode rune。编辑缓冲 MUST 最多接受 1024 UTF-8 bytes；输入 overflow MUST sticky 到取消或重新打开，且不得发送截断前缀。客户端 MUST 保存最近 32 条严格递增 EventID 的 ChatEvent，并在 HUD 显示最近最多 6 条、每条最多 32 rune 的稳定事实文本和一条输入；任务生命周期事件（`TaskStarted`、`TaskProgress`、`TaskCompleted`、`TaskFailed`、`TaskTimedOut`）MUST 以服务器事实生成稳定中文事实行，MUST NOT 显示模型生成的自由文本。聊天打开时 MUST 抑制移动、采掘、放置、背包和快捷栏动作。

#### Scenario: 任务事件生成稳定事实行

- **GIVEN** 环内已有一条 Accepted 与一条 `TaskCompleted` 事件
- **WHEN** 客户端渲染聊天 HUD
- **THEN** 每条事件 MUST 各占一行稳定事实文本，行数与每行 rune 上限 MUST 继续遵守既有 6 行 ×32 rune 界限

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

## ADDED Requirements

### Requirement: 移动伙伴按远端玩家同机制插值

客户端 SHALL 对移动中的伙伴使用与远端玩家相同的插值机制在状态批次之间平滑呈现位置；插值 MUST 只消费已验证 `CompanionStates` 批次中的权威位置，MUST NOT 外推超过最新权威状态。静止与移动切换 MUST 不产生位置跳变或残影。

#### Scenario: 批次之间按插值呈现

- **GIVEN** 一个伙伴在连续两个状态批次中位置相距数格
- **WHEN** 客户端在两批次之间渲染多个帧
- **THEN** 伙伴呈现位置 MUST 平滑过渡且始终位于两个权威位置之间，MUST NOT 瞬移或越过最新权威位置

#### Scenario: 断线清空插值状态

- **GIVEN** 一个正在插值呈现的移动伙伴
- **WHEN** 会话断开
- **THEN** 该伙伴的镜像与插值状态 MUST 被清除，重连后 MUST 从新 Spawn 的权威位置重新开始
