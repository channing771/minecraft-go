# 常见块状材料体系 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增 14 种稳定、可放置、可采掘和可持久化的标准立方体材料，以世界坐标 UV 修复连续草地接缝，并用现有单 pass atlas 实现玻璃与树叶 cutout 呈现。

**Architecture:** 继续扩展现有 `core` ID 与 switch，不建立通用方块定义系统；`core.RegisteredBlock` 成为网络和存档边界的合法性来源。渲染侧只给 `mesh.Registry` 增加面可见性判断，AO/天空光继续复用 `Opaque`，terrain shader 从已有世界坐标计算 UV 并执行 alpha cutout，不修改 8 字节实例格式或增加 render pass。

**Tech Stack:** Go 1.26、WebGPU/WGSL、现有 paletted chunk/storage/network codec、OpenSpec、Go `testing`、无窗口 `cmd/mcgo --capture`。

## Global Constraints

- 方块 ID 只追加在 `ChestID` 后，顺序固定为圆石、平滑石、沙子、砾石、橡木原木、橡木木板、树叶、玻璃、砖块、白色羊毛、红色瓦块、黏土、雪块、苔藓圆石；物品 ID 以同顺序追加在 `ItemChest` 后，提交后不得重排或复用。
- 协议固定从 v13 升到 v14；玩家 schema 从 v5 升到 v6；区块 schema 从 v6 升到 v7；三者只做语义版本升级，不改变已有 wire 或存档字段布局。
- 服务端继续是放置、采掘、掉落、背包和持久化的唯一权威；Memory 与 TCP 必须复用相同 codec 和校验。
- 新玩家材料包只在 `PlayerStore.LoadPlayer` 返回 `ErrPlayerNotFound` 时生成：快捷栏为空，背包前 14 格按固定清单各 64 个；已有玩家和 v5→v6 migration 不补发。
- `GlassID` 与 `LeavesID` 是完整碰撞方块但 `Opaque=false`；只使用 alpha `0/255` 与 fragment `discard`，不得增加透明排序、混合、第二 terrain pass 或新的 quad 字段。
- 世界坐标 UV 必须复用现有 section origin 与局部顶点；`mesh.Quad.Pack()` 仍精确为 8 字节，atlas 仍是固定 16×16 2D array。
- 材质必须由确定性 Go 代码生成，不提交 Mojang 资源、外部 bitmap 或新增第三方依赖。
- 不实现世界生成、配方、沙砾重力、树叶腐烂、雪融化、横向原木、薄雪层、PBR、法线图或动画材质。
- Go 注释、GoDoc、测试说明和 OpenSpec 文本使用中文；标识符、wire magic、协议字段名和约定俗成的技术术语保留英文。
- 自动测试与视觉验收只能走 headless/offscreen 路径，不启动或聚焦前台游戏窗口。
- 性能只记录 benchmark、quad 与上传量，不设数值通过门槛；报告结构错误、真实 overflow、数据丢失和 I/O 错误仍失败。
- 每个实现任务遵循 RED→GREEN→相称 race 验证→勾选对应 OpenSpec task→独立提交；不得改写 Hook 或使用豁免变量绕过门禁。
- 保留 `midscene_run/log/ios-device.log`、`midscene_run/log/mcp-base-tools.log`、`midscene_run/log/webdriver-client.log` 的用户改动，任何提交都不得包含它们。

## File Map

| 责任 | 文件 |
|---|---|
| 稳定方块/物品编号和映射 | `internal/core/block.go`, `internal/core/item.go` |
| 权威采掘规则 | `internal/sim/mining.go` |
| 协议版本和方块边界校验 | `internal/network/packet.go`, `internal/network/snapshot.go`, `internal/network/chunk_codec.go` |
| 世界快照边界校验 | `internal/world/snapshot.go` |
| 玩家/区块语义 schema migration | `internal/storage/player_codec.go`, `internal/storage/player_migration.go`, `internal/storage/chunk_codec.go`, `internal/storage/migration.go` |
| 缺失玩家材料包 | `internal/server/player_persistence.go` |
| 面可见性、AO 与天空光语义 | `internal/mesh/greedy.go`, `internal/assets/blocks.go` |
| 程序化材质和 mip | `internal/assets/procedural.go`, `internal/assets/atlas.go` |
| 世界 UV 与 alpha cutout | `internal/render/shader/terrain.wgsl` |
| 无窗口展示场景和 golden | `cmd/mcgo/capture.go`, `cmd/mcgo/testdata/golden/*.png` |
| 行为契约和执行状态 | `openspec/changes/common-block-materials/**`, 归档后的 `openspec/specs/**` |

---

### Task 1: 建立并冻结 OpenSpec change

**Files:**
- Create: `openspec/changes/common-block-materials/.openspec.yaml`
- Create: `openspec/changes/common-block-materials/proposal.md`
- Create: `openspec/changes/common-block-materials/design.md`
- Create: `openspec/changes/common-block-materials/tasks.md`
- Create: `openspec/changes/common-block-materials/specs/common-block-materials/spec.md`
- Create: `openspec/changes/common-block-materials/specs/voxel-visual-presentation/spec.md`
- Create: `openspec/changes/common-block-materials/specs/visual-verification/spec.md`
- Create local report: `.superpowers/sdd/2026-08-09-common-block-materials/task-8-report.md`（被忽略，不提交）
- Reference: `docs/superpowers/specs/2026-08-09-common-block-materials-design.md`

**Interfaces:**
- Consumes: 已批准设计文档中的固定 14 材料清单、采掘规则、v14/v6/v7 兼容策略和 `materials-showcase` 验收条件。
- Produces: active change `common-block-materials`；后续任务只实现该 change 的 `tasks.md`，不得自行扩大范围。

- [ ] **Step 1: 创建 change 骨架**

Run:

```bash
openspec new change common-block-materials
```

Expected: 创建 `openspec/changes/common-block-materials/`，`.openspec.yaml` 包含：

```yaml
schema: spec-driven
created: 2026-08-09
```

- [ ] **Step 2: 写 proposal、design 与 delta specs**

`proposal.md` 必须明确：

```markdown
## Why

当前材料少且 terrain quad 各自重置 UV，相邻草块会在 AO、光照、区段或区块拆分处出现纹理断层；现有 Opaque 语义也无法同时表达可见但不遮光的玻璃和树叶。

## What Changes

- 只追加 14 种标准立方体材料及对应物品，全部可堆叠、放置、采掘并掉落自身。
- 新增 RegisteredBlock 并在协议/存档边界拒绝未知方块。
- 协议升级到 v14、玩家 schema 到 v6、区块 schema 到 v7，布局不变并做 identity migration。
- terrain 使用世界坐标 UV；玻璃和树叶使用同一 atlas、同一 pass 的 alpha cutout。
- 缺失玩家首次获得背包材料包；已有玩家不变。
- 新增无窗口 materials-showcase，并记录而不门禁性能数值。

## Capabilities

### New Capabilities

- common-block-materials

### Modified Capabilities

- voxel-visual-presentation
- visual-verification

## Impact

影响 core/sim/network/world/storage/server/assets/mesh/render/cmd/mcgo；不新增依赖、外部资源、世界生成、配方、方块状态或渲染 pass。
```

`common-block-materials/spec.md` 使用 `## ADDED Requirements`，至少包含以下精确 Requirement/Scenario 对：

