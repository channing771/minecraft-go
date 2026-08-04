## Why

M4E 已在服务端定义并执行石砖、熔炉和铁块三条固定配方，但图形背包仍只展示石砖入口，导致玩家无法通过现有界面完成熔炉和铁块资源链。当前项目说明还分别停留在 M3C/M4C，需要与已交付的 M4E 基线同步，避免后续规划建立在过期事实之上。

## What Changes

- 图形背包同时展示 `4 石头 → 4 石砖`、`8 石头 → 1 熔炉`和 `9 铁锭 → 1 铁块`三条固定配方及各自基于最后确认背包的可合成状态。
- 点击可用配方时继续只发送已有 sequence 与稳定 recipe ID，不预测修改客户端物品镜像；不可用配方不发送请求。
- 修正 HUD 固定上传缓冲中 quad 与 glyph 区域的重叠，并以自动测试锁定非重叠边界。
- 将 `AGENTS.md`、`CLAUDE.md` 和 `openspec/config.yaml` 的当前实现说明统一为 M4E。
- 更新 `README.md` 的操作方式和当前限制，使其准确描述三条可见配方入口。
- 不增加新配方、动态配方注册器、配方选择状态、批量合成或合成网格。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `authoritative-crafting`：补录现有 recipe ID `2`、`3` 的稳定服务端语义，并保持未知配方稳定拒绝。
- `authoritative-inventory`：背包界面从单一石砖入口改为同时展示并操作全部三条现有固定配方。

## Impact

- 代码：`internal/render/hotbar.go`、`internal/render/hotbar_test.go`、`cmd/mcgo/app_test.go`；现有 `cmd/mcgo` 点击路径、`internal/core` 配方、`internal/network` 消息和服务端权威执行逻辑继续复用。
- 文档：`AGENTS.md`、`CLAUDE.md`、`README.md`、`openspec/config.yaml`。
- 兼容性：线上协议保持 v7，玩家 schema 保持 v3，区块 schema 保持 v4，无迁移或回退要求变化。
- 并发与性能：不改变 goroutine 所有权或权威 tick；固定 UI 实例仍受现有容量约束，benchmark scenario 保持 v9，不新增性能基线迁移。
- 依赖：不新增包或第三方依赖，不改变内部包依赖方向。
