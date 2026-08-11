# M4O Responsibility-Oriented Code Organization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在不改变任何可观察行为的前提下，审计全仓 Go 文件，把混合职责的大文件整理为同包职责文件，并仅将完整 HUD 提取为一个新的内部包。

**Architecture:** 以现有 package 与 `internal/archcheck` 依赖方向为基线，从叶子包向命令装配层逐波迁移声明。默认只做同包拆分；唯一预批准的新包是 `internal/render/hud`，其单向依赖既有 `internal/render` 字形接口与窄内部 API `render.ItemColor`，以及 `gfx`、`assets`、`core`、`mesh`，而 `internal/render` 不反向依赖 HUD。

**Scope Decision:** 该规格横跨多个子系统；用户已明确选择一个 OpenSpec change 和一份计划、内部按包分阶段提交，而不是拆成多份独立计划。

**Tech Stack:** Go 1.26、标准库、现有内部包、WebGPU/CGO Darwin 后端、OpenSpec、Go race/fuzz/benchmark、无窗口视觉 capture、`cmd/perfcheck`。

**历史与当前基线：** Task 1–19 段落保留 `96c4aae`、386 文件、协议 v14、区块 schema v7、玩家 schema v5、7 个当时视觉 golden 与固定 hash，作为已经完成的 Task 19 历史审计证据；这些旧值不再是 Task 20 当前验收值。Task 20 单独以 `origin/main=37cdb3e0b3cd241bad1c3e70e5a25bcc9994c4fa` 为基线。

## Global Constraints

- 实施前 MUST 阅读 `AGENTS.md`、`openspec/config.yaml`、本计划、`docs/superpowers/specs/2026-08-09-repository-code-organization-design.md`，以及 active change 的 `proposal.md`、delta spec、`design.md` 和 `tasks.md`。
- Task 2–19 的历史基线是 `96c4aae`；Task 20 开始前 MUST 确认 `origin/main` 精确为 `37cdb3e0b3cd241bad1c3e70e5a25bcc9994c4fa`，并检查 `git status --short --branch`，保留用户及无关修改。
- 本工作 MUST 继续使用 active change `m4o-responsibility-oriented-code-organization`。`m4n-static-block-light` 已由 `37cdb3e` 所在主线归档；Task 20 不得复活其 active change 或篡改归档历史。
- Task 20 全仓审计范围 MUST 覆盖 `37cdb3e` 时 `cmd/` 与 `internal/` 下全部 `412` 个 Go 文件（`155` 个生产文件、`257` 个测试文件），结论固定为 36 split + 2 extract + 0 delete + 374 keep；审计不要求每个文件产生 diff。
- Task 20 MUST 保留主线已有的协议 v15、区块 schema v8、玩家 schema v6、metadata v2、已归档 M4N、common materials、damage/target、material processing、natural generation/oak、light recipe、container-Y、10 个 capture 场景与 benchmark scenario v15；这些能力不得归因于 M4O。
- storage/network fixture、10 个视觉 golden 与其他固定 artifact MUST 直接用 `git diff`/`cmp` 对比 `37cdb3e`，不得用 Task 19 旧 hash 代替；若上游未改性能 baseline，则继续保持原字节。
- 相对 `37cdb3e` 只允许 Task 2 已批准的两个新 Test 和一个 Test 重命名；其余 Test、Benchmark、Fuzz 入口 MUST 完全一致。
- benchmark 与 `perfcheck` 的性能数值及既有阈值只保存记录，不改变退出状态；只有报告结构、身份/provenance、真实 overflow、数据丢失、I/O 错误和非数值命令失败阻断。
- 行数只用于发现候选；不得新增文件行数门禁，不得为了缩短文件制造单函数碎片。
- 除 `internal/render/hud` 外不得新增内部包；若另一个候选确有独立边界，先停止并修改设计、OpenSpec 与本计划，经用户批准后再继续。
- 不得新增第三方依赖、`utils`/`common` 包、工厂、单实现接口、兼容 wrapper 或类型别名层。
- 错误值、错误文本、日志字段、GPU label、资源释放顺序、goroutine/channel/lock/buffer 所有权 MUST 保持不变。
- 声明搬迁采用 baseline green → move → green，不制造人工失败测试；只有新增或改变架构守卫时使用 red → green。
- 拆分 Darwin/CGO 文件时 MUST 保留 `//go:build darwin`；Objective-C bridge 必须留在能够直接引用 `C` 的文件中。
- Go 注释、GoDoc、测试说明和文档 MUST 使用中文；原有英文协议名、wire 字段、外部 API 与 GPU label 保持原样。
- 每项任务只提交该任务文件与对应 `openspec/.../tasks.md` checkbox；Hook 失败必须修根因，不得关闭、改写或设置豁免变量。
- 自动验证不得启动交互式客户端；视觉验证只运行既有无窗口 capture，且不得使用 `--update-golden`。
- Task 20 只允许逐声明解决只读 merge-tree 已确认的 13 个冲突；不得用 ours/theirs 整文件覆盖，不得复活拆分前旧大文件，不得放宽测试或更新 golden/baseline。

## File Structure

- Create: `openspec/changes/m4o-responsibility-oriented-code-organization/{.openspec.yaml,proposal.md,design.md,tasks.md}`
- Create: `openspec/changes/m4o-responsibility-oriented-code-organization/specs/repository-code-organization/spec.md`
- Split: `internal/archcheck/deps_test.go` → `source_guards_test.go`, `dependency_test.go`, `platform_test.go`, `helpers_test.go`
- Split: `internal/gfx/wgpu.go` → `wgpu.go`, `wgpu_convert.go`, `wgpu_pipeline.go`, `wgpu_resource.go`, `wgpu_surface.go`, `wgpu_encoder.go`
- Split: `internal/network/{message.go,codec.go,codec_test.go}` into domain and direction files in the same package
- Split: `internal/storage/{chunk_codec.go,chunk_codec_test.go}` into envelope, logical, container and primitive files
- Split: `internal/sim/engine.go` into engine, step, run, subscription, placement and change files
- Split: `internal/server/{session.go,host.go,publication.go,persistence.go,player_persistence.go}` and their large tests by responsibility
- Split: `internal/client/{mesher.go,predictor.go,predictor_test.go}` by queue/worker/advance/reconcile/presentation responsibility
- Split: `internal/render/renderer.go` by lifecycle, upload and draw responsibility
- Move/Split: `internal/render/hotbar.go`, `hotbar_test.go`, `shader/hotbar.wgsl` → `internal/render/hud/`
- Split: `cmd/mcgo/{app.go,app_test.go,main.go,capture.go,benchmark.go,multiplayer_benchmark.go}` and their large tests by responsibility
- Split: `cmd/perfcheck/{main.go,main_test.go}` by comparison, validation, threshold and CLI responsibility
- Preserve: focused files not named above after explicit package audit; no mechanical rewrite of already cohesive code
- Sync: 把 `37cdb3e` 在精确 13 个冲突路径上的上游增量迁入上述既有职责文件；不新增 `internal/render/hud` 以外的包

---

以下 Task 1–19 是已完成历史计划。其 `96c4aae`、386 文件、v14/v7/v5、旧 hash 与当时未归档 M4N 的描述仅记录执行时事实；Task 20 的当前要求以文首新基线和末尾 Task 20 为准。

### Task 1: 建立 M4O OpenSpec change 与全仓审计清单

**Files:**
- Create: `openspec/changes/m4o-responsibility-oriented-code-organization/.openspec.yaml`
- Create: `openspec/changes/m4o-responsibility-oriented-code-organization/proposal.md`
- Create: `openspec/changes/m4o-responsibility-oriented-code-organization/design.md`
- Create: `openspec/changes/m4o-responsibility-oriented-code-organization/tasks.md`
- Create: `openspec/changes/m4o-responsibility-oriented-code-organization/specs/repository-code-organization/spec.md`

**Interfaces:**
- Consumes: `docs/superpowers/specs/2026-08-09-repository-code-organization-design.md` 的全部批准决策。
- Produces: 后续 Task 2–19 的唯一 active change 与包级审计清单。

- [ ] **Step 1: 冻结基线身份和文件总数**

Run:

```bash
git status --short --branch
git rev-parse HEAD
openspec list --json
rg --files cmd internal -g '*.go' | wc -l
rg --files cmd internal -g '*.go' -g '!*_test.go' | wc -l
rg --files cmd internal -g '*_test.go' | wc -l
zsh -ic 'go version'
```

Expected: 分支为 `codex/m4o-code-organization`，HEAD 包含 `96c4aae`，工作树干净；文件数依次为 `386/144/242`；`m4n-static-block-light` 显示 complete；Go 为 `go1.26`。

- [ ] **Step 2: 写最小 OpenSpec 产物**

`.openspec.yaml` 精确写为：

```yaml
schema: spec-driven
created: 2026-08-10
```

`proposal.md` MUST 明确：全仓职责审计、同包拆分、唯一 HUD 提包、行为完全不变；非目标必须列出功能开发、协议/存档/基线变更、行数门禁、通用工具包和其他新包。

`spec.md` MUST 包含以下完整 Requirements 与可判定场景：

```markdown
### Requirement: 代码组织重构保持外部行为
#### Scenario: 重组后既有契约不漂移
- **GIVEN** 当前协议、存档、视觉与性能 fixture
- **WHEN** 完成职责化文件和包迁移
- **THEN** 所有既有测试与固定 artifact MUST 保持不变

### Requirement: 架构守卫不依赖单一源文件位置
#### Scenario: 同一职责分布到多个文件
- **GIVEN** 一个包内职责被拆到多个生产 Go 文件
- **WHEN** 运行架构守卫
- **THEN** 守卫 MUST 扫描完整职责文件集合并继续拒绝旧路径

### Requirement: 全仓 Go 文件完成职责审计
#### Scenario: 文件无需修改但职责单一
- **GIVEN** 基线中的任意生产或测试 Go 文件
- **WHEN** 完成其所属包的审计任务
- **THEN** 该文件 MUST 被判定为保留、同包拆分、提取新包或删除之一
```

`design.md` MUST 包含设计文档的包级 `386/144/242` 审计表、Task 2–19 文件映射、依赖方向、build tag/CGO 边界、行为不变量和回退规则。`tasks.md` MUST 逐项对应本计划 Task 2–19，并写出每项 focused 命令。

- [ ] **Step 3: 严格校验并提交**

Run:

```bash
openspec validate --all --strict --no-interactive
rg -n '386|144|242|internal/render/hud|协议 v14|schema v7|schema v5|metadata v2|scenario v15' openspec/changes/m4o-responsibility-oriented-code-organization
git diff --check
```

Expected: strict 全绿；冻结身份全部命中；没有实现文件 diff。

```bash
git add openspec/changes/m4o-responsibility-oriented-code-organization
git commit -m "docs: 建立 M4O 代码组织变更"
```

### Task 2: 让架构守卫支持职责文件族

**Files:**
- Delete: `internal/archcheck/deps_test.go`
- Create: `internal/archcheck/source_guards_test.go`
- Create: `internal/archcheck/dependency_test.go`
- Create: `internal/archcheck/platform_test.go`
- Create: `internal/archcheck/helpers_test.go`
- Modify: `openspec/changes/m4o-responsibility-oriented-code-organization/tasks.md`

**Interfaces:**
- Produces: `productionGoSource(t *testing.T, directory string) string`。
- Produces: `topLevelDeclarationNamesIn(t *testing.T, directory, pattern string) map[string]bool`。
- Preserves: 所有现有 archcheck 语义；mcgo 登录检查扫描完整生产包，session 所有权检查扫描 `session*.go` 文件族。

- [ ] **Step 1: 先写跨文件扫描红灯**

