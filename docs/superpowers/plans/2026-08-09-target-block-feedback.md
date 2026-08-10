# 目标方块轮廓与中文名称 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在准星六格内命中完整已加载的注册方块时，显示受地形深度遮挡的细轮廓和中文方块名；任何未知状态或界面遮挡立即隐藏。

**Architecture:** 目标是 `application.renderFrame` 每帧从 Predictor、Camera 与只读 Mirror 临时算出的局部值，不进入网络、预测或存档。专用 `BlockOutlineRenderer` 复用 render 包已有 avatar 立方体数据、shader 与编码 helper，固定十二实例；名称复用既有 NameTagRenderer，只把容量从 7 扩成 8。

**Tech Stack:** Go 1.26（使用用户已有 gvm）、现有 `core.RaycastBlocks`、WebGPU `internal/gfx` 抽象、avatar/name-tag 渲染资源、无窗口视觉抓帧、OpenSpec。

## Global Constraints

- [ ] 共同前置：`m4n-static-block-light` 已归档并合入 `main`；同步远程后冻结与另外两项相同的 `BASE_SHA`。

```bash
cd /Users/chen/chenwork/minecraft-go
BASE_SHA=$(git rev-parse main)
git worktree add .worktrees/target-block-feedback -b codex/target-block-feedback "$BASE_SHA"
cd /Users/chen/chenwork/minecraft-go/.worktrees/target-block-feedback
test "$(git rev-parse HEAD)" = "$BASE_SHA"
```

- [ ] 本分支不得修改 `AGENTS.md`、`CLAUDE.md`、`README.md`、`openspec/config.yaml`、世界生成/迁移、配方/熔炉代码或 `inventory-crafting.png`。
- [ ] 本任务拥有新 `target-block-feedback.png`，以及普通目标提示导致真实变化的其他既有 golden；背包打开隐藏目标，因此 `inventory-crafting.png` 必须逐字节不变。
- [ ] 不下载 Go；不启动或聚焦前台游戏窗口。所有 GPU 验证使用 headless device/`--capture`。
- [ ] 不增加 packet、协议版本、schema、外部字体、第二套 atlas 或通用 overlay 框架。
- [ ] 每个任务组 RED→GREEN→mutation→验证→勾选→提交，然后自动继续。

---

## Task 1: 创建目标反馈 OpenSpec change

**Files:**
- Create: `openspec/changes/target-block-feedback/proposal.md`
- Create: `openspec/changes/target-block-feedback/design.md`
- Create: `openspec/changes/target-block-feedback/specs/voxel-visual-presentation/spec.md`
- Create: `openspec/changes/target-block-feedback/specs/visual-verification/spec.md`
- Create: `openspec/changes/target-block-feedback/tasks.md`

- [ ] 1.1 读取批准设计与两份主规格，用 `openspec-propose` 建立 change。
- [ ] 1.2 `voxel-visual-presentation` 增加：六格本地射线、完整已加载路径、未知/未注册/UI/未 ready 隐藏、十二边深度只读 alpha pass、全部注册方块中文名、7+1 名牌容量与稳定态零分配。`visual-verification` 追加末场景并明确正常渲染链、正确遮挡、`inventory-crafting` 不变。
- [ ] 1.3 design 固定本计划的几何常量、pass 顺序和文件所有权；tasks 映射 Task 2–6。
- [ ] 1.4 严格校验并提交：

```bash
openspec validate target-block-feedback --strict --no-interactive
openspec validate --all --strict --no-interactive
git diff --check
git add openspec/changes/target-block-feedback
git commit -m "docs: 规划目标方块视觉反馈"
```

---

## Task 2: 固定全部方块中文名称与本地目标选择

**Files:**
- Create: `internal/core/block_name.go`
- Create: `internal/core/block_name_test.go`
- Create: `cmd/mcgo/target_block.go`
- Create: `cmd/mcgo/target_block_test.go`

- [ ] 2.1 写名称 RED：对 `AirID..MossyCobblestoneID` 每个注册 ID 查询成功且非空；未知 ID 返回 `"",false`。精确名称数组：

```go
var blockDisplayNames = [...]string{
	"空气", "屏障", "石头", "泥土", "草方块", "基岩", "石砖",
	"煤矿石", "铁矿石", "熔炉", "铁块", "箱子", "发光块", "圆石",
	"平滑石", "沙子", "砾石", "橡木原木", "橡木木板", "树叶", "玻璃",
	"砖块", "白色羊毛", "红色瓦块", "黏土", "雪块", "苔藓圆石",
}
```

