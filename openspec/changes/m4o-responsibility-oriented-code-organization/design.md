## Context

Task 2–19 基于 `96c4aae` 完成了 386 个 Go 文件的职责整理与审计。其后 `origin/main` 前进到 `37cdb3e0b3cd241bad1c3e70e5a25bcc9994c4fa`：该基线有 412 个 Go 文件（155 个生产文件、257 个测试文件），已经包含协议 v15、区块 schema v8、玩家 schema v6、已归档 M4N，以及 common materials、damage/target、material processing、natural generation/oak、light recipe 与 container-Y 等能力。Task 20 只负责把这些上游增量迁入既有职责文件，不把能力归因于 M4O。参见 `proposal.md` 与 `specs/repository-code-organization/spec.md`。

## Goals / Non-Goals

**Goals:**

- 保留 Task 19 基于 `96c4aae` 的 386 文件包级审计作为历史证据；以 `37cdb3e` 的 412 文件重做 Task 20 审计，并让每个文件最终归入保留、同包拆分、提取新包或删除。
- 默认只移动声明；测试与其职责同波次整理，每个波次独立验证、提交和回退。
- 仅把完整 HUD 提取到 `internal/render/hud`，同时让架构守卫覆盖拆分后的职责文件族。
- 解决 13 个已知主线冲突，同时完整保留主线的新行为、fixture、golden 与入口。

**Non-Goals:**

- 不在 `37cdb3e` 之上另行修改算法、CLI、游戏功能、协议/存档/性能契约或并发语义；主线既有 v15/v8/v6 只做无损迁移。
- 不以行数作为门禁，不新增通用包、其他新包、兼容 wrapper、工厂或单实现接口。
- 不重新实现、复制、回退或归因 `37cdb3e` 已有能力；主线已经归档 M4N，Task 20 不复活该 active change。
- 不整份恢复拆分前旧大文件，不新增第二个内部包，不更新 golden/baseline，也不放宽测试。

## Decisions

### Task 2–19 历史审计以包为闭环

以下表格是 `96c4aae` 上已经完成的 Task 19 历史审计。计划点名的文件按 Task 2–18 执行 split/move/delete；同包其余基线文件均先判定为 keep，并由 Task 19 的逐包测试与入口名对比复核。行数只用于发现候选，不决定结论；这些 386 文件数据不作为 Task 20 的当前验收值。

| 包 | 生产 | 测试 | 合计 | 计划结论 |
| --- | ---: | ---: | ---: | --- |
| `cmd/gfxspike` | 1 | 0 | 1 | keep |
| `cmd/mcgo` | 8 | 15 | 23 | Task 13–17 点名文件 split/move，其余 keep |
| `cmd/mcgod` | 1 | 2 | 3 | keep |
| `cmd/perfcheck` | 1 | 1 | 2 | Task 18 split，其余 keep |
| `internal/archcheck` | 0 | 1 | 1 | Task 2 split |
| `internal/assets` | 3 | 3 | 6 | keep |
| `internal/client` | 19 | 20 | 39 | Task 11 点名文件 split，其余 keep |
| `internal/config` | 1 | 1 | 2 | keep |
| `internal/core` | 13 | 15 | 28 | keep |
| `internal/gfx` | 4 | 6 | 10 | Task 3 点名文件 split，其余 keep |
| `internal/gfx/shader` | 1 | 0 | 1 | keep |
| `internal/logging` | 1 | 1 | 2 | keep |
| `internal/mesh` | 4 | 5 | 9 | keep |
| `internal/network` | 13 | 22 | 35 | Task 4 点名文件 split，其余 keep |
| `internal/physics` | 5 | 6 | 11 | keep |
| `internal/profile` | 2 | 2 | 4 | keep |
| `internal/render` | 11 | 17 | 28 | Task 12 split；Task 13 HUD move，并更新 `drop.go`/`drop_test.go` 的共享颜色调用；其余 keep |
| `internal/server` | 16 | 59 | 75 | Task 7–10 点名文件 split，其余 keep |
| `internal/sim` | 14 | 29 | 43 | Task 6 点名文件 split，其余 keep |
| `internal/storage` | 14 | 26 | 40 | Task 5 点名文件 split，其余 keep |
| `internal/world` | 9 | 8 | 17 | keep |
| `internal/worldgen` | 3 | 3 | 6 | keep |
| **总计** | **144** | **242** | **386** | **Task 19 完成最终复核** |

### Task 2–19 历史文件映射