在 `helpers_test.go` 写：

```go
func TestProductionGoSourceScansSplitFiles(t *testing.T) {
	dir := t.TempDir()
	for name, source := range map[string]string{
		"first.go":        "package sample\nfunc firstMarker() {}\n",
		"second.go":       "package sample\nfunc secondMarker() {}\n",
		"ignored_test.go": "package sample\nfunc ignoredMarker() {}\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got := productionGoSource(t, dir)
	if !strings.Contains(got, "firstMarker") || !strings.Contains(got, "secondMarker") {
		t.Fatalf("production source missed split file: %q", got)
	}
	if strings.Contains(got, "ignoredMarker") {
		t.Fatalf("production source included test file: %q", got)
	}
}
```

Run: `zsh -ic 'go test ./internal/archcheck -run TestProductionGoSourceScansSplitFiles -count=1'`

Expected: FAIL，因为 `productionGoSource` 尚不存在。

- [ ] **Step 2: 实现最小包扫描 helper**

在 `helpers_test.go` 实现按文件名排序、跳过 `_test.go` 的读取；错误必须通过 `t.Fatalf` 保留路径。核心循环精确采用：

```go
entries, err := os.ReadDir(directory)
if err != nil {
	t.Fatalf("读取 %s: %v", directory, err)
}
slices.SortFunc(entries, func(left, right os.DirEntry) int {
	return strings.Compare(left.Name(), right.Name())
})
var source strings.Builder
for _, entry := range entries {
	name := entry.Name()
	if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
		continue
	}
	data, err := os.ReadFile(filepath.Join(directory, name))
	if err != nil {
		t.Fatalf("读取 %s: %v", name, err)
	}
	source.Write(data)
}
return source.String()
```

用相同排序/过滤规则实现 `topLevelDeclarationNamesIn`，只读取匹配 `pattern` 的生产文件并合并 `parser.ParseFile` 得到的顶层声明名。

- [ ] **Step 3: 按守卫职责搬迁既有测试**

```text
source_guards_test.go:
  TestTunableConstantsAreNotExported
  TestTunableDefaultsAreOnlyReadInTunablesFile
  TestOnlyCommandsImportConfig
  TestOnlyTCPImplementationImportsNet
  TestLegacyPlayerAuthorityMessagesAreGone
  TestMcgoUsesLoginStreamsInsteadOfAttachedServerEndpoints
  TestMcgoBenchmarkTCPPathUsesTheSharedLoginStateMachine
  TestServerProductionDoesNotDeclareLegacyAttachedWorldWrappers
  TestSessionLifecycleResponsibilitiesStayInSessionFiles

dependency_test.go:
  allowed
  TestInternalDependenciesAreOneWay

platform_test.go:
  TestOnlyGfxImportsWebGPU
  TestMCGodHasNoGraphicsDependencies

helpers_test.go:
  productionGoSource
  topLevelDeclarationNamesIn
  isTunableDefaultName
  localName
  moduleRoot
```

两个 mcgo 守卫必须调用 `productionGoSource(t, filepath.Join(moduleRoot(t), "cmd", "mcgo"))`。legacy server constructor 守卫必须扫描整个 `internal/server` 生产包，不能继续只看 `generator.go` 与 `server.go`。session 守卫必须从 `session*.go` 合并声明，并继续单独断言 `server.go` 不包含这些声明。

- [ ] **Step 4: 验证并提交**

Run:

```bash
gofmt -w internal/archcheck
zsh -ic 'go test ./internal/archcheck -race -count=1'
zsh -ic 'go test ./cmd/mcgo ./internal/server -run "LoginStreams|Session" -count=1'
gofmt -l internal/archcheck
git diff --check
```

Expected: 全绿，`gofmt -l` 无输出。

```bash
git add internal/archcheck openspec/changes/m4o-responsibility-oriented-code-organization/tasks.md
git commit -m "test: 让架构守卫覆盖职责文件族"
```

### Task 3: 拆分 WebGPU 后端文件

**Files:**
- Modify: `internal/gfx/wgpu.go`
- Create: `internal/gfx/wgpu_convert.go`
- Create: `internal/gfx/wgpu_pipeline.go`
- Create: `internal/gfx/wgpu_resource.go`
- Create: `internal/gfx/wgpu_surface.go`
- Create: `internal/gfx/wgpu_encoder.go`
- Modify: `openspec/changes/m4o-responsibility-oriented-code-organization/tasks.md`

**Interfaces:**
- Preserves: `gfx.Device`、`Surface`、资源 wrapper 与全部 WebGPU 调用签名。
- Preserves: Objective-C `metalLayerFromNSWindow` 与 `NewDevice/newDevice` 在 `wgpu.go`，所有新增文件使用 `//go:build darwin`。

- [ ] **Step 1: 记录叶子包基线**

Run:

```bash
zsh -ic 'go test ./internal/core ./internal/world ./internal/physics ./internal/mesh ./internal/assets ./internal/profile ./internal/config ./internal/logging ./internal/worldgen ./internal/gfx ./internal/gfx/shader -race -count=1'
zsh -ic 'go test ./internal/archcheck -count=1'
```

Expected: PASS。该结果同时完成未修改叶子包的保留审计。

- [ ] **Step 2: 原样移动声明**

```text
wgpu.go:
  CGO/Objective-C preamble, must, check, wgpuDevice,
  NewDevice, newDevice, CreateBuffer, CreateShaderModule,
  CreateTexture, CreateSampler, CreateCommandEncoder, Submit, Poll, Release

wgpu_convert.go:
  toBufferUsage, toTextureUsage, toShaderStage, toFormat, fromFormat,
  toBlendState, toViewDimension, toAspect, toFilter, toMipFilter,
  toAddressMode, toVertexFormat, toLoadOp

wgpu_pipeline.go:
  createBindGroupLayouts, toBindGroupLayoutEntry, createPipelineLayout,
  CreateRenderPipeline, CreateComputePipeline, CreateBindGroup,
  resolveBindGroupBufferRange

wgpu_resource.go:
  wgpuBuffer, wgpuShaderModule, wgpuRenderPipeline, wgpuComputePipeline,
  wgpuBindGroup, wgpuSampler, wgpuTextureView, wgpuTexture 及其全部方法,
  mipSize

wgpu_surface.go:
  wgpuSurface 及其全部方法

wgpu_encoder.go:
  wgpuEncoder, wgpuCommandBuffer, wgpuRenderPass, wgpuComputePass 及其全部方法
```

不得修改函数体、label、错误处理或 release 顺序。把 `wgpu.go` 顶部说明改为“本文件族是 gfx 的 WebGPU 后端”。

- [ ] **Step 3: 验证并提交**

Run:

```bash
gofmt -w internal/gfx/wgpu*.go
zsh -ic 'go test ./internal/gfx -race -count=1'
zsh -ic 'go test ./cmd/gfxspike ./cmd/mcgo -run "Headless|GPU|Render" -count=1'
zsh -ic 'go test ./internal/archcheck -count=1'
gofmt -l internal/gfx
git diff --check
```

```bash
git add internal/gfx openspec/changes/m4o-responsibility-oriented-code-organization/tasks.md
git commit -m "refactor: 按资源职责拆分 WebGPU 后端"
```

### Task 4: 按消息领域和方向拆分 network

**Files:**
- Modify: `internal/network/message.go`
- Create: `internal/network/message_command.go`
- Create: `internal/network/message_container.go`
- Create: `internal/network/message_inventory.go`
- Create: `internal/network/message_player.go`
- Create: `internal/network/message_chunk.go`
- Create: `internal/network/message_drop.go`
- Modify: `internal/network/codec.go`
- Create: `internal/network/codec_client.go`
- Create: `internal/network/codec_server.go`
- Create: `internal/network/codec_values.go`
- Delete: `internal/network/codec_test.go`
- Create: `internal/network/codec_golden_test.go`
- Create: `internal/network/codec_invalid_test.go`
- Create: `internal/network/codec_inventory_test.go`
- Create: `internal/network/codec_helpers_test.go`
- Modify: `openspec/changes/m4o-responsibility-oriented-code-organization/tasks.md`

**Interfaces:**
- Preserves: `ClientMessage`、`ServerMessage`、所有消息类型、packet ID、payload bytes、验证顺序和 `codecError` 文本。
- Produces: 仅文件职责变化；package 仍为 `network`。

- [ ] **Step 1: 冻结协议与 codec 基线**

Run:

```bash
zsh -ic 'go test ./internal/network -race -count=1'
shasum -a 256 internal/storage/testdata/chunk-v6.bin internal/storage/testdata/player-v5.bin
```

Expected: network PASS；fixture hash 记录进任务报告但不修改文件。

- [ ] **Step 2: 原样拆分消息声明**

```text
message.go: ClientMessage, ServerMessage, 仅跨领域共享验证 helper
message_command.go: PlayerInput, PlaceBlock, SelectHotbar, CraftRecipe,
  RequestChunkResync, DropSelectedItem
message_container.go: OpenContainer, MoveContainerStack, CloseContainer,
  FurnaceState, ChestState, ContainerClosed 与 container ref 校验
message_inventory.go: InventoryState, MoveInventoryStack
message_player.go: PlayerState, RemotePlayerSpawn/Despawn/States,
  CommandRejected 与 RejectReason
message_chunk.go: ForgetChunks 与区块索引边界
message_drop.go: ItemDrop, ItemDropUpserts, ItemDropRemoves 与 batch 校验
```

如果一个私有 helper 只被一个领域使用，随该领域移动；跨领域的 `finite32/finiteVec3` 留在 `message.go`。

- [ ] **Step 3: 原样拆分 codec 声明**

```text
codec.go:
  MaxSmallPayload, 共享 wire 常量/错误,
  finishEncode, checkSmallPayload, codecError
codec_client.go:
  encodeClientPacketPayload, decodeClientPacketPayload,
  validateDecodedClientWirePacket, validateClientWirePacket
codec_server.go:
  encodeServerControlPayload, decodeServerControlPayload,
  validateServerWirePacket
codec_values.go:
  ItemStack/ContainerRef/DropID 编解码,
  item drop/remote player/block changes/forget chunks 解码 helper 及其固定字节常量
```

不得合并 switch、重排 case 或改动校验时机。

- [ ] **Step 4: 按断言职责拆测试**

```text
codec_golden_test.go:
  ProtocolV1SmallPacketGolden, ProtocolV2RemotePlayerGolden,
  SmallPacketErrorCodeWireValues, SmallPacketCommandRejectedReasonWireValues
codec_inventory_test.go:
  ProtocolV11InventoryCarriesWornToolDurability,
  ProtocolV14InventoryCarriesLightBlockItem
codec_invalid_test.go:
  其余 unknown/malformed/semantic/count/block-changes 拒绝测试
codec_helpers_test.go:
  remotePlayerStatesWireFixture, blockChangesWireFixture,
  blockChangesCountFixture, mustDecodeHex, mustCodecPlayerID,
  goldenInventoryState, goldenInventoryStateHex, inventoryStateWire,
  sameClientPacket, sameServerPacket
```

- [ ] **Step 5: 验证 wire 保真并提交**

Run:

```bash
gofmt -w internal/network
zsh -ic 'go test ./internal/network -race -count=1'
zsh -ic 'go test ./internal/network -run "Golden|ProtocolV14|Malformed|Semantic" -count=1'
zsh -ic 'go test ./internal/network -run=^$ -fuzz=FuzzSmallPacketCodec -fuzztime=10s'
zsh -ic 'go test ./internal/archcheck -count=1'
gofmt -l internal/network
git diff --check
```

```bash
git add internal/network openspec/changes/m4o-responsibility-oriented-code-organization/tasks.md
git commit -m "refactor: 按消息领域拆分网络编解码"
```