```markdown
### Requirement: 稳定材料注册表
- GIVEN 当前稳定编号止于 ChestID/ItemChest
- WHEN 注册 14 种材料
- THEN 编号 MUST 只按批准清单追加，RegisteredBlock/RegisteredItem/ItemPlacement/BlockDrop MUST 一一对应，未知编号 MUST 被拒绝

### Requirement: 权威放置采掘与掉落
- GIVEN 任一新材料物品
- WHEN 服务端放置并完成可采收的采掘
- THEN 世界 MUST 写入对应方块、扣除一件并掉落自身，且三组固定采掘 tick/工具规则 MUST 与设计一致

### Requirement: 缺失玩家材料包
- GIVEN LoadPlayer 返回 ErrPlayerNotFound
- WHEN 准备玩家快照
- THEN 快捷栏 MUST 为空且背包前 14 格 MUST 按固定顺序各含 64 个材料
- GIVEN 已有玩家或未确认登录
- WHEN 恢复、迁移或断开
- THEN 已有背包 MUST 不变且未确认材料包 MUST 不持久化或累加

### Requirement: 协议与存档语义版本
- WHEN 新注册表上线
- THEN ProtocolVersion MUST 为 14、player schema MUST 为 6、chunk schema MUST 为 7，旧数据 MUST identity migrate 且 future/unknown 数据 MUST 明确拒绝

### Requirement: cutout 方块语义
- GIVEN 玻璃或树叶
- WHEN 网格、AO、天空光与碰撞查询它
- THEN 它 MUST 可绘制、保持完整碰撞、不得完全遮挡 AO/天空光，并 MUST 剔除同类内部面
```

`voxel-visual-presentation/spec.md` 使用 `## MODIFIED Requirements`，固定世界坐标 UV、草顶/侧周期边界、14 种确定性 16×16 材质、单 pass cutout、覆盖保持 mip、8 字节实例格式不变。`visual-verification/spec.md` 使用 `## MODIFIED Requirements`，规定在场景表末尾追加 `materials-showcase`，包含 14 种材料、8 格草地、相邻玻璃/树叶和原木顶侧面，且只走无窗口抓帧、沿用现有双阈值。

`design.md` 直接承载批准设计的架构决策，不增加数据驱动注册框架。`tasks.md` 建立与本计划 Task 2..9 一一对应的八组 checkbox，并为每组写明本计划列出的验证命令。

- [ ] **Step 3: 验证 active change 完整且严格通过**

Run:

```bash
openspec validate common-block-materials --strict --no-interactive
openspec validate --all --strict --no-interactive
git diff --check
```

Expected: 两次 OpenSpec 校验均 `0 failed`，`git diff --check` 无输出；`openspec list --json` 只新增 `common-block-materials`。

- [ ] **Step 4: 在任何生产代码变化前记录 terrain mesh 基线**

Run:

```bash
go test ./internal/mesh -run '^$' -bench '^BenchmarkMeshTerrainSection$' -benchmem -count=5
go test ./internal/render -run '^$' -bench '^BenchmarkMeshChunk$' -benchmem -count=5
```

Expected: 两条命令结构性成功。把精确 HEAD、两条命令、五次原始输出和中位 `ns/op`、`B/op`、`allocs/op` 写入 `.superpowers/sdd/2026-08-09-common-block-materials/task-8-report.md` 的“实现前基线”段；该文件不得暂存。

- [ ] **Step 5: 提交 OpenSpec change**

```bash
git add openspec/changes/common-block-materials
git commit -m "docs: 规划常见块状材料体系"
```

Expected: 提交只包含七个 change 产物；三份 `midscene_run/log/*.log` 不在提交中。

---

### Task 2: 追加稳定 ID、物品映射与采掘规则

**Files:**
- Modify: `internal/core/block.go`
- Modify: `internal/core/item.go`
- Modify: `internal/core/item_test.go`
- Modify: `internal/sim/mining.go`
- Modify: `internal/sim/mining_test.go`
- Modify: `internal/sim/interaction_test.go`
- Modify: `internal/physics/collision_test.go`
- Modify: `openspec/changes/common-block-materials/tasks.md`

**Interfaces:**
- Consumes: Task 1 固定的 14 项顺序和三组采掘规则。
- Produces: `func RegisteredBlock(BlockID) bool`；14 组稳定 `BlockID`/`ItemID`；完整的 stack/place/drop/mining 映射，供 Task 3..8 使用。

- [ ] **Step 1: 写注册、映射和采掘 RED 表格测试**

在 `internal/core/item_test.go` 增加同一份固定清单测试：

```go
func TestCommonBlockMaterialsAreFixedAndRoundTrip(t *testing.T) {
	tests := []struct {
		block core.BlockID
		item  core.ItemID
	}{
		{core.CobblestoneID, core.ItemCobblestone},
		{core.SmoothStoneID, core.ItemSmoothStone},
		{core.SandID, core.ItemSand},
		{core.GravelID, core.ItemGravel},
		{core.OakLogID, core.ItemOakLog},
		{core.OakPlanksID, core.ItemOakPlanks},
		{core.LeavesID, core.ItemLeaves},
		{core.GlassID, core.ItemGlass},
		{core.BrickID, core.ItemBrick},
		{core.WhiteWoolID, core.ItemWhiteWool},
		{core.RoofTileID, core.ItemRoofTile},
		{core.ClayID, core.ItemClay},
		{core.SnowBlockID, core.ItemSnowBlock},
		{core.MossyCobblestoneID, core.ItemMossyCobblestone},
	}
	for i, tc := range tests {
		if tc.block != core.ChestID+1+core.BlockID(i) || tc.item != core.ItemChest+1+core.ItemID(i) {
			t.Fatalf("材料 %d 的稳定编号不连续: block=%d item=%d", i, tc.block, tc.item)
		}
		if !core.RegisteredBlock(tc.block) || !core.RegisteredItem(tc.item) {
			t.Fatalf("材料 %d 未注册", i)
		}
		if got, ok := core.ItemPlacement(tc.item); !ok || got != tc.block {
			t.Fatalf("ItemPlacement(%d)=(%d,%v)，想要 (%d,true)", tc.item, got, ok, tc.block)
		}
		if got, ok := core.BlockDrop(tc.block); !ok || got != tc.item {
			t.Fatalf("BlockDrop(%d)=(%d,%v)，想要 (%d,true)", tc.block, got, ok, tc.item)
		}
		if limit, ok := core.ItemStackLimit(tc.item); !ok || limit != 64 {
			t.Fatalf("ItemStackLimit(%d)=(%d,%v)，想要 (64,true)", tc.item, limit, ok)
		}
	}
	if core.RegisteredBlock(core.MossyCobblestoneID + 1) {
		t.Fatal("未知方块被注册")
	}
}
```

在 `internal/sim/mining_test.go` 增加表驱动测试，逐项断言：

