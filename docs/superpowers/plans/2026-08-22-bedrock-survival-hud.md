# Mornlea Bedrock 风格生存 HUD Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在不改变权威状态、协议、存档、配置、Rust ABI 或 GPU pass 的前提下，用 Mornlea 原创的 Bedrock 风格统一快捷栏、生命、氧气、耐久和采掘进度，并用固定 capture 场景长期锁定结果。

**Architecture:** 继续由 `internal/render/hud` 生成 16×16 atlas 像素和固定容量 quad/glyph 布局，`cmd/mornlea` 只提供既有权威镜像，Rust 客户端继续消费原有 48-byte HUD instance stream。只在既有函数和常量上做最小扩展：不建立主题系统、不加入 PNG UI 资产、不新增渲染阶段。

**Tech Stack:** Go 1.26、既有 `internal/render/hud`、既有 `cmd/mornlea` offscreen capture、Rust 1.97.1 `mornlea_client`（仅作为未改 ABI 的验证对象）、OpenSpec。

---

## 执行前置与控制规则

- [ ] 先确认 `mornlea-texture-pack` 已在可用 Metal 环境完成 visual update、逐图验收、终审与归档；未满足时停止，不创建第二个 active change，也不修改任何 HUD/capture/golden 产品文件。
- [ ] 从归档后的最新 `main` 创建隔离 worktree/`codex/bedrock-survival-hud` 分支；运行 `git status --short`，记录并保留所有既有改动。
- [ ] 阅读 `openspec/config.yaml`、本设计文档和新 change 的 `proposal.md`、delta specs、`design.md`、`tasks.md`；实现中发现不一致时先改 OpenSpec 产物。
- [ ] 使用 `superpowers:subagent-driven-development`：下列每个 Task 派发全新的 implementer，完成后由独立 reviewer 同时裁决规格合规与代码质量；问题进入最多 5 轮的修复/复审，结论、证据与 controller ruling 全部写入 `ledger.md`。
- [ ] capture/golden 文件只允许 Task 5 修改；它执行期间不得并行运行其他会改 capture 或 golden 的任务。
- [ ] 全程只使用原创程序化 UI 像素；不得复制、描摹或提交 Mojang UI、字体、PNG 或截图切片。

## Task 1：建立唯一 active OpenSpec change

**Files:**

- Create: `openspec/changes/bedrock-survival-hud/.openspec.yaml`
- Create: `openspec/changes/bedrock-survival-hud/proposal.md`
- Create: `openspec/changes/bedrock-survival-hud/design.md`
- Create: `openspec/changes/bedrock-survival-hud/tasks.md`
- Create: `openspec/changes/bedrock-survival-hud/ledger.md`
- Create: `openspec/changes/bedrock-survival-hud/specs/survival-hud-presentation/spec.md`
- Create: `openspec/changes/bedrock-survival-hud/specs/visual-verification/spec.md`

- [ ] **Step 1: 证明执行前置成立**

  运行：

  ```bash
  git status --short
  find openspec/changes -mindepth 1 -maxdepth 1 -type d | sort
  test ! -d openspec/changes/mornlea-texture-pack
  ```

  预期：不存在未归档的 `mornlea-texture-pack`；若存在，停止本计划。

- [ ] **Step 2: 写 proposal、design 与 ledger 骨架**

  `proposal.md` 把范围限制为：九格快捷栏、选中格、数量、工具耐久、生命、耗损氧气、采掘进度、响应式缩放与 capture；明确背包/合成/箱子/熔炉只做避让，不重绘。

  `design.md` 固化以下实现边界：

  - `internal/render/hud` 独占视觉 token、atlas、布局和容量。
  - 关闭容器时生命/氧气位于快捷栏上方左右；打开容器时移至快捷栏下方既有底部留白，不改变 `InventorySlotAt`。
  - 满氧完全隐藏；耗氧时用十个空槽加覆盖气泡。
  - 采掘可用末端亮标记，不可采用固定警示缺口，不能只靠颜色。
  - 无协议、存档、配置、benchmark、Rust ABI/client ABI/engine ABI 迁移。

  `ledger.md` 预建每个 Task 的 implementer、spec review、quality review、修复轮次、验证证据和 ruling 栏位；不提前填写通过结论。

