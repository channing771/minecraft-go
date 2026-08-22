# Mornlea Texture Pack Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在不改变协议、存档或 Rust ABI 的前提下，把经许可核验并固定来源的 Pixel Perfection 16×16 子集作为客户端默认材质，并允许用户用启动时读取的本地目录覆盖同名纹理。

**Architecture:** 保留 `assets.NewRegistry()` 作为完整的程序化基线；`internal/assets` 依次应用嵌入默认包和可选用户目录，缺失文件逐层回退，格式错误立即失败。材质名称只映射到既有 39 个固定 layer，不允许材质包改变方块/面映射；现有 atlas、mesh 和 Rust 上传路径保持不变。

**Tech Stack:** Go 1.26 标准库（`embed`、`io/fs`、`image/png`、`image/draw`、`encoding/json`）、现有 Rust 1.97.1 构建链、OpenSpec、Go test、现有 visual capture/golden 工具。

**Spec:** `docs/superpowers/specs/2026-08-21-mornlea-texture-pack-design.md`

## Global Constraints

- [ ] 开始执行前确认 `openspec/changes/rust-engine-lod-shell` 已归档或从 active changes 消失，并把实现分支更新到其合并后的最新 `main`；若门禁不满足，停止，不并行修改 capture、atlas、mesh 或 Rust ABI。
- [ ] 每个任务由新的 implementer 子代理完成，随后分别进行独立规格评审和代码质量评审；修复循环与裁决记录到 `openspec/changes/mornlea-texture-pack/ledger.md`，单任务最多 5 轮。
- [ ] 先写失败测试，再写最小实现；不新增第三方 Go 依赖、运行时转换器、ZIP/网络下载、热重载、多分辨率、动画或 PBR。
- [ ] 不因本变更推进 LOD 合并后基线的协议 v23、engine ABI v6、client ABI v7 或 benchmark scenario v18；存档 schema 同样保持不变。
- [ ] 不把 Mojang 资源或未核实授权的二进制资源加入仓库；Pixel Perfection 文件必须来自一个完整的、不可变 upstream commit，并携带许可证、署名、修改说明和逐文件来源表。
- [ ] `mornlea-server` 不导入、不打开、不打包材质资源；benchmark/capture 忽略用户 `texturePackPath`，但 capture 使用嵌入默认材质。
- [ ] 不改 visual/perf 阈值来让门禁通过。

## Planned File Structure

```text
openspec/changes/mornlea-texture-pack/
├── .openspec.yaml
├── proposal.md
├── design.md
├── tasks.md
├── ledger.md
└── specs/
    ├── texture-pack-loading/spec.md
    ├── tunable-constants/spec.md
    ├── visual-verification/spec.md
    └── voxel-visual-presentation/spec.md
internal/assets/
├── pack.go
├── pack_test.go
├── default_pack.go
├── default_pack_test.go
└── packs/pixel_perfection/
    ├── pack.json
    ├── textures/*.png
    ├── ATTRIBUTION.md
    ├── LICENSE.txt
    └── PROVENANCE.json
internal/config/
├── config.go
└── config_test.go
cmd/mornlea/
├── app.go
├── app_dependencies.go
├── app_startup.go
├── app_startup_test.go
├── main.go
└── run_test.go
docs/texture-packs.md
README.md
Makefile
AGENTS.md
CLAUDE.md
docs/notes/progress.md
```

---

### Task 1: Rebase gate and OpenSpec change

**Files:**

- Create: `openspec/changes/mornlea-texture-pack/.openspec.yaml`
- Create: `openspec/changes/mornlea-texture-pack/proposal.md`
- Create: `openspec/changes/mornlea-texture-pack/design.md`
- Create: `openspec/changes/mornlea-texture-pack/tasks.md`
- Create: `openspec/changes/mornlea-texture-pack/ledger.md`
- Create: `openspec/changes/mornlea-texture-pack/specs/texture-pack-loading/spec.md`
- Create: `openspec/changes/mornlea-texture-pack/specs/tunable-constants/spec.md`
- Create: `openspec/changes/mornlea-texture-pack/specs/visual-verification/spec.md`
- Create: `openspec/changes/mornlea-texture-pack/specs/voxel-visual-presentation/spec.md`
- Reference: `openspec/config.yaml`
- Reference: `openspec/specs/{tunable-constants,visual-verification,voxel-visual-presentation}/spec.md`

- [ ] **Step 1: Prove the prerequisite and update the base**

```bash
git status --short
git branch --show-current
test ! -d openspec/changes/rust-engine-lod-shell
git fetch origin main
git rebase origin/main
```

