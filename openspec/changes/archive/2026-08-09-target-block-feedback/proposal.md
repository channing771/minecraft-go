## Why

玩家的准星命中方块后，目前没有本地、即时且深度正确的目标反馈，难以确认正在交互的对象。现在已有只读方块镜像、世界空间名牌和无窗口视觉验证链路，可在不改变服务端权威裁决的前提下补齐这项客户端呈现。

## What Changes

- 在普通游戏界面中，基于本地只读镜像和当前相机的六格射线选取可显示的注册非空气方块；未知、未加载、未 ready、界面遮挡、断开或 reset 时立即隐藏。
- 为目标方块呈现受现有深度附件遮挡的细轮廓，并在方块上方复用世界空间 name-tag 显示全部注册方块的中文名。
- 在无窗口视觉场景表末尾增加 `target-block-feedback`，覆盖正常渲染链、轮廓、中文名和遮挡；打开背包的 `inventory-crafting` 基线保持不变。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `voxel-visual-presentation`: 增加本地目标方块轮廓、中文名称、固定容量和渲染成本的可观察契约。
- `visual-verification`: 增加目标方块反馈的末尾无窗口场景与既有基线不变要求。

## Impact

- 影响 `internal/core` 的方块显示名查询、`cmd/mcgo` 的本地目标状态与抓帧场景、`internal/render` 的轮廓和 name-tag 容量，以及 `internal/gfx` 的可选深度比较映射。
- 客户端目标仅用于呈现；服务端采掘、放置和容器打开仍使用权威射线裁决。
- 不修改网络协议、方块或物品 ID、玩家/区块 schema、世界 metadata 或存档内容；无需迁移。
- 轮廓、名牌和捕获路径使用固定容量，预热后不得为稳定目标状态增加堆分配；不新增外部依赖或前台窗口。
