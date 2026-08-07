# 视觉验证实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让渲染结果能离开 GPU，成为可比对、可人眼查看的 PNG 产物，补上仓库从未验证过任何像素的空白。

**Architecture:** 复用 `--benchmark` 已有的无头 offscreen 全帧渲染路径（`cmd/mcgo/app.go:941` 的 `target := a.colorView`），只补三处：`internal/gfx` 的纹理回读（含 `bytesPerRow` 256 对齐紧缩）、`cmd/mcgo` 的 `--capture` 抓帧出口、以及一个双阈值图像比对器。不新建渲染路径，因为任何"专供截图"的旁路都会让 golden 图与真实客户端所见发生分歧，而分歧正好使 golden 失去意义。

**Tech Stack:** Go 1.26、`github.com/oliverbestmann/webgpu` v1.34.2、标准库 `image/png`（不引入新依赖）。

## Global Constraints

- 设计文档为准：`docs/superpowers/specs/2026-08-07-visual-verification-design.md`。实现与设计不一致时，先更新变更产物，不得只改代码让规格失真。
- Go 命令一律经 `zsh -ic 'gvm use go1.26.0 >/dev/null && <cmd>'` 执行。模块拉取超时时用 `GOPROXY=https://goproxy.cn,direct`。
- 代码注释与 GoDoc 必须用中文；Go 标识符、wire magic、协议字段名保留英文。
- 所有 Go 代码必须过 `gofmt`；`gofmt -l .` 应无输出。
- 只有 `internal/gfx` 可以直接 import WebGPU 绑定。
- 自动测试不得启动或聚焦前台游戏窗口。本计划全部走无头设备，天然满足。
- 本计划不得修改 `docs/notes/perf-baseline.json` 与 `docs/notes/perf-baseline-m5.json`。
- 不得放宽既有正确性断言、资源上限或性能门禁。
- `git add` 必须精确点名文件，**绝不 `git add .`**：工作区常驻 `midscene_run/` 下的修改日志，不得入库。
- 不得改动 benchmark 的 `scenario_version` 或其固定场景定义。
- 视觉图分辨率 **640×360**，golden 存放于 `cmd/mcgo/testdata/golden/`。
- 每个任务末尾提交前执行 `git diff --check`，应无输出。

---

### Task 1: gfx 纹理回读

**Files:**
- Modify: `internal/gfx/gfx.go:292-299`（`TextureUsage` 枚举）、`internal/gfx/gfx.go:315-324`（`Texture` 接口）
- Modify: `internal/gfx/wgpu.go:81-92`（`toTextureUsage`）、`internal/gfx/wgpu.go:970` 之后（`wgpuTexture` 方法）
- Modify: `cmd/mcgo/app_test.go:539-542`（`integrationTexture` 测试替身）
- Modify: `internal/render/font_atlas_test.go:530-534`（`glyphTestTexture` 测试替身）
- Test: `internal/gfx/texture_readback_test.go`（新建）

**Interfaces:**
- Consumes: 无（首个任务）
- Produces: `gfx.TextureUsageCopySrc`（`TextureUsage` 常量）；`gfx.Texture` 接口新方法 `ReadLayer(layer, mip uint32) []byte`，返回行距紧凑（`宽 × 每像素字节`）的像素副本。Task 3 用它读取 offscreen 颜色纹理。

- [ ] **Step 1: 写失败测试**

新建 `internal/gfx/texture_readback_test.go`。**注意首行的 `//go:build darwin` 不能省**，该包的其余测试文件都有，缺了会在非 darwin 平台编译失败。

```go
//go:build darwin

package gfx

import (
	"bytes"
	"testing"
)

// TestTextureReadLayerRoundTripsUnalignedWidth 用非 256 对齐的行距验证紧缩逻辑。
// 100 × 4 = 400 字节/行，会被 WebGPU 填充到 512；若实现直接返回填充后的缓冲，
// 从第二行起每行都会整体错位 112 字节。
func TestTextureReadLayerRoundTripsUnalignedWidth(t *testing.T) {
	dev, err := NewHeadlessDevice()
	if err != nil {
		t.Skipf("本机无可用 GPU 适配器: %v", err)
	}
	defer dev.Release()

	const width, height = 100, 100
	tex := dev.CreateTexture(TextureDesc{
		Label:     "readback-unaligned",
		Width:     width,
		Height:    height,
		Format:    FormatRGBA8Unorm,
		Dimension: TextureDimension2D,
		Usage:     TextureUsageCopyDst | TextureUsageCopySrc,
	})
	defer tex.Release()

	want := make([]byte, width*height*4)
	for i := range want {
		// 251 是质数，与 4 和 100 均互质，保证行内与行间都不出现周期性重复，
		// 错位一行或错位若干字节都会被 bytes.Equal 抓到。
		want[i] = byte(i % 251)
	}
	tex.WriteLayer(0, 0, want)

	got := tex.ReadLayer(0, 0)
	if len(got) != len(want) {
		t.Fatalf("回读长度 = %d，想要 %d", len(got), len(want))
	}
	if bytes.Equal(got, want) {
		return
	}
	for row := 0; row < height; row++ {
		lo, hi := row*width*4, (row+1)*width*4
		if !bytes.Equal(got[lo:hi], want[lo:hi]) {
			t.Fatalf("第 %d 行不匹配：got[:8]=%v want[:8]=%v", row, got[lo:lo+8], want[lo:lo+8])
		}
	}
	t.Fatal("回读内容与写入不一致，但逐行比对未定位到差异行")
}

// TestTextureReadLayerRoundTripsAlignedWidth 覆盖行距恰好等于对齐边界的情形，
// 此时无填充，紧缩逻辑必须是恒等变换而不是少拷一行。
func TestTextureReadLayerRoundTripsAlignedWidth(t *testing.T) {
	dev, err := NewHeadlessDevice()
	if err != nil {
		t.Skipf("本机无可用 GPU 适配器: %v", err)
	}
	defer dev.Release()

	const width, height = 64, 8 // 64 × 4 = 256，恰好对齐
	tex := dev.CreateTexture(TextureDesc{
		Label:     "readback-aligned",
		Width:     width,
		Height:    height,
		Format:    FormatRGBA8Unorm,
		Dimension: TextureDimension2D,
		Usage:     TextureUsageCopyDst | TextureUsageCopySrc,
	})
	defer tex.Release()

	want := make([]byte, width*height*4)
	for i := range want {
		want[i] = byte(i % 251)
	}
	tex.WriteLayer(0, 0, want)

	if got := tex.ReadLayer(0, 0); !bytes.Equal(got, want) {
		t.Fatalf("对齐宽度回读不一致：len(got)=%d len(want)=%d", len(got), len(want))
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/gfx -run TestTextureReadLayer -count=1'
```