Expected: no active `rust-engine-lod-shell`; the implementation starts from its merged result. If the worktree is dirty or the active change still exists, stop and ask the controller to resolve ownership instead of stashing or rewriting another contributor's files.

- [ ] **Step 2: Write the change artifacts from the approved design**

Use change id `mornlea-texture-pack`. The proposal must state that the client gains an embedded adapted Pixel Perfection default plus a local directory override, while the dedicated server and all network/storage contracts are unchanged.

The delta specs must require these observable scenarios:

- `texture-pack-loading`: valid startup-only directory override; layer-by-layer fallback; explicit invalid configured pack fails startup before window/store/network side effects; fixed 16×16 RGBA inputs; no remapping.
- `tunable-constants`: optional top-level `texturePackPath`, resolved relative to the config file; benchmark/capture ignore local user values; config schema remains v1.
- `voxel-visual-presentation`: embedded default replaces procedural visuals for mapped layers; the existing procedural registry remains the fallback; UV/cutout/water/plant geometry and atlas upload contracts are unchanged.
- `visual-verification`: goldens use the embedded default, include the merged `far-horizon` scene immediately before terminal `water-underwater`, and keep existing comparison thresholds plus the LOD near-band guard.

`design.md` must include the exact load order, size limits, error policy, direct-copy source mapping, immutable source pin, license obligations, startup side-effect ordering and non-goals. `tasks.md` mirrors Tasks 2–8 below. Initialize the ledger with columns for task, implementer, spec review, quality review, iteration and ruling.

- [ ] **Step 3: Validate the planning artifacts**

Run:

```bash
openspec validate mornlea-texture-pack --strict --no-interactive
git diff --check
```

Expected: validation succeeds and whitespace check is empty.

- [ ] **Step 4: Commit the change artifacts**

```bash
git add openspec/changes/mornlea-texture-pack \
	docs/superpowers/specs/2026-08-21-mornlea-texture-pack-design.md \
	docs/superpowers/plans/2026-08-21-mornlea-texture-pack.md
git commit -m "docs: propose texture pack loading"
```

---

### Task 2: Implement the bounded directory-pack loader

**Files:**

- Create: `internal/assets/pack.go`
- Create: `internal/assets/pack_test.go`
- Modify: `internal/assets/blocks.go`

- [ ] **Step 1: Write one table-driven failing loader test**

Use `testing/fstest.MapFS` and test the production entry point directly. Cover:

1. a valid `pack.json` plus one 16×16 PNG replaces only its named layer, including an RGBA leaves/glass PNG with intermediate alpha;
2. an absent known texture leaves the previous layer bytes unchanged;
3. missing manifest, malformed JSON, unsupported format, invalid UTF-8, empty/over-128-byte name, malformed PNG, non-16×16 image, over-limit manifest and over-limit texture return contextual errors;
4. entries whose `fs.File.Stat` result is non-regular are rejected;
5. unknown manifest fields are ignored and emit one `slog.Warn`;
6. known texture processing follows the fixed layer table, independent of directory iteration order, while unknown files are never opened;
7. one invalid present texture leaves every registry layer unchanged.

The smallest useful test helper may encode a solid 16×16 PNG in memory. Do not create fixture directories.

Run:

```bash
go test ./internal/assets -run 'TestApplyPack' -count=1
```

Expected: FAIL because the loader does not exist.

- [ ] **Step 2: Add the fixed logical-name table**

In `blocks.go`, keep `NewRegistry()` unchanged as the procedural constructor. Add only the unexported binding type and table:

```go
type textureBinding struct {
	name  string
	layer uint16
}

var textureBindings = [...]textureBinding{
	{name: "stone", layer: LayerStone},
	{name: "dirt", layer: LayerDirt},
	{name: "grass_top", layer: LayerGrassTop},
	{name: "grass_side", layer: LayerGrassSide},
	{name: "bedrock", layer: LayerBedrock},
	{name: "stone_brick", layer: LayerStoneBrick},
	{name: "coal_ore", layer: LayerCoalOre},
	{name: "iron_ore", layer: LayerIronOre},
	{name: "furnace", layer: LayerFurnace},
	{name: "iron_block", layer: LayerIronBlock},
	{name: "chest", layer: LayerChest},
	{name: "light_block", layer: LayerLightBlock},
	{name: "leaves", layer: LayerLeaves},
	{name: "glass", layer: LayerGlass},
	{name: "cobblestone", layer: LayerCobblestone},
	{name: "smooth_stone", layer: LayerSmoothStone},
	{name: "sand", layer: LayerSand},
	{name: "gravel", layer: LayerGravel},
	{name: "oak_log_side", layer: LayerOakLogSide},
	{name: "oak_log_top", layer: LayerOakLogTop},
	{name: "oak_planks", layer: LayerOakPlanks},
	{name: "brick", layer: LayerBrick},
	{name: "white_wool", layer: LayerWhiteWool},
	{name: "roof_tile", layer: LayerRoofTile},
	{name: "clay", layer: LayerClay},
	{name: "snow_top", layer: LayerSnowTop},
	{name: "snow_side", layer: LayerSnowSide},
	{name: "mossy_cobblestone", layer: LayerMossyCobblestone},
	{name: "water", layer: LayerWater},
	{name: "farmland_dry", layer: LayerFarmlandDry},
	{name: "farmland_wet", layer: LayerFarmlandWet},
	{name: "wheat_0", layer: LayerWheat0},
	{name: "wheat_1", layer: LayerWheat1},
	{name: "wheat_2", layer: LayerWheat2},
	{name: "wheat_3", layer: LayerWheat3},
	{name: "wheat_4", layer: LayerWheat4},
	{name: "wheat_5", layer: LayerWheat5},
	{name: "wheat_6", layer: LayerWheat6},
	{name: "wheat_7", layer: LayerWheat7},
}
```

