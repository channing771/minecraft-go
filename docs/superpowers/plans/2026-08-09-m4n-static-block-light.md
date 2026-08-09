# M4N Static Block Light Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在不向服务端、线上协议或存档增加光照数组的前提下，交付一个可注入、可放置、可挖回但没有合成配方的完整发光块，并让客户端从权威方块镜像派生静态方块光。

**Architecture:** 在既有 `48³` mesher scratch 中把高四位继续用于天空光、低四位用于方块光；先完成天空光传播，再对全部固定亮度源执行多源 FIFO，最终把 packed byte 原样写入 `Quad.Light`。资源、放置与采掘仍由服务端权威，Memory/TCP 复用同一模拟路径；协议只扩展稳定枚举语义到 v14，区块 envelope 只升到 schema v7。

**Tech Stack:** Go 1.26、标准库、现有 `internal/core`/`sim`/`world`/`mesh`/`client`/`render`/`gfx`、WebGPU WGSL、OpenSpec、无窗口 capture、`cmd/perfcheck`。

## Global Constraints

- 实现前 MUST 读取 active change 的 `proposal.md`、全部 delta specs、`design.md` 与 `tasks.md`；实现与设计不一致时先更新 change 产物。
- Go MUST 使用现有 gvm Go 1.26；不得新增第三方依赖、worker pool、光照缓存、协议 packet、存档光照数组或服务端光照状态。
- `LightBlockID` 与 `ItemLightBlock` MUST 只追加在既有稳定枚举末尾；不得重排旧 ID，也不得新增 `RecipeID`。
- 发光块 MUST 是完整不透明立方体，固定 `Emission=15`；方块光只穿过空气，六向每格减 `1`，距离 `14` 为 `1`，距离 `15` 为 `0`，多源取最大。
- `Quad.Light` 高四位 MUST 保持天空光，低四位 MUST 表示方块光；shader MUST 用 `max(sky_base, block)` 合光，方块光不得受昼夜影响。
- 普通非列顶变化 dirty 集合 MUST 不超过 `27` 个区段且 MUST 完整覆盖所有实际受影响区段；列顶变化 MUST 继续不超过 `216` 个区段；不得新增专用 dirty 图或放宽队列上限。
- 每个 mesher worker MUST 复用一个精确 `48³` 的 levels 与一个同容量 FIFO；稳定构建 MUST 零分配，最坏输入 MUST 不溢出。
- 协议 MUST 唯一支持 v14；packet ID、payload 长度与字段布局不变。玩家 schema MUST 保持 v5，区块 schema MUST 升到 v7，metadata MUST 保持 v2。
- benchmark MUST 升到 scenario v15；唯一可授权的 workload 迁移是 `14:15`。M2 v6 基线保持字节不变；M5 v14 报告只保留为历史证据。
- 自动验证 MUST 无窗口；只允许本计划中的 offscreen capture 和 benchmark，不得启动交互式客户端。
- 代码注释、GoDoc、测试说明和文档 MUST 使用中文；Go 标识符、协议名与 wire 字段保留英文。
- 每个代码任务 MUST 按 red → green → refactor 执行，完成 focused race 验证后只提交该任务范围；不得改写 Hook 或使用豁免变量绕过门禁。

## File Structure

- Create: `openspec/changes/m4n-static-block-light/.openspec.yaml`
- Create: `openspec/changes/m4n-static-block-light/{proposal.md,design.md,tasks.md}`
- Create: `openspec/changes/m4n-static-block-light/specs/static-block-light/spec.md`
- Create: `openspec/changes/m4n-static-block-light/specs/{authoritative-daylight,authoritative-inventory,authoritative-mining,bounded-benchmark-workload,hardware-performance-baselines,visual-verification}/spec.md`
- Create: `internal/core/light_block_test.go` — 新 ID、物品映射与无配方契约。
- Modify: `internal/core/{block.go,item.go}` — 追加方块与物品。
- Modify: `internal/assets/{blocks.go,blocks_test.go,procedural.go}` — 独立材质层与固定发光等级。
- Modify: `internal/sim/{mining.go,mining_test.go}` — 石砖同级的发光块采掘规则。
- Modify: `internal/network/{packet.go,packet_test.go,codec_test.go,worldtime_test.go,drop_test.go}` — 协议 v14 与 v13 拒绝。
- Modify: `internal/storage/{chunk_codec.go,migration.go,migration_test.go,chunk_chest_test.go,chunk_codec_fuzz_test.go,player_codec_test.go}` — schema v7 no-op 迁移、fuzz 语料与玩家 v5 保真。
- Create: `internal/storage/chunk_light_block_test.go`
- Create: `internal/storage/testdata/chunk-v7.bin`
- Rename: `internal/mesh/skylight.go` → `internal/mesh/light.go`
- Rename: `internal/mesh/skylight_test.go` → `internal/mesh/light_test.go`
- Rename: `internal/mesh/skylight_internal_test.go` → `internal/mesh/light_internal_test.go`
- Modify: `internal/mesh/{greedy.go,greedy_test.go,light.go,light_test.go,light_internal_test.go}` — packed 双通道传播。
- Modify: `internal/client/{mesher.go,skylight_test.go}` — worker scratch 类型、dirty 与过期结果覆盖。
- Modify: `cmd/gfxspike/main.go`, `internal/render/{bench_test.go,daylight_test.go,shader/terrain.wgsl}` — 调用点与 shader 合光。
- Modify: `cmd/mcgo/{capture.go,capture_test.go}` — `block-light-room` fixture。
- Create: `cmd/mcgo/testdata/golden/block-light-room.png`
- Create: `internal/server/block_light_integration_test.go` — Memory/TCP 共用纵向脚本。
- Modify: `cmd/mcgo/{benchmark.go,benchmark_v5_test.go,benchmark_v6_test.go}` — scenario v15。
- Modify: `cmd/perfcheck/{main.go,main_test.go}` — 唯一 `14:15` 迁移。
- Modify: `docs/notes/{perf-baseline-m5.json,perf-baseline.md,perf-baseline-m5.md}` — v15 记录与当前 M5 Memory 基线。
- Modify: `README.md`, `docs/notes/lan-server.md`, `AGENTS.md`, `CLAUDE.md`, `openspec/config.yaml` — 已交付能力和版本边界。

---

### Task 1: 建立并严格校验 M4N OpenSpec change

**Files:**
- Create: `openspec/changes/m4n-static-block-light/.openspec.yaml`
- Create: `openspec/changes/m4n-static-block-light/proposal.md`
- Create: `openspec/changes/m4n-static-block-light/design.md`
- Create: `openspec/changes/m4n-static-block-light/tasks.md`
- Create: `openspec/changes/m4n-static-block-light/specs/static-block-light/spec.md`
- Create: `openspec/changes/m4n-static-block-light/specs/{authoritative-daylight,authoritative-inventory,authoritative-mining,bounded-benchmark-workload,hardware-performance-baselines,visual-verification}/spec.md`

**Interfaces:**
- Consumes: `docs/superpowers/specs/2026-08-09-m4n-static-block-light-design.md` 的全部已批准决策。
- Produces: active change `m4n-static-block-light`；后续任务不得超出其 delta specs。

