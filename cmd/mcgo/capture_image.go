package main

import (
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
)

// captureGoldenDir 是 golden 基线目录，相对仓库根目录。
// mcgo 的其余相对路径默认值（例如 --world 的 worlds/default）同样假定从仓库根目录运行，
// 这里延续同一约定，不额外引入 runtime.Caller 之类的自定位逻辑。
const captureGoldenDir = "cmd/mcgo/testdata/golden"

// captureThresholds 的数值来自同机重复抓帧 14 次的实测漂移分布
// （前 12 次用于收集数据并定稿阈值，第 13、14 次在阈值定稿后确认仍全绿），
// 具体测量结果见 docs/superpowers/specs/2026-08-07-visual-verification-design.md §6。
// 不要凭直觉调整——放宽阈值等于放弃门禁。
var captureThresholds = diffThreshold{
	MaxChannelDelta:   2,
	MaxDiffPixelRatio: 0.0001,
}

// compareAgainstGolden 把 img 与 <goldenDir>/<name>.png 比对。
// 通过阈值时返回量化差异与 nil；超阈值或基线缺失时返回错误，
// 前者还会把实拍图与差异图写进 outDir 供人查看——只报比例数字等于让人盲修。
// goldenDir 作为参数而非直接用 captureGoldenDir 常量，是为了让单元测试
// 可以指向临时目录，不必读写仓库里的真实基线。
func compareAgainstGolden(
	goldenDir, outDir, name string, img *image.NRGBA, threshold diffThreshold,
) (imageDiff, error) {
	goldenPath := filepath.Join(goldenDir, name+".png")
	want, err := readPNG(goldenPath)
	if err != nil {
		return imageDiff{}, fmt.Errorf(
			"读取 golden 基线 %s 失败（若是首次建立基线，先加 --update-golden）: %w",
			goldenPath, err)
	}
	diff, vis, err := compareImages(img, want)
	if err != nil {
		return imageDiff{}, fmt.Errorf("比对场景 %s 与基线: %w", name, err)
	}
	if diff.withinThreshold(threshold) {
		return diff, nil
	}
	if err := writePNG(filepath.Join(outDir, name+"-actual.png"), img); err != nil {
		return diff, err
	}
	if err := writePNG(filepath.Join(outDir, name+"-diff.png"), vis); err != nil {
		return diff, err
	}
	return diff, fmt.Errorf("超出阈值：%s（实拍与差异图见 %s）", diff, outDir)
}

// readPNG 读取一张 PNG 并转成 NRGBA，用于加载 golden 基线。
func readPNG(path string) (*image.NRGBA, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	decoded, err := png.Decode(file)
	if err != nil {
		return nil, fmt.Errorf("解码 %s: %w", path, err)
	}
	if nrgba, ok := decoded.(*image.NRGBA); ok {
		return nrgba, nil
	}
	// writePNG 写出的图像应始终解码回 *image.NRGBA；这条分支只是防御性兜底，
	// 避免未来换编码器或手工替换 golden 文件时静默产出错位的比对结果。
	bounds := decoded.Bounds()
	converted := image.NewNRGBA(bounds)
	draw.Draw(converted, bounds, decoded, bounds.Min, draw.Src)
	return converted, nil
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