Add a test asserting `len(textureBindings) == int(layerCount)`, unique names, unique layers and complete `0..layerCount-1` coverage. `applyPack` may assign `registry.layers[binding.layer]` directly because it is in the same package; do not add a one-line mutation wrapper or expose the table.

- [ ] **Step 3: Implement the minimal v1 loader with standard library only**

In `pack.go`, implement:

```go
const (
	packFormatVersion = 1
	packManifestLimit = 4 << 10
	packTextureLimit  = 64 << 10
	packTextureSize   = 16
)

type packManifest struct {
	Format int    `json:"format"`
	Name   string `json:"name"`
}

func applyPack(registry *Registry, root fs.FS) error
```

Required behavior:

- Read `pack.json` and each `textures/<logical-name>.png` with `fs.Open`, `Stat`, `io.LimitReader(limit+1)` and `io.ReadAll`; reject non-regular files and oversized content before decoding.
- Require valid UTF-8 manifest bytes, `format == 1`, and a trimmed non-empty name no longer than 128 UTF-8 bytes.
- Decode the manifest once into both `packManifest` and `map[string]json.RawMessage`; warn for keys other than `format` and `name` with `slog.Warn`.
- Iterate only `textureBindings`; never scan the directory and never accept arbitrary filenames.
- Treat `fs.ErrNotExist` for a known texture as fallback. All other open/read/decode errors are fatal and include the validated pack name plus logical name.
- Use `png.DecodeConfig` to reject dimensions before full decode, then `png.Decode`, `image.NewNRGBA` and `draw.Draw` to normalize any supported PNG color model to exactly `16*16*4` RGBA bytes.
- Decode all replacements into a temporary fixed array first and mutate `Registry` only after every present texture validates, so one bad file cannot leave a partially applied pack.
- Accept arbitrary valid per-layer RGBA pixels, including intermediate alpha. Do not add logical-layer-specific alpha, pixel-structure or local-license validation; render classification, geometry, layer IDs and mapping remain owned by the registry rather than the pack.

Do not add a runtime importer, manifest texture mapping, image scaling, format sniffing, cache, interface, or goroutine.

- [ ] **Step 4: Run the focused checks**

