## Why

仓库从未验证过任何一个像素。`internal/render` 下有十余个测试文件，但断言的全部是 CPU 侧 `Prepare` 的产物——quad 数量、字节偏移、公式输出。着色器算错、混合模式配错、深度状态配错、顶点位置符号写反，这些都不改变 quad 数量与偏移量，因此能完整通过现有全部门禁。M4L 的血条就是这样验收的：依据是 `maxHotbarQuads = 147` 与 `hotbarGlyphOffset = 7424` 两个常量。

`internal/gfx` 已有 `Buffer.ReadBack()`，但 `Texture` 接口没有任何回读能力，渲染结果无法离开 GPU。下一个功能里程碑是光照传播——典型的"算法单测全过、屏幕上就是不对"的领域，在它开工前补上这条验证轴，收益最大。

## What Changes

- `internal/gfx` 的 `Texture` 接口新增回读能力，并新增 `TextureUsageCopySrc` 用途位。回读结果的行距必须是紧凑的，由实现负责消化 WebGPU 对 `CopyTextureToBuffer` 的 256 字节行距对齐要求。
- `cmd/mcgo` 新增 `--capture <dir>` 无头抓帧模式，在固定 tick 上把渲染结果写成 PNG；新增 `--update-golden` 用于显式重建基线。两者与 `--benchmark`、`--connect` 互斥。
- 新增双阈值图像比对器与三张 golden 基线图（`terrain-noon`、`hud-hotbar-health`、`avatar-nametag`），分辨率 640×360。
- 新增 `make visual-check` / `make visual-update` 门禁入口。

不改协议、不改存档格式、不改 benchmark 场景定义、不改任何既有性能门禁，因此无迁移。

## Capabilities

### New Capabilities

- `visual-verification`: 渲染结果的像素级验证——回读、抓帧、与冻结基线的双阈值比对，以及基线更新的显式性要求。

### Modified Capabilities

（无。本变更不改变任何既有能力的行为契约，只新增一条验证轴。）

## Impact

- `internal/gfx/gfx.go`、`internal/gfx/wgpu.go`：`Texture` 接口与 wgpu 实现。这是本变更唯一的架构边界改动。
- `cmd/mcgo/app.go`、`cmd/mcgo/main.go`：无头分支的选择条件、分辨率与纹理用途；标志解析与互斥校验。
- `cmd/mcgo/capture.go`、`cmd/mcgo/visual_compare.go`（新增）：抓帧驱动、场景表、PNG 输出、比对器。
- `cmd/mcgo/testdata/golden/*.png`（新增）：三张基线图，合计应在百 KB 量级。
- `internal/render/font_atlas_test.go`、`cmd/mcgo/app_test.go`：两个 `gfx.Texture` 测试替身需补齐新方法，否则编译不过。
- `Makefile`、`README.md`：门禁入口与使用说明。
- 依赖：不新增任何依赖。PNG 编码用标准库 `image/png`。
- CI：本变更**不**把视觉门禁接入 CI。仓库的 `ci.yml` 只有一个 `test` job，接入需要新增 job 并处理 runner 的 GPU 可用性，且在拿到实测漂移数据前无法确定接入形态。留作独立变更。