- [ ] 2.2 最小实现：`BlockDisplayName(id BlockID) (string,bool)` 先调用 `RegisteredBlock`，再按数组索引返回；不建本地化系统或 map。
- [ ] 2.3 写目标 RED。用正常 Mirror snapshot 构造空气路径和一个方块，并把 `client.Predictor` Begin 为 Ready；覆盖：六格内命中、超过六格、路径中缺失 chunk、命中 unknown（测试可在已接受 MirrorChunk 上直接写入未知 ID）、空气、Predictor 未 ready、背包打开、熔炉/箱子打开、debug panel 可见。
- [ ] 2.4 目标类型只保留当前帧所需值：

```go
type blockTarget struct {
	Position core.BlockPos
	Name     string
}
```

`application.currentBlockTarget()` 先检查 ready 和 UI；再用 `core.RaycastBlocks(camera.Pos,camera.Forward(),6,lookup)`。lookup 遇到未加载或未注册值返回包内 sentinel error，使射线不能穿过未知区域；只把注册非空气当 solid。任何 error/found=false/空名称都返回 false，不在正常未知边界每帧打日志。
- [ ] 2.5 运行 RED/GREEN/mutation：把未加载从 error 改成 air 时“未知路径阻断”必须失败；恢复后：

```bash
gofmt -w internal/core/block_name.go internal/core/block_name_test.go cmd/mcgo/target_block.go cmd/mcgo/target_block_test.go
zsh -ic 'go test ./internal/core ./cmd/mcgo -run "BlockDisplayName|CurrentBlockTarget" -race -count=1'
git diff --check
```

- [ ] 2.6 勾选并提交：

```bash
git add internal/core/block_name.go internal/core/block_name_test.go cmd/mcgo/target_block.go cmd/mcgo/target_block_test.go openspec/changes/target-block-feedback/tasks.md
git commit -m "feat: 计算本地目标方块与中文名"
```

---

## Task 3: 实现固定十二边的深度轮廓 renderer

**Files:**
- Create: `internal/render/block_outline.go`
- Create: `internal/render/block_outline_test.go`

- [ ] 3.1 写几何 RED：`buildBlockOutlineParts` 恰好返回 12 个 `avatarPart`；4 条 X 边、4 条 Y 边、4 条 Z 边；整体 bounds 从 `position-0.003` 到 `position+1.003`；长边长度 `1.006`，另外两轴厚度 `0.018`；所有 alpha 固定 `0.86`。
- [ ] 3.2 写资源/pass RED：构造时恰好三个固定 buffer；dynamic upload 大小 `1236` bytes（camera `0..79`、instances 从 `256` 起共 `12*80`、indirect `1216..1235`）；pipeline 使用 `Depth32Float`、`DepthWrite=false`、`BlendAlpha`；无目标零 pass，有目标一次 indexed indirect draw；Release 幂等且只释放自有 handle。
- [ ] 3.3 写稳定态零分配 RED：预分配 `parts cap=12` 与 `upload len=1236`，`testing.AllocsPerRun(100, ...) == 0`。
- [ ] 3.4 运行 RED：

```bash
zsh -ic 'go test ./internal/render -run "BlockOutline" -count=1'
```

- [ ] 3.5 在一个文件内实现专用 renderer，直接复用同包的 `avatarPart`、`avatarCuboid`、`avatarCubeVertices`、`avatarCubeIndices`、`avatarShader`、`encodeAvatarPartsInto`、`encodeAvatarCameraInto` 和 uint32 encoder；十二条边统一使用 `[4]float32{1,1,1,blockOutlineAlpha}`。固定常量：

```go
const (
	blockOutlineParts          = 12
	blockOutlineExpand float32 = 0.003
	blockOutlineWidth  float32 = 0.018
	blockOutlineAlpha  float32 = 0.86
)
```

公开值保持最小：

```go
type BlockOutline struct {
	Visible  bool
	Position core.BlockPos
}
```

不要抽取 avatar “基类”、共享 GPU handle 或新 shader。

- [ ] 3.6 headless 测试先清颜色/深度、画一个遮挡方块、再画 outline 并 Submit/Poll，确保 WGSL/bind/depth descriptor 被真实 WebGPU 接受；不得创建前台窗口。
- [ ] 3.7 GREEN 与 mutation：把 `DepthWrite` 改 true 时 descriptor 测试失败；恢复后运行：