- [ ] **Step 1: 重新确认分支、基线与 active change**

Run:

```bash
git status --short --branch
openspec list --json
zsh -ic 'go version'
```

Expected: 当前分支是 M4N 功能分支、工作树无用户未归类改动、active change 列表为空、Go 输出 `go1.26`。若出现其他 active change，先判断是否与 M4N 文件边界冲突，不直接覆盖。

- [ ] **Step 2: 用 `openspec-propose` 生成最小完整 change**

先加载 `openspec-propose` skill。`.openspec.yaml` 精确写为：

```yaml
schema: spec-driven
created: 2026-08-09
```

`proposal.md` MUST 明确：新增完整发光块和无配方物品、客户端派生静态方块光、协议 v14、区块 schema v7、scenario v15；非目标逐条列出真实火把、透明/彩色/动态光、正常获取入口、服务端光照状态和新 packet。

`design.md` MUST 固化以下实现选择：

```text
LightBlockID = ChestID + 1
ItemLightBlock = ItemChest + 1
Emission(LightBlockID) = 15
levels 高四位天空光、低四位方块光
一个 48³ levels、一个 48³ queue、每 worker 复用
先天空光，后所有光源同时入队的方块光 BFS
普通 dirty <= 27、列顶 dirty <= 216
shader: max(0.08 + sky*(daylight-0.08), block)
protocol v14；chunk schema v7；player schema v5；metadata v2
benchmark scenario v15；唯一迁移 14:15
```

`tasks.md` MUST 逐项对应本计划 Task 2–9，并为每项写出 focused 命令；不得把归档列为自动步骤。

- [ ] **Step 3: 写七份可判定 delta spec**

`static-block-light/spec.md` 新增以下 Requirements，每条至少包含成功和边界 Scenario：

```text
### Requirement: 发光块与物品 ID 稳定且没有正常获取入口
### Requirement: 客户端从权威方块镜像确定性派生静态方块光
### Requirement: 方块光更新在既有有界 mesher 路径收敛
### Requirement: packed 光照在 shader 中按最大值合成
### Requirement: 发光块兼容协议 v14 与区块 schema v7
```

其他 delta MUST 完整复制被修改的主规格 Requirement 后作以下精确变更：

- `authoritative-daylight`: 线上协议 `v13→v14`、区块 schema `v6→v7`；删除“低四位固定为 0”，改为高四位天空光/低四位方块光，保持玩家 v5、metadata v2。
- `authoritative-inventory`: “协议 v13 严格有界”改为 v14；把 `ItemLightBlock` 纳入有效完整物品和普通整格放置，但六条固定配方仍精确为六条。
- `authoritative-mining`: 在石砖/熔炉/箱子同档加入发光块：无正确镐 `30` tick 无掉落、石镐 `15`、铁镐 `8`，正确镐掉落一个发光块物品。
- `bounded-benchmark-workload`: 当前 workload 改为 v15；同版本 v15 可比；默认拒绝 v14/v15；唯一显式迁移改为 `14:15`；`13:14` 只保留历史同版本报告，不再可授权。
- `hardware-performance-baselines`: 当前 M5 基线必须来自完整 Memory v15；用 `14:15` 完整性和硬件身份校验后精确提升；TCP v15 独立记录；M2 v6 不变。
- `visual-verification`: 新增末尾场景 `block-light-room`；封闭房间午夜只由一个发光块照亮，房外无边界漏光，未收敛或 golden 超阈值失败。

- [ ] **Step 4: 严格校验并做范围审计**

Run:

```bash
openspec validate --all --strict --no-interactive
rg -n "LightBlockID|ItemLightBlock|48³|27|216|协议 v14|schema v7|schema v5|metadata v2|scenario v15|14:15|没有.*配方|block-light-room" openspec/changes/m4n-static-block-light
rg -n "透明|彩色|动态|服务端.*光照|新.*packet" openspec/changes/m4n-static-block-light
git diff --check
```

Expected: strict 全通过；冻结值全部命中；第二个 `rg` 只命中非目标或否决方案，不命中实施任务。

- [ ] **Step 5: 提交 OpenSpec change**

```bash
git add openspec/changes/m4n-static-block-light
git commit -m "docs: 建立 M4N 静态方块光变更"
```

### Task 2: 追加发光块资源、物品与采掘规则

**Files:**
- Create: `internal/core/light_block_test.go`
- Modify: `internal/core/block.go`
- Modify: `internal/core/item.go`
- Modify: `internal/assets/blocks.go`
- Modify: `internal/assets/blocks_test.go`
- Modify: `internal/assets/procedural.go`
- Modify: `internal/sim/mining.go`
- Modify: `internal/sim/mining_test.go`

**Interfaces:**
- Produces: `core.LightBlockID`, `core.ItemLightBlock`。
- Produces: `(*assets.Registry).Emission(world.BlockID) uint8`。
- Preserves: `core.RecipeChest` 仍是最后一个 recipe ID；正常合成入口仍只有六条。

- [ ] **Step 1: 先写资源契约红灯**

在 `internal/core/light_block_test.go` 写出这些精确断言：

```go
func TestLightBlockIDsMappingsAndNoRecipeStayStable(t *testing.T) {
	if core.LightBlockID != core.ChestID+1 {
		t.Fatalf("LightBlockID=%d，必须追加在 ChestID 之后", core.LightBlockID)
	}
	if core.ItemLightBlock != core.ItemChest+1 {
		t.Fatalf("ItemLightBlock=%d，必须追加在 ItemChest 之后", core.ItemLightBlock)
	}
	if limit, ok := core.ItemStackLimit(core.ItemLightBlock); !ok || limit != 64 {
		t.Fatalf("发光块堆叠上限=(%d,%v)，想要 (64,true)", limit, ok)
	}
	if block, ok := core.ItemPlacement(core.ItemLightBlock); !ok || block != core.LightBlockID {
		t.Fatalf("发光块放置映射=(%d,%v)", block, ok)
	}
	if item, ok := core.BlockDrop(core.LightBlockID); !ok || item != core.ItemLightBlock {
		t.Fatalf("发光块掉落映射=(%d,%v)", item, ok)
	}
	if core.RecipeChest != 6 {
		t.Fatalf("最后一个固定配方 ID=%d，想要 6", core.RecipeChest)
	}
	if _, ok := core.Recipe(core.RecipeChest + 1); ok {
		t.Fatal("发光块不得增加配方")
	}
}
```

在 `internal/assets/blocks_test.go` 增加 `TestLightBlockUsesIndependentLayerAndFixedEmission`，断言材质层不同于 stone/chest、RGBA 长度为 `16*16*4`、alpha 全为 `255`、`Emission(LightBlockID)==15`、其他已知和未知 ID 为 `0`。

在 `internal/sim/mining_test.go` 的 `TestMiningRule` 表中增加：

```go
{name: "发光块空手", block: core.LightBlockID, held: core.ItemNone, ticks: 30, harvestable: false},
{name: "发光块石镐", block: core.LightBlockID, held: core.ItemStonePickaxe, ticks: 15, harvestable: true},
{name: "发光块铁镐", block: core.LightBlockID, held: core.ItemIronPickaxe, ticks: 8, harvestable: true},
{name: "发光块普通物品", block: core.LightBlockID, held: core.ItemStone, ticks: 30, harvestable: false},
```

