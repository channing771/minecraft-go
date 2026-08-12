# 自然材料生成与离线迁移 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让沙子、砾石、黏土和雪块进入确定性新区块生成，并提供带强制完整备份、断点续跑和幂等完成标记的旧世界离线迁移。

**Architecture:** 继续由 `worldgen.Generator` 的同一纯判断同时服务 `BaseBlockAt` 与 `GenerateChunk`；矿石只在自然材料判断最终仍为石头时覆盖。离线入口只存在于 `mcgod`，通过具体 `*storage.DiskStore` 的只读键枚举和备份能力处理磁盘世界，不扩大服务器运行时的 `storage.Store` 接口。

**Tech Stack:** Go 1.26（使用用户已有 gvm）、现有 Perlin/worldgen、DiskStore 区域存档、OpenSpec、Go 标准库文件复制与原子 rename。

## Global Constraints

- [ ] 共同前置：在 `main` 上归档已经 `28/28` 完成的 `m4n-static-block-light`，运行 `openspec validate --all --strict --no-interactive`，提交并合入；`openspec list --json` 不得再列出该 change。
- [ ] 同步远程 `main` 后冻结一次共同 `BASE_SHA`，三个并行任务都必须从该 SHA 创建；本任务使用：

```bash
cd /Users/chen/chenwork/minecraft-go
BASE_SHA=$(git rev-parse main)
git worktree add .worktrees/natural-material-generation -b codex/natural-material-generation "$BASE_SHA"
cd /Users/chen/chenwork/minecraft-go/.worktrees/natural-material-generation
test "$(git rev-parse HEAD)" = "$BASE_SHA"
```

- [ ] 本分支不得修改 `AGENTS.md`、`CLAUDE.md`、`README.md`、`openspec/config.yaml`、任何视觉 golden 或另外两个任务的 OpenSpec。
- [ ] 不下载 Go；所有 Go 命令使用现有 gvm 环境，例如 `zsh -ic 'go test ...'`。
- [ ] 不启动前台游戏窗口。本任务没有人工视觉步骤。
- [ ] 性能数值只记录；存档损坏、丢数据、备份/同步失败、真实 overflow 与 I/O 错误仍必须失败。
- [ ] 每完成一个任务组：跑该组验证、只勾当前 checkbox、提交，然后自动进入下一组。

---

## Task 1: 创建并严格校验独立 OpenSpec change

**Files:**
- Create: `openspec/changes/natural-material-generation/proposal.md`
- Create: `openspec/changes/natural-material-generation/design.md`
- Create: `openspec/changes/natural-material-generation/specs/natural-material-generation/spec.md`
- Create: `openspec/changes/natural-material-generation/tasks.md`

- [ ] 1.1 读取 `openspec/config.yaml`、批准设计和现有 `deterministic-ore-generation` / `common-block-materials` 主规格，然后用 `openspec-propose` 创建 change；不要复制历史文档全文。
- [ ] 1.2 delta spec 至少写成可判定 Requirement/Scenario，覆盖：四种自然材料、单点/整区块一致、负坐标/边界连续、矿石只替换最终石头、离线互斥、完整备份、稳定键顺序、七种自然值强制重算、非自然负载保留、失败续跑、完成后幂等、schema v8/protocol v15/metadata v2 不变。
- [ ] 1.3 design 固定本计划中的常量、批大小 `32`、进度文件 `material-migration-v1.json`、备份身份文件 `.mcgo-world-backup-v1.json` 和所有权边界；tasks 逐项映射本计划 Task 2–7。
- [ ] 1.4 运行：

```bash
openspec validate natural-material-generation --strict --no-interactive
openspec validate --all --strict --no-interactive
git diff --check
```

Expected: change 与全仓校验均成功。

- [ ] 1.5 只提交 OpenSpec 产物：

```bash
git add openspec/changes/natural-material-generation
git commit -m "docs: 规划自然材料生成与离线迁移"
```

