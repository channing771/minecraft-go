# 程序化方块云 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在现有 fullscreen sky pass 中加入世界锚定、随权威时间缓慢东移、昼夜变色并遮挡天体的稀疏方块云层。

**Architecture:** CPU 只向现有 sky uniform 追加相机世界位置和一个标量云偏移；fragment shader 将视线与固定 `Y=192` 平面求交，以 `16×16` 方块单元和固定整数哈希生成约 25% 覆盖的云岛。云在星星、太阳和月亮之后混合，因此天然遮挡天体；不增加纹理、mesh、buffer、pass 或每帧分配。

**Tech Stack:** Go 1.26、WGSL、现有 `internal/render` fullscreen sky pass、headless WebGPU tests、无窗口 capture、OpenSpec。

## Global Constraints

- 从设计提交 `dc61422` 创建独立 worktree 与分支 `codex/procedural-block-clouds`；执行时先使用 `superpowers:using-git-worktrees`。
- 使用用户现有 gvm Go 1.26，不下载 Go。
- 固定云平面 `Y=192`、cell `16×16` blocks；有效宏格约 75%，每个有效 `4×4` 宏格仅一个 5-cell 十字云岛，理论覆盖约 `23.4%`。
- 云向东移动：`CloudOffsetAt(worldTime) = float32(worldTime)/80`；shader 采样 `worldX-cloudOffset`，因此 80 tick 平移 1 block。
- 相机位于云层上方、视线平行/向下或交点不在正向时云 mask 必须为 0。
- 继续使用现有 sky pass 和 uniform buffer；不得新增 texture、sampler、mesh、GPU buffer、render pass、goroutine 或 CPU per-frame allocation。
- 云必须在星/月/日之后混合并随 `Daylight` 变暗；不得改变既有天体方向、星图或 terrain depth 遮挡。
- 性能只记录，不因数值变化失败；零分配、真实 overflow、shader/结构错误、测试失败和数据损坏仍是门禁。
- 并行实现阶段不得更新视觉 golden。前三条线路合入后，本分支最后 rebase，一次性无窗口重生成并逐张复核所有受天空影响的 golden。
- 不启动或聚焦前台游戏窗口；注释、GoDoc、测试说明与文档使用中文。

---

## File Map

- Create: `openspec/changes/procedural-block-clouds/**` — proposal、design、delta spec、tasks。
- Create: `openspec/changes/procedural-block-clouds/specs/celestial-sky-presentation/spec.md` — 云层可观察契约。
- Modify: `internal/render/daylight.go` — `CloudOffsetAt` 纯函数与常量。
- Modify: `internal/render/daylight_test.go` — 0/80 tick 与大时间有限值测试。
- Modify: `internal/render/renderer.go` — `Camera.CloudOffset`、112-byte sky uniform。
- Modify: `internal/render/shader/sky.wgsl` — 平面求交、哈希云岛、昼夜混色与天体遮挡。
- Modify: `internal/render/sky_test.go` — uniform、世界锚定、覆盖率、上下层、昼夜与遮挡 headless 测试。
- Modify: `internal/render/hot_path_allocation_test.go` — 保持 warmed Render 0 alloc。
- Modify: `cmd/mcgo/app.go` — 从权威 `worldTimeTicks` 传递云偏移。
- Modify after final rebase: `cmd/mcgo/testdata/golden/*.png` — 只提交实际受天空改变的图片。
- Archive: `openspec/changes/archive/2026-08-10-procedural-block-clouds/`。
- Modify on archive: `openspec/specs/celestial-sky-presentation/spec.md`。

### Task 1: 建立程序化云 OpenSpec change

**Files:**
- Create: `openspec/changes/procedural-block-clouds/**`

**Interfaces:**
- Consumes: `DayNightAt`、`render.Camera`、sky uniform/shader。
- Produces: 只修改 `celestial-sky-presentation` 的 strict-valid delta。

- [ ] **Step 1: 创建 change**

```bash
openspec new change procedural-block-clouds
openspec instructions proposal --change procedural-block-clouds --json
```

- [ ] **Step 2: 写 proposal、design 与 delta spec**

delta spec 必须包含：固定 Y/cell/覆盖形态；相同世界时间和相机得到相同图案；80 tick 东移 1 block；相机世界坐标视差；上方/无正交点不绘制；近地平线淡出；昼夜颜色；遮挡星/月/日；terrain depth 仍覆盖天空；零新增资源和零 CPU 热路径分配。

`design.md` 固化下面的唯一算法，不提供调节配置：