```bash
gofmt -w internal/assets/pack.go internal/assets/pack_test.go internal/assets/blocks.go
go test ./internal/assets -race -count=1
git diff --check
```

Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add internal/assets/pack.go internal/assets/pack_test.go internal/assets/blocks.go
git commit -m "feat: load bounded texture pack directories"
```

---

### Task 3: Vendor and embed the licensed Pixel Perfection subset

**Files:**

- Create: `internal/assets/default_pack.go`
- Create: `internal/assets/default_pack_test.go`
- Create: `internal/assets/packs/pixel_perfection/pack.json`
- Create: `internal/assets/packs/pixel_perfection/textures/*.png`
- Create: `internal/assets/packs/pixel_perfection/ATTRIBUTION.md`
- Create: `internal/assets/packs/pixel_perfection/LICENSE.txt`
- Create: `internal/assets/packs/pixel_perfection/PROVENANCE.json`

- [ ] **Step 1: Pin and verify the upstream source before copying anything**

The immutable upstream pin is `7935d064fc6f993d1b5038ed5ec17a615600cf0a`. Shell DNS blocks `git clone` in this workspace, so fetch every approved file through the official GitHub connector with that full ref and record the returned Git blob SHA; never omit the ref or use a moving branch name in provenance.

Expected: the official repository metadata and commit URL identify the same 40-hex commit, and the exact-ref README states CC BY-SA 4.0. Do not substitute files from search results, release ZIPs, third-party mirrors or Minecraft distributions.

Before copying, assert every source path below exists at the pinned commit. If any path differs, update the OpenSpec design and obtain controller approval before changing the mapping.

- [ ] **Step 2: Write the failing provenance contract, then copy only the approved subset**

First add a complete `TestEmbeddedDefaultPackProvenance` to `default_pack_test.go`, reading the future tree through `os.DirFS("packs/pixel_perfection")`. It must declare the exact source/destination table below; require the pack manifest, attribution, full license and provenance files; assert every destination is a 16×16 PNG matching the SHA-256 recorded in provenance; and reject extra PNGs. Run it before creating the asset directory:

```bash
go test ./internal/assets -run TestEmbeddedDefaultPackProvenance -count=1
```

Expected: FAIL because the embedded default pack is absent. Only after observing that failure, copy/rename the approved source files.

Copy/rename these upstream PNGs without resizing, recoloring, compositing or extracting animation frames:

| Mornlea logical name | Pixel Perfection source path |
|---|---|
| `stone` | `default/default_stone.png` |
| `dirt` | `default/default_dirt.png` |
| `grass_top` | `default/default_grass.png` |
| `grass_side` | `default/default_grass_side.png` |
| `bedrock` | `bedrock/bedrock.png` |
| `stone_brick` | `default/default_stone_brick.png` |
| `furnace` | `default/default_furnace_front.png` |
| `iron_block` | `default/default_steel_block.png` |
| `leaves` | `default/default_leaves.png` |
| `glass` | `default/default_glass.png` |
| `cobblestone` | `default/default_cobble.png` |
| `sand` | `default/default_sand.png` |
| `gravel` | `default/default_gravel.png` |
| `oak_log_side` | `default/default_tree.png` |
| `oak_log_top` | `default/default_tree_top.png` |
| `oak_planks` | `default/default_wood.png` |
| `brick` | `default/default_brick.png` |
| `white_wool` | `wool/wool_white.png` |
| `clay` | `default/default_clay.png` |
| `snow_top` | `default/default_snow.png` |
| `snow_side` | `default/default_snow.png` |
| `mossy_cobblestone` | `default/default_mossycobble.png` |
| `farmland_dry` | `farming/farming_soil.png` |
| `farmland_wet` | `farming/farming_soil_wet.png` |
| `wheat_0` … `wheat_7` | `farming/farming_wheat_1.png` … `farming/farming_wheat_8.png` |

Keep these seven layers procedural: `coal_ore`, `iron_ore`, `light_block`, `roof_tile`, `water`, `smooth_stone`, `chest`. The first five would require composition, animation extraction or a non-equivalent substitute; the fixed commit does not contain the planned `default/default_stone_block.png` or another direct smooth-stone equivalent; `default/default_chest_front.png` is 14×14 and cannot satisfy the no-transform 16×16 contract.

- [ ] **Step 3: Add complete legal and provenance metadata**

Create:

- `LICENSE.txt`: the full CC BY-SA 4.0 legal text from the pinned upstream file; if upstream only carries a short notice/link, use the canonical legalcode from Creative Commons and record that URL in attribution/provenance.
- `ATTRIBUTION.md`: title, Hugh “XSSheep” Rutland as original author, listed upstream contributors, upstream repository URL, pinned full commit, CC BY-SA 4.0 URL, and the statement that Mornlea selected and renamed a subset without pixel transformations.
- `PROVENANCE.json`: upstream repository, full commit, license id/url, and one record per destination containing source path and SHA-256 of the vendored bytes. `snow_top` and `snow_side` remain separate destination records with the same source/hash.
- `pack.json`: `{"format":1,"name":"Pixel Perfection for Mornlea"}`.

Do not put Go code or generated comments into the third-party notice files.

Run the already-written provenance test again:

```bash
go test ./internal/assets -run TestEmbeddedDefaultPackProvenance -count=1
```

Expected: PASS.

- [ ] **Step 4: Write failing default-constructor tests**

`default_pack_test.go` must additionally assert:

1. every mapped layer equals the normalized bytes of its embedded PNG, while the seven fallback layers remain identical to `NewRegistry()`, and the programmatic/embedded leaves and glass keep binary alpha;
2. default and procedural registries have the same atlas layer count/byte length, and two calls to `NewDefaultRegistry()` produce byte-identical atlas output;
3. the embedded FS contains `pack.json`, `ATTRIBUTION.md`, `LICENSE.txt` and `PROVENANCE.json`;
4. a user override replaces an embedded layer while an absent user file retains the embedded bytes, and a valid intermediate-alpha override is accepted without changing classification or mapping;
5. an invalid user pack returns `nil, error`, never a partially applied registry.

Run:

```bash
go test ./internal/assets -run 'TestEmbeddedDefaultPack|TestDefaultRegistry|TestRegistryWithOverride' -count=1
```

Expected: FAIL because the constructors do not exist.

- [ ] **Step 5: Add the two product constructors**

In `default_pack.go`:

```go
//go:embed packs/pixel_perfection
var defaultPackFS embed.FS

// NewDefaultRegistry 构造带 Mornlea 内嵌默认材质的注册表。
func NewDefaultRegistry() *Registry

// NewRegistryWithOverride 在内嵌默认材质之上应用用户目录覆盖。
func NewRegistryWithOverride(root fs.FS) (*Registry, error)
```

Implementation rules:

- `NewDefaultRegistry()` calls `NewRegistry()`, obtains `fs.Sub(defaultPackFS, "packs/pixel_perfection")`, applies the embedded pack, and panics with context only if the repository-owned embedded pack is invalid. Tests make this a build-time defect, not recoverable user input.
- `NewRegistryWithOverride()` creates a fresh default registry and applies `root`; it returns user-pack errors and never mutates any registry visible to the caller on failure.
- Do not add a constructor options struct, pack interface, cache or clone API.

- [ ] **Step 6: Run focused checks and commit**

```bash
gofmt -w internal/assets/default_pack.go internal/assets/default_pack_test.go
go test ./internal/assets -race -count=1
git diff --check
git add internal/assets/default_pack.go internal/assets/default_pack_test.go internal/assets/packs/pixel_perfection
git commit -m "feat: embed Pixel Perfection default textures"
```

---

### Task 4: Add the startup-only texture pack path to config v1

**Files:**

- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Write failing config tests**

Add focused cases proving:

1. `Defaults()` leaves both texture path fields empty;
2. `texturePackPath: "packs/local"` preserves that raw string and resolves an absolute clean path relative to the directory containing the loaded config file;
3. an absolute path remains absolute after `filepath.Clean`;
4. an empty value disables the override;
5. a non-string value fails with `解析 texturePackPath 字段` context;
6. `Save` writes only the raw `texturePackPath`, never the resolved path;
7. `texturePackPath` is known to `warnUnknownTopLevel` and is absent from numeric `Fields()`;
8. `CurrentVersion` remains 1.

Run:

```bash
go test ./internal/config -run 'Test.*TexturePack' -count=1
```

Expected: FAIL because the fields are absent.

- [ ] **Step 2: Add raw and resolved fields without a schema migration**

Extend `Config`:

```go
// TexturePackPath 是配置文件原文，供保存时无损往返。
TexturePackPath string `json:"texturePackPath,omitempty"`
// ResolvedTexturePackPath 是相对配置文件目录解析后的客户端启动路径。
ResolvedTexturePackPath string `json:"-"`
```

In `decodeConfig`, decode the top-level field as a string. Empty stays empty. For a non-empty relative value, join it to `filepath.Dir(path)` and call `filepath.Abs`; for an absolute value, call `filepath.Clean`. Add `texturepackpath` to the known top-level set. Do not add it to `Fields()`, the debug tunable groups or `Config.Apply()`.

Keep `CurrentVersion = 1`: this optional field changes neither storage nor required config syntax.

- [ ] **Step 3: Run focused checks and commit**

```bash
gofmt -w internal/config/config.go internal/config/config_test.go
go test ./internal/config -race -count=1
git diff --check
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat: configure a local texture pack directory"
```

---

### Task 5: Wire the registry into all client startup modes

**Files:**

- Modify: `cmd/mornlea/app.go`
- Modify: `cmd/mornlea/app_dependencies.go`
- Modify: `cmd/mornlea/app_startup.go`
- Create: `cmd/mornlea/app_startup_test.go`
- Modify: `cmd/mornlea/main.go`
- Modify: `cmd/mornlea/run_test.go`

- [ ] **Step 1: Write failing run-path isolation tests**

At the `runWithDependencies` seam, capture `applicationOptions` passed to `newApplication` and assert:

- normal local and `--connect` modes receive `effective.ResolvedTexturePackPath`;
- benchmark and capture receive an empty path even when the user's config contains `texturePackPath`;
- remote mode still uses the local path because textures are client presentation, not server state.

Run:

```bash
go test ./cmd/mornlea -run 'TestRun.*TexturePack' -count=1
```

Expected: FAIL because `applicationOptions` has no path.

- [ ] **Step 2: Pass only the resolved path into the application**

Add to `applicationOptions`:

```go
// TexturePackPath 是客户端启动时读取的本地覆盖目录；空值只用内嵌默认材质。
TexturePackPath string
```

In `runWithDependencies`, assign `effective.ResolvedTexturePackPath` with the other effective client configuration. Rely on the existing `resolveConfig` rule that benchmark and capture return `config.Defaults()`; do not add a second config-loading path or special-case server behavior.

- [ ] **Step 3: Write a failing side-effect-order test**

Add `newRegistry func(string) (*assets.Registry, error)` to `applicationDependencies`. In `app_startup_test.go`, make it return a sentinel error and make `dialTCP`, `openStore`, `newWindow`, `newOffscreenRenderer` and host construction fail the test if called. Assert `newApplicationWithDependencies` returns the registry error and no external side effect occurs.

Also test that the empty path calls `assets.NewDefaultRegistry()` and a non-empty path calls `assets.NewRegistryWithOverride(os.DirFS(path))` through the default dependency.

Run:

```bash
go test ./cmd/mornlea -run 'TestNewApplication.*Registry' -count=1
```

Expected: FAIL because registry construction is still hard-coded after connection/window creation.

- [ ] **Step 4: Load the registry before client side effects**

In `defaultApplicationDependencies`, implement the single dependency function:

```go
newRegistry: func(path string) (*assets.Registry, error) {
	if path == "" {
		return assets.NewDefaultRegistry(), nil
	}
	return assets.NewRegistryWithOverride(os.DirFS(path))
},
```

At the start of `newApplicationWithDependencies`, after filling nil dependency defaults but before `context.Background()`, store/network/host/window/renderer work, call `dependencies.newRegistry(options.TexturePackPath)`. Wrap failures with `fmt.Errorf("加载材质包 %q: %w", options.TexturePackPath, err)` without treating an explicitly configured missing directory as fallback. Remove the later `assets.NewRegistry()` call and reuse this registry for atlas upload, HUD layout and meshing.

Do not alter `cmd/mornlea-server`; its build graph must remain asset-free.

- [ ] **Step 5: Run focused checks and commit**

```bash
gofmt -w cmd/mornlea/app.go cmd/mornlea/app_dependencies.go cmd/mornlea/app_startup.go cmd/mornlea/app_startup_test.go cmd/mornlea/main.go cmd/mornlea/run_test.go
go test ./cmd/mornlea -race -count=1
go test ./internal/archcheck -count=1
git diff --check
git add cmd/mornlea/app.go cmd/mornlea/app_dependencies.go cmd/mornlea/app_startup.go cmd/mornlea/app_startup_test.go cmd/mornlea/main.go cmd/mornlea/run_test.go
git commit -m "feat: apply texture packs during client startup"
```

---

### Task 6: Document the pack format and ship third-party notices

**Files:**

- Create: `docs/texture-packs.md`
- Modify: `README.md`
- Modify: `Makefile`

- [ ] **Step 1: Add a focused packaging check before changing the Makefile**

Run the existing macOS build, then prove the notice directory is currently absent:

```bash
make build
test ! -e bin/third-party/pixel-perfection/ATTRIBUTION.md
```

Expected: the final command succeeds before the packaging change. Preserve any existing Rust toolchain fix or unrelated Makefile edit; do not overwrite the file wholesale.

- [ ] **Step 2: Write the user-facing format documentation**

`docs/texture-packs.md` must show the only supported v1 layout:

```text
my-pack/
├── pack.json
└── textures/
    ├── stone.png
    └── wheat_7.png
```

Document:

- `pack.json` requires integer `format: 1` and a non-empty `name`;
- textures are optional 16×16 PNGs, normalized to RGBA at load time;
- the complete stable logical-name list from `textureBindings`;
- missing known files fall back to the embedded default and then procedural texture;
- an explicitly configured unreadable/invalid pack fails client startup;
- `texturePackPath` is relative to the config file unless absolute;
- directory-only and startup-only limitations;
- no Minetest/Minecraft manifest compatibility, ZIP, hot reload, animation, PBR or material/face remapping;
- how to find the Pixel Perfection license, attribution and provenance in source and release output.

Add one short README link under player/configuration documentation. Do not duplicate the complete format in README.

- [ ] **Step 3: Copy notices into the macOS client release unit**

Add Make variables for the source and destination notice directories. In `build`, after creating `bin`, create `bin/third-party/pixel-perfection` and copy exactly `ATTRIBUTION.md`, `LICENSE.txt` and `PROVENANCE.json` there.

Do not add these assets or notices to `build-linux-server`: the dedicated server release remains only the server binary plus matching Rust engine library.

- [ ] **Step 4: Verify documentation and release output**

```bash
make build
cmp internal/assets/packs/pixel_perfection/ATTRIBUTION.md bin/third-party/pixel-perfection/ATTRIBUTION.md
cmp internal/assets/packs/pixel_perfection/LICENSE.txt bin/third-party/pixel-perfection/LICENSE.txt
cmp internal/assets/packs/pixel_perfection/PROVENANCE.json bin/third-party/pixel-perfection/PROVENANCE.json
test -z "$(go list -deps ./cmd/mornlea-server | rg 'internal/assets')"
git diff --check
```

Expected: all three notices are byte-identical and the dedicated server dependency query finds no `internal/assets`.

- [ ] **Step 5: Commit**

```bash
git add docs/texture-packs.md README.md Makefile
git commit -m "docs: describe and attribute texture packs"
```

---

### Task 7: Regenerate and inspect visual goldens after LOD lands

**Files:**

- Modify: `cmd/mornlea/main.go`
- Modify: `cmd/mornlea/capture.go`
- Modify: `cmd/mornlea/run_test.go`
- Modify: `cmd/mornlea/capture_near_band_test.go`
- Modify: existing files under `cmd/mornlea/testdata/golden/` selected by the post-LOD `captureScenes` table
- Reference: post-merge LOD capture scene and tests

- [ ] **Step 1: Confirm scene ownership after the LOD merge**

Read the current `captureScenes` table and its ordering tests. Do not re-create or rename the LOD scene. Verify `far-horizon` is penultimate and `water-underwater` remains last.

```bash
go test ./cmd/mornlea -run 'TestCapture.*Scene|Test.*WaterUnderwater|Test.*Far.*Horizon' -count=1
```

Expected: scene-structure tests pass before golden changes.

- [ ] **Step 2: Demonstrate the expected visual-only failure**

```bash
make visual-check VISUAL_OUT=build/visual-texture-pack-before-update
```

Expected: image comparison fails because mapped material pixels changed. Any crash, missing scene, mesh error, timeout, alpha regression or threshold/config mutation is a real defect; fix it before updating goldens.

- [ ] **Step 3: Write failing material-independent near-band control tests**

Add focused tests to `run_test.go` and `capture_near_band_test.go` proving:

1. `--update-golden` first creates two disposable control applications with the same effective embedded-default registry, seed and render config, with `LodEnabled` the only difference;
2. before any golden write, the update renders `far-horizon` in both control applications and invokes the existing geometrically derived `nearBandGuard.assertUnchanged` on the two current frames;
3. a protected top/bottom row difference fails the whole update and leaves every existing golden byte-identical, even when an old golden is absent;
4. a difference confined to the derived far band closes both controls and only then creates a fresh LOD-on application for formal `runCapture`; that application has not executed the control scene and runs the normal full scene order;
5. every successfully created application closes on success, second-control construction failure, guard failure, fresh-application construction failure and formal-capture failure.

```bash
go test ./cmd/mornlea -run 'Test(TextureGoldenUpdate|Run.*Golden)' -count=1
```

Expected: FAIL because update mode currently creates one application and compares against the old golden only when it exists.

- [ ] **Step 4: Move the existing guard into a pre-write LOD on/off control**

In `runWithDependencies`, create a disposable LOD-on control application and a disposable LOD-off control application from the same resolved default options, with only `Render.LodEnabled` different. Before any baseline write, render `far-horizon` once on each control. Keep both diagnostic images in `VISUAL_OUT`, construct the guard from the LOD-on control camera and shell radii, and call the existing `nearBandGuard.assertUnchanged` on those two current frames.

Move the call site out of `captureOne`'s old-golden branch; do not delete or weaken `nearBandGuard`, its fail-closed geometry, or per-pixel protected-row comparison. Close both disposable controls before returning any construction/guard error. Only after the guard passes and both controls close, construct a fresh LOD-on application and pass only that untouched application to formal `runCapture`; close it on success or failure. Do not reuse the LOD-on control for formal capture and do not cache every scene image in memory. The three-application sequence exists only for explicit update mode; ordinary capture and gameplay still construct one application.

```bash
gofmt -w cmd/mornlea/main.go cmd/mornlea/capture.go cmd/mornlea/run_test.go cmd/mornlea/capture_near_band_test.go
go test ./cmd/mornlea -run 'Test(TextureGoldenUpdate|Run.*Golden|NearBandGuard)' -count=1
```

Expected: PASS. The control no longer depends on historical material pixels or on any old golden being present.

- [ ] **Step 5: Regenerate with the embedded default only**

```bash
make visual-update VISUAL_OUT=build/visual-texture-pack-update
```

Expected: the command reports that the LOD on/off near-band control executed and passed before writing the first baseline. Do not move, delete or rename any tracked golden before this command.

The capture path must ignore any local `texturePackPath`; verify the run-path test from Task 5 before accepting these files. Do not set or temporarily rename the user's config.

- [ ] **Step 6: Inspect the rendered outputs**

Visually inspect at minimum:

- `materials-showcase`: all mapped solids, cutout leaves/glass and the seven procedural fallbacks;
- inventory/HUD scenes: item icons use the same default atlas without bleeding;
- `water-surface-slope` and `water-underwater`: procedural water, transparency ordering and tint are unchanged;
- the post-LOD far-horizon scene: near/default textures and distant LOD presentation have no seam or missing layer;
- wheat/farming scene coverage if present: all eight crop stages retain alpha cutout and crossed-plane geometry.

Compare the new images with the previous revision, then record accepted images and any ruling in the ledger. Do not adjust capture thresholds or modify the LOD near-band guard.

- [ ] **Step 7: Re-run the visual gate and commit**

```bash
make visual-check VISUAL_OUT=build/visual-texture-pack-final
go test ./cmd/mornlea -run 'Test(TextureGoldenUpdate|Run.*Golden|NearBandGuard)' -count=1
git diff --check
git add cmd/mornlea/main.go cmd/mornlea/capture.go cmd/mornlea/run_test.go \
	cmd/mornlea/capture_near_band_test.go cmd/mornlea/testdata/golden
git commit -m "test: update visual baselines for default textures"
```

Expected: every current scene passes with unchanged thresholds.

---

### Task 8: Update baselines, run full verification, and obtain final review

**Files:**

- Modify: `AGENTS.md`
- Modify: `CLAUDE.md`
- Modify: `docs/notes/progress.md`
- Modify: `openspec/changes/mornlea-texture-pack/tasks.md`
- Modify: `openspec/changes/mornlea-texture-pack/ledger.md`

- [ ] **Step 1: Update long-lived project documentation**

Add one current-capability paragraph stating:

- the embedded Pixel Perfection subset is the client default;
- procedural textures remain the final fallback;
- an optional startup-only local 16×16 directory override is configured by `texturePackPath`;
- benchmark/capture ignore local overrides, and the dedicated server does not load assets;
- record the exact versions already established by the merged LOD baseline, and state that this texture change did not advance them.

Apply the same edit to `AGENTS.md` and `CLAUDE.md`, then require byte identity. Add the milestone and attribution link to `docs/notes/progress.md`. Do not copy change-specific non-goals into the baseline docs.

- [ ] **Step 2: Mark completed OpenSpec tasks and reconcile the ledger**

Check each `tasks.md` item only when its implementation and both reviews are complete. The ledger must name every implementer/reviewer, review iteration, resolved finding and controller ruling. Do not archive the change in this task.

- [ ] **Step 3: Run formatting and focused verification**

```bash
gofmt -l .
go test ./internal/assets ./internal/config ./cmd/mornlea -race -count=1
go test ./internal/archcheck -count=1
cmp -s AGENTS.md CLAUDE.md
git diff --check
```

Expected: `gofmt -l .` and `git diff --check` print nothing; all tests and `cmp` succeed.

- [ ] **Step 4: Run Rust, full Go, build and visual verification**

```bash
make rust
make rust-check
go test ./... -race
go vet ./...
make build
make visual-check VISUAL_OUT=build/visual-texture-pack-final-review
openspec validate --all --strict --no-interactive
```

Expected: every command succeeds. Performance numbers may be recorded but no threshold, overflow, completeness or data-loss gate may be relaxed.

- [ ] **Step 5: Ask an independent final reviewer to inspect the whole branch**

The final review must compare the complete branch diff with the approved design and active OpenSpec change, explicitly checking:

- exact load/fallback/error ordering and startup side-effect ordering;
- bounded reads and all-or-nothing user override application;
- fixed 39-name mapping with no Rust/material remap;
- source pin, hashes, attribution, ShareAlike notice and release packaging;
- benchmark/capture isolation and dedicated-server dependency closure;
- visual changes after the merged LOD baseline;
- absence of protocol/storage/config-version/ABI/scenario migrations.

Resolve findings through the ledger and rerun the affected commands plus the full verification above.

- [ ] **Step 6: Commit the reconciled baseline and stop for archive approval**

```bash
git add AGENTS.md CLAUDE.md docs/notes/progress.md openspec/changes/mornlea-texture-pack
git commit -m "docs: record texture pack integration"
git status --short
```

Expected: only known unrelated user changes remain. Report the commits and verification evidence, then ask for explicit approval before running the separate `openspec-archive-change` workflow.