```go
func TestCommonBlockMaterialMiningRules(t *testing.T) {
	assertMiningRule := func(block core.BlockID, held core.ItemID, wantTicks uint16, wantHarvestable bool) {
		t.Helper()
		gotTicks, gotHarvestable := miningRule(block, held)
		if gotTicks != wantTicks || gotHarvestable != wantHarvestable {
			t.Fatalf("miningRule(%d,%d)=(%d,%v)，想要 (%d,%v)",
				block, held, gotTicks, gotHarvestable, wantTicks, wantHarvestable)
		}
	}
	for _, block := range []core.BlockID{
		core.SandID, core.GravelID, core.LeavesID, core.GlassID,
		core.WhiteWoolID, core.ClayID, core.SnowBlockID,
	} {
		assertMiningRule(block, core.ItemCoal, 5, true)
	}
	for _, block := range []core.BlockID{core.OakLogID, core.OakPlanksID} {
		assertMiningRule(block, core.ItemCoal, 15, true)
	}
	for _, block := range []core.BlockID{
		core.CobblestoneID, core.SmoothStoneID, core.BrickID,
		core.RoofTileID, core.MossyCobblestoneID,
	} {
		assertMiningRule(block, core.ItemNone, 30, true)
		assertMiningRule(block, core.ItemBrokenStonePickaxe, 30, true)
		assertMiningRule(block, core.ItemStonePickaxe, 15, true)
		assertMiningRule(block, core.ItemIronPickaxe, 8, true)
		assertMiningRule(block, core.ItemCoal, 30, false)
	}
}
```

在 `internal/physics/collision_test.go` 对 `GlassID` 和 `LeavesID` 调用 `physics.BlockCollisionBoxes(id, true)`，断言 `Loaded=true`、`Count=1` 且首个 box 精确为 `[0,0,0]..[1,1,1]`；这锁定 cutout 只改变呈现/遮光，不改变完整方块碰撞。

把 `TestEnginePlaceValidationAndWhitelist` 的合法放置表扩展为全部 14 个新 ItemID，逐项走真实 `sim.CommandPlaceBlock` 并断言世界写入对应 BlockID、物品数量扣除 1。把 `TestMiningCompletionUsesFixedToolAndDropRules` 至少增加沙子裸手、原木普通物品、圆石石镐、砖块普通物品四条，分别锁定三组时长和可采收/不可采收后的真实掉落结果。

- [ ] **Step 2: 运行 RED**

Run:

```bash
go test ./internal/core ./internal/sim ./internal/physics -run 'CommonBlockMaterial|CommonBlockMaterials|CutoutBlocksUseFullCollision' -count=1
```

Expected: FAIL，首先报告新增常量或 `RegisteredBlock` 未定义。

- [ ] **Step 3: 追加编号并完成最小 switch**

在 `ChestID` 和 `ItemChest` 后按清单追加常量。`RegisteredBlock` 只利用稳定编号连续性：

```go
// RegisteredBlock 报告 id 是否是已注册的稳定方块编号。
func RegisteredBlock(id BlockID) bool {
	return id <= MossyCobblestoneID
}
```

扩展 `BlockDrop`、`ItemStackLimit` 和 `ItemPlacement` 的现有 switch；不引入定义表或注册框架。`miningRule` 用三组 case 合并最小实现：

```go
case core.SandID, core.GravelID, core.LeavesID, core.GlassID,
	core.WhiteWoolID, core.ClayID, core.SnowBlockID:
	return 5, true
case core.OakLogID, core.OakPlanksID:
	return 15, true
case core.StoneID, core.CobblestoneID, core.SmoothStoneID,
	core.BrickID, core.RoofTileID, core.MossyCobblestoneID:
	switch held {
	case core.ItemNone, core.ItemBrokenStonePickaxe, core.ItemBrokenIronPickaxe:
		return 30, true
	case core.ItemStonePickaxe:
		return 15, true
	case core.ItemIronPickaxe:
		return 8, true
	default:
		return 30, false
	}
```

- [ ] **Step 4: 运行 GREEN 和 mutation check**

Run:

```bash
gofmt -w internal/core/block.go internal/core/item.go internal/core/item_test.go internal/sim/mining.go internal/sim/mining_test.go internal/sim/interaction_test.go internal/physics/collision_test.go
go test ./internal/core ./internal/sim ./internal/physics -race -count=1
```

Expected: PASS。临时把 `RegisteredBlock` 上界改回 `ChestID` 时新增测试必须 FAIL；恢复后再次 PASS。

- [ ] **Step 5: 勾选任务并提交**

```bash
git add internal/core/block.go internal/core/item.go internal/core/item_test.go internal/sim/mining.go internal/sim/mining_test.go internal/sim/interaction_test.go internal/physics/collision_test.go openspec/changes/common-block-materials/tasks.md
git commit -m "feat: 注册常见块状材料"
```

---

### Task 3: 升级协议与存档语义版本并拒绝未知方块

**Files:**
- Modify: `internal/network/packet.go`
- Modify: `internal/network/snapshot.go`
- Modify: `internal/network/chunk_codec.go`
- Modify: `internal/network/packet_test.go`
- Modify: `internal/network/worldtime_test.go`
- Modify: `internal/network/drop_test.go`
- Modify: `internal/network/codec_test.go`
- Modify: `internal/network/chunk_codec_test.go`
- Modify: `internal/network/transport_consistency_test.go`
- Modify: `internal/world/snapshot.go`
- Modify: `internal/world/snapshot_test.go`
- Modify: `internal/storage/player_codec.go`
- Modify: `internal/storage/player_migration.go`
- Modify: `internal/storage/player_codec_test.go`
- Modify: `internal/storage/player_migration_test.go`
- Modify: `internal/storage/chunk_codec.go`
- Modify: `internal/storage/migration.go`
- Modify: `internal/storage/chunk_codec_test.go`
- Modify: `internal/storage/migration_test.go`
- Modify: `internal/storage/chunk_furnace_test.go`
- Modify: `internal/storage/chunk_chest_test.go`
- Modify: `openspec/changes/common-block-materials/tasks.md`

**Interfaces:**
- Consumes: `core.RegisteredBlock`, 新材料 ID/ItemID。
- Produces: `ProtocolVersion == 14`；`currentPlayerSchema == 6`；`currentChunkSchema == 7`；网络/世界/存档边界只接受已注册方块。

- [ ] **Step 1: 写版本、identity migration、round-trip 与 unknown rejection RED 测试**

新增或更新断言：

```go
if network.ProtocolVersion != 14 { t.Fatalf("协议版本=%d，想要 14", network.ProtocolVersion) }
if currentPlayerSchema != 6 { t.Fatalf("玩家 schema=%d，想要 6", currentPlayerSchema) }
if currentChunkSchema != 7 { t.Fatalf("区块 schema=%d，想要 7", currentChunkSchema) }
```

在 world/network 快照测试各加入 single、indexed、direct 三种未知 ID 拒绝；未知值固定使用 `core.MossyCobblestoneID + 1`。direct 形态把该值写入首个 15-bit slot 后调用 `Validate`/`NewPalettedContainerFromSnapshot`，必须返回错误。加入以下兼容测试：

- v5 玩家包含一个既有物品栈，迁移到 v6 后 inventory、位置、生命值逐字段不变且 `Migrated=true`；
- v6 区块包含 palette、掉落物、熔炉、箱子和 revision，迁移到 v7 后逐字段不变且 `Migrated=true`；
- 当前 v6 玩家可 round-trip `ItemMossyCobblestone ×64`；
- 当前 v7 区块可 round-trip 含 14 种新 BlockID 的 palette；
- v7/v6 之后的 future schema 继续返回 `ErrFutureVersion`；
- codec Hello golden 从十进制 13/hex `0d` 改为十进制 14/hex `0e`。
- 在 `transport_consistency_test.go` 复用现有 Memory/TCP opener 表，把含 `MossyCobblestoneID` single section 与 `ItemMossyCobblestone ×64` inventory 的合法 Play transcript 分别通过两种 transport，断言接收值逐字段相等；用协议 13 握手时两者都必须返回 `HandshakeVersionMismatch` 且服务端版本为 14。