### Task 5: 按 envelope 与逻辑载荷拆分区块 codec

**Files:**
- Modify: `internal/storage/chunk_codec.go`
- Create: `internal/storage/chunk_codec_logical.go`
- Create: `internal/storage/chunk_codec_container.go`
- Create: `internal/storage/chunk_codec_primitives.go`
- Delete: `internal/storage/chunk_codec_test.go`
- Create: `internal/storage/chunk_codec_envelope_test.go`
- Create: `internal/storage/chunk_codec_roundtrip_test.go`
- Create: `internal/storage/chunk_codec_helpers_test.go`
- Modify: `openspec/changes/m4o-responsibility-oriented-code-organization/tasks.md`

**Interfaces:**
- Preserves: `currentChunkSchema=7`、envelope v1、Zstd、CRC、逻辑载荷顺序、v6→v7 no-op 与全部错误文本。
- Preserves: `encodeChunkPayload`、`decodeChunkPayload` 签名。

- [ ] **Step 1: 冻结 storage golden 与 fixture**

Run:

```bash
zsh -ic 'go test ./internal/storage -race -count=1'
shasum -a 256 internal/storage/testdata/chunk-v6.bin internal/storage/testdata/chunk-v7.bin internal/storage/testdata/player-v5.bin
```

Expected: PASS；三个 hash 记录后保持不变。

- [ ] **Step 2: 原样移动 codec 声明**

```text
chunk_codec.go:
  schema/envelope 常量与 magic, decodedPayload,
  encodeChunkPayload, decodeChunkPayload, chunkFromDTO
chunk_codec_logical.go:
  encodeLogicalChunk, decodeLogicalChunk,
  appendLogicalSection, decodeContainerSnapshot, decodeKey
chunk_codec_container.go:
  furnace/chest/drop validate, append, decode 与 legacy drop decode
chunk_codec_primitives.go:
  appendU32, appendU64, byteDecoder 及全部方法, corrupt
```

压缩、CRC、字段顺序、遍历顺序和错误包装不得改变。

- [ ] **Step 3: 拆分测试并保留 helper 唯一来源**

```text
chunk_codec_envelope_test.go:
  TestFutureSchemaIsRejectedWithoutMutation,
  TestChunkPayloadRejectsMalformedEnvelope
chunk_codec_roundtrip_test.go:
  TestChunkPayloadRoundTripsDeterministically,
  TestChunkPayloadRejectsInvalidSave
chunk_codec_helpers_test.go:
  testLogicalChunk, testAppendSection, testEnvelope,
  testEnvelopeForSchema, testAppendU32, testAppendU64,
  codecFixtureChunk, setFixtureBlock
```

- [ ] **Step 4: 验证 fixture 与 fuzz 保真并提交**

Run:

```bash
gofmt -w internal/storage
zsh -ic 'go test ./internal/storage -race -count=1'
zsh -ic 'go test ./internal/storage -run "ChunkPayload|FutureSchema|Migration" -count=1'
zsh -ic 'go test ./internal/storage -run=^$ -fuzz=FuzzDecodeChunkPayload -fuzztime=10s'
shasum -a 256 internal/storage/testdata/chunk-v6.bin internal/storage/testdata/chunk-v7.bin internal/storage/testdata/player-v5.bin
zsh -ic 'go test ./internal/archcheck -count=1'
gofmt -l internal/storage
git diff --check
```

Expected: hash 与 Step 1 完全一致。

```bash
git add internal/storage openspec/changes/m4o-responsibility-oriented-code-organization/tasks.md
git commit -m "refactor: 拆分区块存档编解码职责"
```

### Task 6: 按 tick 阶段拆分模拟引擎

**Files:**
- Modify: `internal/sim/engine.go`
- Create: `internal/sim/engine_step.go`
- Create: `internal/sim/engine_run.go`
- Create: `internal/sim/engine_subscription.go`
- Create: `internal/sim/engine_placement.go`
- Create: `internal/sim/engine_changes.go`
- Modify: `openspec/changes/m4o-responsibility-oriented-code-organization/tasks.md`

**Interfaces:**
- Preserves: `Engine`、`Clock`、`NewEngine`、`Step`、`Run` 与所有查询签名。
- Preserves: tick 内命令顺序、订阅排序、区块变化 revision、权威状态和 ticker 行为。

- [ ] **Step 1: 记录完整 sim 基线**

Run: `zsh -ic 'go test ./internal/sim -race -count=1'`

Expected: PASS。`mining_test.go` 虽大但只覆盖权威采掘职责，本任务明确保留。

- [ ] **Step 2: 按方法族原样搬迁**

```text
engine.go:
  常量, Clock, sessionState, pendingChunkChanges, Engine,
  NewEngine, WorldTime, advanceWorldTime, Enqueue,
  SubmitGenerated, SubmitAcquired, TickCount,
  CloneReadyChunk, ChunkHash, ChunkInfo, takeInbox
engine_step.go:
  Step
engine_run.go:
  Run, tickerClock 及其方法
engine_subscription.go:
  RegisterObserverSession, WantsChunk, SessionWantsChunk,
  reconcileSubscriptions, wantedSnapshot, sessionWantedSnapshot,
  applyAcquired, subscriptionDistanceSquared, applyGenerated
engine_placement.go:
  executePlacement, validPlayerInput, validPlayerLook,
  finiteInputComponent, normalizeYaw, adjacentBlock,
  placementOverlapsPlayer, mapSetBlockError
engine_changes.go:
  recordChange, finishChanges, sortChunkKeys, chunkKeyLess
```

不得分解 `Step` 函数体或改变调用顺序；本任务只改变声明所在文件。

- [ ] **Step 3: 验证并提交**

Run:

```bash
gofmt -w internal/sim/engine*.go
zsh -ic 'go test ./internal/sim -race -count=1'
zsh -ic 'go test ./internal/server ./internal/client -run "Tick|Placement|Subscription|Predictor" -count=1'
zsh -ic 'go test ./internal/archcheck -count=1'
gofmt -l internal/sim
git diff --check
```

```bash
git add internal/sim openspec/changes/m4o-responsibility-oriented-code-organization/tasks.md
git commit -m "refactor: 按 tick 阶段拆分模拟引擎"
```

### Task 7: 拆分 session、Host 与发布职责

**Files:**
- Modify: `internal/server/session.go`
- Create: `internal/server/session_heartbeat.go`
- Create: `internal/server/session_registry.go`
- Create: `internal/server/session_ingress.go`
- Modify: `internal/server/host.go`
- Create: `internal/server/host_login.go`
- Create: `internal/server/host_shutdown.go`
- Modify: `internal/server/publication.go`
- Create: `internal/server/publication_delta.go`
- Create: `internal/server/publication_snapshot.go`
- Modify: `openspec/changes/m4o-responsibility-oriented-code-organization/tasks.md`

**Interfaces:**
- Preserves: `SessionSpec`、`SessionExit`、`Host`、`Server` 对外方法和全部 session generation/heartbeat/interest 语义。
- Preserves: session outbox、input capacity、登录容量与关闭顺序。

- [ ] **Step 1: 记录 server 基线**

Run: `zsh -ic 'go test ./internal/server -race -count=1'`

Expected: PASS。

- [ ] **Step 2: 原样拆分 session 文件族**

```text
session.go:
  SessionSpec, SessionExit, publication, snapshotRequest,
  session, newSession, newObserverSession, enqueue, close, shutdown,
  disconnectCodeFor, sendDisconnect, fail, closed, writeLoop
session_heartbeat.go:
  heartbeat timer/clock 类型, acceptHeartbeatReply, heartbeatLoop
session_registry.go:
  attach/detach session, attach/detach trusted observer,
  trusted observer center setter/getter, sortedSessionIDsLocked
session_ingress.go:
  incomingCommand, inputIngressBoundary, observer center value 类型,
  endpointReader, translateClientMessage,
  drainTrustedObserverCenter, enqueueIncoming, drainIncoming
```

`inputCapacity` 与 `trustedObserverSessionID` 放在唯一使用它们的职责文件；不得改变常量值。

- [ ] **Step 3: 原样拆分 Host 与 publication**

```text
host.go:
  Host, HostStats, Stats, NewHost, RunAtInputBoundary, Run, pollPlayers
host_login.go:
  pendingLoginStream, activeLogin, acceptLoop, activeLogins,
  reserve/promote/release login, AcceptStream, acquirePreLogin,
  acceptStream, bind/promote/finish pending login, hostPlayerLoadReject
host_shutdown.go:
  collectSessionExit, Shutdown, waitAcceptLoop,
  closePendingLogins, waitPendingLogins

publication.go:
  publish, publishSession, updateSessionView, publishLocalResult,
  closePublicationSessionLocked, sortedPublicationIDsLocked,
  publicationSessionLocked
publication_delta.go:
  queuedDelta, publishForget, publishDeltas, classifyDeltas,
  networkRejectReason, chunkKeyLessForPublication
publication_snapshot.go:
  queueReadyAndResync, queueSnapshot, applyForget,
  publishSnapshots, snapshotDistanceSquared
```

- [ ] **Step 4: 验证架构守卫和并发语义并提交**

Run:

```bash
gofmt -w internal/server/session*.go internal/server/host*.go internal/server/publication*.go
zsh -ic 'go test ./internal/server -race -count=1'
zsh -ic 'go test ./internal/server -run "Session|Heartbeat|Host|Publication|Interest" -count=1'
zsh -ic 'go test ./internal/archcheck -race -count=1'
gofmt -l internal/server
git diff --check
```

```bash
git add internal/server openspec/changes/m4o-responsibility-oriented-code-organization/tasks.md
git commit -m "refactor: 拆分服务端会话与发布职责"
```

### Task 8: 拆分世界持久化调度

**Files:**
- Modify: `internal/server/persistence.go`
- Create: `internal/server/persistence_worker.go`
- Create: `internal/server/persistence_metadata.go`
- Create: `internal/server/persistence_schedule.go`
- Create: `internal/server/persistence_retry.go`
- Create: `internal/server/persistence_status.go`
- Delete: `internal/server/persistence_test.go`
- Create: `internal/server/persistence_schedule_test.go`
- Create: `internal/server/persistence_retry_test.go`
- Create: `internal/server/persistence_backpressure_test.go`
- Create: `internal/server/persistence_helpers_test.go`
- Modify: `openspec/changes/m4o-responsibility-oriented-code-organization/tasks.md`

**Interfaces:**
- Preserves: `PersistenceStatus`、save job/completion/retry 类型、autosave/retry/backpressure 顺序和 snapshot 所有权。

- [ ] **Step 1: 记录持久化基线**

Run: `zsh -ic 'go test ./internal/server -run "Persistence|Save|Autosave|Retry" -race -count=1'`

Expected: PASS。

- [ ] **Step 2: 原样拆分生产声明**

```text
persistence.go:
  saveKind, saveJob, metadataSaveState, saveCompletion,
  retrySave, PersistenceStatus
persistence_worker.go:
  saveWorker, drainSaveCompletions, drainSaveCompletionsWithError,
  applySaveCompletion, applyCommittedSnapshot
persistence_metadata.go:
  applyMetadataCompletion, scheduleMetadataSave
persistence_schedule.go:
  schedulePersistence, dispatchPersistence, groupSaveJobs,
  regionKeyLess, chunkKeyLessForSave
persistence_retry.go:
  retainFailedSave, finishRetryDispatch, allocateRetryID,
  enqueueRetryCohort, ownsRetrySnapshot, dispatchDueRetries,
  pendingRetryCohort, removePendingRetryCohort,
  mergeRetrySnapshots, retryDelay, saturatingAddUint64
persistence_status.go:
  recordSaveFailure, updatePersistenceBackpressure,
  nextPersistenceBackpressure, PersistenceStatus
```