| Task | 职责 | 文件映射 |
| ---: | --- | --- |
| 2 | 架构守卫文件族 | `internal/archcheck/deps_test.go` → `source_guards_test.go`、`dependency_test.go`、`platform_test.go`、`helpers_test.go` |
| 3 | WebGPU 后端 | `internal/gfx/wgpu.go` → `wgpu_convert.go`、`wgpu_pipeline.go`、`wgpu_resource.go`、`wgpu_surface.go`、`wgpu_encoder.go`，核心装配仍留 `wgpu.go` |
| 4 | network 消息与方向 | `message.go`、`codec.go`、`codec_test.go` → `message_{command,container,inventory,player,chunk,drop}.go`、`codec_{client,server,values}.go` 与 `codec_{golden,invalid,inventory,helpers}_test.go` |
| 5 | 区块 codec | `chunk_codec.go`、`chunk_codec_test.go` → `chunk_codec_{logical,container,primitives}.go` 与 `chunk_codec_{envelope,roundtrip,helpers}_test.go` |
| 6 | 模拟 tick 阶段 | `engine.go` → `engine_{step,run,subscription,placement,changes}.go` |
| 7 | session、Host、发布 | `session.go`、`host.go`、`publication.go` → 对应 `session_*`、`host_*`、`publication_*` 职责文件 |
| 8 | 世界持久化 | `persistence.go`、`persistence_test.go` → `persistence_{worker,metadata,schedule,retry,status}.go` 与场景测试/helper 文件 |
| 9 | 玩家持久化 | `player_persistence.go`、`player_persistence_test.go`、`player_flush_test.go` → snapshot/completion/dispatch 与 lifecycle/retry/concurrency/flush barrier 测试文件 |
| 10 | 服务端集成测试 | `tcp_integration_test.go`、`host_test.go`、`multiplayer_tcp_integration_test.go`、`multiplayer_memory_integration_test.go` → TCP restart/parity/furnace、Host capacity/lifecycle、多人 gameplay/capacity/mining/cancel 场景文件及 helper |
| 11 | client mesher/predictor | `mesher.go`、`predictor.go`、`predictor_test.go` → mesher worker/queue、predictor advance/reconcile/presentation 及对应测试/helper |
| 12 | Renderer | 核心 lifecycle 保留在 `renderer.go`；仅将 upload、draw 拆到 `renderer_upload.go`、`renderer_draw.go` |
| 13 | 完整 HUD | `internal/render/hotbar.go`、测试和 shader → `internal/render/hud/{renderer,layout,container,health,atlas,encode}.go`、对应测试与 shader；把共享颜色的唯一实现提升为 `internal/render/drop.go` 中的 `render.ItemColor`，同步 `drop_test.go`；`daylight_test.go` 以 test-only embed 读取移动后的同一份 shader；迁移 `cmd/mcgo` 调用方并更新依赖白名单 |
| 14 | mcgo 应用装配 | `cmd/mcgo/app.go` → `app_{dependencies,metrics,startup,lifecycle,frame,messages,input,render}.go` |
| 15 | mcgo 应用测试 | `app_test.go` → `app_{protocol,render,connection,input,celestial,test_helpers}_test.go` |
| 16 | mcgo 入口与 capture | `main.go`/`main_test.go` → options、run、interactive；`capture.go`/`capture_test.go` → scene、image 生产与测试文件 |
| 17 | benchmark | `benchmark.go`、`multiplayer_benchmark.go` 及大型测试 → measure/report、transport/server 与 scenario/server/report/helper 测试文件 |
| 18 | perfcheck | `cmd/perfcheck/main.go`、`main_test.go` → compare/validate/regression 与 compare/migration/transport/regression/CLI/helper 测试文件 |
| 19 | 审计收尾 | 仅更新本 change 的 `design.md`、`tasks.md`，核对所有 386 个文件和固定 artifact，无生产行为修改 |

### Task 19 历史最终包级审计

审计集合由 `git ls-tree -r --name-only 96c4aae -- cmd internal` 冻结，筛选并排序后的 386 个 Go 路径清单 SHA-256 为 `badfa2853c44fbc6860044149ce0a29c4f10003ffa724018646f43c19fb52416`。下表中的 split、extract、delete 与 keep 互斥，逐行合计等于该包的基线文件数；所有未在“非 keep 路径”列点名的该包基线 Go 文件均为 keep。