预期：编译失败，`undefined: TextureUsageCopySrc` 与 `tex.ReadLayer undefined`。

**编译失败不是合格的红。** 在进入 Step 3 之前，先只加 `TextureUsageCopySrc` 常量与一个 `ReadLayer` 空壳（`return nil`），重跑并确认测试以"回读长度 = 0，想要 40000"失败，这才是真正的红。

- [ ] **Step 3: 加 TextureUsageCopySrc 与接口方法**

`internal/gfx/gfx.go`，`TextureUsage` 枚举末尾追加：

```go
const (
	TextureUsageBinding TextureUsage = 1 << iota
	TextureUsageRenderTarget
	TextureUsageCopyDst
	TextureUsageStorage
	// TextureUsageCopySrc 允许纹理作为 CopyTextureToBuffer 的源，供 ReadLayer 回读。
	// 必须追加在已有枚举之后，以保持既有位值稳定。
	TextureUsageCopySrc
)
```

`Texture` 接口末尾（`Release()` 之前）追加：

```go
	// ReadLayer 回读指定 layer/mip 的全部像素。
	// 返回切片的行距是紧凑的（宽 × 每像素字节），与 WriteLayer 的入参布局一致，
	// 调用方不需要知道底层的对齐填充。
	// 纹理必须带 TextureUsageCopySrc，否则底层校验会失败。
	ReadLayer(layer, mip uint32) []byte
```

`internal/gfx/wgpu.go` 的 `toTextureUsage` 映射表追加一行：

```go
		TextureUsageCopySrc:      wgpu.TextureUsageCopySrc,
```

- [ ] **Step 4: 实现 ReadLayer**

`internal/gfx/wgpu.go`，加在 `mipSize` 之后、`func (t *wgpuTexture) Release()` 之前：

```go
// copyBytesPerRowAlignment 是 WebGPU 对 CopyTextureToBuffer 行距的对齐要求。
const copyBytesPerRowAlignment = 256

// ReadLayer 按 WebGPU 的规矩绕一次 staging buffer，并把填充后的行距紧缩回
// 宽 × 每像素字节。CopyTextureToBuffer 要求 BytesPerRow 按 256 对齐，因此除非
// 行距恰好落在边界上，否则底层缓冲每行都带尾部填充，必须逐行拷出。
func (t *wgpuTexture) ReadLayer(layer, mip uint32) []byte {
	if layer >= t.layers {
		panic(fmt.Errorf("gfx: layer %d 超出纹理层数 %d", layer, t.layers))
	}
	if mip >= t.mipLevels {
		panic(fmt.Errorf("gfx: mip %d 超出纹理 mip 数 %d", mip, t.mipLevels))
	}
	bytesPerPixel := t.format.ByteSize()
	if bytesPerPixel == 0 {
		panic(fmt.Errorf("gfx: 格式 %v 的每像素字节数未知，无法回读", t.format))
	}
	width := mipSize(t.width, mip)
	height := mipSize(t.height, mip)
	tightRow := width * bytesPerPixel
	paddedRow := (tightRow + copyBytesPerRowAlignment - 1) /
		copyBytesPerRowAlignment * copyBytesPerRowAlignment
	size := uint64(paddedRow) * uint64(height)

	dev := t.device
	staging := must(dev.device.TryCreateBuffer(&wgpu.BufferDescriptor{
		Label: "gfx texture readback staging",
		Size:  size,
		Usage: wgpu.BufferUsageMapRead | wgpu.BufferUsageCopyDst,
	}))
	defer staging.Release()

	encoder := must(dev.device.TryCreateCommandEncoder(&wgpu.CommandEncoderDescriptor{
		Label: "gfx texture readback encoder",
	}))
	defer encoder.Release()
	check(encoder.TryCopyTextureToBuffer(
		&wgpu.TexelCopyTextureInfo{
			Texture:  t.texture,
			MipLevel: mip,
			Origin:   wgpu.Origin3D{Z: layer},
			Aspect:   wgpu.TextureAspectAll,
		},
		&wgpu.TexelCopyBufferInfo{
			Buffer: staging,
			Layout: wgpu.TexelCopyBufferLayout{
				BytesPerRow:  paddedRow,
				RowsPerImage: height,
			},
		},
		&wgpu.Extent3D{Width: width, Height: height, DepthOrArrayLayers: 1},
	))

	cmd := must(encoder.TryFinish(nil))
	defer cmd.Release()
	dev.queue.Submit(cmd)

	// MapAsync 的回调由 Poll 驱动；Poll(true) 会一直转到队列清空。
	status := wgpu.MapAsyncStatusError
	check(staging.TryMapAsync(wgpu.MapModeRead, 0, size, func(s wgpu.MapAsyncStatus) {
		status = s
	}))
	dev.device.Poll(true, nil)
	if status != wgpu.MapAsyncStatusSuccess {
		panic(fmt.Errorf("gfx: 映射纹理 staging buffer 失败: %v", status))
	}

	// GetMappedRange 返回的是映射内存上的视图，Unmap 之后就失效，必须拷出来。
	mapped := staging.GetMappedRange(0, uint(size))
	out := make([]byte, uint64(tightRow)*uint64(height))
	for row := uint32(0); row < height; row++ {
		src := uint64(row) * uint64(paddedRow)
		dst := uint64(row) * uint64(tightRow)
		copy(out[dst:dst+uint64(tightRow)], mapped[src:src+uint64(tightRow)])
	}
	check(staging.TryUnmap())
	return out
}
```

- [ ] **Step 5: 补齐两个测试替身**

新接口方法会让所有 `gfx.Texture` 的实现失败编译。仓库里只有两个测试替身：

`cmd/mcgo/app_test.go`，在 `integrationTexture` 的 `WriteLayer` 附近加：

```go
func (*integrationTexture) ReadLayer(uint32, uint32) []byte { return nil }
```

`internal/render/font_atlas_test.go`，在 `glyphTestTexture` 的 `WriteRegion` 之后加：

```go
func (*glyphTestTexture) ReadLayer(uint32, uint32) []byte { return nil }
```

两处都返回 nil：这两个替身都不参与回读路径，返回 nil 比伪造像素更诚实——若将来有人误用，nil 会立刻暴露，而伪造的零像素会静默通过。