- [ ] **Step 2: 运行 focused 测试确认红灯**

Run:

```bash
zsh -ic 'go test ./internal/core ./internal/assets ./internal/sim -run "LightBlock|MiningRule" -count=1'
```

Expected: 因新 ID、`Emission` 和 mining case 尚不存在而编译或断言失败。

- [ ] **Step 3: 实现最小稳定资源映射**

在枚举末尾追加：

```go
// 方块 ID 是协议稳定值，不能重排。
const (
	AirID BlockID = iota
	BarrierID
	StoneID
	DirtID
	GrassID
	BedrockID
	StoneBrickID
	CoalOreID
	IronOreID
	FurnaceID
	IronBlockID
	ChestID
	LightBlockID
)
```

物品枚举在 `ItemChest` 后追加 `ItemLightBlock`，并只把它加入现有三个 switch：

```go
case LightBlockID:
	return ItemLightBlock, true
```

```go
case ItemStone, ItemDirt, ItemGrass, ItemStoneBrick, ItemCoal,
	ItemRawIron, ItemIronIngot, ItemFurnace, ItemIronBlock, ItemChest, ItemLightBlock:
	return MaxStackCount, true
```

```go
case ItemLightBlock:
	return LightBlockID, true
```

不得修改 `internal/core/recipe.go`。

- [ ] **Step 4: 增加独立程序化材质和发光属性**

在 layer 枚举末尾追加 `LayerLightBlock`，`NewRegistry` 生成该层，`Material` 返回该层。材质函数固定为：

```go
func lightBlockTexture() []byte {
	px := noisyTexture(rgb{R: 238, G: 196, B: 76}, 8, 0x4C17)
	frame := rgb{R: 164, G: 106, B: 30}
	for i := 0; i < texSize; i++ {
		paint(px, i, 0, frame)
		paint(px, i, texSize-1, frame)
		paint(px, 0, i, frame)
		paint(px, texSize-1, i, frame)
	}
	fill(px, 4, 4, 12, 12, rgb{R: 255, G: 226, B: 112})
	return px
}
```

注册表发光接口只需一个 switch：

```go
// Emission 返回方块固定发出的方块光等级。实现 mesh.Registry。
func (r *Registry) Emission(id world.BlockID) uint8 {
	if id == core.LightBlockID {
		return 15
	}
	return 0
}
```

- [ ] **Step 5: 把发光块加入既有石砖采掘分支**

只扩展共享 case，不创建新规则表：

```go
case core.StoneBrickID, core.FurnaceID, core.ChestID, core.LightBlockID,
	core.CoalOreID, core.IronOreID:
```

运行已有完成采掘测试时同时覆盖掉落容量不足与错误工具不掉落；若现有表没有对 `LightBlockID` 的这两个路径，向同一表追加用例，不创建第二套模拟装配。

- [ ] **Step 6: 格式化、验证并提交**

Run:

```bash
gofmt -w internal/core/block.go internal/core/item.go internal/core/light_block_test.go internal/assets/blocks.go internal/assets/blocks_test.go internal/assets/procedural.go internal/sim/mining.go internal/sim/mining_test.go
zsh -ic 'go test ./internal/core ./internal/assets ./internal/sim -race -count=1'
zsh -ic 'go test ./internal/archcheck -count=1'
gofmt -l internal/core internal/assets internal/sim
```

Expected: 全部通过且 `gofmt -l` 无输出。

```bash
git add internal/core/block.go internal/core/item.go internal/core/light_block_test.go internal/assets/blocks.go internal/assets/blocks_test.go internal/assets/procedural.go internal/sim/mining.go internal/sim/mining_test.go
git commit -m "feat: 添加静态发光块资源"
```

### Task 3: 升级协议 v14 与区块 schema v7

**Files:**
- Modify: `internal/network/packet.go`
- Modify: `internal/network/{packet_test.go,codec_test.go,worldtime_test.go,drop_test.go}`
- Modify: `internal/storage/chunk_codec.go`
- Modify: `internal/storage/migration.go`
- Modify: `internal/storage/{migration_test.go,chunk_chest_test.go,chunk_codec_fuzz_test.go,player_codec_test.go}`
- Create: `internal/storage/chunk_light_block_test.go`
- Create: `internal/storage/testdata/chunk-v7.bin`

**Interfaces:**
- Produces: `network.ProtocolVersion == 14`，只接受 v14 握手。
- Produces: `currentChunkSchema == 7`，注册 `6→7` no-op 迁移。
- Preserves: packet ID/payload layout、玩家 schema v5、metadata v2、既有 v6 fixture 字节。

- [ ] **Step 1: 写协议和存档红灯**

把版本锁定测试更新为：

```go
func TestProtocolVersionIsFourteen(t *testing.T) {
	if ProtocolVersion != 14 {
		t.Fatalf("协议版本=%d，想要 14", ProtocolVersion)
	}
}
```

`TestProtocolV14RejectsPriorVersionsBeforePlay` MUST 遍历 `version := uint32(1); version < ProtocolVersion; version++`，从而明确覆盖 v13。codec golden 的 hello 字节更新为：

```go
{"hello", StateHandshake, ClientHello{ProtocolVersion: 14}, 0, "0e"},
{"server hello", StateHandshake, ServerHello{ProtocolVersion: 14}, 0, "0e"},
{"handshake reject", StateHandshake, HandshakeReject{ServerProtocolVersion: 14, Code: HandshakeVersionMismatch, Message: "no"}, 1, "0e01026e6f"},
```

在 `internal/storage/chunk_light_block_test.go` 写：

```go
func TestChunkV6MigratesToV7WithoutChangingPayloadSemantics(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "chunk-v6.bin"))
	if err != nil {
		t.Fatal(err)
	}
	key := core.ChunkKey{
		Dimension: core.Overworld,
		Pos:       core.ChunkPos{X: -3, Z: 7},
	}
	got, err := decodeChunkPayload(key, 19, data)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Migrated || got.Schema != 7 {
		t.Fatalf("v6 迁移结果 schema=%d migrated=%v", got.Schema, got.Migrated)
	}
}
```

再增加 `TestChunkV7RoundTripsLightBlockAndDrop`：区块中放一个 `LightBlockID`，持久掉落写一个 `ItemLightBlock`，编码后解码并逐字段相等；`TestPlayerSchemaV5RoundTripsLightBlockItem` 只验证 v5 roundtrip，不重生成玩家 golden。

- [ ] **Step 2: 确认红灯且冻结 v6 fixture**

Run:

```bash
git hash-object internal/storage/testdata/chunk-v6.bin
zsh -ic 'go test ./internal/network ./internal/storage -run "Protocol|CodecGolden|ChunkV6|ChunkV7|LightBlock" -count=1'
```

Expected: 版本和 schema 断言失败；记录第一条 hash，Task 结束前必须相同。

- [ ] **Step 3: 做最小版本升级**

实现精确常量和迁移项：

```go
// ProtocolVersion 是 M4N 唯一支持的协议版本。
const ProtocolVersion uint32 = 14
```

```go
const currentChunkSchema uint32 = 7
```

