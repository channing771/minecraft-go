> 完整步骤级代码见 `docs/superpowers/plans/2026-08-07-visual-verification.md`。本文件是可勾选的执行顺序与验证命令。
> 六个任务组与该计划的 Task 1–6 一一对应。

## 1. gfx 纹理回读

- [ ] 1.1 在 `internal/gfx/texture_readback_test.go`（新建，需 `//go:build darwin`）先写失败测试：非对齐宽度（100×100 RGBA，每行 400 字节，对齐到 512）回读逐字节相等；恰好对齐宽度（64×8 RGBA，每行 256 字节）回读逐字节相等。确认失败原因是断言而非编译错误。
- [ ] 1.2 在 `internal/gfx/gfx.go` 的 `TextureUsage` 末尾追加 `TextureUsageCopySrc`（必须追加在已有枚举之后以保持位值稳定），并在 `Texture` 接口增加读回方法。
- [ ] 1.3 在 `internal/gfx/wgpu.go` 的 `toTextureUsage` 增加 `CopySrc` 映射，并实现读回：`CopyTextureToBuffer` → staging buffer → `MapAsync`，读回后按 256 字节行距逐行紧缩。越界 layer/mip 必须 panic 并带中文错误。
- [ ] 1.4 补齐两个 `gfx.Texture` 测试替身：`cmd/mcgo/app_test.go` 的 `integrationTexture`、`internal/render/font_atlas_test.go` 的 `glyphTestTexture`，均返回 nil。
- [ ] 1.5 验证：`go test ./internal/gfx ./internal/render ./cmd/mcgo -race -count=1`、`go test ./internal/archcheck -count=1`、`go vet ./internal/gfx ./internal/render ./cmd/mcgo`、`gofmt -l internal/gfx internal/render cmd/mcgo` 无输出。
- [ ] 1.6 变异验证：把行距对齐计算改成不对齐，确认 1.1 的非对齐用例变红；恢复后 `git diff` 干净。提交 `feat: 增加 gfx 纹理回读`。

## 2. --capture 标志与互斥校验

- [ ] 2.1 在 `cmd/mcgo/main_test.go` 先写失败测试：`--capture` 值透传到 `mainOptions` 与 `applicationOptions`；`--capture` 与 `--benchmark`、`--capture` 与 `--connect` 分别被拒绝；未指定时字段为空。
- [ ] 2.2 在 `cmd/mcgo/app.go` 的 `applicationOptions` 与 `cmd/mcgo/main.go` 的 `mainOptions` 增加 `CaptureDir string`，在 `parseMainOptions` 增加标志与两条互斥校验。
- [ ] 2.3 验证：`go test ./cmd/mcgo -run TestParseMainOptions -race -count=1`、`gofmt -l cmd/mcgo` 无输出。提交 `feat: 增加 --capture 标志与互斥校验`。

## 3. 抓帧驱动、PNG 输出与 terrain-noon 场景

- [ ] 3.1 在 `cmd/mcgo/capture_test.go`（新建）先写失败测试：BGRA→NRGBA 的通道交换与 alpha 恒定 255；两行两列不发生行列错位。
- [ ] 3.2 新建 `cmd/mcgo/capture.go`：`captureWidth = 640`、`captureHeight = 360`、表驱动的 `captureScenes`（本组只含 `terrain-noon`）、抓帧驱动与 PNG 写出。抓帧顺序必须是「预热若干帧 → 收服务端消息 → 应用场景状态 → 渲染 → 回读」，场景状态的应用必须在收消息之后，否则会被当帧消息覆盖。
- [ ] 3.3 在 `cmd/mcgo/app.go` 把无头分支的条件从 `options.Benchmark` 改为「benchmark 或抓帧」，抓帧时分辨率取 640×360，offscreen 纹理用途增加 `CopySrc`。逐个复核函数内其余判断 `options.Benchmark` 的位置：语义是"无窗口"的改判，语义是"跑性能场景"的保留。
- [ ] 3.4 在 `cmd/mcgo/main.go` 的 `runDependencies` 增加 `runCapture` 字段并接进 `runWithDependencies`。
- [ ] 3.5 验证：`go test ./cmd/mcgo -race -count=1`、`go vet ./cmd/mcgo`、`gofmt -l cmd/mcgo` 无输出。
- [ ] 3.6 端到端跑 `go run ./cmd/mcgo --capture /tmp/mcgo-shots`，确认产出 640×360 的 `terrain-noon.png`，并**实际打开该图查看**——数值检查无法发现"地形根本没画上"。提交 `feat: 增加视觉抓帧出口与 terrain-noon 场景`。