- [ ] **Step 2: 运行 RED**

Run:

```bash
go test ./internal/network ./internal/world ./internal/storage -run 'ProtocolVersion|Schema|Migration|UnknownBlock|CommonBlockMaterial' -count=1
```

Expected: FAIL，版本仍为 13/5/6，且未知 15-bit 范围内 ID 仍被接受。

- [ ] **Step 3: 完成最小版本和合法性实现**

把三个版本常量改为 14/6/7；添加 identity migration：

```go
// v6 与 v5 的 payload 布局相同，只扩展合法物品注册表。
5: func(dto playerDTO) (playerDTO, error) { return dto, nil },

// v7 与 v6 的 payload 布局相同，只扩展合法方块注册表。
6: func(dto chunkDTO) (chunkDTO, error) { return dto, nil },
```

网络和 world 的合法性 helper 统一委托给 core：

```go
func validBlockID(id core.BlockID) bool { return core.RegisteredBlock(id) }

func validSnapshotBlockID(id core.BlockID) bool { return core.RegisteredBlock(id) }
```

对 direct storage 在结构校验后逐槽验证，避免未知值绕过 palette：

```go
for index := 0; index < core.BlocksPerSection; index++ {
	id := core.BlockID(readSectionPacked(section.Packed, section.Bits, index))
	if !core.RegisteredBlock(id) {
		return fmt.Errorf("network: direct block ID %d at block %d is unregistered", id, index)
	}
}
```

world 路径用 `readPacked` 做同样检查；decode 中已有 palette 校验改用同一 helper。错误文案从“超过 15 bits”改为“未注册”，不改变 wire 布局。

- [ ] **Step 4: 运行 GREEN、golden、fuzz seed 与 race**

Run:

```bash
gofmt -w internal/network/packet.go internal/network/snapshot.go internal/network/chunk_codec.go internal/network/packet_test.go internal/network/worldtime_test.go internal/network/drop_test.go internal/network/codec_test.go internal/network/chunk_codec_test.go internal/network/transport_consistency_test.go internal/world/snapshot.go internal/world/snapshot_test.go internal/storage/player_codec.go internal/storage/player_migration.go internal/storage/player_codec_test.go internal/storage/player_migration_test.go internal/storage/chunk_codec.go internal/storage/migration.go internal/storage/chunk_codec_test.go internal/storage/migration_test.go internal/storage/chunk_furnace_test.go internal/storage/chunk_chest_test.go
go test ./internal/network ./internal/world ./internal/storage -race -count=1
go test ./internal/network -run 'Golden|Codec|Chunk' -count=1
go test ./internal/storage -run 'Migration|RoundTrip|Future' -count=1
```

Expected: 全部 PASS；现有 fuzz seed 均不 panic，旧 schema 内容保持逐字段相等。

- [ ] **Step 5: 勾选任务并提交**

```bash
git add internal/network internal/world/snapshot.go internal/world/snapshot_test.go internal/storage openspec/changes/common-block-materials/tasks.md
git commit -m "feat: 升级材料协议与存档版本"
```

---

### Task 4: 为缺失玩家生成一次性材料包

**Files:**
- Modify: `internal/server/player_persistence.go`
- Modify: `internal/server/player_persistence_test.go`
- Modify: `openspec/changes/common-block-materials/tasks.md`

**Interfaces:**
- Consumes: Task 2 的 14 个 ItemID 与 `core.Inventory.Valid()`；现有 `newMissingCachedPlayer`、`Prepare`、`Confirm` 和异步保存流程。
- Produces: `func starterMaterialInventory() core.Inventory`；只供缺失玩家构造使用。

- [ ] **Step 1: 写缺失、已有、未确认和已确认四条 RED 测试**

在现有 persistence fixture 上断言：

```go
func wantStarterMaterialInventory() core.Inventory {
	items := [...]core.ItemID{
		core.ItemCobblestone, core.ItemSmoothStone, core.ItemSand, core.ItemGravel,
		core.ItemOakLog, core.ItemOakPlanks, core.ItemLeaves, core.ItemGlass,
		core.ItemBrick, core.ItemWhiteWool, core.ItemRoofTile, core.ItemClay,
		core.ItemSnowBlock, core.ItemMossyCobblestone,
	}
	var inventory core.Inventory
	for slot, item := range items {
		inventory.Backpack[slot] = core.ItemStack{Item: item, Count: 64}
	}
	return inventory
}
```

测试必须分别证明：`ErrPlayerNotFound` 的 prepared snapshot inventory 精确等于该值且 hotbar 全空；已存玩家的自定义 inventory 完全不变；确认前断开时 `SavePlayer` 调用数为 0，再次 Prepare 仍只有一份 14 格材料；Confirm 后保存并重载时 inventory 精确保留且不再补发。

- [ ] **Step 2: 运行 RED**

Run:

```bash
go test ./internal/server -run 'PlayerPersistence.*(Missing|Existing|Confirm|Starter)' -count=1
```

Expected: FAIL，缺失玩家 inventory 仍为空。

- [ ] **Step 3: 写最小材料包构造并只接入 missing 分支**

```go
func starterMaterialInventory() core.Inventory {
	items := [...]core.ItemID{
		core.ItemCobblestone, core.ItemSmoothStone, core.ItemSand, core.ItemGravel,
		core.ItemOakLog, core.ItemOakPlanks, core.ItemLeaves, core.ItemGlass,
		core.ItemBrick, core.ItemWhiteWool, core.ItemRoofTile, core.ItemClay,
		core.ItemSnowBlock, core.ItemMossyCobblestone,
	}
	var inventory core.Inventory
	for slot, item := range items {
		inventory.Backpack[slot] = core.ItemStack{Item: item, Count: core.MaxStackCount}
	}
	if !inventory.Valid() {
		panic("server: invalid starter material inventory")
	}
	return inventory
}
```

只在 `newMissingCachedPlayer` 的 `sim.PlayerSnapshot` 中设置 `Inventory: starterMaterialInventory()`；不得触碰 `cachedPlayerFromStored` 或 migration。

- [ ] **Step 4: 运行 GREEN 与 race**

Run:

```bash
gofmt -w internal/server/player_persistence.go internal/server/player_persistence_test.go
go test ./internal/server -run 'PlayerPersistence.*(Missing|Existing|Confirm|Starter)' -race -count=1
go test ./internal/server -race -count=1
```

Expected: 全部 PASS；故障注入保存失败测试仍保留重试语义。

- [ ] **Step 5: 勾选任务并提交**

```bash
git add internal/server/player_persistence.go internal/server/player_persistence_test.go openspec/changes/common-block-materials/tasks.md
git commit -m "feat: 为新玩家提供材料包"
```

---

### Task 5: 分离面可见性与 AO/天空光遮挡

**Files:**
- Modify: `internal/mesh/greedy.go`
- Modify: `internal/mesh/greedy_test.go`
- Modify: `internal/mesh/skylight_internal_test.go`
- Modify: `internal/assets/blocks.go`
- Modify: `internal/assets/blocks_test.go`
- Modify: `internal/assets/procedural_test.go`
- Modify: `openspec/changes/common-block-materials/tasks.md`

