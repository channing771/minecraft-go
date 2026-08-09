# 字形墨迹 UV 修正任务

## 1. 定位根因并写下回归测试

- [x] 1.1 排除异步收敛：把 `captureGlyphSettleFrames` 从 32 提到 240 重拍，确认画面逐像素一致，因此不是图集未收敛。
- [x] 1.2 量化各字符的墨迹尺寸与 `墨迹宽 / glyphCellSize` 压缩比，确认严重度与字宽成反比（`i` 0.091、`w` 0.562）。
- [x] 1.3 在 `internal/render/font_atlas_test.go` 写 `TestGlyphUVCoversInkNotWholeCell`，断言 UV 覆盖区与四边形在 1px 以内等尺寸；探针混合最窄（`i r t .`）与较宽（`w S 6`）字形。
- [x] 1.4 确认该测试在修复前失败，且失败信息报出每个字符的确切偏差。
  验证：`go test ./internal/render -run TestGlyphUVCoversInkNotWholeCell -count=1`

## 2. 修正 UV

- [x] 2.1 把 `Rasterize` 内的局部常量 `padding` 提升为包级 `glyphInkPadding`，使光栅化定位与 UV 定位共用同一个值。
- [x] 2.2 `FlushUploads` 分配 slot 时改为按墨迹子矩形计算 UV，并在注释中写明缩放比与丢字的因果。
- [x] 2.3 确认测试转绿，且 `internal/render` 既有测试全部通过。
  验证：`go test ./internal/render -run 'TestGlyphUVCoversInkNotWholeCell|TestGlyphAtlas' -count=1`
- [x] 2.4 变异验证：把 `U1` 改回整格，确认测试变红。恢复后确认 `git diff` 干净。

## 3. 调试面板行距

- [x] 3.1 取字体在 24pt 下的 Ascent/Descent，据此把 `panelRowHeight` 由 18 改为 28，并在注释中写明取值依据与 1080p 容纳约束。
  目标文件：`internal/render/debug_panel.go`

## 4. 面板视觉基线

- [x] 4.1 让抓帧路径无条件构造面板渲染器，benchmark 路径仍不构造。
  目标文件：`cmd/mcgo/main.go`
- [x] 4.2 在 `captureScenes` 末尾追加 `debug-panel` 场景，显式重置上一个场景留下的呈现状态后打开面板。
  目标文件：`cmd/mcgo/capture.go`
- [x] 4.3 给 `captureScene` 增加可选的 `PinVolatile` 钩子，在字形收敛帧之后、最后一帧之前执行；`debug-panel` 用它把帧时与权威 tick 钉成常量。
  依据：首次录制后复跑，该场景超阈值（329/230400 像素、最大通道差 76），差异点落在读数区——面板直接显示帧时与 tick，实测同机重复抓帧 tick 在 412..416、帧时在 3.3..4.3ms 之间。`Apply` 跑在收敛帧之前，收敛帧会把这两个值重新推进，因此必须有独立钩子。
  目标文件：`cmd/mcgo/capture.go`
- [x] 4.4 用 `--update-golden` 重新录制受影响基线，逐张人眼确认。
  验证：`go run ./cmd/mcgo --capture <dir> --update-golden`
- [x] 4.5 `terrain-noon` 不含文本，其重录差异（7/230400 像素、通道差 1）属采样噪声且在阈值内，保留原基线不动。
- [x] 4.6 连续两次复跑确认 `debug-panel` 稳定：5/230400 与 4/230400 像素、最大通道差均为 1，远在阈值内。

## 5. 收尾门禁

- [x] 5.1 `go test ./... -race`
- [x] 5.2 `go vet ./...` 与 `gofmt -l .`
- [x] 5.3 `CGO_ENABLED=0 GOOS=linux go build ./cmd/mcgod`
- [x] 5.4 `openspec validate --all --strict --no-interactive`
- [x] 5.5 抓帧比对复跑一遍，确认五个场景全部通过新基线。
  验证：`go run ./cmd/mcgo --capture <dir>`