- [ ] **Step 3: 按场景拆测试**

```text
persistence_schedule_test.go:
  autosave、urgent save、group/sort、completion、budget 与 config 测试
persistence_retry_test.go:
  save failure、retry delay/cohort、partial commit、starvation 测试
persistence_backpressure_test.go:
  hysteresis、status copy、acquire backpressure 测试
persistence_helpers_test.go:
  persistenceTestStore、newPersistenceServer、dirty engine helper、
  persistenceRevisions 与所有 save assertion helper
```

- [ ] **Step 4: 验证并提交**

Run:

```bash
gofmt -w internal/server/persistence*.go
zsh -ic 'go test ./internal/server -run "Persistence|Save|Autosave|Retry" -race -count=1'
zsh -ic 'go test ./internal/server -race -count=1'
zsh -ic 'go test ./internal/archcheck -count=1'
gofmt -l internal/server
git diff --check
```

```bash
git add internal/server openspec/changes/m4o-responsibility-oriented-code-organization/tasks.md
git commit -m "refactor: 拆分世界持久化调度职责"
```

### Task 9: 拆分玩家持久化生命周期

**Files:**
- Modify: `internal/server/player_persistence.go`
- Create: `internal/server/player_persistence_snapshot.go`
- Create: `internal/server/player_persistence_completion.go`
- Create: `internal/server/player_persistence_dispatch.go`
- Delete: `internal/server/player_persistence_test.go`
- Create: `internal/server/player_persistence_lifecycle_test.go`
- Create: `internal/server/player_persistence_retry_test.go`
- Create: `internal/server/player_persistence_concurrency_test.go`
- Create: `internal/server/player_persistence_helpers_test.go`
- Modify: `internal/server/player_flush_test.go`
- Create: `internal/server/player_flush_barrier_test.go`
- Modify: `openspec/changes/m4o-responsibility-oriented-code-organization/tasks.md`

**Interfaces:**
- Preserves: `playerPersistence`、`cachedPlayer`、Prepare/Activate/Confirm/Abort/Deactivate/Observe/Poll/Flush/CloseWorker 行为和 save revision 身份。
- Reuses: 既有 `player_flush.go` 与 `player_save_scheduler.go`；不得再建立第二套 scheduler。

- [ ] **Step 1: 记录玩家持久化基线**

Run: `zsh -ic 'go test ./internal/server -run "PlayerPersistence|PlayerSave|PlayerFlush" -race -count=1'`

Expected: PASS。

- [ ] **Step 2: 原样拆分生产声明**

```text
player_persistence.go:
  errors/constants, playerPersistence, cachedPlayer,
  playerSaveJob/completion/identity, newPlayerPersistence,
  Prepare, Activate, Confirm, Abort, Deactivate, Observe, Poll, CloseWorker
player_persistence_snapshot.go:
  cachedPlayer.restore, cachedPlayerFromStored, newMissingCachedPlayer,
  cachedPlayer.save, matchesSave, clonePlayerSave,
  clonePlayerSnapshot, playerSnapshotsEqual
player_persistence_completion.go:
  drainCompletionsLocked, drainInheritedCompletions,
  applyCompletionBatchLocked, applyCompletionBatchWithDispatchLocked,
  applyCompletionLocked, applyCompletionWithDispatchLocked
player_persistence_dispatch.go:
  dispatchLocked, evictCleanLocked, hasInFlightLocked,
  hasDirtyOrInFlightLocked, sortedPlayersLocked, cachedPlayer.evictable
```

- [ ] **Step 3: 按生命周期拆测试**

```text
player_persistence_lifecycle_test.go:
  Prepare/Activate/Confirm/Abort/Deactivate/Observe 的身份与快照测试
player_persistence_retry_test.go:
  coalesce、completion batch、retry/backoff、alias 与 eviction 测试
player_flush_test.go:
  保留既有文件中的基础 Flush 测试
player_flush_barrier_test.go:
  原 player_persistence_test.go 中 Flush、inherited batch、barrier 测试
player_persistence_concurrency_test.go:
  CloseWorker、mutex、并发 Prepare/Abort 测试
player_persistence_helpers_test.go:
  controllablePlayerStore、配置/身份/快照 fixture、poll/receive/assert helper
```

- [ ] **Step 4: 验证并提交**

Run:

```bash
gofmt -w internal/server/player_persistence*.go internal/server/player_flush*.go
zsh -ic 'go test ./internal/server -run "PlayerPersistence|PlayerSave|PlayerFlush" -race -count=1'
zsh -ic 'go test ./internal/server -race -count=1'
zsh -ic 'go test ./internal/archcheck -count=1'
gofmt -l internal/server
git diff --check
```

```bash
git add internal/server openspec/changes/m4o-responsibility-oriented-code-organization/tasks.md
git commit -m "refactor: 拆分玩家持久化生命周期"
```

### Task 10: 按业务场景拆分服务端大型集成测试

**Files:**
- Delete: `internal/server/tcp_integration_test.go`
- Create: `internal/server/tcp_integration_helpers_test.go`
- Create: `internal/server/tcp_restart_integration_test.go`
- Create: `internal/server/transport_parity_integration_test.go`
- Create: `internal/server/furnace_tcp_integration_test.go`
- Delete: `internal/server/host_test.go`
- Create: `internal/server/host_capacity_test.go`
- Create: `internal/server/host_lifecycle_test.go`
- Create: `internal/server/host_test_helpers_test.go`
- Delete: `internal/server/multiplayer_tcp_integration_test.go`
- Create: `internal/server/multiplayer_tcp_gameplay_test.go`
- Create: `internal/server/multiplayer_tcp_capacity_test.go`
- Modify: `internal/server/multiplayer_memory_integration_test.go`
- Create: `internal/server/multiplayer_memory_mining_test.go`
- Create: `internal/server/multiplayer_memory_cancel_test.go`
- Modify: `openspec/changes/m4o-responsibility-oriented-code-organization/tasks.md`

**Interfaces:**
- Preserves: 所有测试名、transport 脚本、timeout、fixture、hash、revision 与断言。
- Produces: 同包唯一 helper 来源；不导出生产 API，不创建 `testutil` 包。

- [ ] **Step 1: 记录完整 server race 基线**

Run: `zsh -ic 'go test ./internal/server -race -count=1'`

Expected: PASS。

- [ ] **Step 2: 拆分 TCP 集成测试**

```text
tcp_integration_helpers_test.go:
  integrationHost/integrationClient/generator 类型,
  start/dial/wait/send/assert 通用 helper
tcp_restart_integration_test.go:
  disconnect/restart、crafting schema、failure matrix、restore fallback、
  save recovery 测试及专属磁盘 fixture helper
transport_parity_integration_test.go:
  Memory/TCP business parity、mining convergence、mutation oracle、
  mining/parity transcript 类型与 helper
furnace_tcp_integration_test.go:
  TestFurnaceSharedByTwoPlayersOverTCP 与 furnace/rejection wait helper
```

- [ ] **Step 3: 拆分 Host 与多人测试**

```text
host_capacity_test.go:
  login capacity、duplicate identity/name、slot/session ID、pre-login bound 测试
host_lifecycle_test.go:
  cleanup、disconnect persistence、autosave、listener、run cancellation、shutdown 测试
host_test_helpers_test.go:
  testLogin、Host/store/listener/endpoint helper 与所有 wait/assert helper

multiplayer_tcp_gameplay_test.go:
  move/edit/despawn 与 drop convergence 测试、gameplay client helper
multiplayer_tcp_capacity_test.go:
  eight-client soak、concurrent login failure/cancel/claim 测试与 collector helper
multiplayer_memory_integration_test.go:
  保留 2000 tick deterministic soak 与共用 script/store 类型
multiplayer_memory_mining_test.go:
  TestMultiplayerMemoryTCPMiningCompetition 与专属 helper
multiplayer_memory_cancel_test.go:
  canceled packet accept 测试与 worker helper
```

- [ ] **Step 4: 验证测试清单未丢失并提交**

Run:

```bash
gofmt -w internal/server/*_test.go
zsh -ic 'go test ./internal/server -race -count=1'
zsh -ic 'go test ./internal/server -run "TCPPlayer|Parity|Mining|Furnace|Host|Multiplayer" -count=1'
zsh -ic 'go test ./internal/archcheck -count=1'
git diff --check
```

对比拆分前后的测试名：

```bash
git grep -h '^func Test' HEAD^ -- internal/server | sed 's/(.*//' | sort > /private/tmp/m4o-server-tests-before.txt
rg '^func Test' internal/server -g '*_test.go' | sed 's/.*func /func /; s/(.*//' | sort > /private/tmp/m4o-server-tests-after.txt
diff -u /private/tmp/m4o-server-tests-before.txt /private/tmp/m4o-server-tests-after.txt
```

Expected: `diff` 无输出。

```bash
git add internal/server openspec/changes/m4o-responsibility-oriented-code-organization/tasks.md
git commit -m "test: 按业务场景整理服务端集成测试"
```

### Task 11: 拆分客户端 mesher 与 predictor

**Files:**
- Modify: `internal/client/mesher.go`
- Create: `internal/client/mesher_worker.go`
- Create: `internal/client/mesher_queue.go`
- Modify: `internal/client/predictor.go`
- Create: `internal/client/predictor_advance.go`
- Create: `internal/client/predictor_reconcile.go`
- Create: `internal/client/predictor_presentation.go`
- Delete: `internal/client/predictor_test.go`
- Create: `internal/client/predictor_advance_test.go`
- Create: `internal/client/predictor_reconcile_test.go`
- Create: `internal/client/predictor_presentation_test.go`
- Create: `internal/client/predictor_helpers_test.go`
- Modify: `openspec/changes/m4o-responsibility-oriented-code-organization/tasks.md`

**Interfaces:**
- Preserves: `Mesher`、`Predictor` 全部导出方法、队列上限、worker scratch、fixed-step 与 reconciliation 顺序。
- Reuses: 既有 `mesher_ready_queue.go`，不得建立第二套 heap/queue。

- [ ] **Step 1: 记录 client 基线**

Run: `zsh -ic 'go test ./internal/client -race -count=1'`

Expected: PASS。

- [ ] **Step 2: 原样拆分 mesher**

```text
mesher.go:
  constants, ChunkStamp, MeshedSection, MesherStats,
  mesherJob/result, Mesher, NewMesher, MarkDirty, ForgetChunk,
  Schedule, Drain, Stats, test hooks, Close
mesher_worker.go:
  work, handle, cloneNeighborhood, stampsMatch
mesher_queue.go:
  markDirtyLocked, enqueueReadyLocked, removeQueued
```

- [ ] **Step 3: 原样拆分 predictor**

```text
predictor.go:
  constants, Control, ReconcileResult, predictedInput, Predictor,
  NewPredictor, Begin, State, Health, HistoryLen, Suspended
predictor_advance.go:
  Advance, sendNeutral, dropAcknowledged, validateControl, finiteFloat32
predictor_reconcile.go:
  ApplyPlayerState, clearForNotReady, validatePlayerState
predictor_presentation.go:
  PresentationPosition, presentationPositionNoAdvance, interpolatedPosition
```

- [ ] **Step 4: 拆分 predictor 测试**

```text
predictor_advance_test.go:
  Begin、control、fixed-step、send failure、unknown boundary、suspension/resume 测试
predictor_reconcile_test.go:
  ApplyPlayerState、health、stale tick、reset/dimension、atomic reject 测试
predictor_presentation_test.go:
  correction、ack continuity、interpolation、snap threshold 测试
predictor_helpers_test.go:
  loadedAirSource、flatClientWorld、ready/authority/advance/assert/clone helper
```