```go
6: func(dto chunkDTO) (chunkDTO, error) {
	// v7 只扩展稳定方块与物品语义，payload 布局不变。
	return dto, nil
},
```

不要修改任何 packet ID、codec 字段顺序、玩家 schema 常量或 metadata 常量。

- [ ] **Step 4: 生成并锁定 v7 golden**

`TestChunkV7RoundTripsLightBlockAndDrop` 沿用 `updateStorageFixtures` flag；更新模式写 `testdata/chunk-v7.bin`，普通模式读取并比较确定性编码。Run:

```bash
zsh -ic 'go test ./internal/storage -run TestChunkV7RoundTripsLightBlockAndDrop -update-storage-fixtures -count=1'
zsh -ic 'go test ./internal/storage -run "ChunkV6|ChunkV7|PlayerSchemaV5" -count=1'
git hash-object internal/storage/testdata/chunk-v6.bin
```

Expected: v7 fixture 新增；v6 hash 与 Step 2 完全一致。

同时把 `chunk-v7.bin` 追加到 `FuzzDecodeChunkPayload` 的 committed seed 列表；v2–v6 项不得删除。

- [ ] **Step 5: 跑 golden、故障边界和 focused race**

Run:

```bash
gofmt -w internal/network/packet.go internal/network/packet_test.go internal/network/codec_test.go internal/network/worldtime_test.go internal/network/drop_test.go internal/storage/chunk_codec.go internal/storage/migration.go internal/storage/migration_test.go internal/storage/chunk_chest_test.go internal/storage/chunk_codec_fuzz_test.go internal/storage/player_codec_test.go internal/storage/chunk_light_block_test.go
zsh -ic 'go test ./internal/network ./internal/storage -race -count=1'
zsh -ic 'go test ./internal/storage -run "Future|CRC|Trunc|Trailing|Migration" -count=1'
zsh -ic 'go test ./internal/storage -run=^$ -fuzz=FuzzDecodeChunkPayload -fuzztime=10s'
zsh -ic 'go test ./internal/archcheck -count=1'
gofmt -l internal/network internal/storage
git diff --check
```

- [ ] **Step 6: 提交**

```bash
git add internal/network/packet.go internal/network/packet_test.go internal/network/codec_test.go internal/network/worldtime_test.go internal/network/drop_test.go internal/storage/chunk_codec.go internal/storage/migration.go internal/storage/migration_test.go internal/storage/chunk_chest_test.go internal/storage/chunk_codec_fuzz_test.go internal/storage/player_codec_test.go internal/storage/chunk_light_block_test.go internal/storage/testdata/chunk-v7.bin
git commit -m "feat: 升级静态光资源兼容版本"
```

### Task 4: 在固定 scratch 中实现 packed 双通道传播

**Files:**
- Rename: `internal/mesh/skylight.go` → `internal/mesh/light.go`
- Rename: `internal/mesh/skylight_test.go` → `internal/mesh/light_test.go`
- Rename: `internal/mesh/skylight_internal_test.go` → `internal/mesh/light_internal_test.go`
- Modify: `internal/mesh/greedy.go`
- Modify: `internal/mesh/greedy_test.go`
- Modify: `internal/client/mesher.go`
- Modify: `cmd/gfxspike/main.go`
- Modify: `internal/render/bench_test.go`

**Interfaces:**
- Renames: `SkyLightScratch` → `LightScratch`，`NewSkyLightScratch` → `NewLightScratch`。
- Extends: `mesh.Registry` with `Emission(world.BlockID) uint8`。
- Preserves: 一个 `[48³]uint8` levels、一个 `[48³]uint32` queue、现有 `MeshSection` 输出与 worker 数量。

- [ ] **Step 1: 先重命名测试文件和测试类型，不改算法**

把三个文件改名，并机械替换类型名、构造器名及 panic 文本中的“天空光”→“光照”。同步所有调用点和测试 registry，测试 registry 默认实现：

```go
func (testRegistry) Emission(id world.BlockID) uint8 {
	if id == core.LightBlockID {
		return 15
	}
	return 0
}
```

先运行：

```bash
zsh -ic 'go test ./internal/mesh ./internal/client ./internal/render ./cmd/gfxspike -run "SkyLight|Mesh|Mesher" -count=1'
```

Expected: 纯重命名后既有天空光测试仍通过；此提交点先不提交。

- [ ] **Step 2: 写方块光传播红灯**

在 `internal/mesh/light_test.go` 把采样 helper 拆成高低四位：

```go
func skyLight(light uint8) uint8   { return light >> 4 }
func blockLight(light uint8) uint8 { return light & 0x0f }
```

新增这些测试，每个都通过 `MeshSection` 观察可见面的相邻空气 packed light，而不是读取内部 levels：

```text
TestBlockLightSourceFaceSamplesAdjacentFourteen
TestBlockLightFallsToOneAtDistanceFourteenAndZeroAtFifteen
TestBlockLightStopsAtNonAirBlockEvenWhenMarkedNonOpaque
TestBlockLightMultipleSourcesTakeMaximum
TestBlockLightCrossesSectionBoundary
TestBlockLightCrossesChunkBoundary
TestBlockLightMissingNeighborStaysDark
TestPackedSkyAndBlockLightBuildIsDeterministic
```

固定夹具 MUST 包含非空气地面，避免 uniform-air center 触发 `MeshSection` 快速返回；光源格为 `15`，其可见面采样相邻空气应得到低四位 `14`。阻断用例 MUST 让一个非 `AirID` 测试方块在 registry 中返回 `Opaque=false`，证明方块光仍不穿过它。

Run:

```bash
zsh -ic 'go test ./internal/mesh -run "BlockLight|PackedSky" -count=1'
```

Expected: 低四位仍为 `0`，新增用例失败。

- [ ] **Step 3: 写内部容量与零分配红灯**

在 `internal/mesh/light_internal_test.go` 保留既有 exact-capacity 与 `AllocsPerRun` 测试，并新增：

```text
TestLightScratchWorstCaseMultipleSourcesFitsExactQueue
TestLightScratchBuildScansEachCellOnceForEmission
TestLightScratchReusesQueueBetweenSkyAndBlockPasses
```

`countingRegistry` 分别计数 `Opaque` 与 `Emission`；完整 `48³` 邻域应有精确 `48*48*48` 次光源扫描，稳定 build 的 `AllocsPerRun(100, build)` 必须为 `0`。

- [ ] **Step 4: 实现 packed scratch 和两个固定 pass**

保留现有数组大小，只统一命名：

```go
const (
	lightMin    = -core.SectionSize
	lightSide   = 3 * core.SectionSize
	lightVolume = lightSide * lightSide * lightSide
	skyMask     = uint8(0xf0)
	blockMask   = uint8(0x0f)
)

type LightScratch struct {
	levels [lightVolume]uint8
	queue  [lightVolume]uint32
	head   int
	tail   int
}

func NewLightScratch() *LightScratch { return new(LightScratch) }
```

`build` 的唯一顺序是：

```go
func (s *LightScratch) build(n *world.Neighborhood, reg Registry) {
	clear(s.levels[:])
	s.head, s.tail = 0, 0
	s.buildSky(n, reg)
	s.head, s.tail = 0, 0
	s.buildBlock(n, reg)
}
```

