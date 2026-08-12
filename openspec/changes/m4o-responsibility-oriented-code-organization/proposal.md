## Why

随着权威模拟、持久化、网络和渲染能力增长，部分 Go 文件已经混合多个职责，增加了理解、评审和回退成本。Task 2–19 已在 `96c4aae` 的 386 个 Go 文件上完成职责整理；其后 `origin/main` 前进到 `37cdb3e` 并引入 13 个同文件冲突，因此还需要一次只迁移上游增量、保持既有职责边界的主线同步。

## What Changes

- 保留 Task 2–19 基于 `96c4aae` 的 386 文件审计作为历史证据；Task 20 以 `37cdb3e` 的全部 412 个 Go 文件为新基线，并将每个文件判定为保留、同包拆分、提取新包或删除。
- 默认在原包内按职责拆分生产代码与测试，保持现有调用、测试入口和资源所有权。
- 将完整 HUD 职责提取到唯一新包 `internal/render/hud`，并保持 `internal/render` 不反向依赖 HUD。
- 调整依赖单一源文件位置的架构守卫，使其扫描完整职责文件集合。
- Task 20 合并 `origin/main` 后，把协议 v15、区块 schema v8、玩家 schema v6、已归档 M4N 与 common materials、damage/target、material processing、natural generation/oak、light recipe、container-Y 等上游能力迁移到既有职责文件；这些能力不归因于 M4O，也不得回退或通过复活旧大文件实现。
- 保持相对 `37cdb3e` 的 CLI、游戏行为、协议、存档、渲染、并发、测试入口和固定 artifact 契约不变。
- 对同一 Apple M2/macOS 环境已在原始 `37cdb3e` 连续复现的 `materials-showcase` 与 `oak-grove` 既有 visual-check 失败，不泛化跳过视觉门禁：Task 20 必须让分支与 detached `37cdb3e` 的 10 个重新 capture PNG 逐字节一致，保留其余 8 场景各自通过 tracked golden，并证明两边仅上述 2 场景的失败摘要完全一致。

非目标：

- 不开发新功能，也不修复与代码组织无关的行为问题。
- 不在 `37cdb3e` 之上另行改变协议、存档 schema、fixture、视觉 golden、benchmark scenario 或性能基线；主线已有 v15/v8/v6 只做无损迁移。
- 不重新设计、复制或归因 `37cdb3e` 已有能力，也不把冲突文件整份恢复为拆分前的大文件。
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
- API 与依赖：新增窄内部 API `render.ItemColor` 作为掉落物与 HUD 共享颜色的唯一实现，并新增 `internal/render/hud` 到既有底层包（含 `internal/render`）的精确单向依赖；`internal/render` 不反向依赖 HUD，外部行为不变。
- 兼容性：Task 2–19 历史审计曾保持协议 v14、区块 schema v7、玩家 schema v5；Task 20 改以 `37cdb3e` 已有的协议 v15、区块 schema v8、玩家 schema v6、世界 metadata v2 与 benchmark scenario v15 为不可回退基线。10 个 tracked 视觉 golden 及 storage/network fixture 必须与主线字节一致；同一 Apple M2/macOS 上分支与 detached `37cdb3e` 的 10 个 capture 输出也必须逐字节一致，且不得修改 golden、阈值或 capture 代码。若上游未改性能 baseline，则继续保持其原字节。
- 并发与性能：goroutine、channel、锁、buffer、资源释放顺序、workload 和阈值数值保持不变；benchmark 与 `perfcheck` 的性能数值只保存记录、不改变退出状态，只有报告结构、身份/provenance、真实 overflow、数据丢失、I/O 错误和非数值命令失败阻断。