```text
intersection = camera.xz + direction.xz * ((192-camera.y)/direction.y)
cell          = floor(vec2(intersection.x-cloudOffset, intersection.y) / 16)
macro         = floor(cell / 4)
active        = hash(macro) & 3 != 0
center        = (1 + bit2(hash), 1 + bit3(hash))
filled        = active && manhattan(cell-macro*4, center) <= 1
```

哈希复用 `sky.wgsl` 现有 `hash_cell`，负 `vec2i` 通过 `bitcast<u32>` 进入哈希。颜色在现有星/月/日绘制后以固定 alpha `0.82` 和地平线 fade 混合。

- [ ] **Step 3: 校验并提交规划**

```bash
openspec validate procedural-block-clouds --strict --no-interactive
openspec validate --all --strict --no-interactive
git diff --check
git add openspec/changes/procedural-block-clouds
git commit -m "docs: 规划程序化方块云"
```

### Task 2: 用时间和 uniform 测试建立 RED

**Files:**
- Modify: `internal/render/daylight_test.go`
- Modify: `internal/render/sky_test.go`

**Interfaces:**
- Produces: `CloudOffsetAt` 的精确速度契约，以及 112-byte uniform layout。

- [ ] **Step 1: 写云偏移 RED**

```go
func TestCloudOffsetUsesAuthoritativeWorldTime(t *testing.T) {
	for _, test := range []struct {
		ticks uint64
		want  float32
	}{
		{0, 0}, {40, 0.5}, {80, 1}, {160, 2},
	} {
		if got := CloudOffsetAt(test.ticks); got != test.want {
			t.Fatalf("CloudOffsetAt(%d)=%v，想要 %v", test.ticks, got, test.want)
		}
	}
}
```

再断言 `CloudOffsetAt(math.MaxUint32)` 为 finite 且非负；不把云偏移塞进周期性的 `DayNight` struct，避免破坏天体 24000 tick 周期契约。

- [ ] **Step 2: 把 uniform 测试改为 112-byte RED**

构造 `Camera{Pos:mgl32.Vec3{24,64,-168}, CloudOffset:1.5}`，把 `TestSkyUniformLayoutAndUpload` 改为：buffer size/write length `112`；offset `96/100/104` 分别为 camera XYZ；offset `108` 为 cloud offset；`84..95` 仍是零 padding。

```bash
go test ./internal/render -run 'TestCloudOffset|TestSkyUniformLayoutAndUpload' -count=1
```

Expected: FAIL，因为 helper/字段/112-byte layout 尚未存在。

### Task 3: 扩展最小 CPU 数据路径

**Files:**
- Modify: `internal/render/daylight.go`
- Modify: `internal/render/renderer.go`
- Modify: `cmd/mcgo/app.go`
- Modify: `internal/render/daylight_test.go`
- Modify: `internal/render/sky_test.go`
- Modify: `internal/render/hot_path_allocation_test.go`

**Interfaces:**
- Produces: `CloudOffsetAt(uint64) float32`；`Camera.CloudOffset`；sky uniform 新 vec4。

- [ ] **Step 1: 实现纯偏移函数**

```go
const cloudTicksPerBlock = 80

// CloudOffsetAt 返回云层沿世界 X 正方向的方块偏移。
func CloudOffsetAt(worldTime uint64) float32 {
	return float32(worldTime) / cloudTicksPerBlock
}
```

不添加状态、缓存或 calibration 配置。

- [ ] **Step 2: 扩展 Camera 与 uniform**

在 `Camera` 追加 `CloudOffset float32`。把 `skyCameraData [96]byte` 和 sky buffer size 改为 `112`。`writeSkyCameraBytes` 保持现有 64-byte inverse matrix、16-byte sun/daylight、16-byte star/padding，并写：

```go
for index, value := range [...]float32{cam.Pos[0], cam.Pos[1], cam.Pos[2], cam.CloudOffset} {
	binary.LittleEndian.PutUint32(out[96+index*4:], math.Float32bits(value))
}
```

- [ ] **Step 3: 从 application 传权威时间**

只在现有 terrain/sky `render.Camera` 构造中追加：

```go
CloudOffset: render.CloudOffsetAt(a.worldTimeTicks),
```

实体 renderer 不消费该字段，无需重复设置。

- [ ] **Step 4: GREEN、mutation 与零分配**

```bash
gofmt -w internal/render/daylight.go internal/render/daylight_test.go \
  internal/render/renderer.go internal/render/sky_test.go \
  internal/render/hot_path_allocation_test.go cmd/mcgo/app.go
go test ./internal/render -run 'TestCloudOffset|TestSkyUniformLayoutAndUpload|TestRendererRenderDoesNotAllocate' -race -count=1
```

Mutation：临时把除数改为 79，80 tick 测试必须 FAIL；恢复。把 uniform offset 改回 96-byte 或漏写 CloudOffset，layout 测试必须 FAIL；恢复。