- [ ] **Step 3: 写 `survival-hud-presentation` delta spec**

  至少包含可判定 Requirement/Scenario：

  1. 快捷栏固定九格、居中、选中格有颜色无关的外扩双层边框。
  2. 数量与耐久继续由权威物品镜像驱动，显示条件不变。
  3. 生命以十个空/半/满心呈现，仅消费已确认值并钳制到 `core.MaxHealth`。
  4. 氧气满值隐藏；耗损时按 `ceil(value*10/MaxOxygenTicks)` 显示覆盖气泡，零值无覆盖。
  5. 采掘进度位于状态行上方、钳制到 100%，可采/不可采同时有颜色与形状差异。
  6. 窄窗口整体等比缩小，零尺寸无实例，所有矩形留在 framebuffer 内。
  7. 容器打开时状态行保持可见、在快捷栏下方且不覆盖 `InventorySlotAt` 可交互格子。
  8. 固定容量、同一 HUD pass、相同 instance 编码和零每帧动态资源保持不变。

- [ ] **Step 4: 写 `visual-verification` MODIFIED delta**

  把当前完整场景顺序写入同一 Requirement，加入新场景后必须是：

  ```text
  terrain-noon
  hud-hotbar-health
  hud-survival-feedback
  avatar-nametag
  inventory-crafting
  debug-panel
  skylight-tunnel
  block-light-room
  materials-showcase
  target-block-feedback
  oak-grove
  ai-companion
  water-surface-slope
  far-horizon
  water-underwater
  ```

  同时要求：

  - `hud-hotbar-health` 覆盖正常九格、选中格、数量、耐久与满血。
  - `hud-survival-feedback` 在同一固定帧覆盖低血、耗损氧气、磨损工具和不可采目标中段进度。
  - `far-horizon` 继续倒数第二，`water-underwater` 继续唯一末场景。
  - 继续使用既有 offscreen 完整渲染链路和双阈值，不调整阈值。

- [ ] **Step 5: 写按本计划对应的 `tasks.md`**

  任务组必须与本文件 Task 2–6 对齐；每组把失败测试、最小实现、focused 验证、spec review、quality review 和提交放在同一组，capture/golden 独占一组。

- [ ] **Step 6: 严格校验并评审**

  运行：

  ```bash
  openspec validate --all --strict --no-interactive
  git diff --check
  ```

  预期：全部 change/spec 通过。独立 reviewer 核对 OpenSpec 只描述可观察行为、设计只描述实现选择、任务可逐项执行；把结论写入 ledger。

- [ ] **Step 7: 提交规划产物**

  ```bash
  git add openspec/changes/bedrock-survival-hud
  git commit -m "docs: propose survival HUD presentation"
  ```

## Task 2：用现有 Go atlas 生成原创心与气泡

**Files:**

- Modify: `internal/render/hud/atlas.go`
- Modify: `internal/render/hud/atlas_test.go`
- Modify: `internal/render/hud/health.go`
- Test: `internal/render/hud/health_test.go`
- Test: `internal/render/hud/oxygen_test.go`

- [ ] **Step 1: 先写 atlas 失败测试**

  将列契约锁为：

  ```go
  const (
      hotbarEmptyHeartColumn = iota
      hotbarHalfHeartColumn
      hotbarFullHeartColumn
      hotbarEmptyBubbleColumn
      hotbarFullBubbleColumn
      hotbarBlockColumnOffset
  )
  ```

  测试必须证明：

  - 五个 UI cell 各自有非透明像素且内容互不相同。
  - 每个 UI cell 的 alpha 只能是 `0` 或 `255`。
  - 连续两次 `buildHotbarTextureAtlas` 逐字节相等。
  - `hotbarBlockColumnOffset + int(item)` 仍逐像素复制 registry 顶面。
  - 所有列 UV 仍落在自己的 16×16 cell 内。

  运行：

  ```bash
  go test ./internal/render/hud -run 'TestHotbar(TextureAtlas|ColumnUV)' -count=1
  ```

  预期：因半心/气泡列尚不存在而失败。

