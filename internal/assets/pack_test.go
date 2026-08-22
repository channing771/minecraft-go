package assets

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

const (
	testManifestLimit = 4 << 10
	testTextureLimit  = 64 << 10
)

func TestApplyPack(t *testing.T) {
	t.Run("有效 RGBA 只覆盖指定层并接受中间 alpha", func(t *testing.T) {
		for _, tt := range []struct {
			name  string
			layer uint16
		}{
			{name: "leaves", layer: LayerLeaves},
			{name: "glass", layer: LayerGlass},
		} {
			t.Run(tt.name, func(t *testing.T) {
				registry := NewRegistry()
				before := snapshotLayers(registry)
				pixels, encoded := solidPNG(t, 16, 16, color.NRGBA{R: 31, G: 79, B: 127, A: 128})
				root := fstest.MapFS{
					"pack.json":                    {Data: manifest(t, "半透明测试包")},
					"textures/" + tt.name + ".png": {Data: encoded},
				}

				if err := applyPack(registry, root); err != nil {
					t.Fatalf("applyPack() error = %v", err)
				}
				for layer := 0; layer < int(layerCount); layer++ {
					want := before[layer]
					if layer == int(tt.layer) {
						want = pixels
					}
					if got := registry.LayerRGBA(layer); !bytes.Equal(got, want) {
						t.Fatalf("layer %d 像素不符", layer)
					}
				}
				if got := registry.LayerRGBA(int(tt.layer))[3]; got != 128 {
					t.Fatalf("中间 alpha = %d，想要 128", got)
				}
			})
		}
	})

	t.Run("缺失已知材质保留原层", func(t *testing.T) {
		registry := NewRegistry()
		before := snapshotLayers(registry)
		if err := applyPack(registry, fstest.MapFS{
			"pack.json": {Data: manifest(t, "空覆盖包")},
		}); err != nil {
			t.Fatalf("applyPack() error = %v", err)
		}
		assertLayersEqual(t, registry, before)
	})

	t.Run("无效输入返回上下文错误", func(t *testing.T) {
		_, wrongSize := solidPNG(t, 15, 16, color.NRGBA{R: 1, G: 2, B: 3, A: 4})
		validManifest := manifest(t, "测试材质包")
		cases := []struct {
			name string
			root fstest.MapFS
			want []string
		}{
			{
				name: "缺失 manifest",
				root: fstest.MapFS{},
				want: []string{"pack.json"},
			},
			{
				name: "损坏 JSON",
				root: fstest.MapFS{"pack.json": {Data: []byte(`{"format":`)}},
				want: []string{"pack.json"},
			},
			{
				name: "不支持的格式",
				root: fstest.MapFS{"pack.json": {Data: []byte(`{"format":2,"name":"测试材质包"}`)}},
				want: []string{"pack.json", "format"},
			},
			{
				name: "manifest 非 UTF-8",
				root: fstest.MapFS{"pack.json": {Data: append([]byte(`{"format":1,"name":"`), 0xff, '"', '}')}},
				want: []string{"pack.json", "UTF-8"},
			},
			{
				name: "名称为空",
				root: fstest.MapFS{"pack.json": {Data: manifest(t, " \t\n")}},
				want: []string{"pack.json", "name"},
			},
			{
				name: "名称超过 128 字节",
				root: fstest.MapFS{"pack.json": {Data: manifest(t, strings.Repeat("界", 43))}},
				want: []string{"pack.json", "name", "128"},
			},
			{
				name: "manifest 超限",
				root: fstest.MapFS{"pack.json": {Data: bytes.Repeat([]byte{' '}, testManifestLimit+1)}},
				want: []string{"pack.json", "4096"},
			},
			{
				name: "manifest 不是普通文件",
				root: fstest.MapFS{"pack.json": {Data: validManifest, Mode: fs.ModeDir}},
				want: []string{"pack.json", "普通文件"},
			},
			{
				name: "PNG 损坏",
				root: fstest.MapFS{
					"pack.json":          {Data: validManifest},
					"textures/stone.png": {Data: []byte("not a png")},
				},
				want: []string{"测试材质包", "stone"},
			},
			{
				name: "PNG 尺寸错误",
				root: fstest.MapFS{
					"pack.json":          {Data: validManifest},
					"textures/stone.png": {Data: wrongSize},
				},
				want: []string{"测试材质包", "stone", "16x16"},
			},
			{
				name: "PNG 超限",
				root: fstest.MapFS{
					"pack.json":          {Data: validManifest},
					"textures/stone.png": {Data: bytes.Repeat([]byte{0}, testTextureLimit+1)},
				},
				want: []string{"测试材质包", "stone", "65536"},
			},
			{
				name: "PNG 不是普通文件",
				root: fstest.MapFS{
					"pack.json":          {Data: validManifest},
					"textures/stone.png": {Mode: fs.ModeDevice},
				},
				want: []string{"测试材质包", "stone", "普通文件"},
			},
		}

		for _, tt := range cases {
			t.Run(tt.name, func(t *testing.T) {
				registry := NewRegistry()
				err := applyPack(registry, tt.root)
				if err == nil {
					t.Fatal("applyPack() error = nil")
				}
				for _, want := range tt.want {
					if !strings.Contains(err.Error(), want) {
						t.Fatalf("error = %q，缺少上下文 %q", err, want)
					}
				}
			})
		}
	})

	t.Run("未知 manifest 字段只告警一次", func(t *testing.T) {
		var logs bytes.Buffer
		oldLogger := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
		t.Cleanup(func() { slog.SetDefault(oldLogger) })

		root := fstest.MapFS{
			"pack.json": {Data: []byte(`{"format":1,"name":"测试材质包","future":true}`)},
		}
		if err := applyPack(NewRegistry(), root); err != nil {
			t.Fatalf("applyPack() error = %v", err)
		}
		if got := strings.Count(logs.String(), "level=WARN"); got != 1 {
			t.Fatalf("WARN 数量 = %d，想要 1；日志：%s", got, logs.String())
		}
		if !strings.Contains(logs.String(), "future") {
			t.Fatalf("告警未包含未知字段名：%s", logs.String())
		}
	})

	t.Run("固定顺序读取已知材质且不打开未知文件", func(t *testing.T) {
		_, stone := solidPNG(t, 16, 16, color.NRGBA{R: 10, G: 20, B: 30, A: 255})
		_, glass := solidPNG(t, 16, 16, color.NRGBA{R: 40, G: 50, B: 60, A: 127})
		root := &recordingFS{FS: fstest.MapFS{
			"pack.json":             {Data: manifest(t, "顺序测试包")},
			"textures/glass.png":    {Data: glass},
			"textures/stone.png":    {Data: stone},
			"textures/unknown.png":  {Data: []byte("must not open")},
			"textures/ignored.meta": {Data: []byte("must not open")},
		}}
		if err := applyPack(NewRegistry(), root); err != nil {
			t.Fatalf("applyPack() error = %v", err)
		}
		want := []string{
			"pack.json",
			"textures/stone.png", "textures/dirt.png", "textures/grass_top.png", "textures/grass_side.png",
			"textures/bedrock.png", "textures/stone_brick.png", "textures/coal_ore.png", "textures/iron_ore.png",
			"textures/furnace.png", "textures/iron_block.png", "textures/chest.png", "textures/light_block.png",
			"textures/leaves.png", "textures/glass.png", "textures/cobblestone.png", "textures/smooth_stone.png",
			"textures/sand.png", "textures/gravel.png", "textures/oak_log_side.png", "textures/oak_log_top.png",
			"textures/oak_planks.png", "textures/brick.png", "textures/white_wool.png", "textures/roof_tile.png",
			"textures/clay.png", "textures/snow_top.png", "textures/snow_side.png", "textures/mossy_cobblestone.png",
			"textures/water.png", "textures/farmland_dry.png", "textures/farmland_wet.png", "textures/wheat_0.png",
			"textures/wheat_1.png", "textures/wheat_2.png", "textures/wheat_3.png", "textures/wheat_4.png",
			"textures/wheat_5.png", "textures/wheat_6.png", "textures/wheat_7.png",
		}
		if !reflect.DeepEqual(root.opened, want) {
			t.Fatalf("打开顺序 = %v，想要 %v", root.opened, want)
		}
	})

	t.Run("一个无效已知材质不会部分应用", func(t *testing.T) {
		_, stone := solidPNG(t, 16, 16, color.NRGBA{R: 77, G: 88, B: 99, A: 255})
		registry := NewRegistry()
		before := snapshotLayers(registry)
		root := fstest.MapFS{
			"pack.json":          {Data: manifest(t, "原子测试包")},
			"textures/stone.png": {Data: stone},
			"textures/dirt.png":  {Data: []byte("broken")},
		}
		if err := applyPack(registry, root); err == nil {
			t.Fatal("applyPack() error = nil")
		}
		assertLayersEqual(t, registry, before)
	})
}