- [ ] **Step 5: 提交 CPU 数据路径**

```bash
git add internal/render/daylight.go internal/render/daylight_test.go \
  internal/render/renderer.go internal/render/sky_test.go \
  internal/render/hot_path_allocation_test.go cmd/mcgo/app.go \
  openspec/changes/procedural-block-clouds/tasks.md
git commit -m "feat: 向天空传递云层世界坐标"
```

### Task 4: 用 headless pixels 驱动 WGSL 云层 GREEN

**Files:**
- Modify: `internal/render/shader/sky.wgsl`
- Modify: `internal/render/sky_test.go`

**Interfaces:**
- Produces: 固定云 mask、昼夜色、天体遮挡；不增加 GPU resource。

- [ ] **Step 1: 先写五类 headless RED**

扩展 `skyCameraAt` 设置 `CloudOffsetAt(phase)`，并加入：

1. **无云区保持天体无平移视差：** 把旧“相机平移没有视差”两台相机改到 `Y>192`，两图仍逐字节相等。
2. **世界锚定与时间平移：** 下方相机 A `(24,64,-168)` + offset 0，与向东移动 16 blocks 且 offset 16 的相机 B 图像相等；只移动相机或只移动 offset 时图像不同。
3. **上下层边界：** 正午 offset 为 75，因此使用相机 `(99,64,-168)` 令中心采样仍落在固定云 cell 中心 `(24,-168)`；相机 `(99,200,-168)` 不出现云，水平/向下视线也不出现云。
4. **覆盖率：** 固定正午天顶、相机 X 同样补偿 offset，覆盖多个宏格的视野；以“与上层无云参考像素不同”计数，比例必须在 `20%..30%`。
5. **昼夜与天体遮挡：** 正午使用 `(99,64,-168)`，午夜 offset 为 225、使用 `(249,64,-168)`，两者中心都采样固定云 cell `(24,-168)`；正午中心太阳和午夜中心月亮都被云色压低，午夜云平均亮度低于正午云；云外星图保持既有计数下限。

这些测试在 shader 尚未改动时应因图像相等/无云而 RED，不能通过改阈值制造 RED。

- [ ] **Step 2: 扩展 WGSL uniform**

```wgsl
struct Sky {
    view_proj_inv:   mat4x4f,
    sun_daylight:    vec4f,
    star_visibility: vec4f,
    camera_cloud:    vec4f,
};
```

`camera_cloud.xyz` 是世界相机位置，`.w` 是向东偏移。

- [ ] **Step 3: 实现固定云 mask**

新增 `cloud_mask(direction: vec3f) -> f32`：

- `camera_cloud.y >= 192.0` 或 `direction.y <= 0.001` 返回 0；
- `distance=(192-cameraY)/direction.y`，`distance<=0` 返回 0；
- 交点 X 先减 `.w` 再按 16 格 floor；Z 不偏移；
- macro 用 `floor(vec2f(cell)/4.0)` 得到，负坐标不得截断向零；
- `hash_cell(vec3u(bitcast<u32>(macro.x), bitcast<u32>(macro.y), 0u))`；
- `hash&3u==0u` 返回 0；中心由 bit2/bit3 选择 1 或2；Manhattan 距离 `<=1` 才填充；
- 最终乘 `smoothstep(0.02,0.08,direction.y)` 地平线 fade。

- [ ] **Step 4: 在天体之后混合云色**

保留现有 gradient→stars→moon→sun 顺序，然后：

```wgsl
let cloud = cloud_mask(direction);
let cloud_night = vec3f(0.12, 0.14, 0.18);
let cloud_day = vec3f(0.90, 0.92, 0.94);
let cloud_color = mix(cloud_night, cloud_day, clamp(sky.sun_daylight.w, 0.0, 1.0));
color = mix(color, cloud_color, cloud * 0.82);
```

terrain 仍在同一 pass 后绘制并使用 depth，不改 pipeline 顺序。

- [ ] **Step 5: GREEN、mutation 与 render race**

```bash
go test ./internal/render -run 'TestSkyHeadlessPixels|TestSkyUniformLayoutAndUpload|TestRendererRenderDoesNotAllocate' -race -count=1
go test ./internal/render -race -count=1
go test ./cmd/mcgo -race -count=1
```

Mutation：临时去掉 `-cloudOffset`，世界锚定测试必须 FAIL；把宏格 active 改为总是 true，覆盖率必须 FAIL；把云混合移到天体之前，遮挡测试必须 FAIL。逐项恢复，mutation 不提交。

- [ ] **Step 6: 记录渲染成本并提交**