**Interfaces:**
- Consumes: `core.RegisteredBlock`、`GlassID`、`LeavesID`。
- Produces: `Registry.FaceVisible(id world.BlockID, adjacent world.BlockID) bool`；`Opaque` 只表达 AO/天空光遮挡。

- [ ] **Step 1: 写面语义 RED 测试**

在 assets 测试覆盖精确真值表：

```go
tests := []struct {
	name string
	id, adjacent core.BlockID
	want bool
}{
	{"空气不出面", core.AirID, core.AirID, false},
	{"未知当前方块不出面", core.MossyCobblestoneID + 1, core.AirID, false},
	{"石头面向空气", core.StoneID, core.AirID, true},
	{"石头被石头遮住", core.StoneID, core.StoneID, false},
	{"石头面向玻璃保留", core.StoneID, core.GlassID, true},
	{"玻璃被石头遮住", core.GlassID, core.StoneID, false},
	{"玻璃同类内部面剔除", core.GlassID, core.GlassID, false},
	{"树叶同类内部面剔除", core.LeavesID, core.LeavesID, false},
	{"不同 cutout 内部面剔除", core.GlassID, core.LeavesID, false},
	{"玻璃面向空气", core.GlassID, core.AirID, true},
}
```

在 mesh 测试构造相邻 glass/glass、leaves/leaves、stone/glass 两格；断言同类内部面不产生，stone/glass 边界只保留 stone 一面，孤立 glass 产生 6 面。另断言 glass/leaves 的 `Opaque=false`，因此 AO 和 `SkyLightScratch` 不把它们当遮挡物。

- [ ] **Step 2: 运行 RED**

Run:

```bash
go test ./internal/assets ./internal/mesh -run 'FaceVisible|Cutout|Glass|Leaves' -count=1
```

Expected: FAIL，`FaceVisible` 尚不存在且 glass/leaves 仍被 `Opaque` 当作实体面来源。

- [ ] **Step 3: 扩展最小接口并替换 greedy 判定**

```go
type Registry interface {
	Opaque(world.BlockID) bool
	FaceVisible(id world.BlockID, adjacent world.BlockID) bool
	Material(id world.BlockID, f Face) uint16
}
```

assets 实现：

```go
func (r *Registry) Opaque(id world.BlockID) bool {
	return core.RegisteredBlock(id) && id != core.AirID &&
		id != core.GlassID && id != core.LeavesID
}

func (r *Registry) FaceVisible(id, adjacent world.BlockID) bool {
	if !core.RegisteredBlock(id) || id == core.AirID || r.Opaque(adjacent) {
		return false
	}
	if !core.RegisteredBlock(adjacent) || adjacent == core.AirID {
		return true
	}
	return r.Opaque(id)
}
```

`MeshSection` 把原来的当前 `Opaque` 和相邻 `Opaque` 两个 guard 替换成：

```go
q := p
q[axis] += step
if !reg.FaceVisible(id, n.At(q[0], q[1], q[2])) {
	continue
}
```

所有测试 registry 增加同签名方法；测试 fake 可用 `return id != world.AirID && adjacent == world.AirID` 保持旧测试语义。`computeAO`、`SkyLightScratch`、`ComputeConnectivity` 继续只调用 `Opaque`。

- [ ] **Step 4: 运行 GREEN 和 mutation check**

Run:

```bash
gofmt -w internal/mesh/greedy.go internal/mesh/greedy_test.go internal/mesh/skylight_internal_test.go internal/assets/blocks.go internal/assets/blocks_test.go internal/assets/procedural_test.go
go test ./internal/assets ./internal/mesh ./internal/client ./internal/render -race -count=1
```

Expected: PASS。临时让 `FaceVisible` 在 cutout/cutout 时返回 true，新增内部面测试必须 FAIL；恢复后 PASS。

- [ ] **Step 5: 勾选任务并提交**

```bash
git add internal/mesh internal/assets/blocks.go internal/assets/blocks_test.go internal/assets/procedural_test.go openspec/changes/common-block-materials/tasks.md
git commit -m "feat: 分离方块面与遮挡语义"
```

---

### Task 6: 使用世界坐标 UV 并实现单 pass alpha cutout

**Files:**
- Modify: `internal/render/shader/terrain.wgsl`
- Create: `internal/render/terrain_shader_test.go`
- Modify: `internal/assets/blocks.go`
- Modify: `internal/assets/procedural.go`
- Modify: `internal/assets/blocks_test.go`
- Modify: `internal/assets/procedural_test.go`
- Modify: `internal/assets/atlas.go`
- Modify: `internal/assets/atlas_test.go`
- Modify: `internal/mesh/quad_test.go`
- Modify: `openspec/changes/common-block-materials/tasks.md`

**Interfaces:**
- Consumes: 现有 shader 的 `world`、`axis`、atlas repeat sampler；Task 2/5 的 `LeavesID`、`GlassID` 和 cutout 面语义。
- Produces: `LayerLeaves`、`LayerGlass` 及其基础 alpha `0/255` 纹理；WGSL `face_uv(world, axis)`；fragment alpha `<0.5` discard；`downsampleCutout(src []byte, size int) []byte`。

- [ ] **Step 1: 写 shader、mip 和实例格式 RED 测试**

创建 `package render` 的最小 source-contract 测试（不能使用外部 `render_test`，因为 `terrainShader` 是包内 embed 变量），并依靠 Task 8 的真实像素 golden 做端到端验证：

```go
func TestTerrainShaderUsesWorldUVAndAlphaCutout(t *testing.T) {
	source := string(terrainShader)
	for _, want := range []string{
		"fn face_uv(world: vec3f, axis: u32) -> vec2f",
		"out.uv    = face_uv(world, axis);",
		"if (c.a < 0.5) { discard; }",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("terrain shader 缺少 %q", want)
		}
	}
}
```

atlas 测试用一个 2×2 输入，其中仅一个 alpha 为 255，断言普通 `downsample` alpha 为 63、`downsampleCutout` alpha 为 255；全透明输入保持 0；RGB 仍等于普通平均值。另断言 `LayerLeaves` 与 `LayerGlass` 的基础 alpha 只含 0/255，且一路降采样到 1×1 仍保留至少一个不透明覆盖像素。`quad_test.go` 增加 `unsafe.Sizeof(mesh.Quad{}.Pack()) == 8` 的固定断言。

- [ ] **Step 2: 运行 RED**

Run:

```bash
go test ./internal/render ./internal/assets ./internal/mesh -run 'TerrainShader|Cutout|Pack' -count=1
```

Expected: FAIL，`LayerLeaves`/`LayerGlass`、shader 世界 UV/fragment discard 和 `downsampleCutout` 均未定义。

- [ ] **Step 3: 实现最小 WGSL 与覆盖保持 mip**

先在 atlas layer 清单末尾追加 `LayerLeaves`、`LayerGlass`，`NewRegistry` 生成并让 `Material(LeavesID/GlassID, face)` 返回对应层。只实现 cutout 所需的最小基础纹理，Task 7 再补结构细节：