```bash
gofmt -w internal/render/block_outline.go internal/render/block_outline_test.go
zsh -ic 'go test ./internal/render -run "BlockOutline" -race -count=1'
zsh -ic 'go test ./internal/render -race -count=1'
zsh -ic 'go test ./internal/archcheck -count=1'
git diff --check
```

- [ ] 3.8 勾选并提交：

```bash
git add internal/render/block_outline.go internal/render/block_outline_test.go openspec/changes/target-block-feedback/tasks.md
git commit -m "feat: 绘制深度正确的方块轮廓"
```

---

## Task 4: 把目标轮廓与名称接入正常帧

**Files:**
- Modify: `internal/render/name_tag.go`
- Modify: `internal/render/name_tag_test.go`
- Modify: `internal/render/dynamic_upload_test.go`
- Modify: `internal/render/multiplayer_bench_test.go`
- Modify: `cmd/mcgo/app.go`
- Modify: `cmd/mcgo/app_test.go`
- Modify: `cmd/mcgo/multiplayer_benchmark.go`

- [ ] 4.1 先把 NameTag capacity RED 改为 8：输入 9 个时稳定排序后保留 8；背景上限 8、glyph 上限 256、dynamic upload 精确 `17152` bytes；现有七名远端玩家测试仍成立。
- [ ] 4.2 最小更新：`maxNameTags=8`、`maxNameTagGlyphs=256`，保持 `nameTagGlyphOffset=768`，其余 renderer 不变。不要改变 remote avatar 上限 7。
- [ ] 4.3 在 `application` 增加 `blockOutlineRenderer`；dependencies 增加与现有 renderer 一致的构造注入；正常构造、失败清理、`releaseRemoteConstructionResources`、`releaseOwnedResources` 和生命周期测试全部接线。`remoteNameTags` 在 application 构造时预分配 `cap=8`。
- [ ] 4.4 `renderFrame` 在远端 presentations 转换后只调用一次 `currentBlockTarget`。命中时 append 一个 `PlayerID{}` 的 NameTag，锚点为 `(X+0.5,Y+1.15,Z+0.5)`，并创建 `BlockOutline{Visible:true}`；未命中使用零值。NameTag `Prepare` 仍只调用一次。
- [ ] 4.5 draw 顺序固定为：terrain → avatar → item drops → block outline → name tags → damage overlay → HUD → debug panel。轮廓共享本帧 `viewProj/daylight/depth`；服务端命令路径不读取这个局部 target。
- [ ] 4.6 更新 multiplayer benchmark 的 NameTag 准备容量但不伪造 target；性能只记录。添加 app 测试验证有目标时 outline pass 位于 item-drop 与 name-tag 之间、背包/容器/debug 时无 outline/name、构造失败不泄漏。
- [ ] 4.7 运行 mutation：删掉 `inventoryOpen` guard 后隐藏测试必须失败；恢复后运行：

```bash
gofmt -w internal/render/name_tag.go internal/render/name_tag_test.go internal/render/dynamic_upload_test.go internal/render/multiplayer_bench_test.go cmd/mcgo/app.go cmd/mcgo/app_test.go cmd/mcgo/multiplayer_benchmark.go
zsh -ic 'go test ./internal/render ./cmd/mcgo -run "NameTag|BlockTarget|RenderOrder|Application.*Release" -race -count=1'
zsh -ic 'go test ./internal/render ./cmd/mcgo -race -count=1'
zsh -ic 'go test ./internal/render -run ^$ -bench "Benchmark.*NameTag|Benchmark.*Multiplayer" -benchmem -count=5'
git diff --check
```

Expected: 正确性与零分配门禁通过；benchmark 数值只记录。

- [ ] 4.8 勾选并提交：

```bash
git add internal/render cmd/mcgo/app.go cmd/mcgo/app_test.go cmd/mcgo/multiplayer_benchmark.go openspec/changes/target-block-feedback/tasks.md
git commit -m "feat: 在正常帧呈现目标方块反馈"
```

---

## Task 5: 追加无窗口 target-block-feedback 场景

**Files:**
- Modify: `cmd/mcgo/capture.go`
- Modify: `cmd/mcgo/capture_test.go`
- Create: `cmd/mcgo/testdata/golden/target-block-feedback.png`
- Modify: only actually changed existing PNGs except `inventory-crafting.png`