在 `sky_test.go` 增加一个最小 `BenchmarkSkyRender`：复用测试 fake device/encoder，先预热一次，再反复 `Render` 并调用 `b.ReportAllocs()`；结果只写本地报告。

```bash
go test ./internal/render -run '^$' -bench BenchmarkSkyRender -benchmem -count=5
git add internal/render/shader/sky.wgsl internal/render/sky_test.go \
  openspec/changes/procedural-block-clouds/tasks.md
git commit -m "feat: 渲染程序化方块云"
```

### Task 5: 并行阶段完整验证并暂停视觉更新

**Files:**
- Modify: `openspec/changes/procedural-block-clouds/tasks.md`

- [ ] **Step 1: 运行非 golden 完整门禁**

```bash
go test ./internal/render ./internal/client ./cmd/mcgo -race -count=1
go test ./internal/archcheck -count=1
go test ./... -race
go vet ./...
gofmt -l .
git diff --check
openspec validate --all --strict --no-interactive
```

Expected: 全部 exit 0。此时 `make visual-check` 因天空预期变化可能失败，不得据此放宽阈值或提前更新 golden。

- [ ] **Step 2: 请求独立代码评审**

评审重点：世界坐标/负坐标、东移符号、上方边界、覆盖率、WGSL uniform alignment、天体后混合、零新增资源和零分配。修复后重跑 Step 1。

- [ ] **Step 3: 暂停在最终 rebase 边界**

确认 storage、oak-tree、light-recipe 三个 PR 尚未全部合入时，不执行 Task 6、不归档、不推送 ready PR。允许推送开发分支备份，但不得把旧 golden 当最终结果。

### Task 6: 最后 rebase 并一次性更新所有天空 golden

**Files:**
- Modify: `cmd/mcgo/testdata/golden/*.png`（仅实际变化者）
- Modify: `openspec/changes/procedural-block-clouds/tasks.md`

**Interfaces:**
- Consumes: 已合入 storage→trees→recipe 的最新远端 main，其中包含 `oak-grove` 和八行 `inventory-crafting`。
- Produces: 最终集成视觉基线；这是四路唯一计划内冲突点。

- [ ] **Step 1: 确认前三项已按顺序合入并 rebase**

```bash
git fetch origin
git log --oneline --decorate -8 origin/main
git rebase origin/main
git status --short --branch
```

Expected: rebase 无未解决冲突；生产代码仍只属于云线路；`oak-grove` 场景与 recipe 8 已存在。

- [ ] **Step 2: 一次性无窗口更新视觉基线**

```bash
make visual-update
git diff --name-only -- cmd/mcgo/testdata/golden
make visual-check
```

逐张只读检查全部十个场景及所有变化图片：云保持稀疏方块岛、不遮满天空；正午/午夜颜色合理；星/月/日被云覆盖处确实在云后；地形/HUD/树木没有回归；`oak-grove` 也包含最终云层。不得接受与天空无关的随机字体、HUD 或地形漂移。

- [ ] **Step 3: 提交最终 golden**

```bash
git add cmd/mcgo/testdata/golden openspec/changes/procedural-block-clouds/tasks.md
git commit -m "test: 更新方块云视觉基线"
```

### Task 7: 最终门禁、评审、归档与交付

**Files:**
- Archive: `openspec/changes/archive/2026-08-10-procedural-block-clouds/**`
- Modify: `openspec/specs/celestial-sky-presentation/spec.md`

- [ ] **Step 1: 在 rebase 后重跑全部门禁**

```bash
go test ./internal/render ./internal/client ./cmd/mcgo -race -count=1
go test ./internal/archcheck -count=1
go test ./... -race
go vet ./...
go test ./internal/render -run '^$' -bench BenchmarkSkyRender -benchmem -count=5
make visual-check
gofmt -l .
git diff --check
openspec validate --all --strict --no-interactive
```

Expected: 全部 exit 0；benchmark 只记录。

- [ ] **Step 2: 请求最终代码与视觉评审**

评审范围必须包含 rebase 后累积 diff 和全部变化 golden。任何天空外漂移、uniform 越界、每帧分配或上方相机云影都必须修复。

- [ ] **Step 3: 归档并提交**

全部 tasks 勾选后：

```bash
openspec archive procedural-block-clouds -y
openspec validate --all --strict --no-interactive
openspec list --json
git diff --check
git add openspec/changes/archive/2026-08-10-procedural-block-clouds \
  openspec/specs/celestial-sky-presentation/spec.md
git commit -m "docs: 归档程序化方块云"
git status --short --branch
```

Expected: active list 不含该 change；除用户原有日志外 worktree clean，可作为四路最后一个 PR 推送与合入。
