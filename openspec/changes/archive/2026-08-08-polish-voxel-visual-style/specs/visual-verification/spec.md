## ADDED Requirements

### Requirement: 视觉基线覆盖统一方块与 HUD 风格

系统 SHALL 通过既有无窗口固定场景记录并比对优化后的方块材质与 HUD 呈现。地形场景 MUST 覆盖程序化方块材质，HUD 场景 MUST 覆盖快捷栏、真实方块缩略图、数量阴影、耐久状态与左下角无背景爱心栏，并 MUST 以独立场景覆盖打开的背包与合成区域；更新基线时 MUST 继续执行既有显式更新与双阈值规则。

#### Scenario: 地形与 HUD 风格变化产生可审查基线

- **WHEN** 显式更新本变更影响的视觉基线
- **THEN** `terrain-noon` MUST 包含当前程序化地形材质
- **AND** `hud-hotbar-health` MUST 包含当前快捷栏、真实方块缩略图、紧凑两位间距的数量数字、耐久状态与左下角无背景爱心栏
- **AND** `inventory-crafting` MUST 包含打开的 3×9 背包区、1×9 快捷栏区和固定合成区域
- **AND** 三张图 MUST 由无窗口完整渲染链路产出

#### Scenario: 远端玩家场景只继承地形背景变化

- **GIVEN** 远端玩家与名牌的渲染逻辑没有变化，但场景共享的程序化地形背景发生变化
- **WHEN** 更新本变更影响的视觉基线
- **THEN** `avatar-nametag` MUST 继承当前地形背景
- **AND** 远端玩家轮廓、颜色与名牌文字 MUST 保持既有可观察语义