```go
func leavesTexture() []byte {
	px := noisyTexture(rgb{R: 62, G: 126, B: 54}, 18, 0x1EA5)
	for y := 0; y < texSize; y++ {
		for x := 0; x < texSize; x++ {
			if hash2(uint32(x), uint32(y), 0x1EA5)%10 < 3 {
				px[(y*texSize+x)*4+3] = 0
			}
		}
	}
	return px
}

func glassTexture() []byte {
	px := make([]byte, texSize*texSize*4)
	frame := rgb{R: 188, G: 222, B: 226}
	for i := 0; i < texSize; i++ {
		paint(px, i, 0, frame); paint(px, i, texSize-1, frame)
		paint(px, 0, i, frame); paint(px, texSize-1, i, frame)
	}
	for _, p := range [][2]int{{0, 0}, {15, 0}, {0, 15}, {15, 15}} {
		paint(px, p[0], p[1], rgb{R: 142, G: 184, B: 190})
	}
	for i := 3; i < 7; i++ { paint(px, i, i, rgb{R: 224, G: 244, B: 246}) }
	return px
}
```

然后在 shader 中加入：

```wgsl
fn face_uv(world: vec3f, axis: u32) -> vec2f {
    if (axis == 0u) { return vec2f(world.y, world.z); }
    if (axis == 1u) { return vec2f(world.z, world.x); }
    return vec2f(world.x, world.y);
}
```

把 vertex 输出改为 `out.uv = face_uv(world, axis);`；fragment 改为：

```wgsl
let c = textureSample(atlas, atlas_smp, in.uv, i32(in.layer));
if (c.a < 0.5) { discard; }
return vec4f(c.rgb * in.shade, 1.0);
```

不增加 varying、quad 字段或 pipeline。cutout mip 复用普通 RGB 平均，只把 alpha 改为四个来源 alpha 的最大值：

```go
func downsampleCutout(src []byte, size int) []byte {
	dst := downsample(src, size)
	half := size / 2
	for y := 0; y < half; y++ {
		for x := 0; x < half; x++ {
			a := byte(0)
			for dy := 0; dy < 2; dy++ {
				for dx := 0; dx < 2; dx++ {
					a = max(a, src[((y*2+dy)*size+x*2+dx)*4+3])
				}
			}
			dst[(y*half+x)*4+3] = a
		}
	}
	return dst
}
```

`UploadTo` 仅对 `LayerLeaves` 和 `LayerGlass` 走 `downsampleCutout`，其他层继续走 `downsample`。

- [ ] **Step 4: 运行 GREEN、shader 编译与 mutation check**

Run:

```bash
gofmt -w internal/render/terrain_shader_test.go internal/assets/blocks.go internal/assets/procedural.go internal/assets/blocks_test.go internal/assets/procedural_test.go internal/assets/atlas.go internal/assets/atlas_test.go internal/mesh/quad_test.go
go test ./internal/render ./internal/assets ./internal/mesh -race -count=1
```

Expected: PASS；headless GPU 可用时 `TestUploadToHeadlessGPU` 成功编译/上传 shader 与 atlas，不可用时只允许既有明确 skip。临时恢复局部 UV 或删掉 discard 时 source-contract 测试必须 FAIL。

- [ ] **Step 5: 勾选任务并提交**

```bash
git add internal/render/shader/terrain.wgsl internal/render/terrain_shader_test.go internal/assets/blocks.go internal/assets/procedural.go internal/assets/blocks_test.go internal/assets/procedural_test.go internal/assets/atlas.go internal/assets/atlas_test.go internal/mesh/quad_test.go openspec/changes/common-block-materials/tasks.md
git commit -m "feat: 连续采样地形材质"
```

---

### Task 7: 生成 14 种自然像素材质并闭合草地边界

**Files:**
- Modify: `internal/assets/blocks.go`
- Modify: `internal/assets/procedural.go`
- Modify: `internal/assets/blocks_test.go`
- Modify: `internal/assets/procedural_test.go`
- Modify: `internal/render/hotbar_test.go`
- Modify: `openspec/changes/common-block-materials/tasks.md`

**Interfaces:**
- Consumes: Task 2 的 BlockID；Task 5 的 material registry；Task 6 已建立的 Leaves/Glass layers、alpha/mip 与 shader 语义。
- Produces: 其余 12 种材料的固定 atlas layers，并收紧 Leaves/Glass 的结构细节；OakLog 顶/底与侧面映射；Grass 周期边缘。

- [ ] **Step 1: 写材质结构与映射 RED 测试**

扩展现有确定性、16×16、非平坦测试，并加入固定映射：

```go
func TestCommonMaterialFaceMappings(t *testing.T) {
	r := assets.NewRegistry()
	if r.Material(core.OakLogID, mesh.FacePosY) != assets.LayerOakLogTop ||
		r.Material(core.OakLogID, mesh.FaceNegY) != assets.LayerOakLogTop ||
		r.Material(core.OakLogID, mesh.FacePosX) != assets.LayerOakLogSide {
		t.Fatal("竖向原木顶底/侧面映射错误")
	}
	for _, block := range []core.BlockID{
		core.CobblestoneID, core.SmoothStoneID, core.SandID, core.GravelID,
		core.OakPlanksID, core.LeavesID, core.GlassID, core.BrickID,
		core.WhiteWoolID, core.RoofTileID, core.ClayID,
		core.SnowBlockID, core.MossyCobblestoneID,
	} {
		if r.Material(block, mesh.FacePosX) != r.Material(block, mesh.FacePosY) && block != core.SnowBlockID {
			t.Fatalf("方块 %d 不应按面变化", block)
		}
	}
	if r.Material(core.SnowBlockID, mesh.FacePosY) == r.Material(core.SnowBlockID, mesh.FacePosX) {
		t.Fatal("雪块顶面与侧面应使用不同材质")
	}
}
```

测试固定结构而非整张 byte golden：圆石/砖/瓦有暗缝且亮度低于主体至少 18；平滑石不出现贯穿整行暗缝；沙/砾至少 6 个相邻颗粒；原木侧面有至少 4 条纵向暗带且顶面存在两个闭合亮度环；木板有错缝与至少 1 个结疤；羊毛有低对比纤维簇；黏土为冷灰蓝；雪顶平均亮度高于侧面；苔藓圆石与圆石有相同暗缝位置且至少 12 个绿色覆盖像素。

cutout 测试逐像素断言 Leaves/Glass alpha 只为 0 或 255；Leaves 透明率在 25%..35%；Glass 中心透明且四边至少各有一个不透明像素。草侧测试计算 16 列连续草缘深度，断言相邻列以及第 15→0 列的差绝对值都 `<=1`；草顶测试断言左右/上下边缘存在跨边界相邻的同色草簇。

- [ ] **Step 2: 运行 RED**

Run:

```bash
go test ./internal/assets ./internal/render -run 'CommonMaterial|Natural|Grass|Cutout|HotbarItem' -count=1
```

Expected: FAIL，新 layers/functions/mappings 未定义，草侧末列与首列深度可能相差超过 1。

- [ ] **Step 3: 追加固定 layers 与最小确定性生成函数**

`blocks.go` 在 Task 6 的 Leaves/Glass 后只追加其余 layer 常量，并在 `NewRegistry` 一次生成；Task 6 的两个 cutout layer 编号不得重排。固定函数和色板如下，不创建配置对象：

