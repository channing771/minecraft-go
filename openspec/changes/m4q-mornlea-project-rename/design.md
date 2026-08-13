## Context

见 `proposal.md`。M4Q 以 Task 1 合并并冻结的统一 HEAD 为功能基线；该基线同时包含 M4P Rust mesh/light 与主线程序化方块云。改名横跨 Go module、两个命令、Rust/C ABI、构建与本机默认数据路径，但不得改变协议、存档、渲染、benchmark 工作负载或固定 artifact。

配置仍归 `internal/config` 所有，玩家身份仍归 `internal/profile` 所有；命令只选择默认或显式加载入口。Go 继续拥有 app、world、sim、network、storage、render 与 `internal/mesh` API，只有 `internal/mesh` 接触 native ABI。服务端依旧不依赖 client、mesh、render 或 gfx。

## Goals / Non-Goals

**Goals:**

- 通过一个可构建的原子身份提交切换当前 module、命令、Rust ABI、构建、CI、Hook、archcheck 与测试身份。
- config/profile 分别复用现有 codec 与临时文件流程，以 `os.Link` no-clobber 发布安全继承旧文件。
- 用 Task 1 固定 artifact、Task 6 测试入口清单与同机双树视觉结果证明纯改名不产生行为漂移。

**Non-Goals:**

- 不建立迁移 framework、兼容 wrapper、module alias、旧 C symbol/env fallback、后台任务或双向同步。
- 不升级 config/profile schema，不改变协议、存档、ABI 数值、benchmark scenario、golden、阈值或性能 baseline。
- 不改写历史设计、归档 change、性能证据或 Git 历史，也不在源码 change 中执行仓库外改名。

## Decisions

### 1. 使用固定的一对一身份映射

| 当前身份 | Mornlea 身份 |
| --- | --- |
| `minecraft-go` | `Mornlea`（叙述）/ `mornlea`（机器标识） |
| `module minecraft-go` | `module github.com/channing771/mornlea` |
| `minecraft-go/internal/...` | `github.com/channing771/mornlea/internal/...` |
| `cmd/mcgo` | `cmd/mornlea` |
| `cmd/mcgod` | `cmd/mornlea-server` |
| `bin/mcgo` | `bin/mornlea` |
| `mcgo` | `mornlea` |
| `mcgod` | `mornlea-server` |
| `mcgo_mesh` crate | `mornlea_mesh` crate |
| `libmcgo_mesh.dylib` | `libmornlea_mesh.dylib` |
| `mcgo_engine.h` | `mornlea_engine.h` |
| `mcgo_engine_abi_version` | `mornlea_engine_abi_version` |
| `mcgo_mesh_section` | `mornlea_mesh_section` |
| `MCGO_ENGINE_*` / `MCGO_STATUS_*` | `MORNLEA_ENGINE_*` / `MORNLEA_STATUS_*` |
| `MINECRAFT_GO_*` / 测试专用 `MCGO_*` | 对应 `MORNLEA_*` |
| 用户目录 `minecraft-go` | 用户目录 `mornlea` |

Hook 的具体身份为 `MORNLEA_HOOKS_ALLOW_NO_SPEC` 与 `[mornlea hook]`；测试 helper 使用 `MORNLEA_SERVER_PROCESS*`、`MORNLEA_AVATAR_COLOR_HELPER`、`MORNLEA_REGION_CRASH_*`。ABI version 保持 `1`，status 保持 `0..9`，install name 保持同一打包结构下的 `@rpath/libmornlea_mesh.dylib`。

选择一对一机械映射是为了让扫描和回退可判定。拒绝保留旧命令 wrapper、symlink、双 module 或旧 C symbol，因为它们会制造两套当前身份和长期兼容面。

### 2. 默认数据迁移留在各自所有者内

config 与 profile 独立按 `current → legacy → missing both` 处理：

1. `os.UserConfigDir()/mornlea/{config,profile}.json` 存在时，它是唯一权威来源；权限、读取或解码失败直接返回，不检查旧文件。
2. 新文件仅在精确 `os.ErrNotExist` 时检查 `os.UserConfigDir()/minecraft-go/{config,profile}.json`；旧文件先经现有 codec 完整解码，再编码为规范化内容。
3. config 两边缺失时返回编译默认值且不落盘；profile 两边缺失时生成一个 UUIDv4 candidate 并参加同一 exclusive 发布竞争。