---

## Task 2: 用同一纯判断生成四种自然材料

**Files:**
- Create: `internal/worldgen/material_test.go`
- Modify: `internal/worldgen/generator.go`
- Modify: `internal/worldgen/generator_test.go`
- Modify: `internal/worldgen/ore_test.go`
- Modify: `internal/worldgen/testdata/golden_seed42.txt`

- [ ] 2.1 先在 `material_test.go` 写 RED 测试。固定 `seed=42`、扫描世界坐标 `[-1024,1024]`（步长 `4`），断言沙/砾石/黏土/雪块都出现；每种材料至少存在一对水平相邻样本；分别检查 `x=15/16`、`z=-17/-16` 与负坐标；并让现有逐坐标 `BaseBlockAt`/`GenerateChunk` 一致性测试覆盖全部 Y。

关键断言形态：

```go
seen := map[core.BlockID]int{}
adjacent := map[core.BlockID]bool{}
for x := int32(-1024); x <= 1024; x += 4 {
	for z := int32(-1024); z <= 1024; z += 4 {
		height := generator.HeightAt(x, z)
		for _, y := range []int32{height, height - 1, height - 2, height - 4, height - 10} {
			block := generator.BaseBlockAt(core.BlockPos{X: x, Y: y, Z: z})
			seen[block]++
			if generator.BaseBlockAt(core.BlockPos{X: x + 1, Y: y, Z: z}) == block {
				adjacent[block] = true
			}
		}
	}
}
for _, block := range []core.BlockID{core.SandID, core.GravelID, core.ClayID, core.SnowBlockID} {
	if seen[block] == 0 || !adjacent[block] {
		t.Fatalf("材料 %d seen=%d adjacent=%v", block, seen[block], adjacent[block])
	}
}
```

- [ ] 2.2 运行 RED：

```bash
zsh -ic 'go test ./internal/worldgen -run "NaturalMaterial|BaseBlockAtMatches|Ore" -count=1'
```

Expected: 四种材料缺失，测试失败；既有矿石测试仍说明当前语义。

- [ ] 2.3 在 `generator.go` 只增加以下编译期常量，不增加配置或第二个噪声对象：

```go
const (
	snowLine             int32   = 88
	sandLine             int32   = 62
	clayNoiseScale               = 1.0 / 96.0
	clayNoiseOffsetX     int32   = 417
	clayNoiseOffsetZ     int32   = -193
	clayNoiseThreshold           = 0.18
	gravelNoiseScale             = 1.0 / 72.0
	gravelNoiseOffsetX   int32   = -271
	gravelNoiseOffsetZ   int32   = 613
	gravelNoiseThreshold         = 0.22
	gravelMaxDepth       int32   = 10
)
```

- [ ] 2.4 把 `terrainBlockAt` 的结果交给一个最小 `naturalBlockAt(pos,height)`：基岩/空气先返回；高地表 `height>=88` 为雪块；低地 `height<=62` 的深度 `0..3` 为沙，只有深度 `2..3` 且 clay 噪声过阈值时为黏土；最终仍为石头且深度 `4..10`、gravel 噪声过阈值时为砾石。`generatedBlockAt` 仅在该结果为石头后执行原铁矿/煤矿判断。

```go
base := g.naturalBlockAt(pos, height)
if base != IDStone { return base }
// 原有 iron/coal 判断保持字节级不动。
```

- [ ] 2.5 运行 GREEN，并显式杀死两个 mutation：把 gravel 判断移到矿石之后时矿石测试应失败；让 `BaseBlockAt` 绕过 `naturalBlockAt` 时单点/整区块一致性测试应失败。恢复生产实现后执行：

```bash
zsh -ic 'go test ./internal/worldgen -race -count=1'
zsh -ic 'go test ./internal/worldgen -run TestGenerateChunkGolden -update -count=1'
zsh -ic 'go test ./internal/worldgen -race -count=1'
gofmt -w internal/worldgen/generator.go internal/worldgen/material_test.go internal/worldgen/generator_test.go internal/worldgen/ore_test.go
gofmt -l internal/worldgen
git diff --check
```

