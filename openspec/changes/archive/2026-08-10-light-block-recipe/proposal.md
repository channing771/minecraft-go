## Why

发光方块已经可以放置并用石镐或铁镐挖回，却没有正常生存流程中的获取入口。追加一条固定合成配方可以闭合玻璃到发光方块的最小材料循环，同时复用既有服务端权威合成与客户端固定配方界面。

## What Changes

- 将服务端权威固定配方从七条扩为八条，固定 recipe ID `8` 为消耗 4 个玻璃并产出 4 个发光方块；ID `1..7` 与合成失败的原子语义保持不变。
- 让正常生存流程可以经固定配方获取发光方块，并保持其既有方块光、放置、采掘、掉落与客户端派生语义。
- 将普通背包固定配方单列从七行扩为八行，在 640×360 framebuffer 中整体缩放，并让按钮命中几何与绘制几何共源。
- 非目标：不增加分页、滚动、动态配方目录或新 UI 框架，不改变发光方块的照明算法，也不增加新的美术资源。
- 兼容性：协议保持 v15、玩家 schema 保持 v6、区块 schema 保持 v8、世界 metadata 保持 v2；不增加或修改 wire 字段，不需要迁移。服务端仍是唯一权威，Memory 与 TCP 继续复用相同合成语义；固定容量调整后仍须保持预热后零分配。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `authoritative-crafting`：固定配方增加 recipe ID `8`，并把未知 ID 上界调整为大于 `8`。
- `static-block-light`：发光方块新增由 4 个玻璃合成 4 个发光方块的正常获取入口，其余静态方块光契约不变。
- `voxel-visual-presentation`：固定配方区域扩为八行，并在 640×360 下保持绘制、缩放与命中一致。

## Impact

- 预计影响 `internal/core` 的固定配方与测试、`internal/render` 的 HUD 固定容量和几何测试，以及 `cmd/mcgo` 的无窗口合成抓帧夹具。
- OpenSpec 将修改 `authoritative-crafting`、`static-block-light` 与 `voxel-visual-presentation` 三项行为契约；唯一允许变化的视觉 golden 是 `cmd/mcgo/testdata/golden/inventory-crafting.png`。
- 不新增依赖，不改变跨 goroutine 消息所有权、存档格式、网络协议或并发边界。