func TestTextureBindingsCoverEveryLayerExactlyOnce(t *testing.T) {
	if got, want := len(textureBindings), int(layerCount); got != want {
		t.Fatalf("textureBindings 长度 = %d，想要 %d", got, want)
	}
	names := make(map[string]struct{}, len(textureBindings))
	layers := make([]bool, layerCount)
	for _, binding := range textureBindings {
		if binding.name == "" {
			t.Fatal("textureBindings 包含空名称")
		}
		if _, exists := names[binding.name]; exists {
			t.Fatalf("textureBindings 包含重复名称 %q", binding.name)
		}
		names[binding.name] = struct{}{}
		if binding.layer >= layerCount {
			t.Fatalf("%q 的 layer = %d，超出上界 %d", binding.name, binding.layer, layerCount)
		}
		if layers[binding.layer] {
			t.Fatalf("textureBindings 包含重复 layer %d", binding.layer)
		}
		layers[binding.layer] = true
	}
	for layer, present := range layers {
		if !present {
			t.Fatalf("textureBindings 缺少 layer %d", layer)
		}
	}
}

type recordingFS struct {
	fs.FS
	opened []string
}

func (f *recordingFS) Open(name string) (fs.File, error) {
	f.opened = append(f.opened, name)
	return f.FS.Open(name)
}

func manifest(t *testing.T, name string) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{"format": 1, "name": name})
	if err != nil {
		t.Fatalf("编码 manifest: %v", err)
	}
	return data
}

func solidPNG(t *testing.T, width, height int, fill color.NRGBA) ([]byte, []byte) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for i := 0; i < len(img.Pix); i += 4 {
		copy(img.Pix[i:i+4], []byte{fill.R, fill.G, fill.B, fill.A})
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatalf("编码 PNG: %v", err)
	}
	return bytes.Clone(img.Pix), encoded.Bytes()
}

func snapshotLayers(registry *Registry) [layerCount][]byte {
	var snapshot [layerCount][]byte
	for layer := range snapshot {
		snapshot[layer] = bytes.Clone(registry.LayerRGBA(layer))
	}
	return snapshot
}

func assertLayersEqual(t *testing.T, registry *Registry, want [layerCount][]byte) {
	t.Helper()
	for layer := range want {
		if got := registry.LayerRGBA(layer); !bytes.Equal(got, want[layer]) {
			t.Fatalf("layer %d 被修改", layer)
		}
	}
}
