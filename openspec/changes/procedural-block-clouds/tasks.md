## 1. 无窗口 RED 契约

- [x] 1.1 在 `internal/render/sky_test.go` 增加直接执行嵌入 production WGSL 的无窗口 headless 行为回归与 mutation 门禁，覆盖 Y=`192`、`16` block cell、`4×4` macro 十字形，以及固定样本 `macro.x, macro.y ∈ [-8, 7]` 的 `256` 个 macro：`hash_cell(vec3u(bitcast<u32>(x), bitcast<u32>(y), 0u)) & 3` 的低两位计数必须为 `72/69/62/53`、活动 macro 必须为 `184`、填充 cell 必须为 `920/4096`、覆盖率必须为 `22.4609375%`（理论 `3/4` 与 `15/64`）；相同输入确定性、每 `80` tick 东移 `1` block、X/Z 世界坐标视差、上方/无正交交点不绘制、地平线 fade、昼夜颜色、云遮挡星/月/日及 terrain 深度覆盖；运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/render -run TestSkyHeadless -count=1'`，并以 production hash、二维输入、inactive/Manhattan/center 与相机/时间/合成 targeted mutation 确认回归稳定 RED。

## 2. 固定 uniform 与 shader

- [x] 2.1 在 `internal/render/daylight.go`、`renderer.go` 与 `sky_test.go` 以既有 `render.Camera`、`DayNightAt` 和单一 sky uniform 传递相机世界坐标及拆分后的世界时间偏移：`0..63` block 局部 `f32` 和紧随 `star_visibility` 的 typed `u32` macro X 偏移；总长保持 `112` bytes、一次上传、一次 draw 和零 Go 热路径分配，并覆盖 `2^28`、`2^31`、`MaxUint64` 邻近值和 64-block rollover 的 80-tick 连续性；运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/render -run "TestSky(UniformLayoutAndUpload|PipelineConfiguration)|TestRendererRenderDoesNotAllocate|TestCloudOffset" -race -count=1'`。
- [x] 2.2 在 `internal/render/shader/sky.wgsl` 复用 `hash_cell`，按 `design.md` 的唯一交点/cell/macro/hash/十字形算法实现云，负 `vec2i` 以 `bitcast<u32>` 哈希，并在现有星/月/日之后用 alpha `0.82` 与固定地平线 fade 合成；不得增加资源、pass、draw 或可调配置。运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/render -run TestSkyHeadless -race -count=1'`。

## 3. 回归与成本门禁

- [x] 3.1 完成 `internal/render/sky_test.go` 的固定样本 `macro.x, macro.y ∈ [-8, 7]`：以 test-only compute entry 直接执行嵌入的生产 `sky.wgsl` `hash_cell` 与十字形，readback 断言 `256` 个 macro 的低两位 `72/69/62/53`、活动 `184`、填充 `920/4096`、覆盖率 `22.4609375%`，以及 `hash & 3 != 0` 的理论 `3/4` macro 激活、理论 `15/64` 覆盖率、确定性、视差、时间、淡出、昼夜、遮挡和 terrain 覆盖；这些精确断言须使生产 hash、二维输入或掩码 mutation 失败，并确认既有太阳、月亮、星空与原深度顺序保持；运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/render -race -count=1 && go test ./internal/render -run TestRendererRenderDoesNotAllocate -count=1'`，不得启动或聚焦前台窗口。

## 4. 全量门禁

- [x] 4.1 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1 && go test ./... -race && go vet ./...'`，再运行 `gofmt -l .`、`openspec validate procedural-block-clouds --strict --no-interactive`、`openspec validate --all --strict --no-interactive` 与 `git diff --check`；所有命令必须成功且 `gofmt -l .` 无输出。

## 5. 视觉收尾、门禁与归档

- [x] 5.1 在 storage、oak、light 均合入后的最终 `main` 统一生成全部受天空影响的视觉 candidate，并逐张人工确认；保持既有视觉阈值，不接受任何非天空内容漂移。
- [ ] 5.2 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1 && go test ./... -race && go vet ./...'`、`gofmt -l .`、`openspec validate procedural-block-clouds --strict --no-interactive`、`openspec validate --all --strict --no-interactive` 与 `git diff --check`，再完成独立评审；性能数值只记录，报告完整性、真实 overflow 和数据丢失仍是门禁。
- [ ] 5.3 在 5.1 与 5.2 完成后，将 `celestial-sky-presentation` delta 同步至主规格并以 `openspec archive procedural-block-clouds --yes` 归档；再次运行 `openspec validate --all --strict --no-interactive`、`git diff --check` 与 `git status --short`，仅提交归档结果。