## 4. 双阈值比对器

- [ ] 4.1 在 `cmd/mcgo/visual_compare_test.go`（新建）先写失败测试，覆盖四种情形：全等、单像素高差值、大面积每通道差 1（须在阈值内）、尺寸不匹配报错；另加一条整图差 50 须超阈值。
- [ ] 4.2 新建 `cmd/mcgo/visual_compare.go`：`diffThreshold`（最大通道差 + 超差像素占比）、`imageDiff`、`compareImages`（返回量化差异与差异可视化图）、`withinThreshold`。比对器只负责测量，阈值裁决交给调用方。只比 RGB，不比 alpha。
- [ ] 4.3 验证：`go test ./cmd/mcgo -race -count=1`、`gofmt -l cmd/mcgo` 无输出。
- [ ] 4.4 变异验证：删掉差值取绝对值的那一步，确认至少一条用例变红；恢复后 `git diff` 干净。提交 `feat: 增加双阈值图像比对器`。

## 5. HUD 与远端玩家视觉场景

- [ ] 5.1 在 `captureScenes` 增加 `hud-hotbar-health`：经 `client.InventoryMirror.Apply` 注入含石砖、石镐（耐久 40/131，磨损条画在偏左位置）、铁镐与背包煤炭的物品状态。用 `ponytail:` 注释标明生命值只覆盖满血 20，部分心形不在覆盖范围内及其原因。
- [ ] 5.2 在 `captureScenes` 增加 `avatar-nametag`：经 `client.RemotePlayers.Apply` 注入一名远端玩家，昵称混用 ASCII 与非 ASCII 以覆盖宽字符字形路径。先读 `applySpawn` 确认零值字段不会被拒，若会被拒则补合法值并在提交信息说明。
- [ ] 5.3 验证：`go test ./cmd/mcgo -race -count=1`、`gofmt -l cmd/mcgo` 无输出；跑一次抓帧并**逐张打开三张图确认**——HUD 场景能看到磨损条与血条，名牌场景的中日文字形没有变成方框或缺字。发现问题必须在本组修完，因为下一组就要把它们冻成基线。提交 `feat: 增加 HUD 与远端玩家视觉场景`。

## 6. golden 基线、阈值实测与门禁接入

- [ ] 6.1 增加 `--update-golden` 标志（只能与 `--capture` 同用），并实现比对流程：更新模式写基线；比对模式读基线、超阈值时输出实拍图与差异图并报错；**基线缺失且未请求更新时必须报错，不得静默创建**。
- [ ] 6.2 先用 `--update-golden` 生成基线，再在同一台机器重复抓帧至少 10 次，记录每次的最大通道差与超差像素占比。阈值取实测最大值再留一档余量，写入 `captureThresholds`，并把实测分布与选定值回填 `docs/superpowers/specs/2026-08-07-visual-verification-design.md` §6 与本变更的 `design.md`。
- [ ] 6.3 提交三张基线图到 `cmd/mcgo/testdata/golden/`，确认合计在百 KB 量级；显著更大时先查分辨率或格式配置，不得直接入库。
- [ ] 6.4 增加 `make visual-check` 与 `make visual-update`（先读 `Makefile` 现有目标写法保持一致），并在 `README.md` 说明怎么跑、基线在哪、什么时候该更新基线（渲染行为**有意**改变时）与什么时候不该更新（比对红了但原因不明——那是要查的信号）。
- [ ] 6.5 反向验证：故意改坏一处渲染参数（如正午日光值 1 → 0.9），确认 `make visual-check` 对应场景变红且差异图能指出位置；恢复后确认全绿且 `git diff` 干净。阈值过松或场景未覆盖到该区域时必须修，不得放过。
- [ ] 6.6 收尾门禁：`gofmt -l .` 无输出、`go vet ./...`、`go test ./... -race`、`go test ./internal/archcheck -count=1`、`git diff --check` 无输出、`openspec validate --all --strict --no-interactive`。若遇 `macos-latest` 已知时序假失败（如 `TestScenarioV7EightSessionServerProbeIsRealAndBounded`），按已知抖动处理，**不得改阈值**。提交 `feat: 冻结视觉 golden 基线并接入 make 门禁`。
