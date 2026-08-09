## MODIFIED Requirements

### Requirement: 图形客户端显示权威生命值

图形客户端 SHALL 显示服务端确认的生命值，客户端 MUST NOT 预测伤害或回复。客户端已有上一份确认生命值基线且新确认值更低时，系统 SHALL 立即显示红色屏幕边缘反馈；反馈 MUST 持续 `180ms` 并按剩余时间线性淡出，连续确认下降 MUST 重新开始完整持续时间。首次确认值、生命值不变或增加、Predictor not-ready MUST NOT 开始新反馈；not-ready 与会话清理 MUST 清除旧反馈。反馈 MUST 覆盖世界画面但位于生命值、背包、容器 HUD 与调试面板下方。

#### Scenario: 显示服务端确认值

- **GIVEN** 客户端收到生命值为 12 的权威玩家状态
- **WHEN** 客户端绘制 HUD
- **THEN** HUD MUST 显示 12
- **AND** 在收到该状态之前 MUST NOT 显示预测值

#### Scenario: 首次确认只建立基线

- **GIVEN** Predictor 尚未 ready
- **WHEN** 客户端首次收到生命值为 12 的 ready 权威状态
- **THEN** HUD MUST 显示 12
- **AND** 受伤反馈 MUST NOT 出现

#### Scenario: 确认生命值下降立即触发并线性淡出

- **GIVEN** 客户端上一份确认生命值为 12
- **WHEN** 新确认生命值变为 7
- **THEN** 当前帧屏幕边缘反馈透明度 MUST 为峰值
- **AND** 经过 90ms 且没有新伤害时，屏幕边缘反馈透明度 MUST 为峰值的一半
- **AND** 经过完整 180ms 后反馈 MUST 消失

#### Scenario: 连续确认伤害重置计时

- **GIVEN** 一次确认伤害的反馈已淡出 90ms
- **WHEN** 确认生命值再次下降
- **THEN** 当前帧屏幕边缘反馈透明度 MUST 恢复峰值
- **AND** MUST 再保持一个完整 180ms 的淡出周期

#### Scenario: 回复与重生不误触发

- **GIVEN** 客户端已有一份确认生命值基线
- **WHEN** 新确认生命值不变或增加
- **THEN** 系统 MUST NOT 开始新的受伤反馈

#### Scenario: not-ready 清除旧反馈

- **GIVEN** 受伤反馈仍在显示
- **WHEN** Predictor 变为 not-ready 或客户端会话被清理
- **THEN** 反馈 MUST 立即消失
- **AND** 下一次 ready 的首份生命值 MUST 只建立新基线

#### Scenario: 反馈不染色 HUD

- **GIVEN** 受伤反馈、生命 HUD、容器 HUD 与调试面板同时可见
- **WHEN** 客户端绘制一帧
- **THEN** 世界画面边缘 MUST 显示红色反馈
- **AND** 生命 HUD、容器 HUD 与调试面板 MUST 保持原色且清晰可读

#### Scenario: 自动验证不打开窗口

- **GIVEN** CI 或开发者运行生命值、反馈或渲染测试
- **WHEN** 自动验证执行
- **THEN** 测试 MUST 可在不启动或聚焦交互式游戏窗口的情况下完成