- [ ] **Step 2: 最小扩展现有 atlas 常量和生成顺序**

  `buildHotbarTextureAtlas` 依次调用：

  ```go
  paintHotbarHeart(pixels, hotbarEmptyHeartColumn, heartEmpty)
  paintHotbarHeart(pixels, hotbarHalfHeartColumn, heartHalf)
  paintHotbarHeart(pixels, hotbarFullHeartColumn, heartFull)
  paintHotbarBubble(pixels, hotbarEmptyBubbleColumn, false)
  paintHotbarBubble(pixels, hotbarFullBubbleColumn, true)
  ```

  只新增包内枚举 `heartFill` 和 `paintHotbarBubble`；继续复用 `hotbarTextureUV`，不新增图集类型、主题接口或二进制资产。`paintHotbarHeart` 直接生成完整半心图标，不再靠裁剪实心 UV 得到半心。

- [ ] **Step 3: 让健康/氧气测试只引用稳定 UI cell**

  增加最小 helper：

  ```go
  func hotbarHeartUV(fill heartFill) [4]float32
  func hotbarBubbleUV(full bool) [4]float32
  ```

  helper 只分派到 `hotbarTextureUV`，不引入通用 sprite registry。先只替换测试可见的心形 UV 引用；氧气绘制在 Task 4 接线。

- [ ] **Step 4: 格式化并运行 focused tests**

  ```bash
  gofmt -w internal/render/hud/atlas.go internal/render/hud/atlas_test.go internal/render/hud/health.go internal/render/hud/health_test.go internal/render/hud/oxygen_test.go
  go test ./internal/render/hud -race -count=1
  git diff --check
  ```

- [ ] **Step 5: 评审、记录并提交**

  reviewer 核对原创像素、二值 alpha、物品列偏移和无额外 abstraction；修复后重跑本 Task 命令并写 ledger。

  ```bash
  git add internal/render/hud/atlas.go internal/render/hud/atlas_test.go internal/render/hud/health.go internal/render/hud/health_test.go internal/render/hud/oxygen_test.go
  git commit -m "feat: add survival HUD atlas icons"
  ```

## Task 3：统一快捷栏、选中格、耐久与采掘轨道

**Files:**

- Modify: `internal/render/hud/layout.go`
- Modify: `internal/render/hud/layout_test.go`
- Modify: `internal/render/hud/renderer_test.go`

- [ ] **Step 1: 为关闭态快捷栏写失败测试**

  表驱动测试固定以下结构，不固定脆弱的全局 quad 下标：

  - 九格的中心总宽度与 `inventorySlotOrigin` 一致。
  - 关闭态面板含外侧阴影和内侧表面。
  - 每格有独立表面，选中格另有外扩高对比边框和强调色内边框。
  - 数量仍是阴影/前景两层，最多两位。
  - 耐久只对 `0 < Durability < MaxDurability` 的工具产生背景和填充。

  运行：

  ```bash
  go test ./internal/render/hud -run 'Test(ClosedHotbar|HotbarCount|Durability)' -count=1
  ```

  预期：因新面板/双层选中结构尚不存在而失败。

- [ ] **Step 2: 在 `layout.go` 内加入固定视觉 token**

  只添加当前产品需要的包内常量和 `[4]float32` 颜色；不建立 token struct、主题对象或配置。增加一个共享几何 helper：

  ```go
  func hotbarRowBounds(width, height float32) (left, top, totalWidth, scale float32)
  ```

  `inventorySlotOrigin`、状态栏锚点和采掘轨道都复用它，避免三份中心宽度公式漂移；打开背包的 36 格面板结构和命中几何保持不变。

- [ ] **Step 3: 最小修改关闭态绘制**

  在 `appendInventoryPanel`/`layoutInventory` 的关闭分支加入面板阴影、表面和双层选中边框；继续使用现有 `appendItemTile`、`appendHotbarCountScaled`、`appendDurabilityBarScaled`。打开态只保留既有容器面板，不借机重做背包/合成/箱子/熔炉。

- [ ] **Step 4: 为采掘形状差异写失败测试**

  覆盖 inactive、`RequiredTicks == 0`、0%、中段、超过 100%、可采和不可采。断言：

  - 进度宽度钳制在轨道内。
  - 可采中段含一个固定宽度亮色末端标记。
  - 不可采中段含固定数量、固定位置的警示缺口。
  - 两种状态即使忽略 RGB，quad 几何序列也不同。

  运行：

  ```bash
  go test ./internal/render/hud -run 'TestMining' -count=1
  ```

  预期：现有纯色两 quad 实现无法满足形状断言。