默认发布创建或校验 0700 父目录，在同目录创建 0600 临时文件，完整写入、`Sync`、`Close` 后调用 `os.Link(temp, target)`。成功者移除临时 hardlink 并同步父目录；`fs.ErrExist` loser 清理自己的临时文件。无论成功或落败，都再次校验默认父目录/目标权限，从目标重读并解码；只有成功发布并最终验证通过的 legacy 迁移者记录一条含精确 `legacy_path`、`current_path` 的 `slog.Info`。任何其他错误清理自己的临时文件但不修改旧文件或赢家。

`internal/config` 与 `internal/profile` 各自实现这段最小私有流程，不新增共享 interface 或通用 migration package。这样避免制造仅有两个使用者且错误语义不同的抽象。

### 3. 默认与显式路径在命令路由边界分开

`config.LoadDefault()` 和空 `profile.Options.Path` 才启用默认迁移。显式 `--config PATH` 继续调用 `config.Load(path)`；自定义 profile path 与 requested-name 保存继续走既有 replace-on-save。客户端 benchmark/capture 的 config 继续直接使用编译默认值；capture 的 profile 由验收命令通过隔离 HOME 固定。专用服务端材料离线迁移仍在 config/server 装配前返回。

Tasks 4–5 暂时保留旧 `DefaultPath()`，先完成不可部署的迁移底层；Task 6 在同一提交中把两个 `DefaultPath()` 切到 `mornlea` 并停止命令把默认路径伪装成显式路径。拒绝只改路径常量，因为中间提交会绕过迁移并生成新 PlayerID。

### 4. 当前身份在一个提交中原子切换

Task 7 同时切换：

- `go.mod` 与所有精确 `minecraft-go/internal/...` imports；
- `cmd/mcgo/**` → `cmd/mornlea/**`、`cmd/mcgod/**` → `cmd/mornlea-server/**`，包括测试与 10 张仅移动路径的 golden；
- `engine/crates/mcgo_mesh/**`、`engine/include/mcgo_engine.h`、Cargo workspace/lock、C symbols 与 `internal/mesh/native_abi.go`；
- `Makefile`、`.github/workflows/ci.yml`、`.gitignore`、`.codex/hooks.json`、`scripts/agent-hooks/**`、`internal/archcheck/**` 及当前测试 helper 身份。

`cmd/gfxspike/main.go`、`internal/client/perf.go`、`internal/logging/logging.go`、`internal/render/avatar_test.go` 与 `internal/storage/region_crash_test.go` 只做对应身份替换。算法、Go 导出 API、packed quad、ABI 数值、fixture、golden 和命令行为不改。拒绝拆成 module、命令、Rust、构建多个提交，因为任一中间状态都可能无法构建或加载 dylib。

### 5. 使用精确的历史与兼容 allowlist

当前代码/构建扫描只覆盖：`go.mod`、`cmd/**`、`internal/**`、`engine/Cargo.toml`、`engine/Cargo.lock`、`engine/crates/**`、`engine/include/**`、`Makefile`、`.github/workflows/ci.yml`、`.codex/hooks.json`、`scripts/agent-hooks/**`、`.gitignore`；明确不扫描 `.git/**`、`engine/target/**`、`bin/**`、`.superpowers/**` 或文档/历史根。

该扫描对旧 module、import、命令、crate、dylib、header、C symbol 与环境变量零容忍。大小写不敏感的裸旧 token 仅允许在 `internal/config/**`、`internal/profile/**` 表达 legacy 数据目录；`.mcgo-world-backup-v1.json` 仅允许在 `internal/storage/backup.go` 与 `internal/storage/backup_test.go`。允许文件也不能豁免旧 import、命令、C symbol、环境变量或注释。

兼容身份 `CHNK`、`MCGC`、`MCGM`、`MCPL`、`MCGR`、`MCGB`、`MGM1` 与 `.mcgo-world-backup-v1.json` 保持原值；它们不是品牌替换目标。历史原文 allowlist 精确为 `docs/superpowers/**`、`openspec/changes/archive/**` 与 `docs/notes/perf-baseline*.{md,json}`，另保留 Git 历史。当前 README 唯一旧字面量例外是链接 `docs/superpowers/specs/2026-07-26-minecraft-go-design.md`；`docs/notes/mornlea-migration.md` 作为新旧映射说明单独允许旧名。拒绝用全仓 `MCG` 禁令或宽泛目录豁免，以免误伤稳定 magic 或隐藏真实品牌残留。