| 包 | 基线 | split | extract | delete | keep | 非 keep 路径与结论 |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| `cmd/gfxspike` | 1 | 0 | 0 | 0 | 1 | 无；全部 keep |
| `cmd/mcgo` | 23 | 10 | 0 | 0 | 13 | split：`app.go`、`app_test.go`、`benchmark.go`、`benchmark_v6_test.go`、`capture.go`、`capture_test.go`、`main.go`、`main_test.go`、`multiplayer_benchmark.go`、`multiplayer_benchmark_test.go` |
| `cmd/mcgod` | 3 | 0 | 0 | 0 | 3 | 无；全部 keep |
| `cmd/perfcheck` | 2 | 2 | 0 | 0 | 0 | split：`main.go`、`main_test.go` |
| `internal/archcheck` | 1 | 1 | 0 | 0 | 0 | split：`deps_test.go` |
| `internal/assets` | 6 | 0 | 0 | 0 | 6 | 无；全部 keep |
| `internal/client` | 39 | 3 | 0 | 0 | 36 | split：`mesher.go`、`predictor.go`、`predictor_test.go` |
| `internal/config` | 2 | 0 | 0 | 0 | 2 | 无；全部 keep |
| `internal/core` | 28 | 0 | 0 | 0 | 28 | 无；全部 keep |
| `internal/gfx` | 10 | 1 | 0 | 0 | 9 | split：`wgpu.go` |
| `internal/gfx/shader` | 1 | 0 | 0 | 0 | 1 | 无；全部 keep |
| `internal/logging` | 2 | 0 | 0 | 0 | 2 | 无；全部 keep |
| `internal/mesh` | 9 | 0 | 0 | 0 | 9 | 无；全部 keep |
| `internal/network` | 35 | 3 | 0 | 0 | 32 | split：`codec.go`、`codec_test.go`、`message.go` |
| `internal/physics` | 11 | 0 | 0 | 0 | 11 | 无；全部 keep |
| `internal/profile` | 4 | 0 | 0 | 0 | 4 | 无；全部 keep |
| `internal/render` | 28 | 1 | 2 | 0 | 25 | split：`renderer.go`；extract：`hotbar.go`、`hotbar_test.go` 到唯一新包 `internal/render/hud`；keep 包含仅适配共享颜色或 test-only shader embed 的 `drop.go`、`drop_test.go`、`daylight_test.go` |
| `internal/server` | 75 | 12 | 0 | 0 | 63 | split：`host.go`、`host_test.go`、`multiplayer_memory_integration_test.go`、`multiplayer_tcp_integration_test.go`、`persistence.go`、`persistence_test.go`、`player_flush_test.go`、`player_persistence.go`、`player_persistence_test.go`、`publication.go`、`session.go`、`tcp_integration_test.go` |
| `internal/sim` | 43 | 1 | 0 | 0 | 42 | split：`engine.go` |
| `internal/storage` | 40 | 2 | 0 | 0 | 38 | split：`chunk_codec.go`、`chunk_codec_test.go` |
| `internal/world` | 17 | 0 | 0 | 0 | 17 | 无；全部 keep |
| `internal/worldgen` | 6 | 0 | 0 | 0 | 6 | 无；全部 keep |
| **总计** | **386** | **36** | **2** | **0** | **348** | **每个基线 Go 文件恰有一个结论** |

没有基线 Go 文件被判定为 delete；Git 中消失的原路径均已按声明去向归入 split 或 extract，而不是死代码删除。当前包集合相对基线只新增预批准的 `internal/render/hud`，其余新增 Go 文件均是上表 split/extract 的目标文件。

### Task 20 主线同步基线与冲突迁移

Task 20 新基线固定为 `37cdb3e0b3cd241bad1c3e70e5a25bcc9994c4fa`。`git ls-tree` 只读核对为 155 个生产 Go 文件 + 257 个测试 Go 文件 = 412；预期结论为 36 split + 2 extract + 0 delete + 374 keep = 412，且相对主线只允许新增已经批准的 `internal/render/hud` 包。M4N 已由主线归档，不得恢复成 active change。

只读 `git merge-tree --write-tree --name-only HEAD origin/main` 精确得到以下 13 个冲突。每项必须按声明迁入目标职责文件；对于仍保留同名核心文件的 content conflict，也不得把上游整份大文件覆盖回来。