- [ ] **Step 5: 最小扩展 `appendMiningBar`**

  继续使用纯色 `hotbarInstance`：轨道和填充复用原逻辑；可采追加一个末端 cap，不可采追加固定 warning notch。比例使用 `min(float32(ProgressTicks)/float32(RequiredTicks), 1)`；不新增 shader、texture cell 或动画状态。

- [ ] **Step 6: 更新容量期望并运行 focused tests**

  此时只按新快捷栏/采掘最坏数量更新 `maxHotbarQuads` 的组成项，最终生命/氧气容量在 Task 4 对账。

  ```bash
  gofmt -w internal/render/hud/layout.go internal/render/hud/layout_test.go internal/render/hud/renderer_test.go
  go test ./internal/render/hud -race -count=1
  git diff --check
  ```

- [ ] **Step 7: 评审、记录并提交**

  reviewer 核对没有改变 `InventorySlotAt`、物品语义或打开态容器视觉，且非颜色标记可机械判定；修复后写 ledger。

  ```bash
  git add internal/render/hud/layout.go internal/render/hud/layout_test.go internal/render/hud/renderer_test.go
  git commit -m "feat: restyle survival hotbar feedback"
  ```

## Task 4：把权威生命和氧气锚定到快捷栏并完成响应式容量门禁

**Files:**

- Modify: `internal/render/hud/health.go`
- Modify: `internal/render/hud/health_test.go`
- Modify: `internal/render/hud/oxygen.go`
- Modify: `internal/render/hud/oxygen_test.go`
- Modify: `internal/render/hud/layout.go`
- Modify: `internal/render/hud/layout_test.go`
- Modify: `internal/render/hud/renderer.go`
- Modify: `internal/render/hud/renderer_test.go`
- Verify unchanged: `internal/render/hud/encode.go`

- [ ] **Step 1: 为生命值写新的失败测试**

  将调用目标改为：

  ```go
  func appendHealthBar(
      dst *hotbarLayout,
      health HealthOverlay,
      open bool,
      width, height float32,
  )
  ```

  覆盖未确认、0、1、2、19、20、越界值、零尺寸、关闭态和打开态。对每组断言空/半/满 cell 数量及锚点：关闭态对齐快捷栏左边沿并位于上方，打开态对齐左边沿并位于快捷栏下方留白；不再接受 framebuffer 左下角锚点。

- [ ] **Step 2: 最小重写 `appendHealthBar`**

  删除当前未使用的 `atlas render.GlyphSource` 参数。每次确认状态先绘制十个空心，再按值绘制完整的半/满 atlas cell；删除半宽裁剪逻辑。位置只读 `hotbarRowBounds` 和 `open`，不读取或推算游戏状态。

- [ ] **Step 3: 为十段氧气写失败测试**

  将调用目标改为：

  ```go
  func appendOxygenBar(
      dst *hotbarLayout,
      oxygen OxygenOverlay,
      open bool,
      width, height float32,
  )
  ```

  覆盖未确认、满值、0、1 tick、每个分段边界、`MaxOxygenTicks-1`、越界值和零尺寸。期望覆盖数用同一整数公式计算：

  ```go
  filled := (int(value)*oxygenSegmentCount + int(core.MaxOxygenTicks) - 1) /
      int(core.MaxOxygenTicks)
  ```

  断言满氧追加 0 quad；耗损时先十个空槽，再追加 `filled` 个满气泡；关闭态对齐快捷栏右边沿并位于上方，打开态位于下方且仍可见。

- [ ] **Step 4: 最小重写 `appendOxygenBar`**

  删除纯色横条常量和比例 quad，改用 `hotbarBubbleUV(false/true)`；继续钳制 `oxygen.Value`，保留“未确认不显示”和“满氧不占 quad”语义。气泡总宽度只由 10×16 图标和固定间隔组成。

- [ ] **Step 5: 接线 `HotbarRenderer.Prepare`**

  仅把已有 `open` 参数传给两个 append 函数：

  ```go
  appendHealthBar(&renderer.layout, health, open, float32(width), float32(height))
  appendOxygenBar(&renderer.layout, oxygen, open, float32(width), float32(height))
  ```

  不修改调用者的数据来源，不增加 predictor 分支或本地预测。