- [ ] **Step 5: 验证并提交**

Run:

```bash
gofmt -w internal/client/mesher*.go internal/client/predictor*.go
zsh -ic 'go test ./internal/client -race -count=1'
zsh -ic 'go test ./internal/client -run "Mesher|Predictor|Reconcile|Presentation" -count=1'
zsh -ic 'go test ./internal/mesh ./internal/render -run "Mesh|Light|Render" -count=1'
zsh -ic 'go test ./internal/archcheck -count=1'
gofmt -l internal/client
git diff --check
```

```bash
git add internal/client openspec/changes/m4o-responsibility-oriented-code-organization/tasks.md
git commit -m "refactor: 拆分客户端网格与预测职责"
```

### Task 12: 拆分核心 Renderer 的上传与绘制职责

**Files:**
- Modify: `internal/render/renderer.go`
- Create: `internal/render/renderer_upload.go`
- Create: `internal/render/renderer_draw.go`
- Modify: `openspec/changes/m4o-responsibility-oriented-code-organization/tasks.md`

**Interfaces:**
- Preserves: `Renderer`、`Camera`、`FrameStats`、`New` 和全部资源/绘制方法。
- Preserves: upload budget、slot origin、candidate face、render pass 和 release 顺序。

- [ ] **Step 1: 记录 render 基线**

Run: `zsh -ic 'go test ./internal/render -race -count=1'`

Expected: PASS。`debug_panel.go`、`font_atlas.go`、`hiz.go` 等文件各自职责单一，审计结论为保留。

- [ ] **Step 2: 原样移动 Renderer 方法族**

```text
renderer.go:
  shader embeds, Camera, FrameStats, sectionSlot, Renderer,
  New, newRenderer, BeginFrame, UploadBudget, PendingUploads,
  LastFrameStats, Release
renderer_upload.go:
  QueueSection, SetConnectivity, Resize, FlushUploads,
  sectionDistance2, uploadOne, takeOrigin, releaseOrigin,
  DropSection, DropOutside, abs32Render
renderer_draw.go:
  Render, cameraStable, cameraSection,
  writeCameraBytes, writeSkyCameraBytes,
  uint32sToBytes, int32sToBytes, uint64sToBytes
```

- [ ] **Step 3: 验证零分配与绘制测试并提交**

Run:

```bash
gofmt -w internal/render/renderer*.go
zsh -ic 'go test ./internal/render -race -count=1'
zsh -ic 'go test ./internal/render -run "Renderer|Upload|Render|Allocation" -count=1'
zsh -ic 'go test ./internal/archcheck -count=1'
gofmt -l internal/render
git diff --check
```

```bash
git add internal/render openspec/changes/m4o-responsibility-oriented-code-organization/tasks.md
git commit -m "refactor: 拆分渲染上传与绘制职责"
```

### Task 13: 把完整 HUD 提取为独立内部包

**Files:**
- Delete: `internal/render/hotbar.go`
- Delete: `internal/render/hotbar_test.go`
- Delete: `internal/render/shader/hotbar.wgsl`
- Modify: `internal/render/drop.go`
- Modify: `internal/render/drop_test.go`
- Modify: `internal/render/daylight_test.go`
- Create: `internal/render/hud/renderer.go`
- Create: `internal/render/hud/layout.go`
- Create: `internal/render/hud/container.go`
- Create: `internal/render/hud/health.go`
- Create: `internal/render/hud/atlas.go`
- Create: `internal/render/hud/encode.go`
- Create: `internal/render/hud/shader/hotbar.wgsl`
- Create: `internal/render/hud/layout_test.go`
- Create: `internal/render/hud/container_test.go`
- Create: `internal/render/hud/health_test.go`
- Create: `internal/render/hud/atlas_test.go`
- Create: `internal/render/hud/renderer_test.go`
- Create: `internal/render/hud/helpers_test.go`
- Modify: `cmd/mcgo/app.go`
- Modify: `cmd/mcgo/app_test.go`
- Modify: `internal/archcheck/dependency_test.go`
- Modify: `openspec/changes/m4o-responsibility-oriented-code-organization/tasks.md`

**Interfaces:**
- Produces: package `minecraft-go/internal/render/hud`。
- Produces narrow internal API: `render.ItemColor`，唯一实现归属 `internal/render/drop.go`，颜色值不变。
- Produces unchanged names: `HotbarRenderer`、`NewHotbarRenderer`、`MiningOverlay`、`HealthOverlay`、`FurnaceOverlay`、`ChestOverlay`、`InventorySlotAt`、`FurnaceSlotAt`、`ChestSlotAt`、`RecipeButtonAt`。
- Consumes: `render.GlyphSource`、`render.UploadBudget`、`render.ItemColor`、`gfx`、`assets`、`core`、`mesh`。

- [ ] **Step 1: 先提升唯一共享颜色实现**

把 `hotbarItemColor` 的实现从 `hotbar.go` 移到 `internal/render/drop.go` 并导出为 `render.ItemColor`。`itemDropColor` 与迁移后的 HUD 都直接调用该实现；`drop_test.go` 改为断言 `ItemColor`，颜色值与覆盖范围不变。不得保留旧函数、wrapper、alias、callback/config 或第二份实现。

- [ ] **Step 2: 让 archcheck 认识唯一新包**

在 `allowed` 增加精确条目：

```go
"internal/render/hud": {
	"internal/core", "internal/mesh", "internal/assets",
	"internal/render", "internal/gfx",
},
```

不要给 `internal/render` 增加 `internal/render/hud` 依赖。

- [ ] **Step 3: 原样移动 HUD 生产声明**

所有新文件 package 为 `hud`，把 `GlyphSource`、`Glyph`、`UploadBudget` 引用加 `render.` 前缀，并把原 `hotbarItemColor` 调用改为 `render.ItemColor`：

```text
renderer.go:
  shader embed, HotbarRenderer, NewHotbarRenderer,
  hotbarPipelineDesc, Prepare, Render, Release
layout.go:
  shared capacity/layout constants, hotbarInstance, hotbarLayout,
  layoutInventory, appendInventoryPanel, appendItemTile,
  appendDurabilityBar*, MiningOverlay, appendMiningBar,
  inventorySlotOrigin, hudScale, InventorySlotAt,
  appendHotbarCount*；物品颜色只调用 render.ItemColor
container.go:
  recipe IDs/constants, FurnaceOverlay/ChestOverlay,
  appendFurnaceRow, appendChestGrid, appendRecipeRows,
  所有 container/recipe origin 与 hit-test 函数
health.go:
  HealthOverlay, health constants, appendHealthBar,
  paintHotbarHeart, hotbarHeartPixel
atlas.go:
  HUD texture constants, buildHotbarTextureAtlas,
  copyHotbarTextureCell, hotbarTextureUV, hotbarItemUV
encode.go:
  hotbar upload offsets/bytes,
  encodeHotbarViewport, encodeHotbarInstances
```

HUD 生产包使用 `//go:embed shader/hotbar.wgsl`，文件内容必须与移动前字节相同。

`hud/layout.go` 不拥有或复制颜色实现；依赖保持 `hud -> render`，`internal/render` 不得导入 HUD。

- [ ] **Step 4: 迁移调用方和测试**

`cmd/mcgo/app.go` 与 `app_test.go` 把上述 HUD 类型/函数从 `render.` 改为 `hud.`，字形接口继续使用 `render.GlyphSource`。

测试映射：

```text
layout_test.go: hotbar/inventory/mining/durability/layout/hit-test 测试
container_test.go: recipe/furnace/chest layout 与 hit-test 测试
health_test.go: health overlay 测试
atlas_test.go: item face/color/texture atlas 测试
renderer_test.go: upload/draw/release/allocation/headless blend 测试
helpers_test.go: inventory/container fixture 与 HUD 专用最小 gfx/glyph fake
```

不得从 `internal/render` 导出仅供测试使用的 helper；HUD 测试自行持有最小 fake。

`drop_test.go` 保留全部测试名，并改为通过唯一实现 `render.ItemColor` 验证掉落物与 HUD 的程序化颜色一致。

`daylight_test.go` 保留 `TestScreenSpaceRenderersIgnoreWorldDaylight` 名称以及既有 name tag/hotbar 断言。删除生产包原 `hotbarShader` 后，在该测试文件增加最小 embed import、test-only `//go:embed hud/shader/hotbar.wgsl` 与测试变量，读取移动后的同一份唯一 shader；不得复制 shader 字节、导出生产 helper、让 `internal/render` 生产包 import HUD，或保留生产 `hotbarShader` wrapper。

- [ ] **Step 5: 验证 shader、颜色、视觉与依赖方向并提交**

先记录旧 shader hash，再比较移动后 hash：

```bash
git show HEAD:internal/render/shader/hotbar.wgsl | shasum -a 256
shasum -a 256 internal/render/hud/shader/hotbar.wgsl
```

Run:

```bash
gofmt -w internal/render/drop.go internal/render/drop_test.go internal/render/daylight_test.go internal/render/hud cmd/mcgo/app.go cmd/mcgo/app_test.go internal/archcheck
zsh -ic 'go test ./internal/render ./internal/render/hud ./cmd/mcgo -race -count=1'
zsh -ic 'go test ./internal/render -run "ItemDropColors|ItemDropColor|ScreenSpaceRenderersIgnoreWorldDaylight" -count=1'
zsh -ic 'go test ./internal/render/hud -run "Hotbar|Inventory|Furnace|Chest|Health|Recipe" -count=1'
zsh -ic 'go test ./internal/archcheck -race -count=1'
VISUAL_OUT=/private/tmp/mcgo-m4o-hud-visual make visual-check
gofmt -l internal/render cmd/mcgo internal/archcheck
git diff --check
```

Expected: `render.ItemColor` 是唯一颜色实现，`hud -> render` 单向且 `render` 生产包不依赖 `hud`；`TestScreenSpaceRenderersIgnoreWorldDaylight` 名称及 name tag/hotbar 断言不变，daylight 测试只 embed 移动后的唯一 shader；两个 shader hash 相同，颜色与 visual golden 不变；visual-check 只比较、不更新 golden。

```bash
git add internal/render cmd/mcgo/app.go cmd/mcgo/app_test.go internal/archcheck openspec/changes/m4o-responsibility-oriented-code-organization/tasks.md
git commit -m "refactor: 提取独立 HUD 渲染包"
```

### Task 14: 按应用生命周期拆分 mcgo 生产装配

**Files:**
- Modify: `cmd/mcgo/app.go`
- Create: `cmd/mcgo/app_dependencies.go`
- Create: `cmd/mcgo/app_metrics.go`
- Create: `cmd/mcgo/app_startup.go`
- Create: `cmd/mcgo/app_lifecycle.go`
- Create: `cmd/mcgo/app_frame.go`
- Create: `cmd/mcgo/app_messages.go`
- Create: `cmd/mcgo/app_input.go`
- Create: `cmd/mcgo/app_render.go`
- Modify: `openspec/changes/m4o-responsibility-oriented-code-organization/tasks.md`

**Interfaces:**
- Preserves: `applicationOptions`、`application`、dependency injection、启动清理、帧顺序、消息处理、输入和资源释放语义。
- Requires: 每个新增文件首行均为 `//go:build darwin`。

- [ ] **Step 1: 记录完整 mcgo 基线**

Run: `zsh -ic 'go test ./cmd/mcgo -race -count=1'`

Expected: PASS。

- [ ] **Step 2: 原样移动类型和启动/生命周期声明**

