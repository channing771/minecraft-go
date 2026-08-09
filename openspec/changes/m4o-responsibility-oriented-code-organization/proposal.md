## Why

随着权威模拟、持久化、网络和渲染能力增长，部分 Go 文件已经混合多个职责，增加了理解、评审和回退成本。本变更对 `cmd/` 与 `internal/` 的全部 Go 文件建立可追踪的职责审计，并只整理已经确认的职责边界。

## What Changes

- 审计基线中的全部 386 个 Go 文件，并将每个文件判定为保留、同包拆分、提取新包或删除。
- 默认在原包内按职责拆分生产代码与测试，保持现有调用、测试入口和资源所有权。
- 将完整 HUD 职责提取到唯一新包 `internal/render/hud`，并保持 `internal/render` 不反向依赖 HUD。
- 调整依赖单一源文件位置的架构守卫，使其扫描完整职责文件集合。
- 保持 CLI、游戏行为、协议、存档、渲染、并发和性能契约完全不变。

非目标：

- 不开发新功能，也不修复与代码组织无关的行为问题。
- 不变更协议、存档 schema、fixture、视觉 golden、benchmark scenario 或性能基线。
- 不新增生产文件或测试文件的行数门禁。
- 不新增 `utils`、`common` 等通用工具包、工厂、单实现接口或兼容 wrapper。
- 除 `internal/render/hud` 外不新增其他包。

## Capabilities

### New Capabilities

- `repository-code-organization`: 约束全仓职责审计、纯结构重组后的行为保真，以及架构守卫对职责文件族的覆盖。

### Modified Capabilities

无。

## Impact

- 受影响区域：`cmd/`、`internal/`、`internal/archcheck` 与本 change 的规划产物。
- API 与依赖：现有导出 API 保持不变；仅新增 `internal/render/hud` 的精确单向依赖。
- 兼容性：协议 v14、区块 schema v7、玩家 schema v5、世界 metadata v2、benchmark scenario v15 及其 fixture/基线均不迁移。
- 并发与性能：goroutine、channel、锁、buffer、资源释放顺序、workload 和阈值保持不变。