- [ ] **Step 6: 扩展 `hudScale` 的关闭态边界**

  用快捷栏、生命、氧气和采掘轨道的联合宽高边界计算关闭态缩放；状态行紧邻快捷栏上方，采掘轨道再位于状态行上方并留固定间隔。打开态继续兼顾既有 `openHUDHeight`。所有最终 rectangle 必须留在 framebuffer 内；不新增最小窗口布局、重排逻辑或 `uiScale` 配置。

- [ ] **Step 7: 写打开容器的几何回归测试**

  在 1280×720、640×360 和一个窄窗口中：

  - 构造 `open=true`、低血、耗氧、最大容器 overlay。
  - 遍历全部 HUD quad，断言矩形在 framebuffer 内。
  - 对 `InventorySlotAt` 可命中的 36 个 slot 矩形，断言生命/氧气矩形与其无交集。
  - 断言 `InventorySlotAt` 在改动前后的边界样本语义不变。

- [ ] **Step 8: 对账固定容量与编码**

  把 `healthQuads` 固定为 20、`oxygenQuads` 固定为 20。分别写出关闭分支（快捷栏面板、双层选中、最坏采掘形状、耐久）和打开分支（36 格、双层选中/来源、最大容器 overlay、耐久）的上限，`maxHotbarQuads` 取两者较大值后再加生命、氧气和聊天；不得把互斥分支相加虚增容量。分别构造两个合法最大组合，证明实例数不超过固定上限，并让较大分支见证该上限；另构造最大 glyph 组合证明不超过 `maxHotbarGlyphs`。

  保留并运行 instance 编码测试，证明 `hotbarInstanceBytes == 48`，更新后的 viewport/quad/glyph offsets 仍按 256 字节对齐且互不重叠，`FrameStreams` 仍只导出实际实例前缀。Go 侧固定缓冲容量和数值期望可随合法上限更新，但字节格式、Rust FFI 与 shader 不得变化。

- [ ] **Step 9: 运行 focused 与架构测试**

  ```bash
  gofmt -w internal/render/hud/health.go internal/render/hud/health_test.go internal/render/hud/oxygen.go internal/render/hud/oxygen_test.go internal/render/hud/layout.go internal/render/hud/layout_test.go internal/render/hud/renderer.go internal/render/hud/renderer_test.go
  go test ./internal/render/hud -race -count=1
  go test ./internal/archcheck -count=1
  git diff --check
  ```

- [ ] **Step 10: 评审、记录并提交**

  reviewer 核对权威值来源、满氧隐藏、打开态可见性、窄窗口、固定容量和编码未变；修复后写 ledger。

  ```bash
  git add internal/render/hud/health.go internal/render/hud/health_test.go internal/render/hud/oxygen.go internal/render/hud/oxygen_test.go internal/render/hud/layout.go internal/render/hud/layout_test.go internal/render/hud/renderer.go internal/render/hud/renderer_test.go
  git commit -m "feat: align survival status around hotbar"
  ```

## Task 5：加入确定性 survival feedback capture 并验收 golden

**Files:**

- Modify: `cmd/mornlea/capture.go`
- Modify: `cmd/mornlea/capture_ai_companion_test.go`
- Create: `cmd/mornlea/capture_hud_test.go`
- Modify: `cmd/mornlea/testdata/golden/hud-hotbar-health.png`
- Create: `cmd/mornlea/testdata/golden/hud-survival-feedback.png`

- [ ] **Step 1: 为场景顺序和夹具恢复写失败测试**

  在 `capture_hud_test.go` 固定：

  - `hud-survival-feedback` 紧跟 `hud-hotbar-health`。
  - 完整 15 场景顺序与 Task 1 delta 一致。
  - `far-horizon` 仍倒数第二，`water-underwater` 仍最后。
  - HUD 夹具应用后能读到指定低血/耗氧和采掘 overlay。
  - `applyCaptureHUDFixture` 返回的 restore closure 调用一次或重复调用后，原 predictor 指针与 mining overlay 都恢复。

  运行：

  ```bash
  go test ./cmd/mornlea -run 'Test(HUDCapture|CaptureSceneOrder|WaterUnderwater|FarHorizon)' -count=1
  ```

  预期：新场景和恢复机制尚不存在而失败。

