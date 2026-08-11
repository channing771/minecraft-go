## Context

见 `proposal.md` 的 Why。当前固定配方由服务端权威定义，客户端只发送 recipe ID；`Inventory.Craft` 已负责在完整物品状态副本上原子扣料和放入产物，Memory 与 TCP 共用 `network.CraftRecipe` 契约。普通背包以固定数组绘制七行配方，并通过统一缩放适配 640×360；本变更只把既有闭环机械扩为八条。

## Goals / Non-Goals

**Goals:**

- 在既有固定配方 switch 末尾追加 recipe ID `8` 的 `4 Glass -> 4 LightBlock`，保持 ID `1..7`、服务端权威、原子失败和传输语义不变。
- 把普通背包固定配方单列扩为八行，并把固定容量精确更新为 `recipeQuads=73`、`recipeGlyphs=20`、`openHUDHeight=670`。
- 保持协议 v15、玩家 schema v6、区块 schema v8、世界 metadata v2 与全部 wire 字段不变。

**Non-Goals:**

- 不引入数据驱动配方目录、注册表、分页、滚动、自适应布局或新 UI 框架。
- 不修改方块光传播、放置、采掘、掉落、存档或网络消息格式。
- 不修改除 `inventory-crafting` 之外的视觉场景或 golden。

## Decisions

### 只追加既有固定配方 switch case

在 `internal/core` 的既有固定配方查询中追加 ID `8`，输入固定为 4 个玻璃，输出固定为 4 个发光方块。`Inventory.Craft` 继续消费该查询结果并负责原子提交；`network.CraftRecipe` 继续只传 recipe ID，客户端不得声明输入或产物。这样数据所有权仍在服务端，Memory/TCP 继续穿过同一消息与模拟边界，也没有新的跨 goroutine 可变数据。

否决单独的发光方块合成路径，因为它会绕过共享的原子失败与传输语义；否决动态配方注册表，因为固定八条配方不需要新抽象或依赖。

### 机械扩展八行固定 HUD 与固定容量

在 `internal/render` 的固定 recipe ID 数组末尾追加 ID `8`，面板、绘制与命中继续遍历同一数组。固定容量同步设为 `recipeQuads=73`、`recipeGlyphs=20`、`openHUDHeight=670`；配方按钮命中矩形与绘制矩形继续由同一组缩放后几何产生。固定数组和编译期容量保持热路径工作量有界，并要求预热后零分配。

否决分页和滚动，因为 640×360 可以通过既有整体缩放容纳八行；否决从数组长度隐式推导容量，因为本 HUD 的容量是需要测试冻结的显式性能契约。

### 只更新既有无窗口合成场景

`cmd/mcgo` 的 `inventory-crafting` fixture 提供使 ID `8` 可合成的玻璃，并让无窗口抓帧覆盖第八行。唯一允许更新的视觉 golden 是 `cmd/mcgo/testdata/golden/inventory-crafting.png`；其他场景、相机和比较阈值保持不变。

## Risks / Trade-offs

- [风险] 第八行可能在 640×360 下被裁切，或命中区域与绘制错位 → 用应用层点击、渲染几何、边界与无窗口 golden 共同验证，并确保两种几何共源。
- [风险] 固定容量少计导致真实 overflow 或稳定态分配 → 以 `recipeQuads=73`、`recipeGlyphs=20` 和最坏布局测试冻结容量，保留真实 overflow 门禁。
- [风险] recipe ID 或数量错误会破坏稳定协议语义 → 表驱动测试冻结 ID `1..8`、`4 Glass -> 4 LightBlock`、未知 ID 边界和失败原子性。
- [取舍] 单列八行进一步缩小打开背包时的整体尺度 → 保持现有交互模型和最小实现，640×360 可读性由视觉检查确认。

## Migration Plan

不需要迁移。发布只增加既有 recipe ID 值域内的新稳定语义并调整客户端固定 HUD；协议、wire、玩家/区块存档和 metadata 版本全部不变。回退时撤销 recipe ID `8`、八行 HUD 与对应 `inventory-crafting` golden；既有存档和网络数据仍可直接使用。
