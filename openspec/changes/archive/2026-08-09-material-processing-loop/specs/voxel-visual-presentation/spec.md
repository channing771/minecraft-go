## MODIFIED Requirements

### Requirement: HUD 在无窗口视觉场景尺寸下保持可读
系统 SHALL 保持 HUD 像素几何与命中几何一致，并 MUST 在 640×360 及更大的 framebuffer 中让九格快捷栏完整落在屏幕内；打开背包时，完整的七行固定合成区域 MUST 通过统一缩放落在 framebuffer 内且不得相互重叠。

#### Scenario: 640×360 关闭 HUD 完整可见
- **WHEN** 在 640×360 framebuffer 绘制快捷栏与爱心栏
- **THEN** 所有栏位、选中边界和左下角无背景爱心 MUST 位于 framebuffer 边界内

#### Scenario: 640×360 打开七行固定合成区域
- **WHEN** 在 640×360 framebuffer 打开背包与固定合成区域
- **THEN** 全部背包栏位、七条配方行与合成按钮 MUST 位于 framebuffer 边界内
- **AND** 命中测试 MUST 与缩放后的绘制矩形一致
