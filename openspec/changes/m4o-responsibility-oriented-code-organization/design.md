## Context

本变更基于提交 `96c4aae` 及其仅含规划文档的后代。基线 `cmd/` 与 `internal/` 共 386 个 Go 文件，其中 144 个生产文件、242 个测试文件；职责热点横跨架构守卫、WebGPU、网络、存储、模拟、服务端、客户端、渲染和命令装配。参见 `proposal.md` 与 `specs/repository-code-organization/spec.md`。

## Goals / Non-Goals

**Goals:**

- 为 386 个基线 Go 文件建立包级审计覆盖，并让每个文件最终归入保留、同包拆分、提取新包或删除。
- 默认只移动声明；测试与其职责同波次整理，每个波次独立验证、提交和回退。
- 仅把完整 HUD 提取到 `internal/render/hud`，同时让架构守卫覆盖拆分后的职责文件族。

**Non-Goals:**

- 不修改算法、CLI、游戏功能、协议/存档/性能契约或并发语义。
- 不以行数作为门禁，不新增通用包、其他新包、兼容 wrapper、工厂或单实现接口。
- 不同步、归档或删除已完成的 `m4n-static-block-light` change。

## Decisions

### 以包为审计闭环

基线审计表冻结如下。计划点名的文件按 Task 2–18 执行 split/move/delete；同包其余基线文件均先判定为 keep，并由 Task 19 的逐包测试与入口名对比复核。行数只用于发现候选，不决定结论。

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
| `internal/render` | 11 | 17 | 28 | Task 12 split；Task 13 HUD move；其余 keep |
| `internal/server` | 16 | 59 | 75 | Task 7–10 点名文件 split，其余 keep |
| `internal/sim` | 14 | 29 | 43 | Task 6 点名文件 split，其余 keep |
| `internal/storage` | 14 | 26 | 40 | Task 5 点名文件 split，其余 keep |
| `internal/world` | 9 | 8 | 17 | keep |
| `internal/worldgen` | 3 | 3 | 6 | keep |
| **总计** | **144** | **242** | **386** | **Task 19 完成最终复核** |

### Task 2–19 文件映射

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
| 12 | Renderer | `renderer.go` → `renderer_upload.go`、`renderer_draw.go` |
| 13 | 完整 HUD | `internal/render/hotbar.go`、测试和 shader → `internal/render/hud/{renderer,layout,container,health,atlas,encode}.go`、对应测试与 shader；迁移 `cmd/mcgo` 调用方并更新依赖白名单 |
| 14 | mcgo 应用装配 | `cmd/mcgo/app.go` → `app_{dependencies,metrics,startup,lifecycle,frame,messages,input,render}.go` |
| 15 | mcgo 应用测试 | `app_test.go` → `app_{protocol,render,connection,input,celestial,test_helpers}_test.go` |
| 16 | mcgo 入口与 capture | `main.go`/`main_test.go` → options、run、interactive；`capture.go`/`capture_test.go` → scene、image 生产与测试文件 |
| 17 | benchmark | `benchmark.go`、`multiplayer_benchmark.go` 及大型测试 → measure/report、transport/server 与 scenario/server/report/helper 测试文件 |
| 18 | perfcheck | `cmd/perfcheck/main.go`、`main_test.go` → compare/validate/regression 与 compare/migration/transport/regression/CLI/helper 测试文件 |
| 19 | 审计收尾 | 仅更新本 change 的 `design.md`、`tasks.md`，核对所有 386 个文件和固定 artifact，无生产行为修改 |

### 默认同包拆分，唯一 HUD 提包

共享包内权威状态的职责留在原包，避免协调接口、循环依赖和类型搬迁。唯一新包 `internal/render/hud` 只允许依赖 `internal/core`、`internal/mesh`、`internal/assets`、`internal/render`、`internal/gfx`；`internal/render` 不得反向依赖 HUD。只有 `internal/gfx` 可以直接导入 WebGPU 绑定，`sim` 不依赖渲染，`world` 不依赖 `network`。

被否决的替代方案是全面领域拆包、只做机械拆文件和新增文件行数门禁：前者扩大 API 与循环风险，第二项遗漏已稳定成域的 HUD，第三项鼓励无意义碎片化。

### build tag、CGO 与并发边界不移动

- `internal/gfx/wgpu*.go` 保留 `//go:build darwin`；Objective-C bridge、`C` 引用与 `NewDevice/newDevice` 留在可安全引用它们的文件。
- 从 `cmd/mcgo/main.go`、`app.go` 及对应测试拆出的文件继续带 `//go:build darwin`；当前无 tag 的 `capture.go` 文件族继续无 tag。
- `cmd/mcgod` 保持无 CGO、无图形依赖的构建能力。
- goroutine、channel、锁、buffer、消息不可变约定、资源所有权和释放顺序保持不变；重组不得引入新协调层。

### 行为与 artifact 不变量

- CLI、游戏行为、错误值/文本、日志字段、GPU label 和绘制顺序不变。
- 协议 v14、packet ID、wire bytes、区块 schema v7、玩家 schema v5、世界 metadata v2、fixture 与 hash 不变。
- 视觉 golden、benchmark workload、scenario v15、M2/M5 性能基线、阈值与报告格式不变。
- 测试、Benchmark、Fuzz 入口名保持不变；自动验证不启动前台窗口，也不更新 golden/baseline。

## Risks / Trade-offs

- [声明移动夹带行为漂移] → 每项先记录 baseline green，只移动一个职责，随后跑 focused race 测试和架构守卫。
- [架构守卫继续绑定旧文件名] → Task 2 先改为扫描生产文件集合与 `session*.go`，不得删除或放宽断言。
- [HUD 形成反向依赖] → 白名单只登记 HUD 到既有底层包的依赖，并由 archcheck 拒绝 `internal/render` 反向导入。
- [build tag 或 CGO 边界损坏] → 保留原 tag，运行 Darwin focused 测试、archcheck 与无图形服务端构建。
- [性能或固定 artifact 漂移] → 对比既有 hash、visual capture、benchmark 与 baseline；不得改期望值掩盖差异。
- [无意义碎片化] → 以职责为单位拆分，未点名且内聚的文件 keep，不设置行数门禁。

## Migration Plan

1. Task 2 先解除守卫对单一文件位置的依赖。
2. Task 3–18 按叶子包到装配层顺序执行，每项独立提交并通过 focused 命令。
3. Task 19 复核包级审计、测试入口、fixture/golden/baseline 和最终共享门禁；change 保持 active。

本变更没有协议、存档或部署迁移。任一波次出现无法解释的行为、golden、hash 或性能差异时立即停止并回退该波次提交，不修改期望值、schema、scenario、阈值或基线；package extraction 若需要新状态、行为分支、协调接口或兼容 wrapper，则取消提包并先修订设计。
