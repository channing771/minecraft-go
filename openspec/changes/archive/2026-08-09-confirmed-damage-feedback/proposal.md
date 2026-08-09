## Why

M4M 已有服务端权威生命值与确认生命 HUD，但确认生命值下降时没有即时画面反馈，玩家在移动中难以察觉刚刚受伤。该变更以一个客户端本地呈现闭环补足反馈，同时保持服务端权威、协议和存档不变。

## What Changes

- 客户端只比较 `Predictor.Health()` 的确认值；首次同步、回复、满血重生和 not-ready 不触发。
- 确认生命值下降时显示 `180ms` 红色边缘遮罩，连续下降重置计时。
- 新增一个固定资源、单全屏三角形的 alpha-blend renderer；零强度不提交 pass。
- 遮罩绘制在世界/name tag 之后、HUD/调试面板之前。
- README 更新当前能力；不更新共享 capture golden。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `authoritative-health`：允许专用受伤反馈 pipeline，并增加仅由确认生命值下降触发的时序、层级和 reset 契约。

## Impact

影响 `cmd/mcgo`、`internal/render` 与 README。无协议、存档、模拟、配置、并发或第三方依赖变化；非激活热路径只增加 O(1) 状态更新和一次分支，不新增 pass、GPU 写入或堆分配。M4N 唯一可能重叠的是 README 当前状态句，生产代码文件不重叠。