```text
app.go:
  applicationOptions, application, applicationWindow, applicationHost
app_dependencies.go:
  applicationDependencies, defaultApplicationDependencies
app_metrics.go:
  tickRecorder, saveRecorder 及其全部方法,
  newPerformanceRecorders
app_startup.go:
  openApplicationStore, newApplication, newApplicationWithDependencies,
  assembleBenchmarkObserverConnection, applicationLoginResult,
  assembleLocalApplicationConnection, cleanupLocalApplicationStartup,
  shutdownApplicationHost, ignoreApplicationStartupCloseError,
  fitFramebuffer, releaseRemoteConstructionResources
app_lifecycle.go:
  Close, releaseOwnedResources, closeClientSession
```

- [ ] **Step 3: 原样移动帧、消息、输入和渲染声明**

```text
app_frame.go:
  updateCenter, requestTrustedObserverCenter, nextSequence,
  frame, renderFrame
app_messages.go:
  drainServerMessages
app_input.go:
  dropSelectedItem, placeBlock, containerOpen,
  setInventoryOpen, clearContainerUI, clickInventorySlot,
  selectHotbarSlot, send
app_render.go:
  remoteRenderPresentations, remoteRenderPresentationsSortedInto,
  framebufferLabel, framebufferSize, cameraChunk,
  depthTarget/newDepthTarget/Release, appendItemDropInstances
```

不得调整方法调用顺序或把现有参数收拢成新 config 类型。

- [ ] **Step 4: 验证架构守卫不再依赖 app.go 并提交**

Run:

```bash
gofmt -w cmd/mcgo/app*.go
zsh -ic 'go test ./cmd/mcgo -race -count=1'
zsh -ic 'go test ./cmd/mcgo -run "Application|Frame|Connection|Inventory|Render" -count=1'
zsh -ic 'go test ./internal/archcheck -race -count=1'
gofmt -l cmd/mcgo
git diff --check
```

```bash
git add cmd/mcgo openspec/changes/m4o-responsibility-oriented-code-organization/tasks.md
git commit -m "refactor: 按生命周期拆分客户端应用装配"
```

### Task 15: 按场景拆分 mcgo 应用测试

**Files:**
- Delete: `cmd/mcgo/app_test.go`
- Create: `cmd/mcgo/app_protocol_test.go`
- Create: `cmd/mcgo/app_render_test.go`
- Create: `cmd/mcgo/app_connection_test.go`
- Create: `cmd/mcgo/app_input_test.go`
- Create: `cmd/mcgo/app_celestial_test.go`
- Create: `cmd/mcgo/app_test_helpers_test.go`
- Modify: `openspec/changes/m4o-responsibility-oriented-code-organization/tasks.md`

**Interfaces:**
- Preserves: 全部测试名、fake 行为、timeout、消息顺序与资源 release 断言。
- Requires: 所有新测试文件使用 `//go:build darwin` 和 `package main`。

- [ ] **Step 1: 原样按场景移动测试**

```text
app_protocol_test.go:
  performance recorder、remote roster/protocol/close、frame drain 测试,
  newRemoteProtocolApplication、remoteSpawn、integrationPlayerID
app_render_test.go:
  render pass、glyph error、construction release、HUD/drop/sky/camera 测试,
  integrationGlyphSource 与 integrationRenderDevice 资源 fake
app_connection_test.go:
  benchmark dial、remote/local assembly、startup cleanup、Close、
  runInteractive disconnect 测试与 connection fake
app_input_test.go:
  drained input、mining overlay、selection/place/drop、inventory、
  furnace/chest/crafting/cursor 测试与交互 helper
app_celestial_test.go:
  celestial world-time 与 Memory/TCP 一致性测试及 endpoint helper
app_test_helpers_test.go:
  仅跨两个以上场景文件共享且无法归属单一场景的最小 helper
```

先移动专属 helper，再判断是否仍需共享文件；不得把所有 fake 聚合成新的巨型 helper。

- [ ] **Step 2: 验证测试名和行为完全一致**

```bash
git grep -h '^func Test' HEAD^ -- cmd/mcgo/app_test.go | sed 's/(.*//' | sort > /private/tmp/m4o-app-tests-before.txt
rg '^func Test' cmd/mcgo/app_*_test.go | sed 's/.*func /func /; s/(.*//' | sort > /private/tmp/m4o-app-tests-after.txt
diff -u /private/tmp/m4o-app-tests-before.txt /private/tmp/m4o-app-tests-after.txt
gofmt -w cmd/mcgo/app_*_test.go
zsh -ic 'go test ./cmd/mcgo -race -count=1'
zsh -ic 'go test ./internal/archcheck -count=1'
gofmt -l cmd/mcgo
git diff --check
```

Expected: 测试名 diff 无输出，全部测试通过。

- [ ] **Step 3: 提交**

```bash
git add cmd/mcgo openspec/changes/m4o-responsibility-oriented-code-organization/tasks.md
git commit -m "test: 按应用场景整理客户端测试"
```

### Task 16: 拆分 mcgo 入口、交互与视觉 capture

**Files:**
- Modify: `cmd/mcgo/main.go`
- Create: `cmd/mcgo/options.go`
- Create: `cmd/mcgo/interactive.go`
- Delete: `cmd/mcgo/main_test.go`
- Create: `cmd/mcgo/options_test.go`
- Create: `cmd/mcgo/run_test.go`
- Create: `cmd/mcgo/interactive_test.go`
- Modify: `cmd/mcgo/capture.go`
- Create: `cmd/mcgo/capture_scene.go`
- Create: `cmd/mcgo/capture_image.go`
- Delete: `cmd/mcgo/capture_test.go`
- Create: `cmd/mcgo/capture_scene_test.go`
- Create: `cmd/mcgo/capture_image_test.go`
- Modify: `openspec/changes/m4o-responsibility-oriented-code-organization/tasks.md`

**Interfaces:**
- Preserves: CLI flags、config/profile 行为、交互输入顺序、capture 场景顺序、阈值和 PNG 编解码。
- Requires: 从 `main.go`/`main_test.go` 拆出的文件保留 `//go:build darwin`；从当前无 build tag 的 `capture.go`/`capture_test.go` 拆出的文件继续不带 build tag。

- [ ] **Step 1: 原样拆分 main 入口**

```text
main.go:
  steadyFrameMeshWorkMax, init, runDependencies,
  run, runWithDependencies, loadApplicationIdentity,
  clientMemoryLimit, main
options.go:
  mainOptions, parseMainOptions, resolveConfigPath,
  resolveConfig, remoteTuningDiverges
interactive.go:
  runInteractive, applyInteractiveCursorInput,
  pressedHotbarNumber, applyInteractiveInput
```

测试映射：

```text
options_test.go:
  parse options、capture/benchmark/dev/config 冲突与默认值测试
run_test.go:
  profile load/bypass、benchmark/capture config、remote tuning 测试
interactive_test.go:
  glyph error、mining gate、cursor suppression 测试与 oneFrameInteractiveWindow
```

- [ ] **Step 2: 原样拆分 capture**

```text
capture.go:
  captureScene, captureScenes, runCapture, captureOne,
  capturePinnedServerTick, captureDrainMax, captureGlyphSettleFrames,
  captureSettleTimeout, captureSettled
capture_scene.go:
  prepareSkylightTunnel, prepareCaptureAirNeighborhood,
  prepareBlockLightRoom, applyCaptureBlockLightRoomChanges,
  applyCaptureMirror
capture_image.go:
  captureGoldenDir, captureThresholds, compareAgainstGolden,
  readPNG, bgraToNRGBA, writePNG

capture_scene_test.go:
  scene 顺序、fixture、mirror/mesher、settle/reset/error 测试
capture_image_test.go:
  BGRA 转换、golden compare、PNG roundtrip/error 测试与 solidColorImage
```

- [ ] **Step 3: 验证 CLI 与视觉保真并提交**

Run:

```bash
gofmt -w cmd/mcgo/main*.go cmd/mcgo/options*.go cmd/mcgo/interactive*.go cmd/mcgo/capture*.go
zsh -ic 'go test ./cmd/mcgo -race -count=1'
zsh -ic 'go test ./cmd/mcgo -run "ParseMainOptions|RunWithDependencies|Interactive|Capture|Golden|PNG" -count=1'
zsh -ic 'go test ./internal/archcheck -count=1'
VISUAL_OUT=/private/tmp/mcgo-m4o-capture-visual make visual-check
gofmt -l cmd/mcgo
git diff --check
```

```bash
git add cmd/mcgo openspec/changes/m4o-responsibility-oriented-code-organization/tasks.md
git commit -m "refactor: 拆分客户端入口与视觉捕获职责"
```

### Task 17: 拆分 benchmark 场景、测量与报告职责

**Files:**
- Modify: `cmd/mcgo/benchmark.go`
- Create: `cmd/mcgo/benchmark_measure.go`
- Create: `cmd/mcgo/benchmark_report.go`
- Modify: `cmd/mcgo/multiplayer_benchmark.go`
- Create: `cmd/mcgo/multiplayer_benchmark_transport.go`
- Create: `cmd/mcgo/multiplayer_benchmark_server.go`
- Delete: `cmd/mcgo/benchmark_v6_test.go`
- Delete: `cmd/mcgo/multiplayer_benchmark_test.go`
- Create: `cmd/mcgo/benchmark_scenario_test.go`
- Create: `cmd/mcgo/benchmark_server_test.go`
- Create: `cmd/mcgo/benchmark_report_test.go`
- Create: `cmd/mcgo/benchmark_helpers_test.go`
- Modify: `openspec/changes/m4o-responsibility-oriented-code-organization/tasks.md`

**Interfaces:**
- Preserves: scenario v15、固定 workload、sample counts、阈值数值、性能仅记录语义、报告 JSON 与原子写入顺序。
- Preserves: M2/M5 baseline 文件字节；本任务不运行 producer、不更新 baseline。

- [ ] **Step 1: 记录 benchmark 单测与微基准基线**

Run:

```bash
zsh -ic 'go test ./cmd/mcgo -run "Scenario|Benchmark|PerformanceThresholds" -race -count=1'
zsh -ic 'go test ./internal/render ./internal/server -run ^$ -bench "RemoteAvatarNameTag|EightPlayerInterest" -benchmem -count=3'
```

Expected: PASS；benchmark 数值记录到任务报告，不修改阈值。

- [ ] **Step 2: 原样拆分单机 benchmark**

```text
benchmark.go:
  scenario constants, runBenchmarkCooldown, printMemoryBreakdown,
  benchmarkReportSkeleton, gpuCompletionMinSamples, runBenchmark
benchmark_measure.go:
  measureGPUCompletionAfterTransportClose, measureProtocolSummary,
  measurePlayerPersistenceSummary, durationP99, waitUntilLoaded,
  waitForBenchmarkCenterConsistency, runWarmup, measurePhase,
  hardwareID, osID, commandOutput
benchmark_report.go:
  writeBenchmarkReport, report FS interfaces/implementation,
  writeBenchmarkReportWithFS, writeSyncedBenchmarkTemp,
  writeBenchmarkReportBytes, syncBenchmarkReportDirectory,
  rollbackBenchmarkReport, validateBenchmarkReport,
  benchmarkPerformanceRecords
```

- [ ] **Step 3: 原样拆分多人 benchmark**

```text
multiplayer_benchmark.go:
  render timing, scenario, benchmarkPlayerID,
  multiplayerClientProbe, sampleFrame, statesNearCamera,
  billboard camera, GPU completion, Summary
multiplayer_benchmark_transport.go:
  canonicalCountingServerStream, uvarintBytes,
  multiplayerServerClient, sendMultiplayerBenchmarkInputs
multiplayer_benchmark_server.go:
  benchmarkServerWindowSummary, formatTickBoundaryOverrun,
  benchmarkServerInputDeadline, runBenchmarkServerMeasuredWindow,
  measureMultiplayerServerProbe, validBenchmarkServerProbe
```