方块光 seed pass MUST 先扫描并入队全部源，随后才传播：

```go
for x := lightMin; x < lightMin+lightSide; x++ {
	for y := lightMin; y < lightMin+lightSide; y++ {
		for z := lightMin; z < lightMin+lightSide; z++ {
			level := reg.Emission(n.At(x, y, z))
			if level == 0 {
				continue
			}
			if level > 15 {
				panic("mesh: 方块发光等级超过 15")
			}
			index := lightIndex(x, y, z)
			s.levels[index] = s.levels[index]&skyMask | level
			s.enqueue(index)
		}
	}
}
```

BFS 对六个轴向邻格执行：域外跳过；当前低四位 `<=1` 停止；目标不是 `AirID` 时跳过，不以 `Registry.Opaque` 决定方块光传播；只有 `next > existing` 才写入并入队。多源可能让空气格被更亮路径更新，因此队列容量证明必须以算法实际入队上限为准；若测试证明单数组 FIFO 会因重复提升超限，最小修复是让全等级 15 源同时入队并以 FIFO 首次到达即最短路径，不增加第二个队列或动态容器。

- [ ] **Step 5: 让 `MeshSection` 原样写 packed byte**

接口与写入精确改为：

```go
type Registry interface {
	Opaque(world.BlockID) bool
	Material(id world.BlockID, f Face) uint16
	Emission(world.BlockID) uint8
}

func MeshSection(n *world.Neighborhood, reg Registry, light *LightScratch) []Quad
```

```go
light: light.at(q[0], q[1], q[2]),
```

不得改变 AO、greedy merge key、quad 数量或 neighborhood clone。

- [ ] **Step 6: 验证传播、容量、分配和调用点**

Run:

```bash
gofmt -w internal/mesh/light.go internal/mesh/light_test.go internal/mesh/light_internal_test.go internal/mesh/greedy.go internal/mesh/greedy_test.go internal/client/mesher.go cmd/gfxspike/main.go internal/render/bench_test.go
zsh -ic 'go test ./internal/mesh -race -count=1'
zsh -ic 'go test ./internal/client ./internal/render ./cmd/gfxspike -race -count=1'
zsh -ic 'go test ./internal/mesh -run TestLightScratchExactCapacityAndStableBuildDoesNotAllocate -count=1'
zsh -ic 'go test ./internal/mesh -run ^$ -bench BenchmarkMeshTerrainSection -benchmem -count=5'
zsh -ic 'go test ./internal/archcheck -count=1'
gofmt -l internal/mesh internal/client cmd/gfxspike internal/render
rg -n "SkyLightScratch|NewSkyLightScratch|skylight.go" . --glob '!docs/superpowers/**'
```

Expected: 全通过；`LightScratch` 稳定构建的 `AllocsPerRun` 为 `0`，`BenchmarkMeshTerrainSection` 正常完成且 `allocs/op` 不高于既有基线 `5`；最后 `rg` 无生产或测试代码命中。

- [ ] **Step 7: 提交**

```bash
git add -A internal/mesh/skylight.go internal/mesh/skylight_test.go internal/mesh/skylight_internal_test.go internal/mesh/light.go internal/mesh/light_test.go internal/mesh/light_internal_test.go internal/mesh/greedy.go internal/mesh/greedy_test.go internal/client/mesher.go cmd/gfxspike/main.go internal/render/bench_test.go
git commit -m "feat: 派生静态方块光"
```

### Task 5: 验证 mesher 收敛并接入 shader 合光

**Files:**
- Modify: `internal/client/skylight_test.go`
- Modify: `internal/render/shader/terrain.wgsl`
- Modify: `internal/render/daylight_test.go`

**Interfaces:**
- Consumes: Task 4 的 packed `Quad.Light`。
- Preserves: Mirror 的 `27/216` dirty 集合、generation/revision/presence 过期拒绝、既有 terrain pipeline 与 uniform 布局。
- Produces: `base = max(sky_base, block)` 的日夜无关方块光呈现。

- [ ] **Step 1: 写 dirty 与过期结果红灯**

在现有 `internal/client/skylight_test.go` 追加：

```text
TestMirrorLightBlockPlacementDirtiesWithinTwentySevenAndCoversAffectedSections
TestMirrorLightBlockRemovalDirtiesWithinTwentySevenAndCoversAffectedSections
TestMirrorLightBlockColumnTopChangeStaysWithinTwoHundredSixteenSections
TestMesherDiscardsStaleBlockLightAfterRemoval
```

前两项在非列顶位置分别把 `AirID↔LightBlockID`，断言 dirty 去重后 `<=27`，并逐个断言所有实际受影响区段都包含在 dirty 集合中；不得锁死恰好 `27`。第三项把发光块置于/移出列顶并断言 `<=216` 且无重复；最后一项按既有 roof stale 测试步骤执行：排队含光源任务、移除光源并提高 revision、先提交旧结果、断言旧 packed 低四位未发布且新区段最终为 `0`。

Run:

```bash
zsh -ic 'go test ./internal/client -run "LightBlock|StaleBlockLight" -count=1'
```

Expected: dirty 测试应直接证明现有边界足够；stale 测试在实现或测试 helper 尚未支持低四位时失败。若 dirty 测试通过，不修改 `mirror.go`。

- [ ] **Step 2: 写 headless shader 红灯**

扩展 `TestTerrainDaylightHeadlessDraw` 的固定 quads，至少包含：

```go
mesh.Quad{X: 0, Y: 0, Z: 0, W: 1, H: 1, Face: mesh.FacePosY, Light: 0xf0},
mesh.Quad{X: 2, Y: 0, Z: 0, W: 1, H: 1, Face: mesh.FacePosY, Light: 0x0f},
mesh.Quad{X: 4, Y: 0, Z: 0, W: 1, H: 1, Face: mesh.FacePosY, Light: 0x88},
```

同一 offscreen draw 分别用正午与午夜 daylight uniform 回读像素，断言：全天空光面午夜显著变暗；全方块光面正午/午夜色差在现有容差内；`0x88` 在午夜由方块光主导；改变 AO 或面朝向仍降低最终亮度。

Run:

```bash
zsh -ic 'go test ./internal/render -run TestTerrainDaylightHeadlessDraw -count=1'
```

Expected: 当前 shader 忽略低四位，午夜发光面断言失败。

- [ ] **Step 3: 只修改 terrain fragment 合光公式**

把当前 sky/base 段替换为：

```wgsl
let sky = f32((light >> 4u) & 0xFu) / 15.0;
let block = f32(light & 0xFu) / 15.0;
let daylight = clamp(scene.daylight_params.x, 0.0, 1.0);
let sky_base = 0.08 + sky * (daylight - 0.08);
let base = max(sky_base, block);
out.shade = face_shade * ao_factor * base;
```

不得新增 uniform、bind group、pipeline、纹理或 draw call。

- [ ] **Step 4: 验证客户端与渲染**

Run:

```bash
gofmt -w internal/client/skylight_test.go internal/render/daylight_test.go
zsh -ic 'go test ./internal/client ./internal/render -race -count=1'
zsh -ic 'go test ./internal/archcheck -count=1'
gofmt -l internal/client internal/render
git diff --check
```