- [ ] **Step 2: 增加一个 capture-only HUD fixture**

  只在 `capture.go` 增加：

  ```go
  type captureHUDFixture struct {
      Health uint8
      Oxygen uint16
      Mining hud.MiningOverlay
  }

  // 在既有 captureScene 末尾增加：
  HUD *captureHUDFixture
  ```

  `applyCaptureHUDFixture` 必须：

  1. 保存原 `app.predictor` 与 `app.miningOverlay`。
  2. 从原 predictor 读取当前有限 `physics.State`。
  3. 创建 `client.NewPredictor()`，以 Ready、`core.Overworld` 的 `network.PlayerState` 调用 `Begin`，位置/速度/落地保持原值，仅钉住 health/oxygen；yaw/pitch 取固定相机值。
  4. 设置 `app.miningOverlay`。
  5. 返回幂等 restore closure。

  这是 capture 测试夹具，不给 `Predictor` 新增“只改 HUD”的产品 API，也不在 `renderFrame` 热路径加入 capture 条件。

- [ ] **Step 3: 在最终抓帧窗口内应用并恢复夹具**

  `captureSceneImage` 在 `Apply` 后、收敛帧前应用 HUD fixture，并在任何后续返回路径 `defer restore()`。这样收敛帧和最终帧都使用固定状态，而下一个共享 application 场景恢复原状态。无 `HUD` 的现有场景行为逐字节不变。

- [ ] **Step 4: 注册 `hud-survival-feedback`**

  新场景紧跟 `hud-hotbar-health`，复用该场景的固定正午、相机和通过 `InventoryMirror.Apply` 装入物品栏的方式；至少放一把磨损工具，并设置：

  ```go
  HUD: &captureHUDFixture{
      Health: 5,
      Oxygen: core.MaxOxygenTicks / 3,
      Mining: hud.MiningOverlay{
          Active: true, ProgressTicks: 4, RequiredTicks: 9, Harvestable: false,
      },
  }
  ```

  删除 `hud-hotbar-health` 上“部分心形无法注入”的旧 `ponytail:` 注释；不要顺手让 capture fixture 支持聊天、伤害或任意 predictor 字段。

- [ ] **Step 5: 更新旧顺序断言并运行非 GPU 测试**

  ```bash
  gofmt -w cmd/mornlea/capture.go cmd/mornlea/capture_ai_companion_test.go cmd/mornlea/capture_hud_test.go
  go test ./cmd/mornlea -run 'Test(HUDCapture|Capture.*Scene|WaterUnderwater|FarHorizon)' -count=1
  go test ./cmd/mornlea -race -count=1
  git diff --check
  ```

- [ ] **Step 6: 在可用 Metal 环境更新候选 golden**

  ```bash
  make visual-update VISUAL_OUT=build/visual-bedrock-survival-hud-update
  git status --short cmd/mornlea/testdata/golden
  ```

  预期：场景清单共 15 张；新增 `hud-survival-feedback.png`。因为现有多数场景也绘制快捷栏/生命，相关旧 golden 的底部 HUD 区域可以变化；世界、实体、LOD、光照、水面和水下区域出现无法由 HUD 解释的变化时必须先查根因，不能直接接受。

- [ ] **Step 7: 人工逐图验收受影响场景**

  逐张打开 15 个候选，并重点记录：

  - `hud-hotbar-health.png`：九格、双层选中、数量、耐久、满血、无耗氧。
  - `hud-survival-feedback.png`：低血、耗氧气泡、磨损工具、不可采中段进度及警示缺口同时可见。
  - `inventory-crafting.png`：状态行在快捷栏下方，所有格子、合成行与命中区无覆盖。

  在 ledger 记录接受的文件清单与人工结论；不通过则回到对应实现 Task 修复，不调整视觉阈值。

- [ ] **Step 8: 复跑完整 visual check**

  ```bash
  make visual-check VISUAL_OUT=build/visual-bedrock-survival-hud-final
  go test ./cmd/mornlea -run 'Test(HUDCapture|Capture.*Scene|WaterUnderwater|FarHorizon)' -count=1
  git diff --check
  ```