### 6. 固定测试入口与 artifact，不以改名重置基线

Task 6 完成后、Task 7 开始前持久化 Test/Benchmark/Fuzz 清单。相对该清单只允许 6 个命令身份测试按 delta spec 的精确映射重命名，并新增 `TestMornleaCurrentIdentity`；其余入口不变。Task 1 持久化 network/storage/worldgen fixture、tracked performance baseline 与 10 张 golden 的 SHA-256；Task 9 从 Git common-dir 复验，禁止修改 tracked artifact。

视觉门禁由 `system_profiler SPHardwareDataType` 的精确 `Chip` 分支裁决：

- Apple M2/macOS：原始 Task 1 `origin/main` 与 Mornlea 分支在各自隔离 HOME 下对全部 10 个场景产出 byte-identical PNG。两边仅允许 `materials-showcase`（最大通道差 1、26 像素、0.0113%）和 `oak-grove`（最大通道差 47、10 像素、0.0043%）保留精确相同的 nonzero 摘要与 actual/diff；其余 8 个场景通过 tracked golden。
- 任意非 Apple M2 Chip：两树全部 10 个场景 PNG byte-identical，两次 non-update `visual-check` 都退出 0，任一树都不得产生 `*-actual.png` 或 `*-diff.png`。

两种裁决都禁止 `visual-update`、golden 更新、阈值调整或 capture 代码修改。相同硬件、命令、隔离 HOME 与原始主线对照是必要条件；不能用跨硬件 golden 偏差推导改名漂移。

## Risks / Trade-offs

- [并发发布后目标权限或内容不安全] → loser 和 winner 都在发布后重复执行 0700/0600 检查并从目标重读，不直接返回 candidate。
- [旧文件非法时生成新 PlayerID，表现为玩家状态丢失] → legacy 解码失败立即返回含旧路径的错误，Random reader 必须零读取。
- [单向复制后交替运行新旧版本造成状态分叉] → 保留旧目录且迁移说明明确无双向同步；程序不自动删除任一目录。
- [原子身份提交范围大] → 先冻结路径、test-entry 与 golden manifests，再用路径映射、identity guard、Rust/dylib 探针和 Linux closure 一次验证。
- [历史豁免隐藏当前残留] → 历史与兼容 allowlist 使用精确路径；当前 code/build roots 由自扫描 archcheck 守卫。
- [硬件相关视觉基线被误判] → 记录精确 Chip，并按上述 M2/非 M2 双合同与原始 Task 1 `origin/main` 同机比较。

## Migration Plan

1. Tasks 4–5 分别以 TDD 完成 config/profile 默认迁移、权限、失败与并发 no-clobber 语义；每项提交只触及所属包与 `tasks.md`。
2. Task 6 原子切换两个公共默认路径和客户端/服务端路由，并持久化 Task 7 初始 test-entry baseline。
3. Task 7 以一个提交完成 Go、命令、Rust ABI、构建、Hook 与守卫身份切换；Task 8 只更新当前文档和单一迁移说明。
4. Task 9 比较固定 hashes、运行 protocol/storage/scenario、fuzz、双树视觉、dylib/Linux、benchmark 记录和全仓门禁，将 exact provenance 写回本设计后停在独立评审。
5. Task 10 独立处理评审结论、勾选 9.1、严格校验并归档 M4Q；该关闭动作不能成为本 change 自我完成的 checkbox。

代码回退按任务提交逆序执行。回退到旧版本时旧程序继续读取保留的 `minecraft-go` 目录；已经复制到 `mornlea` 的文件不自动删除，数据迁移提交可独立回退。身份提交失败时整体回退，不保留半套 module/ABI。

源码 PR 合并并验证后，operator 才依序：在 GitHub 将 `channing771/minecraft-go` 改为 `channing771/mornlea`；更新 `origin` 与需保留的 fork remote；确认 linked worktree 均已合并、保留或安全移除后再移动本地根目录；清理旧 GitNexus 索引并在新绝对路径重建。任一步失败均暂停在当前可用状态：GitHub 名称可恢复并重设 remote，本地目录可移回，GitNexus 可重新索引；不得删除 linked worktree 元数据或用户数据。