| 函数 | 基色/seed | 固定结构 |
|---|---|---|
| `cobblestoneTexture` | `rgb{116,118,120}`, `0xC0B1` | 4 个不规则石块与深色闭合缝 |
| `smoothStoneTexture` | `rgb{142,142,140}`, `0x5A10` | 低对比 4×4 矿物斑，不画贯穿缝 |
| `sandTexture` | `rgb{218,202,146}`, `0x5A2D` | 细颗粒与 3 组双像素亮点 |
| `gravelTexture` | `rgb{112,108,106}`, `0x6A41` | 4 组 2×2 深浅碎石 |
| `oakLogSideTexture` | `rgb{112,76,42}`, `0x0A61` | x=2,6,11,14 纵向树皮暗带 |
| `oakLogTopTexture` | `rgb{166,126,72}`, `0x0A62` | 以 (7.5,7.5) 为中心的两圈像素年轮 |
| `oakPlanksTexture` | `rgb{174,124,68}`, `0x0A63` | y=5,11 横缝、上下排错缝、一个 2×2 结疤 |
| `leavesTexture` | `rgb{62,126,54}`, `0x1EA5` | `hash2%10<3` 透明，其余深浅绿色簇 |
| `glassTexture` | `rgb{188,222,226}` | 透明中心、四边像素框、两段对角高光 |
| `brickTexture` | `rgb{154,74,58}`, `0xB21C` | y=5,11 灰缝与交错竖缝 |
| `whiteWoolTexture` | `rgb{226,222,210}`, `0x7001` | 低对比 2×2 纤维簇 |
| `roofTileTexture` | `rgb{138,62,46}`, `0x711E` | 三排重叠瓦弧与深色横缝 |
| `clayTexture` | `rgb{132,150,158}`, `0xC1A7` | 冷灰蓝细斑块 |
| `snowTopTexture`/`snowSideTexture` | `rgb{244,246,244}` / `rgb{214,228,236}` | 顶面亮白细晶点、侧面冷蓝细层 |
| `mossyCobblestoneTexture` | 复制圆石 | 仅在原像素平均亮度 `>=90` 时用 `hash2(...,0xA055)%5==0` 叠加绿色连片，保留深色石缝 |

复用现有 `noisyTexture`、`paint`、`fill`、`hash2` 和 `clamp8`。只有跨边界绘制需要一个最小 helper：

```go
func paintWrapped(px []byte, x, y int, color rgb) {
	x = (x%texSize + texSize) % texSize
	y = (y%texSize + texSize) % texSize
	paint(px, x, y, color)
}
```

草侧使用固定闭合深度序列，保证每步变化至多 1：

```go
depths := [...]int{3, 4, 4, 5, 6, 6, 5, 4, 3, 3, 4, 5, 5, 4, 3, 3}
```

草顶深/亮簇改用 `paintWrapped` 绘制跨 x=0/15 与 y=0/15 的簇。`Material` 明确映射所有新增 BlockID，default 仅作防御兜底；未知 ID 已在边界拒绝且 `FaceVisible=false`。

- [ ] **Step 4: 运行 GREEN、mutation check 与全层验证**

Run:

```bash
gofmt -w internal/assets/blocks.go internal/assets/procedural.go internal/assets/blocks_test.go internal/assets/procedural_test.go internal/render/hotbar_test.go
go test ./internal/assets ./internal/render -race -count=1
```

Expected: PASS；`NewRegistry()` 两次输出逐字节相等。临时把草侧 `depths[15]` 改为 7 或把 glass 中心 alpha 改为 128 时新增测试必须 FAIL；恢复后 PASS。

- [ ] **Step 5: 勾选任务并提交**

```bash
git add internal/assets internal/render/hotbar_test.go openspec/changes/common-block-materials/tasks.md
git commit -m "feat: 生成常见块状材料材质"
```

---

### Task 8: 增加无窗口材料展示并记录性能

**Files:**
- Modify: `cmd/mcgo/capture.go`
- Modify: `cmd/mcgo/capture_test.go`
- Modify: `internal/mesh/greedy_test.go`
- Create: `cmd/mcgo/testdata/golden/materials-showcase.png`
- Modify: `cmd/mcgo/testdata/golden/*.png`（只保留世界 UV 实际改变的既有场景）
- Modify local report: `.superpowers/sdd/2026-08-09-common-block-materials/task-8-report.md`（Task 1 已创建；被忽略，不提交）
- Modify: `openspec/changes/common-block-materials/tasks.md`

**Interfaces:**
- Consumes: Task 2..7 的全部注册、镜像、mesher、shader 和 atlas 行为；复用 `applyCaptureMirror` 与既有 3×3 空气快照模式。
- Produces: `func prepareMaterialsShowcase(*application) error`；`captureScenes` 末项 `materials-showcase`；无窗口 golden 与前后性能记录。

- [ ] **Step 1: 写展示夹具 RED 测试**

测试 `captureScenes[len(captureScenes)-1].Name == "materials-showcase"` 且 Prepare 非 nil。调用 Prepare 后逐点验证：

- 14 种材料按清单组成 7 列×2 行的 2×2 展示墙，列起点 `x=-10,-7,-4,-1,2,5,8`，下排 `y=1..2`、上排 `y=4..5`，墙面固定 `z=-8`；
- 8 格草带位于 `x=-4..3,y=0,z=-1`；`x=0..3,y=4,z=-2..0` 有石质遮棚以产生天空光/AO 拆分；
- glass 和 leaves 的 2×2 面板分别包含相邻同类方块；
- `OakLogID` 展示墙之外另有 `x=7,y=1..3,z=-1` 的竖直柱，摄像机可同时看到顶面和侧面；
- 所有装入 chunk revision 均为 2，mesher dirty 数非零。

- [ ] **Step 2: 运行 RED**

Run:

```bash
go test ./cmd/mcgo -run 'Capture.*MaterialsShowcase' -count=1
```

Expected: FAIL，场景和 Prepare 尚不存在。

- [ ] **Step 3: 实现固定夹具并追加场景**

`prepareMaterialsShowcase` 先像 `prepareSkylightTunnel` 一样装入 `x,z=-1..1` 的 3×3 空气快照，再按上述固定坐标生成、按 `world.ChunkBlockIndex` 排序并用 `network.BlockChanges{BaseRevision:1, NewRevision:2}` 装入。材料数组精确为：

```go
materials := [...]core.BlockID{
	core.CobblestoneID, core.SmoothStoneID, core.SandID, core.GravelID,
	core.OakLogID, core.OakPlanksID, core.LeavesID, core.GlassID,
	core.BrickID, core.WhiteWoolID, core.RoofTileID, core.ClayID,
	core.SnowBlockID, core.MossyCobblestoneID,
}
```

场景追加到列表末尾，显式重置所有可继承状态：

```go
{
	Name:         "materials-showcase",
	WarmupFrames: 8,
	Prepare:      prepareMaterialsShowcase,
	Apply: func(app *application) error {
		app.worldTimeTicks = 6000
		app.camera.Pos = mgl32.Vec3{0.5, 5.8, 13.5}
		app.camera.Yaw = 0
		app.camera.Pitch = -0.12
		app.inventoryOpen = false
		app.remotePlayers.Reset()
		app.furnace.Reset()
		app.chest.Reset()
		if app.panel != nil { app.panel.visible = false }
		return app.inventory.Apply(network.InventoryState{Inventory: core.Inventory{}})
	},
},
```

- [ ] **Step 4: 运行 GREEN 与无窗口收敛测试**

Run:

