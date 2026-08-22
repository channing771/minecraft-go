package assets

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"io/fs"
	"log/slog"
	"strings"
	"unicode/utf8"
)

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

func applyPack(registry *Registry, root fs.FS) error {
	manifestBytes, err := readPackFile(root, "pack.json", packManifestLimit)
	if err != nil {
		return fmt.Errorf("读取 pack.json: %w", err)
	}
	if !utf8.Valid(manifestBytes) {
		return fmt.Errorf("pack.json 不是有效 UTF-8")
	}

	var manifest packManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return fmt.Errorf("解析 pack.json: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(manifestBytes, &fields); err != nil {
		return fmt.Errorf("解析 pack.json 字段: %w", err)
	}
	if manifest.Format != packFormatVersion {
		return fmt.Errorf("pack.json format=%d，不支持，需要 %d", manifest.Format, packFormatVersion)
	}
	manifest.Name = strings.TrimSpace(manifest.Name)
	if manifest.Name == "" || len(manifest.Name) > 128 {
		return fmt.Errorf("pack.json name 必须是 trim 后非空且不超过 128 UTF-8 字节")
	}
	for field := range fields {
		if field != "format" && field != "name" {
			slog.Warn("忽略材质包 manifest 的未知字段", "pack", manifest.Name, "field", field)
		}
	}

	// 先完整解码到固定数组，再统一替换，避免后续坏文件留下部分生效的材质包。
	var replacements [layerCount][]byte
	for _, binding := range textureBindings {
		path := "textures/" + binding.name + ".png"
		file, err := root.Open(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("材质包 %q 的材质 %q: 打开 %s: %w", manifest.Name, binding.name, path, err)
		}
		data, err := readOpenedPackFile(file, path, packTextureLimit)
		if err != nil {
			return fmt.Errorf("材质包 %q 的材质 %q: %w", manifest.Name, binding.name, err)
		}

		config, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("材质包 %q 的材质 %q: 读取 PNG 配置: %w", manifest.Name, binding.name, err)
		}
		if config.Width != packTextureSize || config.Height != packTextureSize {
			return fmt.Errorf("材质包 %q 的材质 %q: PNG 尺寸为 %dx%d，需要 16x16",
				manifest.Name, binding.name, config.Width, config.Height)
		}
		decoded, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("材质包 %q 的材质 %q: 解码 PNG: %w", manifest.Name, binding.name, err)
		}
		rgba := image.NewNRGBA(image.Rect(0, 0, packTextureSize, packTextureSize))
		draw.Draw(rgba, rgba.Bounds(), decoded, decoded.Bounds().Min, draw.Src)
		replacements[binding.layer] = rgba.Pix
	}

	for layer, pixels := range replacements {
		if pixels != nil {
			registry.layers[layer] = pixels
		}
	}
	return nil
}

func readPackFile(root fs.FS, path string, limit int64) ([]byte, error) {
	file, err := root.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开 %s: %w", path, err)
	}
	return readOpenedPackFile(file, path, limit)
}

func readOpenedPackFile(file fs.File, path string, limit int64) ([]byte, error) {
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("检查 %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s 不是普通文件", path)
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, fmt.Errorf("读取 %s: %w", path, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s 超过 %d 字节", path, limit)
	}
	return data, nil
}