- [ ] **Step 4: 按契约拆 benchmark 测试**

```text
benchmark_scenario_test.go:
  scenario v15、GPU teardown、render sample、celestial sky 测试
benchmark_server_test.go:
  measured window、deadline、counting stream、server probe、
  formatTickBoundaryOverrun 测试
benchmark_report_test.go:
  threshold、write、validation、sample completeness 测试
benchmark_helpers_test.go:
  benchmark stream/endpoint fake、readFloat32、
  validMultiplayerSummary、completeBenchmarkReport
```

保留 `benchmark_v5_test.go` 作为历史格式契约，不改名、不改内容。

- [ ] **Step 5: 验证 workload 和报告契约并提交**

Run:

```bash
gofmt -w cmd/mcgo/benchmark*.go cmd/mcgo/multiplayer_benchmark*.go
zsh -ic 'go test ./cmd/mcgo -race -count=1'
zsh -ic 'go test ./cmd/mcgo -run "ScenarioV15|BenchmarkServer|BenchmarkReport|PerformanceThresholds" -count=1'
zsh -ic 'go test ./internal/render ./internal/server -run ^$ -bench "RemoteAvatarNameTag|EightPlayerInterest" -benchmem -count=3'
zsh -ic 'go test ./internal/archcheck -count=1'
gofmt -l cmd/mcgo
git diff --check
```

```bash
git add cmd/mcgo openspec/changes/m4o-responsibility-oriented-code-organization/tasks.md
git commit -m "refactor: 拆分性能场景与报告职责"
```

### Task 18: 拆分 perfcheck 比较与阈值职责

**Files:**
- Modify: `cmd/perfcheck/main.go`
- Create: `cmd/perfcheck/compare.go`
- Create: `cmd/perfcheck/validate.go`
- Create: `cmd/perfcheck/regression.go`
- Delete: `cmd/perfcheck/main_test.go`
- Create: `cmd/perfcheck/compare_test.go`
- Create: `cmd/perfcheck/migration_test.go`
- Create: `cmd/perfcheck/transport_test.go`
- Create: `cmd/perfcheck/regression_test.go`
- Create: `cmd/perfcheck/cli_test.go`
- Create: `cmd/perfcheck/helpers_test.go`
- Modify: `openspec/changes/m4o-responsibility-oriented-code-organization/tasks.md`

**Interfaces:**
- Preserves: CLI flags、scenario 迁移矩阵、provenance、阈值数值、性能仅记录语义、20% 相对比较、量化噪声和输出文本。
- Preserves: 唯一迁移 `14:15`，不新增其他授权。

- [ ] **Step 1: 记录 perfcheck 基线**

Run:

```bash
zsh -ic 'go test ./cmd/perfcheck -race -count=1'
zsh -ic 'go run ./cmd/perfcheck --baseline docs/notes/perf-baseline.json --current docs/notes/perf-baseline.json --max-regression 0.20'
```

Expected: PASS；输出同场景记录完成。

- [ ] **Step 2: 原样拆分生产声明**

```text
main.go:
  main, readReport, fail
compare.go:
  comparisonSuccessMessage, compareReports,
  compareReportsWithScenarioUpgrade
validate.go:
  validateReportProvenance, appendV6AbsoluteFailures,
  validateV6Report, appendV6MultiplayerRegressions,
  validateV5Report
regression.go:
  m3bLatencyNoiseFloorMS、pollCompletionTickMS、gpuCompletionResolutionMS、
  latencyNoiseFloorMS、persistenceTailNoiseFloorMS、
  appendM3BStableLatencyRegressions、appendM3BLatencyRegressions、appendStableSummaryRegressions、
  appendSummaryRegressions、appendRegression、regressionFloors、
  uniformFloors、persistenceFloors、appendRegressionWithResolution、regressed
```

- [ ] **Step 3: 按比较契约拆测试**

```text
compare_test.go:
  persistence、old report、hardware/protocol、基础 same-scenario 比较
migration_test.go:
  scenario upgrade matrix、历史 scenario 可读性、v12-v15 保真
transport_test.go:
  cross-transport commit/scenario、stable field matrix 与 server probe 测试
regression_test.go:
  threshold、noise floor、quantized metric、persistence tail 测试
cli_test.go:
  success message、performance record、CLI exit 测试
helpers_test.go:
  completeV5..V15ComparableReport、scenarioComparableReport、comparableReport
```

- [ ] **Step 4: 验证迁移矩阵和基线字节并提交**

Run:

```bash
gofmt -w cmd/perfcheck
zsh -ic 'go test ./cmd/perfcheck -race -count=1'
zsh -ic 'go test ./cmd/perfcheck -run "ScenarioUpgrade|CrossTransport|Threshold|NoiseFloor|PersistenceTail" -count=1'
zsh -ic 'go run ./cmd/perfcheck --baseline docs/notes/perf-baseline.json --current docs/notes/perf-baseline.json --max-regression 0.20'
shasum -a 256 docs/notes/perf-baseline.json docs/notes/perf-baseline-m5.json
zsh -ic 'go test ./internal/archcheck -count=1'
gofmt -l cmd/perfcheck
git diff --check
```

Expected hashes:

```text
9691d9752f309795e77176c6f959c357c4c97f1f7daaa4a5a6fddff8bf164d78  docs/notes/perf-baseline.json
5a34fe091cb1aacfee0172db90b5a7f66571202d230e7542660dd8e703132483  docs/notes/perf-baseline-m5.json
```

```bash
git add cmd/perfcheck openspec/changes/m4o-responsibility-oriented-code-organization/tasks.md
git commit -m "refactor: 拆分性能比较与阈值职责"
```

### Task 19（历史审计）: 完成全仓审计、artifact 保真与最终门禁

**Files:**
- Modify: `openspec/changes/m4o-responsibility-oriented-code-organization/design.md`
- Modify: `openspec/changes/m4o-responsibility-oriented-code-organization/tasks.md`
- No production behavior changes

**Interfaces:**
- Consumes: Task 2–18 的独立提交和验证证据。
- Produces: 完整包级审计结果、最终验证报告与 18/18 完成的 active change。

- [ ] **Step 1: 审计所有未修改包与剩余文件**

逐包运行以下固定集合；任何失败先修本计划范围内的搬迁错误，不顺手重构：

```bash
zsh -ic 'go test ./internal/core ./internal/world ./internal/physics ./internal/mesh ./internal/assets ./internal/profile ./internal/config ./internal/logging ./internal/worldgen ./internal/gfx/shader -race -count=1'
zsh -ic 'go test ./internal/network ./internal/storage ./internal/sim ./internal/server ./internal/client ./internal/gfx ./internal/render ./internal/render/hud -race -count=1'
zsh -ic 'go test ./cmd/gfxspike ./cmd/mcgo ./cmd/mcgod ./cmd/perfcheck -race -count=1'
```

在 change `design.md` 的审计表逐包写明：本计划指定文件为 split/move，其余基线文件为 keep；删除项只能是已被职责文件替代的原大文件。不得新增第二个内部包。

再比较基线与当前的测试、benchmark 和 fuzz 入口名：

```bash
{
  git grep -h -E '^func (Test|Benchmark|Fuzz)' 96c4aae -- cmd internal \
    | sed 's/(.*//' \
    | sed 's/^func TestSessionLifecycleResponsibilitiesLiveInSessionFile$/func TestSessionLifecycleResponsibilitiesStayInSessionFiles/'
  printf '%s\n' \
    'func TestProductionGoSourceScansSplitFiles' \
    'func TestTopLevelDeclarationNamesInScansSplitFiles'
} | sort > /private/tmp/m4o-symbols-expected.txt
rg '^func (Test|Benchmark|Fuzz)' cmd internal -g '*_test.go' | sed 's/.*func /func /; s/(.*//' | sort > /private/tmp/m4o-symbols-after.txt
diff -u /private/tmp/m4o-symbols-expected.txt /private/tmp/m4o-symbols-after.txt
```

Expected: `diff` 无输出。相对 `96c4aae` 只允许 Task 2 明确批准的两项新增测试 `TestProductionGoSourceScansSplitFiles`、`TestTopLevelDeclarationNamesInScansSplitFiles`，以及 `TestSessionLifecycleResponsibilitiesLiveInSessionFile` → `TestSessionLifecycleResponsibilitiesStayInSessionFiles` 重命名；除此之外任何 Test、Benchmark 或 Fuzz 入口变化都必须恢复后才能继续。

- [ ] **Step 2: 核对协议、存档、视觉和性能 artifact 字节**

Run:

```bash
shasum -a 256 internal/storage/testdata/chunk-v6.bin internal/storage/testdata/chunk-v7.bin internal/storage/testdata/player-v5.bin
shasum -a 256 docs/notes/perf-baseline.json docs/notes/perf-baseline-m5.json
for file in cmd/mcgo/testdata/golden/*.png; do shasum -a 256 "$file"; done
```

Expected fixture/performance hashes:

```text
598a59f998f00cb0c737a2605e5ea0e58e8083ddd116a37c2f9070ced0b790b6  chunk-v6.bin
b03f94e917a204842547e6b2a65933bb633858f2d6c9ec95a35e2b5c990f8a4a  chunk-v7.bin
6e82a565a94da378e7d4cd6bb1556a54766817d6c475a7844feb279e693e1928  player-v5.bin
9691d9752f309795e77176c6f959c357c4c97f1f7daaa4a5a6fddff8bf164d78  perf-baseline.json
5a34fe091cb1aacfee0172db90b5a7f66571202d230e7542660dd8e703132483  perf-baseline-m5.json
```

Expected golden hashes:

```text
e9de5ae88c1b60db012dc97d5e9026959b0f872a06a996de945e2bc28d0c7603  avatar-nametag.png
e3b6076e09deb1c566529421fc0f7b8d0f06154bf64512224ede14242ebbf8e5  block-light-room.png
d2ec3304bf2034eb9fdac10a7b5aaa4ff4602626e2934be9cf95eb5ff99ccabb  debug-panel.png
dfa842f06ae3d15cd1d7360f61ef81939ea30dce6f578f06c78c7376f59a4517  hud-hotbar-health.png
c5d745421144db481f5a5603aa7ba1b946a6d3030890ad4ba551ebf43f423309  inventory-crafting.png
92afaebe339161c91b99aee7b930353564da2109494a5a755b928c4c4008ace8  skylight-tunnel.png
88cb815a8aa34d6ae5571bcbd23985f3a62569656a460827f8e16d601672180f  terrain-noon.png
```

- [ ] **Step 3: 跑 fuzz、视觉与当前代码性能记录**

Run:

```bash
zsh -ic 'go test ./internal/network -run=^$ -fuzz=FuzzSmallPacketCodec -fuzztime=10s'
zsh -ic 'go test ./internal/storage -run=^$ -fuzz=FuzzDecodeChunkPayload -fuzztime=10s'
VISUAL_OUT=/private/tmp/mcgo-m4o-final-visual make visual-check
TERM=xterm-256color zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport memory --perf-output /private/tmp/mcgo-m4o-current.json"
TERM=xterm-256color zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/perfcheck --baseline /private/tmp/mcgo-m4o-current.json --current /private/tmp/mcgo-m4o-current.json --max-regression 0.20"
TERM=xterm-256color zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/perfcheck --baseline docs/notes/perf-baseline.json --current /private/tmp/mcgo-m4o-current.json --max-regression 0.20"
```

不得覆盖 tracked baseline，也不得提高阈值或 scenario。性能数值差异只保留在报告中，不阻断提交；只有报告结构、身份/provenance、真实 overflow、数据丢失、I/O 错误和非数值命令失败才停止。