```bash
gofmt -w cmd/mcgo/capture.go cmd/mcgo/capture_test.go internal/mesh/greedy_test.go
go test ./cmd/mcgo -run 'Capture.*(MaterialsShowcase|Settled)' -race -count=1
```

Expected: PASS；不创建前台窗口。

- [ ] **Step 5: 记录修改后 benchmark（只记录，不设数值门禁）**

先让既有 terrain benchmark 报告本次最后一个网格结果的实例数和 8 字节上传量，不新增 benchmark 框架：

```go
var quads []mesh.Quad
b.ResetTimer()
for i := 0; i < b.N; i++ {
	quads = mesh.MeshSection(n, reg, light)
}
b.ReportMetric(float64(len(quads)), "quads/op")
b.ReportMetric(float64(len(quads)*8), "upload_bytes/op")
```

Run:

```bash
gofmt -w internal/mesh/greedy_test.go
go test ./internal/mesh -run '^$' -bench '^BenchmarkMeshTerrainSection$' -benchmem -count=5
go test ./internal/render -run '^$' -bench '^BenchmarkMeshChunk$' -benchmem -count=5
```

Expected: 两条命令结构性成功。把五次原始输出、中位 `ns/op`、`B/op`、`allocs/op`、`quads/op` 和 `upload_bytes/op` 追加到 Task 1 已建立的本地报告，与“实现前基线”并列记录；任何数值退化只记录，不让命令失败，也不得修改性能阈值。

- [ ] **Step 6: 无窗口更新、复核并检查全部视觉场景**

Run:

```bash
make visual-update VISUAL_OUT=/private/tmp/common-block-materials-visual
make visual-check VISUAL_OUT=/private/tmp/common-block-materials-visual-check
```

Expected: 两条命令均完成全部场景，第二条沿用 `MaxChannelDelta=2` 与 `MaxDiffPixelRatio=0.0001` 并通过；输出目录含每个 scene 的 PNG。使用本地图片查看工具逐张打开 `/private/tmp/common-block-materials-visual-check/*.png`，确认：14 种材料可区分、草带无相位断层、玻璃/树叶孔洞可信、原木顶/侧可见、既有场景无破损。删除或恢复无实际差异的 golden，只提交 `git diff --name-only cmd/mcgo/testdata/golden` 列出的真实变化。

- [ ] **Step 7: 勾选任务并提交**

```bash
git add cmd/mcgo/capture.go cmd/mcgo/capture_test.go internal/mesh/greedy_test.go cmd/mcgo/testdata/golden openspec/changes/common-block-materials/tasks.md
git commit -m "test: 增加常见材料视觉场景"
```

Expected: 本地 `task-8-report.md` 和三份用户日志不在提交中。

---

### Task 9: 全仓验证、同步主规格并归档

**Files:**
- Modify: `openspec/changes/common-block-materials/tasks.md`
- Modify through sync: `openspec/specs/common-block-materials/spec.md`
- Modify through sync: `openspec/specs/voxel-visual-presentation/spec.md`
- Modify through sync: `openspec/specs/visual-verification/spec.md`
- Move through archive: `openspec/changes/common-block-materials/**` → `openspec/changes/archive/2026-08-09-common-block-materials/**`
- Modify: `AGENTS.md`
- Modify: `CLAUDE.md`
- Modify: `openspec/config.yaml`

**Interfaces:**
- Consumes: Tasks 1..8 全部已提交实现、视觉 golden 和 record-only 性能报告。
- Produces: 严格通过且已归档的 OpenSpec change；同步后的主规格；M4N 基线说明保持三份文件一致。

- [ ] **Step 1: 核对规格覆盖与任务状态**

逐条把 active delta Requirement/Scenario 映射到测试名和提交；确认 Task 1..8 checkbox 全部已勾。运行：

```bash
openspec validate common-block-materials --strict --no-interactive
git diff --check
```

Expected: `0 failed`，无未实现 Requirement、无超出设计的方块状态/配方/世界生成。

- [ ] **Step 2: 运行受影响包和架构验证**

```bash
go test ./internal/core ./internal/world ./internal/network ./internal/storage ./internal/sim ./internal/server ./internal/assets ./internal/mesh ./internal/client ./internal/render ./cmd/mcgo -race -count=1
go test ./internal/archcheck -count=1
```

Expected: 全部 PASS。若 sandbox 仅因 Metal adapter 或 loopback bind 权限失败，必须用完全相同命令申请外部权限重跑；不得改变测试或启动前台窗口。

- [ ] **Step 3: 运行全仓质量验证**

```bash
go test ./... -race
go vet ./...
gofmt -l .
git diff --check
openspec validate --all --strict --no-interactive
```

Expected: tests/vet/OpenSpec 全部成功，`gofmt -l .` 和 `git diff --check` 无输出。性能只在 Task 8 报告记录，不比较数值阈值。

- [ ] **Step 4: 同步 delta 到主规格**

执行时必须调用 `openspec-sync-specs` skill，按 change 的 `existingOutputPaths` 同步：新建 `common-block-materials` 主规格，智能合并而非覆盖 `voxel-visual-presentation` 与 `visual-verification` 既有 Requirement。随后运行：

```bash
openspec validate --all --strict --no-interactive
git diff --check
```

Expected: active change 和全部主规格严格通过，既有视觉阈值与场景契约未丢失。

- [ ] **Step 5: 更新基线说明并保持 AGENTS/CLAUDE 一致**

把三份文件的当前基线最小更新为 M4N、协议 v14、玩家 schema v6、区块 schema v7，并补入 14 种常见块状材料、世界 UV、玻璃/树叶 cutout 和 `materials-showcase`。`AGENTS.md` 与 `CLAUDE.md` 的项目基线段必须逐字一致；不改工程规则和性能 record-only 规则。

- [ ] **Step 6: 勾选最终任务并归档**

勾选最后 checkbox 后，执行时必须调用 `openspec-archive-change` skill，把 change 精确归档到 `openspec/changes/archive/2026-08-09-common-block-materials/`，保留 `.openspec.yaml`。归档后运行：

```bash
openspec list --json
openspec validate --all --strict --no-interactive
git diff --check
```

Expected: active changes 不再包含 `common-block-materials`；全部主规格严格通过；归档内所有 task 已勾选。

- [ ] **Step 7: 提交收尾与归档**

```bash
git add openspec/specs openspec/changes/archive/2026-08-09-common-block-materials AGENTS.md CLAUDE.md openspec/config.yaml
git commit -m "docs: 归档常见块状材料体系"
```

Expected: 只提交实际存在的同步/归档/基线说明变化；工作区除三份用户日志外 clean。

---

## 完成判定

- 14 个 BlockID/ItemID 稳定追加且端到端放置、采掘、掉落、协议和存档 round-trip 全绿。
- 缺失玩家首次得到背包材料包，已有玩家和 identity migration 不被污染。
- 玻璃/树叶可见、不遮 AO/天空光、同类内部面剔除，完整碰撞不变。
- terrain 世界 UV 跨 quad/section/chunk 保持相位；草顶与草侧周期闭合。
- 所有材质确定性生成，alpha cutout/mip/8 字节实例格式契约有测试。
- `materials-showcase` 与所有既有场景仅用无窗口路径通过原双阈值并逐张复核。
- benchmark 只记录，无数值门禁被新增或放宽。
- 全仓 race、vet、gofmt、archcheck、diff check、OpenSpec strict 全绿，change 已同步并归档。