- [ ] **Step 6: 运行测试确认通过**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/gfx ./internal/render ./cmd/mcgo -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go vet ./internal/gfx ./internal/render ./cmd/mcgo'
gofmt -l internal/gfx internal/render cmd/mcgo
```

预期：全部 PASS，`gofmt -l` 与 `go vet` 无输出。

- [ ] **Step 7: 变异验证**

把 `paddedRow` 的计算改成 `paddedRow := tightRow`（去掉对齐），重跑 `TestTextureReadLayerRoundTripsUnalignedWidth`，**必须变红**。恢复后 `git diff` 必须干净。

若改坏后测试仍绿，说明测试没有真正钉住对齐逻辑，先修测试再继续。

- [ ] **Step 8: 提交**

```bash
git add internal/gfx/gfx.go internal/gfx/wgpu.go internal/gfx/texture_readback_test.go \
        cmd/mcgo/app_test.go internal/render/font_atlas_test.go
git diff --check
git commit -m "feat: 增加 gfx 纹理回读"
```

---

### Task 2: --capture 标志与互斥校验

**Files:**
- Modify: `cmd/mcgo/main.go:47-107`（`parseMainOptions`）、`cmd/mcgo/main.go` 的 `mainOptions` 与 `applicationOptions` 定义
- Modify: `cmd/mcgo/app.go:31` 附近（`applicationOptions` 结构体）
- Test: `cmd/mcgo/main_test.go`

**Interfaces:**
- Consumes: 无
- Produces: `applicationOptions.CaptureDir string`（非空表示抓帧模式）；`mainOptions.CaptureDir string`。Task 3 据此选择无头分支并决定输出目录。

- [ ] **Step 1: 写失败测试**

追加到 `cmd/mcgo/main_test.go`。先读该文件已有的表驱动写法并保持一致的断言风格。

```go
func TestParseMainOptionsCaptureDir(t *testing.T) {
	opts, err := parseMainOptions([]string{"--capture", "/tmp/shots"})
	if err != nil {
		t.Fatalf("解析 --capture 失败: %v", err)
	}
	if opts.CaptureDir != "/tmp/shots" {
		t.Fatalf("CaptureDir = %q，想要 %q", opts.CaptureDir, "/tmp/shots")
	}
	if opts.Application.CaptureDir != "/tmp/shots" {
		t.Fatalf("Application.CaptureDir = %q，想要 %q", opts.Application.CaptureDir, "/tmp/shots")
	}
}

func TestParseMainOptionsCaptureRejectsConflicts(t *testing.T) {
	// --capture 与 --benchmark 都会独占无头渲染路径并各自驱动场景，
	// 同时开启的语义无法定义，必须直接拒绝而不是让某一方静默胜出。
	tests := []struct {
		name string
		args []string
	}{
		{"与 benchmark 互斥", []string{"--capture", "/tmp/shots", "--benchmark", "--perf-output", "/tmp/p.json"}},
		{"与 connect 互斥", []string{"--capture", "/tmp/shots", "--connect", "127.0.0.1:25565"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseMainOptions(tc.args); err == nil {
				t.Fatal("想要报错，实际通过")
			}
		})
	}
}