Expected: 全绿，`gofmt -l` 无输出，golden 只反映批准的生成变化。

- [ ] 2.6 勾选对应 OpenSpec tasks 后提交：

```bash
git add internal/worldgen openspec/changes/natural-material-generation/tasks.md
git commit -m "feat: 生成连续自然材料"
```

---

## Task 3: 为 DiskStore 增加确定性只读区块键枚举

**Files:**
- Create: `internal/storage/chunk_keys.go`
- Create: `internal/storage/chunk_keys_test.go`

- [ ] 3.1 写 RED：保存 `{dim:0,x:-33,z:32}`、`{0,0,0}`、`{1,5,-2}` 三个区块，关闭并重开后调用 `ChunkKeys`；断言按 `Dimension,X,Z` 排序、空槽不返回、负 region/slot 还原正确、损坏 region 返回 `ErrCorrupt`、取消 context 返回取消错误，且不会创建新区块或提升 revision。
- [ ] 3.2 运行 RED：

```bash
zsh -ic 'go test ./internal/storage -run "ChunkKeys" -count=1'
```

Expected: `(*DiskStore).ChunkKeys` 尚不存在，编译失败。

- [ ] 3.3 最小实现 `func (store *DiskStore) ChunkKeys(context.Context) ([]core.ChunkKey, error)`：只扫描 `dimensions/{dimension}/regions/r.{regionX}.{regionZ}.region`，其中三个占位均按有符号十进制解析；逐个 `openRegion` 读取已选择 bank，按 `OffsetSector!=0` 还原 slot；关闭该 region；最后用 `slices.SortFunc` 按 `Dimension/Pos.X/Pos.Z` 排序。不要把方法加入 `storage.Store` / `WorldStore`。
- [ ] 3.4 GREEN 与 mutation：删除最终排序时固定乱序夹具必须失败；恢复后运行：

```bash
gofmt -w internal/storage/chunk_keys.go internal/storage/chunk_keys_test.go
zsh -ic 'go test ./internal/storage -run "ChunkKeys|Region|Disk" -race -count=1'
zsh -ic 'go test ./internal/archcheck -count=1'
git diff --check
```

- [ ] 3.5 勾选并提交：

```bash
git add internal/storage/chunk_keys.go internal/storage/chunk_keys_test.go openspec/changes/natural-material-generation/tasks.md
git commit -m "feat: 枚举磁盘世界区块键"
```

---

## Task 4: 在世界锁内创建可验证的完整备份

**Files:**
- Create: `internal/storage/backup.go`
- Create: `internal/storage/backup_test.go`

- [ ] 4.1 写 RED，覆盖：完整复制 metadata/players/dimensions；跳过 `world.lock` 和 `.*.tmp-*`；目标位于源目录内部被拒绝；任意 symlink 被拒绝；既有普通目录被拒绝；同源绝对路径、seed 与版本一致的 `.mcgo-world-backup-v1.json` 允许幂等返回；取消/复制失败时源世界逐字节不变且正式目标目录不存在。
- [ ] 4.2 运行 RED：

```bash
zsh -ic 'go test ./internal/storage -run "WorldBackup" -count=1'
```

- [ ] 4.3 最小实现 `func (store *DiskStore) Backup(ctx context.Context, destination string) error`。在 `store.mu` 与既有 world lock 持有期间：规范源/目标绝对路径；拒绝内部目标和不匹配既有目标；在目标同级创建临时目录；用 `filepath.WalkDir` + `io.Copy` 复制普通文件并同步；拒绝 symlink/设备；写入并同步备份身份 JSON；同步目录后 rename 为正式目标。只删除本次明确创建的临时目录，不引入压缩包或新依赖。
- [ ] 4.4 GREEN 与 mutation：跳过身份校验时“既有任意目录”用例必须失败；恢复后运行：