| # | 冲突路径 | 目标职责文件 | 必须保留的上游语义 |
| ---: | --- | --- | --- |
| 1 | `cmd/mcgo/app.go` | 现有 `app_*` 生产文件族 | common materials、damage/target 的应用装配与输入/呈现路径 |
| 2 | `cmd/mcgo/app_test.go` | `app_*_test.go` 场景文件 | damage/target、材料与资源释放断言 |
| 3 | `cmd/mcgo/capture.go` | `capture.go`、`capture_scene.go`、`capture_image.go` | capture 场景扩为固定 10 项，场景顺序、settle 与 golden 比较不变 |
| 4 | `internal/gfx/wgpu.go` | `wgpu_convert.go`、`wgpu_pipeline.go` | `DepthCompareLessEqual` 转换与 pipeline 使用语义 |
| 5 | `internal/network/codec_test.go` | `codec_golden_test.go`、`codec_inventory_test.go` | 协议 v15 golden 与 inventory/material wire 字节 |
| 6 | `internal/network/message.go` | `message_container.go` | smelting/container 校验顺序与错误语义 |
| 7 | `internal/render/hotbar.go` | `internal/render/hud/{container,layout,atlas}.go` | common materials、8 个配方及 HUD 呈现 |
| 8 | `internal/render/hotbar_test.go` | `internal/render/hud/{container,layout,atlas}_test.go` 与既有 helper | 8 个配方、材料颜色和命中测试 |
| 9 | `internal/server/player_persistence.go` | `player_persistence_snapshot.go` | starter materials 的保存/恢复身份 |
| 10 | `internal/server/player_persistence_test.go` | `player_persistence_lifecycle_test.go` | starter materials 生命周期与兼容恢复断言 |
| 11 | `internal/server/tcp_integration_test.go` | `tcp_restart_integration_test.go`、`furnace_tcp_integration_test.go` | Ready barrier、重连与多人 smelting 行为 |
| 12 | `internal/storage/chunk_codec.go` | `chunk_codec_container.go` | world-Y container 与 schema v8 逻辑载荷 |
| 13 | `internal/storage/chunk_codec_envelope_test.go` | `chunk_codec_envelope_test.go`、`chunk_codec_roundtrip_test.go`、`chunk_codec_helpers_test.go` | schema v8 fixture、envelope、roundtrip 与 helper 唯一来源 |

`37cdb3e` 的上游能力属于主线：M4O 只调整声明位置。冲突解决不得删除上游 case/fixture、不得复制实现或引入 wrapper，也不得用旧分支版本覆盖协议 v15、schema v8/v6、10 个视觉场景或已归档 M4N 能力。

### 默认同包拆分，唯一 HUD 提包

共享包内权威状态的职责留在原包，避免协调接口、循环依赖和类型搬迁。唯一新包 `internal/render/hud` 只允许依赖 `internal/core`、`internal/mesh`、`internal/assets`、`internal/render`、`internal/gfx`；`internal/render` 不得反向依赖 HUD。程序化物品颜色的唯一实现从 `hotbarItemColor` 提升为 `internal/render/drop.go` 中的窄内部 API `render.ItemColor`，掉落物与 HUD 都直接调用它；`hud/layout.go` 不拥有或复制颜色实现，也不引入 wrapper、alias、callback/config、第二包或重复实现。只有 `internal/gfx` 可以直接导入 WebGPU 绑定，`sim` 不依赖渲染，`world` 不依赖 `network`。

跨职责测试 `TestScreenSpaceRenderersIgnoreWorldDaylight` 继续留在 `internal/render/daylight_test.go` 并保留 name tag 与 hotbar 两项断言。HUD shader 移动后，该 `_test.go` 文件通过 test-only `//go:embed hud/shader/hotbar.wgsl` 读取同一份唯一 shader；这不复制 shader 字节、不新增生产 API、不让 `internal/render` 生产包导入 HUD，也不保留生产 `hotbarShader` wrapper。

被否决的替代方案是全面领域拆包、只做机械拆文件和新增文件行数门禁：前者扩大 API 与循环风险，第二项遗漏已稳定成域的 HUD，第三项鼓励无意义碎片化。

### build tag、CGO 与并发边界不移动

- `internal/gfx/wgpu*.go` 保留 `//go:build darwin`；Objective-C bridge、`C` 引用与 `NewDevice/newDevice` 留在可安全引用它们的文件。
- 从 `cmd/mcgo/main.go`、`app.go` 及对应测试拆出的文件继续带 `//go:build darwin`；当前无 tag 的 `capture.go` 文件族继续无 tag。
- `cmd/mcgod` 保持无 CGO、无图形依赖的构建能力。
- goroutine、channel、锁、buffer、消息不可变约定、资源所有权和释放顺序保持不变；重组不得引入新协调层。