- [ ] **Step 5: 提交**

```bash
git add internal/client/skylight_test.go internal/render/shader/terrain.wgsl internal/render/daylight_test.go
git commit -m "feat: 合成天空光与方块光"
```

### Task 6: 增加 `block-light-room` 无窗口视觉场景

**Files:**
- Modify: `cmd/mcgo/capture.go`
- Modify: `cmd/mcgo/capture_test.go`
- Create: `cmd/mcgo/testdata/golden/block-light-room.png`

**Interfaces:**
- Produces: capture 列表末尾固定场景 `block-light-room`。
- Preserves: 640×360、现有双阈值、场景顺序、无窗口 offscreen 路径。

- [ ] **Step 1: 写 fixture 和顺序红灯**

把现有“末场景是 skylight-tunnel”测试改为按名字查找 skylight 场景，并新增：

```go
func TestBlockLightRoomIsLastCaptureScene(t *testing.T) {
	got := captureScenes[len(captureScenes)-1]
	if got.Name != "block-light-room" || got.Prepare == nil || got.Apply == nil {
		t.Fatalf("末场景=%+v，想要完整 block-light-room", got)
	}
}
```

再写 `TestPrepareBlockLightRoomUsesMirrorAndMesher`：执行 `Prepare` 后断言房间中心块是 `LightBlockID`、房间壳是 `StoneID`、房外为空气、所有变化都经 `applyCaptureMirror` 产生 dirty；写 `TestBlockLightRoomApplyResetsSharedPresentationState`，先污染 inventory/panel/remote players/furnace/chest，再断言 Apply 全部显式清空并设午夜和固定相机。

Run:

```bash
zsh -ic 'go test ./cmd/mcgo -run "BlockLightRoom|CaptureScene" -count=1'
```

Expected: 场景尚不存在，测试失败。

- [ ] **Step 2: 用现有 mirror fixture 路径构建封闭房间**

从 `prepareSkylightTunnel` 提取只负责装入 `x,z=-1..1` 全空气快照的 `prepareCaptureAirNeighborhood`，让 tunnel 与 room 都调用它；不要新建通用场景 DSL。

`prepareBlockLightRoom` 固定构造：

```text
房间外壳：x=-6..6，z=-10..2
地板：y=0
天花板：y=6
四壁：x=-6、x=6、z=-10、z=2，y=1..5
唯一光源：(0,3,-4) = LightBlockID
其余内部：AirID
```

按 chunk 分组 `BlockChange`，每组按 `world.ChunkBlockIndex` 排序，再通过：

```go
applyCaptureMirror(app, network.BlockChanges{
	Dimension:    core.Overworld,
	Chunk:        chunk,
	BaseRevision: 1,
	NewRevision:  2,
	Changes:      changes,
})
```

房间必须完全封闭；缺失邻区保持不透明且暗，不能作为室内亮度来源。

- [ ] **Step 3: 把场景追加到列表末尾**

场景值固定为：

```go
{
	Name:         "block-light-room",
	WarmupFrames: 8,
	Prepare:      prepareBlockLightRoom,
	Apply: func(app *application) error {
		app.worldTimeTicks = 18000
		app.camera.Pos = mgl32.Vec3{0.5, 2.8, 0.5}
		app.camera.Yaw = 0
		app.camera.Pitch = 0
		app.inventoryOpen = false
		if app.panel != nil {
			app.panel.visible = false
		}
		if err := app.inventory.Apply(network.InventoryState{Inventory: core.Inventory{}}); err != nil {
			return fmt.Errorf("重置物品栏: %w", err)
		}
		app.remotePlayers.Reset()
		app.furnace.Reset()
		app.chest.Reset()
		return nil
	},
},
```

- [ ] **Step 4: 验证 fixture 后生成全部 golden**

Run:

```bash
gofmt -w cmd/mcgo/capture.go cmd/mcgo/capture_test.go
zsh -ic 'go test ./cmd/mcgo -run "Capture|BlockLightRoom|Visual" -count=1'
mkdir -p /private/tmp/mcgo-m4n-capture
zsh -ic 'go run ./cmd/mcgo --capture /private/tmp/mcgo-m4n-capture --update-golden'
zsh -ic 'go run ./cmd/mcgo --capture /private/tmp/mcgo-m4n-capture-verify'
```

Expected: `--update-golden` 成功后，若既有六张 golden 的重抓结果仅在冻结双阈值内漂移，则把它们恢复为 HEAD 字节并只保留新增 `block-light-room.png`；随后普通 capture verify MUST 在同一冻结阈值内通过。任一既有场景超阈值、不得恢复或语义不成立时停止；不得修改阈值或 capture 比较实现。

- [ ] **Step 5: 人工检查唯一新增图像语义**

打开 `cmd/mcgo/testdata/golden/block-light-room.png`，确认：中央暖色发光块可见；墙面由近到远衰减；午夜没有天空光伪亮；房外和边界没有亮缝。若语义不成立，修 fixture 或生产根因后重生成，不调整阈值。

- [ ] **Step 6: 提交**

```bash
git add cmd/mcgo/capture.go cmd/mcgo/capture_test.go cmd/mcgo/testdata/golden/block-light-room.png
git commit -m "test: 添加方块光视觉基线"
```

### Task 7: 完成 Memory/TCP 放置、照明、挖回纵向闭环

**Files:**
- Create: `internal/server/block_light_integration_test.go`

**Interfaces:**
- Consumes: `openParityTransport`、`parityStep`、`hostTestConfig`、`flatGenerator`、`inventoryDigest` 与 `itemDropDigest`。
- Produces: 同一业务脚本在 Memory/TCP 上得到一致区块、背包、掉落与 packed 方块光。

- [ ] **Step 1: 写共用脚本和 parity 红灯**

新测试 MUST 使用 `package server`，从而直接复用 `tcp_integration_test.go` 的 transport 装配。结果结构固定为：

```go
type staticBlockLightResult struct {
	BeforeLight   uint8
	PlacedLight   uint8
	RemovedLight  uint8
	ChunkHash     [32]byte
	ChunkRevision uint64
	InventoryHash [32]byte
	DropHash      [32]byte
}
```

入口测试：

```go
func TestStaticBlockLightMemoryTCPParity(t *testing.T) {
	memory := runStaticBlockLightScript(t, "memory")
	tcp := runStaticBlockLightScript(t, "tcp")
	if !reflect.DeepEqual(memory, tcp) {
		t.Fatalf("Memory/TCP 方块光结果不一致\nmemory=%+v\ntcp=%+v", memory, tcp)
	}
	if memory.BeforeLight != 0 || memory.PlacedLight == 0 || memory.RemovedLight != 0 {
		t.Fatalf("方块光生命周期=%+v", memory)
	}
}
```

Run:

```bash
zsh -ic 'go test ./internal/server -run TestStaticBlockLightMemoryTCPParity -count=1'
```

Expected: 脚本尚未完成或发光块链路缺失，测试失败。

- [ ] **Step 2: 按现有 mining parity 装配权威玩家**

`runStaticBlockLightScript` 对两种 transport 使用完全相同的数据：

