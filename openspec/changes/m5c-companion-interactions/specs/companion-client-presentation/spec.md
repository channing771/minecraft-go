# companion-client-presentation Specification

## MODIFIED Requirements

### Requirement: 聊天输入和事件显示固定有界

客户端 SHALL 只发送 `@伙伴名 指令` 所需的聊天输入：Enter 打开/发送、Esc 取消、Backspace 删除一个 Unicode rune。编辑缓冲 MUST 最多接受 1024 UTF-8 bytes；输入 overflow MUST sticky 到取消或重新打开，且不得发送截断前缀。客户端 MUST 保存最近 32 条严格递增 EventID 的 ChatEvent，并在 HUD 显示最近最多 6 条、每条最多 32 rune 的稳定事实文本和一条输入；任务生命周期事件（`TaskStarted`、`TaskProgress`、`TaskCompleted`、`TaskFailed`、`TaskTimedOut`、`TaskStopped`）MUST 以服务器事实生成稳定中文事实行，MUST NOT 显示模型生成的自由文本。聊天打开时 MUST 抑制移动、采掘、放置、背包和快捷栏动作。

#### Scenario: 任务事件生成稳定事实行

- **GIVEN** 环内已有一条 Accepted 与一条 `TaskCompleted` 事件
- **WHEN** 客户端渲染聊天 HUD
- **THEN** 每条事件 MUST 各占一行稳定事实文本，行数与每行 rune 上限 MUST 继续遵守既有 6 行 ×32 rune 界限

#### Scenario: 停止事件生成稳定事实行

- **GIVEN** 环内有一条 `TaskStopped` 事件与一条 `NotFollowing` 拒绝事件
- **WHEN** 客户端渲染聊天 HUD
- **THEN** 两条 MUST 各生成一行稳定中文事实文本（含伙伴名与指令摘要），并遵守 6 行 ×32 rune 界限

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