```bash
gofmt -w internal/storage/backup.go internal/storage/backup_test.go
zsh -ic 'go test ./internal/storage -run "WorldBackup|WorldLock|Atomic" -race -count=1'
zsh -ic 'go test ./internal/storage -race -count=1'
git diff --check
```

- [ ] 4.5 勾选并提交：

```bash
git add internal/storage/backup.go internal/storage/backup_test.go openspec/changes/natural-material-generation/tasks.md
git commit -m "feat: 在世界锁内创建迁移备份"
```

---

## Task 5: 实现七种自然值的幂等迁移内核

**Files:**
- Create: `cmd/mcgod/material_migration.go`
- Create: `cmd/mcgod/material_migration_test.go`

- [ ] 5.1 写 RED 纯函数测试 `migrateNaturalMaterials`：旧区块混合石/土/草/沙/砾/黏土/雪、空气、矿石、箱子、熔炉、砖块；固定掉落物、熔炉和箱子负载。断言只在七种自然值处写入 `worldgen.New(seed).BaseBlockAt`，其他方块和全部负载逐值相等，输入 chunk 不变，无变化返回原 revision 候选。
- [ ] 5.2 写 RED runner 测试，使用一个仅供测试故障注入的窄接口：

```go
type materialMigrationStore interface {
	Metadata() storage.Metadata
	ChunkKeys(context.Context) ([]core.ChunkKey, error)
	LoadChunk(context.Context, core.ChunkKey) (storage.StoredChunk, error)
	SaveBatch(context.Context, []storage.ChunkSave) (storage.SaveResult, error)
	Sync(context.Context) error
	Close() error
}
```

覆盖稳定顺序、只改 Overworld、每 `32` 个扫描键更新一次进度、只保存 changed chunk 且 revision `+1`、SaveBatch/Sync/进度 rename 失败即停、部分 region 已提交后同参数重跑不重复加 revision、完成后再次运行零保存。
- [ ] 5.3 运行 RED：

```bash
zsh -ic 'go test ./cmd/mcgod -run "MaterialMigration" -count=1'
```

- [ ] 5.4 实现版本 `1` 的 JSON 状态：`Version`、`Seed`、规范化 `BackupPath`、`LastKey`、`Complete`。状态只通过同目录 temp + file Sync + Rename + directory Sync 原子写；现有状态的 seed/backup/version 不一致直接拒绝。每批先保存/Sync，再更新 `LastKey`；全部完成后再次 Sync，再写 `Complete=true`。
- [ ] 5.5 实现 runner：先 `DiskStore.Backup`，再取 `ChunkKeys`；跳过 `<=LastKey` 和非 Overworld；克隆并迁移；按 32 个扫描键形成批次；无变化键仍推进进度。不要增加反向迁移、并行 worker 或通用 migration framework。
- [ ] 5.6 GREEN、故障恢复与 mutation：删除“无变化不保存”判断时幂等 revision 用例必须失败；恢复后运行：

```bash
gofmt -w cmd/mcgod/material_migration.go cmd/mcgod/material_migration_test.go
zsh -ic 'go test ./cmd/mcgod -run "MaterialMigration" -race -count=1'
zsh -ic 'go test ./internal/storage ./internal/worldgen ./cmd/mcgod -race -count=1'
git diff --check
```

- [ ] 5.7 勾选并提交：

```bash
git add cmd/mcgod/material_migration.go cmd/mcgod/material_migration_test.go openspec/changes/natural-material-generation/tasks.md
git commit -m "feat: 可恢复地迁移旧世界自然材料"
```

---

## Task 6: 接入互斥的 mcgod 离线命令

**Files:**
- Modify: `cmd/mcgod/main.go`
- Modify: `cmd/mcgod/main_test.go`