```go
inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemLightBlock, Count: 1}
stoneFull, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
inventory.Hotbar.Slots[1] = core.ItemStack{
	Item: core.ItemStonePickaxe, Count: 1, Durability: stoneFull,
}
```

玩家位置、视角和目标沿用现有 `runParityTranscript` 已验证的 `(0,1,-5)` 放置/采掘射线，只有物品与目标块改为发光块。脚本顺序固定：等待 Ready 和 3×3 镜像 → 构建无光初始 mesh → slot 0 放置 → 等待 `BlockChanges`/inventory 与 mesher 收敛 → 切 slot 1 持续采掘 15 tick → 等待掉落、区块 revision 和 mesh 收敛 → 断开并 `Host.Shutdown`。

- [ ] **Step 3: 只通过生产 mesher 读取 packed 低四位**

每个阶段使用 `assets.NewRegistry()`、`client.NewMesher()` 与已接受的 `client.Mirror` neighborhood；采样目标可见面：

```go
func meshedBlockLight(quads []mesh.Quad, target core.BlockPos) uint8 {
	lx, ly, lz := target.Local()
	for _, quad := range quads {
		if quad.Face != mesh.FacePosY || int(quad.Y) != ly {
			continue
		}
		if int(quad.X) <= lx && lx < int(quad.X)+int(quad.H) &&
			int(quad.Z) <= lz && lz < int(quad.Z)+int(quad.W) {
			return quad.Light & 0x0f
		}
	}
	return 0
}
```

不要在 server test 重新实现 BFS。测试注入只允许初始玩家存档；方块变化、物品扣减、工具耐久、掉落和持久化必须走真实权威路径。

- [ ] **Step 4: 验证 transport 一致性和 race**

Run:

```bash
gofmt -w internal/server/block_light_integration_test.go
zsh -ic 'go test ./internal/server -run "StaticBlockLight|MiningMemoryTCPParity" -race -count=1'
zsh -ic 'go test ./internal/server -race -count=1'
zsh -ic 'go test ./internal/archcheck -count=1'
gofmt -l internal/server
```

Mutation: 暂时让 `assets.Registry.Emission` 对 `LightBlockID` 返回 `0`，确认 parity 测试的 `PlacedLight` 断言失败；恢复后重跑为 `ok`。

- [ ] **Step 5: 提交**

```bash
git add internal/server/block_light_integration_test.go
git commit -m "test: 覆盖静态方块光纵向闭环"
```

### Task 8: 升级 scenario v15 并生成 M5 记录

**Files:**
- Modify: `cmd/mcgo/benchmark.go`
- Modify: `cmd/mcgo/benchmark_v5_test.go`
- Modify: `cmd/mcgo/benchmark_v6_test.go`
- Modify: `cmd/perfcheck/main.go`
- Modify: `cmd/perfcheck/main_test.go`
- Modify: `docs/notes/perf-baseline-m5.json`
- Modify: `docs/notes/perf-baseline.md`
- Modify: `docs/notes/perf-baseline-m5.md`

**Interfaces:**
- Produces: benchmark `scenario_version=15`。
- Produces: 唯一 `--allow-scenario-upgrade 14:15`。
- Preserves: 分辨率、阶段时长、样本、指标、20% 记录阈值、错误门禁、M2 v6 baseline。

- [ ] **Step 1: 写 scenario 和迁移矩阵红灯**

把 producer 锁定测试改名并断言：

```go
func TestBenchmarkScenarioVersionIncludesStaticBlockLightWorkload(t *testing.T) {
	if scenarioVersion != 15 {
		t.Fatalf("scenarioVersion=%d，想要静态方块光 workload 之后的 v15", scenarioVersion)
	}
}
```

新增 `completeV15ComparableReport(transport string)`，保留所有 v14 完整字段，仅把 `ScenarioVersion` 设为 `15`。迁移矩阵必须包含：

```go
{14, 15, "14:15", false},
{15, 14, "14:15", true},
{13, 14, "13:14", true},
{13, 15, "13:15", true},
{14, 14, "14:15", true},
{15, 15, "14:15", true},
```

再断言 Memory v14→v15 通过完整性/硬件校验且不执行相对回归；Memory/TCP transport 不同即使带 `14:15` 也拒绝迁移；同 commit 的 Memory/TCP v15 只有显式 cross-transport 比较才可比较。

Run:

```bash
zsh -ic 'go test ./cmd/mcgo ./cmd/perfcheck -run "ScenarioVersion|ScenarioUpgrade|StaticBlockLight" -count=1'
```

Expected: 当前 producer 为 14 且 perfcheck 只接受 `13:14`，测试失败。

- [ ] **Step 2: 做唯一常量和授权字符串修改**

精确修改：

```go
scenarioVersion = 15
```

```go
allowScenarioUpgrade := flag.String(
	"allow-scenario-upgrade", "", "只允许显式的 14:15 场景迁移",
)
```

`compareReportsWithScenarioUpgrade` 只接受 `baseline.ScenarioVersion==14`、`current.ScenarioVersion==15`、授权字符串 `14:15`；删除当前 active 的 `13:14` 接受分支，不删除 v13/v14 报告解析和同版本比较支持。

- [ ] **Step 3: 跑比较器与 producer focused 验证**

Run:

```bash
gofmt -w cmd/mcgo/benchmark.go cmd/mcgo/benchmark_v5_test.go cmd/mcgo/benchmark_v6_test.go cmd/perfcheck/main.go cmd/perfcheck/main_test.go
zsh -ic 'go test ./cmd/mcgo ./cmd/perfcheck -race -count=1'
zsh -ic 'go test ./internal/client ./internal/mesh ./internal/render -race -count=1'
git diff --check
```

- [ ] **Step 4: 冻结运行目录并生成 Memory v15**

Run:

```bash
mkdir -p /private/tmp/mcgo-m4n-v15
git rev-parse HEAD
TERM=xterm-256color zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport memory --perf-output '/private/tmp/mcgo-m4n-v15/memory-v15.json'"
zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/perfcheck --baseline docs/notes/perf-baseline-m5.json --current '/private/tmp/mcgo-m4n-v15/memory-v15.json' --max-regression 0.20 --allow-scenario-upgrade 14:15"
```

Expected: producer 写出完整有效 JSON；perfcheck 输出迁移记录成功。性能数值只记录，但缺字段、身份、overflow、数据丢失或 I/O 错误必须失败。

- [ ] **Step 5: 精确提升 Memory 基线并独立生成 TCP v15**

在迁移验证成功后，精确复制 Memory 报告到 `docs/notes/perf-baseline-m5.json`，并验证字节一致；随后独立运行 TCP：

```bash
cp /private/tmp/mcgo-m4n-v15/memory-v15.json docs/notes/perf-baseline-m5.json
cmp -s /private/tmp/mcgo-m4n-v15/memory-v15.json docs/notes/perf-baseline-m5.json
TERM=xterm-256color zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport tcp --perf-output '/private/tmp/mcgo-m4n-v15/tcp-v15.json'"
zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/perfcheck --baseline '/private/tmp/mcgo-m4n-v15/tcp-v15.json' --current '/private/tmp/mcgo-m4n-v15/tcp-v15.json' --max-regression 0.20"
```