func TestParseMainOptionsWithoutCaptureLeavesDirEmpty(t *testing.T) {
	opts, err := parseMainOptions(nil)
	if err != nil {
		t.Fatalf("解析空参数失败: %v", err)
	}
	if opts.CaptureDir != "" {
		t.Fatalf("CaptureDir = %q，想要空", opts.CaptureDir)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo -run TestParseMainOptionsCapture -count=1'
```

预期：编译失败，`opts.CaptureDir undefined`。先加字段（不加解析逻辑）再重跑，确认以 `CaptureDir = ""，想要 "/tmp/shots"` 失败。

- [ ] **Step 3: 加字段与解析**

`cmd/mcgo/app.go` 的 `applicationOptions` 结构体加：

```go
	// CaptureDir 非空时进入视觉抓帧模式：走无头设备，按固定场景抓帧写 PNG。
	CaptureDir string
```

`cmd/mcgo/main.go` 的 `mainOptions` 结构体加同名字段。`parseMainOptions` 中，在 `connect` 之后加标志：

```go
	capture := flags.String("capture", "", "视觉抓帧输出目录；非空时走无头抓帧模式")
```

在 `flags.Parse` 之后的校验区加：

```go
	if *capture != "" && *benchmark {
		return mainOptions{}, errors.New("--capture 不能与 --benchmark 同时使用")
	}
	if *capture != "" && *connect != "" {
		return mainOptions{}, errors.New("--capture 不能与 --connect 同时使用")
	}
```

返回值的 `Application` 里加 `CaptureDir: *capture`，`mainOptions` 里加 `CaptureDir: *capture`。

- [ ] **Step 4: 运行测试确认通过**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo -run TestParseMainOptions -race -count=1'
gofmt -l cmd/mcgo
```

预期：PASS，`gofmt -l` 无输出。

- [ ] **Step 5: 提交**

```bash
git add cmd/mcgo/main.go cmd/mcgo/app.go cmd/mcgo/main_test.go
git diff --check
git commit -m "feat: 增加 --capture 标志与互斥校验"
```

---

### Task 3: 抓帧驱动、PNG 输出与 terrain-noon 场景

**Files:**
- Create: `cmd/mcgo/capture.go`
- Create: `cmd/mcgo/capture_test.go`
- Modify: `cmd/mcgo/app.go:379-393`（无头分支的选择条件、分辨率与纹理 usage）
- Modify: `cmd/mcgo/main.go` 的 `runDependencies` 与 `runWithDependencies`（接入 `runCapture`）

**Interfaces:**
- Consumes: `gfx.Texture.ReadLayer`（Task 1）、`applicationOptions.CaptureDir`（Task 2）
- Produces:
  - `captureWidth = 640`、`captureHeight = 360`（常量）
  - `type captureScene struct { Name string; WarmupFrames int; Apply func(*application) error }`
  - `var captureScenes []captureScene`（表驱动，Task 5 往里加行）
  - `func runCapture(app *application, dir string) error`
  - `func bgraToNRGBA(pixels []byte, width, height int) *image.NRGBA`

- [ ] **Step 1: 写失败测试**

新建 `cmd/mcgo/capture_test.go`。这一步只测纯函数 `bgraToNRGBA`——它不需要 GPU，是整条链上最容易写错且最难察觉的一环（通道顺序错了图仍然"看着像那么回事"）。

```go
package main

import (
	"image"
	"testing"
)

// TestBGRAToNRGBASwapsChannels 钉住通道顺序。
// offscreen 纹理是 BGRA8UnormSrgb，PNG 要的是 RGBA；写反了图像整体偏色，
// 但结构完整，肉眼扫一眼极易放过。
func TestBGRAToNRGBASwapsChannels(t *testing.T) {
	// 单像素：B=1, G=2, R=3, A=4
	got := bgraToNRGBA([]byte{1, 2, 3, 4}, 1, 1)
	want := []byte{3, 2, 1, 255} // R=3, G=2, B=1, A 强制 255
	if len(got.Pix) != len(want) {
		t.Fatalf("Pix 长度 = %d，想要 %d", len(got.Pix), len(want))
	}
	for i := range want {
		if got.Pix[i] != want[i] {
			t.Fatalf("Pix[%d] = %d，想要 %d（完整值 %v）", i, got.Pix[i], want[i], got.Pix)
		}
	}
}

// TestBGRAToNRGBAKeepsRowOrder 用两行两列确认没有行列错位。
func TestBGRAToNRGBAKeepsRowOrder(t *testing.T) {
	pixels := []byte{
		10, 0, 0, 0, 20, 0, 0, 0, // 第 0 行：B=10, B=20
		30, 0, 0, 0, 40, 0, 0, 0, // 第 1 行：B=30, B=40
	}
	img := bgraToNRGBA(pixels, 2, 2)
	if img.Bounds() != image.Rect(0, 0, 2, 2) {
		t.Fatalf("bounds = %v，想要 2x2", img.Bounds())
	}
	for _, tc := range []struct{ x, y int; wantB byte }{
		{0, 0, 10}, {1, 0, 20}, {0, 1, 30}, {1, 1, 40},
	} {
		offset := img.PixOffset(tc.x, tc.y)
		if got := img.Pix[offset+2]; got != tc.wantB {
			t.Fatalf("(%d,%d) 的 B = %d，想要 %d", tc.x, tc.y, got, tc.wantB)
		}
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo -run TestBGRAToNRGBA -count=1'
```

预期：编译失败 `undefined: bgraToNRGBA`。先加空壳 `func bgraToNRGBA(pixels []byte, width, height int) *image.NRGBA { return image.NewNRGBA(image.Rect(0, 0, width, height)) }`，重跑确认以 `Pix[0] = 0，想要 3` 失败。

- [ ] **Step 3: 实现 bgraToNRGBA 与抓帧驱动**

新建 `cmd/mcgo/capture.go`：

```go
package main

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"

	"minecraft-go/internal/physics"
)

// captureWidth/captureHeight 是视觉场景的固定分辨率。
// 刻意远小于 benchmark 的 2560×1440：golden 图要长期入库并反复更新，
// 全尺寸会让仓库历史迅速膨胀，而 360p 足以暴露本设施要抓的问题类别。
const (
	captureWidth  = 640
	captureHeight = 360
)

// captureDrainMax 是抓帧期间每帧处理的服务端消息上限，取值与 benchmark 一致。
const captureDrainMax = benchmarkMessageDrainMax

// captureScene 是一个视觉场景。三要素缺一不可：确定性的世界状态由固定种子与
// waitUntilLoaded 保证，固定的相机位姿与其余呈现状态由 Apply 设置，
// 抓帧时机由 WarmupFrames 固定——三者都必须是常量，任何一项随环境变化，
// 产出的图就不可比对。
type captureScene struct {
	Name string
	// WarmupFrames 是 Apply 之前空跑的帧数，用来让上传预算与网格化收敛。
	WarmupFrames int
	// Apply 在最后一帧渲染前执行，是场景对呈现状态的全部干预。
	// 它跑在 drainServerMessages 之后，因此设置的值不会被当帧的服务端消息覆盖。
	Apply func(*application) error
}

// captureScenes 是表驱动的场景清单，新增场景即新增一行。
var captureScenes = []captureScene{
	{
		Name:         "terrain-noon",
		WarmupFrames: 8,
		Apply: func(app *application) error {
			// 6000 tick 是正午，日光与太阳高度都取到最大值，
			// 是昼夜管线上最容易看出偏差的相位。
			app.worldTimeTicks = 6000
			return nil
		},
	},
}

// runCapture 依次跑完全部视觉场景，把每张图写成 <dir>/<name>.png。
func runCapture(app *application, dir string) error {
	if width, height := app.framebufferSize(); width != captureWidth || height != captureHeight {
		return fmt.Errorf("capture framebuffer=%dx%d，要求精确 %dx%d",
			width, height, captureWidth, captureHeight)
	}
	if app.color == nil {
		return fmt.Errorf("capture 需要无头 offscreen 颜色纹理，当前为 nil")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建抓帧输出目录 %s: %w", dir, err)
	}
	// 复用 benchmark 的加载等待：同样的视距、同样的收敛判据。
	// 抓帧不另设视距，否则图里所见与真实客户端所见就会分歧，golden 随之失去意义。
	if _, err := waitUntilLoaded(app, 5*time.Minute); err != nil {
		return fmt.Errorf("固定场景加载: %w", err)
	}
	for _, scene := range captureScenes {
		if err := captureOne(app, dir, scene); err != nil {
			return fmt.Errorf("场景 %s: %w", scene.Name, err)
		}
		fmt.Printf("已抓取场景 %s\n", scene.Name)
	}
	return nil
}

func captureOne(app *application, dir string, scene captureScene) error {
	for i := 0; i < scene.WarmupFrames; i++ {
		if _, err := app.frame(captureDrainMax, captureDrainMax, physics.FixedDelta); err != nil {
			return fmt.Errorf("预热第 %d 帧: %w", i, err)
		}
	}
	// 最后一帧手工拆开 frame()：先收消息，再让场景覆盖呈现状态，最后渲染。
	// 顺序不能变——Apply 若跑在 drain 之前，设置的值会被当帧的服务端消息覆盖。
	app.drainServerMessages(captureDrainMax)
	if err := scene.Apply(app); err != nil {
		return fmt.Errorf("应用场景状态: %w", err)
	}
	if _, err := app.renderFrame(captureDrainMax); err != nil {
		return fmt.Errorf("渲染抓帧: %w", err)
	}
	pixels := app.color.ReadLayer(0, 0)
	img := bgraToNRGBA(pixels, captureWidth, captureHeight)
	return writePNG(filepath.Join(dir, scene.Name+".png"), img)
}

// bgraToNRGBA 把回读到的 BGRA8 像素转成 PNG 需要的 NRGBA。
// 纹理格式是 sRGB，字节本身已是 sRGB 编码，与 PNG 的约定一致，只需交换 B/R；
// alpha 恒定写 255——渲染目标的 alpha 通道从未被任何管线约定过，
// 直接透传会让 golden 图随无关的管线细节漂移。
func bgraToNRGBA(pixels []byte, width, height int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for i := 0; i < width*height; i++ {
		src, dst := i*4, i*4
		img.Pix[dst+0] = pixels[src+2]
		img.Pix[dst+1] = pixels[src+1]
		img.Pix[dst+2] = pixels[src+0]
		img.Pix[dst+3] = 255
	}
	return img
}

func writePNG(path string, img *image.NRGBA) (returnErr error) {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("创建 %s: %w", path, err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil && returnErr == nil {
			returnErr = fmt.Errorf("关闭 %s: %w", path, closeErr)
		}
	}()
	if err := png.Encode(file, img); err != nil {
		return fmt.Errorf("编码 %s: %w", path, err)
	}
	return nil
}
```

注意 `runCapture` 用到了 `time.Minute`，import 里补 `"time"`。

- [ ] **Step 4: 让无头分支覆盖抓帧模式**

`cmd/mcgo/app.go:379-393`，把 benchmark 专属的无头分支改成同时服务抓帧：

```go
	width, height := 2560, 1440
	headless := options.Benchmark || options.CaptureDir != ""
	if options.CaptureDir != "" {
		width, height = captureWidth, captureHeight
	}
	if headless {
		dev, err = dependencies.newHeadlessDevice()
		colorFormat = gfx.FormatBGRA8UnormSrgb
		if err == nil {
			color = dev.CreateTexture(gfx.TextureDesc{
				Label:     "headless offscreen color",
				Width:     uint32(width),
				Height:    uint32(height),
				Format:    colorFormat,
				Dimension: gfx.TextureDimension2D,
				// CopySrc 是抓帧回读的前提；benchmark 不回读，但共用一张纹理，
				// 多带一个 usage 位没有代价。
				Usage: gfx.TextureUsageRenderTarget | gfx.TextureUsageCopySrc,
			})
			colorView = color.View(gfx.TextureViewDesc{Dimension: gfx.TextureViewDimension2D})
		}
	} else {
		// ……既有窗口分支保持不变
	}
```

同一函数里所有原本判断 `options.Benchmark` 来决定"是否有窗口"的位置（`app.go:263`、`app.go:508`）都要改判 `headless`。逐个查看这些点：判断的语义若是"无窗口"就改，若是"跑性能场景"就保留。

- [ ] **Step 5: 接进 run 流程**

`cmd/mcgo/main.go` 的 `runDependencies` 加一个字段：

```go
	runCapture func(*application, string) error
```

默认依赖里填 `runCapture: runCapture`。`runWithDependencies` 中，在选择 benchmark 还是交互式之前先判抓帧：

```go
	if opts.CaptureDir != "" {
		return deps.runCapture(app, opts.CaptureDir)
	}
```

- [ ] **Step 6: 运行测试确认通过**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go vet ./cmd/mcgo'
gofmt -l cmd/mcgo
```

预期：全部 PASS，无输出。

- [ ] **Step 7: 端到端跑一次并看图**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --capture /tmp/mcgo-shots'
ls -l /tmp/mcgo-shots/
```

预期：`terrain-noon.png` 存在，尺寸 640×360，体积在几十 KB 量级（接近 0 说明是纯色，接近 1 MB 说明可能是噪声）。

**必须实际打开这张图看。** 数值检查过不了"渲染出来的是一片天空、地形根本没画上"这类问题。这一步是本任务唯一能证明自己有效的证据。

- [ ] **Step 8: 提交**

```bash
git add cmd/mcgo/capture.go cmd/mcgo/capture_test.go cmd/mcgo/app.go cmd/mcgo/main.go
git diff --check
git commit -m "feat: 增加视觉抓帧出口与 terrain-noon 场景"
```

---

### Task 4: 双阈值比对器

**Files:**
- Create: `cmd/mcgo/visual_compare.go`
- Create: `cmd/mcgo/visual_compare_test.go`

**Interfaces:**
- Consumes: 无（纯图像运算，不依赖 Task 1-3）
- Produces:
  - `type diffThreshold struct { MaxChannelDelta int; MaxDiffPixelRatio float64 }`
  - `type imageDiff struct { MaxChannelDelta int; DiffPixels int; TotalPixels int; DiffPixelRatio float64 }`
  - `func compareImages(got, want *image.NRGBA) (imageDiff, *image.NRGBA, error)` — 第二个返回值是差异可视化图
  - `func (d imageDiff) withinThreshold(t diffThreshold) bool`

- [ ] **Step 1: 写失败测试**

新建 `cmd/mcgo/visual_compare_test.go`：

```go
package main

import (
	"image"
	"testing"
)

func solidNRGBA(width, height int, r, g, b byte) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for i := 0; i < width*height; i++ {
		img.Pix[i*4+0] = r
		img.Pix[i*4+1] = g
		img.Pix[i*4+2] = b
		img.Pix[i*4+3] = 255
	}
	return img
}

func TestCompareImagesIdentical(t *testing.T) {
	a := solidNRGBA(4, 4, 10, 20, 30)
	b := solidNRGBA(4, 4, 10, 20, 30)
	diff, _, err := compareImages(a, b)
	if err != nil {
		t.Fatalf("比对失败: %v", err)
	}
	if diff.MaxChannelDelta != 0 || diff.DiffPixels != 0 {
		t.Fatalf("全等图的差异 = %+v，想要全零", diff)
	}
}

// TestCompareImagesSinglePixelSpike 覆盖"局部高差值"——接缝漏光的形态。
// 占比极小，只有 MaxChannelDelta 能抓到它。
func TestCompareImagesSinglePixelSpike(t *testing.T) {
	a := solidNRGBA(10, 10, 0, 0, 0)
	b := solidNRGBA(10, 10, 0, 0, 0)
	b.Pix[b.PixOffset(5, 5)+1] = 200 // 单个像素的 G 通道拉高
	diff, _, err := compareImages(a, b)
	if err != nil {
		t.Fatalf("比对失败: %v", err)
	}
	if diff.MaxChannelDelta != 200 {
		t.Fatalf("MaxChannelDelta = %d，想要 200", diff.MaxChannelDelta)
	}
	if diff.DiffPixels != 1 {
		t.Fatalf("DiffPixels = %d，想要 1", diff.DiffPixels)
	}
	if diff.TotalPixels != 100 {
		t.Fatalf("TotalPixels = %d，想要 100", diff.TotalPixels)
	}
}

// TestCompareImagesWideFaintShift 覆盖"大面积微差"——LSB 噪声的形态。
// 每个像素只差 1，必须能被阈值放过，否则 CI 上会变成第二个假失败源。
func TestCompareImagesWideFaintShift(t *testing.T) {
	a := solidNRGBA(10, 10, 100, 100, 100)
	b := solidNRGBA(10, 10, 101, 101, 101)
	diff, _, err := compareImages(a, b)
	if err != nil {
		t.Fatalf("比对失败: %v", err)
	}
	if diff.MaxChannelDelta != 1 {
		t.Fatalf("MaxChannelDelta = %d，想要 1", diff.MaxChannelDelta)
	}
	if !diff.withinThreshold(diffThreshold{MaxChannelDelta: 2, MaxDiffPixelRatio: 0.01}) {
		t.Fatalf("每通道差 1 应当在阈值内，实际 %+v", diff)
	}
}

func TestCompareImagesRejectsSizeMismatch(t *testing.T) {
	// 尺寸不匹配直接失败，不做缩放后比对——缩放会引入插值，
	// 把"分辨率配错了"这个真问题伪装成"有一点点色差"。
	if _, _, err := compareImages(solidNRGBA(4, 4, 0, 0, 0), solidNRGBA(8, 8, 0, 0, 0)); err == nil {
		t.Fatal("尺寸不匹配想要报错，实际通过")
	}
}

func TestDiffPixelRatioExceedsThreshold(t *testing.T) {
	a := solidNRGBA(10, 10, 0, 0, 0)
	b := solidNRGBA(10, 10, 50, 50, 50)
	diff, _, err := compareImages(a, b)
	if err != nil {
		t.Fatalf("比对失败: %v", err)
	}
	if diff.withinThreshold(diffThreshold{MaxChannelDelta: 2, MaxDiffPixelRatio: 0.01}) {
		t.Fatalf("整图差 50 应当超阈值，实际 %+v", diff)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo -run "TestCompareImages|TestDiffPixelRatio" -count=1'
```

预期：编译失败 `undefined: compareImages`。先加返回零值的空壳，重跑确认 `MaxChannelDelta = 0，想要 200` 这类断言失败。

- [ ] **Step 3: 实现比对器**

新建 `cmd/mcgo/visual_compare.go`：

```go
package main

import (
	"fmt"
	"image"
	"image/color"
)

// diffThreshold 是视觉比对的双阈值。
// 刻意不做逐字节比对：sRGB 编解码、光栅化 tie-break、驱动与 GPU 型号差异
// 都会造成个位数 LSB 漂移，逐字节 golden 在共享 CI runner 上必然变成假失败源，
// 而假失败的真实代价是训练所有人无视门禁。
type diffThreshold struct {
	// MaxChannelDelta 是单个像素任一通道被视为"超差"的差值下限（含）以下算通过。
	MaxChannelDelta int
	// MaxDiffPixelRatio 是超差像素占全图的比例上限。
	MaxDiffPixelRatio float64
}

// imageDiff 是一次比对的量化结果。
type imageDiff struct {
	MaxChannelDelta int
	DiffPixels      int
	TotalPixels     int
	DiffPixelRatio  float64
}

func (d imageDiff) withinThreshold(t diffThreshold) bool {
	return d.MaxChannelDelta <= t.MaxChannelDelta && d.DiffPixelRatio <= t.MaxDiffPixelRatio
}

func (d imageDiff) String() string {
	return fmt.Sprintf("最大通道差 %d，超差像素 %d/%d（%.4f%%）",
		d.MaxChannelDelta, d.DiffPixels, d.TotalPixels, d.DiffPixelRatio*100)
}

// compareImages 比对两张同尺寸图，返回量化差异与一张差异可视化图。
// 可视化图把相同像素压暗、把超差像素画成红色，供人眼直接定位问题区域——
// 只报"差异 3.7% 超过阈值 1%"而不给图，等于让人盲修。
func compareImages(got, want *image.NRGBA) (imageDiff, *image.NRGBA, error) {
	if got.Bounds() != want.Bounds() {
		return imageDiff{}, nil, fmt.Errorf(
			"图像尺寸不匹配：实拍 %v，基线 %v", got.Bounds(), want.Bounds())
	}
	bounds := got.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	vis := image.NewNRGBA(bounds)
	result := imageDiff{TotalPixels: width * height}
	// 逐像素扫描，阈值判定留给调用方：比对器只负责测量，不负责裁决。
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			gi, wi := got.PixOffset(x, y), want.PixOffset(x, y)
			maxDelta := 0
			// 只比 RGB：alpha 在抓帧时已被恒定写成 255，比它没有信息量。
			for c := 0; c < 3; c++ {
				delta := int(got.Pix[gi+c]) - int(want.Pix[wi+c])
				if delta < 0 {
					delta = -delta
				}
				if delta > maxDelta {
					maxDelta = delta
				}
			}
			if maxDelta > result.MaxChannelDelta {
				result.MaxChannelDelta = maxDelta
			}
			vi := vis.PixOffset(x, y)
			if maxDelta > 0 {
				result.DiffPixels++
				vis.Pix[vi+0], vis.Pix[vi+1], vis.Pix[vi+2] = 255, 0, 0
			} else {
				dim := want.Pix[wi] / 4
				vis.Pix[vi+0], vis.Pix[vi+1], vis.Pix[vi+2] = dim, dim, dim
			}
			vis.Pix[vi+3] = 255
		}
	}
	if result.TotalPixels > 0 {
		result.DiffPixelRatio = float64(result.DiffPixels) / float64(result.TotalPixels)
	}
	return result, vis, nil
}
```

import 只需要 `fmt` 与 `image`——可视化图直接写 `Pix` 字节，不需要 `image/color`。

`DiffPixels` 统计的是 `maxDelta > 0` 的像素，而 `withinThreshold` 用 `MaxChannelDelta` 单独裁决单点尖峰。两个指标各管一类形态：大面积微差由比例抓，局部尖峰由最大差值抓。

- [ ] **Step 4: 运行测试确认通过**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo -race -count=1'
gofmt -l cmd/mcgo
```

预期：PASS，无输出。

- [ ] **Step 5: 变异验证**

把 `compareImages` 里的 `if delta < 0 { delta = -delta }` 删掉，重跑 `TestCompareImagesSinglePixelSpike` 与 `TestCompareImagesWideFaintShift`，**至少一条必须变红**（差值方向反转会让负差被记为 0）。恢复后 `git diff` 必须干净。

- [ ] **Step 6: 提交**

```bash
git add cmd/mcgo/visual_compare.go cmd/mcgo/visual_compare_test.go
git diff --check
git commit -m "feat: 增加双阈值图像比对器"
```

---

### Task 5: hud-hotbar-health 与 avatar-nametag 场景

**Files:**
- Modify: `cmd/mcgo/capture.go`（`captureScenes` 表）

**Interfaces:**
- Consumes: `captureScene`、`captureScenes`（Task 3）
- Produces: `captureScenes` 增加两行，名称固定为 `hud-hotbar-health` 与 `avatar-nametag`

这两个场景通过既有的客户端镜像入口注入呈现状态，**不新增任何注入 API**：`client.InventoryMirror.Apply`（`internal/client/inventory.go:18`）与 `client.RemotePlayers.Apply`（`internal/client/remote_players.go:48`）都是已导出的、且会走与真实服务端消息完全相同的校验路径。

- [ ] **Step 1: 加 hud-hotbar-health 场景**

在 `captureScenes` 里追加：

```go
	{
		Name:         "hud-hotbar-health",
		WarmupFrames: 8,
		Apply: func(app *application) error {
			app.worldTimeTicks = 6000
			// 走 InventoryMirror.Apply 而不是直接改内部字段：它会执行
			// Inventory.Valid() 校验，因此这份构造数据同时也是一条格式自检。
			inventory := core.Inventory{}
			inventory.Hotbar.Selected = 2
			inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 64}
			inventory.Hotbar.Slots[1] = core.ItemStack{Item: core.ItemStoneBrick, Count: 7}
			// 耐久 40/131 让磨损条画在偏左位置——满耐久和空耐久都是端点，
			// 端点画错了不容易看出来。
			inventory.Hotbar.Slots[2] = core.ItemStack{
				Item: core.ItemStonePickaxe, Count: 1, Durability: 40,
			}
			inventory.Hotbar.Slots[3] = core.ItemStack{
				Item: core.ItemIronPickaxe, Count: 1, Durability: 250,
			}
			inventory.Backpack[0] = core.ItemStack{Item: core.ItemCoal, Count: 12}
			return app.inventory.Apply(network.InventoryState{Inventory: inventory})
		},
	},
```

`cmd/mcgo/capture.go` 的 import 补 `"minecraft-go/internal/core"` 与 `"minecraft-go/internal/network"`。

**这个场景的已知缺口，必须在实现时用 `ponytail:` 注释标出，不要假装覆盖了：**生命值来自 `app.predictor.Health()`，而 `Predictor.ApplyPlayerState`（`internal/client/predictor.go:206`）带 `ServerTick` 单调校验与位置和解，从抓帧钩子里注入会牵动相机。因此本场景只能拿到服务端确认的满血（20），**半血、濒死等部分心形的渲染不在覆盖范围内**。在场景定义上方写：

```go
	// ponytail: 生命值只覆盖满血 20。部分心形需要注入 PlayerState，
	// 而 Predictor.ApplyPlayerState 带 ServerTick 单调校验与位置和解，
	// 从抓帧钩子注入会牵动相机。要覆盖需要先给 Predictor 加一个
	// 只改生命值的测试入口，或让抓帧场景能脚本化地驱动服务端造成伤害。
```

- [ ] **Step 2: 加 avatar-nametag 场景**

```go
	{
		Name:         "avatar-nametag",
		WarmupFrames: 8,
		Apply: func(app *application) error {
			app.worldTimeTicks = 6000
			if app.remotePlayers == nil {
				return fmt.Errorf("avatar-nametag 需要远端玩家追踪器，当前为 nil")
			}
			// 昵称刻意混用 ASCII 与非 ASCII：字形 atlas 的分支在这两类上不同，
			// 只用 ASCII 会漏掉整条宽字符路径。
			spawn := network.RemotePlayerSpawn{
				PlayerID:    core.PlayerID{1},
				DisplayName: "测试Player",
				ServerTick:  1,
				Position:    app.camera.Pos.Add(mgl32.Vec3{0, 0, -6}),
			}
			return app.remotePlayers.Apply(spawn)
		},
	},
```

import 补 `"github.com/go-gl/mathgl/mgl32"`。

`RemotePlayerSpawn` 的字段定义在 `internal/network/message.go:367`；`Dimension` 留零值即可（当前世界只有一个维度）。实现时先读 `applySpawn`（`internal/client/remote_players.go:115`）确认零值 `Dimension` 与 `Yaw`/`Pitch` 不会被拒，若会被拒则按其校验补上合法值，并在提交信息里说明补了什么。

- [ ] **Step 3: 跑一次并看三张图**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo -race -count=1'
gofmt -l cmd/mcgo
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --capture /tmp/mcgo-shots'
ls -l /tmp/mcgo-shots/
```

预期：三个 PNG 各 640×360。

**逐张打开确认**：`hud-hotbar-health` 能看到快捷栏、四个物品格、石镐上偏左的磨损条、右侧血条；`avatar-nametag` 能看到一个人物模型和它上方的「测试Player」名牌，中日文字形没有变成方框或缺字。

看到问题就在本任务内修完——这三张图接下来就要被冻成 golden，冻错了后面所有比对都在维护一个错误的基线。

- [ ] **Step 4: 提交**

```bash
git add cmd/mcgo/capture.go
git diff --check
git commit -m "feat: 增加 HUD 与远端玩家视觉场景"
```

---

### Task 6: golden 基线、阈值实测、make target 与文档

**Files:**
- Create: `cmd/mcgo/testdata/golden/terrain-noon.png`、`hud-hotbar-health.png`、`avatar-nametag.png`
- Modify: `cmd/mcgo/capture.go`（`--update-golden` 与比对流程）
- Modify: `cmd/mcgo/main.go`（`--update-golden` 标志）
- Modify: `Makefile`
- Modify: `README.md`
- Modify: `docs/superpowers/specs/2026-08-07-visual-verification-design.md`（回填实测阈值）

**Interfaces:**
- Consumes: 全部前序任务
- Produces: `make visual-check` 目标；`captureThresholds` 常量

- [ ] **Step 1: 加 --update-golden 与比对流程**

`cmd/mcgo/main.go` 加标志：

```go
	updateGolden := flags.Bool("update-golden", false, "把本次抓帧结果写入 golden 基线")
```

校验：`--update-golden` 只能与 `--capture` 同用，否则报错。字段透传到 `mainOptions.UpdateGolden` 与 `applicationOptions` 无关（它只影响 `runCapture`，从 `runWithDependencies` 直接传入）。

`runCapture` 签名改为 `runCapture(app *application, dir string, updateGolden bool) error`，`runDependencies.runCapture` 同步改签名。行为：

- `updateGolden` 为真：把抓到的图写进 `cmd/mcgo/testdata/golden/<name>.png`。
- 为假：读 golden，比对，超阈值时把实拍图写 `<dir>/<name>-actual.png`、差异图写 `<dir>/<name>-diff.png` 并返回错误。
- **golden 文件缺失且未传 `--update-golden` 时必须报错，绝不静默创建。** 否则第一次运行就会把错误结果冻成基线，此后永远比对不出问题。

- [ ] **Step 2: 实测漂移分布**

先用 `--update-golden` 生成一次基线，然后在同一台机器上重复抓帧 10 次并比对，记录每次的 `imageDiff`：

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --capture /tmp/mcgo-shots --update-golden'
for i in $(seq 1 10); do
  zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --capture /tmp/mcgo-shots' 2>&1 | grep -E '最大通道差|场景'
done
```

把 10 次的最大通道差与超差像素占比记下来。**阈值取实测最大值再留一档余量**（例如实测最大通道差恒为 0 就取 2，实测占比恒为 0 就取 0.001）。

设计文档明确要求："两个阈值的具体数值不在本设计中拍板，而是在交付第 4 步建立 golden 基线时，由同一台机器重复抓帧测得的实际漂移分布决定，并把测得的分布连同选定值一起写进变更产物。"

把实测分布与选定值回填到 `docs/superpowers/specs/2026-08-07-visual-verification-design.md` 的 §6。

在 `capture.go` 里定义：

```go
// captureThresholds 的数值来自同机重复抓帧 10 次的实测漂移分布，
// 具体测量结果见 docs/superpowers/specs/2026-08-07-visual-verification-design.md §6。
// 不要凭直觉调整——放宽阈值等于放弃门禁。
var captureThresholds = diffThreshold{
	MaxChannelDelta:   /* 实测值 */,
	MaxDiffPixelRatio: /* 实测值 */,
}
```

- [ ] **Step 3: 提交 golden 基线**

```bash
git add cmd/mcgo/testdata/golden/terrain-noon.png \
        cmd/mcgo/testdata/golden/hud-hotbar-health.png \
        cmd/mcgo/testdata/golden/avatar-nametag.png
git add cmd/mcgo/capture.go cmd/mcgo/main.go
du -h cmd/mcgo/testdata/golden/
```

确认三张图合计在百 KB 量级。若显著更大，先查是不是分辨率或格式配错了，不要直接入库。

- [ ] **Step 4: 加 make target 与文档**

`Makefile` 加：

```make
.PHONY: visual-check
visual-check:
	go run ./cmd/mcgo --capture $(or $(VISUAL_OUT),build/visual)

.PHONY: visual-update
visual-update:
	go run ./cmd/mcgo --capture $(or $(VISUAL_OUT),build/visual) --update-golden
```

先读 `Makefile` 现有目标的写法（是否已经包了 gvm、是否有统一的输出目录变量），保持一致。

`README.md` 在验证相关章节加一段，说明：视觉验证怎么跑、golden 在哪、什么时候该更新基线（渲染行为**有意**改变时），以及**不该**更新基线的情形（比对红了但没人知道为什么——那是要查的信号，不是要覆盖的噪声）。

- [ ] **Step 5: 反向验证**

这是本计划唯一能证明整套设施有效的证据，不可省略。

故意改坏一处渲染参数——例如把 `internal/render/hotbar.go` 里血条的某个顶点偏移改动几个像素，或把 `render.DayNightAt` 的正午日光值从 1 改成 0.9——然后：

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && make visual-check'
```

**对应场景必须变红**，且 `build/visual/<name>-diff.png` 能明确指出改动位置。

看完差异图后恢复改动，重跑确认全绿，并确认 `git diff` 干净。

若改坏后仍然全绿，说明阈值定得过松或场景没覆盖到该区域——**修阈值或补场景，不要放过**。

- [ ] **Step 6: 全量门禁**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race'
zsh -ic 'gvm use go1.26.0 >/dev/null && go vet ./...'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
gofmt -l .
git diff --check
```

预期：全部通过，`gofmt -l .` 与 `git diff --check` 无输出。

注意 `go test ./... -race` 里若出现 `macos-latest` 上已知的时序假失败（`TestScenarioV7EightSessionServerProbeIsRealAndBounded` 等），按已知抖动处理：**不要改阈值**，重跑确认失败会换测试即可。

- [ ] **Step 7: 提交**

```bash
git add Makefile README.md docs/superpowers/specs/2026-08-07-visual-verification-design.md cmd/mcgo/capture.go
git diff --check
git commit -m "feat: 冻结视觉 golden 基线并接入 make 门禁"
```

---

## 自审记录

**规格覆盖**：设计文档各节对应关系——§3 切口 → Task 3 Step 4；§4.1 gfx 回读 → Task 1；§4.2 抓帧出口 → Task 2 + Task 3；§4.3 比对器 → Task 4；§5 场景与分辨率 → Task 3 Step 3 + Task 5；§6 双阈值 → Task 4 + Task 6 Step 2；§7 门禁接入 → Task 6 Step 4；§8 错误处理（无 GPU skip / golden 缺失不静默创建 / 尺寸不匹配直接失败）→ Task 1 Step 1、Task 6 Step 1、Task 4 Step 1；§10 验证（非对齐宽度、比对器四情形、端到端、反向验证）→ Task 1 Step 1、Task 4 Step 1、Task 3 Step 7、Task 6 Step 5。

**未覆盖项**：§7 的"CI 上先以只记录不失败的形式运行"没有对应任务。这是有意的——仓库的 `.github/workflows/ci.yml` 只有一个 `test` job，接入非阻塞视觉门禁需要新增 job 并处理 runner 的 GPU 可用性，与本计划的六个任务耦合度低，且在拿到 Task 6 Step 2 的实测漂移数据之前无法确定接入形态。**留作本计划落地后的独立变更**，不在此处塞一个只能写占位符的任务。

**类型一致性**：`captureScene.Apply` 在 Task 3 定义为 `func(*application) error`，Task 5 的两处用法一致；`runCapture` 在 Task 3 为 `(app, dir)`、Task 6 Step 1 显式改为 `(app, dir, updateGolden)` 并要求同步改 `runDependencies` 字段；`diffThreshold` 与 `imageDiff` 的字段名在 Task 4 定义、Task 6 引用一致。