- [ ] **Step 4: 跑最终共享门禁**

Run:

```bash
zsh -ic 'go test ./internal/archcheck -count=1'
zsh -ic 'go test ./... -race'
zsh -ic 'go vet ./...'
gofmt -l .
openspec validate --all --strict --no-interactive
git diff --check
```

Expected: 所有命令成功，`gofmt -l .` 无输出。

- [ ] **Step 5: 独立评审并提交收尾**

请求独立 reviewer 检查 `96c4aae..HEAD`，必须逐项回答：

```text
1. 是否存在行为、wire、storage、visual 或 benchmark 契约漂移；
2. 是否出现 internal/render 反向依赖 internal/render/hud；
3. 是否新增未批准的包、抽象、wrapper 或行数门禁；
4. 是否有测试名/场景在移动中丢失；
5. 保留的大文件是否仍只有一个职责；
6. 所有 386 个基线 Go 文件是否已在包级审计中得到结论。
```

修复范围内 Critical/Important 后重跑对应门禁，再勾完 `tasks.md`。最后提交：

```bash
git add openspec/changes/m4o-responsibility-oriented-code-organization
git commit -m "docs: 完成 M4O 全仓职责审计"
```

保持 change active。只有用户明确要求时才 sync/archive、推送、创建 PR 或删除工作树。

### Task 20: 同步 origin/main 并保持职责边界

**Files:**
- Modify: `docs/superpowers/plans/2026-08-10-m4o-responsibility-oriented-code-organization.md`
- Modify: `openspec/changes/m4o-responsibility-oriented-code-organization/{proposal.md,design.md,tasks.md}`
- Modify: `openspec/changes/m4o-responsibility-oriented-code-organization/specs/repository-code-organization/spec.md`
- Merge conflicts: `cmd/mcgo/{app.go,app_test.go,capture.go}`
- Merge conflict: `internal/gfx/wgpu.go`
- Merge conflicts: `internal/network/{codec_test.go,message.go}`
- Merge conflicts: `internal/render/{hotbar.go,hotbar_test.go}`
- Merge conflicts: `internal/server/{player_persistence.go,player_persistence_test.go,tcp_integration_test.go}`
- Merge conflicts: `internal/storage/{chunk_codec.go,chunk_codec_envelope_test.go}`

**Interfaces:**
- Consumes: Task 2–19 已完成的职责文件族，以及 `origin/main=37cdb3e0b3cd241bad1c3e70e5a25bcc9994c4fa` 的上游增量。
- Preserves: 协议 v15、区块 schema v8、玩家 schema v6、已归档 M4N、common materials、damage/target、material processing、natural generation/oak、light recipe、container-Y、10 个 capture 场景、所有固定 artifact 和测试入口。
- Produces: 解决冲突的 merge commit、相对 `37cdb3e` 的 412 文件审计与已推送的 PR 分支；不把任何上游能力归因于 M4O。

- [ ] **Step 1: 提交规划修订并冻结同步身份**

本 Step 只修改上述五个规划/OpenSpec 文件，不执行 merge、不修改 Go 代码。Run:

```bash
git status --short --branch
git rev-parse HEAD
git rev-parse origin/main
git merge-tree --write-tree --name-only HEAD origin/main
git ls-tree -r --name-only 37cdb3e -- cmd internal | rg '\.go$' | wc -l
openspec validate --all --strict --no-interactive
git diff --check
```

Expected: `origin/main` 为 `37cdb3e0b3cd241bad1c3e70e5a25bcc9994c4fa`；Go 文件为 412；merge-tree 精确列出本 Task `Files` 中的 13 个冲突；OpenSpec strict 全绿，diff 只有规划/OpenSpec。

```bash
git add docs/superpowers/plans/2026-08-10-m4o-responsibility-oriented-code-organization.md \
  openspec/changes/m4o-responsibility-oriented-code-organization
git commit -m "docs: 规划 M4O 主线同步冲突修复"
```

- [ ] **Step 2: 合并固定主线并停在 13 个已知冲突**

```bash
git fetch origin
test "$(git rev-parse origin/main)" = 37cdb3e0b3cd241bad1c3e70e5a25bcc9994c4fa
git merge --no-commit origin/main
git diff --name-only --diff-filter=U | sort
```

Expected: merge 只停在以下 13 个冲突；若主线身份或冲突集合变化，立即停止并先修订规划：

```text
cmd/mcgo/app.go
cmd/mcgo/app_test.go
cmd/mcgo/capture.go
internal/gfx/wgpu.go
internal/network/codec_test.go
internal/network/message.go
internal/render/hotbar.go
internal/render/hotbar_test.go
internal/server/player_persistence.go
internal/server/player_persistence_test.go
internal/server/tcp_integration_test.go
internal/storage/chunk_codec.go
internal/storage/chunk_codec_envelope_test.go
```

- [ ] **Step 3: 逐声明迁移上游冲突**

严格按 active `design.md` 的 13 行映射手工解决：

```text
app.go/app_test.go/capture.go
  → 现有 app_*、app_*_test.go、capture_* 文件族；保留 damage/target 与固定 10 场景
wgpu.go
  → wgpu_convert.go、wgpu_pipeline.go；保留 DepthCompareLessEqual
hotbar.go/hotbar_test.go
  → internal/render/hud 的 container/layout/atlas 及测试；保留 8 个配方
message.go
  → message_container.go；保留 smelting validation
codec_test.go
  → codec_golden_test.go、codec_inventory_test.go；保留协议 v15 golden
player_persistence.go/player_persistence_test.go
  → player_persistence_snapshot.go、player_persistence_lifecycle_test.go；保留 starter materials
tcp_integration_test.go
  → tcp_restart_integration_test.go、furnace_tcp_integration_test.go；保留 Ready barrier
chunk_codec.go/chunk_codec_envelope_test.go
  → chunk_codec_container.go、chunk_codec_roundtrip_test.go、chunk_codec_helpers_test.go 及精简 envelope 测试；保留 world-Y container 与 schema v8 fixture
```

不得接受任一冲突文件的整份 ours/theirs，不得恢复拆分前大文件、删除上游 case、复制实现、增加 wrapper/包/依赖、调整 schema/scenario/阈值或更新 artifact。

- [ ] **Step 4: 证明声明、入口和 artifact 与新基线一致**

逐包更新 active `design.md` 的 Task 20 审计，覆盖 `37cdb3e` 的全部 412 个 Go 文件，结论必须精确为 36 split + 2 extract + 0 delete + 374 keep；唯一新增包仍是 `internal/render/hud`。

比较 Test/Benchmark/Fuzz 入口：

```bash
{
  git grep -h -E '^func (Test|Benchmark|Fuzz)' 37cdb3e -- cmd internal \
    | sed 's/(.*//' \
    | sed 's/^func TestSessionLifecycleResponsibilitiesLiveInSessionFile$/func TestSessionLifecycleResponsibilitiesStayInSessionFiles/'
  printf '%s\n' \
    'func TestProductionGoSourceScansSplitFiles' \
    'func TestTopLevelDeclarationNamesInScansSplitFiles'
} | sort > /private/tmp/m4o-task20-symbols-expected.txt
rg '^func (Test|Benchmark|Fuzz)' cmd internal -g '*_test.go' \
  | sed 's/.*func /func /; s/(.*//' \
  | sort > /private/tmp/m4o-task20-symbols-current.txt
diff -u /private/tmp/m4o-task20-symbols-expected.txt /private/tmp/m4o-task20-symbols-current.txt
```

直接对比新主线 artifact，不沿用 Task 19 旧 hash：

```bash
git diff --exit-code 37cdb3e -- \
  internal/storage/testdata \
  internal/network/testdata \
  cmd/mcgo/testdata/golden
test "$(git ls-tree -r --name-only 37cdb3e -- cmd/mcgo/testdata/golden | rg '\.png$' | wc -l | tr -d ' ')" = 10
git diff --exit-code 37cdb3e -- docs/notes/perf-baseline.json docs/notes/perf-baseline-m5.json
```

Expected: 入口 diff 与 artifact diff 均无输出；visual golden 精确 10 个。若上游未修改性能 baseline，上述比较同时证明其原字节不变。

- [ ] **Step 5: 跑冲突域 focused、race、fuzz、视觉和性能记录**

```bash
gofmt -w cmd/mcgo internal/gfx internal/network internal/render internal/server internal/storage
zsh -ic 'go test ./cmd/mcgo ./internal/gfx ./internal/network ./internal/render ./internal/render/hud ./internal/server ./internal/storage -race -count=1'
zsh -ic 'go test ./cmd/mcgo -run "Damage|Target|Capture|Golden|Recipe|Material" -count=1'
zsh -ic 'go test ./internal/gfx -run "DepthCompareLessEqual|Pipeline" -count=1'
zsh -ic 'go test ./internal/network -run "ProtocolV15|Golden|Inventory|Container|Smelting" -count=1'
zsh -ic 'go test ./internal/server -run "Starter|Ready|Furnace|TCPPlayer|PlayerPersistence" -count=1'
zsh -ic 'go test ./internal/storage -run "ChunkPayload|Schema8|Container|WorldY|Fixture" -count=1'
zsh -ic 'go test ./internal/network -run=^$ -fuzz=FuzzSmallPacketCodec -fuzztime=10s'
zsh -ic 'go test ./internal/storage -run=^$ -fuzz=FuzzDecodeChunkPayload -fuzztime=10s'
VISUAL_OUT=/private/tmp/mcgo-m4o-task20-visual make visual-check
TERM=xterm-256color zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport memory --perf-output /private/tmp/mcgo-m4o-task20-current.json"
TERM=xterm-256color zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/perfcheck --baseline /private/tmp/mcgo-m4o-task20-current.json --current /private/tmp/mcgo-m4o-task20-current.json --max-regression 0.20"
TERM=xterm-256color zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/perfcheck --baseline docs/notes/perf-baseline.json --current /private/tmp/mcgo-m4o-task20-current.json --max-regression 0.20"
```

visual-check 只比较 10 个场景，绝不使用 `--update-golden`；性能数值只记录，不改变退出状态。只有报告结构、身份/provenance、真实 overflow、数据丢失、I/O 错误和非数值命令失败阻断。

- [ ] **Step 6: 跑最终共享门禁**

```bash
zsh -ic 'go test ./internal/archcheck -count=1'
zsh -ic 'go test ./... -race'
zsh -ic 'go vet ./...'
gofmt -l .
openspec validate --all --strict --no-interactive
git diff --check
```

Expected: 所有命令成功，`gofmt -l .` 无输出；性能纯数值记录不改变退出状态。

- [ ] **Step 7: 独立评审主线同步**

独立 reviewer 必须逐项回答：13 个冲突是否全部按声明迁移；主线 v15/v8/v6 与已归档 M4N 能力是否完整；damage/target、10 场景、`DepthCompareLessEqual`、8 recipes、smelting validation、v15 golden、starter materials、Ready barrier、world-Y containers、schema8 fixtures 是否保留；是否复活旧大文件或新增包/抽象/wrapper；412 文件、入口与 artifact parity 是否完整。Critical/Important 全部关闭后才可继续。

- [ ] **Step 8: 勾选、完成 merge commit 并 push**

只把 active `tasks.md` 的 20.1 改为 `[x]`，确认 staged merge 精确包含主线增量、13 个冲突的职责化解决、Task 20 最终审计与 checkbox，不包含 ignored report，然后完成 merge commit 并推送当前分支：

```bash
git diff --cached --check
git status --short
git commit
git push origin codex/m4o-code-organization
```

不得 archive/sync M4O、更新 golden/baseline、删除备份分支或删除 worktree；后续 PR 合并由用户另行决定。