不要自动执行 Memory↔TCP 跨 transport 比较；该比较只在用户显式要求时运行。

- [ ] **Step 6: 记录命令、身份、hash 与历史边界**

在 `perf-baseline.md` 和 `perf-baseline-m5.md` 顶部新增 M4N/v15 节：记录 HEAD、硬件/OS/Go、三条正式命令、Memory/TCP 报告路径、Memory 基线 SHA-256、M2 基线 hash 未变、v14 为历史证据、性能数值 record-only。不得删除旧记录。

Run:

```bash
shasum -a 256 docs/notes/perf-baseline.json docs/notes/perf-baseline-m5.json /private/tmp/mcgo-m4n-v15/memory-v15.json /private/tmp/mcgo-m4n-v15/tcp-v15.json
zsh -ic 'go run ./cmd/perfcheck --baseline docs/notes/perf-baseline-m5.json --current docs/notes/perf-baseline-m5.json --max-regression 0.20'
git diff --check
```

- [ ] **Step 7: 提交**

```bash
git add cmd/mcgo/benchmark.go cmd/mcgo/benchmark_v5_test.go cmd/mcgo/benchmark_v6_test.go cmd/perfcheck/main.go cmd/perfcheck/main_test.go docs/notes/perf-baseline-m5.json docs/notes/perf-baseline.md docs/notes/perf-baseline-m5.md
git commit -m "perf: 升级静态方块光场景基线"
```

### Task 9: 更新现状文档并运行全部门禁

**Files:**
- Modify: `README.md`
- Modify: `docs/notes/lan-server.md`
- Modify: `AGENTS.md`
- Modify: `CLAUDE.md`
- Modify: `openspec/config.yaml`
- Modify: `openspec/changes/m4n-static-block-light/tasks.md`

**Interfaces:**
- Produces: M4N 已交付能力、协议 v14、chunk schema v7、player schema v5、metadata v2、scenario v15 的一致现状说明。
- Preserves: 明确“无配方/无正常获取入口”、TCP 可信 LAN 限制、设计文档不等于已实现。

- [ ] **Step 1: 先做文档漂移红灯扫描**

Run:

```bash
rg -n "当前.*M4M|协议 v13|Protocol v13|区块 schema v6|scenario v14|13:14|skylight-tunnel.*末|低四位.*0" README.md docs/notes/lan-server.md AGENTS.md CLAUDE.md openspec/config.yaml
```

Expected: 命中当前现状段落，证明必须更新；历史记录中的 v13/v14 不在本步骤删除范围。

- [ ] **Step 2: 只更新当前能力与操作边界**

当前现状统一写为：

```text
M4N：协议 v14；玩家 schema v5；区块 schema v7；metadata v2；
客户端从权威方块镜像派生传播天空光和静态方块光；
发光块可放置、可用石/铁镐挖回，但没有配方、初始发放、世界生成或管理命令；
benchmark scenario v15；M5 当前基线为 Memory v15，TCP v15 独立记录；M2 v6 不变。
```

README 的 capture 场景清单在末尾追加 `block-light-room`。LAN 文档写清 v13 客户端在 Play 前拒绝、升级前停服备份、回退需恢复备份，不承诺降级写回。不要把真实火把、透明光照或动态熔炉写成已实现。

- [ ] **Step 3: 勾选 active change 已完成任务但不归档**

逐条对照测试证据，把 `openspec/changes/m4n-static-block-light/tasks.md` 中确实完成的 checkbox 改为 `[x]`。任一未完成项保持 `[ ]` 并继续对应任务；不得为了归档而跳过性能、golden 或全仓门禁。

- [ ] **Step 4: 跑 focused、架构、全仓和静态门禁**

Run:

```bash
zsh -ic 'go test ./internal/core ./internal/assets ./internal/sim ./internal/network ./internal/storage ./internal/mesh ./internal/client ./internal/render ./internal/server ./cmd/mcgo ./cmd/perfcheck -race -count=1'
zsh -ic 'go test ./internal/archcheck -count=1'
zsh -ic 'go test ./... -race'
zsh -ic 'go vet ./...'
gofmt -l .
openspec validate --all --strict --no-interactive
git diff --check
```

Expected: 所有命令成功；`gofmt -l .` 无输出。

- [ ] **Step 5: 重跑 artifact 门禁**

Run:

```bash
zsh -ic 'go test ./internal/storage -count=1'
zsh -ic 'go test ./internal/storage -run=^$ -fuzz=FuzzDecodeChunkPayload -fuzztime=10s'
zsh -ic 'go run ./cmd/mcgo --capture /private/tmp/mcgo-m4n-final-capture'
zsh -ic 'go run ./cmd/perfcheck --baseline docs/notes/perf-baseline-m5.json --current docs/notes/perf-baseline-m5.json --max-regression 0.20'
cmp -s /private/tmp/mcgo-m4n-v15/memory-v15.json docs/notes/perf-baseline-m5.json
```

Expected: storage/fuzz/capture/perfcheck 全通过，M5 基线仍是已验证 Memory v15 的精确字节。

- [ ] **Step 6: 做变更范围和规格一致性审计**

Run:

```bash
git status --short
git diff --stat main...HEAD
git diff --check main...HEAD
rg -n "LightBlockID|ItemLightBlock|ProtocolVersion.*14|currentChunkSchema.*7|scenarioVersion.*15|14:15|block-light-room" internal cmd openspec README.md docs AGENTS.md CLAUDE.md
rg -n "RecipeLight|透明光|彩色光|动态光|服务端光照数组" internal cmd
```

Expected: 第一组完整命中交付面；第二组无实现代码命中。独立 review MUST 特别检查：删掉 `Emission`、删掉低四位 shader 解码、接受 v13、移除 stale rejection 或改回 scenario v14 时，至少一项自动测试失败。

- [ ] **Step 7: 提交文档与任务状态**

```bash
git add README.md docs/notes/lan-server.md AGENTS.md CLAUDE.md openspec/config.yaml openspec/changes/m4n-static-block-light/tasks.md
git commit -m "docs: 更新 M4N 静态方块光现状"
```

- [ ] **Step 8: 请求代码评审并停止在 active change**

加载 `superpowers:requesting-code-review`，按每个 task commit、OpenSpec Requirements、focused/full test 输出和 mutation 证据做独立 review。修复所有 P0/P1 和范围内 P2 后，重新运行 Step 4–6。保持 change active；只有用户随后明确要求时才执行 sync/archive、推送或创建 PR。

---

## Plan Self-Review Checklist

- [ ] 每项批准行为都至少落在一个 OpenSpec Scenario、一个生产改动和一个会杀死回归的测试中。
- [ ] 没有占位代码、未命名 helper、未来预留接口或新增依赖。
- [ ] `core.BlockID`/`ItemID`、`world.BlockID`、`mesh.Registry` 和测试 registry 的类型一致。
- [ ] 玩家 schema v5、metadata v2、packet 布局、M2 v6 baseline 和六条 recipe 均保持不变。
- [ ] 所有二进制新增物只有项目生成的 storage golden 与 PNG golden，没有版权材质。
- [ ] 每个任务可单独 review、验证和提交；Task 9 前不声称 M4N 完成。