- [ ] 5.1 写 capture RED：末场景名称必须是 `target-block-feedback`；`Prepare` 走 `prepareCaptureAirNeighborhood`，只在 `{X:0,Y:3,Z:-3}` 放 `BrickID` 并经 Mirror/Mesher dirty；`Apply` 固定正午、camera `{0.5,3.5,2.5}`、Yaw/Pitch 0、关闭 inventory/container/debug/remote 状态；Ready predictor 下 current target 必须是该砖块且名称“砖块”。
- [ ] 5.2 实现 `prepareTargetBlockFeedback` 和列表末项，不增加 capture-only target 开关：

```go
{
	Name: "target-block-feedback", WarmupFrames: 8,
	Prepare: prepareTargetBlockFeedback,
	Apply: func(app *application) error {
		app.worldTimeTicks = 6000
		app.camera.Pos = mgl32.Vec3{0.5, 3.5, 2.5}
		app.camera.Yaw, app.camera.Pitch = 0, 0
		app.inventoryOpen = false
		app.inventorySource = -1
		app.remotePlayers.Reset()
		app.furnace.Reset()
		app.chest.Reset()
		if app.panel != nil { app.panel.visible = false }
		return app.inventory.Apply(network.InventoryState{Inventory: core.Inventory{}})
	},
}
```

- [ ] 5.3 focused 验证：

```bash
gofmt -w cmd/mcgo/capture.go cmd/mcgo/capture_test.go
zsh -ic 'go test ./cmd/mcgo -run "TargetBlockFeedback|CaptureScene|MaterialsShowcase" -race -count=1'
```

- [ ] 5.4 显式更新全部无窗口场景并比对：

```bash
zsh -ic 'make visual-update VISUAL_OUT=/private/tmp/target-block-feedback-visual-update'
test -f cmd/mcgo/testdata/golden/target-block-feedback.png
test -z "$(git diff --name-only -- cmd/mcgo/testdata/golden/inventory-crafting.png)"
zsh -ic 'make visual-check VISUAL_OUT=/private/tmp/target-block-feedback-visual-check'
git diff --check
```

- [ ] 5.5 逐张只读查看新图和所有实际变化的旧图：新图必须看见砖块细边、中文“砖块”、后缘被本体深度遮挡；已有场景只能因正常准星命中出现目标反馈，不能改阈值或用场景开关隐藏。记录实际 PNG 清单。
- [ ] 5.6 勾选并提交，只 stage 实际审查通过的 PNG：

```bash
git add cmd/mcgo/capture.go cmd/mcgo/capture_test.go cmd/mcgo/testdata/golden openspec/changes/target-block-feedback/tasks.md
git commit -m "test: 增加目标方块无窗口视觉场景"
```

---

## Task 6: 全仓验证、归档与独立 PR

**Files:**
- Modify: `openspec/changes/target-block-feedback/tasks.md`
- Sync: `openspec/specs/voxel-visual-presentation/spec.md`
- Sync: `openspec/specs/visual-verification/spec.md`
- Archive: `openspec/changes/archive/2026-08-09-target-block-feedback/**`

- [ ] 6.1 完整门禁：

```bash
zsh -ic 'go test ./internal/core ./internal/render ./cmd/mcgo -race -count=1'
zsh -ic 'go test ./internal/archcheck -count=1'
zsh -ic 'go test ./... -race'
zsh -ic 'go vet ./...'
zsh -ic 'make visual-check VISUAL_OUT=/private/tmp/target-block-feedback-final-visual'
zsh -ic 'go test ./internal/render -run ^$ -bench "Benchmark.*NameTag|Benchmark.*Multiplayer" -benchmem -count=5'
gofmt -l .
git diff --check
openspec validate --all --strict --no-interactive
```

Expected: 全部成功，gofmt 无输出；性能数值仅记录。

- [ ] 6.2 请求独立代码和视觉评审；修复本 change 范围 finding，重跑相称门禁。
- [ ] 6.3 同步 delta、确认 tasks 全勾并归档：

```bash
openspec archive target-block-feedback --yes
openspec validate --all --strict --no-interactive
test -z "$(openspec list --json | grep target-block-feedback || true)"
git diff --check
```

- [ ] 6.4 提交、推送并创建 PR：

```bash
git add internal/core internal/render cmd/mcgo openspec
git commit -m "docs: 归档目标方块视觉反馈"
git push -u origin codex/target-block-feedback
gh pr create --base main --head codex/target-block-feedback --title "feat: 增加目标方块轮廓与中文名" --body-file /private/tmp/target-block-feedback-pr.md
```

- [ ] 6.5 PR 描述列出目标隐藏条件、十二实例/8 名牌容量、pass 顺序、实际 golden 清单、`inventory-crafting` 未变、无协议/schema 改动和完整 headless 验证证据。