- [ ] 6.1 写 RED：`--migrate-materials` 没有 `--backup` 拒绝；正常模式带 `--backup` 拒绝；迁移模式调用注入的 `migrateMaterials(ctx,world,backup)` 后返回，且不得加载 config、打开普通 server store、监听 TCP 或构造 host；锁占用返回 `storage.ErrWorldLocked`；缺失 `world.meta` 不创建新世界。
- [ ] 6.2 `options` 只追加 `MigrateMaterials bool` 和 `Backup string`；`dependencies` 只追加迁移函数。`run` 在 `parseOptions` 后、`resolveConfig` 前走迁移分支；正常 server 分支保持原装配顺序与日志行为。

```go
if opts.MigrateMaterials {
	dependencies := mergeDependencies(injected)
	return dependencies.migrateMaterials(ctx, opts.World, opts.Backup)
}
```

- [ ] 6.3 运行：

```bash
gofmt -w cmd/mcgod/main.go cmd/mcgod/main_test.go
zsh -ic 'go test ./cmd/mcgod -race -count=1'
zsh -ic 'CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./cmd/mcgod'
git diff --check
```

Expected: 专用服务端仍可无 CGO 构建，迁移模式绝不触碰监听器。

- [ ] 6.4 勾选并提交：

```bash
git add cmd/mcgod/main.go cmd/mcgod/main_test.go openspec/changes/natural-material-generation/tasks.md
git commit -m "feat: 增加材料离线迁移命令"
```

---

## Task 7: 真实磁盘纵向验证、归档与 PR

**Files:**
- Modify: `cmd/mcgod/material_migration_test.go`
- Modify: `openspec/changes/natural-material-generation/tasks.md`
- Sync: `openspec/specs/natural-material-generation/spec.md`
- Archive: `openspec/changes/archive/2026-08-09-natural-material-generation/**`

- [ ] 7.1 用真实 `DiskStore` 纵向测试：创建正/负区块与非 Overworld 区块，写入自然方块和 drops/furnaces/chests，关闭后运行迁移；断言备份可独立打开、源世界 changed revision 恰好 `+1`、无变化/非 Overworld revision 不变、schema v8 round-trip/future schema 拒绝保持。
- [ ] 7.2 运行完整门禁：

```bash
zsh -ic 'go test ./internal/worldgen ./internal/storage ./cmd/mcgod -race -count=1'
zsh -ic 'go test ./internal/storage -run "Future|CRC|Trunc|Trailing|Recovery|WorldBackup|MaterialMigration" -count=1'
zsh -ic 'go test ./internal/storage -run=^$ -fuzz=FuzzDecodeChunkPayload -fuzztime=10s'
zsh -ic 'go test ./internal/archcheck -count=1'
zsh -ic 'go test ./... -race'
zsh -ic 'go vet ./...'
gofmt -l .
git diff --check
openspec validate --all --strict --no-interactive
```

Expected: 全部成功，`gofmt -l .` 无输出。

- [ ] 7.3 请求独立代码评审；只修本任务范围内 finding，并重跑相称门禁。
- [ ] 7.4 同步 delta 到 `openspec/specs/natural-material-generation/spec.md`，确认 tasks 全勾后归档：

```bash
openspec archive natural-material-generation --yes
openspec validate --all --strict --no-interactive
test -z "$(openspec list --json | grep natural-material-generation || true)"
git diff --check
```

- [ ] 7.5 提交归档，推送并创建独立 PR：

```bash
git add cmd/mcgod internal/storage internal/worldgen openspec
git commit -m "docs: 归档自然材料生成与迁移"
git push -u origin codex/natural-material-generation
gh pr create --base main --head codex/natural-material-generation --title "feat: 生成并迁移自然材料" --body-file /private/tmp/natural-material-generation-pr.md
```

- [ ] 7.6 PR 描述必须列出强制迁移会覆盖玩家使用七种自然材料搭建的部分、恢复依赖完整备份、未修改协议/schema/metadata，以及真实验证命令。不要合并另外两个任务或共享发布文档。