### 行为与 artifact 不变量

- Task 19 历史审计保持协议 v14、区块 schema v7、玩家 schema v5 与当时固定 hash 的事实不变；Task 20 不再把这些旧值当作当前验收值。
- Task 20 的 CLI、游戏行为、错误值/文本、日志字段、GPU label 和绘制顺序必须与 `37cdb3e` 一致，并保留协议 v15、packet ID/wire bytes、区块 schema v8、玩家 schema v6、世界 metadata v2 和已归档 M4N 行为。
- storage/network fixture、10 个视觉 golden 与其他固定 artifact 直接用 `git diff`/`cmp` 对比 `37cdb3e`，不得沿用 Task 19 的旧 hash 代替主线字节证明。若上游没有修改 M2/M5 性能 baseline，则它们保持原字节；benchmark workload、scenario v15、阈值数值与报告格式不变。
- benchmark 与 `perfcheck` 的性能数值只保存记录、不改变退出状态，只有报告结构、身份/provenance、真实 overflow、数据丢失、I/O 错误和非数值命令失败阻断。
- 相对 `37cdb3e`，只允许 Task 2 明确批准新增 `TestProductionGoSourceScansSplitFiles`、`TestTopLevelDeclarationNamesInScansSplitFiles`，并将 `TestSessionLifecycleResponsibilitiesLiveInSessionFile` 重命名为 `TestSessionLifecycleResponsibilitiesStayInSessionFiles`；其余 Test、Benchmark、Fuzz 入口名完全一致。自动验证不启动前台窗口，也不更新 golden/baseline。

## Risks / Trade-offs

- [声明移动夹带行为漂移] → 每项先记录 baseline green，只移动一个职责，随后跑 focused race 测试和架构守卫。
- [架构守卫继续绑定旧文件名] → Task 2 先改为扫描生产文件集合与 `session*.go`，不得删除或放宽断言。
- [HUD 形成反向依赖] → 白名单只登记 HUD 到既有底层包的依赖，并由 archcheck 拒绝 `internal/render` 反向导入。
- [共享颜色出现重复实现或漂移] → `render.ItemColor` 是 `internal/render/drop.go` 中的唯一实现，掉落物与 HUD 共用它；focused 掉落物测试与视觉 golden 同时验证颜色不变。
- [HUD 迁移断开跨职责昼夜测试] → `daylight_test.go` 仅在测试构建中 embed 移动后的唯一 shader，保持原测试名以及 name tag/hotbar 断言，并比较移动前后 hash。
- [build tag 或 CGO 边界损坏] → 保留原 tag，运行 Darwin focused 测试、archcheck 与无图形服务端构建。
- [性能或固定 artifact 漂移] → 对比既有 hash、visual capture、benchmark 与 baseline；固定 artifact、报告结构或身份异常按错误阻断，性能数值差异只记录，均不得改期望值掩盖差异。
- [无意义碎片化] → 以职责为单位拆分，未点名且内聚的文件 keep，不设置行数门禁。
- [冲突解决复活旧大文件或丢失主线声明] → 只对上表 13 个冲突逐声明迁移，以 `37cdb3e` 的入口、artifact 和 focused 测试做双向 parity，不接受 ours/theirs 整文件覆盖。

## Migration Plan

1. Task 2 先解除守卫对单一文件位置的依赖。
2. Task 3–18 按叶子包到装配层顺序执行，每项独立提交并通过 focused 命令。
3. Task 19 以 `96c4aae` 复核包级审计、测试入口、fixture/golden/baseline 和最终共享门禁；该历史阶段已完成。
4. Task 20 先提交规划修订，再合并固定的 `origin/main=37cdb3e`；手工迁移 13 个冲突中的上游声明，完成 412 文件审计、声明/入口/artifact parity、focused/race/fuzz/10 场景视觉/性能记录与全仓门禁，独立 review 后才勾选、完成 merge commit 并 push。

Task 20 没有自有协议、存档或部署迁移；协议 v15 与 schema v8/v6 是主线既有增量。任一波次出现无法解释的行为、golden、artifact、报告结构、身份/provenance、真实 overflow、数据丢失、I/O 错误或非数值命令失败时立即停止并回退该波次；性能数值差异只记录、不触发停止或回退。不得修改期望值、schema、scenario、阈值或基线；package extraction 若需要新状态、行为分支、协调接口或兼容 wrapper，则取消提包并先修订设计。