- [ ] **Step 9: 评审、记录并提交**

  reviewer 核对 fixture 只存在于 capture、恢复覆盖错误路径、场景互不污染、末尾顺序与阈值未变；修复后写 ledger。

  ```bash
  git add cmd/mornlea/capture.go cmd/mornlea/capture_ai_companion_test.go cmd/mornlea/capture_hud_test.go cmd/mornlea/testdata/golden
  git commit -m "test: lock survival HUD presentation"
  ```

## Task 6：长期基线、全量验证与整分支终审

**Files:**

- Modify identically: `AGENTS.md`
- Modify identically: `CLAUDE.md`
- Modify: `docs/notes/progress.md`
- Modify: `openspec/changes/bedrock-survival-hud/tasks.md`
- Modify: `openspec/changes/bedrock-survival-hud/ledger.md`

- [ ] **Step 1: 更新长期能力描述，不推进契约版本**

  只有实现和 visual 验收完成后，逐字节同步 `AGENTS.md`/`CLAUDE.md`，加入原创 Bedrock 风格生存 HUD、十段气泡、状态行锚点和新 capture 场景。`docs/notes/progress.md` 记录里程碑与验证。

  明确记录本 change 不推进当前协议、存档 schema、engine ABI、client ABI 或 benchmark scenario；不得把 change 的“非目标”写入长期能力段落。

- [ ] **Step 2: 对账任务与 ledger**

  仅对已经有实现、focused 验证和双评审证据的条目勾选 `tasks.md`；ledger 记录所有 agent、commit、评审发现、修复轮次、人工 visual 结论和 controller ruling。

- [ ] **Step 3: 运行格式、focused、架构和文档门禁**

  ```bash
  gofmt -l .
  go test ./internal/render/hud ./cmd/mornlea -race -count=1
  go test ./internal/archcheck -count=1
  cmp -s AGENTS.md CLAUDE.md
  git diff --check
  ```

  预期：`gofmt -l .` 无输出，其余全部退出 0。

- [ ] **Step 4: 运行 Rust 与全仓门禁**

  ```bash
  make rust
  make rust-check
  go test ./... -race
  go vet ./...
  make visual-check VISUAL_OUT=build/visual-bedrock-survival-hud-final-review
  openspec validate --all --strict --no-interactive
  ```

  预期：全部退出 0；不放宽 correctness、overflow、完整性、I/O、数据丢失或视觉阈值门禁。

- [ ] **Step 5: 检查范围与禁区**

  ```bash
  base_commit=$(git merge-base HEAD main)
  git diff --name-only "$base_commit"..HEAD
  git diff --stat "$base_commit"..HEAD
  git status --short
  ```

  人工确认没有产品改动落入 `engine/`、`internal/network`、`internal/sim`、`internal/storage`、`cmd/mornlea-server`，没有 PNG UI 源资产、第三方依赖、配置键、协议字段或 ABI 变化。

- [ ] **Step 6: 独立整分支终审**

  新 reviewer 从 proposal/spec/design/tasks 逐项核验完整 diff，重点检查：权威数据来源、打开容器避让、固定容量、instance 编码、capture 状态恢复、场景顺序、原创资产和 golden 范围。发现进入相应 Task 的修复/复审循环，最终 PASS/CLEAN 与命令证据写入 ledger。

- [ ] **Step 7: 提交文档对账，等待归档批准**

  ```bash
  git add AGENTS.md CLAUDE.md docs/notes/progress.md openspec/changes/bedrock-survival-hud
  git commit -m "docs: record survival HUD milestone"
  git status --short
  ```

  确认工作区只剩已知无关改动。不要自行归档；取得用户明确批准后再使用 `openspec-archive-change` 完成归档。

## 计划自检

- 每项可观察需求都有实现 Task、失败测试和最终门禁。
- 只增加一个必要的共享几何 helper 和一个 capture-only fixture；没有主题框架、PNG UI、GPU/ABI 改动或触屏占位实现。
- 生命/氧气在容器打开时仍可见且不改变命中几何；氧气满值继续完全隐藏。
- 新场景不会污染后续共享 application，末尾 LOD/水下顺序保持原契约。
- capture/golden 修改串行执行，避免与前置材质变更冲突。
